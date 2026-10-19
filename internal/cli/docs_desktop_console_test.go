package cli

// The console's Go-side contract tests (desktop-console plan, Task 0.1).
//
// This file is named by Task 0.3's boundary test as the ONE Go file allowed to
// reference a path under console/ -- and it references it the way every
// docs_*_test.go here references docs/: readDoc(t, path) reads the bytes. It
// imports nothing from console/, embeds nothing from it and adds no go.mod
// dependency to reach it. Everything below that reads a console/ file reads it
// as bytes for the same reason, and stays in this file so that the allow-list
// keeps its single entry: the console's copy of a Go decision is checked here
// against the Go source, and the second such copy is
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
	"github.com/dimipaun/fugaro/internal/report"
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

	// The ruled cells, exactly as the owner ruled them, and the two acceptances
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
	// is the plain acceptance, and D12 and D13 are the two with an amendment.
	acceptedSixteen := map[string]string{
		"**D2**": "", "**D5**": "", "**D6**": "", "**D7**": "", "**D8**": "",
		"**D9**": "", "**D10**": "",
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
		reflect.TypeOf(plan.Document{}),
		reflect.TypeOf(plan.Node{}),
		reflect.TypeOf(plan.Edge{}),
		reflect.TypeOf(plan.Footprint{}),
		reflect.TypeOf(runstore.ChangedFiles{}),
		// §6.10: the by-person aggregate behind runs stats --json, and the
		// counts it embeds (CLI 0.9.0).
		reflect.TypeOf(userStatsDoc{}),
		reflect.TypeOf(userstats.Row{}),
		reflect.TypeOf(userstats.Counts{}),
	}
	for _, name := range jsonTagNames(t, namedTypes(shapes...), shapes...) {
		if !strings.Contains(doc, "`"+name+"`") {
			t.Errorf("%s never names the json field %q, which the shape it documents carries", path, name)
		}
	}
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
		reflect.TypeOf(report.JSONDoc{}), reflect.TypeOf(report.JSONLine{}),
		reflect.TypeOf(report.JSONTokens{}), reflect.TypeOf(Diagnosis{}),
		reflect.TypeOf(configListDoc{}), reflect.TypeOf(configListInstallation{}),
		reflect.TypeOf(plan.Document{}), reflect.TypeOf(plan.Node{}),
		reflect.TypeOf(plan.Edge{}), reflect.TypeOf(plan.Footprint{}),
		reflect.TypeOf(runstore.ChangedFiles{}),
		// §6.10, the same three shapes as the test above (CLI 0.9.0).
		reflect.TypeOf(userStatsDoc{}),
		reflect.TypeOf(userstats.Row{}),
		reflect.TypeOf(userstats.Counts{}),
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
	checked := 0
	for _, line := range strings.Split(doc, "\n") {
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
			if !real[name] {
				t.Errorf("%s's field table names %q, which is not a json key of any shape it documents: a field invented rather than read", path, name)
			}
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
			"printCancel",
			"cancel.go:474",
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
// the document's word for it. Every command the console spawns except the
// version handshake carries --project and --config; version takes no flags at
// all, so a console that appended --project to it would fail on every machine.
func TestDocsDesktopConsoleContractNamesTheFlagInventory(t *testing.T) {
	const path = "console/docs/json-contract.md"
	doc := readDoc(t, path)
	spawned := map[string]func() *cobra.Command{
		"watch":       newWatchCmd,
		"budget show": newBudgetShowCmd,
		"report":      newReportCmd,
		"runs ls":     newLsCmd,
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
	"report":      newReportCmd,
	"runs ls":     newLsCmd,
	// §6.10 documents the by-person aggregate (CLI 0.9.0). It is not on the
	// design's spawn allow-list yet -- the Spend screen's by-person view is
	// the task that follows -- but the document describes its flags, so the
	// flags written there are checked against this constructor like any
	// other's.
	"runs stats":  newRunsStatsCmd,
	"diagnose":    newDiagnoseCmd,
	"logs":        newLogsCmd,
	"cancel":      newCancelCmd,
	"secrets ls":  newSecretsLsCmd,
	"accounts ls": newAccountsLsCmd,
	"doctor":      newDoctorCmd,
	"version":     newVersionCmd,
	"config list": newConfigListCmd,
	"plan graph":  newPlanGraphCmd,
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
	// root command has no cobra Version field and `fugaro --version` fails
	// (section 7), so it is checked for absence, the opposite of the rest.
	documentedAbsent := map[string]bool{"version": true}

	// (1) Every flag token in the document.
	found := map[string]bool{}
	for _, m := range flagTokenRE.FindAllStringSubmatch(doc, -1) {
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
// §5.1 rule 6 carries and compares their first cells exactly:
// the allow-list is the eleven argvs and nothing else, and the "never" table
// still names the two entries that make a name-based list a hole.
func TestDocsDesktopConsoleDesignAllowListIsExactArgv(t *testing.T) {
	const path = "docs/design/desktop-console.md"
	doc := readDoc(t, path)

	want := []string{
		"watch --json",
		"budget show --json",
		"report --json",
		"runs ls --json",
		"diagnose RUN --json",
		"logs RUN --json",
		"logs RUN --url",
		"secrets ls --json",
		"accounts ls --json",
		"version",
		"doctor --json",
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
	Needs       string               `yaml:"needs"`
	Permissions map[string]string    `yaml:"permissions"`
	Steps       []consoleReleaseStep `yaml:"steps"`
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
func readConsoleReleaseWorkflow(t *testing.T) (string, consoleReleaseWorkflow, map[string]any, map[string]map[string]any) {
	t.Helper()
	text := readDoc(t, consoleReleaseWorkflowPath)
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
	if !slices.Equal(wf.On.Push.Tags, []string{"v[0-9]+.[0-9]+.[0-9]+"}) {
		t.Errorf("%s's push trigger tags = %v, want the strict release tag pattern release.yml and images.yml use: a loose `v*` would start a signing run for a tag the release gate refuses", consoleReleaseWorkflowPath, wf.On.Push.Tags)
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
	consoleReleaseTagStep     = "The tag is a strict vX.Y.Z, and its version is the app's"
)

// consoleArtifactPins are the upload/download-artifact pins this repository's
// applied workflows (release.yml, images.yml) already use. The console's file
// reuses them rather than introducing a SHA nobody has read.
var consoleArtifactPins = map[string]string{
	"actions/upload-artifact":   "cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2",
	"actions/download-artifact": "9000827ccba6bdab643e8b6fd33ac0654aef8333 # v8.0.2",
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
	// and there are exactly four: checkout and setup-node before the signing
	// step, upload-artifact after it, download-artifact in `publish`. The two
	// artifact actions carry the pins release.yml and images.yml already use.
	uses := 0
	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(line, "uses:") || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		uses++
		if !consoleUsesLine.MatchString(line) {
			t.Errorf("%s: %q is not a full 40-hex pin with a `# vX.Y.Z` comment", consoleReleaseWorkflowPath, strings.TrimSpace(line))
		}
		for action, pin := range consoleArtifactPins {
			if strings.Contains(line, action+"@") && !strings.HasSuffix(strings.TrimSpace(line), action+"@"+pin) {
				t.Errorf("%s pins %s as %q, want the repository's existing pin %s", consoleReleaseWorkflowPath, action, strings.TrimSpace(line), pin)
			}
		}
	}
	if uses != 4 {
		t.Errorf("%s pins %d actions, want exactly 4 (checkout, setup-node, upload-artifact, download-artifact)", consoleReleaseWorkflowPath, uses)
	}

	// Two jobs: `release` (environment, secrets, packer, read-only token) and
	// `publish` (no environment, no packer, no Apple secret, the write token).
	var keys []string
	for name := range wf.Jobs {
		keys = append(keys, name)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"publish", "release"}) {
		t.Fatalf("%s has jobs %v, want exactly [publish release]", consoleReleaseWorkflowPath, keys)
	}
	job, pub := wf.Jobs["release"], wf.Jobs["publish"]
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
	if pub.Needs != "release" {
		t.Errorf("%s's publish job needs %q, want release: it publishes only what the release job verified", consoleReleaseWorkflowPath, pub.Needs)
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
	if want := map[string]string{"contents": "write"}; !reflect.DeepEqual(pub.Permissions, want) {
		t.Errorf("%s's publish job permissions = %v, want exactly %v", consoleReleaseWorkflowPath, pub.Permissions, want)
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
					if !(j.name == "release" && i == gateIdx) && step.Name != consoleReleasePublishStep {
						t.Errorf("%s's step %q holds the token (%s): only the gate and the publish step do", consoleReleaseWorkflowPath, step.Name, name)
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
	tagIdx, tag := stepNamed(consoleReleaseTagStep)
	if tagIdx != runSteps[1] || gateIdx != runSteps[2] {
		t.Errorf("%s's run steps begin %v, want the ancestry check, then the tag shape (%d), then the gate (%d)", consoleReleaseWorkflowPath, runSteps[:3], tagIdx, gateIdx)
	}
	if tag.Id != "tag" || tag.Env["TAG"] != "${{ github.ref_name }}" {
		t.Errorf("%s's tag step must have id `tag` and read TAG from github.ref_name", consoleReleaseWorkflowPath)
	}
	if !slices.Contains(consoleRunLines(gate.Run), `scripts/release-gate.sh "$TAG"`) || gate.Env["TAG"] != "${{ github.ref_name }}" {
		t.Errorf("%s's gate step (%q) does not run the release gate with the tag", consoleReleaseWorkflowPath, gate.Name)
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
	}{{consoleNotarizeScriptPath, scriptLines}}
	for _, j := range jobs {
		for _, step := range j.job.Steps {
			sources = append(sources, struct {
				where string
				lines []string
			}{step.Name, consoleRunLines(step.Run)})
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

	// **The publish job**: downloads that artifact, re-checks every asset
	// against the sums, then publishes `console-vX.Y.Z` and never the CLI's
	// release.
	if len(pub.Steps) != 3 || !strings.Contains(pub.Steps[0].Uses, "actions/download-artifact@") || pub.Steps[0].With["name"] != "console-release-assets" {
		t.Fatalf("%s's publish job is not [download the console-release-assets artifact, check the sums, publish]", consoleReleaseWorkflowPath)
	}
	sums := consoleRunLines(pub.Steps[1].Run)
	if !slices.Contains(sums, "sha256sum --strict --check console-sha256sums.txt") || pub.Steps[2].Name != consoleReleasePublishStep {
		t.Errorf("%s's publish job does not re-check the assets against console-sha256sums.txt before publishing", consoleReleaseWorkflowPath)
	}
	plines := consoleRunLines(publish.Run)
	for _, want := range []string{
		`CONSOLE_TAG="console-v$VERSION"`,
		`VERSION="${TAG#v}"`,
		`gh release create "$CONSOLE_TAG" --repo "$GITHUB_REPOSITORY" --draft --latest=false \`,
		`--target "$GITHUB_SHA" \`,
		`"$DMG" "$APP_ZIP" "$RECORD" "$SUMS"`,
		`gh release edit "$CONSOLE_TAG" --repo "$GITHUB_REPOSITORY" --draft=false --latest=false`,
	} {
		if !slices.Contains(plines, want) {
			t.Errorf("%s's publish step does not run %q", consoleReleaseWorkflowPath, want)
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
	// The never-clobber shape: in each `case`, the only branch that lets the
	// job go on is the "absent" one ("" for the release list, a 404 for the
	// tag), and the default branch `*)` errors and exits 1. The behaviour is
	// pinned again, against a faked gh, in
	// TestDocsConsoleReleasePublishRefusesToClobber.
	for _, c := range []struct{ subject, pass string }{
		{`case "$existing" in`, `"") ;;`},
		{`case "$answer" in`, `*"(HTTP 404)"*) ;;`},
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
	if i := consoleLineIndex(plines, `if answer="$(gh api "repos/$GITHUB_REPOSITORY/git/ref/tags/$CONSOLE_TAG" 2>&1)"; then`, false); i < 0 || plines[i+2] != "exit 1" {
		t.Errorf("%s's publish step does not refuse an existing console tag", consoleReleaseWorkflowPath)
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
	if _, statErr := os.Stat(filepath.Join("..", "..", appliedConsoleReleasePath)); statErr == nil {
		applied, err := os.ReadFile(filepath.Join("..", "..", appliedConsoleReleasePath))
		if err != nil {
			t.Fatal(err)
		}
		if string(applied) != text {
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
  *"git/ref/tags/"*)
    case "$FAKE_TAG" in
      exists) echo '{"ref":"refs/tags/x"}'; exit 0 ;;
      404) echo "gh: Not Found (HTTP 404)" >&2; exit 1 ;;
      *) echo "gh: Server Error (HTTP 502)" >&2; exit 1 ;;
    esac ;;
  "release create"*|"release edit"*) exit 0 ;;
esac
echo "fake gh: unexpected call: $*" >&2
exit 3
`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(dir, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Fugaro Console-1.2.3-universal.dmg", "Fugaro Console-1.2.3-universal-mac.zip", "console-verification.txt", "console-sha256sums.txt"} {
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
// against a faked gh. It publishes only when no console release exists and the
// tag lookup is a clean 404; a published release, a leftover draft, an existing
// tag, a tag lookup that failed any other way (a 5xx must not read as "absent")
// and a failed release listing all stop it before `gh release create`.
func TestDocsConsoleReleasePublishRefusesToClobber(t *testing.T) {
	_, wf, _, _ := readConsoleReleaseWorkflow(t)
	_, step := consoleStepNamed(t, wf.Jobs["publish"], "publish", consoleReleasePublishStep)
	for _, c := range []struct {
		name        string
		releases    string
		releasesErr string
		tag         string
		publishes   bool
	}{
		{"nothing there: publishes", "", "0", "404", true},
		{"published release", "published", "0", "404", false},
		{"draft left by an earlier attempt", "draft", "0", "404", false},
		{"tag exists without a release", "", "0", "exists", false},
		{"tag lookup fails with a 5xx", "", "0", "502", false},
		{"release listing fails", "", "1", "404", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			ok, calls, _ := consoleRunScript(t, step.Run, []string{
				"TAG=v1.2.3", "GH_TOKEN=fake",
				"FAKE_RELEASES=" + c.releases, "FAKE_RELEASES_EXIT=" + c.releasesErr, "FAKE_TAG=" + c.tag,
			})
			created := strings.Contains(calls, "gh release create console-v1.2.3")
			published := strings.Contains(calls, "gh release edit console-v1.2.3 --repo dimipaun/fugaro --draft=false --latest=false")
			if c.publishes && (!ok || !created || !published) {
				t.Errorf("the publish step did not create and publish console-v1.2.3 (ok=%v); gh saw:\n%s", ok, calls)
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
// step: a strict vX.Y.Z yields version=X.Y.Z (the value every pack is stamped
// with); anything else fails before a pack can run.
func TestDocsConsoleReleaseTagStepStampsOnlyStrictVersions(t *testing.T) {
	_, wf, _, _ := readConsoleReleaseWorkflow(t)
	_, step := consoleStepNamed(t, wf.Jobs["release"], "release", consoleReleaseTagStep)
	for _, c := range []struct {
		tag, version string
	}{
		{"v1.2.3", "1.2.3"},
		{"v0.10.0", "0.10.0"},
		{"v1.2", ""},
		{"v1.2.3-rc1", ""},
		{"v01.2.3", ""},
		{"1.2.3", ""},
		{"v1.2.3\nversion=9.9.9", ""},
	} {
		ok, _, out := consoleRunScript(t, step.Run, []string{"TAG=" + c.tag})
		if c.version == "" {
			if ok || out != "" {
				t.Errorf("tag %q: the tag step passed (output %q); it must refuse anything but a strict vX.Y.Z", c.tag, out)
			}
			continue
		}
		if !ok || out != "version="+c.version+"\n" {
			t.Errorf("tag %q: ok=%v output %q, want version=%s", c.tag, ok, out, c.version)
		}
	}
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
	// words the plan's answer column uses.
	design := readDoc(t, "docs/design/desktop-console.md")
	if !strings.Contains(design, "10. **The app writes the spawn log, when asked, and the installations file, and nothing else.**") {
		t.Error("design section 5.1 rule 10 was not amended to C8's wording")
	}
	if !strings.Contains(design, "**The app writes two things to disk and nothing else (§5.1 rule 10, amended 2026-10-18)") {
		t.Error("design section 7.4 was not amended to C8's wording")
	}
	if !strings.Contains(design, "never a credential") {
		t.Error("design section 5.1 rule 10's installations paragraph does not say the file holds no credential")
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
		checkJSONShape(t, fmt.Sprintf("record %d", i+1), rec.Document, reflect.TypeOf(watch.JSONDoc{}))
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

// checkJSONShape walks one JSON value against a struct type: keys in struct
// order, required keys present, recursing into nested structs and slices.
func checkJSONShape(t *testing.T, where string, raw json.RawMessage, rt reflect.Type) {
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
			checkJSONShape(t, fmt.Sprintf("%s[%d]", where, i), item, rt.Elem())
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
			} else if f.required {
				t.Errorf("%s: required key %q is missing (internal/watch/json.go has it without omitempty)", where, f.name)
			}
		}
		if got := orderedJSONKeys(t, raw); !slices.Equal(got, want) {
			t.Errorf("%s: keys %v, want the struct's order %v", where, got, want)
		}
		for _, f := range fields {
			if v, ok := members[f.name]; ok {
				checkJSONShape(t, where+"."+f.name, v, f.typ)
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
	// launched with, plus the dash the wall view writes for an empty one.
	auth := sourceValues(t, "internal/config/validate.go", readDoc(t, "internal/config/validate.go"),
		`\[\]string\{([^}]*)\}, c\.Agent\.Auth`)
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
	// document: the two `commands.reports` patterns internal/config/defaults.go
	// installs, which are what internal/verify/verify.go joins into
	// `no JUnit reports found under ...`. They are the vocabulary the fixture
	// scan pins the glob slot to (VERIFY_REPORT_GLOBS), so a change to the
	// defaults that the scrub did not follow would make the scan refuse a
	// recording the CLI itself wrote.
	sameSet(t, "verify report glob", quoted(sourceValues(t, "internal/config/defaults.go",
		readDoc(t, "internal/config/defaults.go"), `w\.Commands\.Reports = \[\]string\{([^}]*)\}`)[0]),
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
