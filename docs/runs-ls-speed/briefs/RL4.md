# Task: runs ls: one summary object per run for the table

**Do not launch this task until the owner says so.** It is built only if, after RL1 to RL3 are released, Check 760 reads `runs ls --since 7d` over 8 seconds (D2966). Read its RL4 section in `docs/runs-ls-speed/implementation-plan.md` and `docs/runs-ls-speed/reconciliation.md` (F4, F8, D2966) first.

**Goal.** The table reads `launch.json`, `result.json` and `task.json` for every run. Have the runner write `runs/<slug>/<run>/summary.json` (versioned, strict decode, a schema, add-only fields) at each stage end and at finalize, holding exactly the fields a table row needs, and have `runs ls` read that one object, falling back to the three when it is absent or its `version` is unknown. A summary the job wrote is a hint like the index: it never overrides a value a launcher wrote (`launch.json`). The rows equal the scan's rows.

**Files.** `internal/runstore/summary.go` (new), `internal/runstore/summary_test.go` (new), `schemas/run-summary.schema.json` (new), `internal/runner/runner.go`, `internal/cli/runs.go`. A summary backfill is a separate follow-up task; do not add one here.

**Tests, red first.** `TestSummaryEqualsRecords`, `TestSummaryAbsentFallsBack`, `TestSummaryCannotOverrideLaunch`, `TestSummarySchemaRoundTrip`. Run `go test ./internal/runstore/ ./internal/cli/ -run 'Summary|TestRunsLs' -count=1`, then build, gofmt, vet.

**Rules.** A field never changes meaning. Targeted tests. Do not touch `docs/status.md` or `.github/workflows/`. Commit message: `runs ls: one summary object per run for the table`.
