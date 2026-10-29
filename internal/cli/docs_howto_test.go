package cli

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The five operator how-tos the short setup page points at. docs/gcp-setup.md
// is capped at 60 lines (TestDocsGCPSetupStaysShort), so the topics the page
// before it carried live one file each under docs/how-to/ and the page links
// them. The list is what the pin below walks in both directions: a page that
// leaves it, or a list entry whose file is gone, is a dead end for the reader
// who followed it.
var gcpSetupHowTos = []string{
	"budgets.md",
	"registry-cleanup.md",
	"offboarding.md",
	"retiring-a-workflow.md",
	"teammates.md",
}

// TestDocsGCPSetupLinksEveryHowTo: the page is the short one, so each topic it
// dropped is reachable only through its own list — and a page in docs/how-to/
// that no list names is reachable from nowhere at all, which the directory
// walk is here to catch.
func TestDocsGCPSetupLinksEveryHowTo(t *testing.T) {
	doc := readDoc(t, gcpSetupDoc)
	for _, name := range gcpSetupHowTos {
		link := "how-to/" + name
		if !strings.Contains(doc, "("+link+")") {
			t.Errorf("%s does not link %s: a topic the short page dropped has to be reachable from it", gcpSetupDoc, link)
		}
	}
	pages, err := filepath.Glob(filepath.Join("..", "..", "docs", "how-to", "*.md"))
	if err != nil {
		t.Fatalf("globbing docs/how-to: %v", err)
	}
	if len(pages) == 0 {
		t.Fatalf("docs/how-to holds no pages, and %s links %d of them", gcpSetupDoc, len(gcpSetupHowTos))
	}
	for _, page := range pages {
		if name := filepath.Base(page); !slices.Contains(gcpSetupHowTos, name) {
			t.Errorf("docs/how-to/%s is in no list here and linked from nowhere: the setup page cannot reach it", name)
		}
	}
}

// TestDocsHowToPagesAreShort: each page is a how-to and not a chapter, and the
// commands in it say what they are run at. Two things about the scope check,
// stated because its name is wider than its reach: it is a floor per page (a
// page that labels one command anywhere passes it), and the per-section half
// reads fenced ```bash blocks only — a command written into a sentence, as
// budgets.md's all are, is not read.
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
		if !scopeLabelled(doc) {
			t.Errorf("%s labels no command per installation or per repository, so a reader cannot tell how often to run it", path)
		}
		for _, section := range docSections(doc) {
			for _, block := range bashBlocks(section) {
				if len(block) == 0 || !runsFugaro(block) || scopeLabelled(section) {
					continue
				}
				t.Errorf("%s runs %q under a heading that labels no scope: a reader cannot tell whether to run it once for the installation or once per repository", path, block[0])
			}
		}
	}
}

// scopeLabelled reports whether text labels a command with the scope it is run at.
func scopeLabelled(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "**per installation") || strings.Contains(lower, "**per repository")
}

// runsFugaro reports whether a fenced block runs a fugaro command.
func runsFugaro(block []string) bool {
	for _, line := range block {
		if strings.Contains(line, "fugaro ") {
			return true
		}
	}
	return false
}

// docSections splits a document at its "## " headings, fence-aware the way
// docSection is (a "#" line inside a fence is a comment, not a heading), with
// the text before the first heading as the first section.
func docSections(doc string) []string {
	var out []string
	cur := []string{}
	fenced := false
	for _, line := range strings.Split(doc, "\n") {
		switch {
		case isFence(line):
			fenced = !fenced
		case !fenced && headingLevel(line) == 2:
			out = append(out, strings.Join(cur, "\n"))
			cur = nil
		}
		cur = append(cur, line)
	}
	return append(out, strings.Join(cur, "\n"))
}
