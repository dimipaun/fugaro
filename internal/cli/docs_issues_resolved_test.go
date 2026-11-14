package cli

// The resolved-issues page, pinned where it can rot silently.
//
// docs/issues-resolved.md is the page a person reads to find out why `fugaro
// update` told them a release fixed their issue, what it wrote and where, and
// what has to be true on GitHub for an issue to count as fixed at all. T-RU7
// of docs/issues-resolved-on-update/ writes it; the surfaces it documents are
// T-RU2's record (internal/installrec), T-RU4's pass (internal/issues/resolve.go),
// T-RU5's update step (internal/cli/update_resolved.go) and T-RU6's report
// section (internal/issues/report.go).
//
// Each fact below is one sentence a later edit can drop with nothing else in
// the tree noticing: the object's name and its two fields are what an operator
// types `gcloud storage cat` for, the two lines are what a person sees, the
// milestone rule is the whole of how a fix is decided, and the pass's own
// bounds are what keeps an update quick. The page is also the only place that
// says an agent's session writes nothing, which is the half of the guard a
// reader has to know about.
//
// Mutation (run and restored): delete the milestone rule's sentence from the
// page -> this fails naming "at or below the release that was installed"; drop
// the `fugaro/installed.json` sentence -> fails naming the object; take the
// link out of docs/issues.md -> fails naming "issues-resolved.md". Nothing else
// in the tree reads the page.

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const docsIssuesResolvedPage = "docs/issues-resolved.md"

// TestDocsIssuesResolvedNamesTheObjectAndTheRule: the page names the object the
// update writes, the two lines it prints, the milestone rule that decides a
// fix, and the two fields the store carries the answer in — and docs/issues.md
// links it, because a page nobody links is a page nobody reads.
func TestDocsIssuesResolvedNamesTheObjectAndTheRule(t *testing.T) {
	page := flattenDoc(readDoc(t, docsIssuesResolvedPage))
	for _, want := range []struct{ text, why string }{
		{"fugaro/installed.json", "the object the update writes, which an operator reads with `gcloud storage cat` (installrec.Object)"},
		{"previous_release", "the field that makes the update's own window, so a fix a missed pass left is still reported (F17)"},
		{"This update fixes", "the line a person reads first (update_resolved.go's resolvedSummaryLead)"},
		{"Also fixed since the last pass:", "the block for the fixes an earlier pass never got to report (F6)"},
		{"hit by", "who ran into a fixed issue, which is whom the admin has to tell (F10)"},
		{"resolved_in", "the field the fixing release is recorded in, which every reader of a record compares against"},
		{"closed_as", "how a not-planned or duplicate close is recorded while fixing nothing (D1752, F9)"},
		{"at or below the release that was installed", "the rule: the milestone is the release that first ships the fix, and a later one is left for a later update (F4, D1756)"},
		{"closed as completed", "the state GitHub has to show; anything else is no verdict and the issue stays submitted"},
		{"never run", "the one thing nothing does with the command the record holds (spec step 6)"},
		{"Resolved since the last update:", "the daily report's own list of the same fixes (report.go's resolvedHead)"},
		{"writes nothing", "F16d: a coding agent's session prints the findings and writes none of them"},
		{"The findings are printed", "the half of F16d a reader gets wrong: a session is not one of the cases that print nothing, it prints the list and declines the write"},
		{"20 seconds", "the pass's own bound, which is why an update stays quick even with GitHub slow (F7, F8)"},
	} {
		if !strings.Contains(page, want.text) {
			t.Errorf("%s does not say %q (%s): the page is where a reader finds that fact, and nothing else in the tree carries it", docsIssuesResolvedPage, want.text, want.why)
		}
	}
	if link := flattenDoc(readDoc(t, docsIssuePage)); !strings.Contains(link, "issues-resolved.md") {
		t.Errorf("%s never links %s: the issues page is where a reader of the family ends up, and the resolved page is the other half of an issue's life", docsIssuePage, docsIssuesResolvedPage)
	}
}

// TestDocsIssuesResolvedPinMovesTheCensusRow: the pin above is a new
// `docs_*_test.go` in internal/cli, and the census row that counts them is
// prose the tree cannot regenerate, so the folder that adds one moves the row
// and says so beside it (the row's own rule, docs/rigor-modes/reconciliation.md).
func TestDocsIssuesResolvedPinMovesTheCensusRow(t *testing.T) {
	const path = "docs/rigor-modes/reconciliation.md"
	doc := readDoc(t, path)
	row := mdRow(t, path, doc, "| Docs folders that ship their own `internal/cli/docs_*_test.go` pin |")
	cols := strings.Split(row, "|")
	if len(cols) < 4 {
		t.Fatalf("%s' census row is not a three-column row: %q", path, row)
	}
	pins, err := filepath.Glob("../../internal/cli/docs_*_test.go")
	if err != nil {
		t.Fatalf("the folders' pins could not be listed, so the census row cannot be checked: %v", err)
	}
	if stated := strings.TrimSpace(cols[2]); stated != strconv.Itoa(len(pins)) {
		t.Errorf("%s' census row states %s rows and the tree holds %d `internal/cli/docs_*_test.go` files: the row is a count a reader re-runs, so the folder that adds a pin moves it", path, stated, len(pins))
	}
	const amendment = "Amended 2026-11-14: `internal/cli/docs_issues_resolved_test.go`, the issues-resolved-on-update folder's pin"
	if !strings.Contains(flattenDoc(doc), amendment) {
		t.Errorf("%s does not carry %q: the row's own text says a folder that writes a pin moves the row in its own commit and says so beside it, and a row that moved without the sentence is the merge the row calls the one that did not", path, amendment)
	}
}
