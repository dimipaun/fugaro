// Package blobx adds the few conditional operations Fugaro needs beyond
// gocloud's portable blob API: create-if-absent with the new generation,
// generation-matched replace and delete, and GCS custom time (design §4.1).
// On GCS they are atomic. On other drivers (file:// and mem://, used by
// local runs and tests with a single writer) replace and delete compare
// contents first, which is NOT atomic.
package blobx

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
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"gocloud.dev/blob"
	_ "gocloud.dev/blob/fileblob" // file:// buckets
	_ "gocloud.dev/blob/gcsblob"  // gs:// buckets
	_ "gocloud.dev/blob/memblob"  // mem:// buckets
	"gocloud.dev/gcerrors"
	"google.golang.org/api/googleapi"
)

// Bucket is a blob bucket and, when it is GCS, its bucket name.
type Bucket struct {
	*blob.Bucket
	GCSName string
}

var (
	// ErrConflict means a conditional write or delete lost a race.
	ErrConflict = errors.New("the object changed")
	// ErrExists means Create found the object already there.
	ErrExists = errors.New("object already exists")
	// ErrDestExists means a download's destination path was already taken and
	// the caller asked for a placement that does not replace it
	// (GetFileExactly with replace false, which is `fugaro artifacts get`
	// without --force). It is the same answer as the caller's own pre-check
	// gives for a file it can see, and this is the one that also covers what
	// the pre-check cannot: a file that appeared after it, and a path this
	// filesystem folds onto an existing one.
	ErrDestExists = errors.New("the destination already exists")
	// ErrForbidden means GCS refused a read, a write or a delete for lack
	// of permission (HTTP 403). gocloud reports a 403 as NotFound, so
	// without it a refused write would read as a missing object
	// (docs/design/bucket-iam.md §2.3). On GCS it is produced by every
	// write, by every delete and by every STRICT read (ReadStrict,
	// ReadMaxStrict, GetFile with strict true); a non-strict read never
	// produces it, folding a 403 into ErrNotExist on purpose (see Read and
	// GetFile).
	//
	// It is the GCS answer and nothing more: it is made from a 403, so a
	// driver with no 403 to report keeps its own refusal instead — file://
	// and mem:// refuse with fs.ErrPermission, which reaches the caller
	// unchanged. "Not ErrForbidden" therefore does not mean "not a
	// permission problem", and a caller deciding between an access problem
	// and a missing object asks errors.Is(err, ErrForbidden) first and
	// then its driver's own refusal (internal/cli's isAccessDenied checks
	// both, plus gcerrors.PermissionDenied and a 401).
	ErrForbidden = errors.New("permission denied")
	// ErrNotExist means Read found no object.
	ErrNotExist = errors.New("object does not exist")
	// ErrTooLarge means Read found an object larger than MaxReadBytes.
	ErrTooLarge = errors.New("object is too large")
	// ErrMismatch means GetFile read an object whose sha256 is not the one
	// its caller expected. Nothing is written to the destination.
	ErrMismatch = errors.New("the object's hash does not match")
	// ErrChanged means a streaming call read a different number of bytes
	// than the size the other end announced. In PutFile the source changed
	// while it was uploaded and nothing is committed to the bucket; in
	// GetFile the object ended before its announced size and nothing is
	// written to the destination.
	ErrChanged = errors.New("the file changed while it was read")
)

// MaxReadBytes caps what Read reads. Its objects (locks, launch claims,
// run records) are small JSON that a run's own service account, and so
// its agent, can write; a hostile one of gigabytes must not be read whole.
const MaxReadBytes = 1 << 20

// Open opens a gocloud bucket URL (gs://, file://, mem://).
func Open(ctx context.Context, rawURL string) (*Bucket, error) {
	rawURL = fileURLWithoutSidecars(rawURL)
	b, err := blob.OpenBucket(ctx, rawURL)
	if err != nil {
		return nil, fmt.Errorf("opening bucket %s: %w", rawURL, err)
	}
	out := &Bucket{Bucket: b}
	if u, err := url.Parse(rawURL); err == nil && u.Scheme == "gs" {
		out.GCSName = u.Host
	}
	return out, nil
}

// fileURLWithoutSidecars adds metadata=skip to a file:// bucket URL that
// does not set it. By default fileblob keeps each object's attributes in a
// "<key>.attrs" sidecar that it truncates and rewrites in place before it
// renames the data file into place, so a reader that opens the object
// while it is being rewritten finds an empty sidecar and fails with EOF
// (the data file itself is always whole). A real bucket cannot do that,
// and nothing Fugaro reads off a file bucket lives in the sidecar, so
// without it a read sees the old object or the new one, never a torn one.
func fileURLWithoutSidecars(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "file" {
		return rawURL
	}
	q := u.Query()
	if q.Has("metadata") {
		return rawURL
	}
	q.Set("metadata", "skip")
	u.RawQuery = q.Encode()
	return u.String()
}

// Wrap treats b as a non-GCS bucket. Production code opens buckets with
// Open; Wrap stays exported because the tests of several packages (cache,
// lock, runstore, runner, cli) wrap an in-memory or fault-injecting
// gocloud bucket with it, and an export_test.go serves only its own
// package.
func Wrap(b *blob.Bucket) *Bucket { return &Bucket{Bucket: b} }

func (b *Bucket) client() *storage.Client {
	var c *storage.Client
	if b.GCSName == "" || !b.As(&c) {
		return nil
	}
	return c
}

// write writes data to key, optionally conditioned by cond (GCS only), and
// returns the new generation (0 off GCS).
func (b *Bucket) write(ctx context.Context, key string, data []byte, contentType string, ifNotExist bool, cond *storage.Conditions) (int64, error) {
	var sw *storage.Writer
	opts := &blob.WriterOptions{ContentType: contentType, IfNotExist: ifNotExist, BeforeWrite: func(as func(any) bool) error {
		var oh **storage.ObjectHandle
		if cond != nil && as(&oh) {
			*oh = (*oh).If(*cond)
		}
		as(&sw) // must come after the ObjectHandle (gcsblob's documented order)
		return nil
	}}
	if err := b.WriteAll(ctx, key, data, opts); err != nil {
		return 0, writeErr(err, ifNotExist)
	}
	if sw != nil && sw.Attrs() != nil {
		return sw.Attrs().Generation, nil
	}
	return 0, nil
}

// writeErr classifies a failed write, the same way for WriteAll and for the
// streaming PutFile: a lost precondition is ErrExists for a create-only
// write and ErrConflict otherwise, and a 403 is ErrForbidden (gocloud
// reports a GCS 403 as NotFound, so without it a refused write would read
// as a missing object).
func writeErr(err error, ifNotExist bool) error {
	if gcerrors.Code(err) == gcerrors.FailedPrecondition {
		if ifNotExist {
			return ErrExists
		}
		return ErrConflict
	}
	if isForbidden(err) {
		return fmt.Errorf("%w: %w", ErrForbidden, err)
	}
	return err
}

// sourceReader tags every error its file produces, so PutFile can tell a
// source that failed from a bucket that refused the write: both leave the
// same io.Copy.
type sourceReader struct{ r io.Reader }

// sourceReadErr is a failure of the source file itself.
type sourceReadErr struct{ err error }

func (e sourceReadErr) Error() string { return e.err.Error() }
func (e sourceReadErr) Unwrap() error { return e.err }

func (s sourceReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		err = sourceReadErr{err}
	}
	return n, err
}

// copyAborted is a copy that stopped because the context ended and not because
// the source or the bucket failed and not because the source ran out: the fd
// was closed under a read that was parked, so the error the read reports is
// this call's own doing and the context's error is the honest one.
type copyAborted struct{ err error }

func (e copyAborted) Error() string { return e.err.Error() }
func (e copyAborted) Unwrap() error { return e.err }

// copyBounded copies src into dst, bounded twice: by the size that was
// stat'ed plus one byte, and by ctx. want is that size, so what arrives is at
// most one byte more than the file this call accounted for — a source that
// grows while it is copied (a writer that was never stopped) is refused by the
// caller's own n != want check rather than drained. Without the byte bound a
// source that keeps producing never reaches an end at all.
//
// The context bound is what stops a copy of a source that *stalls*, which no
// byte bound can: a non-blocking FIFO that some other process holds open with
// nothing to send reads as a fd parked on EAGAIN, and a parked read is only
// woken by something else touching the fd. Closing it is that something —
// Close evicts the parked read (internal/poll's evict, which is why the read
// comes back "file already closed" rather than blocking on) — so the watchdog
// closes src when ctx is done and the copy returns copyAborted around ctx.Err.
// src is PutFile's own fd and PutFile closes it again on the way out, which is
// harmless; the stop returned by context.AfterFunc is what keeps the watchdog
// from closing a source whose copy has already finished.
func copyBounded(ctx context.Context, dst io.Writer, src *os.File, want int64) (int64, error) {
	stop := context.AfterFunc(ctx, func() { src.Close() })
	defer stop()
	n, err := io.Copy(dst, io.LimitReader(sourceReader{src}, want+1))
	if err != nil && ctx.Err() != nil && errors.Is(err, fs.ErrClosed) {
		return n, copyAborted{ctx.Err()}
	}
	return n, err
}

// Create writes key only if it does not exist (ErrExists otherwise).
func (b *Bucket) Create(ctx context.Context, key string, data []byte, contentType string) (int64, error) {
	return b.write(ctx, key, data, contentType, true, nil)
}

// Read returns key's content and generation (0 off GCS) from one reader.
// An object larger than MaxReadBytes is ErrTooLarge. An HTTP 403 is
// ErrNotExist, as gocloud's GCS driver maps it: the job and build service
// accounts hold storage.objectUser only under a prefix condition, so they
// lack storage.objects.list and GCS answers a GET of a missing object with
// 403. Callers that treat "absent" as normal depend on this.
func (b *Bucket) Read(ctx context.Context, key string) ([]byte, int64, error) {
	return b.read(ctx, key, MaxReadBytes, false)
}

// ReadMax is Read with a cap of limit bytes instead of MaxReadBytes: an
// object larger is ErrTooLarge, and no more than limit+1 bytes are read.
func (b *Bucket) ReadMax(ctx context.Context, key string, limit int) ([]byte, int64, error) {
	return b.read(ctx, key, limit, false)
}

// ReadStrict is Read, except that an HTTP 403 is ErrForbidden (with the
// googleapi.Error still reachable through errors.As) rather than
// ErrNotExist, and a 404 stays ErrNotExist. The shared-config readers
// (internal/cli/sharedcfg.go) and fugaro image refresh's record read may use
// the strict variants: they run as launchers or operators holding
// unconditional objectAdmin (which includes list), for whom a 403 means no
// access. internal/budget/token's ObjectPresent uses it for a different
// reason: it runs as the job account, whose grant on tokens/ is a conditional
// objectViewer without list, so a 403 there means "missing, or not readable",
// and the strict read keeps that answer apart from a 404 for its caller to
// weigh (the runner's refuseStrayToken) instead of folding it into absence.
func (b *Bucket) ReadStrict(ctx context.Context, key string) ([]byte, int64, error) {
	return b.read(ctx, key, MaxReadBytes, true)
}

// ReadMaxStrict is ReadMax with ReadStrict's treatment of a 403.
func (b *Bucket) ReadMaxStrict(ctx context.Context, key string, limit int) ([]byte, int64, error) {
	return b.read(ctx, key, limit, true)
}

// strictReadErr classifies the error from a failed open, and it is the one
// place a strict read and a non-strict one differ: a strict caller is told
// ErrForbidden for an HTTP 403 (the googleapi.Error stays reachable through
// errors.As, for the callers that want the detail — internal/cli's
// isAccessDenied is both shapes), a non-strict caller has already had that
// 403 folded into ErrNotExist by the check above.
//
// What strictness guarantees is that a refusal is never reported as
// ErrNotExist. ErrForbidden is the answer for the GCS half of that, and the
// one a caller should ask for first, because it saves it from knowing about
// googleapi at all — but it is made from a 403 and a non-GCS driver has none
// to make it from: file:// and mem:// refuse with fs.ErrPermission, which
// this returns as itself, and no strict read of theirs produces the
// sentinel. A caller deciding between an access problem and a missing
// object must therefore check both (internal/cli's isAccessDenied is that
// pair, plus gcerrors.PermissionDenied and a 401).
func strictReadErr(err error, strict bool) error {
	if strict && isForbidden(err) {
		return fmt.Errorf("%w: %w", ErrForbidden, err)
	}
	return err
}

func (b *Bucket) read(ctx context.Context, key string, limit int, strict bool) ([]byte, int64, error) {
	r, err := b.NewReader(ctx, key, nil)
	if gcerrors.Code(err) == gcerrors.NotFound && !(strict && isForbidden(err)) {
		return nil, 0, ErrNotExist
	}
	if err != nil {
		return nil, 0, strictReadErr(err, strict)
	}
	defer r.Close()
	if r.Size() > int64(limit) {
		return nil, 0, fmt.Errorf("%s: %w (%d bytes, the cap is %d)", key, ErrTooLarge, r.Size(), limit)
	}
	data, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if err != nil {
		return nil, 0, err
	}
	if len(data) > limit {
		return nil, 0, fmt.Errorf("%s: %w (the cap is %d bytes)", key, ErrTooLarge, limit)
	}
	var sr *storage.Reader
	if r.As(&sr) {
		return data, sr.Attrs.Generation, nil
	}
	return data, 0, nil
}

// isForbidden reports an HTTP 403 from GCS.
func isForbidden(err error) bool {
	var ae *googleapi.Error
	return errors.As(err, &ae) && ae.Code == http.StatusForbidden
}

// ReplaceIf overwrites key with data only if it is still generation gen
// (GCS) or still holds prev (other drivers), and returns the new generation.
// ErrConflict means the object changed or vanished; any other failure is
// returned as itself. A data that is valid JSON (every current caller's) is
// stored as application/json; anything else keeps gocloud's content sniffing.
func (b *Bucket) ReplaceIf(ctx context.Context, key string, data []byte, gen int64, prev []byte) (int64, error) {
	ct := ""
	if json.Valid(data) {
		ct = "application/json"
	}
	return b.ReplaceIfType(ctx, key, data, ct, gen, prev)
}

// ReplaceIfType is ReplaceIf with the new object's content type given ("" for
// the driver's default), so a replaced YAML object stays application/yaml.
func (b *Bucket) ReplaceIfType(ctx context.Context, key string, data []byte, ct string, gen int64, prev []byte) (int64, error) {
	if b.client() != nil {
		if gen == 0 {
			return 0, fmt.Errorf("replacing %s: no generation to match", key)
		}
		return b.write(ctx, key, data, ct, false, &storage.Conditions{GenerationMatch: gen})
	}
	cur, _, err := b.Read(ctx, key)
	switch {
	case errors.Is(err, ErrNotExist):
		return 0, ErrConflict
	case err != nil:
		return 0, fmt.Errorf("replacing %s: %w", key, err)
	case !bytes.Equal(cur, prev):
		return 0, ErrConflict
	}
	return b.write(ctx, key, data, ct, false, nil)
}

// DeleteIf deletes key only if it is still generation gen (GCS) or still
// holds prev (other drivers). A missing object is not an error.
func (b *Bucket) DeleteIf(ctx context.Context, key string, gen int64, prev []byte) error {
	if c := b.client(); c != nil {
		if gen == 0 {
			return fmt.Errorf("deleting %s: no generation to match", key)
		}
		err := c.Bucket(b.GCSName).Object(key).If(storage.Conditions{GenerationMatch: gen}).Delete(ctx)
		var ae *googleapi.Error
		switch {
		case err == nil, errors.Is(err, storage.ErrObjectNotExist):
			return nil
		case errors.As(err, &ae) && ae.Code == http.StatusPreconditionFailed:
			return ErrConflict
		case errors.As(err, &ae) && ae.Code == http.StatusForbidden:
			return fmt.Errorf("%w: %w", ErrForbidden, err)
		}
		return err
	}
	cur, _, err := b.Read(ctx, key)
	if errors.Is(err, ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !bytes.Equal(cur, prev) {
		return ErrConflict
	}
	return b.Delete(ctx, key)
}

// DeleteExisting is DeleteIf for a caller that must be the one who deleted
// the object (a single-use token): an object already gone is ErrNotExist,
// not success, and a changed object is ErrConflict. Of several callers that
// each read the object and then call DeleteExisting, exactly one gets nil
// (atomic on GCS; on file:// and mem:// the final delete of a missing object
// still fails, so a race there is lost by all but one as well).
func (b *Bucket) DeleteExisting(ctx context.Context, key string, gen int64, prev []byte) error {
	if c := b.client(); c != nil {
		if gen == 0 {
			return fmt.Errorf("deleting %s: no generation to match", key)
		}
		err := c.Bucket(b.GCSName).Object(key).If(storage.Conditions{GenerationMatch: gen}).Delete(ctx)
		var ae *googleapi.Error
		switch {
		case err == nil:
			return nil
		case errors.Is(err, storage.ErrObjectNotExist):
			return ErrNotExist
		case errors.As(err, &ae) && ae.Code == http.StatusPreconditionFailed:
			return ErrConflict
		case errors.As(err, &ae) && ae.Code == http.StatusForbidden:
			return fmt.Errorf("%w: %w", ErrForbidden, err)
		}
		return err
	}
	cur, _, err := b.Read(ctx, key)
	if err != nil {
		return err
	}
	if !bytes.Equal(cur, prev) {
		return ErrConflict
	}
	if err := b.Delete(ctx, key); err != nil {
		if gcerrors.Code(err) == gcerrors.NotFound {
			return ErrNotExist
		}
		return err
	}
	return nil
}

// Put writes key whatever is there (last writer wins). A 403 is
// ErrForbidden, as for the conditional writes.
func (b *Bucket) Put(ctx context.Context, key string, data []byte, contentType string) error {
	_, err := b.write(ctx, key, data, contentType, false, nil)
	return err
}

// PutFile streams path to key with contentType, hashing the bytes as they
// go, and returns the number of bytes written and the lower-case hex sha256
// of what it wrote. Unlike Put, which takes a []byte, this is the write path
// for an object that can be large (a render's output, an attachment) and it
// never holds the file in memory (design artifact-runs.md §3).
//
// ifNotExist is Create's create-only condition, carried into the streaming
// write through the same WriterOptions the existing write uses and
// classified the same way (ErrExists). It is a parameter and not a separate
// method because the caller that needs it is the same caller that needs the
// streaming path: an attachment cannot go through Create, which takes a
// []byte. The condition holds on gocloud's file:// driver too, but it is
// still only race hardening — a condition that fires mid-upload leaves the
// earlier objects written.
//
// On any error it aborts the write before it closes it. go-cloud requires
// Close to be called even for an aborted write, and only cancelling the
// context stops the object being committed — Close alone commits whatever
// arrived. So PutFile owns a per-call cancel and, on a source read error or
// on a source that changed size while it was read, cancels first and then
// closes, returning the original error and not Close's. Without that rule a
// failed upload would leave a truncated object at a key the caller recorded
// (blob.go:436-456 is the same pattern in go-cloud's own MD5 path).
//
// A refused write is classified wherever it surfaces, and on real GCS that
// is the copy loop: gcsblob starts the resumable insert on the first Write,
// carrying IfNotExist as its precondition, and reports a 412 or a 403
// through the writer's pipe — so a create that lost a race, or a write the
// account may not make, arrives as the io.Copy error and not from NewWriter
// or Close. The source is therefore read through a reader that tags its own
// errors, which is what tells the two apart: a tagged error is the file,
// anything else is the bucket, and the bucket's is classified with writeErr
// exactly as WriteAll's is. (Failing the driver's Write is also the only way
// to reach this path at all: gocloud buffers a non-file driver's bytes until
// Close, so a test driver that fails there is how the classification is
// pinned.)
//
// What is streamed is a regular file, and nothing else, checked on the fd this
// call opened and before a byte is read. O_NOFOLLOW refuses a symlink and the
// size bound bounds bytes, but neither bounds *time*: a path replaced by a
// FIFO that another process holds open with nothing to send gives os.NewFile a
// non-blocking fd that is registered with the netpoller, so the first Read
// parks on EAGAIN until a writer sends data, and no context reaches a parked
// read. Close does — it evicts the parked read — which is why the copy is run
// under a watchdog that closes the source when the context ends: a source that
// is refused outright costs nothing, and one that stalls is bounded by the
// caller's own deadline instead of by the container's.
func (b *Bucket) PutFile(ctx context.Context, key, path, contentType string, ifNotExist bool) (size int64, sum string, err error) {
	// OpenSource, not os.Open: the path is opened without following a final
	// symlink and without blocking on a FIFO, because between a caller's own
	// check of this path and this line anything the same user runs may have
	// replaced it — a link to /dev/zero would otherwise stream until the
	// container is killed. The refusal is reported as a source failure, the
	// same as any other: it is the file the caller named that could not be
	// read, and no bucket did anything.
	f, err := OpenSource(path)
	if err != nil {
		return 0, "", fmt.Errorf("reading %s: %w", path, err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, "", err
	}
	// The fstat the copy's size bound comes from is already made here, so the
	// regular-file check costs nothing, happens before any read, and every
	// caller gets it. A directory (its first read is EISDIR), a device, a
	// socket and a FIFO are all refusals rather than streams: a FIFO with an
	// idle writer is the one that never returns, but none of them is the file
	// a caller staged. ErrChanged is the sentinel because that is exactly what
	// happened to the path — it was one thing when the caller looked at it and
	// another when this call opened it — and it is the same answer
	// runstore.PutFileFrom gives a staged path that is not a regular file.
	if !st.Mode().IsRegular() {
		return 0, "", fmt.Errorf("%s: %w (it is not a regular file)", path, ErrChanged)
	}
	return b.PutFileOpened(ctx, key, f, st, contentType, ifNotExist)
}

// PutFileOpened is PutFile for a caller that already holds the file open, and
// it is the whole of PutFile below the open: it re-checks the handle, streams
// exactly size bytes of it, and returns what it wrote. Only the holder of a
// long-lived handle can do what PutFile cannot — judge the file's caps on the
// very fd it then uploads, so the bytes that go up are the bytes that were
// judged, and a file that grew or shrank in between is a refusal rather than
// an upload of something nobody checked (internal/cli's --attach, whose caps
// are checked before the launch's later gates and whose upload runs after
// them: between the two the path may be a different file entirely).
//
// size must be the size the caller read from this same handle. It is the
// copy's bound and not a fresh fstat: a file that grew since the caller looked
// reads one byte past what was judged, which copyBounded's bound allows and
// the n != size check below refuses, so what leaves is never more than the
// size the caller checked against its cap plus one byte. f's own fstat is
// still re-checked here (it costs nothing on an open fd) so that no caller can
// reach the copy with a directory, a device or a FIFO — GetFile-staged
// attachments and the launcher's judged files are the two callers, and only
// one of them is in this module. The handle is closed by the caller, not here.
func (b *Bucket) PutFileOpened(ctx context.Context, key string, src *os.File, st fs.FileInfo, contentType string, ifNotExist bool) (size int64, sum string, err error) {
	path := src.Name()
	if !st.Mode().IsRegular() {
		return 0, "", fmt.Errorf("%s: %w (it is not a regular file)", path, ErrChanged)
	}
	want := st.Size()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	w, err := b.NewWriter(ctx, key, &blob.WriterOptions{ContentType: contentType, IfNotExist: ifNotExist})
	if err != nil {
		return 0, "", writeErr(err, ifNotExist)
	}
	h := sha256.New()
	n, err := copyBounded(ctx, io.MultiWriter(w, h), src, want)
	if err != nil {
		// The abort rule: cancel, then close, and report the cause rather
		// than whatever Close says about the aborted write.
		cancel()
		_ = w.Close()
		// A copy the watchdog cut short is the context's, not the source's:
		// the fd was closed under a read that was never going to return, so
		// what the read reports is this call's own doing.
		var ab copyAborted
		if errors.As(err, &ab) {
			return 0, "", fmt.Errorf("reading %s: %w", path, ab.err)
		}
		var se sourceReadErr
		if errors.As(err, &se) {
			return 0, "", fmt.Errorf("reading %s: %w", path, se.err)
		}
		return 0, "", writeErr(err, ifNotExist)
	}
	if n != want {
		cancel()
		_ = w.Close()
		return 0, "", fmt.Errorf("%s: %w (read %d bytes, its size said %d)", path, ErrChanged, n, want)
	}
	if err := w.Close(); err != nil {
		return 0, "", writeErr(err, ifNotExist)
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// GetFile streams key into path, up to limit bytes, hashing as it goes, and
// returns the size and the lower-case hex sha256 of what it wrote. Nothing
// is ever placed at path unless the whole object arrived: the bytes go to a
// temporary file in the destination's own directory which is renamed into
// place only after the hash is checked, so a mismatch, an over-cap object or
// a truncated one removes the temp file and leaves no destination at all.
// The cap refuses, it never truncates — the object's own size is checked
// first when the backend reports one (file:// and GCS both do) — and an
// object that ends before its announced size is ErrChanged, because a reader
// that stops early stops cleanly and the copy would otherwise report success
// over a prefix of the object.
//
// sum is the *expected* hash (the record's own, or the task spec's for an
// input): when it is non-empty and does not match what was read, the result
// is ErrMismatch with both hashes in the message and nothing at path. It is
// an argument because both callers must keep the "nothing was written"
// property: a caller that checked afterwards would find the mismatching file
// already at its final path. An empty sum means no check, and the hash comes
// back for the caller to record.
//
// strict decides how an HTTP 403 is classified, and the two callers need
// opposite answers (design artifact-runs.md §3). It is the same rule, through
// the same helper, as ReadStrict's:
//
//   - strict (fugaro artifacts get): a 403 is ErrForbidden, with the
//     googleapi.Error still reachable through errors.As, and a 404 stays
//     ErrNotExist. As ErrForbidden's own comment says, that is the GCS
//     answer: the sentinel is made from a 403 alone, so a non-GCS driver
//     keeps its own refusal (fs.ErrPermission) and a caller that wants both
//     checks both. This caller holds unconditional objectAdmin, which
//     includes storage.objects.list (docs/design/bucket-iam.md:88,126-128),
//     so for it a 403 really is an access problem and "no such artifact"
//     would be a lie.
//   - non-strict (the runner's input download): a 403 folds into
//     ErrNotExist, exactly as Read does. The run's own account holds
//     storage.objectUser under a prefix condition and CANNOT list, so GCS
//     answers a GET of a missing object with 403, not 404 (blobx.go's Read).
//     A strict caller here would report a missing input as an IAM problem
//     and send the owner to change grants that are already correct.
//
// Two things this deliberately does not do, named for whoever picks them up:
//
//   - os.CreateTemp creates the temporary 0600, and the object's own mode is
//     not carried onto it, so a downloaded artifact is not executable even if
//     the one that was uploaded was. Nothing reads an artifact's mode today
//     (result.json records name, size, sha256 and content type, §8.1); if a
//     later slice does, this is the line that has to change.
//   - the destination's base name is used as-is in the temporary's name, so a
//     name already close to NAME_MAX can exceed it once the random suffix is
//     appended, and the download fails with ENAMETOOLONG before anything is
//     read. A name that long is a naming question, and naming is where the
//     sanitiser lives (internal/runner/artifact.go's sanitiseName, the plan's
//     Task 4). GetFile itself sanitises nothing: it is given a path by its
//     caller, and refusing the names that must not be handed to it — a
//     `../` out of the artifacts root above all — is fugaro artifacts get's
//     job (the same design's §9, the plan's Task 7).
//
// limit is an upper bound and not an equality, and the difference matters to
// the caller that knows the object's exact size (a run record's byte count):
// comparing what this returns with what the caller expected happens after the
// rename, which is too late to do anything but delete the file this method
// just placed — and with --force, deleting whatever was at that path before.
// GetFileExactly is that caller's entry point, and it checks the size where
// the hash is checked: on the temporary file, before the rename.
func (b *Bucket) GetFile(ctx context.Context, key, path, sum string, limit int64, strict bool) (size int64, sha string, err error) {
	return b.getFile(ctx, key, path, sum, limit, -1, strict, true)
}

// GetFileExactly is GetFile for the caller that knows the object's exact size
// as well as its hash — `fugaro artifacts get`, whose run record carries the
// byte count it uploaded — and it is checked in the same place and at the same
// time as the hash: inside, on the temporary file, BEFORE the rename. A size
// the object does not have is ErrChanged with both numbers in the message, and
// nothing is placed at path.
//
// That placement is the whole point of the method. GetFile's limit is an upper
// bound, so a caller that compared the size afterwards would be comparing it
// to a file that is already at its final path — and both ways out of that are
// wrong: leaving it there breaks "nothing is written unless it is what was
// asked for", and removing it removes whatever was at that path before the
// rename. With --force that is the operator's own file, which this package
// never wrote and must not delete.
//
// want is also the cap, so an object larger than the recorded size is
// ErrTooLarge before its body is read, exactly as with GetFile's limit.
//
// replace says whether path may be replaced. With it, the verified temporary
// file is renamed onto path, which is what `--force` asks for. Without it,
// nothing at path is ever replaced: placement is a link or an O_EXCL create,
// and a path that is already taken — a file that appeared after the caller's
// own check, or one this filesystem considers the same path because of how it
// folds case (straße.png and strasse.png on APFS, A.png and a.png) — fails as
// ErrDestExists with path untouched. The caller's own pre-check is a different
// question asked earlier: it cannot see a file that is not there yet, and it
// asks the filesystem's view of two names under whatever folding that
// filesystem has.
func (b *Bucket) GetFileExactly(ctx context.Context, key, path, sum string, want int64, strict, replace bool) (size int64, sha string, err error) {
	return b.getFile(ctx, key, path, sum, want, want, strict, replace)
}

// getFile is GetFile and GetFileExactly: exact < 0 means the caller knows only
// an upper bound.
func (b *Bucket) getFile(ctx context.Context, key, path, sum string, limit, exact int64, strict, replace bool) (size int64, sha string, err error) {
	r, err := b.NewReader(ctx, key, nil)
	if gcerrors.Code(err) == gcerrors.NotFound && !(strict && isForbidden(err)) {
		return 0, "", ErrNotExist
	}
	if err != nil {
		return 0, "", strictReadErr(err, strict)
	}
	defer r.Close()
	if r.Size() > limit {
		return 0, "", fmt.Errorf("%s: %w (%d bytes, the cap is %d)", key, ErrTooLarge, r.Size(), limit)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".part-*")
	if err != nil {
		return 0, "", err
	}
	tmpName := tmp.Name()
	done := false
	defer func() {
		if !done {
			_ = os.Remove(tmpName)
		}
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, limit+1))
	if err != nil {
		_ = tmp.Close()
		return 0, "", fmt.Errorf("reading %s: %w", key, err)
	}
	if n > limit {
		_ = tmp.Close()
		return 0, "", fmt.Errorf("%s: %w (%d bytes, the cap is %d)", key, ErrTooLarge, n, limit)
	}
	// An object that ends early ends cleanly: the reader returns io.EOF, so
	// the copy above reports no error at all and what is hashed is a
	// prefix of the object — which would then be renamed into place, or,
	// with no expected hash, recorded as the artifact's. The announced size
	// is what tells the two apart, and it is the same guard PutFile makes
	// on its source, from the other side. -1 means the driver does not know
	// the size, and then there is nothing to compare against, exactly as
	// the pre-check above skips it.
	if r.Size() >= 0 && n != r.Size() {
		_ = tmp.Close()
		return 0, "", fmt.Errorf("%s: %w (read %d bytes, the object's size said %d)", key, ErrChanged, n, r.Size())
	}
	// The exact size, if the caller has one, is checked here — on the
	// temporary file, before the rename, beside the hash and for the same
	// reason: a mismatch must leave the destination as it was, not remove
	// what the rename put there.
	if exact >= 0 && n != exact {
		_ = tmp.Close()
		return 0, "", fmt.Errorf("%s: %w (read %d bytes, the record says %d)", key, ErrChanged, n, exact)
	}
	if err := tmp.Close(); err != nil {
		return 0, "", err
	}
	got := hex.EncodeToString(h.Sum(nil))
	want := strings.TrimSpace(sum)
	if want != "" && !strings.EqualFold(got, want) {
		return 0, "", fmt.Errorf("%s: %w (expected sha256 %s, read %s)", key, ErrMismatch, want, got)
	}
	if err := placeVerified(tmpName, path, replace); err != nil {
		return 0, "", err
	}
	done = true
	return n, got, nil
}

// linkFile is os.Link, and a test replaces it (see export_test.go) to reach
// the fallback below: a filesystem without hard links is the one thing this
// path cannot be given by a test on the machine it runs on, and dropping the
// O_EXCL from the fallback would then fail nothing.
var linkFile = os.Link

// placeVerified moves the verified temporary file to path, replacing what is
// at path only when replace says so.
//
// With replace it is os.Rename: the file that was there, if any, is gone, and
// that is what the caller asked for.
//
// Without it the file at path must not be replaced, and nothing here may
// assume path is free — the caller's earlier look cannot see a file that
// arrives in between, and it asks about the exact name it has while a
// filesystem may answer for more than one (a case-insensitive, normalizing
// one merges straße.png with strasse.png and A.png with a.png). os.Link makes
// the new name and fails with EEXIST rather than overwriting, and it is
// atomic: path is never visible half-written. The temporary is removed after
// the link, which leaves the bytes at path and nothing at the temporary's
// name. Where hard links are not supported (some network filesystems, and any
// driver without them) an O_EXCL create plus a copy does the same job with
// the same no-replace promise, minus the atomicity: the copy is of the
// temporary that was just verified, and a copy that fails leaves the file
// this function created removed again.
func placeVerified(tmp, path string, replace bool) error {
	if replace {
		return os.Rename(tmp, path)
	}
	switch err := linkFile(tmp, path); {
	case err == nil:
		_ = os.Remove(tmp)
		return nil
	case errors.Is(err, fs.ErrExist):
		return fmt.Errorf("%s: %w", path, ErrDestExists)
	}
	// Any other reason the link failed: a filesystem without hard links, a
	// link across filesystems, a refusal. The copy below makes the same
	// promise — nothing at path is replaced — so it is the right thing to
	// try rather than a fallback that changes what the caller asked for, and
	// a filesystem that cannot do that either reports its own error.
	return copyNoReplace(tmp, path)
}

// copyNoReplace writes tmp's bytes to a path that must not exist, creating it
// with O_EXCL so that a path taken in the meantime is refused rather than
// overwritten. The file is 0600, as the temporary is; a failure removes the
// file this function made, because it created it and nothing else knows about
// it.
func copyNoReplace(tmp, path string) error {
	src, err := os.Open(tmp)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%s: %w", path, ErrDestExists)
		}
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		_ = os.Remove(path)
		return err
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	_ = os.Remove(tmp)
	return nil
}

// Touch sets the object's GCS custom time, which the bucket's lifecycle
// rule reads as "last used" (design §3.3). No-op on other drivers.
func (b *Bucket) Touch(ctx context.Context, key string, t time.Time) error {
	c := b.client()
	if c == nil {
		return nil
	}
	_, err := c.Bucket(b.GCSName).Object(key).Update(ctx, storage.ObjectAttrsToUpdate{CustomTime: t.UTC()})
	if isForbidden(err) {
		return fmt.Errorf("%w: %w", ErrForbidden, err)
	}
	return err
}
