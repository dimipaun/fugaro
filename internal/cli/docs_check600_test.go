package cli

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Check 600 — named Claude accounts, LC1 to LC4 — as the register writes it.
//
// The number is the accounts-add work's: its implementation plan's handoff row
// sends LC1 to LC4 to this file "under the number free at that merge, as Check
// 35's own row asks (D870)", and that folder's reconciliation D870 is the
// answer that leaves the number to whoever writes the register -- which is
// this task, since the register is another work stream's file.
//
// A check is registered by two things -- a row in the summary table and a
// `## Check N:` section below it -- and either half alone is a number a reader
// cannot use. The backstop that would assert the pairing register-wide
// (`TestChecklistChecksAreGuarded`) is deliberately absent, so each work's own
// docs tests carry the assertion for its own numbers; this file carries it for
// 600, and for its four parts, which are written as four sections because each
// is run on its own and on a different machine (600a and 600b need no Fugaro
// build at all, 600c needs a terminal and an installation, and 600d is the one
// part a script can carry).
//
// Three of the four parts are human-terminal procedures and say so: LC1 and
// LC2 are what Anthropic's own `claude setup-token` prints and asks, and LC3 is
// a login and one hidden paste in the owner's own terminal. What is pinned here
// is that the register says that, that 600d names the ONE command the owner
// runs, that the script and its test are really there -- a section telling the
// owner to run a file nobody wrote is a promise -- and that the block the
// script prints is the block the section describes.
const check600Number = "600"

// check600Parts are the four sections, in the order the register writes them.
var check600Parts = []string{"600a", "600b", "600c", "600d"}

// check600NumberRE reads the register's written sections as the numbers they
// carry, the way docs_check340_test.go's own reading does: the section headings
// are `## Check 600a:` and the number the register is counting is 600.
var check600NumberRE = regexp.MustCompile(`(?m)^## Check ([0-9]+)`)

// check600Section returns one part's section, bounded by the next `## `
// heading so that a check written after it does not widen what is read.
func check600Section(t *testing.T, register, part string) string {
	t.Helper()
	i := strings.Index(register, "\n## Check "+part+":")
	if i < 0 {
		t.Fatalf("%s has no `## Check %s:` section: the row above it points at steps that have to be there", liveChecksDoc, part)
	}
	section := register[i+1:]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	return section
}

// check600Row returns Check 600's summary row, or "" when the table has none.
func check600Row(register string) string {
	re := regexp.MustCompile(`(?m)^\|\s*` + check600Number + `\s*\|.*$`)
	return re.FindString(register)
}

// TestDocsCheck600IsRegistered: the check has both halves of its registration,
// both say it has not been run, and the four parts are all there. A row or a
// section that read as a pass would be a live claim nobody made -- every part
// of this check is the owner's, and none of them has been run -- and a section
// missing one of the four parts would leave an LC with no steps at all, which
// is the hand work the owner's standing rule of 2026-11-07 exists to remove.
func TestDocsCheck600IsRegistered(t *testing.T) {
	register := readDoc(t, liveChecksDoc)

	row := check600Row(register)
	if row == "" {
		t.Fatalf("%s's register has no `| %s |` row: Check %s is written below it, and the table is where a reader finds it", liveChecksDoc, check600Number, check600Number)
	}
	if !strings.Contains(row, "OWNER-RUN, NOT RUN.") {
		t.Errorf("Check %s's row does not read `OWNER-RUN, NOT RUN.`: the check is the owner's and has not been run, and a row that leaves either half out is read as a result", check600Number)
	}
	for _, part := range check600Parts {
		if !strings.Contains(row, "**"+part+"**") {
			t.Errorf("Check %s's row does not name its part **%s**: the row is the whole check's registration and the four parts are how it is run", check600Number, part)
		}
	}

	for _, part := range check600Parts {
		section := check600Section(t, register, part)
		// Flattened and folded, because the register's prose wraps: a pin that
		// missed a phrase across a line break would be a pin on the line breaks.
		flat := strings.ToLower(flattenDoc(section))
		for _, want := range []string{
			"**not run.**",
			// The register's own rule for an owner-run check: no agent runs any
			// step of it, and the text has to say so.
			"no agent runs any step of it",
			// Who wrote it. The plan defines no task id for this check, so the
			// task that implements it is how its own sections cite it -- and
			// the handoff row that sends the four here is D870's.
			"the task that implemented check " + check600Number,
			"d870",
		} {
			if !strings.Contains(flat, want) {
				t.Errorf("Check %s's section does not say %q: the section is where the owner reads what the check settles and how to run it, and a pin that has moved is a section that stopped saying it", part, want)
			}
		}
	}

	// The number's own reading is 600a's, which is where the four sections say
	// why this number and not another: a section that dropped it would leave the
	// register's rule for a work that reserved none unstated.
	first := strings.ToLower(flattenDoc(check600Section(t, register, check600Parts[0])))
	if !strings.Contains(first, "number free at that merge") {
		t.Errorf("Check %s's section does not state the number's own reading (%q): the register's rule is that a work which reserved none takes the number free at its merge, and this section is the record of the one taken", check600Parts[0], "number free at that merge")
	}
}

// TestDocsCheck600EachPartSettlesItsOwnLc: the four sections are one check's,
// and each is one LC of spec §9 -- the four are the parts of one live check and
// not four checks that happen to share a number, so a section that stopped
// naming its own LC would leave a reader unable to tell which of the four they
// are being asked to run.
func TestDocsCheck600EachPartSettlesItsOwnLc(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	for i, part := range check600Parts {
		flat := strings.ToLower(flattenDoc(check600Section(t, register, part)))
		lc := "§9's **lc" + strconv.Itoa(i+1) + "**"
		for _, want := range []string{lc, "[spec.md](accounts-add/spec.md)"} {
			if !strings.Contains(flat, want) {
				t.Errorf("Check %s's section does not name %q: each section is one LC of the accounts-add spec §9, and the section is where that is stated", part, want)
			}
		}
	}

	// The two human answers the operator page is written against, and the one
	// fact each of them turns on: a section that kept the LC but dropped what it
	// measures would leave the page's wording ungrounded.
	for _, want := range []struct{ part, phrase string }{
		// LC1: whether the value stands alone on its line is the whole of what
		// the page must tell an owner to copy.
		{"600a", "alone on its line"},
		// LC2 is the register's own Check 35 and closes it: silent reuse is the
		// answer that would store a token under the wrong name.
		{"600b", "silently reuses"},
		{"600b", "check 35"},
		// LC3: the hidden prompt, and that it does not echo.
		{"600c", "did not echo"},
		// LC4: the one command, and that no value is read on any path.
		{"600d", "check-lc4.sh"},
		{"600d", "never a secret value"},
	} {
		flat := strings.ToLower(flattenDoc(check600Section(t, register, want.part)))
		if !strings.Contains(flat, want.phrase) {
			t.Errorf("Check %s's section does not say %q: it is the reading that part of the check exists to produce", want.part, want.phrase)
		}
	}
}

// TestDocsCheck600dNamesTheOneCommand: 600d is the one part a script can carry,
// so its section is the one with a command in it, and the command is the whole
// of what the owner types. The script and its test are really there: a section
// naming a file nobody wrote is a promise, and the sibling test is what proves
// the script never needs a cloud call to be read.
func TestDocsCheck600dNamesTheOneCommand(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	flat := strings.ToLower(flattenDoc(check600Section(t, register, "600d")))
	const command = "bash scripts/live-checks/check-lc4.sh --project <project> --account <name>"
	if !strings.Contains(flat, command) {
		t.Errorf("Check 600d's section does not name %q: that one command is the whole of what the owner runs, and the check refuses without either argument", command)
	}
	// The three other parts must NOT carry one: a script for a login's prompts
	// would be reading the login's own output, which is the one thing Fugaro's
	// code is built never to do (spec §3.4).
	for _, part := range []string{"600a", "600b", "600c"} {
		if strings.Contains(strings.ToLower(check600Section(t, register, part)), "scripts/live-checks/") {
			t.Errorf("Check %s's section names a script under scripts/live-checks/: those three parts are read off a terminal by hand, and a script for them would parse the login's own output", part)
		}
	}

	for _, path := range []string{"scripts/live-checks/check-lc4.sh", "scripts/live-checks/check-lc4_test.sh"} {
		if _, err := os.Stat("../../" + path); err != nil {
			t.Errorf("%s is named by Check %s and does not exist: %v", path, check600Number, err)
		}
	}
	// The script's own test is wired into the harness that runs every live
	// check's test, which is the only thing that executes it on this tree.
	if got := readRepoFile(t, "scripts/live_checks_test.go"); !strings.Contains(got, `{"check-lc4.sh", "check-lc4_test.sh"}`) {
		t.Errorf("scripts/live_checks_test.go does not run check-lc4.sh's own test: a script whose test is not wired in is a script nothing runs")
	}
}

// TestDocsCheck600NamesEveryBlockKey: the register's "Reading the CHECK LC4
// RESULT block." paragraph is what the owner diffs a pasted block against, so
// it names every key the block prints, and what the block prints is the
// script's own BLOCK_KEYS rather than a paraphrase of it. The paragraph, and
// not the whole section, is the check: several of these keys are named again in
// the prose above, and a key the paragraph leaves out reads as a key the block
// was not meant to print.
func TestDocsCheck600NamesEveryBlockKey(t *testing.T) {
	const script = "scripts/live-checks/check-lc4.sh"
	keys := blockKeys(t, readRepoFile(t, script))
	if len(keys) < 10 {
		t.Fatalf("%s: BLOCK_KEYS parsed as %d keys (%v), which cannot be the block", script, len(keys), keys)
	}
	section := check600Section(t, readDoc(t, liveChecksDoc), "600d")
	const heading = "**Reading the `CHECK LC4 RESULT` block.**"
	i := strings.Index(section, heading)
	if i < 0 {
		t.Fatalf("%s: the Check 600d section has no %q paragraph", liveChecksDoc, heading)
	}
	para := section[i:]
	if j := strings.Index(para, "\n\n"); j >= 0 {
		para = para[:j]
	}
	for _, k := range keys {
		if !strings.Contains(para, "`"+k+"`") {
			t.Errorf("%s's Check 600d %q paragraph does not name the block key `%s`, which %s's BLOCK_KEYS prints", liveChecksDoc, heading, k, script)
		}
	}
}

// TestDocsCheck600ComesAfterTheHighestWrittenSection: the number was taken by
// the register's own rule for a work that reserved none, and the reading that
// rule is applied to is pinned in the claim paragraph above the table. Every
// written section before Check 600 must therefore carry a number below it -- a
// section written above this one would make the paragraph's reading wrong, and
// the register's numbering would then be a claim about a commit and not about
// the tree.
func TestDocsCheck600ComesAfterTheHighestWrittenSection(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	i := strings.Index(register, "\n## Check "+check600Parts[0]+":")
	if i < 0 {
		t.Fatalf("%s has no `## Check %s:` section", liveChecksDoc, check600Parts[0])
	}
	highest := 0
	for _, m := range check600NumberRE.FindAllStringSubmatch(register[:i], -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && n > highest {
			highest = n
		}
	}
	if highest == 0 {
		t.Fatalf("%s carries no `## Check` section above Check %s: this assertion would otherwise pass whatever the claim paragraph says", liveChecksDoc, check600Number)
	}
	if highest >= 600 {
		t.Errorf("Check %s's four sections are written below a section numbered %d: the number was taken as the first one free past every reservation, and a higher section already in the file makes that reading wrong", check600Number, highest)
	}
	// The claim paragraph is above the table and the sections are below it, so
	// the paragraph is what a reader of the table has already been told. It has
	// to carry the rule's own words: "claimed, not reserved" is what makes the
	// number movable by whichever of two claims merges second.
	claim := strings.Index(register, "Check 600 is the accounts-add work's")
	table := strings.Index(register, "\n| 600 |")
	if claim < 0 || table < 0 {
		t.Fatalf("%s: the claim paragraph (%d) and the row (%d) are both part of the registration", liveChecksDoc, claim, table)
	}
	if claim > table {
		t.Errorf("Check %s's claim paragraph is below its row: the paragraph is the record of the number taken and reads with the register's other numbering paragraphs, above the table", check600Number)
	}
	if !strings.Contains(flattenDoc(register[claim:table]), "claimed, not reserved") {
		t.Errorf("Check %s's claim paragraph does not say the number is `claimed, not reserved`: the register's rule is what lets the later of two colliding claims renumber itself", check600Number)
	}
}

// TestDocsCheck600IsThePlansNumber: the number is the accounts-add work's, and
// its own plan is where the handoff is written. A section that took a number
// without the plan that sends it here would be this task's own choice rather
// than the work's, and the register's rule is that the plan that reserved none
// takes the number free at its merge -- which is a fact about the plan's own
// handoff row.
func TestDocsCheck600IsThePlansNumber(t *testing.T) {
	plan := readDoc(t, "docs/accounts-add/implementation-plan.md")
	if !strings.Contains(plan, "| LC1–LC4 |") {
		t.Fatalf("docs/accounts-add/implementation-plan.md has no `LC1–LC4` handoff row: the register's Check %s is that row's own handoff", check600Number)
	}
	for _, want := range []string{
		"`docs/gcp-live-checklist.md`, under the number free at that merge",
		"D870",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("the accounts-add plan's handoff row no longer says %q: the row is what sends LC1 to LC4 to the register, and Check %s is where they landed", want, check600Number)
		}
	}
	// The census of the register's highest written section is a reading of the
	// register, and this work is what moved it: the row periodic-reviews owns
	// has to carry the same number the register does, or a reader of that
	// design's row takes a stale figure for the register's progress.
	census := readDoc(t, "docs/periodic-reviews/README.md")
	if !strings.Contains(census, "The register's highest **written** `## Check` section is **600**") {
		t.Errorf("docs/periodic-reviews/README.md's Checks 340–349 row does not read the register's highest written `## Check` section as 600: Check %s's four sections are the highest written, and that row is the reading other designs take the register's progress from", check600Number)
	}
}

// TestDocsCheck600ScriptKeepsItsPromises: the section says the check creates
// nothing, reads no value and refuses `belong`; these are its own sides of
// those sentences, read out of the script rather than trusted to the prose, so
// a script that started writing would fail here as well as on its own test.
func TestDocsCheck600ScriptKeepsItsPromises(t *testing.T) {
	script := readRepoFile(t, "scripts/live-checks/check-lc4.sh")
	for _, want := range []string{
		"set -Eeuo pipefail",
		// No write of any kind: every gcloud call the script makes is one of
		// these four, and a `secrets versions access` here would be the value
		// this check exists without.
		"run jobs list",
		"run jobs describe",
		"secrets list",
		"get-iam-policy",
		// The guard, by name, on the project and the account.
		"belong",
		// The dry run makes no call at all, which is what its own section says
		// of it.
		"dry-run",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("%s no longer carries %q, which its own section's sentences rest on", "scripts/live-checks/check-lc4.sh", want)
		}
	}
	for _, banned := range []string{"versions access", "versions add", "secrets create", "iam policy-binding", "--set-env-vars"} {
		if strings.Contains(script, banned) {
			t.Errorf("scripts/live-checks/check-lc4.sh carries %q: the check is reads only, it reads no secret value and it changes nothing", banned)
		}
	}
}
