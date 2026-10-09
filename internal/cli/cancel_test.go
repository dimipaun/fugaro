package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/lock"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
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
	// `ls` hides runs older than its window, so the ID's date must follow the clock.
	id := time.Now().UTC().Format("20060102-150405") + "-abcd"
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

// A hard cancel (--now) puts the run's own execution into Cancelled
// itself, the one case cancel causes rather than merely observes: once
// that succeeds, cancel clears the branch lock right away (the same
// clearStaleLock as every other confirmed-terminal path), since a
// hard-cancelled container does not get to write a final record or mark
// its own lock releasing either.
func TestCancelNowClearsALiveLock(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abcd"
	exec := seedRun(t, f, id, "", "", true)
	f.run.SetState(exec, backend.StateRunning)
	writeRecord(t, f, id, &runstore.Record{Version: 1, RunID: id, Execution: exec,
		Status: runstore.StatusRunning, Stage: "implement", Outcome: runstore.OutcomeNone, Branch: "fugaro/" + id})
	key := lock.Key(appSlug, "fugaro/"+id)
	data, _ := json.Marshal(lock.Holder{RunID: id, Execution: exec, ExpiresAt: time.Now().Add(time.Hour)})
	putBuildObject(t, f, key, data)
	out, errOut, err := execute(t, "cancel", "--now", "--json", id)
	if err != nil {
		t.Fatal(err)
	}
	var res cancelResult
	if jerr := json.Unmarshal([]byte(out), &res); jerr != nil || res.Status != cancelCancelled || !res.Hard || res.LockHeld {
		t.Fatalf("cancel --now --json = %+v (parse err %v) (%s)", res, jerr, out)
	}
	if !strings.Contains(errOut, "cleared") {
		t.Fatalf("stderr = %q, want a note that the lock was cleared", errOut)
	}
	path := filepath.Join(strings.TrimPrefix(f.bucket, "file://"), key)
	if _, err := os.Stat(path); err == nil {
		t.Fatal("the lock survives a hard cancel")
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

// ended(), reached from the poll loop once the execution is confirmed
// terminal (not merely forgotten), clears the run's own live lock: the
// exact container-killed-mid-stage case (OOM, SIGBUS, a node loss) that
// would otherwise leave a follow-up waiting the lock's own expiry out.
func TestCancelClearsTheLockWhenEndedUnfinalizedAndExecutionIsTerminal(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abcd"
	exec := seedRun(t, f, id, "", "", true)
	f.run.SetState(exec, backend.StateRunning)
	writeRecord(t, f, id, &runstore.Record{Version: 1, RunID: id, Execution: exec,
		Status: runstore.StatusRunning, Stage: "implement", Outcome: runstore.OutcomeNone, Branch: "fugaro/" + id})
	key := lock.Key(appSlug, "fugaro/"+id)
	data, _ := json.Marshal(lock.Holder{RunID: id, Execution: exec, ExpiresAt: time.Now().Add(time.Hour)})
	putBuildObject(t, f, key, data)
	bucket, err := blobx.Open(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	env := envOn(t, f, bucket)
	defer env.Close()
	env.be = onFirstExecutionRead(env.be, func() { f.run.SetState(exec, backend.StateFailed) })
	var out, errOut strings.Builder
	o := &cancelOptions{grace: time.Second, floorSet: true, finalizeWait: time.Second, poll: time.Millisecond, asJSON: true}
	if err := cancelRun(context.Background(), env, o, id, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	var res cancelResult
	if json.Unmarshal([]byte(out.String()), &res) != nil || res.Status != cancelUnfinalized || res.LockHeld {
		t.Fatalf("cancel = %s, want ended-unfinalized with the lock cleared", out.String())
	}
	if !strings.Contains(errOut.String(), "cleared") {
		t.Fatalf("stderr = %q, want a note that the lock was cleared", errOut.String())
	}
	path := filepath.Join(strings.TrimPrefix(f.bucket, "file://"), key)
	if _, err := os.Stat(path); err == nil {
		t.Fatal("the lock survives a terminal execution's confirmed takeover")
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

// SECURITY: an execution the backend has merely forgotten (ErrNotFound)
// is not positive proof it is terminal — only that the backend has lost
// track of it, which can be a transient listing gap — so clearStaleLock,
// reached here through ended() during the poll loop, must never delete
// the run's own still-live lock on that alone (lock.Stale's gate). If
// that gate were ever removed, this scenario is exactly where it would
// matter: the lock is genuinely this run's own and would otherwise match
// lock.Takeover's holder check and be deleted.
func TestCancelKeepsALiveLockWhenExecutionIsOnlyForgotten(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abcd"
	exec := seedRun(t, f, id, "", "", true)
	f.run.SetState(exec, backend.StateRunning)
	writeRecord(t, f, id, &runstore.Record{Version: 1, RunID: id, Execution: exec,
		Status: runstore.StatusRunning, Stage: "implement", Outcome: runstore.OutcomeNone, Branch: "fugaro/" + id})
	key := lock.Key(appSlug, "fugaro/"+id)
	data, _ := json.Marshal(lock.Holder{RunID: id, Execution: exec, ExpiresAt: time.Now().Add(time.Hour)})
	putBuildObject(t, f, key, data)
	bucket, err := blobx.Open(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	env := envOn(t, f, bucket)
	defer env.Close()
	env.be = forgetBackend{Backend: env.be, reads: new(atomic.Int32), after: 1} // entry read ok, every poll forgets it
	var out strings.Builder
	o := &cancelOptions{grace: time.Minute, floorSet: true, finalizeWait: time.Second, poll: 10 * time.Millisecond, asJSON: true}
	if err := cancelRun(context.Background(), env, o, id, &out, io.Discard); err != nil {
		t.Fatalf("cancel = %v (exit %d)", err, ExitCode(err))
	}
	var res cancelResult
	if json.Unmarshal([]byte(out.String()), &res) != nil || res.Status != cancelUnfinalized || !res.LockHeld {
		t.Fatalf("cancel = %s, want ended-unfinalized with the lock still held", out.String())
	}
	path := filepath.Join(strings.TrimPrefix(f.bucket, "file://"), key)
	if _, err := os.Stat(path); err != nil {
		t.Fatal("a forgotten execution (not positive proof of terminal) must not clear the lock")
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

// An already-finished run whose branch lock is still live and still names
// it (the realistic case: its own lock, never released because the
// container died before writeback's delete) is actively cleared: cancel
// has, at this very point, confirmed the execution terminal through the
// backend (the one signal lock.Stale accepts), so it deletes the lock
// (lock.Takeover) rather than merely reporting it stuck.
func TestCancelAlreadyFinishedClearsItsOwnLiveLock(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abcd"
	exec := seedRun(t, f, id, "", "", true)
	f.run.SetState(exec, backend.StateSucceeded)
	rec := prRecord(id, exec, 7, 1)
	writeRecord(t, f, id, rec)
	key := lock.Key(appSlug, rec.Branch)
	data, _ := json.Marshal(lock.Holder{RunID: id, Execution: exec, ExpiresAt: time.Now().Add(time.Hour)})
	putBuildObject(t, f, key, data)

	out, errOut, err := execute(t, "cancel", "--json", id)
	var res cancelResult
	if jerr := json.Unmarshal([]byte(out), &res); err != nil || jerr != nil || res.Status != cancelAlreadyFinished || res.LockHeld {
		t.Fatalf("cancel --json = %+v (parse err %v), %v (%s)", res, jerr, err, out)
	}
	if !strings.Contains(errOut, "cleared") || !strings.Contains(errOut, id) {
		t.Fatalf("stderr = %q, want a note that the lock was cleared", errOut)
	}
	path := filepath.Join(strings.TrimPrefix(f.bucket, "file://"), key)
	if _, err := os.Stat(path); err == nil {
		t.Fatal("the lock survives a cleared takeover")
	}
}

// The same already-finished run, but its lock names a different
// execution (defense in depth: lock.Takeover re-checks immediately before
// deleting, so a lock that changed — or never was this run's in the
// first place — is left alone, reported LockHeld, not force-cleared on
// cancel's word about a different execution).
func TestCancelAlreadyFinishedCannotClearAMismatchedLock(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abcd"
	exec := seedRun(t, f, id, "", "", true)
	f.run.SetState(exec, backend.StateSucceeded)
	rec := prRecord(id, exec, 7, 1)
	writeRecord(t, f, id, rec)
	data, _ := json.Marshal(lock.Holder{RunID: id, Execution: "projects/proj-1234/locations/us-east5/jobs/fugaro-acme-app-web/executions/fugaro-acme-app-web-other", ExpiresAt: time.Now().Add(time.Hour)})
	putBuildObject(t, f, lock.Key(appSlug, rec.Branch), data)

	out, _, err := execute(t, "cancel", "--json", id)
	var res cancelResult
	if jerr := json.Unmarshal([]byte(out), &res); err != nil || jerr != nil || res.Status != cancelAlreadyFinished || !res.LockHeld {
		t.Fatalf("cancel --json = %+v (parse err %v), %v (%s)", res, jerr, err, out)
	}

	textOut, _, err := execute(t, "cancel", id)
	if err != nil || !strings.Contains(textOut, "branch lock is still held") {
		t.Fatalf("cancel = %q, %v", textOut, err)
	}
}

// Under the 0.7.0 bucket hardening a launcher's delete of locks/ answers
// 403: cancel must report LockHeld and print the clear operator message
// on stderr (the gcloud command), never crash and never silently claim
// the lock is gone.
func TestCancelForbiddenDeleteReportsLockHeldAndTheOperatorCommand(t *testing.T) {
	f := newCloudFixture(t)
	g := gcpfake.NewGCS(t)
	env := envOn(t, f, g.Bucket(t, "runs"))
	defer env.Close()
	const id = "20260927-100000-abcd"
	ctx := context.Background()
	s := runstore.Open(env.bucket.Bucket, appSlug, id)
	if err := s.CreateTask(ctx, &task.Spec{Version: 1, RunID: id, Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x"}); err != nil {
		t.Fatal(err)
	}
	exec := f.run.Start(gcp.JobName(appSlug, "web"))
	if err := s.WriteLaunch(ctx, &runstore.Launch{Version: 1, RunID: id, Execution: exec, LaunchedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	f.run.SetState(exec, backend.StateSucceeded)
	rec := prRecord(id, exec, 7, 1)
	if err := s.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	key := lock.Key(appSlug, rec.Branch)
	data, _ := json.Marshal(lock.Holder{RunID: id, Execution: exec, ExpiresAt: time.Now().Add(time.Hour)})
	if _, err := env.bucket.Create(ctx, key, data, "application/json"); err != nil {
		t.Fatal(err)
	}
	g.ForbidObjectDeletes(1)
	var out, errOut strings.Builder
	o := &cancelOptions{grace: time.Second, floorSet: true, finalizeWait: time.Second, poll: time.Millisecond, asJSON: true}
	if err := cancelRun(ctx, env, o, id, &out, &errOut); err != nil {
		t.Fatalf("cancel = %v", err)
	}
	var res cancelResult
	if jerr := json.Unmarshal([]byte(out.String()), &res); jerr != nil || res.Status != cancelAlreadyFinished || !res.LockHeld {
		t.Fatalf("cancel = %+v (parse err %v) (%s)", res, jerr, out.String())
	}
	if !strings.Contains(errOut.String(), "an operator must clear it") || !strings.Contains(errOut.String(), "gcloud storage rm") {
		t.Fatalf("stderr = %q, want the operator command", errOut.String())
	}
	if ok, _ := env.bucket.Exists(ctx, key); !ok {
		t.Fatal("a forbidden delete must not be reported as if the lock were gone")
	}
}

// The same already-finished run, but with no live lock: the plain message
// is unchanged.
func TestCancelAlreadyFinishedWithoutLiveLock(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abcd"
	exec := seedRun(t, f, id, "", "", true)
	f.run.SetState(exec, backend.StateSucceeded)
	rec := prRecord(id, exec, 7, 1)
	writeRecord(t, f, id, rec)
	out, _, err := execute(t, "cancel", id)
	if err != nil || !strings.Contains(out, "nothing to cancel") || strings.Contains(out, "branch lock") {
		t.Fatalf("cancel = %q, %v", out, err)
	}
}

// A corrupt (or oversized) result.json in the e.State.Terminal() branch
// must not turn "already finished" into a hard failure: the exact run this
// feature is about (one whose container died mid-write) is the one most
// likely to leave a truncated result.json behind.
func TestCancelAlreadyFinishedToleratesCorruptRecord(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abcd"
	exec := seedRun(t, f, id, "", "", true)
	f.run.SetState(exec, backend.StateSucceeded)
	bucket, err := blobx.Open(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	env := envOn(t, f, bucket)
	defer env.Close()
	// The container died mid-write of its final record, right as the
	// backend settled on Succeeded: ownerLaunch's own read (which must
	// still succeed, or cancel would refuse the run outright, a separate
	// and pre-existing behavior this test doesn't touch) sees it fine, and
	// only the fresh read in the e.State.Terminal() branch meets the
	// corruption.
	env.be = onFirstExecutionRead(env.be, func() {
		putBuildObject(t, f, "runs/"+appSlug+"/"+id+"/result.json", []byte("{not json"))
	})
	var out strings.Builder
	o := &cancelOptions{grace: time.Second, floorSet: true, finalizeWait: time.Second, poll: time.Millisecond, asJSON: true}
	if err := cancelRun(context.Background(), env, o, id, &out, io.Discard); err != nil {
		t.Fatalf("cancel = %v", err)
	}
	var res cancelResult
	if jerr := json.Unmarshal([]byte(out.String()), &res); jerr != nil || res.Status != cancelAlreadyFinished || res.LockHeld {
		t.Fatalf("cancel = %+v (parse err %v) (%s)", res, jerr, out.String())
	}
}

// Ruling E: a transient (non-corrupt: a genuine I/O failure, not bad
// JSON) read error of result.json in the e.State.Terminal() branch must
// not fail cancel either — the same tolerance
// TestCancelAlreadyFinishedToleratesCorruptRecord proves for a corrupt
// one, proven here for corruptObject's other branch.
func TestCancelAlreadyFinishedToleratesATransientRecordReadError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	f := newCloudFixture(t)
	const id = "20260927-100000-abcd"
	exec := seedRun(t, f, id, "", "", true)
	f.run.SetState(exec, backend.StateSucceeded)
	writeRecord(t, f, id, prRecord(id, exec, 7, 1))
	path := filepath.Join(strings.TrimPrefix(f.bucket, "file://"), "runs", appSlug, id, "result.json")
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
	bucket, err := blobx.Open(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	env := envOn(t, f, bucket)
	defer env.Close()
	// ownerLaunch's own read must still succeed (a separate, pre-existing
	// behavior this test doesn't touch), so the permission is dropped only
	// once cancel reaches the e.State.Terminal() branch's own fresh read.
	env.be = onFirstExecutionRead(env.be, func() {
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
	})
	var out strings.Builder
	o := &cancelOptions{grace: time.Second, floorSet: true, finalizeWait: time.Second, poll: time.Millisecond, asJSON: true}
	if err := cancelRun(context.Background(), env, o, id, &out, io.Discard); err != nil {
		t.Fatalf("cancel = %v", err)
	}
	var res cancelResult
	if jerr := json.Unmarshal([]byte(out.String()), &res); jerr != nil || res.Status != cancelAlreadyFinished || res.LockHeld {
		t.Fatalf("cancel = %+v (parse err %v) (%s)", res, jerr, out.String())
	}
}

// The other already-finished path: the backend has forgotten the
// execution entirely (ErrNotFound), but result.json is already final. It
// too notes a live lock rather than leaving "nothing to cancel" unexplained.
func TestCancelAlreadyFinishedForgottenExecutionNotesLiveLock(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abcd"
	exec := seedRun(t, f, id, "", "", true)
	rec := prRecord(id, exec, 7, 1) // Status failed, stage writeback: hasFinalized
	writeRecord(t, f, id, rec)
	data, _ := json.Marshal(lock.Holder{RunID: id, ExpiresAt: time.Now().Add(time.Hour)})
	putBuildObject(t, f, lock.Key(appSlug, rec.Branch), data)
	bucket, err := blobx.Open(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	env := envOn(t, f, bucket)
	defer env.Close()
	env.be = forgetBackend{Backend: env.be, reads: new(atomic.Int32), after: 0}
	var out strings.Builder
	o := &cancelOptions{grace: time.Second, floorSet: true, finalizeWait: time.Second, poll: time.Millisecond, asJSON: true}
	if err := cancelRun(context.Background(), env, o, id, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var res cancelResult
	if err := json.Unmarshal([]byte(out.String()), &res); err != nil || res.Status != cancelAlreadyFinished || !res.LockHeld {
		t.Fatalf("cancel = %+v, %v (%s)", res, err, out.String())
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

// TestLockLiveChecksRunIDAndExpiry: lockLive must say "not held" both when
// the lock names a different run (it is not this run's to report) and
// when it has already expired (the runner's own Acquire is free to take
// it, so it is no longer meaningfully "held" either).
func TestLockLiveChecksRunIDAndExpiry(t *testing.T) {
	f := newCloudFixture(t)
	bucket, err := blobx.Open(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	env := envOn(t, f, bucket)
	defer env.Close()
	const slug, branch, id = "acme-app", "fugaro/run-a", "run-a"
	key := lock.Key(slug, branch)
	write := func(h lock.Holder) {
		data, _ := json.Marshal(h)
		putBuildObject(t, f, key, data)
	}
	write(lock.Holder{RunID: "run-b", ExpiresAt: time.Now().Add(time.Hour)})
	if lockLive(context.Background(), env, slug, branch, id, time.Now()) {
		t.Fatal("lockLive reported a lock naming a different run as this run's own")
	}
	write(lock.Holder{RunID: id, ExpiresAt: time.Now().Add(-time.Minute)})
	if lockLive(context.Background(), env, slug, branch, id, time.Now()) {
		t.Fatal("lockLive reported an expired lock as still held")
	}
	write(lock.Holder{RunID: id, ExpiresAt: time.Now().Add(time.Hour)})
	if !lockLive(context.Background(), env, slug, branch, id, time.Now()) {
		t.Fatal("lockLive did not report a live lock naming this run")
	}
}
