// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// A paused run on the Live screen and on Run detail (model-credentials-routing
// VS3; contract-pause R15, D3185 and D3195).
//
// Four things are held here, and each is the task's own acceptance sentence:
//
//   1. **the paused run is drawn as paused, with its resume time.** Both
//      screens show the status word the record carries (`paused`) beside the
//      instant the orchestrator will resume the chain at (`resume_at`) -- the
//      Live screen in a section of its own, Run detail on the line under its
//      status strip, as `internal/watch/render.go` draws the same run in text
//      (`<PAUSED> <ending> <title> · expected resume <stamp> · resumed by
//      <ending>`);
//   2. **the link to the run that resumed it.** `resumed_by` is another run's
//      id, and each screen draws its ending with the whole id on the cell as
//      `data-run-id` -- the door `entry.ts`'s `wireRuns` and `showRun` open that
//      run from. A pause nobody has resumed yet draws the console's `n/a` and
//      carries **no** attribute, so a cell with no door is never mistaken for
//      one that failed to open;
//   3. **the status set gains `paused`, and an unknown word still draws.** The
//      two documents do not close the word, so the console's own table
//      (`RUN_STATUSES`) does: a status in it is drawn as itself and one outside
//      it is drawn as the console's `unknown`, never as the word a document
//      invented. The fixture's fourth finished row carries a status this build
//      has never been taught and must not fail the screen;
//   4. **the released console does not refuse the word** -- the task's own
//      condition for saying whether a `--json` format needs a version bump. The
//      last released reader is checked in verbatim at
//      `test/fixtures/released-console-status.ts` and is asked here, because a
//      rule imported from today's `schema.ts` would answer for today's reader
//      and prove nothing about the release (O8, F54).
//
// The Live half runs over `test/fixtures/paused-run.json`, the invented
// `watch --json` feed whose `finished[]` carries the four shapes this drawing
// turns on; the Run half runs over inline `diagnose RUN --json` documents, for
// the same reason `run.test.ts` does: the screen's row is the record's own, and
// a case that wants an absent field has to be able to write the record without
// it. `fixture.test.ts` says what each row of the feed is for.

import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import { UNAVAILABLE_MARK } from '../../src/shared/availability'
import {
  PAUSED_STATUS,
  RUN_STATUSES,
  UNKNOWN_STATUS_WORD,
  decodeSnapshot,
  runStatusOf,
} from '../../src/shared/schema'
import type { LiveDocument } from '../../src/shared/schema'
import {
  BurnHistory,
  LIVE_PAUSED_RESUME_WORD,
  LIVE_PAUSED_RESUMED_BY_WORD,
  LIVE_PAUSED_STATE,
  buildLiveModel,
  renderLive,
} from '../../src/renderer/screens/live'
import type { LiveFactory, ScreenElement } from '../../src/renderer/screens/live'
import { buildRunModel, renderRun } from '../../src/renderer/screens/run'
import type { RunFactory, RunInputs } from '../../src/renderer/screens/run'
import {
  RELEASED_WHERE,
  ReleasedRefusal,
  releasedFinishedStatus,
} from '../fixtures/released-console-status'

const consoleDir = join(__dirname, '..', '..')
const FIXTURE = join(consoleDir, 'test', 'fixtures', 'paused-run.json')

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

/** one is the single node carrying a class, asserted to be single. */
function one(node: ScreenElement, className: string): FakeScreen {
  const found = all(node, className)
  expect(found, `expected exactly one .${className}`).toHaveLength(1)
  const first = found[0]
  if (first === undefined) throw new Error(`no .${className}`)
  return first
}

/** texts is every cell of a class, in document order. */
function texts(node: ScreenElement, className: string): readonly (string | null)[] {
  return all(node, className).map((one) => one.textContent)
}

/** fixtureDocument is the committed feed's bytes, as `watch --json` wrote them.
 *
 * It is read as one JSON object and not through `src/source/fixture.ts`, which
 * is the header-and-digest reader for a **recorded** `.ndjson` feed: this
 * document is invented, for the reason `fixture.test.ts` gives, and the same
 * split `run-fallback.json` and `run-source.test.ts` make. */
function fixtureDocument(): unknown {
  return JSON.parse(readFileSync(FIXTURE, 'utf8')) as unknown
}

/** theFeed is the fixture through the real decoder, so the Live cases run over
 * a document the app itself would accept. */
function theFeed(): LiveDocument {
  const decoded = decodeSnapshot(fixtureDocument(), FIXTURE)
  if (!decoded.ok) throw new Error(`the fixture is refused: ${decoded.skew.because}`)
  return decoded.value
}

/** liveScreen is the fixture drawn, with a history of exactly that frame. */
function liveScreen(feed: LiveDocument = theFeed()): ScreenElement {
  return renderLive(factory, buildLiveModel(feed, new BurnHistory()), 'light')
}

/** runInputs is the Run screen's inputs over one diagnosis document, with the
 * log window empty: this file is about the record's own two facts. */
function runInputs(diagnosis: unknown): RunInputs {
  return { diagnosis, logs: [], logLink: null }
}

/** theRunScreen is a `diagnose RUN --json` document drawn. */
function theRunScreen(row: Record<string, unknown>): ScreenElement {
  return renderRun(factory, buildRunModel(runInputs({ row })), 'light')
}

/** TRACKED_ROW is the record every Run case starts from: a run that ended
 * `paused` and carries both of the pause's facts. */
const TRACKED_ROW: Readonly<Record<string, unknown>> = Object.freeze({
  run: '20261124-101500-9a3c',
  run_id: '20261124-101500-9a3c',
  repo: 'example-owner/example-repo',
  status: PAUSED_STATUS,
  resume_at: '2026-11-24T11:30:00Z',
  resumed_by: '20261124-113000-77b1',
})

// ---------------------------------------------------------------------------
// 1. The Live screen's paused section.
// ---------------------------------------------------------------------------

describe('the Live screen: the runs that suspended themselves', () => {
  it('draws one row per paused finished run, in the document’s own order', () => {
    const tree = liveScreen()
    // The feed carries four finished rows -- three paused and one whose status
    // this build has never been taught -- so three rows and no fourth.
    expect(texts(tree, 'live-paused-run-id')).toEqual(['9a3c', '4e77', '1f22'])
    expect(texts(tree, 'live-paused-run-title')).toEqual([
      'the console shows a paused run',
      'rotate the pool a project\'s second stage was served from',
      'carry the cascade\'s third source into the switch log',
    ])
    // The state word is the CLI's own upper-cased one and the same word on every
    // row: a run cannot be suspended two ways.
    expect(texts(tree, 'live-paused-state')).toEqual([
      LIVE_PAUSED_STATE,
      LIVE_PAUSED_STATE,
      LIVE_PAUSED_STATE,
    ])
    expect(LIVE_PAUSED_STATE, 'the Live row draws the state word §2 pins').toBe('PAUSED')
    expect(texts(tree, 'live-paused-resume-word')).toEqual([
      LIVE_PAUSED_RESUME_WORD,
      LIVE_PAUSED_RESUME_WORD,
      LIVE_PAUSED_RESUME_WORD,
    ])
    expect(texts(tree, 'live-paused-resume-at')).toEqual([
      '2026-11-24T11:30:00Z',
      '2026-11-24T13:05:00Z',
      UNAVAILABLE_MARK,
    ])
  })

  it('links the row to the run that resumed it, and to nothing when none has', () => {
    const tree = liveScreen()
    const rows = all(tree, 'live-paused-row')
    expect(rows).toHaveLength(3)

    // The first row is a pause a later run resumed: the ending a person types
    // back (`task.ShortRun`), the whole id on the cell as the door.
    const resumed = rows[0]
    if (resumed === undefined) throw new Error('no first row')
    expect(texts(resumed, 'live-paused-by-word')).toEqual([LIVE_PAUSED_RESUMED_BY_WORD])
    const byId = one(resumed, 'live-paused-by-id')
    expect(byId.textContent).toBe('77b1')
    expect(byId.getAttribute('data-run-id')).toBe('20261124-113000-77b1')
    // And the row itself opens the paused run, as every other row of the feed
    // does: one door per run, drawn beside the one to the run that brought it
    // back.
    const door = one(resumed, 'live-paused-run-id')
    expect(door.textContent).toBe('9a3c')
    expect(door.getAttribute('data-run-id')).toBe('20261124-101500-9a3c')

    // The second row's pause is still standing, and the third's listing could
    // not read the pause at all: both draw the console's mark, with the reason
    // in the title rather than a blank -- and neither carries a door.
    const standing = rows[1]
    const unreadable = rows[2]
    if (standing === undefined || unreadable === undefined) throw new Error('missing rows')
    for (const [row, field] of [
      [standing, 'finished[].resumed_by'],
      [unreadable, 'finished[].resumed_by'],
    ] as const) {
      const cell = one(row, 'live-paused-by-id')
      expect(cell.textContent).toBe(UNAVAILABLE_MARK)
      expect(cell.getAttribute('data-run-id')).toBeNull()
      expect(cell.getAttribute('title')).toBe(`the document carries no ${field}`)
    }
    // The two absences are told apart by what they are absences of: the third
    // row has no instant either, the second has one.
    expect(one(unreadable, 'live-paused-resume-at').getAttribute('title')).toBe(
      'the document carries no finished[].resume_at',
    )
  })

  it('draws no paused section at all when the feed has none', () => {
    const feed = theFeed()
    const without = {
      ...feed,
      finished: feed.finished.filter((row) => row.status !== PAUSED_STATUS),
    }
    const tree = liveScreen(without)
    expect(all(tree, 'live-paused')).toEqual([])
    expect(all(tree, 'live-paused-row')).toEqual([])
    // The inbox is still drawn: an empty paused section says nothing, but the
    // operator's own list is an answer a person came for.
    expect(all(tree, 'live-review')).toHaveLength(1)
  })

  it('sits under the run tables and above the review inbox', () => {
    const tree = liveScreen()
    const order = tree.childNodes().map((child) => (child as FakeScreen).classes[0] ?? '')
    const paused = order.indexOf('live-paused')
    expect(paused, 'the paused section is drawn').toBeGreaterThan(-1)
    expect(order.indexOf('live-repos'), 'the tables come first').toBeLessThan(paused)
    expect(order.indexOf('live-review'), 'the inbox keeps the last word').toBeGreaterThan(paused)
  })
})

// ---------------------------------------------------------------------------
// 2. Run detail's paused line.
// ---------------------------------------------------------------------------

describe('the Run screen: a paused record', () => {
  it('draws the status word, the resume instant and the run that resumed it', () => {
    const tree = theRunScreen({ ...TRACKED_ROW })
    expect(one(tree, 'run-status').textContent, 'the strip draws the record’s own word').toBe(
      PAUSED_STATUS,
    )
    const line = one(tree, 'run-paused')
    expect(one(line, 'run-paused-word').textContent).toBe('PAUSED')
    expect(one(line, 'run-paused-at-word').textContent).toBe('expected resume')
    expect(one(line, 'run-paused-at-value').textContent).toBe('2026-11-24T11:30:00Z')
    expect(one(line, 'run-paused-by-word').textContent).toBe('resumed by')
    const by = one(line, 'run-paused-by-value')
    expect(by.textContent, 'the ending is what a person reads').toBe('77b1')
    expect(by.getAttribute('data-run-id'), 'the door carries the whole id').toBe(
      '20261124-113000-77b1',
    )
  })

  it('draws a half-carried pause as the console’s mark, never as a blank', () => {
    const tree = theRunScreen({ ...TRACKED_ROW, resumed_by: null, resume_at: null })
    const line = one(tree, 'run-paused')
    const at = one(line, 'run-paused-at-value')
    expect(at.textContent).toBe(UNAVAILABLE_MARK)
    expect(at.getAttribute('title')).toBe('the document carries no row.resume_at')
    const by = one(line, 'run-paused-by-value')
    expect(by.textContent).toBe(UNAVAILABLE_MARK)
    expect(by.getAttribute('title')).toBe('the document carries no row.resumed_by')
    expect(by.getAttribute('data-run-id'), 'nobody resumed it, so there is nothing to open').toBeNull()
  })

  it('draws no paused line on a run whose record did not end paused', () => {
    // A running record carrying a resumer (a listing's own row can carry both
    // facts of a chain that resumed and went on) is not a paused run, and the
    // line is not drawn for it.
    const tree = theRunScreen({ ...TRACKED_ROW, status: 'running' })
    expect(one(tree, 'run-status').textContent).toBe('running')
    expect(all(tree, 'run-paused')).toEqual([])
  })
})

// ---------------------------------------------------------------------------
// 3. The status set: `paused` in it, an unknown word out of it.
// ---------------------------------------------------------------------------

describe('the console’s status set', () => {
  it('carries paused, and refuses nothing else the CLI’s listing can write', () => {
    expect(RUN_STATUSES).toContain(PAUSED_STATUS)
    expect(runStatusOf(PAUSED_STATUS)).toBe(PAUSED_STATUS)
    expect(runStatusOf('succeeded')).toBe('succeeded')
    expect(runStatusOf('quarantined'), 'a word outside the set is not drawn as itself').toBeNull()
  })

  it('draws an unknown status as unknown rather than failing', () => {
    // The fixture's fourth finished row: a word no reader here has a meaning
    // for. It is neither paused nor a refusal -- the screen draws the
    // console's own word where the status would be.
    const tree = theRunScreen({ ...TRACKED_ROW, status: 'quarantined' })
    expect(one(tree, 'run-status').textContent).toBe(UNKNOWN_STATUS_WORD)
    expect(all(tree, 'run-paused'), 'an unknown status is not a pause').toEqual([])

    // And the Live screen draws it nowhere rather than drawing it wrong: the
    // paused section is the feed filtered to the one status, so a word outside
    // the set simply has no place on this screen.
    const feed = theFeed()
    const unknown = feed.finished.find((row) => row.status === 'quarantined')
    expect(unknown, 'the fixture carries a row this build has never been taught').toBeDefined()
    expect(texts(liveScreen(), 'live-paused-run-title')).not.toContain(
      'a status this build has never been taught',
    )
  })

  it('leaves a record with no status at all drawing nothing', () => {
    // A record that carries no status is not a document with an unknown word in
    // it: the strip's cell stays empty, and the console does not invent one.
    const tree = theRunScreen({ ...TRACKED_ROW, status: undefined })
    expect(one(tree, 'run-status').textContent).toBeNull()
    expect(all(tree, 'run-paused')).toEqual([])
  })
})

// ---------------------------------------------------------------------------
// 4. The released console's own reader, asked about the word.
// ---------------------------------------------------------------------------

describe('the last released console’s parser', () => {
  /** releasedRow is one finished row of the fixture, as the release read it. */
  function releasedRow(at: number): unknown {
    const row = fixtureDocument() as Record<string, unknown>
    const finished = row['finished']
    if (!Array.isArray(finished)) throw new Error('the fixture carries no finished list')
    return finished[at]
  }

  it('accepts the paused word, so no --json format needs a version bump', () => {
    // The release had no table of statuses: it asked only that the key be there
    // and that its value be a string. `paused` is answered as `succeeded` would
    // be, and the two facts beside it (`resume_at`, `resumed_by`) are keys it
    // never read at all.
    expect(releasedFinishedStatus(releasedRow(0), 'finished', 0)).toBe(PAUSED_STATUS)
    // A word the release did not know is answered the same way: the release
    // refused shapes, never vocabularies.
    expect(releasedFinishedStatus(releasedRow(3), 'finished', 3)).toBe('quarantined')
  })

  it('would have refused a row with no status key, so the answer above means something', () => {
    // The copy is the released reader and not a screen that accepts anything: a
    // row missing the key is the refusal it always was, so `paused` coming back
    // as a string is the release reading it rather than ignoring it.
    let thrown: unknown = null
    try {
      releasedFinishedStatus({ run: '20261124-101500-9a3c' }, 'finished', 0)
    } catch (error) {
      thrown = error
    }
    expect(thrown).toBeInstanceOf(ReleasedRefusal)
    expect((thrown as ReleasedRefusal).refusal.field).toBe('finished[0].status')
    expect((thrown as ReleasedRefusal).message).toBe(
      `${RELEASED_WHERE}: finished[0].status is missing`,
    )
  })

  it('answers the paused feed that today’s decoder accepts, with no other difference', () => {
    // The same document, both ways: the release's reader for the one key the
    // question is about, and this build's decoder for the whole feed. A build
    // whose decoder refused what the release accepted would be an older console
    // broken by a newer CLI, which is the thing O8 asks about.
    const decoded = decodeSnapshot(fixtureDocument(), '2026-11-24T12:30:00Z')
    expect(decoded.ok, 'this build reads the paused feed').toBe(true)
    if (!decoded.ok) return
    const statuses = decoded.value.finished.map((row) => row.status)
    expect(statuses).toEqual([PAUSED_STATUS, PAUSED_STATUS, PAUSED_STATUS, 'quarantined'])
    expect(decoded.value.finished[0]?.resumeAt).toBe('2026-11-24T11:30:00Z')
    expect(decoded.value.finished[0]?.resumedBy).toBe('20261124-113000-77b1')
  })
})
