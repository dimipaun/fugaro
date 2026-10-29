package digest

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/runstore"
)

// TestDigestNeedsNoEE: the digest is MIT and stays MIT (D444, T11), so this
// package and everything it imports must build with ee/ deleted, and it must
// not read internal/report either, since that moves to ee/report. The digest
// carries its own copy of the period vocabulary and the dollar rendering
// (period.go) for exactly this reason.
//
// The two halves below are what that costs: the package still writes a digest
// from an untagged build, and nothing its own .go files import -- test files
// included -- reaches into ee/ or into internal/report.
func TestDigestNeedsNoEE(t *testing.T) {
	t.Run("an untagged build writes a digest", func(t *testing.T) {
		rec := run(t, "20261021-090000-cccc", "2026-10-21T09:00:00Z", 1.50, 0,
			runstore.StageTiming{Name: "implement", Calls: counted(20), Agents: counted(1)},
		)
		out := Markdown(Build([]runstore.Record{rec}, week43(t)))
		if len(out) == 0 {
			t.Fatal("Build + Markdown wrote an empty digest")
		}
	})

	t.Run("the import closure stays in the core", func(t *testing.T) {
		root, err := moduleRoot()
		if err != nil {
			t.Fatal(err)
		}
		const module = "github.com/dimipaun/fugaro/"
		eePrefix := module + "ee/"
		// Spelled in two pieces so this test's own source holds no literal of
		// the import path it refuses: the digest's independence is checked by
		// grepping the tree for that import, and this file must not turn up in
		// that list.
		reportPath := module + "internal/" + "report"

		type step struct{ dir, chain string }
		seen := map[string]bool{}
		queue := []step{{filepath.Join(root, "internal", "digest"), "internal/digest"}}
		for len(queue) > 0 {
			s := queue[0]
			queue = queue[1:]
			if seen[s.dir] {
				continue
			}
			seen[s.dir] = true
			imports, err := importsOf(s.dir, s.dir == filepath.Join(root, "internal", "digest"))
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range imports {
				if strings.HasPrefix(imp, eePrefix) {
					t.Fatalf("internal/digest reaches %s from %s; the digest is MIT and must build with ee/ deleted", imp, s.chain)
				}
				if imp == reportPath || strings.HasPrefix(imp, reportPath+"/") {
					t.Fatalf("internal/digest reaches %s from %s; it moves to ee/report, and the digest must build without it", imp, s.chain)
				}
				if !strings.HasPrefix(imp, module) {
					continue
				}
				queue = append(queue, step{filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(imp, module))), s.chain + " -> " + strings.TrimPrefix(imp, module)})
			}
		}
	})
}

// moduleRoot walks up from the test's directory to the directory holding
// go.mod. Hermetic: no go list, no network.
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}

// importsOf is every import path the .go files in dir declare, read with
// parser.ImportsOnly. Test files are read for the digest's own package (a
// test-only import that reaches ee/ breaks `go test ./internal/digest/` just
// as hard) and skipped everywhere else: another package's tests are not
// compiled into the digest's build or its test binary, so an import they make
// -- internal/budget's tests read internal/cli, which still imports
// internal/report today -- says nothing about what the digest needs.
func importsOf(dir string, includeTests bool) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		if !includeTests && strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, parser.ImportsOnly)
		if err != nil {
			return nil, err
		}
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return nil, err
			}
			out = append(out, p)
		}
	}
	return out, nil
}
