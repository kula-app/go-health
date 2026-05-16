package httprouter

import (
	"context"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/julienschmidt/httprouter"

	"github.com/kula-app/go-health/core"
)

// TestMount_registersAllThreeEndpoints wires an Engine through Mount
// and verifies that all three paths return responses with the
// expected Content-Type and a 2xx status code under the happy path.
// It also verifies that fetching a path the framework did not
// register returns the router's default 404.
func TestMount_registersAllThreeEndpoints(t *testing.T) {
	eng := core.NewEngine("svc", "desc")
	eng.RegisterReadyCheck(core.Check{
		Name: "critical", ComponentType: "datastore",
		Run: func(ctx context.Context) []core.Result { return []core.Result{{Status: core.StatusPass}} },
	})
	eng.RegisterCheck(core.Check{
		Name: "informational", ComponentType: "datastore",
		Run: func(ctx context.Context) []core.Result { return []core.Result{{Status: core.StatusPass}} },
	})

	router := httprouter.New()
	Mount(router, eng, Routes{
		Livez:   "/api/livez",
		Readyz:  "/api/readyz",
		Healthz: "/api/healthz",
	})

	srv := httptest.NewServer(router)
	defer srv.Close()

	for _, path := range []string{"/api/livez", "/api/readyz", "/api/healthz"} {
		resp, err := stdhttp.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode != stdhttp.StatusOK {
			t.Errorf("GET %s status = %d, want 200; body=%s", path, resp.StatusCode, body)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "application/health+json" {
			t.Errorf("GET %s Content-Type = %q, want application/health+json", path, ct)
		}
	}

	resp, err := stdhttp.Get(srv.URL + "/api/not-a-health-path")
	if err != nil {
		t.Fatalf("GET unknown path: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != stdhttp.StatusNotFound {
		t.Errorf("GET unknown path status = %d, want 404", resp.StatusCode)
	}
}

// TestMount_readyzFailReturns503 verifies that the readiness handler
// mounted through this adapter correctly propagates a 503 status
// code when a critical check fails. This is the canonical contract
// that kubelet relies on to drain traffic from a degraded pod.
func TestMount_readyzFailReturns503(t *testing.T) {
	eng := core.NewEngine("svc", "desc")
	eng.RegisterReadyCheck(core.Check{
		Name: "critical", ComponentType: "datastore",
		Run: func(ctx context.Context) []core.Result {
			return []core.Result{{Status: core.StatusFail, Output: "down"}}
		},
	})

	router := httprouter.New()
	Mount(router, eng, Routes{
		Livez:   "/livez",
		Readyz:  "/readyz",
		Healthz: "/healthz",
	})

	srv := httptest.NewServer(router)
	defer srv.Close()

	resp, err := stdhttp.Get(srv.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != stdhttp.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}

	resp, err = stdhttp.Get(srv.URL + "/livez")
	if err != nil {
		t.Fatalf("GET /livez: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != stdhttp.StatusOK {
		t.Errorf("livez status = %d, want 200 (must be unaffected by registered checks)", resp.StatusCode)
	}
}
