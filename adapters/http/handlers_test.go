package http

import (
	"context"
	"encoding/json"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/kula-app/go-health/core"
)

func newPassingEngine(t *testing.T) *core.Engine {
	t.Helper()
	e := core.NewEngine("svc", "desc")
	e.RegisterReadyCheck(core.Check{
		Name: "critical", ComponentType: "datastore",
		Run: func(ctx context.Context) []core.Result { return []core.Result{{Status: core.StatusPass}} },
	})
	e.RegisterCheck(core.Check{
		Name: "informational", ComponentType: "datastore",
		Run: func(ctx context.Context) []core.Result { return []core.Result{{Status: core.StatusPass}} },
	})
	return e
}

func TestLivezHandler_passReturns200(t *testing.T) {
	e := newPassingEngine(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/livez", nil)

	LivezHandler(e).ServeHTTP(rec, req)

	if rec.Code != stdhttp.StatusOK {
		t.Errorf("status code = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/health+json" {
		t.Errorf("Content-Type = %q, want application/health+json", ct)
	}
	var body core.HealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Status != core.StatusPass {
		t.Errorf("body.Status = %q, want pass", body.Status)
	}
}

func TestReadyzHandler_failReturns503(t *testing.T) {
	e := core.NewEngine("svc", "desc")
	e.RegisterReadyCheck(core.Check{
		Name: "broken", ComponentType: "datastore",
		Run: func(ctx context.Context) []core.Result {
			return []core.Result{{Status: core.StatusFail, Output: "boom"}}
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/readyz", nil)

	ReadyzHandler(e).ServeHTTP(rec, req)

	if rec.Code != stdhttp.StatusServiceUnavailable {
		t.Errorf("status code = %d, want 503", rec.Code)
	}
}

func TestHealthzHandler_returnsAllChecks(t *testing.T) {
	e := newPassingEngine(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/healthz", nil)

	HealthzHandler(e).ServeHTTP(rec, req)

	var body core.HealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	for _, want := range []string{"system:time", "critical", "informational"} {
		if _, ok := body.Checks[want]; !ok {
			t.Errorf("missing check key %q", want)
		}
	}
}
