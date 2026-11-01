// Package gitops wraps the git CLI operations a run needs.
package gitops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/safetext"
)

// commitSHARE matches a full git object ID in hex: 40 characters (SHA-1)
// or 64 (SHA-256, git's newer, not yet default, object format) — never an
// abbreviation (git has no short-hash wire protocol: `git fetch origin
// <12-hex>` fails outright, "couldn't find remote ref") and never
// anything else: a rev accepted here reaches `git fetch origin <rev>` as
// a bare positional argument, and git itself accepts an option
// (--upload-pack=...) in that position, which a provider-supplied value
// (review mode's PRHead.SHA) must never be allowed to smuggle in.
var commitSHARE = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// ValidCommitSHA reports whether sha is a full, lower-hex git object ID —
// checked again here, right before the only two git calls (CheckoutDetached,
// Worktree) that ever take a caller-supplied rev, even though review mode
// (reviewmode.go, checkReviewPRs) already checks this once itself: this is
// the point nothing can reach `git fetch`/`git worktree add` without
// passing through, so a future caller that forgets review mode's own check
// is still covered.
func ValidCommitSHA(sha string) bool { return commitSHARE.MatchString(sha) }

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

// Identity is the author and committer of commits made during a run whose
// provider names no account of its own. It is Fugaro's own name and an
// address in the reserved .invalid domain: no account owns a commit made
// under it, which is why a provider that does own an account (a GitHub App)
// hands the run that account's identity instead -- see BotIdentity.
var Identity = map[string]string{
	"GIT_AUTHOR_NAME":     "Fugaro",
	"GIT_AUTHOR_EMAIL":    "fugaro@users.noreply.invalid",
	"GIT_COMMITTER_NAME":  "Fugaro",
	"GIT_COMMITTER_EMAIL": "fugaro@users.noreply.invalid",
}

// BotIdentity is the commit identity of a git host's bot account: the
// account's login as the name, and its noreply address as the email, which is
// the form the host links a commit by -- GitHub attributes a commit to a
// GitHub App's bot account when the author and committer are
// "<id>+<login>@users.noreply.github.com" (dependabot[bot] commits as
// "49699333+dependabot[bot]@users.noreply.github.com"), and that link is what
// makes CLA Assistant and blame recognise a run's commits.
//
// An empty id or login is no identity at all, and returns nil: a caller that
// could not read the account keeps Identity rather than half an identity that
// owns nothing.
func BotIdentity(id, login string) map[string]string {
	if id == "" || login == "" {
		return nil
	}
	email := id + "+" + login + "@users.noreply.github.com"
	return map[string]string{
		"GIT_AUTHOR_NAME":     login,
		"GIT_AUTHOR_EMAIL":    email,
		"GIT_COMMITTER_NAME":  login,
		"GIT_COMMITTER_EMAIL": email,
	}
}

// IdentityFor returns the identity a run's commits carry: identity when the
// run has one of its own (a provider's bot account), else the default
// Identity.
func IdentityFor(identity map[string]string) map[string]string {
	if identity == nil {
		return Identity
	}
	return identity
}

// IdentityEnv returns Identity as KEY=VALUE pairs.
func IdentityEnv() []string { return IdentityEnvFor(nil) }

// IdentityEnvFor returns IdentityFor(identity) as KEY=VALUE pairs: the
// environment every git call a run makes is made under.
func IdentityEnvFor(identity map[string]string) []string {
	identity = IdentityFor(identity)
	out := make([]string, 0, len(identity))
	for _, k := range slices.Sorted(maps.Keys(identity)) {
		out = append(out, k+"="+identity[k])
	}
	return out
}

// ModelCredentialVars are the variables the runner keeps out of every git
// call (Repo.StripEnv): a command an agent-written .git/config makes git
// run must never see the model credential.
var ModelCredentialVars = []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN"}

// BudgetTokenEnv is internal/runner's BudgetTokenEnv: the marker the launcher
// writes into a launch's environment, whose value is the launch's seal key
// once the launcher seals (docs/design/budget-token-channel.md §7.1). It is
// named here rather than imported because internal/runner imports this
// package, so the runner's constant is an alias of this one and the two
// cannot drift. See processEnv for why it is stripped.
const BudgetTokenEnv = "FUGARO_BUDGET_TOKEN"

// Repo is a git checkout. Env is added to the process environment for every git call.
type Repo struct {
	Dir string
	Env []string
	// CommitStamp, when set, is what a commit made through CommitAll or
	// CommitEmpty carries — its trailers and, when the launcher named one, its
	// author — and CommitHooks is the directory of the stamp hooks that read
	// it (stamp.go). The two are set together by the runner, whose own commits
	// are the run's and are stamped like the agent's (A3, D603).
	//
	// It is a function and not a Stamp because a run's rigor mode can rise
	// while the run is in flight (runner/rigor.go, roundRigor and
	// finalizeRigor): the Fugaro-Rigor of a commit is the mode the run had
	// reached when the commit was made (D601), and a value read once at
	// bootstrap would understate every commit made after a raise.
	//
	// An empty CommitHooks is the behaviour every other caller has had:
	// repository hooks off and nothing stamped. It is also what keeps the
	// stamp out of a run whose commits are not the run's — a review or an
	// artifact run (A3).
	CommitStamp func() Stamp
	CommitHooks string
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

// HooksDir returns the directory git resolves this checkout's hooks to:
// core.hooksPath as the repository itself configured it, and git's default of
// $GIT_DIR/hooks when it configured none. It is what a stamp hook directory
// chains to (A3, constraint 2), read once by the runner at bootstrap so that
// each of the pass-throughs it writes does not spawn a git of its own.
//
// It is one of the two runner-owned calls made without noHooks — the other is
// gitCommit, which names the stamp's own hook directory instead — because
// reading the configuration noHooks overrides is the whole point of it, and a
// repository's core.hooksPath is where its hooks are. rev-parse is plumbing
// and runs no hook.
// The answer is made absolute here: rev-parse prints it relative to the
// directory it was run in, and the hooks that carry it run in the root of the
// working tree, not in the runner's own working directory.
func (r *Repo) HooksDir(ctx context.Context) (string, error) {
	var stdout bytes.Buffer
	if err := r.gitRun(ctx, r.processEnv(), nil, nil, &stdout, "rev-parse", "--git-path", "hooks"); err != nil {
		return "", err
	}
	dir := strings.TrimSpace(stdout.String())
	if dir == "" || filepath.IsAbs(dir) {
		return dir, nil
	}
	return filepath.Join(r.Dir, dir), nil
}

// GitDir returns the git directory git names for this checkout, absolute:
// where the per-checkout state a caller writes to belongs. It is not always
// <Dir>/.git — in a linked worktree .git is a FILE naming a directory
// elsewhere (the main repository's .git/worktrees/<name>) — so a caller that
// writes inside it asks git rather than joining a path.
//
// One caveat, for the artifact runner's exclude write (inputs/ in
// <GitDir>/info/exclude, design artifact-runs.md §4 step 5): git reads that
// file from the COMMON directory (`git rev-parse --git-common-dir`), which
// in a worktree is not the directory returned here. The runner is unaffected
// because only review mode makes a worktree (runner/reviewmode.go) and an
// artifact run never runs in one; a future caller writing an exclude from
// inside a worktree needs the common dir instead.
func (r *Repo) GitDir(ctx context.Context) (string, error) {
	return r.git(ctx, "rev-parse", "--absolute-git-dir")
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

// CloneBlobless clones remote into dir as a blobless, single-branch partial
// clone of branch: git's `--filter=blob:none` downloads the commits and
// trees but no file contents, which a later checkout fetches for the files
// it actually needs. It is what a job with no derived image clones its
// repository with (design generic-tool.md §2, G4): nothing is baked into
// that job's image, so the start-up cost is this clone.
//
// The history stays complete — never a shallow `--depth 1`, which would
// break checkpoints, follow-ups and merge-base (design §2, G4) — and only
// branch's refs are fetched (`--single-branch`).
//
// It is --no-checkout, as design §2 spells the command, so the clone fetches
// refs alone and leaves no working tree: the checkout of the task's ref —
// CheckoutNewBranch for a first run, checkoutFollowUp for a follow-up, and
// for review mode, which has no branch of its own, a CheckoutRef of the base
// in the runner's bootstrap — is what fills the directory, and the blobs it
// needs are what git then fetches. Revisions nobody checked out — the base
// side of the branch's diff, the branch's own earlier commits, the base tip
// — are what the Prefetch* methods below bring in, for an agent whose git
// carries no credentials of its own.
//
// branch "" clones the remote's own default branch; the runner's bootstrap
// uses that, since the repository's git.base_branch is not readable until
// the checkout exists, and its FetchBase repairs origin/<base> right
// afterwards either way.
//
// dir is created by git, so it must not already be a repository; an empty
// directory is fine (what a container's /work/repo is before a clone job's
// first run clones into it), and its parent is created if absent.
// env and strip are OpenOrClone's: the credential variables, and the
// variables (the model credential) no git call may see.
func CloneBlobless(ctx context.Context, dir, remote, branch string, env []string, strip ...string) (*Repo, error) {
	if remote == "" {
		return nil, fmt.Errorf("cloning %s: no remote", dir)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return nil, err
	}
	args := []string{"clone", "--quiet", "--filter=blob:none", "--no-checkout", "--single-branch"}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	// The `--` keeps a remote that looks like an option (a path beginning
	// with "-") from being read as one.
	args = append(args, "--", remote, dir)
	parent := &Repo{Dir: filepath.Dir(dir), Env: env, StripEnv: strip}
	if _, err := parent.git(ctx, args...); err != nil {
		return nil, err
	}
	return &Repo{Dir: dir, Env: env, StripEnv: strip}, nil
}

// noHooks is prepended to every runner-owned git invocation but the commits
// made through gitCommit, which put the stamp's own hook directory in its
// place. The runner's git is plumbing: a repository hook (husky, lint-staged,
// a pre-push test run) must never block bootstrap's checkout, finalize's
// commit or push, and --no-verify only skips pre-commit and commit-msg, not
// prepare-commit-msg, post-checkout, post-commit or pre-push. The agent's own
// git calls are unaffected; they run the hooks as usual.
var noHooks = []string{"-c", "core.hooksPath=" + os.DevNull}

func (r *Repo) git(ctx context.Context, args ...string) (string, error) {
	out, err := r.gitRaw(ctx, args...)
	return strings.TrimSpace(string(out)), err
}

// Environ is the environment of every git call on r: the process's, then Env,
// without the variables StripEnv names — and, whatever that list says, without
// the provider keys and BudgetTokenEnv, which processEnv strips itself.
func (r *Repo) Environ() []string { return r.processEnv() }

// processEnv is the environment of every git call: the process's, then Env.
func (r *Repo) processEnv() []string {
	env := append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0"), r.Env...)
	// A provider key (FUGARO_PROVIDER_KEY_*) is the gateway's alone, and the
	// budget marker is the run's: no git call, and nothing git runs, ever sees
	// either, whatever StripEnv says. The marker is a secret as soon as the
	// launcher seals — its value is the launch's seal key, and whoever holds
	// it opens this run's token object (docs/design/budget-token-channel.md
	// §7.1) — and an agent-written .git/config (core.fsmonitor, a filter
	// driver) is exactly a way to make git run a command of the agent's
	// choosing, which is why the model credential is stripped the same way.
	// This strip is hygiene, not a boundary: the run's own agent shares the
	// container and its user with the runner and can read the marker from the
	// runner's /proc regardless, so what it removes is one more place the
	// value would surface — a hook's environment, a command's dump of its own
	// env — not the agent's own reach (SECURITY.md, "What one run's agent can
	// reach"; docs/design/budget-token-channel.md §7.1).
	return slices.DeleteFunc(env, func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return config.IsProviderKeyEnv(k) || k == BudgetTokenEnv || slices.Contains(r.StripEnv, k)
	})
}

// gitRaw runs git and returns its stdout exactly as written.
func (r *Repo) gitRaw(ctx context.Context, args ...string) ([]byte, error) {
	var stdout bytes.Buffer
	if err := r.gitTo(ctx, &stdout, args...); err != nil {
		return nil, err
	}
	return stdout.Bytes(), nil
}

// gitTo runs git with its stdout going to out: gitRaw, for a command whose
// output is either wanted exactly as written (a file's bytes) or not wanted
// at all (a diff run only for the lazy object fetches it performs —
// io.Discard, since that output may be far too large to hold in memory).
func (r *Repo) gitTo(ctx context.Context, out io.Writer, args ...string) error {
	return r.gitIO(ctx, nil, out, args...)
}

// gitIn is git for a command fed its input on stdin (`--stdin`,
// `--batch-check`): one process for a list whatever its length, and a list
// that never reaches the command line, where ARG_MAX would cap it.
func (r *Repo) gitIn(ctx context.Context, in string, args ...string) (string, error) {
	var stdout bytes.Buffer
	err := r.gitIO(ctx, strings.NewReader(in), &stdout, args...)
	return strings.TrimSpace(stdout.String()), err
}

func (r *Repo) gitIO(ctx context.Context, in io.Reader, out io.Writer, args ...string) error {
	return r.gitRun(ctx, r.processEnv(), noHooks, in, out, args...)
}

// gitCommit is the git of a commit made through r: the stamp's hook directory
// in place of noHooks, and the stamp's own environment in place of the
// process's (StampEnv). The command-line configuration outranks the
// repository's own and the stamp's environment configuration alike
// (git-config(1)), which is why the directory travels in both and is the same
// one: the environment entries are what the hook reads its values from, and
// the -c is what makes that hook the one git runs.
//
// The stamp is read here, at the commit, and not when the caller set the
// field: it is what the commit carries now.
//
// A Repo with no CommitHooks commits as it always did — repository hooks off,
// nothing stamped.
func (r *Repo) gitCommit(ctx context.Context, args ...string) (string, error) {
	env, prefix := r.processEnv(), noHooks
	if r.CommitHooks != "" {
		var stamp Stamp
		if r.CommitStamp != nil {
			stamp = r.CommitStamp()
		}
		prefix = []string{"-c", "core.hooksPath=" + r.CommitHooks}
		env = StampEnv(stamp, r.CommitHooks, env)
	}
	var stdout bytes.Buffer
	// The commit is made against the branch's own HEAD, not against one a ref
	// the agent planted under refs/replace substitutes: every read this package
	// makes is replacement-blind for that reason, and the commit is a read too —
	// git decides from it what the commit carries, and whether there is anything
	// to commit at all.
	args = append([]string{"--no-replace-objects"}, args...)
	err := r.gitRun(ctx, env, prefix, nil, &stdout, args...)
	return strings.TrimSpace(stdout.String()), err
}

// gitRun is gitIO with the environment and the leading -c arguments named by
// the caller: gitIO's own are the process environment and noHooks.
func (r *Repo) gitRun(ctx context.Context, env, prefix []string, in io.Reader, out io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", append(slices.Clone(prefix), args...)...)
	cmd.Dir = r.Dir
	cmd.Env = env
	cmd.WaitDelay = gitWaitDelay
	cmd.Stdin = in
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = out, &stderr
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
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
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

// CheckoutRef checks out rev — a ref already in the repository, an
// origin/<branch> the caller fetched a moment before — with a detached HEAD
// and removes untracked files (ignored ones are kept, the same guarantee
// CheckoutNewBranch gives). Nothing is fetched: the point is a working tree
// over a ref that is already here, for the job that has none (a clone job's
// clone is --no-checkout, CloneBlobless) and no branch of its own to check
// out — review mode's base branch, which is what its configuration and its
// mise config are read from (runner.bootstrap). It detaches, never moving a
// local branch, because review mode's branch is a lock key, not a ref
// anything may push.
func (r *Repo) CheckoutRef(ctx context.Context, rev string) error {
	if _, err := r.git(ctx, "checkout", "--quiet", "-f", "--detach", rev); err != nil {
		return err
	}
	_, err := r.git(ctx, "clean", "-fd", "--quiet")
	return err
}

// The three Prefetch* methods below bring in file contents a reader of a
// clone job's checkout will ask for, so that whoever runs those commands
// next does not have to fetch them. They exist because a blobless clone
// (CloneBlobless) holds the file contents of one revision — the one a
// checkout brought in — and no others.
//
// Git fetches a missing blob lazily through the clone's promisor remote,
// which is a network call to origin, so it works only for a caller whose
// git carries credentials: the runner's does (Repo.Env), and the agent's
// deliberately does not (agent.env) in the two modes whose agent reads
// someone else's work — a follow-up and a review run. On a private
// repository an unfetched read there is `fatal: could not fetch <oid> from
// promisor remote` (review N1). The runner runs these instead, once, before
// the agent starts.
//
// Each is run with stdout discarded: what is wanted is not the output but
// the lazy fetches git performs while producing it. A diff exits 0 whether
// or not there are differences, so what its status reports is only whether
// the diff could be produced at all — which is exactly the failure these
// exist to prevent, and what an error from one of them means.
//
// What they do not cover: `git blame`, and `git show <rev>:<path>` for a
// revision no one fetched — the file contents of revisions outside these
// three sets. docs/checkout.md says so.

// PrefetchDiff brings in the file contents `git diff rev...head` needs: the
// base's side, at the merge base, of every file the branch changed. This is
// the command the review stage's prompt names, and the base side of it is
// what no checkout of the branch ever brings in.
func (r *Repo) PrefetchDiff(ctx context.Context, rev, head string) error {
	return r.prefetchDiff(ctx, rev+"..."+head)
}

// PrefetchCommits brings in the file contents the branch's own commits need:
// `git log -p rev..head`, `git show <a commit of the branch>` and
// `git diff HEAD~1` (review M2). A diff of the two endpoints does not touch
// them: the revisions in between, and so their versions of the files they
// changed, are missing until something reads them, which is what a reviewer
// reading a pull request commit by commit does.
func (r *Repo) PrefetchCommits(ctx context.Context, rev, head string) error {
	// --format= empties the commit header, so only the patches are produced.
	return r.gitTo(ctx, io.Discard, "log", "-p", "--no-color", "--no-ext-diff", "--no-textconv", "--format=", rev+".."+head, "--")
}

// PrefetchBaseDiff brings in `git diff rev head`'s two sides: the diff
// against where the base branch is now, rather than against the merge base
// (review M2). Its base side is missing whenever the base has moved on
// since the merge base, which is a two-dot diff a reviewer runs by habit.
func (r *Repo) PrefetchBaseDiff(ctx context.Context, rev, head string) error {
	return r.prefetchDiff(ctx, rev, head)
}

// prefetchDiff runs `git diff` over args for its fetches. --no-ext-diff and
// --no-textconv: the repository's own .gitattributes or config must not make
// this run a program, and neither is needed to bring the objects in.
func (r *Repo) prefetchDiff(ctx context.Context, args ...string) error {
	argv := []string{"diff", "--no-color", "--no-ext-diff", "--no-textconv"}
	argv = append(argv, args...)
	argv = append(argv, "--")
	return r.gitTo(ctx, io.Discard, argv...)
}

// lfsFiltersOff are -c overrides that keep git from running any
// externally-configured smudge/process/clean filter — in practice,
// git-lfs, wherever a prior `git lfs install` left it configured, locally
// or globally — while fetching or checking out a reviewed pull request's
// head (reviewmode.go, design generic-tool §3.6, G12). Review mode never
// builds or tests the checkout, so there is no reason to run whatever
// external command a fork's own .gitattributes points a filter at before
// stripClaudeConfig has had a chance to run, or at all.
var lfsFiltersOff = []string{"-c", "filter.lfs.process=", "-c", "filter.lfs.smudge=", "-c", "filter.lfs.clean=", "-c", "filter.lfs.required=false"}

// withLFSFiltersOff returns args with lfsFiltersOff's -c overrides first,
// as a fresh slice each call (never aliasing lfsFiltersOff's own backing
// array).
func withLFSFiltersOff(args ...string) []string {
	return append(append([]string{}, lfsFiltersOff...), args...)
}

// CheckoutDetached fetches rev (a commit, full, never abbreviated — see
// ValidCommitSHA) from origin and checks it out with a detached HEAD,
// review mode's single-pull-request case (design generic-tool §3.6, G12):
// the commit is never a branch of its own, and gitprov.Provider.FetchPRHead
// already named the exact one to read, so this never resolves a ref of its
// own. Untracked files are removed but ignored ones (warm build caches)
// are kept, the same as CheckoutNewBranch. Filters (lfsFiltersOff) are off
// for the fetch and the checkout, the two steps that could run one.
func (r *Repo) CheckoutDetached(ctx context.Context, rev string) error {
	if !ValidCommitSHA(rev) {
		return fmt.Errorf("checking out %s: not a full commit hash", safetext.OneLine(rev))
	}
	if _, err := r.git(ctx, withLFSFiltersOff("fetch", "--quiet", "origin", rev)...); err != nil {
		return err
	}
	if _, err := r.git(ctx, withLFSFiltersOff("checkout", "--quiet", "-f", "--detach", "FETCH_HEAD")...); err != nil {
		return err
	}
	_, err := r.git(ctx, "clean", "-fd", "--quiet")
	return err
}

// Worktree fetches rev from origin and adds it as a detached worktree at
// dir: review mode's pick case (design generic-tool §3.6, G12), one
// directory per pull request it compares, so the reviewer can read every
// attempt at once. dir's parent must already exist. Filters (lfsFiltersOff)
// are off for the fetch and the worktree add, the two steps that could run
// one.
//
// dir is removed first, and any worktree administration left over for it
// pruned, so a dir left behind by an earlier run on the same warm
// container or retried execution (reviewDir's own doc: pick's checkouts
// are not part of the image or clone that gets reset between runs the way
// r.d.WorkDir itself is) does not make `worktree add` fail outright — the
// same reuse `CheckoutDetached`'s own force checkout and clean already
// handle for the single-pull-request case, just reached a different way
// here since a worktree's target directory, unlike a checked-out branch,
// cannot simply be overwritten in place.
func (r *Repo) Worktree(ctx context.Context, rev, dir string) error {
	if !ValidCommitSHA(rev) {
		return fmt.Errorf("adding a worktree at %s: not a full commit hash", safetext.OneLine(rev))
	}
	if _, err := r.git(ctx, withLFSFiltersOff("fetch", "--quiet", "origin", rev)...); err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("removing %s before adding it as a worktree: %w", dir, err)
	}
	if _, err := r.git(ctx, "worktree", "prune"); err != nil {
		return err
	}
	_, err := r.git(ctx, withLFSFiltersOff("worktree", "add", "--quiet", "--detach", dir, "FETCH_HEAD")...)
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

// IsClean reports whether the working tree has no changes and no untracked
// files. Replacement refs are ignored: `git status` compares the index against
// the tree of HEAD's commit, so a refs/replace/<sha> the agent plants in the
// checkout it writes makes a clean tree read as changed (and the other way
// round), and the callers that decide what to commit have to see the tree this
// checkout actually has.
func (r *Repo) IsClean(ctx context.Context) (bool, error) {
	out, err := r.git(ctx, "--no-replace-objects", "status", "--porcelain")
	return out == "", err
}

// CommitAll commits every change, skipping repository hooks, under the stamp
// CommitHooks and CommitStamp name (gitCommit). It reports whether there was
// anything to commit.
func (r *Repo) CommitAll(ctx context.Context, msg string) (bool, error) {
	if _, err := r.git(ctx, "add", "-A"); err != nil {
		return false, err
	}
	if clean, err := r.IsClean(ctx); err != nil || clean {
		return false, err
	}
	_, err := r.gitCommit(ctx, "commit", "--quiet", "--no-verify", "-m", msg)
	return err == nil, err
}

// NewPaths lists the paths the checkout holds that no commit has and that a
// commit would carry: the untracked ones git would add (`git ls-files --others
// --exclude-standard`, ignored ones aside) and the ones the index holds that HEAD
// does not (`git diff --name-only --no-renames --diff-filter=A HEAD`, the working
// tree against HEAD, which sees a file added with `git add -N` as well). Both read
// NUL-separated, so a path with a newline in it survives.
//
// The second listing is not an afterthought: a file the agent `git add`-ed and
// never committed is new all the same, is absent from `--others` because it is
// in the index, and is exactly what a commit would carry first. A path in
// neither listing is one the base commit already holds — the run's own, however
// much it changed — or one git ignores, which `git add -A` skips the same way,
// so a path this call does not return cannot reach the commit either.
func (r *Repo) NewPaths(ctx context.Context) ([]string, error) {
	untracked, err := r.gitRaw(ctx, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	// Read with replacement refs ignored, as IsClean and CommitAllExcept are:
	// what is staged is the index this checkout has, not the one a ref the
	// agent planted makes HEAD's tree claim.
	// No --cached: an intent-to-add entry (`git add -N`) is hidden from
	// `diff --cached` and is in the index, so neither listing would name it, and
	// --no-renames keeps a new file that merely resembles a deleted tracked one
	// an addition rather than a rename the filter drops.
	staged, err := r.gitRaw(ctx, "--no-replace-objects", "diff", "--name-only", "--no-renames", "--diff-filter=A", "-z", "HEAD")
	if err != nil {
		return nil, err
	}
	return append(nulPaths(untracked), nulPaths(staged)...), nil
}

// nulPaths splits one NUL-separated git listing into paths.
func nulPaths(out []byte) []string {
	var paths []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// CommitAllExcept is CommitAll with the named paths held back: everything is
// staged as usual — including them, since that is the only way to see whether
// anything is left to commit — and they are then taken out of the index
// again, so the commit never carries them and they stay untracked in the tree
// where whoever reads the run can find them.
//
// The paths are literals (--literal-pathspecs): a file's own name is data, and
// one beginning with a colon or holding a wildcard would otherwise be read as
// a pathspec's magic (the review diff passes its paths the same way,
// reviewinput.go). They must be relative to the repository root, as NewPaths
// returns them, and the commit is stamped like CommitAll's.
func (r *Repo) CommitAllExcept(ctx context.Context, msg string, except []string) (bool, error) {
	if len(except) == 0 {
		return r.CommitAll(ctx, msg)
	}
	if _, err := r.git(ctx, "add", "-A"); err != nil {
		return false, err
	}
	// The reset is replacement-blind too: `git reset -- <path>` takes the entry
	// it writes back into the index from HEAD's tree, so with a planted ref in
	// place it would put the very path being held back into the index again.
	unstaged := append([]string{"--no-replace-objects", "--literal-pathspecs", "reset", "-q", "--"}, except...)
	if _, err := r.git(ctx, unstaged...); err != nil {
		return false, err
	}
	// Read with replacement refs ignored, as IsClean is: what is committed is
	// the index this checkout has, not the one a ref the agent planted makes
	// HEAD's tree claim. The listing is NUL-separated like NewPaths': the
	// question here is only whether it is empty, and a path with a newline in
	// it must not decide that answer.
	staged, err := r.gitRaw(ctx, "--no-replace-objects", "diff", "--cached", "--name-only", "-z")
	if err != nil {
		return false, err
	}
	if len(nulPaths(staged)) == 0 {
		return false, nil
	}
	_, err = r.gitCommit(ctx, "commit", "--quiet", "--no-verify", "-m", msg)
	return err == nil, err
}

// CommitEmpty makes a commit with no changes, so a PR can exist for a branch
// where the agent committed nothing. It is stamped like CommitAll's.
func (r *Repo) CommitEmpty(ctx context.Context, msg string) error {
	_, err := r.gitCommit(ctx, "commit", "--quiet", "--no-verify", "--allow-empty", "-m", msg)
	return err
}

// ResetTo resets HEAD and the working tree to ref (a commit, never a
// symbolic ref the command being discarded could itself have moved —
// gate.go passes the commit it read before running commands.fix, not
// "HEAD", which a fix that committed on its own would have moved out from
// under a literal HEAD reset), discarding every change to a tracked file —
// including any commit the discarded command made on top of ref — then
// removes untracked files and directories (git clean -fd, never -x):
// anything git already ignores — a warm dependency cache, a restored
// toolchain — survives, the same guarantee CheckoutNewBranch's own clean
// -fd gives a fresh checkout. Used when a command's output, or its own
// history, cannot be trusted (gate.go: commands.fix timed out, failed, or
// committed on its own).
//
// The reset runs with --no-replace-objects: the agent writes the checkout's
// own .git, so a refs/replace/<sha> it plants for ref would otherwise decide
// the tree this puts the working tree at — the caller resets to a commit the
// runner made itself and means that commit's own tree. The clean that follows
// reads no object and needs none.
func (r *Repo) ResetTo(ctx context.Context, ref string) error {
	if _, err := r.git(ctx, "--no-replace-objects", "reset", "--quiet", "--hard", ref); err != nil {
		return err
	}
	_, err := r.git(ctx, "clean", "-fd", "--quiet")
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

// CommitsAhead counts the commits HEAD has that since does not and, when
// except holds anything, that none of except does either: the commits of a
// run's own work, since being the commit the run was cut from and except
// the bases the run works from, whose own commits — a base the agent merged
// or rebased into the branch — are theirs and not the run's. There is more
// than one of those (the run's base of work and the branch its
// configuration came from), and each is the only one that leaves out its
// own commits: measuring against one of them alone counts the other's,
// and measuring against the configuration's base alone counts every commit
// of the ref a run was started from (--ref console with base_branch main),
// so "the agent made no commits" could never fire there.
//
// Each is a commit or a plain ref name, never a moving ref such as HEAD:
// the agent controls the checkout while a stage runs (checkpoint.go's C6a).
// An except the checkout cannot resolve (a base commit the agent never
// fetched) is left out on its own (readableBounds), and the others still
// subtract theirs, which counts more rather than failing. Replace refs are
// the agent's too, so they are not honoured.
func (r *Repo) CommitsAhead(ctx context.Context, since string, except ...string) (int, error) {
	except = r.readableBounds(ctx, except)
	revs := []string{"--no-replace-objects", "rev-list", "--count", "--end-of-options", since + "..HEAD"}
	for _, e := range except {
		revs = append(revs, "^"+e)
	}
	out, err := r.git(ctx, revs...)
	if err != nil {
		if len(except) > 0 {
			return r.CommitsAhead(ctx, since)
		}
		return 0, err
	}
	return strconv.Atoi(out)
}

// OriginHeads returns the commits origin's branches point at now, read
// from origin itself (`ls-remote --heads`), other than runBranch — the
// run's own branch, which the run's pushes fill with its own commits — and
// only those this checkout already holds. It is never the checkout's
// remote-tracking refs: the agent's own git can create or repoint those
// without pushing anything (`git update-ref refs/remotes/origin/x <its
// commit>`), and "a branch on origin holds it" must mean a real branch on
// origin. A head the checkout lacks is left out, and it is checked with
// lazy fetches off: in a blobless partial clone a plain check would fetch
// every missing branch's commit from the promisor remote. GIT_NO_LAZY_FETCH
// is git 2.44's (NoLazyFetch): an older git ignores it, which is why the
// runner asks GitVersion before it narrows anything.
//
// Two processes whatever the branch count: the presence of every head is
// one `cat-file --batch-check`, which answers each `<sha>^{commit}` line in
// order, "<sha> commit <size>" when the checkout holds the commit and
// "<line> missing" when it does not.
func (r *Repo) OriginHeads(ctx context.Context, runBranch string) ([]string, error) {
	if runBranch == "" {
		return nil, errors.New("refusing to read origin's branches without the run's own branch to leave out")
	}
	out, err := r.git(ctx, "ls-remote", "--heads", "origin")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var candidates []string
	var in strings.Builder
	for _, line := range strings.Split(out, "\n") {
		sha, name, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok || name == "refs/heads/"+runBranch || !IsFullSHA(sha) || seen[sha] {
			continue
		}
		seen[sha] = true
		candidates = append(candidates, sha)
		in.WriteString(sha + "^{commit}\n")
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	local := *r
	local.Env = append(slices.Clone(r.Env), "GIT_NO_LAZY_FETCH=1")
	res, err := local.gitIn(ctx, in.String(), "cat-file", "--batch-check")
	if err != nil {
		return nil, err
	}
	answers := strings.Split(res, "\n")
	if len(answers) != len(candidates) {
		return nil, fmt.Errorf("reading origin's branches: cat-file answered %d of %d heads", len(answers), len(candidates))
	}
	var heads []string
	for i, a := range answers {
		if f := strings.Fields(a); len(f) == 3 && f[1] == "commit" {
			heads = append(heads, candidates[i])
		}
	}
	return heads, nil
}

// HeldBy reports whether any of heads holds commit (is it or has it as an
// ancestor), in one process whatever their number: `rev-list --count
// <commit>` with every head subtracted on stdin counts 0 exactly when one of
// them reaches it. The heads go in as "^<sha>" lines rather than after a
// command-line --not, which git does not apply to what --stdin reads. commit
// must be a full commit SHA, and heads are OriginHeads', which the checkout
// holds. It is resolved as "<commit>^{commit}": rev-list walks no commit from
// a tree or a blob and counts 0, which would read as held, so a full SHA that
// names anything but a commit here is an error instead.
func (r *Repo) HeldBy(ctx context.Context, commit string, heads []string) (bool, error) {
	if !IsFullSHA(commit) {
		return false, fmt.Errorf("refusing to look for %q on origin's branches: not a full commit SHA", commit)
	}
	if len(heads) == 0 {
		return false, nil
	}
	out, err := r.gitIn(ctx, notLines(heads), "--no-replace-objects", "rev-list", "--count", "--stdin", "--end-of-options", commit+"^{commit}")
	if err != nil {
		return false, err
	}
	n, err := strconv.Atoi(out)
	return err == nil && n == 0, err
}

// notLines is heads as `rev-list --stdin` subtracts them, one "^<sha>" line
// each. Every head is a full SHA (OriginHeads), so no line is read as an
// option.
func notLines(heads []string) string {
	var b strings.Builder
	for _, h := range heads {
		b.WriteString("^" + h + "\n")
	}
	return b.String()
}

// OwnCommits lists, oldest first (parents before children), the commits
// HEAD has that no branch on origin holds (OriginHeads) other than
// runBranch. It answers "which commits did this run make" without knowing
// which branch the work was cut from: an agent told to `git fetch origin
// <integration> && git checkout -b <b> FETCH_HEAD` starts from a branch
// nothing recorded and, in a single-branch clone, no remote-tracking ref
// names. A branch on origin whose tip the checkout never fetched subtracts
// nothing, which only ever makes the list longer. The heads are read on
// stdin, so a repository with tens of thousands of branches stays one
// process under ARG_MAX.
func (r *Repo) OwnCommits(ctx context.Context, runBranch string) ([]string, error) {
	heads, err := r.OriginHeads(ctx, runBranch)
	if err != nil {
		return nil, err
	}
	out, err := r.gitIn(ctx, notLines(heads), "--no-replace-objects", "rev-list", "--topo-order", "--reverse", "--stdin", "--end-of-options", "HEAD")
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// GitVersion is `git --version` as git prints it ("git version 2.47.3").
func (r *Repo) GitVersion(ctx context.Context) (string, error) {
	return r.git(ctx, "--version")
}

// NoLazyFetch reports whether the git that printed version (GitVersion)
// honours GIT_NO_LAZY_FETCH, which git 2.44 added: an older one ignores the
// variable, and OriginHeads' presence check would then fetch every missing
// branch's commit in a blobless clone. Anything unparseable is a "no".
func NoLazyFetch(version string) bool {
	rest, ok := strings.CutPrefix(version, "git version ")
	if !ok {
		return false
	}
	words := strings.Fields(rest)
	if len(words) == 0 {
		return false
	}
	f := strings.SplitN(words[0], ".", 3)
	if len(f) < 2 {
		return false
	}
	major, err1 := strconv.Atoi(f[0])
	minor, err2 := strconv.Atoi(f[1])
	if err1 != nil || err2 != nil {
		return false
	}
	return major > 2 || major == 2 && minor >= 44
}

// FirstParent returns sha's first parent, "" for a root commit. sha must be
// a full commit SHA, so it is never read as an option or a moving ref.
func (r *Repo) FirstParent(ctx context.Context, sha string) (string, error) {
	if !IsFullSHA(sha) {
		return "", fmt.Errorf("refusing to read the parent of %q: not a full commit SHA", sha)
	}
	out, err := r.git(ctx, "--no-replace-objects", "rev-list", "--parents", "--max-count=1", "--end-of-options", sha)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(out)
	if len(fields) < 2 {
		return "", nil
	}
	return fields[1], nil
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

// TrackedPaths returns the paths the index holds under dir, relative to the
// repository root (`git ls-files -- dir`), so a caller can ask "did the
// repository put anything here?" — a question a walk of the working tree
// cannot answer, because a file in that directory may be untracked or ignored
// and so not the repository's at all. dir must be relative and clean; a
// directory the index has nothing under returns no paths and no error.
func (r *Repo) TrackedPaths(ctx context.Context, dir string) ([]string, error) {
	if !cleanRelPath(dir) {
		return nil, fmt.Errorf("refusing to list %q: the path must be relative and clean, without ..", dir)
	}
	out, err := r.gitRaw(ctx, "ls-files", "-z", "--", dir)
	if err != nil {
		return nil, err
	}
	return nulPaths(out), nil
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
