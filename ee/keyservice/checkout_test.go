// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// P4.1's own cases: the checkout, which is the one act of the portal that
// spends a customer's money and the one route F15 is about.
//
// **What these cases are for.** The finding is not that a page could buy
// something — it is that a page could say *what* it was buying: `custom_data`
// set by a browser is a browser choosing which organisation and which workspace
// the payment is filed under, and a person who could name a victim's workspace
// could buy against it and then refund, or take the licence a customer's own
// money paid for. So the cases below are about what the API decides on its own:
// the organisation is read from the workspace's own record and the caller's
// membership (never from the body), the price is the deployment's, the quantity
// is this service's, the answer is one id, and a body that tries to name the
// organisation is refused for its shape rather than ignored.
//
// **And about what the purchase is admitted by afterwards.** A merchant event is
// admitted because a transaction this API created began it (F15, P4.2), so the
// record this route writes is the whole of that claim: Paddle's transaction id,
// the two ids its `custom_data` carries, what was sold and who bought. The cases
// read it back out of the store rather than trusting the answer.
//
// The fixture is the package's own through P3.1's (newBindingFixtureWith), so
// these cases reach the same in-memory store, the same sign-in spy and the same
// Paddle spy every other case here does; the one thing a case changes about the
// deployment is the price it sells at, which is the deploy's own value
// (Config.PaddlePriceID) and no test's.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// checkoutPrice is the price these cases sell at. It is fixture text shaped the
// way Paddle spells one — a price id is public and is not a secret — and the
// deployment names its own.
const checkoutPrice = "pri_fixture_team_monthly"

// checkoutTerms is the licence terms' version these cases accept, which is the
// page's own value (the wording it rendered) and not the deployment's.
const checkoutTerms = "2026-11-01"

// newCheckoutFixture is the package's fixture selling at a price of the case's
// own, which is the one deployment value a checkout cannot work without.
func newCheckoutFixture(t *testing.T) *bindingFixture {
	t.Helper()
	return newBindingFixtureWith(t, func(f *fixture) {
		f.cfg.PaddlePriceID = checkoutPrice
	})
}

// buy posts one checkout the way the SPA does: the workspace and the licence
// terms' version, as the account the fixture is signed in as.
func (f *bindingFixture) buy(t *testing.T, workspaceID, terms string) *httptest.ResponseRecorder {
	t.Helper()
	return f.apiAsCaller(http.MethodPost, apiCheckoutPath,
		`{"workspace_id":`+jsonString(t, workspaceID)+`,"terms_version":`+jsonString(t, terms)+`}`)
}

// buyingWorkspace is an organisation with one workspace in it and nothing bought
// yet, which is the state every checkout case starts from.
func (f *bindingFixture) buyingWorkspace(t *testing.T) (WireOrganisation, WireWorkspace) {
	t.Helper()
	org := f.seededOrganisation("org-dana", "Dana Ltd", orgTypeCustomer, danaUID, danaEmail, roleOwner)
	return org, f.seedWorkspace("ws-1", org.ID, "Dana's portal")
}

// boundWorkspace is one workspace that has been bound: it carries a project id,
// which is what a subscription is recorded under (store.go's WorkspaceID, and
// the value `merchant.go`'s `purchased` writes).
func (f *bindingFixture) boundWorkspace(id, organisationID, name, projectID string) WireWorkspace {
	f.t.Helper()
	workspace := WireWorkspace{
		ID:             id,
		OrganisationID: organisationID,
		Name:           name,
		ProjectID:      &projectID,
		CreatedAt:      f.now,
	}
	f.seed(workspacesCollection, workspace.ID, workspace)
	return workspace
}

// subscription writes one subscription for a workspace straight into the store,
// which is what a merchant event does (P4.2) and what D2192's rule reads. The
// workspace is named the way the writer landing today names it: the Firebase
// project the subscription's keys bind to.
func (f *bindingFixture) subscription(id, organisationID, workspaceID string, expires time.Time, cancelled bool) {
	f.t.Helper()
	err := f.store.SaveSubscription(context.Background(), Subscription{
		ID:           id,
		Organisation: organisationID,
		WorkspaceID:  workspaceID,
		Seats:        1,
		TermDays:     30,
		Email:        danaEmail,
		At:           f.now.Add(-24 * time.Hour),
		Expires:      expires,
		Cancelled:    cancelled,
	})
	if err != nil {
		f.t.Fatalf("seeding the subscription %s: %v", id, err)
	}
}

// TestCheckoutNeedsLicenceTermsAccepted: a checkout that names no licence terms'
// version buys nothing. The version is the wording the page rendered, so a
// request without one is a request that recorded no acceptance, and an
// acceptance is what a paid term runs under (spec §Legal) — there is nothing to
// sell under a wording nobody agreed to. Empty, blank and absent are one answer,
// and in every one of them the merchant is never reached: a transaction this
// service would have to abandon is a transaction that exists at Paddle.
func TestCheckoutNeedsLicenceTermsAccepted(t *testing.T) {
	f := newCheckoutFixture(t)
	f.buyingWorkspace(t)

	// A body that leaves the field out is not this shape rather than an
	// acceptance of nothing: the shape's two fields are both required (P1.1), so
	// an absent one is refused at the door, and neither answer reaches the
	// merchant.
	rec := f.apiAsCaller(http.MethodPost, apiCheckoutPath, `{"workspace_id":"ws-1"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a checkout with no terms field answered %d: %s", rec.Code, rec.Body)
	}

	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "an empty version", body: `{"workspace_id":"ws-1","terms_version":""}`},
		{name: "a blank version", body: `{"workspace_id":"ws-1","terms_version":"   "}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.apiAsCaller(http.MethodPost, apiCheckoutPath, tc.body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("a checkout with %s answered %d: %s", tc.name, rec.Code, rec.Body)
			}
			if code := refusal(f.fixture, rec); code != "terms_not_accepted" {
				t.Errorf("the refusal is %q, want %q", code, "terms_not_accepted")
			}
		})
	}
	switch held, err := recordsWhere[checkoutTransaction](context.Background(), f.docs, checkoutTransactionsCollection, Where{}); {
	case err != nil:
		t.Fatalf("reading the checkout records: %v", err)
	case len(held) != 0:
		t.Errorf("the store holds %d checkout record(s) for checkouts that accepted no terms", len(held))
	case len(f.paddle.checkouts) != 0:
		t.Errorf("the merchant was asked for %d transaction(s) for checkouts that accepted no terms", len(f.paddle.checkouts))
	}
}

// TestTransactionIsCreatedServerSide: the transaction is this service's. The
// organisation is the workspace's own — read from its record and the caller's
// membership and never from the body — the price is the deployment's, the
// quantity is this service's, and the page is handed one id and nothing else.
//
// The two bodies at the end are the finding itself: one names the organisation
// the payment should be filed under and one names the merchant's `custom_data`
// outright, and both are refused for their shape. A route that read the half of
// a body it knew would be a route where a page sets custom data by another name
// (F15).
func TestTransactionIsCreatedServerSide(t *testing.T) {
	f := newCheckoutFixture(t)
	org, workspace := f.buyingWorkspace(t)

	rec := f.buy(t, workspace.ID, checkoutTerms)
	if rec.Code != http.StatusCreated {
		t.Fatalf("a checkout answered %d: %s", rec.Code, rec.Body)
	}
	answer := map[string]any{}
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("reading the checkout's answer %q: %v", rec.Body.String(), err)
	}
	if len(answer) != 1 || answer["transaction_id"] != f.paddle.transaction.ID {
		t.Errorf("the answer is %v, want the transaction id %q and nothing else", answer, f.paddle.transaction.ID)
	}

	if len(f.paddle.checkouts) != 1 {
		t.Fatalf("the merchant was asked for %d transaction(s), want one", len(f.paddle.checkouts))
	}
	asked := f.paddle.checkouts[0]
	switch {
	case asked.Organisation != org.ID:
		t.Errorf("the transaction is filed under the organisation %q, want the workspace's own %q", asked.Organisation, org.ID)
	case asked.Workspace != workspace.ID:
		t.Errorf("the transaction names the workspace %q, want %q", asked.Workspace, workspace.ID)
	case asked.PriceID != checkoutPrice:
		t.Errorf("the transaction sells the price %q, want the deployment's %q", asked.PriceID, checkoutPrice)
	case asked.Seats != checkoutSeats:
		t.Errorf("the transaction sells %d seat(s), want this service's %d", asked.Seats, checkoutSeats)
	}

	for _, body := range []string{
		`{"workspace_id":"ws-1","terms_version":"` + checkoutTerms + `","organisation_id":"org-erin"}`,
		`{"workspace_id":"ws-1","terms_version":"` + checkoutTerms + `","custom_data":{"org_id":"org-erin"}}`,
		`{"workspace_id":"ws-1","terms_version":"` + checkoutTerms + `","price_id":"pri_whatever"}`,
	} {
		rec := f.apiAsCaller(http.MethodPost, apiCheckoutPath, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("a checkout body naming its own custom data answered %d: %s", rec.Code, rec.Body)
		}
		if code := refusal(f.fixture, rec); code != "invalid_body" {
			t.Errorf("the refusal is %q, want %q", code, "invalid_body")
		}
	}
	if len(f.paddle.checkouts) != 1 {
		t.Errorf("the merchant was asked for %d transaction(s), want the one the checkout's own body earned", len(f.paddle.checkouts))
	}
}

// TestSecondSubscriptionForAWorkspaceIsRefused: one active subscription per
// workspace (D2192). A second one is two payments for one licence, and the act a
// customer wants instead — more seats — is the billing screen's. What is
// "active" is the renewal's own reading (renew.go): not cancelled and not past
// the end of the term it has paid for, so a subscription that has run out, and a
// cancelled one, are both a workspace a customer can buy again for.
//
// **The record names the workspace in the spelling the writer landing today
// uses**, which is the Firebase project its keys bind to (store.go, and
// `merchant.go`'s `purchased`) — a test that seeded the portal workspace id
// there would pin a comparison against a value the field never holds. A
// subscription for a workspace that is still unbound can only carry the portal
// id (F25: the customer paid before `fugaro setup` created anything), so that
// spelling is refused as well.
func TestSecondSubscriptionForAWorkspaceIsRefused(t *testing.T) {
	f := newCheckoutFixture(t)
	org := f.seededOrganisation("org-dana", "Dana Ltd", orgTypeCustomer, danaUID, danaEmail, roleOwner)
	workspace := f.boundWorkspace("ws-1", org.ID, "Dana's portal", "dana-portal-4821")
	f.subscription("sub-live", org.ID, *workspace.ProjectID, f.now.Add(12*24*time.Hour), false)

	rec := f.buy(t, workspace.ID, checkoutTerms)
	if rec.Code != http.StatusConflict {
		t.Fatalf("a second checkout for a subscribed workspace answered %d: %s", rec.Code, rec.Body)
	}
	if code := refusal(f.fixture, rec); code != "already_subscribed" {
		t.Errorf("the refusal is %q, want %q", code, "already_subscribed")
	}

	// And the same workspace before it was bound: its subscription can only be
	// recorded under the portal id the checkout's own custom_data carries.
	unbound := f.seedWorkspace("ws-unbound", org.ID, "Dana's portal before setup")
	f.subscription("sub-unbound", org.ID, unbound.ID, f.now.Add(12*24*time.Hour), false)
	if rec := f.buy(t, unbound.ID, checkoutTerms); rec.Code != http.StatusConflict {
		t.Errorf("a second checkout for an unbound subscribed workspace answered %d: %s", rec.Code, rec.Body)
	}

	if len(f.paddle.checkouts) != 0 {
		t.Errorf("the merchant was asked for %d transaction(s) for a workspace that is already subscribed", len(f.paddle.checkouts))
	}

	// Each of the other three workspaces is the same rule read at one of its
	// edges: no subscription at all is an organisation's second workspace (the
	// rule is one per workspace and not one per organisation), a cancelled
	// subscription is one whose customer has left, and a subscription past its
	// term is one that has run out. All three are bought again.
	for _, tc := range []struct {
		name      string
		id        string
		project   string
		expires   time.Time
		cancelled bool
	}{
		{name: "a workspace with no subscription", id: "ws-2", project: "dana-portal-2"},
		{name: "a cancelled subscription", id: "ws-3", project: "dana-portal-3", expires: f.now.Add(12 * 24 * time.Hour), cancelled: true},
		{name: "a subscription that has run out", id: "ws-4", project: "dana-portal-4", expires: f.now.Add(-time.Hour)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			second := f.boundWorkspace(tc.id, org.ID, "Dana's other portal", tc.project)
			if tc.id != "ws-2" {
				f.subscription("sub-"+tc.id, org.ID, tc.project, tc.expires, tc.cancelled)
			}
			if rec := f.buy(t, second.ID, checkoutTerms); rec.Code != http.StatusCreated {
				t.Errorf("a checkout for %s answered %d: %s", tc.name, rec.Code, rec.Body)
			}
		})
	}
}

// TestAnotherOrgsWorkspaceCannotBeBought: the workspace has to be the caller's
// own. Dana is signed in and belongs to another organisation, so Erin's
// workspace is answered as a workspace that is not there — the same 404 for "not
// there" and "not yours", which is what the organisation reads answer and for
// the same reason: which workspaces exist is a list of the portal's customers.
func TestAnotherOrgsWorkspaceCannotBeBought(t *testing.T) {
	f := newCheckoutFixture(t)
	f.buyingWorkspace(t)
	other := f.seededOrganisation("org-erin", "Erin Ltd", orgTypeCustomer, erinUID, erinEmail, roleOwner)
	theirs := f.seedWorkspace("ws-erin", other.ID, "Erin's portal")

	for _, tc := range []struct {
		name      string
		workspace string
	}{
		{name: "another organisation's workspace", workspace: theirs.ID},
		{name: "a workspace that is not there", workspace: "ws-nope"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.buy(t, tc.workspace, checkoutTerms)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("a checkout for %s answered %d: %s", tc.name, rec.Code, rec.Body)
			}
			if code := refusal(f.fixture, rec); code != "not_found" {
				t.Errorf("the refusal is %q, want %q", code, "not_found")
			}
		})
	}
	if len(f.paddle.checkouts) != 0 {
		t.Errorf("the merchant was asked for %d transaction(s) for a workspace that is not the caller's", len(f.paddle.checkouts))
	}

	// And a member of the organisation is not one of the people who buys for it
	// (D2186): buying commits the organisation to a term, which is an owner's or
	// an admin's act and not a member's.
	f.seed(membershipsCollection, membershipID(other.ID, danaUID), WireMembership{
		OrganisationID: theirs.ID, UID: danaUID, Email: danaEmail, Role: roleMember, CreatedAt: f.now,
	})
	rec := f.buy(t, theirs.ID, checkoutTerms)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a member's checkout answered %d: %s", rec.Code, rec.Body)
	}
	if code := refusal(f.fixture, rec); code != "not_permitted" {
		t.Errorf("the refusal is %q, want %q", code, "not_permitted")
	}
	if len(f.paddle.checkouts) != 0 {
		t.Errorf("the merchant was asked for %d transaction(s) for a member's checkout", len(f.paddle.checkouts))
	}
}

// TestCheckoutRecordsThePurchase: the record a merchant event is admitted by
// (F15, P4.2). It is keyed by the merchant's own transaction id — the only value
// the event and this record share — and it carries the two ids the event is
// mapped back through, what was sold, the buyer and the licence terms'
// acceptance. The instant is this service's clock and not a time a page sent,
// and nothing in it is a secret: the record is read by operations and by
// support, so it holds no key material and no payment detail.
func TestCheckoutRecordsThePurchase(t *testing.T) {
	f := newCheckoutFixture(t)
	org, workspace := f.buyingWorkspace(t)

	if rec := f.buy(t, workspace.ID, checkoutTerms); rec.Code != http.StatusCreated {
		t.Fatalf("a checkout answered %d: %s", rec.Code, rec.Body)
	}
	held, found := heldRecord[checkoutTransaction](f.fixture, checkoutTransactionsCollection, f.paddle.transaction.ID)
	if !found {
		t.Fatalf("the store holds no record of the transaction %s", f.paddle.transaction.ID)
	}
	switch {
	case held.ID != f.paddle.transaction.ID:
		t.Errorf("the record names the transaction %q, want %q", held.ID, f.paddle.transaction.ID)
	case held.OrganisationID != org.ID || held.WorkspaceID != workspace.ID:
		t.Errorf("the record carries %s/%s, want %s/%s", held.OrganisationID, held.WorkspaceID, org.ID, workspace.ID)
	case held.PriceID != checkoutPrice || held.Seats != checkoutSeats:
		t.Errorf("the record sold %q at %d seat(s), want %q at %d", held.PriceID, held.Seats, checkoutPrice, checkoutSeats)
	case held.UID != danaUID || held.Email != danaEmail:
		t.Errorf("the record's buyer is %s/%s, want %s/%s", held.UID, held.Email, danaUID, danaEmail)
	case held.TermsVersion != checkoutTerms:
		t.Errorf("the record accepted the terms %q, want %q", held.TermsVersion, checkoutTerms)
	case !held.TermsAcceptedAt.Equal(f.now):
		t.Errorf("the acceptance is recorded at %v, want the service's own instant %s", held.TermsAcceptedAt, f.now)
	case held.CustomerID != f.paddle.transaction.CustomerID:
		t.Errorf("the record names the customer %q, want the merchant's %q", held.CustomerID, f.paddle.transaction.CustomerID)
	}
	raw, err := json.Marshal(held)
	if err != nil {
		t.Fatalf("reading the record back as JSON: %v", err)
	}
	for _, secret := range []string{paddleAPIKey, string(f.secret)} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("the checkout record carries the deployment's own value %q, which belongs nowhere near a purchase", secret)
		}
	}
}

// TestCheckoutRefusesADeploymentWithNoPrice: a deployment that runs with a
// merchant client and no price is one whose every checkout would ask Paddle to
// sell nothing. It is refused with the field named rather than passed on as the
// merchant's complaint about a body this service built, and nothing is written.
func TestCheckoutRefusesADeploymentWithNoPrice(t *testing.T) {
	f := newBindingFixtureWith(t, func(f *fixture) { f.cfg.PaddlePriceID = "  " })
	_, workspace := f.buyingWorkspace(t)

	rec := f.buy(t, workspace.ID, checkoutTerms)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("a checkout in a deployment with no price answered %d: %s", rec.Code, rec.Body)
	}
	if len(f.paddle.checkouts) != 0 {
		t.Errorf("the merchant was asked for %d transaction(s) by a deployment that sells no price", len(f.paddle.checkouts))
	}
}
