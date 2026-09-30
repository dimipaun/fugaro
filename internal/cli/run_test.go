package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// appExecution is a full execution name of the fixture's web job.
func appExecution(short string) string {
	job := gcp.JobName(appSlug, "web")
	return backend.ExecID{Project: "proj-1234", Region: "us-east5", Job: job, Name: job + "-" + short}.String()
}

func TestRunLaunches(t *testing.T) {
	f := newCloudFixture(t)
	var env map[string]string
	f.run.OnRun = func(c gcpfake.RunCall) { env = c.Env }
	out, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20260927-100000-abcd", "--batch", "tuesday", "--json", "Add a feature")
	if err != nil {
		t.Fatal(err)
	}
	var res launchResult
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Status != "launched" || res.Branch != "fugaro/20260927-100000-abcd" {
		t.Fatalf("result = %+v (%s), %v", res, out, err)
	}
	if env["FUGARO_RUN"] != appSlug+"/20260927-100000-abcd" {
		t.Fatalf("FUGARO_RUN = %q", env["FUGARO_RUN"])
	}
	b, _ := blob.OpenBucket(context.Background(), f.bucket)
	defer b.Close()
	s := runstore.Open(b, appSlug, "20260927-100000-abcd")
	spec, err := s.ReadTask(context.Background())
	if err != nil || spec.RequestedBy != "someone@example.com" || spec.Batch != "tuesday" || spec.Ref != "main" || spec.Workflow != "web" {
		t.Fatalf("task = %+v, %v", spec, err)
	}
	l, err := s.ReadLaunch(context.Background())
	if err != nil || l.Execution != res.Execution {
		t.Fatalf("launch = %+v, %v", l, err)
	}
	// launch.json holds the backend's canonical full name, never a short one.
	if id, ok := backend.ParseExecution(l.Execution); !ok || id.Project != "proj-1234" || id.Job != gcp.JobName(appSlug, "web") || l.Job != id.Job {
		t.Fatalf("launch.json execution %q, job %q", l.Execution, l.Job)
	}
}

func TestRunHumanOutput(t *testing.T) {
	newCloudFixture(t)
	out, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20260927-100000-abcd", "Add a feature")
	if err != nil {
		t.Fatal(err)
	}
	want := "launched " + appSlug + "/20260927-100000-abcd\n  branch fugaro/20260927-100000-abcd\n  logs https://"
	if !strings.HasPrefix(out, want) {
		t.Fatalf("output:\n%s\nwant prefix:\n%s", out, want)
	}
}

func TestRunIdempotentRunID(t *testing.T) {
	f := newCloudFixture(t)
	args := []string{"run", "--repo", "acme/app", "--run-id", "20260927-100000-abcd", "--json", "Add a feature"}
	if _, _, err := execute(t, args...); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, args...)
	if err != nil || !strings.Contains(out, `"already-launched"`) {
		t.Fatalf("second run: %s, %v", out, err)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want 1", n)
	}
	_, _, err = execute(t, "run", "--repo", "acme/app", "--run-id", "20260927-100000-abcd", "A different task")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "different task") {
		t.Fatalf("different task: %v", err)
	}
}

// TestRunConcurrentSameRunID is a smoke test on top of the deterministic
// race tests below: eight launches of one run ID start one execution.
func TestRunConcurrentSameRunID(t *testing.T) {
	f := newCloudFixture(t)
	env := memEnv(t, f)
	spec := &task.Spec{Version: 1, RunID: "20260927-100000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x"}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := *spec
			_ = runstore.Open(env.bucket.Bucket, appSlug, s.RunID).CreateTask(context.Background(), &s)
			_, _ = launchRun(context.Background(), env, appSlug, &s, time.Now())
		}()
	}
	wg.Wait()
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions for one run ID, want 1", n)
	}
}

func setHooks(t *testing.T, before, takeover, after func()) {
	t.Helper()
	launchHooks.beforeClaim, launchHooks.beforeTakeover, launchHooks.afterClaim = before, takeover, after
	t.Cleanup(func() { launchHooks.beforeClaim, launchHooks.beforeTakeover, launchHooks.afterClaim = nil, nil, nil })
	shortWait(t)
}

// shortWait shrinks the loser's wait for launch.json. Tests that touch
// these package variables (or launchHooks) must not use t.Parallel.
func shortWait(t *testing.T) {
	t.Helper()
	w, p := claimWait, claimPoll
	claimWait, claimPoll = 100*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { claimWait, claimPoll = w, p })
}

func raceSpec(t *testing.T, env *cloudEnv) *task.Spec {
	t.Helper()
	spec := &task.Spec{Version: 1, RunID: "20260927-100000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x"}
	if err := runstore.Open(env.bucket.Bucket, appSlug, spec.RunID).CreateTask(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	return spec
}

// The winner launches completely between the loser's first check and its
// Claim. The claim was never cleared, so the
// loser's Claim fails and it finds launch.json.
func TestLaunchWinnerFinishesBeforeLoserClaims(t *testing.T) {
	f := newCloudFixture(t)
	env := memEnv(t, f)
	spec := raceSpec(t, env)
	now := time.Now()
	var winner launchResult
	setHooks(t, func() {
		launchHooks.beforeClaim = nil // the winner runs without hooks
		var err error
		if winner, err = launchRun(context.Background(), env, appSlug, spec, now); err != nil {
			t.Errorf("winner: %v", err)
		}
	}, nil, nil)
	loser, err := launchRun(context.Background(), env, appSlug, spec, now)
	if err != nil || loser.Status != "already-launched" || winner.Status != "launched" || loser.Execution != winner.Execution {
		t.Fatalf("winner %+v, loser %+v, %v", winner, loser, err)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want 1", n)
	}
}

// Two CLIs find the same stale claim. The first to replace it launches;
// the other's generation-matched replace fails, and it reports the launch.
func TestLaunchStaleClaimTakeoverHasOneWinner(t *testing.T) {
	f := newCloudFixture(t)
	env := memEnv(t, f)
	spec := raceSpec(t, env)
	now := time.Now()
	s := runstore.Open(env.bucket.Bucket, appSlug, spec.RunID)
	if ok, _, err := s.Claim(context.Background(), "dead-laptop/1/1", now.Add(-2*claimTTL)); !ok || err != nil {
		t.Fatal(ok, err)
	}
	setHooks(t, nil, func() {
		launchHooks.beforeTakeover = nil
		if res, err := launchRun(context.Background(), env, appSlug, spec, now); err != nil || res.Status != "launched" {
			t.Errorf("first taker: %+v, %v", res, err)
		}
	}, nil)
	second, err := launchRun(context.Background(), env, appSlug, spec, now)
	if err != nil || second.Status != "already-launched" {
		t.Fatalf("second taker: %+v, %v", second, err)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want 1", n)
	}
}

// An earlier, now-stale holder launched and wrote launch.json just before
// our takeover: the re-check after taking the claim catches it.
func TestLaunchRechecksAfterTakingTheClaim(t *testing.T) {
	f := newCloudFixture(t)
	env := memEnv(t, f)
	spec := raceSpec(t, env)
	s := runstore.Open(env.bucket.Bucket, appSlug, spec.RunID)
	setHooks(t, nil, nil, func() {
		_ = s.WriteLaunch(context.Background(), &runstore.Launch{Version: 1, RunID: spec.RunID, Execution: appExecution("late")})
	})
	res, err := launchRun(context.Background(), env, appSlug, spec, time.Now())
	if err != nil || res.Status != "already-launched" || len(f.run.Executions()) != 0 {
		t.Fatalf("res = %+v, err = %v, executions %v", res, err, f.run.Executions())
	}
}

// The same re-check catches a runner's result.json that appeared while we
// took a stale claim over (the earlier holder's launch.json write failed).
func TestLaunchRechecksRecordAfterTakeover(t *testing.T) {
	f := newCloudFixture(t)
	env := memEnv(t, f)
	spec := raceSpec(t, env)
	now := time.Now()
	s := runstore.Open(env.bucket.Bucket, appSlug, spec.RunID)
	if ok, _, _ := s.Claim(context.Background(), "dead-laptop/1/1", now.Add(-2*claimTTL)); !ok {
		t.Fatal("seed claim")
	}
	setHooks(t, nil, nil, func() {
		_ = s.WriteRecord(context.Background(), &runstore.Record{Version: 1, RunID: spec.RunID, Execution: appExecution("slow"), Status: runstore.StatusRunning})
	})
	res, err := launchRun(context.Background(), env, appSlug, spec, now)
	if err != nil || res.Status != "already-launched" || res.Execution != appExecution("slow") || len(f.run.Executions()) != 0 {
		t.Fatalf("res = %+v, err = %v, executions %v", res, err, f.run.Executions())
	}
}

// After we judge the claim stale, another CLI replaces it with a fresh
// one. Our takeover must fail on the generation it read, and nobody may
// launch while the fresh holder is (supposedly) launching.
func TestLaunchStaleJudgementRacesAFreshClaim(t *testing.T) {
	f := newCloudFixture(t)
	env := memEnv(t, f)
	spec := raceSpec(t, env)
	now := time.Now()
	s := runstore.Open(env.bucket.Bucket, appSlug, spec.RunID)
	if ok, _, _ := s.Claim(context.Background(), "dead-laptop/1/1", now.Add(-2*claimTTL)); !ok {
		t.Fatal("seed claim")
	}
	setHooks(t, nil, func() {
		fresh, _ := json.Marshal(runstore.Claim{Holder: "other-laptop/2/2", At: now})
		if err := env.bucket.WriteAll(context.Background(), s.ClaimKey(), fresh, nil); err != nil {
			t.Fatal(err)
		}
	}, nil)
	_, err := launchRun(context.Background(), env, appSlug, spec, now)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "in flight") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want 0", n)
	}
}

// The takeover race itself: Claim's own read saw a stale claim, but before our
// generation-carrying read another CLI replaced it with a fresh one. The
// staleness judgement must come from that read, so we wait, and nobody
// launches while the fresh holder is (supposedly) launching.
func TestLaunchStaleClaimTurnsFreshBeforeTheRead(t *testing.T) {
	f := newCloudFixture(t)
	env := memEnv(t, f)
	spec := raceSpec(t, env)
	now := time.Now()
	s := runstore.Open(env.bucket.Bucket, appSlug, spec.RunID)
	if ok, _, _ := s.Claim(context.Background(), "dead-laptop/1/1", now.Add(-2*claimTTL)); !ok {
		t.Fatal("seed claim")
	}
	shortWait(t)
	launchHooks.beforeRead = func() {
		fresh, _ := json.Marshal(runstore.Claim{Holder: "other-laptop/2/2", At: now})
		_ = env.bucket.WriteAll(context.Background(), s.ClaimKey(), fresh, nil)
	}
	t.Cleanup(func() { launchHooks.beforeRead = nil })
	_, err := launchRun(context.Background(), env, appSlug, spec, now)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "in flight") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want 0", n)
	}
}

// A claim released between our Claim and our read counts as absent,
// and we take it.
func TestLaunchClaimReleasedBeforeTheRead(t *testing.T) {
	f := newCloudFixture(t)
	env := memEnv(t, f)
	spec := raceSpec(t, env)
	s := runstore.Open(env.bucket.Bucket, appSlug, spec.RunID)
	shortWait(t)
	calls := 0
	launchHooks.beforeRead = func() {
		if calls++; calls == 1 {
			_ = env.bucket.Delete(context.Background(), s.ClaimKey()) // the holder's launch was rejected; it released
		}
	}
	t.Cleanup(func() { launchHooks.beforeRead = nil })
	if ok, _, _ := s.Claim(context.Background(), "rejected/1/1", time.Now()); !ok {
		t.Fatal("seed claim")
	}
	res, err := launchRun(context.Background(), env, appSlug, spec, time.Now())
	if err != nil || res.Status != "launched" || len(f.run.Executions()) != 1 {
		t.Fatalf("res = %+v, err = %v, executions %v", res, err, f.run.Executions())
	}
}

// A loser that finds a fresh claim waits for the winner's launch.json
// and reports it, exit 0.
func TestLaunchLoserReportsWinnersLaunch(t *testing.T) {
	f := newCloudFixture(t)
	env := memEnv(t, f)
	spec := raceSpec(t, env)
	shortWait(t)
	claimWait = 2 * time.Second
	s := runstore.Open(env.bucket.Bucket, appSlug, spec.RunID)
	_, _, _ = s.Claim(context.Background(), "winner/1/1", time.Now())
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = s.WriteLaunch(context.Background(), &runstore.Launch{Version: 1, RunID: spec.RunID, Execution: appExecution("w")})
	}()
	res, err := launchRun(context.Background(), env, appSlug, spec, time.Now())
	if err != nil || res.Status != "already-launched" || !strings.HasSuffix(res.Execution, "-w") {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
}

func TestLaunchRejectedReleasesOurClaim(t *testing.T) {
	f := newCloudFixture(t)
	env := memEnv(t, f)
	spec := raceSpec(t, env)
	spec.Workflow = "missing" // no such job: Launch fails with ErrNotFound + ErrRejected
	_, err := launchRun(context.Background(), env, appSlug, spec, time.Now())
	if ExitCode(err) != ExitRemoteError || !errors.Is(err, backend.ErrRejected) || !errors.Is(err, backend.ErrNotFound) || !strings.Contains(err.Error(), "nothing started") ||
		!strings.Contains(err.Error(), "fugaro run --retry "+appSlug+"/"+spec.RunID) {
		t.Fatalf("err = %v", err)
	}
	spec.Workflow = "web"
	if res, err := launchRun(context.Background(), env, appSlug, spec, time.Now()); err != nil || res.Status != "launched" {
		t.Fatalf("retry right after a rejected launch: %+v, %v", res, err)
	}
}

// An ambiguous error (5xx, timeout) may have started an execution, so
// the claim stays and an immediate retry is refused.
func TestLaunchAmbiguousFailureKeepsTheClaim(t *testing.T) {
	f := newCloudFixture(t)
	env := memEnv(t, f)
	spec := raceSpec(t, env)
	shortWait(t)
	f.run.FailRunWith = 503
	if _, err := launchRun(context.Background(), env, appSlug, spec, time.Now()); ExitCode(err) != ExitRemoteError || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("err = %v", err)
	}
	f.run.FailRunWith = 0
	if _, err := launchRun(context.Background(), env, appSlug, spec, time.Now()); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "in flight") {
		t.Fatalf("immediate retry after an ambiguous failure: %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions", n)
	}
}

// :run succeeded but its metadata can't be read. The
// execution exists, so this is ambiguous too, and the claim stays.
func TestLaunchUnreadableMetadataKeepsTheClaim(t *testing.T) {
	f := newCloudFixture(t)
	env := memEnv(t, f)
	spec := raceSpec(t, env)
	shortWait(t)
	f.run.BadRunMetadata = true
	if _, err := launchRun(context.Background(), env, appSlug, spec, time.Now()); ExitCode(err) != ExitRemoteError {
		t.Fatalf("err = %v", err)
	}
	f.run.BadRunMetadata = false
	if _, err := launchRun(context.Background(), env, appSlug, spec, time.Now()); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "in flight") {
		t.Fatalf("immediate retry: %v", err)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want the 1 the first call started", n)
	}
}

// releaseClaim deletes only the claim it holds, so a refused launcher
// never removes another CLI's fresh claim.
func TestReleaseClaimLeavesAForeignClaim(t *testing.T) {
	f := newCloudFixture(t)
	env := memEnv(t, f)
	spec := raceSpec(t, env)
	s := runstore.Open(env.bucket.Bucket, appSlug, spec.RunID)
	if ok, _, _ := s.Claim(context.Background(), "someone-else/9/9", time.Now()); !ok {
		t.Fatal("seed claim")
	}
	releaseClaim(context.Background(), env, s, "me/1/1")
	if ok, _ := env.bucket.Exists(context.Background(), s.ClaimKey()); !ok {
		t.Fatal("releaseClaim deleted a claim it doesn't hold")
	}
	releaseClaim(context.Background(), env, s, "someone-else/9/9")
	if ok, _ := env.bucket.Exists(context.Background(), s.ClaimKey()); ok {
		t.Fatal("releaseClaim left its own claim")
	}
}

func TestRetryBackfillsLaunchFromRecord(t *testing.T) {
	f := newCloudFixture(t)
	env := memEnv(t, f)
	ctx := context.Background()
	spec := raceSpec(t, env)
	s := runstore.Open(env.bucket.Bucket, appSlug, spec.RunID)
	// The CLI died after jobs.run: no launch.json, but the runner already
	// wrote result.json, with the name exactly as fugaro exec builds it from
	// Cloud Run's environment.
	job := gcp.JobName(appSlug, "web")
	recorded, err := backend.ExecutionFromEnv(func(k string) string {
		return map[string]string{"CLOUD_RUN_EXECUTION": job + "-x7k2p", "CLOUD_RUN_JOB": job, "FUGARO_PROJECT": "proj-1234", "FUGARO_REGION": "us-east5"}[k]
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.WriteRecord(ctx, &runstore.Record{Version: 1, RunID: spec.RunID, Execution: recorded, Status: runstore.StatusRunning, Stage: "implement", Outcome: runstore.OutcomeNone})
	res, err := launchRun(ctx, env, appSlug, spec, time.Now())
	if err != nil || res.Status != "already-launched" || len(f.run.Executions()) != 0 {
		t.Fatalf("res = %+v, err = %v, executions = %v", res, err, f.run.Executions())
	}
	l, err := s.ReadLaunch(ctx)
	if err != nil || l.Execution != recorded || l.Job != job {
		t.Fatalf("backfilled launch = %+v, %v", l, err)
	}
	// The backfilled name works against the backend: diagnose, cancel and logs rely on that.
	if _, err := env.be.Execution(ctx, l.Execution); err != nil && !errors.Is(err, backend.ErrNotFound) {
		t.Fatalf("the backend rejects the recorded name: %v", err)
	}
}

func TestRetryRefusesFreshClaim(t *testing.T) {
	f := newCloudFixture(t)
	env := memEnv(t, f)
	ctx := context.Background()
	spec := &task.Spec{Version: 1, RunID: "20260927-100000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x"}
	s := runstore.Open(env.bucket.Bucket, appSlug, spec.RunID)
	_ = s.CreateTask(ctx, spec)
	shortWait(t)
	now := time.Now()
	_, _, _ = s.Claim(ctx, "other-laptop/1", now.Add(-time.Minute))
	if _, err := launchRun(ctx, env, appSlug, spec, now); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "in flight") {
		t.Fatalf("fresh claim: %v", err)
	}
	if _, err := launchRun(ctx, env, appSlug, spec, now.Add(claimTTL)); err != nil || len(f.run.Executions()) != 1 {
		t.Fatalf("stale claim: %v, %d executions", err, len(f.run.Executions()))
	}
}

func TestRunRetryUnlaunched(t *testing.T) {
	f := newCloudFixture(t)
	t.Chdir(t.TempDir())
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	spec := &task.Spec{Version: 1, RunID: "20260927-100000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x"}
	_ = runstore.Open(b, appSlug, spec.RunID).CreateTask(ctx, spec)
	if _, _, err := execute(t, "run", "--retry", "20260927-100000-abcd"); err != nil {
		t.Fatal(err)
	}
	if len(f.run.Executions()) != 1 {
		t.Fatal("--retry did not launch")
	}
	_ = runstore.Open(b, appSlug, "20260927-100000-ffff").CreateTask(ctx, &task.Spec{Version: 1, RunID: "20260927-100000-ffff", Repo: "acme/app", Ref: "main", Task: "y"})
	_ = runstore.Open(b, appSlug, "20260927-100000-ffff").RequestCancel(ctx)
	if _, _, err := execute(t, "run", "--retry", "20260927-100000-ffff"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("retry of a cancelled run: %v", err)
	}
}

func TestRunRetryResolvesAMissingWorkflow(t *testing.T) {
	f := newCloudFixture(t)
	t.Chdir(t.TempDir())
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	_ = runstore.Open(b, appSlug, "20260927-100000-ffff").CreateTask(ctx, &task.Spec{Version: 1, RunID: "20260927-100000-ffff", Repo: "acme/app", Ref: "main", Task: "y"})
	if _, _, err := execute(t, "run", "--retry", appSlug+"/20260927-100000-ffff"); err != nil {
		t.Fatal(err)
	}
	if len(f.run.Executions()) != 1 {
		t.Fatal("--retry did not launch the local config's workflow")
	}
}

func TestRunRetryErrors(t *testing.T) {
	newCloudFixture(t)
	t.Chdir(t.TempDir())
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"run", "--retry", "20260927-100000-abcd", "--repo", "acme/app"}, "--retry"},
		{[]string{"run", "--retry", "20260927-100000-abcd", "some text"}, "--retry"},
		{[]string{"run", "--retry", "20260927-100000-abcd"}, "no run"},
		{[]string{"run", "--retry", "not-a-run"}, "run ID"},
	} {
		if _, _, err := execute(t, tc.args...); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: %v", tc.args, err)
		}
	}
}

func TestRunMaxParallel(t *testing.T) {
	newCloudFixture(t) // max_parallel: 2
	for i, id := range []string{"20260927-100000-aaaa", "20260927-100000-bbbb"} {
		if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", id, "task"); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20260927-100000-cccc", "task")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "max_parallel") {
		t.Fatalf("third run: %v", err)
	}
	// A repeat of a launched run ID is not a new run: the check is skipped.
	if out, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20260927-100000-aaaa", "--json", "task"); err != nil || !strings.Contains(out, "already-launched") {
		t.Fatalf("repeat at max_parallel: %s, %v", out, err)
	}
}

func TestRunNeedsExactlyOneTaskSource(t *testing.T) {
	newCloudFixture(t)
	if _, _, err := execute(t, "run", "--repo", "acme/app"); ExitCode(err) != ExitUserError {
		t.Fatalf("no task: %v", err)
	}
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--task-file", "-", "text too"); ExitCode(err) != ExitUserError {
		t.Fatalf("two task sources: %v", err)
	}
	if _, _, err := executeStdin(t, "  \n", "run", "--repo", "acme/app", "--task-file", "-"); ExitCode(err) != ExitUserError {
		t.Fatalf("empty task: %v", err)
	}
}

func TestRunTaskFromStdin(t *testing.T) {
	f := newCloudFixture(t)
	if _, _, err := executeStdin(t, "Fix the bug\n", "run", "--repo", "acme/app", "--run-id", "20260927-100000-abcd", "--task-file", "-"); err != nil {
		t.Fatal(err)
	}
	b, _ := blob.OpenBucket(context.Background(), f.bucket)
	defer b.Close()
	spec, err := runstore.Open(b, appSlug, "20260927-100000-abcd").ReadTask(context.Background())
	if err != nil || strings.TrimSpace(spec.Task) != "Fix the bug" {
		t.Fatalf("task = %+v, %v", spec, err)
	}
}

// Without --repo, --workflow and --ref, a checkout whose origin is the repo
// supplies all three, from its origin and fugaro.yaml.
func TestRunResolvesFromTheCheckout(t *testing.T) {
	f := newCloudFixture(t)
	testutil.IsolateGit(t)
	f.run.AddJob(gcp.JobName(mustSlug("github", "acme/other"), "svc"), "2", "4Gi")
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	yaml := "version: 1\nproject: aurora\ngit: { provider: github, base_branch: develop }\nworkflows:\n  svc: { base: web-node, commands: { build: sh build.sh, test: sh test.sh } }\n"
	if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if _, _, err := execute(t, "run", "--run-id", "20260927-100000-abcd", "A task"); err != nil {
		t.Fatal(err)
	}
	b, _ := blob.OpenBucket(context.Background(), f.bucket)
	defer b.Close()
	spec, err := runstore.Open(b, mustSlug("github", "acme/other"), "20260927-100000-abcd").ReadTask(context.Background())
	if err != nil || spec.Repo != "acme/other" || spec.Workflow != "svc" || spec.Ref != "develop" {
		t.Fatalf("task = %+v, %v", spec, err)
	}
	// Another repo's run doesn't read this checkout's fugaro.yaml.
	if _, _, err := execute(t, "run", "--repo", "acme/third", "A task"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "git provider") {
		t.Fatalf("unknown repo: %v", err)
	}
	f.appendConfig(t, "  acme/two: { provider: github, workflows: [a, b] }\n")
	if _, _, err := execute(t, "run", "--repo", "acme/two", "A task"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "--workflow") {
		t.Fatalf("two workflows: %v", err)
	}
}

// A plain repeat of --run-id must not launch a run that was cancelled
// before it launched, any more than --retry may.
func TestLaunchRefusesACancelledRun(t *testing.T) {
	f := newCloudFixture(t)
	env := memEnv(t, f)
	spec := raceSpec(t, env)
	_ = runstore.Open(env.bucket.Bucket, appSlug, spec.RunID).RequestCancel(context.Background())
	if _, err := launchRun(context.Background(), env, appSlug, spec, time.Now()); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want 0", n)
	}
}

func TestRunRefusesACancelledRunID(t *testing.T) {
	f := newCloudFixture(t)
	b, _ := blob.OpenBucket(context.Background(), f.bucket)
	defer b.Close()
	spec := &task.Spec{Version: 1, RunID: "20260927-100000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "Add a feature", RequestedBy: "someone@example.com"}
	s := runstore.Open(b, appSlug, spec.RunID)
	_ = s.CreateTask(context.Background(), spec)
	_ = s.RequestCancel(context.Background())
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", spec.RunID, "Add a feature"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want 0", n)
	}
}

// A loser waiting on a claim whose holder was refused (and released it)
// claims at once instead of waiting out claimWait and claimTTL.
func TestLaunchLoserRetriesAReleasedClaim(t *testing.T) {
	f := newCloudFixture(t)
	env := memEnv(t, f)
	spec := raceSpec(t, env)
	shortWait(t)
	claimWait = 5 * time.Second
	s := runstore.Open(env.bucket.Bucket, appSlug, spec.RunID)
	if ok, _, _ := s.Claim(context.Background(), "refused/1/1", time.Now()); !ok {
		t.Fatal("seed claim")
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		_ = env.bucket.Delete(context.Background(), s.ClaimKey())
	}()
	start := time.Now()
	res, err := launchRun(context.Background(), env, appSlug, spec, time.Now())
	if err != nil || res.Status != "launched" || len(f.run.Executions()) != 1 {
		t.Fatalf("res = %+v, err = %v, executions %v", res, err, f.run.Executions())
	}
	if d := time.Since(start); d > 4*time.Second {
		t.Fatalf("took %s: waited out claimWait", d)
	}
}

// On GCS the takeover matches the generation it read, not the content: a
// stale claim rewritten with the same bytes (a new generation) between our
// read and our replace is not ours to take (the path production uses).
func TestLaunchStaleTakeoverOnGCSMatchesGeneration(t *testing.T) {
	t.Run("takeover", func(t *testing.T) {
		f := newCloudFixture(t)
		env := gcsEnv(t, f)
		spec := raceSpec(t, env)
		now := time.Now()
		s := runstore.Open(env.bucket.Bucket, appSlug, spec.RunID)
		if ok, _, err := s.Claim(context.Background(), "dead-laptop/1/1", now.Add(-2*claimTTL)); !ok || err != nil {
			t.Fatal(ok, err)
		}
		if res, err := launchRun(context.Background(), env, appSlug, spec, now); err != nil || res.Status != "launched" || len(f.run.Executions()) != 1 {
			t.Fatalf("res = %+v, err = %v", res, err)
		}
	})
	t.Run("same bytes, new generation", func(t *testing.T) {
		f := newCloudFixture(t)
		env := gcsEnv(t, f)
		spec := raceSpec(t, env)
		now := time.Now()
		s := runstore.Open(env.bucket.Bucket, appSlug, spec.RunID)
		if ok, _, err := s.Claim(context.Background(), "dead-laptop/1/1", now.Add(-2*claimTTL)); !ok || err != nil {
			t.Fatal(ok, err)
		}
		setHooks(t, nil, func() {
			data, _, err := env.bucket.Read(context.Background(), s.ClaimKey())
			if err != nil {
				t.Fatal(err)
			}
			if err := env.bucket.WriteAll(context.Background(), s.ClaimKey(), data, nil); err != nil {
				t.Fatal(err)
			}
		}, nil)
		if _, err := launchRun(context.Background(), env, appSlug, spec, now); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "in flight") {
			t.Fatalf("err = %v", err)
		}
		if n := len(f.run.Executions()); n != 0 {
			t.Fatalf("%d executions, want 0", n)
		}
	})
}

// --retry of a run that did launch reports it, and says where to go next.
func TestRunRetryOfALaunchedRunHints(t *testing.T) {
	newCloudFixture(t)
	t.Chdir(t.TempDir())
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20260927-100000-abcd", "Add a feature"); err != nil {
		t.Fatal(err)
	}
	out, errOut, err := execute(t, "run", "--retry", "20260927-100000-abcd")
	if err != nil || !strings.HasPrefix(out, "already launched") || !strings.Contains(errOut, "fugaro diagnose "+appSlug+"/20260927-100000-abcd") {
		t.Fatalf("out %q, stderr %q, err %v", out, errOut, err)
	}
}

// The local config and the checkout's fugaro.yaml must agree on the
// provider: the slug, and so the run's whole storage prefix, depends on it.
func TestRunProviderMismatch(t *testing.T) {
	newCloudFixture(t)
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@bitbucket.org:acme/app.git")
	yaml := "version: 1\nproject: aurora\ngit: { provider: bitbucket }\nworkflows:\n  web: { base: web-node, commands: { build: sh build.sh, test: sh test.sh } }\n"
	if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if _, _, err := execute(t, "run", "--run-id", "20260927-100000-abcd", "A task"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "agree") {
		t.Fatalf("err = %v", err)
	}
}

// timeoutCheckout makes the working directory a checkout of acme/other whose
// only workflow has a 5m finalize reserve.
func timeoutCheckout(t *testing.T, f *cloudFixture) {
	t.Helper()
	testutil.IsolateGit(t)
	f.run.AddJob(gcp.JobName(mustSlug("github", "acme/other"), "svc"), "2", "4Gi")
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	yaml := "version: 1\nproject: aurora\ngit: { provider: github, base_branch: develop }\nworkflows:\n  svc:\n    base: web-node\n    commands: { build: sh build.sh, test: sh test.sh }\n    timeouts: { total: 2h, finalize_reserve: 5m }\n"
	if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
}

func TestRunTotalTimeoutFlag(t *testing.T) {
	f := newCloudFixture(t)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20260927-100000-abcd", "--total-timeout", "45m", "A task"); err != nil {
		t.Fatal(err)
	}
	b, _ := blob.OpenBucket(context.Background(), f.bucket)
	defer b.Close()
	spec, err := runstore.Open(b, appSlug, "20260927-100000-abcd").ReadTask(context.Background())
	if err != nil || spec.Overrides.TotalTimeout != "45m0s" {
		t.Fatalf("task = %+v, %v", spec, err)
	}
	if got := f.run.RunRequests(); len(got) != 1 || got[0].Timeout != "2820s" {
		t.Fatalf("requests = %+v, want timeout 2820s", got)
	}
}

func TestRunTotalTimeoutValidation(t *testing.T) {
	f := newCloudFixture(t)
	timeoutCheckout(t, f)
	for _, tc := range []struct{ in, want string }{
		{"0", "greater than 0"},
		{"-1m", "greater than 0"},
		{"25h", "24h"},
		{"abc", "duration"},
		{"2m", "finalize_reserve"},
		{"5m", "finalize_reserve"},
	} {
		_, _, err := execute(t, "run", "--total-timeout", tc.in, "A task")
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("--total-timeout %s: %v", tc.in, err)
		}
	}
	if n := len(f.run.RunRequests()); n != 0 {
		t.Fatalf("%d launches after refused timeouts", n)
	}
	if _, _, err := execute(t, "run", "--total-timeout", "10m", "A task"); err != nil {
		t.Fatalf("10m against a 5m reserve: %v", err)
	}
}

func TestRetryKeepsTimeoutOverride(t *testing.T) {
	f := newCloudFixture(t)
	f.run.FailRunWith = 400
	const id = "20260927-100000-abcd"
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", id, "--total-timeout", "45m", "A task"); !errors.Is(err, backend.ErrRejected) {
		t.Fatalf("first launch: %v", err)
	}
	f.run.FailRunWith = 0
	if _, _, err := execute(t, "run", "--retry", id); err != nil {
		t.Fatal(err)
	}
	got := f.run.RunRequests()
	if len(got) != 2 || got[0].Timeout != "2820s" || got[1].Timeout != "2820s" {
		t.Fatalf("requests = %+v, want timeout 2820s twice", got)
	}
}

func TestRetryRefusesTotalTimeout(t *testing.T) {
	newCloudFixture(t)
	t.Chdir(t.TempDir())
	_, _, err := execute(t, "run", "--retry", "20260927-100000-abcd", "--total-timeout", "45m")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "--total-timeout") {
		t.Fatalf("err = %v", err)
	}
}
