package runner_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// leftOver is a file a stage leaves in the checkout without committing it, so
// the run's own leftover commit at finalize (never the stage) is the only
// thing that would carry it onto the branch.
type leftOver func(t *testing.T, req agent.Request, name string)

// implementLeaving is implement with one more thing: a file written after the
// verify run, left untracked. Writing it after the suite ran is what keeps the
// run's verified-test record the one it earned, so a case here is about
// finalize and nothing else.
func implementLeaving(name string, leave leftOver) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo feature > feature.txt && git add -A && git commit -qm 'Add feature'")
		verifyTest(t, ctx, req)
		leave(t, req, name)
		pr := filepath.Join(envValue(req.Env, "FUGARO_STATE_DIR"), "pr.md")
		if err := os.WriteFile(pr, []byte("# Add feature\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return agent.Result{CostUSD: 1}, nil
	}
}

// compiledBinary is a build's leftover as a compiler writes one: a megabyte of
// bytes that are not text — the ELF header carries the first NUL, whatever the
// file's mode is.
func compiledBinary(t *testing.T, req agent.Request, name string) {
	t.Helper()
	b := make([]byte, 1<<20)
	copy(b, "\x7fELF")
	if err := os.WriteFile(filepath.Join(req.Dir, name), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// executableScript is text the run marked executable: a file finalize commits
// like any other, because a mode is not a build's signature.
func executableScript(t *testing.T, req agent.Request, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(req.Dir, name), []byte("#!/bin/sh\necho hello\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// plainText is the run's ordinary uncommitted work: text, no exec bit.
func plainText(t *testing.T, req agent.Request, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(req.Dir, name), []byte("the run's own words\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// branchFiles lists the paths the run's branch carries.
func branchFiles(t *testing.T, h *harness, branch string) []string {
	t.Helper()
	return strings.Fields(testutil.Git(t, h.remote, "ls-tree", "-r", "--name-only", branch))
}

// TestFinalizeRefusesAnUntrackedBinary: a run that leaves a compiled artifact
// in the checkout does not ship it. The artifact stays out of the branch, the
// run is a draft whose reason names the file, and the run's own committed work
// is on the branch all the same — what is refused is the artifact, not the run
// (a 1.9 MB test binary was committed this way once).
func TestFinalizeRefusesAnUntrackedBinary(t *testing.T) {
	h := newHarness(t, "", nil)
	rec, err := h.run(t, implementLeaving("test_dup_case2", compiledBinary), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeDraft || rec.Status != runstore.StatusFailed {
		t.Fatalf("record = %+v; want a draft: the run left a file finalize must not commit", rec)
	}
	if !strings.Contains(rec.Reason, "test_dup_case2") {
		t.Errorf("reason = %q; want it to name the file that was refused", rec.Reason)
	}
	files := branchFiles(t, h, rec.Branch)
	if len(files) == 1 && files[0] == "test_dup_case2" {
		t.Fatalf("the branch carries only the artifact: %v", files)
	}
	for _, f := range files {
		if f == "test_dup_case2" {
			t.Errorf("the branch carries the artifact finalize refused: %v", files)
		}
	}
	if !slices.Contains(files, "feature.txt") {
		t.Errorf("the branch does not carry the run's own work: %v", files)
	}
}

// TestFinalizeCommitsAnUntrackedScript: what the rule deliberately leaves
// alone. A file the run left executable and forgot to commit is a script
// someone wrote — text, which a reviewer can read — and the leftover commit is
// exactly the mechanism that saves it. Refusing it would fail a run over work
// that is the repository's, which is why the refusal reads the content and not
// the mode.
func TestFinalizeCommitsAnUntrackedScript(t *testing.T) {
	h := newHarness(t, "", nil)
	rec, err := h.run(t, implementLeaving("deploy.sh", executableScript), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Reason != "uncommitted changes were committed at finalize, after the last verified test run" {
		t.Fatalf("reason = %q; want the ordinary leftover-commit reason, not a refusal", rec.Reason)
	}
	if !slices.Contains(branchFiles(t, h, rec.Branch), "deploy.sh") {
		t.Errorf("the branch does not carry the script finalize committed: %v", branchFiles(t, h, rec.Branch))
	}
}

// TestFinalizeCommitsNormalFiles: the refusal is for artifacts. A run whose
// last uncommitted file is ordinary text is committed by finalize as it always
// was — the leftover commit, and the reason a person reads when that commit
// voids the run's verified test.
func TestFinalizeCommitsNormalFiles(t *testing.T) {
	h := newHarness(t, "", nil)
	rec, err := h.run(t, implementLeaving("notes.txt", plainText), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Reason != "uncommitted changes were committed at finalize, after the last verified test run" {
		t.Fatalf("reason = %q; want the ordinary leftover-commit reason, not a refusal", rec.Reason)
	}
	if !slices.Contains(branchFiles(t, h, rec.Branch), "notes.txt") {
		t.Errorf("the branch does not carry the file finalize committed: %v", branchFiles(t, h, rec.Branch))
	}
	if subject := testutil.Git(t, h.remote, "log", "-1", "--format=%s", rec.Branch); subject != "fugaro: uncommitted work at finalize" {
		t.Errorf("the branch's tip is %q; want finalize's own leftover commit", subject)
	}
}
