// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Live screen's row model and its drawing (desktop-console plan Task 2.1;
// design sections 6.1 rules 2 and 3, 6.2 screen 1).
//
// Three things are being held here, and each of them is a sentence from the
// plan rather than a preference:
//
//   1. **the row model from a fixture frame** -- the invented feed of Task 1.4
//      goes in through the real decoder and the model's rows come out. The
//      fixture is the one corpus nothing in this repository had to invent
//      twice, and a screen whose model is only ever exercised against a
//      hand-written object is a screen nobody has checked against the wire;
//   2. **each flag's boundary at exactly its threshold** -- 60 s and 180 s, the
//      plan's own red step. The boundary is not re-implemented here as prose:
//      the console's constants are compared, by name, against the Go source
//      that writes the numbers they stand for, so a later change to
//      `internal/watch/build.go` fails in this file rather than on a screen;
//   3. **the burn window against the constant** -- `BurnWindow` is 5 minutes in
//      `internal/watch/state.go`, and the sparkline's window is the same 5
//      minutes or the instrument is drawing a different measurement from the
//      one the CLI made. The boundary is asserted at exactly 300 s, which is
//      what `State.Burn` does: it drops a sample `Before(cut)`, so a sample
//      *at* `now - BurnWindow` is still in the window.
//
// The scan at the end of this file is not decoration either. `sanitize.test.ts`
// scans `src/renderer/` **non-recursively** -- `readdirSync` filtered to `.ts`,
// with no descent -- so the day this task added `src/renderer/screens/`, every
// file in that directory became invisible to the guard Task 2.0 wrote. The scan
// here is the recursive one, over the same forbidden tokens, so the new
// directory is covered rather than merely intended to be.

import { readdirSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import { loadFixture } from '../../src/source/fixture'
import {
  GAP_MARK,
  NOTIONAL_MARK,
  UNAVAILABLE_MARK,
  formatReading,
  measured,
  unavailable,
} from '../../src/shared/availability'
import { TOKEN_PLACEHOLDER } from '../../src/shared/sanitize'
import { decodeSnapshot } from '../../src/shared/schema'
import type { JSONDoc } from '../../src/source/source'
import { githubToken } from '../support/fakeSecrets'
import type { LiveDocument, RunView } from '../../src/shared/schema'
import {
  BurnHistory,
  LIVE_BURN_WINDOW_SECONDS,
  LIVE_LOST_AFTER_SECONDS,
  LIVE_NEAR_DEADLINE_FRACTION,
  LIVE_SILENT_AFTER_SECONDS,
  LIVE_STALE_AFTER_SECONDS,
  LIVE_THEMES,
  PIPELINE_SEGMENTS,
  buildLiveModel,
  contradiction,
  deadlineFlag,
  headroomOf,
  healthFlag,
  pipelineOf,
  renderLive,
  runStateOf,
  sparkline,
  stageSegment,
  watchHealth,
} from '../../src/renderer/screens/live'
import type {
  LiveFactory,
  LiveModel,
  LiveRunRow,
  RunFlag,
  ScreenElement,
  ScreenFactory,
} from '../../src/renderer/screens/live'
import { WRITE_UNAVAILABLE } from '../../src/renderer/writes'
import { SYNTHETIC_WHERE } from './synthetic'

// ---------------------------------------------------------------------------
// The fixture, through the real decoder.
// ---------------------------------------------------------------------------

/** theFixture is the committed synthetic feed, read from disk and verified by
 * the same loader the app uses. */
function theFixture() {
  return loadFixture(readFileSync(SYNTHETIC_WHERE, 'utf8'), SYNTHETIC_WHERE)
}

/** frames is every document of the fixture, decoded, in order. The notices
 * between them are not documents and are skipped by the loader's own record
 * union rather than by a filter here. */
function frames(): readonly LiveDocument[] {
  const out: LiveDocument[] = []
  for (const record of theFixture().records) {
    if (!('document' in record)) continue
    const decoded = decodeSnapshot(record.document, record.at)
    if (!decoded.ok) throw new Error(`${record.at}: ${decoded.skew.because}`)
    out.push(decoded.value)
  }
  return out
}

/** frameAt is the decoded frame the clause of the fixture written for `stamp`
 * produces. A missing stamp is a broken fixture, not a skipped test. */
function frameAt(stamp: string): LiveDocument {
  const found = frames().find((frame) => frame.now === stamp)
  if (found === undefined) throw new Error(`no frame at ${stamp}`)
  return found
}

/** theLastFrame is the fixture's absent-field frame: the repository at its cap,
 * a lost run beside a queued one. */
function theLastFrame(): LiveDocument {
  const all = frames()
  const last = all[all.length - 1]
  if (last === undefined) throw new Error('the fixture has no documents')
  return last
}

/** rawFrames is every document of the fixture as it arrived, undecoded, so a
 * test can add a key the fixture does not carry and put it through the decoder
 * rather than around it. */
function rawFrames(): readonly JSONDoc[] {
  const out: JSONDoc[] = []
  for (const record of theFixture().records) {
    if ('document' in record) out.push(record.document)
  }
  return out
}

/** theRun finds a run in a frame by its wire slug key, so a test names the row
 * it means rather than an index. */
function theRun(frame: LiveDocument, slug: string): RunView {
  const found = frame.runs.find((run) => run.run === slug)
  if (found === undefined) throw new Error(`no run ${slug}`)
  return found
}

const OAUTH_RUN = '20261015-120000-aa01'
const QUEUED_RUN = '20261015-120400-aa03'
const AT_CAP = '2026-10-15T12:04:21Z'

// ---------------------------------------------------------------------------
// The Go sources the constants answer to.
// ---------------------------------------------------------------------------

const HERE = path.dirname(fileURLToPath(import.meta.url))
const REPO_ROOT = path.join(HERE, '..', '..', '..')

/** goConstant reads a constant's right-hand side out of a Go source file. It
 * throws rather than returning '' so that a constant that was renamed fails
 * here, by name, instead of comparing an empty string to a number. */
function goConstant(file: string, name: string): string {
  const where = path.join(REPO_ROOT, 'internal', 'watch', file)
  const source = readFileSync(where, 'utf8')
  const found = new RegExp(String.raw`^\s*${name}\s*=\s*([^\n]+)$`, 'm').exec(source)
  if (found === null || found[1] === undefined) {
    throw new Error(`${where} has no ${name}`)
  }
  return found[1].trim()
}

// ---------------------------------------------------------------------------
// The fake screen.
// ---------------------------------------------------------------------------

/**
 * FakeScreen is the smallest thing the screen may draw into, and it is a fake
 * for the same reason `sanitize.test.ts`'s is: the unit suite has no `document`
 * and the point is the *observation*. Every attribute that could make the
 * window do something -- the navigating attribute, a resource, an event handler
 * -- throws, so a screen that reached for one fails here rather than in a
 * window nobody is watching.
 */
class FakeScreen implements ScreenElement {
  readonly attributes = new Map<string, string>()
  readonly nodes: FakeScreen[] = []
  readonly classes: string[] = []
  textContent: string | null = null

  static readonly FORBIDDEN_ATTRIBUTES = new Set([
    ['h', 'ref'].join(''),
    'src',
    'srcdoc',
    'style',
    'formaction',
    'action',
  ])

  constructor(readonly tag: string, readonly namespace: 'html' | 'svg' = 'html') {}

  setAttribute(name: string, value: string): void {
    const lowered = name.toLowerCase()
    if (FakeScreen.FORBIDDEN_ATTRIBUTES.has(lowered) || lowered.startsWith('on')) {
      throw new Error(`the live screen may not set the ${name} attribute`)
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

/** factory makes Fresh fakes, in the shape the renderer is handed. The two
 * methods are the two namespaces the real factory has, and the fake remembers
 * which one made a node so that the attribute rule below can be about SVG nodes
 * rather than about a tag that happens to be spelled `svg`. */
const factory: LiveFactory = {
  create: (tag) => new FakeScreen(tag),
  createNS: (tag) => new FakeScreen(tag, 'svg'),
}

/** root is a fresh section to draw the screen into. */
function root(): FakeScreen {
  return new FakeScreen('section')
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

/** drawn is the whole tree's text, in document order: what a person reading the
 * screen would read if they read every cell. */
function drawn(node: ScreenElement): string {
  return walk(node)
    .map((leaf) => leaf.textContent ?? '')
    .filter((text) => text !== '')
    .join(' ')
}

/** texts is the text of every node carrying a class. */
function texts(node: ScreenElement, className: string): readonly (string | null)[] {
  return all(node, className).map((leaf) => leaf.textContent)
}

/** modelOf is the screen's model for one frame, with a history of exactly that
 * frame unless a test builds its own. */
function modelOf(frame: LiveDocument): LiveModel {
  return buildLiveModel(frame, new BurnHistory())
}

// ---------------------------------------------------------------------------

describe('the Live screen constants', () => {
  it('are the thresholds internal/watch writes, and cannot drift from them', () => {
    // The plan's words: "the burn window must match `watch`'s own
    // (`BurnWindow`, 5 minutes) and a test asserts the two constants cannot
    // drift". The same holding is applied to the three flag thresholds and to
    // the stale banner, because they are the same kind of fact: a number the
    // console draws a conclusion from, whose author is the CLI.
    expect(goConstant('state.go', 'BurnWindow')).toBe('5 * time.Minute')
    expect(goConstant('state.go', 'StaleAfter')).toBe('45 * time.Second')
    expect(goConstant('build.go', 'SilentAfter')).toBe('60 * time.Second')
    expect(goConstant('build.go', 'LostAfter')).toBe('180 * time.Second')
    expect(goConstant('build.go', 'NearDeadline')).toBe('0.8')

    expect(LIVE_BURN_WINDOW_SECONDS).toBe(5 * 60)
    expect(LIVE_STALE_AFTER_SECONDS).toBe(45)
    expect(LIVE_SILENT_AFTER_SECONDS).toBe(60)
    expect(LIVE_LOST_AFTER_SECONDS).toBe(3 * 60)
    expect(LIVE_NEAR_DEADLINE_FRACTION).toBe(0.8)
  })

  it('names the two themes the screenshot goldens are taken for', () => {
    expect(LIVE_THEMES).toEqual(['light', 'dark'])
  })
})

describe('watchHealth', () => {
  // The CLI's own rule, restated with the console's constants
  // (`internal/watch/build.go:416`): `quiet > LostAfter` is lost, else
  // `quiet > SilentAfter` is silent, else ok. It is `>` and not `>=`, and that
  // is the whole of the boundary the plan names.
  it('puts the boundary at exactly 60 s and 180 s', () => {
    expect(watchHealth(59.999)).toBe('ok')
    expect(watchHealth(60)).toBe('ok')
    expect(watchHealth(60.001)).toBe('silent')
    expect(watchHealth(179.999)).toBe('silent')
    expect(watchHealth(180)).toBe('silent')
    expect(watchHealth(180.001)).toBe('lost')
  })

  it('says nothing about a run whose silence nobody measured', () => {
    expect(watchHealth(null)).toBe('ok')
    expect(watchHealth(Number.NaN)).toBe('ok')
    expect(watchHealth(-1)).toBe('ok')
  })
})

describe('the flags', () => {
  it('draws the three states the CLI computes and nothing else', () => {
    expect(healthFlag('ok')).toBeNull()
    expect(healthFlag('silent')?.name).toBe('silent')
    expect(healthFlag('silent')?.tone).toBe('amber')
    expect(healthFlag('lost')?.name).toBe('lost')
    expect(healthFlag('lost')?.tone).toBe('alarm')

    expect(deadlineFlag('ok')).toBeNull()
    expect(deadlineFlag('near')?.name).toBe('near deadline')
    expect(deadlineFlag('near')?.tone).toBe('amber')
    expect(deadlineFlag('over')?.name).toBe('over deadline')
    expect(deadlineFlag('over')?.tone).toBe('alarm')
  })

  it('explains each flag with the threshold it is about', () => {
    // A flag is a conclusion; the sentence behind it is the evidence. The
    // numbers in it are the constants above rather than a second spelling.
    expect(healthFlag('silent')?.because).toContain(`${LIVE_SILENT_AFTER_SECONDS} s`)
    expect(healthFlag('lost')?.because).toContain(`${LIVE_LOST_AFTER_SECONDS} s`)
    expect(deadlineFlag('near')?.because).toContain(`${LIVE_NEAR_DEADLINE_FRACTION * 100}%`)
  })

  it('draws an enum value the console does not know as unknown, never as healthy', () => {
    // `health` and `deadline` are enums the CLI writes from its own tables
    // (`NON_TEXT_STRING_KEYS`), and a value this build has never seen is a
    // document from a newer CLI. Drawing "not silent" for it would be the
    // console answering a question it cannot read, so it is a flag of its own,
    // amber, in the console's words -- and the document's value is not in it.
    for (const value of ['hibernating', '', 'constructor', '__proto__', 'toString']) {
      const health = healthFlag(value)
      expect(health?.name, `health ${JSON.stringify(value)}`).toBe('unknown health')
      expect(health?.tone).toBe('amber')
      if (value !== '') expect(health?.because).not.toContain(value)
      const deadline = deadlineFlag(value)
      expect(deadline?.name, `deadline ${JSON.stringify(value)}`).toBe('unknown deadline')
      expect(deadline?.tone).toBe('amber')
    }
  })
})

describe('the row model', () => {
  it('reads the project line from the frame', () => {
    const model = modelOf(frameAt('2026-10-15T12:04:20Z'))
    expect(model.project).toBe('<project>')
    expect(model.mode).toBe('observe')
    expect(model.day).toBe('2026-10-15')
    expect(model.now).toBe('2026-10-15T12:04:20Z')
    expect(model.connection.state).toBe('stale')
    expect(model.connection.reason).toBe('no document arrived for 160s')
    expect(formatReading(model.connection.age, 'seconds')).toBe('160 s')
  })

  it('marks the connection stale past 45 s on the age it was given', () => {
    // The console does not recompute the CLI's `state` -- that enum is the
    // authority -- but it does read `age_seconds`, and 45 s is `StaleAfter`.
    expect(modelOf(frameAt('2026-10-15T12:00:00Z')).connection.staleByAge).toBe(false)
    expect(modelOf(frameAt('2026-10-15T12:01:40Z')).connection.staleByAge).toBe(true)
  })

  it('keeps the queued note when the document carries one, and only then', () => {
    // The fixture's frames carry no `queued_note` -- the key is `omitempty` and
    // the synthetic feed was invented without it -- so the absent case is the
    // fixture and the present case is built here. Without this the branch was
    // never drawn by any test, which is the hole a screen full of nothing
    // leaves: a project whose runs bucket could not be read would look exactly
    // like a project with no queued runs.
    expect(modelOf(theLastFrame()).queuedNote).toBeNull()
    expect(all(renderLive(factory, modelOf(theLastFrame()), 'light'), 'live-note')).toEqual([])

    // The key is read off the *wire* document and through the decoder, because
    // the decoded name is `queuedNote` and only the raw frame carries
    // `queued_note` -- building the decoded object by hand would assert the
    // screen against a shape the decoder never produces.
    const raw = rawFrames()[rawFrames().length - 1]
    if (raw === undefined) throw new Error('the fixture has no documents')
    const decoded = decodeSnapshot(
      { ...raw, queued_note: 'the runs bucket could not be read (retrying)' },
      'the fixture frame with a queued note',
    )
    if (!decoded.ok) throw new Error(decoded.skew.because)
    const withNote = buildLiveModel(decoded.value, new BurnHistory())
    expect(withNote.queuedNote).toBe('the runs bucket could not be read (retrying)')
    const tree = renderLive(factory, withNote, 'light')
    expect(texts(tree, 'live-note')).toEqual(['the runs bucket could not be read (retrying)'])
  })

  it('reads a repository row, its bar and its rate', () => {
    const model = modelOf(theLastFrame())
    expect(model.repos).toHaveLength(1)
    const repo = model.repos[0]
    if (repo === undefined) throw new Error('no repository row')
    expect(repo.repo).toBe('r-4c25cf20/r-f1aa9214')
    expect(repo.countedUsd.state).toBe('measured')
    expect(formatReading(repo.countedUsd, 'usd')).toBe('$50.00')
    expect(formatReading(repo.percent, 'percent')).toBe('100.0%')
    expect(formatReading(repo.dailyCapUsd, 'usd')).toBe('$50.00')
    expect(repo.spentUsd.state).toBe('measured')
    expect(repo.notionalUsd.state).toBe('notional')
  })

  it('says "no cap" rather than 0% when no cap is set', () => {
    // The bar's `daily_cap_usd` is `*float64` with no `omitempty` (contract
    // section 1): `null` is "no cap is set", which is not a cap of zero, and a
    // screen that drew 0% would be asserting the project spent nothing against
    // a cap that does not exist.
    const frame = theLastFrame()
    // The CLI writes `daily_cap_usd` and `percent` together (`usdPtr`,
    // `internal/watch/json.go:122`): a bar with no cap has no percentage
    // either, and a document carrying one without the other is not a shape any
    // version of `watch` writes.
    const model = buildLiveModel(
      {
        ...frame,
        total: {
          ...frame.total,
          dailyCapUsd: unavailable('the document has no total.daily_cap_usd'),
          percent: unavailable('the document has no total.percent'),
        },
      },
      new BurnHistory(),
    )
    expect(formatReading(model.total.dailyCapUsd, 'usd')).toBe(UNAVAILABLE_MARK)
    expect(formatReading(model.total.percent, 'percent')).toBe(UNAVAILABLE_MARK)
  })

  it('carries a run row with every field the plan names', () => {
    const model = modelOf(frameAt('2026-10-15T12:00:05Z'))
    const repo = model.repos[0]
    if (repo === undefined) throw new Error('no repository row')
    const row = repo.runs.find((one) => one.run === OAUTH_RUN)
    if (row === undefined) throw new Error('no run row')
    expect(row.title).toBe('wire the console to the live feed')
    expect(row.stage).toBe('implement')
    expect(row.round).toBe('2')
    expect(row.verify).toBe('pending')
    expect(row.models).toBe('claude-sonnet-4-5-20250929 / claude-opus-4-1')
    expect(row.recipe).toBe('console-text-path')
    expect(row.auth).toBe('oauth')
    expect(formatReading(row.age, 'seconds')).toBe('1,505 s')
  })

  it('draws an OAuth run\'s spend as notional and never as money', () => {
    // Design section 6.2's last sentence. `notional: true` says the dollars are
    // a subscription's list price, and the CLI's own counters keep them out of
    // every cap (design section 6.1 rule 1) -- so a screen that drew them as
    // billed dollars would be reporting a spend that never happened.
    const model = modelOf(frameAt('2026-10-15T12:00:05Z'))
    const repo = model.repos[0]
    if (repo === undefined) throw new Error('no repository row')
    const row = repo.runs.find((one) => one.run === OAUTH_RUN)
    if (row === undefined) throw new Error('no run row')
    expect(row.spend.state).toBe('notional')
    expect(formatReading(row.spend, 'usd')).toBe(`${NOTIONAL_MARK}$12.54`)
  })

  it('leaves a spend the document does not know unavailable', () => {
    const model = modelOf(theLastFrame())
    const repo = model.repos[0]
    if (repo === undefined) throw new Error('no repository row')
    const queued = repo.runs.find((one) => one.run === QUEUED_RUN)
    if (queued === undefined) throw new Error('no queued row')
    expect(queued.queued).toBe(true)
    expect(queued.spend.state).toBe('unavailable')
    expect(formatReading(queued.spend, 'usd')).toBe(UNAVAILABLE_MARK)
    // The CLI writes the absent stage, round and models as its own dash, which
    // is text and is drawn as itself; only the spend is `null`.
    expect(queued.stage).toBe('queued')
    expect(queued.round).toBe('-')
    expect(queued.requestedBy).toBe('redacted@example.invalid')
  })

  it('draws the design’s seven columns, and the fields the table has no column for leave the row', () => {
    // **Console redesign step 2b: the row is a table row now.** The design's
    // runs table has RUN, TASK, PIPELINE, CODER / REVIEWER, COST, ELAPSED and
    // STATUS, and the fields the flex row used to draw beside the title --
    // who started the run, the requester, the recipe, the workflow, the verify
    // word -- have no column. They stay in the **model** (`LiveRunRow`), so a
    // later step that wants one has the document's value to draw; what this
    // test holds is that no cell is drawn for them, which is the half a reader
    // of the screen would otherwise have to guess.
    const frame = theLastFrame()
    const dropped = [
      'live-run-verify',
      'live-run-recipe',
      'live-run-workflow',
      'live-run-requester',
      'live-run-started-by',
    ]
    const runs = frame.runs.map((run) =>
      run.run === QUEUED_RUN ? { ...run, launchedBy: 'dana.person+fugaro@example.com' } : run,
    )
    const tree = renderLive(factory, buildLiveModel({ ...frame, runs }, new BurnHistory()), 'light')
    for (const className of dropped) {
      expect(all(tree, className), `${className} is drawn`).toHaveLength(0)
    }
    // The model still carries them, so the columns are a drawing decision and
    // not a hole in the decoder.
    const model = buildLiveModel({ ...frame, runs }, new BurnHistory())
    const row = model.repos[0]?.runs.find((one) => one.run === QUEUED_RUN)
    if (row === undefined) throw new Error('no queued row')
    expect(row.launchedBy).toBe('dana.person+fugaro@example.com')
    expect(row.requestedBy).toBe('redacted@example.invalid')
  })

  it('draws one cell per column, in the design’s order, and the id cell is the door', () => {
    // The row's shape, asserted cell by cell: `entry.ts`'s `wireRuns` reads the
    // text of `.live-run-id` off the row it is bound to and opens Run detail
    // with it, so the id has to be a cell of the row **and** carry the run's own
    // drawn text. A row that drew the id somewhere else, or drew the raw wire
    // value into an attribute, would leave the door matching nothing.
    const tree = renderLive(factory, modelOf(theLastFrame()), 'light')
    const rows = all(tree, 'live-run')
    expect(rows.length).toBeGreaterThan(0)
    for (const row of rows) {
      expect(row.tag).toBe('tr')
      expect(row.getAttribute('data-queued')).not.toBeNull()
      const cells = row.childNodes().filter((child) => child.tag === 'td')
      expect(cells).toHaveLength(7)
      const id = one(row, 'live-run-id')
      expect(id.tag).toBe('td')
      expect(id.textContent).not.toBe('')
      // The door reads this cell and nothing else on the row.
      expect(all(row, 'live-run-id')).toHaveLength(1)
      expect(texts(row, 'live-run-stage')).toHaveLength(1)
      expect(texts(row, 'live-run-state')).toHaveLength(1)
      expect(texts(row, 'live-run-spend')).toHaveLength(1)
      expect(texts(row, 'live-run-age')).toHaveLength(1)
    }
    // The seven `td`s and the `th` row are the same columns: a heading with no
    // cell under it is a column the reader looks for and does not find.
    expect(texts(tree, 'live-table-head')).toEqual([
      'RUN',
      'TASK',
      'PIPELINE',
      'CODER / REVIEWER',
      'COST',
      'ELAPSED',
      'STATUS',
    ])
  })

  it('takes a run\'s rigor from the join by id, and draws no chip without one', () => {
    // Owner request 2026-10-25. **The mode is not in the live frame**: the frame
    // is `JSONRun`, which carries no rigor block, so the row's mode is joined
    // from `runs ls --json` by run id (`shared/rigor.ts`'s `rigorIndex`) and the
    // caller is what makes the join. A run the index does not know is a run with
    // no chip -- never another run's mode -- which is what the default argument
    // (`NO_RIGOR`) gives every caller that has not made one.
    const frame = theLastFrame()
    const index = new Map([
      [OAUTH_RUN, { mode: 'pro', from: 'repo', areas: [] }],
      [QUEUED_RUN, { mode: 'critical', from: 'area', areas: [{ name: 'payments', mode: 'critical' }] }],
    ])
    const model = buildLiveModel(frame, new BurnHistory(), null, index)
    const repo = model.repos[0]
    if (repo === undefined) throw new Error('no repository row')
    expect(repo.runs.find((row) => row.run === OAUTH_RUN)?.rigor?.mode).toBe('pro')
    expect(repo.runs.find((row) => row.run === QUEUED_RUN)?.rigor?.mode).toBe('critical')
    // The same frame with no join at all: every row's rigor is `null`, and no
    // row draws a chip.
    const plain = buildLiveModel(frame, new BurnHistory())
    for (const row of plain.repos[0]?.runs ?? []) expect(row.rigor).toBeNull()
    expect(all(renderLive(factory, plain, 'light'), 'live-run-mode')).toHaveLength(0)
    // An index that knows the run but not its block -- a row the reader would
    // not show, or an id the run list does not name -- is the same absence.
    const partial = buildLiveModel(frame, new BurnHistory(), null, new Map([[OAUTH_RUN, { mode: 'pro', from: 'repo', areas: [] }]]))
    expect(partial.repos[0]?.runs.find((row) => row.run === QUEUED_RUN)?.rigor).toBeNull()
  })

  it('flags the runs the fixture went silent and then lost', () => {
    const silent = modelOf(frameAt('2026-10-15T12:01:40Z')).repos[0]
    if (silent === undefined) throw new Error('no repository row')
    expect(silent.runs[0]?.flags.map((flag) => flag.name)).toEqual(['silent'])

    const lost = modelOf(frameAt('2026-10-15T12:04:20Z')).repos[0]
    if (lost === undefined) throw new Error('no repository row')
    expect(lost.runs[0]?.flags.map((flag) => flag.name)).toEqual(['lost'])
    expect(lost.runs[0]?.tone).toBe('alarm')
  })

  it('holds a quiet run of the fixture to the CLI\'s own boundary', () => {
    // The fixture's runs are 1,500 / 1,600 / 1,760 s old and their `health`
    // says ok / silent / lost. Age is not silence, so this is not a derivation
    // -- it is the one direction that is a contradiction: silence never exceeds
    // age, so a run the CLI calls `silent` cannot be 45 s old. The fixture's own
    // numbers are consistent, and this asserts that the console noticed.
    const frame = frameAt('2026-10-15T12:01:40Z')
    const model = modelOf(frame)
    const repo = model.repos[0]
    if (repo === undefined) throw new Error('no repository row')
    for (const row of repo.runs) {
      expect(row.contradiction).toBeNull()
    }
    const aged = theRun(frame, OAUTH_RUN)
    expect(aged.ageSeconds.value).toBe(1600)
    expect(watchHealth(aged.ageSeconds.value)).toBe('lost')
    expect(healthFlag(aged.health)?.name).toBe('silent')
  })

  it('does not sum a notional dollar into a real total', () => {
    // Phase 2's review focus, asserted on the model rather than on the screen:
    // the total's counted dollars and its notional dollars are two readings and
    // the model never adds them.
    const total = modelOf(theLastFrame()).total
    expect(formatReading(total.countedUsd, 'usd')).toBe('$50.00')
    expect(formatReading(total.notionalUsd, 'usd')).toBe(`${NOTIONAL_MARK}$3.29`)
    expect(total.notionalUsd.state).toBe('notional')
    // And the pair the two cells hold is not the pair a screen that summed
    // them would draw: `$53.29` appears in no cell of the total.
    const tree = renderLive(factory, modelOf(theLastFrame()), 'light')
    expect(drawn(one(tree, 'live-total'))).not.toContain('$53.29')
  })

  it('renders a kill banner with its by, its at and its reason', () => {
    const document: LiveDocument = {
      ...theLastFrame(),
      repos: theLastFrame().repos.map((repo) => ({
        ...repo,
        kill: {
          by: 'redacted@example.invalid',
          at: '2026-10-15T12:03:00Z',
          reason: 'runaway spend',
        },
      })),
    }
    const repo = buildLiveModel(document, new BurnHistory()).repos[0]
    expect(repo?.kill?.by).toBe('redacted@example.invalid')
    expect(repo?.kill?.reason).toBe('runaway spend')
    expect(repo?.kill?.at).toBe('2026-10-15T12:03:00Z')

    const tree = renderLive(factory, buildLiveModel(document, new BurnHistory()), 'light')
    expect(drawn(tree)).toContain('KILLED')
    expect(one(tree, 'live-kill').getAttribute('data-killed')).toBe('true')
    expect(texts(tree, 'live-kill-reason')).toEqual(['runaway spend'])
  })
})

// ---------------------------------------------------------------------------
// The pipeline bar, the run's own word, and the two write buttons the redesign
// draws (console redesign, step 2b).
// ---------------------------------------------------------------------------

/** runnerSources is every Go file of `internal/runner` and `internal/watch`,
 * joined, so a test can hold a stage name against the source that writes it
 * rather than against a list this file keeps. */
function runnerSources(): string {
  const out: string[] = []
  for (const name of ['runner', 'watch']) {
    const where = path.join(REPO_ROOT, 'internal', name)
    for (const entry of readdirSync(where)) {
      if (entry.endsWith('.go')) out.push(readFileSync(path.join(where, entry), 'utf8'))
    }
  }
  return out.join('\n')
}

/** aRow is one row of the fixture's last frame with the fields a test names
 * changed. The pipeline and the chip read the recorded stage and the flags and
 * nothing else, so a test that wants a run at another point of its pipeline
 * edits the stage the CLI would have written rather than inventing a row. */
function aRow(edit: Partial<LiveRunRow>): LiveRunRow {
  const model = modelOf(theLastFrame())
  const base = model.repos[0]?.runs[0] ?? model.orphans[0]
  if (base === undefined) throw new Error('the fixture has no run row')
  return { ...base, ...edit }
}

/** LOST is the flag `internal/watch` raises when a run's heartbeat is gone. */
const LOST: RunFlag = { name: 'lost', because: 'no heartbeat for 3m', tone: 'alarm' }

describe('the pipeline bar', () => {
  it('draws the five segments in the order a run moves through them', () => {
    expect(PIPELINE_SEGMENTS).toEqual(['queued', 'build', 'implement', 'review', 'done'])
  })

  it('maps the runner\'s own stage names onto the five segments, and only those names', () => {
    // **The names are `internal/runner`'s, not this file's.** Each one is a
    // string the runner writes onto the record (`r.rec.Stage = ...`, or
    // `r.stage(ctx, name, ...)`'s callers), plus the `queued` the CLI's queued
    // rows carry -- so a rename in the runner fails here rather than on a
    // screen. The scan reads the Go sources; the assertions beside it are the
    // mapping a reader wants to see in one place.
    const sources = runnerSources()
    for (const stage of [
      'queued',
      'provision',
      'bootstrap',
      'plan',
      'implement',
      'fix',
      'review_first',
      'check',
      'review',
      'finalize',
      'writeback',
      'render',
      'digest',
    ]) {
      expect(sources.includes(`"${stage}"`), `no Go source writes "${stage}"`).toBe(true)
    }

    expect(stageSegment('queued')).toBe('queued')
    expect(stageSegment('provision')).toBe('build')
    expect(stageSegment('bootstrap')).toBe('build')
    expect(stageSegment('plan')).toBe('build')
    expect(stageSegment('implement')).toBe('implement')
    expect(stageSegment('fix')).toBe('implement')
    expect(stageSegment('review_first')).toBe('implement')
    expect(stageSegment('check')).toBe('review')
    expect(stageSegment('review')).toBe('review')
    for (const stage of ['finalize', 'writeback', 'render', 'digest']) {
      expect(stageSegment(stage), stage).toBe('done')
    }
  })

  it('answers nothing for a stage this build has never heard of', () => {
    // A stage string is the document's, and `STAGE_SEGMENTS` is a `Map` for
    // exactly these words: on an object literal `constructor`, `toString` and
    // the like answer with something *inherited* rather than with nothing, and
    // an inherited answer would light a segment no stage named.
    for (const stage of ['', 'kanban', 'constructor', 'toString', '__proto__', 'hasOwnProperty']) {
      expect(stageSegment(stage), stage).toBeNull()
    }
  })

  it('draws what is behind the run past, its own segment current and what is ahead future', () => {
    expect(pipelineOf(aRow({ stage: 'review', flags: [] }))).toEqual([
      { stage: 'queued', mark: 'past' },
      { stage: 'build', mark: 'past' },
      { stage: 'implement', mark: 'past' },
      { stage: 'review', mark: 'current' },
      { stage: 'done', mark: 'future' },
    ])
    // The two ends are reachable: a run that has just been queued has nothing
    // behind it, and one at its last stage has nothing ahead.
    expect(pipelineOf(aRow({ stage: 'queued', flags: [] })).map((one) => one.mark)).toEqual([
      'current',
      'future',
      'future',
      'future',
      'future',
    ])
    expect(pipelineOf(aRow({ stage: 'digest', flags: [] })).map((one) => one.mark)).toEqual([
      'past',
      'past',
      'past',
      'past',
      'current',
    ])
  })

  it('turns the current segment into the danger mark for a lost heartbeat, and moves nothing', () => {
    // A lost run is still *at* the segment the CLI last recorded; what changed
    // is that the console stopped hearing from it. A bar that backed the run up
    // a segment, or cleared the ones behind it, would report a position the
    // record does not carry.
    expect(pipelineOf(aRow({ stage: 'implement', flags: [LOST] })).map((one) => one.mark)).toEqual([
      'past',
      'past',
      'danger',
      'future',
      'future',
    ])
  })

  it('lights no segment for an unknown stage, and still prints the stage under the bar', () => {
    expect(pipelineOf(aRow({ stage: 'kanban' })).map((one) => one.mark)).toEqual([
      'future',
      'future',
      'future',
      'future',
      'future',
    ])
    // The text line is what carries the meaning, so an unknown word reaches the
    // reader even though the bar can say nothing about it.
    const frame = theLastFrame()
    const runs = frame.runs.map((run) => (run.run === OAUTH_RUN ? { ...run, stage: 'kanban' } : run))
    const tree = renderLive(factory, buildLiveModel({ ...frame, runs }, new BurnHistory()), 'light')
    expect(texts(tree, 'live-run-stage')).toContain('kanban')
    const row = all(tree, 'live-run').find((one) => one.getAttribute('data-queued') === 'false')
    if (row === undefined) throw new Error('no running row')
    expect(
      all(row, 'live-pipeline-segment').map((segment) => segment.getAttribute('data-mark')),
    ).toEqual(['future', 'future', 'future', 'future', 'future'])
  })

  it('draws the segments as decoration and the stage, round and state as the text', () => {
    const tree = renderLive(factory, modelOf(theLastFrame()), 'light')
    for (const segments of all(tree, 'live-pipeline-segments')) {
      expect(segments.tag).toBe('ol')
      expect(segments.getAttribute('aria-hidden')).toBe('true')
      expect(segments.childNodes()).toHaveLength(5)
    }
    // The words under the bar are the row's own three cells, so the meaning is
    // never only the colour of a segment.
    for (const row of all(tree, 'live-run')) {
      expect(texts(row, 'live-run-stage')).toHaveLength(1)
      expect(texts(row, 'live-run-round')).toHaveLength(1)
      expect(texts(row, 'live-run-state')).toHaveLength(1)
    }
  })
})

describe('the run\'s word', () => {
  it('is queued, lost, done or running, in that order of precedence', () => {
    // A queued run has no stage to be lost at, and a lost heartbeat is the worse
    // fact of the two remaining: the order is the whole of the rule.
    expect(runStateOf(aRow({ queued: true, flags: [LOST] }))).toBe('queued')
    expect(runStateOf(aRow({ stage: 'digest', flags: [LOST] }))).toBe('lost')
    expect(runStateOf(aRow({ stage: 'finalize', flags: [] }))).toBe('done')
    expect(runStateOf(aRow({ stage: 'implement', flags: [] }))).toBe('running')
    // An unknown stage is not `done`: the console has no ground to say a
    // finishing stage a word it does not know.
    expect(runStateOf(aRow({ stage: 'kanban', flags: [] }))).toBe('running')
  })

  it('is drawn as a word every time, whatever the row\'s colour', () => {
    const tree = renderLive(factory, modelOf(theLastFrame()), 'light')
    const words = new Set(['queued', 'running', 'lost', 'done'])
    for (const chip of all(tree, 'live-run-chip')) {
      expect(words.has(chip.textContent ?? '')).toBe(true)
    }
  })

  it('gives the lost chip its pip as decoration, and the word either way', () => {
    const frame = theLastFrame()
    const runs = frame.runs.map((run) => (run.run === OAUTH_RUN ? { ...run, health: 'lost' } : run))
    const tree = renderLive(factory, buildLiveModel({ ...frame, runs }, new BurnHistory()), 'light')
    const lost = all(tree, 'live-run').find((row) => row.classes.includes('live-run-lost'))
    if (lost === undefined) throw new Error('the fixture has no lost run')
    expect(texts(lost, 'live-run-state')).toEqual(['lost'])
    const pip = one(lost, 'live-run-pip')
    expect(pip.getAttribute('aria-hidden')).toBe('true')
    // The pip says nothing the word does not: it is one character beside it.
    expect(pip.textContent).toBe('●')
  })
})

describe('the two write buttons the redesign draws', () => {
  it('draws Halt project and Reap disabled, with the reason, and binds nothing to them', () => {
    // **The console performs no write but launch-from-console.** Both controls
    // go through `writes.ts`'s `disabledWrite`, which draws a real `button` a
    // reader is told about and attaches no listener of any kind -- no IPC
    // channel, no preload member, no spawn entry. The screen builds no listener
    // either: `live.ts` is a pure drawing file, and the test below reads its
    // source rather than trusting that.
    const frame = theLastFrame()
    const runs = frame.runs.map((run) => (run.run === OAUTH_RUN ? { ...run, health: 'lost' } : run))
    const tree = renderLive(factory, buildLiveModel({ ...frame, runs }, new BurnHistory()), 'light')

    const halt = one(tree, 'live-halt')
    expect(halt.tag).toBe('button')
    expect(halt.getAttribute('type')).toBe('button')
    expect(halt.getAttribute('aria-disabled')).toBe('true')
    expect(halt.getAttribute('title')).toBe(WRITE_UNAVAILABLE)
    expect(halt.getAttribute('aria-label')).toBe(`Halt project: ${WRITE_UNAVAILABLE.toLowerCase()}`)
    expect(halt.childNodes().map((one) => one.textContent)).toEqual(['Halt project'])

    const reap = one(tree, 'live-reap')
    expect(reap.tag).toBe('button')
    expect(reap.getAttribute('aria-disabled')).toBe('true')
    expect(reap.getAttribute('title')).toBe(WRITE_UNAVAILABLE)
    expect(reap.getAttribute('aria-label')).toBe(`Reap: ${WRITE_UNAVAILABLE.toLowerCase()}`)
    expect(reap.childNodes().map((one) => one.textContent)).toEqual(['Reap'])

    // Both are drawn through `disabledWrite` and nothing else, so the attributes
    // above are that function's and not a second drawing of the same control.
    const source = readFileSync(path.join(SRC, 'renderer', 'screens', 'live.ts'), 'utf8')
    expect(source.includes("disabledWrite(factory, 'live-halt', 'Halt project')")).toBe(true)
    expect(source.includes("disabledWrite(factory, 'live-reap', 'Reap')")).toBe(true)
    expect(source.includes('addEventListener')).toBe(false)
  })
})

describe('the runs table\'s own scrolling box', () => {
  it('scrolls the table sideways inside its box rather than widening the panel', () => {
    // The table is seven columns wide with a floor (`min-width`), so at a narrow
    // window the box scrolls and the panel it sits in does not. Both halves are
    // asserted by name: a table given a floor with no scrolling ancestor is a
    // table whose last column is off the screen with no way to reach it.
    const css = readFileSync(path.join(SRC, 'renderer', 'theme', 'flight-deck.css'), 'utf8')
    const box = /div\.live-runs\s*\{([^}]*)\}/.exec(css)
    expect(box?.[1]).toContain('overflow-x: auto')
    const table = /table\.live-table\s*\{([^}]*)\}/.exec(css)
    expect(table?.[1]).toContain('min-width: 1080px')
  })
})

// ---------------------------------------------------------------------------
// The fleet budget panel (console redesign, step 2a-2).
// ---------------------------------------------------------------------------

/**
 * fleetModel is the fixture's last frame with its `total` edited **on the wire
 * and read back through the decoder**, the way the `queued_note` case above
 * does it: `leased_usd` is optional in `schema.ts` (a recording made before it
 * shipped does not carry it), and a panel
 * asserted against a decoded object built by hand would be asserted against a
 * shape `schema.ts` never produces.
 */
function fleetModel(total: Record<string, unknown>): LiveModel {
  const raw = rawFrames()[rawFrames().length - 1]
  if (raw === undefined) throw new Error('the fixture has no documents')
  const carried = raw['total']
  if (carried === null || typeof carried !== 'object') throw new Error('the fixture frame carries no total')
  const decoded = decodeSnapshot(
    { ...raw, total: { ...(carried as Record<string, unknown>), ...total } },
    'the fixture frame with an edited total',
  )
  if (!decoded.ok) throw new Error(decoded.skew.because)
  return buildLiveModel(decoded.value, new BurnHistory())
}

/** segmentNames is each drawn segment's own account, in drawing order: the
 * `data-account` the stylesheet paints it by, read back off the node rather
 * than assumed from the order. */
function segmentNames(tree: ScreenElement): readonly string[] {
  return all(tree, 'live-fleet-segment').map((segment) => segment.getAttribute('data-account') ?? '')
}

/** segmentWidths is each segment's width on the bar's own `viewBox` axis, where
 * the whole axis is the cap. */
function segmentWidths(tree: ScreenElement): readonly (string | null)[] {
  return all(tree, 'live-fleet-segment').map((segment) => segment.getAttribute('width'))
}

/** segmentOffsets is each segment's own left edge on that axis. A bar that drew
 * every segment at zero -- stacking them, drawing past the end or losing the
 * clamp -- has the right widths and the wrong picture. */
function segmentOffsets(tree: ScreenElement): readonly (string | null)[] {
  return all(tree, 'live-fleet-segment').map((segment) => segment.getAttribute('x'))
}

/** legendValues is the number each legend row draws, in row order. */
function legendValues(tree: ScreenElement): readonly (string | null)[] {
  return all(tree, 'live-fleet-key').map(
    (key) => all(key, 'live-num')[0]?.textContent ?? '',
  )
}

/** legendRow is the legend row that carries a given word. The word is what a
 * reader has: a row the bar paints no shape for has no swatch to key it by, and
 * that is exactly the row this suite is about. */
function legendRow(tree: ScreenElement, word: string): FakeScreen {
  const row = all(tree, 'live-fleet-key').find(
    (one) => all(one, 'live-fleet-key-word')[0]?.textContent === word,
  )
  if (row === undefined) throw new Error(`no ${word} legend row`)
  return row
}

/** legendSwatches is each legend row's swatch, or null where the row has none:
 * a swatch keys a shape, so a row whose account the bar does not draw has none. */
function legendSwatches(tree: ScreenElement): readonly (string | null)[] {
  return all(tree, 'live-fleet-key').map(
    (key) => all(key, 'live-fleet-swatch')[0]?.getAttribute('data-account') ?? null,
  )
}

/** panel is the drawn fleet budget panel of a model. */
function panel(model: LiveModel): FakeScreen {
  return one(renderLive(factory, model, 'light'), 'live-fleet')
}

describe('the fleet budget panel', () => {
  it('derives its own figures from the model, and derives nothing twice', () => {
    // The fixture's last frame: a repository at its cap, a lost run beside a
    // queued one. `headroom` is the console's arithmetic (cap less counted),
    // and the three counts are read off the rows the model built.
    const model = modelOf(theLastFrame())
    expect(formatReading(model.fleet.headroom, 'usd')).toBe('$0.00')
    expect(model.fleet.headroom.state).toBe('measured')
    expect(formatReading(model.fleet.burn, 'usd')).toBe('$0.00')
    expect(model.fleet.active).toBe(1)
    expect(model.fleet.queued).toBe(1)
    expect(model.fleet.heartbeatLost).toBe(true)
    // The cap is the document's and the headroom is the difference: a screen
    // that reported the cap as the headroom would show room that is not there.
    expect(formatReading(model.total.dailyCapUsd, 'usd')).toBe('$50.00')
    expect(formatReading(model.fleet.headroom, 'usd')).not.toBe('$50.00')
  })

  it('floors the headroom at zero and says which side it could not read', () => {
    // An over-committed cap is a real state and "minus $10.00 of headroom" is
    // not an amount anybody can spend; an unmeasured side is not a headroom of
    // zero but a question the console cannot answer.
    expect(formatReading(headroomOf(measured(50), measured(60)), 'usd')).toBe('$0.00')
    expect(headroomOf(measured(50), measured(40)).value).toBe(10)
    const noCap = headroomOf(unavailable('the document has no total.daily_cap_usd'), measured(40))
    expect(noCap.value).toBeNull()
    expect(noCap.because).toContain('daily_cap_usd')
    const noCount = headroomOf(measured(50), unavailable('the document has no total.counted_usd'))
    expect(noCount.value).toBeNull()
    expect(noCount.because).toContain('counted_usd')
  })

  it('draws the three segments of the cap when the document measures a lease', () => {
    const tree = renderLive(
      factory,
      fleetModel({ counted_usd: 50, spent_usd: 12.5, leased_usd: 37.5, daily_cap_usd: 100, percent: 50 }),
      'light',
    )
    expect(segmentNames(tree)).toEqual(['spent', 'leased', 'headroom'])
    // Each width is its share of the cap: spent 12.5 of 100, leased 37.5 of 100,
    // headroom the remaining 50. The three add up to the axis and no further,
    // and each starts where the one before it ended.
    expect(segmentWidths(tree)).toEqual(['12.5', '37.5', '50'])
    expect(segmentOffsets(tree)).toEqual(['0', '12.5', '50'])
    expect(legendValues(tree)).toEqual(['$12.50', '$37.50', '$50.00'])
    // Every row here has a shape on the bar, so every row is keyed by one.
    expect(legendSwatches(tree)).toEqual(['spent', 'leased', 'headroom'])
  })

  it('stops drawing at the end of the cap when the parts overrun it', () => {
    // A lease the CLI recorded beside a spend that moved: 80 + 80 against a 100
    // cap. Each share is clamped on its own, which is not enough -- the two
    // together reach 160 and the second segment would be drawn past the end of
    // the axis, over the headroom and out of the bar. The clamp is cumulative,
    // so the bar stops at 100: the lease takes what is left of the axis and the
    // headroom, which is truly zero here, is drawn as nothing at 100.
    const tree = renderLive(
      factory,
      fleetModel({ counted_usd: 160, spent_usd: 80, leased_usd: 80, daily_cap_usd: 100, percent: 100 }),
      'light',
    )
    expect(segmentNames(tree)).toEqual(['spent', 'leased', 'headroom'])
    expect(segmentWidths(tree)).toEqual(['80', '20', '0'])
    expect(segmentOffsets(tree)).toEqual(['0', '80', '100'])
  })

  it('draws two segments and says Leased is not measured when the document lacks it', () => {
    // `leased_usd` is optional on the wire, so a recording made before it
    // shipped is this one. The split cannot be drawn, and the legend's Leased row
    // is where the console says so -- with the reading's own reason in its
    // title, which is what makes `n/a` an answer rather than a hole.
    const tree = renderLive(factory, fleetModel({ counted_usd: 50, spent_usd: undefined, leased_usd: undefined, daily_cap_usd: 100, percent: 50 }), 'light')
    expect(segmentNames(tree)).toEqual(['counted', 'headroom'])
    expect(segmentWidths(tree)).toEqual(['50', '50'])
    expect(segmentOffsets(tree)).toEqual(['0', '50'])
    expect(legendValues(tree)).toEqual(['$50.00', UNAVAILABLE_MARK, '$50.00'])
    // The rows are the accounts the bar has, not the design's three: the one
    // segment under the cap is counted, so the row above it says counted and
    // carries the swatch that keys the fill the bar painted for it. The Leased
    // row keeps its place and its `n/a`, and has no swatch, because there is no
    // leased shape on the bar for a mark to key.
    expect(legendSwatches(tree)).toEqual(['counted', null, 'headroom'])
    const leased = legendRow(tree, 'Leased')
    expect(all(leased, 'live-num')[0]?.getAttribute('title')).toBe('the document has no total.leased_usd')
    // The unmeasured value is marked as such for the stylesheet, and the two
    // measured rows are not.
    expect(all(leased, 'live-num')[0]?.classes).toContain('live-fleet-unread')
    expect(all(legendRow(tree, 'Headroom'), 'live-num')[0]?.classes).not.toContain('live-fleet-unread')
  })

  it('never puts the notional dollars on the cap axis or in a sum', () => {
    // **The one mutation proof.** `counted_usd` and `notional_usd` are the two
    // accounts the whole money grammar keeps apart (design section 6.1 rule 1):
    // notional dollars are a subscription's list price, they are capped by
    // nothing, and a bar that drew them beside the billed dollars -- or a
    // legend that added them -- would be reporting money that was never
    // charged. Fed the sum (50 + 3.29 against a 100 cap), the counted segment
    // below would be 53.29 wide and the headroom $46.71, and `$53.29` would be
    // printed; the assertions fail on all three.
    const model = fleetModel({ counted_usd: 50, spent_usd: undefined, leased_usd: undefined, daily_cap_usd: 100, percent: 50 })
    const tree = renderLive(factory, model, 'light')
    const panel = one(tree, 'live-fleet')
    expect(drawn(panel)).not.toContain('$53.29')
    expect(segmentWidths(tree)).toEqual(['50', '50'])
    expect(legendValues(tree)).toEqual(['$50.00', UNAVAILABLE_MARK, '$50.00'])
    // And the notional figure is drawn once, in the hero: it is a reading of
    // its own, outside the cap, and the bar carries none of it.
    expect(texts(tree, 'live-total-notional')).toEqual([`${NOTIONAL_MARK}$3.29`])
    expect(texts(one(tree, 'live-fleet-hero'), 'live-total-notional')).toEqual([`${NOTIONAL_MARK}$3.29`])
    expect(drawn(one(tree, 'live-fleet-legend'))).not.toContain(NOTIONAL_MARK)
  })

  it('shows the full-cap banner only when the headroom is measured and gone', () => {
    const full = renderLive(factory, fleetModel({ counted_usd: 50, daily_cap_usd: 50, percent: 100 }), 'light')
    expect(all(full, 'live-fleet-full')).toHaveLength(1)
    expect(drawn(one(full, 'live-fleet-full'))).toContain('the cap is fully committed')
    const room = renderLive(factory, fleetModel({ counted_usd: 50, spent_usd: undefined, leased_usd: undefined, daily_cap_usd: 100, percent: 50 }), 'light')
    expect(all(room, 'live-fleet-full')).toHaveLength(0)
  })

  it('draws an empty track, and no banner, when no cap was read', () => {
    // A cap nobody read is not a cap of zero: the bar keeps its track so the
    // panel still looks like a bar, draws no segment in it, and says `n/a`
    // rather than showing a fill at nothing.
    const frame = theLastFrame()
    const model = buildLiveModel(
      {
        ...frame,
        total: {
          ...frame.total,
          dailyCapUsd: unavailable('the document has no total.daily_cap_usd'),
          percent: unavailable('the document has no total.percent'),
        },
      },
      new BurnHistory(),
    )
    const tree = renderLive(factory, model, 'light')
    expect(all(tree, 'live-fleet-track')).toHaveLength(1)
    expect(all(tree, 'live-fleet-segment')).toHaveLength(0)
    expect(one(tree, 'live-cap').textContent).toBe(UNAVAILABLE_MARK)
    expect(legendValues(tree)[2]).toBe(UNAVAILABLE_MARK)
    expect(all(tree, 'live-fleet-full')).toHaveLength(0)
  })

  it('counts the runs that are not queued as active, and says when a heartbeat is lost', () => {
    const lost = renderLive(factory, modelOf(theLastFrame()), 'light')
    expect(one(lost, 'live-fleet-active').textContent).toBe('1')
    expect(texts(lost, 'live-fleet-lost')).toEqual(['heartbeat lost'])

    // The same frame with the lost run healthy: the count is unchanged and the
    // word is gone, because the note is about the heartbeat and not the count.
    const healthy = theLastFrame()
    const runs = healthy.runs.map((run) => ({ ...run, health: 'ok' }))
    const tree = renderLive(factory, buildLiveModel({ ...healthy, runs }, new BurnHistory()), 'light')
    expect(one(tree, 'live-fleet-active').textContent).toBe('1')
    expect(all(tree, 'live-fleet-lost')).toHaveLength(0)
  })

  it('counts the queued runs and says what they are waiting for', () => {
    const tree = renderLive(factory, modelOf(theLastFrame()), 'light')
    expect(one(tree, 'live-fleet-queued').textContent).toBe('1')
    expect(texts(tree, 'live-fleet-waiting')).toEqual(['waiting for a lease'])
    // A queued run is not an active one: the two cells are the two halves of
    // the same list, and a row counted in both would say the fleet is busier
    // than it is.
    expect(one(tree, 'live-fleet-active').textContent).toBe('1')

    // A fleet with nothing queued is waiting for nothing: the note is drawn
    // only where it is true of the number beside it, the way the active cell's
    // lost-heartbeat note is.
    const empty = theLastFrame()
    const runs = empty.runs.map((run) => ({ ...run, queued: false }))
    const mover = renderLive(factory, buildLiveModel({ ...empty, runs }, new BurnHistory()), 'light')
    expect(one(mover, 'live-fleet-queued').textContent).toBe('0')
    expect(all(mover, 'live-fleet-waiting')).toHaveLength(0)
  })

  it('names the panel, and hides the shape the legend already spells out', () => {
    const element = one(renderLive(factory, modelOf(theLastFrame()), 'light'), 'live-fleet')
    expect(element.tag).toBe('section')
    expect(element.getAttribute('aria-label')).toBe('Fleet budget')
    // The bar and its segments are decorative and hidden: every value on them is
    // written out in the legend, so nothing is reachable only through the
    // picture (design section 6.1 rule 3).
    const tree = renderLive(factory, modelOf(theLastFrame()), 'light')
    expect(one(tree, 'live-fleet-bar').getAttribute('aria-hidden')).toBe('true')
    // Nothing on the bar is a number: the shape carries no text a reader would
    // be told and the legend does not already spell out.
    expect(drawn(one(tree, 'live-fleet-bar'))).toBe('')
  })

  it('sets no attribute that could act, on any node of the panel', () => {
    const tree = panel(modelOf(theLastFrame()))
    const forbidden = new Set(['style', ['h', 'ref'].join(''), 'src'])
    for (const node of walk(tree)) {
      for (const name of node.attributes.keys()) {
        expect(forbidden.has(name) || name.startsWith('on'), `${node.tag}[${name}]`).toBe(false)
      }
    }
    expect(() => new FakeScreen('div').setAttribute('style', 'width: 1px')).toThrow()
  })
})

describe('the contradiction', () => {
  it('is reported only where a run\'s own age makes it impossible', () => {
    // A 45 s old run cannot have been quiet for 60 s, so an `silent` flag on it
    // is a document the console should say something about.
    expect(contradiction('silent', 45)).toContain('45 s')
    expect(contradiction('lost', 45)).toContain('45 s')
    // A long-running run can be either: quiet for 100 s is `silent`, and quiet
    // for five seconds is `ok`, and both of those are 30 minutes old.
    expect(contradiction('silent', 1800)).toBeNull()
    expect(contradiction('lost', 1800)).toBeNull()
    expect(contradiction('ok', 45)).toBeNull()
    expect(contradiction('hibernating', 45)).toBeNull()
  })

  it('does not take an inherited Object.prototype member for a health it knows', () => {
    // `HEALTH_SEVERITY` is an object literal, so `HEALTH_SEVERITY['constructor']`
    // is `Object` and not `undefined`: without an own-property check the screen
    // would draw a contradiction sentence, in the console's own voice, for a
    // health value the console has never heard of.
    for (const inherited of ['constructor', 'toString', 'valueOf', 'hasOwnProperty', '__proto__']) {
      expect(contradiction(inherited, 45), inherited).toBeNull()
    }
  })

  it('says nothing about a run whose age the document does not carry', () => {
    // `age_seconds` is `*float64` with no `omitempty` and `schema.ts` reads it
    // on the tolerant axis, so `null` is an accepted document. `watchHealth`
    // calls it ok, and a function that let any flag "beat" that would announce
    // that a run "is only null s old" -- a sentence, and the word `null`, on a
    // screen, from a bound nobody measured.
    expect(contradiction('silent', null)).toBeNull()
    expect(contradiction('lost', null)).toBeNull()
    expect(contradiction('lost', Number.NaN)).toBeNull()
    expect(contradiction('lost', -1)).toBeNull()
  })

  it('draws no contradiction for a silent run with no measured age', () => {
    // The whole way through: model and screen. `age_seconds: null` is what the
    // wire writes for a run whose start the registry does not have.
    const frame = frameAt('2026-10-15T12:01:40Z')
    const document: LiveDocument = {
      ...frame,
      runs: frame.runs.map((run) => ({ ...run, ageSeconds: unavailable('the document has no runs[].age_seconds') })),
    }
    const model = buildLiveModel(document, new BurnHistory())
    const row = model.repos[0]?.runs[0]
    expect(row?.flags.map((flag) => flag.name)).toEqual(['silent'])
    expect(row?.contradiction).toBeNull()

    const tree = renderLive(factory, model, 'light')
    // The word `null` is nowhere on the screen: not as a sentence and not as a
    // drawn value.
    expect(drawn(tree)).not.toContain('null')
    expect(all(tree, 'live-run-contradiction')).toEqual([])
  })
})

describe('the burn history', () => {
  it('keeps the samples inside the window `watch` uses, at exactly 300 s', () => {
    const history = new BurnHistory()
    history.observe('r', { at: '2026-10-15T12:00:00Z', reading: measured(0.01) })
    history.observe('r', { at: '2026-10-15T12:02:00Z', reading: measured(0.02) })
    history.observe('r', { at: '2026-10-15T12:04:59Z', reading: measured(0.03) })

    // 299 s after the first sample: it is still in the window.
    expect(history.window('r', '2026-10-15T12:04:59Z').map((one) => one.at)).toEqual([
      '2026-10-15T12:00:00Z',
      '2026-10-15T12:02:00Z',
      '2026-10-15T12:04:59Z',
    ])
    // Exactly 300 s later: `State.Burn` drops what is `Before(cut)`, so a
    // sample *at* the cut stays -- and one second more takes it out.
    expect(history.window('r', '2026-10-15T12:05:00Z')).toHaveLength(3)
    expect(history.window('r', '2026-10-15T12:05:01Z').map((one) => one.at)).toEqual([
      '2026-10-15T12:02:00Z',
      '2026-10-15T12:04:59Z',
    ])
  })

  it('breaks the sparkline at a reading nobody made', () => {
    // A bar the document does not carry is a gap and not a zero
    // (`availability.ts`), and a line drawn through it would be a measurement
    // in ink. `seriesSegments` is the console's own answer to that, and the
    // model reports the count so a screen cannot quietly draw one segment.
    // The history is keyed by the repository's slug, which is what
    // `buildLiveModel` observes under, so the model the screen is rendered from
    // is the same one this run of samples belongs to.
    const slug = theLastFrame().repos[0]?.slug ?? ''
    const history = new BurnHistory()
    history.observe(slug, { at: '2026-10-15T12:00:00Z', reading: measured(0.01) })
    history.observe(slug, { at: '2026-10-15T12:01:00Z', reading: unavailable('the document has no burn') })
    history.observe(slug, { at: '2026-10-15T12:02:00Z', reading: measured(0.03) })
    const spark = sparkline(history, slug, '2026-10-15T12:02:30Z')
    expect(spark.segments).toBe(2)
    expect(spark.bars).toHaveLength(3)
    expect(formatReading(spark.bars[1]?.height ?? unavailable('none'), 'percent')).toBe(GAP_MARK)
    // The bar is a gap, and the mark is not the whole of it: the reason the
    // sample is missing is what the tooltip has to carry, because `n/a` over a
    // reason tells a person less than the reason does.
    const tree = renderLive(
      factory,
      buildLiveModel(theLastFrame(), history),
      'light',
    )
    const gapBars = all(tree, 'live-burn-bar').filter((bar) => bar.getAttribute('data-state') === 'gap')
    expect(gapBars.length).toBeGreaterThan(0)
    expect(gapBars[0]?.getAttribute('title')).toBe('the document has no burn')
    expect(spark.bars[0]?.height.state).toBe('measured')
    expect(formatReading(spark.bars[2]?.height ?? unavailable('none'), 'percent')).toBe('100.0%')
  })

  it('scales the bars to the window\'s own highest rate, not to a fixed axis', () => {
    const history = new BurnHistory()
    history.observe('r', { at: '2026-10-15T12:00:00Z', reading: measured(0.01) })
    history.observe('r', { at: '2026-10-15T12:01:00Z', reading: measured(0.04) })
    const spark = sparkline(history, 'r', '2026-10-15T12:02:00Z')
    expect(formatReading(spark.bars[0]?.height ?? unavailable('none'), 'percent')).toBe('25.0%')
    expect(formatReading(spark.bars[1]?.height ?? unavailable('none'), 'percent')).toBe('100.0%')
  })

  it('stays bounded however long the feed runs', () => {
    // The caller keeps one history across frames, so a feed at a document a
    // second for a day is 86,400 arrivals per repository. What `window` can
    // return is one window's worth, and everything older than the newest stamp
    // minus `BurnWindow` can never be drawn again -- so it is dropped on the
    // way in rather than held for the life of the window.
    const history = new BurnHistory()
    const base = Date.parse('2026-10-15T00:00:00Z')
    for (let second = 0; second < 2000; second++) {
      history.observe('r', {
        at: new Date(base + second * 1000).toISOString().replace(/\.\d{3}Z$/, 'Z'),
        reading: measured(0.01),
      })
    }
    const end = new Date(base + 1999 * 1000).toISOString().replace(/\.\d{3}Z$/, 'Z')
    expect(history.window('r', end).length).toBeLessThanOrEqual(LIVE_BURN_WINDOW_SECONDS + 1)
    // And the pruning kept exactly the window: the oldest sample left is the
    // one at the cut, which is the sample `State.Burn` also keeps.
    expect(history.window('r', end)[0]?.at).toBe(
      new Date(base + (1999 - LIVE_BURN_WINDOW_SECONDS) * 1000).toISOString().replace(/\.\d{3}Z$/, 'Z'),
    )
  })

  it('rolls the window across the frames a replay hands it', () => {
    // The sparkline is not one frame's rate: a caller replaying the feed keeps
    // one history and the model observes each frame into it, so the window is
    // the last five minutes of *arrivals* rather than the last document. The
    // frame's own stamp -- the CLI's `now`, never the console's clock -- is
    // what the window is taken at, which is what makes a replay reproducible.
    const history = new BurnHistory()
    const repo = theLastFrame().repos[0]
    if (repo === undefined) throw new Error('no repository row')
    for (const frame of frames()) {
      const model = buildLiveModel(frame, history)
      expect(model.repos[0]?.spark.bars.length ?? 0).toBeGreaterThan(0)
    }
    // Five frames over four minutes and twenty-one seconds. At the last
    // frame's own stamp the 300 s window reaches back to 11:59:21, which is
    // before every one of them; forty-four seconds later it reaches 12:00:05
    // and leaves the four frames from there on.
    expect(history.window(repo.slug, AT_CAP)).toHaveLength(5)
    expect(history.window(repo.slug, '2026-10-15T12:05:05Z')).toHaveLength(4)
  })
})

describe('the rows the fixture does not carry', () => {
  /** withRun is the fixture's lost frame with its one registry run edited. */
  function edited(change: Partial<RunView>): LiveDocument {
    const frame = frameAt('2026-10-15T12:01:40Z')
    return { ...frame, runs: frame.runs.map((run) => ({ ...run, health: 'ok', deadline: 'ok', stuck: false, ...change })) }
  }

  it('draws a run past 80% of its stage as an amber row, end to end', () => {
    const tree = renderLive(factory, buildLiveModel(edited({ deadline: 'near' }), new BurnHistory()), 'light')
    const rows = all(tree, 'live-run').filter((row) => row.getAttribute('data-queued') === 'false')
    expect(rows.length).toBeGreaterThan(0)
    for (const row of rows) expect(row.getAttribute('data-tone')).toBe('amber')
    const flags = all(tree, 'live-run-flag')
    expect(flags.map((flag) => flag.textContent)).toContain('near deadline')
    expect(flags[0]?.getAttribute('data-tone')).toBe('amber')
    expect(flags[0]?.getAttribute('title')).toContain('80%')
  })

  it('draws a run over its deadline as an alarm row', () => {
    const tree = renderLive(factory, buildLiveModel(edited({ deadline: 'over' }), new BurnHistory()), 'light')
    const row = all(tree, 'live-run').find((one) => one.getAttribute('data-queued') === 'false')
    expect(row?.getAttribute('data-tone')).toBe('alarm')
    expect(texts(tree, 'live-run-flag')).toContain('over deadline')
  })

  it('draws a stuck run with its class, its flag and an alarm tone', () => {
    const tree = renderLive(factory, buildLiveModel(edited({ stuck: true }), new BurnHistory()), 'light')
    const row = all(tree, 'live-run').find((one) => one.classes.includes('live-run-stuck'))
    expect(row?.getAttribute('data-tone')).toBe('alarm')
    expect(texts(tree, 'live-run-flag')).toContain('stuck')
  })

  it('keeps an amber deadline under a worse health: the row takes the worst tone', () => {
    const tree = renderLive(
      factory,
      buildLiveModel(edited({ deadline: 'near', health: 'lost' }), new BurnHistory()),
      'light',
    )
    const row = all(tree, 'live-run').find((one) => one.getAttribute('data-queued') === 'false')
    expect(row?.getAttribute('data-tone')).toBe('alarm')
    // The health flag is the chip's own word (`lost`), so it is not drawn beside
    // it; the deadline flag, which is a different word, still is.
    expect(texts(tree, 'live-run-flag')).toEqual(['near deadline'])
    expect(all(tree, 'live-run-chip')[0]?.getAttribute('title')).toContain('180 s')
  })
})

describe('the burn bars of a repository with no history', () => {
  it('draws a gap, in the gap\'s mark and state and with its reason as the title', () => {
    // An unparseable `now` gives the window no stamp to end at, so the history
    // has nothing to draw: the console has not seen a sample, which
    // availability.ts calls a gap (an em dash), not an unavailable value (n/a).
    const frame = { ...theLastFrame(), now: 'not a time' }
    const tree = renderLive(factory, buildLiveModel(frame, new BurnHistory()), 'light')
    const bars = all(tree, 'live-burn-bar')
    expect(bars).toHaveLength(1)
    expect(bars[0]?.getAttribute('data-state')).toBe('gap')
    expect(bars[0]?.textContent).toBe(GAP_MARK)
    expect(bars[0]?.getAttribute('title') ?? '').not.toBe('')
  })
})

describe('a burn the document marks as not known', () => {
  // `schema.ts` already maps `known: false` to an unavailable rate, so the
  // decoded path never reaches the screen's own guard. The screen must not
  // lean on that: a burn handed to it with `known: false` and a measured zero
  // beside the flag is drawn as unavailable, never as $0/min.
  const unknownBurn = { known: false, usdPerMin: measured(0), fast: false }

  it('draws n/a and a gap bar, never a measured $0/min', () => {
    const frame = theLastFrame()
    const document: LiveDocument = {
      ...frame,
      repos: frame.repos.map((repo) => ({ ...repo, burn: unknownBurn })),
    }
    const model = buildLiveModel(document, new BurnHistory())
    expect(model.repos.length).toBeGreaterThan(0)
    for (const repo of model.repos) {
      expect(repo.burnPerMin.state).toBe('unavailable')
      expect(formatReading(repo.burnPerMin, 'usd')).toBe('n/a')
    }
    const tree = renderLive(factory, model, 'light')
    const rates = all(tree, 'live-burn-rate')
    expect(rates.length).toBeGreaterThan(0)
    for (const rate of rates) {
      expect(rate.textContent).toBe('n/a')
    }
    const bars = all(tree, 'live-burn-bar')
    expect(bars.length).toBeGreaterThan(0)
    for (const bar of bars) expect(bar.getAttribute('data-state')).toBe('gap')
  })
})

describe('the rendered screen', () => {
  it('draws every part the design names', () => {
    const tree = renderLive(factory, modelOf(theLastFrame()), 'light')
    expect(tree.tag).toBe('section')
    expect(one(tree, 'live').getAttribute('data-theme')).toBe('light')
    expect(texts(tree, 'live-project')).toEqual(['<project>'])
    expect(texts(tree, 'live-mode')).toEqual(['observe'])
    expect(texts(tree, 'live-day')).toEqual(['2026-10-15'])
    expect(texts(tree, 'live-connection')).toEqual(['live'])
    expect(all(tree, 'live-repo')).toHaveLength(1)
    expect(texts(tree, 'live-repo-name')).toEqual(['r-4c25cf20/r-f1aa9214'])
    expect(texts(tree, 'live-runs')).toHaveLength(1)
  })

  it('keeps the title row’s hooks, and draws the connection state as a word', () => {
    // The redesign's title row is the same facts in the design's order: the
    // project, then the connection -- a decorative dot, the CLI's own state word
    // and the age reading beside it -- then the mode and the day. **The word is
    // the state**: the dot's tone is the second channel and never the only one,
    // which is why the dot carries the same `data-state` the word does (the word
    // as drawn, read back off the cell, exactly as the connection cell does it)
    // and why this case reads the word.
    const model = modelOf(theLastFrame())
    const tree = renderLive(factory, model, 'light')
    const header = one(tree, 'live-header')
    expect(header.tag).toBe('header')
    expect(
      header.childNodes().slice(0, 4).map((node) => (node as FakeScreen).classes.join(' ')),
    ).toEqual([
      'live-project',
      'live-connection-dot',
      'live-connection',
      // The age is the numeric cell's own hook as well: it is a reading, drawn
      // through `numberCell` like every other figure on this screen.
      'live-num live-connection-age',
    ])
    expect(texts(header, 'live-project')).toEqual(['<project>'])
    expect(texts(header, 'live-connection')).toEqual(['live'])
    expect(texts(header, 'live-connection-age')).toEqual([
      formatReading(model.connection.age, 'seconds'),
    ])
    expect(texts(header, 'live-mode')).toEqual(['observe'])
    expect(texts(header, 'live-day')).toEqual(['2026-10-15'])

    const dot = one(header, 'live-connection-dot')
    expect(dot.getAttribute('aria-hidden')).toBe('true')
    expect(dot.textContent).toBeNull()
    expect(dot.getAttribute('data-state')).toBe('live')
  })

  it('tones the dot and the word by the state the CLI reported', () => {
    // A state that is not live is drawn twice over -- the dot's tone and the
    // word -- because a colour alone is not a status (design section 3.5). The
    // attribute is the word as drawn on both, so a rule can key either on it,
    // and a state `putText` refused is `n/a` on both rather than the raw string.
    for (const state of ['stale', 'offline', 'refused']) {
      const frame = theLastFrame()
      const document: LiveDocument = { ...frame, connection: { ...frame.connection, state } }
      const header = one(
        renderLive(factory, buildLiveModel(document, new BurnHistory()), 'light'),
        'live-header',
      )
      expect(texts(header, 'live-connection')).toEqual([state])
      expect(one(header, 'live-connection-dot').getAttribute('data-state')).toBe(state)
      expect(one(header, 'live-connection').getAttribute('data-state')).toBe(state)
    }
  })

  it('draws each theme into the same screen', () => {
    for (const theme of LIVE_THEMES) {
      const tree = renderLive(factory, modelOf(theLastFrame()), theme)
      expect(one(tree, 'live').getAttribute('data-theme')).toBe(theme)
    }
  })

  it('renders the fixture\'s queued run, with its spend drawn as n/a', () => {
    const tree = renderLive(factory, modelOf(theLastFrame()), 'dark')
    expect(texts(tree, 'live-run-id')).toContain(QUEUED_RUN)
    const queued = all(tree, 'live-run').filter(
      (row) => row.getAttribute('data-queued') === 'true',
    )
    expect(queued).toHaveLength(1)
    expect(queued[0]?.classes).toContain('live-run-queued')
    expect(drawn(tree)).toContain(UNAVAILABLE_MARK)
  })

  it('draws a flag as a word with its threshold in the title', () => {
    // A `silent` health is a flag whose word is not the chip's own (`running`),
    // so it is drawn beside the chip rather than folded into it.
    const frame = frameAt('2026-10-15T12:04:20Z')
    const runs = frame.runs.map((run) => ({ ...run, health: 'silent' }))
    const tree = renderLive(factory, buildLiveModel({ ...frame, runs }, new BurnHistory()), 'light')
    const flags = all(tree, 'live-run-flag')
    expect(flags.map((flag) => flag.textContent)).toEqual(['silent'])
    expect(flags[0]?.getAttribute('title')).toContain('no heartbeat for over')
    expect(flags[0]?.getAttribute('data-flag')).toBe('silent')
    expect(flags[0]?.getAttribute('data-tone')).toBe('amber')
  })

  it('says a run\'s state once: a flag that is the chip\'s own word joins its title', () => {
    // The bucket's `queued` and the health's `lost` are the two flags whose name
    // *is* the state the chip spells. Drawn beside the chip they would repeat it,
    // and the pipeline line above says it too, so the word is drawn once and the
    // flag's reason moves onto the chip's `title`.
    const tree = renderLive(factory, modelOf(frameAt('2026-10-15T12:04:20Z')), 'light')
    const chips = all(tree, 'live-run-chip')
    expect(chips.map((chip) => chip.textContent)).toContain('lost')
    const lost = chips.find((chip) => chip.textContent === 'lost')
    expect(lost?.getAttribute('title')).toContain('180 s')
    expect(texts(tree, 'live-run-flag')).not.toContain('lost')

    // The last frame is the one that carries a queued run (and a lost one).
    const queued = renderLive(factory, modelOf(theLastFrame()), 'light')
    const queuedChip = all(queued, 'live-run-chip').find((chip) => chip.textContent === 'queued')
    expect(queuedChip).toBeDefined()
    expect(queuedChip?.getAttribute('title')).toContain('runs bucket only')
    expect(texts(queued, 'live-run-flag')).not.toContain('queued')
  })

  it('draws the notional mark on an OAuth run\'s spend', () => {
    const tree = renderLive(factory, modelOf(frameAt('2026-10-15T12:00:05Z')), 'light')
    expect(texts(tree, 'live-run-spend')).toEqual([`${NOTIONAL_MARK}$12.54`])
  })

  it('draws the rigor mode as a chip on a run the join knows, and no chip for one it does not', () => {
    // Owner request 2026-10-25: the mode on a Live row, as the one word the row
    // has room for -- the whole answer, with the layer and the areas, is on the
    // run's own screen (`run.ts`'s `rigorLine`). It is drawn in this build's own
    // word for the mode, like the Projects cell and `runs ls`'s MODE column.
    const frame = theLastFrame()
    const index = new Map([
      [OAUTH_RUN, { mode: 'poc', from: 'request', areas: [] }],
      [QUEUED_RUN, { mode: 'hotfix', from: 'repo', areas: [] }],
    ])
    const tree = renderLive(factory, buildLiveModel(frame, new BurnHistory(), null, index), 'light')
    expect(texts(tree, 'live-run-mode')).toEqual(['POC', 'Hotfix'])
    // The chip is on the run's own row, not loose in the panel.
    const rows = all(tree, 'live-run')
    expect(rows.map((row) => texts(row, 'live-run-mode'))).toEqual([['POC'], ['Hotfix']])
    // With no join the screen is the screen it always drew: no chip anywhere,
    // and no empty span, which would add a flex gap to every row.
    const plain = renderLive(factory, modelOf(frame), 'light')
    expect(all(plain, 'live-run-mode')).toHaveLength(0)
  })

  it('draws a mode this build does not know as the bounded text it is', () => {
    // The direction the reader fails in, on the chip: a mode a newer CLI
    // resolved is kept and put through the text path rather than hidden, and a
    // value shaped like a credential is masked in place.
    const frame = theLastFrame()
    const unknown = new Map([[OAUTH_RUN, { mode: 'standard', from: 'reached', areas: [] }]])
    const tree = renderLive(factory, buildLiveModel(frame, new BurnHistory(), null, unknown), 'light')
    expect(texts(tree, 'live-run-mode')).toEqual(['standard'])
    const token = githubToken()
    const shaped = new Map([[OAUTH_RUN, { mode: token, from: 'repo', areas: [] }]])
    const masked = renderLive(factory, buildLiveModel(frame, new BurnHistory(), null, shaped), 'light')
    expect(texts(masked, 'live-run-mode')).toEqual([TOKEN_PLACEHOLDER])
    expect(drawn(masked)).not.toContain(token)
  })

  it('writes every cell as text and sets no attribute that could act', () => {
    // The screen is handed a fake whose navigating attribute, resource
    // attributes, `style` attribute and every `on*` handler throw, so a screen
    // that reached for one fails here. What is left is the set below: the
    // class hooks the design's density rules need and `data-` readings a test
    // and the stylesheet read, the three accessibility attributes (which cannot
    // make the window do anything), `type` on a `button` (which is what
    // `writes.ts`'s `disabledWrite` draws, and it can only *stop* a form
    // submission), and -- **on a node the SVG namespace made, and only there**
    // -- the presentation attributes the cap bar's geometry is built from. The
    // namespace is what the fake records, not the tag: a `rect` made with
    // `create` is an HTML element and is not held to this set. `path` and its
    // `d` join it with the review panel's decorative mark (console redesign,
    // step 2c): the geometry of a shape is the one thing a stylesheet cannot
    // carry, and the stroke, the fill and the joins are all `flight-deck.css`'s.
    const MAXIMAL = new Set(['svg', 'rect', 'path'])
    const SVG_ATTRIBUTES = new Set([
      'viewBox',
      'preserveAspectRatio',
      'width',
      'height',
      'x',
      'y',
      'rx',
      'd',
    ])
    // `aria-disabled` joins the set with the redesign: the Halt project and
    // Reap buttons are real `button`s a reader is told are not available. The
    // attribute describes the element to assistive technology, it schedules no
    // handler of its own, and a control that carried no text of its own would
    // still be caught by the tests that click it and watch nothing happen.
    const ACCESSIBILITY = new Set(['role', 'aria-label', 'aria-hidden', 'aria-disabled'])
    const tree = renderLive(factory, modelOf(theLastFrame()), 'light')
    // The panel draws at least one of each, or the widening above would be a
    // licence for attributes nothing sets.
    expect(walk(tree).some((node) => node.namespace === 'svg')).toBe(true)
    for (const node of walk(tree)) {
      for (const name of node.attributes.keys()) {
        expect(
          name === 'class' ||
            name === 'title' ||
            name.startsWith('data-') ||
            ACCESSIBILITY.has(name) ||
            (name === 'type' && node.tag === 'button') ||
            (node.namespace === 'svg' && MAXIMAL.has(node.tag) && SVG_ATTRIBUTES.has(name)),
          `${node.tag}[${name}]`,
        ).toBe(true)
      }
    }
  })

  it('puts the sanitized value, not the document\'s, into an attribute', () => {
    // A state `putText` refuses is drawn as `n/a`. `data-state` is read back off
    // the drawn cell for exactly that reason: an attribute echoing the raw
    // string would put back on screen what the refusal kept off it, and a
    // reviewer checking attribute *names* (the test above) would not see it.
    const frame = theLastFrame()
    const raw = 'liv\nve'
    const document: LiveDocument = {
      ...frame,
      connection: { ...frame.connection, state: raw },
    }
    const tree = renderLive(factory, buildLiveModel(document, new BurnHistory()), 'light')
    const connection = one(tree, 'live-connection')
    expect(connection.textContent).toBe(UNAVAILABLE_MARK)
    expect(connection.getAttribute('data-state')).toBe(UNAVAILABLE_MARK)
    expect(connection.getAttribute('data-state')).not.toContain('\n')
  })

  it('leaves the anchor and its destination out of the live screen entirely', () => {
    // Design section 4.3's gap 7 and Task 2.0's rule: the console builds no
    // link on this screen, so there is not one element here that could be
    // clicked into a navigation.
    const tree = renderLive(factory, modelOf(theLastFrame()), 'light')
    expect(walk(tree).filter((node) => node.tag === 'a')).toEqual([])
  })
})

// ---------------------------------------------------------------------------
// The scan Task 2.0's non-recursive one does not reach.
// ---------------------------------------------------------------------------

const SRC = path.join(HERE, '..', '..', 'src')

/** sourceFiles lists every `.ts` file under a directory, at any depth, so the
 * scan runs over what is actually there -- including a directory added after
 * the scan was written. */
function sourceFiles(directory: string): readonly string[] {
  const out: string[] = []
  for (const entry of readdirSync(directory, { withFileTypes: true }).sort((a, b) =>
    a.name < b.name ? -1 : 1,
  )) {
    const full = path.join(directory, entry.name)
    if (entry.isDirectory()) out.push(...sourceFiles(full))
    else if (entry.name.endsWith('.ts')) out.push(full)
  }
  return out
}

/** FORBIDDEN is `sanitize.test.ts`'s list, assembled the same way so this file
 * does not spell the thing it scans for. */
const FORBIDDEN: readonly string[] = Object.freeze([
  ['inner', 'HTML'].join(''),
  ['outer', 'HTML'].join(''),
  ['insertAdjacent', 'HTML'].join(''),
  ['document', '.write'].join(''),
  ['new ', 'Function('].join(''),
  ['ev', 'al('].join(''),
  'toFixed(',
  'toLocaleString(',
  ['Intl', '.NumberFormat'].join(''),
])

describe('src/renderer/, at any depth', () => {
  it('reaches the screens directory', () => {
    const renderer = path.join(SRC, 'renderer')
    const files = sourceFiles(renderer)
    expect(files.length).toBeGreaterThan(0)
    // The scan is only worth anything if it can see the file this task added:
    // a recursive walk that silently stopped at renderer/ would pass while
    // covering nothing new.
    expect(files.some((file) => file.endsWith(path.join('screens', 'live.ts')))).toBe(true)
  })

  it('still recognises a forbidden assignment when it sees one', () => {
    const planted = `element.${['inner', 'HTML'].join('')} = jobTitle`
    expect(planted.includes(FORBIDDEN[0] ?? '')).toBe(true)
    expect('element.textContent = jobTitle'.includes(FORBIDDEN[0] ?? '')).toBe(false)
  })

  it('never interprets a string as markup and never formats a bare number', () => {
    for (const file of sourceFiles(path.join(SRC, 'renderer'))) {
      const source = readFileSync(file, 'utf8')
      for (const token of FORBIDDEN) {
        expect(source.includes(token), `${file}: ${token}`).toBe(false)
      }
    }
  })
})

describe('a repository the feed names by its slug (review round 2 of PR 494)', () => {
  it('draws the slash-free slug repoDisplayName falls back to, with the slug class', () => {
    const frame = theLastFrame()
    const repos = frame.repos.map((repo) => ({ ...repo, repo: 'teammate-app-dc4d4e59e884fc44' }))
    const tree = renderLive(factory, modelOf({ ...frame, repos } as LiveDocument), 'light')
    expect(texts(tree, 'live-repo-name')).toEqual(['teammate-app-dc4d4e59e884fc44'])
  })
})
