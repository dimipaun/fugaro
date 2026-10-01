package runner

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/cache"
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

// RecordWriteTimeout exposes recordWriteTimeout.
const RecordWriteTimeout = recordWriteTimeout

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

func (h *RunHandle) HaltNow(hl runstore.Halt) bool      { return h.r.haltNow(hl) }
func (h *RunHandle) CancelHaltedStage(hl runstore.Halt) { h.r.cancelHaltedStage(hl) }
func (h *RunHandle) MarkCancelled() bool                { return h.r.markCancelled() }
func (h *RunHandle) IsCancelled() bool                  { return h.r.isCancelled() }
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
