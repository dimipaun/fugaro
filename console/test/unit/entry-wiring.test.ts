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
  CONSOLE_THEME,
  LIVE_POLL_MS,
  RAIL_ADD_CLASS,
  RAIL_CLASS,
  RAIL_COLLAPSE_CLASS,
  RAIL_EXPAND_LABEL,
  RAIL_COLLAPSE_LABEL,
  RAIL_HEAD_CLASS,
  RAIL_HINT_CLASS,
  RAIL_LIST_CLASS,
  RAIL_LIST_ID,
  RAIL_NAME_CLASS,
  RAIL_PROJECT_CLASS,
  RAIL_ROW_CLASS,
  RAIL_SPEND_CLASS,
  RAIL_STATE_CLASS,
  RAIL_WARNING_CLASS,
  RAIL_WARNING_GLYPH,
  ISSUE_CLASS,
  ISSUE_COMMAND_CLASS,
  ISSUE_DETAILS_LABEL,
  ISSUE_LINES_CLASS,
  ISSUE_REMOVE_LABEL,
  ISSUE_CHOOSE_LABEL,
  ISSUE_CHECKOUT_LABEL,
  ISSUE_SENTENCE_CLASS,
  ISSUE_SUMMARY_CLASS,
  READ_UNREADABLE,
  VIEW_BUTTON_CLASS,
  VIEW_NAV_CLASS,
  VIEW_PANEL_CLASS,
  bootDashboard,
  isMacPlatform,
  SHORTCUT_ROWS,
  shortcutRow,
  tabStop,
  tabTarget,
} from '../../src/renderer/entry'
import { decodeSnapshot } from '../../src/shared/schema'
import {
  FakeDocument,
  FakeNode,
  aReducedCompatibility,
  aRefusedCompatibility,
  anOkCompatibility,
  everyNode,
  keyEvent,
  textOf,
} from '../support/fakeDom'

/** CONSOLE_ROOT is the package root, which is where the sources, the fixtures
 * and the built tree are read from. */
const CONSOLE_ROOT = join(__dirname, '..', '..')

/** READ_METHODS is the eight reads `entry.ts` may reach for, by the name the
 * preload exposes. Every one of them is on the fake bridge below, and every one
 * records its call -- so "this read was never made" is a claim this suite can
 * make about any of them. */
const READ_METHODS = [
  'subscribeLive',
  'showBudget',
  'runReport',
  'listRuns',
  'statsRuns',
  'diagnoseRun',
  'readRunLogs',
  'runLogLink',
] as const

/** A_REPLY is a bridge method's answer. */
type AReply = (args: unknown) => unknown

/** FakeBridge is the preload seam, recorded: the handshake, the installation,
 * and the eight reads, each answering what the case told it to and appending its
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
 * Live read has been drawn. `navigator` is the window's, which the rail's
 * shortcut keys read for the platform's modifier (Task 6.5); a case that does
 * not pass one drives the "not macOS" half of the rule, which is what a `vm`
 * realm and this suite both are. */
async function boot(
  bridge: Record<string, unknown>,
  document: FakeDocument,
  navigator?: unknown,
): Promise<FakeDocument> {
  await bootDashboard({
    fugaro: bridge,
    document: document as unknown as Document,
    navigator,
  })
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
// The Spend screen's by-person read (console L5b), through the entry's own
// `peopleReader`: the knob turns it on, the document reaches the panel, and a
// refusal becomes the panel's own sentence rather than the screen's.
// ---------------------------------------------------------------------------

describe('the by-person read the entry wires', () => {
  /** STATS is an operator's `runs stats --json` document, as the CLI writes it. */
  const STATS: Readonly<Record<string, unknown>> = {
    project: 'acme',
    by: 'user',
    view: 'all',
    you: 'ada',
    operator: true,
    since: '2026-09-23',
    until: '2026-10-23',
    max_runs: 5000,
    truncated: false,
    rows: [
      { user: 'ada', currency: 'USD', runs: 2, succeeded: 2, counted_usd: 1.5, notional_usd: 0, counted_runs: 2, avg_counted_usd: 0.75 },
      { user: 'bob', currency: 'USD', runs: 1, failed: 1, counted_usd: 0.25, notional_usd: 0, counted_runs: 1, avg_counted_usd: 0.25 },
    ],
    totals: [
      { user: '', currency: 'USD', runs: 3, succeeded: 2, failed: 1, counted_usd: 1.75, notional_usd: 0, counted_runs: 3, avg_counted_usd: 0.58 },
    ],
    unlaunched: 0,
    notes: [],
  }

  /** spendWithPeople boots the dashboard, opens Spend and turns the person
   * knob on, with `statsRuns` answering `stats`. */
  async function spendWithPeople(stats: AReply) {
    const frame = syntheticDocuments()[0] as Readonly<Record<string, unknown>>
    const fake = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(frame),
        showBudget: () => ({ ok: true, data: { document: {} } }),
        runReport: () => ({ ok: true, data: { document: {} } }),
        statsRuns: stats,
      },
    })
    const document = await boot(fake.bridge, new FakeDocument())
    const panel = panelOf(document)
    buttonFor(document, 'spend').click()
    await flush()
    // The knob off is no by-person read at all (D15).
    expect(fake.calls.filter((call) => call.name === 'statsRuns')).toEqual([])
    const knob = classed(panel, 'spend-persons')[0]
    expect(knob, 'the spend screen drew no person knob: the case proves nothing').toBeDefined()
    knob?.click()
    await flush()
    await flush()
    return { panel, calls: fake.calls }
  }

  it('reads `statsRuns` with the picker’s window once the knob is on, and draws its document', async () => {
    const { panel, calls } = await spendWithPeople(() => ({ ok: true, data: { document: STATS } }))
    expect(calls.filter((call) => call.name === 'statsRuns').map((call) => call.args)).toEqual([
      { since: '30d' },
    ])
    const people = classed(panel, 'spend-people')
    expect(people).toHaveLength(1)
    expect(people[0]?.attributes.get('data-available')).toBe('true')
    expect(people[0]?.attributes.get('data-view')).toBe('all')
    const rows = classed(panel, 'spend-people-rows')[0]
    expect(rows?.children.length).toBe(2)
    expect(classed(panel, 'spend-people-reason')).toEqual([])
  })

  it('turns a refused by-person read into the panel’s own sentence, and still draws the report', async () => {
    const { panel } = await spendWithPeople(() => ({
      ok: false,
      reason: 'runs stats: cannot tell who you are, and this view shows your own row alone',
    }))
    const drawn = panel.children[0] as FakeNode
    // The screen is Spend, not the console's refusal screen: the refusal is the
    // panel's alone.
    expect(drawn.attributes.get('class')).not.toBe('console-unavailable')
    const people = classed(panel, 'spend-people')
    expect(people).toHaveLength(1)
    expect(people[0]?.attributes.get('data-available')).toBe('false')
    expect(classed(panel, 'spend-people-reason').map((node) => node.textContent)).toEqual([
      'runs stats: cannot tell who you are, and this view shows your own row alone',
    ])
    expect(classed(panel, 'spend-person')).toEqual([])
  })

  it('draws the console’s own sentence for a by-person answer that is not a snapshot', async () => {
    // `documentOf` refuses the shape, and the throw carries *its* sentence: the
    // panel draws that, not an empty list.
    const { panel } = await spendWithPeople(() => ({ ok: true, data: { document: [] } }))
    expect(classed(panel, 'spend-people')[0]?.attributes.get('data-available')).toBe('false')
    expect(classed(panel, 'spend-people-reason').map((node) => node.textContent)).toEqual([
      READ_UNREADABLE,
    ])
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

  it('hands the startup read the installation’s distribution and checkout (T-D1)', () => {
    // `resolveHandshakeBinary`'s WSL branch is reachable only if the entry hands
    // the read a `distro`, and the directory the CLI starts in travels with it
    // as the wrapper's `--cd` (`wslDirectory`, D3(a)). The two are the fields
    // the store carries and the handshake would otherwise drop: a stored WSL
    // installation read as a host one is a console that reports a missing
    // binary for the installation the operator chose, because the walk is a
    // walk of the host and the host has no `fugaro` (T-C2). This file imports
    // Electron's runtime, so the claim is read off its text -- the same way the
    // fixture-source scan above is -- and the *function body* is what is read,
    // so naming the field in prose elsewhere in the file cannot satisfy it.
    const index = readFileSync(join(CONSOLE_ROOT, 'src', 'main', 'index.ts'), 'utf8')
    // `\n}\n` and not `\n}`: the **return type** ends with a brace at column
    // zero too (`} | null {`), so the shorter pattern reads the signature and
    // nothing else -- and would then fail on a body that is exactly right.
    const declared = /function handshakeInstallation\(\)[\s\S]*?\n}\n/.exec(index)
    if (declared === null) throw new Error('the scan found no handshakeInstallation: it would pass by finding nothing')
    expect(declared[0]).toContain('distro: chosen.distro')
    expect(declared[0]).toContain('checkout: chosen.checkout')
    // ...and the read that consumes it is handed that object: the resolution
    // takes `installation`, and a second object built beside this one would be
    // the same defect one line later.
    expect(index).toMatch(/resolveHandshakeBinary\(\{[\s\S]*?installation: chosen/)
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

// The installation rail (desktop-console plan Task 6.3; design section 4.1).
// ---------------------------------------------------------------------------

/** railOf is the rail the entry appended. */
function railOf(document: FakeDocument): FakeNode {
  const rail = everyNode(document.body).find((node) => node.attributes.get('class') === RAIL_CLASS)
  if (rail === undefined) throw new Error('the entry drew no installation rail')
  return rail
}

/** railRow is the rail's one row for the installation the dashboard can read. */
function railRow(document: FakeDocument): FakeNode {
  const rows = classed(railOf(document), RAIL_ROW_CLASS)
  if (rows.length !== 1) throw new Error(`the rail drew ${rows.length} rows, not one`)
  return rows[0] as FakeNode
}

/** spanOf is one of a row's four spans. */
function spanOf(document: FakeDocument, className: string): FakeNode {
  const span = classed(railRow(document), className)[0]
  if (span === undefined) throw new Error(`the rail's row drew no ${className}`)
  return span
}

/** nowStamp is the console's own clock shape. A frame stamped now is a feed that
 * has not gone quiet, which is what makes the rail's word `running` rather than
 * `silent`: `snapshotReply`'s own stamp is a fixed date, and every frame in this
 * suite is therefore stale unless a case says otherwise. */
function nowStamp(): string {
  return new Date().toISOString().replace(/\.\d{3}Z$/, 'Z')
}

describe('the installation rail', () => {
  it('draws one row for the installation the dashboard can read, with its state word and its glyph, and makes no read of its own', async () => {
    const frame = syntheticDocuments()[0] as Readonly<Record<string, unknown>>
    const { bridge, calls } = fakeBridge({ replies: { subscribeLive: () => snapshotReply(frame, nowStamp()) } })
    const document = await boot(bridge, new FakeDocument())
    const rail = railOf(document)
    // The rail is the body's **first** content child and the grid's first
    // column: the four regions of the design's tree, in the design's order.
    expect(rail.tagName).toBe('ASIDE')
    expect(rail.children.map((child) => child.attributes.get('class'))).toEqual([
      RAIL_HEAD_CLASS,
      RAIL_LIST_CLASS,
      RAIL_ADD_CLASS,
      RAIL_HINT_CLASS,
    ])
    // One row, because one installation is what `installation.current` answers
    // (`chosen: true`, project `acme`). The two identity spans draw the one
    // label the store keeps -- the canvas's two-line identity.
    expect(railRow(document).tagName).toBe('BUTTON')
    expect(spanOf(document, RAIL_NAME_CLASS).textContent).toBe('acme')
    expect(spanOf(document, RAIL_PROJECT_CLASS).textContent).toBe('acme')
    // The state is the word **and** its glyph, never one instead of the other
    // (design section 3.5), and `data-state` is the tone's hook.
    const state = spanOf(document, RAIL_STATE_CLASS)
    expect(state.textContent).toBe('● running')
    expect(state.attributes.get('data-state')).toBe('running')
    // The spend is `n/a` with the console's own sentence until the console has
    // read the budget -- and **the rail never reads it itself**: a `budget` read
    // at mount would be a spawn the entry spec's spawn-log case refuses.
    const spend = spanOf(document, RAIL_SPEND_CLASS)
    expect(spend.textContent).toBe('n/a')
    expect(spend.attributes.get('title')).toBe('no budget show document was read')
    expect(calls.map((call) => call.name)).toEqual(['subscribeLive'])
  })

  it('draws no row for an installation it cannot read, and draws the rail either way', async () => {
    // A bridge with no `installation.list` answer falls back to
    // `installation.current`, and an installation that answer does not name is
    // not invented (design section 4.1).
    for (const installation of [
      { ok: false, reason: 'the installations file could not be read' },
      { ok: true, data: { chosen: false, sentence: 'no installation has been chosen' } },
      { ok: true, data: { nothing: 'the console can read' } },
    ]) {
      const { bridge } = fakeBridge({ installation })
      const document = await boot(bridge, new FakeDocument())
      const rail = railOf(document)
      expect(classed(rail, RAIL_ROW_CLASS)).toHaveLength(0)
      // The region is still the region: no row is not a missing rail.
      expect(classed(rail, RAIL_LIST_CLASS)).toHaveLength(1)
      expect(classed(rail, RAIL_ADD_CLASS)).toHaveLength(2)
    }
  })

  it('announces the collapse and toggles the rail’s own collapsed state', async () => {
    const { bridge } = fakeBridge({})
    const document = await boot(bridge, new FakeDocument())
    const rail = railOf(document)
    const control = classed(rail, RAIL_COLLAPSE_CLASS)[0]
    expect(control, 'the rail drew no collapse control').toBeDefined()
    const button = control as FakeNode
    // The control names what it is about and says which state it is in, so the
    // collapse is announced rather than drawn only (design section 4.1).
    expect(button.attributes.get('aria-controls')).toBe(RAIL_LIST_ID)
    expect(classed(rail, RAIL_LIST_CLASS)[0]?.attributes.get('id')).toBe(RAIL_LIST_ID)
    expect(button.attributes.get('aria-expanded')).toBe('true')
    expect(button.attributes.get('aria-label')).toBe(RAIL_COLLAPSE_LABEL)
    expect(rail.attributes.get('data-collapsed')).toBeUndefined()
    button.click()
    expect(button.attributes.get('aria-expanded')).toBe('false')
    expect(button.attributes.get('aria-label')).toBe(RAIL_EXPAND_LABEL)
    expect(rail.attributes.get('data-collapsed')).toBe('true')
    button.click()
    expect(button.attributes.get('aria-expanded')).toBe('true')
    expect(button.attributes.get('aria-label')).toBe(RAIL_COLLAPSE_LABEL)
    expect(rail.attributes.get('data-collapsed')).toBe('false')
  })

  it('says silent when the live read was refused, and draws the budget read the console made itself', async () => {
    const { bridge, calls } = fakeBridge({
      replies: {
        subscribeLive: () => ({ ok: false, reason: 'the CLI refused this read' }),
        showBudget: () => ({ ok: true, data: { document: { global: { counted_usd: 12.5 } } } }),
      },
    })
    const document = await boot(bridge, new FakeDocument())
    // The rail knows whether it is reading this installation now: the feed was
    // refused, so the word is `silent` and not `running`.
    expect(spanOf(document, RAIL_STATE_CLASS).textContent).toBe('▲ silent')
    expect(spanOf(document, RAIL_STATE_CLASS).attributes.get('data-state')).toBe('silent')
    expect(calls.some((call) => call.name === 'showBudget')).toBe(false)
    // The operator opens Spend. The budget read is the console's own -- the one
    // the caps panel makes -- and the rail draws its counted dollars.
    buttonFor(document, 'spend').click()
    await flush()
    expect(calls.some((call) => call.name === 'showBudget')).toBe(true)
    expect(spanOf(document, RAIL_SPEND_CLASS).textContent).toBe('$12.50')
    expect(spanOf(document, RAIL_SPEND_CLASS).attributes.get('title')).toBeUndefined()
  })

  it('says silent when the feed it holds has gone quiet', async () => {
    // The other half of `silent`: the read answered, and the frame it brought is
    // older than `LIVE_STALE_AFTER_SECONDS`. `snapshotReply`'s own stamp is a
    // fixed date, so this is the state every stale frame leaves the rail in --
    // and the word is drawn beside its glyph, which is what keeps the state
    // readable when the tone is not (design section 3.5).
    const frame = syntheticDocuments()[0] as Readonly<Record<string, unknown>>
    const { bridge } = fakeBridge({ replies: { subscribeLive: () => snapshotReply(frame) } })
    const document = await boot(bridge, new FakeDocument())
    const state = spanOf(document, RAIL_STATE_CLASS)
    expect(state.textContent).toBe('▲ silent')
    expect(state.attributes.get('data-state')).toBe('silent')
  })
})

// ---------------------------------------------------------------------------
// The keyboard (desktop-console plan Task 6.5; design sections 4.1 and 4.3).
// ---------------------------------------------------------------------------

/** navOf is the navigation the entry appended. */
function navOf(document: FakeDocument): FakeNode {
  const nav = everyNode(document.body).find((node) => node.attributes.get('class') === VIEW_NAV_CLASS)
  if (nav === undefined) throw new Error('the entry drew no navigation')
  return nav
}

/** railListOf is the rail's list, which is the tablist its rows are tabs of. */
function railListOf(document: FakeDocument): FakeNode {
  const list = classed(railOf(document), RAIL_LIST_CLASS)[0]
  if (list === undefined) throw new Error('the rail drew no list')
  return list
}

describe('the view navigation’s keyboard', () => {
  it('is a tablist with one tab stop, and the arrows move the view and the focus with it', async () => {
    const { bridge, calls } = fakeBridge({
      replies: { showBudget: () => ({ ok: true, data: { document: { global: { counted_usd: 12.5 } } } }) },
    })
    const document = await boot(bridge, new FakeDocument())
    const nav = navOf(document)
    const live = buttonFor(document, 'live')
    const spend = buttonFor(document, 'spend')
    const projects = buttonFor(document, 'projects')

    // The nav is a tablist and its buttons are its tabs (design section 4.3). The view on
    // screen is marked twice: `aria-current` is the navigation's own mark, which the code
    // emitted before this task and `base.css` already selects, and `aria-selected` is the
    // same fact in the vocabulary a tablist is read with.
    expect(nav.attributes.get('role')).toBe('tablist')
    for (const button of [live, spend, projects]) {
      expect(button.attributes.get('role')).toBe('tab')
      expect(button.attributes.get('type')).toBe('button')
    }
    expect(live.attributes.get('aria-current')).toBe('true')
    expect(live.attributes.get('aria-selected')).toBe('true')
    expect(live.attributes.get('tabindex')).toBe('0')
    // The roving half: the other two are out of the tab order, so the whole navigation is
    // one stop rather than three.
    for (const other of [spend, projects]) {
      expect(other.attributes.get('aria-current')).toBe('false')
      expect(other.attributes.get('aria-selected')).toBe('false')
      expect(other.attributes.get('tabindex')).toBe('-1')
    }

    // Right moves to the next tab **and shows it**: a nav button's selection *is* the view
    // it puts on screen (design section 4.3), so the read behind the view is made and no
    // second key is needed.
    const pressed = live.press('ArrowRight')
    await flush()
    expect(pressed.defaultPrevented, 'the arrow key was left to the window').toBe(true)
    expect(spend.attributes.get('aria-current')).toBe('true')
    expect(spend.attributes.get('aria-selected')).toBe('true')
    expect(spend.attributes.get('tabindex')).toBe('0')
    expect(live.attributes.get('aria-current')).toBe('false')
    expect(live.attributes.get('tabindex')).toBe('-1')
    expect(document.activeElement, 'the ring did not follow the arrow').toBe(spend)
    // The view was *shown* and not merely marked: Spend is the screen that reads the budget,
    // so a handler that moved the marks without calling `show` would leave this list empty.
    expect(calls.filter((call) => call.name === 'showBudget')).toHaveLength(1)

    // End goes to the last tab, Home to the first, and Left wraps back from the first --
    // the one behaviour `tabTarget` documents as the pattern's own.
    spend.press('End')
    await flush()
    expect(projects.attributes.get('aria-current')).toBe('true')
    expect(document.activeElement).toBe(projects)
    projects.press('Home')
    await flush()
    expect(live.attributes.get('aria-current')).toBe('true')
    expect(document.activeElement).toBe(live)
    live.press('ArrowLeft')
    await flush()
    expect(projects.attributes.get('aria-current')).toBe('true')
    expect(document.activeElement).toBe(projects)

    // A key the navigation does not own is left to the window: the nav is a horizontal
    // tablist and its arrows are Left and Right (design section 4.3), while Up and Down are
    // the rail's (section 4.1).
    const down = projects.press('ArrowDown')
    expect(down.defaultPrevented, 'the navigation took a key that is the rail’s').toBe(false)
    expect(projects.attributes.get('aria-current')).toBe('true')
  })

  it('keeps one tab stop while Run detail is up, when no offered view is current', async () => {
    // Run detail is a view with no button (design section 2.1), so while it is up every
    // button is unmarked -- and a roving `tabindex` written as "the current view is 0" alone
    // would leave the navigation with no stop at all, which is a tablist the keyboard cannot
    // enter. The stop falls back to the first offered button, and this is the case that
    // fails if that fallback goes.
    const documents = syntheticDocuments()
    const frame = documents[documents.length - 1] as Readonly<Record<string, unknown>>
    const { bridge } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(frame),
        diagnoseRun: () => ({ ok: true, data: { document: {} } }),
        readRunLogs: () => ({ ok: true, data: { document: { entries: [], total: 0, refused: 0 } } }),
      },
    })
    const document = await boot(bridge, new FakeDocument())
    const row = classed(panelOf(document), 'live-run')[0]
    expect(row, 'the frame drew no run row').toBeDefined()
    ;(row as FakeNode).click()
    await flush()
    expect((panelOf(document).children[0] as FakeNode).attributes.get('class')).toBe('run')
    for (const button of navButtons(document)) {
      expect(button.attributes.get('aria-current')).toBe('false')
      expect(button.attributes.get('aria-selected')).toBe('false')
    }
    expect(
      navButtons(document)
        .filter((button) => button.attributes.get('tabindex') === '0')
        .map((button) => button.attributes.get('data-view')),
      'the navigation left the tab order while a run was on screen',
    ).toEqual(['live'])
  })
})

describe('the installation rail’s keyboard', () => {
  it('is a tablist whose selected row is its one tab stop, and Enter selects the row', async () => {
    const { bridge } = fakeBridge({})
    const document = await boot(bridge, new FakeDocument())
    const list = railListOf(document)
    const row = railRow(document)

    // The list is a tablist in its own right and its row is a tab (design sections 2.1 and
    // 4.1) -- the renderer's first `role=` -- and the row this console can read is the one
    // the console is reading, so it is selected from the first paint.
    expect(list.attributes.get('role')).toBe('tablist')
    expect(row.attributes.get('role')).toBe('tab')
    expect(row.attributes.get('aria-selected')).toBe('true')
    expect(row.attributes.get('tabindex')).toBe('0')

    // The arrows move the focus and change no mark: the rail's pattern is the
    // manual-activation one, so the key that chooses is Enter (design section 4.1).
    const down = row.press('ArrowDown')
    expect(down.defaultPrevented, 'the row’s arrow key was left to the list’s scroll').toBe(true)
    expect(document.activeElement).toBe(row)
    expect(row.attributes.get('aria-selected')).toBe('true')
    expect(row.press('Home').defaultPrevented).toBe(true)
    expect(row.press('End').defaultPrevented).toBe(true)

    const enter = row.press('Enter')
    expect(enter.defaultPrevented).toBe(true)
    expect(row.attributes.get('aria-selected')).toBe('true')
    expect(row.attributes.get('tabindex')).toBe('0')

    // And a key the list does not own is left to the window -- including Left and Right,
    // which are the navigation's.
    expect(row.press('ArrowRight').defaultPrevented).toBe(false)
    expect(row.press('a').defaultPrevented).toBe(false)
  })

  it('answers no key at all when it draws no row, and takes the shortcut keys either way', async () => {
    // A console with no installation chosen draws no row (Task 6.3), so the list has no tab
    // and nothing to address: the keys are still the console's -- a shortcut is prevented
    // whether or not a row answers it -- and nothing is selected, because nothing is drawn.
    const { bridge } = fakeBridge({
      installation: { ok: true, data: { chosen: false, sentence: 'no installation has been chosen' } },
    })
    const document = await boot(bridge, new FakeDocument())
    expect(classed(railOf(document), RAIL_ROW_CLASS)).toHaveLength(0)
    expect(document.press('1', { ctrl: true }).defaultPrevented).toBe(true)
    expect(document.press('2', { ctrl: true }).defaultPrevented).toBe(true)
    expect(document.activeElement).toBeNull()
  })

  it('selects the row a shortcut key names by position, and reads the platform’s modifier', async () => {
    // Control + digit elsewhere: this suite's scope carries no navigator, and a scope with
    // none is not macOS (`isMacPlatform`), which is the same answer a `vm` realm gives.
    const { bridge } = fakeBridge({})
    const document = await boot(bridge, new FakeDocument())
    const row = railRow(document)
    // **The first row's key is pressed as AZERTY produces it**: the code is the number row's
    // and the character is `&`, which is the keystroke the hint asks for on that layout and
    // the one a mapping reading `key` would drop.
    const pressed = document.press('&', { ctrl: true }, 'Digit1')
    expect(pressed.defaultPrevented, 'the console left its own shortcut key to the window').toBe(true)
    expect(row.attributes.get('aria-selected')).toBe('true')
    expect(row.attributes.get('tabindex')).toBe('0')
    expect(document.activeElement, 'the shortcut did not reach the row it named').toBe(row)
    // A position no row is drawn at addresses nothing -- and the key is still one of the
    // three the design grants, so it is still prevented rather than left to the platform.
    expect(document.press('2', { ctrl: true }, 'Digit2').defaultPrevented).toBe(true)
    expect(row.attributes.get('aria-selected')).toBe('true')
    // Command + digit is macOS's spelling and not this platform's, so it is not the
    // console's key here: nothing is taken and nothing is selected.
    expect(document.press('1', { meta: true }, 'Digit1').defaultPrevented).toBe(false)
    // And the design's bound reaches the wiring, not just the mapping: a key above the
    // three rows is not taken at all, so the window keeps it. `Digit4` and `Numpad1` are the
    // two spellings the mapping refuses, and neither is prevented here.
    expect(document.press('4', { ctrl: true }, 'Digit4').defaultPrevented).toBe(false)
    expect(document.press('1', { ctrl: true }, 'Numpad1').defaultPrevented).toBe(false)
    expect(document.activeElement).toBe(row)
  })

  it('reads Command on macOS, where the platform’s modifier is the other one', async () => {
    const { bridge } = fakeBridge({})
    const document = await boot(bridge, new FakeDocument(), { platform: 'MacIntel' })
    const row = railRow(document)
    // The same key that is the console's elsewhere is not the console's here, and the one
    // that is not elsewhere is here -- both directions, because a rule that accepted either
    // modifier everywhere would pass a one-directional case.
    expect(document.press('1', { ctrl: true }, 'Digit1').defaultPrevented).toBe(false)
    expect(document.press('1', { meta: true }, 'Digit1').defaultPrevented).toBe(true)
    expect(document.activeElement).toBe(row)
    expect(row.attributes.get('aria-selected')).toBe('true')
  })

  it('keeps the rail’s three tab stops in the order a reader sees them', async () => {
    // The focus order is the DOM order and the rail is first (design section 4.3): the
    // collapse control, the list -- **one stop however many installations it holds**, which
    // is what the roving `tabindex` buys -- and the add row. Three, and in that order.
    const { bridge } = fakeBridge({})
    const document = await boot(bridge, new FakeDocument())
    const rail = railOf(document)
    const stops = everyNode(rail).filter(
      (node) => node.tagName === 'BUTTON' || node.attributes.has('tabindex'),
    )
    expect(stops.map((node) => node.attributes.get('class'))).toEqual([
      RAIL_COLLAPSE_CLASS,
      RAIL_ROW_CLASS,
      RAIL_ADD_CLASS,
    ])
    // Exactly one of them is the roving stop and it is the selected row: a list with no `0`
    // at all is a list the keyboard cannot enter, and two of them is a list that is two
    // stops.
    expect(stops.filter((node) => node.attributes.get('tabindex') === '0')).toEqual([railRow(document)])
  })
})

describe('the keyboard’s own rules, apart from the DOM', () => {
  it('moves within a tablist by the axis it is given, and wraps at both ends', () => {
    // The navigation is horizontal and the rail's list vertical (design sections 4.1 and
    // 4.3), and the axis is a parameter rather than two functions so the one rule -- which
    // key is the next tab -- is written once.
    expect(tabTarget('ArrowRight', 0, 3, 'x')).toBe(1)
    expect(tabTarget('ArrowRight', 2, 3, 'x')).toBe(0)
    expect(tabTarget('ArrowLeft', 0, 3, 'x')).toBe(2)
    expect(tabTarget('ArrowLeft', 2, 3, 'x')).toBe(1)
    expect(tabTarget('ArrowDown', 0, 3, 'y')).toBe(1)
    expect(tabTarget('ArrowUp', 0, 3, 'y')).toBe(2)
    expect(tabTarget('Home', 2, 3, 'y')).toBe(0)
    expect(tabTarget('End', 0, 3, 'x')).toBe(2)
    // The other axis's arrows are not this tablist's, and neither is any other key.
    expect(tabTarget('ArrowDown', 0, 3, 'x')).toBeNull()
    expect(tabTarget('ArrowRight', 0, 3, 'y')).toBeNull()
    expect(tabTarget('Enter', 0, 3, 'x')).toBeNull()
    expect(tabTarget('a', 0, 3, 'x')).toBeNull()
    // An empty tablist, and an index outside it, move nowhere.
    expect(tabTarget('ArrowRight', 0, 0, 'x')).toBeNull()
    expect(tabTarget('ArrowRight', 3, 3, 'x')).toBeNull()
  })

  it('gives a tablist one tab stop, and the same one answer to both tablists', () => {
    // The roving half of the pattern (design sections 4.1 and 4.3): one tab of
    // the list is in the tab order and every tab beside it is not. The rail's
    // list holds one row today, so a rail that stopped roving would still draw
    // exactly one stop -- this is where that rule is held, and the rail's own
    // case above is where its *write* is held.
    expect(tabStop(true)).toBe('0')
    expect(tabStop(false)).toBe('-1')
  })

  it('spells the shortcut key the platform’s way, and answers the row’s position', () => {
    // **The case the rail's keys are proved by** (the plan's Task 6.5): a digit is the row's
    // *position*, one-based, so `1` is the first row the rail draws and an off-by-one selects
    // the wrong row -- which is what every assertion below would catch.
    expect(shortcutRow(keyEvent('1', { ctrl: true }), false)).toBe(0)
    expect(shortcutRow(keyEvent('2', { ctrl: true }), false)).toBe(1)
    expect(shortcutRow(keyEvent('3', { ctrl: true }), false)).toBe(2)
    expect(shortcutRow(keyEvent('1', { meta: true }), true)).toBe(0)
    // Both halves of the platform rule: the modifier that is not this platform's is not a
    // second spelling, and a digit held with both is a key two rules claim.
    expect(shortcutRow(keyEvent('1', { meta: true }), false)).toBeNull()
    expect(shortcutRow(keyEvent('1', { ctrl: true }), true)).toBeNull()
    expect(shortcutRow(keyEvent('1', { meta: true, ctrl: true }), true)).toBeNull()
    expect(shortcutRow(keyEvent('1', { meta: true, ctrl: true }), false)).toBeNull()
    // No modifier at all is the plain digit, which belongs to the document and not to this.
    expect(shortcutRow(keyEvent('1'), false)).toBeNull()
    expect(shortcutRow(keyEvent('1'), true)).toBeNull()
    // `0` addresses no row: the hint's own sentence is "1 2 3", and a zero-based key would
    // be the off-by-one in the other direction.
    expect(shortcutRow(keyEvent('0', { ctrl: true }, 'Digit0'), false)).toBeNull()
    expect(shortcutRow(keyEvent('a', { ctrl: true }, 'KeyA'), false)).toBeNull()
    expect(shortcutRow(keyEvent('Enter', { ctrl: true }, 'Enter'), false)).toBeNull()
  })

  it('reads the physical key, so the operator’s layout does not spell the shortcut away', () => {
    // **The code is what is read and the character is not** (design section 4.1 grants
    // Command/Control + *digit*): `code` names the key the finger is on and `key` is what the
    // layout produces from it, so the first row's key answers the first row on AZERTY -- where
    // the unshifted `1` key produces `&` and a mapping that read `key` would do nothing at all
    // to the very keystroke the rail's hint tells the operator to press.
    expect(shortcutRow(keyEvent('&', { ctrl: true }, 'Digit1'), false)).toBe(0)
    expect(shortcutRow(keyEvent('"', { ctrl: true }, 'Digit3'), false)).toBe(2)
    expect(shortcutRow(keyEvent('&', { meta: true }, 'Digit1'), true)).toBe(0)
    // And the other direction: a character that *is* a digit is not the console's key when the
    // physical key is not the number row's, which is what makes this a read of `code` rather
    // than a read of `key` with a second spelling added.
    expect(shortcutRow(keyEvent('1', { ctrl: true }, 'KeyQ'), false)).toBeNull()
    expect(shortcutRow(keyEvent('2', { ctrl: true }, 'Digit9'), false)).toBeNull()
    // `Numpad1` is a different physical key and **not** a spelling of `Digit1`: the design
    // grants the number row's digits and names no keypad key, so admitting one would be
    // inventing a binding no ruling made.
    expect(shortcutRow(keyEvent('1', { ctrl: true }, 'Numpad1'), false)).toBeNull()
    // An event with no code at all answers nothing rather than throwing.
    expect(shortcutRow(keyEvent('1', { ctrl: true }, ''), false)).toBeNull()
  })

  it('addresses the three rows the design grants and no fourth', () => {
    // Design section 4.1 grants **Command-1 to Command-3** on macOS and **Control-1 to
    // Control-3** elsewhere -- "the first three rows the rail draws", which is the hint's own
    // sentence ("Switch with the shortcut keys 1 2 3") -- so keys 4 to 9 are not the console's
    // key: they are neither selected nor prevented, and the window keeps them.
    expect(SHORTCUT_ROWS).toBe(3)
    for (const position of [1, 2, 3]) {
      expect(
        shortcutRow(keyEvent(String(position), { ctrl: true }, `Digit${position}`), false),
        `Control-${position} is one of the three keys the design grants`,
      ).toBe(position - 1)
    }
    for (const position of [4, 5, 6, 7, 8, 9]) {
      expect(
        shortcutRow(keyEvent(String(position), { ctrl: true }, `Digit${position}`), false),
        `Control-${position} addresses a row the design grants no key for`,
      ).toBeNull()
      expect(shortcutRow(keyEvent(String(position), { meta: true }, `Digit${position}`), true)).toBeNull()
    }
  })

  it('reads the platform out of the navigator the scope carries', () => {
    // Both spellings the read accepts, and the two answers it must not invent: a navigator
    // with neither field, and no navigator at all (a `vm` realm, this suite).
    expect(isMacPlatform({ userAgentData: { platform: 'macOS' } })).toBe(true)
    expect(isMacPlatform({ platform: 'MacIntel' })).toBe(true)
    expect(isMacPlatform({ userAgentData: { platform: 'Windows' } })).toBe(false)
    expect(isMacPlatform({ platform: 'Linux x86_64' })).toBe(false)
    expect(isMacPlatform({ userAgentData: {}, platform: 'Win32' })).toBe(false)
    expect(isMacPlatform({})).toBe(false)
    expect(isMacPlatform(undefined)).toBe(false)
    expect(isMacPlatform(null)).toBe(false)
    // The modern spelling wins where both are there: `platform` is deprecated and answers
    // `MacIntel` on an iPad, which is the one case where the two disagree about the machine.
    expect(isMacPlatform({ userAgentData: { platform: 'Windows' }, platform: 'MacIntel' })).toBe(false)
    // **A modern spelling that is present and blank is not an answer**, which is what the
    // fallback is really for: `userAgentData.platform` is an empty string where the hints
    // are unavailable or reduced, and a read that took "there is a string" for "there is a
    // platform" would call a Mac running such a build "not macOS" and spell the shortcut
    // keys the other platform's way. The same for a blank legacy name, which is not macOS
    // either.
    expect(isMacPlatform({ userAgentData: { platform: '' }, platform: 'MacIntel' })).toBe(true)
    expect(isMacPlatform({ userAgentData: { platform: '' }, platform: 'Windows' })).toBe(false)
    expect(isMacPlatform({ userAgentData: { platform: '' }, platform: '' })).toBe(false)
    expect(isMacPlatform({ userAgentData: { platform: '' } })).toBe(false)
  })
})

/** THEME_PREFERENCE_SPELLINGS is every way a renderer source could ask the
 * machine which theme to draw, in the order a report lists them. The first two
 * are the web's answer (`matchMedia` and the query it is asked), the next three
 * are Electron's (`nativeTheme`, the property a renderer would set, and the
 * colour-scheme boolean it would read), and the last is the console's own
 * spelling for "which set did the operator pick" -- the value Q1 ruled out, so
 * that a `themeChoice()` added later is caught by this scan rather than by a
 * review. */
const THEME_PREFERENCE_SPELLINGS = [
  'matchMedia',
  'prefers-color-scheme',
  'nativeTheme',
  'themeSource',
  'shouldUseDarkColors',
  'themeChoice',
] as const

/** withoutComments is a source with its comments blanked, so a scan over it
 * reads the *code* rather than the prose. The files under `src/renderer/` name
 * the ruling they obey in their comments -- `entry.ts` says in as many words
 * that no `matchMedia` read comes with the theme literal -- and a scan that read
 * the text would fail on its own explanation. The `//` is only a comment where
 * it is not part of a URL, which is what the character before it is for. */
function withoutComments(source: string): string {
  return source
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/(^|[^:])\/\/[^\n]*/gm, '$1')
}

/** preferenceReads is the spellings a source uses, in a fixed order. */
function preferenceReads(source: string): readonly string[] {
  return THEME_PREFERENCE_SPELLINGS.filter((spelling) => source.includes(spelling))
}

describe('the console’s theme', () => {
  it('is one literal, and no renderer source reads a preference of its own', () => {
    // Task 6.5's first case (design section 3.6, the owner's Q1 ruling of 2026-10-19): one
    // value set ships, dark, so `CONSOLE_THEME` is a literal -- not the answer of a
    // `themeChoice()` -- and nothing under `src/renderer/` asks the machine which theme to
    // draw. The scan is over the directory rather than over `entry.ts`, because the second
    // place a preference could arrive is a screen, and the theme directory's own scan
    // (`theme-tokens.test.ts`) covers the CSS half of the same ruling.
    expect(CONSOLE_THEME).toBe('dark')
    const files = everySource(join(CONSOLE_ROOT, 'src', 'renderer'))
    expect(files.length, 'the scan found no renderer sources: it would pass by finding nothing').toBeGreaterThan(5)
    const offences = files.flatMap((file) =>
      preferenceReads(withoutComments(readFileSync(file, 'utf8'))).map(
        (spelling) => `${file.replace(`${CONSOLE_ROOT}/`, '')}: ${spelling}`,
      ),
    )
    expect(offences, 'a renderer source reads the theme from the machine').toEqual([])

    // The probe: the scan is only worth what it finds, so both halves of it are read off a
    // snippet that carries them -- one in code, one in a comment, since the prose of these
    // files names the ruling it obeys and a scan that read the text would fail on its own
    // explanation.
    expect(
      preferenceReads(
        withoutComments(
          "const dark = matchMedia('(prefers-color-scheme: dark)')\n// nativeTheme.themeSource\n",
        ),
      ),
    ).toEqual(['matchMedia', 'prefers-color-scheme'])
  })
})

// The rail over the list read (owner's design ruling of 2026-10-25: no picker
// screen; every installation in the rail; a warning mark and an issue view for
// the ones the console cannot read).
// ---------------------------------------------------------------------------

/** listRow is one `installation.list` row as the main process answers it. */
function listRow(
  project: string,
  options: {
    readonly open?: boolean
    readonly issue?: 'none' | 'config' | 'stale' | 'store' | 'binary'
    readonly readable?: boolean
    readonly sentence?: string
    readonly command?: string
    readonly details?: readonly string[]
    readonly checkouts?: readonly string[]
  } = {},
): Record<string, unknown> {
  return {
    project,
    open: options.open ?? false,
    active: options.open ?? false,
    saved: true,
    checkouts: options.checkouts ?? [],
    state: {
      issue: options.issue ?? 'none',
      readable: options.readable ?? true,
      sentence: options.sentence ?? '',
      command: options.command ?? '',
      details: options.details ?? [],
    },
  }
}

const ORBIT_SENTENCE = 'edit ~/.config/fugaro/projects/orbit.yaml: unknown field. Then run fugaro doctor --project orbit.'
const ORBIT_LINE = '~/.config/fugaro/projects/orbit.yaml could not be read: unknown field'

function orbitRow(open = false): Record<string, unknown> {
  return listRow('orbit', {
    open,
    issue: 'config',
    readable: false,
    sentence: ORBIT_SENTENCE,
    command: 'fugaro doctor --project orbit',
    details: [ORBIT_LINE],
    checkouts: ['/home/operator/src/orbit'],
  })
}

/** railBridge is `fakeBridge` with the rail's six operations, each recorded. */
function railBridge(
  rows: readonly Record<string, unknown>[],
  answers: Readonly<Record<string, AReply>> = {},
): FakeBridge & { readonly rail: readonly { readonly name: string; readonly args: unknown }[] } {
  const frame = syntheticDocuments()[0] as Readonly<Record<string, unknown>>
  const base = fakeBridge({ replies: { subscribeLive: () => snapshotReply(frame, nowStamp()) } })
  const rail: { name: string; args: unknown }[] = []
  for (const name of [
    'listInstallations',
    'chooseInstallation',
    'setInstallationCheckout',
    'removeInstallation',
    'revealInstallation',
    'openInstallations',
  ]) {
    base.bridge[name] = (args: unknown): unknown => {
      rail.push({ name, args })
      const answer = answers[name]
      if (answer !== undefined) return answer(args)
      return name === 'listInstallations' ? { ok: true, data: { rows } } : { ok: true, data: null }
    }
  }
  return { ...base, rail }
}

function rowsOf(document: FakeDocument): readonly FakeNode[] {
  return classed(railOf(document), RAIL_ROW_CLASS)
}

function rowNamed(document: FakeDocument, project: string): FakeNode {
  const row = rowsOf(document).find((each) => classed(each, RAIL_NAME_CLASS)[0]?.textContent === project)
  if (row === undefined) throw new Error(`the rail drew no row for ${project}`)
  return row
}

function buttonLabelled(root: FakeNode, label: string): FakeNode {
  const button = everyNode(root).find((node) => node.tagName === 'BUTTON' && node.textContent === label)
  if (button === undefined) throw new Error(`no button reads ${label}`)
  return button
}

describe('the rail over the installation list', () => {
  it('draws every listed installation, readable or not, and marks the one with an issue', async () => {
    const { bridge, rail, calls } = railBridge([listRow('aurora', { open: true }), orbitRow()])
    const document = await boot(bridge, new FakeDocument())
    expect(rowsOf(document).map((row) => classed(row, RAIL_NAME_CLASS)[0]?.textContent)).toEqual(['aurora', 'orbit'])
    expect(rail.map((call) => call.name)).toEqual(['listInstallations'])
    // The readable one is open and selected, and the dashboard read it.
    expect(rowNamed(document, 'aurora').attributes.get('aria-selected')).toBe('true')
    expect(calls.map((call) => call.name)).toEqual(['subscribeLive'])
    // The mark is text with a name, never the tone alone (design section 3.5).
    expect(classed(rowNamed(document, 'aurora'), RAIL_WARNING_CLASS)).toHaveLength(0)
    const mark = classed(rowNamed(document, 'orbit'), RAIL_WARNING_CLASS)[0]
    expect(mark, 'the unreadable row drew no warning mark').toBeDefined()
    expect(mark?.textContent).toBe(RAIL_WARNING_GLYPH)
    expect(mark?.attributes.get('role')).toBe('img')
    expect(mark?.attributes.get('aria-label')).toBe(`warning: ${ORBIT_SENTENCE}`)
    // Only the open row is being read; the other says idle.
    expect(classed(rowNamed(document, 'orbit'), RAIL_STATE_CLASS)[0]?.attributes.get('data-state')).toBe('idle')
  })

  it('opens a row’s issue in the main area: the sentence first, the CLI’s text behind details, the actions inside', async () => {
    const { bridge, rail } = railBridge([listRow('aurora', { open: true }), orbitRow()], {
      removeInstallation: () => ({ ok: true, data: { rows: [listRow('aurora', { open: true })] } }),
    })
    const document = await boot(bridge, new FakeDocument())
    rowNamed(document, 'orbit').click()
    await flush()
    const issue = classed(panelOf(document), ISSUE_CLASS)[0]
    expect(issue, 'the click drew no issue view').toBeDefined()
    const view = issue as FakeNode
    expect(view.children[0]?.tagName).toBe('H1')
    expect(view.children[0]?.textContent).toBe('orbit')
    // One plain sentence first, then the exact command.
    expect(view.children[1]?.attributes.get('class')).toBe(ISSUE_SENTENCE_CLASS)
    expect(view.children[1]?.textContent).toBe(ORBIT_SENTENCE)
    expect(classed(view, ISSUE_COMMAND_CLASS)[0]?.textContent).toBe('fugaro doctor --project orbit')
    expect(classed(view, ISSUE_SUMMARY_CLASS)[0]?.textContent).toBe(ISSUE_DETAILS_LABEL)
    expect(classed(view, ISSUE_LINES_CLASS)[0]?.textContent).toBe(ORBIT_LINE)
    // A row the console cannot read offers no switch; it offers the old
    // Installations screen's actions instead.
    expect(everyNode(view).some((node) => node.textContent === ISSUE_CHOOSE_LABEL)).toBe(false)
    expect(rowNamed(document, 'orbit').attributes.get('aria-selected')).toBe('true')
    expect(rowNamed(document, 'aurora').attributes.get('aria-selected')).toBe('false')

    buttonLabelled(view, `${ISSUE_CHECKOUT_LABEL}: /home/operator/src/orbit`).click()
    await flush()
    expect(rail.at(-1)).toEqual({
      name: 'setInstallationCheckout',
      args: { project: 'orbit', checkout: '/home/operator/src/orbit' },
    })

    const current = classed(panelOf(document), ISSUE_CLASS)[0] as FakeNode
    buttonLabelled(current, ISSUE_REMOVE_LABEL).click()
    await flush()
    expect(rail.at(-1)).toEqual({ name: 'removeInstallation', args: { project: 'orbit' } })
    // The row is gone, so its issue is too: the main area is the dashboard again.
    expect(rowsOf(document)).toHaveLength(1)
    expect(classed(panelOf(document), ISSUE_CLASS)).toHaveLength(0)
    expect(rowNamed(document, 'aurora').attributes.get('aria-selected')).toBe('true')
  })

  it('switches on a click on a readable row, and draws the refusal when the switch is refused', async () => {
    const { bridge, rail } = railBridge([listRow('aurora', { open: true }), listRow('comet')], {
      chooseInstallation: () => ({ ok: false, reason: 'the CLI for comet is too old' }),
    })
    const document = await boot(bridge, new FakeDocument())
    rowNamed(document, 'comet').click()
    await flush()
    expect(rail.at(-1)).toEqual({ name: 'chooseInstallation', args: { project: 'comet' } })
    expect(textOf(panelOf(document))).toContain('the CLI for comet is too old')
  })

  it('opens on the first row’s issue, and reads nothing, when no installation can be read', async () => {
    const { bridge, calls } = railBridge([orbitRow(true)])
    const document = await boot(bridge, new FakeDocument())
    expect(classed(panelOf(document), ISSUE_CLASS)).toHaveLength(1)
    expect(calls).toEqual([])
    // The nav still leads somewhere: Live reads on the way in.
    buttonFor(document, 'live').click()
    await flush()
    expect(calls.map((call) => call.name)).toEqual(['subscribeLive'])
    expect(classed(panelOf(document), ISSUE_CLASS)).toHaveLength(0)
  })

  it('opens the picker window from the + row, which is the one way to add an installation', async () => {
    const { bridge, rail } = railBridge([listRow('aurora', { open: true })])
    const document = await boot(bridge, new FakeDocument())
    const add = classed(railOf(document), RAIL_ADD_CLASS).find((node) => node.tagName === 'BUTTON')
    add?.click()
    await flush()
    expect(rail.at(-1)).toEqual({ name: 'openInstallations', args: {} })
  })
})
