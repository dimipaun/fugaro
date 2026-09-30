//go:build live

// Live checks against a real Bitbucket Cloud sandbox repository
// (docs/git-providers.md, "Live check against a sandbox repository").
// They never run in CI: they need the `live` build tag and a token.
//
//	FUGARO_LIVE_REPO=<owner>/<name> FUGARO_BITBUCKET_TOKEN="$(cat <token-file>)" \
//	  go test -tags live -timeout 600s -run 'TestLive' -v ./internal/gitprov/bitbucket/
//
// A -timeout abort kills the test binary without running t.Cleanup (or the
// TestLiveCleanup that follows), so after one, run -run TestLiveCleanup
// again to decline and delete what was left behind.
//
// Environment:
//   - FUGARO_LIVE_REPO: the sandbox repository, as owner/name (required).
//     Unset, every live test is skipped. It is never defaulted: these tests
//     open pull requests and push branches on that one repository.
//   - FUGARO_BITBUCKET_TOKEN: a repository access token for that repository (required)
//   - FUGARO_LIVE_REVIEWER: the account UUID of a dedicated sandbox account
//     to add as a reviewer (optional). Bitbucket notifies that account, so
//     never use a real person's. Unset, the checks that reviewers survive
//     updates are skipped and the pull requests get no reviewers. The value
//     is never logged.
//   - FUGARO_LIVE_RECORD_DIR: when set, each adapter-level check saves its
//     exchanges there as a fixture file (httpfixture.Recorder)
//
// The repository comes only from FUGARO_LIVE_REPO, and every request and
// clone goes to that one repository: these tests open pull requests and
// push branches, and must never be pointed anywhere but the sandbox. Every
// branch they push is named fugaro/live-*; every pull request they open is
// declined, and every branch deleted, when the test ends (TestLiveCleanup
// sweeps anything a crashed run left behind).
package bitbucket

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/httpfixture"
	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
	"github.com/dimipaun/fugaro/internal/testutil"
)

const (
	liveBase      = "master"
	liveBranchPfx = "fugaro/live-"
)

type live struct {
	t        *testing.T
	ws, slug string // the sandbox, from FUGARO_LIVE_REPO
	token    string
	raw      *httpjson.Client // not recorded: for checking what the adapter did
	stamp    string
	repo     *gitops.Repo
	mu       sync.Mutex
	prs      []int
	branches []string
}

func openLive(t *testing.T) *live {
	t.Helper()
	repo := os.Getenv("FUGARO_LIVE_REPO")
	if repo == "" {
		t.Skip("FUGARO_LIVE_REPO (owner/name of the sandbox repository) is not set: refusing to run live tests without an explicit target")
	}
	ws, slug, ok := strings.Cut(repo, "/")
	if !ok || ws == "" || slug == "" || strings.Contains(slug, "/") {
		t.Skipf("FUGARO_LIVE_REPO=%q is not owner/name: refusing to run", repo)
	}
	token := os.Getenv("FUGARO_BITBUCKET_TOKEN")
	if token == "" {
		t.Skip("FUGARO_BITBUCKET_TOKEN is not set")
	}
	return &live{t: t, ws: ws, slug: slug, token: token, stamp: time.Now().UTC().Format("20060102-150405"),
		raw: &httpjson.Client{BaseURL: DefaultBaseURL, Header: http.Header{"Accept": {"application/json"}},
			Auth: func(context.Context) (string, error) { return "Bearer " + token, nil }}}
}

// flipFirst changes the case of s's first letter, for a mixed-case spelling
// of the sandbox's name.
func flipFirst(s string) string {
	if s == "" {
		return s
	}
	if f := strings.ToUpper(s[:1]); f != s[:1] {
		return f + s[1:]
	}
	return strings.ToLower(s[:1]) + s[1:]
}

func (l *live) repoPath(suffix string) string {
	return "/repositories/" + l.ws + "/" + l.slug + suffix
}

// scrub removes the token from s, for anything the test logs.
func (l *live) scrub(s string) string { return strings.ReplaceAll(s, l.token, "[TOKEN]") }

func (l *live) fact(format string, args ...any) {
	l.t.Helper()
	l.t.Log(l.scrub("FACT: " + fmt.Sprintf(format, args...)))
}

// provider returns an adapter for ws/slug whose exchanges are saved as
// fixture <name>.json when FUGARO_LIVE_RECORD_DIR is set.
func (l *live) provider(t *testing.T, name, ws, slug, token string, warn func(string)) *Provider {
	t.Helper()
	rec := &httpfixture.Recorder{Secrets: []string{l.token, token}}
	p, err := New(Options{Workspace: ws, Slug: slug, Token: token, HTTP: &http.Client{Transport: rec, Timeout: 30 * time.Second}, Warn: warn})
	if err != nil {
		t.Fatal(err)
	}
	if dir := os.Getenv("FUGARO_LIVE_RECORD_DIR"); dir != "" {
		t.Cleanup(func() {
			path := filepath.Join(dir, name+".json")
			if err := rec.Save(path); err != nil {
				t.Errorf("saving %s: %v", path, err)
				return
			}
			data, _ := os.ReadFile(path)
			if strings.Contains(string(data), l.token) {
				os.Remove(path)
				t.Errorf("%s held the token: removed", path)
			}
		})
	}
	return p
}

// clone makes a checkout of the sandbox authenticated the runner's way:
// the token only in the environment, answered by the credential helper.
func (l *live) clone(ctx context.Context) {
	t := l.t
	vars, err := gitops.CredentialVars("https://bitbucket.org", "x-token-auth", l.token)
	if err != nil {
		t.Fatal(err)
	}
	env := gitops.WithVars(gitops.IdentityEnv(), vars)
	dir := filepath.Join(t.TempDir(), "sandbox")
	repo, err := gitops.OpenOrClone(ctx, dir, "https://bitbucket.org/"+l.ws+"/"+l.slug+".git", env)
	if err != nil {
		t.Fatal(l.scrub(err.Error()))
	}
	l.repo = repo
	if data, _ := os.ReadFile(filepath.Join(dir, ".git", "config")); strings.Contains(string(data), l.token) {
		t.Fatal(".git/config holds the token")
	}
	l.fact("git clone over HTTPS with the env-only credential helper works; .git/config holds no token")
}

// push creates branch fugaro/live-<stamp>-<name> off the base with one new
// file, and pushes it with gitops.Push (the runner's lease-based push).
func (l *live) push(ctx context.Context, t *testing.T, name string) string {
	t.Helper()
	branch := liveBranchPfx + l.stamp + "-" + name
	if err := l.repo.CheckoutNewBranch(ctx, liveBase, branch); err != nil {
		t.Fatal(l.scrub(err.Error()))
	}
	file := filepath.Join(l.repo.Dir, "live-"+name+".txt")
	if err := os.WriteFile(file, []byte("Fugaro live check "+l.stamp+" "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := l.repo.CommitAll(ctx, "Fugaro live check: "+name); err != nil {
		t.Fatal(l.scrub(err.Error()))
	}
	l.mu.Lock()
	l.branches = append(l.branches, branch)
	l.mu.Unlock()
	if err := l.repo.Push(ctx, branch); err != nil {
		t.Fatal(l.scrub(err.Error()))
	}
	return branch
}

func (l *live) track(id int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, x := range l.prs {
		if x == id {
			return
		}
	}
	l.prs = append(l.prs, id)
}

type livePR struct {
	ID          int    `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Draft       *bool  `json:"draft"`
	State       string `json:"state"`
	Reviewers   []struct {
		UUID string `json:"uuid"`
	} `json:"reviewers"`
	CloseSourceBranch bool `json:"close_source_branch"`
}

func (p livePR) draft() string {
	if p.Draft == nil {
		return "absent"
	}
	return fmt.Sprint(*p.Draft)
}

func (l *live) get(ctx context.Context, t *testing.T, id int) livePR {
	t.Helper()
	var pr livePR
	if err := l.raw.Do(ctx, "GET", l.repoPath(fmt.Sprintf("/pullrequests/%d", id)), nil, &pr); err != nil {
		t.Fatal(l.scrub(err.Error()))
	}
	return pr
}

func (l *live) openFor(ctx context.Context, t *testing.T, branch string) int {
	t.Helper()
	q := url.Values{"q": {fmt.Sprintf(`source.branch.name="%s" AND state="OPEN"`, branch)}}
	var page struct {
		Values []struct{ ID int } `json:"values"`
	}
	if err := l.raw.Do(ctx, "GET", l.repoPath("/pullrequests?"+q.Encode()), nil, &page); err != nil {
		t.Fatal(l.scrub(err.Error()))
	}
	return len(page.Values)
}

// cleanup declines every pull request and deletes every branch this run
// made, logging each.
func (l *live) cleanup(ctx context.Context) {
	l.mu.Lock()
	prs, branches := append([]int(nil), l.prs...), append([]string(nil), l.branches...)
	l.mu.Unlock()
	for _, id := range prs {
		err := l.raw.Do(ctx, "POST", l.repoPath(fmt.Sprintf("/pullrequests/%d/decline", id)), nil, nil)
		l.t.Logf("CLEANUP: decline PR #%d: %v", id, errText(l, err))
	}
	for _, b := range branches {
		err := l.raw.Do(ctx, "DELETE", l.repoPath("/refs/branches/"+url.PathEscape(b)), nil, nil)
		l.t.Logf("CLEANUP: delete branch %s: %v", b, errText(l, err))
	}
}

func errText(l *live, err error) string {
	if err == nil {
		return "ok"
	}
	return l.scrub(err.Error())
}

func TestLiveBitbucket(t *testing.T) {
	testutil.IsolateGit(t)
	l := openLive(t)
	ctx := context.Background()
	t.Cleanup(func() { l.cleanup(context.Background()) })
	l.clone(ctx)
	const body = "Live check description.\n\nSecond paragraph, which must survive every update."
	// Reviewers only when a dedicated sandbox account is named: adding
	// one notifies it.
	var reviewers []string
	if r := os.Getenv("FUGARO_LIVE_REVIEWER"); r != "" {
		reviewers = []string{r}
	} else {
		t.Log("FUGARO_LIVE_REVIEWER is unset: the reviewer-preservation checks are skipped")
	}
	// checkKept fails t unless got still has the description and reviewer
	// count the pull request was created with.
	checkKept := func(t *testing.T, what string, got livePR) {
		t.Helper()
		if got.Description != body {
			t.Errorf("%s changed the description to %q", what, got.Description)
		}
		if reviewers == nil {
			t.Logf("%s: reviewer check skipped (FUGARO_LIVE_REVIEWER unset)", what)
		} else if len(got.Reviewers) != len(reviewers) {
			t.Errorf("%s left %d reviewers, want %d", what, len(got.Reviewers), len(reviewers))
		}
	}

	draftBranch := l.push(ctx, t, "draft")
	draftHead, err := l.repo.HeadSHA(ctx)
	if err != nil {
		t.Fatal(l.scrub(err.Error()))
	}
	mkSpec := func(branch string, draft bool) gitprov.PRSpec {
		return gitprov.PRSpec{Branch: branch, Base: liveBase, Title: "Fugaro live check " + l.stamp, Body: body,
			Draft: draft, Reviewers: reviewers}
	}

	// Draft on create, with a description (and a reviewer, when named).
	var id int
	t.Run("create_draft", func(t *testing.T) {
		p := l.provider(t, "live_create_draft", l.ws, l.slug, l.token, nil)
		pr, err := p.EnsurePR(ctx, mkSpec(draftBranch, true))
		if pr.Number != 0 {
			l.track(pr.Number)
			id = pr.Number
		}
		if err != nil {
			t.Fatal(l.scrub(err.Error()))
		}
		got := l.get(ctx, t, pr.Number)
		l.fact("create with draft:true -> GET draft=%s title=%q reviewers=%d description kept=%v close_source_branch=%v; adapter reports Draft=%v",
			got.draft(), got.Title, len(got.Reviewers), got.Description == body, got.CloseSourceBranch, pr.Draft)
		if !pr.Draft {
			t.Errorf("adapter returned a non-draft PR: %+v", pr)
		}
		checkKept(t, "POST with draft:true", got)
		if got.Draft == nil || !*got.Draft {
			l.fact("Bitbucket IGNORED draft:true on create; the [DRAFT] title fallback is in effect (title %q)", got.Title)
		}
	})
	if id == 0 {
		t.Fatal("no pull request to continue with")
	}

	// Title-only PUT, with no draft field, on a draft PR: does it clear
	// draft, reviewers or description? (Why the adapter's retitle sends
	// draft along.)
	t.Run("put_title_only", func(t *testing.T) {
		before := l.get(ctx, t, id)
		var after livePR
		if err := l.raw.Do(ctx, "PUT", l.repoPath(fmt.Sprintf("/pullrequests/%d", id)), map[string]any{"title": before.Title + " (retitled)"}, &after); err != nil {
			t.Fatal(l.scrub(err.Error()))
		}
		got := l.get(ctx, t, id)
		l.fact("PUT {title} only on a draft=%s PR -> PUT response draft=%s; GET draft=%s title=%q reviewers %d->%d description kept=%v",
			before.draft(), after.draft(), got.draft(), got.Title, len(before.Reviewers), len(got.Reviewers), got.Description == body)
		// A PUT is a partial update (docs/git-providers.md, design §15):
		// the fields it leaves out must all be kept.
		if got.draft() != before.draft() {
			t.Errorf("PUT {title} changed draft from %s to %s", before.draft(), got.draft())
		}
		if len(got.Reviewers) != len(before.Reviewers) {
			t.Errorf("PUT {title} changed the reviewers from %d to %d", len(before.Reviewers), len(got.Reviewers))
		}
		if got.Description != body {
			t.Errorf("PUT {title} changed the description to %q", got.Description)
		}
		// Put it back as it was.
		restore := map[string]any{"title": before.Title}
		if before.Draft != nil {
			restore["draft"] = *before.Draft
		}
		if err := l.raw.Do(ctx, "PUT", l.repoPath(fmt.Sprintf("/pullrequests/%d", id)), restore, nil); err != nil {
			t.Fatal(l.scrub(err.Error()))
		}
		got = l.get(ctx, t, id)
		l.fact("restored with PUT %v -> GET draft=%s title=%q", restore, got.draft(), got.Title)
	})

	// Draft -> ready on update: PUT {title, draft:false}.
	t.Run("existing_to_ready", func(t *testing.T) {
		p := l.provider(t, "live_existing_to_ready", l.ws, l.slug, l.token, nil)
		pr, err := p.EnsurePR(ctx, mkSpec(draftBranch, false))
		if err != nil || pr.Number != id || pr.Draft {
			t.Fatalf("pr = %+v, err = %v", pr, l.scrub(fmt.Sprint(err)))
		}
		got := l.get(ctx, t, id)
		l.fact("update draft->ready with PUT {title, draft:false} -> GET draft=%s title=%q reviewers=%d description kept=%v",
			got.draft(), got.Title, len(got.Reviewers), got.Description == body)
		if got.Draft != nil && *got.Draft {
			t.Error("still a draft after draft:false")
		}
		checkKept(t, "PUT {title, draft:false}", got)
	})

	// Ready -> draft on update: PUT {title, draft:true}.
	t.Run("existing_to_draft", func(t *testing.T) {
		p := l.provider(t, "live_existing_to_draft", l.ws, l.slug, l.token, nil)
		pr, err := p.EnsurePR(ctx, mkSpec(draftBranch, true))
		if err != nil || pr.Number != id || !pr.Draft {
			t.Fatalf("pr = %+v, err = %v", pr, l.scrub(fmt.Sprint(err)))
		}
		got := l.get(ctx, t, id)
		l.fact("update ready->draft with PUT {title, draft:true} -> GET draft=%s title=%q reviewers=%d description kept=%v",
			got.draft(), got.Title, len(got.Reviewers), got.Description == body)
		checkKept(t, "PUT {title, draft:true}", got)
	})

	// Already a draft, draft wanted: no PUT at all.
	t.Run("existing_no_op", func(t *testing.T) {
		p := l.provider(t, "live_existing_no_op", l.ws, l.slug, l.token, nil)
		pr, err := p.EnsurePR(ctx, mkSpec(draftBranch, true))
		if err != nil || pr.Number != id || !pr.Draft {
			t.Fatalf("pr = %+v, err = %v", pr, l.scrub(fmt.Sprint(err)))
		}
	})

	// Mixed-case workspace and slug find the same PR; no duplicate.
	t.Run("mixed_case", func(t *testing.T) {
		p := l.provider(t, "live_mixed_case", flipFirst(l.ws), flipFirst(l.slug), l.token, nil)
		pr, err := p.EnsurePR(ctx, mkSpec(draftBranch, false))
		if pr.Number != 0 {
			l.track(pr.Number)
		}
		if err != nil {
			t.Fatal(l.scrub(err.Error()))
		}
		n := l.openFor(ctx, t, draftBranch)
		l.fact("mixed-case %s/%s: found PR #%d (want #%d), open PRs for the branch = %d, draft=%v",
			flipFirst(l.ws), flipFirst(l.slug), pr.Number, id, n, pr.Draft)
		if pr.Number != id || n != 1 {
			t.Errorf("mixed case opened a second PR or missed the first")
		}
	})

	// Comment posting.
	t.Run("comment", func(t *testing.T) {
		p := l.provider(t, "live_comment", l.ws, l.slug, l.token, nil)
		const text = "### Fugaro live check\n\nA **markdown** comment."
		if err := p.Comment(ctx, gitprov.PR{Number: id}, text); err != nil {
			t.Fatal(l.scrub(err.Error()))
		}
		var page struct {
			Values []struct {
				Content struct {
					Raw string `json:"raw"`
				} `json:"content"`
			} `json:"values"`
		}
		if err := l.raw.Do(ctx, "GET", l.repoPath(fmt.Sprintf("/pullrequests/%d/comments", id)), nil, &page); err != nil {
			t.Fatal(l.scrub(err.Error()))
		}
		found := false
		for _, c := range page.Values {
			found = found || c.Content.Raw == text
		}
		l.fact("comment posted: found on GET comments = %v", found)
		if !found {
			t.Error("comment not found")
		}
	})

	// The follow-up reads: a general comment, an inline one with a reply,
	// the inline thread resolved if the API allows, then the repository,
	// the pull request and its comments read back through the adapter.
	t.Run("follow_up_reads", func(t *testing.T) { l.followUpReads(ctx, t, id, draftBranch, draftHead) })

	// An update by number changes the draft state only.
	t.Run("ensure_by_number", func(t *testing.T) {
		p := l.provider(t, "live_ensure_by_number", l.ws, l.slug, l.token, nil)
		pr, err := p.EnsurePR(ctx, gitprov.PRSpec{Number: id, Branch: draftBranch, Title: "must not be sent", Body: "must not be sent", Draft: true})
		if err != nil || pr.Number != id || !pr.Draft {
			t.Fatalf("pr = %+v, err = %v", pr, l.scrub(fmt.Sprint(err)))
		}
		got := l.get(ctx, t, id)
		l.fact("EnsurePR by number (draft) -> GET draft=%s title=%q description kept=%v", got.draft(), got.Title, got.Description == body)
		if got.Description != body || strings.Contains(got.Title, "must not be sent") {
			t.Errorf("an update by number changed the title or description")
		}
	})

	// Ready on create, and the labels warning once across two PRs.
	t.Run("labels_warning", func(t *testing.T) {
		a, b := l.push(ctx, t, "labels-a"), l.push(ctx, t, "labels-b")
		var mu sync.Mutex
		var warnings []string
		p := l.provider(t, "live_labels_warning", l.ws, l.slug, l.token, func(m string) {
			mu.Lock()
			defer mu.Unlock()
			warnings = append(warnings, m)
		})
		for _, br := range []string{a, b} {
			pr, err := p.EnsurePR(ctx, gitprov.PRSpec{Branch: br, Base: liveBase, Title: "Fugaro live check " + l.stamp + " labels",
				Body: "Labels check.", Labels: []string{"fugaro"}})
			if pr.Number != 0 {
				l.track(pr.Number)
			}
			if err != nil {
				t.Fatal(l.scrub(err.Error()))
			}
			got := l.get(ctx, t, pr.Number)
			l.fact("create with draft:false -> GET draft=%s", got.draft())
		}
		l.fact("labels warnings across two EnsurePR calls = %d: %q", len(warnings), warnings)
		if len(warnings) != 1 {
			t.Errorf("warnings = %q, want one", warnings)
		}
	})

	// An unknown reviewer: the PR is still opened, without reviewers.
	t.Run("reviewer_rejected", func(t *testing.T) {
		br := l.push(ctx, t, "reviewer-rejected")
		p := l.provider(t, "live_reviewer_rejected", l.ws, l.slug, l.token, nil)
		pr, err := p.EnsurePR(ctx, gitprov.PRSpec{Branch: br, Base: liveBase, Title: "Fugaro live check " + l.stamp + " reviewer",
			Body: "Reviewer check.", Reviewers: []string{"{00000000-0000-4000-8000-000000000000}"}})
		if pr.Number != 0 {
			l.track(pr.Number)
		}
		var partial *gitprov.PartialError
		l.fact("unknown reviewer -> PR #%d, partial=%v, err=%v", pr.Number, errors.As(err, &partial), errText(l, err))
		if pr.Number == 0 || !errors.As(err, &partial) {
			t.Errorf("want the PR opened without reviewers and a *PartialError")
		}
	})

	// A declined pull request reads as closed, and an update by number
	// refuses it rather than bringing it back or opening another.
	t.Run("ensure_by_number_declined", func(t *testing.T) {
		if err := l.raw.Do(ctx, "POST", l.repoPath(fmt.Sprintf("/pullrequests/%d/decline", id)), nil, nil); err != nil {
			t.Fatal(l.scrub(err.Error()))
		}
		p := l.provider(t, "live_ensure_by_number_declined", l.ws, l.slug, l.token, nil)
		info, err := p.PullRequest(ctx, id)
		l.fact("declined PR #%d -> PullRequest state=%s err=%v", id, info.State, errText(l, err))
		if err != nil || info.State != gitprov.PRClosed {
			t.Errorf("state = %q, want %q", info.State, gitprov.PRClosed)
		}
		pr, err := p.EnsurePR(ctx, gitprov.PRSpec{Number: id, Branch: draftBranch, Draft: false})
		n := l.openFor(ctx, t, draftBranch)
		l.fact("EnsurePR by number on the declined PR -> pr=%+v err=%v; open PRs for the branch = %d", pr, errText(l, err), n)
		if !errors.Is(err, gitprov.ErrPRNotOpen) || n != 0 {
			t.Errorf("want ErrPRNotOpen and no open pull request")
		}
	})

	// A bad token: HTTP 401, and the token never appears in the error.
	t.Run("unauthorized", func(t *testing.T) {
		p := l.provider(t, "live_unauthorized", l.ws, l.slug, "not-a-real-token-0000", nil)
		_, err := p.EnsurePR(ctx, mkSpec(draftBranch, false))
		l.fact("bad token -> %v", errText(l, err))
		if err == nil || !strings.Contains(err.Error(), "HTTP 401") {
			t.Errorf("err = %v, want HTTP 401", err)
		}
	})
}

// postComment posts a comment on pull request id with the raw client (as
// the token's user), returning its ID.
func (l *live) postComment(ctx context.Context, t *testing.T, id int, in map[string]any) int {
	t.Helper()
	var out struct {
		ID int `json:"id"`
	}
	if err := l.raw.Do(ctx, "POST", l.repoPath(fmt.Sprintf("/pullrequests/%d/comments", id)), in, &out); err != nil {
		t.Fatal(l.scrub(err.Error()))
	}
	return out.ID
}

// followUpReads posts the comments a follow-up reads (see the live*Body
// constants), then reads the repository, pull request id and its comments
// through the adapter, recording them as live_follow_up_reads.json.
func (l *live) followUpReads(ctx context.Context, t *testing.T, id int, branch, head string) {
	general := l.postComment(ctx, t, id, map[string]any{"content": map[string]string{"raw": liveGeneralBody}})
	inlineID := l.postComment(ctx, t, id, map[string]any{"content": map[string]string{"raw": liveInlineBody},
		"inline": map[string]any{"path": liveInlinePath, "to": 1}})
	reply := l.postComment(ctx, t, id, map[string]any{"content": map[string]string{"raw": liveReplyBody},
		"parent": map[string]int{"id": inlineID}})
	resolveErr := l.raw.Do(ctx, "POST", l.repoPath(fmt.Sprintf("/pullrequests/%d/comments/%d/resolve", id, inlineID)), nil, nil)
	resolved := resolveErr == nil
	l.fact("posted general #%d, inline #%d, reply #%d; resolving the inline thread (POST …/comments/%d/resolve) -> %s",
		general, inlineID, reply, inlineID, errText(l, resolveErr))

	var warnings []string
	p := l.provider(t, "live_follow_up_reads", l.ws, l.slug, l.token, func(m string) { warnings = append(warnings, m) })
	repo, err := p.Repository(ctx)
	l.fact("Repository -> private=%v err=%v", repo.Private, errText(l, err))
	if err != nil || !repo.Private {
		t.Errorf("the sandbox must read as private (is_private)")
	}
	info, err := p.PullRequest(ctx, id)
	if err != nil {
		t.Fatal(l.scrub(err.Error()))
	}
	l.fact("PullRequest #%d -> state=%s draft=%v author.account_id=%q source=%s repo=%s head=%q (SameCommit with the pushed head: %v)",
		id, info.State, info.Draft, info.AuthorID, info.SourceBranch, info.SourceRepo, info.HeadSHA, gitprov.SameCommit(info.HeadSHA, head))
	if info.State != gitprov.PROpen || info.SourceBranch != branch || info.AuthorID == "" || !strings.EqualFold(info.SourceRepo, l.ws+"/"+l.slug) {
		t.Errorf("PullRequest = %+v", info)
	}
	comments, err := p.Comments(ctx, id)
	if err != nil {
		t.Fatal(l.scrub(err.Error()))
	}
	l.fact("GET /user with the repository access token: warnings=%q", warnings)
	byBody := map[string]gitprov.Comment{}
	for _, c := range comments {
		byBody[c.Body] = c
		l.fact("comment %s kind=%s author=%q author_id=%q (PR author: %v) self=%v self_known=%v resolved=%v outdated=%v deleted=%v path=%q line=%d",
			c.ID, c.Kind, c.Author, c.AuthorID, c.AuthorID == info.AuthorID, c.Self, c.SelfKnown, c.Resolved, c.Outdated, c.Deleted, c.Path, c.Line)
	}
	g, ok := byBody[liveGeneralBody]
	l.fact("the raw content keeps <!-- … -->: %v", ok)
	if !ok || g.Kind != gitprov.CommentGeneral {
		t.Errorf("the general comment is missing or changed: %+v", g)
	}
	for _, body := range []string{liveInlineBody, liveReplyBody} {
		c, ok := byBody[body]
		if !ok || c.Kind != gitprov.CommentInline || c.Path != liveInlinePath || (resolved && !c.Resolved) {
			t.Errorf("inline comment %q = %+v (found %v, thread resolved %v)", body, c, ok, resolved)
		}
	}
}

// TestLiveCleanup declines every open pull request from a fugaro/live-*
// branch in the sandbox, and deletes every fugaro/live-* branch: a sweep
// for anything a crashed TestLiveBitbucket left behind. It reads only the
// first page of each listing (50 pull requests, 100 branches), which is
// plenty for the sandbox; re-run it if a sweep ever finds a full page.
func TestLiveCleanup(t *testing.T) {
	l := openLive(t)
	ctx := context.Background()
	q := url.Values{"q": {`source.branch.name ~ "` + liveBranchPfx + `" AND state="OPEN"`}, "pagelen": {"50"}}
	var prs struct {
		Values []struct {
			ID     int `json:"id"`
			Source struct {
				Branch struct {
					Name string `json:"name"`
				} `json:"branch"`
			} `json:"source"`
		} `json:"values"`
	}
	if err := l.raw.Do(ctx, "GET", l.repoPath("/pullrequests?"+q.Encode()), nil, &prs); err != nil {
		t.Fatal(l.scrub(err.Error()))
	}
	for _, pr := range prs.Values {
		if strings.HasPrefix(pr.Source.Branch.Name, liveBranchPfx) {
			l.prs = append(l.prs, pr.ID)
		}
	}
	bq := url.Values{"q": {`name ~ "` + liveBranchPfx + `"`}, "pagelen": {"100"}}
	var brs struct {
		Values []struct {
			Name string `json:"name"`
		} `json:"values"`
	}
	if err := l.raw.Do(ctx, "GET", l.repoPath("/refs/branches?"+bq.Encode()), nil, &brs); err != nil {
		t.Fatal(l.scrub(err.Error()))
	}
	for _, b := range brs.Values {
		if strings.HasPrefix(b.Name, liveBranchPfx) {
			l.branches = append(l.branches, b.Name)
		}
	}
	// FUGARO_LIVE_SWEEP_BRANCHES names further run branches (from a
	// `fugaro exec` live run, fugaro/<run-id>) to sweep, comma-separated.
	for _, b := range strings.Split(os.Getenv("FUGARO_LIVE_SWEEP_BRANCHES"), ",") {
		if b = strings.TrimSpace(b); b == "" {
			continue
		}
		if !strings.HasPrefix(b, gitops.RunBranchPrefix) {
			t.Fatalf("refusing to sweep %q: not a fugaro/ branch", b)
		}
		l.branches = append(l.branches, b)
		oq := url.Values{"q": {fmt.Sprintf(`source.branch.name="%s" AND state="OPEN"`, b)}}
		var open struct {
			Values []struct{ ID int } `json:"values"`
		}
		if err := l.raw.Do(ctx, "GET", l.repoPath("/pullrequests?"+oq.Encode()), nil, &open); err != nil {
			t.Fatal(l.scrub(err.Error()))
		}
		for _, pr := range open.Values {
			l.prs = append(l.prs, pr.ID)
		}
	}
	t.Logf("sweeping PRs %v and branches %v", l.prs, l.branches)
	l.cleanup(ctx)
}

// TestLiveInspect logs the state of the sandbox pull requests named in
// FUGARO_LIVE_INSPECT_PRS (comma-separated numbers), for checking what a
// live `fugaro exec` run left: draft state, title, how many open pull
// requests share its branch, and its comments.
func TestLiveInspect(t *testing.T) {
	l := openLive(t)
	ctx := context.Background()
	for _, s := range strings.Split(os.Getenv("FUGARO_LIVE_INSPECT_PRS"), ",") {
		var id int
		if _, err := fmt.Sscan(strings.TrimSpace(s), &id); err != nil {
			continue
		}
		var pr struct {
			livePR
			Author struct {
				AccountID string `json:"account_id"`
			} `json:"author"`
			Source struct {
				Branch struct {
					Name string `json:"name"`
				} `json:"branch"`
			} `json:"source"`
		}
		if err := l.raw.Do(ctx, "GET", l.repoPath(fmt.Sprintf("/pullrequests/%d", id)), nil, &pr); err != nil {
			t.Fatal(l.scrub(err.Error()))
		}
		var page struct {
			Values []struct {
				Content struct {
					Raw string `json:"raw"`
				} `json:"content"`
				User *struct {
					AccountID   string `json:"account_id"`
					DisplayName string `json:"display_name"`
				} `json:"user"`
			} `json:"values"`
		}
		if err := l.raw.Do(ctx, "GET", l.repoPath(fmt.Sprintf("/pullrequests/%d/comments", id)), nil, &page); err != nil {
			t.Fatal(l.scrub(err.Error()))
		}
		var heads []string
		for _, c := range page.Values {
			head, _, _ := strings.Cut(c.Content.Raw, "\n")
			heads = append(heads, head)
		}
		l.fact("PR #%d branch=%s state=%s draft=%s title=%q author.account_id=%q open PRs for the branch=%d comments=%d %q",
			id, pr.Source.Branch.Name, pr.State, pr.draft(), pr.Title, pr.Author.AccountID, l.openFor(ctx, t, pr.Source.Branch.Name), len(page.Values), heads)
		// Account IDs are not secrets: they are what followup.trusted lists.
		for i, c := range page.Values {
			if c.User != nil {
				l.fact("PR #%d comment %d: user.account_id=%q display_name=%q", id, i+1, c.User.AccountID, c.User.DisplayName)
			}
		}
	}
}
