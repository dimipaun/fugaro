package gitops

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

const token = "tok-secret-1234"

func credEnv(remote string) []string {
	return WithVars(IdentityEnv(), CredentialVars(CredentialURL(remote), "x-token-auth", token))
}

func TestCredentialURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://bitbucket.org/acme/web.git":   "https://bitbucket.org",
		"http://127.0.0.1:8080/remote.git":     "http://127.0.0.1:8080",
		"git@github.com:acme/web.git":          "",
		"ssh://git@github.com/acme/web.git":    "",
		"/tmp/remote.git":                      "",
		"https://x-token-auth@bitbucket.org/a": "https://bitbucket.org",
	} {
		if got := CredentialURL(in); got != want {
			t.Errorf("CredentialURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWithVarsReplacesAndAppends(t *testing.T) {
	got := WithVars([]string{"A=1", "B=2", "C=3"}, map[string]string{"B": "x", "D": "y"})
	if !slices.Equal(got, []string{"A=1", "C=3", "B=x", "D=y"}) {
		t.Fatalf("env = %q", got)
	}
}

func TestCredentialsCloneFetchAndPushOverHTTP(t *testing.T) {
	testutil.IsolateGit(t)
	remote := testutil.NewHTTPRemote(t, map[string]string{"README.md": "hi\n"}, testutil.Token("x-token-auth", token))
	dir := filepath.Join(t.TempDir(), "work")
	if _, err := OpenOrClone(ctx, dir, remote.URL, IdentityEnv()); err == nil {
		t.Fatal("clone without credentials succeeded")
	}
	repo, err := OpenOrClone(ctx, dir, remote.URL, credEnv(remote.URL))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	if err := repo.CommitEmpty(ctx, "work"); err != nil {
		t.Fatal(err)
	}
	if err := repo.Push(ctx, "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	if got, want := testutil.Git(t, remote.Bare, "rev-parse", "refs/heads/fugaro/x"), testutil.Git(t, dir, "rev-parse", "HEAD"); got != want {
		t.Fatalf("remote branch %s, want %s", got, want)
	}
	for _, f := range []string{".git/config", ".git/FETCH_HEAD"} {
		if data, _ := os.ReadFile(filepath.Join(dir, f)); strings.Contains(string(data), token) {
			t.Fatalf("%s contains the token:\n%s", f, data)
		}
	}
	if u, err := repo.OriginURL(ctx); err != nil || u != remote.URL {
		t.Fatalf("OriginURL = %q, %v", u, err)
	}
}

func TestCredentialHelperIgnoresOtherHelpersAndHosts(t *testing.T) {
	testutil.IsolateGit(t)
	remote := testutil.NewHTTPRemote(t, map[string]string{"README.md": "hi\n"}, testutil.Token("x-token-auth", token))
	repo, err := OpenOrClone(ctx, filepath.Join(t.TempDir(), "work"), remote.URL, credEnv(remote.URL))
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "asked")
	testutil.Git(t, repo.Dir, "config", "credential.helper", "!f() { echo asked >> "+marker+"; }; f")
	fill := func(host string) (string, error) {
		cmd := exec.Command("git", "credential", "fill")
		cmd.Dir = repo.Dir
		cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0"), credEnv(remote.URL)...)
		cmd.Stdin = strings.NewReader("protocol=http\nhost=" + host + "\n\n")
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	host := strings.TrimPrefix(CredentialURL(remote.URL), "http://")
	out, err := fill(host)
	if err != nil || !strings.Contains(out, "username=x-token-auth\n") || !strings.Contains(out, "password="+token+"\n") {
		t.Fatalf("fill for the remote = %q, %v", out, err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the repository's own credential helper was consulted for the remote's host")
	}
	if out, _ := fill("elsewhere.example"); strings.Contains(out, token) {
		t.Fatalf("another host got the token: %q", out)
	}
}
