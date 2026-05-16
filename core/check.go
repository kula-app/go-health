package core

import (
	"context"
	"time"
)

// Result is what a Check.Run produces when it has finished inspecting
// whatever component the check is responsible for. It is intentionally
// minimal: the caller produces the status and optional diagnostics, and
// the Engine fills in metadata such as time, component type, and the
// per-check Name when assembling the response payload.
//
// Output carries free-form diagnostic text for fail or warn results.
// Concrete check producers typically set Output to the underlying
// error's message. The Engine guarantees that Output is cleared before
// serialization whenever Status is StatusPass, so callers do not need
// to defend against the case where a successful check accidentally
// returns a stale Output value.
//
// ObservedValue and ObservedUnit are optional and may be left zero.
// They are intended for quantitative checks (for example, current
// connection count or response time in milliseconds) where the consumer
// of the health response wants to graph the value over time.
type Result struct {
	// Status is the outcome of running the check. It must be one of
	// StatusPass, StatusWarn, or StatusFail. The Engine aggregates these
	// per-check statuses into the overall response status using strict
	// rules: any fail wins, then any warn, otherwise pass.
	Status Status

	// ObservedValue holds a measured datum for quantitative checks. It
	// may be any JSON-encodable value (string, number, boolean, object,
	// or array). Leave it nil for binary checks where only the Status
	// matters, such as a database ping that either succeeds or fails.
	ObservedValue any

	// ObservedUnit clarifies the unit of measurement for ObservedValue,
	// for example "ms" for milliseconds, "%" for utilization, or "s" for
	// seconds of uptime. Set it whenever ObservedValue is set. Use a
	// well-known unit name from a standards source where possible.
	ObservedUnit string

	// Output is raw diagnostic text describing a fail or warn condition.
	// Concrete check producers typically populate this with the
	// underlying error's message. The Engine omits Output from the
	// serialized response when Status is StatusPass, so a check that
	// successfully completed never leaks an Output value to the client.
	Output string
}

// Check is the unit of registration with the Engine. Construct one
// using a concrete producer such as dbcheck.New or build one inline as
// a struct literal for custom checks. All fields are mutable after
// construction so consumers can override the producer's defaults
// without re-implementing the check from scratch:
//
//	c := dbcheck.New(db)
//	c.Name = "database:postgres-primary"
//	c.Timeout = 5 * time.Second
//	engine.RegisterReadyCheck(c)
//
// The Engine takes a value copy when the check is registered. Mutating
// the local Check value after registration has no effect on what the
// Engine holds.
type Check struct {
	// Name is the key under which this check appears in the response's
	// "checks" map. Use the form "{componentName}:{measurementName}",
	// for example "database:connections" or "storage:reachability".
	// A single-segment name such as "storage" is also valid when only
	// one measurement is being reported for a component.
	//
	// The name "system:time" is reserved for the framework's implicit
	// per-response heartbeat. Registering a Check with this name causes
	// RegisterCheck and RegisterReadyCheck to panic.
	Name string

	// ComponentType classifies the component being checked. Use one of
	// the standard values:
	//
	//	"datastore"  the component is a data store such as a SQL database, object store, or cache
	//	"system"     the component is a system primitive such as a clock or disk
	//	"component"  any other internal component
	//
	// A URI is also acceptable when the component is a well-known
	// external service and you want to identify its type by reference.
	ComponentType string

	// Timeout bounds how long the Run function is allowed to execute.
	// When Timeout is greater than zero, the Engine wraps the request
	// context with context.WithTimeout(ctx, Timeout) before calling Run,
	// so Run will observe ctx.Done() once the deadline is reached.
	// When Timeout is zero, no timeout is applied and the check inherits
	// the request context as-is. Concrete check producers ship a
	// sensible default Timeout that callers may override.
	Timeout time.Duration

	// Run is the function that actually inspects the component and
	// produces a Result. It receives a context that already carries any
	// Timeout configured on the Check, so Run should not wrap the
	// context with another timeout of its own. Run must not block past
	// ctx.Done(), and it must not panic. A nil Run is treated by the
	// Engine as a failed check with a descriptive Output.
	Run func(ctx context.Context) Result
}

// ComponentStatus is the per-component object that appears inside the
// "checks" map of a HealthResponse. The Engine produces values of this
// type by combining a Check's identity (Name, ComponentType) with the
// Result returned by Check.Run, plus the time at which the check ran.
//
// All fields are JSON-tagged with omitempty so that the response stays
// compact and so that the framework's omission rules are honored
// automatically. In particular, Output is cleared by the Engine when
// Status is StatusPass, which combined with omitempty guarantees that
// the "output" key disappears from the JSON on a passing component.
type ComponentStatus struct {
	// ComponentId is a unique identifier for a specific instance of a
	// sub-component, used when the same logical component is reported
	// for multiple replicas or nodes. Leave empty when only one
	// instance is reported.
	ComponentId string `json:"componentId,omitempty"`

	// ComponentType classifies the component. See Check.ComponentType
	// for the accepted values.
	ComponentType string `json:"componentType,omitempty"`

	// ObservedValue is the measurement produced by the check, when the
	// check produces a quantitative result. May be any JSON-encodable
	// value. Omitted when the check is binary or did not measure
	// anything.
	ObservedValue any `json:"observedValue,omitempty"`

	// ObservedUnit clarifies the unit of measurement for ObservedValue.
	// Set this whenever ObservedValue is set.
	ObservedUnit string `json:"observedUnit,omitempty"`

	// Status is the per-component health status. See Status for the
	// allowed values.
	Status Status `json:"status,omitempty"`

	// AffectedEndpoints is a list of URI templates for endpoints that
	// are affected by issues with this component. The framework omits
	// this list from the serialized response when Status is StatusPass,
	// so callers do not need to clear it on a passing check.
	AffectedEndpoints []string `json:"affectedEndpoints,omitempty"`

	// Time is the ISO 8601 timestamp at which the check was run, in
	// RFC 3339 Nano format with UTC location. The Engine sets this
	// field automatically.
	Time string `json:"time,omitempty"`

	// Output carries raw diagnostic text for fail or warn states. The
	// Engine clears Output when Status is StatusPass before
	// serialization, so a passing component does not leak this field.
	Output string `json:"output,omitempty"`

	// Links is a map of link relation names to URIs that point at more
	// information about the component's health, for example a Grafana
	// dashboard or a runbook page.
	Links map[string]string `json:"links,omitempty"`
}

// HealthResponse is the top-level response body produced by the Engine.
// It is the value returned from Engine.RunLivez, Engine.RunReadyz, and
// Engine.RunHealthz, and it is the structure that the HTTP adapter
// serializes as "application/health+json".
//
// Consumers reading this type directly (for example a CLI adapter)
// should always start with Status, which is the only required field
// and which describes the overall service health. The Checks map
// expands the picture into individual components, each one a list of
// ComponentStatus values to support multi-replica reporting.
type HealthResponse struct {
	// Status is the overall health of the service. Required. The
	// Engine derives this value by aggregating the per-component
	// statuses: any fail makes the overall status fail, otherwise any
	// warn makes it warn, otherwise pass.
	Status Status `json:"status"`

	// ServiceId is the unique identifier of the service in the
	// application scope. Set via NewEngine's serviceID argument. Omit
	// to keep the response anonymous.
	ServiceId string `json:"serviceId,omitempty"`

	// Description is a human-friendly description of the service. Set
	// via NewEngine's description argument. Useful for dashboards and
	// for distinguishing between several services in a single view.
	Description string `json:"description,omitempty"`

	// Checks is the per-component breakdown. The map key is the
	// Check.Name from the registered check, plus the reserved
	// "system:time" key that the Engine always populates. Each value
	// is a slice of ComponentStatus so that the same logical component
	// can report multiple replicas, though current built-in checks
	// produce a single entry each.
	Checks map[string][]ComponentStatus `json:"checks,omitempty"`
}
