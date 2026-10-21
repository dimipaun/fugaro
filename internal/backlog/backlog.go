// Package backlog reads the repository's backlog: one Markdown file per item
// under docs/backlog/, and the index a small tool regenerates from them.
//
// The shape is the owner's ruling of 2026-10-20 (docs/rigor-modes/, D26–D32).
// An item is a file named `<id>-<slug>.md`, where the id is the day the item
// was filed and a two-digit sequence that day — so two pull requests filing
// an item at once write two differently named files and the merge is a union
// of disjoint files rather than a conflict (D26). The file carries eight
// front-matter fields, each required, and a body whose headings are named
// (D27). The index is derived state: it is regenerated, never merged by hand,
// and two branches that regenerated it at once are reconciled by re-running
// the command (D28).
//
// Nothing here reads a clock, a network or an environment: every date comes
// from the item file that carries it, so the same folder renders to the same
// bytes and a regeneration in review diffs only where an item changed (D27's
// reason for taking the date from the record rather than from `time.Now`).
package backlog

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	// Dir is the folder the items live in, relative to a repository root.
	Dir = "docs/backlog"
	// IndexName is the generated index inside Dir. It is derived state and is
	// never hand-edited (D28).
	IndexName = "README.md"
	// FormatName is the hand-written document stating the format an item
	// follows, inside Dir. It is not an item and carries no front matter.
	FormatName = "FORMAT.md"
)

// Fields are the eight front-matter keys D27 requires, in the order the
// format document states them. Each is required, and a ninth is a field
// nobody decided: Parse refuses both.
var Fields = []string{
	"id", "title", "mode", "area", "status", "filed-by", "filed-in", "date",
}

// Statuses are D27's three words for `status`, and there is no fourth.
var Statuses = []string{"open", "closed", "dropped"}

// FiledInKinds are the three things that can have filed an item. D27 names
// the two a run produces — `run:<run id>`, and the `pr:<number>` of the pull
// request the run opened — and `hand` is the third: an item a person files has
// no run and no recorded pull request behind it, and that state is said out
// loud rather than spelled as a run id nobody minted.
var FiledInKinds = []string{"run", "pr", "hand"}

// Headings are the body headings D27 names. The first two are required, so an
// item cannot be a title with no text; a heading outside this set is refused,
// because the body's shape is the format and not a preference.
var Headings = []string{
	"## What was left open",
	"## Why",
	"## What done looks like",
	"## Links",
}

// requiredHeadings is the prefix of Headings that an item's body must carry.
var requiredHeadings = Headings[:2]

var (
	// idRE is D26's id: the day an item was filed, a dash, a two-digit
	// sequence that day.
	idRE = regexp.MustCompile(`^[0-9]{8}-[0-9]{2}$`)
	// dateRE is D27's `date`: a UTC day, never a timestamp.
	dateRE = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	// itemNameRE is D26's file name, and it is where an item's id comes from:
	// the first two dash-separated segments.
	itemNameRE = regexp.MustCompile(`^([0-9]{8}-[0-9]{2})-[a-z0-9]+(?:-[a-z0-9]+)*\.md$`)
	// dayRE is the `YYYYMMDD` half of an id, which is what AllocateID
	// allocates a sequence for.
	dayRE = regexp.MustCompile(`^[0-9]{8}$`)
	// filedInRE is D27's two run-produced shapes: a kind, a colon and a
	// non-empty value. `hand` carries no value and is checked separately.
	filedInRE = regexp.MustCompile(`^(run|pr):(.+)$`)
)

// Item is one backlog item: its eight front-matter fields, the path it was
// read from and its body.
type Item struct {
	ID      string // D26's id, and the file name's first two segments.
	Title   string // the filer's words, one line.
	Mode    string // the resolved mode a run ran under; "none" for a hand-filed item.
	Area    string // the matched areas, strictest first, comma-separated; "project" or "repo" for a whole layer.
	Status  string // one of Statuses.
	FiledBy string // the run's launcher, or the filer's login.
	FiledIn string // "run:<id>" or "pr:<number>".
	Date    string // the record's finish time in UTC, as YYYY-MM-DD.
	Path    string // the item's path, repository-relative.
	Body    string // everything after the front matter.
}

// Parse reads one item from data, whose path is the repository-relative path
// it was read from — the path is what the id is checked against, so an id
// resolves to a file without a lookup (D26).
//
// Every rule the format states is a refusal here rather than a warning: a
// missing or unknown field, an empty value, a status outside the three words,
// a date that is not a day, an id that disagrees with the file name, a
// duplicate field and a body missing a required heading. An item the folder's
// pin accepts and this refuses would be one `fugaro backlog list` cannot show,
// which is why the two are checked against each other.
func Parse(path string, data []byte) (Item, error) {
	name := itemName(path)
	m := itemNameRE.FindStringSubmatch(name)
	if m == nil {
		return Item{}, fmt.Errorf("%s: not an item file name: one file per item, `<id>-<slug>.md`, with a lower-kebab slug (D26)", path)
	}
	item, body, err := parseFrontMatter(path, string(data))
	if err != nil {
		return Item{}, err
	}
	item.Path = path
	item.Body = body

	if item.ID != m[1] {
		return Item{}, fmt.Errorf("%s: id %q disagrees with the file name's first two segments %q (D26)", path, item.ID, m[1])
	}
	if !contains(Statuses, item.Status) {
		return Item{}, fmt.Errorf("%s: status %q is not one of %s (D27)", path, item.Status, strings.Join(Statuses, ", "))
	}
	if !dateRE.MatchString(item.Date) {
		return Item{}, fmt.Errorf("%s: date %q is not `YYYY-MM-DD` (D27)", path, item.Date)
	}
	if item.FiledIn != "hand" && !filedInRE.MatchString(item.FiledIn) {
		return Item{}, fmt.Errorf("%s: filed-in %q is none of %s (D27)", path, item.FiledIn, strings.Join(FiledInKinds, ", "))
	}
	if err := checkBody(path, body); err != nil {
		return Item{}, err
	}
	return item, nil
}

// parseFrontMatter splits the `---` fenced front matter from the body, and
// holds the eight fields to D27's list: each present, each non-empty, each
// once, and no ninth.
func parseFrontMatter(path, doc string) (Item, string, error) {
	lines := strings.Split(doc, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r") != "---" {
		return Item{}, "", fmt.Errorf("%s: the file does not open with a `---` front-matter fence (D27)", path)
	}
	values := map[string]string{}
	closed := -1
	for i := 1; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\r")
		if line == "---" {
			closed = i
			break
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return Item{}, "", fmt.Errorf("%s: front-matter line %d (%q) is not `key: value`", path, i+1, line)
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !contains(Fields, key) {
			return Item{}, "", fmt.Errorf("%s: front matter carries %q, which is not one of D27's eight fields", path, key)
		}
		if _, dup := values[key]; dup {
			return Item{}, "", fmt.Errorf("%s: front matter states %q twice", path, key)
		}
		if value == "" {
			return Item{}, "", fmt.Errorf("%s: front matter's %q is empty: every one of the eight is required (D27)", path, key)
		}
		values[key] = value
	}
	if closed < 0 {
		return Item{}, "", fmt.Errorf("%s: the front matter is never closed with a `---` fence (D27)", path)
	}
	for _, key := range Fields {
		if _, ok := values[key]; !ok {
			return Item{}, "", fmt.Errorf("%s: the front matter does not carry the required field %q (D27)", path, key)
		}
	}
	item := Item{
		ID: values["id"], Title: values["title"], Mode: values["mode"],
		Area: values["area"], Status: values["status"], FiledBy: values["filed-by"],
		FiledIn: values["filed-in"], Date: values["date"],
	}
	if !idRE.MatchString(item.ID) {
		return Item{}, "", fmt.Errorf("%s: id %q is not `<YYYYMMDD>-<NN>` (D26)", path, item.ID)
	}
	return item, strings.Join(lines[closed+1:], "\n"), nil
}

// checkBody holds the body to D27's headings: the first two are required, and
// a heading outside the four the format names is a shape nobody decided.
func checkBody(path, body string) error {
	have := map[string]bool{}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasPrefix(line, "## ") {
			continue
		}
		if !contains(Headings, line) {
			return fmt.Errorf("%s: the body carries the heading %q, which is not one of the four the format names (D27)", path, line)
		}
		have[line] = true
	}
	for _, want := range requiredHeadings {
		if !have[want] {
			return fmt.Errorf("%s: the body does not carry the required heading %q (D27)", path, want)
		}
	}
	return nil
}

// LoadDir reads every item in root's backlog folder, sorted by id, and refuses
// a folder holding two items with one id (D26): the collision D26's naming
// rule leaves is a rename on the branch that would merge second, and this is
// where it surfaces rather than on main.
//
// The index and the format document are not items. A .md file that is neither
// of them has to be an item — a stray document in the folder would otherwise
// be a file the index silently does not list, which is the one thing the
// folder's two guards exist to catch.
func LoadDir(root string) ([]Item, error) {
	return Load(filepath.Join(root, Dir))
}

// Load reads every item in the folder dir. It is LoadDir's body: the folder is
// a parameter there so a caller that is not standing at a repository root —
// `fugaro backlog list --dir`, and the folder's own guards over a synthetic
// tree — reads the same way.
func Load(dir string) ([]Item, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var items []Item
	seen := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") || name == IndexName || name == FormatName {
			continue
		}
		itemPath := filepath.Join(dir, name)
		data, err := os.ReadFile(itemPath)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", itemPath, err)
		}
		item, err := Parse(itemPath, data)
		if err != nil {
			return nil, err
		}
		if prev, dup := seen[item.ID]; dup {
			return nil, fmt.Errorf("%s: id %q is already used by %s: two items may not share an id (D26)", item.Path, item.ID, prev)
		}
		seen[item.ID] = item.Path
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

// AllocateID returns the id an item filed on date (a `YYYYMMDD` day) takes:
// the lowest two-digit sequence that day not already used by items. It scans
// the folder's own state rather than a counter nobody owns, which is why D26
// chose a date-plus-sequence name: two filers on two branches collide on one
// id at worst, and the collision is a rename.
func AllocateID(items []Item, date string) (string, error) {
	if !dayRE.MatchString(date) {
		return "", fmt.Errorf("id date %q is not `YYYYMMDD` (D26)", date)
	}
	used := map[string]bool{}
	for _, it := range items {
		used[it.ID] = true
	}
	for n := 1; n <= 99; n++ {
		id := fmt.Sprintf("%s-%02d", date, n)
		if !used[id] {
			return id, nil
		}
	}
	return "", fmt.Errorf("every sequence for %s is taken: the day holds 99 items (D26)", date)
}

// itemName is a path's last element.
func itemName(p string) string {
	return path.Base(strings.ReplaceAll(p, "\\", "/"))
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
