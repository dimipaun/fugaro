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
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"
)

// billingSeats is the quantity the cases below buy at, and it is deliberately
// not one: a request that named the quantity it started from would pass whether
// the field were the whole or an increment.
const billingSeats = 3

// soldPrice is the price these cases' subscriptions were sold at, and it is
// deliberately not the price the deployment sells at now (checkoutPrice, which
// newCheckoutFixture configures). The mirror states the sale and not the
// catalogue — the schema keeps the field for exactly that — and a case that
// seeded one price in both places could not tell which of the two it read.
const soldPrice = "pri_fixture_team_annual"

// lapsedProject is the project of the workspace whose subscription's paid term
// ran out without a cancellation, which is the second of the two ways these
// records stop renewing a subscription. It is a project of its own because a
// workspace is bound to one and the case is about a workspace of its own.
const lapsedProject = "lapsed-portal"

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
	// The licence's start is the payment's instant (D2197) and the subscription's
	// own instant is the last event's (P4.2), which is the same instant only for
	// as long as nothing has happened since: they are seeded a day apart, which is
	// the state a renewal or a seat change leaves, so a mirror that read `starts_at`
	// off the store's record would answer the wrong day.
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
		PriceID:         soldPrice,
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
		At:           at.Add(24 * time.Hour),
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
	if held.PriceID != soldPrice {
		t.Errorf("the mirror's price is %q, want the price the sale was made at %q and not the deployment's %q", held.PriceID, soldPrice, checkoutPrice)
	}
	if !held.StartsAt.Equal(f.now.Add(-24 * time.Hour)) {
		t.Errorf("the mirror starts at %s, want the licence's own instant %s and not the store's %s", held.StartsAt, f.now.Add(-24*time.Hour), f.now)
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

	// The other state the records tell apart (billing.go): a cancellation was
	// recorded, so nothing renews this one and the mirror says so — the term the
	// customer paid for still runs to the end of it, and the instant of the
	// cancellation is the instant the store's record was last moved. The state is
	// read off the records and not stored: P4.2 keeps no Paddle status, so the
	// only two words the mirror may answer are the two the records decide.
	gone := f.paidWorkspace(t, org.ID, "ws-2", secondProject, "sub-gone", 2, f.now.Add(6*24*time.Hour), true)
	read = f.apiAsCaller(http.MethodGet, subscriptionPath(gone.ID), "")
	if read.Code != http.StatusOK {
		t.Fatalf("reading the cancelled subscription answered %d: %s", read.Code, read.Body)
	}
	ended := readAnswer[WireSubscription](f.fixture, read)
	if ended.State != subscriptionStateCanceled {
		t.Errorf("a cancelled subscription reads as %q, want %q", ended.State, subscriptionStateCanceled)
	}
	if ended.RenewsAt != nil {
		t.Errorf("a cancelled subscription renews at %v, want nothing", ended.RenewsAt)
	}
	if ended.EndsAt == nil || !ended.EndsAt.Equal(f.now.Add(6*24*time.Hour)) {
		t.Errorf("a cancelled subscription ends at %v, want the end of the term it paid for", ended.EndsAt)
	}
	if ended.CancelledAt == nil || !ended.CancelledAt.Equal(f.now) {
		t.Errorf("a cancelled subscription was cancelled at %v, want the instant its record was last moved (%s)", ended.CancelledAt, f.now)
	}
	if ended.Seats != 2 {
		t.Errorf("a cancelled subscription holds %d seats, want the 2 it was sold", ended.Seats)
	}

	// And its quantity cannot be changed. The merchant charges at the moment of
	// the change (Paddle's own proration) and the event that would carry the new
	// quantity is the one `seatChanged` drops for a cancelled subscription
	// (S2.1), so a change here is a customer charged for seats this service never
	// licences — and the screen offers the form to anyone who holds a
	// subscription, so the rule is the service's rather than the form's.
	// The other reason nothing renews a subscription, and it is not the same
	// fact: the paid term ran out and no cancellation was ever recorded. That is
	// not a word Paddle said, so the state stays what the merchant last said and
	// the dates are what state the term is over — the two cases stand beside each
	// other because telling them apart is the whole of what these records can do.
	lapsed := f.paidWorkspace(t, org.ID, "ws-3", lapsedProject, "sub-lapsed", 1, f.now.Add(-2*time.Hour), false)
	read = f.apiAsCaller(http.MethodGet, subscriptionPath(lapsed.ID), "")
	if read.Code != http.StatusOK {
		t.Fatalf("reading the subscription whose term ran out answered %d: %s", read.Code, read.Body)
	}
	over := readAnswer[WireSubscription](f.fixture, read)
	if over.State != subscriptionStateActive {
		t.Errorf("a subscription whose term ran out reads as %q, want the %q Paddle last said: nothing cancelled it", over.State, subscriptionStateActive)
	}
	if over.RenewsAt != nil {
		t.Errorf("a subscription whose term ran out renews at %v, want nothing", over.RenewsAt)
	}
	if over.EndsAt == nil || !over.EndsAt.Equal(f.now.Add(-2*time.Hour)) {
		t.Errorf("a subscription whose term ran out ends at %v, want the end of the term it paid for", over.EndsAt)
	}
	if over.CancelledAt != nil {
		t.Errorf("a subscription whose term ran out was cancelled at %v, want nothing: no cancellation was recorded", over.CancelledAt)
	}

	notRenewing := f.apiAsCaller(http.MethodPost, seatsPath(gone.ID), `{"seats":4}`)
	if notRenewing.Code != http.StatusConflict {
		t.Fatalf("buying seats on a subscription that is not renewing answered %d: %s", notRenewing.Code, notRenewing.Body)
	}
	if code := refusal(f.fixture, notRenewing); code != "not_running" {
		t.Errorf("buying seats on a subscription that is not renewing was refused with %q, want %q", code, "not_running")
	}
	if !reflect.DeepEqual(f.paddle.seats, []int{7, 7}) {
		t.Errorf("the merchant was asked for the quantities %v, want the [7 7] it was asked for while the subscription ran", f.paddle.seats)
	}
}

// TestCustomerPortalLinkIsPerOrg: the customer-portal session is minted for the
// organisation and never for anything a request says (P4.3, P4.1's record).
// Paddle's customer is what an organisation's invoices, payment methods and
// subscription hang on, so the one value a request must not be able to name is a
// customer — and this route names none of the three: it takes a workspace, reads
// the organisation that owns it and reads the customer off that organisation's
// own purchases. Every workspace of one organisation therefore answers the same
// customer, and another organisation answers its own.
func TestCustomerPortalLinkIsPerOrg(t *testing.T) {
	f := newCheckoutFixture(t)
	f.paddle.portalURL = "https://sandbox-portal.paddle.example/session/1"
	org, first := f.payingWorkspace(t, 1)
	second := f.paidWorkspace(t, org.ID, "ws-2", secondProject, "sub-dana-2", 1, f.now.Add(20*24*time.Hour), false)

	for _, workspace := range []WireWorkspace{first, second} {
		rec := f.apiAsCaller(http.MethodGet, portalPath(workspace.ID), "")
		if rec.Code != http.StatusOK {
			t.Fatalf("asking for the customer portal of %s answered %d: %s", workspace.ID, rec.Code, rec.Body)
		}
		if got := readAnswer[WireCustomerPortal](f.fixture, rec).URL; got != f.paddle.portalURL {
			t.Errorf("the portal of %s is %q, want the session the merchant answered (%q)", workspace.ID, got, f.paddle.portalURL)
		}
		if f.paddle.customerID != customerOf(org.ID) {
			t.Errorf("the session for %s was minted for the customer %q, want the organisation's own %q", workspace.ID, f.paddle.customerID, customerOf(org.ID))
		}
	}

	// Another organisation's workspace is another customer, and no request can
	// reach across: the route has no field for an organisation, a subscription or
	// a customer, and the one it mints for is read from the caller's membership.
	erin := f.seededOrganisation("org-erin", "Erin Ltd", orgTypeCustomer, erinUID, erinEmail, roleOwner)
	theirs := f.paidWorkspace(t, erin.ID, "ws-erin", "erin-portal-4821", "sub-erin", 1, f.now.Add(20*24*time.Hour), false)
	// A purchase recorded before the merchant named a customer carries none
	// (checkout.go's record), and it is the newer one here: the customer a
	// session is minted for is the newest purchase that has one, because a
	// session for the empty string would be a call about nobody.
	f.seed(checkoutTransactionsCollection, "txn-erin-unstamped", checkoutTransaction{
		ID:              "txn-erin-unstamped",
		OrganisationID:  erin.ID,
		WorkspaceID:     theirs.ID,
		PriceID:         checkoutPrice,
		Seats:           1,
		UID:             erinUID,
		Email:           erinEmail,
		TermsVersion:    checkoutTerms,
		TermsAcceptedAt: f.now,
		CreatedAt:       f.now,
	})
	f.actsAs(erinUID, erinEmail, signInGoogle)
	rec := f.apiAsCaller(http.MethodGet, portalPath(theirs.ID), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("asking for Erin's own customer portal answered %d: %s", rec.Code, rec.Body)
	}
	if f.paddle.customerID != customerOf(erin.ID) {
		t.Errorf("Erin's session was minted for the customer %q, want her organisation's own %q", f.paddle.customerID, customerOf(erin.ID))
	}

	// The session is a URL a browser is sent to, so what leaves here is checked
	// to be https. The merchant's own client checks only that the answer is not
	// empty (paddleclient.go's PortalSession), so this guard is the only one
	// there is, and a session naming another scheme is a failure rather than an
	// answer: nothing is handed to the screen to open.
	f.paddle.portalURL = "http://sandbox-portal.paddle.example/session/2"
	plain := f.apiAsCaller(http.MethodGet, portalPath(theirs.ID), "")
	if plain.Code != http.StatusInternalServerError {
		t.Fatalf("a session that is not https answered %d: %s", plain.Code, plain.Body)
	}
	if code := refusal(f.fixture, plain); code != "unavailable" {
		t.Errorf("a session that is not https was refused with %q, want the failure %q", code, "unavailable")
	}
	var handed WireCustomerPortal
	if err := json.Unmarshal(plain.Body.Bytes(), &handed); err != nil || handed.URL != "" {
		t.Errorf("the answer to a session that is not https carries the URL %q (unmarshal: %v), want none", handed.URL, err)
	}
}

// TestOnlyOwnersAndAdminsBuy: acting on a subscription is an owner's or an
// admin's (D2186), and reading it is a member's — the screen is a member's to
// open, and the number on it is not. Both acts go the same way: Paddle's own
// customer portal is where a payment method is changed and a subscription
// cancelled, so a session minted for a member is a member cancelling the
// organisation's subscription.
func TestOnlyOwnersAndAdminsBuy(t *testing.T) {
	f := newCheckoutFixture(t)
	org, workspace := f.payingWorkspace(t, 1)
	f.setRole(org.ID, danaUID, roleMember)

	// A member reads the facts the billing screen is drawn from.
	read := f.apiAsCaller(http.MethodGet, subscriptionPath(workspace.ID), "")
	if read.Code != http.StatusOK {
		t.Fatalf("a member reading the subscription answered %d: %s", read.Code, read.Body)
	}
	if seats := readAnswer[WireSubscription](f.fixture, read).Seats; seats != 1 {
		t.Errorf("a member reading the subscription saw %d seats, want the one it holds", seats)
	}

	for _, call := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "buying seats", method: http.MethodPost, path: seatsPath(workspace.ID), body: `{"seats":4}`},
		{name: "opening the customer portal", method: http.MethodGet, path: portalPath(workspace.ID)},
	} {
		rec := f.apiAsCaller(call.method, call.path, call.body)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("a member's %s answered %d: %s", call.name, rec.Code, rec.Body)
		}
		if code := refusal(f.fixture, rec); code != "not_permitted" {
			t.Errorf("a member's %s was refused with %q, want %q", call.name, code, "not_permitted")
		}
	}
	if len(f.paddle.seats) != 0 || f.paddle.customerID != "" {
		t.Errorf("the merchant was reached for a member: %v seats, customer %q", f.paddle.seats, f.paddle.customerID)
	}

	// An admin does both, which is the other half of the line.
	f.setRole(org.ID, danaUID, roleAdmin)
	if rec := f.apiAsCaller(http.MethodPost, seatsPath(workspace.ID), `{"seats":4}`); rec.Code != http.StatusOK {
		t.Fatalf("an admin buying seats answered %d: %s", rec.Code, rec.Body)
	}
	if rec := f.apiAsCaller(http.MethodGet, portalPath(workspace.ID), ""); rec.Code != http.StatusOK {
		t.Fatalf("an admin opening the customer portal answered %d: %s", rec.Code, rec.Body)
	}
	if !reflect.DeepEqual(f.paddle.seats, []int{4}) {
		t.Errorf("the merchant was asked for the quantities %v, want [4]", f.paddle.seats)
	}
	if f.paddle.customerID != customerOf(org.ID) {
		t.Errorf("the admin's session was minted for the customer %q, want the organisation's own %q", f.paddle.customerID, customerOf(org.ID))
	}
}

// TestAnotherOrgsSubscriptionIsRefused: every route of this area is a workspace
// of the caller's own or nothing. Another organisation's workspace is answered
// as one that is not there — the same 404 for "not there" and "not yours", which
// is what the organisation reads answer and for the same reason: which
// workspaces exist is a list of the portal's customers — and the merchant is
// never reached for one, so a refusal is not a call that could have changed
// somebody else's subscription.
func TestAnotherOrgsSubscriptionIsRefused(t *testing.T) {
	f := newCheckoutFixture(t)
	f.payingWorkspace(t, 1)
	erin := f.seededOrganisation("org-erin", "Erin Ltd", orgTypeCustomer, erinUID, erinEmail, roleOwner)
	theirs := f.paidWorkspace(t, erin.ID, "ws-erin", "erin-portal-4821", "sub-erin", 1, f.now.Add(20*24*time.Hour), false)

	for _, tc := range []struct {
		name      string
		workspace string
	}{
		{name: "another organisation's workspace", workspace: theirs.ID},
		{name: "a workspace that is not there", workspace: "ws-nope"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, call := range []struct {
				method string
				path   string
				body   string
			}{
				{method: http.MethodGet, path: subscriptionPath(tc.workspace)},
				{method: http.MethodPost, path: seatsPath(tc.workspace), body: `{"seats":9}`},
				{method: http.MethodGet, path: portalPath(tc.workspace)},
			} {
				rec := f.apiAsCaller(call.method, call.path, call.body)
				if rec.Code != http.StatusNotFound {
					t.Fatalf("%s %s answered %d: %s", call.method, call.path, rec.Code, rec.Body)
				}
				if code := refusal(f.fixture, rec); code != "not_found" {
					t.Errorf("%s %s was refused with %q, want %q", call.method, call.path, code, "not_found")
				}
			}
		})
	}
	if len(f.paddle.seats) != 0 || f.paddle.customerID != "" {
		t.Errorf("the merchant was reached for a workspace that is not the caller's: %v seats, customer %q", f.paddle.seats, f.paddle.customerID)
	}
}
