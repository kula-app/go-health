// Package core implements the engine, types, and DTOs for the reusable
// health-check framework. It depends only on the standard library, takes
// no opinion about HTTP transport (see the adapters/http subpackage for
// that), and is laid out so that a future extraction to a standalone
// module is a directory move rather than a redesign.
//
// The response format conforms to RFC draft-inadarei-api-health-check-06,
// "Health Check Response Format for HTTP APIs". The framework adds its
// own decisions on top, including the split between liveness, readiness,
// and dashboard endpoints, the reserved "system:time" check name, and
// strict aggregation rules where any failing component fails the whole
// response. These decisions are documented on the relevant types.
package core

import "net/http"

// Status describes the health of a service or one of its components.
// It is the value of the "status" field in the RFC response payload,
// and it determines the HTTP status code returned by the handler.
//
// Only three values are produced by this package: StatusPass, StatusWarn,
// and StatusFail. The status field is case-insensitive on the consumer
// side, but this package always emits lowercase canonical values.
//
// The status applies to the entire component or service that the response
// represents. Load balancers, service discovery, and other infrastructure
// rely on the HTTP status code returned alongside it to make routing
// decisions, so the HTTPStatusCode method must be the authority for the
// response code rather than a hard-coded constant.
type Status string

const (
	// StatusPass indicates that the service is healthy. All checks are
	// passing, the service is fully operational, and no issues have been
	// detected. Use this when every dependency reachable from the running
	// process is responding as expected.
	//
	// Other implementations accept the aliases "ok" (Node Terminus) and
	// "up" (Spring Boot) as inputs. This package always emits "pass" and
	// expects consumers to canonicalize on read.
	//
	// HTTPStatusCode returns 200 (OK) for this value.
	StatusPass Status = "pass"

	// StatusWarn indicates that the service is healthy but has concerns.
	// The service is still operational and willing to serve traffic, but
	// one or more non-critical components are degraded, performance is
	// below optimal, or some features may be limited. The response body
	// should include diagnostic information in the per-component Output
	// field for any check that contributed to the warning.
	//
	// HTTPStatusCode returns 200 (OK) for this value, which is why warn
	// must not be used for conditions that should drain traffic from the
	// instance. Reach for StatusFail instead when traffic must move away.
	StatusWarn Status = "warn"

	// StatusFail indicates that the service is unhealthy. Critical
	// components are failing, essential dependencies are unavailable, or
	// the service cannot serve requests properly. This value drains
	// traffic away from the instance when used on a Kubernetes readiness
	// probe, and it restarts the pod when used on a liveness probe.
	//
	// Other implementations accept the aliases "error" (Node Terminus)
	// and "down" (Spring Boot) as inputs. This package always emits
	// "fail" and expects consumers to canonicalize on read.
	//
	// HTTPStatusCode returns 503 (Service Unavailable) for this value.
	StatusFail Status = "fail"
)

// HTTPStatusCode returns the HTTP status code that should accompany a
// response carrying this Status. The mapping is:
//
//	StatusPass, StatusWarn  → 200 (OK), the service is available
//	StatusFail              → 503 (Service Unavailable)
//	any other value         → 200 (OK)
//
// The "any other value" branch exists so that an unknown status value,
// for example a typo in a custom check, cannot accidentally take a
// service out of rotation. Unknown statuses are treated as non-critical.
// Use this method when writing the response code on the wire so that the
// code stays in sync with the value of the status field in the body.
func (s Status) HTTPStatusCode() int {
	switch s {
	case StatusFail:
		return http.StatusServiceUnavailable
	default:
		return http.StatusOK
	}
}
