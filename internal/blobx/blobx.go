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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	// ErrNotExist means Read found no object.
	ErrNotExist = errors.New("object does not exist")
	// ErrTooLarge means Read found an object larger than MaxReadBytes.
	ErrTooLarge = errors.New("object is too large")
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
		if gcerrors.Code(err) == gcerrors.FailedPrecondition {
			if ifNotExist {
				return 0, ErrExists
			}
			return 0, ErrConflict
		}
		return 0, err
	}
	if sw != nil && sw.Attrs() != nil {
		return sw.Attrs().Generation, nil
	}
	return 0, nil
}

// Create writes key only if it does not exist (ErrExists otherwise).
func (b *Bucket) Create(ctx context.Context, key string, data []byte, contentType string) (int64, error) {
	return b.write(ctx, key, data, contentType, true, nil)
}

// Read returns key's content and generation (0 off GCS) from one reader.
// An object larger than MaxReadBytes is ErrTooLarge.
func (b *Bucket) Read(ctx context.Context, key string) ([]byte, int64, error) {
	return b.ReadMax(ctx, key, MaxReadBytes)
}

// ReadMax is Read with a cap of limit bytes instead of MaxReadBytes: an
// object larger is ErrTooLarge, and no more than limit+1 bytes are read.
// A refusal (HTTP 403) is returned as itself, never as ErrNotExist,
// although gocloud's GCS driver reports it as not found.
func (b *Bucket) ReadMax(ctx context.Context, key string, limit int) ([]byte, int64, error) {
	r, err := b.NewReader(ctx, key, nil)
	if gcerrors.Code(err) == gcerrors.NotFound && !isForbidden(err) {
		return nil, 0, ErrNotExist
	}
	if err != nil {
		return nil, 0, err
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

// Touch sets the object's GCS custom time, which the bucket's lifecycle
// rule reads as "last used" (design §3.3). No-op on other drivers.
func (b *Bucket) Touch(ctx context.Context, key string, t time.Time) error {
	c := b.client()
	if c == nil {
		return nil
	}
	_, err := c.Bucket(b.GCSName).Object(key).Update(ctx, storage.ObjectAttrsToUpdate{CustomTime: t.UTC()})
	return err
}
