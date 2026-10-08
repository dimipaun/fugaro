package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

const ourMarket = `[{"name":"fugaro","source":"github","repo":"dimipaun/fugaro"}]`

func userInstall(v string) string {
	return `[{"id":"fugaro@fugaro","version":"` + v + `","scope":"user","enabled":true}]`
}

var (
	argsListMarkets = []string{"plugin", "marketplace", "list", "--json"}
	argsListPlugins = []string{"plugin", "list", "--json"}
	argsAdd         = []string{"plugin", "marketplace", "add", "--scope", "user", "dimipaun/fugaro"}
	argsUpdMarket   = []string{"plugin", "marketplace", "update", "fugaro"}
	argsInstall     = []string{"plugin", "install", "fugaro@fugaro", "--scope", "user"}
	argsUpdProject  = []string{"plugin", "update", "fugaro@fugaro", "--scope", "project"}
)

// useFakeClaude makes f the claude the plugin step finds (upgradeEnv restores
// the lookup).
func useFakeClaude(t *testing.T, f *testutil.ClaudePluginFake) {
	t.Helper()
	claudeLookPath = func(name string) (string, error) {
		if name != "claude" {
			return "", exec.ErrNotFound
		}
		return f.Bin(), nil
	}
}

func callArgs(calls []testutil.ClaudeCall) [][]string {
	var out [][]string
	for _, c := range calls {
		out = append(out, c.Args)
	}
	return out
}

func TestUpgradePluginInstallsWithExactArgv(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.On(t, "plugins", userInstall("0.5.2"), argsInstall...)
	useFakeClaude(t, f)
	root, _ := skillCheckout(t, wiredAt("v0.5.2"))
	out, _, err := executeStdin(t, "", "upgrade", "--local", root)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	calls := f.Calls(t)
	want := [][]string{argsListMarkets, argsListPlugins, argsAdd, argsInstall, argsListPlugins}
	if !slices.EqualFunc(callArgs(calls), want, slices.Equal[[]string]) {
		t.Fatalf("calls %q, want %q", callArgs(calls), want)
	}
	// User-scope calls never need the checkout (claudeplugin.needsCheckout):
	// they run in a fresh empty directory, so an owner/name on the command
	// line can never collide with a local directory of that name in root.
	for _, c := range calls {
		if testutil.SamePath(c.Dir, root) {
			t.Errorf("a user-scope call ran in the checkout %s; only a project or local scope update should", root)
		}
	}
	for _, w := range []string{"using claude at " + f.Bin(), "running: " + f.Bin() + " plugin install fugaro@fugaro --scope user",
		"plugin: done: installed 0.5.2; restart running Claude Code sessions to apply it"} {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "claude changed") {
		t.Errorf("the settings note fired though claude changed nothing:\n%s", out)
	}
}

func TestUpgradePluginCurrentRunsOnlyTheLists(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	f := testutil.NewClaudePluginFake(t, ourMarket, userInstall("0.5.2"))
	useFakeClaude(t, f)
	root, _ := skillCheckout(t, wiredAt("v0.5.2"))
	// Task 7 (a parallel PR) has not landed: the cloud step is still the
	// "not implemented" placeholder, so a checkout with pin and plugin both
	// current is not "nothing to do" yet (TestUpgradeUnimplementedStepsAreNeverCurrent),
	// and the run ends with a "not checked: cloud" line instead.
	out, _, err := executeStdin(t, "", "upgrade", "--local", root)
	if err != nil || len(f.Changes(t)) != 0 || !strings.Contains(out, "plugin: current: installed 0.5.2") || !strings.Contains(out, "not checked: cloud") || strings.Contains(out, "nothing to do") {
		t.Fatalf("%v, changes %q\n%s", err, f.Changes(t), out)
	}
	if strings.Contains(out, "claude changed") {
		t.Errorf("the settings note fired though no change call ran:\n%s", out)
	}
}

func TestUpgradePluginUpdatesThisCheckoutsProjectInstall(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	root, _ := skillCheckout(t, wiredAt("v0.5.2"))
	plugins := `[{"id":"fugaro@fugaro","version":"0.5.2","scope":"user"},{"id":"fugaro@fugaro","version":"0.5.1","scope":"project","projectPath":"` + root + `"}]`
	f := testutil.NewClaudePluginFake(t, ourMarket, plugins)
	f.On(t, "plugins", strings.Replace(plugins, `"0.5.1"`, `"0.5.2"`, 1), argsUpdProject...)
	useFakeClaude(t, f)
	out, _, err := executeStdin(t, "", "upgrade", "--local", root)
	if want := [][]string{argsUpdMarket, argsUpdProject}; err != nil || !slices.EqualFunc(f.Changes(t), want, slices.Equal[[]string]) {
		t.Fatalf("%v, changes %q, want %q\n%s", err, f.Changes(t), want, out)
	}
	// The project-scope update is the one call claudeplugin.needsCheckout
	// says acts on the checkout, so it alone runs there.
	for _, c := range f.Calls(t) {
		if slices.Equal(c.Args, argsUpdProject) != testutil.SamePath(c.Dir, root) {
			t.Errorf("call %q ran in %s, checkout is %s", c.Args, c.Dir, root)
		}
	}
}

func TestUpgradePluginNeverReplacesAnotherMarketplace(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	f := testutil.NewClaudePluginFake(t, `[{"name":"fugaro","source":"github","repo":"someone/else"}]`, "[]")
	useFakeClaude(t, f)
	root, _ := skillCheckout(t, wiredAt("v0.5.2"))
	out, _, err := executeStdin(t, "", "upgrade", "--local", root)
	if ExitCode(err) != ExitUserError || len(f.Changes(t)) != 0 || !strings.Contains(out, "never replaces it") {
		t.Fatalf("exit %d, changes %q\n%s", ExitCode(err), f.Changes(t), out)
	}
}

func TestUpgradePluginFailureStopsTheCheckout(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	f := testutil.NewClaudePluginFake(t, ourMarket, "[]")
	f.On(t, "fail", "network down\n", argsInstall...)
	useFakeClaude(t, f)
	root, _ := skillCheckout(t, wiredAt("v0.5.2"))
	out, _, err := executeStdin(t, "", "upgrade", "--local", root)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d\n%s", ExitCode(err), out)
	}
	for _, w := range []string{"plugin: failed: claude plugin install fugaro@fugaro --scope user failed: exit status 1", "claude: network down",
		"  " + root + ": pin current, plugin failed", "once that is fixed, rerun: fugaro upgrade --local " + root} {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
}

func TestUpgradePluginWithoutClaude(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	root, _ := skillCheckout(t, wiredAt("v0.5.2"))
	out, _, err := executeStdin(t, "", "upgrade", "--local", root)
	if err != nil || !strings.Contains(out, "plugin: skipped: claude is not on PATH") || !strings.Contains(out, "/plugin install fugaro@fugaro") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestUpgradeCheckRunsNoClaude(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	f := testutil.NewClaudePluginFake(t, ourMarket, userInstall("0.5.1"))
	useFakeClaude(t, f)
	record := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "plugins", "installed_plugins.json")
	if err := os.MkdirAll(filepath.Dir(record), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(record, []byte(`{"version":2,"plugins":{"fugaro@fugaro":[{"scope":"user","version":"0.5.1"}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	root, _ := skillCheckout(t, wiredAt("v0.5.2"))
	out, _, err := executeStdin(t, "", "upgrade", "--check", root)
	if ExitCode(err) != ExitUserError || len(f.Calls(t)) != 0 || !strings.Contains(out, "plugin: stale: installed 0.5.1, this fugaro is 0.5.2") {
		t.Fatalf("exit %d, calls %+v\n%s", ExitCode(err), f.Calls(t), out)
	}
}

// TestUpgradePluginNotesSettingsChangedByClaude: only a project or local
// scope update runs in the checkout (claudeplugin.needsCheckout), so that is
// the call that can touch its .claude/settings.json (U2: a project-scope
// install writes enabledPlugins there).
func TestUpgradePluginNotesSettingsChangedByClaude(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	root, settings := skillCheckout(t, wiredAt("v0.5.2"))
	plugins := `[{"id":"fugaro@fugaro","version":"0.5.1","scope":"project","projectPath":"` + root + `"}]`
	f := testutil.NewClaudePluginFake(t, ourMarket, plugins)
	f.On(t, "plugins", strings.Replace(plugins, `"0.5.1"`, `"0.5.2"`, 1), argsUpdProject...)
	f.On(t, "touch", "", argsUpdProject...)
	useFakeClaude(t, f)
	out, _, err := executeStdin(t, "", "upgrade", "--local", root)
	if err != nil || !strings.Contains(out, "claude changed "+settings+": review it with git diff") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestUpgradePluginVersionAfterTheCalls(t *testing.T) {
	for _, tc := range []struct {
		after, want string
		code        int
	}{
		{"0.6.0", "installed 0.6.0, newer than this fugaro 0.5.2", ExitOK},
		{"0.5.1", "after the update the installed plugin is still 0.5.1", ExitUserError},
	} {
		t.Run(tc.after, func(t *testing.T) {
			upgradeEnv(t, "0.5.2")
			f := testutil.NewClaudePluginFake(t, ourMarket, "[]")
			f.On(t, "plugins", userInstall(tc.after), argsInstall...)
			useFakeClaude(t, f)
			root, _ := skillCheckout(t, wiredAt("v0.5.2"))
			out, _, err := executeStdin(t, "", "upgrade", "--local", root)
			if ExitCode(err) != tc.code || !strings.Contains(out, tc.want) {
				t.Fatalf("exit %d\n%s", ExitCode(err), out)
			}
		})
	}
}

func TestUpgradePluginListsThatFailAreFailedSteps(t *testing.T) {
	const garbage = "not json"
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T) *testutil.ClaudePluginFake
	}{
		{"markets", func(t *testing.T) *testutil.ClaudePluginFake {
			return testutil.NewClaudePluginFake(t, garbage, "[]")
		}},
		{"installed", func(t *testing.T) *testutil.ClaudePluginFake {
			return testutil.NewClaudePluginFake(t, ourMarket, garbage)
		}},
		{"relist", func(t *testing.T) *testutil.ClaudePluginFake {
			f := testutil.NewClaudePluginFake(t, ourMarket, "[]")
			f.On(t, "plugins", garbage, argsInstall...)
			return f
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upgradeEnv(t, "0.5.2")
			f := tc.setup(t)
			useFakeClaude(t, f)
			root, _ := skillCheckout(t, wiredAt("v0.5.2"))
			out, _, err := executeStdin(t, "", "upgrade", "--local", root)
			if ExitCode(err) != ExitUserError || !strings.Contains(out, "plugin: failed:") || !strings.Contains(out, "plugin failed") {
				t.Fatalf("exit %d\n%s", ExitCode(err), out)
			}
		})
	}
}

func TestUpgradePluginNothingAppliesAfterTheUpdate(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	f := testutil.NewClaudePluginFake(t, ourMarket, "[]")
	useFakeClaude(t, f) // the install call leaves the list empty
	root, _ := skillCheckout(t, wiredAt("v0.5.2"))
	out, _, err := executeStdin(t, "", "upgrade", "--local", root)
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "plugin: failed: after the update claude lists no install of fugaro@fugaro that applies to this checkout") {
		t.Fatalf("exit %d\n%s", ExitCode(err), out)
	}
}

// TestUpgradeCheckPluginNewerThanFugaro: a plugin newer than the binary is a
// note, not stale (design: U3), so --check exits 0 and names brew upgrade.
func TestUpgradeCheckPluginNewerThanFugaro(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	record := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "plugins", "installed_plugins.json")
	if err := os.MkdirAll(filepath.Dir(record), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(record, []byte(`{"version":2,"plugins":{"fugaro@fugaro":[{"scope":"user","version":"0.6.0"}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	root, _ := skillCheckout(t, wiredAt("v0.5.2"))
	out, _, err := executeStdin(t, "", "upgrade", "--check", root)
	if !strings.Contains(out, "plugin: current: installed 0.6.0, newer than this fugaro 0.5.2 (brew upgrade dimipaun/tap/fugaro)") || strings.Contains(out, "plugin: stale") {
		t.Fatalf("%v\n%s", err, out)
	}
}

// TestUpgradePluginForkRepoIsPrintable: the repo in a committed settings.json
// goes into the summary, so an invalid one is shown escaped, never raw, and
// is never passed to claude (marketRepo's ValidRepo check).
func TestUpgradePluginForkRepoIsPrintable(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	f := testutil.NewClaudePluginFake(t, ourMarket, "[]")
	useFakeClaude(t, f)
	evil := `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"github","repo":"ev\u001b[31mil/x","ref":"v0.5.2"}}},"enabledPlugins":{"fugaro@fugaro":true}}`
	root, _ := skillCheckout(t, evil)
	for _, args := range [][]string{{"upgrade", "--local", "--allow-fork", root}, {"upgrade", "--local", root}} {
		out, _, _ := executeStdin(t, "", args...)
		if strings.ContainsRune(out, 0x1b) {
			t.Errorf("%q: a raw escape reached the output: %q", args, out)
		}
		if !strings.Contains(out, "plugin: skipped:") {
			t.Errorf("%q: plugin step not skipped:\n%s", args, out)
		}
	}
	if len(f.Calls(t)) != 0 {
		t.Errorf("claude ran for an invalid repo: %q", f.Calls(t))
	}
}
