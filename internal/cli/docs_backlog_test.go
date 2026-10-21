package cli

// docs/backlog/ is this repository's one backlog (rigor-modes, the owner's
// ruling of 2026-10-20: D26–D32, plan T4.3 and T4.4).
//
// One Markdown file per item, named `<id>-<slug>.md`, so that two pull
// requests filing an item at once write two differently named files and the
// merge is a union rather than a conflict (D26); and one generated index,
// docs/backlog/README.md, which a small tool regenerates and a test pins as
// current (D28). This file holds the ITEMS — the folder's format and the files
// that follow it. The index's two guards are docs_backlog_index_test.go's.
//
// There is no registry test over docs/ in this package: nothing pins the set
// of docs folders and nothing pins a folder's documents on its behalf, so a
// folder that wants its documents pinned writes its own file — this one, the
// same convention docs_rigor_modes_test.go states for docs/rigor-modes/ and
// docs_in_job_review_test.go for docs/in-job-review/.
//
// The items are held by this file reading the bytes: it parses the front
// matter itself and asserts the eight keys D27 names, the four body headings,
// the three-word status vocabulary and the id/file-name agreement, so a format
// rule is checked against the document rather than against the parser that
// happens to implement it. It also asserts internal/backlog's own Parse accepts
// every file, which is the other direction: a file the folder's pin reads as
// well-formed and the tool refuses is a file `fugaro backlog list` cannot show.

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backlog"
)

const (
	// backlogDir is the folder the items live in, relative to the repository
	// root, and backlogFormat the hand-written document stating the format.
	// README.md, the folder's third file, is generated, and is held by
	// docs_backlog_index_test.go.
	backlogDir    = "docs/backlog"
	backlogFormat = "docs/backlog/FORMAT.md"
)

// backlogItemNameRE is D26's file name: the id (the day an item was filed and
// a two-digit sequence that day) and a lower-kebab slug from the title.
var backlogItemNameRE = regexp.MustCompile(`^([0-9]{8}-[0-9]{2})-[a-z0-9]+(?:-[a-z0-9]+)*\.md$`)

// backlogDateRE is D27's `date`: the record's own finish time in UTC, as a
// day. It is a date and never a timestamp, so the index cannot carry a figure
// that moves between two regenerations of an unchanged folder.
var backlogDateRE = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)

// backlogIDRE is D26's id: eight digits, a dash, two digits.
var backlogIDRE = regexp.MustCompile(`^[0-9]{8}-[0-9]{2}$`)

// backlogItemFiles returns the folder's item files — every .md file in it
// except the generated index and the hand-written format — read off the tree
// rather than from a list kept here, so an item filed tomorrow is covered
// tomorrow. A folder that does not exist is an error, not an empty result:
// the red step of plan T4.3 is exactly that state.
func backlogItemFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join("..", "..", backlogDir))
	if err != nil {
		t.Fatalf("%s: %v (the folder, its format and its first items are plan T4.3)", backlogDir, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		if e.Name() == "README.md" || e.Name() == "FORMAT.md" {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// backlogFrontMatter returns the item's front matter as the file itself
// spells it: the `key: value` lines between the opening and closing `---`
// fences, read here rather than through internal/backlog's parser, so the
// assertions below are about the document and not about the code that reads
// it.
func backlogFrontMatter(t *testing.T, name, doc string) map[string]string {
	t.Helper()
	lines := strings.Split(doc, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r") != "---" {
		t.Fatalf("%s: the file does not open with a `---` front-matter fence", name)
	}
	out := map[string]string{}
	for i := 1; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\r")
		if line == "---" {
			return out
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			t.Fatalf("%s: front-matter line %q is not `key: value`", name, line)
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if _, dup := out[key]; dup {
			t.Fatalf("%s: front matter states %q twice", name, key)
		}
		out[key] = value
	}
	t.Fatalf("%s: the front matter is never closed with a `---` fence", name)
	return nil
}

// backlogBodyHeadings returns the H2 headings of the text after the front
// matter, in order.
func backlogBodyHeadings(doc string) []string {
	var out []string
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(strings.TrimRight(line, "\r"), "## ") {
			out = append(out, strings.TrimRight(line, "\r"))
		}
	}
	return out
}

// TestDocsBacklogItemsFollowTheFormat is the folder's own pin on the items:
// every file beside FORMAT.md and README.md is one item, named as D26 names
// it, carrying the eight front-matter fields D27 requires and no ninth, a
// status from D27's three-word vocabulary, and the body headings the format
// names with the first two required. Written red first, against a folder that
// did not exist (plan T4.3's red step), and independent of the tool: the
// parser's own acceptance is asserted separately, below.
func TestDocsBacklogItemsFollowTheFormat(t *testing.T) {
	names := backlogItemFiles(t)
	if len(names) == 0 {
		t.Fatalf("%s holds no item file: the folder, its format and its first items are plan T4.3", backlogDir)
	}
	seen := map[string]string{}
	for _, name := range names {
		path := backlogDir + "/" + name
		m := backlogItemNameRE.FindStringSubmatch(name)
		if m == nil {
			t.Errorf("%s: %q is not an item file name: one file per item, `<id>-<slug>.md`, with a lower-kebab slug (D26)", path, name)
			continue
		}
		doc := readDoc(t, path)
		fm := backlogFrontMatter(t, path, doc)

		// The eight fields, exactly: a missing one is a field a reader cannot
		// trust, and a ninth is a field nobody decided.
		for _, key := range backlog.Fields {
			if _, ok := fm[key]; !ok {
				t.Errorf("%s: the front matter does not carry the required field %q (D27)", path, key)
			}
		}
		for key := range fm {
			if !slices.Contains(backlog.Fields, key) {
				t.Errorf("%s: the front matter carries %q, which is not one of D27's eight fields", path, key)
			}
		}
		for key, value := range fm {
			if value == "" {
				t.Errorf("%s: the front matter's %q is empty: every one of the eight is required (D27)", path, key)
			}
		}

		// The id and the file name are checked against each other, so an id
		// resolves to a path without a lookup (D26).
		if id := fm["id"]; id != "" {
			if !backlogIDRE.MatchString(id) {
				t.Errorf("%s: id %q is not `<YYYYMMDD>-<NN>` (D26)", path, id)
			}
			if id != m[1] {
				t.Errorf("%s: id %q disagrees with the file name's first two segments %q: the name and the field are checked against each other (D26)", path, id, m[1])
			}
			if prev, dup := seen[id]; dup {
				t.Errorf("%s: id %q is already used by %s: two items may not share an id (D26)", path, id, prev)
			}
			seen[id] = path
		}

		// D27's status is three words and no fourth, its date is a day, and its
		// filed-in is one of the three things that can have filed an item.
		if status := fm["status"]; status != "" && !slices.Contains(backlog.Statuses, status) {
			t.Errorf("%s: status %q is not one of %s (D27)", path, status, strings.Join(backlog.Statuses, ", "))
		}
		if date := fm["date"]; date != "" && !backlogDateRE.MatchString(date) {
			t.Errorf("%s: date %q is not `YYYY-MM-DD`: the date is the record's own finish time, never a clock read at regeneration (D27)", path, date)
		}
		if in := fm["filed-in"]; in != "" && !backlogFiledIn(in) {
			t.Errorf("%s: filed-in %q is none of %s (D27)", path, in, strings.Join(backlog.FiledInKinds, ", "))
		}

		// The body's headings are the four the format names, and the first two
		// are required, so an item cannot be a title with no text (D27).
		headings := backlogBodyHeadings(doc)
		have := map[string]bool{}
		for _, h := range headings {
			have[h] = true
			if !slices.Contains(backlog.Headings, h) {
				t.Errorf("%s: the body carries the heading %q, which is not one of the four the format names (D27)", path, h)
			}
		}
		for _, want := range backlog.Headings[:2] {
			if !have[want] {
				t.Errorf("%s: the body does not carry the required heading %q (D27)", path, want)
			}
		}
		if len(headings) == 0 {
			t.Errorf("%s: the body carries no heading at all", path)
		}

		// The other direction: the tool the folder ships has to accept the
		// same bytes. A file this test reads as well-formed and Parse refuses
		// is a file `fugaro backlog list` cannot show.
		if _, err := backlog.Parse(path, []byte(doc)); err != nil {
			t.Errorf("%s: internal/backlog.Parse refuses an item the folder's format accepts: %v", path, err)
		}
	}
}

// backlogFiledIn reports whether a `filed-in` value is one the format names:
// `hand`, or one of the kinds with a value after the colon.
func backlogFiledIn(value string) bool {
	if value == "hand" {
		return true
	}
	for _, kind := range backlog.FiledInKinds {
		if strings.HasPrefix(value, kind+":") && len(value) > len(kind)+1 {
			return true
		}
	}
	return false
}

// TestDocsBacklogFolderStatesItsFormat: the folder carries FORMAT.md, and it
// states the four things the plan's T4.3 asks it to state — the front-matter
// table, the id rule, the body headings and the closing rule (D26, D27, D31)
// — by name, so a reader who meets an item has the rule it follows beside it.
func TestDocsBacklogFolderStatesItsFormat(t *testing.T) {
	doc := readDoc(t, backlogFormat)
	for _, want := range []string{
		"<id>-<slug>.md",
		"YYYYMMDD-NN",
		"## What was left open",
		"## Why",
		"## What done looks like",
		"## Links",
		"open",
		"closed",
		"dropped",
		"## Closed by",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("%s does not state %q: the format document is where the rule an item follows is written down (D26, D27, D31)", backlogFormat, want)
		}
	}
	for _, key := range backlog.Fields {
		if !strings.Contains(doc, "`"+key+"`") {
			t.Errorf("%s does not name the front-matter field %q (D27)", backlogFormat, key)
		}
	}
}
