package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// MiseConfigPaths are the root mise config files a derived image installs
// from and its hash covers (design base-image.md section 4). mise.local.toml
// is never committed, and idiomatic version files are off in the base.
var MiseConfigPaths = []string{"mise.toml", ".mise.toml", "mise/config.toml", ".mise/config.toml",
	".config/mise.toml", ".config/mise/config.toml", ".tool-versions"}

// MiseConfigDirs hold the conf.d fragments (*.toml) mise also reads.
var MiseConfigDirs = []string{"mise/conf.d", ".mise/conf.d", ".config/mise/conf.d"}

// MiseLockfile pins the configs' versions; the hash covers it, and it
// declares no tool.
const MiseLockfile = "mise.lock"

// MiseConfigFiles are the root mise config files of the checkout at root, as
// sorted slash-separated relative paths: regular files only (a link is not
// followed), the lockfile and hidden conf.d fragments left out.
func MiseConfigFiles(root string) ([]string, error) {
	var out []string
	for _, rel := range MiseConfigPaths {
		switch fi, err := os.Lstat(filepath.Join(root, rel)); {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return nil, err
		case fi.Mode().IsRegular():
			out = append(out, rel)
		}
	}
	for _, dir := range MiseConfigDirs {
		matches, err := filepath.Glob(filepath.Join(root, dir, "*.toml"))
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			if fi, err := os.Lstat(m); err != nil || !fi.Mode().IsRegular() || strings.HasPrefix(filepath.Base(m), ".") {
				continue
			}
			rel, err := filepath.Rel(root, m)
			if err != nil {
				return nil, err
			}
			out = append(out, filepath.ToSlash(rel))
		}
	}
	slices.Sort(out)
	return out, nil
}
