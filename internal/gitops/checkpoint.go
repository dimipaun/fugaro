package gitops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
// never forces. It returns nil, pushing nothing, when origin's branch is
// already at sha or ahead of it (the agent pushed by itself). When origin's
// tip is not an ancestor of sha (or is a commit the checkout never had),
// or moves to one between the check and the push, it refuses with
// ErrNotFastForward. Like Push, it only pushes fugaro/<run-id> branches.
//
// The agent controls .git/config while this runs, so:
//   - sha must be a full lowercase commit SHA, the one the caller read: not
//     HEAD, a ref name, an abbreviation or anything with a "+", so it can
//     neither move under the push nor turn the refspec into a forced one;
//   - origin's URL is read once and used for both the check and the push,
//     after "--", and a URL that looks like an option or holds a newline or
//     NUL is refused before git runs with it;
//   - it pushes to that URL rather than the remote's name, so git writes no
//     remote-tracking ref in the checkout.
//
// Errors name the remote "origin", never its URL.
//
// It is not given the base branch, so it would push a sha that is the
// base itself, or behind it: the caller guards that with CountAhead on the
// same sha before calling it (C6a).
func (r *Repo) PushFastForward(ctx context.Context, branch, sha string) error {
	if err := checkRunBranch(ctx, r, branch); err != nil {
		return err
	}
	if !IsFullSHA(sha) {
		return fmt.Errorf("refusing to push %q: not a full commit SHA", sha)
	}
	url, err := r.OriginURL(ctx)
	if err != nil {
		return err
	}
	if err := checkPushURL(url); err != nil {
		return err
	}
	ref := "refs/heads/" + branch
	tip, err := r.remoteTipAt(ctx, url, ref)
	if err != nil {
		return fmt.Errorf("reading %s on origin: %w", branch, hideURL(err, url))
	}
	if tip == sha {
		return nil
	}
	if tip != "" {
		forward, err := r.isAncestor(ctx, tip, sha)
		switch {
		case err != nil:
			return fmt.Errorf("checking %s on origin at %s: %w", branch, tip, err)
		case !forward:
			behind, err := r.isAncestor(ctx, sha, tip)
			if err != nil {
				return fmt.Errorf("checking %s on origin at %s: %w", branch, tip, err)
			}
			if behind {
				return nil // origin is already past sha: nothing to add
			}
			return fmt.Errorf("%s on origin is at %s: %w", branch, tip, ErrNotFastForward)
		}
	}
	if pushFFSeam != nil {
		pushFFSeam()
	}
	// No --force of any kind, and sha is plain hex, so no "+" either: the
	// remote itself refuses a non-fast-forward.
	_, err = r.git(ctx, "push", "--quiet", "--", url, sha+":"+ref)
	if err == nil {
		return nil
	}
	err = hideURL(err, url)
	if isNonFastForward(err) {
		return fmt.Errorf("%s on origin moved during the push: %w (%v)", branch, ErrNotFastForward, err)
	}
	return classified(err)
}

// pushFFSeam, when set by a test, runs between PushFastForward's checks
// and its push.
var pushFFSeam func()

// IsFullSHA reports whether s is a full commit SHA as git prints it: 40
// (SHA-1) or 64 (SHA-256) lowercase hex digits.
func IsFullSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// checkRange checks the bounds of a range a checkpoint inspects: until is
// the full SHA the checkpoint read; since is a full SHA (the last pushed
// commit) or a plain ref name such as origin/main (the work base).
func checkRange(since, until string) error {
	if !IsFullSHA(until) {
		return fmt.Errorf("refusing to read the commits up to %q: not a full commit SHA", until)
	}
	if !IsFullSHA(since) && !plainRef(since) {
		return fmt.Errorf("refusing to read the commits since %q: not a full commit SHA or a plain ref name", since)
	}
	return nil
}

// plainRef reports whether s is a ref name made only of letters, digits,
// ".", "_", "-" and "/", that does not start with "-" and holds no "..":
// nothing git could read as an option, a range or a revision expression.
func plainRef(s string) bool {
	if s == "" || s[0] == '-' || s[0] == '/' || strings.Contains(s, "..") {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == '-', c == '/':
		default:
			return false
		}
	}
	return true
}

// errUnsafeURL is a remote URL git could read as an option, or one that
// holds a character no URL has.
var errUnsafeURL = errors.New("origin's URL looks like an option or holds a newline or NUL")

func checkPushURL(url string) error {
	if url == "" || strings.HasPrefix(url, "-") || strings.ContainsAny(url, "\n\r\x00") {
		return fmt.Errorf("refusing to push: %w", errUnsafeURL)
	}
	return nil
}

// remoteTipAt is remoteTip against url rather than the remote's name.
func (r *Repo) remoteTipAt(ctx context.Context, url, ref string) (string, error) {
	out, err := r.git(ctx, "ls-remote", "--", url, ref)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if sha, name, ok := strings.Cut(line, "\t"); ok && name == ref {
			if !IsFullSHA(sha) {
				return "", fmt.Errorf("origin answered %q for %s, not a commit SHA", sha, ref)
			}
			return sha, nil
		}
	}
	return "", nil
}

// isAncestor reports whether a is an ancestor of b (or b itself). A commit
// the checkout does not have is no ancestor. Any other failure, a
// cancelled context or a timeout among them, is an error, never a "no".
func (r *Repo) isAncestor(ctx context.Context, a, b string) (bool, error) {
	_, err := r.git(ctx, "merge-base", "--is-ancestor", "--end-of-options", a, b)
	switch {
	case err == nil:
		return true, nil
	case ctx.Err() != nil || exitCode(err) < 0:
		return false, err // cancelled, timed out, or git never ran
	case exitCode(err) == 1:
		return false, nil
	}
	// git fails (128) on a commit it does not have: no ancestor. Anything
	// else is an error.
	if _, cerr := r.git(ctx, "cat-file", "-e", "--end-of-options", a+"^{commit}"); cerr != nil && ctx.Err() == nil && exitCode(cerr) > 0 {
		return false, nil
	}
	return false, err
}

// exitCode is the exit status of the git that failed with err, or -1 when
// git did not run or was killed (a cancelled context, a timeout).
func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// isNonFastForward reports whether git refused a push because the remote
// branch is not an ancestor of what was pushed.
func isNonFastForward(err error) bool {
	m := err.Error()
	return strings.Contains(m, "[rejected]") && (strings.Contains(m, "non-fast-forward") || strings.Contains(m, "fetch first"))
}

// urlHidden is an error whose text names the remote "origin" where git
// wrote its URL: the URL is the agent's to set, and a caller logs the text.
type urlHidden struct {
	err error
	url string
}

func (e *urlHidden) Error() string { return strings.ReplaceAll(e.err.Error(), e.url, "origin") }
func (e *urlHidden) Unwrap() error { return e.err }

func hideURL(err error, url string) error {
	if err == nil || url == "" {
		return err
	}
	return &urlHidden{err: err, url: url}
}
