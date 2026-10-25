package cli

import (
	"context"
	"fmt"
	"io"
	"regexp"

	"github.com/dimipaun/fugaro/internal/config"
)

// newRunRef is the ref a new run is cut from when --ref was not given: the
// branch the checkout in the working directory is on, when that is a branch
// origin has and not the repository's base branch, else the base branch
// (the local config's, then the checkout's fugaro.yaml's).
//
// The checkout's branch wins because the run's review measures the run's
// work against the ref it was cut from (internal/runner/diffbase.go): a run
// launched from main whose task then checks out an integration branch
// inside the job reviews that branch's whole divergence from main every
// round — measured at 700-1500 review calls over ~33,000 changed lines.
// Someone launching from a checkout of the integration branch almost always
// means that branch, so it is the default; it is never silent (stderr names
// it and how to get the old default), and an explicit --ref always wins.
//
// "Origin has" is read from this checkout's own remote-tracking ref, never
// the network: a branch that was never pushed (or whose remote-tracking ref
// was pruned) is not used, since the job could not fetch it.
func newRunRef(ctx context.Context, env *cloudEnv, repo string, checkout func() *config.Config, warn io.Writer) (string, error) {
	var base string
	if r, ok := env.localRepo(repo); ok && r.BaseBranch != "" {
		base = r.BaseBranch
	} else if c := checkout(); c != nil && c.Git.BaseBranch != "" {
		base = c.Git.BaseBranch
	}
	if b := checkoutBranchOnOrigin(ctx, repo); b != "" && b != base {
		fmt.Fprintf(warn, "using --ref %s: this checkout is on %s; pass --ref %s to run from the default branch\n", b, b, orDefault(base))
		return b, nil
	}
	if base == "" {
		return "", userErr("cannot tell the base branch of %s; pass --ref", repo)
	}
	return base, nil
}

// orDefault is base for newRunRef's hint, or a placeholder when no base
// branch could be read (the hint still has to name a flag value).
func orDefault(base string) string {
	if base == "" {
		return "<base-branch>"
	}
	return base
}

// checkoutBranchOnOrigin is the branch the checkout in the working directory
// has checked out, when that checkout is of repo, the branch name is one a
// task may carry, and the checkout holds refs/remotes/origin/<branch>; ""
// otherwise (a detached HEAD, another repository, an unpushed branch).
func checkoutBranchOnOrigin(ctx context.Context, repo string) string {
	root := checkoutRoot(ctx, repo)
	if root == "" {
		return ""
	}
	b, err := gitRead(ctx, root, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || !config.ValidBranchName(b) {
		return ""
	}
	if err := gitCmd(ctx, root, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+b+"^{commit}").Run(); err != nil {
		return ""
	}
	return b
}

// taskCutsBranch matches a task that tells the agent to cut its own branch
// from a remote branch inside the run (`git checkout -b work origin/X`,
// `git switch -c work origin/X`): the shape of the briefs that made runs
// launched from main review a whole integration branch. Only this shape is
// matched: a task that merely mentions origin/X (a diff to read, a branch
// to compare with) says nothing about where the work starts.
var taskCutsBranch = regexp.MustCompile(`\b(?:checkout\s+-[bB]|switch\s+(?:-[cC]|--create|--force-create))\s+\S+\s+origin/([A-Za-z0-9][A-Za-z0-9._/-]*)`)

// warnTaskCutsBranch warns, and only warns, when the task text cuts its
// branch from origin/X and X is not the ref the run is cut from: the run's
// review would measure against ref and read X's whole divergence from it.
// The launch is not changed, since a task can say this for reasons a
// pattern cannot see (a comparison it builds, a sibling branch it reads).
func warnTaskCutsBranch(warn io.Writer, text, ref string) {
	for _, m := range taskCutsBranch.FindAllStringSubmatch(text, -1) {
		b := m[1]
		if b == ref || !config.ValidBranchName(b) {
			continue
		}
		fmt.Fprintf(warn, "warning: the task checks out a branch from origin/%s, but the run is cut from %s and its review measures against %s; if the work starts on %s, launch with --ref %s\n", b, ref, ref, b, b)
		return
	}
}
