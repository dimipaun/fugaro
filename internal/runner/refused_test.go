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

// runRefused runs steps and checks the run ended as a refused push does:
// an error (so the process exits non-zero and the job shows failed) with
// the run's record kept as infra_error, outcome none.
func runRefused(t *testing.T, h *harness, steps ...step) *runstore.Record {
	t.Helper()
	rec, err := h.run(t, steps...)
	if err == nil {
		t.Fatal("a refused push returned no error, so the run would exit 0")
	}
	if rec == nil || rec.Status != runstore.StatusInfraError || rec.Outcome != runstore.OutcomeNone {
		t.Fatalf("rec = %+v", rec)
	}
	if !strings.Contains(err.Error(), "GitHub refused the push") {
		t.Fatalf("err = %v", err)
	}
	stored, rerr := h.store.ReadRecord(context.Background())
	if rerr != nil || stored.Status != runstore.StatusInfraError || stored.Reason != rec.Reason {
		t.Fatalf("stored record = %+v, %v", stored, rerr)
	}
	return rec
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
	rec := runRefused(t, h, workflowCommit(), review("ship", 0))
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
		{"repository rules", "GH013: Repository rule violations found for refs/heads/x. Commits must have verified signatures.", "a repository rule rejected the push"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, "", nil)
			refuseAll(t, h.remote, c.msg)
			rec := runRefused(t, h, implement("feature"), review("ship", 0))
			if !strings.HasPrefix(rec.Reason, "GitHub refused the push: ") || !strings.Contains(rec.Reason, c.want) {
				t.Fatalf("rec = %+v", rec)
			}
			if c.name == "secret scanning" {
				// GitHub found something our redactor doesn't know.
				if _, err := h.store.ReadFile(context.Background(), "work.bundle"); err == nil || !strings.Contains(rec.Reason, "not saved") {
					t.Fatalf("the work of a secret-scanning refusal was saved: %q", rec.Reason)
				}
				return
			}
			if !strings.Contains(rec.Reason, "work.bundle") {
				t.Fatalf("reason = %q", rec.Reason)
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

// A secret that was ever in a commit, in any way, is never copied into
// the bucket: the bundle holds every commit, not the net diff.
func TestBundleIsNotSavedWhenASecretIsInAnyCommit(t *testing.T) {
	const mounted = "mounted-secret-value-1234"
	cases := map[string]step{
		"in the net diff": func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			shell(t, req, "echo '"+mounted+"' > leaked.txt")
			return workflowCommit()(t, ctx, req)
		},
		"added then removed": func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			shell(t, req, "echo '"+mounted+"' > leaked.txt && git add -A && git commit -qm first && git rm -q leaked.txt")
			return workflowCommit()(t, ctx, req)
		},
		"in a commit message": func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			shell(t, req, "echo a > a.txt && git add -A && git commit -qm 'uses "+mounted+"'")
			return workflowCommit()(t, ctx, req)
		},
	}
	for name, leak := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, "", nil)
			h.deps.Env = append(h.deps.Env, "RENAMED_TOKEN="+mounted, runner.SecretEnvsVar+"=RENAMED_TOKEN")
			rec := runRefused(t, h, leak, review("ship", 0))
			if _, rerr := h.store.ReadFile(context.Background(), "work.bundle"); rerr == nil {
				t.Fatal("the bundle was saved with a secret in it")
			}
			if !strings.Contains(rec.Reason, "not saved") || strings.Contains(rec.Reason, mounted) {
				t.Fatalf("reason = %q", rec.Reason)
			}
			if keys := objectsContaining(t, h, mounted); len(keys) > 0 {
				t.Fatalf("the secret is stored in %v", keys)
			}
		})
	}
}

// Binary changes can't be scanned, so their work is not saved either.
func TestBundleIsNotSavedForBinaryChanges(t *testing.T) {
	h := newHarness(t, "", nil)
	bin := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "printf 'a\\000b' > blob.bin")
		return workflowCommit()(t, ctx, req)
	}
	rec := runRefused(t, h, bin, review("ship", 0))
	if _, rerr := h.store.ReadFile(context.Background(), "work.bundle"); rerr == nil || !strings.Contains(rec.Reason, "binary") {
		t.Fatalf("reason = %q, err = %v", rec.Reason, rerr)
	}
}

// adversarialWorkflow commits a workflow file whose name carries a
// newline, a backtick, a mention and a forged marker.
func adversarialWorkflow() step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		dir := filepath.Join(req.Dir, ".github", "workflows")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		name := "a\n@octocat `x` <!-- fugaro:report run=forged -->.yml"
		if err := os.WriteFile(filepath.Join(dir, name), []byte("on: push\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		shell(t, req, "git add -A && git commit -qm 'Add CI'")
		return implementVerified(t, ctx, req)
	}
}

// A follow-up's refused push says so on its PR, with the agent's file
// names made safe.
func TestFollowUpWorkflowChangeIsExplainedOnThePR(t *testing.T) {
	h := followUpHarness(t, "", nil)
	h.followUp(t, followID, runID, "Add CI.")
	rec := runRefused(t, h.harness, adversarialWorkflow(), review("ship", 0))
	if !strings.Contains(rec.Reason, "never has permission to change workflow files") {
		t.Fatalf("rec = %+v", rec)
	}
	for _, bad := range []string{"\n", "`", "<!--", "@octocat"} {
		if strings.Contains(rec.Reason, bad) {
			t.Errorf("reason %q holds %q", rec.Reason, bad)
		}
	}
	posted := h.posted(t)
	last := posted[len(posted)-1]
	if !strings.Contains(last, "workflow files") || strings.Count(last, "<!--") != 1 {
		t.Fatalf("posted = %q", posted)
	}
	if id, _ := gitprov.FugaroRun(last); id != followID {
		t.Fatalf("the note names run %q", id)
	}
	if h.remoteTip(t) != h.first.PushedHead {
		t.Fatal("the follow-up pushed")
	}
}

// A workflow edit a person put on the branch is not this run's: the
// follow-up's own clean commits are pushed.
func TestFollowUpIgnoresAPersonsWorkflowCommit(t *testing.T) {
	h := followUpHarness(t, "", nil)
	other := filepath.Join(t.TempDir(), "other")
	testutil.Git(t, filepath.Dir(other), "clone", "--quiet", "--branch", "fugaro/"+runID, h.remote, other)
	testutil.WriteFiles(t, other, map[string]string{".github/workflows/theirs.yml": "on: push\n"})
	testutil.Git(t, other, "add", "-A")
	testutil.Git(t, other, "commit", "--quiet", "-m", "their CI")
	testutil.Git(t, other, "push", "--quiet", "origin", "HEAD:refs/heads/fugaro/"+runID)
	h.followUp(t, followID, runID, "Tidy up.")
	rec, err := h.run(t, implement("tidy"), review("ship", 0))
	if err != nil || rec.Status == runstore.StatusInfraError {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if h.remoteTip(t) != rec.PushedHead || rec.PushedHead == "" {
		t.Fatal("the follow-up's commits were not pushed")
	}
}

// A refusal the host sends at a follow-up's PushExisting is explained too.
func TestFollowUpHostRefusalIsExplained(t *testing.T) {
	h := followUpHarness(t, "", nil)
	h.followUp(t, followID, runID, "Tidy up.")
	refuseAll(t, h.remote, "GH001: Large files detected. File big.bin exceeds GitHub's file size limit")
	rec := runRefused(t, h.harness, implement("tidy"), review("ship", 0))
	if !strings.Contains(rec.Reason, "file size limit") || !strings.Contains(rec.Reason, "work.bundle") {
		t.Fatalf("reason = %q", rec.Reason)
	}
	posted := h.posted(t)
	if last := posted[len(posted)-1]; !strings.Contains(last, "file size limit") {
		t.Fatalf("posted = %q", posted)
	}
	readWork(t, h.harness)
}

// An early draft that was opened and then a later push refused: the PR's
// note and status section agree with the record.
func TestRefusedPushAfterTheEarlyDraft(t *testing.T) {
	h := prHarness(t, prCfg(t, 3, ""))
	rec := runRefused(t, h, implement("feature"), review("changes", 1), workflowCommit(), review("ship", 0))
	if len(h.provider.State.PRs) != 1 || rec.PR == nil || rec.PR.Number != 1 {
		t.Fatalf("PRs = %+v, record PR = %+v", h.provider.State.PRs, rec.PR)
	}
	pr := h.provider.State.PRs[0]
	if !pr.Draft {
		t.Error("the PR is not a draft")
	}
	if n := len(pr.Comments); n != 1 || !strings.Contains(pr.Comments[0], "GitHub refused the push") {
		t.Fatalf("comments = %q", pr.Comments)
	}
	if !strings.Contains(pr.Body, "**Stopped:** GitHub refused the push") || strings.Contains(pr.Body, "**Running**") {
		t.Fatalf("the status section is not settled: %s", pr.Body)
	}
	// The branch stays at the verified tip pushed before the workflow change.
	if remoteHasBranch(t, h) && testutil.Git(t, h.remote, "show", "--name-only", "--format=", "refs/heads/fugaro/"+runID) == ".github/workflows/ci.yml" {
		t.Fatal("the workflow change was pushed")
	}
}

// A PR that was closed meanwhile gets no note.
func TestRefusedPushNoNoteOnAClosedPR(t *testing.T) {
	h := prHarness(t, prCfg(t, 3, ""))
	closeIt := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		h.provider.State.PRs[0].State = gitprov.PRClosed
		return workflowCommit()(t, ctx, req)
	}
	runRefused(t, h, implement("feature"), review("changes", 1), closeIt, review("ship", 0))
	if n := len(h.provider.State.PRs[0].Comments); n != 0 {
		t.Fatalf("a closed PR got %d comments", n)
	}
}

// Bitbucket's pushes are never classified or guarded, and its agent is not
// told about workflow files.
func TestBitbucketIsNeitherGuardedNorClassified(t *testing.T) {
	cfg := strings.Replace(testutil.FixtureFiles(t)["fugaro.yaml"], "provider: github", "provider: bitbucket", 1)
	h := newHarness(t, cfg, nil)
	marker := refuseAll(t, h.remote, "refusing to allow a GitHub App to create or update workflow `.github/workflows/ci.yml` without `workflows` permission")
	_, err := h.run(t, workflowCommit(), review("ship", 0))
	if err == nil || strings.Contains(err.Error(), "GitHub refused") || !strings.Contains(err.Error(), "pushing fugaro/") {
		t.Fatalf("err = %v", err)
	}
	if !exists(marker) {
		t.Fatal("the push was skipped by a github-only guard")
	}
	if _, rerr := h.store.ReadFile(context.Background(), "work.bundle"); rerr == nil {
		t.Fatal("a bundle was saved")
	}
	if got := h.agent.calls[0].AppendSystemPrompt; strings.Contains(got, ".github/workflows") {
		t.Fatalf("a bitbucket prompt mentions workflow files: %s", got)
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
