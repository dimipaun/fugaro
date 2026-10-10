package watch

import "testing"

func TestCursorFollowsRunAcrossRebuild(t *testing.T) {
	before := []Cursor{{"a", ""}, {"a", "r1"}, {"a", "r2"}, {"b", ""}, {"b", "r3"}}
	after := []Cursor{{"a", ""}, {"a", "r1"}, {"b", ""}, {"b", "r3"}} // r2 finished and was filtered out
	if got := Resolve(after, before, Cursor{"a", "r2"}); got != (Cursor{"a", "r1"}) {
		t.Fatalf("vanished run resolved to %+v, want its neighbour a/r1", got)
	}
	if got := Resolve(after, before, Cursor{"b", "r3"}); got != (Cursor{"b", "r3"}) {
		t.Fatalf("present run moved to %+v", got)
	}
}

// The common case: a vanished row's "old position" is filled by the row
// that was after it, not the one before it (removing an item from a list
// slides the rest up, it doesn't slide them down). When both an earlier and
// a later sibling survive, Resolve must prefer the later one.
func TestCursorPrefersDownwardNeighbourOverUpward(t *testing.T) {
	before := []Cursor{{"a", ""}, {"a", "r1"}, {"a", "r2"}, {"a", "r3"}}
	after := []Cursor{{"a", ""}, {"a", "r1"}, {"a", "r3"}} // r2 vanished; r1 and r3 both remain
	if got := Resolve(after, before, Cursor{"a", "r2"}); got != (Cursor{"a", "r3"}) {
		t.Fatalf("got %+v, want r3 (the row that slid up into r2's old slot), not r1", got)
	}
}

// When nothing below the vanished row survives, Resolve climbs back to the
// header; when the header is gone too, it falls back to the first row. "z"
// sorts before "a" so the header and the first row are never the same
// Cursor by coincidence.
func TestCursorFallsBackToHeaderThenFirstRow(t *testing.T) {
	before := []Cursor{{"z", ""}, {"a", ""}, {"a", "r1"}, {"b", ""}}
	afterHeaderOnly := []Cursor{{"z", ""}, {"a", ""}, {"b", ""}}
	if got := Resolve(afterHeaderOnly, before, Cursor{"a", "r1"}); got != (Cursor{"a", ""}) {
		t.Fatalf("got %+v, want the header", got)
	}
	afterBlockGone := []Cursor{{"z", ""}, {"b", ""}}
	if got := Resolve(afterBlockGone, before, Cursor{"a", "r1"}); got != (Cursor{"z", ""}) {
		t.Fatalf("whole block gone: got %+v, want the first row", got)
	}
}

// A finished run is a selectable row too (design generic-tool §10.3: "x on
// the selected row" acknowledges a failure), listed after the block's
// running and queued runs, so x can target one without a special key.
func TestRowsIncludesFinishedAfterRuns(t *testing.T) {
	v := View{Repos: []RepoBlock{
		{Slug: "a", Runs: []RunRow{{Run: "r1"}}, Finished: []RunRow{{Run: "f1"}, {Run: "f2"}}},
	}}
	got := Rows(v, nil)
	want := []Cursor{{"a", ""}, {"a", "r1"}, {"a", "f1"}, {"a", "f2"}}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	}
	// A folded block hides its finished rows too, like it hides its runs.
	folded := Rows(v, map[string]bool{"a": true})
	if len(folded) != 1 {
		t.Fatalf("folded: got %+v, want only the header", folded)
	}
}

func TestRowsHonoursFolds(t *testing.T) {
	v := View{Repos: []RepoBlock{
		{Slug: "a", Runs: []RunRow{{Run: "r1"}, {Run: "r2"}}},
		{Slug: "b", Runs: []RunRow{{Run: "r3"}}},
	}}
	got := Rows(v, nil)
	want := []Cursor{{"a", ""}, {"a", "r1"}, {"a", "r2"}, {"b", ""}, {"b", "r3"}}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	}
	folded := Rows(v, map[string]bool{"a": true})
	wantFolded := []Cursor{{"a", ""}, {"b", ""}, {"b", "r3"}}
	if len(folded) != len(wantFolded) {
		t.Fatalf("folded: got %+v, want %+v", folded, wantFolded)
	}
	for i := range wantFolded {
		if folded[i] != wantFolded[i] {
			t.Fatalf("folded: got %+v, want %+v", folded, wantFolded)
		}
	}
}
