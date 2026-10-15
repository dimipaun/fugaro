package blobx_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"gocloud.dev/blob"
	"gocloud.dev/blob/driver"
	"gocloud.dev/blob/fileblob"
	"gocloud.dev/blob/gcsblob"
	"gocloud.dev/blob/memblob"
	"gocloud.dev/gcerrors"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

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
// of `fugaro runs ls` against a run that was still writing its record.
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

// TestReadMaxIsCapped: ReadMax refuses an object over its own cap.
func TestReadMaxIsCapped(t *testing.T) {
	ctx := context.Background()
	b := blobx.Wrap(memblob.OpenBucket(nil))
	if err := b.WriteAll(ctx, "k", make([]byte, 100), nil); err != nil {
		t.Fatal(err)
	}
	if data, _, err := b.ReadMax(ctx, "k", 100); err != nil || len(data) != 100 {
		t.Fatalf("ReadMax at the cap = %d bytes, %v", len(data), err)
	}
	if _, _, err := b.ReadMax(ctx, "k", 99); !errors.Is(err, blobx.ErrTooLarge) {
		t.Fatalf("ReadMax past the cap = %v, want ErrTooLarge", err)
	}
	if _, _, err := b.ReadMax(ctx, "missing", 99); !errors.Is(err, blobx.ErrNotExist) {
		t.Fatalf("ReadMax of a missing key = %v", err)
	}
}

// forbiddenBucket is a real gcsblob bucket whose server answers every GET
// with 403, as GCS does for a missing object when the caller lacks
// storage.objects.list (the job and build service accounts, whose
// objectUser grants are conditioned on an object-name prefix).
func forbiddenBucket(t *testing.T) *blobx.Bucket {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":{"code":403,"message":"denied"}}`)
	}))
	t.Cleanup(srv.Close)
	ctx := context.Background()
	client, err := storage.NewClient(ctx, option.WithEndpoint(srv.URL+"/storage/v1/"), option.WithoutAuthentication(), storage.WithJSONReads())
	if err != nil {
		t.Fatal(err)
	}
	client.SetRetry(storage.WithPolicy(storage.RetryNever))
	gb, err := gcsblob.OpenBucket(ctx, nil, "b", &gcsblob.Options{Client: client})
	if err != nil {
		t.Fatal(err)
	}
	b := &blobx.Bucket{Bucket: gb, GCSName: "b"}
	t.Cleanup(func() { _ = b.Close() })
	return b
}

// TestReadTreatsForbiddenAsAbsent pins the prefix-conditioned grant
// scenario: without storage.objects.list GCS answers a GET of a missing
// object with 403, so Read and ReadMax must report ErrNotExist (callers
// such as the image gate and lock.Acquire rely on it).
func TestReadTreatsForbiddenAsAbsent(t *testing.T) {
	b := forbiddenBucket(t)
	ctx := context.Background()
	if _, _, err := b.Read(ctx, "k"); !errors.Is(err, blobx.ErrNotExist) {
		t.Fatalf("Read of a 403 object = %v, want ErrNotExist", err)
	}
	if _, _, err := b.ReadMax(ctx, "k", 10); !errors.Is(err, blobx.ErrNotExist) {
		t.Fatalf("ReadMax of a 403 object = %v, want ErrNotExist", err)
	}
}

// TestStrictReadsReportAForbiddenObject: the strict variants return the 403.
func TestStrictReadsReportAForbiddenObject(t *testing.T) {
	b := forbiddenBucket(t)
	ctx := context.Background()
	check := func(name string, err error) {
		t.Helper()
		var ae *googleapi.Error
		if err == nil || errors.Is(err, blobx.ErrNotExist) || !errors.As(err, &ae) || ae.Code != http.StatusForbidden {
			t.Fatalf("%s of a forbidden object = %v, want the 403", name, err)
		}
	}
	_, _, err := b.ReadStrict(ctx, "k")
	check("ReadStrict", err)
	_, _, err = b.ReadMaxStrict(ctx, "k", 10)
	check("ReadMaxStrict", err)
}

// TestStrictReadsKeepAbsentAndCap: strict variants differ only on 403.
func TestStrictReadsKeepAbsentAndCap(t *testing.T) {
	ctx := context.Background()
	b := blobx.Wrap(memblob.OpenBucket(nil))
	if err := b.WriteAll(ctx, "k", make([]byte, 100), nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.ReadStrict(ctx, "missing"); !errors.Is(err, blobx.ErrNotExist) {
		t.Fatalf("ReadStrict missing = %v", err)
	}
	if _, _, err := b.ReadMaxStrict(ctx, "k", 99); !errors.Is(err, blobx.ErrTooLarge) {
		t.Fatalf("ReadMaxStrict over cap = %v", err)
	}
	if data, _, err := b.ReadMaxStrict(ctx, "k", 100); err != nil || len(data) != 100 {
		t.Fatalf("ReadMaxStrict at cap = %d, %v", len(data), err)
	}
}

// ReplaceIfType keeps a replaced YAML object application/yaml.
func TestReplaceIfTypeSetsContentType(t *testing.T) {
	ctx := context.Background()
	for name, b := range map[string]*blobx.Bucket{
		"mem": blobx.Wrap(memblob.OpenBucket(nil)),
		"gcs": gcpfake.NewGCS(t).Bucket(t, "runs"),
	} {
		t.Run(name, func(t *testing.T) {
			gen, err := b.Create(ctx, "r.yaml", []byte("a: 1\n"), "application/yaml")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := b.ReplaceIfType(ctx, "r.yaml", []byte("a: 2\n"), "application/yaml", gen, []byte("a: 1\n")); err != nil {
				t.Fatal(err)
			}
			a, err := b.Attributes(ctx, "r.yaml")
			if err != nil || a.ContentType != "application/yaml" {
				t.Fatalf("content type = %v, %v", a, err)
			}
		})
	}
}

// gocloud reports a GCS 403 as NotFound; every write and delete returns
// ErrForbidden for it instead, so a refused write never reads as "absent".
func TestWriteForbiddenIsClassified(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewGCS(t)
	fake.Put("runs", "fugaro/x.yaml", []byte("a: 1\n"))
	fake.DenyWrites("runs", "fugaro/")
	b := fake.Bucket(t, "runs")
	_, gen, err := b.Read(ctx, "fugaro/x.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"create":          func() error { _, err := b.Create(ctx, "fugaro/y.yaml", []byte("b"), "text/plain"); return err }(),
		"replace":         func() error { _, err := b.ReplaceIf(ctx, "fugaro/x.yaml", []byte("{}"), gen, nil); return err }(),
		"put":             b.Put(ctx, "fugaro/x.yaml", []byte("c"), "text/plain"),
		"delete":          b.DeleteIf(ctx, "fugaro/x.yaml", gen, nil),
		"delete existing": b.DeleteExisting(ctx, "fugaro/x.yaml", gen, nil),
	} {
		if !errors.Is(err, blobx.ErrForbidden) {
			t.Errorf("%s: err = %v, want ErrForbidden", name, err)
		}
		if errors.Is(err, blobx.ErrNotExist) || errors.Is(err, blobx.ErrConflict) {
			t.Errorf("%s: a 403 read as %v", name, err)
		}
	}
	// Outside the denied prefix, writes work.
	if err := b.Put(ctx, "runs/s/r/task.json", []byte("{}"), "application/json"); err != nil {
		t.Fatal(err)
	}
}

// Touch's metadata patch is a write too: a refused one is ErrForbidden, not
// a silently ignored no-op.
func TestTouchForbiddenIsClassified(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewGCS(t)
	fake.Put("runs", "fugaro/x.yaml", []byte("a: 1\n"))
	fake.DenyWrites("runs", "fugaro/")
	b := fake.Bucket(t, "runs")
	if err := b.Touch(ctx, "fugaro/x.yaml", time.Now()); !errors.Is(err, blobx.ErrForbidden) {
		t.Fatalf("Touch = %v, want ErrForbidden", err)
	}
	if fake.HasCustomTime("runs", "fugaro/x.yaml") {
		t.Fatal("the refused patch still set the custom time")
	}
}

// --- the streaming pair: PutFile and GetFile (design artifact-runs.md §3) ---
//
// The methods were written before these tests, so every one of them passed on
// its first run. Each was then checked against a hand-applied mutation of
// blobx.go — the mutation named below is the one that makes it fail, so a
// later edit that removes the property takes the test with it:
//
//	TestPutFileStreamsAndHashes                  the returned sha256 is of nothing
//	TestPutFileSetsContentType                   the writer records no content type
//	TestPutFileWithIfNotExistRefusesAnExistingObject  the create-only condition is dropped
//	TestPutFileWithoutIfNotExistOverwrites       the condition is forced create-only, so the second write is refused
//	TestPutFileAbortsTheWriteBeforeClosingOnAFailedRead  the cancel before Close is removed
//	TestPutFileAbortsOnAShortRead                the size-changed check is removed
//	TestGetFileRefusesOverTheLimitAndLeavesNothing  the download caps are removed
//	TestGetFileRefusesAMismatchAndLeavesNothing  the hash check is removed (and, separately, the temp file is left behind on failure)
//	TestGetFileWithoutAnExpectedHashStillReturnsOne  the returned hash is of nothing
//	TestGetFileClassifiesForbidden               strict mode folds a 403 into not-found
//	TestGetFileNonStrictFoldsForbiddenIntoNotExist  non-strict stops folding a 403 into not-found
//	TestGetFileClassifiesMissingAsNotExist       a 404 is reported as an access problem

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// writeSource puts data in a file, the shape both streaming methods take.
func writeSource(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "src")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestPutFileStreamsAndHashes: an object larger than MaxReadBytes goes up
// from a path and comes back byte-identical. The point of the method is that
// the bytes are never a []byte: PutFile takes a path and io.Copy's it, so
// this is the one write path for a render's output (Review Focus 13 — the
// assertion is the round trip, not an allocation count).
func TestPutFileStreamsAndHashes(t *testing.T) {
	ctx := context.Background()
	b, err := blobx.Open(ctx, "file://"+t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	const size = 8 << 20
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i * 7)
	}
	src := writeSource(t, data)
	n, sum, err := b.PutFile(ctx, "artifacts/big.mp4", src, "video/mp4", false)
	if err != nil || n != size {
		t.Fatalf("PutFile = %d bytes, %v", n, err)
	}
	if want := sha256Hex(data); sum != want {
		t.Fatalf("PutFile sha256 = %s, want %s", sum, want)
	}
	back, _, err := b.ReadMax(ctx, "artifacts/big.mp4", size)
	if err != nil || !bytes.Equal(back, data) {
		t.Fatalf("read back %d bytes, %v", len(back), err)
	}
}

// TestPutFileSetsContentType: the streaming write records the content type
// exactly as write does.
func TestPutFileSetsContentType(t *testing.T) {
	ctx := context.Background()
	src := writeSource(t, []byte("bytes"))
	for name, b := range map[string]*blobx.Bucket{
		"mem": blobx.Wrap(memblob.OpenBucket(nil)),
		"gcs": gcpfake.NewGCS(t).Bucket(t, "runs"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := b.PutFile(ctx, "artifacts/a.mp4", src, "video/mp4", false); err != nil {
				t.Fatal(err)
			}
			a, err := b.Attributes(ctx, "artifacts/a.mp4")
			if err != nil || a.ContentType != "video/mp4" {
				t.Fatalf("content type = %v, %v", a, err)
			}
		})
	}
}

// TestPutFileWithIfNotExistRefusesAnExistingObject: the launcher's --attach
// upload is create-only, and a refused one leaves the object's bytes alone.
func TestPutFileWithIfNotExistRefusesAnExistingObject(t *testing.T) {
	ctx := context.Background()
	b := gcpfake.NewGCS(t).Bucket(t, "runs")
	if _, err := b.Create(ctx, "inputs/shot.png", []byte("the first one"), "image/png"); err != nil {
		t.Fatal(err)
	}
	src := writeSource(t, []byte("the second one"))
	if _, _, err := b.PutFile(ctx, "inputs/shot.png", src, "image/png", true); !errors.Is(err, blobx.ErrExists) {
		t.Fatalf("create-only PutFile = %v, want ErrExists", err)
	}
	data, _, err := b.Read(ctx, "inputs/shot.png")
	if err != nil || string(data) != "the first one" {
		t.Fatalf("the refused write changed the object: %q, %v", data, err)
	}
}

// TestPutFileWithoutIfNotExistOverwrites: the artifact upload's own case — it
// is writing a key the run has not written before, and a repeat wins.
func TestPutFileWithoutIfNotExistOverwrites(t *testing.T) {
	ctx := context.Background()
	b, err := blobx.Open(ctx, "file://"+t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	first := writeSource(t, []byte("v1"))
	if _, _, err := b.PutFile(ctx, "artifacts/a.mp4", first, "video/mp4", false); err != nil {
		t.Fatal(err)
	}
	second := []byte("v2, longer than the first")
	n, sum, err := b.PutFile(ctx, "artifacts/a.mp4", writeSource(t, second), "video/mp4", false)
	if err != nil || n != int64(len(second)) || sum != sha256Hex(second) {
		t.Fatalf("second PutFile = %d, %s, %v", n, sum, err)
	}
	back, _, err := b.ReadMax(ctx, "artifacts/a.mp4", len(second))
	if err != nil || !bytes.Equal(back, second) {
		t.Fatalf("read back = %q, %v", back, err)
	}
}

// abortDriver is a bucket driver that records what happened to a write, so
// the abort-before-Close rule is observable: go-cloud calls Close even on an
// aborted write, and only the cancel keeps the object from being committed.
// NewRangeReader serves the one object a successful write would have left,
// so "nothing at key" is asserted through the bucket and not through the
// driver's own fields.
type abortDriver struct {
	driver.Bucket        // nil: only the methods below are reached
	onWriter      func() // runs when a write starts (after PutFile's stat)
	closes        int    // driver writers closed
	commits       int    // writes that committed
	closeCtxErr   error  // the writer's context at Close time
	data          []byte // the committed object, if any
}

var (
	errAbortClose = errors.New("fake: close failed")
	errNoObject   = errors.New("fake: no object")
)

func (d *abortDriver) ErrorCode(err error) gcerrors.ErrorCode {
	if errors.Is(err, errNoObject) {
		return gcerrors.NotFound
	}
	return gcerrors.Unknown
}
func (d *abortDriver) As(any) bool             { return false }
func (d *abortDriver) ErrorAs(error, any) bool { return false }
func (d *abortDriver) Close() error            { return nil }

func (d *abortDriver) NewTypedWriter(ctx context.Context, _, _ string, _ *driver.WriterOptions) (driver.Writer, error) {
	if d.onWriter != nil {
		d.onWriter()
	}
	return &abortWriter{d: d, ctx: ctx}, nil
}

func (d *abortDriver) NewRangeReader(context.Context, string, int64, int64, *driver.ReaderOptions) (driver.Reader, error) {
	if d.data == nil {
		return nil, errNoObject
	}
	return &abortReader{Reader: bytes.NewReader(d.data)}, nil
}

type abortWriter struct {
	d   *abortDriver
	ctx context.Context
	buf bytes.Buffer
}

func (w *abortWriter) Write(p []byte) (int, error) { return w.buf.Write(p) }

// Close commits only what the driver would commit: nothing, once the context
// has been cancelled. It always returns an error, so a PutFile that reported
// Close's error instead of the original one would fail its test.
func (w *abortWriter) Close() error {
	w.d.closes++
	w.d.closeCtxErr = w.ctx.Err()
	if w.ctx.Err() == nil {
		w.d.commits++
		w.d.data = w.buf.Bytes()
	}
	return errAbortClose
}

type abortReader struct{ *bytes.Reader }

func (r *abortReader) Close() error { return nil }
func (r *abortReader) Attributes() *driver.ReaderAttributes {
	return &driver.ReaderAttributes{Size: int64(r.Len())}
}
func (r *abortReader) As(any) bool { return false }

// TestPutFileAbortsTheWriteBeforeClosingOnAFailedRead: a source that fails
// partway through the copy leaves NO object at key, and Close is still called
// (with a cancelled context) because go-cloud requires it.
func TestPutFileAbortsTheWriteBeforeClosingOnAFailedRead(t *testing.T) {
	ctx := context.Background()
	d := &abortDriver{}
	b := blobx.Wrap(blob.NewBucket(d))
	// A directory opens and stats fine and its first Read fails (EISDIR),
	// which is the shape of a source that errors before any byte is
	// written.
	_, _, err := b.PutFile(ctx, "artifacts/x.mp4", t.TempDir(), "video/mp4", false)
	if err == nil {
		t.Fatal("PutFile of an unreadable source succeeded")
	}
	if errors.Is(err, errAbortClose) {
		t.Fatalf("PutFile returned the abort's Close error, not the read error: %v", err)
	}
	if d.commits != 0 || d.data != nil {
		t.Fatalf("the aborted write committed %d times", d.commits)
	}
	if _, _, err := b.Read(ctx, "artifacts/x.mp4"); !errors.Is(err, blobx.ErrNotExist) {
		t.Fatalf("the aborted write left an object: %v", err)
	}
	if d.closes != 1 {
		t.Fatalf("Close was called %d times, want 1", d.closes)
	}
	if !errors.Is(d.closeCtxErr, context.Canceled) {
		t.Fatalf("Close saw ctx.Err() = %v, want the cancel that stops the commit", d.closeCtxErr)
	}
}

// TestPutFileAbortsOnAShortRead: the source shrank between PutFile's stat and
// its copy, so what arrived is not the file the caller named — no object, and
// the size says so.
func TestPutFileAbortsOnAShortRead(t *testing.T) {
	ctx := context.Background()
	src := writeSource(t, bytes.Repeat([]byte("x"), 64<<10))
	d := &abortDriver{onWriter: func() {
		// The write has started, so the stat is already done.
		if err := os.Truncate(src, 10); err != nil {
			t.Error(err)
		}
	}}
	b := blobx.Wrap(blob.NewBucket(d))
	n, _, err := b.PutFile(ctx, "artifacts/x.mp4", src, "video/mp4", false)
	if !errors.Is(err, blobx.ErrChanged) {
		t.Fatalf("PutFile of a shrinking source = %v, want ErrChanged", err)
	}
	if n != 0 {
		t.Fatalf("PutFile reported %d bytes for an aborted write", n)
	}
	if d.commits != 0 || d.closes != 1 {
		t.Fatalf("commits = %d, closes = %d, want 0 and 1", d.commits, d.closes)
	}
	if _, _, err := b.Read(ctx, "artifacts/x.mp4"); !errors.Is(err, blobx.ErrNotExist) {
		t.Fatalf("the aborted write left an object: %v", err)
	}
}

// TestGetFileRefusesOverTheLimitAndLeavesNothing: the cap refuses, it never
// truncates, and the destination directory is left exactly as it was.
func TestGetFileRefusesOverTheLimitAndLeavesNothing(t *testing.T) {
	ctx := context.Background()
	b := blobx.Wrap(memblob.OpenBucket(nil))
	data := bytes.Repeat([]byte("y"), 100)
	if err := b.WriteAll(ctx, "artifacts/a.mp4", data, nil); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "a.mp4")
	if _, _, err := b.GetFile(ctx, "artifacts/a.mp4", path, "", 99, true); !errors.Is(err, blobx.ErrTooLarge) {
		t.Fatalf("GetFile over the cap = %v, want ErrTooLarge", err)
	}
	assertDirEmpty(t, dir)
	if _, _, err := b.GetFile(ctx, "artifacts/a.mp4", path, "", 100, true); err != nil {
		t.Fatalf("GetFile at the cap = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("GetFile at the cap wrote %d bytes, %v", len(got), err)
	}
}

// TestGetFileRefusesAMismatchAndLeavesNothing: the expected hash is an
// argument precisely so a mismatch never reaches the destination, and no
// temporary file is left behind either.
func TestGetFileRefusesAMismatchAndLeavesNothing(t *testing.T) {
	ctx := context.Background()
	b := blobx.Wrap(memblob.OpenBucket(nil))
	data := []byte("the bytes that are actually in the bucket")
	if err := b.WriteAll(ctx, "artifacts/a.mp4", data, nil); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "a.mp4")
	wrong := sha256Hex([]byte("something else"))
	_, _, err := b.GetFile(ctx, "artifacts/a.mp4", path, wrong, 1<<20, true)
	if !errors.Is(err, blobx.ErrMismatch) {
		t.Fatalf("GetFile with a wrong hash = %v, want ErrMismatch", err)
	}
	if !errors.Is(err, blobx.ErrMismatch) || !strings.Contains(err.Error(), wrong) || !strings.Contains(err.Error(), sha256Hex(data)) {
		t.Fatalf("the mismatch error names neither hash: %v", err)
	}
	assertDirEmpty(t, dir)
}

// TestGetFileWithoutAnExpectedHashStillReturnsOne: "" means no check, and the
// caller still gets the hash to record.
func TestGetFileWithoutAnExpectedHashStillReturnsOne(t *testing.T) {
	ctx := context.Background()
	b, err := blobx.Open(ctx, "file://"+t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Close() })
	data := []byte("a render's bytes")
	src := writeSource(t, data)
	if _, _, err := b.PutFile(ctx, "artifacts/a.mp4", src, "video/mp4", false); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "got.mp4")
	n, sum, err := b.GetFile(ctx, "artifacts/a.mp4", path, "", 1<<20, true)
	if err != nil || n != int64(len(data)) || sum != sha256Hex(data) {
		t.Fatalf("GetFile = %d, %s, %v", n, sum, err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("GetFile wrote %q, %v", got, err)
	}
}

// TestGetFileClassifiesForbidden: the CLI's strict mode returns ErrForbidden,
// with the 403 still reachable, because its caller holds objectAdmin (list
// included) and for it a 403 really is an access problem.
func TestGetFileClassifiesForbidden(t *testing.T) {
	ctx := context.Background()
	b := forbiddenBucket(t)
	dir := t.TempDir()
	_, _, err := b.GetFile(ctx, "artifacts/a.mp4", filepath.Join(dir, "a.mp4"), "", 1<<20, true)
	if !errors.Is(err, blobx.ErrForbidden) {
		t.Fatalf("strict GetFile of a 403 = %v, want ErrForbidden", err)
	}
	var ae *googleapi.Error
	if !errors.As(err, &ae) || ae.Code != http.StatusForbidden {
		t.Fatalf("the 403 is not reachable through errors.As: %v", err)
	}
	assertDirEmpty(t, dir)
}

// TestGetFileNonStrictFoldsForbiddenIntoNotExist: the runner's input download
// is the other caller, and it needs the opposite answer — the run's account
// holds objectUser under a prefix condition and cannot list, so GCS answers a
// GET of an input that is not there with 403. Reporting that as an IAM
// problem would send the owner to change grants that are already correct.
func TestGetFileNonStrictFoldsForbiddenIntoNotExist(t *testing.T) {
	ctx := context.Background()
	b := forbiddenBucket(t)
	dir := t.TempDir()
	_, _, err := b.GetFile(ctx, "inputs/shot.png", filepath.Join(dir, "shot.png"), "", 1<<20, false)
	if !errors.Is(err, blobx.ErrNotExist) {
		t.Fatalf("non-strict GetFile of a 403 = %v, want ErrNotExist", err)
	}
	if errors.Is(err, blobx.ErrForbidden) {
		t.Fatalf("the runner's caller saw an IAM problem: %v", err)
	}
	assertDirEmpty(t, dir)
}

// TestGetFileClassifiesMissingAsNotExist: a genuine 404 is ErrNotExist in
// both modes — strict differs from non-strict on a 403 and on nothing else.
func TestGetFileClassifiesMissingAsNotExist(t *testing.T) {
	ctx := context.Background()
	b := gcpfake.NewGCS(t).Bucket(t, "runs")
	dir := t.TempDir()
	for _, strict := range []bool{false, true} {
		_, _, err := b.GetFile(ctx, "artifacts/nope.mp4", filepath.Join(dir, "nope.mp4"), "", 1<<20, strict)
		if !errors.Is(err, blobx.ErrNotExist) {
			t.Fatalf("GetFile of a missing key (strict=%v) = %v, want ErrNotExist", strict, err)
		}
		if errors.Is(err, blobx.ErrForbidden) {
			t.Fatalf("strict=%v: a 404 read as an access problem: %v", strict, err)
		}
	}
	assertDirEmpty(t, dir)
}

// assertDirEmpty pins the "nothing was written" property the way a caller
// would notice it: no destination, and no temporary file beside it either.
func assertDirEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("%s holds %v, want nothing", dir, names)
	}
}
