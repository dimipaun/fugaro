// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The route a reseller mints a licence by (portal plan P6.2; D2189, D2190, F28,
// F40).
//
// It is `POST` on the customer's own keys path — the path the reseller's screen
// already reads a customer's keys from (`screens/reseller/api.ts`'s
// `customerKeysPath`, api_resellers.go's `apiResellerKeysPath`) — because what it
// writes is that customer's licence and it names the customer the way every
// other route of this area does: in the path, and never in the body. The
// reseller it is for is the caller's own membership and nothing else
// (resellerCaller), so a reseller cannot mint for a customer of another one, and
// the workspace the body names is checked against that customer's organisation
// (mintResellerLicence).
//
// The body is `entitlements.mint`, which is operations' own request shape as
// well: granting a term is one act, and a reseller's is that act under a credit
// limit and a wholesale price (wire_entitlements.go, D2189). The answer is
// `entitlements.entitlement`, the record it wrote.

import (
	"errors"
	"io"
	"net/http"
)

func init() {
	registerAPI(http.MethodPost, apiResellerKeysPath, (*Service).apiResellerMintKey,
		withBody(maxResellerBodyBytes), reaching(needDocs, needSignIn))
}

// apiResellerMintKey grants one term on the caller's reseller behalf: a row, a
// seat count, an end and a check interval, against a customer of its own and
// inside the credit limit operations set for it.
//
// The limits of the row are the row's and the refusal of a sale that passes the
// limit is a 409 rather than a 422: the request is one the reseller may make and
// the state it meets is what refuses it, which is the distinction
// `refuseReseller`'s own two conflicts draw.
func (s *Service) apiResellerMintKey(w http.ResponseWriter, r *http.Request) {
	_, held, ok := s.resellerCaller(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		failAPI(w, err)
		return
	}
	var asked WireEntitlementMint
	if err := decodeWire(body, &asked); err != nil {
		refuseAPI(w, http.StatusBadRequest, "invalid_body", "This request's body is not the shape this route reads.")
		return
	}
	granted, err := s.mintResellerLicence(r.Context(), held, r.PathValue("id"), asked)
	if err != nil {
		refuseResellerMint(w, err)
		return
	}
	answerAPI(w, http.StatusCreated, granted)
}

// refuseResellerMint answers the refusal one failed sale earns, and hands
// everything else to `refuseReseller`: the gate's own refusals (a caller who is
// not a reseller's person, a member who decides nothing, a customer this
// reseller does not hold, the sign-in the reseller organisation's type demands)
// are the same ones the rest of this area answers, and a second list of them
// here would be the place the two drift apart.
//
// The four the mint adds are the row's own bounds — the same four
// `/api/ops/mints` answers, under the same codes, because they are the row's and
// not the door's (F28, D2184, F30) — and the code `over_credit_limit`, which is
// D2189's ceiling and the one refusal the reseller's screen has to draw as a
// sale it may not make rather than a request it typed wrongly.
func refuseResellerMint(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errOperationsBadRow), errors.Is(err, errResellerRowNotPriced):
		refuseAPI(w, http.StatusUnprocessableEntity, "invalid_row", "That row is not one the policy table carries, or not one this reseller has a wholesale price for.")
	case errors.Is(err, errOperationsBadSeats):
		refuseAPI(w, http.StatusUnprocessableEntity, "invalid_seats", "That seat count is not one this row may be granted with.")
	case errors.Is(err, errOperationsBadTerm):
		refuseAPI(w, http.StatusUnprocessableEntity, "invalid_term", "That end is not inside the row's own term.")
	case errors.Is(err, errOperationsCheckInterval):
		refuseAPI(w, http.StatusUnprocessableEntity, "invalid_check_interval", "That check interval is not one this row may be granted with.")
	case errors.Is(err, errResellerNoCredit):
		refuseAPI(w, http.StatusConflict, "over_credit_limit", "This sale would pass the credit limit operations set for this reseller. The licences it already holds are unchanged.")
	default:
		refuseReseller(w, err)
	}
}
