// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// Abuse, and the one boundary the slice's security rests on (subscriptions plan
// S3.2; the design docs/design/key-signing-service.md §3.2, §3.3, §7.2 and
// Check 174). The tests here are about what a stranger can make the intake do,
// and about which half of the service may sign.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestApplicantFieldsCannotInjectMailHeaders: §7.2 — the address the intake
// mails to is the one it verified and nothing an applicant typed is a header
// value. Everything else it can make the intake carry is body text.
func TestApplicantFieldsCannotInjectMailHeaders(t *testing.T) {
	f := newFixture(t)
	hostile := []string{
		applicantAddress + "\r\nBcc: victim@elsewhere.example",
		applicantAddress + "\nSubject: a subject of the applicant's",
		applicantAddress + "\r\n\r\nfugaro",
		"careful@acme.example\r\n",
	}
	for _, raw := range hostile {
		answerBody(t, f.apply(raw))
	}
	if len(f.mail.sent) != 0 {
		t.Errorf("an address carrying a header line mailed %d message(s), want none: the address is the one the intake verified, and one it cannot read is one it does not write", len(f.mail.sent))
	}

	// The link the intake mails is its own: a caller's Host header is not the
	// intake's origin, so nothing a caller sends can rewrite where the
	// verification link points.
	body := url.Values{"email": {"person@fresh.example"}}.Encode()
	req := httptest.NewRequest(http.MethodPost, intakeOrigin+RouteFreeSeat, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Host = "attacker.example"
	req.Header.Set("X-Forwarded-Host", "attacker.example")
	req.RemoteAddr = applicantIP
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	answerBody(t, rec)
	if len(f.mail.sent) != 1 {
		t.Fatalf("the application mailed %d message(s), want the verification one", len(f.mail.sent))
	}
	mail := f.mail.sent[0]
	if !strings.Contains(mail.Body, intakeOrigin+RouteVerify) {
		t.Errorf("the verification link is not on the intake's own origin (%s): %q", intakeOrigin, mail.Body)
	}
	if strings.Contains(mail.Body, "attacker.example") {
		t.Errorf("the caller's Host header reached the mailed link: %q", mail.Body)
	}
	if mail.To != "person@fresh.example" {
		t.Errorf("the mail went to %q, want the verified address", mail.To)
	}
	if mail.Subject != verificationSubject {
		t.Errorf("the mail's subject is %q, want the service's own (%q)", mail.Subject, verificationSubject)
	}
	if strings.ContainsAny(mail.To, "\r\n") {
		t.Errorf("the mail's recipient carries a control character: %q", mail.To)
	}

	// A request the applicant typed is data in the record a person reads, never
	// a header of any mail: the manual form mails nothing at all.
	hostileText := "a seminar\r\nBcc: victim@elsewhere.example\r\n"
	answerBody(t, f.post(RouteRequest, url.Values{"email": {"prof@academic.example"}, "request": {hostileText}}))
	if got := len(f.mail.sent); got != 1 {
		t.Errorf("the manual form mailed %d message(s) in all, want the verification one alone", got)
	}
	queued := f.queued()
	if len(queued) != 1 || queued[0].Detail != hostileText {
		t.Errorf("the queued request is %+v, want the text the applicant wrote: the admin page escapes it on render (§6, S3.4)", queued)
	}
}

// TestRequestsAreThrottled: §7.2 — the intake is throttled per address and per
// IP, so it cannot be used to mail-bomb a third party's address or to enumerate
// (§7.1). The limits are the deploy's; the test's are its own.
func TestRequestsAreThrottled(t *testing.T) {
	f := newFixtureWith(t, Production, func(f *fixture) {
		f.cfg.Throttle = Limits{Requests: 3, Window: time.Hour}
	})

	// Three applications from one address: the three the limit allows.
	for _, at := range []string{"one@acme.example", "two@acme.example", "three@acme.example"} {
		answerBody(t, f.apply(at))
	}
	if got := len(f.mail.sent); got != 3 {
		t.Fatalf("the first three applications mailed %d message(s), want three", got)
	}
	answerBody(t, f.apply("four@acme.example"))
	if got := len(f.mail.sent); got != 3 {
		t.Errorf("a fourth application from one address mailed %d message(s) in all, want the first three: the limit is per address", got)
	}

	// And per IP: three applications from three addresses nobody has seen, all
	// behind one address, and the fourth is refused although its own address is
	// new — the bucket that filled is the connection's.
	const sharedIP = "198.51.100.9:1234"
	for _, at := range []string{"a@elsewhere.example", "b@elsewhere.example", "c@elsewhere.example"} {
		answerBody(t, f.postFrom(sharedIP, RouteFreeSeat, url.Values{"email": {at}}))
	}
	before := len(f.mail.sent)
	answerBody(t, f.postFrom(sharedIP, RouteFreeSeat, url.Values{"email": {"d@elsewhere.example"}}))
	if got := len(f.mail.sent); got != before {
		t.Errorf("a fourth address behind one IP mailed %d message(s), want none: the limit is per IP too", got-before)
	}

	// The window moves: the same address is served again.
	f.advance(2 * time.Hour)
	answerBody(t, f.apply("five@acme.example"))
	if got := len(f.mail.sent); got != before+1 {
		t.Errorf("an application one window later mailed %d message(s), want one: a throttle is a limit over a window, not a ban", got-before)
	}
}

// TestTheThrottleBucketsByTheForwardedAddress: §7.2's per-IP bucket is the
// address the platform's front end saw and not the peer the connection ends at.
// Every applicant arrives from that front end — on Cloud Run one peer for the
// whole internet — so a bucket keyed on the peer is one bucket for everyone, and
// the addresses a caller writes into X-Forwarded-For itself are the entries
// before the last and are not the bucket. With no header at all there is nothing
// but the peer, and that is the bucket.
func TestTheThrottleBucketsByTheForwardedAddress(t *testing.T) {
	f := newFixtureWith(t, Production, func(f *fixture) {
		f.cfg.Throttle = Limits{Requests: 1, Window: time.Hour}
	})
	const front = "10.0.0.1:443" // the platform's front end, one peer for everyone

	send := func(forwarded, email string) *httptest.ResponseRecorder {
		f.t.Helper()
		req := httptest.NewRequest(http.MethodPost, intakeOrigin+RouteFreeSeat, strings.NewReader(url.Values{"email": {email}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if forwarded != "" {
			req.Header.Set("X-Forwarded-For", forwarded)
		}
		req.RemoteAddr = front
		rec := httptest.NewRecorder()
		f.handler.ServeHTTP(rec, req)
		return rec
	}

	answerBody(t, send("198.51.100.9", "one@acme.example"))
	if got := len(f.mail.sent); got != 1 {
		t.Fatalf("the first applicant mailed %d message(s), want the verification one", got)
	}

	// Another address behind the same front end: its own bucket, and it is not
	// the first applicant's.
	before := len(f.mail.sent)
	answerBody(t, send("203.0.113.5", "two@acme.example"))
	if got := len(f.mail.sent); got != before+1 {
		t.Errorf("a second address behind the same front end mailed %d message(s), want one more: the bucket is the forwarded address, not the peer", got-before)
	}

	// The first address again, with an entry the caller wrote itself in front of
	// the one the front end appended: the last entry is the bucket, so the
	// caller cannot choose its own and the bucket it really is in is full.
	before = len(f.mail.sent)
	answerBody(t, send("192.0.2.7, 198.51.100.9", "three@acme.example"))
	if got := len(f.mail.sent); got != before {
		t.Errorf("a caller that wrote its own X-Forwarded-For entry mailed %d message(s), want none", got-before)
	}

	// No header at all — a test, a local run — and the peer is all there is.
	answerBody(t, send("", "four@acme.example"))
	before = len(f.mail.sent)
	answerBody(t, send("", "five@acme.example"))
	if got := len(f.mail.sent); got != before {
		t.Errorf("two requests from one peer with no forwarded address mailed %d message(s), want none: the fallback is the peer", got-before)
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

	// 3. The intake's answers are what the signer count says they are: an
	// application mints through the minter, and one that is queued or refused
	// never reaches a signer at all.
	f := newFixture(t)
	f.apply("maintainer@gmail.example")
	f.apply("dev@buyer.example")
	if got := f.signer.calls; got != 0 {
		t.Errorf("the queued and refused applications signed %d key(s), want none", got)
	}
	f.apply(applicantAddress)
	f.verify(f.mailedToken())
	if got := f.signer.calls; got != 1 {
		t.Errorf("the verified application signed %d key(s), want the minter's one", got)
	}
}

// TestTheOpenSourceCheckRefusesWithoutProofs: D452 is open, so the service is
// handed a proof check or it has none — and with none it queues nothing and
// mints nothing rather than guessing what proof means.
func TestTheOpenSourceCheckRefusesWithoutProofs(t *testing.T) {
	f := newFixture(t)
	svc, err := New(f.svc.cfg, Deps{
		Store:    f.store,
		Mints:    Minter{Signer: f.signer, Keys: f.keys, Now: f.clock},
		Mailer:   f.mail,
		CodeHost: f.host,
		Proofs:   nil,
		Now:      f.clock,
	})
	if err != nil {
		t.Fatalf("building an intake with no proof check: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, intakeOrigin+RouteOpenSource, strings.NewReader(url.Values{
		"email":        {applicantAddress},
		"repositories": {repositoryURL},
	}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = applicantIP
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)

	answerBody(t, rec)
	if got := f.signer.calls; got != 0 {
		t.Errorf("an open-source application with no proof check signed %d key(s), want none", got)
	}
	if len(f.mail.sent) != 0 {
		t.Errorf("an open-source application with no proof check mailed %d message(s), want none", len(f.mail.sent))
	}
}
