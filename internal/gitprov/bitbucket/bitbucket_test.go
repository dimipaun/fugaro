package bitbucket

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
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
	var warnings int32
	var lastMsg string
	p, err := New(Options{
		Workspace: "acme", Slug: "web", Token: "bb-token-1234", BaseURL: srv.URL + "/2.0",
		Warn: func(msg string) { atomic.AddInt32(&warnings, 1); lastMsg = msg },
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

	if got := atomic.LoadInt32(&warnings); got != 1 {
		t.Fatalf("warnings = %d, want 1", got)
	}
	if !strings.Contains(lastMsg, "labels") || !strings.Contains(lastMsg, "fugaro") {
		t.Fatalf("warning message = %q", lastMsg)
	}
}
