package runner

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/cache"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gateway"
	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/lock"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// ToolchainHash exposes toolchainHash to the runner_test package.
var ToolchainHash = toolchainHash

// SetRestoreBound makes cache restore time out after d for the rest of t.
func SetRestoreBound(t *testing.T, d time.Duration) {
	prev := restoreBound
	restoreBound = func(time.Duration) time.Duration { return d }
	t.Cleanup(func() { restoreBound = prev })
}

// SetSlowRestore makes the first cache restore of t block until its
// context ends, as a slow archive would; later restores run normally.
func SetSlowRestore(t *testing.T) {
	prev := restoreCache
	first := true
	restoreCache = func(ctx context.Context, s *cache.Store, key string, roots []string) (bool, error) {
		if first {
			first = false
			<-ctx.Done()
			return false, fmt.Errorf("restoring %s: %w", key, ctx.Err())
		}
		return prev(ctx, s, key, roots)
	}
	t.Cleanup(func() { restoreCache = prev })
}

// CountRestoreCacheCalls makes every cache restore for the rest of t
// increment the returned counter before running as usual, so a test can
// assert restoreCaches never even attempted one (review mode, lockcache.go).
func CountRestoreCacheCalls(t *testing.T) *int {
	prev := restoreCache
	n := 0
	restoreCache = func(ctx context.Context, s *cache.Store, key string, roots []string) (bool, error) {
		n++
		return prev(ctx, s, key, roots)
	}
	t.Cleanup(func() { restoreCache = prev })
	return &n
}

// SetCreateRecord replaces the runner's create-if-absent record write for
// the rest of t.
func SetCreateRecord(t *testing.T, f func(s *runstore.Store, ctx context.Context, rec *runstore.Record) error) {
	prev := createRecord
	createRecord = f
	t.Cleanup(func() { createRecord = prev })
}

// SetWriteRecord replaces the runner's record overwrite for the rest of t.
func SetWriteRecord(t *testing.T, f func(s *runstore.Store, ctx context.Context, rec *runstore.Record) error) {
	prev := writeRecord
	writeRecord = f
	t.Cleanup(func() { writeRecord = prev })
}

// SetLockRelease replaces the branch lock's release for the rest of t.
func SetLockRelease(t *testing.T, f func(l *lock.Lock, ctx context.Context) error) {
	prev := releaseBranchLock
	releaseBranchLock = f
	t.Cleanup(func() { releaseBranchLock = prev })
}

// SetMarkLockReleasing replaces the branch lock's self-release marking
// for the rest of t.
func SetMarkLockReleasing(t *testing.T, f func(l *lock.Lock, ctx context.Context, now time.Time) error) {
	prev := markLockReleasing
	markLockReleasing = f
	t.Cleanup(func() { markLockReleasing = prev })
}

// RecordWriteTimeout exposes recordWriteTimeout.
const RecordWriteTimeout = recordWriteTimeout

// PRAttempts exposes prAttempts: how many times one open of the pull request
// tries before it gives up.
const PRAttempts = prAttempts

// MaxCredentialResumes exposes maxCredentialResumes.
const MaxCredentialResumes = maxCredentialResumes

// CredentialsServiceURLEnv and MetadataHostEnv are where a cascade run reads
// its upstream from, exposed so a runner_test run can be pointed at a
// stand-in the same way a job is pointed at the real service.
const (
	CredentialsServiceURLEnv = credentialsServiceURLEnv
	MetadataHostEnv          = metadataHostEnv
)

// RunHandle reaches a run's halt and cancel hooks from a test's agent.
type RunHandle struct{ r *run }

// OnRun makes every Run of t hand its run to f as it starts.
func OnRun(t *testing.T, f func(*RunHandle)) {
	prev := onNewRun
	onNewRun = func(r *run) { f(&RunHandle{r}) }
	t.Cleanup(func() { onNewRun = prev })
}

// SetHaltGrace shortens how long a halted stage's agent gets to exit by itself.
func SetHaltGrace(t *testing.T, d time.Duration) {
	prev := haltGrace
	haltGrace = d
	t.Cleanup(func() { haltGrace = prev })
}

// SetStrictHaltCheck makes finalize panic when a halt and a cancel are both recorded.
func SetStrictHaltCheck(t *testing.T) {
	prev := strictHaltCheck
	strictHaltCheck = true
	t.Cleanup(func() { strictHaltCheck = prev })
}

// SetCredsWait replaces the sleep a credential wait takes (C1.17) for the
// rest of t: a real wait is the reset the service named, minutes or hours
// away, so a test either returns at once or holds the wait itself.
func SetCredsWait(t *testing.T, f func(ctx context.Context, d time.Duration) error) {
	prev := credsWait
	credsWait = f
	t.Cleanup(func() { credsWait = prev })
}

// NoteExhausted is the credential service's typed `exhausted_until` answer
// (C1.10) as C1.15's upstream delivers it: it ends the stage in flight, and
// stage then waits for the reset and resumes the stage's session.
func (h *RunHandle) NoteExhausted(reset time.Time) {
	h.r.noteExhausted(credsvc.ExhaustedUntil{ResetAt: reset})
}

// ServiceAnswer is the same answer arriving the way it does in a real run:
// internal/gateway parses the credentials service's answer out of its wire
// body and hands it to the run through the upstream's OnServiceAnswer hook
// (C1.15), which is this function. A test that calls it therefore drives the
// run through the wired path and not around it, without standing up the
// service a whole cascade run needs to start (C1.19's own tests do, against
// a stand-in: TestServiceAnswerReachesTheRunsWait and the record's tests in
// gateway_service_internal_test.go).
func (h *RunHandle) ServiceAnswer(a gateway.ServiceAnswer) { h.r.serviceAnswer(a) }

func (h *RunHandle) HaltNow(hl runstore.Halt) bool { return h.r.haltNow(hl) }
func (h *RunHandle) GovernanceHalt(ctx context.Context, reason runstore.HaltReason, detail string) bool {
	return h.r.governanceHalt(ctx, reason, detail)
}

func (h *RunHandle) CancelHaltedStage(hl runstore.Halt) { h.r.cancelHaltedStage(hl) }
func (h *RunHandle) MarkCancelled() bool                { return h.r.markCancelled() }
func (h *RunHandle) Stage() string                      { return h.r.rec.Stage }
func (h *RunHandle) IsCancelled() bool                  { return h.r.isCancelled() }

// Halted reports whether a halt has been recorded yet, which marks the instant
// a governance halt's own stop of the stage's checkpointer begins to wait.
func (h *RunHandle) Halted() bool { return h.r.haltValue() != nil }

// StageCkptStopRegistered reports whether the running stage's checkpointer stop
// is registered: what a mid-stage halt reads before it stops the poll and waits
// for it (stopStageCheckpointer). A test that cannot call the halt itself —
// its own writes are only ordered when its caller is the run goroutine or one
// it joins, which the panic path has neither of — probes the read with this.
func (h *RunHandle) StageCkptStopRegistered() bool {
	h.r.mu.Lock()
	defer h.r.mu.Unlock()
	return h.r.stageCkptStop != nil
}

// Secrets is the values the run's redactor knows at the moment the hook is
// called, and Redact is that redactor: together they are how a test asserts
// that something the run holds (the launch's seal key) is registered before
// bootstrap and hidden from everything the run publishes.
func (h *RunHandle) Secrets() []string      { return h.r.secretList() }
func (h *RunHandle) Redact(s string) string { return h.r.redact(s) }
func (h *RunHandle) SetStageExtra(f func(stage string) ([]string, int64)) {
	h.r.mu.Lock()
	h.r.stageExtra = f
	h.r.mu.Unlock()
}

// ForceHaltAndCancel records both, which the lock rules out, to test the assertion.
func (h *RunHandle) ForceHaltAndCancel(hl runstore.Halt) {
	h.r.mu.Lock()
	h.r.halt, h.r.cancelled = &hl, true
	h.r.mu.Unlock()
}

// SetEarlyOpenTimeout bounds the early PR open to d for the rest of t.
func SetEarlyOpenTimeout(t *testing.T, d time.Duration) {
	prev := earlyOpenTimeout
	earlyOpenTimeout = d
	t.Cleanup(func() { earlyOpenTimeout = prev })
}

// DriveCheckpoints replaces the checkpoint poll's ticker for the rest of
// t: the returned tick sends one poll to the running stage's checkpointer
// and returns once that poll has finished. It fails t when no checkpointer
// takes the poll within 10 seconds.
func DriveCheckpoints(t *testing.T) (tick func()) {
	ch := make(chan time.Time)
	done := make(chan struct{})
	prevTicks, prevTicked := checkpointTicks, checkpointTicked
	checkpointTicks = func(time.Duration) (<-chan time.Time, func()) { return ch, func() {} }
	checkpointTicked = func() { done <- struct{}{} }
	t.Cleanup(func() { checkpointTicks, checkpointTicked = prevTicks, prevTicked })
	return func() {
		t.Helper()
		select {
		case ch <- time.Time{}:
		case <-time.After(10 * time.Second):
			t.Fatal("no checkpointer took the poll")
		}
		<-done
	}
}

// CheckpointersRunning is how many checkpoint goroutines are alive.
func CheckpointersRunning() int32 { return checkpointersRunning.Load() }

// The checkpoint schedule's durations, for tests that move a fake clock.
const (
	CheckpointQuiet    = checkpointQuiet
	CheckpointMinGap   = checkpointMinGap
	CheckpointFallback = checkpointFallback
)

// SetCheckpointPushedSeam runs f in a checkpoint between its successful
// push and saving pushed_head, for the rest of t.
func SetCheckpointPushedSeam(t *testing.T, f func()) {
	prev := checkpointPushedSeam
	checkpointPushedSeam = f
	t.Cleanup(func() { checkpointPushedSeam = prev })
}

// SetFetchedBaseSeam runs f in bootstrap right after the base branch is
// fetched, for the rest of t.
func SetFetchedBaseSeam(t *testing.T, f func()) {
	prev := fetchedBaseSeam
	fetchedBaseSeam = f
	t.Cleanup(func() { fetchedBaseSeam = prev })
}

// SetCheckoutReviewHeadsSeam runs f in reviewMode right before
// checkoutReviewHeads, for the rest of t.
func SetCheckoutReviewHeadsSeam(t *testing.T, f func()) {
	prev := checkoutReviewHeadsSeam
	checkoutReviewHeadsSeam = f
	t.Cleanup(func() { checkoutReviewHeadsSeam = prev })
}

// SetReviewStageDoneSeam runs f in reviewMode right after the review stage
// returns successfully, for the rest of t.
func SetReviewStageDoneSeam(t *testing.T, f func()) {
	prev := reviewStageDoneSeam
	reviewStageDoneSeam = f
	t.Cleanup(func() { reviewStageDoneSeam = prev })
}

// SetPrefetchBound makes each prefetch fetch time out after d, instead of
// prefetchBound, for the rest of t.
func SetPrefetchBound(t *testing.T, d time.Duration) {
	prev := prefetchBound
	prefetchBound = d
	t.Cleanup(func() { prefetchBound = prev })
}

// SetPrefetchFetch replaces the fetch prefetchObjects makes through the
// checkout's own git with f, for the rest of t. f gets the checkout, the
// fetch's context (already bounded by prefetchBound), what is being
// fetched, the base and the head.
func SetPrefetchFetch(t *testing.T, f func(repo *gitops.Repo, ctx context.Context, what, base, head string) error) {
	prev := prefetchFetch
	prefetchFetch = f
	t.Cleanup(func() { prefetchFetch = prev })
}

// SanitiseName exposes the artifact name sanitiser (artifact.go's
// sanitiseName) to the runner_test package: Review Focus 3's refusal table is
// a table over it.
func SanitiseName(name string) (string, error) { return sanitiseName(name) }

// ArtifactContentType exposes the recorded content type (artifact.go's
// artifactContentType) to the runner_test package: the sniff fallback only
// runs for a name whose extension the mime table does not know, which no
// fixture artifact is, so the branch needs its own table. The bytes it takes
// are the head CopyFileHashed hands the staging step.
func ArtifactContentType(name string, head []byte) string { return artifactContentType(name, head) }

// CopyFileHashed exposes the staging copy (artifact.go's copyFileHashed): the
// table for ArtifactContentType copies real files through it, so the head a
// type is sniffed from is the one the staging step really takes — the first
// contentSniffLen bytes of what was written — rather than bytes a test chose.
var CopyFileHashed = copyFileHashed

// SetArtifactCheckedSeam runs f in stageArtifacts after every match has been
// examined and sized against the caps and before the first copy: it is how a
// test changes a match between the check and the use of it (the window a
// background process has, and the one the copy's own open and bound close).
func SetArtifactCheckedSeam(t *testing.T, f func()) {
	prev := artifactCheckedSeam
	artifactCheckedSeam = f
	t.Cleanup(func() { artifactCheckedSeam = prev })
}

// SetArtifactCopiedSeam runs f in stageArtifacts after one match has been
// copied into the staging directory and before its recorded content type is
// taken: the window between the staged file existing and the record describing
// it, which is where a swap at the staged path lands.
func SetArtifactCopiedSeam(t *testing.T, f func()) {
	prev := artifactCopiedSeam
	artifactCopiedSeam = f
	t.Cleanup(func() { artifactCopiedSeam = prev })
}

// SetArtifactStagedSeam runs f in collectArtifacts after the set is staged
// and hashed and before anything is uploaded, for the rest of t: it is how a
// test replaces a staged file between the hash and the upload (Review Focus
// 5).
func SetArtifactStagedSeam(t *testing.T, f func()) {
	prev := artifactStagedSeam
	artifactStagedSeam = f
	t.Cleanup(func() { artifactStagedSeam = prev })
}

// DigestRecords runs the digest step's manifest read against store alone, so a
// test can watch which objects one manifest makes it ask for.
func DigestRecords(ctx context.Context, store *runstore.Store, ids []string) ([]runstore.Record, int, error) {
	r := &run{d: Deps{Store: store}}
	return r.digestRecords(ctx, ids)
}
