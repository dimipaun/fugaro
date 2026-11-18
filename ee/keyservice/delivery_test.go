// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// What happens when the key cannot be delivered (§7.3 read forwards: D2182 puts
// the key in the binding's own mail and this service stores none of it). The mail
// is the only place a key text exists, so a record written for a mail that failed
// is a licence nobody holds and an event the merchant's redelivery finds already
// answered. These tests are that a failed delivery leaves the way back open: the
// event is stored unapplied, so the retry is the one that completes it.

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// TestAnUndeliveredPurchaseNoticeIsRetried: the only thing a purchase mails is
// the notice carrying the portal link (D2182 — the key is the binding's, and it
// is issued in the portal), so a delivery whose mail failed is the one where the
// customer has paid and heard nothing. What recovers it is the merchant's retry,
// and that works because the failed delivery left the event unapplied: the body
// is stored as received (F16) and the state says nothing was made of it.
func TestAnUndeliveredPurchaseNoticeIsRetried(t *testing.T) {
	f := newFixture(t)
	f.mail.err = errors.New("the mail provider refused the message")
	event := purchaseEvent("evt-1", "acme.example", 5, 365)
	event.OccurredAt = f.now
	if rec := f.event(event); rec.Code != http.StatusInternalServerError {
		t.Errorf("a purchase whose notice failed was answered %d, want 500: the merchant's retry is what carries the notice to the buyer", rec.Code)
	}
	if got := len(f.mail.sent); got != 0 {
		t.Errorf("a purchase whose notice failed mailed %d message(s), want none", got)
	}
	if got := f.stored(t, "evt-1").State; got != ledgerStored {
		t.Errorf("the failed purchase's event reads %q, want %q: the retry is answered from the state the first delivery left", got, ledgerStored)
	}
	if got := f.ledger(t); len(got) != 0 {
		t.Errorf("a purchase whose processing failed made %d ledger line(s), want none: the money is not counted until the delivery is one this portal applied", len(got))
	}

	f.mail.err = nil
	f.event(event)
	if got := len(f.mail.sent); got != 1 {
		t.Fatalf("the redelivered purchase mailed %d message(s) in all, want the one the retry carried", got)
	}
	if got := f.mail.sent[0]; got.To != buyerAddress("acme.example") || got.Subject != licenceSubject {
		t.Errorf("the purchase was mailed %+v, want the buyer's own address and the licence notice", got)
	}
	if got := f.ledger(t); len(got) != 1 {
		t.Errorf("the retried purchase made %d ledger line(s), want one", len(got))
	}
	// The grant is the one thing a retry must not repeat: it is written before the
	// notice, so the failed delivery above left a licence the customer can already
	// be binding, and a second grant would supersede it (D2204).
	if got := len(f.entitlements(t)); got != 1 {
		t.Errorf("the redelivered purchase left %d licence(s) in all, want the one grant the first delivery made", got)
	}
}

// TestARenewalKeyIsMailedToTheBuyer: a renewal is minted months after the event
// that carried the buyer's address, so the subscription is the only place it
// still is — and a renewal minted with no address is a key that reaches nobody
// while the term advances and the source is spent, which is a term the customer
// paid for and can never be re-minted (§5.1, §5.3).
func TestARenewalKeyIsMailedToTheBuyer(t *testing.T) {
	f := newFixture(t)
	f.purchase("evt-1", "acme.example", 5, 365)
	held := len(f.mail.sent)

	f.advance(365*day - 30*day)
	minted, renewed, err := f.svc.Renew(context.Background(), "sub-acme.example")
	if err != nil || !renewed {
		t.Fatalf("the renewal minted (renewed=%v, err=%v), want the next key", renewed, err)
	}
	if got := len(f.mail.sent); got != held+1 {
		t.Fatalf("the renewal mailed %d message(s) in all, want the purchase's and the renewal's", got)
	}
	last := f.mail.sent[len(f.mail.sent)-1]
	if last.To != buyerAddress("acme.example") {
		t.Errorf("the renewal was mailed to %q, want the buyer's own address, which the subscription kept from its purchase", last.To)
	}
	if last.Subject != keySubject || !strings.Contains(last.Body, minted.Key) {
		t.Errorf("the renewal mail is %q carrying %q, want it to carry the key %q: the mail is the only place a key text exists (§7.3)", last.Subject, last.Body, minted.Key)
	}
	if got := len(f.records()); got != 2 {
		t.Errorf("the record holds %d key(s), want the purchase's and the renewal's", got)
	}
	if got := f.records()[1].Email; got != buyerAddress("acme.example") {
		t.Errorf("the renewal's record holds the address %q, want the buyer's", got)
	}

	// A subscription the store holds with no address a key can be mailed to is
	// refused rather than renewed into the void.
	f.store.SaveSubscription(context.Background(), Subscription{
		ID: "sub-anonymous.example", Organisation: "anonymous.example", WorkspaceID: workspaceID, Seats: 1, TermDays: 365,
		At: f.now.Add(-365 * day), Expires: f.now.Add(day),
	})
	if _, renewed, err := f.svc.Renew(context.Background(), "sub-anonymous.example"); !errors.Is(err, ErrNoBuyer) || renewed {
		t.Errorf("a renewal for a subscription with no address minted (renewed=%v, err=%v), want ErrNoBuyer: the term would advance on a key nobody received", renewed, err)
	}
	if got := len(f.records()); got != 2 {
		t.Errorf("the refused renewal recorded %d key(s) in all, want the two the customer holds", got)
	}
}

// failingSubscriptions is the store as a deployment that cannot write one
// subscription: what §5.2's two writes make possible, so that a test can hold
// the redelivery to finishing it.
type failingSubscriptions struct {
	*Memory
	refuse int
}

func (f *failingSubscriptions) SaveSubscription(ctx context.Context, sub Subscription) error {
	if f.refuse > 0 {
		f.refuse--
		return errors.New("the store refused the write")
	}
	return f.Memory.SaveSubscription(ctx, sub)
}

// newFailingStore is the fixture whose subscription writes can be made to fail
// from the next one on, so that a test chooses which write the deployment loses.
func newFailingStore(t *testing.T) (*fixture, *failingSubscriptions) {
	t.Helper()
	f := newFixtureWith(t, Production, func(f *fixture) {
		f.store = &Memory{}
		f.deps.Store = &failingSubscriptions{Memory: f.store}
	})
	return f, f.deps.Store.(*failingSubscriptions)
}

// failingBought is the document store as a deployment that cannot stamp one
// checkout record: the write a purchase makes after its grant, its subscription
// and its origin, so that a test can stand in the window a merchant's
// redelivery arrives in — the licence granted, and the record that names it not.
type failingBought struct {
	*docsSpy
	refuse int
}

func (f *failingBought) Put(ctx context.Context, collection, id string, doc Doc) error {
	if f.refuse > 0 && collection == checkoutTransactionsCollection {
		f.refuse--
		return errors.New("the store refused the write")
	}
	return f.docsSpy.Put(ctx, collection, id, doc)
}

// newFailingBoughtStore is the fixture whose checkout record cannot be stamped
// once: refusing that write is what leaves a purchase's grant landed and its
// `recordBought` not, which is the window these cases are about.
func newFailingBoughtStore(t *testing.T) (*fixture, *failingBought) {
	t.Helper()
	f := newFixtureWith(t, Production, func(f *fixture) {
		f.deps.Docs = &failingBought{docsSpy: f.docs}
	})
	return f, f.deps.Docs.(*failingBought)
}

// purchaseWithAFailedRecordWrite is one purchase delivered into the window: the
// grant, the subscription and the origin are written, the record that stamps the
// licence onto the checkout fails, and the answer is the `500` the merchant
// retries. It answers the merchant's own bytes, so that the retry is the event
// the merchant sent and not a second one this service wrote.
func purchaseWithAFailedRecordWrite(t *testing.T, f *fixture, docs *failingBought, e Event) []byte {
	t.Helper()
	e = f.claimsOf(e)
	body := paddleBody(t, e)
	docs.refuse = 1
	if rec := f.deliverRaw(body, signEvent(f.secret, f.now, body)); rec.Code != http.StatusInternalServerError {
		t.Fatalf("a purchase whose record write failed was answered %d, want 500: the merchant's retry is what finishes it", rec.Code)
	}
	if got := len(f.entitlements(t)); got != 1 {
		t.Fatalf("the failed delivery left %d licence(s), want the one its grant wrote", got)
	}
	return body
}

// TestARefundedLicenceStaysRevokedWhenThePurchaseIsRedelivered: the window a
// failed record write leaves open is one a refund can land in, and what the
// purchase's own retry then meets is a licence the money has already taken back.
// Rewriting the grant from the event would set it active again, and nothing
// undoes that: the refund is a processed event, so its own redeliveries are
// skipped and no later one withdraws the licence a second time. A customer
// refunded inside that window would hold a live licence for money that went back
// (F29).
func TestARefundedLicenceStaysRevokedWhenThePurchaseIsRedelivered(t *testing.T) {
	f, docs := newFailingBoughtStore(t)
	event := purchaseEvent("evt-1", "acme.example", 5, 365)
	event.OccurredAt = f.now
	body := purchaseWithAFailedRecordWrite(t, f, docs, event)

	// The money goes back inside the window. The refund names the subscription
	// the origin record began, which the failed delivery did write, so it
	// resolves to the licence and is applied.
	f.event(Event{ID: "evt-2", Type: EventRefund, Organisation: "acme.example"})
	if got := f.onlyEntitlement(t).Status; got != entitlementRevoked {
		t.Fatalf("the refunded licence reads %q, want %q: the money went back (F29)", got, entitlementRevoked)
	}
	if got := f.stored(t, "evt-2").State; got != ledgerProcessed {
		t.Fatalf("the refund reads %q, want %q: a processed event is not applied again", got, ledgerProcessed)
	}

	if rec := f.deliverRaw(body, signEvent(f.secret, f.now, body)); rec.Code != http.StatusOK {
		t.Fatalf("the redelivered purchase was answered %d, want 200: it is the retry that finishes the writes it left", rec.Code)
	}
	held := f.onlyEntitlement(t)
	if held.Status != entitlementRevoked {
		t.Errorf("the redelivered purchase left the licence %q, want %q: a grant a refund already took back is answered and not rewritten, or the customer holds a live licence for money that went back (F29)", held.Status, entitlementRevoked)
	}
}

// TestABoundLicenceStaysBoundWhenThePurchaseIsRedelivered: the same window, and
// the customer binds the licence inside it — the portal serves it, the key is
// minted and the binding is what writes BoundAt. Rewriting the grant from the
// event would clear BoundAt and RebindableAt with it, so the licence the
// customer's key is bound to reads as unbound, and D2198's clock for the next
// rebind is dropped as well.
func TestABoundLicenceStaysBoundWhenThePurchaseIsRedelivered(t *testing.T) {
	f, docs := newFailingBoughtStore(t)
	event := purchaseEvent("evt-1", "acme.example", 5, 365)
	event.OccurredAt = f.now
	body := purchaseWithAFailedRecordWrite(t, f, docs, event)

	f.bind("acme.example", "sub-acme.example")
	bound := f.onlyEntitlement(t)
	if bound.BoundAt == nil {
		t.Fatal("the customer's binding left the licence unbound, want the instant it wrote")
	}

	if rec := f.deliverRaw(body, signEvent(f.secret, f.now, body)); rec.Code != http.StatusOK {
		t.Fatalf("the redelivered purchase was answered %d, want 200: it is the retry that finishes the writes it left", rec.Code)
	}
	licences := f.entitlements(t)
	if len(licences) != 1 {
		t.Fatalf("the redelivery left %d licence(s), want the one licence the purchase bought (D2204)", len(licences))
	}
	held := licences[0]
	if held.BoundAt == nil || !held.BoundAt.Equal(*bound.BoundAt) {
		t.Errorf("the redelivered purchase left the licence bound at %v, want %s: the customer's key is bound to a licence the portal reads as the one it was minted for", held.BoundAt, bound.BoundAt)
	}
	if held.Status != entitlementActive {
		t.Errorf("the redelivered purchase left the licence %q, want %q", held.Status, entitlementActive)
	}
}

// TestARedeliveredPurchaseKeepsTheLicenceItsRecordNames: the checkout's record
// is what a redelivery of a purchase is read against, and once its grant has
// landed it names the licence (`recordBought`). A case's own deliveries cannot
// show that: `claimsOf` stands the record up on every delivery and blanks its
// licence id, so every redelivery in this package is read against an unstamped
// record. This one posts the merchant's bytes itself, so the retry meets the
// record the first delivery stamped — and what it must leave is the one licence
// it names, still bound, rather than a second grant or a licence unbound.
func TestARedeliveredPurchaseKeepsTheLicenceItsRecordNames(t *testing.T) {
	f := newFixture(t)
	event := purchaseEvent("evt-1", "acme.example", 5, 365)
	event.OccurredAt = f.now
	event = f.claimsOf(event)
	body := paddleBody(t, event)
	if rec := f.deliverRaw(body, signEvent(f.secret, f.now, body)); rec.Code != http.StatusOK {
		t.Fatalf("the purchase was answered %d, want 200: %s", rec.Code, rec.Body)
	}
	f.bind("acme.example", "sub-acme.example")
	bound := f.onlyEntitlement(t)

	record, held, err := getRecord[checkoutTransaction](context.Background(), f.docs, checkoutTransactionsCollection, event.Transaction)
	if err != nil || !held {
		t.Fatalf("the checkout's record is held=%v (err=%v), want the purchase's own", held, err)
	}
	if record.EntitlementID != bound.ID {
		t.Fatalf("the checkout's record names the licence %q, want the one the grant wrote (%q)", record.EntitlementID, bound.ID)
	}

	// The merchant sends the event again. Its effect is in the store, so the
	// state is set back to what a delivery whose answer was lost leaves behind —
	// the fixture's deliveries carry the state over, and posting a body again is
	// exactly the retry this case is about.
	if err := f.svc.markEvent(context.Background(), "evt-1", ledgerStored, nil); err != nil {
		t.Fatalf("setting the event back to %q: %v", ledgerStored, err)
	}
	if rec := f.deliverRaw(body, signEvent(f.secret, f.now, body)); rec.Code != http.StatusOK {
		t.Fatalf("the redelivered purchase was answered %d, want 200", rec.Code)
	}
	licences := f.entitlements(t)
	if len(licences) != 1 {
		t.Fatalf("the redelivery left %d licence(s), want the one licence the record names (D2204)", len(licences))
	}
	if licences[0].ID != bound.ID {
		t.Errorf("the redelivery left the licence %q, want the one the checkout's record names (%q)", licences[0].ID, bound.ID)
	}
	if licences[0].BoundAt == nil || !licences[0].BoundAt.Equal(*bound.BoundAt) {
		t.Errorf("the redelivery left the licence bound at %v, want %s: the key the binding minted is bound to it", licences[0].BoundAt, bound.BoundAt)
	}
}

// TestAPurchaseWhoseSubscriptionWriteFailedIsRetried: a purchase grants the
// licence and then saves the subscription, and the store has no transaction to
// make the two one write (§5.2) — so the failure between them is real, and what
// the merchant's retry meets is an event whose grant landed and whose
// subscription did not. The retry reads the licence back off the checkout's own
// record, which the grant wrote it onto, and finishes the writes after it; what
// it must not do is leave the customer with two licences at all (D2204) or a
// term that starts at the retry rather than at the payment (D2197).
func TestAPurchaseWhoseSubscriptionWriteFailedIsRetried(t *testing.T) {
	f, store := newFailingStore(t)
	store.refuse = 1
	event := purchaseEvent("evt-1", "acme.example", 5, 365)
	event.OccurredAt = f.now
	if rec := f.event(event); rec.Code != http.StatusInternalServerError {
		t.Fatalf("a purchase whose subscription write failed was answered %d, want 500: the merchant's retry is what finishes it", rec.Code)
	}
	if _, held, _ := f.store.Subscription(context.Background(), "sub-acme.example"); held {
		t.Fatal("the failed write left a subscription, want the event for the redelivery")
	}
	if got := f.records(); len(got) != 0 {
		t.Fatalf("the failed purchase recorded %d key(s), want none: nothing is minted before a binding (D2196)", len(got))
	}
	mailed := len(f.mail.sent)

	if rec := f.event(event); rec.Code != http.StatusOK {
		t.Fatalf("the redelivered purchase was answered %d, want 200: the write it was waiting for succeeded", rec.Code)
	}
	sub, held, err := f.store.Subscription(context.Background(), "sub-acme.example")
	if err != nil || !held {
		t.Fatalf("the redelivered purchase left no subscription (held=%v, err=%v)", held, err)
	}
	if sub.Organisation != "acme.example" || sub.Seats != 5 || sub.TermDays != 365 {
		t.Errorf("the repaired subscription is %+v, want the event's own organisation, seats and term", sub)
	}
	// The term the event states, and never a zero: a subscription at zero days is
	// one `checkoutPolicy` does not sell, so every renewal and every seat change
	// after it is refused for good — the customer has paid and the store holds a
	// term nobody sells (D442).
	if _, sold := checkoutPolicy(sub.TermDays); !sold {
		t.Errorf("the repaired subscription stands for %d day(s), want a term the checkout sells", sub.TermDays)
	}
	// The term runs from the payment and not from the retry: a repair that read
	// its end off its own instant would file a year's purchase from the day the
	// store came back (D2197).
	if want := event.OccurredAt.Add(365 * day); !sub.Expires.Equal(want) {
		t.Errorf("the repaired subscription expires %s, want the end of the term the customer bought (%s), which runs from the payment (D2197)", sub.Expires, want)
	}
	if sub.Email != buyerAddress("acme.example") {
		t.Errorf("the repaired subscription holds the address %q, want the buyer's, which the renewal needs", sub.Email)
	}
	if got := len(f.mail.sent) - mailed; got != 1 {
		t.Errorf("the redelivery mailed %d message(s), want the purchase's one notice", got)
	}
	if got := f.records(); len(got) != 0 {
		t.Errorf("the redelivery recorded %d key(s), want none: the licence is granted and the key is the binding's (D2196)", len(got))
	}
	// D2204's one grant per workspace is what the retry has to keep and not add
	// to: the customer's binding reads the licence the portal shows as theirs, and
	// a second one — the first grant superseded by the retry's — would leave the
	// key bound to the licence the portal no longer serves them from.
	if got := len(f.entitlements(t)); got != 1 {
		t.Errorf("the retried purchase left %d licence(s) in all, want the one grant the first delivery made", got)
	}

	// A replay of the same purchase does not resurrect state the store has moved
	// past: a cancellation that lands later is not undone by the redelivery.
	f.advance(day)
	f.event(Event{ID: "evt-2", Type: EventCancellation, Organisation: "acme.example"})
	f.event(event)
	cancelled, _, _ := f.store.Subscription(context.Background(), "sub-acme.example")
	if !cancelled.Cancelled {
		t.Error("a redelivered purchase resurrected a cancelled subscription, want the cancellation to stand: an event the store is past does not move it backwards (§5.2)")
	}
}

// TestASeatChangeWhoseSubscriptionWriteFailedIsCompleted: the same failure on
// the reissue path, where the record holds the seats the customer asked for and
// the subscription still holds the old ones.
func TestASeatChangeWhoseSubscriptionWriteFailedIsCompleted(t *testing.T) {
	f, store := newFailingStore(t)
	f.purchase("evt-1", "acme.example", 3, 365)

	f.advance(day)
	store.refuse = 1
	event := Event{
		ID: "evt-2", Type: EventSeatChange, OccurredAt: f.now,
		Subscription: "sub-acme.example", Organisation: "acme.example", Seats: 5, TermDays: 365,
	}
	if rec := f.event(event); rec.Code != http.StatusInternalServerError {
		t.Fatalf("a seat change whose subscription write failed was answered %d, want 500", rec.Code)
	}
	if got := len(f.records()); got != 2 {
		t.Fatalf("the seat change recorded %d key(s), want the reissue's too: the mint is what the customer holds", got)
	}
	mailed, signed := len(f.mail.sent), f.signer.calls

	f.event(event)
	sub, _, _ := f.store.Subscription(context.Background(), "sub-acme.example")
	if sub.Seats != 5 {
		t.Errorf("the repaired subscription holds %d seat(s), want the 5 the reissued key states (§5.3)", sub.Seats)
	}
	if got := len(f.mail.sent); got != mailed {
		t.Errorf("the redelivery mailed %d message(s) in all, want the reissue's one", got)
	}
	if got := f.signer.calls; got != signed {
		t.Errorf("the redelivery signed %d key(s) in all, want the reissue's one", got)
	}
}

// TestAPurchaseWithNoDeliverableAddressIsRefused: the paid path mails the key to
// the buyer's address the merchant's payload carries, so an event that carries
// none is an event the service cannot finish. It is refused, and refused before
// the mint: a purchase minted for and recorded would be a key nobody holds, and
// §7.1's uniform answer is the forms' and not the merchant's.
func TestAPurchaseWithNoDeliverableAddressIsRefused(t *testing.T) {
	f := newFixture(t)
	for i, raw := range []string{
		"",
		"not-an-address",
		buyerAddress("acme.example") + "\r\nBcc: victim@elsewhere.example",
	} {
		rec := f.deliver(Event{
			ID:           "evt-" + strconv.Itoa(i),
			Type:         EventPurchase,
			Mode:         ModeFor(f.env),
			OccurredAt:   f.now,
			Subscription: "sub-" + strconv.Itoa(i),
			Organisation: "acme.example",
			WorkspaceID:  workspaceID,
			Email:        raw,
			Seats:        5,
			TermDays:     365,
		})
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("a purchase carrying %q was answered %d, want 500: it is a purchase the service cannot deliver for", raw, rec.Code)
		}
	}
	if got := f.signer.calls; got != 0 {
		t.Errorf("a purchase with no address to mail the key to signed %d key(s), want none", got)
	}
	if got := len(f.records()); got != 0 {
		t.Errorf("a purchase with no address to mail the key to recorded %d key(s), want none", got)
	}
	if len(f.mail.sent) != 0 {
		t.Errorf("a purchase with no address to mail the key to mailed %d message(s), want none", len(f.mail.sent))
	}
}
