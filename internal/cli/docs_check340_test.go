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
// now requires of a new owner-run check — the ones already in the register keep
// the shape their sections have until the work that touches them converts
// them.
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
		// Exit 1 is two readings, and the section has to say so: a teardown
		// that could not confirm what it deleted exits 1 beside a PASSing
		// list-prefix row, and a reader who takes every exit 1 for E4 stops
		// R12's Terraform on a run that answered the question it was run for.
		"or a form passed and the restore could not confirm what it deleted",
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

	// Who wrote it, cited as the plan cites it. §P8 names the number and says the
	// task that implements each check writes its row and its section; it defines
	// no task id for this one, so a section naming one would point a reader at a
	// task that exists nowhere.
	if !strings.Contains(flat, "the task that implemented check "+check340Number) {
		t.Errorf("Check %s's section does not say who wrote it (the task that implemented Check %s): the register's rule is that the task which implements a check writes its row and its section, and a section that leaves that out reads as the register's own work", check340Number, check340Number)
	}
	if strings.Contains(section, "**CK340**") {
		t.Errorf("Check %s's section cites a task id (`**CK340**`) that docs/periodic-reviews/implementation-plan.md does not define: the plan's §P8 allocates the number and no task, and a citation a reader cannot look up is worse than none", check340Number)
	}
}

// TestDocsCheck340CensusReadsTheRegister: the design's own census of the
// register is a reading of the register, and the two move together — the same
// rule docs/coordinator/README.md's row is held to. This design's row said the
// highest WRITTEN `## Check` section was 290 while this work's own 340 was
// written below it, which is exactly the drift the coordinator's test exists to
// catch for its own row.
func TestDocsCheck340CensusReadsTheRegister(t *testing.T) {
	highest := 0
	for _, m := range check340NumberRE.FindAllStringSubmatch(readDoc(t, liveChecksDoc), -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && n > highest {
			highest = n
		}
	}
	if highest == 0 {
		t.Fatalf("%s carries no `## Check` section: this assertion would otherwise pass whatever the census claims", liveChecksDoc)
	}
	const census = "docs/periodic-reviews/README.md"
	m := check340CensusHighestRE.FindStringSubmatch(readDoc(t, census))
	if m == nil {
		t.Fatalf("%s's checks row no longer states the highest written `## Check` section: that statement is what a reader takes the register's progress from", census)
	}
	if claimed, _ := strconv.Atoi(m[1]); claimed != highest {
		t.Errorf("%s's Checks 340–349 row says the register's highest written `## Check` section is %d, and the register's is %d: the row is a reading of the register, and this design is the one that wrote the section", census, claimed, highest)
	}
}

// check340CensusHighestRE reads the census's own sentence rather than a table
// cell: the row wraps across the whole line, and the number is the sentence's.
var check340CensusHighestRE = regexp.MustCompile("The register's highest \\*\\*written\\*\\* `## Check` section is \\*\\*([0-9]+)\\*\\*")

// TestDocsContributingOwnerRunRuleLinksTheChecklist: CONTRIBUTING.md is at the
// repository root, so a link in it is written from there. The rule this work
// added linked `gcp-live-checklist.md`, which resolves to nothing at the root —
// a reader following it gets a 404 from a rule about being runnable.
func TestDocsContributingOwnerRunRuleLinksTheChecklist(t *testing.T) {
	const path = "CONTRIBUTING.md"
	const want = "[docs/gcp-live-checklist.md](docs/gcp-live-checklist.md)"
	if !strings.Contains(readDoc(t, path), want) {
		t.Errorf("%s's owner-run-check rule does not link the checklist as %s: the file is at the repository root, so a link without `docs/` resolves to nothing", path, want)
	}
	// Every relative link in the file, resolved: the rule is one of a list, and a
	// link that does not resolve is one a reader cannot follow. The count is
	// asserted first: a pattern that quietly stopped matching would leave the
	// loop below passing over nothing, which is the same as not checking.
	links := relativeDocLinks(readDoc(t, path))
	if len(links) == 0 {
		t.Errorf("%s carries no relative link, so this pin checks nothing", path)
	}
	for _, target := range links {
		if _, err := os.Stat("../../" + target); err != nil {
			t.Errorf("%s links %s and there is no such file at the repository root: %v", path, target, err)
		}
	}
}

// relativeDocLinks returns the targets of the markdown links in doc that are
// paths into this repository, with any anchor removed. A target is matched
// whether or not it carries whitespace: a link rewritten with a space in it is
// still one a reader clicks and this test has to resolve, and a pattern that
// skipped those would pass while checking nothing at all.
func relativeDocLinks(doc string) []string {
	var out []string
	for _, m := range check340LinkRE.FindAllStringSubmatch(doc, -1) {
		target, ok := strings.CutPrefix(m[1], "#")
		if ok || target == "" {
			continue
		}
		if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
			continue
		}
		target, _, _ = strings.Cut(target, "#")
		out = append(out, target)
	}
	return out
}

var check340LinkRE = regexp.MustCompile(`\]\(([^()]+)\)`)

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
