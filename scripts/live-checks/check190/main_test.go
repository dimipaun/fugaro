// Tests for Check 190's throwaway proxy, and for the one thing the check's
// timeout row is measured through: the slow route — the prefix the proxy strips
// off the request path before it forwards. Nothing here reaches anything but an
// httptest server on the loopback, and no token of any kind is involved.
//
// The live run of 2026-10-30 answered the slow endpoint with a 502 after 0 s,
// so the 3600 s hold was never measured: a prefix ending in "/" is trimmed
// together with the separator that follows it, and the upstream URL built from
// what is left has no path at all.
package main

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

const testToken = "FAKE-PROXY-TOKEN"

func upstreamPathCases() []struct{ path, prefix, want string } {
	return []struct{ path, prefix, want string }{
		{"/chk190-slow/workspace-a/repo-a.git/info/refs", "/chk190-slow/", "/workspace-a/repo-a.git/info/refs"},
		{"/chk190-slow/workspace-a/repo-a.git/git-receive-pack", "/chk190-slow/", "/workspace-a/repo-a.git/git-receive-pack"},
		{"/workspace-a/repo-a.git/info/refs", "/chk190-slow/", "/workspace-a/repo-a.git/info/refs"},
		{"/chk190-slow/", "/chk190-slow/", "/"},
		// A prefix the check would never deploy: stripping it would eat the
		// separator, so it is not a prefix this proxy strips at all.
		{"/chk190-slow/workspace-a/repo-a.git/info/refs", "/chk190-slow", "/chk190-slow/workspace-a/repo-a.git/info/refs"},
	}
}

func TestTheSlowRouteStripsToTheSameUpstreamPath(t *testing.T) {
	for _, tc := range upstreamPathCases() {
		if got := upstreamPath(tc.path, tc.prefix); got != tc.want {
			t.Errorf("upstreamPath(%q, %q) = %q, want %q", tc.path, tc.prefix, got, tc.want)
		}
	}
}

// The upstream URL the proxy builds out of the slow route has to be a URL: the
// host, then the repository's own path. Concatenated with a path that lost its
// leading separator it is "https://bitbucket.orgworkspace-a/repo-a.git/...",
// which http.NewRequest refuses — the 502 after 0 s the live run saw.
func TestTheSlowRoutesUpstreamURLIsAURL(t *testing.T) {
	for _, tc := range upstreamPathCases() {
		target := "https://bitbucket.org" + upstreamPath(tc.path, tc.prefix)
		u, err := url.Parse(target)
		if err != nil {
			t.Errorf("the upstream URL for %q is not a URL: %v", tc.path, err)
			continue
		}
		if u.Host != "bitbucket.org" || u.Path != tc.want {
			t.Errorf("the upstream URL for %q is %q (host %q, path %q), want host bitbucket.org and path %q",
				tc.path, target, u.Host, u.Path, tc.want)
		}
	}
}

const testSlowPath = "/chk190-slow/workspace-a/repo-a.git/info/refs"

func TestAStallPrefixWithoutBothSeparatorsIsRefused(t *testing.T) {
	for _, p := range []string{"/chk190-slow", "chk190-slow/", "chk190-slow"} {
		if validStallPrefix(p) {
			t.Errorf("validStallPrefix(%q) = true, want false: a prefix that does not start and end with \"/\" is stripped with the separator beside it", p)
		}
		if got := upstreamPath(testSlowPath, p); got != testSlowPath {
			t.Errorf("upstreamPath(%q, %q) = %q, want the path untouched", testSlowPath, p, got)
		}
	}
	for _, p := range []string{"", "/", "/chk190-slow/"} {
		if !validStallPrefix(p) {
			t.Errorf("validStallPrefix(%q) = false, want true", p)
		}
	}
}

func proxyServer(t *testing.T, upstreamURL, stallPrefix string, stallSeconds int) *httptest.Server {
	t.Helper()
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		serveProxy(w, r, client, upstreamURL, testToken, stallPrefix, stallSeconds)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

type seenRequest struct {
	method   string
	uri      string
	auth     string
	body     string
	at       time.Time
	received chan struct{}
}

func recordingUpstream(t *testing.T) (*httptest.Server, *[]seenRequest) {
	t.Helper()
	var mu sync.Mutex
	seen := &[]seenRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		*seen = append(*seen, seenRequest{
			method: r.Method, uri: r.URL.RequestURI(), auth: r.Header.Get("Authorization"),
			body: string(body), at: time.Now(),
		})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/x-git-receive-pack-result")
		io.WriteString(w, "upstream answered")
	}))
	t.Cleanup(srv.Close)
	return srv, seen
}

// The slow push is two requests: the GET of info/refs and the receive-pack POST.
// Both have to arrive at the upstream with the prefix stripped off and the
// answer has to carry the mark the check's row is read with — a 502 from the
// proxy carries no X-Chk190-Upstream and is read as "the request never reached
// Bitbucket".
func TestARequestThroughTheStallPrefixReachesTheUpstream(t *testing.T) {
	upstream, seen := recordingUpstream(t)
	srv := proxyServer(t, upstream.URL, "/chk190-slow/", 0)

	for _, tc := range []struct {
		method string
		body   string
	}{
		{http.MethodGet, ""},
		{http.MethodPost, "push-body"},
	} {
		for _, prefix := range []string{"/chk190-slow", ""} {
			target := srv.URL + prefix + "/workspace-a/repo-a.git"
			if tc.method == http.MethodGet {
				target += "/info/refs?service=git-receive-pack"
			} else {
				target += "/git-receive-pack"
			}
			var resp *http.Response
			var err error
			if tc.method == http.MethodGet {
				resp, err = http.Get(target)
			} else {
				resp, err = http.Post(target, "application/x-git-receive-pack-request", strings.NewReader(tc.body))
			}
			if err != nil {
				t.Fatalf("%s %s: %v", tc.method, target, err)
			}
			got, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("%s through %q answered %d (%q), so the request never reached the upstream",
					tc.method, prefix, resp.StatusCode, string(got))
			}
			if mark := resp.Header.Get(upstreamMark); mark != "200" {
				t.Errorf("%s through %q carries %s: %q, want 200", tc.method, prefix, upstreamMark, mark)
			}
		}
	}

	// The loop above sends the GET twice and then the POST twice: through the
	// slow route and without it. All four reach the upstream with the prefix
	// stripped off, so the two routes are one upstream path.
	want := []seenRequest{
		{method: http.MethodGet, uri: "/workspace-a/repo-a.git/info/refs?service=git-receive-pack"},
		{method: http.MethodGet, uri: "/workspace-a/repo-a.git/info/refs?service=git-receive-pack"},
		{method: http.MethodPost, uri: "/workspace-a/repo-a.git/git-receive-pack", body: "push-body"},
		{method: http.MethodPost, uri: "/workspace-a/repo-a.git/git-receive-pack", body: "push-body"},
	}
	got := *seen
	if len(got) != len(want) {
		t.Fatalf("the upstream saw %d requests, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].method != w.method || got[i].uri != w.uri || got[i].body != w.body {
			t.Errorf("upstream request %d was %s %q (body %q), want %s %q (body %q)",
				i, got[i].method, got[i].uri, got[i].body, w.method, w.uri, w.body)
		}
		if wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("x-token-auth:"+testToken)); got[i].auth != wantAuth {
			t.Errorf("upstream request %d carried %q, want the proxy's own credential", i, got[i].auth)
		}
	}
}

// The hold is the row's whole point: the POST through the prefix must not reach
// the upstream before it.
func TestTheSlowPostIsHeldForTheConfiguredSeconds(t *testing.T) {
	upstream, seen := recordingUpstream(t)
	srv := proxyServer(t, upstream.URL, "/chk190-slow/", 1)

	begin := time.Now()
	resp, err := http.Post(srv.URL+"/chk190-slow/workspace-a/repo-a.git/git-receive-pack",
		"application/x-git-receive-pack-request", strings.NewReader("push-body"))
	if err != nil {
		t.Fatalf("the held POST: %v", err)
	}
	resp.Body.Close()
	elapsed := time.Since(begin)

	got := *seen
	if len(got) != 1 {
		t.Fatalf("the upstream saw %d requests, want 1", len(got))
	}
	if held := got[0].at.Sub(begin); held < time.Second {
		t.Errorf("the upstream saw the POST after %s, want it held for at least the configured second", held)
	}
	if elapsed < time.Second {
		t.Errorf("the caller was answered after %s, want at least the configured second", elapsed)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("the held POST was answered %d, want the upstream's 200", resp.StatusCode)
	}
}
