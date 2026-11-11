package bitbucket

// The commit and tree reads the catalog's copy is made of (D1394, D1395): the
// ref resolved to one commit, and the directory under a context path read at
// it. The fixtures are built at run time, and no literal here is, or looks
// like, a credential.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov"
)

// commitsReply is one answer this server gives: its status, its content type
// and its body, with the headers a listing's paging needs.
type commitsReply struct {
	status int
	ctype  string
	body   string
	header map[string]string
}

// commitsServer answers the endpoints these reads use, keyed by the escaped
// path and query the adapter asks for, and records every request it received
// so a test can say which reads a refusal did not make.
type commitsServer struct {
	srv    *httptest.Server
	tok    string
	routes map[string]commitsReply
	seen   []string
}

func newCommitsServer(t *testing.T) *commitsServer {
	t.Helper()
	f := &commitsServer{tok: "fake-token-not-a-real-credential", routes: map[string]commitsReply{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		at := r.URL.EscapedPath()
		if r.URL.RawQuery != "" {
			at += "?" + r.URL.RawQuery
		}
		f.seen = append(f.seen, at)
		reply, ok := f.routes[at]
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"error": "no route"}`, http.StatusNotFound)
			return
		}
		for k, v := range reply.header {
			w.Header().Set(k, strings.ReplaceAll(v, "{{server}}", f.srv.URL))
		}
		if reply.ctype != "" {
			w.Header().Set("Content-Type", reply.ctype)
		}
		w.WriteHeader(reply.status)
		_, _ = w.Write([]byte(reply.body))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *commitsServer) provider(t *testing.T) *Provider {
	t.Helper()
	p, err := New(Options{Workspace: "acme", Slug: "web", Token: f.tok, BaseURL: f.srv.URL + "/2.0", HTTP: f.srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// asked reports whether the server was asked for at, so a test can assert a
// refusal happened before a read rather than after one.
func (f *commitsServer) asked(at string) bool {
	for _, s := range f.seen {
		if s == at {
			return true
		}
	}
	return false
}

// commitSHA is the commit these reads resolve to: forty lower-hex characters,
// the shape gitops.ValidCommitSHA holds a value to.
const commitSHA = "3f2b1c0d9e8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c"

// srcBase is the src endpoint's path for this repository.
const srcBase = "/2.0/repositories/acme/web/src/" + commitSHA + "/"

// TestCommitResolvesABranchAndTheDefaultBranch: the commit a branch names, and
// the repository's own default branch when the source names none — asked for,
// because this API reads a revision and has no HEAD.
func TestCommitResolvesABranchAndTheDefaultBranch(t *testing.T) {
	f := newCommitsServer(t)
	f.routes["/2.0/repositories/acme/web/commit/release-1.0"] = commitsReply{status: 200, ctype: "application/json", body: `{"hash": "` + commitSHA + `"}`}
	f.routes["/2.0/repositories/acme/web/commit/main"] = commitsReply{status: 200, ctype: "application/json", body: `{"hash": "` + commitSHA + `"}`}
	f.routes["/2.0/repositories/acme/web"] = commitsReply{status: 200, ctype: "application/json", body: `{"mainbranch": {"name": "main"}}`}
	p := f.provider(t)
	got, err := p.Commit(ctx, "release-1.0")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got != commitSHA {
		t.Fatalf("Commit = %q, want %q", got, commitSHA)
	}
	if got, err = p.Commit(ctx, ""); err != nil || got != commitSHA {
		t.Fatalf("Commit(\"\") = %q, %v", got, err)
	}
}

// TestTreeReadsTheFilesUnderThePathAtTheCommit: the directory's files, at the
// commit, each read from the src endpoint — with the paths relative to the
// directory that was read, the entries that are not files left out, and the
// repository's other files left out with them.
//
// The listing here holds a file two levels down, under a subdirectory, because
// a context's subdirectories are part of it: that file is in the listing only
// because the listing was asked to recurse to it (srcMaxDepth), and without
// that depth it would be missing from the read — and so from the digest the
// copy stages (D1401) and from the ceilings it applies (D1398).
func TestTreeReadsTheFilesUnderThePathAtTheCommit(t *testing.T) {
	def := "FROM example.invalid/base\n"
	src := "package main\n"
	deep := "package deep\n"
	f := newCommitsServer(t)
	f.routes[srcBase+"ci/?pagelen=100&max_depth=100"] = commitsReply{status: 200, ctype: "application/json", body: fmt.Sprintf(
		`{"pagelen": 100, "values": [{"path": "ci/Dockerfile", "type": "commit_file", "size": %d}, {"path": "ci/src", "type": "commit_directory"}, {"path": "ci/src/main.go", "type": "commit_file", "size": %d}, {"path": "ci/src/pkg", "type": "commit_directory"}, {"path": "ci/src/pkg/deep.go", "type": "commit_file", "size": %d}, {"path": "web/index.html", "type": "commit_file", "size": 5}]}`,
		len(def), len(src), len(deep))}
	f.routes[srcBase+"ci/Dockerfile"] = commitsReply{status: 200, ctype: "application/octet-stream", body: def}
	f.routes[srcBase+"ci/src/main.go"] = commitsReply{status: 200, ctype: "application/octet-stream", body: src}
	f.routes[srcBase+"ci/src/pkg/deep.go"] = commitsReply{status: 200, ctype: "application/octet-stream", body: deep}
	p := f.provider(t)
	files, err := p.Tree(ctx, "ci", commitSHA, gitprov.TreeLimit{MaxFiles: 10, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	want := []gitprov.TreeFile{{Path: "Dockerfile", Data: []byte(def)}, {Path: "src/main.go", Data: []byte(src)}, {Path: "src/pkg/deep.go", Data: []byte(deep)}}
	if len(files) != len(want) {
		t.Fatalf("Tree = %d files (%v), want %d", len(files), files, len(want))
	}
	for i, g := range files {
		if g.Path != want[i].Path || string(g.Data) != string(want[i].Data) {
			t.Errorf("Tree[%d] = %q %q, want %q %q", i, g.Path, g.Data, want[i].Path, want[i].Data)
		}
	}
}

// TestTreeAsksTheListingForItsDepthAndPageLength: the listing URL is the
// request that decides which files a context is made of, so both of its
// parameters are written out here rather than left to the API's own defaults —
// this API's src listing answers one directory's own entries unless a depth is
// asked for, and the subdirectories of a context are part of the context.
func TestTreeAsksTheListingForItsDepthAndPageLength(t *testing.T) {
	p := newCommitsServer(t).provider(t)
	repo := "/repositories/acme/web/src/" + commitSHA + "/"
	for _, tc := range []struct{ path, want string }{
		{"", repo + "?pagelen=100&max_depth=100"},
		{"ci", repo + "ci/?pagelen=100&max_depth=100"},
		{"ci/", repo + "ci/?pagelen=100&max_depth=100"},
		{"ci/src/pkg", repo + "ci/src/pkg/?pagelen=100&max_depth=100"},
	} {
		if got := p.srcListingURL(tc.path, commitSHA); got != tc.want {
			t.Errorf("srcListingURL(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// TestTreeWalksEveryPageOfAListing: a context larger than one page is read
// whole — the next page's entries are read with it, and the file of an entry
// the first page did not carry is read too.
func TestTreeWalksEveryPageOfAListing(t *testing.T) {
	one := "one\n"
	two := "two\n"
	f := newCommitsServer(t)
	f.routes[srcBase+"ci/?pagelen=100&max_depth=100"] = commitsReply{status: 200, ctype: "application/json",
		body:   fmt.Sprintf(`{"values": [{"path": "ci/a", "type": "commit_file", "size": %d}]}`, len(one)),
		header: map[string]string{"Link": "<{{server}}/2.0/repositories/acme/web/src/" + commitSHA + "/ci/?pagelen=100&max_depth=100&page=2>; rel=\"next\""}}
	f.routes[srcBase+"ci/?pagelen=100&max_depth=100&page=2"] = commitsReply{status: 200, ctype: "application/json",
		body: fmt.Sprintf(`{"values": [{"path": "ci/b", "type": "commit_file", "size": %d}]}`, len(two))}
	f.routes[srcBase+"ci/a"] = commitsReply{status: 200, ctype: "application/octet-stream", body: one}
	f.routes[srcBase+"ci/b"] = commitsReply{status: 200, ctype: "application/octet-stream", body: two}
	p := f.provider(t)
	files, err := p.Tree(ctx, "ci", commitSHA, gitprov.TreeLimit{MaxFiles: 10, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatalf("Tree: %v", err)
	}
	if len(files) != 2 || files[0].Path != "a" || files[1].Path != "b" {
		t.Fatalf("Tree = %v, want a and b", files)
	}
}

// TestTreeReadsADirectoryWrittenWithItsTrailingSlash: a context names a
// directory, and the catalog's grammar takes one written the way the key's own
// example spells it — "ci/" (D1391) — which is the same directory the same
// entry written "ci" names: the read answers the files under it, relative to
// it, and asks the API for it once.
func TestTreeReadsADirectoryWrittenWithItsTrailingSlash(t *testing.T) {
	def := "FROM example.invalid/base\n"
	f := newCommitsServer(t)
	f.routes[srcBase+"ci/?pagelen=100&max_depth=100"] = commitsReply{status: 200, ctype: "application/json",
		body: fmt.Sprintf(`{"values": [{"path": "ci/Dockerfile", "type": "commit_file", "size": %d}]}`, len(def))}
	f.routes[srcBase+"ci/Dockerfile"] = commitsReply{status: 200, ctype: "application/octet-stream", body: def}
	p := f.provider(t)
	files, err := p.Tree(ctx, "ci/", commitSHA, gitprov.TreeLimit{MaxFiles: 10, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatalf("Tree of a context written with its trailing slash: %v", err)
	}
	if len(files) != 1 || files[0].Path != "Dockerfile" || string(files[0].Data) != def {
		t.Fatalf("Tree = %v, want the one file under ci, named relative to it", files)
	}
}

// TestTreeStopsTheWalkOnThePageThatIsOverItsLimit: the caller's ceiling is
// applied as the entries arrive, not after the walk, so a directory whose
// second page takes it over is refused there — the third page is never asked
// for and no file of it is read, which is what the server's record of the
// requests shows.
func TestTreeStopsTheWalkOnThePageThatIsOverItsLimit(t *testing.T) {
	one := "one\n"
	page := func(n int, link bool, name string) commitsReply {
		r := commitsReply{status: 200, ctype: "application/json",
			body: fmt.Sprintf(`{"values": [{"path": "ci/%s", "type": "commit_file", "size": %d}]}`, name, len(one))}
		if link {
			r.header = map[string]string{"Link": fmt.Sprintf("<{{server}}%sci/?pagelen=100&max_depth=100&page=%d>; rel=\"next\"", srcBase, n+1)}
		}
		return r
	}
	f := newCommitsServer(t)
	f.routes[srcBase+"ci/?pagelen=100&max_depth=100"] = page(1, true, "a")
	f.routes[srcBase+"ci/?pagelen=100&max_depth=100&page=2"] = page(2, true, "b")
	f.routes[srcBase+"ci/?pagelen=100&max_depth=100&page=3"] = page(3, false, "c")
	for _, name := range []string{"a", "b", "c"} {
		f.routes[srcBase+"ci/"+name] = commitsReply{status: 200, ctype: "application/octet-stream", body: one}
	}
	p := f.provider(t)
	_, err := p.Tree(ctx, "ci", commitSHA, gitprov.TreeLimit{MaxFiles: 1, MaxBytes: 1 << 20})
	if err == nil || !strings.Contains(err.Error(), gitprov.ErrTreeTooLarge.Error()) {
		t.Fatalf("Tree over its file limit = %v, want %v", err, gitprov.ErrTreeTooLarge)
	}
	if f.asked(srcBase + "ci/?pagelen=100&max_depth=100&page=3") {
		t.Error("the walk went past the page that took it over the ceiling")
	}
	for _, name := range []string{"a", "b", "c"} {
		if f.asked(srcBase + "ci/" + name) {
			t.Errorf("the file %s was read for a directory that is over the ceiling", name)
		}
	}
}

// TestTreeRefusesAListingThatNeverEnds: the caller's ceiling is not what stops
// every listing — an API that keeps answering with a next page would walk for
// ever, and a context whose every entry is a directory counts against no file
// ceiling at all — so the walk has a page count of its own. The page past that
// bound is never asked for, and the page at it was, which is what the server's
// record of the requests shows.
func TestTreeRefusesAListingThatNeverEnds(t *testing.T) {
	f := newCommitsServer(t)
	page := func(n int) commitsReply {
		return commitsReply{status: 200, ctype: "application/json",
			body:   fmt.Sprintf(`{"values": [{"path": "ci/dir%d", "type": "commit_directory"}]}`, n),
			header: map[string]string{"Link": fmt.Sprintf("<{{server}}%sci/?pagelen=100&max_depth=100&page=%d>; rel=\"next\"", srcBase, n+1)}}
	}
	at := func(n int) string {
		if n == 1 {
			return srcBase + "ci/?pagelen=100&max_depth=100"
		}
		return fmt.Sprintf("%sci/?pagelen=100&max_depth=100&page=%d", srcBase, n)
	}
	for n := 1; n <= maxSrcPages; n++ {
		f.routes[at(n)] = page(n)
	}

	p := f.provider(t)
	_, err := p.Tree(ctx, "ci", commitSHA, gitprov.TreeLimit{MaxFiles: 10, MaxBytes: 1 << 20})
	if err == nil {
		t.Fatal("a listing that never ends was walked to its end")
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("the %d pages this read walks", maxSrcPages)) {
		t.Fatalf("Tree error = %v, want the walk's own bound", err)
	}
	if !f.asked(at(maxSrcPages)) {
		t.Error("the walk stopped before the page its bound allows")
	}
	if f.asked(at(maxSrcPages + 1)) {
		t.Error("the walk asked for the page past its bound")
	}
}

// TestTreeRefusesADirectoryOverItsLimit: a directory over the byte ceiling is
// refused as soon as the listing says so — the file that took it over is never
// read, which is what the server's record of the requests shows.
func TestTreeRefusesADirectoryOverItsLimit(t *testing.T) {
	first := "one\n"
	second := "two\n"
	f := newCommitsServer(t)
	f.routes[srcBase+"ci/?pagelen=100&max_depth=100"] = commitsReply{status: 200, ctype: "application/json", body: fmt.Sprintf(
		`{"values": [{"path": "ci/a", "type": "commit_file", "size": %d}, {"path": "ci/b", "type": "commit_file", "size": %d}]}`,
		len(first), len(second))}
	f.routes[srcBase+"ci/a"] = commitsReply{status: 200, ctype: "application/octet-stream", body: first}
	p := f.provider(t)
	_, err := p.Tree(ctx, "ci", commitSHA, gitprov.TreeLimit{MaxFiles: 10, MaxBytes: int64(len(first) + len(second) - 1)})
	if err == nil || !strings.Contains(err.Error(), gitprov.ErrTreeTooLarge.Error()) {
		t.Fatalf("Tree over its byte limit = %v, want %v", err, gitprov.ErrTreeTooLarge)
	}
	if f.asked(srcBase + "ci/b") {
		t.Error("the file over the limit was read")
	}
}

// TestTreeRefusesBytesTheListingUnderstated: the ceiling is about the bytes
// that arrive, and the listing's sizes are only what they were checked
// against — this API's listing is read before the files are, so a host that
// understates one, or a file that changed between the two, would carry the
// tree over the caller's ceiling past the check the listing passed. The file
// read is bounded by what the ceiling has left, so what arrives is over it by
// the one byte that tells the caller the file is larger, and the walk refuses
// there rather than answering a tree of files cut short: the listing says four
// bytes, the file is a hundred, and only the read can tell.
func TestTreeRefusesBytesTheListingUnderstated(t *testing.T) {
	f := newCommitsServer(t)
	f.routes[srcBase+"ci/?pagelen=100&max_depth=100"] = commitsReply{status: 200, ctype: "application/json",
		body: `{"values": [{"path": "ci/a", "type": "commit_file", "size": 4}]}`}
	f.routes[srcBase+"ci/a"] = commitsReply{status: 200, ctype: "application/octet-stream", body: strings.Repeat("x", 100)}
	p := f.provider(t)
	files, err := p.Tree(ctx, "ci", commitSHA, gitprov.TreeLimit{MaxFiles: 10, MaxBytes: 10})
	if err == nil {
		t.Fatalf("Tree = %v, want the tree over its ceiling refused once the bytes arrived", files)
	}
	if !strings.Contains(err.Error(), gitprov.ErrTreeTooLarge.Error()) {
		t.Errorf("Tree error = %v, want it to carry %v", err, gitprov.ErrTreeTooLarge)
	}
}

// TestTreeRefusesMoreFilesThanItsLimit: the same, by count.
func TestTreeRefusesMoreFilesThanItsLimit(t *testing.T) {
	one := "one\n"
	f := newCommitsServer(t)
	f.routes[srcBase+"ci/?pagelen=100&max_depth=100"] = commitsReply{status: 200, ctype: "application/json", body: fmt.Sprintf(
		`{"values": [{"path": "ci/a", "type": "commit_file", "size": %d}, {"path": "ci/b", "type": "commit_file", "size": %d}]}`,
		len(one), len(one))}
	f.routes[srcBase+"ci/a"] = commitsReply{status: 200, ctype: "application/octet-stream", body: one}
	p := f.provider(t)
	_, err := p.Tree(ctx, "ci", commitSHA, gitprov.TreeLimit{MaxFiles: 1, MaxBytes: 1 << 20})
	if err == nil || !strings.Contains(err.Error(), gitprov.ErrTreeTooLarge.Error()) {
		t.Fatalf("Tree over its file limit = %v, want %v", err, gitprov.ErrTreeTooLarge)
	}
}

// TestTreeRefusesAPathOutsideTheRepository: a path this read must not carry
// into an API path is refused before any request — this server holds no route
// for one, and the record of the requests is what says none was made.
func TestTreeRefusesAPathOutsideTheRepository(t *testing.T) {
	f := newCommitsServer(t)
	p := f.provider(t)
	for _, path := range []string{"../etc", "/etc", "ci/../.."} {
		if files, err := p.Tree(ctx, path, commitSHA, gitprov.TreeLimit{MaxFiles: 10, MaxBytes: 1 << 20}); err == nil {
			t.Errorf("Tree(%q) = %d files, want an error", path, len(files))
		}
	}
	if len(f.seen) != 0 {
		t.Errorf("the server was asked for %v, want no request", f.seen)
	}
}
