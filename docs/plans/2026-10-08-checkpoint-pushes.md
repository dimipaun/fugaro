# Checkpoint Pushes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Work the agent commits is saved as soon as it is committed. While a first run's stage runs, the runner pushes each new commit on the run branch:
- after a short quiet period, so a burst of commits is one push;
- at most once a minute while the stage runs;
- at once at every stage boundary.

The push is fast-forward only and uses the runner's credentials. The draft PR opens at the first such push, and its status section says the work is not verified until a passing test covers the pushed commit. A container that dies mid-stage then loses at most about a minute of committed work, instead of all of it. Readiness, reviewers and finalize do not change.

**Architecture:**
- **The schedule** (`checkpointSchedule`, new file `internal/runner/checkpoint_schedule.go`). It is a pure decision driven by the run's clock (`Deps.Now`). A polled tip is pushed:
  - once it has stayed unchanged for `checkpointQuiet` (5 s);
  - no sooner than `checkpointMinGap` (1 min) after the last push;
  - after a failed push, only once `checkpointFallback` (3 min) has passed.

  A tip that never stays quiet is pushed `checkpointFallback` after the oldest unpushed one appeared.
- **The checkpointer** (new file `internal/runner/checkpoint.go`). A goroutine that `stage()` starts next to the halt watcher, for the agent's lifetime. It polls every `checkpointPoll` (10 s). A poll reads the run branch's tip locally (`gitops.Repo.CheckpointTip`), with no network call. When the schedule says so, it checks that SHA with:
  - `CountAhead`, the commits ahead of the base;
  - `ScanRange`, a scan for values the run redacts;
  - `workflowGuardAt`, finalize's workflow guard.

  It then pushes fast-forward only, by SHA, to origin's URL (`PushFastForward`).
- **Stage boundaries.** `afterStage` calls `boundaryCheckpoint`, which pushes whatever the stage left committed and unpushed, at once, on the run goroutine.
- **After a push.** `afterCheckpoint` opens the draft (`openDraftPR`, split out of `openDraft`) or rewrites its status section.
- **Settings and prompt.** A config key `git.pr.checkpoints` (default true) turns all of this off. A system-prompt line tells the agent to commit early.

**Tech Stack:** Go 1.27. The work happens in `internal/gitops` (the git CLI), `internal/runner`, `internal/config`, `internal/gitprov/fake`, `schemas/fugaro.schema.json` and the docs. Tests use:
- local bare remotes (`testutil.NewRemote`) and remote hooks;
- the fake git provider (`fake.Provider`, with the `auditProvider` wrapper);
- the scripted agent (`harness`, `step`);
- the test clock (`testClock`) and a test-driven poll (`DriveCheckpoints`).

**Spec:** [docs/design/checkpoint-pushes.md](../design/checkpoint-pushes.md)

**Checked before review:** the code of Tasks 1 to 7 was applied to a scratch copy of `main` (724ab83), and `go vet` is clean. The focused tests named in those tasks pass with `-race`. So does the full `go test -race ./internal/runner/...` (22 minutes), after the five test updates listed in Task 6, which were its only failures. The checkpoint tests are deterministic:
- every poll is sent by the test (`DriveCheckpoints`) and has finished when the call returns;
- time is the run's test clock;
- nothing sleeps.

## Decisions (veto any before execution starts)

- **C1. Pushes follow commits.** The user ruled that work is saved as soon as the agent commits; a fixed 3-minute timer is not enough. Four named constants in `checkpoint_schedule.go` set the schedule:
  - `checkpointPoll` (10 s): how often the checkpointer reads the branch tip. The read is local: a few git commands, and no network call when nothing changed.
  - `checkpointQuiet` (5 s): a new tip is pushed once it has been unchanged this long, so a burst of commits (a rebase, a fix-and-commit loop) becomes one push of the latest. A tip that never goes quiet (an agent committing every few seconds) is pushed `checkpointFallback` after the oldest unpushed tip appeared.
  - `checkpointMinGap` (1 min): at most one push a minute while a stage runs. A commit that arrives inside the window is pushed at the first poll after the window ends.
  - `checkpointFallback` (3 min, the old interval): the slow path. A failed push is retried this long after the failure, not at every poll. It also bounds the wait of a tip that never goes quiet.

  The poll ticker also covers missed events: there are no events to miss, only states read every 10 s. None of these is configurable; `git.pr.checkpoints: false` is the only knob. Veto alternative: a `git.pr.checkpoint_min_gap` duration (one more config task).
- **C1a. A stage boundary pushes at once and is exempt from the per-minute limit.** `afterStage` (after `implement`, `fix`, `review` and `review_first`) calls `boundaryCheckpoint` on the run goroutine, after the stage's checkpointer has stopped:
  - The agent is idle at a boundary, so there is nothing to coalesce.
  - The boundary is when the stage's work is complete.
  - The next stage (usually a review) commits nothing, so without this push the last commits of `implement` would wait up to a minute into the review.

  The CI cost of the exemption is bounded: a run has 2 + 2 × review rounds boundaries (6 with the default 2 rounds), and a boundary whose tip is already pushed costs nothing. Review boundaries rarely push, because reviews do not commit. The exemption covers the whole schedule: the quiet period, the minute and the failure fallback. A boundary right after a failed push still tries, since the agent is idle and it is the last chance before the next stage. A boundary push counts as the last push for the next stage's limit. Veto alternative: boundaries obey the minute too (at most one extra minute of exposure; the code drops the exemption).
- **C1b. Not a git `post-commit` hook.** The agent owns the checkout:
  - it can disable hooks (`core.hooksPath`, `--no-verify` for some hooks, deleting `.git/hooks`);
  - it can commit with plumbing (`git commit-tree`, `update-ref`) that runs no hook;
  - a hook runs inside the agent's process tree, with the agent's environment.

  It would also push from inside the agent's sandbox with whatever credentials it finds there, which is what checkpoints avoid. Polling from the runner sees every commit whatever made it, and runs with the runner's own credentials and guards.
- **C2. Checkpoints have their own opt-out, `git.pr.checkpoints: false` (default true).** Data-loss protection should be on unless someone turns it off on purpose. `git.pr.early_draft` keeps its meaning, "the PR opens early", and the two keys combine:
  - both on (the default): checkpoints push the branch and the first one opens the draft;
  - `early_draft: false`: checkpoints push the branch and the PR opens at finalize;
  - `checkpoints: false`: 0.5.0's pushes exactly (the verified boundary and finalize).

  The key sits under `git.pr` because the reason to turn it off is the PR's CI cost. An older CLI or job image refuses the key as unknown, so the docs say to set it only once everyone is on 0.5.1. Veto alternative: one key for both behaviours (`early_draft: false` also stops checkpoints). The cost is that repositories without drafts lose the protection.
- **C3. The draft opens at the first checkpoint push, unverified.** This supersedes M9e ruling E1, "the remote branch is always a verified state". The section's head is `**Running: work in progress, not verified**` while the pushed commit has no passing clean test. The part `branch at <sha>: not verified|verified` is added after the verify part, and the stage part reads `checkpoint during stage <name>` for an update made mid-stage. The PR title stays unprefixed (M9e E5: the draft badge says it). Readiness (`Decide`), the flip to ready and `ApplyReady` are unchanged.
- **C4. Fast-forward only, never forced, and only from a settled checkout.** `CheckpointTip` refuses with `ErrGitBusy` when HEAD is not the symbolic ref `refs/heads/fugaro/<id>` or when any of `rebase-merge`, `rebase-apply`, `MERGE_HEAD`, `CHERRY_PICK_HEAD`, `REVERT_HEAD` or `BISECT_LOG` exists in the git directory. A poll that sees this does nothing and is silent. `PushFastForward` does the following:
  - pushes `<sha>:refs/heads/<branch>`, the SHA that was read, so the agent moving the branch mid-push changes nothing;
  - pushes to origin's URL (`git remote get-url origin`), not the remote's name, so git writes no `refs/remotes/origin/…` and takes no ref lock the agent's own `git fetch` could meet;
  - refuses with `ErrNotFastForward` when the remote tip is not an ancestor of the SHA;
  - does nothing when the remote is already there (an agent that pushed by itself).

  A rewritten history (amend, rebase, reset) logs one warning per run and is retried at the fallback. The verified-boundary push and finalize keep `Push` (force-with-lease over a tip that is the run's own, `tipBelongsToRun`), so they reconcile a rewritten branch as they do today. Veto alternative: checkpoints also use `Push`. That keeps them going after a rewrite, but a checkpoint could then replace commits already on the remote.
- **C5. Checkpoints use the same guards as finalize, plus a secret scan.**
  - `workflowGuardAt(ctx, sha)` (GitHub only, as today) and a permanent refusal classified by `asRefusal` stop checkpoints for the run. Finalize then ends the run as today: `endRefused`, which saves the work bundle.
  - `ScanRange(since, sha)` runs before each push, where since is `pushed_head`, or the work base before the first push. It uses the run's redactor (`agent.RedactFunc(r.secretList())`). A hit stops checkpoints for the run, because every later push would carry that commit.
  - A binary or too-large range is pushed, as finalize would push it.

  Finalize itself still pushes without scanning; that is out of scope and listed as future work.
- **C6. Follow-ups do not checkpoint in 0.5.1.** Their branch is an open PR that may be ready and have reviewers, so an unverified checkpoint would be visible to them and could be merged. Finalize's `PushExisting(branch, startSHA)` would also refuse a branch the run itself moved mid-run, with `ErrForeignTip`. Veto alternative (later): checkpoint to a side branch `fugaro/<follow-up id>` and delete it at finalize.
- **C6a. Every check uses the SHA the poll read, never HEAD.** The agent commits concurrently, so the poll reads the tip once and then checks that SHA throughout: the commits ahead (`CountAhead`), the secret scan (`ScanRange`), the workflow guard (`WorkflowFilesIn`) and the push. The prototype showed the failure this prevents. A tip read just before the agent's first commit is the base, and checking "ahead" on HEAD instead pushed the base itself as a checkpoint.
- **C7. The checkpointer's lifetime.** It starts in `stage()` right before `Agent.Run` and stops right after it (and in a `defer`, so a panic cannot leave it running). Stopping cancels its context and waits for it, which aborts a push or PR call in flight. Such a push leaves at most a remote branch with no `pushed_head`, and the boundary or finalize pushes over it (the tip is in the run's reflog). Such a PR create leaves a PR that finalize finds by branch (M9e E4). Other rules:
  - a poll does nothing once a halt or a cancel is recorded, or the time budget is spent;
  - the stage context's deadline never passes `finalize_reserve`, and a cancel cancels it.

  The boundary push runs on the run goroutine with the stage's checkpointer already stopped, and only after a stage that succeeded (`afterStage`). A stage that failed, was halted or was cancelled goes straight to finalize, which pushes. Nothing checkpoints during finalize. `checkpointersRunning` counts live checkpointers, so a test sees each stage's stop.
- **C8. Opening the draft is tried at most twice per run (`checkpointOpenTries`), once per checkpoint push.** Each try is `ensurePR`'s 3 attempts within `earlyOpenTimeout`. After that the verified boundary (`afterStage`'s `openDraft`) or finalize opens the PR. A failed push is a warning, logged once per kind per run.
- **C9. The prompt line goes only where it is true:** first runs with checkpoints on. It reads: commit early and often; Fugaro pushes each new commit within about a minute; only pushed commits survive a dead container; uncommitted changes are lost; add new commits rather than amend or rebase.
- **C10. Nothing new in `result.json`.** `pushed_head` is saved after every checkpoint push (as `pushBranch` does), so a crashed run's record says what reached the remote. Checkpoints are logged (`checkpoint pushed`, with the stage, the short SHA, a count and whether it was a boundary).
- **C11. Release 0.5.1.** This is a patch release, shipped with `fugaro image refresh`. `docs/releases/v0.5.1.md` gets one section; it is created if image refresh's Task 12 has not created it yet. The runner reaches a repository only when its job image is rebuilt from 0.5.1.

**The cost model (for the docs and the release notes).** Worst case, an agent committing without pause through a whole stage, there are 60 pushes an hour (one a minute) plus one per stage boundary. Each push costs:
- one `ls-remote` and one push;
- with a PR, one description read and one write;
- with a PR, one CI run of the repository's PR workflows, and `on: push` or Bitbucket branch pipelines run even without a PR.

An idle agent costs nothing beyond the local poll. A typical agent that commits after each working step pushes a few times per stage. The opt-out is `git.pr.checkpoints: false`.

## Global Constraints

Every task's requirements include these.

From the design:
- A checkpoint pushes only `fugaro/<run id>`, fast-forward only, by SHA, with the runner's credentials. It never commits, never reads or writes the working tree or the index, and writes nothing in the checkout's `.git`.
- A poll is local: it makes no network call and no provider call unless the schedule says to push.
- A checkpoint never fails, blocks or delays the run beyond the cancellation of its own in-flight call. Every error is a warning, and a panic is recovered inside the goroutine.
- A checkpoint is never less guarded than finalize's push: the workflow guard, the host's permanent refusals, plus the secret scan.
- Readiness, reviewers and labels at ready, finalize, follow-ups and `early_draft: false` behave as in 0.5.0, except for the documented changes: an unverified draft, and branch pushes mid-stage and at every boundary.
- Not doing: uncommitted-work snapshots, follow-up checkpoints, configurable timings, a git hook, a finalize secret scan.

Project rules:
- No live cloud applies in tests. Use local bare remotes, remote hooks, the fake provider, the scripted agent and the test clock. Never touch EdgeWeb or EdgeServer, and handle no real secrets.
- Subagent-driven development in one git worktree for this PR group (`.worktrees/checkpoint-pushes`), with a fresh implementer per task. The token-economy rule applies: **Task 5 gets its own review** (it changes every run's stage loop), and the rest are reviewed once on the branch at the end. Dogfood runs, if any, also run from git worktrees.
- Every CI check (`test`, `terraform`, `rules`) is read before merge, each job's log and not only the summary.
- Docs must match behaviour. Task 8's docs test pins the four timings, the cost model, the key and the removed "first verified push" wording, and the `rules` tests (`plugin/*_test.go`) must stay green.
- Releases go through `/new-release` with `docs/releases/vX.Y.Z.md` merged first.
- **The `internal/runner` package is slow (about 19 minutes in full).** Each task runs only the focused tests it names (`-run`), and the PR group runs **one** full `go test ./internal/runner/...` at the end (Task 9). CI runs with `-race`, so the focused runner tests run with `-race` too.
- **No test waits for wall-clock time.** Checkpoint tests drive the poll (`runner.DriveCheckpoints`) and the clock (`testClock`).

## Review Focus

The five failure modes most likely to hit a user, each pinned by a test:

1. **Committed work is still lost when the container dies (the incident).** Expected:
   - a commit is on the remote within one quiet period, or at the end of the current minute;
   - a commit made right before a stage ends is pushed at that boundary;
   - a failed push is retried at the fallback.

   Pinned by `TestCheckpointPushesCommittedWorkMidStage` and `TestFailedPushRetriedByTheFallback` (Task 5), `TestCommitThenStageEndPushedAtTheBoundary` (Task 6), and `TestCheckpointSchedule` (Task 4).
2. **Too many pushes (CI cost, rate limits) or pushes nobody asked for.** Expected:
   - a burst of commits is one push of the latest;
   - commits every 20 s are pushed at most once a minute, the latest at the window's end;
   - an unchanged tip pushes nothing and calls nothing remote.

   Pinned by `TestBurstOfCommitsIsOnePush`, `TestAtMostOnePushPerMinute` and `TestIdlePollsNeverTouchTheRemote` (Task 5), and `TestCheckpointSchedule` (Task 4).
3. **A checkpoint publishes what finalize would not, or fights the agent's git.** Cases: a workflow file on GitHub, a value the run redacts, a rewritten history, a half-done rebase. Expected: nothing is pushed, with one warning, and finalize ends or reconciles the run as today. Pinned by:
   - `TestCheckpointSkipsWorkflowCommits`, `TestCheckpointStopsOnAKnownSecret` and `TestCheckpointNeverRewritesThePushedBranch` (Task 5);
   - `TestPushFastForwardRefusesRewrittenHistory`, `TestCheckpointTipOnlyWhenSettled` and `TestCountAheadOfACommit` (Task 1).
4. **The early draft misleads: it looks verified or ready, or notifies someone.** Expected: a draft with no reviewers or labels, whose number is saved before any other provider call, marked `not verified` until a passing clean test covers the pushed commit. It turns `verified` after one, and becomes ready only at finalize. Pinned by `TestCheckpointOpensDraftMarkedNotVerified` and `TestStatusSaysVerifiedAfterAPassingTest` (Task 6), with `auditProvider`'s every-call checks.
5. **Checkpoints outlive their stage or ignore an opt-out, a halt, a cancel or a follow-up.** Expected:
   - exactly one checkpointer per running stage, and none after the run;
   - no mid-stage push with `git.pr.checkpoints: false`, after a recorded halt or cancel, or in a follow-up;
   - `early_draft: false` pushes the branch but opens no PR mid-run.

   Pinned by `TestStageEndStopsTheCheckpointer`, `TestCheckpointsOffPushesNothingMidStage`, `TestNoCheckpointAfterHalt`, `TestNoCheckpointAfterCancel` and `TestFollowUpDoesNotCheckpoint` (Task 5), and `TestEarlyDraftFalseCheckpointsWithoutPR` (Task 6).

---

## File Structure

| Path | Responsibility | Task |
|---|---|---|
| `internal/gitops/checkpoint.go` (new), `gitops.go`, `gitops_test.go` | `CheckpointTip`, `PushFastForward`, `CountAhead`, `ErrGitBusy`, `ErrNotFastForward` | 1 |
| `internal/gitops/rejected.go`, `rejected_test.go` | `ScanRange`, `WorkflowFilesIn` | 1 |
| `internal/config/config.go`, `defaults.go`, `example.yaml`, `config_test.go`, `schemas/fugaro.schema.json`, `testdata/config/valid/full.yaml` | `git.pr.checkpoints` | 2 |
| `internal/gitprov/fake/fake.go`, `fake_test.go` | `Snapshot` for race-free reads in tests | 3 |
| `internal/runner/checkpoint_schedule.go` (new), `checkpoint_schedule_internal_test.go` (new) | the timings and `checkpointSchedule` | 4 |
| `internal/runner/checkpoint.go` (new), `runner.go`, `refused.go`, `prflow.go`, `export_test.go`, `checkpoint_test.go` (new) | the checkpointer, its poll and its guards | 5 |
| `internal/runner/checkpoint.go`, `prflow.go`, `checkpoint_pr_test.go` (new), `prflow_test.go`, `golden_test.go` | the boundary push, the draft at the first checkpoint, the section | 6 |
| `internal/runner/prompts.go`, `runner.go`, `pure_test.go` | the prompt line | 7 |
| `docs/design/v1.md`, `docs/git-providers.md`, `docs/gcp-setup.md`, `README.md`, `plugin/skills/working/SKILL.md`, `plugin/skills/working/reference/launch.md`, `plugin/skills/working/reference/diagnose.md`, `internal/runner/docs_checkpoint_internal_test.go` (new) | docs | 8 |
| none | full suite, PR | 9 |
| `docs/releases/v0.5.1.md` | release | 10 |

## PR group

One PR, branch `checkpoint-pushes`, Tasks 1 to 9 in order. Task 10 runs after the merge. Tasks 1, 2, 3 and 4 are independent of each other; Task 5 needs all four.

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
    # checkpoints: true       # push each new commit within about a minute, and at every stage end (fast-forward only); false: only at verified stage ends and at the end
```

In `schemas/fugaro.schema.json`, in `git.pr.properties` after `early_draft`:

```json
            "checkpoints": { "type": "boolean", "description": "Push each commit a run makes to its branch within about a minute (after 5 s without a newer one, at most one push a minute, and at once at every stage boundary), fast-forward only, so a container that dies keeps its committed work (default true). With early_draft, the first checkpoint opens the draft pull request, marked not verified. False pushes only at verified stage ends and at the end. Every push to a branch with an open pull request runs its CI: at worst 60 pushes an hour. Fugaro 0.5.1 or later." }
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

### Task 4: The checkpoint schedule

**Files:**
- Create: `internal/runner/checkpoint_schedule.go`
- Test: `internal/runner/checkpoint_schedule_internal_test.go`

**Interfaces:**
- Consumes: nothing (pure).
- Produces:
  ```go
  const checkpointPoll = 10 * time.Second
  const checkpointQuiet = 5 * time.Second
  const checkpointMinGap = time.Minute
  const checkpointFallback = 3 * time.Minute
  const checkpointOpenTries = 2 // used by Task 6
  type checkpointSchedule struct { tip string; since, firstNew, lastPush, failedAt time.Time }
  func (s *checkpointSchedule) due(now time.Time, tip, pushed string) bool
  func (s *checkpointSchedule) pushed(now time.Time)
  func (s *checkpointSchedule) failed(now time.Time)
  ```

- [ ] **Step 1: Write the failing test**

Create `internal/runner/checkpoint_schedule_internal_test.go`:

```go
package runner

import (
	"fmt"
	"testing"
	"time"
)

// TestCheckpointSchedule pins when a polled tip is pushed: after a quiet
// period, at most once a minute, the retry after a failure at the
// fallback, and a tip that never goes quiet at the fallback.
func TestCheckpointSchedule(t *testing.T) {
	t0 := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	var s checkpointSchedule
	check := func(d time.Duration, tip, pushed string, want bool) {
		t.Helper()
		if got := s.due(t0.Add(d), tip, pushed); got != want {
			t.Fatalf("due(+%v, %s, pushed %s) = %v, want %v", d, tip, pushed, got, want)
		}
	}
	check(0, "a", "a", false)             // unchanged: nothing to push
	check(0, "b", "a", false)             // new: the quiet period starts
	check(3*time.Second, "c", "a", false) // a burst: it starts again
	check(7*time.Second, "c", "a", false)
	check(8*time.Second, "c", "a", true) // still for checkpointQuiet: push the latest
	s.pushed(t0.Add(8 * time.Second))
	check(10*time.Second, "c", "c", false)
	check(20*time.Second, "d", "c", false)
	check(40*time.Second, "d", "c", false) // quiet, but inside the minute
	check(67*time.Second, "d", "c", false)
	check(68*time.Second, "d", "c", true) // the window ended: pushed then
	s.failed(t0.Add(68 * time.Second))
	check(2*time.Minute, "d", "c", false) // a failed push waits for the fallback
	check(68*time.Second+checkpointFallback, "d", "c", true)
	end := 68*time.Second + checkpointFallback
	s.pushed(t0.Add(end))
	// An agent that commits every 4 s never leaves the tip quiet: the tip
	// is pushed anyway once the oldest unpushed one is checkpointFallback old.
	start := end + checkpointMinGap
	for i := 0; ; i++ {
		d := time.Duration(i) * 4 * time.Second
		want := d >= checkpointFallback
		check(start+d, fmt.Sprint("busy", i), "c", want)
		if want {
			break
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/runner/ -run TestCheckpointSchedule`
Expected: FAIL to compile: `undefined: checkpointSchedule`.

- [ ] **Step 3: Write the implementation**

Create `internal/runner/checkpoint_schedule.go`:

```go
package runner

import "time"

// The checkpoint schedule (design checkpoint-pushes.md): when a polled
// branch tip is pushed. checkpoint.go runs it.

const (
	// checkpointPoll is how often a running stage's branch tip is read. The
	// read is local (no network), so it costs a few git commands.
	checkpointPoll = 10 * time.Second
	// checkpointQuiet is how long a new tip must stay unchanged before it
	// is pushed, so a burst of commits is one push of the latest.
	checkpointQuiet = 5 * time.Second
	// checkpointMinGap is the least time between two pushes while a stage
	// runs: at most one push a minute, and a commit inside the window is
	// pushed when it ends. Stage boundaries are exempt.
	checkpointMinGap = time.Minute
	// checkpointFallback is the slow path: the retry after a failed push,
	// and the most a tip waits when the agent never stops committing long
	// enough for checkpointQuiet.
	checkpointFallback = 3 * time.Minute
	// checkpointOpenTries bounds how many checkpoint pushes try to open the
	// draft; after that the verified boundary or finalize does.
	checkpointOpenTries = 2
)

// checkpointSchedule decides when a polled tip is pushed. Its clock is
// the run's (Deps.Now), so tests drive it with a fake one.
type checkpointSchedule struct {
	tip      string    // the newest unpushed tip seen
	since    time.Time // when tip was first seen
	firstNew time.Time // when the oldest unpushed tip was first seen
	lastPush time.Time // the last successful push
	failedAt time.Time // the last failed push, zero after a success
}

// due reports whether tip, polled at now, is to be pushed; pushed is the
// run's pushed_head.
func (s *checkpointSchedule) due(now time.Time, tip, pushed string) bool {
	if tip == pushed {
		s.tip, s.since, s.firstNew = "", time.Time{}, time.Time{}
		return false
	}
	if s.tip == "" {
		s.firstNew = now
	}
	if tip != s.tip {
		s.tip, s.since = tip, now
	}
	switch {
	case !s.failedAt.IsZero():
		return now.Sub(s.failedAt) >= checkpointFallback
	case !s.lastPush.IsZero() && now.Sub(s.lastPush) < checkpointMinGap:
		return false
	}
	return now.Sub(s.since) >= checkpointQuiet || now.Sub(s.firstNew) >= checkpointFallback
}

// pushed records a successful push at now; failed a failed one.
func (s *checkpointSchedule) pushed(now time.Time) {
	*s = checkpointSchedule{lastPush: now}
}

func (s *checkpointSchedule) failed(now time.Time) { s.failedAt = now }
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/runner/ -run TestCheckpointSchedule`
Expected: PASS (it is pure: under a second of test time once the package is built).

- [ ] **Step 5: Commit**

```bash
git add internal/runner/checkpoint_schedule.go internal/runner/checkpoint_schedule_internal_test.go
git commit -m "runner: the checkpoint schedule (quiet period, one push a minute, fallback)"
```

---

### Task 5: The checkpointer (critical: own review)

**Files:**
- Create: `internal/runner/checkpoint.go`, `internal/runner/checkpoint_test.go`
- Modify: `internal/runner/runner.go` (the `run` struct, `stage()`), `internal/runner/refused.go` (`workflowGuardAt`), `internal/runner/prflow.go` (`refreshForPush`), `internal/runner/export_test.go`

**Interfaces:**
- Consumes:
  - from Task 1: `gitops.(*Repo).CheckpointTip`, `CountAhead`, `PushFastForward`, `ScanRange`, `WorkflowFilesIn`, `gitops.ErrGitBusy`, `gitops.ErrNotFastForward`;
  - from Task 2: `config.PRSettings.CheckpointsOn`;
  - from Task 3: `fake.(*Provider).Snapshot`;
  - from Task 4: `checkpointSchedule`, the four timings;
  - existing: `refreshGitAuth`, `warnAuthRefresh`, `authValidity`, `asRefusal`, `refusalText`, `workBase`, `haltValue`, `isCancelled`, `budget.Exhausted`, `agent.RedactFunc`, `secretList`, `redact`, `save`, `shortSHA`, `pushTimeout`;
  - in tests: `prHarness`, `prCfg`, `newClock`, `testClock`, `probe`, `followUpHarness`, `newHandleBox`, `runCapHalt`, `implement`, `implementVerified`, `review`, `shell`, `blockUntilDone`, `runRefused`, `remoteHasBranch`, `mustReady`, `followID`, `runID`, `runner.SecretEnvsVar`.
- Produces:
  ```go
  var checkpointTicks = func(d time.Duration) (<-chan time.Time, func()) // the poll's ticker; tests drive it
  var checkpointTicked func()                                              // tests only
  var checkpointersRunning atomic.Int32
  type checkpointState struct { sched checkpointSchedule; stopped bool; warned map[string]bool; openTries, pushes int }
  // run gains: ckpt checkpointState
  func (r *run) checkpointsOn() bool
  func (r *run) startCheckpoints(stageCtx context.Context, stage string) (stop func())
  func (r *run) checkpointBlocked(ctx context.Context) bool
  func (r *run) checkpointTip(ctx context.Context) (string, bool)
  func (r *run) checkpointTick(ctx context.Context, stage string)
  func (r *run) checkpointPush(ctx context.Context, stage, sha string, during bool)
  func (r *run) checkpointClean(ctx context.Context, sha string) bool
  func (r *run) pushCheckpoint(ctx context.Context, sha string) error
  func (r *run) warnCheckpoint(key, msg string, args ...any)
  func (r *run) stopCheckpoints(reason string, args ...any)
  func (r *run) refreshForPush(ctx context.Context)
  func (r *run) workflowGuardAt(ctx context.Context, until string) *gitops.PushRejected
  // export_test.go:
  func DriveCheckpoints(t *testing.T) (tick func())
  func CheckpointersRunning() int32
  const CheckpointQuiet, CheckpointMinGap, CheckpointFallback
  ```

- [ ] **Step 1: Write the failing tests**

Add to `internal/runner/export_test.go`:

```go
// DriveCheckpoints replaces the checkpoint poll's ticker for the rest of
// t: the returned tick sends one poll to the running stage's checkpointer
// and returns once that poll has finished. It fails t when no checkpointer
// takes the poll within 10 seconds.
func DriveCheckpoints(t *testing.T) (tick func()) {
	ch := make(chan time.Time)
	done := make(chan struct{})
	prevTicks, prevTicked := checkpointTicks, checkpointTicked
	checkpointTicks = func(time.Duration) (<-chan time.Time, func()) { return ch, func() {} }
	checkpointTicked = func() { done <- struct{}{} }
	t.Cleanup(func() { checkpointTicks, checkpointTicked = prevTicks, prevTicked })
	return func() {
		t.Helper()
		select {
		case ch <- time.Time{}:
		case <-time.After(10 * time.Second):
			t.Fatal("no checkpointer took the poll")
		}
		<-done
	}
}

// CheckpointersRunning is how many checkpoint goroutines are alive.
func CheckpointersRunning() int32 { return checkpointersRunning.Load() }

// The checkpoint schedule's durations, for tests that move a fake clock.
const (
	CheckpointQuiet    = checkpointQuiet
	CheckpointMinGap   = checkpointMinGap
	CheckpointFallback = checkpointFallback
)
```

Create `internal/runner/checkpoint_test.go`:

```go
package runner_test

import (
	"bytes"
	"context"
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

// ckptRig is a harness whose checkpointer the test drives: tick sends one
// poll and returns when it is done, and the run's clock moves only when
// the test moves it. Nothing in it waits for wall-clock time.
type ckptRig struct {
	*harness
	clock *testClock
	tick  func()
	logs  *bytes.Buffer // read only after the run
}

func newCkptRig(t *testing.T, cfg string) *ckptRig {
	t.Helper()
	tick := runner.DriveCheckpoints(t)
	// The fixture's 5-minute budget is too short for a clock moved by
	// minutes; the stages' own timeouts still count real time.
	h := prHarness(t, strings.Replace(cfg, "total: 5m", "total: 1h", 1))
	c := newClock()
	h.deps.Now = c.Now
	logs := &bytes.Buffer{}
	h.deps.Log = slog.New(slog.NewTextHandler(logs, nil))
	return &ckptRig{harness: h, clock: c, tick: tick, logs: logs}
}

// settle polls, lets a quiet period and a rate window pass, and polls
// again: a new tip is pushed by the second poll unless something holds
// it back.
func (g *ckptRig) settle() {
	g.tick()
	g.clock.advance(runner.CheckpointMinGap)
	g.tick()
}

// remoteTip is the run branch's tip on the remote, "" when it is absent.
func remoteTip(t *testing.T, h *harness) string {
	t.Helper()
	return strings.TrimSpace(testutil.Git(t, h.remote, "for-each-ref", "--format=%(objectname)", "refs/heads/fugaro/"+runID))
}

// pushedHead is the pushed_head saved in the run record.
func pushedHead(t *testing.T, h *harness) string {
	t.Helper()
	rec, err := h.store.ReadRecord(context.Background())
	if err != nil {
		return ""
	}
	return rec.PushedHead
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

// commitWIP commits a file without verifying it and returns HEAD.
func commitWIP(t *testing.T, req agent.Request, name string) string {
	t.Helper()
	shell(t, req, "echo "+name+" > "+name+".txt && git add -A && git commit -qm 'wip "+name+"'")
	return strings.TrimSpace(testutil.Git(t, req.Dir, "rev-parse", "HEAD"))
}

// TestCheckpointPushesCommittedWorkMidStage is the incident: work
// committed in a long stage is on the remote before the stage ends.
func TestCheckpointPushesCommittedWorkMidStage(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ", early_draft: false"))
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		head := commitWIP(t, req, "wip")
		g.settle()
		if pushedHead(t, g.harness) != head || remoteTip(t, g.harness) != head {
			t.Errorf("the committed work is not on the remote mid-stage")
		}
		return implement("feature")(t, ctx, req)
	}
	rec, err := g.run(t, long, review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady || remoteTip(t, g.harness) != rec.HeadSHA {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if !strings.Contains(g.logs.String(), "checkpoint pushed") {
		t.Fatalf("no checkpoint logged:\n%s", g.logs)
	}
}

// TestBurstOfCommitsIsOnePush: commits closer together than the quiet
// period are one push, of the latest.
func TestBurstOfCommitsIsOnePush(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ", early_draft: false"))
	pushes := countPushes(t, g.harness)
	burst := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		var last string
		for _, name := range []string{"one", "two", "three"} {
			last = commitWIP(t, req, name)
			g.tick()
			g.clock.advance(2 * time.Second)
		}
		if n := pushes(); n != 0 {
			t.Errorf("%d pushes inside the burst", n)
		}
		g.clock.advance(runner.CheckpointQuiet)
		g.tick()
		if n := pushes(); n != 1 || pushedHead(t, g.harness) != last {
			t.Errorf("after the burst: %d pushes, pushed %s, want 1 of %s", n, pushedHead(t, g.harness), last)
		}
		return implement("feature")(t, ctx, req)
	}
	if _, err := g.run(t, burst, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
}

// TestAtMostOnePushPerMinute: commits every 20 s are pushed at most once a
// minute, the tip at the end of the window.
func TestAtMostOnePushPerMinute(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ", early_draft: false"))
	pushes := countPushes(t, g.harness)
	steady := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		commitWIP(t, req, "c1")
		g.tick()
		g.clock.advance(runner.CheckpointQuiet)
		g.tick() // the first push, at T
		var last string
		for _, name := range []string{"c2", "c3"} {
			g.clock.advance(20 * time.Second)
			last = commitWIP(t, req, name)
			g.tick()
		}
		g.clock.advance(19 * time.Second) // T+59s
		g.tick()
		if n := pushes(); n != 1 {
			t.Errorf("%d pushes inside the minute, want 1", n)
		}
		g.clock.advance(time.Second) // T+60s: the window ended
		g.tick()
		if n := pushes(); n != 2 || pushedHead(t, g.harness) != last {
			t.Errorf("at the window's end: %d pushes, pushed %s, want 2 with %s", n, pushedHead(t, g.harness), last)
		}
		return implement("feature")(t, ctx, req)
	}
	if _, err := g.run(t, steady, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
}

// TestFailedPushRetriedByTheFallback: a refused push warns once and is
// retried checkpointFallback later, not at every poll.
func TestFailedPushRetriedByTheFallback(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ", early_draft: false"))
	flag, tried := filepath.Join(t.TempDir(), "refuse"), filepath.Join(t.TempDir(), "tried")
	if err := os.WriteFile(flag, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	hook := "#!/bin/sh\ncat >/dev/null\nif [ -e " + flag + " ]; then touch " + tried + "; echo 'try later' >&2; exit 1; fi\n"
	if err := os.WriteFile(filepath.Join(g.remote, "hooks", "pre-receive"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		head := commitWIP(t, req, "wip")
		g.tick()
		g.clock.advance(runner.CheckpointQuiet)
		g.tick()
		if _, err := os.Stat(tried); err != nil || remoteHasBranch(t, g.harness) {
			t.Fatalf("the first push was not tried, or not refused (%v)", err)
		}
		if err := os.Remove(flag); err != nil {
			t.Fatal(err)
		}
		g.clock.advance(time.Minute)
		g.tick()
		if remoteHasBranch(t, g.harness) {
			t.Error("a failed push was retried before the fallback")
		}
		g.clock.advance(runner.CheckpointFallback)
		g.tick()
		if pushedHead(t, g.harness) != head {
			t.Error("the fallback did not retry the push")
		}
		return implement("feature")(t, ctx, req)
	}
	rec, err := g.run(t, long, review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if n := strings.Count(g.logs.String(), "a checkpoint push failed"); n != 1 {
		t.Fatalf("%d push warnings, want 1:\n%s", n, g.logs)
	}
}

// TestIdlePollsNeverTouchTheRemote: once the tip is pushed, polls read
// only the checkout: with the remote gone, nothing fails and the provider
// is not called.
func TestIdlePollsNeverTouchTheRemote(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ""))
	pushes := countPushes(t, g.harness)
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		commitWIP(t, req, "wip")
		g.settle()
		calls := len(g.provider.Snapshot().Calls)
		away := g.remote + ".away"
		if err := os.Rename(g.remote, away); err != nil {
			t.Fatal(err)
		}
		for range 5 {
			g.clock.advance(30 * time.Second)
			g.tick()
		}
		if err := os.Rename(away, g.remote); err != nil {
			t.Fatal(err)
		}
		if n := len(g.provider.Snapshot().Calls); n != calls || pushes() != 1 {
			t.Errorf("idle polls: provider calls %d -> %d, pushes %d", calls, n, pushes())
		}
		return implement("feature")(t, ctx, req)
	}
	if _, err := g.run(t, long, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(g.logs.String(), "level=WARN") {
		t.Fatalf("an idle poll warned:\n%s", g.logs)
	}
}

// TestStageEndStopsTheCheckpointer: each stage has exactly one
// checkpointer, and none outlives the run.
func TestStageEndStopsTheCheckpointer(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ""))
	one := func(t *testing.T) {
		if n := runner.CheckpointersRunning(); n != 1 {
			t.Errorf("%d checkpointers running in a stage, want 1", n)
		}
	}
	if _, err := g.run(t, probe(one, implement("feature")), probe(one, review("ship", 0))); err != nil {
		t.Fatal(err)
	}
	if n := runner.CheckpointersRunning(); n != 0 {
		t.Fatalf("%d checkpointers outlived the run", n)
	}
}

// TestCheckpointNeverRewritesThePushedBranch: after the agent amends a
// pushed commit, checkpoints refuse (one warning) and finalize pushes the
// final branch as it always did.
func TestCheckpointNeverRewritesThePushedBranch(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ", early_draft: false"))
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		first := commitWIP(t, req, "wip")
		g.settle()
		shell(t, req, "git commit -q --amend -m 'wip, amended'")
		g.settle()
		g.settle()
		if got := remoteTip(t, g.harness); got != first {
			t.Errorf("a checkpoint replaced the pushed commit: remote at %s, want %s", got, first)
		}
		return implement("feature")(t, ctx, req)
	}
	rec, err := g.run(t, long, review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady || remoteTip(t, g.harness) != rec.HeadSHA {
		t.Fatalf("finalize did not push the final branch: rec = %+v, err = %v", rec, err)
	}
	if n := strings.Count(g.logs.String(), "were rewritten"); n != 1 {
		t.Fatalf("%d rewrite warnings, want 1:\n%s", n, g.logs)
	}
}

// TestCheckpointSkipsWorkflowCommits: on GitHub a commit that changes a
// workflow file is never pushed by a checkpoint; finalize refuses as today.
func TestCheckpointSkipsWorkflowCommits(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ""))
	ci := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "mkdir -p .github/workflows && echo 'on: push' > .github/workflows/ci.yml && git add -A && git commit -qm 'Add CI'")
		g.settle()
		if remoteHasBranch(t, g.harness) {
			t.Error("a checkpoint pushed a workflow change")
		}
		return implementVerified(t, ctx, req)
	}
	runRefused(t, g.harness, ci, review("ship", 0))
	if n := strings.Count(g.logs.String(), "no more checkpoint pushes"); n != 1 {
		t.Fatalf("%d stop notices, want 1:\n%s", n, g.logs)
	}
}

// TestCheckpointStopsOnAKnownSecret: a commit holding a value the run
// redacts is never pushed mid-run, nor anything after it, even once a
// later commit removes the value.
func TestCheckpointStopsOnAKnownSecret(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ""))
	const mounted = "mounted-checkpoint-secret-value"
	g.deps.Env = append(g.deps.Env, "RENAMED_TOKEN="+mounted, runner.SecretEnvsVar+"=RENAMED_TOKEN")
	leak := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		shell(t, req, "echo "+mounted+" > leak.txt && git add -A && git commit -qm leak")
		g.settle()
		shell(t, req, "git rm -q leak.txt && git commit -qm unleak")
		g.settle()
		if remoteHasBranch(t, g.harness) {
			t.Error("a checkpoint pushed commits holding a secret value")
		}
		return implement("feature")(t, ctx, req)
	}
	if _, err := g.run(t, leak, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(g.logs.String(), "no more checkpoint pushes") || strings.Contains(g.logs.String(), mounted) {
		t.Fatalf("logs:\n%s", g.logs)
	}
}

// TestCheckpointsOffPushesNothingMidStage: git.pr.checkpoints false runs
// no checkpointer at all.
func TestCheckpointsOffPushesNothingMidStage(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ", checkpoints: false"))
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if n := runner.CheckpointersRunning(); n != 0 {
			t.Errorf("%d checkpointers with checkpoints false", n)
		}
		return implement("feature")(t, ctx, req)
	}
	rec, err := g.run(t, long, review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

// TestNoCheckpointAfterHalt: once a halt is recorded, nothing is pushed
// mid-stage; finalize pushes as it does for any halt.
func TestNoCheckpointAfterHalt(t *testing.T) {
	b := newHandleBox(t)
	g := newCkptRig(t, prCfg(t, 2, ""))
	halted := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if !b.h.HaltNow(runCapHalt) {
			t.Fatal("HaltNow was refused")
		}
		commitWIP(t, req, "wip")
		g.settle()
		if remoteHasBranch(t, g.harness) {
			t.Error("a checkpoint pushed after the halt")
		}
		return agent.Result{CostUSD: 1}, nil
	}
	rec, err := g.run(t, halted)
	if err != nil || rec.Status != runstore.StatusHalted {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

// TestNoCheckpointAfterCancel: once a cancel is recorded, nothing is
// pushed mid-stage; finalize leaves the cancelled draft as today.
func TestNoCheckpointAfterCancel(t *testing.T) {
	b := newHandleBox(t)
	g := newCkptRig(t, prCfg(t, 2, ""))
	cancelled := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if !b.h.MarkCancelled() {
			t.Fatal("MarkCancelled was refused")
		}
		commitWIP(t, req, "wip")
		g.settle()
		if remoteHasBranch(t, g.harness) {
			t.Error("a checkpoint pushed after the cancel")
		}
		if err := g.store.RequestCancel(context.Background()); err != nil {
			t.Fatal(err)
		}
		return blockUntilDone(t, ctx, req)
	}
	rec, err := g.run(t, cancelled)
	if err != nil || rec.Status != runstore.StatusCancelled {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

// TestFollowUpDoesNotCheckpoint: a follow-up runs no checkpointer and
// pushes only at finalize, as in 0.5.0 (decision C6).
func TestFollowUpDoesNotCheckpoint(t *testing.T) {
	runner.DriveCheckpoints(t) // the first run's polls are never sent
	h := followUpHarness(t, "", nil, implement("feature"), review("ship", 0))
	start := remoteTip(t, h.harness)
	h.followUp(t, followID, runID, "")
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if n := runner.CheckpointersRunning(); n != 0 {
			t.Errorf("%d checkpointers in a follow-up", n)
		}
		commitWIP(t, req, "more")
		if got := remoteTip(t, h.harness); got != start {
			t.Errorf("the follow-up's branch moved mid-stage: %s -> %s", start, got)
		}
		return implement("again")(t, ctx, req)
	}
	rec, err := h.run(t, long, review("ship", 0))
	mustReady(t, rec, err)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -race ./internal/runner/ -run 'TestCheckpoint|TestBurstOfCommitsIsOnePush|TestAtMostOnePushPerMinute|TestFailedPushRetriedByTheFallback|TestIdlePollsNeverTouchTheRemote|TestStageEndStopsTheCheckpointer|TestNoCheckpointAfter|TestFollowUpDoesNotCheckpoint'`
Expected: FAIL to compile: `undefined: checkpointTicks` (in `export_test.go`).

- [ ] **Step 3: Write the implementation**

Create `internal/runner/checkpoint.go`:

```go
package runner

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/gitops"
)

// Checkpoint pushes (design checkpoint-pushes.md). While a first run's
// stage runs, the runner reads the run branch's tip every checkpointPoll,
// locally, and pushes a new tip once it has been still for checkpointQuiet,
// at most once per checkpointMinGap; each stage boundary pushes what is
// left at once. Pushes are fast-forward only, by sha, with the runner's
// credentials and the same guards as finalize's push, plus a scan for the
// values the run redacts. A checkpoint never fails the run, never forces,
// never commits and never touches the working tree or the index.

// checkpointTicks makes the poll's ticker; tests replace it with a channel
// they drive (DriveCheckpoints).
var checkpointTicks = func(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
}

// checkpointTicked, when set, is called at the end of every poll (tests).
var checkpointTicked func()

// checkpointersRunning counts the checkpoint goroutines alive, so a test
// can see that a stage's end stopped its own.
var checkpointersRunning atomic.Int32

// checkpointState is the run's checkpoint bookkeeping. During a stage only
// the checkpoint goroutine touches it (and the run record); the run
// goroutine uses it only after startCheckpoints' stop has returned.
type checkpointState struct {
	sched     checkpointSchedule
	stopped   bool            // a permanent reason: no more checkpoints this run
	warned    map[string]bool // warnings already logged, by kind
	openTries int             // checkpoint pushes that tried to open the draft
	pushes    int             // checkpoint pushes made
}

// checkpointsOn reports whether this run checkpoints: a first run whose
// fugaro.yaml leaves git.pr.checkpoints on.
func (r *run) checkpointsOn() bool {
	return r.follow == nil && r.cfg != nil && r.cfg.Git.PR.CheckpointsOn()
}

// startCheckpoints starts the stage's checkpoint goroutine on stageCtx and
// returns its stop, which cancels it and waits for it: nothing of it runs
// once stop returns. stop is safe to call more than once.
func (r *run) startCheckpoints(stageCtx context.Context, stage string) (stop func()) {
	if !r.checkpointsOn() || r.ckpt.stopped {
		return func() {}
	}
	ctx, cancel := context.WithCancel(stageCtx)
	ticks, stopTicks := checkpointTicks(checkpointPoll)
	done := make(chan struct{})
	checkpointersRunning.Add(1)
	go func() {
		defer close(done)
		defer checkpointersRunning.Add(-1)
		defer stopTicks()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticks:
				r.checkpointTick(ctx, stage)
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

// stopCheckpoints ends checkpoints for the run, for a reason no later poll
// can change; finalize then pushes, or refuses, as it always did.
func (r *run) stopCheckpoints(reason string, args ...any) {
	r.ckpt.stopped = true
	r.d.Log.Warn("no more checkpoint pushes this run: "+reason+"; finalize pushes as usual", args...)
}

// checkpointBlocked reports whether no checkpoint may run now.
func (r *run) checkpointBlocked(ctx context.Context) bool {
	return ctx.Err() != nil || r.ckpt.stopped || r.pr.gone || r.haltValue() != nil || r.isCancelled() || r.budget.Exhausted()
}

// checkpointTip reads the run branch's tip, locally; false when the
// checkout is busy (the next poll looks again) or the read failed.
func (r *run) checkpointTip(ctx context.Context) (string, bool) {
	sha, err := r.repo.CheckpointTip(ctx, r.rec.Branch)
	switch {
	case errors.Is(err, gitops.ErrGitBusy):
		return "", false
	case err != nil:
		r.warnCheckpoint("tip", "reading the run branch for a checkpoint failed", "err", r.redact(err.Error()))
		return "", false
	}
	return sha, true
}

// checkpointTick is one poll: read the tip locally and push it when the
// schedule says so. A poll whose tip is unchanged makes no network call.
// Every failure is a warning; a panic is recovered here, as a goroutine's
// panic would end the process.
func (r *run) checkpointTick(ctx context.Context, stage string) {
	if checkpointTicked != nil {
		defer checkpointTicked()
	}
	defer func() {
		if p := recover(); p != nil {
			r.d.Log.Error("a checkpoint panicked; carrying on", "stage", stage, "panic", fmt.Sprint(p))
		}
	}()
	if r.checkpointBlocked(ctx) {
		return
	}
	sha, ok := r.checkpointTip(ctx)
	if !ok || !r.ckpt.sched.due(r.d.Now(), sha, r.rec.PushedHead) {
		return
	}
	r.checkpointPush(ctx, stage, sha, true)
}

// checkpointPush pushes sha, the tip just read: every check is on sha,
// never on HEAD, which the agent may have moved since. during says it is a
// poll's push, mid-stage, rather than a boundary's.
func (r *run) checkpointPush(ctx context.Context, stage, sha string, during bool) {
	now := r.d.Now()
	// Of the tip read, not HEAD: a tip read just before the agent's first
	// commit is the base, which is never pushed.
	if ahead, err := r.repo.CountAhead(ctx, r.cfg.Git.BaseBranch, sha); err != nil || ahead == 0 {
		return // no commit of the run's own yet
	}
	if !r.checkpointClean(ctx, sha) {
		if !r.ckpt.stopped {
			r.ckpt.sched.failed(now)
		}
		return
	}
	err := r.pushCheckpoint(ctx, sha)
	switch {
	case err == nil:
	case errors.Is(err, gitops.ErrNotFastForward):
		r.ckpt.sched.failed(now)
		r.warnCheckpoint("rewritten", "the run branch's pushed commits were rewritten; checkpoints wait until a push fast-forwards again (a verified stage end or finalize pushes the branch as it is)", "sha", shortSHA(sha))
		return
	case r.asRefusal(err) != nil:
		r.stopCheckpoints("the host would refuse the push", "reason", refusalText(r.asRefusal(err)))
		return
	case ctx.Err() != nil:
		return // the stage ended mid-push; the boundary or finalize pushes
	default:
		r.ckpt.sched.failed(now)
		r.warnCheckpoint("push", "a checkpoint push failed; it is retried in a few minutes", "err", r.redact(err.Error()))
		return
	}
	r.ckpt.sched.pushed(now)
	r.ckpt.pushes++
	r.d.Log.Info("checkpoint pushed", "stage", stage, "sha", shortSHA(sha), "n", r.ckpt.pushes, "boundary", !during)
}

// checkpointClean scans the commits a checkpoint would add (after the last
// push, or after the work base before the first) for a value the run
// redacts. A hit stops checkpoints for the run: every later push would
// carry that commit. A binary or very large range is pushed, as finalize
// would push it.
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

In `internal/runner/prflow.go`, replace the first six lines of `pushBranch`'s body (the `actx` refresh and its warning) with `r.refreshForPush(ctx)` and add:

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

In `internal/runner/refused.go`, move the body of `workflowGuard` into a new function and keep `workflowGuard` as a wrapper (its doc comment stays):

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

Run: `go test -race ./internal/runner/ -run 'TestCheckpoint|TestBurstOfCommitsIsOnePush|TestAtMostOnePushPerMinute|TestFailedPushRetriedByTheFallback|TestIdlePollsNeverTouchTheRemote|TestStageEndStopsTheCheckpointer|TestNoCheckpointAfter|TestFollowUpDoesNotCheckpoint|TestRefused|TestOpensDraftAfterFirstVerifiedStage|TestEarlyDraftFalseKeepsFinalizeOnly|TestHaltAfterPushLeavesDraftWithComment|TestCancelFinalizesDraftWithNote'`
Expected: PASS (about 40 s). The existing tests in the list pin that a stage whose poll never fires leaves today's flows unchanged.

- [ ] **Step 5: Commit**

```bash
git add internal/runner/checkpoint.go internal/runner/checkpoint_test.go internal/runner/runner.go internal/runner/refused.go internal/runner/prflow.go internal/runner/export_test.go
git commit -m "runner: the checkpointer polls the branch tip and pushes new commits, fast-forward only"
```

- [ ] **Step 6: Own review** (token-economy rule: this task changes every run's stage loop). The reviewer checks four things:
  - nothing but the checkpoint goroutine writes `r.rec`, `r.pr`, `r.ckpt`, `r.env` or `r.auth` between `startCheckpoints` and its `stop`;
  - a poll whose tip is unchanged runs only local git;
  - every path in `checkpointTick` and `checkpointPush` returns without failing the run;
  - `stop` runs before any later write in `stage()`.

  The `-race` focused run above must be clean.

---

### Task 6: The boundary push and the draft at the first checkpoint

**Files:**
- Modify: `internal/runner/prflow.go`, `internal/runner/checkpoint.go`, `internal/runner/prflow_test.go`, `internal/runner/golden_test.go`
- Test: `internal/runner/checkpoint_pr_test.go` (new)

**Interfaces:**
- Consumes:
  - from Task 5: `checkpointPush`, `checkpointTip`, `checkpointBlocked`, `checkpointsOn`, `ckptRig`, `newCkptRig`, `commitWIP`, `pushedHead`, `countPushes`;
  - existing: `latestVerifiedTest`, `ensurePR`, `earlyPRText`, `noteStatusWritten`, `afterStep`, `probe`, `storedPR`, `onlyPR`, `ops`, `count`, `verifyTest`, `envValue`, `statusBegin`.
- Produces:
  ```go
  func (r *run) boundaryCheckpoint(ctx context.Context, stage string)
  func (r *run) afterCheckpoint(ctx context.Context, stage string, during bool)
  func (r *run) openDraftPR(ctx context.Context, stage string, during bool) // openDraft's PR half
  func (r *run) statusUpdate(ctx context.Context, stage string, during bool)    // was (ctx, stage)
  func (r *run) runningSection(stage string, during bool) string                // was (stage)
  func (r *run) pushedPart(records []verify.Record) string
  func pushedVerified(records []verify.Record, sha string) bool
  ```

- [ ] **Step 1: Write the failing tests**

Create `internal/runner/checkpoint_pr_test.go`:

```go
package runner_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// TestCommitThenStageEndPushedAtTheBoundary: a commit made just before the
// stage ends is pushed at the boundary, at once, even inside the minute.
func TestCommitThenStageEndPushedAtTheBoundary(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ", early_draft: false"))
	pushes := countPushes(t, g.harness)
	var last string
	work := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		commitWIP(t, req, "one")
		g.settle() // pushed: the minute's window starts now
		last = commitWIP(t, req, "two")
		return agent.Result{CostUSD: 1}, nil // the stage ends without another poll
	}
	var atReview string
	var n int
	rec, err := g.run(t, work, probe(func(t *testing.T) { atReview, n = pushedHead(t, g.harness), pushes() }, review("ship", 0)))
	if err != nil {
		t.Fatal(err)
	}
	if atReview != last || n != 2 {
		t.Fatalf("at the review stage: pushed %s after %d pushes, want %s after 2", atReview, n, last)
	}
	if rec.Outcome != runstore.OutcomeDraft || rec.PushedHead != rec.HeadSHA {
		t.Fatalf("rec = %+v", rec)
	}
}

// TestCheckpointOpensDraftMarkedNotVerified: the first checkpoint opens the
// draft, with no reviewers, its number saved, and a section that says the
// work is not verified; finalize makes it ready as before.
func TestCheckpointOpensDraftMarkedNotVerified(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ""))
	var body string
	var draft bool
	var reviewers []string
	var saved *runstore.PRRef
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		commitWIP(t, req, "wip")
		g.settle()
		prs := g.provider.Snapshot().PRs
		if len(prs) != 1 {
			t.Fatalf("PRs after the first checkpoint: %d", len(prs))
		}
		body, draft, reviewers = prs[0].Body, prs[0].Draft, prs[0].Reviewers
		saved = storedPR(t, g.harness)
		return implement("feature")(t, ctx, req)
	}
	rec, err := g.run(t, long, review("ship", 0))
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
	pr := onlyPR(t, g.provider)
	if pr.Draft || !strings.Contains(pr.Body, "**Ready for review**") || strings.Contains(pr.Body, "not verified") {
		t.Fatalf("final PR = %+v", pr)
	}
	if calls := ops(g.harness); calls[0] != "EnsurePR#0 draft=true" || count(calls, "EnsurePR", "") != 2 {
		t.Fatalf("calls = %q", calls)
	}
}

// TestStatusSaysVerifiedAfterAPassingTest: once the pushed commit has a
// passing clean test, the next status write says verified.
func TestStatusSaysVerifiedAfterAPassingTest(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ""))
	var head string
	work := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		head = commitWIP(t, req, "wip")
		g.settle()
		verifyTest(t, ctx, req)
		pr := filepath.Join(envValue(req.Env, "FUGARO_STATE_DIR"), "pr.md")
		if err := os.WriteFile(pr, []byte("# Add wip\n\nAdds wip.txt."), 0o644); err != nil {
			t.Fatal(err)
		}
		return agent.Result{CostUSD: 1}, nil
	}
	var body string
	if _, err := g.run(t, afterStep(g.clock, work), probe(func(t *testing.T) { body = g.provider.Snapshot().PRs[0].Body }, review("ship", 0))); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "**Running** ·") || !strings.Contains(body, fmt.Sprintf("branch at `%s`: verified", head[:7])) || strings.Contains(body, "not verified") {
		t.Fatalf("body at the review stage:\n%s", body)
	}
}

// TestEarlyDraftFalseCheckpointsWithoutPR: early_draft false still pushes
// the branch mid-stage, and the PR opens only at finalize.
func TestEarlyDraftFalseCheckpointsWithoutPR(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ", early_draft: false"))
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		head := commitWIP(t, req, "wip")
		g.settle()
		if pushedHead(t, g.harness) != head || len(g.provider.Snapshot().PRs) != 0 {
			t.Errorf("want the branch pushed and no PR mid-run")
		}
		return implement("feature")(t, ctx, req)
	}
	rec, err := g.run(t, long, review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady || onlyPR(t, g.provider).Draft {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

// TestCheckpointOpenTriedTwice: a draft that fails to open is retried by
// the next checkpoint push only, twice in all; the verified boundary
// opens it after that.
func TestCheckpointOpenTriedTwice(t *testing.T) {
	g := newCkptRig(t, prCfg(t, 2, ""))
	g.provider.FailEnsure = 6 // two tries of ensurePR's three attempts
	ensures := func() int { return count(snapOps(g.harness), "EnsurePR", "") }
	long := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		for i, name := range []string{"one", "two", "three"} {
			head := commitWIP(t, req, name)
			g.settle()
			if pushedHead(t, g.harness) != head {
				t.Fatalf("commit %s was not pushed", name)
			}
			if want := 3 * min(i+1, 2); ensures() != want {
				t.Fatalf("after push %d: %d EnsurePR calls, want %d", i+1, ensures(), want)
			}
		}
		return implement("feature")(t, ctx, req)
	}
	rec, err := g.run(t, long, review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeReady || len(g.provider.State.PRs) != 1 {
		t.Fatalf("rec = %+v, err = %v, PRs = %+v", rec, err, g.provider.State.PRs)
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

Five existing tests pin 0.5.0's pushes. The full runner suite on the prototype showed exactly these five failing once boundary pushes exist; nothing else did. The 0.5.0 rules now hold only with checkpoints off, so these tests turn them off, and their names stay true:
- In `internal/runner/prflow_test.go`:
  - `TestNoPushWithoutVerifiedTest` (an unverified boundary pushes nothing): `prHarness(t, prCfg(t, 2, ""))` becomes `prHarness(t, prCfg(t, 2, ", checkpoints: false"))`, and its doc comment gains `With checkpoints off (an unverified boundary pushes since 0.5.1).`
  - `TestFirstRoundFailsThenFixOpensDraft`: `prHarness(t, prCfg(t, 3, ""))` becomes `prHarness(t, prCfg(t, 3, ", checkpoints: false"))`, with the same sentence.
  - `TestEarlyDraftFalseKeepsFinalizeOnly` ("nothing is pushed before finalize"): `prCfg(t, 2, ", early_draft: false")` becomes `prCfg(t, 2, ", early_draft: false, checkpoints: false")`. The new `TestEarlyDraftFalseCheckpointsWithoutPR` covers `early_draft: false` with checkpoints on.
- In `internal/runner/golden_test.go`, `oldFlow` writes `pr: { early_draft: false, checkpoints: false }` instead of `pr: { early_draft: false }`. Its comment, and `oldFlowCfg`'s, say that the two keys restore the finalize-only flow the goldens were recorded with. This fixes `TestTokenRefreshedBetweenStages` and `TestTokenValidityCappedBelowTokenLife` (`provider_test.go`). They count `GitAuth` calls, and a boundary push refreshes the token once more. The goldens are unchanged.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -race ./internal/runner/ -run 'TestCommitThenStageEndPushedAtTheBoundary|TestCheckpointOpensDraftMarkedNotVerified|TestStatusSaysVerifiedAfterAPassingTest|TestEarlyDraftFalseCheckpointsWithoutPR|TestCheckpointOpenTriedTwice'`
Expected: FAIL. Each failure shows a missing behaviour:
- `TestCommitThenStageEndPushedAtTheBoundary`: `at the review stage: pushed <sha one> after 1 pushes` (no boundary push);
- `TestCheckpointOpensDraftMarkedNotVerified`: `PRs after the first checkpoint: 0`;
- `TestStatusSaysVerifiedAfterAPassingTest`: an index-out-of-range panic in its probe (no PR);
- `TestCheckpointOpenTriedTwice`: `after push 1: 0 EnsurePR calls, want 3`.

`TestEarlyDraftFalseCheckpointsWithoutPR` already passes: it pins that this task keeps `early_draft: false` PR-less.

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

Still in `prflow.go`, `afterStage`'s first-run branch becomes (the boundary push goes after the verified push, before the early return of `early_draft: false`):

```go
	if r.follow == nil {
		if r.cfg.Git.PR.EarlyDraftOn() && (stage == "implement" || stage == "fix") {
			if sha, ok := r.verifiedHead(ctx); ok {
				if r.rec.PR == nil {
					r.openDraft(ctx, sha, stage)
					return
				}
				r.pushVerified(ctx, sha)
			}
		}
		// What the stage left committed and still unpushed goes now,
		// fast-forward only: an unverified tip (a verified one was pushed
		// just above). It may open the draft.
		r.boundaryCheckpoint(ctx, stage)
		if !r.cfg.Git.PR.EarlyDraftOn() {
			return
		}
	}
	r.statusUpdate(ctx, stage, false)
```

In `internal/runner/checkpoint.go`, end `checkpointPush` (after the `checkpoint pushed` log line) with `r.afterCheckpoint(ctx, stage, during)`, and add:

```go
// boundaryCheckpoint pushes what a stage left committed and unpushed, at
// once, on the run goroutine after the stage's checkpointer stopped: the
// agent is idle, so there is nothing to wait for, and a boundary is exempt
// from the per-minute limit (a run has a handful of them).
func (r *run) boundaryCheckpoint(ctx context.Context, stage string) {
	if !r.checkpointsOn() || r.checkpointBlocked(ctx) {
		return
	}
	sha, ok := r.checkpointTip(ctx)
	if !ok || sha == r.rec.PushedHead {
		return
	}
	r.checkpointPush(ctx, stage, sha, false)
}


// afterCheckpoint opens the draft at the first checkpoint push, or brings
// an open draft's status section up to date. With early_draft false the
// branch is all a checkpoint pushes: the PR opens at finalize.
func (r *run) afterCheckpoint(ctx context.Context, stage string, during bool) {
	if !r.cfg.Git.PR.EarlyDraftOn() || r.pr.gone {
		return
	}
	if r.rec.PR == nil {
		if r.ckpt.openTries >= checkpointOpenTries {
			return // left to the verified boundary or finalize
		}
		r.ckpt.openTries++
		r.openDraftPR(ctx, stage, during)
		return
	}
	r.statusUpdate(ctx, stage, during)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/runner/ -run 'TestCheckpoint|TestCommitThenStageEnd|TestStatusSays|TestEarlyDraft|TestBurst|TestAtMostOne|TestFailedPushRetried|TestIdlePolls|TestStageEndStops|TestNoCheckpointAfter|TestFollowUp|TestOpensDraftAfterFirstVerifiedStage|TestNoPushWithoutVerifiedTest|TestFirstRoundFailsThenFixOpensDraft|TestStatus|TestCoalesces|TestThreeFailuresStopUpdates|TestHumanEditOutsideMarkersKept|TestDraftFallbackNotReadyLooking|TestRefused|TestTokenRefreshedBetweenStages|TestTokenValidityCappedBelowTokenLife|TestNoPolicyIsM9aBehaviour'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/runner/prflow.go internal/runner/checkpoint.go internal/runner/checkpoint_pr_test.go internal/runner/prflow_test.go internal/runner/golden_test.go
git commit -m "runner: push at every stage boundary; the draft opens at the first checkpoint, marked not verified"
```

---

### Task 7: The agent is told to commit early

**Files:**
- Modify: `internal/runner/prompts.go`, `internal/runner/runner.go` (`agentLoop`)
- Test: `internal/runner/pure_test.go`

**Interfaces:**
- Consumes: `PromptData`, `SystemPrompt`, `checkpointsOn`.
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
	for _, want := range []string{"Commit early and often", "fugaro/x within about a minute", "only pushed commits survive", "uncommitted changes are lost", "rather than amending or rebasing"} {
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

In `internal/runner/prompts.go`, add to `PromptData`:

```go
	// Checkpoints adds the rule to commit early: the runner pushes new
	// commits while the stage runs (first runs with git.pr.checkpoints on).
	Checkpoints bool
```

and in `SystemPrompt`, after the `NoWorkflows` block:

```go
	if d.Checkpoints {
		lines = append(lines, fmt.Sprintf("- Commit early and often, after every step that works: Fugaro pushes each new commit to %s within about a minute, and if this container dies only pushed commits survive; uncommitted changes are lost. Add new commits rather than amending or rebasing commits you already made: rewritten commits are not pushed until the run ends.", d.Branch))
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

### Task 8: Docs, pinned by a test

**Files:**
- Create: `internal/runner/docs_checkpoint_internal_test.go`
- Modify: `docs/git-providers.md`, `docs/design/v1.md`, `docs/gcp-setup.md`, `README.md`, `plugin/skills/working/SKILL.md`, `plugin/skills/working/reference/launch.md`, `plugin/skills/working/reference/diagnose.md`

**Interfaces:**
- Consumes: `checkpointPoll`, `checkpointQuiet`, `checkpointMinGap`, `checkpointFallback`.
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

// TestCheckpointDocsMatchTheCode: the docs state the timings the code uses
// and the cost model, name the opt-out, and no longer say the draft waits
// for a verified push.
func TestCheckpointDocsMatchTheCode(t *testing.T) {
	read := func(p string) string {
		t.Helper()
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	if checkpointMinGap != time.Minute {
		t.Fatal("the docs say at most one push a minute (60 an hour); update them with checkpointMinGap")
	}
	gp := read("../../docs/git-providers.md")
	for _, want := range []string{
		"## Checkpoint pushes (`git.pr.checkpoints`)",
		fmt.Sprintf("every %d seconds", int(checkpointPoll/time.Second)),
		fmt.Sprintf("unchanged for %d seconds", int(checkpointQuiet/time.Second)),
		fmt.Sprintf("%d minutes", int(checkpointFallback/time.Minute)),
		"at most one push a minute", "60 pushes an hour", "every stage boundary", "post-commit hook",
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
Expected: FAIL: `docs/git-providers.md never says "## Checkpoint pushes (`git.pr.checkpoints`)"` (and the other strings), and `… still says the draft opens at the first verified push` for each listed file.

- [ ] **Step 3: Write the docs**

`docs/git-providers.md`:
- In "Early draft PRs", replace the first paragraph (line 78) with:

  > A run opens a **draft** pull request at its first checkpoint push (see "Checkpoint pushes" below: its first commit, within about a minute), not when it finishes. It keeps a **Fugaro status** section in the description current: the stage, the verify state, the commit the branch holds and whether it is verified, the model cost and the update time. Until a passing test on a clean tree covers the pushed commit, the section says `Running: work in progress, not verified`. The PR is marked ready at the end only when it is (design §4.2a). With `git.pr.checkpoints: false` the draft opens at the first verified stage end, as in 0.5.0.

- Insert a new section before `## Pushing and draft pull requests`:

  ```markdown
  ## Checkpoint pushes (`git.pr.checkpoints`)

  Fugaro saves the agent's work as soon as it is committed, so a container that dies (out of memory, out of disk, a killed execution) keeps all of it. While a run's stage runs, the runner reads the run branch every 10 seconds, locally (no network call). When a new commit has stayed unchanged for 5 seconds (so a burst of commits is one push of the latest), it pushes the branch, at most one push a minute: a commit inside that minute is pushed when it ends. At every stage boundary (the end of an implement, fix or review stage) it pushes what is left at once. A push that fails is retried 3 minutes later, and a branch that never stays still for 5 seconds is pushed after 3 minutes anyway. The runner does this itself rather than through a git post-commit hook, which the agent could disable or bypass and which would run with the agent's environment instead of the runner's credentials. The push is a **fast-forward only**: it never forces and never replaces a commit already on the branch. It uses the runner's credentials, never commits for the agent, and never touches uncommitted files: uncommitted work is still lost with the container, so the agent is told to commit early. A checkpoint is guarded like the final push: on GitHub, commits that change `.github/workflows/` are not pushed, and commits holding a value the run redacts (a secret it knows) stop checkpoints for the run. A push that fails never fails the run. If the agent rewrites commits already pushed (amend, rebase), checkpoints pause until a push fast-forwards again; the end of a verified stage and the final push put the branch right as before. Follow-up runs (`fugaro run --pr N`) do not checkpoint: their pull request may already be ready.

  With `git.pr.early_draft` on (the default), the first checkpoint opens the draft PR, marked not verified. It becomes ready only at the end, by the same rule as before. With `early_draft: false`, checkpoints push the branch and the PR still opens only at the end.

  **CI cost.** Every checkpoint push to a branch with an open pull request runs the repository's PR CI, and `on: push` workflows (or Bitbucket branch pipelines) run on any push. The worst case, an agent committing without pause, is 60 pushes an hour (one a minute) plus one per stage boundary; an agent that commits after each working step pushes a few times per stage, and an idle one never. To keep that down, skip drafts in CI (on GitHub, `if: github.event.pull_request.draft == false` on the expensive jobs) and cancel superseded runs (`concurrency: { group: ${{ github.ref }}, cancel-in-progress: true }`), or turn checkpoints off with `git.pr.checkpoints: false`: the branch is then pushed only at a verified stage end and at the end, as in 0.5.0. The key needs Fugaro 0.5.1 everywhere (older CLIs and job images refuse it as unknown); the default needs no key.

  **When a container dies anyway.** On Cloud Run the container's disk is memory: `node_modules`, build output and caches count against `workflows.<name>.resources.memory`. A run killed by a signal (signal 7 or 9) during repeated installs or builds usually needs a larger `resources.memory` (or fewer repeated installs), not a code change. Its draft PR holds the work up to the last checkpoint; continue it with `fugaro run --pr N`.
  ```

`docs/design/v1.md`:
- §4.2a's first paragraph (line 275), replace with:

  > Finalize's PR (§4.1) stays the guarantee that every run with a branch ends in a PR. M9e added a **draft PR** so people can watch the work. Since 0.5.1 it opens at the run's **first checkpoint push** ([checkpoint-pushes.md](checkpoint-pushes.md)), marked not verified, which supersedes M9e ruling E1 that the remote branch is always a verified state. Rulings and open assumptions: [plan](../plans/2026-10-03-m9e-early-draft-pr.md). `git.pr.early_draft: false` (default `true`) turns the early PR off per repository: the PR then opens at finalize only. Checkpoints still push the branch unless `git.pr.checkpoints: false`, which restores 0.5.0's pushes.

- The **When.** bullet (line 277), replace with:

  > - **When.** While a first run's stage runs, the runner polls the branch tip every 10 s (locally) and pushes a new commit once it has been still for 5 s, at most once a minute, and at once at every stage boundary, fast-forward only (saving `pushed_head`). The first such push, with no PR recorded, opens a **draft** PR with no reviewers and no labels, its section headed `Running: work in progress, not verified` until a passing `test` with `clean_tree` covers the pushed commit. At the end of an implement or fix stage that succeeded, a verified HEAD is pushed as before (and opens the draft if no checkpoint did). A run with no commits opens nothing until finalize; a halt before the branch exists opens no PR (D9). The open is bounded (60 s) and tried at most twice by checkpoints; if it fails, the verified boundary or finalize opens the PR.

`docs/gcp-setup.md` line 57: replace `The draft appears at the first verified push and shows the run's progress;` with `The draft appears at the run's first checkpoint push (within about a minute of its first commit), marked not verified, and shows the run's progress ([git-providers.md](git-providers.md#checkpoint-pushes-gitprcheckpoints));`.

`README.md` line 57: replace `a draft at the first verified push, marked ready only when the run passes.` with `each commit is pushed within about a minute while the agent works, a draft opens at the first push (marked not verified), and it is marked ready only when the run passes.`

`plugin/skills/working/SKILL.md` line 51: replace `A draft appears early, at the first verified push, with a status section in its description that the run keeps updating.` with `A draft appears early, at the run's first checkpoint push (its first commit, within about a minute), with a status section in its description that the run keeps updating and that says "not verified" until a passing test covers the pushed commit.`

`plugin/skills/working/reference/launch.md` line 6: replace `A draft pull request appears early, at the run's first verified push, and shows the run's progress in its description;` with `A draft pull request appears early, at the run's first checkpoint push (each commit is pushed within about a minute), marked not verified, and shows the run's progress in its description;`.

`plugin/skills/working/reference/diagnose.md`:
- line 41: replace `at the run's first verified push, while the run is still going,` with `at the run's first checkpoint push (each commit is pushed within about a minute, unverified), while the run is still going,`;
- line 43: replace `The work up to the last verified push is on the branch;` with `The work the agent committed up to the last checkpoint (about a minute before it died) is on the branch, unverified, and uncommitted work is lost;`.

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

### Task 9: The full suite and the PR

- [ ] **Step 1:** Run `gofmt -l . && go vet ./... && go test -race ./internal/gitops/ ./internal/config/ ./schemas/ ./internal/gitprov/... ./plugin/`. Expected: no gofmt output, and PASS.
- [ ] **Step 2:** Run the **one** full runner suite for this PR group: `go test -race -timeout 40m ./internal/runner/...` (about 19 minutes). Expected: PASS. A failure in an existing test that reads `h.provider.State` during a step means the read needs `h.provider.Snapshot()`; fix it in the test, not by slowing checkpoints.
- [ ] **Step 3:** Run `go test ./...` for the rest (`internal/cli` takes about 6 minutes). Expected: PASS.
- [ ] **Step 4:** Push the branch and open the PR. The body lists decisions C1 to C11 (with C1a, C1b and C6a), one line each, and names the M9e ruling E1 that is superseded.
- [ ] **Step 5:** Read every CI check (`test`, `terraform`, `rules`), each job's log and not only the summary, before asking for the merge.

---

### Task 10: Release 0.5.1 (after the merge)

- [ ] **Step 1:** Through a PR to `main`, add to `docs/releases/v0.5.1.md`. Create the file if `fugaro image refresh`'s Task 12 has not created it yet, and keep its entries if it has:

```markdown
- **Checkpoint pushes.** A run now pushes each commit the agent makes within about a minute (after 5 seconds without a newer one, at most one push a minute, and at once at every stage boundary; fast-forward only, never forced), so a container that dies mid-stage keeps the work the agent committed. The draft pull request opens at the first such push, marked "not verified" until a passing test covers the pushed commit; readiness and reviewers are unchanged. Every push to a branch with an open pull request runs its CI (worst case 60 an hour): see `docs/git-providers.md` for skipping drafts in CI, or set `git.pr.checkpoints: false` (needs 0.5.1 everywhere). Rebuild the job images (`fugaro image refresh`) for runs to get it.
```

- [ ] **Step 2:** `/new-release 0.5.1`.
- [ ] **Step 3, the user's live check on the Bitbucket sandbox only** (`edgeappinc/fugarosandbox`; never EdgeWeb or EdgeServer), with the user's go-ahead and task text, after `fugaro image refresh` there:
  1. A run whose task takes several minutes. The branch appears on the host within about a minute of the agent's first commit, before the first stage ends, and the draft's section says `not verified`, then `verified`.
  2. `fugaro cancel --now` on a second such run after its first checkpoint. The branch holds the committed work, and `fugaro diagnose` shows the stale draft.

  This also confirms that Bitbucket accepts a push to the origin URL by SHA (C4).
