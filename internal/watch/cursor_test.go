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
