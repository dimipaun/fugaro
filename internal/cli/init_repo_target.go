package cli

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/image"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/shellword"
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
	root   string
	origin originInfo
	repo   string
	branch string // the default branch's name, as the ref resolved (origin/main: main)
	cfg    *config.Config
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

// gitHardening is put before every git call on a checkout that may be
// somebody else's: no hook runs, no fsmonitor command, no prompt, no
// optional lock; the user's and the system's git config are still read, but
// nothing here runs what the repository's own config names.
var gitHardening = []string{"-c", "core.hooksPath=" + os.DevNull, "-c", "core.fsmonitor=false", "-c", "protocol.ext.allow=never"}

func gitCmd(ctx context.Context, dir string, args ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, "git", append(append([]string{"-C", dir}, gitHardening...), args...)...)
	c.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	c.WaitDelay = 5 * time.Second
	return c
}

func gitRead(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := gitCmd(ctx, dir, args...).Output()
	return strings.TrimSpace(string(out)), err
}

// quoteWord is s as one shell word (the helper every printed command uses):
// a leading-dash value is quoted, and printed after "--" where the command
// takes options.
func quoteWord(s string) string { return shellword.Quote(s) }

// switchLeft is the one line that puts the checkout on the default branch:
// the branch after "--", so a name that begins with a dash is not an option.
func switchLeft(branch string) initflow.Left {
	return initflow.Left{Stage: initflow.Repository, Kind: initflow.LeftCommand, Text: "git switch -- " + quoteWord(branch)}
}

// promptLeft is a Left for a step typed at the user's own terminal: what is
// typed there is the prose, the one-line command that starts it is the only
// thing to paste. args are the command's words after the binary, already
// quoted.
func promptLeft(stage, typed string, args ...string) initflow.Left {
	return initflow.Left{Stage: stage, Kind: initflow.LeftPrompt,
		Text:     "in your own terminal window, not through a coding agent or a pipe, " + typed + " after running",
		Commands: []string{selfCommand() + " " + strings.Join(args, " ")}}
}

// originInfo is what the checkout says its origin is.
type originInfo struct {
	Repo, Host     string // owner/name and lower-case host, from what git resolves
	Effective, Raw string // git remote get-url origin, and the raw remote.origin.url of the checkout's own config
	Rewritten      bool   // the two differ in host or repository: url.<base>.insteadOf in the checkout's config
}

// providerHosts is the host each provider's repositories live on. GitHub
// Enterprise and self-hosted Bitbucket are out of scope: their hosts are
// "unusual" and always take the typed confirmation.
var providerHosts = map[string]string{"github": "github.com", "bitbucket": "bitbucket.org"}

func (o originInfo) standardHost() bool {
	for _, h := range providerHosts {
		if o.Host == h {
			return true
		}
	}
	return false
}

// typed is the text the user types to opt a repository in: owner/name, and
// with the host in front when it is not a provider's own host.
func (o originInfo) typed() string {
	if o.standardHost() && !o.Rewritten {
		return o.Repo
	}
	return o.Host + "/" + o.Repo
}

// originParts is the host and owner/name an origin URL names.
func originParts(origin string) (host, repo string, ok bool) {
	u, err := url.Parse(image.HTTPSOrigin(origin))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", "", false
	}
	repo, ok = repoFromOrigin(origin)
	return strings.ToLower(u.Hostname()), repo, ok
}

// readOrigin derives the checkout's repository from both what git resolves
// (git remote get-url origin: url.<base>.insteadOf applies) and the checkout's
// own raw remote.origin.url. A checkout can ship its own .git/config, so the
// two must name the same host and repository, else the origin is rewritten
// and is not trusted by name.
func readOrigin(ctx context.Context, root string) (originInfo, bool) {
	eff, err := gitRead(ctx, root, "remote", "get-url", "origin")
	host, repo, ok := originParts(eff)
	if err != nil || !ok {
		return originInfo{}, false
	}
	o := originInfo{Repo: repo, Host: host, Effective: eff}
	raw, rerr := gitRead(ctx, root, "config", "--local", "--get", "remote.origin.url")
	o.Raw = raw
	rhost, rrepo, rok := originParts(raw)
	if rerr != nil || !rok || rhost != host || !sameRepo(rrepo, repo) {
		o.Rewritten = true
	}
	return o, true
}

// repoKnown reports whether the local config already lists the repository
// this origin names, on the host of the provider recorded for it. A repository
// of another host with a known owner/name, or an origin git config rewrites,
// is not known.
func repoKnown(lc *localcfg.Config, o originInfo) bool {
	if o.Rewritten {
		return false
	}
	want, err := task.CanonicalRepo(o.Repo)
	if err != nil {
		return false
	}
	for name, r := range lc.Repos {
		if c, err := task.CanonicalRepo(name); err == nil && c == want {
			return providerHosts[r.Provider] == o.Host && o.Host != ""
		}
	}
	return false
}

// sameRepo compares two owner/name strings the way the config does.
func sameRepo(a, b string) bool {
	x, err1 := task.CanonicalRepo(a)
	y, err2 := task.CanonicalRepo(b)
	return err1 == nil && err2 == nil && x == y
}

// defaultBranchRef is the remote-tracking ref of the checkout's default
// branch: where origin says HEAD is, else origin/main or origin/master. It
// reads no network and never falls back to a local branch, whose content
// anyone could have written: no origin ref, no default branch.
func defaultBranchRef(ctx context.Context, root string) string {
	var cands []string
	if ref, err := gitRead(ctx, root, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil && strings.HasPrefix(ref, "origin/") {
		cands = append(cands, ref)
	}
	cands = append(cands, "origin/main", "origin/master")
	for _, c := range cands {
		if _, err := gitRead(ctx, root, "rev-parse", "--verify", "--quiet", "refs/remotes/"+c+"^{commit}"); err == nil {
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
	oi, ok := readOrigin(ctx, root)
	repo := oi.Repo
	if !ok {
		return skip("this checkout has no origin repository to onboard")
	}
	ref := defaultBranchRef(ctx, root)
	if ref == "" {
		return skip("no origin/HEAD, origin/main or origin/master ref here, so no default branch to read: run git fetch origin, then rerun fugaro init")
	}
	branch := strings.TrimPrefix(ref, "origin/")
	shown := pluginwire.Printable(branch)
	var gerr bytes.Buffer
	cat := gitCmd(ctx, root, "cat-file", "blob", "refs/remotes/"+ref+":fugaro.yaml")
	cat.Stderr = &gerr
	data, err := cat.Output()
	if err != nil {
		return skip("no fugaro.yaml on the default branch (" + shown + "): /fugaro:setup writes it; merge its pull request, then rerun fugaro init")
	}
	cfg, problems := config.Parse(data)
	if cfg == nil {
		why := "unreadable"
		if len(problems) > 0 {
			why = problems[0].String()
		}
		lf := initflow.Left{Stage: initflow.Repository, Kind: initflow.LeftCommand, Text: selfCommand() + " validate"}
		return nil, initflow.Status{State: initflow.NeedsYou, Detail: "fugaro.yaml on the default branch (" + shown + ") is not valid: " + pluginwire.Printable(oneLineCLI(why)), Left: &lf}
	}
	if project != "" && cfg.Project != project {
		return skip("fugaro.yaml on the default branch (" + shown + ") names another project (or none): " + pluginwire.Printable(project) + " is not its project")
	}
	// The engine reads the working tree: it must be the default branch's
	// file (line endings aside).
	if wt, err := readFugaroYAML(filepath.Join(root, "fugaro.yaml")); err != nil || !bytes.Equal(normalEOL(wt), normalEOL(data)) {
		lf := switchLeft(branch)
		return nil, initflow.Status{State: initflow.NeedsYou, Detail: "this checkout's fugaro.yaml is not the default branch's (" + pluginwire.Printable(ref) + "): the repository is onboarded from the default branch, so run git fetch, switch to it and update it (git pull), then rerun fugaro init", Left: &lf}
	}
	return &repoTarget{root: root, origin: oi, repo: repo, branch: branch, cfg: cfg}, initflow.Status{}
}

func normalEOL(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }

// oneLineCLI keeps s to its first line.
func oneLineCLI(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return pluginwire.Printable(line)
}
