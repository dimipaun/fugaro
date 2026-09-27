package gcpfake

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

func TestGCSFakeWithRealClient(t *testing.T) {
	ctx := context.Background()
	g := NewGCS(t)
	c := g.Client(t)
	obj := c.Bucket("runs").Object("runs/acme-app/x/task.json")
	w := obj.If(storage.Conditions{DoesNotExist: true}).NewWriter(ctx)
	_, _ = w.Write([]byte("{}"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	gen := w.Attrs().Generation
	w = obj.If(storage.Conditions{DoesNotExist: true}).NewWriter(ctx)
	_, _ = w.Write([]byte("{}"))
	if err := w.Close(); err == nil {
		t.Fatal("DoesNotExist overwrite succeeded")
	}
	r, err := obj.NewReader(ctx)
	if err != nil || r.Attrs.Generation != gen {
		t.Fatalf("reader gen = %v, %v", r, err)
	}
	data, _ := io.ReadAll(r)
	r.Close()
	if string(data) != "{}" {
		t.Fatalf("data = %q", data)
	}
	it := c.Bucket("runs").Objects(ctx, &storage.Query{Prefix: "runs/", Delimiter: "/"})
	a, err := it.Next()
	if err != nil || a.Prefix != "runs/acme-app/" {
		t.Fatalf("prefix listing = %+v, %v", a, err)
	}
	if _, err := it.Next(); !errors.Is(err, iterator.Done) {
		t.Fatalf("second item = %v", err)
	}
	if err := obj.If(storage.Conditions{GenerationMatch: gen + 1}).Delete(ctx); err == nil {
		t.Fatal("mismatched delete succeeded")
	}
	if err := obj.If(storage.Conditions{GenerationMatch: gen}).Delete(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := obj.Attrs(ctx); !errors.Is(err, storage.ErrObjectNotExist) {
		t.Fatalf("after delete: %v", err)
	}
}

// TestGCSFakeResumableUpload covers the path the client takes above its
// chunk size: an initiating POST, then chunks sent to the session URL.
func TestGCSFakeResumableUpload(t *testing.T) {
	ctx := context.Background()
	g := NewGCS(t)
	obj := g.Client(t).Bucket("runs").Object("cache/big")
	want := bytes.Repeat([]byte("0123456789abcdef"), 40_000) // 640 KB: three 256 KiB chunks
	w := obj.If(storage.Conditions{DoesNotExist: true}).NewWriter(ctx)
	w.ChunkSize = 256 * 1024
	if _, err := w.Write(want); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := obj.NewReader(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(r)
	r.Close()
	if !bytes.Equal(got, want) || r.Attrs.Generation != w.Attrs().Generation {
		t.Fatalf("read %d bytes at generation %d, wrote %d at %d", len(got), r.Attrs.Generation, len(want), w.Attrs().Generation)
	}
	var chunks int
	for _, req := range g.Requests() {
		if strings.Contains(req.Query, "upload_id=") {
			chunks++
		}
	}
	if chunks < 3 {
		t.Fatalf("%d resumable chunks, want at least 3", chunks)
	}
}
