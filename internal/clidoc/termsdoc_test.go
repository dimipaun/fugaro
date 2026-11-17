package main

import (
	"os"
	"strings"
	"testing"
)

func TestWebsiteTermsPageIsCurrent(t *testing.T) {
	source, err := os.ReadFile("../../" + termsSource)
	if err != nil {
		t.Fatal(err)
	}
	want, err := renderTermsPage(source)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("../../" + termsPage)
	if err != nil {
		t.Fatalf("%s: %v (regenerate it with `go run ./internal/clidoc` at the repository root)", termsPage, err)
	}
	if string(got) != string(want) {
		t.Errorf("%s is not what %s generates: regenerate it with `go run ./internal/clidoc` at the repository root", termsPage, termsSource)
	}
}

// The page may only remove text: every line it keeps from the agreement is a
// line, or the start of a line, of the source.
func TestWebsiteTermsPageOnlyCutsText(t *testing.T) {
	source, err := os.ReadFile("../../" + termsSource)
	if err != nil {
		t.Fatal(err)
	}
	page, err := renderTermsPage(source)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.SplitN(string(page), "# Part A. ", 2)[1]
	src := string(source)
	for _, line := range strings.Split("# Part A. "+body, "\n") {
		if line == "" {
			continue
		}
		if !strings.Contains(src, line) {
			// A note cut from the middle of a paragraph joins the text either side of it.
			cut := false
			for _, part := range strings.SplitAfter(line, "**.") {
				if part != line && strings.Contains(src, strings.TrimSuffix(part, ".")) {
					cut = true
				}
			}
			if !cut {
				t.Errorf("%s carries a line its source does not: %q", termsPage, line)
			}
		}
	}
	for _, kept := range []string{"DSSD SOFTWARE INC", "bear interest at 12% per year", "US$100.", "# Part G. Legal terms for every plan", "## 34. Governing law, venue and language", termsStatus} {
		if !strings.Contains(string(page), kept) {
			t.Errorf("%s lost %q", termsPage, kept)
		}
	}
}

// The page carries the revision date when the source states one, because
// section 29.1 amends the agreement "by publishing a new dated version": a
// reader at the URL the licence pins has to be able to tell which revision they
// are accepting, and the source's own note is cut from the page.
//
// Both halves are pinned on fixtures rather than on the tree's source, because
// whether the Terms carry a revision is the document's business and not this
// generator's: today's source states none (the agreement is unchanged), and a
// revision of it will state one. What this test holds is that the generator
// reads a stated revision -- so the page says it -- and invents no date the
// source does not state.
func TestWebsiteTermsPageCarriesTheRevisionDate(t *testing.T) {
	statusLine := func(t *testing.T, src string) string {
		t.Helper()
		page, err := renderTermsPage([]byte(src))
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimPrefix(string(page), termsHeaderBlock), "\n")
		if len(lines) < 3 {
			t.Fatalf("the rendered page has no status line: %q", lines)
		}
		return lines[2]
	}

	revised := "# T\n\nwritten 2026-10-27\n\n**Revised 2026-11-17 (task S0.4).**\n\n# Part A. Plans\n\n## 1. A\n\nText.\n\n## 34. Z\n\nEnd.\n"
	if got, want := statusLine(t, revised), termsStatus+". Dated 2026-10-27. Revised 2026-11-17."; got != want {
		t.Errorf("a source that states a revision of 2026-11-17 renders the status line %q, and a reader accepting the agreement has to be told which revision it is", got)
	}

	plain := "# T\n\nwritten 2026-10-27\n\n# Part A. Plans\n\n## 1. A\n\nText.\n\n## 34. Z\n\nEnd.\n"
	if got := statusLine(t, plain); strings.Contains(got, "Revised") {
		t.Errorf("a source that states no revision renders one anyway: %q", got)
	}
}

func TestWebsiteTermsPageRefusesAnUnknownNote(t *testing.T) {
	src := "# T\n\nwritten 2026-10-27\n\n## 1. A\n\nText. (Note for counsel - a new shape.)\n\n## 34. Z\n\nEnd.\n\n---\n\n## Questions for the owner\n"
	if _, err := renderTermsPage([]byte(src)); err == nil {
		t.Fatal("renderTermsPage published a note it does not know how to cut")
	}
	src = strings.Replace(src, " (Note for counsel - a new shape.)", " [Note for counsel: cut\nme.]", 1)
	page, err := renderTermsPage([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(page), "cut") || strings.Contains(string(page), "Questions") || !strings.Contains(string(page), "End.\n") {
		t.Errorf("unexpected page:\n%s", page)
	}
}
