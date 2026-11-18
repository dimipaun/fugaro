// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// P3.3's open-source cases (D2185, F7, D2199): the portal's ask for a licence
// under the programme, and the queue entry it leaves for the operations team to
// decide (P5.1).
//
// What the route is asked about is not a key: the programme's checks are §4.4's
// and they are the website form's own function (`openSourceControl`), and the
// answer to a request that meets them is that a person will read it. So what
// these cases hold is the record the owner's queue ends up with — the account
// the sign-in proved, the canonical URLs the deploy's own host answered for, the
// organisation the proof showed control of — and that nothing else happened: no
// key, no mail to the applicant, and nothing at all in the queue for a request
// the checks refused.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

// openSourceAsk is one ask's body: the repositories the applicant named, as the
// JSON the SPA posts them in.
func openSourceAsk(t *testing.T, repositories ...string) string {
	t.Helper()
	body, err := json.Marshal(OpenSourceCall{Repositories: repositories})
	if err != nil {
		t.Fatalf("building an open-source ask: %v", err)
	}
	return string(body)
}

// askOpenSource posts one open-source ask as the account the fixture's sign-in
// answers.
func (f *fixture) askOpenSource(body string) *httptest.ResponseRecorder {
	f.t.Helper()
	return f.askOpenSourceFrom(applicantIP, body)
}

// askOpenSourceFrom is the same ask from an address a case names, which is what
// tells the per-address bucket from the per-request one.
func (f *fixture) askOpenSourceFrom(ip, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodPost, intakeOrigin+RouteAPIOpenSource, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+portalIDToken)
	req.RemoteAddr = ip
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

// TestOpenSourceRequestQueuesForOperations: an open-source request that meets
// §4.4's checks is not a key — the licence itself is a person's to grant (§17,
// F47) — so what the ask does is leave the operations team a request it can
// decide (D2185, D2199, P5.1). What the owner reads is here: the account the
// sign-in proved — never one the body names — the plan being asked for, the
// organisation the proof showed control of, and the **canonical** URL of every
// repository on the deploy's own host, which is what a later check re-reads and
// not the line the applicant typed.
func TestOpenSourceRequestQueuesForOperations(t *testing.T) {
	f := newFixture(t)
	f.actsAs(danaUID, danaEmail, signInGoogle)
	// One repository is named twice, once with the `.git` suffix and once with a
	// trailing slash: both are how the same repository is spelled, and what is
	// stored is the host's own canonical form of each and never the line.
	rec := f.askOpenSource(openSourceAsk(t,
		repositoryURL, repositoryTwoURL+".git", repositoryURL+"/"))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("the ask answered %d (%s), want 204: the request is a person's to decide and nothing about it is finished", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Errorf("the ask answered with %q, want no body: the applicant holds nothing — no key and no id a screen shows", rec.Body.String())
	}

	open := f.open()
	if len(open) != 1 {
		t.Fatalf("the queue holds %d open request(s), want the one the ask left: %+v", len(open), open)
	}
	held := open[0]
	if held.ID == "" {
		t.Error("the request has no id, so the owner's page cannot open it (§4.5)")
	}
	if held.Email != danaEmail {
		t.Errorf("the request is filed against %q, want the address the sign-in proved (%q)", held.Email, danaEmail)
	}
	if held.Domain != domainOf(danaEmail) {
		t.Errorf("the request carries the domain %q, want the address's own (%q)", held.Domain, domainOf(danaEmail))
	}
	if held.Plan != openPlanName {
		t.Errorf("the request names the plan %q, want the programme's own (%q): the owner reads it by what is being asked for", held.Plan, openPlanName)
	}
	if held.Organisation != openSourceOrg {
		t.Errorf("the request names the organisation %q, want what the proof showed control of (%q) and not a portal organisation (P3.3)", held.Organisation, openSourceOrg)
	}
	if want := []string{repositoryURL, repositoryTwoURL}; !slices.Equal(held.Repositories, want) {
		t.Errorf("the request names %v, want the canonical URLs %v: the store keeps what the host answered for and never the line the applicant typed (§4.4, §7.3)", held.Repositories, want)
	}
	if held.Decision != Undecided {
		t.Errorf("the request reads %q, want it waiting for a person (%q)", held.Decision, Undecided)
	}
	if !held.At.Equal(f.now) {
		t.Errorf("the request is dated %s, want the ask's own instant %s", held.At, f.now)
	}
	if held.Detail != "" {
		t.Errorf("the request carries the detail %q, and this route has no field for an applicant's text", held.Detail)
	}
	// Both repositories were asked about and the proof was asked for once: the
	// checks ran, and they ran on the two repositories the applicant named.
	if want := []string{"acme/widget", "acme/gadget"}; !slices.Equal(f.host.asked, want) {
		t.Errorf("the code host was asked about %v, want %v", f.host.asked, want)
	}
	if f.proofs.asked != 1 {
		t.Errorf("the proof of control was asked for %d time(s), want once", f.proofs.asked)
	}

	// Nothing was minted and nothing was mailed to the applicant: an open-source
	// key is what a person grants from the queue, and the applicant's answer is
	// the queue entry itself.
	if keys := f.mintedKeys(); len(keys) != 0 {
		t.Errorf("the ask minted %d key(s), want none: the licence is a person's to grant (§17)", len(keys))
	}
	if len(f.mail.sent) != 0 {
		t.Errorf("the ask mailed the applicant %d message(s), want none", len(f.mail.sent))
	}
	alerts := f.notifier.alerts
	if len(alerts) != 1 {
		t.Fatalf("the ask notified the owner %d time(s), want once: a queue nobody is told about is a queue nobody reads (§4.5)", len(alerts))
	}
	if alerts[0].Subject != queueSubject {
		t.Errorf("the notification's subject is %q, want the service's own (%q)", alerts[0].Subject, queueSubject)
	}
	// The link is the portal's own page for the queue (P5.1), which is where the
	// owner decides it since P9.3 retired this service's admin page.
	for _, wanted := range []string{danaEmail, openPlanName, openSourceOrg, repositoryURL, routeOps} {
		if !strings.Contains(alerts[0].Body, wanted) {
			t.Errorf("the notification does not carry %q, and the owner decides the request off it: %q", wanted, alerts[0].Body)
		}
	}
}

// TestOpenSourceRequestIsRefusedForALineTheHostDoesNotServe: §4.4's safety is
// that what an applicant typed is not an address this service dials. Every line
// is read against the deploy's own code host before any of them is asked about,
// so a line naming another host, a plain http address, an internal address or a
// path that is not `owner/name` refuses the whole request — and the code host is
// never called with it.
func TestOpenSourceRequestIsRefusedForALineTheHostDoesNotServe(t *testing.T) {
	for _, tc := range []struct{ name, line string }{
		{"another host", "https://elsewhere.example/acme/widget"},
		{"a plain http address", "http://code.example/acme/widget"},
		{"an address on the platform's own network", "https://169.254.169.254/latest/meta-data"},
		{"a path deeper than a repository", "https://code.example/acme/widget/tree/main"},
		{"a segment that climbs out of the path", "https://code.example/acme/../widget"},
		{"a line that is not a URL at all", "acme/widget"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.actsAs(danaUID, danaEmail, signInGoogle)
			rec := f.askOpenSource(openSourceAsk(t, repositoryURL, tc.line))
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("the ask answered %d (%s), want 422: one line the deploy's host does not serve refuses the request", rec.Code, rec.Body.String())
			}
			if code := refusal(f, rec); code != "unserved_repository" {
				t.Errorf("the refusal carries the code %q, want %q", code, "unserved_repository")
			}
			// The first line was a repository of the host's and the line under
			// test is not: nothing was asked about, because every line is read
			// before any of them is (§4.4).
			if len(f.host.asked) != 0 {
				t.Errorf("the code host was asked about %v, want no call: what an applicant typed is not an address this service dials", f.host.asked)
			}
			if open := f.open(); len(open) != 0 {
				t.Errorf("the refused ask left %+v in the queue, want nothing", open)
			}
			if f.proofs.asked != 0 {
				t.Errorf("the proof of control was asked for %d time(s), want none: the request was refused before the checks", f.proofs.asked)
			}
		})
	}
}

// TestOpenSourceRequestIsRefusedWhereTheChecksSayNo: F47 — a public repository
// proves nothing about who is asking — so the programme's two conditions are
// gates and not hints. A private repository, a licence the OSI does not approve,
// a repository the host has no answer for and a request with no proof of control
// are all refused, and every one of them leaves the queue empty: what a person
// decides is a request that met the conditions, not one they have to re-check.
func TestOpenSourceRequestIsRefusedWhereTheChecksSayNo(t *testing.T) {
	for _, tc := range []struct {
		name   string
		url    string
		proved bool
	}{
		{name: "a private repository", url: privateURL, proved: true},
		{name: "a repository under a licence the OSI does not approve", url: unlicensedURL, proved: true},
		{name: "a repository under a permissive licence the OSI does not approve", url: unapprovedURL, proved: true},
		{name: "a repository the host has no answer for", url: "https://code.example/acme/unknown", proved: true},
		{name: "a request with no proof of control", url: repositoryURL, proved: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.proofs.proved = tc.proved
			f.actsAs(danaUID, danaEmail, signInGoogle)
			rec := f.askOpenSource(openSourceAsk(t, tc.url))
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("the ask answered %d (%s), want 422: the request does not meet the programme's conditions", rec.Code, rec.Body.String())
			}
			if code := refusal(f, rec); code != "not_eligible" {
				t.Errorf("the refusal carries the code %q, want %q", code, "not_eligible")
			}
			if open := f.open(); len(open) != 0 {
				t.Errorf("the refused ask left %+v in the queue, want nothing: a request a person has to re-check is not what the queue is for", open)
			}
			if keys := f.mintedKeys(); len(keys) != 0 {
				t.Errorf("the refused ask minted %d key(s), want none", len(keys))
			}
			if len(f.notifier.alerts) != 0 {
				t.Errorf("the refused ask notified the owner, and there is nothing for them to decide: %+v", f.notifier.alerts)
			}
		})
	}
}

// TestOpenSourceIsRefusedWhereTheDeploymentProvesNothing: a deployment that
// names no proof of control refuses every request here, which is what
// `openSourceControl` does with no Proofs and what F47 asks for — visibility and
// licence are not identity — rather than admitting a request on the strength of
// a public repository. What the applicant is told is the checks' own refusal and
// not a failure of the service's making: what a maintainer can do about a
// deployment's gap is nothing, and §7.2 keeps the service's inside out of an
// answer.
func TestOpenSourceIsRefusedWhereTheDeploymentProvesNothing(t *testing.T) {
	f := newFixtureWith(t, Production, func(f *fixture) {
		f.deps.Proofs = nil
	})
	f.actsAs(danaUID, danaEmail, signInGoogle)
	rec := f.askOpenSource(openSourceAsk(t, repositoryURL))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("the ask answered %d (%s), want 422: a deployment with no proof of control refuses the programme's requests (F47)", rec.Code, rec.Body.String())
	}
	if code := refusal(f, rec); code != "not_eligible" {
		t.Errorf("the refusal carries the code %q, want %q", code, "not_eligible")
	}
	if open := f.open(); len(open) != 0 {
		t.Errorf("the refused ask left %+v in the queue, want nothing", open)
	}
}

// TestOpenSourceRequestIsRefusedForABodyItCannotRead: the body is the API's own
// JSON and it is read strictly, so a body naming an address — which is the
// account the request would be filed against if the shape read one — a body with
// a field this route does not know, a body naming no repository and a body
// naming more than one request reads are all refused before anything is asked
// about or queued.
func TestOpenSourceRequestIsRefusedForABodyItCannotRead(t *testing.T) {
	many := make([]string, 0, 9)
	for i := 0; i < 9; i++ {
		many = append(many, fmt.Sprintf("https://code.example/acme/repo-%d", i))
	}
	f := newFixture(t)
	f.actsAs(danaUID, danaEmail, signInGoogle)
	bodies := map[string]string{
		"an address the body names":        `{"repositories":["` + repositoryURL + `"],"email":"someone@elsewhere.example"}`,
		"an organisation the body names":   `{"repositories":["` + repositoryURL + `"],"organisation":"org-somebody"}`,
		"a field the route does not know":  `{"repositories":["` + repositoryURL + `"],"note":"please"}`,
		"no repositories at all":           `{"repositories":[]}`,
		"entries that are all blank":       `{"repositories":["","  "]}`,
		"more repositories than one reads": openSourceAsk(t, many...),
		"not an object at all":             `["` + repositoryURL + `"]`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			rec := f.askOpenSource(body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("the ask answered %d (%s), want 400", rec.Code, rec.Body.String())
			}
			if code := refusal(f, rec); code != "unreadable_body" {
				t.Errorf("the refusal carries the code %q, want %q", code, "unreadable_body")
			}
		})
	}
	if open := f.open(); len(open) != 0 {
		t.Errorf("a body the route cannot read left %+v in the queue, want nothing", open)
	}
	if len(f.host.asked) != 0 {
		t.Errorf("a body the route cannot read asked the code host about %v, want no call", f.host.asked)
	}
}

// TestOpenSourceRequestIsRefusedWithoutASignIn: the account is what a request is
// filed against and what the owner answers, so a request without a sign-in, one
// whose token the verifier refuses and one whose sign-in proved no address are
// all answered 401 and none of them leaves anything in the queue. The third is
// the case the route's own guard is for: a sign-in with no address is an account
// no request can be filed against, and without the guard the entry would name an
// empty address the owner cannot answer.
func TestOpenSourceRequestIsRefusedWithoutASignIn(t *testing.T) {
	f := newFixture(t)
	body := openSourceAsk(t, repositoryURL)
	for _, tc := range []struct {
		name string
		// auth is the Authorization header as the case writes it, empty for a
		// request that carries none.
		auth string
		// err and caller are what the verifier answers if it is reached.
		err    error
		caller Caller
	}{
		{name: "no sign-in at all"},
		{
			name: "a sign-in the verifier does not admit", auth: portalBearerScheme + " " + portalIDToken,
			err: ErrUnverifiedSignIn,
		},
		{
			name: "a sign-in that named no address", auth: portalBearerScheme + " " + portalIDToken,
			caller: Caller{UID: danaUID, Provider: signInGoogle},
		},
		{
			name: "a sign-in whose address is not one", auth: portalBearerScheme + " " + portalIDToken,
			caller: Caller{UID: danaUID, Email: "not an address", Provider: signInGoogle},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.signIn.tokens = nil
			f.signIn.err = tc.err
			f.signIn.caller = tc.caller
			req := httptest.NewRequest(http.MethodPost, intakeOrigin+RouteAPIOpenSource, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			req.RemoteAddr = applicantIP
			rec := httptest.NewRecorder()
			f.handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("the ask answered %d (%s), want 401: the account is what a request is filed against, so a request without one is nobody's to file", rec.Code, rec.Body.String())
			}
			if code := refusal(f, rec); code != "unauthenticated" {
				t.Errorf("the refusal carries the code %q, want %q", code, "unauthenticated")
			}
			if open := f.open(); len(open) != 0 {
				t.Errorf("an ask with no verified account left %+v in the queue, want nothing", open)
			}
			if len(f.host.asked) != 0 {
				t.Errorf("an ask with no verified account asked the code host about %v, want no call", f.host.asked)
			}
		})
	}
}

// TestOpenSourceIsMountedOnlyWithItsSeams: the route reads the sign-in to learn
// the account it is filed against and the document store to write the queue
// entry, and it declares both where it is registered. It is the declaration and
// not the handler that keeps the service honest: a deployment mounting this door
// without either is refused when it starts (api.go's `reaching`, `missing`),
// rather than answering every ask with a failure of its own.
func TestOpenSourceIsMountedOnlyWithItsSeams(t *testing.T) {
	route, known := apiRoutes.lookup(http.MethodPost, RouteAPIOpenSource)
	if !known {
		t.Fatalf("no route is mounted at %s %s, so nothing serves an open-source ask", http.MethodPost, RouteAPIOpenSource)
	}
	for _, seam := range []struct {
		need apiNeed
		why  string
	}{
		{needSignIn, "the account a request is filed against is the one the sign-in proved (§4.1)"},
		{needDocs, "the queue entry is the store's record (§4.5)"},
	} {
		if !slices.Contains(route.needs, seam.need) {
			t.Errorf("the route %s %s does not declare %s it reads, and %s: a deployment holding none mounts it and answers every ask with a failure of its own",
				route.method, route.path, seam.need, seam.why)
		}
	}
	only := &apiRegistry{routes: []apiRoute{route}, byPath: map[string][]int{}}
	if err := only.missing(Deps{}); err == nil {
		t.Error("a deployment holding neither seam the open-source ask reads is not refused the route, want the start refused (P1.0)")
	}
}

// TestTheAsksAreThePathAndFieldsTheSPASends pins both halves of one contract:
// the paths these two routes are mounted at and the field names their bodies
// carry. The SPA posts to them from the workspace card
// (ee/portal/src/screens/workspaces/workspaces.tsx), and its own suite pins its
// half — the path it joins `API_BASE` to and the body it sends — so what keeps
// the two one contract is that each side names the same facts here.
//
// The mistake this is written against is a real one: the card posted the
// portal's own workspace id in the trial's field, to a path
// (`/api/opensource`) nothing is mounted at, and the two halves of that
// contract were pinned nowhere.
func TestTheAsksAreThePathAndFieldsTheSPASends(t *testing.T) {
	if RouteTrial != "/api/trial" {
		t.Errorf("RouteTrial is %q, want the path the SPA's `/trial` is joined to", RouteTrial)
	}
	if RouteAPIOpenSource != "/api/open-source" {
		t.Errorf("RouteAPIOpenSource is %q, want the path the SPA's `/open-source` is joined to", RouteAPIOpenSource)
	}
	for _, door := range []struct {
		ask  string
		path string
	}{
		{"the trial", RouteTrial},
		{"the open-source ask", RouteAPIOpenSource},
	} {
		if _, known := apiRoutes.lookup(http.MethodPost, door.path); !known {
			t.Errorf("nothing is mounted at POST %s, so %s is a request the API answers 404 no_route", door.path, door.ask)
		}
	}

	// The bodies, read strictly as the routes read them: the trial names the
	// **Firebase project id** in `workspace_id`, and the open-source ask names
	// the repositories in `repositories`.
	var trial TrialCall
	if err := decodeWire([]byte(`{"workspace_id":"example-portal"}`), &trial); err != nil {
		t.Fatalf("the body the SPA posts to the trial is not one this route reads: %v", err)
	}
	if trial.WorkspaceID != "example-portal" {
		t.Errorf("the trial's body reads %q, want the Firebase project id it carries", trial.WorkspaceID)
	}
	var ask OpenSourceCall
	if err := decodeWire([]byte(`{"repositories":["https://code.example/acme/widget"]}`), &ask); err != nil {
		t.Fatalf("the body the SPA posts to the open-source ask is not one this route reads: %v", err)
	}
	if len(ask.Repositories) != 1 || ask.Repositories[0] != "https://code.example/acme/widget" {
		t.Errorf("the open-source ask's body reads %v, want the lines the applicant sent", ask.Repositories)
	}

	// What the two fields are is what the card got wrong: the portal's own
	// workspace id is not a Firebase project id, and the trial's field is the
	// project's (K2.7's rule is counted on it, D2201). A portal id — the
	// 43-character one `newID` mints, as base62 — fails the shape binding and
	// the trial both check, which is what the route answers 422 `invalid_project_id`
	// about.
	id, err := newID()
	if err != nil {
		t.Fatalf("minting a portal id: %v", err)
	}
	if validProjectID(id) {
		t.Errorf("the portal's own workspace id %q is a valid Firebase project id, and the two are different names for different things: the trial's field carries the project's", id)
	}
}

// TestOpenSourceRequestIsThrottledPerAccount: what is scarce behind this route
// is a person's attention on the queue (§4.5, P5.1), so the second allowance is
// the account's — the address the sign-in proved — and not the route's or the
// address the request came from. The fixture's rate is one ask per window, so
// the same account asking again from another address meets the bucket, and
// another account asking from another address is served.
func TestOpenSourceRequestIsThrottledPerAccount(t *testing.T) {
	f := newFixtureWith(t, Production, func(f *fixture) {
		f.cfg.Throttle = Limits{Requests: 1, Window: time.Hour}
	})
	f.actsAs(danaUID, danaEmail, signInGoogle)
	if rec := f.askOpenSourceFrom("198.51.100.60:4040", openSourceAsk(t, repositoryURL)); rec.Code != http.StatusNoContent {
		t.Fatalf("the first ask answered %d (%s), want 204", rec.Code, rec.Body.String())
	}
	second := f.askOpenSourceFrom("198.51.100.61:4040", openSourceAsk(t, repositoryTwoURL))
	if second.Code != http.StatusTooManyRequests {
		t.Errorf("the same account asking twice answered %d (%s), want 429: the allowance is the account's and not the address's", second.Code, second.Body.String())
	}
	f.actsAs(erinUID, erinEmail, signInGoogle)
	third := f.askOpenSourceFrom("198.51.100.62:4040", openSourceAsk(t, repositoryTwoURL))
	if third.Code != http.StatusNoContent {
		t.Errorf("another account's ask answered %d (%s), want 204: the bucket is the account's and not the route's", third.Code, third.Body.String())
	}
	if open := f.open(); len(open) != 2 {
		t.Errorf("the queue holds %d open request(s), want the two that were served", len(open))
	}
}

// TestOpenSourceQueueIsReadAsTheServiceWritesIt: the record this route writes is
// the one `queue` reads, so what the owner's page lists is the request the
// applicant made and not a second shape. It is a small case because it is a
// small claim: the queue's own reader answers the entry's email.
func TestOpenSourceQueueIsReadAsTheServiceWritesIt(t *testing.T) {
	f := newFixture(t)
	f.actsAs(danaUID, danaEmail, signInGoogle)
	if rec := f.askOpenSource(openSourceAsk(t, repositoryURL)); rec.Code != http.StatusNoContent {
		t.Fatalf("the ask answered %d (%s), want 204", rec.Code, rec.Body.String())
	}
	held, err := f.svc.queue(context.Background())
	if err != nil {
		t.Fatalf("reading the queue: %v", err)
	}
	if len(held) != 1 || held[0].Email != danaEmail {
		t.Errorf("the queue reads %+v, want the one request the ask wrote for %s", held, danaEmail)
	}
}
