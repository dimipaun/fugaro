// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The ledger's storage: the events as they were received, what became of each,
// and the readable ledger derived from them (portal plan P4.2; F16, F15).
//
// The order the spec asks for is the order below: **verify, store, then
// process**. The store step is not an audit trail bolted on afterwards — it is
// what makes processing safe. A verified body is written under Paddle's own
// event id before anything is done with it, so a redelivery finds the record of
// the first one and the state it left, and a body that does not decode is kept
// too, under the digest of itself, because a body this service cannot read is
// exactly the one operations has to be able to look at.
//
// The id is Paddle's and never this service's, and that is the security half of
// it (F16's round 2): a stranger who could name the key a body is stored under
// could pre-store a forged body under a real event id, and the real delivery
// would then be read as a redelivery of the forgery. The signature is checked
// first, so nothing reaches this file unverified, and an undecodable body is
// keyed by its own digest rather than by an id it does not carry.
//
// Nothing here derives a ledger entry from anything but a stored event (P4.2's
// TestLedgerDerivesFromStoredEvents): a figure in a report is always walkable
// back to the body it came from, and the body is the one Paddle signed.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"time"
)

const (
	// paddleEventsCollection holds one document per verified delivery, keyed by
	// Paddle's event id or, for a body that does not decode, by its digest.
	paddleEventsCollection = "paddle_events"

	// paddleSubscriptionsCollection holds one document per Paddle subscription
	// this API began, keyed by Paddle's subscription id: what F15 admits a
	// renewal or a proration by. Paddle creates those itself, so the only thing
	// that tells one of them from a subscription a stranger started is the
	// transaction the subscription began with — and that transaction is the one
	// the API created at the checkout (P4.1).
	paddleSubscriptionsCollection = "paddle_subscriptions"
)

// What the portal has made of one stored event (wire_ledger.go's State).
const (
	// ledgerStored is a verified body that has not been read yet, which is what
	// a delivery that failed mid-processing leaves behind: the merchant's retry
	// is answered from the state, and operations can see the body either way.
	ledgerStored = "stored"
	// ledgerUndecodable is a verified body this service could not read. It is
	// answered 400 so the merchant sends it again, and it is kept so that a
	// person can see what arrived.
	ledgerUndecodable = "undecodable"
	// ledgerProcessed is an event whose effect the store holds. A redelivery of
	// one is answered without applying it a second time, which is D2192's
	// idempotency and the whole of it for the events that mint nothing.
	ledgerProcessed = "processed"
	// ledgerParked is an event whose transaction and subscription both began
	// with something this API did not create (F15). It is kept and left for
	// operations, which attaches or dismisses it (P5.1).
	ledgerParked = "parked"
	// ledgerDismissed is operations ending a parked event.
	ledgerDismissed = "dismissed"
)

// eventDigest is the id an undecodable body is stored under: its own digest, so
// that the same bytes delivered twice are one record and no id of Paddle's is
// ever claimed by a body that did not carry one (F16).
func eventDigest(body []byte) string {
	sum := sha256.Sum256(body)
	return "digest:" + hex.EncodeToString(sum[:])
}

// keepReceived writes one verified delivery as it arrived, with the header that
// proved it, and answers whether the store already held a processed event under
// that id.
//
// The body is written on every delivery, including a redelivery, because the
// spec says the event is stored as received: what a redelivery must not do is
// unmake what the first one did, so the state the store already holds is carried
// over and never reset. `processed` is the answer a caller acts on — the event
// is not applied twice — and the second answer is the state a body arrived in,
// which is what lets the caller tell a fresh delivery from a retry.
func (s *Service) keepReceived(ctx context.Context, received WireLedgerEvent) (WireLedgerEvent, error) {
	held, present, err := s.storedEvent(ctx, received.ID)
	if err != nil {
		return WireLedgerEvent{}, err
	}
	if present {
		received.State = held.State
		received.EntitlementID = held.EntitlementID
	}
	if received.State == "" {
		received.State = ledgerStored
	}
	doc, err := storedDoc(received)
	if err != nil {
		return WireLedgerEvent{}, err
	}
	if err := s.docs.Put(ctx, paddleEventsCollection, received.ID, doc); err != nil {
		return WireLedgerEvent{}, fmt.Errorf("keyservice: the event %s was verified and could not be stored: %w", received.ID, err)
	}
	return received, nil
}

// markEvent moves one stored event to what the portal made of it. The read is
// not repeated here: a caller marks an event it has just written, and the write
// is the whole of what the state is.
func (s *Service) markEvent(ctx context.Context, id, state string, entitlementID *string) error {
	held, present, err := s.storedEvent(ctx, id)
	if err != nil {
		return err
	}
	if !present {
		return fmt.Errorf("keyservice: the event %s is not one the store holds", id)
	}
	held.State = state
	if entitlementID != nil {
		held.EntitlementID = entitlementID
	}
	doc, err := storedDoc(held)
	if err != nil {
		return err
	}
	return s.docs.Put(ctx, paddleEventsCollection, id, doc)
}

// markEventFrom moves one stored event out of the state a caller decided it in,
// and it reads that state and writes the new one in **one transaction**.
//
// Whether an event may move is a question about the state the store holds, and
// a read answered outside the write that acts on it is a state something else
// can move in between: two resolutions of one parked event (P5.1) both read
// `parked`, both write, and the second overwrites the first — the decision the
// first operator made is gone and only the log says it ever happened. The read
// here is part of what the transaction read, so a competing resolution is
// either seen as the state it left or a conflict that runs this again on it.
//
// It is `markEvent`'s act with the state as a precondition rather than beside
// it: what a caller knows about the event is what it read, and the state the
// event is in now is the store's to answer. A caller that read the event only
// to write it back uses `markEvent`; a caller whose whole decision is "it is
// still parked" uses this.
//
// What it refuses with is the two answers a reader of a stored event already
// branches on — the id the store does not hold, and the state the decision was
// not made in — so a door does not have to tell them apart from a store that
// failed.
func (s *Service) markEventFrom(ctx context.Context, id, from, to string, entitlementID *string) (WireLedgerEvent, error) {
	var written WireLedgerEvent
	err := s.docs.Update(ctx, func(ctx context.Context, tx Tx) error {
		held, present, err := getRecord[WireLedgerEvent](ctx, tx, paddleEventsCollection, id)
		if err != nil {
			return err
		}
		if !present {
			return errOperationsNoEvent
		}
		if held.State != from {
			return errOperationsEventAnswered
		}
		held.State = to
		if entitlementID != nil {
			held.EntitlementID = entitlementID
		}
		if err := putRecord(ctx, tx, paddleEventsCollection, id, held); err != nil {
			return err
		}
		written = held
		return nil
	})
	if err != nil {
		return WireLedgerEvent{}, err
	}
	return written, nil
}

// storedEvent reads one stored event back.
func (s *Service) storedEvent(ctx context.Context, id string) (WireLedgerEvent, bool, error) {
	return getRecord[WireLedgerEvent](ctx, s.docs, paddleEventsCollection, id)
}

// eventsReceived is every stored event, oldest first: the order the ledger is
// read in, and the order a person reading the sales record reads it in.
//
// The order is the store's own and the sort is stable over it, so two events
// with one instant keep the order they were written in rather than swapping
// between two reads of the same list.
func (s *Service) eventsReceived(ctx context.Context) ([]WireLedgerEvent, error) {
	held, err := recordsWhere[WireLedgerEvent](ctx, s.docs, paddleEventsCollection, Where{})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(held, func(i, j int) bool {
		return held[i].ReceivedAt.Before(held[j].ReceivedAt)
	})
	return held, nil
}

// paddleOrigin is what one Paddle subscription began with: the transaction this
// API created for it, and the entitlement that transaction bought. It is
// written when a purchase is processed, and it is what F15's admission rule
// reads a renewal or a proration by — those are Paddle's own acts on a
// subscription the API began, and nothing else about them says so.
type paddleOrigin struct {
	SubscriptionID string    `json:"subscription_id"`
	TransactionID  string    `json:"transaction_id"`
	EntitlementID  string    `json:"entitlement_id"`
	OrganisationID string    `json:"organisation_id"`
	WorkspaceID    string    `json:"workspace_id"`
	At             time.Time `json:"at"`
}

// recordOrigin writes what one subscription began with. It is an upsert under
// the subscription's own id: a redelivery of the purchase writes the same
// record, and a second purchase that produced a second subscription is a second
// record.
func (s *Service) recordOrigin(ctx context.Context, origin paddleOrigin) error {
	doc, err := storedDoc(origin)
	if err != nil {
		return err
	}
	if err := s.docs.Put(ctx, paddleSubscriptionsCollection, origin.SubscriptionID, doc); err != nil {
		return fmt.Errorf("keyservice: the subscription %s was paid for and could not be recorded: %w", origin.SubscriptionID, err)
	}
	return nil
}

// origin reads what one subscription began with.
func (s *Service) origin(ctx context.Context, subscriptionID string) (paddleOrigin, bool, error) {
	return getRecord[paddleOrigin](ctx, s.docs, paddleSubscriptionsCollection, subscriptionID)
}

// licenceSubscription is the merchant's subscription one licence was bought
// under, and the empty string for a licence no merchant event sold — a free
// seat, a trial, an operations grant.
//
// It is the reverse of what a purchase's origin record states, and it is read
// because the key a binding mints has to name the subscription its renewal is
// served by: a key whose record names none is one no renewal can serve. The read
// is a query on the entitlement's own id, and it answers one record at most — a
// subscription's origin is written once, and rewritten only by that
// subscription's own later events.
func (s *Service) licenceSubscription(ctx context.Context, entitlementID string) (string, error) {
	origins, err := recordsWhere[paddleOrigin](ctx, s.docs, paddleSubscriptionsCollection,
		Where{Field: "entitlement_id", Value: entitlementID})
	if err != nil {
		return "", err
	}
	for _, origin := range origins {
		return origin.SubscriptionID, nil
	}
	return "", nil
}

// ledgerEntries is the readable ledger: one line per stored event, derived from
// the event itself and from nothing else (P5.2 reads this).
//
// Two kinds of stored event make no line. An event that is **not a sale** moves
// no money — a cancellation, a body that did not decode — and an event this
// portal did **not apply** is not a sale of this portal's: a parked event is
// Paddle telling this service about money somebody else took (F15), and a body
// whose processing failed is one no figure has been read out of yet. Counting
// either would put a stranger's revenue in this portal's report.
//
// What is left is every purchase, renewal, refund, chargeback and adjustment the
// portal applied, with the money signed, so that a report's total is the sum of
// the lines.
func (s *Service) ledgerEntries(ctx context.Context) ([]WireLedgerEntry, error) {
	events, err := s.eventsReceived(ctx)
	if err != nil {
		return nil, err
	}
	entries := make([]WireLedgerEntry, 0, len(events))
	for _, held := range events {
		if held.State != ledgerProcessed {
			continue
		}
		if entry, sale := ledgerEntryOf(held); sale {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

// ledgerEntryOf reads one stored event as one line of the ledger, and answers
// false for an event that is not a sale.
//
// The body is read again here rather than a figure being kept beside it, and
// that is the point of the whole shape: what a report says about a sale is what
// Paddle's own signed body said about it, and a figure stored beside the body
// could drift from the body it came from. A body that no longer decodes makes
// no line — it is the undecodable case, and it is visible as such in the events
// themselves.
func ledgerEntryOf(held WireLedgerEvent) (WireLedgerEntry, bool) {
	n, err := decodeNotification([]byte(held.Body))
	if err != nil {
		return WireLedgerEntry{}, false
	}
	kind, sale := ledgerKind(n)
	if !sale {
		return WireLedgerEntry{}, false
	}
	entry := WireLedgerEntry{
		ID:   held.ID,
		At:   n.OccurredAt,
		Kind: kind,
		// The channel is direct: a reseller's sale reaches this portal as an
		// operations grant and not as a merchant event (P6.2), so every line a
		// merchant event makes is one the customer bought from Fugaro.
		Channel: "direct",
		Seats:   n.seats(),
	}
	if held.EntitlementID != nil {
		entry.EntitlementID = held.EntitlementID
	}
	entry.AmountMicros = n.grandTotalMicros()
	entry.Currency = n.Data.CurrencyCode
	if workspace := n.custom("workspace_id"); workspace != "" {
		entry.WorkspaceID = &workspace
	}
	entry.OrganisationID = n.custom("org_id")
	return entry, true
}
