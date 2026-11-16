package creds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Build pairs are the service's record of what a Bitbucket build's grant paired
// its identity to (B1, D2302; contract-build-token.md rule 9): the repositories
// of ONE catalog entry, written under
// creds/buildpairs/<identity>/<build id> as the grant is answered
// (credsvc/answerBuildGrant) and read by the proxy's own admission on every
// call that build's git makes (credsvc/buildProxy).
//
// # Why the pairing is a record and not an answer
//
// The grant is not a bearer ticket. A project has ONE build identity (B5), so
// every build of the project holds the same ID token, and what bounds a build's
// traffic is therefore the record — what the service wrote for THAT build id,
// out of the entry the request named — and never a field of a call. The record
// names the repositories by canonical name, which is the key space the
// published image map and the proxy's classification share, so the proxy holds
// a call to a set the request could not have widened: the repository a build
// asks for is one the entry's own map put on the entry.
//
// This is why the grant is REFUSED when the record cannot be written: a grant
// whose pairing was not recorded names a proxy that would refuse every call the
// build makes, and an answer that sends a build's git at a fence it cannot pass
// is a lie rather than a refusal.
//
// # What a pairing is not
//
// It holds no credential value. The proxy forwards a build's call with the
// installation's own Bitbucket token out of the bundle (R9) and issues the
// build nothing, which is the whole of D2302; nothing here is a token, a secret
// or a hash of one. It is written by the service's own account under creds/,
// whose rules deny the whole subtree to every run identity (O1).
//
// # Time
//
// Nothing here reads the clock. Expires is the caller's — the grant's own
// instant, capped from the request's need (credsvc's clampBuildTTL) — and the
// proxy asks the record whether it has ended, exactly as a lease is asked
// (Lease.Live): a test states the time it means, and a service has one clock
// per call. A pairing that outlived its build would be a standing grant to the
// entry's repositories for whoever runs as the project's build identity next,
// which is why the value is written and checked rather than left to a cleanup.
type BuildPair struct {
	// Identity is the project's build identity the pairing was written for: the
	// verified account of the caller that asked for the grant
	// (AuthConfig.BuildAccounts). It is a path segment of the node rather than
	// a member of its value (json:"-", as Lease.Slug and Run are): the value's
	// own copy would be a second place for it to be wrong, and a reader fills
	// it in from the path it read.
	Identity string `json:"-"`
	// BuildID is the build the grant was asked for
	// (buildtoken.Request.BuildID), and the node's second path segment. It is
	// the caller's own word and buys bookkeeping rather than authorization —
	// the contract says so in as many words — and what it buys is exactly this
	// record: two builds of one project running at once do not share or
	// overwrite one pairing, and each pairing ends with the build that asked
	// for it.
	BuildID string `json:"-"`
	// Entry is the catalog entry the request named, recorded so that an
	// operator reading the node can see what a build was paired for without
	// re-reading a map that has since moved.
	Entry string `json:"entry"`
	// Repositories are the entry's repositories at the instant of the grant, by
	// canonical name and sorted, and they are the whole of the fence around a
	// build's proxy traffic: a call for a repository that is not one of these
	// is refused by name, which is S2's rule read for a build.
	Repositories []string `json:"repositories"`
	// Expires is when the pairing ends: the grant's own instant. The proxy
	// serves no call of a pairing that has ended, and nothing has to be deleted
	// for that to be true — the record lapses on its own, as a lease does.
	Expires time.Time `json:"expires"`
}

// Live reports whether the pairing is still in force at now. It is Slip.Live's
// question asked of a different record, and it is asked the same way: an
// instant equal to the record's own is an ended one, so a pairing whose life
// the clock has reached serves nothing.
func (p BuildPair) Live(now time.Time) bool { return now.Before(p.Expires) }

// The build pair store's refusals. Each is a sentinel so a caller can answer
// the wire without reading a message, and none of them carries a value or a
// caller's own text (F32): a refusal names what is wrong with the record.
var (
	// ErrNoBuildPair is a pairing the store was asked for with an empty
	// identity or an empty build id. It is a refusal and not a panic because
	// both arrive from a request — the identity from a verified token, the id
	// from the call's own header — and the path builders in this package panic
	// on an empty segment.
	ErrNoBuildPair = errors.New("creds: a build pair needs an identity and a build id")
	// ErrBuildPairMalformed is a pairing that could not be written as it
	// stands: it must name the entry it came from, at least one repository and
	// a positive expiry. A record missing any of them is one nothing can be
	// decided from — a pairing with no repositories would fence a build out of
	// every repository, and one with no expiry would be the standing grant
	// BuildPair.Expires exists to prevent — so the writer refuses it where the
	// mistake is rather than every reader afterwards.
	ErrBuildPairMalformed = errors.New("creds: a build pair must name its entry, at least one repository and a positive expiry")
)

// BuildPairStore is what the service writes a grant's pairing with and what
// the proxy's admission reads it back from.
//
// Two methods, because the record is one fact with one writer and one reader:
// the grant writes it (PutBuildPair) as it answers, and the proxy reads it
// (BuildPair) on every call a build's git makes. There is no delete: a pairing
// ends at its own instant (Live) rather than by a cleanup somebody has to run,
// which is the property that keeps a build's grant bounded even when nothing
// ever comes back to clear it.
type BuildPairStore interface {
	// PutBuildPair writes one pairing, replacing a record for the same
	// identity and build id. It is a compare-and-set under the node it read,
	// so two writers cannot tear it, and it is deliberately NOT create-only: a
	// grant answered twice for one build — a retried call — must be able to
	// refresh its own pairing, and the record it replaces is the same build's.
	PutBuildPair(ctx context.Context, p BuildPair) error
	// BuildPair reads one pairing. found=false is a pairing that was never
	// written, and it is not an error: the proxy decides what an absent pairing
	// means, and what it means is that the call is refused.
	BuildPair(ctx context.Context, identity, buildID string) (BuildPair, bool, error)
}

// RTDBStore is a BuildPairStore, checked by the compiler rather than left to
// the first caller that wires the two together.
var _ BuildPairStore = (*RTDBStore)(nil)

// pathBuildPairs is the subtree the pairings live under. It is under creds/
// with the slips, the marks, the placements and the counter, and the rules deny
// the whole subtree to every run identity (O1): the service writes it with its
// own account, so a new child here needs no rules change and opens no node to a
// caller.
const pathBuildPairs = "creds/buildpairs"

// PathBuildPair is one build's pairing: the identity and the build id, in that
// order, because a build identity is one account of the project and the builds
// it runs are many.
func PathBuildPair(identity, buildID string) string {
	return pathBuildPairs + "/" + seg("build identity", identity) + "/" + seg("build id", buildID)
}

// PutBuildPair implements BuildPairStore.
func (s *RTDBStore) PutBuildPair(ctx context.Context, p BuildPair) error {
	if p.Identity == "" || p.BuildID == "" {
		return ErrNoBuildPair
	}
	if err := p.checkWritable(); err != nil {
		return err
	}
	// The path is the record, not a field of it (BuildPair.Identity): a value
	// carrying its own keys would be a second place for them to be wrong.
	identity, buildID := p.Identity, p.BuildID
	p.Identity, p.BuildID = "", ""
	path := PathBuildPair(identity, buildID)
	if _, err := cas(ctx, s.db(), path, s.attempts(), func(BuildPair, bool) (BuildPair, error) { return p, nil }); err != nil {
		return fmt.Errorf("creds: writing the build pair %q: %w", path, err)
	}
	return nil
}

// BuildPair implements BuildPairStore.
//
// The node is decoded with the strict decoder, as a slip and a mark are and for
// the same reason: a member this release does not model would otherwise be
// dropped silently, and a dropped member is a zero field — for a pairing, no
// repositories at all, which is a fence that admits nothing or, written the
// other way round, a reader that cannot tell a whole record from a truncated
// one. A record that cannot be read in full is therefore an error and never a
// pairing that is half read.
func (s *RTDBStore) BuildPair(ctx context.Context, identity, buildID string) (BuildPair, bool, error) {
	if identity == "" || buildID == "" {
		return BuildPair{}, false, ErrNoBuildPair
	}
	path := PathBuildPair(identity, buildID)
	raw := json.RawMessage{}
	_, found, err := s.db().GetETag(ctx, path, &raw)
	if err != nil {
		return BuildPair{}, false, fmt.Errorf("creds: reading the build pair %q: %w", path, err)
	}
	if !found {
		return BuildPair{}, false, nil
	}
	p, err := decodeStrict[BuildPair](raw)
	if err != nil {
		return BuildPair{}, false, fmt.Errorf("creds: reading the build pair %q: %w", path, err)
	}
	p.Identity, p.BuildID = identity, buildID
	return p, true, nil
}

// checkWritable holds PutBuildPair to the fields the record cannot do without.
// A pairing missing any of them is one no call can be decided from: the entry
// is what the record says it was written for, the repositories are the fence,
// and an unset expiry is the standing grant BuildPair.Expires exists to
// prevent. A PAST instant is not refused here — nothing in this file reads the
// clock — so the instant a pairing ends is the grant's own and whether it has
// passed is the proxy's question at the call.
func (p BuildPair) checkWritable() error {
	if p.Entry == "" || len(p.Repositories) == 0 || p.Expires.IsZero() {
		return ErrBuildPairMalformed
	}
	for _, r := range p.Repositories {
		if r == "" {
			return ErrBuildPairMalformed
		}
	}
	return nil
}
