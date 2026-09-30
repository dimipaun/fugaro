package runner

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitops"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// Restored exposes restored to the runner_test package.
type Restored = restored

// RestoreSession runs restoreSession for a run with d's workdir and
// environment, on the checkout already in d.WorkDir.
func RestoreSession(t *testing.T, ctx context.Context, d Deps, prev *runstore.Store, head string) Restored {
	t.Helper()
	repo, err := gitops.Open(d.WorkDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	r := &run{d: d, repo: repo}
	return r.restoreSession(ctx, prev, head)
}

// RecordSessionGit records the argument lists of every git call restore
// makes, for the rest of t.
func RecordSessionGit(t *testing.T) func() [][]string {
	var mu sync.Mutex
	var calls [][]string
	prev := sessionGit
	sessionGit = func(ctx context.Context, repo *gitops.Repo, args ...string) (string, error) {
		mu.Lock()
		calls = append(calls, slices.Clone(args))
		mu.Unlock()
		return prev(ctx, repo, args...)
	}
	t.Cleanup(func() { sessionGit = prev })
	return func() [][]string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(calls)
	}
}
