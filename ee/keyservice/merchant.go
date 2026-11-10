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
	// ErrUnknownSubscription is an id the store does not hold: a renewal asked
	// for by one, or an event about one, which is an ordering the service missed
	// rather than a mint to force. What reaches the merchant is a `500` and
	// therefore a redelivery, which is the whole of the repair this shape can
	// offer — an event dropped here is one nothing will ever apply.
	ErrUnknownSubscription = errors.New("keyservice: there is no subscription by that id")
	// ErrNoBuyer is an event the service cannot deliver a key for: the paid path
	// mails the key to the buyer's own address, and an event whose address is
	// missing or unreadable is one that would end in a key nobody holds. It is
	// answered with a failure rather than a mint, so the merchant's retry and the
	// owner's alerting see it (§5.1).
	ErrNoBuyer = errors.New("keyservice: the event carries no address a key can be mailed to")
	// ErrNoClaims is an event that does not say what to mint: a purchase or a
	// seat change with no organisation, no seats or no term. Those three are the
	// claims a key would carry and the term it would cover, so an event missing
	// one is one this service cannot serve — and it is refused, like an event
	// with no instant, rather than dropped: an event dropped here is a customer
	// who has paid and receives nothing, with nothing recorded, nothing mailed,
	// nothing queued and nothing logged, and the merchant never sends it again.
	// What has to carry the three is the deploy's payload → `Event` mapping
	// (S3.7).
	ErrNoClaims = errors.New("keyservice: the event does not say what to mint")
	// ErrNoInstant is an event with no instant of its own. §5.2 orders the
	// subscription's state by it, so an event without one is one the service
	// cannot place — and answering it with the service's own clock would make
	// every redelivery of it outrank the state it must not move.
	ErrNoInstant = errors.New("keyservice: the event carries no instant of its own")
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
		// §5.2's ordering is by the subscription's own instant, so an event
		// without one cannot be placed in it — and stamping it with the service's
		// clock is worse than refusing it: the stamp is *now*, so every
		// redelivery of that event is newer than the state it must not move, and
		// a replayed purchase would rewrite a cancelled subscription and let a
		// renewal mint a paid key for it. It is refused rather than dropped
		// silently, so the deploy's mapping of the merchant's payload to this
		// shape (S3.7) fails visibly instead of minting nothing for a customer.
		fail(w, fmt.Errorf("%w: the event %s", ErrNoInstant, e.ID))
		return
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
//
// An event about a subscription the store does not hold is the one case that is
// an error, and it is a `500` so the merchant sends it again. It is not
// hypothetical: a cancellation arriving before the purchase it belongs to, or
// after a purchase whose subscription write failed, is exactly the state a
// store without transactions leaves — and dropping such an event silently loses
// it for good, because the purchase that follows saves an uncancelled
// subscription and the renewal then mints a paid key for a customer who has
// left. A purchase is the exception, because it is what creates the
// subscription. The other repair — a tombstone the store keeps for an event it
// has no subscription for — is a shape the durable backend (S3.5) would have to
// grow, and a retry the owner can see is the one this shape can offer.
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
	// purchase does not resurrect a cancelled subscription (§5.2). What is
	// excluded is an event *older* than the state and nothing else — an event
	// tied with the subscription's own instant is not older than it, and it is
	// ordinary traffic, because a merchant's instants come at second resolution
	// and a purchase and the cancellation that follows it can share one. Dropping
	// the tied event instead loses it: nothing is stored for it, and the drop is
	// before the event id is recorded, so its redelivery is dropped the same way.
	if held && e.OccurredAt.Before(sub.At) {
		return nil
	}
	switch e.Type {
	case EventPurchase:
		return s.purchased(ctx, e)
	case EventSeatChange:
		if !held {
			return fmt.Errorf("%w: the seat change %s for the subscription %s", ErrUnknownSubscription, e.ID, e.Subscription)
		}
		return s.seatChanged(ctx, e, sub)
	case EventCancellation:
		if !held {
			return fmt.Errorf("%w: the cancellation %s for the subscription %s", ErrUnknownSubscription, e.ID, e.Subscription)
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
// the seats, the term, the address and the end of the term are all in it — and
// what it writes into is the subscription the store holds, never a fresh one, so
// a state the store has moved on to since is not thrown away by the repair.
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
	// cancellation or a later term has already moved past. It excludes the same
	// thing it excludes there, an event strictly older than the state, and not a
	// tie: an event tied with the stored instant is one the merchant meant.
	if held && e.OccurredAt.Before(sub.At) {
		return nil
	}
	switch e.Type {
	case EventPurchase:
		to, deliverable := address(record.Email)
		if !deliverable {
			return fmt.Errorf("%w: the purchase %s", ErrNoBuyer, e.ID)
		}
		// The state is written from what the store holds when it holds one, and
		// never from a fresh subscription: a redelivery is not a second purchase,
		// and writing the record's fields into a literal drops everything the
		// subscription has since become — a cancellation that landed on the
		// purchase's own instant is undone by the very write that repairs the
		// term, and the renewal then mints a paid key for a customer who has left
		// (§5.2, §5.3). Only the fields the record is the source of move.
		sub.ID = e.Subscription
		sub.Organisation = record.Organisation
		sub.Seats = record.Seats
		sub.TermDays = daysBetween(record.Issued, record.Expires)
		sub.Email = to
		sub.At = e.OccurredAt
		sub.Expires = record.Expires
		return s.store.SaveSubscription(ctx, sub)
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
		return fmt.Errorf("%w: the purchase %s", ErrNoClaims, e.ID)
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
		// The same refusal a purchase without seats gets, and for the same
		// reason: a seat change that names no seats is one this service cannot
		// serve, and a customer who has paid for one receives nothing if it is
		// dropped quietly.
		return fmt.Errorf("%w: the seat change %s", ErrNoClaims, e.ID)
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
//
// The mint and the store write are two writes, as a purchase's are, and the same
// failure is repaired the same way: the source is recorded before the term is
// saved, so a store that refuses the save leaves a key in the customer's mailbox
// and a subscription still ending where it did — and every later trigger finds
// the source recorded and would stop there. The recorded source carries the end
// of the term it covers, so what is left is read off the record and written.
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
	if record, minted, err := s.recorded(ctx, source); err != nil {
		return Minted{}, false, err
	} else if minted {
		// One mint per (subscription, term, seats) (§5.2): the source names the
		// term, so a second trigger inside the window finds this and stops — and
		// what it stops on is the record, which is where the term's end is if the
		// first trigger minted and could not save.
		return Minted{}, false, s.renewed(ctx, sub, record)
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

// renewed finishes a renewal whose key is already minted and whose term the
// store did not take: the record names the end of the term it covers and the
// instant it was issued, so the state the customer paid for is written from it.
// The subscription's own end is the test — it is what the window and the source
// are both computed from — and a state the term has already moved past is left
// alone, so a stale source cannot pull it back.
func (s *Service) renewed(ctx context.Context, sub Subscription, record Record) error {
	if !sub.Expires.Before(record.Expires) {
		return nil
	}
	sub.Expires = record.Expires
	sub.At = record.Issued
	return s.store.SaveSubscription(ctx, sub)
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
