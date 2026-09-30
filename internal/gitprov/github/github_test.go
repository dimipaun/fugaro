package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/httpfixture"
)

const branchName = "fugaro/20260927-101500-abcd"

var ctx = context.Background()

func open(t *testing.T, fixture string) *Provider {
	t.Helper()
	return openWarn(t, fixture, nil)
}

// openWarn is open with the adapter's warnings sent to warn.
func openWarn(t *testing.T, fixture string, warn func(string)) *Provider {
	t.Helper()
	srv := httpfixture.Serve(t, filepath.Join("testdata", fixture))
	p, err := New(Options{Owner: "acme", Repo: "web", AppID: "1234", PrivateKey: key(t), BaseURL: srv.URL, Warn: warn,
		Now: func() time.Time { return time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func spec(draft bool, labels, reviewers []string) gitprov.PRSpec {
	return gitprov.PRSpec{Branch: branchName, Base: "main", Title: "Add search", Body: "Adds search.",
		Draft: draft, Labels: labels, Reviewers: reviewers}
}

func TestCreateWithLabelsAndReviewers(t *testing.T) {
	p := open(t, "create_ready.json")
	pr, err := p.EnsurePR(ctx, spec(false, []string{"fugaro"}, []string{"octocat", "acme/platform"}))
	if err != nil || pr != (gitprov.PR{Number: 12, URL: "https://github.com/acme/web/pull/12"}) {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

// TestDraftUnsupportedFallsBackToTitle covers private repositories whose
// plan has no draft pull requests: the run still gets its PR, marked as a
// draft in the title (design §15).
func TestDraftUnsupportedFallsBackToTitle(t *testing.T) {
	p := open(t, "draft_unsupported.json")
	if pr, err := p.EnsurePR(ctx, spec(true, nil, nil)); err != nil || pr.Number != 16 || !pr.Draft {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

// TestAgentOpenedPRIsOnlyToggled covers a pull request the agent opened
// itself with gh: it is found by branch and converted to a draft, with no
// second pull request and no change to its title, body or labels.
func TestAgentOpenedPRIsOnlyToggled(t *testing.T) {
	p := open(t, "existing_to_draft.json")
	if pr, err := p.EnsurePR(ctx, spec(true, []string{"fugaro"}, []string{"octocat"})); err != nil || pr.Number != 13 || !pr.Draft {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

func TestExistingPRDraftUnsupportedRetitles(t *testing.T) {
	p := open(t, "existing_draft_unsupported.json")
	if pr, err := p.EnsurePR(ctx, spec(true, nil, nil)); err != nil || pr.Number != 17 || !pr.Draft {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

// TestExistingPrefixedDraftUnsupportedSkipsPATCH covers a pull request
// already carrying the "[DRAFT] " fallback prefix (from an earlier run on a
// repository without draft support) when a draft is wanted again: the
// title is already right, so no retitle PATCH is sent. The fixture ends at
// the GraphQL call, so httpfixture would fail the test on a PATCH.
func TestExistingPrefixedDraftUnsupportedSkipsPATCH(t *testing.T) {
	p := open(t, "existing_prefixed_draft_unsupported.json")
	pr, err := p.EnsurePR(ctx, spec(true, nil, nil))
	if err != nil || pr != (gitprov.PR{Number: 18, URL: "https://github.com/acme/web/pull/18", Draft: true}) {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

func TestExistingDraftMarkedReady(t *testing.T) {
	p := open(t, "existing_to_ready.json")
	if pr, err := p.EnsurePR(ctx, spec(false, nil, nil)); err != nil || pr.Number != 15 || pr.Draft {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

func TestReadyRemovesDraftPrefix(t *testing.T) {
	p := open(t, "existing_prefix_to_ready.json")
	if pr, err := p.EnsurePR(ctx, spec(false, nil, nil)); err != nil || pr.Number != 16 || pr.Draft {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

// TestMarkReadyFailureIsPartial covers the direction rule (gitprov.go,
// PartialError): a failed markPullRequestReadyForReview leaves the pull
// request looking like a draft, the conservative direction, so it is a
// *gitprov.PartialError with the populated (still-draft) PR rather than an
// empty one.
func TestMarkReadyFailureIsPartial(t *testing.T) {
	p := open(t, "mark_ready_fails.json")
	pr, err := p.EnsurePR(ctx, spec(false, nil, nil))
	var partial *gitprov.PartialError
	if !errors.As(err, &partial) || pr.Number != 15 || !pr.Draft || !strings.Contains(err.Error(), "not accessible") {
		t.Fatalf("pr = %+v, err = %v", pr, err)
	}
}

// TestRetitleFailureStrippingPrefixIsPartial covers "ready wanted, not a
// draft, prefixed title": the shared retitle step only ever strips the
// "[DRAFT] " prefix, so a failure there leaves the title looking more
// draft-like than requested — the conservative direction — and must be a
// *gitprov.PartialError reporting the draft-looking state, not a plain
// error or an empty PR.
func TestRetitleFailureStrippingPrefixIsPartial(t *testing.T) {
	p := open(t, "existing_prefix_retitle_fails.json")
	pr, err := p.EnsurePR(ctx, spec(false, nil, nil))
	var partial *gitprov.PartialError
	if !errors.As(err, &partial) || pr.Number != 16 || !pr.Draft || !strings.Contains(err.Error(), "not accessible") {
		t.Fatalf("pr = %+v, err = %v", pr, err)
	}
}

// TestDraftConversion5xxIsPlainError covers a GraphQL failure that is not
// GitHub saying drafts are unsupported (here, a 500): the title fallback
// must not be attempted (the fixture holds no PATCH exchange), and the
// existing, unmodified PR is returned alongside a plain, retryable error.
func TestDraftConversion5xxIsPlainError(t *testing.T) {
	p := open(t, "draft_conversion_5xx.json")
	pr, err := p.EnsurePR(ctx, spec(true, nil, nil))
	var partial *gitprov.PartialError
	if err == nil || errors.As(err, &partial) || pr.Number != 20 || pr.Draft {
		t.Fatalf("pr = %+v, err = %v", pr, err)
	}
}

// TestReviewerTeamMustBeOwnersTeam covers the "org/team" reviewer form: a
// team outside the repository's own owner cannot be requested, so it must
// be rejected clearly (as a PartialError alongside the created PR) rather
// than silently asking a same-named team in the wrong organization.
func TestReviewerTeamMustBeOwnersTeam(t *testing.T) {
	p := open(t, "reviewer_wrong_org.json")
	pr, err := p.EnsurePR(ctx, spec(false, nil, []string{"otherorg/platform"}))
	var partial *gitprov.PartialError
	if !errors.As(err, &partial) || pr.Number != 21 || !strings.Contains(err.Error(), "otherorg/platform") {
		t.Fatalf("pr = %+v, err = %v", pr, err)
	}
}

// TestDraftAndRetitleFailIsPlainError covers the unsafe direction: neither
// the GraphQL conversion to a real draft nor the title-prefix fallback
// succeeds, so the pull request is left looking fully ready though a draft
// was requested. That must not be a *gitprov.PartialError (which the runner
// treats as done): it is a plain, retryable error, with the existing,
// still-unmodified PR populated for the caller.
func TestDraftAndRetitleFailIsPlainError(t *testing.T) {
	p := open(t, "draft_and_retitle_fail.json")
	pr, err := p.EnsurePR(ctx, spec(true, nil, nil))
	var partial *gitprov.PartialError
	if err == nil || errors.As(err, &partial) || pr.Number != 19 || pr.Draft {
		t.Fatalf("pr = %+v, err = %v", pr, err)
	}
}

func TestLabelFailureIsPartial(t *testing.T) {
	p := open(t, "labels_forbidden.json")
	pr, err := p.EnsurePR(ctx, spec(false, []string{"fugaro"}, nil))
	var partial *gitprov.PartialError
	if !errors.As(err, &partial) || pr.Number != 18 || !strings.Contains(err.Error(), "403") {
		t.Fatalf("pr = %+v, err = %v", pr, err)
	}
}

func TestComment(t *testing.T) {
	p := open(t, "comment.json")
	if err := p.Comment(ctx, gitprov.PR{Number: 12}, "### Fugaro run report"); err != nil {
		t.Fatal(err)
	}
}

// TestGitAuthSetsGHToken uses a fixed clock (see open), so the second
// GitAuth's minValid must sit strictly between the two fixture tokens' full
// lifetimes (60m for the first, 80m for the second): long enough that the
// cached first token can no longer satisfy it, forcing a remint, but short
// enough that the freshly minted second token can (round-1 hardening,
// apptoken.go: a token minted with less life left than minValid is an
// error, so 90m — beyond even the second token's 80m — would not do).
func TestGitAuthSetsGHToken(t *testing.T) {
	p := open(t, "token_refresh.json")
	a, err := p.GitAuth(ctx, 45*time.Minute)
	if err != nil || a.Username != "x-access-token" || a.Token != "ghs_fixture_token_1" || a.Env["GH_TOKEN"] != a.Token || a.Expires.IsZero() {
		t.Fatalf("auth = %+v, %v", a, err)
	}
	if a, err := p.GitAuth(ctx, 75*time.Minute); err != nil || a.Token != "ghs_fixture_token_2" {
		t.Fatalf("second auth = %+v, %v", a, err)
	}
}

func TestGraphQLURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://api.github.com":         "https://api.github.com/graphql",
		"https://ghe.example.com/api/v3": "https://ghe.example.com/api/graphql",
		"http://127.0.0.1:5555":          "http://127.0.0.1:5555/graphql",
	} {
		if got := graphqlURL(in); got != want {
			t.Errorf("graphqlURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNewValidates(t *testing.T) {
	for _, o := range []Options{{Repo: "web", AppID: "1", PrivateKey: key(t)}, {Owner: "acme", Repo: "web", PrivateKey: key(t)}, {Owner: "acme", Repo: "web", AppID: "1"}} {
		if _, err := New(o); err == nil {
			t.Errorf("New(%+v) succeeded", o)
		}
	}
}

func TestGitHubRepositoryVisibility(t *testing.T) {
	for fixture, private := range map[string]bool{"repo_private.json": true, "repo_public.json": false} {
		t.Run(fixture, func(t *testing.T) {
			info, err := open(t, fixture).Repository(ctx)
			if err != nil || info.Private != private {
				t.Fatalf("info = %+v, %v", info, err)
			}
		})
	}
}

func TestGitHubRepositoryVisibilityMissing(t *testing.T) {
	p := fakeAPI(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/repos/acme/web" {
			w.Write([]byte(`{"full_name":"acme/web"}`))
			return true
		}
		return false
	})
	if info, err := p.Repository(ctx); err == nil {
		t.Fatalf("a repository without a private field read as %+v", info)
	}
}

const headSHA = "0123456789abcdef0123456789abcdef01234567"

func TestGitHubPullRequestOpen(t *testing.T) {
	got, err := open(t, "pull_open.json").PullRequest(ctx, 12)
	want := gitprov.PRInfo{Number: 12, URL: "https://github.com/acme/web/pull/12", State: gitprov.PROpen, Draft: true,
		AuthorID: "900001", SourceBranch: branchName, SourceRepo: "acme/web", HeadSHA: headSHA}
	if err != nil || got != want {
		t.Fatalf("got  %+v, %v\nwant %+v", got, err, want)
	}
}

func TestGitHubPullRequestMerged(t *testing.T) {
	got, err := open(t, "pull_merged.json").PullRequest(ctx, 12)
	if err != nil || got.State != gitprov.PRMerged || got.Draft {
		t.Fatalf("got %+v, %v", got, err)
	}
}

// TestGitHubPullRequestFork covers a head repository GitHub reports as
// null (a deleted fork): the source repository is unknown, so it is
// empty, and can never match the task's repository.
func TestGitHubPullRequestFork(t *testing.T) {
	got, err := open(t, "pull_fork.json").PullRequest(ctx, 12)
	if err != nil || got.State != gitprov.PROpen || got.SourceRepo != "" || got.SourceBranch != branchName || got.HeadSHA != headSHA {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestGitHubComments(t *testing.T) {
	got, err := open(t, "comments.json").Comments(ctx, 12)
	if err != nil {
		t.Fatal(err)
	}
	disc := func(id string) string { return "https://github.com/acme/web/pull/12#discussion_" + id }
	want := []gitprov.Comment{
		{ID: "IC_kwDOA602", Kind: gitprov.CommentGeneral, Author: "dave", AuthorID: "1004", SelfKnown: true,
			Body: "Drive-by: use a map.", CreatedAt: at("2026-09-19T10:00:00Z"), URL: "https://github.com/acme/web/pull/12#issuecomment-602"},
		{ID: "PRRC_1", Kind: gitprov.CommentInline, Author: "alice", AuthorID: "1001", Collaborator: true, SelfKnown: true, Resolved: true,
			Path: "src/a.go", Line: 10, Body: "Rename this.", CreatedAt: at("2026-09-20T10:00:00Z"), URL: disc("PRRC_1")},
		{ID: "PRRC_4", Kind: gitprov.CommentInline, Author: "alice", AuthorID: "1001", Collaborator: true, SelfKnown: true, Outdated: true,
			Path: "src/c.go", Body: "Handle the error here.", CreatedAt: at("2026-09-21T09:00:00Z"), URL: disc("PRRC_4")},
		{ID: "PRRC_2", Kind: gitprov.CommentInline, Author: "alice", AuthorID: "1001", Collaborator: true, SelfKnown: true,
			Path: "src/b.go", Line: 20, Body: "This loop is off by one.", CreatedAt: at("2026-09-21T10:00:00Z"), URL: disc("PRRC_2")},
		{ID: "PRRC_3", Kind: gitprov.CommentInline, Author: "bob", AuthorID: "1002", Collaborator: true, SelfKnown: true,
			Path: "src/b.go", Line: 20, Body: "Agreed, and add a test.", CreatedAt: at("2026-09-21T11:00:00Z"), URL: disc("PRRC_3")},
		{ID: "PRR_kwDOA501", Kind: gitprov.CommentReview, Author: "alice", AuthorID: "1001", Collaborator: true, SelfKnown: true,
			Body: "Close; see the inline comments.", CreatedAt: at("2026-09-21T12:00:00Z"), URL: "https://github.com/acme/web/pull/12#pullrequestreview-501"},
		{ID: "PRRC_5", Kind: gitprov.CommentInline, Author: "bob", AuthorID: "1002", Collaborator: true, SelfKnown: true, Truncated: true,
			Path: "src/d.go", Line: 7, Body: "First of many.", CreatedAt: at("2026-09-22T08:00:00Z"), URL: disc("PRRC_5")},
		{ID: "IC_kwDOA601", Kind: gitprov.CommentGeneral, Author: "alice", AuthorID: "1001", Collaborator: true, SelfKnown: true,
			Body: "Please also update the docs.", CreatedAt: at("2026-09-22T09:00:00Z"), URL: "https://github.com/acme/web/pull/12#issuecomment-601"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d comments, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if !got[i].CreatedAt.Equal(want[i].CreatedAt) {
			t.Errorf("comment %d: created %v, want %v", i, got[i].CreatedAt, want[i].CreatedAt)
		}
		got[i].CreatedAt, want[i].CreatedAt = time.Time{}, time.Time{}
		if got[i] != want[i] {
			t.Errorf("comment %d:\n got  %+v\n want %+v", i, got[i], want[i])
		}
	}
}

func TestGitHubCommentsPaged(t *testing.T) {
	got, err := open(t, "comments_paged.json").Comments(ctx, 12)
	if err != nil {
		t.Fatal(err)
	}
	var bodies []string
	for _, c := range got {
		bodies = append(bodies, c.Body)
	}
	if strings.Join(bodies, " ") != "one two three four five six" {
		t.Fatalf("bodies = %q", bodies)
	}
}

// TestGitHubCommentsForeignLink covers a Link to the next page on another
// host: the listing fails rather than send the installation token there.
func TestGitHubCommentsForeignLink(t *testing.T) {
	if got, err := open(t, "comments_foreign_link.json").Comments(ctx, 12); err == nil || !strings.Contains(err.Error(), "paging left the API host") {
		t.Fatalf("got %+v, %v", got, err)
	}
}

// fakeAPI serves the App-installation exchanges and GET /app itself, and
// hands every other request to h, failing the test when h doesn't handle it.
func fakeAPI(t *testing.T, h func(w http.ResponseWriter, r *http.Request) bool) *Provider {
	t.Helper()
	return fakeAPIWarn(t, h, nil)
}

// fakeAPIWarn is fakeAPI with the adapter's warnings sent to warn. h sees
// every request first, so it can override the defaults.
func fakeAPIWarn(t *testing.T, h func(w http.ResponseWriter, r *http.Request) bool, warn func(string)) *Provider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case h(w, r):
		case r.URL.Path == "/repos/acme/web/installation":
			w.Write([]byte(`{"id":777}`))
		case r.URL.Path == "/app/installations/777/access_tokens":
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"token":"ghs_fixture_token_1","expires_at":"2026-09-27T11:00:00Z"}`))
		case r.URL.Path == "/app":
			w.Write([]byte(`{"slug":"fugaro-app"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			http.Error(w, "unexpected", http.StatusTeapot)
		}
	}))
	t.Cleanup(srv.Close)
	p, err := New(Options{Owner: "acme", Repo: "web", AppID: "1234", PrivateKey: key(t), BaseURL: srv.URL, Warn: warn,
		Now: func() time.Time { return time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestGitHubIdentityRetriedAfterTransientFailure covers a GET /app that
// fails for a passing reason (a cancelled context, a 408, a 5xx): it is not
// remembered, so the next lookup asks again and can succeed. A definite
// answer (a 403, or the slug) is remembered, and GET /app isn't sent again.
func TestGitHubIdentityRetriedAfterTransientFailure(t *testing.T) {
	var mu sync.Mutex
	var appStatus []int // what successive GET /app requests answer
	var warnings []string
	p := fakeAPIWarn(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/app" {
			return false
		}
		mu.Lock()
		defer mu.Unlock()
		if len(appStatus) == 0 {
			t.Errorf("GET /app sent after a definite answer")
			http.Error(w, "unexpected", http.StatusTeapot)
			return true
		}
		status := appStatus[0]
		appStatus = appStatus[1:]
		if status != http.StatusOK {
			http.Error(w, `{"message":"no"}`, status)
			return true
		}
		w.Write([]byte(`{"slug":"fugaro-app"}`))
		return true
	}, func(msg string) { warnings = append(warnings, msg) })

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, ok := p.appSlug(cancelled); ok {
		t.Fatal("a cancelled lookup found the slug")
	}
	// A 408 is as passing as a 5xx, as the Bitbucket adapter treats it.
	appStatus = []int{http.StatusBadGateway, http.StatusRequestTimeout, http.StatusOK}
	if _, ok := p.appSlug(ctx); ok {
		t.Fatal("a 502 found the slug")
	}
	if _, ok := p.appSlug(ctx); ok {
		t.Fatal("a 408 found the slug")
	}
	if slug, ok := p.appSlug(ctx); !ok || slug != "fugaro-app" {
		t.Fatalf("slug = %q, %t after a transient failure", slug, ok)
	}
	if slug, ok := p.appSlug(ctx); !ok || slug != "fugaro-app" {
		t.Fatalf("slug = %q, %t once known", slug, ok)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %q, want one", warnings)
	}

	// A 403 is a definite answer: remembered, and warned about once.
	warnings = nil
	p = fakeAPIWarn(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/app" {
			return false
		}
		mu.Lock()
		defer mu.Unlock()
		if len(appStatus) == 0 {
			t.Errorf("GET /app sent after a definite answer")
		}
		appStatus = nil
		http.Error(w, `{"message":"Resource not accessible by integration"}`, http.StatusForbidden)
		return true
	}, func(msg string) { warnings = append(warnings, msg) })
	appStatus = []int{http.StatusForbidden}
	for range 2 {
		if _, ok := p.appSlug(ctx); ok {
			t.Fatal("a 403 found the slug")
		}
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %q, want one", warnings)
	}
}

// TestGitHubCommentsPageCap covers a listing that never ends: it stops at
// the cap with an error naming it, for the review threads and for a REST
// listing alike.
func TestGitHubCommentsPageCap(t *testing.T) {
	for _, endless := range []string{"/graphql", "/repos/acme/web/issues/12/comments"} {
		t.Run(endless, func(t *testing.T) {
			var mu sync.Mutex
			hits := 0
			var p *Provider
			p = fakeAPI(t, func(w http.ResponseWriter, r *http.Request) bool {
				mu.Lock()
				defer mu.Unlock()
				more := r.URL.Path == endless
				if more {
					hits++
				}
				switch r.URL.Path {
				case "/graphql":
					fmt.Fprintf(w, `{"data":{"repository":{"pullRequest":{"reviewThreads":{"pageInfo":{"hasNextPage":%t,"endCursor":"c%d"},"nodes":[]}}}}}`, more, hits)
				case "/repos/acme/web/pulls/12/reviews", "/repos/acme/web/issues/12/comments":
					if more {
						w.Header().Set("Link", fmt.Sprintf(`<http://%s%s?per_page=100&page=%d>; rel="next"`, r.Host, r.URL.Path, hits+1))
					}
					w.Write([]byte(`[]`))
				default:
					return false
				}
				return true
			})
			_, err := p.Comments(ctx, 12)
			if err == nil || !strings.Contains(err.Error(), "20 pages") {
				t.Fatalf("err = %v", err)
			}
			if hits != 20 {
				t.Fatalf("%s served %d pages, want 20", endless, hits)
			}
		})
	}
}

// TestGitHubCommentsSelf covers telling Fugaro's own comments apart: the
// App's bot as REST shows it (<slug>[bot]) and as GraphQL shows it (a Bot
// named <slug>), but not a person whose login is the slug, nor another bot.
func TestGitHubCommentsSelf(t *testing.T) {
	got, err := open(t, "comments_self.json").Comments(ctx, 12)
	if err != nil {
		t.Fatal(err)
	}
	self := map[string]bool{}
	for _, c := range got {
		if !c.SelfKnown {
			t.Errorf("%s: SelfKnown is false", c.ID)
		}
		self[c.ID] = c.Self
	}
	want := map[string]bool{"PRRC_21": true, "PRRC_22": false, "PRRC_23": false, "IC_kwDOA621": true, "IC_kwDOA622": false}
	if len(self) != len(want) {
		t.Fatalf("self = %v", self)
	}
	for id, w := range want {
		if self[id] != w {
			t.Errorf("%s: Self = %t, want %t", id, self[id], w)
		}
	}
}

// TestGitHubCommentsIdentityForbidden covers an App that can't read its
// own identity: the comments are still listed, none is marked as Fugaro's
// or as known either way, and one warning says so, however many listings
// follow (GET /app is tried once per Provider).
func TestGitHubCommentsIdentityForbidden(t *testing.T) {
	var warnings []string
	p := openWarn(t, "app_identity_forbidden.json", func(msg string) { warnings = append(warnings, msg) })
	for round := 0; round < 2; round++ {
		got, err := p.Comments(ctx, 12)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range got {
			if c.Self || c.SelfKnown {
				t.Errorf("%s: Self = %t, SelfKnown = %t", c.ID, c.Self, c.SelfKnown)
			}
		}
		if round == 0 && len(got) != 2 {
			t.Fatalf("got %+v", got)
		}
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "403") {
		t.Fatalf("warnings = %q", warnings)
	}
}

func TestGitHubEnsureByNumber(t *testing.T) {
	s := spec(false, []string{"fugaro"}, []string{"octocat"})
	s.Number = 12
	pr, err := open(t, "ensure_by_number.json").EnsurePR(ctx, s)
	if err != nil || pr != (gitprov.PR{Number: 12, URL: "https://github.com/acme/web/pull/12"}) {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

// TestGitHubEnsureByNumberClosed covers a pull request closed since the
// run began: the update fails with ErrPRNotOpen and sends nothing (the
// fixture has no mutation, so one would fail the test).
func TestGitHubEnsureByNumberClosed(t *testing.T) {
	s := spec(true, nil, nil)
	s.Number = 12
	pr, err := open(t, "ensure_by_number_closed.json").EnsurePR(ctx, s)
	if !errors.Is(err, gitprov.ErrPRNotOpen) || !strings.Contains(err.Error(), "closed") || pr.Number != 12 {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

func TestGitHubEnsureByNumberBranchMismatch(t *testing.T) {
	s := spec(true, nil, nil)
	s.Number, s.Branch = 12, "fugaro/20260927-111500-ef01"
	pr, err := open(t, "pull_open.json").EnsurePR(ctx, s)
	if !errors.Is(err, gitprov.ErrPRNotOpen) || !strings.Contains(err.Error(), branchName) || pr.Number != 12 {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

// TestGitHubPullRequestWrongNumber: a response for another pull request
// than the one asked for is refused, so nothing acts on that other PR.
func TestGitHubPullRequestWrongNumber(t *testing.T) {
	p := fakeAPI(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/repos/acme/web/pulls/12" {
			return false
		}
		w.Write([]byte(`{"number":13,"state":"open","html_url":"https://github.com/acme/web/pull/13","head":{"ref":"fugaro/x","sha":"0123456789abcdef0123456789abcdef01234567"},"user":{"id":1}}`))
		return true
	})
	if _, err := p.PullRequest(ctx, 12); err == nil || !strings.Contains(err.Error(), "the response is for #13") {
		t.Fatalf("err = %v", err)
	}
	if _, err := p.EnsurePR(ctx, gitprov.PRSpec{Number: 12, Branch: "fugaro/x", Draft: true}); err == nil {
		t.Fatal("EnsurePR updated another pull request")
	}
}
