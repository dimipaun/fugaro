# Fast `runs ls`: reconciliation

Checked against `origin/main` at `b8858034b` on 2026-11-19. **Where this document disagrees with [spec.md](spec.md), this document wins.**
Decisions **D2960–D2967** are taken as recommendations (the owner approved the shape in outline and asked for rows, not questions); **D2968–D2969** are reserved. **Check 760** is taken; **761–769** are reserved.

## Assumptions

- **A1.** The owner's outline stands: RL1 live set, RL2 index, RL3 `--pr` through it, RL4 optional summary, RL5 docs and Check. RL2 is split into three tasks (RL2, RL2W, RL2B) and RL5 into two (RL5, RL6) so each stays near five files.
- **A2.** Same rows, same columns, same `--json`: this is a speed change, so no format moves.
- **A3.** The times in the spec are the baseline; the target is stated per query in Check 760 and is a reading, never a CI assertion.

## What the repository already has

| Need | What exists |
| --- | --- |
| The window listing | `runstore.ListRunIDs` (`internal/runstore/list.go`): a delimiter listing of `runs/<slug>/` from the run id's time prefix, so its cost follows the window |
| The row loader | `loadRows` (`internal/cli/runs.go`) with `lsFilter`, `readRunVia`, `followExecution`; `--pr` already skips the execution join for runs off the pull request (`mayBeOnPR`, `taskOnOtherPR`) |
| The cache | `internal/runcache`: lists every object of the repository to compare generations, then reads only what changed |
| The live notion | the PR-state fold in `loadRows` (the review row going on a pull request makes it live) and `internal/runview` |
| A launch claim | `runstore.Store.Claim`: `runs/<slug>/<run>/launching`, present between `task.json` and the launch |
| Who writes where | the job account writes `runs/<slug>/` only (a project job account every slug); a launcher writes `runs/`, `tokens/`, `issues/` ([bucket-iam §2.1, §3](../bucket-iam/spec.md)); the runs bucket ages `runs/` out at 90 days, the same window as `prLookback` |

## Findings

- **F1 (Gap, resolution).** `--state running` reads 20 s for 0 rows because the live set is tiny and the scan is not. The backend already lists executions in one call, so no new store is needed (D2960).
- **F2 (Gap, resolution).** A `launching` run has no execution yet, so the backend list cannot see it. Its claim object is the only evidence; it is found by listing only the recent window the claim's staleness allows, never the whole history.
- **F3 (Gap → D2962).** A new top-level `index/` prefix (the outline's `index/<slug>/pr-<N>.json`) would need a job-account write grant, a Terraform and IAM change on a security path, and a single shared file per pull request that any run on the slug could overwrite whole. One small object per run under the job's existing prefix needs no IAM change and cannot be overwritten as a unit.
- **F4 (Gap → D2963).** The job account is not trusted (`SECURITY.md`): an agent can write or delete objects under its slug. The index is therefore a hint, never a source of rows: `--pr N` reads each named run's own records and shows it only if they say it is on N. A forged entry adds a read, never a row; an erased entry hides a run, which D2964 bounds.
- **F5 (Gap → D2964).** An index written by new runners is silent about runs that ran before it existed, and about runs launched by an older job image still in flight at `fugaro update`. Empty index: scan, as today. Non-empty index: union with a scan of the last 24 hours (3 s measured), so a missing recent entry cannot hide a run.
- **F6 (Rule → D2965).** Existing pull requests need entries. `fugaro update` writes them, once, in place (CLAUDE.md's hard rule), and a run in flight is neither waited for nor rewritten: the step only creates absent objects.
- **F7 (Gap).** `runcache` lists every object of a repository, so index objects under `runs/<slug>/` join that listing, and any reader that walks `runs/<slug>/` must skip the `pr-index/` directory (it is not a run id; `ListRunIDs` already drops what `RunTime` rejects). RL2 greps every reader of the prefix and pins the skip.
- **F8 (Rule).** A field never changes meaning: the index and any summary object are new formats with a `version`, add-only fields, strict decode and a schema under `schemas/`.
- **F9 (Rule).** No prompt, no wait: the backfill and the fallback never ask, and a slow fallback prints a one-line note on stderr (`--json` stays clean).

## Decisions

| # | Question | Recommendation | Answer |
| --- | --- | --- | --- |
| D2960 | Where do `--state running` and `launching` read (F1, F2)? | The backend's non-terminal executions plus the `launching` claims of the claim window; no new store | **Backend list plus claims**, as recommended (2026-11-19, outline) |
| D2961 | Index store: object or Realtime Database? | Object in the runs bucket: it ages with the runs, needs no new service and no rules change | **Object**, as recommended |
| D2962 | Layout and writer (F3)? | One object per run: `runs/<slug>/pr-index/<N>/<run-id>.json`, written by the runner for its own run when it opens or follows the pull request, and by the launching CLI for `run --pr N`. No IAM change. This replaces the outline's `index/<slug>/pr-<N>.json`, which needs a grant and is one overwritable file | **Per-run object under `runs/<slug>/pr-index/`**, as recommended; recorded as the one change to the outline |
| D2963 | How far is the index trusted (F4)? | A hint only: every listed run's own records are read and checked against N | **Hint, verified per run**, as recommended |
| D2964 | A missing or partial index (F5)? | Empty: today's scan. Non-empty: union with a 24 h scan. No new flag; `--no-cache` still reads objects from the bucket and still uses the index | **As recommended**; an entry erased by a hostile run on a shared job is accepted as a residual and stated in `bucket-iam.md` §10 by RL5 |
| D2965 | Backfill (F6)? | A `fugaro update` step: for each repository's runs of the last 90 days that name a pull request, create the entry if absent; idempotent; one compact line; convergence test from the previous version | **In `update`**, as recommended |
| D2966 | The per-run summary (RL4)? | Build only if, after RL1–RL3, `runs ls --since 7d` on Check 760 reads over 8 s | **Gated on the reading**, as recommended |
| D2967 | The live notion in the PR-state fold? | The fold reads the live set (D2960) for the live word instead of every record | **Yes**, as recommended |

## Open items

- **What RL4's follow-up left (2026-11-24).** Two concrete things #3672's status named, and neither
  is filed: no backlog item was written (the backlog is a node `fugaro backlog add` writes, which
  this work did not run).
  - **Its own body is one clause behind its code.** Since `fb5644fb3` the re-read falls back in
    **two** cases — a summary that has gone or cannot be vouched for, and one that still says
    `running` while the execution has ended — where the section "Where it is read, and why" names
    one. The correction belongs in that pull request's text.
  - **The cost of the summary write at each stage end is unmeasured.** One small object beside a
    record write that already happens; no reading of it was taken here.

## UNVERIFIED

- **U1.** The claim window: how old a `launching` claim can be before `runview` treats it as abandoned. RL1 reads it from `internal/runview` and uses that bound; if the constant is not exported RL1 exports it.
- **U2.** Whether the backend's non-terminal listing is one call for a shared project job (several jobs). RL1 pins the call count with the fake backend and reports the real count in Check 760.
