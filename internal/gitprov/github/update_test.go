package github

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/httpfixture"
)

// tokenPrefix is the two exchanges every fixture starts with.
const tokenPrefix = `
  {"method": "GET", "path": "/repos/acme/web/installation", "status": 200, "response": {"id": 777}},
  {"method": "POST", "path": "/app/installations/777/access_tokens", "status": 201, "response": {"token": "ghs_fixture_token_1", "expires_at": "2026-09-27T11:00:00Z"}}`

// openInline serves the exchanges (after the token prefix) written inline.
func openInline(t *testing.T, exchanges ...string) *Provider {
	t.Helper()
	file := filepath.Join(t.TempDir(), "fixture.json")
	body := "[" + tokenPrefix
	for _, e := range exchanges {
		body += ",\n  " + e
	}
	if err := os.WriteFile(file, []byte(body+"\n]"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := httpfixture.Serve(t, file)
	p, err := New(Options{Owner: "acme", Repo: "web", AppID: "1234", PrivateKey: key(t), BaseURL: srv.URL,
		Now: func() time.Time { return time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// getPR is the GET of pull request 12 with the given extra fields.
func getPR(state string, draft bool, title, body, extra string) string {
	b := "null"
	if body != "" {
		b = fmt.Sprintf("%q", body)
	}
	return fmt.Sprintf(`{"method": "GET", "path": "/repos/acme/web/pulls/12", "status": 200, "response": {"number": 12, "state": %q, "merged": false, "draft": %t, "html_url": "https://github.com/acme/web/pull/12", "node_id": "PR_kwDOA12", "title": %q, "body": %s, "user": {"login": "bot", "id": 9}, "head": {"ref": "fugaro/x", "sha": "abc", "repo": {"full_name": "acme/web"}}%s}}`,
		state, draft, title, b, extra)
}

func patch(req string) string {
	return `{"method": "PATCH", "path": "/repos/acme/web/pulls/12", "request": ` + req + `, "status": 200, "response": {"number": 12}}`
}

func ptr[T any](v T) *T { return &v }

func TestDraftCreateSendsNoReviewers(t *testing.T) {
	// The fixture ends after the create: a labels or reviewers request
	// would fail the test (and so would a missing create).
	p := openInline(t,
		`{"method": "GET", "path": "/repos/acme/web/pulls?head=acme:fugaro/20260927-101500-abcd&state=open&per_page=1", "status": 200, "response": []}`,
		`{"method": "POST", "path": "/repos/acme/web/pulls", "request": {"title": "Add search", "head": "fugaro/20260927-101500-abcd", "base": "main", "body": "Adds search.", "draft": true}, "status": 201, "response": {"number": 12, "html_url": "https://github.com/acme/web/pull/12", "draft": true, "node_id": "PR_kwDOA12", "title": "Add search"}}`)
	pr, err := p.EnsurePR(ctx, spec(true, []string{"fugaro"}, []string{"octocat", "acme/platform"}))
	if err != nil || pr != (gitprov.PR{Number: 12, URL: "https://github.com/acme/web/pull/12", Draft: true}) {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

func TestUpdatePR(t *testing.T) {
	const url = "https://github.com/acme/web/pull/12"
	tests := []struct {
		name      string
		exchanges []string
		update    gitprov.PRUpdate
		want      gitprov.PR
		wantErr   error  // matched with errors.Is
		errText   string // substring of a plain error
	}{
		{name: "body only sends only the body",
			exchanges: []string{getPR("open", false, "T", "old", ""), patch(`{"body": "new"}`)},
			update:    gitprov.PRUpdate{Body: ptr("new")}, want: gitprov.PR{Number: 12, URL: url}},
		{name: "title only sends only the title",
			exchanges: []string{getPR("open", true, "T", "old", ""), patch(`{"title": "T2"}`)},
			update:    gitprov.PRUpdate{Title: ptr("T2")}, want: gitprov.PR{Number: 12, URL: url, Draft: true}},
		{name: "title and body in one PATCH",
			exchanges: []string{getPR("open", false, "T", "old", ""), patch(`{"title": "T2", "body": "new"}`)},
			update:    gitprov.PRUpdate{Title: ptr("T2"), Body: ptr("new")}, want: gitprov.PR{Number: 12, URL: url}},
		{name: "nothing changes: no PATCH",
			exchanges: []string{getPR("open", false, "T", "same", "")},
			update:    gitprov.PRUpdate{Title: ptr("T"), Body: ptr("same")}, want: gitprov.PR{Number: 12, URL: url}},
		{name: "null body reads as empty: empty body is no change",
			exchanges: []string{getPR("open", false, "T", "", "")},
			update:    gitprov.PRUpdate{Body: ptr("")}, want: gitprov.PR{Number: 12, URL: url}},
		{name: "prefix-marked draft keeps its prefix through a retitle",
			exchanges: []string{getPR("open", false, "[DRAFT] T", "b", ""), patch(`{"title": "[DRAFT] T2"}`)},
			update:    gitprov.PRUpdate{Title: ptr("T2")}, want: gitprov.PR{Number: 12, URL: url, Draft: true, DraftFallback: true}},
		{name: "closed PR is not written",
			exchanges: []string{getPR("closed", false, "T", "b", "")},
			update:    gitprov.PRUpdate{Body: ptr("new")}, want: gitprov.PR{Number: 12, URL: url}, wantErr: gitprov.ErrPRNotOpen},
		{name: "draft flip to ready after a body change",
			exchanges: []string{getPR("open", true, "T", "old", ""), patch(`{"body": "new"}`),
				`{"method": "POST", "path": "/graphql", "request": {"query": "mutation($id: ID!) { markPullRequestReadyForReview(input: {pullRequestId: $id}) { pullRequest { isDraft } } }", "variables": {"id": "PR_kwDOA12"}}, "status": 200, "response": {"data": {}}}`},
			update: gitprov.PRUpdate{Body: ptr("new"), Draft: ptr(false)}, want: gitprov.PR{Number: 12, URL: url}},
		{name: "same draft state sends no flip",
			exchanges: []string{getPR("open", true, "T", "b", "")},
			update:    gitprov.PRUpdate{Draft: ptr(true)}, want: gitprov.PR{Number: 12, URL: url, Draft: true}},
		{name: "PATCH failure is a plain error",
			exchanges: []string{getPR("open", false, "T", "old", ""),
				`{"method": "PATCH", "path": "/repos/acme/web/pulls/12", "request": {"body": "new"}, "status": 502, "response": {"message": "bad gateway"}}`},
			update: gitprov.PRUpdate{Body: ptr("new")}, want: gitprov.PR{Number: 12, URL: url}, errText: "updating pull request #12"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := openInline(t, tt.exchanges...).UpdatePR(ctx, 12, tt.update)
			switch {
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
			case tt.errText != "":
				var partial *gitprov.PartialError
				if err == nil || !strings.Contains(err.Error(), tt.errText) || errors.As(err, &partial) {
					t.Fatalf("err = %v, want a plain error containing %q", err, tt.errText)
				}
			case err != nil:
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("pr = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestUpdatePRChangesBodyOnly pins A4's request shape: a body update is a
// PATCH whose body holds the body field and nothing else (the fixture
// matches the request exactly), so title, draft and reviewers are untouched.
func TestUpdatePRChangesBodyOnly(t *testing.T) {
	p := openInline(t, getPR("open", true, "Add search", "old", `, "requested_reviewers": [{"login": "octocat", "id": 1}]`), patch(`{"body": "new"}`))
	pr, err := p.UpdatePR(ctx, 12, gitprov.PRUpdate{Body: ptr("new")})
	if err != nil || !pr.Draft {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

func TestPullRequestReturnsBody(t *testing.T) {
	p := openInline(t, getPR("open", false, "Add search", "Line one.\n\nLine two.", ""), getPR("open", false, "Add search", "", ""))
	info, err := p.PullRequest(ctx, 12)
	if err != nil || info.Body != "Line one.\n\nLine two." || info.Title != "Add search" {
		t.Fatalf("info = %+v, %v", info, err)
	}
	if info, err = p.PullRequest(ctx, 12); err != nil || info.Body != "" {
		t.Fatalf("null body: info = %+v, %v", info, err)
	}
}

func TestApplyReady(t *testing.T) {
	labels := `{"method": "POST", "path": "/repos/acme/web/issues/12/labels", "request": {"labels": ["fugaro"]}, "status": 200, "response": []}`
	reviewers := `{"method": "POST", "path": "/repos/acme/web/pulls/12/requested_reviewers", "request": {"reviewers": ["octocat"], "team_reviewers": ["platform"]}, "status": 201, "response": {}}`
	have := `, "labels": [{"name": "Fugaro"}], "requested_reviewers": [{"login": "OctoCat", "id": 1}], "requested_teams": [{"slug": "platform"}]`
	want := []string{"octocat", "acme/platform"}
	tests := []struct {
		name      string
		exchanges []string
		reviewers []string
		labels    []string
		wantErr   error
		partial   string
	}{
		{name: "both", exchanges: []string{getPR("open", false, "T", "b", ""), labels, reviewers}, reviewers: want, labels: []string{"fugaro"}},
		{name: "already applied: no writes", exchanges: []string{getPR("open", false, "T", "b", have)}, reviewers: want, labels: []string{"fugaro"}},
		{name: "only the missing reviewer",
			exchanges: []string{getPR("open", false, "T", "b", `, "requested_reviewers": [{"login": "octocat", "id": 1}]`),
				`{"method": "POST", "path": "/repos/acme/web/pulls/12/requested_reviewers", "request": {"reviewers": [], "team_reviewers": ["platform"]}, "status": 201, "response": {}}`},
			reviewers: want},
		{name: "nothing wanted", exchanges: []string{getPR("open", false, "T", "b", "")}},
		{name: "closed", exchanges: []string{getPR("closed", false, "T", "b", "")}, reviewers: want, wantErr: gitprov.ErrPRNotOpen},
		{name: "unknown reviewer is partial, labels still applied",
			exchanges: []string{getPR("open", false, "T", "b", ""), labels,
				`{"method": "POST", "path": "/repos/acme/web/pulls/12/requested_reviewers", "request": {"reviewers": ["ghost"], "team_reviewers": []}, "status": 422, "response": {"message": "Reviews may only be requested from collaborators."}}`},
			reviewers: []string{"ghost"}, labels: []string{"fugaro"}, partial: "requesting reviewers"},
		{name: "foreign team is partial and sends nothing for reviewers",
			exchanges: []string{getPR("open", false, "T", "b", "")}, reviewers: []string{"other/team"}, partial: "outside acme"},
		{name: "label failure is partial",
			exchanges: []string{getPR("open", false, "T", "b", ""),
				`{"method": "POST", "path": "/repos/acme/web/issues/12/labels", "request": {"labels": ["fugaro"]}, "status": 403, "response": {"message": "Resource not accessible by integration"}}`},
			labels: []string{"fugaro"}, partial: "adding labels"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := openInline(t, tt.exchanges...).ApplyReady(ctx, 12, tt.reviewers, tt.labels)
			var partial *gitprov.PartialError
			switch {
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
			case tt.partial != "":
				if !errors.As(err, &partial) || !strings.Contains(err.Error(), tt.partial) {
					t.Fatalf("err = %v, want a PartialError containing %q", err, tt.partial)
				}
			case err != nil:
				t.Fatal(err)
			}
		})
	}
}

// TestApplyReadyIdempotent applies, then applies again against the PR as
// GitHub then shows it: the second call writes nothing.
func TestApplyReadyIdempotent(t *testing.T) {
	have := `, "labels": [{"name": "fugaro"}], "requested_reviewers": [{"login": "octocat", "id": 1}], "requested_teams": []`
	p := openInline(t,
		getPR("open", false, "T", "b", ""),
		`{"method": "POST", "path": "/repos/acme/web/issues/12/labels", "request": {"labels": ["fugaro"]}, "status": 200, "response": []}`,
		`{"method": "POST", "path": "/repos/acme/web/pulls/12/requested_reviewers", "request": {"reviewers": ["octocat"], "team_reviewers": []}, "status": 201, "response": {}}`,
		getPR("open", false, "T", "b", have))
	for i := 0; i < 2; i++ {
		if err := p.ApplyReady(ctx, 12, []string{"octocat"}, []string{"fugaro"}); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
}

// TestGitHubDraftFallbackReported: a host that refuses drafts (HTTP 422
// mentioning drafts) gets a normal PR with the title prefix, and the
// returned PR says so, so the runner can tell the user.
func TestGitHubDraftFallbackReported(t *testing.T) {
	pr, err := open(t, "draft_unsupported.json").EnsurePR(ctx, spec(true, []string{"fugaro"}, []string{"octocat"}))
	if err != nil || pr != (gitprov.PR{Number: 16, URL: "https://github.com/acme/web/pull/16", Draft: true, DraftFallback: true}) {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
	// A real draft is not a fallback.
	pr, err = open(t, "create_ready.json").EnsurePR(ctx, spec(false, []string{"fugaro"}, []string{"octocat", "acme/platform"}))
	if err != nil || pr.DraftFallback {
		t.Fatalf("ready create: pr = %+v, %v", pr, err)
	}
}

// TestGitHubDraft422WithoutDraftIsNotFallback keeps the 422 detection
// conservative: a 422 that does not mention drafts is an error, not a
// silent downgrade to a prefixed normal PR.
func TestGitHubDraft422WithoutDraftIsNotFallback(t *testing.T) {
	p := openInline(t,
		`{"method": "GET", "path": "/repos/acme/web/pulls?head=acme:fugaro/20260927-101500-abcd&state=open&per_page=1", "status": 200, "response": []}`,
		`{"method": "POST", "path": "/repos/acme/web/pulls", "status": 422, "response": {"message": "Validation Failed", "errors": [{"resource": "PullRequest", "code": "custom", "message": "No commits between main and fugaro/x"}]}}`)
	if pr, err := p.EnsurePR(ctx, spec(true, nil, nil)); err == nil {
		t.Fatalf("pr = %+v, want an error", pr)
	}
}
