package gitops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrNotFastForward means the run branch on origin is not an ancestor of
// the commit to push: the agent rewrote commits already pushed, or someone
// else pushed. A checkpoint never forces, so it leaves the branch as it is.
var ErrNotFastForward = errors.New("the branch on origin is not an ancestor of the commit to push")

// ErrGitBusy means the checkout is not settled on the branch: HEAD is
// elsewhere (detached, another branch) or a merge, rebase, cherry-pick,
// revert or bisect is in progress. A checkpoint waits for its next tick.
var ErrGitBusy = errors.New("the checkout is not settled on the branch")

// inProgress are what git keeps in the git directory while an operation
// is unfinished.
var inProgress = []string{"rebase-merge", "rebase-apply", "MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "BISECT_LOG"}

// CheckpointTip returns the tip of the local branch when the checkout is
// settled on it: HEAD is the symbolic ref refs/heads/<branch> and no
// operation is in progress. It only reads: it takes no lock and writes
// nothing, so it is safe while the agent's own git runs.
func (r *Repo) CheckpointTip(ctx context.Context, branch string) (string, error) {
	head, err := r.git(ctx, "symbolic-ref", "-q", "HEAD")
	if err != nil || head != "refs/heads/"+branch {
		return "", ErrGitBusy // detached (symbolic-ref exits 1) or elsewhere
	}
	gitDir, err := r.git(ctx, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}
	for _, name := range inProgress {
		if _, err := os.Stat(filepath.Join(gitDir, name)); err == nil {
			return "", ErrGitBusy
		}
	}
	return r.git(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch+"^{commit}")
}

// PushFastForward pushes sha to branch on origin only as a fast-forward: it
// never forces. When origin already has sha there it does nothing; when
// origin's tip is not an ancestor of sha (or is a commit the checkout
// never had) it refuses with ErrNotFastForward. It pushes the given sha,
// not HEAD, and to origin's URL rather than the remote's name, so git
// writes no remote-tracking ref in the checkout. Like Push, it only pushes
// fugaro/<run-id> branches.
func (r *Repo) PushFastForward(ctx context.Context, branch, sha string) error {
	if err := checkRunBranch(ctx, r, branch); err != nil {
		return err
	}
	ref := "refs/heads/" + branch
	tip, err := r.remoteTip(ctx, ref)
	if err != nil {
		return fmt.Errorf("reading %s on origin: %w", branch, err)
	}
	if tip == sha {
		return nil
	}
	if tip != "" {
		if _, err := r.git(ctx, "merge-base", "--is-ancestor", tip, sha); err != nil {
			return fmt.Errorf("%s on origin is at %s: %w", branch, tip, ErrNotFastForward)
		}
	}
	url, err := r.OriginURL(ctx)
	if err != nil {
		return err
	}
	// No --force of any kind: the remote itself refuses a non-fast-forward.
	_, err = r.git(ctx, "push", "--quiet", url, sha+":"+ref)
	return classified(err)
}
