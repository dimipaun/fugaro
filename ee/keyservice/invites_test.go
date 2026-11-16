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

	"github.com/dimipaun/fugaro/internal/access"
)

// The accounts these cases are about. None of them is a real address.
const (
	samUID   = "uid-sam"
	samEmail = "sam@example.com"
)

// portalOriginHere is the portal's own origin for the case that reads the mail
// path on its own, with no fixture: an invitation's link is built from the
// portal's origin (portalOrigin), so a message handed to the mailer carries a
// link to a page the portal serves.
const portalOriginHere = "https://app.fugaro.example"

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

// acceptAsClaiming accepts one invitation as the account whose token CLAIMED the
// address named, with the caller spelled the way P1.3's verifier answers it
// (signedInAsClaiming): what an invitation is compared against is the member of
// the claimed address and not the folded one mcpauth hands back.
func (f *fixture) acceptAsClaiming(uid, claimed, provider, invitationID string) *httptest.ResponseRecorder {
	f.t.Helper()
	f.signedInAsClaiming(uid, claimed, provider)
	return f.apiAsCaller(http.MethodPost, apiInviteAcceptPathOf(invitationID), "")
}

// signedInAsClaiming makes the fixture's verifier answer the way the seam does
// for a token that claimed this address: mcpauth's fold of the WHOLE address as
// the Email and access.Member of the claimed spelling as the Member. actsAs
// keeps the address as it was given — one spelling for both fields — which is
// what a case that is not about the fold wants, and this is the case that is.
func (f *fixture) signedInAsClaiming(uid, claimed, provider string) {
	f.t.Helper()
	member, err := access.Member(claimed)
	if err != nil {
		f.t.Fatalf("building the member of the claimed address %q: %v", claimed, err)
	}
	f.signIn.caller = Caller{
		UID:      uid,
		Email:    strings.ToLower(claimed),
		Member:   member,
		Provider: provider,
		AuthTime: f.now,
	}
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

// portalOriginOf is the fixture deployment's portal origin, which is what an
// invitation's link is built from, and it is read the way the service reads it
// so that a case about a link is a case about the origin the mail really names.
func portalOriginOf(f *fixture) string {
	f.t.Helper()
	origin, err := portalOrigin(f.cfg.URL)
	if err != nil {
		f.t.Fatalf("reading the portal's origin from the deployment's intake origin %q: %v", f.cfg.URL, err)
	}
	return origin
}

// linkIn is whether one message's body carries a link to one invitation, checked
// against the portal origin rather than the intake's: the page the link names is
// served by the portal, so a mail built from the intake's own origin is a link
// to a 404 and no invitation can be accepted through it.
func linkIn(f *fixture, mail Message, invitationID string) bool {
	f.t.Helper()
	return strings.Contains(mail.Body, invitationURL(portalOriginOf(f), invitationID))
}

// auditedInvitation is whether the log holds one invitation action against one
// organisation and one invitation: it is D456's line for the acts that write,
// and for the operations membership acting in another organisation it is the
// line beside the read across that got it there.
func (f *fixture) auditedInvitation(action, organisationID, invitationID string) bool {
	f.t.Helper()
	for _, entry := range f.auditEntries() {
		if entry.Action != action || entry.TargetID == nil || *entry.TargetID != invitationID {
			continue
		}
		if entry.OrganisationID != nil && *entry.OrganisationID == organisationID {
			return true
		}
	}
	return false
}

// TestTheInvitationLinkPointsAtThePortal: an invitation's link names the page
// that accepts it, and that page is the portal's. The intake answers on `keys.`
// plus the site's canonical host (§3.1) and serves no portal route, so a mail
// built from the deployment's intake origin is a link to a 404 and no invitation
// can be accepted through it (F40). The portal's origin is read from that one
// configured origin — the pair is a deployment convention, spec §7 — and a
// deployment it cannot be read from refuses the invitation rather than writing
// one and mailing a link nobody can follow.
func TestTheInvitationLinkPointsAtThePortal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		intake string
		want   string
	}{
		{name: "the site's canonical host", intake: "https://keys.fugaro.dev", want: "https://app.fugaro.dev"},
		{name: "the staging host", intake: "https://keys.staging.fugaro.dev", want: "https://app.staging.fugaro.dev"},
		{name: "an origin with a trailing slash", intake: "https://keys.fugaro.dev/", want: "https://app.fugaro.dev"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			origin, err := portalOrigin(tc.intake)
			if err != nil {
				t.Fatalf("reading the portal's origin from %q: %v", tc.intake, err)
			}
			if origin != tc.want {
				t.Errorf("the portal's origin is %q, want %q", origin, tc.want)
			}
			if link := invitationURL(origin, "inv_1"); !strings.HasPrefix(link, tc.want+routeInvitation) {
				t.Errorf("an invitation's link is %q, want it under %s%s", link, tc.want, routeInvitation)
			}
		})
	}

	for _, tc := range []struct{ name, intake string }{
		{name: "an intake on no label of its own", intake: "https://intake.fugaro.dev"},
		{name: "the site's canonical host itself", intake: "https://fugaro.dev"},
		{name: "an origin with no scheme", intake: "keys.fugaro.dev"},
		{name: "nothing at all", intake: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if origin, err := portalOrigin(tc.intake); err == nil {
				t.Errorf("an intake origin of %q answered the portal's origin %q, want the refusal: the portal's origin is `%s.` plus the site's canonical host and nothing else",
					tc.intake, origin, portalHostLabel)
			}
		})
	}

	// A deployment whose configured origin is not that shape: no invitation is
	// written and none is mailed, because the link it would carry leads nowhere.
	f := newFixtureWith(t, Production, func(b *fixture) { b.cfg.URL = "https://intake.fugaro.dev" })
	customer := f.signedUp(danaUID, danaEmail, "Example Ltd")
	f.actsAs(danaUID, danaEmail, signInGoogle)

	rec := f.inviteAs(customer.ID, samEmail, roleMember)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("inviting from a deployment whose portal origin cannot be read answered %d (%s), want 500", rec.Code, rec.Body)
	}
	if held := f.invitationsOf(customer.ID); len(held) != 0 {
		t.Errorf("the refused invitation was written anyway: %+v", held)
	}
	if len(f.mail.sent) != 0 {
		t.Error("the refused invitation mailed a link to a page nobody serves")
	}

	// The seed reads the same origin and refuses the same deployment, before it
	// writes the operations organisation: the mail is the whole of what leads
	// its first owner to it.
	if _, err := f.svc.SeedOperations(context.Background(), opsEmail); err == nil {
		t.Error("the seed accepted a deployment whose invitation link cannot be built")
	}
	if orgs := f.organisations(); len(orgs) != 1 || orgs[0].ID != customer.ID {
		t.Errorf("the refused seed wrote %+v, want the one organisation that was already there", orgs)
	}
	if len(f.mail.sent) != 0 {
		t.Error("the refused seed mailed a link to a page nobody serves")
	}
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
	// The link names the PORTAL's own origin, which is where the page is served
	// (TestTheInvitationLinkPointsAtThePortal is the rule's own case).
	mail := f.lastMail()
	switch {
	case mail.To != samEmail:
		t.Errorf("the invitation was mailed to %q, want the invited address %q", mail.To, samEmail)
	case !linkIn(f, mail, invitation.ID):
		t.Errorf("the invitation's mail carries no link to %s: %s", invitationURL(portalOriginOf(f), invitation.ID), mail.Body)
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
	// Who invited whom into which organisation, who joined when, and what was
	// withdrawn are the lines the log is read for (D456): an invitation nobody
	// can account for months later is an invitation nobody can account for.
	if !f.auditedInvitation(actionInvitationCreate, org.ID, invitation.ID) {
		t.Error("the invitation left no audit line, and every invitation action is written to the log (D456)")
	}
	if !f.auditedInvitation(actionInvitationAccept, org.ID, invitation.ID) {
		t.Error("the acceptance left no audit line, and every invitation action is written to the log (D456)")
	}

	// An address with a capital in its local part. What the invitation holds is
	// the address as it was typed and what the sign-in answers is mcpauth's
	// whole-address fold BESIDE the member of the claimed spelling, so the two
	// are one member where access.Member folds them the same way — and a sign-in
	// claiming another spelling of that local part is another member, refused
	// like a stranger's. Comparing the invitation's address with the folded
	// address gets both halves wrong: the person it was mailed to meets a 404,
	// and the other spelling is admitted.
	const claimed = "Sam.Other@example.com"
	f.actsAs(danaUID, danaEmail, signInGoogle)
	capitalised := f.invited(org.ID, claimed, roleMember)
	if capitalised.Email != claimed {
		t.Errorf("the invitation is bound to %q, want the address as it was asked for %q: the mail goes to what the inviter typed", capitalised.Email, claimed)
	}
	rec = f.acceptAsClaiming(samUID, "SAM.OTHER@example.com", signInGoogle, capitalised.ID)
	if rec.Code != http.StatusNotFound {
		t.Errorf("a sign-in claiming %q accepted an invitation for %q (%d, %s), want 404: two spellings access.Member calls two people are two people (F31)", "SAM.OTHER@example.com", claimed, rec.Code, rec.Body)
	}
	if held, _ := f.invitation(capitalised.ID); held.State != invitePending {
		t.Errorf("the refused acceptance left the invitation %q, want it %q", held.State, invitePending)
	}
	if accepted := f.acceptAsClaiming(samUID, claimed, signInGoogle, capitalised.ID); accepted.Code != http.StatusOK {
		t.Errorf("the address the invitation was mailed to accepting it answered %d (%s), want it: the local part is kept byte for byte on both sides", accepted.Code, accepted.Body)
	} else if answer := readAnswer[WireInvite](f, accepted); answer.State != inviteAccepted || answer.AcceptedAt == nil {
		t.Errorf("the accepted invitation is %+v, want it accepted and dated", answer)
	}

	// The other direction of the same fold: an invitation written in one
	// spelling of a local part is no admission for another spelling of it, which
	// is what comparing the invitation's address with mcpauth's folded one would
	// admit — both sides lower-cased, two members made into one (F31).
	const lower = "sam.third@example.com"
	f.actsAs(danaUID, danaEmail, signInGoogle)
	third := f.invited(org.ID, lower, roleMember)
	if rec := f.acceptAsClaiming(samUID, "SAM.THIRD@example.com", signInGoogle, third.ID); rec.Code != http.StatusNotFound {
		t.Errorf("a sign-in claiming %q accepted an invitation for %q (%d, %s), want 404: access.Member keeps the local part byte for byte, so those are two people", "SAM.THIRD@example.com", lower, rec.Code, rec.Body)
	}
	if accepted := f.acceptAsClaiming(samUID, lower, signInGoogle, third.ID); accepted.Code != http.StatusOK {
		t.Errorf("the address the invitation was mailed to accepting it answered %d (%s), want it", accepted.Code, accepted.Body)
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
	if !f.auditedInvitation(actionInvitationRevoke, org.ID, second.ID) {
		t.Error("the withdrawal left no audit line, and every invitation action is written to the log (D456)")
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

// TestInvitationsAreRateLimited: an invitation is mail to an address the caller
// chooses, sent from the deployment's own verified sender and carrying text the
// caller wrote, and the door that reaches it is a rate (F35): every other path
// that mails has one, for the reason throttle.go's header states — so the forms
// cannot be used to mail-bomb a third party's address. The allowance is the
// organisation's, not one caller's, and it is spent by an invitation that would
// be mailed and by nothing else: a body the route refuses for its shape is not
// an invitation and does not spend one.
func TestInvitationsAreRateLimited(t *testing.T) {
	f := newFixtureWith(t, Production, func(b *fixture) {
		b.cfg.InviteRate = Limits{Requests: 2, Window: time.Hour}
	})
	org := f.signedUp(danaUID, danaEmail, "Example Ltd")
	other := f.signedUp(erinUID, erinEmail, "Other Ltd")
	f.actsAs(danaUID, danaEmail, signInGoogle)

	// A request the route refuses for its own shape is not an invitation, so the
	// allowance is still whole.
	if rec := f.inviteAs(org.ID, "one@example.com", "chief"); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("an invitation at a role no membership holds answered %d (%s), want the refusal", rec.Code, rec.Body)
	}

	for _, address := range []string{"one@example.com", "two@example.com"} {
		if created := f.invited(org.ID, address, roleMember); created.Email != address {
			t.Errorf("the invitation is bound to %q, want %q", created.Email, address)
		}
	}
	mailed := len(f.mail.sent)

	rec := f.inviteAs(org.ID, "three@example.com", roleMember)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("the invitation past the organisation's rate answered %d (%s), want 429", rec.Code, rec.Body)
	}
	if code := refusal(f, rec); code != "rate_limited" {
		t.Errorf("the refusal carries the code %q, want rate_limited", code)
	}
	if held := f.invitationsOf(org.ID); len(held) != 2 {
		t.Errorf("the store holds %d invitation(s), want the two the rate allowed: a refused request writes nothing", len(held))
	}
	if len(f.mail.sent) != mailed {
		t.Error("the refused invitation was mailed anyway, and the rate is there to bound the mail")
	}

	// The allowance is the organisation's and not one caller's: a second admin
	// of it shares the one bucket, and another organisation's admins have their
	// own.
	f.seed(membershipsCollection, membershipID(org.ID, erinUID), WireMembership{
		OrganisationID: org.ID, UID: erinUID, Email: erinEmail, Role: roleAdmin, CreatedAt: f.now,
	})
	f.actsAs(erinUID, erinEmail, signInGoogle)
	if rec := f.inviteAs(org.ID, "four@example.com", roleMember); rec.Code != http.StatusTooManyRequests {
		t.Errorf("a second admin of the same organisation answered %d (%s), want the refusal: the allowance is the organisation's and not one caller's", rec.Code, rec.Body)
	}
	if rec := f.inviteAs(other.ID, "four@example.com", roleMember); rec.Code != http.StatusCreated {
		t.Errorf("another organisation's admin answered %d (%s), want it: one organisation's allowance is not another's", rec.Code, rec.Body)
	}

	// And the rate is a window: the allowance is whole again once the hour it
	// was spent in has passed.
	f.advance(time.Hour)
	f.actsAs(danaUID, danaEmail, signInGoogle)
	if rec := f.inviteAs(org.ID, "five@example.com", roleMember); rec.Code != http.StatusCreated {
		t.Errorf("an invitation in the window after the one the allowance was spent in answered %d (%s), want it", rec.Code, rec.Body)
	}

	// A deployment that names no rate is refused where it is built, as one that
	// names no intake rate is: an organisation with no rate is one whose owners
	// can be made to mail on demand.
	cfg := f.svc.cfg
	cfg.InviteRate = Limits{}
	if _, err := New(cfg, *f.deps); err == nil {
		t.Error("a deployment that names no invitation rate was built, and an internet-facing door that mails without a rate is one a stranger sets")
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

	// An admin invites people, and makes no owners. An invitation's role is the
	// membership it becomes when it is accepted (F40), so `owner` asked for by an
	// admin is an admin conferring what D2186 and D2195 give an owner alone — and
	// the invitation is the route by which a role arrives before the person does,
	// which is why the rule holds here and not only where an existing membership
	// is moved. The second address is the one an admin could hold as a second
	// one of their own, which is the same act with nobody but them in the way of
	// accepting it.
	for _, address := range []string{"new@example.com", "erin.other@example.com"} {
		rec := f.inviteAs(org.ID, address, roleOwner)
		if rec.Code != http.StatusForbidden {
			t.Errorf("an admin inviting %s as an owner answered %d (%s), want 403: a role is moved by an owner and by nothing else", address, rec.Code, rec.Body)
		}
		if code := refusal(f, rec); code != "not_permitted" {
			t.Errorf("the refusal carries the code %q, want not_permitted, which is what the members screen branches on", code)
		}
	}
	if held := f.invitationsOf(org.ID); len(held) != 1 || held[0].ID != invitation.ID {
		t.Errorf("the refused owner invitations were written: %+v", held)
	}
	// An admin still invites what an admin may, and the owner still makes owners:
	// what refuses is the role held, not the role's name in the body.
	if created := f.invited(org.ID, "colleague@example.com", roleAdmin); created.Role != roleAdmin {
		t.Errorf("an admin inviting an admin wrote the role %q, want %q", created.Role, roleAdmin)
	}
	f.actsAs(danaUID, danaEmail, signInGoogle)
	if created := f.invited(org.ID, "second@example.com", roleOwner); created.Role != roleOwner {
		t.Errorf("the owner inviting an owner wrote the role %q, want %q", created.Role, roleOwner)
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

// TestAnOperationsMemberReadsButDecidesNothing: reading across organisations is
// what the operations membership is for, and the invitation list keeps it — it is
// a read the operations team makes everywhere and it is written to the log
// (D456, F9). Inviting somebody into another organisation and withdrawing an
// invitation there are not reads: they are decisions D2186 and D2195 give that
// organisation's owners and admins, and the operations membership is not a
// standing inside the organisation it reaches, so it carries neither act — a
// plain member decides nothing there, and neither does an operations owner. The
// owner is the case that matters, because the membership an accepted invitation
// becomes is the accepting account's (F40): an operations account that invited
// its own address as an owner would hold an owner membership of a customer's
// organisation, cross F5's line, and make its later acts through that membership
// — the customer's own, and unwritten to the log (D456, F9).
func TestAnOperationsMemberReadsButDecidesNothing(t *testing.T) {
	f := newFixture(t)
	customer := f.signedUp(danaUID, danaEmail, "Example Ltd")
	f.actsAs(danaUID, danaEmail, signInGoogle)
	invitation := f.invited(customer.ID, samEmail, roleMember)

	operations := f.seededOrganisation("org_operations", "Operations", orgTypeOperations, opsUID, opsEmail, roleMember)
	f.actsAs(opsUID, opsEmail, signInGoogle)

	// Neither role decides anything in another organisation, and the owner is
	// the one that matters: the operations membership is what got the caller to
	// the read and is not a standing inside the organisation it reaches, so an
	// operations owner is refused there exactly as a plain member is. What an
	// admitted act would do is the reason — the invitation's membership is filed
	// under the accepting account (F40), so an operations account that invited
	// its own address as an owner would hold an owner membership of a customer's
	// organisation (F5) and make every later act of theirs through it, which is
	// that organisation's own membership and leaves no audit line (D456, F9).
	for _, role := range []string{roleMember, roleOwner} {
		f.setRole(operations.ID, opsUID, role)

		rec := f.apiAsCaller(http.MethodGet, apiOrgInvitesPathOf(customer.ID), "")
		if rec.Code != http.StatusOK {
			t.Fatalf("an operations %s reading another organisation's invitations answered %d (%s), want them: reading across organisations is what the operations membership is for", role, rec.Code, rec.Body)
		}
		if listed := readAnswer[WireInviteList](f, rec).Invites; len(listed) != 1 || listed[0].ID != invitation.ID {
			t.Errorf("the operations read answered %+v, want the invitation that organisation holds", listed)
		}

		for _, tc := range []struct {
			name   string
			method string
			path   string
			body   string
		}{
			{name: "inviting somebody", method: http.MethodPost, path: apiOrgInvitesPathOf(customer.ID), body: `{"email":"new@example.com","role":"owner"}`},
			{name: "withdrawing an invitation", method: http.MethodDelete, path: apiOrgInvitePathOf(customer.ID, invitation.ID)},
		} {
			t.Run(tc.name+" as an operations "+role, func(t *testing.T) {
				rec := f.apiAsCaller(tc.method, tc.path, tc.body)
				if rec.Code != http.StatusForbidden {
					t.Fatalf("%s as an operations %s answered %d (%s), want 403: reading across organisations is not deciding for them", tc.name, role, rec.Code, rec.Body)
				}
				if code := refusal(f, rec); code != "not_permitted" {
					t.Errorf("the refusal carries the code %q, want not_permitted", code)
				}
			})
		}
	}
	if held, _ := f.invitation(invitation.ID); held.State != invitePending {
		t.Errorf("the refused withdrawal left the invitation %q, want it %q", held.State, invitePending)
	}
	if held := f.invitationsOf(customer.ID); len(held) != 1 || held[0].ID != invitation.ID {
		t.Errorf("the refused invitation wrote %+v, want the one invitation that was already there", held)
	}
	if _, member := f.membership(customer.ID, opsUID); member {
		t.Error("an operations account holds a membership of the customer's organisation, and admitting an invitation act through the operations membership is how that membership is made (F5)")
	}
	// A refused act is not an act: the log holds what happened, so no refusal
	// left the operations account an invitation line (D456).
	for _, entry := range f.auditEntries() {
		if entry.ActorUID == opsUID && (entry.Action == actionInvitationCreate || entry.Action == actionInvitationRevoke) {
			t.Errorf("the refused operations account left an %s line: %+v", entry.Action, entry)
		}
	}
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
	if mail := f.lastMail(); mail.To != opsEmail || !linkIn(f, mail, seeded.ID) {
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

// TestSeedIsRefusedOnceAnOperationsMemberExists: the seed's "already seeded"
// predicate is a member or a live invitation, and they are two doors. Here the
// seeded invitation was accepted and spent, so no pending invitation is left to
// refuse on and the member is the whole of what stops a re-run — and a re-run is
// the one write that reaches the live operations organisation without consulting
// its email-domain list, so a second seed with any address would mail an owner
// invitation into the team that reads across every customer's records.
func TestSeedIsRefusedOnceAnOperationsMemberExists(t *testing.T) {
	f := newFixture(t)

	seeded, err := f.svc.SeedOperations(context.Background(), opsEmail)
	if err != nil {
		t.Fatalf("seeding the operations organisation: %v", err)
	}
	held := f.invitationsOf(seeded.ID)
	if len(held) != 1 {
		t.Fatalf("the seed wrote %d invitation(s), want one", len(held))
	}
	f.accepted(opsUID, opsEmail, held[0].ID)
	if spent, _ := f.invitation(held[0].ID); spent.State != inviteAccepted {
		t.Fatalf("the seeded invitation is %q, want it spent, so the member is the only thing the next seed can refuse on", spent.State)
	}
	mailed := len(f.mail.sent)

	if _, err := f.svc.SeedOperations(context.Background(), samEmail); err == nil {
		t.Fatal("a second seed was accepted into a live operations organisation, and a re-run mails an owner invitation to any address it is given")
	}
	if orgs := f.organisations(); len(orgs) != 1 || orgs[0].ID != seeded.ID {
		t.Errorf("the refused seed wrote %+v, want the one organisation the first seed wrote", orgs)
	}
	if now := f.invitationsOf(seeded.ID); len(now) != 1 || now[0].ID != held[0].ID {
		t.Errorf("the refused seed wrote %+v, want the invitation the first seed wrote and nothing else", now)
	}
	if member, found := f.membership(seeded.ID, opsUID); !found || member.Role != roleOwner {
		t.Errorf("the refused seed left the operations organisation's member %+v (found %v), want the owner the first seed's invitation made", member, found)
	}
	if len(f.mail.sent) != mailed {
		t.Error("the refused seed mailed something, and the address it names is one this deployment never decided about")
	}
}

// TestSeedIsRunnableAgainAfterItsMailFails: a failed send must not leave the
// deployment in the one state the seed's own guard refuses on. The seed writes
// an organisation with no member at all, so an invitation left pending is one
// nobody can withdraw and one that refuses the seed for the whole of the seven
// days it is live — a fresh deployment with no way in, its only door shut by the
// record of an invitation nobody ever received. What the failure leaves is the
// organisation, and the operator's remedy, running the seed again, works.
func TestSeedIsRunnableAgainAfterItsMailFails(t *testing.T) {
	f := newFixture(t)
	f.mail.err = errors.New("the mail provider refused the message")

	if _, err := f.svc.SeedOperations(context.Background(), opsEmail); err == nil {
		t.Fatal("a seed whose mail the provider refused was accepted, and a link nobody received must not read as one that was sent")
	}
	orgs := f.organisations()
	if len(orgs) != 1 {
		t.Fatalf("the store holds %d organisation(s), want the one the seed wrote", len(orgs))
	}
	if held := f.invitationsOf(orgs[0].ID); len(held) != 0 {
		t.Errorf("the failed seed left %+v, want nothing: a pending invitation here is what refuses the next seed, and this organisation has no member to withdraw it", held)
	}
	if len(f.mail.sent) != 0 {
		t.Error("the failed send recorded a message anyway")
	}

	f.mail.err = nil
	if _, err := f.svc.SeedOperations(context.Background(), samEmail); err != nil {
		t.Fatalf("running the seed again after a failed send: %v", err)
	}
	if again := f.organisations(); len(again) != 1 || again[0].ID != orgs[0].ID {
		t.Errorf("the second seed wrote %+v, want the organisation already there and no second one", again)
	}
	held := f.invitationsOf(orgs[0].ID)
	if len(held) != 1 || held[0].Email != samEmail || held[0].State != invitePending {
		t.Errorf("the store holds %+v, want the one pending invitation the second seed wrote", held)
	}
	if len(f.mail.sent) != 1 {
		t.Errorf("the second seed mailed %d message(s), want the one it delivered", len(f.mail.sent))
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

// TestInvitationRefusesAnAddressThatIsNoMember: the address on the invite form
// is a value a caller chooses, and it is the only thing between that body and an
// invitation the store holds and the deployment's own sender mails. An address
// that is no member of this tree is refused before anything is written, before
// the organisation's rate is spent and before the mail path is reached — an
// invitation bound to a value access.Member refuses is one nobody can be
// compared against, so it could never be accepted by the person it was sent to
// (F31's fail-closed half).
func TestInvitationRefusesAnAddressThatIsNoMember(t *testing.T) {
	f := newFixture(t)
	org := f.signedUp(danaUID, danaEmail, "Example Ltd")
	f.actsAs(danaUID, danaEmail, signInGoogle)

	rec := f.inviteAs(org.ID, "not an address", roleMember)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("an invitation to an address access.Member refuses answered %d (%s), want the refusal", rec.Code, rec.Body)
	}
	if code := refusal(f, rec); code != "invalid_email" {
		t.Errorf("the refusal carries the code %q, want invalid_email", code)
	}
	if held := f.invitationsOf(org.ID); len(held) != 0 {
		t.Errorf("the store holds %+v, want nothing: an invitation that cannot be accepted is one this deployment must not mail", held)
	}
	if len(f.mail.sent) != 0 {
		t.Error("the refused invitation was mailed, and its address is the thing nothing here could compare")
	}
}

// TestInvitationIsRecordedWhenTheMailFails: an invitation is written before it
// is mailed, so a mail path that fails leaves one that stands — pending,
// withdrawable, and acceptable by the person it names once they are told of it
// by other means. What the log records is that invitation and the account that
// asked for it, so the `invitation.create` line follows the write and not the
// send: written after the send, it was a line the failure returned before, and
// the organisation's list then held an invitation no record named (D456, F9).
func TestInvitationIsRecordedWhenTheMailFails(t *testing.T) {
	f := newFixture(t)
	org := f.signedUp(danaUID, danaEmail, "Example Ltd")
	f.actsAs(danaUID, danaEmail, signInGoogle)
	f.mail.err = errors.New("the mail provider refused the message")

	rec := f.inviteAs(org.ID, samEmail, roleMember)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("an invitation whose mail the provider refused answered %d (%s), want the failure: a link nobody received must not read as one that was sent", rec.Code, rec.Body)
	}
	held := f.invitationsOf(org.ID)
	if len(held) != 1 || held[0].State != invitePending {
		t.Fatalf("the store holds %+v, want the one invitation the request wrote and left pending", held)
	}
	if !f.auditedInvitation(actionInvitationCreate, org.ID, held[0].ID) {
		t.Error("the invitation the request wrote left no invitation.create line, and an invitation that stands is one somebody created (D456)")
	}
	if len(f.mail.sent) != 0 {
		t.Error("the failed send recorded a message anyway")
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
	message := invitationMail(portalOriginHere, "Example Ltd", invitation, inviteTTL)

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
	if !strings.Contains(sent.Text, invitationURL(portalOriginHere, invitation.ID)) {
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
