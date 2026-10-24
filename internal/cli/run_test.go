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
	"github.com/dimipaun/fugaro/internal/recipe"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// appExecution is a full execution name of the fixture's web job.
func appExecution(short string) string {
	job := gcp.JobName(appSlug, "web")
	return backend.ExecID{GCPProject: "proj-1234", Region: "us-east5", Job: job, Name: job + "-" + short}.String()
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
	if id, ok := backend.ParseExecution(l.Execution); !ok || id.GCPProject != "proj-1234" || id.Job != gcp.JobName(appSlug, "web") || l.Job != id.Job {
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
	recorded, err := backend.CloudRunCodec().FromEnv(func(k string) string {
		return map[string]string{"CLOUD_RUN_EXECUTION": job + "-x7k2p", "CLOUD_RUN_JOB": job, "FUGARO_GCP_PROJECT": "proj-1234", "FUGARO_PROJECT": "aurora", "FUGARO_REGION": "us-east5"}[k]
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
	yaml := "version: 1\nproject: aurora\ngit: { provider: github, base_branch: develop }\nworkflows:\n  svc: { commands: { build: sh build.sh, test: sh test.sh } }\n"
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
	yaml := "version: 1\nproject: aurora\ngit: { provider: bitbucket }\nworkflows:\n  web: { commands: { build: sh build.sh, test: sh test.sh } }\n"
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
	yaml := "version: 1\nproject: aurora\ngit: { provider: github, base_branch: develop }\nworkflows:\n  svc:\n    commands: { build: sh build.sh, test: sh test.sh }\n    timeouts: { total: 2h, finalize_reserve: 5m }\n"
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

// TestAnArtifactLaunchRefusesACapSetThatCannotFitMemory is the launch's arm
// of TestOutputCapsMustFitTheWorkflowsMemory's rule (Review Focus 12): the
// same runner.RecipeGateProblems list is called in finishLaunch's pre-write
// block, after the image gates and before createTask, so an over-memory
// artifact launch is refused with nothing written at all — no task, no
// execution. The runner's own bootstrap is the third arm, for a launch whose
// recipe the launcher cannot resolve.
func TestAnArtifactLaunchRefusesACapSetThatCannotFitMemory(t *testing.T) {
	f := newCloudFixture(t)
	testutil.IsolateGit(t)
	f.run.AddJob(gcp.JobName(mustSlug("github", "acme/other"), "svc"), "2", "4Gi")
	// A job image whose runner knows mode: artifact (artifactsSince): a spec
	// carrying the mode is refused outright by an older one, so the launch
	// would be refused by that gate before it reached the memory arithmetic
	// this test is about.
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), "svc", release080)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	yaml := "version: 1\nproject: aurora\ngit: { provider: github, base_branch: develop }\n" +
		"workflows:\n  svc:\n    commands: { build: sh build.sh, test: sh test.sh, render: sh render.sh }\n" +
		"    resources: { memory: 2Gi }\n"
	testutil.WriteFiles(t, dir, map[string]string{
		"fugaro.yaml":                 yaml,
		".fugaro/recipes/sizzle.yaml": "version: 1\nname: sizzle\nmode: artifact\noutputs: { globs: [out/*.mp4] }\nsteps:\n  - render: {}\n",
	})
	t.Chdir(dir)
	_, _, err := execute(t, "run", "--recipe", "sizzle", "--run-id", "20261015-100000-a1b2", "Render the sizzle reel")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "resources.memory 2Gi") ||
		!strings.Contains(err.Error(), "5 GiB") {
		t.Fatalf("err = %v", err)
	}
	// Nothing was written: the refusal comes before createTask, so the run
	// exists neither as a task nor as an execution.
	b, _ := blob.OpenBucket(context.Background(), f.bucket)
	defer b.Close()
	if _, err := runstore.Open(b, mustSlug("github", "acme/other"), "20261015-100000-a1b2").ReadTask(context.Background()); err == nil {
		t.Fatal("the task was written before the caps were checked")
	}
	if n := len(f.run.Executions()); n != 0 {
		t.Fatalf("%d executions, want 0", n)
	}
	// A workflow whose memory holds the set launches (the same checkout, one
	// key changed): the refusal is the arithmetic, not artifact recipes.
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": strings.Replace(yaml, "memory: 2Gi", "memory: 8Gi", 1)})
	if _, _, err := execute(t, "run", "--recipe", "sizzle", "--run-id", "20261015-100000-a1b2", "Render the sizzle reel"); err != nil {
		t.Fatalf("a workflow with room for the set: %v", err)
	}
}

// backendNamed is a backend.Backend that answers with one codec and nothing
// else: launchBackend reads the seam for the backend a launch runs on, and no
// test can select cloud-batch in this build (internal/localcfg refuses the
// name until the enable task), so the name a launch would carry is stood in
// for here. The embedded interface is nil, so any other method called on it
// panics rather than answering something wrong.
type backendNamed struct {
	backend.Backend
	codec backend.Codec
}

func (b backendNamed) Codec() backend.Codec { return b.codec }

// TestAnArtifactLaunchIsRefusedOnCloudBatch is the launch's arm of Global
// Constraint 13: an artifact-producing run is never launched on cloud-batch.
// `fugaro validate` judges the checkout's own recipe offline, so the arm that
// covers a launch naming its own recipe -- the one a reviewer found missing --
// is this gate, which asks the same artifactOnBatchRefusal with the recipe the
// launch resolves, before anything is written. (A repository recipe resolves
// from this launcher's working tree, not at the task's ref the runner reads:
// TestTheArtifactLaunchGateJudgesTheWorkingTreeNotTheRef pins that residual.)
//
// Both ways a launch can carry an artifact recipe are here: the task's own
// (--recipe, a repository recipe the launcher reads from the checkout) and the
// one this checkout resolves for it (agent.recipe). The cloud-run case is the
// mutation guard: a boundary that refused every backend would refuse the one
// backend artifact runs live on (design artifact-runs.md D11).
//
// Mutation (drop the gate, or drop its backend comparison): the cloud-batch
// case stops refusing; the message assertion fails.
func TestAnArtifactLaunchIsRefusedOnCloudBatch(t *testing.T) {
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	yaml := "version: 1\nproject: aurora\ngit: { provider: github }\n" +
		"workflows:\n  svc:\n    commands: { build: sh build.sh, test: sh test.sh, render: sh render.sh }\n" +
		"    resources: { memory: 8Gi }\n"
	testutil.WriteFiles(t, dir, map[string]string{
		"fugaro.yaml":                 yaml,
		".fugaro/recipes/sizzle.yaml": "version: 1\nname: sizzle\nmode: artifact\noutputs: { globs: [out/*.mp4] }\nsteps:\n  - render: {}\n",
	})
	t.Chdir(dir)
	// agent.recipe is written after the first case, so the checkout-resolved
	// arm is a separate launch and the task's own recipe is not resolved
	// through it.
	spec := func() *task.Spec {
		return &task.Spec{Version: 1, RunID: "20261020-100000-a1b2", Repo: "acme/other",
			Workflow: "svc", Task: "Render the sizzle reel",
			Recipe: &task.Recipe{Name: "sizzle", Source: "repo"}}
	}
	env := func(name backend.Codec) *cloudEnv { return &cloudEnv{be: backendNamed{codec: name}} }
	for _, tc := range []struct {
		name string
		be   backend.Codec
		want bool
	}{
		{"cloud-batch", backend.BatchCodec(), true},
		{"cloud-run", backend.CloudRunCodec(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkArtifactLaunch(context.Background(), env(tc.be), spec())
			if tc.want {
				if err == nil || !strings.Contains(err.Error(), artifactOnBatchMessage) {
					t.Fatalf("an artifact launch on %s = %v, want the artifact boundary's refusal", tc.name, err)
				}
				if ExitCode(err) != ExitUserError {
					t.Errorf("the refusal is not a user error: %v", err)
				}
				return
			}
			if err != nil && strings.Contains(err.Error(), artifactOnBatchMessage) {
				t.Fatalf("an artifact launch on %s carries the artifact boundary's refusal, and that is the backend artifact runs live on: %v", tc.name, err)
			}
		})
	}
	// The checkout-resolved arm: a first run with no recipe in its task takes
	// agent.recipe from this checkout, which is the recipe `fugaro validate`
	// reads too -- and the same refusal follows it.
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": yaml + "agent: { recipe: sizzle }\n"})
	noRecipe := spec()
	noRecipe.Recipe = nil
	err := checkArtifactLaunch(context.Background(), env(backend.BatchCodec()), noRecipe)
	if err == nil || !strings.Contains(err.Error(), artifactOnBatchMessage) {
		t.Fatalf("an artifact launch through agent.recipe = %v, want the artifact boundary's refusal", err)
	}
}

// TestAnArtifactLaunchIsRefusedOnCloudBatchFromAnotherDirectory is the same
// boundary asked from a working directory that is not a checkout of the run's
// repository -- the hole a review found in the first version of the gate, when
// it resolved the checkout first and returned early when there was none. A task
// carrying its own recipe text needs no checkout, so that launch reached the
// backend unrefused.
//
// Three cases from the same scratch directory, on each backend:
//   - a task carrying an artifact recipe's own text: judged on the mode this
//     launcher can read (the boundary's ordinary refusal);
//   - a repository recipe: nothing here can read it, so on cloud-batch the
//     launch is refused fail-closed instead (artifactUnreadOnBatchRefusal),
//     because the runner resolves that recipe at the task's ref and an
//     artifact run must not reach a Batch VM by that path;
//   - a task carrying a non-artifact recipe's own text: refused too, and not on
//     the text it carries. The text is readable here and says mode: implement,
//     but the runner reads the repository layer at the task's ref first
//     (TestRecipeRepoLayerWins), so what the ref holds beside this name is what
//     runs -- and this directory has no checkout of that repository to ask
//     (artifactShadowedOnBatchRefusal, and
//     TestAnArtifactRecipeAtTheRefShadowsTheTasksCarriedTextOnCloudBatch for
//     the other half). The mutation guard for the fail-closed arms is a launch
//     whose ref really has no file of that name, which passes on cloud-batch:
//     see that test's second case. Cloud-run is the guard for the backend gate,
//     since cloud-run is where artifact runs live (design artifact-runs.md
//     D11).
//
// Mutation (restore the cfg == nil early return, or drop the fail-closed arm):
// the inline artifact case stops refusing; the repository-recipe case on
// cloud-batch is passed to the runner.
func TestAnArtifactLaunchIsRefusedOnCloudBatchFromAnotherDirectory(t *testing.T) {
	testutil.IsolateGit(t)
	t.Chdir(t.TempDir())
	env := func(name backend.Codec) *cloudEnv { return &cloudEnv{be: backendNamed{codec: name}} }
	inline := func(name, yaml string) *task.Spec {
		return &task.Spec{Version: 1, RunID: "20261020-100000-a1b2", Repo: "acme/other", Ref: "main",
			Workflow: "svc", Task: "Render the sizzle reel",
			Recipe: &task.Recipe{Name: name, Source: string(recipe.SourceProject),
				SHA256: recipe.Sum([]byte(yaml)), YAML: yaml}}
	}
	for _, tc := range []struct {
		name string
		spec *task.Spec
		be   backend.Codec
		want string // the refusal the launch must carry, "" for none
	}{
		{"an artifact recipe's own text on cloud-batch", inline("sizzle", projectSizzle), backend.BatchCodec(), artifactOnBatchMessage},
		{"a repository recipe on cloud-batch", &task.Spec{Version: 1, RunID: "20261020-100000-a1b2", Repo: "acme/other",
			Workflow: "svc", Task: "Render the sizzle reel", Recipe: &task.Recipe{Name: "sizzle", Source: string(recipe.SourceRepo)}}, backend.BatchCodec(), artifactUnreadOnBatchRefusal("acme/other")},
		// The carried name matches the carried text (implementRecipe says
		// name: impl), as a real launch's always does, so the refusal comes
		// from the ref read having no checkout to ask -- not from the name
		// check, which the next case pins on its own.
		{"an implement recipe's own text on cloud-batch", inline("impl", implementRecipe), backend.BatchCodec(), artifactShadowedOnBatchRefusal("impl", "acme/other", "main")},
		{"carried text whose name disagrees with the task's on cloud-batch", inline("sizzle", implementRecipe), backend.BatchCodec(), artifactShadowedOnBatchRefusal("sizzle", "acme/other", "main")},
		{"an artifact recipe's own text on cloud-run", inline("sizzle", projectSizzle), backend.CloudRunCodec(), ""},
		{"a repository recipe on cloud-run", &task.Spec{Version: 1, RunID: "20261020-100000-a1b2", Repo: "acme/other",
			Workflow: "svc", Task: "Render the sizzle reel", Recipe: &task.Recipe{Name: "sizzle", Source: string(recipe.SourceRepo)}}, backend.CloudRunCodec(), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkArtifactLaunch(context.Background(), env(tc.be), tc.spec)
			if tc.want == "" {
				if err != nil && (strings.Contains(err.Error(), artifactOnBatchMessage) || strings.Contains(err.Error(), artifactUnreadOnBatchRefusal(tc.spec.Repo))) {
					t.Fatalf("a launch with no artifact boundary to cross was refused by it: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("checkArtifactLaunch from a directory that is not the run's checkout = %v, want the refusal %q", err, tc.want)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("the refusal is not a user error: %v", err)
			}
		})
	}
}

// TestAnArtifactRecipeAtTheRefShadowsTheTasksCarriedTextOnCloudBatch: the
// task's own recipe text is not the recipe the runner will run, whatever the
// task says about where it came from. internal/runner/recipe.go's
// resolveRecipe reads the repository layer at the task's ref FIRST and only
// falls back to the task's embedded copy (TestRecipeRepoLayerWins), so a file
// of the same name at the ref IS the run's recipe, mode included. A launch
// that carries a project or catalog recipe therefore says nothing about the
// recipe that runs -- `fugaro run --recipe sizzle` from a working directory
// whose checkout is not the one that resolved that name, against a base branch
// whose .fugaro/recipes/sizzle.yaml is mode: artifact, carries the catalog's
// implement text and would run the ref's artifact recipe on a Batch VM.
//
// So the launch's arm asks the ref for a carried recipe's name, exactly as the
// runner does, and refuses the artifact one it finds there. The second case is
// the mutation guard: a name the ref has no file for really is judged on the
// text the task carries, so "refuse every launch that carries text on
// cloud-batch" fails here; the third is the backend's own guard, since cloud-run
// is where artifact runs live (design artifact-runs.md D11).
//
// Mutation (judge the carried text alone, as this gate did): the first case
// stops refusing and the artifact recipe at the ref reaches a Batch VM.
func TestAnArtifactRecipeAtTheRefShadowsTheTasksCarriedTextOnCloudBatch(t *testing.T) {
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q", "-b", "main")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	yaml := "version: 1\nproject: aurora\ngit: { provider: github }\n" +
		"workflows:\n  svc:\n    commands: { build: sh build.sh, test: sh test.sh, render: sh render.sh }\n"
	testutil.WriteFiles(t, dir, map[string]string{
		"fugaro.yaml":                 yaml,
		".fugaro/recipes/sizzle.yaml": projectSizzle,
	})
	testutil.Git(t, dir, "add", "-A")
	testutil.Git(t, dir, "commit", "-q", "-m", "an artifact recipe on the base branch")
	t.Chdir(dir)
	// The launcher resolved the name sizzle against a checkout that did not
	// have it (the file is on the ref, not in the tree), so the task carries
	// the catalog's text for it -- the shape `fugaro run --recipe` produces
	// from another directory.
	if err := os.Remove(filepath.Join(dir, ".fugaro", "recipes", "sizzle.yaml")); err != nil {
		t.Fatal(err)
	}
	carried := func(name, text string) *task.Spec {
		return &task.Spec{Version: 1, RunID: "20261020-100000-a1b2", Repo: "acme/other", Ref: "main",
			Workflow: "svc", Task: "Render the sizzle reel",
			Recipe: &task.Recipe{Name: name, Source: string(recipe.SourceCatalog),
				SHA256: recipe.Sum([]byte(text)), YAML: text}}
	}
	env := func(name backend.Codec) *cloudEnv { return &cloudEnv{be: backendNamed{codec: name}} }
	for _, tc := range []struct {
		name string
		spec *task.Spec
		be   backend.Codec
		want string
	}{
		{"the ref's artifact recipe of that name, carried implement text", carried("sizzle", implementRecipe), backend.BatchCodec(), artifactOnBatchMessage},
		{"a name the ref has no file for", carried("impl", implementRecipe), backend.BatchCodec(), ""},
		{"the ref's artifact recipe of that name on cloud-run", carried("sizzle", implementRecipe), backend.CloudRunCodec(), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkArtifactLaunch(context.Background(), env(tc.be), tc.spec)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("checkArtifactLaunch = %v, want no refusal", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("checkArtifactLaunch = %v, want the refusal %q: the runner resolves the repository layer at the ref first, so the task's carried text is not the recipe that runs", err, tc.want)
			}
			if ExitCode(err) != ExitUserError {
				t.Errorf("the refusal is not a user error: %v", err)
			}
		})
	}
}

// TestACarriedRecipeTheRefCannotBeReadForIsRefusedOnCloudBatch is the
// fail-closed half of the same boundary: a launch whose task carries its recipe
// text says nothing about the ref, and this launcher has no checkout of that
// repository to ask one of, so nothing on this path can tell whether a file of
// that name at the ref is mode: artifact. `fugaro run --recipe <a project or
// catalog name>` from a directory that is not the repository's checkout is
// exactly that launch, and it is refused rather than passed to a runner that
// would resolve the ref itself.
//
// The carried name matches the carried text's own name, as a real launch's
// always does, so the name check cannot refuse it first: what refuses it is
// the ref read finding no checkout here.
//
// Mutation (drop the ref read's error arm, or have recipeAtRef answer "not
// found" when there is no checkout root): the launch is passed, and the ref's
// artifact recipe runs on a Batch VM.
func TestACarriedRecipeTheRefCannotBeReadForIsRefusedOnCloudBatch(t *testing.T) {
	testutil.IsolateGit(t)
	t.Chdir(t.TempDir())
	spec := &task.Spec{Version: 1, RunID: "20261020-100000-a1b2", Repo: "acme/other", Ref: "main",
		Workflow: "svc", Task: "Render the sizzle reel",
		Recipe: &task.Recipe{Name: "impl", Source: string(recipe.SourceCatalog),
			SHA256: recipe.Sum([]byte(implementRecipe)), YAML: implementRecipe}}

	err := checkArtifactLaunch(context.Background(), &cloudEnv{be: backendNamed{codec: backend.BatchCodec()}}, spec)
	if err == nil || !strings.Contains(err.Error(), artifactShadowedOnBatchRefusal("impl", "acme/other", "main")) {
		t.Fatalf("a carried recipe whose ref cannot be read = %v, want the fail-closed refusal", err)
	}
	if ExitCode(err) != ExitUserError {
		t.Errorf("the refusal is not a user error: %v", err)
	}
}

// TestTheArtifactLaunchGateJudgesTheWorkingTreeNotTheRef pins what the
// launch's arm of the artifact boundary does NOT cover, so the comments that
// describe it can say exactly what it enforces: a repository recipe is judged
// by the file in this launcher's WORKING TREE (recipeForLaunch), while the
// runner reads the same file at the task's ref -- the repository's base branch
// unless the launch names another. A working tree whose recipe says
// mode: implement therefore passes the gate for a ref whose recipe says
// mode: artifact, and that is the residual the plan's Task 10 records in its
// step 5 inventory and must close before backend: cloud-batch is selectable
// (Global Constraint 13). Task 10's fix flips this test to assert the refusal;
// until then a test that passed here would be a comment claiming more than
// the code does.
//
// The control is the same launch with the working tree matching the ref: the
// gate refuses it, so what lets the first launch through is the tree/ref
// difference and nothing else.
func TestTheArtifactLaunchGateJudgesTheWorkingTreeNotTheRef(t *testing.T) {
	testutil.IsolateGit(t)
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q", "-b", "main")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	yaml := "version: 1\nproject: aurora\ngit: { provider: github }\n" +
		"workflows:\n  svc:\n    commands: { build: sh build.sh, test: sh test.sh, render: sh render.sh }\n"
	testutil.WriteFiles(t, dir, map[string]string{
		"fugaro.yaml":                 yaml,
		".fugaro/recipes/sizzle.yaml": projectSizzle,
	})
	testutil.Git(t, dir, "add", "-A")
	testutil.Git(t, dir, "commit", "-q", "-m", "an artifact recipe on the base branch")
	t.Chdir(dir)
	spec := &task.Spec{Version: 1, RunID: "20261020-100000-a1b2", Repo: "acme/other", Ref: "main",
		Workflow: "svc", Task: "Render the sizzle reel",
		Recipe: &task.Recipe{Name: "sizzle", Source: string(recipe.SourceRepo)}}
	env := &cloudEnv{be: backendNamed{codec: backend.BatchCodec()}}

	// The control: the working tree is the ref, and the gate refuses.
	if err := checkArtifactLaunch(context.Background(), env, spec); err == nil || !strings.Contains(err.Error(), artifactOnBatchMessage) {
		t.Fatalf("an artifact recipe on cloud-batch whose working tree matches the ref = %v, want the artifact boundary's refusal", err)
	}

	// The residual: the working tree says implement, the ref still says
	// artifact, and the gate judges the working tree.
	testutil.WriteFiles(t, dir, map[string]string{".fugaro/recipes/sizzle.yaml": "version: 1\nname: sizzle\nsteps:\n  - review: {}\n"})
	if err := checkArtifactLaunch(context.Background(), env, spec); err != nil && strings.Contains(err.Error(), artifactOnBatchMessage) {
		t.Fatalf("the gate refused a launch whose working-tree recipe is not mode: artifact (%v): it now reads the ref the runner reads, which closes the residual the plan's Task 10 step 5 records -- flip this test to assert the refusal and drop the sentence from Task 10's inventory and the comments in validate_batch.go and run.go", err)
	}
}

// implementRecipe is a non-artifact recipe's own text, in the shape a project
// or catalog recipe travels in (no mode: key parses as implement).
const implementRecipe = "version: 1\nname: impl\nsteps:\n  - review: {}\n"
