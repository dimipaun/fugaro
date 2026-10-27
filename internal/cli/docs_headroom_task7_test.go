package cli

import (
	"strings"
	"testing"
)

// sliceBetween returns the part of s from the first occurrence of from to the
// first occurrence of to after it, so a claim can be read inside one section
// of a long document rather than anywhere in it.
func sliceBetween(t *testing.T, s, from, to string) string {
	t.Helper()
	i := strings.Index(s, from)
	if i < 0 {
		t.Fatalf("document does not contain %q", from)
	}
	rest := s[i:]
	j := strings.Index(rest, to)
	if j < 0 {
		t.Fatalf("document does not contain %q after %q", to, from)
	}
	return rest[:j]
}

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
	} {
		if strings.Contains(plan, banned) {
			t.Errorf("the Task 7 note still carries the withdrawn claim %q", banned)
		}
	}

	l2 := sliceBetween(t, plan, "Check L2. Can Headroom's own configuration", "Check L3.")
	if !strings.Contains(l2, "Task 7") {
		t.Error("Check L2 no longer points at the Task 7 note")
	}

	// The correction to the size-limit sentence rests on the design's own
	// words, so that citation is pinned too.
	for _, want := range []string{"images/size-budget.sh", "4.0 GB hard limit", "3.5 GB warning"} {
		if !strings.Contains(design, want) {
			t.Errorf("the design no longer names its size budget (%q)", want)
		}
	}
}
