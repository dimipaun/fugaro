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
	if want := []string{"base", "reload", "check job", "builds"}; !slices.Equal(*ran, want) {
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

// In a coding agent's session the cloud write is refused even with --yes —
// the refresh is the one thing a marker stops — while the local steps run, as
// they do under fugaro upgrade --local.
func TestUpdateCloudWritesRefusedInAgentSessionEvenWithYes(t *testing.T) {
	_, root := upgradeRig(t)
	settings := filepath.Join(root, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(wiredAt("v0.5.0")), 0o644); err != nil {
		t.Fatal(err)
	}
	ran := fakeSteps(t, "", nil)
	t.Setenv("CLAUDECODE", "1")

	e, out := checkoutEngine(t, &initOptions{yes: true})
	st := checkoutStageOf(t, e)
	if _, err := st.Apply(t.Context(), initflow.Env{Yes: true, Interactive: true}); err != nil {
		t.Fatalf("the stage refused the local steps too: %v\n%s", err, out)
	}
	if len(*ran) != 0 || strings.Contains(out.String(), "fugaro image refresh of") {
		t.Fatalf("an agent session reached the cloud: steps %q\n%s", *ran, out)
	}
	if !strings.Contains(out.String(), "cloud: skipped: a coding agent's session") {
		t.Errorf("the cloud step was not refused by name:\n%s", out)
	}
}

// The local steps are the ones a marker leaves alone: the pin moves and the
// plugin is installed in the user's own Claude Code, exactly as fugaro
// upgrade --local does there.
func TestUpdateLocalStepsRunInAgentSession(t *testing.T) {
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
	t.Setenv("CLAUDECODE", "1")

	e, out := checkoutEngine(t, &initOptions{yes: true})
	if _, err := checkoutStageOf(t, e).Apply(t.Context(), initflow.Env{Yes: true, Interactive: true}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if r := pluginwire.Status(settings, "0.5.1", ""); r.Pin != pluginwire.OK {
		t.Errorf("the pin was not moved in an agent's session: %+v\n%s", r, out)
	}
	if len(f.Changes(t)) == 0 {
		t.Errorf("the plugin was not installed in an agent's session\n%s", out)
	}
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
