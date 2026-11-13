// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The rules store.go states about its interface, as a suite: one set of cases
// run against the store a test has always driven and against the one the deploy
// names (firestore_store.go over the portal's document store). Two stores and
// one suite is the point — the durable store is a second implementation of
// rules that until now only one implementation kept, and a rule the second one
// breaks is a rule that would first be broken in production, on the records of
// whoever holds a licence.
//
// What the suite cannot hold is where the two stores differ on purpose. One is
// a difference of cost — a record that moves one field is read and written back
// whole here, where the memory store writes once (TouchKey) — and it is named in
// firestore_store.go. The other is a difference of answer, a record nobody
// named, and it is a case of its own below, because the suite runs against both
// stores and so can only hold what they agree on.

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/keyservice/docstore"
)

// The suite's own instants. They are fixed and in UTC because every case
// compares what a store answered with a value written here: a reading of the
// clock would make a failure a race rather than a rule.
var (
	issuedAt  = time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)
	expiresAt = issuedAt.Add(365 * 24 * time.Hour)
)

// storeSuite runs the suite against one store, under the name it is reported
// as. The store comes from a constructor and not a value, because each case
// needs one the earlier cases have not written to.
func storeSuite(t *testing.T, name string, open func(t *testing.T) Store) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		t.Run("AnIssuanceIsRecordedOnce", func(t *testing.T) { testIssuanceOnce(t, open) })
		t.Run("AnIssuanceKeepsTheSourceItWasWrittenUnder", func(t *testing.T) { testIssuanceSource(t, open) })
		t.Run("AnApplicationIsUpsertedById", func(t *testing.T) { testApplicationUpsert(t, open) })
		t.Run("AVerificationIsUpsertedById", func(t *testing.T) { testVerificationUpsert(t, open) })
		t.Run("ASubscriptionIsReadBackById", func(t *testing.T) { testSubscription(t, open) })
		t.Run("ALookupOfANameTheStoreCannotFileIsNotHeld", func(t *testing.T) { testUnstorableLookup(t, open) })
		t.Run("ARevocationIsWrittenOnce", func(t *testing.T) { testRevocationOnce(t, open) })
		t.Run("ARevocationNamesTheKeyItWithdraws", func(t *testing.T) { testRevocationUnnamed(t, open) })
		t.Run("AKeyStateKeepsItsDigestThroughASighting", func(t *testing.T) { testTouchKey(t, open) })
		t.Run("AHandoverIsAnsweredInsideItsWindow", func(t *testing.T) { testHandoverWindow(t, open) })
		t.Run("ATrialIsClaimedOncePerInstallationAndAccount", func(t *testing.T) { testTrialOnce(t, open) })
	})
}

func TestStoreOnDocstoreConformance(t *testing.T) {
	storeSuite(t, "the memory store", func(t *testing.T) Store { return &Memory{} })
	storeSuite(t, "the document store", func(t *testing.T) Store { return NewDocStore(docstore.NewMemory()) })
}

// record is one issuance: §7.3's fields with no key material, which is the
// whole of what a store holds of a key (store.go's Record).
func record(source string) Record {
	return Record{
		Source:       source,
		KeyID:        "fgk_" + source,
		Plan:         paidPlanName,
		Tier:         "team",
		Features:     []string{"seats"},
		Seats:        25,
		Organisation: "acme.example",
		Email:        applicantAddress,
		Issued:       issuedAt,
		Expires:      expiresAt,
	}
}

func testIssuanceOnce(t *testing.T, open func(t *testing.T) Store) {
	ctx := context.Background()
	store := open(t)
	first := record("evt_1")
	if err := store.SaveIssuance(ctx, first); err != nil {
		t.Fatalf("SaveIssuance = %v, want the record kept", err)
	}
	again := record("evt_1")
	again.KeyID = "fgk_other"
	if err := store.SaveIssuance(ctx, again); !errors.Is(err, ErrAlreadyRecorded) {
		t.Fatalf("SaveIssuance of a source the store holds = %v, want ErrAlreadyRecorded: §5.2's redelivery mints nothing, and the second delivery must not replace the record of the first either", err)
	}
	held, err := store.Issuances(ctx)
	if err != nil {
		t.Fatalf("Issuances = %v", err)
	}
	if len(held) != 1 {
		t.Fatalf("the store holds %d issuance(s), want 1: a refused save is not a second record", len(held))
	}
	if held[0].KeyID != first.KeyID {
		t.Errorf("the issuance is %q, want %q: the record the refused save carried must not have reached the store", held[0].KeyID, first.KeyID)
	}
}

func testIssuanceSource(t *testing.T, open func(t *testing.T) Store) {
	ctx := context.Background()
	store := open(t)
	for _, source := range []string{"evt_1", "evt_2", "rotation:fgk_1"} {
		if err := store.SaveIssuance(ctx, record(source)); err != nil {
			t.Fatalf("SaveIssuance(%s) = %v", source, err)
		}
	}
	held, err := store.Issuances(ctx)
	if err != nil {
		t.Fatalf("Issuances = %v", err)
	}
	sources := make([]string, 0, len(held))
	for _, r := range held {
		sources = append(sources, r.Source)
	}
	// Sorted here because the two stores answer a list in different orders —
	// one appends, one files by document id — and the interface promises
	// neither.
	slices.Sort(sources)
	if strings.Join(sources, ",") != "evt_1,evt_2,rotation:fgk_1" {
		t.Errorf("the store holds %v, want one record per source: a source with a colon in it — a rotation's — is a name like any other and not a path the store may split", sources)
	}
}

func testApplicationUpsert(t *testing.T, open func(t *testing.T) Store) {
	ctx := context.Background()
	store := open(t)
	for _, decision := range []Decision{Undecided, Approved} {
		if err := store.SaveApplication(ctx, Application{
			ID: "req_1", Plan: openPlanName, Email: applicantAddress, Detail: "a widget",
			At: issuedAt, Decision: decision, DecidedAt: issuedAt,
		}); err != nil {
			t.Fatalf("SaveApplication = %v", err)
		}
	}
	held, err := store.Applications(ctx)
	if err != nil {
		t.Fatalf("Applications = %v", err)
	}
	if len(held) != 1 {
		t.Fatalf("the store holds %d application(s), want 1: a request is saved under its own id and a second save is the same request answered", len(held))
	}
	if held[0].Decision != Approved {
		t.Errorf("the request's decision is %q, want %q: the second save is the state the store holds", held[0].Decision, Approved)
	}
}

func testVerificationUpsert(t *testing.T, open func(t *testing.T) Store) {
	ctx := context.Background()
	store := open(t)
	pending := Verification{ID: "tok_1", Plan: openPlanName, Email: applicantAddress, Expires: expiresAt}
	if err := store.SaveVerification(ctx, pending); err != nil {
		t.Fatalf("SaveVerification = %v", err)
	}
	spent := pending
	spent.Used = true
	if err := store.SaveVerification(ctx, spent); err != nil {
		t.Fatalf("SaveVerification of the spent token = %v", err)
	}
	held, err := store.Verifications(ctx)
	if err != nil {
		t.Fatalf("Verifications = %v", err)
	}
	if len(held) != 1 || !held[0].Used {
		t.Fatalf("the store holds %+v, want one spent token: a save under an id the store holds is that record and not a second", held)
	}
}

func testSubscription(t *testing.T, open func(t *testing.T) Store) {
	ctx := context.Background()
	store := open(t)
	sub := Subscription{
		ID: "sub_1", Organisation: "acme.example", WorkspaceID: "acme-widget",
		Seats: 25, TermDays: 365, Email: applicantAddress, At: issuedAt, Expires: expiresAt,
	}
	if err := store.SaveSubscription(ctx, sub); err != nil {
		t.Fatalf("SaveSubscription = %v", err)
	}
	held, found, err := store.Subscription(ctx, sub.ID)
	if err != nil || !found {
		t.Fatalf("Subscription(%s) = (%v, %v), want the record just saved", sub.ID, found, err)
	}
	if held.WorkspaceID != sub.WorkspaceID || held.Seats != sub.Seats || !held.Expires.Equal(sub.Expires) {
		t.Errorf("the subscription came back as %+v, want %+v", held, sub)
	}
	if _, found, err := store.Subscription(ctx, "sub_nobody"); err != nil || found {
		t.Errorf("Subscription of an id nobody saved = (%v, %v), want (false, nil): a record that is not there is an answer", found, err)
	}
	all, err := store.Subscriptions(ctx)
	if err != nil {
		t.Fatalf("Subscriptions = %v", err)
	}
	if len(all) != 1 {
		t.Errorf("the store holds %d subscription(s), want 1", len(all))
	}
}

// testUnstorableLookup is the read half of a name the store cannot file, and it
// is the half whose id comes out of a request: renew.go's proved hands KeyState
// and Revocation the key_id of an unauthenticated body, and a merchant event
// naming no subscription reaches Subscription. A store that answered the
// refusal its write path owes would turn a stranger's guess into the service
// failing to read, where every route answers one uniform refusal, and it would
// answer one request two ways — store.go's memory store files whatever it is
// handed and has always answered (false, nil) here.
//
// The empty id is a row of the same shape and not a rule of its own: both are
// names no record of the service could have, and both are answered as a record
// the store does not hold.
func testUnstorableLookup(t *testing.T, open func(t *testing.T) Store) {
	for _, tc := range []struct{ why, id string }{
		{"an id nobody named", ""},
		{"an id carrying the store's path separator", "a/b"},
		{"an id of two periods", ".."},
		{"an id in the database's reserved form", "__x__"},
		{"an id carrying a control character", "fgk_one\ttwo"},
	} {
		t.Run(tc.why, func(t *testing.T) {
			ctx := context.Background()
			store := open(t)
			check := func(what string, held bool, err error) {
				t.Helper()
				if held || err != nil {
					t.Errorf("%s of %s = (%v, %v), want (false, nil): what the store cannot file it also does not hold, and a lookup's id is the request's, so the refusal belongs to the caller who wrote the record and not to the one who asked for it", what, tc.why, held, err)
				}
			}
			_, held, err := store.KeyState(ctx, tc.id)
			check("KeyState", held, err)
			_, held, err = store.Revocation(ctx, tc.id)
			check("Revocation", held, err)
			_, held, err = store.Subscription(ctx, tc.id)
			check("Subscription", held, err)
			_, held, err = store.Handover(ctx, tc.id, issuedAt)
			check("Handover", held, err)
		})
	}
}

func testRevocationOnce(t *testing.T, open func(t *testing.T) Store) {
	ctx := context.Background()
	store := open(t)
	if absent, _, err := store.KeyState(ctx, "fgk_1"); err != nil || absent.KeyID != "" {
		t.Fatalf("KeyState of a key nothing saved = (%+v, %v)", absent, err)
	}
	first := Revocation{KeyID: "fgk_1", Reason: "refund", At: issuedAt}
	if err := store.Revoke(ctx, first); err != nil {
		t.Fatalf("Revoke = %v", err)
	}
	second := Revocation{KeyID: "fgk_1", Reason: "chargeback", At: issuedAt.Add(time.Hour)}
	if err := store.Revoke(ctx, second); err != nil {
		t.Fatalf("Revoke of a key already withdrawn = %v, want it accepted as the same revocation", err)
	}
	held, found, err := store.Revocation(ctx, "fgk_1")
	if err != nil || !found {
		t.Fatalf("Revocation(fgk_1) = (%v, %v), want the entry", found, err)
	}
	if held.Reason != first.Reason || !held.At.Equal(first.At) {
		t.Errorf("the entry is %+v, want %+v: §8's list only grows and a second delivery does not edit the first", held, first)
	}
	all, err := store.Revocations(ctx)
	if err != nil {
		t.Fatalf("Revocations = %v", err)
	}
	if len(all) != 1 {
		t.Errorf("the list holds %d entries, want 1", len(all))
	}
}

func testRevocationUnnamed(t *testing.T, open func(t *testing.T) Store) {
	store := open(t)
	if err := store.Revoke(context.Background(), Revocation{Reason: "refund", At: issuedAt}); !errors.Is(err, ErrRevocationUnnamed) {
		t.Errorf("Revoke with no key_id = %v, want ErrRevocationUnnamed: an entry under the empty id would answer every lookup of an unnamed key", err)
	}
}

func testTouchKey(t *testing.T, open func(t *testing.T) Store) {
	ctx := context.Background()
	store := open(t)
	digest := freshDigest(t)
	if err := store.SaveKeyState(ctx, KeyState{KeyID: "fgk_1", WorkspaceID: "acme-widget", RenewalSecretHash: digest, LastSeen: issuedAt}); err != nil {
		t.Fatalf("SaveKeyState = %v", err)
	}
	if err := store.TouchKey(ctx, "fgk_1", issuedAt.Add(time.Hour)); err != nil {
		t.Fatalf("TouchKey = %v", err)
	}
	held, found, err := store.KeyState(ctx, "fgk_1")
	if err != nil || !found {
		t.Fatalf("KeyState(fgk_1) = (%v, %v)", found, err)
	}
	if !held.LastSeen.Equal(issuedAt.Add(time.Hour)) {
		t.Errorf("the key's last sighting is %v, want %v", held.LastSeen, issuedAt.Add(time.Hour))
	}
	if held.RenewalSecretHash != digest {
		t.Errorf("the sighting replaced the key's renewal digest with %q: what a sighting writes is the instant and nothing else", held.RenewalSecretHash)
	}

	if err := store.TouchKey(ctx, "fgk_1", issuedAt); err != nil {
		t.Fatalf("TouchKey with an earlier instant = %v", err)
	}
	held, _, err = store.KeyState(ctx, "fgk_1")
	if err != nil {
		t.Fatalf("KeyState(fgk_1) = %v", err)
	}
	if !held.LastSeen.Equal(issuedAt.Add(time.Hour)) {
		t.Errorf("an out-of-order sighting moved the key's last-seen to %v, want it left at %v: the instant only moves forward", held.LastSeen, issuedAt.Add(time.Hour))
	}

	if err := store.TouchKey(ctx, "fgk_nobody", issuedAt); err != nil {
		t.Fatalf("TouchKey of a key the store does not hold = %v, want nil", err)
	}
	if _, found, err := store.KeyState(ctx, "fgk_nobody"); err != nil || found {
		t.Errorf("after a sighting of a key the store never held, KeyState = (%v, %v), want it left alone: an entry invented here would be a key with no digest, which is the one state that could never be renewed", found, err)
	}
}

func testHandoverWindow(t *testing.T, open func(t *testing.T) Store) {
	ctx := context.Background()
	store := open(t)
	held := Handover{
		KeyID: "fgk_1", SecretHash: freshDigest(t),
		Key: "fgk_2", RenewalSecret: "secret-issued-as-the-test-runs",
		Until: issuedAt.Add(24 * time.Hour),
	}
	if err := store.SaveHandover(ctx, held, issuedAt); err != nil {
		t.Fatalf("SaveHandover = %v", err)
	}
	inside, found, err := store.Handover(ctx, "fgk_1", issuedAt.Add(time.Hour))
	if err != nil || !found {
		t.Fatalf("Handover inside the window = (%v, %v), want the rotation", found, err)
	}
	if inside.Key != held.Key || inside.SecretHash != held.SecretHash {
		t.Errorf("the handover came back as %+v, want %+v", inside, held)
	}
	if expired, found, err := store.Handover(ctx, "fgk_1", held.Until); err != nil || found {
		t.Errorf("Handover at the instant the window closes = (%+v, %v), want it answered as absent: the window is what the promise bounds the successor's key and secret by", expired, err)
	}
	if err := store.SaveHandover(ctx, Handover{Key: "fgk_3"}, issuedAt); !errors.Is(err, ErrHandoverUnnamed) {
		t.Errorf("SaveHandover with no key_id = %v, want ErrHandoverUnnamed: an entry under the empty id would answer for every key nobody named, and this one answers with a key", err)
	}
}

func testTrialOnce(t *testing.T, open func(t *testing.T) Store) {
	ctx := context.Background()
	store := open(t)
	claimed := Trial{Installation: "acme-widget", Account: applicantAddress, KeyID: "fgk_1", At: issuedAt}
	if err := store.ClaimTrial(ctx, claimed); err != nil {
		t.Fatalf("ClaimTrial = %v, want the trial handed out", err)
	}
	if err := store.ClaimTrial(ctx, Trial{Installation: "acme-widget", Account: "other@acme.example", KeyID: "fgk_2", At: issuedAt}); !errors.Is(err, ErrTrialAlreadyClaimed) {
		t.Errorf("a second trial for the same installation = %v, want ErrTrialAlreadyClaimed", err)
	}
	if err := store.ClaimTrial(ctx, Trial{Installation: "acme-gadget", Account: applicantAddress, KeyID: "fgk_3", At: issuedAt}); !errors.Is(err, ErrTrialAlreadyClaimed) {
		t.Errorf("a second trial for the same account at another installation = %v, want ErrTrialAlreadyClaimed: §14's rule is one trial per installation *or* account", err)
	}
	if err := store.ClaimTrial(ctx, Trial{Installation: "acme-gadget", KeyID: "fgk_4", At: issuedAt}); !errors.Is(err, ErrTrialUnnamed) {
		t.Errorf("a trial with no account = %v, want ErrTrialUnnamed", err)
	}
	held, err := store.Trials(ctx)
	if err != nil {
		t.Fatalf("Trials = %v", err)
	}
	if len(held) != 1 || held[0].KeyID != claimed.KeyID {
		t.Errorf("the store holds %+v, want the one trial that was claimed: a refused claim is not a trial", held)
	}
	// The account a refused claim named is not spent by the refusal: a person
	// whose installation already had a trial may still have one elsewhere.
	if err := store.ClaimTrial(ctx, Trial{Installation: "acme-sprocket", Account: "other@acme.example", KeyID: "fgk_5", At: issuedAt}); err != nil {
		t.Errorf("ClaimTrial for an account that was only named by a refused claim = %v, want the trial handed out", err)
	}
}

// TestTheDocStoreDropsAnExpiredHandover is the one thing the suite above cannot
// see: that the store stops *holding* a handover once its window has closed.
// What a handover holds is a successor's key and its renewal secret, so a
// durable store that answered "absent" and kept the record would be the growing
// list of live credentials store.go's Handover refuses — and the answer through
// the interface is the same either way, which is why this reads the document
// store the records are actually kept in.
//
// The sweep runs before the write and before the read, as store.go's memory
// store runs it, so a record the write that follows is the next call's to
// collect: what this pins is that every call collects, not that one write can.
func TestTheDocStoreDropsAnExpiredHandover(t *testing.T) {
	ctx := context.Background()
	db := docstore.NewMemory()
	store := NewDocStore(db)
	live := Handover{
		KeyID: "fgk_1", SecretHash: freshDigest(t),
		Key: "fgk_2", RenewalSecret: "secret-issued-as-the-test-runs",
		Until: issuedAt.Add(24 * time.Hour),
	}
	if err := store.SaveHandover(ctx, live, issuedAt); err != nil {
		t.Fatalf("SaveHandover = %v", err)
	}
	if held := handovers(t, ctx, db); len(held) != 1 {
		t.Fatalf("the store holds %d handover(s) after one write, want 1", len(held))
	}

	// A window that had already closed when it was written, which is what an
	// instance whose clock is behind leaves behind.
	stale := Handover{
		KeyID: "fgk_9", SecretHash: freshDigest(t),
		Key: "fgk_10", RenewalSecret: "another-secret-issued-as-the-test-runs",
		Until: issuedAt,
	}
	if err := store.SaveHandover(ctx, stale, issuedAt); err != nil {
		t.Fatalf("SaveHandover of a window that had already closed = %v", err)
	}

	// The next write sweeps it and keeps the one still inside its window: a
	// sweep that took the live window with it would lose the successor's key
	// and secret, which is the one thing a handover exists to answer with.
	next := Handover{
		KeyID: "fgk_20", SecretHash: freshDigest(t),
		Key: "fgk_21", RenewalSecret: "a-third-secret-issued-as-the-test-runs",
		Until: live.Until,
	}
	if err := store.SaveHandover(ctx, next, issuedAt); err != nil {
		t.Fatalf("SaveHandover = %v", err)
	}
	if held := handovers(t, ctx, db); len(held) != 2 || held[0].KeyID != live.KeyID || held[1].KeyID != next.KeyID {
		t.Errorf("the store holds %+v, want the two handovers inside their windows (%s, %s) and not the one whose window had closed", held, live.KeyID, next.KeyID)
	}

	// The read sweeps too, so a key nobody asks about again is still collected
	// by whichever call comes next.
	if expired, found, err := store.Handover(ctx, live.KeyID, live.Until); err != nil || found {
		t.Fatalf("Handover past the window = (%+v, %v), want it answered as absent", expired, err)
	}
	if held := handovers(t, ctx, db); len(held) != 0 {
		t.Errorf("after a read past its window, the store still holds %+v, want nothing: what the sweep would not collect is key material kept for the life of the database", held)
	}
}

// handovers is what the document store really holds, read past the store's own
// answers: the suite in this file goes through the interface and this cannot.
func handovers(t *testing.T, ctx context.Context, db docstore.Store) []Handover {
	t.Helper()
	docs, err := db.Query(ctx, handoverCollection, docstore.Query{})
	if err != nil {
		t.Fatalf("reading the handovers the store keeps: %v", err)
	}
	out := make([]Handover, 0, len(docs))
	for _, d := range docs {
		var h Handover
		if err := d.Decode(&h); err != nil {
			t.Fatalf("decoding a handover the store keeps: %v", err)
		}
		out = append(out, h)
	}
	return out
}

// TestTheDocstoreRefusesARecordNobodyNamed is the one difference between the two
// stores, and it is a case of its own because the suite above cannot hold it:
// the suite runs against both, and store.go's memory store, which has no
// document ids, keeps a record whose own id is empty. Nothing in the service
// produces one — merchant's redelivery source, renew's rotation and admin's
// approved request all name what they minted for — so what this pins is that a
// caller who one day does is told rather than served.
func TestTheDocstoreRefusesARecordNobodyNamed(t *testing.T) {
	ctx := context.Background()
	store := NewDocStore(docstore.NewMemory())
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"an issuance with no source", func() error { return store.SaveIssuance(ctx, Record{KeyID: "fgk_1"}) }},
		{"an application with no id", func() error { return store.SaveApplication(ctx, Application{Plan: openPlanName}) }},
		{"a verification with no id", func() error { return store.SaveVerification(ctx, Verification{Plan: openPlanName}) }},
		{"a subscription with no id", func() error { return store.SaveSubscription(ctx, Subscription{Organisation: "acme.example"}) }},
		{"a key state with no key id", func() error { return store.SaveKeyState(ctx, KeyState{WorkspaceID: "acme-widget"}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, docstore.ErrUnnamed) {
				t.Errorf("%s = %v, want docstore.ErrUnnamed: the empty id is the one id every unnamed record would share, so a store that kept one would answer every lookup that named nothing", tc.name, err)
			}
		})
	}
}

// txWatch is a document store that records how it was used: how many
// transactions were opened over it, which calls went through a transaction's own
// object, and which reached the store itself.
//
// The suite proves what each of DocStore's rules answers. What no sequential
// case can see is how the answer was reached: firestore_store.go leads with
// "everything a check needs is one transaction", and the reason this package
// depends on the seam's transactions at all is the three checks written that
// way — an issuance under a source the store holds, a revocation, and §14's
// trial. Split any of them into a read and a write beside the transaction and
// the suite still passes: two callers asking at once, which is the pair the rule
// exists to refuse, is exactly what a sequential case cannot produce.
//
// It is the two paths and not the transaction's depth that are counted, because
// the store's own methods are reachable from inside a transaction's function as
// well — that is the mutation worth catching, and it leaves a call that reads or
// writes a document the transaction's read set never saw.
type txWatch struct {
	db docstore.Store

	opened  int
	through int
	direct  []string
}

func (w *txWatch) directCall(call string) {
	w.direct = append(w.direct, call)
}

func (w *txWatch) Get(ctx context.Context, collection, id string, out any) (bool, error) {
	w.directCall("Get " + collection + "/" + id)
	return w.db.Get(ctx, collection, id, out)
}

func (w *txWatch) Put(ctx context.Context, collection, id string, doc any) error {
	w.directCall("Put " + collection + "/" + id)
	return w.db.Put(ctx, collection, id, doc)
}

func (w *txWatch) Delete(ctx context.Context, collection, id string) error {
	w.directCall("Delete " + collection + "/" + id)
	return w.db.Delete(ctx, collection, id)
}

func (w *txWatch) Query(ctx context.Context, collection string, q docstore.Query) ([]docstore.Document, error) {
	return w.db.Query(ctx, collection, q)
}

func (w *txWatch) RunTransaction(ctx context.Context, fn func(ctx context.Context, tx docstore.Transaction) error) error {
	w.opened++
	return w.db.RunTransaction(ctx, func(ctx context.Context, tx docstore.Transaction) error {
		return fn(ctx, &txWatchTx{tx: tx, watch: w})
	})
}

// txWatchTx is the transaction object the watcher hands out: every call a
// function makes through it is counted as one that is part of the transaction.
type txWatchTx struct {
	tx    docstore.Transaction
	watch *txWatch
}

func (t *txWatchTx) Get(ctx context.Context, collection, id string, out any) (bool, error) {
	t.watch.through++
	return t.tx.Get(ctx, collection, id, out)
}

func (t *txWatchTx) Put(ctx context.Context, collection, id string, doc any) error {
	t.watch.through++
	return t.tx.Put(ctx, collection, id, doc)
}

func (t *txWatchTx) Query(ctx context.Context, collection string, q docstore.Query) ([]docstore.Document, error) {
	t.watch.through++
	return t.tx.Query(ctx, collection, q)
}

// TestTheDocstoreChecksAndWritesInOneTransaction holds the rule the store's
// header leads with, which nothing else here does: the two stores answer the
// same way whether a check runs inside a transaction or beside one, so only a
// store that watches the calls can tell the two apart.
//
// Each rule is driven once. One transaction has to be opened — not none, and not
// two, which is a check committed apart from the write it decides — every call it
// makes has to go through that transaction's own object, and none may reach the
// store itself, because a read made there is a read made on a state the write
// does not commit onto.
func TestTheDocstoreChecksAndWritesInOneTransaction(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		call func(store Store) error
	}{
		{"an issuance under a source the store holds", func(store Store) error {
			return store.SaveIssuance(ctx, record("evt_1"))
		}},
		{"a revocation", func(store Store) error {
			return store.Revoke(ctx, Revocation{KeyID: "fgk_1", Reason: "refund", At: issuedAt})
		}},
		{"a trial, which reads two documents", func(store Store) error {
			return store.ClaimTrial(ctx, Trial{Installation: "acme-widget", Account: applicantAddress, KeyID: "fgk_1", At: issuedAt})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			watched := &txWatch{db: docstore.NewMemory()}
			if err := tc.call(NewDocStore(watched)); err != nil {
				t.Fatalf("%s = %v, want the record kept", tc.name, err)
			}
			if watched.opened != 1 {
				t.Errorf("%s opened %d transaction(s) over the store, want 1: the check and the write it decides are one transaction, or two callers asking at once both get the answer they asked for", tc.name, watched.opened)
			}
			if watched.through == 0 {
				t.Errorf("%s made no call through the transaction it opened: a transaction nothing read and nothing wrote is not the check-then-write the rule asks for", tc.name)
			}
			if len(watched.direct) != 0 {
				t.Errorf("%s reached the store itself: %v. A check read there and a write made beside it is the check-then-write the store's rules are written to refuse, and no sequential case above can tell it from this one", tc.name, watched.direct)
			}
		})
	}
}
