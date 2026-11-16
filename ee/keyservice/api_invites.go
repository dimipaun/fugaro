// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The portal's invitation routes (portal plan P2.2; spec §Tenancy, F9, F10,
// F40): an organisation's owners and admins read who has been asked, invite an
// address, and withdraw one; the person an invitation is bound to accepts it
// once they have signed in.
//
// The three organisation-scoped routes are the ones the members screen already
// calls — `GET /api/orgs/{id}/invites`, `POST` beside it, and
// `DELETE /api/orgs/{id}/invites/{id}` (ee/portal/src/screens/members) — spelled
// here as the API spells every route. The accepting call is the one the
// invitation's mail leads to and it is scoped by the invitation alone:
// the person accepting is not a member of anything yet, so there is no
// organisation of theirs to name, and the invitation itself is what says which
// one they are joining.
//
// Every route is behind one sign-in, and the caller is never the body's: the
// inviter recorded on an invitation is the verified address (invites.go), and
// the acceptance compares the caller's verified address with the invitation's
// own. Nothing about an invitation's id is a secret — the mail carries it and
// the page is public — so what keeps an invitation safe is that its acceptance
// is a person's own address and nothing else.

import (
	"errors"
	"io"
	"net/http"
)

// The routes and the bound on the body that carries one: an address of at most
// what an address can be and one of the three roles fits in it by orders of
// magnitude.
const (
	apiOrgInvitesPath   = "/api/orgs/{id}/invites"
	apiOrgInvitePath    = "/api/orgs/{id}/invites/{invite}"
	apiInviteAcceptPath = "/api/invites/{invite}/accept"

	maxInviteBodyBytes = 1 << 10
)

func init() {
	registerAPI(http.MethodGet, apiOrgInvitesPath, (*Service).apiListInvitations,
		reaching(needDocs, needSignIn))
	registerAPI(http.MethodPost, apiOrgInvitesPath, (*Service).apiCreateInvitation,
		withBody(maxInviteBodyBytes), reaching(needDocs, needSignIn))
	registerAPI(http.MethodDelete, apiOrgInvitePath, (*Service).apiRevokeInvitation,
		reaching(needDocs, needSignIn))
	registerAPI(http.MethodPost, apiInviteAcceptPath, (*Service).apiAcceptInvitation,
		reaching(needDocs, needSignIn))
}

// apiListInvitations answers one organisation's invitations: an owner's or an
// admin's read (D2186, D2195), the operations membership's across every
// organisation, and that read written to the log before it is answered
// (accessTo, D456).
func (s *Service) apiListInvitations(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.apiCaller(w, r)
	if !ok {
		return
	}
	invitations, err := s.listInvitations(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		refuseInvitation(w, err)
		return
	}
	answerAPI(w, http.StatusOK, WireInviteList{Invites: invitations})
}

// apiCreateInvitation invites one address into one organisation with one role.
// The route is what the members screen's form posts, and what it answers is the
// invitation itself, so the list the screen is holding gets the row it just
// made.
func (s *Service) apiCreateInvitation(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.apiCaller(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		failAPI(w, err)
		return
	}
	var asked WireInviteCreate
	if err := decodeWire(body, &asked); err != nil {
		refuseAPI(w, http.StatusBadRequest, "invalid_body", "This request's body is not the shape this route reads.")
		return
	}
	invitation, err := s.createInvitation(r.Context(), actor, r.PathValue("id"), asked)
	if err != nil {
		refuseInvitation(w, err)
		return
	}
	answerAPI(w, http.StatusCreated, invitation)
}

// apiRevokeInvitation withdraws one invitation. It answers nothing, like every
// other withdrawal: what changed is a record the caller re-reads, and the row
// the screen draws is the list's and not this answer's.
func (s *Service) apiRevokeInvitation(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.apiCaller(w, r)
	if !ok {
		return
	}
	if _, err := s.revokeInvitation(r.Context(), actor, r.PathValue("id"), r.PathValue("invite")); err != nil {
		refuseInvitation(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// apiAcceptInvitation is the invitation's own route: the signed-in caller
// becomes the member the invitation names them as, once, and the invitation is
// spent. What refuses it beyond the invitation's own state is the SSO rule of
// the organisation being joined (invites.go).
func (s *Service) apiAcceptInvitation(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.apiCaller(w, r)
	if !ok {
		return
	}
	invitation, err := s.acceptInvitation(r.Context(), actor, r.PathValue("invite"))
	if err != nil {
		refuseInvitation(w, err)
		return
	}
	answerAPI(w, http.StatusOK, invitation)
}

// refuseInvitation answers the refusal one failed invitation request earns.
// Which of them it is, is what the portal's screens branch on: `not_found` for
// an invitation the caller may not act on (a member of another organisation, or
// an address the invitation is not bound to — the same answer whether the
// record is not there or is not theirs), `not_permitted` for a member of the
// organisation who decides nothing, `sso_required` for a sign-in the
// organisation's rule refuses, and the two states a spent invitation is in.
//
// What it never says is which of "not there" and "not yours" a 404 was, for the
// reason refuseOrgAccess does not: which ids exist is not something a stranger
// learns from this service.
func refuseInvitation(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNoSuchOrganisation), errors.Is(err, errNoSuchInvitation):
		refuseAPI(w, http.StatusNotFound, "not_found", "This invitation is not one you may act on.")
	case errors.Is(err, errInviteNotPermitted):
		refuseAPI(w, http.StatusForbidden, "not_permitted", "Only an organisation's owners and admins may invite people, read its invitations and withdraw one.")
	case errors.Is(err, ErrSSORequired), errors.Is(err, ErrDirectoryNotPinned):
		refuseAPI(w, http.StatusForbidden, "sso_required", "This organisation's members sign in with Google or Microsoft. Sign in that way to use it.")
	case errors.Is(err, errInvitationExpired):
		refuseAPI(w, http.StatusGone, "invitation_expired", "This invitation has expired. Ask an owner or an admin of the organisation for another.")
	case errors.Is(err, errInvitationClosed):
		refuseAPI(w, http.StatusConflict, "invitation_not_pending", "This invitation has already been accepted or withdrawn.")
	case errors.Is(err, errInviteEmailRefused):
		refuseAPI(w, http.StatusUnprocessableEntity, "invalid_email", "This is not an email address an invitation can be bound to.")
	case errors.Is(err, errInviteDomainRefused):
		refuseAPI(w, http.StatusUnprocessableEntity, "email_not_allowed", "This address is outside the domains this organisation's invitations are limited to.")
	case errors.Is(err, errInviteRoleRefused):
		refuseAPI(w, http.StatusUnprocessableEntity, "invalid_role", "A role is one of owner, admin and member.")
	default:
		failAPI(w, err)
	}
}
