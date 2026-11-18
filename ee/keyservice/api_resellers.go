// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The reseller routes (portal plan P6.1; D2186, D2187, D2189, D2191, F5, F40).
//
// **Two gates, and each route says which one it is under.** The reads and the
// two acts that are a reseller's own — reading its customers and their keys'
// metadata, making a customer organisation, inviting that customer's first owner
// — are behind `resellerCaller`, which is the caller's own reseller membership
// (resellers.go's resellerFor) and nothing else: the reseller those routes act
// for is the caller's, and no body and no path names it, because a reseller that
// could name itself in a request could name another one's customer. The three
// acts that are operations' — creating a reseller organisation, setting its
// credit limit and its rates, and relinking a customer — are behind P5.1's
// `operationsCaller`, which is D2187's role and fresh sign-in and the audit line
// every one of them writes.
//
// **Why these three are not under `/api/ops`.** They are this area's shapes and
// this area's subject: an `/api/ops/**` route is P5.1's table, pinned by its own
// test against every line it writes, and the plan puts this area's door here
// (the paths of the reseller's own window are P6.4's, `screens/reseller/api.ts`,
// and `screens/ops/api.ts` names no reseller path at all). A route is gated by
// the rule it needs and not by the prefix it carries: `/api/resellers` answers
// the reseller's own reads from its own token, and refuses every act on it to
// anybody who is not the operations team.
//
// **The shapes are the ones the portal already carries** (F20): `resellers.
// reseller` is what operations sets and the reseller reads, `resellers.customer`
// is one row of a reseller's list, and `resellers.relink` is operations' move.
// `orgs.create` and `invites.create` are reused for the two writes a reseller
// makes, because they ask for exactly what those shapes carry — a name, and an
// address with a role — and a second shape for the same two fields would be a
// second thing to keep in step with the schema.

import (
	"errors"
	"io"
	"net/http"
	"strings"
)

// The routes this area mounts. The two the reseller's own screen calls are
// spelled as `ee/portal/src/screens/reseller/api.ts` spells them
// (`CUSTOMERS_PATH` and `customerKeysPath`); the rest are this area's own and
// are named after what they do.
const (
	apiResellersPath          = "/api/resellers"
	apiResellerRelinkPath     = "/api/resellers/relink"
	apiResellerTermsPath      = "/api/resellers/terms"
	apiResellerTermsSetPath   = "/api/resellers/{id}/terms"
	apiResellerCustomersPath  = "/api/resellers/customers"
	apiResellerKeysPath       = "/api/resellers/customers/{id}/keys"
	apiResellerFirstOwnerPath = "/api/resellers/customers/{id}/invites"

	// A name of at most 64 characters, and a rate list of a handful of rows: the
	// body is smaller than an operations one by an order of magnitude, and the
	// cap is here rather than inherited so that a route which grew a field would
	// be a route whose cap somebody looked at.
	maxResellerBodyBytes = 4 << 10
)

func init() {
	registerAPI(http.MethodGet, apiResellerCustomersPath, (*Service).apiResellerCustomers,
		reaching(needDocs, needSignIn))
	registerAPI(http.MethodGet, apiResellerKeysPath, (*Service).apiResellerCustomerKeys,
		reaching(needDocs, needSignIn))
	registerAPI(http.MethodGet, apiResellerTermsPath, (*Service).apiReadResellerTerms,
		reaching(needDocs, needSignIn))
	registerAPI(http.MethodPost, apiResellerCustomersPath, (*Service).apiResellerCreateCustomer,
		withBody(maxResellerBodyBytes), reaching(needDocs, needSignIn))
	registerAPI(http.MethodPost, apiResellerFirstOwnerPath, (*Service).apiResellerInviteFirstOwner,
		withBody(maxResellerBodyBytes), reaching(needDocs, needSignIn))

	registerAPI(http.MethodPost, apiResellersPath, (*Service).apiOperationsCreateReseller,
		withBody(maxResellerBodyBytes), reaching(needDocs, needSignIn))
	registerAPI(http.MethodPut, apiResellerTermsSetPath, (*Service).apiOperationsSetResellerTerms,
		withBody(maxResellerBodyBytes), reaching(needDocs, needSignIn))
	registerAPI(http.MethodPost, apiResellerRelinkPath, (*Service).apiOperationsRelinkCustomer,
		withBody(maxResellerBodyBytes), reaching(needDocs, needSignIn))
}

// resellerCaller is the gate the reseller's own routes pass, in the shape P5.1's
// operationsCaller has: the sign-in, then the standing, in one place, so that a
// route added later cannot hold one and forget the other.
//
// It is deliberately not a second way into the operations area and not a second
// way out of it: the standing it asks is the caller's own reseller membership
// (resellerFor), which is where the sign-in rule of THAT organisation is applied
// (D2207, F10), and a member of the operations organisation who is not a member
// of a reseller organisation is refused here exactly as anybody else is.
func (s *Service) resellerCaller(w http.ResponseWriter, r *http.Request) (Caller, resellerAccess, bool) {
	actor, ok := s.apiCaller(w, r)
	if !ok {
		return Caller{}, resellerAccess{}, false
	}
	held, err := s.resellerFor(r.Context(), actor)
	if err != nil {
		refuseReseller(w, err)
		return Caller{}, resellerAccess{}, false
	}
	return actor, held, true
}

// apiResellerCustomers answers the customers the caller's reseller holds, newest
// link first: what spec §Resellers promises it, and the list the reseller's own
// screen draws (P6.4).
func (s *Service) apiResellerCustomers(w http.ResponseWriter, r *http.Request) {
	_, held, ok := s.resellerCaller(w, r)
	if !ok {
		return
	}
	customers, err := s.resellerCustomers(r.Context(), held)
	if err != nil {
		refuseReseller(w, err)
		return
	}
	answerAPI(w, http.StatusOK, customers)
}

// apiResellerCustomerKeys answers one customer's keys as metadata, which is F5's
// whole of what a reseller may see of them: the customer is the caller's own
// (resellerCustomer), and the shape carries no secret to begin with
// (wire_keys.go's WireKeyMetadata).
func (s *Service) apiResellerCustomerKeys(w http.ResponseWriter, r *http.Request) {
	_, held, ok := s.resellerCaller(w, r)
	if !ok {
		return
	}
	org, _, err := s.resellerCustomer(r.Context(), held, r.PathValue("id"))
	if err != nil {
		refuseReseller(w, err)
		return
	}
	keys, err := s.customerKeyMetadata(r.Context(), org.ID)
	if err != nil {
		refuseReseller(w, err)
		return
	}
	answerAPI(w, http.StatusOK, keys)
}

// apiReadResellerTerms answers the caller's own reseller's credit limit and
// wholesale rates, which is the one read of this area that is about the reseller
// itself and not about a customer it sold to.
//
// It is the reseller's because the number is the reseller's to work inside:
// D2189 makes the limit the ceiling on what it may mint, and a reseller that
// cannot read its own limit would learn it by being refused a sale. Nothing here
// is operations' alone — the whole record is what operations writes and what the
// reseller accrues against — and a plain member reads it as they read the
// customers (D2186, D2195).
func (s *Service) apiReadResellerTerms(w http.ResponseWriter, r *http.Request) {
	_, held, ok := s.resellerCaller(w, r)
	if !ok {
		return
	}
	terms, err := s.resellerTerms(r.Context(), held.Organisation)
	if err != nil {
		refuseReseller(w, err)
		return
	}
	answerAPI(w, http.StatusOK, terms)
}

// apiResellerCreateCustomer is D2191's act from the reseller's side: it makes a
// customer organisation, linked to the caller's reseller, with nobody in it. The
// first owner is invited separately (apiResellerInviteFirstOwner), because the
// two are two acts with two answers and a person may want to check the tenant
// before mailing anybody.
//
// It answers the organisation, which is what the reseller's screen needs to show
// the customer it just created — the name it typed, the id it will quote to
// operations, and the link.
func (s *Service) apiResellerCreateCustomer(w http.ResponseWriter, r *http.Request) {
	actor, held, ok := s.resellerCaller(w, r)
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
	org, err := s.createCustomerOrganisation(r.Context(), actor, held, name)
	if err != nil {
		refuseReseller(w, err)
		return
	}
	answerAPI(w, http.StatusCreated, org)
}

// apiResellerInviteFirstOwner invites one address to be a customer's first owner
// (F40). It answers the invitation, as P2.2's own route does, so the reseller
// sees the address and the day it expires.
func (s *Service) apiResellerInviteFirstOwner(w http.ResponseWriter, r *http.Request) {
	actor, held, ok := s.resellerCaller(w, r)
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
	invitation, err := s.inviteCustomerFirstOwner(r.Context(), actor, held, r.PathValue("id"), asked)
	if err != nil {
		refuseReseller(w, err)
		return
	}
	answerAPI(w, http.StatusCreated, invitation)
}

// apiOperationsCreateReseller is D2191's other half: **only operations creates a
// reseller organisation.** The body is a name (`orgs.create`), which is the
// whole of what a create knows — the id is minted here and the commercial terms
// arrive with the next route — and the answer is the reseller's terms as they
// now stand: the organisation, no credit and no rates, which is a reseller that
// may sell nothing until operations prices it.
func (s *Service) apiOperationsCreateReseller(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.operationsCaller(w, r, operationsMayChange)
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
	terms, err := s.createReseller(r.Context(), actor, name, WireReseller{})
	if err != nil {
		refuseReseller(w, err)
		return
	}
	answerAPI(w, http.StatusCreated, terms)
}

// apiOperationsSetResellerTerms sets one reseller's credit limit and wholesale
// rates (D2189, D2190). The body is `resellers.reseller`, the same shape the
// reseller reads, because it is the same record: what it carries beyond the money
// is `organisation_id`, which must be the path's reseller — a body naming another
// is refused rather than ignored, so that the record written is the record asked
// for — and `created_at`, which the answer takes from the organisation and never
// from the body (resellerTermsFor): a body that could set it would be a body that
// could rewrite when a reseller was created.
func (s *Service) apiOperationsSetResellerTerms(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.operationsCaller(w, r, operationsMayChange)
	if !ok {
		return
	}
	id := r.PathValue("id")
	body, err := io.ReadAll(r.Body)
	if err != nil {
		failAPI(w, err)
		return
	}
	var asked WireReseller
	if err := decodeWire(body, &asked); err != nil {
		refuseAPI(w, http.StatusBadRequest, "invalid_body", "This request's body is not the shape this route reads.")
		return
	}
	if strings.TrimSpace(asked.OrganisationID) != id {
		refuseAPI(w, http.StatusUnprocessableEntity, "invalid_reseller", "This request sets the terms of the reseller in its path, and its body names another one.")
		return
	}
	terms, err := s.setResellerTerms(r.Context(), actor, id, asked)
	if err != nil {
		refuseReseller(w, err)
		return
	}
	answerAPI(w, http.StatusOK, terms)
}

// apiOperationsRelinkCustomer moves one customer organisation to another reseller
// (D2191, F14's resolution). It answers the link as it now stands, which is the
// row the reseller's list will show from here.
func (s *Service) apiOperationsRelinkCustomer(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.operationsCaller(w, r, operationsMayChange)
	if !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		failAPI(w, err)
		return
	}
	var asked WireResellerRelink
	if err := decodeWire(body, &asked); err != nil {
		refuseAPI(w, http.StatusBadRequest, "invalid_body", "This request's body is not the shape this route reads.")
		return
	}
	link, err := s.relinkResellerCustomer(r.Context(), actor, asked)
	if err != nil {
		refuseReseller(w, err)
		return
	}
	answerAPI(w, http.StatusCreated, link)
}

// refuseReseller answers the refusal one failed reseller request earns.
//
// `not_found` covers the three ways a customer or a reseller can be missing —
// another reseller's customer, a customer nobody linked, an id nobody has — and
// it covers them with one sentence on purpose: which ids exist, and which
// organisation holds which customer, is not something a stranger learns from this
// service (refuseOrgAccess's own rule). `not_permitted` is a member of a reseller
// who decides nothing, a customer's first owner asked for at a role that is not
// `owner`, and an address that is already one of the reseller's own people —
// which is F40 read from the other side, and is a request the caller may not make
// rather than a body that is malformed.
//
// The two conflicts are the states a customer organisation can be in and not the
// request's fault: it already has its own people (`first_owner_set`), or it is
// already held by the reseller the relink names (`already_linked`). A withdrawal
// of the first owner's invitation is not one of them — an invitation that expired
// or was withdrawn leaves the organisation with nobody who can invite (F40), so
// the reseller may invite again, which is what makes 409 here about the
// organisation and not about a single mail.
func refuseReseller(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNoSuchResellerCustomer), errors.Is(err, errNoSuchOrganisation),
		errors.Is(err, errNotAReseller):
		refuseAPI(w, http.StatusNotFound, "not_found", "This is not a customer or a reseller this request may act on.")
	case errors.Is(err, errNotReseller):
		refuseAPI(w, http.StatusForbidden, "not_reseller", "These screens are a reseller organisation's, and this sign-in is not one of its people.")
	case errors.Is(err, errResellerAmbiguous):
		refuseAPI(w, http.StatusForbidden, "not_permitted", "This account belongs to more than one reseller organisation, and this request names none. Ask operations to act for it.")
	case errors.Is(err, errResellerNotPermitted):
		refuseAPI(w, http.StatusForbidden, "not_permitted", "Only a reseller organisation's owners and admins may do this. Members read its customers.")
	case errors.Is(err, ErrSSORequired), errors.Is(err, ErrDirectoryNotPinned):
		refuseAPI(w, http.StatusForbidden, "sso_required", "A reseller organisation's members sign in with Google or Microsoft. Sign in that way to use it.")
	case errors.Is(err, errOrgDailyLimit):
		refuseAPI(w, http.StatusTooManyRequests, "too_many_organisations", "This reseller has made as many customer organisations as a day allows. The ones it has are unchanged; try again tomorrow.")
	case errors.Is(err, errFirstOwnerSet):
		refuseAPI(w, http.StatusConflict, "first_owner_set", "This customer organisation has its own people already. Its owners and admins invite from here, and the reseller's part in it is done.")
	case errors.Is(err, errResellerAlreadyLinked):
		refuseAPI(w, http.StatusConflict, "already_linked", "That customer is already linked to this reseller, so there is nothing to move.")
	case errors.Is(err, errOperationsBadRow):
		refuseAPI(w, http.StatusUnprocessableEntity, "invalid_row", "That row is not one the policy table carries.")
	case errors.Is(err, errResellerBadCreditLimit), errors.Is(err, errResellerBadRate):
		refuseAPI(w, http.StatusUnprocessableEntity, "invalid_terms", "A reseller's terms are a credit limit of zero or more and one price per policy row, each of zero or more, with no row priced twice.")
	case errors.Is(err, errInviteEmailRefused):
		refuseAPI(w, http.StatusUnprocessableEntity, "invalid_email", "This is not an email address an invitation can be bound to.")
	case errors.Is(err, errInviteRateLimited):
		refuseAPI(w, http.StatusTooManyRequests, "rate_limited", "This organisation has invited as many people as it may right now. Try again later.")
	default:
		failAPI(w, err)
	}
}
