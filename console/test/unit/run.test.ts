// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// Run detail's header, its status strip, its stage timeline and the two-column
// body (console redesign, step 3a; design section 2.6).
//
// Four things are held here, and each is a sentence of the redesign rather than
// a preference:
//
//   1. **the way back.** The breadcrumb's `Live` crumb is a real `button` and
//      the retry is a real `button` that says it does nothing -- the console
//      navigates and acts with buttons and never with a navigating attribute,
//      and `entry.ts` binds the one crumb listener this screen asks for;
//   2. **the strip never draws a blank.** An outcome or a stage the record left
//      out is the console's own `n/a` with its reason in the `title`, and a tone
//      is a second channel beside the word and never the only one;
//   3. **a bar is a share of the longest measured stage**, and a stage nothing
//      timed draws no fill at all -- a zero-width fill beside a track would say
//      "this stage took no time", which is a different fact from "nothing timed
//      it";
//   4. **the two columns are the reading order** the design lists, and no node
//      the screen draws carries an attribute that could act (the fake throws on
//      the navigating attribute, a resource, a style and every handler, so a
//      screen that reached for one fails here).

import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import { UNAVAILABLE_MARK, estimated, measured, notional, unavailable } from '../../src/shared/availability'
import { ADDRESS_PLACEHOLDER } from '../../src/shared/sanitize'
import {
  RUN_LOGS_BUSY,
  RUN_RECORD_BUSY,
  buildRunModel,
  renderRun,
  renderRunFrame,
  runBusyLabel,
  runMainChildren,
  runRecordChildren,
} from '../../src/renderer/screens/run'
import type {
  RunFactory,
  RunModel,
  RunRow,
  RunStage,
  ScreenElement,
} from '../../src/renderer/screens/run'
import { WRITE_UNAVAILABLE } from '../../src/renderer/writes'

/** TRACKED is the run the header names, and TRACKED_REPO its repository: the
 * two strings the breadcrumb and the heading are read against. */
const TRACKED = '20261029-213000-ab12'
const TRACKED_REPO = 'example-owner/example-repo'

/**
 * FakeScreen is the element a screen draws into, the shape `live.test.ts` uses
 * for the same reason: the unit suite has no `document` and the point is the
 * *observation*. Every attribute that could make the window do something --
 * the navigating attribute, a resource, an event handler -- throws, so a screen
 * that reached for one fails here rather than in a window nobody is watching.
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
      throw new Error(`the run screen may not set the ${name} attribute`)
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

/** factory makes fresh fakes, in the shape the renderer is handed. The two
 * methods are the two namespaces the real factory has, and the fake remembers
 * which one made a node so that the attribute rule below can be about SVG nodes
 * rather than about a tag that happens to be spelled `svg`. */
const factory: RunFactory = {
  create: (tag) => new FakeScreen(tag),
  createNS: (tag) => new FakeScreen(tag, 'svg'),
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

/** texts is the text of every node carrying a class, in document order. */
function texts(node: ScreenElement, className: string): readonly (string | null)[] {
  return all(node, className).map((found) => found.textContent)
}

/** drawn is the whole tree's text, in document order: what a person reading the
 * screen would read if they read every cell. */
function drawn(node: ScreenElement): string {
  return walk(node)
    .map((found) => found.textContent ?? '')
    .filter((text) => text !== '')
    .join('\n')
}

/** classesOf is the first class of each child, which is what the tree order
 * cases read a container by. */
function classesOf(node: ScreenElement): readonly string[] {
  return node.childNodes().map((child) => (child as FakeScreen).classes[0] ?? '')
}

/** LEGACY_DOCUMENT is the sentence a value the model holds as an `unavailable`
 * reading is drawn with; the exact wording is not what these cases read. */
const LEGACY_DOCUMENT = 'the document carries no timing for this value'

/** ROW is the record every case starts from: a run with nothing absent, so a
 * case that wants an absent field says which one. */
const ROW: RunRow = Object.freeze({
  run: TRACKED,
  runId: TRACKED,
  repo: TRACKED_REPO,
  workflow: 'go',
  recipe: 'review',
  oauthAccount: 'ops@example.test',
  status: 'running',
  stage: 'verify',
  reason: null,
  branch: 'main',
  outcome: 'ready',
  requestedBy: 'ana',
  launchedBy: 'ci-runner',
  created: null,
  startedAt: null,
  imageAge: measured(90),
  terminal: false,
  settled: false,
  followUp: false,
  staleDraft: false,
  rigor: null,
})

/** model is the whole screen, with one field overridable at a time. */
function model(over: Partial<RunModel> = {}, rowOver: Partial<RunRow> = {}): RunModel {
  return {
    row: { ...ROW, ...rowOver },
    ledger: { model: measured(1), compute: measured(0.5), total: measured(1.5), notional: unavailable(LEGACY_DOCUMENT) },
    spendStages: [],
    spendStagesDropped: measured(0),
    tolerated: null,
    stages: [],
    halt: null,
    pullRequest: { repository: TRACKED_REPO, number: 412, url: null, link: null, because: 'no link' },
    logs: {
      lines: [],
      total: measured(0),
      dropped: measured(0),
      refused: measured(0),
      link: null,
      linkBecause: null,
    },
    diagnosis: {
      failed: [],
      flaky: [],
      findings: [],
      agentMessage: null,
      logTail: [],
      reportPath: null,
      draftNote: null,
      commentsPath: null,
      byRoute: [],
      byModel: [],
      reportedUsd: measured(0),
      chargeUsd: measured(0),
      reportedWarning: null,
      pinWarnings: [],
      followUpPr: measured(0),
      followUpComments: measured(0),
      followUpSession: null,
    },
    replay: {
      records: measured(0),
      windowSeconds: measured(0),
      dropped: measured(0),
      from: null,
      to: null,
      stamps: [],
      scrubbed: null,
      scrubbedBecause: null,
    },
    warnings: [],
    ...over,
  }
}

/** stage is one stage of the timeline, a passing build unless a case says
 * otherwise. */
function stage(over: Partial<RunStage> = {}): RunStage {
  return {
    stage: 'build',
    passed: true,
    tests: measured(12),
    failures: measured(0),
    duration: measured(30),
    spend: unavailable(LEGACY_DOCUMENT),
    deadline: null,
    deadlineBecause: LEGACY_DOCUMENT,
    ...over,
  }
}

// ---------------------------------------------------------------------------
// The breadcrumb and the header.
// ---------------------------------------------------------------------------

describe('renderRun: the header', () => {
  it('draws the way back as a breadcrumb of three crumbs, the first a button', () => {
    const tree = renderRun(factory, model(), 'light')
    const crumb = one(tree, 'run-crumb')
    expect(crumb.tag).toBe('nav')
    expect(crumb.getAttribute('aria-label')).toBe('Breadcrumb')

    const live = one(tree, 'run-crumb-live')
    expect(live.tag, 'the Live crumb is the way back, and the console navigates with buttons').toBe('button')
    expect(live.textContent).toBe('Live')
    expect(one(tree, 'run-crumb-repo').textContent).toBe(TRACKED_REPO)
    expect(one(tree, 'run-crumb-run').textContent).toBe(TRACKED)

    // The slashes separate the crumbs visually and are not part of what a
    // breadcrumb reads out.
    const separators = all(tree, 'run-crumb-sep')
    expect(separators).toHaveLength(2)
    for (const separator of separators) expect(separator.getAttribute('aria-hidden')).toBe('true')
  })

  it('draws the run id as the one heading', () => {
    const tree = renderRun(factory, model(), 'light')
    const heading = one(tree, 'run-id')
    expect(heading.tag).toBe('h1')
    expect(heading.textContent).toBe(TRACKED)
  })

  it('draws the retry as a real button that says it does nothing, and binds no listener', () => {
    const tree = renderRun(factory, model(), 'light')
    const retry = one(tree, 'run-retry')
    expect(retry.tag).toBe('button')
    expect(retry.getAttribute('type')).toBe('button')
    expect(retry.getAttribute('aria-disabled')).toBe('true')
    expect(retry.getAttribute('title')).toBe(WRITE_UNAVAILABLE)
    // The label is drawn through `disabledWrite`'s own span, so the button
    // carries a name a reader hears and the screen carries the words.
    expect(drawn(tree)).toContain('Retry run')

    // The screen draws the refusal and never performs the write: a listener
    // here would be the write the design withholds. `entry.ts` binds the
    // breadcrumb's crumb and nothing on this screen.
    const source = readFileSync(join(__dirname, '..', '..', 'src', 'renderer', 'screens', 'run.ts'), 'utf8')
    expect(source).not.toContain('addEventListener')
  })

  it('draws the meta line with a label and a value for each of the record\'s own columns', () => {
    const tree = renderRun(factory, model(), 'light')
    expect(texts(tree, 'run-meta-label')).toEqual([
      'repo',
      'workflow',
      'recipe',
      'branch',
      'account',
      'requested by',
      'started by',
    ])
    expect(one(tree, 'run-workflow').textContent).toBe('go')
    expect(one(tree, 'run-recipe').textContent).toBe('review')
    expect(one(tree, 'run-branch').textContent).toBe('main')
    // The account is an identity and goes through the `person` class, the class
    // the wire key carries: an address is the console's marker rather than
    // itself, here as in the requester cell beside it (`degraded-run-account`
    // is drawn the same way).
    expect(one(tree, 'run-account').textContent).toBe(ADDRESS_PLACEHOLDER)
    expect(drawn(tree)).not.toContain('ops@example.test')
  })

  it('draws an absent column as the mark with its reason, never as a blank cell', () => {
    const tree = renderRun(factory, model({}, { oauthAccount: null, recipe: null }), 'light')
    for (const [hook, field] of [
      ['run-account', 'row.oauth_account'],
      ['run-recipe', 'row.recipe'],
    ] as const) {
      const cell = one(tree, hook)
      expect(cell.textContent, `${hook} is never blank`).toBe(UNAVAILABLE_MARK)
      expect(cell.getAttribute('title')).toContain(field)
      expect(cell.classes, `${hook} is drawn in the mark's own class`).toContain('run-meta-absent')
    }
  })

  it('draws no started-by field at all for a record that carries no launcher', () => {
    // A record written before `launch.json` existed has no launcher, and its
    // `n/a` would read as a launcher the console could not name rather than as
    // one the record never carried (projects.test.ts holds the same sentence).
    const tree = renderRun(factory, model({}, { launchedBy: null, requestedBy: null }), 'light')
    expect(all(tree, 'run-started-by')).toHaveLength(0)
    expect(all(tree, 'run-requested-by')).toHaveLength(0)
    expect(texts(tree, 'run-meta-label'), 'and no label hangs over nothing').toEqual([
      'repo',
      'workflow',
      'recipe',
      'branch',
      'account',
    ])
  })

  it('draws no Rigor row for a record that carries no block', () => {
    expect(all(renderRun(factory, model(), 'light'), 'run-rigor')).toHaveLength(0)
    expect(all(renderRun(factory, model(), 'light'), 'run-halt')).toHaveLength(0)
  })

  it('draws the halt banner only for a record that carries a halt', () => {
    const halt = { reason: 'budget', scope: 'repo', at: '2026-10-01T09:00:00Z', detail: null }
    const tree = renderRun(factory, model({ halt }), 'light')
    expect(texts(tree, 'run-halt-word')).toEqual(['HALTED'])
    expect(one(tree, 'run-halt-reason').textContent).toBe('budget')
    expect(texts(tree, 'run-halt-scope-word')).toEqual(['scope'])
    expect(one(tree, 'run-halt-scope').textContent).toBe('repo')
    expect(texts(tree, 'run-halt-at-word')).toEqual(['at'])
    expect(one(tree, 'run-halt-at').textContent).toBe('2026-10-01T09:00:00Z')

    const bare = renderRun(factory, model({ halt: { ...halt, scope: null, at: null } }), 'light')
    expect(all(bare, 'run-halt-scope')).toHaveLength(0)
    expect(all(bare, 'run-halt-at')).toHaveLength(0)
    expect(all(bare, 'run-halt-sep')).toHaveLength(0)
  })
})

// ---------------------------------------------------------------------------
// The status strip.
// ---------------------------------------------------------------------------

describe('renderRun: the status strip', () => {
  it('draws four cells, each a caption over a value', () => {
    const tree = renderRun(factory, model(), 'light')
    expect(all(tree, 'run-strip-cell')).toHaveLength(4)
    expect(texts(tree, 'run-strip-label')).toEqual(['LIFECYCLE', 'OUTCOME', 'STAGE', 'LEDGER'])
    expect(one(tree, 'run-status').textContent).toBe('running')
    expect(one(tree, 'run-outcome').textContent).toBe('ready')
    expect(one(tree, 'run-stage').textContent).toBe('verify')
  })

  it('draws an absent outcome or stage as the mark with its reason, never blank', () => {
    const tree = renderRun(factory, model({}, { outcome: null, stage: null }), 'light')
    for (const [hook, field] of [
      ['run-outcome', 'row.outcome'],
      ['run-stage', 'row.stage'],
    ] as const) {
      const cell = one(tree, hook)
      expect(cell.textContent, `${hook} is never blank`).toBe(UNAVAILABLE_MARK)
      expect(cell.getAttribute('title')).toContain(field)
    }
    // An absent outcome has no tone either: the tone is a colour for a word, and
    // there is no word.
    expect(one(tree, 'run-outcome').getAttribute('data-tone')).toBeNull()
  })

  it('draws the outcome\'s tone beside the word and never instead of it', () => {
    const ready = one(renderRun(factory, model(), 'light'), 'run-outcome')
    expect(ready.textContent).toBe('ready')
    expect(ready.getAttribute('data-tone')).toBe('success')

    // `draft` is the failure tone: runstore's own `StatusFailed` is "a draft PR
    // exists".
    const failed = one(renderRun(factory, model({}, { outcome: 'draft' }), 'light'), 'run-outcome')
    expect(failed.textContent, 'the word is always drawn').toBe('draft')
    expect(failed.getAttribute('data-tone')).toBe('failure')

    // `none` and a word this build does not know are drawn in the primary tone
    // with no tone of their own: runstore gives `none` two readings, and
    // painting either of them would be a claim about the run rather than a
    // reading of it.
    for (const word of ['none', 'wrapped-up']) {
      const other = one(renderRun(factory, model({}, { outcome: word }), 'light'), 'run-outcome')
      expect(other.textContent).toBe(word)
      expect(other.getAttribute('data-tone')).toBeNull()
    }
  })

  it('draws the ledger cell as the settled/terminal pair, in words', () => {
    const tree = renderRun(factory, model(), 'light')
    expect(texts(tree, 'run-settled')).toEqual(['still moving'])
    expect(texts(tree, 'run-flags-state')).toEqual(['· still running'])

    const settled = renderRun(factory, model({}, { settled: true, terminal: true, staleDraft: true, followUp: true }), 'light')
    expect(texts(settled, 'run-settled')).toEqual(['settled'])
    expect(texts(settled, 'run-flags-state')).toEqual(['· terminal'])
    expect(texts(settled, 'run-stale')).toEqual(['stale draft'])
    expect(texts(settled, 'run-follow-up')).toEqual(['follow-up'])
  })
})

// ---------------------------------------------------------------------------
// The stage timeline.
// ---------------------------------------------------------------------------

describe('renderRun: the stage timeline', () => {
  it('draws a bar as a share of the longest measured stage', () => {
    const stages = [
      stage({ stage: 'build', duration: measured(10) }),
      stage({ stage: 'test', duration: measured(20) }),
      stage({ stage: 'lint', duration: unavailable(LEGACY_DOCUMENT) }),
    ]
    const tree = renderRun(factory, model({ stages }), 'light')
    expect(texts(tree, 'run-stage-name')).toEqual(['build', 'test', 'lint'])
    expect(texts(tree, 'run-stage-duration')).toEqual(['10 s', '20 s', UNAVAILABLE_MARK])

    // Half the longest is half the axis, and the longest is the whole of it.
    const fills = all(tree, 'run-stage-fill')
    expect(fills).toHaveLength(2)
    expect(fills[0]?.getAttribute('width')).toBe('50')
    expect(fills[1]?.getAttribute('width')).toBe('100')

    // A stage nothing timed draws no fill: its slot holds a track and nothing
    // else, so it is never read as a stage that took no time.
    const bars = all(tree, 'run-stage-bar')
    expect(bars).toHaveLength(3)
    expect(bars[2]?.childNodes()).toHaveLength(1)
    for (const icon of all(tree, 'run-stage-icon')) expect(icon.getAttribute('aria-hidden')).toBe('true')
  })

  it('draws the longest stage alone at the whole axis when it is the only one timed', () => {
    const tree = renderRun(factory, model({ stages: [stage({ duration: measured(7) })] }), 'light')
    expect(all(tree, 'run-stage-fill')[0]?.getAttribute('width')).toBe('100')
  })

  it('draws a failed stage\'s bar in the failure fill and a passed stage\'s in the live one', () => {
    const tree = renderRun(factory, model({ stages: [stage({ duration: measured(4) }), stage({ passed: false, duration: measured(4) })] }), 'light')
    expect(all(tree, 'run-stage-fill')).toHaveLength(1)
    expect(all(tree, 'run-stage-fill-failed')).toHaveLength(1)
  })

  it('keeps the passed/failed word as text beside a decorative mark', () => {
    const tree = renderRun(factory, model({ stages: [stage({ passed: true }), stage({ stage: 'test', passed: false }), stage({ stage: 'lint', passed: null })] }), 'light')
    expect(texts(tree, 'run-stage-passed')).toEqual(['passed', 'failed'])
    for (const icon of all(tree, 'run-stage-icon')) expect(icon.getAttribute('aria-hidden')).toBe('true')
    expect(all(tree, 'run-stage-check')).toHaveLength(1)
    expect(all(tree, 'run-stage-cross')).toHaveLength(1)
    // A record whose outcome this build does not know draws the neutral mark and
    // no word: `run-stage-passed` is a word about the run, and there is none.
    expect(all(tree, 'run-stage-dot')).toHaveLength(1)
  })

  it('draws the counts under the bar with the tests and the failures beside them', () => {
    const tree = renderRun(factory, model({ stages: [stage({ tests: measured(12), failures: measured(2) })] }), 'light')
    expect(one(tree, 'run-stage-tests').textContent).toBe('12')
    expect(one(tree, 'run-stage-failures').textContent).toBe('2')
    expect(drawn(tree)).toContain('tests ·')
  })

  it('keeps the spend cell\'s own mark and the deadline cell where they were', () => {
    const tree = renderRun(factory, model({ stages: [stage()] }), 'light')
    expect(one(tree, 'run-stage-spend').textContent).toBe(UNAVAILABLE_MARK)
    expect(one(tree, 'run-stage-deadline').textContent).toBe(UNAVAILABLE_MARK)

    const near = renderRun(factory, model({ stages: [stage({ deadline: 'near', deadlineBecause: null })] }), 'light')
    expect(one(near, 'run-stage-deadline').textContent).toBe('near')
  })
})

// ---------------------------------------------------------------------------
// The diagnosis panel's chips.
// ---------------------------------------------------------------------------

describe('renderRun: the diagnosis chips', () => {
  it('draws failed and flaky each as a chip carrying the word and the test\'s name', () => {
    const diagnosis = { ...model().diagnosis, failed: ['TestRollover'], flaky: ['TestClock'] }
    const tree = renderRun(factory, model({ diagnosis }), 'light')
    expect(texts(tree, 'run-failed-word')).toEqual(['failed:'])
    expect(texts(tree, 'run-failed-test')).toEqual(['TestRollover'])
    expect(texts(tree, 'run-flaky-word')).toEqual(['flaky:'])
    expect(texts(tree, 'run-flaky-test')).toEqual(['TestClock'])
    // The word and the name are one chip, so the chip reads as the sentence it
    // is rather than as two cells that happen to sit beside each other.
    expect(one(tree, 'run-failed').childNodes()).toHaveLength(2)
    expect(one(tree, 'run-flaky').childNodes()).toHaveLength(2)
  })

  it('draws no chip for a run that failed nothing', () => {
    const tree = renderRun(factory, model(), 'light')
    expect(all(tree, 'run-failed')).toHaveLength(0)
    expect(all(tree, 'run-flaky')).toHaveLength(0)
  })

  it('draws the agent\'s own message under a caption and a decorative mark', () => {
    const diagnosis = { ...model().diagnosis, agentMessage: 'the suite timed out' }
    const tree = renderRun(factory, model({ diagnosis }), 'light')
    expect(texts(tree, 'run-agent-word')).toEqual(['THE AGENT SAID'])
    expect(one(tree, 'run-agent-text').textContent).toBe('the suite timed out')
    expect(one(tree, 'run-agent-icon').getAttribute('aria-hidden')).toBe('true')
  })
})

// ---------------------------------------------------------------------------
// The ledger, the reconciliation and the two splits.
// ---------------------------------------------------------------------------

/** runFrame is a `diagnose RUN --json` document with one row, for the cases that
 * read what the model derives from a record rather than a hand-built reading. */
function runFrame(cost: Record<string, unknown>): Record<string, unknown> {
  return { row: { run: TRACKED, run_id: TRACKED, repo: TRACKED_REPO, cost } }
}

/** ledgerTree builds the screen from a `runstore.Cost` block. */
function ledgerTree(cost: Record<string, unknown>): ScreenElement {
  return renderRun(factory, buildRunModel({ diagnosis: runFrame(cost), logs: [], logLink: null }), 'light')
}

describe('renderRun: the ledger', () => {
  it('draws the billed total as the hero and never the plan\'s part added to it', () => {
    // The one figure on this screen that must not move: `total_usd` is what the
    // table billed, the model line is that figure's own billed part, and
    // `model_notional_usd` is the plan's list price -- money nobody was charged.
    // A hero of `total + notional` is a bill that does not exist.
    const tree = ledgerTree({
      model_usd: 1.25,
      model_notional_usd: 0.4,
      compute_usd: 0.5,
      compute_estimated: true,
      total_usd: 1.35,
      model_basis: 'api-list',
    })
    expect(one(tree, 'run-ledger-total-value').textContent).toBe('$1.35')
    expect(one(tree, 'run-ledger-model-value').textContent, 'the model line is the billed part of the whole figure').toBe('$0.85')
    expect(one(tree, 'run-ledger-compute-value').textContent).toBe('est. $0.50')
    expect(one(tree, 'run-ledger-notional-value').textContent, 'the plan\'s part is marked notional').toBe('~$0.40')
    const text = drawn(tree)
    expect(text, 'the billed dollars plus the plan\'s part, drawn nowhere').not.toContain('$1.75')
    expect(text, 'the whole model figure, drawn as money nowhere').not.toContain('$1.25')
    // And the plan's part is no segment of the bar: the bar is two shares of
    // the billed total.
    for (const segment of [...all(tree, 'run-ledger-billed'), ...all(tree, 'run-ledger-estimate')]) {
      expect(Number(segment.getAttribute('width')), 'a bar segment is the plan\'s share').not.toBeCloseTo((0.4 / 1.35) * 100, 2)
    }
  })

  it('draws the bar as the model and the compute as shares of the total', () => {
    const ledger = { model: measured(1.5), compute: estimated(0.5), total: measured(2), notional: notional(0.25) }
    const tree = renderRun(factory, model({ ledger }), 'light')
    expect(one(tree, 'run-ledger-bar').getAttribute('aria-hidden'), 'the legend carries the values as text').toBe('true')
    expect(all(tree, 'run-ledger-billed')[0]?.getAttribute('width')).toBe('75')
    expect(all(tree, 'run-ledger-estimate')[0]?.getAttribute('width')).toBe('25')
    expect(all(tree, 'run-ledger-estimate')[0]?.getAttribute('x'), 'the two segments tile the axis').toBe('75')
  })

  it('draws no bar when the compute is not estimated, and the legend still carries the value', () => {
    // The bar is two shares of the total; with one of them unknown there is no
    // second share to draw, and a bar of one segment would read as a run whose
    // compute was free.
    const ledger = { model: measured(1.5), compute: unavailable(LEGACY_DOCUMENT), total: measured(2), notional: unavailable(LEGACY_DOCUMENT) }
    const tree = renderRun(factory, model({ ledger }), 'light')
    expect(all(tree, 'run-ledger-bar')).toHaveLength(0)
    expect(one(tree, 'run-ledger-model-value').textContent).toBe('$1.50')
    expect(one(tree, 'run-ledger-compute-value').textContent).toBe(UNAVAILABLE_MARK)
  })

  it('draws the plan\'s part once: a record whose whole figure is the plan\'s draws it on the model line', () => {
    // A subscription run written after C1.16 carries the part as its whole
    // figure; one written before carries no key at all. Both draw the figure
    // once, under the notional mark, and neither draws a second notional line.
    for (const cost of [
      { model_usd: 3.2, model_notional_usd: 3.2, compute_usd: 0, compute_estimated: true, total_usd: 0, model_basis: 'api-list' },
      { model_usd: 3.2, compute_usd: 0, compute_estimated: true, total_usd: 0, model_basis: 'subscription' },
    ]) {
      const tree = ledgerTree(cost)
      expect(one(tree, 'run-ledger-model-value').textContent).toBe('~$3.20')
      expect(all(tree, 'run-ledger-notional'), 'the same dollars in two lines').toHaveLength(0)
      expect(drawn(tree).split('~$3.20'), 'the plan\'s figure, drawn exactly once').toHaveLength(2)
    }
  })

  it('draws a notional line only when the model line is billed money', () => {
    const tree = ledgerTree({
      model_usd: 3.2,
      model_notional_usd: 2,
      compute_usd: 0.5,
      compute_estimated: true,
      total_usd: 1.7,
      model_basis: 'api-list',
    })
    expect(one(tree, 'run-ledger-model-value').textContent).toBe('$1.20')
    expect(one(tree, 'run-ledger-notional-value').textContent).toBe('~$2.00')
  })

  it('draws the reported/charged pair, and the difference only when both were measured', () => {
    const measured_ = { ...model().diagnosis, reportedUsd: measured(1.2), chargeUsd: measured(1.25) }
    const tree = renderRun(factory, model({ diagnosis: measured_ }), 'light')
    expect(one(tree, 'run-reported-usd').textContent).toBe('$1.20')
    expect(one(tree, 'run-charge-usd').textContent).toBe('$1.25')
    expect(one(tree, 'run-recon-diff-value').textContent, 'the table charged, less what the providers reported').toBe('$0.05')
    expect(one(tree, 'run-recon-diff').classes).toContain('run-recon-diff-warn')

    const absent = { ...model().diagnosis, reportedUsd: unavailable(LEGACY_DOCUMENT), chargeUsd: measured(1.25) }
    const missing = renderRun(factory, model({ diagnosis: absent }), 'light')
    expect(one(missing, 'run-reported-usd').textContent).toBe(UNAVAILABLE_MARK)
    const difference = one(missing, 'run-recon-diff-value')
    expect(difference.textContent).toBe(UNAVAILABLE_MARK)
    expect(difference.getAttribute('title'), 'the reason, not a subtraction from a zero nobody wrote').toContain('no provider reported')
    expect(one(missing, 'run-recon-diff').classes).not.toContain('run-recon-diff-warn')
  })

  it('keeps the difference out of the warn tone when the two accounts agree', () => {
    const diagnosis = { ...model().diagnosis, reportedUsd: measured(1.2), chargeUsd: measured(1.2) }
    const tree = renderRun(factory, model({ diagnosis }), 'light')
    expect(one(tree, 'run-recon-diff-value').textContent).toBe('$0.00')
    expect(one(tree, 'run-recon-diff').classes).not.toContain('run-recon-diff-warn')
  })

  it('draws the two splits as the document wrote them, and never adds them up', () => {
    const diagnosis = {
      ...model().diagnosis,
      byRoute: [{ name: 'anthropic-api', usd: measured(0.9) }],
      byModel: [{ name: 'claude-sonnet-5', usd: measured(1.25) }],
    }
    const tree = renderRun(factory, model({ diagnosis }), 'light')
    expect(texts(tree, 'run-route-name')).toEqual(['anthropic-api'])
    expect(texts(tree, 'run-route-usd')).toEqual(['$0.90'])
    expect(texts(tree, 'run-model-name')).toEqual(['claude-sonnet-5'])
    expect(texts(tree, 'run-model-usd')).toEqual(['$1.25'])
    expect(all(tree, 'run-split')).toHaveLength(2)
    expect(all(tree, 'run-split-absent')).toHaveLength(0)
    // The two splits are different scopes of the same run and no document sums
    // them: a total here would be a figure the console invented.
    expect(drawn(tree), 'the two splits, added up').not.toContain('$2.15')
  })

  it('draws an absent split as the mark with its reason, one line each', () => {
    const tree = renderRun(factory, model(), 'light')
    expect(texts(tree, 'run-route-name')).toEqual([])
    const absent = all(tree, 'run-split-absent')
    expect(absent).toHaveLength(2)
    for (const line of absent) expect(line.textContent).toBe(UNAVAILABLE_MARK)
    expect(absent[0]?.getAttribute('title')).toContain('route_by')
    expect(absent[1]?.getAttribute('title')).toContain('model_by')
  })
})

// ---------------------------------------------------------------------------
// The replay scrubber.
// ---------------------------------------------------------------------------

/** replayTree builds the screen from a list of arrival stamps and a scrub. */
function replayTree(stamps: readonly string[], scrubAt: string | null): ScreenElement {
  const arrivals = stamps.map((receivedAt) => ({ receivedAt, document: { project: 'example-project' }, notices: [] }))
  return renderRun(factory, buildRunModel({ diagnosis: runFrame({}), logs: [], logLink: null, replay: arrivals, scrubAt }), 'light')
}

describe('renderRun: the replay scrubber', () => {
  const STAMPS = ['2026-10-15T12:00:00Z', '2026-10-15T12:04:00Z', '2026-10-15T12:10:00Z']

  it('places one tick per record at its own share of the window', () => {
    const tree = replayTree(STAMPS, null)
    const ticks = all(tree, 'run-replay-tick')
    expect(ticks, 'one tick per record that arrived').toHaveLength(3)
    expect(ticks.map((tick) => tick.getAttribute('x1'))).toEqual(['0', '40', '100'])
    expect(one(tree, 'run-replay-track').getAttribute('aria-hidden')).toBe('true')
    expect(one(tree, 'run-replay-from').textContent).toBe(STAMPS[0])
    expect(one(tree, 'run-replay-to').textContent).toBe(STAMPS[2])
    expect(one(tree, 'run-replay-mid').textContent).toBe('2026-10-15T12:05:00.000Z')
    // With nothing scrubbed the whole window is what is shown, so every tick is
    // in the live tone and there is no playhead to be past.
    expect(all(tree, 'run-replay-tick-past')).toHaveLength(0)
    expect(all(tree, 'run-replay-playhead')).toHaveLength(0)
    expect(all(tree, 'run-replay-fill'), 'no playhead, no fill').toHaveLength(0)
    expect(one(tree, 'run-replay-scrubbed').textContent).toContain('whole window')
  })

  it('draws the playhead at the moment scrubbed to, and the ticks past it in the past tone', () => {
    const tree = replayTree(STAMPS, '2026-10-15T12:05:00Z')
    const playhead = all(tree, 'run-replay-playhead')
    expect(playhead).toHaveLength(1)
    expect(playhead[0]?.getAttribute('x1')).toBe('40')
    expect(all(tree, 'run-replay-fill')[0]?.getAttribute('width'), 'the fill runs from the window start to the playhead').toBe('40')
    expect(all(tree, 'run-replay-tick')).toHaveLength(3)
    expect(all(tree, 'run-replay-tick-past'), 'the ticks after the playhead').toHaveLength(1)
    expect(one(tree, 'run-replay-at').textContent, 'the newest frame at or before the moment').toBe('2026-10-15T12:04:00Z')
  })

  it('draws no positions for a ring with fewer than two distinct stamps, and says so', () => {
    const tree = replayTree(['2026-10-15T12:00:00Z'], null)
    expect(all(tree, 'run-replay-tick')).toHaveLength(0)
    expect(all(tree, 'run-replay-playhead')).toHaveLength(0)
    const note = one(tree, 'run-replay-no-span').textContent ?? ''
    expect(note).toContain('no span')
    // The track itself is still drawn: it is the window's own axis, and a
    // reader still needs to see that there is one.
    expect(all(tree, 'run-replay-track')).toHaveLength(1)
  })
})

// ---------------------------------------------------------------------------
// The two columns.
// ---------------------------------------------------------------------------

describe('renderRun: the two columns', () => {
  it('draws the main column and the aside in the design\'s reading order', () => {
    const tree = renderRun(factory, model(), 'light')
    const layout = one(tree, 'run-layout')
    expect(classesOf(layout)).toEqual(['run-main', 'run-aside'])

    const main = one(tree, 'run-main')
    // The record read's host is the column's first child, and the log window and
    // its replay are the column's other two. **The host is the record's own and
    // not the column**: the mark that read wears is over the panels it draws, and
    // a mark over the whole column would dim and announce the log window with it
    // -- a section that has answered and is not being read.
    expect(classesOf(main)).toEqual(['run-record', 'run-logs', 'run-replay'])
    // The host's own busy slot is its first child. It is empty in a one-shot
    // drawing -- `entry.ts`'s `showRun` draws the record read's mark into it
    // while that read is out -- and an empty slot is not laid out at all
    // (`flight-deck.css`). It is drawn for the reason the Spend sections' slots
    // are: the frame goes up before either read answers, so the mark has
    // somewhere to go.
    const host = one(tree, 'run-record')
    expect(classesOf(host)).toEqual([
      'console-busy-slot',
      'run-stages',
      'run-tolerated-absent',
      'run-diagnosis',
    ])

    const aside = one(tree, 'run-aside')
    expect(classesOf(aside)).toEqual(['run-ledger', 'run-pr'])
  })

  it('draws the warnings after both columns', () => {
    const tree = renderRun(factory, model({ warnings: ['a line the reader should see'] }), 'light')
    const root = tree as FakeScreen
    expect(classesOf(root)).toEqual(['run-header', 'run-layout', 'run-warning'])
  })

  it('names every panel, so each is a region a reader can jump to', () => {
    const tree = renderRun(factory, model(), 'light')
    for (const name of ['run-stages', 'run-diagnosis', 'run-logs', 'run-replay', 'run-ledger', 'run-pr']) {
      const panel = one(tree, name)
      expect(panel.tag, `${name} is a section`).toBe('section')
      expect(panel.getAttribute('aria-label'), `${name} is named`).toBeTruthy()
    }
  })
})

// ---------------------------------------------------------------------------
// The frame the click draws (console 0.118.0).
// ---------------------------------------------------------------------------

describe('renderRunFrame: the frame a run is opened in', () => {
  it('draws the run that was clicked, its way back, and its two sections empty', () => {
    // The owner's report of 2026-11-01: a click on a run id looked like it had
    // not registered, because the screen waited for `diagnose` and `logs` before
    // drawing anything at all. The frame is what the click itself draws -- the
    // id it names is the one the door was asked about, and there is nothing of
    // the record in it yet.
    const frame = renderRunFrame(factory, 'light', TRACKED)
    const root = frame.root as FakeScreen
    expect(root.classes).toContain('run')
    expect(root.getAttribute('data-theme')).toBe('light')
    expect(one(frame.root, 'run-id').tag).toBe('h1')
    expect(one(frame.root, 'run-id').textContent).toBe(TRACKED)
    // The way back is drawn with the frame, and bound in the same turn: a door
    // that only appeared once both reads answered would be a door the operator
    // cannot take out of a screen they are waiting on.
    expect(one(frame.root, 'run-crumb-live').tag).toBe('button')
    expect(one(frame.root, 'run-layout') as FakeScreen).toBeDefined()

    // Neither section carries a figure, and both are named regions.
    for (const [name, section] of [
      ['run-diagnosis', frame.diagnosis],
      ['run-logs', frame.logs],
    ] as const) {
      expect(section.tag, `${name} is a section`).toBe('section')
      expect(section.getAttribute('aria-label'), `${name} is named`).toBeTruthy()
    }
    expect(all(frame.root, 'run-log-line')).toEqual([])
    expect(all(frame.root, 'run-stages')).toEqual([])

    // The frame leaves the column **and the record's host inside it** empty, and
    // hands its two callers the order: `renderRun` appends it to both, and the
    // entry replaces their children with it on every fill. The host holds the
    // record's own panels and the slot the record read's mark goes into, and the
    // log window is the column's other child -- never inside the host, which is
    // the region that mark dims and announces -- while the log section's own
    // slot is where the log read's mark goes.
    expect(frame.main.childNodes()).toEqual([])
    expect(frame.recordHost.childNodes()).toEqual([])
    expect((frame.recordHost as FakeScreen).classes).toEqual(['run-record'])
    const logsSlot = frame.logs.childNodes()
    expect(logsSlot).toHaveLength(1)
    expect((logsSlot[0] as FakeScreen).classes).toEqual(['console-busy-slot'])
    expect((frame.mainSlot as FakeScreen).classes).toEqual(['console-busy-slot'])
    expect(runRecordChildren(frame, { before: [], diagnosis: false, arrivals: [] })).toEqual([frame.mainSlot])
    expect(runMainChildren(frame, { before: [], diagnosis: false, arrivals: [] })).toEqual([
      frame.recordHost,
      frame.logs,
    ])
  })

  it('writes the record’s host and the column as one order, and leaves the diagnosis out until its read answers', () => {
    const frame = renderRunFrame(factory, 'light', TRACKED)
    const record = factory.create('section')
    const arrival = factory.create('section')

    // The two reads answer in an unknown order, and the host is one list rather
    // than two halves: its own busy slot, the record's panels and then the
    // diagnosis -- the three things the record's read owns and the region its
    // mark covers.
    expect(runRecordChildren(frame, { before: [record], diagnosis: true, arrivals: [arrival] })).toEqual([
      frame.mainSlot,
      record,
      frame.diagnosis,
    ])
    // The column is the host, then the log window and what the log read brought
    // with it: the log window is the frame's own and never the record's, which is
    // why the record's mark cannot dim it.
    expect(runMainChildren(frame, { before: [record], diagnosis: true, arrivals: [arrival] })).toEqual([
      frame.recordHost,
      frame.logs,
      arrival,
    ])
    // A run whose diagnosis refused has no diagnosis section in the column at
    // all: the refusal is drawn in the slot instead, and an empty named region
    // beside it would be a panel that says the run reported nothing.
    expect(runRecordChildren(frame, { before: [], diagnosis: false, arrivals: [] })).toEqual([frame.mainSlot])
    expect(runMainChildren(frame, { before: [], diagnosis: false, arrivals: [] })).toEqual([
      frame.recordHost,
      frame.logs,
    ])
  })

  it('draws a hostile run id as the mark, in the heading and in the busy label', () => {
    // The id comes from a row the console drew, but it is not this screen's own
    // string: it is put through the `runId` class, and one the class refuses is
    // drawn as the console's `n/a` with the reason in the `title` -- never as
    // markup, a control character or a bidi override.
    const hostile = [
      '<img src=x onerror=alert(1)>',
      'run\u0007id',
      'run‮id',
      'run⁦id⁩',
    ]
    for (const run of hostile) {
      const frame = renderRunFrame(factory, 'light', run)
      const heading = one(frame.root, 'run-id')
      expect(heading.textContent, run).toBe(UNAVAILABLE_MARK)
      expect(heading.getAttribute('title'), run).toBeTruthy()
      // The busy label is a sentence with the id in it, so it is the second
      // place a hostile id would reach the screen.
      expect(runBusyLabel(run, RUN_RECORD_BUSY), run).toBe(`Reading ${RUN_RECORD_BUSY} of ${UNAVAILABLE_MARK}`)
      expect(runBusyLabel(run, RUN_LOGS_BUSY), run).toBe(`Reading ${RUN_LOGS_BUSY} of ${UNAVAILABLE_MARK}`)
    }
    // A run id the class admits is drawn as itself, so the case above is about
    // the refusal and not about the label being constant.
    expect(runBusyLabel(TRACKED, RUN_RECORD_BUSY)).toBe(`Reading ${RUN_RECORD_BUSY} of ${TRACKED}`)
  })
})

// ---------------------------------------------------------------------------
// The attribute rule.
// ---------------------------------------------------------------------------

describe('renderRun: what the screen is allowed to set', () => {
  it('writes every cell as text and sets no attribute that could act', () => {
    // The screen is handed a fake whose navigating attribute, resource
    // attributes, `style` attribute and every `on*` handler throw, so a screen
    // that reached for one fails here. What is left is the set below: the class
    // hooks and `data-` readings a stylesheet reads, the accessibility
    // attributes (which cannot make the window do anything), `type` on a
    // `button` (which is what `writes.ts`'s `disabledWrite` draws, and it can
    // only *stop* a form submission), and -- **on a node the SVG namespace made,
    // and only there** -- the presentation attributes a shape's geometry is
    // built from. The namespace is what the fake records, not the tag: a `rect`
    // made with `create` is an HTML element and is not held to this set. The
    // geometry, the stroke, the fill and the joins are the one thing a
    // stylesheet cannot carry, and they are all `flight-deck.css`'s.
    const MAXIMAL = new Set(['svg', 'rect', 'path', 'line'])
    const SVG_ATTRIBUTES = new Set([
      'viewBox',
      'preserveAspectRatio',
      'width',
      'height',
      'x',
      'y',
      'x1',
      'x2',
      'y1',
      'y2',
      'rx',
      'd',
    ])
    const ACCESSIBILITY = new Set(['role', 'aria-label', 'aria-hidden', 'aria-disabled'])
    const tree = renderRun(
      factory,
      model({
        stages: [stage({ passed: false })],
        diagnosis: { ...model().diagnosis, agentMessage: 'timed out' },
        // The ledger's bar and the scrubber's track are the screen's two other
        // SVG drawings; without them the widening below would be a licence for
        // attributes nothing on this tree sets.
        ledger: { model: measured(1), compute: estimated(0.5), total: measured(1.5), notional: notional(0.25) },
        replay: {
          records: measured(2),
          windowSeconds: measured(900),
          dropped: measured(0),
          from: '2026-10-15T12:00:00Z',
          to: '2026-10-15T12:10:00Z',
          stamps: ['2026-10-15T12:00:00Z', '2026-10-15T12:10:00Z'],
          scrubbed: null,
          scrubbedBecause: null,
        },
      }),
      'light',
    )
    // The panel draws at least one SVG node, or the widening above would be a
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

  it('draws no control in the ledger, the pull request or the replay panel', () => {
    // The three panels 3b draws are read-only: the pull request is text with no
    // link (`link` is typed `null`), the ledger has no Copy button (the console
    // has no clipboard without a `src/main` change), and the scrubber's
    // previous/next controls are not drawn because no ring reaches this screen.
    // A `button` here would be a control that does nothing.
    const tree = renderRun(
      factory,
      model({
        replay: {
          records: measured(3),
          windowSeconds: measured(900),
          dropped: measured(0),
          from: '2026-10-15T12:00:00Z',
          to: '2026-10-15T12:10:00Z',
          stamps: ['2026-10-15T12:00:00Z', '2026-10-15T12:04:00Z', '2026-10-15T12:10:00Z'],
          scrubbed: null,
          scrubbedBecause: null,
        },
      }),
      'light',
    )
    for (const panel of ['run-ledger', 'run-pr', 'run-replay', 'run-diagnosis']) {
      for (const node of walk(one(tree, panel))) {
        expect(node.tag, `${panel} draws a control`).not.toBe('button')
      }
    }
  })

  it('carries the theme on the root and nothing else', () => {
    const root = renderRun(factory, model(), 'dark') as FakeScreen
    expect(root.getAttribute('data-theme')).toBe('dark')
  })
})
