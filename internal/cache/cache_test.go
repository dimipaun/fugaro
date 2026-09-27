package cache

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/blobx"
)

func TestSaveRestore(t *testing.T) {
	ctx := context.Background()
	s := &Store{Bucket: blobx.Wrap(memblob.OpenBucket(nil)), Slug: "acme-app", Workflow: "web", MaxBytes: 1 << 20}
	src := t.TempDir()
	write(t, src, map[string]string{"pkg/a.tgz": "A"})
	saved, err := s.Save(ctx, "k1", []string{src})
	if err != nil || !saved {
		t.Fatalf("Save = %v, %v", saved, err)
	}
	if saved, err := s.Save(ctx, "k1", []string{src}); err != nil || saved {
		t.Fatalf("second Save = %v, %v (an existing key must be left alone)", saved, err)
	}
	dst := filepath.Join(t.TempDir(), "cache")
	hit, err := s.Restore(ctx, "k1", []string{dst})
	if err != nil || !hit {
		t.Fatalf("Restore = %v, %v", hit, err)
	}
	if data, _ := os.ReadFile(filepath.Join(dst, "pkg/a.tgz")); string(data) != "A" {
		t.Fatalf("restored = %q", data)
	}
	if hit, err := s.Restore(ctx, "missing", []string{dst}); err != nil || hit {
		t.Fatalf("Restore miss = %v, %v", hit, err)
	}
}

func TestSaveSkipsOversizedArchive(t *testing.T) {
	ctx := context.Background()
	b := blobx.Wrap(memblob.OpenBucket(nil))
	s := &Store{Bucket: b, Slug: "acme-app", Workflow: "web", MaxBytes: 1024}
	src := t.TempDir()
	write(t, src, map[string]string{"big": strings.Repeat("x", 8192)})
	if _, err := s.Save(ctx, "k1", []string{src}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Save = %v", err)
	}
	if ok, _ := b.Exists(ctx, ObjectKey("acme-app", "web", "k1")); ok {
		t.Fatal("an oversized archive was uploaded")
	}
}

func TestSaveSkipsEmptyRoots(t *testing.T) {
	ctx := context.Background()
	b := blobx.Wrap(memblob.OpenBucket(nil))
	s := &Store{Bucket: b, Slug: "acme-app", Workflow: "web"}
	empty := t.TempDir()
	if err := os.MkdirAll(filepath.Join(empty, "only-dirs"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, roots := range [][]string{{filepath.Join(t.TempDir(), "missing")}, {empty}} {
		if saved, err := s.Save(ctx, "k1", roots); saved || err != nil {
			t.Fatalf("Save(%v) = %v, %v", roots, saved, err)
		}
	}
	if ok, _ := b.Exists(ctx, ObjectKey("acme-app", "web", "k1")); ok {
		t.Fatal("an empty archive now blocks the key")
	}
}

func TestRestoreCorruptArchiveIsWarning(t *testing.T) {
	ctx := context.Background()
	b := blobx.Wrap(memblob.OpenBucket(nil))
	s := &Store{Bucket: b, Slug: "acme-app", Workflow: "web", MaxBytes: 1 << 20}
	_ = b.WriteAll(ctx, ObjectKey("acme-app", "web", "k1"), []byte("not zstd"), nil)
	hit, err := s.Restore(ctx, "k1", []string{t.TempDir()})
	if hit || err == nil {
		t.Fatalf("Restore corrupt = %v, %v (want a miss with an error the runner logs as a warning)", hit, err)
	}
}
