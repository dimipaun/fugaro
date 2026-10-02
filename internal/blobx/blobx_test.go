package blobx_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gocloud.dev/blob"
	"gocloud.dev/blob/fileblob"
	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

func containsParam(query, name string) bool {
	v, err := url.ParseQuery(query)
	return err == nil && v.Has(name)
}

func TestConditionalOpsOnGCS(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewGCS(t)
	b := fake.Bucket(t, "runs")
	gen, err := b.Create(ctx, "k", []byte("v1"), "text/plain")
	if err != nil || gen == 0 {
		t.Fatalf("Create = %d, %v", gen, err)
	}
	if _, err := b.Create(ctx, "k", []byte("again"), "text/plain"); !errors.Is(err, blobx.ErrExists) {
		t.Fatalf("second Create = %v", err)
	}
	data, rgen, err := b.Read(ctx, "k")
	if err != nil || string(data) != "v1" || rgen != gen {
		t.Fatalf("Read = %q %d %v", data, rgen, err)
	}
	gen2, err := b.ReplaceIf(ctx, "k", []byte("v2"), gen, data)
	if err != nil || gen2 == gen {
		t.Fatalf("ReplaceIf = %d, %v", gen2, err)
	}
	if _, err := b.ReplaceIf(ctx, "k", []byte("v3"), gen, data); !errors.Is(err, blobx.ErrConflict) {
		t.Fatalf("stale ReplaceIf = %v", err)
	}
	if err := b.DeleteIf(ctx, "k", gen, data); !errors.Is(err, blobx.ErrConflict) {
		t.Fatalf("stale DeleteIf = %v", err)
	}
	if err := b.DeleteIf(ctx, "k", 0, nil); err == nil {
		t.Fatal("DeleteIf with generation 0 on GCS must be refused")
	}
	if err := b.Touch(ctx, "k", time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)); err != nil || !fake.HasCustomTime("runs", "k") {
		t.Fatalf("Touch: %v", err)
	}
	if err := b.DeleteIf(ctx, "k", gen2, []byte("v2")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Read(ctx, "k"); !errors.Is(err, blobx.ErrNotExist) {
		t.Fatalf("Read after delete = %v", err)
	}
	var preconditions int
	for _, r := range fake.Requests() {
		if containsParam(r.Query, "ifGenerationMatch") {
			preconditions++
		}
	}
	if preconditions < 4 { // create, two replaces, two deletes (one refused locally)
		t.Fatalf("%d requests carried ifGenerationMatch", preconditions)
	}
}

func TestConditionalOpsFallback(t *testing.T) {
	ctx := context.Background()
	b := blobx.Wrap(memblob.OpenBucket(nil))
	if _, err := b.Create(ctx, "k", []byte("v1"), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := b.ReplaceIf(ctx, "k", []byte("v2"), 0, []byte("not v1")); !errors.Is(err, blobx.ErrConflict) {
		t.Fatalf("content mismatch = %v", err)
	}
	if _, err := b.ReplaceIf(ctx, "k", []byte("v2"), 0, []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if err := b.DeleteIf(ctx, "k", 0, []byte("v2")); err != nil {
		t.Fatal(err)
	}
	if err := b.Touch(ctx, "missing", time.Now()); err != nil {
		t.Fatalf("Touch is a no-op off GCS: %v", err)
	}
}

// TestReplaceIfFallbackSurfacesReadErrors: only a vanished or changed object
// is a conflict. An I/O failure reading the current content must come back
// as itself, or a caller would report it as a lost race.
func TestReplaceIfFallbackSurfacesReadErrors(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	ctx := context.Background()
	dir := t.TempDir()
	fb, err := fileblob.OpenBucket(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fb.Close() })
	b := blobx.Wrap(fb)
	if _, err := b.Create(ctx, "k", []byte("v1"), ""); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "k")
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	_, err = b.ReplaceIf(ctx, "k", []byte("v2"), 0, []byte("v1"))
	if err == nil || errors.Is(err, blobx.ErrConflict) || !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("ReplaceIf over an unreadable object = %v, want the permission error", err)
	}
	if _, err := b.ReplaceIf(ctx, "missing", []byte("v2"), 0, []byte("v1")); !errors.Is(err, blobx.ErrConflict) {
		t.Fatalf("ReplaceIf of a vanished object = %v, want ErrConflict", err)
	}
}

func TestReplaceIfStoresJSONAsJSON(t *testing.T) {
	ctx := context.Background()
	for name, b := range map[string]*blobx.Bucket{
		"mem": blobx.Wrap(memblob.OpenBucket(nil)),
		"gcs": gcpfake.NewGCS(t).Bucket(t, "runs"),
	} {
		t.Run(name, func(t *testing.T) {
			if err := b.WriteAll(ctx, "k", []byte("garbage"), nil); err != nil {
				t.Fatal(err)
			}
			prev, gen, err := b.Read(ctx, "k")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := b.ReplaceIf(ctx, "k", []byte(`{"run_id":"x"}`), gen, prev); err != nil {
				t.Fatal(err)
			}
			a, err := b.Attributes(ctx, "k")
			if err != nil || a.ContentType != "application/json" {
				t.Fatalf("content type after a JSON replace = %v, %v", a, err)
			}
		})
	}
}

// TestReadIsCapped: every object Read serves is small JSON (locks,
// claims, records), and the job's service account can write any of them,
// so a larger one fails with ErrTooLarge instead of being read whole.
func TestReadIsCapped(t *testing.T) {
	ctx := context.Background()
	b := blobx.Wrap(memblob.OpenBucket(nil))
	if err := b.WriteAll(ctx, "ok", make([]byte, blobx.MaxReadBytes), nil); err != nil {
		t.Fatal(err)
	}
	if data, _, err := b.Read(ctx, "ok"); err != nil || len(data) != blobx.MaxReadBytes {
		t.Fatalf("Read at the cap = %d bytes, %v", len(data), err)
	}
	if err := b.WriteAll(ctx, "huge", make([]byte, blobx.MaxReadBytes+1), nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Read(ctx, "huge"); !errors.Is(err, blobx.ErrTooLarge) {
		t.Fatalf("Read past the cap = %v, want ErrTooLarge", err)
	}
}

// TestFileBucketReadsAreNeverTorn: a file:// bucket read while the same
// key is rewritten returns the old or the new content. With fileblob's
// default attribute sidecar, a read that lands while the sidecar is being
// truncated fails with EOF; this was the flaky "reading result.json: EOF"
// of `fugaro ls` against a run that was still writing its record.
func TestFileBucketReadsAreNeverTorn(t *testing.T) {
	ctx := context.Background()
	b, err := blobx.Open(ctx, "file://"+t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	if err := b.WriteAll(ctx, "result.json", []byte(`{"n":0}`), &blob.WriterOptions{ContentType: "application/json"}); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 1; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if err := b.WriteAll(ctx, "result.json", []byte(fmt.Sprintf(`{"n":%d}`, i)), &blob.WriterOptions{ContentType: "application/json"}); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		data, _, err := b.Read(ctx, "result.json")
		if err != nil || !json.Valid(data) {
			close(stop)
			<-done
			t.Fatalf("read during a rewrite = %q, %v", data, err)
		}
	}
	close(stop)
	<-done
}

func TestDeleteExistingIsStrict(t *testing.T) {
	ctx := context.Background()
	g := gcpfake.NewGCS(t)
	g.AddBucket("b", 1, nil)
	for name, b := range map[string]*blobx.Bucket{"gcs": g.Bucket(t, "b"), "mem": blobx.Wrap(memblob.OpenBucket(nil))} {
		if _, err := b.Create(ctx, "k", []byte("v"), "text/plain"); err != nil {
			t.Fatal(err)
		}
		data, gen, err := b.Read(ctx, "k")
		if err != nil {
			t.Fatal(err)
		}
		if name == "mem" { // prev is compared off GCS only
			if err := b.DeleteExisting(ctx, "k", gen, []byte("other")); !errors.Is(err, blobx.ErrConflict) {
				t.Fatalf("changed object: %v", err)
			}
		}
		if err := b.DeleteExisting(ctx, "k", gen, data); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := b.DeleteExisting(ctx, "k", gen, data); !errors.Is(err, blobx.ErrNotExist) {
			t.Fatalf("%s: an already deleted object must be ErrNotExist, got %v", name, err)
		}
		if err := b.DeleteIf(ctx, "k", gen, data); err != nil {
			t.Fatalf("%s: DeleteIf must stay idempotent: %v", name, err)
		}
	}
}
