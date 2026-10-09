package localcfg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
)

func TestLayerCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	getenv := func(k string) string {
		if k == "XDG_CACHE_HOME" {
			return dir
		}
		return ""
	}
	if _, ok := LoadLayerCache(getenv, "aurora"); ok {
		t.Fatal("a cache entry before any save")
	}
	at := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	e := SharedCacheEntry{GCPProject: "proj-1234", Bucket: "fugaro-runs-proj-1234", Generation: 7, CheckedAt: at, YAML: "version: 1\n"}
	if err := SaveLayerCache(getenv, "aurora", e); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "fugaro", "project-layers", "aurora.json")
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("cache file %s: %v %v", path, fi, err)
	}
	got, ok := LoadLayerCache(getenv, "aurora")
	if !ok || got != e {
		t.Fatalf("loaded %+v, %v", got, ok)
	}
	if !got.UsableOffline(at.Add(6*24*time.Hour)) || got.UsableOffline(at.Add(8*24*time.Hour)) {
		t.Fatal("the offline allowance is not 7 days")
	}
	if err := DropLayerCache(getenv, "aurora"); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadLayerCache(getenv, "aurora"); ok {
		t.Fatal("an entry after the drop")
	}
	if err := DropLayerCache(getenv, "aurora"); err != nil {
		t.Fatalf("dropping a missing entry: %v", err)
	}
	if err := SaveLayerCache(getenv, "../x", e); err == nil || !strings.Contains(err.Error(), "not a project name") {
		t.Fatalf("a bad project name: %v", err)
	}
	big := e
	big.YAML = strings.Repeat("x", 64<<10+1)
	if err := SaveLayerCache(getenv, "aurora", big); err == nil || !strings.Contains(err.Error(), "cap") {
		t.Fatalf("an oversized entry was saved: %v", err)
	}
	if _, ok := LoadLayerCache(getenv, "aurora"); ok {
		t.Fatal("an oversized entry was loaded")
	}
	if fi, err := os.Stat(filepath.Dir(path)); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("cache directory: %v %v", fi, err)
	}
}

func TestLayerCacheLoadMisses(t *testing.T) {
	dir := t.TempDir()
	getenv := func(k string) string {
		if k == "XDG_CACHE_HOME" {
			return dir
		}
		return ""
	}
	good := SharedCacheEntry{GCPProject: "proj-1234", Bucket: "fugaro-runs-proj-1234", Generation: 7,
		CheckedAt: time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC), YAML: "version: 1\n"}
	path := filepath.Join(dir, "fugaro", "project-layers", "aurora.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	put := func(data []byte) {
		t.Helper()
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	enc := func(e SharedCacheEntry) []byte {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	put(enc(good))
	if got, ok := LoadLayerCache(getenv, "aurora"); !ok || got != good {
		t.Fatalf("the control entry is a miss: %+v %v", got, ok)
	}
	noYAML, noProject, noBucket, noTime := good, good, good, good
	noYAML.YAML, noProject.GCPProject, noBucket.Bucket, noTime.CheckedAt = "", "", "", time.Time{}
	atCap, over := good, good
	atCap.YAML = strings.Repeat("x", config.LayerMaxBytes)
	over.YAML = strings.Repeat("x", config.LayerMaxBytes+1)
	for name, tc := range map[string]struct {
		data []byte
		hit  bool
	}{
		"empty yaml":         {enc(noYAML), false},
		"empty gcp_project":  {enc(noProject), false},
		"empty bucket":       {enc(noBucket), false},
		"zero checked_at":    {enc(noTime), false},
		"corrupt json":       {[]byte(`{"yaml": "version: 1\n", `), false},
		"yaml over the cap":  {enc(over), false},
		"file over the size": {append(enc(good), strings.Repeat(" ", 6*config.LayerMaxBytes+1024)...), false},
		"yaml at the cap":    {enc(atCap), true},
	} {
		put(tc.data)
		if _, ok := LoadLayerCache(getenv, "aurora"); ok != tc.hit {
			t.Errorf("%s: loaded = %v, want %v", name, ok, tc.hit)
		}
	}
}
