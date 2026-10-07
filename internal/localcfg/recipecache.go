package localcfg

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/dimipaun/fugaro/internal/recipe"
)

// recipeCachePath is $XDG_CACHE_HOME/fugaro/recipes/<project>/<name>.json,
// else under ~/.cache (decision D9 of docs/plans/2026-10-07-recipes.md).
func recipeCachePath(getenv func(string) string, project, name string) (string, error) {
	if !recipe.NameRE.MatchString(name) {
		return "", fmt.Errorf("%q is not a recipe name", name)
	}
	p, err := cachePath(getenv, "recipes", project)
	if err != nil {
		return "", err
	}
	return filepath.Join(strings.TrimSuffix(p, ".json"), name+".json"), nil
}

// LoadRecipeCache is the cached project recipe, whatever its age: the caller
// decides with Fresh and UsableOffline, and parses YAML again on every use.
// Any trouble is a miss.
func LoadRecipeCache(getenv func(string) string, project, name string) (SharedCacheEntry, bool) {
	path, err := recipeCachePath(getenv, project, name)
	if err != nil {
		return SharedCacheEntry{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 2*recipe.MaxBytes+1024 {
		return SharedCacheEntry{}, false
	}
	var e SharedCacheEntry
	if json.Unmarshal(data, &e) != nil || e.GCPProject == "" || e.Bucket == "" || e.CheckedAt.IsZero() || e.YAML == "" || len(e.YAML) > recipe.MaxBytes {
		return SharedCacheEntry{}, false
	}
	return e, true
}

// SaveRecipeCache stores the entry, 0600 in a 0700 directory, atomically.
// Best effort: callers ignore the error.
func SaveRecipeCache(getenv func(string) string, project, name string, e SharedCacheEntry) error {
	path, err := recipeCachePath(getenv, project, name)
	if err != nil {
		return err
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return writeCacheFile(path, data)
}

// DropRecipeCache removes the entry; a missing one is not an error.
func DropRecipeCache(getenv func(string) string, project, name string) error {
	path, err := recipeCachePath(getenv, project, name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
