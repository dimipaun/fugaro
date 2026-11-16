// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The portal's entitlement route (portal plan P3.1): the list of what the
// caller's organisation holds, which is the other half of the screen's cards —
// a card is a workspace and the entitlement that licenses it
// (`ee/portal/src/screens/workspaces/workspaces.tsx`).
//
// It is one read and it is the caller's own organisation's, for the reason the
// workspace list is (api_workspaces.go): the SPA names no organisation, and the
// one a person is working in is the switcher's own head.
//
// What is listed is every grant the organisation holds, ended and superseded
// ones included: the card of a workspace whose licence was superseded still
// names that licence — its key runs to the end of the term it was granted for —
// and a list that dropped it would leave the card with nothing to say. What the
// screen does with a status is the screen's business, and the wire shape carries
// the status for exactly that.

import (
	"net/http"
	"time"
)

const apiEntitlementsPath = "/api/entitlements"

func init() {
	registerAPI(http.MethodGet, apiEntitlementsPath, (*Service).apiListEntitlements,
		reaching(needDocs, needSignIn))
}

// apiListEntitlements answers the caller's organisation's entitlements, the
// oldest first: the order a person's licences were granted in, which is the
// order the screen reads them back as.
func (s *Service) apiListEntitlements(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.apiCaller(w, r)
	if !ok {
		return
	}
	org, present, err := s.currentOrganisation(r.Context(), actor)
	if err != nil {
		refuseOrgAccess(w, err)
		return
	}
	answer := WireEntitlementList{Entitlements: []WireEntitlement{}}
	if present {
		held, err := recordsWhere[WireEntitlement](r.Context(), s.docs, entitlementsCollection,
			Where{Field: "organisation_id", Value: org.ID})
		if err != nil {
			failAPI(w, err)
			return
		}
		oldestFirst(held, func(one WireEntitlement) time.Time { return one.CreatedAt }, func(one WireEntitlement) string { return one.ID })
		answer.Entitlements = held
	}
	answerAPI(w, http.StatusOK, answer)
}
