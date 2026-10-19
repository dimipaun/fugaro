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
// not merged (P3.5's `mobile/src/voice/` today) is the control and passes.
//
// Mutation (verified, run and restored): put "is **not merged**" back into
// mobile/src/mailbox/speaker.js's header, or write "P3.2 (`mobile/src/feed`) is
// not merged" into any file under mobile/: this fails, both ways. A paragraph
// saying "P3.5 is not merged" stays green, because `mobile/src/voice/` is not in
// the tree.
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
// word, the section heading, and the per-task dependency rows a reader who
// jumps straight to P6.6 actually lands on.
//
// **The flag word is scoped to the lines that name Phase 6, and 2026-10-19 is
// why.** It was a ban on the phrase anywhere in the document, which was a
// stand-in for the fact this test is about — no APPROVED phase may still claim
// to be unapproved — and it held only while Phase 6 was the last phase written.
// The telemetry phase (P7.1–P7.5) is now written and genuinely unapproved, so it
// must be able to carry the flag word, and it does. The ban is now applied per
// line to every line naming `Phase 6` or `P6.` — the phase summary row, the
// heading, each task heading and each dependency row — and Phase 7's own lines
// carry the converse assertion below. A phase whose status line is deleted is
// still red in both directions.
//
// Mutation (verified, run and restored): put "NOT APPROVED" back into the
// Phase 6 heading, drop the approval tag from one task's dependency row, delete
// "NOT APPROVED" from the Phase 7 heading, or drop the "proposed 2026-10-19"
// tag from one P7 row: this test fails, all four ways.
func TestDocsMobilePlanPhase6IsApproved(t *testing.T) {
	const path = "docs/mobile/implementation-plan.md"
	doc := readDoc(t, path)

	// The flag word, scoped to the phase it is about: any line that names Phase
	// 6 must not say the phase is unapproved.
	for _, line := range strings.Split(doc, "\n") {
		if (strings.Contains(line, "Phase 6") || strings.Contains(line, "P6.")) &&
			strings.Contains(line, "NOT APPROVED") {
			t.Errorf("%s carries a NOT APPROVED status line on a line naming Phase 6: the owner approved all of Phase 6 on 2026-10-18, and no line about it may still claim otherwise:\n\t%s", path, line)
		}
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

	// The converse, for the phase that IS unapproved (added 2026-10-19 with
	// Phase 7, the telemetry phase). A reader who opens the file at P7.3's line
	// must be told the owner has approved nothing, so Phase 7's heading carries
	// the flag word and every P7 row carries its own "proposed" tag. Without
	// this half, scoping the ban above would let Phase 7's status be deleted
	// silently — the failure mode the whole test exists to prevent, moved one
	// phase along.
	p7head := "## Phase 7 —"
	j := strings.Index(doc, p7head)
	if j < 0 {
		t.Fatalf("%s no longer has a %q section heading; update this test's markers", path, p7head)
	}
	p7end := strings.Index(doc[j:], "\n\n### Task P7.1")
	if p7end < 0 {
		t.Fatalf("%s's Phase 7 heading is no longer followed by P7.1's task section; update this test's markers", path)
	}
	for _, want := range []string{"PROPOSED 2026-10-19", "NOT APPROVED"} {
		if section := doc[j : j+p7end]; !strings.Contains(section, want) {
			t.Errorf("%s's Phase 7 heading does not say %q: %q", path, want, section)
		}
	}
	for n := 1; n <= 5; n++ {
		row := fmt.Sprintf("\n| P7.%d *(proposed 2026-10-19)*", n)
		if !strings.Contains(doc, row) {
			t.Errorf("%s has no %q: P7.%d's dependency row does not record that the owner has approved nothing, so a reader checking that one task is told the telemetry phase is approved", path, row, n)
		}
	}
}
