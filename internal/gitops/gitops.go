// Package gitops wraps the git CLI operations a run needs.
package gitops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
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

// ModelCredentialVars are the variables the runner keeps out of every git
// call (Repo.StripEnv): a command an agent-written .git/config makes git
// run must never see the model credential.
var ModelCredentialVars = []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN"}

// Repo is a git checkout. Env is added to the process environment for every git call.
type Repo struct {
	Dir string
	Env []string
	// StripEnv names variables that never reach git or anything git runs
	// (a core.fsmonitor command or a filter driver an agent wrote into
	// .git/config), whether they come from the process or from Env.
	StripEnv []string
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
func OpenOrClone(ctx context.Context, dir, remote string, env []string, strip ...string) (*Repo, error) {
	if r, err := Open(dir, env); err == nil {
		r.StripEnv = strip
		return r, nil
	}
	if remote == "" {
		return nil, fmt.Errorf("%s has no git checkout and no remote to clone", dir)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return nil, err
	}
	parent := &Repo{Dir: filepath.Dir(dir), Env: env, StripEnv: strip}
	if _, err := parent.git(ctx, "clone", "--quiet", remote, dir); err != nil {
		return nil, err
	}
	return &Repo{Dir: dir, Env: env, StripEnv: strip}, nil
}

// noHooks is prepended to every runner-owned git invocation. The runner's
// git is plumbing: a repository hook (husky, lint-staged, a pre-push test
// run) must never block bootstrap's checkout, finalize's commit or push, and
// --no-verify only skips pre-commit and commit-msg, not prepare-commit-msg,
// post-checkout, post-commit or pre-push. The agent's own git calls are
// unaffected; they run the hooks as usual.
var noHooks = []string{"-c", "core.hooksPath=" + os.DevNull}

func (r *Repo) git(ctx context.Context, args ...string) (string, error) {
	out, err := r.gitRaw(ctx, args...)
	return strings.TrimSpace(string(out)), err
}

// processEnv is the environment of every git call: the process's, then
// Env, without the variables in StripEnv.
// Environ is the environment of every git call on r.
func (r *Repo) Environ() []string { return r.processEnv() }

func (r *Repo) processEnv() []string {
	env := append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0"), r.Env...)
	// A provider key (FUGARO_PROVIDER_KEY_*) is the gateway's alone: no git
	// call, and nothing git runs, ever sees one, whatever StripEnv says.
	return slices.DeleteFunc(env, func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return config.IsProviderKeyEnv(k) || slices.Contains(r.StripEnv, k)
	})
}

// gitRaw runs git and returns its stdout exactly as written.
func (r *Repo) gitRaw(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append(slices.Clone(noHooks), args...)...)
	cmd.Dir = r.Dir
	cmd.Env = r.processEnv()
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
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
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

// DefaultBranch asks origin which branch its HEAD names (the repository's
// default branch), so it is never a name the checked-out files chose.
func (r *Repo) DefaultBranch(ctx context.Context) (string, error) {
	out, err := r.git(ctx, "ls-remote", "--symref", "origin", "HEAD")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if ref, ok := strings.CutPrefix(line, "ref: refs/heads/"); ok {
			if name, _, ok := strings.Cut(ref, "\tHEAD"); ok && name != "" {
				return name, nil
			}
		}
	}
	return "", fmt.Errorf("origin does not say which branch is its default")
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
	return r.countAhead(ctx, base, "HEAD")
}

// CountAhead counts the commits of until that origin/<base> does not have.
// A checkpoint asks about the tip it read, never about HEAD, which the
// agent may have moved since, so until must be a full commit SHA.
func (r *Repo) CountAhead(ctx context.Context, base, until string) (int, error) {
	if !IsFullSHA(until) {
		return 0, fmt.Errorf("refusing to count the commits of %q: not a full commit SHA", until)
	}
	return r.countAhead(ctx, base, until)
}

func (r *Repo) countAhead(ctx context.Context, base, until string) (int, error) {
	out, err := r.git(ctx, "rev-list", "--count", "--end-of-options", "origin/"+base+".."+until)
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

// ShowFile returns the file at path in revision rev (`git show
// rev:path`), whatever the working tree holds. path must be relative and
// clean, without "." or ".." segments, and rev must not look like an
// option, so neither can reach outside the revision's tree or into git's
// arguments.
func (r *Repo) ShowFile(ctx context.Context, rev, path string) ([]byte, error) {
	if rev == "" || strings.HasPrefix(rev, "-") || strings.ContainsAny(rev, ": \t\n") {
		return nil, fmt.Errorf("refusing to read a file at revision %q", rev)
	}
	if !cleanRelPath(path) {
		return nil, fmt.Errorf("refusing to read %q: the path must be relative and clean, without ..", path)
	}
	return r.gitRaw(ctx, "show", "--no-textconv", rev+":"+path)
}

// BlobSize returns the size in bytes of the file at path in revision rev
// (`git cat-file -s`), without reading it. path and rev are checked as
// ShowFile checks them; a path the tree lacks is an error wrapping
// fs.ErrNotExist.
func (r *Repo) BlobSize(ctx context.Context, rev, path string) (int64, error) {
	if rev == "" || strings.HasPrefix(rev, "-") || strings.ContainsAny(rev, ": \t\n") {
		return 0, fmt.Errorf("refusing to size a file at revision %q", rev)
	}
	if !cleanRelPath(path) {
		return 0, fmt.Errorf("refusing to size %q: the path must be relative and clean, without ..", path)
	}
	if mode, err := r.TreeEntryMode(ctx, rev, path); err != nil {
		return 0, err
	} else if mode == "" {
		return 0, fmt.Errorf("%s at %s: %w", path, rev, fs.ErrNotExist)
	}
	out, err := r.git(ctx, "cat-file", "-s", rev+":"+path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(out), 10, 64)
}

// TreeEntryMode returns the mode of path in revision rev's tree
// (`git ls-tree`), such as "100644" for a file or "120000" for a symbolic
// link, or "" when the tree has no such entry. path must be as ShowFile
// accepts it.
func (r *Repo) TreeEntryMode(ctx context.Context, rev, path string) (string, error) {
	if rev == "" || strings.HasPrefix(rev, "-") || strings.ContainsAny(rev, ": \t\n") {
		return "", fmt.Errorf("refusing to read the tree of revision %q", rev)
	}
	if !cleanRelPath(path) {
		return "", fmt.Errorf("refusing to look up %q: the path must be relative and clean, without ..", path)
	}
	out, err := r.git(ctx, "ls-tree", rev, "--", path)
	if err != nil || out == "" {
		return "", err
	}
	mode, _, _ := strings.Cut(out, " ")
	return mode, nil
}

// cleanRelPath reports whether p is a relative, clean, slash-separated
// path with no empty, "." or ".." segment, that doesn't start with "-".
func cleanRelPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "-") || strings.ContainsAny(p, "\\\x00") {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// ErrForeignTip means the remote branch's tip is a commit this run never
// had: someone else pushed to it. Push leaves it alone.
var ErrForeignTip = errors.New("something else pushed to the branch")

// ErrBranchGone means PushExisting found no remote branch to update.
var ErrBranchGone = errors.New("the branch no longer exists on origin")

// RemoteTip returns the commit branch points at on origin, or "" when
// origin has no such branch.
func (r *Repo) RemoteTip(ctx context.Context, branch string) (string, error) {
	return r.remoteTip(ctx, "refs/heads/"+branch)
}

// Push pushes HEAD to the run branch on origin. The agent may already have
// pushed the branch and then amended or rebased it, so the push is forced,
// but under a lease: the remote branch is overwritten only if it is absent
// or its tip is a commit that belongs to this run (see tipBelongsToRun). A
// tip pushed from anywhere else is left alone and Push fails. Only
// fugaro/ branches are ever pushed, so the base branch cannot be touched,
// and the suffix is validated as a real branch name so the refspec can't be
// used to reach some other ref.
func (r *Repo) Push(ctx context.Context, branch string) error {
	if err := checkRunBranch(ctx, r, branch); err != nil {
		return err
	}
	ref := "refs/heads/" + branch
	tip, err := r.remoteTip(ctx, ref)
	if err != nil {
		return fmt.Errorf("reading %s on origin: %w", branch, err)
	}
	if tip != "" {
		ours, err := r.tipBelongsToRun(ctx, tip)
		if err != nil {
			return fmt.Errorf("checking whether %s's tip on origin belongs to this run: %w", branch, err)
		}
		if !ours {
			return fmt.Errorf("%s on origin is at %s, a commit this run never had: %w, so it is not overwritten", branch, tip, ErrForeignTip)
		}
	}
	_, err = r.git(ctx, "push", "--quiet", "--force-with-lease="+ref+":"+tip, "origin", "HEAD:"+ref)
	return classified(err)
}

// PushExisting pushes HEAD to a branch that must still be on origin at
// expected, the commit the run started from, as a follow-up's is. When
// the remote branch is gone (its pull request was merged and the branch
// deleted) it refuses with ErrBranchGone rather than recreate it. When it
// is anywhere but expected, or HEAD itself (a retried push), someone else
// pushed to it, even a rewind to an older commit, and it refuses with
// ErrForeignTip. The lease is on the tip it checked, so a change between
// the check and the push fails the push too.
func (r *Repo) PushExisting(ctx context.Context, branch, expected string) error {
	if err := checkRunBranch(ctx, r, branch); err != nil {
		return err
	}
	ref := "refs/heads/" + branch
	tip, err := r.remoteTip(ctx, ref)
	if err != nil {
		return fmt.Errorf("reading %s on origin: %w", branch, err)
	}
	if tip == "" {
		return fmt.Errorf("%s no longer exists on origin; not recreating it: %w", branch, ErrBranchGone)
	}
	head, err := r.HeadSHA(ctx)
	if err != nil {
		return err
	}
	if expected == "" || (tip != expected && tip != head) {
		return fmt.Errorf("%s on origin is at %s, not %s where this run started: %w, so it is not overwritten", branch, tip, expected, ErrForeignTip)
	}
	if _, err = r.git(ctx, "push", "--quiet", "--force-with-lease="+ref+":"+tip, "origin", "HEAD:"+ref); err != nil {
		// A push that landed after the check fails the lease: that is
		// someone else's push, like one the check saw.
		now, rerr := r.remoteTip(ctx, ref)
		switch {
		case rerr == nil && now == "":
			return fmt.Errorf("%s was deleted from origin during the push; not recreating it: %w", branch, ErrBranchGone)
		case rerr == nil && now != tip:
			return fmt.Errorf("%s on origin moved to %s during the push: %w, so it is not overwritten (%v)", branch, now, ErrForeignTip, err)
		}
		return classified(err)
	}
	return nil
}

// checkRunBranch refuses a branch the runner must never push: anything
// but a valid fugaro/<id> branch, so the refspec can't reach another ref.
func checkRunBranch(ctx context.Context, r *Repo, branch string) error {
	if !strings.HasPrefix(branch, RunBranchPrefix) || len(branch) == len(RunBranchPrefix) {
		return fmt.Errorf("refusing to push %q: the runner only pushes %s<run-id> branches", branch, RunBranchPrefix)
	}
	if _, err := r.git(ctx, "check-ref-format", "--branch", branch); err != nil {
		return fmt.Errorf("refusing to push %q: not a valid branch name: %w", branch, err)
	}
	return nil
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

// tipBelongsToRun reports whether tip is a commit this run's checkout
// produced: either HEAD still descends from it (a plain amend or a rebase
// that keeps it as a parent), or it is in HEAD's own reflog (a rebase that
// drops it as a parent still records it there, since every commit and
// checkout in this checkout's history appends to HEAD's reflog). A commit
// that is merely present in the local object store — for instance because
// something else's push to the branch was later brought in with a plain
// `git fetch origin` — is neither, and must not be mistaken for this run's
// own tip.
func (r *Repo) tipBelongsToRun(ctx context.Context, tip string) (bool, error) {
	if _, err := r.git(ctx, "cat-file", "-e", tip+"^{commit}"); err != nil {
		return false, nil
	}
	if _, err := r.git(ctx, "merge-base", "--is-ancestor", tip, "HEAD"); err == nil {
		return true, nil
	}
	reflog, err := r.git(ctx, "reflog", "show", "--format=%H", "HEAD")
	if err != nil {
		return false, err
	}
	return slices.Contains(strings.Split(reflog, "\n"), tip), nil
}
