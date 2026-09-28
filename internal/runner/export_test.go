package runner

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/cache"
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
