package runner_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// withoutBaseBranchKey removes the fixture's own git.base_branch, leaving the
// file the project's default base branch applies to. The fixture sets it, and
// a file that sets it wins every layer below.
func withoutBaseBranchKey(t *testing.T, cfg string) string {
	t.Helper()
	out := strings.Replace(cfg, "  base_branch: main\n", "", 1)
	if out == cfg {
		t.Fatal("the fixture no longer writes git.base_branch")
	}
	return out
}

// TestTaskBaseBranchIsFetchedAndTargeted: a task whose fugaro.yaml writes no
// git.base_branch is cut from the branch the task carries. The runner fetches
// that branch (repo.FetchBase) and records it as the base every later stage
// reads — the pull request's base, the diff's, the review's scope.
func TestTaskBaseBranchIsFetchedAndTargeted(t *testing.T) {
	cfg := withoutBaseBranchKey(t, testutil.FixtureFiles(t)["fugaro.yaml"])
	h := newHarness(t, cfg, &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "master",
		BaseBranch: "master", Workflow: "app", Task: "Add a feature"})
	addBaseBranch(t, h, "master")
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	mustReady(t, rec, err)
	if rec.BaseBranch != "master" {
		t.Fatalf("base_branch = %q, want master: the task's base is the run's", rec.BaseBranch)
	}
}

// TestFileBaseBranchBeatsTheTasks: the file's own git.base_branch is the base
// whatever the task carries. A task field written for a run started where the
// file said nothing must not follow the checkout to a branch whose file chose
// another base — the pull request would open against the wrong branch.
func TestFileBaseBranchBeatsTheTasks(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"], "  base_branch: main\n", "  base_branch: develop\n", 1)
	h := newHarness(t, cfg, &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "develop",
		BaseBranch: "master", Workflow: "app", Task: "Add a feature"})
	addBaseBranch(t, h, "develop")
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	mustReady(t, rec, err)
	if rec.BaseBranch != "develop" {
		t.Fatalf("base_branch = %q, want develop: the file's own key wins", rec.BaseBranch)
	}
}

// TestFollowUpTaskBaseBranch: a follow-up reads its configuration from the
// branch it continues (readBaseConfig), which refuses a file whose base is not
// that ref. The file here writes no git.base_branch, so the run's base is the
// one the task carries — and the resolution must be what answers the check,
// or every follow-up of a run cut from a project's default base is refused.
func TestFollowUpTaskBaseBranch(t *testing.T) {
	h := followUpHarness(t, withoutBaseBranchKey(t, followUpYAML(t, "")), func(h *harness) { addBaseBranch(t, h, "master") })
	h.followUp(t, followID, runID, "Tidy up.")
	spec := &task.Spec{Version: 1, RunID: followID, Repo: "acme/app", Ref: "master", BaseBranch: "master",
		Workflow: h.first.Workflow, Task: "Tidy up.", Branch: "fugaro/" + runID, PR: 1, PreviousRun: runID}
	if err := h.store.WriteTask(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	rec, err := h.run(t, implement("tidy"), review("ship", 0))
	mustReady(t, rec, err)
	if rec.BaseBranch != "master" {
		t.Fatalf("follow-up base_branch = %q, want master", rec.BaseBranch)
	}
}
