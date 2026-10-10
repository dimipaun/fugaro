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

**Run IDs are (almost) launch times.** A run ID is
`YYYYMMDD-HHMMSS-<hex>`, its UTC mint time (`task.NewRunID`), minted by the
launching CLI just before it writes `task.json`, the claim and
`launch.json`. IDs therefore sort by time, and "the runs launched in the
last 30 minutes" is, up to a small margin, "the run directories whose name
sorts at or after now − 30 min". GCS lists from a start offset server side
(`storage.Query.StartOffset`), so that listing costs the same for a
repository with 50 000 runs as for one with 5. The claim and `launch.json`
are never deleted, so any listing that matches them by name (an earlier
version of this change used a `matchGlob`) grows with every run ever
launched; and GCS cannot list by modification time.

The one exception is `fugaro run --retry` of a stored task that never
started: it launches under the task's old ID, so it is outside that
listing (see §5).

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

**Which runs are queued.** A run is a candidate when its ID was minted
within `queuedLookback` (30 minutes) plus `queuedMintMargin` (5 minutes):
`runstore.ListRunIDs(since)` lists `runs/<slug>/` (delimiter `/`) from the
offset `runs/<slug>/<since as YYYYMMDD-HHMMSS>`. One offset also covers a
day boundary (`20261007-235900-…` sorts before `20261008-000100-…`), so no
second listing is needed after midnight; a bucket that ignores the offset
(`file://`, memblob in tests) lists the prefix and the same check is
applied to each ID locally. A candidate is dropped unless its claim's or
`launch.json`'s own timestamp is within the 30 minutes. It is shown queued when it has no
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
The live screen starts a background scan at once and then every 60
seconds plus up to 5 seconds of jitter (`queuedPollEvery`,
`queuedPollJitter`), slower than the 15-second RTDB view because every scan
lists each repository; the first frame never waits for it. `watch --once`
waits for its one scan at most `queuedOnceWait` (3 seconds), then prints
without queued rows and with a note.

Within a repository the runs are read `queuedRunReaders` (6) at a time,
all under the repository's one deadline. When the deadline fires mid-way
the rows already read are kept and the note counts the rest ("<slug>: N of
M runs not read"); they are never thrown away with the unread ones.

A scan reads the repositories in parallel, `queuedWorkers` (8) at a time,
each under its own `queuedRepoTimeout` (10 seconds) covering its listing
and its reads; the marker check and the repository listing each have
`queuedStepTimeout` (10 seconds). A repository that times out or fails is
left out and counted in the note; the rows of the others are kept.

The bucket handle is opened once per session, on its own goroutine with
the session's context (its credentials keep the context they were found
with); a scan waits for it at most `queuedOpenTimeout` (20 seconds) and
never past quitting, so quitting never waits for credential discovery. An
open still running past the timeout is not restarted: the next scan waits
for the same one. A failed open (no credentials, no grant, a marker
mismatch) is retried only after a backoff of 1, 2, 4, then 5 minutes
(`queuedOpenBackoffMin`/`Max`); scans in between fail at once with its
error, so credential discovery is not repeated every poll.

**Cost.** Once per session: the open and one read of the project marker.
Per scan: one listing of the repositories (none when the local config
names them), then one small listing per repository, starting at the
lookback offset, so it returns only the run directories minted in the last
35 minutes (a single page unless a repository launched over 1000 runs in
that time). That is repos + 1 list calls per scan however long the
history; per run minted in that window, 3 to 5 small reads (`task.json`,
`launch.json`, `result.json`, the `cancel` marker and, with no
`launch.json`, the claim), or 3 for a run that has a record (`readRun` always reads `task.json`, `launch.json` and `result.json`). At the 60 s
cadence one open watch over 60 repositories makes about 61 list calls a
minute, about 88 000 a day (Class A), plus the reads of recent runs (Class
B); 15 seconds and a full listing per repository before cost about 40
times that for a repository with 5000 runs.

**When reads fail.** A run that can't be read (or holds a corrupt object)
is left out and counted in a one-line note ("runs: N could not be
read and are not shown"); a repository that can't be read is left out and
counted the same way ("N of M repositories not read"); the rest of the
scan goes on. A scan that fails as a whole (bucket unopenable, repository
listing timeout) sets the note "queued runs unavailable: …". The last good
queued rows are kept through `queuedDropAfter` − 1 such failures in a row
(the note then says they are from an earlier read) and dropped at the
third (about three minutes), so rows from a bucket that stays unreadable
are never presented as live.

**Who sees them.** Only someone who can read the runs bucket: a launcher or
an operator. A plain Firebase Viewer without bucket access never sees a
queued row; they always get the one-line "queued runs unavailable" note,
and everything else watch shows. The audience that can launch runs is the
audience that can see them queued.

## 5. Limitations

- **`--retry` of an old stored task is not shown queued.** Its ID (the
  stored task's) is older than the listing's offset, though its
  `launch.json` is fresh. It appears in watch as a live row once its runner
  starts; `fugaro ls` shows it pending meanwhile.
- **A caller-chosen `--run-id` is not covered** (`v1.md`; `dogfooding.md`
  recommends one per batch piece). The listing relies on the ID's mint time:
  a pre-chosen or old ID is never shown queued, and a future-dated one is
  listed on every scan until the lookback passes it.
- **`watch --once` may often print "queued runs unavailable"** at around 60
  repositories: its 3-second budget (`queuedOnceWait`) is often not enough
  for the first scan, which opens the bucket, finds credentials and lists
  every repository. Use `fugaro watch` (the TUI) or run `--once` twice for
  the full picture.
- **A run that dies before its runner writes `result.json`** shows queued,
  then stuck from 10 minutes, until 30 minutes after its launch; `fugaro ls`
  shows it `infra_error` (lost).
- **A run that starts and finishes between two scans** can briefly
  reappear as queued: its registry entry is gone from RTDB while the last
  scan (up to a minute old) still lists it without a record. The next scan
  drops it.
- **An unreadable cancel marker hides a live queued row**: the run is
  counted as unreadable in the note, not shown.
- **Client clock skew** shifts the 30-minute cutoff and the stuck warning:
  ages come from the launching client's `LaunchedAt`/`Claim.At` and the run
  ID's mint time, compared with the watching client's clock.
- **A Firebase Viewer without bucket access never sees queued rows**, only
  the note (§3, "Who sees them").

## 6. What changed

- `internal/watch`: `QueuedRun` (with `Stale`), `MergeQueued`,
  `QueuedStuckAfter` (`queued.go`); `RunRow.Queued/Stuck/Workflow/RequestedBy`
  and `View.QueuedNote`; the `queued` flags/stage render in `plain.go`,
  `render.go` (TUI) and `json.go` (`queued`, `stuck`, `workflow`,
  `requested_by`, `queued_note`, all `omitempty`). All task.json-derived
  text is sanitised (`clean`).
- `internal/runstore`: `ListRunIDs(since)` lists from a server-side start
  offset on GCS (same result as before, for `ls` and the budget rollover
  too; only cheaper).
- `internal/gcpfake`: the GCS fake honours `startOffset`, `endOffset`,
  `maxResults` and `pageToken`, and counts list calls.
- `internal/cli/watch_queued.go`: `queuedScanner` (one reused bucket-only
  handle, no backend, opened off the scan with a timeout and a backoff;
  repositories read in parallel, each with its own deadline),
  `queuedFromRun` (the rule above, reusing `ls`'s `readRun` plus the cancel
  marker), `fetchQueuedOnce` (bounded wait), `queuedSource` (background
  polling at the slower cadence, failure rule).
- `internal/cli/watch.go`: wires `WatchDeps.Queued` into `--once`, the
  streaming (`--plain`/`--json`) loop and the TUI (`TUIOptions.Queued`).

No change to `internal/budget/rules` or any write path.
