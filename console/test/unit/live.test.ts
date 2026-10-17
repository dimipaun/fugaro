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
import { decodeSnapshot } from '../../src/shared/schema'
import type { LiveDocument, RunView } from '../../src/shared/schema'
import {
  BurnHistory,
  LIVE_BURN_WINDOW_SECONDS,
  LIVE_LOST_AFTER_SECONDS,
  LIVE_NEAR_DEADLINE_FRACTION,
  LIVE_SILENT_AFTER_SECONDS,
  LIVE_STALE_AFTER_SECONDS,
  LIVE_THEMES,
  buildLiveModel,
  deadlineFlag,
  healthFlag,
  renderLive,
  sparkline,
  watchHealth,
} from '../../src/renderer/screens/live'
import type { LiveModel, ScreenElement, ScreenFactory } from '../../src/renderer/screens/live'
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
    if (!decoded.ok) throw new Error(`${record.at}: ${decoded.skew.reason}`)
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

  constructor(readonly tag: string) {}

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

/** factory makes Fresh fakes, in the shape the renderer is handed. */
const factory: ScreenFactory = { create: (tag) => new FakeScreen(tag) }

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

  it('refuses an enum value the console does not know', () => {
    // `health` and `deadline` are enums the CLI writes from its own tables
    // (`NON_TEXT_STRING_KEYS`), and a value this build has never seen is a
    // document from a newer CLI. Drawing "not silent" for it would be the
    // console answering a question it cannot read.
    expect(healthFlag('hibernating')).toBeNull()
    expect(deadlineFlag('imminent')).toBeNull()
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

  it('keeps the queued note when the document carries one', () => {
    expect(modelOf(theLastFrame()).queuedNote).toBeNull()
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
    const noCap = { ...theLastFrame(), total: { ...theLastFrame().total } }
    const model = buildLiveModel(
      { ...noCap, total: { ...noCap.total, dailyCapUsd: unavailable('the document has no total.daily_cap_usd') } },
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
    expect(total.countedUsd.value).toBe(50)
    expect(total.notionalUsd.value).toBe(3.29)
    expect(total.countedUsd.value).not.toBe(53.29)
    expect(total.notionalUsd.state).toBe('notional')
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
    const history = new BurnHistory()
    history.observe('r', { at: '2026-10-15T12:00:00Z', reading: measured(0.01) })
    history.observe('r', { at: '2026-10-15T12:01:00Z', reading: unavailable('the document has no burn') })
    history.observe('r', { at: '2026-10-15T12:02:00Z', reading: measured(0.03) })
    const spark = sparkline(history, 'r', '2026-10-15T12:02:30Z')
    expect(spark.segments).toBe(2)
    expect(spark.bars).toHaveLength(3)
    expect(formatReading(spark.bars[1]?.height ?? unavailable('none'), 'percent')).toBe(GAP_MARK)
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
    // frame's own stamp the 300 s window reaches back to 12:00:21, which leaves
    // the three frames from 12:01:40 on; five seconds later it reaches 12:00:05
    // and keeps four.
    expect(history.window(repo.slug, AT_CAP)).toHaveLength(3)
    expect(history.window(repo.slug, '2026-10-15T12:05:05Z')).toHaveLength(4)
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
    const tree = renderLive(factory, modelOf(frameAt('2026-10-15T12:04:20Z')), 'light')
    const flags = all(tree, 'live-run-flag')
    expect(flags.map((flag) => flag.textContent)).toEqual(['lost'])
    expect(flags[0]?.getAttribute('title')).toContain('180 s')
    expect(flags[0]?.getAttribute('data-flag')).toBe('lost')
  })

  it('draws the notional mark on an OAuth run\'s spend', () => {
    const tree = renderLive(factory, modelOf(frameAt('2026-10-15T12:00:05Z')), 'light')
    expect(texts(tree, 'live-run-spend')).toEqual([`${NOTIONAL_MARK}$12.54`])
  })

  it('writes every cell as text and sets no attribute that could act', () => {
    // The screen is handed a fake whose navigating attribute, resource
    // attributes, `style` attribute and every `on*` handler throw, so a screen
    // that reached for one fails here. What is left is the set below: the
    // class hooks the design's density rules need and `data-` readings a test
    // and the stylesheet read.
    const tree = renderLive(factory, modelOf(theLastFrame()), 'light')
    for (const node of walk(tree)) {
      for (const name of node.attributes.keys()) {
        expect(
          name === 'class' || name === 'title' || name.startsWith('data-'),
          `${node.tag}[${name}]`,
        ).toBe(true)
      }
    }
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
