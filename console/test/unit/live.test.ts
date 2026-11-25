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
  notional,
  unavailable,
} from '../../src/shared/availability'
import { TOKEN_PLACEHOLDER } from '../../src/shared/sanitize'
import { decodeRunsList, decodeSnapshot } from '../../src/shared/schema'
import type { JSONDoc } from '../../src/source/source'
import { RUNS_LIST_MAX_LIMIT } from '../../src/main/ipc'
import { githubToken } from '../support/fakeSecrets'
import type { LiveDocument, RunsListRow, RunView } from '../../src/shared/schema'
import {
  BurnHistory,
  HourHistory,
  LIVE_BURN_WINDOW_SECONDS,
  LIVE_BURN_WINDOW_WORD,
  LIVE_CAPS_HIDDEN_WORD,
  LIVE_HOUR_MAX_SAMPLES,
  LIVE_HOUR_WINDOW_SECONDS,
  LIVE_HOUR_WINDOW_WORD,
  LIVE_LOST_AFTER_SECONDS,
  LIVE_NEAR_DEADLINE_FRACTION,
  LIVE_OLDER_BUSY,
  LIVE_OLDER_PAGE,
  LIVE_OLDER_TITLE,
  LIVE_OLDER_WORD,
  LIVE_SILENT_AFTER_SECONDS,
  LIVE_STALE_AFTER_SECONDS,
  LIVE_THEMES,
  PIPELINE_SEGMENTS,
  buildLiveModel,
  concurrentOf,
  contradiction,
  deadlineFlag,
  headroomOf,
  healthFlag,
  hoursLeftOf,
  liveOlderOf,
  liveOlderReading,
  liveOlderRefused,
  olderPageOf,
  olderRequest,
  perHourOf,
  pipelineOf,
  redZoneOf,
  renderLive,
  runStateOf,
  sparkline,
  stageSegment,
  watchHealth,
} from '../../src/renderer/screens/live'
import type {
  LiveFactory,
  LiveModel,
  LiveOlder,
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
/** their endings, which is what the RUN cell draws (`shortRun`) */
const OAUTH_ENDING = 'aa01'
const QUEUED_ENDING = 'aa03'
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

  it('draws the design’s columns, and the fields the table has no column for leave the row', () => {
    // **Console redesign step 2b: the row is a table row now.** The design's
    // runs table has RUN, TASK, PIPELINE, CODER / REVIEWER, COST, ELAPSED and
    // STATUS, and the fields the flex row used to draw beside the title -- the
    // requester, the recipe, the workflow, the verify word -- have no column.
    // They stay in the **model** (`LiveRunRow`), so a later step that wants one
    // has the document's value to draw; what this test holds is that no cell is
    // drawn for them, which is the half a reader of the screen would otherwise
    // have to guess. **The Started-by cell is not one of them any more**: it is
    // the `BY` column the live tab now follows `watch` in (D1704, `draws the BY
    // cell` below).
    const frame = theLastFrame()
    const dropped = [
      'live-run-verify',
      'live-run-recipe',
      'live-run-workflow',
      'live-run-requester',
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
    // `data-run-id` of `.live-run-id` off the row it is bound to and opens Run
    // detail with it, so the id has to be a cell of the row **and** carry the
    // whole run id the door opens -- never the drawn ending, which is a display
    // and not an identifier the CLI accepts.
    const tree = renderLive(factory, modelOf(theLastFrame()), 'light')
    const rows = all(tree, 'live-run')
    expect(rows.length).toBeGreaterThan(0)
    for (const row of rows) {
      expect(row.tag).toBe('tr')
      expect(row.getAttribute('data-queued')).not.toBeNull()
      const cells = row.childNodes().filter((child) => child.tag === 'td')
      expect(cells).toHaveLength(8)
      const id = one(row, 'live-run-id')
      expect(id.tag).toBe('td')
      expect(id.textContent).not.toBe('')
      expect(id.getAttribute('data-run-id')).not.toBeNull()
      // The door reads this cell and nothing else on the row.
      expect(all(row, 'live-run-id')).toHaveLength(1)
      expect(texts(row, 'live-run-stage')).toHaveLength(1)
      expect(texts(row, 'live-run-state')).toHaveLength(1)
      expect(texts(row, 'live-run-spend')).toHaveLength(1)
      expect(texts(row, 'live-run-age')).toHaveLength(1)
    }
    // The eight `td`s and the `th` row are the same columns: a heading with no
    // cell under it is a column the reader looks for and does not find.
    expect(texts(tree, 'live-table-head')).toEqual([
      'RUN',
      'TASK',
      'PIPELINE',
      'CODER / REVIEWER',
      'COST',
      'ELAPSED',
      'BY',
      'STATUS',
    ])
  })

  it('shows the short ID, and keeps the whole id on the cell’s door', () => {
    // **Spec part 1: the RUN column shows the last four characters.** They are
    // what a person types back into the CLI (`internal/task/shortid.go`'s
    // `ShortRun`, and the console's half of the same rule in
    // `src/shared/shortid.ts`), and the cell is still the door -- so the whole
    // id travels with it in `data-run-id`, which is what `wireRuns` reads.
    const tree = renderLive(factory, modelOf(theLastFrame()), 'light')
    expect(texts(tree, 'live-run-id')).toEqual([OAUTH_ENDING, QUEUED_ENDING])
    expect(all(tree, 'live-run-id').map((cell) => cell.getAttribute('data-run-id'))).toEqual([
      OAUTH_RUN,
      QUEUED_RUN,
    ])
  })

  it('extends a colliding pair to the shortest ending that tells them apart', () => {
    // **Spec part 3 (reconciliation F1, F15): two runs printed together that
    // share their last four characters are shown with as many extra characters
    // as it takes to tell them apart, and no more.** The extension runs through
    // the dash, because a run id's tail past the four hex digits is the start
    // time -- so the two ids below, which differ at the sixth character, are
    // `8-3bb2` and `1-3bb2`: the same pair `internal/task/shortid.go`'s red step
    // names.
    const frame = theLastFrame()
    const older = { ...theRun(frame, OAUTH_RUN), run: '20261108-115248-3bb2' }
    const newer = { ...theRun(frame, QUEUED_RUN), run: '20261107-010101-3bb2' }
    const model = buildLiveModel({ ...frame, runs: [older, newer] }, new BurnHistory())
    expect(texts(renderLive(factory, model, 'light'), 'live-run-id')).toEqual(['8-3bb2', '1-3bb2'])
    // The two cells still carry their own whole ids, which is what the doors
    // open: neither ending is an id any command would take.
    expect(
      all(renderLive(factory, model, 'light'), 'live-run-id').map((cell) => cell.getAttribute('data-run-id')),
    ).toEqual(['20261108-115248-3bb2', '20261107-010101-3bb2'])
  })

  it('cuts the ending from the id the console would draw, not from the wire text', () => {
    // **The ending is a cut of the *sanitized* id.** A run id arrives as
    // untrusted text, and the `runId` class strips an invisible character
    // (a bidi override here, `‮`) before its shape check -- so the wire
    // value below is accepted and the cell keeps the clean id as
    // `data-run-id`. An ending cut from the raw value would carry the override
    // into the four characters a person reads and types back, and would not be
    // a suffix of the id the door opens at all.
    const frame = theLastFrame()
    const runs = [{ ...theRun(frame, OAUTH_RUN), run: `${OAUTH_RUN}‮` }, theRun(frame, QUEUED_RUN)]
    const cells = all(
      renderLive(factory, buildLiveModel({ ...frame, runs }, new BurnHistory()), 'light'),
      'live-run-id',
    )
    expect(cells[0]?.textContent).toBe(OAUTH_ENDING)
    expect(cells[0]?.getAttribute('data-run-id')).toBe(OAUTH_RUN)
    for (const cell of cells) {
      expect(cell.getAttribute('data-run-id')?.endsWith(cell.textContent ?? '')).toBe(true)
    }
  })

  it('sanitizes the repository the collision rule prefixes, not only the ending', () => {
    // The cross-repository branch of `displayRuns` draws `<slug>/<ending>`, and
    // the repository is untrusted text too. Two runs of the same id under two
    // repositories are the case that branch exists for (F15); the slug in front
    // of the ending goes through the class a repository is drawn in, so a
    // zero-width space in one of them cannot ride into the cell.
    const frame = theLastFrame()
    const base = theRun(frame, OAUTH_RUN)
    const runs = [
      { ...base, slug: 'orphan​one' },
      { ...base, slug: 'orphan-two' },
    ]
    const tree = renderLive(factory, buildLiveModel({ ...frame, runs }, new BurnHistory()), 'light')
    expect(texts(tree, 'live-run-id')).toEqual([`orphanone/${OAUTH_ENDING}`, `orphan-two/${OAUTH_ENDING}`])
    expect(all(tree, 'live-run-id').map((cell) => cell.getAttribute('data-run-id'))).toEqual([
      OAUTH_RUN,
      OAUTH_RUN,
    ])
  })

  it('draws a repository’s rows in watch’s order, which is the frame’s own', () => {
    // **The order is the document's.** `watch` orders each repository's rows
    // itself (`sortRuns` in `internal/watch/build.go`, applied before `json.go`
    // emits them: `StartedAt`, then the run id) and the live tab follows it
    // (D1704), so the console draws the rows in the order they arrived rather
    // than sorting them a second time. A second sort here can only diverge:
    // **`JSONRun` carries no start stamp**, and the `age_seconds` standing in
    // for it is not the start read backwards. The CLI leaves the age null for a
    // record with no `StartedAt` -- which `sortRuns` orders **first**, as the
    // smallest start, and which an age cannot place at all -- and it clamps a
    // start in the future to zero, which `sortRuns` still orders by the real
    // stamp while no age can tell it from a run that began this second.
    const frame = theLastFrame()
    const base = theRun(frame, OAUTH_RUN)
    const runs = [
      { ...base, run: '20261015-121000-aa01', ageSeconds: unavailable('the record carries no started_at') },
      { ...base, run: '20261015-121200-aa02', ageSeconds: measured(300) },
      { ...base, run: '20261015-121500-aa03', ageSeconds: measured(100) },
    ]
    const model = buildLiveModel({ ...frame, runs }, new BurnHistory())
    expect(model.repos[0]?.runs.map((row) => row.run)).toEqual([
      '20261015-121000-aa01',
      '20261015-121200-aa02',
      '20261015-121500-aa03',
    ])
    expect(texts(renderLive(factory, model, 'light'), 'live-run-id')).toEqual(['aa01', 'aa02', 'aa03'])
  })

  it('draws the BY cell as the record wrote the launcher', () => {
    // `watch`'s runs table has a `BY` column (`internal/watch/plain.go`'s
    // `launcherFull`), and the live tab follows it (D1704). The value is drawn
    // through `shared/launcher.ts` **whole** -- the console no longer cuts at the
    // address the way the CLI's own column still does (D3213, 2026-11-21) -- and
    // a row whose record carries no launcher draws **no element at all**, never
    // another person's name.
    const frame = theLastFrame()
    const runs = frame.runs.map((run) => ({ ...run, launchedBy: 'dana.person+fugaro@example.com' }))
    const tree = renderLive(factory, buildLiveModel({ ...frame, runs }, new BurnHistory()), 'light')
    expect(texts(tree, 'live-run-started-by')).toEqual([
      'dana.person+fugaro@example.com',
      'dana.person+fugaro@example.com',
    ])
    const without = renderLive(factory, modelOf(theLastFrame()), 'light')
    expect(all(without, 'live-run-started-by')).toEqual([])
  })

  it('takes a run\'s rigor from the join by id, and draws no chip without one', () => {
    // Owner request 2026-10-25. **The row's rigor is the join, not the frame**:
    // the frame's `JSONRun` carries the run's mode and the layer that set it
    // (rigor-modes plan T1.7, CLI 0.23.0) and nothing else of the block, and the
    // console reads a run's rigor through one reader for all three screens that
    // draw it (`shared/rigor.ts`), so the row's is joined from `runs ls --json`
    // by run id (`rigorIndex`) and the caller is what makes the join. A run the
    // index does not know is a run with no chip -- never another run's mode --
    // which is what the default argument (`NO_RIGOR`) gives every caller that
    // has not made one.
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

// ---------------------------------------------------------------------------
// The Live stylesheets.
// ---------------------------------------------------------------------------

/**
 * The screen's size and its contrast are the stylesheets' facts and not this
 * file's: `screens.css` draws the burn line and the runs box's scrolling, and
 * `flight-deck.css` the hour line beside it and the table's own floor. Two
 * questions below are asked of the rules themselves -- how big a sparkline's box
 * is, and what a text colour is read against -- because neither can be answered
 * from the markup: a bar carries no width and no colour, and a rule's pair is
 * two declarations a browser resolves.
 */

/** themeSheet reads one stylesheet of the theme, failing by name rather than
 * with an ENOENT from somewhere further along. */
function themeSheet(name: string): string {
  return readFileSync(path.join(SRC, 'renderer', 'theme', name), 'utf8')
}

/** withoutAtRules removes every `@name { ... }` block with its braces matched, so
 * that the rules inside one are not read as the stylesheet's own. **This is not
 * tidiness.** `@media (forced-colors: active)` repaints these hooks in the
 * reader's own colours -- `CanvasText`, `GrayText`, `Highlight` -- which are the
 * system's and not the console's, so a ratio computed from them would be a ratio
 * nobody chose; and a `@keyframes` body is a transition's frame rather than a
 * pair a person reads at rest. */
function withoutAtRules(css: string): string {
  let out = ''
  let from = 0
  for (let at = css.indexOf('@', from); at !== -1; at = css.indexOf('@', from)) {
    out += css.slice(from, at)
    const open = css.indexOf('{', at)
    if (open === -1) return out
    let depth = 1
    let end = open + 1
    while (end < css.length && depth > 0) {
      if (css[end] === '{') depth++
      else if (css[end] === '}') depth--
      end++
    }
    from = end
  }
  return out + css.slice(from)
}

/** plainRules is a stylesheet's own rules, as their selector and their body, in
 * file order. A rule of an at-rule is not in the list (`withoutAtRules`). */
function plainRules(css: string): readonly { readonly selector: string; readonly body: string }[] {
  const source = withoutAtRules(css.replace(/\/\*[\s\S]*?\*\//g, ''))
  const rules: { selector: string; body: string }[] = []
  for (const [, selector, body] of source.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    rules.push({ selector: (selector ?? '').trim().replace(/\s+/g, ' '), body: body ?? '' })
  }
  return rules
}

/** declarationsOf is a rule body's declarations: property, in lower case, to
 * value. The last one wins, which is what a browser does with a repeated
 * property. */
function declarationsOf(body: string): ReadonlyMap<string, string> {
  const found = new Map<string, string>()
  for (const declaration of body.split(';')) {
    const colon = declaration.indexOf(':')
    if (colon === -1) continue
    found.set(declaration.slice(0, colon).trim().toLowerCase(), declaration.slice(colon + 1).trim())
  }
  return found
}

/** liveRule is the one rule of a sheet whose selector is exactly `selector`,
 * asserted to be one: two rules for the same hook would mean the declarations
 * this case reads are not the ones the window paints. */
function liveRule(sheet: string, selector: string): ReadonlyMap<string, string> {
  const found = plainRules(themeSheet(sheet)).filter((rule) => rule.selector === selector)
  expect(found, `${sheet} carries ${String(found.length)} rules for ${selector}`).toHaveLength(1)
  const first = found[0]
  if (first === undefined) throw new Error(`${sheet} carries no ${selector} rule`)
  return declarationsOf(first.body)
}

/** paint is an opaque sRGB triple, the only shape a contrast ratio is computed
 * from. */
type Paint = readonly [number, number, number]

/** hexPaint reads an opaque hex literal; anything else is refused rather than
 * skipped, because a colour this file cannot compute is exactly the colour a
 * regression would hide in. */
function hexPaint(value: string, where: string): Paint {
  const hex = /^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$/.exec(value.trim())
  if (hex === null) {
    throw new Error(
      `${where}: ${JSON.stringify(value)} is not an opaque hex colour, so the pairs below cannot ` +
        `be computed from it`,
    )
  }
  const digits = hex[1]?.length === 3 ? [...(hex[1] ?? '')].map((one) => one + one).join('') : (hex[1] ?? '')
  return [0, 2, 4].map((i) => Number.parseInt(digits.slice(i, i + 2), 16)) as unknown as Paint
}

/** liveTokens is every colour name a Live rule resolves against: the set
 * `palette.css` declares and the Flight Deck block `flight-deck.css` declares on
 * `:root`, which is the layer the redesigned screens are painted from. The later
 * sheet wins a name both declare, which is the order the bundle loads them in. */
function liveTokens(): ReadonlyMap<string, string> {
  const found = new Map<string, string>()
  for (const sheet of ['palette.css', 'flight-deck.css']) {
    for (const rule of plainRules(themeSheet(sheet))) {
      if (!rule.selector.split(',').map((one) => one.trim()).includes(':root')) continue
      for (const [name, value] of declarationsOf(rule.body)) found.set(name, value)
    }
  }
  return found
}

/** colourOf resolves one declaration's colour to an opaque triple, over the
 * `ground` a tint lands on. A `var()` is followed by name; a tint is composited
 * over the ground, because a chip's background is one of the theme's own with
 * alpha and what a person reads the text against is the panel underneath. */
function colourOf(
  value: string,
  tokens: ReadonlyMap<string, string>,
  ground: Paint,
  where: string,
): Paint {
  const named = /^var\((--[a-z0-9-]+)\)$/.exec(value.trim())
  if (named !== null) {
    const declared = tokens.get(named[1] ?? '')
    if (declared === undefined) throw new Error(`${where}: ${value} names a token the theme does not declare`)
    return colourOf(declared, tokens, ground, where)
  }
  const tint = /^rgba?\(([^)]+)\)$/.exec(value.trim())
  if (tint !== null) {
    const parts = (tint[1] ?? '').split(',').map((one) => Number.parseFloat(one))
    const [r, g, b] = parts
    const alpha = parts[3] ?? 1
    if (r === undefined || g === undefined || b === undefined || Number.isNaN(alpha)) {
      throw new Error(`${where}: ${value} is not a colour this test can composite`)
    }
    return [r, g, b].map((channel, i) => Math.round(channel * alpha + (ground[i] ?? 0) * (1 - alpha))) as unknown as Paint
  }
  return hexPaint(value, where)
}

/** channelLuminance and relativeLuminance are WCAG 2.1's relative luminance, and
 * contrastRatio is `(L1+0.05)/(L2+0.05)` with the lighter one first. The
 * arithmetic is the same four lines `theme-tokens.test.ts` and
 * `website/test/a11y.mjs` carry -- written out here rather than imported, since
 * both are module-local and the a11y module runs its own cases on import. */
function channelLuminance(channel: number): number {
  const c = channel / 255
  return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
}

function contrastRatio(fg: Paint, bg: Paint): number {
  const [a, b] = [fg, bg].map((rgb) => {
    const [r, g, bl] = rgb.map(channelLuminance) as [number, number, number]
    return 0.2126 * r + 0.7152 * g + 0.0722 * bl
  }) as [number, number]
  const [hi, lo] = a >= b ? [a, b] : [b, a]
  return (hi + 0.05) / (lo + 0.05)
}

/** isLiveSelector says whether a selector is this screen's: a rule is Live's when
 * a class it names is `live` or begins `live-`, which is how §2 of the styling
 * design marks a hook. `button.run-crumb-live` is Run's and is not one of them. */
function isLiveSelector(selector: string): boolean {
  for (const [, name] of selector.matchAll(/\.([\w-]+)/g)) {
    if (name === 'live' || (name ?? '').startsWith('live-')) return true
  }
  return false
}

/** GROUNDS are the panels a Live rule's background can land on: the page, the
 * panel the redesigned screen paints on, and the sunken one its cells use. A
 * tint's ratio depends on what is under it and a rule does not say which panel
 * that is, so every pair below is measured against all three -- the answer must
 * not depend on the choice. */
const GROUNDS: readonly string[] = Object.freeze(['--bg', '--surface-2', '--surface-sunken'])

describe('the Live stylesheets', () => {
  it('paints every text colour above 4.5:1 on the background its rule paints with it', () => {
    // The owner's report of 2026-11-18 named "grey text on light blue, looks like
    // a percentage": the sparkline's burn bar is `--text-muted` on `--accent`,
    // about 1.24:1. The repair is the text going away rather than the pair being
    // disguised (the bar is geometry, `spark()`), and this case is what keeps a
    // pair from arriving again: a rule of either stylesheet that paints a text
    // colour and a background together is measured, whatever it selects.
    const tokens = liveTokens()
    const failures: string[] = []
    let pairs = 0
    for (const sheet of ['screens.css', 'flight-deck.css']) {
      for (const rule of plainRules(themeSheet(sheet))) {
        if (!isLiveSelector(rule.selector)) continue
        const declarations = declarationsOf(rule.body)
        const fg = declarations.get('color')
        const bg = declarations.get('background') ?? declarations.get('background-color')
        if (fg === undefined || bg === undefined) continue
        // A rule that paints no background of its own paints no pair: what its
        // text is read against is an ancestor's, and that is the palette table's
        // question (`theme-tokens.test.ts`), not a rule's.
        if (bg === 'none' || bg === 'transparent') continue
        pairs++
        for (const ground of GROUNDS) {
          const under = hexPaint(tokens.get(ground) ?? '', ground)
          const where = `${sheet}: ${rule.selector}`
          const ratio = contrastRatio(
            colourOf(fg, tokens, under, `${where} color`),
            colourOf(bg, tokens, under, `${where} background`),
          )
          if (ratio < 4.5) {
            failures.push(`${where} -- ${fg} on ${bg} is ${ratio.toFixed(1)}:1 over ${ground}, needs 4.5:1`)
          }
        }
      }
    }
    // A scan that found nothing would pass this case without checking anything,
    // so the floor is asserted: the two tinted chips, the two disabled writes and
    // the fleet's full warning are Live rules that paint their own pair.
    expect(pairs, 'no Live rule paints a text colour and a background together').toBeGreaterThanOrEqual(3)
    expect(failures, 'text below 4.5:1 in the Live stylesheets').toEqual([])
  })

  it('is one fixed-size box per sparkline, so a window of samples cannot widen the pane', () => {
    // The line is a single element with a fixed size that clips and packs to
    // its end, and its ticks are capped: the newest samples are the ones kept, and
    // a window of a thousand is the same box as a window of three. Every
    // declaration is asserted by name, because each is one of the ways the row
    // grew or vanished before -- an untamed `height`, an `overflow` that spilled
    // into the panel, a tick that followed its text's width, a box that packed
    // from the start and so hid the newest sample when it clipped, and, once the
    // text was gone, a box with **no width of its own**: a flex item sized to its
    // content, whose content was empty ticks of zero width, resolved to its gaps
    // alone -- every tick 0 px and the box still 1 px wider per sample. The width
    // is a constant and the tick has a floor, which is what those two are.
    const burn = liveRule('screens.css', 'ol.live-burn')
    expect(burn.get('display')).toBe('flex')
    expect(burn.get('width')).toBe('calc(8 * var(--row-height))')
    expect(burn.get('height')).toBe('var(--row-height)')
    expect(burn.get('overflow')).toBe('hidden')
    expect(burn.get('justify-content')).toBe('flex-end')
    const burnBar = liveRule('screens.css', 'li.live-burn-bar')
    expect(burnBar.get('height')).toBe('100%')
    expect(burnBar.get('min-width')).toBe('1px')
    expect(burnBar.get('max-width')).toBe('12px')
    expect(burnBar.get('overflow')).toBe('hidden')
    const hour = liveRule('flight-deck.css', 'ol.live-hour')
    expect(hour.get('width')).toBe('calc(8 * var(--row-height))')
    expect(hour.get('height')).toBe('var(--row-height)')
    expect(hour.get('overflow')).toBe('hidden')
    expect(hour.get('justify-content')).toBe('flex-end')
    const hourBar = liveRule('flight-deck.css', 'li.live-hour-bar')
    expect(hourBar.get('height')).toBe('100%')
    expect(hourBar.get('min-width')).toBe('1px')
    expect(hourBar.get('max-width')).toBe('12px')
  })

  it('draws no sample as a text cell', () => {
    // The percent inside every tick was the whole of the growth: an `li` with
    // text has a minimum width of its own, so a window of samples pushed the
    // panel wider whatever the stylesheet said. A bar is geometry -- the share in
    // `data-height`, the state in `data-state`, the rate in `title` -- and the
    // text is gone, at every sample count.
    const history = new BurnHistory()
    const base = Date.parse('2026-10-15T12:00:00Z')
    for (let second = 0; second < 240; second++) {
      history.observe(theLastFrame().repos[0]?.slug ?? '', {
        at: new Date(base + second * 1000).toISOString().replace(/\.\d{3}Z$/, 'Z'),
        reading: measured(0.02),
      })
    }
    const model = buildLiveModel(frameAt(AT_CAP), history)
    const tree = renderLive(factory, model, 'light')
    // One element per sparkline: the two windows are one `ol` each, and the word
    // beside each is the console's own name for the window rather than a line.
    expect(all(tree, 'live-burn')).toHaveLength(model.repos.length)
    expect(all(tree, 'live-hour')).toHaveLength(model.repos.length)
    const bars = [...all(tree, 'live-burn-bar'), ...all(tree, 'live-hour-bar')]
    expect(bars.length).toBeGreaterThanOrEqual(240)
    for (const bar of bars) {
      expect(bar.textContent, `${bar.classes.join(' ')} carries text`).toBeNull()
    }
    // And the bar keeps what a person needs from it: the share it is drawn at,
    // whether it is a sample at all, and the rate the sample measured.
    const sample = all(tree, 'live-burn-bar').find((bar) => bar.getAttribute('data-height') !== null)
    expect(sample?.getAttribute('data-state')).toBe('measured')
    expect(sample?.getAttribute('title')).toContain('$')
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

  it('bounds the rows so a long list scrolls inside the box instead of lengthening the page', () => {
    // A repository's runs are its longest list and nothing capped them: the rows
    // ran down the page and pushed everything under them away. The box is bounded
    // and scrolls its own rows, which is the same answer the width already had --
    // and it is the box's declaration that does it, not a limit on the rows the
    // model builds, so a taller window shows more rows rather than the console
    // deciding which of them a person may see.
    const box = liveRule('flight-deck.css', 'div.live-runs')
    expect(box.get('max-height')).toBe('320px')
    expect(box.get('overflow-y')).toBe('auto')
    expect(box.get('overflow-x')).toBe('auto')
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
  it('draws one bar, in the gap\'s state and with its reason as the title', () => {
    // An unparseable `now` gives the window no stamp to end at, so the history
    // has nothing to draw: the console has not seen a sample, which
    // availability.ts calls a gap, not an unavailable value (n/a). The bar is the
    // empty line's one mark and carries **no text** like every other bar -- the
    // mark a reading would be drawn as is what made a bar a text cell, and the
    // reason it is empty is the `title` either way. It carries no `data-height`
    // at all, which is the one thing that tells it from a sample drawn at the
    // em dash: there is no share to draw.
    const frame = { ...theLastFrame(), now: 'not a time' }
    const tree = renderLive(factory, buildLiveModel(frame, new BurnHistory()), 'light')
    const bars = all(tree, 'live-burn-bar')
    expect(bars).toHaveLength(1)
    expect(bars[0]?.getAttribute('data-state')).toBe('gap')
    expect(bars[0]?.getAttribute('data-height')).toBeNull()
    expect(bars[0]?.textContent).toBeNull()
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
    expect(texts(tree, 'live-run-id')).toContain(QUEUED_ENDING)
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
// Older history on demand (the console-fixes plan's IR7, finding F4).
// ---------------------------------------------------------------------------

/** page is one `runs ls --json` answer, read through the real decoder: the rows
 * are `runview.Row`s as the CLI writes them, so what the section folds in is
 * what the wire carries and not an object invented beside it. */
function page(...ids: readonly string[]): readonly RunsListRow[] {
  const raw = {
    runs: ids.map((run, at) => ({
      run,
      repo: 'r-4c25cf20/r-f1aa9214',
      status: at % 2 === 0 ? 'ready' : 'halted',
      terminal: true,
      title: `older run ${String(at)}`,
      ...(at % 2 === 0 ? {} : { halt: { by: 'someone', at: '2026-10-14T09:00:00Z', reason: 'spent' } }),
    })),
  }
  const decoded = decodeRunsList(raw)
  if (!decoded.ok) throw new Error(decoded.skew.because)
  return decoded.value.runs
}

/** runIds is `n` run ids in the CLI's own order, newest first. */
function runIds(n: number): readonly string[] {
  return Array.from({ length: n }, (_, at) => `20261014-1200${String(at).padStart(2, '0')}-bb01`)
}

const OLDEST_OF_THE_WINDOW = '20261015-120000-aa01'

/** withOlder is the screen drawn for one older section, over the fixture's last
 * frame. */
function withOlder(older: LiveOlder): FakeScreen {
  return renderLive(factory, buildLiveModel(theLastFrame(), new BurnHistory(), null, undefined, undefined, older), 'light')
}

describe('older history on demand (IR7)', () => {
  it('asks for the page after the last run drawn, at a page size the console admits', () => {
    // The acceptance's own argv: `runs ls --json --limit=50 --after=<last run
    // id>`. The cursor is a **run id** and not an instant, which is what the
    // wire keeps (`contract-index.md`), and the page size is this file's own
    // constant -- held under the console's ceiling here rather than by an import
    // in the screen, because the renderer may not import `src/main`.
    const request = olderRequest(liveOlderOf(true, OLDEST_OF_THE_WINDOW))
    expect(request).toEqual({ limit: LIVE_OLDER_PAGE, after: OLDEST_OF_THE_WINDOW })
    expect(LIVE_OLDER_PAGE).toBeLessThanOrEqual(RUNS_LIST_MAX_LIMIT)
    expect(LIVE_OLDER_PAGE).toBeGreaterThan(0)
  })

  it('has nothing to ask for before the floor, with no cursor, or with a read already out', () => {
    // Four refusals, and each is a different reason to spawn nothing: the CLI is
    // below IR1's release, the window drew no run to page after, the last page
    // was short, and a page is already being read.
    expect(olderRequest(liveOlderOf(false, OLDEST_OF_THE_WINDOW))).toBeNull()
    expect(olderRequest(liveOlderOf(true, null))).toBeNull()
    const short = olderPageOf(page(...runIds(3)), liveOlderOf(true, OLDEST_OF_THE_WINDOW))
    expect(short.more).toBe(false)
    expect(olderRequest(short)).toBeNull()
    const reading = liveOlderReading(liveOlderOf(true, OLDEST_OF_THE_WINDOW))
    expect(olderRequest(reading)).toBeNull()
    expect(reading.reading).toBe(true)
  })

  it('appends the second page after the first, with the cursor on the last run id of the page just read', () => {
    // **The cursor is the last run id of the page just read**, not of the whole
    // list: `--after` starts after that run, so the next page begins with the
    // run that follows it. The first page is a full page (`LIVE_OLDER_PAGE`
    // rows), so another may follow; the rows of the two pages are appended in
    // the CLI's order and never sorted or de-duplicated here.
    const first = page(...runIds(LIVE_OLDER_PAGE))
    const lastOfFirst = runIds(LIVE_OLDER_PAGE)[LIVE_OLDER_PAGE - 1] ?? ''
    const afterFirst = olderPageOf(first, liveOlderOf(true, OLDEST_OF_THE_WINDOW))
    expect(afterFirst.rows).toHaveLength(LIVE_OLDER_PAGE)
    expect(afterFirst.cursor).toBe(lastOfFirst)
    expect(afterFirst.more).toBe(true)

    const second = page(...runIds(4))
    const afterSecond = olderPageOf(second, afterFirst)
    expect(afterSecond.rows.map((row) => row.run)).toEqual([
      ...first.map((row) => row.run),
      ...second.map((row) => row.run),
    ])
    expect(afterSecond.cursor).toBe(runIds(4)[3])
    // A page shorter than the page size is the end of the list: this console
    // cannot count a history it has not read, and the alternative -- an empty
    // page -- is a round trip whose answer is nothing.
    expect(afterSecond.more).toBe(false)
  })

  it('draws nothing at all below the floor, and nothing for a window with no run', () => {
    // A control whose press is a refusal is worse than no control: below the
    // floor the section is not drawn -- not empty, and not disabled -- and the
    // Live list is the day's runs, which is what it always was.
    expect(all(withOlder(liveOlderOf(false, OLDEST_OF_THE_WINDOW)), 'live-older')).toEqual([])
    expect(all(withOlder(liveOlderOf(true, null)), 'live-older')).toEqual([])
    // The default model -- every drawing the earlier tests and the goldens were
    // taken from -- is the same nothing.
    expect(all(renderLive(factory, modelOf(theLastFrame()), 'light'), 'live-older')).toEqual([])
  })

  it('draws the rows, each a door to its run, and the control that asks for the next page', () => {
    // A **full** page, which is the case a control is drawn in: the page came
    // back at the page size, so another may follow (`more`).
    const older = olderPageOf(page(...runIds(LIVE_OLDER_PAGE)), liveOlderOf(true, OLDEST_OF_THE_WINDOW))
    const tree = withOlder(older)
    const section = one(tree, 'live-older')
    expect(section.tag).toBe('section')
    expect(section.getAttribute('aria-label')).toBe(LIVE_OLDER_TITLE)
    expect(texts(section, 'live-older-title')).toEqual([LIVE_OLDER_TITLE])
    expect(all(section, 'live-older-row')).toHaveLength(LIVE_OLDER_PAGE)
    expect(all(section, 'live-older-run-id').map((cell) => cell.getAttribute('data-run-id'))).toEqual([
      ...runIds(LIVE_OLDER_PAGE),
    ])
    expect(all(section, 'live-older-repo').map((cell) => cell.textContent)).toEqual(
      Array.from({ length: LIVE_OLDER_PAGE }, () => 'r-4c25cf20/r-f1aa9214'),
    )
    // A halted run's status word **is** the halt (`runstore.StatusHalted`), so
    // the cell says it once and the section draws no second mark for it.
    expect(texts(section, 'live-older-status').slice(0, 3)).toEqual(['ready', 'halted', 'ready'])
    expect(texts(section, 'live-older-load')).toEqual([LIVE_OLDER_WORD])
    expect(one(section, 'live-older-load').getAttribute('type')).toBe('button')
  })

  it('draws its own busy state while a page is out, and keeps the rows under it', () => {
    // **The section's busy state is this read's and not the feed's** (F4): the
    // control says what is being read in the frame the press happened in, the
    // section announces itself busy, and the rows already drawn stay -- a page
    // being read is not a page that said the older runs are gone.
    const drawn = olderPageOf(page(...runIds(LIVE_OLDER_PAGE)), liveOlderOf(true, OLDEST_OF_THE_WINDOW))
    const tree = withOlder(liveOlderReading(drawn))
    const section = one(tree, 'live-older')
    expect(section.getAttribute('aria-busy')).toBe('true')
    expect(texts(section, 'live-older-load')).toEqual([LIVE_OLDER_BUSY])
    expect(one(section, 'live-older-load').getAttribute('aria-disabled')).toBe('true')
    expect(all(section, 'live-older-row')).toHaveLength(LIVE_OLDER_PAGE)
    // The busy slot is the section's first child, so the entry's mark lands at
    // the top of this section rather than under the rows.
    expect((section.childNodes()[0] as FakeScreen).classes).toContain('console-busy-slot')
  })

  it('keeps the rows and the control under a refusal, and draws the CLI’s own sentence', () => {
    // A refused page is a read that did not answer, not a list that ended: the
    // rows stay, the control stays (the operator's press is the remedy), and the
    // sentence is drawn in its own place with the section's own field.
    const refused = liveOlderRefused(
      olderPageOf(page(...runIds(LIVE_OLDER_PAGE)), liveOlderOf(true, OLDEST_OF_THE_WINDOW)),
      'runs ls: exit status 1',
    )
    const section = one(withOlder(refused), 'live-older')
    expect(texts(section, 'live-older-refusal')).toEqual(['runs ls: exit status 1'])
    expect(all(section, 'live-older-row')).toHaveLength(LIVE_OLDER_PAGE)
    expect(texts(section, 'live-older-load')).toEqual([LIVE_OLDER_WORD])
    expect(one(section, 'live-older-load').getAttribute('aria-disabled')).toBeNull()
    expect(section.getAttribute('aria-busy')).toBeNull()
  })

  it('draws no control at the end of the list, and the rows it did read', () => {
    const end = olderPageOf(page(...runIds(2)), liveOlderOf(true, OLDEST_OF_THE_WINDOW))
    const section = one(withOlder(end), 'live-older')
    expect(all(section, 'live-older-load')).toEqual([])
    expect(all(section, 'live-older-row')).toHaveLength(2)
  })

  it('holds the section’s words in one place', () => {
    // The control's label and the busy label are this file's vocabulary and are
    // drawn through `literal`: nothing a job wrote reaches them.
    expect(LIVE_OLDER_WORD).toBe('Load older')
    expect(LIVE_OLDER_BUSY).toContain('older')
    expect(LIVE_OLDER_TITLE).toBe('Older runs')
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

// ---------------------------------------------------------------------------
// The gauge, the hour line and the two cap bars (slice V, D736/D737)
// ---------------------------------------------------------------------------

describe('the spend-rate gauge and the cap bars', () => {
  /** gaugeOf is the drawn gauge of a model: the panel's one `div.live-gauge`. */
  function gaugeOf(model: LiveModel): FakeScreen {
    return one(panel(model), 'live-gauge')
  }

  it('live: red zone from headroom and hours left', () => {
    // **The red zone is a rate and not a threshold anyone chose** (D737): what
    // is left of today's cap, divided by what is left of the UTC day. At 19:00Z
    // five hours remain, so $10 of headroom gives $2.00/h -- and the same $10
    // an hour before midnight gives $10.00/h, which is why a fixed line would be
    // a number about nobody's day.
    expect(hoursLeftOf('2026-10-15T19:00:00Z')).toBe(5)
    expect(hoursLeftOf('2026-10-15T23:00:00Z')).toBe(1)
    expect(formatReading(redZoneOf(measured(10), '2026-10-15T19:00:00Z'), 'usd')).toBe('$2.00')
    expect(formatReading(redZoneOf(measured(10), '2026-10-15T23:00:00Z'), 'usd')).toBe('$10.00')
    // A stamp exactly at midnight is a whole day and never zero hours: dividing
    // by none is the one answer a red zone may not be.
    expect(hoursLeftOf('2026-10-15T00:00:00Z')).toBe(24)
    // A stamp the console cannot read is a sentence rather than a rate, and so
    // is a headroom nobody measured: a cap that was never read has no zone.
    expect(redZoneOf(measured(10), 'not a stamp').state).toBe('unavailable')
    expect(redZoneOf(unavailable('no cap'), '2026-10-15T19:00:00Z').because).toContain('what is left of the day’s cap')

    // The gauge's other figure is the same burn in $/h (`perHourOf`), with the
    // state and the lower-bound flag carried across unchanged: a notional burn
    // must not become money by being multiplied.
    expect(formatReading(perHourOf(measured(0.1)), 'usd')).toBe('$6.00')
    expect(perHourOf(measured(0.1, { lowerBound: true })).lowerBound).toBe(true)
    expect(perHourOf(notional(1)).state).toBe('notional')
    expect(perHourOf(unavailable('no burn')).state).toBe('unavailable')

    // End to end, on figures chosen so the zone lands on a whole rate: the
    // frame's stamp is 12:04:21Z, so 11.9275 hours are left, and $71.565 of
    // headroom is $6.00/h. A burn of $0.10/min is the same $6.00/h, which puts
    // the needle exactly on the red zone's own edge.
    const model = fleetModel({
      daily_cap_usd: 100,
      counted_usd: 28.435,
      leased_usd: 28.435,
      percent: 28.435,
      burn: { known: true, usd_per_min: 0.1, fast: false },
    })
    expect(formatReading(model.fleet.redZone, 'usd')).toBe('$6.00')
    expect(formatReading(model.fleet.rate, 'usd')).toBe('$6.00')
    const gauge = gaugeOf(model)
    // The axis is twice the red-zone rate, so the zone is the right half of the
    // track and the needle's position is comparable with it at one scale.
    expect(one(gauge, 'live-gauge-zone').getAttribute('x')).toBe('50')
    expect(one(gauge, 'live-gauge-zone').getAttribute('width')).toBe('50')
    expect(one(gauge, 'live-gauge-needle').getAttribute('x')).toBe('50')
    expect(texts(gauge, 'live-gauge-rate')).toEqual(['$6.00'])

    // **A red zone of zero is a red zone and not a missing one.** A spent-out
    // cap has no headroom, so no rate outlasts the day: the whole track is red
    // and the needle stands one unit inside its right-hand end, where the
    // `viewBox` still draws it.
    const atCap = fleetModel({ daily_cap_usd: 50, counted_usd: 50, leased_usd: 50, percent: 100 })
    expect(atCap.fleet.redZone.value).toBe(0)
    expect(one(gaugeOf(atCap), 'live-gauge-zone').getAttribute('width')).toBe('100')
    expect(one(gaugeOf(atCap), 'live-gauge-needle').getAttribute('x')).toBe('99')

    // A zone nobody can read draws no zone and no needle -- an unreadable
    // threshold is not a threshold of none -- and a rate nobody can read leaves
    // the zone where it is, because the zone is still a fact about the day.
    const noHeadroom = fleetModel({ daily_cap_usd: null, percent: null })
    expect(noHeadroom.fleet.redZone.state).toBe('unavailable')
    const blind = gaugeOf(noHeadroom)
    expect(all(blind, 'live-gauge-zone')).toEqual([])
    expect(all(blind, 'live-gauge-needle')).toEqual([])
    expect(texts(blind, 'live-gauge-rate')).toEqual(['$0.00'])
    const noRate: LiveModel = {
      ...model,
      fleet: { ...model.fleet, rate: unavailable('the document carries no burn rate') },
    }
    expect(all(gaugeOf(noRate), 'live-gauge-zone')).toHaveLength(1)
    expect(all(gaugeOf(noRate), 'live-gauge-needle')).toEqual([])
  })

  it('live: hour history bounded', () => {
    // The hour line is the console's own second history (D737): an hour of
    // arrivals, held only while the console runs, and nothing in `internal/watch`
    // is asked to agree with it. What is held here is that it is an hour, that
    // its boundary is the same half-open one the burn window uses, and that it
    // is bounded twice over.
    expect(LIVE_HOUR_WINDOW_SECONDS).toBe(60 * 60)
    const hours = new HourHistory()
    hours.observe('r', { at: '2026-10-15T12:00:00Z', reading: measured(0.01) })
    hours.observe('r', { at: '2026-10-15T13:00:00Z', reading: measured(0.02) })
    expect(hours.window('r', '2026-10-15T13:00:00Z')).toHaveLength(2)
    expect(hours.window('r', '2026-10-15T13:00:01Z').map((one) => one.at)).toEqual(['2026-10-15T13:00:00Z'])

    // The stamp keeps its milliseconds: at half-second spacing two arrivals
    // would otherwise collapse into one, and the history holds one sample per
    // stamp on purpose.
    const stamp = (ms: number): string => new Date(ms).toISOString()
    const base = Date.parse('2026-10-15T00:00:00Z')
    // 5,000 arrivals half a second apart are 41 minutes, so the window still
    // holds every one of them and the sample cap is what binds.
    const fast = new HourHistory()
    for (let n = 0; n < 5000; n++) {
      fast.observe('r', { at: stamp(base + n * 500), reading: measured(0.01) })
    }
    expect(fast.size('r')).toBe(LIVE_HOUR_MAX_SAMPLES)
    // And an hour-long feed keeps an hour rather than the session: the oldest
    // sample left is the one at the cut, which is the shape `window` returns.
    const long = new HourHistory()
    for (let second = 0; second < 5000; second++) {
      long.observe('r', { at: stamp(base + second * 1000), reading: measured(0.01) })
    }
    expect(long.window('r', stamp(base + 4999 * 1000))).toHaveLength(LIVE_HOUR_WINDOW_SECONDS + 1)
    expect(long.window('r', stamp(base + 4999 * 1000))[0]?.at).toBe(
      stamp(base + (4999 - LIVE_HOUR_WINDOW_SECONDS) * 1000),
    )

    // The two lines are two instruments over the same repository: a sample
    // fourteen minutes old is off the five-minute line and still on the hour's,
    // which is the whole reason the second history exists.
    const slug = theLastFrame().repos[0]?.slug ?? ''
    const burn = new BurnHistory()
    const hour = new HourHistory()
    for (const history of [burn, hour]) {
      history.observe(slug, { at: '2026-10-15T11:50:00Z', reading: measured(0.05) })
    }
    const model = buildLiveModel(frameAt('2026-10-15T12:04:21Z'), burn, null, new Map(), hour)
    const row = model.repos[0]
    if (row === undefined) throw new Error('no repository row')
    expect(row.spark.bars).toHaveLength(1)
    expect(row.hour.bars).toHaveLength(2)
    // Both are drawn, and each under its own window's word: two shapes side by
    // side that only their colour tells apart is the one thing the design
    // forbids (`spark`).
    const meters = one(renderLive(factory, model, 'light'), 'live-repo')
    expect(texts(meters, 'live-meter-word')).toEqual([LIVE_BURN_WINDOW_WORD, LIVE_HOUR_WINDOW_WORD])
    expect(all(meters, 'live-hour-bar')).toHaveLength(2)
  })

  it('live: burn window still equals BurnWindow', () => {
    // D737 added a second history and left the first one alone: one hour is an
    // *addition* to the five-minute window, never a widening of it. The window
    // the burn line is drawn over is still `internal/watch/state.go`'s, so the
    // drawn instrument still measures the thing the CLI's own rate measures.
    expect(goConstant('state.go', 'BurnWindow')).toBe('5 * time.Minute')
    expect(LIVE_BURN_WINDOW_SECONDS).toBe(5 * 60)
    const held = new BurnHistory()
    held.observe('r', { at: '2026-10-15T12:00:00Z', reading: measured(0.01) })
    // A sample *at* the cut stays and one second further takes it out, which is
    // what `State.Burn` does with the same two stamps.
    expect(held.window('r', '2026-10-15T12:05:00Z')).toHaveLength(1)
    expect(held.window('r', '2026-10-15T12:05:01Z')).toHaveLength(0)
    // The hour history does not reach the five-minute line: a sample nineteen
    // minutes old is inside the hour the second history keeps and outside the
    // five minutes this one does, and the burn line is drawn from the burn
    // history alone.
    const slug = theLastFrame().repos[0]?.slug ?? ''
    const burn = new BurnHistory()
    const hour = new HourHistory()
    for (const history of [burn, hour]) {
      history.observe(slug, { at: '2026-10-15T11:45:00Z', reading: measured(0.09) })
    }
    const model = buildLiveModel(frameAt(AT_CAP), burn, null, new Map(), hour)
    const row = model.repos[0]
    if (row === undefined) throw new Error('no repository row')
    // One bar on the five-minute line -- the frame's own sample, which the model
    // observes into both histories on every build -- against two on the hour's:
    // the sample nineteen minutes back and the frame's. That difference is the
    // whole point of the second history, and it is the burn window that draws
    // the first line, not the wider one.
    expect(row.spark.bars).toHaveLength(1)
    expect(row.hour.bars).toHaveLength(2)
    expect(row.spark.bars[0]?.at).toBe(AT_CAP)
  })

  it('live: concurrent bar against max_parallel', () => {
    // The second cap bar is the concurrent-run cap: the runs that are *not*
    // queued, as a share of `max_parallel` (V1.1's field). The fixture's last
    // frame has one live run and one queued one, so a cap of 8 is a quarter
    // taken.
    const model = fleetModel({ max_parallel: 8 })
    expect(model.fleet.active).toBe(1)
    expect(model.fleet.queued).toBe(1)
    expect(formatReading(model.fleet.concurrent, 'percent')).toBe('12.5%')
    const panelModel = panel(model)
    const runs = one(panelModel, 'live-fleet-runs-cap')
    expect(runs.getAttribute('data-fill')).toBe('12.5%')
    expect(runs.getAttribute('data-state')).toBe('measured')
    // The cap itself is drawn beside the caption: a share with no "of what" is
    // not a figure anyone can act on, and the label is what says which cap this
    // bar is a share of.
    expect(one(panelModel, 'live-fleet-cap-limit').textContent).toBe('8')
    expect(texts(panelModel, 'live-fleet-cap-word')).toEqual(['SPEND OF CAP', 'RUNS OF CAP'])
    // The spend bar beside it is unchanged: the day's money as a share of the
    // day's cap, from the document's own percent.
    expect(one(panelModel, 'live-fleet-spend-cap').getAttribute('data-fill')).toBe('100.0%')

    // A cap of zero is a real cap and not a missing one: nothing running is
    // honestly 0% of it, and a run running against it is a document
    // contradicting itself -- there is no finite share of none, and `Infinity`
    // drawn as a full bar would be the misreport this screen exists not to make.
    expect(formatReading(concurrentOf(0, measured(0)), 'percent')).toBe('0.0%')
    expect(concurrentOf(1, measured(0)).state).toBe('unavailable')
    expect(concurrentOf(1, measured(0)).because).toContain('is 0')
  })

  it('live: a null max_parallel draws an unavailable bar', () => {
    // `max_parallel` is null for a writer with no config to read (and absent
    // altogether from a CLI older than the field), and null is **unknown** and
    // never a cap of none: a bar drawn full over an installation nobody capped
    // says "the fleet is at its limit" about a limit that was never set.
    const model = fleetModel({ max_parallel: null })
    expect(model.total.maxParallel.state).toBe('unavailable')
    expect(formatReading(model.total.maxParallel, 'count')).toBe(UNAVAILABLE_MARK)
    const panelModel = panel(model)
    const runs = one(panelModel, 'live-fleet-runs-cap')
    expect(runs.getAttribute('data-fill')).toBe(UNAVAILABLE_MARK)
    expect(runs.getAttribute('data-state')).toBe('unavailable')
    expect(runs.textContent).toBe(UNAVAILABLE_MARK)
    expect(runs.getAttribute('title')).toContain('max_parallel')
    const limit = one(panelModel, 'live-fleet-cap-limit')
    expect(limit.textContent).toBe(UNAVAILABLE_MARK)
    expect(limit.getAttribute('title')).toContain('max_parallel')
    // The spend bar is not affected: a cap nobody could read for concurrent runs
    // says nothing about the day's money.
    expect(one(panelModel, 'live-fleet-spend-cap').getAttribute('data-state')).toBe('measured')

    // `concurrentOf` says the same thing without a document: an unreadable cap
    // is unavailable with the cap's own reason, whatever the run count is.
    expect(concurrentOf(3, unavailable('no max_parallel')).state).toBe('unavailable')
  })

  it('live: gauge withheld in the reduced mode', () => {
    const model = fleetModel({ max_parallel: 8 })
    const open = renderLive(factory, model, 'light')
    expect(all(open, 'live-gauge-zone')).toHaveLength(1)
    expect(all(open, 'live-gauge-needle')).toHaveLength(1)

    // In the reduced mode the `caps` view is withheld, and the answer travels as
    // a model the screen is handed (`capsWithheld`) rather than as a flag the
    // drawing reads: a figure the gate withheld is one the gauge cannot draw
    // back later. An unreadable rate and an unreadable zone are drawn as
    // unavailable readings, so the gauge loses its shapes and not only its
    // numbers.
    const reduced = renderLive(factory, model, 'light', true)
    expect(all(reduced, 'live-gauge-zone')).toEqual([])
    expect(all(reduced, 'live-gauge-needle')).toEqual([])
    const rate = one(reduced, 'live-gauge-rate')
    expect(rate.textContent).toBe(UNAVAILABLE_MARK)
    expect(rate.getAttribute('title')).toBe(LIVE_CAPS_HIDDEN_WORD)
    const runs = one(reduced, 'live-fleet-runs-cap')
    expect(runs.getAttribute('data-state')).toBe('unavailable')
    expect(runs.getAttribute('data-fill')).toBe(UNAVAILABLE_MARK)
    expect(one(reduced, 'live-fleet-cap-limit').textContent).toBe(UNAVAILABLE_MARK)
    // **The money of the totals block is not withheld.** `counted_usd`,
    // `daily_cap_usd` and this bar have been read since the console's first
    // release, and taking them back is the `spend` view's question rather than
    // the cap bars' -- so the spend bar beside the runs bar still draws, at the
    // document's own percent.
    expect(one(reduced, 'live-fleet-spend-cap').getAttribute('data-fill')).toBe('100.0%')
    expect(one(reduced, 'live-cap').textContent).toBe('$50.00')
  })
})
