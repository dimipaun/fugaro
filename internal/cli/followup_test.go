package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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

// TestRunPRForgedRecordNeverBypassesTheLock is the security-review test:
// a launcher can write any run's result.json (bucket-iam.md §10, L3) even
// under the 0.7.0 hardening, which closes locks/ itself but not runs/. If
// checkBranchLock read it, writing {"status":"succeeded"} over a live
// run's record would let a second launch proceed to push the same branch.
// Every record shape here — forged terminal, genuinely running, or
// missing entirely — must still refuse: only the backend's own word on
// the holder's execution (below) can lift a live lock.
func TestRunPRForgedRecordNeverBypassesTheLock(t *testing.T) {
	holder := runIDAt(0, "000100", "bbbb")
	cases := []struct {
		name string
		rec  *runstore.Record
	}{
		{"forged terminal record (succeeded)", &runstore.Record{Version: 1, RunID: holder, Repo: "acme/app", Workflow: "web",
			Status: runstore.StatusSucceeded, Stage: "writeback", Outcome: runstore.OutcomeReady, StartedAt: time.Now().Add(-2 * time.Hour)}},
		{"genuinely running record", &runstore.Record{Version: 1, RunID: holder, Repo: "acme/app", Workflow: "web",
			Status: runstore.StatusRunning, Stage: "implement", Outcome: runstore.OutcomeNone, StartedAt: time.Now().Add(-2 * time.Hour)}},
		{"no record at all", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newCloudFixture(t)
			seedRoot(t, f, rootID, time.Now().Add(-time.Hour))
			if c.rec != nil {
				writeRecord(t, f, holder, c.rec)
			}
			data, _ := json.Marshal(lock.Holder{RunID: holder, ExpiresAt: time.Now().Add(time.Hour)})
			putBuildObject(t, f, lock.Key(appSlug, "fugaro/"+rootID), data)
			wantRefused(t, f, "branch busy: run "+holder, "run", "--repo", "acme/app", "--pr", "7")
		})
	}
}

// A live lock is taken over, and deleted outright (not just bypassed), the
// moment the backend confirms its holder's own execution has ended: the
// one signal a launcher cannot forge. Deleting it, rather than merely
// deciding to launch past it, is what lets the runner's own lock.Acquire
// (the only place a lock is ever taken over) succeed right after: it
// creates the lock afresh, finding nothing there to conflict with.
func TestRunPRTakesOverLockWhenBackendConfirmsTermination(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-time.Hour))
	holder := runIDAt(0, "000100", "bbbb")
	exec := f.run.Start(gcp.JobName(appSlug, "web"))
	f.run.SetState(exec, backend.StateFailed)
	key := lock.Key(appSlug, "fugaro/"+rootID)
	data, _ := json.Marshal(lock.Holder{RunID: holder, Execution: exec, ExpiresAt: time.Now().Add(time.Hour)})
	putBuildObject(t, f, key, data)
	_, errOut, err := execute(t, "run", "--repo", "acme/app", "--pr", "7")
	if err != nil {
		t.Fatalf("a lock whose holder's execution the backend confirms ended: %v (%s)", err, errOut)
	}
	if !strings.Contains(errOut, holder) {
		t.Fatalf("stderr = %q, want a note naming %s", errOut, holder)
	}
	path := filepath.Join(strings.TrimPrefix(f.bucket, "file://"), key)
	if _, err := os.Stat(path); err == nil {
		t.Fatal("the lock survives a takeover the backend confirmed")
	}
}

// Neither signal proves the holder over: its execution is genuinely still
// running.
func TestRunPRRefusesLockWhenExecutionStillRunning(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-time.Hour))
	holder := runIDAt(0, "000100", "bbbb")
	exec := f.run.Start(gcp.JobName(appSlug, "web"))
	f.run.SetState(exec, backend.StateRunning)
	data, _ := json.Marshal(lock.Holder{RunID: holder, Execution: exec, ExpiresAt: time.Now().Add(time.Hour)})
	putBuildObject(t, f, lock.Key(appSlug, "fugaro/"+rootID), data)
	n := len(f.run.Executions())
	_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "branch busy: run "+holder) {
		t.Fatalf("err = %v", err)
	}
	if len(f.run.Executions()) != n {
		t.Fatal("a follow-up launched even though its holder's execution is still running")
	}
}

// Nor does an execution the backend has never heard of (never started, or
// long forgotten): "unknown" is not "terminal".
func TestRunPRRefusesLockWhenExecutionUnknown(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-time.Hour))
	holder := runIDAt(0, "000100", "bbbb")
	const unknownExec = "projects/proj-1234/locations/us-east5/jobs/fugaro-acme-app-web/executions/fugaro-acme-app-web-99999"
	data, _ := json.Marshal(lock.Holder{RunID: holder, Execution: unknownExec, ExpiresAt: time.Now().Add(time.Hour)})
	putBuildObject(t, f, lock.Key(appSlug, "fugaro/"+rootID), data)
	wantRefused(t, f, "branch busy: run "+holder, "run", "--repo", "acme/app", "--pr", "7")
}

// Under the 0.7.0 bucket hardening a launcher's delete of locks/ answers
// 403: checkBranchLock must print the clear operator message, naming the
// gcloud command, rather than crash or silently refuse as plain "branch
// mismatchedNameBackend answers every Execution call truthfully except
// that it renames the result, as if the backend had, by some fault,
// confused two executions.
type mismatchedNameBackend struct {
	backend.Backend
	answerAs string
}

func (m mismatchedNameBackend) Execution(ctx context.Context, name string) (backend.Execution, error) {
	e, err := m.Backend.Execution(ctx, name)
	if err != nil {
		return e, err
	}
	e.Name = m.answerAs
	return e, nil
}

// executionTerminal's defensive backend.SameExecution check matters: a
// backend that answers Terminal() for a DIFFERENT execution than the one
// asked about (a bug, or a name collision) must never be read as proof
// the asked-about one is over, or a launcher could take over a live
// lock on another run's unrelated termination.
func TestCheckBranchLockRequiresTheSameExecutionNamed(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-time.Hour))
	bucket, err := blobx.Open(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	env := envOn(t, f, bucket)
	defer env.Close()
	holder := runIDAt(0, "000100", "bbbb")
	exec := f.run.Start(gcp.JobName(appSlug, "web"))
	f.run.SetState(exec, backend.StateFailed)
	env.be = mismatchedNameBackend{Backend: env.be, answerAs: "projects/proj-1234/locations/us-east5/jobs/fugaro-acme-app-web/executions/some-other-run"}
	branch := "fugaro/" + rootID
	key := lock.Key(appSlug, branch)
	data, _ := json.Marshal(lock.Holder{RunID: holder, Execution: exec, ExpiresAt: time.Now().Add(time.Hour)})
	if _, err := env.bucket.Create(context.Background(), key, data, "application/json"); err != nil {
		t.Fatal(err)
	}
	err = checkBranchLock(context.Background(), env, appSlug, branch, time.Now(), io.Discard)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "branch busy: run "+holder) {
		t.Fatalf("err = %v, want branch busy naming %s", err, holder)
	}
	if ok, _ := env.bucket.Exists(context.Background(), key); !ok {
		t.Fatal("the lock was taken over on a mismatched execution name")
	}
}

// busy" (which would send an operator looking at the wrong thing — the
// backend has, in fact, already proven the holder over).
func TestCheckBranchLockForbiddenDeleteNamesTheOperatorCommand(t *testing.T) {
	f := newCloudFixture(t)
	g := gcpfake.NewGCS(t)
	env := envOn(t, f, g.Bucket(t, "runs"))
	defer env.Close()
	holder := runIDAt(0, "000100", "bbbb")
	exec := f.run.Start(gcp.JobName(appSlug, "web"))
	f.run.SetState(exec, backend.StateFailed)
	branch := "fugaro/" + rootID
	key := lock.Key(appSlug, branch)
	data, _ := json.Marshal(lock.Holder{RunID: holder, Execution: exec, ExpiresAt: time.Now().Add(time.Hour)})
	wantGen, err := env.bucket.Create(context.Background(), key, data, "application/json")
	if err != nil {
		t.Fatal(err)
	}
	g.DenyWrites("runs", "locks/")
	var errOut strings.Builder
	err = checkBranchLock(context.Background(), env, appSlug, branch, time.Now(), &errOut)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "an operator must clear it") ||
		!strings.Contains(err.Error(), "gcloud storage rm") || !strings.Contains(err.Error(), holder) {
		t.Fatalf("err = %v", err)
	}
	// The generation named is the one Takeover itself verified (its own
	// read, right before the refused delete), never a later re-read: a
	// second read, after the delete already failed, could by then see a
	// different run's lock and print its generation instead, which an
	// operator running the command by hand would delete.
	wantSuffix := fmt.Sprintf("#%d", wantGen)
	if !strings.HasSuffix(strings.TrimSpace(err.Error()), wantSuffix) {
		t.Fatalf("err = %v, want it to end with %s (the lock's own generation)", err, wantSuffix)
	}
	if ok, _ := env.bucket.Exists(context.Background(), key); !ok {
		t.Fatal("a forbidden delete must not be reported as if the lock were gone")
	}
}

// TestLockClearMessageNeverReReadsTheGeneration pins the fix directly: a
// mutation that had lockClearMessage (or its caller) read the lock fresh,
// instead of using the generation lock.Takeover already verified, would
// pass TestCheckBranchLockForbiddenDeleteNamesTheOperatorCommand above as
// long as nothing else touches the lock in between — which is exactly
// what a real race can do. Here, right where Takeover's own
// beforeTakeoverDelete hook fires (after its read, before its refused
// delete attempt), a new holder replaces the lock entirely, under a new
// generation. The printed command must still name the OLD, already-
// verified generation and run ID: a fresh read at this point would name
// the NEW holder's lock, which a launcher running the command by hand
// would then delete out from under a live run.
func TestLockClearMessageNeverReReadsTheGeneration(t *testing.T) {
	f := newCloudFixture(t)
	g := gcpfake.NewGCS(t)
	env := envOn(t, f, g.Bucket(t, "runs"))
	defer env.Close()
	holder := runIDAt(0, "000100", "bbbb")
	exec := f.run.Start(gcp.JobName(appSlug, "web"))
	f.run.SetState(exec, backend.StateFailed)
	branch := "fugaro/" + rootID
	key := lock.Key(appSlug, branch)
	data, _ := json.Marshal(lock.Holder{RunID: holder, Execution: exec, ExpiresAt: time.Now().Add(time.Hour)})
	wantGen, err := env.bucket.Create(context.Background(), key, data, "application/json")
	if err != nil {
		t.Fatal(err)
	}
	g.DenyWrites("runs", "locks/")
	rival := runIDAt(0, "000200", "cccc")
	var restore func()
	restore = lock.SetBeforeTakeoverDelete(func() {
		restore() // fire once
		rivalData, _ := json.Marshal(lock.Holder{RunID: rival, ExpiresAt: time.Now().Add(time.Hour)})
		g.Put("runs", key, rivalData) // a direct write: bypasses DenyWrites entirely
	})
	t.Cleanup(func() {
		if restore != nil {
			restore()
		}
	})
	err = checkBranchLock(context.Background(), env, appSlug, branch, time.Now(), io.Discard)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), holder) || strings.Contains(err.Error(), rival) {
		t.Fatalf("err = %v, want it to name %s, not the rival %s that replaced the lock afterward", err, holder, rival)
	}
	wantSuffix := fmt.Sprintf("#%d", wantGen)
	if !strings.HasSuffix(strings.TrimSpace(err.Error()), wantSuffix) {
		t.Fatalf("err = %v, want it to end with %s (the generation verified before the rival replaced the lock)", err, wantSuffix)
	}
}

// TestLockClearMessageRefusesToNameAnUnpinnedCommand: when Takeover never
// verified a generation at all (gen == 0 — its own read failed before any
// delete was even attempted), lockClearMessage must not print a gcloud
// storage rm command: an unpinned one would delete whatever is at that
// key when an operator eventually runs it, which could by then be a
// different, live run's lock. It says the lock couldn't be read and asks
// for a look by hand instead.
func TestLockClearMessageRefusesToNameAnUnpinnedCommand(t *testing.T) {
	f := newCloudFixture(t)
	bucket, err := blobx.Open(context.Background(), f.bucket)
	if err != nil {
		t.Fatal(err)
	}
	env := envOn(t, f, bucket)
	defer env.Close()
	msg := lockClearMessage(env, "locks/acme-app/deadbeef", 0, "20261010-000100-bbbb")
	if strings.Contains(msg, "gcloud storage rm") {
		t.Fatalf("message = %q, must not print an unpinned rm command", msg)
	}
	if !strings.Contains(msg, "could not be read") || !strings.Contains(msg, "20261010-000100-bbbb") {
		t.Fatalf("message = %q, want it to say the lock could not be read, naming the run", msg)
	}
}

// A new run acquires the branch lock between checkBranchLock's own
// backend-confirmed decision and lock.Takeover's delete: Takeover's
// generation-matched delete loses the race (ErrHolderChanged), and
// checkBranchLock must map that to "branch busy", never to success — a
// mutation that instead treated ErrHolderChanged as nil would launch a
// second run straight into the one just acquired.
// TestLockMessagesSanitizeTheRunID: a lock's run_id is read straight back
// from the lock object, which a launcher could write with any bytes in
// it (before locks/ is write-protected, or from a lock an old, buggy
// runner wrote); both the takeover note (checkBranchLock, on success) and
// lockClearMessage (on a refused delete) must never let a raw newline or
// an escape sequence from it reach the terminal.
func TestLockMessagesSanitizeTheRunID(t *testing.T) {
	const hostileID = "bbbb\nrival\x1b[31mred\x1b[0m"

	t.Run("the cleared note", func(t *testing.T) {
		f := newCloudFixture(t)
		seedRoot(t, f, rootID, time.Now().Add(-time.Hour))
		exec := f.run.Start(gcp.JobName(appSlug, "web"))
		f.run.SetState(exec, backend.StateFailed)
		data, _ := json.Marshal(lock.Holder{RunID: hostileID, Execution: exec, ExpiresAt: time.Now().Add(time.Hour)})
		putBuildObject(t, f, lock.Key(appSlug, "fugaro/"+rootID), data)
		_, errOut, err := execute(t, "run", "--repo", "acme/app", "--pr", "7")
		if err != nil {
			t.Fatalf("a lock whose holder's execution the backend confirms ended: %v (%s)", err, errOut)
		}
		if strings.Contains(errOut, "bbbb\nrival") || strings.Contains(errOut, "\x1b[31m") {
			t.Fatalf("stderr = %q, a raw control character or escape sequence from the run ID leaked through", errOut)
		}
	})

	t.Run("the operator command", func(t *testing.T) {
		f := newCloudFixture(t)
		g := gcpfake.NewGCS(t)
		env := envOn(t, f, g.Bucket(t, "runs"))
		defer env.Close()
		exec := f.run.Start(gcp.JobName(appSlug, "web"))
		f.run.SetState(exec, backend.StateFailed)
		branch := "fugaro/" + rootID
		key := lock.Key(appSlug, branch)
		data, _ := json.Marshal(lock.Holder{RunID: hostileID, Execution: exec, ExpiresAt: time.Now().Add(time.Hour)})
		if _, err := env.bucket.Create(context.Background(), key, data, "application/json"); err != nil {
			t.Fatal(err)
		}
		g.DenyWrites("runs", "locks/")
		err := checkBranchLock(context.Background(), env, appSlug, branch, time.Now(), io.Discard)
		if err == nil {
			t.Fatal("want a refusal")
		}
		if strings.Contains(err.Error(), "bbbb\nrival") || strings.Contains(err.Error(), "\x1b[31m") {
			t.Fatalf("err = %q, a raw control character or escape sequence from the run ID leaked through", err.Error())
		}
	})
}

func TestRunPRRefusesWhenLockChangesDuringTakeover(t *testing.T) {
	f := newCloudFixture(t)
	seedRoot(t, f, rootID, time.Now().Add(-time.Hour))
	holder := runIDAt(0, "000100", "bbbb")
	exec := f.run.Start(gcp.JobName(appSlug, "web"))
	f.run.SetState(exec, backend.StateFailed)
	key := lock.Key(appSlug, "fugaro/"+rootID)
	data, _ := json.Marshal(lock.Holder{RunID: holder, Execution: exec, ExpiresAt: time.Now().Add(time.Hour)})
	putBuildObject(t, f, key, data)

	rival := runIDAt(0, "000200", "cccc")
	var restore func()
	restore = lock.SetBeforeTakeoverDelete(func() {
		restore() // fire once
		rivalData, _ := json.Marshal(lock.Holder{RunID: rival, ExpiresAt: time.Now().Add(time.Hour)})
		putBuildObject(t, f, key, rivalData)
	})
	t.Cleanup(func() {
		if restore != nil {
			restore()
		}
	})
	n := len(f.run.Executions())
	_, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "branch busy: run "+holder) {
		t.Fatalf("err = %v, want branch busy naming %s", err, holder)
	}
	if len(f.run.Executions()) != n {
		t.Fatal("a follow-up launched despite the lock changing during its takeover")
	}
	path := filepath.Join(strings.TrimPrefix(f.bucket, "file://"), key)
	data2, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got lock.Holder
	if json.Unmarshal(data2, &got) != nil || got.RunID != rival {
		t.Fatalf("the rival's lock did not survive: %s", data2)
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

// A halted run that pushed is a valid previous_run, like any other status;
// a newer halted run that never pushed is skipped.
func TestRunPRAfterHaltedRun(t *testing.T) {
	f := newCloudFixture(t)
	now := time.Now()
	seedRoot(t, f, rootID, now.Add(-72*time.Hour))
	halted := runIDAt(1, "230000", "bbbb")
	seedFollowUp(t, f, halted, rootID, rootID, now.Add(-10*time.Hour), runstore.StatusHalted, true)
	early := runIDAt(0, "000100", "dddd")
	seedFollowUp(t, f, early, rootID, halted, now.Add(-time.Hour), runstore.StatusHalted, false)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "7", "--run-id", fuID); err != nil {
		t.Fatal(err)
	}
	if got := readSpec(t, f, fuID); got.PreviousRun != halted {
		t.Fatalf("previous_run = %s, want the halted run %s that pushed", got.PreviousRun, halted)
	}
}
