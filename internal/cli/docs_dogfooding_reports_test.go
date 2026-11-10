package cli

// Fugaro's own reports, as the page a person running Fugaro on this repository
// reads them (plan T4.4, D324).
//
// The task-governance plan answers one question about this repository's own
// reports: they go into this public repository, under the redaction rules a
// report is written to, because they are the product's own dogfooding of the
// feature and the source the website's cost figures are read from (D324,
// docs/task-governance/reconciliation.md). docs/dogfooding.md is where a person
// meets that answer, and the page carried none of it: the report run, the
// folder and the rule that no person may be read back out of a committed report
// were documented in docs/recipes.md, in the working skill's reference and in
// the digest's own tests, and nowhere on the page about this repository's own
// installation.
//
// The pin is deliberately half code and half prose, the way this package's
// other docs tests are. The folder the page names is not a string the page owns:
// it is digest.Dir, the one folder `fugaro runs digest` reads and writes by
// default, so the assertion is made against the constant and a layout that moves
// leaves the page behind and fails here. The launch the page names is the
// catalog's own run-digest recipe, read out of the catalog rather than retyped,
// so a recipe that is renamed or dropped fails here too. What is left is the
// prose a reader acts on: the cadence is the owner's and no agent sets it up
// (the shipped command schedules nothing, D965), a report carries no address and
// no task text (D323's allowed list, G1), the website's figures stay a person's
// hand edit, and the first reports are the owner's own report run, which the
// register holds as Check 136.
//
// The red step is this file against the page as it stands: the section is not
// there, so the first assertion -- that the page names the reports' folder --
// fails.
//
// What it does not pin is the register's own state. Whether Check 136 has been
// run is the register's claim, held by
// internal/cli/docs_task_governance_checks_test.go against
// docs/gcp-live-checklist.md; a second copy of it here would be a second copy to
// drift, and the page points at the check rather than restating its verdict.

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/digest"
	"github.com/dimipaun/fugaro/internal/recipe"
)

// dogfoodingReportsSection returns the page's own section on this repository's
// reports, from its heading to the next `## ` heading. Bounded rather than read
// whole, so a phrase the rest of the page happens to carry does not satisfy a
// pin made about this section.
func dogfoodingReportsSection(t *testing.T) string {
	t.Helper()
	page := readDoc(t, "docs/dogfooding.md")
	heading := "\n## Fugaro's own run reviews\n"
	i := strings.Index(page, heading)
	if i < 0 {
		t.Fatalf("docs/dogfooding.md has no `## Fugaro's own run reviews` section: this repository's reports are the page's own practice, and D324's answer about where they go has to be readable on it")
	}
	section := page[i+len(heading):]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	return section
}

// TestDocsDogfoodingNamesThisRepositorysReportFolder: the section names the
// reports' folder, and the folder it names is the CLI's own -- digest.Dir, the
// default `fugaro runs digest` reads and writes -- so the page and the layout
// cannot drift apart.
func TestDocsDogfoodingNamesThisRepositorysReportFolder(t *testing.T) {
	if digest.Dir != "docs/fugaro-reviews" {
		t.Fatalf("digest.Dir = %q: D321's layout is docs/fugaro-reviews, and a move of it is what this page's section and this test have to follow", digest.Dir)
	}
	flat := flattenDoc(dogfoodingReportsSection(t))
	if !strings.Contains(flat, digest.Dir) {
		t.Errorf("docs/dogfooding.md's reports section never names %q: the page says where this repository's reports live, and that folder is digest.Dir", digest.Dir)
	}
	// The index beside the periods is what a reader following the page's link
	// into the folder lands on, so the section names it as well.
	if !strings.Contains(flat, "README.md") {
		t.Errorf("docs/dogfooding.md's reports section never names the generated index beside the reports: %s holds one README.md, and a reader who opens the folder without knowing that reads it as a report", digest.Dir)
	}
}

// TestDocsDogfoodingNamesTheLaunchThatWritesThem: the section names the launch
// a person schedules, and the recipe it names is in the catalog and is still
// the one that starts with the digest step -- read out of internal/recipe
// rather than retyped, so a renamed or dropped recipe fails here.
func TestDocsDogfoodingNamesTheLaunchThatWritesThem(t *testing.T) {
	text, ok := recipe.CatalogText("run-digest")
	if !ok {
		t.Fatal("the catalog has no run-digest recipe: docs/dogfooding.md's section names it as the launch that writes this repository's reports")
	}
	if !strings.Contains(string(text), "digest:") {
		t.Errorf("the catalog's run-digest recipe no longer carries a digest step:\n%s", text)
	}
	flat := flattenDoc(dogfoodingReportsSection(t))
	if !strings.Contains(flat, "fugaro run --recipe run-digest") {
		t.Errorf("docs/dogfooding.md's reports section never names `fugaro run --recipe run-digest`: the page says what writes the reports, and that launch is it")
	}
}

// TestDocsDogfoodingKeepsTheCadenceTheOwners: the section says who schedules the
// run and that nothing schedules itself. The shipped command writes no cron
// job, no workflow and no routine (D965), so a page that left the reader to
// infer a schedule is a page a person reads as one Fugaro keeps for them.
func TestDocsDogfoodingKeepsTheCadenceTheOwners(t *testing.T) {
	// Folded, because the rule is the page's own sentence and the page opens it
	// with a capital.
	flat := strings.ToLower(flattenDoc(dogfoodingReportsSection(t)))
	for _, want := range []string{
		// The schedule is the owner's, said as a rule rather than implied.
		"the schedule is the owner's",
		// And no agent sets one up on their behalf.
		"no agent sets it up",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("docs/dogfooding.md's reports section never says %q: the cadence is the user's and nothing sets one up for them", want)
		}
	}
}

// TestDocsDogfoodingSaysWhatAReportMayNotCarry: the page commits reports to a
// public repository, so it is where D323's redaction rule has to be readable --
// no address and no task text, and the figures the run's own records already
// give a reader. The two halves are pinned together because either alone reads
// as a page that has not weighed the other.
func TestDocsDogfoodingSaysWhatAReportMayNotCarry(t *testing.T) {
	flat := flattenDoc(dogfoodingReportsSection(t))
	for _, want := range []string{
		"no address",
		"no task text",
		// The reading that makes the redaction worth stating: the report is
		// safe to commit where the page says it goes.
		"public",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("docs/dogfooding.md's reports section never says %q: the reports are committed to a public repository, and what a report may hold is the rule that makes that safe", want)
		}
	}
}

// TestDocsDogfoodingLeavesTheWebsiteFiguresToAPerson: D324's answer names the
// other half of the dogfooding -- the website's cost figures are read from the
// reports, and refreshing them stays a person's hand edit. The pin is on both
// halves, because the figures file is owned by the website's own work stream
// and a run that wrote it would be a second, unmeasured source.
func TestDocsDogfoodingLeavesTheWebsiteFiguresToAPerson(t *testing.T) {
	flat := flattenDoc(dogfoodingReportsSection(t))
	if !strings.Contains(flat, "website/data/typical-costs.yaml") {
		t.Errorf("docs/dogfooding.md's reports section never names website/data/typical-costs.yaml: D324's answer is that the reports are where the page's figures are read from, and the page has to name the file")
	}
	if !strings.Contains(flat, "by a person") {
		t.Errorf("docs/dogfooding.md's reports section never says the website's figures stay a person's: a run that wrote them would be a figure nothing measured")
	}
}

// TestDocsDogfoodingPointsAtTheRegistrationOfTheFirstReports: the first reports
// are the owner's own report run, and the register holds it. The page points at
// the check rather than restating its verdict, so the pin is on the pointer --
// the check number and the register that carries it -- and not on whether it
// has been run, which is the register's own claim.
func TestDocsDogfoodingPointsAtTheRegistrationOfTheFirstReports(t *testing.T) {
	flat := flattenDoc(dogfoodingReportsSection(t))
	for _, want := range []string{
		"Check 136",
		"gcp-live-checklist.md",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("docs/dogfooding.md's reports section never names %q: the first reports are the owner's own report run, and the register's Check 136 is where its steps are written", want)
		}
	}
}
