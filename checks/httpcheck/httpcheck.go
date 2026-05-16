// Package httpcheck produces a core.Check that verifies an HTTP
// endpoint returns a successful response. The check issues a single
// GET request against the configured URL and treats any 2xx or 3xx
// response as healthy; any 4xx, 5xx, or transport-level error
// (connection refused, TLS handshake failure, DNS resolution failure,
// timeout) is treated as a failure.
//
// Typical wiring:
//
//	engine.RegisterReadinessCheck(httpcheck.New(http.DefaultClient, "https://api.example.com/health"))
//
// The check is appropriate for downstream services that expose their
// own health endpoint and for third-party SaaS APIs that publish a
// status URL. It is not appropriate for endpoints that require a
// non-GET method, a request body, or response-body assertions — in
// those cases write a custom core.Check inline rather than wrapping
// this one.
//
// Authentication is delegated to the supplied *http.Client. The
// idiomatic shape is an auth-injecting http.RoundTripper installed on
// the client's Transport, which leaves the check itself unaware of
// the credential. Custom TLS configuration, proxy settings, and
// connection pooling are likewise the client's responsibility.
package httpcheck

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/kula-app/go-health/core"
)

// DefaultName is the value used for core.Check.Name when New produces
// a Check. Callers may override the Name on the returned value when
// they want to distinguish multiple HTTP checks, for example
// "billing:health" and "search:health".
const DefaultName = "http"

// DefaultComponentType is the value used for core.Check.ComponentType
// when New produces a Check. The RFC "component" type is the generic
// §4.2 fallback and is appropriate for an arbitrary HTTP endpoint.
const DefaultComponentType = "component"

// DefaultTimeout is the per-check timeout applied to the HTTP request.
// Five seconds is generous enough to tolerate a cold-start TLS
// handshake against a public endpoint yet short enough that a remote
// outage cannot stall a probe handler indefinitely. Override when the
// target endpoint is known to have higher steady-state latency.
const DefaultTimeout = 5 * time.Second

// httpDoer is the minimal interface required to issue an HTTP request.
// *http.Client satisfies it directly. The interface is unexported
// because production callers always reach it through New with a
// concrete *http.Client; it exists so the package's own tests can stub
// HTTP responses without standing up an httptest server.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// New returns a core.Check that issues a GET request to url on each
// Run, using client to execute the request. The returned Check is
// pre-populated with DefaultName, DefaultComponentType, and
// DefaultTimeout. Override any of those fields on the returned value
// before passing it to an Engine.
//
// Pass condition: the response status code is in the 2xx-3xx range.
// Fail conditions:
//
//   - 4xx and 5xx responses, with Output set to "unexpected status:
//     NNN".
//   - Any error from client.Do, with Output set to err.Error(). This
//     covers context cancellation (the Engine's per-Check timeout),
//     DNS failures, connection refused, TLS handshake failures, and
//     read errors on the request line.
//
// The Result reports url as ComponentId so the per-component entry in
// the response identifies which endpoint the result is for.
//
// The response body is drained and closed even on success so the
// underlying connection can be reused by the *http.Client's keep-alive
// pool. The body is otherwise ignored: response-body assertions are
// outside the scope of this check.
func New(client *http.Client, url string) core.Check {
	return newWithDoer(client, url)
}

// newWithDoer is the implementation backing New. It is unexported and
// exists so that the package's own tests can supply a stub httpDoer
// without making real HTTP calls. Production callers always reach this
// through New, which passes a concrete *http.Client.
func newWithDoer(d httpDoer, url string) core.Check {
	return core.Check{
		Name:          DefaultName,
		ComponentType: DefaultComponentType,
		Timeout:       DefaultTimeout,
		Run: func(ctx context.Context) []core.Result {
			r := core.Result{ComponentId: url}

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				r.Status = core.StatusFail
				r.Output = err.Error()
				return []core.Result{r}
			}

			resp, err := d.Do(req)
			if err != nil {
				r.Status = core.StatusFail
				r.Output = err.Error()
				return []core.Result{r}
			}
			defer resp.Body.Close()
			_, _ = io.Copy(io.Discard, resp.Body)

			if resp.StatusCode >= 200 && resp.StatusCode < 400 {
				r.Status = core.StatusPass
				return []core.Result{r}
			}
			r.Status = core.StatusFail
			r.Output = fmt.Sprintf("unexpected status: %d", resp.StatusCode)
			return []core.Result{r}
		},
	}
}
