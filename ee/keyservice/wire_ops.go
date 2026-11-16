// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The operations API (portal plan P5.1; D2187, D2190, D2204, F9).
//
// Operations acts across organisations and is the one actor that may change
// somebody else's licence: it grants and changes terms, it decides the queue of
// requests waiting for a person, it sets the two lists the sign-in rule reads,
// and it transfers a project id from one workspace to another (D2204, TN-P1).
// Every one of those is audited, reads included (D456), which is why the shapes
// here are small requests whose effect is read back from `audit.entry` rather
// than in an answer.
//
// Nothing here carries a key: operations sees metadata (F5), and the one thing
// it can do with a secret is cut a handover short, which takes it away rather
// than handing it over.

import "time"

// WireEntitlementAction is operations' request to change a term it already
// granted: a mint is `entitlements.mint` and this is every later change to it. A
// revoke ends every key the entitlement ever minted (F29) and not only the one
// the installation is running.
type WireEntitlementAction struct {
	EntitlementID string `json:"entitlement_id"`
	// Action is one of extend, seats, comp and revoke. The four share a shape
	// because they are one route with one target; which fields below are read
	// is the action's, and an action that reads none of them is the whole of
	// what it says.
	Action string `json:"action"`
	// Seats is the new count, and it is read by seats alone.
	Seats *int `json:"seats"`
	// EndsAt is the new end, and it is read by extend alone: it moves a term an
	// operations member decided, and it is still capped at the row's own term.
	EndsAt *time.Time `json:"ends_at"`
	// Reason is the licensor's word for ending a term or shortening one, and it
	// is what the audit line carries: it reaches nobody but the operations team
	// (store.go's Revocation says the same about a revocation's reason).
	Reason *string `json:"reason"`
}

// WireProjectTransfer moves one Firebase project id from the workspace that
// bound it to another (D2204, TN-P1): the remedy for a squatted binding, and for
// a customer who set the wrong project up. It is audited, and the workspace that
// loses the id loses its key with it.
type WireProjectTransfer struct {
	ProjectID   string `json:"project_id"`
	WorkspaceID string `json:"workspace_id"`
	// Reason is what the audit line carries for the person who reads it months
	// later, and it is optional: a transfer can be routine.
	Reason *string `json:"reason"`
}

// WireOpsLists is the two lists the operations owner sets. Both are read by the
// sign-in rule rather than by a screen (D2207, F40): EmailDomains limits which
// addresses operations may invite, and MicrosoftDirectories is which Microsoft
// (Entra ID) directories a reseller's Microsoft sign-in is accepted from. An
// empty directory list refuses every microsoft.com sign-in, which is the safe
// reading of "nobody set one".
//
// MicrosoftDirectories carries a NEW name and not the old `microsoft_tenants`
// one: the old field held Identity Platform tenant ids, and a tenant is the door
// a sign-in is made through rather than the directory an account lives in, so a
// reader of a stored `microsoft_tenants` value would pin the wrong thing and no
// error would say so (D2207's amendment, 2026-11-13).
type WireOpsLists struct {
	EmailDomains         []string `json:"email_domains"`
	MicrosoftDirectories []string `json:"microsoft_directories"`
}

// WireQueueDecision is operations' answer to one item of the queue of requests
// waiting for a person: an open-source approval today (P3.3, P5.1), and any
// other request the checks send to a human.
type WireQueueDecision struct {
	ID       string `json:"id"`
	Decision string `json:"decision"`
	// Note is what the operations member wrote, and it is shown to nobody but
	// the team: the applicant is told the decision and not the discussion.
	Note *string `json:"note"`
}

// WireEventResolution is operations' answer to one parked Paddle event (F15,
// P5.1): a notification whose transaction and subscription did not begin with a
// transaction this API created is stored and parked, and operations either
// attaches it to an entitlement or dismisses it. Both are audited, and neither
// edits the stored body.
type WireEventResolution struct {
	// ID is the stored event's id: the parked body is never re-keyed, because
	// the id is what makes a redelivery idempotent.
	ID       string `json:"id"`
	Decision string `json:"decision"`
	// EntitlementID is what the event is attached to, and it is read by attach
	// alone: a dismissal attaches nothing.
	EntitlementID *string `json:"entitlement_id"`
}

func init() {
	registerWire("ops.entitlement_action", func() any { return new(WireEntitlementAction) })
	registerWire("ops.project_transfer", func() any { return new(WireProjectTransfer) })
	registerWire("ops.lists", func() any { return new(WireOpsLists) })
	registerWire("ops.queue_decision", func() any { return new(WireQueueDecision) })
	registerWire("ops.event_resolution", func() any { return new(WireEventResolution) })
}
