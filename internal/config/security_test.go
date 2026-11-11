package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/orchestrator/sensitivity"
)

// security.go's tests (SC9, F10, F23, D1506, D1511).
//
// What they hold. The resolution is a union and never a replacement: the
// engine's floor, then the project layer's rows, then the repository's, with a
// name as a row's identity and the first writer's row kept — so a repository
// can add to its project's list and can never take a row away from it, and an
// empty list at any layer is not a way to ask for less (D1504, D1506). Every
// row also carries the layer it is in force from, which is what the reader
// prints and what doctor counts.

// securityTestRepo is a repository's own fugaro.yaml carrying block as its
// security: key, decoded by the same strict reader every repository's file
// goes through. The rest of the file is the least a fugaro.yaml needs.
func securityTestRepo(t *testing.T, block string) *Config {
	t.Helper()
	text := "version: 1\nproject: acme\ngit: { provider: github }\n" + block +
		"workflows: { server: { commands: { build: make, test: make test } } }\n"
	c, ps := Parse([]byte(text))
	if len(ps) > 0 {
		t.Fatalf("the repository's own file is invalid: %v", ps)
	}
	return c
}

// securityTestLayer is a project layer carrying block as its security: key.
func securityTestLayer(t *testing.T, block string) *ProjectLayer {
	t.Helper()
	return mustLayer(t, "version: 1\nproject: acme\ngcp_project: acme-fugaro\n"+block)
}

// securityTestRow is the one row of list named name, or a failure: a name is a
// row's identity in the list in force, so a resolution that carries a name
// twice carries a name with two answers.
func securityTestRow(t *testing.T, list SecurityList, name string) SecurityRow {
	t.Helper()
	var found []SecurityRow
	for _, row := range list {
		if row.Pattern.Name == name {
			found = append(found, row)
		}
	}
	if len(found) != 1 {
		t.Fatalf("the list in force carries %d rows named %q, want exactly one: %+v", len(found), name, list)
	}
	return found[0]
}

// securityTestFloorRows is the engine's floor as the list in force carries
// it: every row, with the source the resolution gives it.
func securityTestFloorRows() SecurityList {
	floor := sensitivity.Floor()
	out := make(SecurityList, 0, len(floor))
	for _, row := range floor {
		out = append(out, SecurityRow{Pattern: row, Source: SecuritySourceFloor})
	}
	return out
}

// TestRepoKeyCannotRemoveAProjectRow is the direction D1506 exists for, and
// the red step this task was written against: a repository's list is the
// union's last writer, so a row that writes again a name the project layer
// already holds — here a narrower glob for the same name — cannot take the
// project's row away from it, and the repository's own rows are added beside
// the floor's and the project's rather than replacing either.
func TestRepoKeyCannotRemoveAProjectRow(t *testing.T) {
	layer := securityTestLayer(t, "security: { patterns: [ { name: payments, match: \"internal/payments/**\", category: credentials } ] }\n")
	repo := securityTestRepo(t, "security: { patterns: [ { name: payments, match: \"internal/payments/legacy/**\", category: artifacts }, { name: billing, match: \"internal/billing/**\", category: credentials } ] }\n")

	list := SecurityPatterns(repo.Security, layer)

	payments := securityTestRow(t, list, "payments")
	if payments.Pattern.Match != "internal/payments/**" || payments.Pattern.Category != sensitivity.Credentials {
		t.Errorf("payments = %+v, want the project layer's own row: a repository may add rows and may not replace one", payments.Pattern)
	}
	if payments.Source != SecuritySourceProject {
		t.Errorf("payments.source = %q, want %q: the project layer is the first writer of that name", payments.Source, SecuritySourceProject)
	}
	billing := securityTestRow(t, list, "billing")
	if billing.Source != SecuritySourceRepo {
		t.Errorf("billing.source = %q, want %q", billing.Source, SecuritySourceRepo)
	}
	if want := len(sensitivity.Floor()) + 2; len(list) != want {
		t.Errorf("the list in force holds %d rows, want the floor's %d plus the project's one and the repository's one (%d): a union adds, and nothing a later layer writes takes a row of an earlier one away", len(list), len(sensitivity.Floor()), want)
	}
}

// TestEmptyRepoListIsTheProjectListAndTheFloor: a repository that writes no
// security: key — or an empty one — resolves to the floor and the project
// layer's rows, never to the floor alone.
func TestEmptyRepoListIsTheProjectListAndTheFloor(t *testing.T) {
	layer := securityTestLayer(t, "security: { patterns: [ { name: payments, match: \"internal/payments/**\", category: credentials } ] }\n")
	for _, repo := range []*Config{securityTestRepo(t, ""), securityTestRepo(t, "security: {}\n")} {
		list := SecurityPatterns(repo.Security, layer)

		if want := len(sensitivity.Floor()) + 1; len(list) != want {
			t.Fatalf("the list in force holds %d rows, want the floor's %d and the project's one", len(list), want)
		}
		payments := securityTestRow(t, list, "payments")
		if payments.Source != SecuritySourceProject {
			t.Errorf("payments.source = %q, want %q: a repository with no list does not clear its project's", payments.Source, SecuritySourceProject)
		}
	}
}

// TestEmptyProjectListIsTheFloor: a project layer that writes the key with no
// rows adds nothing, and neither does a repository that writes none beside it:
// the list in force is the floor alone, every row of it the engine's own.
func TestEmptyProjectListIsTheFloor(t *testing.T) {
	layer := securityTestLayer(t, "security: {}\n")
	repo := securityTestRepo(t, "security: {}\n")

	list := SecurityPatterns(repo.Security, layer)

	if len(list) != len(sensitivity.Floor()) {
		t.Fatalf("the list in force holds %d rows, want the floor's %d", len(list), len(sensitivity.Floor()))
	}
	for i, row := range list {
		if row.Source != SecuritySourceFloor {
			t.Errorf("rows[%d] (%s).source = %q, want %q: an empty block is a layer that wrote nothing", i, row.Pattern.Name, row.Source, SecuritySourceFloor)
		}
	}
}

// TestBothEmptyIsTheFloor: no project layer at all and a repository with no
// key is the floor, which is the state of every repository that writes
// neither — the list in force is never nothing (D1504).
func TestBothEmptyIsTheFloor(t *testing.T) {
	repo := securityTestRepo(t, "")

	list := SecurityPatterns(repo.Security, nil)

	floor := securityTestFloorRows()
	if len(list) != len(floor) {
		t.Fatalf("the list in force holds %d rows, want the floor's %d", len(list), len(floor))
	}
	for i, row := range list {
		if row != floor[i] {
			t.Errorf("rows[%d] = %+v, want %+v: the floor is the list in force, row for row", i, row, floor[i])
		}
	}
}

// TestResolutionNamesEachRowsSource: every row of the list in force carries
// the layer it came from, out of D1512's three words, and the three layers are
// in force in the union's own order — the floor's rows first, then the
// project's, then the repository's.
func TestResolutionNamesEachRowsSource(t *testing.T) {
	layer := securityTestLayer(t, "security: { patterns: [ { name: payments, match: \"internal/payments/**\", category: credentials } ] }\n")
	repo := securityTestRepo(t, "security: { patterns: [ { name: billing, match: \"internal/billing/**\", category: credentials } ] }\n")

	list := SecurityPatterns(repo.Security, layer)

	seen := map[SecuritySource]int{}
	want := map[string]SecuritySource{"payments": SecuritySourceProject, "billing": SecuritySourceRepo}
	for i, row := range list {
		seen[row.Source]++
		if want, ok := want[row.Pattern.Name]; ok && row.Source != want {
			t.Errorf("rows[%d] (%s).source = %q, want %q", i, row.Pattern.Name, row.Source, want)
		}
		if !strings.Contains("floor project repo", string(row.Source)) {
			t.Errorf("rows[%d] (%s).source = %q, want one of floor, project, repo", i, row.Pattern.Name, row.Source)
		}
	}
	if seen[SecuritySourceFloor] != len(sensitivity.Floor()) {
		t.Errorf("the list in force carries %d rows from the floor, want %d", seen[SecuritySourceFloor], len(sensitivity.Floor()))
	}
	if seen[SecuritySourceProject] != 1 || seen[SecuritySourceRepo] != 1 {
		t.Errorf("sources = %v, want one row from the project layer and one from the repository", seen)
	}
	if got := len(list.Patterns()); got != len(list) {
		t.Errorf("Patterns() = %d rows, want the list's own %d", got, len(list))
	}
}

// TestResolutionIsTheSameFunctionEveryReaderCalls is D1511's "one source of
// truth" held to the code rather than promised: the three readers of slices 1
// and 2 call this one resolution, and no file of either package reads the
// engine's floor directly any more. A reader that kept its own call to
// sensitivity.Floor() would be the second list the day a layer's rows are
// added to it — which is the disagreement D1511 exists to make impossible.
func TestResolutionIsTheSameFunctionEveryReaderCalls(t *testing.T) {
	for _, rel := range []string{"cli/security.go", "cli/doctor_security.go", "runner/security.go"} {
		path := filepath.Join("..", rel)
		if !fileCalls(t, path, "config", "SecurityPatterns") {
			t.Errorf("%s does not call config.SecurityPatterns: every reader of the list in force is the one resolution (D1511)", path)
		}
	}
	for _, dir := range []string{"../cli", "../runner"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("reading %s: %v", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(dir, name)
			if fileCalls(t, path, "sensitivity", "Floor") {
				t.Errorf("%s reads sensitivity.Floor() directly: the list in force is config.SecurityPatterns' answer, floor, project layer and repository merged", path)
			}
		}
	}
}

// fileCalls reports whether the file's own source holds a call to pkg.name.
// The call is read out of the syntax tree rather than matched in the text: a
// doc comment that names the function is not a call to it, and a check that
// could not tell the two apart would fail on the comment that explains it.
func fileCalls(t *testing.T, path, pkg, name string) bool {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if ok && id.Name == pkg && sel.Sel.Name == name {
			found = true
		}
		return true
	})
	return found
}
