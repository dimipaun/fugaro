# Fast `runs ls`: implementation plan

This plan builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md).

**Status: plan only. Nothing is implemented and no task is approved.** Each task needs the owner's explicit go before it is launched. The shape was agreed in outline on 2026-11-19; the decisions D2960–D2967 are recommendations awaiting the owner's confirmation. Each task has a ready brief under [briefs/](briefs/), whose first line is its `# Task:` title.

## Conventions every task follows

- `CLAUDE.md`'s commands, hermetic tests (`internal/gcpfake`, `internal/testutil`, `t.TempDir()`), no secret-shaped literal, comments that say why, targeted test runs (`-run`), `gofmt -l .` printing nothing, `git add <paths>` never `-A`.
- **Same rows.** A faster path must return the rows the scan returns: every task that adds a path pins it with an equivalence test on a mixed fixture, run against the fake bucket and the fake backend.
- **A field never changes meaning.** The index (and the summary, if built) are new formats with a `version`, add-only fields, strict decode and a schema in `schemas/`; `runs ls --json` is untouched.
- **No prompt, no wait** (`docs/no-prompt-policy/`): the fallback and the backfill never ask; a fallback prints one stderr line, never into `--json`.
- **`fugaro update` is the whole upgrade** (CLAUDE.md hard rule): RL2B ships the backfill inside `update`, safe with runs in flight, with a convergence test from the previous version.
- **Plugin skills follow the CLI**: RL5 changes the sentences a skill names about `runs ls` in the same release.
- Boundaries. *Always*: a red test before the change; equivalence with the scan. *Ask first*: any IAM or Terraform change (none is planned: D2962 needs none). *Never*: an index entry that adds a row without the run's own records agreeing (D2963); a run that waits for the index; a live cloud action by an agent (Check 760 is the owner's).

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| `--state running` and `launching` return the rows the scan returns | RL1's `TestLiveSetEqualsScan` |
| The live queries read no finished run's objects | RL1's `TestLiveSetReadsNoFinishedRun` |
| A forged index entry never adds a row | RL3's `TestForgedEntryAddsNoRow` |
| A run that is on N and missing from the index still shows (older image, erased entry) | RL3's `TestRecentRunMissingFromIndexStillShows` |
| No entry for N: today's scan, same rows | RL3's `TestNoIndexFallsBackToScan` |
| A writer can never overwrite another run's entry as a unit | RL2's `TestEntryPathIsPerRun`; RL2W's `TestRunnerWritesOnlyItsOwnEntry` |
| Readers of `runs/<slug>/` skip `pr-index/` | RL2's `TestReadersSkipPRIndex` |
| `fugaro update` converges an installation of the previous version and leaves an in-flight run untouched | RL2B's `TestUpdateBackfillsPRIndex`, `TestUpdateBackfillLeavesRunInFlight` |
| `--no-cache` still works and still gives the same rows | RL3's `TestNoCacheUsesIndex` |

## Slices

- **Slice A, the live queries** (RL1): inert without the others, useful alone.
- **Slice B, the pull-request index** (RL2, RL2W, RL2B, RL3): one release; until RL3 lands the index is written and unread, which is inert.
- **Slice C, optional and closing** (RL4, RL5, RL6).

## Tasks

### RL1: `--state running` and `launching` read the live set
- Acceptance: with `--state running` or `--state launching` (alone or with `--repo`, `--mine`, `--since`), `loadRows` lists the backend's non-terminal executions once and the `launching` claims of the claim window only (U1), then reads only those runs' `launch.json`, `result.json` and `task.json`; the PR-state fold takes its `live` word from the same set when it needs no other record (D2967); the rows are exactly the scan's rows for those states; no per-run read happens for a finished run.
- Files: `internal/cli/runs.go`, `internal/cli/runs_live.go` (new), `internal/cli/runs_live_test.go` (new), `internal/runview/runview.go` (only to export the claim bound if U1 needs it).
- Interfaces: consumes `backend.List` with a non-terminal filter, `runstore.Store.Claim`'s object; produces `liveRuns(ctx, env, f) ([]lsRef, error)` that `loadRows` calls for the two states.
- Tests: `TestLiveSetEqualsScan` (mixed fixture: running, launching, succeeded, failed, a claim past its bound), `TestLiveSetReadsNoFinishedRun` (a counting bucket), `TestLiveSetOneExecutionList` (U2), `TestLiveStateWithPRFold`.
- Command: `go test ./internal/cli/ -run 'TestLiveSet|TestLiveState|TestRunsLs' -count=1`
- Red step: `TestLiveSetReadsNoFinishedRun` fails on the scan.
- Depends on: none.
- Commit: `runs ls: --state running and launching read the live set (D2960)`
- Reviewer tier: standard.

### RL2: the pull-request index format and store
- Acceptance: `runstore.PRIndexEntry` (`version`, `run_id`, `slug`, `pr`, `kind` of `opened`, `followed` or `reviewed`, `at`) round-trips through `schemas/pr-index.schema.json` with strict decoding; `PutPRIndex` writes `runs/<slug>/pr-index/<N>/<run-id>.json` create-if-absent (a second write of the same run is a no-op, never an overwrite); `ListPRIndex(ctx, slug, n)` returns run ids from one prefix listing and decodes strictly, skipping an undecodable object with a warning, never failing the query; every existing reader of `runs/<slug>/` (found by grep, named in the PR) skips `pr-index/`.
- Files: `internal/runstore/prindex.go` (new), `internal/runstore/prindex_test.go` (new), `schemas/pr-index.schema.json` (new), `schemas/schemas_test.go`, `internal/runstore/list.go` (only if a reader needs the skip).
- Interfaces: produces `runstore.PRIndexEntry`, `PutPRIndex`, `ListPRIndex`, `PRIndexPath`.
- Tests: `TestPRIndexSchemaRoundTrip`, `TestEntryPathIsPerRun`, `TestPutIsCreateIfAbsent`, `TestListPRIndexByNumber`, `TestBadEntrySkipped`, `TestReadersSkipPRIndex`.
- Command: `go test ./internal/runstore/ ./schemas/ -run 'PRIndex|Entry|Readers|Schema' -count=1`
- Red step: `TestEntryPathIsPerRun` fails before the path function exists.
- Depends on: none.
- Commit: `runstore: the pull-request index, one object per run (D2962)`
- Reviewer tier: opus with an independent review: a new object the untrusted job account writes and a reader trusts as a hint.

### RL2W: the runner and the launcher write their entry
- Acceptance: the runner writes its own run's entry (`kind: opened`) when it opens a pull request and (`followed`) when a follow-up run starts on one, never another run's; a write failure is a warning in the run's log and never fails the run; the CLI writes the entry for `fugaro run --pr N` at launch, so an older job image's follow-up is covered; a run that opens no pull request writes none.
- Files: `internal/runner/prindex.go` (new), `internal/runner/prindex_test.go` (new), `internal/runner/runner.go`, `internal/cli/followup.go`, `internal/cli/followup_test.go`.
- Interfaces: consumes RL2's `PutPRIndex`.
- Tests: `TestRunnerWritesOnlyItsOwnEntry`, `TestRunnerEntryOnOpenAndFollow`, `TestEntryWriteFailureIsWarning`, `TestLauncherWritesEntryForFollowUp`, `TestNoPRNoEntry`.
- Command: `go test ./internal/runner/ -run 'PRIndex|Entry' -count=1 && go test ./internal/cli/ -run 'TestLauncherWritesEntry' -count=1` (the runner command is targeted; the package is not run whole)
- Red step: `TestRunnerEntryOnOpenAndFollow` fails before the hook.
- Depends on: RL2 (the store it calls).
- Commit: `runner, cli: write the pull-request index entry when a run opens or follows (D2962)`
- Reviewer tier: opus with an independent review: an agent-adjacent write path.

### RL2B: `fugaro update` backfills the index
- Acceptance: `fugaro update` (and `update --repo`) walks each repository's runs of the last 90 days once, reads each run's records, and creates the entry of every run that names a pull request, create-if-absent, with the kind the run's own records give it (`opened`, `followed`, or `reviewed` for a review, which names the pull request only in the record its artifact stage wrote); it prints one compact line (count written) and nothing when there is nothing to write; a second update writes nothing; it never rewrites an object a run holds and waits for no run (`TestUpdateLeavesARunInFlightRunning` still passes); an installation at the previous version (runs, no index) converges to the same `--pr` rows after one update.
- Files: `internal/cli/update_prindex.go` (new), `internal/cli/update_prindex_test.go` (new), `internal/cli/update.go`, `docs/design/update-in-flight.md` (the one in-flight line the step adds).
- Interfaces: consumes RL2's `PutPRIndex` and the existing row reader.
- Tests: `TestUpdateBackfillsPRIndex` (previous-version fixture), `TestUpdateBackfillIdempotent`, `TestUpdateBackfillLeavesRunInFlight`, `TestUpdateBackfillQuietWhenNothing`, plus `go test ./internal/cli/ -run Docs` for the in-flight doc pin.
- Command: `go test ./internal/cli/ -run 'TestUpdateBackfill|TestUpdateLeavesARunInFlightRunning|Docs' -count=1 -timeout 30m`
- Red step: `TestUpdateBackfillsPRIndex` fails before the step.
- Depends on: RL2.
- Commit: `update: backfill the pull-request index in place (D2965)`
- Reviewer tier: opus with an independent review: HARD RULE in `CLAUDE.md`, and a bulk write into the runs bucket.

### RL3: `runs ls --pr N` reads the index
- Acceptance: `--pr N` lists `pr-index/<N>/`, reads only the named runs plus the review run going on the pull request (the fold needs it) plus every run of the last 24 hours (D2964), checks each against N from its own records (D2963), and returns the rows the scan returns; with no entry for N it scans as today and prints one stderr line; `--no-cache` bypasses the run cache and still uses the index; `--json` is unchanged; `chain_continues` and `run --pr`, which call `loadRows` with `pr`, take the speed with no change of their own.
- Files: `internal/cli/runs.go`, `internal/cli/runs_pr.go` (new), `internal/cli/runs_pr_test.go` (new).
- Interfaces: consumes RL2's `ListPRIndex`; shares `loadRows` with RL1.
- Tests: `TestPRIndexEqualsScan` (mixed fixture), `TestForgedEntryAddsNoRow`, `TestRecentRunMissingFromIndexStillShows`, `TestNoIndexFallsBackToScan`, `TestNoCacheUsesIndex`, `TestPRReadsOnlyNamedRuns` (a counting bucket), `TestPRFallbackNoteNotInJSON`.
- Command: `go test ./internal/cli/ -run 'TestPRIndexEqualsScan|TestForgedEntry|TestRecentRun|TestNoIndex|TestNoCache|TestPRReads|TestPRFallback|TestRunsLs' -count=1`
- Red step: `TestPRReadsOnlyNamedRuns` fails on the scan.
- Depends on: RL2 (the reader it calls); RL1 (both edit `loadRows` in `runs.go`, so they run in order).
- Commit: `runs ls: --pr N reads the pull-request index (D2962, D2963, D2964)`
- Reviewer tier: opus with an independent review: it decides which runs a landing decision sees.

### RL4: the per-run summary object (optional, owner decides)
- **Gate: build only if, after RL1–RL3 are released, Check 760's `runs ls --since 7d` reading is over 8 s (D2966).** The owner decides after reading it; until then this task is not launched.
- Acceptance: the runner writes `runs/<slug>/<run>/summary.json` (versioned, strict, a schema) at each stage end and at finalize with exactly the fields a table row needs; the table reads it and falls back to the three objects when it is absent or its `version` is unknown; a summary the job wrote is a hint like the index and never overrides a record a launcher wrote (`launch.json`); the rows equal the scan's rows.
- Files: `internal/runstore/summary.go` (new), `internal/runstore/summary_test.go` (new), `schemas/run-summary.schema.json` (new), `internal/runner/runner.go`, `internal/cli/runs.go`. The backfill (an `update` step like RL2B) is a follow-up task the owner's go would add, since a summary exists only for runs after it.
- Tests: `TestSummaryEqualsRecords`, `TestSummaryAbsentFallsBack`, `TestSummaryCannotOverrideLaunch`, `TestSummarySchemaRoundTrip`.
- Command: `go test ./internal/runstore/ ./internal/cli/ -run 'Summary|TestRunsLs' -count=1`
- Red step: `TestSummaryEqualsRecords` fails before the writer.
- Depends on: RL3 (it edits the same loader and the gate reads RL1–RL3's result).
- Commit: `runs ls: one summary object per run for the table`
- Reviewer tier: opus with an independent review: the row would carry job-written values.

### RL5: documents, plugin skills and the release line
- Acceptance: `docs/bucket-iam/spec.md` §10 names the residual of D2964 (an erased entry on a shared job hides a run older than 24 hours from `--pr N`, never invents one); the `working` and `landing` skills say that `runs ls --state running` and `--pr N` are the fast queries and name the stderr fallback line; `docs/design/update-in-flight.md` is current after RL2B; the release notes of the release that carries RL3 add a line to Highlights and `### For operators` stays "run `fugaro update`, it backfills the pull-request index"; the CLI reference is regenerated with `go run ./ee/clidoc` if any help text moved.
- Files: `docs/bucket-iam/spec.md`, `plugin/skills/working/SKILL.md`, `plugin/skills/landing/SKILL.md`, the release notes file of that release under `docs/releases/`, `website/content/cli-reference.md` (only if generated output changes).
- Tests: `go test ./internal/cli/ -run Docs -count=1 -timeout 30m`, `go test ./plugin/... -count=1`, `go test ./ee/clidoc/ -run TestCliReferenceIsCurrent -count=1`; `npm run check` in `website/` only if website content changed.
- Command: as above.
- Red step: the Docs and plugin tests are the pins; a sentence changed in one place and not its pinned twin fails.
- Depends on: RL1, RL3 (the documents describe what they built); RL2B (the operator line).
- Commit: `docs: fast runs ls, its residual and the skills' sentences`
- Reviewer tier: opus with an independent review: plugin skills are a security path (`fugaro security paths --check plugin/skills/working/SKILL.md` answers yes, `plugin-skills`).

### RL6: Check 760, the owner's timing check
- Acceptance: `scripts/live-checks/check-760-runs-ls-speed.sh` times, on the installation it is given and with the run cache warm, `runs ls --state running`, `runs ls --pr N` (N given, or the newest pull request found) with and without `--no-cache`, and `runs ls --since 7d`, three times each, and prints the median beside the 2026-11-19 baseline (20 s, 23 s and 32 s, 15 s); targets: `--state running` and `--pr N` under 5 s, and the reading that decides RL4 (default listing over 8 s); it also compares the rows of `--pr N` for a pull request older than the backfill (read through the fallback scan) with those of a recent one (read through the index) by count and run ids only, as a sanity reading; it exits 0 and prints readings, never fails on a slow reading; no cloud write.
- Files: `scripts/live-checks/check-760-runs-ls-speed.sh` (new), `scripts/live-checks/check-760-runs-ls-speed_test.sh` (new), `scripts/live_checks_test.go` (the `liveCheckScripts` entry), `internal/cli/docs_check760_test.go` (new: the checklist row's pin), `docs/gcp-live-checklist.md` (the row and the section for 760).
- Tests: the `_test.sh` (dry run, no cloud) through `scripts/live_checks_test.go`; the docs pin.
- Command: `go test ./scripts/ -run TestLiveCheckScripts -count=1 && go test ./internal/cli/ -run TestDocsCheck760 -count=1`
- Red step: the `_test.sh` fails before the script exists.
- Depends on: RL1, RL3 (it times what they built), RL2B (the index must be backfilled on the real project).
- Commit: `live-checks: Check 760, timing runs ls`
- Reviewer tier: standard.
- Owner-run: after the release and `fugaro update`, the owner runs `bash scripts/live-checks/check-760-runs-ls-speed.sh --installation <installation> --pr <N>`. No agent performs any step of it.

## Parallel execution

**Levels.**

| Level | Tasks |
| --- | --- |
| 0 | RL1, RL2 |
| 1 | RL2W, RL2B, RL3 |
| 2 | RL5, RL6, RL4 (optional, after the owner reads Check 760) |

**Edges kept**, each with its reason:

- RL2W → RL2, RL2B → RL2, RL3 → RL2: they call the store RL2 builds. RL2 is a type, a path and two functions (size 2); a contract task would be nearly all of it, so the edge is kept.
- RL3 → RL1: both edit `loadRows` in `internal/cli/runs.go`; two writers of one file cannot share a level.
- RL4 → RL3: the same file and the gate reads RL3's result.
- RL5 → RL1, RL3, RL2B: the documents describe what they built.
- RL6 → RL1, RL3, RL2B: the check times what they built, on an index the backfill filled.

**Edges broken.** None. The decoupling pass is on (`planning.decoupling` is `true` for the project and the repository; `rigor.default` is `startup`) and was run on every edge: each dependent needs RL2's interface only, but RL2 is so small that a contract plus its integration task lengthens the path (RL2 → RL3 → RL5, weight 2 + 3 + 1 = 6) instead of shortening it.

**Critical path:** `fugaro plan graph` reports RL1 → RL3 → RL4 (3 tasks), because it counts the optional RL4; without RL4 it is RL1 → RL3 → RL5 (weight 2 + 3 + 1 = 6), and RL2 → RL3 → RL5 has the same weight.

**Shared files.** No level has two writers of one file:

| File | Writers (level) |
| --- | --- |
| `internal/cli/runs.go` | RL1 (0), RL3 (1), RL4 (2, optional) |
| `internal/runner/runner.go` | RL2W (1), RL4 (2, optional) |
| `internal/cli/update.go` | RL2B (1) |
| `schemas/schemas_test.go` | RL2 (0) |
| `docs/design/update-in-flight.md` | RL2B (1), RL5 reads it only |
| `docs/bucket-iam/spec.md`, `plugin/skills/*/SKILL.md`, release notes | RL5 (2) |
| `docs/gcp-live-checklist.md`, `scripts/live_checks_test.go` | RL6 (2) |

**Contract changes anticipated.** None.

## The estimate the graph read printed

As `fugaro plan graph` printed it: `ready in 285–1140 min, no history yet for 8 tasks (8 tasks; seeds for 8; from the seeds: no history is cached on this machine yet)`. It is informational and reserves no budget.

## Checks the plan takes

- **Check 760** (RL6): the timing of the three queries and the RL4 gate reading. The owner runs it after the release; it gates nothing but the decision on RL4.

## After-deploy steps

None beyond `fugaro update`, which backfills the index (RL2B). Release notes: `### For operators` is "run `fugaro update`" plus one line saying the update writes the pull-request index for the last 90 days of runs, creates only what is missing and waits for no run.

## The proposed launch

Seven agent tasks (RL1, RL2, RL2W, RL2B, RL3, RL5, RL6; RL4 is held until Check 760 is read) in three waves: RL1 and RL2 together, then RL2W, RL2B and RL3, then RL5 and RL6. Each run's spend is bounded by the repository's own caps. None is launched until the owner says so.

## Later, not in this plan

A Realtime Database live index; a summary backfill; pagination of the 90-day listing.

## Considered and declined

- A Realtime Database live index: it fixes `--state running` only.
- The outline's single `index/<slug>/pr-<N>.json` file under a new prefix (D2962: needs an IAM grant and is overwritable as a unit).
- Trusting the index for rows (D2963).
- A new `--no-index` flag (D2964: `--no-cache` and the 24 h union are enough).
- A contract task for RL2's types (above: no gain).
