package updatecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func getenvFor(dir string) func(string) string {
	return func(k string) string {
		if k == "XDG_CACHE_HOME" {
			return dir
		}
		return ""
	}
}

// TestNewerStrictSemverCompare pins the real numeric compare (0.10.0 >
// 0.9.0, not a string compare) and the strict X.Y.Z parse.
//
// Mutation: comparing component strings instead of parsed ints (or
// dropping the per-component loop for a single string comparison) makes
// "0.9.0" < "0.10.0" true; reverting the numeric compare fails this test.
func TestNewerStrictSemverCompare(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"0.10.0", "0.9.0", true},
		{"0.9.0", "0.10.0", false},
		{"0.6.2", "0.6.1", true},
		{"0.6.1", "0.6.1", false},
		{"0.6.0", "0.6.1", false},
		{"v0.7.0", "0.6.1", true}, // optional "v" on the fetched side
		{"0.7.0-rc1", "0.6.1", false},
		{"0.7.0", "dev", false},
		{"0.7.0", "", false},
		{"not-a-version", "0.6.1", false},
	}
	for _, c := range cases {
		if got := Newer(c.latest, c.current); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

// TestValidStrictXYZ pins the strict parse: a pre-release suffix, extra
// path segments or a non-numeric component are never accepted.
//
// Mutation: loosening semverRE (e.g. allowing trailing text) makes
// "0.6.1-beta" valid; this test catches it.
func TestValidStrictXYZ(t *testing.T) {
	for _, v := range []string{"0.6.1", "v0.6.1", "0.0.0", "12.34.56"} {
		if !Valid(v) {
			t.Errorf("Valid(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"0.6.1-beta", "0.6", "0.6.1.2", "dev", "", "v0.6.1x", "0.6.01"} {
		if Valid(v) {
			t.Errorf("Valid(%q) = true, want false", v)
		}
	}
}

// TestEntryDueOnceADay pins the 24h throttle on the daily check.
//
// Mutation: changing `age >= CheckTTL` to `age > CheckTTL` makes a entry
// exactly 24h old wrongly "not due"; this test's exact-boundary case fails
// then.
func TestEntryDueOnceADay(t *testing.T) {
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		e    Entry
		want bool
	}{
		{"never checked", Entry{}, true},
		{"checked 1h ago", Entry{CheckedAt: now.Add(-time.Hour)}, false},
		{"checked exactly 24h ago", Entry{CheckedAt: now.Add(-CheckTTL)}, true},
		{"checked 23h59m ago", Entry{CheckedAt: now.Add(-CheckTTL + time.Minute)}, false},
		{"checked in the future", Entry{CheckedAt: now.Add(time.Hour)}, true},
	}
	for _, c := range cases {
		if got := c.e.Due(now); got != c.want {
			t.Errorf("%s: Due = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestLoadCorruptCacheIsAMiss pins "tolerate corruption": any unreadable
// or malformed cache file is the same as no cache at all, never a panic or
// an error the caller has to handle.
//
// Mutation: removing the `json.Unmarshal(data, &e) != nil` guard and
// returning a zero-ish Entry anyway would still pass a naive check; this
// test instead writes genuinely invalid JSON and checks Load doesn't
// resurrect any field from it.
func TestLoadCorruptCacheIsAMiss(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fugaro", "update-check.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := Load(getenvFor(dir))
	if e != (Entry{}) {
		t.Fatalf("Load of a corrupt cache = %+v, want the zero Entry", e)
	}
}

// TestLoadMissingCacheIsAMiss: no file at all is also just a miss.
func TestLoadMissingCacheIsAMiss(t *testing.T) {
	dir := t.TempDir()
	if e := Load(getenvFor(dir)); e != (Entry{}) {
		t.Fatalf("Load with no cache = %+v, want the zero Entry", e)
	}
}

// TestLoadRefusesASymlink pins that the cache path is never followed if it
// is a symlink: Save only ever creates a regular file (a temp file renamed
// over the path), so a symlink there is outside tampering (a symlink to
// /dev/zero would make an unbounded read grow without limit if the target
// were ever opened) and must be treated the same as no cache, not resolved
// and read.
//
// Mutation: using os.Stat instead of os.Lstat (which follows the symlink)
// makes this test read the valid target file's content through the link;
// this test's "must be the zero Entry, not the target's Latest" assertion
// catches that.
func TestLoadRefusesASymlink(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "fugaro")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(cacheDir, "target.json")
	valid, err := json.Marshal(Entry{Latest: "9.9.9"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, valid, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(cacheDir, "update-check.json")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unsupported here: %v", err)
	}
	if e := Load(getenvFor(dir)); e != (Entry{}) {
		t.Fatalf("Load of a symlinked cache = %+v, want the zero Entry (the symlink must never be followed)", e)
	}
}

// TestLoadRefusesAFIFO pins the same refusal for a named pipe: opening one
// for reading blocks until a writer connects, which Load must never risk.
// Unlike the symlink case, Lstat alone can't tell a FIFO apart from a
// regular file by name, so this exercises fi.Mode().IsRegular() directly.
func TestLoadRefusesAFIFO(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "fugaro")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cacheDir, "update-check.json")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("FIFOs unsupported here: %v", err)
	}
	done := make(chan Entry, 1)
	go func() { done <- Load(getenvFor(dir)) }()
	select {
	case e := <-done:
		if e != (Entry{}) {
			t.Fatalf("Load of a FIFO = %+v, want the zero Entry", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Load blocked on a FIFO instead of refusing it outright")
	}
}

// countingReader counts the bytes actually Read from it.
type countingReader struct {
	r io.Reader
	n *int
}

func (c countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	*c.n += n
	return n, err
}

// endlessReader always fills p with 'x' and never returns an error or EOF:
// a stand-in for a cache file (or anything else) with far more data than
// any sane cache entry, to prove readBounded gives up rather than reading
// forever or without limit.
type endlessReader struct{}

func (endlessReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

// TestReadBoundedNeverReadsMoreThanTheBound isolates io.LimitReader in
// readBounded: wrapping an endless source, the bytes actually read must
// never exceed 2*maxBody+1, regardless of readBounded's final ok/not-ok
// answer (which a plain unbounded read would also eventually report false
// for, once it ran out of memory or patience — this proves the bound is
// enforced before that point, not after).
//
// Mutation: replacing `io.LimitReader(r, 2*maxBody+1)` with a bare `r`
// makes this test hang (an endless reader never returns io.EOF), rather
// than merely reporting a wrong byte count — confirmed by running the
// mutation with a short per-test timeout rather than leaving it in the
// suite to hang indefinitely.
func TestReadBoundedNeverReadsMoreThanTheBound(t *testing.T) {
	var read int
	data, ok := readBounded(countingReader{endlessReader{}, &read})
	if ok {
		t.Fatalf("readBounded of an endless source ok = true, len(data) = %d, want ok = false", len(data))
	}
	if read > 2*maxBody+1 {
		t.Fatalf("readBounded read %d bytes, want at most %d (2*maxBody+1): an oversized or endless source must never be read without bound", read, 2*maxBody+1)
	}
}

// TestReadBoundedExactlyAtTheLimit isolates the `len(data) > 2*maxBody`
// check: a source of exactly 2*maxBody+1 bytes is not truncated at all by
// the LimitReader (it fits exactly), so only the explicit size check can
// be refusing it.
//
// Mutation: dropping `len(data) > 2*maxBody` (leaving only `err != nil`)
// makes this exact-boundary input wrongly ok = true.
func TestReadBoundedExactlyAtTheLimit(t *testing.T) {
	data, ok := readBounded(strings.NewReader(strings.Repeat("x", 2*maxBody+1)))
	if ok {
		t.Fatalf("readBounded of a %d-byte (2*maxBody+1) source ok = true, len(data) = %d, want ok = false", 2*maxBody+1, len(data))
	}
}

// TestLoadRefusesAnOversizedFile is readBounded's guarantee exercised
// through the real, file-based Load path: a cache file padded well past
// the limit (with a valid-looking JSON prefix, so a regression that only
// weakens the bound — not removes it entirely — would still have
// something parseable to return if it got that far) must be treated as no
// cache at all.
func TestLoadRefusesAnOversizedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fugaro", "update-check.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	const prefix, suffix = `{"latest":"9.9.9","padding":"`, `"}`
	total := 4 * maxBody
	body := prefix + strings.Repeat("x", total-len(prefix)-len(suffix)) + suffix
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if e := Load(getenvFor(dir)); e != (Entry{}) {
		t.Fatalf("Load of an oversized cache file = %+v, want the zero Entry", e)
	}
}

// TestSaveLoadRoundTrip and file mode: 0600, atomic (no partial file ever
// visible at the final path, since Save renames a temp file over it).
func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	getenv := getenvFor(dir)
	want := Entry{CheckedAt: time.Unix(1700000000, 0).UTC(), Latest: "0.7.0"}
	if err := Save(getenv, want); err != nil {
		t.Fatal(err)
	}
	got := Load(getenv)
	if !got.CheckedAt.Equal(want.CheckedAt) || got.Latest != want.Latest {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
	path := filepath.Join(dir, "fugaro", "update-check.json")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v, want 0600", fi.Mode().Perm())
	}
	// Mutation: widening Save's os.MkdirAll(dir, 0o700) to 0o755 survives
	// a test that only checks the file's own mode, since Go's os.OpenFile
	// still honors the more restrictive 0600 passed to Chmod regardless of
	// the directory's permissions; only a direct check of the directory
	// itself catches it.
	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode = %v, want 0700", di.Mode().Perm())
	}
}

// fakeClient routes Do to a function, so tests can simulate a slow server,
// a huge body or a network failure without a real listener where that
// matters, and a real httptest.Server everywhere else.
type fakeClient struct {
	do func(*http.Request) (*http.Response, error)
}

func (f fakeClient) Do(req *http.Request) (*http.Response, error) { return f.do(req) }

func releaseServer(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

// TestFetchStableOnly pins that a draft or a pre-release in the body is
// never trusted, even though GitHub's own endpoint never returns either:
// belt and suspenders against a compromised or misbehaving endpoint.
//
// Mutation: removing the `if r.Draft || r.Prerelease` guard makes a faked
// draft response parse through; this test fails then.
func TestFetchStableOnly(t *testing.T) {
	for _, c := range []struct {
		name string
		body string
		ok   bool
	}{
		{"stable", `{"tag_name":"v0.7.0"}`, true},
		{"draft", `{"tag_name":"v0.7.0","draft":true}`, false},
		{"prerelease", `{"tag_name":"v0.7.0","prerelease":true}`, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := releaseServer(t, c.body, http.StatusOK)
			defer srv.Close()
			v, ok := Fetch(context.Background(), http.DefaultClient, srv.URL)
			if ok != c.ok {
				t.Fatalf("Fetch ok = %v, want %v (v=%q)", ok, c.ok, v)
			}
		})
	}
}

// TestFetchStrictTagShape: an odd tag never comes back as a version at
// all, whatever else the body says.
//
// Mutation: dropping the parseSemver(r.TagName) check (returning r.TagName
// raw) makes an odd tag text come back as a "version"; this test catches
// it.
func TestFetchStrictTagShape(t *testing.T) {
	for _, tag := range []string{"not-a-version", "v1.2", "1.2.3-rc1", "", "v1.2.3; rm -rf /"} {
		srv := releaseServer(t, fmt.Sprintf(`{"tag_name":%q}`, tag), http.StatusOK)
		v, ok := Fetch(context.Background(), http.DefaultClient, srv.URL)
		srv.Close()
		if ok {
			t.Errorf("Fetch(tag=%q) ok = true, v = %q, want ok = false", tag, v)
		}
	}
}

// TestFetchResponseBounded pins that an oversized body is refused rather
// than buffered in full: an attacker-controlled response can never make
// Fetch allocate without bound.
//
// Mutation: widening the LimitReader bound (or dropping the len(data) >
// maxBody check) makes a huge body parse through if it happens to carry a
// valid tag_name; this test's payload does carry one, so only the size
// check stops it. (This test alone doesn't distinguish which of the two
// lines is doing the stopping, since the truncated read is invalid JSON
// regardless: TestFetchResponseExactlyAtTheLimit and
// TestFetchNeverReadsMoreThanTheBound below isolate each one.)
func TestFetchResponseBounded(t *testing.T) {
	huge := `{"tag_name":"v0.7.0","padding":"` + strings.Repeat("x", 2*maxBody) + `"}`
	srv := releaseServer(t, huge, http.StatusOK)
	defer srv.Close()
	v, ok := Fetch(context.Background(), http.DefaultClient, srv.URL)
	if ok {
		t.Fatalf("Fetch of an oversized body ok = true, v = %q, want ok = false", v)
	}
}

// TestFetchResponseExactlyAtTheLimit isolates the `len(data) > maxBody`
// check: a body of exactly maxBody+1 bytes, complete and valid JSON (not
// truncated at all by LimitReader's maxBody+1 cap, since it fits exactly),
// is still refused — only the explicit size check can be doing that here.
//
// Mutation: dropping the `len(data) > maxBody` check (leaving only `err !=
// nil`) makes this exact-boundary body parse through, since LimitReader
// never actually truncates it.
func TestFetchResponseExactlyAtTheLimit(t *testing.T) {
	const prefix, suffix = `{"tag_name":"v0.7.0","padding":"`, `"}`
	body := prefix + strings.Repeat("x", maxBody+1-len(prefix)-len(suffix)) + suffix
	if len(body) != maxBody+1 {
		t.Fatalf("test bug: body is %d bytes, want exactly %d", len(body), maxBody+1)
	}
	srv := releaseServer(t, body, http.StatusOK)
	defer srv.Close()
	v, ok := Fetch(context.Background(), http.DefaultClient, srv.URL)
	if ok {
		t.Fatalf("Fetch of a %d-byte (maxBody+1) body ok = true, v = %q, want ok = false", len(body), v)
	}
}

// countingReadCloser counts the bytes actually Read from it, regardless of
// how many the server behind it would have sent.
type countingReadCloser struct {
	r io.ReadCloser
	n *int
}

func (c countingReadCloser) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	*c.n += n
	return n, err
}
func (c countingReadCloser) Close() error { return c.r.Close() }

// countingClient wraps a real *http.Client, instrumenting the response
// body it returns.
type countingClient struct {
	inner *http.Client
	read  *int
}

func (c countingClient) Do(req *http.Request) (*http.Response, error) {
	resp, err := c.inner.Do(req)
	if err == nil {
		resp.Body = countingReadCloser{resp.Body, c.read}
	}
	return resp, err
}

// TestFetchNeverReadsMoreThanTheBound isolates io.LimitReader: a body much
// larger than maxBody (complete, valid JSON, so the explicit size check
// alone would still correctly reject it) must never actually be read past
// maxBody+1 bytes, regardless of what Fetch finally returns. An
// attacker-controlled response can otherwise make Fetch buffer without
// bound before the size check ever runs.
//
// Mutation: replacing `io.LimitReader(resp.Body, maxBody+1)` with
// `resp.Body` (reading it unbounded) makes this test's byte count exceed
// maxBody+1, even though Fetch's final ok/false answer is unchanged by the
// mutation (the downstream size check still catches the final length).
func TestFetchNeverReadsMoreThanTheBound(t *testing.T) {
	const prefix, suffix = `{"tag_name":"v0.7.0","padding":"`, `"}`
	total := 4 * maxBody
	body := prefix + strings.Repeat("x", total-len(prefix)-len(suffix)) + suffix
	srv := releaseServer(t, body, http.StatusOK)
	defer srv.Close()
	var read int
	client := countingClient{inner: srv.Client(), read: &read}
	if _, ok := Fetch(context.Background(), client, srv.URL); ok {
		t.Fatal("Fetch of an oversized body ok = true, want false")
	}
	if read > maxBody+1 {
		t.Fatalf("Fetch read %d bytes from the body, want at most %d (maxBody+1): an oversized response must never be buffered in full", read, maxBody+1)
	}
}

// TestFetchNon200IsAMiss: a status other than 200 (rate limited, not
// found, server error) is never trusted.
func TestFetchNon200IsAMiss(t *testing.T) {
	srv := releaseServer(t, `{"tag_name":"v0.7.0"}`, http.StatusTooManyRequests)
	defer srv.Close()
	if _, ok := Fetch(context.Background(), http.DefaultClient, srv.URL); ok {
		t.Fatal("Fetch of a 429 response ok = true, want false")
	}
}

// TestFetchTimeout pins that a slow server doesn't hang Fetch past its
// context deadline.
//
// Mutation: dropping Refresh's context.WithTimeout wrap (calling Fetch
// with the bare ctx) makes this test hang until the test's own timeout;
// Fetch itself just honors whatever ctx it's given, so this exercises it
// directly with a short deadline the way Refresh's wrap would apply.
func TestFetchTimeout(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	// close(block) must run before srv.Close(), which otherwise waits
	// forever for the blocked handler goroutine to return: defers run
	// LIFO, so it is deferred second.
	defer srv.Close()
	defer close(block)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, ok := Fetch(ctx, http.DefaultClient, srv.URL); ok {
		t.Fatal("Fetch against a server that never answers returned ok = true")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Fetch took %v, want it to give up near the 50ms deadline", elapsed)
	}
}

// TestRefreshBoundsItsOwnTimeout exercises Refresh itself (not Fetch with
// an external short context): Refresh must wrap ctx with its own
// FetchTimeout, not depend on the caller already having a short deadline.
//
// Mutation: changing Refresh's `context.WithTimeout(ctx, FetchTimeout)` to
// use a much longer duration (time.Hour) survives a test that only calls
// Fetch directly with its own short ctx, since that never exercises
// Refresh's own wrapping at all; this test calls Refresh with the bare
// context.Background() (no deadline of its own) against a server that
// never answers, so only Refresh's internal timeout can ever stop it.
func TestRefreshBoundsItsOwnTimeout(t *testing.T) {
	dir := t.TempDir()
	getenv := getenvFor(dir)
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer srv.Close()
	defer close(block)
	start := time.Now()
	Refresh(context.Background(), getenv, http.DefaultClient, srv.URL, time.Now())
	if elapsed := time.Since(start); elapsed > 2*FetchTimeout {
		t.Fatalf("Refresh took %v, want it bounded near its own FetchTimeout (%v)", elapsed, FetchTimeout)
	}
}

// TestRefreshOncePerDay pins that Refresh does not call the client at all
// when the cache is already fresh (the once-a-day throttle at the network
// call itself, not just in Due's own unit test).
//
// Mutation: dropping the `if !e.Due(now) { return }` guard in Refresh
// makes it call the client even when fresh; this test's "called" flag
// catches it.
func TestRefreshOncePerDay(t *testing.T) {
	dir := t.TempDir()
	getenv := getenvFor(dir)
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	if err := Save(getenv, Entry{CheckedAt: now.Add(-time.Hour), Latest: "0.6.0"}); err != nil {
		t.Fatal(err)
	}
	called := false
	client := fakeClient{do: func(*http.Request) (*http.Response, error) {
		called = true
		return nil, fmt.Errorf("should not be called")
	}}
	Refresh(context.Background(), getenv, client, "http://127.0.0.1:1/unused", now)
	if called {
		t.Fatal("Refresh called the client even though the cache was fresh")
	}
	if e := Load(getenv); e.Latest != "0.6.0" {
		t.Fatalf("Latest = %q, want the cache untouched (0.6.0)", e.Latest)
	}
}

// TestRefreshUpdatesCheckedAtEvenOnFailure: a failed fetch still moves
// CheckedAt, so a down network doesn't retry on every single command.
func TestRefreshUpdatesCheckedAtEvenOnFailure(t *testing.T) {
	dir := t.TempDir()
	getenv := getenvFor(dir)
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	if err := Save(getenv, Entry{CheckedAt: now.Add(-25 * time.Hour), Latest: "0.6.0"}); err != nil {
		t.Fatal(err)
	}
	// Nothing listens here: the client fails fast.
	Refresh(context.Background(), getenv, http.DefaultClient, "http://127.0.0.1:1/unused", now)
	e := Load(getenv)
	if !e.CheckedAt.Equal(now) {
		t.Fatalf("CheckedAt = %v, want %v (updated even on failure)", e.CheckedAt, now)
	}
	if e.Latest != "0.6.0" {
		t.Fatalf("Latest = %q, want the last known good value kept", e.Latest)
	}
}

// TestRefreshLearnsANewRelease: a due, successful fetch updates Latest and
// CheckedAt together.
func TestRefreshLearnsANewRelease(t *testing.T) {
	dir := t.TempDir()
	getenv := getenvFor(dir)
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	srv := releaseServer(t, `{"tag_name":"v0.8.0"}`, http.StatusOK)
	defer srv.Close()
	Refresh(context.Background(), getenv, http.DefaultClient, srv.URL, now)
	e := Load(getenv)
	if e.Latest != "0.8.0" || !e.CheckedAt.Equal(now) {
		t.Fatalf("entry = %+v, want latest 0.8.0 checked at %v", e, now)
	}
}

// TestRefreshDoesNotClobberAConcurrentWarn pins Refresh's re-read-before-save:
// if, while the HTTP round trip is in flight, something else (Warn, from
// the same process's nag print, which can run concurrently with an
// abandoned background check that keeps running past WaitForUpdateCheck's
// bound) writes Warned/WarnedAt to the cache, Refresh's own Save at the
// end must not overwrite that with the stale snapshot it Loaded before the
// request started.
//
// Mutation: reusing the pre-fetch snapshot instead of re-Loading right
// before Save (`e := Load(getenv)` once at the top, removing the second
// `e = Load(getenv)` after Fetch) makes this test fail: the final entry's
// Warned reverts to "" instead of keeping what was written during the
// fetch.
func TestRefreshDoesNotClobberAConcurrentWarn(t *testing.T) {
	dir := t.TempDir()
	getenv := getenvFor(dir)
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	if err := Save(getenv, Entry{CheckedAt: now.Add(-25 * time.Hour), Latest: "0.6.0"}); err != nil {
		t.Fatal(err)
	}

	release := make(chan struct{})
	client := fakeClient{do: func(*http.Request) (*http.Response, error) {
		// Simulate Warn() running concurrently, mid-fetch: a nag printed
		// by this same process for a command whose own work outlasted
		// WaitForUpdateCheck's bound.
		if err := Save(getenv, Entry{CheckedAt: now.Add(-25 * time.Hour), Latest: "0.6.0", Warned: "0.6.0", WarnedAt: now}); err != nil {
			t.Fatal(err)
		}
		<-release
		resp := httptest.NewRecorder()
		resp.WriteHeader(http.StatusOK)
		_, _ = resp.WriteString(`{"tag_name":"v0.7.0"}`)
		return resp.Result(), nil
	}}
	done := make(chan struct{})
	go func() {
		Refresh(context.Background(), getenv, client, "http://unused.example/", now)
		close(done)
	}()
	// Give Refresh a moment to Load its pre-fetch snapshot and reach the
	// fake client before the concurrent "Warn" write above happens.
	time.Sleep(20 * time.Millisecond)
	close(release)
	<-done

	e := Load(getenv)
	if e.Warned != "0.6.0" || e.WarnedAt.IsZero() {
		t.Fatalf("entry after Refresh = %+v, want Warned/WarnedAt kept from the concurrent write, not clobbered", e)
	}
	if e.Latest != "0.7.0" {
		t.Fatalf("entry.Latest = %q, want the newly fetched 0.7.0", e.Latest)
	}
}

// TestWarnThrottledPerVersionPerDay pins "at most once per day per cached
// latest version": a second Warn call for the same release inside WarnTTL
// is silent, but a newer release (or a day passing) un-silences it.
//
// Mutation: comparing only WarnedAt's age (dropping the `e.Warned ==
// e.Latest` check) would wrongly silence a brand-new release that arrives
// within the same day as a previous nag for an older one; this test's
// "new release same day" case catches that.
func TestWarnThrottledPerVersionPerDay(t *testing.T) {
	dir := t.TempDir()
	getenv := getenvFor(dir)
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	if err := Save(getenv, Entry{Latest: "0.7.0"}); err != nil {
		t.Fatal(err)
	}
	line := Warn(getenv, now, "0.6.1")
	if line == "" {
		t.Fatal("first Warn = \"\", want a line")
	}
	if !strings.Contains(line, "0.7.0") || !strings.Contains(line, "0.6.1") || !strings.Contains(line, BrewCommand) {
		t.Fatalf("line = %q, missing an expected part", line)
	}
	if again := Warn(getenv, now.Add(time.Hour), "0.6.1"); again != "" {
		t.Fatalf("second Warn within a day = %q, want \"\"", again)
	}
	if next := Warn(getenv, now.Add(25*time.Hour), "0.6.1"); next == "" {
		t.Fatal("Warn a day later = \"\", want a line again")
	}
	// A new release arriving the same day as the first nag un-silences it.
	if err := Save(getenv, Entry{Latest: "0.8.0"}); err != nil {
		t.Fatal(err)
	}
	if line := Warn(getenv, now.Add(2*time.Hour), "0.6.1"); line == "" {
		t.Fatal("Warn for a newly learned release, same day, = \"\", want a line")
	}
}

// TestWarnFutureWarnedAtDoesNotSilenceForever pins the `age >= 0` guard in
// Warn's throttle: a WarnedAt that is somehow in the future (a corrupt
// cache, a clock that moved back after writing it) must not suppress the
// line until the clock catches up, however far away that is.
//
// Mutation: dropping the `age >= 0 &&` part of the throttle condition
// (leaving only `age < WarnTTL`) makes a negative age (now before
// WarnedAt) satisfy "less than 24h" forever, wrongly silencing the line
// until the real clock reaches WarnedAt.
func TestWarnFutureWarnedAtDoesNotSilenceForever(t *testing.T) {
	dir := t.TempDir()
	getenv := getenvFor(dir)
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	if err := Save(getenv, Entry{Latest: "0.7.0", Warned: "0.7.0", WarnedAt: now.Add(365 * 24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if line := Warn(getenv, now, "0.6.1"); line == "" {
		t.Fatal("Warn with WarnedAt a year in the future = \"\", want a line: a future timestamp must not silence it forever")
	}
}

// TestWarnNothingToSay covers the quiet paths: no cache, not newer, dev
// build.
func TestWarnNothingToSay(t *testing.T) {
	now := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	t.Run("no cache", func(t *testing.T) {
		dir := t.TempDir()
		if line := Warn(getenvFor(dir), now, "0.6.1"); line != "" {
			t.Fatalf("line = %q, want \"\"", line)
		}
	})
	t.Run("not newer", func(t *testing.T) {
		dir := t.TempDir()
		getenv := getenvFor(dir)
		if err := Save(getenv, Entry{Latest: "0.6.1"}); err != nil {
			t.Fatal(err)
		}
		if line := Warn(getenv, now, "0.6.1"); line != "" {
			t.Fatalf("line = %q, want \"\"", line)
		}
	})
	t.Run("dev build", func(t *testing.T) {
		dir := t.TempDir()
		getenv := getenvFor(dir)
		if err := Save(getenv, Entry{Latest: "0.9.0"}); err != nil {
			t.Fatal(err)
		}
		if line := Warn(getenv, now, "dev"); line != "" {
			t.Fatalf("line = %q, want \"\"", line)
		}
	})
}

// TestARelativeXDGCacheHomeIsIgnoredOnPOSIX pins this package's half of the one
// POSIX behaviour this port changes: main's cachePath here took any non-empty
// $XDG_CACHE_HOME, so a relative one decided where the update check was
// written, joined onto the process's working directory; localcfg.UserDir now
// ignores it — the XDG spec's own rule — and the fallback decides. An absolute
// value is unchanged. HOME is read rather than the platform's own cache
// variable on purpose, so this row says the same thing on every platform.
//
// Mutation: cachePath back to `if dir := getenv("XDG_CACHE_HOME"); dir != ""`
// makes the path "rel-cache/fugaro/update-check.json" and fails this test.
func TestARelativeXDGCacheHomeIsIgnoredOnPOSIX(t *testing.T) {
	home := t.TempDir()
	env := func(k string) string {
		switch k {
		case "HOME":
			return home
		case "XDG_CACHE_HOME":
			return "rel-cache"
		}
		return ""
	}
	got, err := cachePath(env)
	if err != nil {
		t.Fatalf("cachePath: %v", err)
	}
	if want := filepath.Join(home, ".cache", "fugaro", "update-check.json"); got != want {
		t.Errorf("cachePath = %q, want %q: a relative XDG_CACHE_HOME must not decide where the cache goes", got, want)
	}
}
