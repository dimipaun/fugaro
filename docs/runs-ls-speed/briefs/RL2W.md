# Task: runner, cli: write the pull-request index entry when a run opens or follows

**Superseded (D3204, 2026-11-25).** The runner's write this brief asks for retired with IR5a of `docs/console-fixes/implementation-plan.md`, which deleted `internal/runner/prindex_test.go` and the tests named below with it; the launcher's write it also asks for stops with IR5b, with the objects left in place.

Build task RL2W of `docs/runs-ls-speed/implementation-plan.md`. It needs RL2 merged (it calls `runstore.PutPRIndex`). Read its RL2W section and `docs/runs-ls-speed/reconciliation.md` (F4, F5, D2962, D2963).

**Goal.** Make the index get written. The runner writes its own run's entry (`kind: opened`) when it opens a pull request and (`followed`) when a follow-up run starts on one, never another run's entry. A write failure is a warning in the run's log and never fails the run. The launching CLI writes the entry for `fugaro run --pr N` at launch, so a follow-up on an older job image is still covered. A run that opens no pull request writes none.

**Files.** `internal/runner/prindex.go` (new), `internal/runner/prindex_test.go` (new), `internal/runner/runner.go`, `internal/cli/followup.go`, `internal/cli/followup_test.go`. Find the exact place the runner learns the pull request number (the finalize step that calls `internal/gitprov`) and hook there; keep the diff small.

**Tests, red first.** `TestRunnerWritesOnlyItsOwnEntry`, `TestRunnerEntryOnOpenAndFollow` (fails before the hook), `TestEntryWriteFailureIsWarning`, `TestLauncherWritesEntryForFollowUp`, `TestNoPRNoEntry`. Use the fake provider and `internal/gcpfake`. Run `go test ./internal/runner/ -run 'PRIndex|Entry' -count=1` and `go test ./internal/cli/ -run 'TestLauncherWritesEntry' -count=1`; never the whole runner package (about 25 minutes). Then build, gofmt, vet.

**Rules.** The runner never waits on the index. No prompt. Do not touch `docs/status.md` or `.github/workflows/`. Commit message: `runner, cli: write the pull-request index entry when a run opens or follows (D2962)`.
