# Task: runs ls: --state running and launching read the live set

Build task RL1 of `docs/runs-ls-speed/implementation-plan.md`. Read that plan's RL1 section, `docs/runs-ls-speed/reconciliation.md` (F1, F2, D2960, D2967, U1, U2) and `CLAUDE.md` first.

**Goal.** `fugaro runs ls --state running` and `--state launching` take 20 s for 0 rows because `loadRows` (`internal/cli/runs.go`) reads every run in the window and filters afterwards. Make those two states read the live set directly: one backend list of non-terminal executions, plus the `launching` claim objects of the claim window only (the bound `internal/runview` uses to call a claim abandoned; export it if needed), then read only those runs' `launch.json`, `result.json` and `task.json`. Where the PR-state fold needs only the `live` word, take it from the same set.

**Files.** `internal/cli/runs.go`, `internal/cli/runs_live.go` (new), `internal/cli/runs_live_test.go` (new), `internal/runview/runview.go` only to export the claim bound.

**Acceptance.** The rows for `--state running` and `--state launching` equal the scan's rows on a mixed fixture (running, launching, succeeded, failed, a claim past its bound); no per-run object is read for a finished run (a counting bucket proves it); one execution list call. No change to columns or `--json`.

**Tests, red first.** `TestLiveSetEqualsScan`, `TestLiveSetReadsNoFinishedRun` (fails on the scan), `TestLiveSetOneExecutionList`, `TestLiveStateWithPRFold`. Use `internal/gcpfake` and the fake backend. Run `go test ./internal/cli/ -run 'TestLiveSet|TestLiveState|TestRunsLs' -count=1`, then `go build ./... && gofmt -l . && go vet ./...`.

**Rules.** Targeted tests only, not the whole `internal/cli` package. No new flag, no prompt. Comments say why. Do not touch `docs/status.md` or `.github/workflows/`. Commit message: `runs ls: --state running and launching read the live set (D2960)`.
