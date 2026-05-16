package dbcheck

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kula-app/go-health/core"
)

type fakePinger struct {
	err   error
	delay time.Duration
}

func (f *fakePinger) PingContext(ctx context.Context) error {
	select {
	case <-time.After(f.delay):
		return f.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestNew_defaults(t *testing.T) {
	c := New(&fakePinger{})
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
	c := New(&fakePinger{})
	r := c.Run(context.Background())
	if r.Status != core.StatusPass {
		t.Errorf("Status = %q, want pass", r.Status)
	}
	if r.Output != "" {
		t.Errorf("Output = %q, want empty", r.Output)
	}
}

func TestRun_fail(t *testing.T) {
	c := New(&fakePinger{err: errors.New("connection refused")})
	r := c.Run(context.Background())
	if r.Status != core.StatusFail {
		t.Errorf("Status = %q, want fail", r.Status)
	}
	if r.Output != "connection refused" {
		t.Errorf("Output = %q, want 'connection refused'", r.Output)
	}
}

// TestRun_timeoutDeadlineExceeded verifies that when the caller wraps
// the request context with a deadline shorter than the Pinger's
// response time (which is what the Engine does using c.Timeout), Run
// returns a Fail Result whose Output carries the deadline-exceeded
// error message. This mirrors the per-check timeout contract that the
// Engine applies in production.
func TestRun_timeoutDeadlineExceeded(t *testing.T) {
	c := New(&fakePinger{delay: 200 * time.Millisecond})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	r := c.Run(ctx)
	if r.Status != core.StatusFail {
		t.Errorf("Status = %q, want fail", r.Status)
	}
	if !strings.Contains(r.Output, "deadline exceeded") {
		t.Errorf("Output = %q, want it to contain 'deadline exceeded'", r.Output)
	}
}
