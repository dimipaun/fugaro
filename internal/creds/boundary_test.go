package creds

// The import boundary around the credential state (credentials-service plan
// Convention 11; O1's ruling that creds/ is denied to every run token).
//
// What keeps a run out of the cascade's state is the DATABASE, and this file is
// not that. A run token holds no permission on creds/ at all: the rules deny the
// whole subtree to every run identity, and the service writes it with its own
// admin account (O1, D136). That is the enforcement, it is pinned by C0.6/C1.3,
// and no test in this package can stand in for it.
//
// What this file pins is a second layer, and it is worth being exact about its
// limits, because the first version of this comment was not (review finding m8).
// It is an allow-list of the packages that may IMPORT internal/creds: the
// package itself, the service that owns the state (internal/credsvc, C1.10
// onward), and the operator's CLI (internal/cli, whose `fugaro accounts`
// commands show the marks and clear a broken one, C1.21). Every package on the
// run side is off the list — internal/runner and internal/gateway (the in-runner
// gateway), internal/agent, internal/managed, internal/backend, internal/task —
// so that a refactor cannot quietly move a write of the state into the code a
// run executes, where a later loosening of the rules would hand it to a
// contributor's process.
//
// It is not "nothing here is ever linked into the runner", and a reader should
// not take it that way: cmd/fugaro links internal/cli and the runner into ONE
// binary, so the binary a run's image carries does contain the operator commands
// and, from C1.21, the state above them. The plan puts them there deliberately
// (the operator has no admin endpoint to call, so the CLI writes creds/ as the
// operator's own admin identity) and what a run cannot do is hold that identity:
// the run's token is a different principal, and the rules are what refuse it. So
// this test says "no run-side package imports the state", and never "the state
// is not in the run's process".
//
// The scan reads import lines, parsed rather than grepped, so a comment or a
// string that merely names the package is not an import and is not this test's
// business. It walks the whole module, this file included: a test file is as
// much part of a package's build as any other, and there is no exemption. The
// allow-list itself is asserted to be exactly the three entries below, so
// widening it is an edit a reviewer sees rather than a new import that slips in.

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// credsPackage is the import path this boundary is about.
const credsPackage = "github.com/dimipaun/fugaro/internal/creds"

// credsImporters is the ONE-LIST of directories (module-relative, slash
// separated) whose files may import credsPackage. Widening it is a deliberate
// edit here, made with the reason written next to the new entry, and never a
// quiet new import in some future package.
var credsImporters = []string{
	"internal/creds",   // the package itself, and its tests
	"internal/credsvc", // the service: the cascade's one caller (C1.10, C1.11)
	"internal/cli",     // the operator's `fugaro accounts` commands (C1.21)
}

// TestCredsIsNotReachableFromARun fails if any file outside the allow-list
// imports the credential state.
func TestCredsIsNotReachableFromARun(t *testing.T) {
	// The tests of a package run with the package's directory as the working
	// directory, so the module root is two levels up.
	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("the module root is not at %s: %v", root, err)
	}

	var found []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata") {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if allowlisted(rel) {
			return nil
		}
		imports, err := importPaths(path)
		if err != nil {
			return err
		}
		for _, imp := range imports {
			if imp == credsPackage {
				found = append(found, rel+" imports "+credsPackage)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) > 0 {
		t.Fatalf("a run-side file imports the credential state:\n  %s\n\nA run token holds no write on creds/ (O1), and the rules are what enforce it (C1.3); this list is what keeps a write of the state out of the code a run executes, so that a later loosening of the rules is not enough to hand the cascade's marks to a contributor's process. Move the call into the service, or add the directory to credsImporters with its reason if this is genuinely a new caller of the state.",
			strings.Join(found, "\n  "))
	}

	// The allow-list is exactly the three callers above: a fourth entry is a
	// deliberate edit here and not a quiet import somewhere (review finding m8),
	// and a name that is no longer a caller should leave the list rather than
	// widen it.
	want := []string{"internal/creds", "internal/credsvc", "internal/cli"}
	if len(credsImporters) != len(want) {
		t.Fatalf("the allow-list holds %d entries %v; want exactly %v", len(credsImporters), credsImporters, want)
	}
	for i, dir := range want {
		if credsImporters[i] != dir {
			t.Fatalf("the allow-list is %v; want exactly %v", credsImporters, want)
		}
	}
}

// allowlisted reports whether a module-relative file is one of the allowed
// importers: its directory is on the list, or is below one that is.
func allowlisted(rel string) bool {
	for _, dir := range credsImporters {
		if rel == dir || strings.HasPrefix(rel, dir+"/") {
			return true
		}
	}
	return false
}

// importPaths parses one Go file and answers the import paths it names. Parsing
// rather than grepping is what keeps a comment or a string that mentions the
// package out of the result.
func importPaths(path string) ([]string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(f.Imports))
	for _, imp := range f.Imports {
		out = append(out, strings.Trim(imp.Path.Value, `"`))
	}
	return out, nil
}
