package runner_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// bigBranch is the long-lived branch of the oversized-range tests.
const bigBranch = "rigor-modes"

// addBigBranch creates bigBranch on h's remote at main's tip plus one
// commit of lines lines, so that its divergence from main is past the
// runner's oversized-range threshold while any run's own change is small.
func addBigBranch(t *testing.T, h *harness, lines int) {
	t.Helper()
	seed := filepath.Join(t.TempDir(), "seed-big")
	testutil.Git(t, filepath.Dir(seed), "clone", "--quiet", h.remote, seed)
	var b strings.Builder
	for i := 1; i <= lines; i++ {
		b.WriteString(strconv.Itoa(i) + "\n")
	}
	if err := os.WriteFile(filepath.Join(seed, "integration.txt"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, seed, "add", "-A")
	testutil.Git(t, seed, "commit", "--quiet", "-m", "the integration branch's own work")
	testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/"+bigBranch)
}

// implementOnBigBranch is what the reported briefs had the agent do inside
// a run launched without --ref: put its work on origin/<bigBranch>, then
// commit a small change of its own.
func implementOnBigBranch(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
	shell(t, req, "git fetch -q origin "+bigBranch+":refs/remotes/origin/"+bigBranch+" && git reset -q --hard origin/"+bigBranch)
	return implement("feature")(t, ctx, req)
}

// TestOversizedReviewRangeIsRestrictedToTheRunsOwnCommits: a run launched
// from main whose agent moved its work onto an integration branch reviews
// origin/main...HEAD, the whole of that branch. The base is not changed —
// the runner cannot know what the work was meant to start on — but the
// review prompt opens with the restriction to the run's own commits, by a
// command that does not depend on the base, and the run's report says the
// range was oversized and that --ref is the fix.
//
// Mutation (run, restore): make reviewRestriction return "" after the
// baseUnreadable case — the prompt and the report lose the note.
func TestOversizedReviewRangeIsRestrictedToTheRunsOwnCommits(t *testing.T) {
	spec := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Workflow: "app", Task: "Add a feature"}
	h := newHarness(t, "", spec)
	addBigBranch(t, h, 3000)
	rec, err := h.run(t, implementOnBigBranch, review("ship", 0))
	mustReady(t, rec, err)
	prompt := reviewPromptOf(t, h.agent.calls)
	note, scope, ok := strings.Cut(prompt, "Review the changes on this branch against ")
	if !ok {
		t.Fatalf("the scope line is gone:\n%s", prompt)
	}
	for _, want := range []string{
		"this run's own commits change only",
		"git log --name-only HEAD --not --exclude=origin/fugaro/" + runID + " --remotes=origin",
		"3001 changed line(s)",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("the note before the scope line lacks %q:\n%s", want, note)
		}
	}
	if !strings.HasPrefix(scope, "origin/main ") {
		t.Errorf("the base changed; the scope line reads:\n%s", scope)
	}
	report, err := h.store.ReadFile(context.Background(), "report.md")
	if err != nil {
		t.Fatal(err)
	}
	if want := "review range was oversized: base main"; !strings.Contains(string(report), want) {
		t.Errorf("the report lacks %q:\n%s", want, report)
	}
	if !strings.Contains(string(report), "use --ref") {
		t.Errorf("the report does not name --ref as the fix:\n%s", report)
	}
}

// TestNormalReviewRangeIsNotRestricted: the same integration branch, with
// the run launched from it (--ref), and a plain run from main: neither
// range is larger than the run's own work, so neither prompt nor report
// carries the note.
func TestNormalReviewRangeIsNotRestricted(t *testing.T) {
	spec := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: bigBranch, Workflow: "app", Task: "Add a feature"}
	onBranch := newHarness(t, "", spec)
	addBigBranch(t, onBranch, 3000)
	plain := newHarness(t, "", nil)
	for name, h := range map[string]*harness{"--ref " + bigBranch: onBranch, "main": plain} {
		rec, err := h.run(t, implement("feature"), review("ship", 0))
		mustReady(t, rec, err)
		if prompt := reviewPromptOf(t, h.agent.calls); strings.Contains(prompt, "own commits change only") {
			t.Errorf("%s: a normal range is restricted:\n%s", name, prompt)
		}
		report, err := h.store.ReadFile(context.Background(), "report.md")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(report), "oversized") {
			t.Errorf("%s: the report calls a normal range oversized:\n%s", name, report)
		}
	}
}
