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

// ourMarket is Claude Code's record of the fugaro marketplace as fugaro added
// it before this change: declared with no ref, so it follows
// dimipaun/fugaro's default branch.
const ourMarket = `[{"name":"fugaro","source":"github","repo":"dimipaun/fugaro"}]`

// ourMarketAt is that record for a marketplace declared at ref: what the
// plugin step itself writes (claudeplugin's pinned add).
func ourMarketAt(ref string) string {
	return `[{"name":"fugaro","source":"github","repo":"dimipaun/fugaro","ref":"` + ref + `"}]`
}

func userInstall(v string) string {
	return `[{"id":"fugaro@fugaro","version":"` + v + `","scope":"user","enabled":true}]`
}

var (
	argsListMarkets = []string{"plugin", "marketplace", "list", "--json"}
	argsListPlugins = []string{"plugin", "list", "--json"}
	argsAdd         = []string{"plugin", "marketplace", "add", "--scope", "user", "dimipaun/fugaro#v0.5.2"}
	argsRemove      = []string{"plugin", "marketplace", "remove", "fugaro", "--scope", "user"}
	// argsBack is the add that puts back what argsRemove cleared: the plain
	// source releases before this one declared, which is what ourMarket holds.
	argsBack       = []string{"plugin", "marketplace", "add", "--scope", "user", "dimipaun/fugaro"}
	argsUpdMarket  = []string{"plugin", "marketplace", "update", "fugaro"}
	argsInstall    = []string{"plugin", "install", "fugaro@fugaro", "--scope", "user"}
	argsUpdProject = []string{"plugin", "update", "fugaro@fugaro", "--scope", "project"}
)

// refusedAdd is what the real claude answers to an add of a name it has
// already declared from a different source (2.1.283, 2026-11-09): an exit 1
// that names the only way through, which is the clear this step runs first.
const refusedAdd = "Cannot add marketplace \"fugaro\": its network source differs from the one declared for it in settings (kind, target, or a fetch-shaping field such as headers / ref / path / sparsePaths); the source must match the one declared for this name in settings (or change the declaration)."

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

// TestUpgradePluginMovesAnUnpinnedMarketplaceToTheRelease: Claude Code's own
// marketplace on this machine is what the plugin is installed and updated
// from, and the step used to declare it with no ref, so it followed
// dimipaun/fugaro's default branch. On a patch release cut from release/X.Y
// main's plugin.json still carries the version main was last bumped to, so
// `fugaro update` on 0.21.1 (2026-11-09) installed 0.21.0 from it and failed a
// complete update with "the installed plugin is still 0.21.0". The step
// declares the marketplace at this release's tag before anything is installed
// from it, and it clears the old declaration first: claude refuses an add of a
// name it has already declared from a different source, and the fake refuses
// it here too until the clear has run.
func TestUpgradePluginMovesAnUnpinnedMarketplaceToTheRelease(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	f := testutil.NewClaudePluginFake(t, ourMarket, userInstall("0.5.1"))
	f.On(t, "fail", refusedAdd, argsAdd...)
	f.On(t, "rm", "fail."+testutil.ClaudePluginKey(argsAdd...), argsRemove...)
	// What Claude Code serves is decided by the marketplace's ref, so the fake
	// changes the installed version when the pinned add runs — the way the
	// failed update of 2026-11-09 got 0.21.0 from an unpinned marketplace.
	f.On(t, "plugins", userInstall("0.5.2"), argsAdd...)
	f.On(t, "plugins", userInstall("0.5.2"), argsUpdUser...)
	useFakeClaude(t, f)
	root, _ := skillCheckout(t, wiredAt("v0.5.2")) // the checkout's own pin is already right
	out, _, err := executeStdin(t, "", "upgrade", "--local", root)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := [][]string{argsRemove, argsAdd, argsUpdMarket, argsUpdUser}
	if !slices.EqualFunc(f.Changes(t), want, slices.Equal[[]string]) {
		t.Fatalf("changes %q, want %q\n%s", f.Changes(t), want, out)
	}
	if !strings.Contains(out, "plugin: done: installed 0.5.2") {
		t.Errorf("the plugin did not converge on this release's version:\n%s", out)
	}
}

// TestUpgradePluginReportsAMarketplaceItCannotDeclare: claude refuses the add, and
// the clear it refused it by is not fugaro's to make either (a source declared
// for the name in settings fugaro does not own, an organization's, say). The
// plugin cannot be had on this machine, so the step says exactly that and hands
// over the commands, and the update's other steps are not stopped for it.
func TestUpgradePluginReportsAMarketplaceItCannotDeclare(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	f := testutil.NewClaudePluginFake(t, ourMarket, userInstall("0.5.1"))
	f.On(t, "fail", refusedAdd, argsAdd...)
	f.On(t, "fail", "Marketplace 'fugaro' is not declared in user settings. Omit --scope to remove it from all scopes.", argsRemove...)
	useFakeClaude(t, f)
	root, _ := skillCheckout(t, wiredAt("v0.5.2"))
	out, _, err := executeStdin(t, "", "upgrade", "--local", root)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !slices.EqualFunc(f.Changes(t), [][]string{argsRemove, argsAdd}, slices.Equal[[]string]) {
		t.Fatalf("changes %q\n%s", f.Changes(t), out)
	}
	for _, w := range []string{"continuing without clearing", "plugin: skipped: claude would not declare the fugaro marketplace at v0.5.2",
		"/plugin marketplace remove fugaro", "/plugin marketplace add dimipaun/fugaro#v0.5.2"} {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
}

// TestUpgradePluginPutsBackTheDeclarationItsClearRemoved: the clear runs before
// the add because claude refuses an add of a name already declared from another
// source, so an add that then fails must not leave the machine without the
// marketplace it had — and claude's own refusal of the declaration is the only
// add failure the step reports and carries on from: anything else (a clone that
// failed, a network that is down) fails the step.
func TestUpgradePluginPutsBackTheDeclarationItsClearRemoved(t *testing.T) {
	for _, tc := range []struct {
		name, said, want string
		code             int
	}{
		{"a clone that failed", "fatal: unable to access 'https://github.com/dimipaun/fugaro.git/': Could not resolve host",
			"plugin: failed: claude plugin marketplace add --scope user dimipaun/fugaro#v0.5.2 failed: exit status 1", ExitUserError},
		{"claude refusing the declaration", refusedAdd, "plugin: skipped: claude would not declare the fugaro marketplace at v0.5.2", ExitOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upgradeEnv(t, "0.5.2")
			f := testutil.NewClaudePluginFake(t, ourMarket, userInstall("0.5.1"))
			f.On(t, "fail", tc.said, argsAdd...)
			useFakeClaude(t, f)
			root, _ := skillCheckout(t, wiredAt("v0.5.2"))
			out, _, err := executeStdin(t, "", "upgrade", "--local", root)
			if ExitCode(err) != tc.code {
				t.Fatalf("exit %d\n%s", ExitCode(err), out)
			}
			want := [][]string{argsRemove, argsAdd, argsBack}
			if !slices.EqualFunc(f.Changes(t), want, slices.Equal[[]string]) {
				t.Fatalf("changes %q, want %q\n%s", f.Changes(t), want, out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("output lacks %q:\n%s", tc.want, out)
			}
		})
	}
}

// TestUpgradePluginNeverLowersANewerMarketplaceRef: a newer fugaro pinned
// Claude Code's marketplace to a newer tag (U10: a pin is never moved down),
// so the step refreshes that declaration instead of re-declaring it at this
// release's tag, and the newer plugin that comes back is a note, not a failure.
func TestUpgradePluginNeverLowersANewerMarketplaceRef(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	f := testutil.NewClaudePluginFake(t, ourMarketAt("v0.6.0"), userInstall("0.6.0"))
	useFakeClaude(t, f)
	root, _ := skillCheckout(t, wiredAt("v0.5.2"))
	out, _, err := executeStdin(t, "", "upgrade", "--local", root)
	if err != nil || !slices.EqualFunc(f.Changes(t), [][]string{argsUpdMarket, argsUpdUser}, slices.Equal[[]string]) {
		t.Fatalf("%v, changes %q\n%s", err, f.Changes(t), out)
	}
	if !strings.Contains(out, "plugin: done: installed 0.6.0, newer than this fugaro 0.5.2") {
		t.Errorf("a newer pin was not reported as one:\n%s", out)
	}
}

func TestUpgradePluginCurrentRunsOnlyTheLists(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	f := testutil.NewClaudePluginFake(t, ourMarketAt("v0.5.2"), userInstall("0.5.2"))
	useFakeClaude(t, f)
	root, _ := skillCheckout(t, wiredAt("v0.5.2"))
	// pin and plugin current, cloud, rules and policy skipped by --local: nothing to do.
	out, _, err := executeStdin(t, "", "upgrade", "--local", root)
	if err != nil || len(f.Changes(t)) != 0 || !strings.Contains(out, "plugin: current: installed 0.5.2") || !strings.Contains(out, "nothing to do: "+root) {
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
	f := testutil.NewClaudePluginFake(t, ourMarketAt("v0.5.2"), plugins)
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
	f := testutil.NewClaudePluginFake(t, ourMarketAt("v0.5.2"), "[]")
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
	f := testutil.NewClaudePluginFake(t, ourMarketAt("v0.5.2"), userInstall("0.5.1"))
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
	f := testutil.NewClaudePluginFake(t, ourMarketAt("v0.5.2"), plugins)
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
			f := testutil.NewClaudePluginFake(t, ourMarketAt("v0.5.2"), "[]")
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
			return testutil.NewClaudePluginFake(t, ourMarketAt("v0.5.2"), garbage)
		}},
		{"relist", func(t *testing.T) *testutil.ClaudePluginFake {
			f := testutil.NewClaudePluginFake(t, ourMarketAt("v0.5.2"), "[]")
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
	f := testutil.NewClaudePluginFake(t, ourMarketAt("v0.5.2"), "[]")
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
	f := testutil.NewClaudePluginFake(t, ourMarketAt("v0.5.2"), "[]")
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
