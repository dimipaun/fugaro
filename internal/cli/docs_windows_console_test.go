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
//   - **The twelve decisions are answered, and answering is not approving.**
//     The owner answered D1-D12 on 2026-10-20, D7 against the recommendation
//     (the pack runs at release time only). A document still saying "none is
//     answered" would describe a plan nobody may start; a plan whose OWNER
//     APPROVAL GATE had quietly become "passed" would be a plan that starts
//     without the owner saying so.
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
// The mutations that prove it: empty D6's answer cell, and change D7's "(c)" to
// "(a)". Each fails by naming the row.
func TestDocsWindowsConsoleReconciliationRecordsTheOwnerAnswers(t *testing.T) {
	const path = "docs/console-windows/reconciliation.md"
	doc := readDoc(t, path)

	const header = "| # | The owner's answer (2026-10-20) | Why, in one line |"
	if !strings.Contains(doc, header) {
		t.Fatalf("%s has no answers table under %q: the owner answered D1-D12 on 2026-10-20, and the record has to be a table a reader can check a decision against", path, header)
	}
	if !strings.Contains(flattenDoc(doc), "Answering a decision is not approving") {
		t.Errorf("%s no longer says that answering a decision is not approving the build: the answers are answers, and the plan's OWNER APPROVAL GATE is a separate thing that is still not passed", path)
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
}

// TestDocsWindowsConsolePlanIsReleaseTimeOnly pins the plan's half of the D7
// ruling: the status line says the decisions are answered and the gate is not
// passed, the pack is a release-time job, the accepted risk is written down,
// and the OWNER APPROVAL GATE still refuses to start anything.
//
// The mutations that prove it: change the status line's "not passed" to
// "passed", and delete the accepted-risk sentence. Each fails here.
func TestDocsWindowsConsolePlanIsReleaseTimeOnly(t *testing.T) {
	const path = "docs/console-windows/implementation-plan.md"
	doc := readDoc(t, path)
	flat := flattenDoc(doc)

	for _, want := range []string{
		// The status line: answered, not approved.
		"the twelve decisions are answered",
		"the OWNER APPROVAL GATE is still not passed",
		// Answering is not approving.
		"Answering a decision is not approving the build",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("%s no longer carries %q: the D7 ruling of 2026-10-20 is what the plan is re-cut around, and the plan has to say what it is, what it costs, and that it is not an approval", path, want)
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

	// The gate, whole: it says it is not passed, it records every answer, and it
	// says in those words that an answer is not an approval.
	gate := windowsConsoleBlock(t, path, doc, "## 5. OWNER APPROVAL GATE", "")
	gateFlat := flattenDoc(gate)
	if !strings.Contains(gateFlat, "Not passed") {
		t.Errorf("%s's OWNER APPROVAL GATE no longer says it is not passed: the owner's answers are answers to decisions, not an approval of eight parts, a staged workflow and an owner checklist", path)
	}
	for i := 1; i <= 12; i++ {
		if !strings.Contains(gateFlat, "D"+strconv.Itoa(i)) {
			t.Errorf("%s's OWNER APPROVAL GATE no longer names D%d: the gate is where a reader checks which decisions are answered and which are not", path, i)
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
