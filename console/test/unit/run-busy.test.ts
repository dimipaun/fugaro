// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Run screen's two busy marks, and the checkout write that outlives its tab
// (console 0.118.0 "Plum": the follow-up to #1520 and the review of #1530).
//
// Three claims, and every one of them is about a view nobody is looking at:
//
//   1. **The record read's mark is over its own panels and not the whole
//      column.** `section.run-logs` is not inside the element the record read's
//      `data-busy`/`aria-busy` goes on, so a log window that landed first is
//      neither dimmed nor announced busy while `run.diagnose` is still out.
//   2. **Leaving a run ends both of its reads.** The fifteen-second line a slow
//      read arms is disarmed, so nothing is written into a tree the panel no
//      longer holds -- and a run opened afterwards draws and fills as it always
//      did.
//   3. **A checkout chosen during a tab switch draws nothing.** The write and the
//      pairing probe behind it take seconds; an answer that lands after the
//      operator moved on draws neither the plan nor a refusal, and does not
//      invalidate the tab they chose.
//
// The harness is this file's own, as every suite's here is (`entry-wiring.test.ts`
// states the same rule): the fake bridge records every call, so "no read was
// made" and "the write landed" are claims these cases can make.

import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { loadFixture } from '../../src/source/fixture'
import { bootDashboard, VIEW_NAV_CLASS, VIEW_PANEL_CLASS } from '../../src/renderer/entry'
import { NO_PLAN_CHECKOUT } from '../../src/source/source'
import { CHECKOUT_SELECT_CLASS } from '../../src/renderer/checkout'
import { NO_CHECKOUT_USE_LABEL } from '../../src/renderer/screens/nocheckout'
import { BUSY_ATTRIBUTE, BUSY_BUSY_ATTRIBUTE, BUSY_STILL_CLASS } from '../../src/renderer/section'
import { READING_STILL_MS } from '../../src/renderer/screens/reading'
import { anOkCompatibility, everyNode, FakeDocument, FakeNode, textOf } from '../support/fakeDom'

/** CONSOLE_ROOT is the package root, which is where the fixture and the plan
 * document are read from. */
const CONSOLE_ROOT = join(__dirname, '..', '..')

/** CONSOLE_READ_METHODS and RAIL_METHODS are the bridge methods the fake below
 * answers: the ten reads `entry.ts` may reach for, and the rail's six
 * operations. Every one records its call. */
const CONSOLE_READ_METHODS = [
  'subscribeLive',
  'showBudget',
  'runReport',
  'listRuns',
  'statsRuns',
  'coordinatorStatus',
  'diagnoseRun',
  'readRunLogs',
  'runLogLink',
  'planStatus',
] as const

const RAIL_METHODS = [
  'listInstallations',
  'chooseInstallation',
  'setInstallationCheckout',
  'removeInstallation',
  'revealInstallation',
  'openInstallations',
] as const

type AReply = (args: unknown) => unknown

interface FakeBridge {
  readonly bridge: Record<string, unknown>
  readonly calls: readonly { readonly name: string; readonly args: unknown }[]
}

/** fakeBridge is the preload seam, recorded: the handshake, the installation,
 * the ten reads and the rail's six operations. A method the case gave no reply
 * for refuses with the console's own words for a read it did not ask for, which
 * is also what makes "this read was never made" visible in `calls`. */
function fakeBridge(
  options: {
    readonly rows?: readonly Readonly<Record<string, unknown>>[]
    readonly replies?: Readonly<Record<string, AReply>>
  } = {},
): FakeBridge {
  const calls: { name: string; args: unknown }[] = []
  const bridge: Record<string, unknown> = {
    getCompatibility: (): unknown => ({ ok: true, data: anOkCompatibility() }),
    getInstallation: (): unknown => ({
      ok: true,
      data: {
        chosen: true,
        id: '0123456789abcdef',
        project: 'acme',
        binary: '/opt/fugaro/bin/fugaro',
        cliVersion: '1.2.3',
        mode: 'ok',
      },
    }),
  }
  for (const name of [...CONSOLE_READ_METHODS, ...RAIL_METHODS]) {
    bridge[name] = (args: unknown): unknown => {
      calls.push({ name, args })
      const reply = options.replies?.[name]
      if (reply !== undefined) return reply(args)
      if (name === 'listInstallations') return { ok: true, data: { rows: options.rows ?? [] } }
      return { ok: false, reason: `the console did not read ${name} in this case` }
    }
  }
  return { bridge, calls }
}

/** syntheticDocuments is the committed recording's documents, verified by the
 * console's own loader -- the same frames the stub CLI prints for the e2e. */
function syntheticDocuments(): readonly Readonly<Record<string, unknown>>[] {
  const text = readFileSync(join(CONSOLE_ROOT, 'test', 'fixtures', 'synthetic.ndjson'), 'utf8')
  return loadFixture(text, 'test/fixtures/synthetic.ndjson').records
    .filter((record) => 'document' in record)
    .map((record) => ('document' in record ? record.document : {}))
}

/** LIVE_FRAME is the last of them, which is the frame that carries two run rows
 * -- the two this suite opens, one after the other. */
const LIVE_FRAME = syntheticDocuments()[syntheticDocuments().length - 1] as Readonly<Record<string, unknown>>

/** snapshotReply is one `live.watch` answer: the envelope `CliBackedHandlers`
 * builds, with the document inside it. */
function snapshotReply(document: Readonly<Record<string, unknown>>): Record<string, unknown> {
  return {
    ok: true,
    data: { document, receivedAt: '2026-10-15T12:00:00Z', sequence: 1, day: '2026-10-15', rolled: false, notices: [] },
  }
}

/** boot runs the entry against a fake document and answers it, once the first
 * Live read has been drawn. */
async function boot(bridge: Record<string, unknown>): Promise<FakeDocument> {
  const document = new FakeDocument()
  await bootDashboard({ fugaro: bridge, document: document as unknown as Document })
  return document
}

/** panelOf is the panel the screens are drawn into. */
function panelOf(document: FakeDocument): FakeNode {
  const panel = everyNode(document.body).find((node) => node.attributes.get('class') === VIEW_PANEL_CLASS)
  if (panel === undefined) throw new Error('the entry drew no panel')
  return panel
}

/** buttonFor is one navigation button. */
function buttonFor(document: FakeDocument, view: string): FakeNode {
  const nav = everyNode(document.body).find((node) => node.attributes.get('class') === VIEW_NAV_CLASS)
  const button = nav?.children.find((node) => node.attributes.get('data-view') === view)
  if (button === undefined) throw new Error(`the navigation offers no ${view} view`)
  return button
}

/** classed finds the drawn elements carrying one class. */
function classed(root: FakeNode, className: string): readonly FakeNode[] {
  return everyNode(root).filter((node) => (node.attributes.get('class') ?? '').split(/\s+/).includes(className))
}

/** flush lets the entry's own microtasks run: every read is answered through a
 * promise, and a drawing lands a turn after the call that asked for it. */
async function flush(): Promise<void> {
  for (let i = 0; i < 8; i += 1) await Promise.resolve()
}

/** holding is a bridge answer the case releases by hand. A promise that is
 * already resolved cannot be observed half-way through, and the state these
 * cases are about -- the read is on its way, or the write has not been answered
 * -- only exists while one is outstanding. A released holder answers every later
 * call with the same value. */
function holding(): { readonly reply: () => unknown; readonly release: (value: unknown) => void } {
  let answered: { readonly value: unknown } | null = null
  const waiters: ((value: unknown) => void)[] = []
  return {
    reply: () =>
      answered === null
        ? new Promise((resolve) => {
            waiters.push(resolve)
          })
        : answered.value,
    release: (value: unknown) => {
      answered = { value }
      for (const waiter of waiters.splice(0)) waiter(value)
    },
  }
}

/** busyAt is `node` and every ancestor carrying one of the two busy attributes:
 * exactly the elements that would dim it and announce it busy. */
function busyAt(node: FakeNode): readonly FakeNode[] {
  const found: FakeNode[] = []
  for (let each: FakeNode | null = node; each !== null; each = each.parent) {
    if (each.attributes.has(BUSY_ATTRIBUTE) || each.attributes.has(BUSY_BUSY_ATTRIBUTE)) found.push(each)
  }
  return found
}

/** readingOf is the panel's placeholder sentence, or `null` when the panel holds
 * something else: the one observable that says a tab is waiting rather than
 * showing an answer. */
function readingOf(document: FakeDocument): string | null {
  const panel = panelOf(document)
  const kind = (panel.children[0] as FakeNode | undefined)?.attributes.get('data-kind')
  return kind === 'reading' ? textOf(panel) : null
}

/** listRow is one `installation.list` row as the main process answers it. */
function listRow(
  project: string,
  options: { readonly checkoutChoices?: readonly Readonly<Record<string, unknown>>[] } = {},
): Record<string, unknown> {
  return {
    project,
    open: true,
    active: true,
    saved: true,
    checkouts: [],
    checkoutChoices: options.checkoutChoices ?? [],
    state: { issue: 'none', readable: true, sentence: '', command: '', details: [] },
  }
}

// ---------------------------------------------------------------------------
// The two busy marks of one run.
// ---------------------------------------------------------------------------

describe('the Run screen’s two section reads', () => {
  /** openRun boots the committed frame, clicks a run row, and holds both of that
   * run's reads: the state every case below is about is one read landing while
   * the other is still out. */
  async function openRun(): Promise<{
    readonly document: FakeDocument
    readonly panel: FakeNode
    readonly diagnosis: ReturnType<typeof holding>
    readonly logs: ReturnType<typeof holding>
  }> {
    const diagnosis = holding()
    const logs = holding()
    const { bridge } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(LIVE_FRAME),
        diagnoseRun: () => diagnosis.reply(),
        readRunLogs: () => logs.reply(),
        runLogLink: () => ({ ok: false, reason: 'this run has no transcript link' }),
      },
    })
    const document = await boot(bridge)
    const panel = panelOf(document)
    ;(classed(panel, 'live-run')[0] as FakeNode).click()
    await flush()
    return { document, panel, diagnosis, logs }
  }

  it('marks the record’s own host, and never the log window that landed first', async () => {
    const { panel, diagnosis, logs } = await openRun()
    // The log window answers first, and the record's read is still out.
    logs.release({
      ok: true,
      data: {
        document: {
          entries: [{ time: '2026-10-15T12:00:00Z', severity: 'info', stage: 'implement', message: 'the run started' }],
          total: 1,
          refused: 0,
        },
      },
    })
    await flush()
    const logsSection = classed(panel, 'run-logs')[0] as FakeNode
    expect(classed(logsSection, 'run-log-line'), 'the log window did not land: the case proves nothing').toHaveLength(1)
    // The log read answered, so nothing about it is being read any more -- and
    // the record's mark, which is still up, must not reach it: the host is the
    // panel the record draws into, and the log window is not in it.
    expect(busyAt(logsSection), 'the record’s busy mark reached the log window').toEqual([])

    // The record's own host does carry it, which is what the case above is the
    // absence of.
    const host = classed(panel, 'run-record')[0] as FakeNode
    expect(host, 'the frame drew no host for the record’s own panels').toBeDefined()
    expect(host.attributes.get(BUSY_ATTRIBUTE)).toBe('true')
    expect(host.attributes.get(BUSY_BUSY_ATTRIBUTE)).toBe('true')

    // And the mark comes down with the read it belongs to.
    diagnosis.release({ ok: true, data: { document: {} } })
    await flush()
    expect(classed(panel, 'run-record')[0]?.attributes.get(BUSY_ATTRIBUTE)).toBeUndefined()
    expect(classed(panel, 'run-record')[0]?.attributes.get(BUSY_BUSY_ATTRIBUTE)).toBeUndefined()
  })

  it('keeps the log window’s own mark when the record landed first', async () => {
    const { panel, diagnosis, logs } = await openRun()
    diagnosis.release({ ok: true, data: { document: {} } })
    await flush()
    const logsSection = classed(panel, 'run-logs')[0] as FakeNode
    expect(logsSection.attributes.get(BUSY_ATTRIBUTE), 'the log read lost its own host').toBe('true')
    expect(logsSection.attributes.get(BUSY_BUSY_ATTRIBUTE)).toBe('true')
    // The record answered, so its host is not busy -- and the log section's mark
    // is the section's own, not something above it.
    expect(classed(panel, 'run-record')[0]?.attributes.get(BUSY_ATTRIBUTE)).toBeUndefined()
    expect(busyAt(classed(panel, 'run-diagnosis')[0] as FakeNode)).toEqual([])
    logs.release({ ok: true, data: { document: { entries: [], total: 0, refused: 0 } } })
    await flush()
    expect(logsSection.attributes.get(BUSY_ATTRIBUTE)).toBeUndefined()
  })
})

// ---------------------------------------------------------------------------
// Leaving a run.
// ---------------------------------------------------------------------------

describe('leaving a run', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('ends both reads, disarms their line, and draws a run opened afterwards', async () => {
    const diagnosis = holding()
    const logs = holding()
    const { bridge } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(LIVE_FRAME),
        diagnoseRun: () => diagnosis.reply(),
        readRunLogs: () => logs.reply(),
        runLogLink: () => ({ ok: false, reason: 'this run has no transcript link' }),
      },
    })
    const document = await boot(bridge)
    const panel = panelOf(document)
    ;(classed(panel, 'live-run')[0] as FakeNode).click()
    await flush()
    const run = panel.children[0] as FakeNode
    const host = classed(run, 'run-record')[0] as FakeNode
    const logsSection = classed(run, 'run-logs')[0] as FakeNode
    expect(host.attributes.get(BUSY_ATTRIBUTE), 'the run was not reading at all').toBe('true')

    // The operator moves to another view while both reads are out.
    buttonFor(document, 'live').click()
    await flush()
    expect(run.parent, 'the run was still on the panel: the case proves nothing').toBeNull()

    await vi.advanceTimersByTimeAsync(READING_STILL_MS + 1000)
    // Neither mark wrote its fifteen-second line into the tree the panel no
    // longer holds.
    expect(classed(run, BUSY_STILL_CLASS), 'a line was written into a detached run').toEqual([])
    // And both reads ended: neither host keeps the state the mark put up.
    expect(host.attributes.get(BUSY_ATTRIBUTE)).toBeUndefined()
    expect(host.attributes.get(BUSY_BUSY_ATTRIBUTE)).toBeUndefined()
    expect(logsSection.attributes.get(BUSY_ATTRIBUTE)).toBeUndefined()
    expect(logsSection.attributes.get(BUSY_BUSY_ATTRIBUTE)).toBeUndefined()

    // A run opened afterwards draws and fills as it always did: the door ends
    // the reads of the run that was left and nothing else.
    ;(classed(panel, 'live-run')[0] as FakeNode).click()
    await flush()
    const again = panel.children[0] as FakeNode
    expect(classed(again, 'run-record')[0]?.attributes.get(BUSY_ATTRIBUTE)).toBe('true')
    expect(classed(again, 'run-logs')[0]?.attributes.get(BUSY_ATTRIBUTE)).toBe('true')
    diagnosis.release({ ok: true, data: { document: {} } })
    logs.release({ ok: true, data: { document: { entries: [], total: 0, refused: 0 } } })
    await flush()
    expect(classed(again, 'run-stages'), 'the record read of the second run was dropped').toHaveLength(1)
    expect(classed(again, 'console-busy'), 'a mark outlived the reads').toEqual([])
  })
})

// ---------------------------------------------------------------------------
// A checkout chosen during a tab switch.
// ---------------------------------------------------------------------------

/**
 * FakeWorker is the layout protocol without a browser. The graph screen builds a
 * worker the moment a plan is drawn, and this suite stubs one in so the case
 * that must *not* draw a Graph would draw one if the console did.
 */
class FakeWorker {
  private readonly listeners = new Map<string, ((event: { readonly data?: unknown }) => void)[]>()

  postMessage(message: unknown): void {
    const request = message as Record<string, unknown>
    if (request['cmd'] !== 'layout') return
    const graph = request['graph'] as {
      readonly children: readonly { readonly id: string; readonly width: number; readonly height: number }[]
      readonly edges: readonly { readonly id: string }[]
    }
    const data = {
      children: graph.children.map((child, index) => ({
        id: child.id,
        x: index * 300,
        y: 0,
        width: child.width,
        height: child.height,
      })),
      edges: graph.edges.map((edge) => ({ id: edge.id, sections: [] })),
    }
    for (const listener of this.listeners.get('message') ?? []) {
      listener({ data: { id: request['id'], data } })
    }
  }

  addEventListener(type: string, listener: (event: { readonly data?: unknown }) => void): void {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), listener])
  }

  terminate(): void {}
}

/** PLAN_STATUS is the graph's own document, so the case that must not draw a
 * Graph has a plan it could have drawn. */
const PLAN_STATUS: unknown = JSON.parse(readFileSync(join(CONSOLE_ROOT, 'test', 'fixtures', 'plan-status.json'), 'utf8'))

/** planStatusReply is one `plan.status` answer with that document inside it. */
function planStatusReply(): Record<string, unknown> {
  return {
    ok: true,
    data: { document: PLAN_STATUS, receivedAt: '2026-10-15T12:00:00Z', sequence: 1, day: '2026-10-15', rolled: false, notices: [] },
  }
}

/** reportDocument is a `report --json` document, which is what tells the case
 * that the Spend tab's own read landed. */
function reportDocument(): Readonly<Record<string, unknown>> {
  return {
    project: 'acme',
    source: 'firestore',
    degraded: false,
    by: 'day',
    since: '2026-10-30',
    until: '2026-10-31',
    weeks: 'iso-utc-monday',
    rows: [],
  }
}

describe('a checkout chosen during a tab switch', () => {
  const CHOICE = { repository: 'github/owner/acme', path: '/src/acme', more: 0 }

  beforeEach(() => {
    vi.stubGlobal('Worker', FakeWorker)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  /** switchDuringProbe runs the whole race and answers what the panel holds
   * afterwards: the Graph card chooses a checkout (by itself, or when the case
   * presses its button), the operator moves to Spend while the write and the
   * probe behind it are still out, and the write then answers. `answer` is what
   * it answers with, so the same race can be read for the write that was
   * accepted and for the one that was refused. */
  async function switchDuringProbe(
    door: 'auto' | 'choose',
    answer: Readonly<Record<string, unknown>>,
  ): Promise<{
    readonly document: FakeDocument
    readonly panel: FakeNode
    readonly calls: readonly { readonly name: string; readonly args: unknown }[]
    readonly releaseReport: (value: unknown) => void
  }> {
    const write = holding()
    const report = holding()
    let recorded = false
    const { bridge, calls } = fakeBridge({
      rows: [
        listRow('acme', { checkoutChoices: door === 'auto' ? [CHOICE] : [CHOICE, { ...CHOICE, path: '/src/scratch' }] }),
      ],
      replies: {
        subscribeLive: () => snapshotReply(LIVE_FRAME),
        showBudget: () => ({ ok: true, data: { document: {} } }),
        runReport: () => report.reply(),
        setInstallationCheckout: () => {
          recorded = true
          return write.reply()
        },
        // The plan refuses for want of a checkout until one is written, which is
        // what draws the card the race starts from.
        planStatus: () => (recorded ? planStatusReply() : { ok: false, reason: NO_PLAN_CHECKOUT }),
      },
    })
    const document = await boot(bridge)
    const panel = panelOf(document)
    buttonFor(document, 'graph').click()
    await flush()
    if (door === 'choose') {
      // The card's menu, with the entry the operator picked: the directory the
      // write carries is the option's own value, which a fake node derives from
      // nothing, so the case writes it.
      const menu = classed(panel, CHECKOUT_SELECT_CLASS)[0] as unknown as { value: string }
      menu.value = CHOICE.path
      const button = everyNode(panel).find(
        (node) => node.tagName === 'BUTTON' && node.textContent === NO_CHECKOUT_USE_LABEL,
      )
      if (button === undefined) throw new Error('the card drew no checkout button')
      button.click()
      await flush()
    }
    const wrote = calls.filter((call) => call.name === 'setInstallationCheckout')
    expect(wrote, `the ${door} door never wrote a checkout: the case proves nothing`).toHaveLength(1)

    // The operator leaves the Graph while the write is out.
    buttonFor(document, 'spend').click()
    await flush()
    expect(classed(panel, 'spend'), 'the Spend tab did not come up: the case proves nothing').toHaveLength(1)

    write.release(answer)
    await flush()
    return { document, panel, calls, releaseReport: (value) => report.release(value) }
  }

  const ACCEPTED = { ok: true, data: null }
  const REFUSED = { ok: false, reason: 'the directory is not one this installation records' }

  it.each([
    { door: 'auto' as const, answer: ACCEPTED, what: 'the write was accepted' },
    { door: 'auto' as const, answer: REFUSED, what: 'the write was refused' },
    { door: 'choose' as const, answer: ACCEPTED, what: 'the write was accepted' },
    { door: 'choose' as const, answer: REFUSED, what: 'the write was refused' },
  ])('draws nothing when $what and the operator left the Graph', async ({ door, answer }) => {
    const { panel, releaseReport } = await switchDuringProbe(door, answer)
    // The tab the operator chose is still the one on screen: neither the plan
    // read that follows the write nor the refusal it may draw belongs to it.
    expect(classed(panel, 'spend'), 'the checkout answer replaced the tab the operator chose').toHaveLength(1)
    expect(classed(panel, 'graph-canvas'), 'the Graph was drawn over another tab').toEqual([])
    expect(classed(panel, 'console-unavailable'), 'a refusal was drawn over another tab').toEqual([])

    // And the Spend tab's own read was not invalidated by an answer for a view
    // nobody is on: it lands and fills its section.
    releaseReport({ ok: true, data: { document: reportDocument() } })
    await flush()
    expect(classed(panel, 'spend-since'), 'the chosen tab’s own read was dropped').toHaveLength(1)
  })
})
