package cache

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/blobx"
)

// linkedRoot makes root a symlink to a directory holding secret, and
// returns that directory.
func linkedRoot(t *testing.T, root string) string {
	t.Helper()
	target := t.TempDir()
	write(t, target, map[string]string{"session.jsonl": "secret"})
	if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, root); err != nil {
		t.Fatal(err)
	}
	return target
}

func TestSaveRefusesSymlinkedRoot(t *testing.T) {
	ctx := context.Background()
	b := blobx.Wrap(memblob.OpenBucket(nil))
	s := &Store{Bucket: b, Slug: "acme-app", Workflow: "web"}
	root := filepath.Join(t.TempDir(), "node_modules")
	linkedRoot(t, root)
	if _, err := s.Save(ctx, "k1", []string{root}); !errors.Is(err, ErrLinkedRoot) {
		t.Fatalf("Save = %v, want ErrLinkedRoot", err)
	}
	if ok, _ := b.Exists(ctx, ObjectKey("acme-app", "web", "k1")); ok {
		t.Fatal("the symlink's target was archived")
	}
}

func TestRestoreRefusesSymlinkedRoot(t *testing.T) {
	ctx := context.Background()
	b := blobx.Wrap(memblob.OpenBucket(nil))
	s := &Store{Bucket: b, Slug: "acme-app", Workflow: "web"}
	src := t.TempDir()
	write(t, src, map[string]string{"hooks/pre-commit": "evil"})
	if saved, err := s.Save(ctx, "k1", []string{src}); err != nil || !saved {
		t.Fatalf("Save = %v, %v", saved, err)
	}
	root := filepath.Join(t.TempDir(), "node_modules")
	target := linkedRoot(t, root)
	if _, err := s.Restore(ctx, "k1", []string{root}); !errors.Is(err, ErrLinkedRoot) || errors.Is(err, ErrBadArchive) {
		t.Fatalf("Restore = %v, want ErrLinkedRoot (and not a bad archive)", err)
	}
	if _, err := os.Stat(filepath.Join(target, "hooks")); err == nil {
		t.Fatal("the restore wrote through the symlinked root")
	}
	if ok, _ := b.Exists(ctx, ObjectKey("acme-app", "web", "k1")); !ok {
		t.Fatal("a good archive was deleted because of the local root")
	}
}

// TestCheckLinks covers the whole path below its base: the cached
// directory itself or any directory above it may be the link.
func TestCheckLinks(t *testing.T) {
	work, home := t.TempDir(), t.TempDir()
	write(t, work, map[string]string{"node_modules/x": "", "real/dir/y": ""})
	write(t, home, map[string]string{".m2/repository/z": ""})
	if err := CheckLinks([]string{"node_modules", "real/dir", "~/.m2/repository", "missing/dir"}, work, home); err != nil {
		t.Fatalf("CheckLinks on real directories = %v", err)
	}
	for _, tc := range []struct{ name, path, link string }{
		{"root in the checkout", "vendor", filepath.Join(work, "vendor")},
		{"parent in the checkout", "build/cache", filepath.Join(work, "build")},
		{"root in home", "~/.cache-tool", filepath.Join(home, ".cache-tool")},
		{"parent in home", "~/.cache/tool", filepath.Join(home, ".cache")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			linkedRoot(t, tc.link)
			if err := CheckLinks([]string{tc.path}, work, home); !errors.Is(err, ErrLinkedRoot) {
				t.Fatalf("CheckLinks(%s) = %v, want ErrLinkedRoot", tc.path, err)
			}
		})
	}
}

// TestCheckLinksTrustsTheBase: home or the checkout may itself sit behind
// a symlink (macOS's /var, an image's layout); only what lies below the
// base is judged.
func TestCheckLinksTrustsTheBase(t *testing.T) {
	real := t.TempDir()
	write(t, real, map[string]string{"node_modules/x": ""})
	base := filepath.Join(t.TempDir(), "work")
	if err := os.Symlink(real, base); err != nil {
		t.Fatal(err)
	}
	if err := CheckLinks([]string{"node_modules"}, base, t.TempDir()); err != nil {
		t.Fatalf("CheckLinks under a linked base = %v", err)
	}
}
