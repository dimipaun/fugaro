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
	// The names only: this dump is the environment the hook ran with, and a
	// failure is exactly the case where it holds something that must not
	// leave the process, so the message never prints a value (a container
	// whose environment carries one would put it in the test log).
	var leaked []string
	for _, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, "ANTHROPIC_") || strings.Contains(line, "CLAUDE_CODE_OAUTH_TOKEN") || strings.Contains(line, "real") {
			if name, _, ok := strings.Cut(line, "="); ok {
				leaked = append(leaked, name)
			}
		}
	}
	if len(leaked) > 0 {
		t.Fatalf("the hook saw a model credential in %d of the command's environment variables: %s", len(leaked), strings.Join(leaked, ", "))
	}
}
