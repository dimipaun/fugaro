package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/pluginwire"
)

func withVersion(t *testing.T, v string) {
	t.Helper()
	old := Version
	Version = v
	t.Cleanup(func() { Version = old })
	skillWarned.Store(false)
	t.Cleanup(func() { skillWarned.Store(false) })
	// No real user config or installed plugins in any of these tests.
	cfg := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	t.Setenv(noSkillWarningEnv, "")
	t.Setenv("CLOUD_RUN_EXECUTION", "")
}

// skillCheckout makes a checkout whose settings hold content ("" for none).
func skillCheckout(t *testing.T, content string) (root, settings string) {
	t.Helper()
	root = t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	settings = filepath.Join(root, ".claude", "settings.json")
	if content != "" {
		if err := os.Mkdir(filepath.Dir(settings), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(settings, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root, settings
}

func wiredAt(ref string) string {
	return `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"github","repo":"dimipaun/fugaro","ref":"` + ref + `"}}},"enabledPlugins":{"fugaro@fugaro":true}}`
}

func TestUpdateSkillsNeedsNoCredentials(t *testing.T) {
	withVersion(t, "0.2.0")
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/nonexistent")
	t.Setenv("FUGARO_PROJECT", "")
	root, settings := skillCheckout(t, "")
	out, errOut, err := execute(t, "update-skills", "--dir", root)
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if !strings.Contains(out, "git diff") || !strings.Contains(out, "v0.2.0") {
		t.Errorf("out = %q", out)
	}
	if b, _ := os.ReadFile(settings); !strings.Contains(string(b), `"ref": "v0.2.0"`) {
		t.Errorf("settings = %s", b)
	}
	for _, f := range []string{"project", "region", "repo"} {
		if newUpdateSkillsCmd().Flags().Lookup(f) != nil {
			t.Errorf("update-skills has a cloud flag --%s", f)
		}
	}
}

func TestUpdateSkillsCheckExitCode(t *testing.T) {
	withVersion(t, "0.2.0")
	for _, tc := range []struct {
		name, content string
		want          int
	}{
		{"ok", wiredAt("v0.2.0"), 0},
		{"outdated", wiredAt("v0.1.0"), 1},
		{"newer", wiredAt("v0.3.0"), 1},
		{"unpinned", wiredAt(""), 1},
		{"not wired", `{}`, 1},
		{"missing", "", 1},
	} {
		root, settings := skillCheckout(t, tc.content)
		var before []byte
		if tc.content != "" {
			before, _ = os.ReadFile(settings)
		}
		_, _, err := execute(t, "update-skills", "--check", "--dir", root)
		if got := ExitCode(err); got != tc.want {
			t.Errorf("%s: exit = %d (%v), want %d", tc.name, got, err, tc.want)
		}
		if tc.content != "" {
			if after, _ := os.ReadFile(settings); string(after) != string(before) {
				t.Errorf("%s: --check wrote the file", tc.name)
			}
		} else if _, err := os.Stat(settings); !os.IsNotExist(err) {
			t.Errorf("%s: --check created the file", tc.name)
		}
	}
	// Not installed is informational: the pin is ok and Claude Code has no record.
	root, _ := skillCheckout(t, wiredAt("v0.2.0"))
	if _, _, err := execute(t, "update-skills", "--check", "--dir", root); err != nil {
		t.Errorf("ok with no install: %v", err)
	}
	// A dev binary cannot compare: informational too.
	Version = "dev"
	if _, _, err := execute(t, "update-skills", "--check", "--dir", root); err != nil {
		t.Errorf("dev: %v", err)
	}
}

func TestUpdateSkillsOutsideCheckoutPrintsSnippet(t *testing.T) {
	withVersion(t, "0.2.0")
	dir := t.TempDir()
	out, _, err := execute(t, "update-skills", "--dir", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"ref": "v0.2.0"`) || !strings.Contains(out, "not in a checkout") {
		t.Errorf("out = %q", out)
	}
	if es, _ := os.ReadDir(dir); len(es) != 0 {
		t.Errorf("wrote into %s: %v", dir, es)
	}
	if _, _, err := execute(t, "update-skills", "--check", "--dir", dir); ExitCode(err) != 1 || !strings.Contains(err.Error(), "not in a checkout") {
		t.Errorf("--check outside a checkout: %v", err)
	}
}

func TestUpdateSkillsDevBuildWritesNothing(t *testing.T) {
	withVersion(t, "dev")
	root, settings := skillCheckout(t, "")
	out, _, err := execute(t, "update-skills", "--dir", root)
	if err != nil || !strings.Contains(out, "<release tag>") || !strings.Contains(out, "dev build") {
		t.Fatalf("err=%v out=%q", err, out)
	}
	if _, err := os.Stat(settings); !os.IsNotExist(err) {
		t.Error("a dev build wrote settings")
	}
}

func TestUpdateSkillsInvalidFileLeftAlone(t *testing.T) {
	withVersion(t, "0.2.0")
	root, settings := skillCheckout(t, "{ // mine\n}")
	_, errOut, err := execute(t, "update-skills", "--dir", root)
	if ExitCode(err) != 1 || !strings.Contains(errOut, `"ref": "v0.2.0"`) || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("err=%v stderr=%q", err, errOut)
	}
	if b, _ := os.ReadFile(settings); string(b) != "{ // mine\n}" {
		t.Errorf("rewritten: %q", b)
	}
}

func TestUpdateSkillsJSON(t *testing.T) {
	withVersion(t, "0.2.0")
	root, _ := skillCheckout(t, wiredAt("v0.1.0"))
	out, _, err := execute(t, "update-skills", "--json", "--dir", root)
	if err != nil {
		t.Fatal(err)
	}
	var got updateSkillsOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if !got.Checkout || !got.Changed || got.Ref != "v0.2.0" || got.Report == nil || got.Report.Pin != pluginwire.OK {
		t.Errorf("got %+v", got)
	}
	out, _, err = execute(t, "update-skills", "--json", "--check", "--dir", root)
	if err != nil {
		t.Fatal(err)
	}
	got = updateSkillsOutput{}
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.Report == nil || got.Report.Pin != pluginwire.OK || got.Changed {
		t.Errorf("check json: %v %+v", err, got)
	}
	out, _, _ = execute(t, "update-skills", "--json", "--dir", t.TempDir())
	got = updateSkillsOutput{}
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.Checkout || got.Snippet == "" {
		t.Errorf("outside json: %v %+v", err, got)
	}
}

func warnLine(t *testing.T, stderr string) bool {
	t.Helper()
	return strings.Contains(stderr, "warning: the Fugaro plugin pinned in .claude/settings.json is 0.1.0, this is 0.2.0: run fugaro upgrade --local")
}

func TestWarningOncePerProcess(t *testing.T) {
	withVersion(t, "0.2.0")
	root, _ := skillCheckout(t, wiredAt("v0.1.0"))
	t.Chdir(root)
	_, errOut, _ := execute(t, "validate", filepath.Join(root, "missing.yaml"))
	if !warnLine(t, errOut) || strings.Count(errOut, "Fugaro plugin") != 1 {
		t.Fatalf("stderr = %q", errOut)
	}
	_, errOut, _ = execute(t, "validate", filepath.Join(root, "missing.yaml"))
	if strings.Contains(errOut, "Fugaro plugin") {
		t.Errorf("second command in the process warned again: %q", errOut)
	}
}

func TestWarningNeverOnStdoutOrJSON(t *testing.T) {
	withVersion(t, "0.2.0")
	root, _ := skillCheckout(t, wiredAt("v0.1.0"))
	cfg := filepath.Join(root, "fugaro.yaml")
	if err := os.WriteFile(cfg, []byte(cliMinimalYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	out, errOut, err := execute(t, "validate", "--json", cfg)
	if err != nil && ExitCode(err) != ExitUserError {
		t.Fatal(err)
	}
	if strings.Contains(out, "plugin") {
		t.Errorf("stdout has the warning: %q", out)
	}
	var v validateOutput
	if json.Unmarshal([]byte(out), &v) != nil {
		t.Errorf("stdout is not JSON: %q", out)
	}
	if !warnLine(t, errOut) {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestWarningSilencedByEnv(t *testing.T) {
	for _, v := range []string{"1", "yes"} {
		withVersion(t, "0.2.0")
		root, _ := skillCheckout(t, wiredAt("v0.1.0"))
		t.Chdir(root)
		t.Setenv(noSkillWarningEnv, v)
		_, errOut, _ := execute(t, "validate", filepath.Join(root, "missing.yaml"))
		if strings.Contains(errOut, "Fugaro plugin") {
			t.Errorf("%s: warned: %q", v, errOut)
		}
	}
	withVersion(t, "0.2.0")
	root, _ := skillCheckout(t, wiredAt("v0.1.0"))
	t.Chdir(root)
	t.Setenv(noSkillWarningEnv, "0")
	if _, errOut, _ := execute(t, "validate", filepath.Join(root, "missing.yaml")); !warnLine(t, errOut) {
		t.Errorf("=0 should not silence: %q", errOut)
	}
}

func TestNoWarningInCloudRun(t *testing.T) {
	withVersion(t, "0.2.0")
	root, _ := skillCheckout(t, wiredAt("v0.1.0"))
	t.Chdir(root)
	t.Setenv("CLOUD_RUN_EXECUTION", "exec-1")
	if _, errOut, _ := execute(t, "validate", filepath.Join(root, "missing.yaml")); strings.Contains(errOut, "Fugaro plugin") {
		t.Errorf("warned inside Cloud Run: %q", errOut)
	}
}

func TestWarningOnlyForStaleness(t *testing.T) {
	for name, tc := range map[string]struct {
		content, version string
		want             string
	}{
		"ok":          {wiredAt("v0.2.0"), "0.2.0", ""},
		"newer":       {wiredAt("v0.3.0"), "0.2.0", "newer than this fugaro 0.2.0: upgrade fugaro"},
		"not wired":   {`{}`, "0.2.0", ""},
		"unpinned":    {wiredAt(""), "0.2.0", ""},
		"dev":         {wiredAt("v0.1.0"), "dev", ""},
		"no settings": {"", "0.2.0", ""},
	} {
		withVersion(t, tc.version)
		root, _ := skillCheckout(t, tc.content)
		t.Chdir(root)
		_, errOut, _ := execute(t, "validate", filepath.Join(root, "missing.yaml"))
		if tc.want == "" && strings.Contains(errOut, "Fugaro plugin") {
			t.Errorf("%s: warned: %q", name, errOut)
		}
		if tc.want != "" && !strings.Contains(errOut, tc.want) {
			t.Errorf("%s: stderr = %q", name, errOut)
		}
	}
}

func TestWarningInstalledDiffers(t *testing.T) {
	withVersion(t, "0.2.0")
	root, _ := skillCheckout(t, wiredAt("v0.2.0"))
	cfg := os.Getenv("CLAUDE_CONFIG_DIR")
	_ = os.MkdirAll(filepath.Join(cfg, "plugins"), 0o755)
	_ = os.WriteFile(filepath.Join(cfg, "plugins", "installed_plugins.json"),
		[]byte(`{"plugins":{"fugaro@fugaro":[{"scope":"user","version":"0.1.0"}]}}`), 0o644)
	t.Chdir(root)
	_, errOut, _ := execute(t, "validate", filepath.Join(root, "missing.yaml"))
	if !strings.Contains(errOut, "installed Fugaro plugin is 0.1.0 but .claude/settings.json pins 0.2.0") {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestNoWarningForWatchOrOtherCommands(t *testing.T) {
	withVersion(t, "0.2.0")
	root, _ := skillCheckout(t, wiredAt("v0.1.0"))
	t.Chdir(root)
	if _, errOut, _ := execute(t, "version"); strings.Contains(errOut, "Fugaro plugin") {
		t.Errorf("version warned: %q", errOut)
	}
	if _, errOut, _ := execute(t, "update-skills", "--check"); strings.Contains(errOut, "warning: the Fugaro plugin pinned") {
		t.Errorf("update-skills duplicated the line: %q", errOut)
	}
}

func forkAt(repo, ref string) string {
	return `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"github","repo":"` + repo + `","ref":"` + ref + `"}}},"enabledPlugins":{"fugaro@fugaro":true}}`
}

func TestForkAlreadyAtOurTagIsStillNamed(t *testing.T) {
	withVersion(t, "0.2.0")
	root, _ := skillCheckout(t, forkAt("acme/fugaro-fork", "v0.2.0"))
	out, errOut, err := execute(t, "update-skills", "--dir", root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut, "WARNING") || !strings.Contains(errOut, "acme/fugaro-fork") || !strings.Contains(out, "foreign") {
		t.Errorf("stdout=%q stderr=%q", out, errOut)
	}
	// --json: the object says it, stderr still warns.
	out, errOut, err = execute(t, "update-skills", "--json", "--dir", root)
	var got updateSkillsOutput
	if err != nil || json.Unmarshal([]byte(out), &got) != nil || got.Foreign != "acme/fugaro-fork" || !strings.Contains(errOut, "WARNING") {
		t.Errorf("json: err=%v out=%q stderr=%q", err, out, errOut)
	}
	// --check says it too and exits 1.
	_, errOut, err = execute(t, "update-skills", "--check", "--dir", root)
	if ExitCode(err) != 1 || !strings.Contains(errOut, "acme/fugaro-fork") {
		t.Errorf("check: err=%v stderr=%q", err, errOut)
	}
}

func TestForkRefNeedsAllowFork(t *testing.T) {
	withVersion(t, "0.2.0")
	content := forkAt("acme/fugaro-fork", "v0.1.0")
	root, settings := skillCheckout(t, content)
	_, errOut, err := execute(t, "update-skills", "--dir", root)
	if ExitCode(err) != 1 || !strings.Contains(errOut, "acme/fugaro-fork") || !strings.Contains(err.Error(), "--allow-fork") {
		t.Fatalf("err=%v stderr=%q", err, errOut)
	}
	if b, _ := os.ReadFile(settings); string(b) != content {
		t.Fatalf("written without --allow-fork: %s", b)
	}
	out, _, _ := execute(t, "update-skills", "--json", "--dir", root)
	var got updateSkillsOutput
	if json.Unmarshal([]byte(out), &got) != nil || got.Error == "" || got.Foreign != "acme/fugaro-fork" {
		t.Errorf("json refusal: %q", out)
	}
	if _, errOut, err := execute(t, "update-skills", "--allow-fork", "--dir", root); err != nil || !strings.Contains(errOut, "WARNING") {
		t.Fatalf("allow-fork: err=%v stderr=%q", err, errOut)
	}
	if b, _ := os.ReadFile(settings); !strings.Contains(string(b), `"v0.2.0"`) || !strings.Contains(string(b), "acme/fugaro-fork") {
		t.Errorf("settings = %s", b)
	}
}

func TestRepoStringsAreEscaped(t *testing.T) {
	withVersion(t, "0.2.0")
	root, _ := skillCheckout(t, forkAt(`a\u001b[2Jb\u202ec`, "v0.2.0"))
	for _, args := range [][]string{{"update-skills"}, {"update-skills", "--check"}} {
		out, errOut, _ := execute(t, append(args, "--dir", root)...)
		if strings.ContainsAny(out+errOut, "\x1b\u202e") || !strings.Contains(out+errOut, `\u001b`) {
			t.Errorf("%v: raw control characters or no escape: %q %q", args, out, errOut)
		}
	}
}

func TestDisabledPluginReported(t *testing.T) {
	withVersion(t, "0.2.0")
	root, settings := skillCheckout(t, `{"enabledPlugins":{"fugaro@fugaro":false}}`)
	out, errOut, err := execute(t, "update-skills", "--dir", root)
	if err != nil || !strings.Contains(errOut, "disabled by this repository") || !strings.Contains(out, "not wired") {
		t.Fatalf("err=%v out=%q stderr=%q", err, out, errOut)
	}
	if b, _ := os.ReadFile(settings); !strings.Contains(string(b), `"fugaro@fugaro": false`) {
		t.Errorf("settings = %s", b)
	}
}

func TestUpdateSkillsRefusalsAreUserErrorsWithJSONObject(t *testing.T) {
	withVersion(t, "0.2.0")
	for name, content := range map[string]string{
		"invalid": "{ // mine\n}",
		"git":     `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"git","url":"https://x.invalid/r.git"}}}}`,
	} {
		root, settings := skillCheckout(t, content)
		_, errOut, err := execute(t, "update-skills", "--dir", root)
		if ExitCode(err) != 1 || !strings.Contains(errOut, `"ref": "v0.2.0"`) {
			t.Errorf("%s: err=%v stderr=%q", name, err, errOut)
		}
		out, _, err := execute(t, "update-skills", "--json", "--dir", root)
		var got updateSkillsOutput
		if ExitCode(err) != 1 || json.Unmarshal([]byte(out), &got) != nil || got.Error == "" || got.Snippet == "" {
			t.Errorf("%s json: err=%v out=%q", name, err, out)
		}
		if b, _ := os.ReadFile(settings); string(b) != content {
			t.Errorf("%s: rewritten", name)
		}
	}
}
