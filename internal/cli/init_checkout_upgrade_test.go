package cli

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// checkoutStageName is the stage of the converge that runs the checkout's own
// steps (fugaro upgrade's pin, plugin and cloud steps) inside `fugaro update`.
const checkoutStageName = "checkout"

// checkoutFile writes one file of a checkout fixture, creating its directory.
func checkoutFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// noClaudeOnPath: the checkout stage runs fugaro upgrade's plugin step, which
// looks claude up through claudeLookPath, so a converge test that runs in a
// checkout never runs — or fails on — the developer's own claude.
func noClaudeOnPath(t *testing.T) {
	t.Helper()
	old := claudeLookPath
	claudeLookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	t.Cleanup(func() { claudeLookPath = old })
}

// checkoutEngine is the converge's engine over the rig's checkout: the
// command and output the loop's stages print through, the run's flags, and a
// local config for the lines that name the project.
func checkoutEngine(t *testing.T, o *initOptions) (*initEngine, *syncBuf) {
	t.Helper()
	if o == nil {
		o = &initOptions{}
	}
	if o.door == "" {
		o.door = updateDoor.name
	}
	out := &syncBuf{}
	cmd := NewRootCmd()
	cmd.SetOut(out)
	cmd.SetErr(&syncBuf{})
	cmd.SetIn(strings.NewReader(""))
	return &initEngine{
		r:  &initRun{o: o, w: out, in: bufio.NewReader(strings.NewReader("")), cmd: cmd},
		lc: &localcfg.Config{Name: "aurora", GCPProject: "proj-1234", Region: "us-east5"},
	}, out
}

// checkoutStageOf is that stage out of the list the update door registers:
// the test names it by its name, so what it drives is the stage `fugaro
// update` runs and not one the test built itself.
func checkoutStageOf(t *testing.T, e *initEngine) initflow.Stage {
	t.Helper()
	st, ok := stageNamed(initStages(e), checkoutStageName)
	if !ok {
		t.Fatalf("fugaro update registers no %s stage: %v; the checkout's own steps (the plugin pin, the Claude Code plugin, the repository's images) run in no stage of the converge", checkoutStageName, namesOf(initStages(e)))
	}
	return st
}

// stageOutcome runs one stage through the loop with the options the engine
// builds, which is how the converge runs it.
func stageOutcome(t *testing.T, e *initEngine, st initflow.Stage) *initflow.Result {
	t.Helper()
	res, err := initflow.Run(t.Context(), []initflow.Stage{st}, e.options())
	if err != nil {
		t.Fatalf("the loop: %v", err)
	}
	return res
}

// A checkout whose plugin pin and installed plugin predate this binary, and
// whose repository is behind this release's base image: `fugaro update` runs
// fugaro upgrade's own steps for it, so all three move — the pin to this
// release's tag, the plugin in Claude Code, and the job images through the
// refresh.
func TestUpdateRunsPinPluginAndImages(t *testing.T) {
	_, root := upgradeRig(t)
	settings := filepath.Join(root, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(wiredAt("v0.5.0")), 0o644); err != nil {
		t.Fatal(err)
	}
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.On(t, "plugins", userInstall("0.5.1"), argsInstall...)
	useFakeClaude(t, f)
	ran := fakeSteps(t, "", nil)

	e, out := checkoutEngine(t, &initOptions{yes: true})
	res := stageOutcome(t, e, checkoutStageOf(t, e))
	if res.Failed != nil || len(res.Left) != 0 {
		t.Fatalf("the checkout stage did not finish: failed %+v, left %+v\n%s", res.Failed, res.Left, out)
	}
	if r := pluginwire.Status(settings, "0.5.1", ""); r.Pin != pluginwire.OK {
		t.Errorf("the pin was not moved to this release: %+v\n%s", r, out)
	}
	if len(f.Changes(t)) == 0 {
		t.Errorf("the plugin was not installed in Claude Code\n%s", out)
	}
	// The refresh's own steps for this checkout: its base image pinned by
	// digest, the daily check job, and the billable rebuilds. (The fake's
	// reload step is not among them: this repository declares no agent image
	// for it to move.)
	if want := []string{"base", "check job", "builds"}; !slices.Equal(*ran, want) {
		t.Errorf("the job images were not refreshed: steps %q, want %q\n%s", *ran, want, out)
	}
	if strings.Contains(out.String(), "not in a checkout") {
		t.Errorf("the stage treated its own checkout as none:\n%s", out)
	}
}

// The billable build is not covered by the run's one confirmation: the review
// screen lists it, and a run with no terminal and without --yes leaves the
// checkout's steps for the user, running nothing.
func TestUpdateImageBuildNeedsItsOwnConfirmation(t *testing.T) {
	var b strings.Builder
	r := &initRun{cmd: &cobra.Command{}, o: &initOptions{}, w: &b, projectName: initProjectName, gcpProject: initProject}
	e := &initEngine{r: r, lc: &localcfg.Config{}, adminsRead: true,
		spec: infra.InstallationSpec{RunsBucket: initRunsBucket, StateBucket: initStateBucket}}
	e.previewed = []initflow.StageResult{{Name: checkoutStageName, State: initflow.Todo}}
	e.reviewScreen(t.Context(), "test")
	if !strings.Contains(b.String(), "billable build of the checkout's job images") {
		t.Errorf("the review screen does not list the checkout's billable build:\n%s", b.String())
	}

	_, root := upgradeRig(t)
	settings := filepath.Join(root, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(wiredAt("v0.5.0")), 0o644); err != nil {
		t.Fatal(err)
	}
	ran := fakeSteps(t, "", nil)
	noTerminal(t)
	e, out := checkoutEngine(t, &initOptions{})
	res := stageOutcome(t, e, checkoutStageOf(t, e))
	if len(res.Left) != 1 || res.ExitCode() != 1 {
		t.Fatalf("a run with no terminal and without --yes did not leave the checkout's steps for the user: %+v\n%s", res, out)
	}
	if len(*ran) != 0 || string(upgradeRead(t, settings)) != wiredAt("v0.5.0") {
		t.Errorf("something ran without a confirmation: steps %q\n%s", *ran, out)
	}
	if l := res.Left[0]; l.Stage != checkoutStageName || !strings.Contains(l.Text, "upgrade") {
		t.Errorf("the left-for-you is %+v, want the checkout stage's own one-line route", l)
	}
}

// Image refresh pins this release's base images by digest, and a tag-only
// reference in the local config is refused with the refresh's own sentence:
// the build never runs, and the stage stops there.
func TestUpdateImageRefreshRefusesTagOnly(t *testing.T) {
	r, _ := upgradeRig(t)
	r.appendConfig(t, "base_images: {base: "+refreshHost+"/fugaro-base/fugaro-base:dev-abc}\n")
	ran := fakeSteps(t, "", nil)

	e, out := checkoutEngine(t, &initOptions{yes: true})
	res := stageOutcome(t, e, checkoutStageOf(t, e))
	if res.Failed == nil || !strings.Contains(res.Failed.Error, "neither this release's pin through the remote repository nor a release image an earlier fugaro copied") {
		t.Fatalf("a tag-only base image was not refused by the refresh: %+v\n%s", res.Failed, out)
	}
	if len(*ran) != 0 {
		t.Errorf("the refresh ran a step over a tag-only base image: %q\n%s", *ran, out)
	}
}

// Outside a checkout there is nothing for the stage to do: one line, and the
// converge goes on.
func TestUpdateOutsideACheckoutSkipsCheckoutSteps(t *testing.T) {
	t.Chdir(t.TempDir())
	e, _ := checkoutEngine(t, &initOptions{yes: true})
	st := checkoutStageOf(t, e)
	status, err := st.Check(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.State != initflow.Skipped || !strings.Contains(status.Detail, "not in a checkout") {
		t.Fatalf("outside a checkout: %+v", status)
	}
	out, err := st.Apply(t.Context(), initflow.Env{Yes: true})
	if err != nil || out.Changed {
		t.Fatalf("an apply outside a checkout: %+v, %v", out, err)
	}
}

// agentCheckout is the fixture the two agent-session tests share: a checkout
// whose pin and installed plugin predate this binary, with a fake claude that
// can install 0.5.1, and CLAUDECODE set — the session a coding agent runs in.
func agentCheckout(t *testing.T) (string, *testutil.ClaudePluginFake, *[]string) {
	t.Helper()
	_, root := upgradeRig(t)
	settings := filepath.Join(root, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(wiredAt("v0.5.0")), 0o644); err != nil {
		t.Fatal(err)
	}
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.On(t, "plugins", userInstall("0.5.1"), argsInstall...)
	useFakeClaude(t, f)
	ran := fakeSteps(t, "", nil)
	t.Setenv("CLAUDECODE", "1")
	return settings, f, ran
}

// The local steps are the ones a marker leaves alone: through the loop the
// converge drives (never a direct Apply — the loop's own agent gate is what
// the stage answers with LocalUnderAgent), the pin moves and the plugin is
// installed in the user's own Claude Code, exactly as fugaro upgrade --local
// does in the same session.
func TestUpdateLocalStepsRunInAgentSession(t *testing.T) {
	settings, f, _ := agentCheckout(t)

	e, out := checkoutEngine(t, &initOptions{yes: true})
	res := stageOutcome(t, e, checkoutStageOf(t, e))
	if res.Failed != nil || len(res.Left) != 0 || res.ExitCode() != 0 {
		t.Fatalf("the loop did not finish the stage in an agent's session: failed %+v, left %+v\n%s", res.Failed, res.Left, out)
	}
	if got := stateOf(res, checkoutStageName); got != initflow.Changed {
		t.Fatalf("the stage is %q in an agent's session, want %q\n%s", got, initflow.Changed, out)
	}
	if r := pluginwire.Status(settings, "0.5.1", ""); r.Pin != pluginwire.OK {
		t.Errorf("the pin was not moved in an agent's session: %+v\n%s", r, out)
	}
	if len(f.Changes(t)) == 0 {
		t.Errorf("the plugin was not installed in an agent's session\n%s", out)
	}
}

// In a coding agent's session the cloud write is refused even with --yes —
// the refresh is the one thing a marker stops — while the local steps run
// through the same loop.
func TestUpdateCloudWritesRefusedInAgentSessionEvenWithYes(t *testing.T) {
	_, _, ran := agentCheckout(t)

	e, out := checkoutEngine(t, &initOptions{yes: true})
	res := stageOutcome(t, e, checkoutStageOf(t, e))
	if res.Failed != nil || len(res.Left) != 0 {
		t.Fatalf("the checkout stage did not finish: failed %+v, left %+v\n%s", res.Failed, res.Left, out)
	}
	if len(*ran) != 0 || strings.Contains(out.String(), "fugaro image refresh of") {
		t.Fatalf("an agent session reached the cloud: steps %q\n%s", *ran, out)
	}
	if !strings.Contains(out.String(), "cloud: skipped: a coding agent's session") {
		t.Errorf("the cloud step was not refused by name:\n%s", out)
	}
	// The local half really ran, so this is not a stage that refused
	// everything and looked right by saying nothing.
	if stateOf(res, checkoutStageName) != initflow.Changed || !strings.Contains(out.String(), "pin: done") {
		t.Errorf("the stage's local steps did not run in an agent's session:\n%s", out)
	}
}

// Running fugaro update twice is uneventful: the second run finds the
// checkout current — the steps' own check mode says so — and writes nothing,
// builds nothing and needs no terminal, and a third run without a terminal
// and without --yes is done the same way rather than left for the user. This
// is the whole of what makes it safe to run the one command again.
func TestUpdateSecondRunWritesNothing(t *testing.T) {
	r, root := upgradeRig(t)
	settings := filepath.Join(root, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(wiredAt("v0.5.0")), 0o644); err != nil {
		t.Fatal(err)
	}
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.On(t, "plugins", userInstall("0.5.1"), argsInstall...)
	useFakeClaude(t, f)
	ran := fakeSteps(t, "", nil)

	e, out := checkoutEngine(t, &initOptions{yes: true})
	res := stageOutcome(t, e, checkoutStageOf(t, e))
	if res.Failed != nil || stateOf(res, checkoutStageName) != initflow.Changed {
		t.Fatalf("the first run did not bring the checkout up: %+v\n%s", res, out)
	}
	// What the steps leave on a real machine after that run, and what neither
	// fake writes: the record Claude Code keeps of the plugin it installed,
	// and the base image the refresh resolved and pinned into fugaro.yaml
	// (the digest reference, the shape the check reads as current). The second
	// run reads exactly these.
	checkoutFile(t, filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"fugaro@fugaro":[{"scope":"user","version":"0.5.1"}]}}`)
	r.appendConfig(t, "image_source: ghcr.io/dimipaun\nbase_images: {base: "+pinnedRef("base", "0.5.1")+"}\n")
	settled, plugins := upgradeRead(t, settings), len(f.Changes(t))
	*ran = nil

	for _, tc := range []struct {
		name  string
		o     *initOptions
		noTTY bool
	}{
		{name: "again, with --yes", o: &initOptions{yes: true}},
		{name: "with no terminal and no --yes", o: &initOptions{}, noTTY: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.noTTY {
				noTerminal(t)
			}
			e, out := checkoutEngine(t, tc.o)
			res := stageOutcome(t, e, checkoutStageOf(t, e))
			if res.Failed != nil || len(res.Left) != 0 || res.ExitCode() != 0 {
				t.Fatalf("a second run was not done: failed %+v, left %+v\n%s", res.Failed, res.Left, out)
			}
			if got := stateOf(res, checkoutStageName); got != initflow.Done {
				t.Errorf("the checkout stage is %q on a current checkout, want %q\n%s", got, initflow.Done, out)
			}
			if len(*ran) != 0 {
				t.Errorf("the second run ran the refresh's steps: %q\n%s", *ran, out)
			}
			if got := string(upgradeRead(t, settings)); got != settled {
				t.Errorf("the second run rewrote the checkout's settings:\n%s\nwant:\n%s", got, settled)
			}
			if got := len(f.Changes(t)); got != plugins {
				t.Errorf("the second run changed the plugin %d more time(s)\n%s", got-plugins, out)
			}
		})
	}
}

// A checkout whose origin is not an owner/name is a failure, not a skip: the
// refresh cannot resolve the repository, which is a real local-file error and
// one fugaro upgrade fails on too. (A checkout with no origin at all is the
// one the step is dropped for: TestUpdateSkipsCloudOnlyWhereTheCheckoutHasNoOrigin.)
func TestUpdateFailsOnAnOriginItCannotRead(t *testing.T) {
	t.Run("an origin that is not owner/name", func(t *testing.T) {
		_, root := upgradeRig(t)
		settings := filepath.Join(root, ".claude", "settings.json")
		if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(settings, []byte(wiredAt("v0.5.0")), 0o644); err != nil {
			t.Fatal(err)
		}
		testutil.Git(t, root, "remote", "set-url", "origin", "https://github.com/")
		ran := fakeSteps(t, "", nil)

		e, out := checkoutEngine(t, &initOptions{yes: true})
		res := stageOutcome(t, e, checkoutStageOf(t, e))
		if res.Failed == nil || !strings.Contains(res.Failed.Error, "cannot tell the repository (owner/name) from the checkout's origin") {
			t.Fatalf("an origin with no owner/name was not a failure: %+v\n%s", res, out)
		}
		if len(*ran) != 0 {
			t.Errorf("the refresh ran over an origin it cannot read: %q\n%s", *ran, out)
		}
		if got := string(upgradeRead(t, settings)); got != wiredAt("v0.5.0") {
			t.Errorf("the pin moved before the run failed on the origin:\n%s\nwant it untouched:\n%s", got, wiredAt("v0.5.0"))
		}
	})

	// A checkout git itself cannot read is the other half: the stage never
	// concluded "no origin", so it did not skip the step over it.
	t.Run("a checkout git cannot read", func(t *testing.T) {
		upgradeEnv(t, "0.5.1")
		dir := t.TempDir()
		checkoutFile(t, filepath.Join(dir, ".git"), "gitdir: "+filepath.Join(dir, "gone")+"\n")
		checkoutFile(t, filepath.Join(dir, "fugaro.yaml"), "version: 1\nproject: aurora\ngit: { provider: github }\n")
		checkoutFile(t, filepath.Join(dir, ".claude", "settings.json"), wiredAt("v0.5.0"))
		t.Chdir(dir)
		ran := fakeSteps(t, "", nil)

		e, out := checkoutEngine(t, &initOptions{yes: true})
		res := stageOutcome(t, e, checkoutStageOf(t, e))
		if res.Failed == nil || !strings.Contains(res.Failed.Error, "reading the checkout's remotes") {
			t.Fatalf("a checkout git cannot read was not a failure: %+v\n%s", res, out)
		}
		if len(*ran) != 0 {
			t.Errorf("the refresh ran in a checkout git cannot read: %q\n%s", *ran, out)
		}
	})
}

// A checkout whose fugaro.yaml names another project is not this run's: the
// pin and the job images are not moved into it, the same gate the plugin stage
// puts on its own wiring. The plugin in the user's own Claude Code is the
// same plugin for every project, and the step still runs.
func TestUpdateLeavesAnotherProjectsCheckoutAlone(t *testing.T) {
	_, root := upgradeRig(t)
	settings := filepath.Join(root, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(wiredAt("v0.5.0")), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, root, "remote", "set-url", "origin", "https://github.com/acme/app.git")
	checkoutFile(t, filepath.Join(root, "fugaro.yaml"), "version: 1\nproject: someoneelse\ngit: { provider: github }\n")
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.On(t, "plugins", userInstall("0.5.1"), argsInstall...)
	useFakeClaude(t, f)
	ran := fakeSteps(t, "", nil)

	e, out := checkoutEngine(t, &initOptions{yes: true})
	res := stageOutcome(t, e, checkoutStageOf(t, e))
	if res.Failed != nil || len(res.Left) != 0 {
		t.Fatalf("the checkout stage did not finish: failed %+v, left %+v\n%s", res.Failed, res.Left, out)
	}
	if got := string(upgradeRead(t, settings)); got != wiredAt("v0.5.0") {
		t.Errorf("the pin moved in another project's checkout:\n%s", got)
	}
	if len(*ran) != 0 {
		t.Errorf("the refresh ran in another project's checkout: %q\n%s", *ran, out)
	}
	for _, step := range []string{"pin", "cloud"} {
		if !strings.Contains(out.String(), step+": skipped: this checkout's fugaro.yaml names another project") {
			t.Errorf("the %s step was not skipped by name:\n%s", step, out)
		}
	}
	if len(f.Changes(t)) == 0 {
		t.Errorf("the plugin was not installed: it is the same plugin for every project\n%s", out)
	}
}

// A checkout with no origin remote has no repository for the refresh to work
// on: the cloud step is skipped — the pin and the plugin still move, which is
// where this differs from fugaro upgrade, whose cloud step fails the same read
// because there the refresh was asked for by name.
func TestUpdateSkipsCloudOnlyWhereTheCheckoutHasNoOrigin(t *testing.T) {
	_, root := upgradeRig(t)
	settings := filepath.Join(root, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(wiredAt("v0.5.0")), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, root, "remote", "remove", "origin")
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.On(t, "plugins", userInstall("0.5.1"), argsInstall...)
	useFakeClaude(t, f)
	ran := fakeSteps(t, "", nil)

	e, out := checkoutEngine(t, &initOptions{yes: true})
	res := stageOutcome(t, e, checkoutStageOf(t, e))
	if res.Failed != nil || len(res.Left) != 0 {
		t.Fatalf("the checkout stage did not finish: failed %+v, left %+v\n%s", res.Failed, res.Left, out)
	}
	if !strings.Contains(out.String(), "cloud: skipped: this checkout has no origin repository to refresh") {
		t.Errorf("the cloud step was not skipped by name:\n%s", out)
	}
	if len(*ran) != 0 {
		t.Errorf("the refresh ran in a checkout with no repository to refresh: %q\n%s", *ran, out)
	}
	if r := pluginwire.Status(settings, "0.5.1", ""); r.Pin != pluginwire.OK {
		t.Errorf("the pin was not moved in a checkout with no origin: %+v\n%s", r, out)
	}
	if len(f.Changes(t)) == 0 {
		t.Errorf("the plugin was not installed in a checkout with no origin\n%s", out)
	}
}

// The stage's own two other reasons to have nothing to do: a development build
// has no release to move to, and a checkout Fugaro is not set up in is not one
// of its checkouts. Both are skips with their own line, and in both nothing
// runs.
func TestUpdateSkipsWithoutAReleaseOrAFugaroCheckout(t *testing.T) {
	t.Run("a development build", func(t *testing.T) {
		_, root := upgradeRig(t)
		settings := filepath.Join(root, ".claude", "settings.json")
		if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(settings, []byte(wiredAt("v0.5.0")), 0o644); err != nil {
			t.Fatal(err)
		}
		ran := fakeSteps(t, "", nil)
		withVersion(t, "")

		e, out := checkoutEngine(t, &initOptions{yes: true})
		st := checkoutStageOf(t, e)
		status, err := st.Check(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if status.State != initflow.Skipped || !strings.Contains(status.Detail, "a development build has no release") {
			t.Fatalf("a development build: %+v", status)
		}
		if _, err := st.Apply(t.Context(), initflow.Env{Yes: true}); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if len(*ran) != 0 || string(upgradeRead(t, settings)) != wiredAt("v0.5.0") {
			t.Errorf("a development build ran something: steps %q\n%s", *ran, out)
		}
	})

	t.Run("a checkout Fugaro is not set up in", func(t *testing.T) {
		upgradeEnv(t, "0.5.1")
		t.Chdir(t.TempDir())
		testutil.Git(t, ".", "init", "--quiet")
		ran := fakeSteps(t, "", nil)

		e, out := checkoutEngine(t, &initOptions{yes: true})
		st := checkoutStageOf(t, e)
		status, err := st.Check(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if status.State != initflow.Skipped || !strings.Contains(status.Detail, "not a Fugaro checkout") {
			t.Fatalf("a checkout with no fugaro.yaml and no wiring: %+v", status)
		}
		if _, err := st.Apply(t.Context(), initflow.Env{Yes: true}); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if len(*ran) != 0 {
			t.Errorf("a checkout Fugaro is not set up in ran something: %q\n%s", *ran, out)
		}
	})
}

// The steps the checkout stage calls are fugaro upgrade's own, and fugaro
// upgrade still runs them: the stage added a caller, not a copy.
func TestUpgradeStillWorks(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	root, settings := skillCheckout(t, wiredAt("v0.5.1"))
	out, _, err := executeStdin(t, "", "upgrade", "--local", root)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if r := pluginwire.Status(settings, "0.5.2", ""); r.Pin != pluginwire.OK {
		t.Errorf("fugaro upgrade --local no longer pins the plugin: %+v\n%s", r, out)
	}
}

// The checkout stage's cloud half is the refresh's step 4, where a billable
// build is asked for one build at a time. Under fugaro update the loop's
// steps are stubbed (there is no GCP project in a test), so this drives that
// very step the way the stage does, at a terminal and without --yes: two
// stale workflows are two questions, and a question answered with something
// other than the project's name submits no build at all. One confirmation
// for several billable builds is one confirmation too few, and the check
// reads it as the user having approved builds nobody asked about.
//
// Mutation (run, restore): ask once, before the loop over the workflows,
// and this test fails: two billable builds, one question.
func TestUpdateAsksBeforeEachBillableBuild(t *testing.T) {
	t.Run("one confirmation per billable build", func(t *testing.T) {
		useVersion(t, "0.5.1")
		r := newAnchorModeRig(t, refreshYAML, true)
		fb := useFakeBuilder(t)
		e, tg := buildTarget(t, r)
		p := &refreshPlan{workflows: []string{"app", "api"}}
		putWorkflowRecordFrom("app", "0.5.1", managedRef("base", "0.4.0"))
		putWorkflowRecordFrom("api", "0.5.1", managedRef("base", "0.4.0"))
		out := atTerminal(t, e, initProjectName+"\n"+initProjectName+"\n")
		if err := e.r.refreshBuilds(t.Context(), p, tg); err != nil {
			t.Fatalf("%v\n%s", err, out.String())
		}
		if len(fb.specs) != 2 {
			t.Fatalf("built %+v, want both stale workflows", fb.specs)
		}
		if got := strings.Count(out.String(), "Type "+initProjectName+" to confirm"); got != 2 {
			t.Errorf("two billable builds were asked for %d time(s), want one confirmation each:\n%s", got, out.String())
		}
	})

	// The answer to the FIRST question decides the first build alone: a
	// wrong name stops the run there, with nothing submitted, which is what
	// "answering no runs no build" has to mean for a user who said no.
	t.Run("a wrong name runs no build", func(t *testing.T) {
		useVersion(t, "0.5.1")
		r := newAnchorModeRig(t, refreshYAML, true)
		fb := useFakeBuilder(t)
		e, tg := buildTarget(t, r)
		p := &refreshPlan{workflows: []string{"app", "api"}}
		putWorkflowRecordFrom("app", "0.5.1", managedRef("base", "0.4.0"))
		putWorkflowRecordFrom("api", "0.5.1", managedRef("base", "0.4.0"))
		out := atTerminal(t, e, "nope\n")
		err := e.r.refreshBuilds(t.Context(), p, tg)
		if err == nil || !strings.Contains(err.Error(), "not confirmed") {
			t.Fatalf("a declined build was not a refusal: %v\n%s", err, out.String())
		}
		if len(fb.specs) != 0 {
			t.Errorf("a declined build submitted %+v", fb.specs)
		}
	})
}
