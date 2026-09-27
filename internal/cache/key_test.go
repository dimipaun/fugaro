package cache

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

func write(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for p, c := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestKeyOf(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{"yarn.lock": "v1", "a/build.gradle": "x", "b/build.gradle.kts": "y"})
	e := config.CacheEntry{Key: []string{"yarn.lock"}, Paths: []string{"~/.yarn/berry/cache"}}
	k1, ok, err := KeyOf(root, e, "base@sha256:1", "tc1")
	if err != nil || !ok || len(k1) != 64 {
		t.Fatalf("KeyOf = %q %v %v", k1, ok, err)
	}
	if k2, _, _ := KeyOf(root, e, "base@sha256:2", "tc1"); k2 == k1 {
		t.Fatal("the base image does not change the key")
	}
	if k3, _, _ := KeyOf(root, config.CacheEntry{Key: e.Key, Paths: []string{"~/.other"}}, "base@sha256:1", "tc1"); k3 == k1 {
		t.Fatal("the paths do not change the key")
	}
	if k5, _, _ := KeyOf(root, e, "base@sha256:1", "tc2"); k5 == k1 {
		t.Fatal("the toolchain does not change the key")
	}
	write(t, root, map[string]string{"yarn.lock": "v2"})
	if k4, _, _ := KeyOf(root, e, "base@sha256:1", "tc1"); k4 == k1 {
		t.Fatal("the key file's content does not change the key")
	}
	glob := config.CacheEntry{Key: []string{"**/*.gradle*"}, Paths: []string{"~/.gradle/caches"}}
	if _, ok, err := KeyOf(root, glob, "", "tc1"); !ok || err != nil {
		t.Fatalf("glob KeyOf ok=%v err=%v", ok, err)
	}
	if _, ok, _ := KeyOf(root, config.CacheEntry{Key: []string{"missing.lock"}, Paths: []string{"x"}}, "", "tc1"); ok {
		t.Fatal("no matching key file must mean no cache")
	}
}

func TestResolve(t *testing.T) {
	got, err := Resolve([]string{"~/.cache/yarn", "node_modules/.cache"}, "/work/repo", "/home/fugaro")
	if err != nil || got[0] != "/home/fugaro/.cache/yarn" || got[1] != "/work/repo/node_modules/.cache" {
		t.Fatalf("Resolve = %v, %v", got, err)
	}
	for _, bad := range []string{"/etc", "~/../../etc", "../x", "~", "~user/x", ".git", ".git/hooks", "sub/.GIT/x"} {
		if _, err := Resolve([]string{bad}, "/work/repo", "/home/fugaro"); err == nil {
			t.Errorf("Resolve(%q) accepted", bad)
		}
	}
	for _, overlap := range [][]string{{"~/.cache", "~/.cache/yarn"}, {"x/y", "x"}, {"a", "./a"}} {
		if _, err := Resolve(overlap, "/work/repo", "/home/fugaro"); err == nil {
			t.Errorf("Resolve(%q) accepted overlapping roots", overlap)
		}
	}
	if _, err := Resolve([]string{"~/.cache/a", "~/.cache/ab"}, "/work/repo", "/home/fugaro"); err != nil {
		t.Errorf("sibling roots with a common prefix refused: %v", err)
	}
}
