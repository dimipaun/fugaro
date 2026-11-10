// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The paid path: the merchant of record's events (subscriptions plan S3.2, the
// design docs/design/key-signing-service.md §5). Every event here is signed the
// way the merchant signs one, delivered to the webhook route, and read back
// through the record and ee/licence's verifier.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/licence"
)

const day = 24 * time.Hour

// TestPurchaseEventNeedsAValidSignature: §5.2 — the raw body is verified before
// it is read, and a body whose signature does not verify is dropped without a
// mint and without a store write.
func TestPurchaseEventNeedsAValidSignature(t *testing.T) {
	f := newFixture(t)
	event := Event{
		ID:           "evt-1",
		Type:         EventPurchase,
		Mode:         Live,
		OccurredAt:   f.now,
		Subscription: "sub-acme.example",
		Organisation: "acme.example",
		Seats:        5,
		TermDays:     365,
	}
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("encoding the event: %v", err)
	}
	for _, tc := range []struct {
		name   string
		header string
	}{
		{name: "no signature at all", header: ""},
		{name: "a signature of another secret", header: signEvent([]byte("another-secret"), f.now, body)},
		{name: "a signature over another body", header: signEvent(f.secret, f.now, append(body, ' '))},
		{name: "a header that is not the merchant's own", header: "h1=deadbeef"},
	} {
		rec := f.deliverRaw(body, tc.header)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: the intake answered %d, want 200: the merchant is not told which of its events a stranger could forge", tc.name, rec.Code)
		}
	}
	if got := f.signer.calls; got != 0 {
		t.Errorf("an unverified event signed %d key(s), want none", got)
	}
	if got := len(f.records()); got != 0 {
		t.Errorf("an unverified event recorded %d key(s), want none", got)
	}
	if _, held, _ := f.store.Subscription(context.Background(), "sub-acme.example"); held {
		t.Error("an unverified event created a subscription, want the body dropped before it is read")
	}

	// The merchant's own signature, and the same event mints.
	f.deliverRaw(body, signEvent(f.secret, f.now, body))
	if got := len(f.records()); got != 1 {
		t.Errorf("the signed event recorded %d key(s), want one", got)
	}
}

// TestPurchaseEventIsIdempotent: §5.2 — the same event delivered twice mints
// once. The second delivery is dropped whole: the clock has moved by then, so a
// state the redelivery re-applied would show it.
func TestPurchaseEventIsIdempotent(t *testing.T) {
	f := newFixture(t)
	first := f.purchase("evt-1", "acme.example", 5, 365)
	f.advance(6 * time.Hour)
	f.deliver(Event{
		ID:           "evt-1",
		Type:         EventPurchase,
		Mode:         Live,
		OccurredAt:   f.now.Add(-6 * time.Hour),
		Subscription: "sub-acme.example",
		Organisation: "acme.example",
		Seats:        5,
		TermDays:     365,
	})

	if got := f.signer.calls; got != 1 {
		t.Errorf("the same event delivered twice signed %d key(s), want one", got)
	}
	records := f.records()
	if len(records) != 1 {
		t.Fatalf("the same event delivered twice recorded %d key(s), want one", len(records))
	}
	sub, held, err := f.store.Subscription(context.Background(), "sub-acme.example")
	if err != nil || !held {
		t.Fatalf("the subscription is %v, held=%v, want the purchase's own", err, held)
	}
	if !sub.Expires.Equal(first.Expires) {
		t.Errorf("the redelivery moved the term's end to %s, want the first delivery's %s", sub.Expires, first.Expires)
	}
}

// TestReplayedOrOutOfOrderEventIsIgnored: §5.2 — the store keeps the
// subscription's own state and an event older than what it holds does not move
// it backwards, so a cancellation followed by a replayed purchase does not
// resurrect a key.
func TestReplayedOrOutOfOrderEventIsIgnored(t *testing.T) {
	f := newFixture(t)
	purchase := Event{
		ID:           "evt-1",
		Type:         EventPurchase,
		Mode:         Live,
		OccurredAt:   f.now,
		Subscription: "sub-acme.example",
		Organisation: "acme.example",
		Seats:        5,
		TermDays:     365,
	}
	f.deliver(purchase)
	f.advance(day)
	f.event(Event{ID: "evt-2", Type: EventCancellation, Organisation: "acme.example"})
	if got := f.signer.calls; got != 1 {
		t.Fatalf("the purchase and the cancellation signed %d key(s), want the purchase's one: a cancellation stops the reissue and withdraws nothing (§5.3)", got)
	}

	// The same purchase replayed, and a purchase the merchant never re-sent but
	// that arrives late with an older instant on it.
	f.deliver(purchase)
	f.event(Event{ID: "evt-3", Type: EventPurchase, OccurredAt: f.now.Add(-2 * day), Organisation: "acme.example", Seats: 9, TermDays: 365})

	if got := f.signer.calls; got != 1 {
		t.Errorf("a replay and an out-of-order purchase signed %d key(s) in all, want the purchase's one", got)
	}
	sub, held, err := f.store.Subscription(context.Background(), "sub-acme.example")
	if err != nil || !held {
		t.Fatalf("the subscription is %v, held=%v", err, held)
	}
	if !sub.Cancelled {
		t.Error("the subscription reads uncancelled after a replayed purchase, want the cancellation to stand")
	}
	if sub.Seats != 5 {
		t.Errorf("the out-of-order purchase moved the seats to %d, want the 5 the subscription holds", sub.Seats)
	}
	// And the renewal that would have followed does not happen.
	f.advance(365*day - 30*day)
	if _, renewed, err := f.svc.Renew(context.Background(), "sub-acme.example"); err != nil || renewed {
		t.Errorf("a cancelled subscription renewed (renewed=%v, err=%v), want nothing: a cancellation stops the reissue", renewed, err)
	}
}

// TestRenewalKeyIssuesThirtyDaysBeforeExpiry: T26 — the next key issues 30 days
// before the current one expires, its `issued` is the day the service runs and
// its `expires` is the end of the term it covers, so the two keys overlap and
// the verifier takes the latest valid one.
func TestRenewalKeyIssuesThirtyDaysBeforeExpiry(t *testing.T) {
	f := newFixture(t)
	first := f.purchase("evt-1", "acme.example", 5, 365)

	f.advance(365*day - 31*day)
	if _, renewed, err := f.svc.Renew(context.Background(), "sub-acme.example"); err != nil || renewed {
		t.Errorf("a renewal 31 days before expiry minted (renewed=%v, err=%v), want nothing: the window opens at 30 (§5.3)", renewed, err)
	}

	f.advance(day)
	minted, renewed, err := f.svc.Renew(context.Background(), "sub-acme.example")
	if err != nil {
		t.Fatalf("the renewal failed: %v", err)
	}
	if !renewed {
		t.Fatal("a renewal at exactly 30 days before expiry minted nothing, want the next key")
	}
	wantExpiry := first.Expires.Add(365 * day)
	if !minted.Record.Expires.Equal(wantExpiry) {
		t.Errorf("the renewal covers to %s, want the term's end %s: issued plus a term plus the 30-day lead (§2)", minted.Record.Expires, wantExpiry)
	}
	if !minted.Record.Issued.Equal(f.now) {
		t.Errorf("the renewal's `issued` is %s, want the service's own clock (%s)", minted.Record.Issued, f.now)
	}
	claims := f.claims(minted.Key)
	if claims.Expires != wantExpiry.Unix() || claims.Issued != f.now.Unix() {
		t.Errorf("the renewal key claims issued=%d expires=%d, want %d and %d", claims.Issued, claims.Expires, f.now.Unix(), wantExpiry.Unix())
	}
	if claims.Seats != 5 || claims.Organisation != "acme.example" {
		t.Errorf("the renewal key claims %d seat(s) for %q, want the subscription's own", claims.Seats, claims.Organisation)
	}
	if got := f.claims(f.mintedKeys()[0]); got.Seats != 5 {
		t.Errorf("the first key claims %d seat(s), want the purchase's 5: a renewal withdraws nothing", got.Seats)
	}

	if _, again, err := f.svc.Renew(context.Background(), "sub-acme.example"); err != nil || again {
		t.Errorf("a second renewal for the same term minted (renewed=%v, err=%v), want one mint per (subscription, term, seats)", again, err)
	}
}

// TestSeatChangeReissues: §5.3 — `seats` is a claim in the key, so a customer
// who adds or removes seats holds a key that states the number they bought. The
// seats move; the term the customer already paid for does not.
func TestSeatChangeReissues(t *testing.T) {
	f := newFixture(t)
	first := f.purchase("evt-1", "acme.example", 3, 365)
	f.advance(30 * day)
	f.event(Event{ID: "evt-2", Type: EventSeatChange, Organisation: "acme.example", Seats: 5, TermDays: 365})

	records := f.records()
	if len(records) != 2 {
		t.Fatalf("the seat change left %d key(s), want the purchase's and its own", len(records))
	}
	changed := records[1]
	if changed.Seats != 5 {
		t.Errorf("the reissued key claims %d seat(s), want the 5 the customer holds", changed.Seats)
	}
	if !changed.Expires.Equal(first.Expires) {
		t.Errorf("the reissued key covers to %s, want the term's end %s: a seat change reissues inside the term the customer paid for", changed.Expires, first.Expires)
	}
	claims := f.claims(f.mintedKeys()[1])
	if claims.Seats != 5 || claims.Expires != first.Expires.Unix() {
		t.Errorf("the reissued key claims %d seat(s) to %d, want 5 to %d", claims.Seats, claims.Expires, first.Expires.Unix())
	}
}

// TestCancellationStopsReissue: §5.3 — the key the customer holds keeps working
// until it expires (there is no revocation, §9.2) and the renewal does not come.
func TestCancellationStopsReissue(t *testing.T) {
	f := newFixture(t)
	first := f.purchase("evt-1", "acme.example", 5, 365)
	held := f.mintedKeys()[0]
	f.advance(day)
	f.event(Event{ID: "evt-2", Type: EventCancellation, Organisation: "acme.example"})

	f.advance(365*day - 31*day)
	if _, renewed, err := f.svc.Renew(context.Background(), "sub-acme.example"); err != nil || renewed {
		t.Errorf("a cancelled subscription renewed (renewed=%v, err=%v), want nothing", renewed, err)
	}
	if got := len(f.records()); got != 1 {
		t.Errorf("the record holds %d key(s), want the one the customer holds", got)
	}
	if claims := f.claims(held); claims.Expires != first.Expires.Unix() {
		t.Errorf("the held key reads expires=%d, want %d: a cancellation withdraws nothing", claims.Expires, first.Expires.Unix())
	}
}

// TestTestModeEventsMintStagingKeysOnly: §5.4 and Check 179 — a merchant event
// in test mode mints a staging key and nothing else, and the key id is what
// makes a production verifier refuse it rather than half-accept it (§2).
func TestTestModeEventsMintStagingKeysOnly(t *testing.T) {
	staging := newFixtureIn(t, Staging)
	staging.purchase("evt-1", "acme.example", 5, 365)
	key := staging.mintedKeys()[0]
	if !strings.HasPrefix(key, stagingKeyID+".") {
		t.Errorf("a test-mode purchase minted a key under %q, want the staging id %q", key[:strings.IndexByte(key, '.')], stagingKeyID)
	}
	if _, _, err := licence.VerifyAgainst([]byte(key), staging.now, licence.Keyring{productionKeyID: staging.ring[productionKeyID]}); !errors.Is(err, licence.ErrUnknownKeyID) {
		t.Errorf("a production keyring read a staging key as %v, want ErrUnknownKeyID", err)
	}
	if _, state, err := licence.VerifyAgainst([]byte(key), staging.now, staging.ring); err != nil || state != licence.Valid {
		t.Errorf("the staging ring reads its own key as %v, %v", state, err)
	}

	// The production intake, and the same event: nothing is minted.
	production := newFixture(t)
	production.event(Event{ID: "evt-1", Type: EventPurchase, Mode: Test, Organisation: "acme.example", Seats: 5, TermDays: 365})
	if got := production.signer.calls; got != 0 {
		t.Errorf("a production intake signed %d key(s) for a test-mode event, want none", got)
	}
	// And the staging intake refuses a live one, so the pair is one pair.
	staging.event(Event{ID: "evt-2", Type: EventPurchase, Mode: Live, Organisation: "other.example", Seats: 5, TermDays: 365})
	if got := staging.signer.calls; got != 1 {
		t.Errorf("a staging intake signed %d key(s) in all, want the test-mode purchase's one", got)
	}
}
