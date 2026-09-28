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

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// seedLost seeds a run launched long ago whose execution the backend no
// longer knows and whose runner never wrote a record (C-I2).
func seedLost(t *testing.T, f *cloudFixture, id string) {
	t.Helper()
	seedRun(t, f, id, "", "someone@example.com", false)
	b, _ := blob.OpenBucket(context.Background(), f.bucket)
	defer b.Close()
	if err := runstore.Open(b, appSlug, id).WriteLaunch(context.Background(), &runstore.Launch{Version: 1, RunID: id,
		Execution: appExecution("gone1"), LaunchedAt: time.Now().Add(-runstore.ClaimTTL - time.Minute)}); err != nil {
		t.Fatal(err)
	}
}

// ls, ls --watch and cancel agree on a lost run: infra_error, settled.
func TestLostRunIsInfraErrorEverywhere(t *testing.T) {
	f := newCloudFixture(t)
	id := time.Now().UTC().Format("20060102") + "-090000-aaaa"
	seedLost(t, f, id)
	got, _ := lsJSON(t)
	if len(got.Runs) != 1 || got.Runs[0].Status != "infra_error" || got.Runs[0].Reason != runview.ReasonLost || !got.Runs[0].Settled {
		t.Fatalf("ls = %+v", got.Runs)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := execute(t, "ls", "--watch", "--json", "--interval", "10ms")
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ls --watch never settled on a lost run")
	}
	out, _, err := execute(t, "cancel", "--json", id)
	var res cancelResult
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || res.Status != cancelUnfinalized {
		t.Fatalf("cancel = %s, %v", out, err)
	}
}

// Having won the claim, launchRun re-checks the cancel marker: a cancel
// that landed while it claimed stops the launch, and the claim is released
// so cancel sees the launch end (C-I3).
func TestLaunchRechecksCancelAfterTakingTheClaim(t *testing.T) {
	f := newCloudFixture(t)
	env := memEnv(t, f)
	spec := raceSpec(t, env)
	s := runstore.Open(env.bucket.Bucket, appSlug, spec.RunID)
	setHooks(t, nil, nil, func() { _ = s.RequestCancel(context.Background()) })
	_, err := launchRun(context.Background(), env, appSlug, spec, time.Now())
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want 0", n)
	}
	if held, _ := env.bucket.Exists(context.Background(), s.ClaimKey()); held {
		t.Fatal("the claim is still held")
	}
}

// cancel on a run whose launch claim is fresh doesn't say "never launched":
// it marks the run and waits for the launch; when launch.json appears it
// cancels the execution like any launched run (C-I3).
func TestCancelWaitsForAnInFlightLaunch(t *testing.T) {
	f := newCloudFixture(t)
	shortWait(t)
	claimWait = 5 * time.Second
	const id = "20260927-100000-abcd"
	seedRun(t, f, id, "", "", false)
	b, _ := blob.OpenBucket(context.Background(), f.bucket)
	defer b.Close()
	s := runstore.Open(b, appSlug, id)
	if ok, _, err := s.Claim(context.Background(), "laptop/1/1", time.Now()); !ok || err != nil {
		t.Fatal(ok, err)
	}
	started := make(chan string, 1)
	go func() {
		time.Sleep(100 * time.Millisecond)
		exec := f.run.Start(gcp.JobName(appSlug, "web"))
		f.run.SetState(exec, backend.StateRunning)
		_ = s.WriteLaunch(context.Background(), &runstore.Launch{Version: 1, RunID: id, Execution: exec, LaunchedAt: time.Now()})
		started <- exec
	}()
	out, _, err := execute(t, "cancel", "--json", "--now", "--poll", "10ms", id)
	var res cancelResult
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || res.Status != cancelCancelled || !res.Marker {
		t.Fatalf("cancel = %s, %v", out, err)
	}
	if exec := <-started; f.run.State(exec) != backend.StateCancelled {
		t.Fatalf("execution state %s", f.run.State(exec))
	}
}

// A fresh claim that yields no launch in time: cancel reports launching,
// with the marker written, so the launcher or the runner stops it.
func TestCancelReportsALaunchStillInFlight(t *testing.T) {
	f := newCloudFixture(t)
	shortWait(t)
	const id = "20260927-100000-abcd"
	seedRun(t, f, id, "", "", false)
	b, _ := blob.OpenBucket(context.Background(), f.bucket)
	defer b.Close()
	if ok, _, err := runstore.Open(b, appSlug, id).Claim(context.Background(), "laptop/1/1", time.Now()); !ok || err != nil {
		t.Fatal(ok, err)
	}
	out, _, err := execute(t, "cancel", "--poll", "10ms", id)
	if err != nil || !strings.Contains(out, "in flight") || !cancelled(t, f, id) {
		t.Fatalf("cancel = %q, %v", out, err)
	}
	out, _, err = execute(t, "cancel", "--json", "--poll", "10ms", id)
	var res cancelResult
	if err != nil || json.Unmarshal([]byte(out), &res) != nil || res.Status != cancelLaunching || !res.Marker {
		t.Fatalf("cancel --json = %s, %v", out, err)
	}
}

// finishingBackend lets the runner write its final record and exit just
// before the CLI reads the execution, the window of C-M1.
type finishingBackend struct {
	backend.Backend
	finish func(name string)
}

func (fb finishingBackend) Execution(ctx context.Context, name string) (backend.Execution, error) {
	fb.finish(name)
	return fb.Backend.Execution(ctx, name)
}

// A run that finishes between the record read and the execution read is
// its final record's status, not infra_error (C-M1).
func TestLoadRowsOneRunFinishingMidRead(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20200101-000000-dddd"
	e := seedRun(t, f, id, "", "someone@example.com", true)
	f.run.SetState(e, backend.StateRunning)
	writeRecord(t, f, id, &runstore.Record{Version: 1, RunID: id, Execution: e, Status: runstore.StatusRunning, Stage: "finalize"})
	env, err := openCloud(context.Background(), cloudOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer env.Close()
	env.be = finishingBackend{Backend: env.be, finish: func(string) {
		writeRecord(t, f, id, &runstore.Record{Version: 1, RunID: id, Execution: e, Status: runstore.StatusSucceeded, Stage: "writeback"})
		f.run.SetState(e, backend.StateSucceeded)
	}}
	rows, err := loadRows(context.Background(), env, lsFilter{runRef: appSlug + "/" + id}, time.Now())
	if err != nil || len(rows) != 1 || rows[0].Status != "succeeded" {
		t.Fatalf("rows = %+v, %v", rows, err)
	}
}

// A run that already ran without a cloud execution (a local run against
// the same bucket) is not launched again by --retry (C-M4).
func TestRetryDoesNotRelaunchARunWithARecord(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abcd"
	seedRun(t, f, id, "", "someone@example.com", false)
	writeRecord(t, f, id, &runstore.Record{Version: 1, RunID: id, Status: runstore.StatusSucceeded, Stage: "writeback"})
	out, errOut, err := execute(t, "run", "--retry", id)
	if err != nil || len(f.run.Executions()) != 0 || !strings.Contains(out+errOut, "already") {
		t.Fatalf("run --retry = %q %q, %v; executions %v", out, errOut, err, f.run.Executions())
	}
}

type listRecorder struct {
	backend.Backend
	got *backend.ListFilter
}

func (l listRecorder) List(ctx context.Context, f backend.ListFilter) ([]backend.Execution, error) {
	*l.got = f
	return l.Backend.List(ctx, f)
}

// checkMaxParallel bounds its listing by Cloud Run's longest task timeout:
// no active execution can be older, so it needn't page through the
// region's whole history on every launch (C-M7).
func TestCheckMaxParallelBoundsTheListing(t *testing.T) {
	f := newCloudFixture(t)
	env := memEnv(t, f)
	var got backend.ListFilter
	env.be = listRecorder{Backend: env.be, got: &got}
	if err := checkMaxParallel(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	if !got.ActiveOnly || got.Since.IsZero() || time.Since(got.Since) < gcp.MaxTaskTimeout {
		t.Fatalf("filter = %+v", got)
	}
}

// A run whose compute was not estimated never reads as free: its row and
// the totals line say so. A run that never launched has no compute at all
// and is not counted (C-M10).
func TestLsMarksUnestimatedCompute(t *testing.T) {
	f := newCloudFixture(t)
	today := time.Now().UTC().Format("20060102")
	seedRun(t, f, today+"-090000-aaaa", "", "someone@example.com", false)
	seedRun(t, f, today+"-091000-bbbb", "", "someone@example.com", false)
	c := runstore.ModelOnlyCost(1.5, runstore.BasisAPIList)
	writeRecord(t, f, today+"-091000-bbbb", &runstore.Record{Version: 1, RunID: today + "-091000-bbbb", Status: runstore.StatusFailed, Stage: "implement", CostUSD: 1.5, Cost: &c})
	human, _, err := execute(t, "ls")
	if err != nil || strings.Count(human, "compute not estimated") != 2 || !strings.Contains(human, "compute not estimated for 1 run") {
		t.Fatalf("ls =\n%s%v", human, err)
	}
	got, _ := lsJSON(t)
	if got.Totals.ComputeNotEstimated != 1 {
		t.Fatalf("totals = %+v", got.Totals)
	}
}

// diagnose on a run that never launched shows its row, as ls would, not
// an error (C-M11).
func TestDiagnoseUnlaunchedRunShowsItsRow(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abcd"
	seedRun(t, f, id, "", "someone@example.com", false)
	out, _, err := execute(t, "diagnose", "--json", id)
	var d Diagnosis
	if err != nil || json.Unmarshal([]byte(out), &d) != nil || d.Row.Status != runview.StatusUnlaunched {
		t.Fatalf("diagnose = %s, %v", out, err)
	}
	b, _ := blob.OpenBucket(context.Background(), f.bucket)
	_ = runstore.Open(b, appSlug, id).RequestCancel(context.Background())
	b.Close()
	out, _, err = execute(t, "diagnose", id)
	if err != nil || !strings.Contains(out, "cancelled before launch") {
		t.Fatalf("diagnose = %s, %v", out, err)
	}
}

// A corrupt launch.json or result.json is that run's error row, with a
// warning; the other runs are listed as usual (queued fix).
func TestLsCorruptObjectIsAPerRowError(t *testing.T) {
	f := newCloudFixture(t)
	today := time.Now().UTC().Format("20060102")
	good, badLaunch, badRecord := today+"-090000-aaaa", today+"-091000-bbbb", today+"-092000-cccc"
	for _, id := range []string{good, badLaunch, badRecord} {
		seedRun(t, f, id, "", "someone@example.com", false)
	}
	b, _ := blob.OpenBucket(context.Background(), f.bucket)
	_ = runstore.Open(b, appSlug, badLaunch).PutFile(context.Background(), "launch.json", []byte("{not json"), "application/json")
	_ = runstore.Open(b, appSlug, badRecord).PutFile(context.Background(), "result.json", []byte(`{"status": 7}`), "application/json")
	b.Close()
	got, errOut := lsJSON(t)
	status := map[string]string{}
	for _, r := range got.Runs {
		status[r.RunID] = r.Status + ": " + r.Reason
	}
	if len(got.Runs) != 3 || !strings.HasPrefix(status[good], runview.StatusUnlaunched) ||
		!strings.Contains(status[badLaunch], "error: launch.json") || !strings.Contains(status[badRecord], "error: result.json") {
		t.Fatalf("rows = %v", status)
	}
	if strings.Count(errOut, "warning") != 2 {
		t.Fatalf("stderr = %q", errOut)
	}
	// diagnose shows the same row.
	out, _, err := execute(t, "diagnose", "--json", badRecord)
	var d Diagnosis
	if err != nil || json.Unmarshal([]byte(out), &d) != nil || d.Row.Status != runview.StatusError {
		t.Fatalf("diagnose = %s, %v", out, err)
	}
}

// ls without --repo lists a config repository that has no provider when
// the working directory is its checkout, whose fugaro.yaml names one
// (design §5.4), instead of skipping it.
func TestLsUsesTheCheckoutForAProviderlessRepo(t *testing.T) {
	testutil.IsolateGit(t)
	f := newCloudFixture(t)
	f.appendConfig(t, "  acme/other: { workflows: [svc] }\n")
	slug := mustSlug("github", "acme/other")
	id := time.Now().UTC().Format("20060102") + "-090000-aaaa"
	b, _ := blob.OpenBucket(context.Background(), f.bucket)
	_ = runstore.Open(b, slug, id).CreateTask(context.Background(), &task.Spec{Version: 1, RunID: id, Repo: "acme/other", Ref: "main", Workflow: "svc", Task: "x"})
	b.Close()
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	yaml := "version: 1\ngit: { provider: github, base_branch: main }\nworkflows:\n  svc: { base: web-node, commands: { build: sh build.sh, test: sh test.sh } }\n"
	if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	got, errOut := lsJSON(t)
	if len(got.Runs) != 1 || got.Runs[0].RunID != id || strings.Contains(errOut, "skipping") {
		t.Fatalf("ls = %+v, stderr %q", got.Runs, errOut)
	}
}

// cancel's grace floor comes from the run's own record when it carries the
// finalize reserve, with no checkout needed (queued fix; fixer 1's field).
func TestGraceFloorReadsTheRecordsReserve(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abcd"
	seedRun(t, f, id, "", "", false)
	writeRecord(t, f, id, &runstore.Record{Version: 1, RunID: id, Status: runstore.StatusRunning, Stage: "implement", FinalizeReserveS: 90})
	b, _ := blob.OpenBucket(context.Background(), f.bucket)
	defer b.Close()
	got, what := graceFloor(context.Background(), runstore.Open(b, appSlug, id), &cancelOptions{})
	if got != 90*time.Second+runnerReaction || !strings.Contains(what, "the run's finalize reserve") {
		t.Fatalf("floor = %s (%s)", got, what)
	}
}

// An object past runstore's read cap is that run's error row, like a
// corrupt one, and so is an oversized launch claim.
func TestLsOversizedObjectIsAPerRowError(t *testing.T) {
	f := newCloudFixture(t)
	today := time.Now().UTC().Format("20060102")
	bigRecord, bigClaim := today+"-090000-aaaa", today+"-091000-bbbb"
	seedRun(t, f, bigRecord, "", "someone@example.com", false)
	seedRun(t, f, bigClaim, "", "someone@example.com", false)
	huge := []byte(`{"pad":"` + strings.Repeat("x", int(runstore.MaxRecordBytes)) + `"}`)
	b, _ := blob.OpenBucket(context.Background(), f.bucket)
	_ = runstore.Open(b, appSlug, bigRecord).PutFile(context.Background(), "result.json", huge, "application/json")
	_ = runstore.Open(b, appSlug, bigClaim).PutFile(context.Background(), "launching", huge, "application/json")
	b.Close()
	got, errOut := lsJSON(t)
	if len(got.Runs) != 2 {
		t.Fatalf("rows = %+v", got.Runs)
	}
	for _, r := range got.Runs {
		if r.Status != runview.StatusError {
			t.Fatalf("row = %+v", r)
		}
	}
	if strings.Count(errOut, "warning") != 2 {
		t.Fatalf("stderr = %q", errOut)
	}
	// A launch over an oversized claim fails with a clear remote error.
	_, _, err := execute(t, "run", "--retry", bigClaim)
	if ExitCode(err) != ExitRemoteError || !strings.Contains(err.Error(), "launch claim") {
		t.Fatalf("run --retry over an oversized claim: %v", err)
	}
}
