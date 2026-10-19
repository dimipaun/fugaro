package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// mobilePhase3Directories maps each Phase 3 task of
// docs/mobile/implementation-plan.md to the directory its Files list gives it.
// The map is this test's reading of the plan; the tree half of the check is
// derived, because "is this directory here?" is the fact that moves.
var mobilePhase3Directories = map[string][]string{
	"P3.1": {"mobile/src/app", "mobile/src/skeleton", "mobile/src/accounts"},
	"P3.2": {"mobile/src/feed"},
	"P3.3": {"mobile/src/jobs"},
	"P3.4": {"mobile/src/audio"},
	"P3.5": {"mobile/src/voice"},
	"P3.6": {"mobile/src/mailbox"},
}

// phase3TaskRE finds a Phase 3 task token ("P3.4") in prose.
var phase3TaskRE = regexp.MustCompile(`P3\.[0-9]`)

// phase3NotMergedSubjects returns the Phase 3 tasks a flattened paragraph says
// are "not merged": the tasks named in the clause each occurrence of the phrase
// sits in, where a clause starts at the previous sentence break.
//
// The clause, and not the paragraph, is the unit: `speaker.js`'s header
// discusses P3.4, P3.5 and P3.6 in one paragraph, and only P3.4 was the subject
// of "is not merged" -- a paragraph-wide check would blame P3.6 for a claim
// about P3.4 and could never go green.
func phase3NotMergedSubjects(flat string) []string {
	lower := strings.ToLower(flat)
	var subjects []string
	for at := 0; at < len(lower); {
		i := strings.Index(lower[at:], "not merged")
		if i < 0 {
			break
		}
		i += at
		start := 0
		for _, boundary := range []string{". ", "; ", " — ", ": "} {
			// ". " rather than ".": a task token contains a period of its own.
			if j := strings.LastIndex(flat[:i], boundary); j >= 0 && j+len(boundary) > start {
				start = j + len(boundary)
			}
		}
		for _, task := range phase3TaskRE.FindAllString(flat[start:i], -1) {
			if !slices.Contains(subjects, task) {
				subjects = append(subjects, task)
			}
		}
		at = i + len("not merged")
	}
	return subjects
}

// jsCommentLeaderRE matches a JavaScript comment leader at the start of a line,
// with the one space this tree writes after it. Replacing it with nothing but
// the newline turns a wrapped comment block into the paragraph its author wrote.
var jsCommentLeaderRE = regexp.MustCompile(`\n//[ \t]?`)

// TestDocsMobilePhase3NotMergedClaimsAgreeWithTheTree: a mobile document that
// says a Phase 3 task "is not merged" is making a claim about this repository's
// tree, and the tree is what decides whether it is true. PR #592 put
// `mobile/src/audio/` on `main`, and three files under `mobile/` went on saying
// P3.4 was not merged -- `mobile/src/mailbox/speaker.js`'s header,
// `mobile/src/mailbox/README.md`'s "The audio stub" section and
// `mobile/src/mailbox/speaker.test.js`'s first line -- because P3.6 was
// written when it was true and nothing read those sentences afterwards. This is
// the same drift `TestDocsVoiceModuleReconciliationPinsItsBaseline` catches in
// the voice-module documents, and it is caught the same way: the claim is read
// out of the document and checked against `os.Stat`.
//
// Paragraph-level, because that is the unit a reader reads: a paragraph that
// says "not merged" and names a task whose directory the tree carries is the
// offence, wherever in `mobile/` it is. A paragraph about a task that really has
// not merged (P3.3's `mobile/src/jobs/`, the only Phase 3 directory the tree
// still does not carry) is the control and passes.
//
// Mutation (verified, run and restored): put "is **not merged**" back into
// mobile/src/mailbox/speaker.js's header, or write "P3.2 (`mobile/src/feed`) is
// not merged" into any file under mobile/: this fails, both ways. A paragraph
// saying "P3.3 is not merged" stays green, because `mobile/src/jobs/` is not in
// the tree. The control used to be P3.5, and it moved when P3.5's work arrived
// in this revision's tree: `mobile/src/voice/` is there now, so a paragraph
// saying "P3.5 is not merged" is the offence rather than the control, and the
// only directory left to play that part is P3.3's.
func TestDocsMobilePhase3NotMergedClaimsAgreeWithTheTree(t *testing.T) {
	var files []string
	err := filepath.WalkDir("../../mobile", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "node_modules" || entry.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".md") || strings.HasSuffix(path, ".js") {
			files = append(files, strings.TrimPrefix(path, "../../"))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The control: the walk read the files that made the claim, and they still
	// discuss P3.4, so neither an empty walk nor a deleted discussion can pass
	// for a corrected one.
	for _, want := range []string{"mobile/README.md", "mobile/src/mailbox/README.md", "mobile/src/mailbox/speaker.js"} {
		if !slices.Contains(files, want) {
			t.Fatalf("the walk over mobile/ did not read %s; the scan is not the tree", want)
		}
	}
	discussed := 0
	for _, file := range files {
		text := readDoc(t, file)
		if strings.Contains(text, "P3.4") {
			discussed++
		}
		// A comment's paragraph break is a bare `//`, and its prose wraps the
		// same way markdown's does, so "not merged" is written across two lines
		// in `speaker.js`'s header and read as one phrase here: the leaders go,
		// and a paragraph's whitespace is collapsed before it is searched.
		if strings.HasSuffix(file, ".js") {
			text = jsCommentLeaderRE.ReplaceAllString(text, "\n")
		}
		for _, para := range strings.Split(text, "\n\n") {
			flat := strings.Join(strings.Fields(para), " ")
			for _, task := range phase3NotMergedSubjects(flat) {
				for _, dir := range mobilePhase3Directories[task] {
					if _, err := os.Stat("../../" + dir); err == nil {
						t.Errorf("%s says %s is not merged, and %s is in the tree: the claim is about this repository, so the tree decides it", file, task, dir)
					}
				}
			}
		}
	}
	if discussed == 0 {
		t.Errorf("no file under mobile/ names P3.4 any more: %d files were read, so the discussion this check guards was deleted rather than corrected", len(files))
	}
}

// TestDocsMobilePlanPhase6IsApproved: the mobile plan's Phase 6 carried
// "NOT APPROVED" on every one of its thirteen tasks until 2026-10-18, when
// the owner approved the phase. A plan that still says so is a plan the next
// agent will not start from — P6.2, P6.3 and P6.4 have all been built and
// merged under it — so the status is pinned here the way
// docs_desktop_console_test.go pins Task 3.4's ("NOT APPROVED TO IMPLEMENT"
// must not survive the owner's go).
//
// Three halves, because a phase's status is stated in three places: the flag
// word the whole document must be free of, the section heading, and the
// per-task dependency rows a reader who jumps straight to P6.6 actually
// lands on.
//
// Mutation (verified, run and restored): put "NOT APPROVED" back into the
// Phase 6 heading, or drop the approval tag from one task's dependency row:
// this test fails, both ways.
func TestDocsMobilePlanPhase6IsApproved(t *testing.T) {
	const path = "docs/mobile/implementation-plan.md"
	doc := readDoc(t, path)

	const approved = "approved 2026-10-18"
	if strings.Contains(doc, "NOT APPROVED") {
		t.Errorf("%s still carries a NOT APPROVED status line: the owner approved all of Phase 6 on 2026-10-18, and nothing else in this plan claims that phrase", path)
	}
	head := "## Phase 6 —"
	i := strings.Index(doc, head)
	if i < 0 {
		t.Fatalf("%s no longer has a %q section heading; update this test's markers", path, head)
	}
	end := strings.Index(doc[i:], "\n\n### Task P6.1")
	if end < 0 {
		t.Fatalf("%s's Phase 6 heading is no longer followed by P6.1's task section; update this test's markers", path)
	}
	for _, want := range []string{
		"## Phase 6 — APPROVED by the owner on 2026-10-18",
		"the owner approved all of Phase 6 on 2026-10-18",
	} {
		if section := doc[i : i+end]; !strings.Contains(section, want) {
			t.Errorf("%s's Phase 6 heading does not say %q: %q", path, want, section)
		}
	}
	// Every task's own row, which is where a reader who opens the file at
	// P6.6's line reads the status from.
	for n := 1; n <= 13; n++ {
		row := fmt.Sprintf("\n| P6.%d *(approved 2026-10-18)*", n)
		if !strings.Contains(doc, row) {
			t.Errorf("%s has no %q: P6.%d's dependency row does not record the owner's approval, so a reader checking that one task is told Phase 6 is still unapproved", path, row, n)
		}
	}
}

// TestDocsMobilePlanP44NamesItsFiles: P4.4's entry said "**Files.** None in this
// repository." -- it was written as the owner-run task that makes the EAS
// project, the signing keys and the store entries, all of which live outside
// the tree. It is not: the two files a build reads are committed, and they are
// P4.4's -- `mobile/eas.json` (the profiles) and `mobile/app.config.js` (the
// dynamic config that resolves the two store identities from EAS environment
// variables). The register says so in check 89's row, and `mobile/README.md`
// says so in its own section; the plan is the third place a reader looks, and
// until this test it was the one that still said none of it exists.
//
// The pin is held to the register's own words rather than to a copy of them: if
// the row for check 89 stops naming the two files, this test fails on that too,
// because the two documents describing one task's files have to agree.
//
// The pin covers the two summaries a reader reaches without opening the task,
// because a claim is only corrected where it is read: the level table's row for
// level 3 and the PR-groups row for P-H both filed P4.4 under "none", and the
// level table is the artifact the assignments are justified from.
//
// Mutations (each verified, run and restored): put "**Files.** None in this
// repository." back into P4.4's entry (fails on the entry); drop
// `mobile/app.config.js` from the entry's file list (fails on the entry); drop
// "**Owner-run.**" from the entry (fails on the owner-run half); rename
// every mention of `mobile/app.config.js` in check 89's row to
// `mobile/app.json` (fails on the cross-document half -- the row names the file
// in three places, so removing one of them is not a mutation of the property,
// which is that the register names it at all); put "none (the owner's accounts)"
// back as first the level-3 row's file column and then the P-H row's (fails on
// each summary).
func TestDocsMobilePlanP44NamesItsFiles(t *testing.T) {
	const path = "docs/mobile/implementation-plan.md"
	doc := readDoc(t, path)

	const head = "### Task P4.4:"
	i := strings.Index(doc, head)
	if i < 0 {
		t.Fatalf("%s no longer has a %q section heading; update this test's markers", path, head)
	}
	end := strings.Index(doc[i:], "\n### Task ")
	if end < 0 {
		t.Fatalf("%s's P4.4 section is not followed by another task heading; update this test's markers", path)
	}
	entry := doc[i : i+end]

	if strings.Contains(entry, "**Files.** None in this repository.") {
		t.Errorf("%s's P4.4 entry still says its files are none in this repository, and P4.4's own two files are in the tree (`mobile/eas.json`, `mobile/app.config.js`): the entry is what a later reader trusts about what this task owns, and check 89's row already attributes both to it", path)
	}
	for _, want := range []string{"mobile/eas.json", "mobile/app.config.js"} {
		if !strings.Contains(entry, want) {
			t.Errorf("%s's P4.4 entry does not name %s: the file a build is made from is the entry's subject, and a reader who opens P4.4 has to find it there", path, want)
		}
	}
	// The owner-run half is what the entry is about and has to survive the
	// correction: the EAS project, the keys and the identity values are the
	// owner's, and no agent runs a step of it.
	for _, want := range []string{"**Owner-run.**", "Global Constraint 9", "**Reviewer tier.** owner."} {
		if !strings.Contains(entry, want) {
			t.Errorf("%s's P4.4 entry no longer carries %q: naming the two committed files does not make the EAS project, the signing keys or the store entries an agent's to run", path, want)
		}
	}

	// The two summaries a reader reaches **without** opening P4.4 at all, which
	// is why the task section alone is not enough: the level table's row for
	// level 3 is the artifact the level assignments are read from, and the
	// PR-groups row is what tells an agent whether this task's files arrive in a
	// PR. Both said P4.4 owns nothing in the tree.
	levelRow := planRow(t, path, doc, "**P4.4** the store path (owner-run)")
	if strings.Contains(levelRow, "none (the owner's accounts)") {
		t.Errorf("%s's level-3 row still files P4.4 under %q: the level table is what a reader checks to see which files a level writes, and P4.4's two files are in the tree (`mobile/eas.json`, `mobile/app.config.js`)", path, "none (the owner's accounts)")
	}
	for _, want := range []string{"mobile/eas.json", "mobile/app.config.js"} {
		if !strings.Contains(levelRow, want) {
			t.Errorf("%s's level-3 row does not name %s: the row's file column is the only place the level's write set is stated, and a reader who trusts it concludes P4.4 owns nothing here", path, want)
		}
	}
	groupRow := planRow(t, path, doc, "| **P-H** |")
	if strings.Contains(groupRow, "**No PR.**") {
		t.Errorf("%s's P-H row still says %q for the whole group: P4.4's `mobile/eas.json` and `mobile/app.config.js` are committed code, so they arrive in a PR like any other file -- what is owner-run and PR-less is the EAS project, the keys and the store entries", path, "**No PR.**")
	}
	for _, want := range []string{"mobile/eas.json", "mobile/app.config.js"} {
		if !strings.Contains(groupRow, want) {
			t.Errorf("%s's P-H row does not name %s, so it reads as a group with no files and no PR: P4.4 commits it", path, want)
		}
	}

	// ... and the register agrees about whose files these are, which is the
	// cross-document half of the pin: check 89's row names P4.4's two files.
	register := readDoc(t, "docs/gcp-live-checklist.md")
	row := registerRow(t, register, "89")
	for _, want := range []string{"mobile/eas.json", "mobile/app.config.js"} {
		if !strings.Contains(row, want) {
			t.Errorf("the register row for check 89 does not name %s, which the plan attributes to P4.4: the check is built from that file, so the two documents have to name it the same way", want)
		}
	}
}

// planRow returns the single line of doc that contains marker, and stops the
// test when the marker is missing or ambiguous -- the two markers this file
// looks for are a task name inside the level table's row and a PR-group name,
// and both tables have rows that look alike (there is a level 3 in the Phase 6
// table too), so "the row this is about" has to be resolved by something the
// row itself says rather than by position.
func planRow(t *testing.T, path, doc, marker string) string {
	t.Helper()
	var found []string
	for _, line := range strings.Split(doc, "\n") {
		if strings.Contains(line, marker) {
			found = append(found, line)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%s has %d lines containing %q, want exactly 1; update this test's markers", path, len(found), marker)
	}
	return found[0]
}
