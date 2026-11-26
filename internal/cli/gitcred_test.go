package cli

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/buildtoken"
	"github.com/dimipaun/fugaro/internal/credsvc"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/gitprov/github"
	"github.com/dimipaun/fugaro/internal/gitprov/httpfixture"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// gitCredentialEnv clears every variable git-credential reads, so the
// developer's environment can't leak into a test. The four in the second list
// are the fetch's own (B4.5): the credential step of a switched repository is
// handed them by Cloud Build, and a test that left the developer's environment
// standing could switch a case that means to build from a mounted secret.
func gitCredentialEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"GIT_TOKEN", "GIT_USER", "GITHUB_APP_KEY", "GITHUB_APP_ID", "FUGARO_GITHUB_API_URL",
		envCredentialsServiceURL, gitCredWorkflowEnv, gitCredSecretEnvsEnv, gitCredSecretDirEnv} {
		t.Setenv(k, "")
	}
}

// readCredential asks real git for the credential of host in file.
func readCredential(t *testing.T, file, host string) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	get := exec.Command(git, "credential-store", "--file="+file, "get")
	get.Stdin = strings.NewReader("protocol=https\nhost=" + host + "\n\n")
	out, err := get.Output()
	if err != nil {
		t.Fatalf("git credential-store get: %v", err)
	}
	return string(out)
}

func TestGitCredentialBitbucket(t *testing.T) {
	testutil.IsolateGit(t)
	gitCredentialEnv(t)
	const token = "t/o@k:e%n+ 'x\"y"
	t.Setenv("GIT_TOKEN", token)
	t.Setenv("GIT_USER", "x-token-auth")
	out := filepath.Join(t.TempDir(), "git-credentials")
	stdout, stderr, err := execute(t, "image", "git-credential", "--provider", "bitbucket", "--repo-url", "https://bitbucket.org/acme/app.git", "--out", out)
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "" || stderr != "" {
		t.Errorf("git-credential printed %q / %q", stdout, stderr)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://x-token-auth:t%2Fo%40k%3Ae%25n%2B%20%27x%22y@bitbucket.org\n"; string(data) != want {
		t.Errorf("line = %q, want %q", data, want)
	}
	if fi, err := os.Stat(out); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, %v", fi.Mode(), err)
	}
	if got := readCredential(t, out, "bitbucket.org"); !strings.Contains(got, "username=x-token-auth\n") || !strings.Contains(got, "password="+token+"\n") {
		t.Errorf("git read back %q", got)
	}
	// A file that is already there, with a looser mode, is replaced and
	// tightened.
	if err := os.Chmod(out, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "image", "git-credential", "--provider", "bitbucket", "--repo-url", "https://bitbucket.org/acme/app.git", "--out", out); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(out); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode after a rewrite = %v", fi.Mode())
	}

	t.Setenv("GIT_TOKEN", "tok\nen")
	if _, _, err := execute(t, "image", "git-credential", "--provider", "bitbucket", "--repo-url", "https://bitbucket.org/acme/app.git", "--out", out); ExitCode(err) != ExitUserError || strings.Contains(err.Error(), "tok") {
		t.Errorf("a token with a newline: exit %d, %v", ExitCode(err), err)
	}
	t.Setenv("GIT_TOKEN", "")
	if _, _, err := execute(t, "image", "git-credential", "--provider", "bitbucket", "--repo-url", "https://bitbucket.org/acme/app.git", "--out", out); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "GIT_TOKEN") {
		t.Errorf("no token: exit %d, %v", ExitCode(err), err)
	}
}

// TestGitCredentialRefusesAnOutThatKeptNothing: the step reads back what it
// wrote, so a step that wrote none fails where the step that owed the
// credential is named. Without it the build runs on and clones with an empty
// file, answering `could not read Username for 'https://github.com'` — a
// sentence naming neither the credential nor the build that failed to write it
// (check 390).
func TestGitCredentialRefusesAnOutThatKeptNothing(t *testing.T) {
	testutil.IsolateGit(t)
	line := "https://x-token-auth:tok3n@bitbucket.org\n"
	empty := filepath.Join(t.TempDir(), "git-credentials")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err := credentialLanded(empty, line)
	if ExitCode(err) != ExitUserError {
		t.Fatalf("a credential the out kept nothing of: exit %d, %v", ExitCode(err), err)
	}
	if !strings.Contains(err.Error(), "holds no credential after writing it") || strings.Contains(err.Error(), "tok3n") {
		t.Errorf("the refusal does not name what happened, or prints the token: %v", err)
	}
	// The control: the line the step wrote is what a build reads.
	wrote := filepath.Join(t.TempDir(), "git-credentials")
	if err := os.WriteFile(wrote, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := credentialLanded(wrote, line); err != nil {
		t.Errorf("the credential the step wrote was refused: %v", err)
	}
}

func appKeyPEM(t *testing.T) string {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}))
}

// TestGitCredentialGitHubMints: for GitHub the step mints an installation
// token that can only read the one repository; the fixture fails the test
// on any other permissions or repositories.
func TestGitCredentialGitHubMints(t *testing.T) {
	testutil.IsolateGit(t)
	gitCredentialEnv(t)
	srv := httpfixture.Serve(t, filepath.Join("testdata", "github_build_token.json"))
	t.Setenv("GITHUB_APP_KEY", appKeyPEM(t))
	t.Setenv("GITHUB_APP_ID", "12345")
	defer func(u string) { gitCredGitHubAPI = u }(gitCredGitHubAPI)
	gitCredGitHubAPI = srv.URL
	out := filepath.Join(t.TempDir(), "git-credentials")
	stdout, stderr, err := execute(t, "image", "git-credential", "--provider", "github", "--repo-url", "https://github.com/acme/webapp.git", "--out", out)
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "" || stderr != "" {
		t.Errorf("git-credential printed %q / %q", stdout, stderr)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://x-access-token:ghs_fixture%2Fbuild%2Btoken@github.com\n"; string(data) != want {
		t.Errorf("line = %q, want %q", data, want)
	}
	if got := readCredential(t, out, "github.com"); !strings.Contains(got, "password=ghs_fixture/build+token\n") {
		t.Errorf("git read back %q", got)
	}

	t.Setenv("GITHUB_APP_ID", "")
	if _, _, err := execute(t, "image", "git-credential", "--provider", "github", "--repo-url", "https://github.com/acme/webapp.git", "--out", out); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "GITHUB_APP_ID") {
		t.Errorf("no App ID: exit %d, %v", ExitCode(err), err)
	}
}

// TestGitCredentialRefusesForeignHost: the credential is only ever for the
// provider's own host, over https, at a URL that carries none already.
func TestGitCredentialRefusesForeignHost(t *testing.T) {
	gitCredentialEnv(t)
	t.Setenv("GIT_TOKEN", "tok")
	t.Setenv("GIT_USER", "x-token-auth")
	t.Setenv("GITHUB_APP_KEY", "not a key")
	t.Setenv("GITHUB_APP_ID", "12345")
	dir := t.TempDir()
	for _, c := range []struct{ provider, url string }{
		{"bitbucket", "https://evil.example/acme/app.git"},
		{"bitbucket", "https://bitbucket.org.evil.example/acme/app.git"},
		{"bitbucket", "https://bitbucket.org:8443/acme/app.git"},
		{"bitbucket", "http://bitbucket.org/acme/app.git"},
		{"bitbucket", "https://user:pw@bitbucket.org/acme/app.git"},
		{"bitbucket", "https://github.com/acme/app.git"},
		{"github", "https://bitbucket.org/acme/app.git"},
		{"github", "https://github.com/acme"},
		{"gitlab", "https://gitlab.com/acme/app.git"},
	} {
		out := filepath.Join(dir, "git-credentials")
		_, _, err := execute(t, "image", "git-credential", "--provider", c.provider, "--repo-url", c.url, "--out", out)
		if ExitCode(err) != ExitUserError {
			t.Errorf("%s %s: exit %d, %v", c.provider, c.url, ExitCode(err), err)
		}
		if err != nil && strings.Contains(err.Error(), "pw") {
			t.Errorf("the error shows the URL's password: %v", err)
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Errorf("%s %s: a credential file was written", c.provider, c.url)
		}
	}
}

// TestGitCredentialIgnoresAPIOverride: the step holds the App's private
// key, so its JWT only ever goes to GitHub's own API, whatever the step's
// environment says.
func TestGitCredentialIgnoresAPIOverride(t *testing.T) {
	gitCredentialEnv(t)
	if gitCredGitHubAPI != "https://api.github.com" {
		t.Fatalf("the credential step's GitHub API is %s", gitCredGitHubAPI)
	}
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true; http.Error(w, "no", http.StatusTeapot) }))
	defer srv.Close()
	t.Setenv("FUGARO_GITHUB_API_URL", srv.URL)
	t.Setenv("GITHUB_APP_KEY", appKeyPEM(t))
	t.Setenv("GITHUB_APP_ID", "12345")
	// A GitHub API that refuses every connection stands in for the real
	// one, so the test never reaches the network.
	defer func(u string) { gitCredGitHubAPI = u }(gitCredGitHubAPI)
	gitCredGitHubAPI = "http://127.0.0.1:1"
	out := filepath.Join(t.TempDir(), "git-credentials")
	if _, _, err := execute(t, "image", "git-credential", "--provider", "github", "--repo-url", "https://github.com/acme/webapp.git", "--out", out); err == nil {
		t.Fatal("minted without reaching GitHub")
	}
	if hit {
		t.Error("the App JWT went to FUGARO_GITHUB_API_URL")
	}
}

// buildCredentialsService is a stand-in for the installation's credentials
// service on B4.3's build route: it serves one answer the test names, records
// what each call asked for and what identity token it presented, and refuses
// every other method and path — so a step that asked the wrong route, asked
// with no token or asked twice is caught rather than assumed.
//
// It is the shape of the route and not a model of the service: what the service
// decides (which repository a build account is bound to, which containers the
// record holds) is internal/credsvc's own, and this stands in for its answer
// alone.
type buildCredentialsService struct {
	mu       sync.Mutex
	requests []credsvc.BuildCredentialsRequest
	bearers  []string
	answer   credsvc.BuildCredentialsResponse
	// status and body are the refusal this service answers instead of the
	// answer above; status 0 is a 200.
	status int
	body   string
}

func (s *buildCredentialsService) start(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != credsvc.BuildCredentialsPath {
			http.Error(w, "not a route of this service", http.StatusNotFound)
			return
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "unreadable", http.StatusBadRequest)
			return
		}
		var req credsvc.BuildCredentialsRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			http.Error(w, "not this contract's body", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.requests = append(s.requests, req)
		s.bearers = append(s.bearers, r.Header.Get("Authorization"))
		status, failure, answer := s.status, s.body, s.answer
		s.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, failure)
			return
		}
		out, err := json.Marshal(answer)
		if err != nil {
			http.Error(w, "unbuildable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(out)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (s *buildCredentialsService) asked() []credsvc.BuildCredentialsRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

func (s *buildCredentialsService) presented() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.bearers)
}

// buildFetchEnv is the environment the credential step of a build that fetches
// from the service is handed: the service's address, the workflow whose secrets
// the build mounts, the variables it mounts them under and the volume the
// fetched values are left in. Every one reaches the step as a variable of the
// build's own environment and never as a flag. It answers the volume's path.
func buildFetchEnv(t *testing.T, serviceURL, workflow, secretEnvs string) string {
	t.Helper()
	t.Setenv(envCredentialsServiceURL, serviceURL)
	t.Setenv(gitCredWorkflowEnv, workflow)
	t.Setenv(gitCredSecretEnvsEnv, secretEnvs)
	dir := t.TempDir()
	t.Setenv(gitCredSecretDirEnv, dir)
	return dir
}

// TestGitCredentialFetchesFromTheServiceWhenSet: with
// FUGARO_CREDENTIALS_SERVICE_URL set, the credential step fetches the build's
// git credential AND the workflow secrets the build mounts from B4.3's build
// route, presenting the build's own identity token minted at the metadata
// server, and reads neither GIT_TOKEN nor GITHUB_APP_KEY: the mounted secret is
// a decoy the answer has to beat, which is what a step that fell back to its
// environment fails on.
//
// The token it presents is the build account's Google identity token, the one
// Cloud Run IAM admits the call with and the route verifies its caller by, and
// it is minted per call. The App ID is the one thing this step holds and the
// service does not, so a GitHub build sends it and a Bitbucket one signs
// nothing.
//
// Mutation (verified, run and restored): drop the branch of runGitCredential
// that reads envCredentialsServiceURL and this fails on every case — the file
// holds the mounted decoy and the service is never asked.
func TestGitCredentialFetchesFromTheServiceWhenSet(t *testing.T) {
	const (
		token  = "service-token-not-a-real-credential"
		secret = "service-secret-not-a-real-credential"
		mount  = "mounted-token-not-a-real-credential"
	)
	for _, c := range []struct{ provider, host, user, repoURL, appID string }{
		{gitprov.KindBitbucket, "bitbucket.org", "x-token-auth", "https://bitbucket.org/acme/app.git", ""},
		{gitprov.KindGitHub, "github.com", github.GitUsername, "https://github.com/acme/webapp.git", "12345"},
	} {
		t.Run(c.provider, func(t *testing.T) {
			testutil.IsolateGit(t)
			gitCredentialEnv(t)
			meta := buildCallMetadata(t)
			svc := &buildCredentialsService{answer: credsvc.BuildCredentialsResponse{
				Git: credsvc.BuildGitCredential{Provider: c.provider, Username: c.user, Token: token, Host: c.host},
				Secrets: []credsvc.BuildSecretValue{
					{Env: "NPM_TOKEN", Value: secret},
					{Env: "PUBLISH_TOKEN", Value: secret},
				},
			}}
			srv := svc.start(t)
			dir := buildFetchEnv(t, srv.URL, "web", "NPM_TOKEN PUBLISH_TOKEN")
			// The step's environment still carries the mounted provider secret,
			// as it does until B4.9 drops the mount: a build that reads it is a
			// build that did not fetch.
			t.Setenv(gitCredTokenEnv, mount)
			t.Setenv(gitCredUserEnv, "x-token-auth")
			t.Setenv(gitCredAppIDEnv, c.appID)
			out := filepath.Join(t.TempDir(), "git-credentials")
			stdout, stderr, err := execute(t, "image", "git-credential", "--provider", c.provider, "--repo-url", c.repoURL, "--out", out)
			if err != nil {
				t.Fatal(err)
			}
			if stdout != "" || stderr != "" {
				t.Errorf("git-credential printed %q / %q", stdout, stderr)
			}
			data, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if want := "https://" + credEscape(c.user) + ":" + credEscape(token) + "@" + c.host + "\n"; string(data) != want {
				t.Errorf("line = %q, want the served credential %q and not the mounted one", data, want)
			}
			if fi, err := os.Stat(out); err != nil || fi.Mode().Perm() != 0o600 {
				t.Errorf("mode = %v, %v", fi.Mode(), err)
			}
			for _, env := range []string{"NPM_TOKEN", "PUBLISH_TOKEN"} {
				path := filepath.Join(dir, env)
				got, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("%s: %v: the build step mounts this file and refuses a build without it", env, err)
				}
				if string(got) != secret {
					t.Errorf("%s = %q, want the served value", env, got)
				}
				if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
					t.Errorf("%s mode = %v, %v", env, fi.Mode(), err)
				}
			}
			asked := svc.asked()
			if len(asked) != 1 {
				t.Fatalf("the step asked the service %d times, want the one call a build makes", len(asked))
			}
			if asked[0].Workflow != "web" || !slices.Equal(asked[0].Secrets, []string{"NPM_TOKEN", "PUBLISH_TOKEN"}) || asked[0].AppID != c.appID {
				t.Errorf("the step asked %+v, want the workflow %q, the variables the build mounts and the App ID %q", asked[0], "web", c.appID)
			}
			if bears := svc.presented(); len(bears) != 1 || bears[0] != "Bearer "+buildE2EIdentityToken {
				t.Errorf("the step presented %q, want the build account's own identity token under Bearer", bears)
			}
			if got := meta.count(); got != 1 {
				t.Errorf("the metadata server was read %d times, want the one mint a call makes", got)
			}
		})
	}
}

// TestGitCredentialUnchangedWithoutTheService: an installation that has not
// switched a repository sets no address — the template substitutes the empty
// value, and a base image whose fugaro predates the service ignores the
// fetch's variables entirely — so the step reads the secret it was mounted,
// exactly as it did before, and asks the service nothing: no call, no identity
// token, and no secret file written into the volume the build step reads.
//
// This is the previous-version installation pinned: a build on an installation
// whose service is not deployed, or whose repository has not been switched,
// still builds.
func TestGitCredentialUnchangedWithoutTheService(t *testing.T) {
	const (
		mount = "mounted-token-not-a-real-credential"
		other = "service-token-not-a-real-credential"
	)
	testutil.IsolateGit(t)
	gitCredentialEnv(t)
	meta := buildCallMetadata(t)
	svc := &buildCredentialsService{answer: credsvc.BuildCredentialsResponse{
		Git: credsvc.BuildGitCredential{Provider: gitprov.KindBitbucket, Username: "x-token-auth", Token: other, Host: "bitbucket.org"},
	}}
	srv := svc.start(t)
	dir := buildFetchEnv(t, "", "web", "NPM_TOKEN")
	t.Setenv(gitCredTokenEnv, mount)
	t.Setenv(gitCredUserEnv, "x-token-auth")
	out := filepath.Join(t.TempDir(), "git-credentials")
	stdout, stderr, err := execute(t, "image", "git-credential", "--provider", gitprov.KindBitbucket, "--repo-url", "https://bitbucket.org/acme/app.git", "--out", out)
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "" || stderr != "" {
		t.Errorf("git-credential printed %q / %q", stdout, stderr)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://x-token-auth:" + credEscape(mount) + "@bitbucket.org\n"; string(data) != want {
		t.Errorf("line = %q, want the mounted credential %q", data, want)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Errorf("the step left %d entries in %s (%v), want none: nothing was fetched", len(entries), gitCredSecretDirEnv, err)
	}
	if got := len(svc.asked()); got != 0 {
		t.Errorf("the step asked the service %d times with no address set, want none", got)
	}
	if got := meta.count(); got != 0 {
		t.Errorf("the step minted its identity %d times with no address set, want none", got)
	}
	_ = srv
}

// TestGitCredentialNeverPrintsTheValue: the credential and the workflow secrets
// the step fetches reach their files and nowhere else — not the command's
// output, and not a refusal. Every refusal that comes after the answer was read
// is this file's own sentence and names the field it refused, because a build's
// log is the one place a credential a build holds becomes everyone's.
//
// Each case's answer is complete in everything but the one thing it is refused
// for, and each case pins the sentence it was refused with: an answer holding a
// secret the build did not ask for is refused by writeBuildSecrets whatever its
// credential is, so a case whose answer is short of its secret is refused by
// that guard and says nothing about the credential's own.
func TestGitCredentialNeverPrintsTheValue(t *testing.T) {
	const (
		token  = "service-token-not-a-real-credential"
		secret = "service-secret-not-a-real-credential"
	)
	served := func() credsvc.BuildGitCredential {
		return credsvc.BuildGitCredential{Provider: gitprov.KindBitbucket, Username: "x-token-auth", Token: token, Host: "bitbucket.org"}
	}
	// The secret the case below mounts, so the answer is refused for its
	// credential alone and never for a short answer.
	mounted := []credsvc.BuildSecretValue{{Env: "NPM_TOKEN", Value: secret}}
	testutil.IsolateGit(t)
	gitCredentialEnv(t)
	buildCallMetadata(t)
	for _, c := range []struct {
		name   string
		answer credsvc.BuildCredentialsResponse
		reason string
	}{
		{"a credential for another provider", credsvc.BuildCredentialsResponse{
			Git:     credsvc.BuildGitCredential{Provider: gitprov.KindGitHub, Username: github.GitUsername, Token: token, Host: "bitbucket.org"},
			Secrets: mounted,
		}, "answered a credential for the provider github"},
		{"a credential for another host", credsvc.BuildCredentialsResponse{
			Git:     credsvc.BuildGitCredential{Provider: gitprov.KindBitbucket, Username: "x-token-auth", Token: token, Host: "evil.example"},
			Secrets: mounted,
		}, "answered a credential for the host evil.example"},
		{"a credential holding a control character", credsvc.BuildCredentialsResponse{
			Git:     credsvc.BuildGitCredential{Provider: gitprov.KindBitbucket, Username: "x-token-auth", Token: token + "\n", Host: "bitbucket.org"},
			Secrets: mounted,
		}, "holds a control character"},
		{"a secret the build does not mount", credsvc.BuildCredentialsResponse{
			Git:     served(),
			Secrets: []credsvc.BuildSecretValue{{Env: "NOT_MOUNTED", Value: secret}},
		}, "which this build does not mount"},
		{"a secret answered twice", credsvc.BuildCredentialsResponse{
			Git:     served(),
			Secrets: []credsvc.BuildSecretValue{{Env: "NPM_TOKEN", Value: secret}, {Env: "NPM_TOKEN", Value: secret}},
		}, "answered NPM_TOKEN twice"},
	} {
		t.Run(c.name, func(t *testing.T) {
			svc := &buildCredentialsService{answer: c.answer}
			srv := svc.start(t)
			dir := buildFetchEnv(t, srv.URL, "web", "NPM_TOKEN")
			// The mounted secret is a decoy, so a step that never asked the
			// service cannot pass by refusing on its empty environment instead.
			t.Setenv(gitCredTokenEnv, "mounted-token-not-a-real-credential")
			t.Setenv(gitCredUserEnv, "x-token-auth")
			out := filepath.Join(t.TempDir(), "git-credentials")
			stdout, stderr, err := execute(t, "image", "git-credential", "--provider", gitprov.KindBitbucket, "--repo-url", "https://bitbucket.org/acme/app.git", "--out", out)
			if err == nil {
				t.Fatalf("the step wrote a credential out of %s", c.name)
			}
			if !strings.Contains(err.Error(), c.reason) {
				t.Errorf("the step refused %s with %v, want the refusal naming %q", c.name, err, c.reason)
			}
			if got := len(svc.asked()); got != 1 {
				t.Fatalf("the step asked the service %d times, want the one call whose answer it refused (err: %v)", got, err)
			}
			for _, value := range []string{token, secret} {
				if strings.Contains(err.Error(), value) {
					t.Errorf("the refusal repeats what the service answered: %v", err)
				}
				if strings.Contains(stdout+stderr, value) {
					t.Errorf("the step printed %q / %q", stdout, stderr)
				}
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Errorf("a refused call wrote a credential file")
			}
			if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
				t.Errorf("a refused call left %d entries in %s (%v), want none", len(entries), gitCredSecretDirEnv, err)
			}
		})
	}
}

// buildTokenRoute is a stand-in for the installation's credentials service on
// the entry route (`/v1/build/token`): it serves one answer the test names,
// records what each call asked for and what identity token it presented, and
// refuses every other method and path — so a step that asked the wrong route,
// asked with no token or asked twice is caught rather than assumed. It is the
// shape of the route and not a model of the service: what the service decides
// (which repositories the entry's own image map puts on a build) is
// internal/credsvc's own, and this stands in for its answer alone.
type buildTokenRoute struct {
	mu       sync.Mutex
	requests []buildtoken.Request
	bearers  []string
	answer   buildtoken.Response
}

func (s *buildTokenRoute) start(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != credsvc.BuildTokenPath {
			http.Error(w, "not a route of this service", http.StatusNotFound)
			return
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "unreadable", http.StatusBadRequest)
			return
		}
		var req buildtoken.Request
		if err := json.Unmarshal(raw, &req); err != nil {
			http.Error(w, "not this contract's body", http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.requests = append(s.requests, req)
		s.bearers = append(s.bearers, r.Header.Get("Authorization"))
		answer := s.answer
		s.mu.Unlock()
		out, err := json.Marshal(answer)
		if err != nil {
			http.Error(w, "unbuildable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(out)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (s *buildTokenRoute) asked() []buildtoken.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

func (s *buildTokenRoute) presented() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.bearers)
}

// TestCatalogGitCredentialFromService is the catalog build's credential step
// end to end: with FUGARO_CREDENTIALS_SERVICE_URL set the step no longer reads
// a secret of the entry's at all — the catalog build names none (IS8, D3330) —
// and asks the installation's service for the one credential it clones the
// entry's repositories with, presenting the build's own identity token minted
// at the metadata server. It is the entry route a catalog build asks, not the
// repository route a repository's build asks, and the request is the entry,
// the build's own id, the provider, the repositories the bake composed stages
// for and how long the step needs them.
//
// A Bitbucket build is answered a proxy grant and no token (R6), and this step
// writes only a token: it refuses and writes nothing, so the build fails at its
// credential step rather than cloning with a placeholder. The refusal is proven
// here with the same route and the same identity a GitHub case passes with, so
// what is refused is the answer's shape and not the wiring.
//
// Mutation (verified, run and restored): make writeBuildToken accept a grant
// (write the grant's URL as a credential) and the Bitbucket case writes a file
// and fails; drop the entry mode's canonicalization of --repos and the request
// names Acme/App rather than acme/app, which the service refuses by name.
func TestCatalogGitCredentialFromService(t *testing.T) {
	const (
		token = "entry-token-not-a-real-credential"
		ttl   = 3600
	)
	t.Run("github", func(t *testing.T) {
		testutil.IsolateGit(t)
		gitCredentialEnv(t)
		meta := buildCallMetadata(t)
		t.Setenv(gitCredAppIDEnv, "12345")
		route := &buildTokenRoute{answer: buildtoken.Response{Token: &buildtoken.Token{
			Value: token, User: github.GitUsername, Host: "github.com",
			ExpiresAt: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC),
		}}}
		srv := route.start(t)
		t.Setenv(envCredentialsServiceURL, srv.URL)
		out := filepath.Join(t.TempDir(), "git-credentials")
		stdout, stderr, err := execute(t, "image", "git-credential",
			"--entry", "agent", "--build-id", "b-1", "--provider", gitprov.KindGitHub,
			// Two spellings of one repository, so what the request carries is
			// the canonical name once (the key space the service reads the
			// entry's own from).
			"--repos", "Acme/App,acme/app", "--ttl-seconds", strconv.Itoa(ttl), "--out", out)
		if err != nil {
			t.Fatalf("the catalog build's credential step failed: %v", err)
		}
		asked := route.asked()
		if len(asked) != 1 {
			t.Fatalf("the step asked the service %d times, want the one request it is", len(asked))
		}
		req := asked[0]
		if req.Entry != "agent" || req.BuildID != "b-1" || req.Provider != gitprov.KindGitHub || req.TTLSeconds != ttl {
			t.Errorf("the step asked for %+v, want the entry being built, this build's own id, its provider and the lifetime it needs", req)
		}
		if len(req.Repositories) != 1 || req.Repositories[0] != "acme/app" {
			t.Errorf("the step named the repositories %v, want acme/app once: the entry route reads them against the entry's own map", req.Repositories)
		}
		if presented := route.presented(); len(presented) != 1 || !strings.HasPrefix(presented[0], "Bearer ") {
			t.Errorf("the step presented %v, want the build's own identity token as the bearer", presented)
		}
		if meta.count() != 1 {
			t.Errorf("the metadata server was read %d times for one call, want the one mint a call makes", meta.count())
		}
		got := readCredential(t, out, "github.com")
		if !strings.Contains(got, "password="+token) {
			t.Errorf("the step wrote %q, want the credential the service answered", got)
		}
		if strings.Contains(stdout+stderr, token) {
			t.Errorf("the step printed what it wrote: %q / %q", stdout, stderr)
		}
	})
	t.Run("bitbucket", func(t *testing.T) {
		grant := func(srv string, mutate func(*buildtoken.ProxyGrant)) buildtoken.Response {
			g := &buildtoken.ProxyGrant{
				URL: srv + credsvc.BitbucketProxyPrefix, Repositories: []string{"acme/app"},
				ExpiresAt: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC),
			}
			if mutate != nil {
				mutate(g)
			}
			return buildtoken.Response{Proxy: g}
		}
		run := func(t *testing.T, mutate func(*buildtoken.ProxyGrant)) (string, error) {
			t.Helper()
			testutil.IsolateGit(t)
			gitCredentialEnv(t)
			buildCallMetadata(t)
			route := &buildTokenRoute{}
			srv := route.start(t)
			route.answer = grant(srv.URL, mutate)
			t.Setenv(envCredentialsServiceURL, srv.URL)
			// The mounted token is a decoy: a step that fell back to its
			// environment instead of the grant would pass on it.
			t.Setenv(gitCredTokenEnv, "mounted-token-not-a-real-credential")
			t.Setenv(gitCredUserEnv, "x-token-auth")
			out := filepath.Join(t.TempDir(), "git-credentials")
			_, _, err := execute(t, "image", "git-credential",
				"--entry", "agent", "--build-id", "b-1", "--provider", gitprov.KindBitbucket,
				"--repos", "acme/app", "--ttl-seconds", strconv.Itoa(ttl), "--out", out)
			if err != nil {
				if _, serr := os.Stat(out); !os.IsNotExist(serr) {
					t.Errorf("a refused build wrote a credential file")
				}
				return "", err
			}
			got, rerr := os.ReadFile(out)
			if rerr != nil {
				t.Fatal(rerr)
			}
			return string(got), nil
		}
		t.Run("the grant is written as the one rewrite, with no token", func(t *testing.T) {
			got, err := run(t, nil)
			if err != nil {
				t.Fatalf("the step refused its own installation's grant: %v", err)
			}
			if !strings.HasPrefix(got, "[url \"http://127.0.0.1:") || !strings.HasSuffix(got, credsvc.BitbucketProxyPrefix+"\"]\n\tinsteadOf = https://bitbucket.org/\n") || strings.Count(got, "\n") != 2 {
				t.Errorf("the step wrote %q, want the one insteadOf section for the proxy and nothing else", got)
			}
			if strings.Contains(got, "mounted-token") {
				t.Errorf("the step wrote the mounted token: %q", got)
			}
		})
		// Mutation (verified, run and restored): compare the grant's URL by its
		// path alone and the other-host case writes a rewrite to a host that
		// would be offered the build's identity; drop the repository set check and
		// the wider case does too.
		for _, c := range []struct {
			name   string
			mutate func(*buildtoken.ProxyGrant)
			want   string
		}{
			{"another host", func(g *buildtoken.ProxyGrant) { g.URL = "https://evil.example" + credsvc.BitbucketProxyPrefix }, "not this installation's credentials service"},
			{"another path", func(g *buildtoken.ProxyGrant) { g.URL = strings.TrimSuffix(g.URL, "/") + "x/" }, "not this installation's credentials service"},
			{"a wider repository set", func(g *buildtoken.ProxyGrant) { g.Repositories = []string{"acme/app", "acme/other"} }, "held to exactly"},
			{"a narrower repository set", func(g *buildtoken.ProxyGrant) { g.Repositories = nil }, "held to exactly"},
			{"a grant that has ended", func(g *buildtoken.ProxyGrant) { g.ExpiresAt = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC) }, "already ended"},
		} {
			t.Run("refuses "+c.name, func(t *testing.T) {
				_, err := run(t, c.mutate)
				if err == nil || !strings.Contains(err.Error(), c.want) {
					t.Errorf("err = %v, want it to say %q", err, c.want)
				}
			})
		}
	})
}
