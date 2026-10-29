package coordpath

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/budget"
)

// TestCoordpathPathsPinnedToTheirLiterals: the generated rules and the doctor
// check that reads their marker both write and read these strings, so a
// changed literal here moves the coordinator's whole subtree out from under
// the grant that authorises it (D195). The literals are pinned rather than
// derived, so the test fails on the change and not on the drift it causes.
func TestCoordpathPathsPinnedToTheirLiterals(t *testing.T) {
	if PathRoot != "coordinator" {
		t.Errorf("PathRoot = %q, want %q", PathRoot, "coordinator")
	}
	if PathProject != "coordinator/project" {
		t.Errorf("PathProject = %q, want %q", PathProject, "coordinator/project")
	}
	if PathRepos != "coordinator/repos" {
		t.Errorf("PathRepos = %q, want %q", PathRepos, "coordinator/repos")
	}
	slug := "owner/repo-a"
	want := "coordinator/repos/" + budget.Key(slug)
	if got := PathRepo(slug); got != want {
		t.Errorf("PathRepo(%q) = %q, want %q (the slug escaped through budget.Key, as the rules expect)", slug, got, want)
	}
}

// TestCoordpathDocumentPathsPinnedToTheirLiterals pins the two document paths:
// the project-wide one hangs off PathProject, the repository's off that
// repository's own node.
func TestCoordpathDocumentPathsPinnedToTheirLiterals(t *testing.T) {
	slug := "owner/repo-a"
	id := DocumentID("intent", "who", "what")
	if got, want := PathProjectDocument(id), PathProject+"/"+id; got != want {
		t.Errorf("PathProjectDocument(%q) = %q, want %q", id, got, want)
	}
	if got, want := PathRepoDocument(slug, id), "coordinator/repos/"+budget.Key(slug)+"/"+id; got != want {
		t.Errorf("PathRepoDocument(%q, %q) = %q, want %q", slug, id, got, want)
	}
}

// TestCoordpathDocumentIDLengthPrefixesItsParts is the property the ID's whole
// reason rests on: a document's key describes the thing it is about and
// nothing else, so two different descriptions of two different things must not
// collide. Without the length prefix the parts run together and ("a", "bc") and
// ("ab", "c") would be one document.
func TestCoordpathDocumentIDLengthPrefixesItsParts(t *testing.T) {
	if DocumentID("a", "bc") == DocumentID("ab", "c") {
		t.Error("DocumentID(\"a\", \"bc\") == DocumentID(\"ab\", \"c\"): the parts are not length-prefixed, so two different documents collide")
	}
	if DocumentID("a", "bc") != DocumentID("a", "bc") {
		t.Error("DocumentID is not deterministic: the same description twice is not one key")
	}
	if DocumentID("one") == DocumentID("one", "") {
		t.Error("DocumentID(\"one\") == DocumentID(\"one\", \"\"): a trailing empty part is not distinguished")
	}
}

// TestCoordpathDocumentIDIsOneHexSHA256: the answer has to be a Realtime
// Database key, so it is lower-case hex and not base64 — a base64 key can hold
// a slash or a plus, and a hex key holds neither (the same reason budget.Key
// exists).
func TestCoordpathDocumentIDIsOneHexSHA256(t *testing.T) {
	id := DocumentID("project", "view")
	if len(id) != 64 {
		t.Fatalf("DocumentID = %q (%d chars), want 64: the hex of one SHA-256", id, len(id))
	}
	if strings.ToLower(id) != id {
		t.Errorf("DocumentID = %q, want lower-case hex", id)
	}
	for _, r := range id {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Fatalf("DocumentID = %q holds %q, which is not lower-case hex", id, r)
		}
	}
}

// coordpathDecls is the source of each named declaration in path, as
// go/printer renders it: layout and comments do not survive, so the two
// copies of a name compare equal exactly when they declare the same thing.
func coordpathDecls(t *testing.T, path string, names []string) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	out := map[string]string{}
	record := func(name string, node ast.Node) {
		if !slices.Contains(names, name) {
			return
		}
		var buf bytes.Buffer
		if err := printer.Fprint(&buf, fset, node); err != nil {
			t.Fatalf("rendering %s of %s: %v", name, path, err)
		}
		out[name] = buf.String()
	}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.GenDecl:
			for _, s := range d.Specs {
				vs, ok := s.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, n := range vs.Names {
					record(n.Name, vs)
				}
			}
		case *ast.FuncDecl:
			record(d.Name.Name, d)
		}
	}
	return out
}

// TestCoordpathIsTheOnlyCopy: the paths of the coordinator's subtree are
// declared here and nowhere else in the repository. Until E2.1b the library
// carried its own copies of all seven names, and two definitions of one subtree
// is exactly the drift this package exists to end (D195): a second declaration
// anywhere is a copy that can disagree with the one the rules grant a read on,
// and the library's own tests would never see it. The walk skips this package,
// which holds the one definition, and every directory that holds no code.
//
// What it refuses is the shape a copy comes back in: a top-level declaration of
// one of the seven names that spells the subtree — one built on PathRoot, or on
// the subtree's own name written as a literal. The seven names alone are not the
// subtree's: internal/budget declares a PathProject of its own and
// internal/projectpolicy a PathRepo and a PathRepos, each of a different
// subtree, and none of them is a copy of this one. A use of the names is not a
// declaration at all and is everywhere.
func TestCoordpathIsTheOnlyCopy(t *testing.T) {
	names := []string{
		"PathRoot", "PathProject", "PathRepos",
		"PathRepo", "PathProjectDocument", "PathRepoDocument", "DocumentID",
	}
	literal := regexp.MustCompile(`\bPathRoot\s*=\s*"coordinator"`)
	err := filepath.WalkDir("../..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel("../..", path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			if rel == "internal/coordpath" || d.Name() == "testdata" ||
				d.Name() == ".git" || d.Name() == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") {
			return nil
		}
		for name, decl := range coordpathDecls(t, path, names) {
			if !strings.Contains(decl, "coordinator") && !strings.Contains(decl, "PathRoot") {
				continue
			}
			t.Errorf("%s declares %s of the coordinator's subtree: it has one definition, here, and a second is the copy the rules cannot see (%s)", rel, name, decl)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if m := literal.FindString(string(data)); m != "" {
			t.Errorf("%s writes the subtree's own name as a literal (%q): it is PathRoot's, and the rules grant a read on the value, not on a copy of it", rel, m)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
