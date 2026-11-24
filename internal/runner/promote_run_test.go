package runner_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/backlog"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// promoteItem is one item of the promote fixture's folder — the area's own
// recorded debt, in the shape docs/backlog/FORMAT.md states and that a promote
// pass reads.
func promoteItem(status string) string {
	return "---\nid: 20261118-01\ntitle: no retry on the charge call\nlevel: startup\nkind: deferred\n" +
		"area: payments\nstatus: " + status + "\nfiled-by: dimipaun\nfiled-in: run:20261117-090000-abcd\ndate: 2026-11-18\n---\n\n" +
		"## What was left open\n\nNo retry on `internal/payments/charge.go`.\n\n## Why\n\nThe gateway is flaky.\n"
}

// promoteAreaCfg is the fixture's fugaro.yaml with the payments area rule
// above its agent block: the area every promote test here raises.
func promoteAreaCfg(t *testing.T) string {
	t.Helper()
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"], "agent:\n",
		"rigor:\n  default: poc\n  areas:\n    - { name: payments, match: \"internal/payments/**\", mode: startup }\nagent:\n", 1)
	if !strings.Contains(cfg, "name: payments") {
		t.Fatal("the fixture's config names no agent block to put the area rule above")
	}
	return cfg
}

// promoteAreaPaths is the area's declared paths, the ones a promote launch
// resolves the level from and the run declares.
func promoteAreaPaths() []string { return []string{"internal/payments/**"} }

// promoteTask is the task a promote run of the payments area runs: the block
// the launcher writes, its area's paths declared, and the level asked for.
func promoteTask() *task.Spec {
	paths := promoteAreaPaths()
	return &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Workflow: "app",
		Task: "Raise the payments area to startup.", Rigor: "startup", Paths: paths,
		Promote: &task.Promote{Area: "payments", Paths: paths}}
}

// payDownKeepOpen is the first promote run's agent as its brief asks it to
// work: it raises the area's code, and it closes nothing — a first promote
// run's draft pull request is opened after the implement stage, so it has no
// number to name in an item's `## Closed by` while it works, and the brief
// sends the close on to the next promote run on the pull request
// (promoteBrief's closing line). The item therefore stays open here.
func payDownKeepOpen(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
	sys := req.AppendSystemPrompt
	if !strings.Contains(sys, "PROMOTE: raise the payments area to startup.") {
		t.Errorf("the promote brief never reached the coder:\n%s", sys)
	}
	if !strings.Contains(sys, "20261118-01") || !strings.Contains(sys, "<<<fugaro-promote-") {
		t.Errorf("the brief does not quote the area's item in a fenced block:\n%s", sys)
	}
	if !strings.Contains(sys, "Close nothing in this pass") {
		t.Errorf("a first promote run's brief does not forbid closing (the draft has no number yet):\n%s", sys)
	}
	shell(t, req, "echo '// retried' >> internal/payments/charge.go && git add -A && git commit -qm 'Pay down the charge item'")
	verifyTest(t, ctx, req)
	pr := filepath.Join(envValue(req.Env, "FUGARO_STATE_DIR"), "pr.md")
	if err := os.WriteFile(pr, []byte("# Raise the payments area\n\nPay down the item on the charge call; it is left open for the next run on this pull request to close."), 0o644); err != nil {
		t.Fatal(err)
	}
	return agent.Result{CostUSD: 1}, nil
}

// TestPromoteReachesTheAgentTheRecordAndTheReader is the whole delivery of a
// promote run, through the loop and not through a hand-built record: the brief
// reaches the coder's prompt, the item the run paid down is read off the
// branch at finalize, and the pull request's own status section carries what
// the pass did with it. Every seam in that chain was unpinned, and each is
// what a promote run is for.
//
// The agent works as the brief asks — it pays the item down and closes
// nothing — so the section reports the item left open and never a close. A
// fixture that closed the item here (contrary to the brief it received) would
// hide the whole two-run flow: this run's own diff is the fix, but it has no
// pull request to name, and the close belongs to the run on it
// (TestPromoteFollowUpClosesWhatTheEarlierRunPaidDown).
//
// Mutation (delete promptData's promote block, the readPromote call in the
// finalize, or the promotePart term in finalSection): a row below fails, and
// the pass stops reaching the agent, the record or the reader.
func TestPromoteReachesTheAgentTheRecordAndTheReader(t *testing.T) {
	h := newHarnessFiles(t, promoteAreaCfg(t), promoteTask(), map[string]string{
		"internal/payments/charge.go":          "package payments\n\nfunc Charge() error { return nil }\n",
		backlog.Dir + "/20261118-01-charge.md": promoteItem("open"),
	})
	rec, err := h.run(t, payDownKeepOpen, review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec.Outcome, err)
	}

	got := onlyPR(t, h.provider).Body
	for _, want := range []string{
		"**Promote: the payments area at startup**",
		"- left open 20261118-01: no retry on the charge call",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the pull request's status section lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "- closed 20261118-01") {
		t.Errorf("the section reports a close the brief forbade:\n%s", got)
	}
}

// TestPromoteFollowUpClosesWhatTheEarlierRunPaidDown is the second half of the
// flow the first run's brief describes: the first promote run pays the item
// down and leaves it open (no pull request to name), and the next promote run
// on the pull request closes it.
//
// The close is judged against the pull request's own diff from its base, not
// this run's: the follow-up commits only the item's flip, so its own changed
// files hold no code the item names — the fix is behind the commit the
// follow-up started at (refused.go's workBase). Judging against the run's own
// diff (promoteChanged's next line) reports the close as a stray and lets no
// promote run ever close what the first one paid down, which is exactly the
// contradiction LV7.1's acceptance and D31 forbid.
//
// Mutation (judge the close against r.changedPaths() instead of the pull
// request's diff): the section reads a stray, and the "closed" row below
// fails.
func TestPromoteFollowUpClosesWhatTheEarlierRunPaidDown(t *testing.T) {
	h := followUpHarness(t, promoteAreaCfg(t), func(h *harness) {
		// The area's debt is part of the repository before the first run
		// works: a promote pass pays down a recorded item, and a fixture whose
		// folder is empty runs the audit instead.
		seed := filepath.Join(t.TempDir(), "seed")
		testutil.Git(t, filepath.Dir(seed), "clone", "--quiet", h.remote, seed)
		testutil.WriteFiles(t, seed, map[string]string{
			"internal/payments/charge.go":          "package payments\n\nfunc Charge() error { return nil }\n",
			backlog.Dir + "/20261118-01-charge.md": promoteItem("open"),
		})
		testutil.Git(t, seed, "add", "-A")
		testutil.Git(t, seed, "commit", "--quiet", "-m", "record the area's debt")
		testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/main")
		if err := h.store.WriteTask(context.Background(), promoteTask()); err != nil {
			t.Fatal(err)
		}
	}, payDownKeepOpen, review("ship", 0))

	// The follow-up carries the promote block the launcher writes (the task
	// is the run's own, not inherited like the recipe): it is a promote run
	// on the pull request the first one opened.
	h.followUpRecipe(t, followID, runID, "Close the item the earlier run paid down.", nil, func(spec *task.Spec) {
		spec.Rigor, spec.Paths = "startup", promoteAreaPaths()
		spec.Promote = &task.Promote{Area: "payments", Paths: promoteAreaPaths()}
	})
	closeIt := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		sys := req.AppendSystemPrompt
		if !strings.Contains(sys, "This run is on pull request #1") {
			t.Errorf("the follow-up's brief does not name the pull request to close against:\n%s", sys)
		}
		shell(t, req, "perl -pi -e 's/^status: open$/status: closed/' "+backlog.Dir+"/20261118-01-charge.md"+
			" && printf 'Closed by pull request #1.\\n' >> "+backlog.Dir+"/20261118-01-charge.md"+
			" && git add -A && git commit -qm 'Close the charge item'")
		verifyTest(t, ctx, req)
		hand := filepath.Join(envValue(req.Env, "FUGARO_STATE_DIR"), "followup.md")
		if err := os.WriteFile(hand, []byte("Closed the item the earlier run paid down.\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return agent.Result{CostUSD: 1}, nil
	}
	rec, err := h.run(t, closeIt, review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec.Outcome, err)
	}
	got := onlyPR(t, h.provider).Body
	if !strings.Contains(got, "- closed 20261118-01: no retry on the charge call") {
		t.Errorf("the second promote run did not close what the first paid down:\n%s", got)
	}
	if strings.Contains(got, "does not admit") {
		t.Errorf("the close is reported as a stray, judged against the run's own diff:\n%s", got)
	}
}
