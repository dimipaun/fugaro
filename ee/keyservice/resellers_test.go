// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The cases for the reseller area (portal plan P6.1; D2189, D2190, D2191, F5,
// F40).
//
// Every case here starts from a reseller organisation that is **seeded** rather
// than created, and its owner's membership with it, for one reason: the
// organisation operations creates through `POST /api/resellers` has no member by
// design (nothing about it is a sign-up, and the person who will own it is
// invited the way the operations organisation's own first owner is), and the
// invitation into a RESELLER organisation is P2.2's route with the reseller's
// own SSO rule on it — which is a door, but one whose acceptance is a person's
// sign-in and not this area's. What these cases are about is what a reseller and
// operations do once the reseller's people are there, so the people are seeded
// the way P2.2 seeds the operations organisation's (`seededOrganisation`).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	resellerID     = "org-reseller-1"
	rivalID        = "org-reseller-2"
	resellerLimit  = int64(5_000_000)
	resellerRate   = int64(1_000)
	buyerUID       = "uid-buyer"
	buyerMail      = "buyer@example.com"
	rivalBuyerMail = "rivalbuyer@example.com"
)

// resellerOrg seeds one reseller organisation with one member and the terms
// operations would have set on it, which is the state every case here starts
// from.
func (f *fixture) resellerOrg(id, name, uid, email, role string) WireOrganisation {
	f.t.Helper()
	org := f.seededOrganisation(id, name, orgTypeReseller, uid, email, role)
	f.seed(resellerTermsCollection, org.ID, WireReseller{
		OrganisationID:    org.ID,
		CreditLimitMicros: resellerLimit,
		Rates:             []WireResellerRate{{Row: PolicyTeamMonthly, DailyRateMicros: resellerRate}},
		CreatedAt:         f.now,
	})
	return org
}

// soldBy posts one customer organisation the way a reseller's screen does, and
// answers the organisation the route made.
func (f *fixture) soldBy(name string) WireOrganisation {
	f.t.Helper()
	rec := f.apiAsCaller(http.MethodPost, apiResellerCustomersPath, `{"name":`+jsonString(f.t, name)+`}`)
	if rec.Code != http.StatusCreated {
		f.t.Fatalf("a reseller creating the customer organisation %q answered %d: %s", name, rec.Code, rec.Body)
	}
	return readAnswer[WireOrganisation](f, rec)
}

// customersOf reads the caller's own customers, which is one route for every
// reseller: whose list it is, is the token's.
func (f *fixture) customersOf() []WireResellerCustomer {
	f.t.Helper()
	rec := f.apiAsCaller(http.MethodGet, apiResellerCustomersPath, "")
	if rec.Code != http.StatusOK {
		f.t.Fatalf("the customers read answered %d: %s", rec.Code, rec.Body)
	}
	return readAnswer[[]WireResellerCustomer](f, rec)
}

// firstOwnerInvite posts the invitation that makes a customer organisation's
// first owner, as the reseller's screen would.
func (f *fixture) firstOwnerInvite(organisationID, email, role string) *httptest.ResponseRecorder {
	f.t.Helper()
	body := `{"email":` + jsonString(f.t, email) + `,"role":` + jsonString(f.t, role) + `}`
	return f.apiAsCaller(http.MethodPost, resellerFirstOwnerPathOf(organisationID), body)
}

// link reads one customer's link out of the fixture's store.
func (f *fixture) link(organisationID string) (WireResellerCustomer, bool) {
	f.t.Helper()
	return heldRecord[WireResellerCustomer](f, resellerCustomersCollection, organisationID)
}

// terms reads one reseller's terms out of the fixture's store.
func (f *fixture) terms(organisationID string) (WireReseller, bool) {
	f.t.Helper()
	return heldRecord[WireReseller](f, resellerTermsCollection, organisationID)
}

// auditedAction is every line of the log written under one action, which is what
// the cases about operations' acts read.
func (f *fixture) auditedAction(action string) []WireAuditEntry {
	f.t.Helper()
	held := []WireAuditEntry{}
	for _, entry := range f.auditEntries() {
		if entry.Action == action {
			held = append(held, entry)
		}
	}
	return held
}

func resellerKeysPathOf(organisationID string) string {
	return strings.Replace(apiResellerKeysPath, "{id}", organisationID, 1)
}

func resellerFirstOwnerPathOf(organisationID string) string {
	return strings.Replace(apiResellerFirstOwnerPath, "{id}", organisationID, 1)
}

func resellerTermsSetPathOf(organisationID string) string {
	return strings.Replace(apiResellerTermsSetPath, "{id}", organisationID, 1)
}

// resellerTermsBody is one `resellers.reseller` body as the SPA sends it: every
// field of the shape, because the wire refuses a body that leaves one out.
func resellerTermsBody(t *testing.T, organisationID string, limit int64, created time.Time, rates ...WireResellerRate) string {
	t.Helper()
	raw, err := json.Marshal(WireReseller{
		OrganisationID:    organisationID,
		CreditLimitMicros: limit,
		Rates:             append([]WireResellerRate{}, rates...),
		CreatedAt:         created,
	})
	if err != nil {
		t.Fatalf("building the terms body: %v", err)
	}
	return string(raw)
}

// relinkBody is one `resellers.relink` body as operations sends it.
func relinkBody(t *testing.T, customerID, resellerOrganisationID string) string {
	t.Helper()
	raw, err := json.Marshal(WireResellerRelink{
		CustomerOrganisationID: customerID,
		ResellerOrganisationID: resellerOrganisationID,
	})
	if err != nil {
		t.Fatalf("building the relink body: %v", err)
	}
	return string(raw)
}

// TestOnlyOperationsCreatesAResellerOrg: D2191 — a reseller organisation is
// operations' to make and nobody else's, and what it makes is an empty tenant: no
// member, no accepted terms, no money set, and a line in the log.
//
// The three callers refused here are the three a door that only checked "signed
// in" would admit: a reseller's own owner (who is a member of a reseller
// organisation and not of the operations one), an ordinary customer's owner, and
// an operations **member**, who D2187 lets read the operations screens and change
// nothing.
func TestOnlyOperationsCreatesAResellerOrg(t *testing.T) {
	f := newFixture(t)
	ops := f.operationsOrg(roleOwner)
	f.resellerOrg(resellerID, "Partner Ltd", sellerUID, sellerMail, roleOwner)

	f.actsAs(sellerUID, sellerMail, signInGoogle)
	readAPIRefusal(t, f.apiAsCaller(http.MethodPost, apiResellersPath, `{"name":"Rival Ltd"}`),
		http.StatusForbidden, "not_operations")

	f.signedUp(danaUID, danaEmail, "Example Ltd")
	readAPIRefusal(t, f.apiAsCaller(http.MethodPost, apiResellersPath, `{"name":"Rival Ltd"}`),
		http.StatusForbidden, "not_operations")

	f.setRole(ops.ID, opsUID, roleMember)
	f.actsAs(opsUID, opsEmail, signInGoogle)
	readAPIRefusal(t, f.apiAsCaller(http.MethodPost, apiResellersPath, `{"name":"Rival Ltd"}`),
		http.StatusForbidden, "not_permitted")

	f.setRole(ops.ID, opsUID, roleOwner)
	f.actsAs(opsUID, opsEmail, signInGoogle)
	rec := f.apiAsCaller(http.MethodPost, apiResellersPath, `{"name":"  Rival Ltd  "}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("operations creating a reseller organisation answered %d: %s", rec.Code, rec.Body)
	}
	terms := readAnswer[WireReseller](f, rec)
	if terms.OrganisationID == "" {
		t.Fatal("the created reseller's terms name no organisation")
	}
	// Nothing is priced until operations prices it, and nothing is prepaid: a
	// reseller that may sell nothing is the state a create leaves, and it is the
	// safe one (D2189).
	if terms.CreditLimitMicros != 0 || len(terms.Rates) != 0 {
		t.Errorf("the create answered the terms %+v, want no credit limit and no rates", terms)
	}
	if !terms.CreatedAt.Equal(f.now) {
		t.Errorf("the create answered created_at %s, want the instant it was made (%s)", terms.CreatedAt, f.now)
	}

	org, found := f.organisation(terms.OrganisationID)
	if !found {
		t.Fatalf("the store holds no organisation %s after operations created it", terms.OrganisationID)
	}
	if org.Type != orgTypeReseller {
		t.Errorf("the created organisation's type is %q, want %q: D2191 gives a reseller organisation its own type, and F10 hangs the sign-in rule on it",
			org.Type, orgTypeReseller)
	}
	if org.Name != "Rival Ltd" {
		t.Errorf("the created organisation is named %q, want the name trimmed to Rival Ltd", org.Name)
	}
	if org.ResellerID != nil || org.Terms != nil {
		t.Errorf("the created organisation is %+v, want no reseller link and no accepted terms: nobody signed anything up", org)
	}
	members, err := membersOf(context.Background(), f.docs, org.ID)
	if err != nil {
		t.Fatalf("reading the new organisation's members: %v", err)
	}
	if len(members) != 0 {
		t.Errorf("the created reseller organisation holds %d member(s), want none: its people arrive by invitation", len(members))
	}
	// The terms record is written with the organisation and not after it: an
	// organisation nothing may be sold through is not one anybody asked for.
	held, ready := f.terms(org.ID)
	if !ready {
		t.Error("operations created a reseller organisation and no terms record, so the two are not one act")
	}
	if held.CreditLimitMicros != 0 || len(held.Rates) != 0 {
		t.Errorf("the stored terms are %+v, want the zero the answer carries", held)
	}

	lines := f.auditedAction(actionResellerCreate)
	if len(lines) != 1 {
		t.Fatalf("creating a reseller organisation wrote %d %s line(s), want one (D456)", len(lines), actionResellerCreate)
	}
	line := lines[0]
	if line.ActorEmail != opsEmail || line.Read || line.TargetID == nil || *line.TargetID != org.ID {
		t.Errorf("the create's line is %+v, want the operations caller's own act on %s and not a read", line, org.ID)
	}
	if line.OrganisationID == nil || *line.OrganisationID != org.ID {
		t.Errorf("the create's line names the organisation %v, want the one it made (%s)", line.OrganisationID, org.ID)
	}
}

// TestCreditLimitAndRatesAreOperationsSet: D2189's money is operations' to set on
// an existing reseller — the ceiling on unpaid accrual and one wholesale price
// per policy row — and the reseller reads its own back. What operations may not
// set is anything the schema refuses: a negative limit, a rate below zero, a row
// the policy table does not carry, or one row priced twice.
func TestCreditLimitAndRatesAreOperationsSet(t *testing.T) {
	f := newFixture(t)
	f.operationsOrg(roleOwner)
	created := readAnswer[WireReseller](f, f.apiAsCaller(http.MethodPost, apiResellersPath, `{"name":"Partner Ltd"}`))

	// A body that names another reseller is refused rather than ignored: the
	// record written is the record asked for, and it is the one in the path.
	readAPIRefusal(t, f.apiAsCaller(http.MethodPut, resellerTermsSetPathOf(created.OrganisationID),
		resellerTermsBody(t, resellerID, 7_000_000, f.now)),
		http.StatusUnprocessableEntity, "invalid_reseller")

	// The instant in the body is not taken: a body that could set it would be a
	// body that could rewrite when a reseller was created.
	body := resellerTermsBody(t, created.OrganisationID, 7_000_000, f.now.Add(-90*24*time.Hour),
		WireResellerRate{Row: PolicyTeamMonthly, DailyRateMicros: 250},
		WireResellerRate{Row: PolicyTeamAnnual, DailyRateMicros: 3_000},
	)
	rec := f.apiAsCaller(http.MethodPut, resellerTermsSetPathOf(created.OrganisationID), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("operations setting a reseller's terms answered %d: %s", rec.Code, rec.Body)
	}
	terms := readAnswer[WireReseller](f, rec)
	if terms.CreditLimitMicros != 7_000_000 {
		t.Errorf("the answer carries the credit limit %d, want 7000000", terms.CreditLimitMicros)
	}
	if len(terms.Rates) != 2 || terms.Rates[0].Row != PolicyTeamMonthly || terms.Rates[0].DailyRateMicros != 250 ||
		terms.Rates[1].Row != PolicyTeamAnnual || terms.Rates[1].DailyRateMicros != 3_000 {
		t.Errorf("the answer carries the rates %+v, want the two that were set", terms.Rates)
	}
	if !terms.CreatedAt.Equal(created.CreatedAt) {
		t.Errorf("the answer carries created_at %s, want the organisation's own instant %s", terms.CreatedAt, created.CreatedAt)
	}
	stored, found := f.terms(created.OrganisationID)
	if !found || stored.CreditLimitMicros != terms.CreditLimitMicros || len(stored.Rates) != 2 {
		t.Errorf("the stored terms are %+v (found %v), want what the answer carries (%+v)", stored, found, terms)
	}

	// The reseller reads its own: D2189's limit is the number it works inside,
	// and a plain member reads it as they read the customers (D2186, D2195).
	f.seed(membershipsCollection, membershipID(created.OrganisationID, sellerUID), WireMembership{
		OrganisationID: created.OrganisationID, UID: sellerUID, Email: sellerMail, Role: roleMember, CreatedAt: f.now,
	})
	f.actsAs(sellerUID, sellerMail, signInGoogle)
	own := f.apiAsCaller(http.MethodGet, apiResellerTermsPath, "")
	if own.Code != http.StatusOK {
		t.Fatalf("a reseller member reading its own terms answered %d: %s", own.Code, own.Body)
	}
	memberView := readAnswer[WireReseller](f, own)
	if memberView.CreditLimitMicros != 7_000_000 || len(memberView.Rates) != 2 {
		t.Errorf("the reseller's own read answers %+v, want the terms operations set", memberView)
	}

	// Setting terms is operations' act and nobody else's, and it is a change:
	// D2187's fresh sign-in is asked with the role.
	readAPIRefusal(t, f.apiAsCaller(http.MethodPut, resellerTermsSetPathOf(created.OrganisationID), body),
		http.StatusForbidden, "not_operations")

	f.actsAs(opsUID, opsEmail, signInGoogle)
	cases := []struct {
		name string
		body string
		code string
	}{
		{name: "a credit limit below zero", code: "invalid_terms",
			body: resellerTermsBody(t, created.OrganisationID, -1, f.now)},
		{name: "a rate below zero", code: "invalid_terms",
			body: resellerTermsBody(t, created.OrganisationID, 0, f.now,
				WireResellerRate{Row: PolicyTeamMonthly, DailyRateMicros: -1})},
		{name: "a row the policy table does not carry", code: "invalid_row",
			body: resellerTermsBody(t, created.OrganisationID, 0, f.now,
				WireResellerRate{Row: "team-fortnightly", DailyRateMicros: 1})},
		{name: "one row priced twice", code: "invalid_terms",
			body: resellerTermsBody(t, created.OrganisationID, 0, f.now,
				WireResellerRate{Row: PolicyTeamMonthly, DailyRateMicros: 1},
				WireResellerRate{Row: PolicyTeamMonthly, DailyRateMicros: 2})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			readAPIRefusal(t, f.apiAsCaller(http.MethodPut, resellerTermsSetPathOf(created.OrganisationID), tc.body),
				http.StatusUnprocessableEntity, tc.code)
		})
	}
	// A refused set changes nothing: the terms in the store are the ones the
	// admitted set wrote.
	after, _ := f.terms(created.OrganisationID)
	if after.CreditLimitMicros != 7_000_000 || len(after.Rates) != 2 {
		t.Errorf("a refused set left the terms %+v, want the 7000000 the admitted set wrote", after)
	}

	// An id that is a customer organisation is not a reseller: the same answer
	// an id nobody has gets, because which ids are which organisations is not
	// something this service says.
	customer := f.signedUp(danaUID, danaEmail, "Example Ltd")
	f.actsAs(opsUID, opsEmail, signInGoogle)
	readAPIRefusal(t, f.apiAsCaller(http.MethodPut, resellerTermsSetPathOf(customer.ID),
		resellerTermsBody(t, customer.ID, 1, f.now)),
		http.StatusNotFound, "not_found")
	readAPIRefusal(t, f.apiAsCaller(http.MethodPut, resellerTermsSetPathOf("org-nobody"),
		resellerTermsBody(t, "org-nobody", 1, f.now)),
		http.StatusNotFound, "not_found")
}

// TestResellerSeesOnlyItsCustomers: the list is the caller's own (D2191's link,
// read from the token and never from a body), a reseller reads its customers'
// keys and no other customer's, a plain member reads and decides nothing, and a
// reseller's people sign in by Google or Microsoft and not by email link (D2207,
// F10).
func TestResellerSeesOnlyItsCustomers(t *testing.T) {
	f := newFixture(t)
	mine := f.resellerOrg(resellerID, "Partner Ltd", sellerUID, sellerMail, roleOwner)
	rival := f.resellerOrg(rivalID, "Rival Ltd", erinUID, erinEmail, roleOwner)

	f.actsAs(sellerUID, sellerMail, signInGoogle)
	bought := f.soldBy("Example Ltd")
	if bought.Type != orgTypeCustomer {
		t.Errorf("the customer organisation's type is %q, want %q", bought.Type, orgTypeCustomer)
	}
	if bought.ResellerID == nil || *bought.ResellerID != mine.ID {
		t.Errorf("the customer organisation carries the reseller link %v, want %s", bought.ResellerID, mine.ID)
	}
	f.soldBy("Second Ltd")

	mineList := f.customersOf()
	if len(mineList) != 2 {
		t.Fatalf("the reseller's list holds %d customer(s), want the two it created: %+v", len(mineList), mineList)
	}
	for _, customer := range mineList {
		if customer.ResellerOrganisationID != mine.ID {
			t.Errorf("the list carries %s as a customer of %s, want only this reseller's", customer.OrganisationID, customer.ResellerOrganisationID)
		}
		if customer.Name == "" || customer.LinkedAt.IsZero() {
			t.Errorf("the row %+v names no customer or no link instant", customer)
		}
	}

	f.actsAs(erinUID, erinEmail, signInGoogle)
	if held := f.customersOf(); len(held) != 0 {
		t.Errorf("a reseller that sold to nobody holds %+v, want an empty list", held)
	}
	theirs := f.soldBy("Rival's customer")
	if theirs.ResellerID == nil || *theirs.ResellerID != rival.ID {
		t.Errorf("the rival's customer carries the link %v, want %s", theirs.ResellerID, rival.ID)
	}
	if held := f.customersOf(); len(held) != 1 || held[0].OrganisationID != theirs.ID {
		t.Errorf("the rival's list holds %+v, want only its own customer %s", held, theirs.ID)
	}

	// Its customers' keys it reads; another reseller's it does not, and an id
	// nobody has is the same answer.
	f.actsAs(sellerUID, sellerMail, signInGoogle)
	readAPIRefusal(t, f.apiAsCaller(http.MethodGet, resellerKeysPathOf(theirs.ID), ""),
		http.StatusNotFound, "not_found")
	readAPIRefusal(t, f.apiAsCaller(http.MethodGet, resellerKeysPathOf("org-nobody"), ""),
		http.StatusNotFound, "not_found")
	readAPIRefusal(t, f.apiAsCaller(http.MethodGet, resellerKeysPathOf(mine.ID), ""),
		http.StatusNotFound, "not_found")

	// A member reads the list and decides nothing: the customer organisation is
	// an owner's or an admin's act (D2186, D2195).
	f.setRole(mine.ID, sellerUID, roleMember)
	if held := f.customersOf(); len(held) != 2 {
		t.Errorf("a member's read of the list holds %d customer(s), want the same two", len(held))
	}
	readAPIRefusal(t, f.apiAsCaller(http.MethodPost, apiResellerCustomersPath, `{"name":"A third"}`),
		http.StatusForbidden, "not_permitted")

	// D2207, F10: the rule follows the membership the request uses.
	f.actsAs(sellerUID, sellerMail, signInEmailLink)
	readAPIRefusal(t, f.apiAsCaller(http.MethodGet, apiResellerCustomersPath, ""),
		http.StatusForbidden, "sso_required")
}

// TestResellerNeverSeesASecret: F5 — a reseller reads its customers' key
// metadata and never a key. The key and its renewal secret here are REAL (a
// licence bound to a project mints one, and the fixture keeps the whole bundle),
// because a check against material that is not there checks nothing.
func TestResellerNeverSeesASecret(t *testing.T) {
	f := newRevealFixture(t)
	if len(f.mints.answers) != 1 {
		t.Fatalf("the binding minted %d key(s), want the one whose material this case looks for", len(f.mints.answers))
	}
	predecessor := f.mints.answers[0]
	f.resellerOrg(resellerID, "Partner Ltd", sellerUID, sellerMail, roleOwner)

	// The customer organisation this fixture built is linked to the reseller
	// through operations' own route, which is the one way a link is made.
	f.operationsOrg(roleOwner)
	rec := f.apiAsCaller(http.MethodPost, apiResellerRelinkPath, relinkBody(t, f.org.ID, resellerID))
	if rec.Code != http.StatusCreated {
		t.Fatalf("operations linking a customer to a reseller answered %d: %s", rec.Code, rec.Body)
	}

	f.actsAs(sellerUID, sellerMail, signInGoogle)
	keys := f.apiAsCaller(http.MethodGet, resellerKeysPathOf(f.org.ID), "")
	if keys.Code != http.StatusOK {
		t.Fatalf("a reseller reading its customer's keys answered %d: %s", keys.Code, keys.Body)
	}
	metadata := readAnswer[[]WireKeyMetadata](f.fixture, keys)
	if len(metadata) != 1 {
		t.Fatalf("the metadata read answers %d key(s), want the one the binding minted: %+v", len(metadata), metadata)
	}
	if metadata[0].KeyID != predecessor.Record.KeyID {
		t.Errorf("the metadata names the key %s, want %s", metadata[0].KeyID, predecessor.Record.KeyID)
	}
	if metadata[0].Seats != 3 || metadata[0].Tier == "" {
		t.Errorf("the metadata is %+v, want it to describe what the customer bought", metadata[0])
	}
	body := keys.Body.String()
	for _, secret := range []string{predecessor.Key, predecessor.RenewalSecret} {
		if secret != "" && strings.Contains(body, secret) {
			t.Errorf("a key's own material reached the reseller's read: %s", secret)
		}
	}
	for _, forbidden := range []string{"secret", "sealed", "renewal"} {
		if strings.Contains(strings.ToLower(body), forbidden) {
			t.Errorf("the reseller's metadata answer carries %q", forbidden)
		}
	}

	// And the two routes that do hand material over are refused: the reveal
	// rotates the customer's key (P3.2) and belongs to the customer
	// organisation's own owners and admins.
	readAPIRefusal(t, f.apiAsCaller(http.MethodPost, apiKeysRevealPath,
		`{"workspace_id":`+jsonString(t, f.workspace.ID)+`}`),
		http.StatusNotFound, "not_found")
	readAPIRefusal(t, f.apiAsCaller(http.MethodGet, apiKeysLicencePath+"?workspace_id="+f.workspace.ID, ""),
		http.StatusNotFound, "not_found")
	if len(f.mints.answers) != 1 {
		t.Errorf("a refused reveal minted a key: the deployment holds %d answer(s), want the binding's one", len(f.mints.answers))
	}
}

// TestResellerIsNotAMemberOfItsCustomers: F40 — the reseller creates the
// customer's organisation and is never a member of it. The organisation it makes
// starts empty, the reseller's own owner holds no membership of it and does not
// see it in the switcher, and when the customer's first owner accepts the
// invitation what arrives is THEIR membership: the reseller's owner still holds
// none, and the customer's organisation still does not appear in the reseller's
// list of its own organisations.
func TestResellerIsNotAMemberOfItsCustomers(t *testing.T) {
	f := newFixture(t)
	mine := f.resellerOrg(resellerID, "Partner Ltd", sellerUID, sellerMail, roleOwner)

	f.actsAs(sellerUID, sellerMail, signInGoogle)
	bought := f.soldBy("Example Ltd")
	if bought.ResellerID == nil || *bought.ResellerID != mine.ID {
		t.Fatalf("the customer organisation does not carry the reseller link: %+v", bought)
	}
	members, err := membersOf(context.Background(), f.docs, bought.ID)
	if err != nil {
		t.Fatalf("reading the customer organisation's members: %v", err)
	}
	if len(members) != 0 {
		t.Errorf("the customer organisation holds %d member(s) the moment it is created, want none: the reseller is never one of them (F40)", len(members))
	}
	if _, found := f.membership(bought.ID, sellerUID); found {
		t.Error("the reseller's own owner became a member of the customer organisation it created")
	}
	for _, org := range f.listOrgs() {
		if org.ID == bought.ID {
			t.Error("the customer organisation appears in the reseller's own list of organisations: it is another tenant")
		}
	}

	invitation := readAnswer[WireInvite](f, f.firstOwnerInvite(bought.ID, buyerMail, roleOwner))
	accepted := f.acceptAs(buyerUID, buyerMail, signInGoogle, invitation.ID)
	if accepted.Code != http.StatusOK {
		t.Fatalf("the customer's first owner accepting answered %d: %s", accepted.Code, accepted.Body)
	}
	// The membership that arrived is the customer's own person's.
	if member, found := f.membership(bought.ID, buyerUID); !found || member.Role != roleOwner {
		t.Errorf("the accepted first owner's membership is %+v (found %v), want an owner of %s", member, found, bought.ID)
	}
	f.actsAs(sellerUID, sellerMail, signInGoogle)
	if _, found := f.membership(bought.ID, sellerUID); found {
		t.Error("the reseller's own owner is a member of the customer organisation after the first owner accepted")
	}
	for _, org := range f.listOrgs() {
		if org.ID == bought.ID {
			t.Error("the customer organisation appears in the reseller's own list of organisations after its first owner accepted")
		}
	}
	// And the people inside it are the customer's: the reseller's owner is not
	// among them, and the customer's owner is the only one.
	members, err = membersOf(context.Background(), f.docs, bought.ID)
	if err != nil {
		t.Fatalf("reading the customer organisation's members: %v", err)
	}
	if len(members) != 1 || members[0].UID != buyerUID {
		t.Errorf("the customer organisation holds %+v, want only its first owner", members)
	}

	// The other direction of the same line: a customer's owner is not one of a
	// reseller's people and reaches none of this area's routes.
	f.actsAs(buyerUID, buyerMail, signInGoogle)
	readAPIRefusal(t, f.apiAsCaller(http.MethodGet, apiResellerCustomersPath, ""),
		http.StatusForbidden, "not_reseller")
	readAPIRefusal(t, f.apiAsCaller(http.MethodGet, apiResellerTermsPath, ""),
		http.StatusForbidden, "not_reseller")
	readAPIRefusal(t, f.apiAsCaller(http.MethodGet, resellerKeysPathOf(bought.ID), ""),
		http.StatusForbidden, "not_reseller")
	// Its own organisation it still reads, through the routes that are the
	// organisation's own: the reseller's area is not one of them.
	if _, found := f.organisation(bought.ID); !found {
		t.Errorf("the store holds no organisation %s after its first owner accepted", bought.ID)
	}
	f.actsAs(buyerUID, buyerMail, signInGoogle)
	if rec := f.apiAsCaller(http.MethodGet, apiOrgMembersPathOf(bought.ID), ""); rec.Code != http.StatusOK {
		t.Errorf("the customer's own owner reading its organisation's members answered %d: %s", rec.Code, rec.Body)
	}
}

// TestRelinkIsAudited: D2191 — operations holds the link and can move it, the
// move is written to the log before it is made, and it moves the link and not
// money: the ledger's lines carry the channel they had when they were written.
func TestRelinkIsAudited(t *testing.T) {
	f := newFixture(t)
	first := f.resellerOrg(resellerID, "First Ltd", sellerUID, sellerMail, roleOwner)
	second := f.resellerOrg(rivalID, "Second Ltd", erinUID, erinEmail, roleOwner)

	f.actsAs(sellerUID, sellerMail, signInGoogle)
	bought := f.soldBy("Example Ltd")

	// A reseller cannot move a customer: only operations sets this link, and a
	// reseller that could claim a customer could claim its accrual.
	readAPIRefusal(t, f.apiAsCaller(http.MethodPost, apiResellerRelinkPath, relinkBody(t, bought.ID, second.ID)),
		http.StatusForbidden, "not_operations")

	ledgerBefore := len(f.ledger(t))
	f.operationsOrg(roleOwner)
	f.advance(time.Minute)
	rec := f.apiAsCaller(http.MethodPost, apiResellerRelinkPath, relinkBody(t, bought.ID, second.ID))
	if rec.Code != http.StatusCreated {
		t.Fatalf("operations relinking a customer answered %d: %s", rec.Code, rec.Body)
	}
	moved := readAnswer[WireResellerCustomer](f, rec)
	if moved.OrganisationID != bought.ID || moved.ResellerOrganisationID != second.ID {
		t.Errorf("the relink answered %+v, want %s linked to %s", moved, bought.ID, second.ID)
	}
	if moved.Name != "Example Ltd" {
		t.Errorf("the relink answered the name %q, want the customer's own", moved.Name)
	}
	if !moved.LinkedAt.Equal(f.now) {
		t.Errorf("the relink answered linked_at %s, want the instant it was made (%s): a relink writes a new one", moved.LinkedAt, f.now)
	}
	org, found := f.organisation(bought.ID)
	if !found || org.ResellerID == nil || *org.ResellerID != second.ID {
		t.Errorf("the customer organisation carries the link %+v, want %s: the two halves are one fact", org.ResellerID, second.ID)
	}
	stored, found := f.link(bought.ID)
	if !found || stored.ResellerOrganisationID != second.ID || !stored.LinkedAt.Equal(f.now) {
		t.Errorf("the stored link is %+v (found %v), want the move", stored, found)
	}

	// Each reseller's list follows the link: the customer is one reseller's at a
	// time.
	f.actsAs(sellerUID, sellerMail, signInGoogle)
	if held := f.customersOf(); len(held) != 0 {
		t.Errorf("the reseller the customer left still holds %+v", held)
	}
	f.actsAs(erinUID, erinEmail, signInGoogle)
	if held := f.customersOf(); len(held) != 1 || held[0].OrganisationID != bought.ID {
		t.Errorf("the reseller the customer moved to holds %+v, want %s", held, bought.ID)
	}

	lines := f.auditedAction(actionResellerRelink)
	if len(lines) != 1 {
		t.Fatalf("the relink wrote %d %s line(s), want one (D456)", len(lines), actionResellerRelink)
	}
	line := lines[0]
	if line.ActorEmail != opsEmail || line.Read {
		t.Errorf("the relink's line is %+v, want the operations caller's own act and not a read", line)
	}
	if line.OrganisationID == nil || *line.OrganisationID != bought.ID {
		t.Errorf("the relink's line names the organisation %v, want the customer %s", line.OrganisationID, bought.ID)
	}
	if line.TargetID == nil || *line.TargetID != second.ID {
		t.Errorf("the relink's line names the target %v, want the reseller it moved to %s", line.TargetID, second.ID)
	}
	// The move is a link and not money: nothing was charged, credited or posted.
	if after := len(f.ledger(t)); after != ledgerBefore {
		t.Errorf("the relink wrote %d ledger line(s), want none: what it changes is which reseller the accrual from here belongs to",
			after-ledgerBefore)
	}

	f.actsAs(opsUID, opsEmail, signInGoogle)
	cases := []struct {
		name string
		body string
		code string
		gone int
	}{
		{name: "the same reseller it is already linked to", code: "already_linked",
			body: relinkBody(t, bought.ID, second.ID)},
		{name: "a customer nobody has", code: "not_found", gone: http.StatusNotFound,
			body: relinkBody(t, "org-nobody", first.ID)},
		{name: "a reseller nobody has", code: "not_found", gone: http.StatusNotFound,
			body: relinkBody(t, bought.ID, "org-nobody")},
		{name: "a reseller organisation as the customer", code: "not_found", gone: http.StatusNotFound,
			body: relinkBody(t, first.ID, second.ID)},
		{name: "a customer organisation as the reseller", code: "not_found", gone: http.StatusNotFound,
			body: relinkBody(t, bought.ID, bought.ID)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status := http.StatusConflict
			if tc.gone != 0 {
				status = tc.gone
			}
			readAPIRefusal(t, f.apiAsCaller(http.MethodPost, apiResellerRelinkPath, tc.body), status, tc.code)
		})
	}
	// Nothing above moved it back: the customer is still the second reseller's.
	if held, _ := f.link(bought.ID); held.ResellerOrganisationID != second.ID {
		t.Errorf("a refused relink left the link %+v, want %s", held, second.ID)
	}
	if lines := f.auditedAction(actionResellerRelink); len(lines) != 1 {
		t.Errorf("the refusals wrote %d further %s line(s), want none: a refused act is not an act", len(lines)-1, actionResellerRelink)
	}
}

// TestResellerCannotInviteAfterTheFirstOwner: F40 — a reseller invites each
// customer's FIRST owner and nothing more. It is a refusal to invite a second
// person or another role, and it holds once the first owner has accepted: the
// organisation is its own people's from then on, and they invite from inside it
// through P2.2's route.
//
// What is NOT refused is inviting again after the invitation expired. Nobody but
// the reseller can invite into an organisation that has no member — the reseller
// is not one, and an act through the operations membership is refused
// (invites.go) — so refusing the reseller a second try would be an organisation
// nobody can ever reach.
func TestResellerCannotInviteAfterTheFirstOwner(t *testing.T) {
	f := newFixture(t)
	f.resellerOrg(resellerID, "Partner Ltd", sellerUID, sellerMail, roleOwner)
	f.actsAs(sellerUID, sellerMail, signInGoogle)
	bought := f.soldBy("Example Ltd")

	// A role that is not an owner, an address that is not one, and the
	// reseller's own people: three refusals before anything is written.
	readAPIRefusal(t, f.firstOwnerInvite(bought.ID, buyerMail, roleMember),
		http.StatusForbidden, "not_permitted")
	readAPIRefusal(t, f.firstOwnerInvite(bought.ID, "not an address", roleOwner),
		http.StatusUnprocessableEntity, "invalid_email")
	readAPIRefusal(t, f.firstOwnerInvite(bought.ID, sellerMail, roleOwner),
		http.StatusForbidden, "not_permitted")
	if held := f.invitationsOf(bought.ID); len(held) != 0 {
		t.Fatalf("a refused invitation was written anyway: %+v", held)
	}

	rec := f.firstOwnerInvite(bought.ID, buyerMail, roleOwner)
	if rec.Code != http.StatusCreated {
		t.Fatalf("the reseller inviting the customer's first owner answered %d: %s", rec.Code, rec.Body)
	}
	invitation := readAnswer[WireInvite](f, rec)
	if invitation.OrganisationID != bought.ID || invitation.Role != roleOwner || invitation.Email != buyerMail {
		t.Errorf("the invitation is %+v, want the customer's first owner", invitation)
	}
	if invitation.InvitedBy != sellerMail {
		t.Errorf("the invitation records the inviter %q, want the reseller's own person %s", invitation.InvitedBy, sellerMail)
	}
	if !invitation.ExpiresAt.Equal(f.now.Add(inviteTTL)) {
		t.Errorf("the invitation expires at %s, want %s", invitation.ExpiresAt, f.now.Add(inviteTTL))
	}
	if mail := f.lastMail(); mail.To != buyerMail || !strings.Contains(mail.Body, invitation.ID) {
		t.Errorf("the mail is %+v, want the link to the invitation for %s", mail, buyerMail)
	}
	// The act is the invitation's own and is in the log as one (P2.2's action),
	// written after the record and not after the mail.
	if lines := f.auditedAction(actionInvitationCreate); len(lines) != 1 || lines[0].ActorEmail != sellerMail {
		t.Errorf("the first owner's invitation wrote %+v, want one %s line by the reseller", lines, actionInvitationCreate)
	}

	// A second invitation into the same organisation, while the first stands.
	readAPIRefusal(t, f.firstOwnerInvite(bought.ID, "second@example.com", roleOwner),
		http.StatusConflict, "first_owner_set")
	// A member of the reseller cannot invite at all, at either attempt
	// (D2186, D2195).
	f.setRole(resellerID, sellerUID, roleMember)
	readAPIRefusal(t, f.firstOwnerInvite(bought.ID, "second@example.com", roleOwner),
		http.StatusForbidden, "not_permitted")
	f.setRole(resellerID, sellerUID, roleOwner)

	// The first owner accepts, and the reseller's part in the organisation is
	// done.
	accepted := f.acceptAs(buyerUID, buyerMail, signInGoogle, invitation.ID)
	if accepted.Code != http.StatusOK {
		t.Fatalf("the first owner accepting answered %d: %s", accepted.Code, accepted.Body)
	}
	f.actsAs(sellerUID, sellerMail, signInGoogle)
	readAPIRefusal(t, f.firstOwnerInvite(bought.ID, "third@example.com", roleOwner),
		http.StatusConflict, "first_owner_set")

	// The customer's own owner invites from inside its organisation, which is
	// what the reseller's part ending means.
	f.actsAs(buyerUID, buyerMail, signInGoogle)
	if rec := f.inviteAs(bought.ID, "colleague@example.com", roleMember); rec.Code != http.StatusCreated {
		t.Errorf("the customer's own owner inviting a colleague answered %d: %s", rec.Code, rec.Body)
	}

	// An invitation nobody accepted leaves the organisation reachable: once it
	// has expired there is no live one, so the reseller may invite again.
	f.actsAs(sellerUID, sellerMail, signInGoogle)
	second := f.soldBy("Second Ltd")
	if rec := f.firstOwnerInvite(second.ID, rivalBuyerMail, roleOwner); rec.Code != http.StatusCreated {
		t.Fatalf("inviting a second customer's first owner answered %d: %s", rec.Code, rec.Body)
	}
	f.advance(inviteTTL + time.Hour)
	again := f.firstOwnerInvite(second.ID, rivalBuyerMail, roleOwner)
	if again.Code != http.StatusCreated {
		t.Fatalf("inviting again after the first invitation expired answered %d: %s", again.Code, again.Body)
	}
	if held, found := f.invitation(readAnswer[WireInvite](f, again).ID); !found || held.State != invitePending {
		t.Errorf("the second invitation is %+v (found %v), want a pending one", held, found)
	}
}

// TestAnotherOrgsResellerCustomersAreRefused: the cross-tenant rule of the
// portal's conventions, for this area — an owner or an admin of another
// organisation acting on this reseller's customers is refused, and refused the
// way an id nobody has is refused, so that a stranger learns neither which
// customers exist nor who holds them. The reseller's own routes are the
// reseller's own: a customer organisation's owner, and an operations member who
// is not one of a reseller's people, reach none of them.
func TestAnotherOrgsResellerCustomersAreRefused(t *testing.T) {
	f := newFixture(t)
	mine := f.resellerOrg(resellerID, "Partner Ltd", sellerUID, sellerMail, roleOwner)
	f.resellerOrg(rivalID, "Rival Ltd", erinUID, erinEmail, roleOwner)

	f.actsAs(sellerUID, sellerMail, signInGoogle)
	bought := f.soldBy("Example Ltd")
	invitation := readAnswer[WireInvite](f, f.firstOwnerInvite(bought.ID, buyerMail, roleOwner))
	if invitation.OrganisationID != bought.ID {
		t.Fatalf("the first owner's invitation is for %s, want %s", invitation.OrganisationID, bought.ID)
	}

	// Another reseller, whose customer this is not.
	f.actsAs(erinUID, erinEmail, signInGoogle)
	readAPIRefusal(t, f.apiAsCaller(http.MethodGet, resellerKeysPathOf(bought.ID), ""),
		http.StatusNotFound, "not_found")
	readAPIRefusal(t, f.firstOwnerInvite(bought.ID, "someone@example.com", roleOwner),
		http.StatusNotFound, "not_found")
	for _, customer := range f.customersOf() {
		if customer.OrganisationID == bought.ID {
			t.Errorf("another reseller's list holds %s", bought.ID)
		}
	}

	// An owner of an ordinary customer organisation, and the operations
	// organisation's own member: neither is one of a reseller's people, so
	// neither reaches this area at all.
	if _, found := f.organisation(mine.ID); !found {
		t.Fatalf("the store holds no reseller organisation %s", mine.ID)
	}
	other := f.seededOrganisation("org-other", "Other Ltd", orgTypeCustomer, danaUID, danaEmail, roleOwner)
	f.actsAs(danaUID, danaEmail, signInGoogle)
	readAPIRefusal(t, f.apiAsCaller(http.MethodGet, resellerKeysPathOf(other.ID), ""),
		http.StatusForbidden, "not_reseller")
	f.operationsOrg(roleOwner)
	readAPIRefusal(t, f.apiAsCaller(http.MethodGet, apiResellerCustomersPath, ""),
		http.StatusForbidden, "not_reseller")
	readAPIRefusal(t, f.apiAsCaller(http.MethodGet, apiResellerTermsPath, ""),
		http.StatusForbidden, "not_reseller")
	// The operations team still acts on the reseller's money, which is the other
	// half of the same rule — and it is that act that is audited, not a read of
	// this area's own screens.
	terms := readAnswer[WireReseller](f, f.apiAsCaller(http.MethodPut, resellerTermsSetPathOf(mine.ID),
		resellerTermsBody(t, mine.ID, resellerLimit, f.now, WireResellerRate{Row: PolicyTeamMonthly, DailyRateMicros: resellerRate})))
	if terms.OrganisationID != mine.ID {
		t.Errorf("operations setting the reseller's terms answered %+v, want %s", terms, mine.ID)
	}
}
