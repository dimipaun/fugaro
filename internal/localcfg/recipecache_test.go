package localcfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/recipe"
)

func TestRecipeCache(t *testing.T) {
	dir := t.TempDir()
	getenv := func(k string) string {
		if k == "XDG_CACHE_HOME" {
			return dir
		}
		return ""
	}
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	if _, ok := LoadRecipeCache(getenv, "aurora", "team"); ok {
		t.Fatal("hit on an empty cache")
	}
	e := SharedCacheEntry{GCPProject: "proj-1234", Bucket: "fugaro-runs-proj-1234", Generation: 3, CheckedAt: now, YAML: "version: 1\n"}
	if err := SaveRecipeCache(getenv, "aurora", "team", e); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, "fugaro", "recipes", "aurora", "team.json"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("cache file: %v, %v", st, err)
	}
	if st, err := os.Stat(filepath.Join(dir, "fugaro", "recipes", "aurora")); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("cache dir: %v, %v", st, err)
	}
	got, ok := LoadRecipeCache(getenv, "aurora", "team")
	if !ok || got.YAML != e.YAML || got.Generation != 3 || !got.Fresh(now.Add(23*time.Hour)) || got.Fresh(now.Add(25*time.Hour)) ||
		!got.UsableOffline(now.Add(7*24*time.Hour)) || got.UsableOffline(now.Add(7*24*time.Hour+time.Second)) {
		t.Fatalf("entry = %+v, %v", got, ok)
	}
	if _, ok := LoadRecipeCache(getenv, "aurora", "other"); ok {
		t.Fatal("another recipe hits")
	}
	if err := SaveRecipeCache(getenv, "aurora", "../escape", e); err == nil {
		t.Fatal("a bad recipe name was written")
	}
	if err := DropRecipeCache(getenv, "aurora", "team"); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadRecipeCache(getenv, "aurora", "team"); ok {
		t.Fatal("hit after drop")
	}
	if err := DropRecipeCache(getenv, "aurora", "team"); err != nil {
		t.Fatalf("dropping a missing entry: %v", err)
	}
}

// TestEveryNameCheckAcceptsACopy covers this package's half of R19: the cache
// stores and reads back a project's copy of a recipe, and refuses any other
// dotted name, so a copy name can never become a path out of the cache
// directory. The rule is one rule over three checks — internal/recipe's parser
// (with the schema) and the task's spec test their halves under the same name
// — so the full table of names they agree about lives with the parser's.
func TestEveryNameCheckAcceptsACopy(t *testing.T) {
	dir := t.TempDir()
	getenv := func(k string) string {
		if k == "XDG_CACHE_HOME" {
			return dir
		}
		return ""
	}
	e := SharedCacheEntry{GCPProject: "proj-1234", Bucket: "b", CheckedAt: time.Now(), YAML: "version: 1\n"}
	maxCopy := recipe.CopyName(strings.Repeat("n", 40), "github", "acme/"+strings.Repeat("m", 60))
	for _, name := range []string{
		recipe.CopyName("review", "github", "acme/app-web"),
		maxCopy,
	} {
		if err := SaveRecipeCache(getenv, "aurora", name, e); err != nil {
			t.Errorf("%q: %v", name, err)
			continue
		}
		if _, ok := LoadRecipeCache(getenv, "aurora", name); !ok {
			t.Errorf("%q did not round-trip", name)
		}
	}
	for _, name := range []string{
		"review.acme-app-web",
		"review.acme-app-web-1234567",
		"review..acme-app-web-12345678",
		"Review.acme-app-web-12345678",
		maxCopy + "m",
	} {
		if err := SaveRecipeCache(getenv, "aurora", name, e); err == nil {
			t.Errorf("%q was written", name)
		}
		if err := DropRecipeCache(getenv, "aurora", name); err == nil {
			t.Errorf("%q was dropped", name)
		}
		if _, ok := LoadRecipeCache(getenv, "aurora", name); ok {
			t.Errorf("%q hit", name)
		}
	}
}

func TestRecipeCacheRefusesOversizedAndBadNames(t *testing.T) {
	dir := t.TempDir()
	getenv := func(k string) string {
		if k == "XDG_CACHE_HOME" {
			return dir
		}
		return ""
	}
	e := SharedCacheEntry{GCPProject: "proj-1234", Bucket: "b", CheckedAt: time.Now(), YAML: strings.Repeat("a", 16<<10+1)}
	if err := SaveRecipeCache(getenv, "aurora", "team", e); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadRecipeCache(getenv, "aurora", "team"); ok {
		t.Fatal("an entry over the 16 KiB recipe limit hit")
	}
	for _, bad := range []string{"", "..", "a/b", "A", "-a", "a.json"} {
		if _, ok := LoadRecipeCache(getenv, "aurora", bad); ok {
			t.Errorf("name %q hit", bad)
		}
		if err := DropRecipeCache(getenv, "aurora", bad); err == nil {
			t.Errorf("name %q dropped", bad)
		}
	}
	if err := SaveRecipeCache(getenv, "../x", "team", e); err == nil {
		t.Error("a bad project name was written")
	}
}

func TestRecipeCacheEscapedMaxRecipe(t *testing.T) {
	dir := t.TempDir()
	getenv := func(k string) string {
		if k == "XDG_CACHE_HOME" {
			return dir
		}
		return ""
	}
	e := SharedCacheEntry{GCPProject: "proj-1234", Bucket: "b", CheckedAt: time.Now(), YAML: strings.Repeat("<", 16<<10)}
	if err := SaveRecipeCache(getenv, "aurora", "team", e); err != nil {
		t.Fatal(err)
	}
	got, ok := LoadRecipeCache(getenv, "aurora", "team")
	if !ok || got.YAML != e.YAML {
		t.Fatal("a 16 KiB recipe of escaped characters did not round-trip")
	}
}
