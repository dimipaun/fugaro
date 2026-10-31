package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
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
	noClaudeOnPath(t) // and the checkout stage may not run the developer's claude
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
	if !errors.As(err, &ny) || !strings.Contains(ny.Left.Text, "setup --github-app-id <id>") || strings.Contains(out.String(), "GitHub App ID") {
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
	if err := e.r.writeRepoConfig(t.Context(), e.lc, infra.RepoSpec{Name: "acme/app", Provider: "github", BaseBranch: "main", GitHubAppID: "12345"}, cfg, path, nil); err != nil {
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

// update --repo is the engine unchanged: its flag checks, its --print-vars
// output (the variables alone on stdout, no result), and the embedded run's
// refusal of an installation of another project, with the same words.
func TestInitRepoUnchanged(t *testing.T) {
	isolateProjects(t, t.TempDir())
	path := writeProject(t, "aurora", "proj-1234")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, "base_images: {base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc}\n"...), 0o600); err != nil {
		t.Fatal(err)
	}
	root := gitCheckout(t, filepath.Join(t.TempDir(), "app"), checkoutYAML("github", "api-key", "aurora", ""))
	testutil.Git(t, root, "remote", "add", "origin", "https://github.com/acme/webapp.git")
	t.Chdir(t.TempDir())
	out, errOut, err := execute(t, "setup", "--repo", "--print-vars", "--project", "aurora", "--github-app-id", "42", root)
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
		{"setup", "--repo", "--firebase-project", "fb-proj-1234", root},
		{"setup", "--repo", "--launcher", "user:a@b.c", root},
		{"setup", "--no-build"},
		{"setup", "--allow-job-delete"},
	} {
		if _, _, err := execute(t, tc...); ExitCode(err) != ExitUserError || err == nil {
			t.Errorf("%v: exit %d, err %v", tc, ExitCode(err), err)
		}
	}
	// --github-app-id is init's own flag (the converge asks for it), but not
	// with the modes that never reach the repository; a bad value is refused
	// where it is given.
	for _, tc := range [][]string{
		{"setup", "--github-app-id", "42", "--print-vars", "--project", "aurora"},
		{"setup", "--github-app-id", "42", "--config-only"},
		{"update", "--github-app-id", "42", "--forget"},
		{"setup", "--github-app-id", "forty-two", "--plan-only"},
		{"setup", "--repo", "--github-app-id", "4 2", root},
	} {
		if _, _, err := execute(t, tc...); ExitCode(err) != ExitUserError || err == nil || !strings.Contains(err.Error(), "github-app-id") {
			t.Errorf("%v: exit %d, err %v", tc, ExitCode(err), err)
		}
	}
}

// The repository stage runs the same engine as update --repo: against the
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
	r.appendConfig(t, "base_images: {base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc}\n")
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

// Review fix (1), security (decision L16): the init repo engine's own build
// offer (repoEngine, step 6) must use resolveForBuild's strictly-resolved
// config, not the earlier, lenient one it read at step 1 — pinning the
// actual call site inside repoEngine, not just the resolveForBuild helper
// (TestResolveForBuildRefusesWhenTheLayerBucketIsUnreadable, image_layer_test.go,
// only proves the helper itself refuses; swapping the argument back at the
// call site left that test, and the whole focused set, green).
//
// layerBucketOpener is patched to fail (an unreachable bucket), as
// run_layer_test.go's TestRunRefusesWhenTheLayerBucketIsUnreadable does for
// fugaro run: with isolateCache leaving nothing cached, a strict reader
// refuses outright, and a lenient one resolves as "no layer" (no error).
// The repository and its first build are onboarded far enough (terraform
// applies, a build would otherwise be needed and offered) that this is a
// real refusal of a real build, not an early, unrelated failure.
//
// Mutation (run, restore): change repoEngine's `r.offerBuilds(ctx, lc,
// buildCfg, spec, names)` call to pass `cfg` (the step 1 lenient read)
// instead of `buildCfg`, and this test fails: the build is offered (and,
// with --yes and no terminal here, left pending) instead of being refused,
// and would have gone out with no layer.
func TestRepoEngineRefusesTheBuildWhenTheLayerBucketIsUnreadable(t *testing.T) {
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
	r.appendConfig(t, "base_images: {base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:dev-abc}\n")
	r.script["show"] = map[string]any{"stdout": `{"format_version":"1.0","values":{"root_module":{"resources":[{"address":"x"}]}}}`}
	r.script["output"] = map[string]any{"stdout": outputsJSON(t)} // project_name: aurora, matching the checkout below
	r.save(t)
	isolateCache(t)
	// Anchored (gcp_project), on bitbucket (no GitHub App to pre-check), with
	// one workflow that needs no build record to be offered a first build.
	yaml := "version: 1\nproject: aurora\ngcp_project: proj-1234\ngit: { provider: bitbucket }\nagent: { auth: oauth }\nworkflows:\n" +
		"  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"
	dir := repoCheckout(t, "https://bitbucket.org/acme/sandbox.git", yaml)
	t.Chdir(dir)
	// Both of app's secrets (its git credential and, agent.auth: oauth,
	// claude-oauth-token) need a version, or infra.NeedsBuild leaves the
	// workflow out as not ready to build (same reason as the "both seeded"
	// comment below), and this test would never reach resolveForBuild at
	// all: the layer bucket's own unreadability would then go untested.
	slug := mustSlug("bitbucket", "acme/sandbox")
	label, err := gcp.RepoLabel(slug)
	if err != nil {
		t.Fatal(err)
	}
	seedLabel := func(name string) map[string]string {
		return map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: label, gcp.LabelSecret: name}
	}
	r.sm.Seed(gcp.SecretID(slug, "bitbucket-token"), seedLabel("bitbucket-token"), []byte("tok"))
	r.sm.Seed(gcp.SecretID(slug, "claude-oauth-token"), seedLabel("claude-oauth-token"), []byte("tok"))
	e := rigEngine(t, r, &initOptions{yes: true})
	fb := useFakeBuilder(t)
	open := layerBucketOpener
	t.Cleanup(func() { layerBucketOpener = open })
	layerBucketOpener = func(context.Context, string) (*blobx.Bucket, error) {
		return nil, &net.OpError{Op: "dial", Err: errors.New("no route to host")}
	}
	err = e.r.repoEngine(t.Context(), ".", "", true)
	if ExitCode(err) != ExitRemoteError || !strings.Contains(err.Error(), "no route to host") {
		t.Fatalf("exit %d, err %v", ExitCode(err), err)
	}
	if len(fb.specs) != 0 {
		t.Fatalf("%d build(s) submitted despite an unreadable, uncached project layer bucket", len(fb.specs))
	}
	if len(r.ran(t, "apply")) == 0 {
		t.Fatal("fixture: the repository's own terraform apply never ran, so this test never reached the build-offer step it means to pin")
	}
}

// (code review, high) D2 made project scope the default store, and a
// repository whose git credential lives only there must still get its first
// image build: SecretVersions/NeedsBuild looked at the repository's own
// container alone, so the engine exited 0 without ever reaching the build
// offer, the job was never deployed, and the run listed the credential as
// still missing with the repository-copy command
// (`secrets set bitbucket-token --repo acme/sandbox`) that D2's other
// message sites were changed to stop printing.
//
// The Anthropic key is absent on purpose (agent.auth: oauth, no key): it is
// the optional mount, so it gates nothing and must not stand in for the
// credential this test is about.
//
// Mutation (run, restore): give init.go's SecretVersions its pre-D2 body (or
// hand it the pre-Readiness spec) and no build is submitted, bitbucket-token
// comes back under res.Missing, and this test fails; drop the
// ProjectSecretCandidates lookup in infra.SecretCommands and it fails on the
// `--repo` line alone.
func TestRepoEngineBuildsOnAProjectWideGitCredential(t *testing.T) {
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
	// A base at gitSecretIDSince (0.7.5) or later: an older image's fugaro
	// predates project-wide resolution and B2's own gate refuses a
	// repository whose credential has no repository copy (its daily check
	// job's rebuild would fall back to one that has no version), which is
	// this test's whole setup.
	r.appendConfig(t, "base_images: {base: "+sandboxBaseRelease+"}\n")
	r.script["show"] = map[string]any{"stdout": `{"format_version":"1.0","values":{"root_module":{"resources":[{"address":"x"}]}}}`}
	r.script["output"] = map[string]any{"stdout": outputsJSON(t)} // project_name: aurora, matching the checkout below
	// The owner's own opt-in to a project-wide git credential (round 2, the
	// owner's ruling of 2026-10-15): bitbucket-token has no project-wide
	// default -- a repository access token is issued for one repository -- so a
	// project copy of it is this repository's only through an explicit
	// project_secrets entry that lists it.
	r.appendConfig(t, "project_secrets:\n  bitbucket-token: { allow_data_to: [acme/sandbox] }\n")
	r.save(t)
	isolateCache(t)
	layerDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(layerDir, "fugaro"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layerDir, "fugaro", "project-layer.yaml"), []byte(testProjectLayer), 0o644); err != nil {
		t.Fatal(err)
	}
	openLayer := layerBucketOpener
	t.Cleanup(func() { layerBucketOpener = openLayer })
	layerBucketOpener = func(ctx context.Context, _ string) (*blobx.Bucket, error) { return blobx.Open(ctx, "file://"+layerDir) }
	openRecords := openRecordBucket
	t.Cleanup(func() { openRecordBucket = openRecords })
	openRecordBucket = func(ctx context.Context, _ string) (*blobx.Bucket, error) {
		return blobx.Open(ctx, "file://"+t.TempDir())
	}
	// Anchored (gcp_project), on bitbucket (no GitHub App to pre-check), with
	// one workflow that needs no build record to be offered a first build.
	yaml := "version: 1\nproject: aurora\ngcp_project: proj-1234\ngit: { provider: bitbucket }\nagent: { auth: oauth }\nworkflows:\n" +
		"  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"
	dir := repoCheckout(t, "https://bitbucket.org/acme/sandbox.git", yaml)
	t.Chdir(dir)
	// The git credential exists project-wide with a version and the marks
	// discovery checks, and nowhere else: this is what `fugaro secrets set
	// bitbucket-token --project-wide` stores for an owner whose local config
	// allow-lists this repository (the entry appended above; with no entry the
	// name has no project-wide scope at all, so the bare command stores the
	// repository's own copy). The bare claude-oauth-token is the other name of
	// agent.auth: oauth's mounts that is not project-scope-eligible
	// (person-owned), so it is seeded as the repository's own copy, which is
	// what update --repo's prompt stores for it.
	projectID := gcp.ProjectSecretID(initProjectName, "bitbucket-token")
	r.sm.Seed(projectID, projectLabels(initProjectName, "bitbucket-token"), []byte("tok"))
	slug := mustSlug("bitbucket", "acme/sandbox")
	label, err := gcp.RepoLabel(slug)
	if err != nil {
		t.Fatal(err)
	}
	r.sm.Seed(gcp.SecretID(slug, "claude-oauth-token"),
		map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: label, gcp.LabelSecret: "claude-oauth-token"}, []byte("tok"))
	e := rigEngine(t, r, &initOptions{})
	fb := useFakeBuilder(t)
	out := atTerminal(t, e, strings.Repeat(initProjectName+"\n", 3))
	if err := e.r.repoEngine(t.Context(), ".", "", true); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if len(fb.specs) != 1 || fb.specs[0].Workflow != "app" {
		t.Fatalf("specs = %+v, want the app workflow's first build: a project-wide credential must not strand the repository", fb.specs)
	}
	for _, m := range e.r.res.Missing {
		if strings.Contains(m, "bitbucket-token") {
			t.Errorf("a credential stored project-wide is listed as missing: %v", e.r.res.Missing)
		}
	}
}

// Review fix (code review on the prior fix): submitAndWait's project-layer
// copy write now opens the record bucket through openRecordBucket (the
// test seam), not blobx.Open directly: this is the happy path that gap
// left untested — a repository onboarded far enough to actually submit its
// first build, with a project layer that resolves and a reachable record
// bucket, confirming the copy lands at builds/<slug>/project-layer.yaml and
// the submitted spec carries its sha256.
//
// Mutation (run, restore): change submitAndWait's openRecordBucket(ctx,
// lc.RecordBucketURL()) call back to blobx.Open(ctx, lc.RecordBucketURL()),
// and this test fails: blobx.Open ignores the override below and tries the
// real gs:// URL, which (unlike layerBucketOpener) TestMain does not
// panic-guard, so it attempts a real network connection and the build
// fails with a connection/credentials error instead of succeeding.
func TestRepoEngineWritesTheProjectLayerCopyForItsFirstBuild(t *testing.T) {
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
	// A base at layeredSince (0.6.0) or later: an older one is refused
	// before the copy write this test means to exercise.
	r.appendConfig(t, "base_images: {base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:0.6.0}\n")
	r.script["show"] = map[string]any{"stdout": `{"format_version":"1.0","values":{"root_module":{"resources":[{"address":"x"}]}}}`}
	r.script["output"] = map[string]any{"stdout": outputsJSON(t)} // project_name: aurora, matching the checkout below
	r.save(t)
	isolateCache(t)
	// A reachable project layer, published to a file://-backed fake bucket.
	layerDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(layerDir, "fugaro"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layerDir, "fugaro", "project-layer.yaml"), []byte(testProjectLayer), 0o644); err != nil {
		t.Fatal(err)
	}
	openLayer := layerBucketOpener
	t.Cleanup(func() { layerBucketOpener = openLayer })
	layerBucketOpener = func(ctx context.Context, _ string) (*blobx.Bucket, error) { return blobx.Open(ctx, "file://"+layerDir) }
	// A reachable record bucket, likewise file://-backed, so the copy write
	// (and its content) can be read back after the run.
	recordsDir := t.TempDir()
	openRecords := openRecordBucket
	t.Cleanup(func() { openRecordBucket = openRecords })
	openRecordBucket = func(ctx context.Context, _ string) (*blobx.Bucket, error) {
		return blobx.Open(ctx, "file://"+recordsDir)
	}
	// Anchored (gcp_project), on bitbucket (no GitHub App to pre-check), with
	// one workflow that needs no build record to be offered a first build.
	// testProjectLayer's own anchor is project aurora, gcp_project proj-1234,
	// matching this checkout and r.script["output"]'s project_name.
	yaml := "version: 1\nproject: aurora\ngcp_project: proj-1234\ngit: { provider: bitbucket }\nworkflows:\n" +
		"  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"
	dir := repoCheckout(t, "https://bitbucket.org/acme/sandbox.git", yaml)
	t.Chdir(dir)
	// testProjectLayer's defaults.agent.auth is api-key, so the workflow
	// needs an anthropic-api-key secret besides its git credential, or
	// NeedsBuild leaves it out (not an image.* concern this test means to
	// exercise): both are seeded so the build is actually offered.
	slug := mustSlug("bitbucket", "acme/sandbox")
	label, err := gcp.RepoLabel(slug)
	if err != nil {
		t.Fatal(err)
	}
	seedLabels := func(name string) map[string]string {
		return map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: label, gcp.LabelSecret: name}
	}
	r.sm.Seed(gcp.SecretID(slug, "bitbucket-token"), seedLabels("bitbucket-token"), []byte("tok"))
	r.sm.Seed(gcp.SecretID(slug, "anthropic-api-key"), seedLabels("anthropic-api-key"), []byte("key"))
	e := rigEngine(t, r, &initOptions{})
	fb := useFakeBuilder(t)
	// The first build's confirmation is typed-only (initflow.Typed excludes
	// --yes, unlike the terraform apply's own ordinary one, and unlike
	// fugaro image refresh's own build confirmation): a fake terminal, with
	// the project's name on stdin for each of the three prompts this
	// reaches (the terraform apply, the build, then the apply that deploys
	// the image just built).
	out := atTerminal(t, e, strings.Repeat(initProjectName+"\n", 3))
	if err := e.r.repoEngine(t.Context(), ".", "", true); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	l, ps := config.ParseProjectLayer([]byte(testProjectLayer), config.LayerAnchor{})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if len(fb.specs) != 1 || fb.specs[0].ProjectLayerSHA256 != l.SHA256 {
		t.Fatalf("specs = %+v, want one with ProjectLayerSHA256 %s", fb.specs, l.SHA256)
	}
	got, err := os.ReadFile(filepath.Join(recordsDir, "builds", slug, "project-layer.yaml"))
	if err != nil || string(got) != testProjectLayer {
		t.Fatalf("the copy = %q, %v; want testProjectLayer", got, err)
	}
}

// (code review, high + medium) A repository whose only pin is served by a
// provider it may send code to needs no Anthropic key -- but the spec the
// build gate reads is the pre-readiness one, whose job still mounts
// ANTHROPIC_API_KEY (spec.go sets it for every api-key run). Gating the first
// image build on that mount left such a repository with no build at all
// (infra.NeedsBuild skipped it, so nothing was ever submitted and the job the
// build is meant to deploy could never come), and the same key was listed
// under "Still missing" with a fugaro secrets set command, and carried into
// res.Missing and --json, for a run that never calls Anthropic.
//
// Mutation (run, restore): drop `&& !OptionalSecret(l)` from NeedsBuild's skip
// guard and no spec is submitted here; drop the SecretStep.Optional filter in
// printMissing and the key comes back into res.Missing.
func TestRepoEngineBuildsKeylessProviderOnly(t *testing.T) {
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
	// The project config enforces, so the gateway exists: openrouter serves
	// every pin this checkout commits, and the run never calls Anthropic.
	r.appendConfig(t, "budget: { mode: enforce, per_run_usd: 2 }\n"+
		"providers:\n  openrouter:\n    kind: anthropic-compat\n    base_url: https://openrouter.ai/api\n"+
		"    auth: bearer\n    secret: openrouter-api-key\n    models: [\"deepseek/*\"]\n    allow_data_to: [acme/sandbox]\n"+
		"base_images: {base: us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-base:0.6.0}\n")
	r.script["show"] = map[string]any{"stdout": `{"format_version":"1.0","values":{"root_module":{"resources":[{"address":"x"}]}}}`}
	r.script["output"] = map[string]any{"stdout": outputsJSON(t)}
	r.save(t)
	isolateCache(t)
	layerDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(layerDir, "fugaro"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layerDir, "fugaro", "project-layer.yaml"), []byte(testProjectLayer), 0o644); err != nil {
		t.Fatal(err)
	}
	openLayer := layerBucketOpener
	t.Cleanup(func() { layerBucketOpener = openLayer })
	layerBucketOpener = func(ctx context.Context, _ string) (*blobx.Bucket, error) { return blobx.Open(ctx, "file://"+layerDir) }
	openRecords := openRecordBucket
	t.Cleanup(func() { openRecordBucket = openRecords })
	openRecordBucket = func(ctx context.Context, _ string) (*blobx.Bucket, error) {
		return blobx.Open(ctx, "file://"+t.TempDir())
	}
	yaml := "version: 1\nproject: aurora\ngcp_project: proj-1234\ngit: { provider: bitbucket }\n" +
		"agent: { auth: api-key, models: { coder: deepseek/deepseek-v4-flash, reviewer: deepseek/deepseek-v4-flash, background: deepseek/deepseek-v4-flash } }\n" +
		"workflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"
	dir := repoCheckout(t, "https://bitbucket.org/acme/sandbox.git", yaml)
	t.Chdir(dir)
	slug := mustSlug("bitbucket", "acme/sandbox")
	label, err := gcp.RepoLabel(slug)
	if err != nil {
		t.Fatal(err)
	}
	seedLabels := func(name string) map[string]string {
		return map[string]string{gcp.LabelManaged: gcp.ManagedValue, gcp.LabelRepo: label, gcp.LabelSecret: name}
	}
	// Every mounted secret has a version -- but not the optional Anthropic
	// key, which no call of this run needs.
	r.sm.Seed(gcp.SecretID(slug, "bitbucket-token"), seedLabels("bitbucket-token"), []byte("tok"))
	r.sm.Seed(gcp.SecretID(slug, "openrouter-api-key"), seedLabels("openrouter-api-key"), []byte("key"))
	e := rigEngine(t, r, &initOptions{})
	fb := useFakeBuilder(t)
	out := atTerminal(t, e, strings.Repeat(initProjectName+"\n", 3))
	if err := e.r.repoEngine(t.Context(), ".", "", true); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if len(fb.specs) != 1 || fb.specs[0].Workflow != "app" {
		t.Fatalf("specs = %+v, want the app workflow's first build: the keyless run must not be stranded", fb.specs)
	}
	for _, m := range e.r.res.Missing {
		if strings.Contains(m, config.AnthropicSecret) {
			t.Errorf("the key is still listed as missing for a run that never calls Anthropic: %v", e.r.res.Missing)
		}
	}
}

// rigEngine is an engine over the rig's local config, as the converge builds
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
	_, _, err := executeStdin(t, "", "setup", "--non-interactive", "--yes", "--onboard-repo", "acme/app")
	if err == nil || !strings.Contains(err.Error(), "--non-interactive: missing") || !strings.Contains(err.Error(), "--github-app-id") {
		t.Fatalf("%v", err)
	}
	if len(r.ran(t, "apply")) != 0 {
		t.Errorf("calls %q", r.calls(t))
	}
}

// A run that wrote a new machine's config from an installation is told so
// before it is asked anything:
// an unlisted repository's typed opt-in would be a prompt for a step that is
// then refused.
func TestRepoStageNewMachineRefusalComesBeforeTheTypedPrompt(t *testing.T) {
	dir := repoCheckout(t, githubOrigin, checkoutYAML("github", "oauth", "aurora", ""))
	t.Chdir(dir)
	fakeTerminal(t)
	e, out := stageEngine(t, "acme/app\n", nil)
	e.lc.Repos = nil // not listed: it would ask for owner/name
	e.newMachine = "wrote this machine's local config"
	s := newRepositoryStage(e)
	s.engineStage.run = func(context.Context) error {
		t.Error("the engine ran after a new machine's config was written")
		return nil
	}
	_, err := s.Apply(t.Context(), initflow.Env{Interactive: true})
	var ny *initflow.NeedsYouError
	if !errors.As(err, &ny) || !strings.Contains(ny.Left.Text, "this machine's local config") {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "Type") || strings.Contains(out.String(), "CONFIRM") {
		t.Errorf("asked before refusing:\n%s", out.String())
	}
}
