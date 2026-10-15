# The JSON contract the console reads

The console is a renderer of documents the CLI already emits (design
[desktop-console.md](../../docs/design/desktop-console.md) §1, D1): it spawns
the installed `fugaro`, reads its JSON, and draws it. Nothing in it reads the
cloud, holds a credential or writes to the database, so **this document is the
whole of what it may rely on**, and it is the reason the console can be
reviewed at all without a live installation.

It is pinned in two passes, by two tasks of the plan
([2026-10-15-desktop-console.md](../../docs/plans/2026-10-15-desktop-console.md)):

- **Task 0.1** pins each document's shape, field by field, against the struct
  that encodes it.
- **Task 0.2** — this section — pins what `watch --json` guarantees to a
  **long-lived child process**: the one command the console runs as a stream
  rather than as a one-shot read. It settles the design's §12 item 2 by reading
  the code, not by assuming it, and it decides whether the console's sidecar
  (D1, option A) needs a supervisor process.

Every claim below names the symbol it was read from, on `main` at `411fc18`
(2026-10-15). The review criterion for this document is that every sentence
cites a symbol (plan, "Review Focus", phase 0).

## 1. What `watch --json` guarantees a long-lived child process

### 1.1 One document per change, on a one-second floor

`WatchDeps.emit` (`internal/cli/watch.go`) writes one document per call —
`json.NewEncoder(d.Out).Encode(watch.BuildJSON(d.LC.Name, v))` — and
`watchStream` calls it when `refused || (ready && dirty && (last.IsZero() ||
time.Since(last) >= gap))`. A refusal is the one case that bypasses the
readiness, change and gap tests and is emitted unconditionally (§1.5); every
other document waits for them. Under `--json` the gap is `watchJSONGap`, one
second, whatever `--interval` says: `--interval` sets the supervisor's polling
period (`watch.Options.Interval`), not the document rate.

`dirty` is set by a `put`/`patch` event on any of the four streams, by
`UpdDay`, by `UpdPolling`, by a change in the queued/finished rows read from
the runs bucket, and by a change in the connection kind (`v.Conn.Kind !=
lastConn`). `UpdTick` alone never sets it.

Three consequences a consumer must hold to:

- **A document is a sampled whole, not a delta.** Two events inside one second
  produce one document carrying the complete current state. The console renders
  the newest document and never accumulates; nothing is lost by dropping an
  older one.
- **Silence means "live and unchanged", not "the feed is dead".** An idle
  project emits nothing at all. The console's own clock, plus `now`,
  `connection.state` and `connection.age_seconds` from the last document, is
  what tells it the data is old.
- **A connection-state change is announced.** When a stream goes quiet past
  `watch.StaleAfter` (45 s) or fails, the next tick builds a view whose
  `v.Conn.Kind` differs from `lastConn`, which sets `dirty` and emits — so the
  console is *told* the data went `stale` or `offline` instead of inferring it.

### 1.2 The first document arrives within `watchFirstFrame`

`ready` is `seen == [4]bool{true, true, true, true} || time.Since(started) >=
watchFirstFrame` (5 s): all four streams have delivered a `put` of `"/"`, or
five seconds have passed. The supervisor's first update is `UpdDay`
(`Supervisor.run`, `internal/watch/source.go`), which sets `dirty` immediately,
so a child that can read nothing at all still writes one frame within about
five seconds — the "offline frame" `watchFirstFrame`'s own comment names. The
console may expect a first document within ~5 s of spawning and must not need
one sooner.

### 1.3 A slow reader makes the child block; it never drops a document

The pipe is not a queue with a policy: it is a queue with a **stall**. A full
pipe blocks `json.NewEncoder(d.Out).Encode`, `watchStream` stops draining
`Supervisor.Updates()`, and `Supervisor.emit`'s `out` channel — `make(chan
Update, 64)`, `watch.Start` — fills; the supervisor's own loop then blocks in
`Supervisor.emit`, and behind it the per-stream goroutines block feeding
`streamPhase`'s unbuffered `in` channel. Nothing is
discarded, and nothing grows without bound: the only queues are `watch.Start`'s
64 slots, `rtdb.Client.Stream`'s 16 (`internal/rtdb/stream.go`), and the
kernel's pipe buffer.

A stall longer than `rtdb.Client`'s `c.idle` timeout (90 s) reaches the
database. `streamOnce` resets that idle timer only as it consumes lines, so a
blocked consumer stops feeding the timer, the connection is cancelled, the
stream ends with an `error` event, `runStream` backs off (`backMin`, 1 s,
doubling to `backMax`, 30 s) and reconnects — and **a reconnection starts with
a `put` of `"/"` holding the whole current value**, so a change made while the
stream was down is not lost. The cost of a slow reader is latency and
reconnects, not lost data. The console must still drain the pipe promptly, and
its stalled-reader budget is measured in seconds, not minutes.

### 1.4 A closed stdout ends the child

The console's own death closes the pipe. The child's next write to file
descriptor 1 raises `SIGPIPE`, and Go's runtime does not ignore it for
descriptors 1 and 2: the default disposition kills the process, which exits
with status 141 (`128 + 13`). So the child does not linger when the console
goes away and does not need to be killed — the console still reaps it, and must
not report 141 as a CLI error. If the write instead returns an error (a stdout
that is not a pipe), `watchStream` returns that error as it came — it is not
wrapped with `remote()` — and `ExitCode` maps an error that is not an
`*ExitError` to `ExitUserError`: `main` prints `fugaro: …` to stderr and exits
1. Exit 2 is not reachable from this path, so a console reading the status sees
either 0, 1 or 141.

### 1.5 A refused read is terminal, and it is announced exactly once

`State.conn` (`internal/watch/state.go`) maps a permission error to
`ConnRefused`. `watchStream`'s `refused` test emits that document **before** the
`ready`/`dirty`/gap test — unconditionally — and then returns
`dbErr(d.LC, rtdb.ErrPermission, false)` (`internal/cli/budget.go`). `main`
prints `fugaro: project <name>'s budget database refused the read: you need the
Viewer role …` to stderr and exits with `ExitUserError` (1).

There is no in-child retry after a refusal: `streamPhase` returns `phaseHalt`
and `haltPhase` keeps only the clock. **Restarting the child before the
identity or the role changes refuses again in the same way**, so the console
shows the CLI's own sentence and stops rather than looping (design §6.3's last
row). `--once` differs in shape and not in outcome: it emits no document at all
and fails with the same sentence.

### 1.6 A dead or withdrawn stream is not an exit

A non-permission failure does not end the child. Three consecutive `error`
events with the same cause (`cause`) make `streamPhase` call `probe` and fall
back to `pollPhase`, which re-reads the four paths every `Options.Interval`
(5 s by default) and tries the stream again every `SSERetry` (60 s) through
`phaseTrial`. A `cancel` event — the server withdrawing the listen — and a
permission error both end the stream phase in `phaseHalt`, which keeps only the
clock running: **the child stays alive, and its view stays `offline`
(`State.conn`'s `otherErr` branch) or `refused`, indefinitely.** The console
must not wait for an exit to learn the feed is dead; it reads
`connection.state` and ages the data itself.

### 1.7 UTC midnight is a gap, not a zero

`streamPhase`'s tick compares `budget.Day(s.now())` (`internal/budget/model.go`)
with the day it is streaming. When it changes, it cancels the day pair and
waits for both streams to close (`dayWG.Wait()`) **before** opening the new
day's pair (`openDay`), and `rollDay` emits `UpdDay` in between. `State.Handle`'s
`UpdDay` case calls `SetDay`, which empties both spend trees (`rtdb.Tree.Reset`)
and the burn windows; `watchStream` treats `UpdDay` as dirty.

So the rollover emits a document whose `day` is the new UTC date and whose
counters are all zero — `total.counted_usd` 0, every repository's
`counted_usd` 0, `burn.known` false — **a document that is not a measurement**,
and the day's real numbers follow once the new pair delivers its `put` of
`"/"`. The repository list is rebuilt from `/agents` and the kill switches, so
a repository known only from `/spend/<day>/repos` disappears from `repos` until
then. A consumer must read a changed `day` as the design's *gap* state and
break the series there, and must not draw the zero. The burn rate stays unknown
for `BurnWarmup` (60 s) after the rollover, because `SetDay` restarted the
window.

### 1.8 The degraded mode is a second shape, and one of its lines is not JSON

When the selected installation has no `budget.rtdb_url`, `runWatch` calls
`runWatchDegraded` instead. Its document is `watchDegradedDoc` — `lsDoc` plus
`degraded` and `notice` — with no `connection`, no caps, no burn and no kill
state, and its run list is bounded by `degradedSince` (24 hours). It is emitted
every `watchPlainInterval` (10 s) or `--interval`, unconditionally, whether or
not anything changed.

Its error path writes a plain sentence to **stdout**, on the same stream as the
documents: `fmt.Fprintf(out, "%s\nrun records unavailable (retrying): %s\n",
degradedNotice, safetext.Strip(err.Error()))`, where `degradedNotice` is `no
budget backend: showing run records only`. In `--json` mode too — `clear` is
false there, so no ANSI escapes are written. The reader's rule is therefore not
a precaution but a requirement: **skip any line that is not a JSON object**,
surface it as a header warning, and never treat it as a parse error or a crash.
Everything else human — `printProjectHeader`'s `project: <name> (GCP <id>)`,
the update nag, error lines — goes to stderr, which is not the document stream.

### 1.9 The thresholds, for a consumer that is not a terminal

| Constant | Value | What it means to the console |
|---|---|---|
| `watchJSONGap` | 1 s | The floor between two documents. A ceiling on the feed's rate, never a promise of one. |
| `watchFirstFrame` | 5 s | The longest wait for the four streams before the first document, readable or not (§1.2). |
| `watchPlainInterval` | 10 s | The degraded mode's period, and the default `--interval` for plain frames. |
| `degradedSince` | 24 h | How far back the degraded document's run list reaches. Not a cap and not a total. |
| `watch.StaleAfter` | 45 s | Per-stream silence, keep-alives counting as events, after which `connection.state` is `stale`. The console's own stale banner (design §6.3) is the same number, and the child announces the transition. |
| `watch.SilentAfter` | 60 s | Per-run silence after which a run's `health` is `silent` (`internal/watch/build.go`). A property of the run, not of the connection. |
| `watch.LostAfter` | 180 s | The same, for `lost`. |
| `watch.BurnWindow` / `BurnWarmup` | 5 min / 60 s | The burn rate's window and the span it needs before `burn.known` is true. A restarted window — a reconnect, a rollover — reports `known: false`, not a rate of zero. |
| `watch.Options.Tick` | 1 s | The supervisor's clock: how quickly a stale, offline or day-rolled view becomes visible. |
| `watch.Options.Interval` | 5 s | The polling period, and `--interval` when given. |
| `watch.Options.SSERetry` | 60 s | How often polling tries the stream again. |
| `rtdb.Client` idle | 90 s | The SSE silence that ends a connection (§1.3). |
| `backMin` / `backMax` | 1 s / 30 s | The stream's reconnect backoff, doubling between them. |

### 1.10 How many documents arrive per minute, in each mode

| Mode | Rate | Notes |
|---|---|---|
| Live SSE (`watch --json`) | at most 60/min, and as few as **0** | One per change, floored at 1 s. An idle, live project is silent; a connection-state change always emits (§1.1). The first document lands within ~5 s (§1.2). |
| Polling (`--poll`, or after three same-cause stream errors) | one per poll period: 12/min at the 5 s default | Each poll delivers a full tree per path (`pollOnce`), which sets `dirty`; the 1 s JSON floor never binds. |
| Degraded (no `budget.rtdb_url`) | one per `watchPlainInterval`: 6/min at the 10 s default | Unconditional, changed or not, forever (§1.8). |
| `--once` | exactly one, then exit | A bounded read by plain GETs (`watchOnce`), not a stream. |

### 1.11 Does option A need a supervisor process? Yes

The console owns the child's lifecycle, and four facts above are why:

1. **The child exits on a refusal** (§1.5), after one document. Something has to
   decide not to restart it, and to put the CLI's own sentence on screen.
2. **The child does not exit on a dead stream** (§1.6). A hung, offline child is
   indistinguishable from a working one by its liveness alone, so the console
   needs its own staleness clock — `connection.state` and `age_seconds` from
   the documents it has, against its own time.
3. **The child dies by `SIGPIPE` when the console's pipe closes** (§1.4), so
   shutdown is the console's business: close the pipe, reap the process, and do
   not read 141 as an error.
4. **A stalled reader is a real failure mode, not a hypothetical one** (§1.3):
   the console must drain stdout continuously, or the feed reconnects and the
   numbers go stale for reasons of the console's own making.

One child, supervised: one `watch --json` per selected installation (D13), with
`--once` reserved for bounded reads. Nothing here needs several `watch`
children — the documents already carry the whole project — so the supervisor
supervises one process and restarts it with backoff, never on a refusal.

### 1.12 The reader rules that follow

1. Split stdout on newlines and decode each line as one JSON object; **skip a
   line that is not** (§1.8).
2. Render the newest document; never accumulate deltas (§1.1).
3. Silence is not death: use `now`, `connection.state`, `connection.age_seconds`
   and the console's own clock (§1.1, §1.6).
4. A changed `day` is a break in the series, never a zero (§1.7).
5. A missing field is `unavailable`, never `0` (design §6.1 rule 1).
6. `--repo` narrows the document: `WatchDeps.emit` calls `watch.FilterRepo`, so
   `repos` carries the filtered repository alone and `len(repos)` is not the
   project's repository count while the Live screen is filtered.
7. Expect the child to end — on a refusal, or on `SIGPIPE` — and never loop on
   a refusal (§1.4, §1.5).
8. Never `--follow`, and never `--once` for the live feed: the allow-list is
   design §5.1's table, and the console reads a bounded window or a stream.
