// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The portal's workspace routes (portal plan P3.1; D2196, D2205): the list the
// screen draws its cards from and the binding that mints a key.
//
// Both routes read the caller's own organisation and no organisation in their
// path, which is the shape the SPA calls (`GET /workspaces`, `POST
// /workspaces/bind`): a person works in one organisation at a time — the one
// they last selected — and the switcher's own head is what that is
// (currentOrganisation). An organisation that is not the caller's is therefore
// not reachable here at all, and the licence a binding names is read through the
// membership of the organisation that granted it.
//
// The refusals are the ones a screen branches on rather than one status: a
// project with no installation behind it is a person who has to run `fugaro
// setup` first, a rebind inside its window is a person who has to wait, and a
// project another workspace holds is neither of those. Every one of them is a
// sentence for a person and a code for the client.

import (
	"errors"
	"io"
	"net/http"
	"time"
)

const (
	apiWorkspacesPath    = "/api/workspaces"
	apiWorkspaceBindPath = "/api/workspaces/bind"

	// maxWorkspaceBodyBytes is the cap on a binding's body: one id and one
	// Firebase project id, so a body the SPA builds fits in it by orders of
	// magnitude.
	maxWorkspaceBodyBytes = 1 << 10
)

func init() {
	registerAPI(http.MethodGet, apiWorkspacesPath, (*Service).apiListWorkspaces,
		reaching(needDocs, needSignIn))
	registerAPI(http.MethodPost, apiWorkspaceBindPath, (*Service).apiBindWorkspace,
		withBody(maxWorkspaceBodyBytes), reaching(needDocs, needSignIn))
}

// apiListWorkspaces answers the caller's organisation's workspaces, the oldest
// first. A workspace is where a licence and a Firebase project meet, so this is
// the list the screen's cards are made of, and an organisation with none is an
// empty list rather than an absence: the shape is a list.
func (s *Service) apiListWorkspaces(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.apiCaller(w, r)
	if !ok {
		return
	}
	org, present, err := s.currentOrganisation(r.Context(), actor)
	if err != nil {
		refuseOrgAccess(w, err)
		return
	}
	answer := WireWorkspaceList{Workspaces: []WireWorkspace{}}
	if present {
		held, err := recordsWhere[WireWorkspace](r.Context(), s.docs, workspacesCollection,
			Where{Field: "organisation_id", Value: org.ID})
		if err != nil {
			failAPI(w, err)
			return
		}
		oldestFirst(held, func(one WireWorkspace) time.Time { return one.CreatedAt }, func(one WireWorkspace) string { return one.ID })
		answer.Workspaces = held
	}
	answerAPI(w, http.StatusOK, answer)
}

// apiBindWorkspace binds one licence of the caller's organisation to one
// Firebase project, which is the act that mints the key (D2196). It answers the
// workspace as it now stands, so a screen redraws from the answer rather than
// from a guess about what binding did to two records.
func (s *Service) apiBindWorkspace(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.apiCaller(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		failAPI(w, err)
		return
	}
	var asked WireWorkspaceBind
	if err := decodeWire(body, &asked); err != nil {
		refuseAPI(w, http.StatusBadRequest, "invalid_body", "This request's body is not the shape this route reads.")
		return
	}
	workspace, err := s.bindEntitlement(r.Context(), actor, asked.EntitlementID, asked.ProjectID)
	if err != nil {
		refuseBinding(w, err)
		return
	}
	answerAPI(w, http.StatusOK, workspace)
}

// refuseBinding answers the refusal one failed binding earns. Each of them is
// the caller's to act on, so each is refused rather than failed: what this
// service could not check (a database that did not answer) is a request to make
// again, and what the caller has to change (a project id that is not one, a
// rebind inside its window) says what to change.
//
// The organisation read refusals are api_orgs.go's own: a licence of an
// organisation the caller does not belong to is answered as an organisation they
// may not read, which is the same 404 for "not there" and "not yours".
func refuseBinding(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNoSuchEntitlement):
		refuseAPI(w, http.StatusNotFound, "not_found", "This licence is not one you may bind.")
	case errors.Is(err, ErrSSORequired), errors.Is(err, ErrDirectoryNotPinned):
		refuseAPI(w, http.StatusForbidden, "sso_required", "This organisation's members sign in with Google or Microsoft. Sign in that way to use it.")
	case errors.Is(err, errBindingNotPermitted):
		refuseAPI(w, http.StatusForbidden, "not_permitted", "Only an organisation's owners and admins may bind a licence.")
	case errors.Is(err, errAirGappedRebind):
		refuseAPI(w, http.StatusForbidden, "air_gapped", "An air-gapped licence is issued once, at binding, and is never rebound. Contact Fugaro to move it.")
	case errors.Is(err, errProjectIDShape):
		refuseAPI(w, http.StatusUnprocessableEntity, "invalid_project_id", "A Firebase project id is 6 to 30 characters of lower-case letters, digits and dashes, and begins with a letter.")
	case errors.Is(err, errNoInstallation):
		refuseAPI(w, http.StatusUnprocessableEntity, "no_installation", "No Fugaro installation answers in that project. Run `fugaro setup` there first, then bind it again.")
	case errors.Is(err, errEntitlementNotActive):
		refuseAPI(w, http.StatusConflict, "licence_not_active", "This licence is not active, so there is no key to mint for it.")
	case errors.Is(err, errEntitlementEnded):
		refuseAPI(w, http.StatusConflict, "licence_ended", "This licence's term has ended. Buy a new one to license this workspace.")
	case errors.Is(err, errEntitlementBound):
		refuseAPI(w, http.StatusConflict, "already_bound", "This licence was bound a moment ago. Reload the page and try again.")
	case errors.Is(err, errByAnotherEntitlement):
		refuseAPI(w, http.StatusConflict, "workspace_licensed", "This workspace is already licensed by another entitlement.")
	case errors.Is(err, errProjectIDBound):
		refuseAPI(w, http.StatusConflict, "project_id_bound", "That Firebase project is already bound to another workspace.")
	case errors.Is(err, errRebindTooSoon):
		refuseAPI(w, http.StatusTooManyRequests, "rebind_too_soon", "A workspace may be rebound once every 30 days. You can bind this licence again when the window opens.")
	case errors.Is(err, errProbeUnreachable):
		refuseAPI(w, http.StatusServiceUnavailable, "unreachable", "That project's database could not be reached, so this licence was not bound. Try again.")
	default:
		failAPI(w, err)
	}
}
