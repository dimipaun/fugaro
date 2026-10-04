package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
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
	const id = "20260927-100000-abcd"
	exec := seedRun(t, f, id, "", "", true)
	f.run.SetState(exec, backend.StateRunning)
	bucket, err := blobx.Open(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	env := envOn(t, f, bucket)
	defer env.Close()
	// Land the runner's finalize exactly after cancel's entry read sees the
	// execution still running, in place of a sleeping goroutine racing the
	// poll loop on wall-clock time.
	env.be = onFirstExecutionRead(env.be, func() {
		writeFinal(t, f, id, exec)
		f.run.SetState(exec, backend.StateSucceeded)
	})
	var out strings.Builder
	o := &cancelOptions{grace: time.Second, floorSet: true, finalizeWait: time.Second, poll: time.Millisecond, asJSON: true}
	if err := cancelRun(context.Background(), env, o, id, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var res cancelResult
	if err := json.Unmarshal([]byte(out.String()), &res); err != nil || res.Status != "finalized" || res.Hard || !cancelled(t, f, id) {
		t.Fatalf("cancel = %+v, %v", res, err)
	}
}

func TestCancelHardAfterGrace(t *testing.T) {
	f := newCloudFixture(t)
	exec := seedRun(t, f, "20260927-100000-abcd", "", "", true)
	f.run.SetState(exec, backend.StateRunning)
	out, _, err := execute(t, "cancel", "--json", "--grace", "30ms", "--grace-floor", "0", "--poll", "10ms", "20260927-100000-abcd")
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
	_, _, err := execute(t, "cancel", "--grace", "20ms", "--grace-floor", "0", "--finalize-wait", "40ms", "--poll", "10ms", "20260927-100000-abcd")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "finalizing") {
		t.Fatalf("err = %v", err)
	}
	if f.run.State(exec) != backend.StateRunning {
		t.Fatal("cancel hard-cancelled a run mid-finalize")
	}
}

// After a double launch, launch.json names a duplicate that exited at
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
	out, _, err = execute(t, "cancel", "--json", "--grace", "30ms", "--grace-floor", "0", "--poll", "10ms", id)
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

// entryHook runs fire synchronously right after Execution's first call
// returns: cancelRun's entry read, before it writes the marker or starts
// its poll loop. Tests use it to land a state change exactly where a
// sleeping goroutine used to race the poll loop on wall-clock time, making
// the sequencing deterministic regardless of system load.
type entryHook struct {
	backend.Backend
	calls atomic.Int32
	fire  func()
}

func onFirstExecutionRead(be backend.Backend, fire func()) backend.Backend {
	return &entryHook{Backend: be, fire: fire}
}

func (h *entryHook) Execution(ctx context.Context, name string) (backend.Execution, error) {
	e, err := h.Backend.Execution(ctx, name)
	if h.calls.Add(1) == 1 {
		h.fire()
	}
	return e, err
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
	env.be = raceBackend{Backend: env.be, finish: func(n string) {
		writeFinal(t, f, id, n)
		f.run.SetState(n, backend.StateSucceeded)
	}}
	var out strings.Builder
	o := &cancelOptions{grace: 0, floorSet: true, finalizeWait: time.Second, poll: 10 * time.Millisecond, asJSON: true}
	if err := cancelRun(context.Background(), env, o, id, &out, io.Discard); err != nil {
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
	o := &cancelOptions{grace: 0, floorSet: true, finalizeWait: time.Second, poll: 10 * time.Millisecond, asJSON: true}
	err = cancelRun(context.Background(), env, o, id, io.Discard, io.Discard)
	if ExitCode(err) != ExitRemoteError || f.run.State(exec) != backend.StateRunning {
		t.Fatalf("err = %v, state %s", err, f.run.State(exec))
	}
	o.now = true
	var out strings.Builder
	o.asJSON = true
	if err := cancelRun(context.Background(), env, o, id, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var res cancelResult
	if json.Unmarshal([]byte(out.String()), &res) != nil || res.Status != "cancelled" || !res.Hard || res.Marker {
		t.Fatalf("cancel --now = %s", out.String())
	}
}

// writeFinal writes the final record a runner writes just before exiting.
func writeFinal(t *testing.T, f *cloudFixture, id, exec string) {
	t.Helper()
	b, _ := blob.OpenBucket(context.Background(), f.bucket)
	defer b.Close()
	_ = runstore.Open(b, appSlug, id).WriteRecord(context.Background(), &runstore.Record{Version: 1, RunID: id, Execution: exec,
		Status: runstore.StatusCancelled, Stage: "finalize", Outcome: runstore.OutcomeNone})
}

// An execution that ends while result.json still says running died
// without finalizing: never report it as finalized.
func TestCancelReportsARunThatEndedUnfinalized(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abcd"
	exec := seedRun(t, f, id, "", "", true)
	f.run.SetState(exec, backend.StateRunning)
	writeStage(t, f, id, exec, "implement")
	bucket, err := blobx.Open(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	env := envOn(t, f, bucket)
	defer env.Close()
	base := env.be
	fail := func() { f.run.SetState(exec, backend.StateFailed) }

	// Flip the execution to failed right after cancel's entry read sees it
	// still running, instead of racing a sleeping goroutine against the
	// poll loop on wall-clock time.
	env.be = onFirstExecutionRead(base, fail)
	var out strings.Builder
	o := &cancelOptions{grace: time.Second, floorSet: true, finalizeWait: time.Second, poll: time.Millisecond, asJSON: true}
	if err := cancelRun(context.Background(), env, o, id, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var res cancelResult
	if err := json.Unmarshal([]byte(out.String()), &res); err != nil || res.Status != "ended-unfinalized" || res.Hard || !res.Marker {
		t.Fatalf("cancel = %+v, %v", res, err)
	}

	f.run.SetState(exec, backend.StateRunning)
	env.be = onFirstExecutionRead(base, fail)
	var human strings.Builder
	o2 := &cancelOptions{grace: time.Second, floorSet: true, finalizeWait: time.Second, poll: time.Millisecond}
	if err := cancelRun(context.Background(), env, o2, id, &human, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human.String(), "the run ended without finalizing; check fugaro diagnose") || strings.Contains(human.String(), "finalized (") {
		t.Fatalf("human cancel = %q", human.String())
	}
}

// forgetBackend forgets every execution after its first few reads.
type forgetBackend struct {
	backend.Backend
	reads *atomic.Int32
	after int32
}

func (fb forgetBackend) Execution(ctx context.Context, name string) (backend.Execution, error) {
	if fb.reads.Add(1) > fb.after {
		return backend.Execution{}, fmt.Errorf("reading execution %s: %w", name, backend.ErrNotFound)
	}
	return fb.Backend.Execution(ctx, name)
}

// Note 5: an execution the backend forgets during the poll counts as
// finished, as it does at entry; it is not a remote failure.
func TestCancelToleratesAnExecutionForgottenMidPoll(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abcd"
	exec := seedRun(t, f, id, "", "", true)
	f.run.SetState(exec, backend.StateRunning)
	writeStage(t, f, id, exec, "implement")
	bucket, err := blobx.Open(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	env := envOn(t, f, bucket)
	defer env.Close()
	env.be = forgetBackend{Backend: env.be, reads: new(atomic.Int32), after: 2} // entry read, one poll, then gone
	var out strings.Builder
	o := &cancelOptions{grace: time.Minute, floorSet: true, finalizeWait: time.Second, poll: 10 * time.Millisecond, asJSON: true}
	if err := cancelRun(context.Background(), env, o, id, &out, io.Discard); err != nil {
		t.Fatalf("cancel = %v (exit %d)", err, ExitCode(err))
	}
	var res cancelResult
	if json.Unmarshal([]byte(out.String()), &res) != nil || res.Status != "ended-unfinalized" || res.Hard {
		t.Fatalf("cancel = %s", out.String())
	}
	if f.run.State(exec) != backend.StateRunning {
		t.Fatalf("state %s: cancel acted on a forgotten execution", f.run.State(exec))
	}
}

// Note 8: --grace never undercuts the finalize reserve, and says so.
func TestCancelGraceIsFlooredToTheFinalizeReserve(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abcd"
	exec := seedRun(t, f, id, "", "", true)
	f.run.SetState(exec, backend.StateRunning)
	writeStage(t, f, id, exec, "implement")
	start := time.Now()
	out, errOut, err := execute(t, "cancel", "--grace", "10ms", "--grace-floor", "300ms", "--poll", "10ms", id)
	if err != nil || time.Since(start) < 300*time.Millisecond || f.run.State(exec) != backend.StateCancelled {
		t.Fatalf("cancel = %q, %v after %s, state %s", out, err, time.Since(start), f.run.State(exec))
	}
	if !strings.Contains(errOut, "shorter than --grace-floor") || !strings.Contains(out, "waiting up to 300ms") {
		t.Fatalf("stdout %q, stderr %q", out, errOut)
	}
}

func TestGraceFloorDefaultsToTheFinalizeReserve(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abcd"
	seedRun(t, f, id, "", "", false)
	b, _ := blob.OpenBucket(context.Background(), f.bucket)
	defer b.Close()
	// This test's working directory is not a checkout of acme/app, so the
	// workflow's reserve is unknown here. The floor adds the time the runner
	// takes to notice the marker and stop the stage: 30s poll, 10s kill.
	if got, _ := graceFloor(context.Background(), runstore.Open(b, appSlug, id), &cancelOptions{}); got != defaultFinalizeReserve+40*time.Second {
		t.Fatalf("floor = %s, want %s", got, defaultFinalizeReserve+40*time.Second)
	}
	if got, _ := graceFloor(context.Background(), runstore.Open(b, appSlug, id), &cancelOptions{floorSet: true, floor: time.Second}); got != time.Second {
		t.Fatalf("overridden floor = %s", got)
	}
}

// --now cancels at once, so a --grace with it would be ignored: the two
// are refused together, as a user error.
func TestCancelGraceAndNowAreExclusive(t *testing.T) {
	newCloudFixture(t)
	_, _, err := execute(t, "cancel", "--now", "--grace", "5m", "20260927-100000-abcd")
	if ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "grace") {
		t.Fatalf("cancel --now --grace = exit %d, %v", ExitCode(err), err)
	}
}

// A follow-up is cancelled like any run: the marker, the grace, and the
// runner finalizing its pushed work onto the existing PR, which is draft.
func TestCancelFollowUp(t *testing.T) {
	f := newCloudFixture(t)
	root, id := runIDAt(1, "090000", "aaaa"), runIDAt(0, "000100", "bbbb")
	exec := seedSpec(t, f, followUpSpec(id, root, root, 7), true)
	f.run.SetState(exec, backend.StateRunning)
	bucket, err := blobx.Open(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	env := envOn(t, f, bucket)
	defer env.Close()
	// Land the runner's finalize exactly after cancel's entry read sees the
	// execution still running, in place of a sleeping goroutine racing the
	// poll loop on wall-clock time.
	env.be = onFirstExecutionRead(env.be, func() {
		rec := prRecord(id, exec, 7, 1)
		rec.Status, rec.Stage, rec.Branch = runstore.StatusCancelled, "finalize", "fugaro/"+root
		rec.FollowUp = &runstore.FollowUp{PR: 7, PreviousRun: root}
		writeRecord(t, f, id, rec)
		f.run.SetState(exec, backend.StateSucceeded)
	})
	var out strings.Builder
	o := &cancelOptions{grace: time.Second, floorSet: true, finalizeWait: time.Second, poll: time.Millisecond}
	if err := cancelRun(context.Background(), env, o, id, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "finalized (draft PR https://github.com/acme/app/pull/7)") || !cancelled(t, f, id) {
		t.Fatalf("cancel = %q", out.String())
	}
	if f.run.State(exec) != backend.StateSucceeded {
		t.Fatalf("the follow-up was hard-cancelled: %s", f.run.State(exec))
	}
	if cancelled(t, f, root) {
		t.Fatal("cancelling the follow-up marked the run that opened the PR")
	}
}

func TestCancelHaltedRunAlreadyFinished(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abcd"
	exec := seedRun(t, f, id, "", "", true)
	f.run.SetState(exec, backend.StateSucceeded)
	rec := prRecord(id, exec, 7, 1)
	rec.Status = runstore.StatusHalted
	rec.Halt = &runstore.Halt{Reason: runstore.HaltRunCap, Scope: "run", At: time.Now().UTC()}
	writeRecord(t, f, id, rec)
	out, _, err := execute(t, "cancel", "--json", id)
	var res cancelResult
	_ = json.Unmarshal([]byte(out), &res)
	if err != nil || res.Status != "already-finished" || cancelled(t, f, id) {
		t.Fatalf("cancel = %+v, %v (%s)", res, err, out)
	}
}
