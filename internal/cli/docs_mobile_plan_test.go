package cli

import (
	"fmt"
	"strings"
	"testing"
)

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
