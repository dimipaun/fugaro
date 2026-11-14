package bbtokens

// The project lookup's tests (T5): whom it asks, with which token, what it
// holds on to, and what it refuses to spend.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
)

// The UUIDs and the token values below are plain words and never token-shaped
// strings (the plan's conventions).
const (
	projectUUID = "{acme-project}"
	otherUUID   = "{other-project}"
)

// tokCI is one project token's document key, spelled as the bundle spells it.
const tokCI Name = "bitbucket-token--ci"

// testTokens is the bundle's Bitbucket token entries as the contract's
// BundleTokens answers them: the names it holds and each entry's value.
type testTokens map[Name]string

func (t testTokens) Names() []Name {
	names := make([]Name, 0, len(t))
	for name := range t {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })
	return names
}

func (t testTokens) Value(name Name) (string, bool) {
	value, ok := t[name]
	return value, ok
}

// projectRecord is one project token's record: its type and the project UUID
// the operator stated for it.
func projectRecord(name Name, uuid string) Record {
	return Record{Name: name, Type: TypeProject, Coverage: []string{"acme"}, ProjectUUID: uuid}
}

// ---------------------------------------------------------------------------
// The fake API
// ---------------------------------------------------------------------------

// projectFake stands in for api.bitbucket.org. It records every call as the
// resolver built it — scheme, host, path, method and the credential — and
// refuses a call to any other host, which is what pins the lookup to the
// adapter's constant API root (F22): a resolver that addressed somewhere else
// fails here rather than being answered.
type projectFake struct {
	srv *httptest.Server

	mu      sync.Mutex
	calls   []projectSeen
	answers map[string]projectReply
}

// projectSeen is one call as it arrived, before the hop is rewritten.
type projectSeen struct {
	method string
	scheme string
	host   string
	path   string
	accept string
	auth   string
}

// projectReply is what the fake answers one repository with.
type projectReply struct {
	status int
	uuid   string
}

func newProjectFake(t *testing.T) *projectFake {
	t.Helper()
	f := &projectFake{answers: map[string]projectReply{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		reply, ok := f.answers[r.URL.Path]
		f.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if reply.status != http.StatusOK {
			w.WriteHeader(reply.status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"project":{"uuid":"`+reply.uuid+`","key":"ACME"}}`)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// reply says what the API answers for one repository.
func (f *projectFake) reply(workspace, repo string, status int, uuid string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers["/2.0/repositories/"+workspace+"/"+repo] = projectReply{status: status, uuid: uuid}
}

// client is the HTTP client the resolver under test makes its lookups with:
// a call to any host but Bitbucket's own API root is refused.
func (f *projectFake) client() *http.Client {
	base, err := url.Parse(f.srv.URL)
	if err != nil {
		panic("the fake's URL is not a URL: " + err.Error())
	}
	return &http.Client{Transport: &projectTransport{base: base, fake: f}}
}

type projectTransport struct {
	base *url.URL
	fake *projectFake
}

func (h *projectTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "api.bitbucket.org" {
		return nil, fmt.Errorf("the project lookup addressed %s://%s, and Bitbucket's API root is api.bitbucket.org alone", r.URL.Scheme, r.URL.Host)
	}
	h.fake.record(projectSeen{
		method: r.Method,
		scheme: r.URL.Scheme,
		host:   r.URL.Host,
		path:   r.URL.Path,
		accept: r.Header.Get("Accept"),
		auth:   r.Header.Get("Authorization"),
	})
	c := r.Clone(r.Context())
	c.URL.Scheme = h.base.Scheme
	c.URL.Host = h.base.Host
	c.Host = h.base.Host
	return http.DefaultTransport.RoundTrip(c)
}

func (f *projectFake) record(s projectSeen) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
}

func (f *projectFake) seen() []projectSeen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]projectSeen(nil), f.calls...)
}

// ---------------------------------------------------------------------------
// T5's tests
// ---------------------------------------------------------------------------

func TestProjectOfCachesWithinTTL(t *testing.T) {
	f := newProjectFake(t)
	f.reply("acme", "app", http.StatusOK, projectUUID)
	l := NewProjectLookup([]Record{projectRecord(tokCI, projectUUID)}, testTokens{tokCI: "ci-value"}, f.client())
	now := time.Unix(0, 0)
	l.now = func() time.Time { return now }

	got, err := l.ProjectOf(context.Background(), "bitbucket/acme/app", tokCI)
	if err != nil {
		t.Fatalf("ProjectOf: %v", err)
	}
	if got != projectUUID {
		t.Fatalf("ProjectOf answered %q, want the API's own project UUID %q", got, projectUUID)
	}
	if n := len(f.seen()); n != 1 {
		t.Fatalf("the first lookup made %d calls, want 1", n)
	}

	// A second ask inside the TTL is served from memory: the API is not asked
	// again.
	if _, err := l.ProjectOf(context.Background(), "bitbucket/acme/app", tokCI); err != nil {
		t.Fatalf("the cached lookup: %v", err)
	}
	if n := len(f.seen()); n != 1 {
		t.Fatalf("a second lookup within the TTL made %d calls, want the 1 the cache holds", n)
	}

	// Past the TTL the API is asked again, and the fresh answer is what is
	// served.
	now = now.Add(ProjectLookupTTL)
	f.reply("acme", "app", http.StatusOK, otherUUID)
	got, err = l.ProjectOf(context.Background(), "bitbucket/acme/app", tokCI)
	if err != nil {
		t.Fatalf("the lookup past the TTL: %v", err)
	}
	if got != otherUUID {
		t.Fatalf("ProjectOf answered %q past the TTL, want the fresh %q", got, otherUUID)
	}
	if n := len(f.seen()); n != 2 {
		t.Fatalf("a lookup past the TTL made %d calls in total, want 2", n)
	}

	// The cache is bounded: more distinct repositories than it holds never grow
	// it past its bound.
	for i := 0; i <= maxProjectCacheEntries; i++ {
		repo := fmt.Sprintf("r%d", i)
		f.reply("acme", repo, http.StatusOK, projectUUID)
		if _, err := l.ProjectOf(context.Background(), "bitbucket/acme/"+repo, tokCI); err != nil {
			t.Fatalf("the lookup of %s: %v", repo, err)
		}
	}
	if n := len(l.entries); n > maxProjectCacheEntries {
		t.Fatalf("the cache holds %d entries, above its bound of %d", n, maxProjectCacheEntries)
	}
}

func TestProjectOfEvictsOn403And404(t *testing.T) {
	f := newProjectFake(t)
	f.reply("acme", "app", http.StatusOK, projectUUID)
	f.reply("acme", "hidden", http.StatusForbidden, "")
	f.reply("acme", "gone", http.StatusNotFound, "")
	l := NewProjectLookup([]Record{projectRecord(tokCI, projectUUID)}, testTokens{tokCI: "ci-value"}, f.client())

	if _, err := l.ProjectOf(context.Background(), "bitbucket/acme/app", tokCI); err != nil {
		t.Fatalf("the first lookup: %v", err)
	}
	// Evicting a cached answer is what the proxy does on its own 403 or 404
	// (T9): the next lookup asks again.
	l.Evict("bitbucket/acme/app")
	if _, err := l.ProjectOf(context.Background(), "bitbucket/acme/app", tokCI); err != nil {
		t.Fatalf("the lookup after Evict: %v", err)
	}
	if n := len(f.seen()); n != 2 {
		t.Fatalf("the lookup after Evict made %d calls in total, want 2", n)
	}

	// A 403 and a 404 are answers about the token that asked and not about the
	// repository, so neither is cached: the next call asks again.
	for _, repo := range []string{"hidden", "gone"} {
		before := len(f.seen())
		for i := 0; i < 2; i++ {
			got, err := l.ProjectOf(context.Background(), "bitbucket/acme/"+repo, tokCI)
			if err != nil {
				t.Fatalf("the lookup of %s: %v", repo, err)
			}
			if got != "" {
				t.Fatalf("the lookup of %s answered %q, want not covered", repo, got)
			}
		}
		if n := len(f.seen()) - before; n != 2 {
			t.Fatalf("two lookups of %s made %d calls, want 2: a refused answer is not cached", repo, n)
		}
	}
}

func TestProjectOfUsesTheCandidateProjectToken(t *testing.T) {
	f := newProjectFake(t)
	f.reply("acme", "app", http.StatusOK, projectUUID)
	f.reply("acme", "boom", http.StatusInternalServerError, "")
	records := []Record{
		projectRecord(tokCI, projectUUID),
		projectRecord("bitbucket-token--other", otherUUID),
		{Name: "bitbucket-token--all", Type: TypeWorkspace, Coverage: []string{"acme/*"}},
		{Name: "bitbucket-token--one", Type: TypeRepo, Coverage: []string{"acme/app"}},
		{Name: "bitbucket-token", Coverage: []string{"acme/app"}},
	}
	tokens := testTokens{
		tokCI:                    "ci-value",
		"bitbucket-token--other": "other-value",
		"bitbucket-token--all":   "all-value",
		"bitbucket-token--one":   "one-value",
		"bitbucket-token":        "legacy-value",
	}
	l := NewProjectLookup(records, tokens, f.client())

	if _, err := l.ProjectOf(context.Background(), "bitbucket/acme/app", "bitbucket-token--other"); err != nil {
		t.Fatalf("ProjectOf: %v", err)
	}
	seen := f.seen()
	if len(seen) != 1 {
		t.Fatalf("the lookup made %d calls, want 1", len(seen))
	}
	if want := "Bearer other-value"; seen[0].auth != want {
		t.Fatalf("the lookup sent %q, want the candidate project token's own credential %q", seen[0].auth, want)
	}

	// A workspace token, a repository token and the untyped legacy key are
	// never spent on this lookup: each is answered "not covered" without a
	// call, because the project UUID of a repository is never confirmed with a
	// wider or a narrower credential (R2).
	for _, name := range []Name{"bitbucket-token--all", "bitbucket-token--one", "bitbucket-token"} {
		got, err := l.ProjectOf(context.Background(), "bitbucket/acme/app", name)
		if err != nil {
			t.Fatalf("the lookup with %s: %v", name, err)
		}
		if got != "" {
			t.Fatalf("the lookup with %s answered %q, want not covered", name, got)
		}
	}
	if n := len(f.seen()); n != 1 {
		t.Fatalf("a lookup with a workspace, repository or untyped token made a call (%d calls in total)", n)
	}

	// A status that is neither an answer nor a refusal is an error, and the
	// error names the token and never its value.
	_, err := l.ProjectOf(context.Background(), "bitbucket/acme/boom", tokCI)
	if err == nil {
		t.Fatal("a 500 from the API was answered as a lookup result, want an error")
	}
	if strings.Contains(err.Error(), "ci-value") {
		t.Fatalf("the error carries the token's value: %v", err)
	}
	if !strings.Contains(err.Error(), string(tokCI)) {
		t.Fatalf("the error does not name the token: %v", err)
	}
}

func TestProjectOfEmptyUUIDNeverMatches(t *testing.T) {
	f := newProjectFake(t)
	f.reply("acme", "app", http.StatusOK, "")
	l := NewProjectLookup([]Record{
		projectRecord("bitbucket-token--unstated", ""),
		projectRecord(tokCI, projectUUID),
	}, testTokens{
		"bitbucket-token--unstated": "unstated-value",
		tokCI:                       "ci-value",
	}, f.client())

	// A record that states no project UUID can never match, so the API is not
	// asked with its token at all.
	got, err := l.ProjectOf(context.Background(), "bitbucket/acme/app", "bitbucket-token--unstated")
	if err != nil {
		t.Fatalf("the lookup with a record that states no UUID: %v", err)
	}
	if got != "" {
		t.Fatalf("a record that states no project UUID answered %q, want not covered", got)
	}
	if n := len(f.seen()); n != 0 {
		t.Fatalf("a record that states no project UUID made %d calls, want none", n)
	}

	// An answer that states no project UUID matches nothing either, and is not
	// held: the next call asks again.
	for i := 0; i < 2; i++ {
		got, err := l.ProjectOf(context.Background(), "bitbucket/acme/app", tokCI)
		if err != nil {
			t.Fatalf("the lookup of an answer with no project UUID: %v", err)
		}
		if got != "" {
			t.Fatalf("an answer with no project UUID was served as %q, want not covered", got)
		}
	}
	if n := len(f.seen()); n != 2 {
		t.Fatalf("two lookups of an empty answer made %d calls, want 2: an empty answer is not cached", n)
	}
}

func TestProjectLookupHostIsConstant(t *testing.T) {
	f := newProjectFake(t)
	f.reply("acme", "app", http.StatusOK, projectUUID)
	l := NewProjectLookup([]Record{projectRecord(tokCI, projectUUID)}, testTokens{tokCI: "ci-value"}, f.client())

	if _, err := l.ProjectOf(context.Background(), "bitbucket/acme/app", tokCI); err != nil {
		t.Fatalf("ProjectOf: %v", err)
	}
	seen := f.seen()
	if len(seen) != 1 {
		t.Fatalf("the lookup made %d calls, want 1", len(seen))
	}
	if seen[0].method != http.MethodGet {
		t.Fatalf("the lookup used %s, want GET", seen[0].method)
	}
	if seen[0].scheme != "https" || seen[0].host != "api.bitbucket.org" {
		t.Fatalf("the lookup addressed %s://%s, and only the adapter's constant API root may be addressed", seen[0].scheme, seen[0].host)
	}
	asked := seen[0].scheme + "://" + seen[0].host + seen[0].path
	if want := bitbucket.DefaultBaseURL + "/repositories/acme/app"; asked != want {
		t.Fatalf("the lookup asked %s, want the adapter's constant API root with the repository under it: %s", asked, want)
	}
	if seen[0].accept != "application/json" {
		t.Fatalf("the lookup sent Accept %q, want application/json", seen[0].accept)
	}
}
