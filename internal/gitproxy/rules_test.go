package gitproxy

import (
	"testing"

	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

const (
	// runBranch is the fb claim of the identity these tests decide for, and
	// jobRepo is the calling job account's repository (JobAccount.Repo): the
	// canonical, provider-qualified form the policy and the launcher use.
	runBranch = "fugaro/run-a"
	jobRepo   = "bitbucket/acme/sandbox"
)

// admittedBlock is the published block of an installation that proxies this
// repository: the repository on the allow-list, and proxy on (G3).
func admittedBlock() *projectpolicy.BitbucketToken {
	return &projectpolicy.BitbucketToken{AllowRepos: []string{"bitbucket/acme/*"}, Proxy: true}
}

// TestRulesPushOnlyRunBranch is this task's red step: before the rules
// existed, a Decide that allowed everything let a push to refs/heads/main
// through.
func TestRulesPushOnlyRunBranch(t *testing.T) {
	own := Request{
		Kind:        KindPush,
		Repo:        jobRepo,
		UpdatesRead: true,
		Updates:     []RefUpdate{{Old: oidA, New: oidB, Ref: "refs/heads/" + runBranch, HashLen: 40}},
	}
	if got := Decide(own, Identity{Run: "run-a", Branch: runBranch}, jobRepo, admittedBlock()); !got.Allow {
		t.Fatalf("the run's own branch was refused: %q", got.Reason)
	}
	other := own
	other.Updates = []RefUpdate{{Old: oidA, New: oidB, Ref: "refs/heads/main", HashLen: 40}}
	if got := Decide(other, Identity{Run: "run-a", Branch: runBranch}, jobRepo, admittedBlock()); got.Allow {
		t.Fatalf("a push to refs/heads/main was allowed (%q), want a refusal", got.Reason)
	}
}
