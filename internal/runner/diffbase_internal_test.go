package runner

import (
	"strings"
	"testing"
)

// TestHugeDiffThreshold pins the one place the review prompt's "this diff is
// huge" line is decided: prompts.go's hugeDiff. Both numbers are
// thresholds, and a diff exactly at one is not past it — the note is for
// diffs that are larger than the numbers say, never for the number itself.
//
// Mutation (change either comparison to >=, or to a plain equality): the
// at-the-threshold case fails.
func TestHugeDiffThreshold(t *testing.T) {
	// The values themselves, not just that the note follows them: raising
	// either is how review cost comes back, so it is a deliberate edit to
	// this test as well.
	if hugeDiff != (DiffSize{Files: 100, Lines: 8000}) {
		t.Fatalf("hugeDiff = %+v, want 100 files and 8000 lines", hugeDiff)
	}
	at := DiffSize{Files: hugeDiff.Files, Lines: hugeDiff.Lines}
	if at.Huge() {
		t.Errorf("a diff exactly at the threshold (%+v) reads as huge", at)
	}
	for _, over := range []DiffSize{
		{Files: hugeDiff.Files + 1, Lines: hugeDiff.Lines},
		{Files: hugeDiff.Files, Lines: hugeDiff.Lines + 1},
		{Files: 500},
		{Lines: 20000},
	} {
		if !over.Huge() {
			t.Errorf("%+v reads as small, want huge (> %+v)", over, hugeDiff)
		}
	}
}

// TestReviewPromptHugeNoteIsUpFrontAndNamesTheBase: the huge-diff note comes
// before the scope line — the reviewer reads it before it reads the diff it
// is about — and it names the base explicitly, in the command it tells the
// reviewer to scope itself with, the same base the scope line diffs
// against. The note is absent for a diff at the threshold: it costs prompt
// and attention, and every review must not pay it.
//
// Mutation (append the note after the scope line, or drop the base from it):
// this test fails.
func TestReviewPromptHugeNoteIsUpFrontAndNamesTheBase(t *testing.T) {
	const base = "integration-0.9.6"
	small := ReviewPrompt("", "", base, DiffSize{Files: hugeDiff.Files, Lines: hugeDiff.Lines})
	if strings.Contains(small, "huge") {
		t.Fatalf("a diff at the threshold is warned about:\n%s", small)
	}
	big := ReviewPrompt("", "", base, DiffSize{Files: hugeDiff.Files + 1})
	note, scope, ok := strings.Cut(big, "Review the changes on this branch against ")
	if !ok {
		t.Fatalf("the scope line is gone:\n%s", big)
	}
	for _, want := range []string{
		"Do NOT fan out sub-agents",
		"git log --name-only origin/" + base + "..HEAD",
		"review only what this pull request's own commits touch",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("the note lacks %q:\n%s", want, note)
		}
	}
	if !strings.Contains(scope, "origin/"+base) {
		t.Errorf("the scope line names another base:\n%s", scope)
	}
	if !strings.Contains(note, "101 file(s)") {
		t.Errorf("the note does not say how big the diff is:\n%s", note)
	}
}

// TestParseNumstat is the size the review prompt's note is decided by, read
// from the same numstat call the review input's own change list comes from
// (reviewinput.go, reviewChanges): a binary file (numstat's "-" counts) is
// one file and no lines.
func TestParseNumstat(t *testing.T) {
	changes, size, err := parseNumstat("3\t4\ta.txt\x00-\t-\tb.png\x0012\t0\tc.txt\x00")
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 3 || changes[0].path != "a.txt" || changes[0].binary || changes[1].path != "b.png" || !changes[1].binary {
		t.Fatalf("changes = %+v", changes)
	}
	if size != (DiffSize{Files: 3, Lines: 19}) {
		t.Fatalf("size = %+v, want 3 files and 19 lines (the binary file counts as one file, no lines)", size)
	}
	if _, _, err := parseNumstat("x\t2\ta.txt\x00"); err == nil {
		t.Error("a non-numeric count parsed without an error")
	}
}
