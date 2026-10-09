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
	"github.com/dimipaun/fugaro/internal/cache"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/lock"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// ErrDuplicateExecution means another execution already owns this run
// (design §4.7). The duplicate exits without writing anything.
var ErrDuplicateExecution = errors.New("another execution already owns this run")

// taskTimeoutSlack is what Terraform adds to timeouts.total for the Cloud
// Run task timeout (design §4.5), the same backend.TaskTimeoutSlack.
const taskTimeoutSlack = backend.TaskTimeoutSlack

// lockSlack is how far past the task timeout the branch lock (and the
// record's deadline) lasts, so a live run never loses its lock.
const lockSlack = time.Minute

// startupMargin is what writebackGrace leaves for the container's
// startup: StartedAt, which the grace is measured from, comes after Cloud
// Run starts the task timeout's clock.
const startupMargin = 15 * time.Second

// writebackGrace is how far past timeouts.total writeback's uploads may
// run: the task timeout's slack, less what follows them (the lock release
// and the final record, each on a bounded context of its own) and
// startupMargin, so the task timeout never kills the run before its lock
// is released and its final record is written.
const writebackGrace = taskTimeoutSlack - releaseDeferredTimeout - recordWriteTimeout - startupMargin

// writebackFloor is the least time writeback gets, however late it starts.
const writebackFloor = 20 * time.Second

// restoreBound is how long cache restore may take for a run whose
// timeouts.total is total; tests may replace it.
var restoreBound = func(total time.Duration) time.Duration { return min(10*time.Minute, total/6) }

// restoreCache restores one cache; tests may replace it.
var restoreCache = func(ctx context.Context, s *cache.Store, key string, roots []string) (bool, error) {
	return s.Restore(ctx, key, roots)
}

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

// staleHolder reports whether a live-looking branch lock's holder has
// provably ended, from the holder's own run record (lock.Stale): the only
// signal a lock takeover, which happens only here, ever uses. A record
// this run can't read, or one still "running" (the common case of a
// killed container that never finalized), leaves the lock live: it is
// taken over once it expires, as before.
func (r *run) staleHolder(ctx context.Context, h lock.Holder) bool {
	rec, err := runstore.Open(r.d.Bucket.Bucket, r.d.Store.Slug(), h.RunID).ReadRecord(ctx)
	if err != nil {
		return false
	}
	stale := lock.Stale(rec)
	if stale {
		r.d.Log.Info("branch lock taken over: holder's run has ended", "branch", r.rec.Branch, "holder_run_id", h.RunID, "holder_status", rec.Status)
	}
	return stale
}

func (r *run) acquireLock(ctx context.Context) error {
	if r.d.Bucket == nil {
		return nil
	}
	h := lock.Holder{RunID: r.spec.RunID, Execution: r.d.Execution, ExpiresAt: r.lockDeadline()}
	l, err := lock.Acquire(ctx, r.d.Bucket, lock.Key(r.d.Store.Slug(), r.rec.Branch), h, r.d.Now(), lock.WithStale(func(held lock.Holder) bool { return r.staleHolder(ctx, held) }))
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
	slug := r.d.Store.Slug()
	cur, v, err := runstore.ReadRecordVersion(ctx, r.d.Bucket, slug, r.spec.RunID)
	if err != nil {
		r.d.Log.Warn("reading the duplicate's own run record failed", "err", r.redact(err.Error()))
		return
	}
	if !backend.SameExecution(cur.Execution, r.d.Execution) {
		return // not the record this execution created
	}
	expires := owner.ExpiresAt
	cur.Execution, cur.Deadline = owner.Execution, &expires
	switch err := runstore.ReplaceRecordIf(ctx, r.d.Bucket, slug, r.spec.RunID, cur, v); {
	case errors.Is(err, runstore.ErrChanged):
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
	if err := releaseBranchLock(r.lock, ctx); err != nil {
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
	return &cache.Store{Bucket: r.d.Bucket, Slug: r.d.Store.Slug(), Workflow: r.rec.Workflow,
		MaxBytes: r.d.CacheMaxBytes, Warn: r.d.Log.Warn}
}

// cacheKey is s's key in the current checkout, read within ctx. A key file
// that isn't a regular file is left out, with a warning.
func (r *run) cacheKey(ctx context.Context, s cacheSlot) (string, bool, error) {
	return cache.KeyOf(ctx, r.d.WorkDir, s.entry, r.cacheBase, r.toolchain, r.d.Log.Warn)
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
		if err == nil {
			// A committed symlink at or above a root would take the
			// restore past Resolve's checks, into .git or elsewhere.
			err = cache.CheckLinks(e.Paths, r.d.WorkDir, home)
		}
		if err != nil {
			r.d.Log.Warn("cache skipped", "err", r.redact(err.Error()))
			continue
		}
		slot := cacheSlot{entry: e, roots: roots}
		r.caches = append(r.caches, slot)
		key, ok, err := r.cacheKey(ctx, slot)
		switch {
		case err != nil && ctx.Err() != nil:
			// The bound passed while the key was read: counted, like an
			// entry the bound stopped before it started.
			timedOut = true
			skipped++
			continue
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
		if ctx.Err() != nil {
			// The bound passed before this entry started (the previous one
			// finished just in time): it was never slow, so it is counted,
			// not named.
			timedOut = true
			skipped++
			continue
		}
		hit, err := restoreCache(ctx, store, key, roots)
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

// writeback is the last stage (design §4.1): the session, cache
// write-back, then the lock. A cancelled run saves its session (small, and
// what a follow-up resumes) but skips the caches, so a cancel never waits
// on a cache upload. Nothing here fails the run.
func (r *run) writeback(ctx context.Context) {
	r.rec.Stage = "writeback"
	r.save(ctx)
	deadline := r.rec.StartedAt.Add(r.wf.Timeouts.Total.Duration + writebackGrace)
	if floor := r.d.Now().Add(writebackFloor); deadline.Before(floor) {
		deadline = floor
	}
	wctx, cancel := context.WithDeadline(context.WithoutCancel(ctx), deadline)
	defer cancel()
	r.saveSession(wctx)
	if r.d.Bucket != nil && !r.isCancelled() {
		store, home := r.cacheStore(), envLookup(r.d.Env, "HOME")
		for _, s := range r.caches {
			// Checked again: the agent may have replaced a root, or a
			// directory above one, with a symlink to ~/.claude, the
			// runner's credentials or .git.
			if err := cache.CheckLinks(s.entry.Paths, r.d.WorkDir, home); err != nil {
				r.d.Log.Warn("cache not written", "err", r.redact(err.Error()))
				continue
			}
			// Recomputed from the final tree: when the agent changed a
			// lockfile, the new dependencies belong under the new key.
			key, ok, err := r.cacheKey(wctx, s)
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
	// On a context of its own: the uploads may have used up writeback's
	// deadline, and a release on it would fail at once and leave the lock
	// until it expires.
	rctx, cancelRelease := context.WithTimeout(context.WithoutCancel(ctx), releaseDeferredTimeout)
	defer cancelRelease()
	r.releaseLock(rctx)
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

// recordWriteTimeout bounds each result.json write, the final one
// included, so a stalled bucket cannot hold the runner until the task
// timeout (writebackGrace leaves room for it).
const recordWriteTimeout = 30 * time.Second

// Seams for the record writes and the lock release; tests may replace them.
var (
	createRecord      = (*runstore.Store).CreateRecord
	writeRecord       = (*runstore.Store).WriteRecord
	releaseBranchLock = (*lock.Lock).Release
)
