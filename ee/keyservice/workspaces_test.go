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
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/keyservice/docstore"
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
// server of this case's own, the minter wrapped so that the whole answer — the
// secret as well as the key — is kept, and the document store held to the one
// rule the deploy's own keeps: a transaction reads before it writes
// (docstore's ErrReadAfterWrite). Every case here gets all three, so no case can
// reach Firebase or the key ring by forgetting one, and no case can pass on a
// transaction the backend the deploy runs would refuse.
func newBindingFixture(t *testing.T) *bindingFixture {
	t.Helper()
	return newBindingFixtureWith(t, nil)
}

// newBindingFixtureWith is the same fixture with one change of a case's own made
// before the intake is built from it, which is where a case that has to fail or
// race a write puts the store that does it.
func newBindingFixtureWith(t *testing.T, edit func(*fixture)) *bindingFixture {
	t.Helper()
	mints := &bundleMints{}
	probe := newProbeServer()
	f := newFixtureWith(t, Production, func(f *fixture) {
		mints.inner = f.recording
		f.deps.Mints = mints
		probe.install(t)
		f.deps.Docs = readBeforeWrite{Docs: f.docs}
		if edit != nil {
			edit(f)
		}
	})
	// The sign-ins these cases make are Google's, which is what an organisation's
	// own members sign in with and what no SSO rule refuses.
	f.actsAs(danaUID, danaEmail, signInGoogle)
	return &bindingFixture{fixture: f, probe: probe, mints: mints}
}

// probeServer is D2205's database in a case: what each of the hosts a default
// database can be at answers, and every host and project id it was asked about.
// It is installed into the package's own probe seam, which is the seam a
// deployment fills with Firebase's three hosts.
//
// One server stands in for all three, so `install` gives each host a path of its
// own and what a read is answered with is what the host it went to was given: a
// case about an installation in a region is exactly a case where the hosts
// answer differently.
type probeServer struct {
	mu      sync.Mutex
	asked   []probeAsk
	answers map[string]probeAnswer
	// during is one step a case runs while the database is being read — the
	// window a competing request has to get through, since the probe is where
	// this service waits on another system. It runs once.
	during func()

	server *httptest.Server
}

// probeAsk is one read the service made: the host it went to and the project id
// it asked about.
type probeAsk struct {
	host    string
	project string
}

// probeAnswer is what one host answers. refuses is a host that is not there at
// all, which is the third answer D2205 names: no answer binds unchecked.
type probeAnswer struct {
	status  int
	body    string
	refuses bool
}

// The two answers most cases want, named: a database whose rules are locked,
// which is every installation's, and a host holding no database at its address.
var (
	probeLocked  = probeAnswer{status: http.StatusUnauthorized, body: `{"error":"Permission denied"}`}
	probeMissing = probeAnswer{status: http.StatusNotFound, body: `{"error":"404 Not Found. No such database."}`}
)

// newProbeServer is every host answering as a locked database: a case that is
// not about D2205's read at all binds without shaping the probe.
func newProbeServer() *probeServer {
	answers := make(map[string]probeAnswer, len(productionDatabases))
	for _, host := range productionDatabases {
		answers[host.location] = probeLocked
	}
	return &probeServer{answers: answers}
}

// all sets what every host answers, which is how a case that is about D2205's
// read as a whole shapes it: the three hosts are one fake.
func (p *probeServer) all(answer probeAnswer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for host := range p.answers {
		p.answers[host] = answer
	}
}

// at sets what one host answers and leaves the others alone: an installation
// whose database is in a region is what a case needs that for.
func (p *probeServer) at(host string, answer probeAnswer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.answers[host] = answer
}

func (p *probeServer) install(t *testing.T) {
	t.Helper()
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, project := probeAskOf(r.URL.Path)
		p.mu.Lock()
		p.asked = append(p.asked, probeAsk{host: host, project: project})
		answer, known := p.answers[host]
		during := p.during
		p.during = nil
		p.mu.Unlock()
		if !known {
			// A read at a path this case did not install a host at: the answer
			// is the failure of a host, which is what this is from here.
			answer = probeAnswer{status: http.StatusInternalServerError, body: "this case's database was asked at " + host + ", which is not one of its hosts"}
		}
		if during != nil {
			during()
		}
		if answer.refuses {
			// A connection closed with no answer at all: what a project whose
			// database cannot be reached looks like from here.
			panic(http.ErrAbortHandler)
		}
		w.WriteHeader(answer.status)
		_, _ = io.WriteString(w, answer.body)
	}))
	databases := make([]databaseHost, 0, len(productionDatabases))
	for _, host := range productionDatabases {
		databases = append(databases, databaseHost{
			location: host.location,
			address:  p.server.URL + "/" + host.location + "/%s",
		})
	}
	previous := probeInstallation
	probeInstallation = projectInstallation{databases: databases, client: p.server.Client()}
	t.Cleanup(func() {
		probeInstallation = previous
		p.server.Close()
	})
}

// probeAskOf reads the host and the project id out of one request's path, which
// the case's server is installed as: `<host>/<project>/.json`.
func probeAskOf(path string) (host, project string) {
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(path, "/"), "/.json"), "/")
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

// saw is every project id the case's database was asked about.
func (p *probeServer) saw() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	ids := make([]string, 0, len(p.asked))
	for _, ask := range p.asked {
		ids = append(ids, ask.project)
	}
	return ids
}

// sawHosts is every host the case's database was asked at, in the order the
// reads were made: which host answered an installation, and so which hosts were
// read at all, is what a case about a regional installation is about.
func (p *probeServer) sawHosts() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	hosts := make([]string, 0, len(p.asked))
	for _, ask := range p.asked {
		hosts = append(hosts, ask.host)
	}
	return hosts
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
	f.probe.all(probeMissing)
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
		{name: "no answer at all", shape: func(f *bindingFixture) {
			f.probe.all(probeAnswer{refuses: true})
		}},
		{name: "a database that failed", shape: func(f *bindingFixture) {
			f.probe.all(probeAnswer{
				status: http.StatusInternalServerError,
				body:   "the database is having a bad day",
			})
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
// root — at each of the three hosts a default database can answer at, the legacy
// one first. The addresses are pinned here because the answers they give are
// not: what a real database answers a stranger is UNVERIFIED (the
// reconciliation's open list), and the addresses are the half this repository
// can hold.
func TestTheProbeReadsTheProjectsOwnDatabase(t *testing.T) {
	want := []string{
		"https://" + exampleProject + "-default-rtdb.firebaseio.com/.json",
		"https://" + exampleProject + "-default-rtdb.europe-west1.firebasedatabase.app/.json",
		"https://" + exampleProject + "-default-rtdb.asia-southeast1.firebasedatabase.app/.json",
	}
	var read []string
	for _, host := range productionDatabases {
		address := host.url(exampleProject)
		read = append(read, address)
		// The project id is put into the host BEFORE the database's own suffix: an
		// id built from the name of another host is refused by the shape check, and
		// this is what the shape is protecting.
		if !strings.HasPrefix(strings.TrimPrefix(address, "https://"), exampleProject+"-") {
			t.Errorf("the address %s does not begin with the project id it was asked about", address)
		}
	}
	if !slices.Equal(read, want) {
		t.Errorf("the probe reads %v, want %v", read, want)
	}
	// The legacy host is the first read of the three, which is what `ask` rests
	// on: the ordinary project — every installation made before a region could be
	// chosen — is answered in one request.
	if len(read) == 0 || !strings.HasSuffix(read[0], ".firebaseio.com/.json") {
		t.Errorf("the first host the probe reads is %v, want the legacy firebaseio.com one", read)
	}
}

// TestAnInstallationInEuropeIsFound: an installation whose database is in
// europe-west1 answers at a host of its own, and D2205's read finds it there
// (F13, D2205). The legacy host is read first and has no database at its
// address, which is what every host but one says about an installation that is
// not its own; the binding then proceeds on the refusal the European host gives,
// which is the same refusal-is-an-installation rule the legacy host's answer is
// read by.
func TestAnInstallationInEuropeIsFound(t *testing.T) {
	f := newBindingFixture(t)
	f.probe.all(probeMissing)
	f.probe.at(europeDatabase, probeLocked)
	_, workspace, held := f.licensingWorkspace(t)

	rec := f.bind(t, held.ID, exampleProject)
	if rec.Code != http.StatusOK {
		t.Fatalf("binding an installation whose database is in europe-west1 answered %d, want 200: %s", rec.Code, rec.Body)
	}
	if bound, _ := f.entitlement(held.ID); bound.BoundAt == nil {
		t.Errorf("the entitlement records no binding, and the installation was found and the key minted")
	}
	if licensed, _ := f.workspace(workspace.ID); licensed.ProjectID == nil || *licensed.ProjectID != exampleProject {
		t.Errorf("the workspace is %+v, want it bound to %s", licensed, exampleProject)
	}
	if len(f.mints.answers) != 1 {
		t.Errorf("the binding minted %d keys, want one", len(f.mints.answers))
	}
	// Which hosts were read: the legacy one, which answered that it holds no
	// database, and then the European one, which answered as an installation. The
	// read stops at the first host that answers as one, so the asian host is
	// never asked — a binding is not three reads when one of them has answered.
	if saw := f.probe.sawHosts(); !slices.Equal(saw, []string{usCentralDatabase, europeDatabase}) {
		t.Errorf("the binding read %v, want the legacy host and then europe-west1", saw)
	}
}

// TestNoDatabaseAnywhereIsNoInstallation: a project with no database at any of
// the three hosts has no installation, and the person is told to set one up
// (D2205). Every host answers that it holds no database, and that is the one
// answer that is an absence rather than a host this service did not check.
func TestNoDatabaseAnywhereIsNoInstallation(t *testing.T) {
	f := newBindingFixture(t)
	f.probe.all(probeMissing)
	_, workspace, held := f.licensingWorkspace(t)

	rec := f.bind(t, held.ID, exampleProject)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("binding a project with no database anywhere answered %d, want 422: %s", rec.Code, rec.Body)
	}
	fault := readAnswer[apiError](f.fixture, rec).Error
	if fault.Code != "no_installation" {
		t.Errorf("the refusal's code is %q, want no_installation", fault.Code)
	}
	if !strings.Contains(fault.Message, "fugaro setup") {
		t.Errorf("the refusal's sentence is %q, and the fix is a setup the person has to run", fault.Message)
	}
	if saw := f.probe.sawHosts(); !slices.Equal(saw, []string{usCentralDatabase, europeDatabase, asiaDatabase}) {
		t.Errorf("the binding read %v, want all three hosts before it answered that there is no installation", saw)
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

// TestTheLegacyHostIsStillReadFirst: the legacy host is D2205's first read
// (F13), and the two regional hosts follow it in the order a database's
// locations are named. An installation at any of them is found, and the read
// ends at the first host that answers as one rather than at the one that holds
// it: which host holds the database is the platform's answer and not a decision
// this service makes.
func TestTheLegacyHostIsStillReadFirst(t *testing.T) {
	// An ordinary installation: the legacy host answers as one and nothing else is
	// read at all, which is the one-request path every installation made before a
	// region could be chosen takes.
	t.Run("an installation at the legacy host is read once", func(t *testing.T) {
		f := newBindingFixture(t)
		_, _, held := f.licensingWorkspace(t)

		rec := f.bind(t, held.ID, exampleProject)
		if rec.Code != http.StatusOK {
			t.Fatalf("binding an installation at the legacy host answered %d, want 200: %s", rec.Code, rec.Body)
		}
		if saw := f.probe.sawHosts(); !slices.Equal(saw, []string{usCentralDatabase}) {
			t.Errorf("the binding read %v, want the legacy host alone: the hosts after it are read only when it holds no database", saw)
		}
	})

	// And the order of the two regional hosts the read falls back through: the
	// legacy host, then europe-west1, then asia-southeast1.
	t.Run("the regional hosts are read in order", func(t *testing.T) {
		f := newBindingFixture(t)
		f.probe.all(probeMissing)
		f.probe.at(asiaDatabase, probeLocked)
		_, _, held := f.licensingWorkspace(t)

		rec := f.bind(t, held.ID, exampleProject)
		if rec.Code != http.StatusOK {
			t.Fatalf("binding an installation whose database is in asia-southeast1 answered %d, want 200: %s", rec.Code, rec.Body)
		}
		if saw := f.probe.sawHosts(); !slices.Equal(saw, []string{usCentralDatabase, europeDatabase, asiaDatabase}) {
			t.Errorf("the binding read %v, want the legacy host first and the regional ones after it in the order a database's locations are named", saw)
		}
	})
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

// readBeforeWrite is the fixture's store with the deploy's own rule on
// transactions: a read of a document the same transaction has already written is
// refused (docstore.ErrReadAfterWrite; docstore's own memory store and
// Firestore both hold it). The fixture's spy runs a transaction's function
// against itself and holds no such rule, so a grant or a binding that read what
// it had just written passed here and would be refused by the store the deploy
// runs — which is the one thing a test of this area may not be blind to.
//
// It is the base every case of this file is built on, so the rule is what these
// cases run under rather than something one of them remembers to ask for.
type readBeforeWrite struct {
	Docs
}

func (d readBeforeWrite) Update(ctx context.Context, fn func(context.Context, Tx) error) error {
	return d.Docs.Update(ctx, func(ctx context.Context, tx Tx) error {
		return fn(ctx, &readBeforeWriteTx{Tx: tx})
	})
}

// readBeforeWriteTx is one transaction under that rule. Both of a transaction's
// reads answer it: a document and a query are the same read of the store as far
// as this rule is concerned, and a query taken after a write is one the deploy's
// store refuses too.
type readBeforeWriteTx struct {
	Tx
	written bool
}

func (t *readBeforeWriteTx) Get(ctx context.Context, collection, id string) (Doc, error) {
	if t.written {
		return nil, docstore.ErrReadAfterWrite
	}
	return t.Tx.Get(ctx, collection, id)
}

func (t *readBeforeWriteTx) Query(ctx context.Context, collection string, where Where) ([]Doc, error) {
	if t.written {
		return nil, docstore.ErrReadAfterWrite
	}
	return t.Tx.Query(ctx, collection, where)
}

func (t *readBeforeWriteTx) Put(ctx context.Context, collection, id string, doc Doc) error {
	t.written = true
	return t.Tx.Put(ctx, collection, id, doc)
}

func (t *readBeforeWriteTx) Delete(ctx context.Context, collection, id string) error {
	t.written = true
	return t.Tx.Delete(ctx, collection, id)
}

// The withdrawal a rebind makes is a write of this transaction as much as a Put
// is: the key store's records are in the same document store the backend runs
// the transaction over, so a read after one is what the backend refuses.
func (t *readBeforeWriteTx) Revoke(ctx context.Context, revocations []Revocation) error {
	t.written = true
	return t.Tx.Revoke(ctx, revocations)
}

// refusingDocs is the fixture's store with the other failure these cases have to
// be able to make: a transaction that cannot commit. Everything else is the
// store it wraps, so a request runs exactly as far as the commit.
type refusingDocs struct {
	Docs
	refuses bool
}

func (d *refusingDocs) Update(ctx context.Context, fn func(context.Context, Tx) error) error {
	if d.refuses {
		return errors.New("keyservice: the store refused the write")
	}
	return d.Docs.Update(ctx, fn)
}

// refusingStore is the wrapper the intake of such a case reaches, whose flag is
// what makes the next commit fail.
func (f *bindingFixture) refusingStore(t *testing.T) *refusingDocs {
	t.Helper()
	store, ok := f.deps.Docs.(*refusingDocs)
	if !ok {
		t.Fatalf("the fixture's store is %T, want the wrapper this case built it with", f.deps.Docs)
	}
	return store
}

// racingDocs is the fixture's store with one write made by somebody else in the
// instant before a transaction runs: what a second request that got there first
// leaves behind, and the only way a case can put this service's own re-reads —
// the guards inside a transaction that decide on state another writer has
// already moved — against state that really moved.
//
// The write is made straight into the spy, which is a committed write of its
// own and not one of the transaction's: what the rule above is about is the
// transaction reading what it wrote itself.
type racingDocs struct {
	Docs
	spy    *docsSpy
	before func(*docsSpy)
}

func (d *racingDocs) Update(ctx context.Context, fn func(context.Context, Tx) error) error {
	before := d.before
	d.before = nil
	if before != nil {
		before(d.spy)
	}
	return d.Docs.Update(ctx, fn)
}

// racer wraps the fixture's store in one of those, and answers it so the case
// can set the write that lands before the next transaction.
func (f *bindingFixture) racer(t *testing.T) *racingDocs {
	t.Helper()
	store, ok := f.deps.Docs.(*racingDocs)
	if !ok {
		t.Fatalf("the fixture's store is %T, want the wrapper this case built it with", f.deps.Docs)
	}
	return store
}

// docsOnDocstore is the portal's seam over a real document store
// (ee/keyservice/docstore), which is the one thing no other case here has: the
// deployment's own two seams are two faces of one database — NewDocStore is
// handed the store the portal's documents live in — and the fixture's spy is a
// map that cannot tell a nested transaction from a committed one. A case that
// wires both seams over one store therefore puts this behind Deps.Docs and a
// NewDocStore over the same behind Deps.Store, and what it then reads back is
// what the deploy's backend would have left.
//
// The deploy's own adapter is docstore_docs.go (P1.7), and this stands beside
// it rather than in its place: what a case needs here is the one collection it
// refuses to write, and the adapter has no such hook. The cases over both are
// what hold the seam's contract.
type docsOnDocstore struct {
	db docstore.Store
	// refuse is the one collection this store's transactions fail to write,
	// which is how a case makes a transaction stop after it has staged
	// something: RunTransaction commits nothing of a function that returned an
	// error, so what is read back afterwards is what the transaction would have
	// left if its commit had been refused.
	refuse string
}

func (d *docsOnDocstore) Get(ctx context.Context, collection, id string) (Doc, error) {
	var held map[string]any
	found, err := d.db.Get(ctx, collection, id, &held)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrNoDocument
	}
	return Doc(held), nil
}

func (d *docsOnDocstore) Put(ctx context.Context, collection, id string, doc Doc) error {
	return d.db.Put(ctx, collection, id, doc)
}

func (d *docsOnDocstore) Query(ctx context.Context, collection string, where Where) ([]Doc, error) {
	q := docstore.Query{}
	if where.Field != "" {
		q.Where = []docstore.Where{{Field: where.Field, Equals: where.Value}}
	}
	found, err := d.db.Query(ctx, collection, q)
	if err != nil {
		return nil, err
	}
	docs := make([]Doc, 0, len(found))
	for _, doc := range found {
		docs = append(docs, Doc(doc.Fields))
	}
	return docs, nil
}

func (d *docsOnDocstore) Update(ctx context.Context, fn func(context.Context, Tx) error) error {
	return d.db.RunTransaction(ctx, func(ctx context.Context, tx docstore.Transaction) error {
		return fn(ctx, &docstoreTx{tx: tx, refuse: d.refuse})
	})
}

// docstoreTx is one of those transactions, and its removals are the document
// store's own: the store's transaction drops a document with the writes it
// commits, which is what the portal's seam means by a delete (docstore's
// docs.go).
type docstoreTx struct {
	tx     docstore.Transaction
	refuse string
}

func (t *docstoreTx) Get(ctx context.Context, collection, id string) (Doc, error) {
	var held map[string]any
	found, err := t.tx.Get(ctx, collection, id, &held)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrNoDocument
	}
	return Doc(held), nil
}

func (t *docstoreTx) Query(ctx context.Context, collection string, where Where) ([]Doc, error) {
	q := docstore.Query{}
	if where.Field != "" {
		q.Where = []docstore.Where{{Field: where.Field, Equals: where.Value}}
	}
	found, err := t.tx.Query(ctx, collection, q)
	if err != nil {
		return nil, err
	}
	docs := make([]Doc, 0, len(found))
	for _, doc := range found {
		docs = append(docs, Doc(doc.Fields))
	}
	return docs, nil
}

func (t *docstoreTx) Put(ctx context.Context, collection, id string, doc Doc) error {
	if t.refuse != "" && collection == t.refuse {
		return errStoreRefused
	}
	return t.tx.Put(ctx, collection, id, doc)
}

func (t *docstoreTx) Delete(ctx context.Context, collection, id string) error {
	if t.refuse != "" && collection == t.refuse {
		return errStoreRefused
	}
	return t.tx.Delete(ctx, collection, id)
}

// Revoke withdraws keys inside this transaction, which is what store.go's own
// Revoke does in a transaction of its own: the records go to the revocations
// collection of the store this transaction runs in, as the documents store.go's
// DocStore writes there, and a key already withdrawn keeps the reason and the
// instant of the entry that withdrew it.
//
// Every read comes before every write because the document store refuses one
// after the other (docstore's ErrReadAfterWrite), which is why the caller hands
// over the whole list at once rather than calling this once per key.
func (t *docstoreTx) Revoke(ctx context.Context, revocations []Revocation) error {
	withdrawn := make([]bool, len(revocations))
	for i, r := range revocations {
		if r.KeyID == "" {
			return ErrRevocationUnnamed
		}
		held, err := t.tx.Get(ctx, revocationCollection, r.KeyID, nil)
		if err != nil {
			return err
		}
		withdrawn[i] = held
	}
	for i, r := range revocations {
		if withdrawn[i] {
			continue
		}
		if err := t.tx.Put(ctx, revocationCollection, r.KeyID, r); err != nil {
			return err
		}
	}
	return nil
}

// errStoreRefused is the write a case's own store refuses, which stands for the
// commit the deployment's backend does not make.
var errStoreRefused = errors.New("keyservice: the store refused the write")

// racedStore is the key store with one read of its issuance list handed back
// after somebody else has bound: the list a rebind reads the keys it replaces
// from is the store's and not a document any transaction holds, so a competing
// bind that commits while that read is in flight is a key this request never
// decided on — which is the only thing that can put a winner's key in a loser's
// hand.
type racedStore struct {
	Store
	before func()
}

func (s *racedStore) Issuances(ctx context.Context) ([]Record, error) {
	if before := s.before; before != nil {
		s.before = nil
		before()
	}
	return s.Store.Issuances(ctx)
}

// racedIssuances is the wrapper the intake of such a case reaches, whose hook is
// what runs the competing bind.
func (f *bindingFixture) racedIssuances(t *testing.T) *racedStore {
	t.Helper()
	store, ok := f.deps.Store.(*racedStore)
	if !ok {
		t.Fatalf("the fixture's key store is %T, want the wrapper this case built it with", f.deps.Store)
	}
	return store
}

// TestARebindThatCannotCommitHasRevokedNothing: D2198's revocation is made with
// the binding it belongs to and not on the way into it. A rebind whose write
// fails therefore leaves the licence exactly where it was — the predecessor live
// at the installation it still serves, the successor withdrawn, and the retry
// that follows revoking the one and minting the other — rather than a licence
// moved with its window shut for thirty days, or an installation whose key was
// withdrawn for a rebind that never happened.
func TestARebindThatCannotCommitHasRevokedNothing(t *testing.T) {
	f := newBindingFixtureWith(t, func(f *fixture) {
		f.deps.Docs = &refusingDocs{Docs: f.deps.Docs}
	})
	store := f.refusingStore(t)
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
	if rec := f.bind(t, held.ID, exampleProject); rec.Code != http.StatusOK {
		t.Fatalf("the first binding answered %d: %s", rec.Code, rec.Body)
	}
	if len(f.mints.answers) != 1 {
		t.Fatalf("the first binding minted %d keys, want the one it bound", len(f.mints.answers))
	}
	replaced := f.mints.answers[0].Record.KeyID

	store.refuses = true
	if rec := f.bind(t, held.ID, secondProject); rec.Code != http.StatusInternalServerError {
		t.Fatalf("a rebind whose store refused the write answered %d (%s), want this service's own failure", rec.Code, rec.Body)
	}
	store.refuses = false

	revoked, found, err := f.store.Revocation(context.Background(), replaced)
	if err != nil || found {
		t.Errorf("the key the uncommitted rebind replaces is %+v (found=%v err=%v), want it live: the licence still runs it",
			revoked, found, err)
	}
	// The key the failed rebind minted is gone: nothing of an act that did not
	// happen is left live.
	if len(f.mints.answers) != 2 {
		t.Fatalf("the rebind minted %d keys in all, want the one it could not record", len(f.mints.answers))
	}
	unrecorded := f.mints.answers[1].Record.KeyID
	if withdrawn, found, err := f.store.Revocation(context.Background(), unrecorded); err != nil || !found || withdrawn.Reason != ReasonUnrecorded {
		t.Errorf("the key the uncommitted rebind minted is %+v (found=%v err=%v), want it withdrawn as unrecorded",
			withdrawn, found, err)
	}
	// And the licence is where it was: the old project, bound, with no window shut
	// against the retry.
	bound, _ := f.entitlement(held.ID)
	if bound.WorkspaceID == nil || *bound.WorkspaceID != workspace.ID {
		t.Errorf("the licence names the workspace %v after a rebind that could not commit", bound.WorkspaceID)
	}
	if bound.RebindableAt != nil {
		t.Errorf("the rebind that did not happen set the window at %s", *bound.RebindableAt)
	}
	if licensed, _ := f.workspace(workspace.ID); licensed.ProjectID == nil || *licensed.ProjectID != exampleProject {
		t.Errorf("the workspace is bound to %v after a rebind that could not commit, want %s", licensed.ProjectID, exampleProject)
	}

	if rec := f.bind(t, held.ID, secondProject); rec.Code != http.StatusOK {
		t.Fatalf("the retry answered %d: %s", rec.Code, rec.Body)
	}
	if licensed, _ := f.workspace(workspace.ID); licensed.ProjectID == nil || *licensed.ProjectID != secondProject {
		t.Errorf("the retry bound the workspace to %v, want %s", licensed.ProjectID, secondProject)
	}
	if withdrawn, found, err := f.store.Revocation(context.Background(), replaced); err != nil || !found || withdrawn.Reason != ReasonRebind {
		t.Errorf("the key the retry replaces is %+v (found=%v err=%v), want it withdrawn as a rebind", withdrawn, found, err)
	}
}

// TestARebindOverTheOneStoreBothSeamsShare: the same two halves over what the
// deploy wires — a portal and a key store that are one document store, since
// NewDocStore is handed the database the portal's documents live in. The
// withdrawal D2198 makes has to go through the transaction the binding commits
// in: the key store's own Revoke opens a transaction of its own, which the
// backend refuses from inside one (docstore's errNestedTransaction), and a
// deployment that reached the two seams over two stores would commit the
// withdrawal even when the binding it belongs to never landed.
//
// The fixture's own spy cannot tell either apart — it is a map that commits
// whatever a transaction staged — so both seams here are over a real document
// store, and what this case reads is what that store holds: the withdrawal out
// of the key store's own reader, the binding out of the portal's collection.
func TestARebindOverTheOneStoreBothSeamsShare(t *testing.T) {
	portal := &docsOnDocstore{}
	f := newBindingFixtureWith(t, func(f *fixture) {
		db := docstore.NewMemory()
		portal.db = db
		f.deps.Store = NewDocStore(db)
		f.deps.Docs = readBeforeWrite{Docs: portal}
	})
	ctx := context.Background()
	_, workspace, held := f.licensingWorkspace(t)
	if rec := f.bind(t, held.ID, exampleProject); rec.Code != http.StatusOK {
		t.Fatalf("binding answered %d: %s", rec.Code, rec.Body)
	}
	replaced := f.claimsOf(t, 0, exampleProject).KeyID

	// A rebind whose transaction cannot commit leaves nothing behind: the
	// withdrawal is staged in the transaction and goes with it, and the licence
	// is still where it was. The write is refused late — after the withdrawal —
	// so what is read back is the state the store keeps when the commit is not
	// made.
	portal.refuse = workspacesCollection
	if rec := f.bind(t, held.ID, secondProject); rec.Code != http.StatusInternalServerError {
		t.Fatalf("a rebind whose transaction could not commit answered %d (%s), want this service's own failure", rec.Code, rec.Body)
	}
	portal.refuse = ""
	if withdrawn, found, err := f.deps.Store.Revocation(ctx, replaced); err != nil || found {
		t.Errorf("the key of a rebind that did not commit is %+v (found=%v err=%v) with both seams over one store, want it live",
			withdrawn, found, err)
	}
	// The key that failed rebind minted is withdrawn all the same: the store's
	// own Revoke is a call outside any transaction (withdrawUnrecorded), and the
	// nesting this case is about is not what stops one.
	if len(f.mints.answers) != 2 {
		t.Fatalf("the rebind minted %d keys in all, want the one it could not record", len(f.mints.answers))
	}
	unrecorded := f.mints.answers[1].Record.KeyID
	if withdrawn, found, err := f.deps.Store.Revocation(ctx, unrecorded); err != nil || !found || withdrawn.Reason != ReasonUnrecorded {
		t.Errorf("the key the uncommitted rebind minted is %+v (found=%v err=%v), want it withdrawn as unrecorded",
			withdrawn, found, err)
	}

	// The retry commits, and everything it decided is in the one store: the
	// replaced key withdrawn as a rebind, the licence moved to the new project.
	if rec := f.bind(t, held.ID, secondProject); rec.Code != http.StatusOK {
		t.Fatalf("the retry answered %d: %s", rec.Code, rec.Body)
	}
	withdrawn, found, err := f.deps.Store.Revocation(ctx, replaced)
	if err != nil || !found {
		t.Fatalf("the key the rebind replaces is not in the key store both seams share (found=%v err=%v): %+v", found, err, withdrawn)
	}
	if withdrawn.Reason != ReasonRebind {
		t.Errorf("the replaced key's reason is %q, want %q", withdrawn.Reason, ReasonRebind)
	}
	if !withdrawn.At.Equal(f.now) {
		t.Errorf("the revocation is recorded at %s, want the rebind's instant %s", withdrawn.At, f.now)
	}
	fresh := f.claimsOf(t, 2, secondProject).KeyID
	if _, found, err := f.deps.Store.Revocation(ctx, fresh); err != nil || found {
		t.Errorf("the key the rebind minted is itself revoked (found=%v err=%v)", found, err)
	}
	bound, found, err := getRecord[WireWorkspace](ctx, portal, workspacesCollection, workspace.ID)
	if err != nil || !found {
		t.Fatalf("the workspace the binding wrote is not in the shared store (found=%v err=%v)", found, err)
	}
	if bound.ProjectID == nil || *bound.ProjectID != secondProject {
		t.Errorf("the workspace both seams share is bound to %v, want %s", bound.ProjectID, secondProject)
	}
}

// TestAnAirGappedLicenceIsBoundWithItsFileOrNotAtAll: D2206's file is written in
// the binding's own transaction, so an air-gapped licence is either bound with
// the file it is issued as or not bound at all. No act of this service writes one
// later, and a commit without it would be a customer whose page offers a download
// that answers nothing.
func TestAnAirGappedLicenceIsBoundWithItsFileOrNotAtAll(t *testing.T) {
	f := newBindingFixtureWith(t, func(f *fixture) {
		f.deps.Docs = &refusingDocs{Docs: f.deps.Docs}
	})
	store := f.refusingStore(t)
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

	store.refuses = true
	if rec := f.bind(t, held.ID, exampleProject); rec.Code != http.StatusInternalServerError {
		t.Fatalf("binding an air-gapped licence whose store refused the write answered %d (%s), want this service's own failure",
			rec.Code, rec.Body)
	}
	store.refuses = false

	if _, found := heldRecord[WireLicenceFile](f.fixture, licenceFilesCollection, held.ID); found {
		t.Errorf("a licence file is held for a binding that did not commit: the record and the file are one write (D2206)")
	}
	if after, _ := f.entitlement(held.ID); after.BoundAt != nil {
		t.Errorf("the licence is bound at %s although the binding did not commit", *after.BoundAt)
	}
	if licensed, _ := f.workspace(workspace.ID); licensed.ProjectID != nil {
		t.Errorf("the workspace names the project %v although the binding did not commit", *licensed.ProjectID)
	}

	// The binding that does commit carries its file with it.
	if rec := f.bind(t, held.ID, exampleProject); rec.Code != http.StatusOK {
		t.Fatalf("the binding after the failed one answered %d: %s", rec.Code, rec.Body)
	}
	if _, found := heldRecord[WireLicenceFile](f.fixture, licenceFilesCollection, held.ID); !found {
		t.Errorf("the committed air-gapped binding holds no licence file (D2206)")
	}
}

// TestASupersededLicenceCannotBeBound: a licence that is not active mints
// nothing (F25). The guard is the first thing this door reads after the
// caller's membership — before the project is checked and before the minter is
// reached — so a licence a purchase replaced is a refusal and not a key for a
// term the organisation is no longer on.
func TestASupersededLicenceCannotBeBound(t *testing.T) {
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
	f.advance(time.Hour)
	f.grant(t, entitlementGrant{
		At:             f.now,
		OrganisationID: org.ID,
		WorkspaceID:    &workspace.ID,
		Source:         entitlementSourcePurchase,
		Row:            PolicyTeamAnnual,
		Seats:          3,
		StartsAt:       f.now,
	})

	rec := f.bind(t, trial.ID, exampleProject)
	if rec.Code != http.StatusConflict {
		t.Fatalf("binding the superseded trial answered %d, want 409: %s", rec.Code, rec.Body)
	}
	if code := f.refused(rec); code != "licence_not_active" {
		t.Errorf("the refusal's code is %q, want licence_not_active", code)
	}
	if len(f.mints.answers) != 0 {
		t.Errorf("a refused binding minted %d keys", len(f.mints.answers))
	}
	if saw := f.probe.saw(); len(saw) != 0 {
		t.Errorf("the probe was asked about %v, and a licence that is not active is refused before the project is read", saw)
	}
	if bound, _ := f.entitlement(trial.ID); bound.BoundAt != nil {
		t.Errorf("the superseded licence records a binding at %s", bound.BoundAt)
	}
	if licensed, _ := f.workspace(workspace.ID); licensed.ProjectID != nil {
		t.Errorf("the workspace names the project %v, and nothing was bound", licensed.ProjectID)
	}
}

// TestALicenceSupersededBetweenTheReadAndTheWriteIsRefused: the guard above is
// re-read inside the transaction, because the state it decides on is the state
// another writer can move between this request's read and its write (D2204). The
// transaction refuses and the key the minter had signed for it is withdrawn:
// nothing of the binding that did not happen stays live.
func TestALicenceSupersededBetweenTheReadAndTheWriteIsRefused(t *testing.T) {
	f := newBindingFixtureWith(t, func(f *fixture) {
		f.deps.Docs = &racingDocs{Docs: f.deps.Docs, spy: f.docs}
	})
	racer := f.racer(t)
	_, workspace, held := f.licensingWorkspace(t)
	racer.before = func(*docsSpy) {
		superseded, found := f.entitlement(held.ID)
		if !found {
			t.Fatalf("the licence %s is gone from the store", held.ID)
		}
		superseded.Status = entitlementSuperseded
		f.seed(entitlementsCollection, superseded.ID, superseded)
	}

	rec := f.bind(t, held.ID, exampleProject)
	if rec.Code != http.StatusConflict {
		t.Fatalf("binding a licence superseded while the request was in flight answered %d, want 409: %s", rec.Code, rec.Body)
	}
	if code := f.refused(rec); code != "licence_not_active" {
		t.Errorf("the refusal's code is %q, want licence_not_active", code)
	}
	if len(f.mints.answers) != 1 {
		t.Fatalf("the refused binding minted %d keys, want the one its transaction refused", len(f.mints.answers))
	}
	unrecorded := f.mints.answers[0].Record.KeyID
	if withdrawn, found, err := f.store.Revocation(context.Background(), unrecorded); err != nil || !found || withdrawn.Reason != ReasonUnrecorded {
		t.Errorf("the key of the refused binding is %+v (found=%v err=%v), want it withdrawn as unrecorded", withdrawn, found, err)
	}
	if bound, _ := f.entitlement(held.ID); bound.BoundAt != nil {
		t.Errorf("the licence records a binding at %s although the transaction refused", bound.BoundAt)
	}
	if licensed, _ := f.workspace(workspace.ID); licensed.ProjectID != nil {
		t.Errorf("the workspace names the project %v although the transaction refused", licensed.ProjectID)
	}
}

// TestABindingThatRacedAnotherIsRefused: the binding that got there first wins.
// The second of two requests for one licence meets the re-read inside the
// transaction and is answered as a reload rather than as a binding that half
// happened: a second key for one licence is a key nothing would revoke.
func TestABindingThatRacedAnotherIsRefused(t *testing.T) {
	f := newBindingFixtureWith(t, func(f *fixture) {
		f.deps.Docs = &racingDocs{Docs: f.deps.Docs, spy: f.docs}
	})
	racer := f.racer(t)
	_, workspace, held := f.licensingWorkspace(t)
	racer.before = func(*docsSpy) {
		bound, found := f.entitlement(held.ID)
		if !found {
			t.Fatalf("the licence %s is gone from the store", held.ID)
		}
		at := f.now
		bound.BoundAt = &at
		f.seed(entitlementsCollection, bound.ID, bound)
	}

	rec := f.bind(t, held.ID, exampleProject)
	if rec.Code != http.StatusConflict {
		t.Fatalf("binding a licence bound while the request was in flight answered %d, want 409: %s", rec.Code, rec.Body)
	}
	if code := f.refused(rec); code != "already_bound" {
		t.Errorf("the refusal's code is %q, want already_bound", code)
	}
	if len(f.mints.answers) != 1 {
		t.Fatalf("the refused binding minted %d keys, want the one its transaction refused", len(f.mints.answers))
	}
	if withdrawn, found, err := f.store.Revocation(context.Background(), f.mints.answers[0].Record.KeyID); err != nil || !found || withdrawn.Reason != ReasonUnrecorded {
		t.Errorf("the key of the refused binding is %+v (found=%v err=%v), want it withdrawn as unrecorded", withdrawn, found, err)
	}
	// The binding that won is the one the store holds, and this request wrote none
	// of it: the workspace is still unbound and the licence still names it.
	licensed, _ := f.workspace(workspace.ID)
	if licensed.ProjectID != nil {
		t.Errorf("the workspace names the project %v, and this request bound nothing", licensed.ProjectID)
	}
	if after, _ := f.entitlement(held.ID); after.WorkspaceID == nil || *after.WorkspaceID != workspace.ID {
		t.Errorf("the licence names the workspace %v, want the one it was granted for", after.WorkspaceID)
	}
}

// TestABindingIntoAWorkspaceLicensedByAnotherGrant: a workspace holds one active
// licence (D2204), and binding is not the act that decides which one it is — the
// grant that supersedes the old one is. Two grants landing at once leave two
// active licences for one workspace with the pointer on the last of them, which
// is the state this guard answers: the licence the workspace is not pointing at
// is refused rather than bound over the one it holds.
func TestABindingIntoAWorkspaceLicensedByAnotherGrant(t *testing.T) {
	f := newBindingFixture(t)
	org := f.seededOrganisation("org-dana", "Dana Ltd", orgTypeCustomer, danaUID, danaEmail, roleOwner)
	workspace := f.seedWorkspace("ws-1", org.ID, "Dana's portal")
	winner := f.grant(t, entitlementGrant{
		At:             f.now,
		OrganisationID: org.ID,
		WorkspaceID:    &workspace.ID,
		Source:         entitlementSourcePurchase,
		Row:            PolicyTeamAnnual,
		Seats:          3,
		StartsAt:       f.now,
	})
	runnerUp := WireEntitlement{
		ID:             "ent-runner-up",
		OrganisationID: org.ID,
		WorkspaceID:    &workspace.ID,
		Source:         entitlementSourcePurchase,
		Row:            winner.Row,
		Tier:           winner.Tier,
		Kind:           winner.Kind,
		Seats:          winner.Seats,
		StartsAt:       f.now,
		EndsAt:         winner.EndsAt,
		Status:         entitlementActive,
		CreatedAt:      f.now,
	}
	f.seed(entitlementsCollection, runnerUp.ID, runnerUp)

	rec := f.bind(t, runnerUp.ID, exampleProject)
	if rec.Code != http.StatusConflict {
		t.Fatalf("binding a licence another one licenses the workspace with answered %d, want 409: %s", rec.Code, rec.Body)
	}
	if code := f.refused(rec); code != "workspace_licensed" {
		t.Errorf("the refusal's code is %q, want workspace_licensed", code)
	}
	if len(f.mints.answers) != 0 {
		t.Errorf("a refused binding minted %d keys", len(f.mints.answers))
	}
	if saw := f.probe.saw(); len(saw) != 0 {
		t.Errorf("the probe was asked about %v, and the workspace is refused before the project is read", saw)
	}
	if licensed, _ := f.workspace(workspace.ID); licensed.ProjectID != nil {
		t.Errorf("the workspace names the project %v, and nothing was bound", licensed.ProjectID)
	}
	if licensed, _ := f.workspace(workspace.ID); licensed.EntitlementID == nil || *licensed.EntitlementID != winner.ID {
		t.Errorf("the workspace is licensed by %v, want the grant it points at, %s", licensed.EntitlementID, winner.ID)
	}
}

// TestARebindThatRacesAnotherDoesNotRevokeTheWinnersKey: two rebinds of one
// licence at once both read the same live key, and the one that commits first
// mints the key the licence is on from then. What the other withdraws is the key
// it read (D2198) and never the winner's: a withdrawal of a key it never saw
// would leave the licence bound to the winner's project with every key it has
// ever minted revoked and the next rebind thirty days away.
func TestARebindThatRacesAnotherDoesNotRevokeTheWinnersKey(t *testing.T) {
	f := newBindingFixture(t)
	_, workspace, held := f.licensingWorkspace(t)
	if rec := f.bind(t, held.ID, exampleProject); rec.Code != http.StatusOK {
		t.Fatalf("binding answered %d: %s", rec.Code, rec.Body)
	}
	replaced := f.mints.answers[0].Record.KeyID

	// The competing rebind commits while this request waits on the project's
	// database: it moves the licence to its own project and mints the key that is
	// live from then on.
	f.probe.during = func() {
		if _, err := f.svc.bindEntitlement(context.Background(), f.signIn.caller, held.ID, secondProject); err != nil {
			t.Errorf("the rebind this case runs during the probe failed: %v", err)
		}
	}

	rec := f.bind(t, held.ID, exampleProject)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("the rebind that lost the race answered %d, want 429: %s", rec.Code, rec.Body)
	}
	if code := f.refused(rec); code != "rebind_too_soon" {
		t.Errorf("the refusal's code is %q, want rebind_too_soon", code)
	}
	if len(f.mints.answers) != 3 {
		t.Fatalf("the binding and the two rebinds minted %d keys, want one each", len(f.mints.answers))
	}
	winner := f.mints.answers[1].Record.KeyID
	if withdrawn, found, err := f.store.Revocation(context.Background(), winner); err != nil || found {
		t.Errorf("the key the rebind that won minted is %+v (found=%v err=%v), want it live: the loser revoked a key it never read",
			withdrawn, found, err)
	}
	loser := f.mints.answers[2].Record.KeyID
	if withdrawn, found, err := f.store.Revocation(context.Background(), loser); err != nil || !found || withdrawn.Reason != ReasonUnrecorded {
		t.Errorf("the key the losing rebind minted is %+v (found=%v err=%v), want it withdrawn as unrecorded", withdrawn, found, err)
	}
	if withdrawn, found, err := f.store.Revocation(context.Background(), replaced); err != nil || !found || withdrawn.Reason != ReasonRebind {
		t.Errorf("the key both rebinds replace is %+v (found=%v err=%v), want it withdrawn as a rebind", withdrawn, found, err)
	}
	// And the licence is where the winner put it, with the key it minted there.
	if licensed, _ := f.workspace(workspace.ID); licensed.ProjectID == nil || *licensed.ProjectID != secondProject {
		t.Errorf("the workspace is bound to %v, want the winning rebind's project %s", licensed.ProjectID, secondProject)
	}
}

// TestARebindThatReadsTheKeysItReplacesAfterACompetingBind: what a rebind
// withdraws is the key it read (D2198), and the list it reads is the store's
// issuance list and not a document any transaction holds. A competing rebind
// that commits while that read is in flight is a winner's key in the loser's
// hand, and the loser may not withdraw it: the guards the transaction re-reads
// decide whether anything is revoked at all, so a rebind that has lost the race
// withdraws its own key and leaves the winner's live — and the licence on the
// winner's project with a key that works there.
func TestARebindThatReadsTheKeysItReplacesAfterACompetingBind(t *testing.T) {
	f := newBindingFixtureWith(t, func(f *fixture) {
		f.deps.Store = &racedStore{Store: f.deps.Store}
	})
	store := f.racedIssuances(t)
	_, workspace, held := f.licensingWorkspace(t)
	if rec := f.bind(t, held.ID, exampleProject); rec.Code != http.StatusOK {
		t.Fatalf("binding answered %d: %s", rec.Code, rec.Body)
	}
	replaced := f.mints.answers[0].Record.KeyID

	// The competing rebind commits in the instant this request reads the keys it
	// replaces: the key the winner mints is on the list this request holds, and
	// the list was read before anything decided on it.
	store.before = func() {
		if _, err := f.svc.bindEntitlement(context.Background(), f.signIn.caller, held.ID, secondProject); err != nil {
			t.Errorf("the rebind this case runs while the keys are being read failed: %v", err)
		}
	}

	rec := f.bind(t, held.ID, exampleProject)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("the rebind that lost the race answered %d, want 429: %s", rec.Code, rec.Body)
	}
	if code := f.refused(rec); code != "rebind_too_soon" {
		t.Errorf("the refusal's code is %q, want rebind_too_soon", code)
	}
	if len(f.mints.answers) != 3 {
		t.Fatalf("the binding and the two rebinds minted %d keys, want one each", len(f.mints.answers))
	}
	winner := f.mints.answers[1].Record.KeyID
	if withdrawn, found, err := f.store.Revocation(context.Background(), winner); err != nil || found {
		t.Errorf("the key the rebind that won minted is %+v (found=%v err=%v), want it live: the loser revoked it although its own transaction refused",
			withdrawn, found, err)
	}
	loser := f.mints.answers[2].Record.KeyID
	if withdrawn, found, err := f.store.Revocation(context.Background(), loser); err != nil || !found || withdrawn.Reason != ReasonUnrecorded {
		t.Errorf("the key the losing rebind minted is %+v (found=%v err=%v), want it withdrawn as unrecorded", withdrawn, found, err)
	}
	if withdrawn, found, err := f.store.Revocation(context.Background(), replaced); err != nil || !found || withdrawn.Reason != ReasonRebind {
		t.Errorf("the key both rebinds replace is %+v (found=%v err=%v), want it withdrawn as a rebind", withdrawn, found, err)
	}
	// And the licence is where the winner put it, with the key it minted there.
	if licensed, _ := f.workspace(workspace.ID); licensed.ProjectID == nil || *licensed.ProjectID != secondProject {
		t.Errorf("the workspace is bound to %v, want the winning rebind's project %s", licensed.ProjectID, secondProject)
	}
}

// TestTwoBindsOfOneProjectDoNotBothWin: one Firebase project is one installation
// and runs one key (D2204), and the check that refuses the second workspace to
// name it is a question about the collection the binding is written into, which
// the deploy's store makes part of a transaction's own state only when it is
// asked inside one. A bind that asked before it wrote is a check two requests
// pass together, and what is left of that pair is two workspaces on one
// installation with one of the two keys verifying nowhere.
func TestTwoBindsOfOneProjectDoNotBothWin(t *testing.T) {
	f := newBindingFixture(t)
	_, firstWorkspace, first := f.licensingWorkspace(t)
	other := f.seededOrganisation("org-eve", "Eve Ltd", orgTypeCustomer, danaUID, danaEmail, roleOwner)
	otherWorkspace := f.seedWorkspace("ws-2", other.ID, "Eve's portal")
	second := f.grant(t, entitlementGrant{
		At:             f.now,
		OrganisationID: other.ID,
		WorkspaceID:    &otherWorkspace.ID,
		Source:         entitlementSourcePurchase,
		Row:            PolicyTeamAnnual,
		Seats:          3,
		StartsAt:       f.now,
	})

	// The competing bind commits while this request is between that check and the
	// transaction that writes the binding: the probe is the slow step the two
	// share.
	f.probe.during = func() {
		if rec := f.bind(t, second.ID, exampleProject); rec.Code != http.StatusOK {
			t.Errorf("the bind this case runs during the probe answered %d: %s", rec.Code, rec.Body)
		}
	}

	rec := f.bind(t, first.ID, exampleProject)
	if rec.Code != http.StatusConflict {
		t.Fatalf("the bind that lost the race answered %d, want 409: %s", rec.Code, rec.Body)
	}
	if code := f.refused(rec); code != "project_id_bound" {
		t.Errorf("the refusal's code is %q, want project_id_bound", code)
	}
	if len(f.mints.answers) != 2 {
		t.Fatalf("the two binds minted %d keys, want one each", len(f.mints.answers))
	}
	winner := f.mints.answers[0].Record.KeyID
	if withdrawn, found, err := f.store.Revocation(context.Background(), winner); err != nil || found {
		t.Errorf("the key the bind that won minted is %+v (found=%v err=%v), want it live", withdrawn, found, err)
	}
	loser := f.mints.answers[1].Record.KeyID
	if withdrawn, found, err := f.store.Revocation(context.Background(), loser); err != nil || !found || withdrawn.Reason != ReasonUnrecorded {
		t.Errorf("the key the losing bind minted is %+v (found=%v err=%v), want it withdrawn as unrecorded", withdrawn, found, err)
	}
	// One workspace names the project, and it is the winner's: the losing bind
	// wrote nothing, and the licence it holds is still unbound.
	bound, err := recordsWhere[WireWorkspace](context.Background(), f.docs, workspacesCollection,
		Where{Field: "project_id", Value: exampleProject})
	if err != nil {
		t.Fatalf("reading the workspaces bound to %s: %v", exampleProject, err)
	}
	if len(bound) != 1 || bound[0].ID != otherWorkspace.ID {
		t.Errorf("the project %s is named by %d workspace(s), %v, want the one the winning bind wrote", exampleProject, len(bound), bound)
	}
	if licensed, _ := f.workspace(firstWorkspace.ID); licensed.ProjectID != nil {
		t.Errorf("the workspace of the losing bind names the project %v, and its bind wrote nothing", *licensed.ProjectID)
	}
	if after, _ := f.entitlement(first.ID); after.BoundAt != nil {
		t.Errorf("the licence of the losing bind records a binding at %s", *after.BoundAt)
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
