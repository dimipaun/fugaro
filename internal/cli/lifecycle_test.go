package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
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
