package core

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestNewEngine_setsIdentity(t *testing.T) {
	e := NewEngine("svc-id", "my service")
	if e == nil {
		t.Fatal("NewEngine returned nil")
	}
	if e.serviceID != "svc-id" {
		t.Errorf("serviceID = %q, want %q", e.serviceID, "svc-id")
	}
	if e.description != "my service" {
		t.Errorf("description = %q, want %q", e.description, "my service")
	}
}

func TestNewEngine_defaultLogger(t *testing.T) {
	e := NewEngine("svc", "desc")
	if e.logger == nil {
		t.Error("default logger should not be nil")
	}
}

func TestNewEngine_withLogger(t *testing.T) {
	custom := slog.New(slog.NewTextHandler(io.Discard, nil))
	e := NewEngine("svc", "desc", WithLogger(custom))
	if e.logger != custom {
		t.Error("WithLogger should override default logger")
	}
}

func TestRegisterCheck_putsInHealthzOnly(t *testing.T) {
	e := NewEngine("svc", "desc")
	e.RegisterCheck(Check{Name: "foo", ComponentType: "system"})

	if len(e.healthz) != 1 {
		t.Errorf("healthz len = %d, want 1", len(e.healthz))
	}
	if len(e.readyz) != 0 {
		t.Errorf("readyz len = %d, want 0", len(e.readyz))
	}
}

func TestRegisterReadyCheck_putsInBothSets(t *testing.T) {
	e := NewEngine("svc", "desc")
	e.RegisterReadyCheck(Check{Name: "foo", ComponentType: "system"})

	if len(e.healthz) != 1 {
		t.Errorf("healthz len = %d, want 1", len(e.healthz))
	}
	if len(e.readyz) != 1 {
		t.Errorf("readyz len = %d, want 1", len(e.readyz))
	}
}

func TestRegisterCheck_copiesValue(t *testing.T) {
	e := NewEngine("svc", "desc")
	c := Check{Name: "foo", ComponentType: "system"}
	e.RegisterCheck(c)

	// Mutating c after registration must not affect what Engine holds.
	c.Name = "mutated"
	if e.healthz[0].Name != "foo" {
		t.Errorf("Engine should hold a copy; got %q", e.healthz[0].Name)
	}
}

func TestRegisterCheck_rejectsReservedSystemTime(t *testing.T) {
	e := NewEngine("svc", "desc")
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic when registering reserved name 'system:time'")
		}
	}()
	e.RegisterCheck(Check{Name: "system:time", ComponentType: "system"})
}

func TestRegisterReadyCheck_rejectsReservedSystemTime(t *testing.T) {
	e := NewEngine("svc", "desc")
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic when registering reserved name 'system:time'")
		}
	}()
	e.RegisterReadyCheck(Check{Name: "system:time", ComponentType: "system"})
}

func TestRunLivez_alwaysPassesWithSystemTime(t *testing.T) {
	e := NewEngine("svc-id", "my service")
	// Register checks that would fail if executed. Livez must ignore them.
	e.RegisterReadyCheck(Check{
		Name:          "would-fail",
		ComponentType: "system",
		Run: func(ctx context.Context) []Result {
			return []Result{{Status: StatusFail, Output: "should not run"}}
		},
	})

	resp := e.RunLivez(context.Background())

	if resp.Status != StatusPass {
		t.Errorf("Status = %q, want pass", resp.Status)
	}
	if resp.ServiceId != "svc-id" {
		t.Errorf("ServiceId = %q, want svc-id", resp.ServiceId)
	}
	if len(resp.Checks) != 1 {
		t.Errorf("len(Checks) = %d, want 1 (only system:time)", len(resp.Checks))
	}
	if _, ok := resp.Checks["system:time"]; !ok {
		t.Error("missing system:time entry")
	}
	if got := resp.Checks["system:time"][0].Status; got != StatusPass {
		t.Errorf("system:time status = %q, want pass", got)
	}
	if resp.Checks["system:time"][0].Time == "" {
		t.Error("system:time should have a non-empty Time")
	}
}

func TestRunReadyz_runsOnlyReadyChecks(t *testing.T) {
	e := NewEngine("svc", "desc")
	e.RegisterCheck(Check{
		Name: "informational", ComponentType: "datastore",
		Run: func(ctx context.Context) []Result { return []Result{{Status: StatusPass}} },
	})
	e.RegisterReadyCheck(Check{
		Name: "critical", ComponentType: "datastore",
		Run: func(ctx context.Context) []Result { return []Result{{Status: StatusPass}} },
	})

	resp := e.RunReadyz(context.Background())

	if _, ok := resp.Checks["critical"]; !ok {
		t.Error("readyz should include critical check")
	}
	if _, ok := resp.Checks["informational"]; ok {
		t.Error("readyz should NOT include informational check")
	}
	if _, ok := resp.Checks["system:time"]; !ok {
		t.Error("readyz should include system:time")
	}
}

func TestRunHealthz_runsAllChecks(t *testing.T) {
	e := NewEngine("svc", "desc")
	e.RegisterCheck(Check{
		Name: "informational", ComponentType: "datastore",
		Run: func(ctx context.Context) []Result { return []Result{{Status: StatusPass}} },
	})
	e.RegisterReadyCheck(Check{
		Name: "critical", ComponentType: "datastore",
		Run: func(ctx context.Context) []Result { return []Result{{Status: StatusPass}} },
	})

	resp := e.RunHealthz(context.Background())

	if _, ok := resp.Checks["critical"]; !ok {
		t.Error("healthz should include critical check")
	}
	if _, ok := resp.Checks["informational"]; ok != true {
		t.Error("healthz should include informational check")
	}
	if _, ok := resp.Checks["system:time"]; !ok {
		t.Error("healthz should include system:time")
	}
}

func TestAggregation(t *testing.T) {
	tests := []struct {
		name string
		a, b Status
		want Status
	}{
		{"pass+pass", StatusPass, StatusPass, StatusPass},
		{"pass+warn", StatusPass, StatusWarn, StatusWarn},
		{"warn+warn", StatusWarn, StatusWarn, StatusWarn},
		{"pass+fail", StatusPass, StatusFail, StatusFail},
		{"warn+fail", StatusWarn, StatusFail, StatusFail},
		{"fail+fail", StatusFail, StatusFail, StatusFail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := NewEngine("svc", "desc")
			a, b := tt.a, tt.b
			e.RegisterCheck(Check{Name: "a", ComponentType: "system", Run: func(ctx context.Context) []Result { return []Result{{Status: a}} }})
			e.RegisterCheck(Check{Name: "b", ComponentType: "system", Run: func(ctx context.Context) []Result { return []Result{{Status: b}} }})
			got := e.RunHealthz(context.Background()).Status
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRunHealthz_zeroesOutputOnPass(t *testing.T) {
	e := NewEngine("svc", "desc")
	e.RegisterCheck(Check{
		Name: "x", ComponentType: "system",
		Run: func(ctx context.Context) []Result {
			return []Result{{Status: StatusPass, Output: "leaked"}}
		},
	})
	resp := e.RunHealthz(context.Background())
	if got := resp.Checks["x"][0].Output; got != "" {
		t.Errorf("Output on pass = %q, want empty", got)
	}
}

// TestRunHealthz_zeroesAffectedEndpointsOnPass verifies the RFC §4.6
// suppression rule: AffectedEndpoints must be absent from a passing
// component, even if the check producer mistakenly returns it.
func TestRunHealthz_zeroesAffectedEndpointsOnPass(t *testing.T) {
	e := NewEngine("svc", "desc")
	e.RegisterCheck(Check{
		Name: "x", ComponentType: "system",
		Run: func(ctx context.Context) []Result {
			return []Result{{
				Status:            StatusPass,
				AffectedEndpoints: []string{"/leaked"},
			}}
		},
	})
	resp := e.RunHealthz(context.Background())
	if got := resp.Checks["x"][0].AffectedEndpoints; got != nil {
		t.Errorf("AffectedEndpoints on pass = %v, want nil", got)
	}
}

// TestRunHealthz_multiSubComponent verifies that a Check returning
// multiple Results lands as multiple ComponentStatus entries under the
// same map key, each carrying its own ComponentId. This is the
// Cassandra-style multi-node case from RFC §4 / §5.
func TestRunHealthz_multiSubComponent(t *testing.T) {
	e := NewEngine("svc", "desc")
	e.RegisterCheck(Check{
		Name: "cassandra:connections", ComponentType: "datastore",
		Run: func(ctx context.Context) []Result {
			return []Result{
				{ComponentId: "node-1", Status: StatusPass, ObservedValue: 42, ObservedUnit: "connections"},
				{ComponentId: "node-2", Status: StatusWarn, ObservedValue: 95, ObservedUnit: "connections", Output: "near pool limit"},
			}
		},
	})

	resp := e.RunHealthz(context.Background())

	got := resp.Checks["cassandra:connections"]
	if len(got) != 2 {
		t.Fatalf("len(checks[cassandra:connections]) = %d, want 2", len(got))
	}
	if got[0].ComponentId != "node-1" || got[1].ComponentId != "node-2" {
		t.Errorf("componentIds = %q,%q; want node-1,node-2", got[0].ComponentId, got[1].ComponentId)
	}
	if got[0].Status != StatusPass {
		t.Errorf("node-1 status = %q, want pass", got[0].Status)
	}
	if got[1].Status != StatusWarn || got[1].Output != "near pool limit" {
		t.Errorf("node-2 status/output = %q/%q, want warn/'near pool limit'", got[1].Status, got[1].Output)
	}
	// Aggregation: any warn (no fail) → overall warn.
	if resp.Status != StatusWarn {
		t.Errorf("overall status = %q, want warn", resp.Status)
	}
}

// TestRunHealthz_emptyResultsOmitsKey verifies that a Check returning
// an empty slice is interpreted as "nothing to report" and the key is
// omitted from the response entirely.
func TestRunHealthz_emptyResultsOmitsKey(t *testing.T) {
	e := NewEngine("svc", "desc")
	e.RegisterCheck(Check{
		Name: "nothing", ComponentType: "system",
		Run: func(ctx context.Context) []Result { return nil },
	})
	resp := e.RunHealthz(context.Background())
	if _, ok := resp.Checks["nothing"]; ok {
		t.Error("empty Result slice should omit the key from response")
	}
	if resp.Status != StatusPass {
		t.Errorf("overall status = %q, want pass (empty results contribute nothing)", resp.Status)
	}
}

// TestRunHealthz_nilRunSurfacesAsFail verifies the engine's defense
// against a half-built Check whose Run is nil: a single failing entry
// appears with a descriptive Output rather than crashing the process.
func TestRunHealthz_nilRunSurfacesAsFail(t *testing.T) {
	e := NewEngine("svc", "desc")
	e.RegisterCheck(Check{Name: "broken", ComponentType: "system"})
	resp := e.RunHealthz(context.Background())
	got := resp.Checks["broken"]
	if len(got) != 1 {
		t.Fatalf("len(checks[broken]) = %d, want 1", len(got))
	}
	if got[0].Status != StatusFail {
		t.Errorf("status = %q, want fail", got[0].Status)
	}
	if got[0].Output == "" {
		t.Error("nil-Run failure should have non-empty Output")
	}
}

func TestRunHealthz_appliesTimeout(t *testing.T) {
	e := NewEngine("svc", "desc")
	e.RegisterCheck(Check{
		Name: "slow", ComponentType: "system",
		Timeout: 50 * time.Millisecond,
		Run: func(ctx context.Context) []Result {
			<-ctx.Done()
			return []Result{{Status: StatusFail, Output: ctx.Err().Error()}}
		},
	})
	start := time.Now()
	resp := e.RunHealthz(context.Background())
	elapsed := time.Since(start)

	if elapsed > 200*time.Millisecond {
		t.Errorf("RunHealthz took %v, expected < 200ms (timeout should fire near 50ms)", elapsed)
	}
	if got := resp.Checks["slow"][0].Status; got != StatusFail {
		t.Errorf("status = %q, want fail", got)
	}
}

func TestRunHealthz_parallelExecution(t *testing.T) {
	e := NewEngine("svc", "desc")
	const checkDelay = 100 * time.Millisecond
	for _, name := range []string{"a", "b"} {
		e.RegisterCheck(Check{
			Name: name, ComponentType: "system",
			Run: func(ctx context.Context) []Result {
				time.Sleep(checkDelay)
				return []Result{{Status: StatusPass}}
			},
		})
	}
	start := time.Now()
	_ = e.RunHealthz(context.Background())
	elapsed := time.Since(start)

	if elapsed >= 2*checkDelay {
		t.Errorf("checks ran sequentially: %v >= %v", elapsed, 2*checkDelay)
	}
}

func TestRunHealthz_contextCancellationPropagates(t *testing.T) {
	e := NewEngine("svc", "desc")
	started := make(chan struct{})
	e.RegisterCheck(Check{
		Name: "long-running", ComponentType: "system",
		// No Timeout. Relies on the caller-provided context for cancellation.
		Run: func(ctx context.Context) []Result {
			close(started)
			<-ctx.Done()
			return []Result{{Status: StatusFail, Output: ctx.Err().Error()}}
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan HealthResponse, 1)
	go func() { done <- e.RunHealthz(ctx) }()

	<-started
	cancel()

	select {
	case resp := <-done:
		if got := resp.Checks["long-running"][0].Status; got != StatusFail {
			t.Errorf("status = %q, want fail (context canceled)", got)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("RunHealthz did not return after parent context cancellation")
	}
}

// TestEndToEnd_toggleCriticalCheck wires an Engine with one
// RegisterCheck stub (informational) and one RegisterReadyCheck stub
// (critical) and exercises all three Run methods under two regimes:
// first with both stubs passing, then with the critical stub flipped
// to fail. The assertion is that RunLivez is never affected by either
// stub's outcome (it must always be StatusPass), whereas RunReadyz
// and RunHealthz both transition to StatusFail once the critical stub
// fails. This is the canonical end-to-end check that validates the
// scoping contract: liveness must not depend on registered checks,
// and a critical check failure must propagate to both readiness and
// the dashboard endpoint.
func TestEndToEnd_toggleCriticalCheck(t *testing.T) {
	var criticalStatus = StatusPass

	e := NewEngine("svc", "desc")
	e.RegisterCheck(Check{
		Name: "informational", ComponentType: "datastore",
		Run: func(ctx context.Context) []Result { return []Result{{Status: StatusPass}} },
	})
	e.RegisterReadyCheck(Check{
		Name: "critical", ComponentType: "datastore",
		Run: func(ctx context.Context) []Result {
			return []Result{{Status: criticalStatus, Output: "boom"}}
		},
	})

	t.Run("all passing", func(t *testing.T) {
		criticalStatus = StatusPass
		ctx := context.Background()

		if got := e.RunLivez(ctx).Status; got != StatusPass {
			t.Errorf("livez = %q, want pass", got)
		}
		if got := e.RunReadyz(ctx).Status; got != StatusPass {
			t.Errorf("readyz = %q, want pass", got)
		}
		if got := e.RunHealthz(ctx).Status; got != StatusPass {
			t.Errorf("healthz = %q, want pass", got)
		}
	})

	t.Run("critical fails", func(t *testing.T) {
		criticalStatus = StatusFail
		ctx := context.Background()

		if got := e.RunLivez(ctx).Status; got != StatusPass {
			t.Errorf("livez = %q, want pass (must be unaffected by registered checks)", got)
		}
		if got := e.RunReadyz(ctx).Status; got != StatusFail {
			t.Errorf("readyz = %q, want fail", got)
		}
		if got := e.RunHealthz(ctx).Status; got != StatusFail {
			t.Errorf("healthz = %q, want fail", got)
		}
	})
}
