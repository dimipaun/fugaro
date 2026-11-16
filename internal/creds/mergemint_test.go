package creds

// The merge mint client's own tests (mergemint.go, E16). They are the client's
// half of the route's contract: what the call carries, what the client refuses
// before it makes one, and what it refuses to hand on when the service answers.
//
// Everything here is hermetic (Convention 9): the service is an httptest server
// on loopback and no test opens a socket to anything else. No literal in this
// file is, or looks like, a credential (Convention 2): the token, the ID token
// and the App ID are built here and mean nothing anywhere.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// mergeHead is a commit-shaped head: forty hex digits derived from a number
// rather than written down.
func mergeHead(n int) string { return fmt.Sprintf("%040x", n) }

// mintCall is one call the fake credentials service answered.
type mintCall struct {
	path  string
	auth  string
	body  string
	run   string
	job   string
	agent string
}

// mintFake stands in for the installation's credentials service. It records what
// it was asked and answers with what the test says, so the client's request is
// asserted against the bytes that actually left it rather than against the
// struct it built.
type mintFake struct {
	srv *httptest.Server

	mu    sync.Mutex
	calls []mintCall

	// status is the status the answer carries; 0 is 200.
	status int
	// answer is the body the answer carries.
	answer string
	// location, when set, is the Location header of a 302 the fake answers
	// with: the redirect the client must not follow.
	location string
}

func newMintFake(t *testing.T) *mintFake {
	t.Helper()
	f := &mintFake{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading the call: %v", err)
		}
		f.mu.Lock()
		f.calls = append(f.calls, mintCall{
			path:  r.URL.Path,
			auth:  r.Header.Get("Authorization"),
			body:  string(body),
			run:   r.Header.Get("X-Fugaro-Run-Identity"),
			job:   r.Header.Get("X-Fugaro-Run-Job"),
			agent: r.Header.Get("User-Agent"),
		})
		status, answer, location := f.status, f.answer, f.location
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if location != "" {
			w.Header().Set("Location", location)
		}
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// seen is the calls the fake answered, in order.
func (f *mintFake) seen() []mintCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]mintCall(nil), f.calls...)
}

// mintAnswer is a well-formed answer body: the token, its life, and the
// repository it was minted for.
func mintAnswer(token, repo string, expires time.Time) string {
	b, err := json.Marshal(map[string]any{"token": token, "expires_at": expires, "repository": repo})
	if err != nil {
		panic("creds test: building an answer: " + err.Error())
	}
	return string(b)
}

// testToken and testIDToken are the values these tests move around. They are
// named so that a search for them in a log, an error or a line is a search for
// something that could only be there by mistake.
const (
	testToken   = "creds-test-token-not-a-real-credential"
	testIDToken = "creds-test-id-token-not-a-real-credential"
	testAppID   = "1234"
	testRepo    = "acme/aurora"
)

// mintClient is the client under test, pointed at the fake: its URL is the
// fake's loopback address, its ID token is the test's, and its clock is fixed so
// the answer's expiry is comparable exactly.
func mintClient(t *testing.T, f *mintFake, now time.Time) *ServiceMergeMint {
	t.Helper()
	return &ServiceMergeMint{
		URL:     f.srv.URL,
		AppID:   testAppID,
		IDToken: func(context.Context) (string, error) { return testIDToken, nil },
		Now:     func() time.Time { return now },
	}
}

// TestMergeMintClientAsksForOnePullRequestAtOneHead: the call the client makes
// is the service's contract and nothing more — the path, the daemon's own ID
// token as the whole of its identity, and four body fields.
//
// The absence of the run identity's headers is asserted rather than assumed: the
// route's caller holds none, and a client that presented one would be answered
// by the service's run-identity refusal rather than by the class.
func TestMergeMintClientAsksForOnePullRequestAtOneHead(t *testing.T) {
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	f := newMintFake(t)
	f.answer = mintAnswer(testToken, testRepo, now.Add(time.Hour))
	m := mintClient(t, f, now)

	tok, err := m.MintMerge(t.Context(), testRepo, 7, mergeHead(1))
	if err != nil {
		t.Fatalf("MintMerge refused a call the service admits: %v", err)
	}
	if tok.Token != testToken || tok.Repository != testRepo || !tok.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Errorf("the token = %+v, want the answer's own token, life and repository", tok)
	}

	calls := f.seen()
	if len(calls) != 1 {
		t.Fatalf("the client made %d calls, want one: %v", len(calls), calls)
	}
	call := calls[0]
	if call.path != MergeMintPath {
		t.Errorf("the client called %s, want %s", call.path, MergeMintPath)
	}
	if call.auth != "Bearer "+testIDToken {
		t.Errorf("the client presented %q, want the daemon's own ID token alone", call.auth)
	}
	if call.run != "" || call.job != "" {
		t.Errorf("the client presented a run identity (%q/%q), and this route's caller holds none", call.run, call.job)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(call.body), &sent); err != nil {
		t.Fatalf("the call's body is not JSON: %v (%q)", err, call.body)
	}
	want := map[string]any{"repository": testRepo, "number": float64(7), "head_sha": mergeHead(1), "app_id": testAppID}
	if len(sent) != len(want) {
		t.Fatalf("the call carried %d fields %v, want the four this contract has", len(sent), sent)
	}
	for field, want := range want {
		if got := sent[field]; got != want {
			t.Errorf("the call's %s = %v, want %v", field, got, want)
		}
	}
}

// TestMergeMintClientCarriesTheServicesRefusal: a refusal is a sentinel with the
// service's own error code beside it — and never the service's sentence, which is
// written for an operator's log, and never a value.
func TestMergeMintClientCarriesTheServicesRefusal(t *testing.T) {
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		status int
		code   string
	}{
		{"the head moved", http.StatusBadRequest, "request_refused"},
		{"the class refused", http.StatusUnauthorized, "unverified_job_identity"},
		{"the rate", http.StatusServiceUnavailable, "state_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMintFake(t)
			f.status = tc.status
			f.answer = `{"error":"` + tc.code + `","message":"` + testToken + ` is in the service's own sentence"}`
			m := mintClient(t, f, now)

			tok, err := m.MintMerge(t.Context(), testRepo, 7, mergeHead(1))
			if !errors.Is(err, ErrMergeMint) {
				t.Fatalf("the refusal is %v, want ErrMergeMint: a daemon must tell a refusal from an outage", err)
			}
			if !strings.Contains(err.Error(), tc.code) {
				t.Errorf("the refusal %q does not carry the service's own code %q", err, tc.code)
			}
			if strings.Contains(err.Error(), "is in the service's own sentence") {
				t.Errorf("the refusal %q repeats the service's own sentence, which is written for an operator's log", err)
			}
			if strings.Contains(err.Error(), testToken) {
				t.Errorf("the refusal %q carries a credential value", err)
			}
			if tok != (MergeToken{}) {
				t.Errorf("a refused mint answered a token: %+v", tok)
			}
		})
	}
}

// TestMergeMintClientRefusesWhatItWillNotSend: every one of these calls is
// refused by the client and reaches no service at all — a daemon with no URL, no
// App ID or no ID token has no identity to ask with, and a repository, a pull
// request number or a head that is not one is a call the service would refuse
// anyway, refused here one round trip earlier.
//
// The URL cases are the ones a bearer credential makes necessary: the daemon's
// ID token travels in a header, so the client will not send it over plain http to
// anything but a loopback address.
func TestMergeMintClientRefusesWhatItWillNotSend(t *testing.T) {
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	f := newMintFake(t)
	f.answer = mintAnswer(testToken, testRepo, now.Add(time.Hour))
	base := mintClient(t, f, now)

	for _, tc := range []struct {
		name   string
		mutate func(*ServiceMergeMint)
		repo   string
		number int
		head   string
	}{
		{name: "no URL", mutate: func(m *ServiceMergeMint) { m.URL = "" }, repo: testRepo, number: 7, head: mergeHead(1)},
		{name: "no App ID", mutate: func(m *ServiceMergeMint) { m.AppID = "" }, repo: testRepo, number: 7, head: mergeHead(1)},
		{name: "no ID token", mutate: func(m *ServiceMergeMint) { m.IDToken = nil }, repo: testRepo, number: 7, head: mergeHead(1)},
		{name: "a URL that is not one", mutate: func(m *ServiceMergeMint) { m.URL = "://not-a-url" }, repo: testRepo, number: 7, head: mergeHead(1)},
		{name: "a URL with a path", mutate: func(m *ServiceMergeMint) { m.URL = "https://creds.example.invalid/v1" }, repo: testRepo, number: 7, head: mergeHead(1)},
		{name: "a URL with a user", mutate: func(m *ServiceMergeMint) { m.URL = "https://user@creds.example.invalid" }, repo: testRepo, number: 7, head: mergeHead(1)},
		{name: "plain http to another host", mutate: func(m *ServiceMergeMint) { m.URL = "http://creds.example.invalid" }, repo: testRepo, number: 7, head: mergeHead(1)},
		{name: "no repository", repo: "", number: 7, head: mergeHead(1)},
		{name: "no pull request number", repo: testRepo, number: 0, head: mergeHead(1)},
		{name: "a pull request number below one", repo: testRepo, number: -1, head: mergeHead(1)},
		{name: "no head", repo: testRepo, number: 7, head: ""},
		{name: "a head that is not a commit", repo: testRepo, number: 7, head: "the-head"},
		{name: "a head of another shape", repo: testRepo, number: 7, head: strings.Repeat("Z", 40)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := *base
			if tc.mutate != nil {
				tc.mutate(&m)
			}
			tok, err := m.MintMerge(t.Context(), tc.repo, tc.number, tc.head)
			if !errors.Is(err, ErrMergeMintIdentity) && !errors.Is(err, ErrMergeMint) {
				t.Fatalf("the call was refused with %v, want the client's own refusal", err)
			}
			if tok != (MergeToken{}) {
				t.Errorf("a refused call answered a token: %+v", tok)
			}
		})
	}
	if n := len(f.seen()); n != 0 {
		t.Errorf("the client sent %d calls it refuses to make, want none", n)
	}
}

// TestMergeMintClientRefusesAnAnswerItMustNotUse: the four answers below are
// ones this client will not hand on — an answer that is not JSON, one that
// carries no token, one minted for ANOTHER repository, and one whose life has
// already run out. Every one of them is ErrMergeMint and every one of them
// answers no token.
//
// The other-repository cell is the one that matters most: a token minted for a
// repository the caller did not name would merge the wrong repository, and the
// check is the reason the answer's field is the caller's own spelling.
func TestMergeMintClientRefusesAnAnswerItMustNotUse(t *testing.T) {
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		answer string
	}{
		{"an answer that is not JSON", `not json at all`},
		{"an answer with no token", mintAnswer("", testRepo, now.Add(time.Hour))},
		{"an answer for another repository", mintAnswer(testToken, "acme/elsewhere", now.Add(time.Hour))},
		{"an answer whose life has run out", mintAnswer(testToken, testRepo, now.Add(-time.Minute))},
		{"an answer whose life ends exactly now", mintAnswer(testToken, testRepo, now)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMintFake(t)
			f.answer = tc.answer
			m := mintClient(t, f, now)
			tok, err := m.MintMerge(t.Context(), testRepo, 7, mergeHead(1))
			if !errors.Is(err, ErrMergeMint) {
				t.Fatalf("the client used %s: %v", tc.name, err)
			}
			if tok != (MergeToken{}) {
				t.Errorf("the client handed on a token from %s: %+v", tc.name, tok)
			}
			if strings.Contains(err.Error(), testToken) {
				t.Errorf("the refusal %q carries the token", err)
			}
		})
	}
}

// TestMergeMintClientNeverFollowsARedirect: a redirect's destination is chosen
// by the answer rather than by the configuration, and the call carries the
// daemon's own ID token — so the client stops at the redirect and answers the
// refusal instead of presenting the credential to wherever it pointed.
func TestMergeMintClientNeverFollowsARedirect(t *testing.T) {
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	elsewhere := newMintFake(t)
	elsewhere.answer = mintAnswer(testToken, testRepo, now.Add(time.Hour))
	f := newMintFake(t)
	f.status, f.location = http.StatusFound, elsewhere.srv.URL+MergeMintPath

	m := mintClient(t, f, now)
	if _, err := m.MintMerge(t.Context(), testRepo, 7, mergeHead(1)); !errors.Is(err, ErrMergeMint) {
		t.Fatalf("the client answered %v for a redirect, want ErrMergeMint", err)
	}
	if n := len(elsewhere.seen()); n != 0 {
		t.Errorf("the client followed the redirect: the other server saw %d calls, want none", n)
	}
}

// TestMergeMintClientRefusesAnOversizedAnswer: the answer is bounded, because a
// service that answered with a stream would otherwise be read into this process's
// memory whole.
func TestMergeMintClientRefusesAnOversizedAnswer(t *testing.T) {
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	f := newMintFake(t)
	f.answer = mintAnswer(testToken, testRepo, now.Add(time.Hour)) + strings.Repeat(" ", maxMergeAnswerBytes)
	m := mintClient(t, f, now)
	if _, err := m.MintMerge(t.Context(), testRepo, 7, mergeHead(1)); !errors.Is(err, ErrMergeMint) {
		t.Fatalf("the client read an oversized answer as %v, want ErrMergeMint", err)
	}
}

// TestMergeMintClientSatisfiesTheInterfaceTheDaemonHolds: the daemon and, through
// E8, the provider depend on MergeMinter and not on the way the mint is reached.
func TestMergeMintClientSatisfiesTheInterfaceTheDaemonHolds(t *testing.T) {
	var _ MergeMinter = (*ServiceMergeMint)(nil)
	if MergeMintPath != "/v1/github/merge-token" {
		t.Errorf("the client's merge path = %q, want the path the service serves", MergeMintPath)
	}
}
