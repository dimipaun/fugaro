package bitbucket

// The catalog's file read (D1140, PIC3): what the adapter asks Bitbucket
// Cloud for, what it refuses to ask for at all, and the one rule every
// refusal here keeps -- no value reaches an error.
//
// The fixtures are built at run time. No literal in this file is, or looks
// like, a credential.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

// contentsServer stands in for the Bitbucket API. It records what it was
// asked, and answers what the case set.
type contentsServer struct {
	srv  *httptest.Server
	tok  string
	hits atomic.Int64

	status     int
	ctype      string
	body       []byte
	mainBranch string
	redirect   string // when set, every request is answered with a redirect to it

	// seen is every request's method, escaped path and Authorization header,
	// in the order the server received them.
	seen []contentsCall
}

type contentsCall struct{ method, path, auth string }

func newContentsServer(t *testing.T) *contentsServer {
	t.Helper()
	f := &contentsServer{
		tok: "fake-token-not-a-real-credential", status: http.StatusOK,
		ctype: "application/octet-stream", body: []byte("FROM scratch\n"), mainBranch: "main",
	}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		f.seen = append(f.seen, contentsCall{r.Method, r.URL.EscapedPath(), r.Header.Get("Authorization")})
		if f.redirect != "" {
			http.Redirect(w, r, f.redirect, http.StatusFound)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/repositories/acme/web") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"mainbranch":{"name":%q}}`, f.mainBranch)
			return
		}
		w.Header().Set("Content-Type", f.ctype)
		w.WriteHeader(f.status)
		_, _ = w.Write(f.body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *contentsServer) provider(t *testing.T) *Provider {
	t.Helper()
	p, err := New(Options{Workspace: "acme", Slug: "web", Token: f.tok, BaseURL: f.srv.URL + "/2.0", HTTP: f.srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestContentsReadsAFileAtABranch: the one read this adapter adds, asked for
// with the provider's own credential, at the repository's own path.
func TestContentsReadsAFileAtABranch(t *testing.T) {
	f := newContentsServer(t)
	f.body = []byte("FROM node:24\nCOPY . /app\n")
	p := f.provider(t)
	// The read is exercised through the interface the catalog fetches
	// through, so a Provider that stopped answering gitprov.ContentsReader
	// fails here rather than at a caller's type assertion.
	var r gitprov.ContentsReader = p
	data, err := r.Contents(ctx, "docker/agent.Dockerfile", "release-1.0")
	if err != nil {
		t.Fatalf("Contents: %v", err)
	}
	if string(data) != string(f.body) {
		t.Errorf("Contents = %q, want the file's own bytes", data)
	}
	if len(f.seen) != 1 {
		t.Fatalf("the server saw %d requests, want 1", len(f.seen))
	}
	want := "/2.0/repositories/acme/web/src/release-1.0/docker/agent.Dockerfile"
	if f.seen[0].path != want || f.seen[0].method != http.MethodGet {
		t.Errorf("the read went to %s %s, want GET %s", f.seen[0].method, f.seen[0].path, want)
	}
	if f.seen[0].auth != "Bearer "+f.tok {
		t.Errorf("the read presented %q, want the provider's own token", f.seen[0].auth)
	}
}

// TestContentsEscapesEachPathSegmentAndTheRef: a segment's own "?" or "#"
// stays in the file's name, and a branch's "/" stays in the ref rather than
// becoming two path segments.
func TestContentsEscapesEachPathSegmentAndTheRef(t *testing.T) {
	f := newContentsServer(t)
	p := f.provider(t)
	if _, err := p.Contents(ctx, "docker/agent#1.Dockerfile", "release/1.0"); err != nil {
		t.Fatalf("Contents: %v", err)
	}
	want := "/2.0/repositories/acme/web/src/release%2F1.0/docker/agent%231.Dockerfile"
	if len(f.seen) != 1 || f.seen[0].path != want {
		t.Fatalf("the read went to %v, want %s", f.seen, want)
	}
}

// TestContentsResolvesTheDefaultBranchWhenNoRefIsNamed: a source that names
// no branch is read at the repository's own default branch, which Bitbucket
// names in the repository it is asked about -- never at a ref this adapter
// guessed.
func TestContentsResolvesTheDefaultBranchWhenNoRefIsNamed(t *testing.T) {
	f := newContentsServer(t)
	f.mainBranch = "develop"
	p := f.provider(t)
	if _, err := p.Contents(ctx, "Dockerfile", ""); err != nil {
		t.Fatalf("Contents: %v", err)
	}
	if len(f.seen) != 2 {
		t.Fatalf("the server saw %d requests, want the repository read then the file read", len(f.seen))
	}
	if !strings.HasSuffix(f.seen[0].path, "/repositories/acme/web") {
		t.Errorf("the first request was %s, want the repository's own", f.seen[0].path)
	}
	if want := "/2.0/repositories/acme/web/src/develop/Dockerfile"; f.seen[1].path != want {
		t.Errorf("the read went to %s, want %s", f.seen[1].path, want)
	}
}

// TestContentsRefusesARepositoryWithNoDefaultBranch: nothing is read at a ref
// the API did not name.
func TestContentsRefusesARepositoryWithNoDefaultBranch(t *testing.T) {
	f := newContentsServer(t)
	f.mainBranch = ""
	p := f.provider(t)
	if _, err := p.Contents(ctx, "Dockerfile", ""); err == nil {
		t.Fatal("Contents read a file at a repository that names no default branch")
	}
	if f.hits.Load() != 1 {
		t.Errorf("the server saw %d requests, want the repository read alone", f.hits.Load())
	}
}

// TestContentsRefusesAPathOutOfTheRepository is the traversal guard: a path
// that is not one clean relative file path inside the repository is refused
// before any request is built, so neither the API nor a proxy in front of it
// ever resolves a path the caller did not name. Dropping the guard makes the
// request go out and this test fail.
func TestContentsRefusesAPathOutOfTheRepository(t *testing.T) {
	for _, path := range []string{
		"",
		"/etc/agent.Dockerfile",
		"..",
		"../docker/agent.Dockerfile",
		"docker/../../etc/agent.Dockerfile",
		"docker/./agent.Dockerfile",
		"docker//agent.Dockerfile",
		"docker/",
		`docker\..\..\etc\agent.Dockerfile`,
		`..\..\etc\agent.Dockerfile`,
		"docker/agent\x00.Dockerfile",
		"docker/agent.Dockerfile\n",
	} {
		f := newContentsServer(t)
		p := f.provider(t)
		if _, err := p.Contents(ctx, path, "main"); err == nil {
			t.Errorf("Contents(%q) read a file", path)
		}
		if n := f.hits.Load(); n != 0 {
			t.Errorf("Contents(%q): the server saw %d requests, want none", path, n)
		}
	}
}

// TestContentsRefusesARefThatIsNeitherACommitNorABranch: the ref reaches a
// URL path segment, so only the two shapes the catalog can carry -- a commit
// SHA, or a branch name by the git ref grammar -- are sent.
func TestContentsRefusesARefThatIsNeitherACommitNorABranch(t *testing.T) {
	for _, ref := range []string{
		"main..x",
		"a b",
		"-main",
		"main.lock",
		"main~1",
		"main^",
		"main:other",
		"main\n",
		"refs//heads",
	} {
		f := newContentsServer(t)
		p := f.provider(t)
		if _, err := p.Contents(ctx, "Dockerfile", ref); err == nil {
			t.Errorf("Contents at ref %q read a file", ref)
		}
		if n := f.hits.Load(); n != 0 {
			t.Errorf("Contents at ref %q: the server saw %d requests, want none", ref, n)
		}
	}
}

// TestContentsAcceptsACommitAndABranch: the two shapes a source's ref may be.
func TestContentsAcceptsACommitAndABranch(t *testing.T) {
	for _, ref := range []string{
		"main",
		"release/1.0",
		"v1.2.3",
		"0123456789abcdef0123456789abcdef01234567",
	} {
		f := newContentsServer(t)
		p := f.provider(t)
		if _, err := p.Contents(ctx, "Dockerfile", ref); err != nil {
			t.Errorf("Contents at ref %q: %v", ref, err)
		}
	}
}

// TestContentsRefusesADirectory: Bitbucket answers a directory with its
// listing, and a listing is not a Dockerfile. What a caller gets is a file's
// bytes or an error.
func TestContentsRefusesADirectory(t *testing.T) {
	f := newContentsServer(t)
	f.ctype = "application/json"
	f.body = []byte(`{"values":[{"path":"docker/agent.Dockerfile","type":"commit_file"}]}`)
	p := f.provider(t)
	data, err := p.Contents(ctx, "docker", "main")
	if err == nil {
		t.Fatalf("Contents returned a directory listing: %q", data)
	}
}

// TestContentsIsBounded: gitprov.MaxContentsBytes is this read's ceiling too,
// and a reply over it is refused rather than carried into a build.
func TestContentsIsBounded(t *testing.T) {
	f := newContentsServer(t)
	f.body = make([]byte, gitprov.MaxContentsBytes+1)
	p := f.provider(t)
	if _, err := p.Contents(ctx, "Dockerfile", "main"); err == nil {
		t.Fatal("Contents returned more than gitprov.MaxContentsBytes")
	}
}

// TestContentsRefusesAnEmptyFile: a definition of no bytes is not one.
func TestContentsRefusesAnEmptyFile(t *testing.T) {
	f := newContentsServer(t)
	f.body = nil
	p := f.provider(t)
	if _, err := p.Contents(ctx, "Dockerfile", "main"); err == nil {
		t.Fatal("Contents returned an empty file")
	}
}

// TestContentsRefusesARedirectOffTheAPIRoot: the token is this provider's,
// and a redirect is the one way a request would take it to a host the run
// never named. Following the redirect makes this test fail on the second
// server's own count.
func TestContentsRefusesARedirectOffTheAPIRoot(t *testing.T) {
	var elsewhere atomic.Int64
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhere.Add(1)
		_, _ = w.Write([]byte("FROM evil\n"))
	}))
	t.Cleanup(other.Close)
	f := newContentsServer(t)
	f.redirect = other.URL + "/2.0/repositories/acme/web/src/main/Dockerfile"
	p := f.provider(t)
	data, err := p.Contents(ctx, "Dockerfile", "main")
	if err == nil {
		t.Fatalf("Contents followed a redirect off the API root: %q", data)
	}
	if n := elsewhere.Load(); n != 0 {
		t.Errorf("the host the redirect named was asked %d times, want none", n)
	}
}

// TestContentsRedactsTheTokenFromAnError: an answer that echoes the request's
// own credential back -- a proxy's page, a hostile or broken API -- must not
// carry it into the error a caller logs.
func TestContentsRedactsTheTokenFromAnError(t *testing.T) {
	f := newContentsServer(t)
	f.status = http.StatusForbidden
	f.ctype = "application/json"
	f.body = []byte(`{"error":{"message":"the request's Authorization: Bearer ` + f.tok + ` was rejected"}}`)
	p := f.provider(t)
	_, err := p.Contents(ctx, "Dockerfile", "main")
	if err == nil {
		t.Fatal("Contents accepted a 403")
	}
	if strings.Contains(err.Error(), f.tok) {
		t.Errorf("the error carries the token: %v", err)
	}
	if !strings.Contains(err.Error(), "HTTP 403") {
		t.Errorf("the error does not say what happened: %v", err)
	}
}

// TestContentsRefusesATokenTheAPIDidNotAccept: a refusal is a refusal, not a
// file read.
func TestContentsRefusesATokenTheAPIDidNotAccept(t *testing.T) {
	f := newContentsServer(t)
	f.status = http.StatusUnauthorized
	p := f.provider(t)
	if _, err := p.Contents(ctx, "Dockerfile", "main"); err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("Contents = %v, want an HTTP 401 refusal", err)
	}
}

// TestContentsUsesTokenSource: the read goes through the provider's token
// source like every other call here, so a minted token is used and never a
// static one that was also passed.
func TestContentsUsesTokenSource(t *testing.T) {
	f := newContentsServer(t)
	minted := "fake-minted-token-not-a-real-credential"
	p, err := New(Options{Workspace: "acme", Slug: "web", Token: "fake-static-token-not-a-real-credential",
		TokenSource: fixedTokenSource{token: minted}, BaseURL: f.srv.URL + "/2.0", HTTP: f.srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Contents(context.Background(), "Dockerfile", "main"); err != nil {
		t.Fatalf("Contents: %v", err)
	}
	if len(f.seen) != 1 || f.seen[0].auth != "Bearer "+minted {
		t.Fatalf("the read presented %v, want the source's own token", f.seen)
	}
}
