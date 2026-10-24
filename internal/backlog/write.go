package backlog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// Filing an item (docs/rigor-modes/implementation-plan.md T4.5, D9, D26–D28):
// the one place an item file is written.
//
// The folder allocates the id rather than the caller, because D26 makes the id
// the file's name and the folder's own state is the only place the day's
// sequence is known. Filing also regenerates the index, so the commit that adds
// an item is the commit that regenerates it (D28) — a filed item the index does
// not list is the drift the folder's two guards exist to catch, and a filing
// command is the one caller that can keep them green without a person
// remembering to run a second command.
//
// It reads no clock: the day an item is filed is given to it (D26's id is the
// day the item was filed, and D27's `date` is that day), so the same filing on
// the same folder is the same bytes on every machine, and a caller that wants
// "today" says so.

// MaxSlugRunes bounds the slug half of a file name. An item's title is a
// filer's own words and a file name is what a reader has to live with, so the
// slug is cut to a length a listing can show rather than the title's own.
const MaxSlugRunes = 60

// New is an item a caller is filing: D27's eight fields less the id, which the
// folder allocates, and less the status, which is always `open` — an item is
// filed open and closed by the pull request that fixes it (D31).
type New struct {
	Title   string // the filer's words, one line
	Mode    string // the resolved mode a run ran under; "none" for a hand-filed item
	Area    string // the matched areas, strictest first, comma-separated; "project" or "repo" for a whole layer
	FiledBy string // the run's launcher, or the filer's login
	FiledIn string // "run:<run id>", "pr:<number>" or "hand"
	Date    string // the day it was filed, as YYYY-MM-DD
	Body    string // the body, its required headings included (Body renders one)
}

// Write files one item in dir — the folder, not the repository root — and
// regenerates the index beside it. The folder is created when it is not there:
// a repository whose first item is filed by a run has no docs/backlog/ yet, and
// a filing command that refused to make it would make the first item of every
// repository a manual step.
//
// Every rule the format states is checked before anything is written, so a
// filing that cannot become an item the folder accepts leaves the folder exactly
// as it was. What is written is parsed again — the file, not the struct — so a
// written item is by construction one Parse and the folder's pin both accept.
func Write(dir string, n New) (Item, error) {
	if err := n.check(); err != nil {
		return Item{}, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Item{}, fmt.Errorf("create %s: %w", dir, err)
	}
	items, err := Load(dir)
	if err != nil {
		return Item{}, err
	}
	slug, err := Slug(n.Title)
	if err != nil {
		return Item{}, err
	}
	id, err := AllocateID(items, strings.ReplaceAll(n.Date, "-", ""))
	if err != nil {
		return Item{}, err
	}
	name := id + "-" + slug + ".md"
	itemPath := filepath.Join(dir, name)
	// The same title filed twice on one day is the one filing the folder
	// refuses: it is the accidental double-submit, and both files would say
	// the same thing about the same day. The same title on another day is not
	// that — an item is filed by the run that left it open, two runs a week
	// apart can leave the same thing open, and each item carries its own
	// run and its own date. The id is what must not repeat, and AllocateID
	// has already made sure of that (D26).
	for _, it := range items {
		if itemSlug(it) == slug && it.Date == n.Date {
			return Item{}, fmt.Errorf("%s: %s already holds this title for %s: the same item filed twice on one day is two files saying one thing (D26)", itemPath, it.Path, it.Date)
		}
	}
	doc := render(n, id)
	item, err := Parse(itemPath, []byte(doc))
	if err != nil {
		return Item{}, err
	}
	if err := os.WriteFile(itemPath, []byte(doc), 0o644); err != nil {
		return Item{}, fmt.Errorf("write %s: %w", itemPath, err)
	}
	items = append(items, item)
	indexPath := filepath.Join(dir, IndexName)
	if err := os.WriteFile(indexPath, []byte(Render(items)), 0o644); err != nil {
		return Item{}, fmt.Errorf("write %s: %w", indexPath, err)
	}
	return item, nil
}

// render is the item file's bytes: the `---` fenced front matter in the order
// the format lists the fields, then the body.
func render(n New, id string) string {
	var b strings.Builder
	b.WriteString("---\n")
	for _, f := range []struct{ key, value string }{
		{"id", id}, {"title", n.Title}, {"mode", n.Mode}, {"area", n.Area},
		{"status", "open"}, {"filed-by", n.FiledBy}, {"filed-in", n.FiledIn}, {"date", n.Date},
	} {
		fmt.Fprintf(&b, "%s: %s\n", f.key, oneLine(f.value))
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimRight(n.Body, "\n"))
	b.WriteString("\n")
	return b.String()
}

// Body renders an item's body from the parts the format names. The first two
// headings are required — an item is not a title with no text (D27) — and the
// last two are written when the filer has them.
func Body(what, why, done string, links []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## What was left open\n\n%s\n\n## Why\n\n%s\n", strings.TrimSpace(what), strings.TrimSpace(why))
	if done = strings.TrimSpace(done); done != "" {
		fmt.Fprintf(&b, "\n## What done looks like\n\n%s\n", done)
	}
	if len(links) > 0 {
		b.WriteString("\n## Links\n\n")
		for _, l := range links {
			fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(l))
		}
	}
	return b.String()
}

// check holds a filing to the format's own rules before anything is written.
// The front matter is one line per field by construction, so a value carrying a
// line break is a value that would forge a field and is refused rather than
// flattened.
func (n New) check() error {
	switch {
	case strings.TrimSpace(n.Title) == "":
		return fmt.Errorf("an item needs a title: the title is the item's one line (D27)")
	case strings.ContainsFunc(n.Title, unicode.IsControl):
		return fmt.Errorf("the title carries a control character: it is one line (D27)")
	}
	if _, err := Slug(n.Title); err != nil {
		return err
	}
	for _, f := range []struct{ key, value string }{
		{"mode", n.Mode}, {"area", n.Area}, {"filed-by", n.FiledBy},
	} {
		switch {
		case strings.TrimSpace(f.value) == "":
			return fmt.Errorf("an item needs a %s: every one of D27's eight fields is required", f.key)
		case strings.ContainsFunc(f.value, unicode.IsControl):
			return fmt.Errorf("the %s carries a control character: it is one line (D27)", f.key)
		}
	}
	if !dateRE.MatchString(n.Date) {
		return fmt.Errorf("date %q is not `YYYY-MM-DD` (D27)", n.Date)
	}
	if n.FiledIn != "hand" && !filedInRE.MatchString(n.FiledIn) {
		return fmt.Errorf("filed-in %q is none of %s (D27)", n.FiledIn, strings.Join(FiledInKinds, ", "))
	}
	for _, want := range requiredHeadings {
		text, ok := section(n.Body, want)
		if !ok {
			return fmt.Errorf("the body does not carry the required heading %q (D27)", want)
		}
		if text == "" {
			return fmt.Errorf("the body's %q says nothing: an item is not a title with no text (D27)", want)
		}
	}
	return nil
}

// section returns the text under one heading, up to the next one, and whether
// the heading is there at all.
func section(body, heading string) (string, bool) {
	lines := strings.Split(body, "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimRight(line, "\r") == heading {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return "", false
	}
	var kept []string
	for _, line := range lines[start:] {
		if strings.HasPrefix(strings.TrimRight(line, "\r"), "## ") {
			break
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n")), true
}

// Slug is D26's slug: lower-kebab words from the title. A title with no word in
// it — punctuation, or a script this rule cannot pass through — is refused
// rather than filed under an empty name, because the slug is what a reader
// reads in a listing and an empty one is a file nobody can cite.
func Slug(title string) (string, error) {
	var b strings.Builder
	lastDash := true // leading punctuation must not open with a dash
	for _, c := range strings.ToLower(title) {
		switch {
		case c >= 'a' && c <= 'z' || c >= '0' && c <= '9':
			b.WriteRune(c)
			lastDash = false
		case !lastDash:
			b.WriteRune('-')
			lastDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "", fmt.Errorf("title %q has no word to make a slug from (D26)", title)
	}
	if r := []rune(slug); len(r) > MaxSlugRunes {
		slug = strings.Trim(strings.TrimRight(string(r[:MaxSlugRunes]), "-"), "-")
	}
	return slug, nil
}

// itemSlug is the slug half of an item file's name, which is what a duplicate
// filing is recognised by: the id carries the day and the day's sequence, so
// two filings of one title the same day differ in their ids and agree in their
// slugs, while the same title on another day is another item.
func itemSlug(it Item) string {
	name := strings.TrimSuffix(itemName(it.Path), ".md")
	return strings.TrimPrefix(name, it.ID+"-")
}

// oneLine flattens a value to one line, so a field can carry no second line.
// check has already refused a control character in every value a filer writes;
// this is the belt for the ones the refresh paths compose.
func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\r", " ")), " ")
}
