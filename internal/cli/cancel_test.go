package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/runstore"
)

func cancelled(t *testing.T, f *cloudFixture, id string) bool {
	b, _ := blob.OpenBucket(context.Background(), f.bucket)
	defer b.Close()
	ok, _ := runstore.Open(b, appSlug, id).CancelRequested(context.Background())
	return ok
}

func TestCancelRunnerFinalizesInGrace(t *testing.T) {
	f := newCloudFixture(t)
	exec := seedRun(t, f, "20260927-100000-abcd", "", "", true)
	f.run.SetState(exec, backend.StateRunning)
	go func() { time.Sleep(50 * time.Millisecond); f.run.SetState(exec, backend.StateSucceeded) }()
	out, _, err := execute(t, "cancel", "--json", "--grace", "5s", "--poll", "10ms", "20260927-100000-abcd")
	var res cancelResult
	_ = json.Unmarshal([]byte(out), &res)
	if err != nil || res.Status != "finalized" || res.Hard || !cancelled(t, f, "20260927-100000-abcd") {
		t.Fatalf("cancel = %+v, %v", res, err)
	}
}

func TestCancelHardAfterGrace(t *testing.T) {
	f := newCloudFixture(t)
	exec := seedRun(t, f, "20260927-100000-abcd", "", "", true)
	f.run.SetState(exec, backend.StateRunning)
	out, _, err := execute(t, "cancel", "--json", "--grace", "30ms", "--poll", "10ms", "20260927-100000-abcd")
	var res cancelResult
	_ = json.Unmarshal([]byte(out), &res)
	if err != nil || res.Status != "cancelled" || !res.Hard {
		t.Fatalf("cancel = %+v, %v", res, err)
	}
	if e := f.run.State(exec); e != backend.StateCancelled {
		t.Fatalf("execution state = %s", e)
	}
}

func writeStage(t *testing.T, f *cloudFixture, id, exec, stage string) {
	t.Helper()
	b, _ := blob.OpenBucket(context.Background(), f.bucket)
	defer b.Close()
	_ = runstore.Open(b, appSlug, id).WriteRecord(context.Background(), &runstore.Record{Version: 1, RunID: id, Execution: exec,
		Status: runstore.StatusRunning, Stage: stage, Outcome: runstore.OutcomeNone})
}

func TestCancelCountsWritebackAsFinalized(t *testing.T) {
	f := newCloudFixture(t)
	exec := seedRun(t, f, "20260927-100000-abcd", "", "", true)
	f.run.SetState(exec, backend.StateRunning)
	writeStage(t, f, "20260927-100000-abcd", exec, "writeback")
	out, _, err := execute(t, "cancel", "--json", "--grace", "30ms", "--poll", "10ms", "20260927-100000-abcd")
	var res cancelResult
	_ = json.Unmarshal([]byte(out), &res)
	if err != nil || res.Status != "finalized" || res.Hard || f.run.State(exec) != backend.StateRunning {
		t.Fatalf("cancel during writeback = %+v, %v, state %s", res, err, f.run.State(exec))
	}
}

func TestCancelNeverHardCancelsDuringFinalize(t *testing.T) {
	f := newCloudFixture(t)
	exec := seedRun(t, f, "20260927-100000-abcd", "", "", true)
	f.run.SetState(exec, backend.StateRunning)
	writeStage(t, f, "20260927-100000-abcd", exec, "finalize")
	_, _, err := execute(t, "cancel", "--grace", "20ms", "--finalize-wait", "40ms", "--poll", "10ms", "20260927-100000-abcd")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "finalizing") {
		t.Fatalf("err = %v", err)
	}
	if f.run.State(exec) != backend.StateRunning {
		t.Fatal("cancel hard-cancelled a run mid-finalize")
	}
}

// N-2: after a double launch, launch.json names a duplicate that exited at
// once, and result.json names the execution that owns the run. ls and
// cancel must follow the record.
func TestDoubleLaunchViewsFollowTheRecord(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abcd"
	dup := seedRun(t, f, id, "", "", true) // launch.json names this one
	owner := f.run.Start(gcp.JobName(appSlug, "web"))
	f.run.SetState(dup, backend.StateFailed)
	f.run.SetState(owner, backend.StateRunning)
	writeStage(t, f, id, owner, "implement")
	out, _, err := execute(t, "ls", "--json")
	var got lsOut
	if err != nil || json.Unmarshal([]byte(out), &got) != nil || len(got.Runs) != 1 {
		t.Fatalf("ls = %s, %v", out, err)
	}
	if row := got.Runs[0]; row.Status != "running" || !backend.SameExecution(row.Execution, owner) {
		t.Fatalf("row = %+v (want running, the owner %s)", row, owner)
	}
	out, _, err = execute(t, "cancel", "--json", "--grace", "30ms", "--poll", "10ms", id)
	var res cancelResult
	_ = json.Unmarshal([]byte(out), &res)
	if err != nil || res.Status != "cancelled" || f.run.State(owner) != backend.StateCancelled {
		t.Fatalf("cancel = %+v, %v; owner state %s", res, err, f.run.State(owner))
	}
}

func TestCancelNotLaunchedAndFinished(t *testing.T) {
	f := newCloudFixture(t)
	seedRun(t, f, "20260927-100000-aaaa", "", "", false)
	out, _, err := execute(t, "cancel", "--json", "20260927-100000-aaaa")
	if err != nil || !json.Valid([]byte(out)) || !cancelled(t, f, "20260927-100000-aaaa") {
		t.Fatalf("not launched: %s, %v", out, err)
	}
	exec := seedRun(t, f, "20260927-100000-bbbb", "", "", true)
	f.run.SetState(exec, backend.StateSucceeded)
	out, _, err = execute(t, "cancel", "--json", "20260927-100000-bbbb")
	var res cancelResult
	_ = json.Unmarshal([]byte(out), &res)
	if err != nil || res.Status != "already-finished" || cancelled(t, f, "20260927-100000-bbbb") {
		t.Fatalf("finished: %+v, %v", res, err)
	}
}

func TestCancelNowHardCancelsAtOnce(t *testing.T) {
	f := newCloudFixture(t)
	exec := seedRun(t, f, "20260927-100000-abcd", "", "", true)
	f.run.SetState(exec, backend.StateRunning)
	writeStage(t, f, "20260927-100000-abcd", exec, "implement")
	out, _, err := execute(t, "cancel", "--now", "20260927-100000-abcd")
	if err != nil || !strings.Contains(out, "cancelled the execution") || strings.Contains(out, "grace period") {
		t.Fatalf("cancel --now = %q, %v", out, err)
	}
	if f.run.State(exec) != backend.StateCancelled || !cancelled(t, f, "20260927-100000-abcd") {
		t.Fatalf("state %s, marker %v", f.run.State(exec), cancelled(t, f, "20260927-100000-abcd"))
	}
}

// Note 8: not even --now stops a run mid-finalize.
func TestCancelNowStillWaitsOutFinalize(t *testing.T) {
	f := newCloudFixture(t)
	exec := seedRun(t, f, "20260927-100000-abcd", "", "", true)
	f.run.SetState(exec, backend.StateRunning)
	writeStage(t, f, "20260927-100000-abcd", exec, "finalize")
	_, _, err := execute(t, "cancel", "--now", "--finalize-wait", "30ms", "--poll", "10ms", "20260927-100000-abcd")
	if ExitCode(err) != ExitUserError || f.run.State(exec) != backend.StateRunning {
		t.Fatalf("err = %v, state %s", err, f.run.State(exec))
	}
}

func TestCancelFinalizedReportsThePR(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abcd"
	exec := seedRun(t, f, id, "", "", true)
	f.run.SetState(exec, backend.StateRunning)
	b, _ := blob.OpenBucket(context.Background(), f.bucket)
	defer b.Close()
	_ = runstore.Open(b, appSlug, id).WriteRecord(context.Background(), &runstore.Record{Version: 1, RunID: id, Execution: exec,
		Status: runstore.StatusCancelled, Stage: "finalize", PR: &runstore.PRRef{Number: 7, URL: "https://example.com/pr/7"}})
	out, _, err := execute(t, "cancel", "--poll", "10ms", id)
	if err != nil || !strings.Contains(out, "waiting up to") || !strings.Contains(out, "finalized (draft PR https://example.com/pr/7)") {
		t.Fatalf("cancel = %q, %v", out, err)
	}
}

// raceBackend lets the execution finish just before cancel's Cancel call,
// so the fake answers FAILED_PRECONDITION.
type raceBackend struct {
	backend.Backend
	finish func(string)
}

func (r raceBackend) Cancel(ctx context.Context, name string) error {
	r.finish(name)
	return r.Backend.Cancel(ctx, name)
}

func TestCancelToleratesAnExecutionThatJustFinished(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abcd"
	exec := seedRun(t, f, id, "", "", true)
	f.run.SetState(exec, backend.StateRunning)
	bucket, err := blobx.Open(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	env := envOn(t, f, bucket)
	defer env.Close()
	env.be = raceBackend{Backend: env.be, finish: func(n string) { f.run.SetState(n, backend.StateSucceeded) }}
	var out strings.Builder
	o := &cancelOptions{grace: 0, finalizeWait: time.Second, poll: 10 * time.Millisecond, asJSON: true}
	if err := cancelRun(context.Background(), env, o, id, &out); err != nil {
		t.Fatal(err)
	}
	var res cancelResult
	if err := json.Unmarshal([]byte(out.String()), &res); err != nil || res.Status != "finalized" || res.Hard {
		t.Fatalf("cancel = %s, %v", out.String(), err)
	}
}

func TestCancelMarkerFailureLeavesTheExecution(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abcd"
	exec := seedRun(t, f, id, "", "", true)
	f.run.SetState(exec, backend.StateRunning)
	bucket, err := blobx.Open(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	env := envOn(t, f, bucket)
	defer env.Close()
	// Make the marker unwritable: a directory where the object would go.
	dir := strings.TrimPrefix(f.bucket, "file://") + "/runs/" + appSlug + "/" + id + "/cancel"
	if err := os.MkdirAll(dir+"/x", 0o755); err != nil {
		t.Fatal(err)
	}
	o := &cancelOptions{grace: 0, finalizeWait: time.Second, poll: 10 * time.Millisecond, asJSON: true}
	err = cancelRun(context.Background(), env, o, id, io.Discard)
	if ExitCode(err) != ExitRemoteError || f.run.State(exec) != backend.StateRunning {
		t.Fatalf("err = %v, state %s", err, f.run.State(exec))
	}
	o.now = true
	var out strings.Builder
	o.asJSON = true
	if err := cancelRun(context.Background(), env, o, id, &out); err != nil {
		t.Fatal(err)
	}
	var res cancelResult
	if json.Unmarshal([]byte(out.String()), &res) != nil || res.Status != "cancelled" || !res.Hard || res.Marker {
		t.Fatalf("cancel --now = %s", out.String())
	}
}
