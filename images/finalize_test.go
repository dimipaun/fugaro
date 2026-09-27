package images_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/images"
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

func TestFinalizeCheckoutStripsPushInsteadOf(t *testing.T) {
	repo, home := finalizeFixture(t)
	testutil.Git(t, repo, "config", "url.https://x-access-token:tok@example.invalid/.pushInsteadOf", "https://example.invalid/")
	if out, err := finalize(t, repo, home); err != nil {
		t.Fatalf("finalize-checkout: %v\n%s", err, out)
	}
	cfg := strings.ToLower(testutil.Git(t, repo, "config", "--local", "--list"))
	// "pushinsteadof=" (not bare "pushinsteadof"): the fixture's own temp
	// directory name embeds this test's name, which contains "PushInsteadOf".
	for _, bad := range []string{"pushinsteadof=", "tok@"} {
		if strings.Contains(cfg, bad) {
			t.Errorf("git config still has %q:\n%s", bad, cfg)
		}
	}
}

func TestFinalizeCheckoutRejectsGlobalPushInsteadOf(t *testing.T) {
	repo, home := finalizeFixture(t)
	global := filepath.Join(t.TempDir(), "gitconfig")
	cfg := "[url \"https://x-access-token:s3cr3t@example.invalid/\"]\n\tpushInsteadOf = https://example.invalid/\n"
	if err := os.WriteFile(global, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	out, err := finalize(t, repo, home)
	if err == nil || !strings.Contains(out, "pushInsteadOf") {
		t.Fatalf("err = %v, output %q", err, out)
	}
	if strings.Contains(out, "s3cr3t") {
		t.Fatal("finalize-checkout printed the pushInsteadOf value")
	}
}

func TestFinalizeCheckoutRejectsGlobalTokenOnlyInsteadOf(t *testing.T) {
	repo, home := finalizeFixture(t)
	global := filepath.Join(t.TempDir(), "gitconfig")
	cfg := "[url \"https://ghp_s3cr3ttoken@example.invalid/\"]\n\tinsteadOf = https://example.invalid/\n"
	if err := os.WriteFile(global, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	out, err := finalize(t, repo, home)
	if err == nil || !strings.Contains(out, "insteadOf") {
		t.Fatalf("err = %v, output %q", err, out)
	}
	if strings.Contains(out, "s3cr3ttoken") {
		t.Fatal("finalize-checkout printed the insteadOf value")
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

// TestFinalizeCheckoutUsesTheSharedCredentialPatterns keeps finalize-checkout
// and fugaro image selftest on one source of truth: every pattern the
// selftest applies (images.GitCredential*) appears verbatim, single-quoted,
// in the script.
func TestFinalizeCheckoutUsesTheSharedCredentialPatterns(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("common", "finalize-checkout.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, re := range []string{
		images.GitCredentialHelperKey,
		images.GitCredentialExtraHeaderKey,
		images.GitCredentialInsteadOfKey,
		images.GitCredentialPushInsteadOfKey,
		images.GitCredentialURL,
		images.GitCredentialRemoteURLKey,
	} {
		if !strings.Contains(string(data), "'"+re+"'") {
			t.Errorf("finalize-checkout.sh does not use the pattern '%s'", re)
		}
	}
}

// TestFinalizeCheckoutStripsKeysWithSpaces: a URL subsection may contain a
// space, so the strip loop must take each key name whole. Split on
// whitespace, the fragments fail --unset-all and git's error can echo part
// of a credential-bearing URL into the build log.
func TestFinalizeCheckoutStripsKeysWithSpaces(t *testing.T) {
	repo, home := finalizeFixture(t)
	testutil.Git(t, repo, "config", "url.https://x-access-token:s3cr3t@my host.invalid/.insteadOf", "https://example.invalid/")
	testutil.Git(t, repo, "config", "http.https://my host.invalid/.extraheader", "AUTHORIZATION: basic s3cr3t")
	out, err := finalize(t, repo, home)
	if err != nil {
		t.Fatalf("finalize-checkout: %v\n%s", err, out)
	}
	if strings.Contains(out, "s3cr3t") {
		t.Fatalf("finalize-checkout printed a credential: %q", out)
	}
	if cfg, _ := os.ReadFile(filepath.Join(repo, ".git", "config")); strings.Contains(string(cfg), "s3cr3t") {
		t.Fatalf("the local config still holds the credential:\n%s", cfg)
	}
}

// TestFinalizeCheckoutRejectsIncludedCredential: `git config --local` does
// not follow include.path, so a helper in an included file survives the
// local strip. The all-scope check must still refuse it, without blaming
// the system or global config alone.
func TestFinalizeCheckoutRejectsIncludedCredential(t *testing.T) {
	repo, home := finalizeFixture(t)
	inc := filepath.Join(t.TempDir(), "included.gitconfig")
	if err := os.WriteFile(inc, []byte("[credential]\n\thelper = store\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, repo, "config", "include.path", inc)
	out, err := finalize(t, repo, home)
	if err == nil || !strings.Contains(out, "credential helper is still configured") || !strings.Contains(out, "included") {
		t.Fatalf("err = %v, output %q", err, out)
	}
}

// TestFinalizeCheckoutRejectsCredentialRemoteURLs: every remote's url and
// pushurl count, not only origin's fetch URL. A token in a pushurl or in a
// second remote's URL must fail the build without being printed.
func TestFinalizeCheckoutRejectsCredentialRemoteURLs(t *testing.T) {
	for name, args := range map[string][]string{
		"origin pushurl": {"config", "remote.origin.pushurl", "https://x-access-token:s3cr3t@github.com/acme/app.git"},
		"second remote":  {"remote", "add", "upstream", "https://s3cr3t@github.com/acme/upstream.git"},
	} {
		t.Run(name, func(t *testing.T) {
			repo, home := finalizeFixture(t)
			testutil.Git(t, repo, args...)
			out, err := finalize(t, repo, home)
			if err == nil || !strings.Contains(out, "embeds credentials") {
				t.Fatalf("err = %v, output %q", err, out)
			}
			if strings.Contains(out, "s3cr3t") {
				t.Fatalf("finalize-checkout printed the token: %q", out)
			}
		})
	}
}

// TestFinalizeCheckoutAcceptsACleanSecondRemote: a second remote without
// credentials is fine.
func TestFinalizeCheckoutAcceptsACleanSecondRemote(t *testing.T) {
	repo, home := finalizeFixture(t)
	testutil.Git(t, repo, "remote", "add", "upstream", "https://github.com/acme/upstream.git")
	testutil.Git(t, repo, "config", "remote.origin.pushurl", "https://github.com/acme/app.git")
	if out, err := finalize(t, repo, home); err != nil {
		t.Fatalf("finalize-checkout: %v\n%s", err, out)
	}
}
