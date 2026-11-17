package creds

// The label client's own tests (labelclient.go, T4). They are the client's half
// of the route's contract: what each act's call carries, what the client refuses
// before it makes one, what it reads out of the service's answers, and what no
// error of its may carry.
//
// Everything here is hermetic (Convention 9): the service is the httptest server
// mergemint_test.go builds (serviceFake) on loopback, and no test opens a socket
// to anything else — the plain-http cases below are refused before a connection
// could be attempted. No literal in this file is, or looks like, a credential
// (Convention 2): testIDToken and testToken are the package's own two
// placeholders, and every assertion about them is an assertion that they are
// absent from what the client writes.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// labelRun is a run id of the shape the route corroborates a write's pull
// request against — the form the daemon's own items carry, and the same one the
// syncer's tests use.
const labelRun = "20261116-120000-a1b2"

// labelClient is the client under test, pointed at the fake: one repository, the
// test's own App ID, and the daemon's own ID token minted by a function the test
// can watch.
func labelClient(t *testing.T, f *serviceFake) *ServiceLabeler {
	t.Helper()
	return &ServiceLabeler{
		URL:        f.srv.URL,
		Repository: testRepo,
		AppID:      testAppID,
		IDToken:    func(context.Context) (string, error) { return testIDToken, nil },
	}
}

// labelAnswer is a well-formed answer body: the pull requests a read read.
func labelAnswer(prs ...LabelPullRequest) string {
	b, err := json.Marshal(LabelAnswer{PullRequests: prs})
	if err != nil {
		panic("creds test: building an answer: " + err.Error())
	}
	return string(b)
}

// noSecret asserts that a refusal carries none of the values this package moves
// around: the daemon's ID token, the token a service answer would carry if this
// were the mint's route, and the service's own sentence (which is written for an
// operator's log and never relayed).
func noSecret(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("there is no refusal to read")
	}
	for _, value := range []string{testIDToken, testToken, "in the service's own sentence"} {
		if strings.Contains(err.Error(), value) {
			t.Errorf("the refusal %q carries %q", err, value)
		}
	}
}

// TestLabelClientAsksForOneCallPerAct: every act's call is the service's
// contract and nothing more — the path, the daemon's own ID token as the whole
// of its identity, the repository and App ID the client was built with, and the
// fields that act takes.
//
// The bodies are compared as whole maps, which is what makes two things about
// them asserted rather than assumed: that an act carries no field another act
// takes (the service decodes strictly and refuses one), and that `ensure`
// carries no colour and no description — the request's shape has nowhere to put
// them, so a caller cannot choose how a label looks (F26).
//
// The absence of the run identity's headers is asserted for the same reason it
// is asserted of the mint: this route's caller holds no run identity, and a
// client that presented one would be answered by the service's run-identity
// refusal rather than by the class.
func TestLabelClientAsksForOneCallPerAct(t *testing.T) {
	f := newServiceFake(t)
	f.answer = labelAnswer(LabelPullRequest{Number: 7, State: "open", Labels: []string{"fugaro:running"}})
	m := labelClient(t, f)

	for _, tc := range []struct {
		name string
		call func() error
		want map[string]any
	}{
		{
			name: "list",
			call: func() error { _, err := m.ListOpenPRLabels(t.Context()); return err },
			want: map[string]any{"repository": testRepo, "action": LabelActionList, "app_id": testAppID},
		},
		{
			name: "get",
			call: func() error { _, _, err := m.PRLabels(t.Context(), 7); return err },
			want: map[string]any{"repository": testRepo, "action": LabelActionGet, "app_id": testAppID, "number": float64(7)},
		},
		{
			name: "ensure",
			call: func() error {
				return m.EnsureLabel(t.Context(), labelRunningName, "1D76DB", "A Fugaro job is working on this pull request")
			},
			want: map[string]any{"repository": testRepo, "action": LabelActionEnsure, "app_id": testAppID, "name": labelRunningName},
		},
		{
			name: "add",
			call: func() error { return m.AddLabels(t.Context(), 7, labelRun, []string{"spec:forge-labels"}) },
			want: map[string]any{"repository": testRepo, "action": LabelActionAdd, "app_id": testAppID, "number": float64(7), "name": "spec:forge-labels", "run_id": labelRun},
		},
		{
			name: "remove",
			call: func() error { return m.RemoveLabels(t.Context(), 7, labelRun, []string{labelFailedName}) },
			want: map[string]any{"repository": testRepo, "action": LabelActionRemove, "app_id": testAppID, "number": float64(7), "name": labelFailedName, "run_id": labelRun},
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
			if call.path != LabelPath {
				t.Errorf("the client called %s, want %s", call.path, LabelPath)
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

	// A set is a call per name, in the order the caller gave them, each with
	// the run id: the route's request carries one label, so nothing else could
	// be one call.
	before := len(f.seen())
	if err := m.AddLabels(t.Context(), 7, labelRun, []string{labelRunningName, "spec:forge-labels"}); err != nil {
		t.Fatalf("a two-name add was refused: %v", err)
	}
	calls := f.seen()[before:]
	if len(calls) != 2 {
		t.Fatalf("a two-name add made %d calls, want two", len(calls))
	}
	for i, want := range []string{labelRunningName, "spec:forge-labels"} {
		var sent map[string]any
		if err := json.Unmarshal([]byte(calls[i].body), &sent); err != nil {
			t.Fatalf("the call's body is not JSON: %v (%q)", err, calls[i].body)
		}
		if sent["name"] != want || sent["run_id"] != labelRun || sent["number"] != float64(7) {
			t.Errorf("call %d carried %v, want the name %s, the run %s and the number 7", i, sent, want, labelRun)
		}
	}

	// A set with nothing in it asks the service for nothing: the syncer writes
	// differences, and an empty difference is a tick with nothing to write.
	before = len(f.seen())
	if err := m.AddLabels(t.Context(), 7, labelRun, nil); err != nil {
		t.Fatalf("an empty add was refused: %v", err)
	}
	if err := m.RemoveLabels(t.Context(), 7, labelRun, []string{}); err != nil {
		t.Fatalf("an empty removal was refused: %v", err)
	}
	if n := len(f.seen()) - before; n != 0 {
		t.Errorf("the client made %d calls for empty sets, want none", n)
	}
}

// TestLabelClientReadsTheAnswers: the two reads answer what the service's shape
// says they do — a listing as the map of open pull requests the caller keys a
// difference off, and one pull request's state and labels, merged and closed
// included, because clearing the status labels of one that closed is what the
// read is for (R5, R8).
func TestLabelClientReadsTheAnswers(t *testing.T) {
	f := newServiceFake(t)
	m := labelClient(t, f)

	f.answer = labelAnswer(
		LabelPullRequest{Number: 5, State: "open", Labels: []string{"fugaro:running", "spec:forge-labels"}},
		LabelPullRequest{Number: 3, State: "open"},
	)
	labels, err := m.ListOpenPRLabels(t.Context())
	if err != nil {
		t.Fatalf("the listing was refused: %v", err)
	}
	if len(labels) != 2 || len(labels[5]) != 2 || len(labels[3]) != 0 {
		t.Errorf("the listing answered %v, want two open pull requests, one of them with no labels", labels)
	}

	f.answer = labelAnswer(LabelPullRequest{Number: 7, State: "merged", Labels: []string{"spec:forge-labels"}})
	state, names, err := m.PRLabels(t.Context(), 7)
	if err != nil {
		t.Fatalf("the pull request read was refused: %v", err)
	}
	if state != "merged" || len(names) != 1 || names[0] != "spec:forge-labels" {
		t.Errorf("the read answered %q %v, want the merged pull request and its labels", state, names)
	}
}

// TestLabelClientRefusesPlainHTTP: the call presents the daemon's own ID token,
// which is a bearer credential, so the client will not send it over plain http
// to anything but a loopback address — and it refuses BEFORE it mints one, which
// is asserted here rather than assumed: a token minted for a service this code
// then refuses to send it to is a credential this process did not need to hold.
//
// The loopback cell is the other half of the rule and it is the one the other
// tests depend on: the fake IS an http service on 127.0.0.1, so every call above
// is a call the rule admits.
func TestLabelClientRefusesPlainHTTP(t *testing.T) {
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
			m := labelClient(t, f)
			m.URL = tc.url
			m.IDToken = func(context.Context) (string, error) { minted++; return testIDToken, nil }

			_, err := m.ListOpenPRLabels(t.Context())
			if !errors.Is(err, ErrLabel) {
				t.Fatalf("the client answered %v, want ErrLabel for a URL it will not send a credential to", err)
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
	m := labelClient(t, f)
	m.URL = "https://127.0.0.1:1"
	_, err := m.ListOpenPRLabels(t.Context())
	if !errors.Is(err, ErrLabel) || !strings.Contains(err.Error(), "could not be reached") {
		t.Fatalf("the client answered %v for an https service it cannot reach, want ErrLabel for the call", err)
	}
}

// TestLabelClientMapsRefusals: every refusal the service answers with reaches a
// caller as ErrLabel with the service's own error code beside it — a sentinel a
// daemon can tell a refusal from an outage by — and never the service's sentence,
// which is written for an operator's log, and never a value.
func TestLabelClientMapsRefusals(t *testing.T) {
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
			m := labelClient(t, f)

			for _, call := range []struct {
				act string
				do  func() error
			}{
				{"list", func() error { _, err := m.ListOpenPRLabels(t.Context()); return err }},
				{"get", func() error { _, _, err := m.PRLabels(t.Context(), 7); return err }},
				{"ensure", func() error {
					return m.EnsureLabel(t.Context(), labelRunningName, "1D76DB", "A Fugaro job is working on this pull request")
				}},
				{"add", func() error { return m.AddLabels(t.Context(), 7, labelRun, []string{labelRunningName}) }},
				{"remove", func() error { return m.RemoveLabels(t.Context(), 7, labelRun, []string{labelRunningName}) }},
			} {
				err := call.do()
				if !errors.Is(err, ErrLabel) {
					t.Fatalf("the %s call answered %v, want ErrLabel: a daemon must tell a refusal from an outage", call.act, err)
				}
				if errors.Is(err, ErrLabelIdentity) {
					t.Errorf("the %s call answered a refusal the client makes itself: %v", call.act, err)
				}
				if !strings.Contains(err.Error(), tc.code) {
					t.Errorf("the %s refusal %q does not carry the service's own code %q", call.act, err, tc.code)
				}
				noSecret(t, err)
			}

			// The two reads answer nothing at all when they are refused: a
			// partial listing or a state read out of a refusal would be a
			// difference the syncer computes from an answer it never got.
			labels, err := m.ListOpenPRLabels(t.Context())
			if !errors.Is(err, ErrLabel) || len(labels) != 0 {
				t.Errorf("a refused listing answered %d labels (%v), want none", len(labels), err)
			}
			state, names, err := m.PRLabels(t.Context(), 7)
			if !errors.Is(err, ErrLabel) || state != "" || len(names) != 0 {
				t.Errorf("a refused read answered %q and %d labels (%v), want nothing", state, len(names), err)
			}
		})
	}

	// A service that is not there at all is the same sentinel with no code: the
	// daemon reports the tick's labels unwritten either way, and the difference
	// between the two is what it retries on.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := dead.URL
	dead.Close()
	m := &ServiceLabeler{URL: url, Repository: testRepo, AppID: testAppID,
		IDToken: func(context.Context) (string, error) { return testIDToken, nil }}
	_, err := m.ListOpenPRLabels(t.Context())
	if !errors.Is(err, ErrLabel) {
		t.Fatalf("a service that is not there answered %v, want ErrLabel", err)
	}
	noSecret(t, err)
}

// TestLabelClientRefusesWhatItWillNotSend: every call below is refused by the
// client and reaches no service at all — a daemon with no URL, repository or App
// ID, or with nothing to mint its identity with, has no identity to ask with,
// and a repository, a pull request number, a run id or a label name the route
// does not take is a call the service would refuse anyway, refused here one
// round trip earlier.
//
// The label name cells are the route's whole fence on what a stolen daemon
// identity could ask for (F18, F26): the two status names and R3's spec shape,
// and nothing else — not a label of a person's, not a name outside both
// namespaces, not a name that only lowers into one of Fugaro's own, and for a
// removal not a spec label a person may have added.
func TestLabelClientRefusesWhatItWillNotSend(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*ServiceLabeler)
		call   func(*ServiceLabeler) error
	}{
		{name: "no URL", mutate: func(m *ServiceLabeler) { m.URL = "" },
			call: func(m *ServiceLabeler) error { _, err := m.ListOpenPRLabels(t.Context()); return err }},
		{name: "no repository", mutate: func(m *ServiceLabeler) { m.Repository = "" },
			call: func(m *ServiceLabeler) error { _, err := m.ListOpenPRLabels(t.Context()); return err }},
		{name: "no App ID", mutate: func(m *ServiceLabeler) { m.AppID = "" },
			call: func(m *ServiceLabeler) error { _, err := m.ListOpenPRLabels(t.Context()); return err }},
		{name: "no ID token", mutate: func(m *ServiceLabeler) { m.IDToken = nil },
			call: func(m *ServiceLabeler) error { _, err := m.ListOpenPRLabels(t.Context()); return err }},
		{name: "a repository that is not owner/name", mutate: func(m *ServiceLabeler) { m.Repository = "aurora" },
			call: func(m *ServiceLabeler) error { _, err := m.ListOpenPRLabels(t.Context()); return err }},
		{name: "a repository with two names", mutate: func(m *ServiceLabeler) { m.Repository = "acme/aurora/extra" },
			call: func(m *ServiceLabeler) error { _, err := m.ListOpenPRLabels(t.Context()); return err }},
		{name: "no pull request number", call: func(m *ServiceLabeler) error { _, _, err := m.PRLabels(t.Context(), 0); return err }},
		{name: "a pull request number below one", call: func(m *ServiceLabeler) error { _, _, err := m.PRLabels(t.Context(), -1); return err }},
		{name: "an add with no number", call: func(m *ServiceLabeler) error {
			return m.AddLabels(t.Context(), 0, labelRun, []string{labelRunningName})
		}},
		{name: "an add with no run id", call: func(m *ServiceLabeler) error { return m.AddLabels(t.Context(), 7, "", []string{labelRunningName}) }},
		{name: "an add with a run id that is not one", call: func(m *ServiceLabeler) error {
			return m.AddLabels(t.Context(), 7, "the-run", []string{labelRunningName})
		}},
		{name: "a removal with no run id", call: func(m *ServiceLabeler) error { return m.RemoveLabels(t.Context(), 7, "", []string{labelRunningName}) }},
		{name: "no label", call: func(m *ServiceLabeler) error {
			return m.EnsureLabel(t.Context(), "", "1D76DB", "A Fugaro job is working on this pull request")
		}},
		{name: "a label a person made", call: func(m *ServiceLabeler) error {
			return m.EnsureLabel(t.Context(), "needs-review", "ffffff", "a person's label")
		}},
		{name: "a status Fugaro has no label for", call: func(m *ServiceLabeler) error { return m.AddLabels(t.Context(), 7, labelRun, []string{"fugaro:queued"}) }},
		{name: "a bare namespace", call: func(m *ServiceLabeler) error { return m.AddLabels(t.Context(), 7, labelRun, []string{"fugaro:"}) }},
		{name: "a spec name that is not R3's shape", call: func(m *ServiceLabeler) error {
			return m.AddLabels(t.Context(), 7, labelRun, []string{"spec:../../etc/passwd"})
		}},
		{name: "a spec name longer than the host takes", call: func(m *ServiceLabeler) error {
			return m.AddLabels(t.Context(), 7, labelRun, []string{"spec:" + strings.Repeat("a", 45)})
		}},
		{name: "a spec name spelled with a character outside ASCII", call: func(m *ServiceLabeler) error {
			return m.AddLabels(t.Context(), 7, labelRun, []string{"spec:ſorge-labels"})
		}},
		// The two cells below are what holds the ASCII check to be FIRST: each
		// name is one whose Unicode fold lands inside what this route writes —
		// U+0130 onto "i" and U+212A onto "k" — so a check that folded the name
		// before it looked at the bytes would admit a label the host would never
		// match against Fugaro's own. The cell above does not pin that order: ſ
		// stays ſ under folding, so R3's shape refuses it either way.
		{name: "a status name that only folds into one of Fugaro's own", call: func(m *ServiceLabeler) error {
			return m.AddLabels(t.Context(), 7, labelRun, []string{"fugaro:runnİng"})
		}},
		{name: "a spec name that only folds into R3's shape", call: func(m *ServiceLabeler) error {
			return m.AddLabels(t.Context(), 7, labelRun, []string{"spec:Ka"})
		}},
		{name: "a removal of a spec label", call: func(m *ServiceLabeler) error {
			return m.RemoveLabels(t.Context(), 7, labelRun, []string{"spec:forge-labels"})
		}},
		{name: "a set whose second name the route does not write", call: func(m *ServiceLabeler) error {
			return m.AddLabels(t.Context(), 7, labelRun, []string{labelRunningName, "needs-review"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newServiceFake(t)
			minted := 0
			m := labelClient(t, f)
			m.IDToken = func(context.Context) (string, error) { minted++; return testIDToken, nil }
			if tc.mutate != nil {
				tc.mutate(m)
			}
			err := tc.call(m)
			if !errors.Is(err, ErrLabelIdentity) {
				t.Fatalf("the call was refused with %v, want the client's own refusal", err)
			}
			noSecret(t, err)
			if minted != 0 {
				t.Errorf("the client minted the daemon's identity %d times for a call it refuses, want none", minted)
			}
			if n := len(f.seen()); n != 0 {
				t.Errorf("the client sent %d calls it refuses to make, want none", n)
			}
		})
	}
}

// TestLabelClientRefusesAnAnswerItMustNotUse: the answers below are ones this
// client will not hand on — one that is not JSON, one that is larger than a
// label call's, a read that answers no pull request or several, one that answers
// another pull request than the caller asked about, and a listing that names no
// pull request number.
//
// The other-pull-request cell is the one that matters most: a state and a set of
// labels for a pull request the caller did not ask about would be read by the
// syncer as this one's, which is the wrong labels written on the wrong pull
// request.
func TestLabelClientRefusesAnAnswerItMustNotUse(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer string
		call   func(*ServiceLabeler) error
	}{
		{"an answer that is not JSON", `not json at all`,
			func(m *ServiceLabeler) error { _, err := m.ListOpenPRLabels(t.Context()); return err }},
		{"an answer larger than a label call's", labelAnswer(LabelPullRequest{Number: 5, State: "open"}) + strings.Repeat(" ", maxLabelAnswerBytes),
			func(m *ServiceLabeler) error { _, err := m.ListOpenPRLabels(t.Context()); return err }},
		{"a read that answers no pull request", labelAnswer(),
			func(m *ServiceLabeler) error { _, _, err := m.PRLabels(t.Context(), 7); return err }},
		{"a read that answers two", labelAnswer(LabelPullRequest{Number: 7, State: "open"}, LabelPullRequest{Number: 5, State: "open"}),
			func(m *ServiceLabeler) error { _, _, err := m.PRLabels(t.Context(), 7); return err }},
		{"a read that answers another pull request", labelAnswer(LabelPullRequest{Number: 5, State: "open"}),
			func(m *ServiceLabeler) error { _, _, err := m.PRLabels(t.Context(), 7); return err }},
		{"a listing that names no pull request number", labelAnswer(LabelPullRequest{Number: 0, State: "open"}),
			func(m *ServiceLabeler) error { _, err := m.ListOpenPRLabels(t.Context()); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newServiceFake(t)
			f.answer = tc.answer
			m := labelClient(t, f)
			if err := tc.call(m); !errors.Is(err, ErrLabel) {
				t.Fatalf("the client used %s: %v", tc.name, err)
			} else {
				noSecret(t, err)
			}
		})
	}
}

// TestLabelClientNeverFollowsARedirect: a redirect's destination is chosen by
// the answer rather than by the configuration, and the call carries the daemon's
// own ID token — so the client stops at the redirect and answers the refusal
// instead of presenting the credential to wherever it pointed.
func TestLabelClientNeverFollowsARedirect(t *testing.T) {
	elsewhere := newServiceFake(t)
	elsewhere.answer = labelAnswer()
	f := newServiceFake(t)
	f.status, f.location = http.StatusFound, elsewhere.srv.URL+LabelPath

	m := labelClient(t, f)
	if _, err := m.ListOpenPRLabels(t.Context()); !errors.Is(err, ErrLabel) {
		t.Fatalf("the client answered %v for a redirect, want ErrLabel", err)
	}
	if n := len(elsewhere.seen()); n != 0 {
		t.Errorf("the client followed the redirect: the other server saw %d calls, want none", n)
	}
}

// TestLabelClientAllowsBitbucketGetWithoutAppID: a Bitbucket client is built
// with no App ID, and the `get` act is the one it asks for — which is what lets
// an installation with Bitbucket alone reach the read at all (D2736, G5).
//
// Three facts are one design here and are asserted together: the client needs no
// App ID, because that host has no GitHub App and the service mints its read
// token from the installation's Bitbucket read consumer instead; it refuses an
// App ID it is given, because the service refuses a Bitbucket call that carries
// one and one round trip earlier is where the daemon can still see why; and the
// four acts around the read are refused by the client itself, because that host
// has no pull request labels, so a caller that asked for one would be spending a
// request on a refusal. Supported() is false on such a client for the same
// reason, which is what keeps the syncer's turn a no-op.
func TestLabelClientAllowsBitbucketGetWithoutAppID(t *testing.T) {
	f := newServiceFake(t)
	f.answer = labelAnswer(LabelPullRequest{Number: 7, State: "merged"})
	m := labelClient(t, f)
	m.AppID, m.Bitbucket = "", true

	if m.Supported() {
		t.Error("a Bitbucket client reports a host with pull request labels, and that host has none")
	}

	// The read: one call, no App ID, and the state it answered with — no labels,
	// because there are none to read.
	before := len(f.seen())
	state, labels, err := m.PRLabels(t.Context(), 7)
	if err != nil {
		t.Fatalf("a Bitbucket get with no App ID was refused: %v", err)
	}
	if state != "merged" {
		t.Errorf("the read answered the state %q, want merged", state)
	}
	if len(labels) != 0 {
		t.Errorf("the read answered the labels %v, and that host has none", labels)
	}
	calls := f.seen()[before:]
	if len(calls) != 1 {
		t.Fatalf("the read made %d calls, want one", len(calls))
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(calls[0].body), &sent); err != nil {
		t.Fatalf("the call's body is not JSON: %v (%q)", err, calls[0].body)
	}
	want := map[string]any{"repository": testRepo, "action": LabelActionGet, "app_id": "", "number": float64(7)}
	if len(sent) != len(want) {
		t.Fatalf("the call carried %d fields %v, want the %d this act takes", len(sent), sent, len(want))
	}
	for field, value := range want {
		if sent[field] != value {
			t.Errorf("the call's %s = %v, want %v", field, sent[field], value)
		}
	}

	// The four acts a Bitbucket repository cannot answer, refused here with the
	// service reached not at all.
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{name: "list", call: func() error { _, err := m.ListOpenPRLabels(t.Context()); return err }},
		{name: "ensure", call: func() error { return m.EnsureLabel(t.Context(), labelRunningName, "1D76DB", "text") }},
		{name: "add", call: func() error { return m.AddLabels(t.Context(), 7, labelRun, []string{labelRunningName}) }},
		{name: "remove", call: func() error { return m.RemoveLabels(t.Context(), 7, labelRun, []string{labelFailedName}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(f.seen())
			err := tc.call()
			if !errors.Is(err, ErrLabelIdentity) {
				t.Fatalf("the %s act on a Bitbucket repository was refused with %v, want this client's own refusal", tc.name, err)
			}
			noSecret(t, err)
			if n := len(f.seen()) - before; n != 0 {
				t.Errorf("the client sent %d calls for an act the host cannot answer, want none", n)
			}
		})
	}

	// An App ID the client is given is refused, get included: a Bitbucket call
	// names none, and a field quietly dropped would have the daemon believing it
	// named an App whose key signed the read.
	m.AppID = testAppID
	minted := 0
	m.IDToken = func(context.Context) (string, error) { minted++; return testIDToken, nil }
	if _, _, err := m.PRLabels(t.Context(), 7); !errors.Is(err, ErrLabelIdentity) {
		t.Errorf("a Bitbucket get carrying an App ID was refused with %v, want this client's own refusal", err)
	}
	if minted != 0 {
		t.Errorf("the client minted the daemon's identity %d times for a call it refuses, want none", minted)
	}
}

// TestLabelClientSatisfiesTheInterfaceTheDaemonHolds: the daemon and, through
// T7, the provider depend on Labeler and not on the way a label is reached — and
// the path and the acts it speaks are the ones the service serves, named here so
// that a rename on one side is a failure on this one.
func TestLabelClientSatisfiesTheInterfaceTheDaemonHolds(t *testing.T) {
	var _ Labeler = (*ServiceLabeler)(nil)
	if LabelPath != "/v1/github/labels" {
		t.Errorf("the client's label path = %q, want the path the service serves", LabelPath)
	}
	for _, tc := range []struct{ act, want string }{
		{LabelActionList, "list"},
		{LabelActionGet, "get"},
		{LabelActionEnsure, "ensure"},
		{LabelActionAdd, "add"},
		{LabelActionRemove, "remove"},
	} {
		if tc.act != tc.want {
			t.Errorf("the client's act %q = %q, want the act the service reads", tc.want, tc.act)
		}
	}
	if labelRunningName != "fugaro:running" || labelFailedName != "fugaro:failed" {
		t.Errorf("the client's status names = %q and %q, want the two the route writes", labelRunningName, labelFailedName)
	}
}

// TestLabelClientDoesNotCacheTheDaemonsIdentity: the ID token is minted per
// call and never held, which is the merge mint's rule for the same credential:
// its life is the platform's business, and a cached one would be a second place
// a credential lives.
func TestLabelClientDoesNotCacheTheDaemonsIdentity(t *testing.T) {
	f := newServiceFake(t)
	f.answer = labelAnswer()
	minted := 0
	m := labelClient(t, f)
	m.IDToken = func(context.Context) (string, error) {
		minted++
		return fmt.Sprintf("%s-%d", testIDToken, minted), nil
	}
	for i := 0; i < 3; i++ {
		if _, err := m.ListOpenPRLabels(t.Context()); err != nil {
			t.Fatalf("call %d was refused: %v", i, err)
		}
	}
	if minted != 3 {
		t.Errorf("three calls minted the daemon's identity %d times, want one each", minted)
	}
	for _, call := range f.seen() {
		if !strings.HasPrefix(call.auth, "Bearer "+testIDToken) {
			t.Errorf("a call presented %q, want the token minted for that call", call.auth)
		}
	}
}
