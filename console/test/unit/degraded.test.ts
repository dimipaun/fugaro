// The console's degraded, stale and absent states (desktop-console plan Task
// 3.1; design section 6.3).
//
// The plan's own test for this task is `test/e2e/degraded.spec.ts`, which draws
// every state in the console's real window under its real CSP. This file is
// here because the e2e suite needs a display: it holds the same four conditions
// at the level that can run anywhere -- the model, and what the screen draws
// into a fake element -- so a screen whose arithmetic or whose vocabulary
// drifted fails on the machine that has no window as well as on the one that
// has. Both files assert the *same* facts where they overlap; the boundary is
// that the spec is the only one that can say "under the real policy, in a real
// document".
//
// What is held here, in the plan's own words:
//
//   - **the caps are `n/a` and never `0`** -- the task's red step. A project
//     with no budget backend has no cap field at all (contract section 10.8),
//     and the failure this guards against is a screen that draws a missing
//     number as a zero: `$0.00` of a $50 cap is a project that has spent
//     nothing, which is a different fact from one whose cap cannot be read.
//   - **a permission refusal shows the CLI's text unchanged** -- the sentence
//     goes in and the same characters come out, remedy and roles included.
//   - **the absent-binary screen** -- what is needed, the install command, the
//     picker, and no dashboard around it.
//   - **a stale feed breaks the graphs** rather than interpolating across the
//     silence, and keeps the last-known values with the timestamp they came
//     from.
//   - **the console's stale threshold is the CLI's** (`watch.StaleAfter`, 45 s),
//     asserted as two constants that cannot drift.

import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import {
  FEED_STALE_AFTER_SECONDS,
  FUGARO_INSTALL_COMMAND,
  feedSilence,
  noInstallationState,
} from '../../src/source/cli'
import { loadFixture } from '../../src/source/fixture'
import { refusalFrom } from '../../src/source/source'
import { githubToken } from '../support/fakeSecrets'
import { ESTIMATED_MARK, UNAVAILABLE_MARK, estimated, formatReading, measured, unavailable } from '../../src/shared/availability'
import { sanitizeRefusal, sanitizeText, TOKEN_PLACEHOLDER } from '../../src/shared/sanitize'
import { decodeDocument } from '../../src/shared/schema'
import type { DegradedDocument, LiveDocument } from '../../src/shared/schema'
import { LIVE_BURN_MAX_SAMPLES, LIVE_STALE_AFTER_SECONDS, BurnHistory, buildLiveModel, renderLive } from '../../src/renderer/screens/live'
import type { FeedHealth, LiveTheme, ScreenElement, ScreenFactory } from '../../src/renderer/screens/live'
import {
  DEGRADED_THEMES,
  NO_CAPS_BECAUSE,
  buildNoFirebaseModel,
  refusedState,
  renderNoFirebase,
  renderUnavailable,
} from '../../src/renderer/screens/degraded'
import type { NoBinaryState, RefusedState } from '../../src/renderer/screens/degraded'

const consoleDir = join(__dirname, '..', '..')
const fixturePath = join(consoleDir, 'test', 'fixtures', 'degraded.ndjson')

// ---------------------------------------------------------------------------
// The fake screen.
// ---------------------------------------------------------------------------

/** FakeScreen is the smallest thing a screen may draw into: the same fake
 * `live.test.ts` hands the Live screen, with the same refusal of every
 * attribute that could make a window *do* something. */
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
      throw new Error(`a screen may not set the ${name} attribute`)
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

const factory: ScreenFactory = { create: (tag) => new FakeScreen(tag) }

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

/** drawn is the whole tree's text, in document order. */
function drawn(node: ScreenElement): string {
  return walk(node)
    .map((leaf) => leaf.textContent ?? '')
    .filter((text) => text !== '')
    .join(' ')
}

// ---------------------------------------------------------------------------
// The fixtures and the states.
// ---------------------------------------------------------------------------

/** theDegradedDocument is the committed degraded fixture's newest document,
 * through the real decoder: the same bytes a replayed feed would carry. */
function theDegradedDocument(): DegradedDocument {
  const loaded = loadFixture(readFileSync(fixturePath, 'utf8'), 'test/fixtures/degraded.ndjson')
  let out: DegradedDocument | null = null
  for (const record of loaded.records) {
    if (!('document' in record)) continue
    const decoded = decodeDocument(record.document, 'the fixture frame')
    expect(decoded.ok).toBe(true)
    if (decoded.ok && decoded.value.shape === 'degraded') out = decoded.value
  }
  if (out === null) throw new Error('the degraded fixture has no degraded document')
  return out
}

/** theNotices are the fixture's non-JSON lines, in order: contract section 10.8
 * says a reader keeps them, and this is what the console keeps. */
function theNotices(): readonly { readonly text: string }[] {
  const loaded = loadFixture(readFileSync(fixturePath, 'utf8'), 'test/fixtures/degraded.ndjson')
  return loaded.records.flatMap((record) => ('notice' in record ? [{ text: record.notice }] : []))
}

/** aFeed is one console-side staleness reading, measured off a frame's own
 * stamp: `seconds` after the frame arrived is `seconds` of silence, and the
 * console's clock is that stamp plus the silence -- never a stamp invented
 * here, because the window is five minutes wide and a clock an hour out would
 * empty it rather than test it. */
function aFeed(frame: LiveDocument, seconds: number): FeedHealth {
  return {
    since: frame.now,
    at: new Date(Date.parse(frame.now) + seconds * 1000).toISOString().replace(/\.\d{3}Z$/, 'Z'),
    seconds,
    stale: seconds > FEED_STALE_AFTER_SECONDS,
  }
}

/** theLiveFrames are the synthetic fixture's live documents, in order: the
 * frames the stale tests draw, because they are documents the decoder has
 * already accepted. */
function theLiveFrames(): readonly LiveDocument[] {
  const live = loadFixture(
    readFileSync(join(consoleDir, 'test', 'fixtures', 'synthetic.ndjson'), 'utf8'),
    'test/fixtures/synthetic.ndjson',
  )
  const out: LiveDocument[] = []
  for (const record of live.records) {
    if (!('document' in record)) continue
    const decoded = decodeDocument(record.document, 'the synthetic frame')
    if (decoded.ok && decoded.value.shape === 'live') out.push(decoded.value)
  }
  if (out.length < 2) throw new Error('the synthetic fixture has fewer than two live documents')
  return out
}

/** theLiveFrame is the first of them: where the burn history starts. */
function theLiveFrame(): LiveDocument {
  const frames = theLiveFrames()
  const first = frames[0]
  if (first === undefined) throw new Error('the synthetic fixture has no live document')
  return first
}

/** theLaterFrame is the last of them: five minutes on, so the silence in
 * between is inside the burn window. */
function theLaterFrame(): LiveDocument {
  const frames = theLiveFrames()
  const last = frames[frames.length - 1]
  if (last === undefined) throw new Error('the synthetic fixture has no live document')
  return last
}

/** aRefusal is the CLI refusing a Firestore read, in its own words: the two
 * role names and the remedy are `internal/cli/report.go`'s `firestoreErr`, kept
 * here as the text a screen must not alter. */
const REFUSAL_SENTENCE =
  "project sandbox's spend history refused the read: you need the Cloud Datastore Viewer role (roles/datastore.viewer) on its Firebase project sandbox-db, plus the Service Usage Consumer role (roles/serviceusage.serviceUsageConsumer); GCP owners and editors have both; gcloud auth application-default login may be needed (rpc error: code = PermissionDenied)"

/** aRefusedState is that refusal as the screen's data, built the way a caller
 * builds it: from a `SourceRefusal`, whose `sentence` is what the CLI printed. */
function aRefusedState(): RefusedState {
  const refusal = refusalFrom('budget.show', REFUSAL_SENTENCE)
  return refusedState('budget.show', refusal)
}

/** anAbsentState is the state `src/source/cli.ts` builds when resolution found
 * no binary. */
function anAbsentState(): NoBinaryState {
  return noInstallationState({
    reason: 'no fugaro binary was found: looked at /usr/local/bin/fugaro, /usr/bin/fugaro',
    appEnv: {},
    home: '/nonexistent-home',
  })
}

// ---------------------------------------------------------------------------

describe('the console’s stale threshold is the CLI’s', () => {
  it('is the same 45 s on the source side and on the screen', () => {
    // `watch.StaleAfter` (`internal/watch/state.go`) is 45 s, and both of these
    // stand for it: the source measures the feed's silence with one and the
    // banner explains itself with the other. A console that bannered at a
    // different number would be telling the operator about a silence the CLI it
    // is reading does not call one.
    expect(FEED_STALE_AFTER_SECONDS).toBe(45)
    expect(LIVE_STALE_AFTER_SECONDS).toBe(FEED_STALE_AFTER_SECONDS)
  })
})

describe('the console’s own reading of a quiet feed', () => {
  it('measures the silence from arrival, not from the document’s `now`', () => {
    const quiet = feedSilence({ receivedAt: '2026-10-15T13:00:00Z' }, new Date('2026-10-15T13:04:20Z'))
    expect(quiet.seconds).toBe(260)
    expect(quiet.stale).toBe(true)
    expect(quiet.since).toBe('2026-10-15T13:00:00Z')
  })

  it('is stale strictly after the threshold, as `State.conn` is', () => {
    const at = (seconds: number): boolean =>
      feedSilence({ receivedAt: '2026-10-15T13:00:00Z' }, new Date(Date.parse('2026-10-15T13:00:00Z') + seconds * 1000)).stale
    expect(at(44)).toBe(false)
    expect(at(45)).toBe(false)
    expect(at(46)).toBe(true)
  })

  it('has measured no silence at all when no frame has arrived', () => {
    const none = feedSilence(null, new Date('2026-10-15T13:04:20Z'))
    // `null`, and not `0`: a console that has heard nothing has not measured a
    // silence of zero seconds, and `0` is a number a screen could draw.
    expect(none.seconds).toBeNull()
    expect(none.stale).toBe(false)
    expect(none.since).toBeNull()
  })
})

describe('the no-Firebase screen', () => {
  it('draws a cap the console cannot read as n/a, never as zero', () => {
    // The plan's red step, at this level: a renderer that defaulted a missing
    // cap to `0` draws `$0.00` here, and `$0.00` of a $50 cap is "this project
    // has spent nothing" -- a different fact from "there is no cap to read".
    const model = buildNoFirebaseModel(theDegradedDocument())
    const tree = renderNoFirebase(factory, model, 'light')
    const cap = one(tree, 'degraded-cap')
    expect(cap.textContent).toBe(UNAVAILABLE_MARK)
    expect(cap.textContent).not.toContain('0.00')
    expect(cap.getAttribute('data-state')).toBe('unavailable')
    expect(cap.getAttribute('title')).toBe(NO_CAPS_BECAUSE)
    expect(model.capsKnown).toBe(false)
  })

  it('offers no kill or resume control, rather than a disabled one', () => {
    const tree = renderNoFirebase(factory, buildNoFirebaseModel(theDegradedDocument()), 'light')
    expect(one(tree, 'degraded-caps').getAttribute('data-kill-capable')).toBe('false')
    // Nothing to press: no kill banner, and no control of any kind.
    expect(all(tree, 'live-kill')).toEqual([])
    expect(all(tree, 'degraded-kill')).toEqual([])
    expect(walk(tree).filter((node) => node.tag === 'button')).toEqual([])
  })

  it('draws the run records the degraded document carries', () => {
    const document = theDegradedDocument()
    const tree = renderNoFirebase(factory, buildNoFirebaseModel(document), 'light')
    expect(all(tree, 'degraded-run')).toHaveLength(document.runs.length)
    expect(drawn(tree)).toContain('20261015-123000-bb01')
    expect(drawn(tree)).toContain('failed')
    // The subscription row's model dollars are marked notional by name, so a
    // reader cannot add them to a billed figure.
    const notionalRows = walk(tree).filter((node) => node.getAttribute('data-notional-model') === 'true')
    expect(notionalRows).toHaveLength(1)
    expect(notionalRows[0]?.getAttribute('data-notional-model')).toBe('true')
  })

  it('does not price a run whose compute was not estimated at zero', () => {
    const document = theDegradedDocument()
    const unestimated = document.runs.find((run) => !run.cost.computeEstimated)
    expect(unestimated, 'the fixture has a run whose compute is not estimated').toBeDefined()
    const model = buildNoFirebaseModel(document)
    const row = model.runs.find((one) => one.runId === (unestimated?.runId ?? ''))
    expect(row?.computeUsd.state).toBe('unavailable')
    expect(row?.computeUsd.value).toBeNull()
  })

  it('marks compute as estimated, the state design section 6.1 rule 1 requires', () => {
    // Review round 5 of PR 478: this screen drew a row whose `compute_estimated`
    // was true as a plain measured figure -- `$0.90` -- where `projects.ts` and
    // `run.ts` draw `est. $0.90` on the same field. Compute is an estimate by
    // design, and a figure that is an estimate drawn as a measurement is the
    // wrong number that looks right.
    const document = theDegradedDocument()
    const estimated = document.runs.find((run) => run.cost.computeEstimated)
    expect(estimated, 'the fixture has a run whose compute was estimated').toBeDefined()
    const model = buildNoFirebaseModel(document)
    const row = model.runs.find((one) => one.runId === (estimated?.runId ?? ''))
    expect(row?.computeUsd.state).toBe('estimated')
    const tree = renderNoFirebase(factory, model, 'light')
    expect(drawn(tree)).toContain(ESTIMATED_MARK)
    // The row's own cell, not the totals row's: the totals are the CLI's own
    // sums and carry the lower-bound mark instead.
    expect(all(tree, 'degraded-run-compute')[0]?.textContent).toBe(`${ESTIMATED_MARK} $0.90`)
  })

  it('draws a non-zero compute whose flag is off as the number it is', () => {
    // The other half of the same helper: the zero is the case the flag exists
    // for, and a figure that is present is the figure -- hiding it would be the
    // worse of the two errors, which is what `projects.ts` says on this field.
    const document = theDegradedDocument()
    const row = document.runs.find((run) => run.cost.computeEstimated)
    if (row === undefined) throw new Error('the fixture has an estimated row')
    const odd: DegradedDocument = {
      ...document,
      runs: [{ ...row, cost: { ...row.cost, computeEstimated: false, computeUsd: measured(0.9) } }],
    }
    const model = buildNoFirebaseModel(odd)
    expect(model.runs[0]?.computeUsd.state).toBe('measured')
    expect(model.runs[0]?.computeUsd.value).toBe(0.9)
  })

  it('marks a total built over unknown compute as a lower bound', () => {
    const document = theDegradedDocument()
    expect(document.totals.computeNotEstimated.value ?? 0).toBeGreaterThan(0)
    const model = buildNoFirebaseModel(document)
    expect(model.totals.totalUsd.lowerBound).toBe(true)
    const tree = renderNoFirebase(factory, model, 'light')
    expect(one(tree, 'degraded-total-usd').textContent).toContain('≥')
  })

  it('keeps the lower bounds when the count itself is unavailable', () => {
    // Review round 5 of PR 478: `computeNotEstimated.value ?? 0` read an
    // *unavailable* count as "nothing was unestimated", so the totals lost their
    // `>=` mark while the note beside them still promised lower bounds -- the
    // skew case Task 3.2 exists for. `0` is the only value that makes a sum
    // exact; an unavailable count is an unknown, and an unknown makes the figure
    // a lower bound.
    const document = theDegradedDocument()
    const skewed: DegradedDocument = {
      ...document,
      totals: { ...document.totals, computeNotEstimated: unavailable('the count is not in this document') },
    }
    const model = buildNoFirebaseModel(skewed)
    expect(model.totals.computeNotEstimated.value).toBeNull()
    expect(model.totals.totalUsd.lowerBound).toBe(true)
    expect(model.totals.computeUsd.lowerBound).toBe(true)
    const tree = renderNoFirebase(factory, model, 'light')
    expect(one(tree, 'degraded-total-usd').textContent).toContain('≥')
    // The count is drawn `n/a`, not as a zero, and the note goes with the marks.
    expect(one(tree, 'degraded-compute-unknown').textContent).toBe('n/a')
    expect(all(tree, 'degraded-compute-word')).toHaveLength(1)
  })

  it('draws a counted zero as an exact sum, with no lower-bound note', () => {
    // The one document that makes these figures exact, and the note follows the
    // marks rather than contradicting them.
    const document = theDegradedDocument()
    const counted: DegradedDocument = {
      ...document,
      totals: { ...document.totals, computeNotEstimated: measured(0) },
    }
    const model = buildNoFirebaseModel(counted)
    expect(model.totals.totalUsd.lowerBound).toBe(false)
    const tree = renderNoFirebase(factory, model, 'light')
    expect(one(tree, 'degraded-total-usd').textContent).not.toContain('≥')
    expect(all(tree, 'degraded-compute-word')).toEqual([])
  })

  it('shows the CLI’s notice and every line that was not JSON', () => {
    const tree = renderNoFirebase(factory, buildNoFirebaseModel(theDegradedDocument(), theNotices()), 'light')
    expect(one(tree, 'degraded-notice').textContent).toBe('no budget backend: showing run records only')
    const warnings = all(tree, 'degraded-warning').map((node) => node.textContent)
    expect(warnings).toHaveLength(1)
    expect(warnings[0]).toContain('run records unavailable (retrying)')
  })

  it('says it is polling, because the degraded document has no connectivity', () => {
    const tree = renderNoFirebase(factory, buildNoFirebaseModel(theDegradedDocument()), 'light')
    expect(one(tree, 'degraded-mode').textContent).toBe('polling')
    expect(one(tree, 'degraded-mode').getAttribute('data-state')).toBe('polling')
  })

  it('carries the theme, and one value per theme the design names', () => {
    const model = buildNoFirebaseModel(theDegradedDocument())
    for (const theme of DEGRADED_THEMES) {
      const tree = renderNoFirebase(factory, model, theme as LiveTheme)
      expect((tree as FakeScreen).getAttribute('data-theme')).toBe(theme)
      expect((tree as FakeScreen).getAttribute('data-kind')).toBe('no-firebase')
    }
  })

  it('gives every figure it draws the alignment hook, and gives it to no other cell', () => {
    // Design §2.10's gap, closed by the styling work (plan Task 6.4): five
    // screens have a `numberCell` and this one prepended no class at all, so the
    // degraded screen's figures had no tabular-figures and no right-align hook
    // while every other screen's did. The hook is `degraded-num`, the rule that
    // reads it is in `theme/screens.css` beside the other four, and this is the
    // case that fails if the prepend is removed.
    const model = buildNoFirebaseModel(theDegradedDocument())
    const tree = renderNoFirebase(factory, model, 'light')
    // The feed age is drawn only when the console has a frame to age, so it is
    // asserted on a model that has one: the same document, handed a feed.
    const frame = theLiveFrames()[0]
    if (frame === undefined) throw new Error('the synthetic fixture has no live frame')
    const fed = renderNoFirebase(factory, { ...model, feed: aFeed(frame, 90) }, 'light')
    const figures = [
      'degraded-runs',
      'degraded-model-usd',
      'degraded-model-notional-usd',
      'degraded-compute-usd',
      'degraded-total-usd',
      'degraded-compute-unknown',
      'degraded-run-model',
      'degraded-run-compute',
      'degraded-run-total',
    ]
    for (const figure of figures) {
      // `span` only, and `degraded-runs` is why: §2.7 gives the list and the
      // count on it the same class, so the region comes first in document order
      // and only the `span` is a reading.
      const nodes = all(tree, figure).filter((node) => node.tag === 'span')
      expect(nodes.length, `.${figure} is not drawn as a reading at all, so the case is vacuous for it`).toBeGreaterThan(0)
      for (const node of nodes) expect(node.classes, `.${figure} carries no degraded-num`).toContain('degraded-num')
    }
    // And the hook is the readings' rather than the screen's: a cell that draws
    // a document string is not a number and may not carry it.
    for (const text of ['degraded-project', 'degraded-mode', 'degraded-run-id', 'degraded-run-created', 'degraded-since-at']) {
      for (const node of all(tree, text)) expect(node.classes, `.${text} is not a figure`).not.toContain('degraded-num')
    }
    const age = one(fed, 'degraded-since-age')
    expect(age.classes).toContain('degraded-num')
    expect(one(fed, 'degraded-since-at').classes).not.toContain('degraded-num')
  })
})

describe('the stale Live screen', () => {
  it('breaks the graphs instead of interpolating across the silence', () => {
    // A history that has seen one frame, then goes quiet for a minute and a
    // half, then hears from the CLI again: the silence is a **gap sample**, so
    // the sparkline is two segments rather than one line drawn through a
    // measurement nobody made (design section 6.1 rule 1).
    const history = new BurnHistory()
    const first = theLiveFrame()
    buildLiveModel(first, history, aFeed(first, 2))
    const stale = buildLiveModel(first, history, aFeed(first, 90))
    const bars = stale.repos[0]?.spark.bars ?? []
    expect(bars[bars.length - 1]?.height.state).toBe('gap')
    // The last-known sample is still drawn: the screen keeps what it read.
    expect(bars.some((bar) => bar.height.value !== null)).toBe(true)

    const later = theLaterFrame()
    const recovered = buildLiveModel(later, history, aFeed(later, 1))
    expect(recovered.repos[0]?.spark.segments).toBe(2)
    const tree = renderLive(factory, recovered, 'light')
    expect(one(tree, 'live-burn').getAttribute('data-segments')).toBe('2')
    expect(all(tree, 'live-burn-bar').some((bar) => bar.getAttribute('data-state') === 'gap')).toBe(true)
  })

  it('draws each sample once and in time order across repeated stale rebuilds', () => {
    // The probe from the gate review: one fresh build, then the banner is drawn
    // by rebuilding with the last document and no new frame, every ten seconds.
    // Each rebuild used to observe the old sample again, so the last value was
    // redrawn after every gap, out of time order (design section 6.3).
    const history = new BurnHistory()
    const frame = theLiveFrame()
    buildLiveModel(frame, history, aFeed(frame, 2))
    let bars: readonly { readonly at: string; readonly height: { readonly state: string } }[] = []
    for (const seconds of [50, 60, 70, 80]) {
      bars = buildLiveModel(frame, history, aFeed(frame, seconds)).repos[0]?.spark.bars ?? []
    }
    const stamps = bars.map((bar) => Date.parse(bar.at))
    expect(stamps).toEqual([...stamps].sort((a, b) => a - b))
    expect(new Set(stamps).size).toBe(stamps.length)
    // The document's sample appears once, and nothing measured follows a gap.
    expect(bars.filter((bar) => bar.at === frame.now && bar.height.state !== 'gap')).toHaveLength(1)
    const firstGap = bars.findIndex((bar) => bar.height.state === 'gap')
    expect(firstGap).toBeGreaterThan(0)
    expect(bars.slice(firstGap).every((bar) => bar.height.state === 'gap')).toBe(true)
    // One silence is one gap, stamped at the console's latest reading.
    expect(bars.filter((bar) => bar.height.state === 'gap')).toHaveLength(1)
    expect(bars[bars.length - 1]?.at).toBe(aFeed(frame, 80).at)
  })

  it('keeps the last-known bar when the console clock is behind the CLI clock', () => {
    // `document.now` is the CLI's clock and `feed.at` the console's. The rule:
    // the gap is stamped on the CLI's clock (the last stamp plus the measured
    // silence), so a console that is behind neither pushes the last-known bar
    // out of the window nor stamps the gap before it.
    const history = new BurnHistory()
    const frame = theLiveFrame()
    const behind: FeedHealth = {
      since: frame.now,
      at: new Date(Date.parse(frame.now) - 120_000).toISOString().replace(/\.\d{3}Z$/, 'Z'),
      seconds: 90,
      stale: true,
    }
    const bars = buildLiveModel(frame, history, behind).repos[0]?.spark.bars ?? []
    expect(bars.map((bar) => bar.height.state === 'gap')).toEqual([false, true])
    expect(bars[0]?.at).toBe(frame.now)
    expect(Date.parse(bars[1]?.at ?? '')).toBeGreaterThanOrEqual(Date.parse(bars[0]?.at ?? ''))
  })

  it('draws fresh bars at once after a stale episode when the console clock is far ahead of the CLI clock', () => {
    // Review round 3 of PR 494: a console clock ten minutes ahead stamped the
    // gap at console time, the history pruned at that stamp, and every fresh
    // document was older than the cut -- dropped on arrival, bars empty for the
    // next five minutes of frames. The samples live on the CLI's clock: the gap
    // is the last document's stamp plus the silence the console measured (a
    // duration, which no clock offset changes), and the console's own stamp
    // only decides *whether* the feed is stale.
    const history = new BurnHistory()
    const frame = theLiveFrame()
    const later = (seconds: number): string =>
      new Date(Date.parse(frame.now) + seconds * 1000).toISOString().replace(/\.\d{3}Z$/, 'Z')
    buildLiveModel(frame, history)
    const ahead: FeedHealth = { since: later(600), at: later(690), seconds: 90, stale: true }
    const stale = buildLiveModel(frame, history, ahead).repos[0]?.spark.bars ?? []
    expect(stale.map((bar) => bar.height.state === 'gap')).toEqual([false, true])
    expect(stale[1]?.at).toBe(later(90))
    for (const seconds of [100, 110, 120]) {
      const fresh: LiveDocument = { ...frame, now: later(seconds) }
      const bars = buildLiveModel(fresh, history, { since: later(600 + seconds), at: later(601 + seconds), seconds: 1, stale: false })
        .repos[0]?.spark.bars ?? []
      const last = bars[bars.length - 1]
      expect(last?.at, String(seconds)).toBe(fresh.now)
      expect(last?.height.state, String(seconds)).not.toBe('gap')
    }
  })

  it('bounds the history even when the stamps cannot be placed in time', () => {
    // Review round 3 of PR 494: 5001 samples with unparseable stamps were all
    // kept. The window is the first bound; LIVE_BURN_MAX_SAMPLES is the second.
    const history = new BurnHistory()
    const frame = theLiveFrame()
    const burn = buildLiveModel(frame, new BurnHistory()).repos[0]?.burnPerMin ?? measured(0)
    for (let index = 0; index < 5000; index += 1) {
      history.observe('k', { at: `not a time ${String(index)}`, reading: burn })
    }
    expect(history.size('k')).toBeLessThanOrEqual(LIVE_BURN_MAX_SAMPLES)
    // ... and a run of placeable stamps a second apart, longer than the bound.
    const placed = new BurnHistory()
    for (let index = 0; index < 5000; index += 1) {
      placed.observe('k', { at: new Date(Date.parse(frame.now) + index * 10).toISOString(), reading: burn })
    }
    expect(placed.size('k')).toBeLessThanOrEqual(LIVE_BURN_MAX_SAMPLES)
    expect(placed.window('k', new Date(Date.parse(frame.now) + 4999 * 10).toISOString()).length).toBeGreaterThan(0)
  })

  it('places 20,000 out-of-order samples in milliseconds, in time order', () => {
    // Review round 4 of PR 494: each out-of-order sample walked the list back
    // with a `Date.parse` per step, so 20,000 reverse-ordered samples took
    // 5.9 s. Placement is now a binary search over stamps parsed once.
    const frame = theLiveFrame()
    const burn = buildLiveModel(frame, new BurnHistory()).repos[0]?.burnPerMin ?? measured(0)
    const base = Date.parse(frame.now)
    for (const step of [1000, 10]) {
      const history = new BurnHistory()
      const start = performance.now()
      for (let index = 20_000; index > 0; index -= 1) {
        history.observe('k', { at: new Date(base + index * step).toISOString(), reading: burn })
      }
      expect(performance.now() - start, String(step)).toBeLessThan(250)
      expect(history.size('k')).toBeLessThanOrEqual(LIVE_BURN_MAX_SAMPLES)
      const stamps = history.window('k', new Date(base + 20_000 * step).toISOString()).map((sample) => Date.parse(sample.at))
      expect(stamps.length).toBeGreaterThan(0)
      expect(stamps).toEqual([...stamps].sort((a, b) => a - b))
    }
  })

  it('keeps the last-known values and says when they were read', () => {
    const frame = theLiveFrame()
    const model = buildLiveModel(frame, new BurnHistory(), aFeed(frame, 90))
    const tree = renderLive(factory, model, 'light')
    const banner = one(tree, 'live-stale')
    expect(banner.getAttribute('data-stale')).toBe('true')
    expect(banner.getAttribute('data-silence')).toBe(formatReading({ state: 'measured', value: 90, because: '', lowerBound: false }, 'seconds'))
    expect(drawn(tree)).toContain('no document has arrived for')
    expect(drawn(tree)).toContain('stale after 45 s')
    // The values themselves are still drawn -- the screen keeps the last ones it
    // read rather than emptying itself -- and the root carries the arrival stamp
    // they came from.
    expect(one(tree, 'live-since-at').textContent).toBe(frame.now)
    expect((tree as FakeScreen).getAttribute('data-received-at')).toBe(frame.now)
    expect(all(tree, 'live-repo')).toHaveLength(model.repos.length)
  })

  it('draws no banner at all when the caller keeps no feed reading', () => {
    const frame = theLiveFrame()
    const tree = renderLive(factory, buildLiveModel(frame, new BurnHistory()), 'light')
    expect(all(tree, 'live-stale')).toEqual([])
    expect(all(tree, 'live-since')).toEqual([])
    expect((tree as FakeScreen).getAttribute('data-received-at')).toBe('')
  })
})

describe('the console with no binary to read', () => {
  it('gives the one figure it draws the same alignment hook as the degraded screen', () => {
    // The searched count is this screen's only reading, and it comes through the
    // same `numberCell` as the degraded screen's figures -- so it carries the
    // same hook (design §2.10: the hook is the helper's, not the screen's).
    const tree = renderUnavailable(factory, anAbsentState(), 'light')
    expect(one(tree, 'unavailable-searched-count').classes).toContain('degraded-num')
  })

  it('names what is needed, the install command and the picker, and draws no dashboard', () => {
    const state = anAbsentState()
    expect(state.installCommand).toBe(FUGARO_INSTALL_COMMAND)
    expect(state.searched.length).toBeGreaterThan(0)
    const tree = renderUnavailable(factory, state, 'light')
    expect((tree as FakeScreen).getAttribute('data-kind')).toBe('no-binary')
    expect(one(tree, 'unavailable-headline').textContent).toBe('no fugaro binary')
    expect(one(tree, 'unavailable-install-command').textContent).toBe(FUGARO_INSTALL_COMMAND)
    expect(one(tree, 'unavailable-picker').getAttribute('data-picker')).toBe('binary')
    // How many places were searched, and never which: no path is drawn on this
    // console -- the `path` class turns every one of them into `<path>`, and a
    // column of `<path>` under a sentence saying the console looked somewhere
    // tells the operator nothing. The count is the fact that survives.
    expect(one(tree, 'unavailable-searched-count').textContent).toBe(String(state.searched.length))
    const drawn = walk(tree).map((node) => node.textContent).join('\n')
    expect(drawn).not.toContain('/opt/homebrew')
    expect(drawn).not.toContain('/usr/local/bin')
    // The resolution's own sentence is read as prose, so the paths inside it
    // are drawn as the console's `<path>` marker -- the design's own mark for a
    // path, and the reason the sentence is still worth drawing: it says the
    // console looked and that nothing was there, and each place it looked is
    // marked rather than named.
    expect(one(tree, 'unavailable-reason').textContent).toContain('no fugaro binary was found')
    expect(one(tree, 'unavailable-reason').textContent).toContain('<path>')
    // Not a partially drawn dashboard: no screen, no cap, no run list.
    expect(all(tree, 'live')).toEqual([])
    expect(all(tree, 'degraded-caps')).toEqual([])
    expect(all(tree, 'degraded-run')).toEqual([])
    expect(walk(tree).filter((node) => node.tag === 'section')).toHaveLength(1)
  })
})

describe('the console after a refused read', () => {
  it('shows the CLI’s text unchanged, remedy and roles included', () => {
    const state = aRefusedState()
    const tree = renderUnavailable(factory, state, 'light')
    const sentence = one(tree, 'unavailable-sentence')
    // Character for character: this is the assertion the plan asks for, and the
    // `toBe` is the whole of it.
    expect(sentence.textContent).toBe(REFUSAL_SENTENCE)
    expect(sentence.textContent).toContain('roles/datastore.viewer')
    expect(sentence.textContent).toContain('roles/serviceusage.serviceUsageConsumer')
    expect(one(tree, 'unavailable-operation').textContent).toBe('budget.show')
    expect((tree as FakeScreen).getAttribute('data-kind')).toBe('refused')
    expect(all(tree, 'live')).toEqual([])
  })

  it('is drawn verbatim because `putText` would eat the remedy', () => {
    // This is why the sentence does not go through the `sentence` class,
    // asserted rather than argued: the free-text rules redact inside it, and one
    // of the things they redact is the answer to the operator's problem. Review
    // round 5 of PR 478 kept the assertion and changed what the screen does with
    // it: `refusedState` runs `sanitizeRefusal`, which is the credential rules
    // and nothing else, so the remedy survives while a token does not.
    const through = sanitizeText(REFUSAL_SENTENCE, 'sentence', 'the CLI sentence')
    expect(through.ok).toBe(true)
    const text = through.ok ? through.text : ''
    expect(text).not.toBe(REFUSAL_SENTENCE)
    expect(text).toContain('[redacted]')
    expect(text).not.toContain('roles/datastore.viewer')
    // And the door the screen actually uses draws it character for character.
    const drawn = sanitizeRefusal(REFUSAL_SENTENCE, 'the CLI refusal sentence')
    expect(drawn.ok && drawn.text).toBe(REFUSAL_SENTENCE)
  })

  it('still redacts a credential a child echoed into its refusal', () => {
    // The half the raw `literal` this screen used to call skipped: a sentence is
    // not a document, but it is also not the console's own text, and a child
    // that prints the token it failed with must not put it on a screen.
    const token = githubToken()
    const refusal = refusalFrom('budget.show', `permission denied for ${token}`)
    const tree = renderUnavailable(factory, refusedState('budget.show', refusal), 'light')
    const sentence = one(tree, 'unavailable-sentence')
    expect(sentence.textContent).not.toContain(token)
    expect(sentence.textContent).toBe(`permission denied for ${TOKEN_PLACEHOLDER}`)
  })

  it('sanitizes again when a caller builds the state without refusedState', () => {
    const token = githubToken()
    const built: RefusedState = { kind: 'refused', operation: `budget.show ${token}`, sentence: `denied for ${token}` }
    const tree = renderUnavailable(factory, built, 'light')
    expect(one(tree, 'unavailable-sentence').textContent).toBe(`denied for ${TOKEN_PLACEHOLDER}`)
    expect(one(tree, 'unavailable-operation').textContent).toBe(`budget.show ${TOKEN_PLACEHOLDER}`)
    expect(drawn(tree)).not.toContain(token)
  })

  it('does not offer a link, a control or a second sentence about the fix', () => {
    const tree = renderUnavailable(factory, aRefusedState(), 'light')
    // The remedy is *inside* the CLI's sentence; a console that restated it
    // could restate it wrongly, and the operator would have two to reconcile.
    expect(walk(tree).filter((node) => node.tag === 'a' || node.tag === 'button')).toEqual([])
    expect(all(tree, 'unavailable-install-command')).toEqual([])
  })
})

describe('the degraded document’s own warnings (review round 2 of PR 494)', () => {
  it('draws warnings[] through the sentence class, a token in one redacted', () => {
    const token = githubToken()
    const document: DegradedDocument = {
      ...theDegradedDocument(),
      warnings: ['a failed image rebuild runs a stale image', `the image status can't be read: ${token}`],
    }
    const tree = renderNoFirebase(factory, buildNoFirebaseModel(document), 'light')
    const drawnWarnings = all(tree, 'degraded-document-warning').map((node) => node.textContent)
    expect(drawnWarnings).toEqual([
      'a failed image rebuild runs a stale image',
      `the image status can't be read: ${TOKEN_PLACEHOLDER}`,
    ])
    expect(drawn(tree)).not.toContain(token)
  })
})
