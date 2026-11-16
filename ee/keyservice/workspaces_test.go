// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// P3.1's own cases: a workspace, the binding that attaches an entitlement to a
// Firebase project, and the key that binding mints (D2196–D2198, D2204, D2205).
//
// The three seams these cases reach are fakes and none of them is a cloud: the
// project's database is an httptest server standing in for D2205's read, the
// minter is the package's own (a real signature over the fixture's key ring, no
// key ring API), and the document store is the in-memory one the fixture holds.
// What is NOT here is a live project: the exact answers a real database gives
// are UNVERIFIED (the reconciliation's open list carries them), so what these
// cases pin is this service's reading of the two answers the plan names —
// "permission denied" for a locked database and "no such database" for a
// project without one (D2205, F41).
//
// The fixture is the package's, extended rather than rebuilt: the same
// organisation, membership, sign-in and document-store spies every other case in
// this package drives, plus the two seams this area is the first to reach.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/licence"
)

// The project ids these cases bind: both are shaped the way Firebase spells one
// (the schema's own pattern), and neither is a project anybody owns.
const (
	exampleProject = "example-portal"
	secondProject  = "another-portal"
)

// bindingFixture is the package's fixture with P3.1's two seams.
type bindingFixture struct {
	*fixture
	probe *probeServer
	mints *bundleMints
}

// newBindingFixture builds the package's fixture with the probe replaced by a
// server of this case's own and the minter wrapped so that the whole answer —
// the secret as well as the key — is kept. Every case here gets both, so no case
// can reach Firebase or the key ring by forgetting one.
func newBindingFixture(t *testing.T) *bindingFixture {
	t.Helper()
	mints := &bundleMints{}
	probe := &probeServer{status: http.StatusUnauthorized, body: `{"error":"Permission denied"}`}
	f := newFixtureWith(t, Production, func(f *fixture) {
		mints.inner = f.recording
		f.deps.Mints = mints
		probe.install(t)
	})
	// The sign-ins these cases make are Google's, which is what an organisation's
	// own members sign in with and what no SSO rule refuses.
	f.actsAs(danaUID, danaEmail, signInGoogle)
	return &bindingFixture{fixture: f, probe: probe, mints: mints}
}

// probeServer is D2205's database in a case: what it answers, and every project
// id it was asked about. It is installed into the package's own probe seam,
// which is the seam a deployment fills with Firebase's host.
type probeServer struct {
	mu     sync.Mutex
	asked  []string
	status int
	body   string
	// refuses is a server that is not there at all, which is the third answer
	// D2205 names: no answer binds unchecked.
	refuses bool

	server *httptest.Server
	closed bool
}

func (p *probeServer) install(t *testing.T) {
	t.Helper()
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.asked = append(p.asked, strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/.json"))
		status, body, refuses := p.status, p.body, p.refuses
		p.mu.Unlock()
		if refuses {
			// A connection closed with no answer at all: what a project whose
			// database cannot be reached looks like from here.
			panic(http.ErrAbortHandler)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	previous := probeInstallation
	probeInstallation = projectInstallation{
		database: p.server.URL + "/%s",
		client:   p.server.Client(),
	}
	t.Cleanup(func() {
		probeInstallation = previous
		p.server.Close()
	})
}

// saw is every project id the case's database was asked about.
func (p *probeServer) saw() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.asked...)
}

// bundleMints is the package's recording minter with the answers kept whole: the
// package's recorder keeps the key texts (which is where a test reads a key back
// through the verifier), and these cases are also about the secret a rebind
// replaces and the licence file an air-gapped binding hands over.
type bundleMints struct {
	inner   *recordingMints
	answers []MintedV1
}

func (m *bundleMints) Mint(ctx context.Context, req Request) (Minted, error) {
	return m.inner.Mint(ctx, req)
}

func (m *bundleMints) MintV1(ctx context.Context, req RequestV1) (MintedV1, error) {
	answer, err := m.inner.MintV1(ctx, req)
	if err != nil {
		return MintedV1{}, err
	}
	m.answers = append(m.answers, answer)
	return answer, nil
}

// claimsOf verifies one key this case's minting produced through the verifier an
// installation uses, which is how a case reads what a key actually states rather
// than what the service meant to write.
func (f *bindingFixture) claimsOf(t *testing.T, index int, projectID string) licence.Claims {
	t.Helper()
	if index >= len(f.mints.answers) {
		t.Fatalf("this deployment minted %d keys and the case reads the one at %d", len(f.mints.answers), index)
	}
	claims, _, err := licence.Verify([]byte(f.mints.answers[index].Key), f.now, f.ring, projectID)
	if err != nil {
		t.Fatalf("verifying the key minted at %d: %v", index, err)
	}
	return claims
}

// bind posts one binding the way the SPA does: the entitlement and the project
// id, as the account the fixture is signed in as.
func (f *bindingFixture) bind(t *testing.T, entitlementID, projectID string) *httptest.ResponseRecorder {
	t.Helper()
	return f.apiAsCaller(http.MethodPost, apiWorkspaceBindPath,
		`{"entitlement_id":`+jsonString(t, entitlementID)+`,"project_id":`+jsonString(t, projectID)+`}`)
}

// seedWorkspace writes one workspace straight into the store: the shape a
// checkout's (P4.1) or an operations grant's workspace is in before any binding.
func (f *bindingFixture) seedWorkspace(id, organisationID, name string) WireWorkspace {
	f.t.Helper()
	workspace := WireWorkspace{ID: id, OrganisationID: organisationID, Name: name, CreatedAt: f.now}
	f.seed(workspacesCollection, workspace.ID, workspace)
	return workspace
}

// workspace reads one workspace out of the store.
func (f *bindingFixture) workspace(id string) (WireWorkspace, bool) {
	f.t.Helper()
	return heldRecord[WireWorkspace](f.fixture, workspacesCollection, id)
}

// entitlement reads one entitlement out of the store.
func (f *bindingFixture) entitlement(id string) (WireEntitlement, bool) {
	f.t.Helper()
	return heldRecord[WireEntitlement](f.fixture, entitlementsCollection, id)
}

// refused is the code of one refusal body, which is what the SPA branches on.
func (f *bindingFixture) refused(rec *httptest.ResponseRecorder) string {
	return refusal(f.fixture, rec)
}

// grant writes one entitlement the way a purchase or an operations grant lands,
// which is through the area's own creation and not a route of this case's.
func (f *bindingFixture) grant(t *testing.T, grant entitlementGrant) WireEntitlement {
	t.Helper()
	held, err := f.svc.grantEntitlement(context.Background(), grant)
	if err != nil {
		t.Fatalf("granting %s %s: %v", grant.Source, grant.Row, err)
	}
	return held
}

// onlyEntitlement is the one entitlement an organisation holds, which is what a
// sign-up's free seat is.
func (f *bindingFixture) onlyEntitlement(t *testing.T, organisationID string) WireEntitlement {
	t.Helper()
	held := f.entitlementsOf(t, organisationID)
	if len(held) != 1 {
		t.Fatalf("the organisation %s holds %d entitlements, and this case reads the one", organisationID, len(held))
	}
	return held[0]
}

// entitlementsOf is every entitlement one organisation holds.
func (f *bindingFixture) entitlementsOf(t *testing.T, organisationID string) []WireEntitlement {
	t.Helper()
	held, err := recordsWhere[WireEntitlement](context.Background(), f.docs, entitlementsCollection,
		Where{Field: "organisation_id", Value: organisationID})
	if err != nil {
		t.Fatalf("reading the entitlements of %s: %v", organisationID, err)
	}
	return held
}

// licensingWorkspace is an organisation with one workspace and one paid
// entitlement in it, which is the state every case about binding starts from.
func (f *bindingFixture) licensingWorkspace(t *testing.T) (WireOrganisation, WireWorkspace, WireEntitlement) {
	t.Helper()
	org := f.seededOrganisation("org-dana", "Dana Ltd", orgTypeCustomer, danaUID, danaEmail, roleOwner)
	workspace := f.seedWorkspace("ws-1", org.ID, "Dana's portal")
	held := f.grant(t, entitlementGrant{
		At:             f.now,
		OrganisationID: org.ID,
		WorkspaceID:    &workspace.ID,
		Source:         entitlementSourcePurchase,
		Row:            PolicyTeamAnnual,
		Seats:          3,
		StartsAt:       f.now,
	})
	return org, workspace, held
}

// TestBindingMintsTheKey is the task's first case: an entitlement bound to a
// Firebase project is what mints its key (D2196). The answer is the workspace
// with the project on it, the entitlement records the binding, and the key the
// minter signed names the project and no other workspace.
func TestBindingMintsTheKey(t *testing.T) {
	f := newBindingFixture(t)
	org := f.signedUp(danaUID, danaEmail, "Dana Ltd")
	free := f.onlyEntitlement(t, org.ID)
	if free.WorkspaceID != nil {
		t.Fatalf("the free seat a sign-up grants is bound to the workspace %v before any binding", *free.WorkspaceID)
	}

	rec := f.bind(t, free.ID, exampleProject)
	if rec.Code != http.StatusOK {
		t.Fatalf("binding the free seat answered %d: %s", rec.Code, rec.Body)
	}
	workspace := readAnswer[WireWorkspace](f.fixture, rec)
	if workspace.ProjectID == nil || *workspace.ProjectID != exampleProject {
		t.Errorf("the bound workspace names the project %v, want %q", workspace.ProjectID, exampleProject)
	}
	if workspace.OrganisationID != org.ID {
		t.Errorf("the bound workspace belongs to %s, want %s", workspace.OrganisationID, org.ID)
	}
	if workspace.EntitlementID == nil || *workspace.EntitlementID != free.ID {
		t.Errorf("the bound workspace is licensed by %v, want %s", workspace.EntitlementID, free.ID)
	}
	if workspace.BoundAt == nil || !workspace.BoundAt.Equal(f.now) {
		t.Errorf("the binding is recorded at %v, want the service's own instant %s", workspace.BoundAt, f.now)
	}
	if held, found := f.workspace(workspace.ID); !found || held.ProjectID == nil || *held.ProjectID != exampleProject {
		t.Errorf("the store holds %+v for the workspace %s, want it bound to %q", held, workspace.ID, exampleProject)
	}

	bound, found := f.entitlement(free.ID)
	if !found || bound.BoundAt == nil || !bound.BoundAt.Equal(f.now) {
		t.Errorf("the entitlement's binding is recorded as %+v, want %s", bound.BoundAt, f.now)
	}
	if bound.WorkspaceID == nil || *bound.WorkspaceID != workspace.ID {
		t.Errorf("the bound entitlement names the workspace %v, want %s", bound.WorkspaceID, workspace.ID)
	}
	if bound.Status != entitlementActive {
		t.Errorf("the bound entitlement is %q, want %q", bound.Status, entitlementActive)
	}
	if bound.RebindableAt != nil {
		t.Errorf("the first binding holds the next one back until %s, and D2198 leaves the first rebind immediate", bound.RebindableAt)
	}

	if len(f.mints.answers) != 1 {
		t.Fatalf("binding minted %d keys, want one", len(f.mints.answers))
	}
	claims := f.claimsOf(t, 0, exampleProject)
	if claims.WorkspaceID != exampleProject {
		t.Errorf("the minted key names the workspace %q, want the project id %q (TN2)", claims.WorkspaceID, exampleProject)
	}
	if claims.Organisation != org.ID {
		t.Errorf("the minted key names the organisation %q, want %s", claims.Organisation, org.ID)
	}
	if claims.Tier != licence.TierTeam || claims.Kind != licence.KindComped || claims.Seats != 1 {
		t.Errorf("the free seat's key is %s/%s at %d seat(s), want team/comped at one",
			claims.Tier, claims.Kind, claims.Seats)
	}
	state, known, err := f.store.KeyState(context.Background(), claims.KeyID)
	if err != nil || !known {
		t.Fatalf("the key state of %s: known=%v err=%v", claims.KeyID, known, err)
	}
	if state.WorkspaceID != exampleProject {
		t.Errorf("the key's state names the workspace %q, want %q: a renewal mints its successor into it",
			state.WorkspaceID, exampleProject)
	}
}

// TestUnboundEntitlementHasNoKey: an entitlement nobody has bound is a state and
// not an error (F25). Nothing is minted, the workspace has no project, and the
// store holds no issuance — which is what makes "not bound yet" a licence a
// customer can still bind rather than a key that was issued into nothing.
func TestUnboundEntitlementHasNoKey(t *testing.T) {
	f := newBindingFixture(t)
	_, granted, _ := f.licensingWorkspace(t)
	// Read back rather than trusting the value the grant answered: what the store
	// holds is the claim, and a grant that wrote the binding into its answer
	// alone would pass a case that read its own return.
	workspace, found := f.workspace(granted.ID)
	if !found {
		t.Fatalf("the workspace %s is gone from the store", granted.ID)
	}
	held, found := f.entitlement(f.onlyEntitlement(t, granted.OrganisationID).ID)
	if !found {
		t.Fatalf("the entitlement of %s is gone from the store", granted.ID)
	}

	if len(f.mints.answers) != 0 {
		t.Errorf("an unbound entitlement minted %d keys, want none", len(f.mints.answers))
	}
	if issued, err := f.store.Issuances(context.Background()); err != nil || len(issued) != 0 {
		t.Errorf("the store holds %d issuances for an unbound entitlement, want none (err=%v)", len(issued), err)
	}
	if held.BoundAt != nil {
		t.Errorf("the unbound entitlement records a binding at %s", held.BoundAt)
	}
	if workspace.ProjectID != nil {
		t.Errorf("the unbound workspace names the project %v", workspace.ProjectID)
	}
	if workspace.EntitlementID == nil || *workspace.EntitlementID != held.ID {
		t.Errorf("the workspace is licensed by %v, want the entitlement granted for it (%s)", workspace.EntitlementID, held.ID)
	}
	if issued := f.entitlementsOf(t, held.OrganisationID); len(issued) != 1 || issued[0].Status != entitlementActive {
		t.Errorf("the organisation holds %+v, want the one active entitlement", issued)
	}
}

// TestTermRunsFromPayment: the term is the payment's and binding does not start
// a new one (D2197). An annual licence paid for ten days ago has twenty days
// left of its month when it is finally bound, and the key the binding mints runs
// to the end of the month that was paid for.
func TestTermRunsFromPayment(t *testing.T) {
	f := newBindingFixture(t)
	org := f.seededOrganisation("org-dana", "Dana Ltd", orgTypeCustomer, danaUID, danaEmail, roleOwner)
	workspace := f.seedWorkspace("ws-1", org.ID, "Dana's portal")
	paid := f.now.Add(-10 * 24 * time.Hour)
	held := f.grant(t, entitlementGrant{
		At:             paid,
		OrganisationID: org.ID,
		WorkspaceID:    &workspace.ID,
		Source:         entitlementSourcePurchase,
		Row:            PolicyTeamMonthly,
		Seats:          2,
		StartsAt:       paid,
	})
	if want := paid.Add(monthTerm); !held.EndsAt.Equal(want) {
		t.Fatalf("the month bought on %s ends %s, want %s: the term runs from the payment", paid, held.EndsAt, want)
	}

	rec := f.bind(t, held.ID, exampleProject)
	if rec.Code != http.StatusOK {
		t.Fatalf("binding answered %d: %s", rec.Code, rec.Body)
	}
	claims := f.claimsOf(t, 0, exampleProject)
	if !claims.IssuedAt.Equal(f.now) {
		t.Errorf("the key was issued at %s, want the binding's instant %s", claims.IssuedAt, f.now)
	}
	if !claims.PaidThrough.Equal(held.EndsAt) {
		t.Errorf("the key runs to %s, want the end of the term the customer paid for, %s (D2197, F30)",
			claims.PaidThrough, held.EndsAt)
	}
	if claims.PaidThrough.Equal(f.now.Add(monthTerm)) {
		t.Errorf("the key runs a fresh month from the binding, which is the term D2197 refuses")
	}
	issued := f.issuances(t)
	if len(issued) != 1 {
		t.Fatalf("the store holds %d issuances, want one", len(issued))
	}
	if !issued[0].Expires.Equal(held.EndsAt) {
		t.Errorf("the issuance ends %s, want the entitlement's own end %s", issued[0].Expires, held.EndsAt)
	}
	// The whole days the issuance stands for are what is LEFT of the term: ten of
	// its thirty days were spent before anything was bound.
	if issued[0].TermDays != 20 {
		t.Errorf("the issuance stands for %d day(s), want the 20 the term has left", issued[0].TermDays)
	}
}

// TestWorkspaceIDIsAFirebaseProjectID: the key's `workspace_id` claim is the
// Firebase project id and never the portal's own workspace id (TN2, D2196). An
// installation compares that claim with its own project, so a key carrying the
// portal's id is a key no installation accepts.
func TestWorkspaceIDIsAFirebaseProjectID(t *testing.T) {
	f := newBindingFixture(t)
	_, workspace, held := f.licensingWorkspace(t)

	if rec := f.bind(t, held.ID, exampleProject); rec.Code != http.StatusOK {
		t.Fatalf("binding answered %d: %s", rec.Code, rec.Body)
	}
	claims := f.claimsOf(t, 0, exampleProject)
	if claims.WorkspaceID != exampleProject {
		t.Errorf("the key's workspace_id is %q, want the project id %q", claims.WorkspaceID, exampleProject)
	}
	if claims.WorkspaceID == workspace.ID {
		t.Errorf("the key carries the portal's own workspace id %q", claims.WorkspaceID)
	}
	bound, _ := f.workspace(workspace.ID)
	if bound.ProjectID == nil || *bound.ProjectID != exampleProject {
		t.Errorf("the workspace's project id is %v, want %q", bound.ProjectID, exampleProject)
	}
	if bound.ID == *bound.ProjectID {
		t.Errorf("the workspace's portal id and its project id are one value, %q", bound.ID)
	}
}

// TestOnlyOwnersAndAdminsBind: binding mints a key for an organisation, so it is
// an owner's or an admin's act (P3.1). A member is refused before anything is
// minted, and the refusal is the one an SPA branches on.
func TestOnlyOwnersAndAdminsBind(t *testing.T) {
	f := newBindingFixture(t)
	org, _, held := f.licensingWorkspace(t)
	f.seed(membershipsCollection, membershipID(org.ID, erinUID), WireMembership{
		OrganisationID: org.ID, UID: erinUID, Email: erinEmail, Role: roleMember, CreatedAt: f.now,
	})

	f.actsAs(erinUID, erinEmail, signInGoogle)
	rec := f.bind(t, held.ID, exampleProject)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a member's binding answered %d, want 403: %s", rec.Code, rec.Body)
	}
	if code := f.refused(rec); code != "not_permitted" {
		t.Errorf("a member's binding refused with %q, want not_permitted", code)
	}
	if len(f.mints.answers) != 0 {
		t.Errorf("a refused binding minted %d keys", len(f.mints.answers))
	}

	f.setRole(org.ID, erinUID, roleAdmin)
	if rec := f.bind(t, held.ID, exampleProject); rec.Code != http.StatusOK {
		t.Fatalf("an admin's binding answered %d: %s", rec.Code, rec.Body)
	}
	f.actsAs(danaUID, danaEmail, signInGoogle)
	if rec := f.bind(t, held.ID, secondProject); rec.Code != http.StatusOK {
		t.Fatalf("an owner's rebind answered %d: %s", rec.Code, rec.Body)
	}
	if len(f.mints.answers) != 2 {
		t.Errorf("two bindings minted %d keys, want two", len(f.mints.answers))
	}
}

// TestAnotherOrgsEntitlementIsRefused: every route of this area is answered for
// the caller's own organisation, and a licence of somebody else's is not there
// (P3.1's cross-tenant case). The refusal is the one an organisation read earns,
// so a stranger learns nothing about which ids exist.
func TestAnotherOrgsEntitlementIsRefused(t *testing.T) {
	f := newBindingFixture(t)
	_, _, held := f.licensingWorkspace(t)
	f.signedUp(erinUID, erinEmail, "Erin Ltd")

	f.actsAs(erinUID, erinEmail, signInGoogle)
	rec := f.bind(t, held.ID, exampleProject)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("binding another organisation's licence answered %d, want 404: %s", rec.Code, rec.Body)
	}
	if code := f.refused(rec); code != "not_found" {
		t.Errorf("binding another organisation's licence refused with %q, want not_found", code)
	}
	if len(f.mints.answers) != 0 {
		t.Errorf("a refused binding minted %d keys", len(f.mints.answers))
	}

	// The two reads are the caller's own organisation's and hold nothing of the
	// other one: the free seat the second organisation was granted is what its
	// list answers, and none of the first organisation's records is in it.
	listed := readAnswer[WireEntitlementList](f.fixture, f.apiAsCaller(http.MethodGet, apiEntitlementsPath, "")).Entitlements
	if len(listed) != 1 || listed[0].Source != entitlementSourceFreeSeat {
		t.Errorf("the caller's own licences are %+v, want the single free seat their sign-up granted", listed)
	}
	for _, one := range listed {
		if one.OrganisationID != listed[0].OrganisationID {
			t.Errorf("a licence of another organisation (%s) is in the caller's list", one.OrganisationID)
		}
	}
	if workspaces := readAnswer[WireWorkspaceList](f.fixture, f.apiAsCaller(http.MethodGet, apiWorkspacesPath, "")).Workspaces; len(workspaces) != 0 {
		t.Errorf("the caller's own workspaces are %+v, want none: the other organisation's are not theirs", workspaces)
	}
}

// TestProjectIDIsBoundOnce: one project id is bound by one workspace across
// every organisation (D2204). A second organisation naming the same project is
// refused, and nothing is minted for it.
func TestProjectIDIsBoundOnce(t *testing.T) {
	f := newBindingFixture(t)
	_, _, mine := f.licensingWorkspace(t)
	if rec := f.bind(t, mine.ID, exampleProject); rec.Code != http.StatusOK {
		t.Fatalf("the first binding answered %d: %s", rec.Code, rec.Body)
	}

	other := f.seededOrganisation("org-erin", "Erin Ltd", orgTypeCustomer, erinUID, erinEmail, roleOwner)
	theirs := f.seedWorkspace("ws-erin", other.ID, "Erin's portal")
	held := f.grant(t, entitlementGrant{
		At:             f.now,
		OrganisationID: other.ID,
		WorkspaceID:    &theirs.ID,
		Source:         entitlementSourcePurchase,
		Row:            PolicyTeamAnnual,
		Seats:          1,
		StartsAt:       f.now,
	})
	f.actsAs(erinUID, erinEmail, signInGoogle)
	rec := f.bind(t, held.ID, exampleProject)
	if rec.Code != http.StatusConflict {
		t.Fatalf("binding a project another organisation holds answered %d, want 409: %s", rec.Code, rec.Body)
	}
	if code := f.refused(rec); code != "project_id_bound" {
		t.Errorf("the second binding refused with %q, want project_id_bound", code)
	}
	if len(f.mints.answers) != 1 {
		t.Errorf("%d keys were minted, want the one the first binding minted", len(f.mints.answers))
	}
	if bound, _ := f.workspace(theirs.ID); bound.ProjectID != nil {
		t.Errorf("the refused workspace is bound to %v", bound.ProjectID)
	}
	// The same organisation rebinding its own workspace to the project it already
	// holds is not a second binding of it: that is D2198's rebind, and the check
	// is about another workspace and not about the project id appearing twice.
	f.actsAs(danaUID, danaEmail, signInGoogle)
	if rec := f.bind(t, mine.ID, exampleProject); rec.Code != http.StatusOK {
		t.Fatalf("rebinding to the project the workspace already holds answered %d: %s", rec.Code, rec.Body)
	}
}

// TestFirstRebindIsImmediate: the first rebind after a binding is not held by
// the 30-day clock, so an owner can undo a binding somebody else made at once
// (D2198). The clock is set by the rebind itself.
func TestFirstRebindIsImmediate(t *testing.T) {
	f := newBindingFixture(t)
	_, _, held := f.licensingWorkspace(t)
	if rec := f.bind(t, held.ID, exampleProject); rec.Code != http.StatusOK {
		t.Fatalf("binding answered %d: %s", rec.Code, rec.Body)
	}
	if bound, _ := f.entitlement(held.ID); bound.RebindableAt != nil {
		t.Fatalf("the binding holds the next one back until %s, and the first rebind is immediate (D2198)", bound.RebindableAt)
	}

	rec := f.bind(t, held.ID, secondProject)
	if rec.Code != http.StatusOK {
		t.Fatalf("the first rebind, at the binding's own instant, answered %d: %s", rec.Code, rec.Body)
	}
	rebound := readAnswer[WireWorkspace](f.fixture, rec)
	if rebound.ProjectID == nil || *rebound.ProjectID != secondProject {
		t.Errorf("the rebound workspace names the project %v, want %q", rebound.ProjectID, secondProject)
	}
	after, _ := f.entitlement(held.ID)
	if after.RebindableAt == nil || !after.RebindableAt.Equal(f.now.Add(rebindWindow)) {
		t.Errorf("the rebind leaves the next one free at %v, want %s: the clock starts with it",
			after.RebindableAt, f.now.Add(rebindWindow))
	}
}

// TestRebindIsOncePerThirtyDays: a rebind is one per thirty days (D2198). The
// second one inside the window is refused and mints nothing, and the same
// request after the window is served.
func TestRebindIsOncePerThirtyDays(t *testing.T) {
	f := newBindingFixture(t)
	_, _, held := f.licensingWorkspace(t)
	if rec := f.bind(t, held.ID, exampleProject); rec.Code != http.StatusOK {
		t.Fatalf("binding answered %d: %s", rec.Code, rec.Body)
	}
	if rec := f.bind(t, held.ID, secondProject); rec.Code != http.StatusOK {
		t.Fatalf("the first rebind answered %d: %s", rec.Code, rec.Body)
	}

	f.advance(rebindWindow - time.Hour)
	rec := f.bind(t, held.ID, exampleProject)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("a rebind inside the window answered %d, want 429: %s", rec.Code, rec.Body)
	}
	if code := f.refused(rec); code != "rebind_too_soon" {
		t.Errorf("the early rebind refused with %q, want rebind_too_soon", code)
	}
	if len(f.mints.answers) != 2 {
		t.Errorf("the refused rebind minted a key: %d keys are out, want the two bindings'", len(f.mints.answers))
	}
	if bound, _ := f.workspace(f.workspaceOf(t, held)); bound.ProjectID == nil || *bound.ProjectID != secondProject {
		t.Errorf("the refused rebind moved the workspace: it names %v, want %q", bound.ProjectID, secondProject)
	}

	f.advance(2 * time.Hour)
	if rec := f.bind(t, held.ID, exampleProject); rec.Code != http.StatusOK {
		t.Fatalf("the rebind after the window answered %d: %s", rec.Code, rec.Body)
	}
	if len(f.mints.answers) != 3 {
		t.Errorf("three bindings minted %d keys, want three", len(f.mints.answers))
	}
}

// TestRebindRevokesTheOldKey: a rebind revokes the key it replaces (D2198) —
// the key of a workspace that moved is a key of an installation nobody is
// looking at any more, and the store's own list is where the licensor reads it.
func TestRebindRevokesTheOldKey(t *testing.T) {
	f := newBindingFixture(t)
	_, _, held := f.licensingWorkspace(t)
	if rec := f.bind(t, held.ID, exampleProject); rec.Code != http.StatusOK {
		t.Fatalf("binding answered %d: %s", rec.Code, rec.Body)
	}
	old := f.claimsOf(t, 0, exampleProject)

	if rec := f.bind(t, held.ID, secondProject); rec.Code != http.StatusOK {
		t.Fatalf("the rebind answered %d: %s", rec.Code, rec.Body)
	}
	withdrawn, found, err := f.store.Revocation(context.Background(), old.KeyID)
	if err != nil || !found {
		t.Fatalf("the replaced key %s is not in the store's revocation list (found=%v err=%v)", old.KeyID, found, err)
	}
	if withdrawn.Reason != ReasonRebind {
		t.Errorf("the replaced key's reason is %q, want %q", withdrawn.Reason, ReasonRebind)
	}
	if !withdrawn.At.Equal(f.now) {
		t.Errorf("the revocation is recorded at %s, want the rebind's instant %s", withdrawn.At, f.now)
	}
	fresh := f.claimsOf(t, 1, secondProject)
	if _, found, err := f.store.Revocation(context.Background(), fresh.KeyID); err != nil || found {
		t.Errorf("the key the rebind minted is itself revoked (found=%v err=%v)", found, err)
	}
}

// TestRebindRotatesTheSecret: a rebind mints a new key with a secret of its own
// (D2198, D2135). The old secret is what a leaked installation holds, and a
// rebind that kept it would leave whoever holds it able to renew the key of the
// workspace the licence moved to.
func TestRebindRotatesTheSecret(t *testing.T) {
	f := newBindingFixture(t)
	_, _, held := f.licensingWorkspace(t)
	if rec := f.bind(t, held.ID, exampleProject); rec.Code != http.StatusOK {
		t.Fatalf("binding answered %d: %s", rec.Code, rec.Body)
	}
	first := f.claimsOf(t, 0, exampleProject)
	firstSecret := f.mints.answers[0].RenewalSecret

	if rec := f.bind(t, held.ID, secondProject); rec.Code != http.StatusOK {
		t.Fatalf("the rebind answered %d: %s", rec.Code, rec.Body)
	}
	second := f.claimsOf(t, 1, secondProject)
	secondSecret := f.mints.answers[1].RenewalSecret
	if firstSecret == "" || secondSecret == "" {
		t.Fatalf("a binding answered no renewal secret (%q, %q), and a key nobody can renew is not a licence (D2135)", firstSecret, secondSecret)
	}
	if firstSecret == secondSecret {
		t.Errorf("the rebind kept the secret %q", firstSecret)
	}
	if first.RenewalSecretHash == second.RenewalSecretHash {
		t.Errorf("both keys carry the renewal secret hash %q, want one each (D2135)", first.RenewalSecretHash)
	}
	if first.RenewalSecretHash != digest(firstSecret) {
		t.Errorf("the key's hash is not the digest of the secret it was answered with, which is what /renew compares")
	}
	state, known, err := f.store.KeyState(context.Background(), second.KeyID)
	if err != nil || !known {
		t.Fatalf("the state of the key the rebind minted: known=%v err=%v", known, err)
	}
	if state.RenewalSecretHash != digest(secondSecret) {
		t.Errorf("the state carries the hash %q, want the new secret's %q", state.RenewalSecretHash, digest(secondSecret))
	}
}

// TestRevokedKeysSecretFetchesNothing: the secret of a key a rebind replaced
// fetches nothing at all (D2198). The store's revocation list is what §8's
// renewal reads first, so the holder of the old installation is handed no
// successor and told nothing about the licence's new workspace.
func TestRevokedKeysSecretFetchesNothing(t *testing.T) {
	f := newBindingFixture(t)
	_, _, held := f.licensingWorkspace(t)
	if rec := f.bind(t, held.ID, exampleProject); rec.Code != http.StatusOK {
		t.Fatalf("binding answered %d: %s", rec.Code, rec.Body)
	}
	old := f.claimsOf(t, 0, exampleProject)
	oldSecret := f.mints.answers[0].RenewalSecret
	if rec := f.bind(t, held.ID, secondProject); rec.Code != http.StatusOK {
		t.Fatalf("the rebind answered %d: %s", rec.Code, rec.Body)
	}

	rec := f.call(RouteRenew, RenewalCall{KeyID: old.KeyID, WorkspaceID: exampleProject, RenewalSecret: oldSecret})
	if rec.Code != http.StatusOK {
		t.Fatalf("the replaced key's own renewal call answered %d, want the revoked state: %s", rec.Code, rec.Body)
	}
	answer := answerOf(t, rec)
	if answer.Key != "" || answer.RenewalSecret != "" {
		t.Errorf("the replaced key's secret fetched a key: %+v", answer)
	}
	if answer.Status != StatusRevoked {
		t.Errorf("the replaced key answers %q, want %q (§8)", answer.Status, StatusRevoked)
	}
	if strings.Contains(rec.Body.String(), `"key"`) {
		t.Errorf("the answer carries a key field at all: %s", rec.Body)
	}
}

// TestBindingNeedsAFugaroInstallation: the project must exist (D2205). A project
// with no database is refused with the one instruction that fixes it, and
// nothing is bound and nothing is minted — a binding is not something this
// service may guess at.
func TestBindingNeedsAFugaroInstallation(t *testing.T) {
	f := newBindingFixture(t)
	f.probe.status = http.StatusNotFound
	f.probe.body = `{"error":"404 Not Found. No such database."}`
	_, workspace, held := f.licensingWorkspace(t)

	rec := f.bind(t, held.ID, exampleProject)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("binding a project without an installation answered %d, want 422: %s", rec.Code, rec.Body)
	}
	fault := readAnswer[apiError](f.fixture, rec).Error
	if fault.Code != "no_installation" {
		t.Errorf("the refusal's code is %q, want no_installation", fault.Code)
	}
	if !strings.Contains(fault.Message, "fugaro setup") {
		t.Errorf("the refusal's sentence is %q, and the fix is a setup the person has to run", fault.Message)
	}
	if len(f.mints.answers) != 0 {
		t.Errorf("a refused binding minted %d keys", len(f.mints.answers))
	}
	if bound, _ := f.entitlement(held.ID); bound.BoundAt != nil {
		t.Errorf("the entitlement records a binding at %s, and nothing was bound", bound.BoundAt)
	}
	if after, _ := f.workspace(workspace.ID); after.ProjectID != nil {
		t.Errorf("the workspace names the project %v, and nothing was bound", after.ProjectID)
	}
}

// TestUnreachableProbeNeverBinds: no answer refuses and never binds unchecked
// (D2205). A database that cannot be reached — one that closes the connection,
// and one that answers a failure of its own — is neither a locked installation
// nor a missing one, and the person is told to try again.
func TestUnreachableProbeNeverBinds(t *testing.T) {
	for _, answer := range []struct {
		name  string
		shape func(f *bindingFixture)
	}{
		{name: "no answer at all", shape: func(f *bindingFixture) { f.probe.refuses = true }},
		{name: "a database that failed", shape: func(f *bindingFixture) {
			f.probe.status = http.StatusInternalServerError
			f.probe.body = "the database is having a bad day"
		}},
	} {
		t.Run(answer.name, func(t *testing.T) {
			f := newBindingFixture(t)
			answer.shape(f)
			_, _, held := f.licensingWorkspace(t)

			rec := f.bind(t, held.ID, exampleProject)
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("binding against an unreadable database answered %d, want 503: %s", rec.Code, rec.Body)
			}
			fault := readAnswer[apiError](f.fixture, rec).Error
			if fault.Code != "unreachable" {
				t.Errorf("the refusal's code is %q, want unreachable", fault.Code)
			}
			if !strings.Contains(strings.ToLower(fault.Message), "try again") {
				t.Errorf("the refusal's sentence is %q, and the one thing the person can do is try again", fault.Message)
			}
			if len(f.mints.answers) != 0 {
				t.Errorf("a refused binding minted %d keys", len(f.mints.answers))
			}
			if bound, _ := f.entitlement(held.ID); bound.BoundAt != nil {
				t.Errorf("the entitlement records a binding at %s, and nothing was bound", bound.BoundAt)
			}
		})
	}
}

// TestProjectIDShapeIsCheckedBeforeTheProbe: the id is checked against
// Firebase's own grammar before it is put into a host name (D2205, F41). An id
// that is not one is refused as the caller's mistake and no request leaves this
// service: a name that reaches a host builder is a name that can name another
// host.
func TestProjectIDShapeIsCheckedBeforeTheProbe(t *testing.T) {
	for _, bad := range []string{
		"Example-Portal",
		"example-portal!",
		"example.portal",
		"../example-portal",
		"example-portal-default-rtdb.firebaseio.com",
		"exam",
		"-example-portal",
		"example-portal-",
		"example portal",
		"",
	} {
		t.Run(bad, func(t *testing.T) {
			f := newBindingFixture(t)
			_, _, held := f.licensingWorkspace(t)

			rec := f.bind(t, held.ID, bad)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("binding the project id %q answered %d, want 422: %s", bad, rec.Code, rec.Body)
			}
			if code := f.refused(rec); code != "invalid_project_id" {
				t.Errorf("the refusal's code is %q, want invalid_project_id", code)
			}
			if saw := f.probe.saw(); len(saw) != 0 {
				t.Errorf("the probe was asked about %v, and an id outside the grammar never reaches a host name", saw)
			}
			if len(f.mints.answers) != 0 {
				t.Errorf("a refused binding minted %d keys", len(f.mints.answers))
			}
		})
	}
	// The ids the grammar admits are the ones binding proceeds with, so the check
	// is the grammar's and not a list of names this case happens to use.
	for _, good := range []string{"a-very-l", exampleProject, "aaaaaa"} {
		f := newBindingFixture(t)
		_, _, held := f.licensingWorkspace(t)
		if rec := f.bind(t, held.ID, good); rec.Code != http.StatusOK {
			t.Errorf("binding the project id %q answered %d, want it admitted: %s", good, rec.Code, rec.Body)
		}
	}
}

// TestTheProbeReadsTheProjectsOwnDatabase: D2205's read is of the project's own
// Realtime Database, which every installation creates
// (`deploy/terraform/gcp/modules/firebase/firebase.tf`), unauthenticated, at its
// root. The address is pinned here because the answers it gives are not: what a
// real database answers a stranger is UNVERIFIED (the reconciliation's open
// list), and the address is the half this repository can hold.
func TestTheProbeReadsTheProjectsOwnDatabase(t *testing.T) {
	probe := projectInstallation{database: productionDatabase}
	address := probe.url(exampleProject)
	want := "https://" + exampleProject + "-default-rtdb.firebaseio.com/.json"
	if address != want {
		t.Errorf("the probe reads %s, want %s", address, want)
	}
	// The project id is put into the host BEFORE the database's own suffix: an id
	// built from the name of another host is refused by the shape check, and this
	// is what the shape is protecting.
	if !strings.HasPrefix(strings.TrimPrefix(address, "https://"), exampleProject+"-") {
		t.Errorf("the address %s does not begin with the project id it was asked about", address)
	}
}

// TestAnEndedEntitlementCannotBeBound: a licence whose term has run out mints
// nothing (F27's rule, read at this door). The minter refuses an end the clock
// has reached, and this is the same refusal said in the words of the screen that
// asked for it.
func TestAnEndedEntitlementCannotBeBound(t *testing.T) {
	f := newBindingFixture(t)
	org := f.seededOrganisation("org-dana", "Dana Ltd", orgTypeCustomer, danaUID, danaEmail, roleOwner)
	workspace := f.seedWorkspace("ws-1", org.ID, "Dana's portal")
	paid := f.now.Add(-monthTerm)
	held := f.grant(t, entitlementGrant{
		At:             paid,
		OrganisationID: org.ID,
		WorkspaceID:    &workspace.ID,
		Source:         entitlementSourcePurchase,
		Row:            PolicyTeamMonthly,
		Seats:          1,
		StartsAt:       paid,
	})

	rec := f.bind(t, held.ID, exampleProject)
	if rec.Code != http.StatusConflict {
		t.Fatalf("binding an ended licence answered %d, want 409: %s", rec.Code, rec.Body)
	}
	if code := f.refused(rec); code != "licence_ended" {
		t.Errorf("the refusal's code is %q, want licence_ended", code)
	}
	if len(f.mints.answers) != 0 {
		t.Errorf("a refused binding minted %d keys", len(f.mints.answers))
	}
}

// TestAirGappedIsIssuedOnce: an air-gapped entitlement is issued once, at
// binding, as a file (D2206). The file is the signed licence text and holds no
// secret, so a second download is the same bytes; and a rebind is refused for a
// customer, because a second issue of it is a second live copy of a key nothing
// can revoke.
func TestAirGappedIsIssuedOnce(t *testing.T) {
	f := newBindingFixture(t)
	org := f.seededOrganisation("org-dana", "Dana Ltd", orgTypeCustomer, danaUID, danaEmail, roleOwner)
	workspace := f.seedWorkspace("ws-1", org.ID, "Dana's air gap")
	held := f.grant(t, entitlementGrant{
		At:             f.now,
		OrganisationID: org.ID,
		WorkspaceID:    &workspace.ID,
		Source:         entitlementSourceOperations,
		Row:            PolicyEnterpriseAirGapped,
		Seats:          5,
		StartsAt:       f.now,
	})
	if !held.AirGapped {
		t.Fatalf("the %s row is not air-gapped in this grant", held.Row)
	}

	if rec := f.bind(t, held.ID, exampleProject); rec.Code != http.StatusOK {
		t.Fatalf("binding an air-gapped licence answered %d: %s", rec.Code, rec.Body)
	}
	file, found := heldRecord[WireLicenceFile](f.fixture, licenceFilesCollection, held.ID)
	if !found {
		t.Fatalf("no licence file is kept for the air-gapped entitlement %s", held.ID)
	}
	if file.Licence == "" {
		t.Fatalf("the licence file holds no licence text, and it is the file (D2206)")
	}
	claims, _, err := licence.Verify([]byte(file.Licence), f.now, f.ring, exampleProject)
	if err != nil {
		t.Fatalf("verifying the air-gapped licence file: %v", err)
	}
	if claims.CheckInterval != nil {
		t.Errorf("the air-gapped key checks at %q, and its row is the one row that never contacts the server (D2138)",
			*claims.CheckInterval)
	}
	if claims.Seats != 5 || claims.Tier != licence.TierEnterprise {
		t.Errorf("the air-gapped key is %s at %d seat(s), want enterprise at 5", claims.Tier, claims.Seats)
	}
	if file.KeyID != claims.KeyID {
		t.Errorf("the file names the key %s and the text is %s", file.KeyID, claims.KeyID)
	}
	if !file.PaidThrough.Equal(claims.PaidThrough) {
		t.Errorf("the file runs to %s and the key to %s", file.PaidThrough, claims.PaidThrough)
	}
	if len(f.mints.answers) != 1 {
		t.Fatalf("binding minted %d keys, want the one the file holds", len(f.mints.answers))
	}
	if secret := f.mints.answers[0].RenewalSecret; secret != "" {
		t.Errorf("the air-gapped key was answered with a renewal secret, and a file that holds one is a credential on disk")
	}

	// The second download is the same text: nothing is re-signed and nothing is
	// rotated, which is what "issued once" is worth to the person holding a copy.
	again, found := heldRecord[WireLicenceFile](f.fixture, licenceFilesCollection, held.ID)
	if !found || again != file {
		t.Fatalf("a second read of the licence file answered %+v, want the file that was issued: %+v", again, file)
	}

	f.advance(time.Hour)
	rec := f.bind(t, held.ID, secondProject)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("rebinding an air-gapped licence answered %d, want 403: %s", rec.Code, rec.Body)
	}
	if code := f.refused(rec); code != "air_gapped" {
		t.Errorf("the rebind refused with %q, want air_gapped", code)
	}
	if len(f.mints.answers) != 1 {
		t.Errorf("the refused rebind minted a second key: %d are out, and a file nothing can revoke is issued once",
			len(f.mints.answers))
	}
}

// workspacesFor is one organisation's workspaces as the list route answers them.
func (f *bindingFixture) workspacesFor(t *testing.T, organisationID string) []WireWorkspace {
	t.Helper()
	held, err := recordsWhere[WireWorkspace](context.Background(), f.docs, workspacesCollection,
		Where{Field: "organisation_id", Value: organisationID})
	if err != nil {
		t.Fatalf("reading the workspaces of %s: %v", organisationID, err)
	}
	return held
}

// workspaceOf is the workspace one entitlement is bound to, which is null while
// the licence is unbound and is the workspace a case has to read the project id
// from.
func (f *bindingFixture) workspaceOf(t *testing.T, held WireEntitlement) string {
	t.Helper()
	bound, found := f.entitlement(held.ID)
	if !found || bound.WorkspaceID == nil {
		t.Fatalf("the entitlement %s names no workspace", held.ID)
	}
	return *bound.WorkspaceID
}

// issuances is every issuance the store holds.
func (f *bindingFixture) issuances(t *testing.T) []Record {
	t.Helper()
	issued, err := f.store.Issuances(context.Background())
	if err != nil {
		t.Fatalf("reading the store's issuances: %v", err)
	}
	return issued
}
