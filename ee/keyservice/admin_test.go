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
