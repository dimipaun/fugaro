// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The paid path: the merchant of record's events (§5). Paddle is the merchant
// for Team checkout (D442), a purchase mints a key stating the seats bought, a
// seat change reissues, a cancellation stops the reissue, and a test-mode event
// mints a staging key and nothing else.
//
// Two rules give this path its shape, and both are about not minting twice: a
// body is verified **before it is read**, and what was minted for which source
// is written down before the answer is returned (§5.2).

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Mode is whether an event is the merchant's live one or its test one.
type Mode string

const (
	Live Mode = "live"
	Test Mode = "test"
)

// ModeFor is the one mode an intake in an environment accepts, and it is the
// deployment's pair that makes it true: the production intake mints production
// keys and the staging one mints staging keys, and neither acts on the other's
// events (§5.4).
func ModeFor(env Environment) Mode {
	if env == Staging {
		return Test
	}
	return Live
}

// The three event types this path acts on (§5.1). Anything else the merchant
// sends is read, recorded as nothing and answered: an event the service does
// not know is not a reason to answer the merchant with an error it would retry.
const (
	EventPurchase     = "purchase"
	EventSeatChange   = "seat_change"
	EventCancellation = "cancellation"
)

// SigningHeader is the header the merchant signs with. It is Paddle's own name
// and it is a constant here rather than configuration, because the scheme below
// is Paddle's too.
const SigningHeader = "Paddle-Signature"

// RenewalLead is T26's 30 days: the next key is minted inside this window
// before the term it follows ends, so two keys overlap for a month and an
// issuing outage bites only once it lasts past 60 days (§8).
const RenewalLead = 30 * 24 * time.Hour

var (
	// ErrSignature is a body the merchant did not sign, or a header this
	// service cannot read as its scheme.
	ErrSignature = errors.New("keyservice: the merchant's signature does not verify over this body")
	// ErrUnknownSubscription is a renewal asked for by an id the store does not
	// hold: an event ordering the service missed, which is the deploy's to
	// reconcile and not a mint to force.
	ErrUnknownSubscription = errors.New("keyservice: there is no subscription by that id")
	// ErrNoBuyer is an event the service cannot deliver a key for: the paid path
	// mails the key to the buyer's own address, and an event whose address is
	// missing or unreadable is one that would end in a key nobody holds. It is
	// answered with a failure rather than a mint, so the merchant's retry and the
	// owner's alerting see it (§5.1).
	ErrNoBuyer = errors.New("keyservice: the event carries no address a key can be mailed to")
)

// Event is one merchant event as the intake reads it. It is the service's own
// canonical shape rather than the merchant's raw notification body: the names
// are this project's, the decode is strict, and the mapping from Paddle's
// payload to this one is the deploy's (S3.7) — which is also where a field
// Paddle adds later is absorbed, instead of silently failing a strict decode
// here.
type Event struct {
	ID           string    `json:"id"`
	Type         string    `json:"type"`
	Mode         Mode      `json:"mode"`
	OccurredAt   time.Time `json:"occurred_at"`
	Subscription string    `json:"subscription"`
	Organisation string    `json:"organisation"`
	// Email is the buyer's own address as the merchant's payload carries it,
	// and it is where the key is mailed: the record's personal data is the
	// minimum a key's delivery needs (§7.3). An event without one is refused
	// rather than minted for, because the key it would carry reaches nobody.
	Email    string `json:"email"`
	Seats    int    `json:"seats"`
	TermDays int    `json:"term_days"`
}

// VerifySignature checks the merchant's signature over the raw body, in the
// scheme Paddle signs with: `Paddle-Signature: ts=<unix>;h1=<hex>`, where the
// digest is HMAC-SHA256 of `ts:body` under the deployment's signing secret.
//
// There is no freshness window, and that is deliberate: §5.2 makes replay the
// event id's job and out-of-order delivery the subscription's own state's, so a
// window would be a second rule that refuses a delivery the merchant meant —
// while an attacker replaying a captured body gets a mint that the record
// already holds and an ordering that does not move backwards either way (§9.1
// row 9).
func VerifySignature(secret []byte, header string, body []byte) error {
	if len(secret) == 0 {
		return errors.New("the merchant's signing secret is not configured, so no body can be verified and none is read")
	}
	stamp, signature := "", ""
	for _, part := range strings.Split(header, ";") {
		name, value, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found {
			continue
		}
		switch name {
		case "ts":
			if stamp == "" {
				stamp = value
			}
		case "h1":
			if signature == "" {
				signature = value
			}
		}
	}
	if stamp == "" || signature == "" {
		return fmt.Errorf("%w: the header carries no ts and h1", ErrSignature)
	}
	if _, err := strconv.ParseInt(stamp, 10, 64); err != nil {
		return fmt.Errorf("%w: the timestamp is not a unix second", ErrSignature)
	}
	signed, err := hex.DecodeString(signature)
	if err != nil {
		return fmt.Errorf("%w: the h1 is not hexadecimal", ErrSignature)
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(stamp + ":"))
	mac.Write(body)
	if !hmac.Equal(signed, mac.Sum(nil)) {
		return ErrSignature
	}
	return nil
}

// webhook receives one merchant event. Everything it refuses is refused with
// the same answer, `200` and no body: a forger learns nothing about which
// bodies are close to verifying, and the merchant is not told which of its
// events a stranger could forge either (§5.2). A failure on this side is a
// `500`, which is the merchant's cue to send it again.
func (s *Service) webhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxEventBytes))
	if err != nil {
		s.accept(w)
		return
	}
	if err := VerifySignature(s.cfg.MerchantSecret, r.Header.Get(SigningHeader), body); err != nil {
		s.accept(w)
		return
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var e Event
	if err := dec.Decode(&e); err != nil {
		s.accept(w)
		return
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		s.accept(w)
		return
	}
	if e.ID == "" || e.Subscription == "" {
		s.accept(w)
		return
	}
	if e.Mode != ModeFor(s.cfg.Environment) {
		s.accept(w)
		return
	}
	if e.OccurredAt.IsZero() {
		e.OccurredAt = s.now()
	}
	if err := s.apply(r.Context(), e); err != nil {
		fail(w, err)
		return
	}
	s.accept(w)
}

// apply is one event's effect on the store: §5.2's idempotency first, then the
// subscription's own order, then the type. Both guards are before any mint, and
// both are refusals to act rather than errors — a redelivery and a late event
// are the merchant's ordinary traffic.
func (s *Service) apply(ctx context.Context, e Event) error {
	if record, held, err := s.recorded(ctx, e.ID); err != nil {
		return err
	} else if held {
		return s.completed(ctx, e, record)
	}
	sub, held, err := s.store.Subscription(ctx, e.Subscription)
	if err != nil {
		return err
	}
	// An event the store is already past does not move it backwards: a replayed
	// purchase does not resurrect a cancelled subscription (§5.2).
	if held && !e.OccurredAt.After(sub.At) {
		return nil
	}
	switch e.Type {
	case EventPurchase:
		return s.purchased(ctx, e)
	case EventSeatChange:
		if !held {
			return nil
		}
		return s.seatChanged(ctx, e, sub)
	case EventCancellation:
		if !held {
			return nil
		}
		sub.Cancelled = true
		sub.At = e.OccurredAt
		return s.store.SaveSubscription(ctx, sub)
	default:
		return nil
	}
}

// completed is the second half of an event whose mint is already recorded, and
// §5.2's idempotency guard is what a redelivery of one meets. What it is for is
// the write the first delivery did not finish: a purchase mints and *then* saves
// the subscription, so a store failure between the two leaves a customer with a
// delivered key and no state — no renewal, no seat changes, no cancellation, and
// no paid-domain refusal — and the merchant's retry would otherwise stop at the
// recorded source and leave it that way forever. Here the retry finishes it from
// the record the mint left, which is the whole of what the event's effect needs:
// the seats, the term, the address and the end of the term are all in it.
//
// The same read-then-write limit is why the store, and not this function, is
// where a durable fix belongs; the Store contract says so and S3.5 is the
// backend that can.
func (s *Service) completed(ctx context.Context, e Event, record Record) error {
	sub, held, err := s.store.Subscription(ctx, e.Subscription)
	if err != nil {
		return err
	}
	// §5.2's ordering is re-read here for the same reason it is read in apply: a
	// redelivery of a purchase must not resurrect the subscription a later
	// cancellation or a later term has already moved past.
	if held && !e.OccurredAt.After(sub.At) {
		return nil
	}
	switch e.Type {
	case EventPurchase:
		to, deliverable := address(record.Email)
		if !deliverable {
			return fmt.Errorf("%w: the purchase %s", ErrNoBuyer, e.ID)
		}
		return s.store.SaveSubscription(ctx, Subscription{
			ID:           e.Subscription,
			Organisation: record.Organisation,
			Seats:        record.Seats,
			TermDays:     daysBetween(record.Issued, record.Expires),
			Email:        to,
			At:           e.OccurredAt,
			Expires:      record.Expires,
		})
	case EventSeatChange:
		if !held || sub.Cancelled || sub.Seats == record.Seats {
			return nil
		}
		sub.Seats = record.Seats
		sub.At = e.OccurredAt
		return s.store.SaveSubscription(ctx, sub)
	}
	return nil
}

// purchased mints the first key of a subscription and records the term it
// covers. The mint happens through the minter and never through a signer here,
// and the seats are the ones the merchant's event names (§5.1).
func (s *Service) purchased(ctx context.Context, e Event) error {
	if e.Organisation == "" || e.Seats < 1 || e.TermDays < 1 {
		return nil
	}
	to, deliverable := address(e.Email)
	if !deliverable {
		return fmt.Errorf("%w: the purchase %s", ErrNoBuyer, e.ID)
	}
	minted, err := s.mint(ctx, s.mintRequest(s.cfg.Paid, e.Organisation, e.Seats, e.TermDays), to, e.ID)
	if err != nil {
		return err
	}
	return s.store.SaveSubscription(ctx, Subscription{
		ID:           e.Subscription,
		Organisation: e.Organisation,
		Seats:        e.Seats,
		TermDays:     e.TermDays,
		Email:        to,
		At:           e.OccurredAt,
		Expires:      minted.Record.Expires,
	})
}

// seatChanged reissues inside the term the customer already paid for: `seats`
// is a claim in the key, and the term is not (§5.3). The new key covers what is
// left of the term, so it ends where the key it replaces ends.
func (s *Service) seatChanged(ctx context.Context, e Event, sub Subscription) error {
	if e.Seats < 1 {
		return nil
	}
	days := daysBetween(s.now(), sub.Expires)
	if days < 1 {
		// The term is over and the renewal is the path that covers the next
		// one; a seat change on an ended term mints nothing rather than
		// minting something that has already expired.
		return nil
	}
	to, deliverable := address(e.Email)
	if !deliverable {
		return fmt.Errorf("%w: the seat change %s", ErrNoBuyer, e.ID)
	}
	if _, err := s.mint(ctx, s.mintRequest(s.cfg.Paid, sub.Organisation, e.Seats, days), to, e.ID); err != nil {
		return err
	}
	sub.Seats = e.Seats
	sub.At = e.OccurredAt
	return s.store.SaveSubscription(ctx, sub)
}

// Renew mints the next key for one subscription when T26's window is open: 30
// days before the term it follows ends, its `issued` on the day the service
// runs, its `expires` at the end of the term it covers.
//
// What calls it is the merchant's renewal or the scheduler that reads one, and
// the route and the authentication that reach it are the deploy's (§5.3) — this
// is the service's half of the rule, and the window is what makes a trigger that
// fires twice harmless: the store moves to the term the key covers, so the
// second call is 395 days early and does nothing.
//
// Nothing is minted for a cancelled subscription, and nothing is withdrawn from
// one: the key the customer holds keeps working until it expires, because the
// service has no revocation (S2.1, §5.3).
//
// The key is mailed, and mailed to the address the subscription kept from its
// purchase: the renewal happens months after the event that carried the address
// and the record is the only place it still is (§5.1). A subscription whose
// address is not one the intake can mail to is refused with ErrNoBuyer rather
// than minted for — the term would otherwise advance on a key that reached
// nobody, and the source is spent once, so that term could never be re-minted.
func (s *Service) Renew(ctx context.Context, subscriptionID string) (Minted, bool, error) {
	sub, held, err := s.store.Subscription(ctx, subscriptionID)
	if err != nil {
		return Minted{}, false, err
	}
	if !held {
		return Minted{}, false, fmt.Errorf("%w: %s", ErrUnknownSubscription, subscriptionID)
	}
	if sub.Cancelled {
		return Minted{}, false, nil
	}
	now := s.now()
	if now.Before(sub.Expires.Add(-RenewalLead)) {
		return Minted{}, false, nil
	}
	next := sub.Expires.Add(time.Duration(sub.TermDays) * 24 * time.Hour)
	days := daysBetween(now, next)
	if days < 1 {
		return Minted{}, false, nil
	}
	source := "renew:" + sub.ID + ":" + sub.Expires.UTC().Format(time.RFC3339)
	if _, minted, err := s.recorded(ctx, source); err != nil {
		return Minted{}, false, err
	} else if minted {
		// One mint per (subscription, term, seats) (§5.2): the source names the
		// term, so a second trigger inside the window finds this and stops.
		return Minted{}, false, nil
	}
	to, deliverable := address(sub.Email)
	if !deliverable {
		return Minted{}, false, fmt.Errorf("%w: the subscription %s", ErrNoBuyer, sub.ID)
	}
	answer, err := s.mint(ctx, s.mintRequest(s.cfg.Paid, sub.Organisation, sub.Seats, days), to, source)
	if err != nil {
		return Minted{}, false, err
	}
	sub.Expires = next
	sub.At = now
	if err := s.store.SaveSubscription(ctx, sub); err != nil {
		return Minted{}, false, err
	}
	return answer, true, nil
}

// mintRequest is the minter's Request for one plan: the claims, and a term in days.
// The service never states a date, and it cannot: `issued` and `expires` are the
// minter's own (§2).
func (s *Service) mintRequest(plan Plan, organisation string, seats, days int) Request {
	return Request{
		Environment:  s.cfg.Environment,
		Tier:         plan.Tier,
		Features:     append([]string(nil), plan.Features...),
		Seats:        seats,
		Organisation: organisation,
		Plan:         plan.Name,
		TermDays:     days,
	}
}

// daysBetween is the whole days from one instant to another, rounded up, and
// never negative.
//
// It rounds up because the term it spells is the term a key has to cover: a key
// that ends a few hours short of the end of the term the customer paid for is a
// key that lapses inside its own term, and one that ends a few hours late is the
// overlap T26 already admits.
func daysBetween(from, to time.Time) int {
	left := to.Sub(from)
	if left <= 0 {
		return 0
	}
	day := 24 * time.Hour
	return int((left + day - 1) / day)
}
