package s3check

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/kula-app/go-health/core"
)

type fakeHeader struct {
	err   error
	delay time.Duration
}

func (f *fakeHeader) HeadBucket(ctx context.Context, _ *s3.HeadBucketInput, _ ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
	select {
	case <-time.After(f.delay):
		return &s3.HeadBucketOutput{}, f.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestNew_defaults(t *testing.T) {
	c := newWithHeader(&fakeHeader{}, "test-bucket")
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
	c := newWithHeader(&fakeHeader{}, "test-bucket")
	r := c.Run(context.Background())
	if len(r) != 1 {
		t.Fatalf("len(r) = %d, want 1", len(r))
	}
	if r[0].Status != core.StatusPass {
		t.Errorf("Status = %q, want pass", r[0].Status)
	}
}

func TestRun_fail(t *testing.T) {
	c := newWithHeader(&fakeHeader{err: errors.New("access denied")}, "test-bucket")
	r := c.Run(context.Background())
	if len(r) != 1 {
		t.Fatalf("len(r) = %d, want 1", len(r))
	}
	if r[0].Status != core.StatusFail {
		t.Errorf("Status = %q, want fail", r[0].Status)
	}
	if r[0].Output != "access denied" {
		t.Errorf("Output = %q, want 'access denied'", r[0].Output)
	}
}

// TestRun_timeoutDeadlineExceeded verifies that when the caller wraps
// the request context with a deadline shorter than the HeadBucket call's
// response time, which is what the Engine does using c.Timeout, Run
// returns a Fail Result whose Output carries the deadline-exceeded
// error message.
func TestRun_timeoutDeadlineExceeded(t *testing.T) {
	c := newWithHeader(&fakeHeader{delay: 200 * time.Millisecond}, "test-bucket")

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
