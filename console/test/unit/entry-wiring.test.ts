// The dashboard's wiring (desktop-console plan Task 3.4c, step 2).
//
// `bundle.test.ts` runs the built bundle and proves the entry reaches the
// bridge; `test/e2e/entry.spec.ts` drives the real window. This suite is the
// middle one, and it is the only place the *wiring* is driven directly: the
// gate's verdict decides what is offered, every read goes through the bridge the
// way the preload would answer it, and the poll is a clock the test owns.
//
// Six claims are held here, and each of them is a claim about a decision the
// entry makes rather than about a drawing a screen makes:
//
//   1. **A refused handshake mounts no screen and makes no read at all.** D7's
//      refusal is the whole document; the entry returns before the header, the
//      navigation and every read.
//   2. **A withheld view is not offered, and its read is never made.** In a
//      reduced mode the navigation lists Live and nothing else, and `runReport`
//      and `showBudget` are not called -- not on a click, not on a poll.
//   3. **A reply the console cannot read draws the unavailable state.** An
//      undecodable `live.watch` document is a refusal screen with the schema's
//      own sentence in it, and no figure anywhere -- and so is a read that
//      rejected instead of answering, which is the other way a read fails.
//   4. **The Live screen is the frame, and a run row is a door.** The committed
//      synthetic recording's repository and runs are drawn, and clicking a run
//      row asks for that run's diagnosis and logs.
//   5. **At most one `live.watch` is in flight, and polling stops when nobody is
//      looking.** The interval is not a second read while one is outstanding,
//      and a hidden window, a view other than Live, or an open run stops it.
//   6. **A read that lands after the operator moved on is not drawn.** The panel
//      is the view they chose: a spend report or a live frame that answered late
//      does not replace it.
//
// And one grep: **the fixture source is not reachable from the shipped code.**
// `src/source/fixture.ts` is the unit tests' source, reached only through the
// `sourceFor` they inject; nothing under `src/` imports it, however the import
// is spelled.

import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join } from 'node:path'

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { loadFixture } from '../../src/source/fixture'
import {
  LIVE_POLL_MS,
  READ_UNREADABLE,
  VIEW_BUTTON_CLASS,
  VIEW_NAV_CLASS,
  VIEW_PANEL_CLASS,
  bootDashboard,
} from '../../src/renderer/entry'
import { decodeSnapshot } from '../../src/shared/schema'
import {
  FakeDocument,
  FakeNode,
  aReducedCompatibility,
  aRefusedCompatibility,
  anOkCompatibility,
  everyNode,
  textOf,
} from '../support/fakeDom'

/** CONSOLE_ROOT is the package root, which is where the sources, the fixtures
 * and the built tree are read from. */
const CONSOLE_ROOT = join(__dirname, '..', '..')

/** READ_METHODS is the seven reads `entry.ts` may reach for, by the name the
 * preload exposes. Every one of them is on the fake bridge below, and every one
 * records its call -- so "this read was never made" is a claim this suite can
 * make about any of them. */
const READ_METHODS = [
  'subscribeLive',
  'showBudget',
  'runReport',
  'listRuns',
  'diagnoseRun',
  'readRunLogs',
  'runLogLink',
] as const

/** A_REPLY is a bridge method's answer. */
type AReply = (args: unknown) => unknown

/** FakeBridge is the preload seam, recorded: the handshake, the installation,
 * and the seven reads, each answering what the case told it to and appending its
 * own call to `calls`. */
interface FakeBridge {
  readonly bridge: Record<string, unknown>
  readonly calls: readonly { readonly name: string; readonly args: unknown }[]
}

function fakeBridge(options: {
  readonly compatibility?: Record<string, unknown>
  readonly installation?: unknown
  readonly replies?: Readonly<Record<string, AReply>>
}): FakeBridge {
  const calls: { name: string; args: unknown }[] = []
  const bridge: Record<string, unknown> = {
    getCompatibility: (): unknown => ({ ok: true, data: options.compatibility ?? anOkCompatibility() }),
    getInstallation: (): unknown =>
      options.installation ?? {
        ok: true,
        data: { chosen: true, id: '0123456789abcdef', project: 'acme', binary: '/opt/fugaro/bin/fugaro', cliVersion: '1.2.3', mode: 'ok' },
      },
  }
  for (const name of READ_METHODS) {
    bridge[name] = (args: unknown): unknown => {
      calls.push({ name, args })
      const reply = options.replies?.[name]
      return reply === undefined ? { ok: false, reason: `the console did not read ${name} in this case` } : reply(args)
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

/** snapshotReply is one `live.watch` answer: the envelope `CliBackedHandlers`
 * builds (`src/source/source.ts`), with the document inside it. */
function snapshotReply(
  document: Readonly<Record<string, unknown>>,
  receivedAt = '2026-10-15T12:00:00Z',
): Record<string, unknown> {
  return {
    ok: true,
    data: { document, receivedAt, sequence: 1, day: '2026-10-15', rolled: false, notices: [] },
  }
}

/** boot runs the entry against a fake document and answers it, once the first
 * Live read has been drawn. */
async function boot(bridge: Record<string, unknown>, document: FakeDocument): Promise<FakeDocument> {
  await bootDashboard({ fugaro: bridge, document: document as unknown as Document })
  return document
}

/** panelOf is the panel the screens are drawn into. */
function panelOf(document: FakeDocument): FakeNode {
  const panel = everyNode(document.body).find(
    (node) => node.attributes.get('class') === VIEW_PANEL_CLASS,
  )
  if (panel === undefined) throw new Error('the entry drew no panel')
  return panel
}

/** navButtons is the navigation's buttons, by the view each offers. */
function navButtons(document: FakeDocument): readonly FakeNode[] {
  const nav = everyNode(document.body).find((node) => node.attributes.get('class') === VIEW_NAV_CLASS)
  if (nav === undefined) throw new Error('the entry drew no navigation')
  return nav.children
}

/** buttonFor is one navigation button. */
function buttonFor(document: FakeDocument, view: string): FakeNode {
  const button = navButtons(document).find((node) => node.attributes.get('data-view') === view)
  if (button === undefined) throw new Error(`the navigation offers no ${view} view`)
  return button
}

/** classed finds the drawn elements carrying one class. */
function classed(root: FakeNode, className: string): readonly FakeNode[] {
  return everyNode(root).filter((node) =>
    (node.attributes.get('class') ?? '').split(/\s+/).includes(className),
  )
}

/** flush lets the entry's own microtasks run: every read is answered through a
 * promise, and a drawing lands a turn after the call that asked for it. */
async function flush(): Promise<void> {
  for (let i = 0; i < 8; i += 1) await Promise.resolve()
}

describe('the dashboard the entry mounts', () => {
  it('mounts no screen and makes no read at all when the handshake refused', async () => {
    const { bridge, calls } = fakeBridge({ compatibility: aRefusedCompatibility() })
    const document = await boot(bridge, new FakeDocument())
    // The non-vacuity guard: the entry *ran*, and what it drew is the refusal
    // and nothing else. A bundle that threw on load draws nothing too, and this
    // is what tells the two apart.
    expect(document.body.children.map((child) => child.attributes.get('data-mode'))).toEqual([
      'refused',
    ])
    // Not one read: not the header's, not the Live screen's, not the
    // navigation's -- there is no navigation to make one from.
    expect(calls).toEqual([])
    expect(
      everyNode(document.body).some((node) => node.attributes.get('class') === VIEW_NAV_CLASS),
    ).toBe(false)
  })

  it('offers only the views the gate would draw, and never asks for a withheld one', async () => {
    const { bridge, calls } = fakeBridge({ compatibility: aReducedCompatibility() })
    const document = await boot(bridge, new FakeDocument())
    // The banner is drawn, and the navigation offers Live alone: `spend` is
    // withheld and `projects` is the `caps` view, which a reduced mode withholds
    // with it. A Spend entry here would be a door to a screen that cannot read.
    expect(document.body.children[0]?.attributes.get('data-mode')).toBe('reduced')
    expect(navButtons(document).map((button) => button.attributes.get('data-view'))).toEqual(['live'])
    expect(navButtons(document).map((button) => button.attributes.get('class'))).toEqual([
      VIEW_BUTTON_CLASS,
    ])
    // And the two reads behind the withheld views are not made -- not on the
    // first drawing, and not on a poll.
    expect(calls.map((call) => call.name)).toEqual(['subscribeLive'])
    await flush()
    expect(calls.map((call) => call.name)).toEqual(['subscribeLive'])
    expect(calls.some((call) => call.name === 'runReport')).toBe(false)
    expect(calls.some((call) => call.name === 'showBudget')).toBe(false)
  })

  it('draws the unavailable state, and no figure, for a live.watch reply it cannot decode', async () => {
    // A document the schema refuses: it carries a key this console does not
    // read. The console's answer is the refusal screen with the schema's own
    // sentence -- never a dashboard of zeros, which is what a screen handed an
    // unread document would draw.
    const unknown = { ...(syntheticDocuments()[0] as Record<string, unknown>), mystery: 1 }
    const decoded = decodeSnapshot(unknown, 'live.watch')
    expect(decoded.ok, 'the planted document must be one the schema refuses').toBe(false)
    const because = decoded.ok ? '' : decoded.skew.because

    const { bridge } = fakeBridge({
      replies: { subscribeLive: () => snapshotReply(unknown) },
    })
    const document = await boot(bridge, new FakeDocument())
    const panel = panelOf(document)
    const drawn = panel.children[0] as FakeNode
    expect(drawn.attributes.get('class')).toBe('console-unavailable')
    expect(drawn.attributes.get('data-kind')).toBe('refused')
    expect(textOf(drawn)).toContain(because)
    // No figure at all: nothing of the Live screen was drawn, so there is no
    // repository row, no run and no reading to mistake for a measurement.
    expect(classed(panel, 'live-repo-name')).toEqual([])
    expect(classed(panel, 'live-run')).toEqual([])
  })

  it('draws the sentence when a read rejects instead of answering', async () => {
    // A read can fail to answer two ways: the member throws before it returns,
    // and the promise it returned rejects. `invoke` on a channel with nothing
    // behind it is the second, and it is the one a `try`/`catch` around the call
    // does not see. Both are "the console could not read this": the screen draws
    // the sentence it draws for any other unreadable answer, rather than keeping
    // the previous screen while a rejection nobody caught floats away.
    const { bridge } = fakeBridge({
      replies: {
        subscribeLive: () =>
          snapshotReply(syntheticDocuments()[0] as Readonly<Record<string, unknown>>),
        showBudget: () => Promise.reject(new Error('no handler registered for budget.show')),
        runReport: () => Promise.reject(new Error('no handler registered for spend.report')),
      },
    })
    const document = await boot(bridge, new FakeDocument())
    const panel = panelOf(document)
    buttonFor(document, 'spend').click()
    await flush()

    const drawn = panel.children[0] as FakeNode
    expect(drawn.attributes.get('class')).toBe('console-unavailable')
    expect(drawn.attributes.get('data-kind')).toBe('refused')
    expect(classed(drawn, 'unavailable-operation')[0]?.textContent).toBe('spend.report')
    expect(textOf(drawn)).toContain(READ_UNREADABLE)
  })

  it('draws the synthetic frame’s repository and runs, and opens Run detail from a run row', async () => {
    const documents = syntheticDocuments()
    const frame = documents[documents.length - 1] as Readonly<Record<string, unknown>>
    const { bridge, calls } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(frame),
        // The two reads the Run screen is drawn from answer empty documents:
        // this case is about the wiring -- which run the row asked about -- and
        // the screen reads its own inputs tolerantly.
        diagnoseRun: () => ({ ok: true, data: { document: {} } }),
        readRunLogs: () => ({ ok: true, data: { document: { entries: [], total: 0, refused: 0 } } }),
      },
    })
    const document = await boot(bridge, new FakeDocument())
    const panel = panelOf(document)
    // The frame's own repository and both of its runs, drawn from the
    // committed recording (the last frame carries `20261015-120400-aa03`
    // alongside `20261015-120000-aa01`).
    expect(classed(panel, 'live-repo-name').map((node) => node.textContent)).toEqual([
      'r-4c25cf20/r-f1aa9214',
    ])
    const rows = classed(panel, 'live-run')
    expect(rows.map((row) => classed(row, 'live-run-id')[0]?.textContent)).toEqual([
      '20261015-120000-aa01',
      '20261015-120400-aa03',
    ])

    // The row is a door: clicking it asks for that run, and only that run.
    const first = rows[0] as FakeNode
    first.click()
    await flush()
    expect(calls.filter((call) => call.name === 'diagnoseRun').map((call) => call.args)).toEqual([
      { run: '20261015-120000-aa01' },
    ])
    expect(calls.filter((call) => call.name === 'readRunLogs').map((call) => call.args)).toEqual([
      { run: '20261015-120000-aa01' },
    ])
    // And the Run screen is what the panel holds now, in place of the Live one.
    expect((panel.children[0] as FakeNode).attributes.get('class')).toBe('run')
    expect(classed(panel, 'live-repo-name')).toEqual([])
  })

  it('draws a repository’s kill banner, and withholds it in the reduced mode', async () => {
    // The `kill` view is one panel of the Live screen rather than a screen of its
    // own, so the gate decides whether the model the screen is handed carries the
    // kill state at all (C10). The same killed frame, in the two modes.
    const last = syntheticDocuments()[syntheticDocuments().length - 1] as Readonly<Record<string, unknown>>
    const killed = {
      ...last,
      repos: (last['repos'] as readonly Record<string, unknown>[]).map((repo) => ({
        ...repo,
        kill: { by: 'operator', at: '2026-10-15T12:04:00Z', reason: 'runaway spend' },
      })),
    }
    const banners = async (compatibility: Record<string, unknown>): Promise<readonly FakeNode[]> => {
      const { bridge } = fakeBridge({ compatibility, replies: { subscribeLive: () => snapshotReply(killed) } })
      const document = await boot(bridge, new FakeDocument())
      const panel = panelOf(document)
      expect(classed(panel, 'live-repo-name'), 'the Live screen was not drawn').toHaveLength(1)
      return classed(panel, 'live-kill')
    }
    const drawn = await banners(anOkCompatibility())
    expect(drawn.map((banner) => banner.attributes.get('data-killed')), 'the ok mode drew no kill banner').toContain('true')
    expect(await banners(aReducedCompatibility()), 'the reduced mode drew the kill state').toEqual([])
  })

  it.each(['readRunLogs', 'runLogLink'] as const)(
    'does not draw a run whose %s answer landed after the operator went back to Live',
    async (late) => {
      const held: (() => void)[] = []
      const frame = syntheticDocuments()[syntheticDocuments().length - 1] as Readonly<Record<string, unknown>>
      const answers: Record<string, unknown> = {
        diagnoseRun: { ok: true, data: { document: {} } },
        readRunLogs: { ok: true, data: { document: { entries: [], total: 0, refused: 0 } } },
        runLogLink: { ok: true, data: 'https://console.cloud.google.com/logs' },
      }
      const answer = (name: string) => (): unknown =>
        name === late ? new Promise((resolve) => held.push(() => resolve(answers[name]))) : answers[name]
      const { bridge } = fakeBridge({
        replies: {
          subscribeLive: () => snapshotReply(frame),
          diagnoseRun: answer('diagnoseRun'),
          readRunLogs: answer('readRunLogs'),
          runLogLink: answer('runLogLink'),
        },
      })
      const document = await boot(bridge, new FakeDocument())
      const panel = panelOf(document)
      ;(classed(panel, 'live-run')[0] as FakeNode).click()
      await flush()
      buttonFor(document, 'live').click()
      expect(panel.children[0]?.attributes.get('class')).toBe('live')
      expect(held, `the run’s ${late} answered before the operator moved: the case proves nothing`).toHaveLength(1)
      held[0]?.()
      await flush()
      expect(panel.children[0]?.attributes.get('class'), 'a late run record replaced the Live screen').toBe('live')
    },
  )

  it('draws the refusal, not an empty run, when a run’s own record refuses', async () => {
    // `diagnose` and `logs` are the run's own record: a refused read of either
    // is a run this console cannot draw, and handing the screen `null` would
    // draw an empty log window and a ledger of `n/a` -- which reads as "this run
    // logged nothing and spent nothing" when the console could not ask.
    const frame = syntheticDocuments()[syntheticDocuments().length - 1] as Readonly<Record<string, unknown>>
    const { bridge } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(frame),
        // The diagnosis arrives, so the refusal this case is about is the log
        // read's and not the first read's.
        diagnoseRun: () => ({ ok: true, data: { document: {} } }),
        // The sentence as it arrives from the main process: `cliSentence`
        // stripped the CLI's own `fugaro: ` prefix before the reply was built.
        readRunLogs: () => ({ ok: false, reason: 'no such run 20261015-120000-aa01' }),
      },
    })
    const document = await boot(bridge, new FakeDocument())
    const panel = panelOf(document)
    const row = classed(panel, 'live-run')[0] as FakeNode
    row.click()
    await flush()
    const drawn = panel.children[0] as FakeNode
    expect(drawn.attributes.get('class')).toBe('console-unavailable')
    expect(drawn.attributes.get('data-kind')).toBe('refused')
    // The CLI's own sentence, and the operation it came from.
    expect(textOf(drawn)).toContain('no such run 20261015-120000-aa01')
    expect(classed(panel, 'unavailable-operation')[0]?.textContent).toBe('run.logs')
    // And nothing of the run was drawn: no log window that would read as an
    // empty one.
    expect(classed(panel, 'run-log-line')).toEqual([])
  })

  describe('the Live poll', () => {
    beforeEach(() => {
      vi.useFakeTimers()
    })
    afterEach(() => {
      vi.useRealTimers()
    })

    it('keeps at most one live.watch in flight, however many ticks go by', async () => {
      // The first read answers at once, so the poll starts; every read after it
      // is held open, which is the state a slow watch leaves the console in.
      const held: (() => void)[] = []
      const frame = syntheticDocuments()[0] as Readonly<Record<string, unknown>>
      let made = 0
      const { bridge, calls } = fakeBridge({
        replies: {
          subscribeLive: () => {
            made += 1
            if (made === 1) return Promise.resolve(snapshotReply(frame))
            return new Promise((resolve) => {
              held.push(() => {
                resolve(snapshotReply(frame))
              })
            })
          },
        },
      })
      await boot(bridge, new FakeDocument())
      expect(calls.map((call) => call.name)).toEqual(['subscribeLive'])

      // One tick: a second read is made, and it does not answer.
      await vi.advanceTimersByTimeAsync(LIVE_POLL_MS)
      expect(calls.map((call) => call.name)).toEqual(['subscribeLive', 'subscribeLive'])
      expect(held).toHaveLength(1)

      // Three more ticks with that read outstanding: **no third call**. This is
      // the claim the flag exists for -- a slow read cannot be overtaken by the
      // next tick, so the console never has two watches of one installation in
      // flight at once.
      await vi.advanceTimersByTimeAsync(LIVE_POLL_MS * 3)
      expect(calls.map((call) => call.name)).toEqual(['subscribeLive', 'subscribeLive'])

      // Once it answers, the next tick is a new read.
      held[0]?.()
      await vi.advanceTimersByTimeAsync(LIVE_POLL_MS)
      expect(calls.map((call) => call.name)).toHaveLength(3)
    })

    it('stops polling while the window is not visible, and starts again when it is', async () => {
      const { bridge, calls } = fakeBridge({
        replies: {
          subscribeLive: () => snapshotReply(syntheticDocuments()[0] as Readonly<Record<string, unknown>>),
        },
      })
      const document = await boot(bridge, new FakeDocument())
      expect(document.listeners.get('visibilitychange'), 'the entry listened for nothing').toHaveLength(1)

      // Hidden: the interval is cleared, and the reads stop -- no poll keeps
      // spawning watches for a window nobody can see.
      document.setVisibility('hidden')
      await vi.advanceTimersByTimeAsync(LIVE_POLL_MS * 3)
      expect(calls.filter((call) => call.name === 'subscribeLive')).toHaveLength(1)

      // Visible again: the entry puts the interval back, and the next tick is a
      // read. Both halves are asserted, because a `syncPolling` that only ever
      // cleared the interval would pass the first half alone.
      document.setVisibility('visible')
      await vi.advanceTimersByTimeAsync(LIVE_POLL_MS)
      expect(calls.filter((call) => call.name === 'subscribeLive')).toHaveLength(2)
    })

    it('stops polling when the operator leaves Live, and starts again on the way back', async () => {
      const { bridge, calls } = fakeBridge({
        replies: {
          subscribeLive: () => snapshotReply(syntheticDocuments()[0] as Readonly<Record<string, unknown>>),
        },
      })
      const document = await boot(bridge, new FakeDocument())
      expect(calls.map((call) => call.name)).toEqual(['subscribeLive'])

      // Spend is offered in an `ok` console, and leaving Live for it stops the
      // interval; the read behind the view is made once, on entry.
      buttonFor(document, 'spend').click()
      await flush()
      expect(calls.filter((call) => call.name === 'showBudget')).toHaveLength(1)
      const afterSpend = calls.length
      await vi.advanceTimersByTimeAsync(LIVE_POLL_MS * 3)
      expect(calls.length, 'the Live poll kept running while Spend was on screen').toBe(afterSpend)

      // Back to Live: the screen the last poll drew is put back, and the poll
      // resumes.
      buttonFor(document, 'live').click()
      await vi.advanceTimersByTimeAsync(LIVE_POLL_MS)
      expect(calls.filter((call) => call.name === 'subscribeLive')).toHaveLength(2)
    })

    it('stops polling while a run is on screen, and starts again on the way back', async () => {
      // Run detail is reached from a run row rather than offered in the
      // navigation, and it is still a view: a poll that kept running under it
      // would replace the run the operator opened with the Live screen a second
      // later.
      const { bridge, calls } = fakeBridge({
        replies: {
          subscribeLive: () => snapshotReply(syntheticDocuments()[0] as Readonly<Record<string, unknown>>),
          diagnoseRun: () => ({ ok: true, data: { document: {} } }),
          readRunLogs: () => ({ ok: true, data: { document: {} } }),
          runLogLink: () => ({ ok: false, reason: 'this run has no transcript link' }),
        },
      })
      const document = await boot(bridge, new FakeDocument())
      const panel = panelOf(document)
      ;(classed(panel, 'live-run')[0] as FakeNode).click()
      await flush()
      expect(panel.children[0]?.attributes.get('class')).toBe('run')

      const afterRun = calls.length
      await vi.advanceTimersByTimeAsync(LIVE_POLL_MS * 3)
      expect(calls.length, 'the Live poll kept running under the run screen').toBe(afterRun)

      // And leaving it, by any of the offered views, puts the interval back.
      buttonFor(document, 'live').click()
      await vi.advanceTimersByTimeAsync(LIVE_POLL_MS)
      expect(calls.filter((call) => call.name === 'subscribeLive')).toHaveLength(2)
    })
  })

  describe('a read that lands after the operator moved on', () => {
    // Every read is a message round, and `spend.report` is a whole report: an
    // answer can arrive after the operator has chosen another view. The panel is
    // *their* view, so a drawing that lands late must not replace it -- a screen
    // that names Projects and holds Spend is worse than one that names Spend.
    beforeEach(() => {
      vi.useFakeTimers()
    })
    afterEach(() => {
      vi.useRealTimers()
    })

    it('does not draw a spend report over the view the operator moved to', async () => {
      const held: (() => void)[] = []
      const frame = syntheticDocuments()[0] as Readonly<Record<string, unknown>>
      const { bridge } = fakeBridge({
        replies: {
          subscribeLive: () => snapshotReply(frame),
          showBudget: () => ({ ok: true, data: {} }),
          runReport: () =>
            new Promise((resolve) => {
              held.push(() => resolve({ ok: true, data: {} }))
            }),
        },
      })
      const document = await boot(bridge, new FakeDocument())
      const panel = panelOf(document)
      expect(panel.children[0]?.attributes.get('class')).toBe('live')

      // Spend is entered and its report does not answer; the operator goes back
      // to Live, which puts the last poll's own drawing back at once.
      buttonFor(document, 'spend').click()
      await flush()
      buttonFor(document, 'live').click()
      expect(panel.children[0]?.attributes.get('class')).toBe('live')

      // The spend report answers last. The panel is Live -- the view on screen
      // -- and not the Spend screen the report was read for.
      expect(held, 'the spend report answered before the operator moved: the case proves nothing').toHaveLength(1)
      held[0]?.()
      await flush()
      expect(panel.children[0]?.attributes.get('class')).toBe('live')
    })

    it('does not draw a report for a range the operator has already changed again', async () => {
      // Two control changes in a row are two reports in flight. The one asked
      // for first can answer last, and drawing it would put the earlier range's
      // figures under a picker that marks the later one.
      const held: { since: unknown; answer: () => void }[] = []
      const frame = syntheticDocuments()[0] as Readonly<Record<string, unknown>>
      const { bridge } = fakeBridge({
        replies: {
          subscribeLive: () => snapshotReply(frame),
          showBudget: () => ({ ok: true, data: {} }),
          runReport: (args) =>
            new Promise((resolve) => {
              held.push({
                since: (args as Record<string, unknown>)['since'],
                answer: () => resolve({ ok: true, data: { document: {} } }),
              })
            }),
        },
      })
      const document = await boot(bridge, new FakeDocument())
      const panel = panelOf(document)
      buttonFor(document, 'spend').click()
      await flush()
      held.shift()?.answer()
      await flush()
      const windows = classed(panel, 'spend-window')
      expect(windows.length, 'the spend screen drew no range picker: the case proves nothing').toBeGreaterThan(2)

      // Two ranges, chosen one after the other; neither report has answered.
      windows[1]?.click()
      windows[2]?.click()
      await flush()
      expect(held.map((each) => each.since)).toEqual([windows[1]?.textContent, windows[2]?.textContent])
      const [earlier, later] = held
      later?.answer()
      await flush()
      const drawn = panel.children[0]
      const current = classed(panel, 'spend-window')
        .filter((each) => each.attributes.get('data-current') === 'true')
        .map((each) => each.textContent)
      expect(current).toEqual([windows[2]?.textContent])

      // The earlier range answers last, and the screen is still the later one's.
      earlier?.answer()
      await flush()
      expect(panel.children[0], 'a report for a superseded range replaced the screen').toBe(drawn)
    })

    it('does not draw a live frame over the view the operator moved to', async () => {
      const held: (() => void)[] = []
      let made = 0
      const frames = syntheticDocuments()
      const { bridge } = fakeBridge({
        replies: {
          subscribeLive: () => {
            made += 1
            const frame = frames[made === 1 ? 0 : frames.length - 1] as Readonly<Record<string, unknown>>
            if (made === 1) return snapshotReply(frame)
            return new Promise((resolve) => {
              held.push(() => resolve(snapshotReply(frame)))
            })
          },
          showBudget: () => ({ ok: true, data: {} }),
          runReport: () => ({ ok: true, data: {} }),
        },
      })
      const document = await boot(bridge, new FakeDocument())
      const panel = panelOf(document)

      // A poll is in flight and does not answer; the operator leaves Live for
      // Spend, which draws something of its own.
      await vi.advanceTimersByTimeAsync(LIVE_POLL_MS)
      buttonFor(document, 'spend').click()
      await flush()
      const chosen = panel.children[0]?.attributes.get('class')
      expect(chosen, 'the operator never left Live: the case would prove nothing').not.toBe('live')

      // The poll answers last, with a frame the console *can* read -- so a
      // drawing that landed would be the Live screen, and the panel is not it.
      expect(held, 'the poll answered before the operator moved: the case proves nothing').toHaveLength(1)
      held[0]?.()
      await flush()
      expect(panel.children[0]?.attributes.get('class')).toBe(chosen)
    })
  })
})

// ---------------------------------------------------------------------------
// The fixture source is not reachable from the shipped code.
// ---------------------------------------------------------------------------

/**
 * importsOf is every module specifier a source file names in an `import` or a
 * `require`, which is what "imports nothing from" is a claim about. A mention in
 * prose is not an import -- several of these files name `src/source/fixture.ts`
 * in a comment, on purpose -- so this reads the specifiers and not the text.
 *
 * All three spellings are read: `from '...'` (a static import or a re-export),
 * `require('...')` and `import('...')`. Both quoting styles are read too: a
 * scan that knew only one of them would be a scan that a differently quoted
 * import of the fixture source walks past, which is the claim this makes.
 */
function importsOf(source: string): readonly string[] {
  const found: string[] = []
  for (const match of source.matchAll(
    /(?:\bfrom\s*|\brequire\s*\(\s*|\bimport\s*\(\s*)['"]([^'"]+)['"]/g,
  )) {
    found.push(match[1] as string)
  }
  return found
}

/** everySource is every `.ts` file under a directory. */
function everySource(directory: string): readonly string[] {
  const found: string[] = []
  for (const entry of readdirSync(directory)) {
    const path = join(directory, entry)
    if (statSync(path).isDirectory()) found.push(...everySource(path))
    else if (entry.endsWith('.ts')) found.push(path)
  }
  return found
}

describe('the fixture source', () => {
  it('is imported by nothing under src/ but itself', () => {
    // The probe: this scan is only about imports if it finds them -- and it
    // finds them however they are written. A scan that only read one quoting
    // style would be a scan a double-quoted import walks past, which is the
    // whole of the claim it is here to make.
    expect(
      importsOf(
        `import { a } from '../source/fixture'\nrequire('./x')\n` +
          `import type { B } from "../source/fixture"\nconst c = await import("./fixture")\n`,
      ),
    ).toEqual(['../source/fixture', './x', '../source/fixture', './fixture'])
    const files = everySource(join(CONSOLE_ROOT, 'src'))
    expect(files.length, 'the scan found no sources: it would pass by finding nothing').toBeGreaterThan(20)
    const importers = files
      .filter((file) => !file.endsWith(join('source', 'fixture.ts')))
      .filter((file) => importsOf(readFileSync(file, 'utf8')).some((specifier) => /(^|\/)fixture$/.test(specifier)))
    expect(importers, 'the fixture source is reachable from the shipped code').toEqual([])
  })

  it('leaves the main process the one source the shipped app can construct', () => {
    const index = readFileSync(join(CONSOLE_ROOT, 'src', 'main', 'index.ts'), 'utf8')
    expect(index).toContain('createBoundSource(')
    expect(importsOf(index).filter((specifier) => /fixture/.test(specifier))).toEqual([])
  })
})

// The entry spec draws nothing into its window by hand.
// ---------------------------------------------------------------------------

describe('the entry e2e spec', () => {
  it('loads no code into the window: the Live screen is the bundle’s own drawing', () => {
    // `test/e2e/live.spec.ts`, `degraded.spec.ts`, `skew.spec.ts`, `spend.spec.ts`
    // and the rest all draw their screens by calling the console's own functions
    // through `webContents.executeJavaScript` -- which is the right tool for a
    // spec whose subject is one screen drawn by hand, and the wrong one for this
    // file. `entry.spec.ts` is the spec that asserts the *app* draws: the entry
    // mounts Live on load, polls `live.watch` and paints what came back. A case
    // here that reached for `executeJavaScript` would be drawing the screen
    // itself, and would then pass whether or not the entry ever did.
    //
    // The window's own shape is asserted beside this (one `<script>`, resolved
    // to the bundle), but that cannot see this: code injected through
    // `executeJavaScript` adds no element to the document. So the claim is read
    // off the spec's text, the way the fixture-source scan above is.
    const spec = readFileSync(join(CONSOLE_ROOT, 'test', 'e2e', 'entry.spec.ts'), 'utf8')
    expect(spec.length, 'the spec read empty: it would pass by finding nothing').toBeGreaterThan(1000)
    // The *call*, not the word: this file names `executeJavaScript` in prose to
    // say it does not use it, and a scan over the bare word would fail on its
    // own explanation.
    expect(spec.match(/executeJavaScript\s*\(/g) ?? []).toEqual([])
    // `page.evaluate` is the *reading* half and is what the file uses; the
    // assertion above is only meaningful next to the thing it is not.
    expect(spec).toContain('page.evaluate')
  })
})
