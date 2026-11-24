package versionstamp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/rtdb"
)

// The database the CLI reads this node through is *rtdb.Client (stampOpen in
// internal/cli), so the narrow surface above has to stay one it satisfies. The
// CLI's own use is the compile-time pin; this says which type that is.
var _ Reader = (*rtdb.Client)(nil)

// The clock every test in this file works against, and the two databases they
// act on: the stamp is per installation, so a persona with two projects has two
// nodes and two cache files (A4).
var (
	testNow = time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	urlA    = "https://one-default-rtdb.firebaseio.com"
	urlB    = "https://two-default-rtdb.firebaseio.com"
)

// fakeReader is the database a test injects: it counts the reads a Source makes
// (so "without a read" is asserted and not assumed), records the deadline the
// read was given, and answers with one scripted node or one scripted failure.
type fakeReader struct {
	calls    int
	deadline time.Duration // time left on ctx when Get was called, 0 if none
	raw      string        // the node's bytes; "" and "null" are an absent node
	err      error         // a read failure, which takes precedence over raw
}

func (f *fakeReader) Get(ctx context.Context, _ string, out any) (bool, error) {
	f.calls++
	if dl, ok := ctx.Deadline(); ok {
		f.deadline = time.Until(dl)
	}
	if f.err != nil {
		return false, f.err
	}
	if f.raw == "" || f.raw == "null" {
		return false, nil
	}
	if err := json.Unmarshal([]byte(f.raw), out); err != nil {
		return false, err
	}
	return true, nil
}

// node is the bytes a well-formed node carries for one release.
func node(min string) string {
	return `{"schema":1,"min":"` + min + `","set_at":"2026-11-23T10:00:00Z"}`
}

// testSource is a Source over a temp cache directory (the plan's hermetic rule:
// no home directory, XDG_CACHE_HOME pointed into t.TempDir()) and one fake
// database.
func testSource(t *testing.T, r *fakeReader, openErr error) (Source, func(string) string) {
	t.Helper()
	return testSourceIn(t, t.TempDir(), r, openErr)
}

// testSourceIn is testSource over a cache directory the caller owns, so a test
// that needs two Sources to share one cache directory can say so.
func testSourceIn(t *testing.T, dir string, r *fakeReader, openErr error) (Source, func(string) string) {
	t.Helper()
	getenv := func(key string) string {
		if key == "XDG_CACHE_HOME" {
			return dir
		}
		return ""
	}
	src := New(Options{
		Open: func(context.Context, string, bool) (Reader, error) {
			if openErr != nil {
				return nil, openErr
			}
			return r, nil
		},
		Getenv: getenv,
	})
	return src, getenv
}

// seedCache writes one cache file the way a completed read would have, so a test
// can start from a cache of a chosen age without running a read first.
func seedCache(t *testing.T, getenv func(string) string, url, min string, readAt time.Time) {
	t.Helper()
	path, err := cachePath(getenv, url)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(cacheEntry{
		stampWire: stampWire{Schema: SchemaVersion, Min: min, SetAt: testNow.Add(-time.Hour)},
		ReadAt:    readAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// cachedStamp is what the cache holds: the stamp, and when this machine read it.
func cachedStamp(t *testing.T, getenv func(string) string, url string) (Stamp, time.Time, bool) {
	t.Helper()
	path, err := cachePath(getenv, url)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Stamp{}, time.Time{}, false
	}
	var e cacheEntry
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatalf("the cache this package wrote does not decode: %v", err)
	}
	return e.stamp(), e.ReadAt, true
}

// TestUnreachableDatabasePasses: a database that cannot be reached, and one this
// machine has no credentials for, are the same answer — no stamp, the reason,
// and no error. Failing closed here would turn every RTDB blip into a stop of
// all work, for a fence that is not a security boundary (D3264).
func TestUnreachableDatabasePasses(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"the database is unreachable", errors.New("rtdb: GET /" + Path + ": dial tcp: connection refused")},
		{"this machine has no credentials", errors.New("no Google credentials to reach the budget database: run gcloud auth application-default login")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src, _ := testSource(t, &fakeReader{err: tc.err}, nil)
			res, err := src.Required(context.Background(), Query{URL: urlA, Now: testNow})
			if err != nil {
				t.Fatalf("Required = %v, want no error: a read failure fails open", err)
			}
			if res.Found || res.Origin != OriginNone {
				t.Errorf("Required = %+v, want no stamp from no origin", res)
			}
			if !strings.Contains(res.Reason, tc.err.Error()) {
				t.Errorf("Result.Reason = %q, want it to say why (%q)", res.Reason, tc.err)
			}
		})
	}
}

// TestDeniedReadPassesWithItsReason: a rule denying the read (401/403) is a
// failure like any other. It is the one a member with no roles would hit, and it
// has to leave the command working rather than refusing everything.
func TestDeniedReadPassesWithItsReason(t *testing.T) {
	denied := errors.New("rtdb: GET /" + Path + ": 403: Permission denied")
	src, _ := testSource(t, &fakeReader{err: denied}, nil)
	res, err := src.Required(context.Background(), Query{URL: urlA, Now: testNow})
	if err != nil {
		t.Fatalf("Required = %v, want no error", err)
	}
	if res.Found || res.Origin != OriginNone || !strings.Contains(res.Reason, "403") {
		t.Errorf("Required = %+v, want a denial carried as the reason", res)
	}
}

// TestTimeoutIsTwoSeconds: one read is bounded by ReadTimeout, and the bound is
// 2 seconds (D3271). The check reads the deadline off the context the reader is
// given, which is the same bound ADC's token refresh runs under, and then it
// proves a read that ends on its context fails open rather than erroring.
func TestTimeoutIsTwoSeconds(t *testing.T) {
	if ReadTimeout != 2*time.Second {
		t.Errorf("ReadTimeout = %s, want 2s (D3271)", ReadTimeout)
	}
	r := &fakeReader{raw: node("0.29.0")}
	src, _ := testSource(t, r, nil)
	if _, err := src.Required(context.Background(), Query{URL: urlA, Now: testNow}); err != nil {
		t.Fatal(err)
	}
	if r.deadline <= 0 || r.deadline > ReadTimeout {
		t.Errorf("the read was given %s, want no more than %s", r.deadline, ReadTimeout)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	r = &fakeReader{err: context.Canceled}
	src, _ = testSource(t, r, nil)
	res, err := src.Required(cancelled, Query{URL: urlA, Now: testNow})
	if err != nil {
		t.Fatalf("Required on a cancelled context = %v, want no error", err)
	}
	if res.Found || res.Origin != OriginNone {
		t.Errorf("Required on a cancelled context = %+v, want no stamp from no origin", res)
	}
}

// TestFreshCacheSkipsTheRead: a cache entry younger than CacheTTL answers
// without touching the database (D3263), which is the whole reason the cache
// exists: one read per project per quarter hour.
func TestFreshCacheSkipsTheRead(t *testing.T) {
	r := &fakeReader{raw: node("0.30.0")}
	src, getenv := testSource(t, r, nil)
	seedCache(t, getenv, urlA, "0.29.0", testNow.Add(-time.Minute))
	res, err := src.Required(context.Background(), Query{URL: urlA, Now: testNow})
	if err != nil {
		t.Fatal(err)
	}
	if r.calls != 0 {
		t.Errorf("Required read the database %d time(s), want none: a fresh cache answers", r.calls)
	}
	if !res.Found || res.Origin != OriginCache || res.Stamp.Min != "0.29.0" {
		t.Errorf("Required = %+v, want the cached 0.29.0", res)
	}
	// The entry is not rewritten by a call that read nothing.
	if _, readAt, ok := cachedStamp(t, getenv, urlA); !ok || !readAt.Equal(testNow.Add(-time.Minute)) {
		t.Errorf("a call answered from the cache rewrote it: readAt = %s, %v", readAt, ok)
	}
}

// TestCachedStampAboveBinaryRefusesWithoutARead: a cached value above the binary
// refuses with no read (D3264), which is what makes the fence apply to a command
// that cannot reach the database at all. The refusal is the caller's — Below
// over the cached stamp — and this test asserts both halves: the answer came
// from the cache with no read, and it is below the running binary.
func TestCachedStampAboveBinaryRefusesWithoutARead(t *testing.T) {
	r := &fakeReader{raw: node("0.29.0")}
	src, getenv := testSource(t, r, nil)
	seedCache(t, getenv, urlA, "0.29.0", testNow.Add(-time.Minute))
	res, err := src.Required(context.Background(), Query{URL: urlA, Now: testNow})
	if err != nil {
		t.Fatal(err)
	}
	if r.calls != 0 {
		t.Errorf("Required read the database %d time(s), want none", r.calls)
	}
	if res.Origin != OriginCache || !res.Found {
		t.Fatalf("Required = %+v, want the cache to answer", res)
	}
	if !Below(res.Stamp, "0.28.0") {
		t.Errorf("Below(%+v, 0.28.0) = false: a cached stamp above the binary must refuse", res.Stamp)
	}
	if got := (&BelowError{Stamp: res.Stamp, Version: "0.28.0"}).Error(); !strings.Contains(got, "0.29.0") {
		t.Errorf("the refusal = %q, want it to name the cached release", got)
	}
}

// TestStaleCachedStampAboveBinaryNeedsAReRead: past the window, a cached value
// above the binary refuses only if a read confirms it (D3268). The cached
// value alone is not enough any more — that is the bound that keeps one bad
// entry from stopping a project forever.
func TestStaleCachedStampAboveBinaryNeedsAReRead(t *testing.T) {
	r := &fakeReader{raw: node("0.29.0")}
	src, getenv := testSource(t, r, nil)
	seedCache(t, getenv, urlA, "0.29.0", testNow.Add(-16*time.Minute))
	res, err := src.Required(context.Background(), Query{URL: urlA, Now: testNow})
	if err != nil {
		t.Fatal(err)
	}
	if r.calls != 1 {
		t.Errorf("Required read the database %d time(s), want exactly one: a stale cache is re-read", r.calls)
	}
	if !res.Found || res.Origin != OriginDatabase || res.Stamp.Min != "0.29.0" {
		t.Fatalf("Required = %+v, want the re-read value", res)
	}
	if !Below(res.Stamp, "0.28.0") {
		t.Error("the re-read confirmed 0.29.0 and it is above 0.28.0, so the command must refuse")
	}
}

// TestStaleCachedStampPassesWhenTheReReadFails: the same stale entry, with the
// read failing, is no fence at all — the command goes on with the reason. This
// is the half of D3268 that makes the bound an availability rule: an
// unreachable database cannot keep refusing an old release forever.
func TestStaleCachedStampPassesWhenTheReReadFails(t *testing.T) {
	r := &fakeReader{err: errors.New("rtdb: GET /" + Path + ": 503")}
	src, getenv := testSource(t, r, nil)
	seedCache(t, getenv, urlA, "0.29.0", testNow.Add(-16*time.Minute))
	res, err := src.Required(context.Background(), Query{URL: urlA, Now: testNow})
	if err != nil {
		t.Fatalf("Required = %v, want no error", err)
	}
	if r.calls != 1 {
		t.Errorf("Required read the database %d time(s), want exactly one", r.calls)
	}
	if res.Found || res.Origin != OriginNone || res.Reason == "" {
		t.Errorf("Required = %+v, want no stamp, no origin and the reason", res)
	}
}

// TestFutureCacheTimestampIsStale: an entry dated in the future says nothing
// about its age (a clock that moved back, a copied file), so it is treated as
// stale and re-read — never as the freshest possible cache, which is what a
// negative age read as "younger than 15 minutes" would give, and never as a
// value that answers with no read at all.
func TestFutureCacheTimestampIsStale(t *testing.T) {
	r := &fakeReader{raw: node("0.30.0")}
	src, getenv := testSource(t, r, nil)
	seedCache(t, getenv, urlA, "0.29.0", testNow.Add(time.Minute))
	res, err := src.Required(context.Background(), Query{URL: urlA, Now: testNow})
	if err != nil {
		t.Fatal(err)
	}
	if r.calls != 1 || res.Origin != OriginDatabase || res.Stamp.Min != "0.30.0" {
		t.Errorf("Required = %+v after %d read(s), want the database's value", res, r.calls)
	}

	// And with the read failing it does not answer either.
	r = &fakeReader{err: errors.New("rtdb: GET /" + Path + ": 503")}
	src, getenv = testSource(t, r, nil)
	seedCache(t, getenv, urlA, "0.29.0", testNow.Add(time.Minute))
	res, err = src.Required(context.Background(), Query{URL: urlA, Now: testNow})
	if err != nil {
		t.Fatal(err)
	}
	if res.Found || res.Origin != OriginNone {
		t.Errorf("Required = %+v: a future-dated entry must not answer a failed read", res)
	}
}

// TestStaleCacheReads: the ordinary path past the window — the database is read
// once, its value wins, and the cache is rewritten with it, so the next quarter
// hour is answered without a read.
func TestStaleCacheReads(t *testing.T) {
	r := &fakeReader{raw: node("0.31.0")}
	src, getenv := testSource(t, r, nil)
	seedCache(t, getenv, urlA, "0.29.0", testNow.Add(-16*time.Minute))
	res, err := src.Required(context.Background(), Query{URL: urlA, Now: testNow})
	if err != nil {
		t.Fatal(err)
	}
	if r.calls != 1 || res.Origin != OriginDatabase || res.Stamp.Min != "0.31.0" {
		t.Fatalf("Required = %+v after %d read(s), want the database's value", res, r.calls)
	}
	stamp, readAt, ok := cachedStamp(t, getenv, urlA)
	if !ok || stamp.Min != "0.31.0" || !readAt.Equal(testNow) {
		t.Errorf("the cache after a read = %+v read at %s (%v), want 0.31.0 read at %s", stamp, readAt, ok, testNow)
	}
	path, err := cachePath(getenv, urlA)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("the cache file's mode = %v, want 0600", fi.Mode().Perm())
	}
}

// TestFreshQueryIgnoresTheCache: `update`'s door reads fresh (R8), because a
// stamp raised a minute ago must stop an older update now and not at the end of
// the trust window. Fresh skips the cache for the read and writes it afterwards.
func TestFreshQueryIgnoresTheCache(t *testing.T) {
	r := &fakeReader{raw: node("0.32.0")}
	src, getenv := testSource(t, r, nil)
	seedCache(t, getenv, urlA, "0.31.0", testNow.Add(-time.Minute))
	res, err := src.Required(context.Background(), Query{URL: urlA, Now: testNow, Fresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if r.calls != 1 {
		t.Errorf("a Fresh query made %d read(s), want one even though the cache was fresh", r.calls)
	}
	if res.Origin != OriginDatabase || res.Stamp.Min != "0.32.0" {
		t.Fatalf("Required = %+v, want the database's value", res)
	}
	if stamp, readAt, ok := cachedStamp(t, getenv, urlA); !ok || stamp.Min != "0.32.0" || !readAt.Equal(testNow) {
		t.Errorf("the cache after a fresh read = %+v read at %s (%v), want 0.32.0 read at %s", stamp, readAt, ok, testNow)
	}
}

// TestFreshReadFailureStillRefusesOnAFreshCacheAbove: a fresh read that fails
// falls back to the cache rule, so a cached value above the binary that is
// younger than the window still refuses (D3264, D3268). T4 leans on this: an
// `update` whose read times out must not run below the stamp.
func TestFreshReadFailureStillRefusesOnAFreshCacheAbove(t *testing.T) {
	r := &fakeReader{err: errors.New("rtdb: GET /" + Path + ": dial tcp: connection refused")}
	src, getenv := testSource(t, r, nil)
	seedCache(t, getenv, urlA, "0.29.0", testNow.Add(-time.Minute))
	res, err := src.Required(context.Background(), Query{URL: urlA, Now: testNow, Fresh: true})
	if err != nil {
		t.Fatalf("Required = %v, want no error", err)
	}
	if r.calls != 1 {
		t.Errorf("a Fresh query made %d read(s), want one", r.calls)
	}
	if res.Origin != OriginCache || !res.Found || !Below(res.Stamp, "0.28.0") {
		t.Fatalf("Required = %+v, want the fresh cached 0.29.0 to refuse", res)
	}

	// Past the window the same fallback answers nothing: the cache rule is the
	// same rule on both paths.
	r = &fakeReader{err: errors.New("rtdb: GET /" + Path + ": dial tcp: connection refused")}
	src, getenv = testSource(t, r, nil)
	seedCache(t, getenv, urlA, "0.29.0", testNow.Add(-16*time.Minute))
	res, err = src.Required(context.Background(), Query{URL: urlA, Now: testNow, Fresh: true})
	if err != nil {
		t.Fatalf("Required = %v, want no error", err)
	}
	if res.Found || res.Origin != OriginNone || res.Reason == "" {
		t.Errorf("Required = %+v, want a stale cache to stand in for nothing", res)
	}
}

// TestCacheIsPerDatabase: the stamp is per installation (A4), so two projects
// have two cache files and one project's value never answers for another.
func TestCacheIsPerDatabase(t *testing.T) {
	// Both Sources share one cache directory: with one directory each, the
	// second database would be read from an empty directory whatever the cache
	// file is named, and the test could not fail.
	dir := t.TempDir()
	src, getenv := testSourceIn(t, dir, &fakeReader{raw: node("0.29.0")}, nil)
	if _, err := src.Required(context.Background(), Query{URL: urlA, Now: testNow}); err != nil {
		t.Fatal(err)
	}
	seedCache(t, getenv, urlA, "0.29.0", testNow)

	r := &fakeReader{err: errors.New("rtdb: GET /" + Path + ": 503")}
	srcB, _ := testSourceIn(t, dir, r, nil)
	res, err := srcB.Required(context.Background(), Query{URL: urlB, Now: testNow})
	if err != nil {
		t.Fatal(err)
	}
	if r.calls != 1 || res.Found {
		t.Errorf("Required on the other database = %+v after %d read(s): one project's cache must not answer for another", res, r.calls)
	}
}

// TestCorruptCacheIsNoCache: the cache file is written by this package and by
// nothing else, so a file that is not what it would have written was tampered
// with or truncated, and the rule is updatecheck.readBounded's — it counts as no
// cache rather than as an answer. A file that is never fully buffered is part of
// that rule: an oversized file is refused unread.
func TestCorruptCacheIsNoCache(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"not JSON", []byte("this is not a cache")},
		{"truncated JSON", []byte(`{"schema":1,"min":"0.29.0"`)},
		{"an unknown member", []byte(`{"schema":1,"min":"0.29.0","set_at":"2026-11-23T10:00:00Z","read_at":"2026-11-23T09:59:00Z","verdict":"refuse"}`)},
		{"a min that is not a release", []byte(`{"schema":1,"min":"next","set_at":"2026-11-23T10:00:00Z","read_at":"2026-11-23T09:59:00Z"}`)},
		{"no read_at", []byte(`{"schema":1,"min":"0.29.0","set_at":"2026-11-23T10:00:00Z"}`)},
		{"oversized", bytes.Repeat([]byte("x"), 2*maxCache+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &fakeReader{raw: node("0.30.0")}
			src, getenv := testSource(t, r, nil)
			path, err := cachePath(getenv, urlA)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, tc.data, 0o600); err != nil {
				t.Fatal(err)
			}
			res, err := src.Required(context.Background(), Query{URL: urlA, Now: testNow})
			if err != nil {
				t.Fatalf("Required = %v, want no error", err)
			}
			if r.calls != 1 || res.Origin != OriginDatabase || res.Stamp.Min != "0.30.0" {
				t.Errorf("Required = %+v after %d read(s), want the database read as if no cache existed", res, r.calls)
			}
		})
	}
}

// TestCacheSymlinkIsNoCache: save only ever creates a regular file (a temp file
// renamed over the path), so a symlink at the cache path is somebody else's and
// is never followed — a link to /dev/zero would read without limit if the target
// were opened. It counts as no cache: the database is read and answers.
//
// Mutation: os.Stat in place of os.Lstat follows the link and reads the valid
// entry behind it, which would answer without a read — the read count and the
// database's own value both catch that.
func TestCacheSymlinkIsNoCache(t *testing.T) {
	r := &fakeReader{raw: node("0.29.0")}
	src, getenv := testSource(t, r, nil)
	path, err := cachePath(getenv, urlA)
	if err != nil {
		t.Fatal(err)
	}
	seedCache(t, getenv, urlA, "9.9.9", testNow)
	target := filepath.Join(filepath.Dir(path), "target.json")
	if err := os.Rename(path, target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unsupported here: %v", err)
	}
	res, err := src.Required(context.Background(), Query{URL: urlA, Now: testNow})
	if err != nil {
		t.Fatalf("Required = %v, want no error", err)
	}
	if r.calls != 1 || res.Origin != OriginDatabase || res.Stamp.Min != "0.29.0" {
		t.Errorf("Required with a symlinked cache = %+v after %d read(s), want the symlink treated as no cache", res, r.calls)
	}
}

// TestCacheFIFOIsNoCache pins the same refusal for a named pipe: opening one for
// reading blocks until a writer connects, and Lstat cannot tell it from a
// regular file by name alone, so the regular-file check is what refuses it
// before any open.
//
// Mutation: dropping the IsRegular check makes this test hang on the open rather
// than answer, which the timeout below catches.
func TestCacheFIFOIsNoCache(t *testing.T) {
	r := &fakeReader{raw: node("0.29.0")}
	src, getenv := testSource(t, r, nil)
	path, err := cachePath(getenv, urlA)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("FIFOs unsupported here: %v", err)
	}
	done := make(chan Result, 1)
	go func() {
		res, _ := src.Required(context.Background(), Query{URL: urlA, Now: testNow})
		done <- res
	}()
	select {
	case res := <-done:
		if r.calls != 1 || res.Origin != OriginDatabase || res.Stamp.Min != "0.29.0" {
			t.Errorf("Required with a FIFO cache = %+v after %d read(s), want the FIFO treated as no cache", res, r.calls)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Required blocked on a FIFO cache file: a pipe is opened by nothing in this package")
	}
}

// TestMalformedStampRefuses: a node that is present and is not this shape is the
// one read failure that fails closed. It refuses with *MalformedError wherever
// it is read — including when a good cache is sitting right there, because the
// node is what somebody meant and a cache is a value this binary once saw.
func TestMalformedStampRefuses(t *testing.T) {
	const bad = `{"schema":1,"min":"0.29.0","set_at":"2026-11-23T10:00:00Z","verdict":"refuse"}`
	for _, fresh := range []bool{false, true} {
		r := &fakeReader{raw: bad}
		src, _ := testSource(t, r, nil)
		res, err := src.Required(context.Background(), Query{URL: urlA, Now: testNow, Fresh: fresh})
		if !errors.As(err, new(*MalformedError)) {
			t.Fatalf("Required (Fresh=%v, no cache) = %+v, %v, want a *MalformedError", fresh, res, err)
		}
		if r.calls != 1 {
			t.Errorf("Required (Fresh=%v) made %d read(s), want one", fresh, r.calls)
		}
	}

	// A good cache does not soften it: a fresh read that meets a malformed node
	// refuses, because the node is what somebody meant and the cache is only a
	// value this binary once saw.
	r := &fakeReader{raw: bad}
	src, getenv := testSource(t, r, nil)
	seedCache(t, getenv, urlA, "0.28.0", testNow.Add(-time.Minute))
	if _, err := src.Required(context.Background(), Query{URL: urlA, Now: testNow, Fresh: true}); !errors.As(err, new(*MalformedError)) {
		t.Errorf("Required (Fresh) beside a good cache = %v, want a *MalformedError", err)
	}

	// An ordinary query inside the window never reads, so it answers from the
	// cache: that is the 15 minutes D3263 trusts, and this is where it shows.
	r = &fakeReader{raw: bad}
	src, getenv = testSource(t, r, nil)
	seedCache(t, getenv, urlA, "0.28.0", testNow.Add(-time.Minute))
	res, err := src.Required(context.Background(), Query{URL: urlA, Now: testNow})
	if err != nil || r.calls != 0 || res.Stamp.Min != "0.28.0" {
		t.Errorf("Required from a fresh cache = %+v, %v after %d read(s), want the cached value and no read", res, err, r.calls)
	}
}

// TestNoClientIsNoStamp: a Source with no database to open — doctor answering
// offline, a caller that has not resolved a project's URL — is a read that could
// not be made, not a panic and not an error.
func TestNoClientIsNoStamp(t *testing.T) {
	src := New(Options{Getenv: func(string) string { return t.TempDir() }})
	res, err := src.Required(context.Background(), Query{URL: urlA, Now: testNow})
	if err != nil {
		t.Fatalf("Required = %v, want no error", err)
	}
	if res.Found || res.Origin != OriginNone || res.Reason == "" {
		t.Errorf("Required = %+v, want no stamp, no origin and the reason", res)
	}
}

// TestAnAbsentNodeClearsTheCache: a read that found nothing is an answer too,
// and it must take the cache with it. Otherwise a node removed by hand (T7's
// recovery) would keep refusing from a fresh entry for the rest of the window,
// which is exactly the state the hand edit was made to end.
func TestAnAbsentNodeClearsTheCache(t *testing.T) {
	r := &fakeReader{raw: "null"}
	src, getenv := testSource(t, r, nil)
	seedCache(t, getenv, urlA, "0.29.0", testNow.Add(-time.Minute))
	res, err := src.Required(context.Background(), Query{URL: urlA, Now: testNow, Fresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Found || res.Origin != OriginDatabase {
		t.Fatalf("Required = %+v, want an absent node read from the database", res)
	}
	if _, _, ok := cachedStamp(t, getenv, urlA); ok {
		t.Error("the cache outlived a read that found no stamp")
	}
	// And the next ordinary command reads too, rather than answering a value the
	// node no longer carries.
	if r.calls != 1 {
		t.Fatalf("the fresh read made %d call(s), want one", r.calls)
	}
	if _, err := src.Required(context.Background(), Query{URL: urlA, Now: testNow}); err != nil {
		t.Fatal(err)
	}
	if r.calls != 2 {
		t.Errorf("Required made %d call(s) in all, want two: the cleared cache answers nothing", r.calls)
	}
}
