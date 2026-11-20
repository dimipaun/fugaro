# Task: runs ls: --pr N reads the pull-request index

Build task RL3 of `docs/runs-ls-speed/implementation-plan.md`. It needs RL1 and RL2 merged (it edits the same `loadRows` as RL1 and calls RL2's `ListPRIndex`). Read its RL3 section and `docs/runs-ls-speed/reconciliation.md` (F4, F5, D2963, D2964).

**Goal.** `fugaro runs ls --pr N` takes 23 s for one row because it reads every record in 90 days. Make it list `runs/<slug>/pr-index/<N>/`, then read only the named runs, plus the review run going on the pull request (the PR-state fold needs it), plus every run of the last 24 hours (so a missing recent entry cannot hide a run). Check each against N from its own records and show it only if they agree: the index is a hint, a forged entry adds a read and never a row. With no entry for N, scan exactly as today and print one stderr line (never into `--json`). `--no-cache` still bypasses the run cache and still uses the index. `chain_continues` and `run --pr` call `loadRows` with `pr`, so they take the speed with no change.

**Files.** `internal/cli/runs.go`, `internal/cli/runs_pr.go` (new), `internal/cli/runs_pr_test.go` (new).

**Tests, red first.** `TestPRIndexEqualsScan` (mixed fixture), `TestForgedEntryAddsNoRow`, `TestRecentRunMissingFromIndexStillShows`, `TestNoIndexFallsBackToScan`, `TestNoCacheUsesIndex`, `TestPRReadsOnlyNamedRuns` (counting bucket; fails on the scan), `TestPRFallbackNoteNotInJSON`. Run `go test ./internal/cli/ -run 'TestPRIndexEqualsScan|TestForgedEntry|TestRecentRun|TestNoIndex|TestNoCache|TestPRReads|TestPRFallback|TestRunsLs' -count=1`, then build, gofmt, vet.

**Rules.** Same rows, same columns, same `--json`. No new flag. Targeted tests, not the whole package. Do not touch `docs/status.md` or `.github/workflows/`. Commit message: `runs ls: --pr N reads the pull-request index (D2962, D2963, D2964)`.
