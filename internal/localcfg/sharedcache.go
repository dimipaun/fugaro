package localcfg

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"time"
)

const (
	// SharedFreshFor is how long a fetched shared config is used without
	// reading the runs bucket again.
	SharedFreshFor = 24 * time.Hour
	// SharedOfflineFor is how long a fetched shared config may still be
	// used when the runs bucket can't be reached at all.
	SharedOfflineFor = 7 * 24 * time.Hour
)

// SharedCacheEntry is the last shared config fetched for a project from
// its runs bucket (docs/design/shared-config.md §6). YAML is the object as
// read, which the caller validates again on every use: the cache is in the
// user's own directory, but it is never trusted more than the bucket.
type SharedCacheEntry struct {
	GCPProject string    `json:"gcp_project"`
	Bucket     string    `json:"bucket"`
	Generation int64     `json:"generation"`
	CheckedAt  time.Time `json:"checked_at"`
	YAML       string    `json:"yaml"`
}

// Fresh reports whether the entry was checked less than SharedFreshFor
// before now. A stamp from the future (a clock that moved back) is not.
func (e SharedCacheEntry) Fresh(now time.Time) bool {
	age := now.Sub(e.CheckedAt)
	return age >= 0 && age < SharedFreshFor
}

// UsableOffline reports whether the entry may stand in for an unreachable
// bucket: checked at most SharedOfflineFor before now, and not in the
// future.
func (e SharedCacheEntry) UsableOffline(now time.Time) bool {
	age := now.Sub(e.CheckedAt)
	return age >= 0 && age <= SharedOfflineFor
}

// sharedCachePath is $XDG_CACHE_HOME/fugaro/shared-config/<name>.json,
// else under ~/.cache.
func sharedCachePath(getenv func(string) string, name string) (string, error) {
	return cachePath(getenv, "shared-config", name)
}

// LoadSharedCache is the project's cached shared config, whatever its age:
// the caller decides with Fresh and UsableOffline. Any trouble (no cache, a
// bad name, a truncated or corrupt file, a missing field) is a miss.
func LoadSharedCache(getenv func(string) string, name string) (SharedCacheEntry, bool) {
	path, err := sharedCachePath(getenv, name)
	if err != nil {
		return SharedCacheEntry{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 2*SharedMaxBytes {
		return SharedCacheEntry{}, false
	}
	var e SharedCacheEntry
	if json.Unmarshal(data, &e) != nil || e.GCPProject == "" || e.Bucket == "" || e.CheckedAt.IsZero() || e.YAML == "" {
		return SharedCacheEntry{}, false
	}
	return e, true
}

// SaveSharedCache stores the entry, mode 0600 in a 0700 directory, written
// atomically. It is best effort: callers ignore the error, and the next
// command reads the bucket again.
func SaveSharedCache(getenv func(string) string, name string, e SharedCacheEntry) error {
	path, err := sharedCachePath(getenv, name)
	if err != nil {
		return err
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return writeCacheFile(path, data)
}

// DropSharedCache removes the project's cached shared config; one that is
// not there is not an error.
func DropSharedCache(getenv func(string) string, name string) error {
	path, err := sharedCachePath(getenv, name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
