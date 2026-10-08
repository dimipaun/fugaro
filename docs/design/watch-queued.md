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
Both states end at `runstore.ClaimTTL` (10 minutes) from the claim's or
`launch.json`'s own timestamp: past that, `Join` reclassifies the run as
`unlaunched` (claim only) or `infra_error`/`ReasonLost` (`launch.json`, no
record, no execution). That is exactly the run watch should warn about, so
watch keeps it (§3). The claim and `launch.json` are never deleted by a run
that starts or by one cancelled before launch; cancelling writes a separate
`cancel` marker, which `ls`'s `readRun` only checks when there is no
`launch.json`, so watch checks it itself for a launched run.

**Run IDs are not launch times.** `fugaro run --retry` launches a stored
run under its old ID, so filtering by the ID's mint time
(`runstore.ListRunIDs(since)`) misses it. Watch instead lists the claim and
`launch.json` objects themselves and goes by their modification time
(`runstore.RecentLaunches`).

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

**Which runs are queued.** A run is a candidate when its launch claim or
`launch.json` was written within `queuedLookback` (30 minutes), by the
object's own modification time. It is shown queued when it has no
`result.json` (a record means it started: running, finished, or lost after
starting, never queued), no `cancel` marker, no corrupt object, and
`runview.Join` (with no execution looked up) calls it:

- `launching` or `pending`: a fresh queued row;
- `unlaunched` with a claim, or `infra_error` with `ReasonLost`: runview has
  given the launch up after `ClaimTTL`; watch shows it as a **stuck** queued
  row (`QueuedRun.Stale`), "⚠ not started after N min".

Its age, and the lookback, use the claim's or `launch.json`'s own timestamp.
A stuck row therefore shows from 10 to 30 minutes after the launch, then
leaves the window; `fugaro ls` still lists it (as unlaunched or lost).

**The stuck threshold.** `QueuedStuckAfter` is `runstore.ClaimTTL` (10
minutes) on purpose: the moment runview gives a launch up is the moment
watch starts warning, and because watch keeps those runs (above) instead of
dropping them, the warning is visible for the rest of the lookback. (The
first version of this change dropped them, so the flag could only be seen
between two bucket polls.)

**Never blocking the screen.** Every bucket read runs off the render path.
The live screen starts a background scan at once and every 15 seconds
(`queuedPollInterval`); the first frame never waits for it. `watch --once`
waits for its one scan at most `queuedOnceWait` (3 seconds), then prints
without queued rows and with a note. A scan (marker check, listings, reads)
is bounded by `queuedScanTimeout` (10 seconds) and reuses one bucket handle
for the whole session. Opening the handle (credential discovery) is the
one step outside that timeout, because the handle's credentials keep the
context they were opened with; it too runs in the background.

**Cost per scan.** Once per session: one read of the project marker. Per
repository: the slug resolution `ls` does, then one listing of
`runs/<slug>/` filtered server side with GCS `matchGlob`
(`*/{launch.json,launching}`), i.e. one list call per 1000 such objects; a
repository has at most two per launched run ever, so a repository with
5000 runs costs about 10 list calls, and returns no object body. (On a
non-GCS bucket, i.e. tests and `file://`, the whole prefix is listed and
filtered locally.) Per run launched within the 30-minute lookback: 3 to 5
small reads (`task.json`, `launch.json`, `result.json`, the `cancel` marker
and, with no `launch.json`, the claim). Older runs cost nothing beyond the
listing. The listing still grows with the repository's history (no listing
by modification time exists); that is the one unbounded term, measured
above.

**When reads fail.** A run that can't be read (or holds a corrupt object)
is left out and counted in a one-line note ("queued runs: N could not be
read and are not shown"); the rest of the scan goes on. A scan that fails
as a whole (bucket unreadable, timeout) sets the note "queued runs
unavailable: …". The last good queued rows are kept through
`queuedDropAfter` − 1 such failures in a row (the note then says they are
from an earlier read) and dropped at the third (about 45 seconds), so rows
from a bucket that stays unreadable are never presented as live.

**Who sees them.** Only someone who can read the runs bucket: a launcher or
an operator. A plain Firebase Viewer without bucket access never sees a
queued row; they always get the one-line "queued runs unavailable" note,
and everything else watch shows. The audience that can launch runs is the
audience that can see them queued.

## 4. What changed

- `internal/watch`: `QueuedRun` (with `Stale`), `MergeQueued`,
  `QueuedStuckAfter` (`queued.go`); `RunRow.Queued/Stuck/Workflow/RequestedBy`
  and `View.QueuedNote`; the `queued` flags/stage render in `plain.go`,
  `render.go` (TUI) and `json.go` (`queued`, `stuck`, `workflow`,
  `requested_by`, `queued_note`, all `omitempty`). All task.json-derived
  text is sanitised (`clean`).
- `internal/runstore`: `RecentLaunches` (read only: one filtered listing).
- `internal/cli/watch_queued.go`: `queuedScanner` (one reused bucket-only
  handle, no backend), `queuedFromRun` (the rule above, reusing `ls`'s
  `readRun` plus the cancel marker), `fetchQueuedOnce` (bounded wait),
  `queuedSource` (background polling, failure rule).
- `internal/cli/watch.go`: wires `WatchDeps.Queued` into `--once`, the
  streaming (`--plain`/`--json`) loop and the TUI (`TUIOptions.Queued`).

No change to `internal/budget/rules` or any write path.
