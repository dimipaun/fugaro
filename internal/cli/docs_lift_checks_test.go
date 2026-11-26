package cli

// The three live checks of L8 ([docs/repo-override-rulings](repo-override-rulings/implementation-plan.md),
// block 910–919) are owner-run scripts the owner runs around the two releases
// that lift a repository's keys into the project: 910 records the resolved
// values before release A's `fugaro update`, 911 reads the same values after it
// and compares the two, and 912 reads one checkout at two refs after release B
// to see that the refusal follows the ref and not the file. Tasks CK1 and CK2
// wrote them; CK2 registered all three.
//
// A live check is registered in three places at once and none of them is
// optional: the summary register of docs/gcp-live-checklist.md (one row: what it
// measures and that it is owner-run), a section of that document (the command
// the owner types, what it costs, how to read the block, where to paste it), and
// scripts/live-checks/README.md, which is what a person reads when they are
// looking at the scripts themselves rather than at the register. The harness
// list, scripts/live_checks_test.go, is the fourth: a check whose own test is
// not in it is a test nothing runs.
//
// This test is the pin. It fails before the rows exist, which is the state the
// three scripts were written in: a script nobody registered is a script the
// owner never runs, and the acceptance it was written for is then unmeasured.

import (
	"os"
	"strings"
	"testing"
)

const liftCheckPlan = "repo-override-rulings"

// liftCheck is one of the three: the numbers and file names, the command the
// register prints for it, and the release it is run around — the fact that
// decides when the owner runs it, and therefore the one a reader who lands on
// the wrong row would get wrong.
type liftCheck struct {
	number  string
	script  string
	test    string
	command string
	release string
}

var liftChecks = []liftCheck{
	{
		number:  "910",
		script:  "scripts/live-checks/check-910-lift-snapshot.sh",
		test:    "scripts/live-checks/check-910-lift-snapshot_test.sh",
		command: "bash scripts/live-checks/check-910-lift-snapshot.sh fugaro",
		release: "before release A",
	},
	{
		number:  "911",
		script:  "scripts/live-checks/check-911-lift-compare.sh",
		test:    "scripts/live-checks/check-911-lift-compare_test.sh",
		command: "bash scripts/live-checks/check-911-lift-compare.sh fugaro <the snapshot file 910 printed>",
		release: "after release A",
	},
	{
		number:  "912",
		script:  "scripts/live-checks/check-912-lift-refusal.sh",
		test:    "scripts/live-checks/check-912-lift-refusal_test.sh",
		command: "bash scripts/live-checks/check-912-lift-refusal.sh fugaro <a checkout>",
		release: "after release B",
	},
}

// TestLiftChecks910To912AreRegistered: every one of the three is in the
// register's table, in the register's own section and in the scripts' README,
// each naming the script that does the measuring and its own test, each saying
// it is owner-run and not run yet — which is the truth about all three, since
// release A and release B have not shipped — and each naming the release it
// belongs to.
func TestLiftChecks910To912AreRegistered(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	readme := flattenDoc(readDoc(t, "scripts/live-checks/README.md"))
	for _, c := range liftChecks {
		row := flattenDoc(registerRow(t, register, c.number))
		for _, want := range []string{
			c.script,
			`"Check ` + c.number + `" below`,
			c.test,
			"run by **you**",
			"**never `belong`**",
			"No agent runs any step of it",
			"**OWNER-RUN, NOT RUN.**",
			liftCheckPlan,
			c.release,
		} {
			if !strings.Contains(row, want) {
				t.Errorf("%s's register row for Check %s lacks %q", liveChecksDoc, c.number, want)
			}
		}

		section := flattenDoc(registerSection(t, register, "\n## Check "+c.number+":"))
		for _, want := range []string{
			c.script,
			c.test,
			c.command,
			c.release,
			"**NOT RUN.**",
			"No agent runs any step of it",
			liftCheckPlan,
			// The four verdicts, and what each means for the exit status: a
			// section that promises a block without saying how a row that could
			// not be measured reads would have the owner take exit 3 for a pass.
			"**PASS** (exit 0)",
			"**FAIL** (exit 1)",
			"**INCOMPLETE** (exit 2)",
			"**INCOMPLETE** (exit 3)",
			"**UNVERIFIED**",
		} {
			if !strings.Contains(section, want) {
				t.Errorf("%s's Check %s section lacks %q", liveChecksDoc, c.number, want)
			}
		}

		// The README is where a person who is looking at the scripts themselves
		// lands: it names the script — by its bare name, the way the directory
		// reads — and points at the register for the command.
		if bare := strings.TrimPrefix(c.script, "scripts/live-checks/"); !strings.Contains(readme, bare) {
			t.Errorf("scripts/live-checks/README.md does not name %s: the README is what a person reads when they are looking at the scripts, and Check %s is registered there too", bare, c.number)
		}
	}
}

// TestLiftChecksAreWiredAndReal: the three scripts and their tests exist, and
// each of the three tests is in the harness list that runs them. A section
// naming a file nobody wrote is a promise; a test that no run reaches is a
// script nothing proves.
func TestLiftChecksAreWiredAndReal(t *testing.T) {
	harness := readRepoFile(t, "scripts/live_checks_test.go")
	for _, c := range liftChecks {
		for _, path := range []string{c.script, c.test} {
			if _, err := os.Stat("../../" + path); err != nil {
				t.Errorf("%s is named by Check %s and does not exist: %v", path, c.number, err)
			}
		}
		if want := `{"` + strings.TrimPrefix(c.script, "scripts/live-checks/") + `", "` + strings.TrimPrefix(c.test, "scripts/live-checks/") + `"}`; !strings.Contains(harness, want) {
			t.Errorf("scripts/live_checks_test.go does not carry %s, so Check %s's own test is not run by `go test ./scripts`", want, c.number)
		}
	}
}

// TestLiftChecksSayWhatTheyMeasureInTheRegister: the three rows sit in the
// register together and a reader who finds one is told which of the other two
// goes with it — 910 with 911, and 912 alone, after release B. The rows are what
// the owner reads to decide what to run and when, so a row that names its own
// script but not its place in the sequence leaves the owner with a snapshot and
// nothing to compare it against.
func TestLiftChecksSayWhatTheyMeasureInTheRegister(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	row910 := flattenDoc(registerRow(t, register, "910"))
	if !strings.Contains(row910, "911") {
		t.Errorf("%s's Check 910 row does not name Check 911: 910 records what release A is about to change and 911 is the read it is compared with, so a row that names only itself leaves the snapshot with nothing to be compared to", liveChecksDoc)
	}
	row911 := flattenDoc(registerRow(t, register, "911"))
	if !strings.Contains(row911, "910") {
		t.Errorf("%s's Check 911 row does not name Check 910, whose snapshot it reads", liveChecksDoc)
	}
	row912 := flattenDoc(registerRow(t, register, "912"))
	if !strings.Contains(row912, "default branch") || !strings.Contains(row912, "older ref") {
		t.Errorf("%s's Check 912 row does not say what 912 measures: a lifted key refused on the default branch and named, not refused, on an older ref", liveChecksDoc)
	}
}
