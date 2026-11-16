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
// Mutation (run and restored): take the 30-day rule sentence out -> the test
// fails naming "more than 30 days old is hidden" (the section heading above it
// says "30 days" and does not stand in for the rule); reword the GitHub
// sentence -> fails naming "no GitHub"; drop the alias -> fails naming
// "`fugaro issue list --mine`". For the family test, take `sync` out of the
// page -> it fails naming `fugaro issue sync`, and drop `submit`'s `--ask` ->
// it fails naming that flag; nothing else in the tree reads either.

import (
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

const docsIssuePage = "docs/issues.md"

// TestDocsIssueNamesEveryCommand: the page is the family's own reference, so it
// names every command `fugaro issue` serves and every flag those commands
// carry. The list comes off the tree rather than out of the page, because the
// mistake this catches is a command or a flag added to the family and never
// written down: a page checked against itself names whatever its author
// remembered. The command's own flags are the ones checked -- `--verbose` is
// the root's, on every command of the CLI, and is documented where the rest of
// the global flags are; cobra's `--help` is cobra's.
func TestDocsIssueNamesEveryCommand(t *testing.T) {
	issue, _, err := NewRootCmd().Find([]string{"issue"})
	if err != nil || issue == nil || issue.Name() != "issue" {
		t.Fatalf("the tree carries no `fugaro issue` command (%v): this test's whole subject is missing", err)
	}
	subs := issue.Commands()
	if len(subs) == 0 {
		t.Fatal("`fugaro issue` carries no subcommand, so this test would pass on an empty page")
	}
	page := flattenDoc(readDoc(t, docsIssuePage))
	for _, sub := range subs {
		path := "fugaro issue " + sub.Name()
		if !strings.Contains(page, path) {
			t.Errorf("%s never names `%s`, so a reader looking for that command finds no page for it", docsIssuePage, path)
		}
		sub.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if f.Name == "help" {
				return
			}
			if !strings.Contains(page, "--"+f.Name) {
				t.Errorf("%s never names `--%s`, a flag of `%s`: a reader cannot find out the command takes it", docsIssuePage, f.Name, path)
			}
		})
	}
}

func TestDocsIssueNamesMine(t *testing.T) {
	page := flattenDoc(readDoc(t, docsIssuePage))
	for _, want := range []struct{ text, why string }{
		{"`fugaro issue mine`", "the command the section is about"},
		{"`fugaro issue list --mine`", "the alias, which is the same listing and must not drift from it"},
		{"`--all`", "the flag that shows the rows the 30-day rule hides"},
		{"`--json`", "the flag a tool reads the versioned document with"},
		{"more than 30 days old is hidden", "the rule the finished rows are hidden by, which the section heading alone does not carry"},
		{"snapshot", "the last successful shared read an offline run prints"},
		{"identity cache", "who the user is when no credential lookup can run"},
		{"no GitHub", "D1784: the command reads no GitHub issue state"},
	} {
		if !strings.Contains(page, want.text) {
			t.Errorf("%s never says %q (%s)", docsIssuePage, want.text, want.why)
		}
	}
}
