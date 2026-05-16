// Package tcpcheck produces a core.Check that verifies a TCP endpoint
// is accepting connections. The check opens a connection to the
// configured address and closes it immediately on success: no banner
// read, no protocol handshake, no I/O beyond the TCP handshake itself.
// That is intentionally the cheapest possible signal that a listener
// is alive, and it is the right shape for endpoints whose application
// protocol the framework does not understand (Kafka brokers, custom
// RPC servers, sibling microservices that do not expose an HTTP
// health page).
//
// Typical wiring:
//
//	engine.RegisterReadinessCheck(tcpcheck.New("kafka-broker-0:9092"))
//
// On a successful dial the Result has Status StatusPass. On any error
// from the dial, including a context deadline exceeded due to the
// Check's Timeout, the Result has Status StatusFail with err.Error()
// in Output. The stdlib net package already distinguishes connection
// refused, host unreachable, and timeout in its error messages, so the
// RFC Output text is sufficient diagnostic differentiation without the
// package adding its own categorization layer.
package tcpcheck

import (
	"context"
	"net"
	"time"

	"github.com/kula-app/go-health/core"
)

// DefaultName is the value used for core.Check.Name when New produces
// a Check. Callers may override the Name on the returned value when
// they want to distinguish multiple TCP checks, for example
// "kafka:reachability" and "rpc:reachability".
const DefaultName = "tcp"

// DefaultComponentType is the value used for core.Check.ComponentType
// when New produces a Check. The RFC "component" type is the generic
// fallback in §4.2 and is appropriate for a check that says nothing
// about the application protocol behind the port.
const DefaultComponentType = "component"

// DefaultTimeout is the per-check timeout applied to the TCP dial. Two
// seconds is short enough that a probe handler does not stall on an
// unreachable host yet long enough to tolerate normal network jitter
// inside a cluster. Override for endpoints with known higher
// steady-state connect latency, for example services routed across
// regions.
const DefaultTimeout = 2 * time.Second

// dialer is the minimal interface required to open a TCP connection.
// *net.Dialer satisfies it directly. The interface is unexported
// because production callers always reach it through New with the
// stdlib *net.Dialer; it exists so the package's own tests can stub a
// dial outcome without binding to a real port.
type dialer interface {
	DialContext(ctx context.Context, network, addr string) (net.Conn, error)
}

// New returns a core.Check that dials addr on each Run and reports
// pass when the TCP handshake completes. addr uses the host:port form
// accepted by net.Dial (RFC §3.2.2-compatible).
//
// The returned Check is pre-populated with DefaultName,
// DefaultComponentType, and DefaultTimeout. Override any of those
// fields on the returned value before passing it to an Engine.
//
// The Result reports addr as ComponentId so the per-component entry
// in the response identifies which TCP target the result is for, even
// when several tcpcheck Checks are registered with overlapping Names.
func New(addr string) core.Check {
	return newWithDialer(&net.Dialer{}, addr)
}

// newWithDialer is the implementation backing New. It is unexported
// and exists so that the package's own tests can supply a stub dialer
// without binding a real port. Production callers always reach this
// through New, which passes a zero-value *net.Dialer.
func newWithDialer(d dialer, addr string) core.Check {
	return core.Check{
		Name:          DefaultName,
		ComponentType: DefaultComponentType,
		Timeout:       DefaultTimeout,
		Run: func(ctx context.Context) []core.Result {
			r := core.Result{ComponentId: addr}
			conn, err := d.DialContext(ctx, "tcp", addr)
			if err != nil {
				r.Status = core.StatusFail
				r.Output = err.Error()
				return []core.Result{r}
			}
			_ = conn.Close()
			r.Status = core.StatusPass
			return []core.Result{r}
		},
	}
}
