// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// Entitlements: what an organisation holds, what a new grant replaces, and the
// free seat every sign-up is given (portal plan P3.1; D2199, D2203, D2204).
//
// These cases are about the records and not about keys: a grant mints nothing
// (binding does), so what they read is the store and the two lists the SPA
// draws its cards from. The one that is about keys at all is the free seat's,
// and even there the claim is that no key was minted.

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestSignUpCreatesTheFreeSeatEntitlement: every organisation a sign-up creates
// is granted one free seat, at the instant it accepted the terms (D2199, D2184).
// It is granted outside any workspace — the sign-up makes an organisation and
// nothing else — which is the state the SPA's "Not bound yet" section exists for
// (F6, F25).
func TestSignUpCreatesTheFreeSeatEntitlement(t *testing.T) {
	f := newBindingFixture(t)
	org := f.signedUp(danaUID, danaEmail, "Dana Ltd")
	free := f.onlyEntitlement(t, org.ID)

	if free.OrganisationID != org.ID {
		t.Errorf("the free seat belongs to %s, want the organisation that signed up, %s", free.OrganisationID, org.ID)
	}
	if free.Source != entitlementSourceFreeSeat {
		t.Errorf("the free seat's source is %q, want %q", free.Source, entitlementSourceFreeSeat)
	}
	if free.Row != PolicyFreeSeat {
		t.Errorf("the free seat is minted from the row %q, want %q", free.Row, PolicyFreeSeat)
	}
	if free.Tier != "team" || free.Kind != "comped" || free.Seats != 1 {
		t.Errorf("the free seat is %s/%s at %d seat(s), want team/comped at one (D2184)", free.Tier, free.Kind, free.Seats)
	}
	if free.Status != entitlementActive {
		t.Errorf("a fresh free seat is %q, want %q", free.Status, entitlementActive)
	}
	if free.WorkspaceID != nil {
		t.Errorf("the free seat names the workspace %v, and a sign-up creates none (F6)", *free.WorkspaceID)
	}
	if free.BoundAt != nil || free.RebindableAt != nil {
		t.Errorf("the free seat is bound at %v and rebindable at %v, and no key exists for it yet",
			free.BoundAt, free.RebindableAt)
	}
	if free.AirGapped {
		t.Errorf("the free seat is air-gapped, and its row is the one row that is not (D2206)")
	}
	if !free.StartsAt.Equal(f.now) || !free.CreatedAt.Equal(f.now) {
		t.Errorf("the free seat starts at %s and was created at %s, want the sign-up's own instant %s (D2197)",
			free.StartsAt, free.CreatedAt, f.now)
	}
	if want := f.now.Add(yearTerm); !free.EndsAt.Equal(want) {
		t.Errorf("the free seat ends %s, want the row's own year, %s", free.EndsAt, want)
	}

	// The terms the person accepted are what the seat's start is read from, so
	// the two are one instant and not two.
	held, found := f.organisation(org.ID)
	if !found || held.Terms == nil {
		t.Fatalf("the organisation holds no accepted terms: %+v", held.Terms)
	}
	if !free.StartsAt.Equal(held.Terms.AcceptedAt) {
		t.Errorf("the free seat starts at %s and the terms were accepted at %s", free.StartsAt, held.Terms.AcceptedAt)
	}

	// The same list the SPA reads, so the seat is the answer of a route and not
	// only a record.
	listed := readAnswer[WireEntitlementList](f.fixture, f.apiAsCaller(http.MethodGet, apiEntitlementsPath, "")).Entitlements
	if len(listed) != 1 || listed[0].ID != free.ID {
		t.Errorf("the API answers %+v, want the one free seat %s", listed, free.ID)
	}

	// A grant is not a key: the seat is unbound and nothing was minted.
	if len(f.mints.answers) != 0 {
		t.Errorf("the sign-up minted %d keys, want none: binding is what mints (D2196)", len(f.mints.answers))
	}
	if issued := f.issuances(t); len(issued) != 0 {
		t.Errorf("the store holds %d issuances, want none", len(issued))
	}

	// An organisation that was not created by a sign-up was not granted one:
	// the seat is D2199's act and not a property of every organisation.
	seeded := f.seededOrganisation("org-erin", "Erin Ltd", orgTypeCustomer, erinUID, erinEmail, roleOwner)
	if held := f.entitlementsOf(t, seeded.ID); len(held) != 0 {
		t.Errorf("an organisation nobody signed up holds %+v, want nothing", held)
	}
}

// TestPurchaseSupersedesATrial: a workspace has one active entitlement, and the
// grant that lands beside one supersedes it (D2204). The superseded grant keeps
// the term it was paid for — nothing cuts a trial short — and the workspace is
// licensed by the new one.
func TestPurchaseSupersedesATrial(t *testing.T) {
	f := newBindingFixture(t)
	org := f.seededOrganisation("org-dana", "Dana Ltd", orgTypeCustomer, danaUID, danaEmail, roleOwner)
	workspace := f.seedWorkspace("ws-1", org.ID, "Dana's portal")

	trial := f.grant(t, entitlementGrant{
		At:             f.now,
		OrganisationID: org.ID,
		WorkspaceID:    &workspace.ID,
		Source:         entitlementSourceTrial,
		Row:            PolicyTrial,
		Seats:          1,
		StartsAt:       f.now,
	})
	f.advance(24 * time.Hour)
	purchase := f.grant(t, entitlementGrant{
		At:             f.now,
		OrganisationID: org.ID,
		WorkspaceID:    &workspace.ID,
		Source:         entitlementSourcePurchase,
		Row:            PolicyTeamAnnual,
		Seats:          3,
		StartsAt:       f.now,
	})

	after, found := f.entitlement(trial.ID)
	if !found {
		t.Fatalf("the superseded trial %s is gone from the store, and a superseded grant is a record a statement still names", trial.ID)
	}
	if after.Status != entitlementSuperseded {
		t.Errorf("the trial a purchase replaced is %q, want %q (D2204)", after.Status, entitlementSuperseded)
	}
	if !after.EndsAt.Equal(trial.EndsAt) {
		t.Errorf("the superseded trial now ends %s, want the end it was granted for, %s: a supersede is not an early end",
			after.EndsAt, trial.EndsAt)
	}
	if !purchase.StartsAt.Equal(f.now) || !purchase.EndsAt.Equal(f.now.Add(yearTerm)) {
		t.Errorf("the purchase runs %s to %s, want the year from the payment at %s", purchase.StartsAt, purchase.EndsAt, f.now)
	}
	if purchase.Status != entitlementActive {
		t.Errorf("the purchase is %q, want %q", purchase.Status, entitlementActive)
	}

	licensed, _ := f.workspace(workspace.ID)
	if licensed.EntitlementID == nil || *licensed.EntitlementID != purchase.ID {
		t.Errorf("the workspace is licensed by %v, want the purchase %s (D2204)", licensed.EntitlementID, purchase.ID)
	}

	// One active entitlement per workspace is the rule, and the trial is the
	// record that says the workspace was licensed before it. A second grant is
	// not a third state.
	listed := f.entitlementsOf(t, org.ID)
	if len(listed) != 2 {
		t.Fatalf("the organisation holds %d entitlements, want the trial and the purchase", len(listed))
	}
	active := 0
	for _, one := range listed {
		if one.Status == entitlementActive {
			active++
		}
	}
	if active != 1 {
		t.Errorf("%d of the organisation's entitlements are active, want the purchase alone", active)
	}
}

// TestFirstPurchaseEndsTheFreeSeat: the first paid entitlement of an
// organisation ends its free seat at its next renewal (D2203) — the seat is not
// renewed rather than cut off, so its term stands and the key it minted would
// run to the end of it. A trial does not end it: D2203 names a paid grant.
func TestFirstPurchaseEndsTheFreeSeat(t *testing.T) {
	f := newBindingFixture(t)
	org := f.signedUp(danaUID, danaEmail, "Dana Ltd")
	free := f.onlyEntitlement(t, org.ID)
	// The checkout's own workspace (D2192): a purchase arrives naming one, and
	// the free seat stays where it is.
	trialWorkspace := f.seedWorkspace("ws-trial", org.ID, "The trial")
	paidWorkspace := f.seedWorkspace("ws-paid", org.ID, "The paid one")

	f.advance(time.Hour)
	trial := f.grant(t, entitlementGrant{
		At:             f.now,
		OrganisationID: org.ID,
		WorkspaceID:    &trialWorkspace.ID,
		Source:         entitlementSourceTrial,
		Row:            PolicyTrial,
		Seats:          1,
		StartsAt:       f.now,
	})
	if held, _ := f.entitlement(free.ID); held.Status != entitlementActive {
		t.Fatalf("a trial ended the free seat (%q), and D2203 names the first paid grant", held.Status)
	}

	f.advance(time.Hour)
	paid := f.grant(t, entitlementGrant{
		At:             f.now,
		OrganisationID: org.ID,
		WorkspaceID:    &paidWorkspace.ID,
		Source:         entitlementSourcePurchase,
		Row:            PolicyTeamAnnual,
		Seats:          3,
		StartsAt:       f.now,
	})

	after, _ := f.entitlement(free.ID)
	if after.Status != entitlementSuperseded {
		t.Errorf("the free seat is %q after a purchase, want %q (D2203)", after.Status, entitlementSuperseded)
	}
	if !after.EndsAt.Equal(free.EndsAt) {
		t.Errorf("the free seat now ends %s, want %s: it ends at its next renewal and nothing cuts it short",
			after.EndsAt, free.EndsAt)
	}
	if after.WorkspaceID != nil {
		t.Errorf("the ended free seat names the workspace %v, want none: the purchase is somewhere else", *after.WorkspaceID)
	}
	// The trial is a different workspace's business and the seat's end is not
	// the organisation's licence ending.
	if held, _ := f.entitlement(trial.ID); held.Status != entitlementActive {
		t.Errorf("the trial is %q after a purchase in another workspace, want it active (D2204 is per workspace)", held.Status)
	}
	if paid.Status != entitlementActive {
		t.Errorf("the purchase is %q, want %q", paid.Status, entitlementActive)
	}
	licensed, _ := f.workspace(paidWorkspace.ID)
	if licensed.EntitlementID == nil || *licensed.EntitlementID != paid.ID {
		t.Errorf("the paid workspace is licensed by %v, want %s", licensed.EntitlementID, paid.ID)
	}
	// A second paid grant does not reach back to the seat: it is already ended,
	// and superseding a superseded grant is not a state this shape has.
	f.advance(time.Hour)
	f.grant(t, entitlementGrant{
		At:             f.now,
		OrganisationID: org.ID,
		WorkspaceID:    &paidWorkspace.ID,
		Source:         entitlementSourcePurchase,
		Row:            PolicyTeamAnnual,
		Seats:          5,
		StartsAt:       f.now,
	})
	if again, _ := f.entitlement(free.ID); again.Status != entitlementSuperseded || !again.EndsAt.Equal(free.EndsAt) {
		t.Errorf("the free seat moved with a second purchase: %+v", again)
	}
	// Nothing about an unbound seat was ever a key, so nothing was revoked.
	if issued := f.issuances(t); len(issued) != 0 {
		t.Errorf("the store holds %d issuances, want none: no grant of this case was bound", len(issued))
	}
}

// TestTheListsAreTheCallersOrganisation: the two lists a card is drawn from are
// the caller's own organisation's, in the order they were created, and an empty
// list is an empty array rather than a missing one: the wire shape is a list and
// a reader that has to tell null from empty is a reader that will get it wrong.
func TestTheListsAreTheCallersOrganisation(t *testing.T) {
	f := newBindingFixture(t)
	org := f.seededOrganisation("org-dana", "Dana Ltd", orgTypeCustomer, danaUID, danaEmail, roleOwner)

	empty := f.apiAsCaller(http.MethodGet, apiWorkspacesPath, "")
	if empty.Code != http.StatusOK {
		t.Fatalf("the workspace list answered %d: %s", empty.Code, empty.Body)
	}
	if body := empty.Body.String(); !strings.Contains(body, `"workspaces":[]`) {
		t.Errorf("an organisation with no workspaces is answered %s, want an empty array", body)
	}

	first := f.seedWorkspace("ws-a", org.ID, "First")
	f.advance(time.Hour)
	second := f.seedWorkspace("ws-b", org.ID, "Second")
	f.advance(time.Hour)
	erin := f.seededOrganisation("org-erin", "Erin Ltd", orgTypeCustomer, erinUID, erinEmail, roleOwner)
	theirWorkspace := f.seedWorkspace("ws-erin", erin.ID, "Erin's")

	workspaces := readAnswer[WireWorkspaceList](f.fixture, f.apiAsCaller(http.MethodGet, apiWorkspacesPath, "")).Workspaces
	if len(workspaces) != 2 {
		t.Fatalf("the list answers %+v, want the caller's two workspaces", workspaces)
	}
	if workspaces[0].ID != first.ID || workspaces[1].ID != second.ID {
		t.Errorf("the list is %s then %s, want the order they were created in: %s then %s",
			workspaces[0].ID, workspaces[1].ID, first.ID, second.ID)
	}
	for _, one := range workspaces {
		if one.ID == theirWorkspace.ID {
			t.Errorf("another organisation's workspace %s is in the caller's list", one.ID)
		}
	}

	older := f.grant(t, entitlementGrant{
		At: f.now, OrganisationID: org.ID, WorkspaceID: &first.ID,
		Source: entitlementSourcePurchase, Row: PolicyTeamAnnual, Seats: 3, StartsAt: f.now,
	})
	f.advance(time.Hour)
	newer := f.grant(t, entitlementGrant{
		At: f.now, OrganisationID: org.ID, WorkspaceID: &second.ID,
		Source: entitlementSourceTrial, Row: PolicyTrial, Seats: 1, StartsAt: f.now,
	})
	held := readAnswer[WireEntitlementList](f.fixture, f.apiAsCaller(http.MethodGet, apiEntitlementsPath, "")).Entitlements
	if len(held) != 2 {
		t.Fatalf("the list answers %+v, want the caller's two entitlements", held)
	}
	if held[0].ID != older.ID || held[1].ID != newer.ID {
		t.Errorf("the list is %s then %s, want the order they were granted in: %s then %s",
			held[0].ID, held[1].ID, older.ID, newer.ID)
	}

	// And the other organisation's own list holds its own, which is the other
	// half of "the caller's own": the same route, a different caller.
	f.actsAs(erinUID, erinEmail, signInGoogle)
	theirs := readAnswer[WireWorkspaceList](f.fixture, f.apiAsCaller(http.MethodGet, apiWorkspacesPath, "")).Workspaces
	if len(theirs) != 1 || theirs[0].ID != theirWorkspace.ID {
		t.Errorf("the other organisation's list is %+v, want its own workspace %s", theirs, theirWorkspace.ID)
	}
	if held := readAnswer[WireEntitlementList](f.fixture, f.apiAsCaller(http.MethodGet, apiEntitlementsPath, "")).Entitlements; len(held) != 0 {
		t.Errorf("the other organisation holds %+v, want nothing", held)
	}
}
