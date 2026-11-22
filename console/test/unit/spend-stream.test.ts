// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Spend screen's two streamed reads (console-improvements plan RS5; D2906,
// O3, RS1's line shape, RS2's `readStream`).
//
// The screen makes two reads and RS5 gives both of them the shape RS2 built for
// the Coordinator's jobs: `report --stream --json` and `budget show --stream
// --json` where the operation can be asked, the whole document where it cannot.
// Four facts are this suite's subject, and each is a rule of the plan rather
// than a preference:
//
//   - **the rows are drawn as they arrive, in the CLI's order.** `readStream`
//     hands each chunk to `onRows`, this screen builds a model from the rows read
//     so far, and the `end` line is what carries the totals and completes the
//     section. A model built from a feed also says in its own words what the feed
//     does not carry (`SPEND_STREAM_ABSENT`, `CAPS_STREAM_ABSENT`): the feed is
//     the rows, and the document's header is not a row of it.
//   - **a missing `end` is an interrupted read** with R1's sentence and a Retry,
//     and **never** the rows that happened to arrive drawn as the list.
//   - **a CLI that cannot be asked at all falls back to the whole document** --
//     read through this screen's own reader, header and all -- and draws nothing
//     as it arrives, because there are no lines arriving.
//   - **the reduced mode withholds both views** (`hidesView`): the read is
//     answered without asking either operation, so the console never spawns the
//     read whose figures it may not draw. This is the case the plan's red step
//     names, and the one a stream path that ignored the gate would fail.
//
// The fake bridge is built here from `new-ops.ts`'s types (`newOpsFrom`), the
// way every consumer of C-OPS does: no case imports the stub, and the two
// documents are the shapes `report --json` and `budget show --json` write
// (`console/docs/json-contract.md` sections 3 and 4), written small.

import { describe, expect, it } from 'vitest'

import { newOpsFrom } from '../../src/renderer/new-bridge'
import {
  CAPS_STREAM_ABSENT,
  CAPS_STREAM_HIDDEN,
  SPEND_STREAM_ABSENT,
  SPEND_STREAM_HIDDEN,
  capsFromFeed,
  readCapsStream,
  readReportStream,
} from '../../src/renderer/screens/spend'
import type { SpendControls, SpendModel } from '../../src/renderer/screens/spend'
import { STREAM_INTERRUPTED_WORD } from '../../src/renderer/stream'
import { drawnRows } from '../support/spend'

/** CONTROLS is the view the screen opens on (7d, by day, the person knob off):
 * the arguments `spendArguments` builds out of it are what the stream is asked
 * with. */
const CONTROLS: SpendControls = { since: '7d', until: null, by: 'day', persons: false }

/** A_DAY is one row of a day-grouped `report --json`, written the way the
 * contract writes it: the `*_micros` integer and the `*_usd` string are the same
 * figure. */
function aDay(key: string, modelMicros: number): Record<string, unknown> {
  return {
    key,
    start: key,
    end: key,
    partial: false,
    model_micros: modelMicros,
    model_usd: (modelMicros / 1_000_000).toFixed(6),
    notional_micros: 0,
    notional_usd: '0.000000',
    compute_micros: 0,
    compute_usd: '0.000000',
    run_count: 1,
    counted_runs: 1,
  }
}

/** ROW_LINES are three days of a report feed, in the order `report` writes them
 * (oldest first), as the `row` lines RS1 wraps a document's rows in. */
const ROW_LINES = [
  { type: 'row', row: aDay('2026-10-12', 1_000_000) },
  { type: 'row', row: aDay('2026-10-13', 2_000_000) },
  { type: 'row', row: aDay('2026-10-14', 3_000_000) },
]

/** TOTALS is the totals block the feed's `end` line carries: the report's own
 * totals, which is the one part of the document's header the feed does carry
 * (`ee/report/cli/report_stream.go`). */
const TOTALS: Record<string, unknown> = {
  model_micros: 6_000_000,
  model_usd: '6.000000',
  notional_micros: 0,
  notional_usd: '0.000000',
  compute_micros: 0,
  compute_usd: '0.000000',
  run_count: 3,
  counted_runs: 3,
}

/** END_LINE is the feed's own statement that it finished. */
const END_LINE: Record<string, unknown> = { type: 'end', version: 1, count: 3, totals: TOTALS }

/** WHOLE_REPORT is the whole `report --json` document: the rows under the header
 * the feed does not carry -- the window the CLI resolved, the history it read
 * from and the warnings it wrote. */
const WHOLE_REPORT: Record<string, unknown> = {
  project: 'acme',
  by: 'day',
  source: 'history',
  since: '2026-10-12',
  until: '2026-10-14',
  rows: ROW_LINES.map((line) => line.row),
  totals: TOTALS,
  warnings: ['the history holds a window and days outside it are pruned'],
}

/** REPORT_ROW is one repository's row of a caps feed: the members
 * `internal/cli/budget_stream.go` writes, one element of the document's `repos`
 * array unchanged. */
const REPO_ROW: Record<string, unknown> = {
  slug: 'github/acme/app',
  repo: 'acme/app',
  daily_cap_usd: 25,
  daily_cap_source: 'repo',
  per_run_cap_usd: 4,
  per_run_cap_source: 'repo',
  counted_usd: 3.5,
  spent_usd: 1.25,
  notional_usd: 0.5,
  leased_usd: 2.25,
  headroom_usd: 21.5,
  kill: null,
  running: [],
}

/** KILL_ROW is the project's own kill switch as the feed writes it: the row
 * whose slug is the literal `kill`, with `kill: null` (not absent) when the
 * switch is off. */
const KILL_ROW: Record<string, unknown> = {
  slug: 'kill',
  kill: { by: 'dana@example.com', at: '2026-11-09T11:00:00Z', reason: "the day's spend reached the cap" },
}

/** ROW is one `row` line of a feed: RS1 wraps a document's own row in the line
 * (`{"type":"row","row":{...}}`), because NDJSON has no document to hang an array
 * off and the line is what a reader reads the type of. A row handed to a feed
 * unwrapped is a line with no `type` and one no CLI wrote. */
function aRowLine(row: Record<string, unknown>): Record<string, unknown> {
  return { type: 'row', row }
}

/** CAPS_END is that feed's `end` line: a caps feed annotates it with a count and
 * a version and no totals (`budget show`'s document has none to carry). */
const CAPS_END: Record<string, unknown> = { type: 'end', version: 1, count: 2 }

/** CAPS_LINES is the caps feed itself: each repository's row in the order
 * `budget show --stream` writes it, then the project's kill row in place of the
 * document's `global` block (`budgetKillRow`, `internal/cli/budget_stream.go`). */
const CAPS_LINES = [aRowLine(REPO_ROW), aRowLine(KILL_ROW)]

/** WHOLE_CAPS is the whole `budget show --json` document: the repositories under
 * the project's own figures, the defaults, the mode and the reserve -- every
 * member the feed does not carry. */
const WHOLE_CAPS: Record<string, unknown> = {
  project: 'acme',
  mode: 'enforce',
  day: '2026-11-09',
  max_reserve_usd: 95.5,
  global: { daily_cap_usd: 100, per_run_cap_usd: 20, kill: null },
  defaults: { repo_daily_cap_usd: null, repo_per_run_cap_usd: null },
  repos: [REPO_ROW],
  warnings: [],
}

/** synchronous is the paint the cases drive by hand: the chunk is drawn in the
 * same turn, so the order of the chunks is the order of the recorded drawings. */
const synchronous = (step: () => void): void => {
  step()
}

/** Counters is how many times each operation of a fake bridge was asked: one
 * member per operation and per shape of the read, so a case that says a read was
 * **not** made reads it here rather than inferring it from a sentence. The
 * stream and the whole document are counted apart on purpose -- the fallback is
 * the one read a fallback case must see, and a single counter would not tell the
 * two apart. */
interface Counters {
  reportStreams: number
  reportDocs: number
  capsStreams: number
  capsDocs: number
}

/** aBridge is a bridge that can ask both feeds, over the lines given, and counts
 * what was asked. */
function aBridge(
  reportLines: readonly Record<string, unknown>[] | null,
  capsLines: readonly Record<string, unknown>[] | null,
) {
  const counts: Counters = { reportStreams: 0, reportDocs: 0, capsStreams: 0, capsDocs: 0 }
  const bridge = newOpsFrom({
    streamSpend: () => {
      counts.reportStreams += 1
      // No lines at all is a bridge that cannot ask: `newOpsFrom`'s own
      // `not-available` is what a console with no `spend.stream` handler
      // answers, and the read falls back to the whole document.
      if (reportLines === null) return Promise.resolve({ ok: false, reason: 'no handler' })
      return Promise.resolve({ ok: true, data: { document: { lines: [...reportLines] } } })
    },
    streamBudget: () => {
      counts.capsStreams += 1
      if (capsLines === null) return Promise.resolve({ ok: false, reason: 'no handler' })
      return Promise.resolve({ ok: true, data: { document: { lines: [...capsLines] } } })
    },
  })
  return { bridge, counts }
}

/** theReport is the whole-document read of the report: the screen's own reader,
 * which throws a `SourceRefusal` with the CLI's sentence when the command
 * refused. */
function theReport(
  counts: Counters,
  document: Record<string, unknown> = WHOLE_REPORT,
  refusal: string | undefined = undefined,
) {
  return () => {
    counts.reportDocs += 1
    if (refusal !== undefined) return Promise.reject({ sentence: refusal })
    return Promise.resolve(document)
  }
}

/** theCaps is the whole-document read of the caps, on `theReport`'s terms. */
function theCaps(
  counts: Counters,
  document: Record<string, unknown> = WHOLE_CAPS,
  refusal: string | undefined = undefined,
) {
  return () => {
    counts.capsDocs += 1
    if (refusal !== undefined) return Promise.reject({ sentence: refusal })
    return Promise.resolve(document)
  }
}

/** theKeys is the days each model drew, in the order the screen draws them: the
 * rows behind the chart's groups and the rows outside the range, which is what
 * "the rows appear as lines arrive" means for this screen. */
function theKeys(models: readonly SpendModel[]): string[][] {
  return models.map((model) => drawnRows(model).map((row) => row.key))
}

describe('a report feed that finishes', () => {
  it('draws the rows as they arrive, in the CLI’s order, and the end line carries the totals', async () => {
    const { bridge, counts } = aBridge([...ROW_LINES, END_LINE], null)
    const chunks: SpendModel[] = []
    const outcome = await readReportStream({
      stream: (args) => bridge.streamSpend(args),
      report: theReport(counts),
      controls: CONTROLS,
      caps: null,
      project: 'acme',
      withheld: false,
      onRows: (model) => chunks.push(model),
      paint: synchronous,
    })
    // One chunk per row, each holding the rows read so far: the table is drawn a
    // line at a time and never from a list the console rebuilt.
    expect(theKeys(chunks)).toEqual([
      ['2026-10-12'],
      ['2026-10-12', '2026-10-13'],
      ['2026-10-12', '2026-10-13', '2026-10-14'],
    ])
    expect(outcome.status).toBe('complete')
    expect(outcome.source).toBe('stream')
    if (outcome.status !== 'complete') throw new Error('the feed did not complete')
    expect(drawnRows(outcome.model).map((row) => row.key)).toEqual([
      '2026-10-12',
      '2026-10-13',
      '2026-10-14',
    ])
    // The totals are the `end` line's, and they are the last thing to arrive:
    // a chunk drawn before it carries none of them rather than a zero.
    expect(outcome.model.totals.model.value).toBe(6)
    expect(chunks[chunks.length - 1]?.totals.model.state).toBe('unavailable')
    expect(chunks.length).toBe(ROW_LINES.length)
    // The whole-document read was never made: the feed answered.
    expect(counts.reportDocs).toBe(0)
    expect(counts.reportStreams).toBe(1)
  })

  it('says in its own words what the feed does not carry', async () => {
    const { bridge, counts } = aBridge([...ROW_LINES, END_LINE], null)
    const outcome = await readReportStream({
      stream: (args) => bridge.streamSpend(args),
      report: theReport(counts),
      controls: CONTROLS,
      caps: null,
      project: 'acme',
      withheld: false,
      paint: synchronous,
    })
    if (outcome.status !== 'complete') throw new Error('the feed did not complete')
    // The header is not a row of the feed, and the screen says so rather than
    // drawing a chart with no window under it.
    expect(outcome.model.notes).toContain(SPEND_STREAM_ABSENT)
    expect(outcome.model.range.since).toBeNull()
    expect(outcome.model.source).toBeNull()
  })
})

describe('a report feed that stops without an end', () => {
  it('is an interrupted read with R1’s sentence, and never the rows that arrived', async () => {
    const { bridge, counts } = aBridge([...ROW_LINES], null)
    const outcome = await readReportStream({
      stream: (args) => bridge.streamSpend(args),
      report: theReport(counts),
      controls: CONTROLS,
      caps: null,
      project: 'acme',
      withheld: false,
      paint: synchronous,
    })
    expect(outcome.status).toBe('interrupted')
    expect(outcome.source).toBe('stream')
    if (outcome.status === 'complete') throw new Error('an interrupted feed answered a model')
    expect(outcome.sentence).toBe(STREAM_INTERRUPTED_WORD)
    // The whole document is **not** the answer to an interruption: the CLI was
    // asked and it stopped, and a second reader would be the console covering
    // for the read that failed (R1, `readStream`).
    expect(counts.reportDocs).toBe(0)
  })
})

describe('a CLI that cannot be asked for the feed', () => {
  it('reads the whole document, draws nothing as it arrives, and does not call it streamed', async () => {
    const { bridge, counts } = aBridge(null, null)
    const chunks: unknown[] = []
    const outcome = await readReportStream({
      stream: (args) => bridge.streamSpend(args),
      report: theReport(counts),
      controls: CONTROLS,
      caps: null,
      project: 'acme',
      withheld: false,
      onRows: (model) => chunks.push(model),
      paint: synchronous,
    })
    expect(outcome.status).toBe('complete')
    expect(outcome.source).toBe('whole')
    if (outcome.status !== 'complete') throw new Error('the document did not complete')
    // The document is drawn as the document it is: the window, the history and
    // the warnings are there, and the sentence about what a feed does not carry
    // is not a fact about this read.
    expect(outcome.model.range.since).toBe('2026-10-12')
    expect(outcome.model.source).toBe('history')
    expect(outcome.model.warnings).toEqual(WHOLE_REPORT['warnings'])
    expect(outcome.model.notes).not.toContain(SPEND_STREAM_ABSENT)
    // One answer, one drawing: nothing is handed to `onRows`.
    expect(chunks).toEqual([])
    expect(counts.reportDocs).toBe(1)
  })

  it('draws the whole document’s own refusal as the section’s sentence', async () => {
    const { bridge, counts } = aBridge(null, null)
    const outcome = await readReportStream({
      stream: (args) => bridge.streamSpend(args),
      report: theReport(counts, WHOLE_REPORT, 'no project is configured'),
      controls: CONTROLS,
      caps: null,
      project: 'acme',
      withheld: false,
      paint: synchronous,
    })
    expect(outcome.status).toBe('failed')
    expect(outcome.source).toBe('whole')
    if (outcome.status === 'complete') throw new Error('a refused document answered a model')
    expect(outcome.sentence).toBe('no project is configured')
  })
})

describe('the reduced mode', () => {
  it('withholds the report without asking either read', async () => {
    const { bridge, counts } = aBridge([...ROW_LINES, END_LINE], null)
    const chunks: unknown[] = []
    const outcome = await readReportStream({
      stream: (args) => bridge.streamSpend(args),
      report: theReport(counts),
      controls: CONTROLS,
      caps: null,
      project: 'acme',
      withheld: true,
      onRows: (model) => chunks.push(model),
      paint: synchronous,
    })
    expect(outcome.status).toBe('withheld')
    expect(outcome.source).toBe('none')
    if (outcome.status === 'complete') throw new Error('a withheld view answered a model')
    expect(outcome.sentence).toBe(SPEND_STREAM_HIDDEN)
    // **Neither read was made**: a mode that withholds the figures must not have
    // the console spawn the read whose answer it may not draw.
    expect(counts.reportStreams).toBe(0)
    expect(counts.reportDocs).toBe(0)
    expect(chunks).toEqual([])
  })
})

describe('a caps feed that finishes', () => {
  it('draws the repositories as they arrive and the kill row last', async () => {
    const { bridge, counts } = aBridge(null, [...CAPS_LINES, CAPS_END])
    const chunks: { slugs: string[]; killed: boolean }[] = []
    const outcome = await readCapsStream({
      stream: () => bridge.streamBudget({}),
      budget: theCaps(counts),
      withheld: false,
      onRows: (caps) =>
        chunks.push({ slugs: caps.repos.map((row) => row.slug), killed: caps.global !== null }),
      paint: synchronous,
    })
    // One chunk per row: the repository's, then the kill row's -- which is the
    // chunk the panel's global block appears in, because the kill row is a row
    // of the feed and not a member of its header.
    expect(chunks).toEqual([
      { slugs: ['github/acme/app'], killed: false },
      { slugs: ['github/acme/app'], killed: true },
    ])
    expect(outcome.status).toBe('complete')
    expect(outcome.source).toBe('stream')
    if (outcome.status !== 'complete') throw new Error('the feed did not complete')
    expect(outcome.caps.repos.map((row) => row.slug)).toEqual(['github/acme/app'])
    // The project's kill switch is the feed's own row (`budgetFeedRows`), and
    // the panel draws it in the global block the document keeps it in.
    expect(outcome.caps.global?.kill).not.toBeNull()
    expect(outcome.caps.note).toBe(CAPS_STREAM_ABSENT)
    // The one document member a reader could mistake for a value: an absent
    // `mode` is not the CLI's `""` ("unset"), and the panel says the read did not
    // carry it.
    expect(outcome.caps.mode).toBeNull()
    expect(outcome.caps.modeCarried).toBe(false)
    // The figures the feed has no member for are absences with a reason, never
    // `$0.00`.
    expect(outcome.caps.reserve.because).toBe(CAPS_STREAM_ABSENT)
    // The rail's spend and the gauge read the document's own figures, so a
    // streamed read answers no document at all.
    expect(outcome.document).toBeNull()
    expect(counts.capsStreams).toBe(1)
    expect(counts.capsDocs).toBe(0)
  })
})

describe('a caps feed that stops without an end', () => {
  it('is an interrupted read with R1’s sentence', async () => {
    const { bridge, counts } = aBridge(null, [...CAPS_LINES])
    const outcome = await readCapsStream({
      stream: () => bridge.streamBudget({}),
      budget: theCaps(counts),
      withheld: false,
      paint: synchronous,
    })
    expect(outcome.status).toBe('interrupted')
    if (outcome.status === 'complete') throw new Error('an interrupted feed answered a panel')
    expect(outcome.sentence).toBe(STREAM_INTERRUPTED_WORD)
    // The whole document is not the answer to an interruption, for the report's
    // reason: the CLI was asked and it stopped.
    expect(counts.capsStreams).toBe(1)
    expect(counts.capsDocs).toBe(0)
  })
})

describe('a CLI that cannot be asked for the caps feed', () => {
  it('reads the whole document and draws the panel from it, header and all', async () => {
    const { bridge, counts } = aBridge(null, null)
    const outcome = await readCapsStream({
      stream: () => bridge.streamBudget({}),
      budget: theCaps(counts),
      withheld: false,
      paint: synchronous,
    })
    expect(outcome.status).toBe('complete')
    expect(outcome.source).toBe('whole')
    if (outcome.status !== 'complete') throw new Error('the document did not complete')
    expect(outcome.caps.mode).toBe('enforce')
    expect(outcome.caps.modeCarried).toBe(true)
    expect(outcome.caps.note).toBeNull()
    // The document travels with the panel: the rail's spend and the gauge are
    // the whole document's figures, and this is the read that carries them.
    expect(outcome.document).not.toBeNull()
    expect(counts.capsStreams).toBe(1)
    expect(counts.capsDocs).toBe(1)
  })
})

describe('the reduced mode withholds the caps', () => {
  it('asks neither read and answers the console’s own word', async () => {
    const { bridge, counts } = aBridge(null, [...CAPS_LINES, CAPS_END])
    const outcome = await readCapsStream({
      stream: () => bridge.streamBudget({}),
      budget: theCaps(counts),
      withheld: true,
      paint: synchronous,
    })
    expect(outcome.status).toBe('withheld')
    expect(outcome.source).toBe('none')
    if (outcome.status === 'complete') throw new Error('a withheld view answered a panel')
    expect(outcome.sentence).toBe(CAPS_STREAM_HIDDEN)
    // Neither read was made, for the report's reason: a withheld view that read
    // the rows anyway would be spawning the read whose answer it may not draw.
    expect(counts.capsStreams).toBe(0)
    expect(counts.capsDocs).toBe(0)
  })
})

describe('capsFromFeed', () => {
  it('reads a feed with no kill row as an unknown switch rather than an off one', () => {
    // A feed that carried no `kill` row leaves the project's switch **unknown**,
    // which is not the same fact as off (`budgetKillRow`, `budget_stream.go`).
    const caps = capsFromFeed([REPO_ROW])
    expect(caps.global).toBeNull()
    expect(caps.repos.map((row) => row.slug)).toEqual(['github/acme/app'])
  })
})
