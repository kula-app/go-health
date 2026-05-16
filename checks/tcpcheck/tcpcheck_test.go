package tcpcheck

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/kula-app/go-health/core"
)

// stubDialer implements dialer for tests. delay simulates a slow dial
// so the timeout test can starve the call; err simulates a failed
// dial (refused, unreachable). On a "pass" path the stub returns a
// net.Conn from net.Pipe so the closing path in newWithDialer runs.
type stubDialer struct {
	err   error
	delay time.Duration
}

func (s *stubDialer) DialContext(ctx context.Context, _ string, _ string) (net.Conn, error) {
	select {
	case <-time.After(s.delay):
		if s.err != nil {
			return nil, s.err
		}
		c, _ := net.Pipe()
		return c, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestNew_defaults(t *testing.T) {
	c := newWithDialer(&stubDialer{}, "host:1234")
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

func TestRun_pass(t *testing.T) {
	c := newWithDialer(&stubDialer{}, "host:1234")
	r := c.Run(context.Background())
	if len(r) != 1 {
		t.Fatalf("len(r) = %d, want 1", len(r))
	}
	if r[0].Status != core.StatusPass {
		t.Errorf("Status = %q, want pass", r[0].Status)
	}
	if r[0].ComponentId != "host:1234" {
		t.Errorf("ComponentId = %q, want host:1234", r[0].ComponentId)
	}
	if r[0].Output != "" {
		t.Errorf("Output = %q, want empty", r[0].Output)
	}
}

func TestRun_fail(t *testing.T) {
	c := newWithDialer(&stubDialer{err: errors.New("connection refused")}, "host:1234")
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

// TestRun_timeoutDeadlineExceeded mirrors the dbcheck/s3check/redischeck
// timeout pattern: when the caller wraps the context with a deadline
// shorter than the dialer's response time (what the Engine does using
// c.Timeout), Run returns a failing Result whose Output contains the
// deadline message.
func TestRun_timeoutDeadlineExceeded(t *testing.T) {
	c := newWithDialer(&stubDialer{delay: 200 * time.Millisecond}, "host:1234")

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
