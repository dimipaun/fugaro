// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The portal's key routes (portal plan P3.2; D2182 as amended, D2187, D2206,
// F3, F5, F32, F42): the reveal that rotates a workspace's key, and the file an
// air-gapped licence is downloaded as.
//
// Both routes name a **workspace** and no licence and no project, which is the
// shape the SPA calls (`POST /keys/reveal` with `{workspace_id}`, `GET
// /keys/licence?workspace_id=…`, ee/portal/src/screens/workspaces): a workspace
// is where a licence and a Firebase project meet (D2196), so it is the one id a
// screen holds, and what authorises the request is read from it rather than
// named by the request (checkout.go's rule at the same door).
//
// What the two answer is the two shapes this service already carries and no
// third: `WireRevealBundle`, which is the only one of them with a credential in
// it, and `WireLicenceFile`, which has none. Neither is a shape invented for a
// screen, so there is nothing here for the schemas, the fixtures and the SPA's
// own types to be reconciled with.
//
// **A reveal reaches the sealer**, and it is the first route that does: the
// handover it leaves for the installation running on the key it replaced is
// stored sealed and never in the clear (D2182 as amended, F3). A deployment
// with no sealer therefore does not serve a reveal at all — P1.0's registry is
// what says so, at the start rather than at a person's request (api.go's
// `reaching`).

import (
	"errors"
	"io"
	"net/http"
)

// The routes and the bound on the body that carries one: a workspace id and
// nothing else fits in it by orders of magnitude.
const (
	apiKeysRevealPath  = "/api/keys/reveal"
	apiKeysLicencePath = "/api/keys/licence"

	maxRevealBodyBytes = 1 << 10
)

func init() {
	registerAPI(http.MethodPost, apiKeysRevealPath, (*Service).apiRevealKey,
		withBody(maxRevealBodyBytes), reaching(needDocs, needSignIn, needSealer))
	registerAPI(http.MethodGet, apiKeysLicencePath, (*Service).apiLicenceFile,
		reaching(needDocs, needSignIn))
}

// RevealCall is the reveal's body: the workspace whose key is rotated, which is
// the whole of what the request has to say — the licence, the project and the
// organisation are the workspace's own, and a body that named them would be a
// body naming what authorises it. It is a shape of this file rather than a wire
// type for the reason api_trial.go's TrialCall is: nothing outside this service
// reads a request body, and the portal's own types are the **answers** both
// sides test against (F20).
type RevealCall struct {
	WorkspaceID string `json:"workspace_id"`
}

// apiRevealKey rotates one workspace's key and answers the bundle once: the new
// key id, the key, its renewal secret and the end of the term it covers. The
// bundle is shown to the person who asked for it and stored nowhere (§7.3), and
// the answer is the only time this service will ever say what the secret is.
func (s *Service) apiRevealKey(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.apiCaller(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		failAPI(w, err)
		return
	}
	var asked RevealCall
	if err := decodeWire(body, &asked); err != nil {
		refuseAPI(w, http.StatusBadRequest, "invalid_body", "This request's body is not the shape this route reads.")
		return
	}
	answer, err := s.reveal(r.Context(), actor, asked.WorkspaceID)
	if err != nil {
		refuseReveal(w, err)
		return
	}
	answerAPI(w, http.StatusOK, WireRevealBundle{
		KeyID:         answer.Record.KeyID,
		Key:           answer.Key,
		RenewalSecret: answer.RenewalSecret,
		PaidThrough:   answer.Record.Expires,
	})
}

// apiLicenceFile answers the file an air-gapped licence was issued as (D2206).
// The workspace is the query's, because this is the one route of the two whose
// call is a link the browser follows and not a body this SPA builds.
func (s *Service) apiLicenceFile(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.apiCaller(w, r)
	if !ok {
		return
	}
	file, err := s.licenceFile(r.Context(), actor, r.URL.Query().Get("workspace_id"))
	if err != nil {
		refuseLicenceFile(w, err)
		return
	}
	answerAPI(w, http.StatusOK, file)
}

// refuseReveal answers the refusal one failed reveal earns. Each of them is the
// caller's to act on, so each is refused rather than failed, and the code is what
// the screen branches on: a person who has to sign in again, one who is not the
// organisation's to decide, one holding a licence that is not active, and one
// who has asked too soon are four different things to be told.
//
// The organisation read refusals are api_orgs.go's own, and the two that are not
// a person's to act on are failures: a store that cannot be read is a request to
// make again, and a workspace naming a licence the store does not hold is this
// deployment's own inconsistency rather than a caller's mistake.
func refuseReveal(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNoSuchWorkspace):
		refuseAPI(w, http.StatusNotFound, "not_found", "This workspace is not one you may reveal a key for.")
	case errors.Is(err, ErrSSORequired), errors.Is(err, ErrDirectoryNotPinned):
		refuseAPI(w, http.StatusForbidden, "sso_required", "This organisation's members sign in with Google or Microsoft. Sign in that way to use it.")
	case errors.Is(err, errRevealNotPermitted):
		refuseAPI(w, http.StatusForbidden, "not_permitted", "Only an organisation's owners and admins may reveal a key.")
	case errors.Is(err, ErrStaleSignIn):
		refuseAPI(w, http.StatusForbidden, "stale_sign_in", "Revealing a key needs a sign-in from the last five minutes. Sign in again, then ask once more.")
	case errors.Is(err, errAirGappedReveal):
		refuseAPI(w, http.StatusForbidden, "air_gapped", "An air-gapped licence is issued once and its key has no secret, so there is nothing to reveal. Download its licence file instead.")
	case errors.Is(err, errNoLicence):
		refuseAPI(w, http.StatusConflict, "no_licence", "This workspace has no licence, so there is no key to reveal. Bind one to it first.")
	case errors.Is(err, errEntitlementNotActive):
		refuseAPI(w, http.StatusConflict, "licence_not_active", "This licence is not active, so there is no key to reveal.")
	case errors.Is(err, errEntitlementEnded):
		refuseAPI(w, http.StatusConflict, "licence_ended", "This licence's term has ended. Buy a new one to license this workspace.")
	case errors.Is(err, errWorkspaceUnbound):
		refuseAPI(w, http.StatusConflict, "not_bound", "This workspace's licence is not bound to a Firebase project, so there is no installation to give a key to. Bind it first.")
	case errors.Is(err, errKeyLapsed):
		refuseAPI(w, http.StatusConflict, "key_lapsed", "This workspace's key has already ended, so there is no term left to re-sign. Renew the licence to issue a new key.")
	case errors.Is(err, errRevealTooSoon), errors.Is(err, ErrAlreadyRecorded):
		// The record is what refuses the ordinary second reveal; the store's own
		// answer is what refuses the two that arrive together, where the second
		// one's source is already spent by the first. A person is told one thing
		// either way, because the thing to do about it is the same.
		refuseAPI(w, http.StatusTooManyRequests, "reveal_too_soon", "A workspace's key may be revealed once a day. Ask again when the window opens.")
	default:
		failAPI(w, err)
	}
}

// refuseLicenceFile answers the refusal one failed download earns. A licence
// whose key is revealed has no file to hand over, and that is the one refusal
// here a person acts on by using the other route.
func refuseLicenceFile(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errNoSuchWorkspace):
		refuseAPI(w, http.StatusNotFound, "not_found", "This workspace is not one you may read a licence file for.")
	case errors.Is(err, ErrSSORequired), errors.Is(err, ErrDirectoryNotPinned):
		refuseAPI(w, http.StatusForbidden, "sso_required", "This organisation's members sign in with Google or Microsoft. Sign in that way to use it.")
	case errors.Is(err, errNoLicence):
		refuseAPI(w, http.StatusConflict, "no_licence", "This workspace has no licence, so there is no file to download.")
	case errors.Is(err, errNotAirGapped):
		refuseAPI(w, http.StatusConflict, "not_air_gapped", "This licence's key is revealed in the portal rather than downloaded as a file.")
	default:
		failAPI(w, err)
	}
}
