package cli

// The console-on-Windows documents' Go-side pins (docs/console-windows/).
//
// This file exists for the reason every other docs_*_test.go here does: a
// document is a claim, and a claim with no test under it is a claim that rots.
// What it guards is the owner's ruling of 2026-10-20 -- the console drives the
// LINUX fugaro inside WSL, and the Windows pack runs at release time only --
// and the four places that ruling is written down: the plan for the console's
// Windows build, the reconciliation it is written against, the staged
// release-time pack job, and the two sentences outside that folder the staging
// moves.
//
// Each pin is a claim that can silently go false:
//
//   - **The twelve decisions are answered, the build is approved, and the two
//     are separate dates.** The owner answered D1-D12 on 2026-10-20, D7 against
//     the recommendation (the pack runs at release time only), and approved the
//     build itself on 2026-10-21 for 0.11.0 on `console-windows-wsl`. A document
//     still saying "none is answered" would describe a plan nobody may start; a
//     gate that had quietly gone back to "not passed" would stop work the owner
//     has authorised; and a status line that lost the release or the branch
//     would leave the parts with nowhere to collect.
//   - **The staged job runs at release time and nowhere else.** The ruling is
//     negative -- never on a pull request, not even path-filtered -- and a
//     trigger block is exactly what an edit adds back without noticing.
//   - **The two sentences outside the folder agree with the file.** A staging
//     README that described a pull-request job, and a site page that did too,
//     would each be a reader told the wrong thing about a job that runs once
//     per release.

import (
	"strconv"
	"strings"
	"testing"
)

const (
	// windowsConsoleWorkflowPath is the staged release-time pack job and
	// appliedConsoleWindowsPath is where the owner copies it. Its copy-in
	// removes the staged copy -- the file is new, so there is nothing for it to
	// be byte-identical to -- which is why every reader below goes through
	// consoleWorkflowText rather than reading one of the two paths directly.
	windowsConsoleWorkflowPath = "docs/pending-workflows/console-windows.yml"
	appliedConsoleWindowsPath  = ".github/workflows/console-windows.yml"
	// windowsConsoleJobName is the job's key AND its display name, for
	// console-linux.yml's reason: a check name nobody can match is a check
	// nobody can talk about.
	windowsConsoleJobName = "console-windows"
	// windowsConsoleReleaseTag is the tag the pack runs on: the CLI's own strict
	// vX.Y.Z pattern, the same one console-release.yml runs on, because the
	// Windows pack is a release-time job and a release is what a tag makes.
	windowsConsoleReleaseTag = "v[0-9]+.[0-9]+.[0-9]+"
)

// TestDocsWindowsConsoleReconciliationRecordsTheOwnerAnswers is the answer
// record's own test: section 4 of the reconciliation carries a twelve-row
// table, one row per decision, each with the option the owner chose and a
// one-line why -- and every answer is the recommendation except D7's.
//
// The table is read as a table rather than as prose, for the reason
// TestDocsDesktopConsoleDesignCarriesTheOwnerAnswers gives: a substring check
// is satisfied by a sentence *about* the answers, so a cell could be emptied or
// flipped with the test still green. Every row is compared, in order, by key.
//
// The mutations that prove it: empty D6's answer cell, change D7's "(c)" to
// "(a)", and put the status line's "no decision in §4 is answered" back. Each
// fails by naming the row -- or, for the status line, by naming the sentence.
func TestDocsWindowsConsoleReconciliationRecordsTheOwnerAnswers(t *testing.T) {
	const path = "docs/console-windows/reconciliation.md"
	doc := readDoc(t, path)

	const header = "| # | The owner's answer (2026-10-20) | Why, in one line |"
	if !strings.Contains(doc, header) {
		t.Fatalf("%s has no answers table under %q: the owner answered D1-D12 on 2026-10-20, and the record has to be a table a reader can check a decision against", path, header)
	}
	if !strings.Contains(flattenDoc(doc), "Answering a decision is not approving") {
		t.Errorf("%s no longer says that answering a decision is not approving the build: the answers are answers, and the plan's OWNER APPROVAL GATE is a separate, dated thing -- passed on 2026-10-21", path)
	}

	rows := docTableAfter(t, path, doc, header)
	if len(rows) != 12 {
		t.Fatalf("%s's answer table carries %d rows, want the twelve decisions D1 to D12", path, len(rows))
	}
	seen := map[string]bool{}
	for _, cells := range rows {
		if len(cells) < 3 {
			t.Errorf("%s's answer table has a row of %d cells (%v), want the key, the answer and the one-line why", path, len(cells), cells)
			continue
		}
		key, answer, why := cells[0], cells[1], cells[2]
		seen[key] = true
		if strings.Trim(answer, "* ") == "" {
			t.Errorf("%s leaves %s unanswered: its answer cell is empty, and the owner answered all twelve on 2026-10-20", path, key)
		}
		if strings.Trim(why, "* ") == "" {
			t.Errorf("%s gives no one-line why for %s: an answer recorded without its reason is a bare letter", path, key)
		}
		// D7 is the one answer that is not the recommendation, and the table has
		// to say so: the owner ruled the pack runs at release time only, which is
		// option (c) -- the option the recommendation had refused.
		if key == "**D7**" {
			if !strings.Contains(answer, "(c)") {
				t.Errorf("%s's D7 answer is %q: the owner's ruling of 2026-10-20 is option (c), the release-time-only pack, which the recommendation had refused", path, answer)
			}
			if !strings.Contains(answer, "release time") {
				t.Errorf("%s's D7 answer is %q: it has to say the pack runs at release time, which is the whole of the ruling", path, answer)
			}
			continue
		}
		if !strings.Contains(answer, "(a)") {
			t.Errorf("%s's %s answer is %q: the owner took the recommended option (a) for every decision but D7", path, key, answer)
		}
	}
	for i := 1; i <= 12; i++ {
		key := "**D" + strconv.Itoa(i) + "**"
		if !seen[key] {
			t.Errorf("%s's answer table has no row for %s", path, key)
		}
	}

	// D6 and D7 are the two answers with a consequence a table cell cannot
	// carry: D6's residual window and D7's rewritten section. Both are read in
	// their own section as well, because the section is where a reader lands.
	d6 := windowsConsoleBlock(t, path, doc, "### D6 — ", "\n#")
	for _, want := range []string{
		"Answered, 2026-10-20",
		"kill inside the distribution",
	} {
		if !strings.Contains(flattenDoc(d6), want) {
			t.Errorf("%s's D6 section no longer carries %q: the owner took (a), the kill inside the distribution, and the section is where its residual window is written down rather than implied", path, want)
		}
	}
	d7 := windowsConsoleBlock(t, path, doc, "### D7 — ", "\n#")
	for _, want := range []string{
		"Answered (c)",
		"waste of resources",
		"release time",
		"never on a pull request",
		"a packaging break shows at release time",
		"win32",
		"win",
	} {
		if !strings.Contains(flattenDoc(d7), want) {
			t.Errorf("%s's D7 section no longer carries %q: D7 is the one decision the owner ruled against the recommendation, so its section has to say what the ruling is, in whose words, what it accepts and what it leaves where it was", path, want)
		}
	}

	// The status line, which is the sentence a reader cites and the one place a
	// summary can contradict the table under it: it said "no decision in §4 is
	// answered" while §4 recorded all twelve. Read as its own paragraph, so the
	// answer table below cannot satisfy it.
	status := flattenDoc(windowsConsoleBlock(t, path, doc, "**Status:", "\n\n"))
	for _, want := range []string{
		"The twelve decisions in §4 are answered",
		"answering a decision is not approving the build",
		"OWNER APPROVAL GATE was passed on 2026-10-21",
		"0.11.0",
		"console-windows-wsl",
	} {
		if !strings.Contains(status, want) {
			t.Errorf("%s's status line no longer carries %q: §4 answers all twelve decisions, the plan's gate was passed on 2026-10-21 for 0.11.0 on console-windows-wsl, and the summary has to say both -- an answer is not an approval, and the approval is a separate, dated thing", path, want)
		}
	}
	if strings.Contains(status, "no decision in §4 is answered") {
		t.Errorf("%s's status line says no decision in §4 is answered, and §4's table records all twelve: the summary contradicting the table is exactly what this test is for", path)
	}
}

// TestDocsWindowsConsolePlanIsReleaseTimeOnly pins the plan's half of the D7
// ruling: the status line says the decisions are answered and the build is
// approved for a named release on a named branch, the pack is a release-time
// job, the accepted risk is written down, and the OWNER APPROVAL GATE records
// the approval and what it does *not* cover.
//
// The mutations that prove it: put "Not passed" back in the gate, change the
// approval's date, drop the release or the branch from the status line, and
// delete the accepted-risk sentence. Each fails here.
func TestDocsWindowsConsolePlanIsReleaseTimeOnly(t *testing.T) {
	const path = "docs/console-windows/implementation-plan.md"
	doc := readDoc(t, path)
	flat := flattenDoc(doc)

	for _, want := range []string{
		// The status line: the decisions are answered, and the build is approved
		// for a named release on a named branch. A status line that said only
		// "approved" would leave a reader unable to say what for, or where the
		// parts collect -- and the approval's whole content is those three facts.
		"the twelve decisions are answered",
		"approved to build on 2026-10-21",
		"0.11.0",
		"console-windows-wsl",
		// Answering was not approving, and the two dates are separate: the
		// answers are 2026-10-20 and the approval is 2026-10-21.
		"Answering a decision was not approving the build",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s no longer carries %q: the D7 ruling of 2026-10-20 is what the plan is re-cut around, the owner approved the build on 2026-10-21 for 0.11.0 on console-windows-wsl, and the plan has to say both", path, want)
		}
	}

	// The D7 ruling's own paragraph -- what it is, what it costs and how it is
	// mitigated -- read as that paragraph rather than from the whole file,
	// because "a packaging break shows at release time" appears once more in §5
	// and a file-wide substring would survive the sentence being deleted here.
	ruling := flattenDoc(windowsConsoleBlock(t, path, doc, "**The D7 ruling (2026-10-20)", "\n\n"))
	for _, want := range []string{
		// The pack's home and its one trigger.
		"the Windows pack runs at release time only",
		"never on a pull request",
		// The accepted risk, in the plan's own words.
		"a packaging break shows at release time",
		// And the mitigation, which is a step in the document a release is cut
		// from rather than a hope.
		"docs/release.md",
		"Before you tag",
	} {
		if !strings.Contains(ruling, want) {
			t.Errorf("%s's D7 ruling paragraph no longer carries %q: it is the paragraph that says what the ruling is, what it accepts and what mitigates it", path, want)
		}
	}

	// `[pack]` is the abbreviation the Part-B and Part-H tasks verify with, and
	// under the ruling it can only be a manual dispatch: a job that runs on a
	// tag cannot be run by a task.
	pack := windowsConsoleBlock(t, path, doc, "**`[pack]`**", "\n- **Tier**")
	if !strings.Contains(flattenDoc(pack), "manual dispatch") {
		t.Errorf("%s's `[pack]` abbreviation no longer says the staged job is run by hand (%q): the job runs at release time, so a manual dispatch is the only way a task can run it", path, flattenDoc(pack))
	}
	if !strings.Contains(flattenDoc(pack), "windows-latest") {
		t.Errorf("%s's `[pack]` abbreviation no longer names the runner (%q): the pack is a `windows-latest` job however it is started", path, flattenDoc(pack))
	}

	// Part H's row in the parts table: the part the ruling moved says so where
	// the parts are listed.
	partH := windowsConsoleBlock(t, path, doc, "| **H** — ", "\n")
	if !strings.Contains(partH, "release") {
		t.Errorf("%s's Part H row is %q: Part H is the part the D7 ruling moved, and its row has to say the job is a release-time one", path, strings.TrimSpace(partH))
	}

	// The gate, whole: it records the approval, with its date and its target, it
	// records every answer, and it still says what the approval does not cover --
	// because the one way this section can mislead a reader is by being read as
	// permission for the copy-in, the register row or website/data/install.yaml,
	// none of which the owner approved.
	gate := windowsConsoleBlock(t, path, doc, "## 5. OWNER APPROVAL GATE", "")
	gateFlat := flattenDoc(gate)
	if !strings.Contains(gateFlat, "Passed on 2026-10-21") {
		t.Errorf("%s's OWNER APPROVAL GATE no longer records the owner's approval of 2026-10-21: the plan is approved to build, and a gate still reading \"not passed\" would stop work the owner has authorised", path)
	}
	if strings.Contains(gateFlat, "Not passed") {
		t.Errorf("%s's OWNER APPROVAL GATE says it is not passed again: the owner approved the build on 2026-10-21, and this section is where a reader checks that", path)
	}
	if !strings.Contains(gateFlat, "still needs the owner") {
		t.Errorf("%s's OWNER APPROVAL GATE no longer says what the approval does *not* cover: copying the staged workflow in, the Check 119 row and website/data/install.yaml are the owner's, and an approval read as covering them is the mistake this section can cause", path)
	}
	for i := 1; i <= 12; i++ {
		if !strings.Contains(gateFlat, "D"+strconv.Itoa(i)) {
			t.Errorf("%s's OWNER APPROVAL GATE no longer names D%d: the gate is where a reader checks which decisions are answered and which are not", path, i)
		}
	}

	// The `[unit]` abbreviation and T-H1's difference list are claims about the
	// console's suite and about the file this one is derived from, and both were
	// wrong before this pass: the unit suite was given as 75 files, which is every
	// `.ts` file under `test/` (e2e and fixtures included) rather than what
	// `vitest run --dir test/unit` runs, and the staged job was called
	// `console-linux.yml` "with four differences" while it also carries a named
	// shell, a longer timeout, the two steps that are its second subject, and its
	// own artifact name and retention.
	unit := flattenDoc(windowsConsoleBlock(t, path, doc, "**`[unit]`**", "\n  - **`[types]`**"))
	for _, want := range []string{"57", "54", "test/unit"} {
		if !strings.Contains(unit, want) {
			t.Errorf("%s's `[unit]` abbreviation no longer carries %q: the suite is `vitest run --dir test/unit`, and a figure with the command beside it is the only kind this repository trusts -- Part A's two new files moved it to 57 and 54", path, want)
		}
	}
	if strings.Contains(unit, "75 files") {
		t.Errorf("%s's `[unit]` abbreviation is back to \"75 files\": 75 is every `.ts` file under `test/`, and a task that trusts it goes looking for twenty tests the suite it runs does not have", path)
	}
	th1 := flattenDoc(windowsConsoleBlock(t, path, doc, "**T-H1 — ", "\n**T-H2 — "))
	for _, want := range []string{
		"four differences D7(c) and the runner force",
		"defaults.run.shell: bash",
		"timeout-minutes: 45",
		"retention-days: 90",
		"run the unit suite",
	} {
		if !strings.Contains(th1, want) {
			t.Errorf("%s's T-H1 no longer carries %q: the staged file is not `console-linux.yml` with four differences, and the differences it carries besides those four are what make it a Windows job", path, want)
		}
	}
	// And the release document's note is this work's, not T-H3's to write from
	// scratch: the owner asked for the dry run to exist before the pack does.
	th3 := flattenDoc(windowsConsoleBlock(t, path, doc, "**T-H3 — ", "\n**T-H4 — "))
	if !strings.Contains(th3, "already written") {
		t.Errorf("%s's T-H3 no longer says the release document's note is already written: docs/release.md carries it today, and a task that rewrote it from scratch would undo the dated note it has", path)
	}
}

// TestDocsWindowsConsoleReadmeStatusRecordsTheApproval pins the folder's front
// door. The index's status line is the sentence a reader meets before any
// document, and the same brief that asked for the plan's gate to record the
// approval asked for this line to carry it too: a front door still saying the
// build is unapproved, or one that lost the release or the branch, sends the
// reader to the plan to check a claim the index has already got wrong.
//
// Read as its own paragraph, so the plan row in the table below cannot satisfy
// it: that row is a summary of the plan, and this is the folder's status.
//
// The mutations that prove it: take the approval's date off the line, change
// 0.11.0 to the 0.10.0 the earlier ruling named, and drop the branch. Each
// fails by naming what went missing.
func TestDocsWindowsConsoleReadmeStatusRecordsTheApproval(t *testing.T) {
	const path = "docs/console-windows/README.md"
	doc := readDoc(t, path)
	status := flattenDoc(windowsConsoleBlock(t, path, doc, "**Status:", "\n\n"))
	for _, want := range []string{
		"approved to build on 2026-10-21",
		"0.11.0",
		"console-windows-wsl",
	} {
		if !strings.Contains(status, want) {
			t.Errorf("%s's status line no longer carries %q: the owner approved the build on 2026-10-21 for 0.11.0 on console-windows-wsl, and the index is where a reader checks that before reading any document in this folder", path, want)
		}
	}
	if strings.Contains(status, "not approved") {
		t.Errorf("%s's status line says the build is not approved again: the owner passed the plan's OWNER APPROVAL GATE on 2026-10-21, and the index contradicting the plan is what this test is for", path)
	}
}

// TestWindowsConsoleWorkflowIsReleaseTimeOnly is the staged file's own test,
// and it is the one the ruling is about: the job runs on a strict vX.Y.Z tag
// and by hand, and on nothing else. There is no pull-request trigger of any
// kind, no path filter (a filter would be a second way not to run, on the one
// trigger the owner ruled out entirely), and no secret.
//
// It is read through consoleWorkflowText, so the same test passes before and
// after the owner's copy-in.
//
// The mutations that prove it: add `pull_request:` back to the `on:` block,
// change `tags:` to `branches: [main]`, and add `paths:` under
// `workflow_dispatch`. Each fails by naming the trigger.
func TestWindowsConsoleWorkflowIsReleaseTimeOnly(t *testing.T) {
	text, path := consoleWorkflowText(t, windowsConsoleWorkflowPath, appliedConsoleWindowsPath)
	wf, rawOn, rawJobs := parseConsoleWorkflow(t, path, text)

	// The triggers, exactly: the strict release tag and a manual dispatch. The
	// negative half -- no pull_request -- is the owner's ruling, and it is
	// asserted first because it is the claim an edit that looked like an
	// improvement would silently reverse.
	for name := range rawOn {
		switch name {
		case "push", "workflow_dispatch":
		default:
			t.Errorf("%s triggers on %q: the owner's ruling of 2026-10-20 is that the Windows pack runs at release time only -- a strict vX.Y.Z tag or a manual dispatch -- and never on a pull request, not even path-filtered", path, name)
		}
	}
	if len(rawOn) != 2 {
		t.Errorf("%s triggers on %v, want exactly the release tag and workflow_dispatch", path, rawOn)
	}
	if _, ok := rawOn["pull_request"]; ok {
		t.Errorf("%s carries a pull_request trigger: the owner's ruling is that the Windows pack never runs on a pull request, so this trigger is the ruling reversed", path)
	}
	push, ok := rawOn["push"].(map[string]any)
	if !ok {
		t.Fatalf("%s's push trigger is not a mapping (%v): the pack runs on a tag, not on a branch", path, rawOn["push"])
	}
	if branches, ok := push["branches"]; ok {
		t.Errorf("%s's push trigger names branches (%v): a push to main is not a release, and the pack runs at release time only", path, branches)
	}
	tags, _ := push["tags"].([]any)
	if len(tags) != 1 || tags[0] != windowsConsoleReleaseTag {
		t.Errorf("%s's push trigger tags = %v, want exactly [%q], the strict tag console-release.yml runs on", path, push["tags"], windowsConsoleReleaseTag)
	}
	if _, ok := rawOn["workflow_dispatch"]; !ok {
		t.Errorf("%s has no workflow_dispatch trigger: the pack has to be runnable by hand before a tag, which is the only dry run this design has", path)
	}
	if offences := triggerFilterOffences(rawOn); len(offences) > 0 {
		t.Errorf("%s: %s a path filter is a second way for the job not to run, on the one trigger the owner ruled out entirely", path, strings.Join(offences, "; "))
	}

	// One job, keyed and named console-windows.
	if len(wf.Jobs) != 1 {
		t.Fatalf("%s has jobs %v, want exactly one: it is the pack job, and a second one would be a second release-time job nobody asked for", path, jobKeys(rawJobs))
	}
	job, ok := wf.Jobs[windowsConsoleJobName]
	if !ok {
		t.Fatalf("%s has no %s job: it has %v", path, windowsConsoleJobName, jobKeys(rawJobs))
	}
	if job.Name != windowsConsoleJobName {
		t.Errorf("%s's job is named %q, want the static %q", path, job.Name, windowsConsoleJobName)
	}
	if job.RunsOn != "windows-latest" {
		t.Errorf("%s runs on %q, want windows-latest: the packer's Windows target is what this job exists to exercise", path, job.RunsOn)
	}
	if job.If != "" {
		t.Errorf("%s's job carries `if: %s`: a guarded release-time job is a pack that silently did not run", path, job.If)
	}

	// No secret, no environment, and no shape that could be one -- asked of the
	// raw job as well as of the file, because a job-level `environment:` is
	// exactly the edit that puts a pack next to release credentials.
	rawJob := renderedJob(t, rawJobs, path, windowsConsoleJobName)
	for _, forbidden := range []string{"secrets.", "environment:", "-----BEGIN", "pull_request_target", "workflow_run", "write-all"} {
		if strings.Contains(rawJob, forbidden) {
			t.Errorf("%s's %s job contains %q: it packs an unsigned artifact and holds nothing", path, windowsConsoleJobName, forbidden)
		}
	}

	// Permissions: declared, read-only, at the workflow and at the job.
	if len(wf.Permissions) != 1 || wf.Permissions["contents"] != "read" {
		t.Errorf("%s's workflow permissions = %v, want exactly contents: read", path, wf.Permissions)
	}
	if len(job.Permissions) != 1 || job.Permissions["contents"] != "read" {
		t.Errorf("%s's %s job permissions = %v, want exactly contents: read declared on the job", path, windowsConsoleJobName, job.Permissions)
	}

	// Every action pinned to a full commit with a version comment: Dependabot
	// does not watch docs/pending-workflows/, and this file is copied in by hand.
	uses := 0
	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(line, "uses:") {
			continue
		}
		uses++
		if !consoleUsesLine.MatchString(line) {
			t.Errorf("%s: %q is not a full 40-hex pin with a `# vX.Y.Z` comment", path, strings.TrimSpace(line))
		}
	}
	if uses == 0 {
		t.Fatalf("%s pins no action at all: the job checks the repository out and installs Node", path)
	}

	// The steps: the typecheck and the suite (this is the only place the
	// console's suite ever runs on Windows), Electron's own download, the build,
	// and the pack -- through the npm script rather than a second copy of its
	// command line, with no signing variable and no stamped version.
	runs := strings.Join(stepStrings(wf, windowsConsoleJobName), "\n")
	for _, want := range []string{
		"npm ci --ignore-scripts --no-audit --no-fund",
		"npm run typecheck",
		"npm run test:unit",
		"node node_modules/electron/install.js",
		"npm run build",
		"npm run package:win",
		"unset CSC_LINK CSC_NAME CSC_KEY_PASSWORD",
	} {
		if !strings.Contains(runs, want) {
			t.Errorf("%s never runs %q", path, want)
		}
	}
	for _, forbidden := range []string{"npx ", "npm install", "electron-builder", "extraMetadata", "--publish", "signtool", "CSC_IDENTITY"} {
		if strings.Contains(runs, forbidden) {
			t.Errorf("%s's steps contain %q: the packer is the lockfile's, through the npm script, nothing signs and nothing stamps a version", path, forbidden)
		}
	}

	// The artifact is the point of the job, and an empty upload is a failure.
	uploads := 0
	for _, step := range job.Steps {
		if !strings.Contains(step.Uses, "actions/upload-artifact@") {
			continue
		}
		uploads++
		if step.With["if-no-files-found"] != "error" {
			t.Errorf("%s's upload step takes if-no-files-found %q, want error: a pack that produced nothing and uploaded nothing, successfully, is the one outcome this job must not report", path, step.With["if-no-files-found"])
		}
		// The installer's directory and its extension, spelled without the
		// module-root directory's name: a Go file in this package that writes that
		// directory as a path is a boundary reference the allow-list test counts,
		// and this is a check about a workflow's upload path rather than a reason
		// to widen a one-entry allow-list.
		if !strings.HasSuffix(step.With["path"], "release/*.exe") {
			t.Errorf("%s's upload step uploads %q, which does not name the installer", path, step.With["path"])
		}
	}
	if uploads != 1 {
		t.Errorf("%s has %d upload-artifact steps, want 1: the installer is the point of the job", path, uploads)
	}

	// The header: the copy-in commands, the refusal to be copied in yet, the
	// three edits that have to land first, and the release-time-only sentence. A
	// reader who opens the file rather than the staging README is the likelier
	// one, and the file is what the owner copies.
	flat := flattenDoc(text)
	for _, want := range []string{
		"STAGED, NOT APPLIED",
		"cp docs/pending-workflows/console-windows.yml .github/workflows/console-windows.yml",
		"git rm docs/pending-workflows/console-windows.yml",
		"DO NOT COPY THIS IN YET",
		"release time only",
		"never on a pull request",
		"package:win",
		"win32-x64",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s's header no longer carries %q: the file is what the owner has open, and the staging directory is a proposal until they copy it in", path, want)
		}
	}
}

// TestWindowsConsoleStagingTellsTheOwnerWhatToCopyWhere is the two sentences
// outside docs/console-windows/ that the staging moves, held to the file they
// describe: the staging README's own section for the pack, and the site page
// that names it among the pending workflows. Both described a pull-request job
// before the D7 ruling, and both would be a reader told the wrong thing about a
// job that runs once per release.
//
// The count and the set of names are *not* pinned here: they belong to the
// staging directory's own guards, which check them against the directory in
// every state the owner's copy-ins pass through.
//
// The mutation that proves it: change the README section's "release time only"
// back to "every pull request". It fails here.
func TestWindowsConsoleStagingTellsTheOwnerWhatToCopyWhere(t *testing.T) {
	doc := readDoc(t, "docs/pending-workflows/README.md")
	section := flattenDoc(consoleReadmeSection(t, doc, "## `console-windows.yml`"))
	for _, want := range []string{
		"cp docs/pending-workflows/console-windows.yml .github/workflows/console-windows.yml",
		"git rm docs/pending-workflows/console-windows.yml",
		"release time only",
		"workflow_dispatch",
		"never on a pull request",
		"docs/release.md",
		"if-no-files-found",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("docs/pending-workflows/README.md's `console-windows.yml` section lacks %q: the owner is the one who copies the file in, and this is where they read what it is, when it runs, and what has to land first", want)
		}
	}

	// The site's page names it among the pending workflows and says when it
	// runs. website/test/readme.mjs checks the set and the count word; this is
	// the claim inside that sentence, which nothing else reads.
	site := flattenDoc(readDoc(t, "website/README.md"))
	for _, want := range []string{
		"`docs/pending-workflows/console-windows.yml`",
		"release time",
	} {
		if !strings.Contains(site, want) {
			t.Errorf("website/README.md no longer carries %q about the console's Windows pack: the page names every pending workflow, and a reader who follows the name needs to know it runs at release time rather than on their pull request", want)
		}
	}

	// And the two agree with the file rather than with each other: the trigger
	// set both sentences describe is read back out of the staged workflow.
	text, path := consoleWorkflowText(t, windowsConsoleWorkflowPath, appliedConsoleWindowsPath)
	_, rawOn, _ := parseConsoleWorkflow(t, path, text)
	if _, ok := rawOn["pull_request"]; ok {
		t.Fatalf("%s carries a pull_request trigger, and both sentences above say it never runs on one: the file is what has to move, and this test reads the sentences against it", path)
	}
	if _, ok := rawOn["workflow_dispatch"]; !ok {
		t.Errorf("%s has no workflow_dispatch trigger, and the staging README tells the owner to dry-run the pack by hand before a tag", path)
	}
}

// TestDocsWindowsConsoleReleaseDocsCarryTheDryRun is the release checklist's
// half of the ruling: with the pack moved to release time, the only way to find
// a packaging break before a tag is to run the job by hand, and the document a
// release is cut from has to say so, in the list it is read in order from.
//
// The mutations that prove it: delete the bullet, and move it out of the
// pre-tag checklist. Each fails here.
func TestDocsWindowsConsoleReleaseDocsCarryTheDryRun(t *testing.T) {
	const path = "docs/release.md"
	doc := readDoc(t, path)

	// The bullet lives in the pre-tag checklist: a dry run written anywhere else
	// is a step nobody reaches, because the list is what a release reads. The
	// block ends where the checklist ends -- at the paragraph that introduces
	// `scripts/release.sh` -- rather than at the next heading, because the
	// "Manual steps (fallback)" section below this one also says "Run workflow",
	// so a file-wide or heading-bounded read would be satisfied by that one.
	const checklistEnd = "\n`scripts/release.sh X.Y.Z` does the whole thing"
	checklist := flattenDoc(windowsConsoleBlock(t, path, doc, "**Before you tag", checklistEnd))
	if strings.Contains(checklist, "### Manual steps") {
		t.Fatalf("%s's \"Before you tag\" checklist no longer ends at %q, so this test is reading past the checklist into the sections under it", path, checklistEnd)
	}
	for _, want := range []string{
		"console-windows",
		"Run workflow",
		"release time only",
		"never on a pull request",
	} {
		if !strings.Contains(checklist, want) {
			t.Errorf("%s's \"Before you tag\" checklist no longer carries %q: the Windows pack runs at release time only, so a tag is where a break in it is found -- unless the owner dispatches it by hand first, which is the step this document owes them", path, want)
		}
	}

	// And the console bullet's own sentence: under the ruling the release's
	// assets are unchanged until the owner decides the installer joins
	// `console-vX.Y.Z`, so a sentence claiming a third asset would be false.
	if !strings.Contains(flattenDoc(doc), "the Windows installer is not an asset of `console-vX.Y.Z`") {
		t.Errorf("%s no longer says the Windows installer is not an asset of `console-vX.Y.Z`: D7 answers where and when the pack runs, and whether its artifact joins the console release is a separate decision nothing has taken", path)
	}
}

// TestDocsWindowsConsoleClaimsAboutOtherFilesAreTrue checks the claims these
// documents make *about other files in this repository* against those files.
// Every one of them was wrong in this work's first draft, and every one was
// wrong the same way: the claim was written from memory rather than read back.
//
//   - The index said the CLI track's `reconciliation.md` **§4 U13** hands the
//     console's own Windows question to this folder. U13 is in that document's
//     **§7 (UNVERIFIED)** and puts "anything about the desktop console on
//     Windows" *out of the CLI track's scope*, assigning it to
//     `docs/design/desktop-console.md` D10 -- so the folder's stated provenance
//     was false, and a reader following it would look for a hand-off that does
//     not exist.
//   - Three places cited `SECURITY.md` for a POSIX-only mode caveat it does not
//     contain: that file carries no Windows, POSIX, `chmod` or `0600`/`0700`
//     sentence at all, and the sibling document says the statement is still
//     **owed**.
//   - The index claimed to write out "everything outside this directory that
//     this work touches" while leaving `docs/release.md` out, and claimed there
//     was "no change to an existing test" while changing one.
//   - The staging README told the owner that the sentences its copy-in needs are
//     the ones the `## console-linux.yml` section already gives, and they are
//     not: that copy-in deletes the very file those sentences name.
//   - `docs/release.md` said the console *has* a Windows build, which nothing
//     has built, while two other documents still say Windows stays out.
//
// The mutations that prove it: put "§4 U13" back; put "`SECURITY.md` already
// says" back; drop `docs/release.md` from the surface list; put the
// "already describes" sentence back; and say "the console has a Windows build".
// Each fails here.
func TestDocsWindowsConsoleClaimsAboutOtherFilesAreTrue(t *testing.T) {
	const index = "docs/console-windows/README.md"
	idx := flattenDoc(readDoc(t, index))

	// 1. The provenance: where the console's Windows question is actually
	// settled, and the section of the sibling document it is written in.
	for _, want := range []string{
		"U13, in that document's §7",
		"`docs/design/desktop-console.md` D10 owns it",
	} {
		if !strings.Contains(idx, want) {
			t.Errorf("%s no longer carries %q: U13 is in docs/windows-cli/reconciliation.md's §7 and assigns the console's Windows question to docs/design/desktop-console.md D10, so a citation of it has to say that", index, want)
		}
	}
	if strings.Contains(idx, "§4 U13") {
		t.Errorf("%s cites \"§4 U13\" again: that section of the sibling document is the decisions, and U13 is in its §7 -- the citation is the whole provenance claim this folder has", index)
	}

	// 2. The surface list: the file it left out, and the sentence that claimed
	// nothing existing changed. Read as the list itself, because `docs/release.md`
	// and the test file are named elsewhere in this README too -- a file-wide
	// search would be satisfied by the "Related" section and by O7.
	surface := flattenDoc(windowsConsoleBlock(t, index, idx,
		"1. **`docs/pending-workflows/console-windows.yml`**", "**No production file changes**"))
	for _, want := range []string{
		"`docs/release.md`",
		"a change to an existing test file",
		"`internal/cli/docs_desktop_console_test.go`",
	} {
		if !strings.Contains(surface, want) {
			t.Errorf("%s's surface list no longer carries %q: the list is the claim, and docs/release.md and the marker change in an existing test file are both part of it", index, want)
		}
	}
	if strings.Contains(surface, "no change to an existing test") {
		t.Errorf("%s claims \"no change to an existing test\" again, and internal/cli/docs_desktop_console_test.go is changed: a surface list that is wrong about its own contents is worse than none", index)
	}

	// 3. SECURITY.md, in the index and in the reconciliation: the caveat is not
	// there, and the documents have to say so rather than cite it.
	for _, want := range []string{
		"carries no Windows, POSIX",
		"is **owed**",
	} {
		if !strings.Contains(idx, want) {
			t.Errorf("%s no longer carries %q about SECURITY.md: that file says nothing about Windows or POSIX modes, and windows-cli D12 leaves the statement owed", index, want)
		}
	}
	if strings.Contains(idx, "the mode guarantee is POSIX-only, and the credential boundary") {
		t.Errorf("%s cites SECURITY.md for a POSIX-only mode caveat again: the file contains no Windows, POSIX, `chmod` or `0600`/`0700` sentence at all", index)
	}
	recon := flattenDoc(readDoc(t, "docs/console-windows/reconciliation.md"))
	for _, want := range []string{
		"`SECURITY.md` says none of that today",
		"is **owed**",
	} {
		if !strings.Contains(recon, want) {
			t.Errorf("docs/console-windows/reconciliation.md no longer carries %q about SECURITY.md: F45's consequence is that the POSIX-only statement is owed, not written, and a citation of it would send the reader to a sentence that does not exist", want)
		}
	}
	for _, forbidden := range []string{"SECURITY.md` already says", "SECURITY.md`'s mode caveat", "SECURITY.md`'s existing answer"} {
		if strings.Contains(recon, forbidden) {
			t.Errorf("docs/console-windows/reconciliation.md says %q again: SECURITY.md carries no Windows sentence, and the sibling document's F8/D12 are where the POSIX-only limit is recorded and owed", forbidden)
		}
	}

	// 4. The staging README's own instruction: the sentences its copy-in needs
	// are the ones for the state it creates, and both orderings are given
	// because this file cannot fix the order the owner applies the three in.
	staging := flattenDoc(consoleReadmeSection(t, readDoc(t, "docs/pending-workflows/README.md"), "## `console-windows.yml`"))
	for _, want := range []string{
		"**One file is pending: `shared-tests.yml`**",
		"**Three files are pending: `shared-tests.yml`**",
		"the Linux pair has already been copied in",
	} {
		if !strings.Contains(staging, want) {
			t.Errorf("docs/pending-workflows/README.md's `console-windows.yml` section no longer carries %q: applying this file deletes a file the `console-linux.yml` section's sentences name, so this section has to give the sentences for the states its own copy-in creates", want)
		}
	}
	if strings.Contains(staging, "already describes") {
		t.Errorf("docs/pending-workflows/README.md's `console-windows.yml` section says its copy-in leaves the state the `console-linux.yml` section \"already describes\" again: those sentences name console-windows.yml, which this copy-in deletes, and omit console-linux.yml, which it leaves staged -- following them leaves website/test/readme.mjs red on main")
	}

	// 5. The release document: a plan, not a shipped build -- and the approved
	// plan's own status, which the release text carries because a reader who
	// wants to know whether a Windows installer is coming reads it there.
	release := flattenDoc(readDoc(t, "docs/release.md"))
	for _, want := range []string{
		"planned and staged, not shipped",
		"nothing on Windows has been built",
		"OWNER APPROVAL GATE was passed on 2026-10-21",
	} {
		if !strings.Contains(release, want) {
			t.Errorf("docs/release.md no longer carries %q: the Windows build is the plan's Part H work and nothing has been built, the plan is approved to build for 0.11.0 on console-windows-wsl, and the release document must say both rather than read as if 0.10.0 had a Windows build", want)
		}
	}
	if strings.Contains(release, "the console has a Windows build") {
		t.Errorf("docs/release.md says the console has a Windows build again: the plan is approved but unbuilt, the pack is red until its Parts B and C land, and docs/design/desktop-console.md and the console's own README still say Windows stays out")
	}

	// 6. And the figure the suite's size is given as, in the two other places it
	// is written: `vitest run --dir test/unit`, not the whole `test/` tree. (The
	// number itself cannot be counted from here: this package's one-entry
	// console boundary allow-list is what stops a Go file in it reading the
	// console tree, which is why the command travels beside the figure.)
	text, wfPath := consoleWorkflowText(t, windowsConsoleWorkflowPath, appliedConsoleWindowsPath)
	if !strings.Contains(text, "57 `.ts` files") || !strings.Contains(text, "54") {
		t.Errorf("%s no longer gives the unit suite's size as 57 `.ts` files, 54 of them `*.test.ts`: 75 is every `.ts` file under `test/`, e2e and fixtures included, and it is the figure a reader will otherwise carry", wfPath)
	}
	if !strings.Contains(recon, "57 `.ts` files") {
		t.Errorf("docs/console-windows/reconciliation.md's U3 no longer gives the unit suite's size as 57 `.ts` files: the census rule is that a figure travels with the command that returns it, and this is the figure U3 is about")
	}
}

// windowsConsoleBlock is the text from start to the first line after it that
// begins with stop, or to the end when stop is empty. It is how these tests
// read a claim in the section it is made in rather than anywhere in the file --
// a phrase that moved to another section is a claim that moved, and a phrase
// found anywhere is a phrase that can be satisfied by the wrong one.
func windowsConsoleBlock(t *testing.T, path, doc, start, stop string) string {
	t.Helper()
	i := strings.Index(doc, start)
	if i < 0 {
		t.Fatalf("%s has no %q: these tests read the document, not a memory of it", path, start)
	}
	rest := doc[i+len(start):]
	if stop == "" {
		return rest
	}
	if j := strings.Index(rest, stop); j >= 0 {
		return rest[:j]
	}
	return rest
}
