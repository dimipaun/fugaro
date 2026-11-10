package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/dimipaun/fugaro/internal/report"
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
// local config for the lines that name the project. That config is the
// project's own and lists the repository the rig checks out — the plugin
// stage reads it to tell a checkout of the project's from one it may not wire
// (authState), and a project on a real installation knows the repositories its
// runs are started in.
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
		r: &initRun{o: o, w: out, in: bufio.NewReader(strings.NewReader("")), cmd: cmd},
		lc: &localcfg.Config{Name: "aurora", GCPProject: "proj-1234", Region: "us-east5",
			Repos: map[string]localcfg.Repo{"acme/app": {Provider: "github"}}},
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

// updateStages runs the stages named, in the order given, through the loop:
// the two whose steps are `fugaro upgrade`'s are the plugin stage (the pin and
// the plugin in Claude Code) and the checkout stage (the job images), and a
// test that drives one alone cannot see what the other answers with — the
// run's left-for-you accounting is the loop's, not a stage's.
func updateStages(t *testing.T, e *initEngine, names ...string) *initflow.Result {
	t.Helper()
	stages := initStages(e)
	var sel []initflow.Stage
	for _, n := range names {
		st, ok := stageNamed(stages, n)
		if !ok {
			t.Fatalf("fugaro update registers no %s stage: %v", n, namesOf(stages))
		}
		sel = append(sel, st)
	}
	res, err := initflow.Run(t.Context(), sel, e.options())
	if err != nil {
		t.Fatalf("the loop: %v", err)
	}
	return res
}

// A checkout with a plugin to install and a repository behind this release's
// base image: `fugaro update` runs fugaro upgrade's own steps for it, so the
// plugin goes into Claude Code through the plugin stage (which took the step
// over, U1) and the job images through the checkout stage's refresh. The pin an
// older fugaro left in the checkout is not moved: the merge that wrote it is
// gone (U2).
func TestUpdateRunsThePluginAndImages(t *testing.T) {
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
	res := updateStages(t, e, initflow.Plugin, checkoutStageName)
	if res.Failed != nil || len(res.Left) != 0 {
		t.Fatalf("the run did not finish: failed %+v, left %+v\n%s", res.Failed, res.Left, out)
	}
	if got := stateOf(res, string(initflow.Plugin)); got != initflow.Changed {
		t.Errorf("the plugin stage is %q, want %q: the machine's install is its step (U1)\n%s", got, initflow.Changed, out)
	}
	if upgradeRead(t, settings) != wiredAt("v0.5.0") {
		t.Errorf("the run rewrote the checkout's settings, and nothing writes a pin any more (U2):\n%s", upgradeRead(t, settings))
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

// The run that moves the checkout runs its cloud half even where the cloud
// step's own check reads a finished one. That is the state a full converge
// leaves behind: the images stage pins THIS release's base in the local config
// before the checkout stage is checked, and the cloud step's check reads
// fugaro.yaml and the local config and never a build record (cloudCheck, U9)
// — while the job images are built FROM that base and are exactly what a run
// taking the checkout to this release moves. The plugin stage is where the
// checkout stage reads that this run is such a one (initEngine.checkoutBehind),
// and where it reads that it is not: a checkout already at this release writes
// nothing and builds nothing, which is what keeps the one command safe to run
// twice.
func TestUpdateRunsTheCheckoutTheCloudStepCannotSee(t *testing.T) {
	t.Run("a checkout behind this release", func(t *testing.T) {
		r, root := upgradeRig(t)
		settings := filepath.Join(root, ".claude", "settings.json")
		checkoutFile(t, settings, wiredAt("v0.5.0"))
		// The images stage's own write, in the converge this run is part of.
		r.appendConfig(t, "image_source: ghcr.io/dimipaun\nbase_images: {base: "+pinnedRef("base", "0.5.1")+"}\n")
		f := testutil.NewClaudePluginFake(t, "[]", "[]")
		f.On(t, "plugins", userInstall("0.5.1"), argsInstall...)
		useFakeClaude(t, f)
		ran := fakeSteps(t, "", nil)

		e, out := checkoutEngine(t, &initOptions{yes: true})
		res := updateStages(t, e, initflow.Plugin, checkoutStageName)
		if res.Failed != nil || len(res.Left) != 0 {
			t.Fatalf("the run did not finish: failed %+v, left %+v\n%s", res.Failed, res.Left, out)
		}
		if want := []string{"base", "check job", "builds"}; !slices.Equal(*ran, want) {
			t.Errorf("the run that moves the checkout did not refresh its job images: steps %q, want %q\n%s", *ran, want, out)
		}
	})

	t.Run("a checkout already at this release", func(t *testing.T) {
		r, root := upgradeRig(t)
		settings := filepath.Join(root, ".claude", "settings.json")
		checkoutFile(t, settings, wiredAt("v0.5.1"))
		checkoutFile(t, filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "plugins", "installed_plugins.json"),
			`{"version":2,"plugins":{"fugaro@fugaro":[{"scope":"user","version":"0.5.1"}]}}`)
		r.appendConfig(t, "image_source: ghcr.io/dimipaun\nbase_images: {base: "+pinnedRef("base", "0.5.1")+"}\n")
		useFakeClaude(t, testutil.NewClaudePluginFake(t, "[]", "[]"))
		ran := fakeSteps(t, "", nil)

		e, out := checkoutEngine(t, &initOptions{yes: true})
		res := updateStages(t, e, initflow.Plugin, checkoutStageName)
		if res.Failed != nil || len(res.Left) != 0 {
			t.Fatalf("a current checkout was not done: failed %+v, left %+v\n%s", res.Failed, res.Left, out)
		}
		if got := stateOf(res, checkoutStageName); got != initflow.Done {
			t.Errorf("the checkout stage is %q on a checkout already at this release, want %q\n%s", got, initflow.Done, out)
		}
		if len(*ran) != 0 {
			t.Errorf("a checkout already at this release refreshed its job images: %q\n%s", *ran, out)
		}
	})
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
// carrying a pin an older fugaro left and no plugin installed on this machine,
// with a fake claude that can install 0.5.1, and CLAUDECODE set — the session a
// coding agent runs in.
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
// the stages answer with LocalUnderAgent), the plugin stage installs the plugin
// in the user's own Claude Code and leaves the checkout's files alone, exactly
// as fugaro upgrade --local does in the same session. The cloud half is then the step this
// session leaves for the user
// (TestUpdateAgentSessionLeavesTheCloudStepForTheUser), so the run exits 1:
// its local half is done and the checkout is not.
func TestUpdateLocalStepsRunInAgentSession(t *testing.T) {
	settings, f, _ := agentCheckout(t)

	e, out := checkoutEngine(t, &initOptions{yes: true})
	res := updateStages(t, e, initflow.Plugin, checkoutStageName)
	if res.Failed != nil {
		t.Fatalf("the run failed in an agent's session: %+v\n%s", res.Failed, out)
	}
	if got := stateOf(res, string(initflow.Plugin)); got != initflow.Changed {
		t.Fatalf("the plugin stage is %q in an agent's session, want %q\n%s", got, initflow.Changed, out)
	}
	if upgradeRead(t, settings) != wiredAt("v0.5.0") {
		t.Errorf("an agent's session rewrote the checkout's settings (U2):\n%s", upgradeRead(t, settings))
	}
	if len(f.Changes(t)) == 0 {
		t.Errorf("the plugin was not installed in an agent's session\n%s", out)
	}
	if len(res.Left) != 1 || res.ExitCode() != 1 {
		t.Fatalf("an agent's session reported the checkout done with the images untouched: left %+v, exit %d\n%s", res.Left, res.ExitCode(), out)
	}
}

// In a coding agent's session the cloud write is refused even with --yes —
// the refresh is the one thing a marker stops — while the local steps run
// through the same loop. That refusal is a step left for the user, not a stage
// that skipped half of itself and reported the checkout done.
func TestUpdateCloudWritesRefusedInAgentSessionEvenWithYes(t *testing.T) {
	settings, f, ran := agentCheckout(t)

	e, out := checkoutEngine(t, &initOptions{yes: true})
	res := updateStages(t, e, initflow.Plugin, checkoutStageName)
	if res.Failed != nil {
		t.Fatalf("the run failed: %+v\n%s", res.Failed, out)
	}
	if len(*ran) != 0 || strings.Contains(out.String(), "fugaro image refresh of") {
		t.Fatalf("an agent session reached the cloud: steps %q\n%s", *ran, out)
	}
	if !strings.Contains(out.String(), "cloud: skipped: a coding agent's session") {
		t.Errorf("the cloud step was not refused by name:\n%s", out)
	}
	if len(res.Left) != 1 || res.ExitCode() != 1 {
		t.Fatalf("the cloud half was not left for the user: left %+v, exit %d\n%s", res.Left, res.ExitCode(), out)
	}
	// The local half really ran, so this is not a run that refused everything
	// and looked right by saying nothing: the plugin stage is the one that
	// writes on this machine, and the checkout stage's cloud step is parked.
	if stateOf(res, string(initflow.Plugin)) != initflow.Changed ||
		upgradeRead(t, settings) != wiredAt("v0.5.0") || len(f.Changes(t)) == 0 {
		t.Errorf("the local steps did not run in an agent's session:\n%s", out)
	}
}

// agentUpdateCheckout is agentCheckout for the WHOLE converge: an installation
// that has something to converge (updateRig), a checkout of a repository the
// project lists, a plugin pin older than this binary, and CLAUDECODE set. The
// stage-order test needs every stage before the plugin to be done, which is
// what the installation state is for; the checkout-stage tests above drive one
// stage and need none of it.
func agentUpdateCheckout(t *testing.T) (string, *testutil.ClaudePluginFake, *[]string) {
	t.Helper()
	r := updateRig(t)
	r.repoReady(t)
	// The checkout's own secrets are the last stage before the plugin, so they
	// are stored here: the run has to reach the plugin stage for the order to
	// be what the test reads.
	r.seedRepoSecret(t, "bitbucket-token", []byte(tokenValue))
	r.seedRepoSecret(t, "claude-oauth-token", []byte(tokenValue))
	dir := repoCheckout(t, "https://bitbucket.org/acme/sandbox.git", checkoutYAML("bitbucket", "oauth", initProjectName, ""))
	t.Chdir(dir)
	settings := filepath.Join(dir, ".claude", "settings.json")
	checkoutFile(t, settings, wiredAt("v0.1.0"))
	noClaudeOnPath(t)
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.On(t, "plugins", userInstall("0.2.0"), argsInstall...)
	useFakeClaude(t, f)
	ran := fakeSteps(t, "", nil)
	t.Setenv("CLAUDECODE", "1")
	return settings, f, ran
}

// TestUpdateAgentSessionRunsThePluginBeforeTheCheckout: the stage ORDER,
// through the whole converge the way `fugaro update` runs it. The plugin stage
// runs BEFORE the repository stage and the checkout stage, on the same checkout
// and the same leftover pin, and it is LocalUnderAgent since U1 of
// docs/plugin-user-scope/implementation-plan.md (D941): a coding agent's
// session applies it — the pin and the plugin in Claude Code are this machine's
// own writes, and the install is the step that clears the staleness line every
// command prints — so the stages behind it are reached. Before U1 the plugin
// stage was left whole for the user (`fugaro update-skills`), the loop stopped
// there, and the checkout stage behind it was blocked with every local write
// unmade; one stage at a time cannot see that, which is why this test drives the
// whole converge and reads the stages' lines out of the result.
//
// The cloud half is still the step this session leaves for the user, which is
// what the run's exit says (TestUpdateAgentSessionLeavesTheCloudStepForTheUser
// is the same fact read off the checkout stage alone).
func TestUpdateAgentSessionRunsThePluginBeforeTheCheckout(t *testing.T) {
	settings, f, ran := agentUpdateCheckout(t)

	out, errOut, err := executeStdin(t, "", "update", "--yes", "--json")
	var res convergeJSON
	if uerr := json.Unmarshal([]byte(runJSONObject(t, out)), &res); uerr != nil {
		t.Fatalf("%v:\n%s\n%s", uerr, out, errOut)
	}
	if err == nil {
		t.Errorf("the run reported success with the checkout's cloud half parked:\n%s", out)
	}
	if got, want := res.state(initflow.Plugin), "changed"; got != want {
		t.Errorf("the plugin stage is %q under the marker, want %q: its writes are on this machine, which is what LocalUnderAgent is for\n%s", got, want, out)
	}
	if upgradeRead(t, settings) != wiredAt("v0.1.0") {
		t.Errorf("a coding agent's session rewrote the checkout's settings (U2):\n%s", upgradeRead(t, settings))
	}
	if len(f.Changes(t)) == 0 {
		t.Errorf("the plugin was not installed in Claude Code in a coding agent's session:\n%s", out)
	}
	// The plugin step is not handed back to the user: its own route is the
	// command a session that left it would name.
	for _, l := range res.Left {
		if l["stage"] == initflow.Plugin {
			t.Errorf("the plugin step is left for the user (%q), want it applied in a coding agent's session\n%s", l["text"], out)
		}
	}
	// The stage behind it is reached, and it is the checkout whose cloud half
	// this session may not take: its own line is done with what this session
	// may do, the images are not built, and the run is not done — the parked
	// step is what the exit code and left_for_you say (the loop's applied: a
	// stage that left a step for the user is done, not finished).
	if got := res.state(initflow.Checkout); got != "done" {
		t.Errorf("the checkout stage is %q, want %q: the plugin stage applied, so nothing before it stops the run\n%s", got, "done", out)
	}
	left := false
	for _, l := range res.Left {
		if l["stage"] == initflow.Checkout && strings.Contains(l["text"], "terminal window") {
			left = true
		}
	}
	if !left {
		t.Errorf("the checkout's parked cloud half is not left for the user:\n%s", out)
	}
	if len(*ran) != 0 {
		t.Errorf("the session reached the cloud: steps %q\n%s", *ran, out)
	}
}

// TestUpdateAgentSessionLeavesTheCloudStepForTheUser: what a coding agent's
// session does with the half it may not take. The step is left for the user —
// named, with the command that runs it in a terminal, in the result and in the
// JSON — and the run exits 1, so no script and no reader takes the checkout for
// done while its images were never refreshed. It is said on every run of the
// session, not only the one that moved the pin: the step is the user's until it
// is run where it can be.
//
// Mutation (run, restore): drop planFor's agent arm (case u.agent != "") and
// this test fails: the plan keeps the cloud step, the apply's own refusal
// (cloudAgentRefusal) is then a skip with no Left beside it, and the run
// reports the checkout changed with exit 0 — which is what the loop's
// left-for-you accounting is here for. The two tests above fail with it too.
func TestUpdateAgentSessionLeavesTheCloudStepForTheUser(t *testing.T) {
	settings, _, ran := agentCheckout(t)
	root := filepath.Dir(filepath.Dir(settings))

	e, out := checkoutEngine(t, &initOptions{yes: true})
	res := stageOutcome(t, e, checkoutStageOf(t, e))
	if res.Failed != nil {
		t.Fatalf("the checkout stage failed: %+v\n%s", res.Failed, out)
	}
	if len(res.Left) != 1 {
		t.Fatalf("left %+v, want the checkout's cloud half as one step\n%s", res.Left, out)
	}
	l := res.Left[0]
	if l.Stage != checkoutStageName || l.Kind != initflow.LeftCommand {
		t.Errorf("the left-for-you is %+v, want the checkout stage's own command line", l)
	}
	if !strings.Contains(l.Text, "terminal window") {
		t.Errorf("the left-for-you does not say where the step can be run: %q", l.Text)
	}
	if want := []string{
		selfCommand() + " upgrade --yes " + root,
		selfCommand() + " upgrade " + root,
	}; !slices.Equal(l.Commands, want) {
		t.Errorf("the left-for-you names %q, want %q (the parked step's own two commands, as its refusal words them)", l.Commands, want)
	}

	// The second run of the session: this checkout's own plan holds no local
	// step at all — the pin went with the merge (U2, D949), and the install in
	// Claude Code is the machine-side stage's business, not this checkout's —
	// so the one step left is the cloud half, the step the session may not
	// take. The stage's check says so (it does not read the parked step as
	// done), the plan's line keeps naming it, and the JSON carries it.
	checkoutFile(t, settings, wiredAt("v0.5.1"))
	*ran = nil
	e2, out2 := checkoutEngine(t, &initOptions{yes: true})
	res2 := stageOutcome(t, e2, checkoutStageOf(t, e2))
	if res2.Failed != nil || len(res2.Left) != 1 || res2.ExitCode() != 1 {
		t.Fatalf("the second run of the session dropped the step: failed %+v, left %+v, exit %d\n%s", res2.Failed, res2.Left, res2.ExitCode(), out2)
	}
	if len(*ran) != 0 {
		t.Errorf("the second run reached the cloud: steps %q\n%s", *ran, out2)
	}
	for _, gone := range []string{"pin:", "plugin:"} {
		if strings.Contains(out2.String(), gone) {
			t.Errorf("the second run's plan holds a %s step, and a checkout has no local step any more:\n%s", gone, out2)
		}
	}
	if !strings.Contains(out2.String(), "skipped here: cloud") {
		t.Errorf("the second run no longer says which step it leaves:\n%s", out2)
	}
	// The plan view's row is one clause (report.Brief) and the reason is whole
	// in the log, so what is read here is the run's own block under it: the
	// step it leaves is the checkout's cloud half, named in words.
	if !strings.Contains(out2.String(), "the checkout's cloud half") {
		t.Errorf("the second run no longer says which step it leaves:\n%s", out2)
	}
	b, err := res2.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"left_for_you"`) || !strings.Contains(string(b), selfCommand()+" upgrade --yes "+root) {
		t.Errorf("the JSON does not carry the step left for the user:\n%s", b)
	}
}

// The no-terminal refusal is about a step somebody has to confirm at a
// terminal, and a coding agent's session is not that case: the plugin is this
// machine's own install, which is the whole of what LocalUnderAgent says the
// plugin stage does there. A gate that answered the
// marker's no-terminal run with needs-you left the checkout unmoved, and the
// run's own account of it said the opposite — the failure F13 describes, where
// the step that clears the staleness line every command prints is the one left
// behind.
//
// Mutation (run, restore): drop `u.agent == ""` from the checkout Apply's gate
// or the plugin stage's own marker arm and this test fails on that stage: it
// answers needs-you, and the plugin or the cloud half's own line does not
// move.
func TestUpdateLocalStepsRunInAgentSessionWithoutAYes(t *testing.T) {
	settings, f, ran := agentCheckout(t)
	noTerminal(t)

	e, out := checkoutEngine(t, &initOptions{})
	res := updateStages(t, e, initflow.Plugin, checkoutStageName)
	if res.Failed != nil {
		t.Fatalf("the run failed in an agent's session: %+v\n%s", res.Failed, out)
	}
	if got := stateOf(res, string(initflow.Plugin)); got != initflow.Changed {
		t.Fatalf("the plugin stage is %q in an agent's session with no terminal and no --yes, want %q\n%s", got, initflow.Changed, out)
	}
	if upgradeRead(t, settings) != wiredAt("v0.5.0") {
		t.Errorf("an agent's session rewrote the checkout's settings (U2):\n%s", upgradeRead(t, settings))
	}
	if len(f.Changes(t)) == 0 {
		t.Errorf("the plugin was not installed in an agent's session\n%s", out)
	}
	if len(*ran) != 0 {
		t.Errorf("the session reached the cloud without --yes: steps %q\n%s", *ran, out)
	}
	if len(res.Left) != 1 || res.ExitCode() != 1 {
		t.Fatalf("the cloud half was not left for the user: left %+v, exit %d\n%s", res.Left, res.ExitCode(), out)
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

// A checkout whose fugaro.yaml names another project is not this run's: the job
// images are not refreshed in it, and nothing writes a pin any more (U2). The
// plugin stage keeps the same gate on the half that is the checkout's — the
// write U5 adds (F24, D955) — but the install is not the checkout's business:
// the plugin in the user's own Claude Code is the same one for every project,
// so a checkout this run may not wire is not a machine that needs nothing, and
// the step still runs.
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
	res := updateStages(t, e, initflow.Plugin, checkoutStageName)
	if res.Failed != nil || len(res.Left) != 0 {
		t.Fatalf("the run did not finish: failed %+v, left %+v\n%s", res.Failed, res.Left, out)
	}
	if got := string(upgradeRead(t, settings)); got != wiredAt("v0.5.0") {
		t.Errorf("the pin moved in another project's checkout:\n%s", got)
	}
	if len(*ran) != 0 {
		t.Errorf("the refresh ran in another project's checkout: %q\n%s", *ran, out)
	}
	// The cloud half is the line this checkout stage still prints; the wiring
	// half is not written at all, and the install half is the plugin stage's
	// own step line.
	if !strings.Contains(out.String(), "cloud: skipped: this checkout's fugaro.yaml names another project") {
		t.Errorf("the cloud step was not skipped by name:\n%s", out)
	}
	if len(f.Changes(t)) == 0 {
		t.Errorf("the plugin was not installed: it is the same plugin for every project\n%s", out)
	}
}

// A checkout with no origin remote has no repository for the refresh to work
// on: the cloud step is skipped, which is where this differs from fugaro
// upgrade, whose cloud step fails the same read because there the refresh was
// asked for by name. The leftover pin is not this run's business any more (U2:
// nothing writes one), and the plugin in Claude Code, which is the same one for
// every repository, is still installed: the gate is the checkout write's (F24,
// D955), not the machine's own install.
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
	res := updateStages(t, e, initflow.Plugin, checkoutStageName)
	if res.Failed != nil || len(res.Left) != 0 {
		t.Fatalf("the run did not finish: failed %+v, left %+v\n%s", res.Failed, res.Left, out)
	}
	if !strings.Contains(out.String(), "cloud: skipped: this checkout has no origin repository to refresh") {
		t.Errorf("the cloud step was not skipped by name:\n%s", out)
	}
	if len(*ran) != 0 {
		t.Errorf("the refresh ran in a checkout with no repository to refresh: %q\n%s", *ran, out)
	}
	if upgradeRead(t, settings) != wiredAt("v0.5.0") {
		t.Errorf("the checkout's settings moved in a checkout with no origin:\n%s", upgradeRead(t, settings))
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

// TestUpdateRecognisesACheckoutByItsFugaroYAML (U1, D941/F15): what makes a
// checkout one of Fugaro's is its fugaro.yaml, and only that. The second
// disjunct — a .claude/settings.json whose marketplace entry an older fugaro
// wired — is gone with the step that wrote it (the plugin step, which the
// plugin stage runs now): a repository that never opted in has no such file and
// is still a Fugaro checkout, while a checkout whose only mark is the wiring an
// older release left behind is one Fugaro is not set up in.
//
// The wiring's own words go with it: the skip names the file that is missing
// rather than a settings file the reader cannot see in front of them.
func TestUpdateRecognisesACheckoutByItsFugaroYAML(t *testing.T) {
	// The checkout Fugaro is set up in, with nothing ever wired into it: this
	// stage's one step is still this checkout's to run.
	t.Run("a fugaro.yaml and no wiring is a checkout", func(t *testing.T) {
		_, root := upgradeRig(t)
		if _, err := os.Stat(filepath.Join(root, ".claude", "settings.json")); !os.IsNotExist(err) {
			t.Fatalf("the fixture is wired, so it proves nothing about a repository that never opted in: %v", err)
		}
		e, _ := checkoutEngine(t, &initOptions{yes: true})
		status, err := checkoutStageOf(t, e).Check(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if status.State == initflow.Skipped && strings.Contains(status.Detail, "not a Fugaro checkout") {
			t.Errorf("a checkout whose fugaro.yaml is right there was read as none: %+v", status)
		}
	})

	// The wiring alone, with no fugaro.yaml: the pin in it is a leftover no
	// release writes any more, and it is not what makes a checkout one.
	t.Run("wiring alone is not a checkout", func(t *testing.T) {
		_, root := upgradeRig(t)
		if err := os.Remove(filepath.Join(root, "fugaro.yaml")); err != nil {
			t.Fatal(err)
		}
		checkoutFile(t, filepath.Join(root, ".claude", "settings.json"), wiredAt("v0.5.0"))
		e, _ := checkoutEngine(t, &initOptions{yes: true})
		status, err := checkoutStageOf(t, e).Check(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if status.State != initflow.Skipped || !strings.Contains(status.Detail, "not a Fugaro checkout") {
			t.Fatalf("a checkout whose only mark is the wiring an older fugaro wrote is not this run's: %+v", status)
		}
		if strings.Contains(status.Detail, "settings.json") {
			t.Errorf("the skip names a settings file the reader cannot see in front of them: %q", status.Detail)
		}
	})
}

// The steps the checkout stage calls are fugaro upgrade's own, and fugaro
// upgrade still runs them: the stage added a caller, not a copy. Since U2 the
// step it runs for the plugin is the machine's install, so a checkout with no
// claude on PATH is skipped with the slash commands to type — and its settings
// file is left exactly as it was.
func TestUpgradeStillWorks(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	root, settings := skillCheckout(t, wiredAt("v0.5.1"))
	out, _, err := executeStdin(t, "", "upgrade", "--local", root)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if upgradeRead(t, settings) != wiredAt("v0.5.1") {
		t.Errorf("fugaro upgrade --local rewrote the checkout's settings:\n%s", upgradeRead(t, settings))
	}
	for _, want := range []string{"plugin: skipped: claude is not on PATH", "  " + root + ": plugin skipped, cloud skipped, rules skipped, policy skipped"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// The checkout stage's cloud half is the refresh's step 4, where a billable
// build used to be asked for one build at a time. It asks nothing now (T4):
// the builds are bounded by the lease each one takes from the project's budget,
// so every stale workflow is submitted with no terminal, no --yes and nobody
// asked — under fugaro update the loop's steps are stubbed (there is no GCP
// project in a test), and this drives that very step the way the stage does.
//
// Mutation (run, restore): put a confirmation back in front of the build loop
// (confirmBuild, or one typed question for the whole loop), and this test
// fails: nothing is typed and no terminal is there, so no build runs.
func TestUpdateBuildsEveryStaleWorkflowWithNoAnswer(t *testing.T) {
	useVersion(t, "0.5.1")
	r := newAnchorModeRig(t, refreshYAML, true)
	fb := useFakeBuilder(t)
	e, tg := buildTarget(t, r)
	p := &refreshPlan{workflows: []string{"app", "api"}}
	putWorkflowRecordFrom("app", "0.5.1", managedRef("base", "0.4.0"))
	putWorkflowRecordFrom("api", "0.5.1", managedRef("base", "0.4.0"))
	out := atTerminal(t, e, "")
	if err := e.r.refreshBuilds(t.Context(), p, tg); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if len(fb.specs) != 2 {
		t.Fatalf("built %+v, want both stale workflows", fb.specs)
	}
	for _, asked := range []string{"Type ", "⚠ CONFIRM", "[y/N]", "real terminal"} {
		if strings.Contains(out.String(), asked) {
			t.Errorf("a build asked %q, which no session is refused for answering:\n%s", asked, out.String())
		}
	}
}

// TestUpdateBuildsTheStaleWorkflowsWhenNobodyIsAsked is that same step with
// the stage in front of it: a terminal with nothing typed at it, no --yes, and
// the stale workflows' builds are submitted all the same. Nothing above the
// refresh may be what submits them instead — the run's own confirmation covers
// the ordinary steps, and a billable build is not one of them.
//
// The second row is the installation whose owner never ran `fugaro update
// --firebase-project`, whose config therefore carries no budget block and whose
// builds take no lease at all (image_build_lease.go): they are submitted
// uncounted, with the one line that says so, because what this stage runs IS
// `fugaro update`'s checkout half — a build it could not run without the
// backend would leave every stale image of such an installation unbuilt and
// send its owner to an operator step the one upgrade command is not allowed to
// need (HARD RULE).
//
// Mutation (run, restore): give the refresh's build loop a confirmation again
// (confirmBuild, or the stage's own env.Yes standing in for one) and this test
// fails: nothing is typed, so no build runs. Let the backend-less row's build
// lease its cost (drop openBuildLease's early return) and that row fails
// instead, on the checkout's own error: it stops naming the flag to pass.
func TestUpdateBuildsTheStaleWorkflowsWhenNobodyIsAsked(t *testing.T) {
	for _, tc := range []struct {
		name    string
		backend bool
	}{
		{"with a budget backend", true},
		{"with no budget backend", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, root := upgradeRig(t)
			if tc.backend {
				buildBudgetForRig(t, r.initRig)
			}
			checkoutFile(t, filepath.Join(root, ".claude", "settings.json"), wiredAt("v0.5.0"))
			f := testutil.NewClaudePluginFake(t, "[]", "[]")
			f.On(t, "plugins", userInstall("0.5.1"), argsInstall...)
			useFakeClaude(t, f)
			// The base this release pins, already recorded, and two workflows whose
			// records name an older one: the refresh's step 2 has nothing to pin, so
			// the step this test is about is step 4's builds.
			r.appendConfig(t, "image_source: ghcr.io/dimipaun\nbase_images: {base: "+pinnedRef("base", "0.5.1")+"}\n")
			putWorkflowRecordFrom("app", "0.5.1", managedRef("base", "0.4.0"))
			putWorkflowRecordFrom("api", "0.5.1", managedRef("base", "0.4.0"))
			fb := useFakeBuilder(t)
			// Steps 2 and 3 are stubbed (the registry and the Cloud Run Admin API are
			// not what this test is about) and step 3's target is the rig's own
			// workflows; step 4 is the refresh's own.
			real := newRefreshSteps
			newRefreshSteps = func(run *initRun, e *initEngine, p *refreshPlan) refreshSteps {
				s := real(run, e, p)
				s.base = func(context.Context) error { return nil }
				s.checkJob = func(context.Context, *refreshTarget) error { return nil }
				s.reload = func(context.Context) (*refreshTarget, error) {
					return &refreshTarget{lc: p.lc, cfg: p.cfg, spec: infra.RepoSpec{Name: p.repo, Slug: p.slug,
						RegistryPath: "r", BuildServiceAccountEmail: "b@x.iam",
						Workflows: map[string]infra.WorkflowSpec{"app": {}, "api": {}}}}, nil
				}
				return s
			}
			t.Cleanup(func() { newRefreshSteps = real })

			e, refreshOut := checkoutEngine(t, &initOptions{})
			out := atTerminal(t, e, "")
			// The way `fugaro update` runs it: the plugin stage first, whose
			// stale pin is what says this run is taking the checkout to this
			// release, and the checkout stage behind it — the cloud half's own
			// check reads the local config and a build record never, so the
			// plugin stage is where the run's reach of the images is read
			// (initEngine.checkoutBehind).
			res := updateStages(t, e, initflow.Plugin, checkoutStageName)
			if res.Failed != nil {
				t.Fatalf("the run stopped with nothing typed: %+v\n%s%s", res, out, refreshOut)
			}
			if len(fb.specs) != 2 {
				t.Fatalf("built %+v, want both stale workflows", fb.specs)
			}
			for _, asked := range []string{"Type ", "⚠ CONFIRM", "real terminal"} {
				if strings.Contains(out.String(), asked) {
					t.Errorf("a build asked %q, which nothing is waiting to answer:\n%s", asked, out)
				}
			}
			// An installation with no backend says so, once per build; the one
			// that has a backend says nothing about a missing one.
			said := strings.Contains(out.String()+refreshOut.String(), "has no Firebase budget backend")
			if said != !tc.backend {
				t.Errorf("the note about a missing budget backend was printed %v, and this installation has a budget backend: %v\n%s%s", said, tc.backend, out, refreshOut)
			}
		})
	}
}

// TestUpdateCheckoutBuildLinesAreNeverDrawnOver: the same build step, with the
// converge's own reporter drawing the step in flight. The cloud step is fugaro
// image refresh run as a command of its own (runImageRefresh -> newInitRun), and
// that run printed where its own command said — a stream that draws nothing —
// while the spinner redrew the checkout step over it every 100ms (G1). What a
// build says about itself is the one line in this step a person has to be able
// to read, and the lease and the submission print it.
//
// Mutation (run, restore): drop the stage's writer from the refresh's options
// (refreshOptions.out), and this test fails: the build's own lines went to a
// stream the stage never draws on.
func TestUpdateCheckoutBuildLinesAreNeverDrawnOver(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	clearAgentMarkers(t)
	fakeTerminal(t)
	writerAsTerminal(t)
	r, root := upgradeRig(t)
	buildBudgetForRig(t, r.initRig)
	checkoutFile(t, filepath.Join(root, ".claude", "settings.json"), wiredAt("v0.5.0"))
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.On(t, "plugins", userInstall("0.5.1"), argsInstall...)
	useFakeClaude(t, f)
	// One workflow whose record names an older base: step 2 has nothing to pin
	// and step 4 has one build to run.
	r.appendConfig(t, "image_source: ghcr.io/dimipaun\nbase_images: {base: "+pinnedRef("base", "0.5.1")+"}\n")
	putWorkflowRecordFrom("app", "0.5.1", managedRef("base", "0.4.0"))
	putWorkflowRecordFrom("api", "0.5.1", pinnedRef("base", "0.5.1"))
	fb := useFakeBuilder(t)
	// Steps 2 and 3 are stubbed, as in the test above: the registry and the
	// Cloud Run Admin API are not what this test is about.
	real := newRefreshSteps
	newRefreshSteps = func(run *initRun, e *initEngine, p *refreshPlan) refreshSteps {
		s := real(run, e, p)
		s.base = func(context.Context) error { return nil }
		s.checkJob = func(context.Context, *refreshTarget) error { return nil }
		s.reload = func(context.Context) (*refreshTarget, error) {
			return &refreshTarget{lc: p.lc, cfg: p.cfg, spec: infra.RepoSpec{Name: p.repo, Slug: p.slug,
				RegistryPath: "r", BuildServiceAccountEmail: "b@x.iam",
				Workflows: map[string]infra.WorkflowSpec{"app": {}, "api": {}}}}, nil
		}
		return s
	}
	t.Cleanup(func() { newRefreshSteps = real })

	e, out := checkoutEngine(t, &initOptions{})
	// The converge's own shape: the run's writer is a runWriter over the stream
	// the reporter draws on, and the loop opens the stage's step as it enters it
	// (initflow's converge).
	e.r.rep = report.New(report.Options{Out: out, Getenv: os.Getenv, IsTerminal: writerIsTerminal})
	e.r.w = runWriter{r: e.r, out: out}
	step := e.r.rep.Step(checkoutStageName)
	st := checkoutStageOf(t, e)
	if _, err := st.Apply(context.Background(), initflow.Env{Interactive: true}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	step.Done("")
	if len(fb.specs) != 1 {
		t.Errorf("the build submitted %+v, want the one stale workflow", fb.specs)
	}
	// The build's own line is what the run said it does before it did it, and
	// the stage's stream is where a person reads the step.
	if !strings.Contains(out.String(), "submits a Cloud Build for acme/app/app") {
		t.Errorf("the step's stream carries nothing of the build it submitted:\n%s", out)
	}
}

// A failed — or declined — build is the one state this stage's own check
// cannot see: the refresh's step 2 records this release's base image in the
// local config before step 4 builds from it, and the check a rerun starts with
// (cloudCheck) reads that config and fugaro.yaml and never a build record,
// which lives in the runs bucket. So the run reports the failure with the fix
// that reaches the build again, and this test pins both halves: the fix names
// the run whose cloud step re-reads the records, and a rerun of this door
// itself is done at the checkout (which is why the fix may not name it).
func TestUpdateFailedBuildNamesTheRunThatRetries(t *testing.T) {
	r, root := upgradeRig(t)
	checkoutFile(t, filepath.Join(root, ".claude", "settings.json"), wiredAt("v0.5.0"))
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.On(t, "plugins", userInstall("0.5.1"), argsInstall...)
	useFakeClaude(t, f)
	ran := fakeSteps(t, "builds", errors.New("the build of acme/app/app was refused"))

	e, out := checkoutEngine(t, &initOptions{yes: true})
	res := stageOutcome(t, e, checkoutStageOf(t, e))
	if res.Failed == nil || res.ExitCode() != 2 {
		t.Fatalf("a failed build did not fail the stage: %+v, exit %d\n%s", res, res.ExitCode(), out)
	}
	if !strings.Contains(res.Failed.Error, "the build of acme/app/app was refused") {
		t.Errorf("the failure is not the step's own sentence: %+v", res.Failed)
	}
	if want := selfCommand() + " upgrade --yes " + root; !strings.Contains(res.Failed.Fix, want) {
		t.Errorf("the fix does not name the run that offers the build again (%q): %q", want, res.Failed.Fix)
	}
	if strings.Contains(res.Failed.Fix, "resumes at "+checkoutStageName) {
		t.Errorf("the fix is the loop's default, which does nothing here: %q", res.Failed.Fix)
	}
	if len(*ran) != 3 {
		t.Fatalf("the refresh ran %q, want its steps up to the failed one\n%s", *ran, out)
	}

	// What the refresh's step 2 leaves behind on a real machine, and what the
	// fake does not: the base image in the local config and Claude Code's
	// record of the plugin it installed. The rerun reads those.
	r.appendConfig(t, "image_source: ghcr.io/dimipaun\nbase_images: {base: "+pinnedRef("base", "0.5.1")+"}\n")
	checkoutFile(t, filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"fugaro@fugaro":[{"scope":"user","version":"0.5.1"}]}}`)
	*ran = nil
	e2, out2 := checkoutEngine(t, &initOptions{yes: true})
	res2 := stageOutcome(t, e2, checkoutStageOf(t, e2))
	if res2.Failed != nil || stateOf(res2, checkoutStageName) != initflow.Done || len(*ran) != 0 {
		t.Errorf("a rerun of this door is not done at the checkout after a failed build: %+v, steps %q\n%s", res2, *ran, out2)
	}
}
