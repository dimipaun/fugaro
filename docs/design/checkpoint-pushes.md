# Checkpoint pushes

Status: direction approved by the user (2026-10-07); the schedule was revised on review (2026-10-08) so that pushes follow commits. The details below come from the code, and each one is a decision the user can veto in the plan ([plans/2026-10-08-checkpoint-pushes.md](../plans/2026-10-08-checkpoint-pushes.md)). Delivery: release **0.5.1**.

## Problem

In a real run on another repository, the agent finished the feature and committed it locally, all inside one long `implement` stage. It then spent about 20 minutes on two `fugaro verify build` runs and a second `yarn install`, and the container was killed (signal 7, probably memory or disk). The run ended `infra_error` with outcome `none`. Nothing was pushed and no PR was opened, so the work was lost.

Today the runner pushes in two places only:

- **at a stage boundary**, when HEAD has a passing test on a clean tree (`afterStage`, `verifiedHead`, `openDraft`, `pushVerified` in `internal/runner/prflow.go`);
- **at finalize** (`finalize`, `ensurePR`, `settlePR` in `runner.go`).

The M9e rule E1 ("the remote branch is always a verified state") made the push wait for verification, so a stage that dies before its end pushes nothing. Committed work is lost even though it was safe to keep.

## Behaviour

1. **Checkpoint: pushes follow commits.** Work is saved as soon as the agent commits it. While a first run's stage runs, a goroutine started next to the stage deadline and halt watcher (`stage()` in `runner.go`) reads the run branch's tip every **10 s** (`checkpointPoll`). The read is local, a few git commands with no network call, so an idle agent costs nothing. The tip it read is pushed when all of these hold:
   - HEAD is on the run's own branch `fugaro/<run id>`, and no merge, rebase, cherry-pick, revert or bisect is in progress;
   - the tip is not the commit already pushed (`pushed_head`), and the branch has commits ahead of the base;
   - the tip has stayed unchanged for **5 s** (`checkpointQuiet`), so a burst of commits is one push of the latest;
   - no push happened in the last **minute** (`checkpointMinGap`). A commit inside the window is pushed at the first poll after it ends.

   Two slow paths use **3 minutes** (`checkpointFallback`): a failed push is retried that long after the failure, and a tip that never stays still for 5 s is pushed that long after the oldest unpushed commit appeared.

   **Every stage boundary** pushes at once. After `implement`, `fix`, `review` and `review_first`, `afterStage` pushes whatever the stage left committed and unpushed. This push skips the quiet period and the minute: the agent is idle, the stage's work is complete, and a run has only a handful of boundaries. The worst case is therefore **60 pushes an hour** (one a minute) plus one per boundary.

   The push is **fast-forward only** and is never forced. It uses the runner's own credentials (`refreshGitAuth`). It pushes the tip's SHA, and it pushes to origin's URL so that no local ref is written. It never commits, never reads or changes the working tree, and never takes `index.lock`.

   **Not a git hook.** A `post-commit` hook would fire exactly at each commit, but the agent owns the checkout:
   - it can disable or skip hooks (`core.hooksPath`, removing `.git/hooks`, plumbing commits through `commit-tree` and `update-ref`);
   - a hook runs in the agent's process tree, with the agent's environment rather than the runner's credentials and guards.

   Polling from the runner sees every commit, however it was made.
2. **The draft opens at the first checkpoint**, not after verification. Its status section says `**Running: work in progress, not verified**` and `branch at <sha>: not verified`. Readiness does not change: only finalize makes the PR ready, by §4.2's rule (a passing verified test on the final commit and a `ship` verdict). Reviewers and labels are still added only at ready.
3. **The section says verified** once the pushed commit has a passing test on a clean tree. Two things rewrite the section: the next checkpoint push (`checkpoint during stage <name>`) and the next stage boundary. Boundary updates are unchanged: they still happen at most once per 20 seconds, and three failures in a row stop them.
4. **The agent is told** in its system prompt to commit early and often, because only pushed commits survive a dead container. It is also told to add new commits rather than amend or rebase, because rewritten commits are not checkpointed.
5. **`git.pr.checkpoints`** turns checkpoints on or off (default `true`). `git.pr.early_draft: false` still means "the PR opens at finalize". With it, checkpoints push the branch and open no PR. `git.pr.checkpoints: false` restores 0.5.0's pushes exactly.

## Safety

- **Never less safe than finalize's push.** A checkpoint goes through the same guards as finalize:
  - the workflow-file guard (`workflowGuard`, checked on the SHA being pushed);
  - the host's permanent refusals (`asRefusal`). On a refusal, checkpoints stop for the run and finalize ends the run as it does today (`endRefused`, with the work bundle).
- **One check finalize lacks.** A checkpoint pushes intermediate commits that a later rewrite could remove before finalize, so it first scans the new commits for any value the run redacts (`ScanRange`, the same scan `saveWork` uses). If it finds one, checkpoints stop for the run. A binary or oversized range is pushed, as finalize would push it.
- **The agent's own git.** Each poll reads the branch tip once and checks that SHA throughout: the commits ahead, the secret scan, the workflow guard and the push. It never re-reads HEAD, because the agent commits concurrently. Reading the branch tip while the agent rebases or resets is safe: a poll during a rebase or a merge sees `ErrGitBusy` and waits for the next poll. A history the agent rewrote after a checkpoint is not a fast-forward, so it is refused. The run logs one warning and retries at the fallback: checkpoints resume once the tip descends from the pushed commit again. The verified-boundary push and finalize keep their existing lease push (`Push`: force-with-lease, only over a tip that is the run's own), so they reconcile the branch as they do today. A checkpoint never overwrites anything.
- **A checkpoint never fails the run.** A failed push or PR call is a warning, and the push is retried 3 minutes later or at the next boundary. A panic is recovered inside the goroutine. Opening the draft is tried at most twice, once per checkpoint push; after that it is left to the verified boundary or to finalize.
- **Checkpoints stop before finalize.** The goroutine lives from the agent's start to its return, on the stage's context: it stops at the stage's deadline, which never passes `finalize_reserve`, and at a cancel. When the stage ends it is cancelled and joined. The boundary push then runs on the run goroutine, and nothing checkpoints during finalize. A poll does nothing once a halt or a cancel is recorded or the time budget is spent. While the stage runs, the goroutine is the only writer of the run record, so no lock is needed.
- **Follow-ups do not checkpoint** in 0.5.1. Their branch is an open PR that may be ready and have reviewers, and finalize's `PushExisting` would treat the run's own checkpoint as someone else's push.
- **Providers.** GitHub and Bitbucket both have real drafts (Bitbucket's was verified live on 2026-09-27). On a GitHub plan that refuses drafts, the early PR is a normal one titled `[DRAFT] …`, as today, and it may auto-request CODEOWNERS reviewers. The fix stays `git.pr.early_draft: false`.
- **API cost.** A poll with nothing new costs no network call. A push costs one `ls-remote`, one push and, with a PR, one read and one write of the description. At the worst case of 60 pushes an hour plus boundaries, that is about 180 provider requests an hour, and creating the PR is one call. This is far below GitHub's 5,000 requests an hour per installation.
- **CI cost.** Every checkpoint push to a branch with an open PR runs the repository's CI on that PR. `on: push` workflows run on it even without a PR, as do Bitbucket branch pipelines. The cost model, stated plainly in the docs:
  - worst case, an agent committing without pause, **60 pushes an hour** (one a minute) plus one per stage boundary, and so up to 60 CI runs an hour;
  - an agent that commits after each working step pushes a few times per stage;
  - an idle agent pushes nothing.

  The docs show how to skip drafts or cancel superseded runs in CI, and `git.pr.checkpoints: false` turns checkpoints off.

**Limitation.** The checkpoint workflow guard reads a wider range than finalize's: it starts at the frozen base commit (or the last pushed one), finalize at `origin/<base>`. If the agent rebases onto a newer base that brought someone else's `.github/workflows` change, checkpoints stop for the whole run although the host would accept the push. It fails closed; finalize still pushes.

## Not doing (future work)

- Snapshots of uncommitted work to a hidden ref (`refs/fugaro/wip/<run id>`).
- Checkpoints for follow-ups (a side branch `fugaro/<follow-up id>` is the candidate).
- Configurable timings (poll, quiet period, minute, fallback).
- A pre-push secret scan in finalize itself (today finalize pushes whatever the agent committed, and only the work bundle is scanned).

**Resources.** The signal 7 in the incident is a resources problem, not a code one. On Cloud Run the container's disk is memory, so `node_modules` and build output count against `workflows.<name>.resources.memory`. Repeated `yarn install` or build runs need a larger memory setting. The docs say so next to the checkpoint section.

## Decisions (summary; the plan has the veto list)

- Pushes follow commits: a 10 s local poll, a 5 s quiet period, at most one push a minute, and a 3-minute fallback for failures. Every stage boundary pushes at once, exempt from the minute. Checkpoints are on by default, fast-forward only, and use no git hook.
- The draft opens at the first checkpoint push and is marked not verified.
- Checkpoints are separate from `early_draft`, with their own opt-out.
- Follow-ups are excluded.
- A known secret stops checkpoints.
- `result.json` gets no new field: `pushed_head` already records the last push.

## Delivery

Release **0.5.1** (patch), together with `fugaro image refresh`. The plan has 10 tasks: 9 for the PR and the release after it. The runner change reaches a repository only when its job image is rebuilt from 0.5.1, with `fugaro image refresh` or `fugaro init --repo`. The new key `git.pr.checkpoints` is refused as unknown by older CLIs and job images, so set it only after everyone who works on the repository is on 0.5.1. The default needs no key.
