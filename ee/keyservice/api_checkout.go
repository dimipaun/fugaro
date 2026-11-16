// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The portal's checkout route (portal plan P4.1; F15, D2186, D2192).
//
// One route, one act: a signed-in person names a workspace and the licence terms
// they accepted, and the answer is the transaction id the Paddle overlay is
// opened with. The organisation is never in the body — it is read from the
// workspace's own record and the caller's membership of it — so the one field a
// body could use to name somebody else's customer is a field this shape does not
// have, and `decodeWire` refuses a body carrying one rather than ignoring it.
// That refusal is F15's own: a page that could set `custom_data` could buy
// against a workspace that is not its own.
//
// The refusals are the ones a screen branches on rather than one status: a
// person who has not accepted the terms has a box to tick, a member who is
// neither owner nor admin is a person to ask one, a workspace with a running
// subscription is a person who wants the billing screen instead, and a workspace
// that is not the caller's is answered as one that is not there — the same 404
// for "not there" and "not yours", because which workspaces exist is a list of
// the portal's customers (api_orgs.go).

import (
	"errors"
	"io"
	"net/http"
)

const (
	apiCheckoutPath = "/api/checkout"

	// maxCheckoutBodyBytes is the cap on a checkout's body: two ids, so a body
	// the SPA builds fits in it by orders of magnitude.
	maxCheckoutBodyBytes = 1 << 10
)

func init() {
	registerAPI(http.MethodPost, apiCheckoutPath, (*Service).apiCreateCheckout,
		withBody(maxCheckoutBodyBytes), reaching(needDocs, needSignIn, needPaddle))
}

// apiCreateCheckout opens one purchase for one workspace and answers the
// transaction the overlay is opened with.
func (s *Service) apiCreateCheckout(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.apiCaller(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		failAPI(w, err)
		return
	}
	var asked WireCheckoutCreate
	if err := decodeWire(body, &asked); err != nil {
		refuseAPI(w, http.StatusBadRequest, "invalid_body", "This request's body is not the shape this route reads.")
		return
	}
	session, err := s.checkout(r.Context(), actor, asked)
	if err != nil {
		refuseCheckout(w, err)
		return
	}
	answerAPI(w, http.StatusCreated, session)
}

// refuseCheckout answers the refusal one failed checkout earns. Every refusal
// the caller can act on is one of these; what this service could not do (a
// store that did not answer, a merchant that refused) is failed, which is the
// same split `refuseBinding` makes.
//
// The sign-in refusals are api_orgs.go's own: an organisation whose members sign
// in with Google or Microsoft asks the same of a person buying as of a person
// binding.
func refuseCheckout(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errLicenceTermsNotAccepted):
		refuseAPI(w, http.StatusUnprocessableEntity, "terms_not_accepted", "Accept the licence terms to buy a licence.")
	case errors.Is(err, ErrSSORequired), errors.Is(err, ErrDirectoryNotPinned):
		refuseAPI(w, http.StatusForbidden, "sso_required", "This organisation's members sign in with Google or Microsoft. Sign in that way to use it.")
	case errors.Is(err, errCheckoutNotPermitted):
		refuseAPI(w, http.StatusForbidden, "not_permitted", "Only an organisation's owners and admins may buy a licence.")
	case errors.Is(err, errNoSuchWorkspace):
		refuseAPI(w, http.StatusNotFound, "not_found", "That workspace is not one you may buy for.")
	case errors.Is(err, errWorkspaceSubscribed):
		refuseAPI(w, http.StatusConflict, "already_subscribed", "That workspace already has a running subscription. Change its seats from its billing page instead.")
	default:
		failAPI(w, err)
	}
}
