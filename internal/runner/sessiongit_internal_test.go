package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gitops"
)

// A follow-up's session checks run git in a checkout the agent has written
// to: a core.fsmonitor command there must not see the model credentials.
func TestSessionGitNeverSeesModelCredentials(t *testing.T) {
	// The values are markers: what the assertion below looks for is these
	// three strings, wherever the hook's environment carries them.
	markers := []string{"sk-real-key", "oauth-real", "auth-real"}
	t.Setenv("ANTHROPIC_API_KEY", markers[0])
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", markers[1])
	t.Setenv("ANTHROPIC_AUTH_TOKEN", markers[2])
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
	// The variables the runner strips (gitops.ModelCredentialVars), and the
	// three markers above wherever they turn up: a hook whose environment
	// holds a name from that list, or one of those values under any name at
	// all, is the failure this test exists for.
	//
	// Matching every name that merely begins with `ANTHROPIC_` is not the same
	// claim and is not true: the runner leaves the worker's own model endpoint
	// and default-model variables alone (ANTHROPIC_BASE_URL,
	// ANTHROPIC_DEFAULT_*_MODEL are not in that list, deliberately), so that
	// pattern made this test fail in any container that configures one — the
	// container the package's documented command now runs in
	// (CONTRIBUTING.md).
	//
	// The names only, never a value: this dump is the environment the hook ran
	// with, and a failure is the case where it holds something that must not
	// leave the process.
	var leaked []string
	for _, line := range strings.Split(string(b), "\n") {
		name, _, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if slices.Contains(gitops.ModelCredentialVars, name) || slices.ContainsFunc(markers, func(m string) bool { return strings.Contains(line, m) }) {
			leaked = append(leaked, name)
		}
	}
	if len(leaked) > 0 {
		t.Fatalf("the hook saw a model credential in %d of the command's environment variables: %s", len(leaked), strings.Join(leaked, ", "))
	}
}
