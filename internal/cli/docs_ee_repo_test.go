package cli

// The one reader for the real repository's ee/ tree (licensing plan L1.2).
//
// ee/ is reserved: it holds a README that says so and nothing else, and the
// folder's whole point is that nothing in the core depends on it. A test that
// reads it therefore reads something the core is supposed to survive without,
// and the licensing plan's "core without ee/" workflow deletes the directory to
// prove the core does: every test that reads the real ee/ has to be known by
// name, so that workflow can skip exactly those tests and nothing else.
//
// That is what this file is: readRealEE, the ONE helper through which a test in
// this package reads the real ee/, and eeReadingTests, the exact list of the
// tests that call it. TestEEReadersAreListed holds both halves of that contract
// -- the list names every reader in this package and nothing else, and no
// _test.go file in the module reads ee/ around the helper.
//
// A test that only MATCHES CONTENT naming ee/ is not a reader and is allowed:
// LICENSING.md's link to ee/README.md, the staged workflow's `rm -rf ee`, an
// archive glob. The scan below is about filesystem calls, not about words.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// eeDirName is the directory at the module root this file's helper reads and
// the path segment the scan below looks for.
const eeDirName = "ee"

// eeRepoFile is this file, relative to the module root. It holds the helper, so
// the scan below exempts it by name: the helper necessarily builds a path into
// ee/, and a test cannot list itself without naming itself. If this file is ever
// renamed, the scan reports it as a file that reads ee/ and is not the helper --
// a loud failure whose fix is this constant.
const eeRepoFile = "internal/cli/docs_ee_repo_test.go"

// eeBoundaryPackage is the test-only package that plants its own ee/ trees
// under t.TempDir() (licensing plan L1.1). It is exempt from the scan below for
// the same reason the helper is: its fixtures are directories it built itself,
// never the repository's own ee/.
const eeBoundaryPackage = "internal/eeboundary/"

// eeReadingTests is the exact list of this package's tests that read the real
// repository's ee/ through readRealEE. Adding a reader means adding its name
// here in the same commit, and TestEEReadersAreListed fails both ways: a reader
// that is missing from the list, and a name here whose test no longer reads ee/.
//
// The list is what the "core without ee/" workflow skips, so a name left in it
// after its test stopped reading ee/ costs coverage for nothing; a reader left
// out of it takes that workflow down.
var eeReadingTests = []string{
	"TestDocsEELicensed",
	"TestDocsLicensingLicenceRefCopy",
	"TestDocsLicensingStatement",
}

// readRealEE reads one path of the real repository's ee/ tree, so that every
// read of it goes through one place the workflow can be told about.
//
// rel is slash-separated and relative to ee/: readRealEE(t, "README.md") reads
// ee/README.md. The empty rel names ee/ itself, and readRealEE then answers the
// directory's entries, sorted, one per line -- which files the folder holds is a
// fact about ee/ like any other, and a reader that reached for os.ReadDir itself
// would be a second place the workflow has to know about.
//
// It fails the calling test rather than returning an error: a missing ee/ is
// exactly what a reader must not quietly read as an empty file.
func readRealEE(t *testing.T, rel string) string {
	t.Helper()
	root := consoleBoundaryRoot(t)
	p := filepath.Join(root, eeDirName, filepath.FromSlash(rel))
	if rel == "" {
		entries, err := os.ReadDir(p)
		if err != nil {
			t.Fatalf("cannot list the repository's %s/ directory: %v", eeDirName, err)
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		slices.Sort(names)
		return strings.Join(names, "\n")
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("cannot read %s/%s: %v", eeDirName, rel, err)
	}
	return string(data)
}

// eeRootRelativeReader is this package's own reader of module-root-relative
// files, and the one call in the scan whose argument is not written from the
// file's own directory: readDoc prepends "../../" itself, so readDoc(t,
// "ee/README.md") in internal/cli reads the repository's ee/README.md, while
// every other call above writes its path the way filepath.Join would from where
// the file sits.
const eeRootRelativeReader = "readDoc"

// eeFilesystemCalls is the set of standard-library functions a test could use to
// read the real ee/ without going through the helper: the calls the licensing
// plan names, and nothing else, so a file that merely names ee/ in a string it
// compares is not reported. "fs" is matched by package rather than by name --
// every function that package exports takes or answers a path.
var eeFilesystemCalls = map[string][]string{
	"os":       {"ReadFile", "Open", "OpenFile", "Stat", "Lstat", "ReadDir", "Readlink", "Glob"},
	"ioutil":   {"ReadFile", "ReadDir"},
	"filepath": {"Join", "Abs", "Glob", "Walk", "WalkDir", "EvalSymlinks", "Rel"},
	"path":     {"Join"},
	"fs":       nil,
}

// eeFilesystemCall reports whether a call is one of eeFilesystemCalls. A call
// written as a bare identifier is one of this package's own helpers, and
// eeRootRelativeReader is the only one on that list.
func eeFilesystemCall(call *ast.CallExpr) bool {
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		pkg, ok := fun.X.(*ast.Ident)
		if !ok {
			return false
		}
		names, ok := eeFilesystemCalls[pkg.Name]
		if !ok {
			return false
		}
		return names == nil || slices.Contains(names, fun.Sel.Name)
	case *ast.Ident:
		return fun.Name == eeRootRelativeReader
	}
	return false
}

// eeCallBaseDir is the directory a call's path arguments are written in: the
// file's own, except for eeRootRelativeReader, whose argument is written from
// the module root.
func eeCallBaseDir(fileRel string, call *ast.CallExpr) string {
	if id, ok := call.Fun.(*ast.Ident); ok && id.Name == eeRootRelativeReader {
		return "."
	}
	return path.Dir(fileRel)
}

// eeCallReadsEE reports whether a filesystem call names the repository's ee/
// directory. Each argument is read as a path expression: a string literal, or a
// filepath.Join/path.Join whose literal arguments are joined in order, which is
// the one way a path is written in pieces. A path built from variables is not
// followed -- "testdata/" + name is a detector's limit, not a claim that the
// path is harmless -- and a call with no literal path argument is not a read of
// anything this scan can see.
//
// A path is resolved against the directory the call writes it in (eeCallBaseDir
// above), and it has to land ON the root's ee directory, so "../../ee/README.md"
// from internal/cli and readDoc(t, "ee/README.md") are both the repository's
// ee/, while "docs/ee/x.md" and a temp tree the test made itself are not.
func eeCallReadsEE(fileRel string, call *ast.CallExpr) bool {
	baseDir := eeCallBaseDir(fileRel, call)
	for _, arg := range call.Args {
		if eePathExpressionReadsEE(baseDir, arg) {
			return true
		}
	}
	return false
}

// eePathExpressionReadsEE asks the same question of one argument, resolving it
// from baseDir through the resolution the console boundary shares.
func eePathExpressionReadsEE(baseDir string, expr ast.Expr) bool {
	switch v := expr.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return false
		}
		s, err := strconv.Unquote(v.Value)
		return err == nil && boundaryResolvedIntoDir(baseDir, s, eeDirName)
	case *ast.CallExpr:
		if !consoleBoundaryJoinCall(v) {
			return false
		}
		var parts []string
		for _, a := range v.Args {
			lit, ok := a.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			if s, err := strconv.Unquote(lit.Value); err == nil {
				parts = append(parts, s)
			}
		}
		return len(parts) > 0 && boundaryResolvedIntoDir(baseDir, strings.Join(parts, "/"), eeDirName)
	}
	return false
}

// eeFileReadsEE reports whether the Go file whose module-relative path is fileRel
// reads the real ee/ through one of eeFilesystemCalls.
//
// A file the parser refuses has no AST to attach a call to, so its quoted
// literals are read one by one, and each comma-separated run of them as the Join
// spelling: the same two spellings the parsed half reads. Reading them without a
// call around them over-reports -- a literal that merely names an ee/ path counts
// as a read -- which is the direction the plan's rule takes, and it is what keeps
// a deliberately broken fixture from carrying a read past the scan.
func eeFileReadsEE(fileRel, name, src string) bool {
	f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.SkipObjectResolution)
	if err != nil {
		baseDir := path.Dir(fileRel)
		for _, q := range consoleStringLitRE.FindAllString(src, -1) {
			if s, err := strconv.Unquote(q); err == nil && boundaryResolvedIntoDir(baseDir, s, eeDirName) {
				return true
			}
		}
		for _, run := range consoleStringRunRE.FindAllString(src, -1) {
			var parts []string
			for _, q := range consoleStringLitRE.FindAllString(run, -1) {
				if s, err := strconv.Unquote(q); err == nil {
					parts = append(parts, s)
				}
			}
			if len(parts) > 1 && boundaryResolvedIntoDir(baseDir, strings.Join(parts, "/"), eeDirName) {
				return true
			}
		}
		return false
	}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && eeFilesystemCall(call) && eeCallReadsEE(fileRel, call) {
			found = true
		}
		return true
	})
	return found
}

// eeReaderTestsInCLI is the name of every top-level test in this package whose
// body calls readRealEE, sorted. The whole body is read, closures included, so a
// call made from inside a t.Run subtest counts.
func eeReaderTestsInCLI(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(token.NewFileSet(), e.Name(), src, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("%s does not parse: %v", e.Name(), err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Body == nil || !strings.HasPrefix(fn.Name.Name, "Test") {
				continue
			}
			reads := false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "readRealEE" {
						reads = true
					}
				}
				return true
			})
			if reads {
				out = append(out, fn.Name.Name)
			}
		}
	}
	slices.Sort(out)
	return out
}

// TestEEReadersAreListed holds the contract of this file: the tests that read
// the real ee/ are exactly the names in eeReadingTests, and no test in the
// module reads ee/ around the helper.
func TestEEReadersAreListed(t *testing.T) {
	root := consoleBoundaryRoot(t)
	files, err := consoleBoundaryWalk(root, consoleBoundaryIgnoredDirs(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 100 {
		t.Fatalf("found %d .go files under %s: the walk lost the module", len(files), root)
	}

	readers := eeReaderTestsInCLI(t)
	listed := slices.Clone(eeReadingTests)
	slices.Sort(listed)
	if len(listed) == 0 {
		t.Fatal("eeReadingTests is empty: the helper reads the real ee/, so at least one test must read it through the helper")
	}
	if !slices.Equal(readers, listed) {
		t.Errorf(`the tests that read the real %s/ are not the ones eeReadingTests names:
  read ee/ through readRealEE: %v
  named in eeReadingTests:     %v

A new reader joins the list in the same commit, and a name whose test no longer reads ee/ leaves it: the "core without ee/" workflow skips exactly this list, so a name left behind costs coverage and a reader left out takes that workflow down.`, eeDirName, readers, listed)
	}

	var found []string
	for _, rel := range files {
		if !strings.HasSuffix(rel, "_test.go") || rel == eeRepoFile || strings.HasPrefix(rel, eeBoundaryPackage) {
			continue
		}
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if eeFileReadsEE(rel, rel, string(src)) {
			found = append(found, rel)
		}
	}
	if len(found) > 0 {
		t.Errorf(`a test reads the real %s/ without going through readRealEE:
  %s

ee/ is reserved and the core is expected to build and test without it, so every read of the real directory goes through the one helper in %s and every such test is named in eeReadingTests. A test that only matches content naming ee/ -- a link in LICENSING.md, the workflow's own rm -rf -- is not a read and is not reported here.`,
			eeDirName, strings.Join(found, "\n  "), eeRepoFile)
	}
}
