package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// TestEditionIsCoreUntagged: a build without the ee tag is core, which is the
// whole of the marker's default — the assembly file the tag compiles in is what
// flips it, and nothing else can (design docs/design/ee-boundary-moves.md §7).
// The setter refuses a second call, so a second registration cannot quietly
// halve what the first one set (design §5.5).
//
// The marker is process-wide, so this test puts it back: every later test in
// this package renders and judges under the edition it leaves behind.
func TestEditionIsCoreUntagged(t *testing.T) {
	if got := Edition(); got != EditionCore {
		t.Fatalf("Edition() = %q, want %q: a build without the ee tag is core", got, EditionCore)
	}
	old := editionEE
	t.Cleanup(func() { editionEE = old })
	if err := SetEditionEE(); err != nil {
		t.Fatalf("SetEditionEE: %v", err)
	}
	if got := Edition(); got != EditionEE {
		t.Fatalf("Edition() = %q after the ee build registered itself, want %q", got, EditionEE)
	}
	if err := SetEditionEE(); err == nil {
		t.Error("a second SetEditionEE was accepted")
	}
}

// TestEditionHasNoLdflagsRoute: the edition is kept in no package-level string
// and no build file passes -X for it, so no build flag can raise a core build
// to ee — silencing the warning a core job image is supposed to get — or lower
// a tagged build to core, making the binary with the enterprise code in it deny
// having it (design §7, "Why -ldflags -X cannot flip it").
//
// The proof is the two scanners below on planted violations: neither exists in
// the tree, and a scan that only ever runs over a clean tree would pass while
// catching nothing. `-X` sets package-level string variables and nothing else,
// which is exactly what the first scanner looks for.
func TestEditionHasNoLdflagsRoute(t *testing.T) {
	src, err := os.ReadFile("edition.go")
	if err != nil {
		t.Fatal(err)
	}
	if vars := editionStringVars(t, src); len(vars) > 0 {
		t.Errorf("edition.go declares package-level string variables %v: -ldflags -X can set those", vars)
	}
	root := testutil.ModuleRoot()
	files := editionBuildFiles(t, root)
	if len(files) == 0 {
		t.Fatalf("no build file found under %s: the scan read nothing", root)
	}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if xs := ldflagsEditionTargets(string(data)); len(xs) > 0 {
			rel, _ := filepath.Rel(root, path)
			t.Errorf("%s passes -X for the edition: %v", rel, xs)
		}
	}

	// The planted violations: the two routes this scan exists to catch, and the
	// only ones a build could take.
	plantedVar := []byte("package cli\n\n// The edition, as a build flag would set it.\nvar Edition = \"core\"\n")
	if vars := editionStringVars(t, plantedVar); !slices.Equal(vars, []string{"Edition"}) {
		t.Errorf("the scan read a package-level string variable %v, want [Edition]", vars)
	}
	for _, line := range []string{
		"      -ldflags \"-X github.com/dimipaun/fugaro/internal/cli.Edition=ee\" \\",
		"-X github.com/dimipaun/fugaro/internal/cli.edition=ee",
	} {
		if xs := ldflagsEditionTargets(line); len(xs) == 0 {
			t.Errorf("the scan read no -X in %q", line)
		}
	}
	// And the -X the tree does carry is not one of them: Version and Commit are
	// package-level strings on purpose (root.go, images/base/Dockerfile).
	if xs := ldflagsEditionTargets("-X github.com/dimipaun/fugaro/internal/cli.Version=0.13.0 -X github.com/dimipaun/fugaro/internal/cli.Commit=abc"); len(xs) != 0 {
		t.Errorf("the scan read %v in a Version/Commit -X line", xs)
	}
}

// editionStringVars are the package-level string variables src declares: the
// declarations -ldflags -X can write, named by the variable's own name. A
// const is not one of them — -X writes variables — and neither is a variable of
// another type, which -X refuses at link time.
func editionStringVars(t *testing.T, src []byte) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "edition.go", src, 0)
	if err != nil {
		t.Fatalf("parsing the source: %v", err)
	}
	var out []string
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			isString := false
			if id, ok := vs.Type.(*ast.Ident); ok && id.Name == "string" {
				isString = true
			}
			for _, v := range vs.Values {
				if lit, ok := v.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					isString = true
				}
			}
			if !isString {
				continue
			}
			for _, name := range vs.Names {
				out = append(out, name.Name)
			}
		}
	}
	return out
}

// editionXRE matches a linker -X whose target names the edition: -X takes a
// package path and a variable, so the variable is the target's last element and
// the whole target is one word. The match is deliberately loose (any target
// containing "edition"), because a route the scan does not recognise is a route
// it does not close.
var editionXRE = regexp.MustCompile(`-X\s+[^\s"']*[Ee]dition`)

// ldflagsEditionTargets are the -X targets in a build file that name the
// edition.
func ldflagsEditionTargets(src string) []string { return editionXRE.FindAllString(src, -1) }

// editionBuildFiles are the files that build a fugaro binary, and so the files
// a -X for the edition could hide in: every Dockerfile and Cloud Build file in
// the tree (the images are built by both), .goreleaser.yaml (the release), and
// every shell script, which is the other place a build command can be written.
// node_modules and .git hold no build of this module's binary.
func editionBuildFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules":
				return fs.SkipDir
			}
			return nil
		}
		name := d.Name()
		switch {
		case strings.HasPrefix(name, "Dockerfile"), strings.HasPrefix(name, "cloudbuild"),
			strings.HasPrefix(name, ".goreleaser"), strings.HasSuffix(name, ".sh"):
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}
