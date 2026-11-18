package gitops

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// submoduleCheckout is a checkout whose origin is origin and whose .gitmodules
// declares urls, each submodule with the gitlink in the index that `git
// submodule init` reads .gitmodules for: git copies a submodule's URL into the
// checkout's config only for the submodules the index names, so a checkout
// without them would let this test's comparison against git pass on an empty
// answer.
func submoduleCheckout(t *testing.T, origin string, urls map[string]string) *Repo {
	t.Helper()
	repo, _ := setup(t)
	testutil.Git(t, repo.Dir, "remote", "set-url", "origin", origin)
	names := slices.Sorted(maps.Keys(urls))
	var gm strings.Builder
	for _, name := range names {
		fmt.Fprintf(&gm, "[submodule %q]\n\tpath = %s\n\turl = %s\n", name, name, urls[name])
	}
	testutil.WriteFiles(t, repo.Dir, map[string]string{".gitmodules": gm.String()})
	testutil.Git(t, repo.Dir, "add", ".gitmodules")
	sha := testutil.Git(t, repo.Dir, "rev-parse", "HEAD")
	for _, name := range names {
		testutil.Git(t, repo.Dir, "update-index", "--add", "--cacheinfo", "160000,"+sha+","+name)
	}
	testutil.Git(t, repo.Dir, "commit", "--quiet", "-m", "submodules")
	return repo
}

// TestSubmoduleURLsResolveLikeGit holds this package's resolution of a relative
// submodule URL to git's own: the same .gitmodules in the same checkout, once
// through Submodules and once through `git submodule init`, which is the code a
// nested clone fetches with. A resolution that disagreed would have a run
// decide whether a repository is covered for one repository and have git fetch
// another.
func TestSubmoduleURLsResolveLikeGit(t *testing.T) {
	urls := map[string]string{
		"sibling":   "../lib.git",
		"up":        "../../other/lib.git",
		"here":      "./sub.git",
		"absolute":  "https://bitbucket.org/acme/other.git",
		"overshoot": "../../../../../x/y.git",
		"trailing":  "../x/",
	}
	// What git answers under the https origin, pinned as well as compared: the
	// shapes a repository actually writes (sibling, here, absolute) are what
	// the endpoint a run routes has to be built from, and the two that are
	// git's own odd answers — one "../" too many cutting into the "//" of the
	// scheme, and the one trailing "/" git strips — are what this test is
	// copied from git for.
	want := map[string]string{
		"sibling":   "https://bitbucket.org/acme/lib.git",
		"up":        "https://bitbucket.org/other/lib.git",
		"here":      "https://bitbucket.org/acme/app.git/sub.git",
		"absolute":  "https://bitbucket.org/acme/other.git",
		"overshoot": "https:x/y.git",
		"trailing":  "https://bitbucket.org/acme/x",
	}
	// The scp-like origin resolves to the same strings with the ":" joined
	// back (git@bitbucket.org:acme/lib.git), which is why it is compared
	// against git's read rather than pinned a second time. It is held to the
	// shapes that resolve there: one "../" past its authority has no "/" left
	// to cut and no ":" left either, and git itself refuses the whole
	// .gitmodules for it — "fatal: cannot strip one component off url '.'" —
	// where this package leaves the URL as the repository wrote it for its
	// caller to refuse by name.
	scp := maps.Clone(urls)
	delete(scp, "overshoot")
	for _, origin := range []string{"https://bitbucket.org/acme/app.git", "git@bitbucket.org:acme/app.git"} {
		declared := urls
		if strings.HasPrefix(origin, "git@") {
			declared = scp
		}
		t.Run(origin, func(t *testing.T) {
			repo := submoduleCheckout(t, origin, declared)
			testutil.Git(t, repo.Dir, "submodule", "init")
			subs, err := repo.Submodules(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(subs) != len(declared) {
				t.Fatalf("submodules = %d, want %d: %+v", len(subs), len(declared), subs)
			}
			for _, sub := range subs {
				got := testutil.Git(t, repo.Dir, "config", "--get", "submodule."+sub.Name+".url")
				if sub.URL != got {
					t.Errorf("submodule %s = %q, git resolved it to %q", sub.Name, sub.URL, got)
				}
				if !strings.HasPrefix(origin, "git@") && sub.URL != want[sub.Name] {
					t.Errorf("submodule %s = %q, want %q", sub.Name, sub.URL, want[sub.Name])
				}
			}
		})
	}
}

// TestSubmoduleURLsNoneDeclared: a checkout without .gitmodules declares no
// submodule, and asks git for nothing.
func TestSubmoduleURLsNoneDeclared(t *testing.T) {
	repo, _ := setup(t)
	subs, err := repo.Submodules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if subs != nil {
		t.Fatalf("submodules = %+v, want none", subs)
	}
}

// TestSubmoduleURLsInConfigOutrankGitmodules: the route a run installs
// (PointSubmoduleAt) is the checkout's own configuration and outranks the
// tracked file, which stays byte for byte what the repository wrote — a run
// that rewrote .gitmodules would commit the rewrite — and `git submodule init`
// does not overwrite the route with the URL the file declares.
func TestSubmoduleURLsInConfigOutrankGitmodules(t *testing.T) {
	repo := submoduleCheckout(t, "https://bitbucket.org/acme/app.git", map[string]string{"lib": "../lib.git"})
	file := filepath.Join(repo.Dir, ".gitmodules")
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "http://127.0.0.1:8443/v1/bitbucket/proxy/acme/lib.git"
	if err := repo.PointSubmoduleAt(ctx, "lib", endpoint); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, repo.Dir, "submodule", "init")
	if got := testutil.Git(t, repo.Dir, "config", "--get", "submodule.lib.url"); got != endpoint {
		t.Errorf("submodule.lib.url = %q, want the endpoint %q", got, endpoint)
	}
	after, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf(".gitmodules changed to %q, want %q", after, before)
	}
	if got := testutil.Git(t, repo.Dir, "status", "--porcelain", "--", ".gitmodules"); got != "" {
		t.Errorf("git status reports .gitmodules as %q", got)
	}
}

// TestLFSEndpointIsWhatTheCheckoutConfigCarries: PointLFSAt sets lfs.url, the
// key git-lfs reads in preference to the endpoint it derives from the remote,
// and a checkout nothing points carries none — a run with no forwarder writes
// nothing here, so what git-lfs uses is the remote, and the filters git-lfs
// installed stay as the job's configuration left them.
func TestLFSEndpointIsWhatTheCheckoutConfigCarries(t *testing.T) {
	repo, _ := setup(t)
	// git exits non-zero for a key nothing set, which is why this reads it
	// through the package's own git rather than through testutil.Git.
	if got, err := repo.git(ctx, "config", "--get", "lfs.url"); err == nil {
		t.Fatalf("lfs.url = %q before anything pointed it", got)
	}
	endpoint := "http://127.0.0.1:8443/v1/bitbucket/proxy/acme/app.git/info/lfs"
	if err := repo.PointLFSAt(ctx, endpoint); err != nil {
		t.Fatal(err)
	}
	if got := testutil.Git(t, repo.Dir, "config", "--get", "lfs.url"); got != endpoint {
		t.Errorf("lfs.url = %q, want %q", got, endpoint)
	}
}
