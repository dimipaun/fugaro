// Package gitops wraps the git CLI operations a run needs.
package gitops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// gitWaitDelay bounds how long a git invocation's Wait spends draining
// output after the git process itself has exited, or after ctx ends. A
// process git spawns can leave a descendant that inherits the stdout/stderr
// pipes and keeps them open. Repository hooks never run here (see noHooks),
// but the remote side's processes do: git's own `receive-pack`, unless
// receive.autogc is disabled on the remote, spawns a detached `git
// maintenance run --auto --quiet --detach` after nearly every push
// (confirmed with GIT_TRACE=1 against a real push; see
// internal/testutil.NewRemote, which disables it on the remotes tests
// create). Without a bound, Wait (and so this call) would hang until that
// leaked descendant closes them on its own, however long that takes (see
// internal/procgroup, which guards the same way for agent invocations). A
// real remote we don't control (M2's GitHub/Bitbucket) could still do this;
// unlike our own test remotes, we can't fix its config, so this bound is
// production's only defense.
const gitWaitDelay = 5 * time.Second

// Identity is the author and committer of commits made during a run.
var Identity = map[string]string{
	"GIT_AUTHOR_NAME":     "Fugaro",
	"GIT_AUTHOR_EMAIL":    "fugaro@users.noreply.invalid",
	"GIT_COMMITTER_NAME":  "Fugaro",
	"GIT_COMMITTER_EMAIL": "fugaro@users.noreply.invalid",
}

// IdentityEnv returns Identity as KEY=VALUE pairs.
func IdentityEnv() []string {
	out := make([]string, 0, len(Identity))
	for _, k := range slices.Sorted(maps.Keys(Identity)) {
		out = append(out, k+"="+Identity[k])
	}
	return out
}

// Repo is a git checkout. Env is added to the process environment for every git call.
type Repo struct {
	Dir string
	Env []string
}

// Open returns the checkout at dir.
func Open(dir string, env []string) (*Repo, error) {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return nil, fmt.Errorf("%s is not a git checkout: %w", dir, err)
	}
	return &Repo{Dir: dir, Env: env}, nil
}

// OpenOrClone returns the checkout at dir, cloning remote into it first if
// dir has none. In the container image the checkout is baked in.
func OpenOrClone(ctx context.Context, dir, remote string, env []string) (*Repo, error) {
	if r, err := Open(dir, env); err == nil {
		return r, nil
	}
	if remote == "" {
		return nil, fmt.Errorf("%s has no git checkout and no remote to clone", dir)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return nil, err
	}
	parent := &Repo{Dir: filepath.Dir(dir), Env: env}
	if _, err := parent.git(ctx, "clone", "--quiet", remote, dir); err != nil {
		return nil, err
	}
	return &Repo{Dir: dir, Env: env}, nil
}

// noHooks is prepended to every runner-owned git invocation. The runner's
// git is plumbing: a repository hook (husky, lint-staged, a pre-push test
// run) must never block bootstrap's checkout, finalize's commit or push, and
// --no-verify only skips pre-commit and commit-msg, not prepare-commit-msg,
// post-checkout, post-commit or pre-push. The agent's own git calls are
// unaffected; they run the hooks as usual.
var noHooks = []string{"-c", "core.hooksPath=" + os.DevNull}

func (r *Repo) git(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append(slices.Clone(noHooks), args...)...)
	cmd.Dir = r.Dir
	cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0"), r.Env...)
	cmd.WaitDelay = gitWaitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil && errors.Is(err, exec.ErrWaitDelay) && ctx.Err() == nil && cmd.ProcessState != nil && cmd.ProcessState.Success() {
		// git itself exited 0; WaitDelay fired only because a leaked
		// descendant still held the output pipe open. git had already
		// finished writing everything it was going to write before it
		// exited, so stdout/stderr are complete — this is not a real
		// failure, just the forced pipe closure that unblocked Wait.
		err = nil
	}
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// CheckoutNewBranch fetches ref from origin and points a fresh branch at it.
// Untracked files are removed but ignored ones (warm build caches) are kept.
func (r *Repo) CheckoutNewBranch(ctx context.Context, ref, branch string) error {
	if _, err := r.git(ctx, "fetch", "--quiet", "origin", ref); err != nil {
		return err
	}
	if _, err := r.git(ctx, "checkout", "--quiet", "-f", "-B", branch, "FETCH_HEAD"); err != nil {
		return err
	}
	_, err := r.git(ctx, "clean", "-fd", "--quiet")
	return err
}

// FetchBase updates origin/<base>, which AheadOf and the reviewer diff against.
func (r *Repo) FetchBase(ctx context.Context, base string) error {
	_, err := r.git(ctx, "fetch", "--quiet", "origin", "+refs/heads/"+base+":refs/remotes/origin/"+base)
	return err
}

// HeadSHA returns the commit HEAD points at.
func (r *Repo) HeadSHA(ctx context.Context) (string, error) {
	return r.git(ctx, "rev-parse", "HEAD")
}

// IsClean reports whether the working tree has no changes and no untracked files.
func (r *Repo) IsClean(ctx context.Context) (bool, error) {
	out, err := r.git(ctx, "status", "--porcelain")
	return out == "", err
}

// CommitAll commits every change, skipping repository hooks. It reports
// whether there was anything to commit.
func (r *Repo) CommitAll(ctx context.Context, msg string) (bool, error) {
	if _, err := r.git(ctx, "add", "-A"); err != nil {
		return false, err
	}
	if clean, err := r.IsClean(ctx); err != nil || clean {
		return false, err
	}
	_, err := r.git(ctx, "commit", "--quiet", "--no-verify", "-m", msg)
	return err == nil, err
}

// CommitEmpty makes a commit with no changes, so a PR can exist for a branch
// where the agent committed nothing.
func (r *Repo) CommitEmpty(ctx context.Context, msg string) error {
	_, err := r.git(ctx, "commit", "--quiet", "--no-verify", "--allow-empty", "-m", msg)
	return err
}

// AheadOf counts commits on HEAD that origin/<base> does not have.
func (r *Repo) AheadOf(ctx context.Context, base string) (int, error) {
	out, err := r.git(ctx, "rev-list", "--count", "origin/"+base+"..HEAD")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(out)
}

// RunBranchPrefix starts every branch the runner pushes.
const RunBranchPrefix = "fugaro/"

// OriginURL returns the URL of the checkout's origin remote.
func (r *Repo) OriginURL(ctx context.Context) (string, error) {
	return r.git(ctx, "remote", "get-url", "origin")
}

// Push pushes HEAD to the run branch on origin. The agent may already have
// pushed the branch and then amended or rebased it, so the push is forced,
// but under a lease: the remote branch is overwritten only if it is absent
// or its tip is a commit this checkout has (so it came from this run). A
// tip pushed from anywhere else is left alone and Push fails. Only
// fugaro/ branches are ever pushed, so the base branch cannot be touched.
func (r *Repo) Push(ctx context.Context, branch string) error {
	if !strings.HasPrefix(branch, RunBranchPrefix) || len(branch) == len(RunBranchPrefix) {
		return fmt.Errorf("refusing to push %q: the runner only pushes %s<run-id> branches", branch, RunBranchPrefix)
	}
	ref := "refs/heads/" + branch
	tip, err := r.remoteTip(ctx, ref)
	if err != nil {
		return fmt.Errorf("reading %s on origin: %w", branch, err)
	}
	if tip != "" {
		if _, err := r.git(ctx, "cat-file", "-e", tip+"^{commit}"); err != nil {
			return fmt.Errorf("%s on origin is at %s, a commit this run never had: something else pushed to the branch, so it is not overwritten", branch, tip)
		}
	}
	_, err = r.git(ctx, "push", "--quiet", "--force-with-lease="+ref+":"+tip, "origin", "HEAD:"+ref)
	return err
}

// remoteTip returns the commit ref points at on origin, or "" if it does not exist.
func (r *Repo) remoteTip(ctx context.Context, ref string) (string, error) {
	out, err := r.git(ctx, "ls-remote", "origin", ref)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if sha, name, ok := strings.Cut(line, "\t"); ok && name == ref {
			return sha, nil
		}
	}
	return "", nil
}
