package cli

// The console's Go-side contract tests (desktop-console plan, Task 0.1).
//
// This file is named by Task 0.3's boundary test as the ONE Go file allowed to
// reference a path under console/ -- and it references it the way every
// docs_*_test.go here references docs/: readDoc(t, path) reads the bytes. It
// imports nothing from console/, embeds nothing from it and adds no go.mod
// dependency to reach it.
//
// Two halves. TestDocsDesktopConsoleDesignCarriesTheDecisions is the design's
// own presence test (design section 9.5 item 1): the load-bearing strings of
// docs/design/desktop-console.md. TestDocsDesktopConsoleContractNamesEveryWatchField
// is the stronger half this task adds: console/docs/json-contract.md must name
// every json tag of internal/watch/json.go's shapes, derived from the structs
// themselves rather than from a hand-kept list, so a rename in json.go fails a
// test here instead of silently breaking a screen.

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/budget"
	"github.com/dimipaun/fugaro/internal/report"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/watch"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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
// red step: the owner's answers of 2026-10-15, pinned where the design records
// them -- section 10's last column -- and the two lines that record what the
// owner's answers of 2026-10-16 do to THIS design (nothing: D22, D34 and D21
// belong to docs/design/desktop-console-v2.md's D21-D38, and no row of
// section 10 moves). A ruling cannot be dropped from the column it was written
// into, and the v2 answers cannot quietly start being restated here, without
// failing a named test.
func TestDocsDesktopConsoleDesignCarriesTheOwnerAnswers(t *testing.T) {
	const path = "docs/design/desktop-console.md"
	doc := readDoc(t, path)
	for _, tc := range []struct {
		what   string
		quotes []string
	}{
		{"section 10's answer column", []string{
			"| Owner's answer 2026-10-15 |",
			"the other sixteen accepted as recommended",
		}},
		{"D1, the CLI sidecar behind FugaroSource and never option B", []string{
			"**CLI sidecar (option A), behind the `FugaroSource` interface; direct RTDB/Firestore access with the operator's credentials (option B) never.**",
		}},
		{"D3, this repository, under console/", []string{
			"**This repository, under `console/`.**",
		}},
		{"D4, the cask, the signing and the owner's hands", []string{
			"**Direct download plus a Homebrew cask; signed, notarised and stapled.**",
			"**tag-only**, behind `scripts/release-gate.sh` and a **protected GitHub environment**",
			"**the owner holds the certificate and runs every signing step**",
			"**The Mac App Store is out.**",
		}},
		{"D11, its own artifact from the CLI's vX.Y.Z tag", []string{
			"**Its own artifact, published from the CLI's `vX.Y.Z` tag, with a declared supported CLI range.**",
		}},
		{"D13, answered as recommended with the pairing check", []string{
			"**Accepted as recommended** — with the pairing check above recorded as part of the recommendation",
		}},
		{"the answers of 2026-10-16, recorded as the v2 design's and not this design's", []string{
			"2026-10-16: the two answers to that spec's decisions",
			"no answer of 2026-10-16 changes any row of §10",
			"they do not reopen or amend anything below",
		}},
	} {
		for _, want := range tc.quotes {
			if !strings.Contains(doc, want) {
				t.Errorf("%s no longer carries %s: it never says %q", path, tc.what, want)
			}
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
	"diagnose":    newDiagnoseCmd,
	"logs":        newLogsCmd,
	"cancel":      newCancelCmd,
	"secrets ls":  newSecretsLsCmd,
	"accounts ls": newAccountsLsCmd,
	"doctor":      newDoctorCmd,
	"version":     newVersionCmd,
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
func planPhaseRow(t *testing.T, plan, phase string) (whatShips, row string) {
	t.Helper()
	for _, cells := range docTableAfter(t, "docs/plans/2026-10-14-project-centric.md", plan, "| Phase | What ships |") {
		if strings.Contains(cells[0], phase) {
			return cells[1], strings.Join(cells, " | ")
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
