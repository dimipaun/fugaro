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
	body := strings.SplitN(string(page), "## 1. ", 2)[1]
	src := string(source)
	for _, line := range strings.Split("## 1. "+body, "\n") {
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
	for _, kept := range []string{"[LICENSOR LEGAL NAME]", "[RATE TO BE SET BY COUNSEL]", "[PRICE PER USER SEAT: Order Form]", "**[Placeholder.]**", "## 24. Governing law and venue", termsStatus} {
		if !strings.Contains(string(page), kept) {
			t.Errorf("%s lost %q", termsPage, kept)
		}
	}
}

func TestWebsiteTermsPageRefusesAnUnknownNote(t *testing.T) {
	src := "# T\n\nwritten 2026-10-27\n\n## 1. A\n\nText. (Note for counsel - a new shape.)\n\n## 24. Z\n\nEnd.\n\n---\n\n## Questions for the owner\n"
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
