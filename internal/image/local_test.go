package image

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/testutil"
)

const localYAML = `version: 1
git: { provider: github, base_branch: develop }
workflows:
  web:
    base: web-node
    image: { node: "24.19.0" }
    commands: { build: sh build.sh, test: sh test.sh, reports: ["junit.xml"] }
    secrets:
      - { name: npm-token, env: NPM_TOKEN }
      - { name: other-token, env: OTHER_TOKEN }
`

// localFixture is a clone, on branch main, of a web-node repository whose
// fugaro.yaml bakes base_branch develop.
func localFixture(t *testing.T) (string, *config.Config) {
	t.Helper()
	testutil.IsolateGit(t)
	remote := testutil.NewRemote(t, map[string]string{
		"fugaro.yaml":       localYAML,
		"package.json":      `{"name":"app","private":true}`,
		"package-lock.json": `{"name":"app","lockfileVersion":3,"requires":true,"packages":{"":{"name":"app"}}}`,
		"build.sh":          "#!/bin/sh\n",
	})
	parent := t.TempDir()
	root := filepath.Join(parent, "app")
	testutil.Git(t, parent, "clone", "--quiet", remote, root)
	return root, parseConfig(t, localYAML)
}

// fakeDocker stands in for docker and inspects what BuildLocal hands it,
// while git runs for real.
type fakeDocker struct {
	t          *testing.T
	buildErr   error
	report     *Report // printed by `docker run`; nil prints nothing
	runErr     error
	builds     [][]string
	buildEnv   [][]string
	context    []string // the build context's files
	dockerfile string
	heads      string // `git bundle list-heads` of the bundle
	runs       [][]string
	runEnv     [][]string
	spec       SelftestSpec // the first (unprivileged) selftest run's spec
	rootSpec   SelftestSpec // the second, --user 0 run's spec
	rootReport *Report      // printed by the --user 0 run; nil means a passing full report
}

func (f *fakeDocker) run(ctx context.Context, c Cmd) error {
	if c.Name != "docker" {
		return ExecRunner(ctx, c)
	}
	switch c.Args[0] {
	case "build":
		f.builds, f.buildEnv = append(f.builds, c.Args), append(f.buildEnv, c.Env)
		dir := c.Args[len(c.Args)-1]
		entries, err := os.ReadDir(dir)
		if err != nil {
			f.t.Fatal(err)
		}
		for _, e := range entries {
			f.context = append(f.context, e.Name())
		}
		data, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
		if err != nil {
			f.t.Fatal(err)
		}
		f.dockerfile = string(data)
		f.heads = testutil.Git(f.t, dir, "bundle", "list-heads", "repo.bundle")
		return f.buildErr
	case "run":
		f.runs, f.runEnv = append(f.runs, c.Args), append(f.runEnv, c.Env)
		if slices.Contains(c.Args, "--user") {
			if err := json.NewDecoder(c.Stdin).Decode(&f.rootSpec); err != nil {
				f.t.Fatal(err)
			}
			rep := f.rootReport
			if rep == nil {
				rep = &Report{Passed: true, Checks: []Check{{Name: "no-setuid", OK: true}, {Name: "no-setgid", OK: true}, {Name: "no-file-caps", OK: true}}}
			}
			if err := json.NewEncoder(c.Stdout).Encode(rep); err != nil {
				f.t.Fatal(err)
			}
			if !rep.Passed {
				return errors.New("exit status 1")
			}
			return nil
		}
		if err := json.NewDecoder(c.Stdin).Decode(&f.spec); err != nil {
			f.t.Fatal(err)
		}
		if f.report != nil {
			if err := json.NewEncoder(c.Stdout).Encode(f.report); err != nil {
				f.t.Fatal(err)
			}
		}
		return f.runErr
	}
	f.t.Fatalf("unexpected docker %v", c.Args)
	return nil
}

func localOptions(root string, cfg *config.Config, f *fakeDocker, log *bytes.Buffer) LocalOptions {
	return LocalOptions{
		Root: root, Config: cfg, Workflow: "web", Base: "fugaro-web-node:test", Tag: "app:local",
		Platform: "linux/amd64", Version: "dev", Smoke: true,
		Env: []string{"NPM_TOKEN=npm-canary-1234"}, Log: log, Run: f.run,
	}
}

func TestBuildLocal(t *testing.T) {
	root, cfg := localFixture(t)
	testutil.Git(t, root, "remote", "set-url", "origin", "https://x-access-token:s3cr3t-tok@github.com/acme/app.git")
	head := testutil.Git(t, root, "rev-parse", "HEAD")
	refsBefore := testutil.Git(t, root, "for-each-ref")
	f := &fakeDocker{t: t, report: &Report{Passed: true, Checks: []Check{{Name: "claude", OK: true}}}}
	var log bytes.Buffer
	res, err := BuildLocal(context.Background(), localOptions(root, cfg, f, &log))
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if res.Commit != head || res.Dockerfile != "generated" || res.Origin != "https://github.com/acme/app.git" || res.Smoke == nil || !res.Smoke.Passed {
		t.Fatalf("result %+v", res)
	}

	build := f.builds[0]
	for _, want := range []string{"app:local", "linux/amd64", "FUGARO_BASE=fugaro-web-node:test", "REPO_URL=/src/repo.bundle",
		"BASE_BRANCH=develop", "REPO_ORIGIN=https://github.com/acme/app.git", "id=NPM_TOKEN,env=NPM_TOKEN"} {
		if !slices.Contains(build, want) {
			t.Errorf("docker build args lack %q: %q", want, build)
		}
	}
	if strings.Contains(strings.Join(build, " "), "s3cr3t-tok") || strings.Contains(strings.Join(f.buildEnv[0], " "), "s3cr3t-tok") {
		t.Error("the token in the origin URL reached docker build")
	}
	if strings.Contains(strings.Join(build, " "), "OTHER_TOKEN") || !strings.Contains(log.String(), "OTHER_TOKEN is not set") {
		t.Errorf("an unset secret was passed, or not reported: %q\n%s", build, log.String())
	}
	if !slices.Contains(f.buildEnv[0], "NPM_TOKEN=npm-canary-1234") || !slices.Contains(f.buildEnv[0], "DOCKER_BUILDKIT=1") {
		t.Errorf("docker build env = %q", f.buildEnv[0])
	}
	if !slices.Equal(f.context, []string{"Dockerfile", "repo.bundle"}) {
		t.Errorf("build context = %v", f.context)
	}
	if f.heads != head+" refs/heads/develop" {
		t.Errorf("bundle heads = %q, want HEAD published as develop", f.heads)
	}
	for _, want := range []string{"RUN /usr/local/lib/fugaro/install-node 24.19.0",
		`--mount=type=secret,id=NPM_TOKEN,uid=1000,mode=0400,required=false --mount=type=secret,id=OTHER_TOKEN,uid=1000,mode=0400,required=false if test -e /run/secrets/NPM_TOKEN; then NPM_TOKEN="$(cat /run/secrets/NPM_TOKEN)" || exit 1; export NPM_TOKEN; fi; if test -e /run/secrets/OTHER_TOKEN; then OTHER_TOKEN="$(cat /run/secrets/OTHER_TOKEN)" || exit 1; export OTHER_TOKEN; fi; npm ci`} {
		if !strings.Contains(f.dockerfile, want) {
			t.Errorf("Dockerfile lacks %q", want)
		}
	}
	if got := testutil.Git(t, root, "for-each-ref"); got != refsBefore {
		t.Errorf("the checkout's refs changed:\n%s\nwant\n%s", got, refsBefore)
	}
	if _, err := os.Stat(build[len(build)-1]); !errors.Is(err, os.ErrNotExist) {
		t.Error("the temporary build context was not removed")
	}

	run := f.runs[0]
	if !slices.Contains(run, "NPM_TOKEN") || !slices.Equal(run[len(run)-4:], []string{"app:local", "fugaro", "image", "selftest"}) {
		t.Errorf("docker run args = %q", run)
	}
	if strings.Contains(strings.Join(build, " "), "npm-canary-1234") || strings.Contains(strings.Join(run, " "), "npm-canary-1234") {
		t.Error("the secret value reached docker build or run arguments; only its env name may appear")
	}
	if !slices.Contains(f.runEnv[0], "NPM_TOKEN=npm-canary-1234") {
		t.Errorf("docker run env = %q, want it to carry NPM_TOKEN", f.runEnv[0])
	}
	s := f.spec
	if s.Base != "web-node" || s.Commit != head || s.Origin != "https://github.com/acme/app.git" || s.Node != "24.19.0" || !s.CheckInit ||
		s.RepoDir != "/work/repo" || s.Verify.RepoDir != "/work/repo" || s.Verify.Build != "sh build.sh" || !slices.Equal(s.Verify.Reports, []string{"junit.xml"}) {
		t.Errorf("selftest spec = %+v", s)
	}
	if !s.CheckHardening || s.RootChecks {
		t.Errorf("selftest spec = %+v, want CheckHardening true and RootChecks false", s)
	}
	// The filesystem scans run in a second container as root, with no
	// secrets, so no unreadable directory can hide a file from them.
	if len(f.runs) != 2 {
		t.Fatalf("docker runs = %q, want the selftest and the root scan", f.runs)
	}
	rootRun := f.runs[1]
	if !slices.Contains(rootRun, "--user") || rootRun[slices.Index(rootRun, "--user")+1] != "0" ||
		!slices.Equal(rootRun[len(rootRun)-4:], []string{"app:local", "fugaro", "image", "selftest"}) || slices.Contains(rootRun, "NPM_TOKEN") {
		t.Errorf("root scan docker run args = %q", rootRun)
	}
	if len(f.runEnv[1]) != 0 {
		t.Errorf("root scan docker run env = %q, want none", f.runEnv[1])
	}
	if !f.rootSpec.RootChecks {
		t.Errorf("root scan spec = %+v, want RootChecks", f.rootSpec)
	}
	if !slices.ContainsFunc(res.Smoke.Checks, func(c Check) bool { return c.Name == "no-setuid" }) {
		t.Errorf("smoke report %+v lacks the root scan's checks", res.Smoke)
	}
}

// TestBuildLocalRootScanFailureFailsTheSmoke: a failing root scan fails
// the whole smoke report even when the unprivileged selftest passed.
func TestBuildLocalRootScanFailureFailsTheSmoke(t *testing.T) {
	root, cfg := localFixture(t)
	f := &fakeDocker{t: t, report: &Report{Passed: true, Checks: []Check{{Name: "user", OK: true}}},
		rootReport: &Report{Passed: false, Checks: []Check{{Name: "no-setgid", OK: false, Detail: "unexpected setgid binaries: opt/x"}}}}
	res, err := BuildLocal(context.Background(), localOptions(root, cfg, f, &bytes.Buffer{}))
	if err != nil || res.Smoke == nil || res.Smoke.Passed {
		t.Fatalf("res %+v, err %v", res, err)
	}
	if c, _ := checkNamed(*res.Smoke, "no-setgid"); c.OK || c.Detail == "" {
		t.Errorf("smoke report %+v lacks the failing root check", res.Smoke)
	}
}

// TestBuildLocalRootScanOmissionFailsTheSmoke: a root report that passes
// but leaves a scan out fails the smoke rather than passing by omission.
func TestBuildLocalRootScanOmissionFailsTheSmoke(t *testing.T) {
	root, cfg := localFixture(t)
	f := &fakeDocker{t: t, report: &Report{Passed: true, Checks: []Check{{Name: "user", OK: true}}},
		rootReport: &Report{Passed: true, Checks: []Check{{Name: "no-setuid", OK: true}}}}
	res, err := BuildLocal(context.Background(), localOptions(root, cfg, f, &bytes.Buffer{}))
	if err != nil || res.Smoke == nil || res.Smoke.Passed {
		t.Fatalf("res %+v, err %v", res, err)
	}
	for _, name := range []string{"no-setgid", "no-file-caps"} {
		if c, ok := checkNamed(*res.Smoke, name); !ok || c.OK {
			t.Errorf("smoke report %+v: missing %s not flagged", res.Smoke, name)
		}
	}
}

func TestBuildLocalConvertsSSHOrigin(t *testing.T) {
	root, cfg := localFixture(t)
	testutil.Git(t, root, "remote", "set-url", "origin", "git@bitbucket.org:acme/app.git")
	f := &fakeDocker{t: t, report: &Report{Passed: true}}
	var log bytes.Buffer
	res, err := BuildLocal(context.Background(), localOptions(root, cfg, f, &log))
	if err != nil {
		t.Fatal(err)
	}
	if res.Origin != "https://bitbucket.org/acme/app.git" || !slices.Contains(f.builds[0], "REPO_ORIGIN=https://bitbucket.org/acme/app.git") || f.spec.Origin != res.Origin {
		t.Fatalf("origin %q, build %q, spec %q", res.Origin, f.builds[0], f.spec.Origin)
	}
}

func TestBuildLocalNotesAnUnknownProviderHost(t *testing.T) {
	root, cfg := localFixture(t)
	testutil.Git(t, root, "remote", "set-url", "origin", "https://git.example.invalid/acme/app.git")
	var log bytes.Buffer
	if _, err := BuildLocal(context.Background(), localOptions(root, cfg, &fakeDocker{t: t, report: &Report{Passed: true}}, &log)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "FUGARO_GIT_PROVIDER") {
		t.Fatalf("log = %q", log.String())
	}
}

func TestBuildLocalNeedsOrigin(t *testing.T) {
	root, cfg := localFixture(t)
	testutil.Git(t, root, "remote", "remove", "origin")
	f := &fakeDocker{t: t}
	_, err := BuildLocal(context.Background(), localOptions(root, cfg, f, &bytes.Buffer{}))
	if err == nil || !strings.Contains(err.Error(), "no origin remote") || len(f.builds) != 0 {
		t.Fatalf("err = %v, builds %d", err, len(f.builds))
	}
}

func TestBuildLocalNotesUncommittedChanges(t *testing.T) {
	root, cfg := localFixture(t)
	testutil.WriteFiles(t, root, map[string]string{"scratch.txt": "wip\n"})
	f := &fakeDocker{t: t}
	var log bytes.Buffer
	o := localOptions(root, cfg, f, &log)
	o.Smoke = false
	if _, err := BuildLocal(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "uncommitted changes are not in the image") || len(f.runs) != 0 {
		t.Fatalf("log %q, runs %d", log.String(), len(f.runs))
	}
}

func TestBuildLocalBuildFailure(t *testing.T) {
	root, cfg := localFixture(t)
	f := &fakeDocker{t: t, buildErr: errors.New("exit status 1")}
	res, err := BuildLocal(context.Background(), localOptions(root, cfg, f, &bytes.Buffer{}))
	if err == nil || !strings.Contains(err.Error(), "docker build failed") || res == nil || res.Commit == "" || len(f.runs) != 0 {
		t.Fatalf("res %+v, err %v", res, err)
	}
}

func TestBuildLocalFailedSmokeIsAReport(t *testing.T) {
	root, cfg := localFixture(t)
	f := &fakeDocker{t: t, report: &Report{Passed: false, Checks: []Check{{Name: "node", OK: false}}}, runErr: errors.New("exit status 1")}
	res, err := BuildLocal(context.Background(), localOptions(root, cfg, f, &bytes.Buffer{}))
	if err != nil || res.Smoke == nil || res.Smoke.Passed {
		t.Fatalf("res %+v, err %v", res, err)
	}
}

func TestBuildLocalSmokeWithoutReport(t *testing.T) {
	root, cfg := localFixture(t)
	f := &fakeDocker{t: t, runErr: errors.New("exit status 125")}
	if _, err := BuildLocal(context.Background(), localOptions(root, cfg, f, &bytes.Buffer{})); err == nil || !strings.Contains(err.Error(), "smoke test") {
		t.Fatalf("err = %v", err)
	}
}

func TestBaseRef(t *testing.T) {
	for _, v := range []string{"1.2.3", "v1.2.3"} {
		if got, err := BaseRef("web-node", v); err != nil || got != "ghcr.io/dimipaun/fugaro-web-node:1.2.3" {
			t.Errorf("BaseRef(%q) = %q, %v", v, got, err)
		}
	}
	for _, v := range []string{"dev", "1.2.3-rc1", "1.2"} {
		if _, err := BaseRef("web-node", v); err == nil || !strings.Contains(err.Error(), "--base") {
			t.Errorf("BaseRef(%q) err = %v", v, err)
		}
	}
}

func TestDefaultTag(t *testing.T) {
	cases := map[string]string{"/src/My Web App": "fugaro-my-web-app-web:local", "/src/app.v2": "fugaro-app.v2-web:local", "/src/!!!": "fugaro-repo-web:local"}
	for root, want := range cases {
		if got := DefaultTag(root, "web"); got != want {
			t.Errorf("DefaultTag(%q) = %q, want %q", root, got, want)
		}
	}
}
