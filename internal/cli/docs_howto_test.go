package cli

import (
	"strings"
	"testing"
)

// The five operator how-tos the short setup page points at. docs/gcp-setup.md
// is capped at 60 lines (TestDocsGCPSetupStaysShort), so the topics the page
// before it carried live one file each under docs/how-to/ and the page links
// them. This is the pin that keeps the two halves in step: a page that leaves
// the list, or a list entry whose file is gone, is a dead end for the reader
// who followed it.
var gcpSetupHowTos = []string{
	"budgets.md",
	"registry-cleanup.md",
	"offboarding.md",
	"retiring-a-workflow.md",
	"teammates.md",
}

// TestDocsGCPSetupLinksEveryHowTo: the page is the short one, so each topic it
// dropped is reachable only through its own list.
func TestDocsGCPSetupLinksEveryHowTo(t *testing.T) {
	doc := readDoc(t, gcpSetupDoc)
	for _, name := range gcpSetupHowTos {
		link := "how-to/" + name
		if !strings.Contains(doc, "("+link+")") {
			t.Errorf("%s does not link %s: a topic the short page dropped has to be reachable from it", gcpSetupDoc, link)
		}
	}
}

// TestDocsHowToPagesAreShort: each page is a how-to and not a chapter, and
// every command it gives is labelled with the scope it is run at (the task's
// rule: a reader has to know whether to run it once for the installation or
// once per repository).
func TestDocsHowToPagesAreShort(t *testing.T) {
	for _, name := range gcpSetupHowTos {
		path := "docs/how-to/" + name
		doc := readDoc(t, path)
		if !strings.HasPrefix(doc, "# ") {
			t.Errorf("%s does not open with a title", path)
		}
		if n := strings.Count(doc, "\n"); n > 80 {
			t.Errorf("%s is %d lines: a how-to is short, and the detail belongs in the design it links", path, n)
		}
		if !strings.Contains(doc, "fugaro ") {
			t.Errorf("%s names no fugaro command, and it is a how-to", path)
		}
		lower := strings.ToLower(doc)
		if !strings.Contains(lower, "**per installation") && !strings.Contains(lower, "**per repository") {
			t.Errorf("%s labels no command per installation or per repository, so a reader cannot tell how often to run it", path)
		}
	}
}
