// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The paid path: the merchant of record's events (§5, portal plan P4.2). Paddle
// is the merchant for Team checkout (D442), a purchase grants the customer an
// **entitlement** and never a key (D2196 — the key is minted at binding, which
// is workspaces.go's), a seat change reissues under a new key id, a refund or a
// chargeback withdraws the keys of the entitlement its transaction or
// subscription belongs to (§8, D2131 — see `withdrawn` for how the walk finds
// them and what that does not stop), a cancellation stops the reissue, and a
// test-mode event mints a staging key and nothing else. Enterprise is not this
// path: it is invoiced directly by the entity and its key is the admin page's
// (D2141, §5.5).
//
// Three rules give this path its shape. A body is verified **before it is read**,
// with the signature's own timestamp inside a five-minute window (F16). The
// verified body is then **stored as received, before it is processed**, under
// Paddle's event id — which is what makes a redelivery an event the store
// already has rather than a second application of it, and what keeps a forged
// body from being pre-stored under a real id. And an event is **admitted** only
// if its transaction, or the subscription it belongs to, began with a
// transaction this API created at the checkout (F15, D2192); anything else is
// parked for operations rather than acted on.
//
// The body the intake reads is Paddle's own notification, and the adapter that
// maps it to this service's canonical event is `paddleevents.go`.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
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

// renewalPrefix opens the source the merchant's own renewal writes. The source
// carries the subscription and the term because a renewal's key id says nothing
// about the licence it belongs to, and §8's walk from a refunded key to the rest
// of the licence matches records on exactly that subscription — the renewal's
// among them, which is the key a paying customer actually holds (§5.1).
const renewalPrefix = "renew:"

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
	// ErrNoBuyer is an event the service cannot answer for: the paid path writes
	// the customer a notice at the buyer's own address, and an event whose
	// address is missing or unreadable is one that would end in a licence nobody
	// is told about. It is answered with a failure rather than a grant, so the
	// merchant's retry and the owner's alerting see it (§5.1).
	ErrNoBuyer = errors.New("keyservice: the event carries no address a licence can be sent to")
	// ErrNoTransaction is a purchase this API did not open: the event names a
	// transaction the checkout's own record does not hold, and the subscription
	// it belongs to is one nothing here began either. It is the case F15 parks
	// for operations rather than the case this path refuses, and the two are told
	// apart before `purchased` is reached — so reaching it here means the record
	// a first delivery read was gone by the second, which is a `500` and a
	// redelivery rather than a sale written against a transaction nobody made.
	ErrNoTransaction = errors.New("keyservice: the purchase's transaction is not one this API created")
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
// are this project's, and the mapping from Paddle's payload to this one is
// `paddleevents.go` — which is also where a field Paddle adds later is absorbed,
// instead of failing the intake on the day the merchant ships a change.
type Event struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	Mode       Mode      `json:"mode"`
	OccurredAt time.Time `json:"occurred_at"`
	// Transaction is the merchant's own transaction id, which is the half of
	// F15's admission rule the event itself states: a transaction this API
	// created at the checkout (P4.1) is what makes a purchase admissible, and a
	// renewal of the subscription it began is admissible by the other half.
	Transaction  string `json:"transaction"`
	Subscription string `json:"subscription"`
	Organisation string `json:"organisation"`
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
}

// webhook receives one merchant event, in the order F16 fixes: **verify, store,
// then process**.
//
// Everything it refuses is refused with the same answer, `200` and no body: a
// forger learns nothing about which bodies are close to verifying, and the
// merchant is not told which of its events a stranger could forge either (§5.2).
// A failure on this side is a `500`, which is the merchant's cue to send it
// again.
//
// The one answer that is neither is the undecodable verified body's `400`. It is
// a `400` and not a `200` because the body was Paddle's own — it verified — and
// a body this service cannot read is a mapping that has stopped working, which
// the merchant's retry and operations' list are the two things that see (F16).
// It is stored all the same, so that what arrived is not lost with the failure.
func (s *Service) webhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxEventBytes))
	if err != nil {
		s.accept(w)
		return
	}
	header := r.Header.Get(SigningHeader)
	if err := VerifyDelivery(s.cfg.MerchantSecret, header, body, s.now()); err != nil {
		// A body whose signature does not verify, and a body whose signature is
		// outside the window, are one answer: the window is not something a
		// stranger posting bodies is told about either.
		s.accept(w)
		return
	}
	ctx := r.Context()
	n, err := decodeNotification(body)
	if err != nil {
		digest := eventDigest(body)
		// The digest is the id of a body that names none of Paddle's, and it is
		// derived from the bytes rather than minted here: the same body
		// delivered twice is one record, and a stranger who could choose the id
		// would choose the one a real event is stored under (F16).
		if _, err := s.keepReceived(ctx, WireLedgerEvent{
			ID: digest, ReceivedAt: s.now(), Signature: header, Body: string(body),
		}); err != nil {
			fail(w, err)
			return
		}
		if err := s.markEvent(ctx, digest, ledgerUndecodable, nil); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	held, err := s.keepReceived(ctx, WireLedgerEvent{
		ID: n.key(), ReceivedAt: s.now(), Signature: header,
		Notification: n.EventType, Body: string(body),
	})
	if err != nil {
		fail(w, err)
		return
	}
	if held.State == ledgerProcessed || held.State == ledgerDismissed {
		// §5.2's idempotency, and for the events that mint nothing it is the
		// whole of it: the store already holds what this event did, and the body
		// arriving again does not unmake it. The state is read from the record
		// the store held rather than from this delivery, because it is the store
		// that knows the event was applied.
		s.accept(w)
		return
	}
	e, known := canonicalEvent(n, ModeFor(s.cfg.Environment))
	if !known {
		// A notification this service does not act on: a customer record, a
		// payment failure, a reversal. It is stored and answered, because an
		// event the merchant adds later is not a reason to make it retry forever.
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
		// silently, so the adapter fails visibly instead of minting nothing for a
		// customer.
		fail(w, fmt.Errorf("%w: the event %s", ErrNoInstant, e.ID))
		return
	}
	admitted, err := s.admitted(ctx, e)
	if err != nil {
		fail(w, err)
		return
	}
	if !admitted {
		// F15: an event whose transaction and subscription both began with
		// something this API did not create is parked, not applied and not
		// refused. Operations is what attaches or dismisses it (P5.1), and the
		// body is in the list for them either way.
		if err := s.markEvent(ctx, e.ID, ledgerParked, nil); err != nil {
			fail(w, err)
			return
		}
		s.accept(w)
		return
	}
	if err := s.apply(ctx, e); err != nil {
		fail(w, err)
		return
	}
	if err := s.markEvent(ctx, e.ID, ledgerProcessed, s.entitlementOfEvent(ctx, e)); err != nil {
		fail(w, err)
		return
	}
	s.accept(w)
}

// admitted is F15's rule: an event is admitted when its transaction, or the
// subscription it belongs to, began with a transaction this API created at the
// checkout.
//
// The two halves are Paddle's own behaviour. A first purchase's transaction is
// the one P4.1 created, and the checkout's record is keyed by it. Everything
// after that — a renewal, a proration, a seat change, a cancellation, a refund —
// is Paddle acting on the subscription that transaction began, and what says so
// is the record this service wrote when it processed the purchase
// (`paddleOrigin`). A subscription a stranger started has neither.
func (s *Service) admitted(ctx context.Context, e Event) (bool, error) {
	if e.Transaction != "" {
		if _, held, err := getRecord[checkoutTransaction](ctx, s.docs, checkoutTransactionsCollection, e.Transaction); err != nil {
			return false, err
		} else if held {
			return true, nil
		}
	}
	if e.Subscription != "" {
		if _, held, err := s.origin(ctx, e.Subscription); err != nil {
			return false, err
		} else if held {
			return true, nil
		}
	}
	return false, nil
}

// entitlementOfEvent is the entitlement one event resolved to, or nil for an
// event that resolved to none. It is written onto the stored event so that a
// person reading the ledger can walk from the money to the licence it bought
// without reading the store's own records.
//
// The resolution is the one `withdrawn` makes, because the two are one question
// asked at two moments: a refund names a transaction or a subscription, and the
// entitlement is what both of them belong to.
func (s *Service) entitlementOfEvent(ctx context.Context, e Event) *string {
	held, found, err := s.entitlementFor(ctx, e)
	if err != nil || !found {
		return nil
	}
	return &held.ID
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
		if sub.Seats == e.Seats {
			// Paddle's `subscription.updated` fires for every change to a
			// subscription, and the seats are only one of them: a body stating
			// the seats the store already holds is a change to something else —
			// a price, a status, a billing period — and minting a key for it
			// would reissue the licence on every unrelated update the merchant
			// sends. Nothing is done and the event is recorded as processed,
			// because it is the event that is stored and read, not the mint.
			return nil
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
// the write the first delivery did not finish: a seat change mints and *then*
// saves the subscription, so a store failure between the two leaves a customer
// with a delivered key and a store that still holds the old seats — and the
// merchant's retry would otherwise stop at the recorded source and leave it that
// way forever. Here the retry finishes it from the record the mint left, which
// is the whole of what the event's effect needs: the seats are in it — and what
// it writes into is the subscription the store holds, never a fresh one, so a
// state the store has moved on to since is not thrown away by the repair.
//
// A purchase has no such half any more: it mints nothing, so there is no
// recorded source for a retry to stop at, and what the retry re-applies is the
// grant itself (D2196).
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
	// redelivery of an event must not resurrect the subscription a later
	// cancellation or a later term has already moved past. It excludes the same
	// thing it excludes there, an event strictly older than the state, and not a
	// tie: an event tied with the stored instant is one the merchant meant.
	if held && e.OccurredAt.Before(sub.At) {
		return nil
	}
	if e.Type != EventSeatChange {
		return nil
	}
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

// purchased grants the entitlement a purchase bought, records the subscription
// the merchant's later events are about, and mails the buyer a link. **It mints
// nothing** (D2196).
//
// A **renewal** and a **proration** reach this function too, because Paddle
// states them as a transaction completed like any other and the difference is
// only in what is behind the event: a first payment's transaction is the one
// P4.1 created, and a renewal's is one Paddle made itself against the
// subscription the first one began. What a renewal does here is add the term the
// money bought to the licence that is already there (`renewedLicence`) and mail
// nothing — the customer has been told about that licence once already, and a
// second notice for every month of a subscription is mail nobody asked for.
//
// The key is minted at the binding and not at the sale, and the reason is
// ordering rather than taste: a customer pays before `fugaro setup` has created
// anything (F25), so a purchase that minted would mint a key bound to no project
// — which every verifier refuses (TN2) — and mail it to an address nobody has
// installed anything at. What the purchase leaves is the grant: the row, the
// seats and the term, running from the payment's own instant (D2197), and the
// customer's own act of binding it to a project is what issues the key
// (workspaces.go's `bindEntitlement`).
//
// The mail carries the portal's address and no key (F3, D2182): there is no key
// yet, and the portal is where the customer binds the licence and finds the key
// it mints — and, later, where they find it again.
//
// The record read first is F15's other half: what was bought is the checkout's
// own record and not the notification's word for it, and a purchase whose
// transaction this API never opened is the case that was parked before this
// function was reached (`admitted`), so reaching here without one means the
// record went away between the two reads — a `500`, and the merchant sends it
// again.
//
// Enterprise is refused before any of that, and it is refused rather than sold
// the Team row: §5.5's keys are invoiced directly by the entity and minted from
// the admin page, so a deployment whose paid plan names the Enterprise tier is
// one whose checkout would price a tier that is never self-serve — and the
// customer would be granted a licence nobody sold them (D2141).
func (s *Service) purchased(ctx context.Context, e Event) error {
	if s.cfg.Paid.Tier != licence.TierTeam {
		return fmt.Errorf("the checkout sells Team keys and this deployment's paid plan names the %s tier, and an Enterprise key is invoiced directly and minted from the admin page (D2141, §5.5)", s.cfg.Paid.Tier)
	}
	if e.Organisation == "" || e.WorkspaceID == "" || e.Seats < 1 || e.TermDays < 1 {
		return fmt.Errorf("%w: the purchase %s", ErrNoClaims, e.ID)
	}
	bought, found, err := s.boughtBy(ctx, e)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: the purchase %s", ErrNoTransaction, e.ID)
	}
	// The row is the one the term is sold under, and a term the checkout does not
	// sell is refused rather than sold the nearest one: a licence granted for a
	// period nobody sells is one the customer paid for with whatever they paid
	// (D442). A **renewal** states its own period like any other transaction, and
	// a period Paddle computed — a proration's — is read against the row the
	// subscription was bought on, because the customer bought that row and this
	// event is that purchase continued.
	policy, sold := checkoutPolicy(e.TermDays)
	days := e.TermDays
	if !sold {
		stored, held, err := s.store.Subscription(ctx, e.Subscription)
		if err != nil {
			return err
		}
		if !held {
			return fmt.Errorf("%w: the purchase %s names a term the checkout does not sell (D442)", ErrNoClaims, e.ID)
		}
		days = stored.TermDays
		policy, sold = checkoutPolicy(days)
		if !sold {
			return fmt.Errorf("%w: the purchase %s names a term the checkout does not sell and its subscription's own is not one either (D442)", ErrNoClaims, e.ID)
		}
	}
	to, deliverable := address(bought.Email)
	if !deliverable {
		to, deliverable = address(e.Email)
	}
	if !deliverable {
		return fmt.Errorf("%w: the purchase %s", ErrNoBuyer, e.ID)
	}
	end := e.OccurredAt.Add(time.Duration(days) * 24 * time.Hour)
	// The workspace is the checkout's own and never the notification's: it is the
	// portal workspace the money was for (P4.1's record), and the event's
	// `custom_data` is the same name stated a second time by the body this
	// service signed for. A grant written against the notification's word alone
	// would be a grant for whatever a body named.
	workspace := bought.WorkspaceID
	if workspace == "" {
		return fmt.Errorf("%w: the purchase %s", ErrNoClaims, e.ID)
	}
	// A first purchase is the one that grants; a renewal and a proration are the
	// same licence continued, and what they move is the term of the grant that is
	// already there (`renewedLicence`).
	licence := bought.EntitlementID
	if bought.First {
		entitlement, err := s.grantEntitlement(ctx, entitlementGrant{
			At:             e.OccurredAt,
			OrganisationID: bought.OrganisationID,
			WorkspaceID:    &workspace,
			Source:         entitlementSourcePurchase,
			Row:            policy,
			Seats:          e.Seats,
			StartsAt:       e.OccurredAt,
			EndsAt:         end,
		})
		if err != nil {
			return err
		}
		licence = entitlement.ID
	}
	// The subscription is the store's own record of what the merchant bills: the
	// renewal's window, the seat change's term and the cancellation's own state
	// are all read off it, and Paddle's subscription id is what every later
	// event is matched to a licence by. A renewal moves its end forward and never
	// back — an event redelivered out of order must not take a term off a
	// customer who has paid for it — while a first purchase writes it.
	sub, held, err := s.store.Subscription(ctx, e.Subscription)
	if err != nil {
		return err
	}
	if !held {
		sub = Subscription{ID: e.Subscription}
	}
	sub.Organisation = bought.OrganisationID
	// The workspace the store's own record names is the project id the event
	// carries where it carries one, and the portal's workspace otherwise: a
	// workspace a customer has not bound yet has no project, and D2192's second
	// sale for it is refused by the portal workspace's name just as well
	// (checkout.go's `refuseSubscribedWorkspace` reads both spellings).
	sub.WorkspaceID = e.WorkspaceID
	sub.Seats = e.Seats
	// The term the subscription stands for is the row it was bought on and never
	// the period this one event stated: a period Paddle computed is not a row
	// anybody sells, and a record left holding it is one whose every later event
	// — a seat change, a renewal — is refused for good (D442).
	sub.TermDays = days
	sub.Email = to
	sub.At = e.OccurredAt
	if end.After(sub.Expires) {
		sub.Expires = end
	}
	if err := s.store.SaveSubscription(ctx, sub); err != nil {
		return err
	}
	if err := s.recordOrigin(ctx, paddleOrigin{
		SubscriptionID: e.Subscription,
		TransactionID:  e.Transaction,
		EntitlementID:  licence,
		OrganisationID: bought.OrganisationID,
		WorkspaceID:    workspace,
		At:             e.OccurredAt,
	}); err != nil {
		return err
	}
	if bought.First {
		// The checkout's own record names the licence its money bought, so a
		// refund of this transaction resolves to that licence months later —
		// when the workspace's current one is a licence this money never paid
		// for, or when there is none at all (F16, F29).
		if err := s.recordBought(ctx, bought.TransactionID, licence); err != nil {
			return err
		}
	}
	if !bought.First {
		return s.renewedLicence(ctx, e, licence, end)
	}
	return s.mail.Send(ctx, purchaseMail(to, s.portalLink()))
}

// renewedLicence is a renewal's and a proration's effect: the licence the
// customer holds, with the term the money bought added to it (D2196, D2197).
//
// Nothing is granted and nothing is minted. A renewal buys another term of the
// licence that is already there — the entitlement is the grant and the term is
// what the money moved — and the key the customer's installation runs was minted
// at the binding and belongs to that same grant. A renewal that granted a second
// entitlement would supersede the first (D2204's one active grant per workspace)
// and leave the key the customer holds attached to a grant the portal no longer
// shows as theirs.
//
// The end moves forward and never back: a renewal redelivered out of order, or
// one whose event is older than a term already granted, must not take a term off
// a customer who has paid for it. That is the same direction `purchased` writes
// the subscription's own end in, and the two are the same rule at two records.
func (s *Service) renewedLicence(ctx context.Context, e Event, licence string, end time.Time) error {
	if licence == "" {
		// The subscription is this API's — that is what admitted the event — and
		// a subscription whose origin names no grant is a record this service did
		// not write. It is a `500` and the merchant's retry rather than a silent
		// drop, because a renewal nothing applies is a customer who has paid for a
		// term they do not get.
		return fmt.Errorf("%w: the renewal %s belongs to the subscription %s, whose record names no licence", ErrNoTransaction, e.ID, e.Subscription)
	}
	held, present, err := getRecord[WireEntitlement](ctx, s.docs, entitlementsCollection, licence)
	if err != nil {
		return err
	}
	if !present {
		return fmt.Errorf("%w: the renewal %s names the licence %s, which the store does not hold", ErrNoTransaction, e.ID, licence)
	}
	if !end.After(held.EndsAt) {
		return nil
	}
	held.EndsAt = end
	doc, err := storedDoc(held)
	if err != nil {
		return err
	}
	if err := s.docs.Put(ctx, entitlementsCollection, held.ID, doc); err != nil {
		return fmt.Errorf("keyservice: the licence %s was renewed and its term could not be moved: %w", held.ID, err)
	}
	return nil
}

// boughtBy is what one purchase event is read against: the transaction this API
// created for it, or — for a renewal or a proration, which Paddle creates itself
// (F15) — the subscription that transaction began.
type boughtBy struct {
	TransactionID  string
	OrganisationID string
	WorkspaceID    string
	Email          string
	// EntitlementID is the licence the subscription was begun by, which is what a
	// renewal's and a proration's term is added to. It is empty for a first
	// purchase, because the grant is what that event makes.
	EntitlementID string
	// First is whether this event is the subscription's first payment, which is
	// the one that grants the licence and mails the customer.
	First bool
}

// boughtBy reads the two records a purchase is admitted and read against, in the
// order F15 names them: the checkout's own record by the event's transaction,
// and the subscription's origin by the event's subscription.
func (s *Service) boughtBy(ctx context.Context, e Event) (boughtBy, bool, error) {
	if e.Transaction != "" {
		record, held, err := getRecord[checkoutTransaction](ctx, s.docs, checkoutTransactionsCollection, e.Transaction)
		if err != nil {
			return boughtBy{}, false, err
		}
		if held {
			return boughtBy{
				TransactionID:  record.ID,
				OrganisationID: record.OrganisationID,
				WorkspaceID:    record.WorkspaceID,
				Email:          record.Email,
				First:          true,
			}, true, nil
		}
	}
	if e.Subscription == "" {
		return boughtBy{}, false, nil
	}
	origin, held, err := s.origin(ctx, e.Subscription)
	if err != nil {
		return boughtBy{}, false, err
	}
	if !held {
		return boughtBy{}, false, nil
	}
	// The address a renewal is mailed to is the subscription's own, which the
	// purchase wrote and which is the only place it still is months later
	// (§5.1).
	email := ""
	if stored, found, err := s.store.Subscription(ctx, e.Subscription); err != nil {
		return boughtBy{}, false, err
	} else if found {
		email = stored.Email
	}
	return boughtBy{
		TransactionID:  origin.TransactionID,
		OrganisationID: origin.OrganisationID,
		WorkspaceID:    origin.WorkspaceID,
		Email:          email,
		EntitlementID:  origin.EntitlementID,
	}, true, nil
}

// portalLink is where a notice sends the customer: the portal's own workspace
// list, on the origin this intake answers on. The SPA and this API are one host
// (F21 — Hosting serves the page from the origin it rewrites `/api/**` from), so
// the origin is already configuration and no second field has to name it.
func (s *Service) portalLink() string {
	return strings.TrimSuffix(s.cfg.URL, "/") + portalWorkspacesPath
}

// portalWorkspacesPath is the portal's own route for the list of a customer's
// workspaces, which is where a licence that has just been bought is bound.
const portalWorkspacesPath = "/workspaces"

// licenceSubject is the purchase notice's subject. It is a constant of this
// package's for the reason mail.go's two are: a buyer's own text must not be able
// to reach a mail header (§7.2).
const licenceSubject = "Your Fugaro licence is ready"

// purchaseMail is the notice one purchase sends: where the licence is, and how
// to get the key it issues. **It carries no key** (F3, D2182) — there is none
// yet, because the key is minted at the binding — and that is also why it names
// the portal rather than attaching anything: the portal is where the customer
// binds the licence, and where they find the key again after they have.
func purchaseMail(to, link string) Message {
	return Message{
		To:      to,
		Subject: licenceSubject,
		Body: "Your Fugaro licence is ready.\n\n" +
			"Bind it to your Firebase project in the portal, and your key is issued there:\n" +
			link + "\n\n" +
			"No key is sent by mail. The portal is where it is issued, and where you can find it again.\n",
	}
}

// entitlementEnd is the end of the term an issuance stands for: the instant its
// key was minted plus the term that issuance is for — the term a purchase
// bought, the whole days a renewal covers, or the row's own term where the
// request named none. It is the subscription's own end and never the key's,
// because D2202 caps a connected row's key at thirty days while the entitlement
// the customer bought runs the term they bought — a subscription stored with the
// key's end would open an annual renewal's window at the purchase itself and
// move the term on by a month a year.
//
// The term is whole days and the end a caller may have named is an instant, so
// it is read as a lower bound and not as the end itself: the minter rounds the
// days up (store.go's field comment), which puts this answer at the entitlement's
// own end or up to a day past it and never before it. The direction is the
// package's own — days rounded up are days the customer keeps — and the exact
// instant is what the record's own `Expires` states for the key it minted.
//
// A record with no term at all is the fallback and only that: nothing in this
// service mints one (the minter writes the term it resolved, the row's own
// included), so it is a record the store already holds from a deployment that
// wrote the field's absence, and the key's end is the only length such a record
// states.
func entitlementEnd(record Record) time.Time {
	if record.TermDays < 1 {
		return record.Expires
	}
	return record.Issued.Add(time.Duration(record.TermDays) * 24 * time.Hour)
}

// seatChanged reissues inside the term the customer already paid for: `seats`
// is a claim in the key, and the term is not (§5.3). The new key covers what is
// left of that term — which for a connected row's key is D2202's thirty days
// from the reissue, and never a term taken off the subscription — so the seats
// move, the key is as long as any other key of its row, and the subscription's
// own end is where it was.
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
	// The address is the subscription's own and not the event's: Paddle's
	// notification carries no address at all, and the one the licence was bought
	// with is the one the reissue is mailed to (§5.1). The event's own word for
	// it stands in where the record carries none.
	to, deliverable := address(sub.Email)
	if !deliverable {
		to, deliverable = address(e.Email)
	}
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

// withdrawn writes §8's entries for the keys of a licence the merchant has given
// the money back for (D2131). It is the whole of the event's effect: those keys
// stop being renewable and the customer's own job reads them as revoked, and
// nothing about the subscription moves — a refund is not a cancellation, and the
// merchant's cancellation is the event that stops the reissue (§5.3).
//
// **The event resolves to the entitlement, and never to a key** (F16, F29).
// Paddle sends no key id and never has, so the shape that read one was a shape
// no real notification could drive — and under D2196 there is nothing for it to
// name at the purchase anyway: the key is minted at the binding, so the key a
// customer's installation runs is the one bound to the entitlement, and the
// entitlement is what the transaction and the subscription both resolve to.
//
// The walk is over one **licence** and nothing else, and that is the owner's
// ruling of 2026-11-13 (D2147). Three steps reach a key of it and the walk takes
// all three: the entitlement's own bindings, which are the keys D2196 mints for
// it (`bindingKeys`); the subscription every other issuance of the licence
// stamps on its record — the key a **seat change** reissued, and the merchant's
// own renewal, which is §5.1's ordinary route — and the record's own word for
// the key it replaced, `rotation:<the key it replaces>` (see `rotationSource`),
// which the `/renew` endpoint's rotation carries beside the subscription.
//
// The subscription is the step that matters for a licence bought before D2196
// and for every reissue after it, and a seat change's key belongs to it like any
// other: a seat change mints inside the term the customer already paid for, so
// it is the same licence and not a second one — `seatChanged` says so, and the
// term is the subscription's while only the seats are in the key (§5.3). Reading
// the source as what tells one licence from another was the tempting shortcut
// and it is wrong: the key a customer's installation runs is usually a seat
// change's or a renewal's, so a withdrawal that stopped at the binding would
// leave the licence renewing after the money went back.
//
// Only a key whose record names a subscription is walked by that step, which is
// what keeps a hand-minted key — the admin page's, the mint tool's, a trial's —
// from being read as the same licence as every other key whose record names
// none.
//
// It is still not a cancellation: nothing here ends the subscription's ability
// to mint, so a seat change or a trigger inside a term's window mints a fresh
// key the moment after this runs, and a withdrawal of **that** key is the
// merchant's next event. That is the shape §5.3 and §8 state between them — the
// list withdraws keys, the cancellation ends the subscription — and it puts the
// pairing on the merchant's own events. Making the store's own list stop a
// reissue would mean recording which key a subscription currently holds, which
// is a decision for the owner rather than a repair to make here.
//
// Nothing is recorded here, and that is deliberate: §5.2's idempotency is a
// record of an **issuance**, and a withdrawal mints none. Two things make a
// redelivery harmless anyway — the event's own state is what says it was
// processed, and the list itself refuses to change an entry it holds, so a
// refund delivered twice is one revocation from the first instant either way
// (K2.2). The walk is idempotent for the same reason: every key it reaches is
// revoked against the list that ignores an entry it already holds, and the
// visited set keeps a chain the store happens to hold in a loop from being
// walked for ever.
//
// A refund of a licence that has no key yet withdraws nothing and is not a
// failure: D2196 mints the key at the binding, so a customer who paid and never
// bound anything holds nothing to take back, and the entitlement is superseded
// where it stands.
func (s *Service) withdrawn(ctx context.Context, e Event) error {
	reason := ReasonRefund
	if e.Type == EventChargeback {
		reason = ReasonChargeback
	}
	entitlement, licensed, err := s.entitlementFor(ctx, e)
	if err != nil {
		return err
	}
	records, err := s.store.Issuances(ctx)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	walk := []string{}
	revoke := func(keyID string) error {
		if seen[keyID] {
			return nil
		}
		seen[keyID] = true
		walk = append(walk, keyID)
		return s.store.Revoke(ctx, Revocation{KeyID: keyID, Reason: reason, At: e.OccurredAt})
	}
	// The entitlement's own keys first, because they are the keys this licence
	// actually has: everything below reaches a key through the subscription, and
	// the subscription of a licence bought after D2196 names no key at all until
	// a seat change or a renewal mints one.
	subscription := e.Subscription
	if licensed {
		keys, err := s.bindingKeys(ctx, entitlement.ID)
		if err != nil {
			return err
		}
		for _, keyID := range keys {
			if err := revoke(keyID); err != nil {
				return err
			}
		}
		if subscription == "" {
			// A refund against a transaction alone names no subscription, and the
			// licence's own is where its other keys are: the origin record a
			// purchase wrote is keyed by the subscription and holds the
			// entitlement, so the step back is a query on the entitlement's id.
			subscription, err = s.licenceSubscription(ctx, entitlement.ID)
			if err != nil {
				return err
			}
		}
	}
	// successors indexes the rotations the store holds by the key they replace,
	// so the step from a key to what the endpoint minted from it is a lookup
	// rather than a scan.
	successors := map[string][]string{}
	for _, record := range records {
		if from, rotation := strings.CutPrefix(record.Source, rotationPrefix); rotation {
			successors[from] = append(successors[from], record.KeyID)
		}
	}
	// Every other issuance of the licence's own subscription joins the walk,
	// whatever minted it, and each of those is then followed through the rotations
	// it has. The empty subscription is not a licence: an unsold key names none,
	// so matching on "" would withdraw every hand-minted key there is.
	if subscription != "" {
		for _, record := range records {
			if record.Subscription != subscription {
				continue
			}
			if err := revoke(record.KeyID); err != nil {
				return err
			}
		}
	}
	for len(walk) > 0 {
		keyID := walk[len(walk)-1]
		walk = walk[:len(walk)-1]
		for _, next := range successors[keyID] {
			if err := revoke(next); err != nil {
				return err
			}
		}
	}
	return nil
}

// entitlementFor resolves the entitlement one event is about: the licence the
// money belongs to (F16, F29).
//
// Two steps, and either alone is enough. The event's **subscription** is the
// licence where this portal began it: the origin record a purchase wrote is
// keyed by Paddle's subscription id and names the entitlement. The event's
// **transaction** is the other half: an adjustment against a one-off transaction
// names no subscription, and what that transaction bought is written on the
// checkout's own record, under Paddle's transaction id.
//
// Both steps answer the licence **the money belongs to** and neither asks
// whether it is still the workspace's current one: a licence a later sale
// superseded is still the licence this transaction paid for, and revoking the
// current one instead would withdraw a licence this money never bought (D2204's
// one licence per workspace is about which licence a workspace is served by, not
// about which one a refund is about). An event whose transaction and
// subscription resolve to no licence at all answers false — nothing of this
// portal's was bought with that money.
func (s *Service) entitlementFor(ctx context.Context, e Event) (WireEntitlement, bool, error) {
	if e.Subscription != "" {
		origin, held, err := s.origin(ctx, e.Subscription)
		if err != nil {
			return WireEntitlement{}, false, err
		}
		if held && origin.EntitlementID != "" {
			return getRecord[WireEntitlement](ctx, s.docs, entitlementsCollection, origin.EntitlementID)
		}
	}
	if e.Transaction == "" {
		return WireEntitlement{}, false, nil
	}
	record, held, err := getRecord[checkoutTransaction](ctx, s.docs, checkoutTransactionsCollection, e.Transaction)
	if err != nil {
		return WireEntitlement{}, false, err
	}
	if !held || record.EntitlementID == "" {
		return WireEntitlement{}, false, nil
	}
	return getRecord[WireEntitlement](ctx, s.docs, entitlementsCollection, record.EntitlementID)
}

// Renew mints the next key for one subscription when T26's window is open: 30
// days before the term it follows ends, or half that term where half is shorter
// than the lead, which every monthly row's is, its `issued` on the day the
// service runs, its `expires` at the end of the key it mints — D2202's thirty
// days for a row whose key checks the server, however long the term behind it
// runs.
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
	source := renewalPrefix + sub.ID + ":" + sub.Expires.UTC().Format(time.RFC3339)
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
// store did not take: the record names the term it was minted for, so the state
// the customer paid for is written from it — and it is the term that is written,
// not the end of the key, which D2202 caps at thirty days below it.
// The subscription's own end is the test — it is what the window and the source
// are both computed from — and a state the term has already moved past is left
// alone, so a stale source cannot pull it back.
//
// `At` is left alone here for the reason Renew leaves it alone: it is the
// merchant's timeline and not the service's, and the repair's own instant is not
// an event on it.
func (s *Service) renewed(ctx context.Context, sub Subscription, record Record) error {
	end := entitlementEnd(record)
	if !sub.Expires.Before(end) {
		return nil
	}
	sub.Expires = end
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
// The subscription and the workspace are the merchant's own, and they are written
// into the record and the key's state because this is the only path that mints
// for a subscription and the only one that knows either: the record's
// subscription is what lets a renewal tell a licence still being paid for from
// one that is not, and the state's workspace is the one the successor of that
// renewal is minted into and a request's word for it is not proof (TN2, K2.3).
// A key minted without the pair is a key nothing can renew — refused for the
// subscription by §7's gate and minted into no workspace at all.
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
	// The hash written is the hash the key carries, which is the request's own
	// when the answer holds no secret: D2200's reissue keeps the entitlement's
	// existing hash and answers no new secret at all, so a state hashed from the
	// empty answer would file every reissued key under `digest("")` — a key whose
	// holder can never renew it, holding a secret the store does not know. The
	// state is still written for a reissue, because a key state is keyed by the
	// key's own id and the reissue has a new one: skipping it would leave a key
	// nothing can renew just the same.
	secretHash := req.RenewalSecretHash
	if answer.RenewalSecret != "" {
		secretHash = digest(answer.RenewalSecret)
	}
	// The workspace goes in with the hash, and it is the request's own: that is
	// the workspace the key was minted into, and `/renew` mints the successor
	// into it. It has to be here, because D2202 makes a connected key thirty days
	// of the term it was sold for and `/renew` is the door that carries the
	// entitlement from one of those to the next — a purchased key whose state
	// names no workspace is refused there, and an annual purchase would then be
	// dark from the end of its first key until the merchant's own renewal, which
	// opens RenewalLead before the subscription's end and so on day 335 of a year
	// (renew.go's `renewable`).
	//
	// What naming it costs is nothing on this path: the subscription the key was
	// sold under is written on the record beside this state (`paid`), and `/renew`
	// reads the record before it mints — a key whose subscription the merchant has
	// since cancelled, or let end, is answered its own state and handed nothing
	// (D2148, `renew`) — so neither half is a way past the other.
	if err := s.store.SaveKeyState(ctx, KeyState{
		KeyID: answer.Record.KeyID,
		// The workspace the request bound the key to, which is the one the
		// checkout's own data named (§5.1, TN2): a state without it is one no
		// renewal can serve, because the successor is minted into the key's own
		// workspace and a request's word for it is not proof (K2.3).
		WorkspaceID:       req.WorkspaceID,
		RenewalSecretHash: secretHash,
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
//
// A reissue that keeps the entitlement's existing hash is mailed its key alone
// (D2200): the minter answered no secret, because there is no new one to hand
// out and the holder still has the one this service sent them, so the second
// line is left out rather than mailed empty — a bundle whose second half is
// nothing asks a customer to keep a blank line as the thing they renew with.
func paidKeyMail(to, key, secret string) Message {
	if secret == "" {
		return Message{
			To:      to,
			Subject: keySubject,
			Body: "Your Fugaro licence key:\n\n" + key + "\n\n" +
				"This key replaces one you already hold, and the renewal secret you were sent with it is unchanged.\n" +
				"Install it in the console, or keep it for the CLI.\n",
		}
	}
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
