// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See ee/LICENSE for the terms.

package cli

// The console contract's report half, checked against the tree this package is
// part of (E2.2b1): §4 of console/docs/json-contract.md and the design's §5.1
// row for `report --json` document THIS package's command and the shapes in
// ee/report, so the checks that read the code back off those sentences live
// here, where the command and the shapes are. The core's own
// internal/cli/docs_desktop_console_test.go carries the same checks for every
// section whose command is still in the core, and skips §4 and §8's report row
// (consoleMovedSections, consoleMovedRows), because an untagged build compiles
// neither the command nor the shapes it documents.

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

	"github.com/spf13/pflag"

	report "github.com/dimipaun/fugaro/ee/report"
)

// reportContractPath and reportDesignPath are the two documents that describe
// this command, read from this package: the contract the console's decoder is
// written against, and the design whose spawn table is the console's whole
// read-only guarantee.
const (
	reportContractPath = "../../../console/docs/json-contract.md"
	reportDesignPath   = "../../../docs/design/desktop-console.md"
)

// reportFlagRE finds a --flag the documents write, the same pattern the core's
// own contract tests read flags with.
var reportFlagRE = regexp.MustCompile("--([a-z][a-z-]*)")

// reportKeyRE and reportCellRE read a document's backticked tokens: a token
// that is lowercase, digits and underscores is a json key or a mode value; a Go
// type (`report.JSONDoc`), a path (`ee/report/render.go`) or a phrase is not.
var (
	reportKeyRE  = regexp.MustCompile("^[a-z][a-z0-9_]*$")
	reportCellRE = regexp.MustCompile("`([^`]+)`")
)

// reportFileString is the file read every check here starts from.
func reportFileString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// reportSection is the contract's §4, from its heading to §5's, so a check
// cannot pass on a field named in some other section's prose.
func reportSection(t *testing.T) string {
	t.Helper()
	doc := reportFileString(t, reportContractPath)
	const head = "## 4. `fugaro report --json`"
	i := strings.Index(doc, head)
	if i < 0 {
		t.Fatalf("%s no longer carries the %q section this package's command is documented in", reportContractPath, head)
	}
	body := doc[i:]
	if j := strings.Index(body[1:], "\n## 5. "); j >= 0 {
		body = body[:j+1]
	}
	if len(body) < 2500 {
		t.Fatalf("%s's §4 is %d bytes: the section boundary moved and these checks would read almost nothing", reportContractPath, len(body))
	}
	return body
}

// reportViewTypes are the three shapes §4 documents: the document, the line
// under it, and the token block a line may carry.
func reportViewTypes() []reflect.Type {
	return []reflect.Type{
		reflect.TypeOf(report.JSONDoc{}),
		reflect.TypeOf(report.JSONLine{}),
		reflect.TypeOf(report.JSONTokens{}),
	}
}

// reportJSONKeys is every json key the three shapes carry, dug out of the
// structs — through slices and pointers, so JSONDoc's `rows` reaches JSONLine's
// own keys — so a field added in Go is a field this test demands in the
// document.
func reportJSONKeys() []string {
	var out []string
	var walk func(t reflect.Type)
	walk = func(rt reflect.Type) {
		switch rt.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			walk(rt.Elem())
			return
		case reflect.Struct:
		default:
			return
		}
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name != "" && name != "-" && !slices.Contains(out, name) {
				out = append(out, name)
			}
			walk(f.Type)
		}
	}
	for _, rt := range reportViewTypes() {
		walk(rt)
	}
	slices.Sort(out)
	return out
}

// TestConsoleContractNamesEveryReportField: §4 names every json key the three
// shapes carry. A key the code writes and the document does not name is a field
// the console's decoder was never told about -- the drift the section's own
// "the exact field names" paragraph exists to prevent.
func TestConsoleContractNamesEveryReportField(t *testing.T) {
	body := reportSection(t)
	keys := reportJSONKeys()
	if len(keys) < 25 {
		t.Fatalf("the three shapes carry %d json keys: the walk is looking at the wrong structs", len(keys))
	}
	for _, key := range keys {
		if !strings.Contains(body, "`"+key+"`") {
			t.Errorf("%s's §4 never names the json field %q, which the shape it documents carries", reportContractPath, key)
		}
	}
}

// reportInventoryRow is §8's flag-inventory row for this command: the
// document's own account of what the console may pass it.
func reportInventoryRow(t *testing.T) string {
	t.Helper()
	for _, line := range strings.Split(reportFileString(t, reportContractPath), "\n") {
		if strings.HasPrefix(line, "| `report`") {
			return line
		}
	}
	t.Fatalf("%s's flag inventory has no `report` row: the console spawns it, and §8 is where its flags are stated", reportContractPath)
	return ""
}

// reportDesignRow is the design's §5.1 spawn row for this command, the row that
// says which argvs the console may pass it.
func reportDesignRow(t *testing.T) string {
	t.Helper()
	var row string
	for _, line := range strings.Split(reportFileString(t, reportDesignPath), "\n") {
		if strings.Contains(line, "`fugaro report --json`") {
			row += line + "\n"
		}
	}
	if row == "" {
		t.Fatalf("%s no longer carries the console's argv row for this command", reportDesignPath)
	}
	return row
}

// TestConsoleContractNamesOnlyRealReportFlags: every --flag §4 writes — on the
// line naming the command's constructor and in §4's own flags line — and every
// one §8's inventory row and the design's spawn row write, is a flag of the
// command this package builds, read off cobra rather than against a list kept
// here, so a flag invented in prose fails.
func TestConsoleContractNamesOnlyRealReportFlags(t *testing.T) {
	body := reportSection(t)
	cmd := newReportCmd(reportDeps)
	real := map[string]bool{}
	cmd.Flags().VisitAll(func(f *pflag.Flag) { real[f.Name] = true })
	if len(real) < 8 {
		t.Fatalf("report carries %d flags, want the read's own plus the cloud flags: the constructor is not the command this test documents", len(real))
	}
	for _, want := range []string{"repo", "since", "until", "by", "json", "csv", "project", "config"} {
		if !real[want] {
			t.Errorf("report has no --%s, and %s documents it as one of the command's flags", want, reportContractPath)
		}
	}

	// §4's heading is the argv the design row passes, and the section says so.
	const head = "## 4. `fugaro report --json`"
	if !strings.HasPrefix(body, head) {
		t.Fatalf("%s's §4 heading is not the command's argv any more: want %q", reportContractPath, head)
	}

	// Every --flag the section writes on a line naming the command's
	// constructor, against it. The exported constructor is NewReportCmd; the
	// unexported newReportCmd is the same command with the seam injected, so a
	// line naming either is a line about this command.
	named := 0
	for _, line := range strings.Split(body, "\n") {
		if !strings.Contains(line, "NewReportCmd") && !strings.Contains(line, "newReportCmd") {
			continue
		}
		named++
		for _, m := range reportFlagRE.FindAllStringSubmatch(line, -1) {
			name := strings.TrimRight(m[1], "-")
			if name == "" {
				continue
			}
			if !real[name] {
				t.Errorf("%s's §4 names --%s as a flag of the report's constructor, and the command has no such flag", reportContractPath, name)
			}
		}
	}
	if named == 0 {
		t.Fatalf("%s's §4 no longer names NewReportCmd: these checks would pass vacuously", reportContractPath)
	}

	// §8's inventory row: one line per command the console may spawn, and it
	// answers whether this command carries the cloud flags. A row that says it
	// does and a command that does not is the console's spawn refused on the
	// first installation that is not the ambient one.
	row := reportInventoryRow(t)
	if !strings.Contains(row, "NewReportCmd") && !strings.Contains(row, "newReportCmd") {
		t.Errorf("%s's §8 row for report no longer names the command's constructor: %q", reportContractPath, row)
	}
	if strings.Contains(row, "yes") {
		for _, want := range []string{"project", "config"} {
			if !real[want] {
				t.Errorf("%s's §8 row says report carries the cloud flags, and the command has no --%s", reportContractPath, want)
			}
		}
	}
	for _, m := range reportFlagRE.FindAllStringSubmatch(row, -1) {
		if name := strings.TrimRight(m[1], "-"); name != "" && !real[name] {
			t.Errorf("%s's §8 row names --%s, which the command has no flag for", reportContractPath, name)
		}
	}

	// The design's spawn row is the console's argv: the flags the picker and the
	// breakdown switch pass. §5.1 rule 6 excludes --csv — a second document
	// format the console does not read — so a row that admits it would widen the
	// spawn contract.
	design := reportDesignRow(t)
	for _, want := range []string{"--by", "--since", "--until", "--repo"} {
		if !strings.Contains(design, want) {
			t.Errorf("%s no longer names %s in the console's argv row for report: %q", reportDesignPath, want, design)
		}
	}
	for _, m := range reportFlagRE.FindAllStringSubmatch(design, -1) {
		if name := strings.TrimRight(m[1], "-"); name != "" && !real[name] {
			t.Errorf("%s's report row names --%s, which the command has no flag for", reportDesignPath, name)
		}
	}
	if strings.Contains(design, "--csv") {
		t.Errorf("%s's report row admits --csv: §5.1 rule 6 keeps the console's spawn table to --by, --since, --until and --repo", reportDesignPath)
	}
}

// TestConsoleContractCitesThisPackagesFilesAndSymbols: the paths and the
// `report.X` names §4 and the design's row cite are the moved ones and they
// resolve. It is the check the move made necessary: both documents cite their
// command by path and by symbol, so a citation left pointing at internal/cli/
// or internal/report/ is a reader sent to a file that no longer exists, and a
// symbol renamed here is a citation that reads as checked and is not.
func TestConsoleContractCitesThisPackagesFilesAndSymbols(t *testing.T) {
	var decls []string
	reportDecls(t, "../.", &decls)
	reportDecls(t, ".", &decls)
	if len(decls) < 30 {
		t.Fatalf("ee/report and ee/report/cli declare %d top-level names: the scan is looking in the wrong place", len(decls))
	}

	// §4's own command line is the console reader's entry point, so it cites
	// the constructor a reader can call, and this package declares it.
	body := reportSection(t)
	if !strings.Contains(body, "NewReportCmd") {
		t.Errorf("%s's §4 no longer cites NewReportCmd: the command's line is where a reader is sent to the constructor", reportContractPath)
	}
	if !slices.Contains(decls, "NewReportCmd") {
		t.Errorf("ee/report/cli declares no NewReportCmd, and %s's §4 cites it", reportContractPath)
	}

	cited := 0
	for _, doc := range []struct{ path, text string }{
		{reportContractPath, reportSection(t)},
		{reportDesignPath, reportDesignRow(t)},
	} {
		for _, m := range reportCellRE.FindAllStringSubmatch(doc.text, -1) {
			cite := m[1]
			switch {
			case strings.HasPrefix(cite, "internal/cli/report"),
				strings.HasPrefix(cite, "internal/report/"):
				t.Errorf("%s cites %q, which the move left behind: the report's library and command are under ee/ now", doc.path, cite)
			case strings.HasPrefix(cite, "ee/report/") && strings.HasSuffix(cite, ".go"):
				cited++
				if _, err := os.Stat(filepath.Join("../../..", cite)); err != nil {
					t.Errorf("%s cites %q, which is not in the tree: %v", doc.path, cite, err)
				}
			case strings.HasPrefix(cite, "report."):
				// `report.JSON`, `report.JSONDoc`: a symbol of the moved
				// library, cited by its own name. A citation may carry the
				// signature (`report.JSON(w io.Writer, …) error`), so the name
				// is what stands before the first parenthesis.
				cited++
				name := strings.TrimPrefix(cite, "report.")
				if i := strings.IndexAny(name, "( "); i >= 0 {
					name = name[:i]
				}
				if !slices.Contains(decls, name) {
					t.Errorf("%s cites report.%s, which ee/report does not declare", doc.path, name)
				}
			}
		}
	}
	if cited == 0 {
		t.Fatalf("%s and %s cite no file or symbol of this package: these checks would pass vacuously", reportContractPath, reportDesignPath)
	}
}

// reportDecls appends every top-level name declared by the non-test Go files of
// the package at dir: the functions, types and values a citation can name.
func reportDecls(t *testing.T, dir string, names *[]string) {
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
				// A method is cited as Type.Method, so the receiver is part of
				// the name a citation has to match.
				if recv := reportReceiverName(v.Recv); recv != "" {
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

// reportReceiverName is the base type name of a method's receiver, "" for a
// function: the first half of the Type.Method shape a citation takes.
func reportReceiverName(recv *ast.FieldList) string {
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
