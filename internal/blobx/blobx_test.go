package blobx_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
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

// TestStrictReadsAgreeWithGetFileOnForbidden: there is one answer for "this
// read was refused", and every strict entry point gives it. ReadStrict,
// ReadMaxStrict and GetFile with strict true all produce ErrForbidden, so a
// caller separating an IAM problem from a missing object asks
// errors.Is(err, blobx.ErrForbidden) once, and this is the answer it must be
// able to rely on: it is the only driver-independent one, because on file://
// or mem:// the same refusal arrives as fs.ErrPermission with no
// googleapi.Error anywhere in the error to inspect. The non-strict entries
// never produce the sentinel — they fold the 403 into ErrNotExist for the
// runner's account, which holds objectUser under an object-name prefix,
// cannot list, and so cannot tell the two apart (design artifact-runs.md §3).
func TestStrictReadsAgreeWithGetFileOnForbidden(t *testing.T) {
	ctx := context.Background()
	b := forbiddenBucket(t)
	dir := t.TempDir()
	for name, err := range map[string]error{
		"ReadStrict":     readErr(b.ReadStrict(ctx, "k")),
		"ReadMaxStrict":  readErr(b.ReadMaxStrict(ctx, "k", 10)),
		"strict GetFile": getFileErr(b.GetFile(ctx, "k", filepath.Join(dir, "k"), "", 1<<20, true)),
	} {
		if !errors.Is(err, blobx.ErrForbidden) {
			t.Fatalf("%s of a 403 = %v, want ErrForbidden", name, err)
		}
	}
	assertDirEmpty(t, dir)
	for name, err := range map[string]error{
		"Read":               readErr(b.Read(ctx, "k")),
		"ReadMax":            readErr(b.ReadMax(ctx, "k", 10)),
		"non-strict GetFile": getFileErr(b.GetFile(ctx, "k", filepath.Join(dir, "k"), "", 1<<20, false)),
	} {
		if errors.Is(err, blobx.ErrForbidden) {
			t.Fatalf("%s of a 403 reported an IAM problem: %v", name, err)
		}
		if !errors.Is(err, blobx.ErrNotExist) {
			t.Fatalf("%s of a 403 = %v, want ErrNotExist", name, err)
		}
	}
	assertDirEmpty(t, dir)
}

// readErr drops the two values of a Read-family call, and getFileErr the same
// for GetFile, so the entries above can be a map of name to error.
func readErr(_ []byte, _ int64, err error) error    { return err }
func getFileErr(_ int64, _ string, err error) error { return err }

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
//	TestPutFileRefusesASourceThatIsNotARegularFile  the regular-file refusal is removed, so a FIFO parks the copy and the test fails on its deadline instead of hanging the package
//	TestCopyBoundedStopsAtTheSizeItWasGiven     the copy's size bound is removed, so a source that never ends is drained and then parked instead of returning
//	TestCopyBoundedStopsAtTheContext            the copy's context bound is removed, so a read parked on a fd no writer sends to is never woken
//	TestPutFileClassifiesARefusalFromTheWriter   every copy failure is reported as a source problem
//	TestGetFileRefusesOverTheLimitAndLeavesNothing  both cap checks are removed — and this one alone is NOT enough to catch the post-copy one: every backend it uses reports an accurate Size(), so the pre-check fires first and the line below is the test that pins the other arm
//	TestGetFileRefusesAnOverCapObjectTheReaderDoesNotAnnounce  the post-copy n > limit check is removed
//	TestGetFileRefusesAMismatchAndLeavesNothing  the hash check is removed (and, separately, the temp file is left behind on failure)
//	TestGetFileWithoutAnExpectedHashStillReturnsOne  the returned hash is of nothing
//	TestGetFileAcceptsAMatchingHash              the expected hash is never accepted (the check becomes "if want != \"\""); separately, EqualFold becomes ==, which fails the upper-case hash; separately, TrimSpace is dropped, which fails the padded one
//	TestGetFileLeavesAPreExistingFileAloneWhenItRefuses  the temp file is dropped and the bytes are written straight to path, so the file that was already there is truncated and overwritten
//	TestGetFileRefusesATruncatedObject            the announced-size check after the copy is removed, so a reader that stops early at EOF is reported as a success and the prefix lands at path
//	TestGetFileRefusesOverTheCapBeforeReadingTheBody  the r.Size() > limit pre-check is removed, so the body is read before the cap can refuse it (the post-copy check still returns ErrTooLarge — only the read count moves)
//	TestPutFileReportsASourceFailureAsTheSource  the source stops being read through sourceReader, so its error is classified as the bucket's and the message stops naming the file
//	TestStrictReadsAgreeWithGetFileOnForbidden   a strict read goes back to returning the bare 403 (read: strictReadErr -> err), so errors.Is(err, ErrForbidden) is false for callers that cannot inspect a googleapi.Error
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
	writeErr      error  // what every Write returns, the real GCS shape
	closes        int    // driver writers closed
	commits       int    // writes that committed
	closeCtxErr   error  // the writer's context at Close time
	data          []byte // the committed object, if any
}

var (
	errAbortClose       = errors.New("fake: close failed")
	errNoObject         = errors.New("fake: no object")
	errFakePrecondition = errors.New("fake: precondition failed")
)

func (d *abortDriver) ErrorCode(err error) gcerrors.ErrorCode {
	switch {
	case errors.Is(err, errNoObject):
		return gcerrors.NotFound
	case errors.Is(err, errFakePrecondition):
		return gcerrors.FailedPrecondition
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

func (w *abortWriter) Write(p []byte) (int, error) {
	if w.d.writeErr != nil {
		return 0, w.d.writeErr
	}
	return w.buf.Write(p)
}

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
	// A regular file whose first Read fails (EIO), which is the shape of a
	// source that errors before any byte is written. It has to be a regular
	// file: one that is not is refused before the copy, so it never reaches
	// the abort rule at all.
	_, _, err := b.PutFile(ctx, "artifacts/x.mp4", unreadableRegularSource(t), "video/mp4", false)
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

// TestPutFileDoesNotFollowASymlinkSource: PutFile is given a path by a caller
// that has decided a moment earlier that the path holds the file it wants, and
// anything the same user is running can replace it in between. The open is
// O_NOFOLLOW, so a link is refused rather than followed: what goes up is never
// the bytes behind a link the caller did not choose, and the refusal names the
// path the caller named like every other source failure.
//
// Mutation (open the source with os.Open): the link is followed, the object is
// written from its target, and this test fails on the error and on the
// object's absence.
func TestPutFileDoesNotFollowASymlinkSource(t *testing.T) {
	ctx := context.Background()
	b := blobx.Wrap(memblob.OpenBucket(nil))
	target := writeSource(t, []byte("the bytes behind the link"))
	link := filepath.Join(t.TempDir(), "src")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	_, _, err := b.PutFile(ctx, "artifacts/x.mp4", link, "video/mp4", false)
	if err == nil {
		t.Fatal("PutFile followed a symlink source")
	}
	if !strings.Contains(err.Error(), "reading "+link) {
		t.Fatalf("the refusal does not name the file the caller named: %v", err)
	}
	if _, _, err := b.Read(ctx, "artifacts/x.mp4"); !errors.Is(err, blobx.ErrNotExist) {
		t.Fatalf("the refused write left an object: %v", err)
	}
}

// fifoSource makes a FIFO and returns its path. With hold it also returns the
// test's own end of it, held open read-write and never written to: a plain
// write end blocks in its open until the reader's, and a FIFO that no writer
// has ever opened reads as an empty file — an end of file, not a source that
// stalls — so holding both ends (which Linux allows, and is why the writer
// here is not a subprocess) is what gives a reader on it a read that parks
// waiting for data that never comes.
func fifoSource(t *testing.T, hold bool) (string, *os.File) {
	t.Helper()
	fifo := filepath.Join(t.TempDir(), "src")
	if out, err := exec.Command("mkfifo", fifo).CombinedOutput(); err != nil {
		t.Fatalf("mkfifo %s: %v: %s", fifo, err, out)
	}
	if !hold {
		return fifo, nil
	}
	w, err := os.OpenFile(fifo, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	return fifo, w
}

// unreadableRegularSource is a path that stats as a regular file and whose
// first read fails: /proc/self/mem's offset 0 is not mapped, so reading it
// answers EIO. That is the shape of a source that fails partway through the
// copy, which is now the only shape that reaches it — anything that is not a
// regular file is refused before the first read.
func unreadableRegularSource(t *testing.T) string {
	t.Helper()
	const p = "/proc/self/mem"
	if _, err := os.Stat(p); err != nil {
		t.Skipf("no %s to read on this machine: %v", p, err)
	}
	return p
}

// TestPutFileRefusesASourceThatIsNotARegularFile: what PutFile streams is a
// file, and the path it is given is one a caller looked at a moment ago — so
// anything the same user is running can have replaced it with something else
// by the time this call opens it. O_NOFOLLOW refuses a link and the copy's
// size bound bounds bytes, but neither bounds time: a FIFO another process
// holds open with nothing to send gives os.NewFile a non-blocking fd that is
// registered with the netpoller, so the first read parks on EAGAIN until a
// writer sends data, and nothing the caller does with its context reaches a
// parked read. The fstat the size bound comes from is already made before any
// read, so the refusal is made there and every caller gets it. A FIFO with no
// writer at all is the other half of the same hole: it reads as an empty file,
// so streamed it would leave an empty object at the key as a success.
//
// Mutation (drop the regular-file refusal): the two FIFO arms never return
// from their first read and this test fails on its own deadline by name
// instead of wedging the package, while the directory and device arms stream
// something that is not a file. Nothing is written before the refusal: the
// driver is never even asked for a writer, which is what d.closes pins.
func TestPutFileRefusesASourceThatIsNotARegularFile(t *testing.T) {
	ctx := context.Background()
	for name, src := range map[string]func(*testing.T) string{
		"a FIFO with an idle writer": func(t *testing.T) string { p, _ := fifoSource(t, true); return p },
		"a FIFO with no writer":      func(t *testing.T) string { p, _ := fifoSource(t, false); return p },
		"a directory":                func(t *testing.T) string { return t.TempDir() },
		"a device":                   func(t *testing.T) string { return "/dev/null" },
	} {
		t.Run(name, func(t *testing.T) {
			path := src(t)
			if _, err := os.Stat(path); err != nil {
				t.Skipf("no %s on this machine: %v", path, err)
			}
			d := &abortDriver{}
			b := blobx.Wrap(blob.NewBucket(d))
			done := make(chan error, 1)
			go func() {
				_, _, err := b.PutFile(ctx, "artifacts/x.mp4", path, "video/mp4", false)
				done <- err
			}()
			select {
			case err := <-done:
				if err == nil {
					t.Fatalf("PutFile streamed %s", path)
				}
				if !errors.Is(err, blobx.ErrChanged) {
					t.Fatalf("PutFile of %s = %v, want ErrChanged", path, err)
				}
				if !strings.Contains(err.Error(), path) {
					t.Fatalf("the refusal does not name the path it refused: %v", err)
				}
			case <-time.After(20 * time.Second):
				t.Fatalf("PutFile of %s did not return: it is not refused before it is read", path)
			}
			if d.commits != 0 || d.closes != 0 {
				t.Fatalf("the refused source was written anyway: commits = %d, closes = %d", d.commits, d.closes)
			}
			if _, _, err := b.Read(ctx, "artifacts/x.mp4"); !errors.Is(err, blobx.ErrNotExist) {
				t.Fatalf("the refused write left an object: %v", err)
			}
		})
	}
}

// TestCopyBoundedStopsAtTheSizeItWasGiven: the copy is bounded by the size
// that was stat'ed, plus the one byte that makes a source that grew visible to
// the caller's n != want check. A FIFO's size is zero and its writer here
// holds the other end with bytes already in it, so the copy reads exactly one
// byte and returns: the source never ends, and the bound is what ends it.
// Without the bound the read drains what is there and then parks for more,
// which is this test's deadline.
func TestCopyBoundedStopsAtTheSizeItWasGiven(t *testing.T) {
	ctx := context.Background()
	fifo, w := fifoSource(t, true)
	if _, err := w.Write([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	f, err := blobx.OpenSource(fifo)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	type result struct {
		n   int64
		err error
	}
	done := make(chan result, 1)
	go func() {
		n, err := blobx.CopyBounded(ctx, io.Discard, f, 0)
		done <- result{n, err}
	}()
	select {
	case got := <-done:
		if got.err != nil || got.n != 1 {
			t.Fatalf("copyBounded of a source that never ends = %d bytes, %v; want 1 and no error", got.n, got.err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the copy of a source that never ends did not return: it is not bounded")
	}
}

// TestCopyBoundedStopsAtTheContext: the byte bound has nothing to bound when a
// source stalls instead of producing — a read parked on a fd that no writer
// sends to is woken by nothing the copy itself does — so the copy also runs
// under a watchdog that closes the source when the context ends, and Close
// evicts the parked read. The error is the context's: the read's own failure
// is this call closing the file under it.
//
// Mutation (drop the watchdog, so the copy is io.Copy alone): this test's
// deadline is what reports it, by name, instead of the package hanging.
func TestCopyBoundedStopsAtTheContext(t *testing.T) {
	fifo, _ := fifoSource(t, true) // idle writer, nothing sent
	f, err := blobx.OpenSource(fifo)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := blobx.CopyBounded(ctx, io.Discard, f, 0)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("copyBounded of a stalled source = %v, want the context's deadline", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the copy of a stalled source outlived its context: nothing closes the source it is parked on")
	}
}

// TestPutFileClassifiesARefusalFromTheWriter: on real GCS the refusal does
// not come from NewWriter or from Close — gcsblob starts the resumable insert
// carrying IfNotExist on the first Write and reports a 412 or a 403 through
// the writer's pipe — so the copy loop's error is the one that has to be
// classified, and a bucket that refused must not read as a bad source file.
func TestPutFileClassifiesARefusalFromTheWriter(t *testing.T) {
	ctx := context.Background()
	src := writeSource(t, []byte("the bytes"))
	for name, tc := range map[string]struct {
		writeErr  error
		notExist  bool
		want      error
		forbidden bool
	}{
		"a lost create-only race is ErrExists":             {errFakePrecondition, true, blobx.ErrExists, false},
		"a lost race without the condition is ErrConflict": {errFakePrecondition, false, blobx.ErrConflict, false},
		"a refused write is ErrForbidden":                  {&googleapi.Error{Code: http.StatusForbidden, Message: "denied"}, false, blobx.ErrForbidden, true},
	} {
		t.Run(name, func(t *testing.T) {
			d := &abortDriver{writeErr: tc.writeErr}
			b := blobx.Wrap(blob.NewBucket(d))
			_, _, err := b.PutFile(ctx, "inputs/shot.png", src, "image/png", tc.notExist)
			if !errors.Is(err, tc.want) {
				t.Fatalf("PutFile = %v, want %v", err, tc.want)
			}
			var ae *googleapi.Error
			if got := errors.As(err, &ae); got != tc.forbidden {
				t.Fatalf("the 403 reachable through errors.As = %v, want %v (%v)", got, tc.forbidden, err)
			}
			if strings.Contains(err.Error(), "reading") {
				t.Fatalf("a refused write was reported as a source problem: %v", err)
			}
			if d.commits != 0 || d.closes != 1 {
				t.Fatalf("commits = %d, closes = %d, want 0 and 1", d.commits, d.closes)
			}
			if !errors.Is(d.closeCtxErr, context.Canceled) {
				t.Fatalf("Close saw ctx.Err() = %v, want the cancel that stops the commit", d.closeCtxErr)
			}
		})
	}
}

// TestPutFileReportsASourceFailureAsTheSource: the two failures that reach the
// copy loop are told apart by which side errored. A source that cannot be read
// keeps reading like one — the message names the file the caller named, and no
// bucket sentinel is set — which is what the tagging is for; without it every
// copy failure is the bucket's, and TestPutFileClassifiesARefusalFromTheWriter
// is the other half of the same rule.
func TestPutFileReportsASourceFailureAsTheSource(t *testing.T) {
	ctx := context.Background()
	src := unreadableRegularSource(t) // stats as a regular file; its first Read fails
	d := &abortDriver{}
	b := blobx.Wrap(blob.NewBucket(d))
	_, _, err := b.PutFile(ctx, "artifacts/x.mp4", src, "video/mp4", false)
	if err == nil {
		t.Fatal("PutFile of an unreadable source succeeded")
	}
	if !strings.Contains(err.Error(), "reading "+src) {
		t.Fatalf("a source failure is not reported as a source problem: %v", err)
	}
	for _, sentinel := range []error{blobx.ErrExists, blobx.ErrConflict, blobx.ErrForbidden} {
		if errors.Is(err, sentinel) {
			t.Fatalf("a source failure was classified as a bucket one (%v): %v", sentinel, err)
		}
	}
	if d.commits != 0 || d.closes != 1 {
		t.Fatalf("commits = %d, closes = %d, want 0 and 1", d.commits, d.closes)
	}
	if !errors.Is(d.closeCtxErr, context.Canceled) {
		t.Fatalf("Close saw ctx.Err() = %v, want the cancel that stops the commit", d.closeCtxErr)
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

// errReadFailure is what a reader that breaks partway through an object
// returns, the shape of a connection dropped mid-download.
var errReadFailure = errors.New("fake: read failed")

// sizedDriver serves one object whose reader reports a size the caller
// chooses: a backend that tells the reader nothing (Size() == -1) or one
// that understates the object. blob.Reader.Size is the driver's
// Attributes().Size, so this is how GetFile's pre-check is made not to fire.
// It also counts the reads of the body and can fail partway through it, so
// "nothing was downloaded" and "the body was never touched" are assertable
// rather than inferred from an error.
type sizedDriver struct {
	driver.Bucket
	data      []byte
	size      int64 // what the reader announces
	failAfter int   // bytes served before the read fails; 0 means never
	reads     int   // Read calls on the body, of every reader it served
}

func (d *sizedDriver) ErrorCode(error) gcerrors.ErrorCode { return gcerrors.Unknown }
func (d *sizedDriver) As(any) bool                        { return false }
func (d *sizedDriver) ErrorAs(error, any) bool            { return false }
func (d *sizedDriver) Close() error                       { return nil }

func (d *sizedDriver) NewRangeReader(context.Context, string, int64, int64, *driver.ReaderOptions) (driver.Reader, error) {
	return &sizedReader{Reader: bytes.NewReader(d.data), d: d}, nil
}

type sizedReader struct {
	*bytes.Reader
	d      *sizedDriver
	served int
}

func (r *sizedReader) Read(p []byte) (int, error) {
	r.d.reads++
	if r.d.failAfter > 0 && r.served >= r.d.failAfter {
		return 0, errReadFailure
	}
	n, err := r.Reader.Read(p)
	r.served += n
	return n, err
}

func (r *sizedReader) Close() error { return nil }
func (r *sizedReader) Attributes() *driver.ReaderAttributes {
	return &driver.ReaderAttributes{Size: r.d.size}
}
func (r *sizedReader) As(any) bool { return false }

// TestGetFileRefusesAnOverCapObjectTheReaderDoesNotAnnounce: the cap is the
// bytes read, not the size the backend claims. Every other backend in these
// tests reports an accurate size, so only a reader with none — or one whose
// number is smaller than the object — reaches the post-copy check, and
// without it the object streams straight past its cap into the destination.
// An understated reader that serves MORE than it announced is refused as
// ErrChanged rather than trusted (TestGetFileRefusesATruncatedObject is the
// same guard from the short side), so the "still arrives" half below uses an
// honest reader: what it pins is that the post-copy check does not refuse a
// legal download.
func TestGetFileRefusesAnOverCapObjectTheReaderDoesNotAnnounce(t *testing.T) {
	ctx := context.Background()
	data := bytes.Repeat([]byte("z"), 100)
	for name, size := range map[string]int64{"no size": -1, "understated": 10} {
		t.Run(name, func(t *testing.T) {
			b := blobx.Wrap(blob.NewBucket(&sizedDriver{data: data, size: size}))
			dir := t.TempDir()
			path := filepath.Join(dir, "a.mp4")
			if _, _, err := b.GetFile(ctx, "artifacts/a.mp4", path, "", 99, true); !errors.Is(err, blobx.ErrTooLarge) {
				t.Fatalf("GetFile of an unannounced 100-byte object under a 99-byte cap = %v, want ErrTooLarge", err)
			}
			assertDirEmpty(t, dir)
			if size >= 0 {
				// A reader that announced 10 and served 100 is a broken
				// backend, and the bytes it served are not what it said they
				// would be: refused, not recorded.
				if _, _, err := b.GetFile(ctx, "artifacts/a.mp4", path, "", 100, true); !errors.Is(err, blobx.ErrChanged) {
					t.Fatalf("GetFile of an understated reader = %v, want ErrChanged", err)
				}
				assertDirEmpty(t, dir)
				return
			}
			// The same object under a cap that fits still arrives.
			if _, _, err := b.GetFile(ctx, "artifacts/a.mp4", path, "", 100, true); err != nil {
				t.Fatalf("GetFile at the cap = %v", err)
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("GetFile at the cap wrote %d bytes, %v", len(got), err)
			}
		})
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

// TestGetFileAcceptsAMatchingHash: sum is the expected hash, and a matching one
// is the ordinary case — both callers pass a real hash (the record's own, the
// task spec's for an input), so the check has to accept it however it is
// written. Every other GetFile test passes "" or a deliberately wrong hash, so
// a check that refused every hash it was given, or compared the hex
// case-sensitively, would fail every download in production with nothing here
// to notice.
func TestGetFileAcceptsAMatchingHash(t *testing.T) {
	ctx := context.Background()
	b := blobx.Wrap(memblob.OpenBucket(nil))
	data := []byte("the render's bytes")
	if err := b.WriteAll(ctx, "artifacts/a.mp4", data, nil); err != nil {
		t.Fatal(err)
	}
	sum := sha256Hex(data)
	for name, given := range map[string]string{
		"lower case":        sum,
		"upper case":        strings.ToUpper(sum),
		"surrounding space": "  " + sum + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "a.mp4")
			n, got, err := b.GetFile(ctx, "artifacts/a.mp4", path, given, 1<<20, true)
			if err != nil || n != int64(len(data)) {
				t.Fatalf("GetFile with the matching hash %q = %d, %v", given, n, err)
			}
			if got != sum {
				t.Fatalf("GetFile returned %s, want the canonical lower-case %s", got, sum)
			}
			back, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(back, data) {
				t.Fatalf("GetFile wrote %q, %v", back, err)
			}
		})
	}
}

// TestGetFileLeavesAPreExistingFileAloneWhenItRefuses: the destination is
// written by renaming the temporary only after the bytes are known good, so a
// file already at path — a re-download over an earlier one, or the caller's
// own file — survives every refusal byte for byte, and no temporary is left
// beside it. Writing straight to path and renaming nothing would satisfy every
// other test here, because they all start with an empty directory.
func TestGetFileLeavesAPreExistingFileAloneWhenItRefuses(t *testing.T) {
	ctx := context.Background()
	old := []byte("the file that was already there")
	data := bytes.Repeat([]byte("z"), 100)
	for name, tc := range map[string]struct {
		driver *sizedDriver
		sum    string
		limit  int64
		want   error
	}{
		"a mismatch":                {&sizedDriver{data: data, size: int64(len(data))}, sha256Hex([]byte("something else")), 1 << 20, blobx.ErrMismatch},
		"an unannounced cap":        {&sizedDriver{data: data, size: -1}, "", 99, blobx.ErrTooLarge},
		"an object that ends early": {&sizedDriver{data: data, size: 1 << 20}, "", 1 << 21, blobx.ErrChanged},
		"a read that fails partway": {&sizedDriver{data: data, size: int64(len(data)), failAfter: 10}, "", 1 << 20, errReadFailure},
	} {
		t.Run(name, func(t *testing.T) {
			b := blobx.Wrap(blob.NewBucket(tc.driver))
			dir := t.TempDir()
			path := filepath.Join(dir, "a.mp4")
			if err := os.WriteFile(path, old, 0o600); err != nil {
				t.Fatal(err)
			}
			_, _, err := b.GetFile(ctx, "artifacts/a.mp4", path, tc.sum, tc.limit, true)
			if !errors.Is(err, tc.want) {
				t.Fatalf("GetFile = %v, want %v", err, tc.want)
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, old) {
				t.Fatalf("the refused download changed the file: %q, %v", got, err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 || entries[0].Name() != "a.mp4" {
				names := make([]string, 0, len(entries))
				for _, e := range entries {
					names = append(names, e.Name())
				}
				t.Fatalf("%s holds %v, want just the pre-existing a.mp4", dir, names)
			}
		})
	}
}

// TestGetFileRefusesATruncatedObject: an object that ends before the size its
// reader announced ends CLEANLY — the reader returns io.EOF, io.Copy returns
// nil, and nothing in the copy loop notices — so without a comparison against
// the announced size the prefix is hashed and renamed into the destination as
// a success. With no expected hash (the ordinary artifact download) the
// caller would then record a hash of partial bytes as the artifact's, and
// with one it would fail as a mismatch against bytes it never fully had. The
// destination must be left empty and the error must name the shortfall.
func TestGetFileRefusesATruncatedObject(t *testing.T) {
	ctx := context.Background()
	const announced = 1 << 20
	served := bytes.Repeat([]byte("z"), 4096) // what the reader actually hands over
	for name, size := range map[string]int64{
		"announced a megabyte, served 4 KiB": announced,
		"announced nothing, served 4 KiB":    0, // the same guard, from the other side
	} {
		t.Run(name, func(t *testing.T) {
			b := blobx.Wrap(blob.NewBucket(&sizedDriver{data: served, size: size}))
			dir := t.TempDir()
			path := filepath.Join(dir, "a.mp4")
			n, sum, err := b.GetFile(ctx, "artifacts/a.mp4", path, "", 1<<21, true)
			if !errors.Is(err, blobx.ErrChanged) {
				t.Fatalf("GetFile of an object that announced %d bytes and served %d = %d, %q, %v; want ErrChanged", size, len(served), n, sum, err)
			}
			if n != 0 || sum != "" {
				t.Fatalf("GetFile returned %d bytes and hash %q for a truncated object", n, sum)
			}
			assertDirEmpty(t, dir)
		})
	}
}

// TestGetFileRefusesOverTheCapBeforeReadingTheBody: when the backend reports the
// object's size the cap is enforced before a byte is transferred, which is the
// point of a cap — an input over the limit must not have its first megabyte
// downloaded, and the runner's fetch must not read what it will not use. The
// post-copy check refuses the same object too, so this pins the early check
// only by what it refuses to read.
func TestGetFileRefusesOverTheCapBeforeReadingTheBody(t *testing.T) {
	ctx := context.Background()
	data := bytes.Repeat([]byte("z"), 100)
	d := &sizedDriver{data: data, size: int64(len(data))}
	b := blobx.Wrap(blob.NewBucket(d))
	dir := t.TempDir()
	path := filepath.Join(dir, "a.mp4")
	if _, _, err := b.GetFile(ctx, "artifacts/a.mp4", path, "", 99, true); !errors.Is(err, blobx.ErrTooLarge) {
		t.Fatalf("GetFile over the announced cap = %v, want ErrTooLarge", err)
	}
	if d.reads != 0 {
		t.Fatalf("GetFile read the body %d times before the cap refused it", d.reads)
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
