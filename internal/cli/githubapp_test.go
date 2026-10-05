package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/infra"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

var (
	appKeyOnce sync.Once
	appKeyData []byte
)

// appKey is a GitHub App private key for the tests (generating one is slow).
func appKey(t *testing.T) []byte {
	t.Helper()
	appKeyOnce.Do(func() { appKeyData = []byte(appKeyPEM(t)) })
	return appKeyData
}

// githubRig is a GitHub that answers the App's installation lookup, and the
// secret read behind it.
type githubRig struct {
	status int
	perms  map[string]string
	auth   []string // the Authorization headers GitHub saw
	keyErr error    // what reading the key answers
	key    []byte
	reads  int
	srv    *httptest.Server
}

var fullPerms = map[string]string{"contents": "write", "pull_requests": "write", "issues": "read", "metadata": "read"}

func newGitHubRig(t *testing.T) *githubRig {
	t.Helper()
	g := &githubRig{status: 200, perms: fullPerms, key: appKey(t)}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.auth = append(g.auth, r.Header.Get("Authorization"))
		if r.URL.Path != "/repos/acme/app/installation" {
			t.Errorf("unexpected GitHub request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(g.status)
		if g.status == 200 {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 9, "permissions": g.perms})
		} else {
			_, _ = w.Write([]byte(`{"message":"nope"}`))
		}
	}))
	t.Cleanup(g.srv.Close)
	oldAPI, oldRead := githubAPIURL, readAppKey
	githubAPIURL = g.srv.URL
	readAppKey = func(_ context.Context, _ *localcfg.Config, secretID string) ([]byte, error) {
		g.reads++
		if secretID != "app-key-secret" {
			t.Errorf("read secret %q", secretID)
		}
		return append([]byte(nil), g.key...), g.keyErr
	}
	t.Cleanup(func() { githubAPIURL, readAppKey = oldAPI, oldRead })
	return g
}

func (g *githubRig) check(t *testing.T) appCheck {
	t.Helper()
	return g.checkWith(t, appKeySource{Read: true, SecretID: "app-key-secret", Notice: io.Discard})
}

func (g *githubRig) checkWith(t *testing.T, src appKeySource) appCheck {
	t.Helper()
	return checkGitHubApp(t.Context(), &localcfg.Config{Name: "aurora", GCPProject: initProject}, "acme/app", "12345", src)
}

// leaks is whether any of text holds the key, a JWT, or what GitHub was sent.
func (g *githubRig) leaks(t *testing.T, text string) bool {
	t.Helper()
	key := string(appKey(t))
	body := strings.Join(strings.Split(strings.TrimSpace(key), "\n")[1:len(strings.Split(strings.TrimSpace(key), "\n"))-1], "")
	for _, secret := range []string{key, body, base64.StdEncoding.EncodeToString(appKey(t)), "BEGIN RSA PRIVATE KEY", "eyJhbGciOi"} {
		if secret != "" && strings.Contains(text, secret) {
			return true
		}
	}
	for _, a := range g.auth {
		if tok := strings.TrimPrefix(a, "Bearer "); tok != "" && strings.Contains(text, tok) {
			return true
		}
	}
	return false
}

func TestGitHubAppPreCheck(t *testing.T) {
	t.Run("fine", func(t *testing.T) {
		g := newGitHubRig(t)
		c := g.check(t)
		if c.Verdict != appFine || len(c.Warnings) != 0 || g.reads != 1 || len(g.auth) != 1 || !strings.HasPrefix(g.auth[0], "Bearer ") {
			t.Fatalf("%+v reads %d auth %v", c, g.reads, g.auth)
		}
	})
	t.Run("not installed", func(t *testing.T) {
		g := newGitHubRig(t)
		g.status = 404
		c := g.check(t)
		if c.Verdict != appFailed || c.Problem != "the GitHub App 12345 is not installed on acme/app" ||
			c.Fix != "install it on this repository only (Settings, GitHub Apps, Install App)" {
			t.Fatalf("%+v", c)
		}
	})
	t.Run("each missing permission, with its fix", func(t *testing.T) {
		g := newGitHubRig(t)
		g.perms = map[string]string{"contents": "read", "metadata": "read"}
		c := g.check(t)
		if c.Verdict != appFailed {
			t.Fatalf("%+v", c)
		}
		for _, want := range []string{"Contents: Write", "Issues: Read", "Pull requests: Write"} {
			if !strings.Contains(c.Problem, want) {
				t.Errorf("the problem lacks %q: %s", want, c.Problem)
			}
		}
		for _, want := range []string{"add Issues: Read, then accept the new permission on the installation", "add Pull requests: Write", "change Contents to Read and write"} {
			if !strings.Contains(c.Fix, want) {
				t.Errorf("the fix lacks %q: %s", want, c.Fix)
			}
		}
		if strings.Contains(c.Problem, "Metadata") {
			t.Errorf("a granted permission is named: %s", c.Problem)
		}
	})
	t.Run("workflows write is a warning, never silence", func(t *testing.T) {
		g := newGitHubRig(t)
		g.perms = map[string]string{"contents": "write", "pull_requests": "write", "issues": "read", "metadata": "read", "workflows": "write", "administration": "read"}
		c := g.check(t)
		if c.Verdict != appFine || len(c.Warnings) != 2 || !strings.Contains(c.Warnings[0], "Workflows: Write") || !strings.Contains(c.Warnings[0], "must never have it") ||
			!strings.Contains(c.Warnings[1], "Administration: Read") {
			t.Fatalf("%+v", c)
		}
	})
	t.Run("GitHub unreachable is not a verdict", func(t *testing.T) {
		g := newGitHubRig(t)
		g.srv.Close()
		c := g.check(t)
		if c.Verdict != appUnknown || !strings.Contains(c.Problem, "does not say the App is fine") {
			t.Fatalf("%+v", c)
		}
		g = newGitHubRig(t)
		g.status = 500
		if c = g.check(t); c.Verdict != appUnknown {
			t.Fatalf("a 500: %+v", c)
		}
	})
	t.Run("rejected credentials", func(t *testing.T) {
		g := newGitHubRig(t)
		g.status = 401
		c := g.check(t)
		if c.Verdict != appFailed || !strings.Contains(c.Problem, "App 12345") || !strings.Contains(c.Fix, "secrets set github-app-key --repo acme/app") {
			t.Fatalf("%+v", c)
		}
	})
	t.Run("the key cannot be read", func(t *testing.T) {
		for name, tc := range map[string]struct {
			err  error
			want string
		}{
			"no version": {gcp.ErrNoVersion, "not stored yet"},
			"no access":  {gcp.ErrNoAccess, "you may not read secret values"},
			"other":      {errors.New("connection reset"), "could not be read"},
		} {
			g := newGitHubRig(t)
			g.keyErr = tc.err
			c := g.check(t)
			if c.Verdict != appUnknown || !strings.Contains(c.Problem, tc.want) || len(g.auth) != 0 {
				t.Errorf("%s: %+v, GitHub asked %d times", name, c, len(g.auth))
			}
		}
	})
	t.Run("a stored value that is not a key is never echoed", func(t *testing.T) {
		g := newGitHubRig(t)
		g.key = []byte("hunter2-not-a-key")
		c := g.check(t)
		if c.Verdict != appFailed || strings.Contains(c.Problem+c.Fix, "hunter2") || len(g.auth) != 0 {
			t.Fatalf("%+v", c)
		}
	})
	t.Run("no secret reaches any output", func(t *testing.T) {
		for _, status := range []int{200, 404, 401, 500} {
			g := newGitHubRig(t)
			g.status = status
			if status == 200 {
				g.perms = map[string]string{"workflows": "write"}
			}
			c := g.check(t)
			all := c.Problem + "\n" + c.Fix + "\n" + strings.Join(c.Warnings, "\n")
			if g.leaks(t, all) {
				t.Errorf("status %d: the result holds the key or a JWT:\n%s", status, all)
			}
		}
	})
}

// The first billable build waits for the App: a failing pre-check leaves the
// builds (needs-you, with the reason and the fix) before anything is offered
// or submitted; a check that could not be made warns and the build is offered
// as before; a fine App goes straight on.
func TestFirstBuildWaitsForTheGitHubApp(t *testing.T) {
	spec := infra.RepoSpec{Name: "acme/app", Provider: "github", GitHubAppID: "12345", Secrets: map[string]string{"github-app-key": "app-key-secret"},
		BuildServiceAccountEmail: "b@x.iam", RegistryPath: "r"}
	cfg := &config.Config{Workflows: map[string]config.Workflow{"app": {Base: "web-node"}}}
	run := func(t *testing.T, g *githubRig) (*initRun, string, int) {
		t.Helper()
		fb := gcpfake.NewBuild(t)
		r, _, out := conditions()[6].run(t, "") // no terminal: an offered build is left, never typed
		r.o.checkApp = true
		lc := &localcfg.Config{Name: initProjectName, GCPProject: initProject, Region: "us-east5",
			BaseImages: map[string]string{"web-node": "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:1.2.3"},
			Endpoints:  localcfg.Endpoints{CloudBuild: fb.URL + "/", NoAuth: true}}
		built, err := r.offerBuilds(t.Context(), lc, cfg, spec, []string{"app"})
		if err != nil || built != 0 {
			t.Fatalf("built %d, %v", built, err)
		}
		posts := 0
		for _, rq := range fb.Requests() {
			if rq.Method == "POST" {
				posts++
			}
		}
		return r, out.String() + "\n" + strings.Join(r.res.Warnings, "\n"), posts
	}
	t.Run("not installed", func(t *testing.T) {
		g := newGitHubRig(t)
		g.status = 404
		r, out, posts := run(t, g)
		if posts != 0 || len(r.buildsLeft) != 1 || !strings.Contains(r.buildHold, "the GitHub App 12345 is not installed on acme/app") ||
			!strings.Contains(out, "install it on this repository only") || !strings.Contains(out, "nothing was billed") || strings.Contains(out, "billable and was not confirmed") {
			t.Fatalf("posts %d left %v hold %q\n%s", posts, r.buildsLeft, r.buildHold, out)
		}
	})
	t.Run("missing permission", func(t *testing.T) {
		g := newGitHubRig(t)
		g.perms = map[string]string{"contents": "write", "pull_requests": "write", "metadata": "read"}
		r, out, _ := run(t, g)
		if !strings.Contains(r.buildHold, "Issues: Read") || !strings.Contains(out, "add Issues: Read, then accept the new permission on the installation") {
			t.Fatalf("hold %q\n%s", r.buildHold, out)
		}
	})
	t.Run("could not check", func(t *testing.T) {
		g := newGitHubRig(t)
		g.status = 500
		r, out, _ := run(t, g)
		// Not claimed fine, not blocked: a warning, and the build is offered
		// (and left only because this run cannot type the confirmation).
		if r.buildHold != "" || len(r.buildsLeft) != 1 || !strings.Contains(out, "does not say the App is fine") || !strings.Contains(out, "billable and was not confirmed") {
			t.Fatalf("hold %q\n%s", r.buildHold, out)
		}
	})
	t.Run("fine", func(t *testing.T) {
		g := newGitHubRig(t)
		r, out, _ := run(t, g)
		if r.buildHold != "" || len(g.auth) != 1 || strings.Contains(out, "GitHub App") {
			t.Fatalf("hold %q asked %d\n%s", r.buildHold, len(g.auth), out)
		}
		if g.leaks(t, out) {
			t.Errorf("output holds a secret:\n%s", out)
		}
	})
	t.Run("not GitHub", func(t *testing.T) {
		g := newGitHubRig(t)
		g.status = 404
		spec := spec
		spec.Provider = "bitbucket"
		fb := gcpfake.NewBuild(t)
		r, _, _ := conditions()[6].run(t, "")
		r.o.checkApp = true
		lc := &localcfg.Config{Name: initProjectName, GCPProject: initProject, Region: "us-east5",
			BaseImages: map[string]string{"web-node": "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:1.2.3"},
			Endpoints:  localcfg.Endpoints{CloudBuild: fb.URL + "/", NoAuth: true}}
		if _, err := r.offerBuilds(t.Context(), lc, cfg, spec, []string{"app"}); err != nil || len(g.auth) != 0 || r.buildHold != "" {
			t.Errorf("err %v, GitHub asked %d times, hold %q", err, len(g.auth), r.buildHold)
		}
	})
}

// The key is never read from Secret Manager without --check-github-app: the
// check is then "not made" and says how to make it, and nothing is read or
// sent. The key typed in this run is used from memory, with no read either.
func TestGitHubAppKeyIsReadOnlyBehindTheFlag(t *testing.T) {
	g := newGitHubRig(t)
	c := g.checkWith(t, appKeySource{SecretID: "app-key-secret"})
	if c.Verdict != appUnknown || g.reads != 0 || len(g.auth) != 0 ||
		!strings.Contains(c.Problem, "rerun with --check-github-app (reads the key with your own credentials) or run") || !strings.Contains(c.Problem, "doctor --check-github-app in your own terminal") {
		t.Fatalf("%+v reads %d GitHub asked %d", c, g.reads, len(g.auth))
	}
	// From this run's memory: checked, nothing read, the caller's copy kept.
	mem := append([]byte(nil), appKey(t)...)
	c = g.checkWith(t, appKeySource{Mem: mem, SecretID: "app-key-secret"})
	if c.Verdict != appFine || g.reads != 0 || len(g.auth) != 1 || string(mem) != string(appKey(t)) {
		t.Fatalf("%+v reads %d asked %d", c, g.reads, len(g.auth))
	}
	// The notice comes before the read, and only with the flag.
	var notice strings.Builder
	g.checkWith(t, appKeySource{Read: true, SecretID: "app-key-secret", Notice: &notice})
	for _, want := range []string{"reads the App's private key with your own credentials", "only the PEM byte copy is cleared", "garbage collection", "GODEBUG=http2debug"} {
		if !strings.Contains(notice.String(), want) {
			t.Errorf("the notice lacks %q: %s", want, notice.String())
		}
	}
	if strings.Contains(notice.String(), "\n\n") || strings.Count(strings.TrimSpace(notice.String()), "\n") != 0 || g.reads != 1 {
		t.Fatalf("notice %q reads %d", notice.String(), g.reads)
	}
}

// Through init: without the flag, a key stored in an earlier run is not read
// (the build is offered as before, with the warning); the key typed in this
// run is used.
func TestInitPreCheckWithoutFlagReadsNothing(t *testing.T) {
	g := newGitHubRig(t)
	spec := infra.RepoSpec{Name: "acme/app", Provider: "github", GitHubAppID: "12345", Secrets: map[string]string{"github-app-key": "app-key-secret"},
		BuildServiceAccountEmail: "b@x.iam", RegistryPath: "r"}
	cfg := &config.Config{Workflows: map[string]config.Workflow{"app": {Base: "web-node"}}}
	fb := gcpfake.NewBuild(t)
	lc := &localcfg.Config{Name: initProjectName, GCPProject: initProject, Region: "us-east5",
		BaseImages: map[string]string{"web-node": "us-east5-docker.pkg.dev/proj-1234/fugaro-base/fugaro-web-node:1.2.3"},
		Endpoints:  localcfg.Endpoints{CloudBuild: fb.URL + "/", NoAuth: true}}
	r, _, out := conditions()[6].run(t, "")
	if _, err := r.offerBuilds(t.Context(), lc, cfg, spec, []string{"app"}); err != nil {
		t.Fatal(err)
	}
	if g.reads != 0 || len(g.auth) != 0 || r.buildHold != "" || !strings.Contains(out.String()+strings.Join(r.res.Warnings, "\n"), "rerun with --check-github-app") {
		t.Fatalf("reads %d asked %d hold %q\n%s %v", g.reads, len(g.auth), r.buildHold, out.String(), r.res.Warnings)
	}
	r, _, _ = conditions()[6].run(t, "")
	r.appKeyMem = append([]byte(nil), appKey(t)...)
	held := r.appKeyMem
	g.status = 404
	defer func() {
		// The typed copy is cleared right after the check, not at the run's end.
		if r.appKeyMem != nil || strings.Trim(string(held), "\x00") != "" {
			t.Errorf("the typed key copy was not cleared after the pre-check")
		}
	}()
	if _, err := r.offerBuilds(t.Context(), lc, cfg, spec, []string{"app"}); err != nil || g.reads != 0 || len(g.auth) != 1 || !strings.Contains(r.buildHold, "not installed") {
		t.Fatalf("typed key: %v reads %d asked %d hold %q", err, g.reads, len(g.auth), r.buildHold)
	}
}

// --check-github-app is refused under a coding agent's marker, by init and by
// doctor, before anything is read or sent.
func TestCheckGitHubAppRefusedInAgentSession(t *testing.T) {
	for _, marker := range agentMarkers {
		t.Run(marker, func(t *testing.T) {
			g := newGitHubRig(t)
			t.Setenv(marker, "1")
			for _, args := range [][]string{{"init", "--check-github-app", "--plan-only"}, {"init", "--repo", "--check-github-app", "--plan-only"}, {"doctor", "--check-github-app", "--json"}} {
				_, _, err := execute(t, args...)
				if err == nil || !strings.Contains(err.Error(), "refused in a coding agent's session") || ExitCode(err) != ExitUserError {
					t.Errorf("%v: %v", args, err)
				}
			}
			if g.reads != 0 || len(g.auth) != 0 {
				t.Errorf("the key was read (%d) or GitHub asked (%d)", g.reads, len(g.auth))
			}
		})
	}
}

// A repository name that is not owner/name never reaches the key or GitHub.
func TestGitHubAppCheckRefusesOddRepoNames(t *testing.T) {
	g := newGitHubRig(t)
	for _, repo := range []string{"acme/..", "../app", "a%2Fb/x", "acme/app/x"} {
		c := checkGitHubApp(t.Context(), &localcfg.Config{Name: "aurora", GCPProject: initProject}, repo, "12345", appKeySource{Read: true, SecretID: "app-key-secret"})
		if c.Verdict != appUnknown {
			t.Errorf("%s: %+v", repo, c)
		}
	}
	if g.reads != 0 || len(g.auth) != 0 {
		t.Errorf("reads %d, GitHub asked %d", g.reads, len(g.auth))
	}
}
