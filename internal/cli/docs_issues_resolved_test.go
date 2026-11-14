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
