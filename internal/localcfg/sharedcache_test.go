package localcfg

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func sharedEntry(yaml string) SharedCacheEntry {
	return SharedCacheEntry{GCPProject: "fugaro-belong", Bucket: "fugaro-runs-fugaro-belong", Generation: 7, CheckedAt: t0, YAML: yaml}
}

func sharedCacheFile(t *testing.T, env func(string) string, name string) string {
	t.Helper()
	return filepath.Join(env("XDG_CACHE_HOME"), "fugaro", "shared-config", name+".json")
}

func TestSharedCacheSaveLoadAndAge(t *testing.T) {
	env := cacheEnv(t)
	if _, ok := LoadSharedCache(env, "belong"); ok {
		t.Fatal("an empty cache answered")
	}
	want := sharedEntry("name: belong\n")
	if err := SaveSharedCache(env, "belong", want); err != nil {
		t.Fatal(err)
	}
	got, ok := LoadSharedCache(env, "belong")
	if !ok || got.GCPProject != want.GCPProject || got.Bucket != want.Bucket || got.Generation != 7 || got.YAML != want.YAML || !got.CheckedAt.Equal(t0) {
		t.Fatalf("got %+v, %v", got, ok)
	}
	if !got.Fresh(t0.Add(SharedFreshFor - time.Second)) {
		t.Error("an entry under a day old is not fresh")
	}
	// Older than a day: still loadable (the caller decides on the offline
	// allowance), but not fresh.
	if got.Fresh(t0.Add(SharedFreshFor)) {
		t.Error("a day-old entry is fresh")
	}
	if !got.UsableOffline(t0.Add(SharedOfflineFor)) || got.UsableOffline(t0.Add(SharedOfflineFor+time.Second)) {
		t.Error("the offline allowance is not seven days")
	}
	// A stamp from the future (a clock that moved back) is neither.
	if got.Fresh(t0.Add(-time.Second)) || got.UsableOffline(t0.Add(-time.Second)) {
		t.Error("an entry from the future was trusted")
	}
	if _, ok := LoadSharedCache(env, "other"); ok {
		t.Error("another project's name answered")
	}
}

func TestSharedCacheMisses(t *testing.T) {
	env := cacheEnv(t)
	if err := SaveSharedCache(env, "belong", sharedEntry("name: belong\n")); err != nil {
		t.Fatal(err)
	}
	path := sharedCacheFile(t, env, "belong")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"truncated":   string(data[:len(data)/2]),
		"empty":       "",
		"not json":    "garbage",
		"no yaml":     `{"gcp_project":"fugaro-belong","bucket":"fugaro-runs-fugaro-belong","checked_at":"2026-09-30T12:00:00Z"}`,
		"no project":  `{"bucket":"fugaro-runs-fugaro-belong","checked_at":"2026-09-30T12:00:00Z","yaml":"x"}`,
		"no bucket":   `{"gcp_project":"fugaro-belong","checked_at":"2026-09-30T12:00:00Z","yaml":"x"}`,
		"no stamp":    `{"gcp_project":"fugaro-belong","bucket":"fugaro-runs-fugaro-belong","yaml":"x"}`,
		"json null":   "null",
		"json array":  "[]",
		"json string": `"x"`,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if e, ok := LoadSharedCache(env, "belong"); ok {
			t.Errorf("%s: a hit: %+v", name, e)
		}
	}
}

func TestSharedCacheNameIsValidated(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	env := func(k string) string {
		if k == "XDG_CACHE_HOME" {
			return cache
		}
		return ""
	}
	for _, name := range []string{"../escape", "../../etc/x", "a/b", "", "UPPER", ".hidden", "-x"} {
		if err := SaveSharedCache(env, name, sharedEntry("x")); err == nil {
			t.Errorf("%q: saved", name)
		}
		if _, ok := LoadSharedCache(env, name); ok {
			t.Errorf("%q: loaded", name)
		}
		if err := DropSharedCache(env, name); err == nil {
			t.Errorf("%q: dropped", name)
		}
	}
	// Nothing was written anywhere: not in the cache, not beside it.
	var found []string
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && p != root {
			found = append(found, p)
		}
		return nil
	})
	if len(found) != 0 {
		t.Errorf("files appeared: %q", found)
	}
}

func TestSharedCacheFileModes(t *testing.T) {
	env := cacheEnv(t)
	if err := SaveSharedCache(env, "belong", sharedEntry("name: belong\n")); err != nil {
		t.Fatal(err)
	}
	path := sharedCacheFile(t, env, "belong")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode %v, want 0600", fi.Mode().Perm())
	}
	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v, want 0700", di.Mode().Perm())
	}
	if err := DropSharedCache(env, "belong"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("after drop: %v", err)
	}
	if err := DropSharedCache(env, "belong"); err != nil {
		t.Errorf("dropping a missing entry: %v", err)
	}
}

// TestSharedCacheWritesAreAtomic: concurrent saves and loads never see a
// partial file, and no temporary file is left behind.
func TestSharedCacheWritesAreAtomic(t *testing.T) {
	env := cacheEnv(t)
	if err := SaveSharedCache(env, "belong", sharedEntry("seed")); err != nil {
		t.Fatal(err)
	}
	path := sharedCacheFile(t, env, "belong")
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				// Different lengths, so a torn write would show.
				y := fmt.Sprintf("w%d-%d-%s", w, i, strings.Repeat("y", (w*50+i)*97))
				if err := SaveSharedCache(env, "belong", sharedEntry(y)); err != nil {
					t.Error(err)
					return
				}
			}
		}(w)
	}
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			select {
			case <-stop:
				return
			default:
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Errorf("the cache file vanished during a save: %v", err)
				return
			}
			var e SharedCacheEntry
			if err := json.Unmarshal(data, &e); err != nil {
				t.Errorf("a partial file was visible (%d bytes): %v", len(data), err)
				return
			}
			if _, ok := LoadSharedCache(env, "belong"); !ok {
				t.Error("a load missed during concurrent saves")
				return
			}
		}
	}()
	wg.Wait()
	close(stop)
	<-readerDone
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "belong.json" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("the cache directory holds %q", names)
	}
}
