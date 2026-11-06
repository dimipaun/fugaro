// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package mcpauth

// M2's backend test: the one check that has to happen before anything is
// opened.
//
// D935 moved these records out of the project's database because no rule can
// keep a launcher, an operator, a budget admin or the history account out of
// it, and the store's whole value is that its name puts it out of reach of
// every identity the installation already lets act. A URL that pointed the
// store back at the database, at the runs bucket or at a managed-prefixed
// bucket would be that same exposure under another name, so the store refuses
// one at construction — where a mistake is a failure rather than a quiet
// widening.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestTheStoreRefusesTheDatabaseAndTheRunsBucket(t *testing.T) {
	for _, tc := range []struct {
		name string
		url  string
		// why is what the refusal's message must name: the rule the URL
		// broke, so that a reader of the error cannot take it for a typo.
		why string
	}{
		{"empty", "", "not a bucket URL"},
		{"the project's database", "https://project-a-default-rtdb.firebaseio.com", "left the project's database"},
		{"the database behind a bucket scheme", "gs://project-a-default-rtdb.firebaseio.com", "not this installation's store"},
		{"the runs bucket", "gs://fugaro-runs-project-a", "the runs bucket"},
		{"a managed-prefixed bucket", "gs://fmgd-fugaro-mcpauth-project-a", "managed prefix"},
		{"another installation's store", "gs://fugaro-mcpauth-project-b", "not this installation's store"},
		{"a prefix inside the bucket", "gs://fugaro-mcpauth-project-a/some/prefix", "names a path"},
		{"a local directory", "file://localhost/tmp/mcpauth", "names a path"},
		{"another object store", "mem://fugaro-mcpauth-project-a", "not a gs:// bucket"},
		{"not a URL at all", "the store", "not a bucket URL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bucket, err := storeBucket(testProject, tc.url)
			if err == nil {
				t.Fatalf("storeBucket accepted %q as bucket %q; want a refusal", tc.url, bucket)
			}
			if !errors.Is(err, ErrStoreURL) {
				t.Errorf("storeBucket(%q) = %v; want ErrStoreURL", tc.url, err)
			}
			if !strings.Contains(err.Error(), tc.why) {
				t.Errorf("storeBucket(%q) = %v; want the message to name %q", tc.url, err, tc.why)
			}
		})
	}

	// The store's own bucket is the one the fake serves and the one M4
	// creates: the URL the coordinator's configuration holds, and nothing
	// else, is what opens.
	bucket, err := storeBucket(testProject, "gs://"+StoreBucketName(testProject))
	if err != nil {
		t.Fatalf("storeBucket refused the store's own bucket: %v", err)
	}
	if bucket != testBucket() {
		t.Fatalf("storeBucket admitted %q; the store the tests use is %q", bucket, testBucket())
	}

	// OpenStore is the same check, so a refused URL fails before a client
	// is made: this call makes no request, which is why it is hermetic.
	if _, err := OpenStore(context.Background(), testProject, "https://project-a-default-rtdb.firebaseio.com"); !errors.Is(err, ErrStoreURL) {
		t.Errorf("OpenStore of the database = %v; want ErrStoreURL", err)
	}
}

// TestTheStoreWritesUnderItsOwnPrefix is the layout half of the same rule: a
// record is under PathPrefix and nowhere else, so the bucket holds one store
// rather than a store and whatever else an operator put there.
func TestTheStoreWritesUnderItsOwnPrefix(t *testing.T) {
	ctx := context.Background()
	s := newFakeStore(t)

	access, refresh, err := Mint(ctx, s, "user:person-1", testClient(), testLimits())
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if _, _, err := Rotate(ctx, s, refresh); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if err := s.PutClient(ctx, testClient()); err != nil {
		t.Fatalf("PutClient: %v", err)
	}

	records := s.objects(t)
	for _, want := range []string{
		tokenPrefix + TokenHash(access),
		clientPrefix + testClient().ID,
	} {
		if _, ok := records[want]; !ok {
			t.Errorf("%s is not in the store", want)
		}
	}
	for key := range records {
		if !strings.HasPrefix(key, PathPrefix) {
			t.Errorf("%s is outside the store's prefix %q", key, PathPrefix)
		}
	}
}
