package credsvc

import (
	"context"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/routing"
)

// legacy_auth.go is the release-1 bridge (MG3): the old record
// creds/repoauth/<slug> read as the chain of sources it stands for, so that a
// run whose image predates the table still resolves.
//
// It exists for ONE release and for one direction of travel. `fugaro update`
// keeps writing the record beside the table for release 1 (update_repoauth.go),
// a run that bootstrapped on the release behind carries no table and asks in the
// old wire — an auth mode and an account, the headers R24 keeps reaching only a
// caller that sends them — and the service it reaches is this one. The record is
// what such a run's reach was decided from before the table existed
// (narrowToRepoAuth, cascade.go), so reading it as a chain is what keeps that
// run exactly where it was. X4 removes this file and the seam with it: the
// table's first release is the only one that has both.
//
// What the record stands for is its own content and nothing else. It names one
// auth mode and, under `oauth`, the accounts the repository's runs may go out
// on (R7); it names no model, no pool of another kind and no admission. So the
// mapping below is a projection of that content into the table's vocabulary and
// never a widening of it: the record's width is bounded by the repository's own
// configuration (D3196's rule, update_repoauth.go's repoAuthSources), and what
// the bridge adds to it is nothing at all. An entry the table's own checks
// refuse — a Vertex source before Check 882 — is in no table and IS on the
// record: the record is wider than the table by precisely the entries D3196
// leaves there, which is the whole reason the walk's switch is the run's image
// and never the record's width or a header a call sends (contract-table.md).
//
// The record is not the only bound the old path had, and the bridge applies the
// second one too. What a call could go out on was the record narrowed by the
// project's own paid-fallback policy: creds' selection admits the project's
// Anthropic key (tier B) only where the published policy writes
// `credentials.fallback: auto`, so an `api-key` or a `cascade` repository under
// `oauth-only` — absent, and the protective default (D152) — spent nothing on
// that key, whatever its own record named. R4 retires that policy into the chain
// itself (`auto` is the key in the chain, `oauth-only` no key), so a run of this
// release carries the decision in its table and an older run has to be answered
// it here: the bridge reads the same node the service's own scope reads and
// answers the key only where it is in force. Reading the record alone would make
// the record wider than the repository's pre-migration access in the one
// direction the record cannot see.

// legacyReadTimeout bounds the read this bridge makes.
//
// OldSources has neither a context nor an error (C1's signature, which the walk
// is written against), so the read it makes is its own: a database that has
// stopped answering must not hold a model call open, and there is no caller left
// to hand a deadline back to. Two seconds is far above what one node of the
// installation's database costs the service and far below anything a caller
// waits for.
const legacyReadTimeout = 2 * time.Second

// authVertex is agent.auth's Vertex spelling, as config.AgentAuths holds it. The
// config package names no constant for this one — its two consts beside it exist
// for the readers that tell those values apart (config's AuthAPIKey, AuthOAuth)
// — and the switch below is a third such reader, so it spells the value here
// rather than at the case.
const authVertex = "vertex"

// legacyAuth is the bridge over one installation's database: the record read
// through creds' own reader, which is the reader the service's old path reads it
// with (creds.Store.RepoAuth over the same node), so the two cannot disagree
// about a record — and the published policy read through the reader the service's
// own scope reads it with, so the two cannot disagree about the paid fallback
// either.
type legacyAuth struct {
	db     creds.DB
	policy legacyPolicy
}

// legacyPolicy is the one question this bridge asks the published policy:
// whether the project pays a Claude call off the shared credentials and onto its
// own Anthropic key (R4, D152). It is a narrow interface rather than the whole
// PolicyReader because the bridge asks nothing else, and because a reader that
// answers this alone is what a test can drive; RTDBPolicy is the implementation
// every deployment hands in, and it is the same value the
// service's Config carries as its Policy.
type legacyPolicy interface {
	Shared(ctx context.Context) (*projectpolicy.Policy, bool, error)
}

// The bridge is the interface the seam declares, checked here rather than at the
// assignment: the assignment is the deployment's (credentials_serve.go), and a
// build that lost this file has to fail to compile rather than serve a call
// through a seam nothing implements.
var _ LegacyBridge = legacyAuth{}

// NewLegacyAuth is the bridge a deployment assigns to LegacyChain: the database
// client the service's other nodes are read with, so the record is read over the
// same service identity as the state beside it and no second client is built for
// it, and the policy reader over that same database, which is what bounds the
// record by the project's paid fallback.
//
// It takes creds.DB rather than *rtdb.Client for the reason NewIssuanceStore
// does (identity_record.go): the bridge makes one read and nothing else, which
// is what lets a test drive it against a fake that speaks the interface.
// *rtdb.Client is the implementation every deployment hands in.
func NewLegacyAuth(db creds.DB, policy legacyPolicy) LegacyBridge {
	return legacyAuth{db: db, policy: policy}
}

// OldSources answers the sources one repository's old record stands for, and
// whether the repository has a record this release can stand for at all.
//
// The two answers are the contract's (chain_wire.go): false is "there is no
// record to read", and the walk refuses rather than serving whatever the record
// happened to hold — the stub's own answer, and the fail-closed direction. It is
// also this bridge's answer for every read it cannot complete: an empty
// repository, a database that refused or did not answer, a node this release
// cannot decode, and a record written by a version of fugaro this one does not
// know. None of those is a fact about the repository's reach, and inventing one
// from a read that failed is the direction that serves a call nobody bounded.
//
// A record that IS read and names nothing servable — an `oauth` record naming no
// account, an `api-key` or a `cascade` record on a project whose policy serves no
// paid key, or an auth mode this release cannot stand for — answers true with no
// sources: the record is there and it admits nobody, which is a different
// situation from a repository no update has recorded and is the reason the
// contract carries the flag beside the list.
func (l legacyAuth) OldSources(repository string) ([]routing.Source, bool) {
	if l.db == nil || repository == "" {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), legacyReadTimeout)
	defer cancel()
	rec, found, err := creds.ReadRepoAuth(ctx, l.db, repository)
	if err != nil || !found {
		return nil, false
	}
	return legacySources(rec, l.paidKey(ctx)), true
}

// paidKey is whether the project's own policy may serve a call from its paid
// Anthropic key: a written `credentials.fallback: auto` (R4's chain, D152's
// protective default read backwards).
//
// Every answer but that one is "no": a policy that names none, a node this
// release cannot read, a database that refused, and an absent reader all read
// as `oauth-only`, which is the value a Settings built without a fallback has
// too (creds.Fallback's zero value) — the fail-closed direction, and the one
// the service's own selection takes for a policy it cannot read.
func (l legacyAuth) paidKey(ctx context.Context) bool {
	if l.policy == nil {
		return false
	}
	p, found, err := l.policy.Shared(ctx)
	if err != nil || !found {
		return false
	}
	var c *projectpolicy.Credentials
	if p != nil {
		c = p.Credentials
	}
	return c.FallbackValue() == projectpolicy.FallbackAuto
}

// legacySources is the record as the chain it stands for, per auth mode, with
// paid saying whether the project's key may be served at all (paidKey):
//
//	api-key  the project's Anthropic key, which is the one credential that mode
//	         names (config.CredentialAnthropicKey) — and nothing at all where
//	         the project does not pay, which is what the old path served a
//	         repository whose record names it on an `oauth-only` project
//	oauth    one pool per account the record names, in the record's own order:
//	         an account IS a pool of one (D3176), and the list is the set of
//	         accounts this repository's runs may go out on, which is the whole of
//	         what the record bounds (R7)
//	cascade  the launcher's own credentials first and the project's key after
//	         them where the project pays (R4: `fallback: auto` is the key in the
//	         chain, `oauth-only` no key)
//	vertex   Vertex, which that mode names and nothing else — never gated on the
//	         fallback, which R3/D3000 make the rule for a Claude call alone: a
//	         project whose Claude calls run on donated tokens still has its
//	         provider and Vertex calls served
//
// A mode this release does not know answers nothing at all: the record was
// written by a release whose vocabulary is not this one's, and a source invented
// for it would be a credential nobody recorded.
func legacySources(rec creds.RepoAuth, paid bool) []routing.Source {
	switch rec.Auth {
	case config.AuthAPIKey:
		if !paid {
			return nil
		}
		return []routing.Source{{Kind: routing.KindAnthropic}}
	case config.AuthOAuth:
		out := make([]routing.Source, 0, len(rec.Accounts))
		for _, account := range rec.Accounts {
			out = append(out, routing.Source{Kind: routing.KindPool, Pool: account})
		}
		return out
	case config.AuthCascade:
		out := make([]routing.Source, 0, 2)
		out = append(out, routing.Source{Kind: routing.KindLauncherPool})
		if paid {
			out = append(out, routing.Source{Kind: routing.KindAnthropic})
		}
		return out
	case authVertex:
		return []routing.Source{{Kind: routing.KindVertex}}
	}
	return nil
}
