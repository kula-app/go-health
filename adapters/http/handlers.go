// Package http is the HTTP adapter for the core.Engine. It exposes
// three http.HandlerFunc factories, one per RFC endpoint, that
// serialize the engine's HealthResponse as application/health+json
// and emit the appropriate HTTP status code on the wire.
//
// The adapter intentionally has no knowledge of routing. Each factory
// returns a plain net/http handler that can be mounted on any router
// the consumer prefers (the stdlib mux, httprouter, chi, gorilla, and
// so on). This keeps the core engine decoupled from any specific
// transport library.
//
// The package name is "http" so the import path reads naturally as
// "adapters/http". Consumers that also import the standard library's
// net/http must alias one of the two imports to avoid a name
// collision. The conventional alias for this package is "healthhttp",
// for example:
//
//	import (
//	    "net/http"
//	    healthhttp "github.com/kula-app/go-health/adapters/http"
//	)
//
// The adapter does not authenticate, rate-limit, or cache responses.
// Apply those concerns at the router level when the endpoints are
// exposed to anyone other than the kubelet.
package http

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/kula-app/go-health/core"
)

// contentType is the media type the adapter sets on every response.
// The value is the one registered by RFC draft-inadarei-api-health-check
// for machine-readable health responses.
const contentType = "application/health+json"

// LivezHandler returns an http.HandlerFunc that invokes e.RunLivez with
// the request context, serializes the result as application/health+json,
// and writes it with status code 200. Mount the returned handler on the
// path that kubelet's liveness probe queries (typically /api/livez or
// equivalent in the service's route layout). The handler is safe to
// reuse across requests and across goroutines.
func LivezHandler(e *core.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		write(w, r, e.RunLivez(r.Context()))
	}
}

// ReadyzHandler returns an http.HandlerFunc that invokes e.RunReadyz
// with the request context and writes the result. The HTTP status code
// is derived from the aggregated response status, so a failing
// readiness check produces a 503 and causes kubelet to drop the pod
// from the Service endpoints until the next successful probe. Mount
// the returned handler on the path kubelet's readiness probe queries.
func ReadyzHandler(e *core.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		write(w, r, e.RunReadyz(r.Context()))
	}
}

// HealthzHandler returns an http.HandlerFunc that invokes e.RunHealthz
// with the request context and writes the full RFC response, including
// every registered check. Use this handler for the dashboard endpoint
// that observability tooling and human operators query. Do not point
// kubelet at it, because /healthz may include expensive checks such as
// storage round-trips that should not run on every probe cycle.
func HealthzHandler(e *core.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		write(w, r, e.RunHealthz(r.Context()))
	}
}

// write serializes resp as application/health+json with the
// corresponding HTTP status code derived from resp.Status.
//
// Encoding is performed into an in-memory buffer first so that an
// encode error does not produce a partial response on the wire. In
// the unlikely event that encoding fails (the framework's response
// type is JSON-safe by construction, so this should only happen
// under extreme conditions such as a panic in a custom ObservedValue
// implementation), the handler responds with 500 Internal Server
// Error and a plain-text body rather than emitting a half-written
// document with a misleading 200 or 503 status.
//
// Write failures on the response stream (for example, a client that
// disconnected mid-response) are surfaced via slog.Default() rather
// than returned. The handler cannot do anything useful to recover
// from a half-sent response, but operators benefit from seeing the
// failure in logs.
func write(w http.ResponseWriter, r *http.Request, resp core.HealthResponse) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(resp); err != nil {
		http.Error(w, "failed to encode health response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(resp.Status.HTTPStatusCode())
	if _, err := w.Write(buf.Bytes()); err != nil {
		slog.Default().WarnContext(r.Context(), "health adapter: failed to write response body", "error", err)
	}
}
