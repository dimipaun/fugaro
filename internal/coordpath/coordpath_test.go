package coordpath

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
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

// TestCoordpathMatchesTheLibrarysCopy: until E2.1b moves the library, the
// subtree has two definitions — the library's, which writes the documents, and
// this package's, which the rules grant and the doctor reads — and nothing in
// the core otherwise holds them together: the library's own state_test.go
// asserts how the names relate to each other, never what they are. A change to
// either copy of either value fails here, and the IDs too, since DocumentID's
// whole body is compared and not merely its shape. Once the library has moved
// out of internal/coordinator there is nothing left to compare against, which
// is the state this test is for.
func TestCoordpathMatchesTheLibrarysCopy(t *testing.T) {
	const library = "../coordinator/state.go"
	if _, err := os.Stat(library); errors.Is(err, fs.ErrNotExist) {
		return
	} else if err != nil {
		t.Fatal(err)
	}
	names := []string{
		"PathRoot", "PathProject", "PathRepos",
		"PathRepo", "PathProjectDocument", "PathRepoDocument", "DocumentID",
	}
	ours := coordpathDecls(t, "coordpath.go", names)
	theirs := coordpathDecls(t, library, names)
	for _, name := range names {
		if ours[name] == "" {
			t.Fatalf("this package no longer declares %s", name)
		}
		if theirs[name] == "" {
			t.Errorf("the library no longer declares %s: the two copies are what keeps the subtree that is written and the subtree that is granted the same one", name)
			continue
		}
		if ours[name] != theirs[name] {
			t.Errorf("%s is declared twice and differently:\n  coordpath: %s\n  the library: %s", name, ours[name], theirs[name])
		}
	}
}
