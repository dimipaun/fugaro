# Check 711: the wall time and size of every console read

Owner-run. Plan task P1 of [implementation-plan.md](implementation-plan.md) writes
[`scripts/console-read-timings.sh`](../../scripts/console-read-timings.sh), and this file says what to type and what
the output means. The reading is the first half of **D2906** — *measure first; add opt-in streaming to the slow
reads* — and what it decides is which commands besides `runs ls` get an opt-in `--stream` (**O3**, answered
2026-11-18: `report` and `budget show` now, further commands by amendment after this check).

## What the owner types

```console
$ bash scripts/console-read-timings.sh
```

That is the whole command when the CLI already knows which project this installation is (`fugaro.yaml`'s
`project:`, else `$FUGARO_PROJECT`, else the only project config on the machine). An installation the CLI cannot
resolve by itself is named the way the console's own reads are named:

```console
$ bash scripts/console-read-timings.sh --project <name>
```

Progress goes to stderr while it runs; the table and the ranking go to stdout, so the reading can be kept:

```console
$ bash scripts/console-read-timings.sh | tee /tmp/check-711-$(date +%F).txt
```

It reads only. Every read it spawns is one the console itself spawns, taken from the `SPAWN_COMMANDS` table in
`console/src/main/spawn.ts` together with the `--project` the console puts on that row, and the table's three
write rows (`settings.edit`, `roadmap.move`, `run.launch`) are skipped by name. Nothing is written outside a
temporary directory, no child gets a stdin, and nothing is confirmed or retried.

## What it does

Every read is run twice, in the table's own order: once **cold** (its first run in this pass — a fresh process,
whatever the CLI caches for itself cold) and once **warm** (immediately after). Cold is the number that matters
for "first loads show rows progressively"; warm is the number that matters for the spec's "revisiting a pane
renders in well under a second".

Every read is bounded, because a read that hangs is itself a finding: an ordinary one is killed at
`--limit-seconds` (90 by default, the `READ_TIMEOUT_MS` the console gives its own reads) and the two feeds
(`watch --json`, `graph --watch --json`) at `--watch-seconds` (5), the window their first line is waited for in.
Neither is a prompt and nothing waits for an answer: a read the bound ended is reported as that bound.

The reads that need a run id (`run.diagnose`, `run.logs`, `run.logLink`) answer a refusal unless the pass is given
one — see `--set` below. A refusal is a row of the table like any other, and it is printed as one.

## What the output means

```text
binary:       fugaro (dev)
installation: the CLI's own default (the checkout's fugaro.yaml project:, else $FUGARO_PROJECT, else the only project config)
reads run:    29 (cold, then warm, in the table's own order)

READ                                                          COLD      WARM      BYTES  NOTE
runs ls --json                                              1.842s    0.612s     184320
...
report --json                                              12.201s    1.104s    2204981
...

THREE SLOWEST BY COLD TIME (--top changes the count; ...)
  1. report --json                                           12.201s      2204981 bytes
  ...
```

- **READ** is the command line the console would spawn, spelled out (`--project <name>` appears when the pass was
  given one, and `--config <path>` when it was). It is the table's own words, never a paraphrase.
- **COLD** and **WARM** are seconds of wall time, measured around the spawn in the shell that runs it: what the
  person waiting for the pane waits for. `--` means the read never answered — the note beside it says which
  refusal it was, or that the bound ended it (and then the seconds shown are the bound, not a time).
- **BYTES** is how much the read wrote to stdout: the size of the document the pane's renderer has to parse before
  it can draw a row. A read that refuses shows what little it wrote rather than nothing.
- **NOTE** is empty for a read that answered; otherwise the CLI's own sentence (first line, cut to 200 characters)
  for a refusal, `first line, killed after Ns` for a feed, or the bound.
- The **ranking** is by cold time, the three slowest first by default. That is the answer to the check's question.

## What to do with the answer

1. **Name the streams.** A read whose cold time is seconds, and whose document the pane must parse whole before it
   can draw anything, is a candidate for the opt-in `--stream` D2906 describes. `runs ls` has it; O3 added
   `report` and `budget show`. A further command is added by amending the plan (RS1's pattern: NDJSON rows, an
   `end` line, `--json` unchanged without the flag), never by widening a read silently.
2. **Run it twice.** The plan's Checks table asks for the pass now and again once RB1, RS2 and PW1 have merged
   (the cache, the two streams and the Project pane). The second run is the evidence for the spec's *"revisiting a
   pane renders in well under a second; first loads show rows progressively"*, so keep both outputs — the pair is
   the before and after.
3. **Keep it with the release's evidence**, not in the repository: it is a fact about one installation on one
   machine (its network, its bucket, its number of runs), and a number pasted into a document would be stale the
   week after.

## Options

| Option | What it does |
| --- | --- |
| `--project <name>` | The installation to measure, on every row that takes `--project` |
| `--config <path>` | Passed after `--project`, for an installation whose config is not where the CLI looks |
| `--fugaro <path>` | The CLI to time, instead of `fugaro` on `PATH` |
| `--only <text>` | Runs the rows whose command contains the text (an operation name like `budget.show`, or words like `runs ls`), so one slow read can be looked at on its own |
| `--set "<words>"` | Appends words to every row — the run id the three run reads take as a positional argument: `--set <run-id>` (`run.diagnose`, `run.logs` and `run.logLink` otherwise answer a refusal, which is a row of the table like any other) |
| `--limit-seconds N` | How long an ordinary read may take before it is killed (default 90) |
| `--watch-seconds N` | How long a feed's first line is waited for (default 5) |
| `--top N` | How many of the slowest reads the ranking prints (default 3) |
| `--self-test` | Runs the whole pass against a stub `fugaro` this script writes, and fails unless the ranking it printed put the slowest stub first |

## What is tested, and what is not

`--self-test` is the script's own hermetic test, and the plan's command runs it: it builds a stub `fugaro` that
sleeps a declared time per command and prints a payload of a declared size, runs the whole pass against it, and
fails unless the slowest stub is ranked first, the ranking is sorted, every row has a time and a byte count, and
the biggest payload is ranked with the row that printed it. That is the part that can be pinned in a checkout, and
a ranking that is not sorted fails it.

It holds the read-only promise above to what the pass did, too. The stub logs every argv it is handed, and the
self-test fails if any of them is one of the table's three write rows' own argv (`settings`, `coordinator roadmap
move --json`, `run --task-file - --json`), or if a write row reached the pass as a read. Those three are written
out in the test rather than read from the script's own list of writes, because a check that took its expectation
from the list a mistake edits would pass whatever that list said. The timings themselves cannot be: they are facts about your installation,
which is why this document exists, and why the plan asks you to run it twice.
