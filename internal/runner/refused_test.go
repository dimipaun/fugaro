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
	"github.com/dimipaun/fugaro/internal/task"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// refSpec is the harness's task with the run started from ref, as
// `fugaro run --ref console` starts one.
func refSpec(ref string) *task.Spec {
	return &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: ref, Task: "Add a feature"}
}

// commitOnBranch commits files onto a new branch name of the remote, cut
// from main: a person's own work, already on the branch a run starts from.
func commitOnBranch(t *testing.T, remote, name string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "seed")
	testutil.Git(t, filepath.Dir(dir), "clone", "--quiet", remote, dir)
	testutil.WriteFiles(t, dir, files)
	testutil.Git(t, dir, "add", "-A")
	testutil.Git(t, dir, "commit", "--quiet", "-m", "a person's work on "+name)
	testutil.Git(t, dir, "push", "--quiet", "origin", "HEAD:refs/heads/"+name)
}

// consoleHarness is a run started from the branch console, which already
// carries workflow files main does not have — the shape of the reported
// bug: the owner committed .github/workflows/console-*.yml on console, and
// `fugaro run --ref console` is cut from it.
func consoleHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t, "", refSpec("console"))
	commitOnBranch(t, h.remote, "console", map[string]string{
		".github/workflows/console-tests.yml":   "on: push\n",
		".github/workflows/console-release.yml": "on: release\n",
		"pipeline.yml":                          "steps:\n",
	})
	return h
}

// consoleStep runs script — which edits, adds, deletes or moves a file and
// commits it — and verifies, the way implement does for a source file.
func consoleStep(script string) step {
	return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, script)
		return implementVerified(t, ctx, req)
	}
}

// A run cut from a branch that already carries workflow files main does
// not have changed none of them, so it is pushed as any run is: the guard
// compares against the commit the run was cut from, not the base branch.
func TestRunFromARefWithWorkflowFilesIsNotRefused(t *testing.T) {
	h := consoleHarness(t)
	// A refusal would end the run non-zero without pushing (endRefused), so
	// the run's own success is the guard letting the push through.
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Status == runstore.StatusInfraError {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if rec.PushedHead == "" || !remoteHasBranch(t, h) || rec.PushedHead != rec.HeadSHA {
		t.Fatalf("the run's work was not pushed: %+v", rec)
	}
	// The branch that landed carries the owner's workflow files and the
	// run's own commit: nothing was stripped off it.
	names := testutil.Git(t, h.remote, "ls-tree", "-r", "--name-only", "refs/heads/"+rec.Branch)
	for _, want := range []string{"feature.txt", ".github/workflows/console-tests.yml", ".github/workflows/console-release.yml"} {
		if !strings.Contains(names, want) {
			t.Errorf("the pushed tree lacks %s: %q", want, names)
		}
	}
}

// The run's own changes on that same ref are still caught, and named.
func TestWorkflowChangesOnTheRunsOwnRefAreRefused(t *testing.T) {
	// want is the file the refusal must name; quiet is one of the owner's
	// workflow files this run did not touch, which it must not name.
	cases := []struct {
		name, script, want, quiet string
	}{
		{
			"edited",
			"echo 'on: pull_request' >> .github/workflows/console-tests.yml && git add -A && git commit -qm 'Edit CI'",
			".github/workflows/console-tests.yml", ".github/workflows/console-release.yml",
		},
		{
			"deleted",
			"git rm -q .github/workflows/console-release.yml && git commit -qm 'Drop CI'",
			".github/workflows/console-release.yml", ".github/workflows/console-tests.yml",
		},
		{
			"added",
			"echo 'on: push' > .github/workflows/new.yml && git add -A && git commit -qm 'Add CI'",
			".github/workflows/new.yml", ".github/workflows/console-release.yml",
		},
		{
			"renamed in",
			"git mv pipeline.yml .github/workflows/pipeline.yml && git commit -qm 'Move it in'",
			".github/workflows/pipeline.yml", ".github/workflows/console-tests.yml",
		},
		{
			// The base branch is only ever subtracted, and the agent's git
			// can repoint its local copy at the run's own tip: what is
			// subtracted is the base as the remote has it.
			"with the local base repointed at the run's tip",
			"echo 'on: pull_request' >> .github/workflows/console-tests.yml && git commit -qam 'Edit CI' && git update-ref refs/remotes/origin/main HEAD",
			".github/workflows/console-tests.yml", ".github/workflows/console-release.yml",
		},
		{
			"a commit a later one undoes",
			"echo 'on: pull_request' >> .github/workflows/console-tests.yml && git commit -qam 'Edit CI' && git revert --no-edit HEAD >/dev/null",
			".github/workflows/console-tests.yml", ".github/workflows/console-release.yml",
		},
		{
			"behind a merge that keeps the run's side",
			"b=$(git branch --show-current) && git checkout -qb side && echo 'on: pull_request' >> .github/workflows/console-tests.yml && " +
				"git commit -qam 'Edit CI' && git checkout -q \"$b\" && git merge -q -s ours side -m m",
			".github/workflows/console-tests.yml", ".github/workflows/console-release.yml",
		},
		{
			"mode only",
			"git update-index --chmod=-x .github/workflows/console-release.yml && git commit -qm 'chmod'",
			".github/workflows/console-release.yml", ".github/workflows/console-tests.yml",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := consoleHarness(t)
			marker := refuseAll(t, h.remote, "this hook must never run")
			rec := runRefused(t, h, consoleStep(c.script), review("ship", 0))
			if !strings.Contains(rec.Reason, "never has permission to change workflow files") {
				t.Fatalf("reason = %q", rec.Reason)
			}
			if !strings.Contains(rec.Reason, c.want) {
				t.Fatalf("reason = %q, want it to name %s", rec.Reason, c.want)
			}
			if strings.Contains(rec.Reason, c.quiet) {
				t.Fatalf("reason = %q, and it blames the run for %s, which it did not touch", rec.Reason, c.quiet)
			}
			if exists(marker) || remoteHasBranch(t, h) {
				t.Fatal("a push was attempted")
			}
			readWork(t, h)
		})
	}
}

// The default-branch case is unchanged: a person's workflow file that was
// already on main before the run started is not the run's either.
func TestRunOnTheBaseBranchIgnoresAPersonsWorkflowFile(t *testing.T) {
	h := newHarness(t, "", nil)
	commitOnBranch(t, h.remote, "main", map[string]string{".github/workflows/person.yml": "on: push\n"})
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Status == runstore.StatusInfraError {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if rec.PushedHead == "" || !remoteHasBranch(t, h) {
		t.Fatalf("the run's own work was not pushed: %+v", rec)
	}
}

// A base branch that gains a workflow file while the run is working — a
// dependabot bump merged on main, say — is not this run's work: a run that
// merges it and changes no workflow file is pushed as any run is. Neither
// bound alone says this: against the commit the run was cut from the merged
// base's file looks like the run's, and against the base branch alone the
// branch the run was cut from looks like it added every workflow file the
// run never touched.
func TestRunThatMergesANewerBaseIsNotRefused(t *testing.T) {
	h := newHarness(t, "", nil)
	seed := filepath.Join(t.TempDir(), "seed")
	mergeTheBase := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		testutil.Git(t, filepath.Dir(seed), "clone", "--quiet", h.remote, seed)
		testutil.WriteFiles(t, seed, map[string]string{".github/workflows/person.yml": "on: push\n"})
		testutil.Git(t, seed, "add", "-A")
		testutil.Git(t, seed, "commit", "--quiet", "-m", "a person's CI")
		testutil.Git(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/main")
		// The run merges it, as one told to keep up with its base does.
		shell(t, req, "git fetch -q origin main && git merge -q --no-edit origin/main")
		return implement("feature")(t, ctx, req)
	}
	rec, err := h.run(t, mergeTheBase, review("ship", 0))
	if err != nil || rec.Status == runstore.StatusInfraError {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if rec.PushedHead == "" || !remoteHasBranch(t, h) {
		t.Fatalf("the run's work was not pushed: %+v", rec)
	}
	names := testutil.Git(t, h.remote, "ls-tree", "-r", "--name-only", "refs/heads/"+rec.Branch)
	for _, want := range []string{"feature.txt", ".github/workflows/person.yml"} {
		if !strings.Contains(names, want) {
			t.Errorf("the pushed tree lacks %s: %q", want, names)
		}
	}
}

// The guard reads the run's own commits between both bounds — the commit it
// was cut from and the bases it works from — and with both of them in place a
// workflow file the run changed itself is still refused, and named. What a
// bound leaves out is that base's own commits, never the run's, however many
// bounds there are.
//
// Mutation: drop the second bound (guardBounds reading only the base the diff
// is measured against) and this run is refused for main's own file, which the
// assertion below catches; widen a bound until it swallows the run's own
// commit (subtract HEAD, say) and the refusal disappears, which runRefused
// catches.
func TestARunWithBothBoundsStillRefusesItsOwnWorkflowFile(t *testing.T) {
	spec := &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: integrationBranch, Workflow: "app", Task: "Add a feature"}
	h := newHarness(t, "", spec)
	addBaseBranch(t, h, integrationBranch)
	marker := ""
	editCI := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		// Both bases the guard subtracts move on while the run works, each
		// with a workflow file of its own, and the run merges them in as one
		// told to keep up with its bases does.
		advanceBranch(t, h.remote, integrationBranch, map[string]string{".github/workflows/integration.yml": "on: push\n"})
		advanceBranch(t, h.remote, "main", map[string]string{".github/workflows/person.yml": "on: push\n"})
		// The hook is installed only now: the two advances above are pushes
		// onto the base branches, which a person makes.
		marker = refuseAll(t, h.remote, "this hook must never run")
		shell(t, req, "git fetch -q origin "+integrationBranch+" main && git merge -q --no-edit origin/"+integrationBranch+" origin/main")
		// The run's own workflow change, which is the run's whatever the
		// bases did.
		shell(t, req, "mkdir -p .github/workflows && echo 'on: pull_request' > .github/workflows/mine.yml && echo x > feature.txt && git add -A && git commit -qm 'Add CI'")
		return implementVerified(t, ctx, req)
	}
	rec := runRefused(t, h, editCI, review("ship", 0))
	if !strings.Contains(rec.Reason, ".github/workflows/mine.yml") {
		t.Fatalf("reason = %q, want it to name the run's own workflow file", rec.Reason)
	}
	for _, other := range []string{".github/workflows/person.yml", ".github/workflows/integration.yml"} {
		if strings.Contains(rec.Reason, other) {
			t.Fatalf("reason = %q, and it blames the run for %s, which a base branch's own commit added", rec.Reason, other)
		}
	}
	if exists(marker) || remoteHasBranch(t, h) {
		t.Fatal("a push was attempted")
	}
}

// "The agent made no commits" is about the run's own commits: a run cut
// from a ref with commits of its own, which the agent adds nothing to, made
// none — measured against the base branch alone it always looks ahead.
func TestARunFromARefThatMakesNoCommitsSaysSo(t *testing.T) {
	h := consoleHarness(t)
	nothing := func(*testing.T, context.Context, agent.Request) (agent.Result, error) { return agent.Result{}, nil }
	rec, err := h.run(t, nothing, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != runstore.OutcomeDraft || rec.Reason != "the agent made no commits" {
		t.Fatalf("record = %+v", rec)
	}
}

// The run's own base is the commit its branch was cut from, recorded at
// bootstrap so the guard's comparison is auditable.
func TestTheRunRecordsTheCommitItWasCutFrom(t *testing.T) {
	h := consoleHarness(t)
	console := testutil.Git(t, h.remote, "rev-parse", "refs/heads/console")
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.StartSHA != console {
		t.Fatalf("recorded start_sha = %q, want console's tip %s", rec.StartSHA, console)
	}
	stored, rerr := h.store.ReadRecord(context.Background())
	if rerr != nil || stored.StartSHA != console {
		t.Fatalf("stored start_sha = %q, %v", stored.StartSHA, rerr)
	}
}

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
		"runs/" + repoSlug + "/" + runID + "/work.bundle",
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
