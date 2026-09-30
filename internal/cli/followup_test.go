package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/lock"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// The runs of these tests are on PR 7 of acme/app. seedRoot and seedFollowUp
// store finished runs with no execution to join.

// seedRoot stores the first run id, which opened PR 7 and pushed, started at.
func seedRoot(t *testing.T, f *cloudFixture, id string, at time.Time) {
	t.Helper()
	seedSpec(t, f, firstRunSpec(id), false)
	rec := prRecord(id, "", 7, 1)
	rec.StartedAt = at
	writeRecord(t, f, id, rec)
}

// seedFollowUp stores follow-up id of root's PR 7 after previous, started
// at, with status; pushed says whether its record has pushed_head.
func seedFollowUp(t *testing.T, f *cloudFixture, id, root, previous string, at time.Time, status runstore.Status, pushed bool) {
	t.Helper()
	seedSpec(t, f, followUpSpec(id, root, previous, 7), false)
	rec := prRecord(id, "", 7, 1)
	rec.Branch, rec.StartedAt, rec.Status = "fugaro/"+root, at, status
	rec.FollowUp = &runstore.FollowUp{PR: 7, PreviousRun: previous}
	if !pushed {
		rec.PushedHead = ""
	}
	writeRecord(t, f, id, rec)
}

// readSpec is run id's stored task.
func readSpec(t *testing.T, f *cloudFixture, id string) *task.Spec {
	t.Helper()
	b, _ := blob.OpenBucket(context.Background(), f.bucket)
	defer b.Close()
	spec, err := runstore.Open(b, appSlug, id).ReadTask(context.Background())
	if err != nil {
		t.Fatalf("task.json of %s: %v", id, err)
	}
	return spec
}

// wantRefused runs fugaro with args and wants exit 1 with want in the error,
// and no execution started.
func wantRefused(t *testing.T, f *cloudFixture, want string, args ...string) {
	t.Helper()
	_, _, err := execute(t, args...)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), want) {
		t.Fatalf("%v: err = %v, want exit 1 with %q", args, err, want)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%v: %d executions, want none", args, n)
	}
}

var (
	rootID = runIDAt(3, "090000", "aaaa")
	fuID   = runIDAt(0, "120000", "f00d") // the follow-up the tests launch
)

func TestRunPRLaunchesFollowUp(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-72*time.Hour))
	out, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--json", "also rename x")
	if err != nil {
		t.Fatal(err)
	}
	var res launchResult
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Status != "launched" || res.PR != 7 || res.PreviousRun != rootID ||
		res.Branch != "fugaro/"+rootID || res.RunID != fuID {
		t.Fatalf("result = %+v (%s), %v", res, out, err)
	}
	for _, key := range []string{`"pr": 7`, `"previous_run": "` + rootID + `"`} {
		if !strings.Contains(out, key) {
			t.Errorf("JSON lacks %s:\n%s", key, out)
		}
	}
	spec := readSpec(t, f, fuID)
	if spec.Branch != "fugaro/"+rootID || spec.PR != 7 || spec.PreviousRun != rootID || spec.Ref != "main" || spec.Workflow != "web" ||
		spec.RequestedBy != "someone@example.com" || spec.Task != "also rename x" || spec.Repo != "acme/app" {
		t.Fatalf("task = %+v", spec)
	}
	if calls := f.run.RunRequests(); len(calls) != 1 || calls[0].Env["FUGARO_RUN"] != appSlug+"/"+fuID {
		t.Fatalf("jobs.run calls = %+v", calls)
	}
}

// The ref and workflow come from the previous run's task, not the local
// config's defaults.
func TestRunPRTakesPreviousRef(t *testing.T) {
	f := newCloudFixture(t)
	spec := firstRunSpec(rootID)
	spec.Ref = "release"
	seedSpec(t, f, spec, false)
	writeRecord(t, f, rootID, prRecord(rootID, "", 7, 1))
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID); err != nil {
		t.Fatal(err)
	}
	if got := readSpec(t, f, fuID); got.Ref != "release" || got.Workflow != "web" {
		t.Fatalf("task = %+v", got)
	}
}

func TestRunPRPrintsPRBranch(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-72*time.Hour))
	args := []string{"run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID}
	out, _, err := execute(t, args...)
	if err != nil {
		t.Fatal(err)
	}
	want := "launched " + appSlug + "/" + fuID + " (follow-up of PR #7, after " + rootID + ")\n  branch fugaro/" + rootID + "\n  logs https://"
	if !strings.HasPrefix(out, want) {
		t.Fatalf("output:\n%s\nwant prefix:\n%s", out, want)
	}
	out, _, err = execute(t, args...)
	if want := "already launched " + appSlug + "/" + fuID + " (follow-up of PR #7, after " + rootID + ")\n  branch fugaro/" + rootID + "\n"; err != nil || !strings.HasPrefix(out, want) {
		t.Fatalf("repeat: %v, output:\n%s\nwant prefix:\n%s", err, out, want)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want 1", n)
	}
}

func TestRunPRNotAFugaroPR(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now())
	wantRefused(t, f, "not a Fugaro PR", "run", "--repo", "acme/app", "--pr", "9")
	// A run older than the runs bucket keeps is not found either.
	old := runIDAt(100, "090000", "bbbb")
	seedSpec(t, f, firstRunSpec(old), false)
	writeRecord(t, f, old, prRecord(old, "", 8, 1))
	wantRefused(t, f, "90 days", "run", "--repo", "acme/app", "--pr", "8")
}

func TestRunPRRefusesActiveRun(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-time.Hour))
	active := runIDAt(0, "000100", "bbbb")
	exec := seedSpec(t, f, followUpSpec(active, rootID, rootID, 7), true)
	writeRecord(t, f, active, &runstore.Record{Version: 1, RunID: active, Repo: "acme/app", Execution: exec, Status: runstore.StatusRunning,
		Stage: "implement", Branch: "fugaro/" + rootID, PR: &runstore.PRRef{Number: 7}, StartedAt: time.Now(),
		FollowUp: &runstore.FollowUp{PR: 7, PreviousRun: rootID}})
	n := len(f.run.Executions())
	_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), active) || !strings.Contains(err.Error(), "still") ||
		!strings.Contains(err.Error(), "fugaro ls --pr 7 --repo acme/app") {
		t.Fatalf("err = %v", err)
	}
	if len(f.run.Executions()) != n {
		t.Fatal("a follow-up launched while another ran")
	}
}

func TestRunPRRefusesErrorRow(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-time.Hour))
	bad := runIDAt(0, "000100", "bbbb")
	seedSpec(t, f, followUpSpec(bad, rootID, rootID, 7), false)
	putBuildObject(t, f, "runs/"+appSlug+"/"+bad+"/result.json", []byte("{not json"))
	wantRefused(t, f, bad, "run", "--repo", "acme/app", "--pr", "7")
}

func TestRunPRRefusesLiveLock(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-time.Hour))
	holder := runIDAt(0, "000100", "bbbb")
	putLock := func(expires time.Time) {
		data, _ := json.Marshal(lock.Holder{RunID: holder, ExpiresAt: expires})
		putBuildObject(t, f, lock.Key(appSlug, "fugaro/"+rootID), data)
	}
	putLock(time.Now().Add(time.Hour))
	wantRefused(t, f, "branch busy: run "+holder, "run", "--repo", "acme/app", "--pr", "7")
	// An expired lock is the runner's to take over.
	putLock(time.Now().Add(-time.Minute))
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7"); err != nil {
		t.Fatalf("expired lock: %v", err)
	}
}

func TestRunPRUnreadableLockIsFine(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-time.Hour))
	putBuildObject(t, f, lock.Key(appSlug, "fugaro/"+rootID), []byte("{garbage"))
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7"); err != nil {
		t.Fatalf("unreadable lock: %v", err)
	}
}

func TestRunPRBranchDisagreement(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-time.Hour))
	other := runIDAt(1, "090000", "cccc")
	seedFollowUp(t, f, runIDAt(0, "000100", "bbbb"), other, rootID, time.Now(), runstore.StatusSucceeded, true)
	wantRefused(t, f, "disagree", "run", "--repo", "acme/app", "--pr", "7")
}

// The branch names its root run; when that run's record still exists, it
// must be on that branch.
func TestRunPRRootBranchMustNameRoot(t *testing.T) {
	f := newCloudFixture(t)
	root := runIDAt(2, "090000", "aaaa")
	seedSpec(t, f, firstRunSpec(root), false)
	rec := prRecord(root, "", 5, 1) // root's own record names another PR and another branch
	rec.Branch = "fugaro/" + runIDAt(2, "090000", "9999")
	writeRecord(t, f, root, rec)
	seedFollowUp(t, f, runIDAt(0, "000100", "bbbb"), root, root, time.Now(), runstore.StatusSucceeded, true)
	wantRefused(t, f, root, "run", "--repo", "acme/app", "--pr", "7")
}

// A record from before follow-ups existed has no pushed_head: its PR
// number means it pushed its head_sha.
func TestRunPRPreM6Record(t *testing.T) {
	f := newCloudFixture(t)
	seedSpec(t, f, firstRunSpec(rootID), false)
	writeRecord(t, f, rootID, &runstore.Record{Version: 1, RunID: rootID, Repo: "acme/app", Workflow: "web", Status: runstore.StatusSucceeded,
		Outcome: runstore.OutcomeReady, Branch: "fugaro/" + rootID, HeadSHA: "2222222222222222222222222222222222222222",
		PR: &runstore.PRRef{Number: 7, URL: "https://github.com/acme/app/pull/7"}})
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID); err != nil {
		t.Fatal(err)
	}
	if got := readSpec(t, f, fuID); got.PreviousRun != rootID || got.Branch != "fugaro/"+rootID {
		t.Fatalf("task = %+v", got)
	}
}

// The root run's objects expired; the follow-ups that remain agree on the
// branch, so it is accepted.
func TestRunPRRootExpired(t *testing.T) {
	f := newCloudFixture(t)
	gone := runIDAt(95, "090000", "aaaa")
	fu1, fu2 := runIDAt(2, "090000", "bbbb"), runIDAt(1, "090000", "cccc")
	seedFollowUp(t, f, fu1, gone, gone, time.Now().Add(-48*time.Hour), runstore.StatusSucceeded, true)
	seedFollowUp(t, f, fu2, gone, fu1, time.Now().Add(-24*time.Hour), runstore.StatusFailed, true)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID); err != nil {
		t.Fatal(err)
	}
	if got := readSpec(t, f, fuID); got.PreviousRun != fu2 || got.Branch != "fugaro/"+gone {
		t.Fatalf("task = %+v", got)
	}
}

func TestRunPRNeverPushed(t *testing.T) {
	f := newCloudFixture(t)
	gone := runIDAt(95, "090000", "aaaa")
	seedFollowUp(t, f, runIDAt(1, "090000", "bbbb"), gone, gone, time.Now().Add(-24*time.Hour), runstore.StatusFailed, false)
	wantRefused(t, f, "pushed", "run", "--repo", "acme/app", "--pr", "7")
}

// previous_run is the newest run that pushed, whatever its status, by
// started_at; a newer run that never pushed is skipped.
func TestRunPRPreviousIsNewestPushed(t *testing.T) {
	f := newCloudFixture(t)
	now := time.Now()
	seedRoot(t, f, rootID, now.Add(-72*time.Hour))
	older := runIDAt(1, "230000", "bbbb") // its run ID is the later one, but it started first
	seedFollowUp(t, f, older, rootID, rootID, now.Add(-10*time.Hour), runstore.StatusFailed, true)
	infra := runIDAt(1, "010000", "cccc")
	seedFollowUp(t, f, infra, rootID, older, now.Add(-5*time.Hour), runstore.StatusInfraError, true)
	unpushed := runIDAt(0, "000100", "dddd")
	seedFollowUp(t, f, unpushed, rootID, infra, now.Add(-time.Hour), runstore.StatusFailed, false)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID); err != nil {
		t.Fatal(err)
	}
	if got := readSpec(t, f, fuID); got.PreviousRun != infra {
		t.Fatalf("previous_run = %s, want %s (older %s, unpushed %s)", got.PreviousRun, infra, older, unpushed)
	}
}

func TestRunPRWarnsUnlaunched(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-time.Hour))
	waiting := runIDAt(0, "000100", "bbbb")
	seedSpec(t, f, followUpSpec(waiting, rootID, rootID, 7), false)
	_, errOut, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID)
	if err != nil || !strings.Contains(errOut, waiting) || !strings.Contains(errOut, "never launched") {
		t.Fatalf("stderr %q, %v", errOut, err)
	}
	if got := readSpec(t, f, fuID); got.PreviousRun != rootID {
		t.Fatalf("task = %+v", got)
	}
}

func TestRunPRFlagConflicts(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-time.Hour))
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--pr", "7", "--ref", "main"}, "--ref"},
		{[]string{"--pr", "7", "--workflow", "web"}, "--workflow"},
		{[]string{"--pr", "0"}, "--pr 0"},
		{[]string{"--pr", "-3"}, "--pr -3"},
		{[]string{"--pr", "7", "--task-file", "-", "text too"}, "not both"},
	} {
		wantRefused(t, f, tc.want, append([]string{"run", "--repo", "acme/app"}, tc.args...)...)
	}
	wantRefused(t, f, "--pr", "run", "--pr", "7", "--retry", rootID)
}

func TestRunPRTextOptional(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-time.Hour))
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID); err != nil {
		t.Fatal(err)
	}
	if got := readSpec(t, f, fuID); got.Task != "" || !got.IsFollowUp() {
		t.Fatalf("task = %+v", got)
	}
}

func TestRunPREmptyTaskFileStdin(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-time.Hour))
	if _, _, err := executeStdin(t, " \n", "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--task-file", "-"); err != nil {
		t.Fatal(err)
	}
	if got := readSpec(t, f, fuID); got.Task != "" {
		t.Fatalf("task = %+v", got)
	}
}

func TestRunPRTaskFile(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-time.Hour))
	path := filepath.Join(t.TempDir(), "extra.md")
	if err := os.WriteFile(path, []byte("Also rename x.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--task-file", path); err != nil {
		t.Fatal(err)
	}
	if got := readSpec(t, f, fuID); got.Task != "Also rename x." {
		t.Fatalf("task = %+v", got)
	}
}

func TestRunPRBatch(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-time.Hour))
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--batch", "tuesday"); err != nil {
		t.Fatal(err)
	}
	if got := readSpec(t, f, fuID); got.Batch != "tuesday" {
		t.Fatalf("task = %+v", got)
	}
}

func TestRunPRTotalTimeout(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-time.Hour))
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--total-timeout", "45m"); err != nil {
		t.Fatal(err)
	}
	if got := readSpec(t, f, fuID); got.Overrides.TotalTimeout != "45m0s" {
		t.Fatalf("task = %+v", got)
	}
	if got := f.run.RunRequests(); len(got) != 1 || got[0].Timeout != "2820s" {
		t.Fatalf("requests = %+v, want timeout 2820s", got)
	}
}

// A repeated --run-id reuses its stored follow-up as it is, without
// resolving the PR again (the run would find itself active).
func TestRunPRRepeatRunIDIsIdempotent(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-time.Hour))
	args := []string{"run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--json", "also rename x"}
	if _, _, err := execute(t, args...); err != nil {
		t.Fatal(err)
	}
	before := readSpec(t, f, fuID)
	out, _, err := execute(t, args...)
	if err != nil || !strings.Contains(out, `"already-launched"`) {
		t.Fatalf("repeat: %s, %v", out, err)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want 1", n)
	}
	if after := readSpec(t, f, fuID); after.PreviousRun != before.PreviousRun || after.RequestedBy != before.RequestedBy || after.Task != before.Task {
		t.Fatalf("stored task changed: %+v → %+v", before, after)
	}
	_, _, err = execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "a different text")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "different task") {
		t.Fatalf("different text: %v", err)
	}
	_, _, err = execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--batch", "other", "also rename x")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "different task") {
		t.Fatalf("different batch: %v", err)
	}
	// A first run's run ID is no follow-up.
	_, _, err = execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", rootID, "x")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "different task") {
		t.Fatalf("a first run's ID: %v", err)
	}
}

// A repeated --run-id of a stored follow-up that never launched launches
// it after the PR's checks, with itself left out.
func TestRunPRRepeatRunIDLaunchesUnlaunched(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-time.Hour))
	spec := followUpSpec(fuID, rootID, rootID, 7)
	spec.Task = "also rename x"
	seedSpec(t, f, spec, false)
	out, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID, "--json", "also rename x")
	if err != nil || !strings.Contains(out, `"launched"`) || len(f.run.Executions()) != 1 {
		t.Fatalf("%s, %v, %d executions", out, err, len(f.run.Executions()))
	}
}

func TestRetryFollowUp(t *testing.T) {
	f := newCloudFixture(t)
	t.Chdir(t.TempDir())
	seedRoot(t, f, rootID, time.Now().Add(-time.Hour))
	seedSpec(t, f, followUpSpec(fuID, rootID, rootID, 7), false)
	out, _, err := execute(t, "run", "--retry", fuID, "--json")
	var res launchResult
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || res.Status != "launched" || res.PR != 7 || res.Branch != "fugaro/"+rootID {
		t.Fatalf("%s, %v", out, err)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want 1", n)
	}
}

// Another run updated the PR after the stored follow-up was made: it
// would act on stale context, so --retry refuses it.
func TestRetryFollowUpRefusedWhenSuperseded(t *testing.T) {
	f := newCloudFixture(t)
	t.Chdir(t.TempDir())
	seedRoot(t, f, rootID, time.Now().Add(-2*time.Hour))
	seedSpec(t, f, followUpSpec(fuID, rootID, rootID, 7), false)
	newer := runIDAt(0, "130000", "bbbb")
	seedFollowUp(t, f, newer, rootID, rootID, time.Now().Add(-time.Hour), runstore.StatusSucceeded, true)
	wantRefused(t, f, "new follow-up", "run", "--retry", fuID)
}

func TestRunPRMaxParallel(t *testing.T) {
	f := newCloudFixture(t) // max_parallel: 2
	seedRoot(t, f, rootID, time.Now().Add(-time.Hour))
	for _, id := range []string{runIDAt(0, "000100", "1111"), runIDAt(0, "000200", "2222")} {
		if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", id, "task"); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "max_parallel") || len(f.run.Executions()) != 2 {
		t.Fatalf("err = %v, %d executions", err, len(f.run.Executions()))
	}
}

// A lock with no holder named is no lock a runner wrote: the runner takes
// it over even before it expires, so the CLI doesn't refuse it either.
func TestRunPRLockWithoutHolderIsFine(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-time.Hour))
	data, _ := json.Marshal(lock.Holder{ExpiresAt: time.Now().Add(time.Hour)})
	putBuildObject(t, f, lock.Key(appSlug, "fugaro/"+rootID), data)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7"); err != nil {
		t.Fatalf("lock without a holder: %v", err)
	}
}

// A run on the PR that never launched is left out, so its branch can't
// make the PR's runs disagree.
func TestRunPRUnlaunchedBranchIgnored(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-time.Hour))
	waiting := runIDAt(0, "000100", "bbbb")
	seedSpec(t, f, followUpSpec(waiting, runIDAt(1, "090000", "cccc"), rootID, 7), false)
	_, errOut, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID)
	if err != nil || !strings.Contains(errOut, "never launched") {
		t.Fatalf("stderr %q, %v", errOut, err)
	}
	if got := readSpec(t, f, fuID); got.Branch != "fugaro/"+rootID {
		t.Fatalf("task = %+v", got)
	}
}

// A first run launched with --ref develop opens its PR on the base its
// config names: the follow-up's ref is that recorded base, not --ref.
func TestRunPRUsesRecordedBaseBranch(t *testing.T) {
	f := newCloudFixture(t)
	spec := firstRunSpec(rootID)
	spec.Ref = "develop"
	seedSpec(t, f, spec, false)
	rec := prRecord(rootID, "", 7, 1)
	rec.BaseBranch = "main"
	writeRecord(t, f, rootID, rec)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID); err != nil {
		t.Fatal(err)
	}
	if got := readSpec(t, f, fuID); got.Ref != "main" {
		t.Fatalf("task ref = %q, want the recorded base main", got.Ref)
	}
}

// A record from before base_branch was recorded falls back to the
// previous run's ref, but one that isn't a branch name is refused at
// launch rather than by the runner, after a container start.
func TestRunPRRefusesNonBranchRef(t *testing.T) {
	for _, ref := range []string{"0123456789abcdef0123456789abcdef01234567", "abc1234", "refs/tags/v1.0", "HEAD", "main~1"} {
		t.Run(ref, func(t *testing.T) {
			f := newCloudFixture(t)
			spec := firstRunSpec(rootID)
			spec.Ref = ref
			seedSpec(t, f, spec, false)
			writeRecord(t, f, rootID, prRecord(rootID, "", 7, 1))
			wantRefused(t, f, "is not a branch name", "run", "--repo", "acme/app", "--pr", "7")
		})
	}
}

func TestRunPRRefsHeadsRefIsItsBranch(t *testing.T) {
	f := newCloudFixture(t)
	spec := firstRunSpec(rootID)
	spec.Ref = "refs/heads/release"
	seedSpec(t, f, spec, false)
	writeRecord(t, f, rootID, prRecord(rootID, "", 7, 1))
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID); err != nil {
		t.Fatal(err)
	}
	if got := readSpec(t, f, fuID); got.Ref != "release" {
		t.Fatalf("task ref = %q, want release", got.Ref)
	}
}

// A malformed --run-id is refused before it names any object.
func TestRunPRRefusesBadRunID(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-time.Hour))
	for _, id := range []string{"../x", "20260930-120000-F00D", "x"} {
		wantRefused(t, f, "--run-id", "run", "--repo", "acme/app", "--pr", "7", "--run-id", id)
	}
}

// A lock object too large for the runner to read would fail its launch:
// the CLI refuses it and names the object.
func TestRunPRRefusesOversizedLock(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-time.Hour))
	key := lock.Key(appSlug, "fugaro/"+rootID)
	putBuildObject(t, f, key, []byte(strings.Repeat(" ", blobx.MaxReadBytes+1)))
	wantRefused(t, f, key, "run", "--repo", "acme/app", "--pr", "7")
}

// ls --pr and run --pr look back over the same window.
func TestPRLookbacksAgree(t *testing.T) {
	if d, err := parseSince(prLookback); err != nil || d != followUpLookback {
		t.Fatalf("ls --pr looks back %v (%v), run --pr %v", d, err, followUpLookback)
	}
}

// A recorded base_branch is a real git.base_branch, however much it looks
// like a commit ID.
func TestRunPRRecordedHexBaseBranch(t *testing.T) {
	for _, base := range []string{"cafe123", "20241001"} {
		t.Run(base, func(t *testing.T) {
			f := newCloudFixture(t)
			spec := firstRunSpec(rootID)
			spec.Ref = base
			seedSpec(t, f, spec, false)
			rec := prRecord(rootID, "", 7, 1)
			rec.BaseBranch = base
			writeRecord(t, f, rootID, rec)
			if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID); err != nil {
				t.Fatal(err)
			}
			if got := readSpec(t, f, fuID); got.Ref != base {
				t.Fatalf("task ref = %q, want %s", got.Ref, base)
			}
		})
	}
}
