// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// Deletion (portal plan P2.4; D2195, F17).
//
// The cases are the plan's five. **A sole owner cannot delete their account** is
// the first one and it is D2195's rule itself: an organisation with no owner is
// one nobody can act in, so the refusal is checked and nothing is written on the
// way to it — no membership goes, no invitation goes, and the sign-in is still
// there.
//
// **Deleting an organisation revokes and cancels** is the pair the decision
// names: the merchant is asked to cancel every subscription the organisation
// runs, and every licence it holds is revoked with every key of it (F29), read
// here out of the store the way a revocation is read everywhere else.
//
// **The account's sign-in goes through the Identity Toolkit** is the seam this
// file installs a server of its own in: the call is this service's own and not
// the browser's, so a case drives a directory of its own and reads the request
// that reached it — the project in the path, the uid in the body, the method —
// rather than trusting that a field was passed along.
//
// **The memberships and the invitations go** is the record half of the same act,
// and the last case is a caller reaching an organisation that is not theirs: the
// answer is the one every other route gives, and nothing of the other
// organisation has moved.
//
// The fixture is the package's binding one (workspaces_test.go), which is what
// gives a case a bound licence with a key in it and a store that refuses a read
// after a write.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	// accountProject is the Firebase project these cases' directory answers for.
	// It is a project id and not a secret: a project id is public, and the SPA is
	// served it in its own configuration.
	accountProject = "portal-fixture"

	// secondOwnerUID and secondOwnerEmail are the second owner these cases put
	// beside the caller: a second owner is what makes an account deletion
	// possible at all, and a second owner of somebody else's organisation is who
	// may not be deleted over.
	secondOwnerUID   = "uid-frank"
	secondOwnerEmail = "frank@example.com"
)

// accountDirectoryServer is the Identity Toolkit's admin API in a case: what it
// answers, and every call it was handed. It is installed into the package's own
// seam (`authUserRemoval`), which is what a deployment fills with the real
// directory (deletion.go) and which no other build reaches.
type accountDirectoryServer struct {
	mu      sync.Mutex
	project string
	status  int
	body    string
	calls   []accountDeletionCall
	// during is one step a case runs while a call is being answered — the window
	// in which the account's records are already gone and its sign-in is not —
	// and what the step answers is kept below for the case to assert. It runs
	// once.
	during    func() bool
	duringRan bool
	duringSaw bool
	server    *httptest.Server
}

// accountDeletionCall is one call the directory was handed, as it arrived.
type accountDeletionCall struct {
	method string
	path   string
	body   string
}

// newAccountDirectoryServer starts the server and points the seam at it for the
// rest of the case.
func newAccountDirectoryServer(t *testing.T) *accountDirectoryServer {
	t.Helper()
	held := &accountDirectoryServer{project: accountProject, status: http.StatusOK}
	held.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		held.mu.Lock()
		held.calls = append(held.calls, accountDeletionCall{method: r.Method, path: r.URL.Path, body: string(body)})
		status, answer, during := held.status, held.body, held.during
		held.during = nil
		held.mu.Unlock()
		if during != nil {
			saw := during()
			held.mu.Lock()
			held.duringRan, held.duringSaw = true, saw
			held.mu.Unlock()
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(held.server.Close)
	previous := authUserRemoval
	authUserRemoval = held.directory().delete
	t.Cleanup(func() { authUserRemoval = previous })
	return held
}

// directory is the deployment's own value pointed at this case's server, which
// is what the seam is handed: what is under test is `delete` and the server is
// at the other end of it.
func (s *accountDirectoryServer) directory() accountDirectory {
	return accountDirectory{endpoint: s.server.URL, project: s.project, client: s.server.Client()}
}

// saw is every call the directory was handed.
func (s *accountDirectoryServer) saw() []accountDeletionCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]accountDeletionCall(nil), s.calls...)
}

// deleted is every uid the directory was asked to delete, read out of the calls
// the way the API reads the field.
func (s *accountDirectoryServer) deleted(t *testing.T) []string {
	t.Helper()
	out := []string{}
	for _, call := range s.saw() {
		var asked struct {
			LocalID string `json:"localId"`
		}
		if err := json.Unmarshal([]byte(call.body), &asked); err != nil {
			t.Fatalf("reading the directory call's body %q: %v", call.body, err)
		}
		out = append(out, asked.LocalID)
	}
	return out
}

// sawDuring is what the case's own step read while a call was in flight, and
// whether it ran at all.
func (s *accountDirectoryServer) sawDuring() (ran, saw bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.duringRan, s.duringSaw
}

// deletionFixture is the binding fixture with the Identity Toolkit's seam
// installed: every case here is about a route that reaches either it or the
// merchant of record, and both are stubs.
type deletionFixture struct {
	*bindingFixture
	directory *accountDirectoryServer
}

func newDeletionFixture(t *testing.T) *deletionFixture {
	t.Helper()
	return &deletionFixture{
		bindingFixture: newCheckoutFixture(t),
		directory:      newAccountDirectoryServer(t),
	}
}

// deleteOrg posts one organisation deletion the way the SPA does.
func (f *deletionFixture) deleteOrg(id string) *httptest.ResponseRecorder {
	f.t.Helper()
	return f.apiAsCaller(http.MethodDelete, "/api/orgs/"+id, "")
}

// deleteAccount posts the caller's own account deletion.
func (f *deletionFixture) deleteAccount() *httptest.ResponseRecorder {
	f.t.Helper()
	return f.apiAsCaller(http.MethodDelete, apiAccountPath, "")
}

// invitationTo writes one invitation straight into the store, in the state the
// case names: the shape P2.2's own route writes and this file does not. A case
// seeds one so that an account deletion meets an invitation an address is bound
// to.
func (f *deletionFixture) invitationTo(id, organisationID, email, state string) WireInvite {
	f.t.Helper()
	invitation := WireInvite{
		ID:             id,
		OrganisationID: organisationID,
		Email:          email,
		Role:           roleMember,
		State:          state,
		InvitedBy:      erinEmail,
		CreatedAt:      f.now,
		ExpiresAt:      f.now.Add(inviteTTL),
	}
	if state == inviteAccepted {
		at := f.now
		invitation.AcceptedAt = &at
	}
	f.seed(invitesCollection, invitation.ID, invitation)
	return invitation
}

// invitation reads one invitation out of the store.
func (f *deletionFixture) invitation(id string) (WireInvite, bool) {
	f.t.Helper()
	return heldRecord[WireInvite](f.fixture, invitesCollection, id)
}

// memberOf writes one membership straight into the store, which is how a case
// puts a second person in an organisation.
func (f *deletionFixture) memberOf(organisationID, uid, email, role string) {
	f.t.Helper()
	f.seed(membershipsCollection, membershipID(organisationID, uid), WireMembership{
		OrganisationID: organisationID, UID: uid, Email: email, Role: role, CreatedAt: f.now,
	})
}

// TestSoleOwnerCannotDeleteTheirAccount: D2195's rule. An account that is the
// only owner of an organisation is refused, and the refusal is the state it
// found: nothing is written on the way to it, so the membership, the invitation
// bound to the address and the sign-in are all still there.
//
// The second half is what makes the refusal the rule rather than a blanket one
// on owners: the same account, in the same organisation, is deleted the moment
// somebody else is an owner too.
func TestSoleOwnerCannotDeleteTheirAccount(t *testing.T) {
	f := newDeletionFixture(t)
	org := f.seededOrganisation("org-dana", "Dana Ltd", orgTypeCustomer, danaUID, danaEmail, roleOwner)
	invitation := f.invitationTo("invite-dana", org.ID, danaEmail, invitePending)
	f.actsAs(danaUID, danaEmail, signInGoogle)

	rec := f.deleteAccount()
	if rec.Code != http.StatusConflict {
		t.Fatalf("a sole owner deleting their account answered %d: %s", rec.Code, rec.Body)
	}
	if code := f.refused(rec); code != "sole_owner" {
		t.Errorf("the refusal is %q, want %q: it is a state the person can change, and the account screen draws the API's own sentence for it", code, "sole_owner")
	}
	if _, held := f.membership(org.ID, danaUID); !held {
		t.Error("the membership is gone after a refusal, and D2195's refusal is made before anything is written")
	}
	if _, held := f.invitation(invitation.ID); !held {
		t.Error("the invitation bound to the refused account's address is gone, and the refusal is made before anything is written")
	}
	if seen := f.directory.deleted(t); len(seen) != 0 {
		t.Errorf("the directory was asked to delete %v, and a refused deletion reaches no sign-in", seen)
	}

	// A second owner is somebody who can still act in the organisation, so the
	// refusal no longer holds — and it is the second owner's membership that
	// changes the answer, not the caller's own role.
	f.memberOf(org.ID, secondOwnerUID, secondOwnerEmail, roleOwner)
	rec = f.deleteAccount()
	if rec.Code != http.StatusNoContent {
		t.Fatalf("an owner deleting their account beside another owner answered %d: %s", rec.Code, rec.Body)
	}
	if seen := f.directory.deleted(t); len(seen) != 1 || seen[0] != danaUID {
		t.Errorf("the directory was asked to delete %v, want exactly %q", seen, danaUID)
	}
	if _, held := f.membership(org.ID, danaUID); held {
		t.Error("the membership stands after the account it belongs to was deleted")
	}
	if _, held := f.membership(org.ID, secondOwnerUID); !held {
		t.Error("the second owner's membership is gone, and their account was never deleted")
	}
}

// TestOrgDeletionRevokesAndCancels: an owner deletes their organisation and the
// two things D2195 names happen — the merchant of record is asked to cancel
// every subscription the organisation runs, and every licence it holds is
// revoked with every key of it (F29).
//
// What is left behind is the sales record: the subscription, the checkout's
// transaction and the licence itself, whose status is what moved. What goes is
// the organisation's own: the organisation, its memberships and its workspaces.
func TestOrgDeletionRevokesAndCancels(t *testing.T) {
	f := newDeletionFixture(t)
	ctx := context.Background()
	org, workspace := f.payingWorkspace(t, billingSeats)
	licence := f.onlyEntitlement(t, org.ID)
	if rec := f.bind(t, licence.ID, exampleProject); rec.Code != http.StatusOK {
		t.Fatalf("binding the licence the purchase bought answered %d: %s", rec.Code, rec.Body)
	}
	issued := f.issuances(t)
	if len(issued) != 1 {
		t.Fatalf("the binding wrote %d issuance(s), and this case reads the one key", len(issued))
	}
	keyID := issued[0].KeyID
	f.actsAs(danaUID, danaEmail, signInGoogle)

	rec := f.deleteOrg(org.ID)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("deleting the organisation answered %d: %s", rec.Code, rec.Body)
	}

	if len(f.paddle.cancelled) != 1 || f.paddle.cancelled[0] != "sub-dana" {
		t.Errorf("the merchant was asked to cancel %v, want the organisation's running subscription alone", f.paddle.cancelled)
	}
	withdrawn, found, err := f.store.Revocation(ctx, keyID)
	switch {
	case err != nil:
		t.Fatalf("reading the key's withdrawal back: %v", err)
	case !found:
		t.Error("the key the deleted organisation's licence minted is not withdrawn, so the installation it was given to runs on")
	case withdrawn.Reason != ReasonDeletion:
		t.Errorf("the key was withdrawn as %q, want %q: the money did not go back, and a person reading this later is asking which of the two happened", withdrawn.Reason, ReasonDeletion)
	case !withdrawn.At.Equal(f.now):
		t.Errorf("the withdrawal is dated %s, want the service's own clock (%s)", withdrawn.At, f.now)
	}
	revoked, present := f.entitlement(licence.ID)
	switch {
	case !present:
		t.Error("the licence's record is gone, and what a customer bought, for how many seats and until when is what a report is made of (P5.2)")
	case revoked.Status != entitlementRevoked:
		t.Errorf("the licence reads %q, want %q", revoked.Status, entitlementRevoked)
	}
	if _, held := f.organisation(org.ID); held {
		t.Error("the organisation is still there after it was deleted")
	}
	if _, held := f.membership(org.ID, danaUID); held {
		t.Error("a membership of the deleted organisation stands, which is a person the portal would list an organisation for that is not there")
	}
	if _, held := f.workspace(workspace.ID); held {
		t.Error("the deleted organisation's workspace stands, and nothing left can reach it")
	}
	if _, present, err := f.store.Subscription(ctx, "sub-dana"); err != nil || !present {
		t.Errorf("the subscription record answered present=%t err=%v, want it kept: it is a sales record, and the merchant's own cancellation event is what moves it (P4.2)", present, err)
	}
	if _, present := heldRecord[checkoutTransaction](f.fixture, checkoutTransactionsCollection, "txn-sub-dana"); !present {
		t.Error("the checkout's transaction is gone, and it is the record of what was bought and by whom")
	}
}

// TestOrgDeletionRevokesTheSubscriptionsKeys: the walk reaches a key by every
// route there is to one, and this is the route that is not the licence's id.
//
// A record written before an issuance carried the entitlement's id names the
// subscription and nothing else, and it is the shape the refund walk covers too
// (merchant.go): the join back to the subscription is a query on the licence's
// id, through the origin record the purchase wrote. A walk that read the
// licence's own records and stopped leaves this key live — at an installation
// whose organisation no longer exists, which is the whole of what a deletion is
// for.
func TestOrgDeletionRevokesTheSubscriptionsKeys(t *testing.T) {
	f := newDeletionFixture(t)
	ctx := context.Background()
	org, _ := f.payingWorkspace(t, billingSeats)
	licence := f.onlyEntitlement(t, org.ID)
	// The key the subscription minted back then: the plan, the buyer, the term
	// and the subscription, and no entitlement id at all.
	older := Record{
		Source:       "txn-before-the-field",
		KeyID:        "key-before-the-field",
		Plan:         licence.Row,
		Subscription: "sub-dana",
		Organisation: org.ID,
		Email:        danaEmail,
		Issued:       f.now.Add(-30 * 24 * time.Hour),
		Expires:      licence.EndsAt,
	}
	if err := f.store.SaveIssuance(ctx, older); err != nil {
		t.Fatalf("seeding the key the licence's subscription minted before a record named the licence: %v", err)
	}
	f.actsAs(danaUID, danaEmail, signInGoogle)

	rec := f.deleteOrg(org.ID)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("deleting the organisation answered %d: %s", rec.Code, rec.Body)
	}

	withdrawn, found, err := f.store.Revocation(ctx, older.KeyID)
	switch {
	case err != nil:
		t.Fatalf("reading the subscription's key back: %v", err)
	case !found:
		t.Error("the key the licence's subscription minted is not withdrawn, so an installation of the deleted organisation runs on")
	case withdrawn.Reason != ReasonDeletion:
		t.Errorf("the key was withdrawn as %q, want %q", withdrawn.Reason, ReasonDeletion)
	}
}

// TestAccountDeletionRemovesTheAuthUser: the account's Firebase Auth user goes,
// through the Identity Toolkit's own call, with this service's credentials and
// the deployment's project — the project in the path and the uid in the body.
//
// The order the two halves of the act are in is read while the call is in
// flight: the sign-in is deleted last, so an account's records are already gone
// by the time the directory is reached. That is what makes a directory that
// refuses recoverable — the person can still sign in, and the request they make
// again is the same one.
func TestAccountDeletionRemovesTheAuthUser(t *testing.T) {
	f := newDeletionFixture(t)
	organisation := f.seededOrganisation("org-erin", "Erin Ltd", orgTypeCustomer, erinUID, erinEmail, roleOwner)
	f.memberOf(organisation.ID, danaUID, danaEmail, roleMember)
	f.actsAs(danaUID, danaEmail, signInGoogle)

	f.directory.during = func() bool {
		_, held := f.membership(organisation.ID, danaUID)
		return !held
	}
	f.directory.status = http.StatusInternalServerError
	f.directory.body = `{"error":{"message":"INTERNAL"}}`
	refused := f.deleteAccount()
	if refused.Code != http.StatusInternalServerError {
		t.Fatalf("a directory that refused answered %d: %s", refused.Code, refused.Body)
	}
	ran, saw := f.directory.sawDuring()
	switch {
	case !ran:
		t.Error("the directory was never reached, so nothing about the order of the two halves was read")
	case !saw:
		t.Error("the account's membership was still there while its sign-in was being deleted, and a failure there leaves records nobody can reach")
	}

	// The second attempt is the whole reason the sign-in goes last: the records
	// are gone, the person can still sign in, and the one thing left is the
	// sign-in itself.
	f.directory.status = http.StatusOK
	f.directory.body = ""
	rec := f.deleteAccount()
	if rec.Code != http.StatusNoContent {
		t.Fatalf("the second attempt answered %d: %s", rec.Code, rec.Body)
	}
	calls := f.directory.saw()
	if len(calls) != 2 {
		t.Fatalf("the directory was handed %d call(s), want one per attempt", len(calls))
	}
	for _, call := range calls {
		switch {
		case call.method != http.MethodPost:
			t.Errorf("the directory was called with %s, want %s", call.method, http.MethodPost)
		case call.path != "/v1/projects/"+accountProject+"/accounts:delete":
			t.Errorf("the directory was called at %q, want the deployment's project in the path", call.path)
		case call.body != `{"localId":"`+danaUID+`"}`:
			t.Errorf("the directory was handed the body %q, want the caller's own uid and nothing else", call.body)
		}
	}
	if _, held := f.membership(organisation.ID, danaUID); held {
		t.Error("the membership stands after the account's sign-in was deleted, and a membership names a uid nobody can sign in as")
	}
}

// TestDeletionRemovesMembershipsAndInvitations: the record half of an account
// deletion. Every membership the person holds goes, in every organisation, and
// so does every invitation bound to their address — in any state, because an
// invitation is that address and nothing else, and the membership an accepted
// one became is going with the rest.
//
// What stays is somebody else's: another person's membership, another person's
// invitation, and the organisations themselves, which belong to whoever is left
// in them.
func TestDeletionRemovesMembershipsAndInvitations(t *testing.T) {
	f := newDeletionFixture(t)
	first := f.seededOrganisation("org-first", "First Ltd", orgTypeCustomer, erinUID, erinEmail, roleOwner)
	f.memberOf(first.ID, danaUID, danaEmail, roleMember)
	// The person is the only owner of the second organisation, so the deletion
	// is refused until somebody else is one: D2195's rule, read here as the
	// state the rest of the case starts from.
	second := f.seededOrganisation("org-second", "Second Ltd", orgTypeCustomer, danaUID, danaEmail, roleOwner)
	accepted := f.invitationTo("invite-accepted", first.ID, danaEmail, inviteAccepted)
	pending := f.invitationTo("invite-pending", second.ID, danaEmail, invitePending)
	mine := f.invitationTo("invite-erin", second.ID, erinEmail, invitePending)
	f.actsAs(danaUID, danaEmail, signInGoogle)

	f.memberOf(second.ID, secondOwnerUID, secondOwnerEmail, roleOwner)
	rec := f.deleteAccount()
	if rec.Code != http.StatusNoContent {
		t.Fatalf("deleting the account answered %d: %s", rec.Code, rec.Body)
	}

	for _, org := range []WireOrganisation{first, second} {
		if _, held := f.membership(org.ID, danaUID); held {
			t.Errorf("the membership of %s stands after the account was deleted", org.ID)
		}
		if _, held := f.organisation(org.ID); !held {
			t.Errorf("the organisation %s is gone, and deleting an account leaves the organisations the person belonged to", org.ID)
		}
	}
	if _, held := f.membership(first.ID, erinUID); !held {
		t.Error("another person's membership is gone, and this deletion is one account's")
	}
	for _, id := range []string{accepted.ID, pending.ID} {
		if _, held := f.invitation(id); held {
			t.Errorf("the invitation %s bound to the deleted account's address stands, and an invitation is that address and nothing else", id)
		}
	}
	if _, held := f.invitation(mine.ID); !held {
		t.Error("an invitation to somebody else's address is gone, and this deletion is one account's")
	}
}

// TestAnotherOrgCannotBeDeleted: a caller reaching an organisation that is not
// theirs is answered as every other route answers it — the same 404 for "not
// there" and "not yours", because which ids exist is a list of the portal's
// customers — and nothing of the other organisation moves.
//
// The member's answer is the other half: a person who IS in the organisation and
// is not its owner is told so, which is a fact about their own membership and
// something asking an owner fixes. Neither of the two refusals reaches the
// merchant, and neither touches a key.
func TestAnotherOrgCannotBeDeleted(t *testing.T) {
	f := newDeletionFixture(t)
	ctx := context.Background()
	victim, workspace := f.payingWorkspace(t, billingSeats)
	licence := f.onlyEntitlement(t, victim.ID)
	if rec := f.bind(t, licence.ID, exampleProject); rec.Code != http.StatusOK {
		t.Fatalf("binding the licence answered %d: %s", rec.Code, rec.Body)
	}
	keyID := f.issuances(t)[0].KeyID
	f.seededOrganisation("org-erin", "Erin Ltd", orgTypeCustomer, erinUID, erinEmail, roleOwner)

	f.actsAs(erinUID, erinEmail, signInGoogle)
	rec := f.deleteOrg(victim.ID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("another organisation's deletion answered %d: %s", rec.Code, rec.Body)
	}
	if code := f.refused(rec); code != "not_found" {
		t.Errorf("the refusal is %q, want %q: which organisation ids exist is a list of the portal's customers", code, "not_found")
	}

	// In the organisation and not its owner: the same route, and an answer the
	// person can act on.
	f.memberOf(victim.ID, erinUID, erinEmail, roleAdmin)
	rec = f.deleteOrg(victim.ID)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("an admin deleting the organisation answered %d: %s", rec.Code, rec.Body)
	}
	if code := f.refused(rec); code != "not_permitted" {
		t.Errorf("the refusal is %q, want %q", code, "not_permitted")
	}
	// An id nobody minted is the 404 as well, and not distinguished from one that
	// is somebody else's.
	if rec := f.deleteOrg("org-nobody"); rec.Code != http.StatusNotFound {
		t.Errorf("an organisation that is not there answered %d: %s", rec.Code, rec.Body)
	}

	if len(f.paddle.cancelled) != 0 {
		t.Errorf("the merchant was asked to cancel %v, and no deletion of that organisation went through", f.paddle.cancelled)
	}
	if _, held := f.organisation(victim.ID); !held {
		t.Error("the organisation is gone after three refusals")
	}
	if _, held := f.membership(victim.ID, danaUID); !held {
		t.Error("the owner's membership is gone after three refusals")
	}
	if _, held := f.workspace(workspace.ID); !held {
		t.Error("the workspace is gone after three refusals")
	}
	if _, found, err := f.store.Revocation(ctx, keyID); err != nil || found {
		t.Errorf("the key was withdrawn (found=%t err=%v), and no deletion of its organisation went through", found, err)
	}
	if held, present := f.entitlement(licence.ID); !present || held.Status != entitlementActive {
		t.Errorf("the licence reads %+v (present=%t), want it active: nothing of it was deleted", held, present)
	}
}

// TestAnAccountAlreadyGoneIsNotAFailure: the directory's own answer for a user
// it does not hold is the state the call is for, so it is not a failure — the
// user is gone, which is what the customer asked for. What is a failure is the
// directory's own, and the caller is told the request could not be answered
// rather than what the provider wrote (§7.2's rule, at a seam this service does
// not own).
func TestAnAccountAlreadyGoneIsNotAFailure(t *testing.T) {
	f := newDeletionFixture(t)
	directory := f.directory.directory()
	// Both shapes the API has for it: the status, and a 200 whose body names the
	// code — the two the history job's own sweep reads.
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "the status", status: http.StatusNotFound, body: ""},
		{name: "the code in the body", status: http.StatusOK, body: `{"error":{"message":"USER_NOT_FOUND"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.directory.status, f.directory.body = tc.status, tc.body
			if err := directory.delete(context.Background(), danaUID); err != nil {
				t.Errorf("deleting an account the directory does not hold answered %v, want nothing", err)
			}
		})
	}
	f.directory.status, f.directory.body = http.StatusInternalServerError, `{"error":{"message":"INTERNAL"}}`
	err := directory.delete(context.Background(), danaUID)
	if err == nil {
		t.Fatal("a directory that failed answered nothing, and the account is not gone")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("the failure reads %q, want the status and not the provider's own body", err)
	}
}
