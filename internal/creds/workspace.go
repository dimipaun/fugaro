package creds

import (
	"errors"
	"fmt"

	"github.com/dimipaun/fugaro/internal/config"
)

// A workspace's shared pool as the member's own service sees it: what a
// credential the workspace holds means for one member, and what makes serving
// it safe (docs/workspaces/spec.md §4, D1298).
//
// **The effective set is an intersection, and the member's policy is the
// tighter side.** A credential the workspace holds is served to a member only
// when BOTH hold: the workspace's own per-project allow-list names that
// member's project, and the member's own policy admits the credential. The
// alternative the owner refused — a workspace credential available to every
// member (D1298's option (B)) — would let one member's compromise reach a
// credential every member's code is billed for, and would serve a credential a
// member's policy excludes, which is the policy bypass the only-tighten rule
// (internal/policy/merge.go) forbids. A workspace can therefore only tighten a
// member: it adds credentials the member's own policy may then admit, and it
// can never widen what that policy refuses.
//
// **The two refusals are told apart, because they are two different fixes.** A
// credential this project is not on the allow-list for is the workspace's
// answer, and an operator takes it to the workspace's owner; a credential the
// member's own policy excludes is the member's, and taking it to the workspace
// would send the operator somewhere that cannot fix it. The messages say which
// leg refused for exactly that reason, and neither repeats a credential's
// value: a name is all this file ever sees (F27).
//
// **This file holds no value and reads no policy.** It is the arithmetic of the
// intersection over names, so it can be tested without a bundle, a database or
// a key: the member side arrives as MemberPolicy, which the caller builds from
// the member's own published policy (credsvc's own reader), and the workspace
// side arrives as the allow-list a shared entry carries.

// Shared is one credential of a workspace's pool, beside the projects the
// workspace offers it to. It carries the credential (a name and a tier, never a
// value — Credential's own rule) and the per-project allow-list D1298 asks for,
// which is the same shape allow_data_to already uses for a model route.
//
// Projects are member project names (config.ProjectNameRE, the grammar a
// workspace and its members are both named by),
// and an empty list is a credential the workspace offers to nobody: an entry
// nobody is on the list for is not a widening, and reading an empty list as
// "everybody" would be the one mistake that turns a shared pool into D1298's
// refused option (B).
type Shared struct {
	Credential
	// Projects is the per-project allow-list: the member projects this
	// credential may be served to. It is the workspace's declaration and never
	// the member's, and the effective set intersects it with the member's own
	// policy (AdmitShared).
	Projects []string
}

// ErrNotShared is the refusal of a credential this member's project is not on
// the workspace's allow-list for. It names the workspace's side of the
// intersection and no value, and it is a refusal before the member's own policy
// is consulted: the workspace did not offer it, so there is nothing for the
// member to admit or refuse.
var ErrNotShared = errors.New("creds: the workspace does not share this credential with that project: its per-project allow-list does not name the member, so the credential is not served to it (D1298)")

// ErrMemberPolicy is the refusal of a credential the member's own policy
// excludes. It names the member's own policy as the reason, so an operator is
// not sent to the workspace for a refusal the member made, and it is the leg
// the member can fix without the workspace's owner.
var ErrMemberPolicy = errors.New("creds: the member's own policy excludes this credential, so the workspace's pool cannot serve it: a workspace can only tighten what a member admits (D1298)")

// MemberPolicy answers whether the member's own policy admits a credential of
// the workspace's pool, by the key that policy speaks. It is a func rather than
// a set because the caller reads it from the member's published policy and the
// key is the only thing this file may know about it: the policy may refuse an
// account, a provider or a model a credential serves, and answering that
// question is the policy reader's, not this file's.
//
// The key it is asked with is the credential's OWN name for every credential
// but one (policyKey), which is the pairing the caller does not have to know
// about: asking the member's policy in the pool's own spelling would be a
// question that policy has no entry for.
type MemberPolicy func(name string) bool

// policyKey is the key the MEMBER'S POLICY holds a pool credential under, which
// is the credential's own name for everything but the pool's ONE tier-B
// credential.
//
// That credential is the project's own Anthropic API key: the bundle holds it
// under config.AnthropicSecret (the name the service's own audit lines, the
// wire contract and the pool speak), while the member's policy keys it by
// config.AnthropicKeyAccount, the sentinel the accounts block's tier-B entry is
// derived under (projectpolicy's DeriveAccounts). The installation's own cascade
// pairs the two spellings the same way and for the same reason (credsvc's
// offeredToReview), and a pool-to-policy lookup that did not would ask the
// member's policy about a name it never holds — silently, because a miss is a
// refusal: the paid key would be offered by the workspace and admitted by
// nobody.
//
// The sentinel's arm matches the TIER as well as the name, exactly as
// offeredToReview's arm does, so what it translates is the paid key and never a
// credential that merely goes by that name: only this rule knows that the pool's
// tier-B credential is the one the member's policy files under the sentinel.
func policyKey(c Credential) string {
	if c.Tier == TierB && c.Name == config.AnthropicSecret {
		return config.AnthropicKeyAccount
	}
	return c.Name
}

// SharedWith reports whether the workspace's allow-list names this member's
// project. It is the workspace's leg alone: a caller asking the whole question
// asks AdmitShared, which asks the member's policy as well.
func (s Shared) SharedWith(project string) bool {
	for _, p := range s.Projects {
		if p == project {
			return true
		}
	}
	return false
}

// AdmitShared is D1298's intersection for one credential: the workspace's
// allow-list names the member's project AND the member's own policy admits the
// credential. A nil error is the credential being served; the error is
// ErrNotShared or ErrMemberPolicy, and each names the leg that refused.
//
// The workspace's leg is asked first, and that order is the design: a
// credential the workspace does not share with this project is not one the
// member's policy is asked about at all, so a member's policy can never appear
// to have admitted something the workspace never offered.
func AdmitShared(project string, s Shared, member MemberPolicy) error {
	if !s.SharedWith(project) {
		return fmt.Errorf("%w (credential %s)", ErrNotShared, s.Name)
	}
	if member == nil || !member(policyKey(s.Credential)) {
		return fmt.Errorf("%w (credential %s)", ErrMemberPolicy, s.Name)
	}
	return nil
}

// SharedFor is the effective set: every credential of the workspace's pool this
// member may be served, in the order the pool declares them and each once. It
// is the list Select's caller passes on as candidates, so it is the whole of
// what the workspace adds to a member's own credentials — and it never widens
// the member's set, because AdmitShared's two legs are both required here.
//
// A credential refused by either leg is dropped rather than reported: this is
// the set a call is offered, and the typed refusal an operator reads comes from
// AdmitShared, which answers about one named credential. Pooling the refusals
// here would mean the caller could not tell a credential the workspace withheld
// from one the member refused, which is the distinction the two errors exist
// for.
func SharedFor(project string, offered []Shared, member MemberPolicy) []Credential {
	var out []Credential
	seen := make(map[string]bool, len(offered))
	for _, s := range offered {
		if seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		if err := AdmitShared(project, s, member); err != nil {
			continue
		}
		out = append(out, s.Credential)
	}
	return out
}
