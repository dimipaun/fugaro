package cli

// U4 of docs/plugin-user-scope/implementation-plan.md (D948, D950): one
// command. `fugaro upgrade` stops existing — cobra's unknown command, no
// tombstone — and the read-only mode it had is `fugaro update --check`'s.
//
// This file is the retirement's own and the mode's own: the two red tests the
// task opened with, in the shape left_commands_test.go gives the W1.2 one
// (TestLsIsGone is the other, the tombstone #350 deleted), and the cases that
// were `fugaro upgrade`'s about the one mode that survived it — the four steps
// it answers, what it refuses, and what a development build does with it
// (upgrade_test.go, which went with the command).

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// convergeEnv is a hermetic machine for a converge at version v: no coding
// agent's session, nothing but the system tools on PATH, and empty Claude Code
// and Fugaro user configs. The name is the retired `fugaro upgrade`'s and says
// what the fixture is: a machine an upgrade run reads, whatever door runs it.
func convergeEnv(t *testing.T, v string) {
	t.Helper()
	withVersion(t, v)
	useSelf(t)
	for _, k := range agentMarkers {
		t.Setenv(k, "")
	}
	t.Setenv("PATH", strings.Join([]string{t.TempDir(), "/usr/bin", "/bin"}, string(os.PathListSeparator)))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// No test finds the developer's own claude: the plugin step looks it up
	// through claudeLookPath, which finds nothing unless a test hands it the
	// fake (useFakeClaude).
	old := claudeLookPath
	claudeLookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	t.Cleanup(func() { claudeLookPath = old })
}

// convergeRead is a file's contents, "" when it does not exist: what a case
// comparing a file before and after a run reads it with.
func convergeRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

// TestUpgradeIsGone: the name resolves to nothing.
func TestUpgradeIsGone(t *testing.T) {
	root := NewRootCmd()
	for _, c := range root.Commands() {
		if c.Name() == "upgrade" {
			t.Fatal("a top-level upgrade command is still registered: U4 folds it into fugaro update, with no tombstone (D950)")
		}
	}
	_, _, err := execute(t, "upgrade", "--check")
	if err == nil || !strings.Contains(err.Error(), `unknown command "upgrade"`) {
		t.Fatalf("fugaro upgrade = %v, want an unknown-command error (D950: not even a tombstone)", err)
	}
	// The exit code is part of the contract, not just the message: with the
	// command gone, `fugaro upgrade` is a typo to cobra, and a typo is a user
	// error (design v1 §9.1). Nothing here touched the cloud.
	if got := ExitCode(err); got != ExitUserError {
		t.Fatalf("fugaro upgrade exit code = %d, want %d (an unknown command is a user error)", got, ExitUserError)
	}
}

// TestUpdateTakesCheckAndNotLocal: what survived of the command's flags.
// `--check` is added to `fugaro update` (read-only, no claude call, no cloud
// call, exit 1 when stale), and `--local` is not: the LocalUnderAgent marker
// absorbs it and `fugaro update-skills` is the plugin-only command.
func TestUpdateTakesCheckAndNotLocal(t *testing.T) {
	update := updateCmdOf(t)
	if update.Flags().Lookup("check") == nil {
		t.Error("fugaro update has no --check: the read-only mode `fugaro upgrade --check` had is this flag's now (D948)")
	}
	if update.Flags().Lookup("local") != nil {
		t.Error("fugaro update takes --local, which D948 dropped: a marker is set by the session and not by a person's roles, so the local half is what plain fugaro update does under one")
	}
}

// TestSetupTakesNoCheck: the mode is the update door's alone. `fugaro setup
// --check` would be a fourth thing to keep in step, and the read-only question
// about an installation that does not exist yet has no answer — so setup
// declares it the way it declares `--forget`, hidden and refused by name
// (setupDoor.foreign), rather than answering it.
func TestSetupTakesNoCheck(t *testing.T) {
	setup := setupCmdOf(t)
	if setup.Flags().Lookup("check") == nil {
		t.Fatal("fugaro setup does not declare --check: it is refused by name rather than left to cobra's unknown-flag error, the shape --forget has on that door")
	}
	if setup.Flags().Lookup("check").Hidden != true {
		t.Error("fugaro setup offers --check in its help: the flag it refuses must not be offered")
	}
	_, _, err := execute(t, "setup", "--check")
	if err == nil || !strings.Contains(err.Error(), "fugaro setup does not take --check") {
		t.Fatalf("fugaro setup --check = %v, want the refusal that names the door which takes it", err)
	}
}

func updateCmdOf(t *testing.T) *cobra.Command {
	t.Helper()
	return doorCmdOf(t, "update")
}

func setupCmdOf(t *testing.T) *cobra.Command {
	t.Helper()
	return doorCmdOf(t, "setup")
}

func doorCmdOf(t *testing.T, name string) *cobra.Command {
	t.Helper()
	for _, c := range NewRootCmd().Commands() {
		if c.Name() == name {
			return c
		}
	}
	t.Fatalf("no `fugaro %s` in this build", name)
	return nil
}

// TestUpdateCheckReportsEveryStep: the mode prints the converge's own four
// steps, in their order, and each one's own state: here the plugin is skipped
// (Claude Code's record of installed plugins has none), the cloud is skipped
// because no local project config selects this checkout, and the two steps
// whose answer lives in the user's project say they cannot tell — the mode
// makes no cloud call. With no step
// stale the checkout is current and the exit is 0. The steps are four: the pin
// went with the merge it wrote (U2), and rules and policy are stages of the
// converge, not steps of a second command (U4).
func TestUpdateCheckReportsEveryStep(t *testing.T) {
	var names []string
	for _, s := range checkSteps {
		names = append(names, s.name)
	}
	if got := strings.Join(names, ","); got != "plugin,cloud,rules,policy" {
		t.Fatalf("checkSteps = %s", got)
	}
	convergeEnv(t, "0.5.2")
	root, settings := skillCheckout(t, wiredAt("v0.5.2"))
	out, _, err := executeStdin(t, "", "update", "--check", root)
	if err != nil || convergeRead(t, settings) != wiredAt("v0.5.2") {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		"fugaro update --check with fugaro 0.5.2",
		"== " + root,
		"plugin: skipped: not installed on this machine (fugaro update-skills installs it through the claude CLI)",
		"cloud: skipped: no local project config selects this checkout",
		"rules: skipped: --check makes no cloud call, so the deployed security rules are not read",
		"policy: skipped: --check makes no cloud call, so the published policy node is not read",
		"nothing to do: " + root + " is current (apart from what the skipped steps name)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	for _, bad := range []string{"not implemented", "not checked:", "plugin: current", "cloud: current", "rules: current", "policy: current"} {
		if strings.Contains(out, bad) {
			t.Errorf("output claims %q of a step that is skipped here:\n%s", bad, out)
		}
	}
}

// TestUpdateCheckRefusesTheConvergeFlags: --check runs none of the converge, so
// the door's own flags are refused by name rather than silently ignored — a run
// that passed one would otherwise read as if it had honoured it. The checkout
// is PATH in this mode (`fugaro update --check [PATH]`), which is why --repo is
// on the refused side: it says the run onboards the repository of the checkout
// at PATH, and this run onboards nothing.
func TestUpdateCheckRefusesTheConvergeFlags(t *testing.T) {
	convergeEnv(t, "0.5.2")
	root, settings := skillCheckout(t, wiredAt("v0.5.1"))
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--check", "--yes"}, "--yes is the converge's"},
		{[]string{"--check", "--repo"}, "--repo is the converge's"},
		{[]string{"--check", "--plan-only"}, "--plan-only is the converge's"},
		{[]string{"--check", "--json"}, "--json is the converge's"},
		{[]string{"--check", "--forget"}, "--forget is the converge's"},
	} {
		args := append(append([]string{"update"}, tc.args...), root)
		out, _, err := executeStdin(t, "", args...)
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), tc.want) || strings.Contains(out, "== ") {
			t.Errorf("%q: exit %d, err %v\n%s", tc.args, ExitCode(err), err, out)
		}
	}
	if convergeRead(t, settings) != wiredAt("v0.5.1") {
		t.Error("a refused mode wrote the settings")
	}
	// The flags the mode does read are the cloud step's own, and the positional
	// checkout: a run that passes one of them beside --check is the mode, not a
	// refusal.
	if _, _, err := executeStdin(t, "", "update", "--check", "--region", "europe-west1", root); err != nil {
		t.Errorf("--check refused a flag it reads: %v", err)
	}
}

// TestUpdateCheckRefusesWhatIsNotACheckout: the mode reads the checkout, so a
// path that is not one — or a checkout Fugaro is not set up in — is the mode's
// own refusal, exit 1, and no step is run.
func TestUpdateCheckRefusesWhatIsNotACheckout(t *testing.T) {
	convergeEnv(t, "0.5.2")
	notCheckout := t.TempDir()
	plain := plainCheckout(t) // a checkout, but not a Fugaro one
	for _, tc := range []struct {
		path, want string
		steps      string
	}{
		{notCheckout, "not in a git checkout", ""},
		{plain, "not a Fugaro checkout", ""},
	} {
		out, _, err := executeStdin(t, "", "update", "--check", tc.path)
		if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: exit %d, err %v\n%s", tc.path, ExitCode(err), err, out)
		}
		if !strings.Contains(out, "checkout: failed: "+tc.want) {
			t.Errorf("%s: the refusal is not reported as the checkout's own:\n%s", tc.path, out)
		}
		for _, step := range []string{"plugin:", "cloud:", "rules:", "policy:"} {
			if strings.Contains(out, step) {
				t.Errorf("%s: a step ran for a path the mode refused (%s):\n%s", tc.path, step, out)
			}
		}
	}
}

// TestUpdateCheckWritesNothing is the mode's one promise: it reads Claude
// Code's record of installed plugins and this checkout's own files, and writes
// none of them — the settings included, where a pin an older fugaro left stands
// whatever the machine's install says. It exits 1 because something is stale,
// and the line that fixes it is the converge, not a second check.
func TestUpdateCheckWritesNothing(t *testing.T) {
	convergeEnv(t, "0.5.2")
	record := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "plugins", "installed_plugins.json")
	checkoutFile(t, record, `{"version":2,"plugins":{"fugaro@fugaro":[{"scope":"user","version":"0.5.1"}]}}`)
	root, settings := skillCheckout(t, wiredAt("v0.5.1"))
	before := convergeRead(t, settings)
	out, _, err := executeStdin(t, "", "update", "--check", root)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "something is stale") {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	if convergeRead(t, settings) != before || !strings.Contains(out, "plugin: stale: installed 0.5.1, this fugaro is 0.5.2") {
		t.Fatalf("changed %v\n%s", convergeRead(t, settings) != before, out)
	}
	// The stale line hands over the converge for the same checkout: a second
	// --check would only report the same staleness.
	if !strings.Contains(out, "stale: run "+selfCommand()+" update --repo "+root+" in your own terminal window") {
		t.Errorf("the stale line does not name the converge for this checkout:\n%s", out)
	}
}

// TestUpdateCheckDevBuild: U11 on the mode — a development build has no release
// to install or refresh to, so every step is skipped and the run exits 0,
// whatever the checkout holds and whatever path it is.
func TestUpdateCheckDevBuild(t *testing.T) {
	convergeEnv(t, "dev")
	root, settings := skillCheckout(t, wiredAt("v0.5.1"))
	before := convergeRead(t, settings)
	out, _, err := executeStdin(t, "", "update", "--check", root)
	if err != nil || convergeRead(t, settings) != before {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
	for _, want := range []string{"a development build (dev)", "plugin: skipped: a development build", "policy: skipped: a development build"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "stale") || strings.Contains(out, "is current") {
		t.Errorf("a development build judged the checkout:\n%s", out)
	}
	// The checkout is still the mode's argument, and the development build does
	// not change that: a path that is not one is the mode's own refusal, before
	// any step is asked anything.
	notCheckout := t.TempDir()
	out, _, err = executeStdin(t, "", "update", "--check", notCheckout)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "not in a git checkout") ||
		strings.Contains(out, "plugin: skipped: a development build") {
		t.Fatalf("exit %d, err %v\n%s", ExitCode(err), err, out)
	}
}
