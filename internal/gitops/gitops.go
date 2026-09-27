// Package gitops wraps the git CLI operations a run needs.
package gitops

import (
	"bytes"
	"context"
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
// output after the git process itself has exited, or after ctx ends. A repo
// hook (or git's own background auto-gc) can fork a child that inherits the
// stdout/stderr pipes and keeps them open; without a bound, Wait (and so
// this call) would hang until that leaked child closes them on its own,
// however long that takes (see internal/procgroup, which guards the same
// way for agent invocations).
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

func (r *Repo) git(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.Dir
	cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0"), r.Env...)
	cmd.WaitDelay = gitWaitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
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

// Push pushes HEAD to branch on origin.
func (r *Repo) Push(ctx context.Context, branch string) error {
	_, err := r.git(ctx, "push", "--quiet", "origin", "HEAD:refs/heads/"+branch)
	return err
}
