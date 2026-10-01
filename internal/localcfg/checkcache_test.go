package localcfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func cacheEnv(t *testing.T) func(string) string {
	t.Helper()
	dir := t.TempDir()
	return func(k string) string {
		if k == "XDG_CACHE_HOME" {
			return dir
		}
		return ""
	}
}

var t0 = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

func TestNameCheckCacheFreshForADay(t *testing.T) {
	env := cacheEnv(t)
	if _, ok := CachedNameCheck(env, "aurora", t0); ok {
		t.Fatal("an empty cache answered")
	}
	want := NameCheck{GCPProject: "proj-1234", RunsBucket: "fugaro-runs-proj-1234", CheckedAt: t0}
	if err := SaveNameCheck(env, "aurora", want); err != nil {
		t.Fatal(err)
	}
	got, ok := CachedNameCheck(env, "aurora", t0.Add(23*time.Hour+59*time.Minute))
	if !ok || got.GCPProject != want.GCPProject || got.RunsBucket != want.RunsBucket || !got.CheckedAt.Equal(t0) {
		t.Fatalf("got %+v, %v", got, ok)
	}
	if _, ok := CachedNameCheck(env, "aurora", t0.Add(24*time.Hour)); ok {
		t.Error("a day-old check is still fresh")
	}
	// A check from the future (a clock that moved back) isn't trusted.
	if _, ok := CachedNameCheck(env, "aurora", t0.Add(-time.Minute)); ok {
		t.Error("a check from the future counted")
	}
	// The file is private, under project-check/<name>.json.
	path := filepath.Join(env("XDG_CACHE_HOME"), "fugaro", "project-check", "aurora.json")
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("%s: %v, %v", path, fi, err)
	}
}

// Each project has its own file, and the entry carries the bucket and GCP
// project it was made for, so a caller that compares them re-checks when
// either changed.
func TestNameCheckCacheIgnoresOtherBucket(t *testing.T) {
	env := cacheEnv(t)
	if err := SaveNameCheck(env, "aurora", NameCheck{GCPProject: "proj-1234", RunsBucket: "fugaro-runs-proj-1234", CheckedAt: t0}); err != nil {
		t.Fatal(err)
	}
	if _, ok := CachedNameCheck(env, "borealis", t0); ok {
		t.Error("aurora's check answered for borealis")
	}
	got, ok := CachedNameCheck(env, "aurora", t0)
	if !ok || got.RunsBucket != "fugaro-runs-proj-1234" || got.GCPProject != "proj-1234" {
		t.Fatalf("got %+v, %v", got, ok)
	}
	// Not a name: never a path.
	if err := SaveNameCheck(env, "../aurora", NameCheck{CheckedAt: t0}); err == nil {
		t.Error("a path was accepted as a name")
	}
	if _, ok := CachedNameCheck(env, "../aurora", t0); ok {
		t.Error("a path was accepted as a name")
	}
}

func TestNameCheckCacheUnreadableIsAMiss(t *testing.T) {
	env := cacheEnv(t)
	dir := filepath.Join(env("XDG_CACHE_HOME"), "fugaro", "project-check")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "aurora.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := CachedNameCheck(env, "aurora", t0); ok {
		t.Error("a corrupt entry answered")
	}
	// Saving over it repairs it; with no cache dir configured it is an error
	// the caller ignores.
	if err := SaveNameCheck(env, "aurora", NameCheck{CheckedAt: t0}); err != nil {
		t.Fatal(err)
	}
	none := func(string) string { return "" }
	if err := SaveNameCheck(none, "aurora", NameCheck{CheckedAt: t0}); err == nil || !strings.Contains(err.Error(), "XDG_CACHE_HOME") {
		t.Errorf("no cache dir: %v", err)
	}
}
