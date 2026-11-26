package runner_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/rigor"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// The run-level half of LV5.1's hand-back rule: the two facts a unit test of
// Decide cannot reach — which file a follow-up owes, and that the runner's
// fallback body is built from the task text rather than from any file of the
// agent's.

// TestDecideHandBackFollowUpIsReady: a Pro follow-up whose own file carries the
// sections and whose review is clean is ready, and the file it owes is
// followup.md — a follow-up that wrote pr.md instead, or wrote nothing, owes it
// still (LV5.1, T5.2: a follow-up is the level its layers resolve).
func TestDecideHandBackFollowUpIsReady(t *testing.T) {
	for _, tc := range []struct {
		name string
		step step
		want string
	}{
		{"the follow-up's own file", withAnswer(implement("rename"), handBackFU), ""},
		{"no followup.md, only the pr.md a first run writes", implement("rename"), runner.ReasonNoHandBack},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := followUpHarness(t, handBackFollowCfg(t, "pro"), func(h *harness) { h.noHandBack = true }, commitOnly("feature"), review("ship", 0))
			h.comment(t, "alice", aliceID, "Please rename the helper to fooBar.")
			h.followUp(t, followID, runID, "")
			rec, err := h.run(t, tc.step, review("ship", 0))
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if rec.Outcome != runstore.OutcomeReady || rec.Reason != "" {
					t.Fatalf("outcome = %s (reason %q), want ready", rec.Outcome, rec.Reason)
				}
				return
			}
			if rec.Outcome != runstore.OutcomeDraft || rec.Reason != tc.want {
				t.Fatalf("outcome = %s (reason %q), want a draft for %q", rec.Outcome, rec.Reason, tc.want)
			}
		})
	}
}

// TestDecideHandBackIgnoresTheTaskText: the runner's fallback body embeds the
// task (rawPRText), so a brief that happens to carry a level's headings
// satisfies nothing — the account has to be the agent's own file
// (reconciliation §3b item 13). The run below is a Hotfix run whose task spells
// both of Hotfix's sections and whose agent left no pr.md, so the body the run
// publishes carries the headings and the run is still a draft.
func TestDecideHandBackIgnoresTheTaskText(t *testing.T) {
	const brief = "# Task: fix the nightly import\n\n" +
		"## Revert plan\n- revert the commit\n\n" +
		"## Morning note\n- what broke: the import stopped on a duplicate key\n"
	spec := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Workflow: "app", Task: brief, Rigor: rigor.Hotfix}
	h := newHarness(t, firstLineCfg(t, 2, ""), spec)
	h.noHandBack = true
	rec, err := h.run(t, noAccount(implement("feature")), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Rigor == nil || rec.Rigor.LevelOf() != rigor.Hotfix {
		t.Fatalf("the run resolved %+v, want the Hotfix the task asked for", rec.Rigor)
	}
	if !strings.Contains(onlyPR(t, h.provider).Body, "## Revert plan") {
		t.Fatalf("the pull request does not carry the task's own headings, so this case proves nothing:\n%s", onlyPR(t, h.provider).Body)
	}
	if rec.Outcome != runstore.OutcomeDraft || rec.Reason != runner.ReasonNoHandBack {
		t.Fatalf("outcome = %s (reason %q), want a draft for %q", rec.Outcome, rec.Reason, runner.ReasonNoHandBack)
	}
}

// noAccount is implement's work with the agent's hand-back file taken back off
// it: a run that committed and verified, and left no account of its own.
func noAccount(s step) step {
	return then(s, func(t *testing.T, req agent.Request) {
		if err := os.Remove(filepath.Join(envValue(req.Env, "FUGARO_STATE_DIR"), "pr.md")); err != nil {
			t.Fatal(err)
		}
	})
}
