# Task: update: backfill the pull-request index in place

Build task RL2B of `docs/runs-ls-speed/implementation-plan.md`. It needs RL2 merged. Read its RL2B section, `docs/runs-ls-speed/reconciliation.md` (F6, D2965), the HARD RULE about `fugaro update` in `CLAUDE.md`, and `docs/design/update-in-flight.md`.

**Goal.** Existing pull requests have no index entries. `fugaro update` (and `update --repo`) walks each repository's runs of the last 90 days once, reads each run's records, and creates the entry of every run that names a pull request, create-if-absent via `runstore.PutPRIndex`. It prints one compact line (the count written) and nothing when there is nothing to write. A second update writes nothing. It never rewrites an object a run holds and waits for no run; `TestUpdateLeavesARunInFlightRunning` must still pass. Add the one in-flight line the step needs to `docs/design/update-in-flight.md` and keep the docs tests green.

**Files.** `internal/cli/update_prindex.go` (new), `internal/cli/update_prindex_test.go` (new), `internal/cli/update.go`, `docs/design/update-in-flight.md`.

**Tests, red first.** `TestUpdateBackfillsPRIndex` (an installation at the previous version: runs, no index; after one update `runs ls --pr N` through the index returns the same rows as the scan), `TestUpdateBackfillIdempotent`, `TestUpdateBackfillLeavesRunInFlight`, `TestUpdateBackfillQuietWhenNothing`. Run `go test ./internal/cli/ -run 'TestUpdateBackfill|TestUpdateLeavesARunInFlightRunning|Docs' -count=1 -timeout 30m`, then build, gofmt, vet.

**Rules.** No prompt, no confirmation step, no operator step: the release's `### For operators` stays "run `fugaro update`". Hermetic tests only. Do not touch `docs/status.md` or `.github/workflows/`. Commit message: `update: backfill the pull-request index in place (D2965)`.
