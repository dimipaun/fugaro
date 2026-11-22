package credsvc

import (
	"context"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/creds"
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
// about a record.
type legacyAuth struct {
	db creds.DB
}

// The bridge is the interface the seam declares, checked here rather than at the
// assignment: the assignment is the deployment's (credentials_serve.go), and a
// build that lost this file has to fail to compile rather than serve a call
// through a seam nothing implements.
var _ LegacyBridge = legacyAuth{}

// NewLegacyAuth is the bridge a deployment assigns to LegacyChain: the database
// client the service's other nodes are read with, so the record is read over the
// same service identity as the state beside it and no second client is built for
// it.
//
// It takes creds.DB rather than *rtdb.Client for the reason NewIssuanceStore
// does (identity_record.go): the bridge makes one read and nothing else, which
// is what lets a test drive it against a fake that speaks the interface.
// *rtdb.Client is the implementation every deployment hands in.
func NewLegacyAuth(db creds.DB) LegacyBridge { return legacyAuth{db: db} }

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
// account, or an auth mode this release cannot stand for — answers true with no
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
	return legacySources(rec), true
}

// legacySources is the record as the chain it stands for, per auth mode:
//
//	api-key  the project's Anthropic key, which is the one credential that mode
//	         names (config.CredentialAnthropicKey)
//	oauth    one pool per account the record names, in the record's own order:
//	         an account IS a pool of one (D3176), and the list is the set of
//	         accounts this repository's runs may go out on, which is the whole of
//	         what the record bounds (R7)
//	cascade  the launcher's own credentials first and the project's key after
//	         them, which is the cascade's two tiers as the ordinary
//	         configuration writes them (R4: `fallback: auto` is the key in the
//	         chain)
//	vertex   Vertex, which that mode names and nothing else
//
// A mode this release does not know answers nothing at all: the record was
// written by a release whose vocabulary is not this one's, and a source invented
// for it would be a credential nobody recorded.
//
// The cascade's answer is the one place the record is lossy, and it is stated
// rather than hidden: the record names no `credentials.fallback`, so a project
// that had written `oauth-only` is answered the key as well — the direction that
// keeps a run in flight going instead of halting it, and the one MG1 writes for
// a repository whose fallback is `auto`, which is the default and the case the
// cascade exists for. The table's own entry carries the exact chain for a run of
// this release, which is why nothing of this release reads the bridge.
func legacySources(rec creds.RepoAuth) []routing.Source {
	switch rec.Auth {
	case config.AuthAPIKey:
		return []routing.Source{{Kind: routing.KindAnthropic}}
	case config.AuthOAuth:
		out := make([]routing.Source, 0, len(rec.Accounts))
		for _, account := range rec.Accounts {
			out = append(out, routing.Source{Kind: routing.KindPool, Pool: account})
		}
		return out
	case config.AuthCascade:
		return []routing.Source{{Kind: routing.KindLauncherPool}, {Kind: routing.KindAnthropic}}
	case authVertex:
		return []routing.Source{{Kind: routing.KindVertex}}
	}
	return nil
}
