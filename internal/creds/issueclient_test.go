package creds

// The issue client's own tests (issueclient.go, B4 and B9). They are the
// client's half of the route's contract: what each act's call carries, what the
// client refuses before it makes one, what it reads out of the service's
// answers, and what no error of its may carry.
//
// The three acts are held here together and not one test file per plan task: a
// `list` and an `open` are B4's (D2718) and a `get` is B9's (D2720), and they
// are one client over one route, sharing one `call` — so a change to the
// identity it presents, the URL rule it holds or the refusals it maps is a
// change all three acts are held to.
//
// Everything here is hermetic (Convention 9): the service is the httptest server
// mergemint_test.go builds (serviceFake) on loopback, and no test opens a socket
// to anything else. No literal in this file is, or looks like, a credential
// (Convention 2): testIDToken and testToken are the package's own placeholders,
// and every assertion about them is an assertion that they are absent from what
// the client writes.
//
// The App id the issues below carry is the client's own testAppID, which is what
// makes an issue the App's own to the sync above this package (D2728): the id
// has to survive the client, the adapter and the wire, and a client that dropped
// it would leave the sync unable to tell this installation's App from any other.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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

// issueAnswer is a well-formed answer body: the issues a read read, or the one a
// creation created.
func issueAnswer(issues ...Issue) string {
	b, err := json.Marshal(IssueAnswer{Issues: issues})
	if err != nil {
		panic("creds test: building an answer: " + err.Error())
	}
	return string(b)
}

// TestIssueClientAsksForOneCallPerAct: every act's call is the service's
// contract and nothing more — the path, the daemon's own ID token as the whole
// of its identity, the repository and App ID the client was built with, and the
// fields that act takes.
//
// The bodies are compared as whole maps, which is what makes two things about
// them asserted rather than assumed: that `list` carries no text at all — the
// listing takes no title and no body, and a client that sent one would be
// asking the service to ignore it — and that `open` carries the item's own title
// and body and nothing else.
//
// The absence of the run identity's headers is asserted for the label client's
// reason: this route's caller holds no run identity, and a client that presented
// one would be answered by the service's run-identity refusal rather than by the
// class.
func TestIssueClientAsksForOneCallPerAct(t *testing.T) {
	f := newServiceFake(t)
	f.answer = issueAnswer(Issue{Number: 7, Title: "a title", Body: "a body", State: "open"})
	s := issueClient(t, f)

	for _, tc := range []struct {
		name string
		call func() error
		want map[string]any
	}{
		{
			name: "list",
			call: func() error { _, err := s.ListOpen(t.Context()); return err },
			want: map[string]any{"repository": testRepo, "action": IssueActionList, "app_id": testAppID},
		},
		{
			name: "open",
			call: func() error { _, err := s.Open(t.Context(), "a title", "a body"); return err },
			want: map[string]any{
				"repository": testRepo, "action": IssueActionOpen, "app_id": testAppID,
				"title": "a title", "body": "a body",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(f.seen())
			if err := tc.call(); err != nil {
				t.Fatalf("the %s call was refused: %v", tc.name, err)
			}
			calls := f.seen()[before:]
			if len(calls) != 1 {
				t.Fatalf("the %s call made %d calls, want one", tc.name, len(calls))
			}
			call := calls[0]
			if call.path != IssuePath {
				t.Errorf("the client called %s, want %s", call.path, IssuePath)
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
			if len(sent) != len(tc.want) {
				t.Fatalf("the %s call carried %d fields %v, want the %d this act takes", tc.name, len(sent), sent, len(tc.want))
			}
			for field, want := range tc.want {
				if got := sent[field]; got != want {
					t.Errorf("the %s call's %s = %v, want %v", tc.name, field, got, want)
				}
			}
		})
	}
}

// TestIssueClientReadsTheAnswers: the two acts answer what the service's shape
// says they do — a listing as the issues it carries, in the host's own order,
// and a creation as the one issue it created. The fields D2728 reads are asserted
// one by one: the number an external id is built from, the state, the author and
// the App signals, and the body the sync hashes and never stores (D2720).
func TestIssueClientReadsTheAnswers(t *testing.T) {
	f := newServiceFake(t)
	s := issueClient(t, f)

	f.answer = issueAnswer(
		Issue{Number: 5, Title: "the first", Body: "body one", URL: "https://github.com/acme/aurora/issues/5", State: "open",
			AuthorLogin: "fugaro-app[bot]", AuthorID: "900001", AppSlug: "fugaro-app", AppID: testAppID},
		Issue{Number: 3, Title: "the second", Body: "body two", State: "open", AuthorLogin: "someone"},
	)
	list, err := s.ListOpen(t.Context())
	if err != nil {
		t.Fatalf("the listing was refused: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("the listing answered %d issues, want two", len(list))
	}
	if list[0].Number != 5 || list[0].Title != "the first" || list[0].Body != "body one" ||
		list[0].State != "open" || list[0].AuthorLogin != "fugaro-app[bot]" || list[0].AppSlug != "fugaro-app" || list[0].AppID != testAppID {
		t.Errorf("the listing answered %+v, want the issue as the service wrote it", list[0])
	}
	if list[1].Number != 3 || list[1].AppSlug != "" || list[1].AppID != "" {
		t.Errorf("the listing answered %+v, want the person's issue with no App of its own", list[1])
	}

	// A repository with no open issues answers an empty listing and not a
	// refusal: nothing open is a fact, and the sync reads it as one (D2742).
	f.answer = issueAnswer()
	empty, err := s.ListOpen(t.Context())
	if err != nil {
		t.Fatalf("an empty listing was refused: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("an empty listing answered %d issues, want none", len(empty))
	}

	f.answer = issueAnswer(Issue{Number: 9, Title: "the mirror's", Body: "body", State: "open",
		AuthorLogin: "fugaro-app[bot]", AppSlug: "fugaro-app", AppID: testAppID})
	opened, err := s.Open(t.Context(), "the mirror's", "body")
	if err != nil {
		t.Fatalf("the creation was refused: %v", err)
	}
	if opened.Number != 9 || opened.AppID != testAppID {
		t.Errorf("the creation answered %+v, want the issue the service created", opened)
	}
}

// TestIssueClientRefusesPlainHTTP: the call presents the daemon's own ID token,
// which is a bearer credential, so the client will not send it over plain http
// to anything but a loopback address — and it refuses BEFORE it mints one, which
// is asserted rather than assumed: a token minted for a service this code then
// refuses to send it to is a credential this process did not need to hold.
func TestIssueClientRefusesPlainHTTP(t *testing.T) {
	for _, tc := range []struct{ name, url string }{
		{"another host", "http://creds.example.invalid"},
		{"an address that is not loopback", "http://10.11.12.13"},
		{"a host whose name only begins with localhost", "http://localhost.creds.example.invalid"},
		{"a loopback name with a path", "http://localhost/v1"},
		{"a URL with a user", "http://user@127.0.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newServiceFake(t)
			minted := 0
			s := issueClient(t, f)
			s.URL = tc.url
			s.IDToken = func(context.Context) (string, error) { minted++; return testIDToken, nil }

			_, err := s.ListOpen(t.Context())
			if !errors.Is(err, ErrIssue) {
				t.Fatalf("the client answered %v, want ErrIssue for a URL it will not send a credential to", err)
			}
			noSecret(t, err)
			if minted != 0 {
				t.Errorf("the client minted the daemon's identity %d times for a URL it refuses, want none", minted)
			}
			if n := len(f.seen()); n != 0 {
				t.Errorf("the client made %d calls, want none", n)
			}
			if strings.Contains(err.Error(), tc.url) {
				t.Errorf("the refusal %q carries the URL", err)
			}
		})
	}

	// https is admitted by the rule and the refusal is then the call's, not the
	// URL's: the port below is loopback's and closed, so nothing leaves this
	// machine and the answer is the one a service that is not there gives.
	f := newServiceFake(t)
	s := issueClient(t, f)
	s.URL = "https://127.0.0.1:1"
	_, err := s.ListOpen(t.Context())
	if !errors.Is(err, ErrIssue) || !strings.Contains(err.Error(), "could not be reached") {
		t.Fatalf("the client answered %v for an https service it cannot reach, want ErrIssue for the call", err)
	}
}

// TestIssueClientMapsRefusals: every refusal the service answers with reaches a
// caller as ErrIssue with the service's own error code beside it — a sentinel a
// daemon can tell a refusal from an outage by — and never the service's sentence,
// which is written for an operator's log, and never a value.
func TestIssueClientMapsRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		code   string
	}{
		{"the call refused", http.StatusBadRequest, "request_refused"},
		{"the class refused", http.StatusUnauthorized, "unverified_job_identity"},
		{"the caller forbidden", http.StatusForbidden, "forbidden"},
		{"the rate", http.StatusTooManyRequests, "rate_limited"},
		{"the service unavailable", http.StatusServiceUnavailable, "state_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newServiceFake(t)
			f.status = tc.status
			f.answer = `{"error":"` + tc.code + `","message":"` + testToken + ` is in the service's own sentence"}`
			s := issueClient(t, f)

			for _, call := range []struct {
				act string
				do  func() error
			}{
				{"list", func() error { _, err := s.ListOpen(t.Context()); return err }},
				{"open", func() error { _, err := s.Open(t.Context(), "a title", "a body"); return err }},
			} {
				err := call.do()
				if !errors.Is(err, ErrIssue) {
					t.Fatalf("the %s call answered %v, want ErrIssue: a daemon must tell a refusal from an outage", call.act, err)
				}
				if errors.Is(err, ErrIssueIdentity) {
					t.Errorf("the %s call answered a refusal the client makes itself: %v", call.act, err)
				}
				if !strings.Contains(err.Error(), tc.code) {
					t.Errorf("the %s refusal %q does not carry the service's own code %q", call.act, err, tc.code)
				}
				noSecret(t, err)
			}

			// Both acts answer nothing at all when they are refused: a partial
			// listing would be read as "the issues the repository still has" —
			// and archive every item of a repository whose listing was cut short
			// — and a creation read out of a refusal would key an item on a
			// number no issue holds.
			list, err := s.ListOpen(t.Context())
			if !errors.Is(err, ErrIssue) || len(list) != 0 {
				t.Errorf("a refused listing answered %d issues (%v), want none", len(list), err)
			}
			issue, err := s.Open(t.Context(), "a title", "a body")
			if !errors.Is(err, ErrIssue) || issue.Number != 0 {
				t.Errorf("a refused creation answered %+v (%v), want nothing", issue, err)
			}
		})
	}

	// A service that is not there at all is the same sentinel with no code: the
	// daemon reports the tick's ingest unread either way, and the difference
	// between the two is what it retries on.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := dead.URL
	dead.Close()
	s := &ServiceIssues{URL: url, Repository: testRepo, AppID: testAppID,
		IDToken: func(context.Context) (string, error) { return testIDToken, nil }}
	_, err := s.ListOpen(t.Context())
	if !errors.Is(err, ErrIssue) {
		t.Fatalf("a service that is not there answered %v, want ErrIssue", err)
	}
	noSecret(t, err)
}

// TestIssueClientRefusesWhatItWillNotSend: every call below is refused by the
// client and reaches no service at all — a daemon with no URL, repository or App
// ID, or with nothing to mint its identity with, has no identity to ask with, and
// a repository the route does not take is a call the service would refuse anyway,
// refused here one round trip earlier.
//
// The last three cells are the answers rather than the calls, and they are the
// ones the sync's own dedupe rests on (D2728): an issue with no number is one no
// external id can be built from, so a listing carrying one is refused whole
// rather than read as a listing with an issue the sync skips — a listing that was
// read as one would leave that repository's items looking closed — and a creation
// that answers no issue or several is not one creation's.
func TestIssueClientRefusesWhatItWillNotSend(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*ServiceIssues)
		answer  string
		call    func(*ServiceIssues) error
		wantErr error
	}{
		{name: "no URL", mutate: func(s *ServiceIssues) { s.URL = "" }, wantErr: ErrIssueIdentity,
			call: func(s *ServiceIssues) error { _, err := s.ListOpen(t.Context()); return err }},
		{name: "no repository", mutate: func(s *ServiceIssues) { s.Repository = "" }, wantErr: ErrIssueIdentity,
			call: func(s *ServiceIssues) error { _, err := s.ListOpen(t.Context()); return err }},
		{name: "no App ID", mutate: func(s *ServiceIssues) { s.AppID = "" }, wantErr: ErrIssueIdentity,
			call: func(s *ServiceIssues) error { _, err := s.ListOpen(t.Context()); return err }},
		{name: "no ID token", mutate: func(s *ServiceIssues) { s.IDToken = nil }, wantErr: ErrIssueIdentity,
			call: func(s *ServiceIssues) error { _, err := s.ListOpen(t.Context()); return err }},
		{name: "a repository that is not owner/name", mutate: func(s *ServiceIssues) { s.Repository = "aurora" }, wantErr: ErrIssueIdentity,
			call: func(s *ServiceIssues) error { _, err := s.ListOpen(t.Context()); return err }},
		{name: "a repository with two names", mutate: func(s *ServiceIssues) { s.Repository = "acme/aurora/extra" }, wantErr: ErrIssueIdentity,
			call: func(s *ServiceIssues) error { _, err := s.Open(t.Context(), "a title", "a body"); return err }},
		{name: "a listing naming no issue number", answer: issueAnswer(Issue{Title: "no number", State: "open"}), wantErr: ErrIssue,
			call: func(s *ServiceIssues) error { _, err := s.ListOpen(t.Context()); return err }},
		{name: "a creation answering no issue", answer: issueAnswer(), wantErr: ErrIssue,
			call: func(s *ServiceIssues) error { _, err := s.Open(t.Context(), "a title", "a body"); return err }},
		{name: "a creation answering two issues", answer: issueAnswer(Issue{Number: 1}, Issue{Number: 2}), wantErr: ErrIssue,
			call: func(s *ServiceIssues) error { _, err := s.Open(t.Context(), "a title", "a body"); return err }},
		{name: "a creation naming no issue number", answer: issueAnswer(Issue{Title: "no number"}), wantErr: ErrIssue,
			call: func(s *ServiceIssues) error { _, err := s.Open(t.Context(), "a title", "a body"); return err }},
		{name: "an answer that is not one", mutate: func(s *ServiceIssues) {}, answer: "{", wantErr: ErrIssue,
			call: func(s *ServiceIssues) error { _, err := s.ListOpen(t.Context()); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newServiceFake(t)
			f.answer = tc.answer
			s := issueClient(t, f)
			if tc.mutate != nil {
				tc.mutate(s)
			}
			err := tc.call(s)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("the call answered %v, want %v", err, tc.wantErr)
			}
			noSecret(t, err)
			// A call the client refuses itself reaches no service; one whose
			// ANSWER is refused reached it exactly once, which is what says the
			// refusal is the answer's and not the call's.
			want := 0
			if tc.answer != "" {
				want = 1
			}
			if n := len(f.seen()); n != want {
				t.Errorf("the client made %d calls, want %d", n, want)
			}
		})
	}
}

// The read's own tests (B9, D2720). They are the same contract held for the
// third act: what the read's call carries, what the client refuses before it
// makes one, what it reads out of the service's answer, and the one refusal a
// caller has to be able to tell from an outage.

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
	f.answer = issueAnswer(Issue{Number: 7, Title: "the ticket's title", Body: "the ticket's body", State: "open"})
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

// TestIssueClientRefusesWhatItCannotAsk: a read this client will not make is a
// refusal before anything is sent. Every cell is one of the identity's own
// refusals — a client that cannot present the daemon's identity, or a read that
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
	f.answer = issueAnswer(Issue{Number: 7, Title: "the ticket's title", Body: "the ticket's body", State: "open"})

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

// TestIssueClientTellsAGoneIssueFromAnOutage: a read that answered "this
// repository holds no such issue" is a fact about the item and a read that
// answered anything else is an outage, and the two are told apart here because
// the caller does different things with them — the launcher blocks the item over
// the first and reads again over the second (briefFor, B9, D2720).
//
// The 404 is the whole of the evidence, so a table that only checked the refusal
// it expects would pass for a client that read every status as gone, which is
// why each cell also asserts the refusal it must NOT read as.
func TestIssueClientTellsAGoneIssueFromAnOutage(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		want   error
		other  error
	}{
		{name: "the repository holds no such issue", status: http.StatusNotFound, want: ErrIssueGone, other: ErrIssue},
		{name: "the service is unavailable", status: http.StatusServiceUnavailable, want: ErrIssue, other: ErrIssueGone},
		{name: "the service refused the call", status: http.StatusForbidden, want: ErrIssue, other: ErrIssueGone},
		{name: "the service failed", status: http.StatusInternalServerError, want: ErrIssue, other: ErrIssueGone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newServiceFake(t)
			f.status = tc.status
			f.answer = `{"error":"not_found","message":"` + testToken + ` is in the service's own sentence"}`
			_, err := issueClient(t, f).Issue(t.Context(), 7)
			if !errors.Is(err, tc.want) {
				t.Fatalf("the client answered %v, want %v", err, tc.want)
			}
			if errors.Is(err, tc.other) {
				t.Errorf("the refusal %v reads as %v as well, and a caller cannot act on both: one is an item nothing can launch, the other an outage to read again", err, tc.other)
			}
			noSecret(t, err)
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
	elsewhere.answer = issueAnswer(Issue{Number: 7, Title: "the ticket's title", Body: "the ticket's body", State: "open"})
	f.status, f.location = http.StatusFound, elsewhere.srv.URL+IssuePath
	if _, err := issueClient(t, f).Issue(t.Context(), 7); !errors.Is(err, ErrIssue) {
		t.Fatalf("the client answered %v, want the redirect refused", err)
	}
	if n := len(elsewhere.seen()); n != 0 {
		t.Errorf("the client followed the redirect: the other server saw %d call(s), want none", n)
	}
}
