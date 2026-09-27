package images_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

// finalizeFixture returns a clone of a fresh remote and a HOME for the script.
func finalizeFixture(t *testing.T) (repo, home string) {
	t.Helper()
	testutil.IsolateGit(t)
	remote := testutil.NewRemote(t, map[string]string{"README.md": "x\n"})
	parent := t.TempDir()
	repo = filepath.Join(parent, "repo")
	testutil.Git(t, parent, "clone", "--quiet", remote, repo)
	return repo, t.TempDir()
}

func finalize(t *testing.T, repo, home string) (string, error) {
	t.Helper()
	cmd := exec.Command("sh", filepath.Join("common", "finalize-checkout.sh"), repo)
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestFinalizeCheckoutStripsCredentials(t *testing.T) {
	repo, home := finalizeFixture(t)
	origin := testutil.Git(t, repo, "remote", "get-url", "origin")
	testutil.Git(t, repo, "config", "credential.helper", "store --file=/run/secrets/git-credentials")
	testutil.Git(t, repo, "config", "http.https://example.invalid/.extraheader", "AUTHORIZATION: basic dG9rZW4=")
	testutil.Git(t, repo, "config", "url.https://x-access-token:tok@example.invalid/.insteadOf", "https://example.invalid/")
	if err := os.WriteFile(filepath.Join(home, ".git-credentials"), []byte("https://u:tok@example.invalid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := finalize(t, repo, home); err != nil {
		t.Fatalf("finalize-checkout: %v\n%s", err, out)
	}
	cfg := strings.ToLower(testutil.Git(t, repo, "config", "--local", "--list"))
	for _, bad := range []string{"credential.", "extraheader", "insteadof", "tok@"} {
		if strings.Contains(cfg, bad) {
			t.Errorf("git config still has %q:\n%s", bad, cfg)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".git-credentials")); !os.IsNotExist(err) {
		t.Error("~/.git-credentials survived")
	}
	if got := testutil.Git(t, repo, "remote", "get-url", "origin"); got != origin {
		t.Errorf("origin changed to %s", got)
	}
}

func TestFinalizeCheckoutRejectsCredentialOrigin(t *testing.T) {
	repo, home := finalizeFixture(t)
	testutil.Git(t, repo, "remote", "set-url", "origin", "https://x-access-token:s3cr3t@github.com/acme/app.git")
	out, err := finalize(t, repo, home)
	if err == nil || !strings.Contains(out, "embeds credentials") {
		t.Fatalf("err = %v, output %q", err, out)
	}
	if strings.Contains(out, "s3cr3t") {
		t.Fatal("finalize-checkout printed the token")
	}
}

func TestFinalizeCheckoutRejectsSSHOrigin(t *testing.T) {
	for _, origin := range []string{"git@bitbucket.org:team/repo.git", "ssh://git@github.com/acme/app.git", "http://github.com/acme/app.git"} {
		t.Run(origin, func(t *testing.T) {
			repo, home := finalizeFixture(t)
			testutil.Git(t, repo, "remote", "set-url", "origin", origin)
			if out, err := finalize(t, repo, home); err == nil || !strings.Contains(out, "must be an https URL") {
				t.Fatalf("err = %v, output %q", err, out)
			}
		})
	}
}

func TestFinalizeCheckoutAcceptsHTTPSOrigin(t *testing.T) {
	repo, home := finalizeFixture(t)
	testutil.Git(t, repo, "remote", "set-url", "origin", "https://bitbucket.org/team/repo.git")
	if out, err := finalize(t, repo, home); err != nil {
		t.Fatalf("finalize-checkout: %v\n%s", err, out)
	}
}

func TestFinalizeCheckoutRejectsGlobalHelper(t *testing.T) {
	repo, home := finalizeFixture(t)
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, []byte("[credential]\n\thelper = store\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	if out, err := finalize(t, repo, home); err == nil || !strings.Contains(out, "credential helper is still configured") {
		t.Fatalf("err = %v, output %q", err, out)
	}
}

func TestFinalizeCheckoutStripsUnscopedExtraHeader(t *testing.T) {
	repo, home := finalizeFixture(t)
	testutil.Git(t, repo, "config", "http.extraheader", "AUTHORIZATION: basic dG9rZW4=")
	if out, err := finalize(t, repo, home); err != nil {
		t.Fatalf("finalize-checkout: %v\n%s", err, out)
	}
	cfg := strings.ToLower(testutil.Git(t, repo, "config", "--local", "--list"))
	// "extraheader=" (not bare "extraheader"): the fixture's own temp
	// directory name embeds this test's name, which contains "ExtraHeader".
	if strings.Contains(cfg, "extraheader=") {
		t.Errorf("git config still has an unscoped http.extraheader:\n%s", cfg)
	}
}

func TestFinalizeCheckoutRejectsGlobalExtraHeader(t *testing.T) {
	repo, home := finalizeFixture(t)
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, []byte("[http]\n\textraheader = AUTHORIZATION: basic dG9rZW4=\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	out, err := finalize(t, repo, home)
	if err == nil || !strings.Contains(out, "extraheader") {
		t.Fatalf("err = %v, output %q", err, out)
	}
	if strings.Contains(out, "dG9rZW4") {
		t.Fatal("finalize-checkout printed the extraheader value")
	}
}

func TestFinalizeCheckoutRejectsGlobalCredentialInsteadOf(t *testing.T) {
	repo, home := finalizeFixture(t)
	global := filepath.Join(t.TempDir(), "gitconfig")
	cfg := "[url \"https://x-access-token:s3cr3t@example.invalid/\"]\n\tinsteadOf = https://example.invalid/\n"
	if err := os.WriteFile(global, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	out, err := finalize(t, repo, home)
	if err == nil || !strings.Contains(out, "insteadOf") {
		t.Fatalf("err = %v, output %q", err, out)
	}
	if strings.Contains(out, "s3cr3t") {
		t.Fatal("finalize-checkout printed the insteadOf value")
	}
}

func TestFinalizeCheckoutAcceptsGlobalNonCredentialInsteadOf(t *testing.T) {
	repo, home := finalizeFixture(t)
	global := filepath.Join(t.TempDir(), "gitconfig")
	cfg := "[url \"https://mirror.example.invalid/\"]\n\tinsteadOf = https://example.invalid/\n"
	if err := os.WriteFile(global, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	if out, err := finalize(t, repo, home); err != nil {
		t.Fatalf("finalize-checkout: %v\n%s", err, out)
	}
}
