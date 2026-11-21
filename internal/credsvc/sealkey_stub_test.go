package credsvc

// This test is the stub's own and goes with it: I1 deletes sealkey_stub.go and
// this file together, because what it pins is the answer a contract with no
// implementations gives, which is not a rule of the wire. Every rule of the wire
// is pinned in sealkey_wire_test.go, which stays.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/budget/token"
)

// TestTheStubReleasesNothing is C1's acceptance line read literally: a build
// that ships the contract and no implementations must not release a key to
// anyone, and the three sides that decide a release must each answer the way
// that admits no caller.
//
// It is deliberately not a test of the SHAPES — those are the compiler's — but
// of the direction each stub fails in: a VM is nobody's, a ciphertext opens
// nothing, and no digest is registered, so the release route's own conjunction
// cannot hold for any caller whatever it presents.
func TestTheStubReleasesNothing(t *testing.T) {
	ctx := context.Background()
	vm := VMIdentity{
		Account: "fugaro-batch@my-project.iam.gserviceaccount.com",
		Project: "my-project",
		Zone:    "us-central1-a",
		Token:   "an-identity-token-no-stub-reads",
	}
	job := "projects/my-project/locations/us-central1/jobs/fugaro-acme-web-20261120-120000-abcd"
	wrappedAt := time.Unix(1_763_000_000, 0)

	if SealKeyJobLink == nil {
		t.Fatal("no link is installed: a build of this contract must install the stub")
	}
	if got, ok, err := SealKeyJobLink.VMsJob(ctx, vm, job, wrappedAt); ok || err != nil {
		t.Fatalf("the stub link accepted a VM: %+v, ok=%v, err=%v", got, ok, err)
	}

	if SealKeyOpener == nil {
		t.Fatal("no opener is installed")
	}
	// A ciphertext of the contract's own size and of nothing at all: the stub
	// must not open either, because it holds no key and asks KMS for none. Its
	// refusal is the service's unreadable-state one and not the release's: the
	// route is what tells a caller ErrSealKeyNotReleased, and a stub claiming
	// KMS had refused a ciphertext would claim a measurement nothing made.
	for _, ciphertext := range [][]byte{make([]byte, token.WrappedCiphertextSize), nil} {
		plain, err := SealKeyOpener.Open(ctx, ciphertext)
		if err == nil {
			t.Fatalf("the stub opener returned %q for a %d-byte ciphertext", plain, len(ciphertext))
		}
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("the stub opener refused with %v, which is not the unreadable-state refusal", err)
		}
	}

	if SealKeyRegistry == nil {
		t.Fatal("no registry is installed")
	}
	digest := token.JobDigest(job)
	if got, ok, err := SealKeyRegistry.Entry(ctx, "acme-web/20261120-120000-abcd", digest); ok || err != nil {
		t.Fatalf("the stub registry answered an entry: %+v, ok=%v, err=%v", got, ok, err)
	}
}
