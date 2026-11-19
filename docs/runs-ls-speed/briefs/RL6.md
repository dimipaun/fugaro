# Task: live-checks: Check 760, timing runs ls

Build task RL6 of `docs/runs-ls-speed/implementation-plan.md`. It needs RL1, RL3 and RL2B merged. Read its RL6 section. Follow the pattern of the existing owner-run checks in `scripts/live-checks/` (`check91.sh` with its `_test.sh`) and of `docs/gcp-live-checklist.md`'s rows for the highest existing checks. The check is the owner's: no agent runs any step of it.

**Goal.** One script, `scripts/live-checks/check-760-runs-ls-speed.sh`, run by the owner as `bash scripts/live-checks/check-760-runs-ls-speed.sh --installation <installation> --pr <N>`. With the run cache warm it times, three runs each and the median printed beside the 2026-11-19 baseline, `fugaro runs ls --state running` (baseline 20 s), `--pr N` with and without `--no-cache` (23 s and 32 s) and the default `--since 7d` (15 s). Targets: `--state running` and `--pr N` under 5 s. It prints the reading that decides RL4 (default listing over 8 s means build RL4). It also compares, by count and run ids only, `--pr N` for a pull request older than the backfill (read through the fallback) with a recent one (read through the index), as a sanity reading. It exits 0 and prints readings, never failing on a slow one, and makes no cloud write.

**Files.** `scripts/live-checks/check-760-runs-ls-speed.sh` (new), `scripts/live-checks/check-760-runs-ls-speed_test.sh` (new, dry run, no cloud), `scripts/live_checks_test.go` (the `liveCheckScripts` entry), `internal/cli/docs_check760_test.go` (new: pins the checklist row), `docs/gcp-live-checklist.md` (the row and a section, marked OWNER-RUN, NOT RUN).

**Tests, red first.** The `_test.sh` fails before the script exists. Run `go test ./scripts/ -run TestLiveCheckScripts -count=1 && go test ./internal/cli/ -run TestDocsCheck760 -count=1`, then `go test ./internal/cli/ -run Docs -count=1 -timeout 30m` for any pinned census the checklist change names.

**Rules.** No secret-shaped literals. Do not touch `docs/status.md` or `.github/workflows/`. Commit message: `live-checks: Check 760, timing runs ls`.
