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
// never serves one that policy does not NAME.
//
// **This file is the NAME leg of that intersection, and the repository leg is
// the service's.** A member's policy can also scope a credential by repository
// (`accounts.<name>.allow_repos`), and the reading of it is split between this
// file and the service: this arithmetic asks which NAMES the member's policy
// admits, and credsvc.Workspace.Shared asks, from the member's own database,
// whether the repository the calling run is on was admitted to that name
// (projectpolicy.PathRepo over the run identity's verified slug) and re-asks the
// member's two canonical `allow_repos` legs over the name it recovers from the
// account's own list (canonicalOfSlug). So a pooled credential is served only on
// repositories the member's own database admits it to AND the member's own lists
// still scope to it, and this file holds none of that: it is the arithmetic over
// names, and a caller wanting the set asks the service.
//
// What is not re-asked is one thing: the pairing of the run's repository with the
// calling job account. It is the member's own front door's check — the repository
// the calling job account belongs to against the run identity's slug — and a
// workspace's table names a member's job accounts and no repository, so there is
// no slug to hold the verified one to, and an account admitted for one repository
// can present a run identity of another. That is docs/workspaces/spec.md §8's
// residual risk 7, stated there and in projectpolicy.MemberAdmitsShared, and
// closing it is the owner's call: the repository per job account is a column the
// owner's per-member table does not carry.
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
// It is a model credential or a forge one, and the pool carries both
// (D2978, D2984, F15): a model entry is the name, the tier and the allow-list,
// and a forge entry adds the kind it is and the repositories the owner's
// credential reaches (Kind, Covers).
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
	// Kind is the kind of credential this entry is, for a FORGE entry: the
	// provider whose repositories it reaches, which is a kind a repository's
	// `git.provider` may be (config.Providers) and the first segment of every
	// repository Covers names (D2978, D2984, F15, and U2 in C3's words).
	//
	// It is EMPTY for a model credential, and that is the pool's own kind
	// rather than a missing answer: the pool was model-only until version 2 of
	// the owner's node (F15), so an entry that names no kind is read as what
	// the pool held before the column existed — which is what a version-1 node
	// decodes to, and what a value built without the column carries (C3's rule
	// 8). The reading widens nothing: a model entry is read from the bundle's
	// model index alone and a forge entry from the accessor of its own kind, so
	// the two arms can never answer for each other.
	Kind string
	// Covers are the concrete repositories a forge entry reaches, canonical
	// ("bitbucket/acme/app"), each with the entry's own Kind as its provider
	// segment: the workspace owner's own list of the repositories their
	// credential is stored for, and never the member's (U2, D2978). It is the
	// coverage a forge route holds a run's repository to, before the member's
	// own policy is asked as well (R5).
	//
	// It is empty for a model credential, which reaches no repository of its
	// own: the repositories a model call may be made for are the member's own
	// policy's business.
	Covers []string
}

// Forge reports whether this pool entry is a forge credential rather than a
// model one: it names a kind, and that is the whole of the difference (Kind's
// own doc states what the empty kind means).
//
// It exists so that the one place the two halves of the pool are told apart —
// the model cascade's candidate list, SharedFor — reads as the question it is
// asking, and so that a later reader of the pool asks it here rather than
// comparing Kind against "" somewhere of its own.
func (s Shared) Forge() bool { return s.Kind != "" }

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
// is the credential's own name for everything but the two the accounts block
// files under a value of its own: the project's paid key and the project's
// provider key.
//
// The first is the project's own Anthropic API key: the bundle holds it under
// config.AnthropicSecret (the name the service's own audit lines, the wire
// contract and the pool speak), while the member's policy keys it by
// config.AnthropicKeyAccount, the sentinel the accounts block's tier-B entry is
// derived under (projectpolicy's DeriveAccounts). The installation's own cascade
// pairs the two spellings the same way and for the same reason (credsvc's
// offeredToReview), and a pool-to-policy lookup that did not would ask the
// member's policy about a name it never holds — silently, because a miss is a
// refusal: the paid key would be offered by the workspace and admitted by
// nobody.
//
// The second is the project's provider key, and it splits the same way for the
// same reason (D2978, F7): the pool speaks config.ProviderKeySecret, which is
// the credential's own logical name and its document key, while the accounts
// block files that credential's axis value as config.ProviderKeyAccount
// (projectpolicy's DeriveAccounts, AK6a's second non-account value). Asking the
// member's policy for "provider-key" would be a question no conforming policy
// answers: the workspace's offering of the provider key would be dropped as
// ErrMemberPolicy for every member. It is a hazard as well as a miss, which is
// why the arm is the only reading that may stand: "provider-key" IS an account
// name an operator may use (ClaudeAccountNameRE admits it, and the account's
// document key is claude-oauth-token-provider-key), so a policy holding such an
// account would answer the pool's question with THAT account's allow_repos —
// one name admitting two credentials, the third-party key authorized by an
// account that is nothing to do with it.
//
// Each arm matches the TIER as well as the name, exactly as offeredToReview's
// does, so what is translated is the credential the accounts block files under
// that value and never a credential that merely goes by its name: a tier-A
// account named "provider-key" is asked about under its own name, which is the
// entry its own policy holds for it.
func policyKey(c Credential) string {
	if c.Tier == TierC && c.Name == config.ProviderKeySecret {
		return config.ProviderKeyAccount
	}
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
// **It is the MODEL list, and a forge entry is not in it.** The pool carries
// every kind the workspace serves (D2978, D2984, F15), and the cascade this
// list is offered to attaches a model credential and nothing else: a Bitbucket
// token or the App key among the candidates would be a candidate whose call
// cannot be made. So the forge entries are dropped here rather than filtered by
// each caller, and the forge routes read them from the pool itself, each with
// its own fences (Covers, and the member's policy — R5).
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
		if s.Forge() {
			continue
		}
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
