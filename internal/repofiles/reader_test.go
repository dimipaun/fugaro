package repofiles

// The reads `fugaro update` makes of the repositories it can list, against
// each provider the repository supports: the GitHub adapter replayed from its
// own fixture (httpfixture), the Bitbucket adapter against the routes its src
// endpoint answers, and the file-backed provider. No literal here is, or looks
// like, a credential.

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/bitbucket"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/gitprov/httpfixture"
	"github.com/dimipaun/fugaro/internal/recipe"
)

// opens is the seam the reader reads through: the provider a test pinned for
// one repository, and a failed open for any other.
func opens(m map[string]gitprov.Provider) gitprov.Opener {
	return func(_ context.Context, kind, repo string) (gitprov.Provider, []string, error) {
		p, ok := m[kind+"/"+repo]
		if !ok {
			return nil, nil, fmt.Errorf("no provider for %s/%s in this test", kind, repo)
		}
		return p, nil, nil
	}
}

// host is a git host this build knows nothing about: it is a provider and no
// reader at all, which is the host U3 asks about — neither a file read nor a
// directory read. It answers nothing, because the reader must name it before
// it calls anything.
type host struct{ gitprov.Provider }

// fileHost is the same, with a file read: it can answer one file and has no
// directory read and no ref read.
type fileHost struct {
	gitprov.Provider
	gitprov.ContentsReader
}

// TestReadEveryListedRepo: every repository the reader can name is read — the
// local config's, and the canonical names the published admits add — each
// one's default-branch fugaro.yaml and its recipes folder, from the provider
// that repository is on.
//
// Both fixtures' folders hold a symbolic link beside a recipe (U3, R12):
// TreeFile carries no mode, so each adapter's read answers the link as a file
// whose bytes are the target's path — GitHub's listing says so with the git
// mode 120000, and Bitbucket's src listing has no type of its own for a link —
// and the recipe's own parse is what judges it. One Bitbucket repository keeps
// no recipes folder at all, which its src listing answers as 404: that is the
// ordinary repository, not one nobody could read.
func TestReadEveryListedRepo(t *testing.T) {
	ctx := context.Background()
	appYAML := "agent:\n  model: claude-x\n"
	app := githubFixture(t, []byte(appYAML), []ghEntry{
		{Path: "review.yaml", Mode: "100644", Data: "name: review\n"},
		{Path: "link.yaml", Mode: "120000", Data: "review.yaml"},
	})
	webYAML := "ab:\n  rate: 0\n"
	webSrv := newBitServer(t, "acme/web")
	webSrv.repo([]byte(webYAML), nil)
	webSrv.noFolder()
	apiYAML := "rigor:\n  default: startup\n"
	apiSrv := newBitServer(t, "acme/api")
	apiSrv.repo([]byte(apiYAML), []ghEntry{
		{Path: "review.yaml", Data: "name: review\n"},
		{Path: "nested/poc.yaml", Data: "name: poc\n"},
		// A symbolic link, which the src endpoint reports the way its listing
		// can: a commit_file whose bytes are the target's path. The adapter's
		// srcEntry reads path/type/size and drops directories, so the link
		// reaches the reader as a file — the other half of U3 is GitHub's mode
		// 120000 entry above.
		{Path: "link.yaml", Data: "review.yaml"},
	})
	extra := &fake.Provider{Repo: "acme/extra", State: fake.State{Files: map[string]string{
		YAML:                         "ab:\n  rate: 5\n",
		recipe.RepoDir + "/poc.yaml": "name: poc\n",
	}}}
	got, patterns, err := Read(ctx, Input{
		Repos: []Listed{
			{Repo: "acme/app", Provider: "github"},
			{Repo: "acme/web", Provider: "bitbucket"},
			{Repo: "acme/api", Provider: "bitbucket"},
		},
		Admits: []string{"github/acme/extra", "github/acme/*", "*", "github/acme/extra"},
	}, opens(map[string]gitprov.Provider{
		"github/acme/app":    app,
		"bitbucket/acme/web": webSrv.provider(t),
		"bitbucket/acme/api": apiSrv.provider(t),
		"github/acme/extra":  extra,
	}))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if want := []string{"github/acme/*", "*"}; !slices.Equal(patterns, want) {
		t.Errorf("unexpanded patterns = %q, want %q", patterns, want)
	}
	// Every repository is answered once, in the order it was listed — the
	// config's three, then the one the admits add.
	want := []string{"acme/app", "acme/web", "acme/api", "acme/extra"}
	if !slices.Equal(names(got), want) {
		t.Fatalf("repositories = %q, want %q", names(got), want)
	}
	at := map[string]Repo{}
	for _, r := range got {
		if r.Unread != "" {
			t.Errorf("%s: Unread = %q, want it read", r.Repo, r.Unread)
		}
		at[r.Repo] = r
	}
	if a := at["acme/app"]; string(a.YAML) != appYAML || a.Provider != "github" {
		t.Errorf("acme/app = provider %q, fugaro.yaml %q", a.Provider, a.YAML)
	}
	if want := []File{{Path: "link.yaml", Data: []byte("review.yaml")}, {Path: "review.yaml", Data: []byte("name: review\n")}}; !slices.EqualFunc(at["acme/app"].Recipes, want, sameFile) {
		t.Errorf("acme/app recipes = %v, want %v", at["acme/app"].Recipes, want)
	}
	if w := at["acme/web"]; string(w.YAML) != webYAML || len(w.Recipes) != 0 || w.Provider != "bitbucket" {
		t.Errorf("acme/web = provider %q, fugaro.yaml %q, %d recipes; want its file and no recipes", w.Provider, w.YAML, len(w.Recipes))
	}
	// The folder was asked for, at the commit the default branch resolved to —
	// a 404 from a listing that was never made would read the same.
	if asked := webSrv.asked(); !slices.Contains(asked, webSrv.listing()) {
		t.Errorf("acme/web asked for %q, want the recipes folder at the resolved commit", asked)
	}
	// A file the listing names under a subdirectory is carried at its path
	// inside the folder, the way the adapter reports it.
	if a := at["acme/api"]; string(a.YAML) != apiYAML {
		t.Errorf("acme/api fugaro.yaml = %q, want %q", a.YAML, apiYAML)
	}
	if want := []File{{Path: "link.yaml", Data: []byte("review.yaml")}, {Path: "nested/poc.yaml", Data: []byte("name: poc\n")}, {Path: "review.yaml", Data: []byte("name: review\n")}}; !slices.EqualFunc(at["acme/api"].Recipes, want, sameFile) {
		t.Errorf("acme/api recipes = %v, want %v", at["acme/api"].Recipes, want)
	}
	// The canonical admit adds the repository, with the kind the admit is
	// qualified by, since the local config does not hold it at all.
	if e := at["acme/extra"]; e.Provider != "github" || string(e.YAML) != "ab:\n  rate: 5\n" {
		t.Errorf("acme/extra = provider %q, fugaro.yaml %q", e.Provider, e.YAML)
	}
	if want := []File{{Path: "poc.yaml", Data: []byte("name: poc\n")}}; !slices.EqualFunc(at["acme/extra"].Recipes, want, sameFile) {
		t.Errorf("acme/extra recipes = %v, want %v", at["acme/extra"].Recipes, want)
	}
}

// TestReadCarriesAFolderOverOneRecipesBytes: a tree read's ceiling is the
// folder's, not one file's, so a repository whose recipes are ordinary in size
// but add up to more than one recipe may hold is read like any other — its
// fugaro.yaml with it. A ceiling of one recipe's own bound would answer the
// whole repository unread, which D3306 leaves unmigrated: it would keep its
// ab.rate opt-out and its rigor, and its recipes would go unrenamed (R12).
func TestReadCarriesAFolderOverOneRecipesBytes(t *testing.T) {
	ctx := context.Background()
	// Each recipe is under recipe.MaxBytes; the two together are over it.
	one, two := strings.Repeat("a", recipe.MaxBytes-1), strings.Repeat("b", recipe.MaxBytes-1)
	if int64(len(one)+len(two)) <= recipe.MaxBytes {
		t.Fatalf("the fixture holds %d bytes, want more than one recipe's %d", len(one)+len(two), recipe.MaxBytes)
	}
	app := githubFixture(t, []byte("ab:\n  rate: 0\n"), []ghEntry{
		{Path: "one.yaml", Mode: "100644", Data: one},
		{Path: "two.yaml", Mode: "100644", Data: two},
	})
	got, _, err := Read(ctx, Input{Repos: []Listed{{Repo: "acme/app", Provider: "github"}}},
		opens(map[string]gitprov.Provider{"github/acme/app": app}))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Read answered %d repositories, want 1", len(got))
	}
	r := got[0]
	if r.Unread != "" {
		t.Fatalf("acme/app: Unread = %q, want a repository whose recipes add up to more than one recipe", r.Unread)
	}
	if string(r.YAML) != "ab:\n  rate: 0\n" {
		t.Errorf("acme/app fugaro.yaml = %q, want it carried", r.YAML)
	}
	want := []File{{Path: "one.yaml", Data: []byte(one)}, {Path: "two.yaml", Data: []byte(two)}}
	if !slices.EqualFunc(r.Recipes, want, sameFile) {
		t.Errorf("acme/app recipes = %d files, want both", len(r.Recipes))
	}
}

// TestReadNamesAnUnreadableRepo: a repository the reader cannot read is one
// line naming why, and never a read that answers half a repository — the
// migration moves nothing for it (D3306). Every case here is a way a read can
// fail that is not the repository's own shape.
func TestReadNamesAnUnreadableRepo(t *testing.T) {
	ctx := context.Background()
	read := func(t *testing.T, listed Listed, m map[string]gitprov.Provider) Repo {
		t.Helper()
		got, _, err := Read(ctx, Input{Repos: []Listed{listed}}, opens(m))
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("Read answered %d repositories, want 1", len(got))
		}
		return got[0]
	}
	for _, c := range []struct {
		name   string
		listed Listed
		m      map[string]gitprov.Provider
		want   []string
	}{
		{
			// U3's host with neither interface: a provider and no reader at
			// all is named before anything is called on it.
			name:   "a host with neither interface",
			listed: Listed{Repo: "acme/app", Provider: "github"},
			m:      map[string]gitprov.Provider{"github/acme/app": &host{}},
			want:   []string{"the github provider cannot read a file out of a repository"},
		},
		{
			name:   "a host with no directory read",
			listed: Listed{Repo: "acme/app", Provider: "github"},
			m:      map[string]gitprov.Provider{"github/acme/app": &fileHost{}},
			want:   []string{"the github provider cannot read a directory out of a repository"},
		},
		{
			name:   "a repository this machine cannot place",
			listed: Listed{Repo: "acme/app"},
			want:   []string{"no git provider is known for it"},
		},
		{
			name:   "a provider that cannot be opened",
			listed: Listed{Repo: "acme/app", Provider: "github"},
			m:      map[string]gitprov.Provider{},
			want:   []string{"its provider could not be opened", "no provider for github/acme/app"},
		},
		{
			name:   "a repository the host does not answer for",
			listed: Listed{Repo: "acme/app", Provider: "github"},
			m: map[string]gitprov.Provider{"github/acme/app": &fake.Provider{
				Repo: "acme/app", FailRepository: 1,
				State: fake.State{Files: map[string]string{YAML: "agent: {}\n"}},
			}},
			want: []string{"the repository could not be read"},
		},
		{
			name:   "a recipes folder over the read's ceiling",
			listed: Listed{Repo: "acme/app", Provider: "github"},
			m: map[string]gitprov.Provider{"github/acme/app": &fake.Provider{
				Repo: "acme/app",
				State: fake.State{Files: map[string]string{
					YAML: "agent: {}\n",
					// The ceiling is the folder's, so one folder over it is the
					// one whose files together hold more than a folder may
					// (MaxRecipeFiles recipes' worth).
					recipe.RepoDir + "/huge.yaml": strings.Repeat("x", MaxRecipeFiles*recipe.MaxBytes+1),
				}},
			}},
			want: []string{"its .fugaro/recipes/ could not be read", "larger than a tree read returns"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := read(t, c.listed, c.m)
			if r.Unread == "" {
				t.Fatalf("%s: read, want the reason it could not be", c.listed.Repo)
			}
			if len(r.YAML) != 0 || len(r.Recipes) != 0 {
				t.Errorf("%s: Unread %q carries %d yaml bytes and %d recipes; an unread repository carries no files", c.listed.Repo, r.Unread, len(r.YAML), len(r.Recipes))
			}
			for _, want := range c.want {
				if !strings.Contains(r.Unread, want) {
					t.Errorf("%s: Unread = %q, want it to name %q", c.listed.Repo, r.Unread, want)
				}
			}
		})
	}

	// A host that refuses the file is named too, and its refusal is not read as
	// the folder's absence: the same fixture answers 404 for the folder (the
	// ordinary repository) and 500 for the file.
	t.Run("a fugaro.yaml the host refuses", func(t *testing.T) {
		f := newBitServer(t, "acme/app")
		f.repo([]byte("agent: {}\n"), nil)
		f.fail(f.yaml(), http.StatusInternalServerError, `{"error": {"message": "boom"}}`)
		r := read(t, Listed{Repo: "acme/app", Provider: "bitbucket"}, map[string]gitprov.Provider{"bitbucket/acme/app": f.provider(t)})
		if !strings.Contains(r.Unread, "its fugaro.yaml could not be read") || !strings.Contains(r.Unread, "500") {
			t.Errorf("Unread = %q, want the refused file named", r.Unread)
		}
	})
}

// TestReadHoldsNoYAML: a repository whose default branch holds no fugaro.yaml
// overrides nothing, which is not a repository nobody could read — the
// distinction that separates "nothing to migrate" from "could not read"
// (D3306), and one the two adapters answer differently: the contents API's own
// 404 is typed, while Bitbucket's src read carries its status as text.
//
// Its recipes are read all the same, which is the point of the distinction: a
// repository that writes no fugaro.yaml still sends the recipes it keeps
// (D3301), and one left unmigrated keeps theirs under names R16's gate refuses
// with a `fugaro update` that could never clear them.
func TestReadHoldsNoYAML(t *testing.T) {
	ctx := context.Background()
	recipeFile := []ghEntry{{Path: "poc.yaml", Data: "name: poc\n"}}
	app := githubFixture(t, nil, recipeFile)
	web := newBitServer(t, "acme/web")
	web.repo(nil, recipeFile)
	got, _, err := Read(ctx, Input{Repos: []Listed{
		{Repo: "acme/app", Provider: "github"},
		{Repo: "acme/web", Provider: "bitbucket"},
	}}, opens(map[string]gitprov.Provider{
		"github/acme/app":    app,
		"bitbucket/acme/web": web.provider(t),
	}))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Read answered %d repositories, want 2", len(got))
	}
	// Every file the folder holds is the point of the read, so its exchange is
	// asserted to have happened: a reader that skipped the folder would answer
	// the same way with no listing at all.
	for _, r := range got {
		if r.Unread != "" {
			t.Errorf("%s: Unread = %q, want a repository that overrides nothing", r.Repo, r.Unread)
		}
		if r.YAML != nil {
			t.Errorf("%s: fugaro.yaml = %q, want none", r.Repo, r.YAML)
		}
		if want := []File{{Path: "poc.yaml", Data: []byte("name: poc\n")}}; !slices.EqualFunc(r.Recipes, want, sameFile) {
			t.Errorf("%s recipes = %v, want %v", r.Repo, r.Recipes, want)
		}
	}
	if asked := web.asked(); !slices.Contains(asked, web.listing()) {
		t.Errorf("acme/web asked for %q, want the recipes folder listed", asked)
	}
}

// TestReadNamesAPatternItCannotList: an admit that is a pattern cannot be
// expanded into names, so it is answered beside the repositories and never
// guessed at — the repositories an update migrates are D3306's real list.
func TestReadNamesAPatternItCannotList(t *testing.T) {
	ctx := context.Background()
	app := &fake.Provider{Repo: "acme/app", State: fake.State{Files: map[string]string{YAML: "agent: {}\n"}}}
	local := &fake.Provider{Repo: "acme/local", State: fake.State{Files: map[string]string{YAML: "agent: {}\n"}}}
	got, patterns, err := Read(ctx, Input{
		Repos:  []Listed{{Repo: "acme/local", Provider: "github"}},
		Admits: []string{"*", "github/acme/app", "github/acme/*", "bitbucket/acme/team/*", "acme/app", "*"},
	}, opens(map[string]gitprov.Provider{"github/acme/app": app, "github/acme/local": local}))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	// "*" twice is one pattern, and "acme/app" names no provider, so neither is
	// a repository this reader can list.
	if want := []string{"*", "github/acme/*", "bitbucket/acme/team/*", "acme/app"}; !slices.Equal(patterns, want) {
		t.Errorf("unexpanded patterns = %q, want %q", patterns, want)
	}
	if want := []string{"acme/local", "acme/app"}; !slices.Equal(names(got), want) {
		t.Fatalf("repositories = %q, want %q", names(got), want)
	}
	if got[1].Unread != "" || got[1].Provider != "github" {
		t.Errorf("acme/app = provider %q, Unread %q; want it read through the kind its admit names", got[1].Provider, got[1].Unread)
	}
}

// TestReadStopsOnADoneContext: a cancelled read is the caller's own and not a
// repository's failure, so it is returned rather than recorded as one
// (the rule `fugaro image survey` keeps).
func TestReadStopsOnADoneContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	app := &fake.Provider{Repo: "acme/app", State: fake.State{Files: map[string]string{YAML: "agent: {}\n"}}}
	got, patterns, err := Read(ctx, Input{Repos: []Listed{{Repo: "acme/app", Provider: "github"}}},
		opens(map[string]gitprov.Provider{"github/acme/app": app}))
	if err != context.Canceled {
		t.Fatalf("Read on a cancelled context = %v, want context.Canceled", err)
	}
	if got != nil || patterns != nil {
		t.Errorf("Read answered %v / %q beside the error", got, patterns)
	}
}

func names(repos []Repo) []string {
	out := make([]string, 0, len(repos))
	for _, r := range repos {
		out = append(out, r.Repo)
	}
	return out
}

func sameFile(a, b File) bool { return a.Path == b.Path && string(a.Data) == string(b.Data) }

// ghEntry is one file a fixture's folder holds, with the git mode the GitHub
// listing carries: a symbolic link is a blob whose bytes are its target's path
// (U3).
type ghEntry struct {
	Path string
	Mode string
	Data string
}

// ghSHA is the commit the fixtures' default branches resolve to: forty
// lower-hex characters, the shape a commit read answers with.
const ghSHA = "3f2b1c0d9e8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c"

// githubFixture replays one GitHub repository's read: the installation token
// mint, the repository read, the default branch's fugaro.yaml, the branch
// resolved to a commit, and the tree under .fugaro/recipes/ read one blob at a
// time — the exchanges the adapter's own tests record, in the order the reader
// makes them. The replay is strict, so a read the fixture did not record fails
// the test whichever side of it did not happen.
//
// A nil yaml leaves the file's exchange out, which is the contents API's own
// 404 — a repository that holds no fugaro.yaml at all.
func githubFixture(t *testing.T, yaml []byte, files []ghEntry) *github.Provider {
	t.Helper()
	tok := "ghs_fixture-not-a-credential"
	blobSHA := func(i int) string { return fmt.Sprintf("%040x", i+1) }
	x := []string{
		`{"method": "GET", "path": "/repos/acme/app/installation", "status": 200, "response": {"id": 777}}`,
		fmt.Sprintf(`{"method": "POST", "path": "/app/installations/777/access_tokens", "status": 201, "response": {"token": %q, "expires_at": "2026-09-27T11:00:00Z"}}`, tok),
		fmt.Sprintf(`{"method": "GET", "path": "/repos/acme/app", "auth": "Bearer %s", "status": 200, "response": {"private": true}}`, tok),
	}
	if yaml != nil {
		x = append(x, fmt.Sprintf(`{"method": "GET", "path": "/repos/acme/app/contents/%s", "status": 200, "response": {"type": "file", "encoding": "base64", "size": %d, "content": %q}}`,
			YAML, len(yaml), base64.StdEncoding.EncodeToString(yaml)))
	} else {
		x = append(x, fmt.Sprintf(`{"method": "GET", "path": "/repos/acme/app/contents/%s", "status": 404, "response": {"message": "Not Found", "documentation_url": "https://docs.github.com/rest"}}`, YAML))
	}
	x = append(x, fmt.Sprintf(`{"method": "GET", "path": "/repos/acme/app/commits/HEAD", "status": 200, "response": {"sha": %q}}`, ghSHA))
	var listed []string
	for i, f := range files {
		listed = append(listed, fmt.Sprintf(`{"path": "%s/%s", "type": "blob", "mode": %q, "size": %d, "sha": %q}`,
			recipe.RepoDir, f.Path, f.Mode, len(f.Data), blobSHA(i)))
	}
	x = append(x, fmt.Sprintf(`{"method": "GET", "path": "/repos/acme/app/git/trees/%s?recursive=1", "status": 200, "response": {"tree": [%s], "truncated": false}}`,
		ghSHA, strings.Join(listed, ", ")))
	for i, f := range files {
		x = append(x, fmt.Sprintf(`{"method": "GET", "path": "/repos/acme/app/git/blobs/%s", "status": 200, "response": {"encoding": "base64", "content": %q}}`,
			blobSHA(i), base64.StdEncoding.EncodeToString([]byte(f.Data))))
	}
	file := filepath.Join(t.TempDir(), "github.json")
	if err := os.WriteFile(file, []byte("["+strings.Join(x, ",")+"]"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := httpfixture.Serve(t, file)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p, err := github.New(github.Options{Owner: "acme", Repo: "app", AppID: "1234", PrivateKey: key, BaseURL: srv.URL,
		Now: func() time.Time { return time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// bitSHA is the commit the Bitbucket fixtures' default branches resolve to.
const bitSHA = "0a1b2c3d4e5f60718293a4b5c6d7e8f901234567"

// bitToken is the credential the Bitbucket routes are held to: a literal that
// is not, and does not look like, a real one.
const bitToken = "fixture-token-not-a-credential"

// bitReply is one answer a fixture gives: its status, its content type and its
// body.
type bitReply struct {
	status int
	ctype  string
	body   string
}

// bitServer answers the src and repository endpoints one Bitbucket read is
// made of, keyed by the escaped path and query the adapter asks for. Its
// routes are a map a test can rewrite — a case about a refused host registers
// the same read with another status — and a route it does not hold is the
// endpoint's own 404, which is what a repository that keeps no recipes folder
// answers.
type bitServer struct {
	mu     sync.Mutex
	srv    *httptest.Server
	name   string
	routes map[string]bitReply
	seen   []string
}

// newBitServer holds one repository's routes, at the "owner/name" the worker
// and repository options are taken from.
func newBitServer(t *testing.T, repo string) *bitServer {
	t.Helper()
	f := &bitServer{name: repo, routes: map[string]bitReply{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		at := r.URL.EscapedPath()
		if r.URL.RawQuery != "" {
			at += "?" + r.URL.RawQuery
		}
		f.mu.Lock()
		f.seen = append(f.seen, at)
		reply, ok := f.routes[at]
		f.mu.Unlock()
		if !ok {
			http.Error(w, `{"error": {"message": "not found"}}`, http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+bitToken {
			http.Error(w, `{"error": {"message": "no token"}}`, http.StatusUnauthorized)
			return
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

func (f *bitServer) at(path string) string { return "/2.0/repositories/" + f.name + path }

// yaml is the route the default branch's fugaro.yaml is read at, and listing
// the route the recipes folder is listed at — at the commit the fixture
// resolves the default branch to.
func (f *bitServer) yaml() string { return f.at("/src/main/" + YAML) }
func (f *bitServer) listing() string {
	return f.at("/src/" + bitSHA + "/" + recipe.RepoDir + "/?pagelen=100&max_depth=100")
}

// asked is every path the adapter asked for, in the order it asked.
func (f *bitServer) asked() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.seen)
}

// repo registers the read of one repository: its object, its default-branch
// fugaro.yaml, the commit that branch resolves to, and one listing for the
// files it holds. A nil yaml leaves the file's route out, which is the src
// endpoint's own 404 — a repository that holds no fugaro.yaml at all.
func (f *bitServer) repo(yaml []byte, files []ghEntry) {
	// The repository object is read once for the visibility, once for the
	// default branch the file is read at, and once for the branch the commit
	// is resolved from: the adapter asks for it rather than assuming a name.
	f.routes[f.at("")] = bitReply{status: 200, ctype: "application/json",
		body: `{"is_private": true, "mainbranch": {"name": "main"}}`}
	if yaml != nil {
		f.routes[f.yaml()] = bitReply{status: 200, ctype: "application/octet-stream", body: string(yaml)}
	}
	f.routes[f.at("/commit/main")] = bitReply{status: 200, ctype: "application/json", body: `{"hash": "` + bitSHA + `"}`}
	var values []string
	for _, file := range files {
		values = append(values, fmt.Sprintf(`{"path": "%s/%s", "type": "commit_file", "size": %d}`, recipe.RepoDir, file.Path, len(file.Data)))
		f.routes[f.at("/src/"+bitSHA+"/"+recipe.RepoDir+"/"+file.Path)] = bitReply{status: 200, ctype: "application/octet-stream", body: file.Data}
	}
	f.routes[f.listing()] = bitReply{status: 200, ctype: "application/json",
		body: fmt.Sprintf(`{"pagelen": 100, "values": [%s]}`, strings.Join(values, ", "))}
}

// noFolder leaves the folder's listing out, so the read meets the 404 that is
// a repository keeping no recipes of its own — not a failure.
func (f *bitServer) noFolder() { delete(f.routes, f.listing()) }

// fail replaces one route with a refusal, for a case about a host that answers
// an error rather than the file or the folder.
func (f *bitServer) fail(path string, status int, body string) {
	f.routes[path] = bitReply{status: status, ctype: "application/json", body: body}
}

func (f *bitServer) provider(t *testing.T) *bitbucket.Provider {
	t.Helper()
	workspace, slug, ok := strings.Cut(f.name, "/")
	if !ok {
		t.Fatalf("bitServer name %q is not owner/name", f.name)
	}
	p, err := bitbucket.New(bitbucket.Options{Workspace: workspace, Slug: slug, Token: bitToken,
		BaseURL: f.srv.URL + "/2.0", HTTP: f.srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
