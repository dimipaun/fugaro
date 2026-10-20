package creds

// The runner boundary (credentials-service plan Convention 11; O1's ruling that
// creds/ is denied to every run token).
//
// The cascade's state is writable by the service's own account and by nothing
// else: a run token holds no permission on creds/ at all, and the rules deny the
// whole subtree to every run identity. That is a rule about who may WRITE, and
// it is enforced in the database and pinned by C0.6/C1.3. This file pins the
// other half of the same claim, which no database rule can express: a package
// that can write the state must not be reachable from a run at all, so that a
// future change to a run's permissions (or a mistake in the rules) does not put
// the cascade's marks in the hands of the code a contributor controls.
//
// So the test is an allow-list of the packages that may import internal/creds,
// and it is deliberately short: the package itself, the service that owns the
// state (internal/credsvc, C1.10 onward), and the operator's CLI (internal/cli,
// which shows the marks and clears a broken one, C1.21). Every package on the
// run side is off the list, and the ones that matter are named below so that a
// reader does not have to work out which they are: internal/runner and
// internal/gateway (the in-runner gateway), internal/agent, internal/managed,
// internal/backend and internal/task, all of which a run's machine builds
// against.
//
// The scan reads import lines, parsed rather than grepped, so a comment or a
// string that merely names the package is not an import and is not this test's
// business. It walks the whole module, this file included: a test file is as
// much part of a package's build as any other, and there is no exemption.

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
		t.Fatalf("a run-side file reaches the credential state:\n  %s\n\nNothing here is ever linked into the runner (Convention 11): a run token holds no write on creds/ (O1), so a package that writes it must not be reachable from a run. Move the call into the service, or add the directory to credsImporters with its reason if this is genuinely a new caller of the state.",
			strings.Join(found, "\n  "))
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
