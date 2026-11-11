// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The admin page: one test per row of the design's §6 defence table
// (subscriptions plan S3.4; docs/design/key-signing-service.md §6, §5.5 and
// Check 173). The page is a second authenticated surface that can mint **any**
// key for **any** tier, so every test here is about a defence that has to hold
// on it: who reaches it, what a cross-site request can make it do, what an
// applicant's text is on it, what every mint writes down, what a burst meets,
// and that the key never appears on the page at all.

import (
	"context"
	"errors"
	"html"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/ee/licence"
)

// TestAdminRefusesAnyoneButTheOwner: §6's first row and Check 173 step 1 — the
// page is reachable only by the project's owner account. Nobody signed in is
// refused, every other account is refused, and so is every action: a refusal
// that only covered the page's render would leave the two POSTs that mint
// open.
//
// What the refusal says is nothing. The page's existence, its queue and the
// fact that this deployment has one are not a stranger's business, so what a
// stranger gets is the answer they would get for a path that is not here at all
// (§7.1 read the other way round: uniformity is for applicants, and for
// everyone else it is silence).
func TestAdminRefusesAnyoneButTheOwner(t *testing.T) {
	f := newFixture(t)
	answerBody(t, f.post(RouteRequest, url.Values{"email": {applicantAddress}, "request": {"a multi-seat trial"}}))

	mint := url.Values{
		"plan": {paidPlanName}, "seats": {"2"}, "term_days": {"365"},
		"organisation": {"acme.example"}, "step_up": {stepUpCode},
		"token": {f.pageToken(ownerAccount, actionMint)},
	}
	decide := f.decideForm(f.open()[0].ID)

	for _, tc := range []struct {
		name    string
		account string
	}{
		{name: "no session at all", account: ""},
		{name: "an account that is not the owner's", account: "someone@else.example"},
		{name: "another account at the owner's own domain", account: "second@fugaro.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.owner.account = tc.account
			page := f.adminGet()
			if page.Code != http.StatusNotFound {
				t.Errorf("the page answered %d, want 404: %s", page.Code, page.Body.String())
			}
			if strings.Contains(page.Body.String(), "queue") || strings.Contains(page.Body.String(), "licence") {
				t.Errorf("the refusal describes the page: %q", page.Body.String())
			}
			if rec := f.adminPost(RouteAdminMint, mint); rec.Code != http.StatusNotFound {
				t.Errorf("a mint answered %d, want 404: %s", rec.Code, rec.Body.String())
			}
			if rec := f.adminPost(RouteAdminDecide, decide); rec.Code != http.StatusNotFound {
				t.Errorf("a decline answered %d, want 404: %s", rec.Code, rec.Body.String())
			}
			// And the page's own routes asked the wrong way answer the same:
			// the patterns carry no method, so nothing reaches the mux's own
			// 405 and its Allow header, which would say the page is here.
			for _, probe := range []struct {
				name string
				rec  *httptest.ResponseRecorder
			}{
				{"the mint route read as a page", f.get(RouteAdminMint, url.Values{})},
				{"the page written to", f.adminPost(RouteAdmin, mint)},
				{"the page's path with a trailing slash", f.get(RouteAdmin+"/", url.Values{})},
				{"a path that is not here at all", f.get("/nowhere", url.Values{})},
			} {
				if probe.rec.Code != http.StatusNotFound {
					t.Errorf("%s answered %d, want 404: %s", probe.name, probe.rec.Code, probe.rec.Body.String())
				}
				if allow := probe.rec.Header().Get("Allow"); allow != "" {
					t.Errorf("%s answered Allow: %s, and that says the page is here", probe.name, allow)
				}
			}
			if got := len(f.recording.keys); got != 0 {
				t.Errorf("a request from %q minted %d key(s), want none", tc.account, got)
			}
			if got := len(f.auditor.lines); got != 0 {
				t.Errorf("a request from %q wrote %d audit line(s), want none", tc.account, got)
			}
			if got := len(f.queued()); got != 1 || len(f.open()) != 1 {
				t.Errorf("a request from %q changed the queue: %+v", tc.account, f.open())
			}
		})
	}

	// And the owner's own account gets the page, which is what makes the three
	// refusals above a rule rather than a page that does not exist.
	f.owner.account = ownerAccount
	page := f.adminGet()
	if page.Code != http.StatusOK {
		t.Fatalf("the owner's page answered %d, want 200: %s", page.Code, page.Body.String())
	}
	if !strings.Contains(page.Body.String(), f.open()[0].ID) {
		t.Error("the page does not show the request waiting in the queue")
	}

	// The owner gets the same 404 for the page's routes asked the wrong way, so
	// what answers there is the page's surface and not the account: the two
	// refusals are one answer, and nobody learns which of them they met.
	for _, probe := range []struct {
		name string
		rec  *httptest.ResponseRecorder
	}{
		{"the mint route read as a page", f.get(RouteAdminMint, url.Values{})},
		{"the page written to", f.adminPost(RouteAdmin, mint)},
		{"the page's path with a trailing slash", f.get(RouteAdmin+"/", url.Values{})},
	} {
		if probe.rec.Code != http.StatusNotFound {
			t.Errorf("the owner's %s answered %d, want 404: %s", probe.name, probe.rec.Code, probe.rec.Body.String())
		}
	}
}

// TestAdminStartsAPlanlessRowOnThePagePlan: a request the manual form
// queued named no plan, so the numbers the page renders for it are the page
// plan's and not the form's own one. The numbers a row renders are what the
// owner's next click submits, so a row that started at one rendered "Term days
// 1" and approving it minted and mailed a one-day key — and nothing takes that
// back: the approval is idempotent on the request and a decline is refused once
// a key exists.
func TestAdminStartsAPlanlessRowOnThePagePlan(t *testing.T) {
	f := newFixtureWith(t, Production, func(f *fixture) {
		// The page plan's own numbers, and neither is one the page renders
		// anywhere else: the Order-Form form's are the fixture's 1 and 365.
		f.cfg.PagePlans = []Plan{{
			Name: enterprisePlanName, Tier: licence.TierEnterprise,
			Features: []string{"seats"}, Seats: 40, TermDays: 90,
		}}
	})
	answerBody(t, f.post(RouteRequest, url.Values{"email": {applicantAddress}, "request": {"a multi-seat trial"}}))

	open := f.open()
	if len(open) != 1 || open[0].Plan != "" {
		t.Fatalf("the manual form queued %+v, want one request that named no plan", open)
	}

	page := f.adminGet()
	if page.Code != http.StatusOK {
		t.Fatalf("the page answered %d, want 200: %s", page.Code, page.Body.String())
	}
	for _, want := range []string{`name="seats" value="40"`, `name="term_days" value="90"`} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("the queue row does not render %s: %s", want, page.Body.String())
		}
	}

	// The numbers the row rendered are the ones the click submits. The
	// organisation is the owner's own field — a manual request names none —
	// and the address is the request's, which the form leaves as the page
	// rendered it.
	rec := f.adminPost(RouteAdminMint, f.mintForm(url.Values{
		"application": {open[0].ID}, "plan": {enterprisePlanName},
		"seats": {"40"}, "term_days": {"90"}, "organisation": {"acme.example"},
	}))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("the approval answered %d, want 303: %s", rec.Code, rec.Body.String())
	}
	records := f.records()
	if len(records) != 1 {
		t.Fatalf("the approval wrote %d record(s), want one", len(records))
	}
	if got := records[0].Seats; got != 40 {
		t.Errorf("the key carries %d seat(s), want the row's 40", got)
	}
	if got := records[0].Expires.Sub(records[0].Issued); got != 90*24*time.Hour {
		t.Errorf("the key runs for %s, want the row's 90 days", got)
	}
}

// TestAdminRefusesACrossSiteRequest: §6's CSRF row and Check 173 step 1 — the
// page's own state is checked on every action, so a form on a page the owner
// visits cannot make the owner's session mint. The check is a token the page
// renders and signs, and the properties that make it one are that it is bound to
// the account and to the action, that it expires, and that nobody without the
// deploy's secret can make one.
//
// A stateless token rather than a cookie the page remembers, because the intake
// runs on more than one instance and a page that held its state in memory would
// refuse the owner's second click at random (§3.4).
func TestAdminRefusesACrossSiteRequest(t *testing.T) {
	f := newFixture(t)
	answerBody(t, f.post(RouteRequest, url.Values{"email": {applicantAddress}, "request": {"a multi-seat trial"}}))
	id := f.open()[0].ID

	fields := url.Values{
		"application": {id}, "plan": {paidPlanName}, "seats": {"2"},
		"term_days": {"365"}, "organisation": {"acme.example"},
		"step_up": {stepUpCode},
	}
	post := func(token string) *http.Response {
		form := url.Values{}
		for name, values := range fields {
			form[name] = values
		}
		if token != "" {
			form.Set("token", token)
		}
		return f.adminPost(RouteAdminMint, form).Result()
	}

	// A token the page rendered one window ago: the page's own, kept from before
	// the clock moved past the period it lives.
	stale := f.pageToken(ownerAccount, actionMint)
	f.advance(16 * time.Minute)

	// A token signed with a secret that is not this deploy's. It is built by a
	// service with the deploy's own shape — its plans, its account, the period
	// its forms live, and the clock, so the token is **live** — and somebody
	// else's secret, which is exactly what a forged one would be. Anything less
	// than that whole shape would be refused by one of the other checks and the
	// secret itself would go unread.
	foreign, err := forgedToken(f.svc.cfg, f.clock)
	if err != nil {
		t.Fatalf("building the forged token: %v", err)
	}

	for _, tc := range []struct {
		name  string
		token string
	}{
		{name: "no token at all", token: ""},
		{name: "a token the page rendered for the other action", token: f.pageToken(ownerAccount, actionDecide)},
		{name: "a token another account's page rendered", token: f.pageToken("someone@else.example", actionMint)},
		{name: "a token one window old", token: stale},
		{name: "a token signed with another secret", token: foreign},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := post(tc.token)
			defer rec.Body.Close()
			if rec.StatusCode != http.StatusForbidden {
				t.Errorf("the action answered %d, want 403", rec.StatusCode)
			}
			if got := len(f.recording.keys); got != 0 {
				t.Errorf("a crossed-site action minted %d key(s), want none", got)
			}
			if got := len(f.auditor.lines); got != 0 {
				t.Errorf("a crossed-site action wrote %d audit line(s), want none", got)
			}
			if got := len(f.open()); got != 1 {
				t.Errorf("a crossed-site action changed the queue: %+v", f.open())
			}
		})
	}

	// The clock the page is read at, and the token it renders now: the page's own
	// is the one that works, so the five refusals above are the token's
	// properties and not the route's silence.
	f.advance(-16 * time.Minute)
	if rec := post(f.pageToken(ownerAccount, actionMint)); rec.StatusCode != http.StatusSeeOther {
		t.Errorf("the page's own token answered %d, want 303", rec.StatusCode)
	}
	if got := len(f.recording.keys); got != 1 {
		t.Errorf("the page's own token minted %d key(s), want one", got)
	}
}

// forgedToken is what a page token looks like when somebody who is not this
// deploy signs one. It is built from the deploy's own configuration with
// another secret in it, so the token is live, inside its period, bound to the
// owner's account and to the minting action — and refused by the signature, and
// by the signature alone.
func forgedToken(cfg Config, now func() time.Time) (string, error) {
	forged := &Service{cfg: cfg, now: now}
	forged.cfg.AdminSecret = []byte(otherAdminSecret)
	return forged.pageToken(ownerAccount, actionMint)
}

// TestQueueEscapesApplicantText: §6's escaping row and Check 173 step 4 — every
// field an applicant typed is escaped on render, so queued text cannot become a
// stored script in the page that can mint. The page is rendered by
// html/template and the applicant's text is a text node in it, and the page
// carries no script at all, which is what its policy says as well.
func TestQueueEscapesApplicantText(t *testing.T) {
	f := newFixture(t)
	hostile := `<script>alert("x")</script><img src=x onerror="alert(1)">`
	answerBody(t, f.post(RouteRequest, url.Values{"email": {applicantAddress}, "request": {hostile}}))

	page := f.adminGet()
	if page.Code != http.StatusOK {
		t.Fatalf("the page answered %d, want 200: %s", page.Code, page.Body.String())
	}
	body := page.Body.String()

	// The page can mint: this is §6's row about exactly the surface that can.
	if !strings.Contains(body, `action="`+RouteAdminMint+`"`) {
		t.Error("the page has no mint form, so the escaping asserted here is not the escaping of the page that can mint")
	}
	if !strings.Contains(body, "onerror") {
		t.Error("the applicant's text is not in the page at all, so nothing about its render was read")
	}
	for _, markup := range []string{"<script>", "<img", "onerror=\"", "</script>"} {
		if strings.Contains(body, markup) {
			t.Errorf("the page carries %q unescaped: the applicant's text is a script in a page that can mint, which is T13 item 8", markup)
		}
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("the applicant's text is nowhere as escaped text either: %q", body)
	}

	// The page says what it may load, and it says none of it: a page that can
	// mint any key for any tier is not one to fetch anything from.
	policy := page.Header().Get("Content-Security-Policy")
	for _, directive := range []string{"default-src 'none'", "form-action 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(policy, directive) {
			t.Errorf("the page's policy is %q and does not carry %q", policy, directive)
		}
	}
	if page.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("the page is cacheable (%q), and the queue is a list of people", page.Header().Get("Cache-Control"))
	}
}

// TestAdminWithoutASecondFactorMintsNothing: §6's step-up row and **D455** — a
// deployment that names no factor is one the page answers read-only, and every
// minting action is refused rather than falling back on the session.
//
// The guard is worth a test of its own because the failure it prevents is
// silent: a page that minted on the session alone would mint any key for any
// tier from a stolen cookie, which is the whole of what the row is about. What
// still works is the reading and the decline — a queue the owner cannot work
// through is a queue that grows.
func TestAdminWithoutASecondFactorMintsNothing(t *testing.T) {
	f := newFixtureWith(t, Production, func(f *fixture) { f.deps.StepUp = nil })
	answerBody(t, f.post(RouteRequest, url.Values{"email": {applicantAddress}, "request": {"a multi-seat trial"}}))
	open := f.open()
	if len(open) != 1 {
		t.Fatalf("the manual form left %d open request(s), want one", len(open))
	}

	page := f.adminGet()
	if page.Code != http.StatusOK {
		t.Fatalf("the page answered %d, want 200: %s", page.Code, page.Body.String())
	}
	body := page.Body.String()
	if !strings.Contains(body, html.EscapeString(refuseNoFactor)) {
		t.Errorf("the page does not say why it can mint nothing: %q", body)
	}
	if strings.Contains(body, `action="`+RouteAdminMint+`"`) {
		t.Error("the page renders a mint form whose every submission it will refuse")
	}

	// The action is refused with the page's own live token and a code: the
	// deployment has nothing to check the code against, so there is nothing a
	// caller can send that makes it a mint.
	if rec := f.adminPost(RouteAdminMint, f.mintForm(url.Values{
		"application": {open[0].ID}, "plan": {paidPlanName}, "seats": {"2"},
		"term_days": {"365"}, "organisation": {"acme.example"},
	})); rec.Code != http.StatusForbidden {
		t.Errorf("a mint from a page with no second factor answered %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if got := len(f.recording.keys); got != 0 {
		t.Errorf("a page with no second factor minted %d key(s), want none (D455)", got)
	}
	if got := len(f.auditor.lines); got != 0 {
		t.Errorf("a page with no second factor wrote %d audit line(s), want none: no key was returned", got)
	}
	if got := len(f.open()); got != 1 {
		t.Errorf("a refused mint changed the queue: %+v", f.open())
	}

	// Declining is not a minting action, so it is not the factor's: §6's step-up
	// is per mint, and the owner still has to be able to clear the queue.
	if rec := f.adminPost(RouteAdminDecide, f.decideForm(open[0].ID)); rec.Code != http.StatusSeeOther {
		t.Errorf("a decline from a page with no second factor answered %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := len(f.open()); got != 0 {
		t.Errorf("the decline did not take the request out of the queue: %+v", f.open())
	}
}

// TestAdminRefusesAMintTheFactorDidNotConfirm: §6's step-up row and **D455** —
// the second factor is checked on every minting action, and a code that is
// missing, unknown, refused or unchecked is a refused mint. Nothing here is
// about the form: the page's own live token and the owner's own session are in
// every request, so what the four refusals read is what the factor answered.
//
// The failure this holds is the silent one: a guard that fell back on the
// session when the factor said no would mint any key for any tier on a stolen
// cookie, and a run whose conclusion is "the page refused it" would read a key.
func TestAdminRefusesAMintTheFactorDidNotConfirm(t *testing.T) {
	f := newFixture(t)
	answerBody(t, f.post(RouteRequest, url.Values{"email": {applicantAddress}, "request": {"a multi-seat trial"}}))

	fields := url.Values{
		"application": {f.open()[0].ID}, "plan": {paidPlanName}, "seats": {"2"},
		"term_days": {"365"}, "organisation": {"acme.example"},
	}
	// A fresh token each time, so every request below is one the page itself
	// rendered, and the only thing that differs is the code.
	mint := func(code string, carried bool) *httptest.ResponseRecorder {
		form := url.Values{"token": {f.pageToken(ownerAccount, actionMint)}}
		for name, values := range fields {
			form[name] = values
		}
		if carried {
			form.Set("step_up", code)
		}
		return f.adminPost(RouteAdminMint, form)
	}
	refused := func(what string, rec *httptest.ResponseRecorder) {
		t.Helper()
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s answered %d, want 403: %s", what, rec.Code, rec.Body.String())
		}
		if got := len(f.recording.keys); got != 0 {
			t.Errorf("%s minted %d key(s), want none", what, got)
		}
		if got := len(f.auditor.lines); got != 0 {
			t.Errorf("%s wrote %d audit line(s), want none: no key was returned", what, got)
		}
		if got := len(f.mail.sent); got != 0 {
			t.Errorf("%s mailed %d message(s), want none", what, got)
		}
		if got := len(f.open()); got != 1 || f.open()[0].Decision != Undecided {
			t.Errorf("%s changed the queue: %+v", what, f.open())
		}
	}

	none := mint("", false)
	refused("a mint with no code at all", none)
	if got := f.stepUp.askedCodes(); len(got) != 0 {
		t.Errorf("a mint with no code asked the factor %d time(s): an empty field is refused before anything is checked against it", len(got))
	}
	if body := none.Body.String(); !strings.Contains(body, refuseNoStepUp) {
		t.Errorf("the refusal says %q, want the sentence that names the missing code", body)
	}

	refused("a mint with a code the factor does not know", mint("000000", true))
	f.stepUp.accept = false
	refused("a mint the factor refuses", mint(stepUpCode, true))
	f.stepUp.accept = true
	asked := f.stepUp.askedCodes()
	if len(asked) != 2 {
		t.Errorf("the factor was asked %d time(s), want two: a code has to reach it to be refused by it (%v)", len(asked), asked)
	}
	if want := ownerAccount + " 000000"; len(asked) > 0 && asked[0] != want {
		t.Errorf("the factor was asked %q, want %q: the code the form carried, for the account it is a factor for", asked[0], want)
	}

	// The factor's own service failing is not a confirmation either, and it is an
	// answer rather than a refusal: nothing was checked, so nothing is minted.
	f.stepUp.err = errors.New("the factor is not answering")
	rec := mint(stepUpCode, true)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("a mint while the factor is failing answered %d, want 500: %s", rec.Code, rec.Body.String())
	}
	if got := len(f.recording.keys); got != 0 {
		t.Errorf("a mint the factor could not check produced %d key(s), want none: an unchecked factor is not a confirmed one", got)
	}
	if got := len(f.auditor.lines); got != 0 {
		t.Errorf("a mint the factor could not check wrote %d audit line(s), want none", got)
	}
	f.stepUp.err = nil

	// And the code the factor does confirm is a mint, which is what makes the
	// five refusals above the factor's answers rather than the route's silence.
	if rec := mint(stepUpCode, true); rec.Code != http.StatusSeeOther {
		t.Fatalf("a confirmed mint answered %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := len(f.recording.keys); got != 1 {
		t.Errorf("the confirmed mint produced %d key(s), want one", got)
	}
	if got := len(f.open()); got != 0 {
		t.Errorf("the approved request is still in the queue: %+v", f.open())
	}
}

// TestAdminRefusesAMintWithNowhereToSendTheKey: the last guard in front of the
// minter, and the only one between an edited form and a key nobody is told
// about. A key is mailed and never stored (§7.3), so a form whose address is not
// one — the field the owner can edit on an approval, and the one field of an
// Order-Form mint nothing else fills in — would reach `s.mint` with `to` empty:
// the minter signs, `s.mint` skips the mail it has no address for and records
// the key against §5.2's source, and the page answers 303 as if it had been
// delivered. What the owner is left with is a licence in the audit line and the
// record and in nobody's hands. The organisation is the same guard's other half:
// it is written into the key's own payload, and a mint that does not say who it
// is for is one the owner cannot answer either.
func TestAdminRefusesAMintWithNowhereToSendTheKey(t *testing.T) {
	f := newFixture(t)
	answerBody(t, f.post(RouteRequest, url.Values{"email": {applicantAddress}, "request": {"a multi-seat trial"}}))
	open := f.open()
	if len(open) != 1 {
		t.Fatalf("the manual form left %d open request(s), want one", len(open))
	}
	// The request carries the applicant's own address, so what the refusals below
	// read is the edited field and not a request that never had one.
	if open[0].Email != applicantAddress {
		t.Fatalf("the request carries %q, want the applicant's address", open[0].Email)
	}
	fields := url.Values{
		"application": {open[0].ID}, "plan": {paidPlanName}, "seats": {"2"},
		"term_days": {"365"}, "organisation": {"acme.example"},
	}

	for _, raw := range []string{
		"not an address", "acme.example", "owner@", "dev@acme", "two addresses@acme.example",
	} {
		form := url.Values{"email": {raw}}
		for name, values := range fields {
			form[name] = values
		}
		rec := f.adminPost(RouteAdminMint, f.mintForm(form))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("a mint to %q answered %d, want 400: %s", raw, rec.Code, rec.Body.String())
		}
		if body := strings.TrimSpace(rec.Body.String()); body != refuseFields {
			t.Errorf("a mint to %q was refused with %q, want the sentence that names what a mint needs", raw, body)
		}
	}

	// The organisation's half of the guard, on the branch that has no request to
	// take one from (§5.5's Order-Form mint), with everything else in the form.
	unowned := f.adminPost(RouteAdminMint, f.mintForm(url.Values{
		"plan": {enterprisePlanName}, "seats": {"40"}, "term_days": {"365"},
		"organisation": {"   "}, "email": {"licensing@acme.example"},
	}))
	if unowned.Code != http.StatusBadRequest {
		t.Errorf("a mint naming no organisation answered %d, want 400: %s", unowned.Code, unowned.Body.String())
	}

	// Nothing was signed, written down, mailed or recorded — and the request the
	// refusals named is still the owner's to answer.
	if got := f.signer.calls; got != 0 {
		t.Errorf("a refused mint signed %d time(s), want none: a key signed for a form with nowhere to send it is a key nothing accounts for", got)
	}
	if got := len(f.mintedKeys()); got != 0 {
		t.Errorf("a refused mint produced %d key(s), want none", got)
	}
	if got := len(f.auditor.lines); got != 0 {
		t.Errorf("a refused mint wrote %d audit line(s), want none: no key was returned", got)
	}
	if got := len(f.mail.sent); got != 0 {
		t.Errorf("a refused mint mailed %d message(s), want none", got)
	}
	if got := len(f.records()); got != 0 {
		t.Errorf("a refused mint recorded %d issuance(s), want none", got)
	}
	if got := f.open(); len(got) != 1 || got[0].Decision != Undecided {
		t.Errorf("a refused mint changed the queue: %+v", got)
	}

	// And the same form with the request's own address is the mint the refusals
	// above held back, so they are the field's answers and not the route's silence.
	if rec := f.adminPost(RouteAdminMint, f.mintForm(fields)); rec.Code != http.StatusSeeOther {
		t.Fatalf("the same form with an address answered %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := len(f.mintedKeys()); got != 1 {
		t.Errorf("the mint that was allowed produced %d key(s), want one", got)
	}
	if got := len(f.mail.sent); got != 1 || f.mail.sent[0].To != applicantAddress {
		t.Errorf("the key went to %+v, want the request's own address (%q)", f.mail.sent, applicantAddress)
	}
}

// TestAdminRefusesAMintForARequestThatIsNotInTheQueue: the form names the
// request it answers, and an id that names none is refused rather than minted.
// The two branches of the one action differ in what makes a mint repeat
// (§5.5): an approval keys on the request it answers and an Order-Form mint on
// the page render, so an id with no request behind it that fell through to the
// form's branch would mint against a source carrying that id — a key issued
// under a name the page cannot read back, for a request the owner meant to
// answer and which stays in the queue.
func TestAdminRefusesAMintForARequestThatIsNotInTheQueue(t *testing.T) {
	f := newFixture(t)
	answerBody(t, f.post(RouteRequest, url.Values{"email": {applicantAddress}, "request": {"a multi-seat trial"}}))
	open := f.open()
	if len(open) != 1 {
		t.Fatalf("the manual form left %d open request(s), want one", len(open))
	}

	for _, id := range []string{"app-00000000-0000-4000-8000-000000000000", "a-request-that-was-never-made"} {
		rec := f.adminPost(RouteAdminMint, f.mintForm(url.Values{
			"application": {id}, "plan": {paidPlanName}, "seats": {"2"},
			"term_days": {"365"}, "organisation": {"acme.example"}, "email": {applicantAddress},
		}))
		if rec.Code != http.StatusNotFound {
			t.Errorf("a mint for the unknown request %q answered %d, want 404: %s", id, rec.Code, rec.Body.String())
		}
		if body := strings.TrimSpace(rec.Body.String()); body != refuseUnknown {
			t.Errorf("a mint for %q was refused with %q, want the sentence that names the missing request", id, body)
		}
	}
	if got := f.signer.calls; got != 0 {
		t.Errorf("a mint for a request that is not there signed %d time(s), want none", got)
	}
	if got := len(f.mintedKeys()); got != 0 {
		t.Errorf("a mint for a request that is not there produced %d key(s), want none", got)
	}
	if got := len(f.records()); got != 0 {
		t.Errorf("a mint for a request that is not there recorded %d issuance(s), want none: a key under an id the page cannot read back is one the queue never shows", got)
	}
	if got := len(f.auditor.lines); got != 0 {
		t.Errorf("a mint for a request that is not there wrote %d audit line(s), want none", got)
	}
	if got := len(f.mail.sent); got != 0 {
		t.Errorf("a mint for a request that is not there mailed %d message(s), want none", got)
	}
	if got := f.open(); len(got) != 1 || got[0].Decision != Undecided {
		t.Errorf("a refused mint changed the queue: %+v", got)
	}

	// The request the queue does hold is still mintable, which is what makes the
	// two refusals above the id's and not the route's.
	if rec := f.adminPost(RouteAdminMint, f.mintForm(url.Values{
		"application": {open[0].ID}, "plan": {paidPlanName}, "seats": {"2"},
		"term_days": {"365"}, "organisation": {"acme.example"},
	})); rec.Code != http.StatusSeeOther {
		t.Fatalf("the request the queue holds answered %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := len(f.mintedKeys()); got != 1 {
		t.Errorf("the mint that was allowed produced %d key(s), want one", got)
	}
}

// TestAdminRefusesAMintForAPlanThePageDoesNotOffer: the plan field is a name the
// deploy offers rather than one the form chose, and it is what the whole mint is
// read out of — the tier, the features, the seats and the term. A name with
// nothing behind it resolves to an empty plan, so a mint that went through would
// be a key with no tier for the verifier to read and a record naming a plan
// nobody sold (D454's caps are per tier: there is no cap to charge a key with no
// tier to).
func TestAdminRefusesAMintForAPlanThePageDoesNotOffer(t *testing.T) {
	f := newFixture(t)

	for _, name := range []string{"enterprise-plus", "0", "Enterprise"} {
		rec := f.adminPost(RouteAdminMint, f.mintForm(url.Values{
			"plan": {name}, "seats": {"40"}, "term_days": {"365"},
			"organisation": {"acme.example"}, "email": {"licensing@acme.example"},
		}))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("a mint naming the plan %q answered %d, want 400: %s", name, rec.Code, rec.Body.String())
		}
		if body := strings.TrimSpace(rec.Body.String()); body != refusePlan {
			t.Errorf("a mint naming the plan %q was refused with %q, want the sentence that names the plan", name, body)
		}
	}
	if got := f.signer.calls; got != 0 {
		t.Errorf("a mint naming a plan the page does not offer signed %d time(s), want none", got)
	}
	if got := len(f.mintedKeys()); got != 0 {
		t.Errorf("a mint naming a plan the page does not offer produced %d key(s), want none", got)
	}
	if got := len(f.auditor.lines); got != 0 {
		t.Errorf("a mint naming a plan the page does not offer wrote %d audit line(s), want none", got)
	}
	if got := len(f.records()); got != 0 {
		t.Errorf("a mint naming a plan the page does not offer recorded %d issuance(s), want none", got)
	}
	if got := len(f.mail.sent); got != 0 {
		t.Errorf("a mint naming a plan the page does not offer mailed %d message(s), want none", got)
	}

	// The plan the page does offer is the mint those refusals held back, so what
	// they read is the name and not the route.
	if rec := f.adminPost(RouteAdminMint, f.mintForm(url.Values{
		"plan": {enterprisePlanName}, "seats": {"40"}, "term_days": {"365"},
		"organisation": {"acme.example"}, "email": {"licensing@acme.example"},
	})); rec.Code != http.StatusSeeOther {
		t.Fatalf("a mint under the page's own plan answered %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := len(f.mintedKeys()); got != 1 {
		t.Errorf("a mint under the page's own plan produced %d key(s), want one", got)
	}
}

// TestAdminRefusesAMintWithNoSeatsOrTerm: the numbers a key is issued with, when
// neither the form nor the plan names one. What the form leaves out falls back
// on the plan, and a plan without its own seats and term is one this deploy can
// hold — `New` refuses a plan it cannot name or a tier no key is issued for, and
// the numbers are the form's to carry — so the guard is the last thing between
// such a plan and a licence issued for no seats and no days at all.
func TestAdminRefusesAMintWithNoSeatsOrTerm(t *testing.T) {
	f := newFixtureWith(t, Production, func(f *fixture) {
		f.cfg.PagePlans = append(f.cfg.PagePlans, Plan{
			Name: "enterprise-unstated", Tier: licence.TierEnterprise,
			Features: []string{"seats"},
		})
	})

	rec := f.adminPost(RouteAdminMint, f.mintForm(url.Values{
		"plan": {"enterprise-unstated"}, "organisation": {"acme.example"},
		"email": {"licensing@acme.example"},
	}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a mint with no seats and no term answered %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if body := strings.TrimSpace(rec.Body.String()); body != refuseFields {
		t.Errorf("a mint with no seats and no term was refused with %q, want the sentence that names what a mint needs", body)
	}
	if got := f.signer.calls; got != 0 {
		t.Errorf("a mint with no seats and no term signed %d time(s), want none: nothing was issued for zero days", got)
	}
	if got := len(f.mintedKeys()); got != 0 {
		t.Errorf("a mint with no seats and no term produced %d key(s), want none", got)
	}
	if got := len(f.auditor.lines); got != 0 {
		t.Errorf("a mint with no seats and no term wrote %d audit line(s), want none", got)
	}
	if got := len(f.mail.sent); got != 0 {
		t.Errorf("a mint with no seats and no term mailed %d message(s), want none", got)
	}

	// The same plan mints once the form carries the numbers, which is what the
	// page's own form always does.
	if rec := f.adminPost(RouteAdminMint, f.mintForm(url.Values{
		"plan": {"enterprise-unstated"}, "seats": {"40"}, "term_days": {"365"},
		"organisation": {"acme.example"}, "email": {"licensing@acme.example"},
	})); rec.Code != http.StatusSeeOther {
		t.Fatalf("the same plan with the form's own numbers answered %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := len(f.mintedKeys()); got != 1 {
		t.Errorf("the mint that carried its numbers produced %d key(s), want one", got)
	}
}

// TestAdminRefusesADeclineForARequestThatIsNotInTheQueue: the decline's half of
// the same read — the id it names is what the answer is written **to**. A form
// naming no request has nothing to answer, and what a decline of nothing would
// write is an application of its own into the store the queue is drawn from: a
// row with no id, no address and no request behind it, with the page answering
// 303 for a decision on nobody's request.
func TestAdminRefusesADeclineForARequestThatIsNotInTheQueue(t *testing.T) {
	f := newFixture(t)
	answerBody(t, f.post(RouteRequest, url.Values{"email": {applicantAddress}, "request": {"a multi-seat trial"}}))
	open := f.open()
	if len(open) != 1 {
		t.Fatalf("the manual form left %d open request(s), want one", len(open))
	}

	for _, id := range []string{"a-request-that-was-never-made", "app-00000000-0000-4000-8000-000000000000"} {
		rec := f.adminPost(RouteAdminDecide, f.decideForm(id))
		if rec.Code != http.StatusNotFound {
			t.Errorf("a decline of the unknown request %q answered %d, want 404: %s", id, rec.Code, rec.Body.String())
		}
		if body := strings.TrimSpace(rec.Body.String()); body != refuseUnknown {
			t.Errorf("a decline of %q was refused with %q, want the sentence that names the missing request", id, body)
		}
	}
	held := f.queued()
	if len(held) != 1 || held[0].ID != open[0].ID || held[0].Decision != Undecided {
		t.Errorf("a decline for a request that is not there wrote one: %+v", held)
	}

	// The request the queue does hold is still the owner's to decline.
	if rec := f.adminPost(RouteAdminDecide, f.decideForm(open[0].ID)); rec.Code != http.StatusSeeOther {
		t.Fatalf("the request the queue holds answered %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := f.open(); len(got) != 0 {
		t.Errorf("the declined request is still in the queue: %+v", got)
	}
	if got := len(f.mintedKeys()); got != 0 {
		t.Errorf("a decline minted %d key(s), want none", got)
	}
}

// TestEveryMintIsAudited: §6's audit row and Check 173 step 3 — every mint
// writes one line naming the tier, the seats, the plan and the actor, and it is
// written **before the key is returned**, so a key that exists is a key that was
// written down.
//
// The line is written for a mint from the page and for nothing else: an action
// that mints nothing — a refusal, a decline, a replay — writes none, which is
// what makes the log a list of keys rather than a list of clicks.
func TestEveryMintIsAudited(t *testing.T) {
	order := &steps{}
	f := newFixture(t)
	f.auditor.log = order
	f.recording.log = order

	// §5.5's Enterprise key: minted from the page, by the owner, from an Order
	// Form, under the plan the page reaches and the site's forms do not.
	rec := f.adminPost(RouteAdminMint, f.mintForm(url.Values{
		"plan":         {enterprisePlanName},
		"seats":        {"40"},
		"term_days":    {"365"},
		"organisation": {"order-2026-0041"},
		"email":        {"licensing@acme.example"},
	}))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("the Order-Form mint answered %d, want 303: %s", rec.Code, rec.Body.String())
	}

	if len(f.auditor.lines) != 1 {
		t.Fatalf("the mint wrote %d audit line(s), want one", len(f.auditor.lines))
	}
	line := f.auditor.lines[0]
	if line.Actor != ownerAccount {
		t.Errorf("the line's actor is %q, want the account that minted (%q)", line.Actor, ownerAccount)
	}
	if line.Tier != licence.TierEnterprise {
		t.Errorf("the line's tier is %q, want %q", line.Tier, licence.TierEnterprise)
	}
	if line.Seats != 40 {
		t.Errorf("the line's seats are %d, want 40", line.Seats)
	}
	if line.Plan != enterprisePlanName {
		t.Errorf("the line's plan is %q, want %q", line.Plan, enterprisePlanName)
	}
	if line.Organisation != "order-2026-0041" {
		t.Errorf("the line's organisation is %q, want the Order Form's id", line.Organisation)
	}
	if line.At.IsZero() {
		t.Error("the line carries no instant")
	}
	if !strings.Contains(line.String(), licence.TierEnterprise) || !strings.Contains(line.String(), ownerAccount) {
		t.Errorf("the line reads %q and does not name the tier and the actor (§6)", line.String())
	}

	// Before the key was returned: the minter is what returns it, so the audit
	// step is in front of the mint step.
	audit, mint := order.index("audit"), order.index("mint")
	if audit < 0 || mint < 0 {
		t.Fatalf("the order log reads %v, want an audit step and a mint step", order.at)
	}
	if audit > mint {
		t.Errorf("the audit line was written after the key was returned (%v): a key that exists and is not written down is the one thing §6's row is about", order.at)
	}

	// And nothing else writes one: a replay mints nothing, a refusal mints
	// nothing, and a decline mints nothing.
	f.adminPost(RouteAdminMint, url.Values{
		"plan": {enterprisePlanName}, "seats": {"1"}, "term_days": {"30"},
		"organisation": {"order-2026-0042"}, "email": {"licensing@acme.example"},
		"step_up": {stepUpCode},
	})
	if got := len(f.auditor.lines); got != 1 {
		t.Errorf("an action with no page token wrote %d audit line(s) in all, want one", got)
	}
}

// TestAFailedAuditLineFailsTheMint: §6's audit row — the line is written before
// the key is returned and it is the mint's own step rather than a report about
// it, so an audit the deployment cannot write is a mint it does not make. The
// other way round is the failure the row exists to prevent: a key that was
// signed, mailed and recorded with nothing anywhere saying it was.
//
// What a failed mint leaves behind is nothing, so the owner's next click is the
// same mint rather than a replay of one that never happened.
func TestAFailedAuditLineFailsTheMint(t *testing.T) {
	f := newFixture(t)
	body := f.mintForm(url.Values{
		"plan": {enterprisePlanName}, "seats": {"40"}, "term_days": {"365"},
		"organisation": {"order-2026-0041"}, "email": {applicantAddress},
	})

	f.auditor.err = errors.New("the sink is not answering")
	if rec := f.adminPost(RouteAdminMint, body); rec.Code != http.StatusInternalServerError {
		t.Errorf("a mint whose audit line could not be written answered %d, want 500: %s", rec.Code, rec.Body.String())
	}
	if got := len(f.recording.keys); got != 0 {
		t.Errorf("a mint with no audit line produced %d key(s), want none: the key is the thing that would exist with nothing to account for it", got)
	}
	if got := len(f.records()); got != 0 {
		t.Errorf("a mint with no audit line wrote %d record(s), want none", got)
	}
	if got := len(f.mail.sent); got != 0 {
		t.Errorf("a mint with no audit line mailed %d message(s), want none", got)
	}

	// And the line being written is what the mint was waiting for: the same
	// submission is a licence once the sink answers.
	f.auditor.err = nil
	if rec := f.adminPost(RouteAdminMint, body); rec.Code != http.StatusSeeOther {
		t.Fatalf("the same mint once the sink answers answered %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := len(f.auditor.lines); got != 1 {
		t.Errorf("the mint wrote %d audit line(s), want one", got)
	}
	if got := len(f.recording.keys); got != 1 {
		t.Errorf("the mint produced %d key(s), want one: a failed audit is a mint that did not happen, not one to replay", got)
	}
}

// TestThePageWithoutAnAuditSinkWritesTheLineToTheLog: §6's audit row and
// **D456** — where the line goes is the open decision and that there is one is
// not, so a deploy that names no Auditor writes it to the process's own log,
// which is D456(a)'s sink as far as this package can express one. The mint is
// unaffected: a destination is not a permission.
func TestThePageWithoutAnAuditSinkWritesTheLineToTheLog(t *testing.T) {
	f := newFixtureWith(t, Production, func(f *fixture) { f.deps.Auditor = nil })

	var logged strings.Builder
	previous := log.Writer()
	log.SetOutput(&logged)
	defer log.SetOutput(previous)

	if rec := f.adminPost(RouteAdminMint, f.mintForm(url.Values{
		"plan": {enterprisePlanName}, "seats": {"40"}, "term_days": {"365"},
		"organisation": {"order-2026-0041"}, "email": {applicantAddress},
	})); rec.Code != http.StatusSeeOther {
		t.Fatalf("the mint answered %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := len(f.recording.keys); got != 1 {
		t.Errorf("the mint produced %d key(s), want one", got)
	}
	if got := len(f.records()); got != 1 {
		t.Errorf("the mint wrote %d record(s), want one", got)
	}
	if got := len(f.mail.sent); got != 1 {
		t.Errorf("the mint mailed %d message(s), want one: a key nobody is sent is not a key", got)
	}

	line := logged.String()
	for _, named := range []string{"mint ", licence.TierEnterprise, ownerAccount, "order-2026-0041"} {
		if !strings.Contains(line, named) {
			t.Errorf("the deployment's log reads %q and does not name %q: a mint with no Auditor is still a mint that was written down", line, named)
		}
	}
}

// TestAnOrderFormMintCanBeIssuedAgain: §5.5's Order-Form mint is a licence for
// an order nobody here has seen, so what makes one submission one key is the
// render it came from rather than the order form's name. The same order form and
// seat count are a licence the owner may have to issue twice — and a record that
// answered the second as the first mint's own success would leave them with no
// way to issue it and nothing that says why.
//
// What is still one click one key is the click: the browser resubmitting the
// form the owner has in front of them is the same render, and mints nothing.
func TestAnOrderFormMintCanBeIssuedAgain(t *testing.T) {
	f := newFixture(t)
	fields := func() url.Values {
		return url.Values{
			"plan": {enterprisePlanName}, "seats": {"40"}, "term_days": {"365"},
			"organisation": {"order-2026-0041"}, "email": {"licensing@acme.example"},
		}
	}

	form := f.mintForm(fields())
	if rec := f.adminPost(RouteAdminMint, form); rec.Code != http.StatusSeeOther {
		t.Fatalf("the Order-Form mint answered %d, want 303: %s", rec.Code, rec.Body.String())
	}

	// The same form a second time: one render, one click, one key.
	if rec := f.adminPost(RouteAdminMint, form); rec.Code != http.StatusSeeOther {
		t.Errorf("re-submitting the form answered %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := len(f.recording.keys); got != 1 {
		t.Errorf("re-submitting one render minted %d key(s), want one", got)
	}
	if got := len(f.auditor.lines); got != 1 {
		t.Errorf("re-submitting one render wrote %d audit line(s), want one", got)
	}

	// The re-issue: the owner opens the page again, and the same order form and
	// seat count is a key of its own rather than the first mint's silence.
	if page := f.adminGet(); page.Code != http.StatusOK {
		t.Fatalf("the page answered %d, want 200: %s", page.Code, page.Body.String())
	}
	if rec := f.adminPost(RouteAdminMint, f.mintForm(fields())); rec.Code != http.StatusSeeOther {
		t.Fatalf("the second licence for one order form answered %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := len(f.recording.keys); got != 2 {
		t.Errorf("the second licence produced %d key(s) in all, want two", got)
	}
	if got := len(f.auditor.lines); got != 2 {
		t.Fatalf("the second licence wrote %d audit line(s) in all, want two", got)
	}
	if got := len(f.mail.sent); got != 2 {
		t.Errorf("the second licence mailed %d message(s) in all, want two", got)
	}
	first, second := f.auditor.lines[0], f.auditor.lines[1]
	if first.Source == second.Source {
		t.Errorf("both licences are recorded as %q: the record cannot tell a re-issue from the mint it repeats", first.Source)
	}
	if first.Organisation != second.Organisation || first.Seats != second.Seats {
		t.Errorf("the two licences name %q/%d and %q/%d, want one order form and seat count issued twice",
			first.Organisation, first.Seats, second.Organisation, second.Seats)
	}
}

// TestAdminOrderFormMintsOncePerLicence: what makes one submission of the form
// one key is the form the owner has in front of them, and what that form asks
// for is the licence — the plan with the tier in it, the seats, the term and the
// address. A source built from the render and two of those fields let a second
// submission from the same render that asked for a **different** licence take
// the first mint's record: answered 303 as a success, with no key, no mail, no
// audit line and no record, which is a licence the owner believes they granted
// (§5.5).
func TestAdminOrderFormMintsOncePerLicence(t *testing.T) {
	f := newFixture(t)
	fields := func() url.Values {
		return url.Values{
			"plan": {enterprisePlanName}, "seats": {"40"}, "term_days": {"365"},
			"organisation": {"order-2026-0041"}, "email": {"licensing@acme.example"},
		}
	}
	// One render: the token is the page's own, and a test that minted a new one
	// per submission would be submitting from a page the owner does not hold.
	render := f.mintForm(fields())
	if rec := f.adminPost(RouteAdminMint, render); rec.Code != http.StatusSeeOther {
		t.Fatalf("the Order-Form mint answered %d, want 303: %s", rec.Code, rec.Body.String())
	}

	want := 1
	for _, tc := range []struct {
		name string
		edit func(url.Values)
	}{
		{"another plan", func(v url.Values) { v.Set("plan", paidPlanName) }},
		{"another term", func(v url.Values) { v.Set("term_days", "90") }},
		{"another seat count", func(v url.Values) { v.Set("seats", "5") }},
		{"another address", func(v url.Values) { v.Set("email", "second@acme.example") }},
	} {
		form := url.Values{}
		for k, v := range render {
			form[k] = append([]string(nil), v...)
		}
		tc.edit(form)
		want++
		if rec := f.adminPost(RouteAdminMint, form); rec.Code != http.StatusSeeOther {
			t.Fatalf("%s answered %d, want 303: %s", tc.name, rec.Code, rec.Body.String())
		}
		if got := len(f.recording.keys); got != want {
			t.Errorf("%s minted nothing: %d key(s) in all, want %d", tc.name, got, want)
		}
		if got := len(f.auditor.lines); got != want {
			t.Errorf("%s wrote %d audit line(s) in all, want %d", tc.name, got, want)
		}
		if got := len(f.records()); got != want {
			t.Errorf("%s wrote %d record(s) in all, want %d", tc.name, got, want)
		}
	}

	// And the form the owner holds, submitted twice, is one click: the same
	// fields are the same licence.
	if rec := f.adminPost(RouteAdminMint, render); rec.Code != http.StatusSeeOther {
		t.Errorf("re-submitting the form answered %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := len(f.recording.keys); got != want {
		t.Errorf("re-submitting the form minted %d key(s) in all, want %d", got, want)
	}

	seen := map[string]bool{}
	for _, record := range f.records() {
		if seen[record.Source] {
			t.Errorf("two licences are recorded as %q: the record cannot tell them apart", record.Source)
		}
		seen[record.Source] = true
	}
}

// burstGate is how long one mint waits for the burst it is part of. It is the
// window a burst of the page's actions arrives inside, and a mint that arrives
// alone waits it out rather than waiting for clicks that are not coming.
const burstGate = 200 * time.Millisecond

// burstMints is the minter with a burst of clicks at it: every mint that arrives
// inside one window goes through together, and a mint that arrives alone waits
// the window out and then goes through on its own.
//
// The two are the two answers a test reads — the mints the page let through and
// the one it did before the window closed — so nothing about the service is
// faked here: what decides which of them happens is the page's own lock.
//
// reached is closed by the first mint to arrive, so a test can put a second
// action in flight beside a mint without waiting on a clock.
type burstMints struct {
	inner   Mints
	want    int
	gate    time.Duration
	open    chan struct{}
	reached chan struct{}

	once sync.Once
	mu   sync.Mutex
	seen int
}

// newBurstMints is that minter, with want the number of arrivals that opens the
// window early: a mint that arrives alone waits it out.
func newBurstMints(inner Mints, want int) *burstMints {
	return &burstMints{
		inner: inner, want: want, gate: burstGate,
		open: make(chan struct{}), reached: make(chan struct{}),
	}
}

func (b *burstMints) Mint(ctx context.Context, req Request) (Minted, error) {
	b.once.Do(func() { close(b.reached) })
	b.mu.Lock()
	b.seen++
	if b.seen == b.want {
		close(b.open)
	}
	b.mu.Unlock()
	select {
	case <-b.open:
	case <-time.After(b.gate):
	}
	return b.inner.Mint(ctx, req)
}

// together submits one form n times at once and answers the status code each
// submission got, in submission order. They all wait on one channel, so the n
// are in flight together rather than queued behind a loop.
func together(n int, submit func(i int) *httptest.ResponseRecorder) []int {
	start := make(chan struct{})
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			codes[i] = submit(i).Code
		}()
	}
	close(start)
	wg.Wait()
	return codes
}

// answered is how many of the codes are one status.
func answered(codes []int, want int) int {
	n := 0
	for _, code := range codes {
		if code == want {
			n++
		}
	}
	return n
}

// TestConcurrentApprovalsMintOnce: §4.5's "an approval mints once" under the one
// thing that can break it — the same form submitted many times at once. What
// makes an approval idempotent is a read of the record before the mint and a
// write of it after (§5.2), and a burst is the window between the two: eight
// clicks must leave one key, one mail, one audit line and one record, and all
// eight answered the way the mint was. Seven keys mailed and one record is the
// failure this reads (§7.3: a key is never stored, so a key minted twice over
// one source is a licence nothing accounts for).
func TestConcurrentApprovalsMintOnce(t *testing.T) {
	const clicks = 8
	f := newFixture(t)
	f.deps.Mints = newBurstMints(f.recording, clicks)
	f.start()

	answerBody(t, f.post(RouteRequest, url.Values{"email": {applicantAddress}, "request": {"a multi-seat trial"}}))
	open := f.open()
	if len(open) != 1 {
		t.Fatalf("the manual form left %d open request(s), want one", len(open))
	}
	id := open[0].ID
	form := f.mintForm(url.Values{
		"application": {id}, "plan": {paidPlanName}, "seats": {"3"},
		"term_days": {"365"}, "organisation": {"acme.example"},
	})

	codes := together(clicks, func(int) *httptest.ResponseRecorder { return f.adminPost(RouteAdminMint, form) })
	if got := answered(codes, http.StatusSeeOther); got != clicks {
		t.Errorf("%d of %d concurrent submissions were answered 303, want all of them: %v", got, clicks, codes)
	}
	records := f.records()
	if got := len(f.recording.keys); got != 1 {
		t.Errorf("the burst minted %d key(s), want one: %d of them would be keys mailed with nothing to account for them (§5.2, §7.3)", got, got-len(records))
	}
	if got := len(f.mail.sent); got != 1 {
		t.Errorf("the burst mailed %d message(s), want one", got)
	}
	if got := len(f.auditor.lines); got != 1 {
		t.Errorf("the burst wrote %d audit line(s), want one: the log is a list of keys", got)
	}
	if len(records) != 1 {
		t.Fatalf("the burst wrote %d record(s), want one", len(records))
	}
	if got := records[0].Source; got != sourceApproval+id {
		t.Errorf("the record's source is %q, want the request it answers (%q)", got, sourceApproval+id)
	}
	if got := f.open(); len(got) != 0 {
		t.Errorf("the approved request is still in the queue: %+v", got)
	}
}

// TestConcurrentMintsRespectTheCap: §6's limits row under the same burst. D454's
// cap is a count of the record and §5.2 writes the record after the key, so a
// burst of eight Order-Form mints — eight forms the page rendered, eight
// sources, one tier — must mint the one the cap allows and answer the other
// seven as the cap's, rather than counting a record none of them has written
// yet.
func TestConcurrentMintsRespectTheCap(t *testing.T) {
	const clicks = 8
	f := newFixtureWith(t, Production, func(f *fixture) {
		f.cfg.MintCaps[licence.TierEnterprise] = Limits{Requests: 1, Window: time.Hour}
	})
	f.deps.Mints = newBurstMints(f.recording, clicks)
	f.start()

	forms := make([]url.Values, clicks)
	for i := range forms {
		forms[i] = f.mintForm(url.Values{
			"plan": {enterprisePlanName}, "seats": {"40"}, "term_days": {"365"},
			"organisation": {"order-2026-0041"}, "email": {"licensing@acme.example"},
		})
	}
	codes := together(clicks, func(i int) *httptest.ResponseRecorder { return f.adminPost(RouteAdminMint, forms[i]) })

	if got := answered(codes, http.StatusSeeOther); got != 1 {
		t.Errorf("%d of %d concurrent mints were answered 303, want the cap's one: %v", got, clicks, codes)
	}
	if got := answered(codes, http.StatusTooManyRequests); got != clicks-1 {
		t.Errorf("%d of %d concurrent mints were refused by the cap, want the other seven: %v", got, clicks, codes)
	}
	if got := len(f.recording.keys); got != 1 {
		t.Errorf("a burst of %d mints under a cap of one minted %d key(s), want one (D454)", clicks, got)
	}
	if got := len(f.auditor.lines); got != 1 {
		t.Errorf("the burst wrote %d audit line(s), want one: the keys that were not minted wrote none", got)
	}
	if got := len(f.mail.sent); got != 1 {
		t.Errorf("the burst mailed %d message(s), want one", got)
	}
	if got := len(f.records()); got != 1 {
		t.Errorf("the burst wrote %d record(s), want one", got)
	}
}

// TestADeclineAndAnApprovalDoNotCross: §4.5's queue with both of the page's
// answers in flight for one request — the "no" beside the mint that grants it.
// They are exclusive, so one click takes and the other is refused. The decline
// reads the record to refuse a request that already has a key, and §5.2 writes
// that record **after** the key: a decline that ran while the mint was still in
// the minter's hands would read the store before the write it is there to find
// and take a "no" for a request about to be licensed. The mint is held inside
// the minter while the decline is submitted, which is that moment.
func TestADeclineAndAnApprovalDoNotCross(t *testing.T) {
	f := newFixture(t)
	mints := newBurstMints(f.recording, 2)
	f.deps.Mints = mints
	f.start()

	answerBody(t, f.post(RouteRequest, url.Values{"email": {applicantAddress}, "request": {"a multi-seat trial"}}))
	open := f.open()
	if len(open) != 1 {
		t.Fatalf("the manual form left %d open request(s), want one", len(open))
	}
	id := open[0].ID
	approve := f.mintForm(url.Values{
		"application": {id}, "plan": {paidPlanName}, "seats": {"3"},
		"term_days": {"365"}, "organisation": {"acme.example"},
	})
	decline := f.decideForm(id)

	codes := make([]int, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		codes[0] = f.adminPost(RouteAdminMint, approve).Code
	}()
	go func() {
		defer wg.Done()
		<-mints.reached
		codes[1] = f.adminPost(RouteAdminDecide, decline).Code
	}()
	wg.Wait()

	if got := answered(codes, http.StatusSeeOther); got != 1 {
		t.Errorf("%d of the two answers were the page's own, want one: a request is declined or approved and not both (%v)", got, codes)
	}
	if got := answered(codes, http.StatusConflict); got != 1 {
		t.Errorf("%d of the two answers refused the other's ground, want one (%v)", got, codes)
	}
	held := f.queued()
	if len(f.records()) == 1 && len(held) == 1 && held[0].Decision == Declined {
		t.Errorf("the request reads Declined with a key issued for it: %+v", held[0])
	}
}

// parkedApplications is the store with the first read of the queue held open
// inside it, holding what it read: how a test leaves an action standing on a
// snapshot without waiting on a clock. The list is a copy (§Memory returns one)
// and the caller goes on with that copy, so a second action that answers a
// request inside the window is exactly the one the parked caller does not see.
type parkedApplications struct {
	*Memory
	gate    time.Duration
	reached chan struct{}

	mu   sync.Mutex
	used bool
}

func (p *parkedApplications) Applications(ctx context.Context) ([]Application, error) {
	held, err := p.Memory.Applications(ctx)
	p.mu.Lock()
	first := !p.used
	p.used = true
	p.mu.Unlock()
	if first {
		close(p.reached)
		time.Sleep(p.gate)
	}
	return held, err
}

// TestAnApprovalDoesNotMintOnAStaleRequest: an approval reads the request the
// form names to know which branch it is on and whether the owner has already
// said no, and §4.5's queue is drawn from that same flag. A decline landing
// between that read and the mint would otherwise be answered twice: the key is
// issued for a request the owner has just declined, and the decline the page
// recorded is written over with Approved, so a request the owner answered "no"
// ends up licensed and the queue says "yes".
//
// The read is parked with the answer it took, which is what makes it stale: the
// decline lands inside the window and what the approval goes on with is the
// list it was given before it did.
func TestAnApprovalDoesNotMintOnAStaleRequest(t *testing.T) {
	var parked *parkedApplications
	f := newFixtureWith(t, Production, func(f *fixture) {
		parked = &parkedApplications{Memory: f.store, gate: burstGate, reached: make(chan struct{})}
		f.deps.Store = parked
	})
	answerBody(t, f.post(RouteRequest, url.Values{"email": {applicantAddress}, "request": {"a multi-seat trial"}}))
	open := f.open()
	if len(open) != 1 {
		t.Fatalf("the manual form left %d open request(s), want one", len(open))
	}
	id := open[0].ID
	approve := f.mintForm(url.Values{
		"application": {id}, "plan": {paidPlanName}, "seats": {"3"},
		"term_days": {"365"}, "organisation": {"acme.example"},
	})
	decline := f.decideForm(id)

	codes := make([]int, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		codes[0] = f.adminPost(RouteAdminMint, approve).Code
	}()
	go func() {
		defer wg.Done()
		<-parked.reached
		codes[1] = f.adminPost(RouteAdminDecide, decline).Code
	}()
	wg.Wait()

	if got := answered(codes, http.StatusSeeOther); got != 1 {
		t.Errorf("%d of the two answers were the page's own, want one: a request is declined or approved and not both (%v)", got, codes)
	}
	if got := answered(codes, http.StatusConflict); got != 1 {
		t.Errorf("%d of the two answers refused the other's ground, want one (%v)", got, codes)
	}
	// And what the page leaves behind is one of those answers: the key with the
	// request approved, or no key with it declined. A request the page answers
	// with anything else is one the owner declined and the record licensed.
	if held := f.queued(); len(f.records()) > 0 && len(held) == 1 && held[0].Decision != Approved {
		t.Errorf("a key was issued for a request the page answers %q: %+v", held[0].Decision, held[0])
	}
}

// failingApplications is the store as a deployment that cannot answer a
// request: the write §5.2 puts **after** the mint, which is the one failure that
// leaves a key issued for a request the queue still shows as open.
type failingApplications struct {
	*Memory
	refuse int
}

func (f *failingApplications) SaveApplication(ctx context.Context, a Application) error {
	if f.refuse > 0 {
		f.refuse--
		return errors.New("the store refused the write")
	}
	return f.Memory.SaveApplication(ctx, a)
}

// TestAFailedDecisionIsWrittenByTheNextClick: the write that answers a request
// comes after the key is out (§5.2's order), so a store that cannot take it
// leaves a request that is open in the queue and already has a key. Both of the
// page's actions have to read that the same way.
//
// A click that only redirected would leave the request in the queue for good —
// the queue is where the owner works and the approval that follows is a replay
// — and a decline would take a "no" for a request the owner has licensed. What
// says a request has been granted is the record, and the flag is only what the
// queue is drawn from.
func TestAFailedDecisionIsWrittenByTheNextClick(t *testing.T) {
	f := newFixtureWith(t, Production, func(f *fixture) {
		f.deps.Store = &failingApplications{Memory: f.store}
	})
	store := f.deps.Store.(*failingApplications)

	answerBody(t, f.post(RouteRequest, url.Values{"email": {applicantAddress}, "request": {"a multi-seat trial"}}))
	open := f.open()
	if len(open) != 1 {
		t.Fatalf("the manual form left %d open request(s), want one", len(open))
	}
	id := open[0].ID
	// A fresh token each time, so neither click is about the page's own state.
	form := func() url.Values {
		return f.mintForm(url.Values{
			"application": {id}, "plan": {paidPlanName}, "seats": {"3"},
			"term_days": {"365"}, "organisation": {"acme.example"},
		})
	}

	// The click that mints: the key is out and the record is written, and the
	// answer is the write that fails.
	store.refuse = 1
	if rec := f.adminPost(RouteAdminMint, form()); rec.Code != http.StatusSeeOther {
		t.Fatalf("the approval answered %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := len(f.recording.keys); got != 1 {
		t.Fatalf("the approval minted %d key(s), want one", got)
	}
	if got := f.open(); len(got) != 1 || got[0].ID != id {
		t.Fatalf("the request whose answer could not be written is not in the queue: %+v", got)
	}

	// A decline is refused, and the refusal is what leaves the request where it
	// is: the owner's next click is the approval, not a "no" for a live key.
	if rec := f.adminPost(RouteAdminDecide, f.decideForm(id)); rec.Code != http.StatusConflict {
		t.Errorf("declining a request that already has a key answered %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if got := f.open(); len(got) != 1 || got[0].Decision != Undecided {
		t.Fatalf("the refused decline changed the request: %+v", got)
	}

	// The next approval finishes what the first one started: the answer is
	// written, and nothing is minted, mailed or recorded a second time.
	before := [3]int{len(f.recording.keys), len(f.auditor.lines), len(f.mail.sent)}
	if rec := f.adminPost(RouteAdminMint, form()); rec.Code != http.StatusSeeOther {
		t.Fatalf("the second approval answered %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := f.open(); len(got) != 0 {
		t.Errorf("the request is still open after the second approval: %+v", got)
	}
	if got := f.queued(); len(got) != 1 || got[0].Decision != Approved {
		t.Errorf("the request reads %+v, want an approved one: the queue is the requests nobody has answered", got)
	}
	for _, counted := range []struct {
		what string
		got  int
		want int
	}{
		{what: "key", got: len(f.recording.keys), want: before[0]},
		{what: "audit line", got: len(f.auditor.lines), want: before[1]},
		{what: "message", got: len(f.mail.sent), want: before[2]},
	} {
		if counted.got != counted.want {
			t.Errorf("the second approval added %d %s(s), want none: the key was already out and written down", counted.got-counted.want, counted.what)
		}
	}
}

// TestPerTierMintLimit: §6's limits row and Check 173 step 5 — D454's per-tier
// cap, read over the issuance record. What a cap does to the excess is the
// recommendation the design carries: it **queues** rather than mints, so an
// honest burst is answered rather than turned away, and the request that was
// over the cap is still there when the window moves.
//
// What the cap counts is the page's own mints and not every key of the tier: a
// merchant's event mints a team key inside the same window here, and the owner's
// first approval has to be answered anyway. A cap that read the whole record
// would refuse a paying customer's trial because somebody bought something.
func TestPerTierMintLimit(t *testing.T) {
	f := newFixtureWith(t, Production, func(f *fixture) {
		f.cfg.MintCaps = map[string]Limits{
			licence.TierCommunity:  {Requests: 20, Window: time.Hour},
			licence.TierTeam:       {Requests: 1, Window: time.Hour},
			licence.TierEnterprise: {Requests: 20, Window: time.Hour},
		}
	})

	// The customer's key first, inside the window the cap counts over. What the
	// cap counts is read as the keys minted since, because the fixture's minter
	// records this one too.
	if bought := f.purchase("evt-cap-0", "buyer.example", 3, 365); bought.Tier != licence.TierTeam {
		t.Fatalf("the purchase minted a %q key, and this is a test about the %q tier's cap", bought.Tier, licence.TierTeam)
	}
	before := len(f.recording.keys)

	answerBody(t, f.post(RouteRequest, url.Values{"email": {applicantAddress}, "request": {"a trial"}}))
	answerBody(t, f.post(RouteRequest, url.Values{"email": {"second@acme.example"}, "request": {"and another"}}))
	open := f.open()
	if len(open) != 2 {
		t.Fatalf("the manual form left %d open request(s), want two", len(open))
	}
	form := func(id string) url.Values {
		return f.mintForm(url.Values{
			"application": {id}, "plan": {paidPlanName}, "seats": {"3"},
			"term_days": {"365"}, "organisation": {"acme.example"},
		})
	}

	// The first approval is answered although the tier's cap is one and the
	// customer's key is already in the window: the cap counts the page's own
	// mints, and the customer's is not one of them.
	if rec := f.adminPost(RouteAdminMint, form(open[0].ID)); rec.Code != http.StatusSeeOther {
		t.Fatalf("the first approval answered %d, want 303: %s, and the customer's key in the window is not a page mint", rec.Code, rec.Body.String())
	}
	if got := len(f.recording.keys) - before; got != 1 {
		t.Fatalf("the first approval minted %d key(s), want one", got)
	}

	// The burst: the tier's one mint for the window is spent, so the second
	// approval mints nothing — and its request stays where it was, undecided and
	// on the page, which is "queued rather than minted".
	if rec := f.adminPost(RouteAdminMint, form(open[1].ID)); rec.Code != http.StatusTooManyRequests {
		t.Errorf("the second mint in one window answered %d, want 429: %s", rec.Code, rec.Body.String())
	}
	if got := len(f.recording.keys) - before; got != 1 {
		t.Errorf("the burst minted %d key(s) in all, want the first one alone", got)
	}
	if got := len(f.auditor.lines); got != 1 {
		t.Errorf("a mint that a cap refused wrote %d audit line(s) in all, want one: no key was returned", got)
	}
	if got := len(f.open()); got != 1 || f.open()[0].ID != open[1].ID {
		t.Errorf("the request the cap refused is not still in the queue: %+v", f.open())
	}

	// The window moves, and the same approval is answered.
	f.advance(2 * time.Hour)
	if rec := f.adminPost(RouteAdminMint, form(open[1].ID)); rec.Code != http.StatusSeeOther {
		t.Errorf("the same approval one window later answered %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if got := len(f.recording.keys) - before; got != 2 {
		t.Errorf("the approval after the window minted %d key(s) in all, want two", got)
	}

	// And the merchant's path is not the page's, either way round: a purchase
	// mints under a page cap the window has spent, because §5.1's events and
	// §5.5's page are two doors and the cap is one of them (§5.1, §5.5).
	if bought := f.purchase("evt-cap-1", "buyer.example", 3, 365); bought.Plan != paidPlanName {
		t.Errorf("a purchase under a spent page cap minted %q, want the merchant's plan", bought.Plan)
	}
}

// TestThePageNeverRendersAKey: §7.3 and §5.5 — the page mints, and a key is
// mailed to the address the mint names and never stored or rendered. The page
// reads the record, so what it can show is a hint of the key id and never the
// key, and the whole of the key exists in exactly one place: the message that
// carried it.
func TestThePageNeverRendersAKey(t *testing.T) {
	f := newFixture(t)
	if rec := f.adminPost(RouteAdminMint, f.mintForm(url.Values{
		"plan": {enterprisePlanName}, "seats": {"2"}, "term_days": {"365"},
		"organisation": {"order-2026-0043"}, "email": {applicantAddress},
	})); rec.Code != http.StatusSeeOther {
		t.Fatalf("the mint answered %d, want 303: %s", rec.Code, rec.Body.String())
	}
	keys := f.mintedKeys()
	if len(keys) != 1 {
		t.Fatalf("the mint produced %d key(s), want one", len(keys))
	}
	body := f.adminGet().Body.String()
	if strings.Contains(body, keys[0]) {
		t.Error("the page renders a key text: a page is a place a key leaks from, and §7.3 keeps it out of the store for the same reason")
	}
	if got := f.records(); len(got) != 1 || strings.Contains(got[0].Email, "\n") {
		t.Errorf("the record is %+v, want one for the address the mint named", got)
	}
}
