// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// Workspaces and binding (portal plan P3.1; F25, D2196).
//
// A workspace is the portal's own object and it is not the Firebase project it
// will be bound to: a customer pays before `fugaro setup` has created anything
// (F25), so the workspace exists first, carries the name the customer chose, and
// the project id is attached to it at binding — which is the act that mints the
// key. That split is why ProjectID is nullable here and why it is a field of its
// own rather than the workspace's identity.
//
// ID is the workspace's portal id: it is what a Paddle transaction carries in
// its custom_data (D2192) and what every entitlement, subscription and statement
// names. ProjectID is the Firebase project id and the key's own `workspace_id`
// claim (TN2), and one project id is bound by one workspace across every
// organisation (D2204).

import "time"

// WireWorkspace is one workspace as the API answers it.
type WireWorkspace struct {
	ID             string `json:"id"`
	OrganisationID string `json:"organisation_id"`
	Name           string `json:"name"`
	// ProjectID is the Firebase project id this workspace is bound to, or null
	// while it is unbound: an unbound workspace has no key and no installation
	// behind it yet.
	ProjectID *string `json:"project_id"`
	// EntitlementID is the one entitlement that is active here (D2204), or null
	// for a workspace with nothing granted to it. A second active entitlement
	// for one workspace is refused, so the field is singular on purpose.
	EntitlementID *string   `json:"entitlement_id"`
	CreatedAt     time.Time `json:"created_at"`
	// BoundAt is when the binding that minted this workspace's key happened,
	// and null while it never did. An owner or admin may rebind once per 30
	// days (D2198); the clock is the entitlement's, because the first rebind
	// after a binding is not held by it.
	BoundAt *time.Time `json:"bound_at"`
}

// WireWorkspaceBind is an owner's or admin's request to bind one entitlement to
// one project: the whole of what binding takes, and both halves are the
// caller's to name. Nothing proves the project belongs to them (D2201) — the API
// checks that an installation exists there (D2205) and that the id is shaped
// like a Firebase project id before it goes into a host name — so the project id
// is text here and never a person's claim about who controls it.
type WireWorkspaceBind struct {
	EntitlementID string `json:"entitlement_id"`
	ProjectID     string `json:"project_id"`
}

func init() {
	registerWire("workspaces.workspace", func() any { return new(WireWorkspace) })
	registerWire("workspaces.bind", func() any { return new(WireWorkspaceBind) })
}
