package cli

// Checks 310 and 311 — the default output on a real terminal, and the same on
// Windows — as the register writes them.
//
// CL12 of docs/cli-output-console-graph/implementation-plan-output.md writes the
// two owner-run halves of the output work's last slice. 310 is macOS or Linux
// with a real pty (the watched command runs under `script`), a real pipe and the
// real log files a converge leaves behind; 311 is the same five commands on
// Windows, where whether a console draws an escape sequence at all is the
// console's own answer, and where there is no `script` to borrow a pty with. The
// plan reserves both numbers and says the sections are written by the task that
// implements the slice, as **NOT RUN**; this file holds the register to that, and
// holds the two scripts to the readings their own sections promise.
//
// A check is registered by two things — a row in the summary table and a
// `## Check N:` section below it — and either half alone is a number a reader
// cannot use: a section without its row is a check nothing points at, and a row
// without its section is a promise of steps that are not written. The backstop
// that would assert the pairing register-wide is deliberately absent
// (docs_compute_backend_test.go asserts its absence), so each work's own docs
// tests carry it for its own numbers.
//
// The red step is this file run against a register that has neither half: the
// first failure names the row that is missing, the second the section.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	clOutputPlan = "docs/cli-output-console-graph/implementation-plan-output.md"
	// The plan as the register links it: the two halves write the link target,
	// which is the path under docs/ with the prefix in the link's own text.
	clOutputPlanLink = "cli-output-console-graph/implementation-plan-output.md"
	// The two numbers this work writes, and the task that writes them.
	clCheck310 = "310"
	clCheck311 = "311"
	// The invocation each row and section documents. 310's script refuses to
	// run without a project it may not fall back for, and 311's without one for
	// the same reason, so a register that names the script alone documents a
	// command that stops with a usage error before it measures anything. The two
	// halves carry the same string, and folding the section's whitespace is what
	// lets one constant pin both.
	clRun310 = "bash scripts/live-checks/check310.sh --project <fugaro project>"
	clRun311 = "pwsh -NoProfile -File scripts/live-checks/check311.ps1 -Project <fugaro project>"
)

// clChecks are the two numbers this work writes: the number, the command the
// owner types, the script and the sibling test that drives it.
var clChecks = []struct{ number, run, script, scriptTest string }{
	{clCheck310, clRun310, "scripts/live-checks/check310.sh", "scripts/live-checks/check310_test.sh"},
	{clCheck311, clRun311, "scripts/live-checks/check311.ps1", "scripts/live-checks/check311_test.sh"},
}

// TestDocsChecks310311AreRegistered: each of the two has both halves of its
// registration — a row in the summary table and a `## Check N:` section below it
// — and both say the check has not been run, that the owner runs it, that it is
// never pointed at `belong`, and that no agent runs any step of it. Each section
// also names the one command the owner types, the sibling test beside the script
// and the task that wrote it, because a check that ships as a script is
// registered by naming it: a section whose steps are a second, hand-kept list is
// how the register and the script come to disagree.
func TestDocsChecks310311AreRegistered(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	for _, c := range clChecks {
		row := bitbucketWorkspaceCheckRowRE(c.number).FindString(register)
		if row == "" {
			t.Errorf("%s's register has no `| %s |` row: Check %s is written below it, and the table is where a reader finds it", liveChecksDoc, c.number, c.number)
		} else {
			// Lowercased the way docs_checks_180_181_test.go reads its own rows:
			// the register writes the marker in capitals and the pins below are
			// the sentence, not its case.
			flatRow := strings.ToLower(flattenDoc(row))
			for _, want := range []string{
				// The register's own marker for an owner-run check that has not
				// been run: a row that lost either half reads as something an
				// agent did.
				"**user-run, not run.**",
				"no agent runs any step of it",
				// The installation this check must never touch.
				"never `belong`",
				// Where the owner's one command and its sibling test live: the
				// row is the half a reader scans, so the script has to be named
				// there and not only in the section below — and named with the
				// argument it refuses to run without.
				strings.ToLower(c.run),
				c.scriptTest,
				// The task that wrote it and the plan it comes from.
				"cl12",
				clOutputPlanLink,
			} {
				if !strings.Contains(flatRow, want) {
					t.Errorf("Check %s's row does not say %q: an owner-run check is registered with the label it was run under and the script you type", c.number, want)
				}
			}
		}

		heading := "\n## Check " + c.number + ":"
		i := strings.Index(register, heading)
		if i < 0 {
			t.Errorf("%s has no `## Check %s:` section: the row above points at steps that have to be there", liveChecksDoc, c.number)
			continue
		}
		// Bounded by the next `## Check` heading, so a check written after this
		// one does not widen what is read here.
		section := register[i+1:]
		if end := strings.Index(section, "\n## Check "); end >= 0 {
			section = section[:end]
		}
		// Flattened and folded, because the register's prose wraps; lowercased
		// for the same reason as the row above.
		flat := strings.ToLower(flattenDoc(section))
		for _, want := range []string{
			"**not run.**",
			"no agent runs any step of it",
			"never `belong`",
			strings.ToLower(c.run),
			c.scriptTest,
			"cl12",
			clOutputPlanLink,
		} {
			if !strings.Contains(flat, want) {
				t.Errorf("Check %s's section does not say %q: the check is owner-run and has not been run, and a section that leaves either half out reads as something an agent did", c.number, want)
			}
		}

		// The scripts and their sibling tests are really there: a row naming a
		// file nobody wrote is a promise.
		for _, p := range []string{c.script, c.scriptTest} {
			if _, err := os.Stat(filepath.Join("..", "..", p)); err != nil {
				t.Errorf("Check %s's section names %s and there is no such file: %v", c.number, p, err)
			}
		}
		// The Go runner in scripts/live_checks_test.go drives every check's
		// sibling test; a check added without its entry is a test no job runs.
		runner := readDoc(t, "scripts/live_checks_test.go")
		if !strings.Contains(runner, `{"`+filepath.Base(c.script)+`", "`+filepath.Base(c.scriptTest)+`"}`) {
			t.Errorf("scripts/live_checks_test.go does not list {%q, %q}: every check's sibling test is driven from that table, and a check missing from it is one no job runs", filepath.Base(c.script), filepath.Base(c.scriptTest))
		}
	}

	// The two run in number order, so a reader who follows the table top to
	// bottom meets the sections in the same order.
	at := 0
	for _, c := range clChecks {
		i := strings.Index(register[at:], "\n## Check "+c.number+":")
		if i < 0 {
			t.Errorf("%s writes Check %s's section out of order: the block reads in the order the numbers give", liveChecksDoc, c.number)
			continue
		}
		at += i
	}
}

// TestDocsCheck310StatesWhatOnlyTheRunSettles: the section has to carry the
// readings the check exists for, in the words the owner follows, because CL12
// writes the script and no other document holds its steps. Each sentence here is
// one no offline test can produce: a real pty, a real pipe, the log files a real
// converge leaves, and the one question a script cannot answer.
func TestDocsCheck310StatesWhatOnlyTheRunSettles(t *testing.T) {
	section := clSection(t, clCheck310)
	for _, want := range []string{
		// The pty, borrowed with `script`: the package's own tests are told
		// whether their writer is a terminal, and this is the reading that is
		// not told.
		"`script`",
		"pty",
		// The two readings the reporter's own rule decides, one with something
		// to turn off and one with nothing to turn on.
		"`NO_COLOR=1`",
		"no escape byte at all",
		// The one document on stdout, and where the human lines went instead.
		"ONE document on stdout",
		"stderr",
		// The forced failure: the step's line, the context, the log's path and
		// the troubleshoot line whose id is that file's own stem (D1352).
		"full output: <path>",
		"Something went wrong. Run: fugaro troubleshoot <id>",
		// The log directory's own rules, read on files this check plants.
		"0600",
		"0700",
		"newest 50",
		"twenty days ago",
		// The plan view prints no closing line by design, said rather than left
		// to the reader to read as a fault.
		"no summary at all",
		// The one reading a script cannot take, and how it is answered.
		"yes/no question",
		// The two knobs an owner changes, and the guard.
		"`--phase look`",
		"`--runs N`",
		"never `belong`",
	} {
		if !strings.Contains(section, strings.Join(strings.Fields(want), " ")) {
			t.Errorf("Check 310's section does not say %q: the check is the only place these facts are ever measured, and CL12 writes it", want)
		}
	}
}

// TestDocsCheck311StatesWhatOnlyTheRunSettles: 311 is the Windows half, and its
// section has to say what only a Windows console settles — the code page, the
// virtual-terminal answer, the ACL that stands in for a POSIX mode — and what
// the owner is asked, since Windows has no `script` to read a pty with.
func TestDocsCheck311StatesWhatOnlyTheRunSettles(t *testing.T) {
	section := clSection(t, clCheck311)
	for _, want := range []string{
		// The two consoles, and that the check needs a Windows machine: the
		// register has to say so, or an owner on a Mac runs a script that stops.
		"**This check needs a Windows machine.**",
		"Windows Terminal",
		"legacy console",
		// What the console answers about itself, and the code page it draws the
		// marks with.
		"`$Host.UI.SupportsVirtualTerminal`",
		"code page",
		// Windows has no mode, so the protection reading is the ACL.
		"ACL",
		"0600",
		// The two readings that are the owner's, and the hint that tells a
		// shell's own handling of a native command from the CLI's.
		"two yes/no questions",
		"`cmd /c fugaro update`",
		// The two modes that run nothing off Windows, which is where the
		// sibling test's pwsh-absent line leaves a reader.
		"`-SelfTest`",
		"`-DryRun`",
		// Retention is deliberately not repeated here, said rather than left to
		// the reader to wonder why 310's half is missing.
		"does **not** repeat 310's retention half",
		// The guard.
		"never `belong`",
	} {
		if !strings.Contains(section, strings.Join(strings.Fields(want), " ")) {
			t.Errorf("Check 311's section does not say %q: the check is the only place these facts are ever measured, and CL12 writes it", want)
		}
	}
}

// TestDocsCheck311SaysTheConsoleIsNotTheLogs: the one thing the two sections must
// not be read as duplicating. 310's retention half is 51 forced-failure runs
// against the log directory, and 311 deliberately does not repeat it; a section
// that dropped the sentence would read as an omission, and one that copied the
// half would make an owner run 51 converges twice for a fact about a directory.
func TestDocsCheck311SaysTheConsoleIsNotTheLogs(t *testing.T) {
	section := clSection(t, clCheck311)
	for _, want := range []string{
		"retention is a property of the log directory, not of the console",
		"does **not** repeat 310's retention half",
	} {
		if !strings.Contains(section, strings.Join(strings.Fields(want), " ")) {
			t.Errorf("Check 311's section does not say %q: the register has to say why the Windows check stops where it does", want)
		}
	}
	// And the row says the same, so a reader who only scans the table is not
	// left thinking the Windows check measured the retention too.
	row := strings.ToLower(flattenDoc(bitbucketWorkspaceCheckRowRE(clCheck311).FindString(readDoc(t, liveChecksDoc))))
	if !strings.Contains(row, "not") || !strings.Contains(row, "retention") {
		t.Errorf("Check 311's row does not say the retention half is not repeated: the row is the half a reader scans")
	}
}

// clSection is the register's text for one check, flattened and folded.
func clSection(t *testing.T, number string) string {
	t.Helper()
	register := readDoc(t, liveChecksDoc)
	i := strings.Index(register, "\n## Check "+number+":")
	if i < 0 {
		t.Fatalf("%s has no `## Check %s:` section: the test above is the one that says it is missing", liveChecksDoc, number)
	}
	section := register[i+1:]
	if end := strings.Index(section, "\n## Check "); end >= 0 {
		section = section[:end]
	}
	return flattenDoc(section)
}

// TestDocsOutputPlanNamesTheTwoChecks: the plan whose rows 310 and 311 this work
// writes has to name both, say the owner runs them, and say the sections are
// written by the task that implements the slice — a plan whose checks are only
// numbers in a table is one a reader cannot tell was ever written.
func TestDocsOutputPlanNamesTheTwoChecks(t *testing.T) {
	plan := flattenDoc(readDoc(t, clOutputPlan))
	for _, want := range []string{
		"| **310** |",
		"| **311** |",
		"the owner",
		"The check sections are written into `docs/gcp-live-checklist.md` by the task that implements the first slice that needs them, as **NOT RUN**",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s does not say %q: the plan's own rows are what CL12 implements", clOutputPlan, want)
		}
	}
}
