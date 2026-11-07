package cli

// report_writers_test.go is CL7b: the project and config writers — `config
// publish`, `allow-delete`, the managed family, `restore` and the budget's
// writers — print through the reporter. Each of them opens its own log under
// its own command name (D1352), and its account on the screen is one line per
// unit of work with the reporter's closing line last (D1354).
//
// This file is the batch's red step: on the tree before the move every test
// here fails, because those commands print free-form lines and keep no log at
// all (onlyLog finds no directory to read).

import (
	"regexp"
	"strings"
	"testing"
)

// closing is the reporter's last line: the three outcomes of G3, with the
// run's own elapsed time.
var closing = regexp.MustCompile(`^(Done|Failed|Left for you) in [0-9.]+s, [0-9]+ steps?$`)

// reported is the account one migrated command owes: the run ends with the
// reporter's closing line, and every unit of work it named has a step line of
// its own. The step names are single tokens, which is what the reporter's own
// readers (stepName) take them to be.
func reported(t *testing.T, out string, steps ...string) {
	t.Helper()
	lines := reportLines(out)
	if len(lines) == 0 {
		t.Fatal("the command printed nothing at all")
	}
	if last := lines[len(lines)-1]; !closing.MatchString(last) {
		t.Errorf("the run does not end with the reporter's closing line, it ends with %q:\n%s", last, out)
	}
	for _, name := range steps {
		if stepLineOf(lines, name) == "" {
			t.Errorf("no step line names %q:\n%s", name, out)
		}
	}
}

// writesALog is D1352's half of the batch: the run kept a log filed under its
// own command name, which is also what a failure's "full output:" line points
// at. A run that kept no log has no directory to read and fails here.
func writesALog(t *testing.T, cmdName string) string {
	t.Helper()
	path, content := onlyLog(t, cmdName)
	if !strings.Contains(content, "\n") && content != "" {
		t.Errorf("%s's log is not a log of lines: %q", cmdName, content)
	}
	return path
}

// ---------------------------------------------------------------- publish

func TestConfigPublishDefaultIsShort(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "")
	noAgentSession(t)
	out, _, err := execute(t, "config", "publish", "--executable-changes", layerFile(t, testProjectLayer))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// The layer is one step and every repository it fans out to is one of its
	// own, rather than the indented "  acme/app: copied to …" prose they were.
	reported(t, out, "layer", "acme/app")
}

func TestConfigPublishWritesALog(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "")
	noAgentSession(t)
	if _, _, err := execute(t, "config", "publish", "--executable-changes", layerFile(t, testProjectLayer)); err != nil {
		t.Fatal(err)
	}
	writesALog(t, "config-publish")
}

// ----------------------------------------------------------- allow-delete

func TestAllowDeleteDefaultIsShort(t *testing.T) {
	allowDeleteRig(t)
	out, _, err := execute(t, allowDeleteArgs(webApproval)...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	reported(t, out, "delete-list")
	if !strings.Contains(out, "may now delete or replace 1 object(s)") {
		t.Errorf("the step does not say what the write approved:\n%s", out)
	}
}

func TestAllowDeleteWritesALog(t *testing.T) {
	allowDeleteRig(t)
	if _, _, err := execute(t, allowDeleteArgs(webApproval)...); err != nil {
		t.Fatal(err)
	}
	writesALog(t, "allow-delete")
}

// ------------------------------------------------------------ managed flag

func TestManagedFlagDefaultIsShort(t *testing.T) {
	managedFlagRig(t)
	terminal(t, true)
	out, _, err := executeStdin(t, initProject+"\n", managedFlagArgs()...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	reported(t, out, "flag")
	if !strings.Contains(out, "read back") {
		t.Errorf("the step does not say the marker was read back:\n%s", out)
	}
}

func TestManagedFlagWritesALog(t *testing.T) {
	managedFlagRig(t)
	terminal(t, true)
	if _, _, err := executeStdin(t, initProject+"\n", managedFlagArgs()...); err != nil {
		t.Fatal(err)
	}
	writesALog(t, "managed-flag")
}

// ---------------------------------------------------------- managed unflag

func TestManagedUnflagDefaultIsShort(t *testing.T) {
	r := managedUnflagRig(t)
	if !managedFlagIsManaged(t, r) {
		t.Fatal("the rig's project does not read as managed before the command")
	}
	terminal(t, true)
	out, _, err := executeStdin(t, initProject+"\n", managedUnflagArgs()...)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	reported(t, out, "unflag")
	// What each half of the mark came off as stays on the screen beside the
	// step, because "was not there" is the difference between a project this
	// command converged and one it did nothing to.
	for _, want := range []string{"removed", "read back", "is not managed"} {
		if !strings.Contains(out, want) {
			t.Errorf("the command does not say %q:\n%s", want, out)
		}
	}
}

func TestManagedUnflagWritesALog(t *testing.T) {
	managedUnflagRig(t)
	terminal(t, true)
	if _, _, err := executeStdin(t, initProject+"\n", managedUnflagArgs()...); err != nil {
		t.Fatal(err)
	}
	writesALog(t, "managed-unflag")
}

// ----------------------------------------------------------- managed apply

func TestManagedApplyDefaultIsShort(t *testing.T) {
	_, root, _ := managedOpenGateRig(t)
	out, errOut, err := execute(t, "managed", "apply", root)
	if err != nil && out == "" {
		t.Fatalf("the run printed no account at all: %v\n%s", err, errOut)
	}
	reported(t, out, "apply")
}

func TestManagedApplyWritesALog(t *testing.T) {
	_, root, _ := managedOpenGateRig(t)
	_, _, _ = execute(t, "managed", "apply", root)
	writesALog(t, "managed-apply")
}

// ----------------------------------------------------------------- restore

func TestRestoreDefaultIsShort(t *testing.T) {
	r, _ := newRestoreRig(t)
	restoreManagedOld(r)
	recordRestoreApply(t, r)
	_, root := restoreCheckout(t)
	out, err := restoreStepsWith(t, r, initProjectName+"\n"+createStdin+"y\n", root)
	if err != nil {
		t.Fatalf("the restore: %v\n%s", err, out)
	}
	reported(t, out, "config", "project", "checkout", "account")
}

func TestRestoreWritesALog(t *testing.T) {
	r, _ := newRestoreRig(t)
	restoreManagedOld(r)
	recordRestoreApply(t, r)
	_, root := restoreCheckout(t)
	if _, err := restoreStepsWith(t, r, initProjectName+"\n"+createStdin+"y\n", root); err != nil {
		t.Fatal(err)
	}
	writesALog(t, "restore")
}

// ------------------------------------------------------------ budget writes

func TestBudgetSetDefaultIsShort(t *testing.T) {
	newBudgetFixture(t, "")
	out, _, err := execute(t, "budget", "set", "--global", "--daily", "60", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	reported(t, out, nodeCG)
	if !strings.Contains(out, "daily $60") {
		t.Errorf("the step does not carry the value that was written:\n%s", out)
	}
}

func TestBudgetSetWritesALog(t *testing.T) {
	newBudgetFixture(t, "")
	if _, _, err := execute(t, "budget", "set", "--global", "--daily", "60", "--yes"); err != nil {
		t.Fatal(err)
	}
	writesALog(t, "budget-set")
}

func TestBudgetKillDefaultIsShort(t *testing.T) {
	newBudgetFixture(t, "")
	out, _, err := execute(t, "budget", "kill", "--all", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	reported(t, out, "project")
	if !strings.Contains(out, "killed") {
		t.Errorf("the step does not say the project was killed:\n%s", out)
	}
}

func TestBudgetKillWritesALog(t *testing.T) {
	newBudgetFixture(t, "")
	if _, _, err := execute(t, "budget", "kill", "--all", "--yes"); err != nil {
		t.Fatal(err)
	}
	writesALog(t, "budget-kill")
}

func TestBudgetResumeDefaultIsShort(t *testing.T) {
	newBudgetFixture(t, "")
	if _, _, err := execute(t, "budget", "kill", "--all", "--yes"); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, "budget", "resume", "--all", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	reported(t, out, "project")
	if !strings.Contains(out, "resumed") {
		t.Errorf("the step does not say the project was resumed:\n%s", out)
	}
}

func TestBudgetResumeWritesALog(t *testing.T) {
	newBudgetFixture(t, "")
	if _, _, err := execute(t, "budget", "kill", "--all", "--yes"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "budget", "resume", "--all", "--yes"); err != nil {
		t.Fatal(err)
	}
	writesALog(t, "budget-resume")
}

// TestNoStepCommandPrintsAnsiOffATerminal: a step-printing command's marks are
// plain where nothing is a terminal, whatever the environment says (G4). The
// reporter decides that once; this is the batch's own guard that no migrated
// command paints its own output.
func TestNoStepCommandPrintsAnsiOffATerminal(t *testing.T) {
	newBudgetFixture(t, "")
	for _, args := range [][]string{
		{"budget", "set", "--global", "--daily", "60", "--yes"},
		{"budget", "kill", "--all", "--yes"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			out, errOut, err := execute(t, args...)
			if err != nil {
				t.Fatalf("%v\n%s%s", err, out, errOut)
			}
			for _, s := range []string{out, errOut} {
				if strings.Contains(s, "\x1b") {
					t.Errorf("`fugaro %s` printed ANSI off a terminal:\n%q", strings.Join(args, " "), s)
				}
			}
			if !strings.Contains(out, markDone) && !strings.Contains(out, markSkipped) {
				t.Errorf("`fugaro %s` printed no step line:\n%s", strings.Join(args, " "), out)
			}
		})
	}
}

// TestTheWriterCommandsAreNamedWhatTheLogsAre: the log's name is the command
// name validateLogName accepts, so a name with a character the file name rule
// refuses would leave a run with no log and no error (OpenLog's refusal is
// silent by design, G5). Spelled here rather than read out of the reporter's
// own regexp, so a rename that broke the rule fails here.
func TestTheWriterCommandsAreNamedWhatTheLogsAre(t *testing.T) {
	re := regexp.MustCompile(`^[a-z][a-z0-9-]{0,40}$`)
	for _, name := range []string{
		"config-publish", "allow-delete", "managed-apply", "managed-flag",
		"managed-unflag", "restore", "budget-set", "budget-kill", "budget-resume",
	} {
		if !re.MatchString(name) {
			t.Errorf("fugaro %s files its log under %q, which is not a name the log rule takes", strings.ReplaceAll(name, "-", " "), name)
		}
	}
}
