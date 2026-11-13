// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The tenancy's own cases (portal plan P2.1): the sign-up that makes an
// organisation and records what it accepted, the hooks P3.1's entitlement is
// created through, the role that lives in a membership and not in a token, the
// SSO rule that follows the membership a request uses, the operations
// membership's reads across organisations and the log they leave, the last-used
// organisation, F35's daily limit, and one organisation's members being shut to
// another's.
//
// The fixture is the package's (keyservice_test.go): these cases drive the
// portal's routes through the handler, so what they read is what the SPA is
// answered, and they write records into the fixture's store only where the
// record is one no route of this area creates — the operations organisation is
// seeded as an invitation (P2.2) and a reseller is created by operations (P6.1).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

// The accounts these cases are about. None of them is a real address and none is
// secret-shaped: the verifier is a spy that answers whatever caller a case sets
// (keyservice_test.go).
const (
	danaUID   = "uid-dana"
	danaEmail = "dana@example.com"
	erinUID   = "uid-erin"
	erinEmail = "erin@example.com"
	opsUID    = "uid-operations"
	opsEmail  = "operations@fugaro.example"
	sellerUID = "uid-seller"
	sellerMail = "seller@partner.example"

	// portalIDToken is what these cases present as the bearer token. The spy
	// answers it with the caller the case set, so the string is a placeholder and
	// obviously not a real token.
	portalIDToken = "fake-id-token-0123456789"

	// signInEmailLink is the provider a passwordless sign-in is recorded under,
	// and the one D2207 refuses for an operations or a reseller membership
	// (portalauth.go's own list).
	signInEmailLink = "emailLink"

	// orgHookCollection is where this file's own organisation-created hook
	// writes. The free seat's entitlement is P3.1's record and not this file's,
	// so the hook here writes a record of its own into a collection of its own.
	orgHookCollection = "orgHookEntitlements"
)

// orgHookSpy is the test's own OrgCreatedHook: what it records is that it ran,
// what it was handed, and — through the transaction it was handed — that what it
// writes lands with the organisation. The entitlement P3.1 registers has this
// shape and this file does not.
type orgHookSpy struct {
	seen []WireOrganisation
	// refuse is a name to fail for, which is how a case reads that a hook's
	// failure takes the organisation with it.
	refuse string
	err    error
}

func (h *orgHookSpy) run(ctx context.Context, tx Tx, org WireOrganisation) error {
	h.seen = append(h.seen, org)
	if h.refuse != "" && h.refuse == org.Name {
		return h.err
	}
	return tx.Put(ctx, orgHookCollection, org.ID, Doc{"organisation_id": org.ID, "seats": 1})
}

func (h *orgHookSpy) reset() {
	h.seen, h.refuse, h.err = nil, "", nil
}

// testOrgHook is registered below, so the sign-up every case in this file drives
// runs a hook of the shape P3.1's is.
var testOrgHook = &orgHookSpy{}

func init() { registerOrgCreated("the test's own", testOrgHook.run) }

// refusingAuditDocs is the fixture's store with the audit collection refusing
// writes, which is how a case reads that a read across organisations is refused
// rather than answered when its line cannot be written (D456: the line is what
// makes the read accountable, and a log that loses one silently is a log nobody
// can rely on).
type refusingAuditDocs struct {
	Docs
	collection string
}

var errAuditRefused = errors.New("the audit log is not writable in this deployment")

func (d refusingAuditDocs) Put(ctx context.Context, collection, id string, doc Doc) error {
	if collection == d.collection {
		return errAuditRefused
	}
	return d.Docs.Put(ctx, collection, id, doc)
}

// actsAs makes the fixture's verifier answer this account, signed in the way
// named: what a case changes to make the next request somebody else's.
func (f *fixture) actsAs(uid, email, provider string) {
	f.signIn.caller = Caller{UID: uid, Email: email, Provider: provider, AuthTime: f.now}
}

// apiAs sends one request to the portal's API behind a sign-in, which is how the
// SPA calls it.
func (f *fixture) apiAs(method, path, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(method, intakeOrigin+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+portalIDToken)
	req.RemoteAddr = applicantIP
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

// signedUp signs one account up as a new organisation's owner and answers the
// organisation, the way the SPA does it.
func (f *fixture) signedUp(uid, email, name string) WireOrganisation {
	f.t.Helper()
	f.actsAs(uid, email, signInGoogle)
	rec := f.apiAs(http.MethodPost, apiOrgsPath, `{"name":`+jsonString(f.t, name)+`}`)
	if rec.Code != http.StatusCreated {
		f.t.Fatalf("signing %s up as %q answered %d: %s", email, name, rec.Code, rec.Body)
	}
	return readAnswer[WireOrganisation](f, rec)
}

// seed writes one record into the fixture's store, which is how a case stands up
// an organisation no route of this area creates.
func (f *fixture) seed(collection, id string, record any) {
	f.t.Helper()
	doc, err := storedDoc(record)
	if err != nil {
		f.t.Fatalf("building the record %s/%s: %v", collection, id, err)
	}
	if err := f.docs.Put(context.Background(), collection, id, doc); err != nil {
		f.t.Fatalf("writing the record %s/%s: %v", collection, id, err)
	}
}

// seededOrganisation is one organisation and one member of it, written straight
// into the store: the shape P2.2 seeds the operations organisation in, and the
// shape P6.1 creates a reseller in.
func (f *fixture) seededOrganisation(id, name, orgType, uid, email, role string) WireOrganisation {
	f.t.Helper()
	org := WireOrganisation{ID: id, Name: name, Type: orgType, CreatedAt: f.now}
	f.seed(organisationsCollection, org.ID, org)
	f.seed(membershipsCollection, membershipID(org.ID, uid), WireMembership{
		OrganisationID: org.ID, UID: uid, Email: email, Role: role, CreatedAt: f.now,
	})
	return org
}

// organisation reads one organisation out of the fixture's store.
func (f *fixture) organisation(id string) (WireOrganisation, bool) {
	f.t.Helper()
	return heldRecord[WireOrganisation](f, organisationsCollection, id)
}

// membership reads one membership out of the fixture's store.
func (f *fixture) membership(organisationID, uid string) (WireMembership, bool) {
	f.t.Helper()
	return heldRecord[WireMembership](f, membershipsCollection, membershipID(organisationID, uid))
}

// organisations is every organisation the store holds.
func (f *fixture) organisations() []WireOrganisation {
	f.t.Helper()
	orgs, err := recordsWhere[WireOrganisation](context.Background(), f.docs, organisationsCollection, Where{})
	if err != nil {
		f.t.Fatalf("reading the organisations: %v", err)
	}
	return orgs
}

// auditEntries is every entry the audit log holds, read the way P5.1's own reader
// will read it.
func (f *fixture) auditEntries() []WireAuditEntry {
	f.t.Helper()
	entries, err := recordsWhere[WireAuditEntry](context.Background(), f.docs, auditCollection, Where{})
	if err != nil {
		f.t.Fatalf("reading the audit log: %v", err)
	}
	return entries
}

// listOrgs reads the switcher's list as the account the verifier answers.
func (f *fixture) listOrgs() []WireOrganisation {
	f.t.Helper()
	rec := f.apiAs(http.MethodGet, apiOrgsPath, "")
	if rec.Code != http.StatusOK {
		f.t.Fatalf("the switcher's list answered %d: %s", rec.Code, rec.Body)
	}
	return readAnswer[WireOrgList](f, rec).Organisations
}

// profile is the role one stored membership carries, which is what the cases
// about roles change.
func (f *fixture) setRole(organisationID, uid, role string) {
	f.t.Helper()
	held, found := f.membership(organisationID, uid)
	if !found {
		f.t.Fatalf("there is no membership of %s for %s to change", organisationID, uid)
	}
	held.Role = role
	f.seed(membershipsCollection, membershipID(organisationID, uid), held)
}

func heldRecord[T any](f *fixture, collection, id string) (T, bool) {
	f.t.Helper()
	record, found, err := getRecord[T](context.Background(), f.docs, collection, id)
	if err != nil {
		f.t.Fatalf("reading %s/%s: %v", collection, id, err)
	}
	return record, found
}

// answer decodes one answer body into the shape the route wrote it as.
func readAnswer[T any](f *fixture, rec *httptest.ResponseRecorder) T {
	f.t.Helper()
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		f.t.Fatalf("reading the answer %q: %v", rec.Body.String(), err)
	}
	return out
}

// refusal is the code of a refusal body, which is what the SPA branches on.
func refusal(f *fixture, rec *httptest.ResponseRecorder) string {
	f.t.Helper()
	return readAnswer[apiError](f, rec).Error.Code
}

// jsonString is one Go string as a JSON string, so a case's body is built rather
// than concatenated by hand.
func jsonString(t *testing.T, value string) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("building a JSON string from %q: %v", value, err)
	}
	return string(raw)
}

// TestSignUpRecordsTermsAcceptance: the organisation a sign-up creates is a
// customer one, its name is the name it was given, and the terms of service the
// person accepted are recorded with the version the deployment serves and the
// instant of the sign-up (D2199). A body that names no name is refused, and a
// body naming a field the shape does not hold — a type, say — is refused for the
// shape and not read for the half of it this build knows.
func TestSignUpRecordsTermsAcceptance(t *testing.T) {
	f := newFixture(t)
	f.actsAs(danaUID, danaEmail, signInGoogle)

	rec := f.apiAs(http.MethodPost, apiOrgsPath, `{"name":"  Example Ltd  "}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("a sign-up answered %d: %s", rec.Code, rec.Body)
	}
	org := readAnswer[WireOrganisation](f, rec)
	switch {
	case org.ID == "":
		t.Error("the organisation has no id, and every record of it is filed by one")
	case org.Name != "Example Ltd":
		t.Errorf("the organisation is named %q, want the name with the space around it removed", org.Name)
	case org.Type != orgTypeCustomer:
		t.Errorf("a sign-up created a %q organisation, and a sign-up is always a customer's (D2191)", org.Type)
	case org.ResellerID != nil:
		t.Error("a sign-up created an organisation under a reseller, which only operations may do (D2191)")
	case !org.CreatedAt.Equal(f.now):
		t.Errorf("the organisation is dated %s, want the sign-up's own instant %s", org.CreatedAt, f.now)
	}
	if org.Terms == nil {
		t.Fatal("the organisation records no acceptance, and sign-up is where the terms are accepted (D2199)")
	}
	if org.Terms.Version != termsVersion {
		t.Errorf("the acceptance names the version %q, want the deployment's %q", org.Terms.Version, termsVersion)
	}
	if !org.Terms.AcceptedAt.Equal(f.now) {
		t.Errorf("the acceptance is dated %s, want the instant of the sign-up %s", org.Terms.AcceptedAt, f.now)
	}

	// What the answer says is what the store holds: an answer built from
	// anything but the record written would be one the next read disagrees with.
	stored, found := f.organisation(org.ID)
	if !found {
		t.Fatal("the organisation the sign-up answered is not in the store")
	}
	if stored.Terms == nil || *stored.Terms != *org.Terms || stored.Name != org.Name {
		t.Errorf("the store holds %+v, want the organisation the answer carried %+v", stored, org)
	}
	// And the person who signed up owns it.
	owner, found := f.membership(org.ID, danaUID)
	switch {
	case !found:
		t.Fatal("the sign-up created no membership, so the organisation has no owner")
	case owner.Role != roleOwner:
		t.Errorf("the founder's membership carries the role %q, want %q", owner.Role, roleOwner)
	case owner.Email != danaEmail:
		t.Errorf("the founder's membership carries the address %q, want the one the sign-in proved %q", owner.Email, danaEmail)
	case owner.LastUsedAt == nil || !owner.LastUsedAt.Equal(f.now):
		t.Errorf("the founder's membership records last use %v, want the sign-up itself: nobody chooses the organisation they have just made", owner.LastUsedAt)
	}

	// A body that names no name, names only space, or names a field the shape
	// does not hold is refused, and none of them creates anything.
	before := len(f.organisations())
	for _, tc := range []struct {
		name string
		body string
		code int
	}{
		{name: "a body with no name", body: `{}`, code: http.StatusBadRequest},
		{name: "a body naming a type", body: `{"name":"Example Ltd","type":"operations"}`, code: http.StatusBadRequest},
		{name: "a name of only space", body: `{"name":"   "}`, code: http.StatusUnprocessableEntity},
		{name: "a name over the schema's bound", body: `{"name":` + jsonString(t, strings.Repeat("a", maxOrganisationName+1)) + `}`, code: http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.apiAs(http.MethodPost, apiOrgsPath, tc.body)
			if rec.Code != tc.code {
				t.Errorf("a sign-up with %s answered %d (%s), want %d", tc.name, rec.Code, rec.Body, tc.code)
			}
			if tc.code != http.StatusCreated && len(f.organisations()) != before {
				t.Errorf("a sign-up refused with %s created an organisation anyway", tc.name)
			}
		})
	}

	// The version recorded is the deployment's and not a figure this build
	// carries: a deployment serving another wording records that one, and the
	// terms an organisation names are therefore the terms its people were shown.
	other := newFixtureWith(t, Production, func(f *fixture) { f.cfg.TermsVersion = "2027-01-05" })
	other.actsAs(danaUID, danaEmail, signInGoogle)
	moved := readAnswer[WireOrganisation](other, other.apiAs(http.MethodPost, apiOrgsPath, `{"name":"Example Ltd"}`))
	if moved.Terms == nil || moved.Terms.Version != "2027-01-05" {
		t.Errorf("a deployment serving the terms %q recorded %+v, want that version", "2027-01-05", moved.Terms)
	}
}

// TestOrgCreatedHooksRun: the hooks registered for a new organisation run inside
// the sign-up's own transaction, they are handed the organisation as it is being
// created, what they write lands with it, and a hook that fails means there is no
// organisation at all — which is what keeps an organisation from existing without
// the free seat P3.1's hook creates (D2199, F6).
func TestOrgCreatedHooksRun(t *testing.T) {
	f := newFixture(t)
	testOrgHook.reset()
	f.actsAs(danaUID, danaEmail, signInGoogle)

	org := f.signedUp(danaUID, danaEmail, "Example Ltd")
	if len(testOrgHook.seen) != 1 {
		t.Fatalf("the hooks ran %d time(s) for one sign-up, want once", len(testOrgHook.seen))
	}
	handed := testOrgHook.seen[0]
	switch {
	case handed.ID != org.ID:
		t.Errorf("the hook was handed the organisation %q, want the one being created %q", handed.ID, org.ID)
	case handed.Type != orgTypeCustomer:
		t.Errorf("the hook was handed a %q organisation, want a customer one: the hooks run for every organisation", handed.Type)
	case handed.Terms == nil || handed.Terms.Version != termsVersion:
		t.Errorf("the hook was handed the terms %+v, want the acceptance the sign-up records, which is what an entitlement is keyed on", handed.Terms)
	}
	if _, written := heldRecord[Doc](f, orgHookCollection, org.ID); !written {
		t.Error("the record the hook wrote is not in the store, so the hook did not run inside the sign-up's transaction")
	}

	// A hook that fails takes the organisation with it: no organisation, no
	// owner's membership, and nothing the hook wrote.
	before := len(f.organisations())
	testOrgHook.refuse, testOrgHook.err = "Doomed Ltd", errors.New("the free seat could not be minted")
	rec := f.apiAs(http.MethodPost, apiOrgsPath, `{"name":"Doomed Ltd"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("a sign-up whose hook failed answered %d (%s), want this service's own failure", rec.Code, rec.Body)
	}
	if len(f.organisations()) != before {
		t.Error("a sign-up whose hook failed created the organisation anyway, so an organisation can exist without what the hook writes")
	}
	if _, found := heldRecord[WireMembership](f, membershipsCollection, membershipID(org.ID, danaUID)); !found {
		t.Error("the failed sign-up took the first organisation's owner with it, so the rollback reached past its own transaction")
	}
	// And the failure did not spend a place of the day's limit: the count is of
	// organisations that exist.
	testOrgHook.refuse, testOrgHook.err = "", nil
	if created := f.signedUp(danaUID, danaEmail, "Third Ltd"); created.Name != "Third Ltd" {
		t.Errorf("the sign-up after a failed one answered %+v, want the organisation it asked for", created)
	}
}

// TestRolesLiveInMemberships: what a person may do is what their membership says
// and never what their token says. One sign-in — one account, one provider, one
// token — is refused the member list as a member and admitted to it the moment
// the stored role says admin, with nothing else about the request changed (D2186,
// D2195).
func TestRolesLiveInMemberships(t *testing.T) {
	f := newFixture(t)
	org := f.signedUp(danaUID, danaEmail, "Example Ltd")

	owner, found := f.membership(org.ID, danaUID)
	if !found || owner.Role != roleOwner {
		t.Fatalf("the founder's membership is %+v, want one carrying the role %q", owner, roleOwner)
	}
	// A person invited as a member belongs to the organisation and decides
	// nothing in it: their membership is the record the invitation wrote.
	f.seed(membershipsCollection, membershipID(org.ID, erinUID), WireMembership{
		OrganisationID: org.ID, UID: erinUID, Email: erinEmail, Role: roleMember, CreatedAt: f.now,
	})
	f.actsAs(erinUID, erinEmail, signInGoogle)
	if rec := f.apiAs(http.MethodGet, apiOrgPathOf(org.ID), ""); rec.Code != http.StatusOK {
		t.Errorf("a member reading their own organisation was answered %d (%s), want it: a member works in the product", rec.Code, rec.Body)
	}
	members := f.apiAs(http.MethodGet, apiOrgMembersPathOf(org.ID), "")
	if members.Code != http.StatusForbidden {
		t.Fatalf("a member reading the member list was answered %d (%s), want the refusal D2186 makes of it", members.Code, members.Body)
	}
	if code := refusal(f, members); code != "not_permitted" {
		t.Errorf("the refusal carries the code %q, want not_permitted", code)
	}

	// The same sign-in, and the only thing that changed is the stored role.
	f.setRole(org.ID, erinUID, roleAdmin)
	after := f.apiAs(http.MethodGet, apiOrgMembersPathOf(org.ID), "")
	if after.Code != http.StatusOK {
		t.Fatalf("an admin reading the member list was answered %d (%s), want it: the role is the membership's", after.Code, after.Body)
	}
	list := readAnswer[WireOrgMembers](f, after).Members
	if len(list) != 2 || !slices.ContainsFunc(list, func(m WireMembership) bool { return m.UID == danaUID }) ||
		!slices.ContainsFunc(list, func(m WireMembership) bool { return m.UID == erinUID }) {
		t.Errorf("the member list holds %+v, want the organisation's two members", list)
	}
	// Nothing about the request changed between the two answers but the record:
	// the token presented is one and the same placeholder string, the account is
	// the same, and the verifier answers one caller — a role read out of any of
	// those would have answered the two the same way.
	//
	// And taking the role away takes the access away.
	f.setRole(org.ID, erinUID, roleMember)
	if back := f.apiAs(http.MethodGet, apiOrgMembersPathOf(org.ID), ""); back.Code != http.StatusForbidden {
		t.Errorf("a member demoted from admin was answered %d (%s), want the refusal again", back.Code, back.Body)
	}
}

// TestSSORuleFollowsTheActorsMembership: the SSO rule follows the membership a
// request USES and not the organisation it is about (D2207, F10). An operations
// member who signed in by email link is refused every request that membership
// carries — including one about a customer organisation — and the same person
// signed in with Google reads it; a reseller's member is refused the same way
// until a directory is pinned; and a customer organisation is not one the rule
// follows at all.
func TestSSORuleFollowsTheActorsMembership(t *testing.T) {
	f := newFixture(t)
	operations := f.seededOrganisation("org_operations", "Operations", orgTypeOperations, opsUID, opsEmail, roleMember)
	reseller := f.seededOrganisation("org_reseller", "Partner Ltd", orgTypeReseller, sellerUID, sellerMail, roleMember)
	customer := f.signedUp(danaUID, danaEmail, "Example Ltd")

	// The operations membership, signed in by email link: refused, and refused
	// for the membership's own organisation and for somebody else's alike.
	f.actsAs(opsUID, opsEmail, signInEmailLink)
	for _, tc := range []struct {
		name   string
		method string
		path   string
	}{
		{name: "the operations organisation itself", method: http.MethodGet, path: apiOrgPathOf(operations.ID)},
		{name: "a customer organisation", method: http.MethodGet, path: apiOrgPathOf(customer.ID)},
		{name: "a customer organisation's members", method: http.MethodGet, path: apiOrgMembersPathOf(customer.ID)},
		// Selecting is the one route that uses a membership of its own subject
		// — a person selects an organisation they belong to — so the rule it
		// follows is that membership's: the operations organisation's.
		{name: "selecting the operations organisation", method: http.MethodPost, path: apiOrgSelectPathOf(operations.ID)},
	} {
		t.Run(tc.name+" by email link", func(t *testing.T) {
			rec := f.apiAs(tc.method, tc.path, "")
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s was answered %d (%s), want the SSO refusal", tc.name, rec.Code, rec.Body)
			}
			if code := refusal(f, rec); code != "sso_required" {
				t.Errorf("the refusal carries the code %q, want sso_required, which is the dialog the spec describes", code)
			}
		})
	}

	// The same person, signed in with Google: admitted.
	f.actsAs(opsUID, opsEmail, signInGoogle)
	read := f.apiAs(http.MethodGet, apiOrgPathOf(customer.ID), "")
	if read.Code != http.StatusOK {
		t.Fatalf("an operations member signed in with Google was answered %d (%s), want the organisation", read.Code, read.Body)
	}
	if got := readAnswer[WireOrganisation](f, read); got.ID != customer.ID {
		t.Errorf("the read answered the organisation %q, want %q", got.ID, customer.ID)
	}

	// A reseller's member is refused the same way until a directory is pinned
	// (D2207's fallback: Google only, which is the rule's zero value).
	f.actsAs(sellerUID, sellerMail, signInEmailLink)
	if rec := f.apiAs(http.MethodGet, apiOrgPathOf(reseller.ID), ""); rec.Code != http.StatusForbidden {
		t.Errorf("a reseller's member by email link was answered %d (%s), want the SSO refusal", rec.Code, rec.Body)
	}
	f.actsAs(sellerUID, sellerMail, signInGoogle)
	if rec := f.apiAs(http.MethodGet, apiOrgPathOf(reseller.ID), ""); rec.Code != http.StatusOK {
		t.Errorf("a reseller's member signed in with Google was answered %d (%s), want the organisation", rec.Code, rec.Body)
	}

	// Nothing follows a customer organisation: the person who signed up by email
	// link reads the organisation they made.
	f.actsAs(danaUID, danaEmail, signInEmailLink)
	if rec := f.apiAs(http.MethodGet, apiOrgPathOf(customer.ID), ""); rec.Code != http.StatusOK {
		t.Errorf("a customer reading their own organisation by email link was answered %d (%s), want it: the rule follows operations and reseller memberships and no other", rec.Code, rec.Body)
	}
}

// TestOperationsReadsAreAudited: the operations membership's reads across
// organisations are written to the audit log by the act that makes them, with the
// actor, the action, the organisation read and the read flag (D456, F9). A person
// reading their own organisation writes nothing, a read that is refused writes
// nothing, and a read whose line cannot be written is refused rather than
// answered.
func TestOperationsReadsAreAudited(t *testing.T) {
	f := newFixture(t)
	f.seededOrganisation("org_operations", "Operations", orgTypeOperations, opsUID, opsEmail, roleMember)
	customer := f.signedUp(danaUID, danaEmail, "Example Ltd")

	// The customer's own read is nobody's business but theirs.
	f.actsAs(danaUID, danaEmail, signInGoogle)
	if rec := f.apiAs(http.MethodGet, apiOrgPathOf(customer.ID), ""); rec.Code != http.StatusOK {
		t.Fatalf("the customer's own read was answered %d (%s)", rec.Code, rec.Body)
	}
	if entries := f.auditEntries(); len(entries) != 0 {
		t.Errorf("a person reading their own organisation wrote %d audit line(s): the log is the operations team's, not every member's", len(entries))
	}

	// The operations membership's read of somebody else's organisation.
	f.actsAs(opsUID, opsEmail, signInGoogle)
	if rec := f.apiAs(http.MethodGet, apiOrgPathOf(customer.ID), ""); rec.Code != http.StatusOK {
		t.Fatalf("the operations read was answered %d (%s)", rec.Code, rec.Body)
	}
	entries := f.auditEntries()
	if len(entries) != 1 {
		t.Fatalf("the operations read wrote %d audit line(s), want one", len(entries))
	}
	entry := entries[0]
	switch {
	case entry.Action != actionOrganisationRead:
		t.Errorf("the line's action is %q, want %q", entry.Action, actionOrganisationRead)
	case !entry.Read:
		t.Error("the line is not marked as a read, and a write is what an incident review looks for while a read is what a privacy question asks about")
	case entry.ActorUID != opsUID || entry.ActorEmail != opsEmail:
		t.Errorf("the line names the actor %q <%s>, want the operations member %q <%s>", entry.ActorUID, entry.ActorEmail, opsUID, opsEmail)
	case entry.OrganisationID == nil || *entry.OrganisationID != customer.ID:
		t.Errorf("the line names the organisation %v, want the one that was read %q", entry.OrganisationID, customer.ID)
	case entry.ID == "" || entry.At.IsZero():
		t.Errorf("the line carries the id %q and the instant %s, want both", entry.ID, entry.At)
	}

	// The member list is an action of its own, so a reader can tell the two
	// reads apart without reading the action's name for a verb.
	if rec := f.apiAs(http.MethodGet, apiOrgMembersPathOf(customer.ID), ""); rec.Code != http.StatusOK {
		t.Fatalf("the operations member read was answered %d (%s)", rec.Code, rec.Body)
	}
	entries = f.auditEntries()
	if len(entries) != 2 || !slices.ContainsFunc(entries, func(e WireAuditEntry) bool { return e.Action == actionOrganisationMembersRead }) {
		t.Errorf("the log holds %+v, want the member read written under %q", entries, actionOrganisationMembersRead)
	}

	// A read that is refused wrote nothing: the log is of what happened, and an
	// organisation the caller may not read is one that was not read.
	before := len(f.auditEntries())
	if rec := f.apiAs(http.MethodGet, apiOrgPathOf("org_nobody"), ""); rec.Code != http.StatusNotFound {
		t.Fatalf("a read of an organisation that is not there answered %d (%s), want the not-found refusal", rec.Code, rec.Body)
	}
	if len(f.auditEntries()) != before {
		t.Error("a refused read wrote an audit line, so the log says an organisation was read that never was")
	}

	// And a read whose line cannot be written is not answered: the entry is what
	// makes the read accountable, and an accountability that fails silently is
	// none.
	f.deps.Docs = refusingAuditDocs{Docs: f.docs, collection: auditCollection}
	f.start()
	f.actsAs(opsUID, opsEmail, signInGoogle)
	if rec := f.apiAs(http.MethodGet, apiOrgPathOf(customer.ID), ""); rec.Code != http.StatusInternalServerError {
		t.Errorf("a read across organisations was answered %d (%s) with an unwritable log, want this service's own failure", rec.Code, rec.Body)
	}
}

// TestLastUsedOrg: a person lands in the organisation they last worked in. The
// order is the memberships' own record of it — the one just signed up or selected
// first, and a membership nobody has used last — and selecting is the caller's
// own record and nobody else's.
func TestLastUsedOrg(t *testing.T) {
	f := newFixture(t)
	first := f.signedUp(danaUID, danaEmail, "First Ltd")
	f.advance(time.Hour)
	second := f.signedUp(danaUID, danaEmail, "Second Ltd")

	// Sign-up is itself a use: the person has just made the organisation they are
	// in.
	f.actsAs(danaUID, danaEmail, signInGoogle)
	if list := f.listOrgs(); len(list) != 2 || list[0].ID != second.ID {
		t.Fatalf("the switcher's list is %+v, want the organisation just signed up first", list)
	}

	// Selecting the older one puts it in front, and records when.
	f.advance(time.Hour)
	if rec := f.apiAs(http.MethodPost, apiOrgSelectPathOf(first.ID), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("selecting an organisation answered %d (%s), want nothing", rec.Code, rec.Body)
	}
	if list := f.listOrgs(); len(list) != 2 || list[0].ID != first.ID || list[1].ID != second.ID {
		t.Errorf("the switcher's list is %+v, want %q first and %q second", list, first.ID, second.ID)
	}
	used, found := f.membership(first.ID, danaUID)
	if !found || used.LastUsedAt == nil || !used.LastUsedAt.Equal(f.now) {
		t.Errorf("the membership records last use %v, want the instant of the selection %s", used.LastUsedAt, f.now)
	}

	// A membership nobody has selected sorts after one that has, which is what
	// an invited person's list looks like before they have worked anywhere.
	f.seed(membershipsCollection, membershipID(second.ID, erinUID), WireMembership{
		OrganisationID: second.ID, UID: erinUID, Email: erinEmail, Role: roleMember, CreatedAt: f.now,
	})
	f.seed(membershipsCollection, membershipID(first.ID, erinUID), WireMembership{
		OrganisationID: first.ID, UID: erinUID, Email: erinEmail, Role: roleMember, CreatedAt: f.now, LastUsedAt: &f.now,
	})
	f.actsAs(erinUID, erinEmail, signInGoogle)
	if list := f.listOrgs(); len(list) != 2 || list[0].ID != first.ID {
		t.Errorf("a list of one used and one never-used organisation is %+v, want the used one first", list)
	}

	// Selecting an organisation the caller is not in changes nothing: the
	// selection is their own record, and there is none to write.
	f.actsAs(danaUID, danaEmail, signInGoogle)
	f.advance(time.Hour)
	if rec := f.apiAs(http.MethodPost, apiOrgSelectPathOf("org_nobody"), ""); rec.Code != http.StatusNotFound {
		t.Errorf("selecting an organisation that is not there answered %d (%s), want the not-found refusal", rec.Code, rec.Body)
	}
	if list := f.listOrgs(); len(list) != 2 || list[0].ID != first.ID {
		t.Errorf("a refused selection moved the list to %+v, want it where it was", list)
	}
}

// TestOrgCreationIsRateLimited: one account may create so many organisations a
// day and no more (F35, D2184). What is counted is the organisations it created
// and not the ones it was invited into, a refused sign-up creates nothing, and
// the account may create again once the window has passed.
func TestOrgCreationIsRateLimited(t *testing.T) {
	f := newFixture(t)
	f.actsAs(danaUID, danaEmail, signInGoogle)

	// A membership in somebody else's organisation is not one this account
	// created, so it is not counted: the limit is on creating tenants.
	invited := f.signedUp(erinUID, erinEmail, "Somebody Else's Ltd")
	f.seed(membershipsCollection, membershipID(invited.ID, danaUID), WireMembership{
		OrganisationID: invited.ID, UID: danaUID, Email: danaEmail, Role: roleMember, CreatedAt: f.now,
	})

	f.actsAs(danaUID, danaEmail, signInGoogle)
	for i := 0; i < orgDailyLimit; i++ {
		body := fmt.Sprintf(`{"name":"Tenant %d"}`, i)
		if rec := f.apiAs(http.MethodPost, apiOrgsPath, body); rec.Code != http.StatusCreated {
			t.Fatalf("organisation %d of the day answered %d (%s), want it created", i+1, rec.Code, rec.Body)
		}
	}
	before := len(f.organisations())
	over := f.apiAs(http.MethodPost, apiOrgsPath, `{"name":"One Too Many"}`)
	if over.Code != http.StatusTooManyRequests {
		t.Fatalf("the organisation past the day's limit answered %d (%s), want the refusal", over.Code, over.Body)
	}
	if code := refusal(f, over); code != "too_many_organisations" {
		t.Errorf("the refusal carries the code %q, want too_many_organisations, which is what a client branches on", code)
	}
	if len(f.organisations()) != before {
		t.Error("the refused sign-up created an organisation, so the limit is a message and not a limit")
	}

	// Another account has a day of its own.
	f.actsAs(erinUID, erinEmail, signInGoogle)
	if rec := f.apiAs(http.MethodPost, apiOrgsPath, `{"name":"Erin's Ltd"}`); rec.Code != http.StatusCreated {
		t.Errorf("another account's first organisation answered %d (%s), want it created: the limit is per account", rec.Code, rec.Body)
	}

	// And the window is a day: the same account may create again after it.
	f.actsAs(danaUID, danaEmail, signInGoogle)
	f.advance(orgLimitWindow + time.Minute)
	if rec := f.apiAs(http.MethodPost, apiOrgsPath, `{"name":"Tomorrow Ltd"}`); rec.Code != http.StatusCreated {
		t.Errorf("a sign-up a day later answered %d (%s), want it created: the day's count is the last 24 hours", rec.Code, rec.Body)
	}
}

// TestAnotherOrgsMembersAreRefused: an organisation's records are its own
// members' and nobody else's, unless the caller is operations. One organisation's
// owner reading another's is refused the organisation, its members and the
// selection alike, the refusal is the same whether the organisation is not there
// or not theirs, and a request with no sign-in at all is refused before any of
// that.
func TestAnotherOrgsMembersAreRefused(t *testing.T) {
	f := newFixture(t)
	alpha := f.signedUp(danaUID, danaEmail, "Alpha Ltd")
	beta := f.signedUp(erinUID, erinEmail, "Beta Ltd")

	f.actsAs(danaUID, danaEmail, signInGoogle)
	for _, tc := range []struct {
		name   string
		method string
		path   string
	}{
		{name: "another organisation's record", method: http.MethodGet, path: apiOrgPathOf(beta.ID)},
		{name: "another organisation's members", method: http.MethodGet, path: apiOrgMembersPathOf(beta.ID)},
		{name: "selecting another organisation", method: http.MethodPost, path: apiOrgSelectPathOf(beta.ID)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.apiAs(tc.method, tc.path, "")
			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s was answered %d (%s), want the not-found refusal", tc.name, rec.Code, rec.Body)
			}
			// The same answer an organisation that is not there gets, so nothing
			// about another customer's organisation is told by the refusal: which
			// ids exist is a list of who the portal's customers are.
			missing := f.apiAs(tc.method, strings.Replace(tc.path, beta.ID, "org_nobody", 1), "")
			if missing.Code != rec.Code || missing.Body.String() != rec.Body.String() {
				t.Errorf("the refusal for another organisation is (%d) %s and for one that is not there (%d) %s, want the two the same",
					rec.Code, rec.Body, missing.Code, missing.Body)
			}
		})
	}

	// Their own organisation, and only their own, is what they read: the list is
	// one organisation, its members are one person.
	if list := f.listOrgs(); len(list) != 1 || list[0].ID != alpha.ID {
		t.Errorf("the switcher's list is %+v, want only the caller's own organisation %q", list, alpha.ID)
	}
	members := f.apiAs(http.MethodGet, apiOrgMembersPathOf(alpha.ID), "")
	if members.Code != http.StatusOK {
		t.Fatalf("the owner reading their own member list was answered %d (%s)", members.Code, members.Body)
	}
	if list := readAnswer[WireOrgMembers](f, members).Members; len(list) != 1 || list[0].UID != danaUID {
		t.Errorf("the member list holds %+v, want the one owner", list)
	}
	// None of that wrote an audit line: no operations membership was used.
	if entries := f.auditEntries(); len(entries) != 0 {
		t.Errorf("%d audit line(s) were written for a person reading their own organisation", len(entries))
	}

	// And a request with no sign-in is refused before any organisation is
	// looked at.
	bare := f.api(http.MethodGet, apiOrgPathOf(alpha.ID), "")
	if bare.Code != http.StatusUnauthorized {
		t.Fatalf("a request with no sign-in answered %d (%s), want 401", bare.Code, bare.Body)
	}
	if code := refusal(f, bare); code != "unauthenticated" {
		t.Errorf("the refusal carries the code %q, want unauthenticated", code)
	}
}

// apiOrgPathOf and the two below spell the routes for one organisation, so a case
// names the organisation and not the path's shape.
func apiOrgPathOf(id string) string {
	return strings.Replace(apiOrgPath, "{id}", id, 1)
}

func apiOrgMembersPathOf(id string) string {
	return strings.Replace(apiOrgMembersPath, "{id}", id, 1)
}

func apiOrgSelectPathOf(id string) string {
	return strings.Replace(apiOrgSelectPath, "{id}", id, 1)
}
