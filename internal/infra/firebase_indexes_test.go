//go:build terraform

package infra

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
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

// TestFirebaseModulePlansRunIndexes plans the Firebase root with the tfvars
// `fugaro update` writes and holds its `google_firestore_index` resources to
// contract-index.md's list: every index the contract names is planned, in the
// order it names, and the plan creates none the contract does not.
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
}
