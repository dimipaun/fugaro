package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// seedPlanTaskLaunch stores a task and a launch.json whose plan_task is
// exactly planTask, with no launcher-side check: the run's own service
// account can write launch.json, so a reader meets whatever is there.
func seedPlanTaskLaunch(t *testing.T, f *cloudFixture, spec *task.Spec, planTask string) {
	t.Helper()
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	s := runstore.Open(b, appSlug, spec.RunID)
	if err := s.CreateTask(ctx, spec); err != nil {
		t.Fatal(err)
	}
	exec := startRunExecution(f, gcp.JobName(appSlug, "web"), appSlug, spec.RunID)
	if err := s.WriteLaunch(ctx, &runstore.Launch{Version: 1, RunID: spec.RunID, Execution: exec, PlanTask: planTask, LaunchedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

// TestPlanTaskReadSideDropsMisshapenIDs: a launch.json whose plan_task is not
// a plan task id (a traversal, spaces, 500 characters) yields no plan_task in
// runs ls --json or in watch --json, for a queued run and a finished one,
// while a well-formed id beside them comes through in both. The readers apply
// task.PlanTaskProblem, the launcher's own list, so the console never joins a
// run by an id no launcher could have written.
//
// Mutation (sanitise and cap only, as before): the three bad ids reach both
// documents and the assertions fail.
func TestPlanTaskReadSideDropsMisshapenIDs(t *testing.T) {
	f := newLaunchBudget(t, "observe")
	good := runID(time.Minute, "a001")
	seedPlanTaskLaunch(t, f.cloudFixture, firstRunSpec(good), "setup-wizard/W1.3")
	bad := map[string]string{
		runID(2*time.Minute, "a002"): "../x",
		runID(3*time.Minute, "a003"): "setup wizard/W 1.3",
		runID(4*time.Minute, "a004"): "plan/" + strings.Repeat("x", 495),
	}
	for run, id := range bad {
		seedPlanTaskLaunch(t, f.cloudFixture, firstRunSpec(run), id)
	}
	done := runID(5*time.Minute, "a005")
	seedPlanTaskLaunch(t, f.cloudFixture, firstRunSpec(done), "../../etc/x")
	finishedAt := time.Now().Add(-20 * time.Minute)
	writeRecord(t, f.cloudFixture, done, &runstore.Record{Version: 1, RunID: done, Repo: "acme/app", Workflow: "web",
		Status: runstore.StatusSucceeded, Outcome: runstore.OutcomeNone,
		StartedAt: time.Now().Add(-25 * time.Minute), FinishedAt: &finishedAt})
	bad[done] = "../../etc/x"

	out, _, err := execute(t, "watch", "--once", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Runs []struct {
			Run      string `json:"run"`
			PlanTask string `json:"plan_task"`
		} `json:"runs"`
		Finished []struct {
			Run      string `json:"run"`
			PlanTask string `json:"plan_task"`
		} `json:"finished"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	watchSeen := map[string]string{}
	for _, r := range doc.Runs {
		watchSeen[r.Run] = r.PlanTask
	}
	for _, r := range doc.Finished {
		watchSeen[r.Run] = r.PlanTask
	}
	ls, _ := lsJSON(t)
	lsSeen := map[string]string{}
	for _, r := range ls.Runs {
		lsSeen[r.RunID] = r.PlanTask
	}
	for what, seen := range map[string]map[string]string{"watch --json": watchSeen, "runs ls --json": lsSeen} {
		if _, ok := seen[good]; !ok {
			t.Fatalf("%s does not list %s", what, good)
		}
		if seen[good] != "setup-wizard/W1.3" {
			t.Errorf("%s: the well-formed id reads %q", what, seen[good])
		}
		for run, id := range bad {
			if _, ok := seen[run]; !ok {
				t.Errorf("%s does not list %s", what, run)
			}
			if seen[run] != "" {
				t.Errorf("%s: %s carries plan_task %q for a launch.json holding %q", what, run, seen[run], id)
			}
		}
	}
	for _, d := range []string{out, lsRawJSON(t)} {
		if n := strings.Count(d, `"plan_task"`); n != 1 {
			t.Errorf("%d plan_task keys, want 1 (the well-formed id):\n%s", n, d)
		}
	}
}
