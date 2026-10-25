package runner

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/task"
)

// TestHugeDiffThreshold pins the one place the review prompt's "this diff is
// huge" line is decided: prompts.go's hugeDiffFiles and hugeDiffLines constants. Both numbers are
// thresholds, and a diff exactly at one is not past it — the note is for
// diffs that are larger than the numbers say, never for the number itself.
//
// Mutation (change either comparison to >=, or to a plain equality): the
// at-the-threshold case fails.
func TestHugeDiffThreshold(t *testing.T) {
	// The values themselves, not just that the note follows them: raising
	// either is how review cost comes back, so it is a deliberate edit to
	// this test as well.
	if hugeDiffFiles != 100 || hugeDiffLines != 8000 {
		t.Fatalf("thresholds = %d files, %d lines; want 100 and 8000", hugeDiffFiles, hugeDiffLines)
	}
	at := DiffSize{Files: hugeDiffFiles, Lines: hugeDiffLines}
	if at.Huge() {
		t.Errorf("a diff exactly at the threshold (%+v) reads as huge", at)
	}
	for _, over := range []DiffSize{
		{Files: hugeDiffFiles + 1, Lines: hugeDiffLines},
		{Files: hugeDiffFiles, Lines: hugeDiffLines + 1},
		{Files: 500},
		{Lines: 20000},
	} {
		if !over.Huge() {
			t.Errorf("%+v reads as small, want huge (> %d files or %d lines)", over, hugeDiffFiles, hugeDiffLines)
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
	small := ReviewPrompt("", "", "origin/"+base, DiffSize{Files: hugeDiffFiles, Lines: hugeDiffLines}, false)
	if strings.Contains(small, "huge") {
		t.Fatalf("a diff at the threshold is warned about:\n%s", small)
	}
	big := ReviewPrompt("", "", "origin/"+base, DiffSize{Files: hugeDiffFiles + 1}, false)
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

// TestBaseValidationOnABareRun covers the three branch-name checks without a
// whole run, over every shape a refused value takes: a refused pull request
// base (follow-up or review head) or run ref falls back, is never returned,
// and a normal name is used as it is.
//
// Mutation (run, restore): `if true` in place of config.ValidBranchName at
// diffbase.go's diffBase, refBase or reviewBase — each fails its own
// subtests below.
func TestBaseValidationOnABareRun(t *testing.T) {
	newRun := func(ref string) *run {
		return &run{
			d:    Deps{Log: slog.New(slog.DiscardHandler)},
			spec: &task.Spec{Ref: ref},
			cfg:  &config.Config{Git: config.Git{BaseBranch: "main"}},
		}
	}
	bad := []string{"--upload-pack=x", "-x", "a`b", "a..b", "a\nb", ""}
	for _, b := range bad {
		r := newRun("integration-0.9.6")
		r.follow = &followState{pr: gitprov.PRInfo{Base: b}}
		if got := r.diffBase(); got != "integration-0.9.6" {
			t.Errorf("follow-up base %q: diffBase = %q, want the run's ref", b, got)
		}
		if !r.baseUnreadable() {
			t.Errorf("follow-up base %q: baseUnreadable = false", b)
		}
		if got := r.reviewBase(reviewHead{Number: 1, Base: b}); got != "integration-0.9.6" {
			t.Errorf("review head base %q: reviewBase = %q, want the run's ref", b, got)
		}
		// A refused run ref falls back to the configuration's base.
		if got := newRun(b).refBase(); got != "main" {
			t.Errorf("run ref %q: refBase = %q, want the configuration's base main", b, got)
		}
	}
	for _, good := range []string{"integration-0.9.6", "feature/x", "main"} {
		r := newRun("main")
		r.follow = &followState{pr: gitprov.PRInfo{Base: good}}
		if got := r.diffBase(); got != good || r.baseUnreadable() {
			t.Errorf("follow-up base %q: diffBase = %q, unreadable = %v", good, got, r.baseUnreadable())
		}
		if got := r.reviewBase(reviewHead{Base: good}); got != good {
			t.Errorf("review head base %q: reviewBase = %q", good, got)
		}
		if got := newRun(good).refBase(); got != good {
			t.Errorf("run ref %q: refBase = %q", good, got)
		}
	}
}

// TestReviewPromptSaysTheBaseCouldNotBeRead: one line, up front, naming the
// fallback and scoping the reviewer to the branch's own commits; absent when
// the base was read.
func TestReviewPromptSaysTheBaseCouldNotBeRead(t *testing.T) {
	unread := ReviewPrompt("", "", "origin/main", DiffSize{}, true)
	for _, want := range []string{"base branch could not be read", "git log origin/main..HEAD"} {
		if !strings.Contains(unread, want) {
			t.Errorf("the prompt lacks %q:\n%s", want, unread)
		}
	}
	if strings.Contains(ReviewPrompt("", "", "origin/main", DiffSize{}, false), "could not be read") {
		t.Error("a readable base is warned about")
	}
}

// TestReviewPromptNamesTheCommitTheRunsOwnRangeStartsAfter: a first run's
// range narrowed to its own commits starts at a commit, and the prompt says
// so before the scope line, which diffs against that commit and names no
// branch; a branch range carries no such line.
//
// Mutation (drop ownRangeNote from ReviewPrompt): this test fails.
func TestReviewPromptNamesTheCommitTheRunsOwnRangeStartsAfter(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef01234567"
	got := ReviewPrompt("", "", sha, DiffSize{}, false)
	note, scope, ok := strings.Cut(got, "Review the changes on this branch against ")
	if !ok {
		t.Fatalf("the scope line is gone:\n%s", got)
	}
	if !strings.Contains(note, sha+" is the commit this run's own commits start after") {
		t.Errorf("the prompt does not say where the run's own commits start:\n%s", got)
	}
	if !strings.HasPrefix(scope, sha+" (see `git diff "+sha+"...HEAD`)") {
		t.Errorf("the scope line does not diff against %s:\n%s", sha, scope)
	}
	if strings.Contains(got, "origin/") {
		t.Errorf("the prompt names a branch:\n%s", got)
	}
	if strings.Contains(ReviewPrompt("", "", "origin/main", DiffSize{}, false), "own commits start after") {
		t.Error("a branch range carries the own-range note")
	}
}
