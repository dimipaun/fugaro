package credsvc

// The cascade (C1.11): which credential a call goes to, what an answer means,
// and what the run is told when nothing can serve it.
//
// Nothing here decides anything creds does not: the marks, the leases, the
// stickiness, the wait-or-switch rule and the tier order are internal/creds'
// (C1.9). This file builds the candidate list the policy admits, hands it to
// creds.Select, and turns one answer into a mark or into the client's answer.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/secretname"
)

// candidates narrows the bundle to the credentials this call may use, and
// reports the ones it did not, with why.
//
// It is the scope's narrowing in two halves (C1, R1). The SCOPE answers what the
// caller may be offered at all — the bundle's entries for an installation, the
// workspace's shared node over them for a member (Offering) — and this function
// narrows that list by what the scope's own policy admits and by what the
// repository's own record says. A tier-B credential needs no admission: it is
// the project's own paid key, and the policy that admits spending at all
// (`fallback: auto`) is the same policy that admits it. A tier-A credential
// needs O4's three-legged admission, and that is why the admission is read only
// when the list holds one: a repository with no shared token is never given
// tier A, and an installation whose bundle holds no tier-A credential never
// consults an admission at all (Global Constraint 4's drop, if it had applied).
//
// A review caller is the one caller whose candidate list is not this
// narrowing: the pairing has already resolved the ONE credential it may be
// served by, and the class buys no other (reviewCandidates). The scope answers
// that list as final (Offered), because a narrowed list would never contain an
// entry a review caller may fall to — and this function is where the two are
// told apart rather than after the selector, because the list is decided here.
//
// The project's crafts narrow it once more (poolOffering, MC6): a node that
// publishes crafts offers the union of what they go out on and nothing beside
// it, which is where a pool's members enter this list and where a credential no
// craft names leaves it. A node that publishes none — every installation today
// — is not narrowed at all. The list the crafts produce is kept in their own
// order, because that order is the owner's and the tier rule reads it: a pool's
// listed order only breaks a tie (F7).
//
// The strategy the list is served under travels back with it, because the same
// walk of the crafts answers both (poolOffering, MC7): a caller that took the
// list and not the strategy would serve a round-robin pool's members under the
// cascade's own rule and never rotate them. Every list that is not the crafts'
// — a review caller's, a node that publishes no crafts — is answered with the
// cascade's rule, which is what a list no strategy was written for has always
// had.
//
// It answers the candidates, the within-tier rule they are served by, the NAME
// of the call's own exclusive candidate when the call has one (personalCandidate)
// and the candidates the narrowing dropped. The name is the one of those that is
// not a property of the list, and it travels beside the list rather than as a
// field of a Credential for two reasons: serving a candidate outright is a fact
// about the CALL — the same token is one more member of the list in a call whose
// slip does not name it — and creds.Credential is the workspace service's own
// vocabulary of names, tiers and a provider, which its guard test holds to
// exactly that. The side that makes the selection is the side that says so
// (creds.Selector.Exclusive).
func (s *Server) candidates(ctx context.Context, id Identity, canonical string, shared *projectpolicy.Policy, b *Bundle) ([]creds.Credential, creds.Strategy, string, []Tried, error) {
	// The pool this call may be served from is the SCOPE's (C1, R1): which
	// credentials a call may be served is a fact about the caller the scope
	// verified — the member it resolved to — and the scope is what decides it,
	// out of the bundle's entries for an installation and out of the
	// workspace's shared node over them for a member. The identity and the
	// repository arrive as arguments because that is how the front door passes
	// them down; they were read from the scope (ServeHTTP), and so is every
	// reader below.
	sc := scopeOfCall(ctx, s)
	pool, err := sc.Offering().Offering(ctx, b)
	if err != nil {
		// The scope could not read what it offers: the refusal's own sentence is
		// the answer, and it travels as this error rather than as an empty list,
		// which the selector would answer with a sentence about the bundle.
		return nil, creds.StrategyFailover, "", nil, err
	}
	if id.JobClass == ClassReview {
		// A review caller's list is its own and final (reviewCandidates): that
		// class's credentials are decided before the cascade's own narrowing,
		// and what it is served is the account's read-only set rather than the
		// pool. The rule is the class's and not the scope's, because it is the
		// pairing's answer read one way for one class (spec §2.3); what the
		// scope answered above is asked for the same reason every other reader
		// is — it is the pool this call belongs to — and is then not what a
		// review call is served by.
		ra, ok := s.reviewAccount(id.JobAccount)
		if !ok {
			// The lookup is guarded although the pairing has already refused an
			// account this table does not hold (pairReview): the class is the
			// verifier's answer and the two lists are configured separately, so
			// a caller admitted as a review one whose email this table does not
			// carry is a deployment disagreeing with itself — and a service that
			// panicked on a state it cannot name would be the worse answer to
			// it.
			return nil, creds.StrategyFailover, "", nil, fmt.Errorf("%w: the calling account is not one of this service's review job accounts", ErrUnavailable)
		}
		out, skipped := reviewCandidates(reviewOfferings(b), ra.Account)
		return out, creds.StrategyFailover, "", skipped, nil
	}
	// The project's crafts narrow the pool one step further (poolOffering, MC6),
	// which is where a pool's members enter this list and where a credential no
	// craft names leaves it. A node that publishes no crafts block narrows
	// nothing and the call is served the whole pool — which is every
	// installation today.
	crafted, strategy, narrowed, err := poolOffering(shared, b)
	if err != nil {
		// The crafts leave this call nothing at all (poolRefusal): the refusal's
		// own sentence is the answer, and it names the crafts and not the bundle
		// an operator would otherwise go looking in.
		return nil, creds.StrategyFailover, "", nil, err
	}
	cands := pool
	var skipped []Tried
	if narrowed {
		// The crafts are a narrowing of the POOL and never a widening of it: the
		// list is the intersection, so a workspace's pool can only ever lose
		// entries to a node that names more, and every credential the crafts and
		// the pool disagree about is reported rather than dropped, so the run's
		// halt message reads the list it was served from and what the two
		// documents differed on.
		cands = heldBy(crafted, pool)
		skipped = append(skipped, craftsAndPoolSkips(crafted, pool)...)
		if len(cands) == 0 {
			// The crafts and this call's pool hold nothing in common: every name
			// the crafts go out on is one the pool does not hold, so this call
			// has no candidate at all. The refusal is the crafts' own
			// (poolRefusal) and not an empty list handed to the selector, which
			// answers an empty list with a sentence about the bundle — the one
			// document that is not at fault, since what disagrees is the
			// published node and the pool this call belongs to.
			return nil, creds.StrategyFailover, "", nil, &poolRefusal{message: poolRefusalHead +
				"the crafts go out on " + credentialNames(crafted) + ", and the pool this call belongs to holds " + credentialNames(pool)}
		}
	}
	// This repository's own named entries join the list the crafts left
	// (ownNamedEntries, KS3): the providers the call's own published policy
	// names and the repository's own Anthropic key where the bundle holds one.
	// AFTER the narrowing, because a craft's access names a credential of the
	// bundle's old vocabulary and every one of them is a tier A or B name
	// (craftOffering): an entry joined before the intersection would leave it
	// again here, reported as one more name the crafts and the pool disagreed
	// on, and the repository would be offered neither its provider key (F32) nor
	// its own Anthropic one (D3008) on exactly the projects that publish crafts.
	// A review caller above is served by its own list and never by this one, so
	// the accessor is read only for a caller that is served from a pool at all.
	cands = ownNamedEntries(cands, shared, b, id.Slug)
	hasA := false
	for _, c := range cands {
		if c.Tier == creds.TierA {
			hasA = true
			break
		}
	}
	// The admission is the scope's policy read (R1): the child node of the
	// repository the pairing resolved, intersected with the project's shared
	// node. For a workspace's member it is that member's own node, read from
	// the member's database — never the installation's, and never another
	// member's.
	admitted := map[string]bool{}
	if hasA {
		var err error
		admitted, err = s.admittedAccounts(ctx, id, canonical)
		if err != nil {
			return nil, creds.StrategyFailover, "", nil, err
		}
	}
	out := make([]creds.Credential, 0, len(cands))
	for _, c := range cands {
		if c.Tier == creds.TierA && !admitted[c.Name] {
			skipped = append(skipped, Tried{Name: c.Name, Tier: wireTier(c.Tier), Reason: ReasonNotAdmitted})
			continue
		}
		out = append(out, c)
	}
	// What the repository's OWN configuration says its runs go out on narrows
	// the admission once more, and it is read from the record `fugaro update`
	// wrote rather than from what this call declares (creds/repoauth, D2070 (a),
	// R7): the declaration is checked against the record and never trusted, and
	// a repository whose record says something this call does not declare is
	// refused rather than served the cascade (G16).
	//
	// The record is read through the scope's OWN reader and never through its
	// state (C1's rule 2): `fugaro update` writes it in the database of the
	// repository's own project, so an installation answers it out of its own
	// database and a workspace out of the MEMBER's, while the state the cascade
	// keeps its marks and leases in is the workspace's own — and a record read
	// out of the wrong database is an absent one, which narrowToRepoAuth reads
	// as "the cascade, unchanged" and would serve a member's run the whole of
	// the workspace's pool.
	rec, found, err := sc.RepoAuth().RepoAuth(ctx, id.Slug)
	if err != nil {
		return nil, creds.StrategyFailover, "", nil, err
	}
	var record *creds.RepoAuth
	if found {
		record = &rec
	}
	out, dropped, err := narrowToRepoAuth(out, record, id.Slug, id.AuthMode, id.AuthAccount)
	if err != nil {
		return nil, creds.StrategyFailover, "", nil, err
	}
	// R7's candidate goes in FRONT, and it goes in here rather than through any
	// narrowing above: what entitles a call to a person's own token is the slip
	// its owner was granted, and neither the project's crafts, the tier's
	// admission nor the repository's recorded auth has anything to say about it
	// (personal_workspace.go says why). Being first is not what serves it,
	// though: the list's order breaks an exact tie and says where a rotation
	// starts (creds' leastRecentlyExhausted, rotate), so a token offered beside
	// the shared entries as one more tier-A member would reach a call about
	// once in N — the tie-break's turn, or a pool's rotation. The name
	// therefore travels back with the list as the call's own exclusive
	// candidate, which the selection serves while it can serve, whatever rule
	// the list would otherwise be served by (creds.Selector.Exclusive, D3144).
	// A refusal is the call's own and not a dropped candidate: a slip naming a
	// personal token is served that token or refused, and the shared entries
	// are never served in its place while it can serve (D2974).
	exclusive := ""
	if personal, ok, err := s.personalCandidate(ctx, sc); err != nil {
		return nil, creds.StrategyFailover, "", nil, err
	} else if ok {
		out = append([]creds.Credential{personal}, out...)
		exclusive = personal.Name
	}
	return out, strategy, exclusive, append(skipped, dropped...), nil
}

// personalCandidate is R7's candidate for one call: the person's own token,
// when the call carries a slip naming one and the member's switch serves it.
//
// It is asked of the SCOPE and never of the bundle, and only where the scope is
// a workspace's: an installation's scope hands out no person half, so an
// installation answers no candidate here and its path is exactly what it was
// (C2, WC5's A3). **This is the ONE place the candidate is assembled** — an
// entry prepended anywhere else would be one more member of its tier, served by
// the tie-break's turn or a pool's rotation instead of outright (D3144) — and
// it is the list the class the reader answers travels out of. The slip handed
// over is the call's VERIFIED binding — the one the fence above reads, and never
// anything the call says — which is the whole of what C2's reader is given.
//
// A failure here is the reader's own refusal and it travels as this call's
// (the switch off, a policy or a roster that could not be read, R11): it is
// returned rather than swallowed, because a call under a slip that names a
// personal token must be refused where the token cannot be served, and the
// sentence the reader answered is the one the run is refused with — the
// `poolRefusal` this call's carries reaches `candidates`' own branch and is
// answered verbatim, never collapsed into a sentence of the call path's.
func (s *Server) personalCandidate(ctx context.Context, sc Scope) (creds.Credential, bool, error) {
	ps, ok := personHalfOf(sc)
	if !ok {
		return creds.Credential{}, false, nil
	}
	r := ps.Personal()
	if r == nil {
		// A workspace scope that holds no reader serves no personal candidate,
		// which is the zero configuration C2 says must serve nothing rather
		// than fail (PersonScopeConfig).
		return creds.Credential{}, false, nil
	}
	b, ok := callSlipOf(ctx)
	if !ok {
		// No verified slip: a call of the personal half is under one, and a
		// call without one has no personal candidate at all (R8).
		return creds.Credential{}, false, nil
	}
	return r.Personal(ctx, creds.Slip{ID: b.id, Owner: b.owner, Allowed: b.allowed})
}

// personHalfOf answers the workspace's own half of one call's scope, and whether
// this scope has one at all.
//
// It has TWO entries because the merged halves reach it two ways, and C2 admits
// both. A DEPLOYMENT's scope hands its half out and declares none of the four
// methods itself (WC4's Persons, which the slip routes dispatch on too, and
// whose guard test pins the four to the half's own type: a second declarer would
// be a second deployment that could serve a person's token) — so a scope is
// asked through the hand-out first. A scope that declares C2's four methods
// itself IS the half, which is the shape C2's own words describe ("a second
// interface a scope may also implement") and the shape WC5's fixture has.
//
// A scope that answers neither — every installation's, and a workspace's before
// its half is wired — hands out no half, and `personalCandidate` is what turns
// that into "no candidate" (C2: absent serves nothing rather than failing).
func personHalfOf(sc Scope) (PersonScope, bool) {
	if ps, ok := personScopeOf(sc); ok {
		return ps, true
	}
	direct, ok := sc.(PersonScope)
	return direct, ok
}

// ownNamedEntries adds this repository's own named entries to the list the
// crafts left (candidates): the offerings KS3 builds, read out of the bundle
// through C1's accessor (contract-provider.md, ReadProviderKey).
//
// They are two facts of the same family (R2, D3008), and both are derived from
// the identity the service verified and never from anything the call says:
//
//   - one entry per provider the call's own published policy names — the
//     project's `providers` block, whose keys are the providers a run may route
//     a model to (config.ModelProvider) and whose names are the credential half
//     of the family's key. It is offered at tier C, and it carries the provider
//     it is the key OF (creds.Credential.Provider): one provider's key serves
//     one provider's models, and the tie is the only thing that keeps a list of
//     several providers' keys from being served the wrong one — which models a
//     key may serve at all is the selection's (creds' D3000 gate), not this
//     function's.
//   - the repository's OWN Anthropic key, where the bundle holds one, at tier B
//     (D3008). A repository with such an entry is served that key and not the
//     project's — which narrowToRepoAuth is what enforces, because the two are
//     both tier B and the paid tier is not what tells them apart.
//
// An entry the bundle does not hold is not offered: ReadProviderKey is what
// answers that, and a candidate the service would then fail to attach is worse
// than no candidate at all — the accessor's "no such credential" is the answer
// that serves nobody, which is what a repository whose key was never copied
// must meet.
//
// The names are offered in a settled order (the providers sorted by name, the
// Anthropic key after them) because the offered order is what breaks a tie
// within a tier (F7), and a map's iteration order would make a tie a matter of
// the Go runtime's choice. A name the list already holds is not offered twice:
// the scope's offering may hold the same entry once KS1's bundle answers the
// family (a candidate offered twice is refused by checkCandidates, which is a
// refusal and not a duplicate).
//
// The list is a copy and never the scope's own slice: what a reader answered is
// the reader's, and this function only ever adds to it.
func ownNamedEntries(cands []creds.Credential, shared *projectpolicy.Policy, b *Bundle, slug string) []creds.Credential {
	var providers []string
	if shared != nil {
		providers = make([]string, 0, len(shared.Providers))
		for name := range shared.Providers {
			providers = append(providers, name)
		}
		slices.Sort(providers)
	}
	extra := make([]creds.Credential, 0, len(providers)+1)
	for _, name := range providers {
		c, ok := ownNamedEntry(b, slug, name, creds.TierC)
		if !ok {
			continue
		}
		// The provider this key is the key OF, which is the loop's own name:
		// the policy's providers block is what named this credential, and the
		// family's key carries it as the credential half.
		c.Provider = name
		extra = append(extra, c)
	}
	if c, ok := ownNamedEntry(b, slug, config.AnthropicKeyAccount, creds.TierB); ok {
		extra = append(extra, c)
	}
	if len(extra) == 0 {
		return cands
	}
	out := make([]creds.Credential, 0, len(cands)+len(extra))
	out = append(out, cands...)
	for _, c := range extra {
		if _, held := lookupCredential(cands, c.Name); held {
			continue
		}
		out = append(out, c)
	}
	return out
}

// ownNamedEntry is one named entry of C1's family, offered with the tier the
// call's list carries it at: the bundle's answer about this repository and this
// credential's name, turned into the candidate's name — or nothing at all, for
// an entry the bundle does not hold or a pair the family's naming rule refuses
// (which no bundle can hold either, RepoCredentialKey being what writes one).
func ownNamedEntry(b *Bundle, slug, credential string, tier creds.Tier) (creds.Credential, bool) {
	if _, ok := ReadProviderKey(b, slug, credential); !ok {
		return creds.Credential{}, false
	}
	name, err := RepoCredentialKey(slug, credential)
	if err != nil {
		return creds.Credential{}, false
	}
	return creds.Credential{Name: name, Tier: tier}, true
}

// lookupCredential is a candidate list's member by name, for the callers that
// ask about a list this file built and not about the selection's own copy of
// one (creds' lookup is that one's).
func lookupCredential(cs []creds.Credential, name string) (creds.Credential, bool) {
	for _, c := range cs {
		if c.Name == name {
			return c, true
		}
	}
	return creds.Credential{}, false
}

// The two auths a non-cascade record may name that this service can serve a
// call by, spelled as config.AgentAuths spells them.
const (
	authModeOAuth  = "oauth"
	authModeAPIKey = "api-key"
)

// narrowToRepoAuth narrows a call's admission to what its repository's own
// recorded auth says it goes out on, and it is the whole of B2.1's boundary.
//
// A nil record is a repository no update has recorded, and a `cascade` one is a
// repository whose runs are the cascade: both keep today's list, because an
// installation this release has not written, and a repository that asks for the
// cascade, are served exactly as they were before this narrowing existed.
//
// Two things are settled before the record is read, because they are about
// WHICH repository's credentials these are and not about the auth they go out
// on:
//
//   - the named entries of C1's family are this repository's own (R2, D3008).
//     Every member of the family carries the repository it was entered for, and
//     a call is served only its own: another repository's entry is dropped
//     here, which is what makes "a repository is never served another
//     repository's Anthropic key" a fact of this function rather than of the
//     order the bundle happens to list its entries in (F42).
//   - a repository that has an Anthropic key of its OWN, entered for it
//     (D3008), is served that one, and the project's key is dropped: the two
//     are both tier B, so the tier cannot tell them apart and this is the one
//     place that can. A repository the bundle holds no own entry for keeps the
//     project's key, exactly as it is served it today.
//
// Every other record serves the call only what it names:
//
//   - `oauth` keeps the ONE tier-A credential of the declared account, and the
//     record has to name that account. A record that names none of its own
//     workflows' accounts is one nothing can be decided from, and a call that
//     declares an account its repository does not use is refused rather than
//     served — the paid key beside that subscription is not this repository's.
//     It keeps every named provider credential as well: a provider's key is not
//     a subscription and not the project's own key, and which models it serves
//     is a question about the model and never about the auth the repository's
//     Claude calls go out on (F32, D3007). Without that, a repository on oauth
//     would be offered no provider credential at all, whatever its policy names.
//   - `api-key` keeps tier B and every named provider credential, and no
//     account may be declared beside it: an account names a subscription, and a
//     call that declares both has declared two contradictory things.
//   - any other recorded auth (`vertex`) is refused. The service holds no
//     credential of that kind — the built-in Vertex credential's calls are the
//     project's own GCP service and are made where the run authenticates as
//     vertex — so a repository whose record says so is served nothing here.
//
// The mode is checked against the record and not the other way round: a call
// that declares nothing where its record says `oauth` is refused, which is the
// case the boundary exists for — the agent holds the run identity, so "I
// declared nothing" must not be a way to the whole cascade.
//
// The list it answers is a narrowing of the admission this function is handed
// and never a widening of it: the declared account's credential has to be in
// the list already, so a record can only ever take away from what O4's
// admission, the crafts and the bundle allowed (G16's direction).
//
// A declared account is normalized the way the record's own writer normalizes
// one (config.ReviewVariantAccount), so the unsuffixed default account may be
// declared as the empty string or by its name and means the same credential.
// That is not a hole: the record still has to name the account, so a call that
// declares none is served the default account only where its repository's own
// configuration names that account.
func narrowToRepoAuth(cands []creds.Credential, rec *creds.RepoAuth, slug, mode, account string) ([]creds.Credential, []Tried, error) {
	cands, own := ownRepositorysEntries(cands, slug)
	cands, preferred := ownKeyBeforeTheProjects(cands, slug)
	settled := append(own, preferred...)
	if rec == nil || rec.Auth == config.AuthCascade {
		return cands, settled, nil
	}
	if mode != rec.Auth {
		return nil, nil, &poolRefusal{message: fmt.Sprintf("this repository's recorded auth is %s, and this call %s: the credentials service serves a repository only the auth its own configuration records, and never the cascade (docs/secrets-brokering, D2070)", rec.Auth, declaredAuthText(mode))}
	}
	if account != "" && rec.Auth != authModeOAuth {
		return nil, nil, &poolRefusal{message: fmt.Sprintf("this repository's recorded auth is %s, and this call declares the account %q beside it: an account names a subscription, and the credentials service serves a repository only the auth its own configuration records (docs/secrets-brokering, D2070)", rec.Auth, account)}
	}
	switch rec.Auth {
	case authModeOAuth:
		account = config.ReviewVariantAccount(account)
		if !rec.NamesAccount(account) {
			return nil, nil, &poolRefusal{message: fmt.Sprintf("this repository's recorded auth is oauth, and this call declares the account %q, which its record does not name (it names %s): the credentials service serves a repository only the accounts its own configuration records (docs/secrets-brokering, D2070)", account, rec.AccountText())}
		}
		out, dropped := keepCandidates(cands, func(c creds.Credential) bool {
			return c.Tier == creds.TierC || (c.Tier == creds.TierA && c.Name == account)
		})
		if len(out) == 0 {
			return nil, dropped, &poolRefusal{message: fmt.Sprintf("this repository's recorded auth is oauth and its record names the account %q, and no credential this call may be served by is that account's: the account is not in this service's bundle, or the repository is not admitted to it (docs/secrets-brokering, D2070)", account)}
		}
		return out, append(settled, dropped...), nil
	case authModeAPIKey:
		out, dropped := keepCandidates(cands, func(c creds.Credential) bool {
			return c.Tier == creds.TierB || c.Tier == creds.TierC
		})
		if len(out) == 0 {
			return nil, dropped, &poolRefusal{message: "this repository's recorded auth is api-key, and this service's bundle holds no paid key to serve it by (docs/secrets-brokering, D2070)"}
		}
		return out, append(settled, dropped...), nil
	}
	return nil, nil, &poolRefusal{message: fmt.Sprintf("this repository's recorded auth is %s, which the credentials service does not serve a call by: only a repository whose auth is cascade, oauth or api-key is served here (docs/secrets-brokering, D2070)", rec.Auth)}
}

// ownRepositorysEntries drops every named entry of C1's family that belongs to
// another repository (R2, D3008, F42), reporting each as a credential this call
// may not be served by.
//
// The family's key carries its repository (RepoCredentialByKey), so this is a
// read of the name and never a second lookup: the entry a call may be served is
// the one the SERVICE built for its own verified repository, and an entry whose
// name says another repository is one this call could only have been offered by
// a bundle listing every repository's keys. The reason it is reported rather
// than dropped in silence is the run's halt message: it reads the list it was
// served from and what was left out of it, and "not admitted" is the closed
// reason set's own word for a credential a repository's configuration leaves
// out (keepCandidates).
func ownRepositorysEntries(cands []creds.Credential, slug string) ([]creds.Credential, []Tried) {
	return keepCandidates(cands, func(c creds.Credential) bool {
		owner, _, ok := RepoCredentialByKey(c.Name)
		return !ok || owner == slug
	})
}

// ownKeyBeforeTheProjects drops the project's own Anthropic key where this
// repository has an entry of its own (D3008): one repository, one entry for its
// Anthropic key, and the repository's own is the one its runs are served.
//
// The project's key is named by the bundle's own spelling of it
// (config.AnthropicSecret, at tier B, CredentialKey) and the repository's own by
// the family's (RepoCredentialKey with config.AnthropicKeyAccount) — the two
// names R2 keeps apart on purpose, so a provider may be called
// "anthropic-api-key" without its entry colliding with the repository's.
//
// Nothing happens for a repository whose own entry the list does not hold: the
// project's key is then exactly what it is served, which is what every
// installation does today and what an installation that copies no key keeps
// doing.
func ownKeyBeforeTheProjects(cands []creds.Credential, slug string) ([]creds.Credential, []Tried) {
	own, err := RepoCredentialKey(slug, config.AnthropicKeyAccount)
	if err != nil {
		return cands, nil
	}
	if _, held := lookupCredential(cands, own); !held {
		return cands, nil
	}
	return keepCandidates(cands, func(c creds.Credential) bool {
		return !(c.Tier == creds.TierB && c.Name == config.AnthropicSecret)
	})
}

// declaredAuthText is what a call declared, as one clause of a refusal: the
// auth by name, or the fact that it declared none. The name is a value of
// config.AgentAuths and the clause is never the caller's own text.
func declaredAuthText(mode string) string {
	if mode == "" {
		return "declares no auth"
	}
	return fmt.Sprintf("declares the auth %q", mode)
}

// keepCandidates splits a list by one rule, reporting what it dropped as the
// credentials this call may not be served by (ReasonNotAdmitted, the reason a
// review caller's list is built by and the reason the crafts' own narrowing
// reports). The reason set is closed, so a credential a repository's own
// configuration leaves out is "not admitted" and not a fifth reason.
func keepCandidates(cands []creds.Credential, keep func(creds.Credential) bool) ([]creds.Credential, []Tried) {
	out := make([]creds.Credential, 0, len(cands))
	var dropped []Tried
	for _, c := range cands {
		if keep(c) {
			out = append(out, c)
			continue
		}
		dropped = append(dropped, Tried{Name: c.Name, Tier: wireTier(c.Tier), Reason: ReasonNotAdmitted})
	}
	return out, dropped
}

// reviewOfferings is the set a review caller is offered FROM: the bundle's
// model credentials and, beside them, the project's provider key — the one
// entry of the bundle that is in no coding caller's list (Credentials) and is
// in this one (AK6b, F15).
//
// The entry is offered and not filtered out, because the pairing is what
// decides who may be served by it and the LIST is what carries the pairing to
// the selector: a review run paired with the account axis's value for that
// credential (config.ProviderKeyAccount) is offered it, and creds.Select then
// refuses it by name — tier C is the reserved third-party route, and the
// refusal's sentence names Check 50 (D150, C3.6) — which is the answer the run
// is owed. Dropping the entry here would answer the same call with the other
// sentence ("no credential is offered") and would be this file deciding a
// question that belongs to the selector.
//
// The append is safe on the accessor's own copy: Credentials answers a fresh
// slice, so nothing here writes into the bundle's entries.
func reviewOfferings(b *Bundle) []creds.Credential {
	all := b.Credentials()
	if e, ok := b.ProviderKey(); ok {
		all = append(all, creds.Credential{Name: e.Name, Tier: e.Tier})
	}
	return all
}

// reviewCandidates is the candidate list of a review caller: the pairing's ONE
// credential, and nothing else at all.
//
// The class buys three routes and no privilege beyond them (spec §2.3), and on
// the model path that is exactly this list: a review call is served by the
// credential its job is of, never by another tier-A account's entry, and never
// by a tier B or C one — so a review call has nothing for the cascade's ladder
// to fall to even where the policy's `fallback` would allow one (spec §10).
//
// The credential is the one the pairing resolved, in one of the axis's three
// spellings (docs/api-key-review, D1417, D1419):
//
//   - an account name, which is the name of the tier-A credential in the
//     bundle: the pairing resolved it from the published admissions, so the two
//     are the same string by construction and not by a second lookup;
//   - config.AnthropicKeyAccount, the sentinel that is not an account: it names
//     the project's own Anthropic API key, which the bundle holds at tier B
//     under its own secret's name (config.AnthropicSecret), and the entry
//     offered is that one — matched by name AND tier, so a bundle that grew a
//     second tier-B credential would never have it offered to a review caller
//     (the class is served by the key it named and by nothing beside it);
//   - config.ProviderKeyAccount, the outage sentinel: it names the project's
//     provider key, which the bundle holds at tier C under its own logical name
//     (config.ProviderKeySecret, AK6b). The entry is offered the same way — by
//     name and tier together — and the offer is where the CALL is refused:
//     creds.Select serves a tier-C credential to every model but a Claude one
//     (D3000), and nothing fills Selector.Model yet, which reads as a Claude
//     one: so the run paired with this value reads the refusal that names Check
//     50 (checkCandidates, D150) and reaches no provider until that check has
//     run. That is what this half is for: the bundle can hold the key and the
//     service can forward with it, and nothing routes a Claude model to a third
//     party yet.
//
// Every credential it did not offer is reported as not admitted, which is what
// that reason means for a caller: this call may not be served by it. A new
// reason would be a value this contract's closed set does not carry, and the
// runner reads that set to build a run's halt message.
func reviewCandidates(all []creds.Credential, account string) ([]creds.Credential, []Tried) {
	var out []creds.Credential
	var skipped []Tried
	for _, c := range all {
		if offeredToReview(c, account) {
			out = append(out, c)
			continue
		}
		skipped = append(skipped, Tried{Name: c.Name, Tier: wireTier(c.Tier), Reason: ReasonNotAdmitted})
	}
	return out, skipped
}

// offeredToReview reports whether one credential is the one a review caller
// paired with account may be served by, and it is the whole of the rule above.
//
// No two of the spellings can overlap: both sentinels carry a doubled dash,
// which config.ClaudeAccountNameRE refuses, so no account name is ever one of
// them and no arm can answer for another's value. The sentinels' arms match the
// tier as well as the name, so what one offers is the project's key at tier B,
// and what the other offers is the provider key at tier C, and never a
// credential that merely goes by either name — the bundle is free to grow
// entries, and only this rule knows that a review caller paired with one of
// these values is served by that credential or by nothing at all.
func offeredToReview(c creds.Credential, account string) bool {
	switch account {
	case config.AnthropicKeyAccount:
		return c.Tier == creds.TierB && c.Name == config.AnthropicSecret
	case config.ProviderKeyAccount:
		return c.Tier == creds.TierC && c.Name == config.ProviderKeySecret
	}
	return c.Tier == creds.TierA && c.Name == account
}

// admittedAccounts is O4's admission, and it is an intersection of three
// published facts rather than a read of one (the review of C1.2, m-2 and
// m-3):
//
//   - the repository's own node names the accounts an administrator admitted
//     it to (C1.2). It is what was written once, by hand, and it can be wider
//     than the policy — two concurrent publishes are a multi-path patch with
//     no compare-and-set, and a node can be edited by hand;
//   - the shared node's accounts.<name>.allow_repos says the account itself is
//     offered to this repository;
//   - the shared node's allow_repos says the project admits the repository at
//     all. That is the leg that closes the `project repos rm` gap, which
//     removes a repository from the allow-list and does not touch its child.
//
// An absent child is an empty admission, never an error: a repository whose
// administrator has admitted it to nothing is admitted to nothing. An
// unreadable child or node is an error the caller refuses on — a reader that
// turned one into an empty set would hand tier A to nobody, which is the same
// answer by luck and the wrong one by construction (fail closed is a refusal,
// not a silent nothing).
//
// A shared caller is narrowed once more, and that narrowing is the class's
// whole difference on the model path (B5.1): the account it may be served by is
// one of the PROFILE's, read live from the published node the row names
// (projectpolicy.ProjectJobAccounts over the profile's job profile, spelled the
// way the accounts block spells the default). A shared job exists so that one
// job serves a profile's repositories, and what it may be served is what that
// profile is for — the account axis and nothing wider. The other two classes
// are unchanged: a repository caller's list is the bundle narrowed by the
// repository's own admission, and a review caller's is decided before this
// function is ever reached (reviewCandidates).
//
// The profile is the identity's own — id.Profile, which only a shared caller
// carries and which the verifier read from this service's own row — and never
// anything the caller sent (G21, spec §2.1).
//
// The policy all three facts are read from is the call's SCOPE's (R1) and not
// this server's configuration: the installation's own published node for a call
// an installation serves, and the member's for one a workspace serves, which is
// what lets one admission serve both deployments. It is read from the call's
// context, exactly as `candidates` reads its own scope (scopeOfCall).
func (s *Server) admittedAccounts(ctx context.Context, id Identity, canonical string) (map[string]bool, error) {
	policy := scopeOfCall(ctx, s).Policy()
	child, found, err := policy.Admissions(ctx, id.Slug)
	if err != nil {
		return nil, err
	}
	if !found || child == nil || child.Credentials == nil {
		return map[string]bool{}, nil
	}
	shared, found, err := policy.Shared(ctx)
	if err != nil {
		return nil, err
	}
	if !found || shared == nil {
		return map[string]bool{}, nil
	}
	if !projectpolicy.AdmitRepo(shared.AllowRepos, canonical) {
		return map[string]bool{}, nil
	}
	out := make(map[string]bool, len(child.Credentials.Accounts))
	for _, name := range child.Credentials.Accounts {
		acct, ok := shared.Accounts[name]
		if !ok {
			// An account the child names and the node does not define. The
			// intersection is empty for it, which is what an intersection
			// means; the publish path refuses to write such a child, and a
			// hand-edit that wrote one is admitted to nothing.
			continue
		}
		if projectpolicy.AdmitRepo(acct.AllowRepos, canonical) {
			out[name] = true
		}
	}
	if id.JobClass != ClassShared {
		return out, nil
	}
	profile, err := shared.ProfileFor(id.Profile)
	if err != nil {
		// The row names a profile the published node does not define: the
		// service's configuration and the project's own document disagree, and
		// the account axis cannot be answered from a profile nobody defines —
		// so it is a refusal (fail closed), not an empty set.
		return nil, fmt.Errorf("%w: the calling job's profile is not one the project's policy defines", err)
	}
	for name := range out {
		if !slices.Contains(profileAccounts(profile), name) {
			delete(out, name)
		}
	}
	return out, nil
}

// profileAccounts is the account axis of one published profile, spelled the way
// the policy's accounts block spells an account: config.ProjectJobAccounts
// answers the job-name axis, where the no-account job is the empty string, and
// the accounts block spells that same account as the default one
// (config.ReviewVariantAccount is the one spelling of the pair, and it is
// config's rather than a second copy here).
//
// The two sentinels travel with it: a profile that lists the Anthropic key or
// the provider key names a job that holds that credential instead of an OAuth
// token, and admitting those names for such a profile is what lets the
// credential its own job holds be selected (ProjectJobTokenAccount's rule,
// read in the other direction). A profile that lists neither never admits
// them, because the list is the profile's own.
func profileAccounts(profile projectpolicy.Profile) []string {
	names := config.ProjectJobAccounts(profile.JobProfile)
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, config.ReviewVariantAccount(name))
	}
	return out
}

// settingsOf is the published policy as creds.Select enforces it. Every
// member is read through a nil-safe accessor of the policy's own — including
// D142's two controls, which are pointers and are read through
// StopsAfterExhaustion and ConcurrencyCap rather than off the block — so an
// installation that publishes no credentials block reads the protective
// defaults (oauth-only, the 15-minute threshold, both controls at their
// protective values) rather than a zero no comparison matches, and a policy
// whose block is absent does not reach a nil *Credentials at all. That is the
// case Decode leaves for a document with no `credentials` key, and it is every
// call's case: an absent block is a policy, not a malformed one.
//
// The two controls are handed to creds as pointers to the values the accessors
// answer, which is what creds' own members mean: nil is the absent key and
// reads as the same default (stops, concurrency) the accessor answers, so
// spelling it out cannot change which control is in force — and it keeps the
// default in one place, the policy's, rather than in a second one here.
//
// Window and LeaseTTL are left zero: both are creds' own bounds (the
// subscription window D142's first control measures, and the lease TTL), they
// are not policy fields, and Settings.withDefaults fills them.
func settingsOf(p *projectpolicy.Policy) creds.Settings {
	var c *projectpolicy.Credentials
	if p != nil {
		c = p.Credentials
	}
	fallback := creds.OAuthOnly
	if c.FallbackValue() == projectpolicy.FallbackAuto {
		fallback = creds.Auto
	}
	stops := c.StopsAfterExhaustion()
	concurrency := c.ConcurrencyCap()
	return creds.Settings{
		Fallback:                     fallback,
		WaitThreshold:                c.WaitThresholdDuration(),
		StopAfterExhaustionPerWindow: &stops,
		ConcurrencyPerToken:          &concurrency,
		BackoffInitial:               c.BackoffInitialDuration(),
		BackoffCap:                   c.BackoffCapDuration(),
	}
}

// wireTier is a tier as the wire spells it. It answers "" for a tier this
// release does not select, which every writer of the contract refuses — so a
// tier the cascade invented would be a refusal and not a header.
//
// Tier C is spelled although this release selects no tier-C credential: the
// provider key reaches a candidate list as a tier and not as a name (AK6b,
// F15), so a Tried row that reported it, or an answer that carried it, names
// the tier the contract spells rather than one it has no word for — the same
// rule read the other way round.
func wireTier(t creds.Tier) string {
	switch t {
	case creds.TierA:
		return TierA
	case creds.TierB:
		return TierB
	case creds.TierC:
		return TierC
	}
	return ""
}

// entryFor resolves a selected candidate to the bundle entry that holds its
// value. It is a selection by KIND for a tier-C candidate and a lookup by name
// for every other, and the difference is the accessor's own reason: the provider
// key is in the document and NOT in byName, because an account may legally be
// named after it, so a lookup by name alone could answer that account's token
// where the provider's key was meant — one name, two credentials, and the value
// attached to a call would be the one nobody chose.
//
// It fails closed on both halves: a name the bundle does not hold is not an
// entry, and a tier-C candidate whose name is not the provider entry's is not
// an entry either. What it answers is the credential that was selected or
// nothing, and the caller refuses on nothing rather than forwarding an empty
// credential (D143).
func entryFor(b *Bundle, c creds.Credential) (Entry, bool) {
	if c.Tier == creds.TierC {
		e, ok := b.ProviderKey()
		if !ok || e.Name != c.Name {
			return Entry{}, false
		}
		return e, true
	}
	return b.Lookup(c.Name)
}

// resolveEntry is R6's order read at the one place a selected candidate becomes
// a value: the per-secret container the candidate names first (DS3.3), and the
// bundle's entry second. It is entryFor with the per-secret read in front of
// it, and the bundle half is that function, unchanged.
//
// The two halves, and why each is where it is:
//
//   - the per-secret read, for a candidate the grammar names: a user-supplied
//     credential is the container the intake wrote when its owner rotated it,
//     and it is served ahead of the bundle's entry because the bundle's is the
//     value that was there BEFORE the rotation (R6). A candidate the grammar
//     does not admit borrows nothing here — it is a bundle credential's name,
//     an account's or the project's own — and it is served exactly as it was
//     before this service knew about containers (G7).
//   - the bundle, asked for the credential name the per-secret name holds: a
//     rotation of a credential the bundle also holds falls back to that entry
//     (R7), so a container that is not there serves the operator's value rather
//     than a refusal. For a tier-C candidate the name is its own and the lookup
//     is entryFor's selection by kind, which is what keeps the provider key out
//     of an account's hands.
//
// A read that FAILED is the third answer and it is NOT a fall: the caller
// refuses the call on it (D143). Every other answer is the entry that was
// selected or nothing, and the caller refuses on nothing rather than forwarding
// an empty credential.
func (s *Server) resolveEntry(ctx context.Context, b *Bundle, c creds.Credential) (Entry, bool, error) {
	name := c.Name
	if c.Tier != creds.TierC {
		e, ok, fallback, err := s.perSecrets().read(ctx, c)
		if err != nil || ok {
			return e, ok, err
		}
		name = fallback
	}
	e, ok := entryFor(b, creds.Credential{Name: name, Tier: c.Tier})
	return e, ok, nil
}

// perSecretSentence is what a run is told when resolveEntry answered the
// failure class, and it is the only place the two sentences of that class are
// told apart: a read that FAILED (ErrPerSecretUnreadable and everything else)
// is perSecretRefusal, and R7's absence of a person's own token is
// ErrPersonalTokenAbsent's own (D2974).
//
// The second is a sentence of its own because the two are different acts for
// the run: a rotation the service could not read is a retry, and a personal
// token that is not there is a person who must run `fugaro secrets user set`
// before their runs can be served at all. It is not a fall to the bundle's
// entry — the name decided that in the read (dynamicsecrets.go) — and the run
// is answered the command rather than the workspace's shared token.
//
// The class is matched by errors.Is rather than by the message: the read
// returns the sentinel unwrapped, and a sentence compared as text would drift
// from the constant the moment either is edited.
func perSecretSentence(err error) string {
	if errors.Is(err, ErrPersonalTokenAbsent) {
		return ErrPersonalTokenAbsent.Error()
	}
	return perSecretRefusal
}

// unsent is the candidates this call has not already sent to. The mark a
// usage limit writes does not always hold its credential — a reset already in
// the past, on a tier whose D142 window is off, leaves a mark that has
// expired — so the retry loop tracks what it sent rather than trusting the
// state to refuse it. Without this, one call could send to the same credential
// again and again.
func unsent(cands []creds.Credential, sent map[string]bool) []creds.Credential {
	out := make([]creds.Credential, 0, len(cands))
	for _, c := range cands {
		if !sent[c.Name] {
			out = append(out, c)
		}
	}
	return out
}

// auditCall writes this service's one line for one call (C1.13): the issuance
// a credential answered, or the refusal this service answered in its place. The
// event is completed from the identity the service verified (Identity.Audit —
// the run, the repository slug, the job the run reported and the account that
// called, and none of them from anything a caller typed) and carries the
// credential's NAME and the tier it is of, never a value: an AuditEvent has no
// field a value could be written into (audit.go).
//
// It is written through the auditor the SERVICE built — NewServer's
// NewProbeAuditor over the bundle loader, the one the probe writes through too
// — and not through a configured one, for exactly the reason C1.13 gives: the
// loader is the one place this service reads a credential, so it is the one
// place that can answer what a value is, and it arms this line's redaction.
// The arming is therefore a fact of the service's construction and not
// something a caller can forget (WithAuditScrub, NewAuditor's refusal).
//
// purpose is what the call asked for and not what the helper writes: the two
// routes this service serves are two acts — a proxied model call (C1.11) and a
// mint (C2.1) — and a refusal taken before the route could be served (the
// operator's stop switch, a stop switch that could not be read) is still a
// refusal of one of them. A line that said "forward" for a mint would be the
// audit telling an operator that a model call was refused when the run asked
// for an installation token.
//
// status is the wire's own vocabulary: StatusOK for an issuance, and one of
// the contract's error types for a refusal. For a model call — every purpose
// but a mint's — the credential and the tier are passed together or not at
// all: an issuance and a refusal that names the credential it would have used
// both carry them, a refusal taken before one was chosen carries neither, and
// the auditor refuses a line that names one without the other (a reader that
// guessed the missing half would be reading an answer this service did not
// give). A mint is the one purpose that rule does not hold for, and the
// auditor says so itself: the App key is not a model credential of the cascade
// and has no tier, so a mint carries the credential's name and an EMPTY tier,
// and the auditor refuses a tier on a mint instead of requiring one — which is
// what makes the refusals above the mint's branch (server.go) able to name the
// App key and no tier.
func (s *Server) auditCall(ctx context.Context, id Identity, purpose, credential, tier, status string) error {
	return s.audit.Write(ctx, id.Audit(AuditEvent{
		Credential: credential,
		Tier:       tier,
		Purpose:    purpose,
		Status:     status,
	}))
}

// refuseCall answers one refusal and records it: one typed refusal to the run,
// one line to the operator, and the run's answer is the same whether or not the
// line lands — an act this service will not record is not made, and a call it
// refuses is not made either way (D143, the rule refuseMint already keeps for
// the refusal half of the mint). A line that could not be written is the
// service's own log line, which is where an operator reads it.
//
// The audit status is the refusal's own wire error type, so the run's halt
// message and the operator's line read the same word for the same refusal.
//
// It also writes the diagnosis the audit line cannot carry (logRefusal): which
// read failed and with what class of error, which is nowhere else once the
// gateway has turned the typed refusal into one sentence for the agent.
func (s *Server) refuseCall(ctx context.Context, w http.ResponseWriter, id Identity, purpose, credential, tier string, status int, typ, message string) {
	s.refuseCallCause(ctx, w, id, purpose, credential, tier, status, typ, message, nil)
}

// refuseCallCause is refuseCall for a refusal whose cause this code knows: the
// error the refusal was taken on, carrying the label of the read that failed
// (labelRead) when one did. The refusal itself is unchanged — the same audited
// act, the same body and the same status — and the cause reaches the service's
// own log line and nothing else: no error text, no field of the answer.
func (s *Server) refuseCallCause(ctx context.Context, w http.ResponseWriter, id Identity, purpose, credential, tier string, status int, typ, message string, cause error) {
	if err := s.auditCall(ctx, id, purpose, credential, tier, typ); err != nil {
		s.log.Error("credsvc: a refused call could not be audited", "error", err)
	}
	// No selection was walked on this path — the refusal is about the call
	// itself, or about the one credential it named — so the line's tried field
	// is empty and says nothing about a list there was none of.
	s.logRefusal(ctx, id, typ, status, message, cause, nil)
	s.refuse(w, status, typ, message)
}

// forward answers the call: it selects, sends, and acts on what came back.
//
// The retry is the whole of the loop and it has exactly two rules. It happens
// only for an answer that says the credential could not serve the call — a
// usage limit (429) or a 401 — and it happens only once per credential and
// only while nothing has been streamed to the client. Both rules are why this
// is a loop over a `sent` set rather than a retry inside `send`: the moment
// the answer is the client's, the call is over.
func (s *Server) forward(ctx context.Context, w http.ResponseWriter, cl *call, sel *creds.Selector, id Identity, b *Bundle, cands []creds.Credential, notAdmitted []Tried) {
	tried := append([]Tried(nil), notAdmitted...)
	sent := make(map[string]bool, len(cands))
	// probed is what THIS call has already probed (C1.12): a name in it is
	// never probed twice by the same call, which is what bounds the loop below
	// when a probe leaves a credential the next selection still wants probed.
	probed := make(map[string]bool, len(cands))
	started, reason := "", ""
	// The run is the identity's: the slug and the run id the service verified
	// (C1.10) are what the cascade's nodes are keyed by, and taking them from
	// anywhere else would be naming a run the service did not verify.
	run := creds.Run{Slug: id.Slug, ID: id.Run}
	// The workspace's cap, attached before the first attempt: every attempt of
	// this call takes its own reservation in send and settles it in answer
	// (WS8). It is nil for a call the meter does not bracket — an
	// installation's scope, a token count — and nothing below then differs.
	s.withMeter(ctx, cl, id)

	pool := cands
	for {
		c, _, err := sel.Select(ctx, run, pool)
		if err != nil {
			// A probe being owed on the earliest candidate is the one answer
			// this loop acts on rather than relays (C1.12): the credential is
			// backed off with its wait passed, so it is asked whether it works
			// and the selection is made again. Anything else — a real wait, a
			// broken mark, an unreadable state — is the run's answer and is
			// refused below.
			due := s.probeDue(ctx, sel, id, cl, b, pool, probed, err)
			// A probe the workspace could not pay for is refused like any other
			// call of this run's: the meter's refusal is on the call (probeOne's
			// own answer, carried here), and the run is answered it rather than
			// the wait the probe was owed on. The alternative — walking on as if
			// the probe had happened — is a call that was never made being
			// treated as one that was.
			//
			// cl.fail is read whatever probeDue answered, because probeOne
			// reports false on the two paths that set it (a reservation refused
			// before the call, a settle that could not be written after it):
			// reading it only where the probe reports success would let a call
			// the workspace refused fall through to the wait the probe was owed
			// on, which the run would then spend its budget re-asking for.
			if cl.fail != nil {
				s.refuseMeter(ctx, w, id, PurposeForward, cl.fail)
				return
			}
			if due {
				continue
			}
			s.refuseSelection(ctx, w, id, err, tried)
			return
		}
		if sent[c.Name] {
			// Select handed back a credential this call has already sent to,
			// which means its mark does not hold it (a usage limit whose reset
			// had already passed, on a tier whose window is off). Sending
			// again is the one thing this loop must not do, so the call is
			// refused rather than retried.
			//
			// It is the one refusal here that names a credential the call has
			// already spent: the line carries it, because the audit exists so
			// that an operator can read what the service did with which
			// credential — and what it did with this one is send to it twice.
			s.refuseCall(ctx, w, id, PurposeForward, c.Name, wireTier(c.Tier), StatusUnavailable, ErrorStateUnavailable,
				"no credential is usable now and none of the ones this call used may be tried again")
			return
		}
		e, ok, err := s.resolveEntry(ctx, b, c)
		if err != nil {
			// The per-secret read failed, and fail-closed withholds the call
			// rather than falling to the bundle's entry (D143, R7): the value
			// the bundle holds is the one the rotation was of, so serving it
			// here would serve, for every run at once, exactly what an operator
			// rotated away — silently, and with no line that says so. The
			// sentence is the class and nothing else: no container and no value
			// is in it. A person's own token is the one candidate of the
			// grammar whose absence is this class too (perSecretSentence), and
			// its sentence is the command that sets one.
			s.refuseCallCause(ctx, w, id, PurposeForward, c.Name, wireTier(c.Tier), StatusUnavailable, ErrorStateUnavailable,
				perSecretSentence(err), labelRead(readSecret, err))
			return
		}
		if !ok {
			// Unreachable: the candidates came from this bundle. A refusal
			// rather than a panic, because the alternative is a service that
			// crashes on a state it cannot name.
			s.refuseCall(ctx, w, id, PurposeForward, c.Name, wireTier(c.Tier), StatusUnavailable, ErrorStateUnavailable,
				"the selected credential is not in the bundle")
			return
		}
		if started == "" {
			started = c.Name
		}
		ans, err := s.send(ctx, cl, e)
		if err != nil {
			if errors.Is(err, errMeterRefused) {
				// The workspace's own refusal, which carries a sentence, a
				// marker and the credential the attempt was made on — none of
				// which an error value holds — and which is answered and
				// audited by the path that knows them.
				s.refuseMeter(ctx, w, id, PurposeForward, cl.fail)
				return
			}
			s.refuseCallCause(ctx, w, id, PurposeForward, c.Name, wireTier(c.Tier), StatusUnavailable, ErrorStateUnavailable,
				err.Error(), labelRead(readUpstream, err))
			return
		}

		switch status := ans.resp.StatusCode; {
		case unauthorizedStatus(status):
			// D139: a 401 marks the credential broken, and only an operator
			// clears that (C1.21). The upstream's own answer is not forwarded:
			// it is about a credential the run does not hold, and a run shown
			// an authentication error it cannot act on is a run that stops for
			// the wrong reason.
			//
			// The attempt's reservation is settled first, at the zero a 401 is
			// worth (no answer is relayed, so no usage is read): a 401 is not a
			// call the provider billed for, and the member's own gateway settles
			// it at zero too — a reservation charged here would be the
			// workspace's month paying for an attempt nobody served. A settle
			// that cannot be written is the refusal the run gets — the
			// credential's mark is not written, because the call is over either
			// way and an unrecorded cost is the thing an operator has to act on.
			ans.resp.Body.Close()
			if fail := s.settleCall(ctx, cl, unchargedUsage()); fail != nil {
				s.refuseMeter(ctx, w, id, PurposeForward, fail)
				return
			}
			if _, err := sel.Broken(ctx, c.Name); err != nil {
				s.refuseCallCause(ctx, w, id, PurposeForward, c.Name, wireTier(c.Tier), StatusUnavailable, ErrorStateUnavailable,
					markRefusal, err)
				return
			}
			tried = append(tried, Tried{Name: c.Name, Tier: wireTier(c.Tier), Reason: ReasonBroken})
			sent[c.Name], reason = true, ReasonBroken
			pool = retryPool(cands, sent)
			continue

		case usageLimitStatus(status):
			// A usage limit. Nothing has been streamed, so the call may be
			// retried — on another credential, never on this one.
			reset := usageLimitReset(ans.resp.Header, s.now())
			// The attempt's reservation is settled as the 401 arm's is, and for
			// the same reason: the retry that may follow is a SECOND call with a
			// reservation of its own, and the one this attempt took must not be
			// left standing on the strength of a settle that was never made.
			// What it settles AT is the zero a 429 is worth, which is what the
			// member's own gateway settles that answer at: an unbilled attempt
			// must not eat the workspace's month, and a run that meets a usage
			// limit on one credential can meet several.
			ans.resp.Body.Close()
			if fail := s.settleCall(ctx, cl, unchargedUsage()); fail != nil {
				s.refuseMeter(ctx, w, id, PurposeForward, fail)
				return
			}
			if _, err := sel.Exhausted(ctx, c.Name, c.Tier, reset); err != nil {
				s.refuseCallCause(ctx, w, id, PurposeForward, c.Name, wireTier(c.Tier), StatusUnavailable, ErrorStateUnavailable,
					markRefusal, err)
				return
			}
			tried = append(tried, Tried{Name: c.Name, Tier: wireTier(c.Tier), Reason: ReasonExhausted})
			sent[c.Name], reason = true, ReasonExhausted
			pool = retryPool(cands, sent)
			continue
		}

		// Everything else is the client's answer, exactly as it came: a 2xx, a
		// 400 for a request Anthropic refused, an overloaded_error, a 500. The
		// service retries none of them — a retry is for a credential that
		// cannot serve the call, and none of these says that.
		var sw *Switch
		if len(sent) > 0 && c.Name != started {
			sw = &Switch{From: started, To: c.Name, Reason: reason}
		}
		scan := &usageLimitScanner{}
		s.answer(ctx, w, id, cl, ans, c, sw, scan)
		if scan.Found() {
			// A usage limit inside a 200 stream: the bytes are the client's
			// already, so this call is not retried and the credential is
			// marked with no reset — C1.9's backoff, which is the shape U3
			// leaves open until a subscription usage limit is observed.
			//
			// The context is stripped of its cancellation: the exhaustion is a
			// fact about the credential whether or not the client stayed to
			// read the answer, and a client that hung up must not be the
			// reason the next call walks into the same limit.
			if _, err := sel.Exhausted(context.WithoutCancel(ctx), c.Name, c.Tier, time.Time{}); err != nil {
				s.log.Warn("credsvc: a usage limit seen inside a stream could not be recorded", "error", err)
			}
		}
		return
	}
}

// retryPool is what a retry selects from: the candidates this call has not
// sent to, or the whole set when there are none left.
//
// The whole set, because the answer then is not "try one" but "tell the run
// what the state says" — Select over the whole set reads the marks this call
// just wrote and answers the earliest reset (or that nothing is usable at
// all). A credential it hands back is one this call has already sent to, and
// the loop refuses on that rather than sending again.
func retryPool(cands []creds.Credential, sent map[string]bool) []creds.Credential {
	if left := unsent(cands, sent); len(left) > 0 {
		return left
	}
	return cands
}

// answerUnaudited is what a call whose issuance line could not be written is
// refused with. It is this file's own sentence and carries no value: nothing of
// the upstream's answer reaches the run, because the answer is what fail-closed
// withholds (D143).
const answerUnaudited = "the call could not be audited, so the credential's answer was not given"

// markRefusal is what a mark that could not be written means. It is a refusal
// and not a detail to log past: the next call's selection is made from the
// state, so a credential this call found unusable and could not record is a
// credential the next call is sent to again.
const markRefusal = "the credential's state could not be recorded"

// selectionUnreadable is what the SERVICE'S OWN LOG says a selection that
// failed on the state was, in the one case where the sentence the run is sent
// is another system's text (refusallog.go): a store's error names the node it
// read, and the run is told it while the log never is. It says only what the
// read it is written beside says in its own word — the selector's marks, the
// leases or the run's placement — and claims nothing about which.
const selectionUnreadable = "the credential's state could not be read"

// noCandidatesHead is what a call with nothing to select from is refused with,
// and it says the fact the old class hid: the state was read, and what it says
// is that the selection was handed an empty list. It names no credential — no
// value is ever named in a sentence — and it sends the operator to the list
// below and to the documents that decide it rather than to the database, which
// is where `state_unavailable` sent them.
//
// It does not say WHICH filter emptied the list, because the reasons on the
// list say that one credential at a time and a sentence that guessed would be
// wrong where the list is not: the same class answers a repository its
// admission leaves nothing, a per-user or project credential a call carries no
// slip for, and a bundle that holds no model credential at all.
const noCandidatesHead = "no credential is offered to this call, and the state was read: the list the selection was given was empty"

// noCandidatesMessage is the sentence a call with no candidate is refused with,
// on the wire and on the operator's own line: the cause, then every credential
// that was considered with the one reason it was dropped, in the order the
// candidate list was walked.
//
// The list is never clipped: a refusal may name maxTriedEntries credentials and
// the bundle may hold no more than that (MaxBundleCredentials), every entry
// here comes from a distinct credential of that bundle, and an account's name is
// at most config.MaxClaudeAccountNameLen bytes — so a list this service can
// build is a list the writer will write, and a refusal that fell back to
// state_unavailable over its own length cannot arise from this path.
//
// An empty list is answered as its own sentence rather than as a head with
// nothing after it: nothing could be considered at all, which is a different
// failure from every credential this call could be served by having been
// dropped, and an operator acts on the two differently.
func noCandidatesMessage(tried []Tried) string {
	if len(tried) == 0 {
		// Nothing was dropped because nothing was there to drop, and the only
		// way to an empty list without a drop is a bundle whose model
		// credentials are empty: every other filter reports what it removed
		// (candidates, the slip fence, narrowToRepoAuth).
		return noCandidatesHead + ": this installation's bundle holds no model credential at all"
	}
	return noCandidatesHead + "; considered and dropped: " + triedLine(tried)
}

// credentialOwnedBy reports whether the credential a call was served is the
// launcher's OWN per-user credential, and it is the whole of when an answer may
// name an owner (F16).
//
// A slip admits a call to more than the launcher's own tokens: a grant's list
// may name bundle entries, which are nobody's, and a pool's credential, which
// the launcher may merely be allowed by and not own (R11, G8) — and the
// owner-only rule holds a per-user name to its person (checkGrantedNames) while
// saying nothing about either. So the GRAMMAR answers here and not the slip:
// `fugaro_u_<id>_<name>` is the one shape of name that carries an owner at all,
// and only when that id is the launcher's verified sub is the launcher the
// person whose token paid. Every other credential — a bundle entry's own name,
// a pool's name, a fugaro_p_ shared one, and a per-user name of somebody else —
// is answered no owner, and the run records none: a record that cannot name the
// token's owner must say nothing rather than name the wrong person.
func credentialOwnedBy(name, launcher string) bool {
	parsed, err := secretname.Parse(name)
	return err == nil && parsed.ID != "" && parsed.ID == launcher
}

// answer writes the upstream's answer to the client, with the headers of this
// contract on it: the tier, the credential's name, the owner of the credential
// that served it when the launcher owns it, and the switch when this call
// moved.
//
// It is also where the call is recorded (C1.13): the credential answered, so
// this is the issuance, and the line is written before a byte of the answer is
// — an issuance this service could not audit is not made (D143), which here
// means the run is refused rather than handed an answer no line accounts for.
// The credential was spent by then (the upstream call is out of this service's
// hands), so what fail-closed withholds is its effect: no byte of the answer
// reaches the run and the run halts on the refusal.
//
// The issuance's line says the credential answered, which is all that was known
// when it was written, and the answer's own outcome is the second line this
// function writes: the stream either ended or it did not (gateway.Pump's own
// bool), and a stream that did not end leaves a line with this call's failure
// on it — a call cut in half is not a call that reads ok.
//
// The headers are built and validated before any of them is written, so an
// answer that cannot carry them is a typed refusal with no upstream header
// leaking onto it — and, since the line is written after them, a refusal with
// no issuance line beside it: a line saying a credential answered a run this
// service then refused would be a line an operator could not act on, and the
// refusal is the one act that happened.
//
// The body is scrubbed of the credential as the headers are, and for every
// tier rather than the subscription one alone: an upstream that echoes the
// credential it was handed — in a 200 body, in a 400's error text, in a
// request id — must not have it echoed on to the run, and every credential
// this service attaches is a secret value (tier B's paid key as much as tier
// A's token). gateway.NewRedactWriter is the gateway's own no-echo rule over a
// body, and it is used here rather than re-implemented for the reason C1.8
// exists: the two forwarders must not drift. It also holds back a tail that
// could still become the credential, so a credential split across two reads is
// redacted too, and its Close writes that tail once the stream ends.
func (s *Server) answer(ctx context.Context, w http.ResponseWriter, id Identity, cl *call, ans *answer, c creds.Credential, sw *Switch, scan *usageLimitScanner) {
	defer ans.resp.Body.Close()
	// The workspace's cap comes off the call here, and it does so whatever
	// happens below: a metered answer is settled on the usage it reports, and
	// every path that leaves this function without doing that — a refusal
	// between here and the settle, a stream that never reported its usage —
	// settles the reservation at what it holds instead. The guard is the last
	// thing to run and does nothing when the call has already settled.
	cm := cl.meter
	stream := answerIsStream(ans.resp, cl.requested)
	var use *callUsage
	if cm != nil {
		defer func() {
			if cm.reserved {
				if fail := s.settleCall(ctx, cl, nil); fail != nil {
					s.log.Error("credsvc: a metered call's reservation could not be settled",
						"credential", fail.credential, "tier", fail.tier)
				}
			}
		}()
		// A whole body is read before anything is written, because the settle
		// is a write the workspace can refuse the call on: a call whose cost
		// cannot be recorded is not served (D1301's fail-closed rule), and an
		// answer whose bytes had already left could not be withheld. A body
		// past the reader's bound, or one that fails mid-read, is settled at
		// its reservation and put back in front of the rest so the answer still
		// reaches the client as it came.
		if !stream {
			use = readMeteredBody(ans)
		} else {
			// A stream's usage arrives inside the answer, so the reader is fed
			// as the bytes go to the client (the tee below) and the call
			// settles after the stream ends.
			use = newCallUsage(true)
		}
		// An answer that is not a 2xx was not billed for, and it is the STATUS
		// that says so rather than the body: an error body reports no usage, and
		// a call the provider refused must not be charged the reservation it
		// took. A body that could not be read is handed on as it is (nil), which
		// keeps the reservation — nothing about that answer is known.
		if use != nil {
			use.uncharged = ans.resp.StatusCode < 200 || ans.resp.StatusCode > 299
		}
		if !stream {
			if fail := s.settleCall(ctx, cl, use); fail != nil {
				s.refuseMeter(ctx, w, id, PurposeForward, fail)
				return
			}
		}
	}
	hdr := http.Header{}
	copyAnswerHeaders(hdr, ans.resp.Header, ans.credential)
	// The workspace's advisory headroom, on the answer to every call of a
	// metered scope (gateway.WorkspaceHeadroomHeader, through
	// SetWorkspaceHeadroom): this is where the service's own headers are set,
	// and copyAnswerHeaders drops any X-Fugaro- header an upstream sent. A
	// workspace that states no ceiling writes nothing here, which is what its
	// members read as "no cap". The settle below has already run for a whole
	// body, so the figure is the room the call left; a stream's is the room its
	// reservation left, which is the lower bound of the two and the honest one
	// to deliver before the call's own cost is known.
	if room, ok := s.answerHeadroom(ctx, cl); ok {
		SetWorkspaceHeadroom(hdr, room)
	}
	if err := SetCredentialHeaders(hdr, wireTier(c.Tier), c.Name); err != nil {
		s.refuseCall(ctx, w, id, PurposeForward, c.Name, wireTier(c.Tier), StatusUnavailable, ErrorStateUnavailable,
			"the answer could not carry the credential that served it")
		return
	}
	if b, ok := callSlipOf(ctx); ok && credentialOwnedBy(c.Name, b.owner) {
		// The owner of the credential that answered (F16): the verified `sub`
		// the slip was issued to, which is what the run records beside the
		// credential's name (runstore.Record.CredentialOwner). It is written
		// only where the credential served IS that launcher's own per-user one
		// (credentialOwnedBy) — a slip admits a call to credentials that are
		// nobody's, and to a pool's, and naming the launcher over either would
		// be a run recording a token's owner that nothing verified. Everywhere
		// else, a slip-less call included, the answer carries none and the
		// run's record holds no owner. The value written is the binding's own —
		// the one the audit line reads (Auditor.Write) — so the two never
		// disagree about which launcher they name.
		if err := SetCredentialOwnerHeader(hdr, b.owner); err != nil {
			s.refuseCall(ctx, w, id, PurposeForward, c.Name, wireTier(c.Tier), StatusUnavailable, ErrorStateUnavailable,
				"the answer could not carry the owner of the credential that served it")
			return
		}
	}
	if sw != nil {
		if err := SetSwitchHeader(hdr, *sw); err != nil {
			s.refuseCall(ctx, w, id, PurposeForward, c.Name, wireTier(c.Tier), StatusUnavailable, ErrorStateUnavailable,
				"the answer could not carry the switch that served it")
			return
		}
	}
	if len(cl.probed) > 0 {
		// The probes this call made before it was served (C1.12): the run
		// accounts for them by the tier each was made on (C1.16), and the
		// answer is the only place it can learn them. An answer that cannot
		// carry them is refused rather than sent without them — a model
		// answer that hid the probes it was served after is a run paying for
		// calls its ledger never saw.
		if err := SetProbeHeader(hdr, cl.probed); err != nil {
			s.refuseCall(ctx, w, id, PurposeForward, c.Name, wireTier(c.Tier), StatusUnavailable, ErrorStateUnavailable,
				"the answer could not carry the probes this call made")
			return
		}
	}
	// The issuance's own line (C1.13), written here and not after the answer:
	// an issuance this service cannot audit is not made, and the credential is
	// spent by now — what is withheld is the answer.
	//
	// The refusal that follows is refused through refuseCall like every other
	// refusal on this path, so the refusal itself is audited too: the act that
	// could not be recorded is the issuance, and a failure to write one line is
	// no reason to leave the line the operator DOES need — the call that got no
	// answer — unwritten. (The auditor may fail this second line as well; then
	// the failure is on the service's log twice, which is where an operator
	// looks for an audit log that is not recording.)
	if err := s.auditCall(ctx, id, PurposeForward, c.Name, wireTier(c.Tier), StatusOK); err != nil {
		s.log.Error("credsvc: the issuance's audit line was not written", "error", err)
		s.refuseCall(ctx, w, id, PurposeForward, c.Name, wireTier(c.Tier), StatusUnavailable, ErrorStateUnavailable, answerUnaudited)
		return
	}
	for k, vs := range hdr {
		w.Header()[k] = vs
	}
	w.WriteHeader(ans.resp.StatusCode)
	tee := func([]byte) {}
	if scan != nil && stream {
		tee = scan.scan
	}
	if use != nil && stream {
		// The meter's own reader is fed beside the usage-limit scan, from the
		// same copy of the bytes that goes to the client: both are reading the
		// answer the run is already getting, and neither may change it. The scan
		// is fed first, because it is the one the forward path acts on.
		inner := tee
		tee = func(b []byte) {
			inner(b)
			use.feed(b)
		}
	}
	// The credential is scrubbed out of the body on its way to the client, and
	// so is every token the caller presented: the run identity this service
	// verified and the job's ID token. An upstream that echoed one of them —
	// in a message's text, in an error, in a request id — must not have it
	// echoed on to the run, and the two tokens are as much this service's to
	// keep out of an answer as the credential is.
	//
	// The writers chain, one per value, and each holds back a tail that could
	// still become its value, so a value split across two reads is redacted
	// too. They wrap the client's own ResponseWriter, and the
	// ResponseController stays on that ResponseWriter rather than on the
	// outermost writer, so flushing is still the flush of the client's
	// connection (C1.8's pump flushes after every write, and a stream must not
	// be delayed by the redaction).
	out := io.Writer(w)
	var rw []io.WriteCloser
	for _, key := range append([]string{ans.credential}, cl.scrubs...) {
		if key == "" {
			continue
		}
		r := gateway.NewRedactWriter(out, key)
		rw = append(rw, r)
		out = r
	}
	// Pump never buffers and flushes after every write, which is what makes
	// the answer reach the run as it arrives (C1.8). Its bool says the stream
	// ended rather than failed, and that answer is recorded below: a stream that
	// failed is a call that did not finish, and the line written above says the
	// credential answered — which it did, and which is why it had to be written
	// before the answer — so without a second line the log of a call cut in half
	// would read ok and nothing else (C1.13).
	ended := gateway.Pump(out, http.NewResponseController(w), ans.resp.Body, tee)
	// Innermost last: a writer's tail is not its value after all, so it is the
	// tail of the answer and it is written — into the writer below it, which
	// then has the same chance to find a value in what it is handed.
	for i := len(rw) - 1; i >= 0; i-- {
		_ = rw[i].Close()
	}
	if cm != nil {
		// The streamed call settles here, on whatever its events reported: a
		// stream that reached its stop event with both counts settles at its
		// usage, and one that was cut before that settles at the reservation it
		// holds — the worst case this call's request could have cost, which is
		// what a call whose cost cannot be read is charged. The answer has left
		// by now, so a settle that cannot be written is the service's log line
		// and nothing else: the run cannot be refused an answer it already has,
		// and the reservation stands until it ages out (budget's own TTL), so
		// the room is held rather than forgotten.
		//
		// This is the one settle that is tried again, and for that reason: it is
		// the one whose loss is permanent. Every member's calls write one month
		// node, so a burst of parallel streams can keep the meter's own fifteen
		// attempts losing, and a stream whose settle is then abandoned is served
		// and charged nowhere. settleStreamedCall says why the attempts are made
		// here rather than after this handler returns.
		if fail := s.settleStreamedCall(ctx, cl, use); fail != nil {
			s.log.Error("credsvc: a metered stream's cost could not be recorded",
				"credential", fail.credential, "tier", fail.tier)
		}
	}
	if ended {
		return
	}
	// The stream did not end, so the answer did not arrive whole: the upstream's
	// body failed mid-stream, or the client's own connection did. Either way the
	// outcome is the call's and not the client's, which is why the context is
	// stripped of its cancellation (the rule the usage-limit scan below follows
	// for the same reason): the failure is a fact about this call whether or not
	// the caller stayed to read it, and an operator reading the log has to see
	// the call that got no whole answer rather than one that looks served.
	//
	// The status is the service's own word for a call it could not complete
	// (state_unavailable), which is the refusal class the runner halts on and
	// the same word every other failure on this path is recorded with. The
	// credential and the tier are named as they are on the issuance's line: the
	// credential that answered is the one whose answer did not arrive, and that
	// is what an operator acts on.
	//
	// A line that cannot be written is the service's log line and nothing else:
	// the answer has left, the credential is spent, and there is no call left to
	// refuse — unlike the issuance's own line above, whose failure withholds the
	// answer, this one records an outcome that has already happened.
	if err := s.auditCall(context.WithoutCancel(ctx), id, PurposeForward, c.Name, wireTier(c.Tier), ErrorStateUnavailable); err != nil {
		s.log.Error("credsvc: the failed stream could not be audited", "error", err)
	}
}

// waitFloor is the shortest wait this service answers a run with, and it is a
// floor rather than a value: a wait the selection answered with is relayed as
// it stands whenever it is really in the future, and an instant that has
// already passed is replaced by now plus this.
//
// An instant in the past is not a wait at all. The runner acts on the typed
// answer by resuming at reset_at (C1.17), so a reset_at behind the run is an
// instruction to resume at once, and a run that resumes at once against a state
// that has not moved is a stage re-run in a tight loop — burning a run's own
// budget on a wait that was never going to end.
//
// Two states reach it, and both are ordinary. A probe owed on a backed-off
// credential is due exactly BECAUSE the credential's own wait has passed, so the
// instant the selection answers with (At) is behind the run by construction: the
// probe is what would move it, and a probe that was not made — the slot is
// another instance's, the lease cannot be read, the claim keeps losing its
// compare-and-set, the run's body names no model — leaves nothing but that
// instant to answer with. And a usage limit whose reset had already passed when
// the answer was read writes a mark that holds nothing (C1.9), so the next
// selection answers with a past instant the same way.
//
// Fifteen seconds is a value, not a measurement: a probe takes seconds (a
// one-token call), and the wait has to be long enough that a runner resuming
// under it does not become the loop this exists to prevent. It is nowhere near
// the policy's own threshold, so no wait this service invents can be mistaken
// for the credential's own reset.
const waitFloor = 15 * time.Second

// refuseSelection turns creds.Select's answer into the client's, and records
// it (C1.13): one line per refused call, with the refusal's own error type as
// its status, no credential VALUE (no value is ever named in a line) and the
// credentials' names and the reason each was skipped beside them, on the
// service's own line as well as in the run's answer: what was considered is
// what the operator has to read to see why a repository was served nothing,
// and the run's line alone would put it where only the run's own reader looks
// (the WARN line is the operator's, and it is what a repository with no
// candidate is diagnosed from).
//
// The typed answer is the one case the runner acts on rather than halts for:
// nothing is usable now and something becomes usable at an instant, so the run
// waits (C1.17). Everything else is a refusal it halts on (D143) — the state
// could not be read, every candidate is broken or held with no time to wait
// for, or the only credentials that could serve the call are of a tier this
// policy does not admit (D152, which only a written `fallback: auto` changes).
//
// A selection with nothing to select from is refused as what it is
// (ErrorNoCandidateCredentials) rather than as an unreadable state: the state
// was read, and what it says is that this repository is admitted no credential
// at all.
//
// The tried list is carried on every refusal this path writes, so the run's
// halt message names what was considered and why each one was skipped. The
// typed wait carries it structurally (ExhaustedUntil), and a refusal that
// halts carries it in the same field of the same body (WriteWireError), so a
// run reading either finds the same list in the same place.
func (s *Server) refuseSelection(ctx context.Context, w http.ResponseWriter, id Identity, err error, tried []Tried) {
	var until *creds.ExhaustedUntil
	if errors.As(err, &until) {
		now := s.now()
		reset := until.At
		if !reset.After(now) {
			// The instant has passed, which is a state the run cannot act on
			// (waitFloor). It is answered as a short wait ahead of the run
			// instead, so the next stage it runs has something to wait for and
			// the loop is bounded by the floor rather than by the state.
			reset = now.Add(waitFloor)
		}
		if aerr := s.auditCall(ctx, id, PurposeForward, "", "", ErrorExhaustedUntil); aerr != nil {
			s.log.Error("credsvc: a refused call could not be audited", "error", aerr)
		}
		ans := ExhaustedUntil{ResetAt: reset, Tried: tried}
		// The selection's refusal is a refusal like every other and leaves the
		// same diagnosis (logRefusal). The message is read off the very struct
		// the answer is written from, so the run and the operator read one
		// refusal and not two renderings of it.
		s.logRefusal(ctx, id, ErrorExhaustedUntil, StatusExhaustedUntil, ans.Error(), err, tried)
		WriteExhaustedUntil(w, ans, now)
		return
	}
	// Nothing to select from is its own refusal, and it is deliberately not
	// ErrorStateUnavailable: the state WAS read, and what the service read is
	// that the candidate list this call would be served from is empty.
	// Answering the "could not be read" class sent the operator to the database
	// (the owner's installation of 2026-11-14, and Check 191 before it) when the
	// answer was in the project's policy and the installation's bundle — and it
	// left the run's answer with an empty tried list, so neither the run nor its
	// report could say which credential was dropped or why.
	//
	// The label is what keeps the two apart, and it is the reason the class is
	// claimed only where `readOf` is empty: creds.Select answers this error bare
	// (checkCandidates builds it with errors.New, internal/creds/errors.go), and
	// every list this service can empty on a FAILED read is refused before the
	// selector is reached — the bundle load and the candidates build each answer
	// with their own sentence and their own label (server.go's cascade step). A
	// policy refusal and a store that could not be read are not the same answer
	// to an operator, so a labelled error that somehow arrived here keeps the
	// state_unavailable class below.
	if errors.Is(err, creds.ErrNoCandidates) && readOf(err) == "" {
		msg := noCandidatesMessage(tried)
		if aerr := s.auditCall(ctx, id, PurposeForward, "", "", ErrorNoCandidateCredentials); aerr != nil {
			s.log.Error("credsvc: a refused call could not be audited", "error", aerr)
		}
		// The run and the operator are sent one rendering and not two: a
		// sentence over the contract's own bound would be a body the writer
		// refuses, and its fallback is the state_unavailable mis-diagnosis this
		// branch exists to end — but this list cannot reach that bound. A tried
		// entry is one credential of a bundle, the bundle holds at most
		// maxTriedEntries of them (MaxBundleCredentials), an account's name is
		// at most config.MaxClaudeAccountNameLen bytes, and the longest reason
		// is a dozen more, so the whole rendering stays far inside it. A test
		// holds the two bounds to each other (TestCascadeNoCandidatesAtTheListBound).
		s.logRefusal(ctx, id, ErrorNoCandidateCredentials, StatusUnavailable, msg, err, tried)
		WriteWireError(w, StatusUnavailable, WireError{
			Error:   ErrorNoCandidateCredentials,
			Message: msg,
			Tried:   tried,
		})
		return
	}
	if aerr := s.auditCall(ctx, id, PurposeForward, "", "", ErrorStateUnavailable); aerr != nil {
		s.log.Error("credsvc: a refused call could not be audited", "error", aerr)
	}
	// The operator's line carries the sentence the run is sent, which is this
	// service's refusal for a selection that found nothing usable (creds'
	// ErrNoCredential, ErrFallbackForbidden, ErrNoCandidates) — and the CLASS
	// instead where the sentence is the store's own text, which is what a
	// labelled failure is (refusallog.go): the store's text names the node it
	// read, and this line never repeats another system's words. The read label
	// and the class beside it are what tell those two apart.
	msg := err.Error()
	if readOf(err) != "" {
		msg = selectionUnreadable
	}
	s.logRefusal(ctx, id, ErrorStateUnavailable, StatusUnavailable, msg, err, tried)
	WriteWireError(w, StatusUnavailable, WireError{
		Error:   ErrorStateUnavailable,
		Message: err.Error(),
		Tried:   tried,
	})
}
