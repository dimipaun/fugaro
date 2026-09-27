package cache

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"

	"gocloud.dev/blob"
	"gocloud.dev/gcerrors"

	"github.com/dimipaun/fugaro/internal/blobx"
)

// Store is one workflow's caches in the runs bucket.
type Store struct {
	Bucket         *blobx.Bucket
	Slug, Workflow string
	MaxBytes       int64 // zero means DefaultMaxBytes
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
func (s *Store) Restore(ctx context.Context, key string, roots []string) (bool, error) {
	obj := ObjectKey(s.Slug, s.Workflow, key)
	r, err := s.Bucket.NewReader(ctx, obj, nil)
	if gcerrors.Code(err) == gcerrors.NotFound {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("opening %s: %w", obj, err)
	}
	defer r.Close()
	if err := Extract(r, roots, s.max()); err != nil {
		return false, fmt.Errorf("restoring %s: %w", obj, err)
	}
	_ = s.Bucket.Touch(ctx, obj, nowFunc()) // best effort: keeps a used cache past the lifecycle rule
	return true, nil
}

// Save archives roots as key unless that archive already exists. It
// returns saved=false, nil when it does.
func (s *Store) Save(ctx context.Context, key string, roots []string) (bool, error) {
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
	if _, err := Write(w, roots, s.max()); err != nil {
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
