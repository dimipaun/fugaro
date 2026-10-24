package backlog

// Render's own rules, on synthetic items: the index's shape is D28's, and it
// has to hold whatever the repository's folder happens to contain.

import (
	"strings"
	"testing"
)

func synth(id, status, title string) Item {
	return Item{
		ID: id, Title: title, Mode: "none", Area: "repo", Status: status,
		FiledBy: "dimipaun", FiledIn: "hand", Date: "2026-10-21",
		Path: Dir + "/" + id + "-a-slug.md",
	}
}

// TestRenderGroupsTheRowsAndSortsThemById: one row per item, sorted by id —
// which is to say by date — with the closed and the dropped rows grouped
// under the open ones, and a row's first cell the id linked to its file.
func TestRenderGroupsTheRowsAndSortsThemById(t *testing.T) {
	doc := Render([]Item{
		synth("20261021-03", "dropped", "dropped one"),
		synth("20261021-02", "closed", "closed one"),
		synth("20261021-01", "open", "open one"),
		synth("20261021-04", "open", "open two"),
	})
	openAt := strings.Index(doc, "## Open")
	closedAt := strings.Index(doc, "## Closed")
	droppedAt := strings.Index(doc, "## Dropped")
	if openAt < 0 || closedAt < 0 || droppedAt < 0 {
		t.Fatalf("Render's index does not carry the three sections:\n%s", doc)
	}
	if !(openAt < closedAt && closedAt < droppedAt) {
		t.Errorf("Render's sections are not open, then closed, then dropped:\n%s", doc)
	}
	row := "| [20261021-01](20261021-01-a-slug.md) | open one | none | repo | open | dimipaun | 2026-10-21 |"
	if !strings.Contains(doc, row) {
		t.Errorf("Render's index does not carry the row %q:\n%s", row, doc)
	}
	// Sorted by id inside a group: 20261021-04 comes after 20261021-01.
	if strings.Index(doc, "20261021-01") > strings.Index(doc, "20261021-04") {
		t.Errorf("Render's open rows are not sorted by id:\n%s", doc)
	}
}

// TestRenderStatesTheCountLineAndOmitsAnEmptyGroup: D28's count line is what
// says a status with no section has no items rather than a section someone
// dropped, so the zero has to be stated there.
func TestRenderStatesTheCountLineAndOmitsAnEmptyGroup(t *testing.T) {
	doc := Render([]Item{synth("20261021-01", "open", "open one"), synth("20261021-02", "closed", "closed one")})
	if !strings.Contains(doc, "2 items: 1 open, 1 closed, 0 dropped.") {
		t.Errorf("Render's count line is not D28's:\n%s", doc)
	}
	if strings.Contains(doc, "## Dropped") {
		t.Errorf("Render emitted a section for a status with no rows:\n%s", doc)
	}
	single := Render([]Item{synth("20261021-01", "open", "open one")})
	if !strings.Contains(single, "1 item: 1 open, 0 closed, 0 dropped.") {
		t.Errorf("Render's count line does not agree with its noun for one item:\n%s", single)
	}
}

// TestRenderAnEmptyFolderSaysSo: a folder with no items renders a document
// rather than a broken one, and states its own zero.
func TestRenderAnEmptyFolderSaysSo(t *testing.T) {
	doc := Render(nil)
	if !strings.HasPrefix(doc, Header) {
		t.Errorf("Render's index does not begin with the generated-file header %q:\n%s", Header, doc)
	}
	if !strings.Contains(doc, "0 items: 0 open, 0 closed, 0 dropped.") {
		t.Errorf("Render's empty index does not state its own zero:\n%s", doc)
	}
	if !strings.Contains(doc, "No items yet.") {
		t.Errorf("Render's empty index says nothing about being empty:\n%s", doc)
	}
	if strings.Contains(doc, "| id |") {
		t.Errorf("Render's empty index carries a table with no rows:\n%s", doc)
	}
}

// TestRenderIsAPureFunctionOfItsArgument: the same items render to the same
// bytes whichever order they are handed over in, and the document is the same
// twice — which is what makes a regeneration in review diff only where an item
// changed (D27, F45's lesson from gencatalog).
func TestRenderIsAPureFunctionOfItsArgument(t *testing.T) {
	a := []Item{synth("20261021-01", "open", "open one"), synth("20261021-02", "closed", "closed one")}
	b := []Item{a[1], a[0]}
	if Render(a) != Render(b) {
		t.Errorf("Render's output depends on the order its items are handed over in")
	}
	if Render(a) != Render(a) {
		t.Errorf("Render is not deterministic")
	}
}

// TestRenderKeepsARowOnOneLine: a title is a filer's own words, so a pipe in
// it would end the row and a newline would end the table. The cell is escaped
// rather than trusted.
func TestRenderKeepsARowOnOneLine(t *testing.T) {
	doc := Render([]Item{synth("20261021-01", "open", "a | pipe and\na newline")})
	for _, line := range strings.Split(doc, "\n") {
		if !strings.HasPrefix(line, "| [20261021-01]") {
			continue
		}
		if !strings.Contains(line, `a \| pipe and a newline`) {
			t.Errorf("Render's row does not escape the title into one cell: %q", line)
		}
		return
	}
	t.Errorf("Render's index carries no row for the item:\n%s", doc)
}

// TestRenderEscapesABackslash: a title ending in a backslash would otherwise
// escape the pipe that closes its cell, and the row's next columns would slide
// into it. The backslash is escaped first, so a title's own `\|` stays text.
func TestRenderEscapesABackslash(t *testing.T) {
	doc := Render([]Item{synth("20261021-01", "open", `C:\temp\ and a\|b`)})
	for _, line := range strings.Split(doc, "\n") {
		if !strings.HasPrefix(line, "| [20261021-01]") {
			continue
		}
		if !strings.Contains(line, `C:\\temp\\ and a\\\|b |`) {
			t.Errorf("Render's row does not escape the backslashes in the title: %q", line)
		}
		return
	}
	t.Errorf("Render's index carries no row for the item:\n%s", doc)
}

// TestRenderNamesItsOwnCommandAndItsFormat: the header names the command that
// regenerates the file, so a reader who finds a stale index has the fix on the
// first line, and the document points at the format an item follows.
func TestRenderNamesItsOwnCommandAndItsFormat(t *testing.T) {
	doc := Render(nil)
	for _, want := range []string{"fugaro backlog index", "do not edit", "[FORMAT.md](FORMAT.md)"} {
		if !strings.Contains(doc, want) {
			t.Errorf("Render's index does not carry %q:\n%s", want, doc)
		}
	}
}
