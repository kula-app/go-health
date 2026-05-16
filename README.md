# kula-app/go-health

A small, reusable Go library for serving HTTP health-check endpoints that follow [RFC draft-inadarei-api-health-check-06](https://datatracker.ietf.org/doc/html/draft-inadarei-api-health-check-06).

`go-health` gives you three transport-agnostic endpoints: `/livez`, `/readyz`, and `/healthz`. Their semantics align with the Kubernetes API server convention. The library also provides a check authoring API that lets you mix database, object-store, and arbitrary custom checks without locking your service into a particular HTTP router or check SDK.

## Why

Most Go services end up reinventing the same three endpoints. `go-health` exists so you only build them once:

- Every response is `application/health+json` per the RFC, so dashboards and uptime tooling already know how to read it.
- `/livez` is process-local with no I/O, `/readyz` runs only the checks you mark critical for traffic, and `/healthz` is the comprehensive view. Liveness cannot cascade-restart your fleet because of a downstream blip.
- The core has no SDK dependencies and each built-in check lives in its own subpackage, so consumers only link the SDKs they actually use.
- The engine returns plain Go structs and does not import `net/http`, so you can exercise it directly in tests without spinning up an HTTP server.

## Install

```bash
go get github.com/kula-app/go-health
```

Requires Go 1.26+.

## Quickstart

```go
package main

import (
    "database/sql"
    "log"
    "net/http"

    "github.com/kula-app/go-health/adapters/http"
    "github.com/kula-app/go-health/checks/dbcheck"
    "github.com/kula-app/go-health/checks/s3check"
    "github.com/kula-app/go-health/core"

    "github.com/aws/aws-sdk-go-v2/service/s3"
)

func mountHealth(mux *http.ServeMux, db *sql.DB, s3client *s3.Client, bucket string) {
    eng := core.NewEngine("my-service", "My Service API")

    // Critical for serving traffic. Appears on /readyz and /healthz.
    eng.RegisterReadyCheck(dbcheck.New(db))

    // Informational only. Appears on /healthz.
    eng.RegisterCheck(s3check.New(s3client, bucket))

    mux.Handle("/livez",   healthhttp.LivezHandler(eng))
    mux.Handle("/readyz",  healthhttp.ReadyzHandler(eng))
    mux.Handle("/healthz", healthhttp.HealthzHandler(eng))
}
```

That's the whole integration. Three checks: a database ping (critical), an S3 head-bucket (informational), plus the implicit `system:time` heartbeat the engine emits on every response.

## Endpoints

| Endpoint   | Purpose                        | Checks executed                                                                                         | Aggregate `fail` HTTP                               |
| ---------- | ------------------------------ | ------------------------------------------------------------------------------------------------------- | --------------------------------------------------- |
| `/livez`   | "Should kubelet restart me?"   | Implicit `system:time` only                                                                             | 200 (no I/O, so cannot fail short of process death) |
| `/readyz`  | "Can I accept traffic?"        | `system:time` + every check registered via `RegisterReadyCheck`                                         | 503 on any registered-readiness check fail          |
| `/healthz` | "Comprehensive dashboard view" | `system:time` + every registered check (whether registered via `RegisterCheck` or `RegisterReadyCheck`) | 503 on any check fail                               |

All three return the same RFC schema. Only the set of checks executed differs.

**Why `/livez` runs no registered checks.** Liveness probe failure restarts the pod. If `/livez` pings the DB and the DB has a transient hiccup, kubelet restarts every replica simultaneously. A downstream blip then cascades into a self-inflicted outage. Liveness must be process-local.

**Why `/readyz` skips informational checks.** Readiness failure drops the pod from the Service endpoints. Mark only checks that genuinely block serving traffic with `RegisterReadyCheck`; everything else (object stores, third-party APIs that you can degrade past) belongs on `/healthz`.

**Why `/healthz` is the dashboard.** It is the human/observability endpoint, called rarely, so the cost of running every check is acceptable.

## Architecture

```
github.com/kula-app/go-health
├── core/              # Engine + RFC types. stdlib only.
├── adapters/
│   ├── http/          # net/http handlers
│   └── httprouter/    # julienschmidt/httprouter mounters
└── checks/
    ├── dbcheck/       # SQL ping
    └── s3check/       # S3 HeadBucket
```

The package separates concerns into three layers:

- `core/` defines `Check`, `Engine`, the RFC DTOs, and the status enum. Pure Go: no HTTP, no SDKs. The `Run*` methods return `HealthResponse` values.
- `adapters/` convert an engine into something a transport can serve. Owns `Content-Type`, status code mapping, and JSON encoding.
- `checks/<name>/` subpackages produce `core.Check` values with sensible defaults. Subpackages do not depend on each other, so consumers only link the SDKs they actually use.

## Built-in checks

### `checks/dbcheck`

A SQL ping. Works with `*sql.DB` directly (which satisfies the package's `Pinger` interface).

```go
c := dbcheck.New(db)
c.Name = "database:postgres"   // override defaults if you want
c.Timeout = 5 * time.Second
eng.RegisterReadyCheck(c)
```

`Run` returns `pass` when `PingContext` is nil-error, otherwise `fail` with the error message in `Output`.

### `checks/s3check`

A `HeadBucket` call against an `*s3.Client`. It verifies reachability and credentials in one cheap API call. It does not test write or delete permissions. IAM regressions on PUT or DELETE surface when real uploads fail rather than on the health endpoint.

```go
eng.RegisterCheck(s3check.New(s3client, "my-bucket"))
```

## Writing custom checks

`Check` is a struct, not an interface. Register a value with whatever `Run` closure you need:

```go
eng.RegisterReadyCheck(core.Check{
    Name:          "queue:depth",
    ComponentType: "datastore",
    Timeout:       2 * time.Second,
    Run: func(ctx context.Context) core.Result {
        depth, err := queue.Depth(ctx)
        if err != nil {
            return core.Result{Status: core.StatusFail, Output: err.Error()}
        }
        if depth > 10_000 {
            return core.Result{
                Status:        core.StatusWarn,
                ObservedValue: depth,
                ObservedUnit:  "messages",
                Output:        "queue depth above warning threshold",
            }
        }
        return core.Result{
            Status:        core.StatusPass,
            ObservedValue: depth,
            ObservedUnit:  "messages",
        }
    },
})
```

Conventions worth knowing:

- Per-check timeout is applied by the engine. Your `Run` receives a `context.Context` already bounded by `Check.Timeout`, so do not wrap it again.
- Checks run in parallel. The engine waits for all of them before responding; there is no fail-fast.
- Status aggregation is uniform across endpoints: any `fail` → top-level `fail`; else any `warn` → `warn`; else `pass`.
- `Register*` takes a value copy. Mutating the original `Check` after registration is a no-op.
- The name `system:time` is reserved. Registering it panics.
- On `pass`, the engine zeroes `Output` before serialization so a stale diagnostic from a successful check never leaks to the client.

## HTTP adapters

Two are shipped:

- `adapters/http` returns `http.HandlerFunc`. Mount them on `net/http`, `chi`, or any router that accepts the standard handler shape.
- `adapters/httprouter` provides convenience mounters for `github.com/julienschmidt/httprouter`. It is optional; only link it if you use httprouter.

Both adapters set `Content-Type: application/health+json` and translate the engine's status to the right HTTP code: `pass`/`warn` → 200, `fail` → 503.

## Logging

The engine writes to `slog.Default()` by default. Override with `core.WithLogger`:

```go
eng := core.NewEngine("my-service", "My Service API",
    core.WithLogger(slog.New(myHandler)),
)
```

The engine logs at `WARN` when aggregate status is `warn`, `ERROR` when `fail`, and stays silent on `pass` so it does not flood logs under the kubelet's default 10-second probe cadence.

## Security

RFC §6 warns that health-check responses can be used by attackers to map your infrastructure. The richest leak surface is `ComponentStatus.Output`, which by default carries the underlying error string from downstream calls. SQL drivers and AWS SDKs both leak host names, regions, ARNs, and request IDs in their error text.

`go-health`'s posture:

- The engine logs the full per-check failure via the configured logger. Check producers in this repo (`dbcheck`, `s3check`) currently put the raw error string into `Output`, and consumers are free to wrap them if they want different behavior.
- Network exposure is the consumer's responsibility. The package returns standard HTTP handlers and does not enforce auth or rate limits. The recommended posture is to keep probe endpoints (`/livez`, `/readyz`) reachable only from inside the cluster (the default with kubelet), and to keep `/healthz` either under the same restriction or behind authenticated ingress when used by external observability tooling.
- If you expose `/healthz` publicly, wrap your check producers so `Output` is replaced with a generic message such as "database unreachable" instead of the raw error.
