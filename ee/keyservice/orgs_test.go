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
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/keyservice/docstore"
)

// The accounts these cases are about. None of them is a real address and none is
// secret-shaped: the verifier is a spy that answers whatever caller a case sets
// (keyservice_test.go).
const (
	danaUID    = "uid-dana"
	danaEmail  = "dana@example.com"
	erinUID    = "uid-erin"
	erinEmail  = "erin@example.com"
	opsUID     = "uid-operations"
	opsEmail   = "operations@fugaro.example"
	sellerUID  = "uid-seller"
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

// unstorableDocs is the fixture's store with the deploy's name rule in front of
// it, which is how a case reads what a request-supplied id does when it names
// something no store can file a document under: the fixture's own store files
// whatever it is handed, and the rule is the seam's own (docstore.ErrUnstorableName)
// rather than a second copy of it — the probe collection is one nothing writes,
// so the store answers "not there" for every name it CAN file and the sentinel
// for the ones it cannot.
type unstorableDocs struct {
	Docs
	rule docstore.Store
}

func (d unstorableDocs) Get(ctx context.Context, collection, id string) (Doc, error) {
	if _, err := d.rule.Get(ctx, "the probe", id, nil); errors.Is(err, docstore.ErrUnstorableName) {
		return nil, err
	}
	return d.Docs.Get(ctx, collection, id)
}

// burstCountDocs is the fixture's store with the day's count widened, the shape
// burstMints has for the mint: the memberships read a sign-up takes before its
// write waits for the burst it is part of, and a read that arrives alone waits
// the window out. That window — a count taken, then a record written — is the
// whole of what signUpMu closes, and a burst that ran one sign-up after another
// would never read it: each of two sign-ups counted inside the gate counts what
// was written before the gate opened and neither sees the other's.
type burstCountDocs struct {
	Docs
	want int
	gate time.Duration
	mu   sync.Mutex
	seen int
	open chan struct{}
	// waited is set once the window has closed on an arrival, so that a burst
	// the lock serialises pays the gate once and not once per sign-up.
	waited bool
}

func newBurstCountDocs(inner Docs, want int) *burstCountDocs {
	return &burstCountDocs{Docs: inner, want: want, gate: burstGate, open: make(chan struct{})}
}

func (d *burstCountDocs) Query(ctx context.Context, collection string, where Where) ([]Doc, error) {
	if collection == membershipsCollection {
		d.waitForTheBurst()
	}
	return d.Docs.Query(ctx, collection, where)
}

func (d *burstCountDocs) waitForTheBurst() {
	d.mu.Lock()
	d.seen++
	if d.seen == d.want {
		close(d.open)
		d.mu.Unlock()
		return
	}
	waited := d.waited
	d.mu.Unlock()
	if waited {
		return
	}
	select {
	case <-d.open:
	case <-time.After(d.gate):
		d.mu.Lock()
		d.waited = true
		d.mu.Unlock()
	}
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

// TestTheSignUpBodyIsCappedAtItsOwnBytes: the create route reads a body of its
// own size and not the mount's default (api_orgs.go's maxOrgBodyBytes). The
// request is answered by the route itself, so the mount's cap is not what a
// sign-up meets: a body at the route's cap is a sign-up like any other, and one
// byte past it is refused 413 body_too_large.
func TestTheSignUpBodyIsCappedAtItsOwnBytes(t *testing.T) {
	f := newFixture(t)
	f.actsAs(danaUID, danaEmail, signInGoogle)

	// Space between JSON tokens is a body a strict decode reads and holds no
	// field of its own, so the body at the cap is one real sign-up and not a
	// padded object the shape would refuse for its padding.
	signUp := `{"name":"Example Ltd"}`
	at := signUp + strings.Repeat(" ", int(maxOrgBodyBytes)-len(signUp))
	if len(at) != maxOrgBodyBytes {
		t.Fatalf("the body at the cap is %d bytes, want this route's own %d", len(at), maxOrgBodyBytes)
	}
	rec := f.apiAs(http.MethodPost, apiOrgsPath, at)
	if rec.Code != http.StatusCreated {
		t.Fatalf("a sign-up whose body is %d bytes was answered %d (%s), want the organisation: this route reads a body of %d", len(at), rec.Code, rec.Body, maxOrgBodyBytes)
	}
	if org := readAnswer[WireOrganisation](f, rec); org.Name != "Example Ltd" {
		t.Errorf("the sign-up at the cap created the organisation %q, want the name its body carried", org.Name)
	}

	before := len(f.organisations())
	over := " " + at
	if len(over) != maxOrgBodyBytes+1 {
		t.Fatalf("the body over the cap is %d bytes, want one past this route's own %d", len(over), maxOrgBodyBytes)
	}
	refused := f.apiAs(http.MethodPost, apiOrgsPath, over)
	if refused.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("a sign-up whose body is %d bytes was answered %d (%s), want the 413 a body over this route's own cap is refused with", len(over), refused.Code, refused.Body)
	}
	if code := refusal(f, refused); code != "body_too_large" {
		t.Errorf("the refusal carries the code %q, want body_too_large", code)
	}
	if len(f.organisations()) != before {
		t.Error("a sign-up refused for the size of its body created an organisation anyway")
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

// TestRegisterOrgCreatedRefusesWhatCannotRun: the hook table's own invariants, at
// the same door the API's route table has (api_test.go's table). An init has
// nobody to return an error to, and every one of these is a hook the sign-up
// would run: a hook with no name is one whose failure cannot be reported, a nil
// hook is one that would panic inside a transaction and take a stranger's sign-up
// with it, and two hooks under one name are one failure read as the other's. The
// name that is already taken is the fixture's own, so the refusal is read without
// leaving a hook behind for the cases after this one.
func TestRegisterOrgCreatedRefusesWhatCannotRun(t *testing.T) {
	runnable := func(context.Context, Tx, WireOrganisation) error { return nil }
	held := func() int {
		orgCreatedHooks.mu.Lock()
		defer orgCreatedHooks.mu.Unlock()
		return len(orgCreatedHooks.hooks)
	}
	before := held()
	for _, tc := range []struct {
		name string
		take func()
	}{
		{"a hook with no name", func() { registerOrgCreated("", runnable) }},
		{"a hook whose name is whitespace", func() { registerOrgCreated("   ", runnable) }},
		{"a hook with nothing to run", func() { registerOrgCreated("a hook that runs nothing", nil) }},
		{"a name two hooks share", func() { registerOrgCreated("the test's own", runnable) }},
	} {
		if !panics(t, tc.take) {
			t.Errorf("%s was registered, want the table to refuse it: a sign-up runs every hook in it", tc.name)
		}
	}
	if got := held(); got != before {
		t.Errorf("the refused registrations left %d hook(s) in the table, want the %d it held: every hook in it runs for every sign-up", got, before)
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
// member who signed in by email link is refused the organisation that membership
// is in and the selection of it, and is admitted the customer organisation their
// other membership carries — which is the read the rule is applied at and the
// list is not, because a list that answered the refusal for the whole of itself
// would leave them unable to reach an organisation this very service admits
// them. The same person signed in with Google reaches both; a reseller's member
// is refused the same way until a directory is pinned; and a customer
// organisation is not one the rule follows at all.
func TestSSORuleFollowsTheActorsMembership(t *testing.T) {
	f := newFixture(t)
	operations := f.seededOrganisation("org_operations", "Operations", orgTypeOperations, opsUID, opsEmail, roleMember)
	reseller := f.seededOrganisation("org_reseller", "Partner Ltd", orgTypeReseller, sellerUID, sellerMail, roleMember)
	customer := f.signedUp(danaUID, danaEmail, "Example Ltd")

	// The operations member belongs to a customer organisation as well, which is
	// what makes the list the read that tells this rule's two memberships apart:
	// one the rule follows and one it does not, in the same caller.
	f.seed(membershipsCollection, membershipID(customer.ID, opsUID), WireMembership{
		OrganisationID: customer.ID, UID: opsUID, Email: opsEmail, Role: roleMember, CreatedAt: f.now,
	})

	// The operations membership, signed in by email link: refused on the requests
	// that use it — the organisation it is in, and the selection of it.
	f.actsAs(opsUID, opsEmail, signInEmailLink)
	for _, tc := range []struct {
		name   string
		method string
		path   string
	}{
		{name: "the operations organisation itself", method: http.MethodGet, path: apiOrgPathOf(operations.ID)},
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

	// Reading an organisation the operations membership reaches ACROSS is the
	// request that rule carries in accessTo, and it is refused for the same
	// reason: D2207 admits the operations team's reads on Google or Microsoft.
	if rec := f.apiAs(http.MethodGet, apiOrgPathOf(reseller.ID), ""); rec.Code != http.StatusForbidden {
		t.Errorf("an operations member by email link reading across organisations was answered %d (%s), want the SSO refusal", rec.Code, rec.Body)
	}

	// The membership the rule does not follow is used, listed and selected: the
	// rule belongs to the membership a request uses, and the switcher's list
	// reads the caller's own memberships and nothing across an organisation
	// boundary. Refusing the list would refuse the customer organisation above
	// with it, and would empty the switcher of the operations organisation the
	// spec keeps in it so that selecting one opens the dialog (spec
	// §Architecture).
	if rec := f.apiAs(http.MethodGet, apiOrgPathOf(customer.ID), ""); rec.Code != http.StatusOK {
		t.Errorf("an operations member by email link reading their own customer organisation was answered %d (%s), want it: nothing follows a customer membership", rec.Code, rec.Body)
	}
	if rec := f.apiAs(http.MethodPost, apiOrgSelectPathOf(customer.ID), ""); rec.Code != http.StatusNoContent {
		t.Errorf("an operations member by email link selecting their own customer organisation was answered %d (%s), want the selection", rec.Code, rec.Body)
	}
	switcher := f.apiAs(http.MethodGet, apiOrgsPath, "")
	if switcher.Code != http.StatusOK {
		t.Fatalf("an operations member by email link read the switcher's list %d (%s), want the organisations they belong to: the list is the memberships' own, and the refusal is the selection's", switcher.Code, switcher.Body)
	}
	if listed := readAnswer[WireOrgList](f, switcher).Organisations; !slices.ContainsFunc(listed, func(o WireOrganisation) bool { return o.ID == operations.ID }) ||
		!slices.ContainsFunc(listed, func(o WireOrganisation) bool { return o.ID == customer.ID }) {
		t.Errorf("the switcher's list by email link holds %+v, want both organisations the caller belongs to, %q and %q", listed, operations.ID, customer.ID)
	}

	// The same person, signed in with Google: admitted both.
	f.actsAs(opsUID, opsEmail, signInGoogle)
	read := f.apiAs(http.MethodGet, apiOrgPathOf(operations.ID), "")
	if read.Code != http.StatusOK {
		t.Fatalf("an operations member signed in with Google was answered %d (%s), want the organisation", read.Code, read.Body)
	}
	if got := readAnswer[WireOrganisation](f, read); got.ID != operations.ID {
		t.Errorf("the read answered the organisation %q, want %q", got.ID, operations.ID)
	}
	if across := f.apiAs(http.MethodGet, apiOrgPathOf(reseller.ID), ""); across.Code != http.StatusOK {
		t.Errorf("the operations read across organisations signed in with Google was answered %d (%s), want the organisation", across.Code, across.Body)
	}
	if list := f.listOrgs(); len(list) != 2 {
		t.Errorf("an operations member signed in with Google listed %+v, want both organisations they belong to", list)
	}

	// A reseller's member is refused the same way until a directory is pinned
	// (D2207's fallback: Google only, which is the rule's zero value).
	f.actsAs(sellerUID, sellerMail, signInEmailLink)
	if rec := f.apiAs(http.MethodGet, apiOrgPathOf(reseller.ID), ""); rec.Code != http.StatusForbidden {
		t.Errorf("a reseller's member by email link was answered %d (%s), want the SSO refusal", rec.Code, rec.Body)
	}
	if rec := f.apiAs(http.MethodPost, apiOrgSelectPathOf(reseller.ID), ""); rec.Code != http.StatusForbidden {
		t.Errorf("a reseller's member by email link selecting it was answered %d (%s), want the SSO refusal", rec.Code, rec.Body)
	}
	switcher = f.apiAs(http.MethodGet, apiOrgsPath, "")
	if switcher.Code != http.StatusOK {
		t.Fatalf("a reseller's member by email link read the switcher's list %d (%s), want their organisation in it: the refusal is the selection's, and the switcher is where the dialog that explains it is opened from", switcher.Code, switcher.Body)
	}
	if listed := readAnswer[WireOrgList](f, switcher).Organisations; len(listed) != 1 || listed[0].ID != reseller.ID {
		t.Errorf("a reseller's member by email link listed %+v, want their own organisation %q in the switcher", listed, reseller.ID)
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
	if list := f.listOrgs(); len(list) != 1 || list[0].ID != customer.ID {
		t.Errorf("a customer's switcher list by email link is %+v, want their own organisation %q: nothing follows a customer membership", list, customer.ID)
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

// TestAuditRefusesALineWithNoActor: the log's own guard (audit.go). A line that
// names no account, no address or no action is refused rather than written,
// because an entry with none of them is a read nobody is answerable for — the
// whole of what the log exists to answer — and one is not written into the log
// to be found later. A line that names its actor and its action is written whole,
// and comes back with the id and the instant the writer gives it.
func TestAuditRefusesALineWithNoActor(t *testing.T) {
	f := newFixture(t)
	org := f.signedUp(danaUID, danaEmail, "Alpha Ltd")
	whole := Caller{UID: danaUID, Email: danaEmail, Provider: signInGoogle, AuthTime: f.now}

	for _, tc := range []struct {
		name  string
		actor Caller
		entry auditEntry
	}{
		{name: "no account", actor: Caller{Email: danaEmail}, entry: auditEntry{Action: actionOrganisationRead, Read: true}},
		{name: "an account of whitespace", actor: Caller{UID: "  ", Email: danaEmail}, entry: auditEntry{Action: actionOrganisationRead, Read: true}},
		{name: "no address", actor: Caller{UID: danaUID}, entry: auditEntry{Action: actionOrganisationRead, Read: true}},
		{name: "an address of whitespace", actor: Caller{UID: danaUID, Email: "  "}, entry: auditEntry{Action: actionOrganisationRead, Read: true}},
		{name: "no action", actor: whole, entry: auditEntry{Read: true}},
		{name: "an action of whitespace", actor: whole, entry: auditEntry{Action: "  ", Read: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(f.auditEntries())
			if err := f.svc.recordAudit(context.Background(), tc.actor, tc.entry); !errors.Is(err, errAuditNeedsAnActor) {
				t.Fatalf("an entry with %s was refused with %v, want errAuditNeedsAnActor: a line nothing and nobody is answerable for is not one this log holds", tc.name, err)
			}
			if entries := f.auditEntries(); len(entries) != before {
				t.Errorf("an entry with %s left %d line(s) in the log, want the refusal to write nothing", tc.name, len(entries)-before)
			}
		})
	}

	target := org.ID
	if err := f.svc.recordAudit(context.Background(), whole, auditEntry{
		Action:         actionOrganisationRead,
		OrganisationID: &target,
		Read:           true,
	}); err != nil {
		t.Fatalf("a line naming its actor and its action was refused: %v", err)
	}
	entries := f.auditEntries()
	if len(entries) != 1 {
		t.Fatalf("the log holds %d line(s), want the one that was written", len(entries))
	}
	entry := entries[0]
	switch {
	case entry.ActorUID != whole.UID || entry.ActorEmail != whole.Email:
		t.Errorf("the line names the actor %q <%s>, want the caller it was written for", entry.ActorUID, entry.ActorEmail)
	case entry.OrganisationID == nil || *entry.OrganisationID != org.ID:
		t.Errorf("the line names the organisation %v, want %q", entry.OrganisationID, org.ID)
	case entry.ID == "" || entry.At.IsZero():
		t.Errorf("the line carries the id %q and the instant %s, want both", entry.ID, entry.At)
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

// TestConcurrentSignUpsRespectTheDailyLimit: F35's limit under the one thing that
// can break it — one account signing up from many places at once. The day's count
// is a read of the account's memberships and a write of one more, and api.go's Tx
// reads by id alone, so the count is taken outside the transaction and signUpMu is
// what makes the two atomic; without it every sign-up of a burst counts the
// records written before the burst and each writes one more, which is an account
// holding as many organisations as it sent requests. The burst is widened at the
// count itself (burstCountDocs), so the window this closes is the one the case
// reads rather than the one the scheduler happened to leave open.
func TestConcurrentSignUpsRespectTheDailyLimit(t *testing.T) {
	const burst = 8
	f := newFixture(t)
	f.deps.Docs = newBurstCountDocs(f.docs, burst)
	f.start()
	f.actsAs(danaUID, danaEmail, signInGoogle)

	codes := together(burst, func(i int) *httptest.ResponseRecorder {
		return f.apiAs(http.MethodPost, apiOrgsPath, fmt.Sprintf(`{"name":"Tenant %d"}`, i))
	})

	if got := answered(codes, http.StatusCreated); got != orgDailyLimit {
		t.Errorf("%d of %d concurrent sign-ups were answered 201, want the day's %d: %v", got, burst, orgDailyLimit, codes)
	}
	if got := len(f.organisations()); got > orgDailyLimit {
		t.Errorf("a burst of %d sign-ups left the account holding %d organisations, want no more than the %d a day allows", burst, got, orgDailyLimit)
	}
	if got := len(f.organisations()); got != orgDailyLimit {
		t.Errorf("the burst created %d organisation(s), want the %d the limit allows: the refusals must be refused and not lost", got, orgDailyLimit)
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
	// An operations organisation and a reseller somebody else is in, so the cases
	// below read what a stranger is told about them: their existence, and which
	// of them is which, is not a stranger's to learn from a refusal either.
	f.seededOrganisation("org_operations", "Operations", orgTypeOperations, opsUID, opsEmail, roleMember)
	f.seededOrganisation("org_reseller", "Partner Ltd", orgTypeReseller, sellerUID, sellerMail, roleMember)

	// The stranger probes with both of the sign-ins one may have: Google, which
	// every rule admits, and the email link, which the operations and the
	// reseller rules refuse. The second is what reads the ORDER of the two — the
	// membership and the rule — because a rule is the membership's and never the
	// guessed organisation's: a service that asked the rule first, as one bounded
	// by the organisation the request is ABOUT, would answer a stranger's guess of
	// org_operations or org_reseller with 403 sso_required where every other id
	// gets the 404, which is the list of the portal's special organisations handed
	// to whoever asks.
	for _, signIn := range []string{signInGoogle, signInEmailLink} {
		f.actsAs(danaUID, danaEmail, signIn)
		for _, tc := range []struct {
			name   string
			method string
			path   string
		}{
			{name: "another organisation's record", method: http.MethodGet, path: apiOrgPathOf(beta.ID)},
			{name: "another organisation's members", method: http.MethodGet, path: apiOrgMembersPathOf(beta.ID)},
			{name: "selecting another organisation", method: http.MethodPost, path: apiOrgSelectPathOf(beta.ID)},
		} {
			t.Run(tc.name+" signed in with "+signIn, func(t *testing.T) {
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
				// An operations or a reseller organisation the caller does not
				// belong to is answered the same way as one that is not there at
				// all: the rule its members sign in by is applied to a membership
				// of it and never to a stranger's guess, which would otherwise tell
				// whoever asked which ids name an organisation of the operations
				// team or of a reseller.
				for _, special := range []string{"org_operations", "org_reseller"} {
					guessed := f.apiAs(tc.method, strings.Replace(tc.path, beta.ID, special, 1), "")
					if guessed.Code != http.StatusNotFound || guessed.Body.String() != rec.Body.String() {
						t.Errorf("the %s %s the caller is not in is answered (%d) %s, want the same not-found refusal as one that is not there (%d) %s",
							special, tc.name, guessed.Code, guessed.Body, rec.Code, rec.Body)
					}
				}
			})
		}
	}

	// Their own organisation, and only their own, is what they read: the list is
	// one organisation, its members are one person.
	f.actsAs(danaUID, danaEmail, signInGoogle)
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

// TestTheSignInDoorRefusesWhatItCannotAdmit: every route of this area is behind
// one door (apiCaller, portalToken), and what the door refuses it refuses by
// itself and before any route is reached. A request carrying no sign-in, one
// presented in another scheme, an empty token and a token past the bound are all
// refused as no sign-in at all — the person has to sign in whichever it was — and
// so is a token the verifier does not admit and a verified sign-in that names no
// account, which is a sign-in every record here would be filed under nothing for.
// A verifier that failed for its own reason is this service's own failure and not
// the caller's: the person's sign-in may be perfectly good.
func TestTheSignInDoorRefusesWhatItCannotAdmit(t *testing.T) {
	f := newFixture(t)
	alpha := f.signedUp(danaUID, danaEmail, "Alpha Ltd")
	whole := Caller{UID: danaUID, Email: danaEmail, Provider: signInGoogle, AuthTime: f.now}

	for _, tc := range []struct {
		name string
		// auth is the Authorization header as the case writes it, empty for a
		// request that carries none.
		auth string
		// err and caller are what the verifier answers if it is reached.
		err    error
		caller Caller
		// asked is whether the verifier is reached at all: a request the door
		// refuses itself never reaches it.
		asked  bool
		status int
		code   string
	}{
		{name: "no sign-in at all", status: http.StatusUnauthorized, code: "unauthenticated"},
		{name: "another scheme", auth: "Basic " + portalIDToken, status: http.StatusUnauthorized, code: "unauthenticated"},
		{name: "a scheme with no token", auth: portalBearerScheme, status: http.StatusUnauthorized, code: "unauthenticated"},
		{name: "an empty token", auth: portalBearerScheme + " ", status: http.StatusUnauthorized, code: "unauthenticated"},
		{name: "a token of whitespace", auth: portalBearerScheme + "   ", status: http.StatusUnauthorized, code: "unauthenticated"},
		{name: "a token past the bound", auth: portalBearerScheme + " " + strings.Repeat("x", maxPortalTokenBytes+1), status: http.StatusUnauthorized, code: "unauthenticated"},
		{
			name: "a sign-in the verifier does not admit", auth: portalBearerScheme + " " + portalIDToken,
			err: ErrUnverifiedSignIn, asked: true, status: http.StatusUnauthorized, code: "unauthenticated",
		},
		{
			name: "a sign-in naming no account", auth: portalBearerScheme + " " + portalIDToken,
			caller: Caller{Email: danaEmail, Provider: signInGoogle}, asked: true,
			status: http.StatusUnauthorized, code: "unauthenticated",
		},
		{
			name: "a verifier that failed for its own reason", auth: portalBearerScheme + " " + portalIDToken,
			err: errors.New("the verifier could not read its key set"), asked: true,
			status: http.StatusInternalServerError, code: "unavailable",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.signIn.tokens = nil
			f.signIn.err = tc.err
			f.signIn.caller = tc.caller

			rec := f.apiWithAuth(http.MethodGet, apiOrgsPath, tc.auth)
			if rec.Code != tc.status {
				t.Fatalf("%s was answered %d (%s), want %d", tc.name, rec.Code, rec.Body, tc.status)
			}
			if code := refusal(f, rec); code != tc.code {
				t.Errorf("the refusal carries the code %q, want %q, which is what the SPA branches on", code, tc.code)
			}
			if reached := len(f.signIn.tokens) > 0; reached != tc.asked {
				t.Errorf("the verifier was reached %t for %s, want %t: the door's own refusals are its own", reached, tc.name, tc.asked)
			}
			if entries := f.auditEntries(); len(entries) != 0 {
				t.Errorf("%s wrote %d audit line(s), want a request that never reached a route to write none", tc.name, len(entries))
			}
		})
	}

	// And a sign-in this service admits is answered: the door is a door and not a
	// wall, and what it hands on is the caller the verifier named.
	f.signIn.tokens = nil
	f.signIn.err = nil
	f.signIn.caller = whole
	list := f.apiWithAuth(http.MethodGet, apiOrgsPath, portalBearerScheme+" "+portalIDToken)
	if list.Code != http.StatusOK {
		t.Fatalf("a sign-in this service admits was answered %d (%s), want the switcher's list", list.Code, list.Body)
	}
	if orgs := readAnswer[WireOrgList](f, list).Organisations; len(orgs) != 1 || orgs[0].ID != alpha.ID {
		t.Errorf("the list is %+v, want the caller's own organisation %q", orgs, alpha.ID)
	}
	if got := f.signIn.tokens; len(got) != 1 || got[0] != portalIDToken {
		t.Errorf("the verifier was handed %q, want the token the request carried", got)
	}
}

// TestAnIDTheStoreCannotFileIsNotThere: the id these routes read is the request's,
// and the store has names it cannot file a document under at all — a slash, a
// control character, "..", the database's reserved __…__ form (docstore's
// ErrUnstorableName). A name no organisation could have been created with is
// answered as the organisation that is not there, the same 404 with the same body
// as an id that was minted and never written, on every route that takes one: the
// store's refusal answered as this service's own is a 500 that says a document is
// there to be argued with, and any second answer is an oracle telling a stranger
// which names the service read.
func TestAnIDTheStoreCannotFileIsNotThere(t *testing.T) {
	f := newFixture(t)
	f.deps.Docs = unstorableDocs{Docs: f.docs, rule: docstore.NewMemory()}
	f.start()
	f.actsAs(danaUID, danaEmail, signInGoogle)

	absent := f.apiAs(http.MethodGet, apiOrgPathOf("org_nobody"), "")
	if absent.Code != http.StatusNotFound {
		t.Fatalf("a read of an organisation that is not there answered %d (%s), want the not-found refusal", absent.Code, absent.Body)
	}

	// The names are the ones the store cannot file, spelled the way a request
	// spells them: the router hands the route the path's own value, so a slash
	// arrives as a slash and not as a segment of its own. A "." segment is left
	// out because the mux answers it with its own redirect before any route is
	// reached, and the ".."-shaped probe is the escaped one that does arrive.
	for _, name := range []string{"a%2Fb", "%2e%2e", "a%00b", "__x__", strings.Repeat("x", 1501)} {
		for _, route := range []struct {
			name   string
			method string
			path   string
			body   string
		}{
			{name: "read", method: http.MethodGet, path: apiOrgPathOf(name)},
			{name: "members read", method: http.MethodGet, path: apiOrgMembersPathOf(name)},
			{name: "selection", method: http.MethodPost, path: apiOrgSelectPathOf(name), body: "{}"},
		} {
			rec := f.apiAs(route.method, route.path, route.body)
			switch {
			case rec.Code != http.StatusNotFound:
				t.Errorf("the %s of %q answered %d (%s), want the not-found refusal: a name no organisation could be created with is not one to argue with", route.name, name, rec.Code, rec.Body)
			case rec.Body.String() != absent.Body.String():
				t.Errorf("the %s of %q answered %s, want the same refusal as an absent organisation (%s): any other answer tells a stranger which names the service read", route.name, name, rec.Body, absent.Body)
			}
		}
	}
}

// apiWithAuth sends one request whose Authorization header is spelled by the
// case, which is how the cases about the door's own refusals present a sign-in
// the fixture's own helper would never build. An empty header is a request that
// carries none.
func (f *fixture) apiWithAuth(method, path, auth string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(method, intakeOrigin+path, nil)
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	req.RemoteAddr = applicantIP
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
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
