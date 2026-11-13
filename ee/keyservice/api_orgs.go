// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The portal's organisation routes (portal plan P2.1; spec §Tenancy): the
// sign-up that makes an organisation, the switcher's list, the last-used
// selection, and the two reads — one organisation and its members — that the
// operations membership widens to every organisation in the portal.
//
// Every route here is behind one sign-in: the SPA presents the project's ID
// token as a bearer token (F31), the verifier answers the caller it names, and
// what that caller may read is the memberships' answer and never the token's.
// Nothing of a body is trusted past its shape: the name an organisation is
// created with is text (and is refused when it is not a name), and a body asking
// for an operations or a reseller organisation is a body asking for a field this
// shape does not have — only operations creates a reseller (D2191, P6.1).
//
// The refusals are the three the portal's clients branch on: 401 for a request
// with no sign-in this service admits, 403 with `sso_required` for a membership
// whose rule refuses this sign-in (the dialog the spec's auth section describes),
// and 404 for an organisation the caller may not read — the same answer whether
// it is not there or not theirs, because which ids exist is a list of the
// portal's customers.

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
)

// The routes and their bounds. The organisation's id is the store's own and it is
// opaque (the schema says so); the two verbs that change something are POSTs.
const (
	apiOrgsPath       = "/api/orgs"
	apiOrgPath        = "/api/orgs/{id}"
	apiOrgMembersPath = "/api/orgs/{id}/members"
	apiOrgSelectPath  = "/api/orgs/{id}/select"

	// maxOrgBodyBytes is the cap on a sign-up's body: the shape is one name of at
	// most maxOrganisationName characters, so a body the SPA builds fits in it by
	// orders of magnitude.
	maxOrgBodyBytes = 1 << 10

	// maxPortalTokenBytes bounds a presented ID token before it is parsed, for
	// the reason mcpauth bounds one: it arrives as a single header value, and one
	// past the bound is refused at the door rather than decoded.
	maxPortalTokenBytes = 8 << 10

	// portalBearerScheme is how the SPA hands its token over. It is the scheme
	// every Firebase client sends and the only one this service reads.
	portalBearerScheme = "Bearer"
)

func init() {
	registerAPI(http.MethodPost, apiOrgsPath, (*Service).apiCreateOrganisation,
		withBody(maxOrgBodyBytes), reaching(needDocs, needSignIn))
	registerAPI(http.MethodGet, apiOrgsPath, (*Service).apiListOrganisations,
		reaching(needDocs, needSignIn))
	registerAPI(http.MethodGet, apiOrgPath, (*Service).apiReadOrganisation,
		reaching(needDocs, needSignIn))
	registerAPI(http.MethodPost, apiOrgSelectPath, (*Service).apiSelectOrganisation,
		reaching(needDocs, needSignIn))
	registerAPI(http.MethodGet, apiOrgMembersPath, (*Service).apiListMembers,
		reaching(needDocs, needSignIn))
}

// apiCreateOrganisation is the portal's sign-up: a verified person names an
// organisation and becomes its owner. The type is never the body's, the terms'
// version is the deployment's (it serves the wording), and the free seat's
// entitlement is P3.1's hook's.
func (s *Service) apiCreateOrganisation(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.apiCaller(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		failAPI(w, err)
		return
	}
	var asked WireOrgCreate
	if err := decodeWire(body, &asked); err != nil {
		refuseAPI(w, http.StatusBadRequest, "invalid_body", "This request's body is not the shape this route reads.")
		return
	}
	name := strings.TrimSpace(asked.Name)
	if !validOrganisationName(name) {
		refuseAPI(w, http.StatusUnprocessableEntity, "invalid_name", "An organisation's name is one to 64 characters.")
		return
	}
	org, err := s.signUp(r.Context(), actor, name)
	switch {
	case errors.Is(err, errOrgDailyLimit):
		refuseAPI(w, http.StatusTooManyRequests, "too_many_organisations", "This account has created all the organisations a day allows. Try again tomorrow.")
		return
	case err != nil:
		failAPI(w, err)
		return
	}
	answerAPI(w, http.StatusCreated, org)
}

// apiListOrganisations answers the organisations the caller belongs to, the one
// they last worked in first: it is what the switcher renders, and its head is
// where a person lands who has not chosen one in this session.
func (s *Service) apiListOrganisations(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.apiCaller(w, r)
	if !ok {
		return
	}
	orgs, err := s.organisationsOf(r.Context(), actor)
	if err != nil {
		// The rule a membership carries is applied to the list, so the refusal
		// here is the same one every other route answers it with.
		refuseOrgAccess(w, err)
		return
	}
	answerAPI(w, http.StatusOK, WireOrgList{Organisations: orgs})
}

// apiReadOrganisation answers one organisation: the caller's own, or — through
// the operations membership, and written to the log — anybody's.
func (s *Service) apiReadOrganisation(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.apiCaller(w, r)
	if !ok {
		return
	}
	access, err := s.accessTo(r.Context(), actor, r.PathValue("id"), actionOrganisationRead)
	if err != nil {
		refuseOrgAccess(w, err)
		return
	}
	answerAPI(w, http.StatusOK, access.Organisation)
}

// apiListMembers answers one organisation's members. It is an owner's or an
// admin's read and not every member's (D2186, D2195): a member reads the
// organisation's screens and decides nothing in the portal, and who else belongs
// is one of the things decided. The operations membership reads it as it reads
// everything, and that read is audited like any other.
func (s *Service) apiListMembers(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.apiCaller(w, r)
	if !ok {
		return
	}
	access, err := s.accessTo(r.Context(), actor, r.PathValue("id"), actionOrganisationMembersRead)
	if err != nil {
		refuseOrgAccess(w, err)
		return
	}
	if !access.Operations && access.Membership.Role != roleOwner && access.Membership.Role != roleAdmin {
		refuseAPI(w, http.StatusForbidden, "not_permitted", "Only an organisation's owners and admins may read its members.")
		return
	}
	members, err := membersOf(r.Context(), s.docs, access.Organisation.ID)
	if err != nil {
		failAPI(w, err)
		return
	}
	answerAPI(w, http.StatusOK, WireOrgMembers{Members: members})
}

// apiSelectOrganisation records the caller's own last use of one organisation,
// which is the whole of what the switcher writes. It answers nothing: what
// changed is the caller's own membership, and the next list they read shows it.
func (s *Service) apiSelectOrganisation(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.apiCaller(w, r)
	if !ok {
		return
	}
	if err := s.selectOrganisation(r.Context(), actor, r.PathValue("id")); err != nil {
		refuseOrgAccess(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// apiCaller verifies the request's sign-in and answers the caller it names. What
// it refuses, it refuses itself: a request with no token, a token this service
// does not admit, and a verified sign-in that names no account — the last of
// which is a sign-in every record here would be filed under nothing for.
//
// A verifier that failed for its own reason (a key set it could not read) is the
// service's failure and not the caller's, so it is a 500 rather than a 401: the
// person's sign-in may be perfectly good.
func (s *Service) apiCaller(w http.ResponseWriter, r *http.Request) (Caller, bool) {
	token, ok := portalToken(r)
	if !ok {
		refuseAPI(w, http.StatusUnauthorized, "unauthenticated", "This request carries no sign-in.")
		return Caller{}, false
	}
	actor, err := s.signIn.Verify(r.Context(), token)
	switch {
	case errors.Is(err, ErrUnverifiedSignIn):
		refuseAPI(w, http.StatusUnauthorized, "unauthenticated", "This sign-in is not one this portal admits.")
		return Caller{}, false
	case err != nil:
		failAPI(w, err)
		return Caller{}, false
	}
	if actor.UID == "" {
		refuseAPI(w, http.StatusUnauthorized, "unauthenticated", "This sign-in names no account.")
		return Caller{}, false
	}
	return actor, true
}

// portalToken reads the sign-in out of the request's Authorization header:
// `Bearer <ID token>`, which is what a Firebase client sends. A header that is
// not that, or a token over the bound, is no sign-in at all rather than a
// failure: the caller is told to sign in, which is what they have to do about
// it either way.
func portalToken(r *http.Request) (string, bool) {
	scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")
	if !found || !strings.EqualFold(scheme, portalBearerScheme) {
		return "", false
	}
	token = strings.TrimSpace(token)
	if token == "" || len(token) > maxPortalTokenBytes {
		return "", false
	}
	return token, true
}

// refuseOrgAccess answers the refusal one failed organisation read earns, which
// is one of the three the clients branch on: an organisation the caller may not
// read, a membership whose SSO rule refuses this sign-in, and this service's own
// failure. What the caller is never told is which of "not there" and "not yours"
// it was.
func refuseOrgAccess(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNoSuchOrganisation):
		refuseAPI(w, http.StatusNotFound, "not_found", "This organisation is not one you may read.")
	case errors.Is(err, ErrSSORequired), errors.Is(err, ErrTenantNotPinned):
		refuseAPI(w, http.StatusForbidden, "sso_required", "This organisation's members sign in with Google or Microsoft. Sign in that way to use it.")
	default:
		failAPI(w, err)
	}
}

// failAPI is this service's own failure: the caller is told that the request
// could not be answered and nothing about why (§7.2's rule, read at the API's
// door), and the reason goes to the process's log, which is the project's log
// sink as far as this package can express one.
func failAPI(w http.ResponseWriter, err error) {
	log.Printf("keyservice: the portal could not answer a request: %v", err)
	refuseAPI(w, http.StatusInternalServerError, "unavailable", "The portal could not answer this request just now.")
}

// answerAPI writes one shape as the API's answer.
func answerAPI(w http.ResponseWriter, status int, answer any) {
	w.Header().Set("Content-Type", apiJSONType)
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(answer); err != nil {
		// The status line and the headers are already written, so there is no
		// refusal left to send: what a failure here leaves is a truncated body,
		// and the log is the one place it can be read.
		log.Printf("keyservice: writing the portal's answer: %v", err)
	}
}
