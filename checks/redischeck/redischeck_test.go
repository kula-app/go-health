package redischeck

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kula-app/go-health/core"
)

// stubPinger implements nodePinger for tests. It carries a fixed addr,
// an optional error to return from Ping, and an optional delay so the
// timeout test can starve a slow node.
type stubPinger struct {
	addr  string
	err   error
	delay time.Duration
}

func (s *stubPinger) Ping(ctx context.Context) error {
	select {
	case <-time.After(s.delay):
		return s.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *stubPinger) Addr() string {
	return s.addr
}

func TestNew_defaults(t *testing.T) {
	c := newWithPinger(&stubPinger{addr: "redis:6379"})
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
	c := newWithPinger(&stubPinger{addr: "redis:6379"})
	r := c.Run(context.Background())
	if len(r) != 1 {
		t.Fatalf("len(r) = %d, want 1", len(r))
	}
	if r[0].Status != core.StatusPass {
		t.Errorf("Status = %q, want pass", r[0].Status)
	}
	if r[0].ComponentId != "redis:6379" {
		t.Errorf("ComponentId = %q, want redis:6379", r[0].ComponentId)
	}
	if r[0].Output != "" {
		t.Errorf("Output = %q, want empty", r[0].Output)
	}
}

func TestRun_fail(t *testing.T) {
	c := newWithPinger(&stubPinger{addr: "redis:6379", err: errors.New("connection refused")})
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

// TestRun_timeoutDeadlineExceeded mirrors the dbcheck/s3check timeout
// test: when the caller wraps the context with a deadline shorter than
// the node's response time (what the Engine does using c.Timeout), Run
// returns a failing Result whose Output contains the deadline message.
func TestRun_timeoutDeadlineExceeded(t *testing.T) {
	c := newWithPinger(&stubPinger{addr: "redis:6379", delay: 200 * time.Millisecond})

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

// stubShardIterator implements shardIterator for tests. It yields the
// configured nodes to the callback, optionally in parallel to match
// ForEachShard's real-world concurrent fan-out so the race detector
// has something to look at.
type stubShardIterator struct {
	nodes    []nodePinger
	parallel bool
}

func (s *stubShardIterator) forEach(ctx context.Context, fn func(p nodePinger)) {
	if !s.parallel {
		for _, n := range s.nodes {
			fn(n)
		}
		return
	}
	var wg sync.WaitGroup
	for _, n := range s.nodes {
		wg.Add(1)
		go func(n nodePinger) {
			defer wg.Done()
			fn(n)
		}(n)
	}
	wg.Wait()
}

func TestNewCluster_defaults(t *testing.T) {
	c := newWithIterator(&stubShardIterator{})
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

// TestCluster_multipleNodes verifies the multi-result fan-out: every
// node in the iterator becomes its own per-component entry, addresses
// land in ComponentId, and a mix of pass and fail surfaces correctly.
// The iterator runs callbacks in parallel; sorting by ComponentId
// makes the assertion deterministic without ordering the iterator.
func TestCluster_multipleNodes(t *testing.T) {
	c := newWithIterator(&stubShardIterator{
		parallel: true,
		nodes: []nodePinger{
			&stubPinger{addr: "10.0.0.1:6379"},
			&stubPinger{addr: "10.0.0.2:6379", err: errors.New("connection refused")},
			&stubPinger{addr: "10.0.0.3:6379"},
		},
	})

	r := c.Run(context.Background())
	if len(r) != 3 {
		t.Fatalf("len(r) = %d, want 3", len(r))
	}

	sort.Slice(r, func(i, j int) bool { return r[i].ComponentId < r[j].ComponentId })

	tests := []struct {
		wantId     string
		wantStatus core.Status
		wantOutput string
	}{
		{"10.0.0.1:6379", core.StatusPass, ""},
		{"10.0.0.2:6379", core.StatusFail, "connection refused"},
		{"10.0.0.3:6379", core.StatusPass, ""},
	}
	for i, tt := range tests {
		if r[i].ComponentId != tt.wantId {
			t.Errorf("r[%d].ComponentId = %q, want %q", i, r[i].ComponentId, tt.wantId)
		}
		if r[i].Status != tt.wantStatus {
			t.Errorf("r[%d].Status = %q, want %q", i, r[i].Status, tt.wantStatus)
		}
		if r[i].Output != tt.wantOutput {
			t.Errorf("r[%d].Output = %q, want %q", i, r[i].Output, tt.wantOutput)
		}
	}
}

// TestCluster_emptyCluster verifies that an iterator yielding no nodes
// produces an empty []Result, which the engine will then interpret as
// "nothing to report" and omit the check key from the response. This
// is the correct shape for a cluster with no configured shards (a
// transient state during topology bootstrap) and is preferable to a
// synthesised sentinel "no nodes" entry that the engine could not
// aggregate sensibly.
func TestCluster_emptyCluster(t *testing.T) {
	c := newWithIterator(&stubShardIterator{})
	r := c.Run(context.Background())
	if len(r) != 0 {
		t.Errorf("len(r) = %d, want 0", len(r))
	}
}
