package cli

// `fugaro backlog index` and `fugaro backlog list` (rigor modes, D28, plan
// T4.4): the pair a person runs over the folder. The folder's own pin lives in
// docs_backlog_test.go and reads the repository's real items; this file drives
// the commands over a folder it builds, so the CLI half is checked without the
// repository's backlog being the fixture.
//
// Nothing here reaches a network, a credential or an installation: the pair
// reads and writes files in a temporary directory.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backlog"
)

// backlogFixture writes two items into a fresh folder and returns it: one
// open item and one closed, so a group and the count line both have something
// to say.
func backlogFixture(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), backlog.Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, doc string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("20261021-01-a-title.md", itemDoc("20261021-01", "a title"))
	write("20261021-02-another.md", strings.Replace(itemDoc("20261021-02", "another"), "status: open", "status: closed", 1))
	return dir
}

// TestBacklogIndexWritesTheIndexFromTheItems: `index` writes <dir>/README.md
// from the item files beside it and says which file it wrote. The bytes are
// the renderer's own, so the command is a thin wrapper over the one function
// the folder's pin holds.
//
// Mutation (verified, run and restored): write the index into the working
// directory instead of <dir> — this fails on the file it cannot find.
func TestBacklogIndexWritesTheIndexFromTheItems(t *testing.T) {
	dir := backlogFixture(t)
	out, _, err := execute(t, "backlog", "index", "--dir", dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, backlog.IndexName)
	if want := "wrote " + path + "\n"; out != want {
		t.Errorf("fugaro backlog index printed %q, want %q", out, want)
	}
	items, err := backlog.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("fugaro backlog index wrote no %s: %v", path, err)
	}
	if got, want := string(data), backlog.Render(items); got != want {
		t.Errorf("%s is not what the items render to:\n--- written ---\n%s\n--- rendered ---\n%s", path, got, want)
	}
	if !strings.HasPrefix(string(data), backlog.Header) {
		t.Errorf("%s does not open with %q: the generated file names the one command that rewrites it (D28)", path, backlog.Header)
	}
}

// TestBacklogListPrintsTheIndexAndWritesNothing: `list` prints the index
// rendered from the items — not read back from README.md, so a stale file
// cannot change what it prints — and the folder is unchanged afterwards. The
// read-only half is the one the console spawns, so a write here would be a
// write in a screen's path.
//
// Mutation (verified, run and restored): have runBacklogList write the index
// before printing — this fails on the folder's listing.
func TestBacklogListPrintsTheIndexAndWritesNothing(t *testing.T) {
	dir := backlogFixture(t)
	before := backlogFolderEntries(t, dir)
	out, _, err := execute(t, "backlog", "list", "--dir", dir)
	if err != nil {
		t.Fatal(err)
	}
	items, err := backlog.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := backlog.Render(items); out != want {
		t.Errorf("fugaro backlog list printed:\n%s\nwant:\n%s", out, want)
	}
	if after := backlogFolderEntries(t, dir); after != before {
		t.Errorf("fugaro backlog list changed the folder: %q, want %q", after, before)
	}
}

// TestBacklogListJSONCarriesTheIndexRows: the document is the index's rows as
// data, in the index's order, with the count line's three figures (D28,
// console/docs/json-contract.md §6.11). The rows are what a screen shows, so
// the fields are read off the wire rather than from the struct.
//
// Mutation (verified, run and restored): report the counts of the whole
// document under `open` — this fails naming the figure.
func TestBacklogListJSONCarriesTheIndexRows(t *testing.T) {
	dir := backlogFixture(t)
	out, _, err := execute(t, "backlog", "list", "--dir", dir, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Dir   string `json:"dir"`
		Items []struct {
			ID      string `json:"id"`
			Title   string `json:"title"`
			Mode    string `json:"mode"`
			Area    string `json:"area"`
			Status  string `json:"status"`
			FiledBy string `json:"filed_by"`
			FiledIn string `json:"filed_in"`
			Date    string `json:"date"`
			Path    string `json:"path"`
		} `json:"items"`
		Open    int `json:"open"`
		Closed  int `json:"closed"`
		Dropped int `json:"dropped"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("fugaro backlog list --json printed no document: %v\n%s", err, out)
	}
	if !strings.HasPrefix(out, "{\n  \"dir\"") {
		t.Errorf("the document is not indented, which is what every other document in the console's set is:\n%s", out)
	}
	if doc.Dir != dir {
		t.Errorf("dir = %q, want %q: the document says which folder it read", doc.Dir, dir)
	}
	if doc.Open != 1 || doc.Closed != 1 || doc.Dropped != 0 {
		t.Errorf("the counts are %d open, %d closed, %d dropped; want 1, 1, 0 — the figures the index's count line states", doc.Open, doc.Closed, doc.Dropped)
	}
	if len(doc.Items) != 2 {
		t.Fatalf("the document carries %d rows, want 2", len(doc.Items))
	}
	first := doc.Items[0]
	if first.ID != "20261021-01" || first.Title != "a title" || first.Mode != "none" ||
		first.Area != "repo" || first.Status != "open" || first.FiledBy != "dimipaun" ||
		first.FiledIn != "hand" || first.Date != "2026-10-21" {
		t.Errorf("the first row is %+v, want the item's own eight fields", first)
	}
	if want := filepath.Join(dir, "20261021-01-a-title.md"); first.Path != want {
		t.Errorf("the first row's path is %q, want %q: the row says which file it was read from", first.Path, want)
	}
	if doc.Items[1].Status != "closed" {
		t.Errorf("the second row's status is %q, want closed: the rows are the index's own order", doc.Items[1].Status)
	}
}

// TestBacklogRefusesAFolderThatIsNotThere: a missing folder is an error and
// not an empty backlog, on both commands, and `index` writes nothing on the
// way out. The same rule internal/backlog states, at the CLI's boundary: an
// empty listing would read as a folder with no items.
//
// Mutation (verified, run and restored): make backlog.Load return an empty
// slice for a folder it cannot read — this fails on both commands.
func TestBacklogRefusesAFolderThatIsNotThere(t *testing.T) {
	dir := filepath.Join(t.TempDir(), backlog.Dir)
	for _, args := range [][]string{
		{"backlog", "list", "--dir", dir},
		{"backlog", "index", "--dir", dir},
	} {
		if _, _, err := execute(t, args...); err == nil {
			t.Errorf("fugaro %s accepted a folder that is not there", strings.Join(args[:2], " "))
		}
	}
	if _, err := os.Stat(filepath.Join(dir, backlog.IndexName)); !os.IsNotExist(err) {
		t.Errorf("fugaro backlog index left %s behind in a folder it could not read: %v", backlog.IndexName, err)
	}
}

// TestBacklogIndexRefusesAnItemTheFormatRefuses: the folder is validated
// before the index is written, so a folder holding a file that is not an item
// leaves the index as it was rather than publishing a document that silently
// omits it. This is the same rule the folder's completeness guard enforces
// from the other side.
//
// Mutation (verified, run and restored): have runBacklogIndex ignore Load's
// error and render an empty index — this fails on the file it wrote.
func TestBacklogIndexRefusesAnItemTheFormatRefuses(t *testing.T) {
	dir := backlogFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("stray\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "backlog", "index", "--dir", dir); err == nil {
		t.Error("fugaro backlog index accepted a folder holding a file that is not an item")
	}
	if _, err := os.Stat(filepath.Join(dir, backlog.IndexName)); !os.IsNotExist(err) {
		t.Errorf("fugaro backlog index wrote %s from a folder it refused: %v", backlog.IndexName, err)
	}
}

// backlogFolderEntries returns the folder's entry names, so "writes nothing"
// is a fact about the folder rather than about a file the test thought of.
func backlogFolderEntries(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return strings.Join(names, " ")
}
