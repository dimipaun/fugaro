// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// Organisations and memberships (portal plan P2.1; spec §Tenancy): the tenant
// every other shape hangs off.
//
// A person has one identity and belongs to many organisations, and their role is
// in the membership and not in the sign-in token: a role carried as a custom
// claim is a role only the identity provider can take away, so revocation would
// have to wait for a token to expire, and `internal/access`'s roles are the
// installation's rather than the portal's.
//
// The organisation carries its type because the SSO rule follows it (F10,
// D2207): a person who belongs to an operations or a reseller organisation is
// refused by email sign-in for any request that uses that membership. It carries
// the terms it accepted because sign-up records them (P2.1), and the operations
// organisation — seeded as an invitation and never signed up to — carries none.

import "time"

// WireOrganisation is one organisation as the API answers it.
type WireOrganisation struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Type is one of operations, customer and reseller. It is set once and
	// never moved: what a reseller may see and what an operations member may
	// read are both decided by it.
	Type string `json:"type"`
	// Terms is the terms of service this organisation's sign-up accepted, or
	// null for one that nobody signed up to. The version is kept beside the
	// instant because the terms change and the record has to say which wording
	// was agreed to (D2199).
	Terms *WireTerms `json:"terms"`
	// ResellerID is the reseller organisation that created this one on its
	// customer's behalf (D2191, D2192), and null for every organisation a
	// reseller did not create. It is a link and not a parent: operations can
	// relink a customer (P6.1), and a customer's own owners are its owners.
	ResellerID *string   `json:"reseller_id"`
	CreatedAt  time.Time `json:"created_at"`
}

// WireTerms is one acceptance of the terms of service.
type WireTerms struct {
	Version string `json:"version"`
	// AcceptedAt is when the person signed up. It is the acceptance's instant
	// and not the organisation's: they are written once, together, and the two
	// are separate fields because a version is not a time.
	AcceptedAt time.Time `json:"accepted_at"`
}

// WireMembership is one person's place in one organisation. The account exists
// in Firebase Auth and the membership here is the whole of what the portal knows
// about them: nothing about a person is copied out of the provider's answer.
type WireMembership struct {
	OrganisationID string `json:"organisation_id"`
	UID            string `json:"uid"`
	// Email is the address the identity carries and it is the address
	// invitations are compared against, both folded by `access.Member` (F10).
	Email string `json:"email"`
	// Role is one of owner, admin and member. Owners and admins are who may
	// bind, buy, invite and reveal; a member reads and acts in the product but
	// decides nothing in the portal (D2186, D2195).
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
	// LastUsedAt is when this person last worked in this organisation, and it
	// is what the switcher lands them on (spec §Tenancy). It is zero for a
	// membership nobody has selected yet, which is not the same as one
	// selected long ago.
	LastUsedAt *time.Time `json:"last_used_at"`
}

// WireOrgCreate is a sign-up's request: the name of the organisation it makes.
// The account is the caller's, so it is not here, and the type is customer:
// only operations creates a reseller organisation (D2191, P6.1).
type WireOrgCreate struct {
	Name string `json:"name"`
}

// WireOrgList is one person's organisations: the switcher's list, the one they
// last worked in first (P2.1). It is a plain object rather than a bare array
// because an answer is an object everywhere else in this API, and because a list
// that grows a field — a page's cursor, a count — is then not a new shape.
type WireOrgList struct {
	Organisations []WireOrganisation `json:"organisations"`
}

// WireOrgMembers is one organisation's members (P2.1), which its owners and its
// admins read and a member does not (D2186).
type WireOrgMembers struct {
	Members []WireMembership `json:"members"`
}

func init() {
	registerWire("orgs.organisation", func() any { return new(WireOrganisation) })
	registerWire("orgs.membership", func() any { return new(WireMembership) })
	registerWire("orgs.create", func() any { return new(WireOrgCreate) })
	registerWire("orgs.list", func() any { return new(WireOrgList) })
	registerWire("orgs.members", func() any { return new(WireOrgMembers) })
}
