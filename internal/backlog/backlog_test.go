package backlog

// The package's own rules, checked on synthetic inputs so they hold whatever
// the repository's own folder happens to contain. The folder's own pin lives
// in internal/cli/docs_backlog_test.go, which is where the tree is read.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// item is a well-formed item's bytes, with one field replaced at a time by the
// cases below, so a refusal is about the field under test and nothing else.
func item(mutate func(*string)) string {
	doc := "---\n" +
		"id: 20261021-01\n" +
		"title: a title\n" +
		"mode: none\n" +
		"area: repo\n" +
		"status: open\n" +
		"filed-by: dimipaun\n" +
		"filed-in: hand\n" +
		"date: 2026-10-21\n" +
		"---\n\n" +
		"## What was left open\n\nthe thing.\n\n" +
		"## Why\n\nbecause.\n"
	if mutate != nil {
		mutate(&doc)
	}
	return doc
}

const itemPath = "docs/backlog/20261021-01-a-title.md"

// TestParseAcceptsAWellFormedItem is the parser's baseline: the eight fields,
// the three-word status, the id agreeing with the file name, and the two
// required headings are all it takes.
func TestParseAcceptsAWellFormedItem(t *testing.T) {
	it, err := Parse(itemPath, []byte(item(nil)))
	if err != nil {
		t.Fatalf("Parse refused a well-formed item: %v", err)
	}
	if it.ID != "20261021-01" || it.Title != "a title" || it.Mode != "none" ||
		it.Area != "repo" || it.Status != "open" || it.FiledBy != "dimipaun" ||
		it.FiledIn != "hand" || it.Date != "2026-10-21" {
		t.Errorf("Parse read %+v, want the file's own eight fields", it)
	}
	if it.Path != itemPath {
		t.Errorf("Parse read Path %q, want %q", it.Path, itemPath)
	}
	if !strings.Contains(it.Body, "## Why") {
		t.Errorf("Parse's Body does not carry the text after the front matter: %q", it.Body)
	}
}

// TestParseRefusesEveryWayAnItemCanBeWrong is the format, one refusal at a
// time: D27's eight fields (missing, unknown, empty, duplicated), D26's id and
// its agreement with the file name, the three-word status, the day-shaped
// date, `filed-in`'s vocabulary, a filed-by that is an address, and D27's
// body headings.
func TestParseRefusesEveryWayAnItemCanBeWrong(t *testing.T) {
	for _, tc := range []struct {
		name   string
		path   string
		mutate func(*string)
		want   string
	}{
		{"no opening fence", itemPath, func(d *string) { *d = strings.TrimPrefix(*d, "---\n") }, "fence"},
		{"unclosed front matter", itemPath, func(d *string) {
			*d = (*d)[:strings.Index(*d, "\n---\n")] + "\n"
		}, "never closed"},
		{"a field is missing", itemPath, func(d *string) { *d = strings.Replace(*d, "area: repo\n", "", 1) }, `"area"`},
		{"a field is empty", itemPath, func(d *string) { *d = strings.Replace(*d, "title: a title", "title:", 1) }, `"title" is empty`},
		{"a field is repeated", itemPath, func(d *string) { *d = strings.Replace(*d, "area: repo\n", "area: repo\narea: repo\n", 1) }, "twice"},
		{"a ninth field", itemPath, func(d *string) { *d = strings.Replace(*d, "date: 2026-10-21", "date: 2026-10-21\nseverity: high", 1) }, `"severity"`},
		{"a line that is not key: value", itemPath, func(d *string) { *d = strings.Replace(*d, "title: a title", "title a title", 1) }, "key: value"},
		{"the id is not a date and a sequence", itemPath, func(d *string) { *d = strings.Replace(*d, "id: 20261021-01", "id: B7", 1) }, "YYYYMMDD"},
		{"the id disagrees with the file name", "docs/backlog/20261021-02-a-title.md", nil, "disagrees"},
		{"the file name is not an item's", "docs/backlog/notes.md", nil, "not an item file name"},
		{"the slug is not lower kebab", "docs/backlog/20261021-01-A_Title.md", nil, "not an item file name"},
		{"a status outside the three words", itemPath, func(d *string) { *d = strings.Replace(*d, "status: open", "status: deferred", 1) }, "deferred"},
		{"a date that is not a day", itemPath, func(d *string) { *d = strings.Replace(*d, "date: 2026-10-21", "date: 2026-10-21T09:00:00Z", 1) }, "YYYY-MM-DD"},
		{"filed-in outside the vocabulary", itemPath, func(d *string) { *d = strings.Replace(*d, "filed-in: hand", "filed-in: somewhere", 1) }, "filed-in"},
		{"filed-by is an email address", itemPath, func(d *string) { *d = strings.Replace(*d, "filed-by: dimipaun", "filed-by: someone@example.com", 1) }, "address"},
		{"a run with no id", itemPath, func(d *string) { *d = strings.Replace(*d, "filed-in: hand", "filed-in: run:", 1) }, "filed-in"},
		{"a required heading is missing", itemPath, func(d *string) { *d = strings.Replace(*d, "## Why\n\nbecause.\n", "", 1) }, "## Why"},
		{"a heading nobody decided", itemPath, func(d *string) { *d = strings.Replace(*d, "## Why", "## Notes", 1) }, "## Notes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.path, []byte(item(tc.mutate)))
			if err == nil {
				t.Fatalf("Parse accepted an item it must refuse")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Parse refused with %q, which does not name %q", err, tc.want)
			}
		})
	}
}

// TestParseAcceptsEachFiledInShape: the two shapes a run produces and the one
// a person does. The vocabulary is the format's, so a fourth spelling is a
// refusal rather than a value nobody can read.
func TestParseAcceptsEachFiledInShape(t *testing.T) {
	for _, want := range []string{"run:20261021-052047-fdd3", "pr:812", "hand"} {
		doc := item(func(d *string) { *d = strings.Replace(*d, "filed-in: hand", "filed-in: "+want, 1) })
		it, err := Parse(itemPath, []byte(doc))
		if err != nil {
			t.Errorf("Parse refused filed-in %q: %v", want, err)
			continue
		}
		if it.FiledIn != want {
			t.Errorf("Parse read filed-in %q, want %q", it.FiledIn, want)
		}
	}
}

// TestParseAcceptsAClosedItemAndItsClosingHeading: D31 says a `closed` item
// carries a `## Closed by` line, so the heading has to be one the format
// accepts — a parser that refused it would make the closing D31 describes
// unrepresentable, and the first item closed as FORMAT.md documents it would
// take the whole folder down with it, since Load returns on the first error.
// It is accepted and not required: requiring it of a `closed` item is the
// closing sweep's rule (plan T4.7).
//
// Mutation (verified, run and restored): drop `## Closed by` from Headings —
// this fails naming the heading.
func TestParseAcceptsAClosedItemAndItsClosingHeading(t *testing.T) {
	doc := item(func(d *string) {
		*d = strings.Replace(*d, "status: open", "status: closed", 1)
		*d += "\n## Closed by\n\npull request #812.\n"
	})
	it, err := Parse(itemPath, []byte(doc))
	if err != nil {
		t.Fatalf("Parse refused a closed item carrying D31's `## Closed by` heading: %v", err)
	}
	if it.Status != "closed" || !strings.Contains(it.Body, "## Closed by") {
		t.Errorf("Parse read %+v, want the item's own status and body", it)
	}

	// The first two headings stay required: the closing heading does not stand
	// in for the text an item has to carry.
	without := item(func(d *string) {
		*d = strings.Replace(*d, "## Why\n\nbecause.\n", "## Closed by\n\npull request #812.\n", 1)
	})
	if _, err := Parse(itemPath, []byte(without)); err == nil || !strings.Contains(err.Error(), "## Why") {
		t.Errorf("Parse accepted an item whose body carries `## Closed by` instead of `## Why`: err = %v", err)
	}
}

// TestLoadDirReadsTheItemsAndNothingElse: the index and the format document
// are not items; every other .md file in the folder is one; the result is
// sorted by id; and a folder holding two items with one id is refused, which
// is where D26's collision surfaces rather than on main.
func TestLoadDirReadsTheItemsAndNothingElse(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, doc string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	second := item(func(d *string) {
		*d = strings.Replace(*d, "id: 20261021-01", "id: 20261021-02", 1)
		*d = strings.Replace(*d, "title: a title", "title: another", 1)
	})
	write("20261021-02-another.md", second)
	write("20261021-01-a-title.md", item(nil))
	write(IndexName, "not an item: no front matter at all\n")
	write(FormatName, "not an item either\n")

	items, err := LoadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("LoadDir read %d items, want 2 (the index and the format are not items)", len(items))
	}
	if items[0].ID != "20261021-01" || items[1].ID != "20261021-02" {
		t.Errorf("LoadDir returned %s, %s: the items are sorted by id, which is to say by date", items[0].ID, items[1].ID)
	}

	// A .md file that is neither the index, the format, nor an item is a file
	// the index would silently not list.
	write("notes.md", "stray\n")
	if _, err := LoadDir(root); err == nil || !strings.Contains(err.Error(), "not an item file name") {
		t.Errorf("LoadDir accepted a stray .md file: err = %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "notes.md")); err != nil {
		t.Fatal(err)
	}

	// D26's one collision: two items with one id.
	write("20261021-02-a-title.md", item(func(d *string) { *d = strings.Replace(*d, "id: 20261021-01", "id: 20261021-02", 1) }))
	if _, err := LoadDir(root); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Errorf("LoadDir accepted two items with one id: err = %v", err)
	}
}

// TestLoadDirRefusesAMissingFolder: a folder that is not there is an error and
// not an empty backlog. The red step of the folder's own pin is exactly this
// state (plan T4.3).
func TestLoadDirRefusesAMissingFolder(t *testing.T) {
	if _, err := LoadDir(t.TempDir()); err == nil {
		t.Fatalf("LoadDir returned no error for a root with no %s", Dir)
	}
}

// TestAllocateIDTakesTheLowestFreeSequence: the id is a day plus a two-digit
// sequence allocated by scanning the folder (D26), so a second item the same
// day is -02 and a gap is filled rather than skipped.
func TestAllocateIDTakesTheLowestFreeSequence(t *testing.T) {
	at := func(id string) Item { return Item{ID: id} }
	for _, tc := range []struct {
		name  string
		items []Item
		want  string
	}{
		{"an empty day", nil, "20261021-01"},
		{"the first taken", []Item{at("20261021-01")}, "20261021-02"},
		{"a gap", []Item{at("20261021-01"), at("20261021-03")}, "20261021-02"},
		{"another day does not count", []Item{at("20261020-01")}, "20261021-01"},
		{"ten items", []Item{at("20261021-01"), at("20261021-02"), at("20261021-03"), at("20261021-04"), at("20261021-05"), at("20261021-06"), at("20261021-07"), at("20261021-08"), at("20261021-09"), at("20261021-10")}, "20261021-11"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := AllocateID(tc.items, "20261021")
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("AllocateID = %q, want %q", got, tc.want)
			}
		})
	}
	if _, err := AllocateID(nil, "2026-10-21"); err == nil {
		t.Errorf("AllocateID accepted a date that is not YYYYMMDD")
	}
	full := make([]Item, 0, 99)
	for n := 1; n <= 99; n++ {
		full = append(full, at(dayID("20261021", n)))
	}
	if _, err := AllocateID(full, "20261021"); err == nil {
		t.Errorf("AllocateID minted a 100th id for one day")
	}
}

func dayID(day string, n int) string {
	return day + "-" + string(rune('0'+n/10)) + string(rune('0'+n%10))
}
