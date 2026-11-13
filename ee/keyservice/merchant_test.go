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
	"log"
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
		WorkspaceID:  workspaceID,
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
		WorkspaceID:  workspaceID,
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
		WorkspaceID:  workspaceID,
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

// TestARedeliveredPurchaseDoesNotResurrectACancelledSubscription: the repair a
// recorded purchase gets from `completed` writes the term the record describes —
// the seats, the term and the address — and it must write it *onto* what the
// store already holds and not into a fresh subscription: a literal drops
// everything the subscription has become since, and the cancellation that landed
// on the purchase's own instant is admitted by the guard above, which excludes an
// event older than the state and not a tie. The renewal that follows then mints a
// paid key for a customer who has left (§5.2, §5.3).
func TestARedeliveredPurchaseDoesNotResurrectACancelledSubscription(t *testing.T) {
	f := newFixture(t)
	purchase := Event{
		ID:           "evt-1",
		Type:         EventPurchase,
		Mode:         ModeFor(f.env),
		OccurredAt:   f.now,
		Subscription: "sub-acme.example",
		Organisation: "acme.example",
		WorkspaceID:  workspaceID,
		Email:        buyerAddress("acme.example"),
		Seats:        5,
		TermDays:     365,
	}
	f.deliver(purchase)
	// The cancellation carries the purchase's own instant, so the redelivery
	// below is tied with the state it finds and not older than it.
	f.event(Event{ID: "evt-2", Type: EventCancellation, Organisation: "acme.example", OccurredAt: f.now})
	signed, mailed := f.signer.calls, len(f.mail.sent)

	if rec := f.deliver(purchase); rec.Code != http.StatusOK {
		t.Fatalf("the redelivered purchase was answered %d, want 200: it is an event the service has already minted for", rec.Code)
	}
	sub, held, err := f.store.Subscription(context.Background(), "sub-acme.example")
	if err != nil || !held {
		t.Fatalf("the subscription is %v, held=%v", err, held)
	}
	if !sub.Cancelled {
		t.Error("a redelivered purchase resurrected a cancelled subscription, want the cancellation to stand: a redelivery is not a second purchase")
	}
	if sub.Seats != 5 || sub.Email != buyerAddress("acme.example") {
		t.Errorf("the redelivery left %+v, want the record's seats and address written onto the subscription the store holds", sub)
	}
	if got := f.signer.calls; got != signed {
		t.Errorf("the redelivery signed %d key(s) in all, want the purchase's one", got)
	}
	if got := len(f.mail.sent); got != mailed {
		t.Errorf("the redelivery mailed %d message(s) in all, want the purchase's one", got)
	}

	// And the renewal a resurrected subscription would have allowed does not
	// happen.
	f.advance(365*day - 30*day)
	if _, renewed, err := f.svc.Renew(context.Background(), "sub-acme.example"); err != nil || renewed {
		t.Errorf("a subscription cancelled at the instant it holds renewed (renewed=%v, err=%v), want nothing", renewed, err)
	}
}

// TestAnEventThatDoesNotSayWhatToMintIsRefused: the paid path mints the claims
// the merchant's event names and covers the term it names, so an event that
// names none of them is one this service cannot serve — and it is refused, the
// way an event with no instant and an event with no address are, rather than
// dropped. A drop is silent in every direction at once: nothing recorded,
// nothing mailed, nothing queued, nothing logged, and a `200` that tells the
// merchant never to send it again. A customer who has paid receives nothing and
// nobody can see it (§5.1).
func TestAnEventThatDoesNotSayWhatToMintIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name  string
		event Event
	}{
		{name: "a purchase for no organisation", event: Event{ID: "evt-1", Type: EventPurchase, WorkspaceID: workspaceID, Seats: 5, TermDays: 365}},
		{name: "a purchase for no workspace", event: Event{ID: "evt-1", Type: EventPurchase, Organisation: "acme.example", Seats: 5, TermDays: 365}},
		{name: "a purchase for no seats", event: Event{ID: "evt-1", Type: EventPurchase, Organisation: "acme.example", WorkspaceID: workspaceID, TermDays: 365}},
		{name: "a purchase for a negative number of seats", event: Event{ID: "evt-1", Type: EventPurchase, Organisation: "acme.example", WorkspaceID: workspaceID, Seats: -5, TermDays: 365}},
		{name: "a purchase for no term", event: Event{ID: "evt-1", Type: EventPurchase, Organisation: "acme.example", WorkspaceID: workspaceID, Seats: 5}},
		{name: "a purchase for a negative term", event: Event{ID: "evt-1", Type: EventPurchase, Organisation: "acme.example", WorkspaceID: workspaceID, Seats: 5, TermDays: -30}},
		{name: "a purchase for a term the checkout does not sell", event: Event{ID: "evt-1", Type: EventPurchase, Organisation: "acme.example", WorkspaceID: workspaceID, Seats: 5, TermDays: 7}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			e := tc.event
			e.Mode, e.Subscription, e.OccurredAt = ModeFor(f.env), "sub-acme.example", f.now
			// The address is one the intake can mail to, so the refusal is the
			// claims' and not the delivery's.
			e.Email = buyerAddress("acme.example")
			if rec := f.deliver(e); rec.Code != http.StatusInternalServerError {
				t.Errorf("%s was answered %d, want 500: the merchant's retry and the owner's alerting are what see an event this service cannot serve", tc.name, rec.Code)
			}
			if got := f.signer.calls; got != 0 {
				t.Errorf("%s signed %d key(s), want none: nothing is minted for an event the service cannot serve", tc.name, got)
			}
			if got := len(f.records()); got != 0 {
				t.Errorf("%s recorded %d key(s), want none", tc.name, got)
			}
			if got := len(f.mail.sent); got != 0 {
				t.Errorf("%s mailed %d message(s), want none", tc.name, got)
			}
			if _, held, _ := f.store.Subscription(context.Background(), "sub-acme.example"); held {
				t.Errorf("%s stored a subscription, want nothing written", tc.name)
			}
		})
	}

	// The same refusal on the reissue path, where the subscription is held and
	// the event names no seats: the seats the customer holds are the ones they
	// keep, and the reissue does not happen.
	f := newFixture(t)
	f.purchase("evt-1", "acme.example", 5, 365)
	signed, mailed := f.signer.calls, len(f.mail.sent)
	if rec := f.event(Event{ID: "evt-2", Type: EventSeatChange, Organisation: "acme.example"}); rec.Code != http.StatusInternalServerError {
		t.Errorf("a seat change naming no seats was answered %d, want 500", rec.Code)
	}
	sub, _, _ := f.store.Subscription(context.Background(), "sub-acme.example")
	if sub.Seats != 5 {
		t.Errorf("a seat change naming no seats left the subscription holding %d seat(s), want the 5 the customer bought", sub.Seats)
	}
	if got := f.signer.calls; got != signed {
		t.Errorf("a seat change naming no seats signed %d key(s) in all, want the purchase's one", got)
	}
	if got := len(f.mail.sent); got != mailed {
		t.Errorf("a seat change naming no seats mailed %d message(s) in all, want the purchase's one", got)
	}
}

// TestASeatChangeThatNamesNoWorkspaceIsRefused: TN2 and §5.1 — the workspace
// binding is a claim every v1 key carries, and the seat change's own event is
// where its binding comes from, so an event that names none is one this service
// cannot serve. It is refused and not dropped: a customer who has paid for a
// seat change receives nothing if it is dropped quietly, and the 500 is what
// puts it in front of the merchant's retry and the deploy's alerting. Nothing is
// minted, so no key is mailed that every verifier would refuse, and the seats
// the customer holds are the ones they keep.
//
// The reason is asserted, and not only the refusals, because the refusals alone
// do not hold this guard: the minter refuses an empty workspace id itself, so a
// seat change that got past this line would answer the same 500 with nothing
// signed, nothing mailed and nothing recorded, and only the sentence the service
// logged would differ. That sentence is this guard's — it names the seat change
// and what it could not serve — and it is the whole of what a deploy has to go
// on when a mapping stops carrying the binding (S3.7).
func TestASeatChangeThatNamesNoWorkspaceIsRefused(t *testing.T) {
	f := newFixture(t)
	f.purchase("evt-1", "acme.example", 5, 365)
	signed, mailed, held := f.signer.calls, len(f.mail.sent), len(f.records())

	var logged strings.Builder
	previous := log.Writer()
	log.SetOutput(&logged)
	defer log.SetOutput(previous)

	f.advance(day)
	rec := f.deliver(Event{
		ID:           "evt-2",
		Type:         EventSeatChange,
		Mode:         ModeFor(f.env),
		OccurredAt:   f.now,
		Subscription: "sub-acme.example",
		Organisation: "acme.example",
		Email:        buyerAddress("acme.example"),
		Seats:        9,
		TermDays:     365,
	})
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("a seat change naming no workspace was answered %d, want 500: the merchant's retry and the owner's alerting are what see an event this service cannot serve", rec.Code)
	}
	if got := f.signer.calls; got != signed {
		t.Errorf("a seat change naming no workspace signed %d key(s) in all, want the purchase's one: a key minted without the binding TN2 requires is one every verifier refuses", got)
	}
	if got := len(f.mail.sent); got != mailed {
		t.Errorf("a seat change naming no workspace mailed %d message(s) in all, want the purchase's one", got)
	}
	if got := len(f.records()); got != held {
		t.Errorf("a seat change naming no workspace recorded %d key(s) in all, want the purchase's one", got)
	}
	sub, stored, err := f.store.Subscription(context.Background(), "sub-acme.example")
	if err != nil || !stored {
		t.Fatalf("reading the subscription: held=%v, err=%v", stored, err)
	}
	if sub.Seats != 5 {
		t.Errorf("a seat change naming no workspace left the subscription holding %d seat(s), want the 5 the customer bought", sub.Seats)
	}

	line := logged.String()
	for _, want := range []string{ErrNoClaims.Error(), "the seat change evt-2"} {
		if !strings.Contains(line, want) {
			t.Errorf("the refusal was logged as %q, want it to name %q: the guard is the only place the seat change's own reason is written, and the minter's own refusal would read as something else", line, want)
		}
	}
}

// lenientMints is the minter as the intake really reaches it (Check 174): the
// minter's own service, another process, which mints the request it is handed
// and does not re-check the claims the intake assembled. It fills an empty
// workspace id instead of refusing it, so a request the service should not have
// made is one this minter answers with a signed key — which is what makes the
// service's own clause the only thing standing between a purchase that names no
// workspace and a key every verifier refuses.
type lenientMints struct {
	Mints
	innerV1 MintsV1
}

func (l lenientMints) MintV1(ctx context.Context, req RequestV1) (MintedV1, error) {
	if req.WorkspaceID == "" {
		req.WorkspaceID = workspaceID
	}
	return l.innerV1.MintV1(ctx, req)
}

// TestAPurchaseThatNamesNoWorkspaceIsRefused: TN2, D2134 and §5.1 — the
// workspace binding scopes a cascade project's model access, so a key minted
// with none is one every verifier that reads the claim refuses. The purchase's
// own event is where the binding comes from, so a purchase that names none is
// refused before anything is minted, mailed or recorded, and the 500 is what
// puts it in front of the merchant's retry and the deploy's alerting.
//
// The minter is the one the assertions are about: behind the intake's call it is
// another process, and a minter that answers what it is handed — the fixture's
// own refuses an empty workspace id, so it cannot tell this guard from the
// minter's — mints this purchase happily. Deleting `e.WorkspaceID == ""` from
// `purchased` therefore fails this test on the signature, the mail and the
// record rather than passing on the minter's refusal.
func TestAPurchaseThatNamesNoWorkspaceIsRefused(t *testing.T) {
	f := newFixture(t)
	f.deps.Mints = lenientMints{Mints: f.deps.Mints, innerV1: f.deps.Mints.(MintsV1)}
	f.start()

	rec := f.deliver(Event{
		ID:           "evt-1",
		Type:         EventPurchase,
		Mode:         ModeFor(f.env),
		OccurredAt:   f.now,
		Subscription: "sub-acme.example",
		Organisation: "acme.example",
		Email:        buyerAddress("acme.example"),
		Seats:        5,
		TermDays:     365,
	})
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("a purchase naming no workspace was answered %d, want 500: the merchant's retry and the owner's alerting are what see an event this service cannot serve", rec.Code)
	}
	if got := f.signer.calls; got != 0 {
		t.Errorf("a purchase naming no workspace signed %d key(s), want none: the key would carry no binding, and every verifier that reads one refuses it (TN2, D2134)", got)
	}
	if got := len(f.records()); got != 0 {
		t.Errorf("a purchase naming no workspace recorded %d key(s), want none", got)
	}
	if got := len(f.mail.sent); got != 0 {
		t.Errorf("a purchase naming no workspace mailed %d message(s), want none", got)
	}
	if _, held, _ := f.store.Subscription(context.Background(), "sub-acme.example"); held {
		t.Errorf("a purchase naming no workspace stored a subscription, want nothing written")
	}
}

// olderMints is the intake wired with the older format's route and nothing else:
// one Mints, as a deployment whose client never grew the v1 route hands the paid
// path.
type olderMints struct{ Mints }

// TestAPurchaseThroughAMinterThatCannotAnswerV1IsRefused: what a customer buys
// is a v1 key — the format whose `key_id` a §8 revocation names and whose claims
// carry the policy (D2130) — so an intake whose minter answers only the older
// route is refused rather than handed a key no released verifier reads, and no
// key is minted, mailed or recorded on the way.
func TestAPurchaseThroughAMinterThatCannotAnswerV1IsRefused(t *testing.T) {
	f := newFixture(t)
	f.deps.Mints = olderMints{Mints: f.deps.Mints}
	f.start()

	if rec := f.event(Event{ID: "evt-1", Type: EventPurchase, Organisation: "acme.example", Seats: 5, TermDays: 365}); rec.Code != http.StatusInternalServerError {
		t.Errorf("a purchase through a minter that answers no v1 request was answered %d, want 500: the merchant's retry and the owner's alerting are what see a deployment wired wrong", rec.Code)
	}
	if got := f.signer.calls; got != 0 {
		t.Errorf("a purchase through a minter that answers no v1 request signed %d key(s), want none: the older format's key carries no key_id for a revocation to name", got)
	}
	if got := len(f.records()); got != 0 {
		t.Errorf("a purchase through a minter that answers no v1 request recorded %d key(s), want none", got)
	}
	if got := len(f.mail.sent); got != 0 {
		t.Errorf("a purchase through a minter that answers no v1 request mailed %d message(s), want none", got)
	}
	if _, held, _ := f.store.Subscription(context.Background(), "sub-acme.example"); held {
		t.Errorf("a purchase through a minter that answers no v1 request stored a subscription, want nothing written")
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
		WorkspaceID:  workspaceID,
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
		WorkspaceID:  workspaceID,
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

// TestAPurchaseWhoseSubscriptionWasNotSavedKeepsItsWorkspace: the repair a
// redelivered purchase gets from `completed` writes the state the record
// describes, and the workspace binding a v1 key carries is **not** in that
// record — the event is the only place it arrives (TN2, D2134). A repair that
// left it behind would strand the customer for good: Renew refuses a
// subscription whose record names no workspace with ErrNoClaims, no later event
// carries the binding again, and the term they paid for could never be minted
// (§5.1, §5.3).
func TestAPurchaseWhoseSubscriptionWasNotSavedKeepsItsWorkspace(t *testing.T) {
	f, store := newFailingStore(t)
	purchase := Event{
		ID:           "evt-1",
		Type:         EventPurchase,
		Mode:         ModeFor(f.env),
		OccurredAt:   f.now,
		Subscription: "sub-acme.example",
		Organisation: "acme.example",
		WorkspaceID:  workspaceID,
		Email:        buyerAddress("acme.example"),
		Seats:        5,
		TermDays:     365,
	}
	store.refuse = 1
	if rec := f.deliver(purchase); rec.Code != http.StatusInternalServerError {
		t.Fatalf("a purchase whose subscription write failed was answered %d, want 500", rec.Code)
	}
	first := f.records()[0]
	if _, held, _ := f.store.Subscription(context.Background(), "sub-acme.example"); held {
		t.Fatal("the purchase the store refused saved a subscription, want the failure this test is about")
	}

	// The merchant's redelivery finishes the state off the record.
	f.deliver(purchase)
	sub, held, err := f.store.Subscription(context.Background(), "sub-acme.example")
	if err != nil || !held {
		t.Fatalf("the redelivered purchase left the subscription (held=%v, err=%v), want the state the record describes", held, err)
	}
	if sub.WorkspaceID != workspaceID {
		t.Errorf("the repaired subscription names the workspace %q, want the %q its purchase carried (TN2)", sub.WorkspaceID, workspaceID)
	}

	f.advance(365*day - 30*day)
	minted, renewed, err := f.svc.Renew(context.Background(), "sub-acme.example")
	if err != nil || !renewed {
		t.Fatalf("the renewal of the repaired subscription answered (renewed=%v, err=%v), want the next key: the customer paid for the term", renewed, err)
	}
	claims := v1Verified(t, f, minted.Key)
	if claims.WorkspaceID != workspaceID || claims.KeyID == first.KeyID {
		t.Errorf("the renewal of the repaired subscription claims workspace %q and key id %q, want the %q its purchase bound and a key of its own", claims.WorkspaceID, claims.KeyID, workspaceID)
	}
}

// failingIssuances is the store as a deployment that cannot write an issuance,
// which on this path is the one write between the mail and the record: what the
// failure leaves is a key in the customer's mailbox that the store has nothing
// about, and it is a live key in the older format's order and not a harmless
// duplicate in v1's, because MintV1 makes a fresh key_id every call.
type failingIssuances struct {
	*Memory
	refuse     int
	duplicated int
	// committed is how many writes land in the store and are then reported as
	// failed, which is what a store that times out on the way back — or that
	// answers ErrAlreadyRecorded from an internal retry after its own write —
	// does: the row is there and the caller is told it is not.
	committed int
}

func (f *failingIssuances) SaveIssuance(ctx context.Context, r Record) error {
	if f.committed > 0 {
		f.committed--
		if err := f.Memory.SaveIssuance(ctx, r); err != nil {
			return err
		}
		return errors.New("the store wrote the record and then reported the write as failed")
	}
	if f.duplicated > 0 {
		f.duplicated--
		return ErrAlreadyRecorded
	}
	if f.refuse > 0 {
		f.refuse--
		return errors.New("the store refused the write")
	}
	return f.Memory.SaveIssuance(ctx, r)
}

// mailedBundle is the pair a paid mail carries: the key and, on the line below
// it, the renewal secret (D2135).
func mailedBundle(t *testing.T, body string) (key, secret string) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "fgk_") {
			if i+1 >= len(lines) || strings.TrimSpace(lines[i+1]) == "" {
				t.Fatalf("the mail ends on the key line, want the renewal secret below it (D2135): %q", body)
			}
			return strings.TrimSpace(line), strings.TrimSpace(lines[i+1])
		}
	}
	t.Fatalf("the mail carries no key line, want the key text itself: %q", body)
	return "", ""
}

func newFailingIssuances(t *testing.T) (*fixture, *failingIssuances) {
	t.Helper()
	f := newFixtureWith(t, Production, func(f *fixture) {
		f.store = &Memory{}
		f.deps.Store = &failingIssuances{Memory: f.store}
	})
	return f, f.deps.Store.(*failingIssuances)
}

// TestAKeyMintedButNotRecordedIsWithdrawn: the paid path mails before it
// records, which the older format could afford and v1 cannot — a v1 mint makes
// a fresh key_id and a fresh renewal secret every call, so a key that is mailed
// and then not recorded is a second live key and not the same key twice. Left
// live it is one nothing can renew, no refund can name and no operator can see
// (K2.2, §8), so the failed write withdraws it before it answers.
func TestAKeyMintedButNotRecordedIsWithdrawn(t *testing.T) {
	f, store := newFailingIssuances(t)
	purchase := Event{
		ID:           "evt-1",
		Type:         EventPurchase,
		Mode:         ModeFor(f.env),
		OccurredAt:   f.now,
		Subscription: "sub-acme.example",
		Organisation: "acme.example",
		WorkspaceID:  workspaceID,
		Email:        buyerAddress("acme.example"),
		Seats:        5,
		TermDays:     365,
	}
	store.refuse = 1
	if rec := f.deliver(purchase); rec.Code != http.StatusInternalServerError {
		t.Fatalf("a purchase whose issuance write failed was answered %d, want 500", rec.Code)
	}
	if got := len(f.mail.sent); got != 1 {
		t.Fatalf("the failed purchase mailed %d message(s), want the one it minted: the mail is what makes the key live", got)
	}
	if got := len(f.records()); got != 0 {
		t.Fatalf("the failed purchase recorded %d key(s), want none", got)
	}
	orphanKey, _ := mailedBundle(t, f.mail.sent[0].Body)
	orphan := v1Verified(t, f, orphanKey)
	entry, held, err := f.store.Revocation(context.Background(), orphan.KeyID)
	if err != nil || !held {
		t.Fatalf("the unrecorded key %s is not in §8's list (held=%v, err=%v), want it withdrawn: nothing else names it", orphan.KeyID, held, err)
	}
	if entry.Reason != ReasonUnrecorded || !entry.At.Equal(f.now) {
		t.Errorf("the withdrawn key's entry is %+v, want %q at the service's own clock (%s)", entry, ReasonUnrecorded, f.now)
	}
	if _, held, _ := f.store.KeyState(context.Background(), orphan.KeyID); held {
		t.Error("the unrecorded key was given a key state, want none: a key with no issuance is not one to renew")
	}
	if _, held, _ := f.store.Subscription(context.Background(), "sub-acme.example"); held {
		t.Error("the failed purchase saved a subscription, want nothing: the state is written after the key")
	}

	// The source is unspent, so the merchant's redelivery mints and records the
	// key the customer actually keeps — a different key from the withdrawn one.
	f.deliver(purchase)
	records := f.records()
	if len(records) != 1 || len(f.mail.sent) != 2 {
		t.Fatalf("the redelivery left %d record(s) and %d mail(s), want one of each more than the failure left", len(records), len(f.mail.sent))
	}
	keptKey, keptSecret := mailedBundle(t, f.mail.sent[1].Body)
	kept := v1Verified(t, f, keptKey)
	if kept.KeyID != records[0].KeyID || kept.KeyID == orphan.KeyID {
		t.Errorf("the redelivery recorded the key %q and mailed %q, want the one it minted now (%q) and not the withdrawn one (%q)", records[0].KeyID, kept.KeyID, kept.KeyID, orphan.KeyID)
	}
	state, held, err := f.store.KeyState(context.Background(), kept.KeyID)
	if err != nil || !held || state.RenewalSecretHash != secretHash(keptSecret) {
		t.Errorf("the redelivery left the key state %+v (held=%v, err=%v), want the digest of the secret it mailed", state, held, err)
	}
	if entry, held, _ := f.store.Revocation(context.Background(), orphan.KeyID); !held || entry.Reason != ReasonUnrecorded {
		t.Errorf("the withdrawn key's entry is %+v (held=%v), want it still in the list: the list only ever grows", entry, held)
	}
}

// TestTheDeliveryThatLosesTheRecordWithdrawsItsOwnKey: two deliveries of one
// event in flight at once both mint and both mail, and only one records — the
// other's write is refused (§5.2, S3.5). Its key is in a mailbox and in no
// record, so it is withdrawn rather than left as a second live licence for one
// purchase.
func TestTheDeliveryThatLosesTheRecordWithdrawsItsOwnKey(t *testing.T) {
	f, store := newFailingIssuances(t)
	purchase := Event{
		ID:           "evt-1",
		Type:         EventPurchase,
		Mode:         ModeFor(f.env),
		OccurredAt:   f.now,
		Subscription: "sub-acme.example",
		Organisation: "acme.example",
		WorkspaceID:  workspaceID,
		Email:        buyerAddress("acme.example"),
		Seats:        5,
		TermDays:     365,
	}
	store.duplicated = 1
	if rec := f.deliver(purchase); rec.Code != http.StatusInternalServerError {
		t.Fatalf("the delivery whose record was already taken was answered %d, want 500: it minted and mailed nothing the store knows about", rec.Code)
	}
	if got := len(f.records()); got != 0 {
		t.Fatalf("the losing delivery recorded %d key(s), want none: the store's answer is the state", got)
	}
	if got := len(f.mail.sent); got != 1 {
		t.Fatalf("the losing delivery mailed %d message(s), want the one its own mint produced", got)
	}
	lostKey, _ := mailedBundle(t, f.mail.sent[0].Body)
	lost := v1Verified(t, f, lostKey)
	if entry, held, _ := f.store.Revocation(context.Background(), lost.KeyID); !held || entry.Reason != ReasonUnrecorded {
		t.Errorf("the losing delivery's key %s is %+v (held=%v), want it withdrawn: the winner's key is the one live licence", lost.KeyID, entry, held)
	}
	if _, held, _ := f.store.KeyState(context.Background(), lost.KeyID); held {
		t.Error("the losing delivery wrote a key state for a key it never recorded, want none")
	}
}

// TestAStoreThatRecordsAndThenFailsKeepsTheMailedKey: the withdrawal above is
// for a key the store really has nothing about, and a store's answer is not the
// state — one that commits and then reports the failure has this key in hand. It
// is the case that turns the withdrawal into a loss: the key is in the
// customer's mailbox, the source is spent, so the redelivery repairs the
// subscription and mints nothing, and what the customer is left with is a
// withdrawn key with no renewal state — a licence they paid for and cannot use,
// with nothing the merchant can retry into existence.
func TestAStoreThatRecordsAndThenFailsKeepsTheMailedKey(t *testing.T) {
	f, store := newFailingIssuances(t)
	purchase := Event{
		ID:           "evt-1",
		Type:         EventPurchase,
		Mode:         ModeFor(f.env),
		OccurredAt:   f.now,
		Subscription: "sub-acme.example",
		Organisation: "acme.example",
		WorkspaceID:  workspaceID,
		Email:        buyerAddress("acme.example"),
		Seats:        5,
		TermDays:     365,
	}
	store.committed = 1
	if rec := f.deliver(purchase); rec.Code != http.StatusOK {
		t.Fatalf("a purchase whose store recorded the issuance and then reported the failure was answered %d, want 200: the record is there and the key is the customer's", rec.Code)
	}
	records := f.records()
	if len(records) != 1 {
		t.Fatalf("the purchase recorded %d key(s), want the one the store's write landed", len(records))
	}
	if got := len(f.mail.sent); got != 1 {
		t.Fatalf("the purchase mailed %d message(s), want the one bundle it minted", got)
	}
	key, secret := mailedBundle(t, f.mail.sent[0].Body)
	claimed := v1Verified(t, f, key)
	if claimed.KeyID != records[0].KeyID {
		t.Errorf("the mailed key is %q and the record is %q, want the key the store holds", claimed.KeyID, records[0].KeyID)
	}
	if entry, held, _ := f.store.Revocation(context.Background(), claimed.KeyID); held {
		t.Errorf("the key the store recorded was withdrawn (%+v), want it live: the store's answer is not the state", entry)
	}
	state, held, err := f.store.KeyState(context.Background(), claimed.KeyID)
	if err != nil || !held || state.RenewalSecretHash != secretHash(secret) {
		t.Errorf("the store holds the key state %+v (held=%v, err=%v), want the digest of the secret the mail carried (D2135): without it the term after this one can never be renewed", state, held, err)
	}
	if _, held, _ := f.store.Subscription(context.Background(), "sub-acme.example"); !held {
		t.Error("the answered purchase saved no subscription, want the state written: the merchant has nothing to send again")
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
	claims := v1Verified(t, f, minted.Key)
	if !claims.PaidThrough.Equal(wantExpiry) || !claims.IssuedAt.Equal(f.now) {
		t.Errorf("the renewal key claims issued=%s paid_through=%s, want %s and %s", claims.IssuedAt, claims.PaidThrough, f.now, wantExpiry)
	}
	if claims.Seats != 5 || claims.Organisation != "acme.example" {
		t.Errorf("the renewal key claims %d seat(s) for %q, want the subscription's own", claims.Seats, claims.Organisation)
	}
	if got := v1Verified(t, f, f.mintedKeys()[0]); got.Seats != 5 {
		t.Errorf("the first key claims %d seat(s), want the purchase's 5: a renewal withdraws nothing", got.Seats)
	}

	// The renewal is a v1 key like the purchase's, and that is what makes it one
	// the rest of the licence machinery can act on: its own `key_id`, which §8's
	// list and a K2.3 reissue name; the workspace the purchase bound, which every
	// verifier reads (TN2, D2134); the row its term is sold under; and the
	// renewal secret mailed beside it and kept as a digest, which is the pair the
	// term after this one is renewed with (D2135).
	if claims.KeyID == first.KeyID || !strings.HasPrefix(claims.KeyID, keyIDPrefix) {
		t.Errorf("the renewal key's key id is %q, and the purchase's is %q, want a new %q id: a reissue is a new key (§8)", claims.KeyID, first.KeyID, keyIDPrefix)
	}
	if claims.WorkspaceID != workspaceID || claims.Tier != licence.TierTeam {
		t.Errorf("the renewal key claims workspace %q and tier %q, want %q and %q", claims.WorkspaceID, claims.Tier, workspaceID, licence.TierTeam)
	}
	if minted.Record.Plan != PolicyTeamAnnual {
		t.Errorf("the renewal mints under the row %q, want the one the subscription's year is sold under (%q)", minted.Record.Plan, PolicyTeamAnnual)
	}
	if minted.RenewalSecret == "" {
		t.Error("the renewal answered with no renewal secret, want the one its own renewal needs (D2135)")
	}
	state, held, err := f.store.KeyState(context.Background(), claims.KeyID)
	if err != nil || !held {
		t.Fatalf("the store holds no key state for the renewal's key %s (held=%v, err=%v), so the term it covers could never be renewed", claims.KeyID, held, err)
	}
	if state.RenewalSecretHash != secretHash(minted.RenewalSecret) {
		t.Errorf("the store holds the digest %q for the renewal's key, want the hash of the secret it answered, %q", state.RenewalSecretHash, secretHash(minted.RenewalSecret))
	}
	sent := f.mail.sent[len(f.mail.sent)-1]
	if !strings.Contains(sent.Body, minted.Key) || !strings.Contains(sent.Body, minted.RenewalSecret) {
		t.Errorf("the renewal's mail is %q, want both lines of D2135's bundle: the key and the secret beside it", sent.Body)
	}

	if _, again, err := f.svc.Renew(context.Background(), "sub-acme.example"); err != nil || again {
		t.Errorf("a second renewal for the same term minted (renewed=%v, err=%v), want one mint per (subscription, term, seats)", again, err)
	}
}

// TestAMonthlyPurchaseIsNotRenewableAtOnce: T26's lead is 30 days and the
// monthly row the checkout sells runs 28 to 31, so a fixed lead opens the
// renewal window at — or, for February, before — the purchase itself. A trigger
// that fired then would mint a second team-monthly key covering the rest of the
// term the customer is in and the whole of the next one: one month billed, two
// months issued, and the second key is one no cancellation withdraws. The window
// is therefore inside the term it follows, in its second half, and a fresh
// monthly purchase is not renewable at all.
func TestAMonthlyPurchaseIsNotRenewableAtOnce(t *testing.T) {
	f := newFixture(t)
	first := f.purchase("evt-1", "acme.example", 5, 30)
	if first.Plan != PolicyTeamMonthly {
		t.Fatalf("a 30-day purchase was minted under the row %q, want the monthly one (%q)", first.Plan, PolicyTeamMonthly)
	}

	if minted, renewed, err := f.svc.Renew(context.Background(), "sub-acme.example"); err != nil || renewed || minted.Key != "" {
		t.Errorf("renewing a fresh monthly purchase answered (key=%q, renewed=%v, err=%v), want nothing: its term has not been reached, and a second key would be a second month issued for one billed", minted.Key, renewed, err)
	}
	if got := len(f.records()); got != 1 {
		t.Errorf("the renewal of a fresh monthly purchase left %d record(s), want the purchase's alone", got)
	}
	if got := len(f.mail.sent); got != 1 {
		t.Errorf("the renewal of a fresh monthly purchase mailed %d message(s), want the purchase's alone", got)
	}
	if sub, _, _ := f.store.Subscription(context.Background(), "sub-acme.example"); !sub.Expires.Equal(first.Expires) {
		t.Errorf("the refused renewal moved the term's end to %s, want the purchase's %s", sub.Expires, first.Expires)
	}

	// The window opens in the term's second half, and what it mints then covers
	// to the end of the term after the one the customer is in — the same overlap
	// an annual renewal has, fifteen days of it rather than thirty.
	f.advance(15 * day)
	minted, renewed, err := f.svc.Renew(context.Background(), "sub-acme.example")
	if err != nil || !renewed {
		t.Fatalf("the monthly renewal in the term's second half answered (renewed=%v, err=%v), want the next key", renewed, err)
	}
	wantExpiry := first.Expires.Add(30 * day)
	claims := v1Verified(t, f, minted.Key)
	if !claims.PaidThrough.Equal(wantExpiry) || !minted.Record.Expires.Equal(wantExpiry) {
		t.Errorf("the monthly renewal covers to %s, want the end of the term it follows (%s): the lead moves the mint and never the term", claims.PaidThrough, wantExpiry)
	}
	if minted.Record.Plan != PolicyTeamMonthly || claims.KeyID == first.KeyID {
		t.Errorf("the monthly renewal minted under the row %q with key id %q, want the row the month is sold under (%q) and a key of its own", minted.Record.Plan, claims.KeyID, PolicyTeamMonthly)
	}
}

// TestALeapYearRenewsOneDayAfterTheWindowOpens: the key a renewal mints covers
// the rest of the term the customer is in and the whole of the next one, so on
// the window's first day it asks for the subscription's own days plus T26's
// 30-day lead — and a leap year is 366 days, one past the tier's ceiling of a
// year plus that lead. The renewal waits the one day rather than asking for a
// term the minter refuses, which at 500 every trigger would leave a customer
// whose licence lapses at the end of a term they paid for.
func TestALeapYearRenewsOneDayAfterTheWindowOpens(t *testing.T) {
	f := newFixture(t)
	first := f.purchase("evt-1", "acme.example", 5, 366)

	f.advance(366*day - 30*day)
	if _, renewed, err := f.svc.Renew(context.Background(), "sub-acme.example"); err != nil || renewed {
		t.Errorf("the leap year's renewal on the window's first day minted (renewed=%v, err=%v), want nothing: the term it would cover is a day past the tier's ceiling", renewed, err)
	}

	f.advance(day)
	minted, renewed, err := f.svc.Renew(context.Background(), "sub-acme.example")
	if err != nil || !renewed {
		t.Fatalf("the leap year's renewal the day after answered (renewed=%v, err=%v), want the next key", renewed, err)
	}
	wantExpiry := first.Expires.Add(366 * day)
	if claims := v1Verified(t, f, minted.Key); !claims.PaidThrough.Equal(wantExpiry) {
		t.Errorf("the renewal covers to %s, want the end of the term it follows (%s): waiting a day moves the mint and never the term", claims.PaidThrough, wantExpiry)
	}
}

// TestARenewalMintsOnlyUnderTheRowTheCheckoutSells: the renewal's key is minted
// under the same rows a purchase is (D442, §12.1), so a subscription whose term
// is one the checkout does not sell and one whose record names no workspace —
// the binding every v1 key carries (TN2, D2134) — are refused with nothing
// minted rather than renewed into a key no row and no verifier can name.
func TestARenewalMintsOnlyUnderTheRowTheCheckoutSells(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct {
		name string
		sub  Subscription
	}{
		{
			name: "a term shorter than the shortest row",
			sub: Subscription{
				ID: "sub-weekly.example", Organisation: "weekly.example", WorkspaceID: workspaceID,
				Email: buyerAddress("weekly.example"), Seats: 5, TermDays: 7,
				At: f.now.Add(-365 * day), Expires: f.now.Add(-day),
			},
		},
		{
			name: "a record with no workspace",
			sub: Subscription{
				ID: "sub-unbound.example", Organisation: "unbound.example",
				Email: buyerAddress("unbound.example"), Seats: 5, TermDays: 365,
				At: f.now.Add(-365 * day), Expires: f.now.Add(-day),
			},
		},
	} {
		if err := f.store.SaveSubscription(context.Background(), tc.sub); err != nil {
			t.Fatalf("saving the subscription for %s: %v", tc.name, err)
		}
		minted, renewed, err := f.svc.Renew(context.Background(), tc.sub.ID)
		if !errors.Is(err, ErrNoClaims) || renewed || minted.Key != "" {
			t.Errorf("the renewal for %s minted (key=%q, renewed=%v, err=%v), want ErrNoClaims with nothing minted", tc.name, minted.Key, renewed, err)
		}
		if after := len(f.records()); after != 0 {
			t.Fatalf("the renewal for %s left %d record(s), want none: a refused renewal spends no source", tc.name, after)
		}
		state, _, err := f.store.Subscription(context.Background(), tc.sub.ID)
		if err != nil {
			t.Fatalf("reading the subscription for %s: %v", tc.name, err)
		}
		if !state.Expires.Equal(tc.sub.Expires) {
			t.Errorf("the term of %s moved to %s, want it where it was (%s): a refused renewal advances nothing", tc.name, state.Expires, tc.sub.Expires)
		}
	}
	if got := len(f.mail.sent); got != 0 {
		t.Errorf("the refused renewals mailed %d message(s), want none", got)
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
	bought := f.now

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
	if !sub.At.Equal(bought) {
		t.Errorf("the retry left the subscription's instant at %s, want the purchase's %s: the renewal is not an event on the merchant's timeline, and a state stamped with the service's clock drops every event sent before it (§5.2)", sub.At, bought)
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

// TestACancellationDeliveredAfterTheRenewalStillCancels: §5.2's ordering
// compares an event's own instant with the subscription's, and the renewal is
// not an event on that timeline — it is the service acting on a customer's
// behalf, and its instant is the service's clock. A renewal that stamped the
// subscription with it would make every event the merchant sent *before* the
// renewal look older than the state: a cancellation that happened before the
// renewal ran and arrived after it — ordinary webhook latency, or the merchant's
// retry after an intake outage — would be answered 200 and dropped for good, the
// subscription would never cancel, and every later renewal would mint another
// paid key for a customer who has left (§5.2, §5.3).
func TestACancellationDeliveredAfterTheRenewalStillCancels(t *testing.T) {
	f := newFixture(t)
	f.purchase("evt-1", "acme.example", 5, 365)
	bought := f.now

	f.advance(365*day - 30*day)
	if _, renewed, err := f.svc.Renew(context.Background(), "sub-acme.example"); err != nil || !renewed {
		t.Fatalf("the renewal answered (renewed=%v, err=%v), want the term's key", renewed, err)
	}
	sub, _, err := f.store.Subscription(context.Background(), "sub-acme.example")
	if err != nil {
		t.Fatalf("reading the subscription: %v", err)
	}
	if !sub.At.Equal(bought) {
		t.Fatalf("the renewal moved the subscription's instant to %s, want the merchant's own %s: the ordering rule compares the two timelines", sub.At, bought)
	}
	minted, mailed := f.signer.calls, len(f.mail.sent)

	// The customer cancelled the day before the renewal ran, and the delivery is
	// late: it arrives now, after the renewal.
	cancelled := f.now.Add(-day)
	if rec := f.event(Event{ID: "evt-2", Type: EventCancellation, Organisation: "acme.example", OccurredAt: cancelled}); rec.Code != http.StatusOK {
		t.Fatalf("the late cancellation was answered %d, want 200", rec.Code)
	}
	sub, _, err = f.store.Subscription(context.Background(), "sub-acme.example")
	if err != nil {
		t.Fatalf("reading the subscription: %v", err)
	}
	if !sub.Cancelled {
		t.Error("a cancellation delivered after a renewal it predates was dropped, want the subscription cancelled: the customer who has left is the customer §5.3 is about")
	}
	if !sub.At.Equal(cancelled) {
		t.Errorf("the subscription's instant is %s, want the cancellation's %s", sub.At, cancelled)
	}

	// And the next term's renewal, a year on: nothing is minted for a customer who
	// has left, which is the harm the drop did — the cancellation was the only
	// thing that ever stopped it.
	f.advance(365 * day)
	if _, renewed, err := f.svc.Renew(context.Background(), "sub-acme.example"); err != nil || renewed {
		t.Errorf("the next term's renewal answered (renewed=%v, err=%v), want nothing: a cancellation stops the reissue", renewed, err)
	}
	if got := f.signer.calls; got != minted {
		t.Errorf("the year after the cancellation signed %d key(s), want the purchase's and the renewal's alone", got)
	}
	if got := len(f.mail.sent); got != mailed {
		t.Errorf("the year after the cancellation mailed %d message(s), want the two the customer was sent", got)
	}
	if got := len(f.records()); got != 2 {
		t.Errorf("the record holds %d key(s), want the purchase's and the renewal's", got)
	}
}

// TestARedeliveredPurchaseDoesNotUndoARenewal: completed's repair finishes a
// write that did not take, from the record the mint left — and a term the store
// has already moved past is not one a redelivery of the purchase may pull back.
// The renewal runs inside the term the purchase paid for, so the store's end is
// later than the record's, and writing the record's end over it would leave the
// store saying the term has ended while the customer holds a key for the year
// after it — and a seat change in that window would mint a key covering the
// wrong term. Nothing is minted twice either: the redelivery is not a purchase.
func TestARedeliveredPurchaseDoesNotUndoARenewal(t *testing.T) {
	f := newFixture(t)
	f.purchase("evt-1", "acme.example", 5, 365)
	bought := f.now

	f.advance(365*day - 30*day)
	if _, renewed, err := f.svc.Renew(context.Background(), "sub-acme.example"); err != nil || !renewed {
		t.Fatalf("the renewal answered (renewed=%v, err=%v), want the term's key", renewed, err)
	}
	renewed, _, err := f.store.Subscription(context.Background(), "sub-acme.example")
	if err != nil {
		t.Fatalf("reading the subscription: %v", err)
	}
	minted, mailed := f.signer.calls, len(f.mail.sent)

	f.event(Event{ID: "evt-1", Type: EventPurchase, Organisation: "acme.example", Seats: 5, TermDays: 365, OccurredAt: bought})

	sub, _, err := f.store.Subscription(context.Background(), "sub-acme.example")
	if err != nil {
		t.Fatalf("reading the subscription: %v", err)
	}
	if !sub.Expires.Equal(renewed.Expires) {
		t.Errorf("the redelivered purchase left the term ending %s, want the renewal's %s: a repair does not move the term backwards", sub.Expires, renewed.Expires)
	}
	if sub.Cancelled {
		t.Error("the redelivery cancelled the subscription")
	}
	if got := f.signer.calls; got != minted {
		t.Errorf("the redelivered purchase signed %d key(s), want none: it is an event the service has already minted for", got-minted)
	}
	if got := len(f.mail.sent); got != mailed {
		t.Errorf("the redelivered purchase mailed %d message(s), want none", got-mailed)
	}
	if got := len(f.records()); got != 2 {
		t.Errorf("the record holds %d key(s), want the purchase's and the renewal's", got)
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
		WorkspaceID:  workspaceID,
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
	claims := v1Verified(t, f, f.mintedKeys()[1])
	if claims.Seats != 5 || !claims.PaidThrough.Equal(first.Expires) {
		t.Errorf("the reissued key claims %d seat(s) to %s, want 5 to %s", claims.Seats, claims.PaidThrough, first.Expires)
	}
}

// TestCancellationStopsReissue: §5.3 — the key the customer holds keeps working
// until it expires (a cancellation withdraws nothing: withdrawing is the refund's
// and the chargeback's, §8) and the renewal does not come.
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
	if claims := v1Verified(t, f, held); !claims.PaidThrough.Equal(first.Expires) {
		t.Errorf("the held key reads paid_through=%s, want %s: a cancellation withdraws nothing", claims.PaidThrough, first.Expires)
	}
}

// TestACancellationStopsTheSeatChangeToo: §5.3's "a cancellation stops the
// reissue" is not the renewal's rule alone, and the seat change is the path that
// shows it — a seat change is a reissue, it mints a second key inside the term
// the customer already holds one for, and a customer who has left has left
// whichever event arrives afterwards. The late seat change is dropped the way a
// redelivery is: the merchant's events are ordinary traffic, a late one is not an
// error to retry forever, and nothing about the subscription moves — not the
// seats, and not its instant.
func TestACancellationStopsTheSeatChangeToo(t *testing.T) {
	f := newFixture(t)
	f.purchase("evt-1", "acme.example", 5, 365)
	f.advance(day)
	f.event(Event{ID: "evt-2", Type: EventCancellation, Organisation: "acme.example"})
	minted, mailed := f.signer.calls, len(f.mail.sent)
	cancelled := f.now

	f.advance(day)
	if rec := f.event(Event{ID: "evt-3", Type: EventSeatChange, Organisation: "acme.example", Seats: 9, TermDays: 365}); rec.Code != http.StatusOK {
		t.Fatalf("the seat change was answered %d, want 200: a dropped event is the merchant's ordinary traffic and not one to retry", rec.Code)
	}

	if got := f.signer.calls; got != minted {
		t.Errorf("a seat change delivered after the cancellation signed %d key(s), want none: a cancellation stops the reissue", got-minted)
	}
	if got := len(f.mail.sent); got != mailed {
		t.Errorf("a seat change delivered after the cancellation mailed %d message(s), want none", got-mailed)
	}
	if got := len(f.records()); got != 1 {
		t.Errorf("the record holds %d key(s), want the one the customer holds", got)
	}
	sub, held, err := f.store.Subscription(context.Background(), "sub-acme.example")
	if err != nil || !held {
		t.Fatalf("reading the subscription: held=%v, err=%v", held, err)
	}
	if !sub.Cancelled {
		t.Error("the subscription is no longer cancelled, want it as the cancellation left it")
	}
	if sub.Seats != 5 {
		t.Errorf("the subscription holds %d seat(s), want the 5 it was cancelled with: a dropped event moves nothing", sub.Seats)
	}
	if !sub.At.Equal(cancelled) {
		t.Errorf("the subscription's instant is %s, want the cancellation's %s: a dropped event moves nothing", sub.At, cancelled)
	}
}

// TestTestModeEventsMintStagingKeysOnly: §5.4 and Check 179 — a merchant event
// in test mode mints a staging key and nothing else, and the signing key named
// in the key is what makes a production verifier refuse it rather than
// half-accept it (§2).
func TestTestModeEventsMintStagingKeysOnly(t *testing.T) {
	staging := newFixtureIn(t, Staging)
	staging.purchase("evt-1", "acme.example", 5, 365)
	key := staging.mintedKeys()[0]
	claims, state, err := licence.VerifyV1([]byte(key), staging.now, staging.ring, workspaceID)
	if err != nil || state != licence.Active {
		t.Fatalf("the staging ring reads its own key as %v, %v", state, err)
	}
	if claims.KID != stagingKeyID {
		t.Errorf("a test-mode purchase minted a key under the ring %q, want the staging id %q", claims.KID, stagingKeyID)
	}
	if _, _, err := licence.VerifyV1([]byte(key), staging.now, licence.Keyring{productionKeyID: staging.ring[productionKeyID]}, workspaceID); !errors.Is(err, licence.ErrUnknownKeyID) {
		t.Errorf("a production keyring read a staging key as %v, want ErrUnknownKeyID", err)
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

// TestAPurchaseMailsTheKeyAndItsRenewalSecret: D2135 and §7.3 — the mail the
// buyer receives is the only place the renewal secret is ever written down (the
// store keeps its digest and never the secret), and that digest is the only
// thing a renewal can be checked against (K2.3). A purchase that mailed the key
// alone, or that left the store no digest, hands a paying customer a licence
// that lapses at the end of its term with nothing either side can do about it —
// and both failures are silent: the mint succeeds and the mail still carries a
// key that works until it expires.
func TestAPurchaseMailsTheKeyAndItsRenewalSecret(t *testing.T) {
	f := newFixture(t)
	bought := f.purchase("evt-1", "acme.example", 5, 365)
	minted := f.mintedKeys()[0]
	claims := v1Verified(t, f, minted)

	if got := len(f.mail.sent); got != 1 {
		t.Fatalf("the purchase mailed %d message(s), want the one that carries the key", got)
	}
	sent := f.mail.sent[0]
	if sent.To != buyerAddress("acme.example") || sent.Subject != keySubject {
		t.Fatalf("the purchase mailed %+v, want the key to the buyer's own address", sent)
	}
	lines := strings.Split(strings.TrimRight(sent.Body, "\n"), "\n")
	keyLine := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == minted {
			keyLine = i
		}
	}
	if keyLine < 0 {
		t.Fatalf("the mail carries no line with the key %q, want the key text itself: %q", minted, sent.Body)
	}
	if keyLine+1 >= len(lines) {
		t.Fatalf("the mail ends on the key line, want the renewal secret below it (D2135): %q", sent.Body)
	}
	secret := strings.TrimSpace(lines[keyLine+1])
	if secret == "" {
		t.Fatalf("the line below the key is empty, want the renewal secret (D2135): %q", sent.Body)
	}
	if got, want := claims.RenewalSecretHash, secretHash(secret); got != want {
		t.Errorf("the key carries the renewal secret's hash %q, want the hash of the secret the mail carried, %q", got, want)
	}

	// The store keeps that digest and never the secret: what a renewal is checked
	// against is the hash, and a store holding the secret is a list of live
	// renewal credentials (§7.3, K2.2).
	state, held, err := f.store.KeyState(context.Background(), bought.KeyID)
	if err != nil || !held {
		t.Fatalf("the store holds no key state for %s (held=%v, err=%v), and without it nothing can serve a renewal", bought.KeyID, held, err)
	}
	if state.KeyID != bought.KeyID {
		t.Errorf("the key state is kept for %q, want the key %q the purchase recorded", state.KeyID, bought.KeyID)
	}
	if state.RenewalSecretHash != claims.RenewalSecretHash {
		t.Errorf("the store holds the digest %q, want the one the key carries, %q: the two are the pair a renewal is checked against", state.RenewalSecretHash, claims.RenewalSecretHash)
	}
	if state.RenewalSecretHash == secret {
		t.Errorf("the store holds the renewal secret itself, want its digest alone: the store is read by whoever operates the service (§7.3)")
	}
}

// TestRefundRevokesTheKey: §8 and D2131 — a refund or a chargeback writes one
// entry of the revocation list, for the one key the merchant names, and that is
// the whole of the event's effect. The list is what the customer's own job and
// §8's status check read, so an entry written for the wrong key is a licence
// withdrawn that nobody was refunded for, and a key left live that was.
//
// The body is verified before it is read, and a refund is the event where that
// matters most: an unsigned one accepted would let anyone who knows a key_id
// revoke a customer's licence, from anywhere, with one POST.
func TestRefundRevokesTheKey(t *testing.T) {
	f := newFixture(t)
	refunded := f.purchase("evt-1", "acme.example", 5, 365)
	other := f.purchase("evt-2", "widget.example", 3, 30)
	signed, recorded := f.signer.calls, len(f.records())

	body, err := json.Marshal(Event{
		ID: "evt-3", Type: EventRefund, Mode: Live, OccurredAt: f.now,
		Subscription: "sub-acme.example", KeyID: refunded.KeyID,
	})
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
	} {
		if rec := f.deliverRaw(body, tc.header); rec.Code != http.StatusOK {
			t.Errorf("%s: the intake answered %d, want 200: the merchant is not told which of its events a stranger could forge", tc.name, rec.Code)
		}
	}
	if got := f.revocations(); len(got) != 0 {
		t.Fatalf("an unverified refund wrote %+v, want no entry", got)
	}

	// The merchant's own refund, naming one of the two keys the customer holds.
	f.advance(time.Hour)
	at := f.now
	f.deliverRaw(body, signEvent(f.secret, at, body))
	entry, held, err := f.store.Revocation(context.Background(), refunded.KeyID)
	if err != nil || !held {
		t.Fatalf("the refund left no entry for %s (held=%v, err=%v), want the key it named withdrawn", refunded.KeyID, held, err)
	}
	if entry.Reason != ReasonRefund {
		t.Errorf("the refund's entry reads %q, want %q", entry.Reason, ReasonRefund)
	}
	if !entry.At.Equal(f.now.Add(-time.Hour)) {
		t.Errorf("the refund's entry is stamped %s, want the event's own instant %s", entry.At, f.now.Add(-time.Hour))
	}
	if _, held, _ := f.store.Revocation(context.Background(), other.KeyID); held {
		t.Errorf("the refund of %s withdrew the key %s too, want the one key the event named", refunded.KeyID, other.KeyID)
	}
	if got := f.revocations(); len(got) != 1 {
		t.Errorf("the refund left %d entry(ies) in §8's list, want one", len(got))
	}
	if got := f.signer.calls; got != signed {
		t.Errorf("a refund signed %d key(s), want none: withdrawing is not issuing", got-signed)
	}
	if got := len(f.records()); got != recorded {
		t.Errorf("a refund recorded %d key(s), want none: §5.2's idempotency is a record of an issuance", got-recorded)
	}

	// The merchant redelivers it, as it redelivers everything, and the entry it
	// already holds is left exactly as it is: the list only ever grows (§8).
	f.advance(2 * time.Hour)
	f.deliverRaw(body, signEvent(f.secret, f.now, body))
	f.advance(2 * time.Hour)
	f.deliverRaw(body, signEvent(f.secret, f.now, body))
	again, _, err := f.store.Revocation(context.Background(), refunded.KeyID)
	if err != nil {
		t.Fatalf("reading the entry back: %v", err)
	}
	if !again.At.Equal(entry.At) || again.Reason != entry.Reason {
		t.Errorf("the redelivered refund changed the entry to %+v, want the first delivery's %+v: a refund delivered twice is one revocation", again, entry)
	}
	if got := f.revocations(); len(got) != 1 {
		t.Errorf("§8's list holds %d entry(ies) after two redeliveries, want the one", len(got))
	}

	// A chargeback names its own reason: what reaches the holder is the key's
	// state, and the licensor's word for it is what the list keeps.
	f.advance(day)
	chargedAt := f.now
	f.event(Event{ID: "evt-4", Type: EventChargeback, OccurredAt: chargedAt, Subscription: "sub-widget.example", KeyID: other.KeyID})
	charged, held, err := f.store.Revocation(context.Background(), other.KeyID)
	if err != nil || !held {
		t.Fatalf("the chargeback left no entry for %s (held=%v, err=%v)", other.KeyID, held, err)
	}
	if charged.Reason != ReasonChargeback || !charged.At.Equal(chargedAt) {
		t.Errorf("the chargeback's entry is %+v, want %q at %s", charged, ReasonChargeback, chargedAt)
	}

	// And a refund that names no key is refused rather than written: an entry
	// with no key_id answers for every key nobody named (K2.2).
	if rec := f.event(Event{ID: "evt-5", Type: EventRefund, OccurredAt: f.now, Subscription: "sub-acme.example"}); rec.Code == http.StatusOK {
		t.Error("a refund that names no key was answered 200, want a refusal: the merchant has to see the bug in the event it sends")
	}
	if got := f.revocations(); len(got) != 2 {
		t.Errorf("§8's list holds %d entry(ies) after the unnamed refund, want the two the merchant named", len(got))
	}

	// A refund older than the subscription's own instant lands anyway: §5.2's
	// ordering is the *subscription's* state and §8's entry is not part of it, so
	// a refund the merchant sends after a seat change has moved that instant
	// forward — or one whose webhook simply arrived late — is the refund the
	// customer is waiting for. Dropped instead, the money is back and the key the
	// refund paid for stays live.
	late := f.purchase("evt-6", "late.example", 2, 30)
	f.advance(day)
	f.event(Event{ID: "evt-7", Type: EventSeatChange, Organisation: "late.example", Seats: 4, TermDays: 30})
	older := f.now.Add(-time.Hour)
	f.event(Event{ID: "evt-8", Type: EventRefund, OccurredAt: older, Subscription: "sub-late.example", KeyID: late.KeyID})
	entry, held, err = f.store.Revocation(context.Background(), late.KeyID)
	if err != nil || !held {
		t.Fatalf("a refund older than the subscription's own instant left no entry for %s (held=%v, err=%v), want the key it named withdrawn", late.KeyID, held, err)
	}
	if entry.Reason != ReasonRefund || !entry.At.Equal(older) {
		t.Errorf("the late refund's entry is %+v, want %q at %s, the event's own instant", entry, ReasonRefund, older)
	}
	// The keys the events named are a licence and a half: the refund of the
	// purchase withdraws the seat change's key beside it, since the two are one
	// licence the customer paid for once (D2147).
	if got := f.revocations(); len(got) != 4 {
		t.Errorf("§8's list holds %d entry(ies), want the four keys of the two licences the merchant named", len(got))
	}
}

// TestARefundRevokesTheWholeRotationChain: §8 and D2147 as the owner ruled on
// 2026-11-13 — a refund withdraws the key its event names **and every successor
// a rotation minted from it**. Those successors are the same licence continued,
// and the money going back has to reach them: without the walk the refunded
// customer keeps a key nothing on the list names, because the entry is keyed by
// the id the merchant's own transaction carries and that is the key a renewal
// replaced.
//
// The chain here is the endpoint's alone, which is the route the walk follows
// from a key to the key that replaced it: TestARefundRevokesEveryKeyOfTheLicence
// is the same ruling on the routes a key is minted by rather than rotated into.
func TestARefundRevokesTheWholeRotationChain(t *testing.T) {
	f, _ := renewing(t)
	refunded := f.purchase("evt-1", "acme.example", 5, 365)
	untouched := f.purchase("evt-2", "widget.example", 3, 30)
	// Two renewals, so the licence the refund names has been succeeded twice: the
	// chain the walk has to follow is three keys long.
	second, secondSecret := renewThroughTheEndpoint(t, f, refunded.KeyID, mailedSecret(t, f, f.mintedKeys()[0]))
	third, _ := renewThroughTheEndpoint(t, f, second.KeyID, secondSecret)

	at := f.now
	f.event(Event{ID: "evt-3", Type: EventRefund, OccurredAt: at, Subscription: refunded.Subscription, KeyID: refunded.KeyID})
	for _, keyID := range []string{refunded.KeyID, second.KeyID, third.KeyID} {
		entry, held, err := f.store.Revocation(context.Background(), keyID)
		if err != nil || !held {
			t.Errorf("the refund left no entry for %s (held=%v err=%v), want the whole chain withdrawn", keyID, held, err)
			continue
		}
		if entry.Reason != ReasonRefund || !entry.At.Equal(at) {
			t.Errorf("the entry for %s is %+v, want %q at the event's own instant %s", keyID, entry, ReasonRefund, at)
		}
	}
	if _, held, _ := f.store.Revocation(context.Background(), untouched.KeyID); held {
		t.Errorf("the refund of %s withdrew the key %s as well, want another licence's key left alone", refunded.KeyID, untouched.KeyID)
	}
	if got := len(f.revocations()); got != 3 {
		t.Errorf("§8's list holds %d entry(ies), want the three keys of the chain", got)
	}

	// The merchant redelivers it, as it redelivers everything: the walk runs
	// again over the same chain and every entry it meets is one the list already
	// holds, so nothing moves — not the reason and not the instant the first
	// delivery stamped (§8, K2.2).
	first := map[string]Revocation{}
	for _, entry := range f.revocations() {
		first[entry.KeyID] = entry
	}
	f.advance(2 * time.Hour)
	f.event(Event{ID: "evt-3", Type: EventRefund, OccurredAt: at, Subscription: refunded.Subscription, KeyID: refunded.KeyID})
	after := f.revocations()
	if len(after) != len(first) {
		t.Fatalf("§8's list holds %d entry(ies) after the redelivery, want the %d the first delivery wrote", len(after), len(first))
	}
	for _, entry := range after {
		was, held := first[entry.KeyID]
		if !held || entry.Reason != was.Reason || !entry.At.Equal(was.At) {
			t.Errorf("the redelivered refund changed the entry for %s to %+v, want the first delivery's %+v: a refund delivered twice is one revocation from the first instant either way", entry.KeyID, entry, was)
		}
	}
}

// TestARefundRevokesEveryKeyOfTheLicence: D2147's ruling on every route a key
// of one sold licence is minted by. A merchant renewal names the subscription
// and the term it covers and **not** the key it follows, and a seat change's
// reissue names the merchant's event — so a walk that follows the rotation chain
// alone, or one that takes a source other than a subscription as the line
// between two licences, leaves a key of this one live, and `/renew` hands its
// holder a fresh successor for a licence the money has gone back for. The key a
// customer's installation actually runs is a seat change's or a renewal's,
// never the purchase's, and the refund names the purchase's key because that is
// the one the merchant's own transaction resolves to: the store holds no
// subscription-to-key index and the event's key id is all it has.
func TestARefundRevokesEveryKeyOfTheLicence(t *testing.T) {
	f, _ := renewing(t)
	refunded := f.purchase("evt-1", "acme.example", 5, 30)
	untouched := f.purchase("evt-2", "widget.example", 3, 30)

	f.advance(20 * day)
	minted, renewed, err := f.svc.Renew(context.Background(), refunded.Subscription)
	if err != nil || !renewed {
		t.Fatalf("the merchant's renewal of %s minted nothing (renewed=%v, err=%v), so there is no second key of the licence to withdraw", refunded.Subscription, renewed, err)
	}
	if minted.Record.Subscription != refunded.Subscription {
		t.Fatalf("the renewal's record names the subscription %q, want the purchase's %q: it is what the walk follows", minted.Record.Subscription, refunded.Subscription)
	}
	before := len(f.records())
	f.event(Event{ID: "evt-4", Type: EventSeatChange, Organisation: "acme.example", Seats: 8, TermDays: 30})
	records := f.records()
	if len(records) != before+1 {
		t.Fatalf("the seat change minted %d key(s), want one", len(records)-before)
	}
	reissued := records[len(records)-1].KeyID
	// The endpoint's rotation of that key, so the walk also has to reach one it
	// found by subscription: the rotation names the key it replaces and stamps
	// the subscription beside it.
	rotated, _ := renewThroughTheEndpoint(t, f, minted.Record.KeyID, minted.RenewalSecret)
	if rotated.KeyID == reissued {
		t.Fatalf("the endpoint's rotation of %s minted the seat change's key %s", minted.Record.KeyID, reissued)
	}

	at := f.now
	f.event(Event{ID: "evt-3", Type: EventRefund, OccurredAt: at, Subscription: refunded.Subscription, KeyID: refunded.KeyID})
	for _, keyID := range []string{refunded.KeyID, minted.Record.KeyID, reissued, rotated.KeyID} {
		entry, held, err := f.store.Revocation(context.Background(), keyID)
		if err != nil || !held {
			t.Errorf("the refund left no entry for %s (held=%v err=%v), want every key of the licence withdrawn: a refunded customer keeps no key a renewal, a seat change or a rotation minted", keyID, held, err)
			continue
		}
		if entry.Reason != ReasonRefund || !entry.At.Equal(at) {
			t.Errorf("the entry for %s is %+v, want %q at the event's own instant %s", keyID, entry, ReasonRefund, at)
		}
	}
	if entry, held, _ := f.store.Revocation(context.Background(), untouched.KeyID); held {
		t.Errorf("the refund of %s withdrew the key %s (%+v), want another licence's key left alone", refunded.KeyID, untouched.KeyID, entry)
	}
	if got := len(f.revocations()); got != 4 {
		t.Errorf("§8's list holds %d entry(ies), want the four keys of the refunded licence", got)
	}
}

// TestARefundOfAKeyNobodySoldTouchesNoOther: the other side of matching a
// licence by its subscription, and both halves of the subscription the walk
// reads. Every key the licensor mints by hand — the admin page's, the mint
// tool's, a trial's, an invoiced Enterprise key — writes no subscription, so an
// empty one is not a licence: a walk that matched on it would withdraw every
// unsold key in the store at the first refund of any one of them. And a refund
// whose key the store does hold is read from that record rather than from the
// event's own word for the subscription, or a refund of an unsold key would walk
// into the licence the merchant named instead — the sold key here — and withdraw
// the keys of a customer whose money was never returned.
func TestARefundOfAKeyNobodySoldTouchesNoOther(t *testing.T) {
	f, _ := renewing(t)
	sold := f.purchase("evt-0", "acme.example", 3, 30)
	first, _, _ := heldKey(t, f, PolicyTeamMonthly)
	second, _, _ := heldKey(t, f, PolicyTeamMonthly)
	withoutASubscription(t, f, first)
	withoutASubscription(t, f, second)

	f.event(Event{ID: "evt-1", Type: EventRefund, OccurredAt: f.now, Subscription: sold.Subscription, KeyID: first})
	if _, held, _ := f.store.Revocation(context.Background(), first); !held {
		t.Errorf("the refund of %s wrote no entry, want the key it named withdrawn", first)
	}
	for _, keyID := range []string{second, sold.KeyID} {
		if entry, held, _ := f.store.Revocation(context.Background(), keyID); held {
			t.Errorf("the refund of the hand-minted key %s withdrew %s as well (%+v), want the key it named alone: its record names no subscription, and an empty one is not the licence the event named", first, keyID, entry)
		}
	}
	if got := len(f.revocations()); got != 1 {
		t.Errorf("§8's list holds %d entry(ies), want the one key the event named", got)
	}
}

// TestSeatChangeReissuesUnderANewKeyID: §5.3, §8 and K2.2 — the reissue is a
// new key under a new `key_id`, and that is what makes §8's list usable at all.
// An entry names one key: a reissue that kept the id of the key it replaces
// would inherit a leak's entry, and the record would name a key that is not the
// one the customer was mailed. What does not move is the term: the customer paid
// for one, and the new key covers what is left of it at the seats they bought.
//
// A refund of the key the reissue replaced withdraws both, because it withdraws
// the licence and not the key the event happens to name (D2147): the reissue
// shares the purchase's subscription, and the key a customer's installation
// actually runs is this one.
func TestSeatChangeReissuesUnderANewKeyID(t *testing.T) {
	f := newFixture(t)
	first := f.purchase("evt-1", "acme.example", 3, 365)
	before := v1Verified(t, f, f.mintedKeys()[0])
	if !strings.HasPrefix(first.KeyID, keyIDPrefix) {
		t.Errorf("the purchase's key id is %q, want one under the %q prefix (spec section 3.2)", first.KeyID, keyIDPrefix)
	}

	f.advance(30 * day)
	f.event(Event{ID: "evt-2", Type: EventSeatChange, Organisation: "acme.example", Seats: 5, TermDays: 365})

	records := f.records()
	if len(records) != 2 {
		t.Fatalf("the seat change left %d key(s), want the purchase's and its own", len(records))
	}
	reissued := records[1]
	if reissued.KeyID == first.KeyID {
		t.Errorf("the reissue kept the key id %q, want its own: an entry of §8's list names one key, and the record has to name the key the customer was mailed", reissued.KeyID)
	}
	after := v1Verified(t, f, f.mintedKeys()[1])
	if after.KeyID != reissued.KeyID {
		t.Errorf("the reissued key claims the key id %q and the record names %q, want the record's own", after.KeyID, reissued.KeyID)
	}
	if after.Seats != 5 || !after.PaidThrough.Equal(before.PaidThrough) {
		t.Errorf("the reissued key claims %d seat(s) to %s, want 5 to %s: the seats move and the term does not", after.Seats, after.PaidThrough, before.PaidThrough)
	}
	if after.WorkspaceID != before.WorkspaceID {
		t.Errorf("the reissued key binds the workspace %q, want the purchase's %q", after.WorkspaceID, before.WorkspaceID)
	}

	if claims := v1Verified(t, f, f.mintedKeys()[1]); claims.KeyID != reissued.KeyID {
		t.Errorf("the key the customer holds claims the key id %q, want the reissued %q", claims.KeyID, reissued.KeyID)
	}

	// The refund names the key the customer no longer holds and withdraws the
	// reissue beside it: the merchant's transaction resolves to the purchase, and
	// both keys are the one licence the money has gone back for.
	f.advance(day)
	f.event(Event{ID: "evt-3", Type: EventRefund, OccurredAt: f.now, Subscription: "sub-acme.example", KeyID: first.KeyID})
	for _, keyID := range []string{first.KeyID, reissued.KeyID} {
		if entry, held, _ := f.store.Revocation(context.Background(), keyID); !held {
			t.Errorf("the refund of %s wrote no entry for %s, want every key of the licence withdrawn", first.KeyID, keyID)
		} else if entry.Reason != ReasonRefund {
			t.Errorf("the entry for %s is %+v, want %q", keyID, entry, ReasonRefund)
		}
	}
}

// TestEnterpriseIsNotACheckoutPlan: D2141 and §5.5 — Enterprise is not sold at
// the checkout. Its keys are invoiced directly by the entity and minted from the
// admin page, so a deployment whose paid plan names the Enterprise tier is one
// whose checkout would price a tier that is never self-serve — and the customer
// would be mailed a key for a licence nobody sold them. The mint is refused
// rather than served the Team row.
func TestEnterpriseIsNotACheckoutPlan(t *testing.T) {
	f := newFixtureWith(t, Production, func(f *fixture) {
		f.cfg.Paid = Plan{Name: paidPlanName, Tier: licence.TierEnterprise, Features: []string{"seats"}}
	})
	if rec := f.deliver(Event{
		ID: "evt-1", Type: EventPurchase, Mode: Live, OccurredAt: f.now,
		Subscription: "sub-acme.example", Organisation: "acme.example", WorkspaceID: workspaceID,
		Email: buyerAddress("acme.example"), Seats: 25, TermDays: 365,
	}); rec.Code == http.StatusOK {
		t.Error("a purchase on a deployment whose paid plan names the Enterprise tier was answered 200, want a refusal: Enterprise is invoiced directly (D2141)")
	}
	if got := f.signer.calls; got != 0 {
		t.Errorf("a checkout that sells no Enterprise key signed %d key(s), want none", got)
	}
	if got := len(f.records()); got != 0 {
		t.Errorf("a checkout that sells no Enterprise key recorded %d key(s), want none", got)
	}
	if len(f.mail.sent) != 0 {
		t.Errorf("a checkout that sells no Enterprise key mailed %d message(s), want none", len(f.mail.sent))
	}
	if _, held, _ := f.store.Subscription(context.Background(), "sub-acme.example"); held {
		t.Error("a refused Enterprise purchase created a subscription, want the event left with nothing")
	}

	// The rows the checkout does sell are the two Team periods, and no term a
	// merchant event can carry reaches an Enterprise one: a renewal, which
	// covers what is left of the term the customer is in, is sold under the row
	// they bought rather than under a longer one. The month row's floor is a
	// calendar month and not the thirty days it is nominal, because the merchant
	// bills a calendar month and February's is 28 days — a refusal here is a 500
	// the merchant retries for ever, so a customer billed for February would
	// never be minted a key at all.
	for _, tc := range []struct {
		days int
		want string
	}{
		{days: 28, want: PolicyTeamMonthly},
		{days: 29, want: PolicyTeamMonthly},
		{days: 30, want: PolicyTeamMonthly},
		{days: 31, want: PolicyTeamMonthly},
		{days: 365, want: PolicyTeamAnnual},
		{days: 366, want: PolicyTeamAnnual},
	} {
		if got, sold := checkoutPolicy(tc.days); !sold || got != tc.want {
			t.Errorf("a term of %d day(s) is sold under %q (sold=%v), want %q", tc.days, got, sold, tc.want)
		}
	}
	if got, sold := checkoutPolicy(7); sold {
		t.Errorf("a term of 7 day(s) is sold under %q, want no row: a key minted for a period nobody sells is a licence the customer paid whatever they paid for", got)
	}
	// The bound above is the same refusal and matters more: a term past a year
	// passes the tier's ceiling by nothing and is refused by the minter, so a
	// purchase carrying one mints nothing and answers 500 to every redelivery
	// rather than failing once at the checkout.
	for _, days := range []int{367, 395, 400} {
		if got, sold := checkoutPolicy(days); sold {
			t.Errorf("a term of %d day(s) is sold under %q, want no row: the minter refuses past the tier's ceiling, and a 500 every redelivery is not a refusal anybody sees", days, got)
		}
	}
	for _, row := range checkoutRows {
		if row.policy == PolicyEnterprise || row.policy == PolicyEnterpriseAirGapped {
			t.Errorf("the checkout sells %q, and §5.5's Enterprise keys are invoiced directly (D2141)", row.policy)
		}
	}
}
