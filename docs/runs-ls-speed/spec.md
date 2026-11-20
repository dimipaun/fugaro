# Spec: make `fugaro runs ls` fast

**Owner: the owner (Dimi Paun). Date: 2026-11-19.** Agreed in outline in conversation on 2026-11-19 after timing the command on the real project; written down here from that outline before the code was re-read for this plan.

## Problem

`fugaro runs ls` reads every run in its window, then filters. Measured 2026-11-19 on the real project with a warm run cache (the runs bucket is GCS, not the Realtime Database):

| Query | Time | Rows |
| --- | --- | --- |
| `runs ls --since 24h` | 3 s | 79 |
| `runs ls --since 7d` (the default) | 15 s | 991 |
| `runs ls --since 90d` | over 250 s | not finished |
| `runs ls --state running` | 20 s | 0 |
| `runs ls --pr 3253` (90-day lookback) | 23 s; 32 s with `--no-cache` | 1 |

The cause: `loadRows` lists every run id in the window, reads each run's `launch.json`, `result.json` and `task.json` (through `internal/runcache`, which still lists every object of the repository to compare generations and saves about 30 %), joins one backend execution listing, and filters (`--state`, `--pr`, `--debt`) only afterwards. `--pr` skips the execution join for runs off the pull request but still reads every record in 90 days.

## What it is

1. **The live queries read the live set.** `--state running` and `--state launching` ask the backend for non-terminal executions and read the `launching` claims, then read only those runs.
2. **A pull-request index.** A small object per pull request names the runs that opened or followed it, written when a run opens or follows the pull request.
3. **`--pr N` reads the index**, then only those runs, and falls back to today's scan when there is no index.
4. **Optionally, a per-run summary object**, built only if the default listing is still slow after 1 to 3.
5. **Documented, and timed by the owner** against the numbers above.

## Not in it

A Realtime Database live index (it would fix `--state running` only, and the common queries are `--pr N` and the ready and stalled sweeps). Any change to the rows, columns or `--json` shape.
