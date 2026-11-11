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
	"net/http"
	"net/url"
	"strings"
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

	// A token signed with a secret that is not this deploy's. It is built by a
	// service that has the deploy's own shape and somebody else's secret, which
	// is exactly what a forged one would be.
	forged := &Service{cfg: Config{AdminSecret: []byte("another-secret-0123456789")}, now: f.clock}
	foreign, err := forged.pageToken(ownerAccount, actionMint)
	if err != nil {
		t.Fatalf("building the forged token: %v", err)
	}

	// A token the page rendered one window ago: the page's own, kept from before
	// the clock moved past the period it lives.
	stale := f.pageToken(ownerAccount, actionMint)
	f.advance(16 * time.Minute)

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

// TestPerTierMintLimit: §6's limits row and Check 173 step 5 — D454's per-tier
// cap, read over the issuance record. What a cap does to the excess is the
// recommendation the design carries: it **queues** rather than mints, so an
// honest burst is answered rather than turned away, and the request that was
// over the cap is still there when the window moves.
func TestPerTierMintLimit(t *testing.T) {
	f := newFixtureWith(t, Production, func(f *fixture) {
		f.cfg.MintCaps = map[string]Limits{
			licence.TierCommunity:  {Requests: 20, Window: time.Hour},
			licence.TierTeam:       {Requests: 1, Window: time.Hour},
			licence.TierEnterprise: {Requests: 20, Window: time.Hour},
		}
	})
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

	if rec := f.adminPost(RouteAdminMint, form(open[0].ID)); rec.Code != http.StatusSeeOther {
		t.Fatalf("the first mint answered %d, want 303: %s", rec.Code, rec.Body.String())
	}

	// The burst: the tier's one mint for the window is spent, so the second
	// approval mints nothing — and its request stays where it was, undecided and
	// on the page, which is "queued rather than minted".
	if rec := f.adminPost(RouteAdminMint, form(open[1].ID)); rec.Code != http.StatusTooManyRequests {
		t.Errorf("the second mint in one window answered %d, want 429: %s", rec.Code, rec.Body.String())
	}
	if got := len(f.recording.keys); got != 1 {
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
	if got := len(f.recording.keys); got != 2 {
		t.Errorf("the approval after the window minted %d key(s) in all, want two", got)
	}

	// A cap is a limit on the page's own mints and not on the service's: the
	// merchant's events and the followed links mint through their own paths, and
	// a cap that counted those would refuse a paying customer because the owner
	// had approved a trial (§5.1, §5.5).
	if rec := f.purchase("evt-cap-1", "buyer.example", 3, 365); rec.Plan != paidPlanName {
		t.Errorf("a purchase under a spent page cap minted %q, want the merchant's plan", rec.Plan)
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
