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
// What the suite cannot hold is where a store is *slower* (a lookup by a field
// that is not the document's id, a record written twice where the memory store
// writes once); those are named in firestore_store.go and are not answers, so
// they are not cases here.

import (
	"context"
	"errors"
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

// storeSuite is one case of the suite and the store it runs against.
func storeSuite(t *testing.T, name string, open func(t *testing.T) Store) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		t.Run("AnIssuanceIsRecordedOnce", func(t *testing.T) { testIssuanceOnce(t, open) })
		t.Run("AnIssuanceKeepsTheSourceItWasWrittenUnder", func(t *testing.T) { testIssuanceSource(t, open) })
		t.Run("AnApplicationIsUpsertedById", func(t *testing.T) { testApplicationUpsert(t, open) })
		t.Run("AVerificationIsUpsertedById", func(t *testing.T) { testVerificationUpsert(t, open) })
		t.Run("ASubscriptionIsReadBackById", func(t *testing.T) { testSubscription(t, open) })
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
	sortStrings(sources)
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

// sortStrings orders a suite's list in place; the stores answer a list in
// different orders (one appends, one sorts by document id) and the interface
// promises neither.
func sortStrings(in []string) {
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && in[j] < in[j-1]; j-- {
			in[j], in[j-1] = in[j-1], in[j]
		}
	}
}
