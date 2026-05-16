// Package httprouter is the julienschmidt/httprouter adapter for the
// core.Engine. It exposes a single Mount function that registers the
// three health endpoints on an existing *httprouter.Router under the
// paths supplied by the caller.
//
// Because the package name is "httprouter" and conflicts with the
// upstream third-party router package of the same name, consumers
// that also import "github.com/julienschmidt/httprouter" must alias
// one of the two. The conventional alias for this package is
// "healthhttprouter", for example:
//
//	import (
//	    "github.com/julienschmidt/httprouter"
//	    healthhttprouter "github.com/kula-app/go-health/adapters/httprouter"
//	)
//
// The adapter does not own the path values. It accepts a Routes
// struct so the consumer can route the health endpoints under any
// prefix that matches its existing URL layout. This keeps the
// framework free of opinions about whether endpoints live at
// /livez, /api/livez, /_health/livez, or anywhere else.
//
// Internally Mount delegates to the http.Handler factories in the
// sibling adapters/http subpackage, so the underlying serialization,
// status-code mapping, and response-body logic are shared with the
// stdlib net/http adapter and only registered once.
package httprouter

import (
	"net/http"

	"github.com/julienschmidt/httprouter"

	healthhttp "github.com/kula-app/go-health/adapters/http"
	"github.com/kula-app/go-health/core"
)

// Routes carries the three URL paths at which the health endpoints
// should be mounted. Every field is required. There are no defaults
// because the framework does not own the URL layout of its consumer.
type Routes struct {
	// Livez is the path the kubelet's liveness probe queries. A
	// failure on this endpoint causes the pod to be restarted, so
	// it must be reserved for in-process signals.
	Livez string

	// Readyz is the path the kubelet's readiness probe queries. A
	// failure on this endpoint causes the pod to be dropped from
	// the Service endpoints, draining traffic away.
	Readyz string

	// Healthz is the path human operators and observability tooling
	// query for the comprehensive dashboard view of the service's
	// health, including non-critical checks.
	Healthz string
}

// Mount registers the three health endpoints on the supplied
// *httprouter.Router using the paths in routes. The endpoints are
// always registered with the HTTP GET method, matching the RFC
// convention that health responses are idempotent reads.
//
// Mount is the single entry point a consumer needs to wire the
// health framework into an httprouter-based service. It must be
// called exactly once per Engine, before the server begins
// accepting traffic, because the underlying adapter handlers
// capture the Engine pointer at registration time.
func Mount(router *httprouter.Router, eng *core.Engine, routes Routes) {
	router.Handler(http.MethodGet, routes.Livez, healthhttp.LivezHandler(eng))
	router.Handler(http.MethodGet, routes.Readyz, healthhttp.ReadyzHandler(eng))
	router.Handler(http.MethodGet, routes.Healthz, healthhttp.HealthzHandler(eng))
}
