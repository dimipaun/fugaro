package cache

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"

	"cloud.google.com/go/storage"
	"gocloud.dev/blob"
	"gocloud.dev/gcerrors"

	"github.com/dimipaun/fugaro/internal/blobx"
)

// Store is one workflow's caches in the runs bucket.
type Store struct {
	Bucket         *blobx.Bucket
	Slug, Workflow string
	MaxBytes       int64 // zero means DefaultMaxBytes
	// Warn, when set, receives warnings that are not errors, such as the
	// number of symlinks Save left out of an archive. Its signature is
	// slog.Logger.Warn's. The arguments hold counts and keys, never paths
	// or content from the cached directories.
	Warn func(msg string, args ...any)
}

func (s *Store) max() int64 {
	if s.MaxBytes > 0 {
		return s.MaxBytes
	}
	return DefaultMaxBytes
}

// Restore extracts key's archive into roots. A missing archive is a miss
// (false, nil); any other failure is (false, err), which callers log and
// carry on from: caches only save time.
//
// After an error the roots may be partly filled, by design: they also
// hold the image's baked warm caches (design §7.2), so wiping them would
// turn one bad archive into a fully cold run and delete content the
// restore never wrote. Links the restore created that resolve outside
// their root are removed even then. An archive that failed as corrupt or
// refused (ErrBadArchive), not on an I/O error, is deleted, conditioned on
// the object read being the one still there, so it doesn't block its key
// until the lifecycle rule expires it; the next run's write-back replaces it.
// A root that is itself a symlink is refused (ErrLinkedRoot) before
// anything is written, and the archive is kept: the fault is local.
func (s *Store) Restore(ctx context.Context, key string, roots []string) (bool, error) {
	for _, r := range roots {
		if err := refuseLinkedRoot(r); err != nil {
			return false, err
		}
	}
	obj := ObjectKey(s.Slug, s.Workflow, key)
	r, err := s.Bucket.NewReader(ctx, obj, nil)
	if gcerrors.Code(err) == gcerrors.NotFound {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("opening %s: %w", obj, err)
	}
	defer r.Close()
	if err := extract(ctx, r, roots, s.max()); err != nil {
		err = fmt.Errorf("restoring %s: %w", obj, err)
		if errors.Is(err, ErrBadArchive) {
			if derr := s.discard(ctx, obj, r); derr != nil {
				err = fmt.Errorf("%w (and deleting it failed: %w)", err, derr)
			}
		}
		return false, err
	}
	_ = s.Bucket.Touch(ctx, obj, nowFunc()) // best effort: keeps a used cache past the lifecycle rule
	return true, nil
}

// discard deletes obj if it is still the object r read. On GCS that is a
// generation-matched blobx.DeleteIf. Other drivers (tests, local runs) have
// no generations, and DeleteIf's fallback compares the whole content,
// which a streamed archive of up to DefaultMaxBytes does not keep in
// memory; there the object's size and modification time stand in for the
// generation, which is not atomic but is enough off GCS.
func (s *Store) discard(ctx context.Context, obj string, r *blob.Reader) error {
	var sr *storage.Reader
	if r.As(&sr) {
		err := s.Bucket.DeleteIf(ctx, obj, sr.Attrs.Generation, nil)
		if errors.Is(err, blobx.ErrConflict) {
			return nil // replaced since: not ours to delete
		}
		return err
	}
	attrs, err := s.Bucket.Attributes(ctx, obj)
	if gcerrors.Code(err) == gcerrors.NotFound {
		return nil
	}
	if err != nil {
		return err
	}
	if attrs.Size != r.Size() || !attrs.ModTime.Equal(r.ModTime()) {
		return nil
	}
	err = s.Bucket.Delete(ctx, obj)
	if gcerrors.Code(err) == gcerrors.NotFound {
		return nil
	}
	return err
}

// Save archives roots as key unless that archive already exists. It
// returns saved=false, nil when it does. Symlinks that resolve outside
// their root are left out, and their count goes to Warn. A root that is
// itself a symlink is refused (ErrLinkedRoot) and nothing is written.
func (s *Store) Save(ctx context.Context, key string, roots []string) (bool, error) {
	for _, r := range roots {
		if err := refuseLinkedRoot(r); err != nil {
			return false, err
		}
	}
	obj := ObjectKey(s.Slug, s.Workflow, key)
	if ok, err := s.Bucket.Exists(ctx, obj); err != nil || ok {
		return false, err
	}
	if !hasFiles(roots) {
		return false, nil // an empty archive would block this key forever
	}
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	w, err := s.Bucket.NewWriter(wctx, obj, &blob.WriterOptions{ContentType: "application/zstd", IfNotExist: true})
	if err != nil {
		return false, err
	}
	st, err := writeArchive(w, roots, s.max())
	if err != nil {
		cancel() // abort: gocloud discards a write whose context is cancelled before Close
		_ = w.Close()
		return false, err
	}
	if err := w.Close(); err != nil {
		if gcerrors.Code(err) == gcerrors.FailedPrecondition {
			return false, nil // a parallel run wrote the same key first
		}
		return false, err
	}
	if st.skippedLinks > 0 && s.Warn != nil {
		s.Warn("cache written without symlinks that resolve outside their root", "key", key, "skipped_links", st.skippedLinks)
	}
	// The lifecycle rule deletes caches by custom time (not used in 30 days),
	// and an object without one never matches it, so set it at birth too.
	_ = s.Bucket.Touch(ctx, obj, nowFunc())
	return true, nil
}

// nowFunc is the clock Touch stamps with; tests may replace it.
var nowFunc = time.Now

// hasFiles reports whether any root holds at least one regular file. A
// missing root counts as empty.
func hasFiles(roots []string) bool {
	for _, r := range roots {
		dir, ok := walkRoot(r)
		if !ok {
			continue
		}
		found := false
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // unreadable parts can't be archived either
			}
			if p != dir && d.Type().IsRegular() {
				found = true
				return filepath.SkipAll
			}
			return nil
		})
		if found {
			return true
		}
	}
	return false
}
