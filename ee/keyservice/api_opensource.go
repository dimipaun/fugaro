// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The portal's open-source route (portal plan P3.3; D2185, D2199): the ask a
// signed-in maintainer makes for a licence under the open-source programme, and
// the queue entry it leaves for a person to decide (P5.1).
//
// It is the portal's half of §4.4 and takes the place of the website's
// `/open-source` form (D2185: the three website forms retire with their routes,
// P9.3), so what it applies is that form's own two checks and nothing new: every
// repository named is public under a licence the OSI approves, and control of
// them is proved (`checks.go`'s `openSourceControl` — the same function the form
// calls, so the programme cannot come to mean two things). What it does **not**
// do is mint: an approval is a person's (F47 — a public repository proves
// nothing about who is asking, and §17 puts the licence itself in the owner's
// hands), so the request is written into the manual queue and the operations
// team decides it (queue.go, P5.1).
//
// The account is the one the portal's sign-in proved and never one the body
// names: a request is filed against the address a person really holds, and a
// body carrying an address at all is a body this route does not read
// (`decodeWire`, §4.1). The organisation half of an open-source grant is the one
// the proof showed control of — the repository's own namespace — which is why
// this route reads no portal organisation and needs none to exist (P2.1): the
// programme is open to a maintainer who has founded nothing in the portal yet,
// and the operations team has the address to ask.
//
// The answer carries nothing: the applicant holds no key and no id, the queue's
// own record is the owner's page's (§4.5), and what the screen draws is its own
// sentence about a request that is waiting.

import (
	"io"
	"log"
	"net/http"
	"strings"
)

// RouteAPIOpenSource is the open-source ask's door on the portal's API (P1.0):
// the SPA's own origin reaches it through Hosting's rewrite (F21), which is why
// it is registered under the subtree rather than mounted by Handler.
//
// It is exported and its own constant, and named with the `/api` prefix for the
// reason `RouteTrial` is (api_trial.go): the SPA posts to this path
// (ee/portal/src/screens/workspaces/workspaces.tsx), and a path that lives in two
// files is a path that drifts between them. The website's own form posted to a
// route of its own, `/open-source`, and that door retired with its route in P9.3
// (service.go): the two bodies differ because the two clients do, and this ask
// is the one that remains.
const RouteAPIOpenSource = "/api/open-source"

// maxOpenSourceBodyBytes is what an open-source ask may body: the repositories
// the licence would cover, as their URLs, one entry each, and the deploy's own
// cap on how many that is (Config.MaxRepositories, whose default is eight). The
// cap here is generous against that — a URL is bounded well under a kilobyte by
// the host's own naming rule — and it is the mount's rule read on this route
// rather than a size one field needs (api.go): a door a signed-in stranger can
// post to is one whose body the service has to hold.
const maxOpenSourceBodyBytes = 4 << 10

func init() {
	// The route reads the sign-in to learn the account it is filed against and
	// the store to write the queue entry, and it says so, so that a deployment
	// serving it without either is refused when it starts rather than answering
	// every ask with a failure of its own (api.go's `reaching`).
	registerAPI(http.MethodPost, RouteAPIOpenSource, (*Service).apiRequestOpenSource,
		withBody(maxOpenSourceBodyBytes), reaching(needDocs, needSignIn))
}

// OpenSourceCall is what an open-source ask carries: the repositories the
// licence would cover, as the URLs the applicant pasted, one entry each.
//
// It carries no address, no organisation and no term: the address is the one the
// sign-in proved (§4.1), the organisation is what the proof check answers
// (checks.go), and the term is the plan's row's (D2133, P1.8). A body naming any
// of them is refused rather than ignored, because `decodeWire` is strict and
// because a request the service files against one account while the body names
// another is exactly the confusion §4.4's checks exist to prevent.
//
// The shape has no entry in the wire registry (wire.go), for TrialCall's reason:
// a registered shape is a JSON Schema, a fixture and the SPA's own types beside
// it, and those are the portal's wire area's files rather than this task's. What
// that leaves untested is the SPA's agreement with this shape, not this side's
// reading of it.
type OpenSourceCall struct {
	Repositories []string `json:"repositories"`
}

// The refusals this route answers with. Each is a code a client branches on and
// a sentence a person reads, and no sentence says anything about the service's
// inside (§7.2's rule, api.go's refusal shape).
const (
	openSourceRateRefusal       = "Too many requests from this account just now. Try again shortly."
	openSourceBodyRefusal       = "This request is not the ask this route reads: it names the repositories the licence would cover, as their URLs, one entry each, and nothing else."
	openSourceEmptyRefusal      = "An open-source request names at least one repository, and this one names none. Add the repositories the licence would cover."
	openSourceManyRefusal       = "An open-source request names more repositories than one request reads. Name the repositories the licence would cover, and ask again for the ones this request left out."
	openSourceRepositoryRefusal = "Every repository is named as its https URL on the code host this deployment reads, as https://<host>/owner/name. One of these entries is not one of that host's repositories."
	openSourceChecksRefusal     = "This request does not meet the open-source programme's conditions, so nothing was queued: every repository has to be public under a licence the OSI approves, and control of it has to be proved. The answer does not say which of those the request missed."
	openSourceSignInRefusal     = "This sign-in names no address, so there is nothing for an open-source request to be filed against. Sign in again."
)

// apiRequestOpenSource is the open-source programme's door: a signed-in account
// names the repositories it maintains, the checks decide whether the programme's
// conditions are met, and a request that meets them is written into the queue for
// a person to answer.
//
// The order is the address's allowance, then the sign-in, then the body and the
// lines it names, then the account's allowance, then the two checks, then the
// queue entry. The address's allowance is spent first for the reason renew.go's
// is (a body this service never parses is as cheap to serve as a good one); the
// sign-in is read before the body because the account is what the request is
// filed against and what the second allowance counts; the lines are read against
// the deploy's own host before any of them is asked about (§4.4 — one line the
// host does not serve refuses the whole request, so the record is never a list
// with a stranger's address in it); the account's allowance is spent after both
// of those and before the code host is called, because the checks are calls into
// the deploy's own credentials and a request that is going to be refused must
// not make them; and the entry is written last, so nothing is left in the queue
// by a request the checks refused.
func (s *Service) apiRequestOpenSource(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.allow("ip:" + clientIP(r)) {
		refuseAPI(w, http.StatusTooManyRequests, "rate_limited", openSourceRateRefusal)
		return
	}
	actor, ok := s.apiCaller(w, r)
	if !ok {
		return
	}
	// The address the sign-in proved is what the request is filed against and
	// what the owner reads it by, and it is read the way every other part of this
	// service reads one (§4.1's `address`): a verifier that proved no address, or
	// one that is not an address, has not done the half of its job this route
	// needs, and a spelling of its own would file one account's request under
	// another's.
	email, readable := address(actor.Email)
	if !readable {
		refuseAPI(w, http.StatusUnauthorized, "unauthenticated", openSourceSignInRefusal)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		refuseAPI(w, http.StatusBadRequest, "unreadable_body", "This request's body could not be read.")
		return
	}
	var call OpenSourceCall
	if err := decodeWire(body, &call); err != nil {
		refuseAPI(w, http.StatusBadRequest, "unreadable_body", openSourceBodyRefusal)
		return
	}
	// An entry that is blank is dropped rather than refused, which is `splitLines`'
	// own reading of the field: a pasted list arrives with a
	// blank line in it, and that is not a repository. An entry that is a line at
	// all is one the applicant meant to name, and every one of those is read
	// below.
	lines := make([]string, 0, len(call.Repositories))
	for _, raw := range call.Repositories {
		if line := strings.TrimSpace(raw); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		refuseAPI(w, http.StatusBadRequest, "unreadable_body", openSourceEmptyRefusal)
		return
	}
	if len(lines) > s.cfg.MaxRepositories {
		refuseAPI(w, http.StatusBadRequest, "unreadable_body", openSourceManyRefusal)
		return
	}
	repositories := make([]RepositoryRef, 0, len(lines))
	named := make([]string, 0, len(lines))
	seen := make(map[string]bool, len(lines))
	for _, line := range lines {
		repository, served := s.repository(line)
		if !served {
			refuseAPI(w, http.StatusUnprocessableEntity, "unserved_repository", openSourceRepositoryRefusal)
			return
		}
		// One repository spelled twice is one repository: `…/widget`, `…/widget.git`
		// and `…/widget/` are the same line of a pasted list, and what the record
		// holds is the set the licence would cover. Without this the owner's page
		// lists the repository twice, the notification names it twice and the host
		// is asked about it twice, which is a duplicate and not a second repository.
		if seen[repository.URL] {
			continue
		}
		seen[repository.URL] = true
		repositories = append(repositories, repository)
		named = append(named, repository.URL)
	}
	// The account's own allowance, and it is the address's rather than the
	// domain's: what is scarce here is a person's attention on the queue (P5.1),
	// and the queue is read by the address a request is filed against.
	if !s.limiter.allow("address:" + email) {
		refuseAPI(w, http.StatusTooManyRequests, "rate_limited", openSourceRateRefusal)
		return
	}
	if s.proofs == nil {
		// A deployment that names no proof check refuses every request here
		// (openSourceControl, F47: a public repository says nothing about who is
		// asking), and the applicant is answered the refusal the checks earn
		// rather than a failure. What the cause is — a deployment incomplete for
		// D452's open question — is the operator's to read, and it is written
		// here rather than in the answer, which says nothing about the service's
		// inside (§7.2).
		log.Printf("keyservice: an open-source request was refused because this deployment names no proof of control (D452)")
	}
	organisation, passed := s.openSourceControl(r.Context(), OpenSource{Email: email, Repositories: repositories})
	if !passed {
		refuseAPI(w, http.StatusUnprocessableEntity, "not_eligible", openSourceChecksRefusal)
		return
	}
	id, err := newID()
	if err != nil {
		failAPI(w, err)
		return
	}
	if err := s.enqueue(r.Context(), Application{
		ID: id,
		// The plan names what is being asked for, which is what the owner reads
		// the entry by: the programme's own plan and not the term, which is the
		// row's to give (D2133).
		Plan:   s.openSource.Name,
		Email:  email,
		Domain: domainOf(email),
		// The organisation a key would bind to is the one the proof showed
		// control of (§4.4) and not a portal organisation: nothing here reads the
		// caller's memberships, and a maintainer who has founded none can still
		// ask.
		Organisation: organisation,
		// What is stored is the canonical URL of each repository on the deploy's
		// own host, and never the line the applicant typed: the record is what a
		// person reads and what a later check would re-read (§4.4, §7.3).
		Repositories: named,
		At:           s.now(),
	}); err != nil {
		failAPI(w, err)
		return
	}
	// 204 and no body: the request is filed and nothing about it is decided (P5.1),
	// and the applicant holds nothing to keep — no key, no id a screen shows — so
	// there is no body to read and none is promised. 202 would say as much, but the
	// SPA's client reads a body wherever there is one and answers 204 as the one
	// status with nothing to parse (`ee/portal/src/api/client.ts`), so the status
	// this route answers is the one shape both sides already agree on.
	w.WriteHeader(http.StatusNoContent)
}
