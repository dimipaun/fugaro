package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/task"
)

// The checkout a first run or the repository stage works on: its default
// branch's fugaro.yaml, read from git with no cloud call, and whether the
// GitHub App's ID is still needed for it.

// appIDFlag is how the missing GitHub App ID is named to the user.
const appIDFlag = "--github-app-id <id> (the GitHub App's numeric ID, not a secret; recorded in the local config)"

var appIDRE = regexp.MustCompile(`^[0-9]{1,20}$`)

// repoTarget is the checkout the repository stage onboards.
type repoTarget struct {
	root, repo string
	branch     string // the default branch's name, as the ref resolved (origin/main: main)
	cfg        *config.Config
}

// needsAppID reports whether the engine will need the GitHub App's ID and
// nothing yet says it: a GitHub repository, no --github-app-id, and no
// local config entry for the repository (lc may be nil: no config yet).
func (t *repoTarget) needsAppID(lc *localcfg.Config, o *initOptions) bool {
	if t.cfg.Git.Provider != "github" || o.githubAppID != "" {
		return false
	}
	return lc == nil || storedAppID(lc, t.repo) == ""
}

// storedAppID is the App ID the local config holds for repo.
func storedAppID(lc *localcfg.Config, repo string) string {
	want, err := task.CanonicalRepo(repo)
	if err != nil {
		return ""
	}
	for name, r := range lc.Repos {
		if c, err := task.CanonicalRepo(name); err == nil && c == want {
			return r.GitHubAppID
		}
	}
	return ""
}

// anyStoredAppID is the one App ID the local config holds for its
// repositories, to suggest for another (a team has one App), or "".
func anyStoredAppID(lc *localcfg.Config) string {
	id := ""
	for _, r := range lc.Repos {
		switch {
		case r.GitHubAppID == "":
		case id == "":
			id = r.GitHubAppID
		case id != r.GitHubAppID:
			return ""
		}
	}
	return id
}

func gitRead(ctx context.Context, dir string, args ...string) (string, error) {
	c := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	c.WaitDelay = 5 * time.Second
	out, err := c.Output()
	return strings.TrimSpace(string(out)), err
}

// defaultBranchRef is the ref of the checkout's default branch: where origin
// says HEAD is, else origin/main or origin/master, else a local main or
// master. It reads no network.
func defaultBranchRef(ctx context.Context, root string) string {
	var cands []string
	if ref, err := gitRead(ctx, root, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil && ref != "" {
		cands = append(cands, ref)
	}
	cands = append(cands, "origin/main", "origin/master", "main", "master")
	for _, c := range cands {
		if _, err := gitRead(ctx, root, "rev-parse", "--verify", "--quiet", c+"^{commit}"); err == nil {
			return c
		}
	}
	return ""
}

// resolveRepoTarget finds the checkout (the working directory's) and its
// default branch's fugaro.yaml, with no cloud call. It returns the target
// when the stage applies, else the status that says why (skipped, or
// needs-you when the user has something to fix); project "" does not check
// the file's project.
func resolveRepoTarget(ctx context.Context, project string) (*repoTarget, initflow.Status) {
	skip := func(why string) (*repoTarget, initflow.Status) {
		return nil, initflow.Status{State: initflow.Skipped, Detail: why}
	}
	root, err := gitRead(ctx, ".", "rev-parse", "--show-toplevel")
	if err != nil {
		return skip("not in a checkout: run fugaro init in a checkout of the repository to onboard it")
	}
	repo, err := checkoutRepo(ctx, root)
	if err != nil {
		return skip("this checkout has no origin repository to onboard")
	}
	ref := defaultBranchRef(ctx, root)
	if ref == "" {
		return skip("cannot tell this checkout's default branch (no origin/HEAD, main or master): run git remote set-head origin --auto, then rerun fugaro init")
	}
	branch := strings.TrimPrefix(ref, "origin/")
	c := exec.CommandContext(ctx, "git", "-C", root, "cat-file", "blob", ref+":fugaro.yaml")
	c.WaitDelay = 5 * time.Second
	data, err := c.Output()
	if err != nil {
		return skip("no fugaro.yaml on the default branch (" + branch + "): /fugaro:setup writes it; merge its pull request, then rerun fugaro init")
	}
	cfg, problems := config.Parse(data)
	if cfg == nil {
		why := "unreadable"
		if len(problems) > 0 {
			why = problems[0].String()
		}
		lf := initflow.Left{Stage: initflow.Repository, Kind: initflow.LeftCommand, Text: "fugaro validate"}
		return nil, initflow.Status{State: initflow.NeedsYou, Detail: "fugaro.yaml on the default branch (" + branch + ") is not valid: " + oneLineCLI(why), Left: &lf}
	}
	if project != "" && cfg.Project != project {
		return skip("fugaro.yaml on the default branch (" + branch + ") names another project (or none): " + project + " is not its project")
	}
	// The engine reads the working tree: it must be the default branch's file.
	if wt, err := os.ReadFile(filepath.Join(root, "fugaro.yaml")); err != nil || !bytes.Equal(wt, data) {
		lf := initflow.Left{Stage: initflow.Repository, Kind: initflow.LeftCommand, Text: "git switch " + branch + " && git pull && " + selfCommand() + " init"}
		return nil, initflow.Status{State: initflow.NeedsYou, Detail: "this checkout's fugaro.yaml is not the default branch's (" + ref + "): the repository is onboarded from the default branch, so switch to it and update it", Left: &lf}
	}
	return &repoTarget{root: root, repo: repo, branch: branch, cfg: cfg}, initflow.Status{}
}

// oneLineCLI keeps s to its first line.
func oneLineCLI(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}
