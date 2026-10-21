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
// The mention half of (a3) is a heuristic, and the file says which half of it
// is which. Its floor is the plan's literal rule: a file whose text holds the
// string "console/" is a mention, wherever that string appears, which is
// deliberately loose -- a string like "github.com/x/console/y" is reported too,
// and the answer is an allow-list entry or a reworded string, made on purpose.
// Above that floor sits a segment-aware detector for the spellings the literal
// rule cannot see, because no single literal holds "console/": a "console" path
// SEGMENT (the word between separators, not a substring of a longer word) that
// sits next to a "docs" segment or is written as a directory, including inside
// a filepath.Join/path.Join argument list -- so Join("..", "..", "console",
// "docs", "json-contract.md") is a mention and Join(tmp, "consolekit", "x") is
// not -- and a RELATIVE path that ends at the directory, "../../console", which
// is the bare reference to console/ itself and holds no "console/" for the
// floor. The detector does not follow a path assembled from variables, and it
// cannot prove that a "console" segment is THIS repository's console; both
// limits err toward the floor, which is the plan's rule and stays in force.
//
// This file is the scanner, so the mention half of (a3) exempts it by name: it
// necessarily holds the literal "console/" itself (in the checks, in their
// messages, and in the exemption), and a test cannot list itself without naming
// itself. It is NOT exempt from (a1) or (a2): the walk covers every Go file in
// the module including this one, so this file's own imports and its own embed
// directives are checked like any other file's.
//
// The walk reads every .go file of the module except two kinds of directory,
// and it is worth being exact about which is which. A name beginning with "."
// or "_" is ignored by the go tool in every version, so those are stepped out
// of -- that is what keeps a nested checkout under .worktrees/ or
// .claude/worktrees/, a second copy of this module and of the console work in
// flight, from failing THIS module's boundary. A directory go.mod's `ignore`
// directive names is stepped out of as well, because the module itself says so.
//
// node_modules is neither, unless go.mod says so: it is not magic to the go
// tool, and in this repository `go list ./...` prints its packages as this
// module's. Skipping it anyway was a real hole -- a planted
// console/node_modules/y/y.go failed no test at all, while console/README.md
// promised it would -- so it is read like any other directory, through the walk
// and through the console-subtree check. Two directories are deliberately
// walked in every case: console/ itself (a Go file there is exactly what (a3)
// forbids) and testdata/ (a reference in a file the build ignores is still a
// reference this contract forbids). A read error is returned rather than
// skipped, so a file that disappears mid-scan fails the test instead of quietly
// shortening the scan.

import (
	"go/ast"
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
	// The second entry, added by W2.1 and argued for here, as the test below
	// asks. The census of every file that still names the retired `init` walks
	// the live roots, and console/ is one of them (its README and one unit test
	// name the command as the retired verb, which is retirement text and not a
	// rename): the walk's list of those files is a list of PATHS, so
	// docs_live_init_census_test.go has to write two console/ paths down. It
	// imports nothing from console/, reads no console/ file, embeds nothing and
	// is a census of the Go tree's own rename -- a mention of a path, which is
	// what this boundary is about only when it is a way in.
	"internal/cli/docs_live_init_census_test.go",
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
//
// The one entry that rule has already been applied to: `spf13/pflag` was
// promoted from the indirect block to the direct one by `next`'s Task 1.6 work
// (internal/cli and plugin test files import it directly, so the direct require
// is what the toolchain asks for), which is why its line here carries no
// "// indirect" comment. The promotion is next's, not the console's: the
// console still contributes nothing to this module.
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
	"github.com/spf13/pflag v1.0.9",
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
	"golang.org/x/text v0.42.0 // the artifact-name collision rule normalizes and FULL folds with it",
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

// consoleBoundaryIgnoredDirs returns the module-root-relative directories
// go.mod's `ignore` directives exclude from the module (a Go 1.24 directive),
// slash-separated and with any "./" prefix removed.
//
// It is read from the module rather than assumed, because node_modules is NOT
// ignored by the go command on its own: it is an ordinary directory unless
// go.mod says otherwise, and in this repository it says nothing of the kind --
// `go list ./...` prints github.com/dimipaun/fugaro/console/node_modules/... as
// a package of this module. Treating node_modules as invisible "because the go
// command ignores it" would leave a real hole in the walk, and did: a planted
// console/node_modules/y/y.go failed no test at all, while console/README.md
// promised it would. Only a name beginning with "." or "_" is ignored by the go
// tool in every version and needs no directive.
func consoleBoundaryIgnoredDirs(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(readDoc(t, "go.mod"), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "ignore ")
		if !ok {
			continue
		}
		if p := strings.TrimPrefix(strings.TrimSpace(rest), "./"); p != "" && p != "." {
			out = append(out, p)
		}
	}
	return out
}

// consoleBoundarySkipDir reports whether the walk steps out of a directory
// rather than into it: a name beginning with "." or "_" (ignored by the go tool
// everywhere -- .git/ and a nested checkout under .worktrees/ or
// .claude/worktrees/ are both that), or a root-relative path go.mod's `ignore`
// directive excludes from the module. node_modules is in neither list unless
// this module's go.mod declares it, so today its .go files are read like any
// other directory's. testdata/ is read on purpose: a reference in a file the
// build ignores is still a reference this contract forbids.
func consoleBoundarySkipDir(name, rel string, ignored []string) bool {
	return strings.HasPrefix(name, ".") ||
		strings.HasPrefix(name, "_") ||
		slices.Contains(ignored, rel)
}

// consoleBoundaryGoFiles is every .go file in the module, relative to the root,
// slash-separated and sorted. It asserts the walk found a module, so no check
// below can pass by looking at nothing.
func consoleBoundaryGoFiles(t *testing.T, root string) []string {
	t.Helper()
	files, err := consoleBoundaryWalk(root, consoleBoundaryIgnoredDirs(t))
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

// consoleBoundaryWalk is consoleBoundaryGoFiles without the "did it find the
// module" guard, so a test can point it at a small tree it built itself, and
// with the directories to step out of passed in rather than read from go.mod.
// An error is returned, never swallowed: a file that cannot be read fails the
// test rather than leaving the scan short.
func consoleBoundaryWalk(root string, ignored []string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p == root {
				return nil
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			if consoleBoundarySkipDir(d.Name(), filepath.ToSlash(rel), ignored) {
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
		return nil, err
	}
	return files, nil
}

// consoleBoundaryModulePath is go.mod's module path, which is what makes an
// import path checkable: "under console/" means "<module>/console" or below.
func consoleBoundaryModulePath(t *testing.T, root string) string {
	t.Helper()
	gomod := readDoc(t, "go.mod")
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
// module root's console directory. An import path is a module path, so the first
// case is the boundary directly; the second covers a relative import, which
// cannot compile in a module but is the same reference spelled differently --
// the boundary is about what a file says. A relative path is resolved against
// the importing file's own directory, so "../../console" from internal/cli
// reaches the root console/ and is caught, while a path that lands on some other
// directory named console is a different directory and is not this boundary.
func consoleBoundaryImportReachesConsole(fileRel, imp, consolePkg string) bool {
	switch {
	case imp == consolePkg, strings.HasPrefix(imp, consolePkg+"/"):
		return true
	case strings.HasPrefix(imp, "./"), strings.HasPrefix(imp, "../"):
		return consoleBoundaryResolvedIntoConsole(fileRel, imp)
	}
	return false
}

// consoleBoundaryResolvedIntoConsole resolves a slash-separated path written
// inside the file at fileRel (both relative to the module root) and reports
// whether it lands in the root console/ directory. This is the one place the
// "which console/ is this?" question is answered, so an import and an embed
// pattern cannot disagree about it.
func consoleBoundaryResolvedIntoConsole(fileRel, p string) bool {
	resolved := path.Join(path.Dir(fileRel), path.Clean(p))
	return resolved == consoleDirName || strings.HasPrefix(resolved, consoleDirName+"/")
}

// consoleBoundaryMention reports whether a Go file mentions a path under the
// module root's console directory -- the (a3) question, asked of one file.
//
// Two detections, and the difference between them is the point. The floor is
// the plan's literal rule: the string "console/" anywhere in the file counts,
// wherever it appears and whatever it is part of. It is deliberately loose. A
// string like "github.com/x/console/y" is therefore a mention, and a file that
// has one either joins the allow-list on purpose or words the string
// differently on purpose; neither is a decision this test makes for the author.
//
// The detector above the floor exists because some files name the console
// without ever writing "console/" in one literal -- filepath.Join("..", "..",
// "console", "docs", "x") is the same path spelled in pieces -- so the file is
// parsed and read as paths: see consoleBoundaryPathMentionsConsole for what a
// "console" segment has to be to count, and consoleBoundaryJoinMentionsConsole
// for the argument-list half. A file the parser refuses is read as runs of
// quoted literals instead, since a broken fixture is exactly the kind of file
// that would otherwise carry a reference past every check.
func consoleBoundaryMention(file, src string) bool {
	if strings.Contains(src, consoleDirName+"/") {
		return true
	}
	f, err := parser.ParseFile(token.NewFileSet(), file, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		// No AST to read, so the quoted literals are read as they are written:
		// each one on its own, and each comma-separated run as the list of path
		// segments it is in the Join spelling. Both are asked the same question
		// the parsed path asks.
		literals := consoleStringLitRE.FindAllString(src, -1)
		for _, q := range literals {
			if s, err := strconv.Unquote(q); err == nil && consoleBoundaryPathMentionsConsole(s) {
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
			if len(parts) > 1 && consoleBoundaryPathMentionsConsole(strings.Join(parts, "/")) {
				return true
			}
		}
		return false
	}
	mention := false
	ast.Inspect(f, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.BasicLit:
			if v.Kind == token.STRING {
				if s, err := strconv.Unquote(v.Value); err == nil && consoleBoundaryPathMentionsConsole(s) {
					mention = true
				}
			}
		case *ast.CallExpr:
			if consoleBoundaryJoinCall(v) && consoleBoundaryJoinMentionsConsole(v.Args) {
				mention = true
			}
		}
		return true
	})
	return mention
}

// consoleBoundaryPathMentionsConsole reports whether a path-shaped string names
// the module's console directory. What counts is a "console" SEGMENT -- the
// word between separators, so "consolekit" and "console.md" are other names --
// that either sits in a relative path ("." or ".." first, which can only mean a
// path built from this module), or is followed by a "docs" segment, which is
// how the console's own paths are written, or is followed by a separator, which
// is a reference to the directory itself. It answers "does this string look
// like a console path", not
// "is this string the path of THIS repository's console": a string from another
// tree with the same shape counts, and the literal floor above counts more
// still, which is the direction the plan's rule asks for.
func consoleBoundaryPathMentionsConsole(s string) bool {
	segs := strings.Split(s, "/")
	// Relative to where the file sits: "." and ".." can only mean a path built
	// from this module, so a "console" segment anywhere in one is this
	// boundary. It is what catches the bare directory reference "../../console"
	// -- no trailing separator for the first case below, no "docs" beside it,
	// and nothing else in this file sees it: it is not an import, not an embed
	// pattern, and the literal floor looks for "console/", which it does not
	// hold. A module path ending in "/console" (github.com/x/console) is
	// somebody else's directory and is deliberately not this case.
	relative := len(segs) > 1 && (segs[0] == "." || segs[0] == "..")
	for i, seg := range segs {
		if seg != consoleDirName {
			continue
		}
		// console/ itself: followed by "docs", or by the separator alone.
		if i+1 < len(segs) && (segs[i+1] == "docs" || segs[i+1] == "") {
			return true
		}
		if relative {
			return true
		}
	}
	return false
}

// consoleBoundaryJoinCall reports whether a call is a filepath.Join / path.Join
// -- the two functions whose ARGUMENT LIST is one path, and so the one place a
// "console/" spelling can hide from a per-literal scan. strings.Join is not one
// of them: its arguments are elements, not path segments, and treating them as
// a path is how a detector starts reporting nonsense. The check is on the name
// as written, so a renamed import (p "path"; p.Join(...)) is not seen -- a limit
// on the safe side, since the literal floor and the per-literal read still
// apply, and a helper that imported path under another name would be a
// deliberate obfuscation rather than an accident.
func consoleBoundaryJoinCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Join" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && (pkg.Name == "path" || pkg.Name == "filepath")
}

// consoleBoundaryJoinMentionsConsole reports whether a Join call builds a path
// through the console directory. Each literal argument is read as a path
// segment: "console" alone (Join(root, "console") is the directory) or a
// path-shaped literal (Join("..", "..", "console", "docs", "x")) is a mention.
// An argument that is a variable or another call is not followed -- this is a
// detector for spellings, not an evaluator, and a path built entirely from
// variables is the case the classifier cannot see.
func consoleBoundaryJoinMentionsConsole(args []ast.Expr) bool {
	if len(args) < 2 {
		return false
	}
	for _, a := range args {
		lit, ok := a.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			continue
		}
		s, err := strconv.Unquote(lit.Value)
		if err != nil {
			continue
		}
		if s == consoleDirName || consoleBoundaryPathMentionsConsole(s) {
			return true
		}
	}
	return false
}

// consoleStringLitRE finds a quoted string in the raw text of a file the parser
// refuses, and consoleStringRunRE finds a run of two or more of them separated
// by commas -- the text-level shape of a Join call's argument list, which is
// the one spelling the literal floor cannot see.
var (
	consoleStringLitRE = regexp.MustCompile(`"[^"\n]*"`)
	consoleStringRunRE = regexp.MustCompile(`"[^"\n]*"(?:\s*,\s*"[^"\n]*")+`)
)

// consoleRelativeImportRE finds a quoted relative import path, in the raw text
// of a file the parser refuses. Such a file gives the import check no AST to
// read, and "../../console" carries no "console/" substring for the mention
// scan either, so this is the one shape that would otherwise slip past both.
var consoleRelativeImportRE = regexp.MustCompile(`"(\.{1,2}/[^"]*)"`)

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

// consoleBoundaryEmbedPatternReachesConsole reports whether a go:embed pattern
// names a file under the module root's console directory. A pattern is a
// slash-separated path relative to the PACKAGE DIRECTORY, so the resolution is
// what decides, exactly as it does for a relative import: "console/docs/x.md"
// reaches the root console/ from the root package and from nowhere else,
// "../../console/docs/x.md" reaches it from internal/cli, and another package's
// "testdata/console/x.md" does not -- a nested "/console/" is a different
// directory and is not this boundary. The all: prefix is Go's "walk this
// directory too" marker rather than part of the path.
func consoleBoundaryEmbedPatternReachesConsole(fileRel, pattern string) bool {
	return consoleBoundaryResolvedIntoConsole(fileRel, strings.TrimPrefix(pattern, "all:"))
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
			src := readDoc(t, rel)
			imports, parsed := consoleBoundaryImports(t, rel, src)
			if !parsed {
				// A file the parser refuses is not compiled either, so it
				// cannot build against console/; it can still say so, and
				// the text is what catches it -- both spellings, since a
				// relative path reaching the root console/ has no
				// "console/" substring for the mention scan either.
				if strings.Contains(src, consolePkg) {
					t.Errorf("%s names the import path %s and does not parse: the Go module and console/ never touch (design section 8.1 item 4)", rel, consolePkg)
				}
				for _, m := range consoleRelativeImportRE.FindAllStringSubmatch(src, -1) {
					if consoleBoundaryImportReachesConsole(rel, m[1], consolePkg) {
						t.Errorf("%s imports %q and does not parse: a relative import of %s/ is the same boundary (plan Task 0.3 a1)", rel, m[1], consoleDirName)
					}
				}
				continue
			}
			for _, imp := range imports {
				if consoleBoundaryImportReachesConsole(rel, imp, consolePkg) {
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
		// Every file below console/ is read, node_modules included. Nothing is
		// stepped over here: this check is about what console/ HOLDS, and a
		// .go file dropped into console/node_modules/ is a .go file in the
		// console directory. (The module-wide walk follows go.mod's `ignore`
		// directives instead, because that one is about what the go command
		// builds; the two questions have different answers and both are asked.)
		var offenders []string
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
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
		if len(consoleBoundaryAllowList) != 2 {
			t.Errorf("consoleBoundaryAllowList carries %d entries, %v: it is a TWO-entry exception -- the console contract's docs test, which reads console/docs/json-contract.md as bytes, and the live-init census, which writes two console/ paths down as the files its walk found naming the retired `init` (W2.1, argued at the entry). A third entry is a deliberate widening of the boundary and must be argued for in the commit that adds it (plan Task 0.3 a3)", len(consoleBoundaryAllowList), consoleBoundaryAllowList)
		}
	})

	t.Run("only_the_allow_list_may_mention_a_console_path", func(t *testing.T) {
		var mentioning []string
		for _, rel := range files {
			if consoleBoundaryMention(rel, readDoc(t, rel)) {
				mentioning = append(mentioning, rel)
			}
		}
		for _, rel := range mentioning {
			if rel == consoleBoundarySelf || slices.Contains(consoleBoundaryAllowList, rel) {
				continue
			}
			t.Errorf("%s mentions a %s/ path and is not on consoleBoundaryAllowList %v: the exception is a list in a test, not a habit, so a new reference is a deliberate edit with the case for it written down (plan Task 0.3 a3)", rel, consoleDirName, consoleBoundaryAllowList)
		}
		// The other direction, per entry: an entry exists because its file
		// reads a console/ document, so an entry whose file no longer mentions
		// one is stale and should be removed deliberately. It is checked per
		// entry rather than as "does anything mention a console/ path", because
		// this scanner mentions one by necessity and a module-wide count could
		// therefore never fall to zero.
		for _, rel := range consoleBoundaryAllowList {
			if !slices.Contains(mentioning, rel) {
				t.Errorf("%s is on consoleBoundaryAllowList but mentions no %s/ path any more: the exception has gone stale and should be removed deliberately, with the commit that stops the file reading the contract (plan Task 0.3 a3)", rel, consoleDirName)
			}
		}
	})

	t.Run("an_allow_listed_file_imports_no_console_path_and_embeds_nothing_from_it", func(t *testing.T) {
		for _, rel := range consoleBoundaryAllowList {
			if !slices.Contains(files, rel) {
				t.Errorf("consoleBoundaryAllowList names %q, which is not a .go file of this module: the walk did not see it", rel)
				continue
			}
			src := readDoc(t, rel)
			imports, _ := consoleBoundaryImports(t, rel, src)
			for _, imp := range imports {
				if consoleBoundaryImportReachesConsole(rel, imp, consolePkg) {
					t.Errorf("%s is allow-listed to mention a %s/ path and not to import one: it imports %q (plan Task 0.3 a3)", rel, consoleDirName, imp)
				}
			}
			for _, pattern := range consoleBoundaryEmbedPatterns(rel, src) {
				if consoleBoundaryEmbedPatternReachesConsole(rel, pattern) {
					t.Errorf("%s is allow-listed to mention a %s/ path and not to embed one: it embeds %q, which puts a console/ file inside the test binary (plan Task 0.3 a3)", rel, consoleDirName, pattern)
				}
			}
		}
	})

	t.Run("the_readme_states_the_module_rule", func(t *testing.T) {
		readme := readDoc(t, consoleDirName+"/README.md")
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
	require := consoleRequireLines(readDoc(t, "go.mod"))
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
		gosum := readDoc(t, "go.sum")
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
	// The proof that this check can see anything is the extractor's own, not a
	// count of what the module happens to hold today. A module with no go:embed
	// directive at all is a legitimate state -- the last one can be deleted on
	// purpose -- and a check that failed on it would be asking the author to
	// break the test to make it pass. So the extractor is shown a directive it
	// must find, and the scan below is free to find none.
	const probe = "package probe\n\nimport _ \"embed\"\n\n//go:embed testdata/zzz/known.md\nvar zzz string\n"
	if got := consoleBoundaryEmbedPatterns("probe.go", probe); !slices.Equal(got, []string{"testdata/zzz/known.md"}) {
		t.Fatalf("the go:embed extractor read %q out of a known source, so a scan finding nothing proves nothing: fix the directive regexp before trusting this test", got)
	}
	for _, rel := range consoleBoundaryGoFiles(t, root) {
		for _, pattern := range consoleBoundaryEmbedPatterns(rel, readDoc(t, rel)) {
			if consoleBoundaryEmbedPatternReachesConsole(rel, pattern) {
				t.Errorf("%s embeds %q with a go:embed directive: no file, test or otherwise, embeds from %s/ (design section 8.1 item 4, plan Task 0.3 a2)", rel, pattern, consoleDirName)
			}
		}
	}
}

// TestConsoleBoundaryWalkSkipsWhatTheGoToolSkips holds the walk's rule, both
// halves of it.
//
// First: a name beginning with "." or "_" is stepped out of. A nested checkout
// under .worktrees/ or .claude/worktrees/ is a whole second copy of this
// module, and it must not fail this one's boundary for reasons that have
// nothing to do with this repository's own files.
//
// Second: node_modules is NOT stepped out of, unless go.mod's `ignore`
// directive names it, because the go tool descends into it otherwise -- in this
// repository `go list ./...` prints its packages as this module's. That half
// was learned the hard way: a planted console/node_modules/y/y.go failed no
// test at all, while console/README.md promised it would.
//
// A visible copy of the very same file still counts, which is what keeps the
// skips from turning into a hole: the file below says
// "console/docs/json-contract.md" and only its directory decides whether that
// is this module's business.
//
// The tree is built under t.TempDir() and passed to consoleBoundaryWalk
// directly, so the module's own walk is the only thing exercised and the
// repository is never written to.
func TestConsoleBoundaryWalkSkipsWhatTheGoToolSkips(t *testing.T) {
	root := t.TempDir()
	const body = "package probe\n\nvar zzzConsoleDoc = \"console/docs/json-contract.md\"\n"
	if !consoleBoundaryMention("probe.go", body) {
		t.Fatalf("the planted body is not recognised as a %s/ mention, so this test would prove nothing about the walk", consoleDirName)
	}
	plant := func(rel string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const visible = "internal/visible/probe.go"
	const modules = "internal/node_modules/dep/probe.go"
	plant(visible)
	plant(modules)
	ignored := []string{
		".worktrees/wt/internal/cli/probe.go",
		".claude/worktrees/wt/internal/cli/probe.go",
		"_scratch/internal/cli/probe.go",
	}
	for _, rel := range ignored {
		plant(rel)
	}

	// With no `ignore` directive, node_modules is read like any other
	// directory: the go tool treats its packages as this module's.
	files, err := consoleBoundaryWalk(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(files, visible) {
		t.Errorf("the walk did not read %s, a visible .go file mentioning a %s/ path: the skip is too wide and the boundary would have a hole in it (%v)", visible, consoleDirName, files)
	}
	if !slices.Contains(files, modules) {
		t.Errorf("the walk did not read %s: node_modules is only ignored when go.mod says `ignore ./node_modules`, and this walk was told nothing was ignored (%v)", modules, files)
	}
	for _, rel := range ignored {
		if slices.Contains(files, rel) {
			t.Errorf("the walk read %s: a name beginning with \".\" or \"_\" is ignored by the go tool everywhere, and a nested checkout of this module must not fail this boundary's checks (plan Task 0.3 a3)", rel)
		}
	}

	// With `ignore ./internal/node_modules` declared, the walk follows the
	// module and steps out of it -- the same file, the opposite answer, which
	// is the whole of the difference between the two halves.
	files, err = consoleBoundaryWalk(root, []string{"internal/node_modules"})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(files, modules) {
		t.Errorf("the walk read %s although it was told go.mod ignores that directory (%v)", modules, files)
	}
	if !slices.Contains(files, visible) {
		t.Errorf("an `ignore` directive took the walk past %s as well: ignoring one directory must not stop the scan (%v)", visible, files)
	}

	// A walk that cannot read what it was pointed at returns the error: a
	// silent empty result is how a scan passes by looking at nothing.
	if _, err := consoleBoundaryWalk(filepath.Join(root, "no-such-directory"), nil); err == nil {
		t.Error("consoleBoundaryWalk on a path that does not exist returned no error: a read failure must fail loudly, not shorten the scan")
	}
}

// TestConsoleBoundaryMentionDetectsConsoleSpellings holds the (a3) mention
// detector: the plan's literal rule is the floor, and above it a "console" path
// segment next to "docs" -- or written as a directory -- is a mention even when
// no single literal holds "console/", which is the filepath.Join spelling. The
// false cases are what keeps the floor from being lowered into a word match:
// "consolekit" and "console.md" are other names, and a Join list of unrelated
// segments is not a path through the console.
func TestConsoleBoundaryMentionDetectsConsoleSpellings(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want bool
	}{
		{"the plan's literal rule (the floor)", `package p

var zzz = "../../console/docs/json-contract.md"`, true},
		{"a console directory reference", `package p

var zzz = "../../console/"`, true},
		{"the same reference with no trailing separator", `package p

var zzz = "../../console"`, true},
		{"a Join into the directory, no docs segment anywhere", `package p

import "path/filepath"

func zzz() ([]byte, error) { return os.ReadFile(filepath.Join("..", "..", "console", "README.md")) }`, true},
		{"filepath.Join spelled in pieces", `package p

import "path/filepath"

var zzz = filepath.Join("..", "..", "console", "docs", "json-contract.md")`, true},
		{"path.Join spelled in pieces", `package p

import "path"

var zzz = path.Join("..", "console", "docs")`, true},
		{"a Join through the directory itself", `package p

import "path/filepath"

func zzz(root string) string { return filepath.Join(root, "console") }`, true},
		{"a longer word that merely starts with console", `package p

var zzz = "github.com/x/consolekit/y"`, false},
		{"console as part of another file name", `package p

var zzz = "docs/console.md"`, false},
		{"another module's console directory", `package p

var zzz = "github.com/x/console"`, false},
		{"a Join list with no console segment", `package p

import "path/filepath"

var zzz = filepath.Join("..", "..", "internal", "docs")`, false},
		{"a string.Join is not a path", `package p

import "strings"

var zzz = strings.Join([]string{"console", "docs"}, "/")`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := consoleBoundaryMention("probe.go", tc.src); got != tc.want {
				t.Errorf("consoleBoundaryMention(%q) = %v, want %v", tc.src, got, tc.want)
			}
		})
	}

	// A file the parser refuses is read as quoted strings, so a broken fixture
	// cannot carry a spelling past the scan -- in pieces, or as one literal.
	for _, tc := range []struct {
		name, src string
	}{
		{"in pieces", "package p\n\nvar zzz = filepath.Join(\"..\", \"..\", \"console\", \"docs\"\n"},
		{"as a bare relative path", "package p\n\nvar zzz = \"../../console\"\n"},
	} {
		t.Run("a file that does not parse, "+tc.name, func(t *testing.T) {
			if !consoleBoundaryMention("broken.go", tc.src) {
				t.Errorf("a file that does not parse, and holds the %s path %s, was not seen as a mention", consoleDirName, tc.name)
			}
		})
	}
}
