package cli

// Checks 500 and 501 — the image setup skill's owner-run checks — as the
// register writes them, and the release that ships them.
//
// The work takes the block 500–509 (docs/image-setup-skill/implementation-plan.md,
// "Checks (reserved 500–509)") and writes two of it: 500 is the skill run end to
// end on the sandbox — the proposal the skill wrote, built through the cloud
// catalog, a run on a baked image, and the warning a repository with a stale
// `image:` block gets — and 501 is `fugaro update` then `fugaro run` on the
// sandbox, reaching a run record on the slim base. Both are task IS13's, both
// ship as one script each, and neither has been run.
//
// A check is registered by two things — a row in the summary table and a
// `## Check N:` section below it — and either half alone is a number a reader
// cannot use: a section without its row is a check nothing points at, and a row
// without its section is a promise of steps that are not written. The backstop
// that would assert the pairing register-wide is deliberately absent
// (docs_compute_backend_test.go asserts its absence), so each work's own docs
// tests carry it for its own numbers. This file carries it for 500 and 501, and
// for the release notes and the folder status that name them.
//
// The red step is this file run against a register that has neither half: the
// first failure names the row that is missing, the second the section.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	issPlan   = "docs/image-setup-skill/implementation-plan.md"
	issReadme = "docs/image-setup-skill/README.md"
	// The plan's own reserved block, whose two written numbers are these.
	issCheck500 = "500"
	issCheck501 = "501"
	// The invocation each row and section documents, argument for argument: the
	// script refuses to run without the checkout it builds from, so a register
	// that names `check-500.sh` alone documents a command that exits 2 with a
	// usage error before it measures anything. The two halves carry the same
	// string, and folding the section's whitespace is what lets one constant
	// pin both.
	issRun500 = "bash scripts/live-checks/check-500.sh --sandbox <a sandbox repository checkout>"
	issRun501 = "bash scripts/live-checks/check-501.sh --sandbox <a sandbox repository checkout>"
	// The next CLI release, from docs/release-streams.yaml (current 0.23.0, so
	// 0.24.0 "Uakari" is next). The notes land here before the cut; when the
	// release is cut this constant moves with the next one.
	issReleaseFile = "docs/releases/v0.24.0.md"
	issSummaryFile = "website/data/release-summaries.yaml"
)

// issChecks are the two numbers this work writes: the number, the command the
// owner types, and the sibling test that drives the script against fake tools.
var issChecks = []struct{ number, run, script, scriptTest string }{
	{issCheck500, issRun500, "scripts/live-checks/check-500.sh", "scripts/live-checks/check-500_test.sh"},
	{issCheck501, issRun501, "scripts/live-checks/check-501.sh", "scripts/live-checks/check-501_test.sh"},
}

// TestDocsChecks500501AreRegistered: each of the two has both halves of its
// registration — a row in the summary table and a `## Check N:` section below
// it — and both say the check has not been run, that the owner runs it, that it
// is never pointed at `belong`, and that no agent runs any step of it. Each
// section also names the one command the owner types, the sibling test that
// drives it dry and the task that wrote it, because a check that ships as a
// script is registered by naming it: a section whose steps are a second,
// hand-kept list is how the register and the script come to disagree.
func TestDocsChecks500501AreRegistered(t *testing.T) {
	register := readDoc(t, liveChecksDoc)
	for _, c := range issChecks {
		row := bitbucketWorkspaceCheckRowRE(c.number).FindString(register)
		if row == "" {
			t.Errorf("%s's register has no `| %s |` row: Check %s is written below it, and the table is where a reader finds it", liveChecksDoc, c.number, c.number)
		} else {
			// Lowercased the way docs_checks_180_181_test.go reads its own rows:
			// the register writes the marker in capitals (`**OWNER-RUN, NOT
			// RUN.**`) and the pins below are the sentence, not its case.
			flatRow := strings.ToLower(flattenDoc(row))
			for _, want := range []string{
				// The register's own marker for an owner-run check that has not
				// been run: a row that lost either half reads as something an
				// agent did, on a sandbox.
				"**owner-run, not run.**",
				"no agent runs any step of it",
				// The installation this check must never touch.
				"never `belong`",
				// Where the owner's one command and its dry driver live: the row
				// is the half a reader scans, so the script has to be named
				// there and not only in the section below — and named with the
				// argument it refuses to run without.
				c.run,
				c.scriptTest,
				// The task that wrote it and the plan it comes from.
				"is13",
				"image-setup-skill",
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
			c.run,
			c.scriptTest,
			"is13",
			issPlan,
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
	for _, c := range issChecks {
		i := strings.Index(register[at:], "\n## Check "+c.number+":")
		if i < 0 {
			t.Errorf("%s writes Check %s's section out of order: the block reads in the order the numbers give", liveChecksDoc, c.number)
			continue
		}
		at += i
	}
}

// TestDocsCheck500StatesWhatOnlyTheRunSettles: the section has to carry the
// readings the check exists for, in the words the owner follows, because IS13
// writes the script and no other document holds its steps. Each sentence here
// is one no offline run can produce: what the skill proposed, that the proposal
// is built through the cloud catalog, that one run starts on a baked image, and
// that a repository with a stale `image:` block is told its key is ignored.
func TestDocsCheck500StatesWhatOnlyTheRunSettles(t *testing.T) {
	section := issSection(t, issCheck500)
	for _, want := range []string{
		// The proposal the skill wrote, read back rather than assumed: the
		// skill itself is the owner's to run in their own session.
		"`fugaro image ls --json`",
		"`fugaro image survey --json`",
		// The skill's own cap (D1903), which nothing in the CLI enforces.
		"at most four",
		"D1903",
		// The build that counts is the cloud one.
		"`fugaro image build`",
		"Cloud Build",
		// The run the check launches, and what says it started on a baked
		// image rather than on the installation's own.
		"a run on a baked image",
		"`baked_commit`",
		// The warning IS1 gives a repository whose own `image:` block is
		// ignored (D1900), and the repository the owner points the check at.
		"is ignored",
		"D1900",
		"`--stale-repo`",
		// The record a run reaches, which is where the run's own reading is.
		"result.json",
		// The one checkout per entry the build needs, because a catalog
		// entry's Dockerfile is built from the repository it belongs to.
		"`CHECK500_CHECKOUT_<entry>`",
		// What the check cannot do, said rather than left to the reader: the
		// entries it builds are the ones the catalog already lists, and nothing
		// here writes a catalog of its own — the skill is the owner's to run in
		// their own session, and a check that proposed one would be proposing
		// the thing it measures.
		"invents no entry of its own",
		// The target, and the guard that keeps it off the dev installation.
		"fugaro-dev",
		"never `belong`",
	} {
		if !strings.Contains(section, strings.Join(strings.Fields(want), " ")) {
			t.Errorf("Check 500's section does not say %q: the check is the only place these facts are ever measured, and IS13 writes it", want)
		}
	}
}

// TestDocsCheck501StatesWhatOnlyTheRunSettles: 501 is the slim base reached by
// the one command that is the whole upgrade, and then by a run. The section has
// to say what the owner types, what the update asks for, and which record the
// run has to reach.
func TestDocsCheck501StatesWhatOnlyTheRunSettles(t *testing.T) {
	section := issSection(t, issCheck501)
	for _, want := range []string{
		// The one command that is the whole upgrade, and the run after it.
		"run `fugaro update`",
		"then `fugaro run`",
		// What the update asks, and does not assume.
		"its own confirmation",
		"base-derived",
		// The base the release ships (IS4) and how the machine's pin is read.
		"slim base",
		"`fugaro doctor --json`",
		// The record the run has to reach, read from the runs bucket.
		"result.json",
		// The run is the reading: the update moving a pin says nothing until a
		// job starts on it.
		"`fugaro runs ls",
		"terminal",
		// The target, and the guard that keeps it off the dev installation.
		"fugaro-dev",
		"never `belong`",
	} {
		if !strings.Contains(section, strings.Join(strings.Fields(want), " ")) {
			t.Errorf("Check 501's section does not say %q: the check is the only place these facts are ever measured, and IS13 writes it", want)
		}
	}
}

// issSection is the register's text for one check, flattened and folded, in the
// case it is written in: the pins below are the register's own tokens — a
// decision number, a command's flag, a marker — so the case is part of the claim.
func issSection(t *testing.T, number string) string {
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

// TestDocsImageSetupSkillSaysTheWorkIsBuilt: the folder's own status says what
// is in the tree — IS1 to IS12 merged, the skill shipped, and the two checks
// written and NOT RUN — and the numbers it holds move with the checks it
// writes. The line it was written with stays below the current one, so the
// claim remains readable as what was true of the moment it describes.
func TestDocsImageSetupSkillSaysTheWorkIsBuilt(t *testing.T) {
	readme := readDoc(t, issReadme)
	var current string
	for _, line := range strings.Split(readme, "\n") {
		if strings.HasPrefix(line, "**Status as of 2026-11-12") {
			current = line
			break
		}
	}
	if current == "" {
		t.Fatalf("%s carries no `**Status as of 2026-11-12` line: the folder's status has to move with the tree, and IS13 is the task that writes the release — if the line was reworded, move this guard's marker with it", issReadme)
	}
	for _, want := range []string{"IS1", "IS12", "IS13", "fugaro:images", "Check 500", "Check 501", "NOT RUN"} {
		if !strings.Contains(current, want) {
			t.Errorf("%s's current status line does not say %q, so the folder reads as if that part were not in the tree: %s", issReadme, want, current)
		}
	}
	// The first-written line is kept: the folder's history is kept rather than
	// rewritten.
	if !strings.Contains(readme, "**Status as of 2026-11-10: in progress, 4/14 merged") {
		t.Errorf("%s: the status as first written is gone: the folder keeps the line it was written with below the current one", issReadme)
	}
	// Numbers held: the block's two written numbers are taken.
	held := ""
	for _, line := range strings.Split(readme, "\n") {
		if strings.HasPrefix(line, "**Numbers held:**") {
			held = line
			break
		}
	}
	if held == "" {
		t.Fatalf("%s carries no `**Numbers held:**` line: it is where a reader sees which numbers the work takes", issReadme)
	}
	for _, want := range []string{"checks 500-509", "500 and 501 are taken"} {
		if !strings.Contains(held, want) {
			t.Errorf("%s's numbers-held line does not say %q: two of the block's numbers are written below, and a line still reading `none taken` is a claim the register contradicts: %s", issReadme, want, held)
		}
	}
	if strings.Contains(held, "none taken") {
		t.Errorf("%s's numbers-held line still says no number of the block is taken, though Check 500 and Check 501 are written: %s", issReadme, held)
	}

	// The plan names the scripts where they are: the repository keeps its
	// owner-run checks under scripts/live-checks/, each with its own _test.sh.
	plan := readDoc(t, issPlan)
	for _, c := range issChecks {
		if !strings.Contains(plan, c.script) {
			t.Errorf("%s does not name %s: the plan is what a reviewer of IS13 holds the work to, and a Files list naming a path nothing is at is a reader's dead end", issPlan, c.script)
		}
		if !strings.Contains(plan, c.scriptTest) {
			t.Errorf("%s does not name %s: every owner-run check ships with the test that drives it against fake tools", issPlan, c.scriptTest)
		}
	}
	if regexp.MustCompile("`scripts/check-50[01]\\.sh`").MatchString(plan) {
		t.Errorf("%s still names a check script at the repository root: the owner-run checks live under scripts/live-checks/, and a plan that points elsewhere is one a reader follows to nothing", issPlan)
	}
	// The plan's Checks section is where IS13's own row reads them, and the
	// owner-run rule is stated there rather than inferred.
	checks := flattenDoc(plan)
	for _, want := range []string{"**Check 500 (OWNER-RUN):**", "**Check 501 (OWNER-RUN):**", "No agent performs any step"} {
		if !strings.Contains(checks, want) {
			t.Errorf("%s's checks block does not say %q: the two numbers and the rule that no agent runs either are what IS13 implements", issPlan, want)
		}
	}
}

// TestDocsReleaseNotesForTheImageSetupSkill: the release that ships the skill
// carries its notes, its end-user summary line and the operator line the plan
// writes, and the notes follow the format docs/releases/README.md states — 3 to
// 6 user bullets, the two `###` headings the site's changelog check recognises,
// and the operators' step the plan asks for.
func TestDocsReleaseNotesForTheImageSetupSkill(t *testing.T) {
	notes := readDoc(t, issReleaseFile)
	var bullets int
	var headings []string
	section := ""
	for _, line := range strings.Split(notes, "\n") {
		trimmed := strings.TrimRight(line, " \t")
		switch {
		case strings.HasPrefix(trimmed, "### "):
			headings = append(headings, strings.TrimSpace(strings.TrimPrefix(trimmed, "### ")))
			section = strings.TrimSpace(strings.TrimPrefix(trimmed, "### "))
			continue
		case strings.HasPrefix(trimmed, "## "), trimmed == "##", strings.HasPrefix(trimmed, "# "):
			t.Errorf("%s carries the top-level heading %q: the file's headings are `###`, because GoReleaser puts the file under its own `## Highlights`", issReleaseFile, trimmed)
			continue
		}
		if strings.HasPrefix(trimmed, "- ") && section == "" {
			bullets++
		}
	}
	if bullets < 3 || bullets > 6 {
		t.Errorf("%s has %d bullets for users; docs/releases/README.md asks for 3 to 6", issReleaseFile, bullets)
	}
	for _, h := range headings {
		if h != "For operators" && h != "Known limits" {
			t.Errorf("%s carries the heading %q; the two a release file may carry are `For operators` and `Known limits`, and the site's changelog check refuses any other", issReleaseFile, h)
		}
	}
	flat := flattenDoc(notes)
	for _, want := range []string{
		// The plan's `### For operators` line, in the owner's own words: the
		// one command, and what it asks rather than assumes.
		"### For operators",
		"run `fugaro update`",
		"its own confirmation",
		// The limits of a release whose two checks have not been run, said in
		// the notes rather than left to the register.
		"### Known limits",
		"Check 500",
		"Check 501",
		"NOT RUN",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s does not say %q: a release ships with its known limits in its notes, and the one command that is the whole upgrade is what its operators run", issReleaseFile, want)
		}
	}

	// The end-user summary, one line, the site's words: no backtick, no
	// internal id and no repository path (website/test/changelog-page.mjs holds
	// the last two, and the file's own header holds the shape).
	summaries := readDoc(t, issSummaryFile)
	entry := regexp.MustCompile(`(?m)^  v0\.24\.0: (.+)$`).FindStringSubmatch(summaries)
	if entry == nil {
		t.Fatalf("%s carries no `  v0.24.0: ...` line: the release's summary is a required artifact wherever the file is, and scripts/release.sh refuses to start without it", issSummaryFile)
	}
	if strings.TrimSpace(entry[1]) == "" {
		t.Errorf("%s's v0.24.0 entry is empty: the changelog page renders it under the release's heading", issSummaryFile)
	}
	if strings.Contains(entry[1], "`") {
		t.Errorf("%s's v0.24.0 entry carries a backtick: the entry is one line of plain text, and the site renders it as written", issSummaryFile)
	}
}
