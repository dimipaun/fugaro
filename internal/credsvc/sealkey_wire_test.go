package credsvc

// The seal-key wire's own tests: what the contract decides that the compiler
// cannot. They stay when I1 deletes the stub (sealkey_stub_test.go carries the
// stub's own answer and goes with it).

import (
	"strings"
	"testing"
)

// TestTheSealKeyPathsAreTheContractsAndNoOthersName: the three paths are this
// contract's own, they are distinct from each other and from every path the
// service already serves, and the release's is not a prefix under which a
// registration could be reached — wire.go's one-class-per-route rule read where
// it costs something, because the release is a JOB's call and the two
// registrations are a LAUNCHER's.
func TestTheSealKeyPathsAreTheContractsAndNoOthersName(t *testing.T) {
	// The service's paths as they stand, spelled here rather than collected:
	// a path added to the service is added here in the same change, which is
	// what makes this test the place a collision is caught.
	others := []string{
		MessagesPath, CountTokensPath, SignPath, VerifyPath, MintPath, ContextTokenPath,
		BitbucketReadPath, GitHubReadPath, MergeMintPath, LabelPath, IssuePath, SlipPath,
		SlipClaimPath, PoolDesignatePath, WorkflowSecretsPath, BuildTokenPath, BuildCredentialsPath,
		HeaderProvider, ProviderRoutePath, ProviderCountTokensPath,
	}
	mine := []string{SealKeyPath, SealKeyRegisterPath, SealKeyJobPath}
	seen := map[string]string{}
	for _, p := range mine {
		if !strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/") {
			t.Fatalf("%q is not a path", p)
		}
		if where, dup := seen[p]; dup {
			t.Fatalf("%q is two of this contract's paths: %s and another", p, where)
		}
		seen[p] = "the seal-key wire"
		for _, other := range others {
			if p == other {
				t.Fatalf("%q is both a seal-key path and a path the service already serves", p)
			}
		}
	}
	// The one prefix the service DOES branch on is the Bitbucket proxy's
	// (servedRoute asks it before the exact-path switch), so no path of this
	// contract may sit under it: a registration under that prefix would be
	// served as a proxied provider call, with that route's rules.
	for _, p := range mine {
		if strings.HasPrefix(p, BitbucketProxyPrefix) {
			t.Fatalf("%q is under the Bitbucket proxy prefix %q, so servedRoute would answer it as a proxied call", p, BitbucketProxyPrefix)
		}
	}
}

// TestTheRunClaimIsHeldToItsOneSpelling: the Run field is a claim a caller
// writes, and it is the two keys of a budget record's path — so a value that is
// not "<slug>/<run id>", or that holds a character a Realtime Database key may
// not, is refused before anything is read under it.
func TestTheRunClaimIsHeldToItsOneSpelling(t *testing.T) {
	for _, good := range []struct{ ref, slug, run string }{
		{"acme-web-1a2b3c4d5e6f7a8b/20261120-120000-abcd", "acme-web-1a2b3c4d5e6f7a8b", "20261120-120000-abcd"},
		{"acme-web-0000000000000000/1", "acme-web-0000000000000000", "1"},
	} {
		slug, run, err := checkRunRef(good.ref)
		if err != nil {
			t.Fatalf("checkRunRef(%q): %v", good.ref, err)
		}
		if slug != good.slug || run != good.run {
			t.Fatalf("checkRunRef(%q) = %q, %q; want %q, %q", good.ref, slug, run, good.slug, good.run)
		}
	}
	for _, bad := range []string{
		"", "/20261120-120000-abcd", "acme-web-1a2b3c4d5e6f7a8b/",
		"acme-web-1a2b3c4d5e6f7a8b/20261120-120000-abcd/extra",
		// A readable name is not a slug: task.ValidSlug is the one shape, and a
		// slug is what a record's path is keyed by.
		"acme-web/20261120-120000-abcd",
		"Acme-Web-1a2b3c4d5e6f7a8b/20261120-120000-abcd",
		"acme.web-1a2b3c4d5e6f7a8b/20261120-120000-abcd",
		"acme-web-1a2b3c4d5e6f7a8b/2026#120000",
		"acme-web-1a2b3c4d5e6f7a8b/2026$120000",
		"acme-web-1a2b3c4d5e6f7a8b/2026[120000]",
	} {
		if slug, run, err := checkRunRef(bad); err == nil {
			t.Fatalf("checkRunRef(%q) = %q, %q; a value of that shape keys no record", bad, slug, run)
		}
	}
}

// TestTheJobClaimIsHeldToABatchJobName: the Job field names a Cloud Batch job,
// which is what the digest is computed over and what the link answers with, so
// a value that is a Cloud Run job's name, a bare job id or a path with a
// wildcard in it is refused rather than digested.
func TestTheJobClaimIsHeldToABatchJobName(t *testing.T) {
	for _, good := range []string{
		"projects/my-project/locations/us-central1/jobs/fugaro-acme-web-20261120-120000-abcd",
		"projects/p/locations/europe-west1/jobs/a",
	} {
		if !validBatchJobNameRE.MatchString(good) {
			t.Fatalf("%q is a Batch job's full resource name and was refused", good)
		}
	}
	for _, bad := range []string{
		"fugaro-acme-web-20261120-120000-abcd",
		"projects/my-project/locations/us-central1/jobs/",
		"projects/my-project/locations/us-central1/jobs/A",
		"projects/my-project/locations/us-central1/jobs/*",
		"projects/my-project/locations/us-central1/jobs/a/b",
		// Cloud Run's spelling is eight fields and not this one.
		"projects/my-project/locations/us-central1/jobs/fugaro/executions/run",
	} {
		if validBatchJobNameRE.MatchString(bad) {
			t.Fatalf("%q is not a Cloud Batch job name and was accepted", bad)
		}
	}
}
