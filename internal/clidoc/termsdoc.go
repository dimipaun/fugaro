package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// The Subscription Terms page is rendered from the draft the licence files
// point at. The draft carries more than the agreement: a banner, a reading
// guide, drafting notes for counsel and a question list. The page carries
// the agreement text alone, so this generator cuts everything else out
// mechanically rather than leaving a hand-trimmed copy to drift.
const (
	termsSource = "docs/licensing/subscription-terms.md"
	termsPage   = "website/content/subscription-terms.md"

	// termsStatus is the line the page shows above the agreement. It named a
	// draft until counsel approved the text (the owner's word of 2026-10-30),
	// and counsel approved section 7.3 as revised to the opt-in ruling of
	// 2026-10-31 (the owner's word of 2026-10-31).
	termsStatus = "Approved by counsel"
)

var termsHeaderBlock = securityHeader + "\n<!-- source: " + termsSource + " -->\n\n"

var (
	termsTitle      = regexp.MustCompile(`(?m)^# .+$`)
	termsDate       = regexp.MustCompile(`\bwritten (\d{4}-\d{2}-\d{2})\b`)
	termsFirst      = regexp.MustCompile(`(?m)^(# Part A\. |## 1\. )`)
	termsLast       = regexp.MustCompile(`(?m)^## 34\. .*$`)
	termsNextHead   = regexp.MustCompile(`(?m)^(---|## )`)
	termsInlineNote = regexp.MustCompile(`[ \t\n]*(\*\*)?\[Note for counsel:[^\]]*\](\*\*)?`)
	termsLeadNote   = regexp.MustCompile(`[ \t]*\*\*Note for counsel:\*\*(?s:.*)$`)
	termsTrailing   = regexp.MustCompile(`(?m)[ \t]+$`)
)

// termsLeftovers are markers of drafting text. Any of them in the output
// means the source grew a note in a shape the cuts below do not know, and the
// generator stops rather than publish it.
var termsLeftovers = []string{"Note for counsel", "Owner choice", "Questions for", "DRAFT FOR COUNSEL", "\n> "}

func renderTermsPage(source []byte) ([]byte, error) {
	src := string(source)

	title := termsTitle.FindString(src)
	if title == "" {
		return nil, fmt.Errorf("%s has no '# ' title", termsSource)
	}
	date := termsDate.FindStringSubmatch(src)
	if date == nil {
		return nil, fmt.Errorf("%s does not say when it was written ('written YYYY-MM-DD')", termsSource)
	}
	start := termsFirst.FindStringIndex(src)
	last := termsLast.FindStringIndex(src)
	if start == nil || last == nil || last[0] < start[0] {
		return nil, fmt.Errorf("%s has no sections 1 to 34", termsSource)
	}
	end := len(src)
	if next := termsNextHead.FindStringIndex(src[last[1]:]); next != nil {
		end = last[1] + next[0]
	}

	var kept []string
	for _, block := range strings.Split(src[start[0]:end], "\n\n") {
		block = strings.Trim(block, "\n")
		if block == "" || strings.HasPrefix(block, "> **Note for counsel") {
			continue
		}
		block = termsInlineNote.ReplaceAllString(block, "")
		block = termsLeadNote.ReplaceAllString(block, "")
		kept = append(kept, block)
	}
	body := termsTrailing.ReplaceAllString(strings.Join(kept, "\n\n"), "")

	page := termsHeaderBlock + title + "\n\n" + termsStatus + ". Dated " + date[1] + ".\n\n" + body + "\n"
	for _, marker := range termsLeftovers {
		if strings.Contains(page, marker) {
			return nil, fmt.Errorf("%s: the agreement text still carries %q after the drafting notes were cut; teach renderTermsPage the new shape", termsSource, marker)
		}
	}
	return []byte(page), nil
}

func writeTermsPage(root string) (bool, error) {
	source, err := os.ReadFile(filepath.Join(root, termsSource))
	if err != nil {
		return false, fmt.Errorf("read %s: %w (run this from the repository root)", termsSource, err)
	}
	page, err := renderTermsPage(source)
	if err != nil {
		return false, err
	}
	out := filepath.Join(root, termsPage)
	if old, err := os.ReadFile(out); err == nil && bytes.Equal(old, page) {
		return false, nil
	}
	if err := os.WriteFile(out, page, 0o644); err != nil {
		return false, err
	}
	return true, nil
}
