package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

func TestMain(m *testing.M) {
	dropInheritedGitConfig()
	cleanup, err := testutil.PrepareFakeCommands()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// dropInheritedGitConfig removes git's environment configuration from the
// process before any test runs. A Fugaro session's stamp sets it for the agent
// (gitops.StampEnv), and configuration git reads from the environment outranks
// the repository's own, so a test that runs git before it reaches the harness —
// which drops them itself, through testutil.IsolateGit — would read
// core.hooksPath, and the credential helper beside it, out of the run the
// session is inside rather than out of the fixture it built. Every test in the
// package starts from a clean git config environment, harness or not.
func dropInheritedGitConfig() {
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); isGitConfigEnv(k) {
			os.Unsetenv(k)
		}
	}
}

// isGitConfigEnv reports whether k is a name git reads configuration from the
// environment under (git-config(1)): the count and its numbered pairs, and the
// GIT_CONFIG_PARAMETERS git's own -c builds.
func isGitConfigEnv(k string) bool {
	return k == "GIT_CONFIG_COUNT" || k == "GIT_CONFIG_PARAMETERS" ||
		strings.HasPrefix(k, "GIT_CONFIG_KEY_") || strings.HasPrefix(k, "GIT_CONFIG_VALUE_")
}

// TestGitConfigEnvironmentIsInheritedNoMore: a test in this package runs git
// under the environment TestMain left it, so what the shell that started the
// test binary carries must not decide what git does. Both halves are the one
// claim: the names are gone, and git in a repository a test made itself
// resolves that repository's own hook directory — which is what an inherited
// core.hooksPath replaces, silently, in every git call the test makes.
//
// Run with GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=core.hooksPath
// GIT_CONFIG_VALUE_0=anywhere: this fails before TestMain drops them and
// passes after.
func TestGitConfigEnvironmentIsInheritedNoMore(t *testing.T) {
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if isGitConfigEnv(k) {
			t.Errorf("%s is in the environment of a test in this package: git reads it in every call the test makes", k)
		}
	}
	// The repository's own configuration is what the test is about, so the
	// machine's is out of the way, as it is outside a run.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "--quiet", "-b", "main", ".")
	got := testutil.Git(t, dir, "rev-parse", "--git-path", "hooks")
	if !filepath.IsAbs(got) {
		got = filepath.Join(dir, got)
	}
	if want := filepath.Join(dir, ".git", "hooks"); got != want {
		t.Errorf("git resolves the repository's hook directory to %s, want %s: an inherited core.hooksPath outranks the repository's own", got, want)
	}
}
