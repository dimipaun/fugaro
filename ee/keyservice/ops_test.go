// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The operations API's own cases (portal plan P5.1; D2187, D2188, D2190, D2204,
// D2207, D454, D456, F5, F9, F28, F29, F40).
//
// What these cases hold to is the pair of rules the area is: who may act
// (D2187's roles and its five minutes), and that every act is written down
// (D456, reads included). Everything else here is one plan row read at the
// route: D454's rate with its queue, D2204's transfer, F29's withdrawal and
// F40's lists.

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/licence"
)

// The fixture's own operations records: the operations organisation's id, one
// customer organisation, the workspace its licence is bound to, a second
// workspace to mint into, a third a project can be transferred onto, the key
// that licence minted, and the handover and parked event these cases decide.
const (
	opsOrgID      = "org_operations"
	opsCustomerID = "org-dana"
	opsBoundWS    = "ws-bound"
	opsMintWS     = "ws-mint"
	opsFreeWS     = "ws-free"
	opsProject    = "example-portal"
	opsOtherProj  = "another-portal"
	opsKeyID      = "key-operations-1"
	opsHandoverID = "key-operations-2"
	opsEventID    = "evt-parked-1"
)

// operationsOrg seeds the operations organisation with the caller in the role
// named, which is the state every case here starts from: the organisation P2.2
// seeds and no route of this area creates.
func (f *fixture) operationsOrg(role string) WireOrganisation {
	f.t.Helper()
	org := f.seededOrganisation(opsOrgID, "Operations", orgTypeOperations, opsUID, opsEmail, role)
	f.actsAs(opsUID, opsEmail, signInGoogle)
	return org
}

// operationsGround is one customer organisation with the records the operations
// screens read: a licence bound to a workspace, a workspace with nothing in it,
// a workspace a project can be transferred onto, the key that licence minted, a
// parked event and an open handover. It is seeded rather than driven through the
// routes, because what these cases are about is what operations does with the
// state and not how the state got there.
func (f *fixture) operationsGround() (WireOrganisation, WireEntitlement) {
	f.t.Helper()
	org := f.seededOrganisation(opsCustomerID, "Dana Ltd", orgTypeCustomer, danaUID, danaEmail, roleOwner)
	project := opsProject
	f.seed(workspacesCollection, opsBoundWS, WireWorkspace{
		ID: opsBoundWS, OrganisationID: org.ID, Name: "Dana's bound", ProjectID: &project, CreatedAt: f.now,
	})
	f.seed(workspacesCollection, opsMintWS, WireWorkspace{
		ID: opsMintWS, OrganisationID: org.ID, Name: "Dana's second", CreatedAt: f.now,
	})
	f.seed(workspacesCollection, opsFreeWS, WireWorkspace{
		ID: opsFreeWS, OrganisationID: org.ID, Name: "Dana's spare", CreatedAt: f.now,
	})
	boundWorkspace := opsBoundWS
	granted, err := f.svc.grantEntitlement(context.Background(), entitlementGrant{
		At:             f.now,
		OrganisationID: org.ID,
		WorkspaceID:    &boundWorkspace,
		Source:         entitlementSourcePurchase,
		Row:            PolicyTeamAnnual,
		Seats:          3,
		StartsAt:       f.now,
	})
	if err != nil {
		f.t.Fatalf("granting the licence these cases read: %v", err)
	}
	// The workspace names the licence it is bound to, which is what makes a
	// transfer the act that moves the project *and* withdraws the key the
	// binding minted.
	bound, _ := heldRecord[WireWorkspace](f, workspacesCollection, opsBoundWS)
	bound.EntitlementID = &granted.ID
	bound.BoundAt = &f.now
	f.seed(workspacesCollection, opsBoundWS, bound)

	if err := f.store.SaveIssuance(context.Background(), Record{
		Source:        bindingPrefix + granted.ID + ":seed",
		KeyID:         opsKeyID,
		EntitlementID: granted.ID,
		Plan:          PolicyTeamAnnual,
		Tier:          granted.Tier,
		Seats:         granted.Seats,
		Organisation:  org.ID,
		Issued:        f.now,
		Expires:       granted.EndsAt,
	}); err != nil {
		f.t.Fatalf("recording the key the licence minted: %v", err)
	}
	if err := f.store.SaveKeyState(context.Background(), KeyState{
		KeyID: opsKeyID, WorkspaceID: opsProject, LastSeen: f.now,
	}); err != nil {
		f.t.Fatalf("recording the key's state: %v", err)
	}
	f.seed(paddleEventsCollection, opsEventID, WireLedgerEvent{
		ID: opsEventID, ReceivedAt: f.now, Signature: "sig", Notification: "transaction.completed",
		Body: `{"event_id":"` + opsEventID + `"}`, State: ledgerParked,
	})
	if err := f.store.SaveHandover(context.Background(), Handover{
		KeyID:      opsHandoverID,
		SecretHash: strings.Repeat("ab", 32),
		Sealed:     []byte(sealerMarker + "successor"),
		Until:      f.now.Add(time.Hour),
	}, f.now); err != nil {
		f.t.Fatalf("opening the handover these cases cut: %v", err)
	}
	return org, granted
}

// mintBody is one operations mint as the SPA sends it: every field of the shape,
// because the wire refuses a body that leaves one out.
func (f *fixture) mintBody(workspaceID, row string, seats int, ends time.Time) string {
	f.t.Helper()
	return `{"workspace_id":` + jsonString(f.t, workspaceID) +
		`,"row":` + jsonString(f.t, row) +
		`,"seats":` + strconv.Itoa(seats) +
		`,"ends_at":` + jsonString(f.t, ends.UTC().Format(time.RFC3339)) +
		`,"check_interval":null}`
}

// actionBody is one term change as the SPA sends it: the four actions share one
// request shape, and a field an action does not read is null.
func actionBody(t *testing.T, entitlementID, action, seats, ends, reason string) string {
	t.Helper()
	return `{"entitlement_id":` + jsonString(t, entitlementID) +
		`,"action":` + jsonString(t, action) +
		`,"seats":` + seats +
		`,"ends_at":` + ends +
		`,"reason":` + reason + `}`
}

// transferBody is one project transfer as the SPA sends it.
func transferBody(t *testing.T, projectID, workspaceID, reason string) string {
	t.Helper()
	return `{"project_id":` + jsonString(t, projectID) +
		`,"workspace_id":` + jsonString(t, workspaceID) +
		`,"reason":` + reason + `}`
}

// cutPath is the handover-cut route with a key id in it, spelled as the SPA
// builds it (`screens/ops/api.ts`).
func cutPath(keyID string) string {
	return strings.Replace(apiOpsHandoverCutPath, "{id}", keyID, 1)
}

// entitlementsOfOrganisation is every licence the store holds for one
// organisation.
func (f *fixture) entitlementsOfOrganisation(organisationID string) []WireEntitlement {
	f.t.Helper()
	held, err := recordsWhere[WireEntitlement](context.Background(), f.docs, entitlementsCollection,
		Where{Field: "organisation_id", Value: organisationID})
	if err != nil {
		f.t.Fatalf("reading the licences of %s: %v", organisationID, err)
	}
	return held
}

// operationsQueuedRecords is the queue D454's rate holds requests in.
func (f *fixture) operationsQueuedRecords() []operationsQueued {
	f.t.Helper()
	held, err := recordsWhere[operationsQueued](context.Background(), f.docs, operationsQueueCollection, Where{})
	if err != nil {
		f.t.Fatalf("reading the operations queue: %v", err)
	}
	return held
}

// TestOperationsMemberCannotMint: D2187 gives the operations organisation's
// members the reads and no act. Every change of this area is refused for a
// member, and the refusal is the role's own code rather than a missing record:
// the requests are well-formed and the state is there, and what refuses them is
// who is asking.
func TestOperationsMemberCannotMint(t *testing.T) {
	f := newFixture(t)
	f.operationsOrg(roleMember)
	_, granted := f.operationsGround()
	ends := jsonString(t, f.now.Add(30*24*time.Hour).UTC().Format(time.RFC3339))

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "a mint", method: http.MethodPost, path: apiOpsMintsPath,
			body: f.mintBody(opsMintWS, PolicyTeamAnnual, 3, f.now.Add(30*24*time.Hour))},
		{name: "an action on a licence", method: http.MethodPost, path: apiOpsEntitlementActionsPath,
			body: actionBody(t, granted.ID, "extend", "null", ends, "null")},
		{name: "a project transfer", method: http.MethodPost, path: apiOpsProjectTransfersPath,
			body: transferBody(t, opsProject, opsFreeWS, "null")},
		{name: "an event resolution", method: http.MethodPost, path: apiOpsEventResolutionsPath,
			body: `{"id":` + jsonString(t, opsEventID) + `,"decision":"dismiss","entitlement_id":null}`},
		{name: "saving the lists", method: http.MethodPut, path: apiOpsListsPath,
			body: `{"email_domains":["fugaro.example"],"microsoft_directories":[]}`},
		{name: "cutting a handover short", method: http.MethodPost, path: cutPath(opsHandoverID), body: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.apiAsCaller(tc.method, tc.path, tc.body)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s as a member of the operations organisation answered %d (%s), want 403 (D2187)", tc.name, rec.Code, rec.Body)
			}
			if code := refusal(f, rec); code != "not_permitted" {
				t.Errorf("the refusal carries the code %q, want not_permitted", code)
			}
		})
	}

	// Nothing of the six happened: no key was minted, the licence is as it was,
	// the project is where it was, the event is still parked, the handover is
	// still open and the lists were not written.
	if minted := f.mintedKeys(); len(minted) != 0 {
		t.Errorf("a member's refused mint minted %v", minted)
	}
	if now, _ := heldRecord[WireEntitlement](f, entitlementsCollection, granted.ID); !now.EndsAt.Equal(granted.EndsAt) || now.Status != granted.Status {
		t.Errorf("the licence is %+v after the refusals, want it unchanged", now)
	}
	if source, _ := heldRecord[WireWorkspace](f, workspacesCollection, opsBoundWS); source.ProjectID == nil || *source.ProjectID != opsProject {
		t.Errorf("the project is %v after the refusals, want it still on %s", source.ProjectID, opsBoundWS)
	}
	if event, _ := heldRecord[WireLedgerEvent](f, paddleEventsCollection, opsEventID); event.State != ledgerParked {
		t.Errorf("the event is %q after the refusals, want it parked", event.State)
	}
	if _, open, _ := f.store.Handover(context.Background(), opsHandoverID, f.now); !open {
		t.Error("the handover was closed by a member's refused cut")
	}
	if _, written := heldRecord[WireOpsLists](f, f.cfg.TenantsCollection, opsOrgID); written {
		t.Error("a member's refused save wrote the lists")
	}

	// The reads are what a member has, and the membership read is where the SPA
	// learns which of the three roles this caller is.
	rec := f.apiAsCaller(http.MethodGet, apiOpsMembershipPath, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("a member reading the operations membership answered %d (%s), want it", rec.Code, rec.Body)
	}
	if role := readAnswer[WireMembership](f, rec).Role; role != roleMember {
		t.Errorf("the membership read answers the role %q, want %q", role, roleMember)
	}
	rec = f.apiAsCaller(http.MethodGet, apiOpsOrganisationsPath+"?q=Dana", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("a member's lookup answered %d (%s), want it: reading across organisations is what the membership is for", rec.Code, rec.Body)
	}
	if found := readAnswer[[]WireOrganisation](f, rec); len(found) != 1 || found[0].ID != opsCustomerID {
		t.Errorf("the lookup answered %+v, want the customer organisation alone", found)
	}
}

// TestMintNeedsAFreshSignIn: a change needs an `auth_time` from the last five
// minutes (D2187). The window is read from the token's own instant, so a session
// that has been open all afternoon is refused where one opened a minute ago is
// not — and the reads are not held by it, because a member reading the screens
// all afternoon is a member reading.
func TestMintNeedsAFreshSignIn(t *testing.T) {
	f := newFixture(t)
	f.operationsOrg(roleOwner)
	f.operationsGround()
	body := f.mintBody(opsMintWS, PolicyTeamAnnual, 3, f.now.Add(30*24*time.Hour))

	for _, tc := range []struct {
		name  string
		age   time.Duration
		fresh bool
	}{
		{name: "three hours old", age: 3 * time.Hour, fresh: false},
		{name: "six minutes old", age: 6 * time.Minute, fresh: false},
		{name: "four minutes old", age: 4 * time.Minute, fresh: true},
		{name: "just signed in", age: 0, fresh: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(f.entitlementsOfOrganisation(opsCustomerID))
			f.signIn.caller.AuthTime = f.now.Add(-tc.age)
			rec := f.apiAsCaller(http.MethodPost, apiOpsMintsPath, body)
			after := len(f.entitlementsOfOrganisation(opsCustomerID))
			if tc.fresh {
				if rec.Code != http.StatusCreated {
					t.Fatalf("a mint on a sign-in %s answered %d (%s), want it minted", tc.name, rec.Code, rec.Body)
				}
				if after != before+1 {
					t.Errorf("the mint wrote %d licence(s), want 1", after-before)
				}
				return
			}
			if rec.Code != http.StatusForbidden {
				t.Fatalf("a mint on a sign-in %s answered %d (%s), want 403 (D2187)", tc.name, rec.Code, rec.Body)
			}
			if code := refusal(f, rec); code != "stale_sign_in" {
				t.Errorf("the refusal carries the code %q, want stale_sign_in", code)
			}
			if after != before {
				t.Errorf("the refused mint wrote %d licence(s), want none", after-before)
			}
			// The sentence has to say what to do about it: a refusal an
			// operator cannot act on is one they work around.
			if message := readAnswer[apiError](f, rec).Error.Message; !strings.Contains(message, "five minutes") {
				t.Errorf("the refusal says %q, want it to name the window", message)
			}
		})
	}

	// A read on the same stale sign-in answers: D2187's window is a change's.
	f.signIn.caller.AuthTime = f.now.Add(-3 * time.Hour)
	if rec := f.apiAsCaller(http.MethodGet, apiOpsMembershipPath, ""); rec.Code != http.StatusOK {
		t.Errorf("a read on a sign-in three hours old answered %d (%s), want it: the window holds changes and not reads", rec.Code, rec.Body)
	}
}

// TestOperationsMintsQueueOverTheRate: D454's cap is per tier and per window,
// and a request over it is **queued rather than lost** — P5.1 carries the rule
// from `admin.go`'s page, and the queue is this API's own because a mint at this
// door has no application behind it to wait in. The term is served from the
// instant it is served and never from the instant it was asked for, so the queue
// holds the request and not a licence.
func TestOperationsMintsQueueOverTheRate(t *testing.T) {
	f := newFixtureWith(t, Production, func(f *fixture) {
		f.cfg.MintCaps = map[string]Limits{
			licence.TierCommunity:  {Requests: 2, Window: time.Hour},
			licence.TierTeam:       {Requests: 2, Window: time.Hour},
			licence.TierEnterprise: {Requests: 2, Window: time.Hour},
		}
	})
	f.operationsOrg(roleOwner)
	f.operationsGround()
	body := f.mintBody(opsMintWS, PolicyTeamAnnual, 3, f.now.Add(30*24*time.Hour))

	// The window's two grants, and then the third request is held back.
	var granted []WireEntitlement
	for i := 0; i < 2; i++ {
		rec := f.apiAsCaller(http.MethodPost, apiOpsMintsPath, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("mint %d of the window answered %d (%s), want it minted", i+1, rec.Code, rec.Body)
		}
		granted = append(granted, readAnswer[WireEntitlement](f, rec))
	}
	rec := f.apiAsCaller(http.MethodPost, apiOpsMintsPath, body)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("the mint over the cap answered %d (%s), want the cap's refusal", rec.Code, rec.Body)
	}
	if code := refusal(f, rec); code != "over_cap" {
		t.Errorf("the refusal carries the code %q, want over_cap", code)
	}
	if message := readAnswer[apiError](f, rec).Error.Message; message != refuseOverCap {
		t.Errorf("the refusal says %q, want the sentence that says the request stays in the queue (%q)", message, refuseOverCap)
	}

	// The request is held and no licence was written for it: a licence written
	// here would be a term running while nobody holds it.
	queued := f.operationsQueuedRecords()
	if len(queued) != 1 {
		t.Fatalf("the queue holds %d request(s), want the one over the cap: %+v", len(queued), queued)
	}
	if queued[0].WorkspaceID != opsMintWS || queued[0].Row != PolicyTeamAnnual || queued[0].Seats != 3 {
		t.Errorf("the queue holds %+v, want the request that was refused", queued[0])
	}
	if queued[0].ActorUID != opsUID {
		t.Errorf("the queued request names the actor %q, want %q: the licence is written for whoever asked", queued[0].ActorUID, opsUID)
	}
	if held := f.entitlementsOfOrganisation(opsCustomerID); len(held) != 3 {
		t.Errorf("the organisation holds %d licence(s), want the seeded one and the window's two", len(held))
	}

	// The window opens and the queued request is served: the next mint drains
	// the queue before it counts itself, so the request that was held back lands
	// under its own id and the window's own grant is still the new request's.
	f.advance(time.Hour)
	// The sign-in is refreshed with the clock: the window a change needs is
	// five minutes of the caller's own session, and this case is about the rate
	// and not about the two of them together.
	f.actsAs(opsUID, opsEmail, signInGoogle)
	rec = f.apiAsCaller(http.MethodPost, apiOpsMintsPath, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("the mint after the window answered %d (%s), want it minted and the queue drained", rec.Code, rec.Body)
	}
	if held := f.operationsQueuedRecords(); len(held) != 0 {
		t.Errorf("the queue holds %+v after the window opened, want it drained", held)
	}
	licences := f.entitlementsOfOrganisation(opsCustomerID)
	if len(licences) != 5 {
		t.Fatalf("the organisation holds %d licence(s), want the seeded one and the four operations granted", len(licences))
	}
	byID := map[string]WireEntitlement{}
	for _, one := range licences {
		byID[one.ID] = one
	}
	served, held := byID[queued[0].ID]
	if !held {
		t.Fatalf("the drained request wrote no licence under its own id %s: %+v", queued[0].ID, licences)
	}
	if !served.StartsAt.After(granted[0].StartsAt) {
		t.Errorf("the served licence starts at %s, want it served from the instant the window opened (%s) and not the instant it was asked for", served.StartsAt, granted[0].StartsAt)
	}
	if served.Source != entitlementSourceOperations || served.Seats != 3 {
		t.Errorf("the served licence is %+v, want the queued request's own grant", served)
	}
}

// TestOperationsQueueDrainsAtTheRate: D454's cap counts the **grants** of the
// window and never the requests waiting in the queue, which is the difference
// between a queue and a wedge.
//
// The state this pins is the one a burst leaves behind: the window takes one
// request, so the queue holds one. A serve pass that counted the waiting record
// as spent room would find no room in the window it is there to fill, serve
// nothing, drain nothing, and leave every later mint refused and queued — the
// tier stops minting for good. What the pass has to do is grant the record it is
// holding, whatever is still waiting behind it.
func TestOperationsQueueDrainsAtTheRate(t *testing.T) {
	f := newFixtureWith(t, Production, func(f *fixture) {
		f.cfg.MintCaps = map[string]Limits{
			licence.TierCommunity:  {Requests: 1, Window: time.Hour},
			licence.TierTeam:       {Requests: 1, Window: time.Hour},
			licence.TierEnterprise: {Requests: 1, Window: time.Hour},
		}
	})
	f.operationsOrg(roleOwner)
	f.operationsGround()
	body := f.mintBody(opsMintWS, PolicyTeamAnnual, 3, f.now.Add(30*24*time.Hour))

	rec := f.apiAsCaller(http.MethodPost, apiOpsMintsPath, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("the window's own grant answered %d (%s), want it minted", rec.Code, rec.Body)
	}
	rec = f.apiAsCaller(http.MethodPost, apiOpsMintsPath, body)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("the mint over the cap answered %d (%s), want it held back", rec.Code, rec.Body)
	}
	held := f.operationsQueuedRecords()
	if len(held) != 1 {
		t.Fatalf("the queue holds %d request(s), want the one the window could not take: %+v", len(held), held)
	}
	requested := held[0]

	// The window opens. The next mint drains the queue before it counts itself,
	// and with the window one request wide the record it drains is the whole of
	// it: the request that was held back becomes the licence, and the request
	// this pass was called for is the one that now waits.
	f.advance(time.Hour)
	f.actsAs(opsUID, opsEmail, signInGoogle)
	rec = f.apiAsCaller(http.MethodPost, apiOpsMintsPath, body)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("the mint after the window answered %d (%s), want the drained request served and the new one held back", rec.Code, rec.Body)
	}
	licences := f.entitlementsOfOrganisation(opsCustomerID)
	byID := map[string]WireEntitlement{}
	for _, one := range licences {
		byID[one.ID] = one
	}
	served, found := byID[requested.ID]
	if !found {
		t.Fatalf("the record the queue was holding was never served: the store holds %+v, want a licence under %s", licences, requested.ID)
	}
	if served.Seats != 3 || served.WorkspaceID == nil || *served.WorkspaceID != opsMintWS {
		t.Errorf("the served licence is %+v, want the request the queue was holding", served)
	}
	if !served.StartsAt.Equal(f.now) {
		t.Errorf("the served licence starts at %s, want the instant it was served (%s)", served.StartsAt, f.now)
	}
	now := f.operationsQueuedRecords()
	if len(now) != 1 || now[0].ID == requested.ID {
		t.Fatalf("the queue holds %+v, want the request this pass could not take and not the one it served", now)
	}
	if now[0].WorkspaceID != opsMintWS || now[0].Row != PolicyTeamAnnual {
		t.Errorf("the queue holds %+v, want the request that was refused", now[0])
	}

	// And it drains again, so a tier under a one-a-window cap keeps minting
	// rather than holding a queue that never empties.
	f.advance(time.Hour)
	f.actsAs(opsUID, opsEmail, signInGoogle)
	if rec := f.apiAsCaller(http.MethodPost, apiOpsMintsPath, body); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("the mint after the second window answered %d (%s), want the queue served once more", rec.Code, rec.Body)
	}
	drained := false
	for _, one := range f.entitlementsOfOrganisation(opsCustomerID) {
		if one.ID == now[0].ID {
			drained = true
		}
	}
	if !drained {
		t.Errorf("the second window served nothing: the store holds no licence under %s", now[0].ID)
	}
}

// TestEveryOperationsActionIsAudited: every route of this area writes a line
// before it acts, reads included (D456). The cases below are the routes
// themselves and the table is pinned against the registry, so a route added to
// this area without a line — or a line nobody wrote down — fails here.
func TestEveryOperationsActionIsAudited(t *testing.T) {
	f := newFixture(t)
	f.operationsOrg(roleOwner)
	_, granted := f.operationsGround()
	ends := jsonString(t, f.now.Add(60*24*time.Hour).UTC().Format(time.RFC3339))

	cases := []struct {
		name string
		// method and route are the registered pair, which is what the registry
		// is pinned against; request is the path asked for, with a real id in it.
		method  string
		route   string
		request string
		body    string
		action  string
		read    bool
	}{
		{name: "the membership", method: http.MethodGet, route: apiOpsMembershipPath, request: apiOpsMembershipPath,
			action: actionOperationsMembershipRead, read: true},
		{name: "the lookup", method: http.MethodGet, route: apiOpsOrganisationsPath, request: apiOpsOrganisationsPath + "?q=Dana",
			action: actionOrganisationSearch, read: true},
		{name: "the workspaces", method: http.MethodGet, route: apiOpsWorkspacesPath, request: apiOpsWorkspacesPath + "?organisation_id=" + opsCustomerID,
			action: actionWorkspaceList, read: true},
		{name: "the licences", method: http.MethodGet, route: apiOpsEntitlementsPath, request: apiOpsEntitlementsPath + "?organisation_id=" + opsCustomerID,
			action: actionEntitlementList, read: true},
		{name: "the members", method: http.MethodGet, route: apiOpsMembersPath, request: apiOpsMembersPath + "?organisation_id=" + opsCustomerID,
			action: actionOrganisationMembersRead, read: true},
		{name: "the keys", method: http.MethodGet, route: apiOpsKeysPath, request: apiOpsKeysPath + "?workspace_id=" + opsBoundWS,
			action: actionKeyList, read: true},
		{name: "the queue", method: http.MethodGet, route: apiOpsEventsPath, request: apiOpsEventsPath + "?state=" + ledgerParked,
			action: actionQueueRead, read: true},
		{name: "the ledger", method: http.MethodGet, route: apiOpsLedgerPath, request: apiOpsLedgerPath + "?month=" + f.now.UTC().Format("2006-01"),
			action: actionLedgerRead, read: true},
		{name: "the log", method: http.MethodGet, route: apiOpsAuditPath, request: apiOpsAuditPath,
			action: actionAuditRead, read: true},
		{name: "the lists read", method: http.MethodGet, route: apiOpsListsPath, request: apiOpsListsPath,
			action: actionOperationsListsRead, read: true},
		{name: "a mint", method: http.MethodPost, route: apiOpsMintsPath, request: apiOpsMintsPath,
			body:   f.mintBody(opsMintWS, PolicyTeamAnnual, 3, f.now.Add(30*24*time.Hour)),
			action: actionEntitlementMint},
		{name: "a comp", method: http.MethodPost, route: apiOpsEntitlementActionsPath, request: apiOpsEntitlementActionsPath,
			body:   actionBody(t, granted.ID, "comp", "null", "null", "null"),
			action: actionEntitlementComp},
		{name: "an extension", method: http.MethodPost, route: apiOpsEntitlementActionsPath, request: apiOpsEntitlementActionsPath,
			body:   actionBody(t, granted.ID, "extend", "null", ends, "null"),
			action: actionEntitlementExtend},
		{name: "a seat change", method: http.MethodPost, route: apiOpsEntitlementActionsPath, request: apiOpsEntitlementActionsPath,
			body:   actionBody(t, granted.ID, "seats", "5", "null", "null"),
			action: actionEntitlementSeats},
		{name: "a transfer", method: http.MethodPost, route: apiOpsProjectTransfersPath, request: apiOpsProjectTransfersPath,
			body:   transferBody(t, opsProject, opsFreeWS, "null"),
			action: actionProjectTransfer},
		{name: "a resolution", method: http.MethodPost, route: apiOpsEventResolutionsPath, request: apiOpsEventResolutionsPath,
			body:   `{"id":` + jsonString(t, opsEventID) + `,"decision":"dismiss","entitlement_id":null}`,
			action: actionLedgerEventDismiss},
		{name: "the lists save", method: http.MethodPut, route: apiOpsListsPath, request: apiOpsListsPath,
			body:   `{"email_domains":["fugaro.example"],"microsoft_directories":[]}`,
			action: actionOperationsListsSet},
		{name: "cutting a handover short", method: http.MethodPost, route: apiOpsHandoverCutPath, request: cutPath(opsHandoverID),
			action: actionKeyHandoverCut},
		// The revocation is last because it is the one act here that ends the
		// licence every other case above writes against.
		{name: "a revocation", method: http.MethodPost, route: apiOpsEntitlementActionsPath, request: apiOpsEntitlementActionsPath,
			body:   actionBody(t, granted.ID, "revoke", "null", "null", jsonString(t, "the customer asked for the term to end")),
			action: actionEntitlementRevoke},
	}

	// The table covers the registry: every route this area registers is asked
	// for here, and a route that is not is the failure this case exists for.
	pinned := map[string]bool{}
	for _, tc := range cases {
		pinned[tc.method+" "+tc.route] = true
	}
	mounted := 0
	for _, route := range apiRoutes.routes {
		if !strings.HasPrefix(route.path, "/api/ops") {
			continue
		}
		mounted++
		if !pinned[route.method+" "+route.path] {
			t.Errorf("the route %s %s is mounted and no case here asks for it, so its line is nobody's to check", route.method, route.path)
		}
	}
	if mounted == 0 {
		t.Fatal("no operations route is mounted at all")
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.apiAsCaller(tc.method, tc.request, tc.body)
			switch rec.Code {
			case http.StatusOK, http.StatusCreated, http.StatusNoContent:
			default:
				t.Fatalf("the %s answered %d (%s), want it answered: a refused action writes no line and this case is about the line", tc.name, rec.Code, rec.Body)
			}
		})
	}

	entries := f.auditEntries()
	for _, tc := range cases {
		count := 0
		for _, entry := range entries {
			if entry.Action != tc.action {
				continue
			}
			count++
			if entry.Read != tc.read {
				t.Errorf("the line for %s carries read=%v, want %v (D456)", tc.name, entry.Read, tc.read)
			}
			if entry.ActorUID != opsUID || entry.ActorEmail != opsEmail {
				t.Errorf("the line for %s names %q (%s), want the caller who made the request", tc.name, entry.ActorUID, entry.ActorEmail)
			}
			if entry.At.IsZero() {
				t.Errorf("the line for %s carries no instant", tc.name)
			}
		}
		if count != 1 {
			t.Errorf("the %s left %d line(s) with the action %s, want one", tc.name, count, tc.action)
		}
	}
	// Every line the log holds is one of the table's, and there is one per
	// request: an action this area writes that the table does not name is one a
	// reader of this case cannot count.
	named := map[string]bool{}
	for _, tc := range cases {
		named[tc.action] = true
	}
	for _, entry := range entries {
		if !named[entry.Action] {
			t.Errorf("the log holds a line with the action %q, which no case here accounts for", entry.Action)
		}
	}
	if len(entries) != len(cases) {
		t.Errorf("the %d requests left %d line(s), want one each (D456)", len(cases), len(entries))
	}
}

// TestOperationsSeatChangeReachesTheKey: §5.3 and D2200 at this door. The seat
// count is a claim in the **key** and not in the term, and the renewal of a
// licence no subscription sold mints the next term from the key's own record
// (renew.go reads the record it holds and never the licence), so a change that
// only rewrote the licence would never reach the installation: the key in its
// hand would go on stating the count the customer gave up, for as long as it
// renews. What the change does instead is what the merchant's own seat change
// does — a successor under the same licence, the licence's renewal secret kept,
// and the key it supersedes left in the handover for `/renew` to answer with.
func TestOperationsSeatChangeReachesTheKey(t *testing.T) {
	// The v1 minter the renewal door mints through (Check 174), which the
	// binding fixture leaves unset because a binding mints through the other
	// seam: the last step below is a renewal and no other part of this case is.
	f := newBindingFixtureWith(t, func(f *fixture) { f.deps.MintsV1 = f.recording })
	org := f.seededOrganisation(opsCustomerID, "Dana Ltd", orgTypeCustomer, danaUID, danaEmail, roleOwner)
	workspace := f.seedWorkspace(opsBoundWS, org.ID, "Dana's workspace")
	held := f.grant(t, entitlementGrant{
		At:             f.now,
		OrganisationID: org.ID,
		WorkspaceID:    &workspace.ID,
		Source:         entitlementSourceOperations,
		Row:            PolicyTeamAnnual,
		Seats:          3,
		StartsAt:       f.now,
	})
	if rec := f.bind(t, held.ID, opsProject); rec.Code != http.StatusOK {
		t.Fatalf("binding the licence answered %d (%s), want the key a seat change is about", rec.Code, rec.Body)
	}
	binding := f.records()[0]
	if binding.Seats != 3 {
		t.Fatalf("the bound key states %d seat(s), want the licence's 3", binding.Seats)
	}
	// The secret the installation keeps is the mint's own answer and nothing the
	// store holds (§7.3), and the key it belongs to is the record's.
	secret := f.secretOf(t, f.mintedKeys()[0])
	if answer := f.mints.answers[0]; answer.Record.KeyID != binding.KeyID {
		t.Fatalf("the mint answered the key %s and the licence's record names %s", answer.Record.KeyID, binding.KeyID)
	}

	f.operationsOrg(roleOwner)
	rec := f.apiAsCaller(http.MethodPost, apiOpsEntitlementActionsPath, actionBody(t, held.ID, "seats", "5", "null", "null"))
	if rec.Code != http.StatusOK {
		t.Fatalf("the seat change answered %d (%s), want it made", rec.Code, rec.Body)
	}
	if answered := readAnswer[WireEntitlement](f.fixture, rec); answered.Seats != 5 {
		t.Errorf("the answer states %d seat(s), want 5", answered.Seats)
	}
	// The licence moved …
	if now, _ := heldRecord[WireEntitlement](f.fixture, entitlementsCollection, held.ID); now.Seats != 5 {
		t.Errorf("the licence states %d seat(s) after the change, want 5", now.Seats)
	}
	// … and so did the key: a successor under the same licence, at the new count.
	records := f.records()
	if len(records) != 2 {
		t.Fatalf("the store holds %d record(s), want the binding's and the seat change's", len(records))
	}
	successor := records[1]
	if successor.KeyID == binding.KeyID {
		t.Fatal("the seat change wrote no key: an installation goes on renewing from the one it holds, so the count it was minted with is the count it keeps")
	}
	if successor.EntitlementID != held.ID || successor.Seats != 5 {
		t.Errorf("the successor is %+v, want a key of %s at 5 seat(s)", successor, held.ID)
	}
	state, known, err := f.store.KeyState(context.Background(), successor.KeyID)
	if err != nil || !known {
		t.Fatalf("the successor has no key state (known=%v, err=%v), want one: a key with no state is one nothing can renew", known, err)
	}
	if state.RenewalSecretHash != digest(secret) {
		t.Errorf("the successor's state holds %q, want the digest of the secret the installation already has: a reissue keeps the licence's secret and never rotates it (D2200)", state.RenewalSecretHash)
	}
	if _, live, err := f.store.Handover(context.Background(), binding.KeyID, f.now); err != nil || !live {
		t.Fatalf("the store holds no live handover for the replaced key %s (live=%v, err=%v): without it only a mail would tell the installation, and this door sends none", binding.KeyID, live, err)
	}

	// The installation is handed the successor: the key it holds is answered
	// with the licence's newest one and nothing is minted, because nothing about
	// that key was spent (D2200).
	rec = f.call(RouteRenew, RenewalCall{KeyID: binding.KeyID, WorkspaceID: opsProject, RenewalSecret: secret})
	if rec.Code != http.StatusOK {
		t.Fatalf("the replaced key's renewal was answered %d (%q), want the seat change's key", rec.Code, rec.Body.String())
	}
	answer := answerOf(t, rec)
	if !answer.NewerKey {
		t.Error("the answer is not marked as a newer key, want it: the seat change's key replaces the one the caller presented (D2200)")
	}
	claims, _, err := licence.Verify([]byte(answer.Key), f.now, f.ring, opsProject)
	if err != nil {
		t.Fatalf("the key the handover answered does not verify: %v", err)
	}
	if claims.KeyID != successor.KeyID || claims.Seats != 5 {
		t.Errorf("`/renew` handed the replaced key %s at %d seat(s), want the licence's newest %s at 5", claims.KeyID, claims.Seats, successor.KeyID)
	}
	if again := f.records(); len(again) != 2 {
		t.Errorf("the handover minted a key of its own: the store holds %d record(s), want the licence's two", len(again))
	}

	// And the count carries on. The successor's own renewal mints the term after
	// it from its own record's seats, which is the path a licence no subscription
	// sold renews through (renew.go), so the installation states the new count
	// from here on rather than the one the customer gave up.
	f.inTheRenewalWindow(t, successor.KeyID)
	rec = f.call(RouteRenew, RenewalCall{KeyID: successor.KeyID, WorkspaceID: opsProject, RenewalSecret: secret})
	if rec.Code != http.StatusOK {
		t.Fatalf("the successor's own renewal was answered %d (%q), want the next term", rec.Code, rec.Body.String())
	}
	var rotation Record
	for _, record := range f.records() {
		if record.Source == rotationSource(successor.KeyID) {
			rotation = record
		}
	}
	if rotation.KeyID == "" {
		t.Fatal("the successor's renewal minted no successor, and the store holds no rotation of it")
	}
	if rotation.Seats != 5 || rotation.EntitlementID != held.ID {
		t.Errorf("the renewed key is %+v, want a key of %s carrying the 5 seats the customer was moved to", rotation, held.ID)
	}
}

// TestOperationsRevokeWithdrawsEveryKeyOfTheLicence: F29 at this door. The
// withdrawal is `merchant.go`'s walk read a second time (its `withdrawn` is the
// merchant's own refunds), and what the two must agree on is the **set** of
// keys: every record that names the licence, every other issuance of the
// subscription it was sold under, and every successor those keys were rotated
// into — and never a key of another licence that happens to be minted from the
// same code. A divergence would leave a live key on a licence operations ended,
// which is the gap F29 states, and no read of this area would show it.
func TestOperationsRevokeWithdrawsEveryKeyOfTheLicence(t *testing.T) {
	f, _ := renewing(t)
	sold := f.purchase("evt-ops-1", "acme.example", 5, 365)
	// The licence this revocation must not reach: another workspace's, because
	// two purchases into one workspace are one licence under D2204 and the
	// second supersedes the first.
	untouched, _, _ := heldKeyIn(t, f, PolicyTeamMonthly, "widget-workshop")
	// Two renewals, so the licence the revocation names has been succeeded
	// twice: the chain the walk follows from a key to the one that replaced it
	// is three keys long.
	theSubscriptionCovers(t, f, sold.KeyID)
	second, secondSecret := renewThroughTheEndpoint(t, f, sold.KeyID, f.secretOf(t, f.mintedKeys()[0]))
	theSubscriptionCovers(t, f, second.KeyID)
	third, _ := renewThroughTheEndpoint(t, f, second.KeyID, secondSecret)
	// The last successor is left naming no subscription, which is the shape a
	// record written before the field existed has: the walk's subscription step
	// cannot reach it, and the licence its own record names is what does (P3.6,
	// F29).
	withoutASubscription(t, f, third.KeyID)

	f.operationsOrg(roleOwner)
	at := f.now
	rec := f.apiAsCaller(http.MethodPost, apiOpsEntitlementActionsPath,
		actionBody(t, sold.EntitlementID, "revoke", "null", "null", jsonString(t, "the customer asked for the licence to end")))
	if rec.Code != http.StatusOK {
		t.Fatalf("the revocation answered %d (%s), want it made", rec.Code, rec.Body)
	}

	for _, keyID := range []string{sold.KeyID, second.KeyID, third.KeyID} {
		entry, held, err := f.store.Revocation(context.Background(), keyID)
		if err != nil || !held {
			t.Errorf("the revocation left no entry for %s (held=%v err=%v), want every key of the licence withdrawn", keyID, held, err)
			continue
		}
		if entry.Reason != ReasonOperations || !entry.At.Equal(at) {
			t.Errorf("the entry for %s is %+v, want %q at the instant the act was made %s", keyID, entry, ReasonOperations, at)
		}
	}
	if _, held, _ := f.store.Revocation(context.Background(), untouched); held {
		t.Errorf("the revocation withdrew the key %s as well, want another licence's key left alone", untouched)
	}
	if got := len(f.revocations()); got != 3 {
		t.Errorf("§8's list holds %d entry(ies), want the three keys of the chain", got)
	}
	// F29's rule is the licence and not only the keys that happened to be
	// minted: a licence left active is one the customer binds again.
	if now, _ := heldRecord[WireEntitlement](f, entitlementsCollection, sold.EntitlementID); now.Status != entitlementRevoked {
		t.Errorf("the licence is %q after the revocation, want %q", now.Status, entitlementRevoked)
	}
}

// TestOperationsNeverSeesASecret: F5's line holds at this door too. Operations
// reads what a licence and a key ARE and never what a key carries: a real key
// and its renewal secret are in the fixture's own record of the mint, and
// neither may appear in any answer of this area. The one thing operations may do
// with a secret is take it away, which is the handover cut that answers nothing.
func TestOperationsNeverSeesASecret(t *testing.T) {
	f := newFixture(t)
	// A real purchase and binding, so a real key and its renewal secret exist:
	// a metadata read that carries no material is only checkable against
	// material that is really there.
	f.purchase("txn-operations", "dana.example", 3, 365)
	f.operationsOrg(roleOwner)
	f.operationsGround()
	if len(f.recording.keys) == 0 || len(f.recording.secrets) == 0 {
		t.Fatal("the fixture minted no key with a secret, so this case checks nothing")
	}

	// The workspace the purchase's binding made, found by the project id it
	// wrote: that key is the one whose material this case looks for.
	bound, err := recordsWhere[WireWorkspace](context.Background(), f.docs, workspacesCollection,
		Where{Field: "project_id", Value: workspaceID})
	if err != nil || len(bound) != 1 {
		t.Fatalf("the purchased binding left %d workspace(s) on %s (err=%v), want one", len(bound), workspaceID, err)
	}

	answered := map[string]bool{}
	for _, tc := range []struct {
		name      string
		workspace string
		project   string
		want      int
	}{
		{name: "the seeded licence's", workspace: opsBoundWS, project: opsProject, want: 1},
		{name: "the purchased licence's", workspace: bound[0].ID, project: workspaceID, want: 1},
	} {
		rec := f.apiAsCaller(http.MethodGet, apiOpsKeysPath+"?workspace_id="+tc.workspace, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("the key metadata read of %s answered %d (%s), want it", tc.name, rec.Code, rec.Body)
		}
		keys := readAnswer[[]WireKeyMetadata](f, rec)
		if len(keys) != tc.want {
			t.Fatalf("the metadata read of %s answers %d key(s), want %d: %+v", tc.name, len(keys), tc.want, keys)
		}
		for _, key := range keys {
			answered[key.KeyID] = true
			if key.WorkspaceID != tc.project {
				t.Errorf("the key %s names the project %q, want the bound one %q (TN2)", key.KeyID, key.WorkspaceID, tc.project)
			}
			if key.KeyID == "" || key.EntitlementID == "" || key.Tier == "" || key.Kind == "" {
				t.Errorf("the metadata for %s is %+v, want it to describe the key", key.KeyID, key)
			}
		}
		// The whole answer as bytes: neither the key texts nor the renewal
		// secrets the mint answered are anywhere in it.
		body := rec.Body.String()
		for _, secret := range append(append([]string(nil), f.recording.keys...), f.recording.secrets...) {
			if secret == "" {
				continue
			}
			if strings.Contains(body, secret) {
				t.Errorf("a key's own material reached the operations read of %s: %s", tc.name, secret)
			}
		}
		// And no field of the answer is one a secret would be carried in: F5
		// lets operations see what a key is and never what it carries.
		for _, forbidden := range []string{"secret", "sealed", "renewal"} {
			if strings.Contains(strings.ToLower(body), forbidden) {
				t.Errorf("the metadata answer of %s carries %q", tc.name, forbidden)
			}
		}
	}
	if !answered[opsKeyID] {
		t.Errorf("the metadata reads answer %v, want the seeded key %s among them", answered, opsKeyID)
	}

	// The cut takes the handover away and answers nothing at all: no body, so
	// nothing about the successor can be read back through it.
	rec := f.apiAsCaller(http.MethodPost, cutPath(opsHandoverID), "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("the handover cut answered %d (%s), want 204 and no body", rec.Code, rec.Body)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("the handover cut answered the body %q, and the one thing it may not do is hand anything over", rec.Body.String())
	}
	if _, open, _ := f.store.Handover(context.Background(), opsHandoverID, f.now); open {
		t.Error("the handover is still open after the cut")
	}
	// And the store no longer holds the sealed bytes: the read just above is
	// what sweeps a window that has closed, and what was kept for that window
	// goes with it.
	if sealed := f.sealedHandovers(); len(sealed) != 0 {
		t.Errorf("the store still holds %d sealed handover(s) after the cut", len(sealed))
	}
}

// sealedHandovers is every handover the store still holds sealed bytes for,
// which is what the sweep dropping a closed window is read through.
func (f *fixture) sealedHandovers() []Handover {
	f.t.Helper()
	f.store.mu.Lock()
	defer f.store.mu.Unlock()
	out := make([]Handover, 0, len(f.store.handovers))
	for _, held := range f.store.handovers {
		if len(held.Sealed) > 0 {
			out = append(out, held)
		}
	}
	return out
}

// TestDomainAndTenantListsAreOwnerSet: the two lists are the operations team's
// own door, which D2187 leaves to the owner alone where every other change here
// is an admin's too (D455, D2207, F40). The lists are the sign-in rule's data
// and not a screen's, so what a refusal leaves behind is checked and not only
// the status.
func TestDomainAndTenantListsAreOwnerSet(t *testing.T) {
	f := newFixture(t)
	operations := f.operationsOrg(roleAdmin)

	// An admin reads them and may not save them.
	rec := f.apiAsCaller(http.MethodGet, apiOpsListsPath, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("an admin reading the lists answered %d (%s), want them", rec.Code, rec.Body)
	}
	empty := readAnswer[WireOpsLists](f, rec)
	if len(empty.EmailDomains) != 0 || len(empty.MicrosoftDirectories) != 0 {
		t.Errorf("a deployment that has set no list answers %+v, want two empty lists", empty)
	}
	rec = f.apiAsCaller(http.MethodPut, apiOpsListsPath, `{"email_domains":["fugaro.example"],"microsoft_directories":["dir-1"]}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("an admin saving the lists answered %d (%s), want 403 (D2187, F40)", rec.Code, rec.Body)
	}
	if _, written := heldRecord[WireOpsLists](f, f.cfg.TenantsCollection, operations.ID); written {
		t.Error("an admin's refused save wrote the lists")
	}

	// A list the schema will not take is refused rather than trimmed into one:
	// what a refusal leaves behind is nothing at all.
	f.setRole(operations.ID, opsUID, roleOwner)
	for _, body := range []string{
		`{"email_domains":["Not A Domain"],"microsoft_directories":[]}`,
		`{"email_domains":["fugaro.example"],"microsoft_directories":[""]}`,
	} {
		rec = f.apiAsCaller(http.MethodPut, apiOpsListsPath, body)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("saving %s answered %d (%s), want the refusal", body, rec.Code, rec.Body)
		}
		if code := refusal(f, rec); code != "invalid_list" {
			t.Errorf("the refusal carries the code %q, want invalid_list", code)
		}
	}
	if _, written := heldRecord[WireOpsLists](f, f.cfg.TenantsCollection, operations.ID); written {
		t.Error("a refused save wrote the lists")
	}

	// The owner's save is what the sign-in rule then reads.
	rec = f.apiAsCaller(http.MethodPut, apiOpsListsPath, `{"email_domains":["fugaro.example"],"microsoft_directories":["dir-1","dir-2"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("the owner saving the lists answered %d (%s), want them written", rec.Code, rec.Body)
	}
	if written := readAnswer[WireOpsLists](f, rec); len(written.EmailDomains) != 1 || written.EmailDomains[0] != "fugaro.example" || len(written.MicrosoftDirectories) != 2 {
		t.Errorf("the save answered %+v, want the two lists it was given", written)
	}
	stored, held := heldRecord[WireOpsLists](f, f.cfg.TenantsCollection, operations.ID)
	if !held || len(stored.EmailDomains) != 1 || len(stored.MicrosoftDirectories) != 2 {
		t.Errorf("the store holds %+v for the operations organisation, want the lists that were saved", stored)
	}
	if domains, err := f.svc.invitationDomains(context.Background(), operations.ID); err != nil || len(domains) != 1 || domains[0] != "fugaro.example" {
		t.Errorf("the sign-in rule reads the domains %v (%v), want the list that was saved: the lists are the rule's own data (F40)", domains, err)
	}

	// An empty list is a real answer and not a missing one: saving nothing is
	// not the same as never having set anything, and the answer says so.
	rec = f.apiAsCaller(http.MethodPut, apiOpsListsPath, `{"email_domains":[],"microsoft_directories":[]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("the owner saving two empty lists answered %d (%s), want them written", rec.Code, rec.Body)
	}
	rec = f.apiAsCaller(http.MethodGet, apiOpsListsPath, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("reading the lists back answered %d (%s), want them", rec.Code, rec.Body)
	}
	if got := readAnswer[WireOpsLists](f, rec); got.EmailDomains == nil || got.MicrosoftDirectories == nil {
		t.Errorf("the lists read back as %+v, want two lists and never a null", got)
	}
}

// TestOperationsCutsAHandoverShort: the one thing operations may do with a
// secret is take it away. A handover is the window a reissue's successor can
// still be fetched in, and cutting it short closes that window at this instant —
// so the successor is no longer answerable and the store drops what it kept for
// the window it no longer has.
func TestOperationsCutsAHandoverShort(t *testing.T) {
	f := newFixture(t)
	f.operationsOrg(roleOwner)
	f.operationsGround()

	held, open, err := f.store.Handover(context.Background(), opsHandoverID, f.now)
	if err != nil || !open {
		t.Fatalf("the seeded handover is not open (open=%v, err=%v), so this case checks nothing", open, err)
	}
	if len(held.Sealed) == 0 {
		t.Fatal("the seeded handover carries nothing sealed, so the cut has nothing to take away")
	}

	rec := f.apiAsCaller(http.MethodPost, cutPath(opsHandoverID), "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("the cut answered %d (%s), want 204", rec.Code, rec.Body)
	}
	if _, open, err := f.store.Handover(context.Background(), opsHandoverID, f.now); err != nil || open {
		t.Errorf("the handover is still open after the cut (open=%v, err=%v)", open, err)
	}
	if sealed := f.sealedHandovers(); len(sealed) != 0 {
		t.Errorf("the store still holds %+v after the cut, want the sealed bytes gone with the window", sealed)
	}

	// A second cut has nothing to close and says so, rather than answer 204 as
	// though it had done something.
	rec = f.apiAsCaller(http.MethodPost, cutPath(opsHandoverID), "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a second cut answered %d (%s), want 404", rec.Code, rec.Body)
	}
	if code := refusal(f, rec); code != "no_handover" {
		t.Errorf("the refusal carries the code %q, want no_handover", code)
	}

	// The cut that happened is the one in the log, and it names the key: a
	// refused cut writes no line, and a line about a key nobody can name is one
	// nobody can audit.
	lines := 0
	for _, entry := range f.auditEntries() {
		if entry.Action != actionKeyHandoverCut {
			continue
		}
		lines++
		if entry.TargetID == nil || *entry.TargetID != opsHandoverID {
			t.Errorf("the cut's line names the target %v, want the key %s", entry.TargetID, opsHandoverID)
		}
		if entry.Read {
			t.Error("the cut's line is marked as a read, and it took a secret away")
		}
	}
	if lines != 1 {
		t.Errorf("the log holds %d cut line(s), want the one cut that happened", lines)
	}
}

// TestProjectTransferIsAudited: D2204's transfer moves one Firebase project id
// from the workspace that bound it to another (TN-P1). The workspace that loses
// it is left unbound and its keys are withdrawn — a key whose claim names a
// project the customer no longer holds is a live licence nobody is looking at —
// and the person whose project was taken can rebind at once, because D2198's
// thirty days are not a remedy's own delay. The act is audited and the project
// id is what the line names.
func TestProjectTransferIsAudited(t *testing.T) {
	f := newFixture(t)
	f.operationsOrg(roleOwner)
	_, granted := f.operationsGround()

	rec := f.apiAsCaller(http.MethodPost, apiOpsProjectTransfersPath,
		transferBody(t, opsProject, opsFreeWS, jsonString(t, "customer set the wrong project up")))
	if rec.Code != http.StatusOK {
		t.Fatalf("the transfer answered %d (%s), want the destination workspace", rec.Code, rec.Body)
	}
	moved := readAnswer[WireWorkspace](f, rec)
	if moved.ID != opsFreeWS || moved.ProjectID == nil || *moved.ProjectID != opsProject {
		t.Errorf("the transfer answered %+v, want the destination holding %q", moved, opsProject)
	}

	source, _ := heldRecord[WireWorkspace](f, workspacesCollection, opsBoundWS)
	if source.ProjectID != nil || source.BoundAt != nil {
		t.Errorf("the source workspace is %+v after the transfer, want it unbound", source)
	}
	stored, _ := heldRecord[WireWorkspace](f, workspacesCollection, opsFreeWS)
	if stored.ProjectID == nil || *stored.ProjectID != opsProject {
		t.Errorf("the destination is %+v in the store, want it holding %q", stored, opsProject)
	}

	// Every key the source binding minted is withdrawn (F29), and the licence
	// itself is untouched: it is the licence the customer still holds, and it is
	// bindable again at once.
	revoked, withdrawn, err := f.store.Revocation(context.Background(), opsKeyID)
	if err != nil {
		t.Fatalf("reading the withdrawal of %s: %v", opsKeyID, err)
	}
	if !withdrawn {
		t.Fatalf("the key %s the source workspace's licence minted was not withdrawn", opsKeyID)
	}
	if revoked.Reason != ReasonOperations {
		t.Errorf("the withdrawal is filed under the reason %q, want %q", revoked.Reason, ReasonOperations)
	}
	after, _ := heldRecord[WireEntitlement](f, entitlementsCollection, granted.ID)
	if after.Status != granted.Status || after.Seats != granted.Seats {
		t.Errorf("the licence is %+v after the transfer, want the licence the customer holds", after)
	}
	if after.RebindableAt == nil || !after.RebindableAt.Equal(f.now) {
		t.Errorf("the licence is rebindable at %v, want the person whose project was taken to be able to bind it at once (D2198)", after.RebindableAt)
	}

	// The transfer is audited, and what the line names is the project id: the
	// question a reader of the log asks months later is which installation moved.
	audited := false
	for _, entry := range f.auditEntries() {
		if entry.Action != actionProjectTransfer {
			continue
		}
		audited = true
		if entry.TargetID == nil || *entry.TargetID != opsProject {
			t.Errorf("the transfer's line names the target %v, want the project id %s", entry.TargetID, opsProject)
		}
		if entry.Read {
			t.Error("the transfer's line is marked as a read, and it moved a project")
		}
		if entry.Detail == nil || !strings.Contains(*entry.Detail, "wrong project") {
			t.Errorf("the transfer's line carries the detail %v, want the reason the operator typed", entry.Detail)
		}
	}
	if !audited {
		t.Error("the transfer left no audit line, and every operations action is written to the log (D456)")
	}

	// A project nobody holds cannot be transferred, and a destination already
	// bound to another project is refused rather than half done: the record
	// would name one project while the key the installation runs was minted for
	// another.
	rec = f.apiAsCaller(http.MethodPost, apiOpsProjectTransfersPath,
		transferBody(t, "no-such-project", opsMintWS, "null"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("transferring a project nobody holds answered %d (%s), want 409", rec.Code, rec.Body)
	}
	if code := refusal(f, rec); code != "project_unbound" {
		t.Errorf("the refusal carries the code %q, want project_unbound", code)
	}
	other := opsOtherProj
	f.seed(workspacesCollection, "ws-taken", WireWorkspace{
		ID: "ws-taken", OrganisationID: opsCustomerID, Name: "Dana's other", ProjectID: &other, CreatedAt: f.now,
	})
	rec = f.apiAsCaller(http.MethodPost, apiOpsProjectTransfersPath,
		transferBody(t, opsProject, "ws-taken", "null"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("transferring into a bound workspace answered %d (%s), want 409", rec.Code, rec.Body)
	}
	if code := refusal(f, rec); code != "workspace_bound" {
		t.Errorf("the refusal carries the code %q, want workspace_bound", code)
	}
}

// TestParkedEventIsResolvedOnce: a parked event — one whose transaction and
// subscription did not begin with a transaction this API created (F15) — is
// operations' to attach or dismiss, and it is decided once. Attaching it says
// which licence it is attached to, dismissing it says it is nobody's, and a
// second answer to the same event is refused rather than applied: the state is
// the record of the decision and the body is never rewritten (D457).
func TestParkedEventIsResolvedOnce(t *testing.T) {
	f := newFixture(t)
	f.operationsOrg(roleOwner)
	_, granted := f.operationsGround()
	body := `{"event_id":"` + opsEventID + `"}`

	// An attach names the licence, and the event ends as one this API placed.
	rec := f.apiAsCaller(http.MethodPost, apiOpsEventResolutionsPath,
		`{"id":`+jsonString(t, opsEventID)+`,"decision":"attach","entitlement_id":`+jsonString(t, granted.ID)+`}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("attaching the parked event answered %d (%s), want it resolved", rec.Code, rec.Body)
	}
	resolved := readAnswer[WireLedgerEvent](f, rec)
	if resolved.State != ledgerProcessed {
		t.Errorf("the attached event is %q, want %q", resolved.State, ledgerProcessed)
	}
	if resolved.EntitlementID == nil || *resolved.EntitlementID != granted.ID {
		t.Errorf("the attached event names the licence %v, want %s", resolved.EntitlementID, granted.ID)
	}
	if resolved.Body != body {
		t.Errorf("the event's body is %q after the attach, and what Paddle signed is never rewritten (D457)", resolved.Body)
	}
	stored, _ := heldRecord[WireLedgerEvent](f, paddleEventsCollection, opsEventID)
	if stored.State != ledgerProcessed || stored.EntitlementID == nil || *stored.EntitlementID != granted.ID {
		t.Errorf("the store holds %+v, want the event resolved onto %s", stored, granted.ID)
	}

	// The second answer is refused, and the refusal costs nothing: the decision
	// stands as the first one left it.
	rec = f.apiAsCaller(http.MethodPost, apiOpsEventResolutionsPath,
		`{"id":`+jsonString(t, opsEventID)+`,"decision":"dismiss","entitlement_id":null}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("deciding the event twice answered %d (%s), want 409", rec.Code, rec.Body)
	}
	if code := refusal(f, rec); code != "already_answered" {
		t.Errorf("the refusal carries the code %q, want already_answered", code)
	}
	stored, _ = heldRecord[WireLedgerEvent](f, paddleEventsCollection, opsEventID)
	if stored.State != ledgerProcessed || stored.EntitlementID == nil || *stored.EntitlementID != granted.ID {
		t.Errorf("the store holds %+v after the second answer, want the first decision standing", stored)
	}

	// A dismissal is its own state and not a processed event: nothing was
	// resolved, and the money is somebody else's.
	f.seed(paddleEventsCollection, "evt-parked-2", WireLedgerEvent{
		ID: "evt-parked-2", ReceivedAt: f.now, Signature: "sig", Notification: "transaction.completed",
		Body: `{"event_id":"evt-parked-2"}`, State: ledgerParked,
	})
	rec = f.apiAsCaller(http.MethodPost, apiOpsEventResolutionsPath,
		`{"id":"evt-parked-2","decision":"dismiss","entitlement_id":null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("dismissing the parked event answered %d (%s), want it resolved", rec.Code, rec.Body)
	}
	if dismissed := readAnswer[WireLedgerEvent](f, rec); dismissed.State != ledgerDismissed || dismissed.EntitlementID != nil {
		t.Errorf("the dismissed event is %+v, want %q and no licence", dismissed, ledgerDismissed)
	}

	// An attach naming no licence, a dismissal naming one, and a decision that
	// is neither are all refused: the decision and the field have to say the
	// same thing.
	f.seed(paddleEventsCollection, "evt-parked-3", WireLedgerEvent{
		ID: "evt-parked-3", ReceivedAt: f.now, Signature: "sig", Notification: "transaction.completed",
		Body: `{"event_id":"evt-parked-3"}`, State: ledgerParked,
	})
	for _, answer := range []string{
		`{"id":"evt-parked-3","decision":"attach","entitlement_id":null}`,
		`{"id":"evt-parked-3","decision":"dismiss","entitlement_id":` + jsonString(t, granted.ID) + `}`,
		`{"id":"evt-parked-3","decision":"maybe","entitlement_id":null}`,
	} {
		rec = f.apiAsCaller(http.MethodPost, apiOpsEventResolutionsPath, answer)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("the resolution %s answered %d (%s), want the refusal", answer, rec.Code, rec.Body)
		}
	}
	if parked, _ := heldRecord[WireLedgerEvent](f, paddleEventsCollection, "evt-parked-3"); parked.State != ledgerParked {
		t.Errorf("the refused resolutions left the event %q, want it parked", parked.State)
	}

	// The queue read answers what is still parked, which is the event nobody
	// has decided and not the two that were.
	rec = f.apiAsCaller(http.MethodGet, apiOpsEventsPath+"?state="+ledgerParked, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("the queue read answered %d (%s), want it", rec.Code, rec.Body)
	}
	if parked := readAnswer[[]WireLedgerEvent](f, rec); len(parked) != 1 || parked[0].ID != "evt-parked-3" {
		t.Errorf("the queue answers %+v, want the event nobody has decided", parked)
	}
}
