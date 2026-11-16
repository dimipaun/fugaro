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
	statuses creds.TokenStatusStore
	client   *http.Client

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
	return &bitbucketTokens{statuses: statuses, client: client}
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
	status, err := b.selectionStatuses(ctx, snap)
	if err != nil {
		return bbtokens.Choice{}, err
	}
	return bbtokens.NewSelector(records, b.lookupFor(records, snap.Bundle), status).Select(ctx, snap, canonical, write)
}

// selectionStatuses is the status store one request's selection reads: every
// token's status, read ONCE, at the head of the selection.
//
// The read is made here rather than left to the selector's per-candidate Read
// for two reasons, and the first is the one that matters.
//
// A store that cannot answer is this request's failure and not a skip. The
// selector's own rule is that a candidate whose status cannot be read is not a
// candidate (select.go, R14's fail-closed direction), which is right for a
// store answering about one token; but the store's read is the SUBTREE's and
// all-or-nothing — one node this release cannot decode fails
// creds.RTDBStore.TokenStatuses for every token (readNameTree) — so a transient
// database error, or one hand-edited node under a name no token is, would skip
// every candidate and leave the request refused with the contract's
// token_not_covered: a class that sends the operator to the node's coverage for
// a fault in the state store, answered 400 and never retried. Returning the
// store's own error instead is what the route already does with one: a store
// that could not answer is not the caller's refusal, so the answer is
// state_unavailable.
//
// The second is cost: the subtree read is a whole GET, so one per candidate is
// one per token on every proxied call (O1's own complaint about this route),
// and judging every candidate against ONE read also means a mark cannot be
// half-seen. It is read per request and never held across requests, so a mark
// the previous request wrote is seen by this one — the contract's Snapshot
// makes the same promise for the two documents.
//
// A bundle holding no Bitbucket token at all is the exception and is not asked
// about the store: which tokens exist is the bundle's fact, the selection
// refuses that request as no_bitbucket_token before reading anything, and that
// answer must not change because the state store is unreachable.
func (b *bitbucketTokens) selectionStatuses(ctx context.Context, snap bbtokens.Snapshot) (bbtokens.StatusStore, error) {
	if snap.Bundle == nil || len(snap.Bundle.Names()) == 0 {
		return tokenStatuses{store: b.statuses}, nil
	}
	held, err := b.statuses.TokenStatuses(ctx)
	if err != nil {
		return nil, err
	}
	return selectionStatuses{held: held, store: b.statuses}, nil
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
	return tokenStatuses{store: b.statuses}.Read(ctx, name)
}

// Write records one token's status.
func (b *bitbucketTokens) Write(ctx context.Context, name bbtokens.Name, st bbtokens.Status) error {
	return tokenStatuses{store: b.statuses}.Write(ctx, name, st)
}

// CountRequest records one request served with the token.
func (b *bitbucketTokens) CountRequest(ctx context.Context, name bbtokens.Name) error {
	return tokenStatuses{store: b.statuses}.CountRequest(ctx, name)
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

// selectionStatuses is one selection's status store: every token's status, as
// the ONE read of the subtree found them, so that the candidates are judged
// against one read and a store that could not be read at all is refused before
// a candidate is looked at (bitbucketTokens.selectionStatuses builds it).
//
// A token the read found nothing for reads as the zero status — the contract's
// "nothing recorded", which is available (StatusStore.Read) — and the read is
// not taken again here: a mark D2356 writes during a request is for the NEXT
// request's read, which this request's answer cannot depend on anyway.
type selectionStatuses struct {
	held  map[string]creds.TokenStatus
	store creds.TokenStatusStore
}

var _ bbtokens.StatusStore = selectionStatuses{}

// Read answers the status this selection's one read found for the token.
func (s selectionStatuses) Read(_ context.Context, name bbtokens.Name) (bbtokens.Status, error) {
	return statusOf(s.held[string(name)]), nil
}

// Write and CountRequest are the store's own, and are here because the contract
// states the status store as one interface: the selector writes nothing — the
// route's reactions are the writers, and they write through bitbucketTokens —
// so nothing calls either of these on a selection's view.
func (s selectionStatuses) Write(ctx context.Context, name bbtokens.Name, st bbtokens.Status) error {
	return tokenStatuses{store: s.store}.Write(ctx, name, st)
}

func (s selectionStatuses) CountRequest(ctx context.Context, name bbtokens.Name) error {
	return tokenStatuses{store: s.store}.CountRequest(ctx, name)
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

// Write records one token's MARK — the state and the hold — over the status the
// store holds for it, and never the rest of the status beside it.
//
// The rest is not this writer's to state: the identity and the verification time
// are the add-time check's (R12 — creds.TokenVerified writes them through this
// same store) and the count is CountRequest's (D2356). An empty Identity or a
// zero VerifiedAt in a Status handed here is therefore "this writer says nothing
// about them" and never "the token has none", and copying them in erases what
// the store holds with nothing said about it — silently, and for exactly the
// facts a failing read cannot supply: the whole subtree fails to decode for
// every token while any one node under it does not (creds.RTDBStore
// .TokenStatuses), so one unreadable node anywhere costs every token its
// identity and its verification time at the next mark.
//
// Starting from what the store holds is also what keeps the facts the contract's
// Status does not carry — when the hold began and the wait it doubles from: the
// mark a 429 leaves is the creds store's own algebra, and a write that replaced
// the node whole would drop the half of it this route cannot state.
func (s tokenStatuses) Write(ctx context.Context, name bbtokens.Name, st bbtokens.Status) error {
	_, err := s.store.UpdateTokenStatus(ctx, string(name), func(prev creds.TokenStatus, _ bool) (creds.TokenStatus, error) {
		prev.State, prev.Until = creds.State(st.State), st.Until
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
