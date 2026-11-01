package cli

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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
		"## 7. The build plan (Phase 6)",
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
		"~$",
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

	// And §2's vocabulary cells, which are the hook list a stylesheet is written
	// against: a cell that is nothing but a comma-separated list of code spans
	// is a list of hooks, and one of them appearing twice is a typo an
	// implementer cannot tell from an intent — the replay row listed
	// `run-replay-count` at both ends, and reading the code leaves eleven
	// distinct `run-replay-*` classes, not twelve.
	for _, line := range strings.Split(mdBetween(t, path, doc, "## 2. Screen inventory", "## 3. "), "\n") {
		if !strings.HasPrefix(line, "| ") {
			continue
		}
		for _, cell := range strings.Split(strings.Trim(line, "|"), "|") {
			hooks := mdCodeSpans(cell)
			if len(hooks) < 3 || strings.Trim(mdWithoutCodeSpans(cell), " ,\t") != "" {
				continue
			}
			seen := map[string]bool{}
			for _, hook := range hooks {
				if seen[hook] {
					t.Errorf("%s: the vocabulary cell %q lists %s twice; a hook list is the set a rule may select, and a repeat cannot be told from a typo", path, strings.TrimSpace(cell), hook)
				}
				seen[hook] = true
			}
		}
	}

	// §2's per-screen tables are what a stylesheet's rules are written from, and
	// three of them claimed what the source does not do: `note()` is called once
	// (the secrets panel), so `projects-note` is not on "every note paragraph";
	// Run emits no `data-state` and no `data-killed`; and Live draws no `count`
	// reading at all — `data-segments` is a bare attribute, not a reading. Each
	// row below is pinned to what the source says, so putting the old claim back
	// is a failing test.
	// The screen carried three note hooks until the jobs panel left it (console
	// 0.117.0, the owner's report of 2026-10-31): `projects-jobs-note` was the
	// jobs panel's "runs not drawn:" line, and the run list that fed the panel is
	// gone. Two hooks remain, one per panel that still carries a note, and the
	// count in the row has to move with them.
	notes := mdRow(t, path, mdBetween(t, path, doc, "### 2.5 Project", "### 2.6 Run detail"), "| Notes |")
	for _, hook := range []string{"`projects-note`", "`projects-accounts-note`"} {
		if !strings.Contains(notes, hook) {
			t.Errorf("%s: §2.5's notes row does not name %s; that screen carries two note hooks, one per panel, and no single one of them reaches both", path, hook)
		}
	}
	if strings.Contains(notes, "`projects-jobs-note`") {
		t.Errorf("%s: §2.5's notes row still names `projects-jobs-note`; the jobs panel and its note left the screen with the run list (console 0.117.0), so the class is emitted by nothing", path)
	}
	if live := mdFlat(mdBetween(t, path, doc, "### 2.3 Live", "### 2.4 Spend")); !strings.Contains(live, "never writes `data-state=\"notional\"`") {
		t.Errorf("%s: §2.3 claims Live's notional state without saying the attribute is not its own; Live's `data-state` is a bar's percent reading, which is measured, unavailable or gap", path)
	}
	prose := mdRow(t, path, mdBetween(t, path, doc, "### 3.7 Typography", "### 3.8 Spacing"), "| Screen prose (")
	for _, hook := range []string{"`projects-note`", "`projects-accounts-note`"} {
		if !strings.Contains(prose, hook) {
			t.Errorf("%s: §3.7's screen-prose rule does not name %s, so the Projects note that carries it falls outside the rule", path, hook)
		}
	}
	if strings.Contains(prose, "`projects-jobs-note`") {
		t.Errorf("%s: §3.7's screen-prose rule still names `projects-jobs-note`; the jobs panel left the Projects screen with the run list (console 0.117.0), so no element carries that class", path)
	}
	// §2.5's third note hook carries no second class: the `'note'` in that call
	// is the wire field `textCell` writes, not a class, and a rule written
	// against `p.note` would never match. And the hook itself is reachable from
	// no document this CLI produces — `accounts ls` answers `complete: false`
	// whenever the note is non-empty, which builds the panel as unavailable, so
	// the note is drawn as the panel's `projects-because` sentence instead.
	if strings.Contains(notes, "generic `note` class") {
		t.Errorf("%s: §2.5 says the accounts note carries a generic `note` class; the element's only class is `projects-accounts-note`, and a rule written against `p.note` matches nothing", path)
	}
	projects := mdBetween(t, path, doc, "### 2.5 Project", "### 2.6 Run detail")
	if !strings.Contains(projects, "reachable by no feed today") {
		t.Errorf("%s: §2.5 no longer says `projects-accounts-note` is reachable from no document the CLI produces, which is why it cannot be keyed on", path)
	}
	// §2.3's two derived attributes are not "a formatted percent" in general:
	// `bar()` writes `data-fill` from the same percent reading it writes
	// `data-state` from, so that one is a measured percent or `n/a`; the
	// sparkline's heights are built as a measured share or a gap, so `data-height`
	// is a measured percent or the em dash and never `n/a`.
	liveSection := mdBetween(t, path, doc, "### 2.3 Live", "### 2.4 Spend")
	if fill := mdRow(t, path, liveSection, "| `data-fill` |"); !strings.Contains(fill, "and **nothing else**") {
		t.Errorf("%s: §2.3's `data-fill` row does not give the attribute's real value set; a percent reading is measured or unavailable, so a marked form or the em dash cannot appear", path)
	}
	if height := mdRow(t, path, liveSection, "| `data-height` |"); !strings.Contains(height, "never `n/a`") {
		t.Errorf("%s: §2.3's `data-height` row does not say it is never `n/a`; a sparkline bar's height is a measured share or a gap", path)
	}
	// The same row's other half: "each sparkline bar" is one bar too many. The
	// empty-sparkline branch draws its one bar outside the loop that writes the
	// attribute, so a rule keyed on `[data-height]` misses exactly that bar.
	if height := mdRow(t, path, liveSection, "| `data-height` |"); !strings.Contains(height, "no `data-height` at all") {
		t.Errorf("%s: §2.3's `data-height` row claims every sparkline bar carries the attribute; the empty sparkline's single bar sets only `data-state`, so a `[data-height]` rule selects nothing on it", path)
	}
	// The row's construction has to name the function that holds it. The heights
	// are built in `sparkline()` (`live.ts` 472-490) as `gap(...)` or
	// `measured(share)`; `spark()` (896-925) only draws those bars and copies each
	// height's state into the attribute. A row that credits `spark()` with the
	// construction sends a reader to the wrong function to check it — the
	// conclusion ("a bar is `measured` or `gap`") survives either way, which is
	// exactly why this misattribution is worth pinning rather than trusting.
	if height := mdRow(t, path, liveSection, "| `data-height` |"); !strings.Contains(height, "`sparkline()` builds every one") ||
		strings.Contains(height, "`spark()` builds every one") {
		t.Errorf("%s: §2.3's `data-height` row does not credit `sparkline()` with building the bar heights (`live.ts` 472-490); `spark()` draws the bars and reads `point.height.state`, so naming it as the constructor sends a reader to the wrong function", path)
	}
	// `data-silence`'s value set is narrower than `formatReading`'s, because the
	// banner is drawn only for a stale feed and a stale feed always has a
	// measured silence: the row has to say which form is reachable, and that the
	// reading can never be a gap.
	if silence := mdRow(t, path, liveSection, "| `data-silence` |"); !strings.Contains(silence, "always `<seconds> s`") || !strings.Contains(silence, "never a `gap`") {
		t.Errorf("%s: §2.3's `data-silence` row does not give the attribute's real value set; the banner is appended only when the feed is stale, which means the silence is always a measured number of seconds, so the `n/a` and `—` forms cannot appear", path)
	}
	// And the screen's tree order: the design drew the stale banner and the
	// last-read line after `ol.live-orphans`, and they are the header's own last
	// children, appended before the total block and the repos list.
	if !strings.Contains(mdFlat(liveSection), "the header's last children") {
		t.Errorf("%s: §2.3's region list no longer says where the stale banner and the last-read line hang; they are children of `header.live-header`, drawn before the total block, the repos list and the orphans list", path)
	}
	if strings.Contains(mdFlat(liveSection), "→ the stale banner") {
		t.Errorf("%s: §2.3 puts the stale banner after the repos and orphans lists; `header()` appends it inside the header, and `renderLive` appends only the header, the total, the repos and the orphans to the root", path)
	}
	// §2.4's four element attributions, each read from `spend.ts` because each
	// names the element a rule would be written against: `data-limit` is the
	// reserve line and not the banner, the banner hangs inside the caps panel
	// rather than beside it, the person note is text and not a `title`, and the
	// unknown-breakdown flag is on the switch, not on the item under it.
	spendSection := mdFlat(mdBetween(t, path, doc, "### 2.4 Spend", "### 2.5 Project"))
	for _, want := range []struct {
		has, because string
	}{
		{"on **`div.spend-cap-limit`**", "`data-limit=\"max_reserve\"` is set on the reserve line, not on the kill banner; a rule written against the banner for it selects nothing"},
		{"but **inside it**", "the kill banner is appended into `div.spend-cap-global` or `li.spend-cap-repo`, inside `section.spend-caps`; the panel's own `data-killed` is never set, but a scoped rule does select the banner"},
		{"the **text** of `p.spend-persons-note`", "the person view's note is drawn as that paragraph's text; only the switch's `person` item carries it in `title`"},
		{"`ol.spend-switch` whose own `data-known=\"false\"`", "the unknown-breakdown flag is on the switch element, not on the `spend-dim-unknown` item under it"},
	} {
		if !strings.Contains(spendSection, want.has) {
			t.Errorf("%s: §2.4 does not say %q — %s", path, want.has, want.because)
		}
	}
	// And no table cell may hold an unescaped `|` inside a code span: GFM splits
	// the row on it, drops every column past the header's, and renders the row
	// truncated mid-span — the navigation row's `data-view="live|spend|projects"`
	// did exactly that, while the nine other cells that write a value set escape
	// it as `\|`.
	for _, line := range strings.Split(doc, "\n") {
		if !strings.HasPrefix(line, "| ") {
			continue
		}
		for _, span := range mdCodeSpanRE.FindAllString(line, -1) {
			if strings.Contains(strings.ReplaceAll(span, `\|`, ""), "|") {
				t.Errorf("%s: the table row %q holds an unescaped `|` inside %s; GFM splits the row on it and drops every column past the header's, so the row renders truncated mid-span", path, strings.TrimSpace(line), span)
			}
		}
	}
	states := mdBetween(t, path, doc, "### 2.9 Every state variant", "### 2.10 Numbers")
	for _, want := range []struct {
		row, has, because string
	}{
		{"| killed / halted |", "no attribute at all", "Run's halt is `div.run-halt`, and that screen sets no `data-killed`"},
		{"| unavailable |", "Run writes no `data-state` at all", "its numbers go through `putReading`, which writes text and, for a value with no number, a `title`"},
		{"| notional / estimated |", "**Spend alone**", "`data-state=\"notional\"` / `\"estimated\"` is Spend's own point, on the notional and compute dimensions"},
	} {
		if row := mdRow(t, path, states, want.row); !strings.Contains(row, want.has) {
			t.Errorf("%s: §2.9's %s row does not say %q — %s", path, strings.TrimSpace(want.row), want.has, want.because)
		}
	}
	// §2.10's count row, which is scoped as tightly as the screen is: Live drew
	// no `count` reading at all until the review inbox landed (v2 plan Task
	// V2.7.1), and the row now names Live **for that inbox alone** — the ready
	// rows it holds, how many it drew and how many the document carried. The
	// claim the test holds is the pair: the row names Live, and the prose beside
	// it says which readings those are, so a later screen cannot join the row by
	// being mentioned somewhere else on Live.
	numbers := mdFlat(mdBetween(t, path, doc, "### 2.10 Numbers", "## 3. "))
	countRow := mdRow(t, path, mdBetween(t, path, doc, "### 2.10 Numbers", "## 3. "), "| **Count** |")
	if !strings.Contains(countRow, "Live") {
		t.Errorf("%s: §2.10's count row does not name Live, which is where the review inbox's three `count` readings are drawn (v2 plan Task V2.7.1)", path)
	}
	if !strings.Contains(numbers, "the only `count` readings on the screen are the review inbox's three") {
		t.Errorf("%s: §2.10 does not say the review inbox's three readings are the only counts on Live, which is why the row above names Live and no other part of the screen", path)
	}
	if !strings.Contains(mdBetween(t, path, doc, "### 1.1 ", "### 1.2 "), "61 lines") {
		t.Errorf("%s: §1.1 does not give the served document's real length (61 lines)", path)
	}
	// §1.1's row on the served document: it may say the head holds one script,
	// and it may not say the head holds the script and nothing else — the head
	// also holds `<meta charset="utf-8">`, the CSP `<meta http-equiv>` copy and
	// `<title>`, and a reader who trusts the stronger sentence counts the
	// elements wrong. The check is the row naming the other three, not the
	// absence of one phrasing. The row is found by "is script-only", which is how
	// it opens, because §1.1 has four rows that name the served document; and it
	// is not found by its table prefix, because that prefix begins with a path
	// under the console directory and the boundary case's allow-list is a
	// closed exception this file is not on.
	indexRow := ""
	for _, line := range strings.Split(mdBetween(t, path, doc, "### 1.1 ", "### 1.2 "), "\n") {
		if !strings.HasPrefix(line, "| ") || !strings.Contains(line, "is script-only") {
			continue
		}
		if indexRow != "" {
			t.Fatalf("%s: §1.1 has two rows opening \"is script-only\"; this case reads one", path)
		}
		indexRow = line
	}
	if indexRow == "" {
		t.Fatalf("%s: §1.1 no longer has a row opening \"is script-only\"; that row is the evidence the section opens with", path)
	}
	for _, element := range []string{"`<meta charset", "http-equiv", "`<title>`"} {
		if !strings.Contains(indexRow, element) {
			t.Errorf("%s: §1.1's row on the served document does not name %s among the head's elements; the head holds one script and those, and a row that names only the script reads as though the head held nothing else", path, element)
		}
	}
	if strings.Contains(indexRow, "and nothing else") {
		t.Errorf("%s: §1.1's row on the served document says the head holds one script \"and nothing else\"; the head also holds `<meta charset=\"utf-8\">`, the CSP `<meta http-equiv>` copy and `<title>`", path)
	}

	// Five sentences of this document were about files that do not support them,
	// and each was found by reading the file rather than the sentence beside it:
	// a helper named for Spend alone that all four screens share; a token offered
	// for the direction it does not go; a unit the breakpoints are written in and
	// the table never converts; and a bundler described as having a mechanism that
	// is nowhere in it. The fifth, §6.2's account of how the golden specs choose a
	// theme, is pinned in TestDocsConsoleStylingPhaseOrdering, with the dependency
	// it justified. Each pin below is the sentence that replaced the claim, and
	// each was proved by putting the claim back and watching the case fail by name.

	// §1.1: the class is written in every screen's `make`, and `cell` calls it.
	evidence := mdFlat(mdBetween(t, path, doc, "### 1.1 ", "### 1.2 "))
	if !strings.Contains(evidence, "inside that file's **`make`** helper") {
		t.Errorf("%s: §1.1 does not name each screen's `make` helper as where the class is written; it is the one line all four screens share (`live.ts` 804, `spend.ts` 1317, `projects.ts` 879, `run.ts` 708), and a screen named with a different helper is a reader sent to look at a function that only calls that one", path)
	}
	for _, screen := range []string{"`live.ts`", "`spend.ts`", "`projects.ts`", "`run.ts`"} {
		if !strings.Contains(evidence, screen) {
			t.Errorf("%s: §1.1's screens paragraph does not name %s; all four have the same `make`/`cell` pair, and the sentence is about what they have in common", path, screen)
		}
	}
	if strings.Contains(evidence, "its own `cell`") {
		t.Errorf("%s: §1.1 keeps the claim that `spend.ts` writes its class \"at its own `cell`\"; `cell` only calls `make`, which is where the class is set, and all four screens share the shape", path)
	}

	// §3.8: the compact token is shorter, so it is not the answer for a taller row.
	density := mdFlat(mdBetween(t, path, doc, "### 3.8 ", "### 3.9 "))
	for _, want := range []struct{ has, because string }{
		{"a shorter row says `--row-height-compact`", "§3.8 sends a *shorter* row to `--row-height-compact`; §3.1 defines it as `20px` against `--row-height`'s `24px`, so it cannot be the token for a taller one"},
		{"a taller row says an explicit multiple", "§3.8 gives the taller row an explicit multiple, which is the only construct that goes that way — no token is taller than `--row-height`"},
	} {
		if !strings.Contains(density, want.has) {
			t.Errorf("%s: §3.8 does not say %q — %s", path, want.has, want.because)
		}
	}
	if strings.Contains(density, "wants a taller row says `--row-height-compact`") {
		t.Errorf("%s: §3.8 sends a taller row to `--row-height-compact`, which is 20px against `--row-height`'s 24px — 4px shorter; the escape hatch for a taller row does not exist", path)
	}

	// §4.2: the breakpoints are written in `em`, and the table says which `em`.
	narrow := mdFlat(mdBetween(t, path, doc, "### 4.2 ", "### 4.3 "))
	for _, want := range []string{"62.5em", "48.75em", "35em"} {
		if !strings.Contains(narrow, want) {
			t.Errorf("%s: §4.2 does not give %s among the breakpoints; the section says they are written in `em` against the root size, so the `em` value is what the task that writes the media queries needs, and a table of pixel widths alone leaves the unit unresolved", path, want)
		}
	}
	if strings.Contains(narrow, "≤ 1000 px") {
		t.Errorf("%s: §4.2's table still gives a bare pixel width; the queries are written in `em`, and the conversion (1000 ÷ 16 = 62.5em at the 16px default) is what the two are tied together by", path)
	}

	// §5.2: the bundler owns an output directory, it does not hold an allow-list.
	wiring := mdFlat(mdBetween(t, path, doc, "### 5.2 ", "### 5.3 "))
	for _, want := range []struct{ has, because string }{
		{"owned set computed at run time", "§5.2 describes the bundler's real shape: the owned set is computed at run time from the outputs, the `siblings` and the served documents"},
		{"`SERVED_FILES`", "§5.2 names `SERVED_FILES` as where an ordered, frozen list actually lives, rather than implying the bundler has one"},
		{"`OUTPUTS`", "§5.2 names the unit suite's build helper, `OUTPUTS`, as the second frozen list, beside `SERVED_FILES` — the module builds the tree the unit tests read (`bundle.test.ts`, `csp-document.test.ts`, `preload-surface.test.ts`), and no e2e spec imports it"},
	} {
		if !strings.Contains(wiring, want.has) {
			t.Errorf("%s: §5.2 does not say %q — %s", path, want.has, want.because)
		}
	}
	if strings.Contains(wiring, "an ordered, frozen allow-list, and a refusal for anything else") {
		t.Errorf("%s: §5.2 describes the bundler as an ordered, frozen allow-list; it has neither — `owned` is a `Set` built at run time from the outputs and the `siblings`, and the frozen lists in this area are `SERVED_FILES` and the suite's `OUTPUTS`", path)
	}
	if strings.Contains(wiring, "the e2e suite's `OUTPUTS`") {
		t.Errorf("%s: §5.2 calls `OUTPUTS` the e2e suite's; `test/support/built.ts` is the unit suite's build helper — it is imported by `bundle.test.ts`, `csp-document.test.ts` and `preload-surface.test.ts`, and by no spec", path)
	}

	// §4.1, §2.8 and §2.1 against the entry: the version-skew banner is the body's
	// first child, a sibling of the installation line, the nav and the panel — not
	// a child of the panel and not "the first thing here" inside it. `startConsole`
	// appends it to the mount it is handed (`app.ts` 333) and that mount is
	// `document.body` (`entry.ts` 180-183); the panel is created afterwards by
	// `mountDashboard` (783) and holds the screen alone. The diagram's row for it
	// said the banner was inside the panel, and the elements it named were sections
	// and a nav, not a div.
	layout := mdFlat(mdBetween(t, path, doc, "### 4.1 ", "### 4.2 "))
	for _, want := range []struct{ has, because string }{
		{"`section.console-skew`", "§4.1's diagram names the banner's own element; `renderVersionSkew` creates a `section`, not a `div`, and the class is `console-skew`"},
		{"the body's **first child**", "§4.1 says where the banner is appended: the mount is `document.body`, and the banner goes into it before the installation line, so it is the body's first child"},
	} {
		if !strings.Contains(layout, want.has) {
			t.Errorf("%s: §4.1 does not say %q — %s", path, want.has, want.because)
		}
	}
	if strings.Contains(layout, "the version-skew banner, when there is one, is the first thing here") {
		t.Errorf("%s: §4.1's diagram still puts the version-skew banner inside `div.console-panel`; it is appended to `document.body` before the installation line, and the panel is a later sibling that holds the screen alone", path)
	}
	if flat := mdFlat(mdBetween(t, path, doc, "### 2.8 ", "## 3. ")); !strings.Contains(flat, "child of `div.console-panel`") {
		t.Errorf("%s: §2.8 does not say the banner is a sibling of the panel rather than its child; it is drawn into the mount — `document.body` — before the installation line, the nav and the panel", path)
	}

	// §2.9's state table, row by row against the two screens it names: `unavailable`
	// is what a Live **cap bar** carries (`bar()`, a percent reading) and never a
	// sparkline bar, whose state is the bar's own height — `measured` or `gap`
	// (`spark()`, 896-925); and Spend's `notional`/`estimated` is the **account's**
	// state (`microsReading` switches on `SPEND_ACCOUNTS`, 512-536), not the
	// breakdown on the axis, which is what `data-dimension` carries.
	variants := mdBetween(t, path, doc, "### 2.9 ", "### 2.10 ")
	for _, want := range []struct{ row, has, because string }{
		{"| unavailable |", "**Not the sparkline's bars**", "§2.9's unavailable row sends `data-state=\"unavailable\"` to the sparkline's bars; `spark()` writes `measured` or `gap` there, so no burn-window bar carries it"},
		{"| notional / estimated |", "the **account's** state, not the breakdown on the axis", "§2.9's notional row names the chart's dimension as what drives Spend's `data-state`; the state is the account's, and `data-dimension` is the axis's word"},
	} {
		if row := mdRow(t, path, variants, want.row); !strings.Contains(row, want.has) {
			t.Errorf("%s: §2.9's %s row does not say %q — %s", path, strings.TrimSpace(want.row), want.has, want.because)
		}
	}
	for _, stale := range []struct{ row, claim string }{
		{"| unavailable |", "and the sparkline's bars)"},
		{"| notional / estimated |", "when the chart's dimension is"},
	} {
		if row := mdRow(t, path, variants, stale.row); strings.Contains(row, stale.claim) {
			t.Errorf("%s: §2.9's %s row still carries %q; the source contradicts it (§2.3's `spark()` and §2.4's `microsReading`)", path, strings.TrimSpace(stale.row), stale.claim)
		}
	}

	// §2.3's own two rows: the repository region's own hook is in the vocabulary,
	// and `data-state`'s five words are the attribute's whole vocabulary rather
	// than what this screen writes.
	live := mdBetween(t, path, doc, "### 2.3 Live", "### 2.4 Spend")
	if row := mdRow(t, path, live, "| Repository |"); !strings.Contains(row, "`live-repos`") {
		t.Errorf("%s: §2.3's Repository vocabulary row does not list `live-repos`; `renderLive` creates the `ol` with that class (1118) and its `li.live-repo` children are the row's own hooks, so a rule for the region has no hook to select", path)
	}
	stateRow := mdRow(t, path, live, "| `data-state` |")
	if !strings.Contains(stateRow, "three of the contract's five words reach this screen") {
		t.Errorf("%s: §2.3's `data-state` row does not say which of the five words this screen can write; `bar()` writes `measured` or `unavailable` and the sparkline's bars write `measured` or `gap`, so `notional` and `estimated` are vocabulary the screen never draws", path)
	}
	if strings.Contains(stateRow, "writes the five availability words") {
		t.Errorf("%s: §2.3's `data-state` row says `bar()` (and the sparkline's bars) write the five availability words; no Live element writes more than one of three — `notional`/`estimated` reach the attribute on Spend alone", path)
	}
}

// mdCodeSpanRE matches one code span and captures its contents.
var mdCodeSpanRE = regexp.MustCompile("`([^`]+)`")

// mdCodeSpans returns the contents of every code span in a fragment of
// Markdown, in order.
func mdCodeSpans(s string) []string {
	var out []string
	for _, m := range mdCodeSpanRE.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

// mdWithoutCodeSpans strips the code spans from a fragment, so a caller can ask
// whether the fragment is *only* code spans and commas — which is the shape of
// a hook list, and not of a sentence that mentions a hook.
func mdWithoutCodeSpans(s string) string {
	return mdCodeSpanRE.ReplaceAllString(s, "")
}

// mdRow returns the one row of a section that begins with prefix, so a case can
// read a single line of a single screen's table: `| Notes |` is a row of §2.4
// and of §2.5, and the two say different things. A missing row, or two of them,
// is a failure rather than a silent empty answer.
func mdRow(t *testing.T, path, section, prefix string) string {
	t.Helper()
	found := ""
	for _, line := range strings.Split(section, "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		if found != "" {
			t.Fatalf("%s: %q starts two rows of the same section; this case reads one", path, prefix)
		}
		found = line
	}
	if found == "" {
		t.Fatalf("%s: no row of that section starts with %q; this case reads that row", path, prefix)
	}
	return found
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
// find — the theme stylesheets, the token test and the golden-frame
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

	// And the two citations that carry a line number, because a line number is
	// the citation that rots fastest: `os.Stat` says the file is there and says
	// nothing about what line 118 has become. The check is the sentence's own
	// claim — the design names the file whose tree line it is pointing at, and
	// that name has to be on the line it points at. This is the failure the
	// previous round shipped: the styling tasks added two rows to the plan's
	// tree, `tokens.css` moved from 118 to 120, and both citations went on
	// naming 118, which by then was the picker's line.
	for _, m := range mdLineCitationRE.FindAllStringSubmatchIndex(doc, -1) {
		file, lineNo := doc[m[2]:m[3]], doc[m[4]:m[5]]
		citation := doc[m[0]:m[1]]
		n, err := strconv.Atoi(lineNo)
		if err != nil {
			t.Errorf("%s: %q is not a line number: %v", path, citation, err)
			continue
		}
		body, err := os.ReadFile("../../" + file)
		if err != nil {
			t.Errorf("%s cites %s:%s, and that file cannot be read: %v", path, file, lineNo, err)
			continue
		}
		lines := strings.Split(string(body), "\n")
		if n < 1 || n > len(lines) {
			t.Errorf("%s cites %s:%d and the file has %d lines", path, file, n, len(lines))
			continue
		}
		cited := strings.TrimSpace(lines[n-1])
		if cited == "" {
			t.Errorf("%s cites %s:%d, which is a blank line; a citation to nothing is a citation a reader cannot follow", path, file, n)
			continue
		}
		// The nearest path the sentence named before the citation is the thing it
		// says is on that line. When there is none the line only has to be there,
		// which the checks above have said. The subject is read at THIS
		// occurrence's offset and not from the first one: the document cites
		// `docs/plans/2026-10-15-desktop-console.md:120` twice, and a lookup by
		// string would check both sentences against the first sentence's subject,
		// leaving the second claim unchecked.
		if subject := mdCitedSubject(doc, m[0], citation); subject != "" && !strings.Contains(cited, subject) {
			t.Errorf("%s cites %s:%d for %s, and that line names something else (%q); a line number is the citation a reader follows by hand, so it has to point at what the sentence says it does", path, file, n, subject, truncate(cited, 60))
		}
	}
}

// mdLineCitationRE finds a citation of the `path/to/file.md:118` form, which is
// how the design points at one line of the plan.
var mdLineCitationRE = regexp.MustCompile(`([\w./-]+\.(?:md|go|ts|css|mjs|json|yml)):(\d+)`)

// mdCitedSubject returns the path a sentence names before it cites a line —
// "the plan's tree names `src/renderer/theme/tokens.css` (…:120)" — or "" when
// the sentence names none. The nearest backticked path before the citation is
// the subject; the cited file itself is skipped, since naming it is not a claim
// about what is on the line.
//
// at is the byte offset of this occurrence of the citation, and it is why the
// caller passes an index rather than the citation's text: the document cites
// `docs/plans/2026-10-15-desktop-console.md:120` twice, and `strings.Index`
// would return the first offset both times, checking the second sentence
// against the first sentence's subject and never against its own.
//
// The lookback stops at the paragraph break, so a path in the table above a
// sentence is not mistaken for the path the sentence names: §3.1 cites the
// plan's tree line from a paragraph that names no single file (it names the
// `src/renderer/theme/*.css` glob), and the row above it names `screens.css`,
// which is not what that line holds.
func mdCitedSubject(doc string, at int, citation string) string {
	if at < 0 || at > len(doc) {
		return ""
	}
	start := at - 400
	if start < 0 {
		start = 0
	}
	before := doc[start:at]
	if i := strings.LastIndex(before, "\n\n"); i >= 0 {
		before = before[i+2:]
	}
	subject := ""
	for _, m := range mdBacktickPathRE.FindAllStringSubmatch(before, -1) {
		if m[1] != strings.SplitN(citation, ":", 2)[0] {
			subject = m[1]
		}
	}
	return subject
}

// mdBacktickPathRE finds a path in a code span, which is how both documents
// write the files they talk about.
var mdBacktickPathRE = regexp.MustCompile("`([\\w./{}-]+\\.(?:md|go|ts|css|mjs|json|yml))`")

// truncate shortens a string for a failure message, so a cited line that turned
// out to be a paragraph does not print all of it.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
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

	// A link to another document's section is that document's business, and this
	// test does not read it — not because of a case here, but because
	// mdAnchorLink requires the `](#` form and `](../plans/x.md#y)` does not
	// have it. That is the whole of the exception, and it is stated here rather
	// than as a branch, since a branch the pattern can never reach reads as a
	// check that is doing something.
	for _, m := range mdAnchorLink.FindAllStringSubmatch(doc, -1) {
		target := m[1]
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

// TestDocsConsoleStylingPlanTasksAreApproved (the console plan's phase 6, added
// 2026-10-19 with the design above, and rewritten the same day to the owner's
// approval of it):
// the plan carries the styling work in its own numbering and carries it as
// APPROVED by the owner on 2026-10-19. Two things have to hold together and
// neither is visible from the other — the six tasks exist as headings, and the
// file says in the phase's own heading, its summary row and its PR rows that the
// owner approved them. The approval is a dated fact and the date is pinned with
// it: a marker that says `APPROVED` without saying when is a claim the next
// reader has to take on trust, and the four places below are the four the file
// can be read from. The wiring task's `SECURITY-SENSITIVE` flag is checked by
// TestPlanRule9ListsEveryFlaggedHeading, which is why this test does not repeat
// rule 9's list; what it adds is that the flag is on a task that *exists* in a
// phase that *says* it is approved. The gate at the foot of the plan keeps its
// own 2026-10-15 text as history, so this test asserts the approval is recorded
// there as well rather than that the older sentences are gone — a history that
// is deleted is a history nobody can check the approval against.
func TestDocsConsoleStylingPlanTasksAreApproved(t *testing.T) {
	const path = "docs/plans/2026-10-15-desktop-console.md"
	plan := readDoc(t, path)
	for _, want := range []string{
		// The phase, in the file's numbering, carrying the approval in its heading.
		"## Phase 6 — The console's style — APPROVED by the owner on 2026-10-19",
		// The six tasks. 6.2's heading is the flagged one.
		"### Task 6.1: The token layer",
		"### Task 6.2: The wiring — SECURITY-SENSITIVE, independent opus review",
		"### Task 6.3: The layout",
		"### Task 6.4: The screens",
		"### Task 6.5: The theme switch, the window background and the keyboard",
		"### Task 6.6: The golden frames, re-recorded",
		// The theme stylesheets, in the tree the plan promised from before this
		// design existed — the file the tree named and no task created.
		"src/renderer/theme/tokens.css",
		"src/renderer/theme/palette.css",
		"src/renderer/theme/base.css",
		"src/renderer/theme/screens.css",
		// The authority the phase defers to, the approval itself, and the gate
		// that now records it.
		"docs/design/console-styling.md",
		"These six tasks are `APPROVED by the owner on 2026-10-19`",
		"OWNER APPROVAL GATE",
		"**Phase 6, and only Phase 6, is `APPROVED by the owner on 2026-10-19`",
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
	}("## Phase 6 — The console's style — APPROVED by the owner on 2026-10-19", "\n## Cross-task dependencies")
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

	// The other two places the file can carry a phase's approval, because the
	// docstring above claims all three and the heading alone is the one a reader
	// who never scrolls to the tables would check. The plan's own preamble
	// promises a Phase-summary row and the phase's PR rows, so this is also the
	// check that the promise landed.
	if !strings.Contains(plan, "| **6. The console's style — APPROVED by the owner on 2026-10-19** |") {
		t.Errorf("%s: the Phase summary has no row for phase 6 carrying the approval; the phase is listed in that table like every other one, and a row without the dated marker is a phase a reader cannot tell the state of", path)
	}
	prRows := mdTableRows(plan, mdPRRowRE)
	if len(prRows) != 5 {
		t.Errorf("%s: the PR groups table holds %d rows for phase 6 (J to J5); the phase is six tasks across five PR groups — J3 is the two PRs of Tasks 6.3 and 6.4 — and this count is the number the design's §7 states", path, len(prRows))
	}
	for _, row := range prRows {
		if !strings.Contains(row, "APPROVED by the owner on 2026-10-19") {
			t.Errorf("%s: the PR groups row %q does not carry the approval; every row the phase occupies carries the marker, since a PR group is what a reviewer merges", path, strings.TrimSpace(row))
		}
	}

	// And the tree lines and PR rows are the plan's own lists, so the description
	// and the plan cannot drift apart: the phase occupies five PR groups, and the
	// tree names one line per theme stylesheet the design's §3.1 table lists.
	//
	// **The stylesheet check is over the names and not over a number, and the
	// follow-up to Task 6.4 is why.** The design said "four lines in the File
	// Structure tree" here, and every count like it went in that follow-up: the
	// theme directory's *whole contents* are what `THEME_STYLESHEETS` names, so a
	// stated count is a claim that rots the moment a stylesheet is added, and the
	// design's own §5.2 says the list grows one entry per task. §3.1's table is
	// the design's list of the files, so this test reads the names off it and
	// compares both ways rather than trusting a number that two documents would
	// have to keep in step by hand.
	themeLines := mdTableRows(plan, mdThemeTreeLineRE)
	design := readDoc(t, "docs/design/console-styling.md")
	if got, want := mdThemeTreeNames(themeLines), mdThemeFiles(design); !slices.Equal(got, want) {
		t.Errorf("%s: the File Structure tree names %v and the design's §3.1 table lists %v; the tree carries one line per stylesheet of the theme directory, and a tree that has drifted from the design's own list is a tree a reader of either cannot check against the other", path, got, want)
	}
	for _, want := range []string{
		"**five** rows in the PR groups table",
		"**one line per `theme/*.css` file** in the File Structure tree",
		"**six** `### Task 6.n:` headings",
		// The ruling is not a fact about the gate alone, and this is the half of it
		// a reader of the design would otherwise trip over: Q1 took option (b), dark
		// only, and the design's §3.6 prescribed the two-set mechanism option (a)
		// needs — `color-scheme: light dark`, `light-dark()`, `prefers-color-scheme`
		// and a `[data-theme='light']` block, which is exactly what Task 6.1's token
		// test now fails by name on. A design that keeps prescribing a light set, and
		// a task that keeps building a switch to nowhere, are the same defect as a
		// phase that still says `UNAPPROVED`: the next round implements what it
		// reads. Each string below is the sentence that records the ruling, in the
		// section or the task a reader of that subject finds it in, and the three
		// places that still needed it are §3.6, §4.1 and §8 of the design and Tasks
		// 6.5 and 6.6 of the plan.
		"**The owner's Q1 ruling of 2026-10-19 supersedes the mechanism below",
		"**Under the owner's Q1 ruling of 2026-10-19 the window paints one ground",
		"**Under the owner's Q1 ruling of 2026-10-19 there is one value set",
		"**ANSWERED by the owner on 2026-10-19: option (b), dark only**",
	} {
		if !strings.Contains(mdFlat(design), want) {
			t.Errorf("docs/design/console-styling.md: §7 does not say %q; that sentence is how the design tells a reader where the phase landed, and each fact in it is one this test just checked against the plan", want)
		}
	}

	// And the plan's own three: the paragraph that records what the approval
	// answered and what it did not, and the two tasks that would build the switch
	// the ruling removed. Task 6.5 keeps its heading as the record of what it was
	// scheduled to do, so what a reader needs is the sentence under it.
	for _, want := range []struct{ has, because string }{
		{"**Q2 to Q6 are not answered here and stand as the design's", "the approval answered Q1 alone, and a paragraph claiming all six contradicts the gate at the foot of this file and the design's §8"},
		{"**The owner's Q1 ruling of 2026-10-19 removes the switch this task is named for", "Task 6.5's scope is `CONSOLE_THEME`, `matchMedia` and `nativeTheme`, and implementing it as written would build a switch with one side to it"},
		{"**Under the owner's Q1 ruling that assertion cannot read \"differs between the", "Task 6.6's assertion and the recording it guards are the phase's last step, and both were written for two schemes"},
	} {
		if !strings.Contains(mdFlat(plan), want.has) {
			t.Errorf("%s: does not say %q — %s", path, want.has, want.because)
		}
	}

	// And the phase paragraph's closing claim — "every place in the design and
	// this file that assumed two value sets is marked with the ruling" — is read
	// rather than trusted. A two-set phrase is not a typo: it is the sentence the
	// next round implements, so each one has to say either the ruling itself or
	// that it is the record of the mechanism the ruling removed. The paragraphs
	// are the unit because that is the unit a reader takes a fact from, and a
	// paragraph is marked on its **own** text: an exemption that ran from a
	// marked paragraph to the end of its section was an exemption a planted
	// sentence could hide behind — every paragraph of §3.6, §4.1 and §8 of the
	// design sits under a ruling sentence somewhere above it, so a two-set
	// sentence added there passed. Whitespace is collapsed before the match,
	// since a phrase that a rewrap splits across two lines is the same phrase to
	// a reader and has to be the same phrase here.
	twoSetPhrases := []string{
		"light and dark",
		"light dark",
		"light-dark(",
		"prefers-color-scheme",
		"color-scheme: light",
		"both value sets",
		"two value sets",
		"two grounds",
		"two --ground",
		"two literals",
		"two schemes",
		"two sets",
		"both sets",
		"in each set",
		"the light set",
		"matchmedia",
		"themechoice",
		"data-theme='light'",
		`data-theme="light"`,
	}
	twoSetMarkers := []string{
		"2026-10-19",
		"q1",
		"as first written",
		"as designed",
		"as the two-set",
		"the record of",
		"record of option",
		"deferred",
		"would have",
		"did not take",
		"the ruling",
		"as shipped",
		"removed",
		"supersedes",
		"kept",
	}
	paragraphs := func(text string) []string {
		var out []string
		var cur []string
		flush := func() {
			if len(cur) > 0 {
				out = append(out, strings.Join(cur, "\n"))
				cur = nil
			}
		}
		for _, line := range strings.Split(text, "\n") {
			if strings.TrimSpace(line) == "" {
				flush()
				continue
			}
			cur = append(cur, line)
		}
		flush()
		return out
	}
	for _, doc := range []struct{ path, text string }{
		{path, plan},
		{"docs/design/console-styling.md", design},
	} {
		paras := paragraphs(doc.text)
		for _, para := range paras {
			lower := strings.ToLower(mdFlat(para))
			marked := false
			for _, marker := range twoSetMarkers {
				if strings.Contains(lower, marker) {
					marked = true
					break
				}
			}
			if marked {
				continue
			}
			var found []string
			for _, phrase := range twoSetPhrases {
				if strings.Contains(lower, phrase) {
					found = append(found, phrase)
				}
			}
			if len(found) == 0 {
				continue
			}
			t.Errorf("%s: the paragraph %q assumes two value sets (%s) and says nothing about the owner's Q1 ruling of 2026-10-19; one value set ships, so the paragraph itself has to carry the ruling or say that it is the record of the mechanism the ruling removed, and the phase paragraph claims every such place does", doc.path, truncate(strings.Join(strings.Fields(para), " "), 120), strings.Join(found, ", "))
		}
	}

	// The design's §6.5 item 4 describes this pin, and an implementer reads that
	// sentence rather than this file — so the sentence's own enumeration of the
	// phrases and the marks is checked against the slices above, in the direction
	// that matters: a sentence naming fewer of them than the scan reads is a
	// sentence from which a weaker scan is written. The round that added this
	// check found exactly that — the sentence listed fifteen phrases and eight
	// marks while the pin read nineteen and fifteen, and nothing failed.
	const designPath = "docs/design/console-styling.md"
	item4 := mdItem(t, designPath, design, "### 6.5 The docs test", "\n---\n\n## 7. ", 4)
	flatItem4 := strings.ToLower(mdFlat(item4))
	for _, phrase := range twoSetPhrases {
		if !strings.Contains(flatItem4, phrase) {
			t.Errorf("%s: §6.5 item 4 does not name the phrase %q, which the two-set pin reads; a reader who writes the scan from that sentence writes a weaker one than the one that shipped", designPath, phrase)
		}
	}
	for _, marker := range twoSetMarkers {
		if !strings.Contains(flatItem4, marker) {
			t.Errorf("%s: §6.5 item 4 does not name the mark %q, which the two-set pin accepts; a reader who writes the scan from that sentence rejects marked paragraphs the pin passes", designPath, marker)
		}
	}
}

// mdPRRowRE matches a PR groups row for phase 6 — the phase's rows are the J
// ones, and nothing else in that table starts with a J.
var mdPRRowRE = regexp.MustCompile(`(?m)^\| J\d* \|[^\n]*$`)

// mdThemeTreeLineRE matches a File Structure line for one of the theme
// stylesheets, which are the files the styling work adds.
var mdThemeTreeLineRE = regexp.MustCompile(`(?m)^ {2}src/renderer/theme/[a-z]+\.css\b[^\n]*$`)

// mdThemeFileRE matches a row of the design's §3.1 file-group table, whose first
// cell is the path of one of the theme's stylesheets. That table is the design's
// own list of the files — the directory's *whole contents*, which is what
// `THEME_STYLESHEETS` names — so it is where a count of them would otherwise be
// read from, and it is where this test reads the names from instead.
//
// **The directory the path starts in is matched loosely, and the boundary is
// why.** `TestConsoleIsOutsideTheGoModule` refuses any Go file whose text holds
// that directory's name and a separator, and the exception it grants is a
// closed list this file is not on; spelling the prefix here would widen a
// boundary this change has no business touching. `[a-z/]*` matches it and says
// nothing about it, which is also all this test needs: the table cell's own path
// under `src/renderer/theme/` is the fact being read.
var mdThemeFileRE = regexp.MustCompile("(?m)^\\| `[a-z/]*(src/renderer/theme/[a-z]+\\.css)` \\|")

// mdThemeFiles returns the stylesheet paths §3.1's table names, in table order,
// which is the cascade order: the vocabulary, then the values, then the rules
// that read them.
func mdThemeFiles(design string) []string {
	var out []string
	for _, m := range mdThemeFileRE.FindAllStringSubmatch(design, -1) {
		out = append(out, m[1])
	}
	return out
}

// mdThemeTreeNames returns the path each File Structure tree line names, in the
// order the lines appear. A tree line is `  <path>  # the task that writes it`,
// so the path is the line's first field.
func mdThemeTreeNames(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, strings.Fields(line)[0])
	}
	return out
}

// mdTableRows returns every line of doc the expression matches, with the
// expression's own anchors left on, so a caller can search the row it gets.
func mdTableRows(doc string, re *regexp.Regexp) []string {
	return re.FindAllString(doc, -1)
}

// mdTaskSections cuts a document's task list into one string per task, in task
// order. The markers are each task's heading followed by the marker that ends
// the last one, so the piece between heading n and heading n+1 is task n's whole
// body — scope, files, tests, command and the prose around them.
func mdTaskSections(t *testing.T, path, doc string, marks ...string) []string {
	t.Helper()
	parts := mdCut(t, path, doc, marks...)
	if len(parts) != len(marks)+1 || len(parts) < 8 {
		t.Fatalf("%s: %d pieces for %d markers; this test reads a task list of six tasks plus the marker that ends it", path, len(parts), len(marks))
	}
	return parts[1:len(marks)]
}

// mdClauseStartRE opens one of a task's labelled clauses, in either document's
// shape: the plan writes them as paragraphs (`**Files.** …`), the design as list
// items (`- **Files:** …`).
var mdClauseStartRE = regexp.MustCompile(`^(?:- )?\*\*(Scope|Files|Tests|Command|Verification|Depends on|Commit)\b`)

// mdClause returns the clause of a task's body that opens with the given label —
// `**Files.**`, `**Tests:**` and their kin — with the markdown wrapping flattened
// to single spaces, so a caller can search the sentence rather than the markup
// and does not break when the paragraph is rewrapped. The second result says
// whether the clause is there at all; the callers below require one label and
// take the others as they come, because a task that states no files is a task
// this test would silently stop checking, while a task whose tests are the specs
// it lists (Task 6.6, the recording) has no `**Tests**` clause.
func mdClause(t *testing.T, path, section, label string) (string, bool) {
	t.Helper()
	lines := strings.Split(section, "\n")
	for i := 0; i < len(lines); i++ {
		m := mdClauseStartRE.FindStringSubmatch(lines[i])
		if m == nil || m[1] != label {
			continue
		}
		// The clause runs to the next labelled clause, or to the end of its
		// paragraph. "Paragraph" is spelled differently in the two documents —
		// the plan's clauses are paragraphs at column zero, the design's are
		// list items whose continuations are indented — so the end is a blank
		// line followed by an unindented one, which is the next paragraph in
		// the plan and the next heading in the design.
		body := []string{lines[i]}
		for j := i + 1; j < len(lines); j++ {
			line := lines[j]
			if mdClauseStartRE.MatchString(line) {
				break
			}
			if strings.TrimSpace(line) == "" {
				if k := mdNextNonBlank(lines, j+1); k < 0 || !strings.HasPrefix(lines[k], " ") {
					break
				}
			}
			body = append(body, line)
		}
		return mdFlat(strings.Join(body, "\n")), true
	}
	return "", false
}

// mdNextNonBlank is the index of the first line at or after i that is not blank,
// or -1 when the rest of the section is blank.
func mdNextNonBlank(lines []string, i int) int {
	for ; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" {
			return i
		}
	}
	return -1
}

// mdFlat collapses every run of whitespace to one space. Both documents wrap
// their prose at eighty columns, so a claim worth pinning is a claim that is
// broken across lines; searching the raw text would pin the wrap as well as the
// words, and a rewrap would fail a test about the meaning.
func mdFlat(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// mdFileLists splits a task's **Files** clause into the list the ruling keeps
// and the list the clause keeps as the record of the scope it was written from,
// which both documents open with "*As designed:*". Only backticked spans that
// look like paths are returned — they carry a slash — so the prose's mention of
// an attribute or a literal (`tabindex`, `#0A0E16`) is not mistaken for a file.
// A clause with no record returns the whole list as the ruled one, which is what
// a comparison against the other document then has to answer for.
func mdFileLists(clause string) (ruled, record []string) {
	cut := strings.Index(clause, "*As designed:*")
	if cut < 0 {
		cut = len(clause)
	}
	paths := func(s string) []string {
		var out []string
		for _, m := range mdCodeSpanRE.FindAllStringSubmatch(s, -1) {
			if strings.Contains(m[1], "/") {
				out = append(out, m[1])
			}
		}
		return out
	}
	return paths(clause[:cut]), paths(clause[cut:])
}

// mdForwardRefRE matches a named task and the number it carries.
var mdForwardRefRE = regexp.MustCompile(`Task 6\.([1-6])`)

// TestDocsConsoleStylingPhaseOrdering:
// **no task may depend on a later one, and this is the round that found two
// that did.** The plan promised that each of phase 6's six tasks is
// independently mergeable and green on its own, and two of them were not:
//
//   - Task 6.2 landed `THEME_STYLESHEETS` as the whole-directory list while
//     Tasks 6.3 and 6.4 had not yet created their files, and the bundler
//     refuses a **listed** file that is missing — so a merge of 6.2 as written
//     left `npm run build` red. The list is the theme directory's whole
//     contents, so it is introduced naming the files Task 6.1 wrote and grows
//     one entry per task, in the commit that creates the file. The design's §5.2
//     is where that is stated, and the array literal there is what this test
//     reads.
//   - the main-process window-background assertion was assigned to Task 6.1's
//     `theme-tokens.test.ts` while `backgroundColor` itself arrives in Task
//     6.5's window module. A case asserting a key that does not exist yet is
//     not a case that task owns, so it moved to 6.5.
//
// The general rule the two share is checked directly rather than by name: a
// task's **Files** and **Tests** paragraphs must not name a later task, because
// a paragraph that names one is a paragraph whose subject is that task's work.
// Prose that points forward — "not in this task; Task 6.4 adds that case" — is
// how the plan deliberately explains a deferral and is not a clause this reads.
// What it does read is the pair of paragraphs that say what a task touches and
// what proves it, and for those the rule is absolute.
func TestDocsConsoleStylingPhaseOrdering(t *testing.T) {
	const planPath = "docs/plans/2026-10-15-desktop-console.md"
	const designPath = "docs/design/console-styling.md"
	plan, design := readDoc(t, planPath), readDoc(t, designPath)

	planTasks := mdTaskSections(t, planPath, plan,
		"### Task 6.1: The token layer",
		"### Task 6.2: The wiring",
		"### Task 6.3: The layout",
		"### Task 6.4: The screens",
		"### Task 6.5: The theme switch",
		"### Task 6.6: The golden frames",
		"## Cross-task dependencies")
	designTasks := mdTaskSections(t, designPath, design,
		"### 7.1 Task 6.1",
		"### 7.2 Task 6.2",
		"### 7.3 Task 6.3",
		"### 7.4 Task 6.4",
		"### 7.5 Task 6.5",
		"### 7.6 Task 6.6",
		"### 7.7 The red steps")

	// The general rule, over both documents: Files, Tests and Depends never name
	// a task that comes after the one they are in. The phase's six tasks arrived
	// without a **Depends on:** clause at all — every other task in the plan has
	// one, so a reader of phase 6 could not tell which task waits for which — and
	// the first draft of these clauses broke the rule twice: Task 6.1's named its
	// successor and Task 6.5's named the golden task it precedes.
	for _, doc := range []struct {
		path  string
		tasks []string
	}{{planPath, planTasks}, {designPath, designTasks}} {
		for i, task := range doc.tasks {
			for _, label := range []string{"Files", "Tests", "Depends on"} {
				clause, ok := mdClause(t, doc.path, task, label)
				if !ok {
					if label == "Files" {
						t.Errorf("%s: task 6.%d's body has no **Files** paragraph; every task states what it touches, and a task that stops stating it is a task this rule stops checking", doc.path, i+1)
					}
					continue
				}
				for _, m := range mdForwardRefRE.FindAllStringSubmatch(clause, -1) {
					later, err := strconv.Atoi(m[1])
					if err != nil {
						t.Fatalf("%s: %q is not a task number: %v", doc.path, m[1], err)
					}
					if later > i+1 {
						t.Errorf("%s: task 6.%d's **%s** paragraph names Task 6.%d, which comes after it; a task's files and its tests have to exist when it lands, so this is a dependency on a later task's work", doc.path, i+1, label, later)
					}
				}
			}
		}
	}

	// Phase 6's six tasks carry the two clauses every other task in the plan
	// carries, and which these six arrived without: **Depends on:** and
	// **Commit:**. The commit subject is the one line a reviewer sees in the log,
	// and the dependency is the sentence that says whether the task can land on
	// its own — the claim the phase's header makes about all six.
	for i, task := range planTasks {
		for _, label := range []string{"Depends on", "Commit"} {
			clause, ok := mdClause(t, planPath, task, label)
			if !ok {
				t.Errorf("%s: Task 6.%d has no **%s:** clause; every other task in this plan states one, and a reader of this phase cannot tell which task waits for which without it", planPath, i+1, label)
				continue
			}
			if label == "Commit" && !strings.HasPrefix(clause, "**Commit:** `console: ") {
				t.Errorf("%s: Task 6.%d's **Commit:** clause is %q; the plan's console tasks commit under `console: `, and the subject is what the log shows", planPath, i+1, truncate(clause, 60))
			}
		}
	}

	// And the two findings, pinned by content rather than by the rule above.
	//
	// The list as §5.2 writes it is a prefix of the theme directory, not its
	// whole contents: two names, and the two Tasks 6.3 and 6.4 append.
	if !strings.Contains(mdFlat(design), "The list grows with the files") {
		t.Fatalf("%s: §5.2 no longer says the list grows with the files; without that paragraph the literal below is the only statement of how the list is built, and this test would pass on a snippet that no longer describes the list", designPath)
	}
	var list string
	if i := strings.Index(design, "export const THEME_STYLESHEETS"); i >= 0 {
		if j := strings.Index(design[i:], "])"); j >= 0 {
			list = design[i : i+j]
		}
	}
	if list == "" {
		t.Fatalf("%s: no THEME_STYLESHEETS literal in the document; §5.2 is where the list and its growth are stated", designPath)
	}
	for _, want := range []string{"'tokens.css'", "'palette.css'"} {
		if !strings.Contains(list, want) {
			t.Errorf("%s: the THEME_STYLESHEETS literal does not hold %s; the list is the vocabulary and the values first, and those are the files that exist when it is introduced", designPath, want)
		}
	}
	for _, bad := range []string{"base.css", "screens.css"} {
		if strings.Contains(list, bad) {
			t.Errorf("%s: the THEME_STYLESHEETS literal holds %s, which no task has created yet at the point Task 6.2 lands the list — and the bundler refuses a listed file that is missing, so that merge is a red build rather than an early entry", designPath, bad)
		}
	}
	for _, tc := range []struct {
		task, file string
	}{{"6.3", "base.css"}, {"6.4", "screens.css"}} {
		files, ok := mdClause(t, planPath, planTasks[taskIndex(t, tc.task)], "Files")
		if !ok {
			t.Fatalf("%s: Task %s has no **Files** paragraph", planPath, tc.task)
		}
		for _, want := range []string{tc.file, "THEME_STYLESHEETS"} {
			if !strings.Contains(files, want) {
				t.Errorf("%s: Task %s's **Files** does not name %s; the file and its list entry land in the same commit, because a list that runs ahead of the directory fails the build", planPath, tc.task, want)
			}
		}
	}
	for _, bad := range []string{"base.css", "screens.css"} {
		for _, label := range []string{"Scope", "Files"} {
			if clause, _ := mdClause(t, planPath, planTasks[1], label); strings.Contains(clause, bad) {
				t.Errorf("%s: Task 6.2's **%s** names %s, and that file does not exist until a later task creates it", planPath, label, bad)
			}
		}
	}

	// The window background: the assertion is Task 6.5's, because the literals
	// are, and Task 6.1's paragraphs must not be about them.
	for _, label := range []string{"Scope", "Files", "Tests"} {
		if clause, _ := mdClause(t, planPath, planTasks[0], label); strings.Contains(clause, "backgroundColor") {
			t.Errorf("%s: Task 6.1's **%s** names backgroundColor, which arrives with Task 6.5's consoleWindowOptions; the case that compares the two literals against the two --ground values lands in the task that adds them", planPath, label)
		}
	}
	if clause, ok := mdClause(t, planPath, planTasks[4], "Scope"); !ok || !strings.Contains(clause, "backgroundColor") {
		t.Errorf("%s: Task 6.5's **Scope** does not name backgroundColor; it is the task that adds both literals to the main process's window module, so a reader of the task would not know it owns the assertion", planPath)
	}
	if !strings.Contains(mdFlat(design), "This case is [Task 6.5](#75-task-65--the-theme-switch-the-window-background-and-the-keyboard)'s and not Task 6.1's") {
		t.Errorf("%s: §6.3 does not say the window-background case is Task 6.5's; a design that leaves the owner unnamed is a design a reader can implement in the wrong task", designPath)
	}

	// §6.2's account of the golden specs, and the dependency it justified. The
	// claim was that the two schemes cannot paint differently until Task 6.5
	// replaces `CONSOLE_THEME`, because every screen stamped `'dark'` in both
	// schemes. The source says the opposite: each spec draws its screen with the
	// theme it names and the screen stamps that argument on its root, so the light
	// run has always said `'light'`, and `CONSOLE_THEME` (entry.ts 76) is the
	// app's boot path, which none of the four specs reads. The frames are identical
	// because no stylesheet paints anything (§1.3) — which is the dependency the
	// task has: the stylesheet, not the theme switch.
	//
	// **The guards below are anchored to the live form of the claim, not to the
	// wording a previous revision happened to use.** The first draft of this
	// correction guarded on "they cannot until Task 6.5 wires" while the plan's
	// surviving sentences read "they cannot until the theme choice is wired" and
	// "belongs to the task that wires it" — so neither `strings.Contains` could
	// ever be true and the pair was inert against exactly the regression it was
	// written for: an implementer who reads Task 6.3 defers the assertion to the
	// task that "wires the theme choice", and Task 6.6's red step loses the
	// assertion it lands with. A guard is the sentence a reader would have to
	// write to be wrong again, and that is what each of these is; every one is
	// mutation-proven by re-planting its claim.
	flatDesign, flatPlan := mdFlat(design), mdFlat(plan)
	if !strings.Contains(flatDesign, "The four golden specs never read it") {
		t.Errorf("%s: §6.2 does not say the golden specs never read `CONSOLE_THEME`; the assertion's dependency is the stylesheet, and a reader who is told the theme switch is its precondition wires the wrong task", designPath)
	}
	if !strings.Contains(flatDesign, "already says `data-theme='light'` today") {
		t.Errorf("%s: §6.2 does not say the light run's root already carries `data-theme='light'`; each spec draws its screen with the theme it names (`mount(theLastFrame(), 'light')`) and `renderLive` stamps that argument, so the two runs already disagree about the attribute", designPath)
	}

	// And the two documents' Task 6.5 **Files** lists agree on the files the
	// *ruled* scope keeps. The ruling removed the switch, not the keyboard: the
	// nav's tablist behaviour and its roving `tabindex` are §4.3's, they live in
	// `entry.ts`, and the case that fails on a regression is `entry-wiring`'s —
	// which the plan's Task 6.5 names and the design's §7.5 stopped naming when
	// its clause was rewritten to the one-set form, leaving §7.5's own Scope and
	// Reviewer tier saying the entry is this task's while its Files said it was
	// untouched. A reader who takes the file list as the task's writes the
	// switch and skips the keyboard.
	if designFiles, ok := mdClause(t, designPath, designTasks[4], "Files"); !ok {
		t.Errorf("%s: §7.5 has no **Files** clause; every task in this phase states what it touches", designPath)
	} else if planFiles, ok := mdClause(t, planPath, planTasks[4], "Files"); !ok {
		t.Errorf("%s: Task 6.5 has no **Files** clause; every task in this phase states what it touches", planPath)
	} else {
		// The comparison is file for file, not a spot check: the design's clause
		// says it is the plan's list, and the round that added this pin found the
		// two lists disagreeing about *which* files the ruling keeps — the design
		// dropped `entry.ts` (and with it the keyboard the ruled scope still owns)
		// while the plan dropped the picker's script and the window unit test but
		// kept the entry. Two names agreeing is what a spot check shows; the third
		// file is where a list can still be wrong.
		designRuled, designRecord := mdFileLists(designFiles)
		planRuled, planRecord := mdFileLists(planFiles)
		if got, want := strings.Join(planRuled, ", "), strings.Join(designRuled, ", "); got != want {
			t.Errorf("%s: Task 6.5's **Files** names %s, and §7.5 names %s; §7.5's clause says it is the plan's list, so the two agree file for file — the ruled scope keeps the nav's tablist behaviour (§4.3, in the entry) and the roving-tabindex case, and drops the picker's script and the window unit test", planPath, got, want)
		}
		if got, want := strings.Join(planRecord, ", "), strings.Join(designRecord, ", "); got != want {
			t.Errorf("%s: Task 6.5's **Files** keeps %s as the record of the two-set scope, and §7.5 keeps %s; the record is what tells a reader which files left the list and why, so both documents keep the same one", planPath, got, want)
		}
	}
	for _, stale := range []struct{ doc, path, claim string }{
		// The design's own record of the retracted claim quotes it, so the only
		// wording that can be guarded here is the wording of a *re-planted*
		// assertion rather than of the record: the sentence that made the light
		// run the dark one, stated as the design's own fact.
		{flatDesign, designPath, "in a light-scheme run the root still says"},
		{flatDesign, designPath, "so the light run still says `data-theme='dark'`"},
		// The plan's two live forms, worded as Tasks 6.3 and 6.4 actually worded
		// them before this round — and the general shape they share, so a
		// re-worded reversal ("the task that wires CONSOLE_THEME") is caught too.
		{flatPlan, planPath, "cannot until the theme choice is wired"},
		{flatPlan, planPath, "belongs to the task that wires the theme choice"},
		{flatPlan, planPath, "every screen stamped its root `data-theme='dark'` in both schemes"},
		{flatPlan, planPath, "the task that wires it and records the two pictures"},
	} {
		if strings.Contains(stale.doc, stale.claim) {
			t.Errorf("%s: the document still carries %q; the golden specs pass the theme to the renderer themselves and never read `CONSOLE_THEME`, so neither the claim nor the dependency it supported holds", stale.path, stale.claim)
		}
	}
	// And the corrected claim is *required*, in the two tasks that carried the
	// wrong one: a paragraph that simply drops the sentence rather than fixing it
	// would leave the reader with no answer at all, and the guards above would
	// pass on the silence. Each clause has to say where the assertion's
	// precondition really is.
	for _, want := range []struct {
		task  int
		label string
		want  string
		why   string
	}{
		{2, "Tests", "what it waits for is the stylesheet, not", "Task 6.3's **Tests** clause must say the computed-background assertion waits on the stylesheet rather than on the theme choice; it is the paragraph an implementer reads to decide which task lands the assertion"},
		{3, "Tests", "it waits on the stylesheets that paint a screen root, not on the theme choice", "Task 6.4's **Tests** clause must repeat the corrected reason rather than point back at a rationale that no longer exists"},
	} {
		clause, ok := mdClause(t, planPath, planTasks[want.task], want.label)
		if !ok {
			t.Fatalf("%s: Task 6.%d has no **%s** clause; this check is about what that paragraph says", planPath, want.task+1, want.label)
		}
		if !strings.Contains(mdFlat(clause), want.want) {
			t.Errorf("%s: Task 6.%d's **%s** clause does not say %q; %s", planPath, want.task+1, want.label, want.want, want.why)
		}
	}
	if clause, ok := mdClause(t, planPath, planTasks[5], "Depends on"); !ok || !strings.Contains(clause, "Task 6.4 (**hard**)") {
		t.Errorf("%s: Task 6.6's **Depends on** does not name Task 6.4 as its hard dependency; the computed-background assertion reads a painted screen root, and the last rules that paint one land with Task 6.4's `screens.css`", planPath)
	}

	// And §6.5 counts this file, so the count is checked against the file rather
	// than trusted. The revision that fixed the ordering errors added the tenth
	// test and left the section at "nine tests", which is the same defect in the
	// other direction: a reader implementing the phase from the design writes
	// what the design enumerates, and the ordering test was in neither the count
	// nor the list.
	self, err := os.ReadFile("docs_console_styling_test.go")
	if err != nil {
		t.Fatalf("reading this test file to count its cases: %v", err)
	}
	cases := len(regexp.MustCompile(`(?m)^func TestDocsConsoleStyling\w*\(`).FindAll(self, -1))
	if cases == 0 {
		t.Fatalf("no TestDocsConsoleStyling case found in this file, which cannot be right: this test is one")
	}
	word, ok := mdNumberWord(cases)
	if !ok {
		t.Skipf("this file declares %d cases, more than §6.5's prose counts in words; enlarge mdNumberWord", cases)
	}
	if !strings.Contains(mdFlat(design), "with "+word+" tests") {
		t.Errorf("%s: §6.5 does not say this file has %s tests, and this file declares %d; the section's count and its enumerated list are what a reader implements the phase from, so a case that lands without a line there is a case nobody writes", designPath, word, cases)
	}
}

// mdNumberWord spells a small count the way §6.5's prose does, so the comparison
// is against the sentence rather than against a digit the document never prints.
func mdNumberWord(n int) (string, bool) {
	words := []string{"zero", "one", "two", "three", "four", "five", "six", "seven",
		"eight", "nine", "ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen",
		"sixteen", "seventeen", "eighteen", "nineteen", "twenty"}
	if n < 0 || n >= len(words) {
		return "", false
	}
	return words[n], true
}

// mdWordValue reads a small count back out of the prose, so a sentence's word
// and the list beside it can be compared. It is mdNumberWord's inverse and
// covers the same range: zero to twenty.
func mdWordValue(word string) (int, bool) {
	for n := 0; n <= 20; n++ {
		if w, ok := mdNumberWord(n); ok && w == word {
			return n, true
		}
	}
	return 0, false
}

// TestDocsConsoleStylingCountsMatch:
// every count this design prints in prose is checked against the thing it
// counts, because a count is a number and a number in a document rots the way a
// link does — silently, and in whichever direction the sentence beside it needs.
// The revision this test was written from is the evidence: §6.5 said "five that
// read §3.4's numbers" over a list numbered 5 to 10, and a reader counting the
// items after that sentence counted six.
//
// The checks are mechanical on purpose — items, rows and clauses, counted by
// their markers rather than by reading the sentences — because a test that
// agreed with the prose would agree with any prose. What it does not do is check
// counts of things outside the document (§3.4's colours, the eight baselines,
// the console's own test files): those are other cases' subjects, and this one
// is about the document keeping its own arithmetic.
func TestDocsConsoleStylingCountsMatch(t *testing.T) {
	const path = "docs/design/console-styling.md"
	doc := readDoc(t, path)

	// §6.5's enumeration: the ordered list runs 1..N with no gap, and N is the
	// number the section's opening line says in words.
	section := mdBetween(t, path, doc, "### 6.5 The docs test", "\n---\n\n## 7. ")
	itemNums := regexp.MustCompile(`(?m)^(\d+)\. `).FindAllStringSubmatch(section, -1)
	if len(itemNums) == 0 {
		t.Fatalf("%s: §6.5 has no ordered list; that list is the enumeration the section's count is about", path)
	}
	for i, m := range itemNums {
		if m[1] != strconv.Itoa(i+1) {
			t.Errorf("%s: §6.5's list jumps from %d to %s; the items are numbered consecutively, and a gap is how an item leaves the list without the count noticing", path, i, m[1])
		}
	}
	stated := regexp.MustCompile(`with (\w+) tests`).FindStringSubmatch(mdFlat(section))
	if stated == nil {
		t.Fatalf("%s: §6.5 does not say how many tests this file has; the count and the enumeration are what a reader implements the phase from", path)
	}
	want, ok := mdWordValue(stated[1])
	if !ok {
		t.Fatalf("%s: §6.5's count %q is not a word this test knows", path, stated[1])
	}
	if want != len(itemNums) {
		t.Errorf("%s: §6.5 says %s tests and enumerates %d items; a count that disagrees with the list under it is the defect this case exists for", path, stated[1], len(itemNums))
	}

	// The group inside that list, which is where the count and the enumeration
	// last drifted apart: "Items 5 to 9 — five of the ten — read §3.4's numbers".
	group := regexp.MustCompile(`Items (\d+) to (\d+) — (\w+) of the (\w+) —`).FindStringSubmatch(mdFlat(section))
	if group == nil {
		t.Errorf("%s: §6.5 no longer names the run of items that read §3.4's numbers as a range; without the range a reader counts the items after the sentence and gets the count wrong, which is exactly what happened here", path)
	} else {
		lo, errLo := strconv.Atoi(group[1])
		hi, errHi := strconv.Atoi(group[2])
		size, okSize := mdWordValue(group[3])
		total, okTotal := mdWordValue(group[4])
		switch {
		case errLo != nil || errHi != nil || !okSize || !okTotal:
			t.Errorf("%s: §6.5's group sentence could not be read: %q", path, group[0])
		default:
			if size != hi-lo+1 {
				t.Errorf("%s: §6.5 says %s items and names the range %d to %d, which holds %d", path, group[3], lo, hi, hi-lo+1)
			}
			if total != len(itemNums) {
				t.Errorf("%s: §6.5's group sentence calls the list \"the %s\" and the list holds %d items", path, group[4], len(itemNums))
			}
		}
	}
	// The sentence under the list that names the same group again, in the words
	// a reader remembers: it has to be the group's own word.
	if m := regexp.MustCompile(`these (\w+) read the file`).FindStringSubmatch(mdFlat(section)); m != nil {
		if group == nil || m[1] != group[3] {
			t.Errorf("%s: §6.5 says \"these %s read the file\" and the group it refers to is counted differently above; the two sentences name the same items", path, m[1])
		}
	} else {
		t.Errorf("%s: §6.5 no longer says how many of its cases read the file rather than the tables", path)
	}

	// §7.7's three counts: the table of red steps, the docs test's own mutation
	// list, and the ordering test's. Each is counted by its separators — table
	// rows and semicolons — rather than by trusting the sentence above it.
	reds := mdBetween(t, path, doc, "### 7.7 The red steps", "**And the ")
	rows := 0
	for _, line := range strings.Split(reds, "\n") {
		if strings.HasPrefix(line, "| ") && !strings.Contains(line, "---") && !strings.Contains(line, "Red step |") {
			rows++
		}
	}
	if m := regexp.MustCompile(`The (\w+) that\s*\n?\s*matter most`).FindStringSubmatch(reds); m != nil {
		if n, ok := mdWordValue(m[1]); !ok || n != rows {
			t.Errorf("%s: §7.7 says the %s red steps that matter most are tabulated and the table has %d rows", path, m[1], rows)
		}
	} else {
		t.Errorf("%s: §7.7 no longer says how many red steps it tabulates", path)
	}

	// The two mutation lists: each says a count in words and then lists that many
	// clauses, separated by semicolons. The span is taken raw — from the sentence
	// to the paragraph or the sentence that ends it — because a flattened
	// document has no paragraph ends left, and these lists carry code spans with
	// periods in them ("`Status: draft.`"), which a sentence-splitting regexp
	// would cut in half.
	for _, tc := range []struct {
		sentence string
		countRE  string
		start    string
		end      string
	}{
		{
			sentence: "this document's own docs test was proved against",
			countRE:  `\*\*And the (\w+) this document's own docs test was proved against`,
			start:    "**And the five this document's own docs test was proved against",
			end:      "The seventh is the plan's",
		},
		{
			sentence: "the ordering test's mutations",
			countRE:  `carries (\w+) of its own`,
			start:    "And the ordering test (§6.5's tenth)",
			end:      "\n---\n",
		},
	} {
		span := mdBetween(t, path, doc, tc.start, tc.end)
		m := regexp.MustCompile(tc.countRE).FindStringSubmatch(span)
		if m == nil {
			t.Errorf("%s: §7.7 no longer states the count for %s as a sentence this case can count", path, tc.sentence)
			continue
		}
		n, ok := mdWordValue(m[1])
		if !ok {
			t.Errorf("%s: §7.7's count for %s is %q, not a word this test knows", path, tc.sentence, m[1])
			continue
		}
		after := span[strings.Index(span, m[0])+len(m[0]):]
		if i := strings.Index(after, ":"); i >= 0 {
			after = after[i+1:]
		}
		clauses := len(strings.Split(strings.TrimSpace(after), ";"))
		if clauses != n {
			t.Errorf("%s: §7.7 says %s for %s and lists %d clauses; a count that disagrees with the list beside it is the defect this case exists for", path, m[1], tc.sentence, clauses)
		}
	}

	// And §6.5's tenth item counts those same mutations by pointing at §7.7, so
	// the two numbers have to be one number. The round that added the Files pin
	// bumped §7.7 to eight and left §6.5 saying seven; nothing failed, because
	// every check above counts a sentence against the list beside it and this
	// sentence's list is in the other section. A count is the same arithmetic
	// wherever it is written, including when it is written one indirection out.
	item10 := mdItem(t, path, doc, "### 6.5 The docs test", "\n---\n\n## 7. ", 10)
	pointed := regexp.MustCompile(`the (\w+) mutations are named`).FindStringSubmatch(mdFlat(item10))
	carried := regexp.MustCompile(`carries (\w+) of its own`).FindStringSubmatch(mdBetween(t, path, doc, "And the ordering test (§6.5's tenth)", "\n---\n"))
	if pointed == nil {
		t.Errorf("%s: §6.5's tenth item no longer says how many mutations it is proved by and where they are named; the sentence a reader follows to §7.7 is what this check reads", path)
	} else if carried == nil {
		t.Errorf("%s: §7.7 no longer states the ordering test's mutation count, so §6.5's %q cannot be checked against it", path, pointed[0])
	} else {
		here, okHere := mdWordValue(pointed[1])
		there, okThere := mdWordValue(carried[1])
		switch {
		case !okHere || !okThere:
			t.Errorf("%s: the two counts of the ordering test's mutations are %q and %q, one of which is not a word this test knows", path, pointed[1], carried[1])
		case here != there:
			t.Errorf("%s: §6.5's tenth item says the %s mutations are named in §7.7 and §7.7 carries %s of its own; the two sentences count the same list, so a revision that bumps one has to bump the other", path, pointed[1], carried[1])
		}
	}

	// §5.3 and §5.6 name the places the bundle case counts the list it gains an
	// entry in. There are two counts and they are arithmetic — the packaged-files
	// list is the two preload paths plus `SERVED_FILES`, so its literal is the
	// list's plus two — and this case recomputes the second from the first rather
	// than reading the sentence that introduces them. A section that names one
	// literal and not the other is how a reader changes one count and misses the
	// second, which is what §5.3 did before this check.
	serveSection := mdFlat(mdBetween(t, path, doc, "### 5.3 The one new entry", "### 5.4 "))
	servedRE := regexp.MustCompile(`expect\(SERVED_FILES\.length, …\)\.toBe\((\d+)\)`)
	requiredRE := regexp.MustCompile(`expect\(required\.length, …\)\.toBe\((\d+)\)`)
	servedM := servedRE.FindStringSubmatch(serveSection)
	requiredM := requiredRE.FindStringSubmatch(serveSection)
	switch {
	case servedM == nil:
		t.Errorf("%s: §5.3 no longer gives `SERVED_FILES.length`'s new literal; that count is one of the places the test changes", path)
	case requiredM == nil:
		t.Errorf("%s: §5.3 gives `SERVED_FILES.length`'s new literal and not the packaged-files list's — `required.length` is derived from `SERVED_FILES`, so it moves with the list and a reader who is told of one count only changes one of the two", path)
	default:
		served, errS := strconv.Atoi(servedM[1])
		required, errR := strconv.Atoi(requiredM[1])
		if errS != nil || errR != nil {
			t.Errorf("%s: §5.3's two count literals could not be read: %q, %q", path, servedM[0], requiredM[0])
		} else if required != served+2 {
			t.Errorf("%s: §5.3 says `SERVED_FILES.length` becomes %d and `required.length` becomes %d; the packaged list is the two preload paths plus the served list, so the second is the first plus two", path, served, required)
		}
	}
	// The row is found by its file name rather than by its prefix: the prefix
	// starts with a path under the console directory, and the boundary case's
	// allow-list is a closed exception this file is not on.
	bundleRow := ""
	for _, line := range strings.Split(mdBetween(t, path, doc, "### 5.6 ", "\n---\n\n## 6. "), "\n") {
		if strings.HasPrefix(line, "| ") && strings.Contains(line, "bundle.test.ts") {
			bundleRow = line
		}
	}
	if bundleRow == "" {
		t.Errorf("%s: §5.6 no longer has a row for the bundle case; that row is where the section repeats the counts §5.3 gives", path)
	} else if !strings.Contains(bundleRow, "required.length") {
		t.Errorf("%s: §5.6's bundle row repeats the `SERVED_FILES.length` count and leaves out `required.length`; the second moves with the list too", path)
	}

	// And the document's own shape: §6.5's first item counts the section
	// headings, so the count is taken from the headings themselves.
	headings := len(regexp.MustCompile(`(?m)^## `).FindAllString(doc, -1))
	if m := regexp.MustCompile(`this document's (\w+) section headings`).FindStringSubmatch(mdFlat(doc)); m != nil {
		if n, ok := mdWordValue(m[1]); !ok || n != headings {
			t.Errorf("%s: §6.5 says this document has %s section headings and it has %d", path, m[1], headings)
		}
	} else {
		t.Errorf("%s: §6.5's presence case no longer counts this document's section headings", path)
	}
}

// mdBetween returns the part of doc that starts at start, the marker included,
// and stops before end. Both markers must be there: a missing one is a document
// this test can no longer read, and a silent empty slice would make every check
// over it vacuous. The markers are raw — newlines and all — because the callers
// count what is between them, and a flattened copy has no line ends left to
// count.
func mdBetween(t *testing.T, path, doc, start, end string) string {
	t.Helper()
	i := strings.Index(doc, start)
	if i < 0 {
		t.Fatalf("%s: %q is not in the document; this test reads what follows it", path, start)
	}
	rest := doc[i:]
	j := strings.Index(rest[len(start):], end)
	if j < 0 {
		t.Fatalf("%s: %q does not end before %q, so this test cannot tell where the section stops", path, start, end)
	}
	return rest[:len(start)+j]
}

// mdItem returns the text of one numbered item of a section's ordered list, from
// its own number to the next item's, so a case reads the item it names rather
// than the whole list around it.
func mdItem(t *testing.T, path, doc, sectionStart, sectionEnd string, n int) string {
	t.Helper()
	section := mdBetween(t, path, doc, sectionStart, sectionEnd)
	start := regexp.MustCompile(`(?m)^` + strconv.Itoa(n) + `\. `).FindStringIndex(section)
	if start == nil {
		t.Fatalf("%s: the section starting %q has no item %d; this test reads that item", path, sectionStart, n)
	}
	rest := section[start[1]:]
	next := regexp.MustCompile(`(?m)^` + strconv.Itoa(n+1) + `\. `).FindStringIndex(rest)
	if next == nil {
		t.Fatalf("%s: item %d of the section starting %q does not end before an item %d, so this test cannot tell where it stops", path, n, sectionStart, n+1)
	}
	return section[start[0] : start[1]+next[0]]
}

// taskIndex maps a task's name to its position among phase 6's six tasks, so a
// case can name the task it is about rather than an index into the cut.
func taskIndex(t *testing.T, task string) int {
	t.Helper()
	for i, name := range []string{"6.1", "6.2", "6.3", "6.4", "6.5", "6.6"} {
		if name == task {
			return i
		}
	}
	t.Fatalf("taskIndex: %q is not one of phase 6's six tasks", task)
	return 0
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
// on the other side of TestConsoleIsOutsideTheGoModule's closed allow-list,
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

// mdSeparatedArm names the arm of §3.4's disjunction a pair takes — "hue" when
// the two hues are at least 50° apart, "luminance" when they are not but the
// luminance contrast between them is at least 3:1 — and whether the pair takes
// either arm at all. Both thresholds are inclusive: the rule is written "at
// least 50°", "at least 3:1".
func mdSeparatedArm(gap, contrast float64) (arm string, separated bool) {
	if gap >= 50 {
		return "hue", true
	}
	if contrast >= 3 {
		return "luminance", true
	}
	return "", false
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
		"(?m)^\\| `(--[a-z-]+)` / `(--[a-z-]+)` \\| ([0-9.]+)° \\| ([0-9.]+) \\| ([a-z]+) \\|")
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

	// The rule §3.4 states, pinned to the two numbers this test computes with:
	// the rule and the arithmetic cannot drift apart silently.
	if !strings.Contains(doc, "at least 50° apart in hue, _or_ at least 3:1") {
		t.Errorf("%s: §3.4 no longer states the 50°-or-3:1 rule this test checks the state colours against", path)
	}
}

// TestDocsConsoleStylingStateSeparation: the ten pairs of state colours in each
// set, as §3.4 prints them, are the ten pairs the literals make — the hue
// distance to a tenth of a degree, the luminance contrast to two decimals — and
// every one of them satisfies the rule the section states. This is the claim the
// first revision of the design got wrong, and the reason it is a table of
// computed numbers now rather than a sentence.
//
// The rule is a disjunction and this enforces it as one: **50° of hue, or 3:1 of
// luminance contrast where hue cannot separate the pair.** So the case fails on a
// pair that misses both, and on a row that claims the wrong arm, but it does not
// demand 3:1 of a pair that already clears 50° — the design does not state that,
// the ten present pairs do not meet it, and a test that pinned it would refuse a
// future token taking the documented fallback. The message it fails with is
// still the section's own: change the token, not the rule.
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

			contrast := mdRatio(t, tc.set[a], tc.set[b])
			if got, err := strconv.ParseFloat(m[4], 64); err != nil {
				t.Fatalf("%s: %s/%s: %v", path, a, b, err)
			} else if math.Abs(got-contrast) > 0.005 {
				t.Errorf("%s: the %s pair table prints %s / %s at %s:1 luminance contrast, and the literals compute %.2f:1", path, tc.name, a, b, m[4], contrast)
			}

			// §3.4's rule is a disjunction — 50° of hue, **or** 3:1 of luminance
			// contrast where hue cannot separate the pair — so this accepts the
			// documented fallback rather than pinning one arm of it. A pair that
			// fails both is the one thing the rule forbids, and a pair that
			// clears the first need not clear the second: none of the ten does
			// today, and demanding it would be a rule the design does not state.
			// The decision itself is mdSeparatedArm, which the case below tests
			// on its own — no pair in the document takes the fallback, so a
			// mutation of the document alone could not reach that arm.
			arm, separated := mdSeparatedArm(gap, contrast)
			if !separated {
				t.Errorf("%s: the %s set's %s and %s are %.1f° apart and %.2f:1 apart; §3.4's rule needs one of the two — 50° of hue, or 3:1 of luminance as the fallback — so change the token, not the rule", path, tc.name, a, b, gap, contrast)
			}
			// And the row says which arm it took, so a fallback pair cannot be
			// tabulated as if hue had separated it.
			if separated && m[5] != arm {
				t.Errorf("%s: the %s pair table says %s / %s are separated by %q, and the literals say %q (%.1f° of hue, %.2f:1 of luminance)", path, tc.name, a, b, m[5], arm, gap, contrast)
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

// TestDocsConsoleStylingSeparationArms: the two arms of §3.4's rule, on their
// own. The document's own ten pairs per set all take the hue arm — every state
// colour has to clear 4.5:1 as text on all three surfaces, which puts them all
// within a narrow luminance band, so no pair of the five present colours is 3:1
// apart in luminance — and so no edit to the document alone can exercise the
// fallback branch. Testing the decision directly is what makes it a tested
// branch rather than dead code, and pins both thresholds as inclusive, which is
// how §3.4 writes them.
func TestDocsConsoleStylingSeparationArms(t *testing.T) {
	for _, tc := range []struct {
		gap, contrast float64
		arm           string
		separated     bool
		why           string
	}{
		{120, 1.1, "hue", true, "hue alone separates these"},
		{50, 1.1, "hue", true, "the rule is \"at least 50°\", so 50° clears it"},
		{49.9, 3.2, "luminance", true, "under 50° of hue, but 3:1 is what the fallback needs and this pair is over it"},
		{49.9, 3, "luminance", true, "the rule is \"at least 3:1\", so exactly 3:1 clears it"},
		{49.9, 2.99, "", false, "under both thresholds, which is the one thing §3.4 forbids"},
		{0, 1, "", false, "identical colours are the far end of the same failure"},
		{0, 21, "luminance", true, "nil hue separation is the case the fallback exists for"},
	} {
		arm, separated := mdSeparatedArm(tc.gap, tc.contrast)
		if separated != tc.separated || arm != tc.arm {
			t.Errorf("%.1f° of hue and %.2f:1 of luminance: mdSeparatedArm says (%q, %v), and §3.4 says (%q, %v) — %s", tc.gap, tc.contrast, arm, separated, tc.arm, tc.separated, tc.why)
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

	// The sentences are searched in the flattened document, so a rewrap is not a
	// failure: the claim is the figure beside its background, and the figures
	// themselves are recomputed just above. Pinning the wrap would tie this case
	// to a column width, which is not what it is about.
	for _, want := range []string{
		// Each figure beside the background it belongs to.
		"`#17A8E6` on **white** is **2.70:1**",
		"`#F2F6FA` the same value is **2.49:1**",
		// And the token the light set names for accent text, since the accent
		// itself cannot be it.
		"**`--accent-ink` `#085A82`** is nevertheless the tone for accent text",
	} {
		if !strings.Contains(mdFlat(doc), want) {
			t.Errorf("%s lacks %q", path, want)
		}
	}
	if ink := light["--accent-ink"]; ink != "#085A82" {
		t.Errorf("%s: the light set's --accent-ink is %q, want #085A82, the tone §3.3 names for accent text on a light ground", path, ink)
	}
}

// TestDocsConsoleStylingRailIsSpecified:
// the owner approved the Claude Design canvas on 2026-10-19, and the rail was the
// one structural addition it made to this document; the owner's ruling of
// 2026-10-28 moved the rail's tree into the redesign's header switcher, so the
// block now states that, and the rail's tree, keys and words, inside the menu. A rail is a region a
// stylesheet is written against *and* a keyboard drives, so §4.1 has to state
// the things an implementer writes it from — that it is a left vertical rail,
// its width, the three state words with the glyph each carries, the shortcut
// keys that select by position, and the read-only rule the canvas keeps — rather
// than leaving the canvas's one drawing to a reader's memory. The three words
// and their glyphs are read off the table §4.1 draws them in, row by row, so a
// glyph moved to the wrong word is a failing case rather than a plausible table.
//
// Its red state is the rail block's absence from §4.1, which is how it was
// written, and the mutations it is proved against are named in §7.7.
func TestDocsConsoleStylingRailIsSpecified(t *testing.T) {
	const path = "docs/design/console-styling.md"
	doc := readDoc(t, path)
	const railMarker = "**The installation rail — now the switcher's menu.**"
	rail := mdBetween(t, path, doc, railMarker, "### 4.2 Narrow windows")
	flat := mdFlat(rail)

	for _, want := range []struct{ has, because string }{
		{"replaced it with the redesign's header switcher", "the block records the ruling that moved the rail, so a reader of the canvas's left rail learns where it went"},
		{"`div.console-switcher-menu`", "the rail's tree is drawn inside the switcher's menu, and the block names the element that holds it"},
		{"`aside.console-rail`", "the rail is one element the entry draws, and its class is the hook a rule selects"},
		{"`div.console-rail-list`", "the list is the rail's tablist, and it is a different element from the rail: a header row and a foot row inside a tablist are children a tablist may not have"},
		{"Switch with the shortcut keys 1 2 3", "the hint is drawn text, so it is a string an implementer copies rather than one they write"},
		{"Command-1", "the shortcut's macOS form is a fact about the platform, and a shortcut described as \"the shortcut keys\" alone is one implemented as the wrong modifier"},
		{"Control-1", "and its form elsewhere is the other half of the same fact"},
		{"READ ONLY", "the canvas adds a region to a read-only console, and a rail that selects is the place a write could quietly arrive"},
		{"+ Add installation", "the foot row's label is drawn text, and it is the row that opens the add-installation flow the console already has"},
		{"role=\"tablist\"", "the rail is a tablist in its own right, which is what makes the arrow keys and a roving tabindex its behaviour rather than the nav's"},
		{"roving `tabindex`", "a tablist that tabs through every row is the defect this section exists to prevent"},
		{"Enter selects", "the arrow keys move focus and Enter is what chooses, which is the manual-activation form of the pattern"},
		{"**Home** and **End**", "Home and End are part of the tablist's pattern and are the two keys a list of twelve rows is actually navigated with — pinned as the one phrase, so a block that drops either key fails rather than passing on the other"},
	} {
		if !strings.Contains(flat, want.has) {
			t.Errorf("%s: §4.1's rail block does not say %q; %s", path, want.has, want.because)
		}
	}

	// The three state words, each read off its own row of the table §4.1 draws
	// them in, so the word, the glyph and the tone have to be one row's. The
	// glyph is what §3.5's "never colour alone" rule leans on here: the word is
	// drawn beside it, and the collapsed rail keeps the glyph and the row's
	// accessible name.
	for _, want := range []struct{ word, glyph, token, because string }{
		{"`running`", "●", "`--ok`", "the dot is the canvas's mark for the installation the console is reading now"},
		{"`silent`", "▲", "`--warn`", "the triangle is the canvas's mark for the installation whose feed has gone quiet, and a silent row drawn as a dot is the one state an operator must not miss"},
		{"`idle`", "○", "`--text-muted`", "the open circle is the canvas's mark for a stored installation nothing is being read from"},
	} {
		row := mdRow(t, path, rail, "| "+want.word+" |")
		for _, cell := range []struct{ has, because string }{
			{want.glyph, want.because},
			{want.token, "every glyph in this table is drawn in a tone the palette already has; a row that names none is a rule written from a colour §3.2 does not have"},
		} {
			if !strings.Contains(row, cell.has) {
				t.Errorf("%s: §4.1's rail table draws %s without %s on its own row; %s", path, want.word, cell.has, cell.because)
			}
		}
	}

	// The rail is a child of the body, so every count of the body's children in
	// the document is a count this section changes. §2.1 states the shape the e2e
	// spec asserts and §4.1 states how many the entry appends; a document that
	// says three in one place and five in another is a document an implementer
	// writes the assertion from by choosing a section.
	chrome := mdFlat(mdBetween(t, path, doc, "### 2.1 ", "### 2.2 "))
	for _, want := range []struct{ has, because string }{
		{"'console-header', 'console-page'", "§2.1 quotes the body-children assertion, and since the header switcher replaced the rail the body holds the header and the page"},
		{"`aside.console-rail`", "§2's inventory is the licence for a selector, so the rail's own element is a row of §2.1's table and not a hook that lives only in §4.1's tree"},
		{"`div.console-rail-list`", "and its list with it, which is the element a rule styles and the tablist the keyboard task gives its keys"},
		{"`button.console-rail-row`", "and the row, which is where the state word, the name, the project and the spend are drawn"},
		{"`span.console-rail-state`", "and the span that carries the word and the glyph §3.5's never-colour-alone rule leans on"},
	} {
		if !strings.Contains(chrome, want.has) {
			t.Errorf("%s: §2.1 does not name %s; %s", path, want.has, want.because)
		}
	}
	if strings.Contains(chrome, "'console-rail', 'console-installation'") {
		t.Errorf("%s: §2.1 still quotes the rail-era body children; the header switcher replaced the rail and the body holds the header and the page", path)
	}

	// The tree §4.1 draws is a count: thirteen classed elements, twelve distinct
	// classes (the foot row and its button share `console-rail-add`), and §2.1
	// lists the same hooks. The document said "six parts" of the same tree while
	// §2.1 said thirteen hooks, and nothing counted either — so both sentences are
	// read against the fence they describe here, and a tree that gains or loses an
	// element has to move both numbers with it.
	var tree string
	if i := strings.Index(rail, "```"); i >= 0 {
		if j := strings.Index(rail[i+3:], "```"); j >= 0 {
			tree = rail[i+3 : i+3+j]
		}
	}
	if tree == "" {
		t.Fatalf("%s: §4.1's rail block has no fenced tree; that fence is what the two counts below are read from", path)
	}
	classRE := regexp.MustCompile(`\.([a-z][a-z0-9-]+)`)
	elements, classes := 0, map[string]bool{}
	for _, line := range strings.Split(tree, "\n") {
		found := classRE.FindAllStringSubmatch(line, -1)
		if len(found) == 0 {
			continue
		}
		elements++
		for _, m := range found {
			classes[m[1]] = true
		}
	}
	treeFields := mdFlat(tree)
	elementsWord, elementsOK := mdNumberWord(elements)
	classesWord, classesOK := mdNumberWord(len(classes))
	if !elementsOK || !classesOK {
		t.Fatalf("%s: §4.1's rail tree has %d classed elements and %d classes, which this test cannot spell", path, elements, len(classes))
	}
	structure := mdFlat(mdBetween(t, path, doc, "### 4.1 The window's structure", "### 4.2 Narrow windows"))
	whole := structure + " " + chrome
	for _, want := range []struct{ has, because string }{
		{"**" + elementsWord + " classed elements**", "§4.1 states how many elements its tree has, and a reader counts them against the fence: a count that names no reading is a count that rots"},
		{"**" + classesWord + " classes**", "and how many classes those elements carry, which is the number of hooks a stylesheet selects"},
		{"**" + classesWord + " hooks on " + elementsWord + " elements**", "§2.1's row states the same two numbers, because §2 is the list a rule is written against and a row that disagrees with §4.1's tree is a licence for a hook that is not there"},
	} {
		needle := want.has
		if !strings.Contains(whole, needle) && !strings.Contains(strings.ToLower(whole), strings.ToLower(needle)) {
			t.Errorf("%s: the rail's tree and its inventory do not agree — %q is in neither §4.1 nor §2.1; %s", path, want.has, want.because)
		}
	}
	if !strings.Contains(treeFields, "one tab each") {
		t.Errorf("%s: §4.1's tree no longer draws one tab per row; the rows are the tablist's tabs, and the fence is where the shape is read from", path)
	}

	// The rail's rows are every installation the CLI listed or the console holds,
	// readable or not (owner's design ruling of 2026-10-25, which put
	// `installation.list` on the dashboard's surface). Task 6.3's plan clause is
	// the one-row rail it built before that ruling, and it keeps its own wording;
	// neither document may promise a row per *stored* installation alone.
	planDoc := readDoc(t, "docs/plans/2026-10-15-desktop-console.md")
	planPath := "docs/plans/2026-10-15-desktop-console.md"
	if !strings.Contains(structure, "one row per installation the CLI listed or this console holds, readable or not") {
		t.Errorf("%s: §4.1 does not say the rail draws one row per installation the CLI listed or this console holds, readable or not; that is the owner's ruling of 2026-10-25, and the sentence the rail's cases are written from", path)
	}
	if strings.Contains(structure, "or draws the row it *can* read") {
		t.Errorf("%s: §4.1 still offers the rail task a choice between landing a new read and drawing the row it can read; the two branches disagree about §11 and about Task 6.3's test, so the block has to state one of them", path)
	}
	for _, d := range []struct{ path, text string }{{path, doc}, {planPath, planDoc}} {
		if strings.Contains(mdFlat(d.text), "one row per stored installation") {
			t.Errorf("%s: the document still requires one row per stored installation; the dashboard cannot read the stored list — `installation.current` takes no argument, and the picker's channels are gated to the picker's own document — so the case has to be written against the installation the console can read", d.path)
		}
	}
	if !strings.Contains(mdFlat(planDoc), "one row per installation the dashboard can read") {
		t.Errorf("%s: Task 6.3's **Tests** clause does not state the row the rail draws; a case written from §4.1 and a clause written against the stored list are two different tasks", planPath)
	}

	for _, want := range []struct{ has, because string }{
		{"up to **three** children", "§4.1 is where the count is stated, and the banner, the header and the page are three"},
		{"the other two children", "in refused mode the banner is the whole document, so the two children §4.1 counts are the ones never appended"},
	} {
		if !strings.Contains(structure, want.has) {
			t.Errorf("%s: §4.1 does not say %q; %s", path, want.has, want.because)
		}
	}
	// §4.2's three figures are viewport widths written in `em` against the root
	// size, and the rail is 216 px of each. A document that calls them the
	// content column's is a document whose layers are read as firing at the
	// column width they no longer describe.
	if strings.Contains(structure, "breakpoints are the *content column's*") {
		t.Errorf("%s: §4.1 calls §4.2's breakpoints the content column's; they are the window's", path)
	}
	narrow := mdFlat(mdBetween(t, path, doc, "### 4.2 ", "### 4.3 "))
	for _, want := range []struct{ has, because string }{
		{"the content column is the whole window", "§4.2 is where the rail's old 216 px subtraction is retired, so the three figures read as the window's widths they always named"},
		{"overlays the page instead of taking a column from it", "and why: the switcher's menu takes no column, so a layer never fires against a narrower one"},
	} {
		if !strings.Contains(narrow, want.has) {
			t.Errorf("%s: §4.2 does not say %q; %s", path, want.has, want.because)
		}
	}

	// `--ok` was bound to one reading before the rail, and the rail's running row
	// is a second. The tone's role cell and §3.5's rule are the two places the
	// binding is stated, so a rail drawn in a tone whose role does not include it
	// is a rule the document cannot justify.
	okRow := mdRow(t, path, mdBetween(t, path, doc, "### 3.2 ", "### 3.3 "), "| `--ok` |")
	if !strings.Contains(okRow, "the rail's `running` row") {
		t.Errorf("%s: §3.2's `--ok` role cell does not carry the rail's `running` row; the rail draws that state in `--ok`, and a state colour whose stated role it does not have is a colour used decoratively, which §3.5 rule 4 forbids", path)
	}
	state := mdFlat(mdBetween(t, path, doc, "### 3.5 ", "### 3.6 "))
	if !strings.Contains(state, "and for the rail's `running` row") {
		t.Errorf("%s: §3.5's rule 4 still binds `--ok` to measured-and-inside-cap alone; the rail's running row is the second reading, and the rule is what the implementer has to answer to", path)
	}

	// And the task that touches the entry: §7's ordering paragraph and §7.5's
	// reviewer tier both said 6.5 was the only one, and 6.3 draws the rail's
	// elements. The sentence is the ordering claim a reviewer reads first, so it
	// is pinned by name here rather than left to prose.
	order := mdFlat(mdBetween(t, path, doc, "**Order, and why it is this one.**", "### 7.1 Task 6.1"))
	if !strings.Contains(order, "6.3 appends the rail's elements") || !strings.Contains(order, "6.5") {
		t.Errorf("%s: §7's ordering paragraph does not name both tasks that touch the entry (6.3, which appends the rail's elements, and 6.5, which adds the attributes and the keys); the paragraph said 6.5 was the only one, and §7.3 puts entry.ts in 6.3's Files", path)
	}
	// A pattern rather than one sentence: §7.5's original wording is not §7's,
	// and a guard on either fixed string passes over the other — which is what
	// §7.7 claims cannot happen. Both documents are read, because the claim was
	// written in both (the plan's `J4` row carried a third wording).
	exclusive := regexp.MustCompile(`\b(?:only|sole|one)\s+tasks?\s+(?:that\s+|to\s+)?touch(?:es)?\s+(?:the\s+)?(?:renderer\s+)?entry\b`)
	for _, d := range []struct{ path, text string }{{path, doc}, {planPath, planDoc}} {
		if m := exclusive.FindString(mdFlat(d.text)); m != "" {
			t.Errorf("%s: the document still calls one task the sole owner of the entry (%q); two do — the rail's elements are Task 6.3's and the attributes and keys are Task 6.5's", d.path, m)
		}
	}

	// A hook is a class, and the rail has twelve of them on thirteen elements.
	// The clauses that derive the §2-inventory selector scan are where the list
	// the scan checks against is defined, so a clause that sends the scan after
	// "thirteen hooks" licenses a rule against a class the entry never emits —
	// the thirteen is the *element* count above, and §2.1 states twelve hooks.
	// All three clauses said thirteen, which is the count this reads back.
	planTasks := mdTaskSections(t, planPath, planDoc,
		"### Task 6.1: The token layer",
		"### Task 6.2: The wiring",
		"### Task 6.3: The layout",
		"### Task 6.4: The screens",
		"### Task 6.5: The theme switch",
		"### Task 6.6: The golden frames",
		"## Cross-task dependencies")
	// The emphasis markers are read through rather than around: the clause bolds
	// the count, so a guard on the bare phrase reads straight past the wording
	// the three clauses actually carried ("**thirteen** hooks").
	thirteenHooks := regexp.MustCompile(`\bthirteen\**\s+hooks\b`)
	for _, d := range []struct{ path, text string }{{path, doc}, {planPath, planDoc}} {
		if m := thirteenHooks.FindString(mdFlat(d.text)); m != "" {
			t.Errorf("%s: the document sends the selector scan after %q; the tree's thirteen are elements and the hooks are twelve (§2.1, and the count read off the fence above), so a scan written against thirteen styles a class nothing emits", d.path, m)
		}
	}
	for _, d := range []struct{ path, label, section string }{
		{path, "§7.4's **Tests**", mdBetween(t, path, doc, "### 7.4 ", "### 7.5 ")},
		{planPath, "Task 6.3's **Tests**", planTasks[2]},
		{planPath, "Task 6.4's **Tests**", planTasks[3]},
	} {
		clause, ok := mdClause(t, d.path, d.section, "Tests")
		if !ok {
			t.Fatalf("%s: %s has no **Tests** clause; the scan's list is derived there", d.path, d.label)
		}
		if flat := mdFlat(clause); !strings.Contains(flat, "twelve") {
			t.Errorf("%s: %s does not state the rail's twelve hooks; the clause is where the scan's list is derived, and a clause that names no count is the sentence that said thirteen", d.path, d.label)
		}
	}

	// And the same two facts decide what the row's fields are read from, because
	// one read cannot supply four fields: `ActiveInstallation` carries a project
	// and no display name and no spend, and the rail's state word is the rail's
	// own. A sentence that says one read answers all four is a sentence Task 6.3
	// is written from, and it cannot be implemented.
	if !strings.Contains(structure, "no one read answers all four") {
		t.Errorf("%s: §4.1 does not say that no one read answers the row's four fields; it claimed the dashboard's one installations read answered all four, and `ActiveInstallation` carries neither a spend nor a rail state word", path)
	}
	for _, want := range []struct{ has, because string }{
		{"`budget.show`", "§4.1 does not name the read behind the row's spend; the console's budget read is where those dollars come from, and a field with no read behind it is a field Task 6.3 invents"},
		{"no display name", "§4.1 draws a name and a project over a payload that keeps one label; the sentence has to say which one the row draws them from"},
	} {
		if !strings.Contains(structure, want.has) {
			t.Errorf("%s: %s", path, want.because)
		}
	}
	for _, d := range []struct{ path, text string }{{path, doc}, {planPath, planDoc}} {
		if strings.Contains(mdFlat(d.text), "installations read answers all four") {
			t.Errorf("%s: the document still says the dashboard's one installations read answers all four of the row's fields; that payload carries a project and no spend and no state word, so two of the four have no read in this design", d.path)
		}
	}

	// Two tasks touch the entry, so both carry the same reviewer note: 6.3 draws
	// the rail's elements into `entry.ts`, which is where a `.style` write or a
	// CSP loosening would have to appear, and §7.5 already says so of itself.
	// §7.3's tier said a bare "ordinary", and so did the plan's Task 6.3.
	for _, d := range []struct{ path, label, section string }{
		{path, "§7.3", mdBetween(t, path, doc, "### 7.3 ", "### 7.4 ")},
		{planPath, "Task 6.3", planTasks[2]},
	} {
		if tier := mdReviewerTier(t, d.path, d.section); !strings.Contains(tier, "§5.5") {
			t.Errorf("%s: %s's **Reviewer tier** does not name §5.5; it is one of the two tasks that touch the entry, and the rule that no style is written and the CSP is not loosened is what a reviewer of that file has to check", d.path, d.label)
		}
	}
}

// mdReviewerTier reads a task's **Reviewer tier** line. It is not one of the
// clauses mdClause knows — the tier is not a Scope, Files, Tests, Command,
// Verification, Depends on or Commit — and it is the sentence that decides what
// scrutiny a diff gets, so the tasks that touch the entry are read back here.
var mdReviewerTierRE = regexp.MustCompile(`^(?:- )?\*\*Reviewer tier:\*\*(.*)$`)

func mdReviewerTier(t *testing.T, path, section string) string {
	t.Helper()
	lines := strings.Split(section, "\n")
	for i, line := range lines {
		m := mdReviewerTierRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		// The note is a paragraph: the design's item indents its continuation
		// lines, the plan's run at column zero, and a blank line ends both.
		paragraph := []string{m[1]}
		for _, next := range lines[i+1:] {
			if strings.TrimSpace(next) == "" {
				break
			}
			paragraph = append(paragraph, next)
		}
		return mdFlat(strings.Join(paragraph, " "))
	}
	t.Fatalf("%s: this task has no **Reviewer tier** line; every task in this phase states the scrutiny its diff gets", path)
	return ""
}

// TestDocsConsoleStylingRailIsInThePlan:
// the rail is a change to two tasks of phase 6 and not to the design alone: its
// structure and the stylesheet's rules for it are Task 6.3's, and its keyboard —
// the tablist behaviour and the shortcut keys — is Task 6.5's, which is already
// the task that gives the nav its keys. A design that describes a region and a
// plan whose tasks do not name it is a region nobody builds, so the two scope
// paragraphs are read here.
//
// This case reads the plan as text for the same boundary reason the rest of this
// file does, and it names the tasks by their clauses rather than by their
// content: the plan is where a task's scope is stated.
func TestDocsConsoleStylingRailIsInThePlan(t *testing.T) {
	const planPath = "docs/plans/2026-10-15-desktop-console.md"
	plan := readDoc(t, planPath)
	tasks := mdTaskSections(t, planPath, plan,
		"### Task 6.1: The token layer",
		"### Task 6.2: The wiring",
		"### Task 6.3: The layout",
		"### Task 6.4: The screens",
		"### Task 6.5: The theme switch",
		"### Task 6.6: The golden frames",
		"## Cross-task dependencies")

	for _, want := range []struct{ task, label, has, because string }{
		{"6.3", "Scope", "installation rail", "the rail's structure and its rules are the layout task's, and a task that does not name the region is a task whose stylesheet is written against a tree nobody draws"},
		{"6.3", "Files", "src/renderer/entry.ts", "the rail's elements are the entry's — the same rule that puts `degraded-num` in Task 6.4's Files: the hook and the rule that selects it land in one commit"},
		{"6.5", "Scope", "shortcut keys", "the rail is a tablist, so its keys are the keyboard task's; a design that leaves the keys unassigned is a rail an operator can only click"},
	} {
		clause, ok := mdClause(t, planPath, tasks[taskIndex(t, want.task)], want.label)
		if !ok {
			t.Fatalf("%s: Task %s has no **%s** clause; this case is about what that clause says", planPath, want.task, want.label)
		}
		if !strings.Contains(mdFlat(clause), want.has) {
			t.Errorf("%s: Task %s's **%s** does not name %q; %s", planPath, want.task, want.label, want.has, want.because)
		}
	}
}
