// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// What happens when the key cannot be delivered (§7.3 read forwards: a key is
// mailed to the applicant and never stored). The mail is the only place a key
// text exists, so a record written for a mail that failed is a licence nobody
// holds and a source that is spent: the applicant has no link left to follow and
// the merchant's redelivery finds the event already answered. These tests are
// that the failure leaves both ways back open.

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// TestAnUndeliveredFreeKeyIsNotRecorded: the verification mail failed, so the
// mint is not recorded and the link the mail carried is not spent — the
// applicant follows it again and the second try is the one that mints.
func TestAnUndeliveredFreeKeyIsNotRecorded(t *testing.T) {
	f := newFixture(t)
	f.apply(applicantAddress)
	token := f.mailedToken()

	f.mail.err = errors.New("the mail provider refused the message")
	rec := f.verify(token)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("a key whose mail failed was answered %d, want 500: the request did not finish, so it is not an answer", rec.Code)
	}
	if got := len(f.records()); got != 0 {
		t.Errorf("a key whose mail failed recorded %d key(s), want none: the record would be a licence the applicant never received", got)
	}

	// The provider is back, and the link the applicant still holds is live.
	f.mail.err = nil
	answerBody(t, f.verify(token))
	if got := len(f.records()); got != 1 {
		t.Fatalf("the second follow of the link recorded %d key(s), want one", got)
	}
	if got := len(f.mail.sent); got != 2 {
		t.Errorf("the intake mailed %d message(s) in all, want the verification one and the key", got)
	}
	if got := f.mail.sent[1].Subject; got != keySubject {
		t.Errorf("the second mail's subject is %q, want the key's own (%q)", got, keySubject)
	}
}

// TestAnUndeliveredPaidKeyIsNotRecorded: the same rule on the merchant's path.
// The redelivery is what recovers a purchase whose mail failed, and it can only
// be that if the failed delivery left the event's source free.
func TestAnUndeliveredPaidKeyIsNotRecorded(t *testing.T) {
	f := newFixture(t)
	f.mail.err = errors.New("the mail provider refused the message")
	event := Event{
		ID:           "evt-1",
		Type:         EventPurchase,
		OccurredAt:   f.now,
		Subscription: "sub-acme.example",
		Organisation: "acme.example",
		Email:        buyerAddress("acme.example"),
		Seats:        5,
		TermDays:     365,
	}
	if rec := f.event(event); rec.Code != http.StatusInternalServerError {
		t.Errorf("a purchase whose key mail failed was answered %d, want 500: the merchant's retry is what carries the key to the buyer", rec.Code)
	}
	if got := len(f.records()); got != 0 {
		t.Errorf("a purchase whose key mail failed recorded %d key(s), want none", got)
	}
	if _, held, _ := f.store.Subscription(context.Background(), "sub-acme.example"); held {
		t.Error("a purchase whose key mail failed created a subscription, want the event left for the redelivery")
	}

	f.mail.err = nil
	f.event(event)
	if got := len(f.records()); got != 1 {
		t.Fatalf("the redelivered purchase recorded %d key(s), want one", got)
	}
	if got := len(f.mail.sent); got != 1 || f.mail.sent[0].To != buyerAddress("acme.example") {
		t.Errorf("the redelivered purchase mailed %+v, want the key to the buyer's own address", f.mail.sent)
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
		ID: "sub-anonymous.example", Organisation: "anonymous.example", Seats: 1, TermDays: 365,
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

// TestAPurchaseWhoseSubscriptionWriteFailedIsCompleted: the paid path mints and
// then saves the subscription, and the Store contract has no transaction to make
// the two one write — so the failure between them is real: a customer holds a
// key the store has no state for, which is no renewal, no seat change, no
// cancellation and no paid-domain refusal. The merchant redelivers, and what the
// redelivery must do is finish the state from the record the mint left rather
// than stop at the recorded source. Nothing is minted or mailed twice.
func TestAPurchaseWhoseSubscriptionWriteFailedIsCompleted(t *testing.T) {
	f, store := newFailingStore(t)
	store.refuse = 1
	event := Event{
		ID:           "evt-1",
		Type:         EventPurchase,
		OccurredAt:   f.now,
		Subscription: "sub-acme.example",
		Organisation: "acme.example",
		Seats:        5,
		TermDays:     365,
	}
	if rec := f.event(event); rec.Code != http.StatusInternalServerError {
		t.Fatalf("a purchase whose subscription write failed was answered %d, want 500: the merchant's retry is what finishes it", rec.Code)
	}
	if _, held, _ := f.store.Subscription(context.Background(), "sub-acme.example"); held {
		t.Fatal("the failed write left a subscription, want the event for the redelivery")
	}
	if got := len(f.records()); got != 1 {
		t.Fatalf("the failed purchase recorded %d key(s), want the one the customer holds", got)
	}
	record := f.records()[0]
	mailed, signed := len(f.mail.sent), f.signer.calls

	rec := f.event(event)
	if rec.Code != http.StatusOK {
		t.Fatalf("the redelivered purchase was answered %d, want 200: the write it was waiting for succeeded", rec.Code)
	}
	sub, held, err := f.store.Subscription(context.Background(), "sub-acme.example")
	if err != nil || !held {
		t.Fatalf("the redelivered purchase left no subscription (held=%v, err=%v), want the state the record describes", held, err)
	}
	if sub.Organisation != "acme.example" || sub.Seats != 5 || sub.TermDays != 365 {
		t.Errorf("the repaired subscription is %+v, want the record's own organisation, seats and term", sub)
	}
	if !sub.Expires.Equal(record.Expires) {
		t.Errorf("the repaired subscription expires %s, want the key's own term %s", sub.Expires, record.Expires)
	}
	if sub.Email != buyerAddress("acme.example") {
		t.Errorf("the repaired subscription holds the address %q, want the buyer's, which the renewal needs", sub.Email)
	}
	if got := len(f.mail.sent); got != mailed {
		t.Errorf("the redelivery mailed %d message(s) in all, want the purchase's one: the key is delivered once", got)
	}
	if got := f.signer.calls; got != signed {
		t.Errorf("the redelivery signed %d key(s) in all, want the purchase's one", got)
	}
	if got := len(f.records()); got != 1 {
		t.Errorf("the redelivery recorded %d key(s), want the one already held", got)
	}

	// And the state the repair wrote is live: the domain now holds a paid
	// subscription, which is what §4.3's refusal reads.
	answerBody(t, f.apply(applicantAddress))
	if queued := f.queued(); len(queued) != 0 {
		t.Errorf("a free-seat application from a repaired domain was queued (%+v), want the paid-domain refusal", queued)
	}
	if got := len(f.mail.sent); got != mailed {
		t.Errorf("the free-seat application mailed %d message(s) in all, want none: the domain has bought", got)
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

// failingVerifications is the store as a deployment that cannot write a
// verification's dropped state: §7.3's drop is a bookkeeping write that
// `dropPersonal` logs and does not answer with a failure, so the write is one a
// store can lose while the request that made it still succeeds — and what is
// left behind is a record that still carries the applicant's address.
type failingVerifications struct {
	*Memory
	refused int
}

func (f *failingVerifications) SaveVerification(ctx context.Context, v Verification) error {
	if v.Email == "" && v.Domain == "" && v.Organisation == "" && len(v.Repositories) == 0 {
		f.refused++
		return errors.New("the store refused the write")
	}
	return f.Memory.SaveVerification(ctx, v)
}

// newUndroppableStore is the fixture whose store refuses exactly the writes that
// drop a verification's personal data, so that a test can hold what is left when
// the drop does not land.
func newUndroppableStore(t *testing.T) (*fixture, *failingVerifications) {
	t.Helper()
	f := newFixtureWith(t, Production, func(f *fixture) {
		f.store = &Memory{}
		f.deps.Store = &failingVerifications{Memory: f.store}
	})
	return f, f.deps.Store.(*failingVerifications)
}

// TestASpentTokenIsSpentEvenWhenTheDropsWriteFails: the token's own flag is the
// single-use rule (§7.2), and it is the *only* thing holding it when the drop
// does not land: with the address, the domain and the organisation still on the
// record, the mint that follows a second follow would go through — the minter
// has nothing to refuse, and a record the store already holds is not a second
// mint but a second *mail*, because `mint` mails before it records. So a leaked
// link would put a second key in the applicant's mailbox with nothing to show
// for it. Two follows, one key signed, one key mailed.
func TestASpentTokenIsSpentEvenWhenTheDropsWriteFails(t *testing.T) {
	f, store := newUndroppableStore(t)
	f.applyOpenSource(applicantAddress, repositoryURL)
	token := f.mailedToken()

	f.verify(token)
	if got := f.signer.calls; got != 1 {
		t.Fatalf("the first follow signed %d key(s), want one", got)
	}
	if store.refused == 0 {
		t.Fatal("the store took the drop, want the write this test is about refused")
	}
	for _, v := range f.verifications() {
		if v.ID != digest(token) {
			continue
		}
		if v.Email == "" || v.Organisation == "" {
			t.Fatal("the record lost the applicant's fields, want them still on it: the flag is the only guard left")
		}
		if !v.Used {
			t.Fatal("the record is not spent, want the spend to be the write that landed")
		}
	}
	mailed, signed := len(f.mail.sent), f.signer.calls

	f.verify(token)
	if got := f.signer.calls; got != signed {
		t.Errorf("the second follow of one token signed %d key(s) in all, want the first follow's one: a token is spent by the request that carries it (§7.2)", got)
	}
	if got := len(f.mail.sent); got != mailed {
		t.Errorf("the second follow of one token mailed %d message(s) in all, want the key the first one sent", got)
	}
	if got := len(f.records()); got != 1 {
		t.Errorf("the record holds %d key(s), want one", got)
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
