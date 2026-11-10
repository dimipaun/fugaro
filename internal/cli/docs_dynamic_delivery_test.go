package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The dynamic-delivery work's own docs pin (plan task DS0.2), written by that
// task and named TestDynamicDeliveryDocsPinned there.
//
// What it holds is what the register's own rule for a check is: a check is
// registered by TWO halves -- a summary row and a `## Check N:` section -- and
// neither half alone is a number a reader can use, so a work that reserved its
// numbers writes both (docs_check340_test.go carries the same assertion for
// 340, docs_check130_test.go and docs_checks_145_149_test.go for theirs; the
// register-wide backstop TestChecklistChecksAreGuarded is deliberately absent).
//
// Checks 510 and 511 are the two this work holds, and this is the task that
// wrote them: both are OWNER-RUN and NOT RUN -- writing a check is not running
// it (DS0.3 is the run), so either half that read as a result would be a live
// claim nobody made. The folder's files, the numbers they claim and the links
// between them are pinned here as well, because this folder is where a reader
// learns what the two checks settle (U3 and F30 for 510, U4 for 511) and which
// decision each one gates (DS2.2 and DS1.5).
const (
	dynamicDeliveryDir     = "docs/secrets-brokering/dynamic-delivery"
	dynamicDeliveryReadme  = dynamicDeliveryDir + "/README.md"
	dynamicDeliverySpec    = dynamicDeliveryDir + "/spec.md"
	dynamicDeliveryRecon   = dynamicDeliveryDir + "/reconciliation.md"
	dynamicDeliveryPlan    = dynamicDeliveryDir + "/implementation-plan.md"
	check510Steps          = dynamicDeliveryDir + "/check510-steps.md"
	check511Steps          = dynamicDeliveryDir + "/check511-steps.md"
	check510Script         = "scripts/live-checks/check510.sh"
	check510ScriptTest     = "scripts/live-checks/check510_test.sh"
	check511Script         = "scripts/live-checks/check511.sh"
	check511ScriptTest     = "scripts/live-checks/check511_test.sh"
	check510StepsLink      = "[secrets-brokering/dynamic-delivery/check510-steps.md]"
	check511StepsLink      = "[secrets-brokering/dynamic-delivery/check511-steps.md]"
	dynamicDeliveryPlanRef = "secrets-brokering/dynamic-delivery/implementation-plan.md"
	dynamicDeliveryCommit  = "live-checks: Checks 510 and 511, written (not run)"
)

// dynamicDeliveryFiles is every file the folder holds, exactly: the three the
// work arrived with, plus the two steps files DS0.2 writes. A file the work
// adds joins this list in the same task, and a file that leaves it is an edit
// here too -- a folder nobody pins is a folder whose census drifts.
var dynamicDeliveryFiles = []string{
	"README.md", "spec.md", "reconciliation.md", "implementation-plan.md",
	"check510-steps.md", "check511-steps.md",
}

// TestDynamicDeliveryDocsPinned: the folder's census, the two checks'
// registrations in the register (both halves, both halves unrun), the links
// between the plan, the register, the steps files and the scripts, and the
// numbers the folder claims (Checks 510-519, D1930-D1959, answered
// 2026-11-09).
func TestDynamicDeliveryDocsPinned(t *testing.T) {
	// 1. The folder's files, read from the tree rather than from a list here: a
	// steps file nobody wrote is the red step this task starts from.
	entries, err := os.ReadDir(filepath.Join("..", "..", dynamicDeliveryDir))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		if e.IsDir() {
			t.Errorf("%s holds the directory %s: this folder is flat, and a reader's census of it is a file list", dynamicDeliveryDir, e.Name())
			continue
		}
		got = append(got, e.Name())
	}
	slices.Sort(got)
	want := slices.Clone(dynamicDeliveryFiles)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("%s holds %v, want %v: the folder's files are pinned by this test, so a file that joins or leaves it is a deliberate edit here", dynamicDeliveryDir, got, want)
	}

	// 2. The numbers the folder claims. The README's own table is where a reader
	// learns which numbers this work holds and which are only reserved, and the
	// reconciliation is where the answers of 2026-11-09 are recorded.
	readme := flattenDoc(readDoc(t, dynamicDeliveryReadme))
	for _, want := range []string{
		"**D1930–D1959**",
		"**Checks 510–519**",
		"510 and 511 assigned",
		"512–519 reserved and unused",
		"answered D1930–D1946 on 2026-11-09",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("%s does not say %q: the numbers this work holds are what the register's rows are checkable against", dynamicDeliveryReadme, want)
		}
	}
	// The plan's own Checks table gives the two numbers and what each measures,
	// and leaves 512-519 to nobody.
	planChecks := flattenDoc(docSection(t, dynamicDeliveryPlan, readDoc(t, dynamicDeliveryPlan), "## Checks"))
	for _, want := range []string{
		"| 510 |", "| 511 |", "512–519 | Reserved, unused",
	} {
		if !strings.Contains(planChecks, want) {
			t.Errorf("%s's Checks table does not say %q: the register's two rows are the numbers this plan holds", dynamicDeliveryPlan, want)
		}
	}

	// 3. Both halves of each registration, in the register, saying what an
	// unrun owner-run check says.
	register := readDoc(t, liveChecksDoc)
	for _, c := range []struct {
		number     string
		steps      string
		stepsRef   string
		script     string
		scriptTest string
	}{{
		number: "510", steps: check510Steps, stepsRef: check510StepsLink, script: check510Script, scriptTest: check510ScriptTest,
	}, {
		number: "511", steps: check511Steps, stepsRef: check511StepsLink, script: check511Script, scriptTest: check511ScriptTest,
	}} {
		row := checkRow(register, c.number)
		if row == "" {
			t.Fatalf("%s's register has no `| %s |` row: Check %s is written below it, and the table is where a reader finds it", liveChecksDoc, c.number, c.number)
		}
		for _, want := range []string{
			dynamicDeliveryPlanRef,
			"DS0.2",
			c.stepsRef,
			c.script,
			"OWNER-RUN, NOT RUN.",
			"no agent runs any step of it",
		} {
			if !strings.Contains(row, want) {
				t.Errorf("Check %s's register row does not say %q: the row is half the registration, and a row that leaves out the task, the steps file, the script or the fact that nobody has run it is half a check", c.number, want)
			}
		}

		section := checkSection(t, register, c.number)
		flat := strings.ToLower(flattenDoc(section))
		for _, want := range []string{
			"**not run.**",
			"no agent runs any step of it",
			c.stepsRef,
			c.script,
			"bash " + c.script,
			"ds0.2",
			// Both halves of a registration are the halves of one statement: a
			// section that promised a result while the row says NOT RUN, or the
			// reverse, reads as a live claim nobody made.
			"ds0.3",
		} {
			if !strings.Contains(flat, want) {
				t.Errorf("Check %s's section does not say %q: the section is the other half of the registration, and the two halves have to agree that this check is written and not run (DS0.3 is its run)", c.number, want)
			}
		}
	}

	// 4. The register's sections carry what each check settles, by the register's
	// own naming: U3 and F30 for 510, U4 for 511, each pointing at the decision
	// it gates. The two sections are read the way the registration halves above
	// are -- flattened and lowercased -- because the register writes its ids in
	// caps (**U3**, **DS2.2**) and whether a section spells one in caps or not
	// is not what a reader of it depends on.
	check510Section := strings.ToLower(flattenDoc(checkSection(t, register, "510")))
	for _, want := range []string{
		"u3", "f30", "versions.add", "secrets.delete", "create", "length", "ds2.2",
	} {
		if !strings.Contains(check510Section, want) {
			t.Errorf("Check 510's section does not say %q: what the check measures (U3, F30) and what it gates (DS2.2's IAM condition shape) are why it is run at all", want)
		}
	}
	check511Section := strings.ToLower(flattenDoc(checkSection(t, register, "511")))
	for _, want := range []string{"u4", "heartbeat", "instance", "ds1.5"} {
		if !strings.Contains(check511Section, want) {
			t.Errorf("Check 511's section does not say %q: what the check measures (U4) and what it gates (DS1.5's heartbeat) are why it is run at all", want)
		}
	}

	// 5. The steps files: each names its own script and that script's test, links
	// back to the register and to this plan, and says no agent runs any step.
	for _, c := range []struct {
		path, number, script, scriptTest string
	}{
		{check510Steps, "510", check510Script, check510ScriptTest},
		{check511Steps, "511", check511Script, check511ScriptTest},
	} {
		steps := flattenDoc(readDoc(t, c.path))
		for _, want := range []string{
			c.script,
			c.scriptTest,
			"docs/gcp-live-checklist.md",
			dynamicDeliveryPlanRef,
			"--dry-run",
			"bash " + c.script,
			"No agent runs any step of it",
			"**NOT RUN",
		} {
			if !strings.Contains(steps, want) {
				t.Errorf("%s does not say %q: a steps file that leaves out its own script, the register it records into or the fact that no agent runs it is a file a reader cannot follow", c.path, want)
			}
		}
	}

	// 6. The scripts and their sibling tests are really there -- a row naming a
	// file nobody wrote is a promise -- and each script keeps the properties the
	// register's row claims about it: the hard-coded sandbox project and region,
	// the refusal of `belong`, the dry run, and the one block.
	for _, c := range []struct{ script, scriptTest, number string }{
		{check510Script, check510ScriptTest, "510"},
		{check511Script, check511ScriptTest, "511"},
	} {
		body := readRepoFile(t, c.script)
		if _, err := os.Stat(filepath.Join("..", "..", c.scriptTest)); err != nil {
			t.Errorf("%s is named by Check %s and does not exist: %v", c.scriptTest, c.number, err)
		}
		for _, want := range []string{
			"set -Eeuo pipefail",
			"P=fugaro-dev",
			"L=us-east5",
			"belong",
			"--dry-run",
			"CHECK " + c.number + " RESULT",
			"RESULT END",
			"no credential value",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s does not carry %q: the register's row promises the owner a dry run, a hard-coded sandbox target, a refusal of `belong` and one block that carries no credential value", c.script, want)
			}
		}
		if bash, err := exec.LookPath("bash"); err == nil {
			if out, err := exec.Command(bash, "-n", filepath.Join("..", "..", c.script)).CombinedOutput(); err != nil {
				t.Errorf("bash -n %s: %v\n%s", c.script, err, out)
			}
		}
		// The Go runner in scripts/live_checks_test.go drives every check's
		// sibling test; a check added without its entry is a test no CI job runs.
		runner := readRepoFile(t, "scripts/live_checks_test.go")
		if !strings.Contains(runner, `{"`+filepath.Base(c.script)+`", "`+filepath.Base(c.scriptTest)+`"}`) {
			t.Errorf("scripts/live_checks_test.go does not list {%q, %q}: every check's sibling test is driven from that table, and a check missing from it is one no job runs", filepath.Base(c.script), filepath.Base(c.scriptTest))
		}
	}

	// 7. The plan's own DS0.2 section names what this task wrote, this test
	// included: the plan is what the reviewer of the task checks the work
	// against, and a Files or Tests list that misses a file is a plan the work
	// left behind.
	ds02 := flattenDoc(taskSection(t, dynamicDeliveryPlan, "### DS0.2 "))
	for _, want := range []string{
		check510Script, check511Script, check510Steps, check511Steps,
		check510ScriptTest, check511ScriptTest,
		"TestDynamicDeliveryDocsPinned",
		dynamicDeliveryCommit,
		"`go test ./internal/cli/ -run Docs -count=1`",
	} {
		if !strings.Contains(ds02, want) {
			t.Errorf("%s's DS0.2 section does not name %q: the task's Files, Tests and Commit are what a reviewer of it holds the work to", dynamicDeliveryPlan, want)
		}
	}

	// 8. The rigor-modes census counts the pins the tree holds, and its own rule
	// is that the folder which moves its row is named in it: this folder's pin
	// moved that row, so the census carries the sentence naming it. A row that
	// moved without one is the merge the census's own text calls the one that
	// did not.
	if !strings.Contains(flattenDoc(readDoc(t, rigorModesRecon)), "Amended 2026-11-10: `internal/cli/docs_dynamic_delivery_test.go`, the dynamic-delivery folder's pin") {
		t.Errorf("%s does not name this folder's pin as the amendment that moved its pin count: the row's own rule is that a folder which writes a pin moves the row in its own commit and says so", rigorModesRecon)
	}
}

// checkRow returns the register's summary row for a check number, or "" when
// the table has none.
func checkRow(register, number string) string {
	re := regexp.MustCompile(`(?m)^\|\s*` + regexp.QuoteMeta(number) + `\s*\|.*$`)
	return re.FindString(register)
}

// checkSection returns a register section, from `## Check N:` to the next `## `
// heading, so a check written after this one does not widen what is read.
func checkSection(t *testing.T, register, number string) string {
	t.Helper()
	i := strings.Index(register, "\n## Check "+number+":")
	if i < 0 {
		t.Fatalf("%s has no `## Check %s:` section: the row above it points at steps that have to be there", liveChecksDoc, number)
	}
	section := register[i+1:]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	return section
}

// The spec and the reconciliation are the two files the plan builds on; the pin
// on them is just that the folder's own README lists them, so a file that left
// the folder is caught by a reader of the README as well as by the census
// above.
var dynamicDeliveryReadmeRows = []string{dynamicDeliverySpec, dynamicDeliveryRecon, dynamicDeliveryPlan}

// TestDynamicDeliveryReadmeLinksTheFolder: the README's File table is where a
// reader starts, so every file it names has to be there (the census above reads
// the folder itself; this reads the table a reader follows).
func TestDynamicDeliveryReadmeLinksTheFolder(t *testing.T) {
	readme := readDoc(t, dynamicDeliveryReadme)
	for _, path := range dynamicDeliveryReadmeRows {
		base := filepath.Base(path)
		if !strings.Contains(readme, "("+base+")") {
			t.Errorf("%s's File table does not link %s: the table is where a reader of this folder starts", dynamicDeliveryReadme, base)
		}
	}
	// The two steps files are what DS0.2 added, and the table of files is the
	// one place a reader learns a steps file exists at all.
	for _, base := range []string{"check510-steps.md", "check511-steps.md"} {
		if !strings.Contains(readme, "("+base+")") {
			t.Errorf("%s's File table does not link %s: the two steps files are this work's checks, and the table is where a reader finds them", dynamicDeliveryReadme, base)
		}
	}
}
