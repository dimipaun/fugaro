package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"
)

// TestDocsConsoleStylingDesignIsWritten:
// the design that gives the Electron console a stylesheet exists, carries its
// eleven sections, states in its own first line that nothing follows it, and
// keeps every claim that makes it reviewable — the evidence that the console
// ships no stylesheet at all, the screen inventory every rule is written
// against, the two token sets with their computed ratios, the state rule that
// no state is carried by colour alone, the single-switch theme, the wiring that
// is admitted by the CSP directive already in force, and the owner's six open
// questions. Its red state is this test: the document did not exist.
//
// The document is docs-only work: it changes no `.css`, no Go, no TypeScript
// and no entry of the handler's served-file list, which is why the first string
// below is the status line rather than a mood.
func TestDocsConsoleStylingDesignIsWritten(t *testing.T) {
	const path = "docs/design/console-styling.md"
	doc := readDoc(t, path)
	for _, want := range []string{
		// The status line, and the rule that nothing follows the document.
		"**Status: design only; nothing is implemented.**",
		"It does not change the CSP",
		"no `.css` file",
		// The sections, in order, as headings: a section that is renamed or
		// dropped must fail here rather than become a citation that dangles.
		"## 1. The problem, and the evidence",
		"## 2. Screen inventory",
		"## 3. The theme system",
		"## 4. Layout",
		"## 5. Wiring (SECURITY-SENSITIVE)",
		"## 6. Tests",
		"## 7. The build plan (Phase 6, UNAPPROVED)",
		"## 8. Open for the owner",
		"## 9. Alternatives considered",
		"## 10. Design brief for the Claude Design canvas",
		"## 11. What this document does not decide",
		// The evidence: no stylesheet, no served entry, no bundler step.
		"SERVED_FILES",
		"No inline style attribute",
		// The theme: two sets, the OS default, one switch, no colour alone.
		"prefers-color-scheme",
		"light-dark(",
		"Never colour alone",
		"KILLED by",
		"NOTIONAL~",
		"(partial)",
		"tabular-nums",
		"forced-colors",
		// The wiring, and the directive that already admits it.
		"is already the directive a stylesheet needs",
		"mediaTypeFor",
		"text/css; charset=utf-8",
		"console.css",
		// The alternatives, priced.
		"adoptedStyleSheets",
		// The plan, the owner, and the number this document does not take.
		"Task 6.2",
		"No D-number is allocated",
		"Q6",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s lacks %q", path, want)
		}
	}
}

// TestDocsConsoleStylingCitationsExist: the design's citations are paths a
// reader has to find — the palette the light set is derived from, the site's
// token layer and the contrast check the design's own test mirrors, and the
// documents it is read against. A substring assertion cannot see one of them
// move, so each is stat'ed here as well as required in the text.
//
// **Every citation this test reads is outside the console directory, and that
// is the boundary rather than a choice about value.** The design cites the
// console's own files by their full paths — the protocol handler it does not
// change, the screens its inventory was read from, the bundler that will emit
// the stylesheet, the four unit tests its wiring section extends — and this
// test does not stat them, because `TestConsoleIsOutsideTheGoModule`'s
// allow-list is a ONE-entry exception (Task 0.3 a3: the console contract's own
// docs test) and a second Go file mentioning a path under that directory would
// be a deliberate widening of it. Those citations are checked by the reader the
// design is written for, and the design says so in its own §6.5; the boundary
// is worth more than the stat.
//
// Deliberately not listed either: everything the design proposes and does not
// find — the four theme stylesheets, the token test and the golden-frame
// directory — because a file a design creates is not a file a design cites, and
// stat'ing one would make this test fail on the document doing its job.
func TestDocsConsoleStylingCitationsExist(t *testing.T) {
	const path = "docs/design/console-styling.md"
	doc := readDoc(t, path)
	for _, cited := range []string{
		// The palette the light set is derived from, the site's token layer,
		// and the contrast check the design's own token test is written in the
		// shape of (so the arithmetic is one implementation, not two).
		"website/src/styles/tokens.css",
		"website/src/styles/palette-logo.css",
		"website/test/a11y.mjs",
		// The documents it is read against.
		"docs/design/desktop-console.md",
		"docs/design/desktop-console-v2.md",
		"docs/plans/2026-10-15-desktop-console.md",
		"docs/website/redesign/reconciliation.md",
	} {
		// The document links its neighbours relatively (../plans/…,
		// ../../website/…), so a citation counts in either form.
		rel, err := filepath.Rel("docs/design", cited)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(doc, cited) && !strings.Contains(doc, rel) {
			t.Errorf("%s no longer cites %s: this list is the document's own citations, so one that has left the text must leave this test", path, cited)
			continue
		}
		if _, err := os.Stat("../../" + cited); err != nil {
			t.Errorf("%s cites %s, which is not in the tree: %v", path, cited, err)
		}
	}
}

// TestDocsConsoleStylingAnchorsResolve: every in-page link in the design
// (`](#…)`) resolves to a heading in the design.
//
// The gap this closes is the one the citation test above cannot: a citation test
// stats a *file*, and a dead `#fragment` is not a file. Three of the design's
// anchors were wrong when this test was added — two missing the second hyphen an
// em dash leaves in a GitHub slug (`— the wiring — SECURITY-SENSITIVE` slugs to
// `…the-wiring--security-sensitive`), and one left over from an earlier revision
// of §7.5 naming a heading that had been rewritten — and every one of them
// rendered as a link that went nowhere while the suite stayed green. A document
// whose own table of contents does not resolve is a document a reader has to
// trust rather than follow, and this design's whole shape is cross-references.
//
// The slug rule is GitHub's, as far as this document needs it: lowercase, drop
// everything that is not a letter, a digit, a space, a hyphen or an underscore,
// then spaces to hyphens. That is why an em dash leaves two hyphens behind
// rather than one.
func TestDocsConsoleStylingAnchorsResolve(t *testing.T) {
	const path = "docs/design/console-styling.md"
	doc := readDoc(t, path)

	slugs := map[string]int{}
	for _, line := range strings.Split(doc, "\n") {
		if !strings.HasPrefix(line, "#") {
			continue
		}
		heading := strings.TrimLeft(line, "#")
		if !strings.HasPrefix(heading, " ") {
			continue
		}
		slug := mdHeadingSlug(heading)
		if slug == "" {
			t.Errorf("%s: the heading %q slugs to the empty string, so nothing can link to it", path, line)
			continue
		}
		slugs[slug]++
	}
	for slug, n := range slugs {
		if n > 1 {
			t.Errorf("%s: %d headings slug to %q: GitHub disambiguates them with a `-1` suffix, so only the first is reachable by the name the document would write", path, n, slug)
		}
	}

	for _, m := range mdAnchorLink.FindAllStringSubmatch(doc, -1) {
		target := m[1]
		// An anchor to another document's section (`../plans/x.md#y`) is that
		// document's business, and this test does not read it.
		if strings.Contains(m[0], ".md#") {
			continue
		}
		if _, ok := slugs[target]; !ok {
			t.Errorf("%s: `%s` resolves to no heading: the link goes nowhere, and no citation test can see it because a fragment is not a file", path, m[0])
		}
	}
}

// mdAnchorLink finds the in-page links of a Markdown document: the URL of a
// `](#…)` link, which is the only form that has to resolve inside the file.
var mdAnchorLink = regexp.MustCompile(`\]\(#([^)\s]+)\)`)

// mdHeadingSlug is GitHub's heading-slug rule, as far as a document of this
// repository's shape needs it: lowercase, keep letters, digits, spaces, hyphens
// and underscores, drop everything else, then turn spaces into hyphens. An
// em dash is dropped and the spaces on either side of it become two hyphens,
// which is the detail the three links this test was written for got wrong.
func mdHeadingSlug(heading string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(heading)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteRune('-')
		}
	}
	return b.String()
}

// TestDocsConsoleStylingPlanTasksAreUnapproved (the console plan's phase 6,
// added 2026-10-19 with the design above):
// the plan carries the styling work in its own numbering and carries it as
// UNAPPROVED. Two things have to hold together and neither is visible from the
// other — the six tasks exist as headings, and the file says in the phase's own
// heading, its summary row and its PR rows that the owner has not approved any
// of them. The wiring task's `SECURITY-SENSITIVE` flag is checked by
// TestPlanRule9ListsEveryFlaggedHeading, which is why this test does not repeat
// rule 9's list; what it adds is that the flag is on a task that *exists* in a
// phase that *says* it is unapproved.
func TestDocsConsoleStylingPlanTasksAreUnapproved(t *testing.T) {
	const path = "docs/plans/2026-10-15-desktop-console.md"
	plan := readDoc(t, path)
	for _, want := range []string{
		// The phase, in the file's numbering, marked unapproved in its heading.
		"## Phase 6 — The console's style — UNAPPROVED",
		// The six tasks. 6.2's heading is the flagged one.
		"### Task 6.1: The token layer",
		"### Task 6.2: The wiring — SECURITY-SENSITIVE, independent opus review",
		"### Task 6.3: The layout",
		"### Task 6.4: The screens",
		"### Task 6.5: The theme switch, the window background and the keyboard",
		"### Task 6.6: The golden frames, re-recorded",
		// The four stylesheets, in the tree the plan promised from before this
		// design existed — the file the tree named and no task created.
		"src/renderer/theme/tokens.css",
		"src/renderer/theme/palette.css",
		"src/renderer/theme/base.css",
		"src/renderer/theme/screens.css",
		// The authority the phase defers to, and the gate it does not pass.
		"docs/design/console-styling.md",
		"These six tasks are `UNAPPROVED`",
		"OWNER APPROVAL GATE",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("%s lacks %q: phase 6's tasks are the styling design's build plan, and a task that leaves the plan must leave this test", path, want)
		}
	}

	// The design's six questions, named where a reader of the plan finds them:
	// a question answered by silently dropping it would be a task that starts
	// without the gate it names.
	section := func(start, end string) string {
		i := strings.Index(plan, start)
		if i < 0 {
			t.Fatalf("plan has no %q", start)
		}
		rest := plan[i:]
		if j := strings.Index(rest[len(start):], end); j >= 0 {
			return rest[:len(start)+j]
		}
		return rest
	}("## Phase 6 — The console's style — UNAPPROVED", "\n## Cross-task dependencies")
	for _, q := range []string{"**Q1**", "**Q2**", "**Q3**", "**Q4**", "**Q5**", "**Q6**"} {
		if !strings.Contains(section, q) {
			t.Errorf("%s: phase 6 no longer names %s among the questions the owner decides before it starts", path, q)
		}
	}
	// And the rule that keeps it out of the first release until the owner says
	// otherwise — the design's recommendation, stated in the plan itself.
	if !strings.Contains(plan, "**Phase 6 — the console's style — is not on this list**") {
		t.Errorf("%s: the MVP list no longer says phase 6 is not on it, which is the design's recommendation (its Q6) and would otherwise read as an omission", path)
	}
}
