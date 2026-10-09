package localcfg

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/dimipaun/fugaro/internal/config"
)

// The project layer's cache (docs/design/layered-config.md §7, decision
// L13): unlike the shared config's and the recipes', it is never "fresh".
// Every command reads the bucket; the entry stands in only when the bucket
// cannot be reached at all, for SharedOfflineFor.

// layerCachePath is $XDG_CACHE_HOME/fugaro/project-layers/<project>.json,
// else under ~/.cache.
func layerCachePath(getenv func(string) string, project string) (string, error) {
	return cachePath(getenv, "project-layers", project)
}

// LoadLayerCache is the project's cached project layer, whatever its age:
// the caller decides with UsableOffline and parses YAML again on every use.
// Any trouble is a miss.
func LoadLayerCache(getenv func(string) string, project string) (SharedCacheEntry, bool) {
	path, err := layerCachePath(getenv, project)
	if err != nil {
		return SharedCacheEntry{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 6*config.LayerMaxBytes+1024 {
		return SharedCacheEntry{}, false
	}
	var e SharedCacheEntry
	if json.Unmarshal(data, &e) != nil || e.GCPProject == "" || e.Bucket == "" || e.CheckedAt.IsZero() || e.YAML == "" || len(e.YAML) > config.LayerMaxBytes {
		return SharedCacheEntry{}, false
	}
	return e, true
}

// SaveLayerCache stores the entry, 0600 in a 0700 directory, atomically.
// Best effort: callers ignore the error.
func SaveLayerCache(getenv func(string) string, project string, e SharedCacheEntry) error {
	path, err := layerCachePath(getenv, project)
	if err != nil {
		return err
	}
	if len(e.YAML) > config.LayerMaxBytes {
		return fmt.Errorf("project layer is %d bytes, over the %d byte cap", len(e.YAML), config.LayerMaxBytes)
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return writeCacheFile(path, data)
}

// DropLayerCache removes the entry; a missing one is not an error.
func DropLayerCache(getenv func(string) string, project string) error {
	path, err := layerCachePath(getenv, project)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
