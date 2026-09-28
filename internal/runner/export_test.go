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
