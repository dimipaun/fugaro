package updatecheck

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
		t.Fatalf("mode = %v, want 0600", fi.Mode().Perm())
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
// check stops it.
func TestFetchResponseBounded(t *testing.T) {
	huge := `{"tag_name":"v0.7.0","padding":"` + strings.Repeat("x", 2*maxBody) + `"}`
	srv := releaseServer(t, huge, http.StatusOK)
	defer srv.Close()
	v, ok := Fetch(context.Background(), http.DefaultClient, srv.URL)
	if ok {
		t.Fatalf("Fetch of an oversized body ok = true, v = %q, want ok = false", v)
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
