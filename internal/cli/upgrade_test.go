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

func TestUpgradeLocalPinsThenNothingToDo(t *testing.T) {
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
	if err != nil || upgradeRead(t, settings) != before || !strings.Contains(out, "pin: current: pinned to v0.5.2") || !strings.Contains(out, "nothing to do: "+root) {
		t.Fatalf("rerun: %v, changed %v\n%s", err, upgradeRead(t, settings) != before, out)
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

func TestUpgradeNeverMovesAPinDown(t *testing.T) {
	upgradeEnv(t, "0.5.2")
	root, settings := skillCheckout(t, wiredAt("v0.6.0"))
	before := upgradeRead(t, settings)
	out, _, err := executeStdin(t, "", "upgrade", "--local", root)
	if ExitCode(err) != ExitUserError || upgradeRead(t, settings) != before {
		t.Fatalf("exit %d, changed %v\n%s", ExitCode(err), upgradeRead(t, settings) != before, out)
	}
	for _, want := range []string{"pin: failed: the plugin is pinned to 0.6.0, newer than this fugaro 0.5.2", "never moves a pin down", "brew upgrade dimipaun/tap/fugaro",
		"  " + root + ": pin failed", "once that is fixed, rerun: fugaro upgrade --local " + root} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
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
	out, _, err := executeStdin(t, "", "upgrade", "--local", root, filepath.Join(root, ".claude"))
	if err != nil || strings.Count(out, "pin: done") != 1 || !strings.Contains(out, "checkout: skipped: the same checkout as "+root) {
		t.Fatalf("%v\n%s", err, out)
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
	out, _, err = executeStdin(t, "", "upgrade", "--local", "--allow-fork", root)
	if data := upgradeRead(t, settings); err != nil || !strings.Contains(data, "v0.5.2") || !strings.Contains(data, "someone/fugaro") {
		t.Fatalf("%v\n%s\n%s", err, data, out)
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
