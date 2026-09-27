package bitbucket

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

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

// TestRetitleFailureStillReturnsPR covers the folded-in fix: when the
// fallback title PUT (adding the "[DRAFT] " prefix because Bitbucket ignored
// draft:true) itself fails, EnsurePR must still return the pull request that
// was created — its number, URL and known draft state — alongside a
// *gitprov.PartialError, rather than an empty PR and a plain error, matching
// the Provider contract that an existing pull request is always returned.
func TestRetitleFailureStillReturnsPR(t *testing.T) {
	p := open(t, "retitle_fails.json")
	pr, err := p.EnsurePR(ctx, spec(true))
	var partial *gitprov.PartialError
	if !errors.As(err, &partial) {
		t.Fatalf("err = %v, want a *gitprov.PartialError", err)
	}
	if pr != (gitprov.PR{Number: 49, URL: "https://bitbucket.org/acme/web/pull-requests/49"}) {
		t.Fatalf("pr = %+v", pr)
	}
	if !strings.Contains(err.Error(), "retitling") {
		t.Fatalf("err = %v, want it to mention retitling", err)
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
