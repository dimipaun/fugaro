package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
// and no entry of `SERVED_FILES`, which is why the first string below is the
// status line rather than a mood.
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

// TestDocsConsoleStylingCitationsExist: the design's load-bearing citations are
// paths a reader has to find — the security-sensitive core it does not change,
// the screens whose class hooks its inventory tabulates, the bundler that will
// emit the stylesheet, the tests its wiring section extends, and the palette the
// light set is derived from. A substring assertion cannot see one of them move,
// so each is stat'ed here as well as required in the text.
//
// Every path below was read on this branch on 2026-10-19 and is in the tree.
// Deliberately not listed: everything the design proposes and does not find —
// `console/src/renderer/theme/*.css`, `console/test/unit/theme-tokens.test.ts`
// and `console/test/e2e/__screenshots__/` — because a file a design creates is
// not a file a design cites, and stat'ing one would make the test fail on the
// document doing its job.
func TestDocsConsoleStylingCitationsExist(t *testing.T) {
	const path = "docs/design/console-styling.md"
	doc := readDoc(t, path)
	for _, cited := range []string{
		// The security-sensitive core, and the one directive that admits a
		// stylesheet: the design reads both and changes neither.
		"console/src/main/protocol.ts",
		"console/src/main/window.ts",
		// The documents whose `<link>` the wiring section adds.
		"console/src/renderer/index.html",
		"console/src/renderer/installations.html",
		"console/src/renderer/refusal.html",
		// The renderer the inventory was read from.
		"console/src/renderer/app.ts",
		"console/src/renderer/entry.ts",
		"console/src/renderer/installations.ts",
		"console/src/renderer/screens/live.ts",
		"console/src/renderer/screens/spend.ts",
		"console/src/renderer/screens/projects.ts",
		"console/src/renderer/screens/run.ts",
		"console/src/renderer/screens/degraded.ts",
		"console/src/shared/availability.ts",
		// The bundler that emits the stylesheet, and the tests the wiring
		// extends.
		"console/tools/bundle-renderer/index.ts",
		"console/test/unit/bundle.test.ts",
		"console/test/unit/build-files.test.ts",
		"console/test/unit/csp-document.test.ts",
		"console/test/unit/installations-view.test.ts",
		"console/test/unit/screen-literals.test.ts",
		"console/test/unit/window.test.ts",
		// The build's own lists, which a stylesheet has to join.
		"console/package.json",
		"console/README.md",
		"console/docs/json-contract.md",
		// The palette the light set is derived from, and the check it mirrors.
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
		// ../../console/…), so a citation counts in either form.
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
		// The designs' own files, in the tree the plan promised them from.
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
