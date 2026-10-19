package cli

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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

// --- the computed numbers of §3.4 -----------------------------------------
//
// The design's §3.4 is a table of numbers, and a number in a document rots the
// way a link does — silently, and in the direction of whatever the sentence
// beside it needs. The first revision of the design is the evidence: it
// asserted that the five state colours were "≥ 60° apart from each other in
// both sets", and that was false in both sets (the dark set's `--warn`/`--error`
// pair was 37.0° apart, its `--halted`/`--stale` pair 55.0°), while every
// contrast figure beside it was right.
//
// So the four tests below recompute §3.4 from the literal values in §3.2 and
// §3.3 with WCAG 2.1's relative-luminance definition, the same arithmetic
// `website/test/a11y.mjs` uses; assert the requirement §3.4 states for each
// token's role; and check §3.2's accounting of which tokens are new against the
// website's palette file rather than against the sentence. **A literal changed
// in §3.2 or §3.3, and the number printed beside it left alone, fails here** —
// which is the red step for all three findings of the review this test was
// written against.
//
// The arithmetic is written out rather than shared, because it is four lines
// and the point is that the test and the document agree on the formula. It is
// deliberately not the console's own token test (§6.3): that one is TypeScript
// on the other side of TestConsoleIsOutsideTheGoModule's one-entry allow-list,
// which is what keeps a Go file from reaching in there at all — the same
// boundary that makes this file cite the console's files as text and never stat
// them. A path under that directory written into a comment here is enough to
// fail that test, which is how this paragraph came to be rewritten.

// mdRGB parses a six-digit sRGB hex literal into its three channel values.
func mdRGB(t *testing.T, hex string) [3]float64 {
	t.Helper()
	if len(hex) != 7 || hex[0] != '#' {
		t.Fatalf("not a six-digit hex literal: %q", hex)
	}
	var out [3]float64
	for i := range out {
		v, err := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
		if err != nil {
			t.Fatalf("not a six-digit hex literal: %q", hex)
		}
		out[i] = float64(v)
	}
	return out
}

// mdLinear is one sRGB channel's linear value, the transfer function WCAG 2.1
// defines relative luminance over.
func mdLinear(c float64) float64 {
	c /= 255
	if c <= 0.03928 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

// mdLum is WCAG 2.1's relative luminance of a six-digit hex literal.
func mdLum(t *testing.T, hex string) float64 {
	t.Helper()
	c := mdRGB(t, hex)
	return 0.2126*mdLinear(c[0]) + 0.7152*mdLinear(c[1]) + 0.0722*mdLinear(c[2])
}

// mdRatio is the WCAG contrast ratio between two literals, lighter one first:
// (L1+0.05)/(L2+0.05).
func mdRatio(t *testing.T, a, b string) float64 {
	t.Helper()
	la, lb := mdLum(t, a), mdLum(t, b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// mdHue is the HSL hue of a six-digit sRGB hex literal, in degrees. A neutral
// has no hue and returns 0; the document never asks for one, because the one
// neutral in either set is `--surface #FFFFFF`, which is only ever a surface.
func mdHue(t *testing.T, hex string) float64 {
	t.Helper()
	c := mdRGB(t, hex)
	r, g, b := c[0]/255, c[1]/255, c[2]/255
	max := math.Max(r, math.Max(g, b))
	min := math.Min(r, math.Min(g, b))
	d := max - min
	if d == 0 {
		return 0
	}
	var h float64
	switch max {
	case r:
		h = 60 * math.Mod((g-b)/d, 6)
	case g:
		h = 60 * ((b-r)/d + 2)
	default:
		h = 60 * ((r-g)/d + 4)
	}
	if h < 0 {
		h += 360
	}
	return h
}

// mdHueGap is the circular distance between two hues in degrees — the smaller
// of |Δh| and 360−|Δh| — which is the distance §3.4's pair tables are about.
func mdHueGap(t *testing.T, a, b string) float64 {
	t.Helper()
	d := math.Abs(mdHue(t, a) - mdHue(t, b))
	if d > 180 {
		d = 360 - d
	}
	return d
}

// mdCut splits s at the given markers, which must appear in that order, and
// returns the pieces between them (one more piece than markers). A marker that
// is missing or out of order fails rather than silently slicing the wrong
// region: these markers are the sub-headings and table captions of one section,
// and a section that has been reordered is exactly what this would otherwise
// misread.
func mdCut(t *testing.T, path, s string, marks ...string) []string {
	t.Helper()
	out := make([]string, 0, len(marks)+1)
	off := 0
	for _, m := range marks {
		i := strings.Index(s[off:], m)
		if i < 0 {
			t.Fatalf("%s: %q is missing, or out of order, in the section being read", path, m)
		}
		out = append(out, s[off:off+i])
		off += i + len(m)
	}
	return append(out, s[off:])
}

// The four table shapes §3.2–§3.4 are written in: a value-set row, a
// three-surface contrast row, the decorative `--border` row (whose ratios are
// deliberately not bold, because they are deliberately not admissible), the
// ink-on-fill row, and a state-pair row.
var (
	mdTokenRowRE   = regexp.MustCompile("(?m)^\\| `(--[a-z-]+)` \\| `(#[0-9A-F]{6})` \\|")
	mdSurfaceRowRE = regexp.MustCompile(
		"(?m)^\\| `(--[a-z-]+)` \\| \\*\\*([0-9.]+)\\*\\* \\| \\*\\*([0-9.]+)\\*\\* \\| \\*\\*([0-9.]+)\\*\\* \\|")
	mdDecorativeRowRE = regexp.MustCompile(
		"(?m)^\\| `(--border)` \\(decorative\\) \\| ([0-9.]+) \\| ([0-9.]+) \\| ([0-9.]+) \\|")
	mdFillRowRE = regexp.MustCompile(
		"(?m)^\\| `(--[a-z-]+)` \\| \\*\\*([0-9.]+)\\*\\* \\| \\*\\*([0-9.]+)\\*\\* \\|")
	mdPairRowRE = regexp.MustCompile(
		"(?m)^\\| `(--[a-z-]+)` / `(--[a-z-]+)` \\| ([0-9.]+)° \\| ([0-9.]+) \\|")
)

// mdTokens reads a value set out of its §3.2/§3.3 table: the rows that pair a
// token name with its opaque six-digit literal. A name declared twice with two
// different values fails, because the rest of these tests would otherwise pick
// whichever row came last and be right by accident.
func mdTokens(t *testing.T, path, section string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, m := range mdTokenRowRE.FindAllStringSubmatch(section, -1) {
		if prev, dup := out[m[1]]; dup && prev != m[2] {
			t.Errorf("%s: %s is declared twice with different values (%s and %s)", path, m[1], prev, m[2])
		}
		out[m[1]] = m[2]
	}
	if len(out) == 0 {
		t.Fatalf("%s: no token rows in the section being read; its table was renamed or reshaped and this test needs updating", path)
	}
	return out
}

// mdStylingDoc returns the document and its four load-bearing regions: the two
// value sets and §3.4's tables. Every test below starts here rather than
// re-finding the same markers.
func mdStylingDoc(t *testing.T) (doc string, dark, light map[string]string, contrast string) {
	t.Helper()
	const path = "docs/design/console-styling.md"
	doc = readDoc(t, path)
	theme := mdCut(t, path, doc,
		"### 3.2 The dark value set",
		"### 3.3 The light value set",
		"### 3.4 Contrast, computed",
		"### 3.5 Never colour alone")
	return doc, mdTokens(t, path, theme[1]), mdTokens(t, path, theme[2]), theme[3]
}

// mdSurfaceOrder is the three painted backgrounds every token is measured
// against, in the column order §3.4 prints them.
var mdSurfaceOrder = []string{"--ground", "--surface", "--surface-alt"}

// mdStateOrder is the five state colours, in the order §3.4's pair tables list
// them: the order the table is written in, so a pair dropped from it is a pair
// the completeness check below can name.
var mdStateOrder = []string{"--ok", "--warn", "--error", "--halted", "--stale"}

// TestDocsConsoleStylingContrastComputes: every ratio §3.4 prints is the ratio
// the literal values in §3.2 and §3.3 compute, to two decimals; every token
// clears the requirement its role carries (4.5:1 for body text, 3:1 for a
// control's boundary); and `--border` is still below 3:1, because §3.4 prints
// it as decorative and a hairline that cleared 3:1 would be a boundary the
// design should have named as one.
func TestDocsConsoleStylingContrastComputes(t *testing.T) {
	const path = "docs/design/console-styling.md"
	doc, dark, light, contrast := mdStylingDoc(t)

	parts := mdCut(t, path, contrast,
		"**Dark**", "**Light**", "**A label on a filled control**",
		"**State separation, computed.**",
		"**Dark: the ten pairs of state colours**",
		"**Light: the ten pairs of state colours**",
		"Circular hue distance")
	darkSurfaces, lightSurfaces, inkTable := parts[1], parts[2], parts[3]

	// The two surface tables, printed ratio by printed ratio.
	for _, tc := range []struct {
		set, name, table string
		tokens           map[string]string
	}{
		{"the dark set", "dark", darkSurfaces, dark},
		{"the light set", "light", lightSurfaces, light},
	} {
		rows := mdSurfaceRowRE.FindAllStringSubmatch(tc.table, -1)
		if len(rows) != 10 {
			t.Fatalf("%s: §3.4's %s table has %d token rows, want 10 (every token but the four that are only ever text on a fill)", path, tc.name, len(rows))
		}
		for _, m := range rows {
			name := m[1]
			hex, declared := tc.tokens[name]
			if !declared {
				t.Errorf("%s: §3.4's %s table prints %s, which §3.2/§3.3 does not declare", path, tc.name, name)
				continue
			}
			for i, surface := range mdSurfaceOrder {
				got, err := strconv.ParseFloat(m[2+i], 64)
				if err != nil {
					t.Fatalf("%s: %s on %s: %v", path, name, surface, err)
				}
				if want := mdRatio(t, hex, tc.tokens[surface]); math.Abs(got-want) > 0.005 {
					t.Errorf("%s: §3.4 prints %s on %s as %s:1, and the literal %s computes %.2f:1", path, name, surface, m[2+i], hex, want)
				}
			}
		}

		// The decorative row, printed unbolded and checked all the same.
		dec := mdDecorativeRowRE.FindStringSubmatch(tc.table)
		if dec == nil {
			t.Fatalf("%s: §3.4's %s table no longer carries a `--border (decorative)` row", path, tc.name)
		}
		for i, surface := range mdSurfaceOrder {
			got, err := strconv.ParseFloat(dec[2+i], 64)
			if err != nil {
				t.Fatalf("%s: --border on %s: %v", path, surface, err)
			}
			want := mdRatio(t, tc.tokens["--border"], tc.tokens[surface])
			if math.Abs(got-want) > 0.005 {
				t.Errorf("%s: §3.4 prints --border on %s as %s:1, and the literal computes %.2f:1", path, surface, dec[2+i], want)
			}
			if want >= 3 {
				t.Errorf("%s: the %s set's --border on %s is %.2f:1; §3.4 prints it as decorative, and a border that clears 3:1 is a control's boundary and must be said to be one", path, tc.name, surface, want)
			}
		}

		// The requirements, from the computed ratios rather than the printed
		// ones: a hex moved in §3.2 or §3.3 fails here even when the table has
		// been updated to match it.
		for _, token := range []string{"--text", "--text-muted", "--accent-ink", "--accent",
			"--ok", "--warn", "--error", "--halted", "--stale"} {
			for _, surface := range mdSurfaceOrder {
				if r := mdRatio(t, tc.tokens[token], tc.tokens[surface]); r < 4.5 {
					t.Errorf("%s: the %s's %s on %s is %.2f:1, and 4.5:1 is what body-size text needs", path, tc.name, token, surface, r)
				}
			}
		}
		for _, token := range []string{"--accent", "--border-strong"} {
			for _, surface := range mdSurfaceOrder {
				if r := mdRatio(t, tc.tokens[token], tc.tokens[surface]); r < 3 {
					t.Errorf("%s: the %s's %s on %s is %.2f:1, and 3:1 is what a boundary marking a control's extent needs", path, tc.name, token, surface, r)
				}
			}
		}
	}

	// The ink-on-fill table: the ink is the dark set's `--ground` and the light
	// set's `--surface`, which is what §3.4 says it is, and every fill clears
	// 4.5:1 against it because a label on a fill is text.
	fills := mdFillRowRE.FindAllStringSubmatch(inkTable, -1)
	if len(fills) != 6 {
		t.Fatalf("%s: §3.4's ink-on-fill table has %d rows, want 6 (the accent and the five states)", path, len(fills))
	}
	for _, m := range fills {
		name := m[1]
		for i, tc := range []struct {
			set  map[string]string
			ink  string
			name string
		}{
			{dark, dark["--ground"], "`--ground`"},
			{light, light["--surface"], "`--surface`"},
		} {
			got, err := strconv.ParseFloat(m[2+i], 64)
			if err != nil {
				t.Fatalf("%s: --ink-on-fill on %s: %v", path, name, err)
			}
			want := mdRatio(t, tc.ink, tc.set[name])
			if math.Abs(got-want) > 0.005 {
				t.Errorf("%s: §3.4 prints the light/dark ink on %s as %s:1, and %s on it computes %.2f:1", path, name, m[2+i], tc.ink, want)
			}
			if want < 4.5 {
				t.Errorf("%s: --ink-on-fill on --%s is %.2f:1, and a label is text", path, strings.TrimPrefix(name, "--"), want)
			}
		}
	}

	// The rule §3.4 states, pinned to the number this test computes with: the
	// two cannot drift apart silently.
	if !strings.Contains(doc, "**Any two of the five state colours are at least 50° apart in hue.**") {
		t.Errorf("%s: §3.4 no longer states the 50° minimum this test checks the state colours against", path)
	}
}

// TestDocsConsoleStylingStateSeparation: the ten pairs of state colours in each
// set, as §3.4 prints them, are the ten pairs the literals make — the hue
// distance to a tenth of a degree, the luminance contrast to two decimals — and
// every one of them clears the 50° minimum the section states. This is the
// claim the first revision of the design got wrong, and the reason it is a
// table of computed numbers now rather than a sentence.
func TestDocsConsoleStylingStateSeparation(t *testing.T) {
	const path = "docs/design/console-styling.md"
	_, dark, light, contrast := mdStylingDoc(t)
	parts := mdCut(t, path, contrast,
		"**Dark**", "**Light**", "**A label on a filled control**",
		"**State separation, computed.**",
		"**Dark: the ten pairs of state colours**",
		"**Light: the ten pairs of state colours**",
		"Circular hue distance")
	for _, tc := range []struct {
		set   map[string]string
		name  string
		table string
	}{
		{dark, "dark", parts[5]},
		{light, "light", parts[6]},
	} {
		rows := mdPairRowRE.FindAllStringSubmatch(tc.table, -1)
		if len(rows) != 10 {
			t.Fatalf("%s: the %s set's pair table has %d rows, want 10: the five state colours have ten pairs, and a pair dropped from the table is a pair this test would stop checking", path, tc.name, len(rows))
		}
		seen := map[string]bool{}
		for _, m := range rows {
			a, b := m[1], m[2]
			if _, ok := tc.set[a]; !ok {
				t.Errorf("%s: the %s pair table names %s, which §3.2/§3.3 does not declare", path, tc.name, a)
			}
			if _, ok := tc.set[b]; !ok {
				t.Errorf("%s: the %s pair table names %s, which §3.2/§3.3 does not declare", path, tc.name, b)
			}
			seen[a+" "+b] = true

			gap := mdHueGap(t, tc.set[a], tc.set[b])
			if got, err := strconv.ParseFloat(m[3], 64); err != nil {
				t.Fatalf("%s: %s/%s: %v", path, a, b, err)
			} else if math.Abs(got-gap) > 0.05 {
				t.Errorf("%s: the %s pair table prints %s / %s as %.1f° apart, and the literals compute %.1f°", path, tc.name, a, b, got, gap)
			}
			if gap < 50 {
				t.Errorf("%s: the %s set's %s and %s are %.1f° apart in hue, under the 50° minimum §3.4 states: change the token, not the rule", path, tc.name, a, b, gap)
			}

			want := mdRatio(t, tc.set[a], tc.set[b])
			if got, err := strconv.ParseFloat(m[4], 64); err != nil {
				t.Fatalf("%s: %s/%s: %v", path, a, b, err)
			} else if math.Abs(got-want) > 0.005 {
				t.Errorf("%s: the %s pair table prints %s / %s at %s:1 luminance contrast, and the literals compute %.2f:1", path, tc.name, a, b, m[4], want)
			}
		}
		for i, a := range mdStateOrder {
			for _, b := range mdStateOrder[i+1:] {
				if !seen[a+" "+b] {
					t.Errorf("%s: the %s pair table omits %s / %s", path, tc.name, a, b)
				}
			}
		}
	}
}

// TestDocsConsoleStylingTokenProvenance: §3.2's accounting of which tokens are
// new is checked against the website's palette file, not against the sentence.
// The first revision said "the six tokens the console needs added: the second
// surface, the control border and the five state colours" and named the wrong
// six — `--warn` is the website's, so there are seven values reused, one reused
// under another name and six new. Reading palette-logo.css is what makes the
// words "reused" and "new" checkable at all; without it they are a claim about
// a file nothing in this repository reads.
func TestDocsConsoleStylingTokenProvenance(t *testing.T) {
	_, dark, _, _ := mdStylingDoc(t)
	site := readDoc(t, "website/src/styles/palette-logo.css")

	// The seven the console copies from the website, byte for byte.
	for name, hex := range map[string]string{
		"--ground":     "#0A0E16",
		"--surface":    "#121A26",
		"--text":       "#E6EEF5",
		"--text-muted": "#9FB2C4",
		"--accent":     "#17A8E6",
		"--border":     "#24303F",
		"--warn":       "#F5A524",
	} {
		if dark[name] != hex {
			t.Errorf("%s: §3.2 declares %s as %q, want %q, the website's value", "docs/design/console-styling.md", name, dark[name], hex)
		}
		if !strings.Contains(site, name+": "+hex+";") {
			t.Errorf("palette-logo.css no longer declares %s: %s; §3.2 reuses it byte for byte, and that reuse is the claim this test checks", name, hex)
		}
	}

	// The one the console reuses under another name: the website names the
	// accent-as-text tone for its one ground, and the console's two grounds make
	// that name wrong here.
	if ink := dark["--accent-ink"]; !strings.Contains(site, "--accent-on-dark: "+ink+";") {
		t.Errorf("palette-logo.css's --accent-on-dark is not %s, the value §3.2's --accent-ink reuses under its new name", ink)
	}

	// The six the console invents: none of their literals may already be in the
	// website palette, or §3.2's word "new" is wrong about that one.
	for _, name := range []string{"--surface-alt", "--border-strong", "--ok", "--error", "--halted", "--stale"} {
		hex := dark[name]
		if hex == "" {
			t.Errorf("docs/design/console-styling.md: §3.2 declares no %s", name)
			continue
		}
		if strings.Contains(site, hex) {
			t.Errorf("docs/design/console-styling.md: §3.2 calls %s (%s) new, and palette-logo.css already carries that literal", name, hex)
		}
	}

	// And the count the accounting adds up to, so a fifteenth token cannot be
	// added to the table without the sentence being revisited.
	if n := len(dark); n != 14 {
		t.Errorf("docs/design/console-styling.md: §3.2 declares %d tokens; the section's accounting is seven reused, one renamed and six new, which is fourteen", n)
	}
}

// TestDocsConsoleStylingAccentOnALightGround: §3.3's correction is a claim
// about two different backgrounds, and both are computed here. `#17A8E6` is
// 2.70:1 on **white** — the figure reconciliation §5.3 records, and the figure
// the first revision of §3.3 attributed to "a light ground" in general — and
// 2.49:1 on this set's own `--ground` `#F2F6FA`, which is the number this
// design actually has to answer for, and 2.31:1 on `--surface-alt`. The prose
// is pinned to the two figures and to the tone the design names in their place,
// because the whole finding was that the right number was in the wrong place.
func TestDocsConsoleStylingAccentOnALightGround(t *testing.T) {
	const path = "docs/design/console-styling.md"
	doc, dark, light, _ := mdStylingDoc(t)

	accent := dark["--accent"]
	if accent != "#17A8E6" {
		t.Fatalf("%s: §3.2's --accent is %q; this test is about the website's #17A8E6 and the light grounds it cannot be text on", path, accent)
	}
	if got := mdRatio(t, accent, "#FFFFFF"); math.Abs(got-2.70) > 0.005 {
		t.Errorf("#17A8E6 on white computes %.2f:1, want 2.70:1, the figure reconciliation §5.3 records for white", got)
	}
	if got := mdRatio(t, accent, light["--ground"]); math.Abs(got-2.49) > 0.005 {
		t.Errorf("#17A8E6 on the light set's own --ground %s computes %.2f:1, want 2.49:1, the figure §3.3 now states", light["--ground"], got)
	}

	// The accent itself, on a light ground, is text; and the design says so.
	for _, surface := range mdSurfaceOrder {
		if r := mdRatio(t, light["--accent"], light[surface]); r < 4.5 {
			t.Errorf("%s: the light set's --accent on %s is %.2f:1, and §3.3's own rule is that the light accent may be text on all three surfaces", path, surface, r)
		}
	}

	for _, want := range []string{
		// Each figure beside the background it belongs to.
		"`#17A8E6` on **white** is\n**2.70:1**",
		"`#F2F6FA` the same value is **2.49:1**",
		// And the token the light set names for accent text, since the accent
		// itself cannot be it.
		"**`--accent-ink` `#085A82`** is nevertheless the tone for accent text",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s lacks %q", path, want)
		}
	}
	if ink := light["--accent-ink"]; ink != "#085A82" {
		t.Errorf("%s: the light set's --accent-ink is %q, want #085A82, the tone §3.3 names for accent text on a light ground", path, ink)
	}
}
