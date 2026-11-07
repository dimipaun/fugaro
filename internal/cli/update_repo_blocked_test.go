package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// convergeLegacyYAML is acme/app with a key 0.7.0 removed (workflows.*.base):
// the file /fugaro:setup wrote before the base-image migration and the owner
// never cleaned up. It is *invalid* — the strict reader refuses the line — so
// the repository stage stops needs-you and the checkout it belongs to is not
// onboarded again by this run.
const convergeLegacyYAML = "version: 1\nproject: aurora\ngit: { provider: github }\nagent: { auth: oauth }\nworkflows:\n" +
	"  app: { base: server, commands: { build: sh build.sh, test: sh test.sh } }\n"

// TestUpdateConvergesPastAnInvalidCheckoutFugaroYAML (#1836): a checkout whose
// default-branch fugaro.yaml still carries a key 0.7.0 removed stops the
// repository stage — the user's to fix, `fugaro validate` — and that stop is
// the user's alone. The installation-level stages (rules, policy, layer) and
// the checkout's own local steps (the pin and the plugin in Claude Code) read
// no repository fugaro.yaml, so the run must still reach them: the plugin
// install is the one the HARD RULE is about here, because it is the step that
// clears the "the installed Fugaro plugin is X but .claude/settings.json pins
// Y: run fugaro update" line every command prints, and a run that never
// reaches it leaves the user in a loop with no way out.
//
// Mutation (run, restore): drop Checkout's pastStops row (initflow/stage.go)
// and this test fails at the first assertion on the checkout stage — it is
// blocked, the pin and the plugin never move, and the local plugin stays on
// the previous release.
func TestUpdateConvergesPastAnInvalidCheckoutFugaroYAML(t *testing.T) {
	r := newConvergeRigYAML(t, v4Node(), convergeLegacyYAML)
	currentRelease(t)

	// The fixture is the issue's state: the installed plugin is the previous
	// release's while the pin is this one's, which is the line doctor and every
	// command warn about.
	installed := installedVersion(t, r)
	if installed != convergePrev {
		t.Fatalf("the fixture's installed plugin is %q, want %q", installed, convergePrev)
	}

	out, errOut, err := executeStdin(t, "", "update", "--yes", "--json")
	if err == nil {
		t.Fatalf("the run reported success while the checkout's own fugaro.yaml is invalid:\n%s", out)
	}
	var res convergeJSON
	if uerr := json.Unmarshal([]byte(out), &res); uerr != nil {
		t.Fatalf("%v:\n%s\n%s", uerr, out, errOut)
	}

	// The checkout's file is still the user's to fix, and the stage that says so
	// is not a lie about the stages that do not read it.
	if got := res.state(initflow.Repository); got != "needs-you" {
		t.Errorf("the repository stage is %q, want needs-you: the invalid file is the user's to fix\n%s", got, out)
	}
	leftRepo := false
	for _, l := range res.Left {
		if l["stage"] == initflow.Repository && strings.Contains(l["text"], "fugaro validate") {
			leftRepo = true
		}
	}
	if !leftRepo {
		t.Errorf("the run does not leave the invalid fugaro.yaml for the user:\n%s", out)
	}

	// The stages that read no repository fugaro.yaml ran.
	for _, name := range []string{initflow.Rules, initflow.Policy, initflow.Layer, initflow.Checkout} {
		if got := res.state(name); got == "blocked" {
			t.Errorf("the %s stage is blocked by the repository stage, which it does not read:\n%s", name, out)
		}
	}

	// The plugin install ran, and the installed copy is this release's: the
	// warning the user could not clear is cleared.
	if got := installedVersion(t, r); got != convergeRelease {
		t.Errorf("the installed Claude Code plugin is %q after fugaro update, want %q: the checkout stage never installed it\n%s", got, convergeRelease, out)
	}
	if calls := r.claude.Changes(t); len(calls) == 0 {
		t.Errorf("the run never ran claude to install the plugin:\n%s", out)
	}

	// The run does not print the staleness line it exists to clear: not before
	// it runs (the door that fixes it pointing at itself), and not after it
	// either, because the plugin is current by then.
	if line := staleWarnings(errOut + out); line != "" {
		t.Errorf("fugaro update printed the staleness line it is the fix for: %q\n%s", line, out)
	}
}

// staleWarnings is every staleness line in a run's output, "" when there is
// none: the sentence a stale line carries, so a test reads the warning and not
// some other mention of the plugin.
func staleWarnings(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "warning: the installed Fugaro plugin") || strings.HasPrefix(line, "warning: the Fugaro plugin") {
			return line
		}
	}
	return ""
}

// installedVersion is the version Claude Code's own record gives for the
// Fugaro plugin, read the way the CLI reads it (installedPlugins).
func installedVersion(t *testing.T, r *convergeRig) string {
	t.Helper()
	path := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "plugins", "installed_plugins.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v", err)
	}
	var doc struct {
		Plugins map[string][]struct {
			Version string `json:"version"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("%v: %s", err, data)
	}
	for _, in := range doc.Plugins[pluginwire.PluginID] {
		if in.Version != "" {
			return in.Version
		}
	}
	return ""
}
