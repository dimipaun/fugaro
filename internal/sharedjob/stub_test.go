package sharedjob

import (
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
)

// TestStubsReturnTheContractShape is C1's red step and its whole test: this
// package declares shapes, and the stubs return them and decide nothing. Every
// assertion below is an assertion that a stub decided nothing — the identity,
// the profile, the repository and the workflow echoed, no repository admitted,
// no sentence refusing one, no profile resolved, no rule read off a profile.
//
// That is the property worth pinning while S1 to S4 are written in parallel
// against this contract: a side must not be able to mistake a stub's shape for
// an answer, and the shapes' zero values are shaped so that it cannot — a
// pairing that was never decided admits nothing.
//
// Mutation (run, restore): make StubPairing answer Allowed: true. A stub that
// admits is the one change that would let a side pass its own tests with
// nothing behind the contract, and this fails on it.
func TestStubsReturnTheContractShape(t *testing.T) {
	ident := RunIdentity{
		Account: "fugaro-standard-bitbucket-run@fugaro-dev.iam.gserviceaccount.com",
		Run:     "fb-20261113-193002-2c83",
		Repo:    "bitbucket/acme/sandbox",
	}
	p, err := StubPairing(ident, "standard-bitbucket")
	if err != nil {
		t.Fatalf("StubPairing: %v", err)
	}
	if p.Identity != ident {
		t.Errorf("the stub pairing carries the identity %+v, want the one it was called with (%+v): the shape echoes what it was asked about", p.Identity, ident)
	}
	if p.Profile != "standard-bitbucket" {
		t.Errorf("the stub pairing carries the profile %q, want %q", p.Profile, "standard-bitbucket")
	}
	if p.Allowed {
		t.Error("the stub pairing admitted a call: a stub decides nothing, and nothing a stub answers may read as an admission")
	}
	if p.Refusal != "" {
		t.Errorf("the stub pairing carries the refusal %q, want none: a refusal names the repository the call asked for, and the stub refuses nothing", p.Refusal)
	}
	var undecided Pairing
	if undecided.Allowed {
		t.Error("a Pairing nobody decided reads as an admission: the zero value is the shape's fail-closed state, and a pairing is allowed only when the side that paired it says so")
	}

	choice, err := StubJobChoice("bitbucket/acme/sandbox", "default")
	if err != nil {
		t.Fatalf("StubJobChoice: %v", err)
	}
	if choice.Repo != "bitbucket/acme/sandbox" || choice.Workflow != "default" {
		t.Errorf("the stub job choice names %q on %q, want the repository and the workflow it was called with", choice.Repo, choice.Workflow)
	}
	if choice.Profile != "" {
		t.Errorf("the stub job choice resolved the profile %q, want none: the stub resolves nothing, and a choice of no profile is what leaves a launch on its per-repository job", choice.Profile)
	}

	published := projectpolicy.Profile{JobProfile: config.JobProfile{
		Provider:       "bitbucket",
		CPU:            4,
		Memory:         "8Gi",
		TimeoutClass:   "long",
		AllowRepos:     []string{"acme/sandbox"},
		Secrets:        []string{config.BitbucketSecret},
		ClaudeAccounts: []string{config.DefaultClaudeAccount, "second"},
	}}
	bp := StubBitbucketProfile("standard-bitbucket", published)
	if bp.Name != "standard-bitbucket" || bp.Provider != "bitbucket" {
		t.Errorf("the stub profile is %q of %q, want the name it was called with on the provider the published profile names", bp.Name, bp.Provider)
	}
	if !slices.Equal(bp.AllowRepos, []string{"acme/sandbox"}) || !slices.Equal(bp.Secrets, []string{config.BitbucketSecret}) {
		t.Errorf("the stub profile carries allow_repos %v and secrets %v, want the published profile's own (%v, %v)",
			bp.AllowRepos, bp.Secrets, published.AllowRepos, published.Secrets)
	}
	// The account axis is the projection's one derivation, and it is the axis
	// of a job's name: the no-account job is the empty account, and the
	// published profile's "default" is that same job rather than a second one.
	if want := []string{"", "second"}; !slices.Equal(bp.Accounts, want) {
		t.Errorf("the stub profile carries the account axis %v, want %v: a job is one per (profile, provider, account), so the launcher and the deployer name the same jobs only from this list", bp.Accounts, want)
	}
	if bp.CPU != 4 || bp.Memory != "8Gi" || bp.TimeoutClass != "long" {
		t.Errorf("the stub profile carries cpu %d, memory %q and timeout_class %q, want the published profile's own (4, \"8Gi\", \"long\")", bp.CPU, bp.Memory, bp.TimeoutClass)
	}
	// A shape a side may edit is a shape that lets it edit the policy: the two
	// lists are the published profile's, and the projection is a copy — the
	// account axis included, which a caller must not be able to rewrite under
	// another side's feet.
	published.AllowRepos[0] = "acme/other"
	published.Secrets[0] = "another-secret"
	published.ClaudeAccounts[0] = "third"
	if !slices.Equal(bp.AllowRepos, []string{"acme/sandbox"}) || !slices.Equal(bp.Secrets, []string{config.BitbucketSecret}) {
		t.Errorf("the projected profile shares the published profile's own lists: it now carries allow_repos %v and secrets %v, and a side that edited the shape it was handed would have edited the policy",
			bp.AllowRepos, bp.Secrets)
	}
	if want := []string{"", "second"}; !slices.Equal(bp.Accounts, want) {
		t.Errorf("the projected profile's account axis moved with an edit to the published profile: it now carries %v, want %v", bp.Accounts, want)
	}
}
