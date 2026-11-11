package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/claudeplugin"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/testutil"
)

const settingsWithOthers = `{
  "permissions": { "allow": ["Bash(go test:*)"] },
  "extraKnownMarketplaces": { "acme": { "source": { "source": "github", "repo": "acme/tools" } } },
  "enabledPlugins": { "acme@acme": true },
  "model": "sonnet"
}
`

// pinnedAmongOthers is a checkout the release before this one left: the two
// entries the merge wrote, beside a repository's own marketplace, its own
// plugin and its own keys — in the layout that merge wrote the whole file in
// (what pluginwire's pretty renders, which is what the removal re-renders the
// file as), so the lines nothing asked about have to come out as the bytes they
// were.
const pinnedAmongOthers = `{
  "permissions": {
    "allow": [
      "Bash(go test:*)"
    ]
  },
  "extraKnownMarketplaces": {
    "acme": {
      "source": {
        "source": "github",
        "repo": "acme/tools"
      }
    },
    "fugaro": {
      "source": {
        "source": "github",
        "repo": "dimipaun/fugaro",
        "ref": "v0.1.0"
      }
    }
  },
  "enabledPlugins": {
    "acme@acme": true,
    "fugaro@fugaro": true
  },
  "model": "sonnet"
}
`

// pinnedAmongOthersGone is that file after the removal, byte for byte: the two
// entries gone, their parent keys kept because each still holds something, and
// only the commas the removal leaves behind changed.
const pinnedAmongOthersGone = `{
  "permissions": {
    "allow": [
      "Bash(go test:*)"
    ]
  },
  "extraKnownMarketplaces": {
    "acme": {
      "source": {
        "source": "github",
        "repo": "acme/tools"
      }
    }
  },
  "enabledPlugins": {
    "acme@acme": true
  },
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

// A pin the removal cannot call its own is left exactly as it is, with the
// sentence for the condition, and it is not a failure: the install half goes to
// the machine's own marketplace (dimipaun/fugaro) whatever the file says, the
// file comes out of the run byte for byte as it was, and the run goes on to the
// next stage (spec §8, U5).
func TestInitPluginStageLeavesAPinItCannotRemoveAlone(t *testing.T) {
	for name, tc := range map[string]struct{ content, said string }{
		"a fork's marketplace": {
			content: forkAt("acme/fugaro-fork", "v0.1.0") + "\n",
			said:    "names the repository acme/fugaro-fork, not dimipaun/fugaro",
		},
		"a file that is not JSON": {
			content: "{not json",
			said:    "is not usable settings",
		},
		"a ref that is not a release tag": {
			content: forkAt(pluginwire.Repo, "main") + "\n",
			said:    `is pinned to "main", which is not a release tag vX.Y.Z`,
		},
		"a plugin the repository disabled": {
			content: `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"github","repo":"dimipaun/fugaro","ref":"v0.1.0"}}},"enabledPlugins":{"fugaro@fugaro":false}}`,
			said:    "is disabled by the repository",
		},
	} {
		t.Run(name, func(t *testing.T) {
			releaseBuild(t, "0.2.0")
			t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
			f := testutil.NewClaudePluginFake(t, "[]", "[]")
			f.On(t, "plugins", userInstall("0.2.0"), argsInstall...)
			settings := wiringCheckout(t, tc.content)
			e, out := stageEngine(t, "", &initOptions{yes: true})
			useFakeClaude(t, f)
			res := runPlugin(t, e)
			if got, _ := os.ReadFile(settings); string(got) != tc.content {
				t.Errorf("the stage rewrote %s:\n%s", settings, got)
			}
			// The sentence goes on the stage's own line, which the run prints
			// with the plan, and it is not a stop: the install half is what the
			// run came to this checkout for and it still happens (spec §8).
			if seen := out.String(); !strings.Contains(seen, "the pin is left as it is: ") || !strings.Contains(seen, tc.said) {
				t.Errorf("the run does not say %q about the pin it may not remove\n%s", tc.said, seen)
			}
			if res.Failed != nil || len(res.Left) != 0 {
				t.Errorf("a pin that cannot be removed is not a failure: failed %+v left %+v", res.Failed, res.Left)
			}
			if stateOf(res, "plugin") != initflow.Changed ||
				!slices.ContainsFunc(callArgs(f.Calls(t)), func(a []string) bool { return slices.Equal(a, addUser("0.2.0")) }) {
				t.Errorf("the machine's own install did not happen: %+v\n%s", res.Stages, out)
			}
		})
	}
}

// U5's migration, and the reason no release may carry U2 without it (#2235): a
// checkout the release before this one pinned converges under fugaro update
// alone, and the staleness line every command printed about it — the one U2
// could not clear — is gone when the run ends. The two entries go, every other
// key and entry comes out byte for byte as it was, nothing is committed, and a
// second run finds no pin left to take out (spec §8).
//
// The machine's own install is already this release's, which is the state the
// migration exists for: a repository whose teammates have run fugaro update has
// the pin and nothing else to do about it. The removal is then the whole of the
// stage's work, and that is not this run moving the checkout — no job image is
// rebuilt for taking two entries out of a settings file.
func TestInitPluginStageRemovesThePinAnOlderFugaroLeft(t *testing.T) {
	const release = "0.5.1"
	releaseBuild(t, release)
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.On(t, "plugins", userInstall(release), argsInstall...)
	settings := wiringCheckout(t, pinnedAmongOthers)
	checkoutFile(t, filepath.Join(cfg, "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"fugaro@fugaro":[{"id":"fugaro@fugaro","version":"`+release+`","scope":"user","enabled":true}]}}`)

	// The line the update has to clear, before the run: .claude/settings.json
	// pins the release before this one, and this binary is the next one.
	if line := staleLine(pluginwire.Status(settings, release, installedPlugins())); !strings.Contains(line, "pinned in .claude/settings.json is 0.1.0, this is 0.5.1") {
		t.Fatalf("the fixture is not a stale pin, so the run cannot show it clearing one: %q", line)
	}

	e, out := stageEngine(t, "", &initOptions{yes: true})
	useFakeClaude(t, f)
	res := runPlugin(t, e)
	st := res.Stages[0]
	if st.State != initflow.Changed || !strings.Contains(st.Detail, "removed the "+pluginwire.PluginID+" pin from") {
		t.Fatalf("the stage reads %+v, want the pin taken out:\n%s", st, out)
	}
	// The diff is the two entries going and the commas behind them, and the
	// sentence about what the repository loses is printed with it.
	for _, want := range []string{
		`  -     "fugaro": {`,
		`  -         "ref": "v0.1.0"`,
		`  -     "fugaro@fugaro": true`,
		removalLoss(),
		"review it with git diff and commit it like any change",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the run does not print %q\n%s", want, out)
		}
	}
	if got := upgradeRead(t, settings); got != pinnedAmongOthersGone {
		t.Errorf("the checkout after the removal:\n%s\nwant:\n%s", got, pinnedAmongOthersGone)
	}
	// The checkout is not moved by this: the plugin this release installs was
	// already the machine's, so nothing here is a reason to rebuild a job image.
	if e.checkoutBehind {
		t.Errorf("taking the pin out alone put the checkout behind, and the flag buys a billable image rebuild (U5):\n%s", out)
	}
	// The line every command printed about the checkout, and the whole of the
	// reason this task is the gate U2 waits behind, is clear.
	if line := staleLine(pluginwire.Status(settings, release, installedPlugins())); line != "" {
		t.Errorf("the run left the staleness line it exists to clear: %q", line)
	}

	// Nothing is removed twice (spec §8): a second run has no pin to offer, and
	// the file it wrote is not written again.
	e, out = stageEngine(t, "", &initOptions{yes: true})
	useFakeClaude(t, f)
	res = runPlugin(t, e)
	if d := res.Stages[0].Detail; strings.Contains(d, "removes the "+pluginwire.PluginID+" pin") {
		t.Errorf("a second run offers the removal of a pin that is gone: %q", d)
	}
	if got := upgradeRead(t, settings); got != pinnedAmongOthersGone {
		t.Errorf("the second run rewrote the file:\n%s", got)
	}
}

// U5 in a coding agent's session: the same run, the same --yes, and the diff is
// printed while nothing is written. The pin is a write into a file the
// repository commits, it is the user's to decide, and no flag or environment
// variable gets around the refusal (spec §8) — the marker is read first, before
// --yes, and nothing unsets it. What the session may do it does: the install
// half still runs, so the machine's plugin reaches this release either way.
func TestInitPluginStageRemovalWritesNothingInAnAgentSession(t *testing.T) {
	const release = "0.5.1"
	releaseBuild(t, release)
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.On(t, "plugins", userInstall(release), argsInstall...)
	settings := wiringCheckout(t, pinnedAmongOthers)
	checkoutFile(t, filepath.Join(cfg, "plugins", "installed_plugins.json"),
		`{"version":2,"plugins":{"fugaro@fugaro":[{"id":"fugaro@fugaro","version":"`+release+`","scope":"user","enabled":true}]}}`)
	e, out := stageEngine(t, "", &initOptions{yes: true})
	useFakeClaude(t, f)
	// The coding agent's session, marked after stageEngine cleared the markers:
	// the run's own read of them is what refuses the write.
	t.Setenv("CLAUDECODE", "1")

	res := runPlugin(t, e)
	if got := upgradeRead(t, settings); got != pinnedAmongOthers {
		t.Errorf("a coding agent's session wrote %s:\n%s", settings, got)
	}
	for _, want := range []string{
		`  -     "fugaro": {`,
		`  -         "ref": "v0.1.0"`,
		removalLoss(),
		initflow.AgentRefusal("CLAUDECODE"),
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the session does not print %q\n%s", want, out)
		}
	}
	if len(res.Left) != 1 || res.ExitCode() != 1 {
		t.Fatalf("the removal was not left for the user: left %+v, exit %d\n%s", res.Left, res.ExitCode(), out)
	}
	// The line to run is the one the removal is in — `fugaro update`, which the
	// user's own terminal runs — and not the machine-side route.
	if l := res.Left[0]; l.Stage != initflow.Plugin || !slices.Contains(l.Commands, selfCommand()+" update") {
		t.Errorf("the session hands back %+v, want the command that takes the pin out", l)
	}
	// What the session may do, it does: the machine's plugin is installed.
	if stateOf(res, "plugin") != initflow.Changed ||
		!slices.ContainsFunc(callArgs(f.Calls(t)), func(a []string) bool { return slices.Equal(a, addUser(release)) }) {
		t.Errorf("the install half was left behind with the write it may not make: %+v\n%s", res.Stages, out)
	}
}

// The project-scope install a checkout can carry shadows the machine's own in
// a session opened there (claudeplugin.Effective), and the removal has nothing
// to do with it: it is Claude Code's own record of what it installed, written
// by Claude Code when the folder was trusted at the pin the checkout carried,
// and no fugaro code writes that file. So the removal takes the two entries out
// of .claude/settings.json and leaves the record exactly as it was — and the
// install that applies in this checkout is still the project-scope one, which
// is a leftover of the pin and not this stage's to take out. Reported, not
// fixed (U5): pruning it would be a write into somebody else's file.
func TestInitPluginStageRemovalLeavesTheProjectScopeInstallAlone(t *testing.T) {
	const release = "0.5.1"
	releaseBuild(t, release)
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.On(t, "plugins", userInstall(release), argsInstall...)
	settings := wiringCheckout(t, pinnedAmongOthers)
	dir := filepath.Dir(filepath.Dir(settings))
	record := `{"version":2,"plugins":{"fugaro@fugaro":[` +
		`{"id":"fugaro@fugaro","version":"0.1.0","scope":"project","projectPath":"` + dir + `"},` +
		`{"id":"fugaro@fugaro","version":"` + release + `","scope":"user","enabled":true}]}}`
	recordPath := filepath.Join(cfg, "plugins", "installed_plugins.json")
	checkoutFile(t, recordPath, record)

	e, out := stageEngine(t, "", &initOptions{yes: true})
	useFakeClaude(t, f)
	res := runPlugin(t, e)
	if st := res.Stages[0]; st.State != initflow.Changed || !strings.Contains(st.Detail, "removed the "+pluginwire.PluginID+" pin from") {
		t.Fatalf("the stage reads %+v, want the pin taken out:\n%s", st, out)
	}
	if got := upgradeRead(t, settings); got != pinnedAmongOthersGone {
		t.Errorf("the checkout after the removal:\n%s", got)
	}
	if got := upgradeRead(t, recordPath); got != record {
		t.Errorf("the removal rewrote Claude Code's own record of installed plugins:\n%s\nwant:\n%s", got, record)
	}
	// The shadow is still there after the pin is gone: the install that applies
	// in this checkout is the project-scope one, which is what a session opened
	// here loads.
	var file struct {
		Plugins map[string][]claudeplugin.Install `json:"plugins"`
	}
	if err := json.Unmarshal([]byte(record), &file); err != nil {
		t.Fatal(err)
	}
	if got := claudeplugin.Effective(dir, file.Plugins[pluginwire.PluginID]); got != "0.1.0" {
		t.Errorf("the install that applies in the checkout is %q, want the project-scope 0.1.0 the pin left behind", got)
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
//
// The fixture carries no pin, so the install half's own line is the whole of
// what the plan says: a pin beside it is U5's removal, and the tests below read
// that.
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
			checkoutFile(t, filepath.Join(dir, ".claude", "settings.json"), settingsWithOthers)
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
