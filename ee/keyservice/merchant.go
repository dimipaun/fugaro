// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The paid path: the merchant of record's events (§5). Paddle is the merchant
// for Team checkout (D442), a purchase mints a v1 key stating the seats bought,
// a seat change reissues under a new key id, a refund or a chargeback withdraws
// the key its event names and every successor a renewal minted from it (§8,
// D2131 — see `withdrawn` for what that does not stop), a cancellation stops the
// reissue, and
// a test-mode event mints a staging key and nothing else. Enterprise is not this
// path: it is invoiced directly by the entity and its key is the admin page's
// (D2141, §5.5).
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

	"github.com/dimipaun/fugaro/ee/licence"
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

// The event types this path acts on (§5.1, §8). Anything else the merchant
// sends is read, recorded as nothing and answered: an event the service does
// not know is not a reason to answer the merchant with an error it would retry.
const (
	EventPurchase     = "purchase"
	EventSeatChange   = "seat_change"
	EventCancellation = "cancellation"
	// EventRefund and EventChargeback are the merchant giving the money back:
	// what they withdraw is the key the buyer holds, and it is §8's revocation
	// list that is written (D2131).
	EventRefund     = "refund"
	EventChargeback = "chargeback"
)

// The reasons §8's list holds that this path writes. Reason is the licensor's
// own word for an entry and never what reaches the holder (K2.2), so it is
// written here where the event that carries it is read.
//
// ReasonRefund and ReasonChargeback are the money the merchant returned.
// ReasonUnrecorded is the service withdrawing its own key: one it minted and
// mailed but could not record, which is a second live key for a purchase that
// already has one, or a key the store has no issuance for at all (K2.2).
const (
	ReasonRefund     = "refund"
	ReasonChargeback = "chargeback"
	ReasonUnrecorded = "unrecorded"
)

// SigningHeader is the header the merchant signs with. It is Paddle's own name
// and it is a constant here rather than configuration, because the scheme below
// is Paddle's too.
const SigningHeader = "Paddle-Signature"

// RenewalLead is T26's 30 days: the next key is minted inside this window
// before the term it follows ends, so two keys overlap for a month and an
// issuing outage bites only once it lasts past 60 days (§8). It is the lead for
// every term longer than twice it, and `Renew` holds a shorter term's window to
// that term's own half — a monthly row's term is 28 to 31 days, so this lead
// would open the window before the purchase that starts it.
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
	// seat change with no organisation, no workspace id, no seats or no term,
	// or one whose term is not a period this checkout sells. Those four are the
	// claims a key would carry and the term it would cover, so an event missing
	// one is one this service cannot serve — and it is refused, like an event
	// with no instant, rather than dropped: an event dropped here is a customer
	// who has paid and receives nothing, with nothing recorded, nothing mailed,
	// nothing queued and nothing logged, and the merchant never sends it again.
	// What has to carry the four is the deploy's payload → `Event` mapping
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
	Email string `json:"email"`
	// WorkspaceID is the Firebase project id of the workspace the customer
	// bought for, and it is the binding every v1 key carries and every verifier
	// compares (D2134 as amended by TN2). The deploy's mapping carries it out of
	// the checkout's own data, where the buyer picked the workspace, and a key
	// minted without one is refused by every verifier that reads it — so a
	// purchase or a seat change without one is refused here rather than minted
	// into a key nobody can install.
	WorkspaceID string `json:"workspace_id"`
	Seats       int    `json:"seats"`
	TermDays    int    `json:"term_days"`
	// KeyID is the key a refund or a chargeback withdraws. §8's list is keyed by
	// the key and not by the subscription, so what the event names is the key the
	// buyer holds — and resolving the merchant's own transaction or subscription
	// to it is the deploy's mapping (S3.7), which is why the field is here rather
	// than looked up: the store is a list of who holds a licence and never of the
	// licences (K2.2, §7.3).
	KeyID string `json:"key_id"`
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

// apply is one event's effect on the store: §5.2's idempotency first, then §8's
// withdrawal where the event is one, then the subscription's own order, then the
// type. Every guard is before any mint, and each is a refusal to act rather than
// an error — a redelivery and a late event are the merchant's ordinary traffic.
//
// The withdrawal is ahead of the ordering guard and not behind it, because §5.2
// orders the *subscription's* state and a withdrawal is not part of it: a refund
// whose instant is older than the subscription's own — the merchant's webhook
// arrived after a seat change moved that instant forward — would be dropped for
// good by that guard, leaving the money returned and the key live.
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
	// The money coming back is §8's own entry and not the subscription's state,
	// so it is applied here, before the subscription is read: what a refund
	// withdraws is the key the event names, and a withdrawal that arrives before
	// the purchase it belongs to has to land as much as one that arrives after it
	// — the key it names is the key it names either way, and the store refuses an
	// entry that names none (K2.2).
	if e.Type == EventRefund || e.Type == EventChargeback {
		return s.withdrawn(ctx, e)
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
		// (§5.2, §5.3). Only the fields the record is the source of move, and the
		// term moves only forward: a renewal runs inside the term the purchase
		// paid for and ends later than it, so a redelivery of the purchase after
		// one would otherwise put the subscription back on the term the renewal
		// replaced — a store that says the term has ended while the customer holds
		// a key for the year after it, and a seat change in that window minting a
		// key that covers the wrong term.
		sub.ID = e.Subscription
		sub.Organisation = record.Organisation
		sub.Seats = record.Seats
		sub.TermDays = daysBetween(record.Issued, record.Expires)
		sub.Email = to
		sub.At = e.OccurredAt
		// The workspace is the event's and not the record's: the issuance record
		// carries no workspace id, so a purchase whose subscription write failed
		// and is repaired here would otherwise leave a record that names none —
		// and Renew refuses such a record rather than minting a key every
		// verifier refuses (TN2, §5.1).
		if e.WorkspaceID != "" {
			sub.WorkspaceID = e.WorkspaceID
		}
		if record.Expires.After(sub.Expires) {
			sub.Expires = record.Expires
		}
		return s.store.SaveSubscription(ctx, sub)
	case EventSeatChange:
		if !held || sub.Cancelled || sub.Seats == record.Seats {
			return nil
		}
		sub.Seats = record.Seats
		if e.WorkspaceID != "" {
			sub.WorkspaceID = e.WorkspaceID
		}
		sub.At = e.OccurredAt
		return s.store.SaveSubscription(ctx, sub)
	}
	return nil
}

// purchased mints the first key of a subscription and records the term it
// covers. The mint happens through the minter and never through a signer here,
// the seats are the ones the merchant's event names (§5.1), and the row is the
// one its term is sold under.
//
// Enterprise is refused before any of that, and it is refused rather than sold
// the Team row: §5.5's keys are invoiced directly by the entity and minted from
// the admin page, so a deployment whose paid plan names the Enterprise tier is
// one whose checkout would price a tier that is never self-serve — and the
// customer would be mailed a key for a licence nobody sold them (D2141).
func (s *Service) purchased(ctx context.Context, e Event) error {
	if s.cfg.Paid.Tier != licence.TierTeam {
		return fmt.Errorf("the checkout sells Team keys and this deployment's paid plan names the %s tier, and an Enterprise key is invoiced directly and minted from the admin page (D2141, §5.5)", s.cfg.Paid.Tier)
	}
	if e.Organisation == "" || e.WorkspaceID == "" || e.Seats < 1 || e.TermDays < 1 {
		return fmt.Errorf("%w: the purchase %s", ErrNoClaims, e.ID)
	}
	policy, sold := checkoutPolicy(e.TermDays)
	if !sold {
		return fmt.Errorf("%w: the purchase %s names a term the checkout does not sell (D442)", ErrNoClaims, e.ID)
	}
	to, deliverable := address(e.Email)
	if !deliverable {
		return fmt.Errorf("%w: the purchase %s", ErrNoBuyer, e.ID)
	}
	minted, err := s.paid(ctx, s.paidRequest(policy, e.WorkspaceID, e.Organisation, e.Seats, e.TermDays), to, e.ID, e.Subscription)
	if err != nil {
		return err
	}
	return s.store.SaveSubscription(ctx, Subscription{
		ID:           e.Subscription,
		Organisation: e.Organisation,
		WorkspaceID:  e.WorkspaceID,
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
//
// The reissue is a **new key**, under a new `key_id`, and that is the whole of
// what makes §8's list able to withdraw it: an entry names one key, so a
// reissued key that kept the id of the key it replaces would be withdrawn by a
// refund of the one the customer no longer holds, and a key reissued after a
// leak would inherit the leak's entry (K2.2, K2.1).
//
// The workspace the reissued key binds to is the event's own, out of the same
// checkout data the purchase's came from (S3.7), and a seat change whose event
// names none is refused rather than minted bound to nothing (TN2, §5.1). The
// subscription the store holds names one too, written by the purchase and kept
// current here, so the event's is a second statement of the same binding rather
// than the only one — and it is the event's that the minted key carries, because
// a seat change is the merchant's statement of the subscription as it now is.
// (Whether a nameless event should fall back to the stored binding instead of
// being refused is a question for the owner, not a decision this path makes.)
//
// A cancelled subscription gets nothing here, as it gets nothing from Renew: the
// key the customer holds keeps working until it expires and no second one is
// minted (S2.1, §5.3).
func (s *Service) seatChanged(ctx context.Context, e Event, sub Subscription) error {
	if sub.Cancelled {
		// §5.3's "a cancellation stops the reissue" is not the renewal's rule
		// alone: a seat change is a reissue, and this is the only paid path that
		// reissues inside a term the customer has already been given a key for, so
		// without this guard it is the one that hands a second key to a customer
		// who has left. It is dropped and not refused, like the redelivery it is
		// the near-twin of: a seat change that lands after the cancellation is late
		// ordinary traffic, and a 500 would have the merchant retry it forever.
		return nil
	}
	if e.Seats < 1 || e.WorkspaceID == "" {
		// The same refusal a purchase without seats gets, and for the same
		// reason: a seat change that names no seats or no workspace is one this
		// service cannot serve, and a customer who has paid for one receives
		// nothing if it is dropped quietly.
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
	policy, sold := checkoutPolicy(sub.TermDays)
	if !sold {
		return fmt.Errorf("%w: the seat change %s is for a subscription whose term is not one the checkout sells (D442)", ErrNoClaims, e.ID)
	}
	if _, err := s.paid(ctx, s.paidRequest(policy, e.WorkspaceID, sub.Organisation, e.Seats, days), to, e.ID, e.Subscription); err != nil {
		return err
	}
	sub.Seats = e.Seats
	sub.WorkspaceID = e.WorkspaceID
	sub.At = e.OccurredAt
	return s.store.SaveSubscription(ctx, sub)
}

// withdrawn writes §8's entries for a key the merchant has given the money back
// for (D2131): the one the event names and every successor a rotation minted
// from it. It is the whole of the event's effect: those keys stop being
// renewable and the customer's own job reads them as revoked, and nothing about
// the subscription moves — a refund is not a cancellation, and the merchant's
// cancellation is the event that stops the reissue (§5.3).
//
// The chain is the **rotation** chain and nothing else, and that is the owner's
// ruling of 2026-11-13: the keys a renewal minted from this one are the same
// licence continued, so the money going back has to reach them, while a seat
// change's key is a different licence the customer still pays for and a refund
// of the key it replaced must not touch it. The two are told apart by the
// record's source — a rotation writes `rotation:<the key it replaces>`
// (see `rotationSource`), a seat change writes the merchant's own event id under
// its subscription — so the walk follows the former and stops on the latter.
// Without it a customer who renewed once and then obtained a refund kept a key
// nothing named and nothing could name: every entry on this list is a key id,
// and the id the refund carries is the superseded one.
//
// It is still not a cancellation: nothing here ends the subscription's ability
// to mint, so a seat change or a trigger inside a term's window mints a fresh
// key the moment after this runs, and a withdrawal of **that** key is the
// merchant's next event. That is the shape §5.3 and §8 state between them — the
// list withdraws keys, the cancellation ends the subscription — and it puts the
// pairing on the deploy's event mapping (S3.7). Making the store's own list stop
// a reissue would mean recording which key a subscription currently holds, which
// is a decision for the owner rather than a repair to make here.
//
// Nothing is recorded here, and that is deliberate: §5.2's idempotency is a
// record of an **issuance**, and a withdrawal mints none. Two things make a
// redelivery harmless anyway — the event id is only ever read for a source it
// might have minted, and the list itself refuses to change an entry it holds, so
// a refund delivered twice is one revocation from the first instant either way
// (K2.2). The walk is idempotent for the same reason: every key it reaches is
// revoked against the list that ignores an entry it already holds, and the
// visited set keeps a chain the store happens to hold in a loop from being
// walked for ever.
//
// A refund whose event names no key is refused by the store rather than
// dropped, and that is the whole of the check here: the one entry an unnamed key
// would write answers for every key nobody named, which is every project
// dropped to Free by a record nobody meant to write (K2.2). The refusal comes
// first, before the chain is read, so an unnamed event withdraws nothing at all
// rather than a successor it happened to reach.
func (s *Service) withdrawn(ctx context.Context, e Event) error {
	reason := ReasonRefund
	if e.Type == EventChargeback {
		reason = ReasonChargeback
	}
	if err := s.store.Revoke(ctx, Revocation{KeyID: e.KeyID, Reason: reason, At: e.OccurredAt}); err != nil {
		return err
	}
	records, err := s.store.Issuances(ctx)
	if err != nil {
		return err
	}
	// successors indexes the rotations the store holds by the key they replace,
	// so the walk from a refunded key to what a renewal minted from it is a
	// lookup rather than a scan per step.
	successors := map[string][]string{}
	for _, record := range records {
		if from, rotation := strings.CutPrefix(record.Source, rotationPrefix); rotation {
			successors[from] = append(successors[from], record.KeyID)
		}
	}
	seen := map[string]bool{e.KeyID: true}
	for walk := []string{e.KeyID}; len(walk) > 0; {
		keyID := walk[len(walk)-1]
		walk = walk[:len(walk)-1]
		for _, next := range successors[keyID] {
			if seen[next] {
				continue
			}
			seen[next] = true
			if err := s.store.Revoke(ctx, Revocation{KeyID: next, Reason: reason, At: e.OccurredAt}); err != nil {
				return err
			}
			walk = append(walk, next)
		}
	}
	return nil
}

// Renew mints the next key for one subscription when T26's window is open: 30
// days before the term it follows ends, or half that term where half is shorter
// than the lead, which every monthly row's is, its `issued` on the day the
// service runs, its `expires` at the end of the term it covers.
//
// The key is a v1 one, minted through the same path a purchase uses and not
// through the older format's `mint`: what every issue path hands a customer is
// the same format (D2130), a renewal is a new key under a new `key_id` — the one
// a refund or a leak is later named by (§8) — it carries the workspace binding
// TN2 requires, and it is mailed with its own renewal secret and written into
// the key state, so that the term after this one can be renewed at all (D2135,
// K2.3). The row is the one the subscription's term is sold under, which is what
// makes a renewal minted inside a term the customer is in carry that term's
// grace and renewal mode (D2133, D442).
//
// What calls it is the merchant's renewal or the scheduler that reads one, and
// the route and the authentication that reach it are the deploy's (§5.3) — this
// is the service's half of the rule, and the window is what makes a trigger that
// fires twice harmless: the store moves to the term the key covers, so the
// second call is 395 days early and does nothing.
//
// Nothing is minted for a cancelled subscription, and nothing is withdrawn from
// one: withdrawing a key is §8's revocation and it is the refund's and the
// chargeback's, and a renewal is neither — the key the customer holds is left
// exactly as it is, and the next trigger is what finds the subscription
// cancelled and stops (D2131, §5.3).
//
// The key is mailed, and mailed to the address the subscription kept from its
// purchase: the renewal happens months after the event that carried the address
// and the record is the only place it still is (§5.1). A subscription whose
// address is not one the intake can mail to is refused with ErrNoBuyer rather
// than minted for — the term would otherwise advance on a key that reached
// nobody, and the source is spent once, so that term could never be re-minted.
//
// The workspace and the row come from the subscription's own record, and a
// record that carries neither the binding nor a term the checkout sells is
// refused rather than minted: a v1 key with no workspace id is one every
// verifier refuses (TN2), and one minted for a period nobody sells is a licence
// the customer paid for with whatever they paid (D442). The refusal is visible
// and the trigger sends it again — the state is what has to be repaired, and a
// key minted to fit it would be the wrong key.
//
// The mint and the store write are two writes, as a purchase's are, and the same
// failure is repaired the same way: the source is recorded before the term is
// saved, so a store that refuses the save leaves a key in the customer's mailbox
// and a subscription still ending where it did — and every later trigger finds
// the source recorded and would stop there. The recorded source carries the end
// of the term it covers, so what is left is read off the record and written.
//
// The subscription's `At` is left alone, and that is the whole of what keeps a
// cancellation: it is the instant of the last merchant event the store applied,
// and §5.2's ordering compares an event's own instant with it. The service's
// clock is not on that timeline, so a renewal that stamped it would make every
// event the merchant sent before the renewal look older than the state — and a
// cancellation that happened before the renewal ran, delivered after it by
// ordinary webhook latency or a merchant retry, would be dropped for good: the
// subscription would never cancel, and every later renewal would mint another
// key for a customer who has left (§5.2, §5.3).
func (s *Service) Renew(ctx context.Context, subscriptionID string) (MintedV1, bool, error) {
	sub, held, err := s.store.Subscription(ctx, subscriptionID)
	if err != nil {
		return MintedV1{}, false, err
	}
	if !held {
		return MintedV1{}, false, fmt.Errorf("%w: %s", ErrUnknownSubscription, subscriptionID)
	}
	if sub.Cancelled {
		return MintedV1{}, false, nil
	}
	now := s.now()
	// T26's lead is 30 days and a monthly row's term is 28 to 31, so a fixed
	// lead opens the window at or before the purchase itself: the trigger that
	// fires then mints a second key for the whole of the term the customer is in
	// and the next one, and the month the merchant billed has bought two. The
	// term's own half is the bound, and it is the half the lead may not exceed:
	// the window still opens inside the term the customer is in, which is the
	// overlap T26 wants — an issuing outage is survived by the key already
	// installed — and it opens in the term's second half rather than on its first
	// day. A year's half is far longer than the lead, so an annual or an
	// Enterprise row renews exactly where it did.
	lead := RenewalLead
	if half := time.Duration(sub.TermDays) * 12 * time.Hour; sub.TermDays > 0 && half < lead {
		lead = half
	}
	if now.Before(sub.Expires.Add(-lead)) {
		return MintedV1{}, false, nil
	}
	next := sub.Expires.Add(time.Duration(sub.TermDays) * 24 * time.Hour)
	days := daysBetween(now, next)
	if days < 1 {
		return MintedV1{}, false, nil
	}
	source := "renew:" + sub.ID + ":" + sub.Expires.UTC().Format(time.RFC3339)
	if record, minted, err := s.recorded(ctx, source); err != nil {
		return MintedV1{}, false, err
	} else if minted {
		// One mint per (subscription, term, seats) (§5.2): the source names the
		// term, so a second trigger inside the window finds this and stops — and
		// what it stops on is the record, which is where the term's end is if the
		// first trigger minted and could not save.
		return MintedV1{}, false, s.renewed(ctx, sub, record)
	}
	policy, sold := checkoutPolicy(sub.TermDays)
	if !sold {
		return MintedV1{}, false, fmt.Errorf("%w: the renewal of the subscription %s is for a term the checkout does not sell (D442)", ErrNoClaims, sub.ID)
	}
	// The key a renewal mints covers the rest of the term the customer is in and
	// the whole of the next one, so it is longer than a term: on the window's
	// first day it asks for the subscription's own days plus T26's 30-day lead,
	// and the tier's ceiling is exactly a year plus that lead. A leap year is 366
	// days, so that first day would ask for 396 and the minter would refuse it —
	// and every trigger after it too, at 500, if this answer were an error rather
	// than nothing to do. Waiting the one day is what the lead means for a year
	// that is a day longer, and it keeps `paid_through` exactly the end of the
	// term the key covers rather than a day short of it.
	if row, known := DefaultPolicy(policy); known {
		if maximum, bounded := tierMaxTerm[row.Tier]; bounded && days > int(maximum/(24*time.Hour)) {
			return MintedV1{}, false, nil
		}
	}
	if sub.WorkspaceID == "" {
		return MintedV1{}, false, fmt.Errorf("%w: the renewal of the subscription %s, whose record names no workspace (TN2)", ErrNoClaims, sub.ID)
	}
	to, deliverable := address(sub.Email)
	if !deliverable {
		return MintedV1{}, false, fmt.Errorf("%w: the subscription %s", ErrNoBuyer, sub.ID)
	}
	answer, err := s.paid(ctx, s.paidRequest(policy, sub.WorkspaceID, sub.Organisation, sub.Seats, days), to, source, sub.ID)
	if err != nil {
		return MintedV1{}, false, err
	}
	sub.Expires = next
	if err := s.store.SaveSubscription(ctx, sub); err != nil {
		return MintedV1{}, false, err
	}
	return answer, true, nil
}

// renewed finishes a renewal whose key is already minted and whose term the
// store did not take: the record names the end of the term it covers, so the
// state the customer paid for is written from it.
// The subscription's own end is the test — it is what the window and the source
// are both computed from — and a state the term has already moved past is left
// alone, so a stale source cannot pull it back.
//
// `At` is left alone here for the reason Renew leaves it alone: it is the
// merchant's timeline and not the service's, and the repair's own instant is not
// an event on it.
func (s *Service) renewed(ctx context.Context, sub Subscription, record Record) error {
	if !sub.Expires.Before(record.Expires) {
		return nil
	}
	sub.Expires = record.Expires
	return s.store.SaveSubscription(ctx, sub)
}

// checkoutRows is the whole of what the checkout sells: the Team month and the
// Team year (D442, spec section 12.1). The two rows are the plans a merchant
// event may be minted under, and Enterprise is not one of them — §5.5's keys are
// invoiced directly and minted from the admin page (D2141).
var checkoutRows = []struct {
	policy string
	period time.Duration
	// floor is the shortest term this row is sold for, and it is below the
	// period for the monthly row alone: the merchant bills a **calendar** month,
	// which February makes 28 days, so a TermDays of 28, 29 or 31 is the month
	// the customer bought rather than a period nobody sells.
	//
	// ceiling is the longest, and it is the calendar's own bound for the same
	// reason: no month is longer than 31 days and no year longer than 366. It is
	// what keeps a term the minter would refuse out of the checkout — the tier's
	// ceiling is a year plus T26's lead (395 days), so a term past a year is
	// minted by nobody, and a purchase carrying one would answer 500 to every
	// redelivery for ever rather than fail once, visibly, at the checkout.
	floor   time.Duration
	ceiling time.Duration
}{
	{PolicyTeamMonthly, monthTerm, 28 * 24 * time.Hour, 31 * 24 * time.Hour},
	{PolicyTeamAnnual, yearTerm, yearTerm, 366 * 24 * time.Hour},
}

// checkoutPolicy answers the row one transaction's term is sold under: the
// longest row the term covers, so that a renewal — which covers what is left of
// the term the customer is in and is therefore longer than a fresh period — is
// minted under the row the customer bought rather than under a longer one.
//
// A term shorter than the shortest row's floor is refused rather than sold the
// cheapest one, and it is the minter's own refusal of a term that is not a term
// read the way it is at the checkout: a key minted for a period nobody sells is
// a licence the customer paid for with whatever they paid. The floor and not the
// period is what decides it, because a refusal here is a 500 the merchant
// retries for ever — a customer whose February month came through as 28 days
// would never be minted a key at all.
func checkoutPolicy(termDays int) (string, bool) {
	policy, sold := "", false
	term := time.Duration(termDays) * 24 * time.Hour
	for _, row := range checkoutRows {
		if term >= row.floor && term <= row.ceiling {
			policy, sold = row.policy, true
		}
	}
	return policy, sold
}

// paidRequest is the minter's v1 Request for one term of one subscription: the
// row the term is sold under, the workspace the checkout bound, and the claims
// and the term in days the merchant's event names (§5.1). It is the v1 twin of
// mintRequest, and the difference between the two is the format: this one names
// a row of the policy table, and the row is where the grace, the renewal mode
// and the check interval come from — the caller names none of them (D2133,
// D2138).
func (s *Service) paidRequest(policy, workspaceID, organisation string, seats, days int) RequestV1 {
	return RequestV1{
		Environment:  s.cfg.Environment,
		Policy:       policy,
		WorkspaceID:  workspaceID,
		Organisation: organisation,
		Seats:        seats,
		TermDays:     days,
	}
}

// paid is the paid path's mint: it asks the minter for one v1 key, mails the
// bundle to the buyer, and writes the issuance and the key's renewal state.
//
// The subscription is the merchant's, and it is written into the record and the
// key's state because this is the only place either is minted for a subscription
// and the only place that knows it: the record carries it so that a renewal can
// tell a licence that is being paid for from one that is not, and the state
// carries the workspace the successor of that renewal is bound to (TN2, K2.3,
// K2.6). A key minted without the pair is a key nothing can renew — refused for
// the subscription by §7's gate and minted into no workspace at all.
//
// The format is the point of the type assertion. The intake holds one Mints —
// the minter's own service (Check 174) — and the minter answers the older
// format's route and the v1 route through the same client, so a service that
// could not answer a v1 request is refused here rather than handed a key in a
// format no released verifier reads: what a customer buys is a v1 key, which is
// the key whose `key_id` a revocation can name and whose claim set carries the
// policy (§8, D2130).
//
// The bundle is mailed for the reason every other issue path's is (D2135): the
// secret's hash goes into the key and the secret goes to the buyer, and only the
// two together can renew. A key mailed without it is a licence that lapses at
// the end of its term with nothing the customer can do about it.
//
// The order is mint, mail, record, and then the key's renewal state — the same
// order `mint` states and for the same reason: a record written for a mail that
// failed is a source spent on a key nobody holds. The state is written last
// because it is the half a failure may lose: an issuance and a key state are two
// writes, and the pair's safe half is a key the store can revoke and cannot
// renew (K2.2). A failure there is still answered, so the deploy's alerting sees
// a key whose renewal nothing can serve.
//
// That order's own price is a different thing in v1 than it was in the older
// format, and it is paid here: `mint` could say that the two keys one source
// mints twice are "the same key in every claim", and that is not true of a v1
// mint — MintV1 makes a fresh key_id and a fresh renewal secret every call. So a
// key minted and mailed and then **not** recorded is not a duplicate of the
// recorded one: it is a second live key, with no issuance and no key state, so
// nothing can renew it, a refund mapped to the recorded key_id leaves it live,
// and the operator reading the store cannot see it at all. The issuance write is
// therefore the one that withdraws the key it was about when it fails — where
// the store really holds no record of that key, which is read back rather than
// taken from the write's answer — which is what puts that key in §8's list where
// the licensor can see and act on it. A
// failed key state withdraws nothing: it is the pair's safe half (above), and
// the key it belongs to is one the store holds, so a revocation can name it and
// a redelivery can finish it.
func (s *Service) paid(ctx context.Context, req RequestV1, applicant, source, subscription string) (MintedV1, error) {
	mints, answersV1 := s.mints.(MintsV1)
	if !answersV1 {
		return MintedV1{}, errors.New("the intake's minter does not answer a v1 mint request, and the paid path issues v1 keys")
	}
	answer, err := mints.MintV1(ctx, req)
	if err != nil {
		return MintedV1{}, err
	}
	answer.Record.Source = source
	answer.Record.Email = applicant
	answer.Record.Subscription = subscription
	if applicant != "" {
		if err := s.mail.Send(ctx, paidKeyMail(applicant, answer.Key, answer.RenewalSecret)); err != nil {
			return MintedV1{}, err
		}
	}
	if err := s.store.SaveIssuance(ctx, answer.Record); err != nil {
		// The key has already been mailed, so a failure here is answered with the
		// key withdrawn: an unrecorded key left live is one no refund can name and
		// no renewal can serve. A redelivery then finds the source free and mints
		// the key the customer actually keeps, which is the path the older format
		// took by minting the same key twice — and it is the source, not the key,
		// that has to be unspent for that to work.
		//
		// The store's answer is not the state, though, and this is the one place
		// the difference matters: a store that commits and then reports the
		// failure — a timeout on the way back, or an internal retry that answers
		// ErrAlreadyRecorded — has this key in hand, and withdrawing it would take
		// the customer's own key away. Worse, the redelivery that follows finds
		// the source spent and repairs nothing but the subscription, so the key the
		// customer paid for would be one withdrawn entry and nothing else, with no
		// key state for the term after it. So the record is re-read before
		// anything is withdrawn: only a source the store really holds no record
		// for, or one it holds under a different key — the delivery that lost its
		// race — has this key withdrawn.
		held, recorded, readErr := s.recorded(ctx, source)
		if readErr != nil {
			// A store that cannot be read is a store that cannot be asked, so
			// nothing is withdrawn on its answer: a withdrawal that lands on a
			// recorded key is the customer's licence gone for good, while a key
			// whose record cannot be read is one the merchant's retry re-reads.
			return MintedV1{}, errors.Join(err, readErr)
		}
		if !recorded || held.KeyID != answer.Record.KeyID {
			if withdrawErr := s.withdrawUnrecorded(ctx, answer.Record.KeyID); withdrawErr != nil {
				return MintedV1{}, errors.Join(err, withdrawErr)
			}
			return MintedV1{}, err
		}
		// The record is this key's, so the write landed and the pair is finished
		// below rather than left for the redelivery, which would find the source
		// spent and write no key state: a key the customer holds and can never
		// renew is the half this path exists to avoid.
	}
	if err := s.store.SaveKeyState(ctx, KeyState{
		KeyID: answer.Record.KeyID,
		// The workspace the request bound the key to, which is the one the
		// checkout's own data named (§5.1, TN2): a state without it is one no
		// renewal can serve, because the successor is minted into the key's own
		// workspace and a request's word for it is not proof (K2.3).
		WorkspaceID:       req.WorkspaceID,
		RenewalSecretHash: digest(answer.RenewalSecret),
	}); err != nil {
		return MintedV1{}, err
	}
	return answer, nil
}

// withdrawUnrecorded puts the key a failed write was about into §8's list: it is
// a key that exists — it was mailed — and that the store has no issuance for, so
// the list is the only place the licensor can see it. The store takes an entry
// for a key it never issued for exactly this reason (K2.2).
func (s *Service) withdrawUnrecorded(ctx context.Context, keyID string) error {
	return s.store.Revoke(ctx, Revocation{KeyID: keyID, Reason: ReasonUnrecorded, At: s.now()})
}

// paidKeyMail carries D2135's bundle: the key on the first line and the renewal
// secret on the second, which is the pair a renewal is made of and the only
// place either is written down outside the key ring. The store holds the
// secret's hash and never the secret (K2.2, §7.3), so a customer who loses this
// mail holds a key that runs to its own end and no further.
func paidKeyMail(to, key, secret string) Message {
	return Message{
		To:      to,
		Subject: keySubject,
		Body: "Your Fugaro licence key:\n\n" + key + "\n" + secret + "\n\n" +
			"Keep both lines: the first is the key and the second is its renewal secret.\n" +
			"Install them in the console, or keep them for the CLI.\n",
	}
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
