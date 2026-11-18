package creds

// The issue client's own tests (issueclient.go, B9). They are the client's half
// of the route's contract: what the read act's call carries, what the client
// refuses before it makes one, what it reads out of the service's answer, and
// what no error of its may carry.
//
// Everything here is hermetic (Convention 9): the service is the httptest server
// mergemint_test.go builds (serviceFake) on loopback, and no test opens a socket
// to anything else — the plain-http cases below are refused before a connection
// could be attempted, and the redirect case is answered by a loopback server
// whose body is never read. No literal in this file is, or looks like, a
// credential (Convention 2): the package's own two placeholders are the values
// the assertions look for.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// issueClient is the client under test, pointed at the fake: one repository, the
// test's own App ID, and the daemon's own ID token minted by a function the test
// can watch.
func issueClient(t *testing.T, f *serviceFake) *ServiceIssues {
	t.Helper()
	return &ServiceIssues{
		URL:        f.srv.URL,
		Repository: testRepo,
		AppID:      testAppID,
		IDToken:    func(context.Context) (string, error) { return testIDToken, nil },
	}
}

// issueAnswer is a well-formed answer body: the one issue a read read.
func issueAnswer(number int, title, body string) string {
	b, err := json.Marshal(IssueAnswer{Issues: []Issue{{Number: number, Title: title, Body: body, State: "open"}}})
	if err != nil {
		panic("creds test: building an answer: " + err.Error())
	}
	return string(b)
}

// TestIssueClientAsksForOneIssueRead: the read's call is the service's contract
// and nothing more — the path, the daemon's own ID token as the whole of its
// identity, and the four fields the act takes: the repository, the act, the App
// ID the service signs as, and the number.
//
// The body is compared as a whole map, which is what makes two things asserted
// rather than assumed: that the read carries no title and no body (the `open`
// act's fields, which the service decodes strictly and would refuse), and that
// it carries no run identity. The absence of the run identity's headers is
// asserted for the mint's reason: this route's caller holds none, and a client
// that presented one would be answered by the service's run-identity refusal
// rather than by the class.
func TestIssueClientAsksForOneIssueRead(t *testing.T) {
	f := newServiceFake(t)
	f.answer = issueAnswer(7, "the ticket's title", "the ticket's body")
	c := issueClient(t, f)

	iss, err := c.Issue(t.Context(), 7)
	if err != nil {
		t.Fatalf("the client refused a read the service admits: %v", err)
	}
	if iss.Number != 7 || iss.Title != "the ticket's title" || iss.Body != "the ticket's body" {
		t.Errorf("the client read %+v, want the issue the service answered", iss)
	}

	calls := f.seen()
	if len(calls) != 1 {
		t.Fatalf("the client made %d call(s), want the one read", len(calls))
	}
	call := calls[0]
	if call.path != IssuePath {
		t.Errorf("the call went to %s, want %s", call.path, IssuePath)
	}
	if call.auth != "Bearer "+testIDToken {
		t.Errorf("the call presented %q, want the daemon's own ID token and nothing else", call.auth)
	}
	if call.run != "" || call.job != "" {
		t.Errorf("the call presented a run identity (%q/%q), and this route's caller holds none", call.run, call.job)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(call.body), &body); err != nil {
		t.Fatalf("the call's body is not JSON: %v", err)
	}
	want := map[string]any{"repository": testRepo, "action": IssueActionGet, "app_id": testAppID, "number": float64(7)}
	if len(body) != len(want) {
		t.Errorf("the call carries %v, want exactly %v", body, want)
	}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("the call's %s is %v, want %v", k, body[k], v)
		}
	}
}

// TestIssueClientRefusesWhatItCannotAsk: a call this client will not make is a
// refusal before anything is sent. Every cell is one of the identity's own
// refusals — a client that cannot present the daemon's identity, or a call that
// names no repository or no issue — and the URL cells are the scheme rule the
// label client and the mint hold theirs to: over plain http the daemon's ID
// token, which is a bearer credential, would travel in cleartext.
//
// Each cell also holds the refusal to the point it is about, which the error
// alone does not: the daemon's token is minted by a function the cell counts,
// and a cell that keeps one must leave it uncalled. Deleting the URL check
// leaves every error here unchanged — the call then goes out to a name that
// does not resolve, and the caller still reads the route's own refusal — so a
// test of the error is a test that passes while the client has already minted
// the bearer credential and sent it in cleartext.
func TestIssueClientRefusesWhatItCannotAsk(t *testing.T) {
	f := newServiceFake(t)
	f.answer = issueAnswer(7, "the ticket's title", "the ticket's body")

	for _, tc := range []struct {
		name    string
		mutate  func(c *ServiceIssues)
		number  int
		wantErr error
	}{
		{name: "no URL", mutate: func(c *ServiceIssues) { c.URL = "" }, number: 7, wantErr: ErrIssueIdentity},
		{name: "no repository", mutate: func(c *ServiceIssues) { c.Repository = "" }, number: 7, wantErr: ErrIssueIdentity},
		{name: "a repository that is not owner/name", mutate: func(c *ServiceIssues) { c.Repository = "aurora" }, number: 7, wantErr: ErrIssueIdentity},
		{name: "no App ID", mutate: func(c *ServiceIssues) { c.AppID = "" }, number: 7, wantErr: ErrIssueIdentity},
		{name: "no ID token", mutate: func(c *ServiceIssues) { c.IDToken = nil }, number: 7, wantErr: ErrIssueIdentity},
		{name: "no issue number", mutate: func(*ServiceIssues) {}, number: 0, wantErr: ErrIssueIdentity},
		{name: "a URL that is not one", mutate: func(c *ServiceIssues) { c.URL = "://not-a-url" }, number: 7, wantErr: ErrIssue},
		{name: "a URL with a path", mutate: func(c *ServiceIssues) { c.URL = "https://creds.example.invalid/v1" }, number: 7, wantErr: ErrIssue},
		{name: "a URL with a user", mutate: func(c *ServiceIssues) { c.URL = "https://user@creds.example.invalid" }, number: 7, wantErr: ErrIssue},
		{name: "plain http to another host", mutate: func(c *ServiceIssues) { c.URL = "http://creds.example.invalid" }, number: 7, wantErr: ErrIssue},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := issueClient(t, f)
			minted := 0
			c.IDToken = func(context.Context) (string, error) {
				minted++
				return testIDToken, nil
			}
			tc.mutate(c)
			if _, err := c.Issue(t.Context(), tc.number); !errors.Is(err, tc.wantErr) {
				t.Fatalf("the client answered %v, want %v", err, tc.wantErr)
			} else {
				noSecret(t, err)
			}
			if calls := f.seen(); len(calls) != 0 {
				t.Errorf("a refused call sent %d call(s) to the service: %+v", len(calls), calls)
			}
			// The cell that carries no mint function of its own (the nil-token
			// refusal, which replaces this one) has nothing to count.
			if c.IDToken != nil && minted != 0 {
				t.Errorf("a refused call minted the daemon's ID token %d time(s), want none: the refusal has to come before the identity is presented", minted)
			}
		})
	}
}

// TestIssueClientRefusesAnAnswerThatIsNotOneIssues: a read is for one issue and
// the service answers the one it was asked for. An answer that is empty would be
// read by the launcher as text no ticket carries, and one that names another
// issue would brief a run with a ticket the item is not about — so both are
// refusals here and not answers a caller reads as its own.
func TestIssueClientRefusesAnAnswerThatIsNotOneIssues(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer string
	}{
		{name: "no issue at all", answer: `{"issues":[]}`},
		{name: "two issues", answer: `{"issues":[{"number":7},{"number":8}]}`},
		{name: "another issue", answer: `{"issues":[{"number":8,"title":"another ticket"}]}`},
		{name: "an answer that is not this route's", answer: `{"pull_requests":[]}`},
		{name: "no answer at all", answer: ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newServiceFake(t)
			f.answer = tc.answer
			if _, err := issueClient(t, f).Issue(t.Context(), 7); !errors.Is(err, ErrIssue) {
				t.Fatalf("the client answered %v, want the route's own refusal", err)
			}
		})
	}
}

// TestIssueClientRelaysNoSentenceAndFollowsNoRedirect: the two things a
// credential-bearing call must never do. The service's own answer is read for
// its status and its error code alone — the sentence beside it is written for an
// operator's log, and it may quote a token — and a redirect is not followed,
// because the destination would be chosen by the answer rather than by the
// installation's configuration.
func TestIssueClientRelaysNoSentenceAndFollowsNoRedirect(t *testing.T) {
	f := newServiceFake(t)
	f.status = 403
	f.answer = `{"error":"forbidden","message":"` + testToken + ` is in the service's own sentence"}`
	_, err := issueClient(t, f).Issue(t.Context(), 7)
	if !errors.Is(err, ErrIssue) {
		t.Fatalf("the client answered %v, want the route's own refusal", err)
	}
	noSecret(t, err)
	if !strings.Contains(err.Error(), "forbidden") {
		t.Errorf("the refusal %q does not carry the service's error code", err)
	}

	elsewhere := newServiceFake(t)
	elsewhere.answer = issueAnswer(7, "the ticket's title", "the ticket's body")
	f.status, f.location = http.StatusFound, elsewhere.srv.URL+IssuePath
	if _, err := issueClient(t, f).Issue(t.Context(), 7); !errors.Is(err, ErrIssue) {
		t.Fatalf("the client answered %v, want the redirect refused", err)
	}
	if n := len(elsewhere.seen()); n != 0 {
		t.Errorf("the client followed the redirect: the other server saw %d call(s), want none", n)
	}
}
