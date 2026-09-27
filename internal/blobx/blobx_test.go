package blobx_test

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

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
