// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

// The console contract's coordinator half, checked against the tree this
// package is part of (E2.1b): §6.13 of console/docs/json-contract.md and the
// design's §5.1 row for `coordinator status --from-store --all --json`
// document THIS package's command, so the checks that read the code back off
// those sentences live here, where the command and the shapes are. The core's
// own internal/cli/docs_desktop_console_test.go carries the same checks for
// every section whose command is still in the core, and skips §6.13 and §8's
// coordinator row (consoleMovedSections, consoleMovedRows), because an untagged
// build compiles neither the command nor the shapes it documents.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/ee/coordinator"
	"github.com/spf13/pflag"
)

// coordinatorContractPath and coordinatorDesignPath are the two documents that
// describe this command, read from this package: the contract the console's
// decoder is written against, and the design whose spawn table is the console's
// whole read-only guarantee.
const (
	coordinatorContractPath = "../../../console/docs/json-contract.md"
	coordinatorDesignPath   = "../../../docs/design/desktop-console.md"
)

// coordinatorFlagRE finds a --flag the documents write, the same pattern the
// core's own contract tests read flags with.
var coordinatorFlagRE = regexp.MustCompile("--([a-z][a-z-]*)")

// readFileString is the file read every check here starts from.
func readFileString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// coordinatorSection is the contract's §6.13, from its heading to the heading of
// the section after it, so a check cannot pass on a field named in some other
// section's table.
func coordinatorSection(t *testing.T) string {
	t.Helper()
	doc := readFileString(t, coordinatorContractPath)
	const head = "### 6.13 "
	i := strings.Index(doc, head)
	if i < 0 {
		t.Fatalf("%s no longer carries the %q section this package's command is documented in", coordinatorContractPath, head)
	}
	body := doc[i:]
	if j := strings.Index(body[1:], "\n### "); j >= 0 {
		body = body[:j+1]
	}
	if len(body) < 1000 {
		t.Fatalf("%s's §6.13 is %d bytes: the section boundary moved and these checks would read almost nothing", coordinatorContractPath, len(body))
	}
	return body
}

// coordinatorViewTypes are the nine shapes §6.13 documents: the document, the
// repository node, the marker beside it, the view, its records, the runs and
// pull requests under them, the collision pair, and the sweep's unread entry.
func coordinatorViewTypes() []reflect.Type {
	return []reflect.Type{
		reflect.TypeOf(coordinator.StoredProjectView{}),
		reflect.TypeOf(coordinator.RepoView{}),
		reflect.TypeOf(coordinator.Stale{}),
		reflect.TypeOf(coordinator.View{}),
		reflect.TypeOf(coordinator.Record{}),
		reflect.TypeOf(coordinator.Run{}),
		reflect.TypeOf(coordinator.PullRequest{}),
		reflect.TypeOf(coordinator.Collision{}),
		reflect.TypeOf(coordinator.Unread{}),
	}
}

// coordinatorJSONKeys is every json key the nine shapes carry, dug out of the
// structs so a field added in Go is a field this test demands in the document.
func coordinatorJSONKeys() []string {
	var out []string
	var walk func(t reflect.Type)
	walk = func(rt reflect.Type) {
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			tag := f.Tag.Get("json")
			if tag == "" || tag == "-" {
				continue
			}
			name := strings.Split(tag, ",")[0]
			if name != "" && !slices.Contains(out, name) {
				out = append(out, name)
			}
			if f.Type.Kind() == reflect.Struct {
				walk(f.Type)
			}
		}
	}
	for _, rt := range coordinatorViewTypes() {
		walk(rt)
	}
	slices.Sort(out)
	return out
}

// coordKeyRE and coordCellRE read a field table's first column: the key tokens
// of "| `a`, `b` | string | … |" and "| `files_truncated` bool | … |" alike. A
// token that is lowercase, digits and underscores is a json key; a Go type
// (`coordinator.RepoView`), a package (`runstore.ChangedFiles`) or a phrase
// ("the sweep's marker") is not one.
var (
	coordKeyRE  = regexp.MustCompile("^[a-z][a-z0-9_]*$")
	coordCellRE = regexp.MustCompile("`([^`]+)`")
)

// coordinatorListedKeys is the keys §6.13's own field tables list, read from
// their first cells rather than searched for in the prose: a key named in a
// sentence and missing from the table is exactly the drift this catches.
func coordinatorListedKeys(t *testing.T, body string) map[string]bool {
	t.Helper()
	listed := map[string]bool{}
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") {
			continue
		}
		cell, _, ok := strings.Cut(strings.TrimPrefix(trimmed, "|"), "|")
		if !ok {
			continue
		}
		for _, m := range coordCellRE.FindAllStringSubmatch(cell, -1) {
			if coordKeyRE.MatchString(m[1]) {
				listed[m[1]] = true
			}
		}
	}
	return listed
}

// TestConsoleContractNamesEveryCoordinatorField: §6.13's field tables list
// exactly the json keys the nine shapes carry, in both directions. A key the
// code writes and the document does not name is a field the console's decoder
// was never told about, and a key the document names and no shape carries is a
// reader written against a field no command emits -- the `columns` a first
// revision of the run list invented.
func TestConsoleContractNamesEveryCoordinatorField(t *testing.T) {
	body := coordinatorSection(t)
	listed := coordinatorListedKeys(t, body)
	if len(listed) < 30 {
		t.Fatalf("%s's §6.13 tables list %d key-shaped names: the table shape changed and this test stopped looking", coordinatorContractPath, len(listed))
	}
	real := map[string]bool{}
	for _, key := range coordinatorJSONKeys() {
		real[key] = true
		if !listed[key] {
			t.Errorf("%s's §6.13 tables never list the json field %q, which the shape it documents carries", coordinatorContractPath, key)
		}
	}
	for key := range listed {
		if !real[key] {
			t.Errorf("%s's §6.13 field table names %q, which is not a json key of any shape it documents: a field invented rather than read", coordinatorContractPath, key)
		}
	}
}

// TestConsoleContractNamesOnlyRealCoordinatorFlags: every --flag §6.13 writes
// -- in its heading's argv, on the line naming its command, and on §8's
// inventory row -- is a flag of the command this package builds, read off cobra
// rather than against a list kept here, so a flag invented in prose fails.
func TestConsoleContractNamesOnlyRealCoordinatorFlags(t *testing.T) {
	body := coordinatorSection(t)
	cmd := newCoordinatorStatusCmd()
	real := map[string]bool{}
	cmd.Flags().VisitAll(func(f *pflag.Flag) { real[f.Name] = true })
	if len(real) < 8 {
		t.Fatalf("coordinator status carries %d flags, want the read's own plus the cloud flags: the constructor is not the command this test documents", len(real))
	}
	for _, want := range []string{"from-store", "all", "repo", "since", "json", "project", "config"} {
		if !real[want] {
			t.Errorf("coordinator status has no --%s, and %s documents it as one of the command's flags", want, coordinatorContractPath)
		}
	}

	// The heading's argv: the words the console's design row passes.
	const head = "### 6.13 `fugaro coordinator status"
	if i := strings.Index(body, head); i < 0 {
		t.Fatalf("%s's §6.13 heading is not the command's argv any more: want %q", coordinatorContractPath, head)
	}
	for _, want := range []string{"--from-store", "--all", "--json"} {
		if !strings.Contains(body[:strings.Index(body, "\n")], want) {
			t.Errorf("%s's §6.13 heading no longer names %s: the row the console spawns is what the console passes", coordinatorContractPath, want)
		}
	}

	// Every --flag the section writes on a line naming the command, against it.
	named := 0
	for _, line := range strings.Split(body, "\n") {
		if !strings.Contains(line, "newCoordinatorStatusCmd") {
			continue
		}
		named++
		for _, m := range coordinatorFlagRE.FindAllStringSubmatch(line, -1) {
			name := strings.TrimRight(m[1], "-")
			if name == "" {
				continue
			}
			if !real[name] {
				t.Errorf("%s's §6.13 names --%s as a flag of newCoordinatorStatusCmd, and the command has no such flag", coordinatorContractPath, name)
			}
		}
	}
	if named == 0 {
		t.Fatalf("%s's §6.13 no longer names newCoordinatorStatusCmd: these checks would pass vacuously", coordinatorContractPath)
	}

	// §8's inventory row, the one the core's own test skips: the console may
	// spawn this command with --project and --config like every other read, and
	// the row is where that is written.
	row := coordinatorInventoryRow(t, "coordinator status")
	for _, want := range []string{"--from-store", "--project", "--config"} {
		if !strings.Contains(row, want) {
			t.Errorf("%s's §8 row for coordinator status no longer names %s: %q", coordinatorContractPath, want, row)
		}
	}
	for _, m := range coordinatorFlagRE.FindAllStringSubmatch(row, -1) {
		name := strings.TrimRight(m[1], "-")
		if name != "" && !real[name] {
			t.Errorf("%s's §8 row names --%s, which coordinator status has no flag for", coordinatorContractPath, name)
		}
	}
}

// coordinatorInventoryRow is §8's flag-inventory row for one of this package's
// two commands: the document's own account of what the console may pass it.
func coordinatorInventoryRow(t *testing.T, command string) string {
	t.Helper()
	for _, line := range strings.Split(readFileString(t, coordinatorContractPath), "\n") {
		if strings.HasPrefix(line, "| `"+command+"`") {
			return line
		}
	}
	t.Fatalf("%s's flag inventory has no `%s` row: the console spawns it, and §8 is where its flags are stated", coordinatorContractPath, command)
	return ""
}

// TestConsoleContractCitesThisPackagesFilesAndSymbols: the paths and the
// `coordinator.X` names §6.13 and the design's row cite are the moved ones and
// they resolve. It is the check the move made necessary: both documents cite
// their command by path and by symbol, so a citation left pointing at
// internal/cli/ is a reader sent to a file that no longer exists, and a symbol
// renamed here is a citation that reads as checked and is not.
func TestConsoleContractCitesThisPackagesFilesAndSymbols(t *testing.T) {
	var decls []string
	coordinatorDecls(t, "../.", &decls)
	if len(decls) < 20 {
		t.Fatalf("ee/coordinator declares %d top-level names: the scan is looking in the wrong place", len(decls))
	}

	var designRow string
	for _, line := range strings.Split(readFileString(t, coordinatorDesignPath), "\n") {
		if strings.Contains(line, "`coordinator status --from-store --all --json`") ||
			strings.Contains(line, "`coordinator show --json --repo <owner/name> --pr <n>`") {
			designRow += line + "\n"
		}
	}
	if designRow == "" {
		t.Fatalf("%s no longer carries the console's argv row for this command", coordinatorDesignPath)
	}

	cited := 0
	for _, doc := range []struct{ path, text string }{
		{coordinatorContractPath, coordinatorSection(t)},
		{coordinatorDesignPath, designRow},
	} {
		for _, m := range coordCellRE.FindAllStringSubmatch(doc.text, -1) {
			cite := m[1]
			switch {
			case strings.HasPrefix(cite, "internal/cli/coordinator"),
				strings.HasPrefix(cite, "internal/coordinator/"):
				t.Errorf("%s cites %q, which the move left behind: the coordinator's library and command are under ee/ now", doc.path, cite)
			case strings.HasPrefix(cite, "ee/coordinator/") && strings.HasSuffix(cite, ".go"):
				cited++
				if _, err := os.Stat(filepath.Join("../../..", cite)); err != nil {
					t.Errorf("%s cites %q, which is not in the tree: %v", doc.path, cite, err)
				}
			case strings.HasPrefix(cite, "coordinator.") && !coordinatorOperations[cite]:
				cited++
				name := strings.TrimPrefix(cite, "coordinator.")
				if !slices.Contains(decls, name) {
					t.Errorf("%s cites coordinator.%s, which ee/coordinator does not declare", doc.path, name)
				}
			}
		}
	}
	if cited == 0 {
		t.Fatalf("%s and %s cite no coordinator file or symbol: these checks would pass vacuously", coordinatorContractPath, coordinatorDesignPath)
	}
}

// coordinatorOperations are the console's own names for its two reads of this
// package's commands (their IPC operations), not symbols of the library: the
// documents write them in the same `coordinator.`-qualified shape a citation
// takes, so without this list the check above would read each one as a citation
// of a symbol ee/coordinator does not declare. Both are written on the design's
// own §5.1 rows and on `console/docs/json-contract.md` §6.13 and §6.15, one per
// command, and `console/src/main/ipc.ts` is where each name is declared.
var coordinatorOperations = map[string]bool{
	"coordinator.status": true,
	"coordinator.show":   true,
}

// coordinatorDecls appends every top-level name declared by the non-test Go
// files of the package at dir: the functions, types and values a citation can
// name.
func coordinatorDecls(t *testing.T, dir string, names *[]string) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			switch v := d.(type) {
			case *ast.FuncDecl:
				// A method is cited as Type.Method (the documents cite
				// `coordinator.Record.Label`), so the receiver is part of the
				// name a citation has to match.
				if recv := receiverName(v.Recv); recv != "" {
					*names = append(*names, recv+"."+v.Name.Name)
				} else {
					*names = append(*names, v.Name.Name)
				}
			case *ast.GenDecl:
				for _, spec := range v.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						*names = append(*names, s.Name.Name)
					case *ast.ValueSpec:
						for _, id := range s.Names {
							*names = append(*names, id.Name)
						}
					}
				}
			}
		}
	}
}

// receiverName is the base type name of a method's receiver, "" for a function:
// the first half of the Type.Method shape the documents cite methods in.
func receiverName(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	expr := recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// coordinatorShowSection is the contract's §6.15, from its heading to the next
// heading of its level or above: `coordinator show`'s document (coordinator v2
// C3). It is read apart from §6.13 because the two document different reads of
// different shapes, and a check that read the whole document would pass on a
// field named in either.
func coordinatorShowSection(t *testing.T) string {
	t.Helper()
	doc := readFileString(t, coordinatorContractPath)
	const head = "### 6.15 "
	i := strings.Index(doc, head)
	if i < 0 {
		t.Fatalf("%s no longer carries the %q section `coordinator show` is documented in", coordinatorContractPath, head)
	}
	body := doc[i:]
	if j := strings.Index(body[1:], "\n## "); j >= 0 {
		body = body[:j+1]
	}
	if len(body) < 1000 {
		t.Fatalf("%s's §6.15 is %d bytes: the section boundary moved and these checks would read almost nothing", coordinatorContractPath, len(body))
	}
	return body
}

// TestConsoleContractNamesEveryShowField: §6.15's field table lists exactly the
// json keys coordinator.PRDocument carries, in both directions — the same check
// §6.13 gets, for the same reason. The document is flat and its table is the
// only one in the section, so a key added to the struct and not to the table is
// a field the console's decoder was never told about, and a key in the table
// that the struct does not carry is a reader written against a field no command
// emits.
func TestConsoleContractNamesEveryShowField(t *testing.T) {
	listed := coordinatorListedKeys(t, coordinatorShowSection(t))
	if len(listed) < 9 {
		t.Fatalf("%s's §6.15 table lists %d key-shaped names: the table shape changed and this test stopped looking", coordinatorContractPath, len(listed))
	}
	rt := reflect.TypeOf(coordinator.PRDocument{})
	real := map[string]bool{}
	for i := 0; i < rt.NumField(); i++ {
		tag := rt.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		key := strings.Split(tag, ",")[0]
		real[key] = true
		if !listed[key] {
			t.Errorf("%s's §6.15 table never lists the json field %q, which coordinator.PRDocument carries", coordinatorContractPath, key)
		}
	}
	for key := range listed {
		if !real[key] {
			t.Errorf("%s's §6.15 field table names %q, which is not a json key of coordinator.PRDocument: a field invented rather than read", coordinatorContractPath, key)
		}
	}
}

// TestConsoleContractNamesOnlyRealShowFlags: every --flag §6.15 writes on a line
// naming its command is a flag of newCoordinatorShowCmd, read off cobra rather
// than against a list kept here, and the heading's argv is the command the row
// names. It is the check that keeps a document from promising the console a flag
// the command does not have — on a read whose argv reaches a host.
func TestConsoleContractNamesOnlyRealShowFlags(t *testing.T) {
	body := coordinatorShowSection(t)
	cmd := newCoordinatorShowCmd()
	real := map[string]bool{}
	cmd.Flags().VisitAll(func(f *pflag.Flag) { real[f.Name] = true })
	for _, want := range []string{"repo", "pr", "json", "project", "config"} {
		if !real[want] {
			t.Errorf("coordinator show has no --%s, and %s documents it as one of the command's flags", want, coordinatorContractPath)
		}
	}

	const head = "### 6.15 `fugaro coordinator show"
	if i := strings.Index(body, head); i < 0 {
		t.Fatalf("%s's §6.15 heading is not the command's argv any more: want %q", coordinatorContractPath, head)
	}
	for _, want := range []string{"--repo", "--pr", "--json"} {
		if !strings.Contains(body[:strings.Index(body, "\n")], want) {
			t.Errorf("%s's §6.15 heading no longer names %s: the row the console spawns is what the console passes", coordinatorContractPath, want)
		}
	}

	named := 0
	for _, line := range strings.Split(body, "\n") {
		if !strings.Contains(line, "newCoordinatorShowCmd") {
			continue
		}
		named++
		for _, m := range coordinatorFlagRE.FindAllStringSubmatch(line, -1) {
			name := strings.TrimRight(m[1], "-")
			if name == "" {
				continue
			}
			if !real[name] {
				t.Errorf("%s's §6.15 names --%s as a flag of newCoordinatorShowCmd, and the command has no such flag", coordinatorContractPath, name)
			}
		}
	}
	if named == 0 {
		t.Fatalf("%s's §6.15 no longer names newCoordinatorShowCmd: these checks would pass vacuously", coordinatorContractPath)
	}

	// §8's inventory row, as §6.13's check reads its own: the console may spawn
	// this command too (coordinator v2 C3's console half wrote the row), and the
	// two flags it fixes are the operation's arguments -- so the row names them
	// and the cloud group's two, and every flag it does name is one the command
	// has.
	row := coordinatorInventoryRow(t, "coordinator show")
	for _, want := range []string{"--repo", "--pr", "--project", "--config"} {
		if !strings.Contains(row, want) {
			t.Errorf("%s's §8 row for coordinator show no longer names %s: %q", coordinatorContractPath, want, row)
		}
	}
	for _, m := range coordinatorFlagRE.FindAllStringSubmatch(row, -1) {
		name := strings.TrimRight(m[1], "-")
		if name != "" && !real[name] {
			t.Errorf("%s's §8 row names --%s, which coordinator show has no flag for", coordinatorContractPath, name)
		}
	}
}
