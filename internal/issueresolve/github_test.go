package issueresolve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// recordedIssue is a GitHub issue response as the issues endpoint really
// answers it, recorded 2026-11-08 and left whole on purpose: the many
// fields this reader has no use for (labels, user, reactions, timeline_url
// ...) are exactly what a strict decode would refuse, so every test below
// proves the reader ignores them rather than only working on a four-field
// stand-in.
const recordedIssue = `{
  "url": "https://api.github.com/repos/dimipaun/fugaro/issues/42",
  "repository_url": "https://api.github.com/repos/dimipaun/fugaro",
  "labels_url": "https://api.github.com/repos/dimipaun/fugaro/issues/42/labels{/name}",
  "comments_url": "https://api.github.com/repos/dimipaun/fugaro/issues/42/comments",
  "events_url": "https://api.github.com/repos/dimipaun/fugaro/issues/42/events",
  "html_url": "https://github.com/dimipaun/fugaro/issues/42",
  "id": 3210987654,
  "node_id": "I_kwDOJ1234567",
  "number": 42,
  "title": "the run stops before it pushes",
  "user": {
    "login": "octocat",
    "id": 1,
    "node_id": "MDQ6VXNlcjE=",
    "avatar_url": "https://avatars.githubusercontent.com/u/1?v=4",
    "html_url": "https://github.com/octocat",
    "type": "User",
    "site_admin": false
  },
  "labels": [
    {"id": 208045946, "node_id": "MDU6TGFiZWwyMDgwNDU5NDY=", "url": "https://api.github.com/repos/dimipaun/fugaro/labels/bug", "name": "bug", "color": "d73a4a", "default": true, "description": "Something isn't working"}
  ],
  "state": "open",
  "state_reason": null,
  "locked": false,
  "assignee": null,
  "assignees": [],
  "milestone": null,
  "comments": 2,
  "created_at": "2026-10-02T09:12:44Z",
  "updated_at": "2026-10-03T07:31:02Z",
  "closed_at": null,
  "author_association": "CONTRIBUTOR",
  "active_lock_reason": null,
  "body": "It happens every time on this machine.",
  "closed_by": null,
  "reactions": {"url": "https://api.github.com/repos/dimipaun/fugaro/issues/42/reactions", "total_count": 0, "+1": 0, "-1": 0, "laugh": 0, "hooray": 0, "confused": 0, "heart": 0, "rocket": 0, "eyes": 0},
  "timeline_url": "https://api.github.com/repos/dimipaun/fugaro/issues/42/timeline",
  "performed_via_github_app": null
}`

// milestone is a GitHub milestone object, as nested in an issue response.
func milestone(title, state string) map[string]any {
	return map[string]any{
		"url":           "https://api.github.com/repos/dimipaun/fugaro/milestones/3",
		"html_url":      "https://github.com/dimipaun/fugaro/milestone/3",
		"labels_url":    "https://api.github.com/repos/dimipaun/fugaro/milestones/3/labels",
		"id":            1002604,
		"node_id":       "MDk6TWlsZXN0b25lMTAwMjYwNA==",
		"number":        3,
		"title":         title,
		"description":   "the release that ships it",
		"creator":       map[string]any{"login": "dimipaun", "id": 2, "type": "User"},
		"open_issues":   0,
		"closed_issues": 7,
		"state":         state,
		"created_at":    "2026-09-01T00:00:00Z",
		"updated_at":    "2026-10-05T00:00:00Z",
		"due_on":        nil,
		"closed_at":     "2026-10-05T00:00:00Z",
	}
}

// issueBody is the recorded issue with fields applied over it: a nil value
// removes the field, so a case can say "no milestone" the way the real
// response says it.
func issueBody(t *testing.T, fields map[string]any) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(recordedIssue), &m); err != nil {
		t.Fatalf("the recorded fixture does not decode: %v", err)
	}
	for k, v := range fields {
		if v == nil {
			delete(m, k)
			continue
		}
		m[k] = v
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// issueServer answers every request with body, and records the paths it was
// asked for.
func issueServer(t *testing.T, status int, body string) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &paths
}

// fetch is Fetch against a fake GitHub, with the reader under test and a
// client that follows no redirect (the one production uses).
func fetch(t *testing.T, srv *httptest.Server, number int) (State, error) {
	t.Helper()
	r := NewReader(nil, srv.URL)
	return r.Fetch(context.Background(), number)
}

func wantState(t *testing.T, got State, err error, want State) {
	t.Helper()
	if err != nil {
		t.Fatalf("Fetch = %v, %v, want %#v, nil", got, err, want)
	}
	if got != want {
		t.Errorf("Fetch = %#v, want %#v", got, want)
	}
}

// TestFetchDecodesARealIssue pins that a whole recorded issue object, with
// every field this reader has no use for, still decodes: the answer is the
// open state and nothing else.
//
// Mutation: decoding into a strict decoder (DisallowUnknownFields) fails
// here on "url" — every real GitHub answer would be refused.
func TestFetchDecodesARealIssue(t *testing.T) {
	srv, _ := issueServer(t, http.StatusOK, issueBody(t, nil))
	got, err := fetch(t, srv, 42)
	wantState(t, got, err, Open{})
}

// TestFetchClosedCompletedCarriesMilestone pins the one answer that fixes
// an issue: closed as completed, with the milestone title as the release,
// canonicalized (a "v" prefix dropped) so it compares with the running
// version.
func TestFetchClosedCompletedCarriesMilestone(t *testing.T) {
	for _, title := range []string{"0.22.0", "v0.22.0"} {
		srv, _ := issueServer(t, http.StatusOK, issueBody(t, map[string]any{
			"state":        "closed",
			"state_reason": "completed",
			"closed_at":    "2026-10-05T10:00:00Z",
			"milestone":    milestone(title, "closed"),
		}))
		got, err := fetch(t, srv, 42)
		wantState(t, got, err, ClosedCompleted{Milestone: "0.22.0"})
	}
}

// TestFetchNotPlannedAndDuplicate pins the two quiet closes: they carry no
// milestone and no release, and are reported as their own states.
func TestFetchNotPlannedAndDuplicate(t *testing.T) {
	for _, c := range []struct {
		reason string
		want   State
	}{
		{"not_planned", ClosedNotPlanned{}},
		{"duplicate", ClosedDuplicate{}},
	} {
		srv, _ := issueServer(t, http.StatusOK, issueBody(t, map[string]any{
			"state":        "closed",
			"state_reason": c.reason,
			"closed_at":    "2026-10-05T10:00:00Z",
		}))
		got, err := fetch(t, srv, 42)
		wantState(t, got, err, c.want)
	}
}

// TestFetchNoVerdictForOddMilestone pins that a completed close without a
// usable release - no milestone, an empty one, a branch name, a partial
// version - is no verdict at all, never a guess: the resolver must leave
// such an issue submitted.
func TestFetchNoVerdictForOddMilestone(t *testing.T) {
	cases := []map[string]any{
		{"milestone": nil},
		{"milestone": milestone("", "closed")},
		{"milestone": milestone("main", "closed")},
		{"milestone": milestone("0.22", "closed")},
		{"milestone": milestone("0.22.0-rc1", "closed")},
	}
	for _, c := range cases {
		fields := map[string]any{"state": "closed", "state_reason": "completed"}
		for k, v := range c {
			fields[k] = v
		}
		srv, _ := issueServer(t, http.StatusOK, issueBody(t, fields))
		got, err := fetch(t, srv, 42)
		wantState(t, got, err, NoVerdict{})
	}
}

// TestFetchNoVerdictForUnknownShape pins the whitelist: an unknown state, a
// closed issue with no reason (or a reason this reader does not know) is no
// verdict, never an error and never a state it decided itself.
func TestFetchNoVerdictForUnknownShape(t *testing.T) {
	for _, fields := range []map[string]any{
		{"state": "open", "state_reason": "completed"},
		{"state": "closed", "state_reason": nil},
		{"state": "closed", "state_reason": "reopened"},
		{"state": "moved", "state_reason": "completed"},
	} {
		srv, _ := issueServer(t, http.StatusOK, issueBody(t, fields))
		got, err := fetch(t, srv, 42)
		wantState(t, got, err, NoVerdict{})
	}
}

// TestFetchPullRequestIsNoVerdict pins that a pull request, which the same
// endpoint serves and which is routinely closed as completed against a
// milestone, is never mistaken for an issue.
func TestFetchPullRequestIsNoVerdict(t *testing.T) {
	srv, _ := issueServer(t, http.StatusOK, issueBody(t, map[string]any{
		"state":        "closed",
		"state_reason": "completed",
		"milestone":    milestone("0.22.0", "closed"),
		"pull_request": map[string]any{
			"url":       "https://api.github.com/repos/dimipaun/fugaro/pulls/42",
			"html_url":  "https://github.com/dimipaun/fugaro/pull/42",
			"diff_url":  "https://github.com/dimipaun/fugaro/pull/42.diff",
			"patch_url": "https://github.com/dimipaun/fugaro/pull/42.patch",
			"merged_at": "2026-10-05T10:00:00Z",
		},
	}))
	got, err := fetch(t, srv, 42)
	wantState(t, got, err, NoVerdict{})
}

// TestFetchRefusesRedirect pins that a redirect is refused twice over: the
// reader's own client never follows one (so the second host is never
// asked), and an answer that came from anywhere but the requested URL is
// refused even when a caller injected a client that does follow.
//
// Mutation: dropping the URL check makes the injected client's answer
// (which is a perfectly good "closed completed") come back as
// ClosedCompleted; dropping CheckRedirect from the default client makes the
// first half of this test hang on the second server's body instead of
// failing.
func TestFetchRefusesRedirect(t *testing.T) {
	var reached int
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached++
		_, _ = w.Write([]byte(issueBody(t, map[string]any{
			"state":        "closed",
			"state_reason": "completed",
			"milestone":    milestone("0.22.0", "closed"),
		})))
	}))
	t.Cleanup(elsewhere.Close)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(redirector.Close)

	got, err := fetch(t, redirector, 42)
	if !errors.Is(err, ErrTransport) {
		t.Fatalf("Fetch = %v, %v, want ErrTransport", got, err)
	}
	if reached != 0 {
		t.Errorf("the redirect target was asked %d times: a redirect must not be followed", reached)
	}

	// A client that does follow: the answer must still be refused, because
	// it did not come from the URL that was requested.
	following := &http.Client{}
	r := NewReader(following, redirector.URL)
	got, err = r.Fetch(context.Background(), 42)
	if !errors.Is(err, ErrTransport) {
		t.Fatalf("Fetch with a following client = %v, %v, want ErrTransport", got, err)
	}
}

// TestFetchBoundsTheBody pins the size bound: a response past it is refused
// without being turned into a verdict, and a large but real-sized response
// (a long discussion on an issue) still decodes.
//
// Mutation: reading the body with io.ReadAll alone (no bound) accepts the
// oversize response and this test's first case fails.
func TestFetchBoundsTheBody(t *testing.T) {
	oversize := issueBody(t, map[string]any{"body": strings.Repeat("x", maxBody)})
	srv, _ := issueServer(t, http.StatusOK, oversize)
	got, err := fetch(t, srv, 42)
	if !errors.Is(err, ErrTransport) {
		t.Fatalf("Fetch = %v, %v, want ErrTransport for a body past the %d byte bound", got, err, maxBody)
	}

	large := issueBody(t, map[string]any{"body": strings.Repeat("x", maxBody/2)})
	srv, _ = issueServer(t, http.StatusOK, large)
	got, err = fetch(t, srv, 42)
	wantState(t, got, err, Open{})
}

// TestFetchRateLimited pins the two exhaustion answers, which the resolver
// must tell apart from an unreachable GitHub: it stops the pass either
// way, but only a rate limit is worth reporting as one.
func TestFetchRateLimited(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusTooManyRequests} {
		srv, _ := issueServer(t, status, `{"message":"API rate limit exceeded for 203.0.113.7."}`)
		got, err := fetch(t, srv, 42)
		if !errors.Is(err, ErrRateLimited) {
			t.Errorf("Fetch(status %d) = %v, %v, want ErrRateLimited", status, got, err)
		}
		if errors.Is(err, ErrTransport) {
			t.Errorf("Fetch(status %d) also matches ErrTransport: the two must stay distinguishable", status)
		}
	}
}

// TestFetchTransportError pins that a network failure, a non-200 answer and
// an undecodable body are all the same typed error, never a state and never
// an untyped error a caller would have to inspect.
func TestFetchTransportError(t *testing.T) {
	r := NewReader(errClient{}, "https://api.github.com")
	got, err := r.Fetch(context.Background(), 42)
	if !errors.Is(err, ErrTransport) {
		t.Errorf("Fetch with a failing client = %v, %v, want ErrTransport", got, err)
	}

	for _, c := range []struct {
		status int
		body   string
	}{
		{http.StatusNotFound, `{"message":"Not Found"}`},
		{http.StatusInternalServerError, `{"message":"Server Error"}`},
		{http.StatusOK, `not json at all`},
		{http.StatusOK, ``},
	} {
		srv, _ := issueServer(t, c.status, c.body)
		got, err := fetch(t, srv, 42)
		if !errors.Is(err, ErrTransport) {
			t.Errorf("Fetch(status %d, body %q) = %v, %v, want ErrTransport", c.status, c.body, got, err)
		}
	}
}

// errClient is a client whose every call fails, standing for a machine with
// no route to GitHub.
type errClient struct{}

func (errClient) Do(*http.Request) (*http.Response, error) {
	return nil, errors.New("dial tcp: network is unreachable")
}

// TestFetchUsesTheRepoConstantAndTheInteger pins the URL: it is built from
// the repository constant and the validated issue number, never from
// anything stored, it carries nothing else (no query, no path segment the
// number could smuggle), and a number that is not a positive integer is
// refused before any request is made.
func TestFetchUsesTheRepoConstantAndTheInteger(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.RequestURI())
		if r.Header.Get("Accept") == "" {
			t.Errorf("the request carries no Accept header")
		}
		_, _ = w.Write([]byte(issueBody(t, nil)))
	}))
	t.Cleanup(srv.Close)

	if _, err := fetch(t, srv, 42); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	want := "GET /repos/dimipaun/fugaro/issues/42"
	if len(seen) != 1 || seen[0] != want {
		t.Fatalf("requests = %q, want [%q]", seen, want)
	}

	for _, number := range []int{0, -1, -42} {
		before := len(seen)
		got, err := fetch(t, srv, number)
		if !errors.Is(err, ErrTransport) {
			t.Errorf("Fetch(%d) = %v, %v, want ErrTransport", number, got, err)
		}
		if len(seen) != before {
			t.Errorf("Fetch(%d) made a request: a number that is not positive must be refused first", number)
		}
	}
}
