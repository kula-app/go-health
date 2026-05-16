// Package redischeck produces a core.Check that verifies a Redis
// deployment is reachable. Two constructors are exposed, matching the
// two common deployment shapes a Go service interacts with:
//
//   - New takes a *redis.Client and produces a single-node check. Use
//     this for a standalone Redis, a single-instance master, or a
//     sentinel-managed failover client (which presents a single
//     endpoint to the application even when replicas exist behind it).
//   - NewCluster takes a *redis.ClusterClient and produces a multi-node
//     check that pings every shard (every master and every replica)
//     individually and returns one core.Result per node. The per-node
//     entries carry the node's address as ComponentId, so a partial
//     cluster outage is visible in the health response rather than
//     collapsed into a single fail/pass.
//
// Both constructors return a core.Check with the same default Name,
// ComponentType, and Timeout. Override any of those fields on the
// returned value before passing it to an Engine.
//
// Typical wiring:
//
//	import "github.com/redis/go-redis/v9"
//
//	rdb := redis.NewClient(&redis.Options{Addr: "redis:6379"})
//	engine.RegisterReadinessCheck(redischeck.New(rdb))
//
//	cluster := redis.NewClusterClient(&redis.ClusterOptions{Addrs: addrs})
//	engine.RegisterReadinessCheck(redischeck.NewCluster(cluster))
//
// On a successful per-node ping the Result has Status StatusPass. On
// any error, Status is StatusFail and Output is err.Error(). The
// engine aggregates per-node statuses using the strict rule "any fail
// fails the overall check", so a single unreachable shard will cause
// RunReadyz to return 503. Consumers who can tolerate a partial
// cluster outage should register this check via RegisterHealthCheck
// instead of RegisterReadinessCheck, or wrap NewCluster's Run with a
// custom aggregator.
package redischeck

import (
	"context"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/kula-app/go-health/core"
)

// DefaultName is the value used for core.Check.Name when New or
// NewCluster produces a Check. Override it when the service uses more
// than one Redis deployment and you want to distinguish them in the
// health response, for example "redis:cache" and "redis:sessions".
const DefaultName = "redis:ping"

// DefaultComponentType is the value used for core.Check.ComponentType
// when New or NewCluster produces a Check. The RFC "datastore" type
// covers Redis in both cache and persistence configurations.
const DefaultComponentType = "datastore"

// DefaultTimeout is the per-check timeout applied to the Redis ping.
// One second is short enough that a probe handler does not stall on an
// unreachable cluster yet long enough to tolerate normal
// connection-pool acquisition latency inside a cluster.
const DefaultTimeout = time.Second

// nodePinger is the minimal interface required to ping a single Redis
// node. *redis.Client satisfies it through the realPinger adapter
// below. The interface is unexported because callers always reach it
// through the New or NewCluster constructors with concrete go-redis
// types; the interface exists so the package's own tests can stub a
// node without standing up a real Redis instance.
type nodePinger interface {
	Ping(ctx context.Context) error
	Addr() string
}

// shardIterator yields every node in a Redis Cluster (every master and
// every replica) to a callback. *redis.ClusterClient satisfies it
// through the clusterShardIterator adapter below. As with nodePinger
// the interface is unexported and exists for testability: the package's
// own tests provide a fake iterator that synthesises any number of
// node states without a real cluster.
type shardIterator interface {
	forEach(ctx context.Context, fn func(p nodePinger))
}

// realPinger adapts a concrete *redis.Client to the nodePinger
// interface. It hides the difference between *redis.StatusCmd's Err()
// and a plain error return so the rest of the package can be written
// against a stdlib-shaped interface.
type realPinger struct {
	c *redis.Client
}

// Ping issues a PING command and returns the error from the resulting
// *redis.StatusCmd. A non-nil error indicates the node is unreachable
// or rejected the command.
func (r *realPinger) Ping(ctx context.Context) error {
	return r.c.Ping(ctx).Err()
}

// Addr returns the configured address of the underlying *redis.Client.
// This is what appears as ComponentId in the per-node Result.
func (r *realPinger) Addr() string {
	return r.c.Options().Addr
}

// clusterShardIterator adapts a concrete *redis.ClusterClient to the
// shardIterator interface. It wraps go-redis's ForEachShard, which
// fans out across every node concurrently, and re-presents each node
// as a nodePinger so the per-node ping logic can be written once.
type clusterShardIterator struct {
	c *redis.ClusterClient
}

// forEach invokes fn for every node in the cluster. ForEachShard's
// callback signature requires returning an error, but this iterator
// always returns nil so that one unreachable node does not abort the
// iteration over its siblings: collecting status from every node is
// the whole point of the multi-result fan-out.
func (i *clusterShardIterator) forEach(ctx context.Context, fn func(p nodePinger)) {
	_ = i.c.ForEachShard(ctx, func(ctx context.Context, n *redis.Client) error {
		fn(&realPinger{c: n})
		return nil
	})
}

// New returns a core.Check that pings a single Redis node on each Run.
// The returned Check is pre-populated with DefaultName,
// DefaultComponentType, and DefaultTimeout. Override any of those
// fields on the returned value before passing it to an Engine.
//
// The Result reports the node's configured address as ComponentId so
// the per-component entry in the response identifies which Redis
// deployment the result is for, even when several redischeck Checks
// are registered with overlapping Names.
//
// On a successful ping the Result has Status StatusPass. On any error
// from PingContext, including a context deadline exceeded due to the
// Check's Timeout, the Result has Status StatusFail and Output set to
// err.Error(). Redis error messages may include host names or auth
// hints; if the /healthz endpoint is exposed publicly, wrap the
// produced Check with a custom Run that redacts the error message.
func New(client *redis.Client) core.Check {
	return newWithPinger(&realPinger{c: client})
}

// newWithPinger is the implementation backing New. It is unexported
// and exists so that the package's own tests can supply a stub
// nodePinger without depending on a real Redis. Production callers
// always reach this through New.
func newWithPinger(p nodePinger) core.Check {
	return core.Check{
		Name:          DefaultName,
		ComponentType: DefaultComponentType,
		Timeout:       DefaultTimeout,
		Run: func(ctx context.Context) []core.Result {
			return []core.Result{pingNode(ctx, p)}
		},
	}
}

// NewCluster returns a core.Check that pings every node in a Redis
// Cluster on each Run and produces one core.Result per node. Each
// per-node Result carries the node's address as ComponentId so a
// partial cluster outage (one shard down, the rest reachable) appears
// in the health response as a mix of pass and fail entries under the
// same check key, matching the multi-sub-component shape described in
// RFC §4.
//
// The returned Check is pre-populated with DefaultName,
// DefaultComponentType, and DefaultTimeout. The Timeout bounds the
// whole fan-out, not each per-node ping individually; ForEachShard
// runs the pings concurrently so the total wall time is roughly the
// slowest per-node round-trip.
//
// Engine aggregation is strict: any failing per-node Result fails the
// overall check, which at the HTTP layer becomes a 503. Services that
// can serve traffic with a degraded cluster (one shard down) should
// register this check via RegisterHealthCheck rather than
// RegisterReadinessCheck, or wrap its Run with a quorum-based
// aggregator.
func NewCluster(client *redis.ClusterClient) core.Check {
	return newWithIterator(&clusterShardIterator{c: client})
}

// newWithIterator is the implementation backing NewCluster. It is
// unexported and exists so that the package's own tests can supply a
// stub shardIterator that synthesizes an arbitrary number of node
// states without standing up a real Redis Cluster. Production callers
// always reach this through NewCluster.
func newWithIterator(it shardIterator) core.Check {
	return core.Check{
		Name:          DefaultName,
		ComponentType: DefaultComponentType,
		Timeout:       DefaultTimeout,
		Run: func(ctx context.Context) []core.Result {
			var (
				mu  sync.Mutex
				out []core.Result
			)
			it.forEach(ctx, func(p nodePinger) {
				r := pingNode(ctx, p)
				mu.Lock()
				out = append(out, r)
				mu.Unlock()
			})
			return out
		},
	}
}

// pingNode is the per-node logic shared by the single-node and cluster
// constructors. It issues one Ping against p, converts the result into
// a core.Result, and tags the entry with p.Addr() as the ComponentId so
// consumers can distinguish nodes within one check key.
func pingNode(ctx context.Context, p nodePinger) core.Result {
	r := core.Result{ComponentId: p.Addr()}
	if err := p.Ping(ctx); err != nil {
		r.Status = core.StatusFail
		r.Output = err.Error()
		return r
	}
	r.Status = core.StatusPass
	return r
}
