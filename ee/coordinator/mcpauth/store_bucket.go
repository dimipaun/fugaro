// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/dimipaun/fugaro/internal/blobx"
	"gocloud.dev/gcerrors"
)

// This file is the one concrete backend of §8's recommendation: the `mcpauth/`
// prefix of a bucket of its own, written with internal/blobx. It is the only
// file that names a store, which is what the interface in store.go is for — an
// owner who answers §8 with a second Realtime Database instance or with
// Firestore changes this file and its test and nothing any other task builds
// against.
//
// **The bucket is its own, and the name is what makes it so** (D935, and the
// correction of 2026-11-05). `fugaro-mcpauth-<gcp-project>` is outside every
// prefix a conditioned role in this installation is fenced to, which is the
// whole point: the managed deployer's `fugaroDeployerStorage` is a
// project-level binding conditioned on `fmgd-`
// (deploy/terraform/gcp/modules/installation/deployer.tf:379, the condition at
// :142) and every operator can act as the deployer (:501), so a store whose
// name carried that prefix would be readable, rewritable and re-grantable by
// every operator — F32 again, one resource over. The one binding this store
// has is M4's, on the coordinator's own service account, and the name is what
// makes "and nobody else" true rather than nearly true.
//
// The checks below are therefore about the name and not about politeness: a
// later edit that pointed the store at the project's database, at the runs
// bucket or at a managed-prefixed bucket has to fail at construction rather
// than work, because each of those is a set of people who could read token
// hashes and revocation state.

// StoreBucketName is the store's bucket for gcpProject. The name is a
// function of the project alone — the convention the runs bucket and both
// state buckets already follow (internal/managed/statebucket.go:75) — and it
// is never taken from a flag or a plan: a name a caller could choose is a name
// that could be pointed at another installation's store.
func StoreBucketName(gcpProject string) string { return "fugaro-mcpauth-" + gcpProject }

// ErrStoreURL refuses a store URL that is not this store's own bucket. The
// message names which of the rules it broke, because the mistake it guards
// against is a plausible one — "the records used to live there" — rather than
// a hostile one.
var ErrStoreURL = errors.New("mcpauth: the store's URL must name the store's own bucket")

// managedBucketPrefix is the managed deployer's fence
// (deploy/terraform/gcp/modules/installation/deployer.tf:142). A store inside
// it is reachable by every operator through the deployer's own account, which
// is the reach D935's answer exists to leave.
const managedBucketPrefix = "fmgd-"

// runsBucketPrefix is the runs bucket's: every launcher reads it whole and
// every operator holds object admin on it
// (deploy/terraform/gcp/modules/installation/iam.tf:207, :222).
const runsBucketPrefix = "fugaro-runs-"

// OpenStore opens the store named by the coordinator's own configuration and
// nothing else. One URL, one bucket, one prefix: the store's bucket is its
// own, and the `mcpauth/` prefix inside it is the store's own.
func OpenStore(ctx context.Context, gcpProject, rawURL string) (Store, error) {
	bucket, err := storeBucket(gcpProject, rawURL)
	if err != nil {
		return nil, err
	}
	b, err := blobx.Open(ctx, "gs://"+bucket)
	if err != nil {
		return nil, err
	}
	return newBucketStore(b), nil
}

// storeBucket is the check OpenStore makes before it opens anything, and it is
// separate so that a test can put a URL to it without a cloud client: every
// refusal below is a refusal to open, not a refusal to use.
func storeBucket(gcpProject, rawURL string) (string, error) {
	if gcpProject == "" {
		return "", fmt.Errorf("%w: the project id is empty", ErrStoreURL)
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("%w: %q is not a bucket URL", ErrStoreURL, rawURL)
	}
	if u.Path != "" && u.Path != "/" {
		return "", fmt.Errorf("%w: %q names a path: the prefix inside the bucket is the store's own", ErrStoreURL, rawURL)
	}
	if u.Scheme == "http" || u.Scheme == "https" {
		return "", fmt.Errorf("%w: %q is a Realtime Database URL: the records left the project's database because every launcher and operator holds a project-level grant on it that no rule binds (D935, reconciliation.md F32)", ErrStoreURL, rawURL)
	}
	if u.Scheme != "gs" {
		return "", fmt.Errorf("%w: %q is not a gs:// bucket", ErrStoreURL, rawURL)
	}
	switch {
	case u.Host == StoreBucketName(gcpProject):
		return u.Host, nil
	case strings.HasPrefix(u.Host, runsBucketPrefix):
		return "", fmt.Errorf("%w: %q is the runs bucket: every launcher reads it whole and every operator holds object admin on it", ErrStoreURL, u.Host)
	case strings.HasPrefix(u.Host, managedBucketPrefix):
		return "", fmt.Errorf("%w: %q is under the managed prefix %q, whose conditioned storage role every operator can act as (deploy/terraform/gcp/modules/installation/deployer.tf:379, :501)", ErrStoreURL, u.Host, managedBucketPrefix)
	default:
		return "", fmt.Errorf("%w: %q is not this installation's store (%q)", ErrStoreURL, u.Host, StoreBucketName(gcpProject))
	}
}

// newBucketStore is the record layer over one bucket. It is how a test reaches
// the real layout without a cloud client: internal/gcpfake serves the bucket
// and the generation precondition the compare-and-swaps stand on.
func newBucketStore(b *blobx.Bucket) Store { return newRecords(bucketObjects{b: b}) }

// bucketObjects is internal/blobx seen as the four operations the record layer
// asks for. Each maps the driver's own answer onto errMissing or errChanged,
// so the layout above reads the same whatever a store turns out to be.
type bucketObjects struct{ b *blobx.Bucket }

func (o bucketObjects) read(ctx context.Context, key string) ([]byte, int64, error) {
	// Strict, because a 403 is not a missing object: gocloud folds one into
	// the other, and a store whose IAM was narrowed by mistake would
	// otherwise report every token as one that was never minted.
	data, gen, err := o.b.ReadStrict(ctx, key)
	switch {
	case errors.Is(err, blobx.ErrNotExist):
		return nil, 0, errMissing
	case err != nil:
		return nil, 0, err
	}
	return data, gen, nil
}

func (o bucketObjects) create(ctx context.Context, key string, data []byte) (int64, error) {
	gen, err := o.b.Create(ctx, key, data, "application/json")
	switch {
	case errors.Is(err, blobx.ErrExists):
		return 0, errChanged
	case err != nil:
		return 0, err
	}
	return gen, nil
}

func (o bucketObjects) replace(ctx context.Context, key string, data []byte, gen int64) (int64, error) {
	// internal/blobx matches the generation on GCS and the contents off it,
	// so the previous bytes are what the write has to match where there is
	// no generation to match. On GCS the read would be one request thrown
	// away, and a store runs on GCS.
	var prev []byte
	if o.b.GCSName == "" {
		var err error
		if prev, _, err = o.b.Read(ctx, key); err != nil && !errors.Is(err, blobx.ErrNotExist) {
			return 0, err
		}
	}
	newGen, err := o.b.ReplaceIf(ctx, key, data, gen, prev)
	switch {
	case errors.Is(err, blobx.ErrConflict):
		return 0, errChanged
	case err != nil:
		return 0, err
	}
	return newGen, nil
}

func (o bucketObjects) delete(ctx context.Context, key string) error {
	// A missing object is not a failure here: the revoke it carries out is
	// idempotent by design, so a second revoke of the same family, and a
	// revoke whose family a rotation already replaced, both succeed.
	if err := o.b.Delete(ctx, key); err != nil && gcerrors.Code(err) != gcerrors.NotFound {
		return err
	}
	return nil
}
