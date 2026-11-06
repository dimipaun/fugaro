// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

// M2's tests stand on a bucket, and this file is how they get one without a
// cloud. internal/gcpfake answers the JSON API and, with it, the one
// precondition the store's compare-and-swap needs (ifGenerationMatch), so a
// test here reads and writes exactly the way the real store does
// (implementation-plan.md §6). A mem:// or file:// bucket would prove the key
// layout and nothing else: off GCS internal/blobx compares contents rather
// than generations, so a rotation racing a revoke would pass here and fail
// where the store actually runs.

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"gocloud.dev/blob"
)

// testBucket is the store's own bucket name for this package's project id:
// the name M4 creates and the only name store_bucket.go opens. It is derived
// the same way production derives it, so a change to either is one change.
func testBucket() string { return StoreBucketName(testProject) }

// fakeStore is one store over a bucket the fake serves, and the bucket
// itself: the "hashes only" test has to read what is at rest rather than what
// the store says it wrote, and only the bucket can answer that.
type fakeStore struct {
	Store
	bucket *blobx.Bucket
}

// newFakeBucket is the bucket the fake serves, for a test that needs the
// bucket itself rather than a store over it: the store's own two backends have
// to be two stores over one bucket before a write by one can be a race the
// other loses (TestRotateDoesNotRevokeWhenItLosesTheRace).
func newFakeBucket(t *testing.T) *blobx.Bucket {
	t.Helper()
	g := gcpfake.NewGCS(t)
	g.AddBucket(testBucket(), 1234, nil)
	return g.Bucket(t, testBucket())
}

func newFakeStore(t *testing.T) *fakeStore {
	t.Helper()
	b := newFakeBucket(t)
	return &fakeStore{Store: newBucketStore(b), bucket: b}
}

// objects is every value the store holds, keyed by its own key. It reads
// through the bucket, so it sees the bytes the store wrote and nothing the
// store remembers.
func (f *fakeStore) objects(t *testing.T) map[string][]byte {
	t.Helper()
	ctx := context.Background()
	out := map[string][]byte{}
	it := f.bucket.List(&blob.ListOptions{Prefix: PathPrefix})
	for {
		obj, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		if obj.IsDir {
			continue
		}
		data, _, err := f.bucket.Read(ctx, obj.Key)
		if err != nil {
			t.Fatal(err)
		}
		out[obj.Key] = data
	}
}
