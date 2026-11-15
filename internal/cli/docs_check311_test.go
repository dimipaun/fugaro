package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Check 311 — the same default output on Windows, in Windows Terminal and in
// the legacy console — as the register writes it.
//
// CL12 of docs/cli-output-console-graph/implementation-plan-output.md writes the
// two owner-run halves of the output work's last slice: 310 on macOS or Linux,
// with a real pty borrowed through `script`, and 311 on Windows, where whether a
// console draws an escape sequence at all is the console's own answer and there
// is no `script` to borrow a pty with. 310's script and its section are on main;
// this file carries 311's, which ships as one PowerShell script rather than as a
// list of steps, because a hand-kept procedure beside a script is how the two
// come to disagree.
//
// A check is registered by two things — a row in the summary table and a
// `## Check N:` section below it — and either half alone is a number a reader
// cannot use: a section without its row is a check nothing points at, and a row
// without its section is a promise of steps that are not written. The
// register-wide backstop that would assert the pairing is deliberately absent
// (docs_compute_backend_test.go asserts its absence), so each work's own docs
// tests carry it for its own numbers.
const check311Number = "311"

// check311Run is the ONE command the owner types, as the row and the section
// both print it. The script refuses to run without a project it may not fall
// back for, so a register that named the script alone would document a command
// that stops on its own guard before it measures anything.
const check311Run = "pwsh -NoProfile -File scripts/live-checks/check311.ps1 -Project <fugaro project>"

// check311PlanDoc is the plan whose CL12 row 311 this work implements.
const check311PlanDoc = "docs/cli-output-console-graph/implementation-plan-output.md"

// TestDocsCheck311IsRegistered: the check has both halves of its registration,
// each half says it has not been run, that the owner runs it, that it is never
// pointed at `belong` and that no agent runs any step of it, and the section
// names the one command the owner types and the sibling test beside the script.
func TestDocsCheck311IsRegistered(t *testing.T) {
	register := readDoc(t, liveChecksDoc)

	// Lowercased, because the register writes its marker in capitals and the
	// pins below are the sentence and not its case.
	row := strings.ToLower(flattenDoc(registerRow(t, register, check311Number)))
	for _, want := range []string{
		// The register's own marker for an owner-run check that has not been
		// run: a row that lost either half reads as something an agent did.
		"**user-run, not run.**",
		"no agent runs any step of it",
		// The installation and the session this check must never touch.
		"never `belong`",
		strings.ToLower(check311Run),
		"scripts/live-checks/check311_test.sh",
		// The task that wrote it and the plan it comes from.
		"cl12",
		"cli-output-console-graph/implementation-plan-output.md",
		// The half of 310 this check deliberately does not repeat, said in the
		// row and not only in the section: the row is the half a reader scans.
		"retention",
	} {
		if !strings.Contains(row, want) {
			t.Errorf("Check %s's row does not say %q: an owner-run check is registered with the label it was run under, the command you type and the half it does not repeat", check311Number, want)
		}
	}

	// Flattened and folded, because the register's prose wraps: a pin that
	// missed a phrase across a line break would be a pin on the line breaks.
	section := strings.ToLower(flattenDoc(registerSection(t, register, "\n## Check "+check311Number+":")))
	for _, want := range []string{
		"**not run.**",
		"no agent runs any step of it",
		"never `belong`",
		strings.ToLower(check311Run),
		"scripts/live-checks/check311_test.sh",
		"cl12",
		"cli-output-console-graph/implementation-plan-output.md",
		// The one reading the macOS and Linux half cannot take, and that the
		// check needs a machine this repository's tests cannot reach.
		"**this check needs a windows machine.**",
		"windows terminal",
		"legacy console",
		"so the two can be compared line for line",
		// What the console answers about itself, and the code page it draws the
		// marks with — read and reported, never set.
		"`$host.ui.supportsvirtualterminal`",
		"code page",
		// Windows has no POSIX mode, so the protection reading is the ACL.
		"acl",
		"0600",
		// The two readings that are the owner's, since Windows has no `script`
		// to read a pty with, and the hint that tells a shell's own handling of
		// a native command from the CLI's.
		"two yes/no questions",
		"the spinner",
		"`cmd /c fugaro update`",
		// The two modes that run nothing off Windows, which is where the sibling
		// test's pwsh-absent line leaves a reader.
		"`-selftest`",
		"`-dryrun`",
		// The knobs an owner changes, and the variable the sibling test uses.
		"`-phase all`",
		"`-fugaroflags '<flags>'`",
		"check311_fugaro",
		// Retention is deliberately not repeated here, said rather than left to
		// the reader to wonder why 310's half is missing.
		"does **not** repeat 310's retention half",
		"retention is a property of the log directory, not of the console",
		// The two refusals, which are rows of the block and both end the run 2.
		"refuses two sessions before it reads anything",
		"internal/agentenv.session",
		"claude_code_sse_port",
		"refused with exit 2",
		// The block's own shape, and the two rows that are the owner's answers.
		"`check 311 result`",
		"`check 311 result end (exit n)`",
		"**`agent`**",
		"**`console-view`**",
		"**`no-color`**",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check %s's section does not say %q: the check is the only place these facts are ever measured, and CL12 writes it", check311Number, want)
		}
	}

	// The scripts and the sibling test are really there: a row naming a file
	// nobody wrote is a promise.
	for _, p := range []string{"scripts/live-checks/check311.ps1", "scripts/live-checks/check311_test.sh"} {
		if _, err := os.Stat(filepath.Join("..", "..", p)); err != nil {
			t.Errorf("Check %s's section names %s and there is no such file: %v", check311Number, p, err)
		}
	}
	// The Go runner in scripts/live_checks_test.go drives every check's sibling
	// test; a check added without its entry is a test no job runs.
	runner := readDoc(t, "scripts/live_checks_test.go")
	if !strings.Contains(runner, `{"check311.ps1", "check311_test.sh"}`) {
		t.Errorf("scripts/live_checks_test.go does not list {\"check311.ps1\", \"check311_test.sh\"}: every check's sibling test is driven from that table, and a check missing from it is one no job runs")
	}
}

// TestDocsCheck311StatesWhatOnlyTheRunSettles: the section has to carry the
// readings the check exists for, in the words the owner follows, because CL12
// writes the script and no other document holds its steps. Each sentence here is
// one no offline test can produce: the console's own answer about virtual
// terminal sequences, the ACL that stands in for a POSIX mode, and the one
// question a script cannot answer.
func TestDocsCheck311StatesWhatOnlyTheRunSettles(t *testing.T) {
	section := strings.ToLower(flattenDoc(registerSection(t, readDoc(t, liveChecksDoc), "\n## Check "+check311Number+":")))
	for _, want := range []string{
		// The console host's own answer, which is what `termenv`'s ANSI profile
		// is asked before a colour is written, and what settles whether an
		// escape sequence is drawn or printed literally.
		"`$host.ui.supportsvirtualterminal`",
		"printed literally",
		// The account the two consoles are compared on.
		"the line in flight",
		// The reading a Windows console has and a POSIX one does not.
		"windows has no 0600",
		// The Windows shell's stand-in for `| cat`.
		"a pipe",
		// The failure's own lines, and the troubleshoot id that is the log's
		// stem.
		"something went wrong. run: fugaro troubleshoot <id>",
		"<command>-<14 digits utc>-<pid>.log",
		// The two writes the check makes, and the one it never makes.
		"%temp%",
		"never set",
		// The one reading that is not a verdict, and the exit that says so.
		"2 is a reading the check could not take",
		// What the owner pastes back, since the block is not a substitute for
		// the two consoles' own output.
		"exit 0 is every fact held",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check %s's section does not say %q: the check is the only place these facts are ever measured, and CL12 writes it", check311Number, want)
		}
	}
}

// TestDocsCheck311SaysTheConsoleIsNotTheLogs: the one thing the two sections
// must not be read as duplicating. 310's retention half is 51 forced-failure
// runs against the log directory, and 311 deliberately does not repeat it; a
// section that dropped the sentence would read as an omission, and one that
// copied the half would make an owner run 51 converges twice for a fact about a
// directory.
func TestDocsCheck311SaysTheConsoleIsNotTheLogs(t *testing.T) {
	section := strings.ToLower(flattenDoc(registerSection(t, readDoc(t, liveChecksDoc), "\n## Check "+check311Number+":")))
	for _, want := range []string{
		"does **not** repeat 310's retention half",
		"retention is a property of the log directory, not of the console",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check %s's section does not say %q: the register has to say why the Windows check stops where it does", check311Number, want)
		}
	}
}

// TestDocsOutputPlanSays311sScriptExists: the plan whose 311 row this work
// implements reserved the number and left the section to the task that writes
// the check. A plan still reading as a table of steps would send a reader
// looking for a procedure the register no longer carries, so its row names the
// script, the sibling test and the section the two halves live in.
func TestDocsOutputPlanSays311sScriptExists(t *testing.T) {
	plan := readDoc(t, check311PlanDoc)
	row := strings.ToLower(flattenDoc(registerRow(t, plan, "**"+check311Number+"**")))
	for _, want := range []string{
		"the owner",
		strings.ToLower(check311Run),
		"scripts/live-checks/check311_test.sh",
		"docs/gcp-live-checklist.md",
		"twice",
	} {
		if !strings.Contains(row, want) {
			t.Errorf("%s's 311 row does not say %q: the plan's own row is what CL12 implements, and a row that names no script is the procedure this work replaced", check311PlanDoc, want)
		}
	}
	// And the plan still says the sections are the register's and not its own,
	// which is the sentence that keeps the two documents from both claiming to
	// hold the steps.
	if !strings.Contains(flattenDoc(plan), "The check sections are written into `docs/gcp-live-checklist.md` by the task that implements the first slice that needs them, as **NOT RUN**") {
		t.Errorf("%s no longer says the check sections live in the register: the plan's rows point at them and hold none of them", check311PlanDoc)
	}
}
