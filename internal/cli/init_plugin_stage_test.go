package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/testutil"
)

const settingsWithOthers = `{
  "permissions": { "allow": ["Bash(go test:*)"] },
  "extraKnownMarketplaces": { "acme": { "source": { "source": "github", "repo": "acme/tools" } } },
  "enabledPlugins": { "acme@acme": true },
  "model": "sonnet"
}
`

func releaseBuild(t *testing.T, v string) {
	t.Helper()
	old := Version
	Version = v
	t.Cleanup(func() { Version = old })
}

// wiringCheckout is a checkout (cwd) with settings as .claude/settings.json
// ("" for none), and the path of that file. Nothing in the tree writes that file
// any more: a pin there is the leftover an older fugaro left, which `update` now
// reports as a notice (U6a, repo_notices.go) and never moves.
func wiringCheckout(t *testing.T, settings string) string {
	t.Helper()
	return wiringCheckoutAt(t, githubOrigin, settings)
}

func wiringCheckoutAt(t *testing.T, origin, settings string) string {
	t.Helper()
	dir := repoCheckout(t, origin, "")
	t.Chdir(dir)
	p := filepath.Join(dir, ".claude", "settings.json")
	if settings != "" {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(settings), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func runPlugin(t *testing.T, e *initEngine) *initflow.Result {
	t.Helper()
	res, err := initflow.Run(t.Context(), []initflow.Stage{newPluginStage(e)}, e.options())
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// U2 of docs/plugin-user-scope/implementation-plan.md (D949, answered by the
// owner 2026-11-04): the pin merge is deleted, so this stage's only write is on
// this machine — the plugin installed in the user's own Claude Code — and a
// checkout that has no .claude/settings.json comes out of it with none. Red
// against the tree the merge was in, where the stage's plan created the file.
func TestPluginStageWritesNoSettingsFile(t *testing.T) {
	releaseBuild(t, "0.2.0")
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.On(t, "plugins", userInstall("0.2.0"), argsInstall...)
	settings := wiringCheckout(t, "")
	e, out := stageEngine(t, "", &initOptions{yes: true})
	useFakeClaude(t, f)
	if res := runPlugin(t, e); stateOf(res, "plugin") != initflow.Changed {
		t.Fatalf("the install half did not run: %+v\n%s", res.Stages, out)
	}
	if _, err := os.Stat(settings); !os.IsNotExist(err) {
		t.Fatalf("the plugin stage wrote %s (err %v): nothing in the tree writes a pin any more (U2)\n%s", settings, err, out)
	}
	if _, err := os.Stat(filepath.Dir(settings)); !os.IsNotExist(err) {
		t.Errorf("the stage made %s, and there is nothing here to put in it", filepath.Dir(settings))
	}
}

// The stage installs the plugin in Claude Code at user scope through the claude
// CLI: the converge's own plugin step, whose fixed calls are the whole of what
// this stage runs. A second run finds the marketplace and the install where the
// first left them and has nothing to do.
func TestInitPluginStageInstallsInClaudeCode(t *testing.T) {
	releaseBuild(t, "0.2.0")
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.On(t, "plugins", userInstall("0.2.0"), argsInstall...)
	wiringCheckout(t, "")
	e, out := stageEngine(t, "", &initOptions{yes: true})
	useFakeClaude(t, f)
	res := runPlugin(t, e)
	if stateOf(res, "plugin") != initflow.Changed || len(res.Left) != 0 {
		t.Fatalf("stages %+v left %v\n%s", res.Stages, res.Left, out)
	}
	want := [][]string{argsListMarkets, argsListPlugins, addUser("0.2.0"), argsInstall, argsListPlugins}
	if !slices.EqualFunc(callArgs(f.Calls(t)), want, slices.Equal[[]string]) {
		t.Fatalf("calls %q, want %q", callArgs(f.Calls(t)), want)
	}
	// What the first run declared and installed is what the second one reads.
	f.Write(t, "marketplaces.json", ourMarketAt("v0.2.0"))
	e, out = stageEngine(t, "", &initOptions{yes: true})
	useFakeClaude(t, f)
	res = runPlugin(t, e)
	if st := res.Stages[0]; st.State != initflow.Done || !strings.Contains(st.Detail, "No changes") {
		t.Errorf("the rerun: %+v\n%s", st, out)
	}
}

// D955 (U8, F24): the machine's own install asks nothing about the checkout's
// repository. The plugin this stage installs is the same one for every
// checkout, and since U6a there is no write in the checkout at all for a gate to
// guard, so a repository the project's config does not list is no reason to send
// a teammate to a second command before their own machine has the plugin. The
// install runs, the checkout is not written, and no line names `fugaro setup`.
func TestInitPluginStageInstallsWhereTheMachineDoesNotKnowTheRepository(t *testing.T) {
	releaseBuild(t, "0.2.0")
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.On(t, "plugins", userInstall("0.2.0"), argsInstall...)
	// A repository the machine's config does not list: the rig's config knows
	// acme/app, and this checkout's origin is another one. `update` does not
	// enrol by asking (D5), and a coding agent's session — the teammate's own,
	// which is where this command runs for the person who holds no owner's
	// role — confirms nothing (canConfirmTyped), so the gate answered
	// needs-you here before U8 and sent them to fugaro setup for a write that
	// is not the repository's.
	settings := wiringCheckoutAt(t, "https://github.com/acme/other.git", "")
	e, out := stageEngine(t, "", &initOptions{door: updateDoor.name, yes: true})
	t.Setenv(agentMarkers[0], "1")
	useFakeClaude(t, f)
	res := runPlugin(t, e)
	if st := res.Stages[0]; st.State != initflow.Changed {
		t.Fatalf("the install half did not run: %+v\n%s", st, out)
	}
	if stateOf(res, "plugin") != initflow.Changed ||
		!slices.ContainsFunc(callArgs(f.Calls(t)), func(a []string) bool { return slices.Equal(a, addUser("0.2.0")) }) {
		t.Errorf("the plugin in the user's own Claude Code was not installed: %+v\n%s", res.Stages, out)
	}
	if seen := out.String(); strings.Contains(seen, setupDoor.name) || strings.Contains(seen, "not one of project") {
		t.Errorf("the machine's own install sent the reader to the repository's gate:\n%s", seen)
	}
	if len(res.Left) != 0 || res.Failed != nil {
		t.Errorf("the run left something for the user over a write that is not the repository's: left %+v failed %+v", res.Left, res.Failed)
	}
	if _, err := os.Stat(settings); err == nil {
		t.Errorf("%s was written: the install is the machine's, and the checkout carries no pin to take out", settings)
	}
}

// Outside a checkout the machine's own install still happens (D955, U8): what
// this stage writes is the user's own Claude Code through the claude CLI, which
// needs no repository at all. Since U6a the stage reads no working directory —
// the install half runs in a fixed one (pluginInstallDir) — so outside a
// checkout behaves exactly as inside one: the plugint installs, and nothing
// outside the machine is written.
func TestInitPluginStageOutsideACheckoutInstallsOnTheMachine(t *testing.T) {
	releaseBuild(t, "0.2.0")
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.On(t, "plugins", userInstall("0.2.0"), argsInstall...)
	dir := t.TempDir()
	t.Chdir(dir)
	e, out := stageEngine(t, "", &initOptions{yes: true})
	useFakeClaude(t, f)
	res := runPlugin(t, e)
	if stateOf(res, "plugin") != initflow.Changed ||
		!slices.ContainsFunc(callArgs(f.Calls(t)), func(a []string) bool { return slices.Equal(a, addUser("0.2.0")) }) {
		t.Fatalf("the machine's own install did not run outside a checkout: %+v\n%s", res.Stages, out)
	}
	if seen := out.String(); strings.Contains(seen, "installs the plugin from the checkout's repository") {
		t.Errorf("an outside-a-checkout run is still sent away from its own machine's plugin:\n%s", seen)
	}
	if len(res.Left) != 0 || res.Failed != nil {
		t.Errorf("left %+v failed %+v", res.Left, res.Failed)
	}
	if es, _ := os.ReadDir(dir); len(es) != 0 {
		t.Errorf("wrote into %s: %v", dir, es)
	}
}

// U6a: the install half runs in a fixed directory and reads no working
// directory, so where the command was typed in — a checkout, a subdirectory of
// one, a linked worktree, a directory outside any of them — makes no difference
// to it. The fake claude records the directory of every call, which is the one
// the stage decided and not the one the test chdir'd into.
func TestPluginInstallRunsInAFixedDirectory(t *testing.T) {
	releaseBuild(t, "0.2.0")
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.On(t, "plugins", userInstall("0.2.0"), argsInstall...)
	dir := wiringCheckout(t, "")
	e, out := stageEngine(t, "", &initOptions{yes: true})
	useFakeClaude(t, f)
	if res := runPlugin(t, e); stateOf(res, "plugin") != initflow.Changed {
		t.Fatalf("the install half did not run: %+v\n%s", res.Stages, out)
	}
	// The same answer from anywhere: the stage's own location, read in the
	// checkout and then outside one, is the fixed directory and never the
	// working directory.
	want := pluginInstallDir()
	inCheckout := newPluginStage(e)
	inCheckout.locate()
	t.Chdir(t.TempDir())
	outside := newPluginStage(e)
	outside.locate()
	if inCheckout.loc.Root != want || outside.loc.Root != want {
		t.Fatalf("the install half's directory is %q in a checkout and %q outside one, want the fixed %q",
			inCheckout.loc.Root, outside.loc.Root, want)
	}
	if want == dir {
		t.Fatalf("the fixed directory is the checkout (%s): the stage would read a working directory again", dir)
	}
	// And no claude call was spawned in the checkout: what this stage writes is
	// the machine's plugin, and the directory claude runs in is nothing but a
	// cwd it needs.
	for _, c := range f.Calls(t) {
		if c.Dir == dir {
			t.Errorf("claude ran in the checkout %q: the install half reads no working directory (U6a)", dir)
		}
	}
}

// The state the stage's read has to tell apart (U2, D949): the checkout's own
// project-scope install is at this release and the machine has none at user
// scope, which is the install this stage makes. Read through the checkout's
// question it is a machine at this release — "No changes", no claude call —
// and the machine the user's own session opens has no plugin at all, which is
// what the plan of a run has to show.
func TestInitPluginStageInstallsWhereOnlyTheCheckoutHasAnInstall(t *testing.T) {
	releaseBuild(t, "0.5.1")
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	dir := repoCheckout(t, githubOrigin, "")
	t.Chdir(dir)
	checkoutFile(t, filepath.Join(dir, ".claude", "settings.json"), wiredAt("v0.5.1"))
	checkoutFile(t, filepath.Join(cfg, "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"fugaro@fugaro":[{"scope":"project","version":"0.5.1","projectPath":"`+dir+`"}]}}`)

	e, out := stageEngine(t, "", &initOptions{planOnly: true})
	res := runPlugin(t, e)
	st := res.Stages[0]
	if st.State != initflow.Todo {
		t.Fatalf("the checkout's own install alone reads as %q (%s): the plugin this stage installs is the machine's, and this machine has none (U2)\n%s", st.State, st.Detail, out)
	}
	if !strings.Contains(st.Detail, "installs the plugin in Claude Code") || !strings.Contains(st.Detail, "not installed on this machine") {
		t.Errorf("the plan says %q, which does not say what the machine is missing\n%s", st.Detail, out)
	}
}

// A dev build has no release plugin to install: skipped with the reason, and
// nothing run and nothing written.
func TestInitPluginStageDevBuildSkipped(t *testing.T) {
	settings := wiringCheckout(t, "")
	e, _ := stageEngine(t, "", &initOptions{yes: true})
	res := runPlugin(t, e)
	if st := res.Stages[0]; st.State != initflow.Skipped || !strings.Contains(st.Detail, "no release plugin to install") {
		t.Fatalf("%+v", st)
	}
	if _, err := os.Stat(settings); err == nil {
		t.Error("a dev build wrote the settings")
	}
}

// The plan-only view says what the run will do about the install half. Since U1
// the stage installs the plugin into the user's own Claude Code, and a plan that
// said "No changes" in the very states the run installs it would read as an
// installation that needs nothing (#1836c). The last case is the other
// direction, so the plan does not become a stage that always has something to
// do: a machine already at this release is done and says so.
//
// The fixture carries a settings file with other people's entries, which the
// plan says nothing about: since U6a this stage takes no pin out of it, and the
// leftover a release behind left is `update`'s notice (repo_notices.go).
func TestInitPluginStagePlanOnlySaysTheInstallRuns(t *testing.T) {
	const (
		installedAtThisRelease = `{"version":2,"plugins":{"fugaro@fugaro":[{"scope":"user","version":"0.5.1"}]}}`
		installedBehind        = `{"version":2,"plugins":{"fugaro@fugaro":[{"scope":"user","version":"0.5.0"}]}}`
	)
	for _, tc := range []struct {
		name string
		// installed is what Claude Code records as installed.
		installed string
		want      initflow.State
		wantSay   string
	}{
		{name: "nothing installed on this machine", want: initflow.Todo,
			wantSay: "installs the plugin in Claude Code"},
		{name: "Claude Code's plugin is behind", installed: installedBehind,
			want: initflow.Todo, wantSay: "Claude Code's own plugin is behind"},
		{name: "the installed plugin is this release's", installed: installedAtThisRelease,
			want: initflow.Done, wantSay: "No changes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			releaseBuild(t, "0.5.1")
			cfg := t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", cfg)
			dir := repoCheckout(t, githubOrigin, "")
			t.Chdir(dir)
			checkoutFile(t, filepath.Join(dir, ".claude", "settings.json"), settingsWithOthers)
			if tc.installed != "" {
				checkoutFile(t, filepath.Join(cfg, "plugins", "installed_plugins.json"), tc.installed)
			}

			e, out := stageEngine(t, "", &initOptions{planOnly: true})
			res := runPlugin(t, e)
			if res.Failed != nil {
				t.Fatalf("the plan-only run failed: %+v\n%s", res.Failed, out)
			}
			st := res.Stages[0]
			if st.State != tc.want {
				t.Errorf("a plan-only run shows the plugin stage as %q, want %q: the plan has to say what the run will do about the install, which is the whole of this stage (U1, U2)\n%s", st.State, tc.want, out)
			}
			if !strings.Contains(st.Detail, tc.wantSay) {
				t.Errorf("the plan says %q, which does not say %q\n%s", st.Detail, tc.wantSay, out)
			}
		})
	}
}
