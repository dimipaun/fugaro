package gitops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// rebaseState are what git keeps in the git directory while a rebase is
// unfinished: rebase-merge for a merge-backed one (the default, and the
// interactive), rebase-apply for the patch-backed one git am runs. The two
// names are read by RebaseInProgress and by the checkpoint's own inProgress
// list, which is the two of them plus the other unfinished operations.
var rebaseState = []string{"rebase-merge", "rebase-apply"}

// ErrRebaseConflict means the rebase stopped on a conflict: the branch could
// not be put on its base without one. The checkout is left where git stopped
// it — the rebase in progress and the markers written into the conflicted
// files — so the caller decides between resolving them and letting the rebase
// go on (RebaseContinue) and putting the branch back (RebaseAbort). It is not
// an error of the git call: the rebase started and went exactly as far as the
// history allows.
var ErrRebaseConflict = errors.New("the rebase stopped on a conflict")

// Rebase puts the checked-out branch on origin/<base>, which the caller has
// fetched (FetchBase). origin/<base> rather than a bare <base>: the checkout
// carries the run's own refs, and the base is what the remote has.
//
// A conflict is ErrRebaseConflict and leaves the checkout mid-rebase; every
// other failure (a dirty tree, a base that is not there) is git's own error
// and leaves no rebase behind.
func (r *Repo) Rebase(ctx context.Context, base string) error {
	if !plainRef(base) {
		return fmt.Errorf("refusing to rebase onto %q: not a plain ref name", base)
	}
	if _, err := r.git(ctx, "rebase", "origin/"+base); err != nil {
		if r.RebaseInProgress(ctx) {
			return fmt.Errorf("rebasing onto origin/%s: %w", base, ErrRebaseConflict)
		}
		return err
	}
	return nil
}

// RebaseInProgress reports whether the checkout holds a rebase git has not
// finished. It only reads: it stats the git directory the way CheckpointTip
// does, so it is safe while the agent's own git runs.
func (r *Repo) RebaseInProgress(ctx context.Context) bool {
	gitDir, err := r.git(ctx, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return false
	}
	for _, name := range rebaseState {
		if _, err := os.Stat(filepath.Join(gitDir, name)); err == nil {
			return true
		}
	}
	return false
}

// RebaseContinue stages what the resolution left and lets the rebase go on.
// Everything is staged, the way a person's `git add -A` before a continue is:
// the conflicted paths carry the markers git wrote until the resolution
// replaced them, and a resolution that added or removed a file has to reach
// the commit the same way. No editor ever opens — the commit message of the
// commit being replayed is the one it already had — and no hook runs, as on
// every other commit this package makes for a run.
//
// ErrRebaseConflict says the rebase stopped again: the resolution left paths
// git could not carry over, or another commit of the branch conflicts with
// the base.
func (r *Repo) RebaseContinue(ctx context.Context) error {
	if _, err := r.git(ctx, "add", "-A"); err != nil {
		return err
	}
	env := append(r.processEnv(), "GIT_EDITOR=true")
	if err := r.gitRun(ctx, env, noHooks, nil, nil, "rebase", "--continue"); err != nil {
		if r.RebaseInProgress(ctx) {
			return fmt.Errorf("continuing the rebase: %w", err)
		}
		return err
	}
	return nil
}

// RebaseAbort puts the branch back where the rebase found it: the commits it
// replayed are dropped and the working tree is the one the run started from.
// It is what a run that cannot resolve a conflict calls before it refuses, so
// what it leaves behind is a branch nobody pushed anything for.
func (r *Repo) RebaseAbort(ctx context.Context) error {
	if _, err := r.git(ctx, "rebase", "--abort"); err != nil {
		return fmt.Errorf("aborting the rebase: %w", err)
	}
	return nil
}
