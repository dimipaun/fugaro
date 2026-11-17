// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// P4.3's own cases: the billing mirror, the seat purchase and Paddle's customer
// portal (D2186, D2192, D2197, F20).
//
// **What a case here stands up is a workspace that is already paying**, because
// that is the state every route of this area is read in: a bound workspace, the
// transaction P4.1's checkout wrote, the subscription and the origin record
// P3.6's purchase event wrote, and the licence it granted. The mirror is the
// reading of exactly those records, so a case that seeded less would be a case
// about a state no deployment reaches — and a case that seeded more would be a
// case about this service deciding something Paddle has already said.
//
// **The routes are spelled as the screen spells them.** `ee/portal/src/screens/
// billing/billing.tsx` is the contract's other half (P4.4 landed it), and its
// own test asserts the same three strings: a case that read the constants beside
// the route instead would pass over a route that had moved.
//
// The fixture is the package's own through P3.1's and P4.1's (newCheckoutFixture),
// so these cases reach the same in-memory store, the same sign-in spy and the
// same Paddle spy every other case here does.

import (
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"
)

// billingSeats is the quantity the cases below buy at, and it is deliberately
// not one: a request that named the quantity it started from would pass whether
// the field were the whole or an increment.
const billingSeats = 3

// The three routes of this area, as the screen builds them.
func subscriptionPath(workspaceID string) string {
	return "/api/workspaces/" + workspaceID + "/subscription"
}

func seatsPath(workspaceID string) string {
	return subscriptionPath(workspaceID) + "/seats"
}

func portalPath(workspaceID string) string {
	return subscriptionPath(workspaceID) + "/portal"
}

// customerOf is the Paddle customer one organisation's purchases are under. It
// is fixture text shaped the way Paddle spells a customer id, and nothing about
// it is a secret.
func customerOf(organisationID string) string { return "ctm-" + organisationID }

// payingWorkspace is the state every case here starts from: an organisation
// whose one workspace is bound and paying.
func (f *bindingFixture) payingWorkspace(t *testing.T, seats int) (WireOrganisation, WireWorkspace) {
	t.Helper()
	org := f.seededOrganisation("org-dana", "Dana Ltd", orgTypeCustomer, danaUID, danaEmail, roleOwner)
	return org, f.paidWorkspace(t, org.ID, "ws-1", exampleProject, "sub-dana", seats, f.now.Add(20*24*time.Hour), false)
}

// paidWorkspace is one bound workspace with a paid subscription on it, and the
// records a purchase leaves behind: the transaction the checkout opened (P4.1),
// the subscription and the origin record the purchase event wrote, and the
// licence that purchase granted (P4.2). Every value the mirror reads is written
// the way a landed writer writes it, and the case names only what it is about.
func (f *bindingFixture) paidWorkspace(t *testing.T, organisationID, id, project, subscriptionID string, seats int, expires time.Time, cancelled bool) WireWorkspace {
	t.Helper()
	workspace := f.boundWorkspace(id, organisationID, "Dana's portal", project)
	at := f.now.Add(-24 * time.Hour)
	transaction := "txn-" + subscriptionID
	licence, err := f.svc.grantEntitlement(context.Background(), entitlementGrant{
		At:             at,
		OrganisationID: organisationID,
		WorkspaceID:    &workspace.ID,
		Source:         entitlementSourcePurchase,
		Row:            PolicyTeamMonthly,
		Seats:          seats,
		StartsAt:       at,
		EndsAt:         expires,
	})
	if err != nil {
		t.Fatalf("granting the licence the purchase %s bought: %v", transaction, err)
	}
	owner, _ := f.ownerOf(t, organisationID)
	f.seed(checkoutTransactionsCollection, transaction, checkoutTransaction{
		ID:              transaction,
		OrganisationID:  organisationID,
		WorkspaceID:     workspace.ID,
		PriceID:         checkoutPrice,
		Seats:           seats,
		UID:             owner.UID,
		Email:           owner.Email,
		TermsVersion:    checkoutTerms,
		TermsAcceptedAt: at,
		CustomerID:      customerOf(organisationID),
		EntitlementID:   licence.ID,
		CreatedAt:       at,
	})
	f.seed(paddleSubscriptionsCollection, subscriptionID, paddleOrigin{
		SubscriptionID: subscriptionID,
		TransactionID:  transaction,
		EntitlementID:  licence.ID,
		OrganisationID: organisationID,
		WorkspaceID:    workspace.ID,
		At:             at,
	})
	if err := f.store.SaveSubscription(context.Background(), Subscription{
		ID:           subscriptionID,
		Organisation: organisationID,
		WorkspaceID:  workspace.ID,
		Seats:        seats,
		TermDays:     30,
		Email:        owner.Email,
		At:           at,
		Expires:      expires,
		Cancelled:    cancelled,
	}); err != nil {
		t.Fatalf("seeding the subscription %s: %v", subscriptionID, err)
	}
	return workspace
}

// ownerOf is the first owner of one organisation, which is who a checkout
// records as the buyer of it.
func (f *bindingFixture) ownerOf(t *testing.T, organisationID string) (WireMembership, bool) {
	t.Helper()
	held, err := recordsWhere[WireMembership](context.Background(), f.docs, membershipsCollection,
		Where{Field: "organisation_id", Value: organisationID})
	if err != nil {
		t.Fatalf("reading the memberships of %s: %v", organisationID, err)
	}
	for _, member := range held {
		if member.Role == roleOwner {
			return member, true
		}
	}
	return WireMembership{}, false
}

// TestBuySeatsUpdatesTheQuantity is the task's first case (P4.3, D2186, D2192):
// a seat purchase moves the quantity Paddle holds, and it moves that one
// subscription rather than making a second — one workspace has one.
//
// The quantity is the whole and not an increment, which is the shape's own
// decision: a customer who types the number they believe they are buying cannot
// buy it twice by sending the request again after a reply that was lost. And the
// store's own record is left where P3.6's event will move it, because the event
// that carries the new quantity is what reissues the key — a seat purchase that
// wrote the seats here would be a reissue that never happens, the redelivery
// carrying the seats the store already holds.
func TestBuySeatsUpdatesTheQuantity(t *testing.T) {
	f := newCheckoutFixture(t)
	org, workspace := f.payingWorkspace(t, billingSeats)

	// The mirror, as the billing screen reads it: Paddle's own subscription id,
	// the quantity, the state and the term, and the two records behind them —
	// the licence's start (D2197) and the price the sale was made at.
	read := f.apiAsCaller(http.MethodGet, subscriptionPath(workspace.ID), "")
	if read.Code != http.StatusOK {
		t.Fatalf("reading the subscription answered %d: %s", read.Code, read.Body)
	}
	held := readAnswer[WireSubscription](f.fixture, read)
	if held.ID != "sub-dana" {
		t.Errorf("the mirror names the subscription %q, want %q", held.ID, "sub-dana")
	}
	if held.OrganisationID != org.ID || held.WorkspaceID != workspace.ID {
		t.Errorf("the mirror is for %s/%s, want %s/%s", held.OrganisationID, held.WorkspaceID, org.ID, workspace.ID)
	}
	if held.State != "active" || held.Seats != billingSeats {
		t.Errorf("the mirror says %q with %d seats, want %q with %d", held.State, held.Seats, "active", billingSeats)
	}
	if held.PriceID != checkoutPrice {
		t.Errorf("the mirror's price is %q, want %q", held.PriceID, checkoutPrice)
	}
	if !held.StartsAt.Equal(f.now.Add(-24 * time.Hour)) {
		t.Errorf("the mirror starts at %s, want the licence's own instant %s", held.StartsAt, f.now.Add(-24*time.Hour))
	}
	if held.RenewsAt == nil || !held.RenewsAt.Equal(f.now.Add(20*24*time.Hour)) {
		t.Errorf("the mirror renews at %v, want the paid term's end", held.RenewsAt)
	}
	if held.EndsAt != nil || held.CancelledAt != nil {
		t.Errorf("a running subscription ends at %v and was cancelled at %v, want neither", held.EndsAt, held.CancelledAt)
	}

	// The purchase itself.
	bought := f.apiAsCaller(http.MethodPost, seatsPath(workspace.ID), `{"seats":7}`)
	if bought.Code != http.StatusOK {
		t.Fatalf("buying seats answered %d: %s", bought.Code, bought.Body)
	}
	if answer := readAnswer[WireSubscription](f.fixture, bought); answer.Seats != 7 {
		t.Errorf("the answer says %d seats, want the 7 Paddle took", answer.Seats)
	}
	if !reflect.DeepEqual(f.paddle.seats, []int{7}) {
		t.Errorf("the merchant was asked for the quantities %v, want [7]", f.paddle.seats)
	}
	stored, present, err := f.store.Subscription(context.Background(), "sub-dana")
	if err != nil || !present {
		t.Fatalf("reading the subscription back: %v (present %t)", err, present)
	}
	if stored.Seats != billingSeats {
		t.Errorf("the store holds %d seats, want the %d P3.6's event has not moved yet", stored.Seats, billingSeats)
	}

	// The same request again is the same purchase: the field is the whole
	// quantity, so a customer whose reply was lost sends it twice and the
	// subscription is where the first one left it.
	again := f.apiAsCaller(http.MethodPost, seatsPath(workspace.ID), `{"seats":7}`)
	if again.Code != http.StatusOK {
		t.Fatalf("buying the same seats again answered %d: %s", again.Code, again.Body)
	}
	if !reflect.DeepEqual(f.paddle.seats, []int{7, 7}) {
		t.Errorf("the merchant was asked for the quantities %v, want [7 7]", f.paddle.seats)
	}

	// A quantity below the shape's own minimum is refused, and the merchant is
	// not reached: the schema's range is the handler's check (P1.1).
	zero := f.apiAsCaller(http.MethodPost, seatsPath(workspace.ID), `{"seats":0}`)
	if zero.Code != http.StatusUnprocessableEntity {
		t.Fatalf("buying no seats answered %d: %s", zero.Code, zero.Body)
	}
	if code := refusal(f.fixture, zero); code != "invalid_seats" {
		t.Errorf("the refusal is %q, want %q", code, "invalid_seats")
	}
	// And a body that is not this shape is refused as one rather than read as
	// the field it was meant to be.
	other := f.apiAsCaller(http.MethodPost, seatsPath(workspace.ID), `{"quantity":7}`)
	if other.Code != http.StatusBadRequest {
		t.Fatalf("buying seats with a body of another shape answered %d: %s", other.Code, other.Body)
	}
	if !reflect.DeepEqual(f.paddle.seats, []int{7, 7}) {
		t.Errorf("the merchant was asked for the quantities %v after two refusals, want [7 7]", f.paddle.seats)
	}
}
