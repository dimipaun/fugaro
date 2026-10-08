# Checkpoint Pushes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** While a first run's stage runs, the runner pushes the run branch's new commits every 3 minutes. The push is fast-forward only and uses the runner's credentials. The draft PR opens at the first such push, and its status section says the work is not verified until a passing test covers the pushed commit. A container that dies mid-stage then loses at most a few minutes of committed work instead of all of it. Readiness, reviewers and finalize do not change.

**Architecture:**
- **The checkpoint goroutine.** `stage()` (`internal/runner/runner.go`) starts it next to the halt watcher. It lives from the agent's start to its return, on the stage's own context, and is cancelled and joined when the agent returns.
- **Each tick** (`checkpoint`, new file `internal/runner/checkpoint.go`) does the following:
  - reads the run branch's tip only when the checkout is settled on it (`gitops.Repo.CheckpointTip`);
  - skips the tip if it is already pushed;
  - scans the new commits for values the run redacts (`gitops.Repo.ScanRange`);
  - applies finalize's workflow guard to that exact commit (`workflowGuardAt`);
  - pushes fast-forward only, by SHA and to origin's URL (`gitops.Repo.PushFastForward`).
- **After a push**, `afterCheckpoint` opens the draft (`openDraftPR`, split out of `openDraft`) or rewrites its status section (`statusUpdate`). The section gains a not-verified head and a `branch at <sha>` part.
- **Settings and prompt.** A config key `git.pr.checkpoints` (default true) turns checkpoints off. A system-prompt line tells the agent to commit early.

**Tech Stack:** Go 1.27. The work happens in `internal/gitops` (the git CLI), `internal/runner`, `internal/config`, `internal/gitprov/fake`, `schemas/fugaro.schema.json` and the docs. Tests use local bare remotes (`testutil.NewRemote`), remote hooks, the fake git provider (`fake.Provider`, with the `auditProvider` wrapper) and the scripted agent (`harness`, `step`).

**Spec:** [docs/design/checkpoint-pushes.md](../design/checkpoint-pushes.md)

**Checked before review:** the code of Tasks 1 to 6 was applied to a scratch copy of `main` (724ab83). `go vet` is clean, and the focused tests named in Tasks 1 to 6 pass with `-race` (the checkpoint tests four times in a row, `-count=4`), alongside the existing early-draft, refused-push, halt, cancel and follow-up tests. Each checkpoint tick runs about a dozen git commands, roughly 0.5 s on a laptop, so the tests wait for finished ticks (`CountCheckpointTicks`) and for the saved `pushed_head`, never for wall-clock time. The remote ref is visible before the remote's hooks finish.

## Decisions (veto any before execution starts)

- **C1. The interval is 3 minutes, a named constant, with no interval knob.** The code has `const checkpointInterval = 3 * time.Minute` and `var checkpointEvery = checkpointInterval`. Only tests change the variable, through `runner.SetCheckpointEvery`. A tick whose branch has not changed makes no network call, so a shorter interval would cost nothing while the agent is idle. A longer one loses more work. Veto alternative: `git.pr.checkpoint_minutes`, an integer from 1 to 30 (one more config task).
- **C2. Checkpoints have their own opt-out, `git.pr.checkpoints: false` (default true).** Data-loss protection should be on unless someone turns it off on purpose. `git.pr.early_draft` keeps its meaning, "the PR opens early", and the two keys combine:
  - both on (the default): checkpoints push the branch and the first one opens the draft;
  - `early_draft: false`: checkpoints push the branch and the PR opens at finalize;
  - `checkpoints: false`: 0.5.0's pushes exactly (the verified boundary and finalize).

  The key sits under `git.pr` because the reason to turn it off is the PR's CI cost. An older CLI or job image refuses the key as unknown, so the docs say to set it only once everyone is on 0.5.1. Veto alternative: one key for both behaviours (`early_draft: false` also stops checkpoints). The cost is that repositories without drafts lose the protection.
- **C3. The draft opens at the first checkpoint push, unverified.** This supersedes M9e ruling E1, "the remote branch is always a verified state". The section's head is `**Running: work in progress, not verified**` while the pushed commit has no passing clean test. The part `branch at <sha>: not verified|verified` is added after the verify part, and the stage part reads `checkpoint during stage <name>` for an update made mid-stage. The PR title stays unprefixed (M9e E5: the draft badge says it). Readiness (`Decide`), the flip to ready and `ApplyReady` are unchanged.
- **C4. Fast-forward only, never forced, and only from a settled checkout.** `CheckpointTip` refuses with `ErrGitBusy` when HEAD is not the symbolic ref `refs/heads/fugaro/<id>` or when any of `rebase-merge`, `rebase-apply`, `MERGE_HEAD`, `CHERRY_PICK_HEAD`, `REVERT_HEAD` or `BISECT_LOG` exists in the git directory. A tick that sees this does nothing and is silent. `PushFastForward` does the following:
  - pushes `<sha>:refs/heads/<branch>`, the SHA that was read, so the agent moving the branch mid-push changes nothing;
  - pushes to origin's URL (`git remote get-url origin`), not the remote's name, so git writes no `refs/remotes/origin/…` and takes no ref lock the agent's own `git fetch` could meet;
  - refuses with `ErrNotFastForward` when the remote tip is not an ancestor of the SHA;
  - does nothing when the remote is already there (an agent that pushed by itself).

  A rewritten history (amend, rebase, reset) logs one warning per run, and ticks keep trying. The verified-boundary push and finalize keep `Push` (force-with-lease over a tip that is the run's own, `tipBelongsToRun`), so they reconcile a rewritten branch as they do today. Veto alternative: checkpoints also use `Push` (lease over the run's own tips). That keeps them going after a rewrite, but a checkpoint could then replace commits already on the remote.
- **C5. Checkpoints use the same guards as finalize, plus a secret scan.**
  - `workflowGuardAt(ctx, sha)` (GitHub only, as today) and a permanent refusal classified by `asRefusal` stop checkpoints for the run. Finalize then ends the run as today: `endRefused`, which saves the work bundle.
  - `ScanRange(since, sha)` runs before each push, where since is `pushed_head`, or the work base before the first push. It uses the run's redactor (`agent.RedactFunc(r.secretList())`). A hit stops checkpoints for the run, because every later push would carry that commit.
  - A binary or too-large range is pushed, as finalize would push it.

  Finalize itself still pushes without scanning; that is out of scope and listed as future work.
- **C6. Follow-ups do not checkpoint in 0.5.1.** Their branch is an open PR that may be ready and have reviewers, so an unverified checkpoint would be visible to them and could be merged. Finalize's `PushExisting(branch, startSHA)` would also refuse a branch the run itself moved mid-run, with `ErrForeignTip`. Veto alternative (later): checkpoint to a side branch `fugaro/<follow-up id>` and delete it at finalize.
- **C6a. Every check uses the SHA the tick read, never HEAD.** The agent commits concurrently, so the tick reads the tip once and then checks that SHA throughout: the commits ahead (`CountAhead`), the secret scan (`ScanRange`), the workflow guard (`WorkflowFilesIn`) and the push. The prototype showed the failure this prevents. A tip read just before the agent's first commit is the base, and checking "ahead" on HEAD instead pushed the base itself as a checkpoint.
- **C7. The checkpointer's lifetime.** It starts in `stage()` right before `Agent.Run` and stops right after it (and in a `defer`, so a panic cannot leave it running). Stopping cancels its context and waits for it, which aborts a push or PR call in flight. Such a push leaves at most a remote branch with no `pushed_head`, and the next boundary or finalize pushes over it (the tip is in the run's reflog). Such a PR create leaves a PR that finalize finds by branch (M9e E4). Other rules:
  - the first tick comes one interval after the agent starts; there is no tick at stage start and no flush at stage end;
  - a tick does nothing once a halt or a cancel is recorded, or the time budget is spent;
  - the stage context's deadline never passes `finalize_reserve`, and a cancel cancels it.

  No checkpoint runs between stages or during finalize.
- **C8. Opening the draft is tried at most twice per run (`checkpointOpenTries`), once per checkpoint push.** Each try is `ensurePR`'s 3 attempts within `earlyOpenTimeout`. After that the verified boundary (`afterStage`'s `openDraft`) or finalize opens the PR. A failed push is a warning, logged once per kind per run, and the next tick tries again.
- **C9. The prompt line goes only where it is true:** first runs with checkpoints on. It reads: commit early and often, only pushed commits survive a dead container, uncommitted changes are lost, add new commits rather than amend or rebase.
- **C10. Nothing new in `result.json`.** `pushed_head` is saved after every checkpoint push (as `pushBranch` does), so a crashed run's record says what reached the remote. Checkpoints are logged (`checkpoint pushed`, with the stage, the short SHA and a count).
- **C11. Release 0.5.1.** This is a patch release, shipped with `fugaro image refresh`. `docs/releases/v0.5.1.md` gets one section; it is created if image refresh's Task 12 has not created it yet. The runner reaches a repository only when its job image is rebuilt from 0.5.1.

## Global Constraints

Every task's requirements include these.

From the design:
- A checkpoint pushes only `fugaro/<run id>`, fast-forward only, by SHA, with the runner's credentials. It never commits, never reads or writes the working tree or the index, and writes nothing in the checkout's `.git`.
- A checkpoint never fails, blocks or delays the run beyond the cancellation of its own in-flight call. Every error is a warning, and a panic is recovered inside the goroutine.
- A checkpoint is never less guarded than finalize's push: the workflow guard, the host's permanent refusals, plus the secret scan.
- Readiness, reviewers and labels at ready, finalize, follow-ups and `early_draft: false` behave as in 0.5.0, except for the documented changes: an unverified draft, and branch pushes mid-stage.
- Not doing: uncommitted-work snapshots, follow-up checkpoints, an interval knob, a finalize secret scan.

Project rules:
- No live cloud applies in tests. Use local bare remotes, remote hooks, the fake provider and the scripted agent. Never touch EdgeWeb or EdgeServer, and handle no real secrets.
- Subagent-driven development in one git worktree for this PR group (`.worktrees/checkpoint-pushes`), with a fresh implementer per task. The token-economy rule applies: **Task 4 gets its own review** (it changes every run's stage loop), and the rest are reviewed once on the branch at the end. Dogfood runs, if any, also run from git worktrees.
- Every CI check (`test`, `terraform`, `rules`) is read before merge, each job's log and not only the summary.
- Docs must match behaviour. Task 7's docs test pins the interval, the key and the removed "first verified push" wording, and the `rules` tests (`plugin/*_test.go`) must stay green.
- Releases go through `/new-release` with `docs/releases/vX.Y.Z.md` merged first.
- **The `internal/runner` package is slow (about 19 minutes in full).** Each task runs only the focused tests it names (`-run`), and the PR group runs **one** full `go test ./internal/runner/...` at the end (Task 8). CI runs with `-race`, so the focused runner tests run with `-race` too.

## Review Focus

The five failure modes most likely to hit a user, each pinned by a test:

1. **Committed work is still lost when the container dies mid-stage (the incident).** Expected: a commit made in a long stage is on the remote within one interval, before the stage ends, and a failed push is retried on the next tick. Pinned by `TestCheckpointPushesCommittedWorkMidStage` and `TestCheckpointFailureNeverFailsTheRun` (Task 4).
2. **A checkpoint publishes what finalize would not, or publishes it earlier.** Cases: a workflow file on GitHub, or a value the run redacts that a later commit removes. Expected: nothing is pushed, checkpoints stop for the run, and finalize ends the run as it does today. Pinned by `TestCheckpointSkipsWorkflowCommits` and `TestCheckpointStopsOnAKnownSecret` (Task 4).
3. **A checkpoint fights the agent's git.** It must not overwrite a rewritten history, push a half-done rebase, or write a ref the agent's fetch could lock on. Expected: fast-forward only, with one warning, and finalize reconciles. Nothing is pushed while a rebase or merge is in progress, and no local ref is written. Pinned by `TestPushFastForwardRefusesRewrittenHistory`, `TestPushFastForwardPushesAndSkipsWhenCurrent` and `TestCheckpointTipOnlyWhenSettled` (Task 1), and `TestCheckpointNeverRewritesThePushedBranch` (Task 4).
4. **The early draft misleads: it looks verified or ready, or notifies someone.** Expected: a draft with no reviewers or labels, whose number is saved before any other provider call, marked `not verified` until a passing clean test covers the pushed commit. It turns `verified` after one, and becomes ready only at finalize. Pinned by `TestCheckpointOpensDraftMarkedNotVerified` and `TestStatusSaysVerifiedAfterAPassingTest` (Task 5), with `auditProvider`'s every-call checks.
5. **Checkpoints ignore an opt-out, a halt, a cancel or a follow-up.** Expected: no mid-stage push with `git.pr.checkpoints: false`, after a recorded halt or cancel, or in a follow-up. `early_draft: false` pushes the branch but opens no PR mid-run. Pinned by `TestCheckpointsOffPushesNothingMidStage`, `TestNoCheckpointAfterHalt`, `TestNoCheckpointAfterCancel` and `TestFollowUpDoesNotCheckpoint` (Task 4), and `TestEarlyDraftFalseCheckpointsWithoutPR` (Task 5).

---

## File Structure

| Path | Responsibility | Task |
|---|---|---|
| `internal/gitops/checkpoint.go` (new), `gitops_test.go` | `CheckpointTip`, `PushFastForward`, `ErrGitBusy`, `ErrNotFastForward` | 1 |
| `internal/gitops/rejected.go`, `rejected_test.go` | `ScanRange`, `WorkflowFilesIn` | 1 |
| `internal/config/config.go`, `defaults.go`, `example.yaml`, `config_test.go`, `schemas/fugaro.schema.json`, `testdata/config/valid/full.yaml` | `git.pr.checkpoints` | 2 |
| `internal/gitprov/fake/fake.go`, `fake_test.go` | `Snapshot` for race-free reads in tests | 3 |
| `internal/runner/checkpoint.go` (new), `runner.go`, `refused.go`, `prflow.go`, `export_test.go`, `checkpoint_test.go` (new) | the checkpointer and its guards | 4 |
| `internal/runner/prflow.go`, `checkpoint.go`, `checkpoint_test.go` | the draft at the first checkpoint, the section | 5 |
| `internal/runner/prompts.go`, `runner.go`, `pure_test.go` | the prompt line | 6 |
| `docs/design/v1.md`, `docs/git-providers.md`, `docs/gcp-setup.md`, `README.md`, `plugin/skills/working/SKILL.md`, `plugin/skills/working/reference/launch.md`, `plugin/skills/working/reference/diagnose.md`, `internal/runner/docs_checkpoint_internal_test.go` (new) | docs | 7 |
| none | full suite, PR | 8 |
| `docs/releases/v0.5.1.md` | release | 9 |

## PR group

One PR, branch `checkpoint-pushes`, Tasks 1 to 8 in order. Task 9 runs after the merge. Tasks 1, 2 and 3 are independent of each other; Task 4 needs all three.

---

### Task 1: Fast-forward pushes, a settled tip, and ranges ending at a commit (`internal/gitops`)

**Files:**
- Create: `internal/gitops/checkpoint.go`
- Modify: `internal/gitops/rejected.go`, `internal/gitops/gitops.go`
- Test: `internal/gitops/gitops_test.go`, `internal/gitops/rejected_test.go`

**Interfaces:**
- Consumes: `checkRunBranch`, `remoteTip`, `classified`, `(*Repo).git`, `(*Repo).OriginURL`, `scanWork`, `MaxScanBytes`, `WorkflowDir`.
- Produces:
  ```go
  var ErrNotFastForward = errors.New("the branch on origin is not an ancestor of the commit to push")
  var ErrGitBusy = errors.New("the checkout is not settled on the branch")
  func (r *Repo) CheckpointTip(ctx context.Context, branch string) (string, error)
  func (r *Repo) PushFastForward(ctx context.Context, branch, sha string) error
  func (r *Repo) ScanRange(ctx context.Context, since, until string, scan func(text string) bool) (WorkScan, error)
  func (r *Repo) WorkflowFilesIn(ctx context.Context, since, until string) ([]string, error)
  func (r *Repo) CountAhead(ctx context.Context, base, until string) (int, error) // AheadOf for any commit
  // scanWork gains until: scanWork(ctx, since, until string, scan func(string) bool, maxBytes int64)
  ```

- [ ] **Step 1: Write the failing tests**

Append to `internal/gitops/gitops_test.go`:

```go
func TestPushFastForwardPushesAndSkipsWhenCurrent(t *testing.T) {
	repo, remote := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	if err := repo.CommitEmpty(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	one, _ := repo.HeadSHA(ctx)
	if err := repo.PushFastForward(ctx, "fugaro/x", one); err != nil {
		t.Fatal(err)
	}
	if got := testutil.Git(t, remote, "rev-parse", "refs/heads/fugaro/x"); got != one {
		t.Fatalf("remote = %s, want %s", got, one)
	}
	if err := repo.CommitEmpty(ctx, "two"); err != nil {
		t.Fatal(err)
	}
	two, _ := repo.HeadSHA(ctx)
	for range 2 { // the second finds the remote already there and pushes nothing
		if err := repo.PushFastForward(ctx, "fugaro/x", two); err != nil {
			t.Fatal(err)
		}
	}
	if got := testutil.Git(t, remote, "rev-parse", "refs/heads/fugaro/x"); got != two {
		t.Fatalf("remote = %s, want %s", got, two)
	}
	// Pushed to the URL, not the remote's name: no remote-tracking ref,
	// so no ref lock the agent's own git could meet.
	if got := testutil.Git(t, repo.Dir, "for-each-ref", "refs/remotes/origin/fugaro/"); got != "" {
		t.Fatalf("the push wrote a local ref: %q", got)
	}
}

func TestPushFastForwardRefusesRewrittenHistory(t *testing.T) {
	repo, remote := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	if err := repo.CommitEmpty(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	one, _ := repo.HeadSHA(ctx)
	if err := repo.PushFastForward(ctx, "fugaro/x", one); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, repo.Dir, "commit", "--quiet", "--amend", "--allow-empty", "-m", "one, amended")
	amended, _ := repo.HeadSHA(ctx)
	if err := repo.PushFastForward(ctx, "fugaro/x", amended); !errors.Is(err, ErrNotFastForward) {
		t.Fatalf("err = %v, want ErrNotFastForward", err)
	}
	if got := testutil.Git(t, remote, "rev-parse", "refs/heads/fugaro/x"); got != one {
		t.Fatalf("the pushed commit was replaced: remote at %s, want %s", got, one)
	}
}

func TestPushFastForwardRefusesNonRunBranches(t *testing.T) {
	repo, _ := setup(t)
	head, _ := repo.HeadSHA(ctx)
	if err := repo.PushFastForward(ctx, "main", head); err == nil || !strings.Contains(err.Error(), "only pushes") {
		t.Fatalf("err = %v, want the run-branch refusal", err)
	}
}

func TestCountAheadOfACommit(t *testing.T) {
	repo, _ := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	base, _ := repo.HeadSHA(ctx)
	if err := repo.CommitEmpty(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	// The tip a checkpoint read before the agent's commit is the base:
	// nothing of the run's own, whatever HEAD has meanwhile.
	if n, err := repo.CountAhead(ctx, "main", base); err != nil || n != 0 {
		t.Fatalf("CountAhead(base) = %d, %v; want 0", n, err)
	}
	if n, err := repo.CountAhead(ctx, "main", "HEAD"); err != nil || n != 1 {
		t.Fatalf("CountAhead(HEAD) = %d, %v; want 1", n, err)
	}
}

func TestCheckpointTipOnlyWhenSettled(t *testing.T) {
	repo, _ := setup(t)
	if err := repo.CheckoutNewBranch(ctx, "main", "fugaro/x"); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFiles(t, repo.Dir, map[string]string{"a.txt": "a\n"})
	if _, err := repo.CommitAll(ctx, "add a"); err != nil {
		t.Fatal(err)
	}
	head, _ := repo.HeadSHA(ctx)
	if got, err := repo.CheckpointTip(ctx, "fugaro/x"); err != nil || got != head {
		t.Fatalf("CheckpointTip = %q, %v; want %s", got, err, head)
	}
	if _, err := repo.CheckpointTip(ctx, "fugaro/y"); !errors.Is(err, ErrGitBusy) {
		t.Fatalf("another branch: err = %v, want ErrGitBusy", err)
	}
	gitDir := filepath.Join(repo.Dir, ".git")
	for _, name := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "BISECT_LOG", "rebase-merge", "rebase-apply"} {
		p := filepath.Join(gitDir, name)
		if err := os.WriteFile(p, []byte(head+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.CheckpointTip(ctx, "fugaro/x"); !errors.Is(err, ErrGitBusy) {
			t.Errorf("%s present: err = %v, want ErrGitBusy", name, err)
		}
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	testutil.Git(t, repo.Dir, "checkout", "--quiet", "--detach")
	if _, err := repo.CheckpointTip(ctx, "fugaro/x"); !errors.Is(err, ErrGitBusy) {
		t.Fatalf("detached HEAD: err = %v, want ErrGitBusy", err)
	}
}
```

Append to `internal/gitops/rejected_test.go`:

```go
func TestScanRangeStopsAtUntil(t *testing.T) {
	repo := branchWith(t,
		commitSpec{msg: "clean", files: map[string]string{"a.txt": "a\n"}},
		commitSpec{msg: "leak", files: map[string]string{"k.txt": "S3CRET-VALUE\n"}},
	)
	clean := testutil.Git(t, repo.Dir, "rev-parse", "HEAD~1")
	if res, err := repo.ScanRange(ctx, "origin/main", clean, hasSecret); err != nil || res.Hit {
		t.Fatalf("up to the clean commit: %+v, %v; want no hit", res, err)
	}
	if res, err := repo.ScanRange(ctx, "origin/main", "HEAD", hasSecret); err != nil || !res.Hit {
		t.Fatalf("up to HEAD: %+v, %v; want a hit", res, err)
	}
}

func TestWorkflowFilesInStopsAtUntil(t *testing.T) {
	repo := branchWith(t,
		commitSpec{msg: "code", files: map[string]string{"a.txt": "a\n"}},
		commitSpec{msg: "ci", files: map[string]string{".github/workflows/ci.yml": "on: push\n"}},
	)
	before := testutil.Git(t, repo.Dir, "rev-parse", "HEAD~1")
	if got, err := repo.WorkflowFilesIn(ctx, "origin/main", before); err != nil || len(got) != 0 {
		t.Fatalf("before the CI commit: %v, %v", got, err)
	}
	if got, err := repo.WorkflowFilesIn(ctx, "origin/main", "HEAD"); err != nil || len(got) != 1 || got[0] != ".github/workflows/ci.yml" {
		t.Fatalf("at HEAD: %v, %v", got, err)
	}
}
```

In `rejected_test.go`, change the existing direct call (line 300) from `repo.scanWork(ctx, "origin/main", func(string) bool { return false }, 1)` to `repo.scanWork(ctx, "origin/main", "HEAD", func(string) bool { return false }, 1)`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/gitops/ -run 'TestPushFastForward|TestCheckpointTip|TestCountAhead|TestScanRange|TestWorkflowFilesIn'`
Expected: FAIL to compile: `repo.PushFastForward undefined`, `repo.CheckpointTip undefined`, `repo.CountAhead undefined`, `repo.ScanRange undefined`, `repo.WorkflowFilesIn undefined`, and too many arguments in the call to `repo.scanWork`.

- [ ] **Step 3: Write the implementation**

Create `internal/gitops/checkpoint.go`:

```go
package gitops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrNotFastForward means the run branch on origin is not an ancestor of
// the commit to push: the agent rewrote commits already pushed, or someone
// else pushed. A checkpoint never forces, so it leaves the branch as it is.
var ErrNotFastForward = errors.New("the branch on origin is not an ancestor of the commit to push")

// ErrGitBusy means the checkout is not settled on the branch: HEAD is
// elsewhere (detached, another branch) or a merge, rebase, cherry-pick,
// revert or bisect is in progress. A checkpoint waits for its next tick.
var ErrGitBusy = errors.New("the checkout is not settled on the branch")

// inProgress are what git keeps in the git directory while an operation
// is unfinished.
var inProgress = []string{"rebase-merge", "rebase-apply", "MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "BISECT_LOG"}

// CheckpointTip returns the tip of the local branch when the checkout is
// settled on it: HEAD is the symbolic ref refs/heads/<branch> and no
// operation is in progress. It only reads: it takes no lock and writes
// nothing, so it is safe while the agent's own git runs.
func (r *Repo) CheckpointTip(ctx context.Context, branch string) (string, error) {
	head, err := r.git(ctx, "symbolic-ref", "-q", "HEAD")
	if err != nil || head != "refs/heads/"+branch {
		return "", ErrGitBusy // detached (symbolic-ref exits 1) or elsewhere
	}
	gitDir, err := r.git(ctx, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}
	for _, name := range inProgress {
		if _, err := os.Stat(filepath.Join(gitDir, name)); err == nil {
			return "", ErrGitBusy
		}
	}
	return r.git(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch+"^{commit}")
}

// PushFastForward pushes sha to branch on origin only as a fast-forward: it
// never forces. When origin already has sha there it does nothing; when
// origin's tip is not an ancestor of sha (or is a commit the checkout
// never had) it refuses with ErrNotFastForward. It pushes the given sha,
// not HEAD, and to origin's URL rather than the remote's name, so git
// writes no remote-tracking ref in the checkout. Like Push, it only pushes
// fugaro/<run-id> branches.
func (r *Repo) PushFastForward(ctx context.Context, branch, sha string) error {
	if err := checkRunBranch(ctx, r, branch); err != nil {
		return err
	}
	ref := "refs/heads/" + branch
	tip, err := r.remoteTip(ctx, ref)
	if err != nil {
		return fmt.Errorf("reading %s on origin: %w", branch, err)
	}
	if tip == sha {
		return nil
	}
	if tip != "" {
		if _, err := r.git(ctx, "merge-base", "--is-ancestor", tip, sha); err != nil {
			return fmt.Errorf("%s on origin is at %s: %w", branch, tip, ErrNotFastForward)
		}
	}
	url, err := r.OriginURL(ctx)
	if err != nil {
		return err
	}
	// No --force of any kind: the remote itself refuses a non-fast-forward.
	_, err = r.git(ctx, "push", "--quiet", url, sha+":"+ref)
	return classified(err)
}
```

In `internal/gitops/gitops.go`, replace `AheadOf` with:

```go
// AheadOf counts commits on HEAD that origin/<base> does not have.
func (r *Repo) AheadOf(ctx context.Context, base string) (int, error) {
	return r.CountAhead(ctx, base, "HEAD")
}

// CountAhead counts the commits of until that origin/<base> does not have.
// A checkpoint asks about the tip it read, never about HEAD, which the
// agent may have moved since.
func (r *Repo) CountAhead(ctx context.Context, base, until string) (int, error) {
	out, err := r.git(ctx, "rev-list", "--count", "origin/"+base+".."+until)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(out)
}
```

In `internal/gitops/rejected.go`:
- replace the body of `WorkflowFiles` with `return r.WorkflowFilesIn(ctx, since, "HEAD")` and add, after it:

```go
// WorkflowFilesIn is WorkflowFiles for the commits since...until, until
// being any commit rather than HEAD: a checkpoint checks the exact commit
// it pushes.
func (r *Repo) WorkflowFilesIn(ctx context.Context, since, until string) ([]string, error) {
	out, err := r.gitRaw(ctx, "diff", "--name-only", "-z", "--no-renames", "--no-ext-diff", "--no-textconv", since+"..."+until, "--", WorkflowDir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if strings.HasPrefix(f, WorkflowDir) {
			files = append(files, f)
		}
	}
	return files, nil // git lists paths in sorted order
}
```

- change `ScanWork` to `return r.scanWork(ctx, since, "HEAD", scan, MaxScanBytes)`, and add:

```go
// ScanRange is ScanWork for the commits since..until, until being any
// commit rather than HEAD.
func (r *Repo) ScanRange(ctx context.Context, since, until string, scan func(text string) bool) (WorkScan, error) {
	return r.scanWork(ctx, since, until, scan, MaxScanBytes)
}
```

- change `scanWork`'s signature to `func (r *Repo) scanWork(ctx context.Context, since, until string, scan func(text string) bool, maxBytes int64) (WorkScan, error)` and its range line to `rng := []string{"^" + since, until}`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/gitops/`
Expected: PASS. This is the whole package; it takes seconds.

- [ ] **Step 5: Commit**

```bash
git add internal/gitops/checkpoint.go internal/gitops/gitops.go internal/gitops/rejected.go internal/gitops/gitops_test.go internal/gitops/rejected_test.go
git commit -m "gitops: fast-forward pushes of a settled branch tip, ranges ending at a commit"
```

---

### Task 2: The `git.pr.checkpoints` key

**Files:**
- Modify: `internal/config/config.go`, `internal/config/defaults.go`, `internal/config/example.yaml`, `schemas/fugaro.schema.json`, `testdata/config/valid/full.yaml`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: `PRSettings`, `applyDefaults`, `earlyDraftBase`.
- Produces:
  ```go
  // PRSettings gains:
  Checkpoints *bool `yaml:"checkpoints"`
  func (p PRSettings) CheckpointsOn() bool
  ```

- [ ] **Step 1: Write the failing test**

Append to `internal/config/config_test.go`:

```go
func TestPRCheckpointsDefaultsTrue(t *testing.T) {
	for _, tc := range []struct {
		pr   string
		want bool
	}{{"", true}, {"  pr: { checkpoints: true }\n", true}, {"  pr: { checkpoints: false, early_draft: false }\n", false}} {
		cfg, problems := Parse([]byte(strings.Replace(earlyDraftBase(t), "git:\n", "git:\n"+tc.pr, 1)))
		if len(problems) > 0 {
			t.Fatalf("%q: %v", tc.pr, problems)
		}
		if got := cfg.Git.PR.CheckpointsOn(); got != tc.want {
			t.Errorf("%q: CheckpointsOn = %v, want %v", tc.pr, got, tc.want)
		}
	}
	if !(PRSettings{}).CheckpointsOn() {
		t.Error("the zero PRSettings must default to checkpoints on")
	}
}
```

In `testdata/config/valid/full.yaml`, change `  pr: { labels: [fugaro], reviewers: [octocat] }` to `  pr: { labels: [fugaro], reviewers: [octocat], early_draft: true, checkpoints: false }`, so the schema corpus (`schemas.TestFugaroSchemaCorpus`) covers the key.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/config/ -run TestPRCheckpointsDefaultsTrue && go test ./schemas/ -run TestFugaroSchemaCorpus`
Expected: FAIL to compile: `cfg.Git.PR.CheckpointsOn undefined`. The schema test, run alone, fails with `full.yaml: schema rejects a valid config` (`additionalProperties`).

- [ ] **Step 3: Write the implementation**

In `internal/config/config.go`, inside `PRSettings` after `EarlyDraft`:

```go
	// Checkpoints pushes a first run's new commits to its branch every few
	// minutes while a stage runs, fast-forward only, so a container that
	// dies keeps its committed work. Unset means true (applyDefaults).
	Checkpoints *bool `yaml:"checkpoints"`
```

and after `EarlyDraftOn`:

```go
// CheckpointsOn reports whether a first run pushes its new commits while a
// stage runs: true unless git.pr.checkpoints is false.
func (p PRSettings) CheckpointsOn() bool { return p.Checkpoints == nil || *p.Checkpoints }
```

In `internal/config/defaults.go`, after the `EarlyDraft` default:

```go
	if c.Git.PR.Checkpoints == nil {
		t := true
		c.Git.PR.Checkpoints = &t
	}
```

In `internal/config/example.yaml`, after the `early_draft` line:

```yaml
    # checkpoints: true       # push new commits every 3 minutes while a stage runs (fast-forward only); false: only at verified stage ends and at the end
```

In `schemas/fugaro.schema.json`, in `git.pr.properties` after `early_draft`:

```json
            "checkpoints": { "type": "boolean", "description": "Push a run's new commits to its branch every 3 minutes while a stage runs, fast-forward only, so a container that dies keeps its committed work (default true). With early_draft, the first checkpoint opens the draft pull request, marked not verified. False pushes only at verified stage ends and at the end. Every push to a branch with an open pull request runs its CI. Fugaro 0.5.1 or later." }
```

(add the comma after the `early_draft` entry.)

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/config/ ./schemas/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config schemas/fugaro.schema.json testdata/config/valid/full.yaml
git commit -m "config: git.pr.checkpoints, default true"
```

---

### Task 3: The fake provider can be read while the runner calls it

**Files:**
- Modify: `internal/gitprov/fake/fake.go`
- Test: `internal/gitprov/fake/fake_test.go`

**Interfaces:**
- Consumes: `Provider.mu`, `Provider.State`, `Provider.load`.
- Produces:
  ```go
  func (p *Provider) Snapshot() State // a copy of State taken under the provider's lock
  ```

The runner tests read `h.provider.State` from the scripted agent's step. Until now no provider call ran while a step ran. A checkpoint calls the provider from its own goroutine during a step, so with `-race` a test must read through the lock.

- [ ] **Step 1: Write the failing test**

Append to `internal/gitprov/fake/fake_test.go` (add `"fmt"` and `"sync"` to its imports):

```go
func TestSnapshotIsSafeDuringCalls(t *testing.T) {
	p := &Provider{Repo: "acme/app"}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 50 {
			if _, err := p.EnsurePR(context.Background(), gitprov.PRSpec{Branch: fmt.Sprintf("fugaro/r%d", i), Base: "main", Title: "t", Draft: true}); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	for range 50 {
		st := p.Snapshot()
		for _, pr := range st.PRs {
			_ = pr.Title
		}
	}
	wg.Wait()
	if n := len(p.Snapshot().PRs); n != 50 {
		t.Fatalf("PRs = %d, want 50", n)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -race ./internal/gitprov/fake/ -run TestSnapshotIsSafeDuringCalls`
Expected: FAIL to compile: `p.Snapshot undefined`.

- [ ] **Step 3: Write the implementation**

In `internal/gitprov/fake/fake.go`, after `save`:

```go
// Snapshot is a copy of the state taken under the provider's lock, for a
// test that reads it while the runner may be calling the provider from
// another goroutine (a checkpoint). The PRs and calls are copied; the
// slices inside each PR are not, and must only be read.
func (p *Provider) Snapshot() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.State // with Path set, every call has already saved and loaded it
	st.PRs = slices.Clone(p.State.PRs)
	st.Calls = slices.Clone(p.State.Calls)
	return st
}
```

(`slices` is already imported. Snapshot does not reload the state file: a load writes `State`, which `auditProvider` reads without the lock in the runner's goroutine.)

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/gitprov/fake/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/gitprov/fake
git commit -m "gitprov/fake: Snapshot, a locked copy of the state"
```

---

### Task 4: The checkpointer (critical: own review)

**Files:**
- Create: `internal/runner/checkpoint.go`, `internal/runner/checkpoint_test.go`
- Modify: `internal/runner/runner.go` (the `run` struct, `stage()`), `internal/runner/refused.go` (`workflowGuardAt`), `internal/runner/prflow.go` (`refreshForPush`), `internal/runner/export_test.go`

**Interfaces:**
- Consumes:
  - from Task 1: `gitops.(*Repo).CheckpointTip`, `CountAhead`, `PushFastForward`, `ScanRange`, `WorkflowFilesIn`, `gitops.ErrGitBusy`, `gitops.ErrNotFastForward`;
  - from Task 2: `config.PRSettings.CheckpointsOn`;
  - from Task 3: `fake.(*Provider).Snapshot`;
  - existing: `refreshGitAuth`, `warnAuthRefresh`, `authValidity`, `asRefusal`, `refusalText`, `workBase`, `haltValue`, `isCancelled`, `budget.Exhausted`, `agent.RedactFunc`, `secretList`, `redact`, `save`, `shortSHA`, `pushTimeout`;
  - in tests: `prHarness`, `prCfg`, `newHarness`, `followUpHarness`, `newHandleBox`, `runCapHalt`, `implement`, `implementVerified`, `review`, `shell`, `blockUntilDone`, `runRefused`, `remoteHasBranch`, `mustReady`, `followID`, `runID`, `runner.SecretEnvsVar`.
- Produces:
  ```go
  const checkpointInterval = 3 * time.Minute
  var checkpointEvery = checkpointInterval
  var checkpointTicked func() // tests only
  const checkpointOpenTries = 2 // used by Task 5
  type checkpointState struct {
  	stopped   bool
  	warned    map[string]bool
  	openTries int
  	pushes    int
  }
  // run gains: ckpt checkpointState
  func (r *run) checkpointsOn() bool
  func (r *run) startCheckpoints(stageCtx context.Context, stage string) (stop func())
  func (r *run) checkpoint(ctx context.Context, stage string)
  func (r *run) checkpointClean(ctx context.Context, sha string) bool
  func (r *run) pushCheckpoint(ctx context.Context, sha string) error
  func (r *run) warnCheckpoint(key, msg string, args ...any)
  func (r *run) stopCheckpoints(reason string, args ...any)
  func (r *run) refreshForPush(ctx context.Context)
  func (r *run) workflowGuardAt(ctx context.Context, until string) *gitops.PushRejected
  // export_test.go:
  func SetCheckpointEvery(t *testing.T, d time.Duration)
  func CountCheckpointTicks(t *testing.T) func() int64
  ```

- [ ] **Step 1: Write the failing tests**

Add to `internal/runner/export_test.go`:

```go
// SetCheckpointEvery makes checkpoints tick every d for the rest of t.
func SetCheckpointEvery(t *testing.T, d time.Duration) {
	prev := checkpointEvery
	checkpointEvery = d
	t.Cleanup(func() { checkpointEvery = prev })
}

// CountCheckpointTicks counts the checkpoint ticks that finished, for the
// rest of t: a tick runs a dozen git commands, so a test waits for ticks,
// never for wall-clock time.
func CountCheckpointTicks(t *testing.T) func() int64 {
	var n atomic.Int64
	prev := checkpointTicked
	checkpointTicked = func() { n.Add(1) }
	t.Cleanup(func() { checkpointTicked = prev })
	return n.Load
}
```

(add `"sync/atomic"` to `export_test.go`'s imports.)

Create `internal/runner/checkpoint_test.go`:

```go
package runner_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

const tick = 20 * time.Millisecond

// tickCount counts the finished checkpoint ticks of the current test
// (ckptHarness sets it).
var tickCount func() int64

// ckptHarness is prHarness with checkpoints every tick, ticks counted, and
// the run's log kept in logs (read only after the run).
func ckptHarness(t *testing.T, cfg string, logs *bytes.Buffer) *harness {
	t.Helper()
	runner.SetCheckpointEvery(t, tick)
	tickCount = runner.CountCheckpointTicks(t)
	h := prHarness(t, cfg)
	h.deps.Log = slog.New(slog.NewTextHandler(logs, nil))
	return h
}

// afterTicks waits until n more checkpoint ticks have finished; with n >= 2
// at least one whole tick ran after the call.
func afterTicks(t *testing.T, n int64) {
	t.Helper()
	start := tickCount()
	waitFor(t, fmt.Sprintf("%d checkpoint ticks", n), func() bool { return tickCount() >= start+n })
}

// pushedHead is the pushed_head saved in the run record: a push is over
// (its remote hooks included) once it is saved, while the remote ref is
// visible before the remote's hooks finish.
func pushedHead(t *testing.T, h *harness) string {
	t.Helper()
	rec, err := h.store.ReadRecord(context.Background())
	if err != nil {
		return ""
	}
	return rec.PushedHead
}

// remoteTip is the run branch's tip on the remote, "" when it is absent.
func remoteTip(t *testing.T, h *harness) string {
	t.Helper()
	return strings.TrimSpace(testutil.Git(t, h.remote, "for-each-ref", "--format=%(objectname)", "refs/heads/fugaro/"+runID))
}

func localHead(t *testing.T, req agent.Request) string {
	t.Helper()
	return strings.TrimSpace(testutil.Git(t, req.Dir, "rev-parse", "HEAD"))
}

// waitFor polls cond until it holds, for at most 10 seconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// countPushes makes the remote count the pushes it accepts (post-receive).
func countPushes(t *testing.T, h *harness) func() int {
	t.Helper()
	log := filepath.Join(t.TempDir(), "pushes")
	script := "#!/bin/sh\ncat >> " + log + "\n"
	if err := os.WriteFile(filepath.Join(h.remote, "hooks", "post-receive"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return func() int {
		data, _ := os.ReadFile(log)
		return strings.Count(string(data), "\n")
	}
}

// commitWIP commits a file without verifying it.
func commitWIP(t *testing.T, req agent.Request, name string) string {
	t.Helper()
	shell(t, req, "echo "+name+" > "+name+".txt && git add -A && git commit -qm 'wip "+name+"'")
	return localHead(t, req)
}

// TestCheckpointPushesCommittedWorkMidStage is the incident: work
// committed in a long stage is on the remote before the stage ends.
func TestCheckpointPushesCommittedWorkMidStage(t *testing.T) {
	var logs bytes.Buffer
	h := ckptHarness(t, prCfg(t, 2, ", early_draft: false"), &logs)
	var pushed string
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		head := commitWIP(t, req, "wip")
		waitFor(t, "the checkpoint push", func() bool { return pushedHead(t, h) == head })
		if remoteTip(t, h) != head {
			t.Errorf("pushed_head %s is not on the remote", head)
		}
		pushed = head
		return implement("feature")(t, ctx, req)
	}
	rec, err := h.run(t, long, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if pushed == "" || rec.Outcome != runstore.OutcomeReady || remoteTip(t, h) != rec.HeadSHA {
		t.Fatalf("pushed mid-stage %q; rec = %+v", pushed, rec)
	}
	if !strings.Contains(logs.String(), "checkpoint pushed") {
		t.Fatalf("no checkpoint logged:\n%s", logs.String())
	}
}

// TestCheckpointSkipsAnUnchangedBranch: a tick with nothing new pushes nothing.
func TestCheckpointSkipsAnUnchangedBranch(t *testing.T) {
	var logs bytes.Buffer
	h := ckptHarness(t, prCfg(t, 2, ", early_draft: false"), &logs)
	pushes := countPushes(t, h)
	var during int
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		head := commitWIP(t, req, "wip")
		waitFor(t, "the checkpoint push", func() bool { return pushedHead(t, h) == head })
		afterTicks(t, 5)
		during = pushes()
		return implement("feature")(t, ctx, req)
	}
	if _, err := h.run(t, long, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if during != 1 {
		t.Fatalf("%d pushes for one new commit, want 1", during)
	}
}

// TestCheckpointNeverRewritesThePushedBranch: after the agent amends a
// pushed commit, checkpoints refuse (one warning) and finalize pushes the
// final branch as it always did.
func TestCheckpointNeverRewritesThePushedBranch(t *testing.T) {
	var logs bytes.Buffer
	h := ckptHarness(t, prCfg(t, 2, ", early_draft: false"), &logs)
	var first, during string
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		first = commitWIP(t, req, "wip")
		waitFor(t, "the checkpoint push", func() bool { return pushedHead(t, h) == first })
		shell(t, req, "git commit -q --amend -m 'wip, amended'")
		afterTicks(t, 3)
		during = remoteTip(t, h)
		return implement("feature")(t, ctx, req)
	}
	rec, err := h.run(t, long, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if during != first {
		t.Fatalf("a checkpoint replaced the pushed commit: remote at %s, want %s", during, first)
	}
	if remoteTip(t, h) != rec.HeadSHA || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("finalize did not push the final branch: remote %s, rec %+v", remoteTip(t, h), rec)
	}
	if n := strings.Count(logs.String(), "were rewritten"); n != 1 {
		t.Fatalf("%d rewrite warnings, want 1:\n%s", n, logs.String())
	}
}

// TestCheckpointSkipsWorkflowCommits: on GitHub a commit that changes a
// workflow file is never pushed by a checkpoint; finalize refuses as today.
func TestCheckpointSkipsWorkflowCommits(t *testing.T) {
	var logs bytes.Buffer
	h := ckptHarness(t, prCfg(t, 2, ""), &logs)
	ci := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "mkdir -p .github/workflows && echo 'on: push' > .github/workflows/ci.yml && git add -A && git commit -qm 'Add CI'")
		afterTicks(t, 3)
		if remoteHasBranch(t, h) {
			t.Error("a checkpoint pushed a workflow change")
		}
		return implementVerified(t, ctx, req)
	}
	runRefused(t, h, ci, review("ship", 0))
	if n := strings.Count(logs.String(), "no more checkpoint pushes"); n != 1 {
		t.Fatalf("%d stop notices, want 1:\n%s", n, logs.String())
	}
}

// TestCheckpointStopsOnAKnownSecret: a commit holding a value the run
// redacts is never pushed mid-run, and neither is anything after it, even
// once a later commit removes the value.
func TestCheckpointStopsOnAKnownSecret(t *testing.T) {
	var logs bytes.Buffer
	h := ckptHarness(t, prCfg(t, 2, ""), &logs)
	const mounted = "mounted-checkpoint-secret-value"
	h.deps.Env = append(h.deps.Env, "RENAMED_TOKEN="+mounted, runner.SecretEnvsVar+"=RENAMED_TOKEN")
	leak := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo "+mounted+" > leak.txt && git add -A && git commit -qm leak")
		afterTicks(t, 3)
		shell(t, req, "git rm -q leak.txt && git commit -qm unleak")
		afterTicks(t, 3)
		if remoteHasBranch(t, h) {
			t.Error("a checkpoint pushed commits holding a secret value")
		}
		return implement("feature")(t, ctx, req)
	}
	if _, err := h.run(t, leak, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "no more checkpoint pushes") || strings.Contains(logs.String(), mounted) {
		t.Fatalf("logs:\n%s", logs.String())
	}
}

// TestCheckpointFailureNeverFailsTheRun: a refused push warns once, the
// next tick retries, and the run ends as usual.
func TestCheckpointFailureNeverFailsTheRun(t *testing.T) {
	var logs bytes.Buffer
	h := ckptHarness(t, prCfg(t, 2, ", early_draft: false"), &logs)
	flag := filepath.Join(t.TempDir(), "refuse")
	if err := os.WriteFile(flag, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	tried := filepath.Join(t.TempDir(), "tried")
	hook := "#!/bin/sh\ncat >/dev/null\nif [ -e " + flag + " ]; then touch " + tried + "; echo 'try later' >&2; exit 1; fi\n"
	if err := os.WriteFile(filepath.Join(h.remote, "hooks", "pre-receive"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		head := commitWIP(t, req, "wip")
		waitFor(t, "a refused checkpoint push", func() bool { _, err := os.Stat(tried); return err == nil })
		afterTicks(t, 3) // more refused tries: still one warning
		if remoteHasBranch(t, h) {
			t.Error("the refusing remote took a push")
		}
		if err := os.Remove(flag); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the retried checkpoint", func() bool { return pushedHead(t, h) == head })
		return implement("feature")(t, ctx, req)
	}
	rec, err := h.run(t, long, review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if n := strings.Count(logs.String(), "a checkpoint push failed"); n != 1 {
		t.Fatalf("%d push warnings, want 1:\n%s", n, logs.String())
	}
}

// TestCheckpointsOffPushesNothingMidStage: git.pr.checkpoints false keeps
// 0.5.0's pushes.
func TestCheckpointsOffPushesNothingMidStage(t *testing.T) {
	var logs bytes.Buffer
	h := ckptHarness(t, prCfg(t, 2, ", checkpoints: false"), &logs)
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		commitWIP(t, req, "wip")
		time.Sleep(50 * tick) // no checkpointer runs, so there is no tick to wait for
		if remoteHasBranch(t, h) || tickCount() != 0 {
			t.Errorf("a checkpoint ran with checkpoints false (%d ticks)", tickCount())
		}
		return implement("feature")(t, ctx, req)
	}
	rec, err := h.run(t, long, review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

// TestNoCheckpointAfterHalt: once a halt is recorded, the stage's last
// seconds push nothing; finalize pushes as it does for any halt.
func TestNoCheckpointAfterHalt(t *testing.T) {
	var logs bytes.Buffer
	b := newHandleBox(t)
	h := ckptHarness(t, prCfg(t, 2, ""), &logs)
	halted := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if !b.h.HaltNow(runCapHalt) {
			t.Fatal("HaltNow was refused")
		}
		commitWIP(t, req, "wip")
		afterTicks(t, 3)
		if remoteHasBranch(t, h) {
			t.Error("a checkpoint pushed after the halt")
		}
		return agent.Result{CostUSD: 1}, nil
	}
	rec, err := h.run(t, halted)
	if err != nil || rec.Status != runstore.StatusHalted {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

// TestNoCheckpointAfterCancel: once a cancel is recorded, nothing is
// pushed mid-stage; finalize leaves the cancelled draft as today.
func TestNoCheckpointAfterCancel(t *testing.T) {
	var logs bytes.Buffer
	b := newHandleBox(t)
	h := ckptHarness(t, prCfg(t, 2, ""), &logs)
	cancelled := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if !b.h.MarkCancelled() {
			t.Fatal("MarkCancelled was refused")
		}
		commitWIP(t, req, "wip")
		afterTicks(t, 3)
		if remoteHasBranch(t, h) {
			t.Error("a checkpoint pushed after the cancel")
		}
		if err := h.store.RequestCancel(context.Background()); err != nil {
			t.Fatal(err)
		}
		return blockUntilDone(t, ctx, req)
	}
	rec, err := h.run(t, cancelled)
	if err != nil || rec.Status != runstore.StatusCancelled {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

// TestFollowUpDoesNotCheckpoint: a follow-up pushes only at finalize, as
// in 0.5.0 (decision C6).
func TestFollowUpDoesNotCheckpoint(t *testing.T) {
	runner.SetCheckpointEvery(t, tick)
	tickCount = runner.CountCheckpointTicks(t)
	h := followUpHarness(t, "", nil, implement("feature"), review("ship", 0))
	start := remoteTip(t, h.harness)
	h.followUp(t, followID, runID, "")
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		commitWIP(t, req, "more")
		before := tickCount()
		time.Sleep(50 * tick) // no checkpointer runs in a follow-up, so there is no tick to wait for
		if tickCount() != before {
			t.Errorf("a follow-up stage ran checkpoint ticks")
		}
		if got := remoteTip(t, h.harness); got != start {
			t.Errorf("a follow-up checkpointed: remote moved from %s to %s", start, got)
		}
		return implement("again")(t, ctx, req)
	}
	rec, err := h.run(t, long, review("ship", 0))
	mustReady(t, rec, err)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -race ./internal/runner/ -run 'TestCheckpoint|TestNoCheckpointAfter|TestFollowUpDoesNotCheckpoint'`
Expected: FAIL to compile: `undefined: checkpointEvery` (in `export_test.go`).

- [ ] **Step 3: Write the implementation**

Create `internal/runner/checkpoint.go`:

```go
package runner

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gitops"
)

// Checkpoint pushes (design checkpoint-pushes.md). While a first run's
// stage runs, the runner pushes the run branch's new commits every
// checkpointInterval: fast-forward only, by sha, with the runner's
// credentials and the same guards as finalize's push, plus a scan for the
// values the run redacts. A checkpoint never fails the run, never forces,
// never commits and never touches the working tree or the index.

// checkpointInterval is how often a running stage's new commits are pushed.
const checkpointInterval = 3 * time.Minute

// checkpointEvery is checkpointInterval; a variable so a test can shorten it.
var checkpointEvery = checkpointInterval

// checkpointTicked, when set, is called at the end of every tick: tests
// wait for ticks rather than for time (CountCheckpointTicks).
var checkpointTicked func()

// checkpointOpenTries bounds how many checkpoint pushes try to open the
// draft (decision C8); after that the verified boundary or finalize does.
const checkpointOpenTries = 2

// checkpointState is the run's checkpoint bookkeeping. During a stage only
// the checkpoint goroutine touches it (and the run record); the run
// goroutine reads it only after startCheckpoints' stop has returned.
type checkpointState struct {
	stopped   bool            // a permanent reason: no more checkpoints this run
	warned    map[string]bool // warnings already logged, by kind
	openTries int             // checkpoint pushes that tried to open the draft
	pushes    int             // checkpoint pushes made
}

// checkpointsOn reports whether this run checkpoints: a first run whose
// fugaro.yaml leaves git.pr.checkpoints on.
func (r *run) checkpointsOn() bool {
	return r.follow == nil && r.cfg != nil && r.cfg.Git.PR.CheckpointsOn() && checkpointEvery > 0
}

// startCheckpoints starts the stage's checkpoint goroutine on stageCtx and
// returns its stop, which cancels it and waits for it: nothing of it runs
// once stop returns. stop is safe to call more than once.
func (r *run) startCheckpoints(stageCtx context.Context, stage string) (stop func()) {
	if !r.checkpointsOn() || r.ckpt.stopped {
		return func() {}
	}
	ctx, cancel := context.WithCancel(stageCtx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(checkpointEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				r.checkpoint(ctx, stage)
			}
		}
	}()
	return sync.OnceFunc(func() { cancel(); <-done })
}

// warnCheckpoint logs msg once per run for key.
func (r *run) warnCheckpoint(key, msg string, args ...any) {
	if r.ckpt.warned == nil {
		r.ckpt.warned = map[string]bool{}
	}
	if r.ckpt.warned[key] {
		return
	}
	r.ckpt.warned[key] = true
	r.d.Log.Warn(msg, args...)
}

// stopCheckpoints ends checkpoints for the run, for a reason no later tick
// can change; finalize then pushes, or refuses, as it always did.
func (r *run) stopCheckpoints(reason string, args ...any) {
	r.ckpt.stopped = true
	r.d.Log.Warn("no more checkpoint pushes this run: "+reason+"; finalize pushes as usual", args...)
}

// checkpoint is one tick: push the run branch's tip if it is new, settled
// and safe. Every failure is a warning; a panic is recovered here, as a
// goroutine's panic would end the process.
func (r *run) checkpoint(ctx context.Context, stage string) {
	if checkpointTicked != nil {
		defer checkpointTicked()
	}
	defer func() {
		if p := recover(); p != nil {
			r.d.Log.Error("a checkpoint panicked; carrying on", "stage", stage, "panic", fmt.Sprint(p))
		}
	}()
	if ctx.Err() != nil || r.ckpt.stopped || r.pr.gone || r.haltValue() != nil || r.isCancelled() || r.budget.Exhausted() {
		return
	}
	sha, err := r.repo.CheckpointTip(ctx, r.rec.Branch)
	switch {
	case errors.Is(err, gitops.ErrGitBusy):
		return // the agent is mid-operation or off the branch; the next tick looks again
	case err != nil:
		r.warnCheckpoint("tip", "reading the run branch for a checkpoint failed", "err", r.redact(err.Error()))
		return
	case sha == r.rec.PushedHead:
		return // nothing new: no network call
	}
	// Of the tip read, not HEAD: the agent may have committed since, and the
	// tip read then is the base, which is never pushed.
	if ahead, err := r.repo.CountAhead(ctx, r.cfg.Git.BaseBranch, sha); err != nil || ahead == 0 {
		return // no commit of the run's own yet
	}
	if !r.checkpointClean(ctx, sha) {
		return
	}
	err = r.pushCheckpoint(ctx, sha)
	switch {
	case err == nil:
	case errors.Is(err, gitops.ErrNotFastForward):
		r.warnCheckpoint("rewritten", "the run branch's pushed commits were rewritten; checkpoints wait until a push fast-forwards again (a verified stage end or finalize pushes the branch as it is)", "sha", shortSHA(sha))
		return
	case r.asRefusal(err) != nil:
		r.stopCheckpoints("the host would refuse the push", "reason", refusalText(r.asRefusal(err)))
		return
	case ctx.Err() != nil:
		return // the stage ended mid-push; the next boundary or finalize pushes
	default:
		r.warnCheckpoint("push", "a checkpoint push failed; the next one tries again", "err", r.redact(err.Error()))
		return
	}
	r.ckpt.pushes++
	r.d.Log.Info("checkpoint pushed", "stage", stage, "sha", shortSHA(sha), "n", r.ckpt.pushes)
}

// checkpointClean scans the commits a checkpoint would add (after the last
// push, or after the work base before the first) for a value the run
// redacts. A hit stops checkpoints for the run: every later push would
// carry that commit. A binary or very large range is pushed, as finalize
// would push it (decision C5).
func (r *run) checkpointClean(ctx context.Context, sha string) bool {
	since := r.rec.PushedHead
	if since == "" {
		since = r.workBase()
	}
	redact := agent.RedactFunc(r.secretList())
	scan, err := r.repo.ScanRange(ctx, since, sha, func(text string) bool { return redact(text) != text })
	switch {
	case err != nil:
		r.warnCheckpoint("scan", "checking a checkpoint's commits for secrets failed; not pushing it", "err", r.redact(err.Error()))
		return false
	case scan.Hit:
		r.stopCheckpoints("the new commits hold a value the run redacts (a secret)")
		return false
	}
	return true
}

// pushCheckpoint pushes sha fast-forward only, after finalize's workflow
// guard on that exact commit, and saves pushed_head at once, as every push
// does, so a run killed later still says what reached the remote.
func (r *run) pushCheckpoint(ctx context.Context, sha string) error {
	r.refreshForPush(ctx)
	if rej := r.workflowGuardAt(ctx, sha); rej != nil {
		return rej // no network call for a push GitHub is sure to refuse
	}
	pctx, cancel := context.WithTimeout(ctx, pushTimeout)
	defer cancel()
	if err := r.repo.PushFastForward(pctx, r.rec.Branch, sha); err != nil {
		return err
	}
	r.rec.PushedHead = sha
	r.save(ctx)
	return nil
}
```

In `internal/runner/prflow.go`, replace the first five lines of `pushBranch`'s body (the `actx` refresh) with `r.refreshForPush(ctx)` and add:

```go
// refreshForPush refreshes the git credentials for a mid-run push, on a
// bound of its own; a failure warns once and keeps the current ones.
func (r *run) refreshForPush(ctx context.Context) {
	actx, cancel := context.WithTimeout(ctx, authRefreshTimeout)
	err := r.refreshGitAuth(actx, authValidity(max(r.wf.Timeouts.FinalizeReserve.Duration, bootstrapAuthMinValid)))
	cancel()
	if err != nil {
		r.warnAuthRefresh(err)
	}
}
```

In `internal/runner/refused.go`, rename the body of `workflowGuard` into a new function and keep `workflowGuard` as a wrapper:

```go
func (r *run) workflowGuard(ctx context.Context) *gitops.PushRejected {
	return r.workflowGuardAt(ctx, "HEAD")
}

// workflowGuardAt is workflowGuard for the commits up to until: a
// checkpoint checks the exact commit it pushes.
func (r *run) workflowGuardAt(ctx context.Context, until string) *gitops.PushRejected {
	if r.providerKind != gitprov.KindGitHub {
		return nil
	}
	files, err := r.repo.WorkflowFilesIn(ctx, r.workBase(), until)
	if err != nil {
		r.d.Log.Warn("checking the commits for workflow files failed; pushing anyway", "err", r.redact(err.Error()))
		return nil
	}
	if len(files) == 0 {
		return nil
	}
	return &gitops.PushRejected{Kind: gitops.RejectWorkflows, Files: files}
}
```

(The doc comment above `workflowGuard` stays where it is.)

In `internal/runner/runner.go`:
- in the `run` struct, after `pr prFlow`, add:

```go
	// ckpt is the checkpoint pushes' state (checkpoint.go).
	ckpt checkpointState
```

- in `stage()`, replace

```go
	res, err := r.d.Agent.Run(stageCtx, req)
	stopWatch()
```

with

```go
	// Checkpoints run only while the agent does: stopped (and waited for)
	// the moment it returns, and by the defer if it panics.
	stopCheckpoints := r.startCheckpoints(stageCtx, name)
	defer stopCheckpoints()
	res, err := r.d.Agent.Run(stageCtx, req)
	stopCheckpoints()
	stopWatch()
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/runner/ -run 'TestCheckpoint|TestNoCheckpointAfter|TestFollowUpDoesNotCheckpoint|TestRefused|TestOpensDraftAfterFirstVerifiedStage|TestNoPushWithoutVerifiedTest|TestEarlyDraftFalseKeepsFinalizeOnly|TestHaltAfterPushLeavesDraftWithComment|TestCancelFinalizesDraftWithNote'`
Expected: PASS. The existing tests in the list pin that the default 3-minute interval leaves today's flows unchanged.

- [ ] **Step 5: Commit**

```bash
git add internal/runner/checkpoint.go internal/runner/checkpoint_test.go internal/runner/runner.go internal/runner/refused.go internal/runner/prflow.go internal/runner/export_test.go
git commit -m "runner: checkpoint pushes while a stage runs, fast-forward only"
```

- [ ] **Step 6: Own review** (token-economy rule: this task changes every run's stage loop). The reviewer checks four things:
  - nothing but the checkpoint goroutine writes `r.rec`, `r.pr`, `r.ckpt`, `r.env` or `r.auth` between `startCheckpoints` and its `stop`;
  - every path in `checkpoint` returns without failing the run;
  - `stop` runs before any later write in `stage()`;
  - the `-race` focused run above is clean.

---

### Task 5: The draft opens at the first checkpoint, marked not verified

**Files:**
- Modify: `internal/runner/prflow.go`, `internal/runner/checkpoint.go`
- Test: `internal/runner/checkpoint_test.go`

**Interfaces:**
- Consumes:
  - from Task 4: `checkpointState.openTries`, `checkpointOpenTries`, `checkpoint`, `ckptHarness`, `commitWIP`, `waitFor`, `afterTicks`, `pushedHead`, `tickCount`, `remoteTip`, `localHead`;
  - existing: `latestVerifiedTest`, `ensurePR`, `earlyPRText`, `noteStatusWritten`, `newClock`, `afterStep`, `probe`, `storedPR`, `onlyPR`, `ops`, `count`, `verifyTest`.
- Produces:
  ```go
  func (r *run) afterCheckpoint(ctx context.Context, stage string)
  func (r *run) openDraftPR(ctx context.Context, stage string, during bool) // openDraft's PR half
  func (r *run) statusUpdate(ctx context.Context, stage string, during bool)    // was (ctx, stage)
  func (r *run) runningSection(stage string, during bool) string                // was (stage)
  func (r *run) pushedPart(records []verify.Record) string
  func pushedVerified(records []verify.Record, sha string) bool
  ```

- [ ] **Step 1: Write the failing tests**

Append to `internal/runner/checkpoint_test.go`:

```go
// TestCheckpointOpensDraftMarkedNotVerified: the first checkpoint opens the
// draft, with no reviewers, its number saved, and a section that says the
// work is not verified; finalize makes it ready as before.
func TestCheckpointOpensDraftMarkedNotVerified(t *testing.T) {
	var logs bytes.Buffer
	h := ckptHarness(t, prCfg(t, 2, ""), &logs)
	var body string
	var draft bool
	var reviewers []string
	var saved *runstore.PRRef
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		head := commitWIP(t, req, "wip")
		waitFor(t, "the draft at the first checkpoint", func() bool {
			return len(h.provider.Snapshot().PRs) == 1 && pushedHead(t, h) == head
		})
		pr := h.provider.Snapshot().PRs[0]
		body, draft, reviewers = pr.Body, pr.Draft, pr.Reviewers
		saved = storedPR(t, h)
		return implement("feature")(t, ctx, req)
	}
	rec, err := h.run(t, long, review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if !draft || len(reviewers) != 0 || saved == nil || saved.Number != 1 {
		t.Fatalf("mid-stage PR: draft=%v reviewers=%v saved=%+v", draft, reviewers, saved)
	}
	for _, want := range []string{statusBegin, "**Running: work in progress, not verified**", "checkpoint during stage `implement`", "`: not verified"} {
		if !strings.Contains(body, want) {
			t.Errorf("mid-stage body lacks %q:\n%s", want, body)
		}
	}
	if rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v", rec)
	}
	pr := onlyPR(t, h.provider)
	if pr.Draft || !strings.Contains(pr.Body, "**Ready for review**") || strings.Contains(pr.Body, "not verified") {
		t.Fatalf("final PR = %+v", pr)
	}
	if calls := ops(h); calls[0] != "EnsurePR#0 draft=true" || count(calls, "EnsurePR", "") != 2 {
		t.Fatalf("calls = %q", calls)
	}
}

// TestStatusSaysVerifiedAfterAPassingTest: once the pushed commit has a
// passing clean test, the next status write says verified.
func TestStatusSaysVerifiedAfterAPassingTest(t *testing.T) {
	var logs bytes.Buffer
	c := newClock()
	h := ckptHarness(t, prCfg(t, 2, ""), &logs)
	h.deps.Now = c.Now
	var head string
	work := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		head = commitWIP(t, req, "wip")
		waitFor(t, "the draft", func() bool { return len(h.provider.Snapshot().PRs) == 1 })
		verifyTest(t, ctx, req)
		pr := filepath.Join(envValue(req.Env, "FUGARO_STATE_DIR"), "pr.md")
		if err := os.WriteFile(pr, []byte("# Add wip\n\nAdds wip.txt."), 0o644); err != nil {
			t.Fatal(err)
		}
		return agent.Result{CostUSD: 1}, nil
	}
	var body string
	if _, err := h.run(t, afterStep(c, work), probe(func(t *testing.T) { body = h.provider.Snapshot().PRs[0].Body }, review("ship", 0))); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "**Running** ·") || !strings.Contains(body, fmt.Sprintf("branch at `%s`: verified", head[:7])) || strings.Contains(body, "not verified") {
		t.Fatalf("body at the review stage:\n%s", body)
	}
}

// TestEarlyDraftFalseCheckpointsWithoutPR: early_draft false still pushes
// the branch mid-stage, and the PR opens only at finalize.
func TestEarlyDraftFalseCheckpointsWithoutPR(t *testing.T) {
	var logs bytes.Buffer
	h := ckptHarness(t, prCfg(t, 2, ", early_draft: false"), &logs)
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		head := commitWIP(t, req, "wip")
		waitFor(t, "the checkpoint push", func() bool { return pushedHead(t, h) == head })
		afterTicks(t, 3)
		if n := len(h.provider.Snapshot().PRs); n != 0 {
			t.Errorf("%d PRs opened mid-run with early_draft false", n)
		}
		return implement("feature")(t, ctx, req)
	}
	rec, err := h.run(t, long, review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady || onlyPR(t, h.provider).Draft {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

// TestCheckpointOpenTriedTwice: a draft that fails to open is retried on
// the next checkpoint push only, twice in all; the verified boundary opens
// it after that.
func TestCheckpointOpenTriedTwice(t *testing.T) {
	var logs bytes.Buffer
	h := ckptHarness(t, prCfg(t, 2, ""), &logs)
	h.provider.FailEnsure = 6 // two tries of ensurePR's three attempts
	ensures := func() int { return count(snapOps(h), "EnsurePR", "") }
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		commitWIP(t, req, "one")
		waitFor(t, "the first open's attempts", func() bool { return ensures() == 3 })
		commitWIP(t, req, "two")
		waitFor(t, "the second open's attempts", func() bool { return ensures() == 6 })
		head := commitWIP(t, req, "three")
		waitFor(t, "the third checkpoint", func() bool { return pushedHead(t, h) == head })
		afterTicks(t, 3)
		if n := ensures(); n != 6 {
			t.Errorf("%d EnsurePR calls, want 6: a third try was made", n)
		}
		return implement("feature")(t, ctx, req)
	}
	rec, err := h.run(t, long, review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady || len(h.provider.State.PRs) != 1 {
		t.Fatalf("rec = %+v, err = %v, PRs = %+v", rec, err, h.provider.State.PRs)
	}
}

// snapOps is ops read through the provider's lock.
func snapOps(h *harness) []string {
	var out []string
	for _, c := range h.provider.Snapshot().Calls {
		out = append(out, fmt.Sprintf("%s#%d %s", c.Op, c.PR, c.Detail))
	}
	return out
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -race ./internal/runner/ -run 'TestCheckpointOpensDraftMarkedNotVerified|TestStatusSaysVerifiedAfterAPassingTest|TestEarlyDraftFalseCheckpointsWithoutPR|TestCheckpointOpenTriedTwice'`
Expected: FAIL. Three tests time out, because nothing opens a PR at a checkpoint yet:
- `TestCheckpointOpensDraftMarkedNotVerified`: `timed out waiting for the draft at the first checkpoint`;
- `TestStatusSaysVerifiedAfterAPassingTest`: `timed out waiting for the draft`;
- `TestCheckpointOpenTriedTwice`: `timed out waiting for the first open's attempts`. `TestEarlyDraftFalseCheckpointsWithoutPR` already passes: it pins that Task 5 keeps `early_draft: false` PR-less.

- [ ] **Step 3: Write the implementation**

In `internal/runner/prflow.go`:

1. Split `openDraft`. Keep the push and the call:

```go
// openDraft pushes the verified HEAD and opens the draft pull request. A
// failure only warns: finalize opens the PR if none was recorded.
func (r *run) openDraft(ctx context.Context, sha, stage string) {
	if err := r.pushBranch(ctx, sha); err != nil {
		r.d.Log.Warn("pushing for the early pull request failed; finalize opens it", "err", r.redact(err.Error()))
		return
	}
	r.openDraftPR(ctx, stage, false)
}
```

and move everything after the push into a new function. Only its section line changes (`runningSection(stage, during)`):

```go
// openDraftPR opens the draft pull request for the branch already pushed
// and records its number before anything else. during says it is a
// checkpoint's, mid-stage. A failure only warns: finalize opens the PR if
// none was recorded.
func (r *run) openDraftPR(ctx context.Context, stage string, during bool) {
	title, desc := r.earlyPRText()
	r.pr.lastStatus = r.d.Now()
	section := r.redact(r.runningSection(stage, during))
	body, trunc := gitprov.ReplaceStatusWith(desc, section, r.statusOptions())
	r.warnTruncated(trunc)
	// A draft carries no reviewers and no labels: they come at ready.
	r.pr.desc = descDigest(title, desc)
	// ensurePR records the number (notePR) the moment the PR exists, before
	// anything else, so a crash from here on finds it by number and one
	// before it finds it by branch.
	octx, cancelOpen := context.WithTimeout(ctx, earlyOpenTimeout)
	pr, err := r.ensurePR(octx, gitprov.PRSpec{Branch: r.rec.Branch, Base: r.cfg.Git.BaseBranch, Title: title, Body: body, Draft: true})
	cancelOpen()
	if err != nil {
		r.d.Log.Warn("opening the early draft pull request failed; finalize opens it", "err", r.redact(err.Error()))
		return
	}
	if !pr.Draft {
		// Not expected: EnsurePR errs only toward more draft. Never leave
		// an early PR looking ready.
		r.d.Log.Error("the early pull request is not a draft; making it one", "pr", pr.Number)
		d := true
		uctx, cancel := context.WithTimeout(ctx, statusCallTimeout)
		_, uerr := r.provider.UpdatePR(uctx, pr.Number, gitprov.PRUpdate{Draft: &d})
		cancel()
		if uerr != nil {
			r.d.Log.Warn("making the early pull request a draft failed", "pr", pr.Number, "err", r.redact(uerr.Error()))
		}
	}
	r.noteStatusWritten(ctx, r.pr.lastStatus)
	if pr.DraftFallback {
		r.rec.DraftFallback = true
		r.pr.lastStatus = time.Time{} // the next boundary says so in the section
		r.save(ctx)
		r.d.Log.Warn("the host has no draft pull requests: the early PR is a normal one marked [DRAFT]", "pr", pr.Number)
	}
	r.d.Log.Info("early draft pull request opened", "pr", pr.Number, "url", pr.URL, "checkpoint", during)
}
```

2. `statusUpdate` gains `during bool` and passes it on: change its signature to `func (r *run) statusUpdate(ctx context.Context, stage string, during bool)` and its write to `err := r.writeStatus(ctx, n, r.redact(r.runningSection(stage, during)), statusCallTimeout)`. In `afterStage`, the call becomes `r.statusUpdate(ctx, stage, false)`.

3. Replace `runningSection` with:

```go
// runningSection is the section while the run is in progress: after stage
// ended, or (during) at a checkpoint in the middle of it. A first run's
// pushed commit without a passing clean test is marked not verified.
func (r *run) runningSection(stage string, during bool) string {
	records, _ := verify.Records(r.d.StateDir)
	head := "**Running**"
	switch {
	case r.follow != nil:
		head = "**Follow-up running**"
	case r.rec.PushedHead != "" && !pushedVerified(records, r.rec.PushedHead):
		head = "**Running: work in progress, not verified**"
	}
	what := fmt.Sprintf("after stage `%s`", inlineText(stage))
	if during {
		what = fmt.Sprintf("checkpoint during stage `%s`", inlineText(stage))
	} else if stage == "review" || stage == "review_first" {
		if p := reviewsPart(r.rec.Reviews); p != "" {
			what = p
		}
	}
	return r.sectionLines(head, what, verifyPart(records), r.pushedPart(records), r.costPart(), r.updated("updated"))
}

// pushedVerified reports whether sha has a passing test on a clean tree.
func pushedVerified(records []verify.Record, sha string) bool {
	t := latestVerifiedTest(records, sha)
	return t != nil && t.Passed
}

// pushedPart says which commit the run branch holds and whether it is
// verified; "" before a first run's first push and for a follow-up.
func (r *run) pushedPart(records []verify.Record) string {
	sha := r.rec.PushedHead
	if sha == "" || r.follow != nil {
		return ""
	}
	if pushedVerified(records, sha) {
		return fmt.Sprintf("branch at `%s`: verified", shortSHA(sha))
	}
	return fmt.Sprintf("branch at `%s`: not verified", shortSHA(sha))
}
```

In `internal/runner/checkpoint.go`, end `checkpoint` (after the `checkpoint pushed` log line) with `r.afterCheckpoint(ctx, stage)` and add:

```go
// afterCheckpoint opens the draft at the first checkpoint push, or brings
// an open draft's status section up to date. With early_draft false the
// branch is all a checkpoint pushes: the PR opens at finalize.
func (r *run) afterCheckpoint(ctx context.Context, stage string) {
	if !r.cfg.Git.PR.EarlyDraftOn() || r.pr.gone {
		return
	}
	if r.rec.PR == nil {
		if r.ckpt.openTries >= checkpointOpenTries {
			return // left to the verified boundary or finalize (decision C8)
		}
		r.ckpt.openTries++
		r.openDraftPR(ctx, stage, true)
		return
	}
	r.statusUpdate(ctx, stage, true)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/runner/ -run 'TestCheckpoint|TestStatusSays|TestEarlyDraft|TestNoCheckpointAfter|TestFollowUpDoesNotCheckpoint|TestOpensDraftAfterFirstVerifiedStage|TestNoPushWithoutVerifiedTest|TestFirstRoundFailsThenFixOpensDraft|TestStatus|TestCoalesces|TestThreeFailuresStopUpdates|TestHumanEditOutsideMarkersKept|TestDraftFallbackNotReadyLooking|TestFollowUp'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/runner/prflow.go internal/runner/checkpoint.go internal/runner/checkpoint_test.go
git commit -m "runner: the draft opens at the first checkpoint, marked not verified"
```

---

### Task 6: The agent is told to commit early

**Files:**
- Modify: `internal/runner/prompts.go`, `internal/runner/runner.go` (`agentLoop`)
- Test: `internal/runner/pure_test.go`

**Interfaces:**
- Consumes: `PromptData`, `SystemPrompt`, `checkpointInterval`, `checkpointsOn`.
- Produces:
  ```go
  // PromptData gains:
  Checkpoints bool
  ```

- [ ] **Step 1: Write the failing test**

Append to `internal/runner/pure_test.go`:

```go
func TestPromptCheckpointRule(t *testing.T) {
	d := PromptData{Branch: "fugaro/x", Base: "main", StateDir: "/s"}
	if got := SystemPrompt(d, ""); strings.Contains(got, "Commit early and often") {
		t.Errorf("a prompt without checkpoints asks for early commits: %s", got)
	}
	d.Checkpoints = true
	got := SystemPrompt(d, "")
	for _, want := range []string{"Commit early and often", "fugaro/x every 3 minutes", "only pushed commits survive", "uncommitted changes are lost", "rather than amending or rebasing"} {
		if !strings.Contains(got, want) {
			t.Errorf("the checkpoint rule lacks %q: %s", want, got)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/runner/ -run 'TestPromptCheckpointRule|TestPrompts|TestPromptWorkflowRule'`
Expected: FAIL to compile: `unknown field Checkpoints in struct literal`.

- [ ] **Step 3: Write the implementation**

In `internal/runner/prompts.go`, add `"time"` to the imports, add to `PromptData`:

```go
	// Checkpoints adds the rule to commit early: the runner pushes new
	// commits while the stage runs (first runs with git.pr.checkpoints on).
	Checkpoints bool
```

and in `SystemPrompt`, after the `NoWorkflows` block:

```go
	if d.Checkpoints {
		lines = append(lines, fmt.Sprintf("- Commit early and often, after every step that works: Fugaro pushes your new commits to %s every %d minutes, and if this container dies only pushed commits survive; uncommitted changes are lost. Add new commits rather than amending or rebasing commits you already made: rewritten commits are not pushed until the run ends.", d.Branch, int(checkpointInterval/time.Minute)))
	}
```

In `internal/runner/runner.go`'s `agentLoop`, the `PromptData` literal gains `Checkpoints: r.checkpointsOn(),`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/runner/ -run 'TestPrompt'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/runner/prompts.go internal/runner/runner.go internal/runner/pure_test.go
git commit -m "runner: the agent is told only pushed commits survive"
```

---

### Task 7: Docs, pinned by a test

**Files:**
- Create: `internal/runner/docs_checkpoint_internal_test.go`
- Modify: `docs/git-providers.md`, `docs/design/v1.md`, `docs/gcp-setup.md`, `README.md`, `plugin/skills/working/SKILL.md`, `plugin/skills/working/reference/launch.md`, `plugin/skills/working/reference/diagnose.md`

**Interfaces:**
- Consumes: `checkpointInterval`.
- Produces: `TestCheckpointDocsMatchTheCode`.

- [ ] **Step 1: Write the failing test**

Create `internal/runner/docs_checkpoint_internal_test.go`:

```go
package runner

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestCheckpointDocsMatchTheCode: the docs state the interval the code uses,
// name the opt-out, and no longer say the draft waits for a verified push.
func TestCheckpointDocsMatchTheCode(t *testing.T) {
	read := func(p string) string {
		t.Helper()
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	gp := read("../../docs/git-providers.md")
	for _, want := range []string{
		"## Checkpoint pushes (`git.pr.checkpoints`)",
		fmt.Sprintf("every %d minutes", int(checkpointInterval/time.Minute)),
		"fast-forward", "git.pr.checkpoints: false", "not verified", "resources.memory",
	} {
		if !strings.Contains(gp, want) {
			t.Errorf("docs/git-providers.md never says %q", want)
		}
	}
	for _, f := range []string{
		"../../docs/git-providers.md", "../../docs/gcp-setup.md", "../../docs/design/v1.md", "../../README.md",
		"../../plugin/skills/working/SKILL.md", "../../plugin/skills/working/reference/launch.md",
		"../../plugin/skills/working/reference/diagnose.md",
	} {
		if strings.Contains(read(f), "first verified push") {
			t.Errorf("%s still says the draft opens at the first verified push", f)
		}
	}
	if !strings.Contains(read("../../docs/design/v1.md"), "checkpoint-pushes.md") {
		t.Error("design v1 §4.2a does not link the checkpoint design")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/runner/ -run TestCheckpointDocsMatchTheCode`
Expected: FAIL: `docs/git-providers.md never says "## Checkpoint pushes (`git.pr.checkpoints`)"`, and `… still says the draft opens at the first verified push` for each listed file.

- [ ] **Step 3: Write the docs**

`docs/git-providers.md`:
- In "Early draft PRs", replace the first paragraph (line 78) with:

  > A run opens a **draft** pull request at its first checkpoint push (see "Checkpoint pushes" below: its first new commits, within a few minutes of the agent committing), not when it finishes. It keeps a **Fugaro status** section in the description current: the stage, the verify state, the commit the branch holds and whether it is verified, the model cost and the update time. Until a passing test on a clean tree covers the pushed commit, the section says `Running: work in progress, not verified`. The PR is marked ready at the end only when it is (design §4.2a). With `git.pr.checkpoints: false` the draft opens at the first verified stage end, as in 0.5.0.

- Insert a new section before `## Pushing and draft pull requests`:

  ```markdown
  ## Checkpoint pushes (`git.pr.checkpoints`)

  While a run's stage runs, Fugaro pushes the run branch's new commits every 3 minutes, so a container that dies (out of memory, out of disk, a killed execution) keeps all the work the agent had committed. The push is a **fast-forward only**: it never forces and never replaces a commit already on the branch. It uses the runner's credentials, never commits for the agent, and never touches uncommitted files: uncommitted work is still lost with the container, so the agent is told to commit early. A checkpoint is guarded like the final push: on GitHub, commits that change `.github/workflows/` are not pushed, and commits holding a value the run redacts (a secret it knows) stop checkpoints for the run. A push that fails is retried at the next checkpoint and never fails the run. If the agent rewrites commits already pushed (amend, rebase), checkpoints pause until a push fast-forwards again; the end of a verified stage and the final push put the branch right as before. Follow-up runs (`fugaro run --pr N`) do not checkpoint: their pull request may already be ready.

  With `git.pr.early_draft` on (the default), the first checkpoint opens the draft PR, marked not verified. It becomes ready only at the end, by the same rule as before. With `early_draft: false`, checkpoints push the branch and the PR still opens only at the end.

  **CI cost.** Every checkpoint push to a branch with an open pull request runs the repository's PR CI, and `on: push` workflows (or Bitbucket branch pipelines) run on any push. During a long stage that can be one CI run every 3 minutes while the agent commits. To keep that down, skip drafts in CI (on GitHub, `if: github.event.pull_request.draft == false` on the expensive jobs) and cancel superseded runs (`concurrency: { group: ${{ github.ref }}, cancel-in-progress: true }`), or turn checkpoints off with `git.pr.checkpoints: false`: the branch is then pushed only at a verified stage end and at the end, as in 0.5.0. The key needs Fugaro 0.5.1 everywhere (older CLIs and job images refuse it as unknown); the default needs no key.

  **When a container dies anyway.** On Cloud Run the container's disk is memory: `node_modules`, build output and caches count against `workflows.<name>.resources.memory`. A run killed by a signal (signal 7 or 9) during repeated installs or builds usually needs a larger `resources.memory` (or fewer repeated installs), not a code change. Its draft PR holds the work up to the last checkpoint; continue it with `fugaro run --pr N`.
  ```

`docs/design/v1.md`:
- §4.2a's first paragraph (line 275), replace with:

  > Finalize's PR (§4.1) stays the guarantee that every run with a branch ends in a PR. M9e added a **draft PR** so people can watch the work. Since 0.5.1 it opens at the run's **first checkpoint push** ([checkpoint-pushes.md](checkpoint-pushes.md)), marked not verified, which supersedes M9e ruling E1 that the remote branch is always a verified state. Rulings and open assumptions: [plan](../plans/2026-10-03-m9e-early-draft-pr.md). `git.pr.early_draft: false` (default `true`) turns the early PR off per repository: the PR then opens at finalize only. Checkpoints still push the branch unless `git.pr.checkpoints: false`, which restores 0.5.0's pushes.

- The **When.** bullet (line 277), replace with:

  > - **When.** While a first run's stage runs, a checkpoint every 3 minutes pushes the branch's new commits, fast-forward only (saving `pushed_head`). The first such push, with no PR recorded, opens a **draft** PR with no reviewers and no labels, its section headed `Running: work in progress, not verified` until a passing `test` with `clean_tree` covers the pushed commit. At the end of an implement or fix stage that succeeded, a verified HEAD is pushed as before (and opens the draft if no checkpoint did). A run with no commits opens nothing until finalize; a halt before the branch exists opens no PR (D9). The open is bounded (60 s) and tried at most twice by checkpoints; if it fails, the verified boundary or finalize opens the PR.

`docs/gcp-setup.md` line 57: replace `The draft appears at the first verified push and shows the run's progress;` with `The draft appears at the run's first checkpoint push (within a few minutes of its first commit), marked not verified, and shows the run's progress ([git-providers.md](git-providers.md#checkpoint-pushes-gitprcheckpoints));`.

`README.md` line 57: replace `a draft at the first verified push, marked ready only when the run passes.` with `new commits are pushed every few minutes while the agent works, a draft opens at the first push (marked not verified), and it is marked ready only when the run passes.`

`plugin/skills/working/SKILL.md` line 51: replace `A draft appears early, at the first verified push, with a status section in its description that the run keeps updating.` with `A draft appears early, at the run's first checkpoint push (its first commits, within minutes), with a status section in its description that the run keeps updating and that says "not verified" until a passing test covers the pushed commit.`

`plugin/skills/working/reference/launch.md` line 6: replace `A draft pull request appears early, at the run's first verified push, and shows the run's progress in its description;` with `A draft pull request appears early, at the run's first checkpoint push (new commits are pushed every few minutes), marked not verified, and shows the run's progress in its description;`.

`plugin/skills/working/reference/diagnose.md`:
- line 41: replace `at the run's first verified push, while the run is still going,` with `at the run's first checkpoint push (new commits are pushed every few minutes, unverified), while the run is still going,`;
- line 43: replace `The work up to the last verified push is on the branch;` with `The work the agent committed up to the last checkpoint (a few minutes before it died) is on the branch, unverified, and uncommitted work is lost;`.

Do not change the skill files' version headers: `/new-release` bumps them.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/runner/ -run TestCheckpointDocsMatchTheCode && go test ./plugin/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add docs/git-providers.md docs/design/v1.md docs/gcp-setup.md README.md plugin/skills/working internal/runner/docs_checkpoint_internal_test.go
git commit -m "docs: checkpoint pushes, the unverified early draft, CI cost and resources"
```

---

### Task 8: The full suite and the PR

- [ ] **Step 1:** Run `gofmt -l . && go vet ./... && go test -race ./internal/gitops/ ./internal/config/ ./schemas/ ./internal/gitprov/... ./plugin/`. Expected: no gofmt output, and PASS.
- [ ] **Step 2:** Run the **one** full runner suite for this PR group: `go test -race -timeout 40m ./internal/runner/...` (about 19 minutes). Expected: PASS. A failure in an existing test that reads `h.provider.State` during a step means the read needs `h.provider.Snapshot()`; fix it in the test, not by slowing checkpoints.
- [ ] **Step 3:** Run `go test ./...` for the rest (`internal/cli` takes about 6 minutes). Expected: PASS.
- [ ] **Step 4:** Push the branch and open the PR. The body lists decisions C1 to C11, one line each, and names the M9e ruling E1 that is superseded.
- [ ] **Step 5:** Read every CI check (`test`, `terraform`, `rules`), each job's log and not only the summary, before asking for the merge.

---

### Task 9: Release 0.5.1 (after the merge)

- [ ] **Step 1:** Through a PR to `main`, add to `docs/releases/v0.5.1.md`. Create the file if `fugaro image refresh`'s Task 12 has not created it yet, and keep its entries if it has:

```markdown
- **Checkpoint pushes.** While a stage runs, a run now pushes its new commits every 3 minutes (fast-forward only, never forced), so a container that dies mid-stage keeps the work the agent committed. The draft pull request opens at the first such push, marked "not verified" until a passing test covers the pushed commit; readiness and reviewers are unchanged. Every push to a branch with an open pull request runs its CI: see `docs/git-providers.md` for skipping drafts in CI, or set `git.pr.checkpoints: false` (needs 0.5.1 everywhere). Rebuild the job images (`fugaro image refresh`) for runs to get it.
```

- [ ] **Step 2:** `/new-release 0.5.1`.
- [ ] **Step 3, the user's live check on the Bitbucket sandbox only** (`edgeappinc/fugarosandbox`; never EdgeWeb or EdgeServer), with the user's go-ahead and task text, after `fugaro image refresh` there:
  1. A run whose task takes longer than 3 minutes. The branch appears on the host before the first stage ends, and the draft's section says `not verified`, then `verified`.
  2. `fugaro cancel --now` on a second such run after its first checkpoint. The branch holds the committed work, and `fugaro diagnose` shows the stale draft.

  This also confirms that Bitbucket accepts a push to the origin URL by SHA (C4).
