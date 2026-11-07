package cli

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Check 340 — the prefix-limited listing grant, R12's gate — as the register
// writes it.
//
// The number is the periodic-reviews work's
// (docs/periodic-reviews/implementation-plan.md §P8 claims 340–342 and leaves
// 343–349 reserved), and the register is another work stream's file: this is
// the task that implements the check writing its own row and section, which is
// the register's rule for a work that reserved none.
//
// A check is registered by two things — a row in the summary table and a
// `## Check N:` section below it — and either half alone is a number a reader
// cannot use. The backstop that would assert the pairing register-wide
// (`TestChecklistChecksAreGuarded`) is deliberately absent, so each work's own
// docs tests carry the assertion for its own numbers; this file carries it for
// 340.
//
// The check is also the reason this work ships a script rather than a list of
// steps: it creates an account, grants bucket IAM and writes objects, so the
// owner's work has to be one command with its own teardown. What is pinned here
// is that the register says so and names that command, and that the script and
// its test are really there — a row pointing at a file nobody wrote is a
// promise, and a section telling the owner to run one is what CONTRIBUTING.md
// now requires of every owner-run check.
const check340Number = "340"

// check340Sections is the register's own register of written sections, read as
// the numbers they carry.
var check340NumberRE = regexp.MustCompile(`(?m)^## Check ([0-9]+)`)

// check340Section returns Check 340's section, bounded by the next `## `
// heading so that a check written after it does not widen what is read.
func check340Section(t *testing.T, register string) string {
	t.Helper()
	i := strings.Index(register, "\n## Check "+check340Number+":")
	if i < 0 {
		t.Fatalf("%s has no `## Check %s:` section: the row above it points at steps that have to be there", liveChecksDoc, check340Number)
	}
	section := register[i+1:]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	return section
}

// check340Row returns Check 340's summary row, or "" when the table has none.
func check340Row(register string) string {
	re := regexp.MustCompile(`(?m)^\|\s*` + check340Number + `\s*\|.*$`)
	return re.FindString(register)
}

// TestDocsCheck340IsRegistered: the check has both halves of its registration,
// each half says it has not been run, and the section names the ONE command the
// owner runs. A check this one is owner-run and unrun: a row or a section that
// read as a pass would be a live claim nobody made, and a section that listed
// steps instead of a script would be the hand work the owner's standing rule of
// 2026-11-07 exists to remove.
func TestDocsCheck340IsRegistered(t *testing.T) {
	register := readDoc(t, liveChecksDoc)

	row := check340Row(register)
	if row == "" {
		t.Fatalf("%s's register has no `| %s |` row: Check %s is written below it, and the table is where a reader finds it", liveChecksDoc, check340Number, check340Number)
	}
	if !strings.Contains(row, "USER-RUN, NOT RUN.") {
		t.Errorf("Check %s's row does not read `USER-RUN, NOT RUN.`: the check is the owner's and has not been run, and a row that leaves either half out is read as a result", check340Number)
	}

	section := check340Section(t, register)
	// Flattened and folded, because the register's prose wraps: a pin that
	// missed a phrase across a line break would be a pin on the line breaks.
	flat := strings.ToLower(flattenDoc(section))
	for _, want := range []string{
		"**not run.**",
		// The register's own rule for an owner-run check: no agent runs any
		// step of it, and the text has to say so.
		"no agent runs any step of it",
		// The one command, and the two arguments it takes. The check refuses
		// without either, so a section that printed a bare script name would be
		// a command that stops on its own guard.
		"bash scripts/live-checks/check340.sh --project <project> --bucket <sandbox bucket>",
		// What it is for, and what happens on the reading that stops the plan.
		"list-prefix",
		"r12",
		"e4",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("Check %s's section does not say %q: the section is where the owner reads what the check settles and how to run it, and a pin that has moved is a section that stopped saying it", check340Number, want)
		}
	}

	// The script and its test are really there: the section names a command, and
	// a command naming a file nobody wrote is a promise rather than a check.
	for _, path := range []string{"scripts/live-checks/check340.sh", "scripts/live-checks/check340_test.sh"} {
		if _, err := os.Stat("../../" + path); err != nil {
			t.Errorf("%s is named by Check %s and does not exist: %v", path, check340Number, err)
		}
	}
}

// TestDocsCheck340ComesAfterTheHighestWrittenSection: the section is appended
// where the register's written sections end rather than inserted among older
// ones, and its number is above every other written section's. A block is
// registered by appending to it, and a section written in the middle is how a
// reader loses track of which numbers are live.
func TestDocsCheck340ComesAfterTheHighestWrittenSection(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	section := check340Section(t, register)
	at := strings.Index(register, section)
	if at < 0 {
		t.Fatalf("%s: the section could not be placed", liveChecksDoc)
	}
	var highest int
	for _, m := range check340NumberRE.FindAllStringSubmatch(register[:at], -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("%s: heading %q does not carry a number", liveChecksDoc, m[0])
		}
		if n > highest {
			highest = n
		}
	}
	if highest >= 340 {
		t.Errorf("%s writes Check %s after `## Check %d`: the check is appended to the register's written sections, and one written in the middle of the older ones is a reader's trap", liveChecksDoc, check340Number, highest)
	}
}

// TestDocsCheck340IsThePlansNumber: the register's number is the plan's, and the
// plan is where the check is defined. A register section under a number the plan
// does not claim is a check nobody allocated, and the plan is the one place that
// says which numbers this work holds.
func TestDocsCheck340IsThePlansNumber(t *testing.T) {
	plan := flattenDoc(readDoc(t, "docs/periodic-reviews/implementation-plan.md"))
	for _, want := range []string{
		"**Check 340 — the prefix-limited listing grant (R12's, and it gates R12).**",
		"Owner-run, **NOT RUN**.",
		"Check 340, Check 341 and Check 342 are this round's; 343–349 are reserved and unused.",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("docs/periodic-reviews/implementation-plan.md no longer says %q: the register's row and section are this work's number, and the plan is what claims it", want)
		}
	}
}
