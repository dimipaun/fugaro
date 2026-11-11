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
// ("" for none), and the path of that file. Since U2 nothing in the tree writes
// that file: it is the leftover pin an older fugaro left, which the stage reads
// and never moves.
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
// CLI: fugaro upgrade's own plugin step, whose fixed calls are the whole of what
// this stage runs. A second run finds the marketplace and the install where the
// first left them and has nothing to do.
func TestInitPluginStageInstallsInClaudeCode(t *testing.T) {
	releaseBuild(t, "0.2.0")
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

// A pin an older fugaro left in the checkout is not this stage's to move, and
// neither is a file it cannot read: the install half goes to the machine's own
// marketplace (dimipaun/fugaro) whatever the file says, and the file comes out
// of the run byte for byte as it was. U5 is the task that offers to take a pin
// out.
func TestInitPluginStageLeavesTheCheckoutsSettingsAlone(t *testing.T) {
	for name, content := range map[string]string{
		"a pin an older fugaro left": wiredAt("v0.1.0") + "\n",
		"a fork's marketplace":       forkAt("acme/fugaro-fork", "v0.1.0") + "\n",
		"a file that is not JSON":    "{not json",
	} {
		t.Run(name, func(t *testing.T) {
			releaseBuild(t, "0.2.0")
			f := testutil.NewClaudePluginFake(t, "[]", "[]")
			f.On(t, "plugins", userInstall("0.2.0"), argsInstall...)
			settings := wiringCheckout(t, content)
			e, out := stageEngine(t, "", &initOptions{yes: true})
			useFakeClaude(t, f)
			res := runPlugin(t, e)
			if got, _ := os.ReadFile(settings); string(got) != content {
				t.Errorf("the stage rewrote %s:\n%s", settings, got)
			}
			if stateOf(res, "plugin") != initflow.Changed || !slices.ContainsFunc(callArgs(f.Calls(t)), func(a []string) bool { return slices.Equal(a, addUser("0.2.0")) }) {
				t.Errorf("the machine's own install did not happen: %+v\n%s", res.Stages, out)
			}
		})
	}
}

// A fugaro.yaml that cannot be read (a symlink, a FIFO, an oversize file) is
// unknown, not a pass: the stage cannot tell the checkout is this project's, so
// nothing is installed from here and the user is told why. A missing one is the
// first-run flow, and the gates pass.
func TestInitPluginStageUnreadableConfigIsUnknown(t *testing.T) {
	releaseBuild(t, "0.2.0")
	for name, make := range map[string]func(t *testing.T, p string){
		"a symlink": func(t *testing.T, p string) {
			target := filepath.Join(t.TempDir(), "other.yaml")
			if err := os.WriteFile(target, []byte(checkoutYAML("github", "oauth", "borealis", "")), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, p); err != nil {
				t.Fatal(err)
			}
		},
		"oversize": func(t *testing.T, p string) {
			if err := os.WriteFile(p, make1MiB(), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			settings := wiringCheckout(t, settingsWithOthers)
			make(t, filepath.Join(filepath.Dir(filepath.Dir(settings)), "fugaro.yaml"))
			e, _ := stageEngine(t, "", &initOptions{yes: true})
			res := runPlugin(t, e)
			if got, _ := os.ReadFile(settings); string(got) != settingsWithOthers {
				t.Errorf("the file changed in a checkout whose fugaro.yaml could not be read:\n%s", got)
			}
			if stateOf(res, "plugin") != initflow.NeedsYou || len(res.Left) != 1 || !strings.Contains(res.Stages[0].Detail, "fugaro.yaml") {
				t.Fatalf("%+v left %+v", res.Stages, res.Left)
			}
		})
	}
}

func make1MiB() []byte { return []byte(strings.Repeat("#", maxFugaroYAML+2)) }

// Outside a checkout there is no repository to install the plugin from and no
// directory to run claude in: the stage skips, saying so, and writes nothing
// anywhere.
func TestInitPluginStageOutsideACheckoutSkips(t *testing.T) {
	releaseBuild(t, "0.2.0")
	dir := t.TempDir()
	t.Chdir(dir)
	e, out := stageEngine(t, "", &initOptions{yes: true})
	res := runPlugin(t, e)
	if st := res.Stages[0]; st.State != initflow.Skipped || !strings.Contains(st.Detail, "not in a checkout") {
		t.Fatalf("%+v\n%s", st, out)
	}
	if es, _ := os.ReadDir(dir); len(es) != 0 {
		t.Errorf("wrote into %s: %v", dir, es)
	}
}

// A subdirectory of a checkout is the checkout: the stage reads the working
// directory the command was typed in and walks up to the one .git is in
// (pluginwire.Locate), so the checkout's gates are read and the install runs.
// Nothing is written between the caller and the checkout's top, because nothing
// in the checkout is written at all any more.
func TestInitPluginStageFindsTheCheckoutFromASubdirectory(t *testing.T) {
	releaseBuild(t, "0.2.0")
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.On(t, "plugins", userInstall("0.2.0"), argsInstall...)
	dir := repoCheckout(t, githubOrigin, "")
	sub := filepath.Join(dir, "src", "deep", "down")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)

	e, out := stageEngine(t, "", &initOptions{yes: true})
	useFakeClaude(t, f)
	res := runPlugin(t, e)
	if stateOf(res, "plugin") != initflow.Changed {
		t.Fatalf("from a subdirectory: %+v\n%s", res.Stages, out)
	}
	for _, p := range []string{filepath.Join(sub, ".claude"), filepath.Join(dir, "src", ".claude"), filepath.Join(dir, ".claude")} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s was made: this stage writes nothing in a checkout", p)
		}
	}
}

// A linked worktree is a checkout of its own: .git is a file there rather than
// a directory, which the walk stops on all the same (os.Lstat), so the gates are
// read off the worktree and neither it nor the checkout it was made from gets a
// settings file (reconciliation.md F14).
func TestInitPluginStageFindsALinkedWorktree(t *testing.T) {
	releaseBuild(t, "0.2.0")
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.On(t, "plugins", userInstall("0.2.0"), argsInstall...)
	main := repoCheckout(t, githubOrigin, "")
	linked := filepath.Join(t.TempDir(), "scratch")
	testutil.Git(t, main, "worktree", "add", "--quiet", linked)
	if fi, err := os.Lstat(filepath.Join(linked, ".git")); err != nil || fi.IsDir() {
		t.Fatalf("the fixture is not a linked worktree: %v %v", fi, err)
	}
	t.Chdir(linked)

	e, out := stageEngine(t, "", &initOptions{yes: true})
	useFakeClaude(t, f)
	res := runPlugin(t, e)
	if stateOf(res, "plugin") != initflow.Changed {
		t.Fatalf("in a linked worktree: %+v\n%s", res.Stages, out)
	}
	for _, p := range []string{filepath.Join(linked, ".claude", "settings.json"), filepath.Join(main, ".claude", "settings.json")} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s was written: this stage writes nothing in a checkout", p)
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
func TestInitPluginStagePlanOnlySaysTheInstallRuns(t *testing.T) {
	const (
		installedAtThisRelease = `{"version":2,"plugins":{"fugaro@fugaro":[{"scope":"user","version":"0.5.1"}]}}`
		installedBehind        = `{"version":2,"plugins":{"fugaro@fugaro":[{"scope":"user","version":"0.5.0"}]}}`
	)
	for _, tc := range []struct {
		name string
		// noOrigin leaves the checkout without a remote, which is the gate's
		// own skip: it guards the checkout write U5 adds, and the install half
		// is the machine's, not the checkout's (F24, D955). installed is what
		// Claude Code records as installed.
		noOrigin  bool
		installed string
		want      initflow.State
		wantSay   string
	}{
		{name: "nothing installed on this machine", want: initflow.Todo,
			wantSay: "installs the plugin in Claude Code"},
		{name: "Claude Code's plugin is behind", installed: installedBehind,
			want: initflow.Todo, wantSay: "Claude Code's own plugin is behind"},
		{name: "the checkout's gate is skipped and the install is not", noOrigin: true,
			want: initflow.Todo, wantSay: "installs the plugin in Claude Code"},
		{name: "the installed plugin is this release's", installed: installedAtThisRelease,
			want: initflow.Done, wantSay: "No changes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			releaseBuild(t, "0.5.1")
			cfg := t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", cfg)
			dir := repoCheckout(t, githubOrigin, "")
			t.Chdir(dir)
			checkoutFile(t, filepath.Join(dir, ".claude", "settings.json"), wiredAt("v0.5.1"))
			if tc.noOrigin {
				testutil.Git(t, dir, "remote", "remove", "origin")
			}
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
