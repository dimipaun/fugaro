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

// TestPromoteReachesTheAgentTheRecordAndTheReader is the whole delivery of a
// promote run, through the loop and not through a hand-built record: the brief
// reaches the coder's prompt, the item the run paid down is read off the
// branch at finalize, and the pull request's own status section carries what
// the pass closed. Every seam in that chain was unpinned, and
// each is what a promote run is for.
//
// Mutation (delete promptData's promote block, the readPromote call in the
// finalize, or the promotePart term in finalSection): a row below fails, and
// the pass stops reaching the agent, the record or the reader.
func TestPromoteReachesTheAgentTheRecordAndTheReader(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"], "agent:\n",
		"rigor:\n  default: poc\n  areas:\n    - { name: payments, match: \"internal/payments/**\", mode: startup }\nagent:\n", 1)
	if !strings.Contains(cfg, "name: payments") {
		t.Fatal("the fixture's config names no agent block to put the area rule above")
	}
	paths := []string{"internal/payments/**"}
	spec := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Workflow: "app",
		Task: "Raise the payments area to startup.", Rigor: "startup", Paths: paths,
		Promote: &task.Promote{Area: "payments", Paths: paths}}
	h := newHarnessFiles(t, cfg, spec, map[string]string{
		"internal/payments/charge.go":          "package payments\n\nfunc Charge() error { return nil }\n",
		backlog.Dir + "/20261118-01-charge.md": promoteItem("open"),
	})

	// The run's own agent pays the item down: the fix and the item's `status:
	// closed` in one commit, which is D31's rule and the only thing the runner
	// reads at finalize.
	payDown := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		sys := req.AppendSystemPrompt
		if !strings.Contains(sys, "PROMOTE: raise the payments area to startup.") {
			t.Errorf("the promote brief never reached the coder:\n%s", sys)
		}
		if !strings.Contains(sys, "20261118-01") || !strings.Contains(sys, "<<<fugaro-promote-") {
			t.Errorf("the brief does not quote the area's item in a fenced block:\n%s", sys)
		}
		shell(t, req, "perl -pi -e 's/^status: open$/status: closed/' "+backlog.Dir+"/20261118-01-charge.md"+
			" && echo '// retried' >> internal/payments/charge.go && git add -A && git commit -qm 'Pay down the charge item'")
		verifyTest(t, ctx, req)
		pr := filepath.Join(envValue(req.Env, "FUGARO_STATE_DIR"), "pr.md")
		if err := os.WriteFile(pr, []byte("# Raise the payments area\n\nPays down its item."), 0o644); err != nil {
			t.Fatal(err)
		}
		return agent.Result{CostUSD: 1}, nil
	}
	rec, err := h.run(t, payDown, review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec.Outcome, err)
	}

	got := onlyPR(t, h.provider).Body
	for _, want := range []string{
		"**Promote: the payments area at startup**",
		"- closed 20261118-01: no retry on the charge call",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the pull request's status section lacks %q:\n%s", want, got)
		}
	}
}
