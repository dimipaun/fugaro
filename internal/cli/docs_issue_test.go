package cli

// The issues page, pinned where it can rot silently.
//
// docs/issues.md is the page a person reads to learn what the `fugaro issue`
// family does with the failures their machine hit. T-IS9 of
// docs/troubleshoot-issue/ writes the page and pins that it names every
// command; the section for `fugaro issue mine` is T-IM7's, and the facts that
// section alone carries are pinned here: which command and which alias list
// the rows, the two flags, the 30-day rule that hides the finished ones, the
// two local copies an offline run reads (the snapshot of the last shared read
// and the identity cache), and the one thing the command deliberately does
// not do — read GitHub (D1784 of docs/issue-mine/reconciliation.md). Each of
// these is a sentence a later edit can drop with nothing else in the tree
// noticing.
//
// Mutation (run and restored): take the 30-day sentence out -> the test fails
// naming "30 days"; reword the GitHub sentence -> fails naming "no GitHub";
// drop the alias -> fails naming "`fugaro issue list --mine`".

import (
	"strings"
	"testing"
)

const docsIssuePage = "docs/issues.md"

func TestDocsIssueNamesMine(t *testing.T) {
	page := flattenDoc(readDoc(t, docsIssuePage))
	for _, want := range []struct{ text, why string }{
		{"`fugaro issue mine`", "the command the section is about"},
		{"`fugaro issue list --mine`", "the alias, which is the same listing and must not drift from it"},
		{"`--all`", "the flag that shows the rows the 30-day rule hides"},
		{"`--json`", "the flag a tool reads the versioned document with"},
		{"30 days", "the rule the finished rows are hidden by"},
		{"snapshot", "the last successful shared read an offline run prints"},
		{"identity cache", "who the user is when no credential lookup can run"},
		{"no GitHub", "D1784: the command reads no GitHub issue state"},
	} {
		if !strings.Contains(page, want.text) {
			t.Errorf("%s never says %q (%s)", docsIssuePage, want.text, want.why)
		}
	}
}
