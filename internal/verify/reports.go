package verify

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/bmatcuk/doublestar/v4"
)

// skipDirs are never searched for reports: they are huge and never hold the
// repository's own results.
var skipDirs = map[string]bool{".git": true, "node_modules": true}

// CollectReports returns report files under root that match any glob
// (relative, slash-separated) and were modified at or after since, so reports
// left by earlier runs are ignored.
func CollectReports(root string, globs []string, since time.Time) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if !matchAny(globs, filepath.ToSlash(rel)) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.ModTime().Unix() < since.Unix() {
			return nil
		}
		out = append(out, p)
		return nil
	})
	return out, err
}

func matchAny(globs []string, rel string) bool {
	for _, g := range globs {
		if ok, _ := doublestar.Match(g, rel); ok {
			return true
		}
	}
	return false
}

// ReadCases parses every fresh report under root.
func ReadCases(root string, globs []string, since time.Time) ([]TestCase, error) {
	files, err := CollectReports(root, globs, since)
	if err != nil {
		return nil, err
	}
	var all []TestCase
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			return nil, err
		}
		cases, err := ParseJUnit(fh)
		fh.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		all = append(all, cases...)
	}
	return all, nil
}
