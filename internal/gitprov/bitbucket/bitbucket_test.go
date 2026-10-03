package bitbucket

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
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
	srv := httpfixture.Serve(t, filepath.Join("testdata", fixture))
	p, err := New(Options{Workspace: "acme", Slug: "web", Token: "bb-token-1234", BaseURL: srv.URL + "/2.0"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func spec(draft bool, reviewers ...string) gitprov.PRSpec {
	return gitprov.PRSpec{Branch: branchName, Base: "main", Title: "Add search", Body: "Adds search.",
		Draft: draft, Labels: []string{"fugaro"}, Reviewers: reviewers}
}

func TestCreateReady(t *testing.T) {
	p := open(t, "create_ready.json")
	pr, err := p.EnsurePR(ctx, spec(false, "{5b1d0c7e-0000-4000-8000-000000000001}", "557058:0000"))
	if err != nil || pr != (gitprov.PR{Number: 42, URL: "https://bitbucket.org/acme/web/pull-requests/42"}) {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

func TestCreateDraft(t *testing.T) {
	p := open(t, "create_draft.json")
	if pr, err := p.EnsurePR(ctx, spec(true)); err != nil || pr.Number != 43 || !pr.Draft {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

// TestDraftIgnoredFallsBackToTitle covers design §15: if Bitbucket does not
// make the pull request a real draft, the title says so instead.
func TestDraftIgnoredFallsBackToTitle(t *testing.T) {
	p := open(t, "draft_ignored.json")
	if pr, err := p.EnsurePR(ctx, spec(true)); err != nil || pr.Number != 44 || !pr.Draft {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

// TestExistingPRIsOnlyToggled covers a pull request the agent opened
// itself: it is found by branch, marked ready, its fallback prefix removed,
// and its title and description otherwise left alone.
func TestExistingPRIsOnlyToggled(t *testing.T) {
	p := open(t, "existing_to_ready.json")
	if pr, err := p.EnsurePR(ctx, spec(false)); err != nil || pr.Number != 45 || pr.Draft {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

func TestRejectedReviewerStillOpensPR(t *testing.T) {
	p := open(t, "reviewer_rejected.json")
	pr, err := p.EnsurePR(ctx, spec(false, "557058:gone"))
	var partial *gitprov.PartialError
	if !errors.As(err, &partial) || pr.Number != 46 || !strings.Contains(err.Error(), "557058:gone") {
		t.Fatalf("pr = %+v, err = %v", pr, err)
	}
}

func TestComment(t *testing.T) {
	p := open(t, "comment.json")
	if err := p.Comment(ctx, gitprov.PR{Number: 42}, "### Fugaro run report"); err != nil {
		t.Fatal(err)
	}
}

func TestUnauthorized(t *testing.T) {
	p := open(t, "unauthorized.json")
	if _, err := p.EnsurePR(ctx, spec(false)); err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("err = %v", err)
	}
}

func TestGitAuth(t *testing.T) {
	p, err := New(Options{Workspace: "acme", Slug: "web", Token: "bb-token-1234"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := p.GitAuth(ctx, 0)
	if err != nil || a.Username != "x-token-auth" || a.Token != "bb-token-1234" || !a.Expires.IsZero() {
		t.Fatalf("auth = %+v, %v", a, err)
	}
}

func TestNewValidates(t *testing.T) {
	for _, o := range []Options{{Slug: "web", Token: "t"}, {Workspace: "acme", Token: "t"}, {Workspace: "acme", Slug: "web"}} {
		if _, err := New(o); err == nil {
			t.Errorf("New(%+v) succeeded", o)
		}
	}
}

// TestLabelsWarnOnce covers the user-approved ruling that Bitbucket Cloud
// has no PR labels: the adapter never sends spec.Labels (the fixture's POST
// bodies carry none, so httpfixture would fail the exchange otherwise), and
// it logs exactly one warning per Provider instance even though two
// EnsurePR calls each carry non-empty Labels.
func TestLabelsWarnOnce(t *testing.T) {
	srv := httpfixture.Serve(t, filepath.Join("testdata", "labels_warning.json"))
	var mu sync.Mutex
	var warnings int
	var lastMsg string
	p, err := New(Options{
		Workspace: "acme", Slug: "web", Token: "bb-token-1234", BaseURL: srv.URL + "/2.0",
		Warn: func(msg string) {
			mu.Lock()
			defer mu.Unlock()
			warnings++
			lastMsg = msg
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	specA := gitprov.PRSpec{Branch: "fugaro/labels-a", Base: "main", Title: "Add search", Body: "Adds search.",
		Labels: []string{"fugaro"}}
	specB := gitprov.PRSpec{Branch: "fugaro/labels-b", Base: "main", Title: "Add search", Body: "Adds search.",
		Labels: []string{"fugaro"}}

	if pr, err := p.EnsurePR(ctx, specA); err != nil || pr.Number != 61 {
		t.Fatalf("first EnsurePR: pr = %+v, err = %v", pr, err)
	}
	if pr, err := p.EnsurePR(ctx, specB); err != nil || pr.Number != 62 {
		t.Fatalf("second EnsurePR: pr = %+v, err = %v", pr, err)
	}

	mu.Lock()
	defer mu.Unlock()
	if warnings != 1 {
		t.Fatalf("warnings = %d, want 1", warnings)
	}
	if !strings.Contains(lastMsg, "labels") || !strings.Contains(lastMsg, "fugaro") {
		t.Fatalf("warning message = %q", lastMsg)
	}
}

// TestReadyStuckAsDraftReportsPartial covers the plan-mandated fix: when a
// ready pull request is wanted but Bitbucket still reports it as a draft
// (it did not honor a prior draft:false), EnsurePR must still return the
// existing, valid pull request — per the Provider contract, a *PartialError
// means the pull request exists but a setting could not be applied — rather
// than an empty PR with a hard error, which would break the contract and
// fail identically on every retry.
func TestReadyStuckAsDraftReportsPartial(t *testing.T) {
	p := open(t, "existing_stuck_draft.json")
	pr, err := p.EnsurePR(ctx, spec(false))
	var partial *gitprov.PartialError
	if !errors.As(err, &partial) {
		t.Fatalf("err = %v, want a *gitprov.PartialError", err)
	}
	if pr != (gitprov.PR{Number: 50, URL: "https://bitbucket.org/acme/web/pull-requests/50", Draft: true}) {
		t.Fatalf("pr = %+v", pr)
	}
}

// TestExistingPRNoOpSkipsPUT covers the minor fix: when an existing pull
// request already has the wanted draft state and no fallback prefix to add
// or remove, EnsurePR must not send a PUT at all. The fixture holds only
// the GET exchange, so httpfixture would fail the test if a PUT were sent.
func TestExistingPRNoOpSkipsPUT(t *testing.T) {
	p := open(t, "existing_no_op.json")
	pr, err := p.EnsurePR(ctx, spec(false))
	if err != nil || pr != (gitprov.PR{Number: 47, URL: "https://bitbucket.org/acme/web/pull-requests/47"}) {
		t.Fatalf("pr = %+v, err = %v", pr, err)
	}
}

// TestRejectedReviewerAndStuckDraftBothReported covers the minor fix: when
// the reviewer-fallback POST succeeds but finish then fails to get the pull
// request out of draft, both failures must be reported, not just one.
func TestRejectedReviewerAndStuckDraftBothReported(t *testing.T) {
	p := open(t, "reviewer_rejected_stuck_draft.json")
	pr, err := p.EnsurePR(ctx, spec(false, "557058:gone"))
	var partial *gitprov.PartialError
	if !errors.As(err, &partial) {
		t.Fatalf("err = %v, want a *gitprov.PartialError", err)
	}
	if pr != (gitprov.PR{Number: 48, URL: "https://bitbucket.org/acme/web/pull-requests/48", Draft: true}) {
		t.Fatalf("pr = %+v", pr)
	}
	if !strings.Contains(err.Error(), "557058:gone") {
		t.Fatalf("err = %v, want it to mention the rejected reviewer", err)
	}
	if !strings.Contains(err.Error(), "ready") && !strings.Contains(err.Error(), "draft") {
		t.Fatalf("err = %v, want it to also mention the stuck draft", err)
	}
}

// TestRetitleAddFailureIsRetryable covers the round-1 correction: when the
// fallback title PUT that ADDS the "[DRAFT] " prefix (because Bitbucket
// ignored draft:true) fails, the PR still looks ready (or unmarked), which
// is safe to leave for a retry — EnsurePR finds the PR again and re-runs
// finish — so this must be a plain, retryable error, not a *PartialError
// (the runner treats *PartialError as success and would not retry, ending
// the run with a PR that looks more ready than requested, violating design
// §4.2). The pull request that was created is still returned in full.
func TestRetitleAddFailureIsRetryable(t *testing.T) {
	p := open(t, "retitle_fails.json")
	pr, err := p.EnsurePR(ctx, spec(true))
	var partial *gitprov.PartialError
	if errors.As(err, &partial) {
		t.Fatalf("err = %v, want a plain retryable error, not *gitprov.PartialError", err)
	}
	if err == nil || !strings.Contains(err.Error(), "retitling") {
		t.Fatalf("err = %v, want it to mention retitling", err)
	}
	if pr != (gitprov.PR{Number: 49, URL: "https://bitbucket.org/acme/web/pull-requests/49"}) {
		t.Fatalf("pr = %+v", pr)
	}
}

// TestRetitleRemoveFailureIsPartial covers the round-1 correction's other
// direction: when the fallback title PUT that REMOVES the "[DRAFT] " prefix
// (ready was wanted, and the pull request still carries the fallback
// prefix) fails, the PR is left looking more like a draft than requested,
// which is the conservative direction (design §4.2) — so this must stay a
// *gitprov.PartialError, and the existing PR must still be returned. This
// calls finish directly (rather than routing through EnsurePR) because the
// normal EnsurePR path for an existing PR already strips the prefix in its
// own PUT before finish ever runs; finish's own fallback PUT only needs to
// remove a leftover prefix when handed a PR that still carries one, which
// is straightforward to construct directly.
func TestRetitleRemoveFailureIsPartial(t *testing.T) {
	p := open(t, "retitle_remove_fails.json")
	in := pullRequest{ID: 51, Title: "[DRAFT] Add search", Draft: false}
	in.Links.HTML.Href = "https://bitbucket.org/acme/web/pull-requests/51"
	pr, err := p.finish(ctx, in, false)
	var partial *gitprov.PartialError
	if !errors.As(err, &partial) {
		t.Fatalf("err = %v, want a *gitprov.PartialError", err)
	}
	if !strings.Contains(err.Error(), "retitling") {
		t.Fatalf("err = %v, want it to mention retitling", err)
	}
	// The prefix is still there, so the pull request is still a draft.
	if pr != (gitprov.PR{Number: 51, URL: "https://bitbucket.org/acme/web/pull-requests/51", Draft: true}) {
		t.Fatalf("pr = %+v", pr)
	}
}

// TestFindEscapesBranchName covers the minor fix: a branch name carrying a
// double quote or backslash must not be allowed to break out of the BBQL
// string literal in find's ?q= parameter.
func TestFindEscapesBranchName(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"values":[]}`))
	}))
	defer srv.Close()

	p, err := New(Options{Workspace: "acme", Slug: "web", Token: "t", BaseURL: srv.URL + "/2.0"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.find(ctx, `fugaro/quo"te\slash`); err != nil {
		t.Fatal(err)
	}
	want := `source.branch.name="fugaro/quo\"te\\slash" AND source.repository.full_name="acme/web" AND state="OPEN"`
	if gotQuery != want {
		t.Fatalf("query = %q, want %q", gotQuery, want)
	}
}

// TestPrefixedPRStaysDraftWithoutPUT covers an existing pull request that
// is already a draft by its fallback title ("[DRAFT] …", draft false) when
// a draft is wanted: it counts as a draft already, so no PUT is sent (one
// that stripped the prefix and failed to re-add it would leave it looking
// ready). The fixture holds only the GET.
func TestPrefixedPRStaysDraftWithoutPUT(t *testing.T) {
	p := open(t, "existing_prefixed_stays_draft.json")
	pr, err := p.EnsurePR(ctx, spec(true))
	if err != nil || pr != (gitprov.PR{Number: 52, URL: "https://bitbucket.org/acme/web/pull-requests/52", Draft: true}) {
		t.Fatalf("pr = %+v, err = %v", pr, err)
	}
}

// TestExistingReadyUpdateFailureIsPartial covers a failed PUT on an
// existing draft pull request that should go ready: the pull request is
// returned, still a draft, with a *gitprov.PartialError (the conservative
// direction), never an empty PR.
func TestExistingReadyUpdateFailureIsPartial(t *testing.T) {
	p := open(t, "existing_ready_update_fails.json")
	pr, err := p.EnsurePR(ctx, spec(false))
	var partial *gitprov.PartialError
	if !errors.As(err, &partial) || !strings.Contains(err.Error(), "updating pull request #53") {
		t.Fatalf("err = %v, want a *gitprov.PartialError about the update", err)
	}
	if pr != (gitprov.PR{Number: 53, URL: "https://bitbucket.org/acme/web/pull-requests/53", Draft: true}) {
		t.Fatalf("pr = %+v", pr)
	}
}

// TestExistingDraftUpdateFailureIsRetryable covers a failed PUT on an
// existing ready pull request that should become a draft: it still looks
// ready, so the error is plain and retryable, and the pull request is
// returned as currently seen.
func TestExistingDraftUpdateFailureIsRetryable(t *testing.T) {
	p := open(t, "existing_draft_update_fails.json")
	pr, err := p.EnsurePR(ctx, spec(true))
	var partial *gitprov.PartialError
	if err == nil || errors.As(err, &partial) || !strings.Contains(err.Error(), "updating pull request #54") {
		t.Fatalf("err = %v, want a plain error about the update", err)
	}
	if pr != (gitprov.PR{Number: 54, URL: "https://bitbucket.org/acme/web/pull-requests/54"}) {
		t.Fatalf("pr = %+v", pr)
	}
}

// TestFindLowercasesRepository covers Bitbucket's lowercase repository
// slugs: source.repository.full_name is compared as a string, so a
// fugaro.yaml or task naming "Acme/Web" must still find the pull request.
func TestFindLowercasesRepository(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"values":[]}`))
	}))
	defer srv.Close()
	p, err := New(Options{Workspace: "Acme", Slug: "Web", Token: "t", BaseURL: srv.URL + "/2.0"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.find(ctx, "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	if want := `source.repository.full_name="acme/web"`; !strings.Contains(gotQuery, want) {
		t.Fatalf("query = %q, want it to contain %q", gotQuery, want)
	}
}

// apiTransport sends every request to srv, whatever its host, so a
// Provider can keep Bitbucket's real API root (and fixtures can hold its
// real absolute "next" URLs) while a fixture server answers.
type apiTransport struct{ srv *httptest.Server }

func (a apiTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	u, _ := url.Parse(a.srv.URL)
	r = r.Clone(r.Context())
	r.URL.Scheme, r.URL.Host, r.Host = u.Scheme, u.Host, u.Host
	return http.DefaultTransport.RoundTrip(r)
}

// openAPI is open with DefaultBaseURL as the API root, for fixtures whose
// bodies carry absolute next-page URLs on api.bitbucket.org.
func openAPI(t *testing.T, fixture string, warn func(string)) *Provider {
	t.Helper()
	srv := httpfixture.Serve(t, filepath.Join("testdata", fixture))
	p, err := New(Options{Workspace: "acme", Slug: "web", Token: "bb-token-1234",
		HTTP: &http.Client{Transport: apiTransport{srv.Server}}, Warn: warn})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBitbucketRepositoryVisibility(t *testing.T) {
	info, err := open(t, "repo_private.json").Repository(ctx)
	if err != nil || !info.Private {
		t.Fatalf("private repository = %+v, %v", info, err)
	}
	p := open(t, "repo_public.json")
	if info, err := p.Repository(ctx); err != nil || info.Private {
		t.Fatalf("public repository = %+v, %v", info, err)
	}
	// A response without is_private is never taken for a private
	// repository.
	if info, err := p.Repository(ctx); err == nil || !strings.Contains(err.Error(), "is_private") {
		t.Fatalf("repository without is_private = %+v, %v; want an error", info, err)
	}
}

func TestBitbucketPullRequestOpen(t *testing.T) {
	p := open(t, "pull_open.json")
	got, err := p.PullRequest(ctx, 12)
	want := gitprov.PRInfo{Number: 12, URL: "https://bitbucket.org/acme/web/pull-requests/12", State: gitprov.PROpen, Draft: true, Title: "Add search",
		AuthorID: "712020:00000000-0000-4000-8000-00000000b07e", SourceBranch: branchName, SourceRepo: "acme/web", HeadSHA: "0123456789ab"}
	if err != nil || got != want {
		t.Fatalf("PullRequest = %+v, %v\nwant %+v", got, err, want)
	}
	if !gitprov.SameCommit(got.HeadSHA, "0123456789abcdef0123456789abcdef01234567") {
		t.Fatalf("head %s does not match the full commit", got.HeadSHA)
	}
	// A deleted author account, a deleted source repository and no source
	// commit leave those fields empty (so the author is nobody's, and the
	// head matches no commit), never a guess.
	got, err = p.PullRequest(ctx, 14)
	if err != nil || got.AuthorID != "" || got.SourceRepo != "" || got.HeadSHA != "" || got.State != gitprov.PROpen {
		t.Fatalf("PullRequest(14) = %+v, %v", got, err)
	}
	// A fork's source repository is reported as the fork's full name.
	if got, err = p.PullRequest(ctx, 15); err != nil || got.SourceRepo != "someone/web-fork" {
		t.Fatalf("PullRequest(15) = %+v, %v", got, err)
	}
}

// TestBitbucketPullRequestWrongID: a response naming another pull request
// than the one asked for is an error, and an update by number never
// writes to it.
func TestBitbucketPullRequestWrongID(t *testing.T) {
	var puts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			puts++
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id": 13, "state": "OPEN", "title": "Other", "draft": true, "source": {"branch": {"name": %q}}}`, branchName)
	}))
	defer srv.Close()
	p, err := New(Options{Workspace: "acme", Slug: "web", Token: "t", BaseURL: srv.URL + "/2.0"})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := p.PullRequest(ctx, 12); err == nil {
		t.Fatalf("PullRequest(12) = %+v, want an error", got)
	}
	s := spec(false)
	s.Number = 12
	if pr, err := p.EnsurePR(ctx, s); err == nil || puts != 0 {
		t.Fatalf("EnsurePR by number = %+v, %v, %d writes", pr, err, puts)
	}
}

func TestBitbucketPullRequestMerged(t *testing.T) {
	got, err := open(t, "pull_merged.json").PullRequest(ctx, 12)
	if err != nil || got.State != gitprov.PRMerged || got.Number != 12 {
		t.Fatalf("PullRequest = %+v, %v", got, err)
	}
}

// TestBitbucketPullRequestDeclined covers both of Bitbucket's closed,
// unmerged states: DECLINED and SUPERSEDED.
func TestBitbucketPullRequestDeclined(t *testing.T) {
	p := open(t, "pull_declined.json")
	for _, n := range []int{12, 13} {
		got, err := p.PullRequest(ctx, n)
		if err != nil || got.State != gitprov.PRClosed || got.Number != n {
			t.Fatalf("PullRequest(%d) = %+v, %v", n, got, err)
		}
	}
}

// TestBitbucketPullRequestUnknownState: a state the adapter doesn't know,
// or none, is an error, never guessed to be open.
func TestBitbucketPullRequestUnknownState(t *testing.T) {
	for _, body := range []string{`{"id": 12, "state": "REOPENED"}`, `{"id": 12}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(body))
		}))
		p, err := New(Options{Workspace: "acme", Slug: "web", Token: "t", BaseURL: srv.URL + "/2.0"})
		if err != nil {
			t.Fatal(err)
		}
		if got, err := p.PullRequest(ctx, 12); err == nil {
			t.Errorf("PullRequest over %s = %+v, want an error", body, got)
		}
		srv.Close()
	}
	p, _ := New(Options{Workspace: "acme", Slug: "web", Token: "t", BaseURL: "http://127.0.0.1:1/2.0"})
	if _, err := p.PullRequest(ctx, 0); err == nil {
		t.Error("PullRequest(0) succeeded")
	}
}

const (
	aliceID = "557058:00000000-0000-0000-0000-000000000001"
	bobID   = "557058:00000000-0000-0000-0000-000000000002"
	botID   = "712020:00000000-0000-4000-8000-00000000b07e"
)

func commentURL(id int) string {
	return fmt.Sprintf("https://bitbucket.org/acme/web/pull-requests/12/_/diff#comment-%d", id)
}

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		panic(err)
	}
	return t
}

// TestBitbucketComments covers a resolved inline thread with a reply (the
// resolution, an empty object as Bitbucket really sends it, is on the
// thread's first comment only), an unresolved, outdated inline thread
// whose reply carries no inline field of its own, a deleted comment, a
// general comment, and a pending one (unpublished, so left out), returned
// oldest first. A live reply carries its thread's inline field, and a
// current comment's inline has no outdated field
// (testdata/recorded/follow_up_reads.json); the reply without one and the
// outdated inline field are kept as the adapter's fallbacks, not seen live.
func TestBitbucketComments(t *testing.T) {
	got, err := open(t, "comments.json").Comments(ctx, 12)
	if err != nil {
		t.Fatal(err)
	}
	base := gitprov.Comment{Collaborator: true, SelfKnown: true}
	mk := func(id int, kind gitprov.CommentKind, author, authorID, body, created string, f func(*gitprov.Comment)) gitprov.Comment {
		c := base
		c.ID, c.Kind, c.Author, c.AuthorID, c.Body, c.CreatedAt, c.URL = fmt.Sprint(id), kind, author, authorID, body, at(created), commentURL(id)
		if f != nil {
			f(&c)
		}
		return c
	}
	want := []gitprov.Comment{
		mk(101, gitprov.CommentInline, "Alice", aliceID, "Rename this.", "2026-09-20T10:00:00.123456Z", func(c *gitprov.Comment) {
			c.Resolved, c.Path, c.Line = true, "src/a.go", 10
		}),
		mk(102, gitprov.CommentInline, "Bob", bobID, "Done.", "2026-09-20T10:30:00Z", func(c *gitprov.Comment) {
			c.Resolved, c.Path, c.Line = true, "src/a.go", 10
		}),
		mk(103, gitprov.CommentInline, "Alice", aliceID, "Handle nil here.", "2026-09-20T11:00:00Z", func(c *gitprov.Comment) {
			c.Outdated, c.Path, c.Line = true, "src/b.go", 7
		}),
		mk(106, gitprov.CommentInline, "Bob", bobID, "Agreed.", "2026-09-20T11:30:00Z", func(c *gitprov.Comment) {
			c.Outdated, c.Path, c.Line = true, "src/b.go", 7
		}),
		mk(104, gitprov.CommentGeneral, "Alice", aliceID, "", "2026-09-20T12:00:00Z", func(c *gitprov.Comment) { c.Deleted = true }),
		mk(105, gitprov.CommentGeneral, "Alice", aliceID, "Please add tests.", "2026-09-20T13:00:00Z", nil),
	}
	if len(got) != len(want) {
		t.Fatalf("got %d comments, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("comment %d:\n got %+v\nwant %+v", i, got[i], want[i])
		}
		if got[i].Kind == gitprov.CommentReview {
			t.Errorf("comment %s is a review summary: Bitbucket has none", got[i].ID)
		}
	}
}

func TestBitbucketCommentsPaged(t *testing.T) {
	got, err := openAPI(t, "comments_paged.json", nil).Comments(ctx, 12)
	if err != nil {
		t.Fatal(err)
	}
	var bodies []string
	for _, c := range got {
		bodies = append(bodies, c.Body)
	}
	if strings.Join(bodies, ",") != "one,two,three" {
		t.Fatalf("bodies = %q", bodies)
	}
}

// TestBitbucketCommentsRefusesForeignNext: a next page on another host is
// never requested (the fixture has no exchange for it), since the request
// would carry the token there.
func TestBitbucketCommentsRefusesForeignNext(t *testing.T) {
	got, err := openAPI(t, "comments_foreign_next.json", nil).Comments(ctx, 12)
	if err == nil || !strings.Contains(err.Error(), "left the API host") {
		t.Fatalf("Comments = %+v, %v; want a refusal", got, err)
	}
	if strings.Contains(err.Error(), "bb-token-1234") {
		t.Fatalf("the error holds the token: %v", err)
	}
}

// TestBitbucketCommentsPageCap: a listing that never ends is refused after
// 20 pages rather than read without end.
func TestBitbucketCommentsPageCap(t *testing.T) {
	var pages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/2.0/user" {
			w.Write([]byte(`{"type": "app_user", "uuid": "{00000000-0000-4000-8000-00000000b07e}"}`))
			return
		}
		pages++
		fmt.Fprintf(w, `{"values": [], "next": "http://%s/2.0/repositories/acme/web/pullrequests/12/comments?pagelen=100&page=%d"}`, r.Host, pages+1)
	}))
	defer srv.Close()
	p, err := New(Options{Workspace: "acme", Slug: "web", Token: "t", BaseURL: srv.URL + "/2.0"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Comments(ctx, 12); err == nil || !strings.Contains(err.Error(), "more than 20 pages") {
		t.Fatalf("err = %v", err)
	}
	if pages != 20 {
		t.Fatalf("served %d pages, want 20", pages)
	}
}

// TestBitbucketCommentsSelf: a comment is Self when its user's uuid is the
// token's user's (GET /user, read once per Provider), and only then: the
// same account_id or display name under another uuid is not.
func TestBitbucketCommentsSelf(t *testing.T) {
	p := open(t, "comments_self.json")
	got, err := p.Comments(ctx, 12)
	if err != nil {
		t.Fatal(err)
	}
	self := map[string]bool{}
	for _, c := range got {
		if !c.SelfKnown {
			t.Errorf("comment %s: SelfKnown false", c.ID)
		}
		self[c.ID] = c.Self
	}
	if want := map[string]bool{"131": true, "132": false, "133": false, "134": false}; !reflect.DeepEqual(self, want) {
		t.Fatalf("self = %v, want %v", self, want)
	}
	// A deleted account (user null) is nobody: no ID, so never trusted.
	if last := got[len(got)-1]; last.ID != "134" || last.AuthorID != "" || last.Author != "" {
		t.Fatalf("the comment by a deleted account = %+v", last)
	}
	if got[0].AuthorID != botID {
		t.Fatalf("the report's AuthorID = %q", got[0].AuthorID)
	}
	// The second listing reuses the identity: the fixture has one GET /user.
	if _, err := p.Comments(ctx, 12); err != nil {
		t.Fatal(err)
	}
}

// TestBitbucketCommentsUserForbidden: a token that may not read GET /user
// leaves every comment's SelfKnown false, with one warning, and the
// lookup isn't tried again.
func TestBitbucketCommentsUserForbidden(t *testing.T) {
	var warnings []string
	p := open(t, "user_forbidden.json")
	p.o.Warn = func(m string) { warnings = append(warnings, m) }
	got, err := p.Comments(ctx, 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Self || got[0].SelfKnown || got[0].AuthorID != botID {
		t.Fatalf("comments = %+v", got)
	}
	if _, err := p.Comments(ctx, 12); err != nil {
		t.Fatal(err)
	}
	// Bitbucket's own answer to a repository access token (seen live).
	if len(warnings) != 1 || !strings.Contains(warnings[0], "GET /user") || !strings.Contains(warnings[0], "HTTP 403") ||
		!strings.Contains(warnings[0], "not accessible by this authentication mechanism") {
		t.Fatalf("warnings = %q, want one about GET /user", warnings)
	}
}

// TestBitbucketCommentsUserTransient: a lookup that fails for a reason
// other than the API's answer about the token (a 5xx, a network error)
// fails the listing instead of reading every comment as not Fugaro's, and
// is tried again on the next listing.
func TestBitbucketCommentsUserTransient(t *testing.T) {
	var warnings []string
	p := open(t, "user_transient.json")
	p.o.Warn = func(m string) { warnings = append(warnings, m) }
	if got, err := p.Comments(ctx, 12); err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("Comments = %+v, %v; want the 503", got, err)
	}
	got, err := p.Comments(ctx, 12)
	if err != nil || len(got) != 1 || !got[0].Self || !got[0].SelfKnown {
		t.Fatalf("Comments after the retry = %+v, %v", got, err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %q", warnings)
	}
}

// TestBitbucketEnsureByNumber updates pull request 12's draft state only:
// the fixture holds no lookup by branch and no POST …/pullrequests, so
// either would fail the test.
func TestBitbucketEnsureByNumber(t *testing.T) {
	s := spec(false)
	s.Number = 12
	s.Title, s.Body = "A new title that must not be sent", "A new body that must not be sent"
	pr, err := open(t, "ensure_by_number.json").EnsurePR(ctx, s)
	if err != nil || pr != (gitprov.PR{Number: 12, URL: "https://bitbucket.org/acme/web/pull-requests/12"}) {
		t.Fatalf("pr = %+v, %v", pr, err)
	}
}

// TestBitbucketEnsureByNumberDeclined: a declined pull request, or one on
// another branch, or a merged one, is never updated or replaced:
// ErrPRNotOpen, with the PR.
func TestBitbucketEnsureByNumberDeclined(t *testing.T) {
	p := open(t, "ensure_by_number_declined.json")
	for _, n := range []int{12, 13, 14} {
		s := spec(false)
		s.Number = n
		pr, err := p.EnsurePR(ctx, s)
		if !errors.Is(err, gitprov.ErrPRNotOpen) || pr.Number != n || pr.URL == "" {
			t.Fatalf("EnsurePR(#%d) = %+v, %v; want ErrPRNotOpen with the PR", n, pr, err)
		}
	}
}
