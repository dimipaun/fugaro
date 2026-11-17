// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The portal's billing routes (portal plan P4.3; D2186, D2192).
//
// Three doors on one workspace: the mirror a billing screen draws, the quantity
// its customer changes, and Paddle's own customer portal. They are mounted where
// the SPA builds them — `/api/workspaces/{workspace}/subscription…`, the paths
// `ee/portal/src/screens/billing/billing.tsx` states as this area's contract —
// and the workspace is in the path and nowhere else. Everything behind the three
// is read from that workspace's own record and the caller's membership of the
// organisation that owns it, so nothing a request names can reach somebody else's
// billing: no route here takes an organisation, a subscription id or a customer
// id, and a body that tried to name one is refused by its shape rather than
// ignored (`decodeWire`).
//
// The refusals are the ones a screen branches on rather than one status: a
// member who is neither owner nor admin is a person to ask one (D2186), a
// workspace that is not the caller's is answered as one that is not there — the
// same 404 for "not there" and "not yours", which is api_orgs.go's rule and
// there for the same reason — a workspace with nothing bought for it is a screen
// with nothing to draw, and an organisation that has never bought has no
// customer to open a portal on.

import (
	"errors"
	"io"
	"net/http"
)

const (
	// apiWorkspaceSubscriptionPath is the mirror, and the two paths below hang
	// off it: the workspace id is a wildcard segment, read with PathValue.
	apiWorkspaceSubscriptionPath = "/api/workspaces/{workspace}/subscription"
	apiWorkspaceSeatsPath        = apiWorkspaceSubscriptionPath + "/seats"
	apiWorkspacePortalPath       = apiWorkspaceSubscriptionPath + "/portal"

	// maxSeatsBodyBytes is the cap on a seat purchase's body: one integer, so a
	// body the SPA builds fits in it by orders of magnitude.
	maxSeatsBodyBytes = 1 << 10
)

func init() {
	// The mirror reaches the store and the sign-in and the merchant of record
	// not at all: it is this service's own records and the deployment's price.
	registerAPI(http.MethodGet, apiWorkspaceSubscriptionPath, (*Service).apiSubscription,
		reaching(needDocs, needSignIn))
	registerAPI(http.MethodPost, apiWorkspaceSeatsPath, (*Service).apiBuySeats,
		withBody(maxSeatsBodyBytes), reaching(needDocs, needSignIn, needPaddle))
	registerAPI(http.MethodGet, apiWorkspacePortalPath, (*Service).apiCustomerPortal,
		reaching(needDocs, needSignIn, needPaddle))
}

// apiSubscription answers one workspace's subscription as the portal last read
// it from the merchant's events. It is the mirror and nothing else: no call
// reaches Paddle here, so a screen that draws this is drawing the last thing
// Paddle said rather than what a request happened to ask.
func (s *Service) apiSubscription(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.apiCaller(w, r)
	if !ok {
		return
	}
	mirror, err := s.readBilling(r.Context(), actor, r.PathValue("workspace"))
	if err != nil {
		refuseBilling(w, err)
		return
	}
	answerAPI(w, http.StatusOK, mirror)
}

// apiBuySeats moves one subscription to the quantity the caller named, and
// answers the subscription as it now stands.
func (s *Service) apiBuySeats(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.apiCaller(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		failAPI(w, err)
		return
	}
	var asked WireSeatChange
	if err := decodeWire(body, &asked); err != nil {
		refuseAPI(w, http.StatusBadRequest, "invalid_body", "This request's body is not the shape this route reads.")
		return
	}
	mirror, err := s.buySeats(r.Context(), actor, r.PathValue("workspace"), asked)
	if err != nil {
		refuseBilling(w, err)
		return
	}
	answerAPI(w, http.StatusOK, mirror)
}

// apiCustomerPortal mints one customer-portal session for the organisation the
// workspace belongs to and answers the URL it is opened at.
func (s *Service) apiCustomerPortal(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.apiCaller(w, r)
	if !ok {
		return
	}
	portal, err := s.customerPortal(r.Context(), actor, r.PathValue("workspace"))
	if err != nil {
		refuseBilling(w, err)
		return
	}
	answerAPI(w, http.StatusOK, portal)
}

// refuseBilling answers the refusal one failed billing request earns. Every
// refusal the caller can act on is one of these; what this service could not do
// (a store that did not answer, a merchant that refused) is failed, which is the
// same split `refuseCheckout` and `refuseBinding` make.
//
// The sign-in refusals are api_orgs.go's own: an organisation whose members sign
// in with Google or Microsoft asks the same of a person buying seats as of a
// person buying a licence — and of a person opening the merchant's own portal,
// which is where a payment method is changed.
func refuseBilling(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errSeatsBelowOne):
		refuseAPI(w, http.StatusUnprocessableEntity, "invalid_seats", "A subscription is for one seat or more.")
	case errors.Is(err, errSubscriptionNotRunning):
		refuseAPI(w, http.StatusConflict, "not_running", "This subscription is not renewing, so its seats cannot be changed. Buying a licence for this workspace starts a new one.")
	case errors.Is(err, ErrSSORequired), errors.Is(err, ErrDirectoryNotPinned):
		refuseAPI(w, http.StatusForbidden, "sso_required", "This organisation's members sign in with Google or Microsoft. Sign in that way to use it.")
	case errors.Is(err, errBillingNotPermitted):
		refuseAPI(w, http.StatusForbidden, "not_permitted", "Only an organisation's owners and admins may manage its billing.")
	case errors.Is(err, errNoSuchWorkspace):
		refuseAPI(w, http.StatusNotFound, "not_found", "That workspace is not one whose billing you may read.")
	case errors.Is(err, errNoSuchSubscription):
		refuseAPI(w, http.StatusNotFound, "no_subscription", "That workspace has no subscription.")
	case errors.Is(err, errNoPaddleCustomer):
		refuseAPI(w, http.StatusNotFound, "no_customer", "This organisation has not bought through Paddle, so there is no customer portal to open.")
	default:
		failAPI(w, err)
	}
}
