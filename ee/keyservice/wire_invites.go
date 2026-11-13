// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// Invitations (portal plan P2.2; spec §Tenancy, "invitations by email link").
//
// An invitation is bound to an address and to nothing else: Firebase Auth sends
// the sign-in link, so what the portal holds is the address the invitation was
// for, and it is accepted only by a verified address equal to it, both folded by
// `access.Member` (F10). It carries no token and no key — a stored token would
// be a list of live credentials, and a key is never mailed at all (D2182) — and
// its state is what makes it single-use.
//
// The role is on the invitation because it has to be chosen before the person
// exists in the organisation: the operations seed is the first invitation of
// that kind (P2.2's SeedOperations), and a reseller invites each customer's
// first owner (D2191).

import "time"

// WireInvite is one invitation as the API answers it.
type WireInvite struct {
	ID             string `json:"id"`
	OrganisationID string `json:"organisation_id"`
	// Email is the invited address, and the invitation is accepted by that
	// address only.
	Email string `json:"email"`
	Role  string `json:"role"`
	// State is one of pending, accepted, revoked and expired. Accepted is
	// written when the address it was bound to first signs in, which is also
	// what binds a membership to a uid (F40): nothing before that knows who the
	// person is.
	State string `json:"state"`
	// InvitedBy is the inviter's address, kept as a person reads it rather than
	// as a uid: an owner looking at the list is asking who asked for this.
	InvitedBy string    `json:"invited_by"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	// AcceptedAt is when it was accepted, and null while it is not: an
	// invitation nobody accepted has no such instant, which is not the
	// creation time.
	AcceptedAt *time.Time `json:"accepted_at"`
}

// WireInviteCreate is an owner's or admin's request to invite somebody.
type WireInviteCreate struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

func init() {
	registerWire("invites.invite", func() any { return new(WireInvite) })
	registerWire("invites.create", func() any { return new(WireInviteCreate) })
}
