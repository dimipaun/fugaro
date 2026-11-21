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
		"level: none\n" +
		"kind: deferred\n" +
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

// TestParseAcceptsAWellFormedItem is the parser's baseline: the nine fields,
// the three-word status, the two-word kind, the id agreeing with the file name,
// and the two required headings are all it takes.
func TestParseAcceptsAWellFormedItem(t *testing.T) {
	it, err := Parse(itemPath, []byte(item(nil)))
	if err != nil {
		t.Fatalf("Parse refused a well-formed item: %v", err)
	}
	if it.ID != "20261021-01" || it.Title != "a title" || it.Level != "none" ||
		it.Kind != "deferred" || it.Area != "repo" || it.Status != "open" ||
		it.FiledBy != "dimipaun" || it.FiledIn != "hand" || it.Date != "2026-10-21" {
		t.Errorf("Parse read %+v, want the file's own nine fields", it)
	}
	if it.Path != itemPath {
		t.Errorf("Parse read Path %q, want %q", it.Path, itemPath)
	}
	if !strings.Contains(it.Body, "## Why") {
		t.Errorf("Parse's Body does not carry the text after the front matter: %q", it.Body)
	}
}

// TestParseRefusesEveryWayAnItemCanBeWrong is the format, one refusal at a
// time: D27's nine fields (missing, unknown, empty, duplicated), D26's id and
// its agreement with the file name, the three-word status, LD5's two-word kind,
// the day-shaped date, `filed-in`'s vocabulary, a filed-by that is an address,
// and D27's body headings.
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
		{"the kind is missing", itemPath, func(d *string) { *d = strings.Replace(*d, "kind: deferred\n", "", 1) }, `"kind"`},
		{"a field is empty", itemPath, func(d *string) { *d = strings.Replace(*d, "title: a title", "title:", 1) }, `"title" is empty`},
		{"a field is repeated", itemPath, func(d *string) { *d = strings.Replace(*d, "area: repo\n", "area: repo\narea: repo\n", 1) }, "twice"},
		{"a tenth field", itemPath, func(d *string) { *d = strings.Replace(*d, "date: 2026-10-21", "date: 2026-10-21\nseverity: high", 1) }, `"severity"`},
		{"a line that is not key: value", itemPath, func(d *string) { *d = strings.Replace(*d, "title: a title", "title a title", 1) }, "key: value"},
		{"the id is not a date and a sequence", itemPath, func(d *string) { *d = strings.Replace(*d, "id: 20261021-01", "id: B7", 1) }, "YYYYMMDD"},
		{"the id disagrees with the file name", "docs/backlog/20261021-02-a-title.md", nil, "disagrees"},
		{"the file name is not an item's", "docs/backlog/notes.md", nil, "not an item file name"},
		{"the slug is not lower kebab", "docs/backlog/20261021-01-A_Title.md", nil, "not an item file name"},
		{"a status outside the three words", itemPath, func(d *string) { *d = strings.Replace(*d, "status: open", "status: deferred", 1) }, "deferred"},
		{"a kind outside the two", itemPath, func(d *string) { *d = strings.Replace(*d, "kind: deferred", "kind: tolerated", 1) }, "open-risk"},
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

// TestParseAcceptsEachKind: LD5's two kinds are the vocabulary, and each is a
// value the parser reads back — the open risk is what makes Critical's own
// filing representable beside a tolerated finding.
func TestParseAcceptsEachKind(t *testing.T) {
	for _, want := range Kinds {
		doc := item(func(d *string) { *d = strings.Replace(*d, "kind: deferred", "kind: "+want, 1) })
		it, err := Parse(itemPath, []byte(doc))
		if err != nil {
			t.Errorf("Parse refused kind %q: %v", want, err)
			continue
		}
		if it.Kind != want {
			t.Errorf("Parse read kind %q, want %q", it.Kind, want)
		}
	}
}

// TestParseReadsTheReleasedLevelKey: a folder filed by a released version
// states its level as `mode` and carries no `kind` at all, and an upgrade must
// not make it unreadable — `fugaro update` folds such a folder into the
// project's backlog, and `fugaro backlog index` regenerates its index still, so
// a refusal here would leave the person hand-editing every item, a repair no
// command performs (v0.26.0's release notes, item 19). The item is read as
// what it was: the level it states, and the deferral a folder of that age could
// carry.
//
// Mutation (verified, run and restored): the fallback is what both halves need
// — dropping it refuses the item for a missing `level`, and accepting the
// released key without reading it refuses it as a tenth field.
func TestParseReadsTheReleasedLevelKey(t *testing.T) {
	doc := item(func(d *string) {
		*d = strings.Replace(*d, "level: none\nkind: deferred\n", "mode: pro\n", 1)
	})
	it, err := Parse(itemPath, []byte(doc))
	if err != nil {
		t.Fatalf("Parse refused an item a released version filed: %v", err)
	}
	if it.Level != "pro" {
		t.Errorf("Parse read the level as %q, want the released `mode` value %q", it.Level, "pro")
	}
	if it.Kind != kindDeferred {
		t.Errorf("Parse read the kind as %q, want %q: an item filed before LD5 carries no kind, and what it left open it left as a deferral", it.Kind, kindDeferred)
	}
}

// TestParseLetsTheLevelKeyDecideOverTheReleasedOne: `level` is the format's key
// and `mode` its released spelling, so an item carrying both is read by `level`
// (the released key is a fallback, never an override) — and a kind the item
// states is the item's own, whatever else it carries. The pair is what makes
// the fallback safe for a folder that was migrated by hand before an upgrade
// read it: the migration's `level` wins over the `mode` left beside it.
//
// Mutation (verified, run and restored): setting the level from the released
// key unconditionally reads `poc` here where the item's own `level` says
// `critical`.
func TestParseLetsTheLevelKeyDecideOverTheReleasedOne(t *testing.T) {
	doc := item(func(d *string) {
		*d = strings.Replace(*d, "level: none\nkind: deferred\n", "level: critical\nmode: poc\nkind: open-risk\n", 1)
	})
	it, err := Parse(itemPath, []byte(doc))
	if err != nil {
		t.Fatalf("Parse refused an item carrying both spellings of the level: %v", err)
	}
	if it.Level != "critical" {
		t.Errorf("Parse read the level as %q, want %q: `level` decides where an item carries both", it.Level, "critical")
	}
	if it.Kind != "open-risk" {
		t.Errorf("Parse read the kind as %q, want %q: a kind the item states is the item's own", it.Kind, "open-risk")
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
