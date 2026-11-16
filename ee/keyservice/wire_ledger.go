// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The ledger (portal plan P4.2; spec §Paddle webhooks, F16).
//
// Two shapes, and the difference between them is the spec's own sentence
// "store each event exactly as received, with a readable sales ledger derived
// from it": the event is Paddle's, kept as it arrived and never edited, and the
// entry is the portal's reading of it. Nothing derives an entry from anything
// but a stored event, so a figure in a report can always be walked back to the
// body it came from (P4.2's TestLedgerDerivesFromStoredEvents, P5.2).
//
// The event is stored before it is processed, which is why it has a state of its
// own: what the portal has done with an event is a fact about the event, and a
// retry that arrives while the first one is still being read finds it already
// there (idempotency by event id, D2192/F16).

import "time"

// WireLedgerEvent is one Paddle notification as it was received.
type WireLedgerEvent struct {
	// ID is Paddle's event id, and for a body that does not decode it is the
	// digest of the body instead: an undecodable body is stored too, and it has
	// no id of Paddle's to be stored under (F16).
	ID         string    `json:"id"`
	ReceivedAt time.Time `json:"received_at"`
	// Signature is the signature header the delivery carried, kept beside the
	// body because it is what proved the body was Paddle's: an event whose
	// signature was not checked never reaches this shape at all (F16).
	Signature string `json:"signature"`
	// Notification is Paddle's own name for the event, and it is empty for a
	// body that was stored without decoding.
	Notification string `json:"notification"`
	// Body is the verified body exactly as received, as text: a body that does
	// not decode is kept as well as one that does, and text is the one form
	// that holds both. It is the record of a sale (counsel's retention item,
	// D457), so it is stored and not summarised.
	Body string `json:"body"`
	// State is what the portal has made of it: stored, undecodable, processed,
	// parked or dismissed. Stored is a body that arrived and is not read yet;
	// undecodable is one that was answered 400 so Paddle retries; parked is one
	// whose transaction or subscription did not begin with a transaction this
	// API created, waiting for operations (F15, P5.1); dismissed is operations
	// ending it.
	State string `json:"state"`
	// EntitlementID is what a processed or attached event resolved to, and null
	// while it resolved to nothing: a parked event has no entitlement until
	// operations attaches it.
	EntitlementID *string `json:"entitlement_id"`
}

// WireLedgerEntry is one line of the readable ledger, derived from one stored
// event.
type WireLedgerEntry struct {
	// ID is the stored event's id, because an entry is what an event was read
	// as and one event makes one line: a refund is its own event.
	ID string    `json:"id"`
	At time.Time `json:"at"`
	// Kind is what the event did to the money: purchase, renewal, refund,
	// chargeback or adjustment. It is the portal's word, not Paddle's, because
	// the two providers' notification names are not a ledger.
	Kind           string `json:"kind"`
	OrganisationID string `json:"organisation_id"`
	// WorkspaceID is the portal workspace the money was for, and null for a
	// line whose event named none.
	WorkspaceID   *string `json:"workspace_id"`
	EntitlementID *string `json:"entitlement_id"`
	// AmountMicros is signed: a refund is the negative of what it gives back,
	// and the reports add these up (P5.2). It is in micro-dollars, the unit the
	// rest of this repository counts money in, so no arithmetic here is done in
	// a float.
	AmountMicros int64  `json:"amount_micros"`
	Currency     string `json:"currency"`
	// Seats is how many the line covers, so a report can say what was bought
	// rather than only what it cost.
	Seats int `json:"seats"`
	// Channel is direct or reseller: the split the reports are asked for
	// (spec §Operations), and it is recorded on the line because the reseller
	// link can be relinked afterwards (P6.1).
	Channel string `json:"channel"`
}

func init() {
	registerWire("ledger.event", func() any { return new(WireLedgerEvent) })
	registerWire("ledger.entry", func() any { return new(WireLedgerEntry) })
}
