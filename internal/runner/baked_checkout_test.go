package runner_test

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// otherRemote is a repository that is not the fixture's (acme/app): the origin
// a baked checkout of another repository carries, and the repository a run that
// opened it would work on and push to.
const otherRemote = "https://github.com/evil/other.git"

// bakedDir makes the directory a shared image lays a repository it baked out
// in, named by the repository's storage slug, beside the work directory
// (runner.go's BakedRepoRoot).
func bakedDir(t *testing.T, h *harness, slug string) string {
	t.Helper()
	dir := filepath.Join(filepath.Dir(h.deps.WorkDir), "baked", slug)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// otherRepositoryCheckout makes dir a checkout of another repository: a clone
// of the fixture's remote, so that nothing here needs a network, whose origin
// is then the other repository's.
func otherRepositoryCheckout(t *testing.T, h *harness, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, filepath.Dir(dir), "clone", "--quiet", h.remote, dir)
	testutil.Git(t, dir, "remote", "set-url", "origin", otherRemote)
}

// originlessCheckout makes dir a checkout git opens and no remote names: git
// itself resolves its repository to a .git with no origin, which is what a
// directory something left behind looks like, and what gitops.Open opens (it
// stats .git) while `git remote get-url origin` fails on it.
func originlessCheckout(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, dir, "init", "--quiet")
}

// hostilePushCheckout makes dir a checkout of this run's own repository whose
// push URL is another repository's: the fetch URL the guard reads is the run's
// own, and `git push origin` would go elsewhere. The other repository is a bare
// one this test makes and the push URL is a file:// URL of it, so a run that
// gets as far as the push really lands its branch there — and a local path
// would not do, because a path names no repository and passes the binding.
func hostilePushCheckout(t *testing.T, h *harness, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, filepath.Dir(dir), "clone", "--quiet", h.remote, dir)
	testutil.Git(t, dir, "config", "remote.origin.pushurl", "file://"+testutil.NewRemote(t, map[string]string{"README.md": "other\n"}))
}

// rewrittenPushCheckout makes dir a checkout of this run's own repository whose
// configuration rewrites the URL a push names to another repository's: its
// origin is a URL of this repository, its pushurl is the same repository in
// another spelling (the ssh one, which the binding admits), and the rewrite is
// the only thing that says the run's commits would land elsewhere — with no
// pushurl set git resolves the rewrite and answers with it, and the pushurl is
// what puts it out of git's own read of the remote (PushURLs' doc).
func rewrittenPushCheckout(t *testing.T, h *harness, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, filepath.Dir(dir), "clone", "--quiet", h.remote, dir)
	testutil.Git(t, dir, "remote", "set-url", "origin", "https://github.com/acme/app.git")
	testutil.Git(t, dir, "config", "remote.origin.pushurl", "git@github.com:acme/app.git")
	testutil.Git(t, dir, "config", "url.https://github.com/evil/.pushInsteadOf", "https://github.com/acme/")
}

// secondRewriteCheckout makes dir a checkout of this run's own repository whose
// configuration rewrites the URL a push names twice: its origin is a URL of this
// repository and its pushurl is the same repository in a third spelling (the
// ssh URL, which the binding admits), so both of the guard's reads answer with
// this repository — and two url.*.insteadOf rules chain, the first being what
// git's own read of the origin applies (https to the scp-like spelling, which
// the binding admits as this repository too) and the second matching that
// rewritten URL, which only a push that names it meets. The second rule is the
// only thing that says the run's commits would land elsewhere.
func secondRewriteCheckout(t *testing.T, h *harness, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, filepath.Dir(dir), "clone", "--quiet", h.remote, dir)
	testutil.Git(t, dir, "remote", "set-url", "origin", "https://github.com/acme/app.git")
	testutil.Git(t, dir, "config", "remote.origin.pushurl", "ssh://git@github.com/acme/app.git")
	testutil.Git(t, dir, "config", "url.git@github.com:.insteadOf", "https://github.com/")
	testutil.Git(t, dir, "config", "url.https://github.com/evil/.insteadOf", "git@github.com:acme/")
}

// urlNamedRemoteCheckout makes dir a checkout of this run's own repository whose
// configuration holds a *remote named by the origin URL* — the section
// [remote "https://github.com/acme/app.git"], which is a remote whose name is
// that URL — with a pushurl of another repository's. git resolves the URL a push
// names to a remote by name, so the checkpoint push (gitops' PushFastForward,
// which names the URL) goes to that pushurl, while the origin, the remote named
// "origin" and the URL's own rewrite all say this repository. The pushurl is a
// file:// URL of a bare repository this test makes, so a run that gets past the
// guard really lands its branch there.
func urlNamedRemoteCheckout(t *testing.T, h *harness, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, filepath.Dir(dir), "clone", "--quiet", h.remote, dir)
	testutil.Git(t, dir, "remote", "set-url", "origin", "https://github.com/acme/app.git")
	testutil.Git(t, dir, "config", "remote.https://github.com/acme/app.git.pushurl", "file://"+testutil.NewRemote(t, map[string]string{"README.md": "other\n"}))
}

// TestBakedCheckoutOriginMismatchRefuses (IS8): the job is for acme/app, and a
// checkout the image already holds is another repository's — a shared image
// built for someone else, or a warm container an earlier job or execution left
// behind. The run must not work on it: the diff would be repo B's source
// committed under repo A's slug and pushed with this run's git credential. It
// stops at bootstrap, before any git credential is requested and before a stage
// runs.
//
// The rows are the ways such a checkout can be another repository's — its
// origin (the work directory, which the admission check reads too, and the
// per-slug directory the run would adopt for its own repository on a shared
// image, which nothing else looks at) and its push URLs, which are where the
// run's own branch actually lands: gitops' Push goes to origin's pushurl when
// the checkout sets one, and a checkpoint (gitops' PushFastForward) pushes by
// origin's URL, which git rewrites by the checkout's url.*.pushInsteadOf and
// url.*.insteadOf rules — the URL it names is the one the origin read returned,
// so a rule matching what another rule produced is a rewrite of that push too.
//
// Mutation (run, restore): drop the checkBakedCheckoutOrigin call — the run
// opens another repository's checkout and fails further down (no fugaro.yaml,
// then a fetch of a remote that does not resolve), with a git credential
// already requested. Mutation (run, restore): return right after
// openBakedCheckout sets r.d.WorkDir, checking only the work directory's own
// checkout — the origin rows for the work directory still pass and the
// per-slug ones fail. Mutation (run, restore): drop the PushURLs loop — the
// push-URL rows fail while every origin row still passes. Mutation (run,
// restore): gitops' PushURLs returning git's own read alone, without the
// pushInsteadOf rewrite of the origin URL — the rewrite rows fail: the run goes
// past the guard and fails further down with a git credential already
// requested. Mutation (run, restore): the same, with the url.*.insteadOf rules
// left out of that resolution — the second-rewrite rows fail, and only they do:
// git's own read of the remote answers with the first rule's result, which is
// this repository's, while the push opens the second rule's, which is not.
// Mutation (run, restore): gitops' PushURLs without the read of the origin URL
// as a remote name — the URL-named-remote rows fail, and only they do: every
// other read answers for the remote named "origin" and for the URL's rewrite,
// which are this repository's.
func TestBakedCheckoutOriginMismatchRefuses(t *testing.T) {
	for _, tc := range []struct {
		name  string
		plant func(t *testing.T, h *harness)
		want  []string // what the refusal must name besides the shared substrings
	}{
		{"the work directory", func(t *testing.T, h *harness) {
			otherRepositoryCheckout(t, h, h.deps.WorkDir)
		}, []string{otherRemote, "evil/other"}},
		{"the directory the run would adopt", func(t *testing.T, h *harness) {
			otherRepositoryCheckout(t, h, bakedDir(t, h, repoSlug))
		}, []string{otherRemote, "evil/other"}},
		{"the work directory's push URL", func(t *testing.T, h *harness) {
			hostilePushCheckout(t, h, h.deps.WorkDir)
		}, []string{"pushes to file://"}},
		{"the directory the run would adopt, by its push URL", func(t *testing.T, h *harness) {
			hostilePushCheckout(t, h, bakedDir(t, h, repoSlug))
		}, []string{"pushes to file://"}},
		{"the work directory's push rewrite", func(t *testing.T, h *harness) {
			rewrittenPushCheckout(t, h, h.deps.WorkDir)
		}, []string{"pushes to https://github.com/evil/app.git"}},
		{"the directory the run would adopt, by its push rewrite", func(t *testing.T, h *harness) {
			rewrittenPushCheckout(t, h, bakedDir(t, h, repoSlug))
		}, []string{"pushes to https://github.com/evil/app.git"}},
		{"the work directory's second push rewrite", func(t *testing.T, h *harness) {
			secondRewriteCheckout(t, h, h.deps.WorkDir)
		}, []string{"pushes to https://github.com/evil/app.git"}},
		{"the directory the run would adopt, by its second push rewrite", func(t *testing.T, h *harness) {
			secondRewriteCheckout(t, h, bakedDir(t, h, repoSlug))
		}, []string{"pushes to https://github.com/evil/app.git"}},
		{"the work directory's URL-named remote", func(t *testing.T, h *harness) {
			urlNamedRemoteCheckout(t, h, h.deps.WorkDir)
		}, []string{"pushes to file://"}},
		{"the directory the run would adopt, by its URL-named remote", func(t *testing.T, h *harness) {
			urlNamedRemoteCheckout(t, h, bakedDir(t, h, repoSlug))
		}, []string{"pushes to file://"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, "", nil)
			auths := 0
			h.provider.Auth = func(time.Duration) gitprov.GitAuth {
				auths++
				return gitprov.GitAuth{}
			}
			tc.plant(t, h)
			rec, err := h.run(t, implement("feature"), review("ship", 0))
			if err == nil || rec == nil || rec.Status != runstore.StatusInfraError {
				t.Fatalf("rec = %+v, err = %v: the run did not refuse another repository's checkout", rec, err)
			}
			for _, want := range append([]string{"acme/app", "not this run's repository"}, tc.want...) {
				if !strings.Contains(rec.Reason, want) {
					t.Errorf("reason %q lacks %q", rec.Reason, want)
				}
			}
			if strings.Contains(rec.Reason, "\n") {
				t.Errorf("the reason is not one line: %q", rec.Reason)
			}
			if auths != 0 {
				t.Errorf("%d git credential requests were made before the refusal", auths)
			}
			if len(h.agent.calls) != 0 {
				t.Errorf("%d stages ran after the refusal", len(h.agent.calls))
			}
		})
	}
}

// TestBakedCheckoutPerSlugPath (IS8): the image is shared by the project's
// repositories and holds each baked one at its own per-slug directory (D1900:
// a per-repository job runs the project's catalog image, which bakes several),
// this run's repository is one of them, and the work directory holds no
// checkout of its own. The run works in the directory the image baked for its
// own repository: the stages run there, the run's branch is checked out there,
// and the push that ends the run comes from it.
func TestBakedCheckoutPerSlugPath(t *testing.T) {
	h := newHarness(t, "", nil)
	baked := bakedDir(t, h, repoSlug)
	testutil.Git(t, filepath.Dir(baked), "clone", "--quiet", h.remote, baked)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	want, err := filepath.EvalSymlinks(baked) // $PWD is resolved (review L2)
	if err != nil {
		t.Fatal(err)
	}
	if got := h.agent.calls[0].Dir; got != want {
		t.Errorf("the implement stage ran in %q, want the image's baked checkout %q", got, want)
	}
	if got := testutil.Git(t, baked, "rev-parse", "--abbrev-ref", "HEAD"); got != "fugaro/"+runID {
		t.Errorf("the baked checkout is on %q, want the run's own branch", got)
	}
	if _, err := os.Stat(filepath.Join(baked, "feature.txt")); err != nil {
		t.Errorf("the baked checkout has no feature.txt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.deps.WorkDir, ".git")); err == nil {
		t.Error("the run cloned into the work directory as well as using the baked checkout")
	}
	if got := testutil.Git(t, h.remote, "rev-parse", "refs/heads/fugaro/"+runID); got != rec.HeadSHA {
		t.Errorf("remote branch %s != record head %s", got, rec.HeadSHA)
	}
}

// TestBakedCheckoutPerSlugPathRecordsItsOwnCommit (IS8, review): the record's
// image names the commit of the checkout the run works on, and on a shared
// catalog image that checkout is the per-slug one bootstrap adopts — the work
// directory holds no checkout at all when the image's record is read, so the
// fallback there is the commit the image's own record names, which belongs to
// whichever repository the image was built from. The two differ here, and the
// field has to be the adopted checkout's.
//
// Mutation (run, restore): drop the read bootstrap makes once the checkout is
// adopted (bakedHead, after openBakedCheckout) — the record keeps ffffffff, the
// image's own build commit, and this test is the one that fails.
func TestBakedCheckoutPerSlugPathRecordsItsOwnCommit(t *testing.T) {
	h := newHarness(t, "", nil)
	baked := bakedDir(t, h, repoSlug)
	testutil.Git(t, filepath.Dir(baked), "clone", "--quiet", h.remote, baked)
	testutil.Git(t, baked, "commit", "--quiet", "--allow-empty", "-m", "baked")
	head := testutil.Git(t, baked, "rev-parse", "HEAD")
	builtAt := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	h.deps.ImageInfoPath = filepath.Join(t.TempDir(), "image.json")
	if err := os.WriteFile(h.deps.ImageInfoPath, []byte(`{"built_at":"`+builtAt.Format(time.RFC3339)+`","commit":"ffffffff"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	stored, err := h.store.ReadRecord(context.Background())
	if err != nil || stored.Image == nil || stored.Image.BakedCommit != head || stored.Image.BuiltAt == nil || !stored.Image.BuiltAt.Equal(builtAt) {
		t.Fatalf("stored image = %+v, %v (the adopted checkout is at %s)", stored.Image, err, head)
	}
	if stored.Image.BakedCommit == "ffffffff" {
		t.Fatal("the baked commit is the image's own build record's, which is another repository's")
	}
}

// TestBakedMissingFallsBackToClone (IS8): the image's per-slug layout holds
// another repository's checkout and none for this run's repository, so nothing
// is baked for this run to work on: it clones its own remote into the work
// directory, and the other repository's checkout is left exactly as it was.
func TestBakedMissingFallsBackToClone(t *testing.T) {
	h := newHarness(t, "", nil)
	other := bakedDir(t, h, mustSlug("github", "evil/other"))
	testutil.Git(t, filepath.Dir(other), "clone", "--quiet", h.remote, other)
	// The other repository's own origin, so a run that opened this checkout
	// would be refused rather than quietly succeed in the wrong directory.
	testutil.Git(t, other, "remote", "set-url", "origin", otherRemote)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if got := testutil.Git(t, h.deps.WorkDir, "config", "remote.origin.url"); got != h.remote {
		t.Errorf("the checkout's origin = %q, want the job's own remote %q", got, h.remote)
	}
	if _, err := os.Stat(filepath.Join(h.deps.WorkDir, "feature.txt")); err != nil {
		t.Errorf("the run did not work in the checkout it cloned: %v", err)
	}
	if got := testutil.Git(t, other, "rev-parse", "--abbrev-ref", "HEAD"); got != "main" {
		t.Errorf("the other repository's baked checkout is on %q, want it left as it was (main)", got)
	}
	if got := testutil.Git(t, other, "branch", "--list", "fugaro/"+runID); got != "" {
		t.Errorf("the other repository's baked checkout holds the run's branch: %q", got)
	}
}

// TestBakedCheckoutPerSlugPathIsCredentialed (IS8, review): a baked job's
// environment carries no FUGARO_REMOTE — its checkout is the only thing that
// names the repository — so the remote this run clones from, fetches and pushes
// and the endpoint its git credential is scoped to are the adopted checkout's
// own origin (cloneURL's phase-1 answer, "the origin of a checkout already in
// the work directory, else the job's remote"). Read before the adoption, that
// answer is the empty string, which is what a run whose work directory holds no
// checkout and whose environment names no remote has: credentialVars then
// installs no credential helper at all (gitops.CredentialVars refuses the empty
// URL), and the fetch of origin, the agent's git and the final push all fail
// with git's own "could not read Username".
//
// The origin is written the way the launcher writes one for this repository
// (internal/infra's repoURL) and not as the fixture's local path, so it is a
// remote whose credential git really needs and really sends: the token has to
// arrive at the 401 the repository's server answers its unauthenticated
// requests with. Nothing here reaches github.com — git's one way out is the
// loopback proxy its own http.<url>.proxy names, which tunnels the connection
// to a git server this test serves over TLS on 127.0.0.1, and the server lets a
// request through only for the provider's username and token (tlsGitServer).
func TestBakedCheckoutPerSlugPathIsCredentialed(t *testing.T) {
	const remote = "https://github.com/acme/app.git"
	h := newHarness(t, "", nil)
	h.provider.Auth = staticAuth(gitToken)
	baked := bakedDir(t, h, repoSlug)
	testutil.Git(t, filepath.Dir(baked), "clone", "--quiet", h.remote, baked)
	testutil.Git(t, baked, "remote", "set-url", "origin", remote)
	tlsGitServer(t, h, remote)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if got := testutil.Git(t, baked, "rev-parse", "--abbrev-ref", "HEAD"); got != "fugaro/"+runID {
		t.Fatalf("the baked checkout is on %q, want the run's own branch", got)
	}
	if got := testutil.Git(t, h.remote, "rev-parse", "refs/heads/fugaro/"+runID); got != rec.HeadSHA {
		t.Fatalf("remote branch %s != record head %s", got, rec.HeadSHA)
	}
	env := h.agent.calls[0].Env
	// Scoped to the endpoint the credential is sent to — the adopted origin's
	// scheme://host, which is what git will open — and carrying the provider's
	// token, not just the provider's own variables (staticAuth's GH_TOKEN).
	var scoped bool
	for _, pair := range configPairs(env) {
		if pair[0] == "credential.https://github.com.helper" {
			scoped = true
		}
	}
	if !scoped {
		t.Errorf("the agent's environment configures no git credential helper for the image's baked checkout (key credential.https://github.com.helper): %v", configPairs(env))
	}
	for k, want := range map[string]string{
		"FUGARO_GIT_TOKEN":    gitToken,
		"FUGARO_GIT_USERNAME": "x-token-auth",
	} {
		if got := envValue(env, k); got != want {
			t.Errorf("agent %s = %q, want %q: the credential is scoped to the checkout the run adopted", k, got, want)
		}
	}
}

// tlsGitServer serves the harness's own bare repository over TLS at the path
// origin names, behind the provider's username and token, and points git at it
// for that URL's host: a request carrying the token is passed to
// `git http-backend`, anything else is answered with the 401 that makes git ask
// its credential helper. git reaches the server through connectProxy, so the URL
// it opens stays the launcher's, and the credential it is asked for is the one
// this run scoped to that URL.
//
// It is testutil.NewHTTPRemote's server over TLS and with the one difference
// this fixture needs: the bare repository is the harness's own (`remote.git`)
// while origin names it as the provider does (acme/app.git), so a symlink lays
// it out under the path the request asks for. The certificate is httptest's,
// which nothing here knows, so sslVerify=false goes into the same global config
// as the proxy — the two are one detour, and git's transport needs both.
func tlsGitServer(t *testing.T, h *harness, origin string) {
	t.Helper()
	testutil.Git(t, h.remote, "config", "http.receivepack", "true")
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "acme"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(h.remote, filepath.Join(root, "acme", "app.git")); err != nil {
		t.Fatal(err)
	}
	backend := &cgi.Handler{
		Path: filepath.Join(testutil.Git(t, h.remote, "--exec-path"), "git-http-backend"),
		Env:  []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"},
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		user, pass, ok := req.BasicAuth()
		if !ok || user != "x-token-auth" || pass != gitToken {
			w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		backend.ServeHTTP(w, req)
	}))
	proxy := connectProxy(t, srv.Listener.Addr().String())
	u, err := url.Parse(origin)
	if err != nil {
		t.Fatal(err)
	}
	gitcfg := filepath.Join(t.TempDir(), "gitconfig")
	cfg := "[http \"" + u.Scheme + "://" + u.Host + "/\"]\n\tproxy = http://" + proxy + "\n\tsslVerify = false\n"
	if err := os.WriteFile(gitcfg, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", gitcfg)
}

// connectProxy is a proxy that serves only CONNECT: it tunnels every connection
// to target, whichever host the CONNECT asked for, which is what keeps this
// test's git on 127.0.0.1 while the URL it opens is the launcher's own.
func connectProxy(t *testing.T, target string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				// The CONNECT head, read to its blank line off br and not off
				// c: whatever the reader buffered past it is the first of the
				// tunnel's bytes (the TLS handshake), and it is copied from
				// there and not reread off the connection.
				br := bufio.NewReader(c)
				for {
					line, err := br.ReadString('\n')
					if err != nil {
						return
					}
					if line == "\r\n" || line == "\n" {
						break
					}
				}
				up, err := net.Dial("tcp", target)
				if err != nil {
					return
				}
				defer up.Close()
				if _, err := c.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
					return
				}
				go io.Copy(up, br)
				io.Copy(c, up)
			}()
		}
	}()
	return ln.Addr().String()
}

// TestBakedCheckoutWithoutAReadableOriginRefuses (IS8): the checkout the image
// holds is one no remote ties to any repository — a .git git opens whose
// `git remote get-url origin` fails. The guard fails closed: a checkout nothing
// names is refused rather than worked on, which is the same hole as another
// repository's checkout and the one the other arm of the guard (an origin that
// reads) would otherwise leave open.
//
// The two rows are the two places such a checkout can be, the mismatch test's.
func TestBakedCheckoutWithoutAReadableOriginRefuses(t *testing.T) {
	for _, tc := range []struct {
		name  string
		plant func(t *testing.T, h *harness)
	}{
		{"the work directory", func(t *testing.T, h *harness) {
			originlessCheckout(t, h.deps.WorkDir)
		}},
		{"the directory the run would adopt", func(t *testing.T, h *harness) {
			originlessCheckout(t, bakedDir(t, h, repoSlug))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, "", nil)
			auths := 0
			h.provider.Auth = func(time.Duration) gitprov.GitAuth {
				auths++
				return gitprov.GitAuth{}
			}
			tc.plant(t, h)
			rec, err := h.run(t, implement("feature"), review("ship", 0))
			if err == nil || rec == nil || rec.Status != runstore.StatusInfraError {
				t.Fatalf("rec = %+v, err = %v: the run did not refuse a checkout with no readable origin", rec, err)
			}
			for _, want := range []string{"carries no origin this run can read", "acme/app"} {
				if !strings.Contains(rec.Reason, want) {
					t.Errorf("reason %q lacks %q", rec.Reason, want)
				}
			}
			if strings.Contains(rec.Reason, "\n") {
				t.Errorf("the reason is not one line: %q", rec.Reason)
			}
			if auths != 0 {
				t.Errorf("%d git credential requests were made before the refusal", auths)
			}
			if len(h.agent.calls) != 0 {
				t.Errorf("%d stages ran after the refusal", len(h.agent.calls))
			}
		})
	}
}
