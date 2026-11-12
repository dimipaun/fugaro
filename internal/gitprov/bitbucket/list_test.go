package bitbucket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov/httpjson"
)

// listServer stands in for the Bitbucket API's workspace listing: it answers
// the page the query asks for, and records what it was asked and with which
// credential.
type listServer struct {
	srv *httptest.Server
	// pages are the pages the listing is cut into, in order; a page's "next"
	// is built from the next one, and the last page carries none.
	pages [][]string
	// next is an override for the first page's "next", for the case where the
	// API answers a link the listing must refuse.
	next string
	// status is the answer's status for every request but a page (a refusal
	// has no pages).
	status int

	seen []listCall
}

type listCall struct {
	path, query, auth string
	// filter is the decoded ?q= the call carried: the API's query language is
	// read here as the API reads it, not as the wire escapes it.
	filter string
}

func newListServer(t *testing.T, pages ...[]string) *listServer {
	t.Helper()
	f := &listServer{pages: pages, status: http.StatusOK}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.seen = append(f.seen, listCall{r.URL.EscapedPath(), r.URL.RawQuery, r.Header.Get("Authorization"), r.URL.Query().Get("q")})
		if f.status != http.StatusOK {
			w.WriteHeader(f.status)
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page < 1 {
			page = 1
		}
		if page > len(f.pages) {
			t.Errorf("the listing asked for page %d of %d", page, len(f.pages))
			w.WriteHeader(http.StatusNotFound)
			return
		}
		values := make([]map[string]any, 0, len(f.pages[page-1]))
		for _, name := range f.pages[page-1] {
			ws, slug, _ := strings.Cut(name, "/")
			values = append(values, map[string]any{"full_name": name, "slug": slug, "workspace": map[string]any{"slug": ws}})
		}
		body := map[string]any{"values": values, "page": page, "pagelen": listPageSize, "size": len(values)}
		switch {
		case page == 1 && f.next != "":
			body["next"] = f.next
		case page < len(f.pages):
			body["next"] = fmt.Sprintf("%s/2.0/repositories/acme?page=%d&pagelen=%d", f.srv.URL, page+1, listPageSize)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *listServer) list(t *testing.T, o ListOptions) ([]string, error) {
	t.Helper()
	if o.BaseURL == "" {
		o.BaseURL = f.srv.URL + "/2.0"
	}
	if o.HTTP == nil {
		o.HTTP = f.srv.Client()
	}
	return ListRepositories(context.Background(), o)
}

// TestBitbucketListPaginates: the listing follows the body's "next" until a
// page answers none, and returns the workspace's repositories as
// "<workspace>/<slug>", sorted and deduplicated. A one-page listing is the
// case a fixture with a single page would have made pass without ever reading
// the second, which is why the first page's answer here is short and its
// "next" is what the rest of the workspace arrives on.
//
// Mutation (verified, run and restored): stop after the first page (drop the
// page.Next branch) — this test fails, the second page's repositories missing.
func TestBitbucketListPaginates(t *testing.T) {
	f := newListServer(t,
		[]string{"acme/app", "acme/dup"},
		[]string{"acme/dup", "acme/zeta"},
	)
	got, err := f.list(t, ListOptions{Workspace: "acme", Token: "token-abcdef"})
	if err != nil {
		t.Fatalf("ListRepositories: %v", err)
	}
	if want := []string{"acme/app", "acme/dup", "acme/zeta"}; !slices.Equal(got, want) {
		t.Errorf("ListRepositories = %v, want %v (sorted, deduplicated across pages)", got, want)
	}
	if len(f.seen) != 2 {
		t.Fatalf("the listing made %d calls, want 2 (one per page)", len(f.seen))
	}
	// The page size is the API's largest, and the second call is the "next"
	// the first answered rather than the first page again.
	if !strings.Contains(f.seen[0].query, "pagelen="+strconv.Itoa(listPageSize)) {
		t.Errorf("the first call asked %q, want the largest page size", f.seen[0].query)
	}
	if !strings.Contains(f.seen[1].query, "page=2") {
		t.Errorf("the second call asked %q, want the next page", f.seen[1].query)
	}
	// The credential is on every call, as the bearer token.
	for i, call := range f.seen {
		if call.auth != "Bearer token-abcdef" {
			t.Errorf("call %d sent Authorization %q, want the token as a bearer credential", i, call.auth)
		}
		if call.path != "/2.0/repositories/acme" {
			t.Errorf("call %d asked %s, want the workspace's repositories", i, call.path)
		}
	}
}

// A project narrows the listing to that project's repositories: the filter is
// Bitbucket's own query language, and the project key goes into a quoted
// literal there, so a key carrying a quote cannot splice a clause of its own
// into the query.
func TestBitbucketListFiltersByProject(t *testing.T) {
	f := newListServer(t, []string{"acme/app"})
	if _, err := f.list(t, ListOptions{Workspace: "acme", Project: `PRO"J`, Token: "token-abcdef"}); err != nil {
		t.Fatalf("ListRepositories: %v", err)
	}
	if len(f.seen) != 1 {
		t.Fatalf("the listing made %d calls, want 1", len(f.seen))
	}
	if want := `project.key="PRO\"J"`; f.seen[0].filter != want {
		t.Errorf("the call asked q=%q, want %s", f.seen[0].filter, want)
	}
}

// The listing's bearer token is a credential a person typed, so it must not
// reach an error. A refusal (an expired or wrongly-scoped token) is the answer
// that carries the most of what the API said, and even that one is the status
// and the API's own body -- never the request's Authorization header.
//
// Mutation (verified, run and restored): put the token in the error (quote it
// in the refusal's sentence) — this test fails, naming it.
func TestBitbucketListErrorNeverCarriesTheToken(t *testing.T) {
	const token = "token-not-a-real-credential-1234"
	f := newListServer(t)
	f.status = http.StatusUnauthorized
	_, err := f.list(t, ListOptions{Workspace: "acme", Token: token})
	if err == nil {
		t.Fatal("ListRepositories accepted a refused listing")
	}
	var se *httpjson.StatusError
	if !errors.As(err, &se) || se.Status != http.StatusUnauthorized {
		t.Errorf("err = %v, want the API's refusal", err)
	}
	if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "Bearer") {
		t.Errorf("the refusal quotes the credential: %v", err)
	}
}

// A "next" the API answers on another host is refused rather than followed:
// the request carries the workspace's token, and a link is a value the API (or
// anything between here and it) writes.
func TestBitbucketListRefusesANextPageOnAnotherHost(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the listing followed a next page to another host: %s", r.URL)
	}))
	t.Cleanup(other.Close)
	f := newListServer(t, []string{"acme/app"})
	f.next = other.URL + "/2.0/repositories/acme?page=2"
	if _, err := f.list(t, ListOptions{Workspace: "acme", Token: "token-abcdef"}); err == nil || !strings.Contains(err.Error(), "paging left the API host") {
		t.Fatalf("err = %v, want the refusal of a next page on another host", err)
	}
}

// A listing that never ends is refused: "next" is followed at most maxListPages
// times, so a workspace whose answers keep pointing at another page is a
// refusal rather than a command that never returns.
func TestBitbucketListRefusesMorePagesThanItsBound(t *testing.T) {
	f := newListServer(t, []string{"acme/app"})
	f.next = f.srv.URL + "/2.0/repositories/acme?page=1" // the same page, forever
	_, err := f.list(t, ListOptions{Workspace: "acme", Token: "token-abcdef"})
	if err == nil || !strings.Contains(err.Error(), "more than 20 pages") {
		t.Fatalf("err = %v, want the refusal of a listing past its page bound", err)
	}
}

// Without a workspace or a token there is nothing to ask, and the refusal says
// which one is missing rather than sending an anonymous call.
//
// Mutation (verified, run and restored): drop either guard — the case it
// covers fails, the listing calling the API for a workspace it never named or
// with no credential at all.
func TestBitbucketListNeedsAWorkspaceAndAToken(t *testing.T) {
	f := newListServer(t, []string{"acme/app"})
	for _, c := range []struct {
		o    ListOptions
		want string
	}{
		{o: ListOptions{Token: "token-abcdef"}, want: "a workspace is required"},
		{o: ListOptions{Workspace: "acme"}, want: "an access token is required"},
	} {
		_, err := f.list(t, c.o)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("ListRepositories(%+v) = %v, want the refusal saying %q", c.o, err, c.want)
		}
	}
	// And the refusal comes before the call: a listing that asked anyway would
	// send an anonymous request, or one for a workspace nobody named.
	if len(f.seen) != 0 {
		t.Errorf("a listing with a required field missing made %d calls, want none: %+v", len(f.seen), f.seen)
	}
}

// A repository whose answer omits full_name is still listed, from the slug and
// the workspace the request named: a listing that dropped it would read as a
// repository the workspace does not hold.
func TestBitbucketListNamesARepositoryWithoutFullName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"values":[{"slug":"app","name":"App"},{"full_name":"acme/web","slug":"web"}]}`)
	}))
	t.Cleanup(srv.Close)
	got, err := ListRepositories(context.Background(), ListOptions{Workspace: "acme", Token: "token-abcdef", BaseURL: srv.URL + "/2.0", HTTP: srv.Client()})
	if err != nil {
		t.Fatalf("ListRepositories: %v", err)
	}
	if want := []string{"acme/app", "acme/web"}; !slices.Equal(got, want) {
		t.Errorf("ListRepositories = %v, want %v", got, want)
	}
}
