package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/testutil"
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
	// update-skills is the machine-side half now (U2, D949): it runs the
	// plugin step, which looks up the claude on PATH. No test may reach the
	// developer's own — a test that wants one hands the step the fake
	// (useFakeClaude, after this).
	oldClaude := claudeLookPath
	claudeLookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	t.Cleanup(func() { claudeLookPath = oldClaude })
}

// installUserPlugin writes Claude Code's record of a plugin installed at user
// scope, at version v, into the config directory withVersion isolated: the
// install doctor's plugin-install answers about wherever the checkout carries
// no release-tag pin (D942, U3 of
// docs/plugin-user-scope/implementation-plan.md), and the one fugaro update
// maintains.
func installUserPlugin(t *testing.T, v string) {
	t.Helper()
	dir := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"version":2,"plugins":{"fugaro@fugaro":[{"scope":"user","version":"` + v + `"}]}}`
	if err := os.WriteFile(filepath.Join(dir, "installed_plugins.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// skillCheckout makes a Fugaro checkout whose settings hold content ("" for
// none): a real repository with a fugaro.yaml, which is the whole of what makes
// a checkout one Fugaro's steps run in (U1 of
// docs/plugin-user-scope/implementation-plan.md: fugaroCheckout lost its second
// disjunct, the .claude/settings.json an older fugaro wired). Its project: is
// one no test's local config selects, which is what leaves the cloud step
// skipped for the fixtures that read it rather than run it.
func skillCheckout(t *testing.T, content string) (root, settings string) {
	t.Helper()
	testutil.IsolateGit(t)
	root = t.TempDir()
	testutil.Git(t, root, "init", "--quiet")
	testutil.Git(t, root, "remote", "add", "origin", "https://github.com/"+skillCheckoutRepo+".git")
	if err := os.WriteFile(filepath.Join(root, "fugaro.yaml"), []byte(skillCheckoutYAML), 0o644); err != nil {
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

// plainCheckout is a git checkout Fugaro is not set up in: no fugaro.yaml, so
// the checkout gate refuses it before any step runs (U1: the wiring an older
// fugaro left in .claude/settings.json is not a second way of being a Fugaro
// checkout).
func plainCheckout(t *testing.T) string {
	t.Helper()
	testutil.IsolateGit(t)
	root := t.TempDir()
	testutil.Git(t, root, "init", "--quiet")
	return root
}

// skillCheckoutRepo is the fixture's origin: a repository no test's local
// project config lists, so the cloud step, where a mode reads it, finds
// nothing of its own here and skips (the step's own line says which of the two
// reasons it is).
const skillCheckoutRepo = "acme/away"

// skillCheckoutYAML is the fugaro.yaml of the fixtures above: enough for the
// checkout gate and for the steps that read the file, naming a project no
// test's local config selects.
const skillCheckoutYAML = "version: 1\nproject: aurora\ngit: { provider: github }\n" +
	"workflows:\n  app: { commands: { build: sh build.sh, test: sh test.sh } }\n"

func wiredAt(ref string) string {
	return `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"github","repo":"dimipaun/fugaro","ref":"` + ref + `"}}},"enabledPlugins":{"fugaro@fugaro":true}}`
}

// The command is the machine-side half (U2, D949): the plugin is installed or
// updated in the user's own Claude Code through the fixed claude calls, nothing
// in the checkout is written, and it needs no credentials and no Terraform.
func TestUpdateSkillsNeedsNoCredentials(t *testing.T) {
	withVersion(t, "0.2.0")
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/nonexistent")
	t.Setenv("FUGARO_PROJECT", "")
	root, settings := skillCheckout(t, wiredAt("v0.1.0"))
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.On(t, "plugins", userInstall("0.2.0"), argsInstall...)
	useFakeClaude(t, f)
	out, errOut, err := execute(t, "update-skills", "--dir", root)
	if err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	if !strings.Contains(out, "installed 0.2.0") || !strings.Contains(out, "restart running Claude Code sessions") {
		t.Errorf("out = %q", out)
	}
	want := [][]string{argsListMarkets, argsListPlugins, addUser("0.2.0"), argsInstall, argsListPlugins}
	if !slices.EqualFunc(callArgs(f.Calls(t)), want, slices.Equal[[]string]) {
		t.Errorf("calls %q, want %q", callArgs(f.Calls(t)), want)
	}
	// The pin an older fugaro left is not this command's to move: it comes out
	// of the run byte for byte as it went in (U2).
	if got := convergeRead(t, settings); got != wiredAt("v0.1.0") {
		t.Errorf("settings = %s, want the leftover pin as it was", got)
	}
	for _, fl := range []string{"project", "region", "repo"} {
		if newUpdateSkillsCmd().Flags().Lookup(fl) != nil {
			t.Errorf("update-skills has a cloud flag --%s", fl)
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
		// A checkout nothing pins is the normal checkout since D949: nothing
		// in the tree writes a pin any more, so "not wired" is information and
		// no longer turns a repository's CI red (D942, U3 of
		// docs/plugin-user-scope/implementation-plan.md). The pinned states
		// around it are unchanged.
		{"not wired", `{}`, 0},
		{"missing", "", 0},
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

// Outside a checkout there is no repository to report on — the plugin is this
// machine's, but the command still reads a checkout for its report — so it says
// so and writes nothing, anywhere.
func TestUpdateSkillsOutsideACheckoutSaysSo(t *testing.T) {
	withVersion(t, "0.2.0")
	dir := t.TempDir()
	out, _, err := execute(t, "update-skills", "--dir", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "not in a checkout") {
		t.Errorf("out = %q", out)
	}
	if es, _ := os.ReadDir(dir); len(es) != 0 {
		t.Errorf("wrote into %s: %v", dir, es)
	}
	js, _, _ := execute(t, "update-skills", "--json", "--dir", dir)
	var got updateSkillsOutput
	if json.Unmarshal([]byte(js), &got) != nil || got.Checkout || got.Snippet != "" || got.Changed {
		t.Errorf("json outside a checkout: %q", js)
	}
	if _, _, err := execute(t, "update-skills", "--check", "--dir", dir); ExitCode(err) != 1 || !strings.Contains(err.Error(), "not in a checkout") {
		t.Errorf("--check outside a checkout: %v", err)
	}
}

// A dev build has no release tag to install from: the step says so, and
// nothing anywhere is written.
func TestUpdateSkillsDevBuildWritesNothing(t *testing.T) {
	withVersion(t, "dev")
	root, settings := skillCheckout(t, "")
	out, _, err := execute(t, "update-skills", "--dir", root)
	if err != nil || !strings.Contains(out, "a development build has no release plugin to install") {
		t.Fatalf("err=%v out=%q", err, out)
	}
	if _, err := os.Stat(settings); !os.IsNotExist(err) {
		t.Error("a dev build wrote settings")
	}
}

// Nothing in the checkout is read for the machine's install, so a file the
// command cannot parse is neither a refusal nor a rewrite: it comes out byte
// for byte, and --check is what is left to be told about it.
func TestUpdateSkillsLeavesAnUnreadableFileAlone(t *testing.T) {
	withVersion(t, "0.2.0")
	const content = "{ // mine\n}"
	root, settings := skillCheckout(t, content)
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.On(t, "plugins", userInstall("0.2.0"), argsInstall...)
	useFakeClaude(t, f)
	out, errOut, err := execute(t, "update-skills", "--dir", root)
	if err != nil {
		t.Fatalf("err=%v stderr=%q", err, errOut)
	}
	if !strings.Contains(out, "installed 0.2.0") {
		t.Errorf("out = %q", out)
	}
	if got := convergeRead(t, settings); got != content {
		t.Errorf("rewritten: %q, want %q", got, content)
	}
	// A file that cannot be parsed is one of "not wired"'s states, and that
	// state is information since D942 (U3 of
	// docs/plugin-user-scope/implementation-plan.md): the exit code flipped
	// with the severity, and the reason stays on the line the command prints —
	// the check says what it could not read, it just no longer fails a
	// repository the merge that wrote pins left behind.
	txt, _, err := execute(t, "update-skills", "--check", "--dir", root)
	if err != nil || !strings.Contains(txt, "not usable settings") {
		t.Errorf("--check: err=%v out=%q", err, txt)
	}
}

func TestUpdateSkillsJSON(t *testing.T) {
	withVersion(t, "0.2.0")
	root, _ := skillCheckout(t, wiredAt("v0.1.0"))
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.On(t, "plugins", userInstall("0.2.0"), argsInstall...)
	useFakeClaude(t, f)
	out, _, err := execute(t, "update-skills", "--json", "--dir", root)
	if err != nil {
		t.Fatal(err)
	}
	var got updateSkillsOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	// changed and ref keep their meanings and are false and absent: the command
	// writes no settings file any more (U2, D949). The leftover pin the checkout
	// still carries is in the report, which is where a reader asks about it.
	if !got.Checkout || got.Changed || got.Ref != "" || got.Report == nil || got.Report.Pin != pluginwire.Outdated || got.Report.Ref != "v0.1.0" {
		t.Errorf("got %+v", got)
	}
	out, _, err = execute(t, "update-skills", "--json", "--check", "--dir", root)
	got = updateSkillsOutput{}
	if uerr := json.Unmarshal([]byte(out), &got); uerr != nil || got.Report == nil || got.Report.Pin != pluginwire.Outdated || got.Changed {
		t.Errorf("check json: %v %+v", uerr, got)
	}
	if ExitCode(err) != 1 {
		t.Errorf("--check on a leftover pin behind this binary: exit = %d, want 1", ExitCode(err))
	}
	out, _, _ = execute(t, "update-skills", "--json", "--dir", t.TempDir())
	got = updateSkillsOutput{}
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.Checkout || got.Snippet != "" {
		t.Errorf("outside json: %v %+v", err, got)
	}
}

// U2 of docs/plugin-user-scope/implementation-plan.md: the machine-side
// command's --json document keeps every member's meaning ("A field never
// changes meaning"): settings and changed stay declared, ref stays absent
// rather than being retyped onto the leftover pin's ref, and nothing in the
// checkout is written.
func TestUpdateSkillsJSONDocumentKeepsItsShape(t *testing.T) {
	withVersion(t, "0.2.0")
	root, settings := skillCheckout(t, "")
	out, _, err := execute(t, "update-skills", "--json", "--dir", root)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	for _, k := range []string{"checkout", "settings", "changed"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("--json dropped %q; a member removed outright bumps the document's version\n%s", k, out)
		}
	}
	if got := string(raw["settings"]); got != `"`+settings+`"` {
		t.Errorf("settings = %s, want the checkout's own file %s", got, settings)
	}
	if got := string(raw["changed"]); got != "false" {
		t.Errorf("changed = %s, want false: nothing in the checkout is written", got)
	}
	if got, ok := raw["ref"]; ok {
		t.Errorf("ref = %s, want it absent: the tag this command pins to is not the leftover pin's ref", got)
	}
	if b, err := os.ReadFile(settings); !os.IsNotExist(err) {
		t.Errorf("wrote %s:\n%s", settings, b)
	}
}

func warnLine(t *testing.T, stderr string) bool {
	t.Helper()
	return strings.Contains(stderr, "warning: the Fugaro plugin pinned in .claude/settings.json is 0.1.0, this is 0.2.0: run fugaro update in your own terminal")
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

// A leftover pin that names someone else's repository is still *read*, and
// --check is where it is named: a fork served under the fugaro name is exactly
// what a person has to be told about (the run itself installs Fugaro's own
// repository and never rewrites what it finds, U2).
func TestForkAlreadyAtOurTagIsStillNamed(t *testing.T) {
	withVersion(t, "0.2.0")
	root, _ := skillCheckout(t, forkAt("acme/fugaro-fork", "v0.2.0"))
	txt, errOut, err := execute(t, "update-skills", "--check", "--dir", root)
	if ExitCode(err) != 1 || !strings.Contains(errOut, "acme/fugaro-fork") || !strings.Contains(txt, "foreign") {
		t.Errorf("check: err=%v stdout=%q stderr=%q", err, txt, errOut)
	}
	// --json: the object says it, stderr still warns.
	out, errOut, err := execute(t, "update-skills", "--json", "--check", "--dir", root)
	var got updateSkillsOutput
	if ExitCode(err) != 1 || json.Unmarshal([]byte(out), &got) != nil || got.Foreign != "acme/fugaro-fork" || !strings.Contains(errOut, "WARNING") {
		t.Errorf("json: err=%v out=%q stderr=%q", err, out, errOut)
	}
}

// The merge's own flag is gone with the merge (D949, U2): --allow-fork moved
// the ref of another repository's marketplace, which is a write, and nothing
// writes any more. A user typing it gets cobra's unknown flag, with no alias
// and no tombstone — the same shape the deleted merge gets everywhere.
func TestUpdateSkillsHasNoWriteFlags(t *testing.T) {
	withVersion(t, "0.2.0")
	root, settings := skillCheckout(t, forkAt("acme/fugaro-fork", "v0.1.0"))
	content := forkAt("acme/fugaro-fork", "v0.1.0")
	_, _, err := execute(t, "update-skills", "--allow-fork", "--dir", root)
	if ExitCode(err) != 1 || !strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("--allow-fork: err=%v, want an unknown flag", err)
	}
	for _, fl := range []string{"allow-fork", "pin"} {
		if newUpdateSkillsCmd().Flags().Lookup(fl) != nil {
			t.Errorf("update-skills has --%s, which was the write's own flag (U2)", fl)
		}
	}
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.On(t, "plugins", userInstall("0.2.0"), argsInstall...)
	useFakeClaude(t, f)
	if _, _, err := execute(t, "update-skills", "--dir", root); err != nil {
		t.Fatalf("a fork's leftover pin is no refusal any more (nothing writes it): %v", err)
	}
	if got := convergeRead(t, settings); got != content {
		t.Errorf("settings = %s, want the fork's pin as it was", got)
	}
}

// A repository string out of the checkout's file reaches the person through
// --check, so it is escaped there; the run path prints nothing of the file at
// all, which is the stronger guarantee the deletion bought (U2).
func TestRepoStringsAreEscaped(t *testing.T) {
	withVersion(t, "0.2.0")
	root, _ := skillCheckout(t, forkAt(`a\u001b[2Jb\u202ec`, "v0.2.0"))
	txt, errOut, _ := execute(t, "update-skills", "--check", "--dir", root)
	js, _, _ := execute(t, "update-skills", "--json", "--check", "--dir", root)
	if strings.ContainsAny(txt+errOut+js, "\x1b\u202e") || !strings.Contains(js, `\u001b`) {
		t.Errorf("raw control characters in the report: %q %q %q", txt, errOut, js)
	}
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.On(t, "plugins", userInstall("0.2.0"), argsInstall...)
	useFakeClaude(t, f)
	out, errOut, err := execute(t, "update-skills", "--dir", root)
	if err != nil || strings.ContainsAny(out+errOut, "\x1b\u202e") || strings.Contains(out+errOut, "fugaro-fork") {
		t.Errorf("the run prints the checkout's repository: err=%v out=%q stderr=%q", err, out, errOut)
	}
}

// A checkout's leftover pin can say the plugin is disabled there. Nothing in
// the tree honours that any more — the plugin is the machine's (D949, U2) — so
// --check reports it as the state of a file, and the run installs the plugin
// and leaves the file byte for byte.
func TestDisabledPluginReported(t *testing.T) {
	withVersion(t, "0.2.0")
	const content = `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"github","repo":"dimipaun/fugaro","ref":"v0.2.0"}}},"enabledPlugins":{"fugaro@fugaro":false}}`
	root, settings := skillCheckout(t, content)
	// "disabled by the repository" is one of "not wired"'s states, which is
	// information since D942 (U3 of
	// docs/plugin-user-scope/implementation-plan.md): --check prints the state
	// and exits 0, the same treatment a checkout nothing pins gets, because
	// neither is a state the machine's install is in.
	txt, _, err := execute(t, "update-skills", "--check", "--dir", root)
	if err != nil || !strings.Contains(txt, "disabled by the repository") {
		t.Fatalf("check: err=%v out=%q", err, txt)
	}
	f := testutil.NewClaudePluginFake(t, "[]", "[]")
	f.On(t, "plugins", userInstall("0.2.0"), argsInstall...)
	useFakeClaude(t, f)
	out, _, err := execute(t, "update-skills", "--dir", root)
	if err != nil || !strings.Contains(out, "installed 0.2.0") {
		t.Fatalf("err=%v out=%q", err, out)
	}
	if got := convergeRead(t, settings); got != content {
		t.Errorf("settings = %s, want the file as it was", got)
	}
}

// Nothing in the checkout is this command's to refuse or repair: the files that
// used to stop it (a fork's marketplace, a source that is not GitHub, a file
// that is not JSON) come out of a run byte for byte, and the document reports
// no error (U2, D949).
func TestUpdateSkillsLeavesEveryCheckoutFileAlone(t *testing.T) {
	withVersion(t, "0.2.0")
	for name, content := range map[string]string{
		"a file that is not JSON":     "{ // mine\n}",
		"a fork's marketplace":        forkAt("acme/fugaro-fork", "v0.1.0"),
		"a source that is not GitHub": `{"extraKnownMarketplaces":{"fugaro":{"source":{"source":"git","url":"https://x.invalid/r.git"}}},"enabledPlugins":{"fugaro@fugaro":true}}`,
	} {
		root, settings := skillCheckout(t, content)
		f := testutil.NewClaudePluginFake(t, "[]", "[]")
		f.On(t, "plugins", userInstall("0.2.0"), argsInstall...)
		useFakeClaude(t, f)
		out, errOut, err := execute(t, "update-skills", "--dir", root)
		if err != nil || !strings.Contains(out, "installed 0.2.0") {
			t.Errorf("%s: err=%v out=%q stderr=%q", name, err, out, errOut)
		}
		if got := convergeRead(t, settings); got != content {
			t.Errorf("%s: rewritten:\n%s", name, got)
		}
		js, _, err := execute(t, "update-skills", "--json", "--dir", root)
		var got updateSkillsOutput
		if err != nil || json.Unmarshal([]byte(js), &got) != nil || got.Error != "" || got.Changed || got.Ref != "" {
			t.Errorf("%s json: err=%v out=%q", name, err, js)
		}
	}
}
