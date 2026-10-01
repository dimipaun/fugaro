package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitops"
)

// A follow-up's session checks run git in a checkout the agent has written
// to: a core.fsmonitor command there must not see the model credentials.
func TestSessionGitNeverSeesModelCredentials(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-real-key")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "oauth-real")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "auth-real")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "--quiet")
	dump := filepath.Join(t.TempDir(), "env.txt")
	hook := filepath.Join(t.TempDir(), "fsmonitor.sh")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nenv > "+dump+"\nprintf '\\0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	run("config", "core.fsmonitor", hook)
	repo := strip(&gitops.Repo{Dir: dir})
	if _, err := sessionGit(context.Background(), repo, "status"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dump)
	if err != nil {
		t.Fatalf("the fsmonitor hook never ran: %v", err)
	}
	if strings.Contains(string(b), "ANTHROPIC_") || strings.Contains(string(b), "CLAUDE_CODE_OAUTH_TOKEN") || strings.Contains(string(b), "real") {
		t.Fatalf("the hook saw a model credential:\n%s", b)
	}
}
