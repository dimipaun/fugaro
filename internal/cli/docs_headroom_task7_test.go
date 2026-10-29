package cli

import (
	"strings"
	"testing"
)

// TestHeadroomTask7NoteCorrections pins the corrections the review of #1162
// asked for in the Task 7 note (docs/plans/2026-10-12-headroom-compression.md,
// its revised 2026-10-27 note) and in the design it cites, so the false
// sentences they replaced cannot come back:
//
//   - the note contradicted itself on whether the telemetry beacon egresses
//     under `HEADROOM_OFFLINE=1`, where the docs it cites name that switch as
//     one that disables the beacon;
//   - it said "neither states a size limit anywhere", where the design's §5
//     "Size cost" paragraph names images/size-budget.sh with a 4.0 GB hard
//     limit and a 3.5 GB warning;
//   - its lock-entry count named pywin32 alone as the Windows-only entry, so
//     95 could not reconcile with the 93 an install has;
//   - it added uv's managed CPython to a base image that already carries one
//     for graphify;
//   - the second blocker (models fetched from HuggingFace at run time) was
//     recorded without saying that it is a blocker, or which of its sizes are
//     unverified.
//
// The corrections also have to be reachable from Check L2, whose parenthetical
// sends the reader to whatever Task 7 found.
func TestHeadroomTask7NoteCorrections(t *testing.T) {
	plan := flattenDoc(readDoc(t, "docs/plans/2026-10-12-headroom-compression.md"))
	design := flattenDoc(readDoc(t, "docs/design/headroom-compression.md"))

	for _, want := range []string{
		"`HEADROOM_OFFLINE` is named among the switches that disable it",
		"UNVERIFIED: whether any beacon egress attempt still happens with `HEADROOM_OFFLINE=1` set",
		"4.0 GB hard limit",
		"3.5 GB warning",
		"#1162 pull request's summary repeated",
		"95 entries",
		"`colorama`",
		"~746 MB",
		"3812 MB",
		"fetches its ML models from `huggingface.co` when it *runs*",
		// The derived figures the 746 MB correction changed with it.
		"~1.9 times the ~400 MB ceiling",
		"~0.56 GB over the current budget",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("the Task 7 note no longer says %q", want)
		}
	}

	for _, banned := range []string{
		"a size limit anywhere — it is the owner's",
		"`pywin32` appears in the universal lock for Windows only",
		`design §5's "materially heavier"`,
		"beacon is on unless",
		"more than four times what the base image has left",
		"~0.67 GB over the current budget",
	} {
		if strings.Contains(plan, banned) {
			t.Errorf("the Task 7 note still carries the withdrawn claim %q", banned)
		}
	}

	// Check L2's own parenthetical already sends the reader to "whatever Task 7
	// found", so the pointer this correction adds is what is pinned — the bare
	// words "Task 7" are there with or without it, and would assert nothing.
	l2 := mdBetween(t, "docs/plans/2026-10-12-headroom-compression.md", plan,
		"Check L2. Can Headroom's own configuration", "Check L3.")
	if !strings.Contains(l2, "its Corrections block records that Headroom fetches its ML models at run time") {
		t.Error("Check L2 no longer points at the Task 7 revised note and its Corrections block")
	}

	// The correction to the size-limit sentence rests on the design's own
	// words, so that citation is pinned too.
	for _, want := range []string{"images/size-budget.sh", "4.0 GB hard limit", "3.5 GB warning"} {
		if !strings.Contains(design, want) {
			t.Errorf("the design no longer names its size budget (%q)", want)
		}
	}
}
