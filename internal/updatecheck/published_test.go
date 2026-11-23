package updatecheck

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// publishedBody is one release object as GitHub's by-tag endpoint answers
// it: the subset Published reads, with the tag, draft and prerelease flags
// a case needs.
func publishedBody(tag string, draft, prerelease bool) string {
	return fmt.Sprintf(`{"tag_name":%q,"draft":%t,"prerelease":%t}`, tag, draft, prerelease)
}

// tagServer is a fake releases API: it answers body at status for every
// request and records the paths it was asked for, so one server proves both
// the answer and the URL the question was put to. releaseServer above
// answers the same way without the record, which most Fetch tests don't
// need; every Published test does, since the tag is the whole question.
func tagServer(t *testing.T, status int, body string) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), paths...)
	}
}

// TestPublishedIgnoresTheUpdateCheckSwitches pins D3269's first half: the
// question `update` asks about its own release is not the nag's, so the
// switch that silences the nag must not silence this — a CI or an offline
// owner who set it still gets a stamp written, or nothing ever raises one
// there. FUGARO_NO_UPDATE_CHECK is the switch this package can reach on its
// own; the project config's update_check: false is internal/cli's gate and
// no code here reads a config at all, which is the point.
//
// Mutation: gating Published on the env switch (returning false, or
// skipping the request) fails this test's call count and its true.
func TestPublishedIgnoresTheUpdateCheckSwitches(t *testing.T) {
	t.Setenv("FUGARO_NO_UPDATE_CHECK", "1")
	srv, paths := tagServer(t, http.StatusOK, publishedBody("v0.29.0", false, false))
	ok, err := Published(context.Background(), srv.Client(), srv.URL, "0.29.0")
	if err != nil {
		t.Fatalf("Published = %v, want the releases API still asked with the switch set", err)
	}
	if !ok {
		t.Fatal("Published = false, want true: the update-check switch is about the nag, never about whether this release is published")
	}
	if got := paths(); len(got) != 1 {
		t.Fatalf("the server was asked %v, want exactly one request", got)
	}
}

// TestPublishedIgnoresAStaleCache pins D3269's second half against the
// option it rejected: reusing the update-check cache and its "newest
// release" answer. The cache here says the newest release is 0.26.0, and
// both a stale and a fresh entry must be ignored — the answer must come
// from the releases API on every call, because a cached newest release is a
// different question and, being switched off by the switches above, would
// sometimes answer nothing at all.
//
// Mutation: answering from Load() (e.g. AtOrBelow(e.Latest, version)) fails
// this test: 0.29.0 is not at or below 0.26.0, and the server would never
// be asked.
func TestPublishedIgnoresAStaleCache(t *testing.T) {
	for _, c := range []struct {
		name    string
		checked time.Time
	}{
		{"a stale entry", time.Now().Add(-25 * time.Hour)},
		{"a fresh entry", time.Now()},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("XDG_CACHE_HOME", dir)
			if err := Save(getenvFor(dir), Entry{CheckedAt: c.checked, Latest: "0.26.0"}); err != nil {
				t.Fatal(err)
			}
			srv, paths := tagServer(t, http.StatusOK, publishedBody("v0.29.0", false, false))
			ok, err := Published(context.Background(), srv.Client(), srv.URL, "0.29.0")
			if err != nil {
				t.Fatalf("Published = %v, want the releases API asked despite the cache", err)
			}
			if !ok {
				t.Fatal("Published = false, want true: the update-check cache must never answer this question")
			}
			if got := paths(); len(got) != 1 {
				t.Fatalf("the server was asked %v, want exactly one request", got)
			}
		})
	}
}

// TestOlderLineHotfixIsPublished is the case that separates this lookup from
// "is there something newer" (D3269): 0.27.5 is published after 0.28.0, and
// the cache's newest release is 0.28.0, so a lookup that compared against
// the newest release (or read the cache at all) would answer false and leave
// the stamp of a perfectly good hotfix release unwritten.
//
// The server answers per path, the way the real API does — the newest
// release at /releases and /releases/latest, the hotfix at its own tag — so
// a lookup that asks the wrong question cannot get the right answer by
// accident.
//
// Mutation: implementing Published as Fetch(Endpoint) plus Newer(latest,
// version) fails this test: it reads /releases/latest (0.28.0), which is
// neither this version nor older than it.
func TestOlderLineHotfixIsPublished(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	if err := Save(getenvFor(dir), Entry{CheckedAt: time.Now(), Latest: "0.28.0"}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if r.URL.Path == "/tags/v0.27.5" {
			_, _ = w.Write([]byte(publishedBody("v0.27.5", false, false)))
			return
		}
		_, _ = w.Write([]byte(publishedBody("v0.28.0", false, false)))
	}))
	defer srv.Close()
	ok, err := Published(context.Background(), srv.Client(), srv.URL, "0.27.5")
	if err != nil {
		t.Fatalf("Published = %v, want an answer", err)
	}
	if !ok {
		t.Fatal("Published = false, want true: the question is about the tag, not about the newest release")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 1 || paths[0] != "/tags/v0.27.5" {
		t.Fatalf("the server was asked %v, want one request for /tags/v0.27.5", paths)
	}
}

// TestDraftOrPrereleaseIsNotPublished pins the flag half of "published": a
// tag can exist and still not be a release an `update` may write a stamp
// for. GitHub's by-tag endpoint answers a draft to a caller with push access
// and a pre-release to anyone, so both flags are read rather than inferred
// from a 404.
//
// Mutation: dropping `!r.Draft && !r.Prerelease` (returning true for any
// 200) fails the two negative cases.
func TestDraftOrPrereleaseIsNotPublished(t *testing.T) {
	for _, c := range []struct {
		name string
		body string
		want bool
	}{
		{"a published release", publishedBody("v0.29.0", false, false), true},
		{"a draft", publishedBody("v0.29.0", true, false), false},
		{"a pre-release", publishedBody("v0.29.0", false, true), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv, _ := tagServer(t, http.StatusOK, c.body)
			ok, err := Published(context.Background(), srv.Client(), srv.URL, "0.29.0")
			if err != nil {
				t.Fatalf("Published = %v, want an answer", err)
			}
			if ok != c.want {
				t.Fatalf("Published = %v, want %v", ok, c.want)
			}
		})
	}
}

// TestUnknownTagIsNotPublished pins a 404 as an answer rather than a
// failure: GitHub answers 404 for a tag that names no release (and for a
// draft to a caller without push access), which is the question itself. It
// also pins the URL: the one release's tag, not the collection or the
// "latest" endpoint, whose answer is about a different release entirely.
//
// Mutation: treating 404 as an unexpected status (an error) fails this
// test; so does asking Endpoint or ReleasesEndpoint itself, which the path
// assertion catches.
func TestUnknownTagIsNotPublished(t *testing.T) {
	srv, paths := tagServer(t, http.StatusNotFound, `{"message":"Not Found"}`)
	ok, err := Published(context.Background(), srv.Client(), srv.URL, "0.29.0")
	if err != nil {
		t.Fatalf("Published = %v, want 404 read as \"not a published release\", not as a failure", err)
	}
	if ok {
		t.Fatal("Published = true, want false for a tag GitHub does not have")
	}
	if got := paths(); len(got) != 1 || got[0] != "/tags/v0.29.0" {
		t.Fatalf("the server was asked %v, want one request for /tags/v0.29.0", got)
	}
}

// TestLookupFailureIsAnError pins the caller's rule (D3268, D3269): anything
// that stops Published answering says so, rather than quietly reporting
// "not published", so the stage leaves the stamp unchanged and prints why
// instead of pretending the release is missing. A wrong "yes" would write a
// stamp no release backs; a wrong "no" would silently never raise one.
//
// The two error-status cases carry a complete, published release object for
// the tag that was asked for — a body Published would answer true from if it
// were the only thing it read. That is what makes them tests of the status
// rule rather than of the parse: without the status switch, these bodies
// parse, match the tag and are published.
//
// Mutation: collapsing any of these to `return false, nil` (or to a bare
// miss, as Fetch does) fails the case, since each asserts a non-nil error;
// so does dropping the status switch, which leaves the 500 and the 429
// answering true from their bodies.
func TestLookupFailureIsAnError(t *testing.T) {
	t.Run("a network failure", func(t *testing.T) {
		srv, _ := tagServer(t, http.StatusOK, publishedBody("v0.29.0", false, false))
		srv.Close()
		ok, err := Published(context.Background(), srv.Client(), srv.URL, "0.29.0")
		if err == nil {
			t.Fatalf("Published = %v, nil, want an error when nothing answers", ok)
		}
		if ok {
			t.Fatal("Published = true, want false alongside the error")
		}
	})
	for _, c := range []struct {
		name   string
		status int
		body   string
	}{
		{"a server error", http.StatusInternalServerError, publishedBody("v0.29.0", false, false)},
		{"a rate limit", http.StatusTooManyRequests, publishedBody("v0.29.0", false, false)},
		{"an oversized body", http.StatusOK, `{"tag_name":"v0.29.0","padding":"` + strings.Repeat("x", 2*maxBody) + `"}`},
		{"a body that is not a release", http.StatusOK, `{not json`},
		{"an answer about another tag", http.StatusOK, publishedBody("v0.28.0", false, false)},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv, _ := tagServer(t, c.status, c.body)
			ok, err := Published(context.Background(), srv.Client(), srv.URL, "0.29.0")
			if err == nil {
				t.Fatalf("Published = %v, nil, want an error", ok)
			}
			if ok {
				t.Fatal("Published = true, want false alongside the error")
			}
		})
	}
}

// TestPublishedRefusesARedirect pins that a 3xx is never read through: the
// client refuses redirects (refuseRedirect, the one internal/cli wires in),
// so the redirect is what Published sees, and it must be an unexpected
// status rather than a trip to whatever host the responder named next.
//
// The redirect carries a complete, published release object for the tag that
// was asked for, written out by hand rather than by http.Redirect's HTML
// body, so the status alone is what refuses it.
//
// Mutation: accepting a 3xx as read-and-published fails this test: the body
// parses, matches the tag and answers true. Letting a client that follows
// redirects answer for us fails it too, since the second server records the
// request.
func TestPublishedRefusesARedirect(t *testing.T) {
	var reached atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		_, _ = w.Write([]byte(publishedBody("v0.29.0", false, false)))
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", target.URL+r.URL.Path)
		w.WriteHeader(http.StatusFound)
		_, _ = w.Write([]byte(publishedBody("v0.29.0", false, false)))
	}))
	defer redirector.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	ok, err := Published(context.Background(), client, redirector.URL, "0.29.0")
	if err == nil {
		t.Fatalf("Published = %v, nil, want a redirect read as an unexpected status", ok)
	}
	if ok {
		t.Fatal("Published = true, want false alongside the error")
	}
	if n := reached.Load(); n != 0 {
		t.Fatalf("the redirect target was fetched %d times, want 0: a redirect must never be followed", n)
	}
}

// TestPublishedRefusesANonReleaseVersion pins the guard in front of the
// request: a version that is not strictly X.Y.Z (a dev build, a pre-release,
// a branch name) has no release tag to ask about, and must be refused
// without a request — asking about "vdev" would either 404 confusingly or,
// worse, find something.
//
// Mutation: dropping the CanonicalRelease guard and building the tag from
// version as given makes the fake client's call count non-zero, and lets a
// garbage version look up a tag that happens to exist.
func TestPublishedRefusesANonReleaseVersion(t *testing.T) {
	for _, version := range []string{"dev", "", "0.29", "0.29.0-rc1", "next", "0.29.0.1"} {
		called := false
		client := fakeClient{do: func(*http.Request) (*http.Response, error) {
			called = true
			return nil, fmt.Errorf("Published asked the releases API about %q", version)
		}}
		ok, err := Published(context.Background(), client, "http://unused.example/releases", version)
		if err == nil {
			t.Fatalf("Published(%q) = %v, nil, want an error", version, ok)
		}
		if ok {
			t.Fatalf("Published(%q) = true, want false", version)
		}
		if called {
			t.Fatalf("Published(%q) asked the releases API, want no request for a version that is not a release", version)
		}
	}
}

// TestPublishedBoundsItsOwnTimeout pins that Published wraps the caller's
// context with its own FetchTimeout, as Refresh does: it is asked from
// `update`, and a caller that hands it a bare context must not be able to
// hang the command on a blackholed network.
//
// Mutation: removing the context.WithTimeout wrap makes this test wait on a
// server that never answers until the package's own timeout kills the run,
// instead of reporting an error near FetchTimeout.
func TestPublishedBoundsItsOwnTimeout(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	// close(block) must run before srv.Close(), which otherwise waits
	// forever for the blocked handler goroutine to return: defers run LIFO,
	// so it is deferred second.
	defer srv.Close()
	defer close(block)
	start := time.Now()
	ok, err := Published(context.Background(), srv.Client(), srv.URL, "0.29.0")
	if err == nil {
		t.Fatalf("Published = %v, nil, want an error when the server never answers", ok)
	}
	if elapsed := time.Since(start); elapsed > 2*FetchTimeout {
		t.Fatalf("Published took %v, want it bounded near its own FetchTimeout (%v)", elapsed, FetchTimeout)
	}
}
