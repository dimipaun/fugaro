package runner

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// prTitleRun is a run with nothing but the fields rawPRText reads: its spec,
// its record, and a state dir with no pr.md in it (the agent wrote none, so
// the pull request's text is built from the task).
func prTitleRun(dir string, spec *task.Spec, runID string) *run {
	return &run{spec: spec, rec: &runstore.Record{RunID: runID}, d: Deps{StateDir: dir}}
}

// Without a pr.md the pull request's title is the run's own, and it comes
// from the ONE place every surface reads (task.Spec.TitleLine): the title
// the launcher stored (fugaro run --title), else the task prompt's first
// line — so a brief that opens with a shared preamble is told apart in the
// pull request and not only in fugaro watch.
//
// Mutation (build the title from the task's first line instead of
// Spec.TitleLine): the PR is titled "Read the plan first." and this test
// fails.
func TestPRTitleUsesTheRunsOwnTitle(t *testing.T) {
	spec := &task.Spec{
		Repo: "acme/app", Task: "Read the plan first.\n\nThen fix the flaky test.",
		Title: "Ship the flaky-test fix",
	}
	title, body := prTitleRun(t.TempDir(), spec, "20261018-120000-abcd").rawPRText()
	if title != "Ship the flaky-test fix" {
		t.Fatalf("PR title = %q, want the run's own title", title)
	}
	// A run with its own title also carries it as the body's first heading:
	// the description says what the run was for even when a reader scrolls
	// past the title.
	if !strings.Contains(body, "# Ship the flaky-test fix\n\n## Task\n\nRead the plan first.") {
		t.Fatalf("PR body has no title heading:\n%s", body)
	}
}

// A run launched without --title is exactly as it was before the field
// existed: the title is the task prompt's first line and the body has no
// heading of its own, byte for byte.
func TestPRTitleOfARunWithoutATitleIsUnchanged(t *testing.T) {
	spec := &task.Spec{Repo: "acme/app", Task: "Add a feature\nand more"}
	title, body := prTitleRun(t.TempDir(), spec, "20261018-120000-abcd").rawPRText()
	if title != "Add a feature" {
		t.Fatalf("PR title = %q, want the task's first line", title)
	}
	want := "Opened by Fugaro run `20261018-120000-abcd`.\n\n## Task\n\nAdd a feature\nand more"
	if body != want {
		t.Fatalf("PR body =\n%q\nwant\n%q", body, want)
	}
}

// The default title is clipped to 72 runes before it becomes a pull request
// title, whatever it came from (the launcher's title, here), so a long one
// cannot overflow the platform's own limit.
func TestPRTitleIsClipped(t *testing.T) {
	long := strings.Repeat("日", 100)
	spec := &task.Spec{Repo: "acme/app", Task: "x", Title: long}
	title, _ := prTitleRun(t.TempDir(), spec, "20261018-120000-abcd").rawPRText()
	runes := []rune(title)
	if len(runes) != 72 || runes[71] != '…' {
		t.Fatalf("title = %q (%d runes), want 71 runes and an ellipsis", title, len(runes))
	}
}
