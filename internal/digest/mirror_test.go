package digest

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestMirrorPathRoundTrips: what MirrorPath builds is what ParseMirrorPath
// reads -- the repository, the dimension and the key, for every one of the
// layout's four periods and for the project's aggregate, whose name carries no
// slug -- and the path is the folder and that name under it. One definition, so
// the step that writes the mirror and the launcher that compares the store
// against it cannot disagree about where a report lives.
func TestMirrorPathRoundTrips(t *testing.T) {
	const dir = "docs/fugaro-reviews"
	for _, key := range []string{"2026-11-18", "2026-W47", "2026-11", "2026"} {
		dim, ok := periodDim(key)
		if !ok {
			t.Fatalf("%q is not a period key this package writes", key)
		}
		for _, slug := range []string{storedSlug, ""} {
			name := MirrorName(slug, dim, key)
			if want := PeriodPath(key); slug != "" && name != slug+"/"+want {
				t.Fatalf("MirrorName(%q, %q, %q) = %q, want %q", slug, dim, key, name, slug+"/"+want)
			}
			if got := MirrorPath(dir, slug, dim, key); got != dir+"/"+name {
				t.Fatalf("MirrorPath = %q, want the folder and %q", got, name)
			}
			// The object a launch hands in is the name alone, with no folder
			// in front of it: that is what MirrorPath answers for dir "".
			if got := MirrorPath("", slug, dim, key); got != name {
				t.Fatalf("MirrorPath with no folder = %q, want %q", got, name)
			}
			gotSlug, gotDim, gotKey, ok := ParseMirrorPath(name)
			if !ok || gotSlug != slug || gotDim != dim || gotKey != key {
				t.Fatalf("ParseMirrorPath(%q) = (%q, %q, %q, %v), want (%q, %q, %q, true)", name, gotSlug, gotDim, gotKey, ok, slug, dim, key)
			}
		}
	}
}

// TestMirrorPathRefusesAKeyThatIsNoPeriod: a name is built from a (dimension,
// key) pair that is a period of this layout or from nothing at all. A key of
// another dimension's shape, a key that names no period, and a slug that is no
// storage slug answer no name -- never a path built from a key the reader would
// not read back as that period, and never the panic PeriodPath would make of a
// key shorter than its year directory.
func TestMirrorPathRefusesAKeyThatIsNoPeriod(t *testing.T) {
	for name, tc := range map[string]struct {
		slug string
		dim  Dim
		key  string
	}{
		"a month key in the day dimension": {storedSlug, ByDay, "2026-11"},
		"a week a year does not have":      {storedSlug, ByWeek, "2026-W54"},
		"a month a year does not have":     {storedSlug, ByMonth, "2026-13"},
		"a key shorter than its year":      {storedSlug, ByDay, "26-11-18"},
		"no key at all":                    {storedSlug, ByDay, ""},
		"a slug that is no storage slug":   {"acme-app", ByDay, "2026-11-18"},
		"a slug with a path in it":         {"acme/app-0123456789abcdef", ByDay, "2026-11-18"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := MirrorName(tc.slug, tc.dim, tc.key); got != "" {
				t.Errorf("MirrorName = %q, want no name", got)
			}
			if got := MirrorPath("docs/fugaro-reviews", tc.slug, tc.dim, tc.key); got != "" {
				t.Errorf("MirrorPath = %q, want no path", got)
			}
		})
	}
}

// TestParseMirrorPathRefusesANameOutsideTheShape: the check the commit step
// holds every object it was handed to before it writes anything is a shape
// check. A name it does not answer is refused, whoever built it: a file at the
// folder's root that is not a year directory (the index among them), a report
// whose year and key disagree, a name with a path in it, a hidden file, a
// deeper name, and the two shapes that are almost right.
func TestParseMirrorPathRefusesANameOutsideTheShape(t *testing.T) {
	for _, rel := range []string{
		"",
		"/2026/2026-11-18.md",
		"README.md",
		"2026-11-18.md",
		"2027/2026-11-18.md", // the year directory and the key disagree
		"2026/notes.md",
		"2026/2026-11-18.txt",
		"2026/2026-11-18.md/x",
		"acme-app-0123456789abcdef/2026/2026-11-18.md/x",
		".git/hooks/pre-commit",
		"./2026/2026-11-18.md",
		"../2026/2026-11-18.md",
		"2026/../2026-11-18.md",
		"acme-app-0123456789abcdef/../2026-11-18.md",
		"acme-app/2026/2026-11-18.md", // a slug without its hash suffix
		"acme-app-0123456789abcde/2026/2026-11-18.md",
		"acme-app-0123456789ABCDEF/2026/2026-11-18.md",
		"2026/2026/2026-11-18.md", // a year directory where the slug goes
		"acme-app-0123456789abcdef/2026/2026-11-18.md/",
	} {
		if slug, dim, key, ok := ParseMirrorPath(rel); ok {
			t.Errorf("ParseMirrorPath(%q) = (%q, %q, %q, true), want a refusal", rel, slug, dim, key)
		}
	}
	// The positive control, and the shapes the refusal list sits beside: the
	// aggregate of any of the layout's four periods parses under a year
	// directory of its own year, and a repository's report parses with its
	// slug, which is the row the index names a repository on.
	if slug, dim, key, ok := ParseMirrorPath("2026/2026-11-18.md"); !ok || slug != "" || dim != ByDay || key != "2026-11-18" {
		t.Fatalf("the aggregate's name = (%q, %q, %q, %v), want it to parse with no slug", slug, dim, key, ok)
	}
	if slug, _, _, ok := ParseMirrorPath(storedSlug + "/2026/2026-11-18.md"); !ok || slug != storedSlug {
		t.Fatalf("a repository's name does not parse: (%q, %v)", slug, ok)
	}
	if _, dim, key, ok := ParseMirrorPath("2026/2026-W47.md"); !ok || dim != ByWeek || key != "2026-W47" {
		t.Fatalf("a week's aggregate does not parse: (%q, %q, %v)", dim, key, ok)
	}
}

// TestMirrorRowsReadsTheFolder: the mirror's own reader is the folder's
// reports -- every repository the folder holds and the project's aggregates
// among them -- each row a row of the layout with its name measured from the
// folder, so the index regenerated over them names the repository of a row
// that belongs to one. A file that is no mirror name (the index, a file a
// person moved in, a report one level deeper) is no row, and a report whose
// block does not decode is a row with no figures.
func TestMirrorRowsReadsTheFolder(t *testing.T) {
	dir := t.TempDir()
	write := func(rel string, body []byte) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("README.md", []byte("the index, which is no row\n"))
	write(storedSlug+"/2026/2026-11-18.md", storedReport(t, weekBlock("2026-11-18", 5, 1, FlagHalt)))
	write(otherSlug+"/2026/2026-11-18.md", storedReport(t, weekBlock("2026-11-18", 2, 0)))
	write(otherSlug+"/2026/notes.md", []byte("a file a person put here"))
	write("2026/2026-11-18.md", storedReport(t, weekBlock("2026-11-18", 7, 0)))
	write("2026/2026-11-19.md", []byte("not a report this package wrote"))
	write("2026/2026-11-17.md", storedReport(t, weekBlock("2026-11-17", 1, 0)))
	write("2026/2026-11/sub/2026-11-18.md", []byte("deeper than the mirror goes"))

	rows, err := MirrorRows(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rows {
		rel := r.Rel
		if r.Read {
			rel += ":read"
		}
		got = append(got, rel)
	}
	want := []string{
		"2026/2026-11-17.md:read",
		// One period, three rows: the two repositories and the aggregate,
		// ordered by name, which is what tells them apart.
		"2026/2026-11-18.md:read",
		storedSlug + "/2026/2026-11-18.md:read",
		otherSlug + "/2026/2026-11-18.md:read",
		"2026/2026-11-19.md",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
	// The figures come from the file's own block, and the aggregate's row is
	// the one no repository owns.
	if r := rows[1]; r.Summary.Runs != 7 || r.Key != "2026-11-18" || r.Dim != ByDay {
		t.Errorf("the aggregate's row = %+v", r)
	}
	if r := rows[2]; r.Summary.Runs != 5 || r.Summary.Halts != 1 {
		t.Errorf("the repository's row = %+v", r)
	}
	// A folder that is not there holds no reports: the first run into a fresh
	// folder reads it without a word.
	empty, err := MirrorRows(filepath.Join(dir, "not-there"))
	if err != nil || len(empty) != 0 {
		t.Fatalf("a folder that is not there = %v, %v, want no rows and no error", empty, err)
	}
}

// TestMirrorRowsRefusesASymlink: the mirror is read and written by the commit
// step, so a link under it is refused rather than followed -- the folder is the
// repository's to shape, and a link is how a report would be read from, or
// written to, somewhere no report run may reach.
func TestMirrorRowsRefusesASymlink(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "outside.md")
	if err := os.WriteFile(outside, storedReport(t, weekBlock("2026-11-18", 1, 0)), 0o644); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(dir, "docs", "fugaro-reviews", "2026")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(inside, "2026-11-18.md")); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}
	_, err := MirrorRows(filepath.Join(dir, "docs", "fugaro-reviews"))
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("MirrorRows = %v, want a refusal naming the link", err)
	}
}
