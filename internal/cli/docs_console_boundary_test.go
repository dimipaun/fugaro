package cli

// The Go/console boundary (desktop-console plan, Task 0.3; design section 8.1
// item 4 and section 9.5 item 2).
//
// The console lives in this repository under console/ (design D3) and is not a
// Go package: go build ./..., go vet ./... and gofmt -l . take no notice of it,
// go.mod and go.sum gain nothing from it, and no Go file in this module builds
// against a path under console/. Three named tests hold that claim, so it stays
// true a year after the task that made it:
//
//   - TestConsoleIsOutsideTheGoModule -- (a1) no Go file in the module has an
//     import under console/; (a3) the one-entry allow-list of files permitted
//     to MENTION a console/ path, plus the two halves of that list's contract;
//     and that console/ itself holds no Go code, no go.mod and no go.sum.
//   - TestGoModHasNoConsoleDependency -- (b) go.mod's require block is
//     unchanged from the baseline recorded in this file, and go.sum names no
//     module under console/.
//   - TestNoGoFileEmbedsFromConsole -- (a2) no file, test or otherwise, has a
//     go:embed directive over a console/ path.
//
// THE BOUNDARY IS ABOUT CODE, NOT ABOUT BYTES, and that distinction is what
// reconciles this task with Task 0.1. Task 0.1's own Go test --
// internal/cli/docs_desktop_console_test.go -- reads console/docs/json-contract.md
// AS BYTES: readDoc(t, path) on a path built from the repository root, in the
// convention every docs_*_test.go here already uses for docs/. An earlier
// revision of the plan forbade ANY Go file referencing a path under console/,
// which made Task 0.1 impossible to write. So (a1) matches on IMPORT LINES
// only -- an import path is a module path, which is the real boundary, while a
// comment or a string that merely names console/docs/json-contract.md is a read
// of a document -- and the allow-list below carries exactly one entry today:
// the one file those reads live in. Its second half is asserted too, because
// the exception is narrow: an allow-listed file may mention a console/ path and
// may neither import one nor embed one. Widening the list is a deliberate edit
// to the slice below, with a test that fails on a second entry, rather than a
// quiet new reference in some future file.
//
// This file is the scanner, so the mention half of (a3) exempts it by name: it
// necessarily holds the literal "console/" itself (in the checks, in their
// messages, and in the exemption), and a test cannot list itself without naming
// itself. It is NOT exempt from (a1) or (a2): the walk covers every Go file in
// the module including this one, so this file's own imports and its own embed
// directives are checked like any other file's.
//
// Two things this file's scan deliberately does not skip, and one it does:
// console/ itself is walked (a Go file there is exactly what (a3) forbids) and
// so is testdata/ (a reference in a file the build ignores is still a reference
// this contract forbids), while .git/ and node_modules/ are skipped because the
// go command ignores them everywhere.

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// consoleDirName is the directory at the module root the whole boundary is
// about. It is also the token the scans below look for, so it is spelled once.
const consoleDirName = "console"

// consoleBoundarySelf is this file, relative to the module root. The mention
// half of (a3) exempts it, and only it, and only from that half. The path is
// written out rather than derived from runtime.Caller, which reports what the
// compiler recorded and so can be a trimmed path under -trimpath. If this file
// is ever renamed, the scan reports it as a file that mentions a console/ path
// and is not on the allow-list -- a loud failure whose fix is this constant.
const consoleBoundarySelf = "internal/cli/docs_console_boundary_test.go"

// consoleBoundaryAllowList is the ONE-ENTRY allow-list of Go files permitted to
// mention a path under console/ without importing anything from it: the console
// contract's own docs test, which reads console/docs/json-contract.md as bytes
// (Task 0.1, and Task 0.2's sibling test in the same file). Task 0.3 says the
// exception is declared in the test that grants it rather than assumed, so this
// slice is that declaration: one entry, a test named for a second one, and a
// per-entry assertion that the file imports no console/ path and embeds nothing
// from one.
var consoleBoundaryAllowList = []string{
	"internal/cli/docs_desktop_console_test.go",
}

// consoleGoModRequireBaseline is go.mod's require block as recorded when Task
// 0.3 landed -- every require line of both blocks (the direct one and the
// indirect one), whitespace-trimmed and sorted, comments and all. Task 0.3
// states the rule as "unchanged from a recorded baseline, so the console cannot
// smuggle a Go dependency in", and this is that baseline.
//
// The maintenance rule, so a future reader does not read a red test as a
// mystery: a dependency added to go.mod on purpose is a deliberate edit to this
// slice, made in the same commit as the require line and said out loud in that
// commit's message. A require line that arrives WITHOUT an edit here is the
// failure this test exists for.
var consoleGoModRequireBaseline = []string{
	"cel.dev/expr v0.25.2 // indirect",
	"cloud.google.com/go v0.123.0 // indirect",
	"cloud.google.com/go/auth v0.23.3 // indirect",
	"cloud.google.com/go/auth/oauth2adapt v0.2.8 // indirect",
	"cloud.google.com/go/compute/metadata v0.9.1 // indirect",
	"cloud.google.com/go/iam v1.12.0 // indirect",
	"cloud.google.com/go/monitoring v1.30.0 // indirect",
	"cloud.google.com/go/storage v1.68.0",
	"github.com/GoogleCloudPlatform/opentelemetry-operations-go/detectors/gcp v1.34.0 // indirect",
	"github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/metric v0.57.0 // indirect",
	"github.com/GoogleCloudPlatform/opentelemetry-operations-go/internal/resourcemapping v0.57.0 // indirect",
	"github.com/aymanbagabas/go-osc52/v2 v2.0.1 // indirect",
	"github.com/bmatcuk/doublestar/v4 v4.10.2",
	"github.com/cespare/xxhash/v2 v2.3.0 // indirect",
	"github.com/charmbracelet/bubbletea v1.3.10",
	"github.com/charmbracelet/colorprofile v0.2.3-0.20250311203215-f60798e515dc // indirect",
	"github.com/charmbracelet/lipgloss v1.1.0 // indirect",
	"github.com/charmbracelet/x/ansi v0.10.1 // indirect",
	"github.com/charmbracelet/x/cellbuf v0.0.13-0.20250311204145-2c3ea96c31dd // indirect",
	"github.com/charmbracelet/x/term v0.2.1 // indirect",
	"github.com/cncf/xds/go v0.0.0-20260202195803-dba9d589def2 // indirect",
	"github.com/creack/pty v1.1.24 // tests only: the hidden-prompt test needs a pseudo-terminal",
	"github.com/envoyproxy/go-control-plane/envoy v1.37.0 // indirect",
	"github.com/envoyproxy/protoc-gen-validate v1.3.3 // indirect",
	"github.com/erikgeiser/coninput v0.0.0-20211004153227-1c3628e74d0f // indirect",
	"github.com/felixge/httpsnoop v1.1.0 // indirect",
	"github.com/go-jose/go-jose/v4 v4.1.4 // indirect",
	"github.com/go-logr/logr v1.4.3 // indirect",
	"github.com/go-logr/stdr v1.2.2 // indirect",
	"github.com/google/s2a-go v0.1.10 // indirect",
	"github.com/google/uuid v1.6.0 // indirect",
	"github.com/google/wire v0.7.0 // indirect",
	"github.com/googleapis/enterprise-certificate-proxy v0.3.22 // indirect",
	"github.com/googleapis/gax-go/v2 v2.24.1 // indirect",
	"github.com/inconshreveable/mousetrap v1.1.0 // indirect",
	"github.com/klauspost/compress v1.20.1",
	"github.com/lucasb-eyer/go-colorful v1.2.0 // indirect",
	"github.com/mattn/go-isatty v0.0.20 // indirect",
	"github.com/mattn/go-localereader v0.0.1 // indirect",
	"github.com/mattn/go-runewidth v0.0.16",
	"github.com/muesli/ansi v0.0.0-20230316100256-276c6243b2f6 // indirect",
	"github.com/muesli/cancelreader v0.2.2 // indirect",
	"github.com/muesli/termenv v0.16.0 // indirect",
	"github.com/planetscale/vtprotobuf v0.6.1-0.20240319094008-0393e58bdf10 // indirect",
	"github.com/rivo/uniseg v0.4.7 // indirect",
	"github.com/santhosh-tekuri/jsonschema/v6 v6.0.3",
	"github.com/spf13/cobra v1.10.2",
	"github.com/spf13/pflag v1.0.9 // indirect",
	"github.com/spiffe/go-spiffe/v2 v2.8.1 // indirect",
	"github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect",
	"go.opentelemetry.io/auto/sdk v1.2.1 // indirect",
	"go.opentelemetry.io/contrib/detectors/gcp v1.44.0 // indirect",
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.68.0 // indirect",
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.69.0 // indirect",
	"go.opentelemetry.io/otel v1.44.0 // indirect",
	"go.opentelemetry.io/otel/metric v1.44.0 // indirect",
	"go.opentelemetry.io/otel/sdk v1.44.0 // indirect",
	"go.opentelemetry.io/otel/sdk/metric v1.44.0 // indirect",
	"go.opentelemetry.io/otel/trace v1.44.0 // indirect",
	"gocloud.dev v0.46.0",
	"golang.org/x/crypto v0.57.0 // indirect",
	"golang.org/x/net v0.59.0 // indirect",
	"golang.org/x/oauth2 v0.37.0",
	"golang.org/x/sync v0.23.0 // indirect",
	"golang.org/x/sys v0.48.0",
	"golang.org/x/term v0.46.0 // the hidden prompt of fugaro secrets set",
	"golang.org/x/text v0.42.0 // indirect",
	"golang.org/x/time v0.16.0 // indirect",
	"golang.org/x/xerrors v0.0.0-20240903120638-7835f813f4da // indirect",
	"google.golang.org/api v0.299.0",
	"google.golang.org/genproto v0.0.0-20260715232425-e75dac1f907d // indirect",
	"google.golang.org/genproto/googleapis/api v0.0.0-20260715232425-e75dac1f907d // indirect",
	"google.golang.org/genproto/googleapis/rpc v0.0.0-20260921155816-b14227669459 // indirect",
	"google.golang.org/grpc v1.84.0 // indirect",
	"google.golang.org/protobuf v1.36.12 // indirect",
	"gopkg.in/yaml.v3 v3.0.1",
}

// consoleEmbedDirectiveRE matches a go:embed directive comment and captures its
// patterns.
var consoleEmbedDirectiveRE = regexp.MustCompile(`^//go:embed\s+(.+)$`)

// consoleBoundaryRoot is the module root, read the way this package's docs
// tests read theirs: these tests run with the package directory as their working
// directory, so the root is two levels up. It fails loudly rather than scanning
// an empty tree if the layout ever changes.
func consoleBoundaryRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("no go.mod at %s: these paths assume the test runs from internal/cli: %v", root, err)
	}
	return root
}

// consoleBoundaryDoc reads a repository-root-relative path. It is readDoc's
// convention (docs_base_test.go) applied to console/README.md, which is a
// document like any other even though it lives outside docs/.
func consoleBoundaryDoc(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// consoleBoundaryGoFiles is every .go file in the module, relative to the root,
// slash-separated and sorted. See this file's doc comment for why console/ and
// testdata/ are walked and only .git/ and node_modules/ are skipped.
func consoleBoundaryGoFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && (d.Name() == ".git" || d.Name() == "node_modules") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(files)
	// A scan that silently lost the module would pass every check below by
	// looking at nothing.
	if len(files) < 100 {
		t.Fatalf("found %d .go files under %s: the walk lost the module", len(files), root)
	}
	return files
}

// consoleBoundaryModulePath is go.mod's module path, which is what makes an
// import path checkable: "under console/" means "<module>/console" or below.
func consoleBoundaryModulePath(t *testing.T, root string) string {
	t.Helper()
	gomod := consoleBoundaryDoc(t, "go.mod")
	for _, line := range strings.Split(gomod, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module ")
		if ok {
			if mod := strings.TrimSpace(rest); mod != "" {
				return mod
			}
		}
	}
	t.Fatalf("go.mod at %s declares no module path", root)
	return ""
}

// consoleBoundaryImports returns a Go file's import paths, parsed rather than
// grepped: the match is on import lines, so a comment or a string that merely
// names console/docs/json-contract.md -- Task 0.1's whole reason for the
// allow-list -- is not an import and is not this check's business. The second
// result is false for a file the parser refuses (a deliberately broken fixture
// in some testdata/), which its callers scan as text instead.
func consoleBoundaryImports(t *testing.T, file, src string) ([]string, bool) {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), file, src, parser.ImportsOnly)
	if err != nil {
		return nil, false
	}
	var out []string
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			t.Errorf("%s: the import %s is not a quoted path", file, imp.Path.Value)
			continue
		}
		out = append(out, p)
	}
	return out, true
}

// consoleBoundaryImportReachesConsole reports whether an import path reaches the
// console directory. An import path is a module path, so the first two cases are
// the boundary; the third covers a relative import, which cannot compile in a
// module but is the same reference spelled differently -- the boundary is about
// what a file says.
func consoleBoundaryImportReachesConsole(imp, consolePkg string) bool {
	switch {
	case imp == consolePkg, strings.HasPrefix(imp, consolePkg+"/"):
		return true
	case strings.HasPrefix(imp, "./"), strings.HasPrefix(imp, "../"):
		clean := path.Clean(imp)
		return clean == consoleDirName || strings.HasPrefix(clean, consoleDirName+"/") ||
			strings.Contains(clean, "/"+consoleDirName+"/")
	}
	return false
}

// consoleBoundaryEmbedPatterns returns the patterns of every go:embed directive
// in a file, read off the parsed comments so only a real directive counts; a
// file the parser refuses is scanned line by line instead, so a directive in a
// fixture the build ignores is still seen.
func consoleBoundaryEmbedPatterns(file, src string) []string {
	var out []string
	if f, err := parser.ParseFile(token.NewFileSet(), file, src,
		parser.ParseComments|parser.SkipObjectResolution); err == nil {
		for _, group := range f.Comments {
			for _, c := range group.List {
				if m := consoleEmbedDirectiveRE.FindStringSubmatch(c.Text); m != nil {
					out = append(out, strings.Fields(m[1])...)
				}
			}
		}
		return out
	}
	for _, line := range strings.Split(src, "\n") {
		if m := consoleEmbedDirectiveRE.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			out = append(out, strings.Fields(m[1])...)
		}
	}
	return out
}

// consoleBoundaryEmbedPatternReachesConsole reports whether an embed pattern
// names a file under console/. Patterns are slash-separated paths relative to
// the package directory, so "../console/..." and "./console/..." reach it too.
func consoleBoundaryEmbedPatternReachesConsole(pattern string) bool {
	clean := path.Clean(pattern)
	return clean == consoleDirName || strings.HasPrefix(clean, consoleDirName+"/") ||
		strings.Contains(clean, "/"+consoleDirName+"/") || strings.HasSuffix(clean, "/"+consoleDirName)
}

// consoleRequireLines returns every require line of a go.mod -- from both the
// parenthesised blocks and the single-line form -- whitespace-trimmed and
// sorted, so the comparison below is about what go.mod requires and not about
// the order go mod happens to write it in.
func consoleRequireLines(gomod string) []string {
	var out []string
	inBlock := false
	for _, line := range strings.Split(gomod, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "require ("):
			inBlock = true
		case inBlock && trimmed == ")":
			inBlock = false
		case inBlock:
			if trimmed != "" {
				out = append(out, trimmed)
			}
		case strings.HasPrefix(trimmed, "require "):
			out = append(out, strings.TrimSpace(strings.TrimPrefix(trimmed, "require ")))
		}
	}
	slices.Sort(out)
	return out
}

// TestConsoleIsOutsideTheGoModule is (a1) and (a3) of Task 0.3: no Go file in
// the module builds against a path under console/, the one-entry allow-list of
// files permitted to mention one is exactly what it says it is, and console/
// holds no Go code and no Go module of its own.
func TestConsoleIsOutsideTheGoModule(t *testing.T) {
	root := consoleBoundaryRoot(t)
	files := consoleBoundaryGoFiles(t, root)
	consolePkg := consoleBoundaryModulePath(t, root) + "/" + consoleDirName

	t.Run("no_go_file_imports_a_console_path", func(t *testing.T) {
		for _, rel := range files {
			src := consoleBoundaryDoc(t, rel)
			imports, parsed := consoleBoundaryImports(t, rel, src)
			if !parsed {
				// A file the parser refuses is not compiled either, so it
				// cannot build against console/; it can still say so, and
				// the text is what catches it.
				if strings.Contains(src, consolePkg) {
					t.Errorf("%s names the import path %s and does not parse: the Go module and console/ never touch (design section 8.1 item 4)", rel, consolePkg)
				}
				continue
			}
			for _, imp := range imports {
				if consoleBoundaryImportReachesConsole(imp, consolePkg) {
					t.Errorf("%s imports %q: no Go file in the module builds against a path under %s/ (design section 8.1 item 4, plan Task 0.3 a1)", rel, imp, consoleDirName)
				}
			}
		}
	})

	t.Run("console_holds_no_go_code_and_no_go_mod", func(t *testing.T) {
		dir := filepath.Join(root, consoleDirName)
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("no %s/ at %s: the console lives there (design D3) and Task 0.3 creates it: %v", consoleDirName, root, err)
		}
		var offenders []string
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if p != dir && d.Name() == "node_modules" {
					return fs.SkipDir
				}
				return nil
			}
			switch name := d.Name(); {
			case strings.HasSuffix(name, ".go"), name == "go.mod", name == "go.sum":
				rel, err := filepath.Rel(root, p)
				if err != nil {
					return err
				}
				offenders = append(offenders, filepath.ToSlash(rel))
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(offenders) > 0 {
			t.Errorf("%s/ is not a Go package and holds no module of its own: it may hold no .go file, no go.mod and no go.sum, and it holds %v. Any of them is either a mistake or a second module, and both break the claim that go build ./... and go vet ./... take no notice of console/ (design section 8.1 item 4, plan Task 0.3)", consoleDirName, offenders)
		}
	})

	t.Run("the_allow_list_has_exactly_one_entry", func(t *testing.T) {
		if len(consoleBoundaryAllowList) != 1 {
			t.Errorf("consoleBoundaryAllowList carries %d entries, %v: it is a ONE-entry exception -- the console contract's docs test, which reads console/docs/json-contract.md as bytes. A second entry is a deliberate widening of the boundary and must be argued for in the commit that adds it (plan Task 0.3 a3)", len(consoleBoundaryAllowList), consoleBoundaryAllowList)
		}
	})

	t.Run("only_the_allow_list_may_mention_a_console_path", func(t *testing.T) {
		var mentioning []string
		for _, rel := range files {
			if strings.Contains(consoleBoundaryDoc(t, rel), consoleDirName+"/") {
				mentioning = append(mentioning, rel)
			}
		}
		// If nothing mentions a console/ path any more, the allow-list entry
		// has gone stale and should be dropped deliberately rather than left
		// as a permission nobody uses.
		if len(mentioning) == 0 {
			t.Errorf("no Go file in the module mentions a %s/ path: the allow-list entry %v is stale and should be removed deliberately (plan Task 0.3 a3)", consoleDirName, consoleBoundaryAllowList)
		}
		for _, rel := range mentioning {
			if rel == consoleBoundarySelf || slices.Contains(consoleBoundaryAllowList, rel) {
				continue
			}
			t.Errorf("%s mentions a %s/ path and is not on consoleBoundaryAllowList %v: the exception is a list in a test, not a habit, so a new reference is a deliberate edit with the case for it written down (plan Task 0.3 a3)", rel, consoleDirName, consoleBoundaryAllowList)
		}
	})

	t.Run("an_allow_listed_file_imports_no_console_path_and_embeds_nothing_from_it", func(t *testing.T) {
		for _, rel := range consoleBoundaryAllowList {
			if !slices.Contains(files, rel) {
				t.Errorf("consoleBoundaryAllowList names %q, which is not a .go file of this module: the walk did not see it", rel)
				continue
			}
			src := consoleBoundaryDoc(t, rel)
			imports, _ := consoleBoundaryImports(t, rel, src)
			for _, imp := range imports {
				if consoleBoundaryImportReachesConsole(imp, consolePkg) {
					t.Errorf("%s is allow-listed to mention a %s/ path and not to import one: it imports %q (plan Task 0.3 a3)", rel, consoleDirName, imp)
				}
			}
			for _, pattern := range consoleBoundaryEmbedPatterns(rel, src) {
				if consoleBoundaryEmbedPatternReachesConsole(pattern) {
					t.Errorf("%s is allow-listed to mention a %s/ path and not to embed one: it embeds %q, which puts a console/ file inside the test binary (plan Task 0.3 a3)", rel, consoleDirName, pattern)
				}
			}
		}
	})

	t.Run("the_readme_states_the_module_rule", func(t *testing.T) {
		readme := consoleBoundaryDoc(t, consoleDirName+"/README.md")
		for _, want := range []string{
			"importable by, or imports, the Go module",
			consoleBoundarySelf,
		} {
			if !strings.Contains(readme, want) {
				t.Errorf("%s/README.md does not state %q: the directory's rule is written where a person who opens the directory reads it (plan Task 0.3)", consoleDirName, want)
			}
		}
	})
}

// TestGoModHasNoConsoleDependency is (b) of Task 0.3: go.mod's require block is
// unchanged from the baseline recorded above, so the console cannot smuggle a
// Go dependency in, and go.sum names no module under console/.
func TestGoModHasNoConsoleDependency(t *testing.T) {
	root := consoleBoundaryRoot(t)
	consolePkg := consoleBoundaryModulePath(t, root) + "/" + consoleDirName
	require := consoleRequireLines(consoleBoundaryDoc(t, "go.mod"))
	want := slices.Clone(consoleGoModRequireBaseline)
	slices.Sort(want)

	if !slices.Equal(require, want) {
		for _, line := range require {
			if !slices.Contains(want, line) {
				t.Errorf("go.mod requires %q, which the baseline recorded in this file does not: a dependency arrived without a deliberate edit to consoleGoModRequireBaseline. If it is intended, add it there in the same commit and say why; if it names a path under %s/, it is the boundary itself (plan Task 0.3 b)", line, consoleDirName)
			}
		}
		for _, line := range want {
			if !slices.Contains(require, line) {
				t.Errorf("go.mod no longer requires %q, which consoleGoModRequireBaseline records: a removal is a deliberate edit to the baseline too (plan Task 0.3 b)", line)
			}
		}
	}

	t.Run("no_require_is_a_path_under_console", func(t *testing.T) {
		for _, line := range require {
			mod := strings.Fields(line)[0]
			if mod == consolePkg || strings.HasPrefix(mod, consolePkg+"/") {
				t.Errorf("go.mod requires the module %q: a Go module under %s/ is the boundary itself, and nothing under %s/ is importable by the Go module (design section 8.1 item 4, plan Task 0.3 a3)", mod, consoleDirName, consoleDirName)
			}
		}
	})

	t.Run("go_sum_names_no_console_module", func(t *testing.T) {
		gosum := consoleBoundaryDoc(t, "go.sum")
		lines := strings.Split(gosum, "\n")
		for i, line := range lines {
			if strings.TrimSpace(line) == "" {
				continue
			}
			// A go.sum line is "<module> <version> <hash>" (or ".../go.mod").
			mod, _, _ := strings.Cut(line, " ")
			if mod == consolePkg || strings.HasPrefix(mod, consolePkg+"/") {
				t.Errorf("go.sum:%d names the module %q: go.sum gains nothing from the console, and a module under %s/ is the boundary itself (plan Task 0.3 a2)", i+1, mod, consoleDirName)
			}
		}
		if len(lines) < 10 {
			t.Errorf("go.sum carries %d lines: the scan is looking at the wrong file", len(lines))
		}
	})
}

// TestNoGoFileEmbedsFromConsole is (a2) of Task 0.3: no file, test or
// otherwise, uses go:embed over a console/ path. Embedding one would put a
// console/ file inside a Go binary, which is a reference of exactly the kind
// the boundary forbids -- and unlike an import it needs no module and no
// package to succeed.
func TestNoGoFileEmbedsFromConsole(t *testing.T) {
	root := consoleBoundaryRoot(t)
	directives := 0
	for _, rel := range consoleBoundaryGoFiles(t, root) {
		for _, pattern := range consoleBoundaryEmbedPatterns(rel, consoleBoundaryDoc(t, rel)) {
			directives++
			if consoleBoundaryEmbedPatternReachesConsole(pattern) {
				t.Errorf("%s embeds %q with a go:embed directive: no file, test or otherwise, embeds from %s/ (design section 8.1 item 4, plan Task 0.3 a2)", rel, pattern, consoleDirName)
			}
		}
	}
	if directives == 0 {
		t.Errorf("no go:embed directive was found anywhere in the module, so this check proved nothing: the directive regexp or the walk is wrong")
	}
}
