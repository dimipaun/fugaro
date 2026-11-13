// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package keyservice

// The API's error reporting's own tests (portal plan P1.6): a panicking route
// and a 5xx answer reach Sentry without the request, and a deployment with no
// DSN reaches nothing at all.
//
// The Sentry a test reports to is an httptest server on the loopback, which is
// the only host a hermetic test reaches: the DSN points at it and the transport
// is the synchronous one the API builds, so the envelope is in the test's hands
// before the answer is (errreport.go's own reason for that transport).

import (
	"encoding/json"
	"io"
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

	// panicProbeSays is what the panicking route panics with. It is the route's
	// own sentence and not the request's, because the panic's own value is the
	// one thing about a failure a report carries.
	panicProbeSays = "the probe's own failure"
)

func init() {
	registerAPI(http.MethodPost, apiPanicPath, panicProbe)
	registerAPI(http.MethodPost, apiFailPath, failProbe)
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

// TestNoDSNReportsNothing: a deployment that names no DSN reports nowhere, and
// its routes answer the same failures they would with one. The environment
// names a DSN the test can watch, because sentry-go reads SENTRY_DSN out of it
// for a client built without one — a report to a host the deploy never named is
// what this file builds no client at all to avoid.
func TestNoDSNReportsNothing(t *testing.T) {
	stand := newSentryStand(t)
	t.Setenv("SENTRY_DSN", stand.dsn())

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
func (s *sentryStand) dsn() string {
	return "http://" + strings.Repeat("a", 32) + "@" + strings.TrimPrefix(s.server.URL, "http://") + "/1"
}

// posted is every envelope the stand was sent, in order.
func (s *sentryStand) posted() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.envelopes...)
}
