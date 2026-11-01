package runcache

import (
	"context"
	"errors"
	"io"
	"strings"

	"cloud.google.com/go/storage"
	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// cachedNames are the three objects of a run this cache holds. They are the
// three a run listing reads; nothing else under a run's prefix is
// cached -- transcripts and reports are large, and the cancel marker and
// the launch claim are read on paths that need the bucket as it is now.
var cachedNames = map[string]bool{"task.json": true, "launch.json": true, "result.json": true}

// List answers, in one listing, the generation of every cached object of
// slug's runs, keyed by object name.
//
// The listing starts at from, the oldest run ID the caller means to read
// (runstore.ListRunIDs's own offset rule: a run ID starts with its mint
// time, so IDs sort by time), and a run outside that window costs nothing
// to skip. Every object of the window is one page of the same listing, so a
// listing of a hundred runs is one call and not three hundred object reads.
//
// What a listing reports decides whether there is an index at all: a bucket
// that carries no generation per object -- file:// and mem:// do not, and
// only GCS does -- and a listing that failed or left a generation out both
// answer nil, "no index, read every object from the bucket". That is the
// cache being off, never a failure of the command that asked: the caller
// then reads the bucket exactly as it did before this package existed, and a
// real failure of the bucket is reported by the read that follows.
//
// The generation is taken from the object the listing answered with and not
// from the bucket's URL, so a bucket that reports generations is cached
// wherever it is opened from.
func List(ctx context.Context, b *blobx.Bucket, slug, from string) map[string]int64 {
	if b == nil || b.Bucket == nil {
		return nil
	}
	prefix := "runs/" + slug + "/"
	opts := &blob.ListOptions{Prefix: prefix}
	if from != "" {
		opts.BeforeList = func(as func(any) bool) error {
			var q *storage.Query
			if as(&q) {
				q.StartOffset = prefix + from
			}
			return nil
		}
	}
	index := map[string]int64{}
	it := b.List(opts)
	for {
		obj, err := it.Next(ctx)
		if errors.Is(err, io.EOF) {
			return index
		}
		if err != nil {
			return nil
		}
		if obj.IsDir || !isCachedObject(prefix, obj.Key) {
			continue
		}
		var attrs storage.ObjectAttrs
		if !obj.As(&attrs) || attrs.Generation == 0 {
			return nil // an object whose generation the listing did not carry: no index at all
		}
		index[obj.Key] = attrs.Generation
	}
}

// isCachedObject reports whether key is one of the three objects of a run
// of the listed slug. Anything else the runs bucket holds under the same
// prefix -- the launch claim, the cancel marker, a future object -- is left
// out of the index, and a caller that reads one reads the bucket: the index
// answers only for the names it was built from.
func isCachedObject(prefix, key string) bool {
	rest, ok := strings.CutPrefix(key, prefix)
	if !ok {
		return false
	}
	parts := strings.Split(rest, "/")
	return len(parts) == 2 && parts[0] != "" && cachedNames[parts[1]]
}

// Reader reads one run's objects, through the cache when it can and from the
// bucket when it cannot.
//
// The zero Reader is "no cache": every object is read from the bucket. That
// is what a caller that is not listing for display passes -- a launch, a
// gate, a budget read -- because a cached object is a snapshot of a listing
// that has already run, and those reads have to see the bucket as it is
// now.
type Reader struct {
	// Cache is where the objects are kept, nil when the CLI has no cache.
	Cache *Cache
	// Index is what a listing saw of this run's own slug: the generation of
	// every cached object at the moment the listing ran. Nil means no index,
	// and every object is read from the bucket.
	Index map[string]int64
}

// Read answers one of a run's objects: its bytes, a nil slice when the
// object is not there, or an error.
//
// With an index, the object's generation decides. An object the listing did
// not see is absent and costs no read -- an absent object is the common
// case for launch.json, and it is the one the bucket cannot answer cheaply.
// An object the listing saw at the generation the cache holds comes from
// the cache; any other generation -- a result.json the runner rewrote, a
// task.json a second writer replaced -- is read again, and the read is what
// the cache is brought to.
func (r Reader) Read(ctx context.Context, s *runstore.Store, name string) ([]byte, error) {
	if r.Index == nil {
		return readBucket(ctx, s, name)
	}
	key := s.Prefix() + name
	gen, listed := r.Index[key]
	if !listed {
		return nil, nil
	}
	if data, ok := r.Cache.Get(key, gen); ok {
		return data, nil
	}
	data, err := readBucket(ctx, s, name)
	if err != nil || data == nil {
		return data, err
	}
	r.Cache.Put(key, gen, data)
	return data, nil
}

// readBucket reads one of a run's objects, turning the store's "not found"
// into an absent object.
func readBucket(ctx context.Context, s *runstore.Store, name string) ([]byte, error) {
	data, err := s.ReadFile(ctx, name)
	if errors.Is(err, runstore.ErrNotFound) {
		return nil, nil
	}
	return data, err
}
