package cli

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The secret-reader audit (secrets-brokering plan, task B0.1): who reads a
// Secret Manager value today, one row per reader, each row carrying the
// decision that keeps it reading and the slice that ends it.
//
// The doc is the source of the IDs B0.2's guard lists in its own exceptions
// table, so its shape is a contract with that task and not a preference: this
// test pins the columns, the ID form, the kind and the exit vocabulary, and
// deliberately nothing else -- the prose around the table, the number of rows
// and the words in a cell are what B0.2, B1.10 and B1.13 edit.
//
// Both tests are named TestDocs... because every later docs task runs
// `-run Docs`, which is how a row that leaves the doc without leaving the
// guard (or the reverse) is caught by the task that moved it.
const (
	secretReadersDoc       = "docs/secrets-brokering/secret-readers.md"
	secretsBrokeringPlan   = "docs/secrets-brokering/implementation-plan.md"
	secretsBrokeringReadme = "docs/secrets-brokering/README.md"
)

// secretReaderColumns is the audit table's header, exactly: B0.2 parses these
// cells by position.
var secretReaderColumns = []string{"ID", "Kind", "Reader", "What it reads", "How", "Decision", "Exit slice"}

// secretsBrokeringSlicesHeader is the plan's own Slices table, the one place
// the exit vocabulary is written down.
var secretsBrokeringSlicesHeader = []string{"Slice", "Release", "What", "Inert?"}

var (
	secretReaderIDRE = regexp.MustCompile(`^E[0-9]+$`)
	sliceIDRE        = regexp.MustCompile(`^S[0-9]+$`)
)

// docRow is one data row of a markdown table, with the 1-based line it sits on
// so a failure names the row a reader can find.
type docRow struct {
	line  int
	cells []string
}

// tableCells splits one markdown table line into its cells. A line is a table
// row when it starts and ends with a pipe; the outer pipes go and the cells are
// trimmed, so a row parses the same however it is padded.
func tableCells(line string) ([]string, bool) {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < 2 || !strings.HasPrefix(trimmed, "|") || !strings.HasSuffix(trimmed, "|") {
		return nil, false
	}
	cells := []string{}
	for _, cell := range strings.Split(trimmed[1:len(trimmed)-1], "|") {
		cells = append(cells, strings.TrimSpace(cell))
	}
	return cells, true
}

// tableSeparator reports whether cells is a table's |---|---| rule row.
func tableSeparator(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		if cell == "" || strings.Trim(cell, "-:") != "" {
			return false
		}
	}
	return true
}

// docTable reads the one table in doc whose header row is exactly header: its
// data rows, and every table row that follows them (which a doc with one table
// has none of, so the caller can say so rather than miss a second table).
func docTable(t *testing.T, doc, path string, header []string) (rows, after []docRow) {
	t.Helper()
	lines := strings.Split(doc, "\n")
	found := -1
	for i, line := range lines {
		cells, ok := tableCells(line)
		if !ok || !slices.Equal(cells, header) {
			continue
		}
		if found >= 0 {
			t.Fatalf("%s has two tables whose header is %q: this test reads one", path, strings.Join(header, " | "))
		}
		found = i
	}
	if found < 0 {
		t.Fatalf("%s has no table whose header is exactly %q", path, strings.Join(header, " | "))
	}
	if found+1 >= len(lines) {
		t.Fatalf("%s:%d: the table's header is the file's last line, so there is no separator row", path, found+1)
	}
	if cells, ok := tableCells(lines[found+1]); !ok || !tableSeparator(cells) {
		t.Fatalf("%s:%d: the line after the table's header is not its |---| separator", path, found+2)
	}
	i := found + 2
	for ; i < len(lines); i++ {
		cells, ok := tableCells(lines[i])
		if !ok {
			break
		}
		rows = append(rows, docRow{line: i + 1, cells: cells})
	}
	for ; i < len(lines); i++ {
		if cells, ok := tableCells(lines[i]); ok {
			after = append(after, docRow{line: i + 1, cells: cells})
		}
	}
	return rows, after
}

// TestDocsSecretReadersTableShape: every row of the audit table has a unique
// E<number> ID, a kind of runtime or operator, a decision, and an exit slice
// the plan's own Slices table names (or never). It is the shape B0.2 parses.
func TestDocsSecretReadersTableShape(t *testing.T) {
	exits := secretsBrokeringExitSlices(t)
	rows, after := docTable(t, readDoc(t, secretReadersDoc), secretReadersDoc, secretReaderColumns)
	if len(rows) == 0 {
		t.Fatalf("%s's audit table has no rows: an audit with no reader is not the table B0.2 keys its exceptions on", secretReadersDoc)
	}
	for _, r := range after {
		t.Errorf("%s:%d: a table row follows the audit table: the doc holds one table, and B0.2 parses it", secretReadersDoc, r.line)
	}
	seen := map[string]int{}
	for _, r := range rows {
		if len(r.cells) != len(secretReaderColumns) {
			t.Errorf("%s:%d: the row has %d cells, want %d (%s)", secretReadersDoc, r.line, len(r.cells), len(secretReaderColumns), strings.Join(secretReaderColumns, " | "))
			continue
		}
		id := r.cells[0]
		switch {
		case !secretReaderIDRE.MatchString(id):
			t.Errorf("%s:%d: the ID %q is not of the form E<number>", secretReadersDoc, r.line, id)
		case seen[id] > 0:
			t.Errorf("%s:%d: the ID %s is already the ID of the row on line %d: B0.2's exceptions table is keyed by these IDs, so a duplicate would make one row unreachable", secretReadersDoc, r.line, id, seen[id])
		default:
			seen[id] = r.line
		}
		if kind := r.cells[1]; kind != "runtime" && kind != "operator" {
			t.Errorf("%s:%d: %s's kind is %q, want runtime or operator", secretReadersDoc, r.line, id, kind)
		}
		if r.cells[5] == "" {
			t.Errorf("%s:%d: %s names no decision: every row says which decision keeps it reading", secretReadersDoc, r.line, id)
		}
		if exit := r.cells[6]; exit != "never" && !slices.Contains(exits, exit) {
			t.Errorf("%s:%d: %s's exit slice is %q, want one of %s or never", secretReadersDoc, r.line, id, exit, strings.Join(exits, ", "))
		}
	}
}

// secretsBrokeringExitSlices is the exit vocabulary the audit's last column may
// use, read out of the plan's own Slices table rather than written down a second
// time here: a slice the plan adds is an exit the doc may name, and one the plan
// renames is a value this test then refuses.
//
// The table's first row -- the slice this audit itself ships in -- is dropped.
// An exit names the slice that ends a read, and a reader cannot exit into the
// slice that is auditing it.
func secretsBrokeringExitSlices(t *testing.T) []string {
	t.Helper()
	rows, _ := docTable(t, readDoc(t, secretsBrokeringPlan), secretsBrokeringPlan, secretsBrokeringSlicesHeader)
	var ids []string
	for _, r := range rows {
		if len(r.cells) == 0 {
			continue
		}
		if id := strings.Trim(r.cells[0], "* "); sliceIDRE.MatchString(id) {
			ids = append(ids, id)
		}
	}
	if len(ids) < 2 {
		t.Fatalf("%s's Slices table yielded %d slice names (%v): this test reads the exit vocabulary from it, so a table it cannot read is a table that cannot check the doc", secretsBrokeringPlan, len(ids), ids)
	}
	return ids[1:]
}

// TestDocsSecretsBrokeringReadmeStatus pins one sentence of the README's status
// line and nothing else about that file: B0.4 writes it and B0.2, B1.10 and
// B1.11 edit it after, so a pin on its shape would fail each of them. What has
// to survive every one of those edits is the date the owner answered the
// decision block this whole directory is built on -- the fact a reader checks
// the rest of the plan against.
func TestDocsSecretsBrokeringReadmeStatus(t *testing.T) {
	status := flattenDoc(readDoc(t, secretsBrokeringReadme))
	const want = "The owner answered D490–D506 on 2026-10-27"
	if !strings.Contains(status, want) {
		t.Errorf("%s no longer says %q: the owner's answers of that date are what every slice in this directory is approved against", secretsBrokeringReadme, want)
	}
}
