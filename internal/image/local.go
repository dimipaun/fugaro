package image

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/verify"
)

// Cmd is one external command BuildLocal runs.
type Cmd struct {
	Name   string
	Args   []string
	Env    []string // added to the current process environment
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Runner runs a Cmd. Tests replace it to stand in for docker.
type Runner func(ctx context.Context, c Cmd) error

// ExecRunner runs c as a child process. When c.Stderr is nil, the error
// includes what the command wrote there.
func ExecRunner(ctx context.Context, c Cmd) error {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Env = append(os.Environ(), c.Env...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = c.Stdin, c.Stdout, c.Stderr
	var stderr bytes.Buffer
	if cmd.Stderr == nil {
		cmd.Stderr = &stderr
	}
	// A leaked descendant holding the output pipe must not hang us (see gitops).
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("%s %s: %w: %s", c.Name, strings.Join(c.Args, " "), err, msg)
		}
		return fmt.Errorf("%s %s: %w", c.Name, strings.Join(c.Args, " "), err)
	}
	return nil
}

var releaseRE = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// BaseRef is the published base image for base that matches fugaro version.
// A development build has none, so the caller must name one.
func BaseRef(base, version string) (string, error) {
	v := strings.TrimPrefix(version, "v")
	if !releaseRE.MatchString(v) {
		return "", fmt.Errorf("fugaro %s is a development build with no published base image; pass --base (build one with images/build-base.sh %s)", version, base)
	}
	return "ghcr.io/dimipaun/fugaro-" + base + ":" + v, nil
}

var tagUnsafeRE = regexp.MustCompile(`[^a-z0-9._-]+`)

// DefaultTag names the local image for workflow in the checkout at root.
func DefaultTag(root, workflow string) string {
	name := strings.Trim(tagUnsafeRE.ReplaceAllString(strings.ToLower(filepath.Base(root)), "-"), "-._")
	if name == "" {
		name = "repo"
	}
	return "fugaro-" + name + "-" + workflow + ":local"
}

// DockerAvailable reports whether a Docker daemon answers.
func DockerAvailable(ctx context.Context, run Runner) error {
	if run == nil {
		run = ExecRunner
	}
	if err := run(ctx, Cmd{Name: "docker", Args: []string{"version", "--format", "{{.Server.Version}}"}, Stdout: io.Discard}); err != nil {
		return fmt.Errorf("docker is not available; `fugaro image build --local` needs a running Docker daemon: %w", err)
	}
	return nil
}

// LocalOptions configure BuildLocal.
type LocalOptions struct {
	Root     string         // the checkout; its HEAD commit is baked in
	Config   *config.Config // the checkout's parsed and checked fugaro.yaml
	Workflow string         // the selected workflow
	Base     string         // base image reference, passed as FUGARO_BASE
	Tag      string         // tag for the built image
	Platform string         // docker --platform; empty means the daemon's default
	Version  string         // fugaro version, for the template header
	Smoke    bool           // run `fugaro image selftest` in the built image
	Env      []string       // where declared secrets are read from, usually os.Environ()
	Log      io.Writer      // git and docker progress
	Run      Runner         // nil means ExecRunner
}

// LocalResult is what `fugaro image build --local --json` prints.
type LocalResult struct {
	Image      string  `json:"image"`
	Base       string  `json:"base"`
	Dockerfile string  `json:"dockerfile"` // "generated", or the repository Dockerfile's path
	Commit     string  `json:"commit"`     // the commit baked into /work/repo
	Origin     string  `json:"origin"`     // the origin URL the checkout keeps
	Smoke      *Report `json:"smoke,omitempty"`
	Error      string  `json:"error,omitempty"`
}

// BuildLocal builds the workflow's derived image with the local Docker
// daemon (design §7.2). The build context holds only the Dockerfile and a
// git bundle of the checkout's HEAD, published as the base branch, so the
// build needs no git credentials. With o.Smoke it then runs the smoke test
// in the image. A failed smoke test is reported in the result, not as an
// error. The result is never nil, so callers can print it either way.
func BuildLocal(ctx context.Context, o LocalOptions) (*LocalResult, error) {
	run := o.Run
	if run == nil {
		run = ExecRunner
	}
	w := o.Config.Workflows[o.Workflow]
	res := &LocalResult{Image: o.Tag, Base: o.Base, Dockerfile: "generated"}
	dockerfile, repoFile, err := Dockerfile(o.Root, o.Config, o.Workflow, o.Version)
	if err != nil {
		return res, err
	}
	if repoFile != "" {
		res.Dockerfile = repoFile
	}
	git := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := run(ctx, Cmd{Name: "git", Args: append([]string{"-C", o.Root}, args...), Stdout: &out})
		return strings.TrimSpace(out.String()), err
	}
	if res.Commit, err = git("rev-parse", "HEAD"); err != nil {
		return res, err
	}
	if status, err := git("--no-optional-locks", "status", "--porcelain"); err == nil && status != "" {
		fmt.Fprintf(o.Log, "fugaro: uncommitted changes are not in the image; it bakes commit %s\n", res.Commit)
	}
	origin, err := git("remote", "get-url", "origin")
	switch {
	case err != nil && strings.Contains(strings.ToLower(err.Error()), "no such remote"):
		// The expected shape of "no origin configured": keep the friendly
		// message rather than the raw git error.
		return res, errors.New("the checkout has no origin remote; the image keeps it as the URL runs fetch from")
	case err != nil:
		// A real git failure (a corrupt checkout, git itself misbehaving);
		// wrap it, but the error text never carries the origin URL, which
		// could hold a token.
		return res, fmt.Errorf("reading the checkout's origin remote: %w", err)
	case origin == "":
		return res, errors.New("the checkout has no origin remote; the image keeps it as the URL runs fetch from")
	}
	res.Origin = HTTPSOrigin(origin)
	if problem := originProblem(res.Origin); problem != "" {
		return res, errors.New(problem)
	}
	if u, err := url.Parse(res.Origin); err == nil && u.Scheme == "https" && u.Hostname() != "github.com" && u.Hostname() != "bitbucket.org" {
		fmt.Fprintf(o.Log, "fugaro: the origin host %s is neither github.com nor bitbucket.org, so runs must set FUGARO_GIT_PROVIDER\n", u.Hostname())
	}

	buildDir, err := os.MkdirTemp("", "fugaro-image-")
	if err != nil {
		return res, err
	}
	defer os.RemoveAll(buildDir)
	contextDir := filepath.Join(buildDir, "context")
	if err := os.Mkdir(contextDir, 0o755); err != nil {
		return res, err
	}
	if err := os.WriteFile(filepath.Join(contextDir, "Dockerfile"), dockerfile, 0o644); err != nil {
		return res, err
	}
	if err := bundle(ctx, run, o.Root, filepath.Join(buildDir, "src.git"), filepath.Join(contextDir, "repo.bundle"), o.Config.Git.BaseBranch); err != nil {
		return res, err
	}

	var secretArgs, runEnvArgs, secretEnv []string
	for _, s := range w.Secrets {
		v := lookupEnv(o.Env, s.Env)
		if v == "" {
			fmt.Fprintf(o.Log, "fugaro: %s is not set, so the build runs without that secret\n", s.Env)
			continue
		}
		secretArgs = append(secretArgs, "--secret", "id="+s.Env+",env="+s.Env)
		runEnvArgs = append(runEnvArgs, "-e", s.Env)
		secretEnv = append(secretEnv, s.Env+"="+v)
	}
	args := []string{"build", "--progress", "plain", "--file", filepath.Join(contextDir, "Dockerfile"), "--tag", o.Tag,
		"--build-arg", "FUGARO_BASE=" + o.Base,
		"--build-arg", "REPO_URL=/src/repo.bundle",
		"--build-arg", "REPO_ORIGIN=" + res.Origin,
		"--build-arg", "BASE_BRANCH=" + o.Config.Git.BaseBranch}
	if o.Platform != "" {
		args = append(args, "--platform", o.Platform)
	}
	args = append(append(args, secretArgs...), contextDir)
	if err := run(ctx, Cmd{Name: "docker", Args: args, Env: append([]string{"DOCKER_BUILDKIT=1"}, secretEnv...), Stdout: o.Log, Stderr: o.Log}); err != nil {
		return res, fmt.Errorf("docker build failed (its output is above): %w", err)
	}
	if !o.Smoke {
		return res, nil
	}

	// Controller ruling: `image build --local`'s in-image selftest always
	// checks the finished image's own hardening (no stray setuid binary, no
	// sudoers grant), alongside the checkout check; this is not a user
	// option.
	spec := SelftestSpec{Base: w.Base, RepoDir: "/work/repo", Commit: res.Commit, Origin: res.Origin, CheckInit: true, CheckHardening: true, Verify: verify.Settings{
		RepoDir: "/work/repo", Build: w.Commands.Build, Test: w.Commands.Test, RerunFailed: w.Commands.RerunFailed,
		Reports: w.Commands.Reports, TimeoutS: int(w.Timeouts.Verify.Seconds()),
	}}
	if w.Base == "web-node" {
		spec.Node = w.Image.Node
	}
	in, err := json.Marshal(spec)
	if err != nil {
		return res, err
	}
	runArgs := []string{"run", "--rm", "-i"}
	if o.Platform != "" {
		runArgs = append(runArgs, "--platform", o.Platform)
	}
	runArgs = append(append(runArgs, runEnvArgs...), o.Tag, "fugaro", "image", "selftest")
	var out bytes.Buffer
	runErr := run(ctx, Cmd{Name: "docker", Args: runArgs, Env: secretEnv, Stdin: bytes.NewReader(in), Stdout: &out, Stderr: o.Log})
	var rep Report
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		const hint = " (the base image's fugaro may predate `image selftest`; rebuild it with images/build-base.sh)"
		if runErr != nil {
			return res, fmt.Errorf("smoke test: %w%s", runErr, hint)
		}
		return res, fmt.Errorf("smoke test printed no report: %w%s", err, hint)
	}
	res.Smoke = &rep
	return res, nil
}

// bundle writes a git bundle of root's HEAD, published as refs/heads/branch.
// It goes through a scratch bare repository, so root's own refs are never
// touched, and turns off gc and maintenance there, so no detached git
// process outlives the call.
func bundle(ctx context.Context, run Runner, root, scratch, out, branch string) error {
	quiet := []string{"-c", "gc.auto=0", "-c", "maintenance.auto=false"}
	steps := [][]string{
		// --template= (empty) skips copying any system or user git template
		// (which can carry hooks) into this scratch repository.
		{"init", "--quiet", "--bare", "--template=", scratch},
		append(append([]string{"-C", scratch}, quiet...), "fetch", "--quiet", "--no-tags", root, "+HEAD:refs/heads/"+branch),
		{"-C", scratch, "bundle", "create", "--quiet", out, "refs/heads/" + branch},
	}
	for _, args := range steps {
		if err := run(ctx, Cmd{Name: "git", Args: args}); err != nil {
			return fmt.Errorf("bundling the checkout: %w", err)
		}
	}
	return nil
}

func lookupEnv(env []string, key string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(env[i], "="); ok && k == key {
			return v
		}
	}
	return ""
}
