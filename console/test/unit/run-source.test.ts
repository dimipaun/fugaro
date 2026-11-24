// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Run screen's credential sources, its fallback badges and its switch log
// (the model-credentials-routing plan's VS1; D3188, contract C1, CS8).
//
// The screen draws four things this file holds, and each is a clause of the
// task's acceptance rather than a preference:
//
//   1. **the source of each step, by kind and name** -- `stages[].source`, drawn
//      from the record and never from a table of kinds this console keeps, so a
//      sixth kind the CLI adds is drawn rather than refused;
//   2. **a fallback badge on a step and on the run** -- `stages[].fallback` on
//      the row and the same flags folded over the run in the status strip, which
//      is the only run-level flag there is: the record carries none, and
//      `cost.fallback_usd` is not one (it is 0 for a fallback a subscription
//      served, whose list price is notional and not money);
//   3. **the switch log, with each move's reason and its cache cost** -- the
//      record's `switches`, one row each, with an unmeasured cache miss drawn as
//      `n/a` rather than as the `$0.00` the wire carries beside the flag;
//   4. **the paid amount spent on fallback** -- `cost.fallback_usd` as its own
//      line in the ledger, a part of the model figure and never a term added to
//      it.
//
// **And the record written before any of it existed draws as it did.** The
// members are `omitempty` on the wire, so the fixture's third stage -- a stage
// no service-answered call was made from -- carries neither, and the same test
// is made again over a document with no new member at all: no source cell, no
// badge, no switch panel, no fallback line. A screen that drew `n/a` on every
// row of every historical run would be reporting the console's own reader rather
// than the run.
//
// The document is `test/fixtures/run-fallback.json`, invented from the structs
// (`internal/cli/diagnose.go`'s `Diagnosis` and `internal/runstore`'s
// `StageTiming`, `Switch` and `Cost`) because no recording carries a switch log:
// the fixture's own note in `fixture.test.ts` says what each case in it is for.
// The Live half of the task -- `auth` read as `-` draws no auth mode -- is the
// last describe, over the committed synthetic feed through the real decoder.

import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import { UNAVAILABLE_MARK, measured } from '../../src/shared/availability'
import { decodeSnapshot, authModeOf, NO_AUTH_MODE } from '../../src/shared/schema'
import type { LiveDocument } from '../../src/shared/schema'
import { loadFixture } from '../../src/source/fixture'
import type { JSONDoc } from '../../src/source/source'
import { BurnHistory, buildLiveModel, renderLive } from '../../src/renderer/screens/live'
import type { LiveFactory, ScreenElement } from '../../src/renderer/screens/live'
import { buildRunModel, renderRun } from '../../src/renderer/screens/run'
import type { RunFactory, RunInputs } from '../../src/renderer/screens/run'
import { SYNTHETIC_WHERE } from './synthetic'

const consoleDir = join(__dirname, '..', '..')
const FIXTURE = join(consoleDir, 'test', 'fixtures', 'run-fallback.json')

// ---------------------------------------------------------------------------
// The harness.
// ---------------------------------------------------------------------------

/** FakeScreen is the element a screen draws into, in this file's own words: it
 * records text, classes and attributes, and throws on the attributes a screen
 * may never set. */
class FakeScreen implements ScreenElement {
  readonly attributes = new Map<string, string>()
  readonly nodes: FakeScreen[] = []
  readonly classes: string[] = []
  textContent: string | null = null

  constructor(readonly tag: string) {}

  setAttribute(name: string, value: string): void {
    const lowered = name.toLowerCase()
    if (lowered === 'style' || lowered.startsWith('on')) {
      throw new Error(`a console screen may not set the ${name} attribute`)
    }
    if (lowered === 'class') {
      this.classes.length = 0
      this.classes.push(...value.split(/\s+/).filter((one) => one !== ''))
    }
    this.attributes.set(name, value)
  }

  getAttribute(name: string): string | null {
    return this.attributes.get(name) ?? null
  }

  removeAttribute(name: string): void {
    this.attributes.delete(name)
  }

  append(child: ScreenElement): void {
    this.nodes.push(child as FakeScreen)
  }

  childNodes(): readonly ScreenElement[] {
    return this.nodes
  }
}

const factory: RunFactory & LiveFactory = {
  create: (tag) => new FakeScreen(tag),
  createNS: (tag) => new FakeScreen(tag),
}

/** walk visits every node of a tree, the root first. */
function walk(node: ScreenElement): readonly FakeScreen[] {
  const fake = node as FakeScreen
  const out: FakeScreen[] = [fake]
  for (const child of fake.childNodes()) out.push(...walk(child))
  return out
}

/** all is every node carrying a class, in document order. */
function all(node: ScreenElement, className: string): readonly FakeScreen[] {
  return walk(node).filter((one) => one.classes.includes(className))
}

/** texts is every cell of a class, in document order. */
function texts(node: ScreenElement, className: string): readonly (string | null)[] {
  return all(node, className).map((one) => one.textContent)
}

/** theDocument is the committed fixture, parsed: the bytes a `diagnose RUN
 * --json` read would answer with. */
function theDocument(): unknown {
  return JSON.parse(readFileSync(FIXTURE, 'utf8')) as unknown
}

/** runInputs is the screen's inputs over one diagnosis document, with the log
 * window empty: this file is about the record's own panels. */
function runInputs(diagnosis: unknown): RunInputs {
  return { diagnosis, logs: [], logLink: null, sentences: {} }
}

/** theRunScreen is the fixture's document drawn, and the spend-by-stage panel
 * inside it. */
function theRunScreen(diagnosis: unknown = theDocument()): ScreenElement {
  return renderRun(factory, buildRunModel(runInputs(diagnosis)), 'light')
}

// ---------------------------------------------------------------------------
// 1. The source of each step, and 2. the fallback badges.
// ---------------------------------------------------------------------------

describe('the Run screen: the source of each step', () => {
  it('names the kind and the name of the source that served each stage', () => {
    const spend = all(theRunScreen(), 'run-spend-stages')[0]
    if (spend === undefined) throw new Error('the fixture draws no spend-by-stage panel')
    // Three stages, and the two cells are drawn per stage: the pool stage's kind
    // and name, the anthropic stage's kind alone -- the two kinds that are one
    // credential of the project carry an empty name, and an empty name is not an
    // absent one -- and nothing at all on the third, which the service answered
    // no call of. The kind is the record's own word and not a lookup in this
    // console's table.
    expect(texts(spend, 'run-source-kind')).toEqual(['pool', 'anthropic'])
    expect(texts(spend, 'run-source-name')).toEqual(['team'])
    // Each row carries the source cells of its own stage, so the third row --
    // the one with neither new member -- is the only row without one.
    const rows = all(spend, 'run-spend-stage')
    expect(rows).toHaveLength(3)
    expect(all(rows[0] as ScreenElement, 'run-spend-stage-source')).toHaveLength(1)
    expect(all(rows[1] as ScreenElement, 'run-spend-stage-source')).toHaveLength(1)
    expect(all(rows[2] as ScreenElement, 'run-spend-stage-source')).toHaveLength(0)
  })

  it('marks the stage that fell back, and the run that has one', () => {
    const tree = theRunScreen()
    const spend = all(tree, 'run-spend-stages')[0]
    if (spend === undefined) throw new Error('the fixture draws no spend-by-stage panel')
    // One stage of the three fell back, so one badge on one row -- and the run's
    // own badge in the status strip, which is the same flags folded over the run.
    expect(texts(spend, 'run-spend-stage-fallback')).toEqual(['fallback'])
    expect(texts(tree, 'run-fallback')).toEqual(['fallback'])
  })
})

// ---------------------------------------------------------------------------
// 3. The switch log.
// ---------------------------------------------------------------------------

describe('the Run screen: the credential switch log', () => {
  it('lists every move with its reason and what the move cost in cache', () => {
    const tree = theRunScreen()
    const switches = all(tree, 'run-switches')[0]
    if (switches === undefined) throw new Error('the fixture draws no switch panel')
    // The record's own order, which is the order the gateway settled them in.
    // The first move has no `from`: the call started on the credential that
    // answered it, which the service writes as an empty one and the console says
    // in words rather than drawing a blank.
    expect(texts(switches, 'run-switch-from')).toEqual([
      'started here',
      'team-shared',
      'alice',
      'team-shared',
    ])
    expect(texts(switches, 'run-switch-to')).toEqual(['team-shared', 'alice', 'team-shared', 'anthropic-key'])
    expect(texts(switches, 'run-switch-reason')).toEqual(['exhausted', 'broken', 'exhausted', 'not_admitted'])
    // The cache miss is an estimate charged to nothing, and the three shapes of
    // one are drawn three ways: a figure, the record's own measured nothing, and
    // **`n/a`** for the unmeasured tier -- where the wire's `0` is the absence of
    // a figure and not a figure, which is the one thing the row must not draw.
    expect(texts(switches, 'run-switch-cache')).toEqual(['$0.00', '$0.42', '$0.00', UNAVAILABLE_MARK])
    const unmeasured = all(switches, 'run-switch-cache')[3]
    expect(unmeasured?.getAttribute('title')).toBe(
      "the answering tier's cache behaviour is not measured: the re-read is unknown rather than free",
    )
  })

  it('draws no switch panel for a run that never switched', () => {
    const document = theDocument() as Record<string, unknown>
    const quiet = { ...document, switches: [] }
    expect(all(theRunScreen(quiet), 'run-switches')).toHaveLength(0)
  })
})

// ---------------------------------------------------------------------------
// 4. The paid amount spent on fallback.
// ---------------------------------------------------------------------------

describe('the Run screen: what the fallback calls cost', () => {
  it('draws the record’s own fallback figure as a line of the ledger, never added to it', () => {
    const tree = theRunScreen()
    const line = all(tree, 'run-ledger-fallback')[0]
    if (line === undefined) throw new Error('the fixture draws no fallback line')
    expect(texts(line, 'run-ledger-fallback-value')).toEqual(['$2.35'])
    // It is a part of the model figure and not a fourth term of the total: the
    // hero is the record's own `total_usd` and nothing on this screen sums the
    // two, so the figure above it is unchanged by the line beneath it.
    expect(texts(tree, 'run-ledger-total-value')).toEqual(['$4.41'])
  })
})

// ---------------------------------------------------------------------------
// A record written before the members existed.
// ---------------------------------------------------------------------------

describe('the Run screen: a record with none of the new members', () => {
  it('draws the same screen it drew before, and no mark for what is absent', () => {
    // The fixture's document with every new member removed, which is what a
    // record written by an older CLI is: no `stages[].source`, no
    // `stages[].fallback`, no `switches` and no `cost.fallback_usd`. Not one of
    // them is drawn as `n/a` -- an absent member is not a question this document
    // answered, and the console cannot tell a run that fell back from a record
    // that predates the member.
    const document = theDocument() as Record<string, unknown>
    const stages = (document['stages'] as readonly Record<string, unknown>[]).map((stage) => {
      const { source: _source, fallback: _fallback, ...rest } = stage
      return rest
    })
    const row = document['row'] as Record<string, unknown>
    const cost = { ...(row['cost'] as Record<string, unknown>) }
    delete cost['fallback_usd']
    const legacy: Record<string, unknown> = { ...document, row: { ...row, cost }, stages }
    delete legacy['switches']
    const tree = theRunScreen(legacy)
    expect(all(tree, 'run-source-kind')).toHaveLength(0)
    expect(all(tree, 'run-source-name')).toHaveLength(0)
    expect(all(tree, 'run-spend-stage-fallback')).toHaveLength(0)
    expect(all(tree, 'run-fallback')).toHaveLength(0)
    expect(all(tree, 'run-switches')).toHaveLength(0)
    expect(all(tree, 'run-ledger-fallback')).toHaveLength(0)
    // The panel that was there before is still there, with the same rows: the
    // absence removes cells and never a stage.
    const spend = all(tree, 'run-spend-stages')[0]
    if (spend === undefined) throw new Error('the legacy document draws no spend-by-stage panel')
    expect(texts(spend, 'run-spend-stage-name')).toEqual(['implement', 'review-1', 'writeback'])
    expect(texts(spend, 'run-spend-stage-usd')).toEqual(['$2.40', '$1.10', '$0.40'])
  })

  it('reads an absent fallback figure as unavailable rather than as zero', () => {
    // The one place the absence is a value: `fallbackMoneyOf` answers a reading
    // with no figure and a reason, so the ledger's own gate -- not the panel --
    // is what keeps the line off the screen. A `measured(0)` there would draw
    // `$0.00 on fallback` on every historical run.
    const document = theDocument() as Record<string, unknown>
    const row = document['row'] as Record<string, unknown>
    const cost = { ...(row['cost'] as Record<string, unknown>) }
    delete cost['fallback_usd']
    const model = buildRunModel(runInputs({ ...document, row: { ...row, cost } }))
    expect(model.ledger.fallback.value).toBeNull()
    expect(model.ledger.fallback.because).toBe('the record carries no row.cost.fallback_usd')
    // And the figure the record does carry is measured, not notional and not a
    // gap: it is money the run was billed.
    expect(buildRunModel(runInputs(theDocument())).ledger.fallback).toEqual(measured(2.35))
  })

  it('reads a document with no stages at all as a run with no fallback flag', () => {
    // The run's own flag is the stages folded over it, so a document with no
    // stages carries none -- and the switch log beside it is a top-level array
    // and is read all the same: the two are not derived from one another.
    const document = theDocument() as Record<string, unknown>
    const model = buildRunModel(runInputs({ ...document, stages: [] }))
    expect(model.fallback).toBe(false)
    expect(model.spendStages).toEqual([])
    expect(model.switches).toHaveLength(4)
  })

  it('keeps a measured zero and an absent key apart in the reading', () => {
    // The two are the same bytes on the wire only when the record says so: a
    // record that writes `fallback_usd: 0` is a run that spent nothing on
    // fallback, and it draws the line. This is the case the gate above must not
    // swallow.
    const document = theDocument() as Record<string, unknown>
    const row = document['row'] as Record<string, unknown>
    const cost = { ...(row['cost'] as Record<string, unknown>), fallback_usd: 0 }
    const tree = theRunScreen({ ...document, row: { ...row, cost } })
    expect(texts(tree, 'run-ledger-fallback-value')).toEqual(['$0.00'])
  })

  it('drops a fallback flag the record does not spell as true', () => {
    // `stages[].fallback` is a bool on the wire and this console reads only
    // `true`: a hand-edited record carrying the string `"true"`, or `1`, draws
    // no badge rather than a badge from a value the CLI never writes.
    const document = theDocument() as Record<string, unknown>
    const stages = (document['stages'] as readonly Record<string, unknown>[]).map((stage, at) =>
      at === 0 ? { ...stage, fallback: 'true' } : stage,
    )
    const tree = theRunScreen({ ...document, stages })
    expect(all(tree, 'run-spend-stage-fallback')).toHaveLength(0)
    expect(all(tree, 'run-fallback')).toHaveLength(0)
  })

  it('draws a source whose name is absent as the kind alone, and never as a value', () => {
    // A kind that names nothing (`anthropic`, `vertex`) writes an empty name,
    // and a hand-edited record can write a name that is not there at all: both
    // draw the kind and no name cell, which is the shape the fixture's second
    // stage already holds.
    const document = theDocument() as Record<string, unknown>
    const stages = (document['stages'] as readonly Record<string, unknown>[]).map((stage, at) =>
      at === 1 ? { ...stage, source: { kind: 'vertex' } } : stage,
    )
    const spend = all(theRunScreen({ ...document, stages }), 'run-spend-stages')[0]
    if (spend === undefined) throw new Error('the document draws no spend-by-stage panel')
    expect(texts(spend, 'run-source-kind')).toEqual(['pool', 'vertex'])
    expect(texts(spend, 'run-source-name')).toEqual(['team'])
  })

  it('draws a stage with no source object as a row with no source cell', () => {
    // `source` present but not an object -- a hand-edited record -- is read as
    // no source rather than as a source of blanks.
    const document = theDocument() as Record<string, unknown>
    const stages = (document['stages'] as readonly Record<string, unknown>[]).map((stage, at) =>
      at === 0 ? { ...stage, source: 'team' } : stage,
    )
    const spend = all(theRunScreen({ ...document, stages }), 'run-spend-stages')[0]
    if (spend === undefined) throw new Error('the document draws no spend-by-stage panel')
    expect(texts(spend, 'run-source-kind')).toEqual(['anthropic'])
  })
})

// ---------------------------------------------------------------------------
// The Live screen: `auth` read as `-`.
// ---------------------------------------------------------------------------

/** theFrames is the committed synthetic feed, decoded through the real decoder:
 * the same documents a replayed `watch --json` carries. */
function theFrames(): readonly LiveDocument[] {
  const loaded = loadFixture(readFileSync(SYNTHETIC_WHERE, 'utf8'), SYNTHETIC_WHERE)
  const out: LiveDocument[] = []
  for (const record of loaded.records) {
    if (!('document' in record)) continue
    const decoded = decodeSnapshot(record.document, record.at)
    if (!decoded.ok) throw new Error(`${record.at}: ${decoded.skew.because}`)
    out.push(decoded.value)
  }
  return out
}

/** theRawFrames is every document of the synthetic feed as it arrived,
 * undecoded, so a test can take a key out and put the document through the
 * decoder rather than around it. */
function theRawFrames(): readonly JSONDoc[] {
  const loaded = loadFixture(readFileSync(SYNTHETIC_WHERE, 'utf8'), SYNTHETIC_WHERE)
  const out: JSONDoc[] = []
  for (const record of loaded.records) {
    if ('document' in record) out.push(record.document)
  }
  return out
}

/** rowFor is the drawn row of one run, found by the id its door carries. */
function rowFor(tree: ScreenElement, run: string): FakeScreen {
  const found = all(tree, 'live-run').filter(
    (row) => all(row, 'live-run-id')[0]?.getAttribute('data-run-id') === run,
  )
  const row = found[0]
  if (row === undefined) throw new Error(`no row for ${run}`)
  return row
}

describe('the Live screen: a run with no auth mode', () => {
  it('answers no auth mode for the CLI’s own mark and for an empty value', () => {
    // The mark is a **value** the CLI writes, not a mode: `auth` is a required
    // string on the live run shape, so a run with nothing to say carries `-`.
    // The empty string is the second way the answer can arrive -- a reader that
    // did not have the key -- and it is the same answer.
    expect(authModeOf(NO_AUTH_MODE)).toBeNull()
    expect(authModeOf('')).toBeNull()
    expect(authModeOf('oauth')).toBe('oauth')
  })

  it('draws no auth cell on the row the feed marks with `-`', () => {
    const frame = theFrames().find((one) => one.runs.some((run) => run.auth === NO_AUTH_MODE))
    if (frame === undefined) throw new Error('the synthetic fixture has no run with `-` for its auth')
    const marked = frame.runs.find((run) => run.auth === NO_AUTH_MODE)
    const served = frame.runs.find((run) => run.auth !== NO_AUTH_MODE)
    if (marked === undefined || served === undefined) throw new Error('the frame is missing a run')
    const tree = renderLive(factory, buildLiveModel(frame, new BurnHistory()), 'light')
    // The row the CLI marked draws no auth cell at all -- not the `-`, and not
    // the console's `n/a` either, which would report a question the feed
    // answered -- while the run beside it that does carry a mode still draws it.
    expect(all(rowFor(tree, marked.run), 'live-run-auth')).toHaveLength(0)
    expect(texts(rowFor(tree, served.run), 'live-run-auth')).toEqual([served.auth])
  })

  it('is the only way the wire can say it: a live run with no auth key is refused', () => {
    // **Why the mark exists and why this console reads it.** `auth` is required
    // on the live run shape, so a feed that dropped the key would be a document
    // the decoder refuses rather than one that means "no auth mode" -- which is
    // what leaves `-` as the wire's one way to say it, and this screen's one
    // case to answer.
    const raw = theRawFrames().find(
      (one) => Array.isArray(one['runs']) && (one['runs'] as readonly unknown[]).length > 0,
    )
    if (raw === undefined) throw new Error('the synthetic fixture has no frame with runs')
    const runs = (raw['runs'] as readonly Record<string, unknown>[]).map(({ auth: _auth, ...rest }) => rest)
    expect(decodeSnapshot({ ...raw, runs }, 'a fixture frame without auth').ok).toBe(false)
  })
})
