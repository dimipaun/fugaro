// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// Invitations, their mail and the operations seed (portal plan P2.2; spec
// §Tenancy, F10, F40).
//
// The cases here drive the portal's routes through the handler, so what they
// read is what the SPA is answered, and they write records into the fixture's
// store only where the record is one no route of this area creates — the
// operations organisation's domain list is P5.1's to set, so a case seeds it
// the way P5.1's writer will.
//
// The fixture is the package's (keyservice_test.go) and the tenancy helpers are
// P2.1's (orgs_test.go): an invitation is a record of that tenancy's, and this
// file adds none of them.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The accounts these cases are about. None of them is a real address.
const (
	samUID   = "uid-sam"
	samEmail = "sam@example.com"
)

// The routes this area registers, spelled here as the SPA spells them: the
// members screen's own calls are `/orgs/{id}/invites` and the withdrawal beside
// them (ee/portal/src/screens/members/members.tsx), and the accepting call is
// what an invitation's mail leads to.
func apiOrgInvitesPathOf(id string) string { return "/api/orgs/" + id + "/invites" }

func apiOrgInvitePathOf(organisationID, invitationID string) string {
	return apiOrgInvitesPathOf(organisationID) + "/" + invitationID
}

func apiInviteAcceptPathOf(id string) string { return "/api/invites/" + id + "/accept" }

// inviteAs posts one invitation as the account the verifier answers.
func (f *fixture) inviteAs(organisationID, email, role string) *httptest.ResponseRecorder {
	f.t.Helper()
	body := `{"email":` + jsonString(f.t, email) + `,"role":` + jsonString(f.t, role) + `}`
	return f.apiAsCaller(http.MethodPost, apiOrgInvitesPathOf(organisationID), body)
}

// invited posts one invitation that is expected to be created, and answers it.
func (f *fixture) invited(organisationID, email, role string) WireInvite {
	f.t.Helper()
	rec := f.inviteAs(organisationID, email, role)
	if rec.Code != http.StatusCreated {
		f.t.Fatalf("inviting %s as %s answered %d: %s", email, role, rec.Code, rec.Body)
	}
	return readAnswer[WireInvite](f, rec)
}

// invitation reads one invitation out of the fixture's store.
func (f *fixture) invitation(id string) (WireInvite, bool) {
	f.t.Helper()
	return heldRecord[WireInvite](f, invitesCollection, id)
}

// invitationsOf is every invitation one organisation holds, read the way the
// list route reads them.
func (f *fixture) invitationsOf(organisationID string) []WireInvite {
	f.t.Helper()
	held, err := recordsWhere[WireInvite](context.Background(), f.docs, invitesCollection,
		Where{Field: "organisation_id", Value: organisationID})
	if err != nil {
		f.t.Fatalf("reading the invitations of %s: %v", organisationID, err)
	}
	return held
}

// acceptAs accepts one invitation as the account named, signed in the way named.
func (f *fixture) acceptAs(uid, email, provider, invitationID string) *httptest.ResponseRecorder {
	f.t.Helper()
	f.actsAs(uid, email, provider)
	return f.apiAsCaller(http.MethodPost, apiInviteAcceptPathOf(invitationID), "")
}

// accepted accepts one invitation as the account named and answers the
// invitation the route answered. The sign-in is Google's, which is what every
// organisation but a Microsoft-pinned reseller admits.
func (f *fixture) accepted(uid, email, invitationID string) WireInvite {
	f.t.Helper()
	rec := f.acceptAs(uid, email, signInGoogle, invitationID)
	if rec.Code != http.StatusOK {
		f.t.Fatalf("accepting %s as %s answered %d: %s", invitationID, email, rec.Code, rec.Body)
	}
	return readAnswer[WireInvite](f, rec)
}

// seedInvitationDomains writes the operations organisation's allowed email
// domain list, which is P5.1's record to write and this file's to stand in for
// (wire_ops.go's WireOpsLists, in the collection the deployment names).
func (f *fixture) seedInvitationDomains(organisationID string, domains ...string) {
	f.t.Helper()
	f.seed(f.cfg.TenantsCollection, organisationID, WireOpsLists{EmailDomains: domains})
}

// lastMail is the last message the intake sent, which is the one a case reads
// after a route that mails.
func (f *fixture) lastMail() Message {
	f.t.Helper()
	if len(f.mail.sent) == 0 {
		f.t.Fatal("the intake mailed nothing, and this case is about the mail it sends")
	}
	return f.mail.sent[len(f.mail.sent)-1]
}

// TestInvitationIsBoundToItsAddress: an invitation is an address and a role, and
// it is accepted by that address alone — the comparison goes through
// access.Member on both sides, so the invitation's spelling and the sign-in's
// are one member where the two fold the same way (F10). The role is on the
// invitation because it has to be chosen before the person exists, and the
// membership it becomes is the accepting account's own uid (F40).
func TestInvitationIsBoundToItsAddress(t *testing.T) {
	f := newFixture(t)
	org := f.signedUp(danaUID, danaEmail, "Example Ltd")
	f.actsAs(danaUID, danaEmail, signInGoogle)

	invitation := f.invited(org.ID, samEmail, roleMember)
	switch {
	case invitation.ID == "":
		t.Error("the invitation has no id, and it is what the accepting call names")
	case invitation.OrganisationID != org.ID:
		t.Errorf("the invitation names the organisation %q, want %q", invitation.OrganisationID, org.ID)
	case invitation.Email != samEmail:
		t.Errorf("the invitation is bound to %q, want %q", invitation.Email, samEmail)
	case invitation.Role != roleMember:
		t.Errorf("the invitation carries the role %q, want %q", invitation.Role, roleMember)
	case invitation.State != invitePending:
		t.Errorf("a new invitation is %q, want %q", invitation.State, invitePending)
	case invitation.InvitedBy != danaEmail:
		t.Errorf("the invitation was invited by %q, want the inviter's address %q: an owner reading the list asks who asked for this", invitation.InvitedBy, danaEmail)
	case !invitation.CreatedAt.Equal(f.now):
		t.Errorf("the invitation is dated %s, want %s", invitation.CreatedAt, f.now)
	case !invitation.ExpiresAt.Equal(f.now.Add(inviteTTL)):
		t.Errorf("the invitation expires %s, want %s: a window is what keeps a forgotten invitation to a departed colleague from being a way in months later", invitation.ExpiresAt, f.now.Add(inviteTTL))
	case invitation.AcceptedAt != nil:
		t.Errorf("the invitation records the acceptance %v, and nobody has accepted it", invitation.AcceptedAt)
	}
	if _, member := f.membership(org.ID, samUID); member {
		t.Error("creating an invitation made the invited account a member, and nothing before acceptance knows who the person is (F40)")
	}

	// The mail carries the link and never the invitation's state: what makes an
	// invitation usable is the sign-in link Firebase Auth sends to the address,
	// and the id in the mail is not a secret (invites.schema.json's own words).
	mail := f.lastMail()
	switch {
	case mail.To != samEmail:
		t.Errorf("the invitation was mailed to %q, want the invited address %q", mail.To, samEmail)
	case !strings.Contains(mail.Body, invitationURL(intakeOrigin, invitation.ID)):
		t.Errorf("the invitation's mail carries no link to %s: %s", invitationURL(intakeOrigin, invitation.ID), mail.Body)
	}

	// Another account accepting it is refused, and refused the same way an
	// invitation that is not there is: which ids exist is not something a
	// stranger learns from this service.
	rec := f.acceptAs(erinUID, erinEmail, signInGoogle, invitation.ID)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("accepting somebody else's invitation answered %d (%s), want 404: an invitation is accepted by its own address and by no other", rec.Code, rec.Body)
	}
	if code := refusal(f, rec); code != "not_found" {
		t.Errorf("the refusal carries the code %q, want not_found", code)
	}
	if _, member := f.membership(org.ID, erinUID); member {
		t.Error("an account the invitation was not bound to became a member by accepting it")
	}
	if held, _ := f.invitation(invitation.ID); held.State != invitePending {
		t.Errorf("the refused acceptance left the invitation %q, want it %q", held.State, invitePending)
	}

	// The bound address accepts it, spelled with a domain in another case: both
	// sides go through access.Member, which folds the domain and keeps the local
	// part byte for byte, so this is the same member and nobody else is.
	answer := f.accepted(samUID, "sam@EXAMPLE.com", invitation.ID)
	switch {
	case answer.State != inviteAccepted:
		t.Errorf("the accepted invitation is %q, want %q", answer.State, inviteAccepted)
	case answer.AcceptedAt == nil || !answer.AcceptedAt.Equal(f.now):
		t.Errorf("the acceptance is dated %v, want %s", answer.AcceptedAt, f.now)
	}
	member, found := f.membership(org.ID, samUID)
	switch {
	case !found:
		t.Fatal("accepting an invitation created no membership, so the person is not in the organisation they accepted an invitation to")
	case member.Role != roleMember:
		t.Errorf("the membership carries the role %q, want the invitation's %q", member.Role, roleMember)
	case member.Email != "sam@EXAMPLE.com":
		t.Errorf("the membership holds the address %q, want the one the sign-in proved", member.Email)
	}
	if stored, _ := f.invitation(invitation.ID); stored.State != inviteAccepted {
		t.Errorf("the store holds the invitation %q, want it %q: the state is what makes it single use", stored.State, inviteAccepted)
	}
}

// TestInvitationIsSingleUse: an accepted invitation is not a second membership,
// a second role or a way back in — accepting it again is refused, and the
// membership that exists is the one the first acceptance wrote (F40).
func TestInvitationIsSingleUse(t *testing.T) {
	f := newFixture(t)
	org := f.signedUp(danaUID, danaEmail, "Example Ltd")
	f.actsAs(danaUID, danaEmail, signInGoogle)
	invitation := f.invited(org.ID, samEmail, roleAdmin)

	first := f.accepted(samUID, samEmail, invitation.ID)

	again := f.acceptAs(samUID, samEmail, signInGoogle, invitation.ID)
	if again.Code != http.StatusConflict {
		t.Fatalf("accepting an accepted invitation answered %d (%s), want the refusal an invitation that is no longer pending earns", again.Code, again.Body)
	}
	if code := refusal(f, again); code != "invitation_not_pending" {
		t.Errorf("the refusal carries the code %q, want invitation_not_pending", code)
	}
	held, _ := f.invitation(invitation.ID)
	switch {
	case held.State != inviteAccepted:
		t.Errorf("the second acceptance left the invitation %q, want %q", held.State, inviteAccepted)
	case held.AcceptedAt == nil || !held.AcceptedAt.Equal(*first.AcceptedAt):
		t.Errorf("the second acceptance moved the acceptance to %v, want the first one's %v: the instant a person joined is not theirs to move", held.AcceptedAt, first.AcceptedAt)
	}
	member, _ := f.membership(org.ID, samUID)
	if !member.CreatedAt.Equal(f.now) {
		t.Errorf("the membership is dated %s, want the first acceptance's %s", member.CreatedAt, f.now)
	}

	// A revoked invitation is refused the same way, and revoking is the
	// organisation's owners' and admins' to do: what is left of it is a record
	// that somebody was asked, which is what a list shows.
	f.actsAs(danaUID, danaEmail, signInGoogle)
	second := f.invited(org.ID, erinEmail, roleMember)
	rec := f.apiAsCaller(http.MethodDelete, apiOrgInvitePathOf(org.ID, second.ID), "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("withdrawing an invitation answered %d (%s), want 204", rec.Code, rec.Body)
	}
	if withdrawn, _ := f.invitation(second.ID); withdrawn.State != inviteRevoked {
		t.Errorf("the withdrawn invitation is %q, want %q", withdrawn.State, inviteRevoked)
	}
	refused := f.acceptAs(erinUID, erinEmail, signInGoogle, second.ID)
	if refused.Code != http.StatusConflict {
		t.Errorf("accepting a withdrawn invitation answered %d (%s), want the refusal an invitation that is no longer pending earns", refused.Code, refused.Body)
	}
	if _, member := f.membership(org.ID, erinUID); member {
		t.Error("a withdrawn invitation still admitted the address it was bound to")
	}
}

// TestInvitationExpiresAndIsNotAcceptable: an invitation is a window and not an
// address that can come back months later. Past it the invitation is expired —
// a state and not a deletion, because a person reading the list is asking what
// happened — and the accepting call is refused (invites.schema.json's own
// reading of the state).
func TestInvitationExpiresAndIsNotAcceptable(t *testing.T) {
	f := newFixture(t)
	org := f.signedUp(danaUID, danaEmail, "Example Ltd")
	f.actsAs(danaUID, danaEmail, signInGoogle)
	invitation := f.invited(org.ID, samEmail, roleMember)

	f.advance(inviteTTL)

	if rec := f.acceptAs(samUID, samEmail, signInGoogle, invitation.ID); rec.Code != http.StatusGone {
		t.Fatalf("accepting an expired invitation answered %d (%s), want 410", rec.Code, rec.Body)
	} else if code := refusal(f, rec); code != "invitation_expired" {
		t.Errorf("the refusal carries the code %q, want invitation_expired", code)
	}
	if _, member := f.membership(org.ID, samUID); member {
		t.Error("an expired invitation admitted the address it was bound to")
	}

	// The list answers it expired, and the store still holds it as it was
	// written: what is expired is the state a reader is shown, derived from the
	// window and the clock, and never a rewrite of the record.
	f.actsAs(danaUID, danaEmail, signInGoogle)
	rec := f.apiAsCaller(http.MethodGet, apiOrgInvitesPathOf(org.ID), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("the invitations list answered %d (%s), want 200", rec.Code, rec.Body)
	}
	listed := readAnswer[WireInviteList](f, rec).Invites
	if len(listed) != 1 || listed[0].State != inviteExpired {
		t.Errorf("the list answers %+v, want the one invitation in the state expired", listed)
	}
	if held, _ := f.invitation(invitation.ID); held.State != invitePending {
		t.Errorf("the store holds the invitation %q, want it as it was written %q: the window is what expired, and the record says what was decided", held.State, invitePending)
	}
}

// TestInvitationsAreAnOwnersAndAdminsRead: who else belongs, and who else was
// asked, is one of the things a member decides nothing about (D2186, D2195): a
// plain member is refused the list and the invitation, and is admitted the
// moment their stored role says admin — with nothing else about the request
// changed.
func TestInvitationsAreAnOwnersAndAdminsRead(t *testing.T) {
	f := newFixture(t)
	org := f.signedUp(danaUID, danaEmail, "Example Ltd")
	f.actsAs(danaUID, danaEmail, signInGoogle)
	invitation := f.invited(org.ID, samEmail, roleMember)

	// The colleague is a member of the organisation and nobody has invited
	// them: their membership is P2.1's record and this file writes it because no
	// route of this area creates one for an account that already exists.
	f.seed(membershipsCollection, membershipID(org.ID, erinUID), WireMembership{
		OrganisationID: org.ID, UID: erinUID, Email: erinEmail, Role: roleMember, CreatedAt: f.now,
	})
	f.actsAs(erinUID, erinEmail, signInGoogle)

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "the invitation list", method: http.MethodGet, path: apiOrgInvitesPathOf(org.ID)},
		{name: "withdrawing an invitation", method: http.MethodDelete, path: apiOrgInvitePathOf(org.ID, invitation.ID)},
		{name: "inviting somebody", method: http.MethodPost, path: apiOrgInvitesPathOf(org.ID), body: `{"email":"new@example.com","role":"member"}`},
	} {
		t.Run(tc.name+" as a member", func(t *testing.T) {
			rec := f.apiAsCaller(tc.method, tc.path, tc.body)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s as a member answered %d (%s), want 403", tc.name, rec.Code, rec.Body)
			}
			if code := refusal(f, rec); code != "not_permitted" {
				t.Errorf("the refusal carries the code %q, want not_permitted, which is what the members screen branches on", code)
			}
		})
	}
	if held, _ := f.invitation(invitation.ID); held.State != invitePending {
		t.Errorf("a member's withdrawal left the invitation %q, want it %q", held.State, invitePending)
	}

	f.setRole(org.ID, erinUID, roleAdmin)
	if rec := f.apiAsCaller(http.MethodGet, apiOrgInvitesPathOf(org.ID), ""); rec.Code != http.StatusOK {
		t.Errorf("the invitation list as an admin answered %d (%s), want it: the role lives in the membership and not in the token", rec.Code, rec.Body)
	}
}

// TestOperationsInviteOutsideTheDomainListIsRefused: invitations to the
// operations organisation are limited to the email-domain list the owner sets
// (F9, P5.1), and a deployment that has set none invites nobody into it: the
// list is data an owner writes, and "nobody set one" is refused rather than read
// as "every domain".
func TestOperationsInviteOutsideTheDomainListIsRefused(t *testing.T) {
	f := newFixture(t)
	operations := f.seededOrganisation("org_operations", "Operations", orgTypeOperations, opsUID, opsEmail, roleOwner)
	f.actsAs(opsUID, opsEmail, signInGoogle)

	// No list at all: every domain is outside it.
	rec := f.inviteAs(operations.ID, "new@outside.example", roleMember)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("an invitation into the operations organisation with no domain list answered %d (%s), want the refusal", rec.Code, rec.Body)
	}
	if code := refusal(f, rec); code != "email_not_allowed" {
		t.Errorf("the refusal carries the code %q, want email_not_allowed", code)
	}
	if len(f.invitationsOf(operations.ID)) != 0 {
		t.Error("a refused invitation was written into the store anyway")
	}

	f.seedInvitationDomains(operations.ID, "fugaro.example")

	// Inside the list, with the domain in another case: the list and the address
	// are folded the same way on both sides, so nobody gets in by spelling the
	// domain differently.
	if invitation := f.invited(operations.ID, "someone@FUGARO.example", roleMember); invitation.Email != "someone@FUGARO.example" {
		t.Errorf("the invitation is bound to %q, want the address as it was asked for", invitation.Email)
	}
	// Outside it, whether it is another domain or a lookalike of a listed one.
	for _, address := range []string{"new@outside.example", "new@notfugaro.example", "new@fugaro.example.evil.example"} {
		t.Run(address, func(t *testing.T) {
			rec := f.inviteAs(operations.ID, address, roleMember)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("inviting %s answered %d (%s), want the refusal: the list is the domains it names and not a suffix of them", address, rec.Code, rec.Body)
			}
		})
	}

	// The list is the operations organisation's own: a customer organisation
	// invites whoever its owners want.
	customer := f.signedUp(danaUID, danaEmail, "Example Ltd")
	f.actsAs(danaUID, danaEmail, signInGoogle)
	if rec := f.inviteAs(customer.ID, "new@outside.example", roleMember); rec.Code != http.StatusCreated {
		t.Errorf("a customer organisation inviting an address outside the operations list answered %d (%s), want it: the list limits the operations organisation's invitations", rec.Code, rec.Body)
	}
}

// TestResellerInviteNeedsSSO: the SSO rule follows the membership a request USES
// (D2207, F10), and an invitation into a reseller organisation is one of those
// requests twice over: the owner who writes it is acting through their reseller
// membership, and the acceptance is the act that creates a reseller membership.
// An email-link sign-in is refused at both, and a Google one is admitted.
func TestResellerInviteNeedsSSO(t *testing.T) {
	f := newFixture(t)
	reseller := f.seededOrganisation("org_reseller", "Partner Ltd", orgTypeReseller, sellerUID, sellerMail, roleOwner)

	f.actsAs(sellerUID, sellerMail, signInEmailLink)
	rec := f.inviteAs(reseller.ID, "colleague@partner.example", roleMember)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("a reseller owner by email link inviting answered %d (%s), want the SSO refusal", rec.Code, rec.Body)
	}
	if code := refusal(f, rec); code != "sso_required" {
		t.Errorf("the refusal carries the code %q, want sso_required", code)
	}
	if len(f.invitationsOf(reseller.ID)) != 0 {
		t.Error("an invitation the SSO rule refused was written into the store anyway")
	}

	f.actsAs(sellerUID, sellerMail, signInGoogle)
	invitation := f.invited(reseller.ID, "colleague@partner.example", roleMember)

	// The acceptance needs it too, and on the accepting side: the membership
	// being created is a reseller membership, so the sign-in that creates it is
	// one the rule admits.
	refused := f.acceptAs("uid-colleague", "colleague@partner.example", signInEmailLink, invitation.ID)
	if refused.Code != http.StatusForbidden {
		t.Fatalf("accepting a reseller invitation by email link answered %d (%s), want the SSO refusal", refused.Code, refused.Body)
	}
	if _, member := f.membership(reseller.ID, "uid-colleague"); member {
		t.Error("an email-link sign-in became a reseller organisation's member by accepting an invitation")
	}
	if held, _ := f.invitation(invitation.ID); held.State != invitePending {
		t.Errorf("the refused acceptance left the invitation %q, want it %q", held.State, invitePending)
	}

	f.accepted("uid-colleague", "colleague@partner.example", invitation.ID)
	if member, found := f.membership(reseller.ID, "uid-colleague"); !found || member.Role != roleMember {
		t.Errorf("the accepted reseller membership is %+v (found %v), want the invitation's role", member, found)
	}
}

// TestAnotherOrgsInvitationsAreRefused: an invitation is one organisation's
// record, and an owner or an admin of another organisation is a stranger to it —
// listing it, withdrawing it and inviting into it are all refused, and refused
// the same way an organisation that is not there is (P2.1's own answer, which is
// what keeps the portal's customer list out of a stranger's hands). The
// operations membership reads it, as it reads everything, and that read is the
// one the log is written for (D456, F9).
func TestAnotherOrgsInvitationsAreRefused(t *testing.T) {
	f := newFixture(t)
	first := f.signedUp(danaUID, danaEmail, "Example Ltd")
	second := f.signedUp(erinUID, erinEmail, "Other Ltd")
	f.actsAs(erinUID, erinEmail, signInGoogle)
	invitation := f.invited(second.ID, samEmail, roleMember)

	f.actsAs(danaUID, danaEmail, signInGoogle)
	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "listing another organisation's invitations", method: http.MethodGet, path: apiOrgInvitesPathOf(second.ID)},
		{name: "inviting into another organisation", method: http.MethodPost, path: apiOrgInvitesPathOf(second.ID), body: `{"email":"new@example.com","role":"member"}`},
		{name: "withdrawing another organisation's invitation", method: http.MethodDelete, path: apiOrgInvitePathOf(second.ID, invitation.ID)},
		// The invitation id is the API's own and it is what the second route
		// names: an owner acting through their own organisation cannot reach
		// another's invitation by naming their own organisation and it.
		{name: "withdrawing another organisation's invitation through one's own", method: http.MethodDelete, path: apiOrgInvitePathOf(first.ID, invitation.ID)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.apiAsCaller(tc.method, tc.path, tc.body)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s answered %d (%s), want 404: another organisation's records are not this caller's", tc.name, rec.Code, rec.Body)
			}
			if code := refusal(f, rec); code != "not_found" {
				t.Errorf("the refusal carries the code %q, want not_found", code)
			}
		})
	}
	if held, _ := f.invitation(invitation.ID); held.State != invitePending {
		t.Errorf("the refused withdrawal left the invitation %q, want it %q", held.State, invitePending)
	}

	// The operations membership reads across, and the read is written to the log
	// before it is answered (P2.1's accessTo).
	f.seededOrganisation("org_operations", "Operations", orgTypeOperations, opsUID, opsEmail, roleOwner)
	f.actsAs(opsUID, opsEmail, signInGoogle)
	rec := f.apiAsCaller(http.MethodGet, apiOrgInvitesPathOf(second.ID), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("the operations membership reading another organisation's invitations answered %d (%s), want them", rec.Code, rec.Body)
	}
	if listed := readAnswer[WireInviteList](f, rec).Invites; len(listed) != 1 || listed[0].ID != invitation.ID {
		t.Errorf("the operations read answered %+v, want the invitation that organisation holds", listed)
	}
	audited := false
	for _, entry := range f.auditEntries() {
		if entry.Action == actionInvitationList && entry.OrganisationID != nil && *entry.OrganisationID == second.ID {
			audited = true
		}
	}
	if !audited {
		t.Error("the operations read of another organisation's invitations left no audit line, and every read across organisations is written to the log (D456)")
	}

	// The invited address still accepts the invitation nobody withdrew.
	f.accepted(samUID, samEmail, invitation.ID)
}

// TestSeedIsAnInvitationBoundAtFirstSignIn: the operations organisation is
// seeded, never signed up to: SeedOperations writes it, its own first
// invitation and nothing else — no membership, no terms — and the person the
// invitation names becomes the operations organisation's first owner when that
// address signs in (F40, D2187). The invitation is mailed to the address, so the
// person the seed names has the link that leads to it.
func TestSeedIsAnInvitationBoundAtFirstSignIn(t *testing.T) {
	f := newFixture(t)

	org, err := f.svc.SeedOperations(context.Background(), opsEmail)
	if err != nil {
		t.Fatalf("seeding the operations organisation: %v", err)
	}
	switch {
	case org.ID == "":
		t.Error("the seeded organisation has no id")
	case org.Type != orgTypeOperations:
		t.Errorf("the seed wrote a %q organisation, want an operations one", org.Type)
	case org.Terms != nil:
		t.Errorf("the seeded organisation records the acceptance %+v, and nobody signed up to it (P2.1)", org.Terms)
	case !org.CreatedAt.Equal(f.now):
		t.Errorf("the seeded organisation is dated %s, want %s", org.CreatedAt, f.now)
	}
	if _, member := f.membership(org.ID, opsUID); member {
		t.Error("the seed wrote a membership, and the operations organisation's first owner is bound at first sign-in (F40)")
	}

	invitations := f.invitationsOf(org.ID)
	if len(invitations) != 1 {
		t.Fatalf("the seed wrote %d invitation(s), want one", len(invitations))
	}
	seeded := invitations[0]
	switch {
	case seeded.Email != opsEmail:
		t.Errorf("the seeded invitation is bound to %q, want the address the seed names %q", seeded.Email, opsEmail)
	case seeded.Role != roleOwner:
		t.Errorf("the seeded invitation carries the role %q, want owner: the seed invites the operations organisation's first owner", seeded.Role)
	case seeded.State != invitePending:
		t.Errorf("the seeded invitation is %q, want %q", seeded.State, invitePending)
	case !seeded.ExpiresAt.Equal(f.now.Add(inviteTTL)):
		t.Errorf("the seeded invitation expires %s, want %s", seeded.ExpiresAt, f.now.Add(inviteTTL))
	case seeded.InvitedBy != opsEmail:
		t.Errorf("the seeded invitation was invited by %q, want the address the seed names: the operator who runs the seed is who asked for it", seeded.InvitedBy)
	}
	if mail := f.lastMail(); mail.To != opsEmail || !strings.Contains(mail.Body, invitationURL(intakeOrigin, seeded.ID)) {
		t.Errorf("the seed mailed %+v, want the invitation's own link to %s", mail, opsEmail)
	}

	// The seeded address signs in and accepts, and becomes the first owner —
	// which is the one membership the seed did not write.
	accepted := f.accepted(opsUID, opsEmail, seeded.ID)
	if accepted.State != inviteAccepted {
		t.Errorf("the accepted invitation is %q, want %q", accepted.State, inviteAccepted)
	}
	member, found := f.membership(org.ID, opsUID)
	if !found || member.Role != roleOwner {
		t.Errorf("the operations organisation's first member is %+v (found %v), want the owner the seed invited", member, found)
	}
}

// TestSeedIsRefusedOnceSeeded: the seed is one act and not a way to make a
// second operations organisation. A deployment that is already seeded refuses,
// and what it holds is what it held: running it again is a mistake an operator
// is told about rather than a second organisation whose members would read
// across every customer's.
func TestSeedIsRefusedOnceSeeded(t *testing.T) {
	f := newFixture(t)

	first, err := f.svc.SeedOperations(context.Background(), opsEmail)
	if err != nil {
		t.Fatalf("seeding the operations organisation: %v", err)
	}
	if _, err := f.svc.SeedOperations(context.Background(), "somebody@fugaro.example"); err == nil {
		t.Fatal("a second seed was accepted, and two operations organisations would be two teams reading across every customer's")
	}
	if orgs := f.organisations(); len(orgs) != 1 || orgs[0].ID != first.ID {
		t.Errorf("the store holds %+v, want the one organisation the first seed wrote", orgs)
	}
	if held := f.invitationsOf(first.ID); len(held) != 1 || held[0].Email != opsEmail {
		t.Errorf("the refused seed wrote %+v, want the invitation the first seed wrote and nothing else", held)
	}
}

// TestSeedRefusesAnAddressThatIsNoMember: the seed's address is the one thing a
// person types into a one-time command, and an address that is no member of this
// tree is refused before anything is written — a member is what an invitation is
// compared through, and one that cannot be folded could never be accepted.
func TestSeedRefusesAnAddressThatIsNoMember(t *testing.T) {
	f := newFixture(t)

	if _, err := f.svc.SeedOperations(context.Background(), "not an address"); err == nil {
		t.Fatal("the seed accepted an address access.Member refuses, and an invitation bound to one nobody can equal is a deployment with no way in")
	}
	if orgs := f.organisations(); len(orgs) != 0 {
		t.Errorf("the refused seed wrote %+v, want nothing", orgs)
	}
	if len(f.mail.sent) != 0 {
		t.Error("the refused seed mailed something")
	}
}

// TestResendMailerSendsNoKey: the mail path hands the provider a message and
// nothing else. The key it authenticates with is the provider's own API key,
// carried in the Authorization header and nowhere else — not in the URL, not in
// the body, not in the process's log — and the mail's own text is what the
// service built for an invitation, which is a link and never a key (D2182,
// F3). A provider that is not there, and one that answers a refusal, are both an
// error to the caller: an invitation nobody received must not read as one that
// was sent.
func TestResendMailerSendsNoKey(t *testing.T) {
	key := []byte("fake-resend-api-key-0123456789")
	invitation := WireInvite{
		ID: "inv_seeded", OrganisationID: "org_operations", Email: samEmail,
		Role: roleOwner, State: invitePending, InvitedBy: opsEmail,
		CreatedAt: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC),
		ExpiresAt: time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC),
	}
	message := invitationMail(intakeOrigin, "Example Ltd", invitation, inviteTTL)

	var asked *http.Request
	var body []byte
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		held, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading the mail provider's request: %v", err)
		}
		asked, body = r.Clone(r.Context()), held
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"mail_1"}`)
	}))
	defer provider.Close()

	mailer, err := NewResendMailer(key, "invitations@fugaro.example", provider.Client())
	if err != nil {
		t.Fatalf("building the mailer: %v", err)
	}
	mailer.endpoint = provider.URL

	if err := mailer.Send(context.Background(), message); err != nil {
		t.Fatalf("sending an invitation: %v", err)
	}
	if asked == nil {
		t.Fatal("the mail provider was never called, so nothing was sent")
	}
	if got, want := asked.Header.Get("Authorization"), "Bearer "+string(key); got != want {
		t.Errorf("the request authenticates with %q, want the provider's key in the Authorization header", got)
	}
	if strings.Contains(asked.URL.String(), string(key)) {
		t.Error("the key is in the request's URL, and a credential in a URL is a credential in every log along the way")
	}
	if strings.Contains(string(body), string(key)) {
		t.Error("the key is in the request's body, and the body is the mail")
	}

	var sent struct {
		From    string   `json:"from"`
		To      []string `json:"to"`
		Subject string   `json:"subject"`
		Text    string   `json:"text"`
	}
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("the provider's body is not the shape Resend reads (%q): %v", body, err)
	}
	switch {
	case sent.From != "invitations@fugaro.example":
		t.Errorf("the mail is from %q, want the deployment's own sender", sent.From)
	case len(sent.To) != 1 || sent.To[0] != message.To:
		t.Errorf("the mail goes to %v, want the one recipient %q", sent.To, message.To)
	case sent.Subject != message.Subject:
		t.Errorf("the mail's subject is %q, want the service's own constant %q", sent.Subject, message.Subject)
	case sent.Text != message.Body:
		t.Errorf("the mail's text is %q, want the body the service built: the mailer is a transport and not a renderer", sent.Text)
	}
	if !strings.Contains(sent.Text, invitationURL(intakeOrigin, invitation.ID)) {
		t.Errorf("the invitation's mail carries no link, and the link is the whole of what leads the person to it: %s", sent.Text)
	}

	// A provider that answers a refusal is an error to the caller.
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"message":"the address is not allowed"}`)
	}))
	defer refusing.Close()
	mailer.endpoint = refusing.URL
	if err := mailer.Send(context.Background(), message); err == nil {
		t.Error("a provider that refused the mail answered no error, and an invitation nobody received reads as one that was sent")
	}

	// A provider that is not there at all is the same error, and neither failure
	// prints the key: what reaches the log is a failure and not a credential.
	var logged strings.Builder
	previous := log.Writer()
	log.SetOutput(&logged)
	defer log.SetOutput(previous)
	mailer.endpoint = provider.URL
	mailer.doer = refusingDoer{err: errNoProvider}
	if err := mailer.Send(context.Background(), message); err == nil {
		t.Error("a mail path that could not reach the provider answered no error")
	}
	if strings.Contains(logged.String(), string(key)) {
		t.Error("the mail path wrote the provider's key to the log")
	}
}

// errNoProvider is a mail path that cannot be reached at all, which is the
// failure a deployment meets when the provider is down or its DNS is wrong. It
// carries nothing about the call and stands for no real error.
var errNoProvider = errors.New("the mail provider is not reachable in this case")

// refusingDoer is the HTTP seam answering a transport failure, so a case reads
// what the mailer does when the call never lands.
type refusingDoer struct{ err error }

func (r refusingDoer) Do(*http.Request) (*http.Response, error) { return nil, r.err }

// TestResendMailerRefusesWhatItCannotSend: the two things a deployment gets
// wrong are the provider's key and its own sender, and both are refused where
// the mailer is built rather than at the first invitation, which is a person
// waiting for a mail that will never arrive.
func TestResendMailerRefusesWhatItCannotSend(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  []byte
		from string
	}{
		{name: "no key", key: nil, from: "invitations@fugaro.example"},
		{name: "a key of whitespace", key: []byte("   "), from: "invitations@fugaro.example"},
		{name: "no sender", key: []byte("fake-resend-api-key-0123456789"), from: "  "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewResendMailer(tc.key, tc.from, nil); err == nil {
				t.Errorf("the mailer was built with %s, want the deployment refused: every request it made would be refused by the provider", tc.name)
			}
		})
	}
	if mailer, err := NewResendMailer([]byte("fake-resend-api-key-0123456789"), "invitations@fugaro.example", nil); err != nil || mailer.doer == nil {
		t.Errorf("building the mailer with no HTTP client answered (%v, %v), want the default one", mailer, err)
	}
}
