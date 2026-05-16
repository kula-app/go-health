// Package dbcheck produces a core.Check that verifies a SQL database
// is reachable by calling PingContext on a minimal Pinger interface.
// The standard library's *sql.DB satisfies Pinger directly, so the
// typical wiring is one line:
//
//	engine.RegisterReadinessCheck(dbcheck.New(db))
//
// The Pinger abstraction keeps this package free of any specific
// database driver dependency, which means consumers using a wrapper
// type (an ORM driver, a connection pool with extra instrumentation,
// or a mock) can pass that wrapper directly as long as it exposes
// PingContext with the same signature.
package dbcheck

import (
	"context"
	"time"

	"github.com/kula-app/go-health/core"
)

// DefaultName is the value used for core.Check.Name when New produces
// a Check. Callers may override the Name on the returned value when
// they want to distinguish multiple database checks, for example
// "database:postgres-primary" and "database:postgres-replica".
const DefaultName = "database:connections"

// DefaultComponentType is the value used for core.Check.ComponentType
// when New produces a Check. It uses the RFC's "datastore" type,
// which is correct for any persistent storage backend. Override it
// when the underlying database is better described another way, for
// example "datastore" remains correct for SQL, NoSQL, time-series,
// and document stores alike.
const DefaultComponentType = "datastore"

// DefaultTimeout is the per-check timeout applied to the database
// ping. Two seconds is short enough that a slow database does not
// stall the kubelet's readiness probe, yet long enough to tolerate
// normal connection-pool acquisition delay. Override when the
// underlying database is known to have higher steady-state latency.
const DefaultTimeout = 2 * time.Second

// Pinger is the minimal interface dbcheck requires from a database
// handle. PingContext should perform a no-op round-trip to verify
// connectivity, which is exactly the contract of the standard
// library's *sql.DB.PingContext method. The interface is named
// Pinger to match Go convention for single-method interfaces.
type Pinger interface {
	PingContext(ctx context.Context) error
}

// New returns a core.Check that calls p.PingContext on each Run.
// The returned Check is pre-populated with DefaultName,
// DefaultComponentType, and DefaultTimeout. Override any of those
// fields on the returned value before passing it to an Engine.
//
// On a successful ping the Check returns Result{Status: StatusPass}.
// On any error from PingContext, including a context deadline
// exceeded due to the Check's Timeout, the Check returns a Result
// with Status set to StatusFail and Output set to err.Error(), which
// the Engine then surfaces as the per-component Output in the
// serialized response. Be aware that database errors may carry
// sensitive details such as host names or schema names; if the
// /healthz endpoint is exposed publicly, wrap the produced Check
// with a custom Run that redacts the error message.
func New(p Pinger) core.Check {
	return core.Check{
		Name:          DefaultName,
		ComponentType: DefaultComponentType,
		Timeout:       DefaultTimeout,
		Run: func(ctx context.Context) []core.Result {
			if err := p.PingContext(ctx); err != nil {
				return []core.Result{{Status: core.StatusFail, Output: err.Error()}}
			}
			return []core.Result{{Status: core.StatusPass}}
		},
	}
}
