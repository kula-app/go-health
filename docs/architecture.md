# Architecture

This document explains _why_ `go-health` is structured the way it is, so future maintainers can judge edge cases without relitigating settled trade-offs. For the _what_ (API surface, usage), see [`README.md`](../README.md) and the package godoc.

## Layered structure

```
core/        # Engine + RFC types. stdlib only.
adapters/    # Transport-specific glue (HTTP today, more later).
checks/      # Per-component check producers, each in its own subpackage.
```

Each layer has one job:

- **`core/`** — defines `Check`, `Engine`, the RFC DTOs, and the status enum. Pure Go: no HTTP types, no SDK imports. The `Run*` methods take a `context.Context` and return a `HealthResponse` value. This is the only layer with engine logic.
- **`adapters/`** — converts an engine into something a transport can serve. Owns `Content-Type`, status code mapping, and JSON encoding. The HTTP adapter is the only file in the package that imports `net/http`. Future adapters (CLI, gRPC) plug into the same engine without touching core.
- **`checks/<name>/`** — each subpackage produces `core.Check` values with sensible defaults. Subpackages do not depend on each other. Consumers import only the checks they use, so a service that needs only the database check does not link the AWS SDK.

This split gives us:

- Engine tests don't need `httptest` — they call `RunHealthz(ctx)` and assert on the returned struct.
- HTTP adapter tests are small and focused — given an engine, verify `Content-Type`, status code, JSON body.
- Future adapters cost nothing to add — the same `core.Engine` instance can power multiple transports simultaneously.

## Design decisions

### Engine + adapter split, not a single HTTP-coupled type

The marginal cost is one extra small package. The structural benefits are large: the core is HTTP-agnostic and testable in isolation, and future adapters (CLI, gRPC) cost a few dozen lines each. A consumer that wants to expose health over both HTTP and a CLI subcommand uses the same engine for both.

Precedent: `prometheus/client_golang/promhttp` follows the same pattern (transport adapter as a separate package).

### `Check` is a struct with public fields, not an interface

Matches stdlib idioms (`http.Server`, `http.Client`). Three concrete benefits:

- Post-construction customization is one line: `c.Timeout = 5 * time.Second; engine.RegisterReadinessCheck(c)`. With an interface you would either have to wrap the value or re-implement the method set.
- No field-vs-method name collisions. Adding a future field is non-breaking; adding an interface method is.
- No need for a `CheckerFunc` adapter type.

The cost: `Check.Run` is a function field rather than a method. In practice this reads identically and trades slightly less type-system formality for a much smaller API surface.

### Three registration methods, one per probe scope

`RegisterLivenessCheck` (livez + readyz + healthz), `RegisterReadinessCheck` (readyz + healthz), and `RegisterHealthCheck` (healthz only) form a strict cascade: the more severe the probe, the smaller the set of checks that participates. A functional option (`Register(c, WithReadyness())`) would be over-engineering — three methods are self-documenting at the call site:

```go
eng.RegisterLivenessCheck(deadlockWatchdog)    // obvious: restart-worthy
eng.RegisterReadinessCheck(dbcheck.New(db))    // obvious: drain-worthy
eng.RegisterHealthCheck(s3check.New(c, bucket)) // obvious: dashboard-only
```

The cascade direction is deliberate: a liveness failure should also fail readiness (drain the pod immediately rather than wait for kubelet's next restart), and `/healthz` should show every check regardless of severity. The alternative — scope-independent sets where a liveness failure does not appear on `/healthz` — would let operators look at the dashboard during an incident and miss the most important signal.

### Probe semantics drive the registration choice

Probe semantics in Kubernetes are not interchangeable:

- **Liveness probe failure restarts the pod.** Running an I/O check via `RegisterLivenessCheck` means a transient downstream blip can restart every replica simultaneously — a downstream hiccup becomes a self-inflicted outage. The godoc on `RegisterLivenessCheck` warns explicitly. The method is reserved for in-process signals that a restart can fix: deadlock detectors, goroutine-leak watchdogs, corrupt internal caches.
- **Readiness probe failure drops the pod from the Service endpoints.** Running a non-critical check via `RegisterReadinessCheck` (e.g. an object store the service can degrade past) means a non-critical dependency outage stops 100% of traffic landing. Such checks belong on `RegisterHealthCheck`.

`/healthz` is the dashboard endpoint, called rarely by humans or observability tooling. Running every check there is acceptable at low request rates.

`RegisterHealthCheck` exists so that a check can appear on the dashboard without also gating traffic on it. Without it, every observable check would have to gate readiness.

### Liveness-check guidance is godoc-only

`RegisterLivenessCheck` is foot-gun-shaped: misuse turns a transient downstream blip into a synchronized restart of every replica. The framework documents the risk prominently in the godoc but does not enforce it at compile or run time, because there is no programmatic way to distinguish "in-process check" from "external I/O check" — both are arbitrary `func(ctx) []Result` values. Forcing producers to declare their I/O nature would either be unverifiable metadata (easy to lie about) or a typed interface (which contradicts the [struct-with-public-fields choice](#check-is-a-struct-with-public-fields-not-an-interface)).

We rely on the godoc warning surfacing in editor tooling at the moment a developer writes `eng.RegisterLivenessCheck(...)`. That covers the common case (writing the call site) without adding the runtime cost of inspecting every probe.

### S3 check is `HeadBucket`, not a write/read/delete round-trip

`HeadBucket` is one S3 API call that verifies reachability and credentials. A full PUT/GET/DELETE round-trip on every probe (the original implementation) costs real money — at the kubelet's default 10 s probe cadence per replica, the bill is non-trivial — for almost no additional signal: an IAM regression on PUT/DELETE will surface when real uploads fail, not when the health probe runs.

Trade-off: we don't catch write-permission regressions on the health endpoint. Acceptable, given the cost reduction.

### Per-check timeout owned by the engine, not by the check's `Run` closure

When the engine sees `Check.Timeout > 0`, it wraps the request context with `context.WithTimeout(ctx, Timeout)` before calling `Run`. `Run` receives an already-bounded context.

This matters because `Check` fields are mutable. A consumer can do:

```go
c := dbcheck.New(db)
c.Timeout = 5 * time.Second
eng.RegisterReadinessCheck(c)
```

If `Run` owned the timeout (built it into the closure at construction time), the override would silently not take effect. By the engine applying it, the override always wins.

### No `Cache-Control` header

RFC §9 suggests one. We omit it because:

- Probe endpoints need fresh data — caching `/livez` or `/readyz` defeats their purpose.
- The cost savings from caching `/healthz` are marginal at expected request rates.
- Adding it later is non-breaking; removing it would be.

### Implicit `system:time` heartbeat

Every `HealthResponse` includes a `system:time` entry with a fresh RFC3339-Nano timestamp. The name is reserved — `Register*` panics if a consumer tries to register it.

Why it exists: it gives every response at least one entry, which means clients can always extract a "what time does the server think it is" datum. Useful for debugging clock skew without adding a check.

Why it panics on collision: silent overwrite would be a debugging trap. A panic at registration time is loud and immediate.

### Output and AffectedEndpoints suppression on pass

When `Result.Status == StatusPass`, the engine zeros both `ComponentStatus.Output` and `ComponentStatus.AffectedEndpoints` before serialization. Combined with `omitempty` JSON tags, this guarantees those keys are absent from the JSON for passing observations.

The motivation is RFC §3.5, §4.6, and §4.8 ("SHOULD be omitted on pass") and a defensive concern: an observation that returns stale diagnostics from a successful retry should not leak them to the client. Future maintainers must preserve this rule when adding new fields with the same RFC suppression semantics.

### `Check.Run` returns a slice

RFC §4 keys each check entry to an array of sub-component objects, supporting the multi-replica case directly (the Cassandra cluster in the spec's §5 example reports one entry per node). `Check.Run` therefore returns `[]Result` rather than a single `Result`.

A single-instance dependency (a primary database, one S3 bucket) returns a one-element slice. A replicated dependency fans out internally and returns one `Result` per replica, each with its own `ComponentId`. The engine flattens all results from one Check under that Check's `Name` key in the response map.

Trade-offs:

- A single `Check.Timeout` bounds the entire fan-out, not each replica individually. Within `Run`, the producer is responsible for honoring `ctx.Done()` across whatever per-replica calls it issues.
- A `Run` returning an empty slice means "the check has nothing to report" and the engine omits the key from the response entirely. This is the correct shape for checks whose target population can legitimately be empty (zero-replica services), and is preferable to forcing a sentinel "no replicas" entry.
- `nil Run` still surfaces as a single failing entry with a descriptive `Output`, so a half-built `Check` cannot crash the engine.

Why not two methods (single-result `Run` and multi-result `RunAll`)? Two function fields would double the API surface and force every producer and adapter to handle both shapes. The slice-only form is uniform: a single-instance check writes `return []Result{{...}}` (a small ergonomic cost) and gets the same semantics as a fan-out check.

### Subpackage layout matches the (now-realized) module structure

The package was developed under `internal/health/` inside a parent service. The subpackage layout was chosen so that extraction would be "move directory, change import paths" — no API or layout changes. That extraction is now done; the layout is preserved for the same reason it was chosen: it reads naturally as a top-level Go module.

## References

- RFC draft-inadarei-api-health-check-06: <https://datatracker.ietf.org/doc/html/draft-inadarei-api-health-check-06>
- Kubernetes API server health endpoints: <https://kubernetes.io/docs/reference/using-api/health-checks/>
- prometheus/client_golang/promhttp (precedent for the transport-adapter-as-separate-package pattern)
