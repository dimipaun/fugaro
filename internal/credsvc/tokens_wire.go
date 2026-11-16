package credsvc

// The real Bitbucket token store, behind the one value this service selects
// and reacts through (I1 of docs/bitbucket-token-types/implementation-plan.md).

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/bbtokens"
	"github.com/dimipaun/fugaro/internal/creds"
)

// bitbucketTokens is the contract's three interfaces over the two documents one
// request is served from and the store its tokens' statuses live in: the
// selector is rebuilt per request, the project resolver is kept, and the
// statuses are read and written through the creds state store's own interface
// (C1, T4, T5, T6).
//
// It answers Selector, ProjectResolver and StatusStore from ONE value, and that
// is the seam the proxy's reactions were built against (bitbucketproxy's
// reactions, reactionStore): the route asks the same value which token serves a
// call and where that token's mark and its cached project match live, so a
// deployment cannot wire a store whose selection and whose reactions disagree
// about which tokens exist — the one thing a reaction must not do.
type bitbucketTokens struct {
	status bbtokens.StatusStore
	client *http.Client

	mu     sync.Mutex
	key    [sha256.Size]byte
	lookup *bbtokens.ProjectLookup
}

var (
	_ bbtokens.Selector        = (*bitbucketTokens)(nil)
	_ bbtokens.ProjectResolver = (*bitbucketTokens)(nil)
	_ bbtokens.StatusStore     = (*bitbucketTokens)(nil)
)

// NewBitbucketTokenStore answers the token store the proxy route is served
// through: the node's typed records (T3), the bundle's entries (T1), the
// project lookup (T5) and the tokens' shared status (T6), behind the contract's
// three interfaces (C1).
//
// statuses is the creds state store's Bitbucket-token subtree, which is where a
// token's status lives so that the readers of the model marks never see it
// (D2351): a deployment wires ONE state store — the one it already hands
// Config.State — and this value is the token half of it. client is the service's
// own HTTP client, and the lookup hardens its own copy of it (no redirects,
// no jar), so the candidate project token is never presented to a host the
// adapter's constant does not name (F22, G10).
func NewBitbucketTokenStore(statuses creds.TokenStatusStore, client *http.Client) bbtokens.Selector {
	return &bitbucketTokens{status: tokenStatuses{store: statuses}, client: client}
}

// Select answers the narrowest usable token covering one request's repository.
//
// The selector is built from THIS request's snapshot and never held: the node
// is the one the request was admitted by, so a policy published between two
// requests is seen by the next one, and a selection and the decision it stands
// on can never be reading two nodes (T8's one snapshot, C1). What IS held is
// the project lookup, because its cache is a fact about the repositories and
// not about a request: T5 takes a repository's project answer once per
// ProjectLookupTTL, and a lookup rebuilt per request would ask bitbucket.org
// again on every proxied call (O1).
func (b *bitbucketTokens) Select(ctx context.Context, snap bbtokens.Snapshot, canonical string, write bool) (bbtokens.Choice, error) {
	records := tokenRecords(snap)
	return bbtokens.NewSelector(records, b.lookupFor(records, snap.Bundle), b.status).Select(ctx, snap, canonical, write)
}

// ProjectOf answers which project a repository is in, through the lookup this
// instance holds for the documents it last saw.
//
// The route's own reactions use Evict and never this (a 403 or a 404 answering
// a proxied call drops the cached match, R2, R4); it is here because the
// contract states the resolver as one interface with the eviction, and a value
// that answered the eviction alone would be one half of it. A call arriving
// before any request has been selected has no documents to look a repository
// up with, and answers "not covered" rather than asking with a token it does
// not hold.
func (b *bitbucketTokens) ProjectOf(ctx context.Context, canonical string, tok bbtokens.Name) (string, error) {
	if lookup := b.current(); lookup != nil {
		return lookup.ProjectOf(ctx, canonical, tok)
	}
	return "", nil
}

// Evict drops what is cached for one repository, so the next selection asks
// again (R2, R4). It is the reaction a proxied 403 or 404 earns.
func (b *bitbucketTokens) Evict(canonical string) {
	if lookup := b.current(); lookup != nil {
		lookup.Evict(canonical)
	}
}

// Read, Write and CountRequest are the contract's status store, answered by the
// creds state store's Bitbucket-token subtree (tokenStatuses). They are the
// reactions' own half of this value (bitbucketproxy's reactions), which is why
// they are here rather than left to a second value a deployment would have to
// wire beside this one.

// Read answers one token's status.
func (b *bitbucketTokens) Read(ctx context.Context, name bbtokens.Name) (bbtokens.Status, error) {
	return b.status.Read(ctx, name)
}

// Write records one token's status.
func (b *bitbucketTokens) Write(ctx context.Context, name bbtokens.Name, st bbtokens.Status) error {
	return b.status.Write(ctx, name, st)
}

// CountRequest records one request served with the token.
func (b *bitbucketTokens) CountRequest(ctx context.Context, name bbtokens.Name) error {
	return b.status.CountRequest(ctx, name)
}

// current is the lookup this instance holds, and nil before any request has
// been selected with it.
func (b *bitbucketTokens) current() *bbtokens.ProjectLookup {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lookup
}

// lookupFor is the resolver one request's selection asks: the lookup already
// held when it was built from the same records and the same bundle values, and
// a fresh one otherwise.
//
// The key is the two documents the lookup reads — every record's own facts and
// every entry's value — and not the files they came from: republishing the node
// with another coverage or another project UUID, and rotating a token's value,
// both change what the question and the credential are, so a lookup holding the
// old ones must not answer for the new ones. A value is hashed and never kept
// in the key: this is a rendering of a credential even if nothing reads it.
func (b *bitbucketTokens) lookupFor(records []bbtokens.Record, tokens bbtokens.BundleTokens) *bbtokens.ProjectLookup {
	key := documentsKey(records, tokens)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.lookup != nil && b.key == key {
		return b.lookup
	}
	b.lookup, b.key = bbtokens.NewProjectLookup(records, tokens, b.client), key
	return b.lookup
}

// documentsKey is the digest of the two things a lookup's answers depend on:
// the records it may spend and the values it spends them with.
func documentsKey(records []bbtokens.Record, tokens bbtokens.BundleTokens) [sha256.Size]byte {
	h := sha256.New()
	write := func(s string) {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	for _, rec := range records {
		write(string(rec.Name))
		write(string(rec.Type))
		write(rec.ProjectUUID)
		write(strings.Join(rec.Coverage, "\x00"))
	}
	if tokens != nil {
		for _, name := range tokens.Names() {
			write(string(name))
			if value, ok := tokens.Value(name); ok {
				write(value)
			}
		}
	}
	var key [sha256.Size]byte
	copy(key[:], h.Sum(nil))
	return key
}

// tokenRecords is the node's typed records as the contract's Record: the
// entries of its bitbucket_tokens key (T3), each carrying the same coverage
// spelling a Record's does (R5).
//
// The legacy bitbucket-token key is deliberately not among them: it is the
// implicit record of R3, read off the snapshot's own block by the selector, and
// a node that never stored a typed token therefore yields the one record it
// always had.
func tokenRecords(snap bbtokens.Snapshot) []bbtokens.Record {
	if snap.Policy == nil || snap.Policy.BitbucketTokens == nil {
		return nil
	}
	entries := snap.Policy.BitbucketTokens.Entries
	out := make([]bbtokens.Record, 0, len(entries))
	for _, e := range entries {
		out = append(out, bbtokens.Record{
			Name:        bbtokens.Name(e.Name),
			Type:        bbtokens.Type(e.Type),
			Coverage:    e.Coverage,
			ProjectUUID: e.ProjectUUID,
		})
	}
	return out
}

// tokenStatuses is the contract's StatusStore over the creds state store's
// Bitbucket-token subtree (T6). The two vocabularies are one — the contract's
// states ARE the creds marks' states (F5) — so the mapping is the fields and
// nothing else, and a token is named the same way in both: the document key it
// is in the bundle under.
type tokenStatuses struct {
	store creds.TokenStatusStore
}

var _ bbtokens.StatusStore = tokenStatuses{}

// Read answers one token's status, and the zero status for a token the store
// holds nothing for, which the contract reads as available.
func (s tokenStatuses) Read(ctx context.Context, name bbtokens.Name) (bbtokens.Status, error) {
	all, err := s.store.TokenStatuses(ctx)
	if err != nil {
		return bbtokens.Status{}, err
	}
	return statusOf(all[string(name)]), nil
}

// Write records one token's status over the status the store holds for it.
//
// The write starts from what it read, so the facts the contract's Status does
// not carry — when the hold began and the wait it doubles from — are kept: the
// mark a 429 leaves is the creds store's own algebra, and a write that replaced
// the node whole would drop the half of it this route cannot state.
func (s tokenStatuses) Write(ctx context.Context, name bbtokens.Name, st bbtokens.Status) error {
	_, err := s.store.UpdateTokenStatus(ctx, string(name), func(prev creds.TokenStatus, _ bool) (creds.TokenStatus, error) {
		prev.State, prev.Until = creds.State(st.State), st.Until
		prev.VerifiedAt, prev.Identity = st.VerifiedAt, st.Identity
		prev.Count = st.Count
		return prev, nil
	})
	return err
}

// CountRequest records one request served with the token, in the window the
// store keeps for it (D2356). The count is advisory: nothing selects on it, and
// the caller drops the error rather than failing a call bitbucket.org already
// served.
func (s tokenStatuses) CountRequest(ctx context.Context, name bbtokens.Name) error {
	_, err := s.store.UpdateTokenStatus(ctx, string(name), func(prev creds.TokenStatus, _ bool) (creds.TokenStatus, error) {
		return prev.RecordRequest(time.Now()), nil
	})
	return err
}

// statusOf is one stored status as the contract states it.
func statusOf(st creds.TokenStatus) bbtokens.Status {
	return bbtokens.Status{
		State:      bbtokens.State(st.State),
		Until:      st.Until,
		VerifiedAt: st.VerifiedAt,
		Identity:   st.Identity,
		Count:      st.Count,
	}
}
