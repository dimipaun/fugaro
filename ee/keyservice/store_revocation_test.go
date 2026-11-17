// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The three things a key's life adds to §7.3's issuance record: the revocation
// list (§8), the digest of a key's renewal secret with the instant it last
// called in (§7), and the trials that have been handed out (§14). Every test
// here drives the store directly, because at this point in the plan the store
// is the only surface those three have — the routes that read them are K2.3's
// and K2.7's, and a test written through them would be pinning a handler that
// does not exist yet.
//
// The one exception is the withdrawal itself, which is not a store call at all:
// what a refund withdraws is decided by the merchant's event and the records
// that name the licence (P3.6, F29), and `TestRevocationCoversEveryRotation` is
// here because what it reads back is the list this package's store keeps.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"testing"
	"time"
)

// freshDigest is the digest of a secret generated as the test runs, which is the
// only shape a test may hand the store (.claude/rules/ee.md, R5: a fixture that
// looked like a secret would be one). It is a real SHA-256 of real 256 random
// bits, so what the store compares is the same size of thing the minter writes.
func freshDigest(t *testing.T) string {
	t.Helper()
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatalf("generating a renewal secret's bytes: %v", err)
	}
	sum := sha256.Sum256(secret)
	return hex.EncodeToString(sum[:])
}

// TestRevokedKeyStaysRevoked: §8's list is the one thing in the store that
// nothing takes back. A revoked key is reissued under a new key_id and the old
// one is never renewed, but the old key's holder is still out there and every
// write the service makes for them still arrives: the check the job makes
// before it has heard, the merchant's redelivery, and the reissue itself beside
// it. What this pins is that none of them resurrects the key — the store has no
// write that removes an entry, and the entry a reader gets back is the one the
// first revocation wrote.
func TestRevokedKeyStaysRevoked(t *testing.T) {
	ctx := context.Background()
	store := &Memory{}
	at := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	withdrawn := "lk_test_withdrawn"
	if err := store.Revoke(ctx, Revocation{KeyID: withdrawn, Reason: "refund", At: at}); err != nil {
		t.Fatalf("revoking the key: %v", err)
	}

	if err := store.TouchKey(ctx, withdrawn, at.Add(time.Hour)); err != nil {
		t.Fatalf("the revoked key's next check: %v", err)
	}
	if err := store.SaveKeyState(ctx, KeyState{KeyID: withdrawn, RenewalSecretHash: freshDigest(t), LastSeen: at}); err != nil {
		t.Fatalf("writing the revoked key's state: %v", err)
	}
	if err := store.SaveIssuance(ctx, Record{Source: "txn_test_refund", KeyID: withdrawn, Seats: 5, Email: "buyer@acme.example", Issued: at}); err != nil {
		t.Fatalf("the redelivered purchase: %v", err)
	}
	if err := store.SaveSubscription(ctx, Subscription{ID: "sub_test", Seats: 5, At: at}); err != nil {
		t.Fatalf("the redelivered subscription: %v", err)
	}
	// §8's reissue: a leaked key comes back as a new key_id, and that is a
	// second entry rather than a change to the first.
	reissued := "lk_test_reissued"
	if err := store.Revoke(ctx, Revocation{KeyID: reissued, Reason: "leaked key", At: at.Add(2 * time.Hour)}); err != nil {
		t.Fatalf("revoking the reissued key: %v", err)
	}

	got, held, err := store.Revocation(ctx, withdrawn)
	if err != nil {
		t.Fatalf("reading the revoked key back: %v", err)
	}
	if !held {
		t.Fatal("the key is not revoked any more: a write the service makes for a holder took the revocation back")
	}
	if got.Reason != "refund" || !got.At.Equal(at) {
		t.Errorf("the revocation is now %+v, want the refund at %s: a later write rewrote what the key was revoked for", got, at)
	}
	all, err := store.Revocations(ctx)
	if err != nil {
		t.Fatalf("reading the revocation list: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("the list holds %d revocation(s), want the revoked key and its reissue: %+v", len(all), all)
	}
}

// TestRevocationIsIdempotent: a refund's event is redelivered, and so is a
// chargeback's (§5.2's rule for every merchant event). The second delivery is
// not a second revocation and not an error — it is the same answer, and the
// entry the store keeps is the first one's: the key was revoked at the instant
// the licensor first withdrew it, and a redelivery that carried another reason
// does not move that instant or rewrite that reason.
func TestRevocationIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store := &Memory{}
	at := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	keyID := "lk_test_refunded"
	first := Revocation{KeyID: keyID, Reason: "refund", At: at}

	if err := store.Revoke(ctx, first); err != nil {
		t.Fatalf("the first revocation: %v", err)
	}
	if err := store.Revoke(ctx, first); err != nil {
		t.Fatalf("the redelivered revocation: %v", err)
	}
	if err := store.Revoke(ctx, Revocation{KeyID: keyID, Reason: "chargeback", At: at.Add(48 * time.Hour)}); err != nil {
		t.Fatalf("a revocation later than the first: %v", err)
	}

	all, err := store.Revocations(ctx)
	if err != nil {
		t.Fatalf("reading the revocation list: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("three revocations of one key left %d entr(ies), want one: %+v", len(all), all)
	}
	got, held, err := store.Revocation(ctx, keyID)
	if err != nil || !held {
		t.Fatalf("reading the revoked key back: held=%v err=%v", held, err)
	}
	if got.Reason != first.Reason || !got.At.Equal(first.At) {
		t.Errorf("the entry is %+v, want the first revocation %+v", got, first)
	}
}

// TestRevocationWithNoKeyIsRefused: a revocation is looked up by the key it
// withdraws, so an entry filed under no key at all is the entry that answers
// for every key nobody named — and the answer it gives is "revoked", which
// lapses a project. A licensor whose event carried no key_id has a bug, and the
// store says so rather than writing the record that hides it.
func TestRevocationWithNoKeyIsRefused(t *testing.T) {
	ctx := context.Background()
	store := &Memory{}
	err := store.Revoke(ctx, Revocation{Reason: "refund", At: time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)})
	if !errors.Is(err, ErrRevocationUnnamed) {
		t.Errorf("a revocation that names no key answered %v, want %v", err, ErrRevocationUnnamed)
	}
	if _, held, err := store.Revocation(ctx, ""); err != nil || held {
		t.Errorf("the refused revocation was written anyway: held=%v err=%v", held, err)
	}
}

// TestOneTrialPerInstallation: §14's one trial per installation, keyed by the
// Firebase project id of the installation (K2.7). A second ask from the same
// installation is refused even when it comes from another account, and the
// refusal writes nothing: the store's list is still one trial and the key the
// refused ask would have minted is not in it.
func TestOneTrialPerInstallation(t *testing.T) {
	ctx := context.Background()
	store := &Memory{}
	at := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	first := Trial{Installation: "fugaro-acme-dev", Account: "first@acme.example", KeyID: "lk_test_trial", At: at}
	if err := store.ClaimTrial(ctx, first); err != nil {
		t.Fatalf("the installation's first trial: %v", err)
	}

	second := Trial{Installation: "fugaro-acme-dev", Account: "second@acme.example", KeyID: "lk_test_trial_two", At: at.Add(time.Hour)}
	if err := store.ClaimTrial(ctx, second); !errors.Is(err, ErrTrialAlreadyClaimed) {
		t.Errorf("a second trial for one installation answered %v, want %v", err, ErrTrialAlreadyClaimed)
	}
	// An installation nobody named matches no trial the store holds, so it
	// would be a trial per ask if it were not refused before that.
	unnamed := Trial{Account: "fourth@acme.example", KeyID: "lk_test_trial_four", At: at}
	if err := store.ClaimTrial(ctx, unnamed); !errors.Is(err, ErrTrialUnnamed) {
		t.Errorf("a trial naming no installation answered %v, want %v", err, ErrTrialUnnamed)
	}
	// Another installation is another trial: nothing else in the record is what
	// §14 counts.
	third := Trial{Installation: "fugaro-other-dev", Account: "third@other.example", KeyID: "lk_test_trial_three", At: at}
	if err := store.ClaimTrial(ctx, third); err != nil {
		t.Errorf("another installation's first trial: %v", err)
	}

	trials, err := store.Trials(ctx)
	if err != nil {
		t.Fatalf("reading the trials: %v", err)
	}
	if len(trials) != 2 {
		t.Fatalf("the store holds %d trial(s), want two: %+v", len(trials), trials)
	}
	for _, held := range trials {
		if held.KeyID == second.KeyID {
			t.Errorf("the refused trial was written anyway: %+v", held)
		}
	}
}

// TestOneTrialPerAccount: the other half of §14's pair. An account that has had
// a trial does not get another by naming a second installation — the account is
// the identity the site's sign-in proved (K2.4), and it is the one thing a
// caller cannot shop around for. An account nobody named is refused for the
// same reason as an installation nobody named: it matches nothing the store
// holds, so it would be a fresh trial on every ask.
func TestOneTrialPerAccount(t *testing.T) {
	ctx := context.Background()
	store := &Memory{}
	at := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	account := "holder@acme.example"
	if err := store.ClaimTrial(ctx, Trial{Installation: "fugaro-acme-dev", Account: account, KeyID: "lk_test_trial", At: at}); err != nil {
		t.Fatalf("the account's first trial: %v", err)
	}

	elsewhere := Trial{Installation: "fugaro-acme-staging", Account: account, KeyID: "lk_test_trial_two", At: at}
	if err := store.ClaimTrial(ctx, elsewhere); !errors.Is(err, ErrTrialAlreadyClaimed) {
		t.Errorf("a second trial for one account answered %v, want %v", err, ErrTrialAlreadyClaimed)
	}
	nameless := Trial{Installation: "fugaro-acme-staging", KeyID: "lk_test_trial_three", At: at}
	if err := store.ClaimTrial(ctx, nameless); !errors.Is(err, ErrTrialUnnamed) {
		t.Errorf("a trial naming no account answered %v, want %v", err, ErrTrialUnnamed)
	}

	trials, err := store.Trials(ctx)
	if err != nil {
		t.Fatalf("reading the trials: %v", err)
	}
	if len(trials) != 1 {
		t.Errorf("the store holds %d trial(s), want the one account's: %+v", len(trials), trials)
	}
}

// TestTheRenewalSecretIsKeptAsADigest: the store holds the digest and never the
// secret (§7, D2135), one per key, and it holds a different one for each key —
// a rotation is a new key_id with its own state beside the old one, not a
// rewrite of it, which is what lets the old key's holder still be answered
// while the new key is what they are answered with.
func TestTheRenewalSecretIsKeptAsADigest(t *testing.T) {
	ctx := context.Background()
	store := &Memory{}
	first, second := freshDigest(t), freshDigest(t)
	if first == second {
		t.Fatal("two generated secrets hashed to one digest, so this test cannot tell two keys apart")
	}
	if err := store.SaveKeyState(ctx, KeyState{KeyID: "lk_test_one", RenewalSecretHash: first}); err != nil {
		t.Fatalf("writing the first key's state: %v", err)
	}
	if err := store.SaveKeyState(ctx, KeyState{KeyID: "lk_test_two", RenewalSecretHash: second}); err != nil {
		t.Fatalf("writing the second key's state: %v", err)
	}

	for keyID, want := range map[string]string{"lk_test_one": first, "lk_test_two": second} {
		got, held, err := store.KeyState(ctx, keyID)
		if err != nil || !held {
			t.Fatalf("reading %s back: held=%v err=%v", keyID, held, err)
		}
		if got.RenewalSecretHash != want {
			t.Errorf("%s's state holds %q, want the digest written for it", keyID, got.RenewalSecretHash)
		}
	}
	if _, held, err := store.KeyState(ctx, "lk_test_never_issued"); err != nil || held {
		t.Errorf("a key the store never held answered held=%v err=%v, want neither", held, err)
	}
}

// TestTheLastSeenLeavesTheDigestAlone: a check is a sighting and not a renewal.
// The handler that records one has just compared the digest, and a
// read-modify-write would put back the digest it read — the one a rotation may
// already have replaced — so the touch moves LastSeen in one write and leaves
// the digest as it stands, and it only moves it forward. A key the store does
// not hold is not given an entry by a sighting: a record with no digest is one
// that could never be renewed.
func TestTheLastSeenLeavesTheDigestAlone(t *testing.T) {
	ctx := context.Background()
	store := &Memory{}
	at := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	keyID := "lk_test_checked_in"
	digest := freshDigest(t)
	if err := store.SaveKeyState(ctx, KeyState{KeyID: keyID, RenewalSecretHash: digest}); err != nil {
		t.Fatalf("writing the key's state: %v", err)
	}

	later := at.Add(26 * time.Hour)
	if err := store.TouchKey(ctx, keyID, later); err != nil {
		t.Fatalf("recording the check: %v", err)
	}
	if err := store.TouchKey(ctx, "lk_test_never_issued", later); err != nil {
		t.Fatalf("recording a check for a key the store does not hold: %v", err)
	}
	// A check that crossed a clock that stepped back, arriving after one that
	// carries a later instant, is not the last time anybody saw the key.
	if err := store.TouchKey(ctx, keyID, at); err != nil {
		t.Fatalf("recording an out-of-order check: %v", err)
	}

	got, held, err := store.KeyState(ctx, keyID)
	if err != nil || !held {
		t.Fatalf("reading the key's state back: held=%v err=%v", held, err)
	}
	if !got.LastSeen.Equal(later) {
		t.Errorf("the key's last seen is %s, want %s", got.LastSeen, later)
	}
	if got.RenewalSecretHash != digest {
		t.Errorf("the check rewrote the key's digest to %q, want %q", got.RenewalSecretHash, digest)
	}
	if _, held, err := store.KeyState(ctx, "lk_test_never_issued"); err != nil || held {
		t.Errorf("a sighting of an unknown key wrote a state for it: held=%v err=%v", held, err)
	}
}

// TestRevocationCoversEveryRotation: F29 — revocation is by **entitlement**, so
// a refund withdraws every key the licence ever minted and not the one the
// merchant's own record happens to name. Withdrawing the purchase's `key_id`
// alone leaves the keys that came after it live: a rotation's successor, which
// the customer's installation is running, and the successor a reveal will hand
// over when P3.2 lands. Either is a paid key the money has gone back for, which
// is the gap F29 states and this test is the closing of.
//
// The licence's keys here are the three shapes its records come in: the
// purchase's binding (`binding:<licence>:`), the successor `/renew` minted
// (`rotation:<key>`), and one that names the licence and nothing else — no
// subscription, so the subscription step cannot match it, and no prefix the walk
// follows, so the walk cannot reach it either. The first two are withdrawn by
// the walk as well as by the sweep, and the third is withdrawn by the sweep
// alone: with the sweep taken out, this case fails on exactly that key, which is
// the F29 gap in one line.
//
// That third record is written straight into the store because the route that
// will mint it is P3.2's reveal, which depends on this task and is not here yet;
// its source is the test's own name for the record, and no part of the sweep
// reads a source at all — the entitlement id is the whole of what it matches on.
func TestRevocationCoversEveryRotation(t *testing.T) {
	f, _ := renewing(t)
	ctx := context.Background()
	bought := f.purchase("evt-covers-1", "acme.example", 5, 365)
	licenceID := bought.EntitlementID
	if licenceID == "" {
		t.Fatal("the purchase's key names no licence, so there is nothing here for a refund to withdraw (P3.6)")
	}
	theSubscriptionCovers(t, f, bought.KeyID)
	rotated, _ := renewThroughTheEndpoint(t, f, bought.KeyID, f.secretOf(t, f.mintedKeys()[0]))

	unreachable := Record{
		Source:        "reveal:test:" + bought.KeyID,
		KeyID:         "lk_test_revealed",
		EntitlementID: licenceID,
		Plan:          bought.Plan,
		Tier:          bought.Tier,
		Seats:         bought.Seats,
		Organisation:  bought.Organisation,
		Issued:        f.now,
		Expires:       bought.Expires,
		TermDays:      bought.TermDays,
	}
	if err := f.store.SaveIssuance(ctx, unreachable); err != nil {
		t.Fatalf("writing the reveal's successor into the store: %v", err)
	}
	// Another licence's key, in another workspace: what a refund of this one
	// withdraws is its own keys, and a sweep by anything but the entitlement would
	// take a stranger's licence with it.
	elsewhere, _, _ := heldKeyIn(t, f, PolicyTeamMonthly, "widget-workshop")
	if got := f.issuance(t, elsewhere).EntitlementID; got == licenceID {
		t.Fatalf("the key %s names this licence, so it is not the case's negative", elsewhere)
	}

	at := f.now
	if rec := f.event(Event{ID: "evt-covers-2", Type: EventRefund, OccurredAt: at, Subscription: bought.Subscription}); rec.Code != http.StatusOK {
		t.Fatalf("the refund was answered %d (%q), want it applied", rec.Code, rec.Body.String())
	}

	withdrawn := map[string]Revocation{}
	for _, revocation := range f.revocations() {
		withdrawn[revocation.KeyID] = revocation
	}
	for _, keyID := range []string{bought.KeyID, rotated.KeyID, unreachable.KeyID} {
		got, held := withdrawn[keyID]
		if !held {
			t.Errorf("the refund left the key %s live, want every key of the licence withdrawn: revocation is by entitlement (F29)", keyID)
			continue
		}
		if got.Reason != ReasonRefund {
			t.Errorf("the key %s was withdrawn for %q, want %q", keyID, got.Reason, ReasonRefund)
		}
		if !got.At.Equal(at) {
			t.Errorf("the key %s was withdrawn at %s, want the instant the money went back (%s)", keyID, got.At, at)
		}
	}
	if _, held := withdrawn[elsewhere]; held {
		t.Errorf("the refund withdrew %s, a key of another licence: what the sweep reads is the entitlement and never the workspace or the address", elsewhere)
	}
	if got := len(f.revocations()); got != 3 {
		t.Errorf("the list holds %d entr(ies), want the licence's three keys and nothing else: %+v", got, f.revocations())
	}
}
