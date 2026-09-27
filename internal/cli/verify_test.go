package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
	"github.com/dimipaun/fugaro/internal/verify"
)

func TestVerifyNeedsStateDir(t *testing.T) {
	t.Setenv("FUGARO_STATE_DIR", "")
	_, _, err := execute(t, "verify", "build")
	if err == nil || !strings.Contains(err.Error(), "FUGARO_STATE_DIR") {
		t.Fatalf("err = %v", err)
	}
}

func TestVerifyExitCodes(t *testing.T) {
	testutil.IsolateGit(t)
	remote := testutil.NewRemote(t, map[string]string{"README.md": "x\n"})
	parent := t.TempDir()
	repo := filepath.Join(parent, "repo")
	testutil.Git(t, parent, "clone", "--quiet", remote, repo)
	state := t.TempDir()
	if err := verify.WriteSettings(state, verify.Settings{RepoDir: repo, Build: "true", Test: "false"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_STATE_DIR", state)
	if _, _, err := execute(t, "verify", "build"); err != nil {
		t.Fatalf("passing build: %v", err)
	}
	_, stderr, err := execute(t, "verify", "test")
	if ExitCode(err) != ExitUserError || !strings.Contains(stderr, "FAILED") {
		t.Fatalf("failing test: exit %d stderr %q", ExitCode(err), stderr)
	}
}
