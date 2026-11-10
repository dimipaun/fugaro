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
		Email:        buyerAddress("acme.example"),
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
		Email:        buyerAddress("acme.example"),
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
		Email:        buyerAddress("acme.example"),
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

// TestAnEventTiedWithTheStoredInstantIsNotDropped: §5.2 excludes an event
// **older** than what the store holds and nothing else. A tie is not a replay:
// a merchant's instants come at second resolution, so a purchase and the
// cancellation that follows it can carry the same one, and so can a redelivery
// of an event the store has already written the instant of. Dropping a tied
// event loses it for good — nothing is stored for it and the drop happens before
// the event id is recorded, so its redelivery is dropped the same way — and a
// cancellation lost that way lets the renewal mint a paid key for a customer who
// has left.
func TestAnEventTiedWithTheStoredInstantIsNotDropped(t *testing.T) {
	f := newFixture(t)
	f.purchase("evt-1", "acme.example", 5, 365)
	// The cancellation carries the instant the purchase does: a merchant's
	// instants are whole seconds, so an event that follows one inside the same
	// second carries the same one.
	f.event(Event{ID: "evt-2", Type: EventCancellation, Organisation: "acme.example", OccurredAt: f.now})

	sub, held, err := f.store.Subscription(context.Background(), "sub-acme.example")
	if err != nil || !held {
		t.Fatalf("the subscription is %v, held=%v", err, held)
	}
	if !sub.Cancelled {
		t.Error("a cancellation tied with the instant the subscription holds was dropped, want it applied: what does not move the store backwards is an event older than it")
	}
	// And the renewal it would have let through does not come.
	f.advance(365*day - 30*day)
	if _, renewed, err := f.svc.Renew(context.Background(), "sub-acme.example"); err != nil || renewed {
		t.Errorf("a subscription cancelled at the instant it holds renewed (renewed=%v, err=%v), want nothing", renewed, err)
	}

	// The same tie at the repair: the state a failed write left is written from
	// the record on the redelivery, and the redelivery carries the instant the
	// subscription already holds.
	g, store := newFailingStore(t)
	g.purchase("evt-1", "acme.example", 3, 365)
	store.refuse = 1
	change := Event{
		ID: "evt-2", Type: EventSeatChange, OccurredAt: g.now,
		Subscription: "sub-acme.example", Organisation: "acme.example", Seats: 5, TermDays: 365,
	}
	if rec := g.event(change); rec.Code != http.StatusInternalServerError {
		t.Fatalf("a seat change whose subscription write failed was answered %d, want 500", rec.Code)
	}
	g.event(change)
	repaired, _, _ := g.store.Subscription(context.Background(), "sub-acme.example")
	if repaired.Seats != 5 {
		t.Errorf("the redelivered seat change left %d seat(s), want the 5 its record holds: a redelivery tied with the stored instant is still the redelivery the repair waits for", repaired.Seats)
	}
}

// TestAnEventForAnUnknownSubscriptionIsRefused: a cancellation or a seat change
// the store has no subscription for is an event the service cannot place — it
// arrived before the purchase it belongs to. Answering 200 drops it for good,
// which is the reproduced failure: the purchase that follows is saved
// uncancelled and the renewal mints a paid key for a customer who has left. The
// merchant is told to send it again, and the retry is what applies it.
func TestAnEventForAnUnknownSubscriptionIsRefused(t *testing.T) {
	f := newFixture(t)
	// The cancellation happens first and is delivered first; the purchase it
	// belongs to happened before it and arrives after it, which is the ordinary
	// out-of-order pair (the two are ordered by their own instants, §5.2).
	bought := f.now
	cancelled := bought.Add(time.Minute)
	for _, e := range []Event{
		{ID: "evt-1", Type: EventCancellation, Organisation: "acme.example", OccurredAt: cancelled},
		{ID: "evt-2", Type: EventSeatChange, Organisation: "acme.example", Seats: 9, OccurredAt: cancelled},
	} {
		if rec := f.event(e); rec.Code != http.StatusInternalServerError {
			t.Errorf("a %s for a subscription the store does not hold was answered %d, want 500: the merchant's retry is the only thing that can apply it", e.Type, rec.Code)
		}
	}
	if got := f.signer.calls; got != 0 {
		t.Errorf("an event for a subscription the store does not hold signed %d key(s), want none", got)
	}
	if _, held, _ := f.store.Subscription(context.Background(), "sub-acme.example"); held {
		t.Error("an event for a subscription the store does not hold stored one, want nothing written")
	}

	// The purchase, which is the one type that creates the subscription and so
	// the one that needs nothing stored to apply.
	f.deliver(Event{
		ID:           "evt-3",
		Type:         EventPurchase,
		Mode:         ModeFor(f.env),
		OccurredAt:   bought,
		Subscription: "sub-acme.example",
		Organisation: "acme.example",
		Email:        buyerAddress("acme.example"),
		Seats:        5,
		TermDays:     365,
	})
	if got := len(f.records()); got != 1 {
		t.Fatalf("the purchase recorded %d key(s), want the buyer's one", got)
	}

	// The redelivery of the cancellation, carrying the instant it always carried.
	if rec := f.event(Event{ID: "evt-1", Type: EventCancellation, Organisation: "acme.example", OccurredAt: cancelled}); rec.Code != http.StatusOK {
		t.Fatalf("the redelivered cancellation was answered %d, want 200: the subscription it belongs to is stored now", rec.Code)
	}
	sub, held, err := f.store.Subscription(context.Background(), "sub-acme.example")
	if err != nil || !held {
		t.Fatalf("the subscription is %v, held=%v, want the purchase's own", err, held)
	}
	if !sub.Cancelled {
		t.Error("the redelivered cancellation did not stop the reissue, want it applied: the event answered with a failure is the one the retry lands")
	}

	// And the renewal a stored but uncancelled subscription would have allowed
	// does not happen.
	f.advance(365*day - 31*day)
	if _, renewed, err := f.svc.Renew(context.Background(), "sub-acme.example"); err != nil || renewed {
		t.Errorf("a cancelled subscription renewed (renewed=%v, err=%v), want nothing: a cancellation stops the reissue", renewed, err)
	}
}

// TestAnEventWhosePurchaseWasNotSavedIsRefused is the same state reached the
// other way: the purchase minted and mailed the key, the store refused the
// subscription write, and the customer's cancellation arrives before the
// merchant's retry of the purchase. It has to be a failure too — the shape has
// no subscription to move yet — and the retry of the purchase is what finishes
// the state the cancellation then lands on.
func TestAnEventWhosePurchaseWasNotSavedIsRefused(t *testing.T) {
	f, store := newFailingStore(t)
	bought := f.now
	cancelled := bought.Add(time.Minute)
	purchase := Event{
		ID:           "evt-1",
		Type:         EventPurchase,
		Mode:         ModeFor(f.env),
		OccurredAt:   bought,
		Subscription: "sub-acme.example",
		Organisation: "acme.example",
		Email:        buyerAddress("acme.example"),
		Seats:        5,
		TermDays:     365,
	}
	store.refuse = 1
	if rec := f.deliver(purchase); rec.Code != http.StatusInternalServerError {
		t.Fatalf("a purchase whose subscription write failed was answered %d, want 500", rec.Code)
	}
	if got := len(f.records()); got != 1 {
		t.Fatalf("the failed purchase recorded %d key(s), want the one the customer holds", got)
	}

	if rec := f.event(Event{ID: "evt-2", Type: EventCancellation, Organisation: "acme.example", OccurredAt: cancelled}); rec.Code != http.StatusInternalServerError {
		t.Errorf("a cancellation whose purchase was not saved was answered %d, want 500: dropped here it is lost, and the retried purchase would save an uncancelled subscription", rec.Code)
	}

	// The merchant's retry of the purchase finishes the state off the record, and
	// the cancellation then applies.
	f.deliver(purchase)
	if _, held, _ := f.store.Subscription(context.Background(), "sub-acme.example"); !held {
		t.Fatal("the redelivered purchase left no subscription, want the state the record describes")
	}
	if rec := f.event(Event{ID: "evt-2", Type: EventCancellation, Organisation: "acme.example", OccurredAt: cancelled}); rec.Code != http.StatusOK {
		t.Fatalf("the retried cancellation was answered %d, want 200", rec.Code)
	}
	sub, _, err := f.store.Subscription(context.Background(), "sub-acme.example")
	if err != nil {
		t.Fatalf("reading the subscription: %v", err)
	}
	if !sub.Cancelled {
		t.Error("the retried cancellation did not stop the reissue, want the subscription cancelled")
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

// TestTheRenewalWhoseTermWasNotSavedIsFinished: the renewal records its source
// before it saves the term, so a store that refuses the save leaves a key in the
// customer's mailbox and a subscription still ending where it did — and since
// the source names the term it covers, every later trigger finds it recorded and
// would stop there. The subscription would then never renew again, with a live
// key in a mailbox as the only sign. The retry finishes the term off the record.
func TestTheRenewalWhoseTermWasNotSavedIsFinished(t *testing.T) {
	f, store := newFailingStore(t)
	first := f.purchase("evt-1", "acme.example", 5, 365)

	f.advance(365*day - 30*day)
	store.refuse = 1
	if _, renewed, err := f.svc.Renew(context.Background(), "sub-acme.example"); err == nil || renewed {
		t.Fatalf("a renewal whose term could not be saved answered (renewed=%v, err=%v), want the failure: the trigger has to retry", renewed, err)
	}
	if got := len(f.records()); got != 2 {
		t.Fatalf("the failed renewal recorded %d key(s), want the purchase's and the renewal's", got)
	}
	record := f.records()[1]
	if got := len(f.mail.sent); got != 2 {
		t.Fatalf("the failed renewal mailed %d message(s), want the key it minted too: the key is in the customer's mailbox whatever the store did", got)
	}
	unmoved, _, _ := f.store.Subscription(context.Background(), "sub-acme.example")
	if !unmoved.Expires.Equal(first.Expires) {
		t.Fatalf("the failed renewal moved the term's end to %s, want the purchase's %s", unmoved.Expires, first.Expires)
	}

	// The trigger fires again, which is what the merchant's retry or the next
	// scheduled one does.
	if _, renewed, err := f.svc.Renew(context.Background(), "sub-acme.example"); err != nil || renewed {
		t.Fatalf("the retried renewal answered (renewed=%v, err=%v), want neither: the key was already minted and mailed", renewed, err)
	}
	sub, _, _ := f.store.Subscription(context.Background(), "sub-acme.example")
	if !sub.Expires.Equal(record.Expires) {
		t.Errorf("the retry left the term ending %s, want the end of the term the renewal key covers (%s): the subscription would otherwise never renew again", sub.Expires, record.Expires)
	}
	if !sub.At.Equal(record.Issued) {
		t.Errorf("the retry left the subscription's instant at %s, want the renewal's own (%s)", sub.At, record.Issued)
	}
	if got := len(f.mail.sent); got != 2 {
		t.Errorf("the retry mailed %d message(s) in all, want the renewal's one alone: the key is delivered once", got)
	}
	if got := f.signer.calls; got != 2 {
		t.Errorf("the retry signed %d key(s) in all, want the purchase's and the renewal's", got)
	}

	// And the repaired subscription renews again when its own term ends, which is
	// the whole of what the failed save had cost: the renewal was issued 30 days
	// into a 395-day key, so the next window opens 365 days after it.
	f.advance(365 * day)
	if _, renewed, err := f.svc.Renew(context.Background(), "sub-acme.example"); err != nil || !renewed {
		t.Errorf("the next term's renewal answered (renewed=%v, err=%v), want the key after the repaired one", renewed, err)
	}
}

// TestAnEventWithNoInstantIsRefused: §5.2 orders the subscription's state by the
// event's own instant, so an event without one cannot be placed in it. Stamping
// it with the service's clock is the trap: the stamp is *now*, so every
// redelivery of that event outranks the state it must not move — a replayed
// purchase rewrites a cancelled subscription, and the renewal that follows mints
// a paid key for a customer who has left.
func TestAnEventWithNoInstantIsRefused(t *testing.T) {
	f := newFixture(t)
	f.purchase("evt-1", "acme.example", 5, 365)
	f.advance(day)
	f.event(Event{ID: "evt-2", Type: EventCancellation, Organisation: "acme.example"})
	signed, mailed := f.signer.calls, len(f.mail.sent)

	rec := f.deliver(Event{
		ID:           "evt-3",
		Type:         EventPurchase,
		Mode:         ModeFor(f.env),
		Subscription: "sub-acme.example",
		Organisation: "acme.example",
		Email:        buyerAddress("acme.example"),
		Seats:        9,
		TermDays:     365,
	})
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("a purchase carrying no instant was answered %d, want 500: the deploy's mapping is what has to say when an event happened", rec.Code)
	}
	if got := f.signer.calls; got != signed {
		t.Errorf("a purchase carrying no instant signed %d key(s) in all, want the purchase's one", got)
	}
	if got := len(f.mail.sent); got != mailed {
		t.Errorf("a purchase carrying no instant mailed %d message(s) in all, want none of its own", got)
	}
	sub, _, err := f.store.Subscription(context.Background(), "sub-acme.example")
	if err != nil {
		t.Fatalf("reading the subscription: %v", err)
	}
	if !sub.Cancelled {
		t.Error("a purchase carrying no instant resurrected a cancelled subscription, want the cancellation to stand")
	}
	if sub.Seats != 5 {
		t.Errorf("the subscription holds %d seat(s), want the 5 the customer bought", sub.Seats)
	}

	// And the renewal a resurrected purchase would have allowed does not happen.
	f.advance(365*day - 31*day)
	if _, renewed, err := f.svc.Renew(context.Background(), "sub-acme.example"); err != nil || renewed {
		t.Errorf("a cancelled subscription renewed (renewed=%v, err=%v), want nothing: the purchase that would have undone the cancellation was refused", renewed, err)
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
