package runner_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// TestCommitThenStageEndPushedAtTheBoundary: a commit made just before the
// stage ends is pushed at the boundary, at once, even inside the minute.
func TestCommitThenStageEndPushedAtTheBoundary(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ", early_draft: false"))
	pushes := countPushes(t, g.harness)
	var last string
	work := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		commitWIP(t, req, "one")
		g.settle() // pushed: the minute's window starts now
		last = commitWIP(t, req, "two")
		return agent.Result{CostUSD: 1}, nil // the stage ends without another poll
	}
	var atReview string
	var n int
	rec, err := g.run(t, work, probe(func(t *testing.T) { atReview, n = pushedHead(t, g.harness), pushes() }, review("ship", 0)))
	if err != nil {
		t.Fatal(err)
	}
	if atReview != last || n != 2 {
		t.Fatalf("at the review stage: pushed %s after %d pushes, want %s after 2", atReview, n, last)
	}
	if rec.Outcome != runstore.OutcomeDraft || rec.PushedHead != rec.HeadSHA {
		t.Fatalf("rec = %+v", rec)
	}
}

// TestCheckpointOpensDraftMarkedNotVerified: the first checkpoint opens the
// draft, with no reviewers, its number saved, and a section that says the
// work is not verified; finalize makes it ready as before.
func TestCheckpointOpensDraftMarkedNotVerified(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ""))
	var body string
	var draft bool
	var reviewers []string
	var saved *runstore.PRRef
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		commitWIP(t, req, "wip")
		g.settle()
		prs := g.provider.Snapshot().PRs
		if len(prs) != 1 {
			t.Fatalf("PRs after the first checkpoint: %d", len(prs))
		}
		body, draft, reviewers = prs[0].Body, prs[0].Draft, prs[0].Reviewers
		saved = storedPR(t, g.harness)
		return implement("feature")(t, ctx, req)
	}
	rec, err := g.run(t, long, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if !draft || len(reviewers) != 0 || saved == nil || saved.Number != 1 {
		t.Fatalf("mid-stage PR: draft=%v reviewers=%v saved=%+v", draft, reviewers, saved)
	}
	for _, want := range []string{statusBegin, "**Running: work in progress, not verified**", "checkpoint during stage `implement`", "`: not verified"} {
		if !strings.Contains(body, want) {
			t.Errorf("mid-stage body lacks %q:\n%s", want, body)
		}
	}
	if rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v", rec)
	}
	pr := onlyPR(t, g.provider)
	if pr.Draft || !strings.Contains(pr.Body, "**Ready for review**") || strings.Contains(pr.Body, "not verified") {
		t.Fatalf("final PR = %+v", pr)
	}
	if calls := ops(g.harness); calls[0] != "EnsurePR#0 draft=true" || count(calls, "EnsurePR", "") != 2 {
		t.Fatalf("calls = %q", calls)
	}
}

// TestStatusSaysVerifiedAfterAPassingTest: once the pushed commit has a
// passing clean test, the next status write says verified.
func TestStatusSaysVerifiedAfterAPassingTest(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ""))
	var head string
	work := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		head = commitWIP(t, req, "wip")
		g.settle()
		verifyTest(t, ctx, req)
		pr := filepath.Join(envValue(req.Env, "FUGARO_STATE_DIR"), "pr.md")
		if err := os.WriteFile(pr, []byte("# Add wip\n\nAdds wip.txt."), 0o644); err != nil {
			t.Fatal(err)
		}
		return agent.Result{CostUSD: 1}, nil
	}
	var body string
	if _, err := g.run(t, afterStep(g.clock, work), probe(func(t *testing.T) { body = g.provider.Snapshot().PRs[0].Body }, review("ship", 0))); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "**Running** ·") || !strings.Contains(body, fmt.Sprintf("branch at `%s`: verified", head[:7])) || strings.Contains(body, "not verified") {
		t.Fatalf("body at the review stage:\n%s", body)
	}
}

// TestEarlyDraftFalseCheckpointsWithoutPR: early_draft false still pushes
// the branch mid-stage, and the PR opens only at finalize.
func TestEarlyDraftFalseCheckpointsWithoutPR(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ", early_draft: false"))
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		head := commitWIP(t, req, "wip")
		g.settle()
		if pushedHead(t, g.harness) != head || len(g.provider.Snapshot().PRs) != 0 {
			t.Errorf("want the branch pushed and no PR mid-run")
		}
		return implement("feature")(t, ctx, req)
	}
	rec, err := g.run(t, long, review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady || onlyPR(t, g.provider).Draft {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

// TestCheckpointOpenTriedTwice: a draft that fails to open is retried by
// the next checkpoint push only, twice in all; the verified boundary
// opens it after that.
func TestCheckpointOpenTriedTwice(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ""))
	g.provider.FailEnsure = 6 // two tries of ensurePR's three attempts
	ensures := func() int { return count(snapOps(g.harness), "EnsurePR", "") }
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		for i, name := range []string{"one", "two", "three"} {
			head := commitWIP(t, req, name)
			g.settle()
			if pushedHead(t, g.harness) != head {
				t.Fatalf("commit %s was not pushed", name)
			}
			if want := 3 * min(i+1, 2); ensures() != want {
				t.Fatalf("after push %d: %d EnsurePR calls, want %d", i+1, ensures(), want)
			}
		}
		return implement("feature")(t, ctx, req)
	}
	rec, err := g.run(t, long, review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady || len(g.provider.State.PRs) != 1 {
		t.Fatalf("rec = %+v, err = %v, PRs = %+v", rec, err, g.provider.State.PRs)
	}
}

// snapOps is ops read through the provider's lock.
func snapOps(h *harness) []string {
	var out []string
	for _, c := range h.provider.Snapshot().Calls {
		out = append(out, fmt.Sprintf("%s#%d %s", c.Op, c.PR, c.Detail))
	}
	return out
}
