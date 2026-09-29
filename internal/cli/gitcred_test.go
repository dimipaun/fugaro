package cli

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitprov/httpfixture"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// gitCredentialEnv clears every variable git-credential reads, so the
// developer's environment can't leak into a test.
func gitCredentialEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"GIT_TOKEN", "GIT_USER", "GITHUB_APP_KEY", "GITHUB_APP_ID", "FUGARO_GITHUB_API_URL"} {
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
	t.Setenv("FUGARO_GITHUB_API_URL", srv.URL)
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
