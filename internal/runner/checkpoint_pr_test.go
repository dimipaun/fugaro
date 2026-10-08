package runner_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/runner"
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
// the next checkpoint push only, twice in all; the verified boundary opens
// it after that.
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

// mountSecret makes value a mounted secret of the run.
func mountSecret(g *ckptRig, value string) {
	g.deps.Env = append(g.deps.Env, "RENAMED_TOKEN="+value, runner.SecretEnvsVar+"=RENAMED_TOKEN")
}

// TestBoundaryPushHoldsBackAWorkflowCommit: a workflow change committed as
// the last act of an unverified stage, with no poll after it, is caught by
// the boundary push alone: nothing reaches the remote, checkpoints stop, a
// warning says so, and the checkpoint does not fail the run (finalize's own
// refusal ends it, as it always did).
func TestBoundaryPushHoldsBackAWorkflowCommit(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ""))
	ci := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "mkdir -p .github/workflows && echo 'on: push' > .github/workflows/ci.yml && git add -A && git commit -qm 'Add CI'")
		return agent.Result{CostUSD: 1}, nil // no poll, no verify
	}
	var atReview bool
	var stopped int
	check := func(t *testing.T) {
		atReview = remoteHasBranch(t, g.harness)
		stopped = strings.Count(g.logs.String(), "no more checkpoint pushes")
	}
	runRefused(t, g.harness, ci, probe(check, review("ship", 0)))
	if atReview {
		t.Error("the boundary pushed a workflow change")
	}
	if stopped != 1 || !strings.Contains(g.logs.String(), "reason=") {
		t.Fatalf("%d stop notices at the review stage, want 1:\n%s", stopped, g.logs)
	}
}

// TestBoundaryPushHoldsBackASecret: a commit holding the mounted secret,
// made as the last act of a stage with no poll after it, is not pushed by
// the boundary; checkpoints stop and the value is not in the logs.
func TestBoundaryPushHoldsBackASecret(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ""))
	const mounted = "mounted-boundary-secret-value"
	mountSecret(g, mounted)
	leak := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo "+mounted+" > leak.txt && git add -A && git commit -qm leak")
		return agent.Result{CostUSD: 1}, nil
	}
	var atReview bool
	var stopped int
	check := func(t *testing.T) {
		atReview = remoteHasBranch(t, g.harness)
		stopped = strings.Count(g.logs.String(), "no more checkpoint pushes")
	}
	if _, err := g.run(t, leak, probe(check, review("ship", 0))); err != nil {
		t.Fatal(err)
	}
	if atReview {
		t.Error("the boundary pushed a commit holding a secret")
	}
	if stopped != 1 || strings.Contains(g.logs.String(), mounted) {
		t.Fatalf("%d stop notices, want 1; logs:\n%s", stopped, g.logs)
	}
}

// TestStatusSectionIsRedacted: a secret value in the status section's text
// (here the words "not verified", which the section carries) never reaches
// the PR body, neither in the section the draft opens with nor in the one
// the next checkpoint push rewrites.
func TestStatusSectionIsRedacted(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ""))
	mountSecret(g, "not verified")
	var first, second string
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		commitWIP(t, req, "one")
		g.settle()
		first = g.provider.Snapshot().PRs[0].Body
		commitWIP(t, req, "two")
		g.settle()
		second = g.provider.Snapshot().PRs[0].Body
		return implement("feature")(t, ctx, req)
	}
	if _, err := g.run(t, long, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"opening": first, "rewritten": second} {
		if !strings.Contains(body, statusBegin) || !strings.Contains(body, "REDACTED") || strings.Contains(body, "not verified") {
			t.Errorf("%s section:\n%s", name, body)
		}
	}
	if first == second {
		t.Error("the second checkpoint did not rewrite the section")
	}
}

// TestNextCheckpointRewritesTheStatusSection: with the draft open, the next
// checkpoint push brings the section up to the new pushed commit.
func TestNextCheckpointRewritesTheStatusSection(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ""))
	var h1, h2, body string
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		h1 = commitWIP(t, req, "one")
		g.settle()
		h2 = commitWIP(t, req, "two")
		g.settle()
		body = g.provider.Snapshot().PRs[0].Body
		return implement("feature")(t, ctx, req)
	}
	if _, err := g.run(t, long, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "branch at `"+h2[:7]+"`") || strings.Contains(body, h1[:7]) {
		t.Fatalf("body after the second checkpoint (first %s, second %s):\n%s", h1[:7], h2[:7], body)
	}
}

// TestNoBoundaryPushAfterHaltOrCancel: a commit left at the end of a stage
// whose run is halted or cancelled is not pushed by the boundary; finalize
// pushes as it does for any halt, and no checkpoint is logged. A halted
// stage returns before afterStage, so the halt case pins that behaviour but
// does not exercise checkpointBlocked; the cancel case does.
func TestNoBoundaryPushAfterHaltOrCancel(t *testing.T) {
	for _, c := range []string{"halt", "cancel"} {
		t.Run(c, func(t *testing.T) {
			b := newHandleBox(t)
			g := newCkptRig(t, prCfg(t, 2, ", early_draft: false"))
			end := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
				commitWIP(t, req, "wip")
				if c == "halt" && !b.h.HaltNow(runCapHalt) || c == "cancel" && !b.h.MarkCancelled() {
					t.Fatal("the halt or cancel was refused")
				}
				return agent.Result{CostUSD: 1}, nil
			}
			steps := []step{end}
			if c == "cancel" {
				steps = append(steps, review("ship", 0)) // a mark alone leaves the run going
			}
			if _, err := g.run(t, steps...); err != nil {
				t.Log(err)
			}
			if strings.Contains(g.logs.String(), "checkpoint pushed") {
				t.Fatalf("a checkpoint pushed after the %s:\n%s", c, g.logs)
			}
		})
	}
}
