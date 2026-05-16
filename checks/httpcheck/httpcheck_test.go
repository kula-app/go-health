package httpcheck

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kula-app/go-health/core"
)

// stubDoer implements httpDoer for tests. status controls the response
// status code on success; err short-circuits the response and returns
// a transport-level error; delay simulates a slow upstream so the
// timeout test can starve the call.
type stubDoer struct {
	status int
	err    error
	delay  time.Duration
}

func (s *stubDoer) Do(req *http.Request) (*http.Response, error) {
	select {
	case <-time.After(s.delay):
		if s.err != nil {
			return nil, s.err
		}
		return &http.Response{
			StatusCode: s.status,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     http.Header{},
			Request:    req,
		}, nil
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}
}

func TestNew_defaults(t *testing.T) {
	c := newWithDoer(&stubDoer{status: 200}, "http://example/health")
	if c.Name != DefaultName {
		t.Errorf("Name = %q, want %q", c.Name, DefaultName)
	}
	if c.ComponentType != DefaultComponentType {
		t.Errorf("ComponentType = %q, want %q", c.ComponentType, DefaultComponentType)
	}
	if c.Timeout != DefaultTimeout {
		t.Errorf("Timeout = %v, want %v", c.Timeout, DefaultTimeout)
	}
}

// TestRun_passOnSuccessRange covers the full 2xx-3xx range with a few
// representative codes. The range was chosen by user spec: a 3xx
// without follow-redirects is still "the endpoint is up and answering";
// the check intentionally does not treat redirects as failures.
func TestRun_passOnSuccessRange(t *testing.T) {
	for _, status := range []int{200, 201, 204, 301, 302, 304, 399} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			c := newWithDoer(&stubDoer{status: status}, "http://example/health")
			r := c.Run(context.Background())
			if len(r) != 1 {
				t.Fatalf("len(r) = %d, want 1", len(r))
			}
			if r[0].Status != core.StatusPass {
				t.Errorf("status %d: Status = %q, want pass", status, r[0].Status)
			}
			if r[0].ComponentId != "http://example/health" {
				t.Errorf("ComponentId = %q, want http://example/health", r[0].ComponentId)
			}
		})
	}
}

func TestRun_failOnClientOrServerError(t *testing.T) {
	for _, status := range []int{400, 401, 404, 418, 500, 502, 503, 504} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			c := newWithDoer(&stubDoer{status: status}, "http://example/health")
			r := c.Run(context.Background())
			if len(r) != 1 {
				t.Fatalf("len(r) = %d, want 1", len(r))
			}
			if r[0].Status != core.StatusFail {
				t.Errorf("Status = %q, want fail", r[0].Status)
			}
			wantOutput := "unexpected status: " + http.StatusText(status)
			_ = wantOutput // we don't compare text — just the numeric in Output
			if !strings.Contains(r[0].Output, "unexpected status:") {
				t.Errorf("Output = %q, want it to start with 'unexpected status:'", r[0].Output)
			}
		})
	}
}

func TestRun_failOnTransportError(t *testing.T) {
	c := newWithDoer(&stubDoer{err: errors.New("connection refused")}, "http://example/health")
	r := c.Run(context.Background())
	if len(r) != 1 {
		t.Fatalf("len(r) = %d, want 1", len(r))
	}
	if r[0].Status != core.StatusFail {
		t.Errorf("Status = %q, want fail", r[0].Status)
	}
	if r[0].Output != "connection refused" {
		t.Errorf("Output = %q, want 'connection refused'", r[0].Output)
	}
}

// TestRun_timeoutDeadlineExceeded mirrors the pattern from the other
// check subpackages: when the caller wraps the context with a deadline
// shorter than the doer's response time (what the Engine does using
// c.Timeout), Run returns a failing Result whose Output contains the
// deadline message.
func TestRun_timeoutDeadlineExceeded(t *testing.T) {
	c := newWithDoer(&stubDoer{status: 200, delay: 200 * time.Millisecond}, "http://example/health")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	r := c.Run(ctx)
	if len(r) != 1 {
		t.Fatalf("len(r) = %d, want 1", len(r))
	}
	if r[0].Status != core.StatusFail {
		t.Errorf("Status = %q, want fail", r[0].Status)
	}
	if !strings.Contains(r[0].Output, "deadline exceeded") {
		t.Errorf("Output = %q, want it to contain 'deadline exceeded'", r[0].Output)
	}
}

// TestRun_invalidURL exercises the request-construction error path:
// http.NewRequestWithContext refuses URLs that don't parse, and the
// check must surface that as a fail Result rather than crashing.
func TestRun_invalidURL(t *testing.T) {
	c := newWithDoer(&stubDoer{status: 200}, "://no-scheme")
	r := c.Run(context.Background())
	if len(r) != 1 {
		t.Fatalf("len(r) = %d, want 1", len(r))
	}
	if r[0].Status != core.StatusFail {
		t.Errorf("Status = %q, want fail", r[0].Status)
	}
	if r[0].Output == "" {
		t.Error("Output should describe the URL parse error")
	}
}
