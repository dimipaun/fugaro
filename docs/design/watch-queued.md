# `fugaro watch` shows queued runs (design)

*Status: 2026-10-08, built. Code references are to `main` before this change.*

## 1. Problem

`fugaro watch` builds its whole view from four Realtime Database streams
(`/config`, `/spend/<day>/global`, `/spend/<day>/repos`, `/agents`;
`internal/watch`, `internal/cli/watch.go`). The `/agents/<slug>/<run>`
registry entry a repository's block of runs comes from
(`budget.Session.Start`, `internal/budget/registry.go`) is written by the
runner only once it has actually started — after the launch claim and
`launch.json` already exist in the runs bucket. A run that is claimed (a CLI
is between `task.json` and `launch.json`) or launched but not yet picked up
by the backend therefore has no registry entry, and `watch` shows nothing
for it, even though `fugaro ls` and `fugaro diagnose` already call it
`launching` or `pending` (`internal/runview`, reading the runs bucket
directly). A user who launched a run that sat `pending` saw it in `ls` but
not in `watch`.

## 2. Investigation

**Where a queued run lives.** The launch claim (`runstore.Claim`, the
`runs/<slug>/<run>/launching` object) and `task.json` are both plain objects
in the runs bucket (GCS), written by the launching CLI before `launch.json`
exists (`internal/cli/run.go`). Neither is ever written to RTDB. `runview.Join`
(`internal/runview/runview.go`) already classifies a run as `StatusLaunching`
(a claim, no `launch.json`, no execution, no record) or `StatusPending`
(`launch.json` exists, the backend hasn't shown an execution or record yet)
purely from these bucket objects — no backend call needed for either case.
Both states are inherently bounded to `runstore.ClaimTTL` (10 minutes) from
the claim's or `launch.json`'s own timestamp: past that, `Join` reclassifies
the run as `unlaunched` (claim only) or `infra_error`/"lost" (`launch.json`
exists). The claim and `launch.json` are never deleted by a run that starts
(the runner's own bootstrap doesn't touch them) or by one that is cancelled
before launch (`runstore.CancelRequested` is a separate marker) — but since
`Join` derives status from the *set* of objects present, not from an
explicit "queued" flag, reading the same objects `ls` reads is enough to
tell a queued run from a started, finished or cancelled one without a new
field or a new write.

**Who can read them.** `fugaro watch` authenticates to RTDB as the caller
(ADC) and documents that it "needs only the Viewer role on the Firebase
project" (`gcp-setup.md`). The runs bucket is a different credential domain:
`init` grants `roles/storage.objectAdmin` on it only to launchers and
operators, and *removes* the project's convenience Viewer binding from it
on purpose, because the bucket holds run transcripts and caches
(`gcp-setup.md` §6.1, "The state and runs buckets have no project-wide
readers"). So a person who is only a Firebase Viewer — the audience `watch`
was written for — cannot always read the bucket; a launcher or operator
(the common case: whoever runs `fugaro run` usually also runs `fugaro
watch`) can. This is a real, intentional permission boundary, not a bug to
route around.

## 3. Decision

Read queued rows from the runs bucket, the same way `fugaro ls` does
(`internal/cli/ls.go`'s `lsSlugs`/`readRun`, joined with `runview.Join`),
through a bucket-only connection (`openQueueBucket`,
`internal/cli/watch_queued.go`) that never opens the compute backend, so the
Cloud Run permission a launcher/operator also holds is never required just
for this. When the bucket can't be read (no grant, or any other failure),
`watch` shows a one-line degrade note instead of the queued rows, and never
touches the RTDB-driven rows: a pure Viewer gets everything they had before,
plus a note, never a crash and never a stale screen. This needed no new
write path and no Realtime Database rules change: it is a read fan-out
layered in `internal/cli`, kept out of `internal/watch`'s existing
network-free, RTDB-only `State`/`Build` model (`MergeQueued` is a pure
function from a `View` and a slice of `QueuedRun` to a `View`; the CLI layer
polls the bucket and calls it).

**Dedup.** A queued row is skipped if its `(slug, run)` already has a live
row from the registry (`MergeQueued`); this is what makes a run that starts
move from its queued row to its live one without a duplicate, independent
of bucket/RTDB poll timing.

**The stuck threshold.** `QueuedStuckAfter` (10 minutes) is a separate named
constant from `runstore.ClaimTTL`, even though they currently hold the same
value: one tunes a display warning, the other a launch's protection window.
Because they coincide today, a row's `Stuck` flag can only turn true in the
narrow window between the runs-bucket poll that still finds it queued and
the next one (which will have reclassified it away); within that window the
displayed age still advances every render. If the two constants ever
diverge, the warning becomes the earlier, non-degenerate signal
`QueuedStuckAfter` was meant to be.

## 4. What changed

- `internal/watch`: `QueuedRun`, `MergeQueued`, `QueuedStuckAfter`
  (`queued.go`); `RunRow.Queued/Stuck/Workflow/RequestedBy` and
  `View.QueuedNote`; the `queued` flags/stage render in `plain.go`,
  `render.go` (TUI) and `json.go` (`queued`, `stuck`, `workflow`,
  `requested_by`, `queued_note`, all `omitempty`).
- `internal/cli/watch_queued.go`: `openQueueBucket` (bucket only, no
  backend), `queuedRuns`/`fetchQueued` (one bucket-only read reusing `ls`'s
  own functions), `queuedSource` (polls every 15s on its own clock,
  independent of the RTDB ticks, keeps the last good rows on a hiccup).
- `internal/cli/watch.go`: wires `WatchDeps.Queued` into `--once`, the
  streaming (`--plain`/`--json`) loop and the TUI (`TUIOptions.Queued`).

No change to `internal/budget/rules` or any write path.
