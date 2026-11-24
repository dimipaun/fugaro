package versionstamp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/updatecheck"
)

// ReadTimeout bounds one read of the node, database round trip and ADC's token
// refresh included: a command's fence must not become a hang, and every caller
// falls back to the cache rule when it expires. D3271 keeps it at 2 seconds and
// leaves a longer door read to Check 900.
const ReadTimeout = 2 * time.Second

// CacheTTL is how long a cached stamp answers without a read (D3263): one RTDB
// GET per project per quarter hour. Past it the node is read again, which is
// what bounds a mistaken or unreachable value (D3268).
const CacheTTL = 15 * time.Minute

// maxCache bounds the cache file this package will read: the stamp is a few
// dozen bytes, and updatecheck.readBounded's rule treats anything larger as no
// cache rather than buffering it.
const maxCache = 64 << 10

// Reader is the narrow surface a Source reads the node through, which
// *rtdb.Client satisfies. out is what the node is decoded into; a null node
// reports found=false.
type Reader interface {
	Get(ctx context.Context, path string, out any) (found bool, err error)
}

// Query is one call's own facts: which database to ask, how to authenticate to
// it, whether the cache may answer, and what time it is.
type Query struct {
	// URL is the project's RTDB URL: the node is per installation, so this is
	// both where the read goes and what the cache file is keyed by (A4).
	URL string
	// NoAuth sends no credentials (the fakes only), exactly as the budget
	// client's own switch does.
	NoAuth bool
	// Fresh skips the cache for the read and writes it afterwards. `fugaro
	// update`'s door uses it (R8): a stamp raised a minute ago must stop an
	// older update at once, not at the end of the trust window.
	Fresh bool
	// Now is the clock the cache's age is measured against. The zero time is
	// time.Now(), which only a caller that has no clock to inject leaves.
	Now time.Time
}

// Origin is where an answer came from.
type Origin string

const (
	// OriginNone is an answer from nowhere: the node could not be read and no
	// cache could stand in for it. Reason says why.
	OriginNone Origin = "none"
	// OriginDatabase is a completed read of the node. Found says whether it
	// carried a stamp.
	OriginDatabase Origin = "database"
	// OriginCache is the cache file, used without a read (D3263) or after a
	// fresh read failed. It always carries a stamp: a cache holds a value, never
	// a verdict, and never an absence.
	OriginCache Origin = "cache"
)

// Result is one answer about the project's stamp. It carries the stamp and
// where the answer came from, never a verdict: whether the running binary is
// below the stamp is Below's, so a cache written by one release is still the
// right answer after `brew upgrade` moves the binary (spec, "Cache").
type Result struct {
	// Found is whether a stamp was found. It is false for a node that is not
	// there, which is a project with no fence, and for OriginNone.
	Found bool
	// Stamp is the value, when Found.
	Stamp Stamp
	// Origin is where the answer came from.
	Origin Origin
	// Reason says why the node could not be read, for the one stderr line a
	// fail-open caller prints (D3264). It is set exactly for OriginNone and
	// never carries a credential.
	Reason string
}

// Source answers what a project's required CLI is. Every reader in the CLI goes
// through it — the root fence, `update`'s door, doctor and the orchestrator
// daemon — so the cache rules, the timeout and the two fail-closed cases live
// once.
//
// Required returns an error only for a stamp that is present and malformed
// (*MalformedError); everything else — an unreachable database, a timeout,
// missing credentials, a denied read — is a Result with its reason and no
// error, because read failures fail open (D3264).
type Source interface {
	Required(ctx context.Context, q Query) (Result, error)
}

// Options are what a Source is built with.
type Options struct {
	// Open returns the reader of the database at url: the CLI passes a function
	// over rtdb.New (newBudgetClient's shape), and a test a fake. It is called
	// once per read, inside ReadTimeout, and a nil Open is a database that
	// cannot be reached.
	Open func(ctx context.Context, url string, noAuth bool) (Reader, error)
	// Getenv reads the environment the cache directory is placed from; nil is
	// os.Getenv.
	Getenv func(string) string
}

// New builds the reader behind this package's one seam in the CLI
// (internal/cli's newStampSource).
func New(o Options) Source {
	getenv := o.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	return &source{open: o.Open, getenv: getenv}
}

type source struct {
	open   func(ctx context.Context, url string, noAuth bool) (Reader, error)
	getenv func(string) string
}

// Required answers the project's stamp for one query.
//
// The cache rule is one rule for both paths (D3263, D3264, D3268): a cache
// entry dated between now and CacheTTL ago answers, and anything else — older,
// dated in the future, absent, corrupt, oversized — does not. An ordinary call
// returns a usable entry without reading at all; a Fresh call reads first and
// falls back to that same rule when the read fails, which is what makes a
// cached value above the binary still refuse an `update` whose fresh read could
// not be made (T4).
//
// A successful read always wins over the cache, whatever it says: the node is
// the answer, and a read that finds nothing removes the entry rather than
// leaving a value the next command would trust for another quarter hour.
func (s *source) Required(ctx context.Context, q Query) (Result, error) {
	now := q.Now
	if now.IsZero() {
		now = time.Now()
	}
	entry, cached := s.load(q.URL)
	usable := cached && fresh(entry.ReadAt, now)
	if usable && !q.Fresh {
		return Result{Found: true, Stamp: entry.stamp(), Origin: OriginCache}, nil
	}
	res, err := s.read(ctx, q)
	if err != nil {
		return Result{}, err
	}
	if res.Origin == OriginDatabase {
		s.save(q.URL, res, now)
		return res, nil
	}
	if usable {
		return Result{Found: true, Stamp: entry.stamp(), Origin: OriginCache}, nil
	}
	return res, nil
}

// stamp is the entry's value as a Stamp. load has already checked its members,
// so the conversion cannot fail here.
func (e cacheEntry) stamp() Stamp {
	return Stamp{Schema: e.Schema, Min: e.Min, SetAt: e.SetAt}
}

// fresh reports whether a cache entry written at readAt may answer at now. A
// timestamp in the future (a clock that moved back, a copied file) counts as
// stale, as updatecheck.Entry.Due does for its own cache: the age is what the
// rule is about, and a negative one says nothing about it.
func fresh(readAt, now time.Time) bool {
	age := now.Sub(readAt)
	return age >= 0 && age < CacheTTL
}

// read makes one bounded read of the node. A failure comes back as OriginNone
// with its reason and no error; a stamp that is present and malformed comes back
// as its own error, which no caller may fail open on.
func (s *source) read(ctx context.Context, q Query) (Result, error) {
	if s.open == nil {
		return Result{Origin: OriginNone, Reason: "no database client to read " + Path + " with"}, nil
	}
	rctx, cancel := context.WithTimeout(ctx, ReadTimeout)
	defer cancel()
	r, err := s.open(rctx, q.URL, q.NoAuth)
	if err != nil {
		return Result{Origin: OriginNone, Reason: err.Error()}, nil
	}
	var raw json.RawMessage
	found, err := r.Get(rctx, Path, &raw)
	if err != nil {
		return Result{Origin: OriginNone, Reason: err.Error()}, nil
	}
	if !found {
		return Result{Origin: OriginDatabase}, nil
	}
	stamp, present, err := Decode(raw)
	if err != nil {
		return Result{}, err
	}
	if !present {
		return Result{Origin: OriginDatabase}, nil
	}
	return Result{Found: true, Stamp: stamp, Origin: OriginDatabase}, nil
}

// cacheEntry is the cache file: the stamp's own members, so the file holds the
// value and nothing about what it means, plus when this machine read it, which
// is the only thing the TTL is measured from. It is not the node's bytes: a
// stamp read by an older release still ages, and a member of the node that a
// later release adds must not make today's cache unreadable.
type cacheEntry struct {
	stampWire
	ReadAt time.Time `json:"read_at"`
}

// cachePath is $XDG_CACHE_HOME/fugaro/required-cli/<hash>.json, else under the
// platform's own cache directory (localcfg.UserDir): one file per RTDB URL,
// because the stamp is per installation and a person may act on several (A4,
// "A monorepo, or two projects"). The URL is hashed rather than spelled, so a
// bug cannot build a file name out of one, and nothing about the project is
// readable from the directory.
func cachePath(getenv func(string) string, url string) (string, error) {
	dir, err := localcfg.UserDir(getenv, runtime.GOOS, localcfg.DirCache)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(url))
	return filepath.Join(dir, "fugaro", "required-cli", hex.EncodeToString(sum[:8])+".json"), nil
}

// load is the cache entry for url, ok false for anything this package will not
// trust: no file, a directory or a symlink, a file over maxCache, unreadable
// bytes, a missing or unknown member, or a stamp Decode refuses. A file is
// written by save below and by nothing else, so a file that is not what save
// would have written was tampered with, and the rule for it is updatecheck's
// own — a corrupt cache is no cache.
func (s *source) load(url string) (cacheEntry, bool) {
	path, err := cachePath(s.getenv, url)
	if err != nil {
		return cacheEntry{}, false
	}
	// Lstat, not Stat: a symlink (to /dev/zero, to a FIFO, to anything) must
	// never be followed, and save only ever creates a regular file.
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return cacheEntry{}, false
	}
	f, err := os.Open(path)
	if err != nil {
		return cacheEntry{}, false
	}
	defer f.Close()
	data, ok := readBounded(f)
	if !ok {
		return cacheEntry{}, false
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var e cacheEntry
	if err := dec.Decode(&e); err != nil {
		return cacheEntry{}, false
	}
	if e.Schema != SchemaVersion || !updatecheck.Valid(e.Min) || e.SetAt.IsZero() || e.ReadAt.IsZero() {
		return cacheEntry{}, false
	}
	return cacheEntry{stampWire: e.stampWire, ReadAt: e.ReadAt}, true
}

// save writes the cache for url: the stamp a successful read found, or nothing
// at all when it found none, because a node that is gone is a cache that must
// go with it — a deleted node (T7's hand edit) would otherwise keep refusing
// commands from a fresh entry for the rest of the window. Best effort: a cache
// that cannot be written costs a read, never an error.
func (s *source) save(url string, res Result, now time.Time) {
	path, err := cachePath(s.getenv, url)
	if err != nil {
		return
	}
	if !res.Found {
		_ = os.Remove(path)
		return
	}
	data, err := json.Marshal(cacheEntry{
		stampWire: stampWire{Schema: res.Stamp.Schema, Min: res.Stamp.Min, SetAt: res.Stamp.SetAt.UTC()},
		ReadAt:    now.UTC(),
	})
	if err != nil {
		return
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	_ = os.Rename(tmp.Name(), path)
}

// readBounded reads all of r up to 2*maxCache bytes; ok is false when that
// bound is hit or the read failed. It is updatecheck.readBounded's rule, which
// is unexported there: a cache file this machine's clock or another user
// controls must never be buffered in full before it is refused.
func readBounded(r io.Reader) (data []byte, ok bool) {
	data, err := io.ReadAll(io.LimitReader(r, 2*maxCache+1))
	if err != nil || len(data) > 2*maxCache {
		return nil, false
	}
	return data, true
}
