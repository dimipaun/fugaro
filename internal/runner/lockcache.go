package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/cache"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/lock"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// ErrDuplicateExecution means another execution already owns this run
// (design §4.7). The duplicate exits without writing anything.
var ErrDuplicateExecution = errors.New("another execution already owns this run")

// taskTimeoutSlack is what Terraform (and the M4 bootstrap) add to
// timeouts.total for the Cloud Run task timeout (design §4.5).
const taskTimeoutSlack = 2 * time.Minute

// lockSlack is how far past the task timeout the branch lock (and the
// record's deadline) lasts, so a live run never loses its lock.
const lockSlack = time.Minute

// writebackGrace is how far past timeouts.total writeback may run: the
// task timeout's slack minus 30s for the final record.
const writebackGrace = 90 * time.Second

// writebackFloor is the least time writeback gets, however late it starts.
const writebackFloor = 20 * time.Second

// restoreBound is how long cache restore may take for a run whose
// timeouts.total is total; tests may replace it.
var restoreBound = func(total time.Duration) time.Duration { return min(10*time.Minute, total/6) }

// unpinnedBase is the base-image part of a cache key when the image was
// not built FROM a digest.
const unpinnedBase = "unpinned"

type cacheSlot struct {
	entry config.CacheEntry
	roots []string
}

// lockDeadline is when the branch lock expires: the task timeout plus a
// minute. result.json records it as the run's deadline.
func (r *run) lockDeadline() time.Time {
	return r.rec.StartedAt.Add(r.wf.Timeouts.Total.Duration + taskTimeoutSlack + lockSlack)
}

func (r *run) acquireLock(ctx context.Context) error {
	if r.d.Bucket == nil {
		return nil
	}
	h := lock.Holder{RunID: r.spec.RunID, Execution: r.d.Execution, ExpiresAt: r.lockDeadline()}
	l, err := lock.Acquire(ctx, r.d.Bucket, lock.Key(task.Slug(r.spec.Repo), r.rec.Branch), h, r.d.Now())
	var busy *lock.BusyError
	if errors.As(err, &busy) && busy.Holder.RunID == r.spec.RunID && r.d.Execution != "" && !backend.SameExecution(busy.Holder.Execution, r.d.Execution) {
		r.disownRecord(ctx, busy.Holder)
		return ErrDuplicateExecution
	}
	if err != nil {
		return err
	}
	r.lock = l
	r.d.Log.Info("branch lock acquired", "branch", r.rec.Branch, "expires_at", h.ExpiresAt)
	return nil
}

// disownRecord is the defensive end of a duplicate caught at the lock:
// the owner's result.json had vanished, so this execution created its own.
// It rewrites that record, and only that one, to name the lock's holder
// and its expiry, so every view follows the owner. The replace is
// generation-matched against the record just read, so a record the owner
// wrote in the meantime is never overwritten; one that names any other
// execution is left alone.
func (r *run) disownRecord(ctx context.Context, owner lock.Holder) {
	key := r.d.Store.Prefix() + "result.json"
	data, gen, err := r.d.Bucket.Read(ctx, key)
	if err != nil {
		r.d.Log.Warn("reading the duplicate's own run record failed", "err", r.redact(err.Error()))
		return
	}
	var cur runstore.Record
	if json.Unmarshal(data, &cur) != nil || !backend.SameExecution(cur.Execution, r.d.Execution) {
		return // not the record this execution created
	}
	expires := owner.ExpiresAt
	cur.Execution, cur.Deadline = owner.Execution, &expires
	out, err := json.MarshalIndent(&cur, "", "  ")
	if err == nil {
		_, err = r.d.Bucket.ReplaceIf(ctx, key, out, gen, data)
	}
	switch {
	case errors.Is(err, blobx.ErrConflict):
		// Changed since it was read: the owner wrote it. Leave it.
	case err != nil:
		r.d.Log.Warn("handing the run record to its owner failed", "err", r.redact(err.Error()))
	}
}

// releaseDeferredTimeout bounds the lock release on Run's deferred path,
// so a hanging delete cannot eat the time left for the final record.
const releaseDeferredTimeout = 15 * time.Second

// releaseLock releases the branch lock, if held, within ctx: callers pass
// a context that is already detached from cancellation and bounded.
func (r *run) releaseLock(ctx context.Context) {
	if r.lock == nil {
		return
	}
	if err := r.lock.Release(ctx); err != nil {
		r.d.Log.Warn("releasing the branch lock failed; it expires on its own", "err", r.redact(err.Error()))
	} else {
		r.d.Log.Info("branch lock released", "branch", r.rec.Branch)
	}
	r.lock = nil
}

// toolchainHash is the toolchain part of a cache key: a stable hash of a
// workflow's image: settings (node, jdk, apt, setup, in that order), so a
// toolchain change such as a Node bump invalidates caches holding native
// binaries built for the old one. Apt packages are sorted (their order
// does not change what is installed); setup steps keep their order.
func toolchainHash(img config.Image) string {
	apt, setup := slices.Clone(img.Apt), img.Setup
	slices.Sort(apt)
	// An empty list and an absent one are the same toolchain.
	if len(apt) == 0 {
		apt = nil
	}
	if len(setup) == 0 {
		setup = nil
	}
	canon := struct {
		Node  string   `json:"node"`
		JDK   string   `json:"jdk"`
		Apt   []string `json:"apt"`
		Setup []string `json:"setup"`
	}{img.Node, img.JDK, apt, setup}
	data, _ := json.Marshal(canon) // strings and slices of them always marshal
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// cacheBase is the base-image part of every cache key: the image's base
// when it is a digest, otherwise the tag, or "unpinned" when there is none
// (a repository Dockerfile). pinned reports whether it is a digest.
func cacheBase(baseImage string) (base string, pinned bool) {
	switch {
	case strings.Contains(baseImage, "@sha256:"):
		return baseImage, true
	case baseImage == "":
		return unpinnedBase, false
	default:
		return baseImage, false
	}
}

func (r *run) cacheStore() *cache.Store {
	return &cache.Store{Bucket: r.d.Bucket, Slug: task.Slug(r.spec.Repo), Workflow: r.rec.Workflow,
		MaxBytes: r.d.CacheMaxBytes, Warn: r.d.Log.Warn}
}

// cacheKey is s's key in the current checkout.
func (r *run) cacheKey(s cacheSlot) (string, bool, error) {
	return cache.KeyOf(r.d.WorkDir, s.entry, r.cacheBase, r.toolchain)
}

// restoreCaches restores every cache entry it can; nothing here fails the
// run. The entries it resolves are kept for writeback.
func (r *run) restoreCaches(ctx context.Context) {
	if r.d.Bucket == nil {
		return
	}
	entries := r.wf.Cache
	if len(entries) == 0 {
		var err error
		if entries, err = config.DefaultCache(r.wf.Base, r.d.WorkDir); err != nil {
			r.d.Log.Warn("no default cache", "err", r.redact(err.Error()))
			return
		}
	}
	if len(entries) == 0 {
		return
	}
	var pinned bool
	r.cacheBase, pinned = cacheBase(r.d.BaseImage)
	r.toolchain = toolchainHash(r.wf.Image)
	if !pinned {
		r.d.Log.Warn("the base image is not pinned by digest; caches may be stale after a base change", "cache_base", r.cacheBase)
	}
	ctx, cancel := context.WithTimeout(ctx, restoreBound(r.wf.Timeouts.Total.Duration))
	defer cancel()
	store, home := r.cacheStore(), envLookup(r.d.Env, "HOME")
	var timedOut bool
	var skipped int // entries not restored once the bound was reached
	defer func() {
		if skipped > 0 {
			r.d.Log.Warn("cache restore skipped: the restore time bound was reached", "entries", skipped)
		}
	}()
	for _, e := range entries {
		for _, w := range cache.ResolveWarnings(e.Paths) {
			r.d.Log.Warn(w)
		}
		if home == "" && slices.ContainsFunc(e.Paths, func(p string) bool { return strings.HasPrefix(p, "~") }) {
			r.d.Log.Warn("cache skipped: HOME is not set", "paths", e.Paths)
			continue
		}
		roots, err := cache.Resolve(e.Paths, r.d.WorkDir, home)
		if err != nil {
			r.d.Log.Warn("cache skipped", "err", r.redact(err.Error()))
			continue
		}
		slot := cacheSlot{entry: e, roots: roots}
		r.caches = append(r.caches, slot)
		key, ok, err := r.cacheKey(slot)
		switch {
		case err != nil:
			r.d.Log.Warn("cache skipped", "err", r.redact(err.Error()))
			continue
		case !ok:
			r.d.Log.Info("cache skipped: no key file", "key_files", e.Key)
			continue
		case timedOut:
			skipped++ // still written back at the end of the run
			continue
		}
		err = ctx.Err() // the bound may already have passed
		var hit bool
		if err == nil {
			hit, err = store.Restore(ctx, key, roots)
		}
		switch {
		case err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded):
			// Name the object, so an operator can delete an archive that
			// is always too slow to restore. Later entries would all fail
			// at once and are only counted.
			timedOut = true
			r.d.Log.Warn("cache restore timed out", "key", key, "object", cache.ObjectKey(store.Slug, store.Workflow, key))
		case err != nil:
			r.d.Log.Warn("cache restore failed", "key", key, "err", r.redact(err.Error()))
		case hit:
			r.d.Log.Info("cache restored", "key", key, "paths", e.Paths)
		default:
			r.d.Log.Info("cache miss", "key", key, "paths", e.Paths)
		}
	}
}

// writeback is the last stage (design §4.1): cache write-back, then the
// lock. A cancelled run only releases its lock, so a cancel never waits on
// an upload. Nothing here fails the run.
func (r *run) writeback(ctx context.Context) {
	r.rec.Stage = "writeback"
	r.save(ctx)
	deadline := r.rec.StartedAt.Add(r.wf.Timeouts.Total.Duration + writebackGrace)
	if floor := r.d.Now().Add(writebackFloor); deadline.Before(floor) {
		deadline = floor
	}
	wctx, cancel := context.WithDeadline(context.WithoutCancel(ctx), deadline)
	defer cancel()
	if r.d.Bucket != nil && !r.cancelled {
		store := r.cacheStore()
		for _, s := range r.caches {
			// Recomputed from the final tree: when the agent changed a
			// lockfile, the new dependencies belong under the new key.
			key, ok, err := r.cacheKey(s)
			if err != nil {
				r.d.Log.Warn("cache not written", "err", r.redact(err.Error()))
				continue
			}
			if !ok {
				continue
			}
			saved, err := store.Save(wctx, key, s.roots)
			switch {
			case errors.Is(err, cache.ErrTooLarge):
				r.d.Log.Warn("cache not written: too large", "key", key)
			case err != nil:
				r.d.Log.Warn("cache write-back failed", "key", key, "err", r.redact(err.Error()))
			case saved:
				r.d.Log.Info("cache written", "key", key)
			}
		}
	}
	r.releaseLock(wctx)
}

// envLookup is key's value in env, the last one winning as in exec.
func envLookup(env []string, key string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(env[i], "="); ok && k == key {
			return v
		}
	}
	return ""
}
