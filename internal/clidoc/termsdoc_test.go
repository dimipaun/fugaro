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

// The page carries the revision date, because section 29.1 amends the
// agreement "by publishing a new dated version": a reader at the URL the
// licence pins has to be able to tell which revision they are accepting, and
// the source's own note is cut from the page.
func TestWebsiteTermsPageCarriesTheRevisionDate(t *testing.T) {
	source, err := os.ReadFile("../../" + termsSource)
	if err != nil {
		t.Fatal(err)
	}
	revised := termsRevised.FindStringSubmatch(string(source))
	if revised == nil {
		t.Fatalf("%s carries no '**Revised YYYY-MM-DD' paragraph: the page then cannot say which revision of the agreement a reader accepts", termsSource)
	}
	page, err := renderTermsPage(source)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimPrefix(string(page), termsHeaderBlock), "\n")
	if len(lines) < 3 {
		t.Fatalf("%s has no status line: %q", termsPage, lines)
	}
	if status := lines[2]; !strings.Contains(status, "Revised "+revised[1]) {
		t.Errorf("%s is what %s generates, and its status line %q does not carry the revision %s", termsPage, termsSource, status, revised[1])
	}

	// A source with no revision renders none: a date the source does not state
	// would be this generator inventing a version.
	plain, err := renderTermsPage([]byte("# T\n\nwritten 2026-10-27\n\n## 1. A\n\nText.\n\n## 34. Z\n\nEnd.\n"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "Revised") {
		t.Errorf("a source with no revision renders one: %q", strings.TrimPrefix(string(plain), termsHeaderBlock))
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
