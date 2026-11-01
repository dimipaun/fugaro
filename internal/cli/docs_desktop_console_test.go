package cli

// The console's Go-side contract tests (desktop-console plan, Task 0.1).
//
// This file is on Task 0.3's boundary test's allow-list as one of the Go files
// that may reference a path under console/ -- and it references it the way every
// docs_*_test.go here references docs/: readDoc(t, path) reads the bytes. It
// imports nothing from console/, embeds nothing from it and adds no go.mod
// dependency to reach it. Everything below that reads a console/ file reads it
// as bytes for the same reason, and the sections whose command is still in the
// core keep being read here: the one entry ee-reorg E2.1b added beside this file
// is the moved coordinator command's own console_contract_test.go, which reads
// that command's section and skips here (consoleMovedSections). The console's
// copy of a Go decision is checked here against the Go source, and the second
// such copy is
// TestDocsDesktopConsoleScrubVocabularyMatchesTheGoSource, reading
// console/tools/scrub-fixture/scrub.ts.
//
// Two halves, three test families. TestDocsDesktopConsoleDesignCarriesTheDecisions
// is the design's own presence test (design section 9.5 item 1): the load-bearing
// strings of docs/design/desktop-console.md. TestDocsDesktopConsoleContractNamesEveryWatchField
// is the stronger half this task adds: console/docs/json-contract.md must name
// every json tag of internal/watch/json.go's shapes, derived from the structs
// themselves rather than from a hand-kept list, so a rename in json.go fails a
// test here instead of silently breaking a screen.
//
// TestDocsDesktopConsoleDesignCarriesTheOwnerAnswers came later, with Task 5.0:
// section 10's answer column, read as the twenty-row table it is (four ruled
// cells compared exactly, the sixteen acceptances to the recommendation they
// accept, and no twenty-first row or empty cell admitted), plus the design's
// record that the owner's answers of 2026-10-16 do not reach this document.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/plan"
	"github.com/dimipaun/fugaro/internal/projectpolicy"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/testutil"
	"github.com/dimipaun/fugaro/internal/userstats"
	"github.com/dimipaun/fugaro/internal/watch"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"gopkg.in/yaml.v3"
)

// TestDocsDesktopConsoleDesignCarriesTheDecisions asserts the design holds
// what the plan's Task 0.1 says it holds: the auth decision (D1), the spawn
// allow-list, the five availability states, the security posture's hard rules
// and the owner-run table. A presence test, not a fidelity test -- a reviewer
// reads the diff for fidelity (design section 9.5).
func TestDocsDesktopConsoleDesignCarriesTheDecisions(t *testing.T) {
	const path = "docs/design/desktop-console.md"
	doc := readDoc(t, path)
	for _, tc := range []struct {
		what   string
		quotes []string
	}{
		{"the auth decision (D1: the CLI sidecar, never a credential in the app)", []string{
			"CLI sidecar (option A)",
			"FugaroSource",
			"direct RTDB/Firestore access with the operator's credentials (option B) never",
		}},
		{"the spawn allow-list, by exact argv", []string{
			"The spawn allow-list is exact argv, not a command name",
			"with no flags at all",
		}},
		{"the five availability states", []string{
			"**measured**",
			"**notional**",
			"**estimated**",
			"**unavailable**",
			"**gap**",
		}},
		{"the security posture's hard rules", []string{
			"contextIsolation: true",
			"nodeIntegration: false",
			"sandbox: true",
			"connect-src 'none'",
			"app://",
			"RunAsNode",
			"An IPC allow-list, not an IPC bridge",
			"Job-written text is text",
			"FUGARO_CONSOLE_SPAWN_LOG",
		}},
		{"the owner-run table, reducible to Checks 59a and 59b", []string{
			"## 7.5 Which steps are owner-run",
			"Check 59a",
			"Check 59b",
			"**owner only**",
		}},
	} {
		for _, want := range tc.quotes {
			if !strings.Contains(doc, want) {
				t.Errorf("%s no longer carries %s: it never says %q", path, tc.what, want)
			}
		}
	}
}

// TestDocsDesktopConsoleDesignCarriesTheOwnerAnswers is the plan's Task 5.0
// red step: the owner's answers of 2026-10-15 pinned where the design records
// them -- section 10's last column -- and the lines that record what the
// owner's answers of 2026-10-16 do to THIS design (nothing: D22, D34 and D21
// belong to docs/design/desktop-console-v2.md's D21-D38, and no row of
// section 10 moves).
//
// The column is read as a TABLE, not as prose, for the reason
// TestDocsDesktopConsoleDesignAllowListIsExactArgv documents: a substring
// check is satisfied by a sentence about the answers, so a cell could be
// emptied, flipped or a twenty-first row added with no answer at all and the
// test would still pass. Every one of the twenty rows is compared, in order,
// with its key and its answer; the four ruled cells exactly, and the sixteen
// acceptances as the recommendation they accept. D13's recommendation carries
// a bare "|" inside a code span (`example|show|layer|init|publish`), which
// docTableAfter splits on, so the answer is read as the row's LAST cell -- the
// column's position is what is fixed, not the cell count.
//
// Three acceptances carry an amendment appended to the cell, and each is
// pinned as the clause that must be there: D10's owner's ruling of 2026-10-20
// (Linux for 0.10.0, unsigned; Windows stays later), D12's Check 59 written as
// 59a and 59b, and D13's pairing check. An amendment is the record of a later
// ruling and it goes where the answer is, so a cell that lost one fails here
// rather than in review.
func TestDocsDesktopConsoleDesignCarriesTheOwnerAnswers(t *testing.T) {
	const path = "docs/design/desktop-console.md"
	doc := readDoc(t, path)

	for _, want := range []string{
		"| Owner's answer 2026-10-15 |",
		"the other sixteen accepted as recommended",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s no longer carries section 10's answer column: it never says %q", path, want)
		}
	}

	// The ruled cells, exactly as the owner ruled them, and the three acceptances
	// that carry a recorded amendment to the recommendation they accept.
	const accepted = "**Accepted as recommended.**"
	ruled := map[string]string{
		"**D1**":  "**CLI sidecar (option A), behind the `FugaroSource` interface; direct RTDB/Firestore access with the operator's credentials (option B) never.**",
		"**D3**":  "**This repository, under `console/`.**",
		"**D4**":  "**Direct download plus a Homebrew cask; signed, notarised and stapled.** Apple signing enters this repository's release path **tag-only**, behind `scripts/release-gate.sh` and a **protected GitHub environment**; **the owner holds the certificate and runs every signing step**. **The Mac App Store is out.**",
		"**D11**": "**Its own artifact, published from the CLI's `vX.Y.Z` tag, with a declared supported CLI range.**",
	}

	// The sixteen lines the plan calls "accepted as recommended", keyed to the
	// clause that must follow the acceptance where the design records one; ""
	// is the plain acceptance, and D10, D12 and D13 are the three with an
	// amendment -- D10's being the owner's Linux ruling of 2026-10-20 (Linux
	// ships from 0.10.0, unsigned; Windows stays later), which the design
	// appends to the acceptance rather than to the recommendation.
	acceptedSixteen := map[string]string{
		"**D2**": "", "**D5**": "", "**D6**": "", "**D7**": "", "**D8**": "",
		"**D9**":  "",
		"**D10**": "**Amended 2026-10-20 by the owner's ruling: Linux for 0.10.0, unsigned; Windows stays later.**",
		"**D12**": "with Check 59 written as 59a and 59b",
		"**D13**": "with the pairing check above recorded",
		"**D14**": "", "**D15**": "", "**D16**": "", "**D17**": "", "**D18**": "",
		"**D19**": "", "**D20**": "",
	}

	rows := docTableAfter(t, path, doc, "| Owner's answer 2026-10-15 |")
	if len(rows) != 20 {
		t.Errorf("%s's section 10 carries %d answer rows, want the twenty decisions D1 to D20", path, len(rows))
	}
	seen := map[string]bool{}
	for _, cells := range rows {
		key := cells[0]
		answer := cells[len(cells)-1]
		seen[key] = true
		if strings.Trim(answer, "*") == "" || strings.TrimSpace(answer) == "" {
			t.Errorf("%s's section 10 leaves %s unanswered: its answer cell is %q", path, key, answer)
			continue
		}
		if want, ok := ruled[key]; ok {
			// D1 alone carries a dated scope note appended on main (mobile D112,
			// 2026-10-17): the owner's ruling must still open the cell verbatim
			// and the note must say the console keeps D1.
			if key == "**D1**" {
				const note = "**Amended 2026-10-17 by mobile D112"
				if !strings.HasPrefix(answer, want+" "+note) || !strings.Contains(answer, "**The desktop console keeps D1**") {
					t.Errorf("%s's D1 answer must open with the ruling of 2026-10-15 verbatim, then the dated mobile D112 scope note that keeps D1 for the console:\n got %q\nwant prefix %q", path, answer, want+" "+note)
				}
				continue
			}
			if answer != want {
				t.Errorf("%s's %s answer is no longer the ruling of 2026-10-15:\n got %q\nwant %q", path, key, answer, want)
			}
			continue
		}
		clause, ok := acceptedSixteen[key]
		if !ok {
			t.Errorf("%s's section 10 carries an answer row for %s, which this test's twenty do not: a decision was added or renumbered", path, key)
			continue
		}
		if !strings.HasPrefix(answer, "**Accepted as recommended") {
			t.Errorf("%s's %s answer is no longer \"accepted as recommended\": it reads %q", path, key, answer)
			continue
		}
		if clause == "" && answer != accepted {
			t.Errorf("%s's %s answer gained a clause beyond the plain acceptance: %q", path, key, answer)
		}
		if clause != "" && !strings.Contains(answer, clause) {
			t.Errorf("%s's %s answer no longer records %q: it reads %q", path, key, clause, answer)
		}
	}
	for key := range ruled {
		if !seen[key] {
			t.Errorf("%s's section 10 no longer carries an answer row for %s", path, key)
		}
	}
	for key := range acceptedSixteen {
		if !seen[key] {
			t.Errorf("%s's section 10 no longer carries an answer row for %s", path, key)
		}
	}

	// What the answers of 2026-10-16 do to THIS design: nothing, and it says so
	// in two places -- the v2 note above section 1 and section 10's own prose.
	for _, want := range []string{
		"2026-10-16: the two answers to that spec's decisions",
		"**No answer of 2026-10-16 changes any row of §10 below**",
		"they do not reopen or amend anything below",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s no longer records what the answers of 2026-10-16 do here: it never says %q", path, want)
		}
	}
}

// jsonTagNames collects every json key of the named types, recursing into the
// structs, pointers and slices that recurse() admits so a nested shape's
// fields are covered too. Types recurse() refuses (time.Time and the like) are
// leaves: their internals are not this contract. An anonymous field with no
// json key of its own (secretEntry's embedded gcp.SecretInfo) contributes its
// inner keys rather than a key of its own.
func jsonTagNames(t *testing.T, recurse func(reflect.Type) bool, types ...reflect.Type) []string {
	t.Helper()
	seen := map[reflect.Type]bool{}
	var out []string
	var walk func(rt reflect.Type)
	walk = func(rt reflect.Type) {
		switch rt.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			walk(rt.Elem())
			return
		case reflect.Struct:
		default:
			return
		}
		if !recurse(rt) || seen[rt] {
			return
		}
		seen[rt] = true
		for i := range rt.NumField() {
			f := rt.Field(i)
			if !f.IsExported() {
				continue
			}
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "" || name == "-" {
				if f.Anonymous {
					walk(f.Type) // embedded: its keys are promoted into this shape
					continue
				}
				t.Errorf("%s.%s carries no json key: the contract cannot name it", rt.Name(), f.Name)
				continue
			}
			out = append(out, name)
			walk(f.Type)
		}
	}
	for _, rt := range types {
		walk(rt)
	}
	if len(out) == 0 {
		t.Fatal("no json keys collected; the walk lost the shapes")
	}
	return out
}

// samePackage returns a recurse predicate admitting the structs of one
// package, the shape the watch test wants (every nested watch shape).
func samePackage(pkg string) func(reflect.Type) bool {
	return func(rt reflect.Type) bool { return rt.PkgPath() == pkg }
}

// namedTypes returns a recurse predicate admitting exactly the given types, so
// a walk over, say, doctorOutput stops at its own keys instead of demanding
// that the document also name doctorProject's or pluginwire.Report's.
func namedTypes(types ...reflect.Type) func(reflect.Type) bool {
	allow := map[reflect.Type]bool{}
	for _, rt := range types {
		allow[rt] = true
	}
	return func(rt reflect.Type) bool { return allow[rt] }
}

// TestDocsDesktopConsoleContractNamesEveryWatchField: console/docs/json-contract.md
// must name every json key of every watch --json shape, backticked, so a
// rename in internal/watch/json.go fails here rather than quietly leaving a
// screen reading a field that no longer exists. The list is derived from the
// structs, not hand-kept.
func TestDocsDesktopConsoleContractNamesEveryWatchField(t *testing.T) {
	const path = "console/docs/json-contract.md"
	doc := readDoc(t, path)
	for _, name := range jsonTagNames(t, samePackage("github.com/dimipaun/fugaro/internal/watch"),
		reflect.TypeOf(watch.JSONDoc{}),
		reflect.TypeOf(watch.JSONConn{}),
		reflect.TypeOf(watch.JSONTotal{}),
		reflect.TypeOf(watch.JSONBurn{}),
		reflect.TypeOf(watch.JSONKill{}),
		reflect.TypeOf(watch.JSONRepo{}),
		reflect.TypeOf(watch.JSONRun{}),
		reflect.TypeOf(watch.JSONFinished{}),
	) {
		if !strings.Contains(doc, "`"+name+"`") {
			t.Errorf("%s never names the watch --json field %q (internal/watch/json.go has it)", path, name)
		}
	}
}

// TestDocsDesktopConsoleContractNamesTheOtherShapesFields: the same
// struct-derived check over the shapes the document describes in its own field
// tables -- the run list behind `runs ls --json` and `diagnose`'s row, a log
// line (whose keys the log reader is built against), the secrets inventory
// whose names come from an embedded gcp.SecretInfo, the local accounts
// document, the cancel result, and the budget documents. A key that exists in
// Go and not in the document fails here, and the document's own tables are
// what it is checked against, so a field listed there cannot be one the code
// does not have without this test's sibling below catching the mismatch.
func TestDocsDesktopConsoleContractNamesTheOtherShapesFields(t *testing.T) {
	const path = "console/docs/json-contract.md"
	doc := readDoc(t, path)
	shapes := []reflect.Type{
		reflect.TypeOf(runview.Row{}),
		reflect.TypeOf(runview.Totals{}),
		reflect.TypeOf(logLine{}),
		reflect.TypeOf(secretEntry{}),
		reflect.TypeOf(gcp.SecretInfo{}),
		reflect.TypeOf(accountsListDoc{}),
		reflect.TypeOf(cancelResult{}),
		reflect.TypeOf(budgetShowDoc{}),
		reflect.TypeOf(showGlobal{}),
		reflect.TypeOf(showDefaults{}),
		reflect.TypeOf(showRepo{}),
		reflect.TypeOf(showRun{}),
		reflect.TypeOf(showKill{}),
		reflect.TypeOf(doctorOutput{}),
		reflect.TypeOf(doctorCheck{}),
		reflect.TypeOf(configListDoc{}),
		reflect.TypeOf(configListInstallation{}),
		reflect.TypeOf(configListCheckout{}),
		reflect.TypeOf(plan.Document{}),
		reflect.TypeOf(plan.Node{}),
		reflect.TypeOf(plan.Edge{}),
		reflect.TypeOf(plan.Footprint{}),
		// The read's own document (V2.2.3): every key of what `plan graph
		// --json` prints, so a computed field the contract does not name fails
		// here too, not only the artifact's own.
		reflect.TypeOf(plan.Graph{}),
		reflect.TypeOf(plan.NodeState{}),
		reflect.TypeOf(plan.CriticalPath{}),
		reflect.TypeOf(runstore.ChangedFiles{}),
		// The rigor block and the rule inside it (docs/rigor-modes D24).
		reflect.TypeOf(runstore.RigorRecord{}),
		reflect.TypeOf(runstore.RigorArea{}),
		// §6.11, the backlog index's rows (CLI 0.11.0).
		reflect.TypeOf(backlogListDoc{}),
		reflect.TypeOf(backlogRow{}),
		// §6.12, the graph joined to the runs (GV1.6): the plan's own figures
		// and the three shapes under them, plus this read's document and its
		// totals.
		reflect.TypeOf(plan.StatePlan{}),
		reflect.TypeOf(plan.StateNode{}),
		reflect.TypeOf(plan.StateRelease{}),
		reflect.TypeOf(plan.StateStream{}),
		reflect.TypeOf(planStatusDoc{}),
		reflect.TypeOf(planStatusTotals{}),
		// §6.13's coordinator shapes moved to ee/coordinator with the command:
		// ee/coordinator/cli/console_contract_test.go pins them against the
		// tagged tree, which is the tree that still serves the console.
	}
	for _, name := range jsonTagNames(t, namedTypes(shapes...), shapes...) {
		if !strings.Contains(doc, "`"+name+"`") {
			t.Errorf("%s never names the json field %q, which the shape it documents carries", path, name)
		}
	}

	// §6.10 is held to its OWN field tables instead of to the whole document,
	// and the difference is the test: `project`, `by`, `rows` and `totals` are
	// words this contract uses in a dozen other sections, so a Contains over
	// all 1100 lines passes for a field §6.10 never lists — which is exactly
	// the drift this test exists to catch. The section's two tables are the
	// document shape and the row shape the counts are flattened into
	// (userstats.Row embeds Counts), and the comparison is set EQUALITY in both
	// directions: a Go key the section does not list fails, and a field row the
	// section lists and the code no longer has fails too — the sibling below
	// refuses invented names document-wide, and this is the same rule where the
	// runs stats shapes are described.
	const stats = "### 6.10 "
	body := section(t, doc, stats, "\n## 7. ")
	listed := map[string]bool{}
	for _, header := range []string{
		"| Field | Type | omitempty | Availability state |",
		"| Field | Type | omitempty | Note |",
	} {
		for _, row := range docTableAfter(t, path, body, header) {
			// A row's first cell can name several fields at once — the four
			// settled counts share one row (`succeeded`, `failed`, `halted`,
			// `infra_error`) — so every backticked name in it is one field of
			// the shape, not the cell's whole text.
			for _, m := range jsonCellRE.FindAllStringSubmatch(row[0], -1) {
				listed[m[1]] = true
			}
		}
	}
	statsShapes := []reflect.Type{
		reflect.TypeOf(userStatsDoc{}),
		reflect.TypeOf(userstats.Row{}),
		reflect.TypeOf(userstats.Counts{}),
	}
	keys := jsonTagNames(t, namedTypes(statsShapes...), statsShapes...)
	for _, name := range keys {
		if !listed[name] {
			t.Errorf("%s's §6.10 tables never list the json field %q, which the shape it documents carries", path, name)
		}
	}
	inGo := map[string]bool{}
	for _, name := range keys {
		inGo[name] = true
	}
	for _, row := range docTableAfter(t, path, body, "| Field | Type | omitempty | Availability state |") {
		for _, m := range jsonCellRE.FindAllStringSubmatch(row[0], -1) {
			if !inGo[m[1]] {
				t.Errorf("%s's §6.10 document table lists %q, which no field of userStatsDoc carries", path, m[1])
			}
		}
	}
}

// TestDocsDesktopConsoleContractNamesEveryReasonCode: §6.8 of the contract is
// what the console is written against, so the list of reasons it gives must be
// exactly the reasons `config list --json` can emit, and in the order it gives
// them — the order is what a reader uses to say which of a row's problems is
// *the* reason it is not valid. (An earlier revision of that block got the
// order wrong and omitted `unreadable` entirely, which is the drift this pins.)
//
// The list is read out of the numbered block §6.8 introduces ("One row carries
// one reason, and which one it is is fixed. In order:"), not searched for in
// the prose: the field table further up names the codes too, so a `Contains`
// over the whole document would pass with the ordered list wrong or a code
// missing from it.
//
// Mutations (run, restore): delete the `unreadable` item, or move
// `not_enrolled` off the end; each fails here. (Both leave the field table, and
// so a whole-document search, untouched.)
func TestDocsDesktopConsoleContractNamesEveryReasonCode(t *testing.T) {
	const path = "console/docs/json-contract.md"
	doc := readDoc(t, path)
	const intro = "**One row carries one reason, and which one it is is fixed.** In order:"
	i := strings.Index(doc, intro)
	if i < 0 {
		t.Fatalf("%s no longer states the order the reasons are reported in", path)
	}
	// The items that follow, up to the first line that is neither one nor a
	// wrapped continuation of one (an item's prose is indented under it).
	var got []string
	for _, line := range strings.Split(doc[i+len(intro):], "\n") {
		if line == "" || line[0] == ' ' || line[0] == '\t' {
			continue
		}
		m := reasonItemRE.FindStringSubmatch(line)
		if m == nil {
			break
		}
		got = append(got, m[1])
	}
	want := []string{
		checkoutNotRepresentable,
		checkoutMissing,
		checkoutUnreadable,
		checkoutNotACheckout,
		checkoutOtherProject,
		checkoutOtherRepository,
		checkoutNotEnrolled,
	}
	if !slices.Equal(got, want) {
		t.Errorf("%s's ordered reason list = %v, want %v", path, got, want)
	}
}

// reasonItemRE is one numbered item of §6.8's ordered list: "3. `unreadable` …".
var reasonItemRE = regexp.MustCompile("^[0-9]+\\. `([a-z_]+)`")

// jsonCellRE is a backticked identifier in a field table's first cell: the
// field names of one row, one or more of them.
var jsonCellRE = regexp.MustCompile("`([A-Za-z_][A-Za-z0-9_]*)`")

// consoleMovedSections are the document's headings whose command and shapes
// moved out of the core — the coordinator's with E2.1b, the report's with
// E2.2b: the code they document is under ee/, so this build cannot name a field
// or a flag of it and the checks below would be reading the shapes of a package
// they cannot import. The same checks run over these sections against the
// tagged tree, where the code is in the build
// (ee/coordinator/cli/console_contract_test.go, ee/report/cli/console_contract_test.go).
var consoleMovedSections = []string{"### 6.13 ", "## 4. `fugaro report --json`"}

// consoleMovedRows are the document's individual lines that name a flag of a
// moved command without sitting under a moved heading: §8's inventory carries
// one row per command the console may spawn, and the coordinator's and the
// report's rows state their flags there. Like consoleMovedSections, the moved
// packages check the same lines against the tagged tree.
var consoleMovedRows = []string{"| `coordinator status`", "| `report`"}

// inMovedSection answers whether heading opens one of consoleMovedSections.
func inMovedSection(heading string) bool {
	for _, h := range consoleMovedSections {
		if strings.HasPrefix(heading, h) {
			return true
		}
	}
	return false
}

// inMovedRow answers whether line is one of consoleMovedRows.
func inMovedRow(line string) bool {
	for _, r := range consoleMovedRows {
		if strings.Contains(line, r) {
			return true
		}
	}
	return false
}

// TestDocsDesktopConsoleContractNamesNoInventedField is the other direction,
// and it is the one a review found missing: every backticked name in the
// document's own field tables must be a real json key of the shape that table
// documents, so a field written from the design rather than read from the code
// (the `columns` a first revision invented on the run list) fails here instead
// of shipping a reader for a key no command emits.
func TestDocsDesktopConsoleContractNamesNoInventedField(t *testing.T) {
	const path = "console/docs/json-contract.md"
	doc := readDoc(t, path)
	real := map[string]bool{}
	for _, name := range jsonTagNames(t,
		samePackage("github.com/dimipaun/fugaro/internal/watch"),
		reflect.TypeOf(watch.JSONDoc{}), reflect.TypeOf(watch.JSONConn{}),
		reflect.TypeOf(watch.JSONTotal{}), reflect.TypeOf(watch.JSONBurn{}),
		reflect.TypeOf(watch.JSONKill{}), reflect.TypeOf(watch.JSONRepo{}),
		reflect.TypeOf(watch.JSONRun{}), reflect.TypeOf(watch.JSONFinished{}),
	) {
		real[name] = true
	}
	shapes := []reflect.Type{
		reflect.TypeOf(runview.Row{}), reflect.TypeOf(runview.Totals{}),
		reflect.TypeOf(logLine{}), reflect.TypeOf(secretEntry{}),
		reflect.TypeOf(gcp.SecretInfo{}), reflect.TypeOf(accountsListDoc{}),
		reflect.TypeOf(cancelResult{}), reflect.TypeOf(budgetShowDoc{}),
		reflect.TypeOf(showGlobal{}), reflect.TypeOf(showDefaults{}),
		reflect.TypeOf(showRepo{}), reflect.TypeOf(showRun{}), reflect.TypeOf(showKill{}),
		reflect.TypeOf(doctorOutput{}), reflect.TypeOf(doctorCheck{}),
		// The report's three shapes moved to ee/report with the command
		// (E2.2b), as §6.13's did with the coordinator; ee/report/cli's
		// console contract test carries them, as that one does.
		reflect.TypeOf(Diagnosis{}),
		// The degraded watch document: `degraded` and `notice` are its own
		// two keys (E2.2b — the report's JSONDoc, which spelled both, moved
		// to ee/report and no longer supplies them to this check).
		reflect.TypeOf(watchDegradedDoc{}),
		reflect.TypeOf(configListDoc{}), reflect.TypeOf(configListInstallation{}),
		reflect.TypeOf(configListCheckout{}),
		reflect.TypeOf(plan.Document{}), reflect.TypeOf(plan.Node{}),
		reflect.TypeOf(plan.Edge{}), reflect.TypeOf(plan.Footprint{}),
		// The read's own document (V2.2.3): the artifact's shape plus what is
		// computed where the graph is read.
		reflect.TypeOf(plan.Graph{}), reflect.TypeOf(plan.NodeState{}),
		reflect.TypeOf(plan.CriticalPath{}),
		reflect.TypeOf(runstore.ChangedFiles{}),
		// The rigor block and the rule inside it (docs/rigor-modes D24).
		reflect.TypeOf(runstore.RigorRecord{}), reflect.TypeOf(runstore.RigorArea{}),
		// §6.10, the same three shapes as the test above (CLI 0.9.0).
		reflect.TypeOf(userStatsDoc{}),
		reflect.TypeOf(userstats.Row{}),
		reflect.TypeOf(userstats.Counts{}),
		// §6.11, the backlog index's rows (CLI 0.11.0).
		reflect.TypeOf(backlogListDoc{}),
		reflect.TypeOf(backlogRow{}),
		// §6.12, the plan's graph joined to its runs (GV1.6).
		reflect.TypeOf(plan.StatePlan{}), reflect.TypeOf(plan.StateNode{}),
		reflect.TypeOf(plan.StateRelease{}), reflect.TypeOf(plan.StateStream{}),
		reflect.TypeOf(planStatusDoc{}), reflect.TypeOf(planStatusTotals{}),
		// §6.13's coordinator shapes moved to ee/coordinator with the command,
		// as in the test above.
	}
	for _, name := range jsonTagNames(t, namedTypes(shapes...), shapes...) {
		real[name] = true
	}
	// tableFieldRE finds every backticked token in a field-table row's first
	// column: "| `key` | type | ... |", "| `a`, `b` | ... |" or
	// "| `key` bool, `other` | ... |" alike. A token that looks like a json
	// key -- lowercase, digits and underscores only, so a Go type
	// (`runview.Row`), a package (`lsDoc`, `gcp.SecretInfo`) or a phrase
	// ("budget show") is not one -- must be a real key of some shape this
	// document describes.
	tableFieldRE := regexp.MustCompile("^\\|[^|]*\\|")
	keyTokenRE := regexp.MustCompile("^[a-z][a-z0-9_]*$")
	backtickRE := regexp.MustCompile("`([^`]+)`")
	// The rows whose first column is a command or a flag rather than a json
	// key: docs/design/desktop-console.md's spawn table and the flag
	// inventory, both of which name commands, not fields.
	notAKey := map[string]bool{"watch": true, "report": true, "diagnose": true,
		"logs": true, "doctor": true, "version": true}
	// approvedAheadOfTheCLI are the wire keys the document names under its own
	// bounded exception (§intro): the owner has approved a release that will
	// write them, no shape here carries them yet, and the console accepts them
	// as optional so that release does not refuse every document whole as
	// `unknown-key`. Admitting them HERE, in the guard, is the point: a reviewer
	// found the first such key documented in prose beside the tables, where this
	// test could not see it, so the exception was invisible to the one check
	// written to catch a field no command emits. The list is self-cleaning in
	// both directions below -- an entry that becomes a real json key fails
	// (its row now belongs to a shape), and one no field table names fails (the
	// exception must not outlive the documentation that justifies it).
	//
	// Empty since `leased_usd` became real (JSONTotal and JSONRepo carry it):
	// a blank entry fails the "no field table names it" half, so the list has
	// no placeholder. `compute_usd`, the other key the exception names, is
	// already a real key of `report --json` and so was never in here.
	approvedAheadOfTheCLI := map[string]bool{}
	checked := 0
	namedInTable := map[string]bool{}
	heading := ""
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "#") {
			heading = line
			continue
		}
		if inMovedSection(heading) {
			continue
		}
		m := tableFieldRE.FindString(line)
		if m == "" {
			continue
		}
		for _, tok := range backtickRE.FindAllStringSubmatch(m, -1) {
			name := tok[1]
			if !keyTokenRE.MatchString(name) || notAKey[name] {
				continue
			}
			checked++
			namedInTable[name] = true
			if !real[name] && !approvedAheadOfTheCLI[name] {
				t.Errorf("%s's field table names %q, which is not a json key of any shape it documents: a field invented rather than read", path, name)
			}
		}
	}
	for name := range approvedAheadOfTheCLI {
		if real[name] {
			t.Errorf("%s: %q is now a real json key of a shape this document describes; drop it from approvedAheadOfTheCLI and let its table row stand on the struct", path, name)
		}
		if !namedInTable[name] {
			t.Errorf("%s: approvedAheadOfTheCLI lists %q, which no field table names; the exception has to be documented where this test can see it", path, name)
		}
	}
	if checked < 100 {
		t.Errorf("%s: only %d field-table rows were checked; the table shape changed and this test stopped looking", path, checked)
	}
}

// TestDocsDesktopConsoleContractStatesTheThreeFeedFacts pins the three facts
// the plan's Task 0.1 requires by name -- each a named sub-assertion, so a
// later edit cannot drop one and leave the document shorter. Each is the
// code's, not an impression: the 1-second document gap is watchJSONGap, the
// narrowing is watch.FilterRepo, and the degraded mode is watchDegradedDoc
// plus runWatchDegraded's plain stdout sentence.
func TestDocsDesktopConsoleContractStatesTheThreeFeedFacts(t *testing.T) {
	const path = "console/docs/json-contract.md"
	doc := readDoc(t, path)
	for _, tc := range []struct {
		fact   string
		quotes []string
	}{
		{"one document per change, compact, at most one a second", []string{
			"one object per line",
			"watchJSONGap",
			"watchFirstFrame",
		}},
		{"--repo narrows the JSON too, through watch.FilterRepo", []string{
			"`--repo` narrows the JSON too",
			"FilterRepo",
			"finished[]` and `ready[]` ignore",
		}},
		{"the degraded mode emits a second shape and a non-JSON stdout line", []string{
			"watchDegradedDoc",
			"`degraded`",
			"`notice`",
			"run records unavailable (retrying)",
			"skip any line\nthat is not a JSON object",
		}},
	} {
		for _, want := range tc.quotes {
			if !strings.Contains(doc, want) {
				t.Errorf("%s does not state the feed fact %q: it never says %q", path, tc.fact, want)
			}
		}
	}
}

// TestDocsDesktopConsoleContractStatesTheRunsStatsOwnViewRefusal pins §6.10's
// own-view paragraph to the three states the command actually has. Two of them
// are the empty table -- a window the caller started nothing in, and an
// identity with no key of its own (nothing printable in it, or a text that
// reads as the unattributed key) -- and the third is not a state of `rows` at
// all: an identity the CLI cannot resolve (local config's user: empty, git
// config user.email answering nothing) is REFUSED before any document exists,
// with an error naming --all-users. A document that files the refusal with the
// empty table sends a console looking for a `rows: []` it will never receive.
// The help's own half of this is TestRunsStatsHelpStatesThePrivateViewsThreeWays
// (runs_stats_test.go), and the refusal itself is behaviour-pinned by
// TestRunsStatsVisibilityIsTheRuling's last case.
func TestDocsDesktopConsoleContractStatesTheRunsStatsOwnViewRefusal(t *testing.T) {
	const path = "console/docs/json-contract.md"
	doc := readDoc(t, path)
	start := strings.Index(doc, "**The `\"own\"` view filters a read that is not its own.**")
	end := strings.Index(doc, "`userstats.Row`")
	if start < 0 || end < 0 || end <= start {
		t.Fatalf("%s's own-view paragraph was renamed or reordered; update this test's markers", path)
	}
	para := doc[start:end]
	for _, want := range []string{
		"`rows: []`",              // the empty table, still stated
		"nothing printable in it", // the identity with no key of its own
		"**refused**",             // and the case that is not a table at all
		"`--all-users`",           // the way past it, as the error names it
	} {
		if !strings.Contains(para, want) {
			t.Errorf("%s's own-view paragraph does not say %q:\n%s", path, want, para)
		}
	}
	// The refusal's own wording, read out of the command rather than written
	// here, so the two cannot drift: the error a caller gets names --all-users
	// too, and that is what the paragraph claims it does.
	f := newCloudFixture(t)
	userStatsPlace(t, f, "", nil, nil)
	testutil.IsolateGit(t)
	t.Setenv("GIT_DIR", t.TempDir())
	out, _, err := execute(t, "runs", "stats", "--json")
	if err == nil || !strings.Contains(err.Error(), "--all-users") {
		t.Fatalf("no identity: err = %v, want the refusal %s's paragraph describes (out: %s)", err, path, out)
	}
}

// TestDocsDesktopConsoleContractStatesWhichDocumentsAreLineDelimited: the
// console's own set has exactly two line-delimited documents, and they do it
// through two different code paths -- the watch feed through json.NewEncoder
// with no SetIndent, the log lines through json.Marshal in printLogEntry. A
// document that claims only the feed is line-delimited, or that calls the log
// form indented, builds the log reader on the wrong parser, which is what a
// review of the first revision found here.
//
// The claim is scoped to the console's set, and the third line-delimited
// document this file describes -- `fugaro cancel RUN --json` (section 6.3),
// which is a write the console never spawns -- is stated separately and
// asserted here too. A second review found the document claiming "exactly two
// commands" without that scope while describing cancel four hundred lines
// earlier, so the absolute is pinned as scoped and cancel's compact form is
// pinned by name.
func TestDocsDesktopConsoleContractStatesWhichDocumentsAreLineDelimited(t *testing.T) {
	const path = "console/docs/json-contract.md"
	doc := readDoc(t, path)
	for _, tc := range []struct {
		what   string
		quotes []string
	}{
		{"the two in the console's set are line-delimited", []string{
			"one compact `logLine` object per line",
			"printLogEntry",
			"json.Marshal",
			"Exactly two of the commands in the console's set are line-delimited",
			"`watch --json` and `logs RUN\n   --json`",
			"Every other command in that set is indented",
		}},
		{"cancel's document is compact, outside that set", []string{
			"**Its document is compact — one object per line**",
			"`printCancel` marshals the struct with `json.Marshal`",
			"the only one outside the console's set",
		}},
		{"every other document in the console's set is indented", []string{
			"## 9. What the console may do with all this",
			"Every document in the console's set is read-only",
		}},
	} {
		for _, want := range tc.quotes {
			if !strings.Contains(doc, want) {
				t.Errorf("%s does not state %s: it never says %q", path, tc.what, want)
			}
		}
	}
	if strings.Contains(doc, "one `logLine` object per entry, indented") {
		t.Errorf("%s still calls logs --json indented, which printLogEntry is not", path)
	}
}

// TestDocsDesktopConsoleContractNamesTheFlagInventory: the spawn contract's
// one flag fact (design section 3.4 rule 3) must be the cobra commands', not
// the document's word for it. Every command the dashboard spawns except the
// version handshake carries --project and --config; version takes no flags at
// all, so a console that appended --project to it would fail on every machine.
// The map is the dashboard's commands, and deliberately not the picker's own
// read: `config list --json` takes neither flag (it prints the installations,
// so there is no project to name), which is why this inventory -- an inventory
// of the flags a project read carries -- must not gain it (§5.1 rule 6).
func TestDocsDesktopConsoleContractNamesTheFlagInventory(t *testing.T) {
	const path = "console/docs/json-contract.md"
	doc := readDoc(t, path)
	spawned := map[string]func() *cobra.Command{
		"watch":       newWatchCmd,
		"budget show": newBudgetShowCmd,
		// `report` is not here: the command moved to ee/report/cli (E2.2b), and
		// an untagged build's tree holds only its stub. ee/report/cli's own
		// contract test checks its row of this inventory.
		"runs ls": newLsCmd,
		// The Spend screen's by-person view (console L5b, design §5.1 rule 6):
		// the console spawns it with --since, so it carries --project and
		// --config like every other dashboard read.
		"runs stats":  newRunsStatsCmd,
		"diagnose":    newDiagnoseCmd,
		"logs":        newLogsCmd,
		"secrets ls":  newSecretsLsCmd,
		"accounts ls": newAccountsLsCmd,
		"doctor":      newDoctorCmd,
	}
	for name, newCmd := range spawned {
		for _, flag := range []string{"project", "config"} {
			if newCmd().Flags().Lookup(flag) == nil {
				t.Errorf("the console spawns %s with --%s, but the command no longer takes it", name, flag)
			}
		}
		if !strings.Contains(doc, "`"+name+"`") {
			t.Errorf("%s never names the spawned command %q", path, name)
		}
		// And it must be a row of the section-8 table itself, not merely a
		// mention somewhere in the file: a command that reached the console's
		// set while the inventory quietly went on listing the older ones would
		// pass the check above (§6.10's own heading names this command anyway).
		if !strings.Contains(doc, "| `"+name+"`") {
			t.Errorf("%s's flag inventory table has no row for the spawned command %q", path, name)
		}
	}
	if cmd := newVersionCmd(); cmd.Flags().HasFlags() {
		t.Errorf("fugaro version takes flags now, so it is no longer the handshake exception the document states: %s", cmd.Flags().FlagUsages())
	}
	if !strings.Contains(doc, "**no — it takes no flags at all**") {
		t.Errorf("%s does not state that version takes no flags at all", path)
	}
}

// consoleContractCommands is every cobra command console/docs/json-contract.md
// documents, by the phrase the document uses for it. The new tests below read
// the document's flags and its `mode` values back off these constructors, so a
// flag or a value written into the document rather than read from the code
// fails there.
var consoleContractCommands = map[string]func() *cobra.Command{
	"watch":       newWatchCmd,
	"budget show": newBudgetShowCmd,
	"runs ls":     newLsCmd,
	// §6.10 documents the by-person aggregate (CLI 0.9.0), and it is now on
	// the design's spawn allow-list too (console L5b, §5.1 rule 6): the Spend
	// screen's by-person view spawns it with --since. The document describes
	// its flags either way, so the flags written there are checked against
	// this constructor like any other's.
	"runs stats":  newRunsStatsCmd,
	"diagnose":    newDiagnoseCmd,
	"logs":        newLogsCmd,
	"cancel":      newCancelCmd,
	"secrets ls":  newSecretsLsCmd,
	"accounts ls": newAccountsLsCmd,
	// §6.5a documents the operator's write to the cascade's state
	// (credentials-service C1.21). It is deliberately NOT on the design's
	// spawn allow-list — the console never writes — but the document
	// describes its flags, so they are checked against this constructor like
	// any other's.
	"accounts clear": newAccountsClearCmd,
	"doctor":         newDoctorCmd,
	"version":        newVersionCmd,
	"config list":    newConfigListCmd,
	"plan graph":     newPlanGraphCmd,
	// §6.11 documents the backlog index's rows (CLI 0.11.0). Like §6.10, the
	// console does not spawn it yet -- the backlog screen is the task that
	// follows -- but the document describes its flags, so they are checked
	// against this constructor like any other command's.
	"backlog list": newBacklogListCmd,
	// §6.12 documents the graph joined to the runs (GV1.6, the CLI release after 0.11.4), and the
	// console spawns it since GV2.1, as §5.1 rule 6's `plan status --json` row. Its flags — including the cloud
	// ones, which this command carries and `plan graph` does not — are checked
	// against this constructor like any other command's.
	"plan status": newPlanStatusCmd,
}

// TestDocsDesktopConsoleContractNamesOnlyRealFlags: every --flag the document
// names must be one a real cobra command has, checked with its own
// Flags().Lookup rather than against a list kept here. This is the check a
// review found missing: the field-table test above reads only first-column key
// tokens, so a flag invented in prose (the `--no-hours` a first revision put
// on `report`, which has no such flag) went through it untouched.
//
// Two levels, because a union alone would let a flag of one command be written
// under another's heading. First, every token anywhere in the document must
// exist on at least one of the commands the document describes. Second, a line
// that states a command's own flags ("**Flags** (`report.go:81`): ..." or
// "Flags: `--json` ...") is checked against THAT section's command, resolved
// from the heading above it.
func TestDocsDesktopConsoleContractNamesOnlyRealFlags(t *testing.T) {
	const path = "console/docs/json-contract.md"
	doc := readDoc(t, path)

	cmds := map[string]*cobra.Command{}
	all := map[string]bool{}
	for name, newCmd := range consoleContractCommands {
		cmd := newCmd()
		cmds[name] = cmd
		cmd.Flags().VisitAll(func(f *pflag.Flag) { all[f.Name] = true })
	}
	// A documented negative: the document names --version in order to say the
	// `version` subcommand has no flag of its own and that the root command's
	// --version is the root's, never the handshake's (§6.7, §8), so it is
	// checked for absence, the opposite of the rest. The root command is not
	// one of the commands this document describes -- the console spawns
	// subcommands -- so its own flag is not in `all` either.
	documentedAbsent := map[string]bool{"version": true}

	// (1) Every flag token in the document, outside the sections whose command
	// moved out of the core with the coordinator (see consoleMovedSections).
	found := map[string]bool{}
	inMoved := false
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "#") {
			inMoved = inMovedSection(line)
			continue
		}
		if inMoved {
			continue
		}
		if inMovedRow(line) {
			continue
		}
		for _, m := range flagTokenRE.FindAllStringSubmatch(line, -1) {
			name := strings.TrimRight(m[1], "-")
			if name == "" {
				continue
			}
			found[name] = true
			switch {
			case documentedAbsent[name] && all[name]:
				t.Errorf("%s documents --%s as absent, but a command has it", path, name)
			case !documentedAbsent[name] && !all[name]:
				t.Errorf("%s names --%s, which is not a flag of any command it documents: a flag written rather than read", path, name)
			}
		}
	}
	if len(found) < 20 {
		t.Errorf("%s: only %d distinct flags were found; the document's shape changed and this test stopped looking", path, len(found))
	}

	// (2) The lines that state a command's own flags, against that command.
	sectionCommand := func(heading string) *cobra.Command {
		best := ""
		for phrase := range consoleContractCommands {
			// The longest phrase wins, so `fugaro budget show` is not read as
			// `fugaro budget` (nor `fugaro runs ls` as `fugaro runs`).
			if strings.Contains(heading, "`fugaro "+phrase) && len(phrase) > len(best) {
				best = phrase
			}
		}
		if best == "" {
			return nil
		}
		return cmds[best]
	}
	heading, checkedLines := "", 0
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "#") {
			heading = line
			continue
		}
		if inMovedSection(heading) {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if !flagsLineRE.MatchString(trimmed) {
			continue
		}
		cmd := sectionCommand(heading)
		if cmd == nil {
			t.Errorf("%s: the flags line %q sits under a heading this test cannot resolve to a command: %q", path, trimmed, heading)
			continue
		}
		checkedLines++
		for _, m := range flagTokenRE.FindAllStringSubmatch(line, -1) {
			name := strings.TrimRight(m[1], "-")
			if name == "" || documentedAbsent[name] {
				continue
			}
			if cmd.Flags().Lookup(name) == nil {
				t.Errorf("%s: %q states --%s as this command's own flag, but its cobra command has no such flag", path, strings.TrimSpace(heading), name)
			}
		}
	}
	if checkedLines < 7 {
		t.Errorf("%s: only %d per-command flags lines were checked; the document's shape changed and this test stopped looking", path, checkedLines)
	}
}

// flagsLineRE finds a line of the document that states one command's own
// flags. This document writes them three ways -- "**Flags** (`report.go:81`):",
// "**Flags, read off the cobra command** (`budget.go:211`):" and, where the
// sentence runs on, "... . Flags: `--repo`" -- so the mark is a "Flags" word
// with a colon within a clause, not a fixed prefix.
var flagsLineRE = regexp.MustCompile(`\bFlags\b[^:]{0,60}:`)

// modeRowRE finds one row of the document's "`mode` per command" table: the
// command in the first cell, the values it can carry in the second.
var modeRowRE = regexp.MustCompile("^\\|\\s*`([^`]+)`\\s*\\|(.*)\\|\\s*$")

// TestDocsDesktopConsoleContractPinsTheModeValuesPerCommand pins the one value
// two documents share and disagree about: `mode`. The document states, per
// command, which values it can carry, and that table is checked against the
// code -- `watch.Build` actually run over each stored /config/mode, so the
// watch half is the function's own answer rather than a reading of it, and the
// two budget constants plus the normalisation `budget show` performs for the
// other. A document that says `watch --json` can carry "" (as a first revision
// did) fails here, since the feed never emits it.
func TestDocsDesktopConsoleContractPinsTheModeValuesPerCommand(t *testing.T) {
	const path = "console/docs/json-contract.md"
	doc := readDoc(t, path)

	// The code's two values, spelled as the document spells them.
	if budget.ModeObserve != "observe" || budget.ModeEnforce != "enforce" {
		t.Fatalf("the mode constants are %q/%q, not observe/enforce: the document's values have drifted from the code's",
			budget.ModeObserve, budget.ModeEnforce)
	}

	// What watch --json emits, for every /config/mode an installation can hold:
	// unset, either real value, and one the code does not know.
	emitted := map[string]bool{}
	for _, stored := range []string{"", budget.ModeObserve, budget.ModeEnforce, "audit"} {
		s := watch.NewState()
		if stored != "" {
			data, err := json.Marshal(map[string]string{"mode": stored})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Config.Apply("/", data, false); err != nil {
				t.Fatalf("seeding /config/mode = %q: %v", stored, err)
			}
		}
		emitted[watch.Build(s, time.Now(), watch.Config{}).Mode] = true
	}
	if !emitted["observe"] || !emitted["enforce"] || len(emitted) != 2 {
		t.Fatalf("watch.Build emits %v, want exactly observe and enforce", emitted)
	}

	// The document's table: command -> the values it says that command can carry.
	start := strings.Index(doc, "**`mode` per command**")
	if start < 0 {
		t.Fatalf("%s no longer carries the \"`mode` per command\" table this test reads", path)
	}
	stated := map[string]map[string]bool{}
	rows := 0
	for _, line := range strings.Split(doc[start:], "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			if rows > 0 {
				break // the table ended
			}
			continue
		}
		m := modeRowRE.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue // the header row and the rule
		}
		cmd := strings.TrimSuffix(m[1], " --json")
		// The values are listed before the em dash that explains them: the
		// rest of the cell backticks identifiers too (`Build`, `/config/mode`).
		valuesText, _, _ := strings.Cut(m[2], "—")
		values := map[string]bool{}
		for _, tok := range regexp.MustCompile("`([^`]*)`").FindAllStringSubmatch(valuesText, -1) {
			values[strings.Trim(tok[1], `"`)] = true
		}
		stated[cmd] = values
		rows++
	}
	if rows == 0 {
		t.Fatalf("%s's \"`mode` per command\" table has no rows", path)
	}
	if len(stated) != 2 {
		t.Fatalf("%s's \"`mode` per command\" table names %d commands, want the two that carry mode: %v", path, len(stated), stated)
	}
	// watch --json: exactly what Build emitted.
	if got := stated["watch"]; len(got) != 2 || !got["observe"] || !got["enforce"] {
		t.Errorf("%s says watch --json's mode can be %v, but watch.Build emits %v", path, got, emitted)
	}
	// budget show --json: the two constants, plus "" -- the value its own
	// normalisation produces for an unset or unknown /config/mode.
	if got := stated["budget show"]; len(got) != 3 || !got["observe"] || !got["enforce"] || !got[""] {
		t.Errorf("%s says budget show --json's mode can be %v, want observe, enforce and \"\" (budget.go:457)", path, got)
	}

	// And the prose rows agree with the table: the watch field row must not
	// offer "", and the budget row must.
	for _, row := range []string{
		"| `mode` | string | — | no | **`observe` or `enforce`, never `\"\"`**",
		"**This is the only document that can carry `\"\"`**",
	} {
		if !strings.Contains(doc, row) {
			t.Errorf("%s no longer states %q: the per-command mode fact moved out of the field rows", path, row)
		}
	}
}

// docTableAfter finds the markdown table whose header line contains header and
// returns its rows' cells, up to the first line that is not a table row.
// Cells are the "|"-separated fields with the outer bars dropped and the
// whitespace trimmed; the header row and the "|---|" rule are not included.
func docTableAfter(t *testing.T, path, doc, header string) [][]string {
	t.Helper()
	start := strings.Index(doc, header)
	if start < 0 {
		t.Fatalf("%s has no table whose header names %q", path, header)
	}
	var rows [][]string
	for _, line := range strings.Split(doc[start:], "\n")[1:] {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") {
			if len(rows) > 0 {
				break
			}
			continue
		}
		cells := strings.Split(strings.Trim(trimmed, "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if strings.Trim(cells[0], "-") == "" { // the |---|---| rule
			continue
		}
		rows = append(rows, cells)
	}
	if len(rows) == 0 {
		t.Fatalf("%s's table under %q has no rows", path, header)
	}
	return rows
}

// firstBacktick is the first `token` of a table cell -- the exact argv of an
// allow-list row, which the row states before any parenthetical.
func firstBacktick(cell string) string {
	m := regexp.MustCompile("`([^`]+)`").FindStringSubmatch(cell)
	if m == nil {
		return ""
	}
	return m[1]
}

// TestDocsDesktopConsoleDesignAllowListIsExactArgv: the design's spawn
// allow-list must be read as a table of exact argument vectors, not as prose
// that happens to contain the command names. A presence test that looks for
// the bare substring "watch --json" is satisfied by a sentence about it, which
// is what a review of the first revision found here: the table could lose a
// row -- or gain one -- and the test would still pass. This parses both tables
// §5.1 rule 6 carries and compares their first cells exactly: the allow-list is
// the sixteen argvs and nothing else -- the console's one spawn table
// (SPAWN_COMMANDS) since the installation rail took the picker's read onto the
// dashboard's surface -- and a review of an earlier revision found the picker's own
// read, `config list --json`, absent here, which left the specification
// forbidding the one read the picker depends on -- and the "never" table still
// names the two entries that make a name-based list a hole.
func TestDocsDesktopConsoleDesignAllowListIsExactArgv(t *testing.T) {
	const path = "docs/design/desktop-console.md"
	doc := readDoc(t, path)

	want := []string{
		"watch --json",
		"budget show --json",
		"report --json",
		"runs ls --json",
		// The Spend screen's by-person view (console L5b): the one read whose
		// rows the CLI scopes to the caller, and the one argv here the console
		// may not widen with --all-users.
		"runs stats --json",
		"diagnose RUN --json",
		"logs RUN --json",
		"logs RUN --url",
		"secrets ls --json",
		"accounts ls --json",
		"version",
		"doctor --json",
		// The installation rail's read (`installation.list`), which the picker
		// window shares. It carries no --project and no --config, so it is absent from the
		// contract document's flag inventory below, which is about flags.
		"config list --json",
		// The graph screen's per-task state (console v2 plan GV2.1, the
		// `plan.status` operation): read-only, no path in the argv.
		"plan status --json",
		// The project view (Bitbucket credentials T1.2, the
		// `coordinator.status` operation): read-only, no path in the argv.
		// `--from-store` is what makes it need no host credential at all --
		// it prints the view the sweep stored -- and `--all` is every
		// repository the installation's config lists.
		"coordinator status --from-store --all --json",
		// The launch (console v2 plan V2.5.1a, the `run.launch` operation):
		// the console's one write and the one thing this table is a fence
		// against, spelled as rule 11's exact argv and nothing else.
		"run --task-file - --json --run-id <run id> --repo <owner/name> [--title=<T>] [--plan-task=<P>] --project <name> [--config <path>]",
	}
	rows := docTableAfter(t, path, doc, "| Allowed argv (exact, in this order) |")
	got := map[string]bool{}
	for _, cells := range rows {
		argv := firstBacktick(cells[0])
		if argv == "" {
			t.Errorf("%s: the allow-list row %q states no exact argv in its first cell", path, cells[0])
			continue
		}
		if strings.HasPrefix(cells[0], "`") && !strings.HasPrefix(cells[0], "`"+argv+"`") {
			t.Errorf("%s: the allow-list row %q starts with more than its argv %q", path, cells[0], argv)
		}
		if got[argv] {
			t.Errorf("%s: the allow-list carries %q twice", path, argv)
		}
		got[argv] = true
	}
	for _, argv := range want {
		if !got[argv] {
			t.Errorf("%s: the allow-list no longer carries the exact argv %q", path, argv)
		}
	}
	for argv := range got {
		if !slices.Contains(want, argv) {
			t.Errorf("%s: the allow-list carries %q, which this test's exact list does not: a spawn contract that grew", path, argv)
		}
	}
	if len(got) != len(want) {
		t.Errorf("%s: the allow-list carries %d argvs, want %d", path, len(got), len(want))
	}

	// The "never" table's first cells: the two entries that are the reason the
	// list is argv-exact rather than name-based.
	never := map[string]bool{}
	for _, cells := range docTableAfter(t, path, doc, "| Never (the \"nothing else\" table) |") {
		argv := firstBacktick(cells[0])
		if argv != "" {
			never[argv] = true
		}
	}
	for _, argv := range []string{"doctor --check-github-app", "logs --follow"} {
		if !never[argv] {
			t.Errorf("%s: the never table no longer names %q", path, argv)
		}
	}
}

// TestDocsDesktopConsoleDesignAllowListIsReadOnlyInTheCode: §5.1 rule 6's
// table is the console's whole read-only guarantee — D14 rests it on the
// exact-argv table, because the CLI cannot tell an admin's identity from
// anyone else's — and a read changes nothing on the machine. The remembered
// checkouts are a file this machine keeps and the resolver every cloud
// command goes through is what writes it, so the code has to know which
// commands are console reads: consoleReadPaths (internal/cli/project.go) is
// that knowledge, and this test reads it back off the design's own table, so
// the two cannot drift. Adding an argv to §5.1 without adding its command
// here fails, and so does a command on the list that the design does not
// allow (bar the reads the design names for the console and §5.1 has no
// row for yet).
func TestDocsDesktopConsoleDesignAllowListIsReadOnlyInTheCode(t *testing.T) {
	const path = "docs/design/desktop-console.md"
	doc := readDoc(t, path)
	allowed := map[string]bool{}
	argvs := 0
	for _, cells := range docTableAfter(t, path, doc, "| Allowed argv (exact, in this order) |") {
		argv := firstBacktick(cells[0])
		if argv == "" {
			continue
		}
		argvs++
		// "budget show --json" is the command `fugaro budget show`: its own
		// words, up to its first flag or its first ARGUMENT (the RUN a
		// diagnose or a logs row takes, spelled in capitals in the table).
		var words []string
		for _, tok := range strings.Fields(argv) {
			if strings.HasPrefix(tok, "-") || tok == strings.ToUpper(tok) {
				break
			}
			words = append(words, tok)
		}
		cmdPath := "fugaro " + strings.Join(words, " ")
		allowed[cmdPath] = true
		if !consoleReadPaths[cmdPath] && !consoleLaunchPaths[cmdPath] {
			t.Errorf("%s allows the console to spawn %q, but %q is not in consoleReadPaths (internal/cli/project.go): a command on the allow-list is a read, and a read must not write the remembered checkouts", path, argv, cmdPath)
		}
	}
	// Fifteen commands: the twelve dashboard reads (logs has two argvs), the
	// rail's `config list --json` (D13's amendment of 2026-10-20), the graph
	// screen's `plan status --json` (GV2.1), the project view's
	// `coordinator status --from-store --all --json` (Bitbucket credentials
	// T1.2) and the launch `fugaro run` (V2.5.1a).
	if len(allowed) != 15 {
		t.Errorf("%s: the allow-list's %d argvs name %d commands, want 15 (logs has two argvs)", path, argvs, len(allowed))
	}
	// The other direction: the list carries §5.1's commands and nothing else,
	// except the read the design names for the console and §5.1 has not grown
	// a row for yet (V2.2.1, the graph document). Another name is a command
	// someone decided the console spawns, and that is a design change, not a
	// table entry.
	namedNotAllowed := map[string]bool{"fugaro plan graph": true}
	for cmdPath := range consoleReadPaths {
		if !allowed[cmdPath] && !namedNotAllowed[cmdPath] {
			t.Errorf("consoleReadPaths carries %q, which is neither an argv §5.1 allows nor a read the design names for the console (%v): the read-only list is the design's, not this file's", cmdPath, namedNotAllowed)
		}
	}
}

// consoleLaunchPaths is §5.1's allow-list written as an exemption rather
// than as a hole: `fugaro run` is the console's one write (rule 11, D22), so
// it is deliberately absent from consoleReadPaths -- a write is not a read
// and must not be -- and a table that grew a second writer would have to
// come back here and say so. The reason sits beside the exemption because
// the exemption is the security claim: one write, named, and nothing else.
var consoleLaunchPaths = map[string]bool{"fugaro run": true}

// planPhaseRow returns the "What ships" cell of the project-centric plan's
// phase table row whose Phase cell names phase (a substring of it), and the
// row's own text, so a claim about which phase ships a command can be checked
// against the phase it is actually in.
//
// The plan's phase table carries a Release column between Phase and What
// ships, so the header named here and the cell read are the table's own.
func planPhaseRow(t *testing.T, plan, phase string) (whatShips, row string) {
	t.Helper()
	for _, cells := range docTableAfter(t, "docs/plans/2026-10-14-project-centric.md", plan, "| Phase | Release | What ships |") {
		if strings.Contains(cells[0], phase) {
			return cells[2], strings.Join(cells, " | ")
		}
	}
	t.Fatalf("docs/plans/2026-10-14-project-centric.md's phase table has no row naming %q", phase)
	return "", ""
}

// TestDocsDesktopConsoleContractCreditsProjectCommandsToTheRightPhase: gap 3
// credits the project-centric plan's phase 1 with shipping some `project ...`
// commands. That credit must be the plan's own phase row, not a recollection
// of it: a second review found the contract crediting `project repos ls` and
// `rm` to phase 1, which the plan gives to phase 4 (Enrollment and migration,
// Task 4.1). Every `project ...` command the contract's gap row states as
// phase 1's must appear in phase 1's own "What ships" cell, and the verbs that
// come later must appear in phase 4's -- so a command moved from one phase to
// another in the plan, or miscredited in the contract, fails here.
func TestDocsDesktopConsoleContractCreditsProjectCommandsToTheRightPhase(t *testing.T) {
	const path = "console/docs/json-contract.md"
	doc := readDoc(t, path)
	plan := readDoc(t, "docs/plans/2026-10-14-project-centric.md")

	phase1, _ := planPhaseRow(t, plan, "1. Policy as data")
	phase4, _ := planPhaseRow(t, plan, "4. Enrollment and migration")

	// The gap-3 row, and the segment of it that credits commands to phase 1.
	var row string
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "| 3 |") && strings.Contains(line, "project show --json") {
			row = line
			break
		}
	}
	if row == "" {
		t.Fatalf("%s no longer carries gap 3's row about `project show --json`", path)
	}
	var credited string
	for _, seg := range strings.Split(row, "—") {
		if strings.Contains(seg, "ships") {
			credited = seg
		}
	}
	if credited == "" {
		t.Fatalf("%s's gap-3 row no longer states which phase ships the project commands: %q", path, row)
	}
	projectRE := regexp.MustCompile("`(project [^`]+)`")
	n := 0
	for _, m := range projectRE.FindAllStringSubmatch(credited, -1) {
		cmd := m[1]
		n++
		if !strings.Contains(phase1, cmd) {
			t.Errorf("%s credits phase 1 with shipping %q, but the plan's phase 1 row (\"What ships\") does not name it", path, cmd)
		}
	}
	if n == 0 {
		t.Fatalf("%s's gap-3 row credits phase 1 with no command at all: %q", path, credited)
	}
	// And the verbs the contract says come later are in the later phase's row.
	// The plan writes that group in its compressed form (`project repos
	// ls/add/rm`/`sync`), so each verb is what is checked, not the spelling of
	// the whole group.
	for _, verb := range []string{"ls", "rm", "sync"} {
		if !strings.Contains(phase4, verb) {
			t.Errorf("the plan's phase 4 row no longer ships `project repos %s`, which %s's gap-3 row places there", verb, path)
		}
		if strings.Contains(phase1, "repos "+verb) || strings.Contains(phase1, "/"+verb) {
			t.Errorf("the plan's phase 1 row now ships `project repos %s`, so %s's gap-3 row is wrong to place it in phase 4", verb, path)
		}
		if !strings.Contains(row, "`"+verb+"`") && !strings.Contains(row, "repos "+verb) {
			t.Errorf("%s's gap-3 row no longer names the verb %s among those that come later", path, verb)
		}
	}
}

// ---------------------------------------------------------------- Task 0.2

// Task 0.2 of the plan (console/docs/json-contract.md section 10): what
// `watch --json` guarantees to a long-lived child process. The plan's Tests
// line for the task names one test, TestDocsDesktopConsoleContractSettlesTheFeed,
// a presence test; the three behavioural tests after it are here because a
// presence test cannot see what the section is actually about. They exercise
// the real command -- `execute` runs NewRootCmd, the fixture is a fake
// Realtime Database -- so the claims section 10 makes about the child's
// lifecycle fail a test when the code changes, not only when the prose does.
// They live in this file rather than a new one because the plan's Files line
// for Task 0.2 is exactly console/docs/json-contract.md and this file.

// TestDocsDesktopConsoleContractSettlesTheFeed pins the consumption contract
// of `watch --json` -- plan Task 0.2, settling the design's section 12 item 2
// by reading the CLI rather than assuming it. Each sub-assertion is a named
// fact and carries the symbol that proves it, so dropping a fact from the
// document fails a test named for it instead of quietly shrinking the
// contract. The four the plan names by hand are the slow reader, the closed
// stdout, the refusal and the UTC-midnight rollover; the rest are the same
// task's steps, and each was read off the code (see section 10's own
// citations).
func TestDocsDesktopConsoleContractSettlesTheFeed(t *testing.T) {
	const path = "console/docs/json-contract.md"
	// Whitespace is normalised, as the sibling docs tests do: a sentence
	// rewrapped by a later edit is the same sentence.
	doc := strings.Join(strings.Fields(readDoc(t, path)), " ")
	for _, c := range []struct {
		fact string
		want []string
	}{
		{
			fact: "a slow reader blocks the child rather than dropping a document",
			want: []string{
				"A slow reader makes the child block; it never drops a document",
				"json.NewEncoder(d.Out).Encode",
				"Supervisor.emit",
				"watch.Start",
				"rtdb.Client.Stream",
				"c.idle",
				"backMin",
				"backMax",
				"a `put` of `\"/\"` holding the whole current value",
			},
		},
		{
			fact: "a closed stdout ends the child, on a write and by a signal",
			want: []string{
				"A closed stdout ends the child — on its next write, and by a signal",
				"SIGPIPE",
				"descriptors 1 and 2",
				"the parent that spawned it sees **a signal, not an exit code**",
				"141",
				"An idle project emits no document",
				"SIGPIPE is therefore not a shutdown mechanism",
				"not wrapped with `remote()`",
				"ExitUserError",
			},
		},
		{
			fact: "a refused read is terminal, and usually announces itself with no document",
			want: []string{
				"A refused read is terminal, and it usually announces itself with no document at all",
				"checkDBProject",
				"before it starts the supervisor",
				"Zero documents is the shape of the ordinary refusal",
				"State.conn",
				"ConnRefused",
				"refused",
				"dbErr(d.LC, rtdb.ErrPermission, false)",
				"phaseHalt",
				"Restarting the child before the identity or the role changes refuses again",
			},
		},
		{
			fact: "UTC midnight is a gap, not a zero",
			want: []string{
				"UTC midnight is a gap, not a zero",
				"budget.Day(s.now())",
				"streamPhase",
				"dayWG.Wait()",
				"rollDay",
				"openDay",
				"UpdDay",
				"SetDay",
				"rtdb.Tree.Reset",
				"BurnWarmup",
				"a document that is not a measurement",
			},
		},
		{
			fact: "the emission rule, and that a document is a sampled whole",
			want: []string{
				"refused || (ready && dirty && (last.IsZero() || time.Since(last) >= gap))",
				"watchJSONGap",
				"A document is a sampled whole, not a delta",
				"Silence means \"live and unchanged\", not \"the feed is dead\"",
				"A connection-state change is announced",
				"watch.StaleAfter",
			},
		},
		{
			fact: "the first document arrives within watchFirstFrame",
			want: []string{
				"The first document arrives within `watchFirstFrame`",
				"seen == [4]bool{true, true, true, true}",
				"watchFirstFrame",
				"Supervisor.run",
				"offline frame",
				// `ready` is only re-evaluated on an update, and updates come
				// from the tick: the arrival bound is the clock plus a tick.
				"`watchFirstFrame` + `watch.Options.Tick`, about 6 s",
			},
		},
		{
			fact: "a dead or withdrawn stream is not an exit",
			want: []string{
				"A dead or withdrawn stream is not an exit",
				"probe",
				"pollPhase",
				"phaseTrial",
				"SSERetry",
				"the child stays alive",
				"`refused` is the one connection state that does *not* linger",
			},
		},
		{
			fact: "the degraded mode is a second shape and a line that is not JSON",
			want: []string{
				"watchDegradedDoc",
				"degradedSince",
				"watchPlainInterval",
				"run records unavailable (retrying): %s",
				"cmd.OutOrStdout()",
				"skip any line that is not a JSON object",
				"printProjectHeader",
				"goes to stderr, which is not the document stream",
			},
		},
		{
			fact: "the thresholds a consumer that is not a terminal needs",
			want: []string{
				"watch.SilentAfter",
				"watch.LostAfter",
				"watch.BurnWindow",
				"watch.Options.Tick",
				"watch.Options.Interval",
				"degradedSince",
			},
		},
		{
			fact: "how many documents arrive per minute in each mode",
			want: []string{
				"How many documents arrive per minute, in each mode",
				"Live SSE (`watch --json`)",
				"Polling (`--poll`, or after three same-cause stream errors)",
				"two per poll",
				"Degraded (no `budget.rtdb_url`)",
				"exactly one, then exit",
			},
		},
		{
			fact: "option A needs a supervisor, and why",
			want: []string{
				"Does option A need a supervisor process? Yes",
				"The child exits on a refusal",
				"The child does not exit on a dead stream",
				"A closed pipe does not end an idle child",
				"one `watch --json` per selected installation",
			},
		},
		{
			fact: "how the child ends when the console asks it to",
			want: []string{
				"How the child ends when the console asks it to",
				"signalContext",
				"os.Interrupt, syscall.SIGTERM, syscall.SIGHUP",
				"the child exits 0",
				"context.AfterFunc",
				"SIGKILL",
				// The tick is not what notices the signal, and a child blocked
				// writing to a stalled reader is why the SIGKILL exists.
				"so a running child notices the cancellation the moment it fires",
				"that is what the `SIGKILL` is for",
			},
		},
		{
			fact: "the reader rules, including the narrowed repo list",
			want: []string{
				"Split stdout on newlines and decode each line as one JSON object",
				"A changed `day` is a break in the series, never a zero",
				"A missing field is `unavailable`, never `0`",
				"watch.FilterRepo",
				"Never `--follow`",
			},
		},
	} {
		t.Run(c.fact, func(t *testing.T) {
			for _, w := range c.want {
				if !strings.Contains(doc, strings.Join(strings.Fields(w), " ")) {
					t.Errorf("%s lacks %q (fact: %s)", path, w, c.fact)
				}
			}
		})
	}
}

// watchJSONLines returns the non-empty lines of a watch --json run's stdout,
// which is the reader's own split: one document per line, and (in the
// degraded mode) possibly a line that is not a document.
func watchJSONLines(out string) []string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// TestWatchRefusalBeforeTheStreamExits1WithNoDocument: the ordinary refusal.
// checkDBProject reads /fugaro/project before runWatch starts the supervisor,
// so a database that refuses the read fails with exit 1 and the CLI's own
// sentence on stderr -- and writes no document at all. Section 10.5's first
// half, and the reason the console must not expect one document on a refusal.
//
// Mutation (run, restore): move the checkDBProject call in runWatch to after
// watch.Start, and this test fails with the refused document on stdout.
func TestWatchRefusalBeforeTheStreamExits1WithNoDocument(t *testing.T) {
	f := newBudgetFixture(t, "")
	f.db.Refuse(401, "UNAUTHENTICATED", "", "expired")
	out, _, err := execute(t, "watch", "--json")
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("watch against a database that refuses the read = %v (exit %d), want a user error (exit %d)",
			err, ExitCode(err), ExitUserError)
	}
	if out != "" {
		t.Fatalf("watch wrote %q before refusing: the refusal is caught before watch.Start, so the ordinary refusal emits no document", out)
	}
	if !strings.Contains(err.Error(), "refused the read") {
		t.Fatalf("err = %v, want the CLI's own refusal sentence (the one main prints to stderr)", err)
	}
}

// TestWatchStreamRefusalEmitsOneDocumentThenExits1: the rarer refusal, one
// that arrives during the stream. Only the agents path is refused, so the
// database check passes and the stream starts; the permission error then
// reaches State.conn as ConnRefused, watchStream emits that one document
// before the ready/dirty/gap test and returns, and the child exits 1 rather
// than halting. Section 10.5's second half and section 10.6's last
// paragraph.
//
// Mutation (run, restore): delete the `if refused { return dbErr(...) }` in
// watchStream, and this test hangs until its context times out instead of
// returning one document and exit 1.
func TestWatchStreamRefusalEmitsOneDocumentThenExits1(t *testing.T) {
	f := newBudgetFixture(t, "")
	// The queued source opens the runs bucket on its own goroutine, and that
	// open's project-name check writes the project-check cache. The open is
	// deliberately left to finish on its own when the command ends
	// (TestQueuedSourceQuitWhileOpenBlocked pins that: an open that ignores a
	// cancelled ctx never blocks shutdown), so its cache write can land after
	// this command returns. Point XDG_CACHE_HOME at TestMain's own directory,
	// which nothing removes, so the late write cannot race t.TempDir's
	// RemoveAll cleanup -- which is how this test used to flake ("directory
	// not empty").
	if testCacheRoot != "" {
		t.Setenv("XDG_CACHE_HOME", filepath.Join(testCacheRoot, t.Name()))
	}
	f.db.RefusePath("agents", 401, "UNAUTHENTICATED", "", "expired")
	cmd := NewRootCmd()
	out := &lockedBuf{}
	cmd.SetOut(out)
	cmd.SetErr(&lockedBuf{})
	cmd.SetArgs([]string{"watch", "--json"})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := cmd.ExecuteContext(ctx)
	if err == nil || ExitCode(err) != ExitUserError {
		t.Fatalf("watch on a stream refusal = %v (exit %d), want a user error (exit %d)",
			err, ExitCode(err), ExitUserError)
	}
	lines := watchJSONLines(out.String())
	if len(lines) != 1 {
		t.Fatalf("want exactly one document (the refusal, emitted before the ready test), got %d: %q", len(lines), out.String())
	}
	var doc struct {
		Connection struct {
			State string `json:"state"`
		} `json:"connection"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &doc); err != nil {
		t.Fatalf("the one line is not a document: %q (%v)", lines[0], err)
	}
	if doc.Connection.State != "refused" {
		t.Fatalf("the one document's connection.state = %q, want refused", doc.Connection.State)
	}
}

// TestWatchCancelReturnsNilSoTheChildExitsZero: the in-process half of
// section 10.12. main.go's signalContext cancels the command's context on
// SIGINT, SIGTERM or SIGHUP, and main only calls os.Exit on a non-nil error,
// so the cancelled watch must return nil for the child to exit 0 -- which is
// what the console's SIGTERM shutdown relies on. (The signal itself is the
// process-level test in watch_child_test.go; this is the half a
// cancellation can show in-process.)
func TestWatchCancelReturnsNilSoTheChildExitsZero(t *testing.T) {
	f := newBudgetFixture(t, "")
	seedWatch(f, "streamed")
	cmd := NewRootCmd()
	out := &lockedBuf{}
	cmd.SetOut(out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"watch", "--json"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(out.String(), "\n") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(out.String(), "\n") {
		t.Fatal("the child wrote no document within 20 s")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a cancelled watch returned %v, want nil: main exits 0 on SIGTERM only because it does", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the cancelled watch did not return: SIGTERM would leave the child running")
	}
}

// ---------------------------------------------------------------------------
// Task 1.4: the console's staged tests workflow.
//
// A workflow change cannot be delivered by an agent run at all (GitHub rejects
// a Fugaro push that touches .github/workflows/**), so this repository's
// workflow changes are written under docs/pending-workflows/ and copied in by
// hand -- see docs/pending-workflows/README.md. The console's CI is staged with
// Task 1.4 and applied by the owner as Task 1.5, and the three tests below are
// the two halves of what that staging has to keep true:
//
//   - TestConsoleTestsWorkflowIsStagedAndPinned reads the STAGED file, which is
//     why these tests keep passing after the copy-in: the staged copy is not
//     deleted (unlike images.yml), and while both files exist they must be
//     byte-identical, so an edit to one without the other fails here rather
//     than drifting silently.
//   - TestConsoleWorkflowJobIsGateable is the one that keeps Task 4.6 from
//     silently breaking every tag on this repository. **Task 4.6 added this
//     job's check runs to scripts/release-gate.sh's `names` array**
//     (`TestReleaseGateRequiresTheConsoleCheck`, below), and because the job is
//     a matrix the gate requires the two names GitHub actually reports for it,
//     `console (ubuntu-latest)` and `console (macos-latest)`, not the bare job
//     key. A check that does not exist is not a success, so a `console` job
//     behind a `paths:` filter, behind an `if:` guard, or renamed by an
//     interpolated matrix value would refuse every release -- including the
//     ones that never touch the console (design section 9.4). The name and the
//     triggers are asserted here so the job the gate names cannot change
//     silently.
// ---------------------------------------------------------------------------

const (
	// consoleTestsWorkflowPath is the staged console CI; appliedConsoleTestsPath
	// is where the owner copies it (plan Task 1.5).
	consoleTestsWorkflowPath = "docs/pending-workflows/console-tests.yml"
	appliedConsoleTestsPath  = ".github/workflows/console-tests.yml"
)

// consoleWorkflow is the staged file's shape, as far as these tests read it.
// The unmodelled keys are covered separately, from the raw mapping, so a
// `paths:` filter or a job-level `if:` cannot hide behind a field this struct
// does not have.
type consoleWorkflow struct {
	On struct {
		Push struct {
			Branches []string `yaml:"branches"`
		} `yaml:"push"`
	} `yaml:"on"`
	Permissions map[string]string             `yaml:"permissions"`
	Jobs        map[string]consoleWorkflowJob `yaml:"jobs"`
}

type consoleWorkflowJob struct {
	Name        string            `yaml:"name"`
	If          string            `yaml:"if"`
	RunsOn      string            `yaml:"runs-on"`
	Permissions map[string]string `yaml:"permissions"`
	Strategy    struct {
		Matrix struct {
			OS []string `yaml:"os"`
		} `yaml:"matrix"`
	} `yaml:"strategy"`
	Steps []consoleWorkflowStep `yaml:"steps"`
}

type consoleWorkflowStep struct {
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	If   string            `yaml:"if"`
	With map[string]string `yaml:"with"`
}

// readConsoleTestsWorkflow is the staged file's text, parsed both ways: into
// the typed shape above, and into raw mappings of the trigger block and of each
// job, where a key this test does not model (`paths:`, `paths-ignore:`, `if:`)
// is still visible.
func readConsoleTestsWorkflow(t *testing.T) (string, consoleWorkflow, map[string]any, map[string]map[string]any) {
	t.Helper()
	text := readDoc(t, consoleTestsWorkflowPath)
	var wf consoleWorkflow
	if err := yaml.Unmarshal([]byte(text), &wf); err != nil {
		t.Fatalf("%s is not valid YAML: %v", consoleTestsWorkflowPath, err)
	}
	var raw struct {
		On   map[string]any            `yaml:"on"`
		Jobs map[string]map[string]any `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(text), &raw); err != nil {
		t.Fatalf("%s is not valid YAML: %v", consoleTestsWorkflowPath, err)
	}
	if len(raw.On) == 0 || len(wf.Jobs) == 0 {
		t.Fatalf("%s parsed with no triggers (%d) or no jobs (%d): these tests read the file, not a comment about it",
			consoleTestsWorkflowPath, len(raw.On), len(wf.Jobs))
	}
	return text, wf, raw.On, raw.Jobs
}

// consoleUsesLine matches one `uses:` line: a full 40-hex commit pin with the
// `# vX.Y.Z` comment that makes the pin reviewable. Dependabot does not watch
// docs/pending-workflows/, and this file is copied in by hand, so the comment is
// how the person copying it can compare pins (README's "Before you copy").
var consoleUsesLine = regexp.MustCompile(`^\s*(?:-\s*)?uses:\s*[^\s@]+@[0-9a-f]{40}\s+#\s+v\d+\.\d+\.\d+\s*$`)

// consoleBlob matches a long run of characters that could be a base64 blob:
// the shape a pasted secret has. The pinned SHAs match it too, so the `uses:`
// lines are removed before it is applied, and `consoleWord` excludes a bare
// identifier -- this workflow's own comment naming a Go test is 42 letters, and
// a check that fires on prose is a check that gets deleted. What is left is a
// long run carrying a `+`, a `/` or a `=`, which is what a real blob has and a
// word does not. The limit is stated rather than hidden: a secret that base64s
// to pure alphanumerics is indistinguishable from an identifier, which is why
// the `secrets.` assertion above is the one that actually holds the line.
var (
	consoleBlob = regexp.MustCompile(`[A-Za-z0-9+/]{40,}={0,2}`)
	consoleWord = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
)

// TestConsoleTestsWorkflowIsStagedAndPinned holds the staged file to what the
// plan's Task 1.4 says it is: pinned, secretless, gated on nothing, and -- once
// the owner has copied it in -- byte-identical to the file under
// .github/workflows/.
func TestConsoleTestsWorkflowIsStagedAndPinned(t *testing.T) {
	text, wf, _, rawJobs := readConsoleTestsWorkflow(t)

	// Every `uses:` is pinned to a full commit SHA with a version comment, and
	// the file has at least one (a workflow whose actions were all deleted would
	// otherwise pass this loop vacuously).
	uses := 0
	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(line, "uses:") {
			continue
		}
		uses++
		if !consoleUsesLine.MatchString(line) {
			t.Errorf("%s: %q is not a full 40-hex pin with a `# vX.Y.Z` comment", consoleTestsWorkflowPath, strings.TrimSpace(line))
		}
	}
	if uses == 0 {
		t.Fatalf("%s pins no action at all: the console job checks the repository out and installs Node", consoleTestsWorkflowPath)
	}

	// No secret of any kind, and no shape that could be one. The signing,
	// notarising and stapling job is Task 4.2's separate file, and this is the
	// assertion that keeps the two from being merged back into one: a job that
	// runs on every pull request must hold nothing a fork's pull request would
	// otherwise be running next to.
	if strings.Contains(text, "secrets.") {
		t.Errorf("%s names a secret: the console job holds none at all (design section 9.4)", consoleTestsWorkflowPath)
	}
	for _, forbidden := range []string{"-----BEGIN", "pull_request_target", "workflow_run", "write-all"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("%s contains %q, which the tests workflow may never have (design section 9.4)", consoleTestsWorkflowPath, forbidden)
		}
	}
	var body []string
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "uses:") {
			continue
		}
		body = append(body, line)
	}
	for _, blob := range consoleBlob.FindAllString(strings.Join(body, "\n"), -1) {
		if consoleWord.MatchString(blob) {
			continue
		}
		t.Errorf("%s carries %q, which is long enough and shaped like a pasted credential: the tests job holds no secret", consoleTestsWorkflowPath, blob)
	}

	// Every job declares `permissions:` explicitly, and only what it needs.
	if len(wf.Permissions) != 1 || wf.Permissions["contents"] != "read" {
		t.Errorf("%s's workflow-level permissions = %v, want exactly contents: read", consoleTestsWorkflowPath, wf.Permissions)
	}
	for name, job := range wf.Jobs {
		if len(job.Permissions) != 1 || job.Permissions["contents"] != "read" {
			t.Errorf("%s's %s job permissions = %v, want exactly contents: read declared on the job", consoleTestsWorkflowPath, name, job.Permissions)
		}
		raw, ok := rawJobs[name]
		if !ok {
			t.Fatalf("%s's %s job is missing from the raw mapping", consoleTestsWorkflowPath, name)
		}
		if _, ok := raw["permissions"]; !ok {
			t.Errorf("%s's %s job inherits permissions instead of declaring them", consoleTestsWorkflowPath, name)
		}
	}

	// The install is `npm ci` from the committed lockfile, with the flags the
	// plan names, and nothing else installs anything: no `npm install`, no
	// `npm test` (the script is `test:unit`), and no unpinned `npx`.
	installs := 0
	var runs []string
	for _, job := range wf.Jobs {
		for _, step := range job.Steps {
			runs = append(runs, step.Run)
			if step.Run == "npm ci --ignore-scripts --no-audit --no-fund" {
				installs++
			}
		}
	}
	if installs != 1 {
		t.Errorf("%s has %d steps running exactly `npm ci --ignore-scripts --no-audit --no-fund`, want 1: it is the plan's install step", consoleTestsWorkflowPath, installs)
	}
	all := strings.Join(runs, "\n")
	for _, want := range []string{"npm run test:unit", "npm run test:e2e"} {
		if !strings.Contains(all, want) {
			t.Errorf("%s never runs %q", consoleTestsWorkflowPath, want)
		}
	}
	for _, forbidden := range []string{"npm install", "npm test\n", "npx "} {
		if strings.Contains(text, forbidden) {
			t.Errorf("%s contains %q: the install is `npm ci` from the lockfile, and nothing here is unpinned", consoleTestsWorkflowPath, forbidden)
		}
	}

	// The two runners, and the Linux pass under a display: Electron has no X on
	// a bare runner, and macOS needs none.
	job := wf.Jobs["console"]
	if !slices.Contains(job.Strategy.Matrix.OS, "ubuntu-latest") || !slices.Contains(job.Strategy.Matrix.OS, "macos-latest") {
		t.Errorf("%s's console job runs on %v, want both ubuntu-latest and macos-latest", consoleTestsWorkflowPath, job.Strategy.Matrix.OS)
	}
	linux, darwin := "", ""
	for _, step := range job.Steps {
		if strings.Contains(step.Run, "test:e2e") {
			switch step.If {
			case "runner.os == 'Linux'":
				linux = step.Run
			case "runner.os != 'Linux'":
				darwin = step.Run
			}
		}
	}
	if !strings.HasPrefix(linux, "xvfb-run") {
		t.Errorf("%s's Linux end-to-end step is %q, want it under xvfb-run: Electron needs a display", consoleTestsWorkflowPath, linux)
	}
	if darwin == "" || strings.Contains(darwin, "xvfb") {
		t.Errorf("%s's macOS end-to-end step is %q, want the plain e2e script (xvfb-run does not exist there)", consoleTestsWorkflowPath, darwin)
	}

	// The Node version is read from console/package.json's `engines.node`, never
	// written here a second time: test/unit/pinning.test.ts keeps that field an
	// exact pin, and a copy of it in this file is a pin that can disagree.
	nodeSteps := 0
	for _, step := range job.Steps {
		if !strings.Contains(step.Uses, "actions/setup-node@") {
			continue
		}
		nodeSteps++
		if step.With["node-version-file"] != "console/package.json" {
			t.Errorf("%s's setup-node takes node-version-file %q, want console/package.json's engines.node", consoleTestsWorkflowPath, step.With["node-version-file"])
		}
		if _, ok := step.With["node-version"]; ok {
			t.Errorf("%s's setup-node pins a version of its own instead of reading engines.node", consoleTestsWorkflowPath)
		}
	}
	if nodeSteps != 1 {
		t.Errorf("%s has %d setup-node steps, want 1", consoleTestsWorkflowPath, nodeSteps)
	}

	// And while both files exist, they are the same file: the staged copy is the
	// reviewed one, so a hand-edited or hand-written workflow under
	// .github/workflows/ is a failure here rather than something nobody reads.
	// (Absence is not asserted: the owner's copy-in is the whole point of the
	// staging, and an absence assertion would turn main red the moment they did
	// it -- Task 1.5.)
	if _, statErr := os.Stat(filepath.Join("..", "..", appliedConsoleTestsPath)); statErr == nil {
		applied, err := os.ReadFile(filepath.Join("..", "..", appliedConsoleTestsPath))
		if err != nil {
			t.Fatal(err)
		}
		if string(applied) != text {
			t.Errorf("%s differs from the staged %s: the workflow under .github/workflows/ must be the reviewed staged copy, not an edit of it", appliedConsoleTestsPath, consoleTestsWorkflowPath)
		}
	} else if !os.IsNotExist(statErr) {
		t.Fatal(statErr)
	}
}

// TestConsoleWorkflowJobIsGateable pins the job name and the triggers, which is
// half of what keeps scripts/release-gate.sh from refusing every tag in this
// repository. Task 4.6 is the other half, and it is in place: the gate's `names`
// array carries this job's check runs, which because the job is a matrix are
// `console (ubuntu-latest)` and `console (macos-latest)` -- derived from this
// file by TestReleaseGateRequiresTheConsoleCheck -- and never the bare key.
// Each check below is a separate way the `console` check runs stop existing on a
// commit the gate asks about, and the two mutations to try first are the ones
// that are invisible until a tag is refused: rename the job to `console-tests`,
// and add `paths: ['console/**']` to its trigger.
func TestConsoleWorkflowJobIsGateable(t *testing.T) {
	_, wf, on, rawJobs := readConsoleTestsWorkflow(t)

	// A job whose key is exactly `console`: the key is what GitHub prefixes onto
	// each matrix leg's check-run name, so it is the stem of the two names the
	// gate requires. A rename has to be a deliberate edit here AND there.
	job, ok := wf.Jobs["console"]
	if !ok {
		var keys []string
		for name := range wf.Jobs {
			keys = append(keys, name)
		}
		slices.Sort(keys)
		t.Fatalf("%s has no `console` job (it has %v): that key is the check name scripts/release-gate.sh will require once Task 4.6 names it", consoleTestsWorkflowPath, keys)
	}
	if len(wf.Jobs) != 1 {
		var keys []string
		for name := range wf.Jobs {
			keys = append(keys, name)
		}
		slices.Sort(keys)
		t.Errorf("%s has jobs %v: the console's tests are one job, and a second one is another check nobody asked for", consoleTestsWorkflowPath, keys)
	}

	// The displayed name is the check-run name, and it is the static string.
	// A matrix value interpolated into it (`console (${{ matrix.os }})`) would
	// rename the check to something the gate cannot find.
	if job.Name != "console" {
		t.Errorf("%s's console job is named %q, want exactly %q: that name is the stem every check run of the job is built from, and the gate requires the legs it produces (Task 4.6)", consoleTestsWorkflowPath, job.Name, "console")
	}
	if strings.Contains(job.Name, "${{") {
		t.Errorf("%s's console job name %q interpolates: a matrix value in the name is a check name the gate cannot look up", consoleTestsWorkflowPath, job.Name)
	}

	// Triggers: every pull request and every push to main, which is ci.yml's
	// pattern minus its docs-only skip.
	if !slices.Equal(wf.On.Push.Branches, []string{"main"}) {
		t.Errorf("%s's push trigger branches = %v, want [main]", consoleTestsWorkflowPath, wf.On.Push.Branches)
	}
	keys := make([]string, 0, len(on))
	for key := range on {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"pull_request", "push"}) {
		t.Errorf("%s triggers on %v, want exactly push and pull_request", consoleTestsWorkflowPath, keys)
	}

	// No `if:` on the job, in particular none on a `changes`/`docs_only` output:
	// a skipped check is not a success, so scripts/release-gate.sh -- which
	// requires the two check runs this job reports -- treats the leg as missing
	// and refuses the tag.
	if job.If != "" {
		t.Errorf("%s's console job carries `if: %s`: a guarded job is a check that can be skipped, and a skipped check is not a success", consoleTestsWorkflowPath, job.If)
	}

	// No `paths:`/`paths-ignore:` at the trigger level or on the job -- **every
	// trigger**, not just `push`. This is the failure that refuses every release,
	// including the ones that ship none of the console: the gate's `names` array
	// carries the two check runs this job reports, and it would require a check
	// this filter skipped. The check is a function over the parsed `on:`
	// map rather than a loop here, because the file has no such filter anywhere:
	// a loop passes whether or not it looks at the trigger it is about, and an
	// earlier revision skipped every key but `push`, so a filter under
	// `pull_request` -- the trigger that decides whether the console job runs on
	// a pull request at all -- was never inspected.
	if offences := triggerFilterOffences(on); len(offences) > 0 {
		t.Errorf("%s's %s the console job must run unconditionally or the release gate refuses every tag",
			consoleTestsWorkflowPath, strings.Join(offences, "; "))
	}
	for _, filter := range []string{"paths", "paths-ignore", "if"} {
		if _, ok := rawJobs["console"][filter]; ok {
			t.Errorf("%s's console job carries `%s:`, and the job that produces the gate's `console` check has neither", consoleTestsWorkflowPath, filter)
		}
	}
}

// triggerFilterOffences reports every trigger-level `paths:`/`paths-ignore:` in
// a parsed `on:` block: the filters that would stop the console job running for
// a change, which is the failure Task 4.6's release gate refuses every tag over.
//
// It is a function over the parsed map rather than a loop inside the test that
// calls it, because the staged workflow carries no such filter at any trigger --
// so a loop there passes by not being reached, which is exactly what an earlier
// revision did: it skipped every key but `push`, and a `paths:` under
// `pull_request:` (the trigger that decides whether the console job runs on a
// pull request at all) was never looked at.
// TestConsoleTriggerFilterOffencesSeesEveryTrigger hands this function the map
// that has one, so the check cannot go back to passing vacuously.
func triggerFilterOffences(on map[string]any) []string {
	keys := make([]string, 0, len(on))
	for key := range on {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	var offences []string
	for _, key := range keys {
		trigger, ok := on[key].(map[string]any)
		if !ok {
			// A bare `pull_request:` is YAML null: the trigger with no filters at
			// all, which is the state this check wants. Anything that is not a
			// mapping carries no `paths:` either.
			continue
		}
		for _, filter := range []string{"paths", "paths-ignore"} {
			if _, ok := trigger[filter]; ok {
				offences = append(offences, key+" trigger carries `"+filter+"`:")
			}
		}
	}
	return offences
}

// TestConsoleTriggerFilterOffencesSeesEveryTrigger is the mutation-proof half of
// the trigger-filter check: it hands that check a map with a filter on the
// trigger the earlier revision skipped, and one on a trigger that does not exist
// today, and requires both to be reported -- and the shape the staged workflow
// actually has to report nothing, so the check is not passing by refusing
// everything.
func TestConsoleTriggerFilterOffencesSeesEveryTrigger(t *testing.T) {
	hostile := map[string]any{
		"push":              map[string]any{"branches": []any{"main"}},
		"pull_request":      map[string]any{"paths": []any{"console/**"}},
		"workflow_dispatch": map[string]any{"paths-ignore": []any{"docs/**"}},
	}
	want := []string{
		"pull_request trigger carries `paths`:",
		"workflow_dispatch trigger carries `paths-ignore`:",
	}
	if got := triggerFilterOffences(hostile); !slices.Equal(got, want) {
		t.Errorf("triggerFilterOffences(%v) = %v, want %v", hostile, got, want)
	}
	// The staged file's own `on:` block: `push:` with branches, and a bare
	// `pull_request:` (YAML null).
	clean := map[string]any{"push": map[string]any{"branches": []any{"main"}}, "pull_request": nil}
	if got := triggerFilterOffences(clean); len(got) != 0 {
		t.Errorf("triggerFilterOffences(%v) = %v, want no offences: a filter on a bare trigger is nil, not an offence", clean, got)
	}
}

// TestPendingWorkflowsReadmeNamesTheConsoleTestsRow asserts the staging
// directory's README carries THIS file's row: what it is, that it is staged
// because no agent may push a workflow here, and the one command that applies
// it. The name is singular and row-specific on purpose -- Task 4.2 adds a second
// console row and a second test, `TestDocsPendingWorkflowsReadmeNamesTheConsoleRows`
// (plural), which asserts both -- so no reader has to work out whether two
// similarly-named tests are the same test.
func TestPendingWorkflowsReadmeNamesTheConsoleTestsRow(t *testing.T) {
	doc := readDoc(t, "docs/pending-workflows/README.md")
	for _, want := range []string{
		// What the file is: the console's own tests, on the two runners, with
		// no secret and nothing to gate it.
		"console-tests.yml",
		"npm run test:unit",
		"macos-latest",
		"xvfb-run",
		// Why it is staged here rather than applied.
		"Fugaro refuses to push a commit that touches `.github/workflows/**`",
		// The one command that applies it.
		"cp docs/pending-workflows/console-tests.yml .github/workflows/console-tests.yml",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/pending-workflows/README.md lacks %q: the console's staged workflow needs its row here, with what it is, why it is staged and the one command that applies it", want)
		}
	}
	// The staged file must exist under its own name, and the row must not tell a
	// reader to delete it: unlike images.yml this staged copy is kept after the
	// copy-in, because TestConsoleTestsWorkflowIsStagedAndPinned asserts the two
	// files are byte-identical from then on.
	if _, err := os.Stat(filepath.Join("..", "..", consoleTestsWorkflowPath)); err != nil {
		t.Fatalf("%s is missing: the README describes a staged file that must be here (%v)", consoleTestsWorkflowPath, err)
	}
	if !strings.Contains(doc, filepath.Base(consoleTestsWorkflowPath)+"` is **not** deleted") &&
		!strings.Contains(doc, "is not deleted after the copy") {
		t.Error("docs/pending-workflows/README.md does not say the console-tests.yml staged copy is kept after the copy-in: it is, because the byte-identity test reads it afterwards")
	}

	// Task 1.5's record: console/README.md names WHEN the workflow was applied
	// as a commit (not a date), and which console tasks had CI and which did not.
	readme := readDoc(t, "console/README.md")
	var line string
	for _, l := range strings.Split(readme, "\n") {
		if strings.Contains(l, "Tests workflow applied at commit") {
			line = l
			break
		}
	}
	if line == "" {
		t.Fatal("console/README.md lacks the Task 1.5 line beginning \"Tests workflow applied at commit\": it must name the applying commit")
	}
	if !regexp.MustCompile(`\b[0-9a-f]{7,40}\b`).MatchString(line) {
		t.Errorf("the Task 1.5 line names no commit hash (7+ hex characters): %q", line)
	}
	if regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}\b`).MatchString(line) {
		t.Errorf("the Task 1.5 line names a date; it must name the applying commit only: %q", line)
	}
	for _, want := range []string{"Tasks 1.0 to 1.4", "no console CI"} {
		if !strings.Contains(line, want) {
			t.Errorf("the Task 1.5 line lacks %q: it must say which console commits had no CI", want)
		}
	}
}

// ---------------------------------------------------------------------------
// Task 4.2: the console's staged signing and notarisation workflow.
//
// The second staged file, and the reason there are two rather than one: this one
// reads the Apple signing material, so it must never run on a pull request, while
// console-tests.yml runs on every pull request and holds nothing. One file
// carrying both would put the key's job definition next to a job any branch can
// run, where the only thing between a fork's pull request and the signing
// material is GitHub's own rule about *forks* (design section 9.4; plan Task
// 4.2's table). These three tests hold the split and the file's shape.
//
// The workflow's *behaviour* cannot run here -- it has never run, and the
// directory's README says so -- but its text can be pinned, which is what these
// do. The mutations that matter, and the ones the red step runs first: adding
// `pull_request:` to the file's `on:` (TestDocsConsoleReleaseWorkflowHoldsNoPull-
// RequestTrigger), a planted `uses: actions/checkout@v5` (the pin loop), and a
// signing step that skips when a secret is absent instead of failing (the
// fail-closed assertion).
// ---------------------------------------------------------------------------

const (
	// consoleReleaseWorkflowPath is the staged signing workflow; appliedConsoleReleasePath
	// is where the owner copies it (plan Task 4.5).
	consoleReleaseWorkflowPath = "docs/pending-workflows/console-release.yml"
	appliedConsoleReleasePath  = ".github/workflows/console-release.yml"

	// consoleReleaseTagWorkflowPath is the staged supersede file that moves the
	// console onto its own `console-vX.Y.Z` tag stream: the console no longer
	// releases from the CLI's `v*` tag, it has its own version line, its own
	// notes file and its own release. While it is staged it is the file's
	// *effective* text -- it is what the owner's copy-in writes over both copies
	// of console-release.yml -- so these tests read it through
	// consoleReleaseEffectiveText rather than reading console-release.yml, which
	// is still the file that runs until that copy-in happens. The byte-identity
	// clause between console-release.yml's two homes is unchanged and still
	// asserted: this file supersedes it, and does not touch it.
	consoleReleaseTagWorkflowPath = "docs/pending-workflows/console-release-tag.yml"
)

// consoleReleaseSecrets is the exact set of secrets the signing job reads, and
// the names the owner's protected GitHub environment `console-release` holds.
// The names are the owner's own (2026-10-17) and D4 does not settle the
// notarisation credential: it is the App Store Connect API key, not an
// app-specific password, so the file names APPLE_API_KEY, APPLE_API_KEY_ID and
// APPLE_API_ISSUER and never APPLE_ID/APPLE_APP_PASSWORD.
var consoleReleaseSecrets = []string{
	"APPLE_CERTIFICATE",
	"APPLE_CERTIFICATE_PASSWORD",
	"APPLE_TEAM_ID",
	"APPLE_API_KEY",
	"APPLE_API_KEY_ID",
	"APPLE_API_ISSUER",
}

// consoleReleaseWorkflow is the staged signing file's shape, as far as these
// tests read it. The unmodelled keys are covered separately, from the raw
// mapping, so an `if:` or a `paths:` cannot hide behind a field this struct
// does not have.
type consoleReleaseWorkflow struct {
	On struct {
		Push struct {
			Tags     []string `yaml:"tags"`
			Branches []string `yaml:"branches"`
		} `yaml:"push"`
	} `yaml:"on"`
	Permissions map[string]string                    `yaml:"permissions"`
	Jobs        map[string]consoleReleaseWorkflowJob `yaml:"jobs"`
}

type consoleReleaseWorkflowJob struct {
	Name        string               `yaml:"name"`
	If          string               `yaml:"if"`
	RunsOn      string               `yaml:"runs-on"`
	Environment string               `yaml:"environment"`
	Needs       consoleNeeds         `yaml:"needs"`
	Permissions map[string]string    `yaml:"permissions"`
	Steps       []consoleReleaseStep `yaml:"steps"`
}

// consoleNeeds is a job's `needs:` as a list, whichever of GitHub's two
// spellings the file uses: one job name, or a list of them. It has to read
// both, because this file is read in both of its states: while the Linux change
// is staged, `publish` needs the one macOS leg; once the owner has copied it in,
// the same `publish` needs `[release, linux]`, and a `needs: string` field
// fails the whole file at parse time -- taking every test in this family with
// it -- rather than reading the second job name.
type consoleNeeds []string

func (n *consoleNeeds) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		if node.Value == "" {
			*n = nil
			return nil
		}
		*n = consoleNeeds{node.Value}
		return nil
	case yaml.SequenceNode:
		var names []string
		if err := node.Decode(&names); err != nil {
			return err
		}
		*n = consoleNeeds(names)
		return nil
	default:
		return fmt.Errorf("`needs:` must be a job name or a list of job names, not %s", node.Tag)
	}
}

type consoleReleaseStep struct {
	Name             string            `yaml:"name"`
	Id               string            `yaml:"id"`
	Uses             string            `yaml:"uses"`
	Run              string            `yaml:"run"`
	If               string            `yaml:"if"`
	With             map[string]string `yaml:"with"`
	Env              map[string]string `yaml:"env"`
	WorkingDirectory string            `yaml:"working-directory"`
}

// readConsoleReleaseWorkflow is the staged signing file's text, parsed both
// ways: into the typed shape above, and into raw mappings of the trigger block
// and of each job, where a key this test does not model is still visible.
// It reads the file's *effective* text -- consoleReleaseEffectiveText: the
// staged supersede file while it is staged, the applied copy afterwards -- so
// the same suite is green on both sides of the owner's copy-in.
//
// The failure messages below name consoleReleaseWorkflowPath, and while the
// supersede file is staged that is the file the change *supersedes* rather than
// the text just read: it is kept staged, byte-identical to the copy that runs,
// and its header names the supersede file and the copy-in -- the file a reader
// is being pointed on to anyway (docs/pending-workflows/README.md).
func readConsoleReleaseWorkflow(t *testing.T) (string, consoleReleaseWorkflow, map[string]any, map[string]map[string]any) {
	t.Helper()
	text, _ := consoleReleaseEffectiveText(t)
	var wf consoleReleaseWorkflow
	if err := yaml.Unmarshal([]byte(text), &wf); err != nil {
		t.Fatalf("%s is not valid YAML: %v", consoleReleaseWorkflowPath, err)
	}
	var raw struct {
		On   map[string]any            `yaml:"on"`
		Jobs map[string]map[string]any `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(text), &raw); err != nil {
		t.Fatalf("%s is not valid YAML: %v", consoleReleaseWorkflowPath, err)
	}
	if len(raw.On) == 0 || len(wf.Jobs) == 0 {
		t.Fatalf("%s parsed with no triggers (%d) or no jobs (%d): these tests read the file, not a comment about it",
			consoleReleaseWorkflowPath, len(raw.On), len(wf.Jobs))
	}
	return text, wf, raw.On, raw.Jobs
}

// consoleTriggerBlock is the file's `on:` block as text: the lines after `on:`
// up to the next top-level key. The forbidden-trigger scan runs over this and
// not over the whole file, so the file's own comment header can explain which
// triggers it refuses -- and the test cannot be satisfied by moving a trigger
// into a comment.
func consoleTriggerBlock(t *testing.T, text string) string {
	t.Helper()
	lines := strings.Split(text, "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimRight(line, " \t") == "on:" {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("%s has no top-level `on:` block: a workflow with no trigger is not a workflow", consoleReleaseWorkflowPath)
	}
	var block []string
	for _, line := range lines[start+1:] {
		if line != "" && line[0] != ' ' && line[0] != '\t' && line[0] != '#' {
			break
		}
		block = append(block, line)
	}
	return strings.Join(block, "\n")
}

// TestDocsConsoleReleaseWorkflowHoldsNoPullRequestTrigger is the named sub-assertion
// the security finding behind the two-file split turns on, split out so the
// failure sentence names the bug. The signing job reads the Apple certificate
// and the App Store Connect key from a protected environment; if it also ran on
// a pull request, the only thing between a branch of this repository and the
// signing material would be GitHub's rule about *forks*, which is a rule about
// forks and not about branches. **The mutation that proves it**: adding
// `pull_request:` to the file's `on:` makes this red.
func TestDocsConsoleReleaseWorkflowHoldsNoPullRequestTrigger(t *testing.T) {
	text, wf, on, _ := readConsoleReleaseWorkflow(t)
	block := consoleTriggerBlock(t, text)

	// The reason is quoted here rather than in a comment nobody reads, because
	// this is the assertion a future edit will meet.
	if strings.Contains(block, "pull_request") {
		t.Errorf("%s's `on:` block carries a pull_request trigger: the signing job must not run on a pull request -- it reads the Apple certificate and the App Store Connect key from a protected environment, and a branch of this repository can open one (design section 9.4)", consoleReleaseWorkflowPath)
	}
	for _, forbidden := range []string{"workflow_run", "workflow_dispatch"} {
		if strings.Contains(block, forbidden) {
			t.Errorf("%s's `on:` block carries %q: the signing job runs on a version tag push and on nothing else -- a dispatch trigger is a manual door into the signing material (design section 9.4)", consoleReleaseWorkflowPath, forbidden)
		}
	}

	// And the same thing read from the parsed trigger, so a trigger written in a
	// form the text scan above would miss is still caught: exactly one trigger,
	// `push`, of a strict vX.Y.Z tag, with no branch list.
	keys := make([]string, 0, len(on))
	for key := range on {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"push"}) {
		t.Errorf("%s triggers on %v, want exactly [push]: it is the tag-only signing job (plan Task 4.2's table)", consoleReleaseWorkflowPath, keys)
	}
	// The console's own tag, and only the console's: `console-vX.Y.Z`. The CLI's
	// `v*` tag releases the CLI (release.yml) and must not start a console
	// signing run any more -- three release streams, each with its own tag
	// (owner addendum 2026-10-22: CLI = animals, Console = fruits, Mobile =
	// birds). A loose `console-v*` would start a signing run for a tag the
	// version check below refuses.
	if !slices.Equal(wf.On.Push.Tags, []string{"console-v[0-9]+.[0-9]+.[0-9]+"}) {
		t.Errorf("%s's push trigger tags = %v, want exactly [console-v[0-9]+.[0-9]+.[0-9]+]: the console releases from its own console-vX.Y.Z tag, and a `v*` tag here would put the console back on the CLI's release stream", consoleReleaseWorkflowPath, wf.On.Push.Tags)
	}
	if len(wf.On.Push.Branches) != 0 {
		t.Errorf("%s's push trigger carries branches %v: the signing job runs on a tag and never on a branch", consoleReleaseWorkflowPath, wf.On.Push.Branches)
	}
	if _, ok := on["push"].(map[string]any)["paths"]; ok {
		t.Errorf("%s's push trigger carries `paths:`: a filtered tag push is a release with no console build at all", consoleReleaseWorkflowPath)
	}
}

// consoleReleaseSigningStep is the name of the one step in the signing job that
// holds a secret. Everything that needs the certificate or the App Store
// Connect key happens inside it, after every install and download has run with
// no secret in any step's environment (review of Task 4.2, round 2).
const consoleReleaseSigningStep = "Sign, notarise and staple the console"

// consoleReleaseVerifyStep is the release job's verification step, the only
// step of the workflow that carries the Apple-network retry helper: it
// validates the ticket on the bundle inside the mounted .dmg, the one inside
// the extracted .zip, and the disk image itself. (The signing step's staples
// and validations are the script's, and the helper there is the script's own
// copy of the same text.)
const consoleReleaseVerifyStep = "Verify the shipped artifacts, and record it"

// consoleRetryHelperName is the one shell function that retries the
// Apple-network calls (`xcrun stapler staple` and `xcrun stapler validate`),
// defined once in console/scripts/notarize-and-staple.sh and, character for
// character, once in the verification step. The October 2026 release run
// (39163351646) failed at Apple's ticket-delivery lookup with NSURLErrorDomain
// -1005, "The network connection was lost" -- a network failure, not a
// rejection. Two of its five attempts reached Apple: the packer's own staple
// inside `npm run package` (the fourth) and a `stapler validate` on the bundle
// in the workflow (the fifth); the three earlier attempts never got that far
// (setup errors, and one with no step started at all). So those calls get five
// attempts with a growing pause, and everything else in the file still fails
// at once.
const consoleRetryHelperName = "retry_apple_network"

// consoleNotarizeScriptPath is the script the signing step notarises, staples
// and validates with: the packer's own notarise-and-staple is inside
// `npm run package`, where its staple cannot be retried on its own, so the
// workflow packs with `-c.mac.notarize=false` and does those three calls here.
// The script is shell, not Go, so it is outside the Go/console boundary
// (internal/cli/docs_console_boundary_test.go) -- but the test that reads it,
// and every other test that names a console/ path, has to live in this file,
// which is the boundary's one allow-listed entry.
const consoleNotarizeScriptPath = "console/scripts/notarize-and-staple.sh"

// consoleReleasePublishStep and consoleReleaseTagStep are the publish job's
// release step and the release job's tag-shape step, which the behavioural
// tests below run under bash with a faked `gh`.
const (
	consoleReleasePublishStep = "Publish the console's own release"
	// consoleReleaseTagStep shapes the tag, consoleReleaseVersionStep holds the
	// tag's X.Y.Z to console/package.json's own version line, and
	// consoleReleaseNotesStep holds it to the console's notes file for that
	// version. All three run before anything is built, and each refuses on its
	// own; the behavioural tests below run the first two.
	consoleReleaseTagStep     = "The tag is a strict console-vX.Y.Z"
	consoleReleaseVersionStep = "The tag's version is console/package.json's"
	consoleReleaseNotesStep   = "The console's notes for this version exist"
)

// consoleArtifactPins are the upload/download-artifact pins this repository's
// applied workflows (release.yml, images.yml) already use. The console's file
// reuses them rather than introducing a SHA nobody has read.
var consoleArtifactPins = map[string]string{
	"actions/upload-artifact":   "cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2",
	"actions/download-artifact": "9000827ccba6bdab643e8b6fd33ac0654aef8333 # v8.0.2",
}

// consoleReleaseActions is every action the console's signing pipeline may run
// at all: the four the two build-and-publish halves need, and -- once the Linux
// change is applied -- the cosign installer the keyless signature over the
// merged sums file needs. It is an allow-list rather than a count because the
// file is read in both of its states, and the Linux leg brings its own checkout,
// setup-node and upload with it.
var consoleReleaseActions = []string{
	"actions/checkout",
	"actions/setup-node",
	"actions/upload-artifact",
	"actions/download-artifact",
	"sigstore/cosign-installer",
}

// consoleRunLines is a step's `run:` script as trimmed, non-empty lines with
// shell comments dropped, so an assertion about a command cannot be satisfied
// by a comment that names it.
func consoleRunLines(run string) []string {
	var out []string
	for _, line := range strings.Split(run, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// consoleRetryHelper is one step's Apple-network retry helper as text, from its
// `retry_apple_network() {` line to the closing brace at the same indentation.
// It fails the test when the step defines it twice (one definition per step,
// called from the sites that need it) or not at all, and it returns the text
// rather than a bool so the two definitions can be compared instead of
// trusted.
func consoleRetryHelper(t *testing.T, where, run string) string {
	t.Helper()
	head := consoleRetryHelperName + "() {"
	lines := strings.Split(run, "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) != head {
			continue
		}
		if start >= 0 {
			t.Fatalf("%s defines %s more than once: one definition per step, and the call sites name it", where, consoleRetryHelperName)
		}
		start = i
	}
	if start < 0 {
		t.Fatalf("%s does not define %s: every Apple-network call there would fail on the first lost connection", where, consoleRetryHelperName)
	}
	indent := lines[start][:len(lines[start])-len(strings.TrimLeft(lines[start], " "))]
	close := indent + "}"
	for i := start + 1; i < len(lines); i++ {
		if lines[i] == close {
			return strings.Join(lines[start:i+1], "\n")
		}
	}
	t.Fatalf("%s's %s has no closing %q: the rest of the step would be its body", where, consoleRetryHelperName, close)
	return ""
}

// consoleLineIndex is the index of the first line of lines equal to want, or
// containing it when contains is true; -1 when there is none.
func consoleLineIndex(lines []string, want string, contains bool) int {
	for i, line := range lines {
		if line == want || (contains && strings.Contains(line, want)) {
			return i
		}
	}
	return -1
}

// consoleReleaseRawSteps is one job's steps as raw mappings, so a key the
// typed struct does not model (`continue-on-error`, a `shell:`) is still
// visible to the assertions.
func consoleReleaseRawSteps(t *testing.T, rawJobs map[string]map[string]any, job string) []map[string]any {
	t.Helper()
	list, ok := rawJobs[job]["steps"].([]any)
	if !ok {
		t.Fatalf("%s's %s job has no step list", consoleReleaseWorkflowPath, job)
	}
	var steps []map[string]any
	for i, s := range list {
		m, ok := s.(map[string]any)
		if !ok {
			t.Fatalf("%s's %s job step %d is not a mapping", consoleReleaseWorkflowPath, job, i)
		}
		steps = append(steps, m)
	}
	return steps
}

// consoleStepNamed is the step of job called name, failing the test if there
// is none.
func consoleStepNamed(t *testing.T, job consoleReleaseWorkflowJob, jobName, name string) (int, consoleReleaseStep) {
	t.Helper()
	for i, s := range job.Steps {
		if s.Name == name {
			return i, s
		}
	}
	t.Fatalf("%s's %s job has no step named %q", consoleReleaseWorkflowPath, jobName, name)
	return -1, consoleReleaseStep{}
}

// TestDocsConsoleReleaseWorkflowIsStagedAndPinned holds the staged signing file
// to what the plan's Task 4.2 says it is, as amended by its two review rounds
// and the owner's "Separate console release" ruling of 2026-10-17: pinned,
// tag-only, gated on main, the tag's version stamped on every pack, no secret in
// any step's environment while anything installs, every secret in exactly one
// step, fail-closed with no way to skip a step, the write token only in a
// `publish` job that runs no repository code, its own `console-vX.Y.Z` release
// and never the CLI's, and -- once the owner has copied it in -- byte-identical
// to the file under .github/workflows/.
//
// It reads the file structurally (yaml.v3) wherever a substring scan is what
// would let a mutation through. Each clause names the mutation it was proved
// against in console/README.md's Task 4.2 table.
func TestDocsConsoleReleaseWorkflowIsStagedAndPinned(t *testing.T) {
	text, wf, _, rawJobs := readConsoleReleaseWorkflow(t)

	// Every `uses:` is pinned to a full commit SHA with a `# vX.Y.Z` comment,
	// and it names one of the actions this pipeline may run at all: checkout and
	// setup-node before a build, upload-artifact after it, download-artifact in
	// `publish`, and -- once the Linux change is applied -- the cosign installer
	// the keyless signature over the merged sums file needs. The two artifact
	// actions carry the pins release.yml and images.yml already use.
	//
	// The count is not pinned: four is a claim about which of the file's two
	// states it is in -- the Linux leg adds its own checkout, setup-node, upload
	// and the installer -- rather than about what it runs, and this test reads
	// the file in both states. The allow-list is the property, and it holds in
	// both: an action outside it is a trust decision nobody made.
	used := map[string]int{}
	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(line, "uses:") || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if !consoleUsesLine.MatchString(line) {
			t.Errorf("%s: %q is not a full 40-hex pin with a `# vX.Y.Z` comment", consoleReleaseWorkflowPath, strings.TrimSpace(line))
		}
		trimmed := strings.TrimSpace(line)
		action, _, _ := strings.Cut(strings.TrimSpace(trimmed[strings.Index(trimmed, "uses:")+len("uses:"):]), "@")
		used[action]++
		for known, pin := range consoleArtifactPins {
			if strings.Contains(line, known+"@") && !strings.HasSuffix(trimmed, known+"@"+pin) {
				t.Errorf("%s pins %s as %q, want the repository's existing pin %s", consoleReleaseWorkflowPath, known, trimmed, pin)
			}
		}
	}
	for action := range used {
		if !slices.Contains(consoleReleaseActions, action) {
			t.Errorf("%s runs the action %s: this pipeline runs checkout, setup-node, the two artifact actions and the cosign installer, and nothing else", consoleReleaseWorkflowPath, action)
		}
	}
	for _, action := range consoleReleaseActions[:4] {
		if used[action] == 0 {
			t.Errorf("%s runs no %s: it builds the console, hands the artifacts to `publish`, and neither can happen without it", consoleReleaseWorkflowPath, action)
		}
	}

	// Two jobs: `release` (environment, secrets, packer, read-only token) and
	// `publish` (no environment, no packer, no Apple secret, the write token).
	// The Linux change adds a third, `linux`, which its own test holds to its
	// shape; here it may only be absent or present, never a fourth thing.
	var keys []string
	for name := range wf.Jobs {
		keys = append(keys, name)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"publish", "release"}) && !slices.Equal(keys, []string{consoleReleaseLinuxJobName, "publish", "release"}) {
		t.Fatalf("%s has jobs %v, want [publish release] while the Linux change is staged and [%s publish release] once it is applied", consoleReleaseWorkflowPath, keys, consoleReleaseLinuxJobName)
	}
	job, pub := wf.Jobs["release"], wf.Jobs["publish"]
	// Whether the Linux change is applied, which is what decides the shape of
	// the clauses below that the merge moves: `publish`'s step list and the
	// asset line of its `gh release create`.
	linuxApplied := false
	if _, ok := wf.Jobs[consoleReleaseLinuxJobName]; ok {
		linuxApplied = true
	}
	if job.RunsOn != "macos-15" {
		t.Errorf("%s's release job runs on %q, want the pinned macos-15: the runner image is the signing toolchain, and macos-latest moves it without a reviewed edit", consoleReleaseWorkflowPath, job.RunsOn)
	}
	if pub.RunsOn != "ubuntu-24.04" {
		t.Errorf("%s's publish job runs on %q, want the pinned ubuntu-24.04", consoleReleaseWorkflowPath, pub.RunsOn)
	}
	if job.Environment != "console-release" {
		t.Errorf("%s's release job takes environment %q, want exactly %q (owner, 2026-10-17)", consoleReleaseWorkflowPath, job.Environment, "console-release")
	}
	if pub.Environment != "" {
		t.Errorf("%s's publish job takes environment %q: it holds no Apple secret and needs none", consoleReleaseWorkflowPath, pub.Environment)
	}
	// `publish` needs the release job -- it publishes only what that job
	// verified -- and, once the Linux change is applied, the Linux leg with it:
	// every leg has to have finished before anything is attached to a release
	// that cannot take another asset afterwards. Nothing else may gate it.
	if !slices.Contains(pub.Needs, "release") || (!slices.Equal(pub.Needs, []string{"release"}) && !slices.Equal(pub.Needs, []string{"release", "linux"})) {
		t.Errorf("%s's publish job needs %v, want [release] while the Linux change is staged and [release, linux] once it is applied: it publishes only what the legs verified", consoleReleaseWorkflowPath, pub.Needs)
	}

	// Permissions: the write token is the publish job's alone. The release job
	// runs the packer's dependency tree twice and holds `contents: read`.
	if strings.Contains(text, "write-all") {
		t.Errorf("%s contains `write-all`", consoleReleaseWorkflowPath)
	}
	if len(wf.Permissions) != 1 || wf.Permissions["contents"] != "read" {
		t.Errorf("%s's workflow-level permissions = %v, want exactly contents: read", consoleReleaseWorkflowPath, wf.Permissions)
	}
	if want := map[string]string{"contents": "read", "checks": "read"}; !reflect.DeepEqual(job.Permissions, want) {
		t.Errorf("%s's release job permissions = %v, want exactly %v: the job that runs the packer never holds a token that can write a release", consoleReleaseWorkflowPath, job.Permissions, want)
	}
	// `contents: write` to create and publish the release, and -- once the
	// Linux change is applied -- `id-token: write` for the keyless cosign call
	// over the merged sums file. Nothing else, in either state.
	wantPub := map[string]string{"contents": "write"}
	if linuxApplied {
		wantPub["id-token"] = "write"
	}
	if !reflect.DeepEqual(pub.Permissions, wantPub) {
		t.Errorf("%s's publish job permissions = %v, want exactly %v", consoleReleaseWorkflowPath, pub.Permissions, wantPub)
	}

	// **Nothing can skip a step or swallow its failure**, in either job: no
	// `if:` except the release job's cleanup `always()`, no
	// `continue-on-error`, no workflow- or job-level `env:`/`defaults:`.
	var top map[string]any
	if err := yaml.Unmarshal([]byte(text), &top); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"env", "defaults"} {
		if _, ok := top[key]; ok {
			t.Errorf("%s has a workflow-level `%s:`: every step would inherit it, and secrets live in exactly one step's env", consoleReleaseWorkflowPath, key)
		}
	}
	type jobRef struct {
		name string
		job  consoleReleaseWorkflowJob
		raw  []map[string]any
	}
	jobs := []jobRef{
		{"release", job, consoleReleaseRawSteps(t, rawJobs, "release")},
		{"publish", pub, consoleReleaseRawSteps(t, rawJobs, "publish")},
	}
	// Once the Linux change is applied the file carries a third job, and the
	// rules this loop asserts are about the file rather than about two of its
	// jobs -- "no step can be skipped and no failure swallowed" is what makes
	// the signing and the publishing unskippable, and a job outside the loop is
	// a job outside that claim. Its own test holds the standalone staged file to
	// its build-only shape; here it is held to the shared hygiene.
	if linux, ok := wf.Jobs[consoleReleaseLinuxJobName]; ok {
		jobs = append(jobs, jobRef{consoleReleaseLinuxJobName, linux, consoleReleaseRawSteps(t, rawJobs, consoleReleaseLinuxJobName)})
	}
	cleanup := -1
	for _, j := range jobs {
		for _, key := range []string{"env", "defaults", "if", "continue-on-error"} {
			if _, ok := rawJobs[j.name][key]; ok {
				t.Errorf("%s's %s job carries a job-level `%s:`: every step would inherit an env, and a guarded or failure-tolerant job is a release that silently publishes nothing, or an unsigned one", consoleReleaseWorkflowPath, j.name, key)
			}
		}
		if len(j.raw) != len(j.job.Steps) {
			t.Fatalf("%s's %s job: %d raw steps, %d typed", consoleReleaseWorkflowPath, j.name, len(j.raw), len(j.job.Steps))
		}
		for i, raw := range j.raw {
			if _, ok := raw["continue-on-error"]; ok {
				t.Errorf("%s's step %q carries `continue-on-error`", consoleReleaseWorkflowPath, j.job.Steps[i].Name)
			}
			if _, ok := raw["shell"]; ok {
				t.Errorf("%s's step %q sets its own `shell:`", consoleReleaseWorkflowPath, j.job.Steps[i].Name)
			}
			cond, has := raw["if"]
			if !has {
				continue
			}
			if j.name == "release" && cond == "always()" && strings.Contains(j.job.Steps[i].Run, "security delete-keychain") && cleanup < 0 {
				cleanup = i
				continue
			}
			t.Errorf("%s's step %q carries `if: %v`: no step is conditional except the release job's cleanup always(), so nothing (the gate, the fail-closed check, the signing, the publishing) can be skipped", consoleReleaseWorkflowPath, j.job.Steps[i].Name, cond)
		}
	}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "#") && strings.Contains(trimmed, "continue-on-error") {
			t.Errorf("%s carries %q: nothing in the signing workflow is allowed to fail quietly", consoleReleaseWorkflowPath, trimmed)
		}
	}
	if cleanup < 0 {
		t.Fatalf("%s has no `if: always()` step deleting the temporary keychain", consoleReleaseWorkflowPath)
	}
	if cleanup != len(job.Steps)-1 {
		t.Errorf("%s's cleanup is step %d of %d, want the release job's last", consoleReleaseWorkflowPath, cleanup, len(job.Steps))
	}
	for _, want := range []string{"appstoreconnect.p8", "console-certificate.p12", "hdiutil detach", "security delete-keychain"} {
		if !strings.Contains(job.Steps[cleanup].Run, want) {
			t.Errorf("%s's cleanup step does not use %q", consoleReleaseWorkflowPath, want)
		}
	}

	// Script hygiene over every step of both jobs:
	//   - `|| true` (or `|| :`) only in the cleanup step;
	//   - no `${{ }}` inside a `run:` -- values reach a script through `env:`;
	//   - no shell tracing (`set -x`, `set -eux`, `set -o xtrace`, `bash -x`;
	//     `ditto -x` is ditto's extract flag, hence the shell forms only);
	//   - no download-and-run: `curl`, `wget`, a pipe into `sh`/`bash`,
	//     `eval`, `source <(...)`;
	//   - no environment dump: a bare `env`, `printenv`, `set`, `export -p`,
	//     `declare -p`/`-x`, as a command or piped.
	trace := regexp.MustCompile(`\bset\s+-[a-zA-Z]*x|\bset\s+-o\s+xtrace|\bbash\s+(-[a-zA-Z]+\s+)*-[a-zA-Z]*x|\bsh\s+-[a-zA-Z]*x`)
	fetchRun := regexp.MustCompile(`\bcurl\b|\bwget\b|\|\s*(sudo\s+)?(ba|z|da)?sh\b|\beval\b|\bsource\s+<\(|\.\s+<\(`)
	envDump := regexp.MustCompile(`(^|[;&|(]\s*|\$\(\s*)(env|printenv|set|export\s+-p|declare\s+-[a-zA-Z]*[px][a-zA-Z]*|compgen\s+-v)\s*($|[|>;&)])|\bprintenv\b`)
	for _, j := range jobs {
		for i, step := range j.job.Steps {
			if strings.Contains(step.Run, "${{") {
				t.Errorf("%s's step %q inlines a `${{ }}` expression into its script: values reach a `run:` through `env:` only", consoleReleaseWorkflowPath, step.Name)
			}
			for _, line := range consoleRunLines(step.Run) {
				if !(j.name == "release" && i == cleanup) && (strings.Contains(line, "|| true") || strings.Contains(line, "|| :")) {
					t.Errorf("%s's step %q swallows a failure (%q): `|| true` is the cleanup step's and nowhere else", consoleReleaseWorkflowPath, step.Name, line)
				}
				if trace.MatchString(line) {
					t.Errorf("%s's step %q traces its shell (%q): a traced shell prints every expanded value, the secrets included", consoleReleaseWorkflowPath, step.Name, line)
				}
				if fetchRun.MatchString(line) {
					t.Errorf("%s's step %q fetches or evaluates code (%q): nothing is downloaded and run outside the lockfile and the pinned actions", consoleReleaseWorkflowPath, step.Name, line)
				}
				if envDump.MatchString(line) {
					t.Errorf("%s's step %q dumps its environment (%q): that prints every value in it, the secrets included", consoleReleaseWorkflowPath, step.Name, line)
				}
			}
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") && trace.MatchString(line) {
			t.Errorf("%s traces its shell (%q)", consoleReleaseWorkflowPath, strings.TrimSpace(line))
		}
	}

	// No secret-shaped literal anywhere, and no PEM block.
	if strings.Contains(text, "-----BEGIN") {
		t.Errorf("%s contains a PEM block", consoleReleaseWorkflowPath)
	}
	var body []string
	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(line, "uses:") {
			body = append(body, line)
		}
	}
	for _, blob := range consoleBlob.FindAllString(strings.Join(body, "\n"), -1) {
		if !consoleWord.MatchString(blob) {
			t.Errorf("%s carries %q, which is shaped like a pasted credential", consoleReleaseWorkflowPath, blob)
		}
	}

	stepNamed := func(name string) (int, consoleReleaseStep) {
		t.Helper()
		return consoleStepNamed(t, job, "release", name)
	}

	// **Secrets are in exactly one step's `env:`** of the release job, each
	// bound to its own name; the publish job holds none, and `GH_TOKEN` is in
	// exactly two steps: the release gate (read-only token, check-runs) and the
	// publish job's release step.
	signIdx, sign := stepNamed(consoleReleaseSigningStep)
	gateIdx, gate := stepNamed("The tag is strict, on main, and its checks passed")
	_, publish := consoleStepNamed(t, pub, "publish", consoleReleasePublishStep)
	for _, j := range jobs {
		for i, step := range j.job.Steps {
			for name, value := range step.Env {
				if strings.Contains(value, "secrets.") && !(j.name == "release" && i == signIdx) {
					t.Errorf("%s's step %q reads %s from a secret: secrets are in exactly one step's env, %q", consoleReleaseWorkflowPath, step.Name, name, consoleReleaseSigningStep)
				}
				if strings.Contains(value, "github.token") || name == "GH_TOKEN" || name == "GITHUB_TOKEN" {
					// The gate step is the one that runs the gate, whichever job
					// it is in: this file runs it twice (the macOS leg and, once
					// the Linux change is applied, the Linux leg), and the rule
					// is about what a step does with the token rather than about
					// which job it sits in.
					if !strings.Contains(step.Run, "scripts/release-gate.sh") && step.Name != consoleReleasePublishStep {
						t.Errorf("%s's step %q holds the token (%s): only a step running the release gate and the publish step do -- the gate reads GitHub's own API and the publish step creates the release, and no other step has any business with a token", consoleReleaseWorkflowPath, step.Name, name)
					}
				}
			}
			for name, value := range step.With {
				if strings.Contains(value, "secrets.") || strings.Contains(value, "github.token") {
					t.Errorf("%s's step %q hands a secret or the token to an action's `with.%s`", consoleReleaseWorkflowPath, step.Name, name)
				}
			}
			if j.name == "publish" && (strings.Contains(step.Uses, "actions/checkout@") || strings.Contains(step.Uses, "actions/setup-node@") || strings.Contains(step.Run, "npm")) {
				t.Errorf("%s's publish job runs repository code (%q%q): the job holding the write token runs no checkout and no packer", consoleReleaseWorkflowPath, step.Uses, step.Name)
			}
		}
	}
	if gate.Env["GH_TOKEN"] != "${{ github.token }}" || publish.Env["GH_TOKEN"] != "${{ github.token }}" {
		t.Errorf("%s: the gate and the publish step must each read GH_TOKEN from github.token", consoleReleaseWorkflowPath)
	}
	wantSignEnv := map[string]string{"VERSION": "${{ steps.tag.outputs.version }}"}
	for _, name := range consoleReleaseSecrets {
		wantSignEnv[name] = "${{ secrets." + name + " }}"
	}
	if !reflect.DeepEqual(sign.Env, wantSignEnv) {
		t.Errorf("%s's signing step env = %v, want exactly the six secrets, each bound to its own name, and the tag's version: %v", consoleReleaseWorkflowPath, sign.Env, wantSignEnv)
	}
	secretRef := regexp.MustCompile(`^\s+([A-Z][A-Z0-9_]*): \$\{\{ secrets\.([A-Z][A-Z0-9_]*) \}\}$`)
	refs := 0
	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(line, "secrets.") || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		m := secretRef.FindStringSubmatch(line)
		if m == nil || m[1] != m[2] {
			t.Errorf("%s: %q is not a step env binding of a secret to its own name", consoleReleaseWorkflowPath, strings.TrimSpace(line))
			continue
		}
		refs++
	}
	if refs != len(consoleReleaseSecrets) {
		t.Errorf("%s has %d secret references, want exactly %d (one each, in the signing step)", consoleReleaseWorkflowPath, refs, len(consoleReleaseSecrets))
	}

	// **The order: on main, a strict tag, the gate, then every install and
	// download, then the secrets, and after them only the upload of the
	// finished assets.**
	var runSteps []int
	for i, s := range job.Steps {
		if s.Run != "" {
			runSteps = append(runSteps, i)
		}
	}
	if len(runSteps) < 3 {
		t.Fatalf("%s's release job runs almost nothing", consoleReleaseWorkflowPath)
	}
	onMain := consoleRunLines(job.Steps[runSteps[0]].Run)
	ancestry := consoleLineIndex(onMain, `if ! git merge-base --is-ancestor "$GITHUB_SHA" origin/main; then`, false)
	if consoleLineIndex(onMain, "git fetch --quiet --no-tags origin main", false) < 0 || ancestry < 0 ||
		ancestry+2 >= len(onMain) || !strings.HasPrefix(onMain[ancestry+1], `echo "::error::`) || onMain[ancestry+2] != "exit 1" {
		t.Errorf("%s's first run step (%q) does not fetch main and refuse a tagged commit that is not its ancestor", consoleReleaseWorkflowPath, job.Steps[runSteps[0]].Name)
	}
	// The four checks before the gate: the commit is on main, the tag is a
	// strict `console-vX.Y.Z`, its version is console/package.json's, and the
	// console's notes for that version exist. Every one of them refuses before
	// the 90 minutes of packing, signing and notarising that follow, and none of
	// them holds a secret.
	tagIdx, tag := stepNamed(consoleReleaseTagStep)
	versionIdx, version := stepNamed(consoleReleaseVersionStep)
	notesIdx, notes := stepNamed(consoleReleaseNotesStep)
	if tagIdx != runSteps[1] || versionIdx != runSteps[2] || notesIdx != runSteps[3] || gateIdx != runSteps[4] {
		t.Errorf("%s's run steps begin %v, want the ancestry check, then the tag shape (%d), console/package.json's version (%d), the notes file (%d), then the gate (%d)",
			consoleReleaseWorkflowPath, runSteps[:5], tagIdx, versionIdx, notesIdx, gateIdx)
	}
	if tag.Id != "tag" || tag.Env["TAG"] != "${{ github.ref_name }}" {
		t.Errorf("%s's tag step must have id `tag` and read TAG from github.ref_name", consoleReleaseWorkflowPath)
	}
	// The version and notes steps read the tag step's output (`steps.tag.outputs.version`),
	// not the raw ref: the prefix is stripped in exactly one place, so a second
	// spelling of it cannot drift.
	if version.Env["VERSION"] != "${{ steps.tag.outputs.version }}" || notes.Env["VERSION"] != "${{ steps.tag.outputs.version }}" {
		t.Errorf("%s's version step env = %v and notes step env = %v, want both to read ${{ steps.tag.outputs.version }}", consoleReleaseWorkflowPath, version.Env, notes.Env)
	}
	// The prefix is stripped with `${TAG#console-v}` and never `${TAG#v}`: a
	// leading `v` is not the console tag's, so `${TAG#v}` would leave
	// `console-vX.Y.Z` as the version and stamp the app with it.
	if !strings.Contains(tag.Run, `echo "version=${TAG#console-v}" >> "$GITHUB_OUTPUT"`) || strings.Contains(tag.Run, `"${TAG#v}"`) {
		t.Errorf("%s's tag step (%q) does not strip the `console-v` prefix: %v", consoleReleaseWorkflowPath, tag.Name, tag.Run)
	}
	if !slices.Contains(consoleRunLines(version.Run), `declared="$(jq -r .version console/package.json)"`) {
		t.Errorf("%s's version step (%q) does not read console/package.json's version with jq, the way release.yml reads the plugin's: %v", consoleReleaseWorkflowPath, version.Name, version.Run)
	}
	if !strings.Contains(version.Run, `[ "$declared" != "$VERSION" ]`) {
		t.Errorf("%s's version step (%q) does not compare console/package.json's version with the tag's: %v", consoleReleaseWorkflowPath, version.Name, version.Run)
	}
	if !strings.Contains(notes.Run, `docs/releases/console/v$VERSION.md`) {
		t.Errorf("%s's notes step (%q) does not look for the console's own notes file docs/releases/console/v$VERSION.md", consoleReleaseWorkflowPath, notes.Name)
	}
	// The gate is the CLI's script, unchanged and CLI-only: it is handed `v` +
	// the version, never the console tag, and the token it needs to read the
	// commit's check-runs.
	if !slices.Contains(consoleRunLines(gate.Run), `scripts/release-gate.sh "v$VERSION"`) ||
		gate.Env["VERSION"] != "${{ steps.tag.outputs.version }}" || gate.Env["GH_TOKEN"] != "${{ github.token }}" {
		t.Errorf("%s's gate step (%q) does not run the CLI's release gate as `v` + the tag's version, with the check-runs token: env %v", consoleReleaseWorkflowPath, gate.Name, gate.Env)
	}
	installIdx, install := stepNamed("Install the pinned toolchain")
	if install.Run != "npm ci --ignore-scripts --no-audit --no-fund" || install.WorkingDirectory != "console" {
		t.Errorf("%s's install step is %q in %q, want exactly `npm ci --ignore-scripts --no-audit --no-fund` in console", consoleReleaseWorkflowPath, install.Run, install.WorkingDirectory)
	}
	warmIdx, warm := stepNamed("Download Electron and warm the packer's cache, with no secret present")
	warmLines := consoleRunLines(warm.Run)
	for _, want := range []string{"node node_modules/electron/install.js", `npm run package -- -c.mac.identity=null -c.mac.notarize=false -c.extraMetadata.version="$VERSION"`, "rm -rf release"} {
		if !slices.Contains(warmLines, want) {
			t.Errorf("%s's warm-up step does not run %q", consoleReleaseWorkflowPath, want)
		}
	}
	if warm.WorkingDirectory != "console" {
		t.Errorf("%s's warm-up step runs in %q, want console", consoleReleaseWorkflowPath, warm.WorkingDirectory)
	}
	for i, step := range job.Steps {
		if step.Uses != "" && i > signIdx && !strings.Contains(step.Uses, "actions/upload-artifact@") {
			t.Errorf("%s runs the action %q after the signing step: only the upload of the finished assets runs there", consoleReleaseWorkflowPath, step.Uses)
		}
	}
	if !(gateIdx < installIdx && installIdx < warmIdx && warmIdx < signIdx) {
		t.Errorf("%s's order is gate %d, install %d, warm-up %d, signing %d: want the gate, then the install, then the downloads, and only then the step holding the secrets", consoleReleaseWorkflowPath, gateIdx, installIdx, warmIdx, signIdx)
	}

	// **The version is the tag's, on every pack, and the packer never
	// notarises.** console/package.json says 0.0.0; every `npm run package`
	// stamps `-c.extraMetadata.version` from the tag step's output, through env
	// (mutation: the stamp removed from either pack). And every one of them
	// carries `-c.mac.notarize=false`, because electron-builder's own
	// notarise-and-staple runs *inside* that command: with the switch absent it
	// submits the bundle and staples it there, where a lost connection to
	// Apple's ticket delivery fails the pack and the staple cannot be retried
	// on its own -- the fourth attempt of the October 2026 release run, error
	// 68 (mutation: the switch removed from any pack).
	packs := 0
	for _, step := range job.Steps {
		for _, line := range consoleRunLines(step.Run) {
			if !strings.Contains(line, "npm run package") {
				continue
			}
			packs++
			if !strings.Contains(line, `-c.extraMetadata.version="$VERSION"`) {
				t.Errorf("%s's step %q packs without the tag's version (%q): every release would say 0.0.0", consoleReleaseWorkflowPath, step.Name, line)
			}
			if !strings.Contains(line, "-c.mac.notarize=false") {
				t.Errorf("%s's step %q packs with the packer's notarisation on (%q): electron-builder then submits and staples the bundle inside `npm run package`, where that staple is one call in the middle of the packer's process and cannot be retried on its own -- the failure the retry in %s exists for. Every pack here carries -c.mac.notarize=false, and only the owner's own `npm run package` (console/README.md, Task 4.1) leaves it on", consoleReleaseWorkflowPath, step.Name, line, consoleNotarizeScriptPath)
			}
			if step.Env["VERSION"] != "${{ steps.tag.outputs.version }}" {
				t.Errorf("%s's step %q packs with VERSION = %q, want the tag step's output", consoleReleaseWorkflowPath, step.Name, step.Env["VERSION"])
			}
		}
	}
	if packs != 3 {
		t.Errorf("%s packs %d times, want 3 (the unsigned warm-up, the signed bundle-only pack and the pack of the .dmg and .zip from the stapled bundle)", consoleReleaseWorkflowPath, packs)
	}

	// The packer only through `npm run package` or its no-rebuild variant,
	// `npm run package:targets`: never `npx`, never by name.
	for _, j := range jobs {
		for _, step := range j.job.Steps {
			for _, line := range consoleRunLines(step.Run) {
				if strings.Contains(line, "npx") || strings.Contains(line, "electron-builder") {
					t.Errorf("%s's step %q runs %q: the packer is the lockfile's, through `npm run package` (or `npm run package:targets`, its `--prepackaged` variant), and never resolved outside the lockfile", consoleReleaseWorkflowPath, step.Name, line)
				}
			}
		}
	}

	// **The signing step, line by line.**
	if sign.WorkingDirectory != "console" || sign.Id != "signed" {
		t.Errorf("%s's signing step runs in %q with id %q, want console and signed", consoleReleaseWorkflowPath, sign.WorkingDirectory, sign.Id)
	}
	lines := consoleRunLines(sign.Run)
	at := func(want string, contains bool) int {
		t.Helper()
		i := consoleLineIndex(lines, want, contains)
		if i < 0 {
			t.Errorf("%s's signing step does not run %q", consoleReleaseWorkflowPath, want)
		}
		return i
	}

	// Fail closed, first, naming the missing secret.
	loop := at("for name in "+strings.Join(consoleReleaseSecrets, " ")+"; do", false)
	empty := at(`if [ -z "${!name}" ]; then`, false)
	if empty >= 0 && (empty+1 >= len(lines) || lines[empty+1] != `missing="$missing $name"`) {
		t.Errorf("%s's fail-closed loop does not record an empty secret's name", consoleReleaseWorkflowPath)
	}
	refuse := at(`if [ -n "$missing" ]; then`, false)
	if refuse >= 0 && (refuse+2 >= len(lines) || !strings.HasPrefix(lines[refuse+1], `echo "::error::missing secret(s):$missing`) || lines[refuse+2] != "exit 1") {
		t.Errorf("%s's fail-closed check does not print the missing names and exit 1", consoleReleaseWorkflowPath)
	}
	firstUse := at("KEYCHAIN_PASSWORD=\"$(openssl rand -base64 32)\"", false)
	if loop != 2 || lines[0] != "set -euo pipefail" || lines[1] != `missing=""` || refuse < 0 || firstUse < refuse {
		t.Errorf("%s's signing step does not start with the fail-closed check (loop at %d, refusal at %d, first use at %d)", consoleReleaseWorkflowPath, loop, refuse, firstUse)
	}
	trap := at("trap remove_key_material EXIT", false)
	if trap < 0 || trap > firstUse {
		t.Errorf("%s's signing step does not set its exit trap before it creates key material", consoleReleaseWorkflowPath)
	}
	at(`security create-keychain -p "$KEYCHAIN_PASSWORD" "$KEYCHAIN"`, false)
	at(`security import "$P12" -k "$KEYCHAIN" -P "$APPLE_CERTIFICATE_PASSWORD" -T /usr/bin/codesign`, false)
	at(`security set-key-partition-list -S apple-tool:,apple: -s -k "$KEYCHAIN_PASSWORD" "$KEYCHAIN" > /dev/null`, false)
	if strings.Contains(text, "-T /usr/bin/security") {
		t.Errorf("%s trusts /usr/bin/security with the signing key: any later command could export it", consoleReleaseWorkflowPath)
	}
	writeP12 := at(`(umask 077 && printf '%s' "$APPLE_CERTIFICATE" | tr -d '\n' | openssl base64 -d -A > "$P12")`, false)
	writeKey := at(`(umask 077 && printf '%s' "$APPLE_API_KEY" > "$KEY")`, false)
	if strings.Contains(sign.Run, "chmod") {
		t.Errorf("%s's signing step chmods a file: key material is created private (umask 077), not tightened after", consoleReleaseWorkflowPath)
	}

	// No command prints the key material: every line that names a variable
	// holding it is one of these, exactly.
	sensitive := regexp.MustCompile(`\$\{?(APPLE_CERTIFICATE|APPLE_CERTIFICATE_PASSWORD|APPLE_API_KEY|KEYCHAIN_PASSWORD|P12|KEY)\b`)
	allowed := []string{
		`rm -f "$P12" "$KEY"`,
		lines[max(writeP12, 0)],
		`security create-keychain -p "$KEYCHAIN_PASSWORD" "$KEYCHAIN"`,
		`security unlock-keychain -p "$KEYCHAIN_PASSWORD" "$KEYCHAIN"`,
		`security import "$P12" -k "$KEYCHAIN" -P "$APPLE_CERTIFICATE_PASSWORD" -T /usr/bin/codesign`,
		`security set-key-partition-list -S apple-tool:,apple: -s -k "$KEYCHAIN_PASSWORD" "$KEYCHAIN" > /dev/null`,
		`rm -f "$P12"`,
		lines[max(writeKey, 0)],
		`export APPLE_API_KEY="$KEY"`,
		// There is deliberately no `xcrun notarytool submit ... --key "$KEY"`
		// entry here any more: the submission is the script's, and the signing
		// step names no xcrun at all (the clause below).
	}
	for _, j := range jobs {
		for _, step := range j.job.Steps {
			for _, line := range consoleRunLines(step.Run) {
				if sensitive.MatchString(line) && !slices.Contains(allowed, line) {
					t.Errorf("%s's step %q runs %q, which touches the key material outside the lines that create, import, hand on and remove it", consoleReleaseWorkflowPath, step.Name, line)
				}
			}
		}
	}

	// The team, on the certificate, before packing; the signature after.
	teamBefore := at(`*"($APPLE_TEAM_ID)\""*) ;;`, false)
	pack := at(`npm run package -- --dir -c.mac.notarize=false -c.extraMetadata.version="$VERSION"`, false)
	if teamBefore < 0 || pack < 0 || teamBefore > pack {
		t.Errorf("%s's signing step does not check the certificate's team before packing", consoleReleaseWorkflowPath)
	}
	teamAfter := at(`if [ "$signed_team" != "$team" ]; then`, false)
	if teamAfter < 0 || teamAfter+2 >= len(lines) || lines[teamAfter+2] != "exit 1" || teamAfter < pack {
		t.Errorf("%s's signing step does not refuse a bundle signed by another team after packing", consoleReleaseWorkflowPath)
	}
	unset := at("unset APPLE_CERTIFICATE APPLE_CERTIFICATE_PASSWORD APPLE_TEAM_ID", false)
	exportKey := at(`export APPLE_API_KEY="$KEY"`, false)
	at(`export CSC_NAME="$identity"`, false)
	at(`export CSC_KEYCHAIN="$KEYCHAIN"`, false)
	if unset < 0 || exportKey < 0 || unset > pack || exportKey > pack || writeKey > pack {
		t.Errorf("%s's signing step packs before it has written the key file, handed on its path and unset the certificate", consoleReleaseWorkflowPath)
	}
	at(`APP="release/mac-universal/Fugaro Console.app"`, false)

	// **The bundle is notarised, stapled and validated by the script, and the
	// .dmg and the .zip are then packed from the bundle that carries the
	// ticket.** The first pack builds the .app alone (mutation: `--dir`
	// dropped, so a target built from an unstapled bundle could reach the
	// release); the script's call for the bundle comes next (mutation: the call
	// removed, or replaced by the two `xcrun stapler` lines it exists to hold,
	// which the both-directions clause below catches); the second pack builds
	// the targets from the stapled bundle (mutation: `--prepackaged "$APP"`
	// dropped).
	//
	// **And the second pack is the variant that does not rebuild.** `npm run
	// package` is `npm run build && electron-builder …`; with `--prepackaged`
	// the bundle is already built, so the compile would only be spent again --
	// in the one step that holds the unlocked keychain and the exported `.p8`
	// path. `npm run package:targets` is the same packer without the build
	// (mutation: this line back to plain `npm run package --`, which the
	// exact-line lookup below misses -- the first pack's line is not it).
	bundlePack := at(`npm run package -- --dir -c.mac.notarize=false -c.extraMetadata.version="$VERSION"`, false)
	appScript := at(`scripts/notarize-and-staple.sh app "$APP"`, false)
	targetsPack := at(`npm run package:targets -- --prepackaged "$APP" -c.mac.notarize=false -c.extraMetadata.version="$VERSION"`, false)
	if bundlePack < 0 || appScript < 0 || targetsPack < 0 || !(bundlePack < appScript && appScript < targetsPack) {
		t.Errorf("%s's signing step does not build the bundle, notarise and staple it through %s, and only then pack the .dmg and the .zip from it (%d, %d, %d)", consoleReleaseWorkflowPath, consoleNotarizeScriptPath, bundlePack, appScript, targetsPack)
	}
	// The signing step makes no `xcrun` call of its own: every staple and
	// validate is the script's, where the retry is, and the submission is the
	// script's too (which checks Apple's status before stapling).
	for _, line := range lines {
		if strings.Contains(line, "xcrun") {
			t.Errorf("%s's signing step runs %q itself: the notarisation, the staple and the validation are %s's, so that the retry is one implementation and the packer (which makes none of these calls) cannot staple where nothing can retry it", consoleReleaseWorkflowPath, line, consoleNotarizeScriptPath)
		}
	}
	if strings.Contains(text, "mac-*") || strings.Contains(text, "mac-arm64") {
		t.Errorf("%s globs or names a thin-arch output directory: the build is universal, release/mac-universal", consoleReleaseWorkflowPath)
	}
	named := at(`*"-$VERSION-"*"|"*"-$VERSION-"*) ;;`, false)
	if named < 0 || named+1 >= len(lines) || lines[named+1] != "*)" || named+3 >= len(lines) || lines[named+3] != "exit 1" {
		t.Errorf("%s's signing step does not refuse artifacts whose names do not carry the tag's version", consoleReleaseWorkflowPath)
	}
	// The disk image's own round: signed, then submitted, stapled and validated
	// by the same script, after the targets were packed (mutation: the script
	// call for the .dmg removed, leaving a .dmg that is signed but never
	// notarised).
	signDMG := at(`codesign --sign "$identity" --keychain "$KEYCHAIN" --timestamp "$DMG"`, false)
	dmgScript := at(`scripts/notarize-and-staple.sh dmg "$DMG"`, false)
	if !(targetsPack < signDMG && signDMG < dmgScript) {
		t.Errorf("%s's signing step does not pack the .dmg, sign it, and then notarise and staple it through %s, in that order (%d, %d, %d)", consoleReleaseWorkflowPath, consoleNotarizeScriptPath, targetsPack, signDMG, dmgScript)
	}

	// **The record is about the shipped bytes**, and record() exits 1 on a
	// failure; the record, the sums and both artifacts leave the job as one
	// workflow artifact.
	verifyIdx, verify := stepNamed(consoleReleaseVerifyStep)
	vlines := consoleRunLines(verify.Run)
	if verify.Env["DMG"] != "${{ steps.signed.outputs.dmg }}" || verify.Env["ZIP"] != "${{ steps.signed.outputs.zip }}" {
		t.Errorf("%s's verification step does not read the signed .dmg and .zip: %v", consoleReleaseWorkflowPath, verify.Env)
	}
	fn := consoleLineIndex(vlines, "record() {", false)
	if fn < 0 || fn+6 >= len(vlines) || vlines[fn+2] != `if ! "$@" >> "$RECORD" 2>&1; then` || vlines[fn+5] != "exit 1" || vlines[fn+6] != "fi" {
		t.Errorf("%s's record() does not exit 1 when a recorded command fails", consoleReleaseWorkflowPath)
	}
	for _, want := range []string{
		`(cd "$(dirname "$DMG")" && shasum -a 256 "$(basename "$DMG")" "$(basename "$ZIP")") > "$SUMS"`,
		`record cat "$SUMS"`,
		`record hdiutil attach "$DMG" -nobrowse -readonly -mountpoint "$MOUNT"`,
		`record codesign --verify --deep --strict --verbose=2 "$IN_DMG"`,
		`record codesign -d --entitlements - "$IN_DMG"`,
		`record spctl --assess --type execute --verbose=4 "$IN_DMG"`,
		`record retry_apple_network xcrun stapler validate "$IN_DMG"`,
		`record ditto -x -k "$ZIP" "$UNZIP"`,
		`record codesign --verify --deep --strict --verbose=2 "$IN_ZIP"`,
		`record spctl --assess --type execute --verbose=4 "$IN_ZIP"`,
		`record retry_apple_network xcrun stapler validate "$IN_ZIP"`,
		`record codesign --verify --strict --verbose=2 "$DMG"`,
		`record retry_apple_network xcrun stapler validate "$DMG"`,
		`record spctl --assess --type open --context context:primary-signature -v "$DMG"`,
		`cp "$DMG" "$ZIP" "$RECORD" "$SUMS" "$ASSETS/"`,
	} {
		if !slices.Contains(vlines, want) {
			t.Errorf("%s's verification step does not run %q", consoleReleaseWorkflowPath, want)
		}
	}
	if verifyIdx+1 >= len(job.Steps) || !strings.Contains(job.Steps[verifyIdx+1].Uses, "actions/upload-artifact@") ||
		job.Steps[verifyIdx+1].With["name"] != "console-release-assets" || job.Steps[verifyIdx+1].With["if-no-files-found"] != "error" {
		t.Errorf("%s does not upload the verified assets as the console-release-assets artifact right after verifying them", consoleReleaseWorkflowPath)
	}

	// **Every `xcrun stapler` call in the pipeline is retried, bounded -- and
	// no other call is.** `xcrun stapler staple` and `xcrun stapler validate`
	// reach Apple's ticket-delivery service over the network, and the October
	// 2026 release run (39163351646) failed there with `NSURLErrorDomain
	// Code=-1005 "The network connection was lost"` -- a lost connection, not a
	// rejected notarisation, in two different processes: its fourth attempt in
	// the packer's own staple inside `npm run package`, its fifth in a
	// `stapler validate` on the bundle here.
	// The first is why the packer's notarisation is off in this file and the
	// script does the three calls; this clause and the script's own make sure
	// the two that stay (the staples and validations here and in the
	// verification step) are the only ones, and that both are retried. One
	// helper, defined once in the script and character for character once in
	// the verification step, retries them. **The other Apple-network calls are
	// deliberately left alone**, and the clauses below hold that too: `xcrun
	// notarytool submit --wait` is the submission (a rejection is final, its
	// exit status and its "Accepted" status are both checked in the script, and
	// it is never wrapped).
	//
	// The mutations this is proved against: removing the helper or its
	// definition from either place (the definition clause), wrapping
	// `codesign`, the `notarytool submit` or a check in it (the "nothing else"
	// clause, which reads the script's lines as well as the steps'), adding a
	// fresh un-retried `xcrun stapler validate` anywhere in the pipeline (the
	// both-directions clause, which an allow-list alone does not catch),
	// turning the packer's notarisation back on in any `npm run package` (the
	// pack clause above), and lowering the attempt bound or the pauses (the
	// bound clause, and TestDocsConsoleReleaseRetryHelperIsBounded, which runs
	// the helper).
	script := readDoc(t, consoleNotarizeScriptPath)
	scriptLines := consoleRunLines(script)
	scriptHelper := consoleRetryHelper(t, consoleNotarizeScriptPath, script)
	verifyHelper := consoleRetryHelper(t, "the verification step", verify.Run)
	// The script's definition sits at column 0 and the step's inside `run: |`,
	// so "character for character" is over the definitions' own lines, trimmed:
	// the body, the bound and the pauses are what must not drift.
	unindent := func(helper string) string {
		var out []string
		for _, line := range strings.Split(helper, "\n") {
			out = append(out, strings.TrimSpace(line))
		}
		return strings.Join(out, "\n")
	}
	if unindent(scriptHelper) != unindent(verifyHelper) {
		t.Errorf("%s defines %s differently from the verification step: the two copies are one helper, character for character, or the bounded retry drifts apart", consoleNotarizeScriptPath, consoleRetryHelperName)
	}
	retryCalls := map[string][]string{
		consoleNotarizeScriptPath: {
			`if ! retry_apple_network xcrun stapler staple "$artifact"; then`,
			`if ! retry_apple_network xcrun stapler validate "$artifact"; then`,
		},
		consoleReleaseVerifyStep: {
			`record retry_apple_network xcrun stapler validate "$IN_DMG"`,
			`record retry_apple_network xcrun stapler validate "$IN_ZIP"`,
			`record retry_apple_network xcrun stapler validate "$DMG"`,
		},
	}
	// The two places that may hold the helper, read the same way: the script's
	// lines and every step's. The signing step may hold neither (its staple and
	// validation are the script's).
	sources := []struct {
		where string
		lines []string
		job   string
	}{{consoleNotarizeScriptPath, scriptLines, "release"}}
	for _, j := range jobs {
		for _, step := range j.job.Steps {
			sources = append(sources, struct {
				where string
				lines []string
				job   string
			}{step.Name, consoleRunLines(step.Run), j.name})
		}
	}
	for _, src := range sources {
		// Both directions. A line that names the helper is one of the calls
		// above, and -- the half an allow-list alone misses -- **every line
		// that calls `xcrun stapler` names it**: a fresh, un-retried
		// `xcrun stapler validate` added here is a call that goes without the
		// retry this change exists for, and would otherwise satisfy every
		// other clause.
		for _, line := range src.lines {
			if strings.Contains(line, "xcrun stapler") && !strings.Contains(line, consoleRetryHelperName) {
				t.Errorf("%s: %q calls `xcrun stapler` without the retry: every staple and every validation on the way to a release is retried, and one that is not is the lost-connection failure coming back", src.where, line)
			}
			if !strings.Contains(line, consoleRetryHelperName) {
				continue
			}
			if line == consoleRetryHelperName+"() {" {
				if _, ok := retryCalls[src.where]; !ok {
					t.Errorf("%s defines %s: only %s and the verification step (%q) may, and a helper anywhere else is a retry nobody bounded", src.where, consoleRetryHelperName, consoleNotarizeScriptPath, consoleReleaseVerifyStep)
				}
				continue
			}
			if !slices.Contains(retryCalls[src.where], line) {
				t.Errorf("%s retries %q: the helper wraps the `xcrun stapler` calls this pipeline makes (%s) and nothing else -- behind codesign, the notarytool submission and its status, the gate, the team check or the secret check it would retry something that has already decided", src.where, line, strings.Join(retryCalls[src.where], ", "))
			}
		}
		// The calls a step is required to make are required of the job whose
		// pipeline makes them. Both legs verify what ships and both verification
		// steps are named for it -- that name is not a lie in either file -- and
		// the Linux leg staples nothing: an unsigned AppImage has no ticket to
		// staple, so the calls below are the release job's.
		if src.job != "release" {
			continue
		}
		for _, want := range retryCalls[src.where] {
			if !slices.Contains(src.lines, want) {
				t.Errorf("%s does not run %q: every `xcrun stapler` call it makes is retried", src.where, want)
			}
		}
	}
	// The script's own refusals, so a mutation of any of them is red here: the
	// staple that never succeeds refuses with the message the signing step used
	// to print, and so does the validation after it.
	for _, c := range []struct {
		call string
	}{
		{`if ! retry_apple_network xcrun stapler staple "$artifact"; then`},
		{`if ! retry_apple_network xcrun stapler validate "$artifact"; then`},
	} {
		i := consoleLineIndex(scriptLines, c.call, false)
		if i < 0 || i+2 >= len(scriptLines) || !strings.HasPrefix(scriptLines[i+1], `echo "::error::`) || scriptLines[i+2] != "exit 1" {
			t.Errorf("%s's %s does not refuse a fifth failure after the message: an unstapled artifact must not reach the release", consoleNotarizeScriptPath, c.call)
		}
	}
	// The submission: once, waited for, read as JSON, and refused unless Apple
	// answered "Accepted" -- with no retry (mutation: the submit wrapped in the
	// helper, or the status check removed so any answer stapled).
	if !slices.ContainsFunc(scriptLines, func(line string) bool {
		return strings.Contains(line, "xcrun notarytool submit") && strings.Contains(line, "--wait") &&
			strings.Contains(line, "--output-format json") && !strings.Contains(line, consoleRetryHelperName)
	}) {
		t.Errorf("%s does not submit the artifact once with `xcrun notarytool submit ... --wait --output-format json` outside the retry", consoleNotarizeScriptPath)
	}
	if !strings.Contains(script, `"Accepted"`) || !strings.Contains(script, "not 'Accepted'") {
		t.Errorf("%s does not refuse a notary answer that is not `Accepted`: a rejection is final and must not be stapled over", consoleNotarizeScriptPath)
	}
	// The submission's result is **the pair**: notarytool's own exit status AND
	// the status in Apple's answer. Neither alone is the whole answer -- a
	// non-zero exit can still leave a printed body behind, and a zero exit can
	// print a status that is not "Accepted" -- so the refusal reads both
	// (mutation: the exit-status half dropped, which
	// TestDocsConsoleNotarizeAndStapleScript's "Accepted body over a non-zero
	// exit" case catches as well, or the condition replaced by `if false;
	// then`).
	refuseStatus := consoleLineIndex(scriptLines, `if [ "$submit_status" -ne 0 ] || [ "$status" != "Accepted" ]; then`, false)
	if refuseStatus < 0 || refuseStatus+2 >= len(scriptLines) || !strings.HasPrefix(scriptLines[refuseStatus+1], `echo "::error::`) || scriptLines[refuseStatus+2] != "exit 1" {
		t.Errorf("%s does not exit 1 unless notarytool exited 0 and the status is `Accepted`: an Accepted-looking body over a non-zero exit is not an accepted submission, and a status that is not `Accepted` is a rejection -- either one refuses at once, with no staple", consoleNotarizeScriptPath)
	}
	// A bundle is submitted zipped by `ditto` (with --keepParent, so the .app
	// survives the round trip); a disk image goes as itself.
	// The no-rebuild variant exists in the manifest, and the script it is a
	// variant *of* still builds: `package:targets` is the pinned local packer
	// alone (mutation: a `npm run build` added to it, which would undo the
	// point of the second pack), and Task 4.1's `package` keeps its build
	// (mutation: the build removed from it, which would leave the owner's own
	// command and this workflow's first pack with nothing to pack).
	pkgBytes, err := os.ReadFile(filepath.Join("..", "..", "console", "package.json"))
	if err != nil {
		t.Fatalf("console/package.json is unreadable: %v", err)
	}
	var manifest struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(pkgBytes, &manifest); err != nil {
		t.Fatalf("console/package.json is not JSON: %v", err)
	}
	if got := manifest.Scripts["package:targets"]; got != "electron-builder --mac --universal --publish never" {
		t.Errorf("console/package.json's `package:targets` is %q, want the pinned local packer alone with no `npm run build`: the pack that runs with --prepackaged packs a bundle that is already built, in the step that holds the unlocked keychain and the exported .p8 path", got)
	}
	if !strings.Contains(manifest.Scripts["package"], "npm run build") {
		t.Errorf("console/package.json's `package` is %q: Task 4.1's command -- and this workflow's first pack -- builds before it packs", manifest.Scripts["package"])
	}
	if !slices.Contains(scriptLines, `ditto -c -k --keepParent "$artifact" "$submission"`) {
		t.Errorf("%s does not zip the bundle with `ditto -c -k --keepParent` before submitting it", consoleNotarizeScriptPath)
	}
	if !strings.Contains(script, `submission="$artifact"`) {
		t.Errorf("%s does not submit a disk image as itself: notarytool takes a .dmg directly", consoleNotarizeScriptPath)
	}
	// The workflow invokes the script by the path a step running in console/
	// resolves -- and the file is executable, or the runner cannot start it.
	info, err := os.Stat(filepath.Join("..", "..", consoleNotarizeScriptPath))
	if err != nil {
		t.Fatalf("%s is missing: the signing step calls it and the retry lives in it (%v)", consoleNotarizeScriptPath, err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("%s is not executable (mode %v): the signing step runs it directly, so the runner would report a permission error instead of the work", consoleNotarizeScriptPath, info.Mode().Perm())
	}
	// The helper is bounded: five attempts, a growing pause between them, and
	// each attempt's number and exit code printed.
	for _, c := range []struct{ where, helper string }{
		{consoleNotarizeScriptPath, scriptHelper},
		{"verification", verifyHelper},
	} {
		for _, want := range []string{
			`while [ "$attempt" -le 5 ]; do`,
			`echo "apple-network attempt $attempt of 5: $* exited $status"`,
			`if [ "$attempt" -eq 5 ]; then`,
			`return "$status"`,
			`1) sleep 15 ;;`,
			`2) sleep 30 ;;`,
			`3) sleep 60 ;;`,
			`4) sleep 90 ;;`,
		} {
			if !strings.Contains(c.helper, want) {
				t.Errorf("%s's %s step's %s lacks %q: five attempts, a growing pause, and each attempt's number and exit code printed", consoleReleaseWorkflowPath, c.where, consoleRetryHelperName, want)
			}
		}
	}

	// **The publish job**: downloads what the build jobs verified, re-checks
	// every asset against the sums, then publishes `console-vX.Y.Z` and never
	// the CLI's release. The Linux change inserts a second download, a second
	// sums check, the merge and the signature between the macOS artifact's
	// download and the publish, so what is asserted here is what holds in both
	// states: the job opens on the macOS artifact's download, it re-checks
	// `console-sha256sums.txt` before it publishes, and it ends on the publish
	// step.
	if len(pub.Steps) < 3 || !strings.Contains(pub.Steps[0].Uses, "actions/download-artifact@") || pub.Steps[0].With["name"] != "console-release-assets" {
		t.Fatalf("%s's publish job does not open on the download of the console-release-assets artifact", consoleReleaseWorkflowPath)
	}
	if last := pub.Steps[len(pub.Steps)-1]; last.Name != consoleReleasePublishStep {
		t.Fatalf("%s's publish job ends on %q, want the publish step %q: the draft-then-publish sequence is the last thing the job does", consoleReleaseWorkflowPath, last.Name, consoleReleasePublishStep)
	}
	rechecks := false
	for _, step := range pub.Steps[:len(pub.Steps)-1] {
		if slices.Contains(consoleRunLines(step.Run), "sha256sum --strict --check console-sha256sums.txt") {
			rechecks = true
		}
	}
	if !rechecks {
		t.Errorf("%s's publish job does not re-check the assets against console-sha256sums.txt before publishing", consoleReleaseWorkflowPath)
	}
	if linuxApplied {
		linuxDownloads := 0
		for _, step := range pub.Steps {
			if strings.Contains(step.Uses, "actions/download-artifact@") && step.With["name"] == "console-linux-assets" {
				linuxDownloads++
			}
		}
		if linuxDownloads != 1 {
			t.Errorf("%s's publish job downloads the console-linux-assets artifact %d times, want exactly 1: publishing before the Linux leg has arrived would ship a release with a leg missing", consoleReleaseWorkflowPath, linuxDownloads)
		}
	}
	plines := consoleRunLines(publish.Run)
	for _, want := range []string{
		`CONSOLE_TAG="console-v$VERSION"`,
		`VERSION="${TAG#console-v}"`,
		`gh release create "$CONSOLE_TAG" --repo "$GITHUB_REPOSITORY" --draft --latest=false \`,
		// The release's body is the console's OWN notes for this version -- the
		// file the release job refused to build without -- read from the tagged
		// tree, because `publish` has no checkout. A release body built from a
		// string in this file is what the console had while it rode the CLI's
		// tag, and the notes are the console's own end-user summary.
		`--notes-file "$BODY" \`,
		`gh release edit "$CONSOLE_TAG" --repo "$GITHUB_REPOSITORY" --draft=false --latest=false`,
	} {
		if !slices.Contains(plines, want) {
			t.Errorf("%s's publish step does not run %q", consoleReleaseWorkflowPath, want)
		}
	}
	// The tag is the owner's, not this job's: the `console-vX.Y.Z` push is what
	// started this run, so the job never creates, moves or reuses it, and no
	// `--target` (the CLI era's way of making the tag itself at $GITHUB_SHA)
	// belongs here. What the job does is resolve the tag to a commit and refuse
	// when that commit is not the one this run built -- publishing under a tag
	// that has moved would put artifacts from one commit under a tag naming
	// another. Reading it through `commits/` rather than `git/ref/tags/`, which
	// returns the tag *object* for an annotated tag.
	if !strings.Contains(publish.Run, `gh api "repos/$GITHUB_REPOSITORY/commits/$CONSOLE_TAG" --jq .sha`) ||
		!strings.Contains(publish.Run, `if [ "$tag_sha" != "$GITHUB_SHA" ]`) {
		t.Errorf("%s's publish step does not resolve the tag it publishes under to this run's commit: %v", consoleReleaseWorkflowPath, plines)
	}
	if strings.Contains(publish.Run, `--target "$GITHUB_SHA"`) {
		t.Errorf("%s's publish step passes --target to `gh release create`: the tag already exists (it is this run's own tag), so the job creates nothing -- the flag is a leftover from when this workflow ran on the CLI's tag", consoleReleaseWorkflowPath)
	}
	// The notes are the console's own file, and they are read at the tagged
	// commit: read at whatever the default branch holds now, a release could
	// carry notes that disagree with the artifacts beside them.
	if !strings.Contains(publish.Run, `NOTES_PATH="docs/releases/console/v$VERSION.md"`) ||
		!strings.Contains(publish.Run, `?ref=$GITHUB_SHA"`) ||
		!strings.Contains(publish.Run, `cat "$NOTES"`) {
		t.Errorf("%s's publish step does not read the console's notes from docs/releases/console/v$VERSION.md at the tagged commit: %v", consoleReleaseWorkflowPath, plines)
	}
	if strings.Contains(publish.Run, `--notes "`) {
		t.Errorf("%s's publish step still passes an inline --notes body: the console's body is its own notes file", consoleReleaseWorkflowPath)
	}
	// The assets it attaches: the macOS pair, the record and the sums in both
	// states, and -- once the Linux change is applied -- the AppImage, the .deb,
	// their own record and the Sigstore bundle over the merged sums file.
	assets := -1
	for i, line := range plines {
		if strings.HasPrefix(line, `"$DMG" "$APP_ZIP"`) {
			assets = i
		}
	}
	if assets < 0 {
		t.Fatalf("%s's publish step does not attach the macOS artifacts (%v)", consoleReleaseWorkflowPath, plines)
	}
	for _, want := range []string{`"$DMG" "$APP_ZIP"`, `"$RECORD"`, `"$SUMS"`} {
		if !strings.Contains(plines[assets], want) {
			t.Errorf("%s's publish step does not attach %s: every artifact and the sums file that covers it are the release", consoleReleaseWorkflowPath, want)
		}
	}
	for _, want := range []string{`"$APPIMAGE"`, `"$DEB"`, `"$LINUX_RECORD"`, `"$BUNDLE"`} {
		if got := strings.Contains(plines[assets], want); got != linuxApplied {
			t.Errorf("%s's publish step attaches %s = %v, want %v: the Linux artifacts, their record and the signature over the sums file arrive with the Linux change and with nothing else", consoleReleaseWorkflowPath, want, got, linuxApplied)
		}
	}
	for _, line := range plines {
		if strings.Contains(line, "gh release upload") || strings.Contains(line, "--clobber") {
			t.Errorf("%s's publish step runs %q: the console never adds to an existing release and never clobbers", consoleReleaseWorkflowPath, line)
		}
		if regexp.MustCompile(`gh release \w+ "\$TAG"`).MatchString(line) {
			t.Errorf("%s's publish step touches the CLI's release (%q)", consoleReleaseWorkflowPath, line)
		}
	}
	// The never-clobber shape: in the `case` over the release list, the only
	// branch that lets the job go on is the "absent" one (""), and the default
	// branch `*)` errors and exits 1. The behaviour is pinned again, against a
	// faked gh, in TestDocsConsoleReleasePublishRefusesToClobber.
	for _, c := range []struct{ subject, pass string }{
		{`case "$existing" in`, `"") ;;`},
	} {
		i := consoleLineIndex(plines, c.subject, false)
		if i < 0 || i+1 >= len(plines) || plines[i+1] != c.pass {
			t.Errorf("%s's publish step: %q does not open with the one passing branch %q", consoleReleaseWorkflowPath, c.subject, c.pass)
			continue
		}
		end := consoleLineIndex(plines[i:], "esac", false)
		def := consoleLineIndex(plines[i:], "*)", false)
		if end < 0 || def < 0 || def > end || !strings.HasPrefix(plines[i+def+1], `echo "::error::`) || plines[i+def+2] != "exit 1" {
			t.Errorf("%s's publish step: %q's default branch does not refuse", consoleReleaseWorkflowPath, c.subject)
		}
	}
	// And the tag probe is not the CLI era's: `git/ref/tags/` (which reads the
	// tag as "does it exist", the question the job asked when *it* created the
	// tag) is replaced by "which commit does it name", because under a
	// tag-push trigger the tag exists on every run by construction.
	if i := consoleLineIndex(plines, `tag_sha="$(gh api "repos/$GITHUB_REPOSITORY/commits/$CONSOLE_TAG" --jq .sha)"`, true); i < 0 || i+2 >= len(plines) || !strings.HasPrefix(plines[i+1], `echo "::error::`) || plines[i+2] != "exit 1" {
		t.Errorf("%s's publish step does not resolve the console tag to a commit and stop when the lookup fails", consoleReleaseWorkflowPath)
	}

	// The Node version is console/package.json's engines.node, and no cache:
	// a restored cache is written by other runs.
	nodeSteps := 0
	for _, step := range job.Steps {
		if !strings.Contains(step.Uses, "actions/setup-node@") {
			continue
		}
		nodeSteps++
		if step.With["node-version-file"] != "console/package.json" {
			t.Errorf("%s's setup-node takes node-version-file %q, want console/package.json", consoleReleaseWorkflowPath, step.With["node-version-file"])
		}
		for _, key := range []string{"node-version", "cache", "cache-dependency-path"} {
			if _, ok := step.With[key]; ok {
				t.Errorf("%s's setup-node sets %q: the version is engines.node, and the signing job restores no cache", consoleReleaseWorkflowPath, key)
			}
		}
	}
	if nodeSteps != 1 {
		t.Errorf("%s has %d setup-node steps, want 1", consoleReleaseWorkflowPath, nodeSteps)
	}

	// The checkout: full history, no credentials kept.
	if !strings.Contains(job.Steps[0].Uses, "actions/checkout@") {
		t.Errorf("%s's first step is %q, want actions/checkout", consoleReleaseWorkflowPath, job.Steps[0].Uses)
	} else {
		if job.Steps[0].With["fetch-depth"] != "0" {
			t.Errorf("%s's checkout has fetch-depth %q, want 0", consoleReleaseWorkflowPath, job.Steps[0].With["fetch-depth"])
		}
		if job.Steps[0].With["persist-credentials"] != "false" {
			t.Errorf("%s's checkout persists credentials", consoleReleaseWorkflowPath)
		}
	}

	// And while both files exist, they are the same file.
	//
	// This clause is the one that goes red when a staged change has not been
	// copied in yet, and its failure is not a nuisance to be silenced: CI's
	// `test` job is red on every push to main while it holds, and
	// scripts/release-gate.sh requires a check run named `test`, so every tag
	// is refused -- the CLI's releases included -- until the owner copies the
	// staged file in. Stop the merge, do not delete the assertion.
	//
	// What it compares is the two copies of console-release.yml, never the
	// text these tests read: while the console-vX.Y.Z change is staged, the
	// effective text is the supersede file, which is *meant* to differ from the
	// file it supersedes -- comparing that would make this clause red for the
	// one change it is not about. The supersede file is copied over both copies,
	// so the clause is untouched by it and still holds the line it always held.
	if _, statErr := os.Stat(filepath.Join("..", "..", appliedConsoleReleasePath)); statErr == nil {
		applied, err := os.ReadFile(filepath.Join("..", "..", appliedConsoleReleasePath))
		if err != nil {
			t.Fatal(err)
		}
		stagedCopy, err := os.ReadFile(filepath.Join("..", "..", consoleReleaseWorkflowPath))
		if err != nil {
			t.Fatal(err)
		}
		if string(applied) != string(stagedCopy) {
			t.Errorf("%s differs from the staged %s: the staged file has a change the owner has not copied in yet. Do not merge before running `cp %s %s` and committing it -- until then main's `test` job is red and scripts/release-gate.sh refuses every tag, the CLI's included (docs/pending-workflows/README.md, %q)",
				appliedConsoleReleasePath, consoleReleaseWorkflowPath, consoleReleaseWorkflowPath, appliedConsoleReleasePath, "## `console-release.yml`")
		}
	} else if !os.IsNotExist(statErr) {
		t.Fatal(statErr)
	}
}

// TestDocsConsoleReleaseStagedChangeSaysItNeedsACopyIn is the human half of the
// byte-identity clause above. While the staged file differs from the applied
// one, `docs/pending-workflows/README.md`'s `console-release.yml` section must
// say so in the owner's own terms: that this is the state and not drift, that
// the applied copy is the older text, and the one command that fixes it. The
// failure of the byte-identity clause is a red test; this one is why a reader
// of the staging directory -- the person who has to act -- is told, without
// having to run the tests.
//
// The mutation that proves it: deleting the "Re-copy required" paragraph from
// that section while the two files differ makes this red. Once the owner has
// copied the file in, the two are identical and this test asserts nothing, as
// it should.
func TestDocsConsoleReleaseStagedChangeSaysItNeedsACopyIn(t *testing.T) {
	staged, err := os.ReadFile(filepath.Join("..", "..", consoleReleaseWorkflowPath))
	if err != nil {
		t.Fatal(err)
	}
	applied, err := os.ReadFile(filepath.Join("..", "..", appliedConsoleReleasePath))
	if os.IsNotExist(err) {
		t.Skipf("%s does not exist yet: there is nothing to copy in, and nothing for the README to say", appliedConsoleReleasePath)
	}
	if err != nil {
		t.Fatal(err)
	}
	if string(staged) == string(applied) {
		return
	}
	section := consoleReadmeSection(t, readDoc(t, "docs/pending-workflows/README.md"), "## `console-release.yml`")
	for _, want := range []string{
		// That the staged file has a change the applied copy does not.
		"Re-copy required",
		// That it is the applied copy which is behind, not the staged one.
		"the applied copy is the older text",
		// Why the reader is looking at a red test rather than at a silently
		// drifted file, and where that test is.
		"TestDocsConsoleReleaseWorkflowIsStagedAndPinned",
		// And the one command that fixes it.
		"cp docs/pending-workflows/console-release.yml .github/workflows/console-release.yml",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("docs/pending-workflows/README.md's `console-release.yml` section lacks %q, and the staged file differs from the applied %s: the owner is the one who has to copy it in, and this is where they read it", want, appliedConsoleReleasePath)
		}
	}
}

// consoleRunScript runs one step's `run:` script under bash in dir, with a
// fake `gh` first on PATH (a shell script that answers from FAKE_* variables
// and logs its calls to $FAKE_LOG), and returns whether it succeeded and the
// calls the fake saw.
func consoleRunScript(t *testing.T, script string, env []string) (bool, string, string) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not on PATH")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := `#!/usr/bin/env bash
echo "gh $*" >> "$FAKE_LOG"
case "$*" in
  *"/releases --jq"*) printf '%s' "$FAKE_RELEASES"; exit "${FAKE_RELEASES_EXIT:-0}" ;;
  # The tag this run was triggered by, resolved to a commit (the step asks for
  # the sha field). The job does not create that tag -- the owner pushes it, and that
  # push is what started this run -- so the only question here is whether it
  # still names the commit this run built. FAKE_TAG=at-sha answers with
  # $GITHUB_SHA (the tag has not moved), FAKE_TAG=moved with another commit, and
  # anything else is the lookup failing: which is NOT "no such tag", because the
  # tag exists by construction, and it must stop the job too.
  *"/commits/"*)
    case "$FAKE_TAG" in
      at-sha) printf '%s' "$GITHUB_SHA"; exit 0 ;;
      moved) printf '%s' "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"; exit 0 ;;
      *) echo "gh: Server Error (HTTP 502)" >&2; exit 1 ;;
    esac ;;
  # The console's own notes, read from the tagged tree: FAKE_NOTES_EXIT=1 is a
  # 404 (the file is not on that commit), FAKE_NOTES_B64 is the base64 content
  # the step pipes through base64 -d, and both spellings of "the read failed"
  # are refused by the publish step rather than published as an empty body.
  *"/contents/"*)
    if [ "${FAKE_NOTES_EXIT:-0}" != 0 ]; then echo "gh: Not Found (HTTP 404)" >&2; exit "${FAKE_NOTES_EXIT}"; fi
    printf '%s' "${FAKE_NOTES_B64-eA==}"; exit 0 ;;
  "release create"*|"release edit"*) exit 0 ;;
esac
echo "fake gh: unexpected call: $*" >&2
exit 3
`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	// RUNNER_TEMP/console-release, which is both the step's working directory
	// and where it writes the console's notes and the release body it composes
	// them into (the workflow sets `working-directory: ${{ runner.temp }}/console-release`).
	work := filepath.Join(dir, "console-release")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	// Every file the publish step requires, from both legs: the merged step
	// (the state this file is in once the owner has copied the Linux change in)
	// reads all of them, and the macOS-only step attaches the ones it names and
	// never looks at the rest, so one fixture runs the same step in every state
	// -- alone, with the Linux legs, and with the update feed.
	for _, name := range []string{
		"Fugaro Console-1.2.3-universal.dmg",
		"Fugaro Console-1.2.3-universal-mac.zip",
		"latest-mac.yml",
		"console-verification.txt",
		"console-sha256sums.txt",
		"Fugaro.Console-1.2.3-linux-x64.AppImage",
		"Fugaro.Console-1.2.3-linux-x64.deb",
		"console-linux-verification.txt",
		"console-sha256sums.txt.sigstore.json",
	} {
		if err := os.WriteFile(filepath.Join(work, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	logPath := filepath.Join(dir, "gh.log")
	out := filepath.Join(dir, "github_output")
	cmd := exec.Command(bash, "-e", "-c", script)
	cmd.Dir = work
	cmd.Env = append([]string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"FAKE_LOG=" + logPath,
		"GITHUB_OUTPUT=" + out,
		"GITHUB_REPOSITORY=dimipaun/fugaro",
		"GITHUB_SHA=0123456789abcdef0123456789abcdef01234567",
		"RUNNER_TEMP=" + dir,
	}, env...)
	runErr := cmd.Run()
	calls, _ := os.ReadFile(logPath)
	output, _ := os.ReadFile(out)
	return runErr == nil, string(calls), string(output)
}

// TestDocsConsoleReleasePublishRefusesToClobber runs the staged publish step
// against a faked gh, in the world the new trigger actually produces: the
// console tag exists and is the tag whose push started the run ($GITHUB_REF_NAME
// / $TAG), so the tag lookup answers "present at the commit this run built".
//
// The `publishes` case is that world, and it is what keeps the step honest: an
// earlier version of this step probed the tag with `git/ref/tags/` and refused
// when it existed, which under a tag-push trigger means refusing *every* console
// release after the full build -- a suite that only modelled "tag absent:
// publishes" stayed green over it. So the tag-lookup cases are the two states
// that can really happen (present at this commit, or moved off it), plus a
// lookup that fails, which is not "no such tag" and must stop the job as well.
//
// A published release, a leftover draft and a failed release listing all stop it
// before `gh release create`.
func TestDocsConsoleReleasePublishRefusesToClobber(t *testing.T) {
	_, wf, _, _ := readConsoleReleaseWorkflow(t)
	_, step := consoleStepNamed(t, wf.Jobs["publish"], "publish", consoleReleasePublishStep)
	for _, c := range []struct {
		name        string
		releases    string
		releasesErr string
		tag         string
		notesErr    string
		notes       string
		publishes   bool
	}{
		{"tag present at the tagged commit: publishes", "", "0", "at-sha", "0", "ok", true},
		{"published release", "published", "0", "at-sha", "0", "ok", false},
		{"draft left by an earlier attempt", "draft", "0", "at-sha", "0", "ok", false},
		// The tag is the owner's and cannot be absent here, but it can have been
		// moved (a force-push, a re-tag) after this run started: publishing then
		// would put assets built from one commit under a tag naming another.
		{"the tag has moved off this run's commit", "", "0", "moved", "0", "ok", false},
		{"tag lookup fails", "", "0", "502", "0", "ok", false},
		{"release listing fails", "", "1", "at-sha", "0", "ok", false},
		// The console's notes are the release body: a version whose notes file
		// is not on the tagged commit, or is there and empty, is not a release
		// this job may publish -- an empty body is what the console had while it
		// rode the CLI's tag, and the file is what replaced it.
		{"notes missing on the tagged commit", "", "0", "at-sha", "1", "ok", false},
		{"notes empty on the tagged commit", "", "0", "at-sha", "0", "empty", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			notesB64 := "eA=="
			if c.notes == "empty" {
				notesB64 = ""
			}
			ok, calls, _ := consoleRunScript(t, step.Run, []string{
				"TAG=console-v1.2.3", "GH_TOKEN=fake",
				"FAKE_RELEASES=" + c.releases, "FAKE_RELEASES_EXIT=" + c.releasesErr, "FAKE_TAG=" + c.tag,
				"FAKE_NOTES_EXIT=" + c.notesErr, "FAKE_NOTES_B64=" + notesB64,
			})
			created := strings.Contains(calls, "gh release create console-v1.2.3")
			published := strings.Contains(calls, "gh release edit console-v1.2.3 --repo dimipaun/fugaro --draft=false --latest=false")
			if c.publishes && (!ok || !created || !published) {
				t.Errorf("the publish step did not create and publish console-v1.2.3 (ok=%v); gh saw:\n%s", ok, calls)
			}
			if c.publishes && !strings.Contains(calls, "gh api repos/dimipaun/fugaro/commits/console-v1.2.3") {
				t.Errorf("the publish step published without resolving the tag it is publishing under to a commit; gh saw:\n%s", calls)
			}
			if !c.publishes && (ok || created || published) {
				t.Errorf("the publish step went on (ok=%v, created=%v) when it must refuse; gh saw:\n%s", ok, created, calls)
			}
			if strings.Contains(calls, "gh release create v1.2.3") || strings.Contains(calls, "upload") {
				t.Errorf("the publish step touched the CLI's release; gh saw:\n%s", calls)
			}
		})
	}
}

// TestDocsConsoleReleaseTagStepStampsOnlyStrictVersions runs the staged tag
// step: a strict console-vX.Y.Z yields version=X.Y.Z (the value every pack is
// stamped with); anything else fails before a pack can run.
//
// The CLI's own `vX.Y.Z` tag is in the refusal list on purpose, and it is the
// point of the change: `v1.2.3` used to start this workflow, and it releases
// the CLI (release.yml) and nothing else now -- three release streams, each with
// its own tag (owner addendum 2026-10-22). The mutation that proves it:
// `^console-v` dropped from the pattern makes the two CLI-shaped cases publish a
// console for the CLI's release.
func TestDocsConsoleReleaseTagStepStampsOnlyStrictVersions(t *testing.T) {
	_, wf, _, _ := readConsoleReleaseWorkflow(t)
	_, step := consoleStepNamed(t, wf.Jobs["release"], "release", consoleReleaseTagStep)
	for _, c := range []struct {
		tag, version string
	}{
		{"console-v1.2.3", "1.2.3"},
		{"console-v0.10.0", "0.10.0"},
		{"console-v0.11.0", "0.11.0"},
		{"v1.2.3", ""},
		{"v0.11.0", ""},
		{"console-v1.2", ""},
		{"console-v1.2.3-rc1", ""},
		{"console-v01.2.3", ""},
		{"console-1.2.3", ""},
		{"1.2.3", ""},
		{"console-v", ""},
		{"console-v1.2.3\nversion=9.9.9", ""},
	} {
		ok, _, out := consoleRunScript(t, step.Run, []string{"TAG=" + c.tag})
		if c.version == "" {
			if ok || out != "" {
				t.Errorf("tag %q: the tag step passed (output %q); it must refuse anything but a strict console-vX.Y.Z", c.tag, out)
			}
			continue
		}
		if !ok || out != "version="+c.version+"\n" {
			t.Errorf("tag %q: ok=%v output %q, want version=%s", c.tag, ok, out, c.version)
		}
	}
}

// TestDocsConsoleReleaseVersionStepRefusesATagItsOwnVersionLineDisagreesWith
// runs the staged version step, in both jobs that carry it, against a
// console/package.json written for the occasion: the tag's X.Y.Z has to be the
// file's own `version` (the same call release.yml makes for the plugin's
// version, one file over).
//
// The mutation that proves it: dropping the comparison (`[ "$declared" !=
// "$VERSION" ]`) publishes a console whose own version line, its update feed
// and its file names disagree with the tag printed on the release -- and,
// because Task 4.4's updater refuses a downgrade, a console stamped *below*
// what console/package.json says would refuse every update it was offered
// afterwards (docs/release.md, "Console releases").
func TestDocsConsoleReleaseVersionStepRefusesATagItsOwnVersionLineDisagreesWith(t *testing.T) {
	_, wf, _, _ := readConsoleReleaseWorkflow(t)
	for _, job := range []string{"release", "linux"} {
		jobName := job
		t.Run(jobName, func(t *testing.T) {
			_, step := consoleStepNamed(t, wf.Jobs[jobName], jobName, consoleReleaseVersionStep)
			for _, c := range []struct {
				name, version, declared string
				ok                      bool
			}{
				{"the tag and the version line agree", "0.11.0", "0.11.0", true},
				{"another release", "1.2.3", "1.2.3", true},
				{"the version line is behind the tag", "0.11.0", "0.10.0", false},
				{"the version line is ahead of the tag", "0.10.0", "0.11.0", false},
				{"the version line is absent", "0.11.0", "", false},
				{"the version line is an empty string", "0.11.0", "null", false},
			} {
				t.Run(c.name, func(t *testing.T) {
					ok, _ := consoleRunVersionStep(t, step.Run, c.version, c.declared)
					if ok != c.ok {
						t.Errorf("console/package.json version %q and tag version %q: ok=%v, want %v", c.declared, c.version, ok, c.ok)
					}
				})
			}
		})
	}
}

// consoleRunVersionStep runs the version step's `run:` script under bash in a
// directory that holds one file, console/package.json, with declared as its
// version (or with no `version` field at all when declared is ""). It returns
// whether the step succeeded. The step reads the file with jq, the way
// release.yml reads the plugin's, so this skips -- rather than passing -- on a
// machine without jq.
func consoleRunVersionStep(t *testing.T, script, version, declared string) (bool, string) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not on PATH")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is not on PATH")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "console"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"fugaro-console","version":"` + declared + `"}`
	if declared == "" {
		manifest = `{"name":"fugaro-console"}`
	} else if declared == "null" {
		manifest = `{"name":"fugaro-console","version":null}`
	}
	if err := os.WriteFile(filepath.Join(dir, "console", "package.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "github_output")
	cmd := exec.Command(bash, "-e", "-c", script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GITHUB_OUTPUT="+out, "VERSION="+version)
	runErr := cmd.Run()
	output, _ := os.ReadFile(out)
	return runErr == nil, string(output)
}

// TestDocsConsoleReleaseRetryHelperIsBounded runs the Apple-network retry
// helper -- the text console/scripts/notarize-and-staple.sh carries, which the
// verification step carries again -- under bash, with a fake `sleep` that
// records the pause instead of taking it and a fake command that fails a set
// number of times. The pins in TestDocsConsoleReleaseWorkflowIsStagedAndPinned
// read the helper's text; this runs it.
//
// What it fixes: a command that succeeds first time is run once, with no
// pause; a command that loses the connection twice is retried after 15 and 30
// seconds and the caller goes on; a command that never gets there is run five
// times with 15, 30, 60 and 90 seconds between them and **only then** returns
// its exit code, so a ticket that is genuinely absent still refuses to
// publish. The mutations that prove it: returning after the first failure
// (`return "$status"` moved above the attempt bound) makes the second and third
// cases red, dropping a `sleep` makes the pauses red, and printing no attempt
// number makes every case red.
func TestDocsConsoleReleaseRetryHelperIsBounded(t *testing.T) {
	helper := consoleRetryHelper(t, consoleNotarizeScriptPath, readDoc(t, consoleNotarizeScriptPath))

	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not on PATH")
	}

	for _, c := range []struct {
		name    string
		fails   int
		code    string
		want    []string // in order, each on its own line
		notWant []string
	}{
		{
			name:  "succeeds first time: one attempt, no pause",
			fails: 0,
			code:  "68",
			want: []string{
				"apple-network attempt 1 of 5: flaky exited 0",
				"helper ok",
			},
			notWant: []string{"attempt 2", "pause"},
		},
		{
			name:  "the connection is lost twice: retried, then the caller goes on",
			fails: 2,
			code:  "68",
			want: []string{
				"apple-network attempt 1 of 5: flaky exited 68",
				"pause 15",
				"apple-network attempt 2 of 5: flaky exited 68",
				"pause 30",
				"apple-network attempt 3 of 5: flaky exited 0",
				"helper ok",
			},
			notWant: []string{"attempt 4", "pause 60"},
		},
		{
			name:  "never gets there: five attempts, then the caller refuses",
			fails: 99,
			code:  "68",
			want: []string{
				"apple-network attempt 1 of 5: flaky exited 68",
				"pause 15",
				"apple-network attempt 2 of 5: flaky exited 68",
				"pause 30",
				"apple-network attempt 3 of 5: flaky exited 68",
				"pause 60",
				"apple-network attempt 4 of 5: flaky exited 68",
				"pause 90",
				"apple-network attempt 5 of 5: flaky exited 68",
				"helper failed 68",
			},
			notWant: []string{"attempt 6"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			// `sleep` is not a bash builtin, so the helper's pauses go through
			// this: it records what it was asked to wait for and returns at
			// once, which is what keeps this test from taking three minutes.
			sleep := "#!/usr/bin/env bash\necho \"pause $1\"\n"
			// A command that fails the first $FLAKY_FAILS times with
			// $FLAKY_CODE and then succeeds, so one test body covers "the
			// network came back" and "it never did".
			flaky := `#!/usr/bin/env bash
n=$(cat "$FLAKY_COUNTER" 2>/dev/null || echo 0)
n=$((n + 1))
echo "$n" > "$FLAKY_COUNTER"
if [ "$n" -le "$FLAKY_FAILS" ]; then
  exit "$FLAKY_CODE"
fi
exit 0
`
			for name, body := range map[string]string{"sleep": sleep, "flaky": flaky} {
				if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			script := "set -euo pipefail\n" + helper + `
if retry_apple_network flaky; then
  echo "helper ok"
else
  echo "helper failed $?"
fi
`
			cmd := exec.Command(bash, "-e", "-c", script)
			cmd.Dir = dir
			cmd.Env = append([]string{
				"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
				"FLAKY_COUNTER=" + filepath.Join(dir, "counter"),
				"FLAKY_FAILS=" + strconv.Itoa(c.fails),
				"FLAKY_CODE=" + c.code,
			}, "HOME="+dir)
			out, runErr := cmd.CombinedOutput()
			if runErr != nil {
				t.Fatalf("the helper's script failed to run (%v); output:\n%s", runErr, out)
			}
			lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
			at := 0
			for _, want := range c.want {
				next := consoleLineIndex(lines[at:], want, true)
				if next < 0 {
					t.Errorf("the helper's output does not carry %q after %d line(s); output:\n%s", want, at, out)
					break
				}
				at += next + 1
			}
			for _, bad := range c.notWant {
				if strings.Contains(string(out), bad) {
					t.Errorf("the helper's output carries %q, which it must not; output:\n%s", bad, out)
				}
			}
		})
	}
}

// TestDocsConsoleNotarizeAndStapleScript runs console/scripts/notarize-and-
// staple.sh -- the signing step's whole notarise/staple/validate path, and the
// reason the packer's own notarisation is off -- under bash, with fake `xcrun`,
// `ditto` and `sleep` on PATH. The pins above read the script's text; this runs
// it, which is the only way to hold the three properties the change exists for:
//
//   - a `xcrun stapler` call that loses the connection recovers inside five
//     attempts (15/30/60/90 seconds apart) and the script goes on;
//   - a `xcrun stapler` call that never gets there is attempted five times and
//     **then** refused, non-zero, with the "carries no stapled ticket" message,
//     so a ticket that is genuinely absent still refuses to publish;
//   - a submission whose status is not `Accepted` -- a rejection -- is refused
//     at once, and so is an "Accepted" body over a non-zero `notarytool` exit:
//     either way no `stapler` call is made at all, no pause is taken, and the
//     script exits non-zero. **That is the "nothing else is retried" half**,
//     and the fake `xcrun` proves it by logging every call it is given.
//
// The mutations this was written against: wrapping the submission in the
// helper (case 5's "submit once" plus the "no pause" clause go red), accepting
// any status (case 5 passes and the refusal clause of the pinned test goes
// red), accepting the status and ignoring `notarytool`'s exit (case 6, which
// is an "Accepted" body printed over a non-zero exit, and the pair clause of
// the pinned test), returning after the first failure (case 2), and dropping
// the fifth attempt (case 3).
func TestDocsConsoleNotarizeAndStapleScript(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not on PATH")
	}

	for _, c := range []struct {
		name          string
		kind          string
		status        string
		submitExit    string
		stapleFails   int
		validateFails int
		noCredentials bool
		ok            bool
		want          []string // in order, each one line of the script's own output
		wantOnOneLine []string // each "a|b": one line carrying all of a and b
		wantCalls     []string // in order, each somewhere in the fake `xcrun`/`ditto` log
		notWant       []string
	}{
		{
			name:   "accepted, and both stapler calls succeed first time",
			kind:   "app",
			status: "Accepted",
			ok:     true,
			want: []string{
				"notarised:",
				"attempt 1 of 5: xcrun stapler staple",
				"attempt 1 of 5: xcrun stapler validate",
				"stapled and validated:",
			},
			wantOnOneLine: []string{
				"attempt 1 of 5: xcrun stapler staple|exited 0",
				"attempt 1 of 5: xcrun stapler validate|exited 0",
			},
			wantCalls: []string{"ditto -c -k --keepParent", "xcrun notarytool submit", "xcrun stapler staple", "xcrun stapler validate"},
			notWant:   []string{"pause", "attempt 2", "::error::"},
		},
		{
			name:        "the connection is lost twice at the staple: retried, then it goes on",
			kind:        "app",
			status:      "Accepted",
			stapleFails: 2,
			ok:          true,
			want: []string{
				"notarised:",
				"attempt 1 of 5: xcrun stapler staple",
				"pause 15",
				"attempt 2 of 5: xcrun stapler staple",
				"pause 30",
				"attempt 3 of 5: xcrun stapler staple",
				"attempt 1 of 5: xcrun stapler validate",
				"stapled and validated:",
			},
			wantOnOneLine: []string{
				"attempt 1 of 5: xcrun stapler staple|exited 68",
				"attempt 3 of 5: xcrun stapler staple|exited 0",
			},
			notWant: []string{"pause 60", "attempt 4", "::error::"},
		},
		{
			name:          "the connection never comes back at the validation: refused after five",
			kind:          "app",
			status:        "Accepted",
			validateFails: 99,
			ok:            false,
			want: []string{
				"attempt 1 of 5: xcrun stapler staple",
				"attempt 1 of 5: xcrun stapler validate",
				"pause 15",
				"pause 30",
				"pause 60",
				"pause 90",
				"attempt 5 of 5: xcrun stapler validate",
				"carries no stapled ticket: refusing to publish artifacts that would carry it unstapled",
			},
			wantOnOneLine: []string{
				"attempt 1 of 5: xcrun stapler staple|exited 0",
				"attempt 1 of 5: xcrun stapler validate|exited 68",
				"attempt 5 of 5: xcrun stapler validate|exited 68",
			},
			notWant: []string{"attempt 6", "stapled and validated:"},
		},
		{
			name:        "the staple never gets there: five attempts, then the refusal",
			kind:        "app",
			status:      "Accepted",
			stapleFails: 99,
			ok:          false,
			want: []string{
				"attempt 1 of 5: xcrun stapler staple",
				"pause 15",
				"pause 30",
				"pause 60",
				"pause 90",
				"attempt 5 of 5: xcrun stapler staple",
				"carries no stapled ticket: refusing to publish artifacts that would carry it unstapled",
			},
			wantOnOneLine: []string{"attempt 5 of 5: xcrun stapler staple|exited 68"},
			notWant:       []string{"attempt 6", "xcrun stapler validate", "stapled and validated:"},
		},
		{
			name:       "a rejected submission reported with a clean exit: no staple, no retry",
			kind:       "app",
			status:     "Invalid",
			submitExit: "0",
			ok:         false,
			want: []string{
				// Apple's answer, printed, and the refusal read out of its
				// status: notarytool exited 0, and the submission is still a
				// rejection. (**This case exits 0 on purpose** -- the status
				// half of the pair is what refuses here, and the case below is
				// the exit-status half. A case that failed on both would pass
				// with either half removed.)
				`"status":"Invalid"`,
				"not 'Accepted'",
			},
			// The submission happened exactly once.
			wantCalls: []string{"xcrun notarytool submit"},
			notWant:   []string{"stapler", "pause", "attempt 2", "stapled and validated:"},
		},
		{
			name:       "an Accepted body over a non-zero notarytool exit: refused, not stapled",
			kind:       "app",
			status:     "Accepted",
			submitExit: "1",
			ok:         false,
			want: []string{
				// Apple's answer says Accepted, and notarytool's own exit says
				// the run did not finish cleanly. Both are required to be good,
				// so this refuses -- at once, with no staple, exactly like a
				// rejection.
				`"status":"Accepted"`,
				"notarytool exited 1",
			},
			wantCalls: []string{"xcrun notarytool submit"},
			notWant:   []string{"stapler", "pause", "attempt 2", "stapled and validated:"},
		},
		{
			name:   "a disk image goes to the notary service as itself: no zip step",
			kind:   "dmg",
			status: "Accepted",
			ok:     true,
			want: []string{
				"notarised:",
				"stapled and validated:",
			},
			wantCalls: []string{"xcrun notarytool submit", "xcrun stapler staple", "xcrun stapler validate"},
			notWant:   []string{"ditto", "::error::"},
		},
		{
			name:          "a missing credential refuses before anything reaches Apple",
			kind:          "app",
			status:        "Accepted",
			noCredentials: true,
			ok:            false,
			wantOnOneLine: []string{"APPLE_API_KEY|refusing to notarise"},
			notWant:       []string{"xcrun", "ditto", "stapler"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			work := filepath.Join(dir, "work")
			if err := os.MkdirAll(work, 0o755); err != nil {
				t.Fatal(err)
			}
			artifact := filepath.Join(work, "Fugaro Console.app")
			if c.kind == "dmg" {
				artifact = filepath.Join(work, "Fugaro Console-1.2.3-universal.dmg")
				if err := os.WriteFile(artifact, []byte("disk image"), 0o644); err != nil {
					t.Fatal(err)
				}
			} else if err := os.MkdirAll(artifact, 0o755); err != nil {
				t.Fatal(err)
			}

			// The fake `xcrun`: it logs every call it is given -- which is how
			// "no stapler call was made" is asserted -- answers a submission
			// with Apple's JSON shape, and fails `stapler staple` or `stapler
			// validate` a set number of times with the exit status the real
			// one uses when the connection is lost (68).
			xcrun := `#!/usr/bin/env bash
echo "xcrun $*" >> "$FAKE_LOG"
case "$1" in
  notarytool)
    echo "{\"id\":\"2efe2717\",\"message\":\"Successfully uploaded file\",\"status\":\"$FAKE_STATUS\"}"
    exit "${FAKE_SUBMIT_EXIT:-0}"
    ;;
  stapler)
    n=$(cat "$FAKE_COUNTER.$2" 2>/dev/null || echo 0)
    n=$((n + 1))
    echo "$n" > "$FAKE_COUNTER.$2"
    case "$2" in
      staple) fails="${FAKE_STAPLE_FAILS:-0}" ;;
      validate) fails="${FAKE_VALIDATE_FAILS:-0}" ;;
      *) fails=0 ;;
    esac
    if [ "$n" -le "$fails" ]; then
      echo "CloudKit: the network connection was lost" >&2
      exit 68
    fi
    exit 0
    ;;
esac
echo "fake xcrun: unexpected call: $*" >&2
exit 3
`
			// `ditto` writes the archive it was asked for, so the submission
			// path is a real file; `sleep` records the pause and returns, which
			// is what keeps this test from taking minutes.
			ditto := `#!/usr/bin/env bash
echo "ditto $*" >> "$FAKE_LOG"
for last in "$@"; do :; done
: > "$last"
`
			sleep := `#!/usr/bin/env bash
echo "pause $1"
`
			for name, body := range map[string]string{"xcrun": xcrun, "ditto": ditto, "sleep": sleep} {
				if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
					t.Fatal(err)
				}
			}

			// The script runs with the fake bin directory as its cwd, so it is
			// named by its absolute path -- the same file the signing step
			// runs, not a copy of it.
			scriptPath, err := filepath.Abs(filepath.Join("..", "..", consoleNotarizeScriptPath))
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(bash, scriptPath, c.kind, artifact)
			cmd.Dir = dir
			env := []string{
				"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
				"HOME=" + dir,
				"TMPDIR=" + dir,
				"FAKE_LOG=" + filepath.Join(dir, "calls.log"),
				"FAKE_COUNTER=" + filepath.Join(dir, "counter"),
				"FAKE_STATUS=" + c.status,
				"FAKE_SUBMIT_EXIT=" + c.submitExit,
				"FAKE_STAPLE_FAILS=" + strconv.Itoa(c.stapleFails),
				"FAKE_VALIDATE_FAILS=" + strconv.Itoa(c.validateFails),
			}
			if !c.noCredentials {
				env = append(env,
					"APPLE_API_KEY="+filepath.Join(dir, "key.p8"),
					"APPLE_API_KEY_ID=ABCDE12345",
					"APPLE_API_ISSUER=00000000-0000-0000-0000-000000000000",
				)
			}
			cmd.Env = env
			out, runErr := cmd.CombinedOutput()
			if (runErr == nil) != c.ok {
				t.Errorf("the script's exit was %v, want ok=%v; output:\n%s", runErr, c.ok, out)
			}
			calls, err := os.ReadFile(filepath.Join(dir, "calls.log"))
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			// The script's own lines and the calls it made are checked
			// separately, each in order: the call log is where "no stapler
			// call was made at all" and "the submission happened once" are
			// read from.
			inOrder := func(what, text string, want []string) {
				t.Helper()
				lines := strings.Split(text, "\n")
				at := 0
				for _, w := range want {
					next := consoleLineIndex(lines[at:], w, true)
					if next < 0 {
						t.Errorf("the %s does not carry %q after %d line(s); output:\n%s\ncalls:\n%s", what, w, at, out, calls)
						return
					}
					at += next + 1
				}
			}
			inOrder("script's output", string(out), c.want)
			inOrder("call log", string(calls), c.wantCalls)
			for _, spec := range c.wantOnOneLine {
				parts := strings.Split(spec, "|")
				found := false
				for _, line := range strings.Split(string(out), "\n") {
					all := true
					for _, part := range parts {
						if !strings.Contains(line, part) {
							all = false
							break
						}
					}
					if all {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("no line of the script's output carries all of %q -- each attempt's number AND its exit code are printed; output:\n%s", parts, out)
				}
			}
			both := string(out) + string(calls)
			for _, bad := range c.notWant {
				if strings.Contains(both, bad) {
					t.Errorf("the script's output or calls carry %q, which they must not; output:\n%s\ncalls:\n%s", bad, out, calls)
				}
			}
		})
	}
}

// TestDocsPendingWorkflowsReadmeNamesTheConsoleRows asserts the staging directory's
// README carries BOTH console rows -- this task's signing workflow and Task
// 1.4's tests workflow -- each saying what the file is, that it is staged because
// no agent may push a workflow here, and the one command that applies it. Task
// 1.4's `TestPendingWorkflowsReadmeNamesTheConsoleTestsRow` (singular) is the
// row-specific counterpart and keeps passing; the plural name is this one, so a
// reader never has to work out whether two similarly-named tests are the same
// test.
func TestDocsPendingWorkflowsReadmeNamesTheConsoleRows(t *testing.T) {
	doc := readDoc(t, "docs/pending-workflows/README.md")

	// Both rows exist as staged files under their own names.
	for _, path := range []string{consoleTestsWorkflowPath, consoleReleaseWorkflowPath} {
		if _, err := os.Stat(filepath.Join("..", "..", path)); err != nil {
			t.Fatalf("%s is missing: the README describes a staged file that must be here (%v)", path, err)
		}
	}

	// This file's row: what it is, why it is staged, the one command that applies
	// it, and the environment it reads.
	for _, want := range []string{
		"console-release.yml",
		// What the file is.
		"notarytool",
		"stapler",
		"macos-15",
		// Its own release, never the CLI's (owner, 2026-10-17).
		"console-vX.Y.Z",
		// Why it is staged here rather than applied.
		"Fugaro refuses to push a commit that touches `.github/workflows/**`",
		// The one command that applies it.
		"cp docs/pending-workflows/console-release.yml .github/workflows/console-release.yml",
		// The protected environment, and the fact that it is the owner's to
		// create: a staging file cannot create an environment (plan Task 4.5).
		"console-release",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/pending-workflows/README.md lacks %q: the console's staged signing workflow needs its row here, with what it is, why it is staged and the one command that applies it", want)
		}
	}

	// The row names every secret the workflow reads, exactly as the file does,
	// and says the notarisation credential is the API key rather than an
	// app-specific password -- the decision D4 does not settle, recorded where a
	// reader looking at the environment's contents will find it.
	for _, name := range consoleReleaseSecrets {
		if !strings.Contains(doc, name) {
			t.Errorf("docs/pending-workflows/README.md does not name the secret %s: the row must name every secret the signing job reads, so the owner can check the environment against it", name)
		}
	}
	if !strings.Contains(doc, "APPLE_APP_PASSWORD") && !strings.Contains(doc, "app-specific password") {
		t.Error("docs/pending-workflows/README.md does not say which notarisation credential the file uses: D4 leaves it open, and the answer here is the App Store Connect API key triple")
	}

	// The "Not verified" section, as the plan asks: this workflow has never run,
	// and the README says so rather than leaving a reader to infer a green
	// pipeline from a CI section. Read from THIS file's section, not from
	// anywhere in the document -- `images.yml`'s own "Not verified" note is
	// already there and would satisfy an unscoped grep while saying nothing about
	// the signing workflow.
	section := consoleReadmeSection(t, doc, "## `console-release.yml`")
	for _, want := range []string{"Not verified", "has never run"} {
		if !strings.Contains(section, want) {
			t.Errorf("docs/pending-workflows/README.md's `## console-release.yml` section lacks %q: neither this workflow nor the signing chain under it has ever run, and the section has to say so", want)
		}
	}
	if !strings.Contains(section, "TestDocsConsoleReleaseWorkflowIsStagedAndPinned") {
		t.Error("docs/pending-workflows/README.md's `## console-release.yml` section does not name the test that pins the file: what is verified is its text, and the section must say by which test")
	}
}

// consoleReadmeSection is the text of one `## …` section of a Markdown
// document: from its heading to the next `## ` heading at the same level, or to
// the end. The scoping is the point -- a phrase that has to be in one file's row
// must not be satisfied by the same phrase in another file's.
func consoleReadmeSection(t *testing.T, doc, heading string) string {
	t.Helper()
	start := strings.Index(doc, heading)
	if start < 0 {
		t.Fatalf("the document has no %q section", heading)
	}
	rest := doc[start+len(heading):]
	if end := strings.Index(rest, "\n## "); end >= 0 {
		return rest[:end]
	}
	return rest
}

// TestConsoleMVPRequiresTheStartupHandshake pins the owner's ruling of
// 2026-10-17: Task 3.3 (the version handshake wired at startup) is a release
// gate for the console MVP, because Task 3.2 alone builds a check nothing calls.
// The plan must carry the task as SECURITY-SENSITIVE, depending on Task 3.2, and
// name it in "What ships with the MVP" and in Task 4.6.
func TestConsoleMVPRequiresTheStartupHandshake(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "plans", "2026-10-15-desktop-console.md"))
	if err != nil {
		t.Fatal(err)
	}
	plan := string(raw)
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
	}
	heading := regexp.MustCompile(`(?m)^### Task 3\.3: .*SECURITY-SENSITIVE, independent opus review$`)
	if !heading.MatchString(plan) {
		t.Error("the Task 3.3 heading does not itself carry `SECURITY-SENSITIVE, independent opus review`")
	}
	if rule := regexp.MustCompile(`(?m)^9\. \*\*Security-sensitive tasks.*$`).FindString(plan); !strings.Contains(rule, "2.0, 3.3, 4.1") {
		t.Error("Global Constraints rule 9's list of security-sensitive tasks does not include 3.3")
	}
	task := section("### Task 3.3:", "\n## Phase 4")
	// "approved by the owner 2026-10-17" replaced "NOT APPROVED TO IMPLEMENT" on
	// 2026-10-17, when the owner gave the go for this task; the rest of the list
	// is unchanged, and the gate itself (this function's reason to exist) is
	// asserted below and by Task 4.6.
	for _, want := range []string{"**Depends on:** Task 3.2", "startup.compatibility", "Red step", "approved by the owner 2026-10-17"} {
		if !strings.Contains(task, want) {
			t.Errorf("Task 3.3 lost %q", want)
		}
	}
	if gate := section("### Task 4.6:", "\n## Phase 5"); !strings.Contains(gate, "Task 3.3 must be merged before the console MVP is released") {
		t.Error("Task 4.6's release gate does not require Task 3.3")
	}
	if ships := section("**What ships with the MVP.**", "\n\n"); !strings.Contains(ships, "Tasks 3.2 and 3.3") {
		t.Error(`"What ships with the MVP" does not name Task 3.3`)
	}
}

// TestConsoleMVPRequiresTheRendererEntry is the sibling of
// TestConsoleMVPRequiresTheStartupHandshake for the proposed Task 3.4 (added
// 2026-10-17): the console cannot draw a real dashboard until a task sets the
// preload, registers the handlers after the handshake, loads a script into the
// document and chooses an installation, and no other task owns that. The plan
// must carry the task as SECURITY-SENSITIVE, depending on Task 3.3, split into
// 3.4a/3.4b/3.4c, and named as an MVP gate in "What ships with the MVP", in Task
// 4.6, in the phase summary and in the cross-task dependencies, and Task 3.3
// must name it as the wiring's owner. Removing Task 3.4 from any of those places
// fails a named check here.
//
// The status line was "NOT APPROVED TO IMPLEMENT" until 2026-10-18, when the
// owner gave the go ("go on task 3.4") and answered C8, C9 and C10, the three
// decisions this task needed. The check below is on the *current* line, in the
// same shape TestConsoleMVPRequiresTheStartupHandshake uses for Task 3.3's
// 2026-10-17 go: a stale "not approved" line, or an answer that went missing from
// the decisions table, is what fails. C8's answer also amends the design --
// §5.1 rule 10 and §7.4 become "the spawn log, when asked, and the installations
// file, and nothing else" -- and that amendment is asserted here too, because a
// plan that promises it and a design that does not carry it is the failure mode
// this pair of documents has to be protected from.
func TestConsoleMVPRequiresTheRendererEntry(t *testing.T) {
	plan := readDoc(t, "docs/plans/2026-10-15-desktop-console.md")
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
	}
	heading := regexp.MustCompile(`(?m)^### Task 3\.4: The renderer entry.*SECURITY-SENSITIVE, independent opus review$`)
	if !heading.MatchString(plan) {
		t.Error("the Task 3.4 heading is missing or does not itself carry `SECURITY-SENSITIVE, independent opus review`")
	}
	if rule := regexp.MustCompile(`(?m)^9\. \*\*Security-sensitive tasks.*$`).FindString(plan); !strings.Contains(rule, "plus 3.4") {
		t.Error("Global Constraints rule 9's list of security-sensitive tasks does not include 3.4")
	}

	task := section("### Task 3.4:", "\n## Phase 4")
	for _, want := range []string{
		"**Status: approved by the owner 2026-10-18.**",
		"**3.4a is implemented**",
		"3.4b and 3.4c are approved and not yet written",
		"C8, C9 and C10 are answered",
		"**Depends on:** Task 3.3 (**hard**: #541 merged",
		"#### 3.4a", "#### 3.4b", "#### 3.4c",
		"registerConsoleHandlers",
		"never in `refused`",
		"script-src 'self'",
		"The CSP delta of this task is none",
		"Shared files",
		"Red step",
		"Owner-run additions",
	} {
		if !strings.Contains(task, want) {
			t.Errorf("Task 3.4 lost %q", want)
		}
	}
	if strings.Contains(task, "NOT APPROVED TO IMPLEMENT") {
		t.Error("Task 3.4 still carries a NOT APPROVED TO IMPLEMENT line: the owner gave the go on 2026-10-18")
	}

	// The three answers this task was waiting on, read out of the decisions table
	// rather than out of the task text: C8 is the persist ruling (and the wording
	// the design now carries), C9 is the separate picker window, C10 is that the
	// kill fields inside `live.watch` stay visible in reduced mode.
	answers := section("| **C8** |", "## Open items")
	for _, want := range []string{
		"Answered by the owner 2026-10-18: yes",
		"one bounded, schema-checked `installations.json` in `userData`, mode `0600`",
		"never read from the operator's own CLI config",
		"a **separate small picker window** opened from the application menu",
		"Answered by the owner 2026-10-18: no",
		"**stay visible**",
	} {
		if !strings.Contains(answers, want) {
			t.Errorf("the decisions table lost %q: C8, C9 and C10 were answered on 2026-10-18", want)
		}
	}

	// The design amendment C8's answer requires, in the design and not only in
	// the plan: rule 10 and section 7.4 now name the second write, in the same
	// words the plan's answer column uses. The same pair of places carries the
	// third write and the two updater-owned locations, added by the owner's
	// D768/D770 answers of 2026-10-30 (auto-update U5): C8's wording stays, as
	// the earlier amendment, and the newer one sits beside it.
	design := readDoc(t, "docs/design/desktop-console.md")
	if !strings.Contains(design, "10. **The app writes the spawn log, when asked, the installations file, `updates.json` and `crash.json`, and nothing else.**") {
		t.Error("design section 5.1 rule 10 was not amended to name the app's four writes (C8's second, D768's third, the crash switch's fourth)")
	}
	if !strings.Contains(design, "Amended 2026-11-01: the crash-reporting switch is a fourth file, `crash.json`") {
		t.Error("design section 5.1 rule 10 lost the 2026-11-01 amendment: the crash-reporting switch is the fourth write, and a later amendment sits beside the earlier ones rather than replacing them")
	}
	if !strings.Contains(design, "(Amended 2026-10-18 by the owner's C8 answer") {
		t.Error("design section 5.1 rule 10 lost the 2026-10-18 C8 amendment: a later amendment sits beside the earlier one rather than replacing it")
	}
	if !strings.Contains(design, "Amended 2026-10-30 by the owner's D768 and D770 answers") {
		t.Error("design section 5.1 rule 10 lost the 2026-10-30 D768/D770 amendment")
	}
	if !strings.Contains(design, "**The app writes four things to disk and nothing else (§5.1 rule 10, amended 2026-10-30 by the owner's D768 and D770 answers and 2026-11-01 by his ruling of 2026-10-31)") {
		t.Error("design section 7.4 was not amended to D768/D770's wording and the 2026-11-01 crash-switch amendment")
	}
	if !strings.Contains(design, "**No telemetry, no analytics, no update check that carries an identifier, and crash reporting only ever by the user's own opt-in.**") {
		t.Error("design section 7.4's promise sentence was not amended: the console reports crashes to Sentry after the person turns the switch on, so the paragraph states that flow rather than promising no crash reporting")
	}
	for _, want := range []string{"hosted in the European Union", "**no message, no path, no user name, no environment and no host name**", "`console/src/main/crash-dsn.ts`", "the per-user switch is the only gate"} {
		if !strings.Contains(design, want) {
			t.Errorf("design section 7.4 no longer states %q: the crash reporter's disclosure is the sentence the website and the console's README repeat, and it names the one gate, the scrubbing, and the DSN's one embedded constant", want)
		}
	}
	if !strings.Contains(design, "never a credential") {
		t.Error("design section 5.1 rule 10's installations paragraph does not say the file holds no credential")
	}
	for _, want := range []string{"`updates.json`", "fugaro-console-updater", "ShipIt", "hardenUpdater", "x-user-staging-id"} {
		if !strings.Contains(design, want) {
			t.Errorf("design section 5.1 rule 10 or 7.4 no longer names %q: D768 and D770 are the amendment that puts the update switch and the updater's two locations on the write list", want)
		}
	}

	// Task 3.3's own section, bounded by Task 3.4's heading so the text of 3.4
	// cannot satisfy it, still carries its own facts and names 3.4 as the owner
	// of the wiring it leaves out.
	own := section("### Task 3.3:", "\n### Task 3.4:")
	for _, want := range []string{"**Depends on:** Task 3.2", "startup.compatibility", "Red step", "**The renderer entry is Task 3.4's, not this task's"} {
		if !strings.Contains(own, want) {
			t.Errorf("Task 3.3's own section lost %q", want)
		}
	}

	if gate := section("### Task 4.6:", "\n## Phase 5"); !strings.Contains(gate, "Task 3.4 must be merged before the console MVP is released") {
		t.Error("Task 4.6's release gate does not require Task 3.4")
	}
	if ships := section("**What ships with the MVP.**", "\n\n"); !strings.Contains(ships, "the renderer entry (Task 3.4") || !strings.Contains(ships, "item 4 or item 5 does not ship") {
		t.Error(`"What ships with the MVP" does not name Task 3.4 as a gate`)
	}
	if summary := section("## Phase summary", "\n## "); !strings.Contains(summary, "Task 3.4") {
		t.Error("the phase summary does not name Task 3.4")
	}
	if deps := section("## Cross-task dependencies", "\n## "); !strings.Contains(deps, "Task 3.4 (the renderer entry") {
		t.Error("the cross-task dependencies do not name Task 3.4")
	}

	// **The status is one fact in five places, and the scan above only holds one
	// of them.** A review of the approval commit found the plan still calling
	// Task 3.4 "proposed 2026-10-17, not approved" in the phase summary, in the
	// cross-task dependencies and in the file tree's annotations for the three
	// files 3.4a creates -- places the section-scoped scan above never reads, so
	// the drift went undetected while the task itself was correct. These are the
	// four strings that said so; each is asserted absent, and the three that
	// carry a status (rather than a question's origin date) are asserted to carry
	// the approval instead. Rewording any of them without updating the others
	// fails here.
	for _, stale := range []string{
		"Task 3.4, proposed 2026-10-17, not approved",
		"Task 3.4 (the renderer entry, proposed",
		"Task 3.4a (proposed)",
		"Task 3.4b (proposed)",
	} {
		if strings.Contains(plan, stale) {
			t.Errorf("the plan still calls Task 3.4 %q: the owner approved it on 2026-10-18, and the status has to be one fact everywhere it is told", stale)
		}
	}
	if row := section("| **3. When things are missing** |", "\n"); !strings.Contains(row, "Task 3.4, approved by the owner 2026-10-18") {
		t.Error("the phase summary's phase-3 row does not carry Task 3.4's approval")
	}
	if bullet := section("- **Task 3.4 (the renderer entry", "\n"); !strings.Contains(bullet, "approved by the owner 2026-10-18") {
		t.Error("the cross-task dependencies bullet does not carry Task 3.4's approval")
	}
	entry := regexp.MustCompile(`(?m)^  src/renderer/entry\.ts .*$`).FindString(plan)
	if !strings.Contains(entry, "Task 3.4a (approved by the owner 2026-10-18") {
		t.Errorf("the file tree's annotation for src/renderer/entry.ts does not carry 3.4a's approval: %q", entry)
	}
	if strings.Contains(entry, "proposed") {
		t.Errorf("the file tree still calls src/renderer/entry.ts proposed: %q", entry)
	}
}

// ---------------------------------------------------------------------------
// Task 4.6: the release gate requires the console check.
//
// scripts/release-gate.sh requires the checks in its `names` list on the tag's
// commit and, in its identical_tree_checks fallback, on the pull request head
// whose tree matches. Task 4.6 adds the console to that list -- and the console
// job is a **matrix**: .github/workflows/console-tests.yml runs one job keyed
// `console` on two operating systems, and GitHub names each leg's check run
// "<job> (<values>)", so what the checks API reports is
// `console (ubuntu-latest)` and `console (macos-latest)`. A bare `console` is
// reported by nothing: check_state matches check runs by exact name, so
// requiring it would make the gate see a check it can never find, and a
// missing check is not a success -- the gate would refuse **every** tag in this
// repository, the CLI-only releases included. That is the failure this task's
// correction (owner, 2026-10-18) exists to prevent, and the one the plan's own
// wording -- "Add `console` to that list" -- would have caused.
//
// So the gate requires both legs, and this test **derives** them from the
// workflow rather than repeating them: rename the job, add a matrix key or add
// an operating system, and the gate's list has to move with it or this fails.
// The two names carry a space, which the old whitespace-split `names` string
// cannot hold -- so `names` is an array, and the array-shaped loops are pinned
// here too, because reverting one of them to `$names` would silently look for
// a check named `console` and one named `(ubuntu-latest)`.
//
// The behaviour is exercised, not just read: a fake gh answers the gate's three
// API calls and runs the gate's own `--jq` expression through a real jq, so the
// name match, the app filter and the status/conclusion rules are all the
// script's, and a green run of a required name from another app is refused for
// the reason the script gives it, not because the fake knew to say so.
// ---------------------------------------------------------------------------

const releaseGatePath = "scripts/release-gate.sh"

// readReleaseGate returns the script's text, the required-check names its
// `names=(...)` array holds, and the names listed by the block of `#   ` lines
// directly above that array -- the comment's own copy of the list, which the
// script's comment asks a reader to keep in step with it.
func readReleaseGate(t *testing.T) (string, []string, []string) {
	t.Helper()
	script := readDoc(t, releaseGatePath)
	lines := strings.Split(script, "\n")
	quoted := regexp.MustCompile(`"([^"]*)"`)
	listed := regexp.MustCompile(`^#   (.*\S)[ \t]*$`)
	var required, commented []string
	found := false
	for i, line := range lines {
		if !strings.HasPrefix(line, "names=(") {
			continue
		}
		if found {
			t.Fatalf("%s has more than one names=(...) line: the required set has to be one list", releaseGatePath)
		}
		found = true
		if !strings.HasSuffix(line, ")") {
			t.Fatalf("%s's names= line is not one array literal on one line, which is the form this test reads: %q", releaseGatePath, line)
		}
		for _, m := range quoted.FindAllStringSubmatch(line, -1) {
			required = append(required, m[1])
		}
		for j := i - 1; j >= 0; j-- {
			m := listed.FindStringSubmatch(lines[j])
			if m == nil {
				break
			}
			commented = append([]string{m[1]}, commented...)
		}
	}
	if !found {
		t.Fatalf("%s has no names=(...) array: the gate's required-check set must be a list this task's test can read", releaseGatePath)
	}
	if len(required) == 0 {
		t.Fatalf("%s's names=(...) array is empty: a gate that requires nothing passes every tag", releaseGatePath)
	}
	if len(commented) == 0 {
		t.Fatalf("%s lists no checks in the `#   ` comment block directly above names=(...): the prose copy of the list is how the two are held together", releaseGatePath)
	}
	return script, required, commented
}

// consoleGateCheckNames derives, from the **applied** console workflow, the
// check-run names its job produces: the job's displayed name (falling back to
// its key, as GitHub does) and one name per matrix leg, "<job> (<values>)"
// with the values joined by ", ". The matrix keys are read in sorted order --
// the current workflow has one, and this is the one place to revisit if a
// second key is ever added, since GitHub joins the values in the order the keys
// are written.
func consoleGateCheckNames(t *testing.T) []string {
	t.Helper()
	names, err := deriveConsoleCheckNames(readDoc(t, appliedConsoleTestsPath))
	if err != nil {
		t.Fatalf("%s: %v", appliedConsoleTestsPath, err)
	}
	return names
}

// deriveConsoleCheckNames is consoleGateCheckNames over a workflow's text, so
// the derivation itself can be run against a workflow whose matrix differs
// (TestConsoleCheckNamesFollowTheMatrix): a derivation that ignored the matrix
// would otherwise agree with the gate for as long as the matrix never moved.
func deriveConsoleCheckNames(text string) ([]string, error) {
	var wf struct {
		Jobs map[string]struct {
			Name     string `yaml:"name"`
			Strategy struct {
				Matrix map[string][]string `yaml:"matrix"`
			} `yaml:"strategy"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(text), &wf); err != nil {
		return nil, fmt.Errorf("not valid YAML, or a matrix this derivation does not read (include/exclude): %v", err)
	}
	if len(wf.Jobs) != 1 {
		var keys []string
		for k := range wf.Jobs {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		return nil, fmt.Errorf("has jobs %v: the release gate requires the checks of the console's tests, and this test cannot tell which of several jobs that is", keys)
	}
	var key string
	for k := range wf.Jobs {
		key = k
	}
	job := wf.Jobs[key]
	display := job.Name
	if display == "" {
		display = key
	}
	// GitHub appends the matrix values only to a name that does not use them
	// itself; a name with an expression in it is a shape this derivation would
	// get wrong, so it refuses rather than guessing.
	if strings.Contains(display, "${{") {
		return nil, fmt.Errorf("the %s job's name %q carries an expression: GitHub then does not append the matrix values, and this derivation does not evaluate expressions", key, display)
	}
	var keys []string
	for k := range job.Strategy.Matrix {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if len(keys) == 0 || len(job.Strategy.Matrix[keys[0]]) == 0 {
		return nil, fmt.Errorf("the %s job has no matrix values (%v): without them it reports one check run named %q, which is not the shape scripts/release-gate.sh requires", key, job.Strategy.Matrix, display)
	}
	legs := [][]string{{}}
	for _, k := range keys {
		var next [][]string
		for _, leg := range legs {
			for _, v := range job.Strategy.Matrix[k] {
				next = append(next, append(slices.Clone(leg), v))
			}
		}
		legs = next
	}
	names := make([]string, 0, len(legs))
	for _, leg := range legs {
		names = append(names, display+" ("+strings.Join(leg, ", ")+")")
	}
	slices.Sort(names)
	return names, nil
}

// TestConsoleCheckNamesFollowTheMatrix runs the derivation the gate test relies
// on against workflows that differ from the applied one, so the console names
// are shown to come from the job key and the matrix rather than from a list
// that happens to match today: an added platform, a renamed job and a job with
// no matrix each change the answer or are refused.
func TestConsoleCheckNamesFollowTheMatrix(t *testing.T) {
	wf := func(job, name, oses string) string {
		out := "on: [push]\njobs:\n  " + job + ":\n"
		if name != "" {
			out += "    name: " + name + "\n"
		}
		if oses != "" {
			out += "    strategy:\n      matrix:\n        os: [" + oses + "]\n"
		}
		return out + "    runs-on: ubuntu-latest\n    steps: [{run: 'true'}]\n"
	}
	for _, c := range []struct {
		label string
		text  string
		want  []string
	}{
		{"the applied shape", wf("console", "console", "ubuntu-latest, macos-latest"), []string{"console (macos-latest)", "console (ubuntu-latest)"}},
		{"an added platform", wf("console", "console", "ubuntu-latest, macos-latest, windows-latest"), []string{"console (macos-latest)", "console (ubuntu-latest)", "console (windows-latest)"}},
		{"a renamed job with no name", wf("desktop", "", "ubuntu-latest"), []string{"desktop (ubuntu-latest)"}},
		{"a display name over the key", wf("console", "console-ui", "macos-latest"), []string{"console-ui (macos-latest)"}},
	} {
		got, err := deriveConsoleCheckNames(c.text)
		if err != nil {
			t.Errorf("%s: %v", c.label, err)
			continue
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: derived %v, want %v", c.label, got, c.want)
		}
	}
	for _, c := range []struct{ label, text string }{
		{"no matrix", wf("console", "console", "")},
		{"a name with an expression", wf("console", "'console ${{ matrix.os }}'", "ubuntu-latest")},
	} {
		if got, err := deriveConsoleCheckNames(c.text); err == nil {
			t.Errorf("%s: derived %v, want a refusal", c.label, got)
		}
	}
}

// TestReleaseGateRequiresTheConsoleCheck holds scripts/release-gate.sh to the
// console checks .github/workflows/console-tests.yml reports, and proves the
// gate's verdict follows them: it passes only when every required check is
// green on the tagged commit, and refuses when a console leg is missing,
// pending, failed, cancelled or green only from some other app. The mutations
// to try first are the ones this task exists to prevent: write the bare
// `console` into the list (every tag is then refused), drop one leg, and revert
// a loop to `for name in $names`.
func TestReleaseGateRequiresTheConsoleCheck(t *testing.T) {
	script, required, commented := readReleaseGate(t)
	consoleNames := consoleGateCheckNames(t)

	// The full set, exactly: the three CLI checks the gate required before this
	// task, and both console legs, and nothing else. The workflow is the source
	// of the console names, so a renamed job or an added platform fails here
	// rather than at a release tag.
	want := append([]string{"test", "terraform", "rules"}, consoleNames...)
	slices.Sort(want)
	got := slices.Clone(required)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("%s requires the checks %v, want %v (the console names derived from %s): a name no workflow reports is a check the gate can never find, and a missing check is not a success", releaseGatePath, got, want, appliedConsoleTestsPath)
	}
	for _, name := range consoleNames {
		if !slices.Contains(required, name) {
			t.Errorf("%s does not require %q, which %s's matrix reports on every commit: a console failure would not stop a tag (Task 4.6)", releaseGatePath, name, appliedConsoleTestsPath)
		}
	}
	if slices.Contains(required, "console") {
		t.Errorf("%s requires the bare name %q, which no workflow reports: the console job is a matrix, so its check runs are %v, and requiring a name that cannot exist refuses every tag in this repository", releaseGatePath, "console", consoleNames)
	}

	// Prose and variable: the comment above the array lists the same set, so the
	// two cannot drift and a reader of the script sees what it requires.
	commentedSorted := slices.Clone(commented)
	slices.Sort(commentedSorted)
	if !slices.Equal(commentedSorted, got) {
		t.Errorf("%s's names array is %v but the comment above it lists %v: they have to name the same set", releaseGatePath, got, commentedSorted)
	}

	// The array is read as an array. A whitespace-split loop turns
	// `console (ubuntu-latest)` into `console` and `(ubuntu-latest)`, two checks
	// that no workflow reports.
	if strings.Contains(script, "for name in $names") {
		t.Errorf("%s still iterates `for name in $names`: word splitting takes a console leg apart, so the gate would require a check named `console` that no workflow reports", releaseGatePath)
	}
	if n := strings.Count(script, `for name in "${names[@]}"`); n != 2 {
		t.Errorf("%s has %d loops over \"${names[@]}\", want 2 (the target's own checks and identical_tree_checks'): every loop over the required set has to keep a name with a space in it whole", releaseGatePath, n)
	}
	if strings.Contains(script, `pending="$name`) {
		t.Errorf("%s still packs the first pending check into one \"$name $state\" string: a console leg's name carries a space, so the message would print it split at the wrong one", releaseGatePath)
	}

	// The documents that state the gate's check list name both legs, and say
	// that the legs exist on main only once console is merged into main and a
	// push has run them: a gate run before that refuses every tag.
	for _, doc := range []string{"docs/release.md", ".claude/skills/new-release/SKILL.md", releaseGatePath} {
		text := readDoc(t, doc)
		for _, name := range consoleNames {
			if doc != releaseGatePath && !strings.Contains(text, name) {
				t.Errorf("%s does not name the required check %q", doc, name)
			}
		}
		if !strings.Contains(text, "merged into `main`") {
			t.Errorf("%s does not say the console check runs exist on main only once console is merged into main (and a push has run them)", doc)
		}
	}

	if len(consoleNames) != 2 {
		t.Fatalf("%s's console job reports %v: this test's cases are written for two legs, and a third platform must be a deliberate edit here", appliedConsoleTestsPath, consoleNames)
	}
	// The two legs, by position in the derived (sorted) set; only one of them is
	// ever non-green in a case below, so position does not decide an
	// expectation -- the gate reports the check that is actually missing.
	legA, legB := consoleNames[0], consoleNames[1]
	// The first console name the script requires: when neither leg is green,
	// that is the one its message names, since it walks the array in order.
	firstConsoleLeg := ""
	for _, name := range required {
		if slices.Contains(consoleNames, name) {
			firstConsoleLeg = name
			break
		}
	}
	if firstConsoleLeg == "" {
		t.Fatalf("%s requires none of the console legs %v: the set assertion above has already failed, and this is the check it failed on", releaseGatePath, consoleNames)
	}

	// requiredChecks is the five checks of a fully green commit, with one entry
	// replaced or removed for a case.
	requiredChecks := func(replace string, app, state string, drop bool) []string {
		var out []string
		for _, name := range append([]string{"test", "terraform", "rules"}, consoleNames...) {
			switch {
			case name == replace && drop:
			case name == replace:
				out = append(out, gateCheckRun(name, app, state))
			default:
				out = append(out, gateCheckRun(name, "github-actions", "success"))
			}
		}
		return out
	}

	t.Run("every required check green is a pass", func(t *testing.T) {
		h := newGateHarness(t)
		h.setTargetChecks(t, requiredChecks("", "", "", false))
		ok, out := h.run(t)
		if !ok {
			t.Fatalf("the gate refused a commit whose every required check is green:\n%s", out)
		}
		if !strings.Contains(out, "gate passed for v1.2.3") {
			t.Errorf("the gate passed without saying so:\n%s", out)
		}
	})

	// One leg at a time, so the message names the check that is not green: a run
	// that is not there and a run that belongs to another app are both "missing"
	// to the gate, which selects on the name and the app slug together. The
	// labels are derived from the legs above, like everything else here.
	type refusal struct {
		name  string
		leg   string
		app   string
		state string
		drop  bool
		// want is the state the gate reports, when it is not the fixture's own
		// (a queued run is pending; skipped, neutral and timed-out runs are
		// completed without success, so failed).
		want string
	}
	var refusals []refusal
	// Every leg and every non-green shape, and the three CLI checks too: the
	// set those three were held to before this task must hold exactly as it did.
	for _, leg := range append([]string{legA, legB}, "test", "terraform", "rules") {
		refusals = append(refusals,
			refusal{name: fmt.Sprintf("%s is missing", leg), leg: leg, drop: true},
			refusal{name: fmt.Sprintf("%s is still running", leg), leg: leg, app: "github-actions", state: "pending"},
			refusal{name: fmt.Sprintf("%s is queued", leg), leg: leg, app: "github-actions", state: "queued", want: "pending"},
			refusal{name: fmt.Sprintf("%s failed", leg), leg: leg, app: "github-actions", state: "failed"},
			refusal{name: fmt.Sprintf("%s was cancelled", leg), leg: leg, app: "github-actions", state: "cancelled"},
			refusal{name: fmt.Sprintf("%s was skipped", leg), leg: leg, app: "github-actions", state: "skipped", want: "failed"},
			refusal{name: fmt.Sprintf("%s is neutral", leg), leg: leg, app: "github-actions", state: "neutral", want: "failed"},
			refusal{name: fmt.Sprintf("%s timed out", leg), leg: leg, app: "github-actions", state: "timed_out", want: "failed"},
			refusal{name: fmt.Sprintf("%s is green, but from another app", leg), leg: leg, app: "some-other-app", state: "success"},
		)
	}
	for _, c := range refusals {
		t.Run(c.name+" is refused", func(t *testing.T) {
			leg := c.leg
			// What the gate's own rules make of the case: a run that is not there
			// and a run that belongs to another app are both "missing", because
			// check_state selects on the name **and** on the app slug.
			state := c.state
			if c.want != "" {
				state = c.want
			}
			if c.drop || c.app != "github-actions" {
				state = "missing"
			}
			h := newGateHarness(t)
			h.setTargetChecks(t, requiredChecks(leg, c.app, c.state, c.drop))
			ok, out := h.run(t)
			if ok {
				t.Fatalf("the gate passed with %s:\n%s", c.name, out)
			}
			wantErr := fmt.Sprintf("required check '%s' is %s on %s", leg, state, h.sha)
			if !strings.Contains(out, wantErr) {
				t.Errorf("the gate refused for the wrong reason:\nwant %q in:\n%s", wantErr, out)
			}
		})
	}

	t.Run("a check named plain console does not stand in for the legs", func(t *testing.T) {
		// The plan's text asked for the bare name. A commit that carries a green
		// run of that name -- and neither matrix leg -- must still be refused:
		// the legs are what GitHub reports for the job that actually runs.
		h := newGateHarness(t)
		var checks []string
		for _, name := range append([]string{"test", "terraform", "rules"}, consoleNames...) {
			if slices.Contains(consoleNames, name) {
				continue
			}
			checks = append(checks, gateCheckRun(name, "github-actions", "success"))
		}
		checks = append(checks, gateCheckRun("console", "github-actions", "success"))
		h.setTargetChecks(t, checks)
		ok, out := h.run(t)
		if ok {
			t.Fatalf("the gate passed on a bare `console` run:\n%s", out)
		}
		if want := fmt.Sprintf("required check '%s' is missing on %s", firstConsoleLeg, h.sha); !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	})

	// `check_state`'s documented coexistence rule -- "cancelled runs (superseded
	// by a newer one) are ignored while another run of the name exists" -- run
	// rather than assumed. Every other fixture in this file writes exactly one
	// run per name, so the `select(.conclusion != "cancelled")` branch was never
	// reached: a mutation that made any cancelled run poison its name survived
	// the whole suite. That is the re-run/superseded flow the rule exists for,
	// and it is the normal state of a name while CI re-runs a failed leg.
	t.Run("a cancelled run beside a green run of the same name is ignored", func(t *testing.T) {
		h := newGateHarness(t)
		var checks []string
		for _, name := range required {
			checks = append(checks, gateCheckRun(name, "github-actions", "success"))
			checks = append(checks, gateCheckRun(name, "github-actions", "cancelled"))
		}
		h.setTargetChecks(t, checks)
		ok, out := h.run(t)
		if !ok {
			t.Fatalf("the gate refused a commit whose every required name has a green run beside a superseded cancelled one:\n%s", out)
		}
		if !strings.Contains(out, "gate passed for v1.2.3") {
			t.Errorf("the gate passed without saying so:\n%s", out)
		}
	})

	t.Run("cancelled runs alone are a cancelled check, not a green one", func(t *testing.T) {
		h := newGateHarness(t)
		var checks []string
		for _, name := range required {
			checks = append(checks, gateCheckRun(name, "github-actions", "cancelled"))
		}
		h.setTargetChecks(t, checks)
		ok, out := h.run(t)
		if ok {
			t.Fatalf("the gate passed on a commit whose only runs of every required name were cancelled:\n%s", out)
		}
		if want := fmt.Sprintf("required check '%s' is cancelled on %s", required[0], h.sha); !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	})

	t.Run("a cancelled run beside a failed run of the same name still fails", func(t *testing.T) {
		h := newGateHarness(t)
		var checks []string
		for _, name := range required {
			checks = append(checks, gateCheckRun(name, "github-actions", "success"))
		}
		checks = append(checks, gateCheckRun(required[0], "github-actions", "failed"))
		h.setTargetChecks(t, checks)
		ok, out := h.run(t)
		if ok {
			t.Fatalf("the gate passed with %s failed beside a cancelled run:\n%s", required[0], out)
		}
		if want := fmt.Sprintf("required check '%s' is failed on %s", required[0], h.sha); !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	})

	t.Run("the identical-tree fallback requires the console legs too", func(t *testing.T) {
		h := newGateHarness(t)
		head := h.identicalTreeHead(t)
		h.setTargetChecks(t, nil)
		h.setHeadChecks(t, requiredChecks("", "", "", false))
		h.setMergedPR(t, h.sha, head)
		ok, out := h.run(t)
		if !ok {
			t.Fatalf("the gate refused a commit whose merged pull request head carries every required check:\n%s", out)
		}
		if !strings.Contains(out, "using the checks of "+head+" (identical tree)") {
			t.Errorf("the fallback did not name the head it used:\n%s", out)
		}
	})

	for _, c := range []struct {
		label, leg, app, state string
	}{
		{"pending " + legA, legA, "github-actions", "pending"},
		{"skipped " + legA, legA, "github-actions", "skipped"},
		{legB + " green only from another app", legB, "some-other-app", "success"},
		{"failed terraform", "terraform", "github-actions", "failed"},
	} {
		t.Run("the identical-tree fallback refuses a head with "+c.label, func(t *testing.T) {
			h := newGateHarness(t)
			head := h.identicalTreeHead(t)
			h.setTargetChecks(t, nil)
			h.setHeadChecks(t, requiredChecks(c.leg, c.app, c.state, false))
			h.setMergedPR(t, h.sha, head)
			ok, out := h.run(t)
			if ok {
				t.Fatalf("the gate passed on a head with %s:\n%s", c.label, out)
			}
			if strings.Contains(out, "identical tree") {
				t.Errorf("the head's checks must not vouch:\n%s", out)
			}
		})
	}

	t.Run("the identical-tree fallback refuses a head without a console leg", func(t *testing.T) {
		h := newGateHarness(t)
		head := h.identicalTreeHead(t)
		h.setTargetChecks(t, nil)
		h.setHeadChecks(t, requiredChecks(legB, "", "", true))
		h.setMergedPR(t, h.sha, head)
		ok, out := h.run(t)
		if ok {
			t.Fatalf("the gate passed on a head missing %s:\n%s", legB, out)
		}
		if strings.Contains(out, "identical tree") {
			t.Errorf("the head's incomplete checks must not vouch:\n%s", out)
		}
		// The target has no checks of its own, so the message names the first
		// name the script requires -- read from the array, not assumed.
		if want := fmt.Sprintf("required check '%s' is missing on %s", required[0], h.sha); !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	})
}

// gateCheckRun is one check run as the fake gh's state file writes it: the
// check-run name, the app slug that owns it, and the state the gate reads.
func gateCheckRun(name, app, state string) string {
	return name + "\t" + app + "\t" + state
}

// gateFakeGH answers the three `gh api` calls scripts/release-gate.sh makes.
// It is a shell script because the gate runs it through PATH, and it runs the
// gate's **own** `--jq` expression through a real jq over check runs built from
// the state file -- so the name match, the `github-actions` app filter and the
// status/conclusion rules are all the script's, and a bug in the expression
// shows up here rather than being papered over by a fake that already knew the
// answer.
const gateFakeGH = `#!/usr/bin/env bash
set -eu
log=${FAKE_GH_LOG:?}
printf 'gh %s\n' "$*" >>"$log"

api=${2:-}
expr=""
prev=""
for a in "$@"; do
  if [ "$prev" = "--jq" ]; then expr=$a; fi
  prev=$a
done

# check_runs_json FILE prints the checks API's own shape, one run per
# "name<TAB>app<TAB>state" line.
check_runs_json() {
  jq -Rn '
    [inputs | select(length > 0) | split("\t")
     | {name: .[0], app: {slug: .[1]}, state: .[2]}
     | . + (if .state == "pending" then {status: "in_progress", conclusion: null}
            elif .state == "failed" then {status: "completed", conclusion: "failure"}
            elif .state == "queued" then {status: "queued", conclusion: null}
            elif .state == "cancelled" then {status: "completed", conclusion: "cancelled"}
            elif .state == "skipped" then {status: "completed", conclusion: "skipped"}
            elif .state == "neutral" then {status: "completed", conclusion: "neutral"}
            elif .state == "timed_out" then {status: "completed", conclusion: "timed_out"}
            elif .state == "success" then {status: "completed", conclusion: "success"}
            else error("fake gh: unknown check state \(.state)") end)]
    | {check_runs: .}' <"$1"
}

case "$api" in
  */check-runs*)
    sha=$(printf '%s' "$api" | sed -n 's|.*/commits/\([^/]*\)/check-runs.*|\1|p')
    state=${FAKE_GH_STATE:?}
    if [ -n "${FAKE_GH_HEAD_STATE:-}" ] && [ -s "${FAKE_GH_PR:-}" ] && [ "$sha" = "$(cut -f3 "${FAKE_GH_PR}")" ]; then
      state=$FAKE_GH_HEAD_STATE
    fi
    check_runs_json "$state" | jq -r "$expr"
    ;;
  */git/commits/*)
    sha=$(printf '%s' "$api" | sed -n 's|.*/git/commits/\([^/?]*\).*|\1|p')
    git -C "${FAKE_GH_ORIGIN:?}" rev-parse "$sha^{tree}"
    ;;
  */pulls*)
    if [ -s "${FAKE_GH_PR:-}" ]; then cat "${FAKE_GH_PR}"; fi
    ;;
  *)
    echo "fake gh: unexpected call: $*" >&2
    exit 3
    ;;
esac
`

// gateHarness is a checkout with a real origin/main, a fake gh first on PATH,
// and the files that fake reads: nothing about the gate's logic is simulated.
type gateHarness struct {
	work   string
	origin string
	sha    string
	bin    string
	root   string
}

func newGateHarness(t *testing.T) *gateHarness {
	t.Helper()
	for _, tool := range []string{"bash", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not on PATH: the gate's own --jq expression is run through a real jq", tool)
		}
	}
	testutil.IsolateGit(t)
	root := t.TempDir()
	origin := testutil.NewRemote(t, map[string]string{"README.md": "seed\n"})
	work := filepath.Join(root, "work")
	testutil.Git(t, root, "clone", "--quiet", origin, work)
	testutil.WriteFiles(t, root, map[string]string{"bin/gh": gateFakeGH})
	return &gateHarness{
		work:   work,
		origin: origin,
		sha:    testutil.Git(t, work, "rev-parse", "HEAD"),
		bin:    filepath.Join(root, "bin"),
		root:   root,
	}
}

func (h *gateHarness) write(t *testing.T, name string, lines []string) string {
	t.Helper()
	path := filepath.Join(h.root, name)
	body := ""
	if len(lines) > 0 {
		body = strings.Join(lines, "\n") + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// setTargetChecks is the tagged commit's check-run set.
func (h *gateHarness) setTargetChecks(t *testing.T, lines []string) {
	t.Helper()
	h.write(t, "target.tsv", lines)
}

// setHeadChecks is the merged pull request head's check-run set.
func (h *gateHarness) setHeadChecks(t *testing.T, lines []string) {
	t.Helper()
	h.write(t, "head.tsv", lines)
}

// setMergedPR is the checks API's listing of the pull request merged as the
// target: the tsv the gate's own jq would have produced.
func (h *gateHarness) setMergedPR(t *testing.T, mergeSHA, headSHA string) {
	t.Helper()
	h.write(t, "pulls.tsv", []string{"2026-01-01T00:00:00Z\t" + mergeSHA + "\t" + headSHA})
}

// identicalTreeHead pushes an empty commit -- the same tree as the target, a
// different commit -- to origin and returns its sha, so the gate's fallback has
// a candidate to read.
func (h *gateHarness) identicalTreeHead(t *testing.T) string {
	t.Helper()
	testutil.Git(t, h.work, "checkout", "--quiet", "-b", "pr-head")
	testutil.Git(t, h.work, "commit", "--quiet", "--allow-empty", "-m", "pr head")
	head := testutil.Git(t, h.work, "rev-parse", "HEAD")
	testutil.Git(t, h.work, "push", "--quiet", "origin", "HEAD:refs/heads/pr-head")
	testutil.Git(t, h.work, "checkout", "--quiet", "main")
	targetTree := testutil.Git(t, h.work, "rev-parse", h.sha+"^{tree}")
	if headTree := testutil.Git(t, h.work, "rev-parse", head+"^{tree}"); headTree != targetTree {
		t.Fatalf("the fixture head's tree is %s, not the target's %s: the fallback is about identical trees", headTree, targetTree)
	}
	return head
}

// run runs scripts/release-gate.sh for v1.2.3 in the checkout and returns
// whether it passed and everything it printed.
func (h *gateHarness) run(t *testing.T, extra ...string) (bool, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", filepath.Join(testutil.ModuleRoot(), releaseGatePath), "v1.2.3")
	cmd.Dir = h.work
	cmd.Env = append(os.Environ(),
		"PATH="+h.bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GITHUB_SHA="+h.sha,
		"GITHUB_REPOSITORY=dimipaun/fugaro",
		// No waiting: a conclusive failure fails at once, and a missing or
		// running check fails on the first pass rather than after a poll.
		"GATE_WAIT_SECONDS=0",
		"GATE_POLL_SECONDS=1",
		"FAKE_GH_LOG="+filepath.Join(h.root, "gh.log"),
		"FAKE_GH_ORIGIN="+h.origin,
		"FAKE_GH_STATE="+filepath.Join(h.root, "target.tsv"),
	)
	for _, name := range []string{"head.tsv", "pulls.tsv"} {
		path := filepath.Join(h.root, name)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if name == "head.tsv" {
			cmd.Env = append(cmd.Env, "FAKE_GH_HEAD_STATE="+path)
		} else {
			cmd.Env = append(cmd.Env, "FAKE_GH_PR="+path)
		}
	}
	cmd.Env = append(cmd.Env, extra...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return err == nil, out.String()
}

// TestPlanRule9ListsEveryFlaggedHeading holds Global Constraints rule 9 to
// its own sentence, "This list is the headings": every task whose heading
// carries `SECURITY-SENSITIVE, independent opus review` is named in the rule.
// The 0.8.5 merge of `console` into `main` (PR #611) had to fold main's Task
// 2.4 into the console's rewritten rule by hand, and no test failed when it
// was dropped; this one does, for 2.4 and for any later flagged task.
func TestPlanRule9ListsEveryFlaggedHeading(t *testing.T) {
	plan := readDoc(t, "docs/plans/2026-10-15-desktop-console.md")
	rule := regexp.MustCompile(`(?m)^9\. \*\*Security-sensitive tasks.*$`).FindString(plan)
	if rule == "" {
		t.Fatal("the plan has no Global Constraints rule 9")
	}
	flagged := regexp.MustCompile(`(?m)^### Task (\d+\.\d+[a-z]?): .*SECURITY-SENSITIVE, independent opus review`).FindAllStringSubmatch(plan, -1)
	if len(flagged) == 0 {
		t.Fatal("the plan has no task heading flagged SECURITY-SENSITIVE: the pattern no longer matches the headings")
	}
	for _, m := range flagged {
		id := m[1]
		if !regexp.MustCompile(`(^|[^0-9.])` + regexp.QuoteMeta(id) + `([^0-9]|$)`).MatchString(rule) {
			t.Errorf("Task %s's heading carries SECURITY-SENSITIVE but rule 9's list does not name it: the rule says the list is the headings", id)
		}
	}
}

// TestDocsDesktopConsoleContractCitesRunsStatsBySymbol: the contract's
// `runs stats --json` section (6.10) cites the code it describes by SYMBOL, not
// by line. The line numbers it used to carry (`runs_stats.go:69`, `:467`,
// `:181`) each pointed at something else as soon as anything was inserted above
// them — the one this test was written for went stale inside the same pull
// request that added the section, when a review fix moved `writeUserStats` from
// :467 to :493 — and a console author who follows a line number that now lands
// on an unrelated statement reads the wrong shape off it. The symbols are what
// the console reads the document for, so the section names them and a line
// number cannot come back.
//
// Mutation (verified, run and restored): put `runs_stats.go:467` back beside
// the section's `writeUserStats` — this fails naming the line citation.
func TestDocsDesktopConsoleContractCitesRunsStatsBySymbol(t *testing.T) {
	const path = "console/docs/json-contract.md"
	doc := readDoc(t, path)
	start := strings.Index(doc, "### 6.10 `fugaro runs stats --json`")
	end := strings.Index(doc, "## 7. The CLI gaps")
	if start < 0 || end <= start {
		t.Fatalf("%s's runs stats section was renamed or reordered; update this test's markers", path)
	}
	section := doc[start:end]
	// The three symbols the section describes, each of which exists in the file
	// it names (read from the source, so a rename cannot leave the contract
	// pointing at a function that is gone).
	src := readDoc(t, "internal/cli/runs_stats.go")
	for _, sym := range []string{"newRunsStatsCmd", "runRunsStats", "userStatsDoc", "writeUserStats"} {
		if !strings.Contains(section, "`"+sym+"`") {
			t.Errorf("%s's runs stats section does not name `%s`: the console reads this document to learn the shape, and a symbol is what stays true", path, sym)
		}
		if !strings.Contains(src, "func "+sym+"(") && !strings.Contains(src, "type "+sym+" ") {
			t.Errorf("%s names `%s`, and internal/cli/runs_stats.go no longer declares it", path, sym)
		}
	}
	// No line citation in the section: one that is right today is wrong at the
	// next insertion above it, and the console cannot tell the two apart.
	if m := regexp.MustCompile(`runs_stats\.go:\d+`).FindString(section); m != "" {
		t.Errorf("%s's runs stats section cites %s instead of a symbol: a line number here rots the moment anything is inserted above the symbol it means", path, m)
	}
}

// TestDocsDesktopConsoleContractCitesBySymbolNotLine: a file:LINE citation in
// the contract is right the day it is written and wrong at the next insertion
// above the symbol it means, and the console cannot tell the two apart. Section
// 6.10 was the first section held to symbol names only; this holds the whole
// document to it, after a round of line pins had already gone stale
// (json.go:97, json.go:91-92, runview.go:57, runview.go:109, queued.go:17 —
// each pointed at an unrelated line by the time it was checked).
//
// Mutation (verified, run and restored): put `json.go:97` back beside a
// `JSONDoc` citation — this fails naming the citation.
func TestDocsDesktopConsoleContractCitesBySymbolNotLine(t *testing.T) {
	const path = "console/docs/json-contract.md"
	doc := readDoc(t, path)
	if ms := regexp.MustCompile(`[A-Za-z0-9_./-]+\.go:[0-9]+`).FindAllString(doc, -1); len(ms) > 0 {
		t.Errorf("%s cites %s: name the symbol (the function, type or constant) instead, so the anchor survives an insertion above it", path, strings.Join(ms, ", "))
	}
}

// TestDocsDesktopConsoleContractHeadingsStandAlone: each of the contract's
// section headings is a heading and nothing else.
//
// The same citation pass joined eleven of them to the body paragraph below —
// `## 3`, `## 4` and `### 6.1` through `### 6.9`, each of which had been a
// heading, a blank line and then the body. Markdown then renders the whole
// command description as heading text and the section anchors and any table of
// contents move with it, and no test noticed: the contract is read as bytes,
// and every other assertion here looks for a phrase that a merged heading still
// contains.
//
// Two rules, because one alone leaves a hole. A heading is followed by a blank
// line — that catches every one of those eleven, since each had body text on the
// next line. And a heading carries none of the `**Command:**` / `**Document:**`
// markers that begin the paragraph under it — that catches the same join made
// with the blank line kept, where the body sits on the heading line itself.
//
// Mutation (verified, run and restored): re-join `## 3.` with the `**Command:**`
// line that follows it — this fails on that line, twice, once per rule.
func TestDocsDesktopConsoleContractHeadingsStandAlone(t *testing.T) {
	const path = "console/docs/json-contract.md"
	lines := strings.Split(readDoc(t, path), "\n")
	for i, line := range lines {
		body := strings.TrimLeft(line, "#")
		if !strings.HasPrefix(line, "#") || !strings.HasPrefix(body, " ") {
			continue // not an ATX heading
		}
		for _, marker := range []string{"**Command:**", "**Document:**"} {
			if strings.Contains(body, marker) {
				t.Errorf("%s:%d: heading carries body text (%s): %q — the body paragraph starts on its own line", path, i+1, marker, line)
			}
		}
		if i+1 < len(lines) && strings.TrimSpace(lines[i+1]) != "" {
			t.Errorf("%s:%d: heading is not followed by a blank line: %q — body text joined onto it?", path, i+1, line)
		}
	}
}

// TestDocsDesktopConsoleContractStatesTheAccountAxis: the `account` row of both
// run shapes used to say the field could not appear on a project job at all —
// "in phase 3 **never**, because a profile may not mount a Claude OAuth token"
// — and internal/watch/json.go's own comment carried the same claim. The
// release this contract documents made it false: a profile lists
// claude_accounts, the deployer builds one job per (profile, provider,
// account), and a row's account is the axis the run's job name was computed
// from, which is exactly what a console shows a screen from. The stale sentence
// is the dangerous half — a console that believes the key can never appear never
// renders one — so it is banned by name in both files, and the row is held to
// the axis the code computes rather than to a hand-kept paraphrase.
//
// Mutation (verified, run and restored): restore the sentence to either file —
// this fails naming it.
func TestDocsDesktopConsoleContractStatesTheAccountAxis(t *testing.T) {
	const path = "console/docs/json-contract.md"
	const code = "internal/watch/json.go"
	doc := readDoc(t, path)
	for _, stale := range []string{
		"in phase 3 **never**",
		"a profile may not mount a Claude OAuth token",
		"absent on every phase-3 project job",
		"which in phase 3 is every project job",
	} {
		for _, where := range []struct{ path, text string }{{path, doc}, {code, readDoc(t, code)}} {
			if strings.Contains(where.text, stale) {
				t.Errorf("%s still says %q: a project job's row can carry an account, and a console reading either document would never render one", where.path, stale)
			}
		}
	}
	// What the code computes: a profile that lists the default account and one
	// named account has a job per entry (config.ProjectJobAccounts), and the
	// named account's job is the one whose row carries an account
	// (runner.ProjectJobOfPolicy, which internal/cli/project_job.go's
	// projectJobIndex.ref puts on the row). The empty string is the profile's
	// no-account job, which is why the key is omitempty and absent there.
	p := &projectpolicy.Policy{Profiles: map[string]projectpolicy.Profile{
		"standard": {JobProfile: config.JobProfile{Provider: "github",
			ClaudeAccounts: []string{config.DefaultClaudeAccount, "work"}}},
	}}
	if _, _, account, ok := runner.ProjectJobOfPolicy(p, gcp.ProjectJobName("standard", "github", "work")); !ok || account != "work" {
		t.Fatalf("ProjectJobOfPolicy(standard, github, work) = (%q, %v): a named account's job no longer carries its name on the row", account, ok)
	}
	if _, _, account, ok := runner.ProjectJobOfPolicy(p, gcp.ProjectJobName("standard", "github", "")); !ok || account != "" {
		t.Fatalf("ProjectJobOfPolicy(standard, github, no account) = (%q, %v): the profile's no-account job no longer reads as the empty axis", account, ok)
	}
	if got := config.ProjectJobAccounts(config.JobProfile{ClaudeAccounts: []string{config.DefaultClaudeAccount, "work"}}); len(got) != 2 {
		t.Fatalf("ProjectJobAccounts(default, work) = %v, want the two entries the row's presence rule is about", got)
	}
	// And the row says it, naming the code that decides it: the symbols are in
	// the row rather than only in this test, so a reader of the contract can
	// check the claim.
	for _, want := range []string{
		"**one job per account**",
		"`config.ProjectJobAccounts`",
		"`config.ProjectJobTokenAccount`",
		"the profile's no-account job",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s's `account` row does not say %q: the axis is runner.ProjectJobOfPolicy's answer over config.ProjectJobAccounts, and the row is what a console reads to know whether to render one", path, want)
		}
	}
}

// TestDocsDesktopConsoleNormalFixtureMatchesTheGoTypes: console/test/fixtures/
// normal.ndjson is the shape of one real 0.8.6 `watch --json` document with
// every value invented, so its shape is the one thing in it that must stay the
// CLI's. Every document in it is decoded into the real watch.JSONDoc with
// DisallowUnknownFields (an unknown key, or a value of the wrong JSON type,
// fails), and then every level is walked against the struct: each key present
// is a tag of that struct, in the struct's own order (the CLI marshals in field
// order), and each tag without omitempty is present. Mutations: an unknown key
// added to the fixture; a required key dropped; a key moved; a number made a
// string.
//
// The keys the fixture may *lack* are `leased_usd` and `max_parallel`, and the
// reason is the fixture's own subject: its header says the recording is 0.8.6's,
// so a required key a later release added cannot be in it, and this file is
// deliberately the old document the console must still read -- updating it to
// the current shape would delete what it exists to prove. Lack is the only
// allowance: a fixture that *carried* either key would still be walked against
// the struct like every other key.
func TestDocsDesktopConsoleNormalFixtureMatchesTheGoTypes(t *testing.T) {
	lines := strings.Split(strings.TrimSpace(readDoc(t, "console/test/fixtures/normal.ndjson")), "\n")
	if len(lines) < 2 {
		t.Fatalf("normal.ndjson holds %d lines, want a header and at least one record", len(lines))
	}
	var docs int
	for i, line := range lines[1:] {
		var rec struct {
			Document json.RawMessage `json:"document"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("record %d: %v", i+1, err)
		}
		if rec.Document == nil {
			continue
		}
		docs++
		dec := json.NewDecoder(bytes.NewReader(rec.Document))
		dec.DisallowUnknownFields()
		var doc watch.JSONDoc
		if err := dec.Decode(&doc); err != nil {
			t.Fatalf("record %d does not decode into watch.JSONDoc: %v", i+1, err)
		}
		checkJSONShape(t, fmt.Sprintf("record %d", i+1), rec.Document, reflect.TypeOf(watch.JSONDoc{}), mayLackOldRecording)
	}
	if docs == 0 {
		t.Fatal("normal.ndjson holds no document")
	}
}

// orderedJSONKeys returns an object's keys in the order they were written.
func orderedJSONKeys(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("not an object: %s", raw)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

// mayLackOldRecording are the keys a document recorded before the release that
// added them may leave out: see TestDocsDesktopConsoleNormalFixtureMatchesTheGoTypes.
var mayLackOldRecording = map[string]bool{"leased_usd": true, "max_parallel": true}

// checkJSONShape walks one JSON value against a struct type: keys in struct
// order, required keys present (bar those in mayLack), recursing into nested
// structs and slices.
func checkJSONShape(t *testing.T, where string, raw json.RawMessage, rt reflect.Type, mayLack map[string]bool) {
	t.Helper()
	for rt.Kind() == reflect.Ptr {
		if string(raw) == "null" {
			return
		}
		rt = rt.Elem()
	}
	switch rt.Kind() {
	case reflect.Slice:
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			t.Errorf("%s: want an array: %v", where, err)
			return
		}
		for i, item := range items {
			checkJSONShape(t, fmt.Sprintf("%s[%d]", where, i), item, rt.Elem(), mayLack)
		}
	case reflect.Struct:
		var members map[string]json.RawMessage
		if err := json.Unmarshal(raw, &members); err != nil {
			t.Errorf("%s: want an object: %v", where, err)
			return
		}
		type field struct {
			name     string
			required bool
			typ      reflect.Type
		}
		var fields []field
		for i := 0; i < rt.NumField(); i++ {
			name, opts, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
			fields = append(fields, field{name, !strings.Contains(opts, "omitempty"), rt.Field(i).Type})
		}
		var want []string
		for _, f := range fields {
			if _, ok := members[f.name]; ok {
				want = append(want, f.name)
			} else if f.required && !mayLack[f.name] {
				t.Errorf("%s: required key %q is missing (internal/watch/json.go has it without omitempty)", where, f.name)
			}
		}
		if got := orderedJSONKeys(t, raw); !slices.Equal(got, want) {
			t.Errorf("%s: keys %v, want the struct's order %v", where, got, want)
		}
		for _, f := range fields {
			if v, ok := members[f.name]; ok {
				checkJSONShape(t, where+"."+f.name, v, f.typ, mayLack)
			}
		}
	}
}

// The scrub's vocabularies against the Go source (owner review of PR #643,
// item 1: "add a drift test comparing the scrub's vocabularies with the Go
// source's constants so they cannot rot silently").
//
// It lives in this file rather than one of its own because Task 0.3's boundary
// test allows exactly ONE Go file to mention a path under console/, and this is
// it: a second file would have to be argued onto that list, while this one
// already reads console/ documents the same way -- as bytes, importing nothing
// from them. It reads console/tools/scrub-fixture/scrub.ts for the same reason
// it reads console/docs/json-contract.md: the console's copy of a Go decision is
// checked against the Go source, not against a hand-kept list on either side.
//
// `console/tools/scrub-fixture/scrub.ts` copies a value only when it is in a
// fixed list and refuses the rest -- deny by default, so the recorder cannot
// leak a field it does not know. The failure that review found is the other side
// of that rule: a list that is too SHORT does not leak, it makes the recorder
// unusable, because the ordinary run it was reading is refused. The lists were
// derived by hand from the Go source once and had already drifted (`bootstrap`,
// `review_first`, `finalize` and `vertex` were all missing, and `warn` was a
// mode no CLI writes).

// scrubTS is the file that holds the vocabularies.
const scrubTS = "console/tools/scrub-fixture/scrub.ts"

// quoted returns every double-quoted Go string literal inside s, in order.
func quoted(s string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

// tsQuoted is quoted for the other language: every single-quoted string literal
// inside s, in order.
func tsQuoted(s string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`'([^']*)'`).FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

// collect returns the first capture of every match of re, in order. It says
// nothing about finding none: most files in a package do not carry the shape
// being looked for, and the caller asserts on the package's total.
func collect(src, re string) []string {
	var out []string
	for _, m := range regexp.MustCompile(re).FindAllStringSubmatch(src, -1) {
		out = append(out, m[1])
	}
	return out
}

// sourceValues is collect for a scan that must find something: a pattern that
// silently matches nothing would compare two empty lists and pass.
func sourceValues(t *testing.T, where, src, re string) []string {
	t.Helper()
	out := collect(src, re)
	if len(out) == 0 {
		t.Fatalf("%s: the pattern %q matched nothing; the source has moved and this test is no longer reading it", where, re)
	}
	return out
}

// runnerSources returns the non-test .go files of a package directory.
func runnerSources(t *testing.T, dir string) []string {
	t.Helper()
	paths, err := filepath.Glob("../../" + dir + "/*.go")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range paths {
		if !strings.HasSuffix(p, "_test.go") {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		t.Fatalf("no source files under %s", dir)
	}
	return out
}

// tsList returns the single-quoted strings of one list in scrub.ts: `key: [...]`
// for a VOCAB entry, or `NAME = [...]` for an exported list.
func tsList(t *testing.T, scrub, pattern, what string) []string {
	t.Helper()
	m := regexp.MustCompile(`(?s)` + pattern).FindStringSubmatch(scrub)
	if m == nil {
		t.Fatalf("%s: %s is not in the file in the form this test reads", scrubTS, what)
	}
	got := tsQuoted(m[1])
	slices.Sort(got)
	if len(got) == 0 {
		t.Fatalf("%s: %s parsed as an empty list", scrubTS, what)
	}
	return got
}

// sameSet fails with both lists when they differ.
func sameSet(t *testing.T, what string, want, got []string) {
	t.Helper()
	want, got = slices.Clone(want), slices.Clone(got)
	slices.Sort(want)
	slices.Sort(got)
	want, got = slices.Compact(want), slices.Compact(got)
	if !slices.Equal(want, got) {
		t.Errorf("%s: the Go source writes %v, the scrub knows %v", what, want, got)
	}
}

// without returns values with every occurrence of drop removed.
func without(values []string, drop string) []string {
	var out []string
	for _, v := range values {
		if v != drop {
			out = append(out, v)
		}
	}
	return out
}

// TestDocsDesktopConsoleScrubVocabularyMatchesTheGoSource compares every list
// the scrub uses to copy a CLI value with the constants the Go source writes it
// from. A value on one side and not the other is a refusal waiting to happen
// (too short) or a pass-through (too long); both fail here.
func TestDocsDesktopConsoleScrubVocabularyMatchesTheGoSource(t *testing.T) {
	scrub := readDoc(t, scrubTS)

	// The stages of runs[].stage, which is the run's REGISTRY entry and nothing
	// else: internal/watch/build.go writes dash(e.Stage) into the row, and that
	// entry takes a stage from exactly two places -- the literal it is created
	// with (budget_session.go's registryEntry) and beginBudgetStage, whose only
	// caller is r.stage, so its vocabulary is the first argument of r.stage's
	// call sites. The runner's other stage strings are other documents: the
	// record's own stage (r.rec.Stage = "render", "writeback", "check",
	// "finalize") goes to result.json, and provisionStage names a timing row. A
	// scrub that knew them would pass a value runs[].stage can never carry, so
	// the scan reads the entry and not the package's every stage string.
	budgetSession := readDoc(t, "internal/runner/budget_session.go")
	stages := sourceValues(t, "internal/runner/budget_session.go", budgetSession, `Stage: "([a-z_]+)"`)
	var budgetStageCalls []string
	for _, path := range runnerSources(t, "internal/runner") {
		src := readDoc(t, strings.TrimPrefix(path, "../../"))
		stages = append(stages, collect(src, `\.stage\(ctx, "([a-z_]+)"`)...)
		budgetStageCalls = append(budgetStageCalls, collect(src, `\.beginBudgetStage\((\w+),`)...)
	}
	if len(stages) < 5 {
		t.Fatalf("internal/runner yielded %d stages (%v); the scans are no longer reading the source", len(stages), stages)
	}
	// Those names are the vocabulary only while r.stage is the one caller of
	// beginBudgetStage and hands it its own name argument: a second call site,
	// or a stage the entry is given some other way, and the list is short.
	if len(budgetStageCalls) != 1 || budgetStageCalls[0] != "name" {
		t.Fatalf("internal/runner calls beginBudgetStage(%v, ...); this test reads the stage names off r.stage's call sites because that call is what puts them in the registry entry", budgetStageCalls)
	}
	// A runs-bucket row with no registry entry yet carries watch's own "queued".
	stages = append(stages, sourceValues(t, "internal/watch/queued.go",
		readDoc(t, "internal/watch/queued.go"), `Stage: "([a-z_]+)"`)...)
	sameSet(t, "stage", stages, tsList(t, scrub, `\bstage: \[([^\]]*)\]`, "VOCAB.stage"))

	// The auth values: the config's allowed set, which is the one a run is
	// launched with, plus the dash the wall view writes for an empty one. The
	// set is config.AgentAuths in defaults.go — validate.go's own check and the
	// run's account gate both read it (config.KnownAgentAuth), so it is the one
	// list, and this scan follows it there rather than reading a copy out of the
	// validator's call site.
	auth := sourceValues(t, "internal/config/defaults.go", readDoc(t, "internal/config/defaults.go"),
		`AgentAuths = \[\]string\{([^}]*)\}`)
	allowed := quoted(auth[0])
	if len(allowed) == 0 {
		t.Fatalf("internal/config/validate.go: the allowed-auth set parsed as empty")
	}
	sameSet(t, "auth", append(allowed, "-"), tsList(t, scrub, `\bauth: \[([^\]]*)\]`, "VOCAB.auth"))

	// The budget mode.
	sameSet(t, "mode", sourceValues(t, "internal/budget/model.go", readDoc(t, "internal/budget/model.go"),
		`Mode\w+ = "([a-z]+)"`), tsList(t, scrub, `\bmode: \[([^\]]*)\]`, "VOCAB.mode"))

	// The connection reasons the CLI writes itself: internal/watch/state.go sets
	// one beside each kind it invents -- "access refused" for a permission
	// error, "connecting" before the first event. The third branch is the
	// transport's own error text, one-lined and clipped there, so the scrub
	// cannot have a vocabulary for it; only these two are compared.
	sameSet(t, "connection reason", sourceValues(t, "internal/watch/state.go", readDoc(t, "internal/watch/state.go"),
		`c\.Kind, c\.Reason = \w+, "([^"]*)"`), tsList(t, scrub, `CONNECTION_REASONS = \[([^\]]*)\]`, "CONNECTION_REASONS"))

	// The verify kinds: the three `fugaro verify` can run. Held in variables
	// rather than passed straight to sameSet because the head grammar below is
	// built from them.
	verifyGo := readDoc(t, "internal/verify/verify.go")
	kinds := sourceValues(t, "internal/verify/verify.go", verifyGo, `Kind\w+\s+Kind = "([a-z]+)"`)
	sameSet(t, "verify kind", kinds, tsList(t, scrub, `VERIFY_KINDS = \[([^\]]*)\]`, "VERIFY_KINDS"))

	// The two status words `Record.Summary` picks between, the flag it appends
	// to the kind, the one warning sentence it writes, and the note it appends
	// to a run that found the tree dirty. The last two are the CLI's own text
	// sitting in the middle of the line: the scrub spells them out, so a second
	// warning or a reworded note fails here rather than making the recorder
	// refuse an ordinary run.
	statuses := sourceValues(t, "internal/verify/verify.go", verifyGo, `status (?::=|=) "([A-Za-z]+)"`)
	sameSet(t, "verify status", statuses, tsList(t, scrub, `VERIFY_STATUSES = \[([^\]]*)\]`, "VERIFY_STATUSES"))
	rerun := sourceValues(t, "internal/verify/verify.go", verifyGo, `kind \+= " ([^"]+)"`)
	sameSet(t, "verify rerun flag", rerun, []string{tsConst(t, scrub, "VERIFY_RERUN_FLAG")})
	sameSet(t, "verify warning", sourceValues(t, "internal/verify/verify.go", verifyGo,
		`rec\.Warning = "([^"]*)"`), []string{tsConst(t, scrub, "VERIFY_WARNING_PREFIX")})
	sameSet(t, "verify clean-tree note", sourceValues(t, "internal/verify/verify.go", verifyGo,
		"s \\+= \"; ([^\"]*)\"\n\t}\n\treturn s"), []string{tsConst(t, scrub, "VERIFY_CLEAN_TREE")})

	// The clip internal/runner puts on the summary before the document carries
	// it: `verifySummaryRunes`, which is also what scrub.ts calls
	// VERIFY_CLIP_RUNES. The scrub needs the number because a clipped line is a
	// PREFIX of the writer's and its matchers have to accept one, so a clip that
	// moved in the registry without moving here would leave the scrub refusing an
	// ordinary recording -- or accepting a cut beyond the one the writer makes.
	sameNumber(t, "verify summary clip (runes)", sourceNumber(t, "internal/runner/budget_session.go",
		readDoc(t, "internal/runner/budget_session.go"), `const verifySummaryRunes = (\d+)`),
		tsNumber(t, scrub, "VERIFY_CLIP_RUNES"))

	// The report globs the warning sentence carries and nowhere else in the
	// document: config.DefaultReports, which internal/config/defaults.go
	// installs as `commands.reports` and which internal/verify/verify.go joins
	// into `no JUnit reports found under ...`. They are the vocabulary the
	// fixture scan pins the glob slot to (VERIFY_REPORT_GLOBS), so a change to
	// the defaults that the scrub did not follow would make the scan refuse a
	// recording the CLI itself wrote. The list is read at its own declaration
	// (`defaultReports`), because the exported name is a function answering a
	// copy of it.
	sameSet(t, "verify report glob", quoted(sourceValues(t, "internal/config/defaults.go",
		readDoc(t, "internal/config/defaults.go"), `defaultReports = \[\]string\{([^}]*)\}`)[0]),
		tsList(t, scrub, `VERIFY_REPORT_GLOBS = \[([^\]]*)\]`, "VERIFY_REPORT_GLOBS"))

	// The connection states, the healths and the deadlines: three inline tables
	// in internal/watch/json.go, keyed by the constants they render.
	jsonGo := readDoc(t, "internal/watch/json.go")
	sameSet(t, "connection state", sourceValues(t, "internal/watch/json.go", jsonGo, `Conn\w+: "([a-z]+)"`),
		tsList(t, scrub, `\bconnectionState: \[([^\]]*)\]`, "VOCAB.connectionState"))
	sameSet(t, "health", sourceValues(t, "internal/watch/json.go", jsonGo, `Health\w+: "([a-z]+)"`),
		tsList(t, scrub, `\bhealth: \[([^\]]*)\]`, "VOCAB.health"))
	sameSet(t, "deadline", sourceValues(t, "internal/watch/json.go", jsonGo, `Deadline\w+: "([a-z]+)"`),
		tsList(t, scrub, `\bdeadline: \[([^\]]*)\]`, "VOCAB.deadline"))

	// The statuses and outcomes of a `finished` row. Both are runstore's own
	// constants; `running` is left out because a finished row is read from a
	// result.json, which only exists once the run has stopped (internal/watch/
	// finished.go), so no document ever carries it.
	sameSet(t, "status", without(sourceValues(t, "internal/runstore/runstore.go",
		readDoc(t, "internal/runstore/runstore.go"), `Status\w+\s+Status = "([a-z_]+)"`), "running"),
		tsList(t, scrub, `\bstatus: \[([^\]]*)\]`, "VOCAB.status"))
	outcomes := sourceValues(t, "internal/runstore/runstore.go", readDoc(t, "internal/runstore/runstore.go"),
		`Outcome\w+\s+Outcome = "([a-z]+)"`)
	outcomes = append(outcomes, sourceValues(t, "internal/runstore/artifact.go",
		readDoc(t, "internal/runstore/artifact.go"), `Outcome\w+\s+Outcome = "([a-z]+)"`)...)
	sameSet(t, "outcome", outcomes, tsList(t, scrub, `\boutcome: \[([^\]]*)\]`, "VOCAB.outcome"))

	// The verify line's head grammar, which is neither a list nor prose: the
	// pattern is read out of scrub.ts as the expression it is, its `${...}`
	// interpolations are filled with the values read above, and the result is
	// compiled here and tried against lines built from verify.go's own format
	// string -- carrying the exit code internal/procgroup/procgroup.go leaves
	// for a process it killed, which is what its own timeout does.
	//
	// A literal dropped from the pattern, an exit code narrowed to `\d+`, or a
	// pattern that spells the kinds out instead of interpolating the list this
	// test compares, fails here. The comment above the pattern in the file
	// spells the same literals out, and cannot stand in for it: a substring
	// check over the whole file passes on the comment alone.
	head := verifyHeadPattern(t, scrub, kinds, statuses, rerun[0])
	headRE, err := regexp.Compile(head)
	if err != nil {
		t.Fatalf("%s: VERIFY_HEAD is not a pattern this test can compile: %v (%s)", scrubTS, err, head)
	}
	format := sourceValues(t, "internal/verify/verify.go", verifyGo, `"(fugaro verify %s #%d: %s \(exit %d)"`)[0]
	killed := sourceValues(t, "internal/procgroup/procgroup.go",
		readDoc(t, "internal/procgroup/procgroup.go"), `code := (-?\d+)`)[0]
	var lines []string
	for _, kind := range kinds {
		for _, status := range statuses {
			for _, code := range []string{"0", killed} {
				lines = append(lines, verifySummaryLine(format, kind, "3", status, code))
			}
		}
		lines = append(lines, verifySummaryLine(format, kind+" "+rerun[0], "5", statuses[0], killed))
	}
	for _, line := range lines {
		if !headRE.MatchString(line) {
			t.Errorf("%s: VERIFY_HEAD (%s) does not match the line verify.go writes: %q", scrubTS, head, line)
		}
	}

	// The tail, segment by segment and in order. The head was held above and the
	// tail's literals were held to nothing, which is the rot this test exists to
	// prevent: scrub.ts spells `Record.Summary`'s tail out part by part, so a
	// segment ADDED to verify.go -- a `; N skipped` for the Skipped count the
	// summary does not print today, say -- would leave the scrub refusing an
	// ordinary recording while this test stayed green.
	//
	// Both sides are rendered to one string per segment, from their own sources:
	// a literal as written (with the writer's `%d` as the digits marker), the
	// test-name joins as the names marker, and `r.Warning` -- which verify.go
	// builds as the sentence below plus the repository's reports -- as that
	// sentence followed by the globs marker. Nothing is retyped here, so a
	// reworded, dropped, reordered or added segment fails on the two renderings.
	warning := sourceValues(t, "internal/verify/verify.go", verifyGo, `rec\.Warning = "([^"]*)"`)[0]
	goTail := summaryTail(t, verifyGo, warning+"\x02globs")
	tsTail := verifyTailRendered(t, scrub)
	if len(goTail) == 0 || len(tsTail) == 0 {
		t.Fatalf("%s: the tail scan read nothing (verify.go %q, scrub.ts %q); the sources have moved", scrubTS, goTail, tsTail)
	}
	if !slices.Equal(goTail, tsTail) {
		t.Errorf("%s: Record.Summary appends %q, the scrub's VERIFY_TAIL renders %q: a tail segment added, reworded, dropped or reordered in verify.go has to move the scrub with it", scrubTS, goTail, tsTail)
	}

	// The counts are the one tail segment a KIND decides on, and the scrub's
	// grammar encodes that decision twice: VERIFY_TAIL's first group is the
	// counts, sliced off entirely for a build or a lint (VERIFY_TEST_TAIL_FROM)
	// and required WHOLE for a test (VERIFY_TEST_GROUPS_REQUIRED), because
	// verify.go writes them under a KindTest condition and the 120-rune clip
	// lands far after them. summaryTail above skips `if` lines -- a condition
	// names what it guards without writing it -- so the condition itself is read
	// here: were the counts moved to another kind, or written unconditionally,
	// the scrub would refuse an ordinary recording of that kind while every
	// rendering above stayed equal.
	counts := sourceValues(t, "internal/verify/verify.go", verifyGo,
		`if r\.Kind == (\w+) \{\s*s \+= fmt\.Sprintf\(", %d tests, %d failures"`)
	if len(counts) != 1 || counts[0] != "KindTest" {
		t.Errorf("internal/verify/verify.go writes the counts under r.Kind == %v; >>%s<< slices them off for every kind but a test and requires them whole for a test, so the two have to agree", counts, scrubTS)
	}
}

// tailDigits, tailNames and tailGlobs mark, in the two renderings compared
// above, the places a segment is not literal text: a `%d`, the test-name lists
// verify.go joins with ", ", and the report globs inside its warning.
const (
	tailDigits = "\x03"
	tailNames  = "\x02names"
	tailGlobs  = "\x02globs"
)

// summaryTail renders the statements Record.Summary appends, in order, one string
// each: literal text as written (`%d` becoming tailDigits), every
// strings.Join(...) as tailNames, and r.Warning as `warning`. The head's own
// fmt.Sprintf is excluded -- VERIFY_HEAD is compared with it -- by starting after
// the r.ExitCode argument that closes it.
func summaryTail(t *testing.T, verifyGo, warning string) []string {
	t.Helper()
	fn := regexp.MustCompile(`(?s)func \(r Record\) Summary\(\) string \{(.*?)\n\}`).FindStringSubmatch(verifyGo)
	if fn == nil {
		t.Fatalf("internal/verify/verify.go: Record.Summary is not a function this test can read")
	}
	tail := fn[1]
	at := strings.Index(tail, "r.ExitCode)")
	if at < 0 {
		t.Fatalf("internal/verify/verify.go: Record.Summary no longer builds its head from r.ExitCode; this test reads the statements after it")
	}
	tail = tail[at+len("r.ExitCode)"):]
	var out []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	for i := 0; i < len(tail); {
		switch {
		case strings.HasPrefix(tail[i:], "if "):
			// A condition names what it guards without writing it: `if r.Warning
			// != ""` must not count as the warning's own segment.
			nl := strings.IndexByte(tail[i:], '\n')
			if nl < 0 {
				i = len(tail)
				break
			}
			i += nl
		case strings.HasPrefix(tail[i:], "strings.Join("):
			// The join's separator is ", ", which is the slot's own internal
			// separator in scrub.ts, not a literal of the tail.
			end := strings.Index(tail[i:], ")")
			if end < 0 {
				t.Fatalf("internal/verify/verify.go: a strings.Join call in Record.Summary is not closed")
			}
			b.WriteString(tailNames)
			i += end + 1
		case strings.HasPrefix(tail[i:], "r.Warning"):
			b.WriteString(warning)
			i += len("r.Warning")
		case tail[i] == '"':
			end := strings.Index(tail[i+1:], `"`)
			if end < 0 {
				t.Fatalf("internal/verify/verify.go: an unterminated literal in Record.Summary")
			}
			b.WriteString(strings.ReplaceAll(tail[i+1:i+1+end], "%d", tailDigits))
			i += end + 2
		case tail[i] == '\n':
			flush()
			i++
		default:
			i++
		}
	}
	flush()
	return out
}

// verifyTailRendered renders scrub.ts's VERIFY_TAIL the way summaryTail renders
// verify.go's: one string per segment, `text` parts as the value they spell out
// (a quoted literal, or a constant this test already compares), `digits` as
// tailDigits and every free slot as its own marker.
func verifyTailRendered(t *testing.T, scrub string) []string {
	t.Helper()
	block := regexp.MustCompile(`(?s)const VERIFY_TAIL[^=]*= \[(.*?)\n\]`).FindStringSubmatch(scrub)
	if block == nil {
		t.Fatalf("%s: VERIFY_TAIL is not a list this test can read", scrubTS)
	}
	partRE := regexp.MustCompile(`text: ([^}]*?)\}|digits: true|free: '([a-z]+)'`)
	var out []string
	for _, line := range strings.Split(block[1], "\n") {
		parts := partRE.FindAllStringSubmatch(line, -1)
		if len(parts) == 0 {
			continue
		}
		var b strings.Builder
		for _, part := range parts {
			switch {
			case part[2] != "":
				b.WriteString("\x02" + part[2])
			case part[1] != "":
				b.WriteString(tsTextValue(t, scrub, part[1]))
			default:
				b.WriteString(tailDigits)
			}
		}
		out = append(out, b.String())
	}
	return out
}

// tsTextValue resolves one `text:` expression of VERIFY_TAIL: each ` + `-joined
// part is a single-quoted literal or the name of a const this test reads out of
// the file. Anything else is a failure rather than a silent pass, so a part
// rewritten into an expression this test cannot follow cannot slip through.
func tsTextValue(t *testing.T, scrub, expr string) string {
	t.Helper()
	var b strings.Builder
	for _, part := range strings.Split(strings.TrimSpace(expr), "+") {
		part = strings.TrimSpace(part)
		if m := regexp.MustCompile(`^'([^']*)'$`).FindStringSubmatch(part); m != nil {
			b.WriteString(m[1])
			continue
		}
		if regexp.MustCompile(`^[A-Z][A-Z_]*$`).MatchString(part) {
			b.WriteString(tsConst(t, scrub, part))
			continue
		}
		t.Fatalf("%s: VERIFY_TAIL has a text part this test cannot read: %q", scrubTS, part)
	}
	return b.String()
}

// tsConst returns the single-quoted value of one `const NAME = '...'` in
// scrub.ts, so a literal the pattern is built from can be compared with the Go
// source's own.
func tsConst(t *testing.T, scrub, name string) string {
	t.Helper()
	m := regexp.MustCompile(`(?s)const ` + name + ` = '([^']*)'`).FindStringSubmatch(scrub)
	if m == nil {
		t.Fatalf("%s: %s is not a const holding one single-quoted string", scrubTS, name)
	}
	return m[1]
}

// sourceNumber returns the one integer a pattern captures, from a scan that
// must find it: a number the scrub copies from the Go source is worth comparing
// for the same reason a list is.
func sourceNumber(t *testing.T, where, src, re string) int {
	t.Helper()
	values := sourceValues(t, where, src, re)
	n, err := strconv.Atoi(values[0])
	if err != nil {
		t.Fatalf("%s: %q is not an integer: %v", where, values[0], err)
	}
	return n
}

// tsNumber returns the integer of one `const NAME = 123` in scrub.ts.
func tsNumber(t *testing.T, scrub, name string) int {
	t.Helper()
	m := regexp.MustCompile(`const ` + name + ` = (\d+)`).FindStringSubmatch(scrub)
	if m == nil {
		t.Fatalf("%s: %s is not a const holding one integer", scrubTS, name)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("%s: %s is not an integer: %v", scrubTS, name, err)
	}
	return n
}

// sameNumber fails with both numbers when they differ.
func sameNumber(t *testing.T, what string, want, got int) {
	t.Helper()
	if want != got {
		t.Errorf("%s: the Go source writes %d, the scrub knows %d", what, want, got)
	}
}

// verifyHeadPattern returns scrub.ts's VERIFY_HEAD as a pattern this test can
// compile: the template literal's escapes undone, and its `${...}`
// interpolations replaced by the values the Go source writes. An interpolation
// this test does not know, or a pattern that no longer uses the one holding the
// kinds or the statuses, is a failure: the pattern has to be built from the
// lists this test compares, not retyped beside them.
func verifyHeadPattern(t *testing.T, scrub string, kinds, statuses []string, rerun string) string {
	t.Helper()
	m := regexp.MustCompile("(?s)VERIFY_HEAD = new RegExp\\(\\s*`([^`]*)`").FindStringSubmatch(scrub)
	if m == nil {
		t.Fatalf("%s: VERIFY_HEAD is not a new RegExp(...) over one template literal; this test reads its source", scrubTS)
	}
	pattern := strings.ReplaceAll(m[1], `\\`, `\`)
	for _, sub := range []struct{ expr, value string }{
		{"${VERIFY_KINDS.join('|')}", "(" + strings.Join(kinds, "|") + ")"},
		{"${VERIFY_STATUSES.join('|')}", "(" + strings.Join(statuses, "|") + ")"},
		{"${VERIFY_RERUN_FLAG}", rerun},
	} {
		if !strings.Contains(pattern, sub.expr) {
			t.Fatalf("%s: VERIFY_HEAD no longer interpolates %s: the pattern must be built from the list this test compares, not retyped beside it", scrubTS, sub.expr)
		}
		pattern = strings.ReplaceAll(pattern, sub.expr, sub.value)
	}
	if strings.Contains(pattern, "${") {
		t.Fatalf("%s: VERIFY_HEAD interpolates something this test cannot fill: %s", scrubTS, pattern)
	}
	return pattern
}

// verifySummaryLine renders verify.go's own format string the way the writer
// does: `fugaro verify %s #%d: %s (exit %d` takes the kind, the record number,
// the status and the exit code, in that order.
func verifySummaryLine(format, kind, n, status, code string) string {
	for _, value := range []string{kind, n, status, code} {
		at := strings.Index(format, "%")
		if at < 0 {
			break
		}
		format = format[:at] + value + format[at+2:]
	}
	return format
}

// ---------------------------------------------------------------------------
// The Linux packaging change (2026-10-20).
//
// The owner's ruling of that date is "yes, Linux for 0.10.0, unsigned", and the
// workflow half of it cannot be applied by an agent at all: GitHub rejects a
// push that touches .github/workflows/**, so both changes are staged under
// docs/pending-workflows/ for the owner to copy in
// (docs/pending-workflows/README.md, and the `## console-linux.yml` and
// `## console-release-linux.yml` sections there).
//
// These tests read the staged text as bytes and as YAML. Two of the three
// claims are ordinary shape claims -- one secretless build job; a release
// pipeline whose third job builds and verifies the Linux artifacts and whose
// publish merges both legs behind one signed sums file. The third is the one a
// *replacement* file needs and a second file would not: that
// console-release-linux.yml, which supersedes console-release.yml, **leaves the
// macOS leg byte-for-byte where it was**. It is asserted by comparing the
// `release` job's parsed mapping between the two files and by requiring every
// `run:` body the old `publish` had to still be in the new one -- so a rewrite
// of the signing job cannot ride along inside a Linux change unseen.
//
// What none of this can check is the packaging itself: the pack, the AppImage
// runtime, dpkg-deb and the three fuse read-backs run on a runner that this
// container is not. Those are Check 59a's, and the staged files say so.
// ---------------------------------------------------------------------------

const (
	// consoleLinuxWorkflowPath is the staged build-only CI job, and
	// appliedConsoleLinuxPath is where the owner copies it. That copy-in removes
	// the staged copy -- the file is new, so it has nothing to be byte-identical
	// to -- which is why every reader below goes through consoleWorkflowText
	// rather than reading one of the two paths.
	consoleLinuxWorkflowPath = "docs/pending-workflows/console-linux.yml"
	appliedConsoleLinuxPath  = ".github/workflows/console-linux.yml"
	// consoleReleaseLinuxWorkflowPath is the staged replacement of
	// consoleReleaseWorkflowPath with the Linux legs added. Its copy-in copies
	// it over the staged console-release.yml and into .github/workflows/, then
	// removes it: after that, the text it produced is the one that runs, and it
	// is read from appliedConsoleReleasePath.
	consoleReleaseLinuxWorkflowPath = "docs/pending-workflows/console-release-linux.yml"
	// consoleReleaseFeedWorkflowPath is the staged replacement of
	// consoleReleaseWorkflowPath with the console's update feed
	// (electron-updater's latest-mac.yml) added. Its copy-in copies it over the
	// staged console-release.yml and into .github/workflows/, then removes it:
	// after that the text it produced is the one that runs, and it is read from
	// appliedConsoleReleasePath.
	consoleReleaseFeedWorkflowPath = "docs/pending-workflows/console-release-feed.yml"
	// consoleReleaseSymbolsWorkflowPath is the staged replacement of
	// consoleReleaseWorkflowPath with the crash reporter's symbol upload added
	// after the build (the owner's ruling of 2026-11-01; design section 7.4). Its
	// copy-in copies it over the staged console-release.yml and into
	// .github/workflows/, then removes it, exactly as the two supersessions
	// before it do.
	consoleReleaseSymbolsWorkflowPath = "docs/pending-workflows/console-release-symbols.yml"
	// consoleLinuxJobName is the job's key AND its display name: a job name
	// with an expression in it is a check name nobody can match, which is the
	// reason console-tests.yml's own comment gives for its static `console`.
	consoleLinuxJobName = "console-linux"
	// consoleReleaseLinuxJobName is the Linux job's key in the *merged* release
	// pipeline, console-release-linux.yml's third job -- a different job from
	// consoleLinuxJobName, which is the standalone build check's: this one packs
	// the release's artifacts and verifies the bytes that ship.
	consoleReleaseLinuxJobName = "linux"
)

// docExists reports whether a repository-relative path exists in this tree. The
// workflow tests ask it about the two homes a staged file has, so that they read
// the file the copy-in has left rather than a path they remember.
func docExists(t *testing.T, path string) bool {
	t.Helper()
	if _, err := os.Stat(filepath.Join("..", "..", path)); err == nil {
		return true
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return false
}

// consoleWorkflowText is a workflow file's text as this tree holds it, read from
// whichever of its two homes exists: the staged copy under
// docs/pending-workflows/ while the change is pending, or the applied copy
// under .github/workflows/ once the owner has copied it in and removed the
// staged one. This is what makes these tests read the *file* rather than one of
// its paths, so the same suite passes before and after the copy-in a human does
// by hand.
//
// A file that exists in both places has to be byte-identical: that is what
// makes a staged copy a copy of what runs rather than a second version of it,
// and it is the clause the staging directory's README rests on. It is checked
// here, once, for every reader.
//
// The path it read comes back with the text, because the messages in these
// tests name the file they are about: before the copy-in that is the staged
// path, and after it the applied one -- a failure has to point at the file a
// reader can open.
func consoleWorkflowText(t *testing.T, staged, applied string) (string, string) {
	t.Helper()
	stagedText, stagedErr := os.ReadFile(filepath.Join("..", "..", staged))
	appliedText, appliedErr := os.ReadFile(filepath.Join("..", "..", applied))
	switch {
	case stagedErr == nil && appliedErr == nil:
		if string(stagedText) != string(appliedText) {
			t.Fatalf("%s and %s differ: while both exist, the staged copy has to be the file that runs, byte for byte (docs/pending-workflows/README.md)", staged, applied)
		}
		return string(stagedText), staged
	case stagedErr == nil:
		return string(stagedText), staged
	case appliedErr == nil:
		return string(appliedText), applied
	default:
		t.Fatalf("neither %s nor %s exists (%v; %v): this test reads a workflow file, not a comment about one", staged, applied, stagedErr, appliedErr)
		return "", ""
	}
}

// consoleReplacementText is consoleWorkflowText for a file that *replaces*
// another one: the staged copy while the change is pending, the applied file
// once its copy-in has written it there. There is no byte-identity clause
// between the two, because until it is copied in the whole point of a
// replacement is that it differs from what runs -- the identity clause is
// consoleWorkflowText's, and it is asked of the file being replaced.
func consoleReplacementText(t *testing.T, staged, applied string) (string, string) {
	t.Helper()
	if docExists(t, staged) {
		return readDoc(t, staged), staged
	}
	return readDoc(t, applied), applied
}

// consoleFeedText is the console signing workflow's text as this tree holds the
// update feed change: the staged supersede file
// (consoleReleaseFeedWorkflowPath) while it is staged, and console-release.yml
// once the owner's copy-in has written it over that file and removed it. It
// goes through consoleReleaseEffectiveText first, so the byte-identity clause
// between the two copies of console-release.yml stays live in every state the
// copy-in passes through.
func consoleFeedText(t *testing.T) (string, string) {
	t.Helper()
	file, path := consoleReleaseEffectiveText(t)
	if docExists(t, consoleReleaseFeedWorkflowPath) {
		return readDoc(t, consoleReleaseFeedWorkflowPath), consoleReleaseFeedWorkflowPath
	}
	return file, path
}

// consoleReleaseEffectiveText is the console signing workflow's text as the
// repository is *heading*: the staged supersede file
// (consoleReleaseTagWorkflowPath) while it is staged, and console-release.yml
// once the owner's copy-in has written it over both copies of that file and
// removed the supersede file. It is what every test about the console's release
// stream reads, so the same suite is green before and after a copy-in only a
// human performs.
//
// The byte-identity clause stays live in every state: consoleWorkflowText is
// called first, so the two copies of console-release.yml are still held to be
// byte-identical while the supersede file is staged, and the supersede file is
// not a licence to edit either of them.
func consoleReleaseEffectiveText(t *testing.T) (string, string) {
	t.Helper()
	file, stagedPath := consoleWorkflowText(t, consoleReleaseWorkflowPath, appliedConsoleReleasePath)
	if docExists(t, consoleReleaseTagWorkflowPath) {
		return readDoc(t, consoleReleaseTagWorkflowPath), consoleReleaseTagWorkflowPath
	}
	return file, stagedPath
}

// parseConsoleWorkflow is readConsoleTestsWorkflow's parsing half: the typed
// shape (steps, permissions, matrix) and the raw mappings of the trigger block
// and of each job, where a key this file does not model (`needs:`,
// `environment:`, `paths:`) is still visible.
func parseConsoleWorkflow(t *testing.T, path, text string) (consoleWorkflow, map[string]any, map[string]map[string]any) {
	t.Helper()
	var wf consoleWorkflow
	if err := yaml.Unmarshal([]byte(text), &wf); err != nil {
		t.Fatalf("%s is not valid YAML: %v", path, err)
	}
	var raw struct {
		On   map[string]any            `yaml:"on"`
		Jobs map[string]map[string]any `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(text), &raw); err != nil {
		t.Fatalf("%s is not valid YAML: %v", path, err)
	}
	if len(raw.On) == 0 || len(wf.Jobs) == 0 {
		t.Fatalf("%s parsed with no triggers (%d) or no jobs (%d): these tests read the file, not a comment about it",
			path, len(raw.On), len(wf.Jobs))
	}
	return wf, raw.On, raw.Jobs
}

// readConsoleWorkflow is consoleWorkflowText and parseConsoleWorkflow in one
// call, for the two files whose staged copy the copy-in removes: it returns the
// text, the path it read it from, and both parsed shapes.
func readConsoleWorkflow(t *testing.T, staged, applied string) (string, string, consoleWorkflow, map[string]any, map[string]map[string]any) {
	t.Helper()
	text, path := consoleWorkflowText(t, staged, applied)
	wf, on, jobs := parseConsoleWorkflow(t, path, text)
	return text, path, wf, on, jobs
}

// readStagedWorkflow is a staged file read by its known path: the text, the
// typed shape and the raw mappings. It is for a file that is staged and stays
// staged -- readConsoleWorkflow is the one to use for a file a copy-in moves.
func readStagedWorkflow(t *testing.T, path string) (string, consoleWorkflow, map[string]any, map[string]map[string]any) {
	t.Helper()
	text := readDoc(t, path)
	wf, on, jobs := parseConsoleWorkflow(t, path, text)
	return text, wf, on, jobs
}

// renderedJob marshals one parsed job back to YAML. The per-job text is what
// the secretlessness assertions read: a claim about the `linux` job has to be
// asked of that job, not of the file around it, or a `secrets.` in the signing
// step would decide it.
func renderedJob(t *testing.T, jobs map[string]map[string]any, path, name string) string {
	t.Helper()
	raw, ok := jobs[name]
	if !ok {
		t.Fatalf("%s has no %s job: it has %v", path, name, jobKeys(jobs))
	}
	out, err := yaml.Marshal(raw)
	if err != nil {
		t.Fatalf("%s's %s job does not marshal back to YAML: %v", path, name, err)
	}
	return string(out)
}

// jobKeys is the job names of a parsed workflow, sorted, for a message that
// says what was there instead.
func jobKeys(jobs map[string]map[string]any) []string {
	keys := make([]string, 0, len(jobs))
	for k := range jobs {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// stepStrings is every step's `run` and `uses` of a workflow, in order: the
// bodies a "nothing here changed" claim is compared against.
func stepStrings(wf consoleWorkflow, job string) []string {
	var out []string
	for _, step := range wf.Jobs[job].Steps {
		switch {
		case step.Uses != "":
			out = append(out, "uses:"+step.Uses)
		case step.Run != "":
			out = append(out, "run:"+step.Run)
		}
	}
	return out
}

// TestConsoleLinuxWorkflowIsBuildOnlyAndSecretless holds the staged CI job to
// what the task asked for and to what the file family requires: one job on
// ubuntu-latest, no secret of any kind, no environment, every action pinned,
// the packer's own script rather than a second copy of its command line, and no
// version stamped into it (only a release knows the version).
//
// The mutations that prove it: give the job `environment: console-release`,
// drop the `pull_request` trigger, replace `npm run package:linux` with an
// inline `npx electron-builder --linux`, add a `paths:` filter, and add
// `-c.extraMetadata.version=0.10.0`.
func TestConsoleLinuxWorkflowIsBuildOnlyAndSecretless(t *testing.T) {
	text, path, wf, rawOn, rawJobs := readConsoleWorkflow(t, consoleLinuxWorkflowPath, appliedConsoleLinuxPath)

	// One job, keyed and named `console-linux`: the key is what a reader types
	// and the name is what the check run is called.
	if len(wf.Jobs) != 1 {
		t.Fatalf("%s has jobs %v, want exactly one: it is a build-only job, and a second one would be a second check run on every pull request", path, jobKeys(rawJobs))
	}
	job, ok := wf.Jobs[consoleLinuxJobName]
	if !ok {
		t.Fatalf("%s has no %s job: it has %v", path, consoleLinuxJobName, jobKeys(rawJobs))
	}
	if job.Name != consoleLinuxJobName {
		t.Errorf("%s's job is named %q, want the static %q: the gate and a reader both match the name", path, job.Name, consoleLinuxJobName)
	}
	if job.RunsOn != "ubuntu-latest" {
		t.Errorf("%s runs on %q, want ubuntu-latest: this job's subject is the packaging path, and what must not move is pinned by the lockfile rather than by the runner image", path, job.RunsOn)
	}

	// Triggers: every pull request and every push to main, with no path filter.
	// A filter here is not the design section 9.4 bug (this job is in no gate's
	// list) and it is still refused: the Linux packaging path is what rots when
	// only a tag runs it.
	if _, ok := rawOn["pull_request"]; !ok {
		t.Errorf("%s does not run on pull_request: %v", path, rawOn)
	}
	push, ok := rawOn["push"].(map[string]any)
	if !ok {
		t.Fatalf("%s's push trigger is not a mapping: %v", path, rawOn["push"])
	}
	if branches, _ := push["branches"].([]any); len(branches) != 1 || branches[0] != "main" {
		t.Errorf("%s's push trigger is %v, want branches: [main]", path, push["branches"])
	}
	for name, trigger := range rawOn {
		block, err := yaml.Marshal(trigger)
		if err != nil {
			t.Fatalf("%s's %s trigger does not marshal back: %v", path, name, err)
		}
		if strings.Contains(string(block), "paths") {
			t.Errorf("%s's %s trigger carries a path filter (%s): the Linux packaging path is exercised by every change or it is not exercised", path, name, strings.TrimSpace(string(block)))
		}
	}

	// No secret, no environment, no shape that could be one -- asked of the raw
	// job as well as of the file, because a job-level `environment:` would be
	// exactly the edit that puts a staging file next to release credentials.
	rawJob := renderedJob(t, rawJobs, path, consoleLinuxJobName)
	for _, forbidden := range []string{"secrets.", "environment:", "-----BEGIN", "pull_request_target", "workflow_run", "write-all"} {
		if strings.Contains(text, forbidden) && forbidden != "environment:" {
			t.Errorf("%s contains %q, which a job that runs on every pull request may never have (design section 9.4)", path, forbidden)
		}
		if strings.Contains(rawJob, forbidden) {
			t.Errorf("%s's %s job contains %q: it packs an unsigned artifact and holds nothing", path, consoleLinuxJobName, forbidden)
		}
	}

	// Permissions: declared, and read-only, both at the workflow and at the job.
	if len(wf.Permissions) != 1 || wf.Permissions["contents"] != "read" {
		t.Errorf("%s's workflow permissions = %v, want exactly contents: read", path, wf.Permissions)
	}
	if len(job.Permissions) != 1 || job.Permissions["contents"] != "read" {
		t.Errorf("%s's %s job permissions = %v, want exactly contents: read declared on the job", path, consoleLinuxJobName, job.Permissions)
	}
	if _, ok := rawJobs[consoleLinuxJobName]["permissions"]; !ok {
		t.Errorf("%s's %s job inherits permissions instead of declaring them", path, consoleLinuxJobName)
	}

	// Every action pinned to a full commit with a version comment, and at least
	// one action: Dependabot does not watch docs/pending-workflows/.
	uses := 0
	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(line, "uses:") {
			continue
		}
		uses++
		if !consoleUsesLine.MatchString(line) {
			t.Errorf("%s: %q is not a full 40-hex pin with a `# vX.Y.Z` comment", path, strings.TrimSpace(line))
		}
	}
	if uses == 0 {
		t.Fatalf("%s pins no action at all: the job checks the repository out and installs Node", path)
	}

	// The install, and the pack.
	runs := strings.Join(stepStrings(wf, consoleLinuxJobName), "\n")
	if n := strings.Count(runs, "npm ci --ignore-scripts --no-audit --no-fund"); n != 1 {
		t.Errorf("%s runs the lockfile install %d times, want exactly 1: it is the pinned toolchain step", path, n)
	}
	for _, want := range []string{
		"node node_modules/electron/install.js",
		"npm run build",
		"npm run package:linux",
	} {
		if !strings.Contains(runs, want) {
			t.Errorf("%s never runs %q", path, want)
		}
	}
	// `package:linux` is the script -- `electron-builder --linux --publish
	// never` -- and not a second copy of its command line: a pack that spelled
	// the flags out here could drop `--publish never` without touching the
	// script the release job also runs. (Asked of the step bodies: the file's
	// own prose names the script and the packer on purpose.)
	for _, forbidden := range []string{"npx ", "npm install", "electron-builder", "extraMetadata", "--publish"} {
		if strings.Contains(runs, forbidden) {
			t.Errorf("%s's steps contain %q: the packer is the lockfile's, through the npm script, and nothing stamps a version -- only a release knows one (console-release.yml)", path, forbidden)
		}
	}

	// The artifacts are uploaded, both kinds, and an empty upload is a failure.
	uploads := 0
	for _, step := range job.Steps {
		if !strings.Contains(step.Uses, "actions/upload-artifact@") {
			continue
		}
		uploads++
		if step.With["if-no-files-found"] != "error" {
			t.Errorf("%s's upload step takes if-no-files-found %q, want error: a build that produced nothing and uploaded nothing, successfully, is the one outcome this job must not report", path, step.With["if-no-files-found"])
		}
		for _, want := range []string{"console/release/*.AppImage", "console/release/*.deb"} {
			if !strings.Contains(step.With["path"], want) {
				t.Errorf("%s's upload step uploads %q, which does not name %s", path, step.With["path"], want)
			}
		}
	}
	if uploads != 1 {
		t.Errorf("%s has %d upload-artifact steps, want 1: the artifacts are the point of the job", path, uploads)
	}
}

// TestConsoleReleaseLinuxAddsTheLinuxLegAndMovesNothingElse is the claim a
// replacement file has to earn. Three jobs; the new one read-only and
// secretless, building and verifying the shipped Linux bytes; `publish` waiting
// for both legs, merging them behind one sums file, signing that file keyless
// and attaching everything -- and the `release` job, the whole macOS signing
// pipeline, byte-for-byte the same job it was.
//
// The mutations that prove it: give the `linux` job `contents: write` or an
// `environment:`, drop `id-token: write` from `publish`, `needs: release` back
// to one leg, drop the cosign step or the `.deb` from `gh release create`, and
// -- the one this test exists for -- change a single character inside the
// `release` job.
func TestConsoleReleaseLinuxAddsTheLinuxLegAndMovesNothingElse(t *testing.T) {
	// The replacement, from whichever home this tree holds it in: staged while
	// the change is pending, `.github/workflows/console-release.yml` once the
	// owner has copied it in. That copy-in writes this text over the staged
	// console-release.yml and then removes the staged replacement, so after it
	// the applied file is the one every reader below reads.
	text, path := consoleReplacementText(t, consoleReleaseLinuxWorkflowPath, appliedConsoleReleasePath)
	wf, _, rawJobs := parseConsoleWorkflow(t, path, text)

	// Three jobs, and what each is for.
	want := []string{"linux", "publish", "release"}
	if got := jobKeys(rawJobs); !slices.Equal(got, want) {
		t.Fatalf("%s has jobs %v, want %v", path, got, want)
	}

	// **The "and moves nothing else" half is a staging-time claim**, and the
	// only one here that cannot be made in both states: it compares the macOS
	// pipeline as it was with this file, and the copy-in's first command makes
	// the staged console-release.yml this very text, so after it there is
	// nothing left to compare. The state is read from the texts themselves
	// rather than from a file's existence, so a tree that kept a staged copy of
	// an applied file cannot pass this half vacuously; and the comparison is
	// made by every run in the staged state, which is the state this file is
	// committed in.
	oldText, oldPath := consoleWorkflowText(t, consoleReleaseWorkflowPath, appliedConsoleReleasePath)
	if oldText == text {
		t.Logf("%s and %s carry the same text, so the 'moves nothing else' comparison has nothing to compare in this tree: the copy-in has been applied", oldPath, path)
	} else {
		oldWF, _, oldJobs := parseConsoleWorkflow(t, oldPath, oldText)

		// The macOS leg, unchanged. This is a comparison of the parsed job
		// rather than of a remembered copy of it: whatever console-release.yml
		// says today is what this file must still say.
		if !reflect.DeepEqual(oldJobs["release"], rawJobs["release"]) {
			t.Errorf("%s's release job is not the one in %s: this file replaces that file, so a change to the signing pipeline has to be one it says out loud (it changes the header, the job inventory and the artifact-name bullets, and adds a job)", path, oldPath)
		}
		// And the job that did move kept everything it had. Its first two steps
		// -- the download and the check of the macOS assets against the sums the
		// release job recorded -- are the same two steps, and five are added
		// after them (the Linux download, the Linux check, the cosign installer,
		// the merge and the signature).
		oldSteps, newSteps := stepStrings(oldWF, "publish"), stepStrings(wf, "publish")
		if len(oldSteps) < 3 {
			t.Fatalf("%s's publish job has %d steps: this test reads its last one as the release step", oldPath, len(oldSteps))
		}
		at := 0
		for i, keep := range oldSteps[:2] {
			next := slices.Index(newSteps[at:], keep)
			if next < 0 {
				t.Errorf("%s's publish job no longer has the step %d of %s (%s) after the ones before it: the merge and the signature are added to that job, not a rewrite of it", path, i+1, oldPath, strings.TrimSpace(keep))
				break
			}
			at += next + 1
		}
		if len(newSteps) != len(oldSteps)+5 {
			t.Errorf("%s's publish job has %d steps, want %d (%d plus the five this change adds)", path, len(newSteps), len(oldSteps)+5, len(oldSteps))
		}
		// The release step itself survives line for line, except the two lines
		// this change edits on purpose: the notes, which have to describe
		// artifacts that are unsigned, and the asset list, which has to carry
		// them.
		oldRelease := oldWF.Jobs["publish"].Steps[len(oldWF.Jobs["publish"].Steps)-1].Run
		deliberatelyEdited := []string{
			`--notes "The Fugaro desktop console built from the CLI's $TAG tag`,
			`"$DMG" "$APP_ZIP" "$RECORD" "$SUMS"`,
		}
		for _, line := range strings.Split(oldRelease, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			if slices.ContainsFunc(deliberatelyEdited, func(prefix string) bool { return strings.HasPrefix(trimmed, prefix) }) {
				continue
			}
			if !strings.Contains(text, trimmed) {
				t.Errorf("%s no longer carries the release step's line %q from %s: the publishing step is edited in two places (the notes and the asset list) and nowhere else", path, trimmed, oldPath)
			}
		}
		// And the release step is still last, and still the one that publishes.
	}
	// The publish step is the last thing `publish` does, in both states: the
	// draft-then-publish sequence cannot be followed by anything, or the release
	// is published before the job that publishes it has finished.
	if last := strings.TrimSpace(stepStrings(wf, "publish")[len(stepStrings(wf, "publish"))-1]); !strings.HasSuffix(last, `echo "published $CONSOLE_TAG"`) {
		t.Errorf("%s's last publish step ends %q, want the release step: the draft-then-publish sequence is the last thing the job does", path, last[len(last)-60:])
	}

	// The linux job: the pinned runner, read-only, and nothing to keep secret.
	linux := renderedJob(t, rawJobs, path, "linux")
	for _, want := range []string{"runs-on: ubuntu-24.04", "contents: read", "checks: read"} {
		if !strings.Contains(linux, want) {
			t.Errorf("%s's linux job does not carry %q: %s", path, want, strings.TrimSpace(linux))
		}
	}
	for _, forbidden := range []string{"secrets.", "environment:", "contents: write", "id-token", "-----BEGIN"} {
		if strings.Contains(linux, forbidden) {
			t.Errorf("%s's linux job carries %q: there is nothing to sign an AppImage with, so this job holds nothing (design section 5.1, the owner's ruling of 2026-10-20)", path, forbidden)
		}
	}

	// Which release stream this text is on. Once console-release-tag.yml has
	// been copied in, the applied file -- the one read here after both
	// supersede files are gone -- triggers on the console's own
	// `console-vX.Y.Z` tag, hands the gate `v` + the version rather than the
	// tag, and resolves the tag the owner pushed rather than asking whether it
	// exists. Those two lines are pinned for the tag stream by
	// TestDocsConsoleReleaseWorkflowIsStagedAndPinned; here they are only read
	// in the form the stream this tree is on carries.
	gateCall := `scripts/release-gate.sh "$TAG"`
	tagRefusal := "could not tell whether the tag $CONSOLE_TAG exists"
	if strings.Contains(text, `tags: ["console-v[0-9]+.[0-9]+.[0-9]+"]`) {
		gateCall = `scripts/release-gate.sh "v$VERSION"`
		tagRefusal = "could not resolve the tag $CONSOLE_TAG to a commit"
	}

	// What the linux job does: the same gate, the same pack, and -- added for
	// Linux because an unsigned artifact has no other claim -- the shipped bytes
	// opened and read.
	linuxRuns := strings.Join(stepStrings(wf, "linux"), "\n")
	for _, want := range []string{
		gateCall,
		"npm run package:linux",
		`sha256sum "$(basename "$APPIMAGE")" "$(basename "$DEB")"`,
		"dpkg-deb --info",
		"dpkg-deb --extract",
		"--appimage-extract",
		"getCurrentFuseWire",
		"console-linux-verification.txt",
	} {
		if !strings.Contains(linuxRuns, want) {
			t.Errorf("%s's linux job never runs %q: the release records what it verified, on the files it ships", path, want)
		}
	}
	// The fuse read-back is the six flips src/main/fuses.ts makes, named one by
	// one: a checker that read one fuse and called the artifact verified is the
	// mutation this catches.
	for _, fuse := range []string{
		"FuseV1Options.RunAsNode",
		"FuseV1Options.EnableNodeOptionsEnvironmentVariable",
		"FuseV1Options.EnableNodeCliInspectArguments",
		"FuseV1Options.EnableEmbeddedAsarIntegrityValidation",
		"FuseV1Options.OnlyLoadAppFromAsar",
		"FuseV1Options.GrantFileProtocolExtraPrivileges",
	} {
		if !strings.Contains(linuxRuns, fuse) {
			t.Errorf("%s's linux job does not read %s back out of the shipped executable: the fuse hook's claim is about the packed tree, and this is the check on the artifact", path, fuse)
		}
	}
	// And it reads the wire out of all three copies of the executable: the
	// unpacked tree, the file inside the .deb and the one inside the AppImage.
	if n := strings.Count(linuxRuns, "node check-fuses.mjs"); n != 3 {
		t.Errorf("%s's linux job reads the fuse wire %d times, want 3 (the linux-unpacked tree, the .deb's own copy and the AppImage's)", path, n)
	}

	// publish: both legs, the write token and the signing identity, and nothing
	// else.
	needs, _ := rawJobs["publish"]["needs"].([]any)
	if len(needs) != 2 || needs[0] != "release" || needs[1] != "linux" {
		t.Errorf("%s's publish job needs %v, want [release, linux]: publishing before the Linux leg has verified would ship a release with a leg missing", path, rawJobs["publish"]["needs"])
	}
	pub := wf.Jobs["publish"]
	if len(pub.Permissions) != 2 || pub.Permissions["contents"] != "write" || pub.Permissions["id-token"] != "write" {
		t.Errorf("%s's publish job permissions = %v, want exactly contents: write and id-token: write (cosign keyless)", path, pub.Permissions)
	}
	pubJob := renderedJob(t, rawJobs, path, "publish")
	for _, forbidden := range []string{"secrets.", "environment:", "-----BEGIN"} {
		if strings.Contains(pubJob, forbidden) {
			t.Errorf("%s's publish job carries %q: it holds no Apple secret and no Linux secret, and its identity is the run's OIDC token", path, forbidden)
		}
	}

	// The cosign installer, at the pin release.yml and images.yml already carry:
	// read out of release.yml rather than typed here, so a Dependabot bump that
	// moves the applied workflows fails this instead of being copied by hand
	// into a staged file nobody watches.
	applied := readDoc(t, ".github/workflows/release.yml")
	cosign := regexp.MustCompile(`sigstore/cosign-installer@[0-9a-f]{40} # v[0-9.]+`).FindString(applied)
	if cosign == "" {
		t.Fatalf(".github/workflows/release.yml no longer pins sigstore/cosign-installer: this test reads the pin from it")
	}
	if !strings.Contains(text, cosign) {
		t.Errorf("%s does not carry %q, the cosign pin .github/workflows/release.yml uses: the keyless signing call here is the same one, so its installer is the same pin", path, cosign)
	}
	for _, want := range []string{
		"cosign sign-blob --yes --bundle=console-sha256sums.txt.sigstore.json console-sha256sums.txt",
		"name: console-linux-assets",
		"name: console-release-assets",
		"console-linux-sha256sums.txt",
		"sha256sum --strict --check console-sha256sums.txt",
		// The refusals a re-run must still meet, verbatim from the old file.
		"the release $CONSOLE_TAG is already published",
		"delete it (gh release delete",
		tagRefusal,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("%s lacks %q", path, want)
		}
	}
	// The merge: one sums file over both legs, checked in the directory that
	// ships, and a collision refused rather than overwritten.
	for _, want := range []string{
		`cat console-macos/console-sha256sums.txt console-linux/console-linux-sha256sums.txt > "$OUT/console-sha256sums.txt"`,
		"refusing to publish a release that is missing a leg",
		"refusing to overwrite one with the other",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("%s lacks %q: the release's sums file has to cover every asset in it", path, want)
		}
	}
	// The release carries every artifact, and the bundle: a release created
	// without the .deb or without the signature over the sums is not one an
	// operator can verify.
	create := regexp.MustCompile(`(?s)gh release create "\$CONSOLE_TAG".*?gh release edit`).FindString(text)
	if create == "" {
		t.Fatalf("%s has no `gh release create` line: this test reads the assets it carries from it", path)
	}
	for _, asset := range []string{`"$DMG"`, `"$APP_ZIP"`, `"$APPIMAGE"`, `"$DEB"`, `"$RECORD"`, `"$LINUX_RECORD"`, `"$SUMS"`, `"$BUNDLE"`} {
		if !strings.Contains(create, asset) {
			t.Errorf("%s's `gh release create` does not attach %s (it attaches %q): every artifact and the signature over the sums file are the release", path, asset, strings.Join(strings.Fields(create), " "))
		}
	}
	if !strings.Contains(oldText, "console-verification.txt") {
		t.Fatalf("%s no longer records a verification file: this test's `LINUX_RECORD` half is written against it", consoleReleaseWorkflowPath)
	}
}

// TestConsoleLinuxStagingTellsTheOwnerWhatToCopyWhere: the two staged files are
// for a person to copy in by hand, and neither can be applied by an agent. What
// this asserts is that the place that person reads -- the staging directory's
// README, and each file's own header -- carries the command, says what happens
// to the staged copy afterwards, and states the one thing about the Linux
// artifacts that must not be misread: that an AppImage is not code-signed.
//
// The mutations that prove it: delete a cp line, delete the `git rm` line from
// the console-linux block, drop the "not code-signed" sentence, and drop the
// note that console-release-linux.yml supersedes the staged console-release.yml.
func TestConsoleLinuxStagingTellsTheOwnerWhatToCopyWhere(t *testing.T) {
	doc := readDoc(t, "docs/pending-workflows/README.md")

	// The count and the names, in the opening line: this directory says what it
	// holds, and it holds two files more than it did. The claim is checked
	// against the directory at the end of this test rather than pinned to the
	// sentence this change writes, because the owner's two copy-ins move it --
	// what must hold is that the sentence and the directory agree, in every
	// state.
	//
	// The sections stay here after a copy-in: they are the record of the change,
	// as `## images.yml` below is the record of that one.

	// The build job's section: what it is, why it is its own workflow, and the
	// command that applies it.
	build := consoleReadmeSection(t, doc, "## `console-linux.yml`")
	for _, want := range []string{
		"cp docs/pending-workflows/console-linux.yml .github/workflows/console-linux.yml",
		"git rm docs/pending-workflows/console-linux.yml",
		"build-only",
		"deriveConsoleCheckNames",
		"TestReleaseGateRequiresTheConsoleCheck",
		"ubuntu-latest",
		"0.0.0",
	} {
		if !strings.Contains(build, want) {
			t.Errorf("docs/pending-workflows/README.md's `console-linux.yml` section lacks %q: the owner is the one who copies it in, and this is where they read what it is and why it is staged apart", want)
		}
	}

	// The two pairs that section prints are the sentences the owner writes when
	// they apply this file, one per state `mobile-release.yml` can be in, and
	// until this test read them nothing did: the copy-in proof splices the Linux
	// pair's, out of `## console-release-linux.yml`. A pair whose names or count
	// have drifted from the state its label describes is a sentence that reddens
	// the two checks below in the owner's commit -- this file replaced in the
	// opening line, and a count and a set the directory does not hold -- so each
	// is held here to the directory that state leaves.
	//
	// The set each pair describes is stated rather than observed, because one of
	// the two states is not this tree's: the pending set with this file gone, and
	// with `mobile-release.yml` in it or out of it. Both are read in every tree,
	// so neither pair can rot while the other is the one the repository is in.
	if docExists(t, consoleLinuxWorkflowPath) {
		const what = "docs/pending-workflows/README.md's `console-linux.yml` section"
		withMobile := pendingWorkflows(t)
		if !slices.Contains(withMobile, filepath.Base(stagedMobileReleaseWorkflowPath)) {
			withMobile = append(withMobile, filepath.Base(stagedMobileReleaseWorkflowPath))
		}
		slices.Sort(withMobile)
		for _, c := range []struct {
			name, label, to string
			want            []string
		}{
			{
				name:  "already applied",
				label: consoleLinuxCopyInMobileAppliedLabel,
				to:    consoleLinuxCopyInMobileStagedLabel,
				want: slices.DeleteFunc(slices.Clone(withMobile), func(n string) bool {
					return n == filepath.Base(consoleLinuxWorkflowPath) || n == filepath.Base(stagedMobileReleaseWorkflowPath)
				}),
			},
			{
				name:  "still staged",
				label: consoleLinuxCopyInMobileStagedLabel,
				to:    consoleLinuxCopyInOtherOrderLabel,
				want:  slices.DeleteFunc(slices.Clone(withMobile), func(n string) bool { return n == filepath.Base(consoleLinuxWorkflowPath) }),
			},
		} {
			pair := readmeSectionPart(t, what, build, c.label, c.to)
			for _, p := range []struct{ what, block, pattern string }{
				{"the opening line", fencedBlockContaining(t, what, pair, "files are pending"), consolePendingBoldPattern},
				{"the site's last paragraph", fencedBlockContaining(t, what, pair, "now holds"), consolePendingPathsPattern},
			} {
				names, count := consolePendingSentence(t, "the pair in `"+what+"` for `mobile-release.yml` "+c.name+", "+p.what, p.block, p.pattern)
				slices.Sort(names)
				if !slices.Equal(names, c.want) {
					t.Errorf("the pair `%s` hands the owner in `%s` names %q, and applying `console-linux.yml` with `mobile-release.yml` %s leaves the directory holding %q: the sentences are held to the directory, and one that named a file that is not pending would be red in the owner's commit", c.label, what, names, c.name, c.want)
				}
				if count != len(c.want) {
					t.Errorf("the pair `%s` hands the owner in `%s` says %d pending workflow(s), and applying `console-linux.yml` with `mobile-release.yml` %s leaves %d (%q): the count and the names are one claim", c.label, what, count, c.name, len(c.want), c.want)
				}
			}
		}
	}

	// The release file's section: the three commands, in order, and the reason
	// there are three.
	release := consoleReadmeSection(t, doc, "## `console-release-linux.yml`")
	first := strings.Index(release, "cp docs/pending-workflows/console-release-linux.yml docs/pending-workflows/console-release.yml")
	second := strings.Index(release, "cp docs/pending-workflows/console-release.yml .github/workflows/console-release.yml")
	third := strings.Index(release, "git rm docs/pending-workflows/console-release-linux.yml")
	if first < 0 || second < first || third < second {
		t.Errorf("docs/pending-workflows/README.md's `console-release-linux.yml` section does not carry the three copy-in commands in order (the staged file, then the applied file, then the removal): %v %v %v", first, second, third)
	}
	for _, want := range []string{
		"supersedes",
		"an AppImage is not code-signed",
		"console-sha256sums.txt.sigstore.json",
		"TestDocsConsoleReleaseWorkflowIsStagedAndPinned",
		"website/README.md",
	} {
		if !strings.Contains(release, want) {
			t.Errorf("docs/pending-workflows/README.md's `console-release-linux.yml` section lacks %q", want)
		}
	}

	// And each staged file's own header carries its commands: a reader who opens
	// the file rather than the README is the likelier one. A file whose copy-in
	// has already been applied is gone, and the commands travel with it -- the
	// text that runs under .github/workflows/ is not a place to keep
	// instructions about a copy-in that has happened -- so this half is asked
	// only of a file that is still here.
	for path, want := range map[string][]string{
		consoleLinuxWorkflowPath: {
			"cp docs/pending-workflows/console-linux.yml .github/workflows/console-linux.yml",
			"git rm docs/pending-workflows/console-linux.yml",
		},
		consoleReleaseLinuxWorkflowPath: {
			"cp docs/pending-workflows/console-release-linux.yml docs/pending-workflows/console-release.yml",
			"cp docs/pending-workflows/console-release.yml .github/workflows/console-release.yml",
			"git rm docs/pending-workflows/console-release-linux.yml",
		},
	} {
		if !docExists(t, path) {
			continue
		}
		header := readDoc(t, path)
		for _, line := range want {
			if !strings.Contains(header, line) {
				t.Errorf("%s's header lacks the copy-in line %q: the file is what the owner has open", path, line)
			}
		}
	}

	// The site's README counts and names the pending files, and this README's
	// own opening line does too. Both are claims about a directory that changes
	// as the owner copies files in, and both are held to the directory rather
	// than to a remembered count: a sentence left behind by a copy-in is a
	// reader told that a file is pending when it runs, or the other way round.
	// `website/test/readme.mjs` makes the site's half of this check in Node;
	// this is the same rule for the two sentences the Go suite can see.
	wantPending := pendingWorkflows(t)
	for _, c := range []struct {
		what, text, pattern string
	}{
		{"website/README.md", readDoc(t, "website/README.md"), consolePendingPathsPattern},
		{"docs/pending-workflows/README.md's opening line", pendingReadmeOpening(t, doc), consolePendingBoldPattern},
	} {
		names, count := consolePendingSentence(t, c.what, c.text, c.pattern)
		if !slices.Equal(names, wantPending) {
			t.Errorf("%s names %v as the pending workflows, and docs/pending-workflows/ holds %v: the sentence has to name exactly the files a copy-in has not taken yet (a staged file it does not name is a pending change nobody was told about, and a name for a file that runs is an instruction to copy in what is already there)", c.what, names, wantPending)
		}
		if count != len(wantPending) {
			t.Errorf("%s says %d pending workflow(s) and docs/pending-workflows/ holds %d (%v): the count and the names are one claim", c.what, count, len(wantPending), wantPending)
		}
	}
}

// TestDocsPendingWorkflowsReadmeFencesCarryNoBlankLine: each of those sentences is handed
// to the owner **inside a fenced block as the exact text to paste** -- one for
// the opening line, one for the site's paragraph, in every state the two
// copy-ins leave the directory in -- and a blank line inside such a block is a
// paragraph break the owner's paste would carry into the sentence. It splits the
// sentence in two when the README is read on GitHub, and nothing caught it: the
// check above flattens whitespace before it reads a sentence, and
// `website/test/readme.mjs` does the same, so a fence broken mid-sentence reads
// exactly like an intact one to both. (One had: "(mobile plan Task" and "P4.3)"
// were two paragraphs in the block for `console-release-linux.yml`'s copy-in.)
//
// A fence is one of these when its first line is one of the two sentence heads:
// the opening line's `**N files are pending: …**`, or the site paragraph's
// `\`docs/pending-workflows/\` now holds …`. The blocks of shell commands beside
// them are left alone -- a blank line between two commands is ordinary, and the
// rule would be about punctuation rather than about text a reader pastes.
func TestDocsPendingWorkflowsReadmeFencesCarryNoBlankLine(t *testing.T) {
	const path = "docs/pending-workflows/README.md"
	doc := readDoc(t, path)

	var blocks [][]string
	var current []string
	inFence := false
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if inFence {
				blocks = append(blocks, current)
				current = nil
			}
			inFence = !inFence
			continue
		}
		if inFence {
			current = append(current, line)
		}
	}
	if inFence {
		t.Fatalf("%s has an unclosed fenced block: the sentences the owner pastes are read from these", path)
	}

	sentences := 0
	for _, block := range blocks {
		if len(block) == 0 {
			continue
		}
		head := block[0]
		if !consolePendingSentenceHeadRE.MatchString(head) && !strings.HasPrefix(head, "`docs/pending-workflows/` now holds") {
			continue
		}
		sentences++
		for i, line := range block {
			if strings.TrimSpace(line) != "" {
				continue
			}
			t.Errorf("%s: the sentence the owner pastes, starting %q, has a blank line at line %d of its fenced block: the block is the exact text, and a blank line is a paragraph break the paste carries into a sentence -- the whitespace-flattening checks above and in website/test/readme.mjs read a broken fence and an intact one the same", path, head, i+1)
			break
		}
	}
	if sentences == 0 {
		t.Fatalf("%s hands the owner no fenced sentence: the copy-in sections carry the text to paste, and this test reads those blocks", path)
	}
}

// pendingWorkflows is the workflows docs/pending-workflows/ still holds as
// pending changes, sorted: every `.yml` in it except the two whose staged copies
// are kept after their copy-in on purpose (`console-tests.yml` and
// `console-release.yml`, whose staged copies are asserted byte-identical to the
// files that run -- docs/pending-workflows/README.md says why). It is derived
// from the directory rather than listed, so it is right in every state the
// owner's copy-ins pass through.
func pendingWorkflows(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join("..", "..", "docs", "pending-workflows"))
	if err != nil {
		t.Fatal(err)
	}
	kept := []string{"console-tests.yml", "console-release.yml"}
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || (!strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml")) {
			continue
		}
		if slices.Contains(kept, name) {
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// pendingReadmeOpening is the staging directory's own opening line: the
// paragraph that says how many files are pending here and names them, up to the
// sentence that separates them from the two staged copies that stay.
func pendingReadmeOpening(t *testing.T, doc string) string {
	t.Helper()
	start := strings.Index(doc, "**")
	if start < 0 {
		t.Fatalf("docs/pending-workflows/README.md does not open with a claim about what is pending")
	}
	end := strings.Index(doc, "**Two other files stay staged here by design")
	if end < start {
		t.Fatalf("docs/pending-workflows/README.md no longer separates the pending files from the two staged copies that stay: this test reads the paragraph between the two")
	}
	return doc[start:end]
}

// consolePendingSentence reads one of those sentences: the names it gives, and
// the count it states as a word. A sentence that states no count, or names no
// file, fails here rather than passing as an empty list. `namePattern` is how
// that sentence spells a name: the site's README names the files by their staged
// paths -- the form `website/test/readme.mjs` reads -- while this directory's
// own opening line bolds each bare name, and neither form may pick up a name
// mentioned in passing beside it.
func consolePendingSentence(t *testing.T, what, text, namePattern string) ([]string, int) {
	t.Helper()
	flat := strings.Join(strings.Fields(text), " ")
	words := map[string]int{"no": 0, "one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6}
	count := -1
	for _, pattern := range []*regexp.Regexp{
		// "**Three files are pending: `shared-tests.yml`** ..."
		consolePendingSentenceHeadRE,
		// "`docs/pending-workflows/` now holds three pending workflows,"
		regexp.MustCompile(`\b(?:holds|names)\s+(\w+) pending`),
	} {
		m := pattern.FindStringSubmatch(flat)
		if m == nil {
			continue
		}
		n, ok := words[strings.ToLower(m[1])]
		if !ok {
			t.Fatalf("%s says %q pending, which is not a count this test can read", what, m[1])
		}
		count = n
		break
	}
	if count < 0 {
		t.Fatalf("%s does not state how many workflows are pending (%q)", what, flat)
	}
	var names []string
	add := func(name string) {
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	switch namePattern {
	case consolePendingPathsPattern:
		for _, name := range regexp.MustCompile(consolePendingPathsPattern).FindAllStringSubmatch(flat, -1) {
			add(name[1])
		}
	case consolePendingBoldPattern:
		// The opening line wraps each pending file in `**` ... `**`, one item
		// per file, and explains each in a parenthetical that is not bold: a
		// name mentioned in passing in one of those -- the staged
		// `console-release.yml` this change supersedes -- is not a file the
		// sentence is counting.
		for _, span := range regexp.MustCompile(`\*\*(.*?)\*\*`).FindAllStringSubmatch(flat, -1) {
			for _, name := range regexp.MustCompile("`([\\w.-]+\\.ya?ml)`").FindAllStringSubmatch(span[1], -1) {
				add(name[1])
			}
		}
	}
	slices.Sort(names)
	if len(names) == 0 {
		t.Fatalf("%s names no workflow file: the sentence has to name the files it counts", what)
	}
	return names, count
}

// consolePendingPathsPattern and consolePendingBoldPattern are the two
// spellings consolePendingSentence can read: the file names it is given as
// patterns rather than as a mode, because the parsing differs, not just the
// pattern.
const (
	consolePendingPathsPattern = "`docs/pending-workflows/([\\w.-]+\\.ya?ml)`"
	consolePendingBoldPattern  = "bold"
)

// consolePendingSentenceHeadRE matches the head of the opening-line sentence
// ("**Three files are pending: `shared-tests.yml`** ..."), which is also how
// TestDocsPendingWorkflowsReadmeFencesCarryNoBlankLine recognises a fenced block that holds
// a sentence rather than a list of commands. The two share it deliberately: a
// sentence the parser reads has to be a sentence whose fence is checked.
var consolePendingSentenceHeadRE = regexp.MustCompile(`\*\*(\w+) files? (?:is|are) pending`)

// TestConsoleReleaseLinuxStagedFileIsNotTheAppliedOne keeps the files this
// change moves in the relation it claims for them, in both of the states the
// owner's copy-in passes through. Three claims, none of which depends on which
// state the tree is in:
//
//   - the staged `console-release.yml` is byte-identical to the file that runs,
//     before the copy-in and after it (the copy-in's first command makes it the
//     replacement's text, which is what the second command copies in);
//   - the build check is in exactly one of its two homes -- staged while the
//     change is pending, applied once it has been copied in, never both (its
//     copy-in is a `cp` and a `git rm`, so both would be a staged copy of a file
//     that already runs);
//   - the replacement, while it is staged, is a change rather than a copy of the
//     file it supersedes; and once it has been applied, the text that runs is
//     the merged one -- the Linux leg is in it -- because the staged
//     replacement, the only other copy of it, is gone.
//
// The mutation that proves it: copy console-release-linux.yml's content over
// docs/pending-workflows/console-release.yml without copying it in, which is the
// state the README's commands pass through in one command and which the first
// clause refuses.
func TestConsoleReleaseLinuxStagedFileIsNotTheAppliedOne(t *testing.T) {
	stagedBuild, appliedBuild := docExists(t, consoleLinuxWorkflowPath), docExists(t, appliedConsoleLinuxPath)
	if stagedBuild == appliedBuild {
		t.Errorf("the build check is %s: %s and %s must hold exactly one of the two -- it is staged until the owner copies it in, and the copy-in removes the staged copy, so a tree with both has a pending file that already runs and a tree with neither has lost it (docs/pending-workflows/README.md)",
			map[bool]string{true: "in both places", false: "in neither place"}[stagedBuild], consoleLinuxWorkflowPath, appliedConsoleLinuxPath)
	}

	applied, err := os.ReadFile(filepath.Join("..", "..", appliedConsoleReleasePath))
	if err != nil {
		t.Fatal(err)
	}
	staged, err := os.ReadFile(filepath.Join("..", "..", consoleReleaseWorkflowPath))
	if err != nil {
		t.Fatal(err)
	}
	if string(applied) != string(staged) {
		t.Errorf("%s and %s differ. That is the 2026-10-19 state this change is deliberately not in: %s is staged as a file of its own precisely so that the byte-identity clause stays green and main does not spend the window before the copy-in refusing every tag (docs/pending-workflows/README.md)",
			appliedConsoleReleasePath, consoleReleaseWorkflowPath, consoleReleaseLinuxWorkflowPath)
	}
	if docExists(t, consoleReleaseLinuxWorkflowPath) {
		replacement, err := os.ReadFile(filepath.Join("..", "..", consoleReleaseLinuxWorkflowPath))
		if err != nil {
			t.Fatal(err)
		}
		if string(replacement) == string(staged) {
			t.Errorf("%s is byte-identical to %s: then it is not a change, and its copy-in commands are instructions to do nothing", consoleReleaseLinuxWorkflowPath, consoleReleaseWorkflowPath)
		}
		return
	}
	// Applied: the replacement is gone, so the file that runs is the only copy
	// of the Linux legs left, and it has to carry them.
	for _, want := range []string{"name: console-linux-assets", "console-linux-verification.txt", "cosign sign-blob --yes --bundle=console-sha256sums.txt.sigstore.json"} {
		if !strings.Contains(string(applied), want) {
			t.Errorf("%s no longer carries %q, and %s is gone: the copy-in's first command writes the replacement over this file, so an applied tree that lacks it has lost the Linux legs entirely", appliedConsoleReleasePath, want, consoleReleaseLinuxWorkflowPath)
		}
	}
}

// consoleFeedFileName is the update feed's own name: electron-updater's macOS
// manifest, which the release job writes beside the .zip (design section 7.3,
// and Task 4.4 of docs/plans/2026-10-15-desktop-console.md -- the packer cannot
// write it: `build.publish` is `null` in console/package.json, so
// electron-builder publishes nothing).
const consoleFeedFileName = "latest-mac.yml"

// consoleFeedTemplate is the printf that writes the feed, character for
// character: the six fields electron-updater reads from a full download and
// nothing else -- no `blockmap`, whose differential download is a later decision
// that is off by default. `url` and `path` are
// the .zip's name **as GitHub stores it**, which is not the name the packer
// gives it: the default macOS artifact name carries a space and GitHub renders
// it as a dot, so a feed naming the packer's own spelling would point
// electron-updater at an asset that does not exist.
const consoleFeedTemplate = `printf 'version: %s\nfiles:\n  - url: %s\n    sha512: %s\n    size: %s\npath: %s\nsha512: %s\nreleaseDate: %s\n' "$VERSION" "$FEED_ASSET" "$ZIP_SHA512" "$ZIP_SIZE" "$FEED_ASSET" "$ZIP_SHA512" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$FEED"`

// consoleFeedSumLine appends the feed's own SHA-256 to the job's sums file, so
// the feed travels to `publish` -- and to the release -- under the merged
// console-sha256sums.txt that cosign signs: the feed is in the sums.
const consoleFeedSumLine = `(cd "$(dirname "$FEED")" && shasum -a 256 "$(basename "$FEED")") >> "$SUMS"`

// consoleReleaseMacosSumsStep is the publish job's check over the macOS leg's
// artifact, which the feed joins: it refuses an artifact with no feed and a
// feed that is not in the sums.
const consoleReleaseMacosSumsStep = "The assets match the sums the release job recorded"

// consoleSecretRefRE is one `secrets.NAME` reference anywhere in a workflow
// file. The feed change adds none.
var consoleSecretRefRE = regexp.MustCompile(`secrets\.[A-Za-z0-9_]+`)

// consoleReleaseShape parses a workflow file text into the shape this family's
// step readers use, naming the file it could not read rather than the one the
// older readers remember.
func consoleReleaseShape(t *testing.T, path, text string) consoleReleaseWorkflow {
	t.Helper()
	var wf consoleReleaseWorkflow
	if err := yaml.Unmarshal([]byte(text), &wf); err != nil {
		t.Fatalf("%s is not valid YAML: %v", path, err)
	}
	return wf
}

// consoleOtherwiseIdentical is the part of a console release workflow the feed
// change must leave exactly as it is: every action pin, the trigger, the
// permissions the workflow and each job hold, the environment each job takes,
// the job set, and every secret the file names. It is compared between the
// staged feed file and the console-release.yml it supersedes, so a change that
// moved a pin, a permission, the signing job's protected environment or a
// secret reference fails here rather than in the owner's copy-in.
func consoleOtherwiseIdentical(t *testing.T, text string) string {
	t.Helper()
	var raw struct {
		Permissions map[string]any            `yaml:"permissions"`
		Jobs        map[string]map[string]any `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(text), &raw); err != nil {
		t.Fatalf("%s is not valid YAML: %v", consoleReleaseWorkflowPath, err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "permissions=%v\n", raw.Permissions)
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(line, "uses:") {
			b.WriteString(trimmed + "\n")
		}
	}
	b.WriteString(consoleTriggerBlock(t, text) + "\n")
	names := make([]string, 0, len(raw.Jobs))
	for name := range raw.Jobs {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		job := raw.Jobs[name]
		fmt.Fprintf(&b, "job %s environment=%v permissions=%v\n", name, job["environment"], job["permissions"])
	}
	for _, ref := range consoleSecretRefRE.FindAllString(text, -1) {
		b.WriteString(ref + "\n")
	}
	return b.String()
}

// consoleLinesDropped is the lines base carries and text does not, as a
// multiset. The feed change adds; the only lines it may take away are the
// comment lines of the file's own header, which gains a paragraph describing
// it.
func consoleLinesDropped(base, text string) []string {
	remaining := map[string]int{}
	for _, line := range strings.Split(text, "\n") {
		remaining[line]++
	}
	var dropped []string
	for _, line := range strings.Split(base, "\n") {
		if remaining[line] > 0 {
			remaining[line]--
			continue
		}
		dropped = append(dropped, line)
	}
	return dropped
}

// TestDocsConsoleReleaseFeedIsStagedAndPinned holds the staged update feed to
// what the console's auto-update needs and to nothing else (the owner's go of
// 2026-10-30; Task 4.4 of docs/plans/2026-10-15-desktop-console.md, design
// section 7.3). The macOS `release` job writes `latest-mac.yml`
// beside the .zip it has just verified, from the tag's version and the .zip's
// own bytes, sums it into the job's console-sha256sums.txt and uploads it with
// the other assets; `publish` requires it, checks it against the sums and
// attaches it. Everything that makes this file safe to run -- the pins, the
// trigger, the permissions, the protected environment the Apple secrets live
// behind and every secret the file names -- is what runs today.
//
// It reads the feed text the way this family reads a supersede file: from
// docs/pending-workflows/console-release-feed.yml while it is staged, and from
// console-release.yml once the owner's copy-in has written it over that file
// and removed it, so the same suite is green on both sides of the copy-in. The
// mutation that proves the first half: the same test against a tree with no
// feed file, where console-release.yml writes, sums and attaches no
// latest-mac.yml at all.
func TestDocsConsoleReleaseFeedIsStagedAndPinned(t *testing.T) {
	text, path := consoleFeedText(t)

	for _, want := range []string{
		`FEED_ASSET="$(basename "$ZIP" | tr ' ' '.')"`,
		`ZIP_SHA512="$(openssl dgst -sha512 -binary "$ZIP" | base64 | tr -d '\n')"`,
		`ZIP_SIZE="$(stat -f%z "$ZIP")"`,
		`FEED="$(dirname "$ZIP")/` + consoleFeedFileName + `"`,
		consoleFeedTemplate,
		consoleFeedSumLine,
		`record cat "$FEED"`,
		`cp "$FEED" "$ASSETS/"`,
		`-s ` + consoleFeedFileName,
		`grep -qF "  ` + consoleFeedFileName + `" console-sha256sums.txt`,
		`FEED="` + consoleFeedFileName + `"`,
		`test -s "$FEED"`,
		`"$FEED"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("%s does not carry %q: the release job writes the update feed beside the zip from the bytes it just verified, sums it, uploads it with the other assets, and publish refuses a release that would ship without it", path, want)
		}
	}
	// The feed's line in the release's sums file has a second reader, and this
	// file is where the next run learns it: the site's copy of that file is
	// checked name by name against the downloads it offers, so a sums file
	// carrying the feed is a website step that has to decide something. The
	// note lives in the header, which the copy-in carries into the workflow
	// itself -- the one place a run that refreshes the site will look.
	for _, want := range []string{"website/data/console-sha256sums.txt", "website/content/downloads.json"} {
		if !strings.Contains(text, want) {
			t.Errorf("%s does not name %q: the feed joins console-sha256sums.txt, and the release's sums file is copied verbatim into the site, where every name in it has to resolve to an offered download (TestWebsiteConsoleReleaseFactsAreComplete) -- so the run that refreshes the site after this change has to be told, or it meets a red `main` it cannot explain", path, want)
		}
	}

	wf := consoleReleaseShape(t, path, text)
	for _, job := range wf.Jobs {
		for _, step := range job.Steps {
			for _, line := range consoleRunLines(step.Run) {
				if strings.Contains(line, "blockmap") {
					t.Errorf("%s's %q step runs %q: the feed carries the fields electron-updater needs from a full download and nothing else, and the differential download is a later decision that is off by default", path, step.Name, line)
				}
			}
		}
	}

	release, ok := wf.Jobs["release"]
	if !ok {
		t.Fatalf("%s has no release job: there is nowhere for the feed to be written", path)
	}
	_, verify := consoleStepNamed(t, release, "release", consoleReleaseVerifyStep)
	if verify.Env["VERSION"] != "${{ steps.tag.outputs.version }}" {
		t.Errorf("%s's verification step VERSION = %q, want the tag step's output: the feed's `version` is the tag's X.Y.Z, the same one stamped on every pack, and a feed built from anything else offers an update to a version that was never released", path, verify.Env["VERSION"])
	}
	vlines := consoleRunLines(verify.Run)
	zipVerified := consoleLineIndex(vlines, `record retry_apple_network xcrun stapler validate "$IN_ZIP"`, false)
	feed := consoleLineIndex(vlines, consoleFeedTemplate, false)
	summed := consoleLineIndex(vlines, consoleFeedSumLine, false)
	if !(zipVerified >= 0 && zipVerified < feed && feed < summed) {
		t.Errorf("%s writes the feed at %d, sums it at %d, and verifies the .zip's copy at %d: the feed describes the bytes of a .zip that has been verified, and its digest has to be in the sums file before that file is uploaded (%v)", path, feed, summed, zipVerified, vlines)
	}
	if consoleLineIndex(vlines, `cp "$FEED" "$ASSETS/"`, false) <= consoleLineIndex(vlines, `cp "$DMG" "$ZIP" "$RECORD" "$SUMS" "$ASSETS/"`, false) {
		t.Errorf("%s copies the feed into the uploaded artifact before the artifacts it belongs beside: the artifact is what publish reads, and every file in the release is in it", path)
	}

	pub, ok := wf.Jobs["publish"]
	if !ok {
		t.Fatalf("%s has no publish job: the feed would be built and never published", path)
	}
	_, sumsStep := consoleStepNamed(t, pub, "publish", consoleReleaseMacosSumsStep)
	slines := consoleRunLines(sumsStep.Run)
	if consoleLineIndex(slines, `if [ ! -s `+consoleFeedFileName+` ]; then`, false) < 0 {
		t.Errorf("%s's %q step does not require exactly one non-empty %s: a macOS artifact with no feed is a console release that can never update itself, and the check that refuses it belongs with the sums check it has to pass anyway", path, consoleReleaseMacosSumsStep, consoleFeedFileName)
	}
	if consoleLineIndex(slines, `grep -qF "  `+consoleFeedFileName+`" console-sha256sums.txt`, true) < 0 {
		t.Errorf("%s's %q step does not check the feed against console-sha256sums.txt: the signature over that file is what vouches for the feed, so a feed the sums file does not list is a manifest nothing signs", path, consoleReleaseMacosSumsStep)
	}
	_, publish := consoleStepNamed(t, pub, "publish", consoleReleasePublishStep)
	plines := consoleRunLines(publish.Run)
	if consoleLineIndex(plines, `FEED="`+consoleFeedFileName+`"`, false) < 0 || consoleLineIndex(plines, `test -s "$FEED"`, false) < 0 {
		t.Errorf("%s's publish step does not read the feed with a non-empty check of its own: every other asset there is `test -s`'d before the release is created, and a release that ships an empty manifest is worse than one with none", path)
	}
	assets := -1
	for i, line := range plines {
		if strings.HasPrefix(line, `"$DMG" "$APP_ZIP"`) {
			assets = i
		}
	}
	if assets < 0 || !strings.Contains(plines[assets], `"$FEED"`) {
		t.Errorf("%s's publish step does not attach the update feed with the rest of the release: the manifest is the one file electron-updater fetches directly, and an uploaded-but-unattached feed is a feed no client can see (%v)", path, plines)
	}

	// The both-states half: while the feed file is staged it has to be a change
	// to the file it supersedes, and a change to nothing else.
	if !docExists(t, consoleReleaseFeedWorkflowPath) {
		return
	}
	base, basePath := consoleWorkflowText(t, consoleReleaseWorkflowPath, appliedConsoleReleasePath)
	if text == base {
		t.Errorf("%s is byte-identical to %s: then it is not a change, and its copy-in commands are instructions to do nothing", consoleReleaseFeedWorkflowPath, basePath)
		return
	}
	if got, want := consoleOtherwiseIdentical(t, text), consoleOtherwiseIdentical(t, base); got != want {
		t.Errorf("%s and %s differ outside the feed: the action pins, the trigger, the permissions, every job's environment and every `secrets.` reference have to be identical, because this change adds a manifest and touches nothing that decides who may run it or what it may read.\nfeed:\n%s\n%s:\n%s", consoleReleaseFeedWorkflowPath, basePath, got, basePath, want)
	}
	// The diff is additions, plus the one line the feed joins the release's
	// asset list on. Anything else that went missing is a line of the pipeline
	// this change had no business rewriting.
	for _, line := range consoleLinesDropped(base, text) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.Contains(text, trimmed+` "$FEED"`) {
			continue
		}
		t.Errorf("%s drops %q, which %s carries: the feed change adds a step's worth of shell, a paragraph of header, and `\"$FEED\"` on the release's asset list -- it rewrites nothing else", consoleReleaseFeedWorkflowPath, trimmed, basePath)
	}
}

// consoleReleaseSymbolsStep is the step the symbol-upload supersession adds to
// the macOS `release` job, by name, and consoleReleaseSymbolsScript is what that
// step runs -- the script whose own unit test pins the three `sentry-cli`
// commands it makes, their arguments, and that the token never reaches an argv.
const (
	consoleReleaseSymbolsStep   = "Upload the crash reporter's symbols, when a token is configured"
	consoleReleaseSymbolsScript = "node scripts/upload-sentry-symbols.mjs"
)

// consoleReleaseSymbolsSecret is the repository secret the step reads -- the
// console-release environment's seventh, beside the six the signing step reads
// -- and consoleReleaseSymbolsRefCount is how many `secrets.NAME` references the
// file carries: one more than the file it supersedes (whose own seven include
// the one the header's rule about them is written with), and the whole of the
// difference between them.
const (
	consoleReleaseSymbolsSecret   = "SENTRY_AUTH_TOKEN"
	consoleReleaseSymbolsRefCount = 8
)

// consoleSymbolsText is the console signing workflow's text as this tree holds
// the symbol-upload change: the staged supersession while it is staged, and
// console-release.yml once the owner's copy-in has written it over that file and
// removed it. It goes through consoleReleaseEffectiveText first, so the
// byte-identity clause between the two copies of console-release.yml stays live
// in every state the copy-in passes through.
func consoleSymbolsText(t *testing.T) (string, string) {
	t.Helper()
	file, path := consoleReleaseEffectiveText(t)
	if docExists(t, consoleReleaseSymbolsWorkflowPath) {
		return readDoc(t, consoleReleaseSymbolsWorkflowPath), consoleReleaseSymbolsWorkflowPath
	}
	return file, path
}

// TestDocsConsoleReleaseSymbolsIsStagedAndPinned holds the staged symbol upload
// to what it claims to be (the owner's ruling of 2026-11-01; design section
// 7.4): one step in the macOS `release` job, after the artifacts have been
// verified and before they leave the job, running
// `console/scripts/upload-sentry-symbols.mjs` with **the file's second secret in
// its environment and nowhere else** -- the token is never an operand of
// anything, because an argv is readable by every process of the same user on the
// runner. Everything that makes the file safe to run is what runs today: the
// pins, the trigger, the permissions, the protected environment and the six
// secrets the signing step already reads.
//
// It reads the file the way this family reads a supersede file: from
// docs/pending-workflows/console-release-symbols.yml while it is staged, and
// from console-release.yml once the owner's copy-in has written it over that
// file and removed it, so the same suite is green on both sides of the copy-in.
// The mutation that proves the first half: the same test against a tree with
// docs/pending-workflows/console-release-symbols.yml deleted, where the applied
// file carries no step of that name and the reader's Fatalf names it.
func TestDocsConsoleReleaseSymbolsIsStagedAndPinned(t *testing.T) {
	text, path := consoleSymbolsText(t)

	wf := consoleReleaseShape(t, path, text)
	release, ok := wf.Jobs["release"]
	if !ok {
		t.Fatalf("%s has no release job: the symbols are uploaded from the build the release job has just verified, and there is nowhere else they could come from", path)
	}
	index, step := consoleStepNamed(t, release, "release", consoleReleaseSymbolsStep)
	if step.Uses != "" {
		t.Errorf("%s's %q step runs the action %q: the upload is a command in this file's own shell, not a marketplace action -- a `uses:` line is a third party's code inside the job that holds the release's signing material, and it would need a pin this file does not have", path, consoleReleaseSymbolsStep, step.Uses)
	}
	if step.WorkingDirectory != "console" {
		t.Errorf("%s's %q step runs in %q, not `console`: the script is resolved from there, as every other step of this job that runs console code does", path, consoleReleaseSymbolsStep, step.WorkingDirectory)
	}
	if !strings.Contains(step.Run, "set -euo pipefail") || !strings.Contains(step.Run, consoleReleaseSymbolsScript) {
		t.Errorf("%s's %q step runs %q: it has to run the pinned script with the same fail-closed shell every other step of this job uses -- a step that swallowed a failure would leave a release whose crashes cannot be read", path, consoleReleaseSymbolsStep, step.Run)
	}
	// The environment: the token, and nothing else. The org, the project, the
	// region and the release are constants inside the script, where no run of
	// this workflow can point them at somebody else's account.
	if got, want := len(step.Env), 1; got != want {
		t.Errorf("%s's %q step's env names %d variables (%v), want the token alone: the script takes the org, the project, the region and the release as its own constants and reads the token from the environment it inherits", path, consoleReleaseSymbolsStep, got, step.Env)
	}
	if got, want := step.Env[consoleReleaseSymbolsSecret], "${{ secrets."+consoleReleaseSymbolsSecret+" }}"; got != want {
		t.Errorf("%s's %q step's %s is %q, want %q", path, consoleReleaseSymbolsStep, consoleReleaseSymbolsSecret, got, want)
	}
	if got := strings.Count(text, "secrets."+consoleReleaseSymbolsSecret); got != 1 {
		t.Errorf("%s names secrets.%s %d times: the token belongs to one step, and a second mention is a second place a change can put it on a command line", path, consoleReleaseSymbolsSecret, got)
	}
	if got := len(consoleSecretRefRE.FindAllString(text, -1)); got != consoleReleaseSymbolsRefCount {
		t.Errorf("%s names %d secrets, want %d: the console-release environment's six plus this change's one, and a secret this file does not need is a secret the release job can read for no reason", path, got, consoleReleaseSymbolsRefCount)
	}

	// Where the step is: the app's debug files and its source maps have to
	// describe the bytes that ship, so the upload runs after the step that
	// verifies the shipped artifacts and before they leave the job as an
	// artifact.
	verifyIndex, _ := consoleStepNamed(t, release, "release", consoleReleaseVerifyStep)
	if index < verifyIndex {
		t.Errorf("%s runs the symbol upload at step %d and verifies the shipped artifacts at step %d: the symbols have to come from the bundle that has just been verified -- an upload before it describes bytes nobody has checked", path, index, verifyIndex)
	}
	artifact := -1
	for i, other := range release.Steps {
		if strings.HasPrefix(other.Uses, "actions/upload-artifact@") {
			artifact = i
		}
	}
	if artifact < 0 {
		t.Fatalf("%s's release job uploads no artifact: this test reads the step order around it, and a file that stopped uploading the verified assets is a change of its own", path)
	}
	if index > artifact {
		t.Errorf("%s runs the symbol upload at step %d, after the verified assets have left the job at step %d: the step would be reading a bundle the job no longer has, and a symbols upload that silently found nothing is a release whose reports cannot be read", path, index, artifact)
	}

	if !docExists(t, consoleReleaseSymbolsWorkflowPath) {
		return
	}
	// The both-states half: while it is staged it has to be a change to the file
	// it supersedes, and a change to nothing else -- the one secret reference
	// aside, which is the change.
	base, basePath := consoleWorkflowText(t, consoleReleaseWorkflowPath, appliedConsoleReleasePath)
	if text == base {
		t.Errorf("%s is byte-identical to %s: then it is not a change, and its copy-in commands are instructions to do nothing", consoleReleaseSymbolsWorkflowPath, basePath)
		return
	}
	got := strings.ReplaceAll(consoleOtherwiseIdentical(t, text), "secrets."+consoleReleaseSymbolsSecret+"\n", "")
	if want := consoleOtherwiseIdentical(t, base); got != want {
		t.Errorf("%s and %s differ outside the symbol step: the action pins, the trigger, the permissions, every job's environment and every other `secrets.` reference have to be identical, because this change adds an upload and touches nothing that decides who may run it or what it may read.\nsymbols:\n%s\n%s:\n%s", consoleReleaseSymbolsWorkflowPath, basePath, got, basePath, want)
	}
	for _, line := range consoleLinesDropped(base, text) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		t.Errorf("%s drops %q, which %s carries: the symbol change adds one step and a paragraph of header, and rewrites nothing else", consoleReleaseSymbolsWorkflowPath, trimmed, basePath)
	}
}

// TestStagedLinuxFilesSurviveTheCopyIn is the proof of the property every test
// in this family is written for: **the copy-in the owner does by hand does not
// turn `main` red.** The tests above read the two Linux files through
// consoleWorkflowText, in whichever of their two homes the tree holds them, so
// that the same suite is green while the change is staged and after it has been
// applied -- and this is the test that checks that claim by making the change
// happen: it copies this tree to a temporary directory, runs the copy-in
// docs/pending-workflows/README.md prescribes on the copy (both files, both
// `git rm`s and the two sentences that count what is pending here), and then
// runs this family of tests **in the copy**.
//
// Why it exists. A clause that only holds before the copy-in is a clause that
// fails on the commit the README tells the owner to make, and the window before
// that commit is the only state anyone runs by hand. Two ways of getting it
// wrong are both refused here rather than in the owner's commit: a test that
// reads docs/pending-workflows/console-linux.yml directly fails once its staged
// copy is gone, and a test that pins a remembered count of pending files fails
// as soon as one of them is applied (and again when the next one is staged).
//
// The copy-in is the README's own text, not a retyped copy of it: its five
// commands are checked against the commands the README prints, and the two
// replacement sentences are read out of the fenced blocks in
// `## console-release-linux.yml`. A README that prescribed a command or a
// sentence this test cannot apply fails here, so the instructions and the
// property cannot drift apart. `git` is not run at all -- the operations are
// the file moves the commands make -- so this needs no repository, no branch
// and no index: a copy of the tree is enough.
//
// The selection it runs is the family the staged files reach
// (consoleCopyInRunPattern), which deliberately does not match this test: a
// pattern that did would run this test inside the copy and recurse. That
// selection is checked for coverage first (consoleCopyInUncoveredTests): a test
// in this package that reads one of the moved files and is not selected fails
// here, rather than being silently left out of the run in the copy.
//
// The mutations that prove it: make consoleWorkflowText read only the staged
// path (fatal instead of falling back to the applied one); pin
// TestConsoleLinuxStagingTellsTheOwnerWhatToCopyWhere's count to the "three"
// this change stages; and add a test outside the pattern that reads one of the
// moved files. Each turns this test red -- the first two on the copied tree
// while the staged tree stays green, the third before the copy is even made --
// and nothing else in the suite notices.
func TestStagedLinuxFilesSurviveTheCopyIn(t *testing.T) {
	if !docExists(t, consoleLinuxWorkflowPath) || !docExists(t, consoleReleaseLinuxWorkflowPath) {
		t.Skipf("the copy-in has been applied in this tree (%s or %s is gone): this test needs the staged state to copy from, and on the applied tree it has nothing left to prove -- the suite it would run is the suite running now",
			consoleLinuxWorkflowPath, consoleReleaseLinuxWorkflowPath)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("this test runs the package's own test binary inside the copy, and it cannot find itself (%v)", err)
	}
	if regexp.MustCompile(consoleCopyInRunPattern).MatchString(t.Name()) {
		t.Fatalf("%q matches this test's own name: the run inside the copy would run this test again, in a tree it would copy again, without end", consoleCopyInRunPattern)
	}

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	copyRoot := filepath.Join(t.TempDir(), "fugaro")
	copyTree(t, root, copyRoot)

	// The copy-in, in the order the README gives it: the build check into
	// .github/workflows/ and its staged copy removed; then the replacement over
	// the staged console-release.yml, that staged file into
	// .github/workflows/, and the replacement removed.
	for _, step := range []struct{ from, to string }{
		{consoleLinuxWorkflowPath, appliedConsoleLinuxPath},
		{consoleReleaseLinuxWorkflowPath, consoleReleaseWorkflowPath},
		{consoleReleaseWorkflowPath, appliedConsoleReleasePath},
	} {
		copyFile(t, filepath.Join(copyRoot, step.from), filepath.Join(copyRoot, step.to))
	}
	for _, gone := range []string{consoleLinuxWorkflowPath, consoleReleaseLinuxWorkflowPath} {
		if err := os.Remove(filepath.Join(copyRoot, gone)); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(copyRoot, gone)); !os.IsNotExist(err) {
			t.Fatalf("%s is still there after the copy-in removed it (%v)", gone, err)
		}
	}

	// And the two sentences the same commit has to carry, spliced in from the
	// README's own blocks. `consolePendingSentence` in this file reads both
	// sentences back in the copied tree when the suite runs there.
	sentences := releaseLinuxCopyInSentences(t)
	spliceOut(t, filepath.Join(copyRoot, "docs/pending-workflows/README.md"), sentences.opening,
		"**", "**Two other files stay staged here by design")
	spliceOut(t, filepath.Join(copyRoot, "website/README.md"), sentences.site,
		"`docs/pending-workflows/` now holds", "the website's two manual")

	// Before running anything, the selection is checked for coverage: a test
	// that reads one of the files the copy-in moves and is not matched by the
	// pattern would be a test this proof silently does not run.
	for _, name := range consoleCopyInUncoveredTests(t) {
		t.Errorf("%s reads what the copy-in changes and %q does not select it, so the run inside the copy would never exercise it: widen consoleCopyInRunPattern (or name the test into the family it belongs to), because this test's whole claim is about the tests that read the files a copy-in moves", name, consoleCopyInRunPattern)
	}

	// It runs **in the copy**: this package's own test binary, started from the
	// copied package directory, so every readDoc and every workflow reader above
	// opens the copied tree's file. The binary is the one this test is running
	// in rather than a freshly built one -- the assertions are the same code
	// either way, and only the files under test differ, which is the whole
	// claim -- and compiling this package a second time from inside a test run
	// costs a gigabyte of compiler memory the machine running the suite may not
	// have (the first version of this test was killed for exactly that).
	cmd := exec.Command(exe, "-test.run", consoleCopyInRunPattern, "-test.count", "1", "-test.timeout", "10m")
	cmd.Dir = filepath.Join(copyRoot, "internal", "cli")
	// The environment is this run's, so the child reads the same HOME, cache and
	// configuration rather than reaching for anything of its own.
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err == nil {
		return
	}
	t.Errorf("the suite is red in a tree with the copy-in applied, which is the commit docs/pending-workflows/README.md tells the owner to make: %v\n%s",
		err, consoleTail(string(out), 60))
}

// consoleCopyInRunPattern is what TestStagedLinuxFilesSurviveTheCopyIn runs in
// the copied tree: the console-workflow, docs and lint families, which is every
// test that reads one of the two staged Linux files (the wider form of the
// pattern a run uses day to day, `DesktopConsole|Docs|Lint`, which does not
// spell the standalone Linux tests' names). It must not match the proof test
// itself, and the proof test checks that it does not.
//
// It is kept honest by consoleCopyInSensitive and the coverage check in the
// proof test: a test that reads one of the files the copy-in moves and is not
// selected here fails that check rather than being quietly left out of the run
// in the copy.
const consoleCopyInRunPattern = `Console|Docs|Lint`

// consoleCopyInSensitive is what a copy-in changes, as a test source spells it:
// the staged files by name, the constants that name their homes and the readers
// this family goes through them with, and
// the two helpers this file reads the staging README's opening line through
// (`pendingWorkflows` derives the pending set from the directory, and
// `pendingReadmeOpening` is the paragraph that states it). A test whose body
// mentions any of these is a test the copy-in can turn red.
//
// What it does not cover, deliberately: a test that reads the README's opening
// line or the site's last paragraph without going through one of these -- the
// check is over the text of the tests, and a string this list does not know
// cannot be seen. The families that read them are selected by
// consoleCopyInRunPattern in full, and a new reader is expected to be named into
// one of them.
// It is a regular expression with word boundaries rather than a list of
// substrings: `pendingWorkflows` is a helper here and `pendingWorkflowsReadmePath`
// is a constant in another file, and a substring search cannot tell them apart.
var consoleCopyInSensitive = regexp.MustCompile(`\b(?:console-linux\.yml|console-release-linux\.yml|console-release-feed\.yml|console-release-symbols\.yml|consoleLinuxWorkflowPath|appliedConsoleLinuxPath|consoleReleaseLinuxWorkflowPath|consoleReleaseFeedWorkflowPath|consoleReleaseSymbolsWorkflowPath|consoleFeedText|consoleSymbolsText|consoleReleaseWorkflowPath|appliedConsoleReleasePath|pendingReadmeOpening|pendingWorkflows)\b`)

// consoleCopyInUncoveredTests is every test in this package whose source
// mentions what a copy-in changes and which consoleCopyInRunPattern does not
// select. The proof test refuses to run while it is non-empty: an unselected
// reader is a test whose behaviour in the copied tree nobody has checked, which
// is the whole failure this family was fixed for.
func consoleCopyInUncoveredTests(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("cannot list this package's sources: %v", err)
	}
	pattern := regexp.MustCompile(consoleCopyInRunPattern)
	var uncovered []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		// Split on the top-level `func ` keyword: every test's body is the text
		// up to the next one, which is enough to ask whether that test mentions
		// what the copy-in moves.
		for _, body := range strings.Split(string(raw), "\nfunc ")[1:] {
			test := strings.SplitN(body, "(", 2)[0]
			if !strings.HasPrefix(test, "Test") || test == t.Name() {
				continue
			}
			if consoleCopyInSensitive.MatchString(body) && !pattern.MatchString(test) {
				uncovered = append(uncovered, name+": "+test)
			}
		}
	}
	slices.Sort(uncovered)
	return uncovered
}

// copyInSentences is the pair of replacement sentences the README prescribes for
// the commit that applies both files: this directory's opening line and the
// last paragraph of website/README.md.
type copyInSentences struct {
	opening, site string
}

// The `## console-release-linux.yml` section prints two pairs of follow-up
// sentences, each under the sentence that says when it is the one to write,
// because the two copy-ins of 2026-10-20 can be made in either order: the third
// file staged that day, `mobile-release.yml`, has a copy-in of its own, and the
// pair a commit needs names what the directory is left holding.
const (
	consoleCopyInMobileStagedLabel  = "**If `mobile-release.yml` is still staged when you make this copy-in**"
	consoleCopyInMobileAppliedLabel = "**If it has already been copied in**"
)

// The `## console-linux.yml` section prints a pair of its own for each of the
// same two states, under labels of its own: applying that file leaves a
// different directory than applying the release file does, so the sentences it
// hands the owner cannot be the Linux pair's. Nothing else read those two pairs
// until TestConsoleLinuxStagingTellsTheOwnerWhatToCopyWhere did, and a pair that
// has drifted from its state is a sentence the owner writes into a red tree --
// the two sentences it replaces are what that test checks against this
// directory. The third marker is the paragraph that follows the second pair, and
// it is what tells the two parts apart when they are read one at a time.
const (
	consoleLinuxCopyInMobileStagedLabel  = "**If `mobile-release.yml` is still staged**, it leaves four"
	consoleLinuxCopyInMobileAppliedLabel = "**If `mobile-release.yml` has already been copied in**, applying this file leaves"
	consoleLinuxCopyInOtherOrderLabel    = "**If the console's other two have been applied first**"
)

// readmeSectionPart returns the part of text that runs from `from` to the start
// of the first `to` after it, or to the end of text when `to` is empty. A pair
// of follow-up sentences is read out of the part its own label introduces, so a
// pair printed under the wrong label -- or a label that lost its pair -- fails
// where it is read.
func readmeSectionPart(t *testing.T, what, text, from, to string) string {
	t.Helper()
	start := strings.Index(text, from)
	if start < 0 {
		t.Fatalf("%s no longer prints %q, the sentence that says when its pair of follow-up sentences is the one to write", what, from)
	}
	rest := text[start+len(from):]
	if to != "" {
		if end := strings.Index(rest, to); end >= 0 {
			rest = rest[:end]
		}
	}
	return rest
}

// releaseLinuxCopyInSentences reads those two sentences out of
// `## console-release-linux.yml`'s fenced blocks -- the section that describes
// the commit both copy-ins land in -- and checks the four commands that commit
// runs are the ones this test ran. Both halves are read from the README because
// both are the README's claim about the tree: a prescribed sentence that the
// directory would contradict is not something this test should paper over by
// writing its own.
//
// The section prints a pair for each of the two orders the copy-ins can be made
// in, and this reads the pair for the tree it is run in: `mobile-release.yml`
// staged beside this section's files means the console copy-in is the first of
// the two, and the pair that names it is the one this copy-in leaves.
func releaseLinuxCopyInSentences(t *testing.T) copyInSentences {
	t.Helper()
	const what = "docs/pending-workflows/README.md's `console-release-linux.yml` section"
	doc := readDoc(t, "docs/pending-workflows/README.md")
	section := consoleReadmeSection(t, doc, "## `console-release-linux.yml`")
	for _, command := range []string{
		"cp docs/pending-workflows/console-release-linux.yml docs/pending-workflows/console-release.yml",
		"cp docs/pending-workflows/console-release.yml .github/workflows/console-release.yml",
		"git rm docs/pending-workflows/console-release-linux.yml",
	} {
		if !strings.Contains(section, command) {
			t.Fatalf("docs/pending-workflows/README.md's `console-release-linux.yml` section no longer prints %q, which is a command this test just ran: the copy-in it prescribes and the copy-in applied here have to be the same one", command)
		}
	}
	build := consoleReadmeSection(t, doc, "## `console-linux.yml`")
	for _, command := range []string{
		"cp docs/pending-workflows/console-linux.yml .github/workflows/console-linux.yml",
		"git rm docs/pending-workflows/console-linux.yml",
	} {
		if !strings.Contains(build, command) {
			t.Fatalf("docs/pending-workflows/README.md's `console-linux.yml` section no longer prints %q, which is a command this test just ran", command)
		}
	}
	pair := readmeSectionPart(t, what, section, consoleCopyInMobileStagedLabel, consoleCopyInMobileAppliedLabel)
	if !docExists(t, stagedMobileReleaseWorkflowPath) {
		pair = readmeSectionPart(t, what, section, consoleCopyInMobileAppliedLabel, "")
	}
	return copyInSentences{
		// The marker is the count-free head of the opening-line block rather
		// than a whole phrase of it: the block states a count ("One file is
		// pending", "Two files are pending", ...), and a marker that spelled the
		// count would have to be edited every time a file is staged or applied
		// -- which is the edit the block itself exists to make in one place.
		opening: fencedBlockContaining(t, what, pair, "files are pending"),
		site:    fencedBlockContaining(t, what, pair, "now holds"),
	}
}

// fencedBlockContaining is the one fenced block of a section that contains
// marker, with the block's own leading and trailing whitespace removed: the
// replacement sentences the README prescribes are written as fenced blocks so
// that they are readable as the text they are.
func fencedBlockContaining(t *testing.T, what, section, marker string) string {
	t.Helper()
	var found []string
	var current []string
	inFence := false
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if inFence {
				found = append(found, strings.Join(current, "\n"))
				current = nil
			}
			inFence = !inFence
			continue
		}
		if inFence {
			current = append(current, line)
		}
	}
	if inFence {
		t.Fatalf("%s has an unclosed fenced block: the sentences this test splices in are read from it", what)
	}
	var matches []string
	for _, block := range found {
		if strings.Contains(block, marker) {
			matches = append(matches, block)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("%s has %d fenced blocks containing %q, and this test needs exactly one to splice in", what, len(matches), marker)
	}
	return strings.TrimSpace(matches[0])
}

// spliceOut replaces the span of a file in the tree copy that runs from the
// first `from` to the start of the first `to` after it, and writes the file
// back. It is how the two prescribed sentences are applied: the README hands
// over the text to write, and the markers around the sentence it replaces are
// its boundaries -- the head of the claim, and the words that follow it and do
// not move. One space separates the two pieces, which is the separator both
// sentences have today. `to` is required to follow `from`, so a README whose
// wording moved fails here rather than splicing a paragraph into some other
// place.
func spliceOut(t *testing.T, path, replacement, from, to string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	start := strings.Index(text, from)
	end := -1
	if start >= 0 {
		if rel := strings.Index(text[start:], to); rel >= 0 {
			end = start + rel
		}
	}
	if start < 0 || end < start {
		t.Fatalf("%s does not carry the text the README's copy-in splice needs (%q ... %q): the prescribed sentence and the sentence it replaces have to meet", path, from, to)
	}
	out := text[:start] + strings.TrimSpace(replacement) + " " + text[end:]
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
}

// copyTree copies a directory tree, skipping two directories that a test never
// reads and that would dominate the copy: .git (history, not the tree) and
// node_modules (the console's installed dependencies, which no Go test here
// reads -- the files they are checked against are in the repository).
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if info.IsDir() {
			if rel == ".git" || rel == "node_modules" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		return copyFileErr(filepath.Join(dst, rel), path, info.Mode().Perm())
	})
	if err != nil {
		t.Fatalf("copying the tree to %s failed: %v", dst, err)
	}
}

// copyFile copies one file into a tree copy, and copyFileErr is the same for
// copyTree's walk, which reports through the walk's error rather than through
// t.Fatal.
func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	info, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := copyFileErr(dst, src, info.Mode().Perm()); err != nil {
		t.Fatalf("copying %s to %s failed: %v", src, dst, err)
	}
}

func copyFileErr(dst, src string, mode os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	// The mode travels with the file: the executable bits are read by tests in
	// this family (the console's notarise script, scripts/release-gate.sh), and a
	// copy that dropped them would fail them for the wrong reason.
	return os.WriteFile(dst, data, mode)
}

// consoleTail is the last n lines of the run in the copy. The child is not run
// with -test.v -- a verbose run writes a line per passing test, which is
// hundreds of lines of chatter around the failure -- and the testing package
// prints a failing test's own output either way, so what comes back is the
// `--- FAIL` blocks and the summary, and the assertion that matters is near the
// end of it.
func consoleTail(out string, n int) string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) > n {
		lines = append([]string{fmt.Sprintf("... (%d lines before these)", len(lines)-n)}, lines[len(lines)-n:]...)
	}
	return strings.Join(lines, "\n")
}

// consoleFuseHookPath is the module electron-builder calls after each platform
// build: the hook that flips the six fuses into the packed executable and reads
// the wire back. It is read here as bytes, like every other console/ path in
// this file.
const consoleFuseHookPath = "console/src/main/fuses.ts"

// consoleSpawnPath is the module that resolves the CLI binary and builds the
// child's environment, and the one table (§3.4 rules 4 and 5's lists, per
// platform) the design's prose is held to.
const consoleSpawnPath = "console/src/main/spawn.ts"

// TestDocsConsoleLinuxFusesGoThroughThePackerContext pins the mechanism §7.6
// states for Linux against the code that implements it, because the two were
// written apart and disagreed: the design said the per-platform choice was made
// by `process.platform`, and the hook makes it from the packer's context.
//
// The difference is not cosmetic. `afterPack` is a **packaging** hook: it runs
// in electron-builder's own process on the machine doing the packing, so
// `process.platform` there is the build host, not the platform being packed --
// a hook that switched on it would resolve the Linux executable while packing
// on Linux and the macOS one under any cross-pack, and the design's claim that
// the two platforms "cannot drift into different postures" rests on the hook
// reading the one value the packer is stating about the artifact in front of it.
// `fuseTarget` switches on `context.electronPlatformName` for exactly that
// reason, and the packer's docs are why the field exists.
//
// The second half is the same claim about the hook's own prose: the comment over
// `AfterPackContext` used to justify reading `packager.executableName` with
// "it exists on every packager (`PlatformPackager` sets it)", which is not true
// -- the field is declared and assigned by `LinuxPackager` alone, and
// `PlatformPackager`'s own reads of an executable name guard on
// `this instanceof LinuxPackager` and fall back to `appInfo.productFilename`
// elsewhere. The optional field and the `sanitizedName.toLowerCase()` fallback
// are right either way; only the reason was wrong, and a reason that is wrong is
// how the next reader concludes the field is safe to read on darwin.
//
// The mutations that prove it: put `process.platform` back into §7.6's fuse
// sentence; change `fuseTarget`'s switch to `process.platform`; and write the
// "exists on every packager" justification back over `AfterPackContext`. Each
// fails one half of this test and nothing else.
func TestDocsConsoleLinuxFusesGoThroughThePackerContext(t *testing.T) {
	design := readDoc(t, "docs/design/desktop-console.md")
	section := consoleReadmeSection(t, design, "### 7.6 Linux, unsigned")
	// Flattened, because the claims below are made in prose that the file wraps:
	// a phrase this test refuses must not be able to hide behind a line break.
	hook := strings.Join(strings.Fields(readDoc(t, consoleFuseHookPath)), " ")

	// §7.6's fuse sentence: the same hook, and the packer's own statement of
	// which platform is being packed.
	for _, want := range []string{"afterPack", "electronPlatformName"} {
		if !strings.Contains(section, want) {
			t.Errorf("docs/design/desktop-console.md's §7.6 does not name %q in its fuse sentence: what decides which executable is patched on each platform is the claim that keeps the two platforms' postures identical, and %s is what decides it", want, consoleFuseHookPath)
		}
	}
	if strings.Contains(section, "process.platform") {
		t.Errorf("docs/design/desktop-console.md's §7.6 says `process.platform` decides which executable the fuse hook patches: the hook runs in the packer's process on the build host rather than in the app, so that value is the machine doing the packing, and the hook reads electron-builder's own `context.electronPlatformName` (fuseTarget in %s) instead", consoleFuseHookPath)
	}

	// And the hook does what the design says it does: one platform switch, on
	// the packer's value, with no reach for the build host's platform anywhere in
	// it.
	if !strings.Contains(hook, "switch (context.electronPlatformName)") {
		t.Errorf("%s no longer switches on `context.electronPlatformName`: that is the value electron-builder states the artifact's platform with, and the one §7.6 says the platform choice is made from", consoleFuseHookPath)
	}
	if strings.Contains(hook, "process.platform") {
		t.Errorf("%s reads `process.platform`: this module runs inside the packer, where that is the build host rather than the platform being packed, and a fuse hook that patched the other platform's executable would produce a binary with the fuses unset", consoleFuseHookPath)
	}

	// The prose half: the field belongs to LinuxPackager, and the file has to
	// say so rather than claim it is on every packager.
	if !strings.Contains(hook, "LinuxPackager") {
		t.Errorf("%s no longer names `LinuxPackager`: `packager.executableName` is declared and assigned by that class alone, with `appInfo.sanitizedName.toLowerCase()` as its fallback, and the hook's reason for reading it has to say where it comes from", consoleFuseHookPath)
	}
	if strings.Contains(hook, "exists on every packager") {
		t.Errorf("%s claims `executableName` \"exists on every packager\": it does not -- `LinuxPackager` declares and assigns it, and the base `PlatformPackager` guards its own reads with `this instanceof LinuxPackager` and falls back to `appInfo.productFilename` elsewhere", consoleFuseHookPath)
	}
}

// TestDocsConsoleSpawnListsCarryBothPlatforms pins §3.4's rules 4 and 5 -- the
// two rules that state where the console looks for the CLI and what `PATH` it
// gives the child -- to the table that implements them, per platform.
//
// The gap it closes was a citation with nothing behind it. §7.6 rule 1 sends the
// reader to "§3.4 rule 4" for "Linux's own well-known locations", and the plan's
// Task 5.1 note (the 2026-10-20 Linux amendment) names "§3.4 rules 4 and 5" as
// where the per-platform resolution and child-`PATH` lists live -- and both rules
// stated only the macOS lists. Linux's were in console/README.md, in
// console/src/main/spawn.ts and in its tests, and nowhere in the document a
// reader was sent to. So each rule now carries the Linux amendment the way D10's
// cell carries its own, and this test holds both halves to spawn.ts's
// DARWIN_LOCATIONS and LINUX_LOCATIONS -- the one table the app resolves against
// (PLATFORM_LOCATIONS, through `locationsFor`).
//
// It is a per-platform, per-list, order-preserving comparison rather than a
// "mentions Linux" check: rule 4's order IS the resolution order (a configured
// path, then the platform's prefixes, and only then the app's own `PATH`) and
// rule 5's is the order of the child's `PATH`, so a list whose entries are all
// present in the wrong order is a different answer to the same question rather
// than a rewording of the same one.
//
// The mutations that prove it: delete the Linux sentence from rule 4 (or from
// rule 5); move one entry of spawn.ts's LINUX_LOCATIONS without moving it in the
// design; and give either platform the other's Homebrew prefix.
func TestDocsConsoleSpawnListsCarryBothPlatforms(t *testing.T) {
	design := readDoc(t, "docs/design/desktop-console.md")
	spawn := readDoc(t, consoleSpawnPath)

	// §3.4's own rules, each bounded by the next numbered rule so that a phrase
	// in rule 5 cannot satisfy rule 4's list.
	recommendation := consoleReadmeSection(t, design, "### 3.4 The recommendation")

	for _, tc := range []struct {
		platform string
		constant string
		field    string
		rule     int
	}{
		{"macOS", "DARWIN_LOCATIONS", "binaryPrefixes", 4},
		{"Linux", "LINUX_LOCATIONS", "binaryPrefixes", 4},
		{"macOS", "DARWIN_LOCATIONS", "childPathPrefixes", 5},
		{"Linux", "LINUX_LOCATIONS", "childPathPrefixes", 5},
	} {
		want := consoleSpawnList(t, spawn, tc.constant, tc.field)
		rule := consoleNumberedRule(t, recommendation, tc.rule)
		if !consoleContainsRun(consoleBacktickedPaths(rule), want) {
			t.Errorf(
				"docs/design/desktop-console.md's §3.4 rule %d does not state %s's %s in the order %s gives it (%s): rule 4 is where the console looks for the CLI and rule 5 is the `PATH` the child gets, and §7.6 and the plan send a reader here for Linux's own lists",
				tc.rule, tc.platform, tc.field, consoleSpawnPath, strings.Join(want, ", "),
			)
		}
	}

	// And the citations a reader follows resolve to those rules: §7.6 rule 1 is
	// where the Linux section says which list the CLI is resolved from, and the
	// plan's 2026-10-20 note is where the task says which rules carry the lists.
	linux := consoleReadmeSection(t, design, "### 7.6 Linux, unsigned")
	if !strings.Contains(linux, "§3.4 rule 4") {
		t.Errorf("§7.6 no longer cites §3.4 rule 4 for the locations Linux's CLI is resolved from, and rule 4 is where Linux's own list is stated")
	}
	if plan := readDoc(t, "docs/plans/2026-10-15-desktop-console.md"); !strings.Contains(plan, "§3.4 rules 4 and 5") {
		t.Errorf("the plan's 2026-10-20 Linux note no longer cites §3.4 rules 4 and 5 for the per-platform resolution and child-`PATH` lists, which are the two rules those lists are stated in")
	}
}

// TestWebsiteConsoleTestHeadersCountTheirItems pins each console test file's own
// account of how many things it checks to the list printed under it.
//
// The header is the first thing a reader of those files meets, and it is a claim
// like every other claim in this repository: `website/test/console.mjs` said
// "Seven things are checked here" over a list of nine -- the optional Linux block
// and the site-wide download gate were added under it and the count was not --
// which is the drift nobody notices by reading, and the one a reader trusts
// instead of counting. The rule is mechanical: the header's numbered items run
// 1..N in order, and the sentence says N.
//
// The mutations that prove it: write "Seven" back over the nine in
// `website/test/console.mjs`, and number two of `console-option.mjs`'s items the
// same way.
func TestWebsiteConsoleTestHeadersCountTheirItems(t *testing.T) {
	claimedRe := regexp.MustCompile(`(?m)^// .*?([A-Z][a-z]+) things are checked`)
	itemRe := regexp.MustCompile(`(?m)^//\s+(\d+)\. \*\*`)
	words := []string{"", "One", "Two", "Three", "Four", "Five", "Six", "Seven", "Eight", "Nine", "Ten", "Eleven", "Twelve"}

	for _, path := range []string{"website/test/console.mjs", "website/test/console-option.mjs"} {
		doc := readDoc(t, path)
		claimed := claimedRe.FindStringSubmatch(doc)
		if claimed == nil {
			t.Errorf("%s no longer says how many things it checks (\"<N> things are checked here\"), and that sentence is where a reader takes the list's length from", path)
			continue
		}
		items := itemRe.FindAllStringSubmatch(doc, -1)
		if len(items) == 0 {
			t.Errorf("%s says %q things are checked and carries no numbered item, so the count is about nothing", path, claimed[1])
			continue
		}
		for i, m := range items {
			if n, err := strconv.Atoi(m[1]); err != nil || n != i+1 {
				t.Errorf("%s's header numbers its items %s at position %d: the list is 1..N in order, and a skipped or repeated number is a list nobody can count", path, m[1], i+1)
			}
		}
		want := 0
		for n, word := range words {
			if word == claimed[1] {
				want = n
			}
		}
		if want == 0 {
			t.Errorf("%s says %q things are checked, which is not a count this test reads (One to Twelve): write it as a word in that range, or extend the list here", path, claimed[1])
			continue
		}
		if len(items) != want {
			t.Errorf("%s says %q things are checked and lists %d: the sentence and the list are one fact, and the sentence is the one that drifts when an item is added under it", path, claimed[1], len(items))
		}
	}
}

// consoleNumberedRule is item n of a markdown numbered list, from its own marker
// to the next item's.
func consoleNumberedRule(t *testing.T, doc string, n int) string {
	t.Helper()
	start := strings.Index(doc, fmt.Sprintf("\n%d. **", n))
	if start < 0 {
		t.Fatalf("the section carries no numbered rule %d", n)
	}
	rest := doc[start+1:]
	if end := strings.Index(rest, fmt.Sprintf("\n%d. **", n+1)); end >= 0 {
		return rest[:end]
	}
	return rest
}

// consoleBacktickedPaths is the backticked tokens of a piece of prose that are
// paths -- absolute or home-relative -- in the order they are written. A
// backticked word that is not a path (`PATH`, a flag, a Go identifier) is not
// collected.
func consoleBacktickedPaths(text string) []string {
	var out []string
	for _, m := range regexp.MustCompile("`([^`]+)`").FindAllStringSubmatch(text, -1) {
		if strings.HasPrefix(m[1], "/") || strings.HasPrefix(m[1], "~") {
			out = append(out, m[1])
		}
	}
	return out
}

// consoleContainsRun reports whether want appears in got as a contiguous run, in
// order: these lists are orders, so a membership check would accept an answer
// with the right directories in the wrong sequence.
func consoleContainsRun(got, want []string) bool {
	for i := 0; i+len(want) <= len(got); i++ {
		if slices.Equal(got[i:i+len(want)], want) {
			return true
		}
	}
	return false
}

// consoleSpawnList is one list of console/src/main/spawn.ts's platform table, as
// the strings the code carries: the field's Object.freeze([...]) inside the named
// constant, read rather than remembered.
func consoleSpawnList(t *testing.T, src, constant, field string) []string {
	t.Helper()
	at := strings.Index(src, "export const "+constant)
	if at < 0 {
		t.Fatalf("%s no longer declares %s, the constant the design's §3.4 rules 4 and 5 state", consoleSpawnPath, constant)
	}
	block := src[at:]
	if next := strings.Index(block[len("export const "+constant):], "\nexport const "); next >= 0 {
		block = block[:len("export const "+constant)+next]
	}
	m := regexp.MustCompile(regexp.QuoteMeta(field) + `:\s*Object\.freeze\(\[([^\]]*)\]\)`).FindStringSubmatch(block)
	if m == nil {
		t.Fatalf("%s's %s carries no %s: Object.freeze([...]) list", consoleSpawnPath, constant, field)
	}
	var out []string
	for _, raw := range strings.Split(m[1], ",") {
		if v := strings.Trim(strings.TrimSpace(raw), "'\""); v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		t.Fatalf("%s's %s.%s is empty, and an empty list is nothing for the design to agree with", consoleSpawnPath, constant, field)
	}
	return out
}

// --- Check 59a and 59b: the console's owner-run live checks (plan Task 4.3) ---
//
// The four tests below pin the two checklist sections Task 4.3 writes. They are
// named TestCheck59… rather than TestChecklist… on purpose: the package's
// TestChecklist… functions are enumerated by name in the compute-backend plan's
// open item 9 and held to the tree in both directions by
// TestDocsComputeBackendNamesEveryChecklistTest, and that enumeration also
// records that the guard test the project-centric design names,
// TestChecklistChecksAreGuarded, is NOT in this tree. These four are this task's
// own, so they wear the check's number instead.

// check59Path is the live checklist, the only document Task 4.3 writes.
const check59Path = "docs/gcp-live-checklist.md"

// check59SectionAt is the one place the two headings are spelled, so a rename
// of either fails here rather than in four places at once.
const (
	check59aHeading = "## Check 59a:"
	check59bHeading = "## Check 59b:"
)

// check59Sections reads both halves of the number. A section that has gone
// missing fails in docSection with the heading that could not be found.
func check59Sections(t *testing.T) (doc, a, b string) {
	t.Helper()
	doc = readDoc(t, check59Path)
	return doc,
		docSection(t, check59Path, doc, check59aHeading),
		docSection(t, check59Path, doc, check59bHeading)
}

// check59FactLines returns a section's FACT lines: the lines that carry the word
// FACT, a command in backticks and the `→` that names what that command must
// print. The shape is the assertion rather than the decoration — round 2 of the
// plan found assertion (4) written as a bare sentence with nothing under it, so
// a line without a command and an expected output is deliberately not a FACT
// line at all here.
func check59FactLines(section string) []string {
	var out []string
	for _, line := range strings.Split(section, "\n") {
		if strings.Contains(line, "FACT:") && strings.Contains(line, "→") {
			out = append(out, line)
		}
	}
	return out
}

// check59FactLine returns the one FACT line naming cmd, and fails the test when
// there is none: a command the assertion rests on and no FACT line for it is
// exactly the defect TestCheck59AssertionFourIsFactLined exists to catch.
func check59FactLine(t *testing.T, facts []string, cmd string) string {
	t.Helper()
	for _, line := range facts {
		if strings.Contains(line, cmd) {
			return line
		}
	}
	t.Fatalf("Check 59a's assertion (4) carries no FACT line naming `%s`: a safety property with no command and no expected output under it is the thing the plan's round 2 found", cmd)
	return ""
}

// check59RestoreBlock returns the fenced bash block of section that is its
// restore step: the block whose own header line names it — `# === Check 59a:
// Restore.` — and not merely a block that mentions the word. Every other block
// is a step, and a restore step that is not a block of its own cannot be held to
// the one rule it exists under.
//
// "Mentions Restore" is not enough, and that was this helper's first version's
// bug: the setup block comes first and its own header says "RE-RUN THIS WHOLE
// BLOCK at the start of every step (1, 2, 3, 4, 5, Restore)", so the setup
// block was returned for both sections and
// TestCheck59RestoreStepSparesPreexistingIdentities never scanned either real
// Restore block — a `security delete-identity` inserted into one left the test
// green, which is the exact mutation it exists to fail. The header's own first
// line is the fact to read, so it is read.
func check59RestoreBlock(t *testing.T, heading, section string) []string {
	t.Helper()
	label := strings.TrimSuffix(strings.TrimPrefix(heading, "## "), ":")
	want := "# === " + label + ": Restore."
	for _, block := range bashBlocks(section) {
		for _, line := range block {
			if strings.HasPrefix(strings.TrimSpace(line), want) {
				return block
			}
		}
	}
	t.Fatalf("%s has no fenced bash block whose own header line names its Restore step (`%s`), so nothing holds the restore to sparing what it did not create", heading, want)
	return nil
}

// TestCheck59IsSplitIntoAAndB asserts the number is written as two sections, as
// design D12 rules and the plan's Task 4.3 requires: `## Check 59a` and
// `## Check 59b`, one number with two sections, the way Check 43 carries 43a to
// 43f. The split is not tidiness — the two halves cannot run at the same moment,
// because 59b needs a published update feed and 59a does not — so a later edit
// that folds them back into one section that cannot run fails a named test
// rather than shipping a check nobody can execute.
func TestCheck59IsSplitIntoAAndB(t *testing.T) {
	doc, a, b := check59Sections(t)

	// The single section the split replaces, in either of its spellings.
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "## Check 59:") || line == "## Check 59" {
			t.Errorf("%s carries %q: Check 59 is one number with TWO sections (59a and 59b), and a single section is the version that cannot run", check59Path, line)
		}
	}
	if strings.Contains(doc, "## Check 59 ") {
		t.Error("the checklist carries a `## Check 59 ` heading: 59 is written as 59a and 59b and never as a bare section")
	}

	// 59a is the first signed build and the safety assertion, and it says so.
	for _, want := range []string{
		"No agent performs any step",
		"NOT RUN",
		"run by you",
		"never `belong`",
	} {
		if !strings.Contains(a, want) {
			t.Errorf("Check 59a does not say %q", want)
		}
	}

	// 59b's FIRST LINES say the two things that make it unrunnable today: it
	// needs a published update feed, and it runs after the first tag that
	// publishes one. Both facts are the reason the two halves are two sections,
	// so they belong where a reader meets the section, not at its end.
	head := b
	if len(head) > 1200 {
		head = head[:1200]
	}
	for _, want := range []string{
		"NOT RUNNABLE",
		"published feed",
		"Task 4.4",
		"0.116.0",
		"second",
		"installed by hand",
	} {
		if !strings.Contains(head, want) {
			t.Errorf("Check 59b's first lines do not say %q: the section's opening is where the two preconditions that stop it running today have to be read", want)
		}
	}

	// Both rows of the summary table, each saying whose check it is.
	rows := map[string]string{}
	for _, line := range strings.Split(doc, "\n") {
		for _, n := range []string{"| 59a |", "| 59b |"} {
			if strings.HasPrefix(line, n) {
				rows[n] = line
			}
		}
	}
	if len(rows) != 2 {
		t.Fatalf("the checklist's summary table carries %d of Check 59's two rows (%v): the split is one number and two rows", len(rows), rows)
	}
	for _, n := range []string{"| 59a |", "| 59b |"} {
		for _, want := range []string{"run by you", "NOT RUN", "never `belong`"} {
			if !strings.Contains(rows[n], want) {
				t.Errorf("the `%s` row does not carry %q", n, want)
			}
		}
	}
	if !strings.Contains(rows["| 59a |"], "0.110.0") {
		t.Error("the `| 59a |` row does not name the published release 59a runs against, which is the fact that makes it runnable today")
	}
	if !strings.Contains(rows["| 59b |"], "0.116.0") {
		t.Error("the `| 59b |` row does not name 0.116.0, the build the check installs by hand and updates away from (D771)")
	}
}

// TestCheck59bPreconditionsAreTheUpdaterBuilds pins D771 -- the owner's ruling
// of 2026-10-30 -- as the section's own preconditions: the check runs against
// `OLD` = 0.116.0, the first build that carries an updater and so the first that
// can be updated, installed by hand because it cannot update itself, and `NEW` =
// the first console release after it. No release is cut only for the check. The
// pair the section carried before -- `console-v0.110.0` and `console-v0.111.0`
// -- named a build with no updater at all and a release that is not the one
// after the first updater build, so naming either here again fails this test.
//
// The same ruling's U3 and U3b, and D768 and D770, are the section's new
// assertions, each pinned as the fact a reader has to find: no `.updaterId`
// under `userData` and no `x-user-staging-id` on any request; the app's own off
// switch stops the checks; nothing installs on quit, and an update installs only
// through the notice's "Restart to update"; and the two updater-owned locations
// appear only after a download.
func TestCheck59bPreconditionsAreTheUpdaterBuilds(t *testing.T) {
	_, _, b := check59Sections(t)

	head := b
	if len(head) > 2400 {
		head = head[:2400]
	}
	for _, want := range []string{
		"0.116.0",
		"installed by hand",
		"first console release after",
		"release is cut only for this check",
	} {
		if !strings.Contains(head, want) {
			t.Errorf("Check 59b's opening does not say %q: D771 is what the check's two preconditions are, and the section's opening is where a reader meets them", want)
		}
	}

	for _, bad := range []string{"0.111.0", "console-v0.110.0", "Mango", "Cherry"} {
		if strings.Contains(b, bad) {
			t.Errorf("Check 59b still names %q: D771's two builds are 0.116.0 and the first console release after it, and the section may not carry the pair the ruling replaces", bad)
		}
	}

	for _, want := range []string{
		"OLD=0.116.0",
		".updaterId",
		"x-user-staging-id",
		"hardenUpdater",
		"fugaro-console-updater",
		"ShipIt",
		"Restart to update",
		"on quit",
		"`updates.json`",
		"Check for Updates Automatically",
		"appear only after a download",
	} {
		if !strings.Contains(b, want) {
			t.Errorf("Check 59b does not carry %q: D768, D770, U3 and U3b are what the check now asserts, and an assertion a reader cannot find is one nobody runs", want)
		}
	}

	// The two opt-outs the console actually honours are the environment variable
	// and the app's own switch -- never the CLI's `update_check`, which the
	// console does not read (D769).
	if !strings.Contains(b, "FUGARO_NO_UPDATE_CHECK=1") {
		t.Error("Check 59b does not name `FUGARO_NO_UPDATE_CHECK=1` as one of the two opt-outs it exercises")
	}
	if strings.Contains(b, "update_check: false") {
		t.Error("Check 59b still exercises a local `update_check: false`: the console never reads the CLI's `update_check` (D769), so the switch it honours is its own")
	}
}

// TestCheck59AssertionFourIsFactLined is the test that makes assertion (4) an
// assertion rather than a sentence: the safety claim — that with the console
// pointed at the sandbox installation it spawned nothing for `belong` — is only
// as good as the commands under it, and each of those commands has to carry the
// output it must produce.
//
// The three jq keys are the ones Task 1.3's spawnlog.test.ts pins the schema to
// (`ts`, `argv`, `cwd`, `project`), so the check and the writer cannot drift; and
// the keys that are NOT fields of anything — `.gcp_project` (D13's installation
// tuple is a project name, a config path, a binary path and a directory),
// `.spawns` and `.repository` — must not be read, because a jq over a key the
// schema does not define prints null for every line and proves nothing.
func TestCheck59AssertionFourIsFactLined(t *testing.T) {
	_, a, _ := check59Sections(t)
	facts := check59FactLines(a)

	// Step 1 and step 4: the same commands, opposite expected outputs. The
	// mutation control is what makes the zeroes mean something.
	for _, req := range []struct{ cmd, want string }{
		{`test -s "${SPAWNLOG}"`, "`0`"},
		{`grep -c belong "${SPAWNLOG}"`, "`0`"},
		{`grep -c edgeappinc "${SPAWNLOG}"`, "`0`"},
	} {
		line := check59FactLine(t, facts, req.cmd)
		if !strings.Contains(line, req.want) {
			t.Errorf("Check 59a's FACT line for `%s` does not name %s as its expected output:\n  %s", req.cmd, req.want, line)
		}
	}

	// Step 2 and step 3: every spawned argv resolves to the sandbox
	// installation, from the sandbox checkout, for the sandbox project.
	for _, cmd := range []string{
		`jq -r '.argv[0]' "${SPAWNLOG}"`,
		`jq -r '.project' "${SPAWNLOG}"`,
		`jq -r '.cwd' "${SPAWNLOG}"`,
	} {
		if !strings.Contains(check59FactLine(t, facts, cmd), "→") {
			t.Errorf("Check 59a's FACT line for `%s` carries no expected output", cmd)
		}
	}

	// The mutation control, whose expected output is 1 for the same two greps
	// that must say 0 above.
	control := check59FactLine(t, facts, `grep -c belong "${SPAWNLOG}.test"`)
	if !strings.Contains(control, "`1`") {
		t.Errorf("Check 59a's mutation control does not name `1` as its expected output, so it cannot show that the command which must say 0 says 1 when the thing it looks for is there:\n  %s", control)
	}
	if !strings.Contains(a, `printf`) || !strings.Contains(a, "${SPAWNLOG}.test") {
		t.Error("Check 59a has no step that appends a `belong` record to a COPY of the log: the mutation control is what makes the three zeroes evidence")
	}

	// The launch: assertion (4) is unrunnable without the variable, and a Finder
	// or Dock launch inherits launchd's environment rather than the shell's, so
	// the app is started from the terminal with the variable already exported.
	launch := "export FUGARO_CONSOLE_SPAWN_LOG=\"${SPAWNLOG}\"\n" +
		"\"/Applications/Fugaro Console.app/Contents/MacOS/Fugaro Console\" &"
	if !strings.Contains(a, launch) {
		t.Error("Check 59a does not export FUGARO_CONSOLE_SPAWN_LOG and start the built bundle by its executable path on the two adjacent lines: without the export the log does not exist, and a Finder or Dock launch never sees the variable")
	}
	if strings.Contains(a, "open \"/Applications/Fugaro Console.app\" &") {
		t.Error("Check 59a starts the logged run with `open`, which does not forward the shell's environment to the launched app")
	}

	// The bundle and the executable are the names Task 4.1's builder
	// configuration pins, read out of it rather than typed here.
	productName := consoleProductName(t)
	if want := "/Applications/" + productName + ".app/Contents/MacOS/" + productName; !strings.Contains(a, want) {
		t.Errorf("Check 59a does not launch %q, the executable name console/package.json's build.productName pins", want)
	}

	// And no jq over a key no shape defines.
	for _, line := range strings.Split(a, "\n") {
		if !strings.Contains(line, "jq") {
			continue
		}
		for _, key := range []string{".gcp_project", ".spawns", ".repository"} {
			if strings.Contains(line, key) {
				t.Errorf("Check 59a reads `%s` with jq: no shape has that key, so the read prints null for every line and proves nothing\n  %s", key, line)
			}
		}
	}
}

// TestCheck59NeverNamesBelongAsATarget is the machine-checkable half of Global
// Constraint 6 for these sections: the string `belong` is never an argument to a
// command the check runs, so a later edit cannot "verify" the negative the
// forbidden way — `fugaro --project belong budget show` is red here, and that is
// the mistake this test exists to catch.
//
// The reconciliation round 3 asked for is in the permitted set. Round 2's
// version allowed only a prose prohibition and the `grep -c belong` FACT line,
// which forbids the one line the mutation control must write: step 4 appends a
// record whose project is `belong` to a COPY of the log. So exactly three
// shapes are permitted, and nothing else:
//
//  1. prose outside a fenced block, stating the prohibition (which is why every
//     such line has to carry the word "never");
//  2. a comment inside a bash block — the string in a line that runs nothing;
//  3. the step-1 grep line, and the single mutation-control `printf` whose
//     output is redirected into `${SPAWNLOG}.test`.
//
// Everything else fails, most importantly any other command line naming it and
// any `--project belong` at all.
func TestCheck59NeverNamesBelongAsATarget(t *testing.T) {
	_, a, _ := check59Sections(t)

	// Which lines are inside a fenced block, and which are prose. A ```bash
	// block is the only kind this section may use for a command, which is what
	// makes "inside a block" a fact the test can read: a line between an opening
	// and a closing fence is a command or a comment about one.
	sectionLines := strings.Split(a, "\n")
	inBlock := make([]bool, len(sectionLines))
	fenced := false
	for i, line := range sectionLines {
		if isFence(line) {
			fenced = !fenced
			continue
		}
		inBlock[i] = fenced
	}

	for i, line := range sectionLines {
		if !strings.Contains(line, "belong") {
			continue
		}
		trimmed := strings.TrimSpace(line)
		switch {
		case inBlock[i]:
			// A command line inside a block. Only three are permitted: a
			// comment (which runs nothing), the step-1 grep, and the mutation
			// control.
			if strings.HasPrefix(trimmed, "#") {
				continue
			}
			if strings.Contains(line, `grep -c belong "${SPAWNLOG}"`) {
				continue
			}
			if strings.Contains(line, "printf") && strings.Contains(line, "${SPAWNLOG}.test") {
				continue
			}
			t.Errorf("Check 59a's bash line %d names `belong` and runs something:\n  %s\nthe only command lines that may name it are the step-1 grep and the mutation control's printf into a copy", i+1, trimmed)
		default:
			// Prose. Every such line states the prohibition, and the word
			// "never" is what makes that a claim this test can read rather
			// than a sentence it hopes is one.
			if !strings.Contains(line, "never") {
				t.Errorf("Check 59a's prose line %d names `belong` without stating the prohibition:\n  %s", i+1, trimmed)
			}
		}
	}

	if strings.Contains(a, "--project belong") {
		t.Error("Check 59a carries `--project belong`, which is the forbidden way to verify this negative")
	}
	for _, bad := range []string{"belong/", "/belong"} {
		if strings.Contains(a, bad) {
			t.Errorf("Check 59a carries %q: the scratch copy is `${SPAWNLOG}.test`, so no path, URL or file name needs the name in it at all", bad)
		}
	}
	if !strings.Contains(a, "no step of this check touches `belong`") {
		if !strings.Contains(a, "never touches `belong`") {
			t.Error("Check 59a does not state the prohibition in its own text: the permitted prose shape is a sentence saying no step touches that installation")
		}
	}
}

// TestCheck59RestoreStepSparesPreexistingIdentities is the mutation-proof half
// of the restore rule: a restore step that "removes the keychain item it was
// signed with" would delete the owner's own signing certificate — the identity
// the whole pipeline depends on, which predates the check by design. So neither
// section's restore step may invoke `security delete-identity`,
// `security delete-certificate` or `security delete-keychain`, and both have to
// record what existed before they started so the rule is enforceable rather than
// remembered.
func TestCheck59RestoreStepSparesPreexistingIdentities(t *testing.T) {
	_, a, b := check59Sections(t)

	for _, section := range []struct{ heading, text string }{
		{check59aHeading, a},
		{check59bHeading, b},
	} {
		label := strings.TrimSuffix(section.heading, ":")
		restore := check59RestoreBlock(t, section.heading, section.text)
		for _, line := range restore {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "#") {
				continue
			}
			for _, banned := range []string{"security delete-identity", "security delete-certificate", "security delete-keychain"} {
				if strings.Contains(line, banned) {
					t.Errorf("%s's Restore invokes `%s`:\n  %s\nthe identity the check did not create is not the check's to remove", label, banned, trimmed)
				}
			}
		}
		// The pre-check record, which is what makes "only what it created" a
		// rule rather than an intention.
		if !strings.Contains(section.text, "security find-identity -v -p codesigning") {
			t.Errorf("%s does not FACT-line `security find-identity -v -p codesigning`, so nothing records which identities predated it", label)
		}
		if !strings.Contains(section.text, "never deleted") {
			t.Errorf("%s does not say a signing identity present before the check is never deleted", label)
		}

		// The app half of the same rule, held the same way, and this is the half
		// that bites hardest: both sections install a bundle into /Applications,
		// so a Restore that removes whatever sits at that path destroys the
		// operator's own copy on exactly the run whose install guard stopped. The
		// stamp is the section's own record that it installed it, and the removal
		// has to be behind that.
		productName := consoleProductName(t)
		// Written by the install step, and written BEFORE the Restore that reads
		// it: a stamp the Restore writes itself would gate the removal on nothing.
		stampWrite := strings.Index(section.text, `> "${CHK:?}/installed-by-this-check.txt"`)
		restoreAt := strings.Index(section.text, "# === "+strings.TrimPrefix(label, "## ")+": Restore.")
		if stampWrite < 0 || restoreAt < 0 || stampWrite > restoreAt {
			t.Errorf("%s installs an app into /Applications and its install step writes no stamp saying it did (stamp write at %d, Restore at %d): without it the Restore cannot tell this check's own install from one that was there before", label, stampWrite, restoreAt)
		}
		if !strings.Contains(section.text, `[ ! -e "$INSTALLED" ] || {`) {
			t.Errorf("%s does not stop before installing when /Applications/%s.app already exists", label, productName)
		}
		stampAt, removeAt := -1, -1
		for i, line := range restore {
			if stampAt < 0 && strings.Contains(line, "installed-by-this-check.txt") {
				stampAt = i
			}
			if removeAt < 0 && strings.Contains(line, "rm -rf") && strings.Contains(line, "$INSTALLED") {
				removeAt = i
			}
		}
		switch {
		case removeAt < 0:
			t.Errorf("%s's Restore never removes the app this check installs, so a completed run leaves it in /Applications", label)
		case stampAt < 0 || stampAt > removeAt:
			t.Errorf("%s's Restore removes /Applications/%s.app without reading the stamp that says this check installed it (stamp at line %d of the block, removal at %d): an app this check did not install is not this check's to remove", label, productName, stampAt, removeAt)
		}
	}

	// 59a's own record is a FACT line, so a restore that could not tell the two
	// apart is caught by the section rather than by a reader.
	facts := check59FactLines(a)
	if check59FactLine(t, facts, "security find-identity -v -p codesigning") == "" {
		t.Error("Check 59a records the identities it found as a FACT line, not just as a command")
	}
}

// consoleProductName reads the app's product name out of the builder
// configuration: `build.productName` in console/package.json. It is the name
// the packaged bundle and its executable carry, so a check that names
// `/Applications/<productName>.app/Contents/MacOS/<productName>` is pinned to
// the packer rather than to a remembered string. This file is the one Go file
// Task 0.3's boundary test allows to mention a console/ path.
func consoleProductName(t *testing.T) string {
	t.Helper()
	var pkg struct {
		Build struct {
			ProductName string `json:"productName"`
		} `json:"build"`
	}
	if err := json.Unmarshal([]byte(readDoc(t, "console/package.json")), &pkg); err != nil {
		t.Fatalf("console/package.json does not decode: %v", err)
	}
	if pkg.Build.ProductName == "" {
		t.Fatal("console/package.json's build.productName is empty, and a packaged bundle with no product name has no bundle path for Check 59a to launch")
	}
	return pkg.Build.ProductName
}

// TestDocsDesktopConsoleDesignLaunchFenceIsWrittenDown pins §5.1 rule 11 — the
// launch, the console's one write — and the never table's moved row, which is
// the fence that rule moves.
//
// Rule 11 was added by the console v2 plan's V2.5.1 (2026-10-27) and amended the
// same day by the owner's rulings and by an independent security review, and it
// is the contract the code lands against (V2.5.1a). It is prose, so nothing else
// holds it: the allow-list table and rule 5's count are deliberately NOT moved by
// this design — they move with the code, in one commit, in V2.5.1a — and the
// "never" table is the one table this rule does edit. The sentences below are
// the claims that review found missing or untrue (the repository conversion, the
// confirmation window, the recorded base branch, the working directory, the one
// row allowed to write to a child, the format characters, the `=` forms, the
// deadline, the rate limits and the replay rule), so this test is what keeps a
// later rewrite from dropping one.
//
// The mutations it fails on: deleting any pinned sentence (each names itself);
// re-wrapping one is not a mutation, because the rule is read as one run of
// whitespace; carrying a `run` argv on the allow-list table other than rule
// 11's, or a rule 5 count that disagrees with the code (the checks after the
// table); and restoring a bare `run` to the never table, which would read as a
// permission for the one argv this rule writes rather than for every other one.
func TestDocsDesktopConsoleDesignLaunchFenceIsWrittenDown(t *testing.T) {
	const path = "docs/design/desktop-console.md"
	doc := readDoc(t, path)

	start := strings.Index(doc, "\n11. **The launch is the console's one write")
	if start < 0 {
		t.Fatalf("%s has no §5.1 rule 11: the launch's fence is not written down", path)
	}
	end := strings.Index(doc[start:], "\n### 5.2 ")
	if end < 0 {
		t.Fatalf("%s never closes §5.1: no `### 5.2` heading follows rule 11", path)
	}
	rule := flattenDoc(doc[start : start+end])

	// The one argv rule 11 writes, spelled once: the pins below and the
	// allow-list check at the end of this test all speak of this same form,
	// so a widened argv cannot pass by looking right in one place.
	const rule11Argv = "run --task-file - --json --run-id <run id> --repo <owner/name> [--title=<T>] [--plan-task=<P>] --project <name> [--config <path>]"

	for _, want := range []struct{ why, text string }{
		{"the one exact argv, in its fixed order", rule11Argv},
		{"a value that rides its own flag word cannot be read as a flag",
			"a value after `=` is part of the flag's own word"},
		{"the row and the code it needs land together",
			"the operation, the preload method and the argv builder"},
		{"a moved row is a plan and not a permission",
			"the console spawns no `run` argv at all"},
		{"the closed schema's five fields",
			"`installation`, `repository`, `task`, `title` and `planTask`"},
		{"the repository identity the records carry",
			"`<provider>/<owner>/<name>`"},
		{"the conversion to what --repo takes",
			"drop the first `<provider>/` segment"},
		{"the providers the console can open a pull request on",
			"**`github` or `bitbucket`**"},
		{"the example that shows the conversion",
			"`github/owner/repo-a` becomes `owner/repo-a`"},
		{"only the identity is read from a checkout row, never its path",
			"Only the identity is read from those rows, never the path"},
		{"the task text's bound, and why it is not the CLI's",
			"at most 16 KiB of valid UTF-8 with no NUL byte"},
		{"the task never reaches the argv",
			"The task text goes to the child on stdin and never into the argv"},
		{"one row is allowed to write to a child",
			"the one row that gets `'pipe'` on stdin"},
		{"every other row's stdin, and the type that pins it",
			"the console never writes to a child"},
		{"the test that holds the other rows",
			"a test pins `'ignore'` for every other row of the spawn table"},
		{"the confirmation is a window the main process owns",
			"a modal window of its own"},
		{"and it is not a native message box",
			"`dialog.showMessageBox` is not used"},
		{"the operator reads the whole task rather than a summary",
			"the whole task text, displayed in full and scrollable"},
		{"the fields the window shows, and nothing else",
			"the installation, the repository, the ref, the title, the plan task, the run id"},
		{"the argv the window shows",
			"the exact argv field by field"},
		{"cancelling is what a stray Enter does",
			"**Cancel is the default button**"},
		{"the renderer cannot reach the window",
			"cannot answer for the operator"},
		{"a run is cut from a branch the installation records",
			"the installation's **recorded base branch**"},
		{"the merged order, layer one",
			"the working directory's checkout of the repository when its `fugaro.yaml` writes `git.base_branch`"},
		{"the merged order, layers two to four",
			"the local config's `repos.<r>.base_branch`, else the project's `base_branch`, else `main`"},
		{"the refusal the CLI used to end on",
			"The old refusal is gone"},
		{"what the fallback means for a row that cannot pass --ref",
			"with nothing recorded the CLI resolves `main` and says nothing"},
		{"the field the console still cannot read",
			"carried **no `base_branch`**"},
		{"the layer a console launch cannot take its ref from",
			"for this row it cannot be the source"},
		{"the two layers it can both read and be cut from",
			"the two layers a console launch can both read and be cut from"},
		{"the setting that does not move this row",
			"which an operator can set without moving this row"},
		{"the read is a CLI change, not a console-only one",
			"it was a CLI change rather than a console-only one"},
		{"and a console-only slice could not build the rule",
			"a console-only slice could not build this rule at all"},
		{"what that change adds to the read",
			"the installation's fields gained the project-level `base_branch`"},
		{"and why it is not a new operation",
			"a field on a read the console already has and not a new operation"},
		{"no field of the schema names a run id",
			"**No field of the schema names a run id**"},
		{"the retry is safe only where the first launch wrote its claim",
			"where the first launch got as far as writing that claim"},
		{"a child killed before its claim leaves the container to start",
			"a child killed before it wrote a claim leaves nothing for the retry to find"},
		{"and a stale claim is taken over",
			"a retry more than `ClaimTTL`'s ten minutes later finds a claim the CLI takes over as stale"},
		{"the floor is a comparison, not the range",
			"a version comparison and not membership in the console's supported range"},
		{"and the range the console reads today is below it",
			"**every CLI the console supported then was below the floor**"},
		{"which is why this slice moves the range",
			"moved the supported range to include `0.11`"},
		{"the task is trimmed, so the window's bytes are the run's",
			"the bytes the window counts and the operator confirms are the bytes the run keeps"},
		{"and an empty task is refused before the window opens",
			"A task that is empty once trimmed is refused on this side"},
		{"what the window shows is what is spawned",
			"**What the window shows is what is spawned.**"},
		{"and it spawns the frozen object rather than rebuilding one",
			"spawns **that frozen object verbatim**"},
		{"an installation that moves under the window cancels it",
			"closes the window as a cancel"},
		{"the never table in the code, and the row it carries today",
			"**In the console's code that table is `NEVER_REACHABLE`**"},
		{"which V2.5.1a replaces with argv-shaped rows",
			"with argv-shaped rows refusing every `run` argv but rule 11's form"},
		{"and the class that test classifies `run` under moves",
			"moves `run` out of `CLI_COMMANDS`' `mutating` class"},
		{"and the allow-list's own argv list gains the row",
			"adds rule 11's argv to `DESIGN_ARGV`"},
		{"no other row of that table moves",
			"**No other `NEVER_REACHABLE` row moves**"},
		{"a title is not a `value` argument",
			"argument kinds of their own"},
		{"and the renderer alphabet is not widened for it",
			"`ARGUMENT_RE` stays unchanged for every other field"},
		{"the retry's ten minutes are the owner's to rule",
			"**And one point is left open rather than ruled: (6) the held-id retry once the claim's ten minutes have passed.**"},
		{"and the recommendation the design records",
			"refuses the retry once `ClaimTTL`'s ten minutes are gone"},
		{"which is where the retry offer points",
			"how long that offer stands is open point 6"},
		{"a repository with no record is refused, and the refusal is actionable",
			"names the repository and what to set"},
		{"the launch runs from the app's own directory, not the operator's checkout",
			"always the app's own private, empty directory, never the operator's checkout"},
		{"which is what keeps the checkout's branch out of the ref",
			"the branch the working directory's checkout is on"},
		{"format characters are refused wherever a value is read",
			"Unicode format characters (`Cf`), the bidi overrides"},
		{"the task is never shown raw",
			"strips the format characters out of the displayed text"},
		{"a launch is not a read, so it has its own deadline",
			"`LAUNCH_TIMEOUT_MS` is not `READ_TIMEOUT_MS`"},
		{"how a launch is stopped",
			"`SIGTERM`, then `SIGKILL` after `SHUTDOWN_GRACE_MS`"},
		{"a killed launch proves nothing and is not retried",
			"the launch's outcome is unknown"},
		{"a repeat of a launch in flight joins it rather than racing it",
			"is attached to the launch in flight"},
		{"a repeat of an answered launch is answered from the record",
			"answered from the app's own record"},
		{"who may replay a launch, and who may not",
			"the window's own main-side `answer()` path"},
		{"the killed launch's retry is the one repeat that spawns again",
			"never a fresh window and a fresh id"},
		{"the rate that keeps a click from becoming a queue of windows",
			"windows are opened at most three times a minute per installation"},
		{"the limit across the whole app",
			"at most two launches in flight across the whole app"},
		{"the cool-down the owner accepted",
			"a ten-second cool-down"},
		{"the WSL wrapper is what makes an `=`-form word safe there",
			"quotes every word of the CLI's argv with `shellQuoteWord`"},
		{"and the argv check takes the two prefixes on this row alone",
			"accept exactly the `--title=` and `--plan-task=` prefixes, for the `run.launch` row alone"},
		{"and the task still arrives there",
			"on the same stdin the cell pipe carries"},
		{"the spawn log records the launch and still no task text",
			"records no task text"},
		{"the surfaces the operator reaches it from",
			"the Runs screen is the only place"},
		{"a yes is refused until the window has settled",
			"**A yes is refused until the window has settled, and the measure is the main process's own**"},
		{"and the delay is measured where the events are, not in the page",
			"with both events read **in the main process** and never by a timer in the page"},
		{"which of the two events the delay runs from",
			"**`CONFIRM_READY_DELAY_MS` (500 ms) after the window's own `show` or `focus`, whichever of the two came last**"},
		{"a focus is not required to answer, and only extends the delay",
			"**Focus is not required, and only ever extends the delay**"},
		{"the quantity the delay is measured against is the double-click interval",
			"**double-click interval** (`I`)"},
		{"and the case it does not cover is recorded rather than claimed away",
			"**That case is recorded rather than claimed away**"},
		{"a re-focus starts the delay over, and Cancel does not wait for it",
			"**Cancel takes no delay at all**"},
		{"what re-chosen means",
			"**Re-chosen means that same installation chosen again**"},
		{"the dashboard a choose replaces, and the child that goes with it",
			"**A choose closes the dashboard window as well**"},
		{"the console with no dashboard, where no parent closes the child",
			"**survives a switch to another installation**"},
		{"what the next part must confirm on a real window",
			"**V2.5.1c must confirm on a real window that closing the parent closes the child**"},
		{"and the second platform behaviour it must confirm, whose failure mode is every launch refused",
			"**and that this window's own `show` and `focus` arrive at all**"},
		{"the replay record's cap, and the order it evicts in",
			"**capped at `MAX_RECORDED_OUTCOMES` (256), oldest evicted first**"},
		{"what an id whose record was evicted answers",
			"**An evicted id stays issued and answers `undefined` without spawning**"},
	} {
		if !strings.Contains(rule, want.text) {
			t.Errorf("%s §5.1 rule 11 no longer says %s:\n  want %q", path, want.why, want.text)
		}
	}

	if strings.Contains(rule, "64 KiB") {
		t.Errorf("%s §5.1 rule 11 still bounds the task at 64 KiB: the operator confirms by reading it, so the bound is 16 KiB", path)
	}

	if !strings.Contains(doc, "**The dashboard's surface is twenty-four operations**") {
		t.Errorf("%s no longer says the surface is twenty-four operations: rule 5's count moved with the code when V2.5.1a landed, and the two cannot disagree", path)
	}

	var neverRows [][]string
	for _, cells := range docTableAfter(t, path, doc, "| Never (the \"nothing else\" table) |") {
		neverRows = append(neverRows, cells)
	}
	if len(neverRows) == 0 {
		t.Fatalf("%s: no row follows the never table's header; update this test's marker", path)
	}
	// The row's first token is the form it refuses: a row that still LEADS with a
	// bare `run` is the old row, and it reads as a permission for the one argv
	// rule 11 writes. A bare `run` ANYWHERE ELSE in the table is the same defect
	// one row down — the entry lived in the "every writer" row's list before
	// rule 11 moved it, so deleting the moved row and putting the bare `run`
	// back beside `cancel` and `sync` is the mutation, not only restoring the
	// row this design replaced. The one row allowed to hold a bare `run` token
	// is the moved row itself, which names the argv it excludes.
	sawRetry := false
	for _, cells := range neverRows {
		leads := firstBacktick(cells[0])
		switch leads {
		case "run --retry":
			sawRetry = true
		case "run":
			t.Errorf("%s's never table still leads a row with a bare `run`: rule 11 moved that row to `run --retry` and every other `run` argv", path)
		}
		if leads == "run --retry" {
			continue
		}
		// The whole row and not a token walk: a row that lists an escaped "|"
		// (the secret commands' row does) splits into several cells, and the
		// backticks pair across that split rather than inside a cell, so a bare
		// `run` appended after the escape is found by the literal spelling and
		// missed by anything that has to pair delimiters. `run --retry` and
		// `runs ls` both keep a character after `run`, so neither matches.
		if strings.Contains(strings.Join(cells, "|"), "`run`") {
			t.Errorf("%s's never table lists a bare `run` in the row that leads with %q: the bare entry belongs to no row but rule 11's, which spells the argv it excludes", path, leads)
		}
	}
	if !sawRetry {
		t.Errorf("%s's never table does not start a row at `run --retry`: rule 11 moved the `run` row, and this is the row it moved to", path)
	}

	// Rule 11's row is the allow-list's one `run` argv, and it landed with
	// V2.5.1a in one commit with the operation. Exactly one row may lead with
	// `run` and it must be that argv spelled as rule 11 writes it: any other
	// `run` argv on the table is a second write nobody ruled on, and a `run`
	// row that leads with anything else is the row the design replaced.
	launches := 0
	for _, cells := range docTableAfter(t, path, doc, "| Allowed argv (exact, in this order) |") {
		argv := firstBacktick(cells[0])
		if argv != "run" && !strings.HasPrefix(argv, "run ") {
			continue
		}
		launches++
		if argv != rule11Argv {
			t.Errorf("%s's allow-list table carries %q: its one `run` row is rule 11's exact argv and nothing else", path, argv)
		}
	}
	if launches != 1 {
		t.Errorf("%s's allow-list table carries %d `run` argvs, want exactly 1 (rule 11's)", path, launches)
	}
}
