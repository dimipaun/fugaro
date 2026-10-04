package runner_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// workflowCommit commits a workflow file and a source file, then verifies.
func workflowCommit() step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "mkdir -p .github/workflows && echo 'on: push' > .github/workflows/ci.yml && echo x > feature.txt && git add -A && git commit -qm 'Add CI'")
		return implementVerified(t, ctx, req)
	}
}

// implementVerified verifies the current HEAD and writes pr.md.
func implementVerified(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
	verifyTest(t, ctx, req)
	pr := filepath.Join(envValue(req.Env, "FUGARO_STATE_DIR"), "pr.md")
	if err := os.WriteFile(pr, []byte("# CI\n\nAdds CI."), 0o644); err != nil {
		t.Fatal(err)
	}
	return agent.Result{CostUSD: 1}, nil
}

// refuseAll makes the remote's pre-receive hook refuse every push with msg.
func refuseAll(t *testing.T, remote, msg string) string {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "hook-ran")
	script := "#!/bin/sh\ncat >/dev/null\ntouch " + marker + "\ncat >&2 <<'EOF_MSG'\n" + msg + "\nEOF_MSG\nexit 1\n"
	if err := os.WriteFile(filepath.Join(remote, "hooks", "pre-receive"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return marker
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func readWork(t *testing.T, h *harness) []byte {
	t.Helper()
	data, err := h.store.ReadFile(context.Background(), "work.bundle")
	if err != nil {
		t.Fatalf("work.bundle is not in the run's prefix: %v", err)
	}
	return data
}

// A workflow change is caught before the push: no network call, a clear
// reason, and the work saved in the run's prefix.
func TestWorkflowChangeSkipsThePushAndSavesTheWork(t *testing.T) {
	h := newHarness(t, "", nil)
	marker := refuseAll(t, h.remote, "this hook must never run")
	rec, err := h.run(t, workflowCommit(), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusInfraError || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("rec = %+v", rec)
	}
	for _, want := range []string{
		"GitHub refused the push", ".github/workflows/ci.yml",
		"Fugaro never has permission to change workflow files; a person must make that change",
		"runs/acme-app/" + runID + "/work.bundle",
	} {
		if !strings.Contains(rec.Reason, want) {
			t.Errorf("reason %q lacks %q", rec.Reason, want)
		}
	}
	if strings.Contains(rec.Reason, "\n") {
		t.Errorf("the reason is not one line: %q", rec.Reason)
	}
	if exists(marker) || remoteHasBranch(t, h) {
		t.Fatal("a push was attempted")
	}
	bundle := filepath.Join(t.TempDir(), "work.bundle")
	if err := os.WriteFile(bundle, readWork(t, h), 0o600); err != nil {
		t.Fatal(err)
	}
	// The saved work restores into a clone of the remote.
	clone := filepath.Join(t.TempDir(), "clone")
	testutil.Git(t, filepath.Dir(clone), "clone", "--quiet", h.remote, clone)
	testutil.Git(t, clone, "fetch", "--quiet", bundle, "HEAD")
	if got := testutil.Git(t, clone, "show", "--name-only", "--format=%s", "FETCH_HEAD"); !strings.Contains(got, ".github/workflows/ci.yml") {
		t.Fatalf("the bundle's tip = %q", got)
	}
	if report, err := h.store.ReadFile(context.Background(), "report.md"); err != nil || !strings.Contains(string(report), "GitHub refused the push") {
		t.Fatalf("report = %q, %v", report, err)
	}
	if len(h.provider.State.PRs) != 0 {
		t.Fatalf("a PR exists: %+v", h.provider.State.PRs)
	}
}

// A rejection the host sends back (here a large file; the workflow case is
// caught earlier) is recognised too.
func TestRefusedPushIsExplainedAndSaved(t *testing.T) {
	cases := []struct{ name, msg, want string }{
		{"large file", "GH001: Large files detected. File big.bin exceeds GitHub's file size limit of 100.00 MB", "over GitHub's file size limit"},
		{"protected", "GH006: Protected branch update failed (protected branch hook declined)", "branch is protected"},
		{"secret scanning", "GH013: Repository rule violations found. Push cannot contain secrets", "push protection"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, "", nil)
			refuseAll(t, h.remote, c.msg)
			rec, err := h.run(t, implement("feature"), review("ship", 0))
			if err != nil {
				t.Fatal(err)
			}
			if rec.Status != runstore.StatusInfraError || !strings.HasPrefix(rec.Reason, "GitHub refused the push: ") || !strings.Contains(rec.Reason, c.want) || !strings.Contains(rec.Reason, "work.bundle") {
				t.Fatalf("rec = %+v", rec)
			}
			readWork(t, h)
		})
	}
}

// Anything else that fails the push keeps the plain error.
func TestUnrecognisedPushFailureStaysAPlainError(t *testing.T) {
	h := newHarness(t, "", nil)
	refuseAll(t, h.remote, "no thanks")
	_, err := h.run(t, implement("feature"), review("ship", 0))
	if err == nil || !strings.Contains(err.Error(), "pushing fugaro/") {
		t.Fatalf("err = %v", err)
	}
	if _, rerr := h.store.ReadFile(context.Background(), "work.bundle"); rerr == nil {
		t.Fatal("a bundle was saved for a failure that may be transient")
	}
}

// A secret that reached a commit is never copied into the bucket.
func TestBundleIsNotSavedWhenASecretIsInTheCommits(t *testing.T) {
	h := newHarness(t, "", nil)
	const mounted = "mounted-secret-value-1234"
	h.deps.Env = append(h.deps.Env, "RENAMED_TOKEN="+mounted, runner.SecretEnvsVar+"=RENAMED_TOKEN")
	leak := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo '"+mounted+"' > leaked.txt")
		return workflowCommit()(t, ctx, req)
	}
	rec, err := h.run(t, leak, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, rerr := h.store.ReadFile(context.Background(), "work.bundle"); rerr == nil {
		t.Fatal("the bundle was saved with a secret in it")
	}
	if !strings.Contains(rec.Reason, "not saved") || strings.Contains(rec.Reason, mounted) {
		t.Fatalf("reason = %q", rec.Reason)
	}
	if keys := objectsContaining(t, h, mounted); len(keys) > 0 {
		t.Fatalf("the secret is stored in %v", keys)
	}
}

// A follow-up's refused push says so on its PR.
func TestFollowUpWorkflowChangeIsExplainedOnThePR(t *testing.T) {
	h := followUpHarness(t, "", nil)
	h.followUp(t, followID, runID, "Add CI.")
	rec, err := h.run(t, workflowCommit(), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "never has permission to change workflow files") {
		t.Fatalf("rec = %+v", rec)
	}
	posted := h.posted(t)
	last := posted[len(posted)-1]
	if !strings.Contains(last, "workflow files") || !strings.Contains(last, ".github/workflows/ci.yml") {
		t.Fatalf("posted = %q", posted)
	}
	if id, _ := gitprov.FugaroRun(last); id != followID {
		t.Fatalf("the note names run %q", id)
	}
}

// The agent is told, for GitHub only.
func TestImplementPromptSaysNoWorkflowFiles(t *testing.T) {
	h := newHarness(t, "", nil)
	if _, err := h.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if got := h.agent.calls[0].AppendSystemPrompt; !strings.Contains(got, ".github/workflows/") {
		t.Fatalf("the system prompt does not mention workflow files: %s", got)
	}
}
