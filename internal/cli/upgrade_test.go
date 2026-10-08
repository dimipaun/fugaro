package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// upgradeEnv is a hermetic machine for fugaro upgrade at version v: no coding
// agent's session, nothing but the system tools on PATH, and empty Claude Code
// and Fugaro user configs.
func upgradeEnv(t *testing.T, v string) {
	t.Helper()
	withVersion(t, v)
	useSelf(t)
	for _, k := range agentMarkers {
		t.Setenv(k, "")
	}
	t.Setenv("PATH", strings.Join([]string{t.TempDir(), "/usr/bin", "/bin"}, string(os.PathListSeparator)))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func upgradeRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

func TestUpgradeLocalPinsThenPinCurrent(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	root, settings := skillCheckout(t, wiredAt("v0.5.1"))
	out, _, err := executeStdin(t, "", "upgrade", "--local", root)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if r := pluginwire.Status(settings, "0.5.2", ""); r.Pin != pluginwire.OK {
		t.Fatalf("pin %+v\n%s", r, out)
	}
	for _, want := range []string{"fugaro upgrade with fugaro 0.5.2", "brew upgrade dimipaun/tap/fugaro && fugaro upgrade", "== " + root,
		"pin: done: pinned to v0.5.2 in " + settings, "git diff", "summary:", "  " + root + ": pin done"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	before := upgradeRead(t, settings)
	out, _, err = executeStdin(t, "", "upgrade", "--local", root)
	if err != nil || upgradeRead(t, settings) != before || !strings.Contains(out, "pin: current: pinned to v0.5.2") || !strings.Contains(out, "not checked: plugin") {
		t.Fatalf("rerun: %v, changed %v\n%s", err, upgradeRead(t, settings) != before, out)
	}
}

// TestUpgradeUnimplementedStepIsNeverCurrent: the plugin step is still a
// placeholder that checks nothing (Task 6 has not landed yet), so no mode
// may call the checkout current as a whole; --check exits 0 for the steps
// it verified and names the one it could not check. The checkout here has
// no fugaro.yaml, so the cloud step (Task 7) is skipped in every mode, not
// "not implemented".
func TestUpgradeUnimplementedStepIsNeverCurrent(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	root, settings := skillCheckout(t, wiredAt("v0.5.2"))
	for _, tc := range []struct {
		name  string
		agent bool
		args  []string
	}{
		{"check", false, []string{"--check"}},
		{"local", false, []string{"--local"}},
		{"yes", false, []string{"--yes"}},
		{"agent", true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.agent {
				t.Setenv(agentMarkers[0], "1")
			}
			out, _, err := executeStdin(t, "", append(append([]string{"upgrade"}, tc.args...), root)...)
			if err != nil || upgradeRead(t, settings) != wiredAt("v0.5.2") {
				t.Fatalf("%v\n%s", err, out)
			}
			for _, want := range []string{"pin: current", "plugin: not implemented: skipped in this build, nothing was checked or changed",
				"cloud: skipped: no fugaro.yaml yet, so no job image to refresh", "not checked: plugin (not implemented in this build)",
				"  " + root + ": pin current, plugin not implemented, cloud skipped", "not checked: plugin: this build does not implement them"} {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}
			for _, bad := range []string{"nothing to do", "is current", "plugin: current", "cloud: current", "plugin: skipped", "cloud: not implemented"} {
				if strings.Contains(out, bad) {
					t.Errorf("output claims %q of a step that does not exist:\n%s", bad, out)
				}
			}
		})
	}
}

func TestUpgradeCheckWritesNothing(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	root, settings := skillCheckout(t, wiredAt("v0.5.1"))
	before := upgradeRead(t, settings)
	out, _, err := executeStdin(t, "", "upgrade", "--check", root)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "something is stale") {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	if upgradeRead(t, settings) != before || !strings.Contains(out, "pin: stale: the pin moves to v0.5.2") {
		t.Fatalf("changed %v\n%s", upgradeRead(t, settings) != before, out)
	}
}

// TestUpgradeNeverMovesAPinDown: U10 for every marketplace and enablement
// state, not only the one pluginwire.Status calls newer.
func TestUpgradeNeverMovesAPinDown(t *testing.T) {
	for _, tc := range []struct {
		name, settings string
		yaml           bool
		flags          []string
	}{
		{"newer", wiredAt("v0.6.0"), false, []string{"--local"}},
		{"fork", `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"github","repo":"someone/fugaro","ref":"v0.6.0"}}},"enabledPlugins":{"fugaro@fugaro":true}}`, false, []string{"--local", "--allow-fork"}},
		{"disabled", `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"github","repo":"dimipaun/fugaro","ref":"v0.6.0"}}},"enabledPlugins":{"fugaro@fugaro":false}}`, true, []string{"--local"}},
		{"check", wiredAt("v0.6.0"), false, []string{"--check"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upgradeEnv(t, "0.5.2")
			root, settings := skillCheckout(t, tc.settings)
			if tc.yaml {
				if err := os.WriteFile(filepath.Join(root, "fugaro.yaml"), []byte("version: 1\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			out, _, err := executeStdin(t, "", append(append([]string{"upgrade"}, tc.flags...), root)...)
			if ExitCode(err) != ExitUserError || upgradeRead(t, settings) != tc.settings {
				t.Fatalf("exit %d, changed %v\n%s", ExitCode(err), upgradeRead(t, settings) != tc.settings, out)
			}
			for _, want := range []string{"pin: failed: the plugin is pinned to 0.6.0, newer than this fugaro 0.5.2", "never moves a pin down", "brew upgrade dimipaun/tap/fugaro",
				"  " + root + ": pin failed, plugin not run, cloud not run", "once that is fixed, rerun: fugaro upgrade " + strings.Join(tc.flags, " ") + " " + root} {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}
			if strings.Contains(out, "pin: done") || strings.Contains(out, "v0.5.2") {
				t.Errorf("the pin moved down:\n%s", out)
			}
		})
	}
}

func TestUpgradeDevBuildChangesNothing(t *testing.T) {
	upgradeEnv(t, "dev")
	root, settings := skillCheckout(t, wiredAt("v0.5.1"))
	before := upgradeRead(t, settings)
	out, _, err := executeStdin(t, "", "upgrade", "--local", root)
	if err != nil || upgradeRead(t, settings) != before || !strings.Contains(out, "a development build (dev)") || !strings.Contains(out, "pin: skipped: a development build") {
		t.Fatalf("%v\n%s", err, out)
	}
}

// TestUpgradeDevBuildSkipsEveryPath is U11 for paths that are not Fugaro
// checkouts, or not checkouts at all: every step skipped, exit 0.
func TestUpgradeDevBuildSkipsEveryPath(t *testing.T) {
	upgradeEnv(t, "dev")
	notCheckout := t.TempDir()
	plain, _ := skillCheckout(t, "")
	out, _, err := executeStdin(t, "", "upgrade", "--local", notCheckout, plain)
	if err != nil {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	for _, want := range []string{"  " + notCheckout + ": pin skipped, plugin skipped, cloud skipped", "  " + plain + ": pin skipped, plugin skipped, cloud skipped"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "failed") || strings.Contains(out, "is current") {
		t.Errorf("a development build judged a path:\n%s", out)
	}
}

func TestUpgradeEveryPathIsTried(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	notCheckout := t.TempDir()
	plain, _ := skillCheckout(t, "") // a checkout, but not a Fugaro one
	root, settings := skillCheckout(t, wiredAt("v0.5.1"))
	out, _, err := executeStdin(t, "", "upgrade", "--local", notCheckout, plain, root)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "stopped in 2 of 3 checkout(s)") {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	if r := pluginwire.Status(settings, "0.5.2", ""); r.Pin != pluginwire.OK {
		t.Fatalf("the failures hid the last checkout: %+v\n%s", r, out)
	}
	for _, want := range []string{"  " + notCheckout + ": checkout failed", "  " + plain + ": checkout failed", "not a Fugaro checkout", "  " + root + ": pin done"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(plain, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Error("a settings file was written in a checkout that is not a Fugaro one")
	}
}

func TestUpgradeSameCheckoutOnce(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	root, _ := skillCheckout(t, wiredAt("v0.5.1"))
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(filepath.Join(sub, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, ".claude", "settings.json"), []byte(wiredAt("v0.5.1")), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := executeStdin(t, "", "upgrade", "--local", root, filepath.Join(root, ".claude"), link, sub)
	if err != nil || strings.Count(out, "pin: done") != 1 || strings.Count(out, "checkout: skipped: the same checkout as "+root) != 3 {
		t.Fatalf("%v\n%s", err, out)
	}
}

// TestUpgradeCountsDistinctCheckouts: "stopped in N of M" counts a checkout
// given twice once.
func TestUpgradeCountsDistinctCheckouts(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	notCheckout := t.TempDir()
	root, _ := skillCheckout(t, wiredAt("v0.5.1"))
	out, _, err := executeStdin(t, "", "upgrade", "--local", notCheckout, root, root)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "stopped in 1 of 2 checkout(s)") {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
}

func TestUpgradeForkNeedsAllowFork(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	fork := `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"github","repo":"someone/fugaro","ref":"v0.5.1"}}},"enabledPlugins":{"fugaro@fugaro":true}}`
	root, settings := skillCheckout(t, fork)
	out, errOut, err := executeStdin(t, "", "upgrade", "--local", root)
	if ExitCode(err) != ExitUserError || upgradeRead(t, settings) != fork || !strings.Contains(errOut, "WARNING") || !strings.Contains(out, "pass --allow-fork if it is yours") {
		t.Fatalf("exit %d\n%s\n%s", ExitCode(err), out, errOut)
	}
	out, errOut, err = executeStdin(t, "", "upgrade", "--local", "--allow-fork", root)
	if data := upgradeRead(t, settings); err != nil || !strings.Contains(data, "v0.5.2") || !strings.Contains(data, "someone/fugaro") {
		t.Fatalf("%v\n%s\n%s", err, data, out)
	}
	if !strings.Contains(errOut, "WARNING: the \"fugaro\" marketplace in "+settings+" is someone/fugaro, not dimipaun/fugaro") || !strings.Contains(out, "pin: done") {
		t.Errorf("the fork's ref moved without the warning:\n%s\n%s", out, errOut)
	}
}

// TestUpgradeCheckReportsAFork: --check reads a fork's pin as stale (exit 1),
// with the warning, and writes nothing.
func TestUpgradeCheckReportsAFork(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	fork := `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"github","repo":"someone/fugaro","ref":"v0.5.1"}}},"enabledPlugins":{"fugaro@fugaro":true}}`
	root, settings := skillCheckout(t, fork)
	out, errOut, err := executeStdin(t, "", "upgrade", "--check", root)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "something is stale") || upgradeRead(t, settings) != fork {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	for _, want := range []string{"pin: stale: the marketplace is the fork someone/fugaro: pass --allow-fork if it is yours (only its ref moves)",
		"  " + root + ": pin stale, plugin not implemented, cloud skipped"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(errOut, "WARNING") {
		t.Errorf("no warning:\n%s", errOut)
	}
}

func TestUpgradeInvalidSettingsNeverRewritten(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	root, settings := skillCheckout(t, "{not json")
	if err := os.WriteFile(filepath.Join(root, "fugaro.yaml"), []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := executeStdin(t, "", "upgrade", "--local", root)
	if ExitCode(err) != ExitUserError || upgradeRead(t, settings) != "{not json" || !strings.Contains(out, "merge this in by hand") {
		t.Fatalf("exit %d\n%s", ExitCode(err), out)
	}
}

func TestUpgradeFlagRules(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	root, settings := skillCheckout(t, wiredAt("v0.5.1"))
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--check", "--yes"}, "--check writes nothing"},
		{[]string{"--local", "--yes"}, "--yes confirms the cloud step"},
		{[]string{"--local", "--repo", "acme/app"}, "which --local skips"},
		{[]string{"--yes", "--workflow", "app", root, root}, "give them with one PATH"},
	} {
		args := append([]string{"upgrade"}, tc.args...)
		if !strings.HasSuffix(strings.Join(args, " "), root) {
			args = append(args, root)
		}
		out, _, err := executeStdin(t, "", args...)
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), tc.want) || strings.Contains(out, "== ") {
			t.Errorf("%q: exit %d, err %v\n%s", tc.args, ExitCode(err), err, out)
		}
	}
	if upgradeRead(t, settings) != wiredAt("v0.5.1") {
		t.Error("a refused command wrote the settings")
	}
}

func TestUpgradeNeedsATerminal(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	root, settings := skillCheckout(t, wiredAt("v0.5.1"))
	out, _, err := executeStdin(t, "", "upgrade", root)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), initflow.NoTerminalAdvice) || !strings.Contains(err.Error(), "--local") ||
		strings.Contains(out, "== ") || upgradeRead(t, settings) != wiredAt("v0.5.1") {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
}
