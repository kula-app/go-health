package core

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Engine registers checks and produces HealthResponse values on demand.
// Construct one per service using NewEngine, register checks with
// RegisterCheck or RegisterReadyCheck, then expose its Run methods
// through whatever transport adapter your service uses, typically the
// HTTP adapter in the adapters/http subpackage.
//
// An Engine is safe for concurrent calls to the Run methods after
// registration is complete. The Engine is not safe for concurrent
// Register calls or for concurrent Register and Run, because the
// internal slices are mutated without locking. Register every check
// during process startup, before the HTTP server begins accepting
// requests, and the package is race-free in practice.
type Engine struct {
	serviceID   string
	description string
	logger      *slog.Logger
	healthz     []Check // every check the consumer has registered; runs on /healthz
	readyz      []Check // the critical subset; runs on /readyz and /healthz
}

// systemTimeCheckName is the reserved key the Engine uses for its
// implicit per-response heartbeat entry. The Engine populates this
// entry on every RunLivez, RunReadyz, and RunHealthz call. The
// registration methods reject a Check with this Name so that a
// caller cannot silently collide with the framework's bookkeeping.
const systemTimeCheckName = "system:time"

// NewEngine constructs an Engine that carries the given service identity.
// The serviceID and description appear verbatim in every HealthResponse
// the Engine produces, so they should be stable strings that identify
// the service to dashboards and operators. The serviceID corresponds to
// the RFC response field "serviceId" and the description corresponds to
// the RFC response field "description". See the README for the recommended
// values to use for each.
//
// Apply Options to customize behavior. The most common option is
// WithLogger, which overrides the default slog.Default() logger so that
// warn-level and error-level health events use the same logger as the
// rest of your service.
//
// The returned Engine has no checks registered. Call RegisterCheck and
// RegisterReadyCheck to populate it before exposing the Run methods.
func NewEngine(serviceID, description string, opts ...Option) *Engine {
	e := &Engine{
		serviceID:   serviceID,
		description: description,
		logger:      slog.Default(),
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// RegisterCheck adds c to the dashboard-only set, meaning c runs on
// RunHealthz but not on RunReadyz. Use this method for checks that are
// useful for human operators looking at the dashboard but that should
// not gate traffic when they fail, for example a storage round-trip
// against an S3 bucket where the service can still serve most
// endpoints even when storage is unhealthy.
//
// The Engine takes a value copy of c. Mutating the local Check after
// calling RegisterCheck has no effect on what the Engine holds, which
// is why mutating c.Timeout or c.Name to override producer defaults
// must happen before this call.
//
// RegisterCheck panics when c.Name equals the reserved value
// "system:time", because that key is owned by the framework. Use any
// other name. The panic is a hard programmer error rather than a
// returned sentinel because misuse would silently corrupt the
// response shape, and surfacing it at startup is safer than letting
// it slip into production.
//
// RegisterCheck is not safe to call concurrently with itself, with
// RegisterReadyCheck, or with the Engine's Run methods. Register
// every check during process startup, before exposing the engine.
func (e *Engine) RegisterCheck(c Check) {
	if c.Name == systemTimeCheckName {
		panic(`health/core: check name "system:time" is reserved by the framework`)
	}
	e.healthz = append(e.healthz, c)
}

// RegisterReadyCheck adds c to both the readiness set and the dashboard
// set, meaning c runs on RunReadyz and on RunHealthz. Use this method
// for checks that must succeed for the service to accept traffic, for
// example a database ping where every endpoint depends on the database.
// A fail Result from c will make RunReadyz produce a response with
// StatusFail, which the HTTP adapter maps to a 503, which in turn
// signals kubelet to drop the pod from the Service endpoints.
//
// The same value-copy semantics and concurrency rules as RegisterCheck
// apply. RegisterReadyCheck also panics when c.Name equals "system:time".
func (e *Engine) RegisterReadyCheck(c Check) {
	if c.Name == systemTimeCheckName {
		panic(`health/core: check name "system:time" is reserved by the framework`)
	}
	e.healthz = append(e.healthz, c)
	e.readyz = append(e.readyz, c)
}

// RunLivez returns a HealthResponse that reports only the framework's
// implicit "system:time" entry. No registered checks are executed.
// Status is always StatusPass.
//
// This is the response that the HTTP adapter's LivezHandler serializes
// to kubelet's liveness probe. The contract that liveness must honor is
// "is this process alive enough to make forward progress at all", which
// is answered by the fact that this function returned at all. Running
// any external dependency check here would risk restarting the pod on
// transient downstream issues, so the function intentionally ignores
// everything that has been registered with RegisterCheck or
// RegisterReadyCheck.
//
// The ctx argument is accepted for symmetry with the other Run methods,
// but RunLivez does not perform I/O so the context is not consulted.
func (e *Engine) RunLivez(ctx context.Context) HealthResponse {
	return HealthResponse{
		Status:      StatusPass,
		ServiceId:   e.serviceID,
		Description: e.description,
		Checks: map[string][]ComponentStatus{
			systemTimeCheckName: {e.systemTimeStatus()},
		},
	}
}

// systemTimeStatus produces the framework's implicit "system:time"
// component entry. The Engine attaches one of these to every response
// regardless of which Run method produced it, so consumers can rely on
// the presence of a non-empty Time field as a heartbeat indicator even
// when no other checks are registered.
func (e *Engine) systemTimeStatus() ComponentStatus {
	return ComponentStatus{
		ComponentType: "system",
		Status:        StatusPass,
		Time:          time.Now().UTC().Format(time.RFC3339Nano),
	}
}

// runChecks executes the supplied checks in parallel, applies each
// Check's Timeout to its execution context, converts the resulting
// Results into ComponentStatus values, and returns them grouped by
// check name. The framework's implicit "system:time" entry is always
// included in the returned map alongside the requested checks.
//
// Each Check.Run produces a slice of Result values: a single-instance
// dependency reports one observation, a replicated dependency reports
// one observation per replica. All observations from one Check appear
// under the Check's Name key in the returned map, matching the RFC's
// per-component-array shape. A Check that returns an empty slice is
// omitted from the response entirely.
//
// Parallel execution is unconditional. Even when only one check is
// supplied, runChecks still launches a goroutine for it so that the
// concurrency contract is uniform: a single slow check cannot block
// faster siblings from completing. The function returns only after
// every goroutine has finished, which means the total wall time is
// bounded by the slowest individual check.
//
// runChecks does not enforce its own overall deadline. Callers that
// want to cap end-to-end probe time should pass a context with a
// deadline, which propagates into each check through runOne.
func (e *Engine) runChecks(ctx context.Context, checks []Check) map[string][]ComponentStatus {
	out := make(map[string][]ComponentStatus, len(checks)+1)
	out[systemTimeCheckName] = []ComponentStatus{e.systemTimeStatus()}

	if len(checks) == 0 {
		return out
	}

	results := make([][]ComponentStatus, len(checks))
	var wg sync.WaitGroup
	for i, c := range checks {
		wg.Add(1)
		go func(i int, c Check) {
			defer wg.Done()
			results[i] = e.runOne(ctx, c)
		}(i, c)
	}
	wg.Wait()

	for i, c := range checks {
		if len(results[i]) == 0 {
			continue
		}
		out[c.Name] = append(out[c.Name], results[i]...)
	}
	return out
}

// runOne is the per-check execution path. It wraps parentCtx with a
// per-check deadline when c.Timeout is positive, invokes c.Run, and
// converts each returned Result into a ComponentStatus carrying the
// Check's component metadata, the current timestamp, and any
// diagnostics from the Result.
//
// runOne defends against two misuse modes. First, when c.Run is nil,
// runOne returns a single failing ComponentStatus with a descriptive
// Output rather than dereferencing the nil function value, so a
// half-built Check cannot crash the process. Second, when an
// observation's Status is StatusPass, runOne clears the Output and
// AffectedEndpoints fields unconditionally so the corresponding keys
// disappear from the serialized response, honoring RFC §3.5, §4.6, and
// §4.8. A misbehaving check producer that returns these fields even on
// success cannot leak them to the client.
//
// An empty slice from c.Run is propagated as an empty slice, allowing
// runChecks to omit the Check from the response entirely. This is the
// correct shape for "the check ran but had nothing to report".
func (e *Engine) runOne(parentCtx context.Context, c Check) []ComponentStatus {
	ctx := parentCtx
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(parentCtx, c.Timeout)
		defer cancel()
	}

	var results []Result
	if c.Run == nil {
		results = []Result{{Status: StatusFail, Output: "check has no Run function"}}
	} else {
		results = c.Run(ctx)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	out := make([]ComponentStatus, 0, len(results))
	for _, r := range results {
		cs := ComponentStatus{
			ComponentId:       r.ComponentId,
			ComponentType:     c.ComponentType,
			Status:            r.Status,
			ObservedValue:     r.ObservedValue,
			ObservedUnit:      r.ObservedUnit,
			Time:              now,
			Output:            r.Output,
			AffectedEndpoints: r.AffectedEndpoints,
			Links:             r.Links,
		}
		if cs.Status == StatusPass {
			cs.Output = ""
			cs.AffectedEndpoints = nil
		}
		out = append(out, cs)
	}
	return out
}

// aggregate reduces a checks map to a single overall Status using the
// framework's strict rule: any failing component fails the whole
// response, any warning component (in the absence of failures) warns
// the whole response, and only when every component passes does the
// overall response pass.
//
// The rule is deliberately conservative. A consumer that wants to
// treat a partial failure as a warning instead can override the
// returned response Status before serializing it, but the default
// behavior preserves the load-balancer-friendly invariant that any
// failing dependency drains the instance.
func aggregate(checks map[string][]ComponentStatus) Status {
	overall := StatusPass
	for _, list := range checks {
		for _, cs := range list {
			switch cs.Status {
			case StatusFail:
				return StatusFail
			case StatusWarn:
				overall = StatusWarn
			}
		}
	}
	return overall
}

// RunReadyz executes every Check that was registered with
// RegisterReadyCheck and returns the aggregated HealthResponse. The
// response status is StatusFail when any one of those checks failed,
// StatusWarn when at least one warned (and none failed), and
// StatusPass when every check passed. The framework's implicit
// "system:time" entry is always present in the response.
//
// This is the response that the HTTP adapter's ReadyzHandler
// serializes for kubelet's readiness probe. A 503 response causes
// kubelet to drop the pod from the Service endpoints until the next
// successful probe, which is the right behavior when a critical
// dependency such as the database is unreachable.
//
// RunReadyz emits a warn-level log entry when the aggregated status is
// StatusWarn and an error-level entry when it is StatusFail, both via
// the Engine's configured logger. Pass results are logged at nothing,
// because under kubelet's default ten-second probe cadence passing logs
// would dominate the operational signal.
func (e *Engine) RunReadyz(ctx context.Context) HealthResponse {
	checks := e.runChecks(ctx, e.readyz)
	status := aggregate(checks)
	e.emitLog(ctx, "readiness", status, len(e.readyz))
	return HealthResponse{
		Status:      status,
		ServiceId:   e.serviceID,
		Description: e.description,
		Checks:      checks,
	}
}

// RunHealthz executes every registered check, whether registered via
// RegisterCheck or RegisterReadyCheck, and returns the aggregated
// HealthResponse. The aggregation rule is identical to RunReadyz, but
// the set of checks executed is the superset.
//
// This is the response that the HTTP adapter's HealthzHandler
// serializes. It is intended for human operators and dashboards rather
// than for kubelet probes, because it can include expensive checks
// such as storage round-trips that should not run on every probe
// cycle. The aggregated logging behavior is the same as RunReadyz.
func (e *Engine) RunHealthz(ctx context.Context) HealthResponse {
	checks := e.runChecks(ctx, e.healthz)
	status := aggregate(checks)
	e.emitLog(ctx, "health", status, len(e.healthz))
	return HealthResponse{
		Status:      status,
		ServiceId:   e.serviceID,
		Description: e.description,
		Checks:      checks,
	}
}

// emitLog writes a single log line at WARN level for StatusWarn
// aggregates and at ERROR level for StatusFail aggregates. The kind
// argument labels the originating Run method ("readiness" or
// "health") so an operator scanning logs can tell which endpoint
// produced the entry.
//
// Pass results emit nothing so that the steady-state probe cadence
// does not produce six lines per minute per replica per probe.
func (e *Engine) emitLog(ctx context.Context, kind string, status Status, checksCount int) {
	switch status {
	case StatusWarn:
		e.logger.WarnContext(ctx, kind+" check completed with warnings",
			"status", string(status), "checks_count", checksCount)
	case StatusFail:
		e.logger.ErrorContext(ctx, kind+" check completed with failures",
			"status", string(status), "checks_count", checksCount)
	}
}
