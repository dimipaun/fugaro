package runner_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/lock"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// ownerRecord puts exec1's running record in place, as the owner of the
// run would have written it.
func ownerRecord(t *testing.T, h *harness) *runstore.Record {
	t.Helper()
	first := &runstore.Record{Version: 1, RunID: runID, Repo: "acme/app", Execution: exec1, Status: runstore.StatusRunning, Stage: "implement", Outcome: runstore.OutcomeNone}
	if err := h.store.WriteRecord(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	return first
}

func assertOwnerRecordIntact(t *testing.T, h *harness) {
	t.Helper()
	stored, err := h.store.ReadRecord(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stored.Execution != exec1 || stored.Status != runstore.StatusRunning || stored.Stage != "implement" {
		t.Fatalf("the duplicate overwrote result.json: %+v", stored)
	}
	if len(h.provider.State.PRs) != 0 {
		t.Fatal("the duplicate opened a PR")
	}
}

// TestDuplicateExecutionFailingBeforeItsRecordWritesNothing covers a
// duplicate that fails before it could tell it was one: here
// task.json can't be read. Its failure record must not replace the
// owner's.
func TestDuplicateExecutionFailingBeforeItsRecordWritesNothing(t *testing.T) {
	h := newHarness(t, "", nil)
	withBucket(h)
	ownerRecord(t, h)
	if err := h.bucket.Delete(context.Background(), h.store.Prefix()+"task.json"); err != nil {
		t.Fatal(err)
	}
	h.deps.Execution = exec2
	if _, err := h.run(t); err == nil {
		t.Fatal("a run without task.json succeeded")
	}
	assertOwnerRecordIntact(t, h)
}

// TestFailureBeforeTheFirstRecordIsStillRecorded: with no record in place,
// the same early failure is recorded, so views see an infra_error rather
// than a run that never started.
func TestFailureBeforeTheFirstRecordIsStillRecorded(t *testing.T) {
	h := newHarness(t, "", nil)
	withBucket(h)
	if err := h.bucket.Delete(context.Background(), h.store.Prefix()+"task.json"); err != nil {
		t.Fatal(err)
	}
	h.deps.Execution = exec2
	if _, err := h.run(t); err == nil {
		t.Fatal("a run without task.json succeeded")
	}
	stored, err := h.store.ReadRecord(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != runstore.StatusInfraError || stored.Execution != exec2 || stored.RunID != runID {
		t.Fatalf("record = %+v, want an infra_error of %s naming %s", stored, runID, exec2)
	}
}

// TestDuplicateExecutionCreateErrorWritesNothing: the first record's
// create fails with an error other than ErrExists. The runner reads the
// record back, finds the owner's, and exits as a duplicate.
func TestDuplicateExecutionCreateErrorWritesNothing(t *testing.T) {
	h := newHarness(t, "", nil)
	withBucket(h)
	ownerRecord(t, h)
	runner.SetCreateRecord(t, func(*runstore.Store, context.Context, *runstore.Record) error {
		return errors.New("transient storage error")
	})
	h.deps.Execution = exec2
	if _, err := h.run(t); !errors.Is(err, runner.ErrDuplicateExecution) {
		t.Fatalf("err = %v, want ErrDuplicateExecution", err)
	}
	assertOwnerRecordIntact(t, h)
}

// TestCreateErrorWithNoRecordIsFatalAndWritesNothing: the create fails
// and nothing can be read back, so ownership is unknown. The run stops
// and writes nothing.
func TestCreateErrorWithNoRecordIsFatalAndWritesNothing(t *testing.T) {
	h := newHarness(t, "", nil)
	withBucket(h)
	runner.SetCreateRecord(t, func(*runstore.Store, context.Context, *runstore.Record) error {
		return errors.New("transient storage error")
	})
	var writes int
	runner.SetWriteRecord(t, func(s *runstore.Store, ctx context.Context, rec *runstore.Record) error {
		writes++
		return s.WriteRecord(ctx, rec)
	})
	h.deps.Execution = exec2
	if _, err := h.run(t); err == nil {
		t.Fatal("the run carried on without a first record")
	}
	if _, err := h.store.ReadRecord(context.Background()); !errors.Is(err, runstore.ErrNotFound) {
		t.Fatalf("a record was written: err = %v", err)
	}
	if writes != 0 {
		t.Fatalf("%d record writes, want none", writes)
	}
}

// TestCreateCommittedDespiteAnErrorIsOwned: the create's response is lost
// but the record landed. Reading it back finds this execution, which owns
// the run.
func TestCreateCommittedDespiteAnErrorIsOwned(t *testing.T) {
	h := newHarness(t, "", nil)
	withBucket(h)
	runner.SetCreateRecord(t, func(s *runstore.Store, ctx context.Context, rec *runstore.Record) error {
		if err := s.CreateRecord(ctx, rec); err != nil {
			return err
		}
		return errors.New("response lost")
	})
	h.deps.Execution = exec1
	if rec, err := h.run(t, implement("feature"), review("ship", 0)); err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

// TestRecordWritesAreBounded: every record write has a deadline
// of its own, so a stalled bucket can't hold the runner.
func TestRecordWritesAreBounded(t *testing.T) {
	h := newHarness(t, "", nil)
	h.deps.Execution = exec1
	var mu sync.Mutex
	var unbounded, n int
	check := func(ctx context.Context) {
		mu.Lock()
		defer mu.Unlock()
		n++
		if d, ok := ctx.Deadline(); !ok || time.Until(d) > runner.RecordWriteTimeout {
			unbounded++
		}
	}
	runner.SetWriteRecord(t, func(s *runstore.Store, ctx context.Context, rec *runstore.Record) error {
		check(ctx)
		return s.WriteRecord(ctx, rec)
	})
	runner.SetCreateRecord(t, func(s *runstore.Store, ctx context.Context, rec *runstore.Record) error {
		check(ctx)
		return s.CreateRecord(ctx, rec)
	})
	if _, err := h.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if n == 0 || unbounded > 0 {
		t.Fatalf("%d of %d record writes had no bound of their own", unbounded, n)
	}
}

// TestWritebackReleasesTheLockOnAFreshContext: when writeback's
// deadline has already passed, the lock is still released on a context of
// its own rather than on the expired one.
func TestWritebackReleasesTheLockOnAFreshContext(t *testing.T) {
	h := newHarness(t, "", nil)
	b := withBucket(h)
	// A clock an hour behind puts writeback's deadline in the past.
	h.deps.Now = func() time.Time { return time.Now().Add(-time.Hour) }
	var expired []error
	runner.SetLockRelease(t, func(l *lock.Lock, ctx context.Context) error {
		expired = append(expired, ctx.Err())
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return l.Release(ctx)
	})
	// Its stage deadline has passed as well, so implement ends at once.
	if _, err := h.run(t, blockUntilDone); err != nil {
		t.Fatal(err)
	}
	if len(expired) != 1 || expired[0] != nil {
		t.Fatalf("lock releases saw context errors %v, want one release on a live context", expired)
	}
	if ok, _ := b.Exists(context.Background(), lock.Key(h.store.Slug(), "fugaro/"+runID)); ok {
		t.Fatal("the lock survives the run")
	}
}

// TestCancelBeforeBootstrapIsSeenAtOnce: a
// cancel marker already in place is seen at the start of bootstrap, not
// only at the first poll.
func TestCancelBeforeBootstrapIsSeenAtOnce(t *testing.T) {
	h := newHarness(t, "", nil)
	h.deps.Execution = exec1
	h.deps.CancelPoll = time.Hour
	if err := h.store.RequestCancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	rec, err := h.run(t) // no agent steps may run
	if err == nil || rec == nil || rec.Status != runstore.StatusCancelled || rec.Stage != "bootstrap" {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if len(h.provider.State.PRs) != 0 {
		t.Fatal("a cancelled run opened a PR")
	}
}

// The two not-the-owner returns carry text the run did not word, and both are
// printed: internal/cli/exec.go hands Run's error to the command's error and
// cmd/fugaro/main.go writes it to the job's stderr. The first return is the
// claim's own outcome — ErrDuplicateExecution, or errOwnerUnknown, whose
// message embeds what the store said while the first record's create failed
// and the read came back empty — and the second is a duplicate whose bootstrap
// failed outright before it could claim anything. redactErr is applied on both
// (runner.go's deferred handler), and these two tests are what makes removing
// either call visible.
//
// Mutation (run, restored): drop `err = r.redactErr(err)` from the
// ErrDuplicateExecution/errOwnerUnknown return in Run's deferred handler.
// TestOwnerUnknownErrorIsRedacted fails on the leaked secret and no other test
// does. Mutation (run, restored): drop the same call from the ErrExists return
// below it. TestDuplicateFailingAtBootstrapIsRedacted fails.
func TestOwnerUnknownErrorIsRedacted(t *testing.T) {
	h := newHarness(t, "", nil)
	withBucket(h)
	const secret = "ATBBsecret"
	// The job's remote is where a credential a launcher set lives, and
	// bootstrap registers it with the redactor before the claim.
	h.deps.Remote = "https://example.invalid/acme/app.git?access_token=" + secret
	h.deps.Execution = exec2
	runner.SetCreateRecord(t, func(*runstore.Store, context.Context, *runstore.Record) error {
		// The store refused, in the shape a backend error takes when it
		// quotes the URL it was handed — and nothing can be read back, so
		// ownership is unknown.
		return errors.New("creating " + h.store.Prefix() + "result.json: the backend refused " + h.deps.Remote)
	})
	rec, err := h.run(t)
	if err == nil {
		t.Fatalf("rec = %+v, want the claim's failure", rec)
	}
	if !strings.Contains(err.Error(), "could not tell whether another execution owns this run") {
		t.Fatalf("err = %v, want the ownership-unknown failure", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("the returned error quotes the remote's credential, and it is printed to the job's stderr: %v", err)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Errorf("nothing was redacted on this path, so the mutation that drops redactErr here would go unnoticed: %v", err)
	}
	if rec != nil {
		t.Errorf("rec = %+v, want none: this execution does not own the run", rec)
	}
}

// TestDuplicateFailingAtBootstrapIsRedacted is the other not-the-owner return:
// an execution that is not the owner because its bootstrap failed before the
// claim, landing on a record the owner already wrote. The error the run returns
// is that bootstrap failure — the task read, the first thing bootstrap does —
// and its text is the store's, here a bucket whose own path carries a value the
// job mounted as a secret, which is what a backend's error looks like when it
// quotes the URL it was opened with. addMountedSecrets is what registers that
// value, before bootstrap runs, and this path is why it is registered that
// early: nothing else has had a chance to.
func TestDuplicateFailingAtBootstrapIsRedacted(t *testing.T) {
	h := newHarness(t, "", nil)
	const secret = "ATBBsecret"
	// The value is one the job has mounted as a secret, which is what
	// addMountedSecrets registers before bootstrap runs — the registration
	// that covers exactly these first-moment failures. A remote's own
	// credential would not do: addRemoteSecrets runs inside bootstrap, after
	// the task read below has already failed.
	h.deps.Env = append(h.deps.Env, runner.SecretEnvsVar+"=ACME_TOKEN", "ACME_TOKEN="+secret)
	// A store of its own, opened on a bucket whose path carries the same
	// value: its errors quote the path, so the failure text below contains a
	// value the run has registered as a secret.
	dir := filepath.Join(t.TempDir(), secret)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bucket, err := blob.OpenBucket(context.Background(), "file://"+dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bucket.Close() })
	h.deps.Store, h.store = runstore.Open(bucket, repoSlug, runID), runstore.Open(bucket, repoSlug, runID)
	// The owner's record is in place, and this execution's task.json cannot
	// be read: it fails reading the task, before the claim, and the record it
	// then tries to create finds the owner's. The object is a symlink that
	// points at itself, so the driver's error is not "not found" (which
	// runstore words by itself, driver text and all dropped) but a failure
	// that quotes the path it was handed — the path with the credential in
	// it, which is the leak this test is about.
	ownerRecord(t, h)
	link := filepath.Join(dir, h.store.Prefix(), "task.json")
	if err := os.Symlink("task.json", link); err != nil {
		t.Fatal(err)
	}
	h.deps.Execution = exec2
	rec, err := h.run(t)
	if err == nil {
		t.Fatalf("rec = %+v, want the bootstrap failure", rec)
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("the returned error quotes the credential the store was handed, and it is printed to the job's stderr: %v", err)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Errorf("nothing was redacted on this path, so the mutation that drops redactErr here would go unnoticed: %v", err)
	}
	if rec != nil {
		t.Errorf("rec = %+v, want none: this execution does not own the run", rec)
	}
	assertOwnerRecordIntact(t, h)
}
