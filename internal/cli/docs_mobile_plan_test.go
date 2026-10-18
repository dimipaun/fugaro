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
