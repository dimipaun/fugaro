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
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/report"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/watch"
	"github.com/spf13/cobra"
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
			"watch --json",
			"budget show --json",
			"report --json",
			"runs ls --json",
			"diagnose RUN --json",
			"logs RUN --url",
			"secrets ls --json",
			"accounts ls --json",
			"doctor --json",
			"with no flags at all",
			"doctor --check-github-app",
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

// TestDocsDesktopConsoleContractStatesWhichDocumentsAreLineDelimited: exactly
// two of the commands the console reads emit one JSON object per line, and
// they do it through two different code paths -- the watch feed through
// json.NewEncoder with no SetIndent, the log lines through json.Marshal in
// printLogEntry. A document that claims only the feed is line-delimited, or
// that calls the log form indented, builds the log reader on the wrong parser,
// which is what a review of the first revision found here.
func TestDocsDesktopConsoleContractStatesWhichDocumentsAreLineDelimited(t *testing.T) {
	const path = "console/docs/json-contract.md"
	doc := readDoc(t, path)
	for _, want := range []string{
		"one compact `logLine` object per line",
		"printLogEntry",
		"json.Marshal",
		"Exactly two commands are line-delimited",
		"`watch --json` and `logs RUN\n   --json`",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s does not state which documents are line-delimited: it never says %q", path, want)
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
