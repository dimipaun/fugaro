// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The API's error reporting's own tests (portal plan P1.6): a panicking route
// and a 5xx answer reach Sentry without the request, a panic whose answer is
// already under way goes on to the server, and a deployment with no DSN (or one
// Sentry cannot read) reaches nothing at all and keeps its panics in its log.
//
// The Sentry a test reports to is an httptest server on the loopback, which is
// the only host a hermetic test reaches: the DSN points at it and the transport
// is the synchronous one the API builds, so the envelope is in the test's hands
// before the answer is (errreport.go's own reason for that transport).

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// The probes are this file's own doors, registered the way an area's file
// registers one (api.go): the two failures reporting is about, so a test reads
// what the reporter makes of each without an area existing yet.
const (
	apiPanicPath = "/api/probe/panic"
	apiFailPath  = "/api/probe/fail"
	apiHalfPath  = "/api/probe/half"

	// halfAnswer is how much of an answer the third probe had written when it
	// panicked: a refusal written after it would read as the rest of one answer,
	// which is the whole of what the wrapper has to avoid.
	halfAnswer = `{"organisation":{"id":"acme`

	// panicProbeSays is what the panicking route panics with. It is the route's
	// own sentence and not the request's, because the panic's own value is the
	// one thing about a failure a report carries.
	panicProbeSays = "the probe's own failure"
)

func init() {
	registerAPI(http.MethodPost, apiPanicPath, panicProbe)
	registerAPI(http.MethodPost, apiFailPath, failProbe)
	registerAPI(http.MethodPost, apiHalfPath, panicAfterWriting)
}

// panicProbe is a door that panics, which is the failure no route can answer.
func panicProbe(_ *Service, _ http.ResponseWriter, _ *http.Request) {
	panic(panicProbeSays)
}

// failProbe is a door that answers the way a route's own failure does: a 5xx
// and nothing else. Its status is a 502 rather than a 500 so that a test reads
// the rule as "5xx" and not as "the one status a panic happens to answer with"
// (P1.6).
func failProbe(_ *Service, w http.ResponseWriter, _ *http.Request) {
	refuseAPI(w, http.StatusBadGateway, "upstream_failed", "The merchant did not answer.")
}

// panicAfterWriting is a door that fails after it has answered: the status line
// and part of a body are out, and then it panics. It writes no refusal of its
// own, so what the test reads after the panic is the wrapper's doing and not
// this door's.
func panicAfterWriting(_ *Service, w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", apiJSONType)
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, halfAnswer)
	panic(panicProbeSays)
}

// TestPanicIsReportedWithoutTheBody: a route that panics is reported and
// refused, and the report carries the route, the method and the panic — and no
// body, no header and no address, because the request reaches a third party's
// service through nothing but this file (P1.6).
func TestPanicIsReportedWithoutTheBody(t *testing.T) {
	stand := newSentryStand(t)
	f := newFixtureWith(t, Production, func(f *fixture) { f.cfg.SentryDSN = stand.dsn() })

	const (
		body       = "the body a caller sent, holding acme's own records"
		headerName = "X-Caller-Reference"
		headerSame = "acme-widget-42"
		caller     = "198.51.100.9"
	)

	req := httptest.NewRequest(http.MethodPost, intakeOrigin+apiPanicPath, strings.NewReader(body))
	req.Header.Set("Content-Type", apiJSONType)
	req.Header.Set(headerName, headerSame)
	req.RemoteAddr = caller + ":443"
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	// The panic is answered, and with the API's one refusal shape: a door that
	// recovered a panic and wrote nothing would hand the SPA an empty 200.
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("a panicking route answered %d, want %d: %s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	var refused WireRefusal
	if err := json.Unmarshal(rec.Body.Bytes(), &refused); err != nil {
		t.Fatalf("the answer to a panic is not the API's refusal: %v: %s", err, rec.Body.String())
	}
	if refused.Error.Code != apiFailedCode || refused.Error.Message != apiFailedMessage {
		t.Errorf("a panic was refused with %+v, want %q and %q", refused.Error, apiFailedCode, apiFailedMessage)
	}

	posted := stand.posted()
	if len(posted) != 1 {
		t.Fatalf("the panic reached Sentry %d time(s), want one", len(posted))
	}
	held := posted[0]
	for _, want := range []string{apiPanicPath, panicProbeSays, `"status":"500"`} {
		if !strings.Contains(held, want) {
			t.Errorf("the report does not carry %s, so it says nothing about what failed:\n%s", want, held)
		}
	}
	for _, unwanted := range []struct {
		what string
		held string
	}{
		{"the body a caller sent", body},
		{"the name of a header the caller sent", headerName},
		{"a header's value", headerSame},
		{"the caller's address", caller},
	} {
		if strings.Contains(held, unwanted.held) {
			t.Errorf("the report carries %s (%q): a report reaches Sentry and never the request (§7.2):\n%s", unwanted.what, unwanted.held, held)
		}
	}
}

// TestAPanicThatAlreadyAnsweredGoesOnToTheServer (P1.6): a route whose answer is
// already under way when it panics is not answered a second time. A refusal
// written after the half answer would reach the client as the rest of that
// answer — a 500 the API never meant, which is worse than the connection the
// server closes over a panic. So the panic goes on to the server, unchanged, and
// the failure is still reported: the wrapper's recover is what would have taken
// that report away.
//
// The test stands where net/http's own recover stands, since a panic recovered
// inside the handler is one the server never hears about; the recorder is the
// answer as far as it got.
func TestAPanicThatAlreadyAnsweredGoesOnToTheServer(t *testing.T) {
	stand := newSentryStand(t)
	f := newFixtureWith(t, Production, func(f *fixture) { f.cfg.SentryDSN = stand.dsn() })

	req := httptest.NewRequest(http.MethodPost, intakeOrigin+apiHalfPath, strings.NewReader("{}"))
	req.Header.Set("Content-Type", apiJSONType)
	req.RemoteAddr = applicantIP
	rec := httptest.NewRecorder()

	var raised any
	func() {
		defer func() { raised = recover() }()
		f.handler.ServeHTTP(rec, req)
	}()

	if raised == nil {
		t.Fatal("a panic the wrapper could not answer was swallowed, and the server never saw it: a truncated body then reads as a whole answer")
	}
	if raised != panicProbeSays {
		t.Errorf("the server was handed %v, want the route's own panic (%q)", raised, panicProbeSays)
	}

	// The caller's answer is the half the route wrote, and nothing is written
	// after it: a refusal here would be a second status and a second body.
	if rec.Code != http.StatusOK {
		t.Errorf("the answer went out as %d, want %d: the route had already answered when it panicked", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != halfAnswer {
		t.Errorf("the caller's answer is %q, want the half the route wrote (%q) and no refusal after it", got, halfAnswer)
	}

	posted := stand.posted()
	if len(posted) != 1 {
		t.Fatalf("the panic reached Sentry %d time(s), want one", len(posted))
	}
	for _, want := range []string{apiHalfPath, panicProbeSays, `"status":"500"`} {
		if !strings.Contains(posted[0], want) {
			t.Errorf("the report carries no %s, so a panic beside a half answer is reported as half a failure:\n%s", want, posted[0])
		}
	}
}

// TestNoDSNReportsNothing: a deployment that names no DSN reports nowhere, and
// its routes answer the same failures they would with one. The environment
// names a DSN the test can watch, because sentry-go reads SENTRY_DSN out of it
// for a client built without one — a report to a host the deploy never named is
// what this file builds no client at all to avoid.
//
// Reporting nowhere is not recording nowhere: the panic reaches the process's
// own log, which is the only trace such a deployment has — the recover that
// answers the route is what keeps net/http from writing one itself.
func TestNoDSNReportsNothing(t *testing.T) {
	stand := newSentryStand(t)
	t.Setenv("SENTRY_DSN", stand.dsn())
	said := captureLog(t)

	f := newFixture(t)

	if rec := f.api(http.MethodPost, apiPanicPath, "{}"); rec.Code != http.StatusInternalServerError {
		t.Errorf("a panicking route answered %d with nowhere to report, want %d: reporting is off, and answering is not", rec.Code, http.StatusInternalServerError)
	}
	if rec := f.api(http.MethodPost, apiFailPath, "{}"); rec.Code != http.StatusBadGateway {
		t.Errorf("a failing route answered %d with nowhere to report, want %d", rec.Code, http.StatusBadGateway)
	}
	if got := stand.posted(); len(got) != 0 {
		t.Errorf("a deployment with no DSN reported %d event(s), want none:\n%s", len(got), strings.Join(got, "\n"))
	}
	for _, want := range []string{apiPanicPath, panicProbeSays} {
		if got := said.text(); !strings.Contains(got, want) {
			t.Errorf("the log of a deployment with no DSN does not carry %s, so its panic was recorded nowhere at all:\n%s", want, got)
		}
	}
}

// TestAnUnreadableDSNReportsNothing (P1.6): a DSN Sentry cannot read leaves the
// API reporting nowhere, which is the same off a deployment with no DSN holds —
// refused once, at the mount, which has no error to return, so the service
// starts and keeps answering instead of refusing to (the reading
// newErrReporter states). The stand is the host that DSN names, and it is
// reached by nothing: a value Sentry refuses is not a host this service reports
// to. The panic still reaches the process's log, and so does the sentence that
// says reporting is off.
func TestAnUnreadableDSNReportsNothing(t *testing.T) {
	stand := newSentryStand(t)
	said := captureLog(t)

	f := newFixtureWith(t, Production, func(f *fixture) { f.cfg.SentryDSN = stand.unreadable() })

	if rec := f.api(http.MethodPost, apiPanicPath, "{}"); rec.Code != http.StatusInternalServerError {
		t.Errorf("a panicking route answered %d beside an unreadable DSN, want %d: reporting is off, and answering is not", rec.Code, http.StatusInternalServerError)
	}
	if got := stand.posted(); len(got) != 0 {
		t.Errorf("a deployment whose DSN Sentry cannot read reported %d event(s), want none:\n%s", len(got), strings.Join(got, "\n"))
	}
	for _, want := range []struct {
		what string
		held string
	}{
		{"the refusal of the DSN, which is what tells an operator reporting is off", "cannot read it"},
		{"the route that panicked", apiPanicPath},
		{"what it panicked with", panicProbeSays},
	} {
		if got := said.text(); !strings.Contains(got, want.held) {
			t.Errorf("the log does not carry %s:\n%s", want.what, got)
		}
	}
}

// TestFiveHundredAnswersAreReported (P1.6): an answer that failed is reported
// with the status it went out with, and an answer a caller asked for and got is
// not — a refusal the API makes on purpose is the API working.
func TestFiveHundredAnswersAreReported(t *testing.T) {
	stand := newSentryStand(t)
	f := newFixtureWith(t, Production, func(f *fixture) { f.cfg.SentryDSN = stand.dsn() })

	if rec := f.api(http.MethodPost, apiFailPath, "{}"); rec.Code != http.StatusBadGateway {
		t.Fatalf("the failing route answered %d, want %d", rec.Code, http.StatusBadGateway)
	}
	posted := stand.posted()
	if len(posted) != 1 {
		t.Fatalf("the failed answer reached Sentry %d time(s), want one", len(posted))
	}
	for _, want := range []string{apiFailPath, `"status":"502"`} {
		if !strings.Contains(posted[0], want) {
			t.Errorf("the report does not carry %s:\n%s", want, posted[0])
		}
	}

	// A refusal is not a failure: a path no area registered is the API's own
	// 404, and nothing about it belongs in a report.
	if rec := f.api(http.MethodPost, "/api/nothing/here", "{}"); rec.Code != http.StatusNotFound {
		t.Fatalf("a path no area registered answered %d, want %d", rec.Code, http.StatusNotFound)
	}
	if got := stand.posted(); len(got) != 1 {
		t.Errorf("the API reported %d event(s) after a 404, want the one failure alone", len(got))
	}
}

// sentryStand is a Sentry a test can point a DSN at: an httptest server on the
// loopback and the envelopes posted to it, in order.
type sentryStand struct {
	server *httptest.Server

	mu        sync.Mutex
	envelopes []string
}

func newSentryStand(t *testing.T) *sentryStand {
	t.Helper()
	stand := &sentryStand{}
	stand.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading an envelope Sentry was sent: %v", err)
		}
		stand.mu.Lock()
		stand.envelopes = append(stand.envelopes, string(body))
		stand.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(stand.server.Close)
	return stand
}

// dsn is the DSN a client reaches this stand with. The public key of a DSN is
// not a secret — it ships in every client that reports — and the test builds
// one here rather than naming one anywhere.
func (s *sentryStand) dsn() string { return "http://" + s.key() + "@" + s.address() + "/1" }

// unreadable is a DSN naming this stand with a scheme a DSN does not have, so
// the host in it is one this service could have reached and sentry-go refuses
// the value before it ever does.
func (s *sentryStand) unreadable() string { return "ftp://" + s.key() + "@" + s.address() + "/1" }

func (s *sentryStand) key() string { return strings.Repeat("a", 32) }

func (s *sentryStand) address() string { return strings.TrimPrefix(s.server.URL, "http://") }

// posted is every envelope the stand was sent, in order.
func (s *sentryStand) posted() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.envelopes...)
}

// logCapture is the process's own log while one test runs, which is where a
// deployment that reports nowhere has its failures written. log is one
// package-level logger, so a test that reads it hands it back on the way out.
type logCapture struct {
	mu   sync.Mutex
	said strings.Builder
}

func (c *logCapture) Write(body []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.said.Write(body)
}

// text is everything written to the log so far.
func (c *logCapture) text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.said.String()
}

func captureLog(t *testing.T) *logCapture {
	t.Helper()
	held := &logCapture{}
	done := log.Writer()
	log.SetOutput(held)
	t.Cleanup(func() { log.SetOutput(done) })
	return held
}
