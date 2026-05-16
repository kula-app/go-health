// Package s3check produces a core.Check that verifies an S3 bucket is
// reachable and that the configured credentials are valid. It issues
// a single HeadBucket request against the AWS SDK v2 client on each
// Run. HeadBucket is the cheapest correctness check S3 offers, so
// this Check is appropriate for running on every dashboard request
// without imposing meaningful cost on the bucket's request budget.
//
// HeadBucket verifies reachability and credentials. It does not
// verify object-level read, write, or delete permissions. Failures
// caused by missing object-level permissions will surface when the
// service actually tries to use the bucket, not on the health
// endpoint. If that distinction matters for your service, run an
// additional check that performs the more specific operation, such
// as a small PutObject and DeleteObject pair on a known sentinel key.
package s3check

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/kula-app/go-health/core"
)

// DefaultName is the value used for core.Check.Name when New produces
// a Check. Override it when the service uses more than one bucket and
// you want to distinguish them in the health response, for example
// "storage:builds" and "storage:cache".
const DefaultName = "storage"

// DefaultComponentType is the value used for core.Check.ComponentType
// when New produces a Check. The RFC "datastore" type covers any
// persistent object store.
const DefaultComponentType = "datastore"

// DefaultTimeout is the per-check timeout applied to the HeadBucket
// call. Five seconds is generous enough to tolerate a cold-start
// connection from inside the cluster to S3 yet short enough that a
// regional outage cannot stall a probe handler indefinitely.
const DefaultTimeout = 5 * time.Second

// bucketHeader captures the subset of *s3.Client that this package
// uses. It is unexported because consumers should pass the concrete
// *s3.Client they already construct in their service wiring (which
// trivially satisfies this interface). The interface exists only so
// that the package's own tests can stub HeadBucket without spinning
// up a fake S3 HTTP server.
type bucketHeader interface {
	HeadBucket(ctx context.Context, params *s3.HeadBucketInput, optFns ...func(*s3.Options)) (*s3.HeadBucketOutput, error)
}

// New returns a core.Check that calls HeadBucket on the given client
// for the given bucket name. The returned Check is pre-populated with
// DefaultName, DefaultComponentType, and DefaultTimeout. Override any
// of those fields on the returned value before passing it to an
// Engine.
//
// On a successful HeadBucket the Check returns Result{Status:
// StatusPass}. On any error from HeadBucket, including a context
// deadline exceeded due to the Check's Timeout, the Check returns a
// Result with Status set to StatusFail and Output set to err.Error(),
// which the Engine then surfaces as the per-component Output in the
// serialized response. AWS error messages can include bucket names,
// regions, request IDs, and IAM ARNs; if the /healthz endpoint is
// exposed publicly, wrap the produced Check with a custom Run that
// redacts the error message before returning.
func New(client *s3.Client, bucket string) core.Check {
	return newWithHeader(client, bucket)
}

// newWithHeader is the implementation backing New. It is unexported
// and exists so that the package's own tests can supply a stub
// bucketHeader without going through the AWS SDK. Production callers
// always reach this through New.
func newWithHeader(h bucketHeader, bucket string) core.Check {
	return core.Check{
		Name:          DefaultName,
		ComponentType: DefaultComponentType,
		Timeout:       DefaultTimeout,
		Run: func(ctx context.Context) []core.Result {
			_, err := h.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)})
			if err != nil {
				return []core.Result{{Status: core.StatusFail, Output: err.Error()}}
			}
			return []core.Result{{Status: core.StatusPass}}
		},
	}
}
