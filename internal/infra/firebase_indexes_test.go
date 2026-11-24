//go:build terraform

package infra

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/runindex"
)

// The run index's composite indexes (docs/console-fixes/contract-index.md,
// C-INDEX; plan task RI2). Firestore serves a query ordered by one field with
// no index of ours, and refuses one that also filters on an equality without a
// composite index — with an error, not a slower answer — so the closed list in
// the contract is the difference between `runs ls`, `runs stats`, `plan status`
// and `graph` working and failing.
//
// What is asserted here is the PLAN of the Firebase root against the real
// provider: the field paths, their directions and the collection are the
// provider's own reading of the module, so a misspelled attribute or an order
// the API does not know fails here rather than in the owner's apply. What the
// indexes must BE is read from contract-index.md's table rather than written
// out again here: the contract is C-INDEX's, this task consumes it, and a row
// added to one without the other fails with both lists printed.

// runIndexCollection is the collection the index's readers query
// (contract-index.md, "The collections").
const runIndexCollection = "runIndex"

// contractIndexes reads the composite indexes contract-index.md's table lists,
// each as "field=DIRECTION,field=DIRECTION" with the fields in the order the
// query sorts by them, sorted so the caller can compare sets.
func contractIndexes(t *testing.T) []string {
	t.Helper()
	const path = "docs/console-fixes/contract-index.md"
	doc, err := os.ReadFile(filepath.Join("..", "..", path))
	if err != nil {
		t.Fatalf("reading %s (the contract this task consumes): %v", path, err)
	}
	text := string(doc)
	const heading = "## The sort key, the cursor and the composite indexes\n"
	start := strings.Index(text, heading)
	if start < 0 {
		t.Fatalf("%s no longer has its %q section: this test reads the index list out of it", path, strings.TrimSpace(heading))
	}
	section := text[start+len(heading):]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	// One table row per index, whose first cell is the field list:
	// "| `slug` ASC, `updated_at` DESC | one repository's runs ... |".
	row := regexp.MustCompile("(?m)^\\| (?:(?:`[a-z_]+` (?:ASC|DESC))(?:, )?)+ \\|")
	field := regexp.MustCompile("`([a-z_]+)` (ASC|DESC)")
	order := map[string]string{"ASC": "ASCENDING", "DESC": "DESCENDING"}
	var out []string
	for _, r := range row.FindAllString(section, -1) {
		var parts []string
		for _, f := range field.FindAllStringSubmatch(r, -1) {
			parts = append(parts, f[1]+"="+order[f[2]])
		}
		if len(parts) < 2 {
			t.Fatalf("%s: the row %q names fewer than two fields, so it is not a composite index: the table's shape changed and this test must follow it", path, strings.TrimSpace(r))
		}
		out = append(out, strings.Join(parts, ","))
	}
	if len(out) == 0 {
		t.Fatalf("%s: no composite index row was read: the table's shape changed and this test must follow it", path)
	}
	slices.Sort(out)
	return out
}

// plannedIndexes reads the composite indexes a plan creates, in the same shape
// contractIndexes answers in.
func plannedIndexes(t *testing.T, planned map[string]plannedResource, project string) []string {
	t.Helper()
	var out []string
	for addr, r := range planned {
		if r.Type != "google_firestore_index" {
			continue
		}
		if !slices.Equal(r.Actions, []string{"create"}) {
			t.Errorf("%s is planned as %v, want a create: the indexes are declared by the module and nothing else makes them", addr, r.Actions)
		}
		if got := r.After["collection"]; got != runIndexCollection {
			t.Errorf("%s is on collection %v, want %q: the index's readers query that one collection (contract-index.md)", addr, got, runIndexCollection)
		}
		if got, ok := r.After["project"]; !ok || got != project {
			t.Errorf("%s is in project %v, want the Firebase project the tfvars name (%s): the index is built where the installation's database is", addr, got, project)
		}
		if got := r.After["database"]; got != "(default)" {
			t.Errorf("%s is in database %v, want %q: the installation has one Firestore database and its name is fixed", addr, got, "(default)")
		}
		fields, _ := r.After["fields"].([]any)
		if len(fields) < 2 {
			t.Fatalf("%s carries %v, which is not a composite index: at least the equality and the sort key are needed", addr, r.After["fields"])
		}
		var parts []string
		for _, f := range fields {
			m, _ := f.(map[string]any)
			path, _ := m["field_path"].(string)
			dir, _ := m["order"].(string)
			if path == "" || dir == "" {
				t.Fatalf("%s carries the field %v, which names no field_path or order", addr, f)
			}
			parts = append(parts, path+"="+dir)
		}
		out = append(out, strings.Join(parts, ","))
	}
	return out
}

// TestFirebaseModulePlansNoIndexWithoutTheDatabase is the gate's own test: an
// installation whose Firestore database is not there plans no index at all.
//
// Two installations reach the root with that answer, and they are one case
// here: a fresh one, whose own run creates the database after this root is
// applied (D3027), and one whose location step was left for a person, so the
// database is not there either. What both would meet without the gate is the
// same: a create refused for a database that does not exist, an apply that
// fails, and a run that never reaches the step that creates the database — so
// `fugaro update` could never converge. With it, such a run plans no index,
// converges, and the next update — which reads the database it has by then —
// plans the contract's indexes.
func TestFirebaseModulePlansNoIndexWithoutTheDatabase(t *testing.T) {
	spec := firebaseDBSpec(t, "us-east5", "aurora-fp")
	spec.FirestoreDatabaseExists = false
	_, planned := firebasePlanAgainst(t, spec, nil)
	if got := plannedIndexes(t, planned, spec.Project); len(got) != 0 {
		t.Errorf("the Firebase root plans %v with no database to put them in: CreateIndex is refused for a database that is not there, and the apply that carried it could never converge (RI2)", got)
	}

	// The gate is the whole difference: with the database there the same root
	// plans everything the gated plan does, and the indexes besides. A root
	// that dropped or changed another resource only for an installation without
	// a database would be a change nobody asked for, in the one plan that runs
	// before the database exists.
	spec.FirestoreDatabaseExists = true
	_, with := firebasePlanAgainst(t, spec, nil)
	for addr := range planned {
		if _, ok := with[addr]; !ok {
			t.Errorf("%s is planned only when the database is missing: the gate is about the run index's composite indexes and nothing else", addr)
		}
	}
	if len(with) <= len(planned) {
		t.Errorf("the plan with the database there holds %d resources and the gated one %d: the indexes are missing from the plan that has the database", len(with), len(planned))
	}
}

// indexKeysOfRows names each row the way the module does: its fields joined
// with "_", in the order the row lists them. want is sorted, so this is too.
func indexKeysOfRows(rows []string) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		var fields []string
		for _, f := range strings.Split(row, ",") {
			name, _, _ := strings.Cut(f, "=")
			fields = append(fields, name)
		}
		out = append(out, strings.Join(fields, "_"))
	}
	return out
}

// indexKeysOfAddresses are the keys a plan's index addresses carry, which are
// the module's `for_each` keys and therefore the names the Firebase root's
// state holds each index under.
func indexKeysOfAddresses(t *testing.T, planned map[string]plannedResource) []string {
	t.Helper()
	var out []string
	for addr, r := range planned {
		if r.Type != "google_firestore_index" {
			continue
		}
		key, ok := strings.CutPrefix(addr, "module.firebase.google_firestore_index.run[")
		key, quoted := strings.CutSuffix(key, "]")
		if !ok || !quoted || len(key) < 2 || !strings.HasPrefix(key, `"`) || !strings.HasSuffix(key, `"`) {
			t.Fatalf("%s is not an address this test reads a key out of: the module's for_each keys are the names the state holds its indexes under (RI2)", addr)
		}
		out = append(out, strings.Trim(key, `"`))
	}
	slices.Sort(out)
	return out
}

// TestFirebaseModulePlansRunIndexes plans the Firebase root with the tfvars
// `fugaro update` writes and holds its `google_firestore_index` resources to
// contract-index.md's list: every index the contract names is planned, and the
// plan creates none the contract does not.
//
// The same three are pinned by name, because `fugaro update` decides whether a
// converged installation carries the indexes by comparing that installation's
// state — whose addresses are these keys — with runindex.Indexes: a row the
// contract and the module carry and that list does not is a row such an
// installation is never given, and the query it serves is refused for ever.
func TestFirebaseModulePlansRunIndexes(t *testing.T) {
	// Firestore's location is the module's own input and the indexes go where
	// the database is, so the region here is the one every other Firebase plan
	// in this package uses.
	spec := firebaseDBSpec(t, "us-east5", "aurora-fp")
	_, planned := firebasePlanAgainst(t, spec, nil)
	project := spec.Project

	want := contractIndexes(t)
	got := plannedIndexes(t, planned, project)
	slices.Sort(got)
	if !slices.Equal(want, got) {
		t.Errorf("the Firebase root plans the composite indexes %v, and docs/console-fixes/contract-index.md lists %v:\nthe contract is C-INDEX's and this task consumes it, so the module declares exactly its rows (RI2)", got, want)
	}

	keys := slices.Clone(runindex.Indexes)
	slices.Sort(keys)
	if contractKeys := indexKeysOfRows(want); !slices.Equal(contractKeys, keys) {
		t.Errorf("contract-index.md's rows name the indexes %v and runindex.Indexes names them %v: the CLI compares that list with the Firebase root's state, so a row added in one place and not the other is an index a converged installation is never given (RI2)", contractKeys, keys)
	}
	if planKeys := indexKeysOfAddresses(t, planned); !slices.Equal(planKeys, keys) {
		t.Errorf("the Firebase root plans the indexes %v and runindex.Indexes names them %v: the CLI's state comparison is written against that list and the state's addresses are these keys (RI2)", planKeys, keys)
	}
}
