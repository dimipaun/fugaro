package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Checks 390–392 — the three owner-run halves of the composite management
// commands — as the register writes them.
//
// AP10 of docs/composite-commands/implementation-plan.md writes the release
// actions and the live checks of the work that made a management command run
// the steps its change owes (**D1561**): 390 reads one command alone (an
// `image add` that must leave nothing owed), 391 the pending record's collapse
// (**D1562**, **D1570**), and 392 the failure path, where a step that cannot run
// keeps the change and the step and exits non-zero (**D1563**). Each is ONE
// script the owner runs, with a sibling test that drives it against a fake
// `fugaro` (and, for 390, a fake `gcloud`) so its FAILs are provable offline.
//
// A check is registered by two things — a row in the summary table and a
// `## Check N:` section below it — and either half alone is a number a reader
// cannot use. The register-wide backstop that would assert the pairing is
// deliberately absent (docs_compute_backend_test.go asserts its absence), so
// each work's own docs tests carry it for its own numbers; these are 390–392's.
const (
	compositeChecksPlan = "docs/composite-commands/implementation-plan.md"
	// The next CLI release, from docs/release-streams.yaml (current 0.28.0, so
	// 0.29.0 "Zander" is next). A feature writes its bullets into the notes of
	// the release it ships in BEFORE the cut — that is how v0.28.0's own file
	// grew, and how docs_checks_500_501_test.go's release file was written —
	// and when the release is cut this constant moves with the next one.
	compositeChecksRel = "docs/releases/v0.29.0.md"
)

// compositeChecks is each check's number and the script and sibling test the
// row and the section both name. The number is the only thing that differs
// between the three, so the pins below run over this table rather than three
// times by hand.
var compositeChecks = []struct{ n, script, sibling string }{
	{"390", "scripts/live-checks/check-390.sh", "scripts/live-checks/check-390_test.sh"},
	{"391", "scripts/live-checks/check-391.sh", "scripts/live-checks/check-391_test.sh"},
	{"392", "scripts/live-checks/check-392.sh", "scripts/live-checks/check-392_test.sh"},
}

// TestDocsCompositeChecksAreRegistered: each of 390–392 has both halves of its
// registration, each half says it has not been run, that the owner runs it, that
// it is never pointed at `belong` and that no agent runs any step of it, and
// each names the one command the owner types and the sibling test beside the
// script.
func TestDocsCompositeChecksAreRegistered(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	runner := readDoc(t, "scripts/live_checks_test.go")

	for _, c := range compositeChecks {
		// Lowercased, because the register writes its markers in capitals and
		// the pins below are the sentence and not its case.
		row := strings.ToLower(flattenDoc(registerRow(t, register, c.n)))
		for _, want := range []string{
			// The register's own marker for an owner-run check that has not
			// been run: a row that lost either half reads as something an
			// agent did.
			"**owner-run, not run.**",
			"no agent runs any step of it",
			"never `belong`",
			"bash " + c.script,
			c.sibling,
			// The task that wrote it and the plan it comes from.
			"ap10",
			"composite-commands/implementation-plan.md",
		} {
			if !strings.Contains(row, want) {
				t.Errorf("Check %s's row does not say %q: an owner-run check is registered with the label it was run under, the command you type and the sibling test that proves it", c.n, want)
			}
		}

		// Flattened and folded, because the register's prose wraps: a pin that
		// missed a phrase across a line break would be a pin on the line
		// breaks.
		section := strings.ToLower(flattenDoc(registerSection(t, register, "\n## Check "+c.n+":")))
		for _, want := range []string{
			"**not run.**",
			"no agent runs any step of it",
			"never `belong`",
			c.script,
			c.sibling,
			"ap10",
			"composite-commands/implementation-plan.md",
			// The block the owner pastes back, and the exits that read it.
			"check " + c.n + " result",
			"exit `0` means the claim held",
			// The verdict a row that could not be read ends at, and the exit
			// that says so rather than a FAIL about a fact nobody measured.
			"**not measured**",
			// The failure this check is: a management command that wrote its
			// change and left its steps to a second command.
			"owe nothing",
		} {
			if !strings.Contains(section, want) {
				t.Errorf("Check %s's section does not say %q: the section is the only place the check's steps are written, and AP10 writes it", c.n, want)
			}
		}

		// The scripts and the sibling test are really there: a row naming a
		// file nobody wrote is a promise.
		for _, p := range []string{c.script, c.sibling} {
			if _, err := os.Stat(filepath.Join("..", "..", p)); err != nil {
				t.Errorf("Check %s's section names %s and there is no such file: %v", c.n, p, err)
			}
		}
		// The Go runner in scripts/live_checks_test.go drives every check's
		// sibling test; a check added without its entry is a test no job runs.
		if !strings.Contains(runner, `{"`+filepath.Base(c.script)+`", "`+filepath.Base(c.sibling)+`"}`) {
			t.Errorf("scripts/live_checks_test.go does not list {%q, %q}: every check's sibling test is driven from that table, and a check missing from it is one no job runs", filepath.Base(c.script), filepath.Base(c.sibling))
		}
	}
}

// TestDocsCheck392SaysWhyItCannotRunOnAManagedProject: 392 forces its failure
// with the marker that refuses a policy publication in a coding agent's session
// (**D1571**), and on a **managed** project that marker is no refusal at all —
// the managed gate applies the stage in such a session exactly as it does for
// the owner (**D1016**) — so the forced run would publish and exit 0 and the
// check would read its loudest FAIL about the one thing that did not happen. The
// refusal has to be stated, and stated as a refusal to run rather than as a
// caveat a reader could take as a FAIL to expect.
func TestDocsCheck392SaysWhyItCannotRunOnAManagedProject(t *testing.T) {
	section := strings.ToLower(flattenDoc(registerSection(t, readDoc(t, liveChecksDoc), "\n## Check 392:")))
	for _, want := range []string{
		// The gate and the rule that makes the marker useless there.
		"managed",
		"d1016",
		// The read that decides it, named: the marks' own answer, not a guess.
		"fugaro managed status --json",
		// The refusal itself, and what it would otherwise misreport.
		"refuses to run",
		"exit 0",
		// The marker is set on the child's command line and nowhere else: a
		// check that exported it would refuse its own run before it measured
		// anything.
		"`claudecode=1`",
		"the child's own command line",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Check 392's section does not say %q: a managed project makes the forced failure no failure, and the section has to refuse the run rather than report it", want)
		}
	}
}

// TestDocsCompositeReleaseNotesCarryTheReleaseActions: the notes of the release
// this work ships in state what a management command now does — one command runs
// the steps its change owes, `--no-apply` stops it, and a step that cannot run
// costs the run and not the work — and they carry the operator section this
// work's plan writes, which is the whole upgrade and nothing more: `fugaro
// update`, and no second operator command.
//
// The section is due in the same pull request as the bullets, not in the one
// that cuts the release: docs/composite-commands/implementation-plan.md names it
// for AP10 and answers it with "run `fugaro update`" and nothing else, and
// docs/releases/vX.Y.Z.md of the last twelve releases each carry it. The
// end-user summary line is the one that waits for the cut
// (docs/releases/README.md), so this test asserts nothing about it.
func TestDocsCompositeReleaseNotesCarryTheReleaseActions(t *testing.T) {
	notes := readDoc(t, compositeChecksRel)
	flat := strings.ToLower(flattenDoc(notes))
	for _, want := range []string{
		// What a management command does now: it runs the steps that make its
		// change live, so no second command is needed (**D1561**).
		"runs the steps that make its change live",
		"`--no-apply` saves the change and stops",
		"fugaro apply",
		// The failure path: the change is saved, the step is owed and the run
		// is the thing that is lost (**D1563**).
		"leaves the change saved and the step owed",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s does not say %q: AP10 writes the release actions, and a reader has to be able to tell what changed for them", compositeChecksRel, want)
		}
	}

	// The section, and not a section that happens to be there: what an operator
	// owes after this release is one step, and it is the one the plan answers
	// this work with. A file whose section is missing is a release that ships
	// the step to nobody.
	i := strings.Index(flat, "### for operators")
	if i < 0 {
		t.Errorf("%s carries no `### For operators` section: AP10 writes the release actions, and the one an operator owes after this release is `fugaro update` under a `### For operators` heading in the same pull request", compositeChecksRel)
	} else if !strings.Contains(flat[i:], "fugaro update") {
		t.Errorf("%s carries a `### For operators` section that does not name `fugaro update`: the section of a release is the whole upgrade and nothing more (docs/releases/README.md, docs/composite-commands/implementation-plan.md)", compositeChecksRel)
	}
}
