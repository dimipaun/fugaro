// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The portal's deletion routes (portal plan P2.4; D2195, F17): the two doors a
// person ends something with — an organisation they own, and their own account.
//
// Both are DELETEs on the thing itself and neither reads a body, so the whole of
// what a request names is its path and the sign-in behind it: an organisation the
// id names, and the account the token proved. Nothing here takes an organisation,
// a uid or an address from a body, and a request that sent one would be refused
// by the shape rather than read for the half of it this build knows — the shape
// is nothing at all.
//
// **What the caller may not have is one answer.** An organisation that is not
// there and one the caller does not own are both 404 (`refuseDeletion`): the list
// of organisation ids that exist is a list of the portal's customers, and the
// same rule is `api_orgs.go`'s for a read. A caller who IS in the organisation
// and is not its owner is told so, because that is a fact about their own
// membership and asking an owner is what they can do about it — the same split
// the members screen makes with `not_permitted`.
//
// Both routes answer 204 and nothing else: what was deleted is not a shape to
// answer with, and a client that needs to know what is left reads the list it
// always reads (`GET /orgs`).

import (
	"errors"
	"net/http"
)

// apiAccountPath is the account's own door. It names no id: the account is the
// one the request's sign-in proved, and there is no way to ask for another's.
const apiAccountPath = "/api/account"

func init() {
	// The organisation's deletion reaches the merchant of record, because
	// deleting an organisation is what cancels what it pays for: a deployment
	// without that client would mount a door whose every use fails halfway.
	registerAPI(http.MethodDelete, apiOrgPath, (*Service).apiDeleteOrganisation,
		reaching(needDocs, needSignIn, needPaddle))
	registerAPI(http.MethodDelete, apiAccountPath, (*Service).apiDeleteAccount,
		reaching(needDocs, needSignIn))
}

// apiDeleteOrganisation ends one organisation and everything it runs.
func (s *Service) apiDeleteOrganisation(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.apiCaller(w, r)
	if !ok {
		return
	}
	if err := s.deleteOrganisation(r.Context(), actor, r.PathValue("id")); err != nil {
		refuseDeletion(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// apiDeleteAccount ends the caller's own account.
func (s *Service) apiDeleteAccount(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.apiCaller(w, r)
	if !ok {
		return
	}
	if err := s.deleteAccount(r.Context(), actor); err != nil {
		refuseDeletion(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// refuseDeletion answers the refusal one failed deletion earns.
//
// What this service could not do — the merchant that did not cancel, the Identity
// Toolkit that did not answer, a store that failed — is failed rather than
// refused, which is the split every other area makes (`refuseBilling`,
// `refuseBinding`): the caller is told that the request could not be answered,
// and the reason goes to the log. A deletion that failed that way is one they can
// make again, and the state it left is the state it started from as far as
// anything the caller can see goes.
//
// The sole-owner refusal is a 409 and not a 403, because the person is permitted
// to delete their own account and the refusal is a state they can change: delete
// the organisation, or make somebody else an owner. `sole_owner` is the code the
// account screen draws the API's own sentence for.
func refuseDeletion(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNoSuchOrganisation):
		refuseAPI(w, http.StatusNotFound, "not_found", "This organisation is not one you may delete.")
	case errors.Is(err, errDeletionNotPermitted):
		refuseAPI(w, http.StatusForbidden, "not_permitted", "Only an organisation's owner may delete it.")
	case errors.Is(err, errSoleOwner):
		refuseAPI(w, http.StatusConflict, "sole_owner", "You are the only owner of an organisation, so it cannot be left without one. Delete that organisation, or make somebody else an owner, and then delete your account.")
	case errors.Is(err, ErrSSORequired), errors.Is(err, ErrDirectoryNotPinned):
		refuseAPI(w, http.StatusForbidden, "sso_required", "This organisation's members sign in with Google or Microsoft. Sign in that way to use it.")
	default:
		failAPI(w, err)
	}
}
