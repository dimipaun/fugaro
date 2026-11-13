// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The audit log (portal plan P2.1, P5.1; D456, F9).
//
// Every operations action is written here, reads included: a member of the
// operations team looking at somebody else's organisation is an action somebody
// is answerable for, and a log that only held the writes would answer "who
// changed this?" and never "who looked?". Read says which one an entry is, so a
// reader can filter the two without the action's name having to spell it.
//
// An entry is written by the act it describes and never by the reader, and it is
// never edited or deleted: it is the record that makes the rest of the portal's
// power accountable, so the table only grows.

import "time"

// WireAuditEntry is one audited action as the operations log answers it.
type WireAuditEntry struct {
	ID string    `json:"id"`
	At time.Time `json:"at"`
	// ActorUID and ActorEmail are the person, both kept: the uid is what the
	// sign-in verifier answers and the address is what a person reading the log
	// recognises, and an address can be reused by a provider's account in a way
	// a uid cannot.
	ActorUID   string `json:"actor_uid"`
	ActorEmail string `json:"actor_email"`
	// OrganisationID is the organisation the action used or acted on, and null
	// for an action that named none — a lookup across every organisation is the
	// case it exists for (P2.1's TestOperationsReadsAreAudited).
	OrganisationID *string `json:"organisation_id"`
	// Action is the operation's own name, spelled `noun.verb`, and it is what
	// the log is read by: an entry whose action is free text is an entry nobody
	// can count.
	Action string `json:"action"`
	// TargetID is what the action acted on — an entitlement, a key, a
	// workspace, a reseller — and null for an action with no single target.
	TargetID *string `json:"target_id"`
	// Read is true for an action that changed nothing. It is a field of its own
	// rather than a convention in Action because the two are read differently:
	// a write is what an incident review looks for and a read is what a privacy
	// question asks about (F10, D456).
	Read bool `json:"read"`
	// Detail is what the actor wrote about the action, and it is data on render
	// and never markup: it is free text a person typed, and the log is shown in
	// a browser.
	Detail *string `json:"detail"`
}

func init() {
	registerWire("audit.entry", func() any { return new(WireAuditEntry) })
}
