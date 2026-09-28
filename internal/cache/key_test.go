package cache

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	k1, ok, err := KeyOf(context.Background(), root, e, "base@sha256:1", "tc1", nil)
	if err != nil || !ok || len(k1) != 64 {
		t.Fatalf("KeyOf = %q %v %v", k1, ok, err)
	}
	if k2, _, _ := KeyOf(context.Background(), root, e, "base@sha256:2", "tc1", nil); k2 == k1 {
		t.Fatal("the base image does not change the key")
	}
	if k3, _, _ := KeyOf(context.Background(), root, config.CacheEntry{Key: e.Key, Paths: []string{"~/.other"}}, "base@sha256:1", "tc1", nil); k3 == k1 {
		t.Fatal("the paths do not change the key")
	}
	if k5, _, _ := KeyOf(context.Background(), root, e, "base@sha256:1", "tc2", nil); k5 == k1 {
		t.Fatal("the toolchain does not change the key")
	}
	write(t, root, map[string]string{"yarn.lock": "v2"})
	if k4, _, _ := KeyOf(context.Background(), root, e, "base@sha256:1", "tc1", nil); k4 == k1 {
		t.Fatal("the key file's content does not change the key")
	}
	glob := config.CacheEntry{Key: []string{"**/*.gradle*"}, Paths: []string{"~/.gradle/caches"}}
	if _, ok, err := KeyOf(context.Background(), root, glob, "", "tc1", nil); !ok || err != nil {
		t.Fatalf("glob KeyOf ok=%v err=%v", ok, err)
	}
	if _, ok, _ := KeyOf(context.Background(), root, config.CacheEntry{Key: []string{"missing.lock"}, Paths: []string{"x"}}, "", "tc1", nil); ok {
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

func TestResolveRefusesCredentialPaths(t *testing.T) {
	for _, bad := range []string{
		"~/.ssh", "~/.ssh/keys", "~/.claude", "~/.claude/projects", "~/.claude.json", "~/.CLAUDE",
		"~/.config", "~/.config/gcloud", "~/.config/gcloud/logs", "~/.git-credentials", "~/.npmrc",
		"~/.netrc", "~/.docker", "~/.docker/config.json",
		"~/.config/gh", "~/.config/gh/hosts.yml", "~/.aws", "~/.aws/credentials", "~/.kube", "~/.kube/config", "~/.gnupg",
	} {
		_, err := Resolve([]string{bad}, "/work/repo", "/home/fugaro")
		if err == nil {
			t.Errorf("Resolve(%q) accepted a credential path", bad)
		} else if !strings.Contains(err.Error(), "~/.config/<tool>") {
			t.Errorf("Resolve(%q) = %v; the error does not suggest a narrower path", bad, err)
		}
	}
	// Only home paths are credential paths: a checkout directory of the
	// same name is the repository's own.
	for _, ok := range []string{"~/.npm", "~/.cache/yarn", "~/.config/yarn", ".docker/cache", "~/.sshx", "~/.config/ghx", "~/.m2", "~/.gradle"} {
		if _, err := Resolve([]string{ok}, "/work/repo", "/home/fugaro"); err != nil {
			t.Errorf("Resolve(%q) = %v", ok, err)
		}
	}
}

func TestResolveWarnings(t *testing.T) {
	got := ResolveWarnings([]string{"~/.config/yarn", "~/.cache/yarn", ".config/x", "~/./.config/gh"})
	if len(got) != 2 || !strings.Contains(got[0], "~/.config/yarn") || !strings.Contains(got[1], "~/./.config/gh") {
		t.Fatalf("ResolveWarnings = %q", got)
	}
}

// TestKeyOfSurvivesSymlinkLoops: the agent can plant self-referencing
// directory links before writeback; a "**" key pattern must not follow them,
// or the walk grows exponentially and never returns.
func TestKeyOfSurvivesSymlinkLoops(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{"sub/x.lock": "v1"})
	for _, name := range []string{"a", "b", "c"} {
		if err := os.Symlink(".", filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	e := config.CacheEntry{Key: []string{"**/x.lock"}, Paths: []string{"~/.cache/x"}}
	done := make(chan error, 1)
	go func() {
		_, ok, err := KeyOf(context.Background(), root, e, "", "tc", nil)
		if err == nil && !ok {
			err = os.ErrNotExist
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("KeyOf with symlink loops: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("KeyOf hung on a tree with symlink loops")
	}
}
