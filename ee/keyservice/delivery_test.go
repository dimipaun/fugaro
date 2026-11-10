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
