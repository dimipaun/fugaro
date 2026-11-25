// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// Abuse, and the one boundary the slice's security rests on (subscriptions plan
// S3.2; the design docs/key-signing-service/spec.md §3.2, §3.3, §7.2 and
// Check 174). The tests here are about what a stranger can make the intake do,
// and about which half of the service may sign.
//
// The doors these cases reach are the portal's own since P9.3: the three
// website forms retired with their routes and what a stranger posts to is the
// open-source ask and the trial ask (api_opensource.go, api_trial.go), which is
// why every case here signs in and drives a route rather than a page.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestApplicantFieldsCannotInjectMailHeaders: §7.2 — nothing a caller sends is a
// header, an origin or a link the service writes. The address a request is filed
// against is the one the sign-in proved, and an address carrying a header line is
// not an address; a repository line is read as the deploy's own host's URL, so a
// line carrying a header is not a repository; and the notification that tells the
// owner a request is waiting leads to the portal's own queue page, which a
// caller's Host header cannot rewrite.
func TestApplicantFieldsCannotInjectMailHeaders(t *testing.T) {
	f := newFixture(t)

	// The account is the one the sign-in proved (§4.1), and it is read the way
	// every part of this service reads an address: one that carries a header line
	// is not one, so the request is refused rather than filed against a value
	// that would reach the owner's notification as a line of its own.
	for _, raw := range []string{
		danaEmail + "\r\nBcc: victim@elsewhere.example",
		danaEmail + "\nSubject: a subject of the applicant's",
		danaEmail + "\r\n\r\nfugaro",
		"careful@acme.example\r\n",
	} {
		f.signsInAs(danaUID, raw)
		rec := f.askOpenSource(openSourceAsk(t, repositoryURL))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("a sign-in naming %q was answered %d (%s), want 401: an address the service cannot read is one no request is filed against", raw, rec.Code, rec.Body.String())
		}
	}
	if open := f.open(); len(open) != 0 {
		t.Errorf("an address carrying a header line queued %+v, want nothing", open)
	}
	if got := len(f.mail.sent); got != 0 {
		t.Errorf("an address carrying a header line mailed %d message(s), want none", got)
	}
	if got := len(f.notifier.alerts); got != 0 {
		t.Errorf("an address carrying a header line notified the owner %d time(s), want none", got)
	}

	// A repository line is read as the deploy's own code host's URL before
	// anything is asked about or queued (§4.4), so a line carrying a header is a
	// line that host does not serve and the whole ask is refused.
	f.actsAs(danaUID, danaEmail, signInGoogle)
	rec := f.askOpenSource(openSourceAsk(t, "https://code.example/acme/widget\r\nBcc: victim@elsewhere.example"))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("a repository line carrying a header was answered %d (%s), want 422: what a caller typed is not an address this service dials", rec.Code, rec.Body.String())
	}
	if got := len(f.host.asked); got != 0 {
		t.Errorf("a repository line carrying a header asked the code host about %v, want no call", f.host.asked)
	}
	if open := f.open(); len(open) != 0 {
		t.Errorf("a repository line carrying a header queued %+v, want nothing", open)
	}

	// The link the notification carries is the portal's own queue page, built
	// from the deployment's own origin (portalOrigin) and never from the
	// request's Host header.
	origin, err := portalOrigin(f.cfg.URL)
	if err != nil {
		t.Fatalf("reading the portal's origin from the deployment's own: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, intakeOrigin+RouteAPIOpenSource, strings.NewReader(openSourceAsk(t, repositoryURL)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+portalIDToken)
	req.Host = "attacker.example"
	req.Header.Set("X-Forwarded-Host", "attacker.example")
	req.RemoteAddr = applicantIP
	rec = httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("the ask answered %d (%s), want 204", rec.Code, rec.Body.String())
	}
	alerts := f.notifier.alerts
	if len(alerts) != 1 {
		t.Fatalf("the ask notified the owner %d time(s), want one: a queue nobody is told about is a queue nobody reads (§4.5)", len(alerts))
	}
	if want := origin + routeOps; !strings.Contains(alerts[0].Body, want) {
		t.Errorf("the notification does not lead to the portal's own queue page (%s): %q", want, alerts[0].Body)
	}
	if strings.Contains(alerts[0].Body, "attacker.example") {
		t.Errorf("the caller's Host header reached the notification's link: %q", alerts[0].Body)
	}
	if alerts[0].Subject != queueSubject {
		t.Errorf("the notification's subject is %q, want the service's own constant (%q)", alerts[0].Subject, queueSubject)
	}
	if got := f.mail.sent; len(got) != 0 {
		t.Errorf("the ask mailed the applicant %d message(s), want none: a queued request is the owner's to answer (§4.5)", len(got))
	}
}

// TestRequestsAreThrottled: §7.2 — the intake is throttled per address and per
// IP, so it cannot be used to mail-bomb a third party's address or to enumerate
// (§7.1). The limits are the deploy's; the test's are its own.
func TestRequestsAreThrottled(t *testing.T) {
	f := newFixtureWith(t, Production, func(f *fixture) {
		f.cfg.Throttle = Limits{Requests: 3, Window: time.Hour}
	})

	// Three asks from one account: the three the limit allows.
	f.actsAs(danaUID, danaEmail, signInGoogle)
	for i := 0; i < 3; i++ {
		rec := f.askOpenSource(openSourceAsk(t, repositoryURL))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("ask %d inside the rate answered %d (%s), want 204", i, rec.Code, rec.Body.String())
		}
	}
	fourth := f.askOpenSource(openSourceAsk(t, repositoryURL))
	if fourth.Code != http.StatusTooManyRequests {
		t.Errorf("a fourth ask from one account answered %d (%s), want 429: the limit is per address", fourth.Code, fourth.Body.String())
	}
	if open := f.open(); len(open) != 3 {
		t.Errorf("the queue holds %d open request(s), want the three the rate allowed", len(open))
	}

	// And per IP: three asks from three accounts nobody has seen, all behind one
	// address, and the fourth is refused although its own account is new — the
	// bucket that filled is the connection's.
	const sharedIP = "198.51.100.9:1234"
	for _, account := range []struct{ uid, email string }{
		{"uid-a", "a@elsewhere.example"},
		{"uid-b", "b@elsewhere.example"},
		{"uid-c", "c@elsewhere.example"},
	} {
		f.actsAs(account.uid, account.email, signInGoogle)
		if rec := f.askOpenSourceFrom(sharedIP, openSourceAsk(t, repositoryURL)); rec.Code != http.StatusNoContent {
			t.Fatalf("an ask from %s answered %d (%s), want 204", account.email, rec.Code, rec.Body.String())
		}
	}
	before := len(f.open())
	f.actsAs("uid-d", "d@elsewhere.example", signInGoogle)
	if rec := f.askOpenSourceFrom(sharedIP, openSourceAsk(t, repositoryURL)); rec.Code != http.StatusTooManyRequests {
		t.Errorf("a fourth account behind one IP answered %d (%s), want 429: the limit is per IP too", rec.Code, rec.Body.String())
	}
	if open := f.open(); len(open) != before {
		t.Errorf("an ask refused over the IP's rate queued %d more request(s), want none", len(open)-before)
	}

	// The window moves: the same account is served again.
	f.advance(2 * time.Hour)
	f.actsAs(danaUID, danaEmail, signInGoogle)
	if rec := f.askOpenSource(openSourceAsk(t, repositoryURL)); rec.Code != http.StatusNoContent {
		t.Errorf("an ask one window later answered %d (%s), want 204: a throttle is a limit over a window, not a ban", rec.Code, rec.Body.String())
	}
}

// TestTheThrottleKeepsNothingARefusedRequestMade: §7.2's limiter is the only
// state an unauthenticated caller can add to, and the email address is the one
// thing it chooses: a limiter that wrote down every request would let a stranger
// grow the intake's memory without limit, one address per post, on requests it
// refused anyway. And what the window has passed goes: a bucket with nothing
// left in the window is dropped rather than kept as an empty one, so the map
// holds what the deploy's own rate allowed and not what was thrown at it.
func TestTheThrottleKeepsNothingARefusedRequestMade(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	limiter, err := newThrottle(Limits{Requests: 2, Window: time.Hour}, func() time.Time { return now })
	if err != nil {
		t.Fatalf("building the limiter: %v", err)
	}
	const peer = "ip:203.0.113.7"
	for i := 0; i < 2; i++ {
		if !limiter.allow(peer, "address:dev@acme.example") {
			t.Fatalf("request %d inside the rate was refused, want it allowed", i)
		}
	}
	if got := len(limiter.seen); got != 2 {
		t.Fatalf("the limiter holds %d bucket(s) after two allowed requests, want the two they made", got)
	}

	// The peer's rate is spent. A thousand posts from it, each with an address
	// nobody has seen, are all refused and leave nothing behind.
	for i := 0; i < 1000; i++ {
		if limiter.allow(peer, "address:fresh"+strconv.Itoa(i)+"@acme.example") {
			t.Fatalf("post %d was allowed past the peer's rate, want a refusal", i)
		}
	}
	if got := len(limiter.seen); got != 2 {
		t.Errorf("the limiter holds %d bucket(s) after a thousand refused posts with fresh addresses, want the two the allowed requests made: a stranger must not be able to grow this map", got)
	}

	// The window moves: the peer is inside its rate again, and the address of the
	// request that comes with it is the only new bucket.
	now = now.Add(time.Hour + time.Second)
	if !limiter.allow(peer, "address:someone@acme.example") {
		t.Fatal("the same peer one window later was refused, want the window to move")
	}
	if got := len(limiter.seen); got > 3 {
		t.Errorf("the limiter holds %d bucket(s) after a window moved, want the buckets of the two windows at most", got)
	}
}

// TestTheThrottleDropsABucketTheWindowEmptied: a bucket is not kept once the
// window has passed what it holds. What reads it is a request that is refused
// anyway — the other bucket is at its limit — and what it must leave behind is
// that bucket and not an empty one, so a key that applied once a year ago is not
// a thing the intake carries forever.
func TestTheThrottleDropsABucketTheWindowEmptied(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	limiter, err := newThrottle(Limits{Requests: 1, Window: time.Hour}, func() time.Time { return now })
	if err != nil {
		t.Fatalf("building the limiter: %v", err)
	}
	const peer = "ip:203.0.113.7"
	if !limiter.allow("address:first@acme.example", peer) {
		t.Fatal("the first request was refused, want it allowed")
	}

	// One window on, the peer is inside its rate again and its bucket holds the
	// new request; the first address's bucket holds nothing of the window.
	now = now.Add(2 * time.Hour)
	if !limiter.allow("address:second@acme.example", peer) {
		t.Fatal("a request one window later was refused, want it allowed")
	}
	// The stale address comes back, and the peer's rate refuses it — which is
	// where its emptied bucket is read and dropped.
	if limiter.allow("address:first@acme.example", peer) {
		t.Fatal("the peer's rate was spent and the request was allowed, want a refusal")
	}
	if _, held := limiter.seen["address:first@acme.example"]; held {
		t.Error("a bucket whose requests the window has passed is still held, want it dropped when it is next read")
	}
	if got := len(limiter.seen); got != 2 {
		t.Errorf("the limiter holds %d bucket(s), want the peer's and the address the window holds", got)
	}
}

// TestTheThrottleBucketsByTheForwardedAddress: §7.2's per-IP bucket is the
// address the platform's front end saw and not the peer the connection ends at.
// Every applicant arrives from that front end — on Cloud Run one peer for the
// whole internet — so a bucket keyed on the peer is one bucket for everyone, and
// the addresses a caller writes into X-Forwarded-For itself are the entries
// before the last and are not the bucket. With no header at all there is nothing
// but the peer, and that is the bucket.
//
// Every ask is made as an account of its own, so what refuses one is the IP's
// bucket and never the account's (TestOpenSourceRequestIsThrottledPerAccount is
// the other half).
func TestTheThrottleBucketsByTheForwardedAddress(t *testing.T) {
	f := newFixtureWith(t, Production, func(f *fixture) {
		f.cfg.Throttle = Limits{Requests: 1, Window: time.Hour}
	})
	const front = "10.0.0.1:443" // the platform's front end, one peer for everyone

	send := func(uid, email, forwarded string) *httptest.ResponseRecorder {
		f.t.Helper()
		f.actsAs(uid, email, signInGoogle)
		req := httptest.NewRequest(http.MethodPost, intakeOrigin+RouteAPIOpenSource, strings.NewReader(openSourceAsk(t, repositoryURL)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+portalIDToken)
		if forwarded != "" {
			req.Header.Set("X-Forwarded-For", forwarded)
		}
		req.RemoteAddr = front
		rec := httptest.NewRecorder()
		f.handler.ServeHTTP(rec, req)
		return rec
	}

	if rec := send("uid-one", "one@acme.example", "198.51.100.9"); rec.Code != http.StatusNoContent {
		t.Fatalf("the first ask answered %d (%s), want 204", rec.Code, rec.Body.String())
	}

	// Another address behind the same front end: its own bucket, and it is not
	// the first applicant's.
	if rec := send("uid-two", "two@acme.example", "203.0.113.5"); rec.Code != http.StatusNoContent {
		t.Errorf("a second address behind the same front end answered %d (%s), want 204: the bucket is the forwarded address, not the peer", rec.Code, rec.Body.String())
	}

	// The first address again, with an entry the caller wrote itself in front of
	// the one the front end appended: the last entry is the bucket, so the
	// caller cannot choose its own and the bucket it really is in is full.
	if rec := send("uid-three", "three@acme.example", "192.0.2.7, 198.51.100.9"); rec.Code != http.StatusTooManyRequests {
		t.Errorf("a caller that wrote its own X-Forwarded-For entry answered %d (%s), want 429", rec.Code, rec.Body.String())
	}

	// No header at all — a test, a local run — and the peer is all there is.
	if rec := send("uid-four", "four@acme.example", ""); rec.Code != http.StatusNoContent {
		t.Errorf("the first ask from one peer with no forwarded address answered %d (%s), want 204: the fallback is the peer", rec.Code, rec.Body.String())
	}
	if rec := send("uid-five", "five@acme.example", ""); rec.Code != http.StatusTooManyRequests {
		t.Errorf("a second ask from the same peer with no forwarded address answered %d (%s), want 429: the fallback bucket is the peer and it is spent", rec.Code, rec.Body.String())
	}
}

// TestOnlyTheMinterCallsTheSigner: §3.2/§3.3 and Check 174 — the split T13
// rules is that the internet-facing intake cannot sign. The intake reaches a
// key through Mints alone, it holds no Signer anywhere in its types, and no
// file of its own calls one.
func TestOnlyTheMinterCallsTheSigner(t *testing.T) {
	// 1. No type the intake is built from carries a Signer.
	signerType := reflect.TypeOf((*Signer)(nil)).Elem()
	for _, typ := range []reflect.Type{reflect.TypeOf(Service{}), reflect.TypeOf(Deps{}), reflect.TypeOf(Config{})} {
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			if field.Type == signerType || field.Type.Implements(signerType) {
				t.Errorf("%s.%s carries a Signer: the intake is reachable from the internet and mints through the minter's own service alone (T13, §3.2)", typ, field.Name)
			}
		}
	}

	// 2. No file that serves the intake's own routes calls one. The minter's
	// file does, and this test's own files do because the fixture holds one.
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("listing the package's files: %v", err)
	}
	callers := 0
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		if bytes.Contains(src, []byte(".Sign(")) {
			callers++
			if filepath.Base(path) != "minter.go" {
				t.Errorf("%s calls a signer: the intake's own code must reach a key through Mints and never through a signing call (Check 174)", path)
			}
		}
	}
	if callers != 1 {
		t.Errorf("%d file(s) call a signer, want the minter's one: a second signing path is a second identity that can mint (§3.3)", callers)
	}

	// 3. The intake's answers are what the signer count says they are: a request
	// a person decides is answered from the queue and reaches no signer at all,
	// and the key a route does issue is the minter's.
	f := newFixture(t)
	f.actsAs(danaUID, danaEmail, signInGoogle)
	if rec := f.askOpenSource(openSourceAsk(t, repositoryURL)); rec.Code != http.StatusNoContent {
		t.Fatalf("the open-source ask answered %d (%s), want 204", rec.Code, rec.Body.String())
	}
	if got := f.signer.calls; got != 0 {
		t.Errorf("the ask a person has to decide signed %d key(s), want none: §17 puts the licence in the owner's hands, so nothing is minted before they answer", got)
	}
	trial := trialAs(t)
	if rec := trial.ask(trialBody(workspaceID)); rec.Code != http.StatusNoContent {
		t.Fatalf("the trial ask answered %d (%s), want 204", rec.Code, rec.Body.String())
	}
	if got := trial.signer.calls; got != 1 {
		t.Errorf("the trial ask signed %d key(s), want the minter's one", got)
	}
}
