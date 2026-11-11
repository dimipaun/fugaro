package gitops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// RebaseContinue stages the resolution of the paths git stopped on and lets
// the rebase go on. Only the unmerged paths are staged, not the whole tree:
// the commit being replayed must stay the commit it was, and a stage that
// edited a file the conflict had nothing to do with would otherwise have that
// edit carried into it. No editor ever opens — the commit message of the
// commit being replayed is the one it already had — and no hook runs, as on
// every other commit this package makes for a run.
//
// ErrRebaseConflict says the rebase stopped again: the resolution left paths
// git could not carry over, another commit of the branch conflicts with the
// base, or nothing was resolved at all.
func (r *Repo) RebaseContinue(ctx context.Context) error {
	paths, err := r.UnmergedPaths(ctx)
	if err != nil {
		return err
	}
	if len(paths) > 0 {
		// A resolution nobody made is refused rather than committed: the
		// working tree still holds the markers git wrote, and staging them
		// would carry those lines into the commit being replayed and out to
		// the branch as content.
		if left, err := r.conflictMarkersLeft(ctx, paths); err != nil {
			return err
		} else if left {
			return fmt.Errorf("%s still holds the conflict markers git wrote: %w", strings.Join(paths, ", "), ErrRebaseConflict)
		}
		if _, err := r.git(ctx, append([]string{"add", "--"}, paths...)...); err != nil {
			return err
		}
	}
	env := append(r.processEnv(), "GIT_EDITOR=true")
	if err := r.gitRun(ctx, env, noHooks, nil, nil, "rebase", "--continue"); err != nil {
		if r.RebaseInProgress(ctx) {
			// Still unfinished: git stopped again, which for a branch of more
			// than one commit is the next commit conflicting with the base.
			// That is the sentinel too, as Rebase's own conflict is — the
			// caller resolves it or refuses over it — rather than git's error,
			// which reads as a fault of the call: the runner's loop is bounded
			// by this reading (rebaseResolves), and an unreadable error there
			// ends the run as an infrastructure fault, which is retryable and
			// retries into the same conflict.
			return fmt.Errorf("continuing the rebase: %w", ErrRebaseConflict)
		}
		return err
	}
	return nil
}

// leftoverMarker is what `git diff --check` calls a line git wrote into a
// conflicted file and nobody took out: the reading is English, as
// isNonFastForward's is, and a git that localizes it leaves the resolution
// to the rebase itself, which refuses to continue over unmerged paths.
const leftoverMarker = "leftover conflict marker"

// conflictMarkersLeft reports whether any of paths, as they now are in the
// working tree, still holds a line git wrote as a conflict marker. It is
// `git diff --check` over exactly those paths against the index, which is
// git's own reading of a leftover marker (a whitespace problem is not one:
// only that message counts) — asked before the resolution is staged, because
// what is judged is the file the stage left, not the diff of the commit
// being replayed.
func (r *Repo) conflictMarkersLeft(ctx context.Context, paths []string) (bool, error) {
	var out bytes.Buffer
	// --check exits non-zero when it found anything, so its error is the
	// report itself: only what it wrote is read, and a git that never ran
	// writes nothing and is caught by the continue below.
	if err := r.gitIO(ctx, nil, &out, append([]string{"diff", "--check", "--"}, paths...)...); err != nil && out.Len() == 0 {
		return false, err
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasSuffix(line, ": "+leftoverMarker) {
			return true, nil
		}
	}
	return false, nil
}

// UnmergedPaths are the paths git stopped the rebase on: the conflicted ones,
// which the index still holds both sides of. A resolution that replaced the
// markers in the working tree is listed until RebaseContinue stages it, so a
// caller reads this to name what conflicts, never to tell a resolved file
// from an unresolved one.
func (r *Repo) UnmergedPaths(ctx context.Context) ([]string, error) {
	out, err := r.git(ctx, "diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
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
