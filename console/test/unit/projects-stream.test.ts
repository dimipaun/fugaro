// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Project tab's streamed caps read (console-improvements plan RS5; D2906,
// O3, RS1's line shape, RS2's `readStream`).
//
// The tab reads `budget show --stream --json` where the operation can be asked
// and the whole `budget show --json` document where it cannot, and this suite is
// about the four facts the plan names for it:
//
//   - **the caps table is drawn as the rows arrive**, in the CLI's order, and the
//     `end` line is what completes the section. The model behind the chunk is the
//     screen's own `budgetFromFeed`: one `row` line is one element of the
//     document's `repos` array and the project's kill switch is the row whose slug
//     is `kill` (`internal/cli/budget_stream.go`), which is where a chunk's
//     drawing gets its global block from.
//   - **a missing `end` is an interrupted read** with R1's sentence and a Retry,
//     and never the rows that arrived drawn as the list.
//   - **a CLI that cannot be asked falls back to the whole document** -- the read
//     that carries every member the panels draw -- and a feed-assembled document
//     is a different thing from that document, which is what `source` says.
//   - **the reduced mode withholds the view** (`hidesView`): the read is answered
//     without asking either operation, so the console never spawns the read whose
//     caps and kill figures it may not draw.
//
// The fake bridge is built here from `new-ops.ts`'s types (`newOpsFrom`), the way
// every consumer of C-OPS does; the fixture is `budget show --json`'s shape
// written small (`console/docs/json-contract.md` section 4).

import { describe, expect, it } from 'vitest'

import { newOpsFrom } from '../../src/renderer/new-bridge'
import {
  BUDGET_STREAM_SOURCE_LINE,
  budgetFromFeed,
  buildProjectsModel,
  readBudgetStream,
} from '../../src/renderer/screens/projects'
import { CAPS_STREAM_ABSENT, CAPS_STREAM_HIDDEN } from '../../src/renderer/screens/spend'
import { STREAM_INTERRUPTED_WORD } from '../../src/renderer/stream'
import type { JSONDoc } from '../../src/source/source'

/** REPO_ROW is one repository's row of a caps feed: one element of the
 * document's `repos` array, unchanged (`internal/cli/budget_stream.go`). */
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

/** SEVEN_ROW is a second repository, so a chunk can be told from the one before
 * it by more than its kill row. */
const SEVEN_ROW: Record<string, unknown> = { ...REPO_ROW, slug: 'github/acme/seven', repo: 'acme/seven' }

/** KILL_ROW is the project's own kill switch as the feed writes it: the row whose
 * slug is the literal `kill`, in place of the document's `global` block. */
const KILL_ROW: Record<string, unknown> = {
  slug: 'kill',
  kill: { by: 'dana@example.com', at: '2026-11-09T11:00:00Z', reason: "the day's spend reached the cap" },
}

/** aRowLine wraps a document's own row in the line RS1 writes it as: NDJSON has
 * no document to hang an array off, and the line is what a reader reads the type
 * of (`{"type":"row","row":{...}}`). */
function aRowLine(row: Record<string, unknown>): Record<string, unknown> {
  return { type: 'row', row }
}

/** CAPS_LINES is the feed: two repositories' rows, then the kill row. */
const CAPS_LINES = [aRowLine(REPO_ROW), aRowLine(SEVEN_ROW), aRowLine(KILL_ROW)]

/** CAPS_END is the feed's own statement that it finished: a caps feed annotates
 * it with a count and a version and no totals (`budget show`'s document has
 * none to carry). */
const CAPS_END: Record<string, unknown> = { type: 'end', version: 1, count: 3 }

/** WHOLE_CAPS is the whole `budget show --json` document: the members the feed
 * does not carry (the project, the mode, the day, the reserve, the global
 * block's own caps, the defaults, the warnings) under the same repository rows. */
const WHOLE_CAPS: Record<string, unknown> = {
  project: 'acme',
  mode: 'enforce',
  day: '2026-11-09',
  max_reserve_usd: 95.5,
  global: { daily_cap_usd: 100, per_run_cap_usd: 20, kill: null },
  defaults: { repo_daily_cap_usd: null, repo_per_run_cap_usd: null },
  repos: [REPO_ROW, SEVEN_ROW],
  warnings: [],
}

/** synchronous is the paint the cases drive by hand: the chunk is drawn in the
 * same turn, so the order of the chunks is the order of the recorded drawings. */
const synchronous = (step: () => void): void => {
  step()
}

/** Counters is how many times each shape of the read was asked, counted apart so
 * a case can say the stream was asked and the whole document was not. */
interface Counters {
  streams: number
  docs: number
}

/** aBridge is a bridge that can ask the caps feed over the lines given, and
 * counts what was asked. `null` lines is a bridge that cannot ask: `newOpsFrom`
 * answers its own `not-available`, which is what a console with no
 * `budget.stream` handler answers. */
function aBridge(lines: readonly Record<string, unknown>[] | null) {
  const counts: Counters = { streams: 0, docs: 0 }
  const bridge = newOpsFrom({
    streamBudget: () => {
      counts.streams += 1
      if (lines === null) return Promise.resolve({ ok: false, reason: 'no handler' })
      return Promise.resolve({ ok: true, data: { document: { lines: [...lines] } } })
    },
  })
  return { bridge, counts }
}

/** theDocument is the whole-document read: the screen's own reader, which throws
 * a `SourceRefusal` carrying the CLI's sentence when the command refused. */
function theDocument(
  counts: Counters,
  document: Record<string, unknown> = WHOLE_CAPS,
  refusal: string | undefined = undefined,
) {
  return () => {
    counts.docs += 1
    if (refusal !== undefined) return Promise.reject({ sentence: refusal })
    return Promise.resolve(document as JSONDoc)
  }
}

/** slugsOf is the repositories a document or a chunk carries, in their order. */
function slugsOf(document: JSONDoc): string[] {
  const repos = document['repos']
  if (!Array.isArray(repos)) return []
  return repos.map((row) => String((row as Record<string, unknown>)['slug']))
}

/** killedIn is whether a chunk carries the project's kill row: the row is the
 * feed's, and `budgetFromFeed` puts it in the document's own `global.kill`. */
function killedIn(document: JSONDoc): boolean {
  const global = document['global']
  return global !== null && typeof global === 'object' && 'kill' in (global as JSONDoc)
}

/** panelOf is one panel of a model by key. */
function panelOf(model: ReturnType<typeof buildProjectsModel>, key: string) {
  const panel = model.panels.find((candidate) => candidate.key === key)
  if (panel === undefined) throw new Error(`the model has no ${key} panel`)
  return panel
}

describe('a caps feed that finishes', () => {
  it('draws the cap rows as they arrive, in the CLI’s order, and the end line completes the section', async () => {
    const { bridge, counts } = aBridge([...CAPS_LINES, CAPS_END])
    const chunks: { slugs: string[]; killed: boolean }[] = []
    const outcome = await readBudgetStream({
      stream: () => bridge.streamBudget({}),
      budget: theDocument(counts),
      withheld: false,
      onRows: (document) => chunks.push({ slugs: slugsOf(document), killed: killedIn(document) }),
      paint: synchronous,
    })
    // One chunk per row, each holding the rows read so far: the caps table is
    // drawn a line at a time, and the project's kill row is the chunk the global
    // block appears in -- it is a row of the feed and not a member of its header.
    expect(chunks).toEqual([
      { slugs: ['github/acme/app'], killed: false },
      { slugs: ['github/acme/app', 'github/acme/seven'], killed: false },
      { slugs: ['github/acme/app', 'github/acme/seven'], killed: true },
    ])
    expect(outcome.status).toBe('complete')
    expect(outcome.source).toBe('stream')
    if (outcome.status !== 'complete') throw new Error('the feed did not complete')
    expect(slugsOf(outcome.document)).toEqual(['github/acme/app', 'github/acme/seven'])
    // The whole document was never read: the feed answered.
    expect(counts.streams).toBe(1)
    expect(counts.docs).toBe(0)
  })

  it('builds the tab’s model from the rows, and says what the feed does not carry', async () => {
    const { bridge, counts } = aBridge([...CAPS_LINES, CAPS_END])
    const outcome = await readBudgetStream({
      stream: () => bridge.streamBudget({}),
      budget: theDocument(counts),
      withheld: false,
      paint: synchronous,
    })
    if (outcome.status !== 'complete') throw new Error('the feed did not complete')
    const model = buildProjectsModel({
      budget: outcome.document,
      accounts: null,
      secrets: null,
      policy: null,
      doctor: null,
      streamed: true,
    })
    // The three panels the caps document fills name the read whose rows they
    // drew, which is the feed's command line and not the whole document's.
    for (const key of ['caps', 'kill', 'repositories']) {
      const panel = panelOf(model, key)
      expect(panel.state).toBe('read')
      expect(panel.source).toBe(BUDGET_STREAM_SOURCE_LINE)
    }
    // The rows arrived and so did the kill row: the caps list is the two
    // repositories plus the `global` row the kill row's own document block makes
    // (`buildProjectsModel` draws one row per block the document carries).
    expect(panelOf(model, 'caps').count?.value).toBe(3)
    // The rows arrived and so did the kill row: the switch is drawn from the
    // feed's own row.
    expect(model.kill.global).not.toBeNull()
    expect(model.kill.rows.map((row) => row.slug)).toEqual(['github/acme/app', 'github/acme/seven'])
    // The caps panel says beside the table what the feed did not carry: the
    // project's own rows, the defaults and the mode are not rows of the feed, and
    // a table drawn from it without this sentence would read as a project whose
    // global and default caps are unset.
    expect(panelOf(model, 'caps').because).toBe(CAPS_STREAM_ABSENT)
    // The largest single lease is a top-level member of the document and no row
    // of the feed: `n/a` with the console's reason, never the CLI's own "no
    // reserve limit" read off an absence.
    expect(model.maxReserve.state).toBe('unavailable')
    expect(model.maxReserve.because).toBe(CAPS_STREAM_ABSENT)
    // The members the feed does not carry at all are absences, not zeroes.
    expect(model.mode).toBeNull()
    expect(model.project).toBeNull()
  })
})

describe('a caps feed that stops without an end', () => {
  it('is an interrupted read with R1’s sentence, and never the rows that arrived', async () => {
    const { bridge, counts } = aBridge([...CAPS_LINES])
    const outcome = await readBudgetStream({
      stream: () => bridge.streamBudget({}),
      budget: theDocument(counts),
      withheld: false,
      paint: synchronous,
    })
    expect(outcome.status).toBe('interrupted')
    expect(outcome.source).toBe('stream')
    if (outcome.status === 'complete') throw new Error('an interrupted feed answered a document')
    expect(outcome.sentence).toBe(STREAM_INTERRUPTED_WORD)
    // The whole document is **not** the answer to an interruption: the CLI was
    // asked and it stopped, and a second reader would be the console covering
    // for the read that failed (R1, `readStream`).
    expect(counts.docs).toBe(0)
  })
})

describe('a CLI that cannot be asked for the feed', () => {
  it('reads the whole document and draws the panels from it, header and all', async () => {
    const { bridge, counts } = aBridge(null)
    const chunks: JSONDoc[] = []
    const outcome = await readBudgetStream({
      stream: () => bridge.streamBudget({}),
      budget: theDocument(counts),
      withheld: false,
      onRows: (document) => chunks.push(document),
      paint: synchronous,
    })
    expect(outcome.status).toBe('complete')
    expect(outcome.source).toBe('whole')
    if (outcome.status !== 'complete') throw new Error('the document did not complete')
    // The document itself travels, not a document reassembled from rows: it is
    // the read that carries the members the feed has no row for.
    expect(outcome.document).toBe(WHOLE_CAPS)
    // One answer, one drawing: nothing is handed to `onRows`, because there were
    // no lines arriving.
    expect(chunks).toEqual([])
    expect(counts.streams).toBe(1)
    expect(counts.docs).toBe(1)

    const model = buildProjectsModel({
      budget: outcome.document,
      accounts: null,
      secrets: null,
      policy: null,
      doctor: null,
    })
    const caps = panelOf(model, 'caps')
    expect(caps.source).toBe('fugaro budget show --json')
    expect(caps.because).toBeNull()
    expect(model.maxReserve.value).toBe(95.5)
    expect(model.mode).toBe('enforce')
  })

  it('draws the whole document’s own refusal as the section’s sentence', async () => {
    const { bridge, counts } = aBridge(null)
    const outcome = await readBudgetStream({
      stream: () => bridge.streamBudget({}),
      budget: theDocument(counts, WHOLE_CAPS, 'no project is configured'),
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
  it('withholds the caps without asking either read', async () => {
    const { bridge, counts } = aBridge([...CAPS_LINES, CAPS_END])
    const chunks: JSONDoc[] = []
    const outcome = await readBudgetStream({
      stream: () => bridge.streamBudget({}),
      budget: theDocument(counts),
      withheld: true,
      onRows: (document) => chunks.push(document),
      paint: synchronous,
    })
    expect(outcome.status).toBe('withheld')
    expect(outcome.source).toBe('none')
    if (outcome.status === 'complete') throw new Error('a withheld view answered a document')
    expect(outcome.sentence).toBe(CAPS_STREAM_HIDDEN)
    // **Neither read was made**: a mode that withholds the caps and kill figures
    // must not have the console spawn the read whose answer it may not draw.
    expect(counts.streams).toBe(0)
    expect(counts.docs).toBe(0)
    expect(chunks).toEqual([])
  })
})

describe('budgetFromFeed', () => {
  it('leaves the kill switch out of a feed that carried no kill row', () => {
    // A feed with no `kill` row leaves the project's switch **unknown**, which is
    // not the same fact as off (`budgetKillRow`, `budget_stream.go`): the
    // reassembled document has no `global` at all rather than a null kill.
    const document = budgetFromFeed([REPO_ROW])
    expect('global' in document).toBe(false)
    expect(slugsOf(document)).toEqual(['github/acme/app'])
  })
})
