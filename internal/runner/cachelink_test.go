package runner_test

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/cache"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// cacheArchives counts the cache archives in the harness's bucket.
func cacheArchives(t *testing.T, h *harness) int {
	t.Helper()
	it := h.bucket.List(nil)
	n := 0
	for obj, err := it.Next(context.Background()); err == nil; obj, err = it.Next(context.Background()) {
		if strings.HasPrefix(obj.Key, "cache/") && strings.HasSuffix(obj.Key, ".tar.zst") {
			n++
		}
	}
	return n
}

// TestAgentLinkedCacheRootIsNotWrittenBack: the agent replaces the cache
// root with a symlink before writeback. Whatever it points at (the
// agent's sessions, the runner's credentials, the checkout's .git) is
// never archived.
func TestAgentLinkedCacheRootIsNotWrittenBack(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target func(h *harness, home, tmp string) string
	}{
		{"agent sessions", func(h *harness, home, tmp string) string { return filepath.Join(home, ".claude") }},
		{"runner credentials", func(h *harness, home, tmp string) string { return filepath.Join(tmp, "creds") }},
		{"git metadata", func(h *harness, home, tmp string) string { return filepath.Join(h.deps.WorkDir, ".git") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, cacheYAML, nil)
			withBucket(h)
			var logs bytes.Buffer
			h.deps.Log = slog.New(slog.NewTextHandler(&logs, nil))
			home := envValue(h.deps.Env, "HOME")
			root := filepath.Join(home, ".fugaro-test-cache")
			link := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
				target := tc.target(h, home, t.TempDir())
				if err := os.MkdirAll(target, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(target, "secret"), []byte("do-not-upload"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.RemoveAll(root); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, root); err != nil {
					t.Fatal(err)
				}
				return implement("feature")(t, ctx, req)
			}
			rec, err := h.run(t, link, review("ship", 0))
			if err != nil || rec.Status != runstore.StatusSucceeded {
				t.Fatalf("rec = %+v, err = %v", rec, err)
			}
			if n := cacheArchives(t, h); n != 0 {
				t.Fatalf("%d cache archives written through the symlinked root", n)
			}
			if !strings.Contains(logs.String(), "symlink") {
				t.Fatalf("no warning about the symlinked cache root:\n%s", logs.String())
			}
		})
	}
}

// linkCacheYAML caches a checkout directory under vendor/.
const linkCacheYAML = `version: 1
git: { provider: github, base_branch: main }
agent: { auth: api-key, review_rounds: 1 }
workflows:
  app:
    base: web-node
    commands:
      build: sh build.sh
      test: sh test.sh
      reports: ["build/test-results/*.xml"]
    cache:
      - { key: [README.md], paths: ["vendor/cache"] }
    secrets:
      - { name: fixture-fails, env: FIXTURE_FAILS_FILE }
    timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }
`

// TestCommittedLinkAboveCacheRootIsNotRestored: the repository commits
// vendor -> .git, so the cache root vendor/cache lies under a link into
// the checkout's git metadata. A restore must not write there.
func TestCommittedLinkAboveCacheRootIsNotRestored(t *testing.T) {
	h := newHarness(t, linkCacheYAML, nil)
	b := withBucket(h)
	var logs bytes.Buffer
	h.deps.Log = slog.New(slog.NewTextHandler(&logs, nil))
	seed := filepath.Join(t.TempDir(), "seed")
	testutil.Git(t, filepath.Dir(seed), "clone", "--quiet", h.remote, seed)
	if err := os.Symlink(".git", filepath.Join(seed, "vendor")); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, seed, "add", "-A")
	testutil.Git(t, seed, "commit", "--quiet", "-m", "link vendor")
	testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/main")

	// Plant an archive under the key bootstrap computes, holding a hook.
	keyRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(keyRoot, "README.md"), []byte(h.files["README.md"]), 0o644); err != nil {
		t.Fatal(err)
	}
	key, ok, err := cache.KeyOf(keyRoot, cacheEntry(t, linkCacheYAML), "unpinned", runner.ToolchainHash(config.Image{}))
	if err != nil || !ok {
		t.Fatal(err)
	}
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "hooks", "pre-commit"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	store := &cache.Store{Bucket: b, Slug: h.store.Slug(), Workflow: "app"}
	if saved, err := store.Save(context.Background(), key, []string{src}); err != nil || !saved {
		t.Fatalf("planting the archive: %v, %v", saved, err)
	}

	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if _, err := os.Stat(filepath.Join(h.deps.WorkDir, ".git", "cache")); err == nil {
		t.Fatal("the restore wrote into .git through the committed link")
	}
	if !strings.Contains(logs.String(), "symlink") {
		t.Fatalf("no warning about the symlinked cache path:\n%s", logs.String())
	}
}
