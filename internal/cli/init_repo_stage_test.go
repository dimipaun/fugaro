package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// stageEngine is an engine with just what the stages here read: the output,
// the stdin and the local config's name.
func stageEngine(t *testing.T, stdin string, o *initOptions) (*initEngine, *syncBuf) {
	t.Helper()
	for _, k := range agentMarkers {
		t.Setenv(k, "") // this session may be a coding agent's
	}
	out := &syncBuf{}
	cmd := NewRootCmd()
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetErr(&syncBuf{})
	if o == nil {
		o = &initOptions{}
	}
	return &initEngine{r: &initRun{o: o, w: out, in: bufio.NewReader(strings.NewReader(stdin)), cmd: cmd}, lc: &localcfg.Config{Name: "aurora", Repos: map[string]localcfg.Repo{"acme/app": {Provider: "github"}}}}, out
}

func commit(t *testing.T, dir, msg string) {
	t.Helper()
	testutil.Git(t, dir, "add", ".")
	testutil.Git(t, dir, "commit", "-q", "-m", msg)
}

// No fugaro.yaml on the default branch: the stage is skipped, saying why and
// what to do next, and the loop goes on (nothing is blocked, nothing left).
func TestRepoStageSkippedWithoutConfig(t *testing.T) {
	dir := repoCheckout(t, githubOrigin, "")
	t.Chdir(dir)
	e, _ := stageEngine(t, "", nil)
	s := newRepositoryStage(e)
	res, err := initflow.Run(t.Context(), []initflow.Stage{s}, e.options())
	if err != nil {
		t.Fatal(err)
	}
	st := res.Stages[0]
	if st.State != initflow.Skipped || !strings.Contains(st.Detail, "no fugaro.yaml on the default branch") || !strings.Contains(st.Detail, "/fugaro:setup") || len(res.Left) != 0 {
		t.Fatalf("%+v, left %v", st, res.Left)
	}
	// Outside a checkout too.
	t.Chdir(t.TempDir())
	e, _ = stageEngine(t, "", nil)
	if st, _ := newRepositoryStage(e).Check(t.Context()); st.State != initflow.Skipped || !strings.Contains(st.Detail, "not in a checkout") {
		t.Errorf("outside a checkout: %+v", st)
	}
}

// The default branch's fugaro.yaml decides, not whatever the working tree
// holds: one only on a feature branch is not there yet; the working tree must
// be the default branch's file for the engine (which reads it) to onboard it.
func TestRepoStageUsesDefaultBranchConfig(t *testing.T) {
	dir := repoCheckout(t, githubOrigin, "")
	t.Chdir(dir)
	yaml := checkoutYAML("github", "oauth", "aurora", "")
	testutil.Git(t, dir, "switch", "-q", "-c", "setup")
	if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	commit(t, dir, "setup")
	check := func() initflow.Status {
		t.Helper()
		e, _ := stageEngine(t, "", nil)
		st, err := newRepositoryStage(e).Check(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	if st := check(); st.State != initflow.Skipped || !strings.Contains(st.Detail, "default branch (main)") {
		t.Fatalf("on the feature branch only: %+v", st)
	}
	testutil.Git(t, dir, "switch", "-q", "main")
	testutil.Git(t, dir, "merge", "-q", "setup")
	fetched(t, dir)
	testutil.Git(t, dir, "switch", "-q", "setup")
	// Merged: on the feature branch with the same file, it applies.
	if st := check(); st.State != initflow.Todo || !strings.Contains(st.Detail, "acme/app") {
		t.Fatalf("merged: %+v", st)
	}
	// A working-tree file that is not the default branch's is the user's to
	// settle first.
	if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte(yaml+"# edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := check()
	if st.State != initflow.NeedsYou || st.Left == nil || !strings.Contains(st.Left.Text, "git switch -- main") || strings.Contains(st.Left.Text, "\n") {
		t.Fatalf("edited: %+v %+v", st, st.Left)
	}
	// Another project's file is not this project's repository.
	if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	e, _ := stageEngine(t, "", nil)
	e.lc.Name = "borealis"
	if st, _ := newRepositoryStage(e).Check(t.Context()); st.State != initflow.Skipped || !strings.Contains(st.Detail, "another project") {
		t.Errorf("another project: %+v", st)
	}
	// An invalid file on the default branch is the user's to fix.
	testutil.Git(t, dir, "switch", "-q", "main")
	if err := os.WriteFile(filepath.Join(dir, "fugaro.yaml"), []byte("version: 1\nworkflows: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commit(t, dir, "broken")
	fetched(t, dir)
	e, _ = stageEngine(t, "", nil)
	if st, _ := newRepositoryStage(e).Check(t.Context()); st.State != initflow.NeedsYou || !strings.Contains(st.Detail, "not valid") {
		t.Errorf("invalid: %+v", st)
	}
}

// gateStage is a stage that records when it ran.
type gateStage struct {
	name  string
	state initflow.State
	log   *[]string
}

func (s gateStage) Name() string { return s.name }
func (s gateStage) Check(context.Context) (initflow.Status, error) {
	return initflow.Status{State: s.state, Detail: "stub"}, nil
}
func (s gateStage) Plan(context.Context, initflow.Env) (initflow.Plan, error) {
	return initflow.Plan{}, nil
}
func (s gateStage) Apply(context.Context, initflow.Env) (initflow.Outcome, error) {
	*s.log = append(*s.log, s.name)
	return initflow.Outcome{Changed: true}, nil
}
func (s gateStage) Verify(context.Context) error { return nil }
func (s gateStage) Left() initflow.Left {
	return initflow.Left{Kind: initflow.LeftCommand, Text: "do the " + s.name}
}

// The repository stage never runs before the secrets are stored: with them
// waiting on the user it is blocked and its engine is not called; once they
// are done it runs, after them (the first image build's confirmation is the
// engine's own, and comes after).
func TestRepoStageRunsAfterSecrets(t *testing.T) {
	dir := repoCheckout(t, githubOrigin, checkoutYAML("github", "oauth", "aurora", ""))
	t.Chdir(dir)
	var log []string
	fakeTerminal(t) // the secrets stage is never covered by --yes: a terminal applies it
	run := func(secrets initflow.State) *initflow.Result {
		log = nil
		e, _ := stageEngine(t, "", &initOptions{githubAppID: "12345"})
		s := newRepositoryStage(e)
		s.engineStage.run = func(context.Context) error { log = append(log, "repository engine"); return nil }
		res, err := initflow.Run(t.Context(), []initflow.Stage{gateStage{"secrets", secrets, &log}, s}, e.options())
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	res := run(initflow.NeedsYou)
	if len(log) != 0 || stateOf(res, "repository") != initflow.Blocked || len(res.Left) != 1 {
		t.Fatalf("blocked: log %v, stages %+v", log, res.Stages)
	}
	res = run(initflow.Todo)
	if got := strings.Join(log, ","); got != "secrets,repository engine" || stateOf(res, "repository") != initflow.Done {
		t.Fatalf("log %q, stages %+v", got, res.Stages)
	}
	// A failed secrets stage stops it as well.
	log = nil
	e, _ := stageEngine(t, "", &initOptions{githubAppID: "12345"})
	s := newRepositoryStage(e)
	s.engineStage.run = func(context.Context) error { log = append(log, "repository engine"); return nil }
	if _, err := initflow.Run(t.Context(), []initflow.Stage{failingStage{gateStage{"secrets", initflow.Todo, &log}}, s}, e.options()); err != nil || len(log) != 0 {
		t.Errorf("after a failed secrets stage: %v, %v", err, log)
	}
}

type failingStage struct{ gateStage }

func (f failingStage) Apply(context.Context, initflow.Env) (initflow.Outcome, error) {
	return initflow.Outcome{}, errors.New("store refused")
}

// The GitHub App's ID is asked once, at a terminal, handed to the engine, and
// not asked again in the run; with the local config holding it (the engine
// records it when it succeeds) it is never asked, and --non-interactive does
// not list it as missing.
func TestAppIDAskedOnceAndStored(t *testing.T) {
	dir := repoCheckout(t, githubOrigin, checkoutYAML("github", "oauth", "aurora", ""))
	t.Chdir(dir)
	fakeTerminal(t)
	e, out := stageEngine(t, "not a number\n12345\n99999\n", nil)
	s := newRepositoryStage(e)
	var seen []string
	s.engineStage.run = func(context.Context) error { seen = append(seen, e.r.o.githubAppID); return nil }
	for range 2 {
		if _, err := s.Apply(t.Context(), initflow.Env{Interactive: true, Yes: true}); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(seen, ",") != "12345,12345" || strings.Count(out.String(), "GitHub App ID for") != 2 { // the prompt, once, and its retry
		t.Errorf("engine saw %v:\n%s", seen, out.String())
	}
	// Not interactive: the one-line command, not a prompt.
	e, out = stageEngine(t, "12345\n", nil)
	s = newRepositoryStage(e)
	s.engineStage.run = func(context.Context) error { t.Error("the engine ran without the App ID"); return nil }
	_, err := s.Apply(t.Context(), initflow.Env{})
	var ny *initflow.NeedsYouError
	if !errors.As(err, &ny) || !strings.Contains(ny.Left.Text, "init --github-app-id <id>") || strings.Contains(out.String(), "GitHub App ID") {
		t.Errorf("err %v\n%s", err, out.String())
	}
	if m := s.Missing(); len(m) != 1 || !strings.Contains(m[0], "--github-app-id") {
		t.Errorf("Missing %v", m)
	}
	// The flag, or the local config's entry, answers it.
	e, _ = stageEngine(t, "", &initOptions{githubAppID: "7"})
	if m := newRepositoryStage(e).Missing(); len(m) != 0 {
		t.Errorf("with the flag: %v", m)
	}
	// What the engine writes after a success is what later runs read.
	e, _ = stageEngine(t, "", nil)
	path := filepath.Join(t.TempDir(), "aurora.yaml")
	e.lc = &localcfg.Config{Version: 1, Name: "aurora", GCPProject: initProject, Region: "us-east5", RunsBucket: initRunsBucket}
	cfg, problems := config.Parse([]byte(checkoutYAML("github", "oauth", "aurora", "")))
	if cfg == nil {
		t.Fatal(problems)
	}
	e.r.cmd.SetErr(io.Discard)
	if err := e.r.writeRepoConfig(e.lc, infra.RepoSpec{Name: "acme/app", Provider: "github", BaseBranch: "main", GitHubAppID: "12345"}, cfg, path, nil); err != nil {
		t.Fatal(err)
	}
	lc, err := localcfg.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	e.lc = lc
	if got := storedAppID(lc, "acme/app"); got != "12345" {
		t.Fatalf("stored %q", got)
	}
	if m := newRepositoryStage(e).Missing(); len(m) != 0 {
		t.Errorf("with the config's entry: %v", m)
	}
	// Another repository of the team gets it as the suggestion.
	if anyStoredAppID(lc) != "12345" {
		t.Error("the team's App ID is not suggested")
	}
}

// init --repo is the engine unchanged: its flag checks, its --print-vars
// output (the variables alone on stdout, no result), and the embedded run's
// refusal of an installation of another project, with the same words.
func TestInitRepoUnchanged(t *testing.T) {
	isolateProjects(t, t.TempDir())
	path := writeProject(t, "aurora", "proj-1234")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, "base_images: {web-node: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:dev-abc}\n"...), 0o600); err != nil {
		t.Fatal(err)
	}
	root := gitCheckout(t, filepath.Join(t.TempDir(), "app"), checkoutYAML("github", "api-key", "aurora", ""))
	testutil.Git(t, root, "remote", "add", "origin", "https://github.com/acme/webapp.git")
	t.Chdir(t.TempDir())
	out, errOut, err := execute(t, "init", "--repo", "--print-vars", "--project", "aurora", "--github-app-id", "42", root)
	if err != nil || !strings.HasPrefix(strings.TrimSpace(out), "{") || strings.Contains(out, `"stages"`) || strings.Contains(out, "left_for_you") || !strings.Contains(errOut, "ungated") {
		t.Fatalf("print-vars: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	if json.Valid([]byte(out)) {
		var m map[string]any
		_ = json.Unmarshal([]byte(out), &m)
		if _, ok := m["stages"]; ok {
			t.Errorf("a result was appended to the variables: %s", out)
		}
	}
	// The installation's flags are still refused for --repo, and the
	// repository's still refused without it.
	for _, tc := range [][]string{
		{"init", "--repo", "--firebase", "fb-proj-1234", root},
		{"init", "--repo", "--launcher", "user:a@b.c", root},
		{"init", "--no-build"},
		{"init", "--allow-job-delete"},
	} {
		if _, _, err := execute(t, tc...); ExitCode(err) != ExitUserError || err == nil {
			t.Errorf("%v: exit %d, err %v", tc, ExitCode(err), err)
		}
	}
	// --github-app-id is init's own flag (the converge asks for it), but not
	// with the modes that never reach the repository; a bad value is refused
	// where it is given.
	for _, tc := range [][]string{
		{"init", "--github-app-id", "42", "--print-vars", "--project", "aurora"},
		{"init", "--github-app-id", "42", "--config-only"},
		{"init", "--github-app-id", "42", "--forget"},
		{"init", "--github-app-id", "forty-two", "--plan-only"},
		{"init", "--repo", "--github-app-id", "4 2", root},
	} {
		if _, _, err := execute(t, tc...); ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "github-app-id") {
			t.Errorf("%v: exit %d, err %v", tc, ExitCode(err), err)
		}
	}
}

// The repository stage runs the same engine as init --repo: against the
// rig, an installation of another project is refused with the engine's words,
// as the stage's failure, in the converge's result and with the engine's exit
// code.
func TestRepoStageRunsTheEngine(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	w, err := r.gcs.Bucket(t, initStateBucket).NewWriter(context.Background(), infra.StatePrefixInstallation+"/default.tfstate", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("{}")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r.appendConfig(t, "base_images: {web-node: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:dev-abc}\n")
	r.script["show"] = map[string]any{"stdout": `{"format_version":"1.0","values":{"root_module":{"resources":[{"address":"x"}]}}}`}
	r.script["output"] = map[string]any{"stdout": outputsJSONWith(t, map[string]any{"project_name": "borealis"})}
	r.save(t)
	dir := repoCheckout(t, "https://bitbucket.org/acme/sandbox.git", checkoutYAML("bitbucket", "oauth", "aurora", ""))
	t.Chdir(dir)
	e := rigEngine(t, r, &initOptions{yes: true})
	res, err := initflow.Run(t.Context(), []initflow.Stage{&preflightStage{e}, newRepositoryStage(e)}, e.options())
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed == nil || res.Failed.Stage != "repository" {
		t.Fatalf("stages %+v", res.Stages)
	}
	err = res.Cause
	if err == nil || !strings.Contains(err.Error(), "fugaro.yaml names project aurora, but this installation is project borealis") || ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if len(r.ran(t, "apply")) != 0 {
		t.Errorf("a refused repository was applied: %q", r.calls(t))
	}
}

// rigEngine is an engine over the rig's local config, as fugaro init builds
// it: no new config to write (old is the file's content).
func rigEngine(t *testing.T, r *initRig, o *initOptions) *initEngine {
	t.Helper()
	lc, err := localcfg.Load(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd()
	cmd.SetIn(strings.NewReader(""))
	cmd.SetErr(&syncBuf{})
	run := &initRun{o: o, w: &syncBuf{}, in: bufio.NewReader(strings.NewReader("")), cmd: cmd}
	run.setProject(lc)
	spec, err := installOptions(o, lc)
	if err != nil {
		t.Fatal(err)
	}
	return &initEngine{r: run, lc: lc, spec: spec, path: r.cfg, old: old}
}

// With every input given, the repository stage's own Missing makes
// --non-interactive name the GitHub App's ID before any stage runs.
func TestNonInteractiveNamesAppIDFromTheStage(t *testing.T) {
	r := newInitRig(t)
	r.stateBucket()
	root := repoCheckout(t, githubOrigin, checkoutYAML("github", "oauth", "aurora", ""))
	t.Chdir(root)
	_, _, err := executeStdin(t, "", "init", "--non-interactive", "--yes", "--onboard-repo", "acme/app")
	if err == nil || !strings.Contains(err.Error(), "--non-interactive: missing") || !strings.Contains(err.Error(), "--github-app-id") {
		t.Fatalf("%v", err)
	}
	if len(r.ran(t, "apply")) != 0 {
		t.Errorf("calls %q", r.calls(t))
	}
}
