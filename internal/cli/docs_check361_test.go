package cli

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Check 361 — the workspace's own Firebase's sign-in providers, W3's live check
// — as the register writes it.
//
// The number is the workspaces design's to take and not to claim: its
// reconciliation says so in as many words ("**Checks.** None is claimed", F22)
// and its plan §7 puts a live check's number out of the plan's own reach ("**Any
// live check, by number.**", numbered by whichever task writes it), so the check
// takes the first one free above the register's written sections.
//
// A check is registered by two things — a row in the summary table and a
// `## Check N:` section below it — and either half alone is a number a reader
// cannot use. Like Check 340's own test, this file carries the pairing for its
// number rather than the register-wide backstop that does not exist.
//
// The check is also why W3 ships a script rather than a list of steps: the
// providers of a workspace's Firebase are enabled by the owner and no hermetic
// test can hold what the project actually carries, so the owner's work has to be
// one command with its own guards. What is pinned here is that the register says
// so, that it names that command with the argument it requires, and that the
// script and its test are really there — a row pointing at a file nobody wrote is
// a promise.
const check361Number = "361"

// check361NumberRE reads the register's written sections as the numbers they
// carry.
var check361NumberRE = regexp.MustCompile(`(?m)^## Check ([0-9]+)`)

// check361Section returns Check 361's section, bounded by the next `## ` heading
// so that a check written after it does not widen what is read.
func check361Section(t *testing.T, register string) string {
	t.Helper()
	i := strings.Index(register, "\n## Check "+check361Number+":")
	if i < 0 {
		t.Fatalf("%s has no `## Check %s:` section: the row above it points at steps that have to be there", liveChecksDoc, check361Number)
	}
	section := register[i+1:]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	return section
}

// check361Row returns Check 361's summary row, or "" when the table has none.
func check361Row(register string) string {
	re := regexp.MustCompile(`(?m)^\|\s*` + check361Number + `\s*\|.*$`)
	return re.FindString(register)
}

// TestDocsCheck361IsRegistered: the check has both halves of its registration,
// each half says it has not been run, and the section names the ONE command the
// owner runs. The check is owner-run and unrun: a row or a section that read as a
// pass would be a live claim nobody made, and a section that listed steps instead
// of a script would be the hand work the owner's standing rule of 2026-11-07
// exists to remove.
func TestDocsCheck361IsRegistered(t *testing.T) {
	register := readDoc(t, liveChecksDoc)

	row := check361Row(register)
	if row == "" {
		t.Fatalf("%s's register has no `| %s |` row: Check %s is written below it, and the table is where a reader finds it", liveChecksDoc, check361Number, check361Number)
	}
	if !strings.Contains(row, "USER-RUN, NOT RUN.") {
		t.Errorf("Check %s's row does not read `USER-RUN, NOT RUN.`: the check is the owner's and has not been run, and a row that leaves either half out is read as a result", check361Number)
	}

	section := check361Section(t, register)
	// Flattened and folded, because the register's prose wraps: a pin that
	// missed a phrase across a line break would be a pin on the line breaks.
	flat := strings.ToLower(flattenDoc(section))
	for _, want := range []string{
		"**not run.**",
		// The register's own rule for an owner-run check: no agent runs any
		// step of it, and the text has to say so.
		"no agent runs any step of it",
		// The one command, with the argument it requires. The check refuses
		// without `--project`, so a section that printed a bare script name
		// would be a command that stops on its own guard.
		"bash scripts/live-checks/check361.sh --project <the workspace's own project>",
		// What the pin is, both halves of it: the providers it admits, and the
		// two claims it refuses.
		"`google.com` and `password`",
		"refuses `custom` and `anonymous`",
		"spec §3",
		// Why the step is the owner's at all: the installation path initializes
		// the project with no provider, and nothing in the tree polices a
		// workspace's, in either direction.
		"identity platform is initialized there with **no sign-in provider**",
		"does not police a workspace's providers in either direction",
		// The read is one GET and changes nothing, which is what makes it safe
		// to run again.
		"nothing is created, granted, written or deleted",
		// The reading that is not a verdict about the providers, and the one
		// that is: a refused read is exit `2`.
		"could not tell",
		"a refused read stops the run with exit `2`",
		// The credential is never printed and never on a command line.
		"no credential is printed, logged or put on a command line",
		"`ps` shows a process's argv to every user on the machine",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("Check %s's section does not say %q: the section is where the owner reads what the check settles and how to run it, and a pin that has moved is a section that stopped saying it", check361Number, want)
		}
	}

	// The script and its test are really there: the section names a command, and
	// a command naming a file nobody wrote is a promise rather than a check.
	for _, path := range []string{"scripts/live-checks/check361.sh", "scripts/live-checks/check361_test.sh"} {
		if _, err := os.Stat("../../" + path); err != nil {
			t.Errorf("%s is named by Check %s and does not exist: %v", path, check361Number, err)
		}
	}

	// Who wrote it, cited as the plan cites it: the task that implements a check
	// writes its row and its section, and the plan it belongs to is W3's.
	if !strings.Contains(flat, "the task that implemented check "+check361Number) {
		t.Errorf("Check %s's section does not say who wrote it (the task that implemented Check %s): the register's rule is that the task which implements a check writes its row and its section", check361Number, check361Number)
	}
	if !strings.Contains(flat, "workspaces/implementation-plan.md") {
		t.Errorf("Check %s's section does not name the plan that carries the task: a reader has to be able to look up what the check implements", check361Number)
	}
}

// TestDocsCheck361IsThePlansUnclaimedNumber: the register's number is one the
// design left to the task that writes the check. The workspaces design claims no
// number at all, and says so in both files — a design that claimed one and a
// register that used another would be two answers to the same question.
func TestDocsCheck361IsThePlansUnclaimedNumber(t *testing.T) {
	reconciliation := strings.ToLower(flattenDoc(readDoc(t, "docs/workspaces/reconciliation.md")))
	for _, want := range []string{
		"**checks.** none is claimed.",
		"take the next free numbers at the task that writes them (f22)",
	} {
		if !strings.Contains(reconciliation, want) {
			t.Errorf("docs/workspaces/reconciliation.md no longer says %q: the number this check took is the design's to allocate at the task that writes it, and a design that claims one would make the register's number the wrong one", want)
		}
	}
	plan := strings.ToLower(flattenDoc(readDoc(t, "docs/workspaces/implementation-plan.md")))
	if !strings.Contains(plan, "**any live check, by number.**") {
		t.Errorf("docs/workspaces/implementation-plan.md no longer puts a live check's number out of the plan's own reach (`**Any live check, by number.**`): the register's row and section are this task's to write, under the number free when it writes them")
	}
}

// TestDocsCheck361ComesAfterTheHighestWrittenSection: the section is appended
// where the register's written sections end rather than inserted among older
// ones, and its number is above every other written section's. A block is
// registered by appending to it, and a section written in the middle is how a
// reader loses track of which numbers are live.
func TestDocsCheck361ComesAfterTheHighestWrittenSection(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	section := check361Section(t, register)
	at := strings.Index(register, section)
	if at < 0 {
		t.Fatalf("%s: the section could not be placed", liveChecksDoc)
	}
	var highest int
	for _, m := range check361NumberRE.FindAllStringSubmatch(register[:at], -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("%s: heading %q does not carry a number", liveChecksDoc, m[0])
		}
		if n > highest {
			highest = n
		}
	}
	if highest >= 361 {
		t.Errorf("%s writes Check %s after `## Check %d`: the check is appended to the register's written sections, and one written in the middle of the older ones is a reader's trap", liveChecksDoc, check361Number, highest)
	}
}
