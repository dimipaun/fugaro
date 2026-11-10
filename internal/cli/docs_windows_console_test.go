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
//     build itself on 2026-10-22 for 0.110.0, its pull requests targeting main. A document
//     still saying "none is answered" would describe a plan nobody may start; a
//     gate that had quietly gone back to "not passed" would stop work the owner
//     has authorised; and a status line that lost the release would leave the
//     parts with no target.
//   - **The staged job runs at release time and nowhere else.** The ruling is
//     negative -- never on a pull request, not even path-filtered -- and a
//     trigger block is exactly what an edit adds back without noticing.
//   - **The two sentences outside the folder agree with the file.** A staging
//     README that described a pull-request job, and a site page that did too,
//     would each be a reader told the wrong thing about a job that runs once
//     per release.

import (
	"slices"
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
	// windowsConsoleFollowUpEdits is how many follow-up edits the pack's copy-in
	// commit needs, as the word the section writes: the site's last section, this
	// README's own opening line, and the section's own status line, which
	// TestWindowsConsoleStagingTellsTheOwnerWhatToCopyWhere reads against the
	// tree in both directions -- so a section that says one fewer sends the owner
	// away with a line that turns the suite red in their commit.
	windowsConsoleFollowUpEdits = "Three"
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
		t.Errorf("%s no longer says that answering a decision is not approving the build: the answers are answers, and the plan's OWNER APPROVAL GATE is a separate, dated thing -- passed on 2026-10-22", path)
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
		"OWNER APPROVAL GATE was passed on 2026-10-22",
		"0.110.0",
	} {
		if !strings.Contains(status, want) {
			t.Errorf("%s's status line no longer carries %q: §4 answers all twelve decisions, the plan's gate was passed on 2026-10-22 for 0.110.0, and the summary has to say both -- an answer is not an approval, and the approval is a separate, dated thing", path, want)
		}
	}
	if strings.Contains(status, "no decision in §4 is answered") {
		t.Errorf("%s's status line says no decision in §4 is answered, and §4's table records all twelve: the summary contradicting the table is exactly what this test is for", path)
	}
}

// TestDocsWindowsConsolePlanIsReleaseTimeOnly pins the plan's half of the D7
// ruling: the status line says the decisions are answered and the build is
// approved for a named release, the pack is a release-time
// job, the accepted risk is written down, and the OWNER APPROVAL GATE records
// the approval and what it does *not* cover.
//
// The mutations that prove it: put "Not passed" back in the gate, change the
// approval's date, drop the release from the status line, and
// delete the accepted-risk sentence. Each fails here.
func TestDocsWindowsConsolePlanIsReleaseTimeOnly(t *testing.T) {
	const path = "docs/console-windows/implementation-plan.md"
	doc := readDoc(t, path)
	flat := flattenDoc(doc)

	for _, want := range []string{
		// The status line: the decisions are answered, and the build is approved
		// for a named release. A status line that said only "approved" would
		// leave a reader unable to say what for.
		"the twelve decisions are answered",
		"approved to build on 2026-10-22",
		"0.110.0",
		// Answering was not approving, and the two dates are separate: the
		// answers are 2026-10-20 and the approval is 2026-10-22.
		"Answering a decision was not approving the build",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s no longer carries %q: the D7 ruling of 2026-10-20 is what the plan is re-cut around, the owner approved the build on 2026-10-22 for 0.110.0, and the plan has to say both", path, want)
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
	if !strings.Contains(gateFlat, "Passed on 2026-10-22") {
		t.Errorf("%s's OWNER APPROVAL GATE no longer records the owner's approval of 2026-10-22: the plan is approved to build, and a gate still reading \"not passed\" would stop work the owner has authorised", path)
	}
	if strings.Contains(gateFlat, "Not passed") {
		t.Errorf("%s's OWNER APPROVAL GATE says it is not passed again: the owner approved the build on 2026-10-22, and this section is where a reader checks that", path)
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

// TestDocsWindowsConsoleReadmeRecordsTheApproval pins the folder's front door.
// The index's status line is the sentence a reader meets before any document,
// and the same brief that asked for the plan's gate to record the approval asked
// for this line to carry it too: a front door still saying the build is
// unapproved, or one that lost the release, sends the reader to
// the plan to check a claim the index has already got wrong.
//
// The status line is read as its own paragraph, so the plan row in the table
// below cannot satisfy it: that row is a summary of the plan, and this is the
// folder's status. **The rest of the index is read whole**, because the same
// claim is made twice in a document this size and the second one is the one a
// reader arrives at from the plan: a review found line 120 still saying "the
// plan is not approved" under a status line that records the approval, which a
// status-paragraph-only check cannot see.
//
// The mutations that prove it: take the approval's date off the status line,
// change 0.110.0 to the 0.10.0 the earlier ruling named, and put
// the present-tense "the plan is not approved" back anywhere in the file. Each
// fails by naming what went missing or what came back.
func TestDocsWindowsConsoleReadmeRecordsTheApproval(t *testing.T) {
	const path = "docs/console-windows/README.md"
	doc := readDoc(t, path)
	status := flattenDoc(windowsConsoleBlock(t, path, doc, "**Status:", "\n\n"))
	for _, want := range []string{
		"approved to build on 2026-10-22",
		"0.110.0",
	} {
		if !strings.Contains(status, want) {
			t.Errorf("%s's status line no longer carries %q: the owner approved the build on 2026-10-22 for 0.110.0, and the index is where a reader checks that before reading any document in this folder", path, want)
		}
	}
	if strings.Contains(status, "not approved") {
		t.Errorf("%s's status line says the build is not approved again: the owner passed the plan's OWNER APPROVAL GATE on 2026-10-22, and the index contradicting the plan is what this test is for", path)
	}

	// The whole index, not only the status paragraph: every sentence in it that
	// describes this folder's plan is a claim about the same approval, and the
	// present tense is what makes one false. The phrases are the exact ones a
	// document reverts to -- "not approved", "no task is approved", "still not
	// passed" -- rather than the bare word "approved", so a sentence about what
	// the approval does *not* cover stays legal.
	whole := flattenDoc(doc)
	for _, forbidden := range []string{
		"the plan is not approved",
		"no task is approved",
		"GATE is not passed",
		"is still not passed",
	} {
		if strings.Contains(whole, forbidden) {
			t.Errorf("%s says %q: the owner approved the build on 2026-10-22 for 0.110.0, and a document that says the plan is unapproved somewhere below a status line recording the approval is a claim that is false on main", path, forbidden)
		}
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
		// to widen a closed allow-list.
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

// TestWindowsConsolePackIsNotInTheReleaseGate is D8(a)'s guard: the staged
// Windows job's check name is not a name the release gate requires, and the
// file's own header says why. The structural reason is the gate's derivation --
// it reads console-tests.yml's **single** job -- and D7(c)'s release-time-only
// trigger is stated as a property of this job, not as a difference from that
// file's triggers: console-tests.yml is dispatched by hand today, so its
// trigger is not what keeps the Windows pack out of it, and a sentence saying
// otherwise would be false in the tree it ships in.
//
// The gate's list itself is TestReleaseGateRequiresTheConsoleCheck's subject,
// and it is re-read here rather than restated: the console's names are derived
// from the applied workflow by that test's own derivation, so a tree in which
// it would fail fails here too, by name.
//
// The mutations that prove it: add `console-windows` to scripts/release-gate.sh's
// `names=(...)` array (and to the `#   ` block above it) and both list cases
// fail; delete the header's "NOT in the gate" sentence, or its
// "never on a pull request", and the header case fails by naming the sentence;
// put the false "console-tests.yml runs on every pull request" reason back and
// the forbidden-phrase case fails by naming it.
func TestWindowsConsolePackIsNotInTheReleaseGate(t *testing.T) {
	text, path := consoleWorkflowText(t, windowsConsoleWorkflowPath, appliedConsoleWindowsPath)

	// The gate, read the way the test that owns it reads it: the `names=(...)`
	// array and the `#   ` comment block above it, which that script's own
	// comment asks a reader to keep in step with it.
	_, required, commented := readReleaseGate(t)
	for _, c := range []struct {
		what  string
		names []string
	}{
		{"the names=(...) array", required},
		{"the `#   ` comment block above it", commented},
	} {
		if slices.Contains(c.names, windowsConsoleJobName) {
			t.Errorf("%s's %s carries %q: the owner's answer to D8 is that the Windows pack does not gate a release -- a red Windows pack is information rather than a blocked release -- and the leg would have to land with a widened derivation, changing which checks gate every tag in this repository (docs/console-windows/reconciliation.md D8(a), finding F41)", releaseGatePath, c.what, windowsConsoleJobName)
		}
	}

	// And the tree is in the state TestReleaseGateRequiresTheConsoleCheck
	// requires: the console's names still derive from the applied workflow, and
	// none of them is this job's. A derivation that refused would fatal inside
	// consoleGateCheckNames, which is that test's first half failing here by name.
	for _, name := range consoleGateCheckNames(t) {
		if strings.Contains(name, windowsConsoleJobName) {
			t.Errorf("%s reports the check %q: this test reads the console's names out of the workflow the release gate is derived from, and the Windows pack is a different workflow's job -- a Windows leg there is the gate change D8(a) refuses", appliedConsoleTestsPath, name)
		}
	}

	// The derivation itself cannot read this workflow, which is the mechanical
	// half of "it cannot join the gate": a job the derivation can read is a job a
	// later edit could add to the names array in one line.
	if names, err := deriveConsoleCheckNames(text); err == nil {
		t.Errorf("%s: deriveConsoleCheckNames read the staged Windows workflow and derived %v. The derivation is written for console-tests.yml's shape -- one job with a matrix -- and it refuses anything else so a second job cannot join the gate unnoticed; a Windows job it reads is that refusal lost", path, names)
	}

	// The two rulings, in the header a reader opens rather than in the staging
	// README they may not. The gate's derivation is the structural reason, and
	// D7(c)'s trigger is this job's own property -- stated as such, because the
	// two files' triggers do not differ: console-tests.yml's only one is
	// `workflow_dispatch` since the owner's ruling of 2026-11-07, so a sentence
	// claiming it runs on every pull request would be a reason that does not hold
	// in the tree this file ships in.
	header := flattenDoc(windowsConsoleBlock(t, path, text, "# WHY THIS IS ITS OWN WORKFLOW", "\n# WHAT THIS FILE IS"))
	for _, want := range []string{
		"NOT in the gate",
		"refuses a workflow with more than one job",
		"names=(...)",
		"never on a pull request",
		"workflow_dispatch` today",
	} {
		if !strings.Contains(header, want) {
			t.Errorf("%s's own-workflow section no longer carries %q: the gate's list is derived from console-tests.yml's single job, this job is never on a pull request, and console-tests.yml is dispatched by hand today -- a reason that treats it as a pull-request workflow is false (D8(a), D7(c))", path, want)
		}
	}

	// And the staging README says the same two things, where the owner reads what
	// they are about to copy in.
	section := flattenDoc(consoleReadmeSection(t, readDoc(t, "docs/pending-workflows/README.md"), "## `console-windows.yml`"))
	for _, want := range []string{
		"NOT in the gate",
		"more than one job",
		"never on a pull request",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("docs/pending-workflows/README.md's `console-windows.yml` section no longer carries %q: the owner is the one who copies the file in, and the section is where they read that the pack does not gate a release and does not run on a pull request", want)
		}
	}

	// And neither text may reach for the reason that is no longer true:
	// console-tests.yml stopped running on pull requests in the owner's ruling of
	// 2026-11-07 (e5d6ba7c), so "it runs on every pull request" is exactly the
	// sort of claim this work would otherwise record as the owner's reasoning --
	// a reader would decide on it, and no pinned string above would catch it.
	// (The same sentence is still in console-tests.yml's own header, which an
	// agent run may not edit: the file under .github/workflows/ would have to
	// move with it, and this run cannot push one.)
	for _, c := range []struct{ what, text string }{
		{"docs/pending-workflows/console-windows.yml's own-workflow section", header},
		{"docs/pending-workflows/README.md's `console-windows.yml` section", section},
	} {
		for _, forbidden := range []string{"runs on every pull request", "runs on every one", "on every pull request from any branch"} {
			if strings.Contains(c.text, forbidden) {
				t.Errorf("%s says %q of a workflow: console-tests.yml's only trigger is workflow_dispatch since the owner's ruling of 2026-11-07, so this is a reason that does not hold in the tree it ships in", c.what, forbidden)
			}
		}
	}
}

// TestWindowsConsoleStagingTellsTheOwnerWhatToCopyWhere is what the staging
// moves outside docs/console-windows/, held to the file it describes: the two
// sentences -- the staging README's own section for the pack, and the site page
// that names it, among the pending workflows while the staged copy is here and
// as the file that runs once the copy-in has removed it -- and the section's
// status line.
// Those two both described a pull-request job before the D7 ruling, and both
// would be a reader told the wrong thing about a job that runs once per release;
// the status line is the half of "this file is still staged" that the copy-in
// edits, so it has to say the truth of the tree that commit leaves.
//
// The count and the set of names are *not* pinned here: they belong to the
// staging directory's own guards, which check them against the directory in
// every state the owner's copy-ins pass through. The status line is pinned here
// for the opposite reason -- the state its copy-in creates is one no guard that
// reads the directory can see.
//
// The mutations that prove it: change the README section's "release time only"
// back to "every pull request", and flip its status line to `applied, not
// pending` while the staged copy is still there. Each fails here.
func TestWindowsConsoleStagingTellsTheOwnerWhatToCopyWhere(t *testing.T) {
	doc := readDoc(t, "docs/pending-workflows/README.md")
	section := flattenDoc(consoleReadmeSection(t, doc, consoleWindowsReadmeHeading))
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

	// The section states how many follow-up edits its commit needs, and that
	// count is the list it introduces: the status line read below is one of
	// them, so a section that says one fewer is an owner told to leave it out.
	// The mobile-release section is held to its own count the same way, through
	// the regex that reads the word in either copy-in.
	if edits := mobileReleaseFollowUpEditsRE.FindAllStringSubmatch(section, -1); len(edits) != 1 || edits[0][1] != windowsConsoleFollowUpEdits {
		t.Errorf("docs/pending-workflows/README.md's `console-windows.yml` section states how many follow-up edits its copy-in needs %d time(s) (%v), and it is %s: the count and the list it introduces are one claim, and this copy-in needs the site's last section, this README's opening line and the status line below", len(edits), edits, windowsConsoleFollowUpEdits)
	}

	// The status line is the machine-readable half of "this file is still
	// pending": it is what the copy-in edits, and the owner who has just run
	// that commit has to be able to see from this document whether they
	// finished. So it is read against the tree in both directions rather than
	// asserted as one fixed sentence -- the rule the shared-tests section's own
	// line and the mobile-release section's follow -- because the copy-in
	// removes the staged copy, and a section that kept saying "pending"
	// afterwards would be sending the reader to a file that is not there. This
	// read is also the only one that reaches that state:
	// TestDocsPendingWorkflowsReadmeDescribesEveryStagedFile iterates what the
	// directory still holds, so the file leaves its set the moment it is copied
	// in.
	for _, problem := range consoleWindowsStatusProblems(consoleReadmeSection(t, doc, consoleWindowsReadmeHeading), docExists(t, windowsConsoleWorkflowPath)) {
		t.Errorf("docs/pending-workflows/README.md's `console-windows.yml` section %s", problem)
	}

	// The site's page names it and says when it runs -- among the pending
	// workflows while the staged copy is here, and as the file that runs once
	// the copy-in has removed it, because the page's list is held to the
	// directory (website/test/readme.mjs, and
	// TestConsoleLinuxStagingTellsTheOwnerWhatToCopyWhere for the other
	// direction). This is the claim inside that sentence, which nothing else
	// reads.
	site := flattenDoc(readDoc(t, "website/README.md"))
	named := "`" + windowsConsoleWorkflowPath + "`"
	if !docExists(t, windowsConsoleWorkflowPath) {
		named = "`" + appliedConsoleWindowsPath + "`"
	}
	for _, want := range []string{
		named,
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

// consoleWindowsReadmeHeading is the section of docs/pending-workflows/README.md
// that describes the Windows pack: the file's own document, which the copy-in
// section, the console's README and docs/console-windows/ all point at.
const consoleWindowsReadmeHeading = "## `console-windows.yml`"

// consoleWindowsStatusProblems reads that section's status line against one
// state of the tree, and reports every way it can disagree with it: an empty
// slice when the line is the one that state needs.
//
// A file is pending while its staged copy exists -- the half-done copy-in
// included, where both copies are present and the staged one is still the
// reviewed text -- and applied once the copy-in has removed it. So the line is
// edited in the same commit as the `cp`, and a section that kept saying "staged
// and pending" afterwards would be describing a file that is not there.
//
// Both directions are read from one function because this tree can only be in
// the first: the state the line is *for* is the one the copy-in creates, and no
// reader that iterates what the directory holds can see it -- the file leaves
// that set as it is copied in. That is why the state is a parameter rather than
// a `docExists` call, and why TestWindowsConsoleStagingStateRulesSeeEveryState
// pins both of them, including the one this repository cannot be in. It is the
// same rule TestSharedTestsWorkflowStagingStateIsWhatTheReadmeSays applies to
// `shared-tests.yml`'s own line, and the mobile-release section's line follows.
//
// The section's **opening** line is what is read, not the section: the copy-in
// procedure prints the line it writes, so a search over the whole section would
// be satisfied by that quote and stay green with the live line saying the
// opposite.
func consoleWindowsStatusProblems(section string, staged bool) []string {
	body := strings.TrimSpace(strings.TrimPrefix(section, consoleWindowsReadmeHeading))
	stagedCopy, want := "is gone", "**Status: `console-windows.yml` is applied, not pending.**"
	if staged {
		stagedCopy, want = "is still there", "**Status: `console-windows.yml` is staged and pending.**"
	}
	if strings.HasPrefix(body, want) {
		return nil
	}
	return []string{"does not open with " + want + " while the staged copy " + stagedCopy + ": that line is what the copy-in edits, and it has to be the section's opening claim rather than a sentence quoted further down"}
}

// TestWindowsConsoleStagingStateRulesSeeEveryState pins that read against both
// states of the tree and both mistakes, including the state this repository
// cannot be in: the copy-in, where the staged copy is gone and the line has to
// have moved with it. Each case asserts the problems *and* that a correct
// section produces none, so a rule that refused everything would fail here
// rather than looking strict.
func TestWindowsConsoleStagingStateRulesSeeEveryState(t *testing.T) {
	const (
		stagedLine  = "**Status: `console-windows.yml` is staged and pending.**"
		appliedLine = "**Status: `console-windows.yml` is applied, not pending.**"
	)
	section := func(line string) string {
		return consoleWindowsReadmeHeading + "\n\n" + line + "\n\nThe rest of the section.\n"
	}
	for _, tc := range []struct {
		name   string
		staged bool
		line   string
		want   string // the line this state needs, and "" when the section is right
	}{
		{"staged and pending, the state this branch is in", true, stagedLine, ""},
		{"copied in and the line moved with it", false, appliedLine, ""},
		{"copied in, but the section still says staged", false, stagedLine, appliedLine},
		{"still staged, but the line already says applied", true, appliedLine, stagedLine},
		{"no status line at all", true, "Nothing has been copied in yet.", stagedLine},
	} {
		t.Run(tc.name, func(t *testing.T) {
			problems := consoleWindowsStatusProblems(section(tc.line), tc.staged)
			if tc.want == "" {
				if len(problems) != 0 {
					t.Fatalf("problems = %q, want none: this section is the one that state needs", problems)
				}
				return
			}
			if len(problems) != 1 {
				t.Fatalf("problems = %q, want exactly one", problems)
			}
			if !strings.Contains(problems[0], tc.want) {
				t.Errorf("the problem does not name %q, the line the tree needs: an owner who is told the line is wrong and not which line to write has to guess, and the two spellings differ by three words in the middle of a long sentence; got %q", tc.want, problems[0])
			}
		})
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
//     has built, while two other documents still said Windows stays out.
//   - Its own O7 item, and the reconciliation's, went on saying the design's
//     and the console's README's notes were outstanding after they landed.
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
		// One more than the console's own count in each: the credentials
		// service's staged images.yml (Task C1.14) is pending beside them.
		"**Two files are pending: `shared-tests.yml`**",
		"**Four files are pending: `shared-tests.yml`**",
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
		"OWNER APPROVAL GATE was passed on 2026-10-22",
		"target release 0.110.0",
	} {
		if !strings.Contains(release, want) {
			t.Errorf("docs/release.md no longer carries %q: the Windows build is the plan's Part H work and nothing has been built, the plan is approved to build for 0.110.0, and the release document must say both rather than read as if 0.10.0 had a Windows build", want)
		}
	}
	if strings.Contains(release, "the console has a Windows build") {
		t.Errorf("docs/release.md says the console has a Windows build again: the plan is approved but unbuilt, no host has carried a `--win` pack to an artifact, and docs/design/desktop-console.md and the console's own README carry dated notes that say the build half moved and the signing half did not")
	}

	// 6. And the figure the suite's size is given as, in the two other places it
	// is written: `vitest run --dir test/unit`, not the whole `test/` tree. (The
	// number itself cannot be counted from here: this package's closed
	// console boundary allow-list is what stops a Go file in it reading the
	// console tree, which is why the command travels beside the figure.)
	text, wfPath := consoleWorkflowText(t, windowsConsoleWorkflowPath, appliedConsoleWindowsPath)
	if !strings.Contains(text, "57 `.ts` files") || !strings.Contains(text, "54") {
		t.Errorf("%s no longer gives the unit suite's size as 57 `.ts` files, 54 of them `*.test.ts`: 75 is every `.ts` file under `test/`, e2e and fixtures included, and it is the figure a reader will otherwise carry", wfPath)
	}
	if !strings.Contains(recon, "57 `.ts` files") {
		t.Errorf("docs/console-windows/reconciliation.md's U3 no longer gives the unit suite's size as 57 `.ts` files: the census rule is that a figure travels with the command that returns it, and this is the figure U3 is about")
	}

	// 7. And the two planning records this landing falsifies. This folder's
	// README carries O7 as the item that owes the other documents their notes,
	// and the reconciliation carries it as a finding, and both said in the
	// present tense that those notes were still outstanding. Landing them turns
	// that into a false claim about files outside this folder, which is what
	// this test is for: docs/release.md's twin clause was deleted in this very
	// change for the identical reason, and a reader who arrives at O7 from the
	// plan has to be able to trust it as much as that paragraph. There is no
	// later task that would notice: O7 is the last item of §5 and Part H's
	// other tasks do not read this folder.
	//
	// The mutations that prove it: put "are still untouched and still say
	// Windows stays out" back in the README's item, and take "Satisfied
	// 2026-11-10" off the reconciliation's.
	o7 := flattenDoc(windowsConsoleBlock(t, index, idx, "- **O7 —", "## Related"))
	if !strings.Contains(o7, "carry their notes as of 2026-11-10") {
		t.Errorf("%s's O7 item no longer carries %q: Part H (T-H3) landed the notes it owes on 2026-11-10, and an item still reading as outstanding contradicts the design, the console's own README and docs/release.md at once", index, "carry their notes as of 2026-11-10")
	}
	if strings.Contains(o7, "still untouched and still say Windows stays out") {
		t.Errorf("%s's O7 item says the design and the console's README \"are still untouched and still say Windows stays out\" again: both carry their dated notes as of 2026-11-10, which is what %q records", index, "carry their notes as of 2026-11-10")
	}
	reconO7 := flattenDoc(windowsConsoleBlock(t, "docs/console-windows/reconciliation.md", recon, "- **O7 —", "## 6. UNVERIFIED"))
	if !strings.Contains(reconO7, "Satisfied 2026-11-10") {
		t.Errorf("docs/console-windows/reconciliation.md's O7 finding is not marked satisfied: the three documents it names all carry the dated note as of 2026-11-10, and a finding left open in the document the plan is written from sends the next reader to write them a second time")
	}
}

// TestDocsWindowsConsoleNotesSayWhichHalfMoved pins the dated notes this work
// owes outside docs/console-windows/.
//
// The sentence "Windows stays out" is one claim this repository makes in four
// documents (F43: this design, docs/release.md, the console's own README and
// docs/windows-cli/wsl.md), and inside the design it is written four times.
// The owner's ruling of 2026-10-20 moves half of it: the console's Windows
// *build* is in by that ruling -- the console drives the *Linux* `fugaro` inside
// a WSL distribution, and the pack is staged as a release-time job -- while the
// *signing* answer is untouched, because there is still no Authenticode identity
// and none is planned. Each place keeps its own sentence and gains a dated note
// that says which half moved: a rewrite would claim more than the ruling does,
// and a note is the thing a reader can check.
//
// The design's four are read here, each as the block it is made in -- section
// 4.2's list, section 7.6's rule 3, the owner-run table's Windows-signing row
// and D10's answer cell -- and so are docs/release.md's paragraph and
// docs/windows-cli/wsl.md's paragraph and row. The console's own README's
// section is the one note not read here: it is read in
// docs_desktop_console_test.go, which is the file this package's console
// boundary allow-list admits to a path under the console tree -- the reason is
// written there rather than here, because a Go file in this package may not
// spell such a path (docs_console_boundary_test.go, Task 0.3 a3).
//
// docs/windows-cli/wsl.md's pair is the same ruling read the other way round: its
// console row says the console is out of scope because it needs a Windows CLI
// first, and the console's own route is what removes that need for the console,
// while the CLI track's F40 stays true of the CLI (O1, F43) -- so both the
// paragraph above that table and the row itself are read here too.
//
// The mutations that prove it: delete one note, move one out of the block it
// belongs to, and put the release document's "still carry the older sentence"
// clause back. Each fails here.
func TestDocsWindowsConsoleNotesSayWhichHalfMoved(t *testing.T) {
	const design = "docs/design/desktop-console.md"
	designDoc := readDoc(t, design)

	// Every note says the same three things, because "which half moved" is only
	// checkable if the note names both halves and the identity neither one has:
	// the date, the build half with the pack's own staged path, the signing half,
	// and the Authenticode identity that does not exist.
	note := []string{
		windowsConsoleNoteDate,
		"the *build* half",
		"the signing half",
		"Authenticode",
		windowsConsoleWorkflowPath,
	}
	for _, tc := range []struct{ what, start, stop string }{
		{"section 4.2's Windows bullet", "- **Windows as a *packaged* deliverable**", "\n- **The backing-repository history view**"},
		{"section 7.6's rule 3", "3. **Windows is untouched.**", "\n4. **What does not come with it.**"},
		{"the owner-run table's Windows-signing row", "| Windows signing |", "\n"},
		{"D10's answer cell", "| **D10** |", "\n"},
	} {
		block := flattenDoc(windowsConsoleBlock(t, design, designDoc, tc.start, tc.stop))
		for _, want := range note {
			if !strings.Contains(block, want) {
				t.Errorf("%s's %s carries no dated note (%q is missing): the owner's ruling of 2026-10-20 moves half of \"Windows stays out\" -- the build is in, the signing answer is not -- and each of the four places has to say which half, in its own block, rather than being rewritten", design, tc.what, want)
			}
		}
	}

	// docs/windows-cli/wsl.md: the paragraph that sends the console's own
	// question to the design's D10, and the table row that answers it "out of
	// scope". Both halves are read there, bounded by the table under them so a
	// note in the wrong one of the two cannot satisfy both.
	const wsl = "docs/windows-cli/wsl.md"
	wslDoc := readDoc(t, wsl)
	notSubstitute := flattenDoc(windowsConsoleBlock(t, wsl, wslDoc, "What WSL is *not* is a substitute for the port", "\n## Install it, inside WSL"))
	for _, want := range []string{windowsConsoleNoteDate, "reaches", "wsl.exe", "unsigned"} {
		if !strings.Contains(notSubstitute, want) {
			t.Errorf("%s's paragraph about what a Windows CLI would still buy no longer carries %q: the console reaches a WSL-only installation itself, and the sentence that says it cannot is the one this note is for", wsl, want)
		}
	}
	row := flattenDoc(windowsConsoleBlock(t, wsl, wslDoc, "| The desktop console |", "\n"))
	for _, want := range []string{windowsConsoleNoteDate, "Windows CLI", "no longer", "wsl.exe", "unsigned"} {
		if !strings.Contains(row, want) {
			t.Errorf("%s's desktop-console table row no longer carries %q: the row's \"it needs a Windows CLI first\" is half reversed for the console, and the note has to say which half -- the CLI track's F40 stays true of the CLI", wsl, want)
		}
	}

	// And the release document, which is where a reader arrives from: it says in
	// the console paragraph that these places carry the older sentence and that
	// theirs land with Part H. That was true while they did; after this change it
	// is the notes rather than the older sentences that a reader finds there.
	const release = "docs/release.md"
	releaseDoc := readDoc(t, release)
	console := flattenDoc(windowsConsoleBlock(t, release, releaseDoc, "- **The console is its own release, `console-vX.Y.Z`, never an asset of the CLI's**", "\n- **Dry-run the Windows pack before the tag, by hand.**"))
	for _, want := range []string{windowsConsoleNoteDate, "saying which half"} {
		if !strings.Contains(console, want) {
			t.Errorf("%s's console paragraph no longer carries %q: it is the paragraph that says which half of \"Windows stays out\" moved, and after Part H's notes land it is what a reader is sent to", release, want)
		}
	}
	for _, gone := range []string{"still carry the older sentence", "its pack is red until"} {
		if strings.Contains(console, gone) {
			t.Errorf("%s's console paragraph still says %q: the four places carry their dated notes, and the Parts B and C edits the pack was said to be waiting on have landed -- what has not happened is a run", release, gone)
		}
	}
}

// TestDocsPendingWorkflowsReadmeDescribesEveryStagedFile is T-H4's guard, and it
// is the staging convention's own rather than this folder's: every workflow
// still staged under docs/pending-workflows/ is described by a section of that
// directory's README, by name.
//
// The directory is where a workflow change waits for the owner -- no Fugaro run
// may write under .github/workflows/, because GitHub refuses the push -- and
// that README is the only place the person who copies a file in reads what it
// is, which commands copy it in, and whether it is still pending at all. A
// `.yml` staged there with no section of its own is a file whose copy-in nobody
// can find. So the guard reads three things per staged file -- the heading that
// names it, the status line that states its state, and the `cp` line that is its
// copy-in -- and each is derived from the directory rather than listed, so it
// holds for every state the owner's copy-ins pass through.
//
// The status line is read at the section's **opening**, and the section is not
// searched: a section quotes its own line the moment an owner is told which line
// to write -- the console-windows section names both spellings of its own a few
// paragraphs below the live one -- so `Contains` over the section is satisfied by
// that quotation and stays green with the live line saying the opposite. The
// first non-blank line after the heading is where the convention puts it, and
// both files staged here today open with it (`## shared-tests.yml` above states
// the rule; `## mobile-release.yml`'s guard reads its own line the same way).
//
// **What it cannot read is the state the copy-in creates**, and that gap is
// filled rather than described: the file leaves this guard's set the moment it
// is copied in, so a section left saying "staged and pending" over a staged copy
// that is gone is invisible here, and
// TestWindowsConsoleStagingTellsTheOwnerWhatToCopyWhere reads that line against
// the tree in both directions instead. What the guard does read is the other
// mistake -- a line flipped to `applied, not pending` while the staged copy is
// still there -- so the two together make the line load-bearing in every state
// the owner can leave the tree in, which is what the convention asks of it
// (the `## shared-tests.yml` section above states the rule; the mobile-release
// section's own guard applies it to its line).
//
// The mutations that prove it: delete the heading that names
// `console-windows.yml`, change its status line to `applied, not pending`, and
// delete the `cp` line from its copy-in block. Each fails here by naming the
// file.
func TestDocsPendingWorkflowsReadmeDescribesEveryStagedFile(t *testing.T) {
	lines := strings.Split(readDoc(t, pendingWorkflowsReadmePath), "\n")
	for _, name := range pendingWorkflows(t) {
		// The heading is read at the start of a line rather than anywhere in the
		// document, because this README quotes its own headings in prose: the
		// paragraph at the top points at `## `images.yml``, and a substring
		// search would take that mention for the section and read the claim it
		// is made in. The first heading that names the file is the section that
		// describes it; the ones after it are the records of earlier stagings of
		// the same name.
		heading := "## `" + name + "`"
		start := -1
		for i, line := range lines {
			if strings.HasPrefix(line, heading) {
				start = i
				break
			}
		}
		if start < 0 {
			t.Errorf("%s has no %q heading: every workflow staged in this directory is described by a section of this README, and a file staged without one is a file whose copy-in nobody can find", pendingWorkflowsReadmePath, heading)
			continue
		}
		end := len(lines)
		for i := start + 1; i < len(lines); i++ {
			if strings.HasPrefix(lines[i], "## ") {
				end = i
				break
			}
		}
		// The status line, read where it is: the first non-blank line of the
		// section, which is where the convention puts it and the only place a
		// quotation of it cannot be mistaken for it.
		opening := ""
		for _, line := range lines[start+1 : end] {
			if strings.TrimSpace(line) != "" {
				opening = strings.TrimSpace(line)
				break
			}
		}
		if !strings.HasPrefix(opening, "**Status:") || !strings.Contains(opening, "staged and pending") {
			t.Errorf("%s's %q section opens with %q rather than a status line saying the file is staged and pending: the section that describes a staged workflow is also the one that says whether the owner still has to copy it in, and a status left saying something else is a reader sent to a file that is not there", pendingWorkflowsReadmePath, name, opening)
		}
		flat := flattenDoc(strings.Join(lines[start:end], "\n"))
		copyIn := "cp docs/pending-workflows/" + name + " .github/workflows/" + name
		if !strings.Contains(flat, copyIn) {
			t.Errorf("%s's %q section carries no copy-in command (%q): this README is where the owner reads how to apply a staged file, so a section without the `cp` line is a change nobody can land", pendingWorkflowsReadmePath, name, copyIn)
		}
	}
}

// windowsConsoleNoteDate is the date the notes T-H3 owes carry: the landing
// date of the change that wrote them, which is what a reader comparing them
// against the ruling of 2026-10-20 they record is looking for.
const windowsConsoleNoteDate = "2026-11-10"

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
