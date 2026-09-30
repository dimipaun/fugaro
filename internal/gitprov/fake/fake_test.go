package fake

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/testutil"
)

func TestEnsurePRCreatesThenUpdates(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "provider.json")
	p := &Provider{Path: path}
	pr, err := p.EnsurePR(ctx, gitprov.PRSpec{Branch: "fugaro/x", Title: "T", Draft: true})
	if err != nil || pr.Number != 1 || !pr.Draft {
		t.Fatalf("create = %+v, %v", pr, err)
	}
	pr2, err := (&Provider{Path: path}).EnsurePR(ctx, gitprov.PRSpec{Branch: "fugaro/x", Title: "ignored", Draft: false})
	if err != nil || pr2.Number != 1 || pr2.Draft {
		t.Fatalf("update = %+v, %v", pr2, err)
	}
	if err := p.Comment(ctx, pr2, "report"); err != nil {
		t.Fatal(err)
	}
	st, err := Load(path)
	if err != nil || len(st.PRs) != 1 || st.PRs[0].Spec.Title != "T" || st.PRs[0].Draft || len(st.PRs[0].Comments) != 1 {
		t.Fatalf("state = %+v, %v", st, err)
	}
}

func TestFailEnsureThenSucceed(t *testing.T) {
	ctx := context.Background()
	p := &Provider{FailEnsure: 1}
	if _, err := p.EnsurePR(ctx, gitprov.PRSpec{Branch: "fugaro/x"}); err == nil {
		t.Fatal("first EnsurePR should fail")
	}
	if pr, err := p.EnsurePR(ctx, gitprov.PRSpec{Branch: "fugaro/x"}); err != nil || pr.Number != 1 {
		t.Fatalf("second EnsurePR = %+v, %v", pr, err)
	}
}

func TestGitAuth(t *testing.T) {
	ctx := context.Background()
	if a, err := (&Provider{}).GitAuth(ctx, time.Minute); err != nil || a.Token != "" {
		t.Fatalf("default GitAuth = %+v, %v", a, err)
	}
	var asked time.Duration
	p := &Provider{Auth: func(min time.Duration) gitprov.GitAuth {
		asked = min
		return gitprov.GitAuth{Username: "u", Token: "tok-1234"}
	}}
	if a, err := p.GitAuth(ctx, 45*time.Minute); err != nil || a.Token != "tok-1234" || asked != 45*time.Minute {
		t.Fatalf("GitAuth = %+v, %v (asked %s)", a, err, asked)
	}
}

const runBranch = "fugaro/20260925-000000-0a1b"

// TestFakeEnsureByNumberNeverCreates: an update by number never opens a
// pull request, not even when the one it names is closed.
func TestFakeEnsureByNumberNeverCreates(t *testing.T) {
	ctx := context.Background()
	p := &Provider{}
	if _, err := p.EnsurePR(ctx, gitprov.PRSpec{Branch: runBranch, Title: "T", Body: "B", Draft: false}); err != nil {
		t.Fatal(err)
	}
	pr, err := p.EnsurePR(ctx, gitprov.PRSpec{Number: 1, Branch: runBranch, Title: "other", Body: "other", Draft: true})
	if err != nil || pr.Number != 1 || !pr.Draft {
		t.Fatalf("update by number = %+v, %v", pr, err)
	}
	if st := p.State.PRs[0]; !st.Draft || st.Spec.Title != "T" || st.Spec.Body != "B" {
		t.Fatalf("update by number touched more than the draft state: %+v", st)
	}
	for _, s := range []gitprov.PRState{gitprov.PRClosed, gitprov.PRMerged} {
		p.State.PRs[0].State = s
		pr, err := p.EnsurePR(ctx, gitprov.PRSpec{Number: 1, Branch: runBranch, Draft: false})
		if !errors.Is(err, gitprov.ErrPRNotOpen) || pr.Number != 1 {
			t.Fatalf("%s PR: EnsurePR = %+v, %v; want ErrPRNotOpen with the PR", s, pr, err)
		}
		if len(p.State.PRs) != 1 || !p.State.PRs[0].Draft {
			t.Fatalf("%s PR: state changed: %+v", s, p.State.PRs)
		}
	}
	if _, err := p.EnsurePR(ctx, gitprov.PRSpec{Number: 7, Branch: runBranch}); err == nil || len(p.State.PRs) != 1 {
		t.Fatalf("a missing PR by number: %v, %d PRs", err, len(p.State.PRs))
	}
	// The failure-injection paths keep the PR a refusal returns.
	p.FailEnsureAfterCreate = 1
	if pr, err := p.EnsurePR(ctx, gitprov.PRSpec{Number: 1, Branch: runBranch}); !errors.Is(err, gitprov.ErrPRNotOpen) || pr.Number != 1 {
		t.Fatalf("FailEnsureAfterCreate refusal = %+v, %v", pr, err)
	}
	p.PartialEnsure = errors.New("labels")
	if pr, err := p.EnsurePR(ctx, gitprov.PRSpec{Number: 1, Branch: runBranch}); !errors.Is(err, gitprov.ErrPRNotOpen) || pr.Number != 1 {
		t.Fatalf("PartialEnsure refusal = %+v, %v", pr, err)
	}
}

func TestFakeEnsureByNumberBranchMismatch(t *testing.T) {
	ctx := context.Background()
	p := &Provider{}
	if _, err := p.EnsurePR(ctx, gitprov.PRSpec{Branch: runBranch, Draft: true}); err != nil {
		t.Fatal(err)
	}
	_, err := p.EnsurePR(ctx, gitprov.PRSpec{Number: 1, Branch: "fugaro/20260925-000000-ffff", Draft: false})
	if !errors.Is(err, gitprov.ErrPRNotOpen) {
		t.Fatalf("err = %v, want ErrPRNotOpen", err)
	}
	if len(p.State.PRs) != 1 || !p.State.PRs[0].Draft {
		t.Fatalf("state changed: %+v", p.State.PRs)
	}
}

func TestFakeCommentsMergedByTime(t *testing.T) {
	ctx := context.Background()
	t0 := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC) // before any comment posted now
	p := &Provider{SelfID: "fugaro-bot"}
	pr, err := p.EnsurePR(ctx, gitprov.PRSpec{Branch: runBranch})
	if err != nil {
		t.Fatal(err)
	}
	st := &p.State.PRs[0]
	st.Comments = []string{"legacy report", "first report", "second report"}
	// A state file written before comment times has none for its first
	// comment: a missing time sorts first.
	st.CommentTimes = []time.Time{{}, t0.Add(time.Minute), t0.Add(3 * time.Minute)}
	st.Foreign = []gitprov.Comment{
		{ID: "c2", Kind: gitprov.CommentInline, Author: "Ada", AuthorID: "1234567", Collaborator: true, Body: "fix the nil check", Path: "a.go", Line: 3, CreatedAt: t0.Add(2 * time.Minute)},
		{ID: "c0", Kind: gitprov.CommentGeneral, Author: "Linus", AuthorID: "7654321", Body: "early", CreatedAt: t0},
	}
	got, err := p.Comments(ctx, pr.Number)
	if err != nil {
		t.Fatal(err)
	}
	var bodies []string
	for _, c := range got {
		bodies = append(bodies, c.Body)
		if !c.SelfKnown {
			t.Errorf("%q: SelfKnown is false", c.Body)
		}
	}
	want := []string{"legacy report", "early", "first report", "fix the nil check", "second report"}
	if len(bodies) != len(want) {
		t.Fatalf("bodies = %q, want %q", bodies, want)
	}
	for i := range want {
		if bodies[i] != want[i] {
			t.Fatalf("bodies = %q, want %q", bodies, want)
		}
	}
	for _, c := range got {
		self := c.Body == "legacy report" || c.Body == "first report" || c.Body == "second report"
		if c.Self != self {
			t.Errorf("%q: Self = %v", c.Body, c.Self)
		}
		if self && (c.Kind != gitprov.CommentGeneral || c.AuthorID != "fugaro-bot") {
			t.Errorf("Fugaro's comment = %+v", c)
		}
	}
	if got[3].Path != "a.go" || got[3].Line != 3 || got[3].Kind != gitprov.CommentInline {
		t.Errorf("foreign comment lost its fields: %+v", got[3])
	}
	// A comment posted now gets a time, and sorts last.
	if err := p.Comment(ctx, pr, "third report"); err != nil {
		t.Fatal(err)
	}
	if got, _ = p.Comments(ctx, pr.Number); got[len(got)-1].Body != "third report" || got[len(got)-1].CreatedAt.IsZero() {
		t.Fatalf("last comment = %+v", got[len(got)-1])
	}
	p.FailComments = 1
	if _, err := p.Comments(ctx, pr.Number); err == nil {
		t.Fatal("FailComments did not fail")
	}
	if _, err := p.Comments(ctx, pr.Number); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Comments(ctx, 9); err == nil {
		t.Fatal("comments of a missing PR")
	}
}

// TestFakeStateFileBackCompat: a state file written before PR states,
// comment times and visibility existed still loads, as an open PR on a
// private repository.
func TestFakeStateFileBackCompat(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "provider.json")
	old := `{"prs":[{"number":1,"url":"https://example.invalid/pr/1","draft":true,"spec":{"branch":"` + runBranch + `","base":"main","title":"T","body":"B","draft":true},"comments":["report"]}]}`
	// A test may hand-write foreign comments, in snake_case like the rest.
	withForeign := strings.Replace(old, `"comments":["report"]`, `"comments":["report"],"foreign":[{"id":"c1","kind":"inline","author":"Ada","author_id":"1234567","collaborator":true,"path":"a.go","line":3,"body":"fix it","created_at":"2026-01-02T10:00:00Z"}]`, 1)
	fp := filepath.Join(t.TempDir(), "foreign.json")
	if err := os.WriteFile(fp, []byte(withForeign), 0o644); err != nil {
		t.Fatal(err)
	}
	fcs, err := (&Provider{Path: fp}).Comments(ctx, 1)
	if err != nil || len(fcs) != 2 || fcs[1].AuthorID != "1234567" || fcs[1].Kind != gitprov.CommentInline || !fcs[1].Collaborator || fcs[1].Line != 3 || fcs[1].CreatedAt.IsZero() {
		t.Fatalf("hand-written foreign comments = %+v, %v", fcs, err)
	}
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &Provider{Path: path, Repo: "acme/app", SelfID: "fugaro-bot"}
	info, err := p.PullRequest(ctx, 1)
	if err != nil || info.State != gitprov.PROpen || !info.Draft || info.SourceBranch != runBranch || info.SourceRepo != "acme/app" || info.URL != "https://example.invalid/pr/1" {
		t.Fatalf("PullRequest = %+v, %v", info, err)
	}
	if repo, err := p.Repository(ctx); err != nil || !repo.Private {
		t.Fatalf("Repository = %+v, %v", repo, err)
	}
	cs, err := p.Comments(ctx, 1)
	if err != nil || len(cs) != 1 || cs[0].Body != "report" || !cs[0].Self || !cs[0].CreatedAt.IsZero() {
		t.Fatalf("Comments = %+v, %v", cs, err)
	}
	if _, err := p.EnsurePR(ctx, gitprov.PRSpec{Number: 1, Branch: runBranch, Draft: false}); err != nil {
		t.Fatal(err)
	}
	st, err := Load(path)
	if err != nil || len(st.PRs) != 1 || st.PRs[0].Draft || st.PRs[0].Comments[0] != "report" {
		t.Fatalf("state after update = %+v, %v", st, err)
	}
}

func TestFakeRepositoryVisibility(t *testing.T) {
	ctx := context.Background()
	p := &Provider{}
	if r, err := p.Repository(ctx); err != nil || !r.Private {
		t.Fatalf("default Repository = %+v, %v", r, err)
	}
	p.State.Public = true
	if r, err := p.Repository(ctx); err != nil || r.Private {
		t.Fatalf("public Repository = %+v, %v", r, err)
	}
	p.FailRepository = 1
	if _, err := p.Repository(ctx); err == nil {
		t.Fatal("FailRepository did not fail")
	}
	if _, err := p.Repository(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestFakePullRequestHeadFromRemote(t *testing.T) {
	testutil.IsolateGit(t)
	ctx := context.Background()
	remote := testutil.NewRemote(t, map[string]string{"a.txt": "a\n"})
	tip := testutil.Git(t, remote, "rev-parse", "refs/heads/main")
	testutil.Git(t, remote, "update-ref", "refs/heads/"+runBranch, tip)

	p := &Provider{Repo: "acme/app", Remote: remote, SelfID: "fugaro-bot"}
	if _, err := p.EnsurePR(ctx, gitprov.PRSpec{Branch: runBranch, Base: "main", Draft: true}); err != nil {
		t.Fatal(err)
	}
	info, err := p.PullRequest(ctx, 1)
	if err != nil || info.HeadSHA != tip || info.SourceRepo != "acme/app" || info.AuthorID != "fugaro-bot" || info.Number != 1 {
		t.Fatalf("PullRequest = %+v, %v (tip %s)", info, err, tip)
	}
	st := &p.State.PRs[0]
	st.Head = "0123456789ab"
	st.Source = "someone/fork"
	st.AuthorID = "1234567"
	st.State = gitprov.PRMerged
	info, err = p.PullRequest(ctx, 1)
	if err != nil || info.HeadSHA != "0123456789ab" || info.SourceRepo != "someone/fork" || info.AuthorID != "1234567" || info.State != gitprov.PRMerged {
		t.Fatalf("PullRequest with overrides = %+v, %v", info, err)
	}
	st.Head = ""
	testutil.Git(t, remote, "update-ref", "-d", "refs/heads/"+runBranch)
	// A deleted branch (a merge that closed it) still has its PR, as on a
	// real host; the fake just doesn't know its head.
	if info, err := p.PullRequest(ctx, 1); err != nil || info.HeadSHA != "" || info.Number != 1 || info.State != gitprov.PRMerged {
		t.Fatalf("PullRequest of a branch the remote lacks = %+v, %v", info, err)
	}
	// Any other git failure is an error.
	bad := &Provider{Remote: filepath.Join(t.TempDir(), "missing.git"), State: p.State}
	if _, err := bad.PullRequest(ctx, 1); err == nil {
		t.Fatal("PullRequest against a missing remote succeeded")
	}
	p.FailPullRequest = 1
	st.Head = "0123456789ab"
	if _, err := p.PullRequest(ctx, 1); err == nil {
		t.Fatal("FailPullRequest did not fail")
	}
	if _, err := p.PullRequest(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := p.PullRequest(ctx, 2); err == nil {
		t.Fatal("PullRequest of a missing PR")
	}
}
