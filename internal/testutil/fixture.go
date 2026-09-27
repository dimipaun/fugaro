package testutil

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// FixtureFiles returns testdata/fixture-repo as a path → content map.
func FixtureFiles(t *testing.T) map[string]string {
	t.Helper()
	root := filepath.Join(ModuleRoot(), "testdata", "fixture-repo")
	files := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		files[filepath.ToSlash(rel)] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}
