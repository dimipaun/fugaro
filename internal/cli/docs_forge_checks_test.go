package cli

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// X1's pin on the forge, authorship and change-set live checks.
//
// docs/forge-authorship-images/implementation-plan.md's task **X1** writes the
// five entries its work reserved -- Checks **230, 231, 232, 234 and 235** (233
// was withdrawn in review round 1 and stays reserved and unused; 236-249 are
// reserved and unused) -- into the register, which is another work stream's
// file, and this is the test that holds them there.
//
// A check is registered by TWO halves: a summary row in the table and a
// `## Check N:` section below it. Neither half alone is a number a reader can
// use, so each entry is asserted in both (docs_check340_test.go,
// docs_dynamic_delivery_test.go and docs_check130_test.go carry the same
// assertion for their own numbers; the register-wide backstop
// `TestChecklistChecksAreGuarded` is deliberately absent).
//
// Every one of the five is OWNER-RUN and NOT RUN: writing a check is not
// running it, and the tasks that run them are this plan's own owner-run steps
// (A5, B2, C5b, C1 and D3). A commit that records an owner's result -- the
// paste-back a run makes, the way the register's other checks carry theirs --
// updates the row and the section together and moves the readings in this
// file's entry table with them; TestDocsForgeChecksNotRun is the half that
// fails while a result is claimed and no run has been made.
const forgeAuthorshipPlan = "docs/forge-authorship-images/implementation-plan.md"

// forgeCheckEntry is one of the five entries, and the facts its two halves have
// to carry: what it settles (the decision or reconciliation item it is run for),
// what has to exist before it can run, and the commands the owner types. The
// substrings are the register's own claims rather than this test's prose about
// them: a section that dropped one is a section that stopped telling the owner
// what the check is for or how to run it.
type forgeCheckEntry struct {
	number   string // the register's number
	runBy    string // the plan's task whose owner-run step runs it
	subject  string // the heading's subject, as the register spells it
	settles  []string
	prereqs  []string
	commands []string
}

var forgeCheckEntries = []forgeCheckEntry{
	{
		number: "230", runBy: "A5",
		subject: "the author, the committer and the trailers",
		settles: []string{"d648", "u9", "d606", "d645", "squash", "fugaro-run"},
		prereqs: []string{"a release", "sandbox", "git.author: launcher"},
		commands: []string{
			`fugaro run --title "Check 230"`,
			`git log --format='%an <%ae> | %cn <%ce>%n%(trailers)' origin/main..`,
			`git log -1 --format='%an <%ae>%n%B' origin/main`,
		},
	},
	{
		number: "231", runBy: "B2",
		subject: "the App's Checks and Commit statuses",
		settles: []string{"d614", "d615", "checks: read", "commit statuses: read"},
		prereqs: []string{"a release", "sandbox", "settings", "installation"},
		commands: []string{
			"go test -tags live ./internal/gitprov/github/ -run Checks -count=1",
		},
	},
	{
		number: "232", runBy: "C5b",
		subject:  "a read-only token for context repositories",
		settles:  []string{"u7", "d613", "read-only", "refused"},
		prereqs:  []string{"c5a", "c5b", "credentials service", "sandbox"},
		commands: []string{"fugaro run --context", "git push"},
	},
	{
		number: "234", runBy: "C1",
		subject: "`fugaro project sync --check` against a Bitbucket workspace",
		settles: []string{"d623", "d625", "writes nothing"},
		prereqs: []string{"c1", "bitbucket", "token"},
		commands: []string{
			"fugaro project sync --check",
			"fugaro project sync",
		},
	},
	{
		number: "235", runBy: "D3",
		subject:  "a two-repository change set flips ready together",
		settles:  []string{"d614", "d616", "d641", "d642"},
		prereqs:  []string{"d1", "d2", "d3", "ee"},
		commands: []string{"--change-set", "--repo", "settle"},
	},
}

// forgeCheckNumbers is the five numbers this plan reserved for X1, in the order
// the plan's own Check numbers table marks them: the census test reads the plan
// and compares it with this list, so the two cannot drift apart.
var forgeCheckNumbers = []string{"230", "231", "232", "234", "235"}

// forgeCheckHeadingRE finds every `## Check N:` heading's number, so the five
// can be placed against the sections that were already written and the register's
// own highest section can be read.
var forgeCheckHeadingRE = regexp.MustCompile(`(?m)^## Check ([0-9]+[a-z]?)`)

// forgePlannedNumbers returns the numbers the plan's Check numbers table marks
// as written by X1, in file order.
func forgePlannedNumbers(t *testing.T) []string {
	t.Helper()
	section := docSection(t, forgeAuthorshipPlan, readDoc(t, forgeAuthorshipPlan), "## Check numbers")
	var planned []string
	for _, line := range strings.Split(section, "\n") {
		if !strings.HasPrefix(line, "| ") || strings.HasPrefix(line, "| Check ") || strings.HasPrefix(line, "| ---") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "| "), " | ")
		if len(cells) >= 3 && strings.TrimSpace(cells[2]) == "X1" {
			planned = append(planned, strings.TrimSpace(cells[0]))
		}
	}
	return planned
}

// TestDocsForgeChecksWritten: each of the five entries has both halves of its
// registration, each half carries what the plan says it measures and which
// task's owner-run step runs it, and the block is appended where the register's
// written sections end rather than inserted among older ones.
func TestDocsForgeChecksWritten(t *testing.T) {
	register := readDoc(t, liveChecksDoc)

	for _, c := range forgeCheckEntries {
		row := checkRow(register, c.number)
		if row == "" {
			t.Fatalf("%s's register has no `| %s |` row: Check %s is written below it, and the table is where a reader finds it", liveChecksDoc, c.number, c.number)
		}
		for _, want := range []string{
			"forge-authorship-images/implementation-plan.md",
			c.runBy,
			"OWNER-RUN, NOT RUN.",
			"no agent runs any step of it",
			"sandbox",
		} {
			if !strings.Contains(row, want) {
				t.Errorf("Check %s's row does not say %q: the row is half the registration, and a row that leaves out the plan, the task that runs it or the fact that nobody has run it is half a check", c.number, want)
			}
		}

		section := checkSection(t, register, c.number)
		flat := strings.ToLower(flattenDoc(section))
		for _, want := range []string{
			"**not run.**",
			"no agent runs any step of it",
			"x1",
			"forge-authorship-images/implementation-plan.md",
			strings.ToLower(c.runBy),
			strings.ToLower(c.subject),
		} {
			if !strings.Contains(flat, want) {
				t.Errorf("Check %s's section does not say %q: the section is the other half of the registration, and it is where the owner reads who wrote it, who runs it and what it is about", c.number, want)
			}
		}
		for _, group := range [][]string{c.settles, c.prereqs} {
			for _, want := range group {
				if !strings.Contains(flat, want) {
					t.Errorf("Check %s's section does not say %q: the section is where the owner reads what the check settles and what has to exist before it can run", c.number, want)
				}
			}
		}
		// The commands are read against the section's own case, not the folded
		// text above: `-run Checks` and `%B` are the command, and a section that
		// spelled one `-run checks` would print a `go test` invocation that
		// matches nothing and reads as one that ran.
		typed := flattenDoc(section)
		for _, want := range c.commands {
			if !strings.Contains(typed, want) {
				t.Errorf("Check %s's section does not say %q: the section is where the owner reads what to type, and a command the section spells differently is a command that does something else", c.number, want)
			}
		}
	}

	// Check 230's one reading that is easy to get wrong, and the one a review
	// round caught: D602 leaves `Fugaro-Model` off every commit the runner makes
	// itself (a runner-owned commit belongs to no stage) and off every commit of
	// a stage that pins no model, so a section that asks the owner for the three
	// trailers on every commit sends them to a false failure of A1's hook on a
	// repository whose stages pin none. The carve-out has to be named, and the
	// reading that produces the false reading must not be there.
	c230 := strings.ToLower(flattenDoc(checkSection(t, register, "230")))
	for _, want := range []string{"d602", "fugaro-model"} {
		if !strings.Contains(c230, want) {
			t.Errorf("Check 230's section does not say %q: the trailers a run's commits carry are not all three, and a section that leaves D602's carve-out out has the owner read an expected absence as a result", want)
		}
	}
	for _, text := range []string{checkRow(register, "230"), c230} {
		if strings.Contains(strings.ToLower(text), "three trailers") {
			t.Errorf("Check 230 asks for \"three trailers\": D602 leaves `Fugaro-Model` off the runner's own commits and off every commit of a stage that pins no model, so the phrase is a reading the check cannot make on every repository it names")
		}
	}

	// The plan's own census, read in the other direction: every number it marks
	// X1 has both halves here, and this file's list is exactly the plan's. A
	// number in one and not the other is a check nobody allocated.
	planned := forgePlannedNumbers(t)
	if !slices.Equal(planned, forgeCheckNumbers) {
		t.Errorf("%s's Check numbers table marks %v as written by X1, and this test's entries are %v: the register's five entries and the plan's census are one statement", forgeAuthorshipPlan, planned, forgeCheckNumbers)
	}

	// The withdrawn number: 233 was reserved and given up in review round 1, and
	// the register must carry no half of it -- a reserved number is never reused
	// or renumbered, so a row or a section appearing under it later is a reader's
	// trap rather than a new check.
	if row := checkRow(register, "233"); row != "" {
		t.Errorf("%s carries a `| 233 |` row: 233 was withdrawn in review round 1 and is reserved and unused, and this plan writes no check under it", liveChecksDoc)
	}
	if strings.Contains(register, "## Check 233:") {
		t.Errorf("%s carries a `## Check 233:` section: 233 was withdrawn in review round 1 and is reserved and unused", liveChecksDoc)
	}

	// Appended, not inserted: the five come after every section that was already
	// in the register and in their own ascending order. A block written in the
	// middle of older ones is how a reader loses track of which numbers are live.
	lastOld := -1
	for _, m := range forgeCheckHeadingRE.FindAllStringSubmatchIndex(register, -1) {
		if !slices.Contains(forgeCheckNumbers, register[m[2]:m[3]]) {
			lastOld = m[0]
		}
	}
	if lastOld < 0 {
		t.Fatalf("%s carries no `## Check` section other than these five: this assertion would otherwise pass whatever the register holds", liveChecksDoc)
	}
	prev := lastOld
	for _, number := range forgeCheckNumbers {
		at := strings.Index(register, "\n## Check "+number+":")
		if at < 0 {
			t.Fatalf("%s has no `## Check %s:` section: the row above it points at a section that has to be there", liveChecksDoc, number)
		}
		if at < prev {
			t.Errorf("%s writes Check %s before the sections that were already written: the block is appended where the register's written sections end", liveChecksDoc, number)
		}
		prev = at
	}

	// The plan's own X1 section names what this task wrote, this test file
	// included: a Files, Tests, Command or Commit list that misses a file is a
	// plan the work left behind, and a reviewer reads the plan against the tree.
	x1 := flattenDoc(docSection(t, forgeAuthorshipPlan, readDoc(t, forgeAuthorshipPlan), "### X1. The live-check entries"))
	for _, want := range []string{
		liveChecksDoc,
		"internal/cli/docs_forge_checks_test.go",
		"TestDocsForgeChecksWritten",
		"TestDocsForgeChecksNotRun",
		"`go test ./internal/cli/ -run 'Docs' -count=1`",
		"docs: live checks 230-232, 234 and 235 for forge, authorship and change sets (not run)",
	} {
		if !strings.Contains(x1, want) {
			t.Errorf("%s's X1 section does not name %q: the task's Files, Tests, Command and Commit are what a reviewer of it holds the work to", forgeAuthorshipPlan, want)
		}
	}

	// The pin is one more `internal/cli/docs_*_test.go`, so it moved the census
	// that counts them: docs/rigor-modes/reconciliation.md's row is a running
	// total of other folders' guards, and its own rule (which
	// docs_dynamic_delivery_test.go holds it to for its own pin) is that the
	// folder which writes one moves the row in the same commit and names itself
	// in the sentence beside it. A row that moved without a sentence is the merge
	// that row calls the one that did not.
	if !strings.Contains(flattenDoc(readDoc(t, rigorModesRecon)), "Amended 2026-11-11: `internal/cli/docs_forge_checks_test.go`, the forge-authorship-images plan's pin") {
		t.Errorf("%s does not name this pin as the amendment that moved its `internal/cli/docs_*_test.go` count: the row is a total of other folders' guards, and a folder that writes one says so in its own commit", rigorModesRecon)
	}
}

// TestDocsForgeChecksNotRun: nothing in the five entries reads as a result.
//
// The commit that records an owner's result updates both halves of the entry it
// belongs to, and this test is the half that fails while one half claims a run
// the other does not: a row or a section carrying a verdict, an exit code or a
// date is a live claim nobody made. The tasks that run the five are A5, B2,
// C5b, C1 and D3, and none of them has run.
func TestDocsForgeChecksNotRun(t *testing.T) {
	register := readDoc(t, liveChecksDoc)

	// The shapes a run's result takes in this register: a dated line ("**RUN
	// 2026-10-30: FAILED — THE GATE**"), either verdict, and a reading of an exit
	// code. None of them belongs in an entry whose run has not been made.
	resultMarkers := []string{"PASSED", "FAILED", "**PASS**", "**FAIL**", "exit `0`", "exit 0"}
	runRE := regexp.MustCompile(`RUN 20[0-9]{2}-`)
	dateRE := regexp.MustCompile(`20[0-9]{2}-[0-9]{2}-[0-9]{2}`)

	for _, c := range forgeCheckEntries {
		row := checkRow(register, c.number)
		section := checkSection(t, register, c.number)
		if !strings.Contains(row, "OWNER-RUN, NOT RUN.") {
			t.Errorf("Check %s's row does not read `OWNER-RUN, NOT RUN.`: the check is the owner's and has not been run, and a row that leaves either half out is read as a result", c.number)
		}
		if !strings.Contains(strings.ToLower(flattenDoc(section)), "**not run.**") {
			t.Errorf("Check %s's section does not say `**NOT RUN.**`: writing a check is not running it, and the section is where a reader looks for which of the two it is", c.number)
		}
		for _, text := range []string{row, section} {
			for _, bad := range resultMarkers {
				if strings.Contains(text, bad) {
					t.Errorf("Check %s carries %q: no run of this check has been made, so a verdict or an exit reading here is a live claim nobody made", c.number, bad)
				}
			}
			if runRE.MatchString(text) {
				t.Errorf("Check %s carries a dated run line (%q): the entry is written and not run, and the commit that records a result is the one that adds it", c.number, runRE.FindString(text))
			}
			if dateRE.MatchString(text) {
				t.Errorf("Check %s carries a date (%q): a date in this register is a run's date, and no run of this check has been made", c.number, dateRE.FindString(text))
			}
		}
	}

	// The register's highest written section is still above this block: the five
	// are a low block appended at the end, so a reader taking the register's
	// progress from its highest number is not moved by them, and the census tests
	// that read that number stay true.
	highest := 0
	for _, m := range forgeCheckHeadingRE.FindAllStringSubmatch(register, -1) {
		if n, err := strconv.Atoi(strings.TrimRight(m[1], "abcdef")); err == nil && n > highest {
			highest = n
		}
	}
	if highest <= 235 {
		t.Errorf("%s's highest written `## Check` section is %d: the five entries are a low block appended below the sections already written, and a register whose highest section is one of them is a reading the census tests would take from this block", liveChecksDoc, highest)
	}
}
