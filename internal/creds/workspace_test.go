package creds

import (
	"errors"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// TestWorkspaceCredentialIsRefusedWhenTheMembersPolicyExcludesIt: D1298's
// intersection is the whole reason a shared pool is safe. The workspace offers
// the credential to this project and serves it to nobody the member's own
// policy excludes, because the member's policy is the tighter side of "only
// tighten" (internal/policy/merge.go) and a service that served past it would
// be a policy bypass.
//
// The refusal is the member's own, and the test holds the two shapes of it
// apart: a credential the member's policy excludes is ErrMemberPolicy (the fix
// is the member's policy), which is a different answer from a credential the
// workspace does not share with this project at all (ErrNotShared, the fix is
// the workspace's allow-list). An operator sent to the workspace for a refusal
// the member made is the cost of getting the two confused.
func TestWorkspaceCredentialIsRefusedWhenTheMembersPolicyExcludesIt(t *testing.T) {
	shared := Shared{
		Credential: Credential{Name: "claude-oauth-token-team", Tier: TierA},
		Projects:   []string{"project-a"},
	}
	// The member's own policy refuses this credential, which its allow-list
	// admits: the intersection is the member's answer.
	excluded := func(string) bool { return false }
	err := AdmitShared("project-a", shared, excluded)
	if !errors.Is(err, ErrMemberPolicy) {
		t.Fatalf("AdmitShared with the member's policy excluding the credential = %v, want ErrMemberPolicy", err)
	}
	if errors.Is(err, ErrNotShared) {
		t.Errorf("the refusal = %v, which reads as the workspace's allow-list refusing it: it is the member's own policy that excludes it, and the operator is not sent to the workspace for a refusal the member made", err)
	}
	if got := SharedFor("project-a", []Shared{shared}, excluded); len(got) != 0 {
		t.Errorf("SharedFor = %v, want none: the effective set is the intersection and the member admits nothing", got)
	}

	// The same credential with the member's policy admitting it is served: the
	// refusal above is the policy's and not the shape's.
	admits := func(name string) bool { return name == "claude-oauth-token-team" }
	if err := AdmitShared("project-a", shared, admits); err != nil {
		t.Fatalf("AdmitShared with the member's policy admitting the credential = %v, want it served", err)
	}
	got := SharedFor("project-a", []Shared{shared}, admits)
	if len(got) != 1 || got[0] != shared.Credential {
		t.Fatalf("SharedFor = %v, want the one credential the member's policy admits", got)
	}
}

// TestWorkspaceCredentialIsRefusedForAMemberOutsideItsAllowList: the other leg
// of the intersection, and the one the workspace decides. A credential whose
// per-project allow-list does not name this project is refused *before* the
// member's policy is consulted, and the refusal says so: one member's
// compromise may not reach a credential the workspace shares with another, and
// a member whose policy would admit it is not served it (D1298's alternative
// (B), which the owner refused).
func TestWorkspaceCredentialIsRefusedForAMemberOutsideItsAllowList(t *testing.T) {
	shared := Shared{
		Credential: Credential{Name: "claude-oauth-token-team", Tier: TierA},
		Projects:   []string{"project-b"},
	}
	// A member's policy that admits everything changes nothing: the leg that
	// refuses is the workspace's own allow-list.
	admitsEverything := func(string) bool { return true }
	err := AdmitShared("project-a", shared, admitsEverything)
	if !errors.Is(err, ErrNotShared) {
		t.Fatalf("AdmitShared for a project the allow-list does not name = %v, want ErrNotShared", err)
	}
	if errors.Is(err, ErrMemberPolicy) {
		t.Errorf("the refusal = %v, which reads as the member's own policy refusing it: the workspace did not share this credential with this project at all", err)
	}
	if got := SharedFor("project-a", []Shared{shared}, admitsEverything); len(got) != 0 {
		t.Errorf("SharedFor = %v, want none: the workspace shares this credential with project-b and not with this project", got)
	}
	// The project the allow-list does name is served, with the same policy.
	if err := AdmitShared("project-b", shared, admitsEverything); err != nil {
		t.Fatalf("AdmitShared for the project the allow-list names = %v, want it served", err)
	}
}

// TestSharedAsksTheMembersPolicyInThePolicySOwnSpelling: the pool and the
// member's published policy spell the project's own Anthropic API key
// differently, and the member's leg is the one that has to pair them.
//
// The pool holds it under config.AnthropicSecret, the name the bundle, the wire
// contract and the service's audit lines speak; the member's `accounts` block
// files it under config.AnthropicKeyAccount, the sentinel projectpolicy's
// DeriveAccounts derives the tier-B entry under. A lookup in the pool's own
// spelling finds no entry, and a miss is a refusal — so the paid key would be
// offered by the workspace and admitted by nobody, silently, with no error for
// an operator to read. The pairing mirrors the installation's own cascade
// (credsvc's offeredToReview), tier included: a tier-A credential that merely
// goes by the paid key's name is not the credential the sentinel's entry
// answers for.
func TestSharedAsksTheMembersPolicyInThePolicySOwnSpelling(t *testing.T) {
	paid := Shared{
		Credential: Credential{Name: config.AnthropicSecret, Tier: TierB},
		Projects:   []string{"project-a"},
	}
	// The member's policy names the sentinel and nothing else, which is exactly
	// what its own publish path writes for the key.
	sentinelOnly := func(name string) bool { return name == config.AnthropicKeyAccount }
	if err := AdmitShared("project-a", paid, sentinelOnly); err != nil {
		t.Fatalf("AdmitShared for the paid key a policy names by the sentinel = %v, want it served", err)
	}
	got := SharedFor("project-a", []Shared{paid}, sentinelOnly)
	if len(got) != 1 || got[0] != paid.Credential {
		t.Fatalf("SharedFor = %v, want the paid key: the member's policy admits it under the sentinel", got)
	}

	// The pairing is the tier's as well as the name's. A credential that is not
	// the paid one — the same name at another tier — is asked about under its own
	// name and refused by a policy that names only the sentinel, which is what
	// keeps the sentinel's entry from admitting a credential it does not stand
	// for.
	notPaid := Shared{
		Credential: Credential{Name: config.AnthropicSecret, Tier: TierA},
		Projects:   []string{"project-a"},
	}
	if err := AdmitShared("project-a", notPaid, sentinelOnly); !errors.Is(err, ErrMemberPolicy) {
		t.Errorf("AdmitShared for a credential that merely goes by the paid key's name = %v, want ErrMemberPolicy: the sentinel stands for the tier-B key", err)
	}
	// And a policy that names the pool's own spelling — the way the pool speaks
	// it, which the publish path never writes — is what a tier-A credential is
	// asked about, so the translation is not a blanket substitution.
	poolSpelling := func(name string) bool { return name == config.AnthropicSecret }
	if err := AdmitShared("project-a", notPaid, poolSpelling); err != nil {
		t.Fatalf("AdmitShared for a tier-A credential the policy names as the pool does = %v, want it served", err)
	}
}

// TestSharedForKeepsEveryAdmittedCredentialOnce: the effective set is a set.
// A credential the workspace declares twice — the shape a hand-edited node or
// two publishes produce — is served once rather than twice, because the caller
// hands the answer to a selector and a doubled candidate is a credential the
// cascade would consider twice for one call.
func TestSharedForKeepsEveryAdmittedCredentialOnce(t *testing.T) {
	a := Shared{Credential: Credential{Name: "a", Tier: TierA}, Projects: []string{"project-a"}}
	b := Shared{Credential: Credential{Name: "b", Tier: TierB}, Projects: []string{"project-a"}}
	got := SharedFor("project-a", []Shared{a, a, b}, func(string) bool { return true })
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "b" {
		t.Fatalf("SharedFor = %v, want a and b once each", got)
	}
}
