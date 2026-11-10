package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
// ("" for none), and the path of that file.
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

// The wiring merges only after the owner's own confirmation (T7: a person at
// the terminal, nothing typed), and a coding agent's session asks nothing at
// all: the stage is LocalUnderAgent (U1), so the loop applies it there and the
// write — this machine's own file — is made with nothing read off stdin. It
// shows the diff, and merges only the plugin's two entries into the file,
// pinned to the binary's tag.
func TestWiringStageConfirmsAndMergesOnly(t *testing.T) {
	releaseBuild(t, "0.2.0")
	settings := wiringCheckout(t, settingsWithOthers)
	fakeTerminal(t)

	// A coding agent's session asks nothing and writes the pin: "y" on stdin
	// would be a confirmation nobody is there to give, and no `fugaro
	// update-skills` is left behind.
	e, _ := stageEngine(t, "y\n", nil)
	t.Setenv(agentMarkers[0], "1")
	res := runPlugin(t, e)
	got, _ := os.ReadFile(settings)
	if stateOf(res, "plugin") != initflow.Changed || len(res.Left) != 0 || !strings.Contains(string(got), `"ref": "v0.2.0"`) {
		t.Fatalf("agent session: stages %+v left %v\n%s", res.Stages, res.Left, got)
	}
	t.Setenv(agentMarkers[0], "")
	// The checks below read the file as it was before that write.
	if err := os.WriteFile(settings, []byte(settingsWithOthers), 0o644); err != nil {
		t.Fatal(err)
	}

	// Confirmed: the person is at their terminal, so nothing is typed at all
	// — "n" on stdin would decline the step if anything were read.
	e, out := stageEngine(t, "n\n", nil)
	res = runPlugin(t, e)
	if stateOf(res, "plugin") != initflow.Changed || len(res.Left) != 0 {
		t.Fatalf("stages %+v left %v", res.Stages, res.Left)
	}
	// The diff of the file it would write is on the screen before the write:
	// the added marketplace entry is its own line, at the file's own
	// indentation under the diff's marker.
	if !strings.Contains(out.String(), "+ "+`    "fugaro": {`) {
		t.Errorf("the diff was not shown before the write:\n%s", out.String())
	}
	var m map[string]json.RawMessage
	data, _ := os.ReadFile(settings)
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("%v:\n%s", err, data)
	}
	for _, k := range []string{"permissions", "model"} {
		if _, ok := m[k]; !ok {
			t.Errorf("%s lost:\n%s", k, data)
		}
	}
	for _, want := range []string{`"acme@acme": true`, `"acme/tools"`, `"fugaro@fugaro": true`, `"repo": "dimipaun/fugaro"`, `"ref": "v0.2.0"`, `Bash(go test:*)`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("the merged file lacks %s:\n%s", want, data)
		}
	}
	// The closing note is the converge's own (pluginWired) and not the
	// update-skills one: this run IS `fugaro update`, and its install half —
	// which has no claude to run on this rig — is the step that note would
	// name, with the commands to run it by hand printed on the step's own line
	// above (/plugin marketplace add …#v0.2.0, the claude-less fallback).
	for _, want := range []string{"Updated", "git diff", "was not installed by this run", "Claude Code installs the plugin", "only trust folders you trust"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the output lacks %q:\n%s", want, out.String())
		}
	}
	for _, bad := range []string{pluginFirstRun, pluginRefresh} {
		if strings.Contains(out.String(), bad) {
			t.Errorf("the run prints the update-skills note %q, which sends its own reader to the command it is running:\n%s", bad, out.String())
		}
	}
	// The second run has nothing to do, and says so.
	e, _ = stageEngine(t, "", nil)
	res = runPlugin(t, e)
	if st := res.Stages[0]; st.State != initflow.Done || !strings.Contains(st.Detail, "No changes") {
		t.Errorf("rerun: %+v", st)
	}
	// --yes confirms without a question.
	if err := os.WriteFile(settings, []byte(settingsWithOthers), 0o644); err != nil {
		t.Fatal(err)
	}
	e, out = stageEngine(t, "", &initOptions{yes: true})
	if res = runPlugin(t, e); stateOf(res, "plugin") != initflow.Changed || strings.Contains(out.String(), "[y/N]") || !strings.Contains(out.String(), "confirmed by --yes") {
		t.Errorf("--yes: %+v\n%s", res.Stages, out.String())
	}
}

// Without a terminal and without --yes nothing is asked and nothing is
// written: the loop refuses to go on.
func TestWiringStageNeedsConfirmation(t *testing.T) {
	releaseBuild(t, "0.2.0")
	settings := wiringCheckout(t, settingsWithOthers)
	e, _ := stageEngine(t, "y\n", nil)
	_, err := initflow.Run(t.Context(), []initflow.Stage{newPluginStage(e)}, e.options())
	var nt *initflow.NoTerminalError
	if !errors.As(err, &nt) {
		t.Fatalf("err %v", err)
	}
	if got, _ := os.ReadFile(settings); string(got) != settingsWithOthers {
		t.Errorf("written without a confirmation:\n%s", got)
	}
	// The plan-only run shows the diff and writes nothing.
	e, out := stageEngine(t, "", &initOptions{planOnly: true})
	res := runPlugin(t, e)
	if got, _ := os.ReadFile(settings); string(got) != settingsWithOthers || stateOf(res, "plugin") != initflow.Todo || !strings.Contains(out.String(), "fugaro@fugaro") {
		t.Errorf("plan-only: %+v\n%s", res.Stages, out.String())
	}
}

// Outside a checkout there is nothing to write: the settings to add are
// printed, once, and the stage is skipped.
func TestWiringStageOutsideCheckoutPrintsSnippet(t *testing.T) {
	releaseBuild(t, "0.2.0")
	t.Chdir(t.TempDir())
	e, out := stageEngine(t, "", &initOptions{yes: true})
	res := runPlugin(t, e)
	if st := res.Stages[0]; st.State != initflow.Skipped || !strings.Contains(st.Detail, "not in a checkout") {
		t.Fatalf("%+v", st)
	}
	if strings.Count(out.String(), `"fugaro@fugaro": true`) != 1 || !strings.Contains(out.String(), `"ref": "v0.2.0"`) || !strings.Contains(out.String(), ".claude/settings.json") {
		t.Errorf("the snippet:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(".claude", "settings.json")); err == nil {
		t.Error("a settings file was written outside a checkout")
	}
}

// A dev build has no tag to pin to: skipped with the reason, nothing written.
func TestWiringStageDevBuildSkipped(t *testing.T) {
	settings := wiringCheckout(t, "")
	e, _ := stageEngine(t, "", &initOptions{yes: true})
	res := runPlugin(t, e)
	if st := res.Stages[0]; st.State != initflow.Skipped || !strings.Contains(st.Detail, "no release tag") {
		t.Fatalf("%+v", st)
	}
	if _, err := os.Stat(settings); err == nil {
		t.Error("a dev build wrote the settings")
	}
}

// A marketplace that is not Fugaro's is never moved without --allow-fork; a
// file that is not JSON is never rewritten; both are the user's.
func TestWiringStageLeavesWhatIsNotItsOwn(t *testing.T) {
	releaseBuild(t, "0.2.0")
	fork := `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"github","repo":"acme/fugaro-fork","ref":"v0.1.0"}}},"enabledPlugins":{"fugaro@fugaro":true}}` + "\n"
	settings := wiringCheckout(t, fork)
	e, _ := stageEngine(t, "", &initOptions{yes: true})
	res := runPlugin(t, e)
	if got, _ := os.ReadFile(settings); string(got) != fork || stateOf(res, "plugin") != initflow.NeedsYou || len(res.Left) != 1 ||
		!strings.Contains(res.Left[0].Text, "--allow-fork") || !strings.Contains(res.Stages[0].Detail, "acme/fugaro-fork") {
		t.Fatalf("fork: %+v left %v\n%s", res.Stages, res.Left, got)
	}
	// With --allow-fork only its ref moves.
	e, _ = stageEngine(t, "", &initOptions{yes: true, allowFork: true})
	res = runPlugin(t, e)
	got, _ := os.ReadFile(settings)
	if stateOf(res, "plugin") != initflow.Changed || !strings.Contains(string(got), "acme/fugaro-fork") || !strings.Contains(string(got), `"v0.2.0"`) || strings.Contains(string(got), "dimipaun") {
		t.Fatalf("allow-fork: %+v\n%s", res.Stages, got)
	}
	// Invalid JSON: named, untouched.
	if err := os.WriteFile(settings, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	e, _ = stageEngine(t, "", &initOptions{yes: true})
	res = runPlugin(t, e)
	got, _ = os.ReadFile(settings)
	if string(got) != "{not json" || stateOf(res, "plugin") != initflow.NeedsYou || !strings.Contains(res.Stages[0].Detail, "settings.json") || len(res.Left) != 1 || strings.Contains(res.Left[0].Text, "\n") {
		t.Fatalf("invalid: %+v left %v\n%s", res.Stages, res.Left, got)
	}
}

// A fugaro.yaml that cannot be read (a symlink, a FIFO, an oversize file) is
// unknown, not a pass: the stage cannot tell the checkout is this project's,
// so nothing is wired and the user is told why. A missing one is the
// first-run flow and is wired.
func TestWiringStageUnreadableConfigIsUnknown(t *testing.T) {
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
				t.Errorf("wired in a checkout whose fugaro.yaml could not be read:\n%s", got)
			}
			if stateOf(res, "plugin") != initflow.NeedsYou || len(res.Left) != 1 || !strings.Contains(res.Stages[0].Detail, "fugaro.yaml") {
				t.Fatalf("%+v left %+v", res.Stages, res.Left)
			}
		})
	}
}

func make1MiB() []byte { return []byte(strings.Repeat("#", maxFugaroYAML+2)) }

// A subdirectory of a checkout is the checkout: the stage reads the working
// directory the command was typed in and walks up to the one .git is in
// (pluginwire.Locate), so the checkout's own .claude/settings.json is the file
// that gets the pin and nothing is written on the way there. The step that
// moved into this stage ran in whatever directory the command was typed in
// (U1 of docs/plugin-user-scope/implementation-plan.md), and this is the
// assertion that the place it writes is the checkout's and not the caller's.
// Its one residue: the walk stops at $HOME, so a checkout above the user's own
// home directory is out of reach (reconciliation.md F12).
func TestWiringStageWiresTheCheckoutFromASubdirectory(t *testing.T) {
	releaseBuild(t, "0.2.0")
	dir := repoCheckout(t, githubOrigin, "")
	settings := filepath.Join(dir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(settingsWithOthers), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "src", "deep", "down")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)

	e, out := stageEngine(t, "", &initOptions{yes: true})
	res := runPlugin(t, e)
	if stateOf(res, "plugin") != initflow.Changed {
		t.Fatalf("from a subdirectory: %+v\n%s", res.Stages, out)
	}
	got, _ := os.ReadFile(settings)
	if !strings.Contains(string(got), `"ref": "v0.2.0"`) {
		t.Errorf("the checkout's own settings were not wired from a subdirectory:\n%s", got)
	}
	for _, p := range []string{filepath.Join(sub, ".claude"), filepath.Join(dir, "src", ".claude")} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s was made: the stage writes the checkout's own settings file and nothing between it and the caller", p)
		}
	}
}

// A linked worktree is a checkout of its own, and the stage wires that one:
// .git is a file there rather than a directory, which the walk stops on all the
// same (os.Lstat), so the file that gets the pin is the worktree's own and the
// checkout it was made from is left alone (reconciliation.md F14).
func TestWiringStageWiresALinkedWorktree(t *testing.T) {
	releaseBuild(t, "0.2.0")
	main := repoCheckout(t, githubOrigin, "")
	linked := filepath.Join(t.TempDir(), "scratch")
	testutil.Git(t, main, "worktree", "add", "--quiet", linked)
	if fi, err := os.Lstat(filepath.Join(linked, ".git")); err != nil || fi.IsDir() {
		t.Fatalf("the fixture is not a linked worktree: %v %v", fi, err)
	}
	t.Chdir(linked)

	e, out := stageEngine(t, "", &initOptions{yes: true})
	res := runPlugin(t, e)
	if stateOf(res, "plugin") != initflow.Changed {
		t.Fatalf("in a linked worktree: %+v\n%s", res.Stages, out)
	}
	got, err := os.ReadFile(filepath.Join(linked, ".claude", "settings.json"))
	if err != nil || !strings.Contains(string(got), `"ref": "v0.2.0"`) {
		t.Errorf("the worktree's own settings were not wired (err %v):\n%s", err, got)
	}
	if _, err := os.Stat(filepath.Join(main, ".claude", "settings.json")); err == nil {
		t.Errorf("the pin was written in the checkout the worktree was made from, which this command is not in")
	}
}

// The plan-only view says what the run will do about the install half. Since U1
// the stage installs the plugin into the user's own Claude Code, and a plan that
// read only the wiring half reported the stage as done — "No changes" — in the
// very states the run installs it: the pin already this release's with nothing
// installed, the pin current with Claude Code's own record behind (#1836c), and
// a wiring half that is skipped where the install half is not (the plugin is
// one per machine, the wiring is per checkout). The last case is the other
// direction, so the plan does not become a stage that always has something to
// do: a checkout at this release, installed, is done and says so.
func TestPluginStagePlanOnlySaysTheInstallRuns(t *testing.T) {
	const (
		installedAtThisRelease = `{"version":2,"plugins":{"fugaro@fugaro":[{"scope":"user","version":"0.5.1"}]}}`
		installedBehind        = `{"version":2,"plugins":{"fugaro@fugaro":[{"scope":"user","version":"0.5.0"}]}}`
	)
	for _, tc := range []struct {
		name string
		// noOrigin leaves the checkout without a remote, which is the wiring
		// half's own skip (the install half is the machine's, not the
		// checkout's); installed is what Claude Code records as installed.
		noOrigin  bool
		installed string
		want      initflow.State
		wantSay   string
	}{
		{name: "the pin is this release's and nothing is installed", want: initflow.Todo,
			wantSay: "installs the plugin in Claude Code"},
		{name: "the pin is this release's and Claude Code's plugin is behind", installed: installedBehind,
			want: initflow.Todo, wantSay: "Claude Code's own plugin is behind"},
		{name: "the wiring half is skipped and the install half is not", noOrigin: true,
			want: initflow.Todo, wantSay: "installs the plugin in Claude Code"},
		{name: "the pin and the installed plugin are this release's", installed: installedAtThisRelease,
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
				t.Errorf("a plan-only run shows the plugin stage as %q, want %q: the plan has to say what the run will do about the install half, which is a step of this stage (U1)\n%s", st.State, tc.want, out)
			}
			if !strings.Contains(st.Detail, tc.wantSay) {
				t.Errorf("the plan says %q, which does not say %q\n%s", st.Detail, tc.wantSay, out)
			}
		})
	}
}
