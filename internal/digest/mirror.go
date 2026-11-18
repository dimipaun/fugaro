package digest

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dimipaun/fugaro/internal/task"
)

// The mirror: the committed copy of the stored reports (E5, D2750). A project
// that designates a repository and a folder commits the store's documents into
// it, and the folder's shape is this file's -- the layout's own periods, one
// level deeper for a repository's report.
//
//   - `<dir>/<slug>/<YYYY>/<key>.md` is one repository's report, where slug is
//     the storage slug every object of that repository is addressed by
//     (task.Slug) and the rest is D321's own path (layout.go's PeriodPath).
//   - `<dir>/<YYYY>/<key>.md` is the project's aggregate (R8), the one document
//     that is no repository's and names every one of them.
//   - `<dir>/README.md` is the index, regenerated over the whole folder by the
//     layout's own IndexMarkdown.
//
// One definition, two readers: the run's commit-reports step writes the folder
// through it (internal/runner/reports_commit.go) and the launcher that hands
// the store's bytes in compares the store against the folder through it (the
// report object's own name is a mirror-relative path). A third writer of a
// repository's own folder is not what this is: D321's tree under a checkout's
// digest.Dir holds one repository's reports with no slug in it, and its two
// writers keep producing it byte for byte (layout.go).
//
// The rows below are the layout's own Row, with Rel measured from the folder,
// so IndexMarkdown names a report's file and -- on a row that carries a slug --
// the repository it belongs to, without the layout knowing a mirror exists.

// MirrorName is one report's name in the mirror, relative to the folder:
// `<slug>/<YYYY>/<key>.md` for a repository's report, `<YYYY>/<key>.md` for
// the project's aggregate (an empty slug), and "" for a (dim, key) pair that is
// no period of this layout -- a key of another dimension's shape, or one that
// names no period at all (PeriodOfKey). A caller that hands it one has no path
// to build, and building one anyway would name a file no reader of the mirror
// would read back as that period.
//
// It is ParseMirrorPath's other direction, and the two are one definition: what
// this writes is what that reads, for every period and every slug of a
// repository's storage slug shape.
func MirrorName(slug string, dim Dim, key string) string {
	if _, err := PeriodOfKey(dim, key); err != nil {
		return ""
	}
	rel := PeriodPath(key)
	if slug == "" {
		return rel
	}
	if !task.ValidSlug(slug) {
		return ""
	}
	return slug + "/" + rel
}

// MirrorPath is MirrorName's file in the checkout: the folder dir and the
// report's own name under it. dir is the designation's folder, relative to the
// repository root (projectpolicy.ValidReportsCommitDir), and "" answers the
// name alone, which is what the object a launch hands in is addressed by.
func MirrorPath(dir, slug string, dim Dim, key string) string {
	name := MirrorName(slug, dim, key)
	switch {
	case name == "":
		return ""
	case dir == "":
		return name
	}
	return strings.TrimSuffix(dir, "/") + "/" + name
}

// MirrorObjectKey is where a launch puts one object it hands the commit step,
// under the run's own prefix in the runs bucket: the mirror-relative name under
// the reports prefix, so the object's key is the name the step checks and the
// path it commits, and the two cannot drift.
//
// prefix is the run's own prefix (internal/runstore's Store.Prefix, "runs/
// <slug>/<run>/"), which is the one prefix the run's account reads; the object
// is written there by the launcher's own credential (D2754) and read back here.
// It is the sibling of runstore.InputKey, and it lives here rather than there
// because the name is the mirror's: what a launch hands in is addressed by a
// report's own name, and this package owns that shape.
func MirrorObjectKey(prefix, name string) string {
	return prefix + ReportsPrefix + "/" + name
}

// ParseMirrorPath reads a mirror-relative name -- the shape above and nothing
// else -- and answers the repository it belongs to ("" for the project's
// aggregate), the period and the key.
//
// It is the check every object a launch hands the commit-reports step is held
// to before a byte is written, and it is a shape check rather than a
// sanitisation: a name that is not exactly `<slug>/<YYYY>/<key>.md` or
// `<YYYY>/<key>.md`, with the year directory the key's own year (ReportName)
// and the slug a storage slug (task.ValidSlug), is refused outright, so
// nothing is cleaned into something plausible and no component of a name is
// ever `.`, `..`, absolute, empty or hidden. task.InputNameProblem is
// deliberately not this check: it admits a name beginning with a dot because
// an --attach input is written under the git-excluded inputs/ (internal/task),
// and this step commits the path it writes.
func ParseMirrorPath(rel string) (slug string, dim Dim, key string, ok bool) {
	if rel == "" || strings.HasPrefix(rel, "/") {
		return "", "", "", false
	}
	parts := strings.Split(rel, "/")
	if len(parts) == 3 {
		slug = parts[0]
		if !task.ValidSlug(slug) {
			return "", "", "", false
		}
		parts = parts[1:]
	} else if len(parts) != 2 {
		return "", "", "", false
	}
	dim, key, ok = ReportName(parts[0] + "/" + parts[1])
	if !ok {
		return "", "", "", false
	}
	return slug, dim, key, true
}

// MirrorRows reads the mirror's folder: one row per report file under dir,
// whichever repository it belongs to and the project's aggregates among them,
// each row's Rel the name it carries under dir. Rows are ordered by period and
// then by name, so the index one writer regenerates is the index another
// regenerates byte for byte (G3) -- two repositories and the aggregate may
// report the same period, and the name is what tells those rows apart.
//
// A file that is there but whose summary block is not one this package wrote
// is a row with no figures (ReadRow), and a file that is no mirror name at all
// -- the index beside them, a name a person moved in, a file an older layout
// left -- is no row: the folder is the repository's to shape, and the index is
// regenerated over the reports rather than over every file. A directory that
// is not there holds no reports (the first run into a fresh folder), and a
// symbolic link anywhere under it is refused rather than followed: the mirror
// is written and read by the commit step, and a link would put either the read
// or the write somewhere no report run may reach.
func MirrorRows(dir string) ([]Row, error) {
	var rows []Row
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symbolic link, and the report mirror is neither read nor written through one: the folder is the repository's to shape, but a link under it would put a committed report somewhere no report run may reach", rel)
		}
		_, dim, key, ok := ParseMirrorPath(rel)
		if !ok {
			return nil
		}
		row := Row{Dim: dim, Key: key, Rel: rel}
		if s, read := ReadRow(path, key); read {
			row.Summary, row.Read = s, true
		}
		rows = append(rows, row)
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Key != rows[j].Key {
			return rows[i].Key < rows[j].Key
		}
		return rows[i].Rel < rows[j].Rel
	})
	return rows, nil
}
