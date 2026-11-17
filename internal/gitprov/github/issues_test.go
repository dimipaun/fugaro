package github

// The issue calls' tests (D2718, B11), against a server of the test's own
// rather than a fixture file: what is pinned here is the shape of one call --
// the endpoint, the query, the body and the installation token the call is
// made with -- and each handler answers exactly the requests it expects, so a
// call that invented an endpoint, asked for another state or carried another
// field fails rather than being replayed into a pass.
//
// The token every request must carry is the source installed on the provider
// (fixtureSource, C2.2): the routes that reach these calls mint their own
// token for the repository and hand the provider nothing else, which is what
// the Authorization check below is about.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

// issueProvider serves the issue calls of one test with h, as a provider
// whose installation token comes from a source of the test's own (C2.2), so
// no request the handler sees is an installation-token mint: every one of
// them is a call the test's own code made.
func issueProvider(t *testing.T, h http.HandlerFunc) *Provider {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	p, err := New(Options{Owner: "acme", Repo: "web", AppID: "1234", ServiceHeldKey: true, BaseURL: srv.URL,
		Now: func() time.Time { return time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	p.UseServiceTokens(fixtureSource{})
	return p
}

// serveJSON answers one request with status and body, after checking the
// credential it was made with: a call that reached GitHub without the
// service's own token would be the very thing the routes around these calls
// exist to keep from happening (D2718).
func serveJSON(t *testing.T, w http.ResponseWriter, r *http.Request, status int, body string) {
	t.Helper()
	if got, want := r.Header.Get("Authorization"), "Bearer "+serviceFixtureToken; got != want {
		t.Errorf("%s %s: Authorization = %q, want %q", r.Method, r.URL.Path, got, want)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := io.WriteString(w, body); err != nil {
		t.Errorf("writing the answer to %s %s: %v", r.Method, r.URL.Path, err)
	}
}

// checkCreateBody pins what one create request carries: the title and the
// body and nothing else, since a field this adapter invented -- a label, an
// assignee -- would be one the route never vouched for.
func checkCreateBody(t *testing.T, r *http.Request, title, body string) {
	t.Helper()
	var got map[string]any
	if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
		t.Fatalf("decoding the create's body: %v", err)
	}
	if got["title"] != title || got["body"] != body {
		t.Errorf("the create carried %v, want title %q and body %q", got, title, body)
	}
	if len(got) != 2 {
		t.Errorf("the create carried %d fields (%v), want title and body alone", len(got), got)
	}
}

// TestGitHubListIssuesExcludesPullRequests is D2720's exclusion: GitHub's
// issue list is the pull request list too, and a backlog item is never a pull
// request. The entry that is dropped is told apart by its `pull_request`
// field and by nothing else -- one of the issues kept carries the words "pull
// request" in its own title -- and the listing asks GitHub for the open ones.
//
// Mutation (keep every entry, or drop by title): the PR entry's number shows
// up in the answer, or the issue titled "Opening a pull request fails" does.
func TestGitHubListIssuesExcludesPullRequests(t *testing.T) {
	p := issueProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/repos/acme/web/issues" {
			t.Errorf("%s %s, want GET /repos/acme/web/issues", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("state"); got != "open" {
			t.Errorf("the listing asked for state=%q, want open", got)
		}
		serveJSON(t, w, r, 200, `[
			{"number": 12, "title": "Search returns nothing", "body": "Type a word and nothing happens.",
			 "state": "open", "html_url": "https://github.com/acme/web/issues/12",
			 "user": {"login": "octocat", "id": 1}},
			{"number": 11, "title": "Add search", "state": "open",
			 "pull_request": {"url": "https://api.github.com/repos/acme/web/pulls/11"}},
			{"number": 9, "title": "Opening a pull request fails", "body": null, "state": "open"}
		]`)
	})
	got, err := p.ListOpenIssues(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []Issue{
		{Number: 12, Title: "Search returns nothing", Body: "Type a word and nothing happens.",
			URL: "https://github.com/acme/web/issues/12", State: "open", AuthorLogin: "octocat", AuthorID: "1"},
		{Number: 9, Title: "Opening a pull request fails", State: "open"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListOpenIssues = %+v, want %+v", got, want)
	}
}

// TestGitHubListIssuesPaginates pins the walk over the listing's pages: the
// Link header's next page is followed and its issues are the answer's too, so
// a repository whose issues do not fit one page is read whole rather than
// quietly truncated at the first hundred.
//
// Mutation (read only the first page): the answer holds two issues instead of
// three, and the page-2 request never arrives.
func TestGitHubListIssuesPaginates(t *testing.T) {
	var asked []string
	p := issueProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("per_page"); got != "100" {
			t.Errorf("the listing asked for per_page=%q, want 100", got)
		}
		page := r.URL.Query().Get("page")
		asked = append(asked, page)
		switch page {
		case "":
			next := "http://" + r.Host + r.URL.Path + "?" + r.URL.RawQuery + "&page=2"
			w.Header().Set("Link", "<"+next+">; rel=\"next\"")
			serveJSON(t, w, r, 200, `[
				{"number": 12, "title": "Search returns nothing", "state": "open"},
				{"number": 11, "title": "Add search", "state": "open"}
			]`)
		case "2":
			serveJSON(t, w, r, 200, `[{"number": 7, "title": "Backlog: mirror items", "state": "open"}]`)
		default:
			t.Errorf("the listing asked for page %q, which this test serves nothing for", page)
			serveJSON(t, w, r, 200, `[]`)
		}
	})
	got, err := p.ListOpenIssues(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var numbers []int
	for _, i := range got {
		numbers = append(numbers, i.Number)
	}
	if want := []int{12, 11, 7}; !reflect.DeepEqual(numbers, want) {
		t.Fatalf("ListOpenIssues returned the issues %v, want %v", numbers, want)
	}
	if want := []string{"", "2"}; !reflect.DeepEqual(asked, want) {
		t.Errorf("the listing asked for the pages %q, want %q", asked, want)
	}
}

// TestGitHubReadIssue pins the single read: the issue's number, title, body,
// url and state, and the two signals D2728 reads the mirror marker on -- the
// account the issue was opened with, and the App the action was performed
// through (performed_via_github_app), which is empty when a person opened it.
func TestGitHubReadIssue(t *testing.T) {
	p := issueProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/repos/acme/web/issues/7" {
			t.Errorf("%s %s, want GET /repos/acme/web/issues/7", r.Method, r.URL.Path)
		}
		serveJSON(t, w, r, 200, `{"number": 7, "title": "Backlog: mirror items",
			"body": "Open an issue for every item that has none.", "state": "open",
			"html_url": "https://github.com/acme/web/issues/7",
			"user": {"login": "fugaro-app[bot]", "id": 900001},
			"performed_via_github_app": {"id": 42, "slug": "fugaro-app"}}`)
	})
	got, err := p.ReadIssue(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	want := Issue{Number: 7, Title: "Backlog: mirror items", Body: "Open an issue for every item that has none.",
		URL: "https://github.com/acme/web/issues/7", State: "open",
		AuthorLogin: "fugaro-app[bot]", AuthorID: "900001", AppSlug: "fugaro-app"}
	if got != want {
		t.Fatalf("ReadIssue = %+v, want %+v", got, want)
	}
}

// TestGitHubReadIssueRefusesAPullRequest: this endpoint reports pull requests
// too, and a caller given one would carry a review's title and body into a
// brief (D2720). The refusal names the number, so an operator reading it
// knows which id is wrong.
//
// Mutation (return the pull request's fields as an issue's): the read
// succeeds and this test fails on the missing error.
func TestGitHubReadIssueRefusesAPullRequest(t *testing.T) {
	p := issueProvider(t, func(w http.ResponseWriter, r *http.Request) {
		serveJSON(t, w, r, 200, `{"number": 11, "title": "Add search", "body": "Adds search.", "state": "open",
			"html_url": "https://github.com/acme/web/pull/11",
			"pull_request": {"url": "https://api.github.com/repos/acme/web/pulls/11"}}`)
	})
	_, err := p.ReadIssue(ctx, 11)
	if err == nil {
		t.Fatal("ReadIssue answered for a number GitHub reports as a pull request")
	}
	if want := "reading issue #11: GitHub reports it as a pull request"; err.Error() != want {
		t.Errorf("ReadIssue error = %q, want %q", err.Error(), want)
	}
}

// TestGitHubReadIssueRefusesAnotherIssuesAnswer: the number the answer names
// is what the caller acts on -- the item's external id, the brief built from
// the body -- so an answer for another issue is refused rather than that
// issue's text handed back under the number asked for. A transferred issue is
// how one arrives in practice: GitHub redirects, a following client lands
// somewhere else, and the body that comes back is not the one approved.
//
// Mutation (return raw.read() whatever its number): the read succeeds and this
// test fails on the missing error.
func TestGitHubReadIssueRefusesAnotherIssuesAnswer(t *testing.T) {
	p := issueProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/repos/acme/web/issues/7" {
			t.Errorf("%s %s, want GET /repos/acme/web/issues/7", r.Method, r.URL.Path)
		}
		serveJSON(t, w, r, 200, `{"number": 8, "title": "Opening a pull request fails",
			"body": "Another issue's text.", "state": "open"}`)
	})
	_, err := p.ReadIssue(ctx, 7)
	if err == nil {
		t.Fatal("ReadIssue answered with issue #8 for a request for #7")
	}
	if want := "reading issue #7: the response is for #8"; err.Error() != want {
		t.Errorf("ReadIssue error = %q, want %q", err.Error(), want)
	}
}

// TestGitHubReadIssueRefusesANonNumber: a number that is not one is refused
// before any request is made. GitHub has no issue #0, so asking spends a
// request of the route's per-repository budget on an answer no caller wants,
// and the refusal is the route's to report.
//
// Mutation (drop the number <= 0 guard): a request arrives for /issues/0 and
// this test fails on it.
func TestGitHubReadIssueRefusesANonNumber(t *testing.T) {
	p := issueProvider(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("%s %s: a number that is not an issue number was asked for", r.Method, r.URL.Path)
		serveJSON(t, w, r, 404, `{"message": "Not Found"}`)
	})
	for _, number := range []int{0, -1} {
		_, err := p.ReadIssue(ctx, number)
		if err == nil {
			t.Fatalf("ReadIssue(%d) answered a number that is not an issue number", number)
		}
		if want := fmt.Sprintf("reading issue #%d: not an issue number", number); err.Error() != want {
			t.Errorf("ReadIssue(%d) error = %q, want %q", number, err.Error(), want)
		}
	}
}

// TestGitHubOpenIssue pins the one write these calls make: the endpoint, the
// title and body alone, and the issue GitHub answered with.
func TestGitHubOpenIssue(t *testing.T) {
	const body = "The item has no issue of its own yet."
	p := issueProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/repos/acme/web/issues" {
			t.Errorf("%s %s, want POST /repos/acme/web/issues", r.Method, r.URL.Path)
		}
		checkCreateBody(t, r, "Mirror: Add search", body)
		serveJSON(t, w, r, 201, `{"number": 42, "title": "Mirror: Add search", "body": "`+body+`",
			"state": "open", "html_url": "https://github.com/acme/web/issues/42",
			"user": {"login": "fugaro-app[bot]", "id": 900001},
			"performed_via_github_app": {"id": 42, "slug": "fugaro-app"}}`)
	})
	got, err := p.OpenIssue(ctx, "Mirror: Add search", body)
	if err != nil {
		t.Fatal(err)
	}
	want := Issue{Number: 42, Title: "Mirror: Add search", Body: body,
		URL: "https://github.com/acme/web/issues/42", State: "open",
		AuthorLogin: "fugaro-app[bot]", AuthorID: "900001", AppSlug: "fugaro-app"}
	if got != want {
		t.Fatalf("OpenIssue = %+v, want %+v", got, want)
	}
}

// TestGitHubOpenIssueRefusedWhenTheAppCannotWrite: an installation whose App
// does not hold `issues: write` is refused at the write with 403 (U3), and
// that refusal is an error rather than a silent nothing the mirroring would
// read as an issue it opened.
func TestGitHubOpenIssueRefusedWhenTheAppCannotWrite(t *testing.T) {
	p := issueProvider(t, func(w http.ResponseWriter, r *http.Request) {
		serveJSON(t, w, r, 403, `{"message": "Resource not accessible by integration"}`)
	})
	_, err := p.OpenIssue(ctx, "Mirror: Add search", "")
	if err == nil {
		t.Fatal("OpenIssue reported success although GitHub refused the write with 403")
	}
	if want := "opening an issue"; !strings.Contains(err.Error(), want) {
		t.Errorf("OpenIssue error = %q, want it to carry %q", err.Error(), want)
	}
}

// TestGitHubOpenIssueRefusesAnAnswerWithNoNumber: the number of this answer
// is what the item's external id is written from (D2728), so an answer
// without one is refused rather than returned as issue #0 -- an id every
// later read would miss and the next tick would open a second issue for.
//
// Mutation (return the zero Issue): the external id becomes "…#0" and this
// test fails on the missing error.
func TestGitHubOpenIssueRefusesAnAnswerWithNoNumber(t *testing.T) {
	p := issueProvider(t, func(w http.ResponseWriter, r *http.Request) {
		serveJSON(t, w, r, 201, `{}`)
	})
	_, err := p.OpenIssue(ctx, "Mirror: Add search", "")
	if err == nil {
		t.Fatal("OpenIssue reported success although the answer named no issue")
	}
	if want := "the answer names no issue number"; !strings.Contains(err.Error(), want) {
		t.Errorf("OpenIssue error = %q, want it to carry %q", err.Error(), want)
	}
}
