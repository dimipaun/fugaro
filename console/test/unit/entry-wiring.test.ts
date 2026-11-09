// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
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
  LIVE_RIGOR_TTL_MS,
  RAIL_ADD_CLASS,
  RAIL_CLASS,
  RAIL_HEAD_CLASS,
  RAIL_HINT_CLASS,
  RAIL_LIST_CLASS,
  RAIL_NAME_CLASS,
  RAIL_PROJECT_CLASS,
  RAIL_ROW_CLASS,
  RAIL_SPEND_CLASS,
  RAIL_STATE_CLASS,
  RAIL_WARNING_CLASS,
  RAIL_WARNING_GLYPH,
  RAIL_EMPTY_CLASS,
  RAIL_TITLE_CLASS,
  RAIL_TITLE_ID,
  ISSUE_HEADLINE_CLASS,
  ONBOARDING_CLASS,
  ONBOARDING_COMMAND_CLASS,
  ONBOARDING_TITLE_CLASS,
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
  GAUGE_CAP_CLASS,
  GAUGE_CLASS,
  GAUGE_FILL_CLASS,
  GAUGE_LABEL_CLASS,
  GAUGE_NOTIONAL_CLASS,
  HALT_CLASS,
  SHELL_HEADER_CLASS,
  SHELL_PAGE_CLASS,
  SHELL_WORDMARK_CLASS,
  SWITCHER_BUTTON_CLASS,
  SWITCHER_CLASS,
  SWITCHER_DOT_CLASS,
  SWITCHER_MENU_CLASS,
  SWITCHER_NAME_CLASS,
  SWITCHER_VERSION_CLASS,
  COORDINATOR_SUMMARY_SINCE,
  GRAPH_BUSY,
  GRAPH_SCOPE_BUSY,
  RUNS_SINCE,
  SETTINGS_EDITOR_CLASS,
  SETTINGS_EDIT_OUTCOME_CLASS,
  VIEW_PANEL_CLASS,
  VIEWS,
  bootDashboard,
  isMacPlatform,
  SHORTCUT_ROWS,
  shortcutRow,
  tabStop,
  tabTarget,
} from '../../src/renderer/entry'
import { GRAPH_DOCUMENT_VERSION, decodeSnapshot } from '../../src/shared/schema'
import { ADDRESS_PLACEHOLDER } from '../../src/shared/sanitize'
import { NO_PLAN_CHECKOUT } from '../../src/source/source'
import { READING_STILL, READING_STILL_MS, READING_WORDS } from '../../src/renderer/screens/reading'
import {
  NO_CHECKOUT_AUTO_SENTENCE,
  NO_CHECKOUT_CHOICE_LABEL,
  NO_CHECKOUT_EMPTY_SENTENCE,
  NO_CHECKOUT_HEADLINE,
  NO_CHECKOUT_KIND,
  NO_CHECKOUT_SCOPE_CLASS,
  NO_CHECKOUT_SCOPE_LABEL,
  NO_CHECKOUT_USE_LABEL,
} from '../../src/renderer/screens/nocheckout'
import { CHECKOUT_SELECT_CLASS, checkoutLabel } from '../../src/renderer/checkout'
import { GRAPH_SCOPE_ALL, GRAPH_SCOPE_PLAN } from '../../src/renderer/screens/graph'
import {
  BUSY_ATTRIBUTE,
  BUSY_BUSY_ATTRIBUTE,
  BUSY_CLASS,
  BUSY_LABEL_CLASS,
  BUSY_SPINNER_CLASS,
  BUSY_STILL_CLASS,
  CACHE_FRESH_MS,
  STAMP_AT_CLASS,
  STAMP_CLASS,
  STAMP_REFRESH_CLASS,
  STAMP_REFRESH_LABEL,
} from '../../src/renderer/section'
import {
  COORDINATOR_SHAPE,
  PROJECTS_SHAPE,
  PROJECTS_MODELS_SHAPE,
  SKELETON_BLOCK_CLASS,
  SKELETON_CLASS,
  SKELETON_SHAPES,
} from '../../src/renderer/skeleton'
import {
  COORDINATOR_BUSY,
  COORDINATOR_DESCRIPTION_BUSY,
  COORDINATOR_DESCRIPTION_CLASS,
  COORDINATOR_DESCRIPTION_HIDE_WORD,
  COORDINATOR_DESCRIPTION_LINE_CLASS,
  COORDINATOR_DESCRIPTION_SHOW_WORD,
  COORDINATOR_LAUNCH_WORD,
  COORDINATOR_NO_AGE_WORD,
  COORDINATOR_OPEN_SPEND_WORD,
  COORDINATOR_QUEUE_BUSY,
  COORDINATOR_QUEUE_JOBS_CLASS,
  COORDINATOR_QUEUE_JOBS_WORD,
  COORDINATOR_QUEUE_PART_CLASS,
  COORDINATOR_QUEUE_REPO_KILL_WORD,
  COORDINATOR_RETRY_WORD,
  COORDINATOR_SOURCE_LINE,
  COORDINATOR_TOTALS_BUSY,
  COORDINATOR_TRANSCRIPT_BUSY,
  COORDINATOR_WINDOW_BUSY,
  COORDINATOR_WINDOW_FLOOR_WORD,
  COORDINATOR_WINDOW_RUNS_WORD,
  COORDINATOR_WINDOW_SLOT_CLASS,
  COORDINATOR_WINDOW_SUCCEEDED_WORD,
  COORDINATOR_TRANSCRIPT_CLASS,
  COORDINATOR_TRANSCRIPT_WORD,
  COORDINATOR_JOBS_BUSY,
  COORDINATOR_JOBS_CLASS,
  COORDINATOR_JOBS_EMPTY_WORD,
  COORDINATOR_JOBS_FLOOR_WORD,
  COORDINATOR_JOBS_HEAD_CLASS,
  COORDINATOR_JOBS_LIST_CLASS,
  COORDINATOR_JOBS_LOAD_WORD,
  COORDINATOR_JOBS_OLDER_WORD,
  COORDINATOR_JOBS_PAGE,
  COORDINATOR_JOBS_RUNS_WORD,
  COORDINATOR_JOBS_WINDOW_WORD,
  COORDINATOR_JOB_DETAIL_CLASS,
} from '../../src/renderer/screens/coordinator'
import { shortRun } from '../../src/shared/shortid'
import {
  PROJECTS_ACCOUNT_KEYS_CLASS,
  PROJECTS_BUSY,
  PROJECTS_CREDENTIALS_SERVICE_CLASS,
  PROJECTS_MANAGED_CLASS,
  PROJECTS_MODELS_BUSY,
  PROJECTS_RETIREMENTS_CLASS,
  PROJECTS_SETTINGS_BUSY,
  SETTINGS_PENCIL_CLASS,
} from '../../src/renderer/screens/projects'
import { ACCOUNT_KEYS_BUSY, ACCOUNT_KEYS_TITLE } from '../../src/renderer/screens/accountkeys'
import {
  CREDENTIALS_SERVICE_BUSY,
  CREDENTIALS_SERVICE_TITLE,
} from '../../src/renderer/screens/credentialsservice'
import { MANAGED_BUSY, MANAGED_TITLE } from '../../src/renderer/screens/managed'
import { POLICY_NODE_TITLE } from '../../src/renderer/screens/policynotes'
import { RETIREMENTS_BUSY, RETIREMENTS_SET_TITLE } from '../../src/renderer/screens/retirements'
import type { SkeletonShape } from '../../src/renderer/skeleton'
import { WRITE_UNAVAILABLE } from '../../src/renderer/writes'
import {
  LAUNCH_FORM_CLASS,
  LAUNCH_OPEN_CLASS,
  LAUNCH_PLAN_TASK_CLASS,
  LAUNCH_REPOSITORY_CLASS,
  LAUNCH_SUBMIT_CLASS,
  LAUNCH_TASK_CLASS,
  LAUNCH_TITLE_CLASS,
} from '../../src/renderer/screens/launch'
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

/**
 * READ_METHODS is the eighteen reads `entry.ts` may reach for (`ReadBridge`'s
 * own list less `summaryRuns`, which `queueBridge` answers with its own
 * recorder), by the name the preload exposes. Every one of them is on the
 * fake bridge below, and every one records its call -- so "this read was never
 * made" is a claim this suite can make about any of them.
 *
 * `graphRead` is the graph plan's CG9a read, and it is the fifteenth: the
 * Graph's second scope is read through it, and a case that drove the Graph with
 * a bridge that could not answer it would be driving a console whose only
 * remaining scope is the plan's. `managedStatus`, `policyShow` and
 * `environmentReport` are the managed-project screens plan's CM4 rows, and they
 * are the last three: the Project tab's later sections read them (CM10).
 */
const READ_METHODS = [
  'subscribeLive',
  'showBudget',
  'runReport',
  'listRuns',
  'statsRuns',
  'listModels',
  'readSettings',
  'coordinatorStatus',
  'coordinatorShow',
  'coordinatorIntents',
  'diagnoseRun',
  'readRunLogs',
  'runLogLink',
  'planStatus',
  'graphRead',
  'managedStatus',
  'policyShow',
  'environmentReport',
] as const

/** A_REPLY is a bridge method's answer. */
type AReply = (args: unknown) => unknown

/** FakeBridge is the preload seam, recorded: the handshake, the installation,
 * and the fifteen reads (`READ_METHODS`), each answering what the case told it to
 * and appending its own call to `calls`. */
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

/** policyPanelOf is the policy panel of the Project tab: a member of the
 * `PROJECT_PANEL_KEYS` the body draws, found by its own `data-panel` rather than
 * by a class hook, because the tab's panels all wear the same shell (CM10). */
function policyPanelOf(document: FakeDocument): FakeNode {
  const panel = classed(panelOf(document), 'projects-panel').find(
    (node) => node.attributes.get('data-panel') === 'policy',
  )
  if (panel === undefined) throw new Error('the entry drew no policy panel')
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
 * promise, and a drawing lands a turn after the call that asked for it. The
 * turns are counted rather than waited for, as `confirm.test.ts`'s own twelve
 * are, so an assertion is about order and never about timing -- and there are
 * twenty-four of them because the deepest chain here is the Graph's: the plan
 * read refuses for an installation that records no checkout, the coordinator
 * read follows it, and its drawing lands after that. */
async function flush(): Promise<void> {
  for (let i = 0; i < 24; i += 1) await Promise.resolve()
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
    // The banner is drawn, and the navigation offers Live, Graph and
    // Coordinator: `spend` is withheld and `projects` is the `caps` view, which
    // a reduced mode withholds with it. A Spend entry here would be a door to a
    // screen that cannot read. The Graph is offered because `plan.status` is not
    // a view this mode withholds (GV2.1's decision, and `VIEWS`' own `null`): it
    // draws no spend and no caps. `coordinator` is offered because the project
    // view is repository state rather than money, so the mode withholds no
    // figure of it (Bitbucket credentials T1.2).
    expect(document.body.children[0]?.attributes.get('data-mode')).toBe('reduced')
    expect(navButtons(document).map((button) => button.attributes.get('data-view'))).toEqual([
      'live',
      'graph',
      'coordinator',
    ])
    expect(navButtons(document).map((button) => button.attributes.get('class'))).toEqual([
      VIEW_BUTTON_CLASS,
      VIEW_BUTTON_CLASS,
      VIEW_BUTTON_CLASS,
    ])
    // And the two reads behind the withheld views are not made -- not on the
    // first drawing, and not on a poll. `listRuns` IS made, and it is not one of
    // them: it is the run list, which a reduced mode keeps (the run list, the
    // stages, the ages and the deadlines are fields `watch` has always carried,
    // `ReducedView`'s own doc comment), and the Live screen reads it for the
    // rigor join its rows draw a chip from. What is withheld is `runReport` and
    // `showBudget`, and the two assertions below are the ones that say so.
    expect(calls.map((call) => call.name)).toEqual(['subscribeLive', 'listRuns'])
    await flush()
    expect(calls.map((call) => call.name)).toEqual(['subscribeLive', 'listRuns'])
    expect(calls.some((call) => call.name === 'runReport')).toBe(false)
    expect(calls.some((call) => call.name === 'showBudget')).toBe(false)
  })

  it('draws the unavailable state, and no figure, for a live.watch reply it cannot decode', async () => {
    // A document the schema refuses: a field it reads arrives retyped, which is
    // a format change and not an addition (a key it does not know is ignored).
    // The console's answer is the refusal screen with the schema's own sentence
    // naming the read and the field -- never a dashboard of zeros, which is what
    // a screen handed an unread document would draw.
    const unknown = { ...(syntheticDocuments()[0] as Record<string, unknown>), now: 1 }
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
    buttonFor(document, 'spend').click()
    await flush()

    // The refusal is the report section's own, inside the frame: the caps are a
    // second read of the same screen, and one that refuses is not a reason to
    // take the other's section away.
    const report = sectionOf(document, 'spend-report')
    const drawn = classed(report, 'console-unavailable')[0] as FakeNode
    expect(drawn, 'the report refused without drawing a refusal in its own section').toBeDefined()
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
    expect(rows.map((row) => classed(row, 'live-run-id')[0]?.textContent)).toEqual(['aa01', 'aa03'])

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

  it('goes back to Live from the Run screen’s own breadcrumb crumb', async () => {
    // The breadcrumb's `Live` crumb is the way back from a run the operator
    // opened, and it is the one listener the Run screen asks for: `showRun`
    // binds it to the same `show('live')` the nav's own Live button calls. The
    // crumb is a real `button` rather than a navigating element, because the
    // console navigates with buttons and the window refuses everything else.
    const frame = syntheticDocuments()[syntheticDocuments().length - 1] as Readonly<Record<string, unknown>>
    const { bridge } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(frame),
        diagnoseRun: () => ({ ok: true, data: { document: {} } }),
        readRunLogs: () => ({ ok: true, data: { document: { entries: [], total: 0, refused: 0 } } }),
      },
    })
    const document = await boot(bridge, new FakeDocument())
    const panel = panelOf(document)
    const row = classed(panel, 'live-run')[0] as FakeNode
    row.click()
    await flush()
    const crumb = classed(panel, 'run-crumb-live')[0]
    expect(crumb?.tagName, 'the Run screen drew no Live crumb to click').toBe('BUTTON')

    crumb?.click()
    await flush()
    expect(
      (panel.children[0] as FakeNode).attributes.get('class'),
      'the crumb left the Run screen',
    ).toBe('live')
    expect(classed(panel, 'run-crumb-live')).toEqual([])
    // And the crumb is bound to the view and to nothing else: the run it left
    // is not read again by going back.
    expect(classed(panel, 'live-run')).not.toEqual([])
  })

  it('opens the whole run id from a row whose RUN cell draws only its ending', async () => {
    // **The door reads the cell's `data-run-id` and not its text** (spec part 1,
    // reconciliation F8, D1707). A colliding pair is the case that makes the
    // difference visible: neither row's drawn ending is a run id at all, so a
    // door still reading the text would ask the CLI about a run nobody named.
    const frame = syntheticDocuments()[syntheticDocuments().length - 1] as Readonly<Record<string, unknown>>
    const recorded = frame['runs'] as readonly Readonly<Record<string, unknown>>[]
    const ids = ['20261108-115248-3bb2', '20261107-010101-3bb2']
    const runs = recorded.map((run, index) => ({ ...run, run: ids[index] ?? run['run'] }))
    const { bridge, calls } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply({ ...frame, runs }),
        // The two reads the Run screen is drawn from answer empty documents:
        // this case is about which run the row asked about.
        diagnoseRun: () => ({ ok: true, data: { document: {} } }),
        readRunLogs: () => ({ ok: true, data: { document: { entries: [], total: 0, refused: 0 } } }),
      },
    })
    const document = await boot(bridge, new FakeDocument())
    const panel = panelOf(document)
    const rows = classed(panel, 'live-run')
    expect(rows.map((row) => classed(row, 'live-run-id')[0]?.textContent)).toEqual(['8-3bb2', '1-3bb2'])

    ;(rows[0] as FakeNode).click()
    await flush()
    expect(calls.filter((call) => call.name === 'diagnoseRun').map((call) => call.args)).toEqual([
      { run: '20261108-115248-3bb2' },
    ])
  })

  it('does not open Run detail from a click on the Reap a lost row draws', async () => {
    // A lost run's row draws Reap through `disabledWrite`: `aria-disabled`
    // rather than `disabled`, so a keyboard reader reaches it and its click
    // fires and bubbles to the row's own listener. The row is a door to the
    // run, and a click that landed on the button is the button's.
    const frame = syntheticDocuments()[syntheticDocuments().length - 1] as Readonly<Record<string, unknown>>
    const { bridge, calls } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(frame),
        diagnoseRun: () => ({ ok: true, data: { document: {} } }),
        readRunLogs: () => ({ ok: true, data: { document: { entries: [], total: 0, refused: 0 } } }),
      },
    })
    const document = await boot(bridge, new FakeDocument())
    const panel = panelOf(document)
    const lost = classed(panel, 'live-run-lost')[0] as FakeNode
    const reap = classed(lost, 'live-reap')[0]
    expect(reap?.tagName, 'the lost row drew no Reap to click').toBe('BUTTON')

    lost.click(reap ?? null)
    await flush()
    expect(calls.filter((call) => call.name === 'diagnoseRun')).toEqual([])
    expect(calls.filter((call) => call.name === 'readRunLogs')).toEqual([])
    expect(
      (panel.children[0] as FakeNode).attributes.get('class'),
      'the Reap click left the Live screen',
    ).toBe('live')

    // And the rest of the row is still the door: a click on the id cell opens
    // the run it names.
    lost.click(classed(lost, 'live-run-id')[0] ?? null)
    await flush()
    expect(calls.filter((call) => call.name === 'diagnoseRun').map((call) => call.args)).toEqual([
      { run: '20261015-120000-aa01' },
    ])
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

  it('draws the refusal in the section that refused, not over the run’s own screen', async () => {
    // `diagnose` and `logs` are two reads of a run's own record, and each has
    // its own section: one refusing says nothing about the other. The log read
    // refusing here must not take the diagnosis down with it -- and it must
    // not be drawn as an empty window either, which would read as "this run
    // logged nothing" when the console could not ask.
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
    // The screen is still the run's: this is a section's refusal, not the
    // screen's.
    expect((panel.children[0] as FakeNode).attributes.get('class')).toBe('run')
    // The diagnosis the other read answered is up, and the column is no longer
    // reading.
    expect(classed(panel, 'run-stages')).toHaveLength(1)
    expect(classed(panel, 'run-main')[0]?.attributes.get('data-busy')).toBeUndefined()
    // The refusal is the log section's own, naming the read it came from.
    const logs = classed(panel, 'run-logs')[0] as FakeNode
    const drawn = classed(logs, 'console-unavailable')[0] as FakeNode
    expect(drawn, 'the refusal was not drawn in the log section').toBeDefined()
    expect(drawn.attributes.get('data-kind')).toBe('refused')
    expect(textOf(drawn)).toContain('no such run 20261015-120000-aa01')
    expect(classed(logs, 'unavailable-operation')[0]?.textContent).toBe('run.logs')
    // And nothing of the window was drawn: no log line that would read as an
    // empty one, and no replay figures about a window nobody read.
    expect(classed(panel, 'run-log-line')).toEqual([])
    expect(classed(panel, 'run-replay')).toEqual([])
  })

  it('draws a refused diagnosis over the log window the other read drew', async () => {
    // The same rule the other way round: the record read is the one that
    // refuses, and the log window that arrived before it stays on screen.
    const frame = syntheticDocuments()[syntheticDocuments().length - 1] as Readonly<Record<string, unknown>>
    const { bridge } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(frame),
        diagnoseRun: () => ({ ok: false, reason: 'no such run 20261015-120000-aa01' }),
        readRunLogs: () => ({
          ok: true,
          data: { document: { entries: [{ time: '2026-10-15T12:00:00Z', severity: 'info', stage: 'implement', message: 'the run started' }], total: 1, refused: 0 } },
        }),
      },
    })
    const document = await boot(bridge, new FakeDocument())
    const panel = panelOf(document)
    ;(classed(panel, 'live-run')[0] as FakeNode).click()
    await flush()
    expect((panel.children[0] as FakeNode).attributes.get('class')).toBe('run')
    expect(classed(panel, 'run-stages'), 'the empty ledger was drawn under a refused diagnosis').toEqual([])
    expect(classed(panel, 'unavailable-operation').map((node) => node.textContent)).toEqual(['run.diagnose'])
    expect(classed(panel, 'run-log-line')).toHaveLength(1)
    // The log read answered, so its replay panel is up -- the refused diagnosis
    // is the record's and has no say over what the window's read drew.
    expect(classed(panel, 'run-replay')).toHaveLength(1)
    expect(classed(panel, 'run-logs')[0]?.attributes.get('data-busy')).toBeUndefined()
  })

  describe('the frame a run is opened in', () => {
    /** opened boots the committed frame and clicks its first run row. The two
     * reads that draw a run answer what the case told them to, so a case can
     * hold them, land them in either order, or refuse one. */
    async function opened(replies: Readonly<Record<string, AReply>>): Promise<{
      readonly document: FakeDocument
      readonly panel: FakeNode
      readonly calls: readonly { readonly name: string; readonly args: unknown }[]
    }> {
      const frame = syntheticDocuments()[syntheticDocuments().length - 1] as Readonly<Record<string, unknown>>
      const fake = fakeBridge({ replies: { subscribeLive: () => snapshotReply(frame), ...replies } })
      const document = await boot(fake.bridge, new FakeDocument())
      const panel = panelOf(document)
      ;(classed(panel, 'live-run')[0] as FakeNode).click()
      return { document, panel, calls: fake.calls }
    }

    it('draws the run’s frame in the click’s own turn, before either read answers', async () => {
      // The owner's report of 2026-11-01: a click on a run id looked like it had
      // not registered, because the screen waited for both reads before drawing
      // anything. Neither read answers in this case at all -- and there is no
      // `await` between the click above and the assertions below, so what they
      // see is what the click itself drew.
      const diagnosis = holding()
      const logs = holding()
      const frame = syntheticDocuments()[syntheticDocuments().length - 1] as Readonly<Record<string, unknown>>
      const fake = fakeBridge({
        replies: {
          subscribeLive: () => snapshotReply(frame),
          diagnoseRun: diagnosis.reply,
          readRunLogs: logs.reply,
          runLogLink: () => ({ ok: false, reason: 'this run has no transcript link' }),
        },
      })
      const document = await boot(fake.bridge, new FakeDocument())
      const panel = panelOf(document)
      ;(classed(panel, 'live-run')[0] as FakeNode).click()
      // **Nothing is awaited between the click above and the assertions
      // below.** A case that flushed first would pass against a screen that
      // drew a turn later, and drawing a turn later is the whole of the
      // owner's complaint.
      expect(panel.children[0]?.attributes.get('class'), 'the click drew no run screen').toBe('run')
      // The run the operator clicked, and the way back from it.
      expect(classed(panel, 'run-id')[0]?.textContent).toBe('20261015-120000-aa01')
      expect(classed(panel, 'run-crumb-live')[0]?.tagName).toBe('BUTTON')
      // Both sections wear their own mark and say what they are reading.
      const main = classed(panel, 'run-record')[0] as FakeNode
      const logsSection = classed(panel, 'run-logs')[0] as FakeNode
      expect(main?.attributes.get('data-busy'), 'the record’s host does not say it is reading').toBe('true')
      expect(main?.attributes.get('aria-busy')).toBe('true')
      expect(logsSection?.attributes.get('aria-busy')).toBe('true')
      const labels = classed(panel, 'console-busy-label').map((node) => node.textContent)
      expect(labels).toContain('Reading the diagnosis of 20261015-120000-aa01')
      expect(labels).toContain('Reading the log window of 20261015-120000-aa01')
      // A skeleton stands in each section, so the frame is a shape rather than
      // blank space.
      expect(classed(panel, 'console-skeleton')).toHaveLength(2)
      // And no figure is drawn: nothing has arrived yet.
      expect(classed(panel, 'run-stages')).toEqual([])
      expect(classed(panel, 'run-log-line')).toEqual([])
      expect(classed(panel, 'run-log-link-note')).toEqual([])
    })

    it('fills each section when its own read answers, whichever order they land in', async () => {
      // The log window answers first: its section paints, and the diagnosis is
      // still marked as reading -- the screen is never held until the last read.
      const diagnosis = holding()
      const logs = holding()
      const { panel } = await opened({
        diagnoseRun: diagnosis.reply,
        readRunLogs: logs.reply,
        runLogLink: () => ({ ok: false, reason: 'this run has no transcript link' }),
      })
      logs.release({
        ok: true,
        data: { document: { entries: [{ time: '2026-10-15T12:00:00Z', severity: 'info', stage: 'implement', message: 'the run started' }], total: 1, refused: 0 } },
      })
      await flush()
      expect(classed(panel, 'run-log-line')).toHaveLength(1)
      // The replay panel is the log read's own arrival, drawn with the window it
      // replays: it is up as soon as that read lands, before the record's has.
      expect(classed(panel, 'run-replay')).toHaveLength(1)
      expect(classed(panel, 'run-logs')[0]?.attributes.get('data-busy')).toBeUndefined()
      expect(classed(panel, 'run-record')[0]?.attributes.get('data-busy')).toBe('true')
      expect(classed(panel, 'console-busy-label').map((node) => node.textContent)).toEqual([
        'Reading the diagnosis of 20261015-120000-aa01',
      ])

      // Then the record lands, and the window is still there: the two fills are
      // written into the same column without either taking the other's place.
      diagnosis.release({ ok: true, data: { document: {} } })
      await flush()
      expect(classed(panel, 'run-stages')).toHaveLength(1)
      expect(classed(panel, 'run-log-line')).toHaveLength(1)
      expect(classed(panel, 'run-tolerated-absent')).toHaveLength(1)
      // And the record's fill left the other read's replay panel exactly where
      // the log read drew it: one panel, never two.
      expect(classed(panel, 'run-replay')).toHaveLength(1)
      // Neither section is reading any more, and neither kept a placeholder.
      expect(classed(panel, 'console-busy-label')).toEqual([])
      expect(classed(panel, 'console-skeleton')).toEqual([])
      expect(classed(panel, 'run-main')[0]?.attributes.get('data-busy')).toBeUndefined()
      expect(classed(panel, 'run-logs')[0]?.attributes.get('data-busy')).toBeUndefined()
      // The way back is still bound after both fills.
      const crumb = classed(panel, 'run-crumb-live')[0] as FakeNode
      crumb.click()
      await flush()
      expect((panel.children[0] as FakeNode).attributes.get('class')).toBe('live')
    })

    it('keeps the log read’s replay panel when the diagnosis refuses after it', async () => {
      // The replay panel is what the *log* read draws, and the diagnosis is the
      // record read's: a record that refuses says nothing about the window that
      // arrived before it, so the panel stays under it. The log read answers
      // first here, so the refusal is landing on a column that already holds it.
      const diagnosis = holding()
      const { panel } = await opened({
        diagnoseRun: diagnosis.reply,
        readRunLogs: () => ({
          ok: true,
          data: { document: { entries: [{ time: '2026-10-15T12:00:00Z', severity: 'info', stage: 'implement', message: 'the run started' }], total: 1, refused: 0 } },
        }),
        runLogLink: () => ({ ok: false, reason: 'this run has no transcript link' }),
      })
      await flush()
      expect(classed(panel, 'run-log-line')).toHaveLength(1)
      expect(classed(panel, 'run-replay')).toHaveLength(1)

      diagnosis.release({ ok: false, reason: 'no such run 20261015-120000-aa01' })
      await flush()
      expect(classed(panel, 'unavailable-operation').map((node) => node.textContent)).toEqual(['run.diagnose'])
      expect(classed(panel, 'run-stages'), 'the timeline was drawn under a refused diagnosis').toEqual([])
      expect(classed(panel, 'run-log-line')).toHaveLength(1)
      expect(
        classed(panel, 'run-replay'),
        'the refused diagnosis took the log read’s replay panel with it',
      ).toHaveLength(1)
    })

    it('rewrites the window’s sentence when the transcript link lands after it', async () => {
      // The window's own note -- "no `logs RUN --url` line arrived" -- is a
      // sentence about a read that had not answered yet. A link that arrives
      // after the window must replace it, or the log section would claim the
      // CLI printed no link when the console had simply not been told.
      const link = holding()
      const { panel } = await opened({
        diagnoseRun: () => ({ ok: true, data: { document: {} } }),
        readRunLogs: () => ({
          ok: true,
          data: { document: { entries: [{ time: '2026-10-15T12:00:00Z', severity: 'info', stage: 'implement', message: 'the run started' }], total: 1, refused: 0 } },
        }),
        runLogLink: link.reply,
      })
      await flush()
      const note = classed(panel, 'run-log-link-note')[0] as FakeNode
      expect(note?.textContent, 'the window never said its link was missing').toContain('logs RUN --url')
      expect(classed(panel, 'run-log-link')).toEqual([])

      link.release({ ok: true, data: 'https://console.cloud.google.com/logs/query' })
      await flush()
      expect(classed(panel, 'run-log-link-note'), 'the missing-link sentence outlived the link').toEqual([])
      expect(classed(panel, 'run-log-link')).toHaveLength(1)
      // And the window itself was redrawn, not lost with the sentence.
      expect(classed(panel, 'run-log-line')).toHaveLength(1)
    })
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
      expect(calls.map((call) => call.name)).toEqual(['subscribeLive', 'listRuns'])

      // One tick: a second watch is made, and it does not answer. The rigor join
      // is not read again -- its TTL is `LIVE_RIGOR_TTL_MS` and this tick is one
      // second in -- so a poll is one `watch` and the join is not on its clock.
      await vi.advanceTimersByTimeAsync(LIVE_POLL_MS)
      expect(calls.map((call) => call.name)).toEqual(['subscribeLive', 'listRuns', 'subscribeLive'])
      expect(held).toHaveLength(1)

      // Three more ticks with that read outstanding: **no third call**. This is
      // the claim the flag exists for -- a slow read cannot be overtaken by the
      // next tick, so the console never has two watches of one installation in
      // flight at once.
      await vi.advanceTimersByTimeAsync(LIVE_POLL_MS * 3)
      expect(calls.map((call) => call.name)).toEqual(['subscribeLive', 'listRuns', 'subscribeLive'])

      // Once it answers, the next tick is a new read.
      held[0]?.()
      await vi.advanceTimersByTimeAsync(LIVE_POLL_MS)
      expect(calls.map((call) => call.name)).toHaveLength(4)
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
      expect(calls.map((call) => call.name)).toEqual(['subscribeLive', 'listRuns'])

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

    it('reads the rigor join on entry and then no more often than its own TTL', async () => {
      // The Live screen's rows draw a mode the live frame does not carry, so the
      // entry makes a second read -- `runs ls --json` -- and joins it by run id
      // (`shared/rigor.ts`). **That read is not on the poll's clock**: a block is
      // written once at bootstrap and the only thing a fresh read adds is a run
      // that started since, so it is made on entry and then at most once per
      // `LIVE_RIGOR_TTL_MS`. On the poll's 1 s clock it would be sixty
      // invocations a minute of a command the Live screen does not otherwise
      // need.
      const frame = syntheticDocuments()[0] as Readonly<Record<string, unknown>>
      const { bridge, calls } = fakeBridge({
        replies: {
          subscribeLive: () => snapshotReply(frame),
          listRuns: () => ({ ok: true, data: { document: { project: 'example-project', runs: [], warnings: [] } } }),
        },
      })
      await boot(bridge, new FakeDocument())
      expect(calls.map((call) => call.name)).toEqual(['subscribeLive', 'listRuns'])

      // Five polls, five seconds: the watch is read five more times and the join
      // is not read again.
      await vi.advanceTimersByTimeAsync(LIVE_POLL_MS * 5)
      expect(calls.filter((call) => call.name === 'subscribeLive')).toHaveLength(6)
      expect(calls.filter((call) => call.name === 'listRuns')).toHaveLength(1)

      // Past the TTL the next tick refreshes it -- once, however many ticks the
      // advance covers, because the read's own start is what the TTL is measured
      // from.
      await vi.advanceTimersByTimeAsync(LIVE_RIGOR_TTL_MS * 3)
      expect(calls.filter((call) => call.name === 'listRuns').length).toBeGreaterThan(1)
      expect(calls.filter((call) => call.name === 'listRuns').length).toBeLessThanOrEqual(5)
    })

    it('keeps the last rigor join when a later read of it is refused', async () => {
      // A refused read is not a reason to take the chips off the rows: the map
      // stays as it was, so a console whose `runs ls` fails for one tick draws
      // the modes it already knew rather than a screen with no mode on it.
      const frame = syntheticDocuments()[0] as Readonly<Record<string, unknown>>
      let refusals = 0
      const { bridge, calls } = fakeBridge({
        replies: {
          subscribeLive: () => snapshotReply(frame),
          listRuns: () => {
            refusals += 1
            if (refusals === 1) {
              return {
                ok: true,
                data: {
                  document: {
                    project: 'example-project',
                    runs: [{ run: '20261015-120000-aa01', rigor: { mode: 'pro', from: 'repo' } }],
                    warnings: [],
                  },
                },
              }
            }
            return { ok: false, reason: 'the CLI refused this read' }
          },
        },
      })
      const document = await boot(bridge, new FakeDocument())
      // The join answers a turn after the first drawing, so the chip lands on
      // the next poll -- which is the same order every other read lands in.
      await flush()
      await vi.advanceTimersByTimeAsync(LIVE_POLL_MS)
      expect(classed(panelOf(document), 'live-run-mode').map((node) => node.textContent)).toEqual(['Pro'])

      await vi.advanceTimersByTimeAsync(LIVE_RIGOR_TTL_MS + LIVE_POLL_MS)
      expect(calls.filter((call) => call.name === 'listRuns').length).toBeGreaterThan(1)
      // The refused read drew no refusal screen and took no chip away.
      expect(classed(panelOf(document), 'live-run-mode').map((node) => node.textContent)).toEqual(['Pro'])
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
      // Neither is the range the read above answered: a range this session has
      // already read is drawn from the cache and starts no read at all, so a
      // case about two reads in flight has to pick two it has not read.
      // `windows[0]` is the range the opening read answered, which the cache
      // would serve without a read.
      const first = windows[1]
      const second = windows[2]
      expect(first?.textContent).not.toBe(second?.textContent)
      first?.click()
      second?.click()
      await flush()
      expect(held.map((each) => each.since)).toEqual([first?.textContent, second?.textContent])
      const [earlier, later] = held
      later?.answer()
      await flush()
      const drawn = panel.children[0]
      const current = classed(panel, 'spend-window')
        .filter((each) => each.attributes.get('data-current') === 'true')
        .map((each) => each.textContent)
      expect(current).toEqual([second?.textContent])

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
      { since: '7d' },
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

// The confirmation window (V2.5.1a part 2b-3b-1): the one place in this app a
// `fugaro run` child becomes possible.
// ---------------------------------------------------------------------------

describe('the launch the entry wires', () => {
  it('builds the confirmation window inside main, over the default session, and rebuilds the runner over it', () => {
    // The window needs `session.defaultSession` and it registers its two IPC
    // channels once, so it cannot be built at module scope -- and it has to
    // exist before a renderer can reach `run.launch`. What makes building it in
    // `main` safe is that the handler reads `launchRunner` at message time and
    // the surface goes on `ipcMain` only from `onDecided`, which only
    // `lifecycle.start()` and `choose` reach, both from inside `main`.
    //
    // This file imports Electron's runtime, so the claims are read off `index.ts`
    // as **text**, and the *function bodies* are what is read: naming a constant
    // in prose elsewhere in the file cannot satisfy any of them.
    const index = readFileSync(join(CONSOLE_ROOT, 'src', 'main', 'index.ts'), 'utf8')
    const main = /async function main\(\): Promise<void> \{[\s\S]*?\n\}\n/.exec(index)
    if (main === null) throw new Error('the scan found no main(): it would pass by finding nothing')
    // The **code**, with the comments blanked: a comment inside these bodies can
    // name a call to say why another one comes first, so a scan that read prose
    // would answer about that sentence rather than about the call.
    const body = withoutComments(main[0])

    // The window is built in `main`'s own body, before the startup opens a
    // window -- the first moment a renderer could ask for a launch -- and on
    // `session.defaultSession`, which is the only session the `app://` handler
    // serves: a window on a partition would issue its document request where
    // nothing answers it, and every launch would be declined.
    expect(body).toContain('createConfirmWindow(')
    expect(body).toContain('session: session.defaultSession')
    expect(body.indexOf('createConfirmWindow(')).toBeLessThan(body.indexOf('await lifecycle.start()'))
    expect(index).not.toContain('fromPartition')

    // ...and before the **picker**, which is the other path that puts the
    // surface on `ipcMain`: `installInstallationPicker` registers the rail's
    // operations and closes over `onChosen`, so a picker installed first is a
    // window whose choose can reach `run.launch` -- and `cancelFor` -- before the
    // confirmer the launch would be answered through exists.
    const pickerAt = body.indexOf('installInstallationPicker(')
    if (pickerAt < 0) throw new Error('the scan found no installInstallationPicker: it would pass by finding nothing')
    expect(body.indexOf('createConfirmWindow(')).toBeLessThan(pickerAt)

    // The module-scope runner is the fail-closed default: the declining
    // confirmer is what answers a launch before `main` has built the window.
    const declared = /let launchRunner = createLaunchRunner\(\{[\s\S]*?\n\}\)\n/.exec(index)
    if (declared === null) throw new Error('the entry has no module-scope launchRunner to read')
    expect(declared[0]).toContain('confirmer: DECLINING_CONFIRMER')

    // Exactly the two: the fail-closed one at module scope and the one `main`
    // builds over the window's confirmer. A third would be a second launcher --
    // its own in-flight caps, its own run ids, its own held windows -- beside
    // the one the surface and the cancel paths read.
    expect(withoutComments(index).match(/createLaunchRunner\(/g) ?? []).toHaveLength(2)

    // And **both** of `main`'s flows are over that one window's confirmer, never
    // over the declining one: D1115's "one window, two questions" is one
    // `createConfirmWindow` call in this whole file and two runners built over
    // it -- a second window would be a second implementation of the tick, the
    // delay and the sender gate.
    const asked = /const confirmer = createConfirmWindow\(\{[\s\S]*?\n  \}\)\n/.exec(body)
    if (asked === null) throw new Error('the entry builds no confirmer to read')
    expect(withoutComments(index).match(/createConfirmWindow\(/g) ?? []).toHaveLength(1)
    expect(body).toContain('launchRunner = createLaunchRunner(')
    expect(body).toContain('settingsEditRunner = createSettingsEdit(')
    expect(body).not.toContain('DECLINING_CONFIRMER')

    // Both places the entry learns an installation moved cancel the launch its
    // project has in flight: the choose -- "re-chosen" is that installation
    // being chosen again, since a launch names its own installation -- and the
    // rail's removal. The cancel comes before `lifecycle.choose`, so the window
    // is already gone by the time the new installation's window opens.
    const chosen = /onChosen: \(chosen\) => \{[\s\S]*?\n    \},\n/.exec(body)
    if (chosen === null) throw new Error('the entry has no onChosen to read')
    expect(chosen[0]).toContain('launchRunner.cancelFor(chosen.project)')
    expect(chosen[0].indexOf('launchRunner.cancelFor(chosen.project)')).toBeLessThan(
      chosen[0].indexOf('lifecycle.choose'),
    )
    expect(chosen[0]).toContain('lifecycle.choose')
    // **And the settings edit with it** (D1115): an installation that moved
    // closes the edit's window too, and one already read for that installation
    // must not write. Both flows are named, in both places, because the mark the
    // edit leaves is only reachable while its window is not.
    expect(chosen[0]).toContain('settingsEditRunner.cancelFor(chosen.project)')

    const removed = /onRecordRemoved: \(project\) => \{[\s\S]*?\n    \},\n/.exec(body)
    if (removed === null) throw new Error('the entry has no onRecordRemoved to read')
    expect(removed[0]).toContain('launchRunner.cancelFor(project)')
    expect(removed[0]).toContain('settingsEditRunner.cancelFor(project)')
  })

  it('brings the update policy up with the launcher it defers to, and stops it on the way out', () => {
    // The auto-update policy (Task 4.4) is the second thing this entry starts,
    // and the one thing it starts that is not a child process. The claims are
    // read off the text, as above.
    const index = readFileSync(join(CONSOLE_ROOT, 'src', 'main', 'index.ts'), 'utf8')
    const main = /async function main\(\): Promise<void> \{[\s\S]*?\n\}\n/.exec(index)
    if (main === null) throw new Error('the scan found no main(): it would pass by finding nothing')
    const body = withoutComments(main[0])

    // The policy is built **after** the launcher, because the launcher is what
    // it asks: `isLaunchFlowIdle` is read at every prompt and every install, and
    // a policy built over the module-scope default would be asking a runner that
    // answers for no launches at all.
    expect(body).toContain('startUpdates(')
    expect(body.indexOf('createLaunchRunner(')).toBeLessThan(body.indexOf('startUpdates('))

    // The two halves of the same fact: the policy asks `isIdle()` when it is
    // about to prompt or install, and the flow tells it through `onIdle` when a
    // launch that was in flight settles -- so a download that finished mid-launch
    // is offered the moment the launch ends rather than at the next check.
    expect(body).toContain('isLaunchFlowIdle: () => launchRunner.isIdle()')
    expect(body).toContain('launchRunner.onIdle(')
    expect(body).toContain('launchFlowIdle()')

    // Exactly the one: a second `startUpdates` would be a second controller --
    // its own timer, its own feed URL, its own listeners on the update client.
    expect(withoutComments(index).match(/startUpdates\(/g) ?? []).toHaveLength(1)

    // The controller is module scope, because `before-quit` is not inside
    // `main`, and the quit stops it: its timer is a live handle and its
    // generation counter is what abandons a check whose listing is in flight.
    if (/let updates: UpdateController \| null = null/.exec(index) === null) {
      throw new Error('the entry has no module-scope `updates` to read')
    }
    const quit = /app\.on\('before-quit', \(event\) => \{[\s\S]*?\n\}\)\n/.exec(index)
    if (quit === null) throw new Error('the entry has no before-quit to read')
    expect(quit[0]).toContain('updates?.stop()')
  })

  it('gives the switch its one source, and the menu its two items only while a controller runs (U4)', () => {
    // The off switch of D768 (a): `<userData>/updates.json` is what the policy's
    // gate reads, and the two menu items are handed to the picker over the same
    // settings -- so the checkbox and the gate are one value rather than two
    // that could disagree.
    const index = readFileSync(join(CONSOLE_ROOT, 'src', 'main', 'index.ts'), 'utf8')
    const main = /async function main\(\): Promise<void> \{[\s\S]*?\n\}\n/.exec(index)
    if (main === null) throw new Error('the scan found no main(): it would pass by finding nothing')
    const body = withoutComments(main[0])

    expect(body).toContain('switchOn: () => updateSettings.auto')
    expect(body).toContain('controller: () => updates')
    // The settings are built **before** the policy, which reads them for the
    // first check `startUpdates` runs, and read afresh after that.
    expect(body.indexOf('createUpdateSettings(')).toBeLessThan(body.indexOf('startUpdates('))
    // The items go over only when there is a controller: `startUpdates` answered
    // null on everything that is not a packaged macOS app, which is the whole of
    // "macOS only" here -- nothing repeats that admission test.
    expect(body).toContain('updates === null')
    expect(body).toContain('updateMenu: {')
    expect(body).toContain('setAuto: (on) => updateSettings.setAuto(on)')
    expect(body).toContain('checkNow: () => updateSettings.checkNow()')
    expect(withoutComments(index).match(/updateMenu:/g) ?? []).toHaveLength(1)
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
      RAIL_EMPTY_CLASS,
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
    // The two reads the mount makes and no more: the Live frame, and the run
    // list the Live screen's rigor join is read from. The rail reads neither.
    expect(calls.map((call) => call.name)).toEqual(['subscribeLive', 'listRuns'])
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

  it('draws the shell: a header with the switcher holding the rail, and a page holding the panel', async () => {
    const frame = syntheticDocuments()[0] as Readonly<Record<string, unknown>>
    const { bridge } = fakeBridge({ replies: { subscribeLive: () => snapshotReply(frame, nowStamp()) } })
    const document = await boot(bridge, new FakeDocument())
    // The header switcher replaced the rail (owner's ruling of 2026-10-28): the
    // body holds the header and the page, and the rail is inside the menu.
    expect(document.body.children.map((child) => child.attributes.get('class'))).toEqual([
      SHELL_HEADER_CLASS,
      SHELL_PAGE_CLASS,
    ])
    const header = document.body.children[0] as FakeNode
    expect(header.tagName).toBe('HEADER')
    expect(header.children.map((child) => child.attributes.get('class'))).toEqual([
      SHELL_WORDMARK_CLASS,
      SWITCHER_CLASS,
      VIEW_NAV_CLASS,
      GAUGE_CLASS,
      HALT_CLASS,
    ])
    const page = document.body.children[1] as FakeNode
    expect(page.tagName).toBe('MAIN')
    expect(page.children.map((child) => child.attributes.get('class'))).toEqual([VIEW_PANEL_CLASS])

    // The switcher names the installation and its CLI, and its menu holds the
    // installation line (the CLI's full path) and the rail, folded at first.
    const switcher = classed(header, SWITCHER_CLASS)[0] as FakeNode
    const button = classed(switcher, SWITCHER_BUTTON_CLASS)[0] as FakeNode
    const menu = classed(switcher, SWITCHER_MENU_CLASS)[0] as FakeNode
    expect(classed(button, SWITCHER_NAME_CLASS)[0]?.textContent).toBe('acme')
    expect(classed(button, SWITCHER_VERSION_CLASS)[0]?.textContent).toBe('CLI 1.2.3 · ok')
    expect(classed(button, SWITCHER_DOT_CLASS)[0]?.attributes.get('data-state')).toBe('running')
    expect(menu.children.map((child) => child.attributes.get('class'))).toEqual(['console-installation', RAIL_CLASS])
    expect(textOf(menu)).toContain('/opt/fugaro/bin/fugaro')
    expect(menu.attributes.get('hidden')).toBe('')
    expect(button.attributes.get('aria-expanded')).toBe('false')
    expect(button.attributes.get('aria-controls')).toBe(menu.attributes.get('id'))
    button.click()
    expect(menu.attributes.has('hidden')).toBe(false)
    expect(button.attributes.get('aria-expanded')).toBe('true')
    // Escape folds it and hands the focus back to the button.
    const escape = switcher.press('Escape')
    expect(escape.defaultPrevented).toBe(true)
    expect(menu.attributes.get('hidden')).toBe('')
    expect(document.activeElement).toBe(button)
    // Activating a row folds the menu as well.
    button.click()
    railRow(document).click()
    expect(menu.attributes.get('hidden')).toBe('')
  })

  it('draws Halt fleet disabled, with its reason, and binds nothing to it', async () => {
    const { bridge, calls } = fakeBridge({})
    const document = await boot(bridge, new FakeDocument())
    const halt = classed(document.body, HALT_CLASS)[0] as FakeNode
    expect(halt.tagName).toBe('BUTTON')
    expect(halt.attributes.get('aria-disabled')).toBe('true')
    expect(halt.attributes.get('title')).toBe(WRITE_UNAVAILABLE)
    expect(textOf(halt).trim()).toBe('Halt fleet')
    // No listener of any kind: the console's one approved write is not this.
    expect(halt.listeners.size).toBe(0)
    const before = calls.length
    halt.click()
    expect(calls.length).toBe(before)
  })

  it('fills the fleet gauge from the Live total, and from the budget read', async () => {
    const frame = syntheticDocuments()[0] as Readonly<Record<string, unknown>>
    const budget = {
      global: { daily_cap_usd: 40, counted_usd: 10, spent_usd: 4, notional_usd: 1.5 },
    }
    const { bridge } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(frame, nowStamp()),
        showBudget: () => ({ ok: true, data: { document: budget } }),
      },
    })
    const document = await boot(bridge, new FakeDocument())
    const gauge = classed(document.body, GAUGE_CLASS)[0] as FakeNode
    expect(classed(gauge, GAUGE_LABEL_CLASS)[0]?.textContent).toBe('GLOBAL')
    const total = (frame['total'] ?? {}) as Record<string, unknown>
    const cap = total['daily_cap_usd']
    const capText = classed(gauge, GAUGE_CAP_CLASS)[0]?.textContent
    if (typeof cap === 'number') expect(capText).toBe(`$${cap.toFixed(2)}`)
    else expect(capText).toBe('n/a')
    buttonFor(document, 'spend').click()
    await flush()
    expect(classed(gauge, GAUGE_CAP_CLASS)[0]?.textContent).toBe('$40.00')
    expect(classed(gauge, GAUGE_NOTIONAL_CLASS)[0]?.textContent).toContain('1.50')
    // The bar is counted over cap, as the fill's width in the SVG's units, and
    // carries no notional segment.
    expect(classed(gauge, GAUGE_FILL_CLASS)[0]?.attributes.get('width')).toBe('24')
    expect(gauge.attributes.get('title')).toBe('counted $10.00 of the $40.00 daily cap (25.0%)')
  })

  it('withholds the gauge’s figures when the mode withholds caps', async () => {
    const { bridge } = fakeBridge({ compatibility: aReducedCompatibility() })
    const document = await boot(bridge, new FakeDocument())
    const gauge = classed(document.body, GAUGE_CLASS)[0] as FakeNode
    expect(classed(gauge, GAUGE_CAP_CLASS)[0]?.textContent).toBe('n/a')
    expect(classed(gauge, GAUGE_FILL_CLASS)[0]?.attributes.get('width')).toBe('0')
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

describe('the view navigation', () => {
  it('is five buttons, each a tab stop, the view on screen marked aria-current="page"', async () => {
    // The redesign's tabs (owner's ruling of 2026-10-28) replaced Task 6.5's nav tablist:
    // ordinary tab stops whose own Enter and Space are the click. They stay buttons because
    // the renderer writes no `href`.
    const { bridge, calls } = fakeBridge({
      replies: { showBudget: () => ({ ok: true, data: { document: { global: { counted_usd: 12.5 } } } }) },
    })
    const document = await boot(bridge, new FakeDocument())
    const nav = navOf(document)
    const live = buttonFor(document, 'live')
    const graph = buttonFor(document, 'graph')
    const spend = buttonFor(document, 'spend')
    const projects = buttonFor(document, 'projects')
    const coordinator = buttonFor(document, 'coordinator')
    expect(nav.attributes.get('role')).toBeUndefined()
    expect(nav.attributes.get('aria-label')).toBe('Console')
    for (const button of [live, graph, spend, projects, coordinator]) {
      expect(button.tagName).toBe('BUTTON')
      expect(button.attributes.get('type')).toBe('button')
      expect(button.attributes.has('tabindex')).toBe(false)
      expect(button.attributes.has('role')).toBe(false)
    }
    expect(live.attributes.get('aria-current')).toBe('page')
    for (const other of [graph, spend, projects, coordinator]) expect(other.attributes.has('aria-current')).toBe(false)

    // A click shows the view and makes its read.
    graph.click()
    await flush()
    expect(graph.attributes.get('aria-current')).toBe('page')
    expect(live.attributes.has('aria-current')).toBe(false)
    expect(calls.filter((call) => call.name === 'planStatus')).toHaveLength(1)
    spend.click()
    await flush()
    expect(spend.attributes.get('aria-current')).toBe('page')
    expect(calls.filter((call) => call.name === 'showBudget')).toHaveLength(1)

    // The arrows are not the nav's any more: they are left to the window.
    expect(spend.press('ArrowRight').defaultPrevented).toBe(false)
    expect(spend.attributes.get('aria-current')).toBe('page')

    // The wordmark goes to Live and takes the focus there.
    ;(classed(document.body, SHELL_WORDMARK_CLASS)[0] as FakeNode).click()
    expect(live.attributes.get('aria-current')).toBe('page')
    expect(document.activeElement).toBe(live)
  })

  it('marks no view while Run detail is up', async () => {
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
    for (const button of navButtons(document)) expect(button.attributes.has('aria-current')).toBe(false)
  })

  it('draws Halt project and Reap disabled, and neither asks the main process for anything', async () => {
    // The redesign (step 2b) draws the two controls a reader of a project panel
    // and of a lost run expects. They are **drawn** and not wired: the console's
    // one approved write is launch-from-console, so both go through `writes.ts`'s
    // `disabledWrite` and neither is a channel, a preload member or a spawn
    // entry. `live.ts` being a pure drawing file is what makes that true; this
    // reads it off the booted window rather than off the source.
    const documents = syntheticDocuments()
    const frame = documents[documents.length - 1] as Readonly<Record<string, unknown>>
    const { bridge, calls } = fakeBridge({
      replies: { subscribeLive: () => snapshotReply(frame) },
    })
    const document = await boot(bridge, new FakeDocument())
    const panel = panelOf(document)
    for (const className of ['live-halt', 'live-reap']) {
      const drawn = classed(panel, className)[0] as FakeNode | undefined
      expect(drawn, `.${className} was not drawn`).toBeDefined()
      const button = drawn as FakeNode
      expect(button.tagName).toBe('BUTTON')
      expect(button.attributes.get('aria-disabled')).toBe('true')
      expect(button.attributes.get('title')).toBe(WRITE_UNAVAILABLE)
      expect(button.listeners.size).toBe(0)
      const before = calls.length
      button.click()
      await flush()
      expect(calls.length, `.${className} asked the main process for something`).toBe(before)
    }
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

  it('keeps the rail’s two tab stops in the order a reader sees them', async () => {
    // Inside the switcher's menu the rail's stops are the list -- **one stop however many
    // installations it holds**, which is what the roving `tabindex` buys -- and the add row.
    const { bridge } = fakeBridge({})
    const document = await boot(bridge, new FakeDocument())
    const rail = railOf(document)
    const stops = everyNode(rail).filter(
      (node) => node.tagName === 'BUTTON' || node.attributes.has('tabindex'),
    )
    expect(stops.map((node) => node.attributes.get('class'))).toEqual([RAIL_ROW_CLASS, RAIL_ADD_CLASS])
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
    readonly checkoutChoices?: readonly Record<string, unknown>[]
  } = {},
): Record<string, unknown> {
  return {
    project,
    open: options.open ?? false,
    active: options.open ?? false,
    saved: true,
    checkouts: options.checkouts ?? [],
    checkoutChoices: options.checkoutChoices ?? [],
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
    // The menu is the main process's own list -- one entry per repository the
    // CLI re-validated and enrolled -- and it is not the picker window's
    // `checkouts`, which carries a path with no repository to label it by.
    checkoutChoices: [{ repository: 'github/owner/orbit', path: '/home/operator/src/orbit', more: 0 }],
  })
}

/** railBridge is `fakeBridge` with the rail's six operations, each recorded. */
function railBridge(
  rows: readonly Record<string, unknown>[],
  answers: Readonly<Record<string, AReply>> = {},
  onboard?: Readonly<Record<string, unknown>>,
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
      return name === 'listInstallations'
        ? { ok: true, data: { rows, ...(onboard === undefined ? {} : { onboard }) } }
        : { ok: true, data: null }
    }
  }
  return { ...base, rail }
}

/**
 * queueBridge is the fake bridge for the Coordinator's action queue (console
 * 0.118.0, K1): one open installation that records a checkout and offers the
 * repositories the case gives it -- `QUEUE_OFFER` alone unless a case about the
 * prefill's match needs another -- and the runs, plan and budget reads answered
 * by the case.
 *
 * **Every call is recorded in the order it was made**, which is what lets a case
 * say that the queue's four reads went out in the turn the tab was opened in --
 * the claim the owner's rule of 2026-10-31 is about. `launchRun` is the
 * console's one write and is recorded here too, so "the queue spawned nothing"
 * is a claim about the same list.
 */
function queueBridge(
  answers: Readonly<Record<string, AReply>>,
  repositories: readonly string[] = [QUEUE_OFFER],
): FakeBridge {
  const base = railBridge([
    {
      ...listRow('acme', { open: true }),
      checkout: '/home/operator/src/acme',
      repositories,
    },
  ])
  // The base's list is the same array its own reads append to; it is typed
  // `readonly` because a case reads it, and this helper is the one writer.
  const calls = base.calls as { name: string; args: unknown }[]
  for (const name of [
    'coordinatorStatus',
    'coordinatorShow',
    'listRuns',
    'planStatus',
    'showBudget',
    'launchRun',
    'openPullRequest',
  ]) {
    base.bridge[name] = (args: unknown): unknown => {
      calls.push({ name, args })
      const answer = answers[name]
      return answer === undefined
        ? { ok: false, reason: `the console did not read ${name} in this case` }
        : answer(args)
    }
  }
  // The pane's own read (K5) is the one that answers with a document rather
  // than a refusal when a case says nothing about it: every case in this file
  // that opens the Coordinator is a case about something else, and a refusal by
  // default would put a second refusal -- naming `coordinator.intents` -- on a
  // page whose own case asserts it has one.
  const intents = answers['coordinatorIntents']
  base.bridge['coordinatorIntents'] = (args: unknown): unknown => {
    calls.push({ name: 'coordinatorIntents', args })
    return intents === undefined ? { ok: true, data: { document: { repos: [] } } } : intents(args)
  }
  // The strip's window (0.119.0) answers with a document for the same reason and
  // one of its own: the read is a *second* section of a panel whose own cases
  // assert on what the first one drew, and an empty window draws figures that
  // belong to no read a case named.
  const summary = answers['summaryRuns']
  base.bridge['summaryRuns'] = (args: unknown): unknown => {
    calls.push({ name: 'summaryRuns', args })
    return summary === undefined
      ? { ok: true, data: { document: AN_EMPTY_WINDOW } }
      : summary(args)
  }
  return base
}

function rowsOf(document: FakeDocument): readonly FakeNode[] {
  return classed(railOf(document), RAIL_ROW_CLASS)
}

function rowNamed(document: FakeDocument, project: string): FakeNode {
  const row = rowsOf(document).find((each) => classed(each, RAIL_NAME_CLASS)[0]?.textContent === project)
  if (row === undefined) throw new Error(`the rail drew no row for ${project}`)
  return row
}

/** askForJobs presses the queue's jobs control and lets its answer land: the
 * jobs are the one read the Coordinator does not make on entry (rule 5 of §6.2's
 * opening), so a case about a job item is a case that asked for it. */
async function askForJobs(document: FakeDocument): Promise<void> {
  buttonLabelled(panelOf(document), COORDINATOR_QUEUE_JOBS_WORD).click()
  await flush()
}

/** pullButtons is one class of the numbers the screen drew as **buttons**, in
 * document order. A number whose triple the screen could not form is a span of
 * text, so this is also what tells the two apart. */
function pullButtons(root: FakeNode, className: string): readonly FakeNode[] {
  return classed(root, className).filter((node) => node.tagName === 'BUTTON')
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
    expect(calls.map((call) => call.name)).toContain('subscribeLive')
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

    // The chooser is a menu of the installation's own checkouts and one button
    // beside it, and the directory it sends is the entry the menu carries: the
    // control's value is what the main process matches against its own list.
    const menu = classed(view, CHECKOUT_SELECT_CLASS)[0] as FakeNode
    expect(menu.tagName).toBe('SELECT')
    expect(menu.children.map((option) => option.attributes.get('value'))).toEqual(['/home/operator/src/orbit'])
    // A real `select` reports its first option as its value before anyone
    // touches it; the fake node derives nothing, so the case writes it.
    ;(menu as unknown as { value: string }).value = '/home/operator/src/orbit'
    buttonLabelled(view, ISSUE_CHECKOUT_LABEL).click()
    await flush()
    expect(rail.at(-1), 'the menu sent a directory it was not offering').toEqual({
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
    expect(calls.map((call) => call.name)).toContain('subscribeLive')
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

// The launch control the Live poll redraws around (V2.5.1b follow-up).
// ---------------------------------------------------------------------------

/** launchField is one of the launch form's controls, by its class. */
function launchField(document: FakeDocument, className: string): FakeNode {
  const found = classed(document.body, className)[0]
  if (found === undefined) throw new Error(`the launch form drew no .${className}`)
  return found
}

/** valueOf is the value a control carries, which the fake holds only where the
 * entry (or a case) put one. */
function valueOf(node: FakeNode): string {
  const value = (node as unknown as { readonly value?: unknown }).value
  return typeof value === 'string' ? value : ''
}

/** typeInto writes one control's value the way an operator's typing would. */
function typeInto(document: FakeDocument, className: string, value: string): void {
  ;(launchField(document, className) as unknown as { value: string }).value = value
}

describe('the launch control the Live poll redraws around', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('keeps the form, the task, the title and the repository across a poll tick', async () => {
    // The poll replaces the Live screen under the control once a second, and a
    // control **rebuilt** on that tick would be a form that emptied itself while
    // the operator was typing: the typed task and title and the repository the
    // select holds all have to survive it. The entry keeps the control it built
    // for the same offer of repositories and appends the same element back --
    // this case is that claim, driven through a real tick.
    const frame = syntheticDocuments()[0] as Readonly<Record<string, unknown>>
    const row = {
      ...listRow('aurora', { open: true }),
      repositories: ['github/owner/repo-a', 'github/owner/repo-b'],
    }
    const { bridge, calls } = railBridge([row], { subscribeLive: () => snapshotReply(frame, nowStamp()) })
    const document = await boot(bridge, new FakeDocument())

    ;(launchField(document, LAUNCH_OPEN_CLASS) as FakeNode).click()
    await flush()
    const form = launchField(document, LAUNCH_FORM_CLASS)
    const screen = panelOf(document).children[0]
    typeInto(document, LAUNCH_TASK_CLASS, 'summarise the alerting backlog')
    typeInto(document, LAUNCH_TITLE_CLASS, 'the nightly backlog')
    typeInto(document, LAUNCH_REPOSITORY_CLASS, 'github/owner/repo-b')

    await vi.advanceTimersByTimeAsync(LIVE_POLL_MS)
    await flush()

    // The tick really happened: a second watch, and the Live screen under the
    // control is a new element. Without this the assertions below would hold for
    // a poll that drew nothing at all.
    expect(calls.filter((call) => call.name === 'subscribeLive')).toHaveLength(2)
    expect(panelOf(document).children[0], 'the tick replaced no screen').not.toBe(screen)
    // ...and through it the form is the one the operator was typing into.
    expect(launchField(document, LAUNCH_FORM_CLASS), 'the poll rebuilt the launch form').toBe(form)
    expect(valueOf(launchField(document, LAUNCH_TASK_CLASS))).toBe('summarise the alerting backlog')
    expect(valueOf(launchField(document, LAUNCH_TITLE_CLASS))).toBe('the nightly backlog')
    expect(valueOf(launchField(document, LAUNCH_REPOSITORY_CLASS))).toBe('github/owner/repo-b')
  })
})

describe('the rail with nothing left to open', () => {
  const FIRST = { kind: 'first-installation', command: 'fugaro setup' }

  it('draws the onboarding in the main area and an empty rail with its + when there is no installation at all', async () => {
    const { bridge, calls, rail } = railBridge([], {}, FIRST)
    const document = await boot(bridge, new FakeDocument())
    const onboarding = classed(panelOf(document), ONBOARDING_CLASS)[0]
    expect(onboarding, 'the panel drew no onboarding').toBeDefined()
    expect(classed(onboarding as FakeNode, ONBOARDING_TITLE_CLASS)[0]?.textContent).toBe('Set up your first installation')
    expect(classed(onboarding as FakeNode, ONBOARDING_COMMAND_CLASS)[0]?.textContent).toBe('fugaro setup')
    // Nothing to read, so nothing is read -- no Live refusal drawn in its place.
    expect(calls).toEqual([])
    const empty = classed(railOf(document), RAIL_EMPTY_CLASS)[0]
    expect(empty?.attributes.get('hidden')).toBeUndefined()
    expect(rowsOf(document)).toHaveLength(0)
    classed(railOf(document), RAIL_ADD_CLASS).find((node) => node.tagName === 'BUTTON')?.click()
    await flush()
    expect(rail.at(-1)?.name).toBe('openInstallations')
  })

  it('hides the empty-state line while the rail has rows', async () => {
    const { bridge } = railBridge([listRow('aurora', { open: true })])
    const document = await boot(bridge, new FakeDocument())
    expect(classed(railOf(document), RAIL_EMPTY_CLASS)[0]?.attributes.get('hidden')).toBe('')
  })

  it('draws the onboarding at once when the last installation is removed from its issue view', async () => {
    const { bridge, calls } = railBridge([orbitRow(true)], {
      removeInstallation: () => ({ ok: true, data: { rows: [], onboard: FIRST } }),
    })
    const document = await boot(bridge, new FakeDocument())
    buttonLabelled(classed(panelOf(document), ISSUE_CLASS)[0] as FakeNode, ISSUE_REMOVE_LABEL).click()
    await flush()
    expect(classed(panelOf(document), ONBOARDING_CLASS)).toHaveLength(1)
    expect(rowsOf(document)).toHaveLength(0)
    expect(calls).toEqual([])
  })

  it('opens the next readable installation at once when the open one is removed, with a fresh read', async () => {
    const { bridge, calls } = railBridge([orbitRow(true)], {
      removeInstallation: () => ({ ok: true, data: { rows: [listRow('aurora', { open: true })] } }),
    })
    const document = await boot(bridge, new FakeDocument())
    expect(calls).toEqual([])
    buttonLabelled(classed(panelOf(document), ISSUE_CLASS)[0] as FakeNode, ISSUE_REMOVE_LABEL).click()
    await flush()
    expect(classed(panelOf(document), ISSUE_CLASS)).toHaveLength(0)
    expect(calls.map((call) => call.name)).toContain('subscribeLive')
    expect(rowNamed(document, 'aurora').attributes.get('aria-selected')).toBe('true')
  })

  it('never redraws the removed installation’s Live frame: the next one is read afresh', async () => {
    const stale = listRow('aurora', { open: true, issue: 'stale', readable: true, sentence: 'aurora is stale.' })
    stale['saved'] = true
    const { bridge, calls } = railBridge([stale, listRow('comet')], {
      removeInstallation: () => ({ ok: true, data: { rows: [listRow('comet', { open: true })] } }),
    })
    const document = await boot(bridge, new FakeDocument())
    const before = calls.filter((call) => call.name === 'subscribeLive').length
    rowNamed(document, 'aurora').click()
    await flush()
    buttonLabelled(classed(panelOf(document), ISSUE_CLASS)[0] as FakeNode, ISSUE_REMOVE_LABEL).click()
    await flush()
    expect(calls.filter((call) => call.name === 'subscribeLive').length).toBe(before + 1)
    expect(rowNamed(document, 'comet').attributes.get('aria-selected')).toBe('true')
  })
})

describe('the rail’s accessibility (review m5)', () => {
  it('names its tablist by the rail’s own title', async () => {
    const { bridge } = railBridge([listRow('aurora', { open: true })])
    const document = await boot(bridge, new FakeDocument())
    const title = classed(railOf(document), RAIL_TITLE_CLASS)[0]
    const list = classed(railOf(document), RAIL_LIST_CLASS)[0]
    expect(title?.attributes.get('id')).toBe(RAIL_TITLE_ID)
    expect(list?.attributes.get('aria-labelledby')).toBe(RAIL_TITLE_ID)
  })

  it('moves the keyboard to the issue’s heading when the row was activated from the keyboard', async () => {
    const { bridge } = railBridge([listRow('aurora', { open: true }), orbitRow()])
    const document = await boot(bridge, new FakeDocument())
    rowNamed(document, 'orbit').press('Enter')
    await flush()
    const heading = classed(panelOf(document), ISSUE_HEADLINE_CLASS)[0]
    expect(heading?.attributes.get('tabindex')).toBe('-1')
    expect(document.activeElement).toBe(heading)
  })

  it('leaves the focus where it was on a pointer click', async () => {
    const { bridge } = railBridge([listRow('aurora', { open: true }), orbitRow()])
    const document = await boot(bridge, new FakeDocument())
    rowNamed(document, 'orbit').click()
    await flush()
    const heading = classed(panelOf(document), ISSUE_HEADLINE_CLASS)[0]
    expect(document.activeElement).not.toBe(heading)
  })
})

// ---------------------------------------------------------------------------
// The graph view's own wiring (GV2.3b): the keyboard, and the two reads behind
// it.
//
// The screen next door (`graph.test.ts`) is a function of the documents it is
// handed. What is held here is the part the *entry* owns: that a redraw puts the
// keyboard back in the tree it drew -- on the element the gesture was made on,
// and on the element it was already on when no gesture caused the redraw -- that
// the graph's tablist answers the same keys the navigation's does, that a Refresh
// pressed while a read is in flight is not dropped, and that a plan the console
// could not read is not redrawn from the plan it read before it.
//
// **The focus rule is the one to mutation-prove, and the Fit case is where.**
// `redraw` takes the target every call site names; drop the one the Fit control
// passes --
//
//   redraw(screen.change({ zoom: 1 }), FIT_BUTTON)  ->  redraw(screen.change({ zoom: 1 }), null)
//
// -- and that case fails: the tree is replaced either way, and with `null` the
// keyboard is left on the control the panel no longer holds. Red before, green
// after.
// ---------------------------------------------------------------------------

/** PLAN_STATUS is the graph's own document as the wire has it: the fixture the
 * screen next door is pinned against, so a case here drives the same graph. */
const PLAN_STATUS: unknown = JSON.parse(
  readFileSync(join(CONSOLE_ROOT, 'test', 'fixtures', 'plan-status.json'), 'utf8'),
)

/** GRAPH_RUNS is the console's run list, ending with the suffixes the fixture's
 * tasks were joined to: the join that gives a node a door. */
const GRAPH_RUNS: readonly string[] = [
  '20261025-121805-aa01',
  '20261025-121805-aa02',
  '20261025-121805-aa03',
  '20261025-121805-aa04',
]

/**
 * FakeWorker is the layout protocol without a browser. The graph screen builds a
 * worker the moment the view is opened (`LayoutClient`'s constructor), and a
 * suite with no `Worker` at all drives the refusal screen rather than the graph
 * -- so every case below stubs this one in. It answers each layout as it is
 * posted, and the `register` message the client sends from its constructor is
 * ignored: the client matches a reply by request id and drops the registration's.
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

/**
 * HeldWorker is FakeWorker with every reply held until the case releases it, so
 * the moment between "the plan is read" and "the picture is drawn" can be looked
 * at: the layout is the last await of a plan read, and it is the one the operator
 * is most likely to be inside when they move on. `latest` is the worker the
 * console built, which is how the case reaches the one that is holding.
 */
class HeldWorker extends FakeWorker {
  static latest: HeldWorker | null = null

  private readonly held: (() => void)[] = []
  private open = false

  constructor() {
    super()
    HeldWorker.latest = this
  }

  override postMessage(message: unknown): void {
    if (this.open) {
      super.postMessage(message)
      return
    }
    this.held.push(() => {
      super.postMessage(message)
    })
  }

  release(): void {
    this.open = true
    for (const answer of this.held.splice(0)) answer()
  }
}

/** planStatusReply is one `plan.status` answer: the envelope `CliBackedHandlers`
 * builds, with the fixture's document inside it. */
function planStatusReply(document: unknown = PLAN_STATUS): Record<string, unknown> {
  return {
    ok: true,
    data: {
      document,
      receivedAt: '2026-10-15T12:00:00Z',
      sequence: 1,
      day: '2026-10-15',
      rolled: false,
      notices: [],
    },
  }
}

/** withFinishedRelease is the fixture with its first release finished: the two
 * tasks of that release leave the canvas until their chip is opened, which is the
 * one way a task the model holds has no card to select. */
function withFinishedRelease(): unknown {
  const document = JSON.parse(JSON.stringify(PLAN_STATUS)) as {
    plans: { releases: { version: string; finished: boolean }[] }[]
  }
  const release = document.plans[0]?.releases.find((each) => each.version === '1.4.0')
  if (release === undefined) throw new Error('the fixture no longer carries release 1.4.0')
  release.finished = true
  return document
}

/** runsReply is the console's run list, in the shape `runIdsIn` reads. */
function runsReply(): Record<string, unknown> {
  return { ok: true, data: { document: { runs: GRAPH_RUNS.map((run) => ({ run })) } } }
}

/** graphBridge is the fake bridge with the graph's two reads answered, and the
 * layout worker stubbed in: the two things a case that wants a drawn graph needs
 * and nothing else. `worker` is the layout protocol a case wants held -- see
 * `HeldWorker` -- and the default is the one that answers at once. */
function graphBridge(
  replies: Readonly<Record<string, AReply>> = {},
  worker: typeof FakeWorker = FakeWorker,
): FakeBridge {
  vi.stubGlobal('Worker', worker)
  return fakeBridge({
    replies: { planStatus: () => planStatusReply(), listRuns: () => runsReply(), ...replies },
  })
}

/** openGraph boots a console and opens the Graph view, and answers the document
 * once the plan's first read has been drawn. */
async function openGraph(bridge: Record<string, unknown>): Promise<FakeDocument> {
  const document = await boot(bridge, new FakeDocument())
  buttonFor(document, 'graph').click()
  await flush()
  return document
}

/** nodeNamed is one drawn node card or list row by the key it carries. */
function nodeNamed(document: FakeDocument, key: string): FakeNode | undefined {
  return [...classed(panelOf(document), 'graph-node'), ...classed(panelOf(document), 'graph-row')].find(
    (node) => node.attributes.get('data-key') === key,
  )
}

/** keyed is the first drawn node card's key: the door a case clicks. */
function firstKeyed(document: FakeDocument): string {
  const node = classed(panelOf(document), 'graph-node')[0]
  const key = node?.attributes.get('data-key')
  if (key === undefined || key === null) throw new Error('the graph drew no node to select')
  return key
}

/** nodeForTask is the drawn card or row for one task of the fixture plan: the
 * layout key is `<plan length>:<plan>:<task>`, so the task id is its tail. */
function nodeForTask(document: FakeDocument, task: string): FakeNode | undefined {
  return [...classed(panelOf(document), 'graph-node'), ...classed(panelOf(document), 'graph-row')].find((node) =>
    (node.attributes.get('data-key') ?? '').endsWith(`:${task}`),
  )
}

/** wordsOf is the words drawn on one task's card, in document order: what a
 * reader sees of the state, and what a live frame's stage moves. */
function wordsOf(document: FakeDocument, task: string): readonly string[] {
  const node = nodeForTask(document, task)
  if (node === undefined) throw new Error(`the graph drew no node for ${task}`)
  return everyNode(node)
    .filter((each) => each.attributes.get('data-word') !== null)
    .map((each) => each.textContent ?? '')
}

/** frameWithStage is one live frame whose row for one run suffix carries a stage:
 * the console's own `live.watch` document with the run id and the stage of its
 * first row replaced, so the frame is a frame the decoder accepts and the graph's
 * fold has a stage to fold. The suffix is what the fold joins on, and `aa02` is
 * the fixture plan's one running task (`graph.test.ts`'s own join). */
function frameWithStage(suffix: string, stage: string): Readonly<Record<string, unknown>> {
  const document = JSON.parse(JSON.stringify(syntheticDocuments()[0])) as { runs: Record<string, unknown>[] }
  const row = document.runs[0]
  if (row === undefined) throw new Error('the synthetic frame has no run row to change')
  row['run'] = `20261025-121805-${suffix}`
  row['stage'] = stage
  return document
}

describe('the graph view’s keyboard', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('puts the keyboard back in the tree a click or an arrow key just redrew', async () => {
    const { bridge } = graphBridge()
    const document = await openGraph(bridge)
    const key = firstKeyed(document)

    const clicked = nodeNamed(document, key)
    if (clicked === undefined) throw new Error('the graph drew no node to select')
    clicked.click()
    await flush()

    // The click selected the task and the tree was replaced: the keyboard has to
    // be on the node **the panel now holds**, not on the one that was taken down
    // -- a reader whose focus dropped to `<body>` on every arrow key is a reader
    // who cannot walk the picture at all (design section 4.3).
    const selected = nodeNamed(document, key)
    if (selected === undefined) throw new Error('the redraw lost the node')
    expect(selected.attributes.get('data-selected')).toBe('true')
    expect(document.activeElement, 'the focus did not land in the tree the click drew').toBe(selected)

    // And the same for the arrow keys, which move the selection along.
    const before = classed(panelOf(document), 'graph-node').map((node) => node.attributes.get('data-key'))
    selected.press('ArrowDown')
    await flush()
    const moved = before[1] ?? ''
    const next = nodeNamed(document, moved)
    if (next === undefined) throw new Error('the arrow key selected nothing')
    expect(next.attributes.get('data-selected')).toBe('true')
    expect(document.activeElement, 'the focus did not follow the arrow key').toBe(next)
    // One selection, and it is the one the arrow key moved to: the node the
    // click had selected is in the tree that was taken down.
    expect(
      classed(panelOf(document), 'graph-node').filter(
        (node) => node.attributes.get('data-selected') === 'true',
      ),
    ).toEqual([next])
  })

  it('drives the graph’s stream filter with the arrows, and takes the focus to the segment it chose', async () => {
    const { bridge } = graphBridge()
    const document = await openGraph(bridge)
    const tabs = classed(panelOf(document), 'graph-tab')
    expect(tabs.map((tab) => tab.attributes.get('data-tab'))).toEqual(['stream:example', 'stream:', 'all'])
    expect(tabs.map((tab) => tab.attributes.get('tabindex'))).toEqual(['-1', '-1', '0'])

    // Right wraps from the last tab to the first, and a tab's selection *is* the
    // graph it draws: the screen re-lays the narrowed graph out, so the redraw
    // lands a turn later and the focus follows it there.
    const right = (tabs[2] as FakeNode).press('ArrowRight')
    await flush()
    expect(right.defaultPrevented, 'the graph left its own arrow key to the window').toBe(true)
    const after = classed(panelOf(document), 'graph-tab')
    expect(after.map((tab) => tab.attributes.get('aria-pressed'))).toEqual(['true', 'false', 'false'])
    expect(after.map((tab) => tab.attributes.get('tabindex'))).toEqual(['0', '-1', '-1'])
    expect(document.activeElement, 'the ring did not follow the tab the arrow chose').toBe(after[0])

    // Home and End are the tablist's, and a key this tablist does not own -- Up
    // and Down are the *node* doors' -- is left to the window.
    const end = (after[0] as FakeNode).press('End')
    await flush()
    expect(end.defaultPrevented).toBe(true)
    const back = classed(panelOf(document), 'graph-tab')
    expect(back.map((tab) => tab.attributes.get('aria-pressed'))).toEqual(['false', 'false', 'true'])
    expect(document.activeElement).toBe(back[2])
    expect((back[2] as FakeNode).press('ArrowDown').defaultPrevented).toBe(false)
  })

  it('opens the inspector on a click and closes it on Escape, with the keyboard back on the card', async () => {
    const { bridge } = graphBridge()
    const document = await openGraph(bridge)
    const key = firstKeyed(document)

    // Nothing is selected when the graph opens, and the panel is a panel of a
    // selection: there is nothing to draw beside a canvas nobody has chosen a
    // task in.
    expect(classed(panelOf(document), 'graph-inspector')).toEqual([])

    const card = nodeNamed(document, key)
    if (card === undefined) throw new Error('the graph drew no node to select')
    card.click()
    await flush()

    const panel = classed(panelOf(document), 'graph-inspector')[0]
    expect(panel, 'the click selected the task and drew no inspector').toBeDefined()
    // Beside the canvas and not instead of it: the picture is still the picture.
    expect(classed(panelOf(document), 'graph-canvas')).toHaveLength(1)
    expect(
      [...classed(panel as FakeNode, 'graph-inspector-need'), ...classed(panel as FakeNode, 'graph-inspector-unblock')].length,
      'the panel listed neither what the task waits on nor what it unblocks',
    ).toBeGreaterThan(0)

    // Escape closes it, and the keyboard goes back to the card the panel was
    // opened from rather than to `<body>` (design section 4.3).
    const selected = nodeNamed(document, key)
    if (selected === undefined) throw new Error('the redraw lost the card the click selected')
    expect(selected.press('Escape').defaultPrevented, 'the graph left Escape to the window').toBe(true)
    await flush()
    expect(classed(panelOf(document), 'graph-inspector')).toEqual([])
    const back = nodeNamed(document, key)
    expect(document.activeElement, 'the panel closed and dropped the keyboard').toBe(back)
  })

  it('opens the inspector on Enter where the card has no run to open', async () => {
    // Enter is the card's own key and it opens the card's run -- except where
    // there is no run, and the card is exactly the one a keyboard reader needs
    // the panel for: `task-three` is the fixture's ready task with no run, which
    // is the only task a keyboard can reach the inspector through.
    const { bridge } = graphBridge()
    const document = await openGraph(bridge)
    const card = nodeForTask(document, 'task-three')
    if (card === undefined) throw new Error('the graph drew no card for the fixture’s runless task')
    expect(card.attributes.get('data-run-id'), 'the fixture’s runless task carries a run').toBeUndefined()
    expect(card.press('Enter').defaultPrevented).toBe(true)
    await flush()
    const panel = classed(panelOf(document), 'graph-inspector')[0]
    if (panel === undefined) throw new Error('Enter on a card with no run opened nothing')
    expect(textOf(panel)).toContain('A task nothing depends on')
  })

  it('selects the task an inspector button names, and keeps the keyboard in the drawing either way', async () => {
    // **The one press that can name a card the drawing does not have.** Release
    // 1.4.0 is finished here, so the canvas leaves its two tasks out until the
    // chip is open (`visibleNodes`) -- and `task-five`, which is in the open
    // release, waits on one task of each kind: `task-three` is on the canvas and
    // `task-two` is not. The press selects either of them, and the keyboard has
    // to end up somewhere it can go on from in both cases.
    const { bridge } = graphBridge({ planStatus: () => planStatusReply(withFinishedRelease()) })
    const document = await openGraph(bridge)
    const card = nodeForTask(document, 'task-five')
    if (card === undefined) throw new Error('the graph drew no card for task-five')
    card.click()
    await flush()

    const needs = classed(panelOf(document), 'graph-inspector-need')
    expect(needs.map((need) => need.attributes.get('data-key')?.endsWith(':task-three'))).toEqual([true, false])

    // `task-two` is not drawn: the keyboard goes to the panel, which is drawn and
    // is now about the task the press chose.
    const hidden = needs[1]
    if (hidden === undefined) throw new Error('the panel listed nothing this task waits on')
    hidden.click()
    await flush()
    const after = classed(panelOf(document), 'graph-inspector')[0]
    if (after === undefined) throw new Error('the panel closed under the task it was selecting')
    expect(textOf(after)).toContain('The second task of the example plan')
    expect(
      document.activeElement,
      'the press named a card that is not drawn and dropped the keyboard',
    ).toBe(after)

    // And where the card **is** drawn, the keyboard lands on it: the panel is
    // about `task-two` now, and what that task unblocks is `task-five`, which is
    // in the open release and on the canvas.
    const drawnTask = classed(panelOf(document), 'graph-inspector-unblock')[0]
    if (drawnTask === undefined) throw new Error('the panel listed nothing this task unblocks')
    drawnTask.click()
    await flush()
    const landed = nodeForTask(document, 'task-five')
    if (landed === undefined) throw new Error('the redraw lost the card the press selected')
    expect(landed.attributes.get('data-selected')).toBe('true')
    expect(document.activeElement, 'the keyboard did not land on the card the press selected').toBe(landed)
  })

  it('keeps the keyboard on the control a key pressed, and leaves it alone when it is elsewhere', async () => {
    const { bridge } = graphBridge()
    const document = await openGraph(bridge)

    // Fit and the view switch are the two controls whose redraw used to leave the
    // reader on `<body>`: both are `button`s a Tab reaches, both replace the
    // panel's tree, and a keyboard reader who pressed Enter on one has to be on
    // the one the new tree holds (design section 4.3).
    const fit = classed(panelOf(document), 'graph-fit')[0]
    if (fit === undefined) throw new Error('the graph drew no Fit control')
    fit.focus()
    fit.click()
    await flush()
    const afterFit = classed(panelOf(document), 'graph-fit')[0]
    expect(afterFit, 'the Fit control drew nothing: the case would prove nothing').not.toBe(fit)
    expect(document.activeElement, 'the Fit control dropped the keyboard').toBe(afterFit)

    const view = classed(panelOf(document), 'graph-view')[0]
    if (view === undefined) throw new Error('the graph drew no view switch')
    view.focus()
    view.click()
    await flush()
    const afterView = classed(panelOf(document), 'graph-view')[0]
    if (afterView === undefined) throw new Error('the view switch drew nothing: the case would prove nothing')
    expect(afterView).not.toBe(view)
    expect(document.activeElement, 'the view switch dropped the keyboard').toBe(afterView)
    // Back to the canvas, so the read below has a picture to draw.
    afterView.click()
    await flush()
    expect(classed(panelOf(document), 'graph-canvas').length).toBeGreaterThan(0)

    // And a redraw that no gesture caused never takes the keyboard from outside
    // the graph: the reader is on the navigation's own Graph button, and the
    // read's redraw leaves them there.
    const nav = buttonFor(document, 'graph')
    nav.focus()
    ;(classed(panelOf(document), 'graph-refresh')[0] as FakeNode).click()
    await flush()
    expect(classed(panelOf(document), 'graph-canvas').length).toBeGreaterThan(0)
    expect(document.activeElement, 'a read pulled the keyboard into the graph').toBe(nav)
  })

  it('re-queues one plan read when Refresh is pressed while a read is in flight', async () => {
    // The operator's Refresh is a read of its own, and a read that is dropped
    // because one was already outstanding is a gesture with nothing behind it --
    // which is the state a slow `plan.status` leaves the screen in. So the second
    // press asks for **one** more read, made when the first settles.
    const held: (() => void)[] = []
    let made = 0
    const { bridge, calls } = graphBridge({
      planStatus: () => {
        made += 1
        if (made !== 2) return planStatusReply()
        return new Promise((resolve) => {
          held.push(() => {
            resolve(planStatusReply())
          })
        })
      },
    })
    const document = await openGraph(bridge)
    expect(calls.filter((call) => call.name === 'planStatus')).toHaveLength(1)

    const refresh = classed(panelOf(document), 'graph-refresh')[0] as FakeNode
    refresh.click()
    await flush()
    expect(calls.filter((call) => call.name === 'planStatus')).toHaveLength(2)
    // The second press lands while the first read is outstanding.
    refresh.click()
    await flush()
    expect(calls.filter((call) => call.name === 'planStatus')).toHaveLength(2)

    // The read settles, and the read the second press asked for is made.
    ;(held[0] as () => void)()
    await flush()
    expect(calls.filter((call) => call.name === 'planStatus')).toHaveLength(3)
    // And it drew: the panel is the graph rather than the view the console was
    // showing while the read was on its way.
    expect(classed(panelOf(document), 'graph-canvas').length).toBeGreaterThan(0)
  })
})

describe('the graph’s live frame', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('draws the stage a frame carries for a task of the plan, and leaves the keyboard where it was', async () => {
    // The join GV2.3b exists for: the plan says the task is `running` and the
    // *feed* says which stage it is in, so a frame is what moves a word from
    // "implementing" to "in review". Nothing else in the console writes a stage
    // into the drawing.
    const { bridge } = graphBridge({
      subscribeLive: () => snapshotReply(frameWithStage('aa02', 'review')),
    })
    const document = await openGraph(bridge)
    // The plan's own stage, before any frame has been folded in.
    expect(wordsOf(document, 'task-two')).toContain('implementing')

    // A node is selected -- which is the state a frame's redraw is most likely to
    // interrupt, because selecting one is what puts the keyboard on the canvas.
    const selected = nodeForTask(document, 'task-one')
    if (selected === undefined) throw new Error('the graph drew no task-one')
    selected.click()
    await flush()
    const key = selected.attributes.get('data-key')

    await vi.advanceTimersByTimeAsync(LIVE_POLL_MS)
    await flush()
    // The frame reached the drawing...
    expect(wordsOf(document, 'task-two'), 'the frame’s stage never reached the node').toContain('in review')
    expect(wordsOf(document, 'task-two')).not.toContain('implementing')
    // ...and the reader is still on the task they had selected, in the tree the
    // frame drew rather than on the one it replaced.
    const stillSelected = nodeForTask(document, 'task-one')
    expect(stillSelected).not.toBe(selected)
    expect(stillSelected?.attributes.get('data-key')).toBe(key)
    expect(document.activeElement, 'the frame dropped the keyboard off the selection').toBe(stillSelected)
  })

  it('does not pull the keyboard into the graph when the reader is somewhere else', async () => {
    // A frame is not a gesture: it lands on a poll tick, whether or not anybody
    // is looking at the graph's own elements, and a redraw that focused the
    // selection would take a reader out of the navigation mid-walk.
    const { bridge } = graphBridge({
      subscribeLive: () => snapshotReply(frameWithStage('aa02', 'review')),
    })
    const document = await openGraph(bridge)
    const nav = buttonFor(document, 'graph')
    nav.focus()

    await vi.advanceTimersByTimeAsync(LIVE_POLL_MS)
    await flush()
    expect(wordsOf(document, 'task-two')).toContain('in review')
    expect(document.activeElement, 'a frame pulled the keyboard into the graph').toBe(nav)
  })

  it('keeps the rail’s state word while the graph is the view on screen', async () => {
    // The rail's word is the **read's** own and not the Live screen's: the frame
    // the graph folds is the same `live.watch` read (`pollLive` routes it to
    // `frameGraph`), so a feed that has gone quiet has to read `silent` here too.
    // The word is written whether or not the frame moved anything -- the third
    // read below brings the same frame back, which the screen answers with no
    // drawing at all, and the word still follows the read.
    const frame = frameWithStage('aa02', 'review')
    let receivedAt = nowStamp()
    const { bridge } = graphBridge({ subscribeLive: () => snapshotReply(frame, receivedAt) })
    const document = await openGraph(bridge)
    const state = (): string | null => spanOf(document, RAIL_STATE_CLASS).attributes.get('data-state') ?? null
    expect(state(), 'the mount’s own read was fresh: the case would prove nothing').toBe('running')

    receivedAt = '2026-10-15T12:00:00Z'
    await vi.advanceTimersByTimeAsync(LIVE_POLL_MS)
    await flush()
    expect(state(), 'a feed gone quiet left the rail on running').toBe('silent')

    receivedAt = nowStamp()
    await vi.advanceTimersByTimeAsync(LIVE_POLL_MS)
    await flush()
    expect(state(), 'a live feed left the rail on silent').toBe('running')
  })
})

describe('the graph’s plan read when it refuses', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('keeps the refusal on screen when the next live frame arrives', async () => {
    // A refusal is the graph screen's answer for a plan the console cannot read.
    // A frame is the *other* input (GV2.3b joins the plan to the feed's stages),
    // and a frame drawn over the refusal would put the plan the console read
    // before the refusal back on screen -- a picture that says the plan is fine
    // when the read that just failed says it is not.
    const frame = syntheticDocuments()[0] as Readonly<Record<string, unknown>>
    let refused = false
    const { bridge } = graphBridge({
      planStatus: () => (refused ? { ok: false, reason: 'the plan could not be read' } : planStatusReply()),
      subscribeLive: () => snapshotReply(frame),
    })
    const document = await openGraph(bridge)
    expect(classed(panelOf(document), 'graph-canvas').length).toBeGreaterThan(0)

    refused = true
    ;(classed(panelOf(document), 'graph-refresh')[0] as FakeNode).click()
    await flush()
    const refusal = panelOf(document).children[0] as FakeNode
    expect(refusal.attributes.get('class')).toBe('console-unavailable')

    // A poll tick: the frame is read, and it is not drawn.
    await vi.advanceTimersByTimeAsync(LIVE_POLL_MS)
    await flush()
    expect(panelOf(document).children[0], 'a frame drew the plan the refusal replaced').toBe(refusal)
    expect(classed(panelOf(document), 'graph-canvas')).toEqual([])
  })
})

// ---------------------------------------------------------------------------
// The panel a tab switch leaves behind (console 0.116.1).
// ---------------------------------------------------------------------------

/** holding is a bridge answer the case releases by hand. A promise that is
 * already resolved cannot be observed half-way through, and the state this
 * section is about -- the read is on its way and the panel must say so -- only
 * exists while one is outstanding. A released holder answers every later read
 * with the same value, so one holder can serve a read the operator makes twice. */
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

/** operationOf is the read a refusal screen names, or `null` when the panel
 * holds something that is not a refusal. */
function operationOf(document: FakeDocument): string | null {
  return classed(panelOf(document), 'unavailable-operation')[0]?.textContent ?? null
}

/** holdsText is whether any node under a root draws exactly this text. */
function holdsText(root: FakeNode, text: string): boolean {
  return everyNode(root).some((node) => node.textContent === text)
}

/** controlUnder is the one drawn button carrying exactly this label under a
 * root. The settings editor's controls carry words rather than class hooks --
 * they are drawn by the entry into a row a screen drew -- so a case finds the
 * one it means by the word the operator reads. */
function controlUnder(root: FakeNode, label: string): FakeNode {
  const found = everyNode(root).filter((node) => node.tagName === 'BUTTON' && node.textContent === label)
  expect(found, `expected exactly one control labelled ${JSON.stringify(label)}`).toHaveLength(1)
  return found[0] as FakeNode
}

/** theBox is the one input under a root: the settings editor's value box. A
 * screen's interfaces carry text and attributes and never a value, so a case
 * writes one on the node behind the drawn element, exactly as `valueOf` reads
 * it. */
function theBox(root: FakeNode): FakeNode {
  const found = everyNode(root).filter((node) => node.tagName === 'INPUT')
  expect(found, 'expected exactly one box').toHaveLength(1)
  return found[0] as FakeNode
}

/** typeValue writes what an operator would type into one control. */
function typeValue(box: FakeNode, value: string): void {
  ;(box as unknown as { value: string }).value = value
}

/** textIn is the text of the single node carrying one class under a root: the
 * drawn value of one cell of a screen this suite is reading. */
function textIn(root: FakeNode, className: string): string {
  const found = classed(root, className)
  expect(found, `expected exactly one .${className}`).toHaveLength(1)
  return found[0]?.textContent ?? ''
}

/** figureOfIn is one count cell's word and its drawn figure, in the order they
 * are drawn: the two halves are one cell, and a case reads them as a reader
 * does -- together. */
function figureOfIn(root: FakeNode, className: string): readonly [string, string] {
  const cell = classed(root, className)[0]
  if (cell === undefined) throw new Error(`no .${className}`)
  return [textIn(cell, 'coordinator-count-word'), textIn(cell, 'coordinator-count-value')]
}

/** readingOf is the panel's placeholder sentence, or `null` when the panel holds
 * something else: the one observable that says a tab is waiting rather than
 * showing an answer. */
function readingOf(document: FakeDocument): string | null {
  const panel = panelOf(document)
  const kind = (panel.children[0] as FakeNode | undefined)?.attributes.get('data-kind')
  return kind === 'reading' ? textOf(panel) : null
}

const GRAPH_REFUSAL = 'the plan read refused, in the CLI’s own words'
const BUDGET_REFUSAL = 'the budget read refused, in the CLI’s own words'
const REPORT_REFUSAL = 'the report read refused, in the CLI’s own words'
const RUNS_REFUSAL = 'the run list refused, in the CLI’s own words'
const COORDINATOR_REFUSAL = 'the coordinator read refused, in the CLI’s own words'
const INTENTS_REFUSAL = 'the declared intents read refused, in the CLI’s own words'
/** MANAGED_REFUSAL is a refused `managed status --json`, in the CLI's own words:
 * the sentence **both** of the sections that read it draw, because they are one
 * read (the managed state and the retirements). */
const MANAGED_REFUSAL = 'the managed read refused, in the CLI’s own words'
/** WINDOW_REFUSAL is a refused `runs summary --json`, in the CLI's own words: the
 * sentence the strip draws beside the figures that are not its read's. */
const WINDOW_REFUSAL = 'the window’s read refused, in the CLI’s own words'

/** AN_INTENTS_DOCUMENT is one stored declaration in the `--all` shape the
 * pane's own read prints (`coordinator intents --all --json`, K5). The person
 * who declared it is a value the store holds and never an identity anything is
 * admitted by (reconciliation finding #11), which is why the case reads it back
 * as the text it is. */
const AN_INTENTS_DOCUMENT = {
  repos: [
    {
      repo: 'owner/repo-a',
      provider: 'github',
      intents: [
        {
          id: '0123456789abcdef0123456789abcdef',
          declared_by: 'person-1',
          at: '2026-11-02T08:00:00Z',
          branch: 'feat/a-thing',
          release: '0.119.0',
          files: ['console/src/renderer/entry.ts'],
          services: [],
        },
      ],
    },
  ],
}

describe('the placeholder a tab switch draws', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('never leaves the Graph’s refusal on the panel under another tab', async () => {
    // The defect this fix is for: `show` marked the tab and started its read,
    // and the panel kept the Graph's refusal -- a screen naming one read, under
    // a tab that asked for another -- for as long as the new read took.
    const runs = holding()
    const budget = holding()
    const report = holding()
    const coordinator = holding()
    const { bridge } = graphBridge({
      planStatus: () => ({ ok: false, reason: GRAPH_REFUSAL }),
      showBudget: () => budget.reply(),
      runReport: () => report.reply(),
      // The run list is **held and never released**: no tab of this case may
      // read it, so a screen that did would answer its own refusal below rather
      // than the sentence this case is about.
      listRuns: () => runs.reply(),
      coordinatorStatus: () => coordinator.reply(),
      statsRuns: () => ({ ok: false, reason: REPORT_REFUSAL }),
    })
    const document = await openGraph(bridge)
    expect(operationOf(document), 'the Graph’s own refusal is the screen to leave behind').toBe('plan.status')
    expect(holdsText(panelOf(document), GRAPH_REFUSAL)).toBe(true)

    // ---- Spend -----------------------------------------------------------
    buttonFor(document, 'spend').click()
    await flush()
    expect(readingOf(document)).toContain('Reading Spend…')
    expect(holdsText(panelOf(document), GRAPH_REFUSAL), 'the Graph’s refusal is still on screen').toBe(false)
    expect(operationOf(document), 'another tab’s read is named on the Spend tab').toBe(null)

    // A slow read says so, once, without cancelling anything.
    await vi.advanceTimersByTimeAsync(READING_STILL_MS)
    expect(readingOf(document)).toContain(READING_STILL)

    budget.release({ ok: false, reason: BUDGET_REFUSAL })
    report.release({ ok: false, reason: REPORT_REFUSAL })
    await flush()
    expect(operationOf(document), 'the spend read draws its own refusal').toBe('spend.report')
    expect(holdsText(panelOf(document), REPORT_REFUSAL)).toBe(true)
    expect(holdsText(panelOf(document), GRAPH_REFUSAL)).toBe(false)

    // The timer is disarmed by the drawing that landed: a line armed for a
    // read nobody is waiting for cannot arrive over the screen that answered.
    await vi.advanceTimersByTimeAsync(READING_STILL_MS)
    expect(holdsText(panelOf(document), READING_STILL)).toBe(false)

    // ---- Projects --------------------------------------------------------
    // Projects draws its own frame in the click's frame and reads one document,
    // `budget show` -- which this session has already refused, so the refusal is
    // the whole of what the tab has to draw. What matters here is that the
    // Graph's sentence is not on it and that the run list is not one of the reads
    // it names (`the Projects screen reads` below holds the read it makes).
    buttonFor(document, 'projects').click()
    await flush()
    expect(operationOf(document), 'the Projects screen drew no refusal of its own').toBe('budget.show')
    expect(holdsText(panelOf(document), BUDGET_REFUSAL)).toBe(true)
    expect(holdsText(panelOf(document), GRAPH_REFUSAL)).toBe(false)
    // The panel names the reads the screen made and no other read.
    expect(holdsText(panelOf(document), 'plan.status')).toBe(false)
    expect(holdsText(panelOf(document), 'runs.list'), 'the run list is not a read of this screen').toBe(
      false,
    )

    // ---- Coordinator -----------------------------------------------------
    // The Coordinator is the one tab of these that draws a frame of its own with
    // reads still out: the source line and the console's sentence about the
    // view's age are up in the click's frame, and its sections wear the mark and
    // the shape of what is coming. The installation records no checkout, so two
    // of its four reads are made at all -- the stored view and `budget show` --
    // and the budget's answer is already in hand: this case released that holder
    // under Spend, so the part it owns is drawn from a refusal while the stored
    // view is still on its way. The jobs are the read the tab does not make on
    // entry (rule 5), so this case asks for them below, before they can refuse.
    buttonFor(document, 'coordinator').click()
    await flush()
    expect(busyLabels(document), 'the Coordinator drew no mark of its own').toContain(COORDINATOR_BUSY)
    // The Graph's sentence is gone, and what names a command on this panel is
    // the **Coordinator's own** reads: the budget read whose refusal Spend
    // released above landed in the queue's budget part, and it names
    // `budget.show` -- the command, not the tab that asked for it. The run list
    // is the one read still out, and it is the read the queue's mark is for.
    expect(operationOf(document), 'the Graph’s refusal outlived its tab').toBe('budget.show')
    expect(holdsText(panelOf(document), GRAPH_REFUSAL)).toBe(false)
    await vi.advanceTimersByTimeAsync(READING_STILL_MS)
    expect(holdsText(panelOf(document), READING_STILL), 'the mark never said the read was slow').toBe(true)

    coordinator.release({ ok: false, reason: COORDINATOR_REFUSAL })
    await flush()
    expect(operationOf(document), 'the coordinator read draws its own refusal').toBe('coordinator.status')
    // ...and the budget's refusal did not go with it: they are two reads, and
    // the queue draws one part each.
    expect(classed(panelOf(document), 'unavailable-operation').map((node) => node.textContent)).toContain(
      'budget.show',
    )
    expect(holdsText(panelOf(document), COORDINATOR_REFUSAL)).toBe(true)
    expect(holdsText(panelOf(document), GRAPH_REFUSAL)).toBe(false)

    // The jobs are read when the operator asks for them and not before: the
    // press is what makes `runs ls` go out, and its refusal is drawn in the part
    // the control was drawn in.
    buttonLabelled(panelOf(document), COORDINATOR_QUEUE_JOBS_WORD).click()
    runs.release({ ok: false, reason: RUNS_REFUSAL })
    await flush()
    // The run list was the last read made, and each refusal is where its own
    // read belongs: the document's in the queue's first part, the run list's in
    // the second, the budget's in the fourth. The third -- the plan tasks -- is
    // **not a refusal but a note**: this installation records no checkout, so
    // `plan status` was never asked and the part says the tasks are not known.
    const parts = classed(panelOf(document), COORDINATOR_QUEUE_PART_CLASS)
    expect(
      parts.map((part) => classed(part, 'unavailable-operation')[0]?.textContent ?? null),
      'a part drew a refusal for a read that is not its own',
    ).toEqual(['coordinator.status', 'runs.list', null, 'budget.show'])
    expect(holdsText(panelOf(document), COORDINATOR_REFUSAL)).toBe(true)
    expect(holdsText(panelOf(document), RUNS_REFUSAL)).toBe(true)
    // Every read behind every section has answered, so no section is waiting:
    // there is no spinner left on a queue the console has finished reading.
    expect(busyLabels(document)).toEqual([])
  })

  it('asks the operator to read the tab by the word the navigation gave it', () => {
    // The placeholder names the tab the operator just pressed, so the word is
    // the button's own and not a second name for the same view: "Reading Caps…"
    // under a tab labelled Project would be the console asking its reader to
    // check a name it drew itself.
    const words: Readonly<Record<string, string>> = READING_WORDS
    expect(Object.keys(words)).toHaveLength(VIEWS.length)
    for (const entry of VIEWS) {
      expect(words[entry.id], `the ${entry.id} view has no placeholder word`).toBe(entry.label)
    }
  })

  it('names the projects tab Project, the settings of this one project', async () => {
    // A Fugaro project is one installation -- many repositories and people, one
    // GCP project, one Firebase -- and this screen is the settings of *this*
    // one, so the tab and the word it reads by say Project (the owner's ruling
    // of 2026-10-31). What is renamed is the visible word alone: the view's own
    // id is `projects`, and so are the module, the classes and the `data-view`
    // this test finds the button by.
    const { bridge } = everyReadHeld()
    const document = await boot(bridge, new FakeDocument())
    expect(buttonFor(document, 'projects').textContent).toBe('Project')
    expect(navButtons(document).map((button) => button.textContent)).not.toContain('Projects')
    expect(READING_WORDS.projects).toBe('Project')
  })

  it('draws the placeholder for the Graph only when there is nothing to redraw', async () => {
    const plan = holding()
    const budget = holding()
    const report = holding()
    const { bridge } = graphBridge({
      planStatus: () => plan.reply(),
      // Spend's two reads are held rather than answered: the case is about the
      // placeholder a switch puts up **while** a read is out, and a refusal that
      // landed in the same turn would draw over it before the case could look.
      showBudget: () => budget.reply(),
      runReport: () => report.reply(),
    })
    const document = await boot(bridge, new FakeDocument())

    // The first entry into the Graph has no picture to put back up.
    buttonFor(document, 'graph').click()
    await flush()
    expect(readingOf(document)).toContain('Reading Graph…')
    await vi.advanceTimersByTimeAsync(READING_STILL_MS)
    expect(readingOf(document)).toContain(READING_STILL)

    plan.release(planStatusReply())
    await flush()
    expect(readingOf(document)).toBe(null)
    expect(classed(panelOf(document), 'graph-canvas').length).toBeGreaterThan(0)

    // A Graph that holds a picture keeps it: leaving and coming back redraws the
    // plan this file already read rather than taking it down to say it is
    // reading it again.
    buttonFor(document, 'spend').click()
    await flush()
    expect(readingOf(document)).toContain('Reading Spend…')
    buttonFor(document, 'graph').click()
    await flush()
    expect(readingOf(document)).toBe(null)
    expect(classed(panelOf(document), 'graph-canvas').length).toBeGreaterThan(0)
  })

  it('draws the placeholder for Live when this console has not polled yet', async () => {
    // A console that opened on an issue has drawn no Live screen, and the poll
    // that draws the first one is a read like any other.
    const frame = syntheticDocuments()[0] as Readonly<Record<string, unknown>>
    const live = holding()
    const { bridge } = fakeBridge({
      replies: {
        subscribeLive: () => live.reply(),
        showBudget: () => ({ ok: false, reason: BUDGET_REFUSAL }),
      },
    })
    const document = new FakeDocument()
    const booted = bootDashboard({
      fugaro: bridge,
      document: document as unknown as Document,
    })
    await flush()
    live.release(snapshotReply(frame, nowStamp()))
    await booted
    await flush()
    expect(classed(panelOf(document), 'live').length).toBeGreaterThan(0)

    // The issue view takes the panel without a Live read; the tab comes back to
    // a console that holds no frame.
    const rail = railBridge([listRow('aurora', { open: true }), orbitRow()])
    void rail
    expect(readingOf(document), 'the Live screen this console holds is not a placeholder').toBe(null)
  })
})

// ---------------------------------------------------------------------------
// The skeleton a tab draws while its data is on its way (console 0.116.1).
//
// The owner, 2026-10-31: "when changing tabs in the Console we should show a
// skeleton while we load the data, no?" The placeholder's own sentence says a
// tab is waiting, and the shapes beside it are what the panel is going to be:
// one wrapper per section of that tab (`renderer/skeleton.ts`'s
// `SKELETON_SHAPES`), replaced section by section as the reads land. The
// helper's own claims -- what a block is, that the tree says nothing to a reader
// and that the heights are the vocabulary's -- are `skeleton.test.ts`'s, and the
// shimmer's two arms are `graph-motion.test.ts`'s.
// ---------------------------------------------------------------------------

/** skeletonsOf is the panel's skeletons in document order, as the section each
 * one stands in for. */
function skeletonsOf(document: FakeDocument): readonly string[] {
  return classed(panelOf(document), SKELETON_CLASS).map((node) => node.attributes.get('data-section') ?? '')
}

/** skeletonOf is one section's skeleton, or `null` when that section is not
 * waiting on anything. */
function skeletonOf(document: FakeDocument, section: string): FakeNode | null {
  return (
    classed(panelOf(document), SKELETON_CLASS).find(
      (node) => node.attributes.get('data-section') === section,
    ) ?? null
  )
}

/** blocksOf is one section's blocks, as the kind each one is drawn as. */
function blocksOf(document: FakeDocument, section: string): readonly string[] {
  const wrapper = skeletonOf(document, section)
  if (wrapper === null) return []
  return classed(wrapper, SKELETON_BLOCK_CLASS).map((node) => node.attributes.get('data-shape') ?? '')
}

/** everyReadHeld is a bridge whose reads all hold, which is what a case about a
 * wait needs: a tab that has just been clicked stays on its placeholder for as
 * long as the case says. */
function everyReadHeld(): FakeBridge {
  return graphBridge({
    planStatus: () => holding().reply(),
    showBudget: () => holding().reply(),
    runReport: () => holding().reply(),
    listRuns: () => holding().reply(),
    listModels: () => holding().reply(),
    coordinatorStatus: () => holding().reply(),
  })
}

describe('the skeleton a tab draws while its data is on its way', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  /** ownShape is the one section of a tab that draws **its own frame** in the
   * click's frame rather than the panel's placeholder: Projects (`budget show`)
   * and the Coordinator (the stored view), which have something true to draw
   * before their read answers -- the command the panel is reading and, for the
   * Coordinator, the console's sentence about the view's age -- so a placeholder
   * standing in for the whole screen would be a shape over a header the console
   * already knows. The other three wait as a panel, and their sections are
   * `SKELETON_SHAPES[tab]`.
   *
   * **The Coordinator is a list and not one shape** (console 0.118.0, K1): its
   * frame holds three sections, each with a read of its own -- the action queue,
   * the fleet's totals and the panel of repositories -- and all three are empty
   * in the click's frame, so all three wear the shape of what is coming. That is
   * the owner's rule of 2026-10-31 drawn: four reads that start together and a
   * section per read, rather than the whole tab behind its slowest. */
  const ownShape = (tab: string): readonly SkeletonShape[] | null => {
    if (tab === 'projects') return [PROJECTS_SHAPE, PROJECTS_MODELS_SHAPE]
    if (tab === 'coordinator') return SKELETON_SHAPES.coordinator
    return null
  }

  it('draws each tab’s sections, in the order the tab draws them, in the click’s own frame', async () => {
    for (const tab of Object.keys(READING_WORDS) as readonly (keyof typeof READING_WORDS)[]) {
      const { bridge } = everyReadHeld()
      const document = await boot(bridge, new FakeDocument())
      buttonFor(document, tab).click()
      // **Nothing is awaited**: the whole point of the shape is that it is up in
      // the frame the click happened in, not a turn later when the read the
      // click started has already answered in a test.
      const own = ownShape(tab)
      expect(skeletonsOf(document), `the ${tab} tab drew no skeleton in the click’s own frame`).toEqual(
        (own ?? SKELETON_SHAPES[tab]).map((shape) => shape.section),
      )
      // The sentence a reader is told is still drawn -- visually hidden beside
      // the shapes rather than printed over them -- and the blocks hold the shape
      // of the section they stand in for. A tab that draws its own frame says
      // what it is reading in that frame's own mark instead: the mark's label is
      // the sentence, and the frame's source line is above it.
      if (own === null) {
        expect(readingOf(document), `the ${tab} placeholder lost its sentence`).toContain(
          `Reading ${READING_WORDS[tab]}…`,
        )
      } else {
        expect(busyLabels(document), `the ${tab} tab drew no mark of its own`).toContain(
          tab === 'projects' ? PROJECTS_BUSY : COORDINATOR_BUSY,
        )
        const root = classed(panelOf(document), tab)[0]
        expect(root, `the ${tab} tab drew no screen of its own`).toBeDefined()
        expect(
          everyNode(root as FakeNode).some(
            (node) => node.attributes.get(BUSY_BUSY_ATTRIBUTE) === 'true',
          ),
          `the ${tab} screen’s own section is not marked busy`,
        ).toBe(true)
      }
      for (const shape of own ?? SKELETON_SHAPES[tab]) {
        expect(blocksOf(document, shape.section), `${tab}'s ${shape.section} drew the wrong blocks`).toEqual([
          ...shape.blocks,
        ])
      }
      await flush()
    }
  })

  it('replaces one section’s skeleton the moment that section’s read lands, and no other', async () => {
    // Spend is the tab whose two reads answer apart, which is what makes the
    // per-section half of the owner's rule visible: the caps are drawn from their
    // own answer while the report is still waiting, and the report's shape -- and
    // its mark -- stay exactly where they were.
    const caps = holding()
    const report = holding()
    const { bridge } = graphBridge({
      planStatus: () => holding().reply(),
      showBudget: () => caps.reply(),
      runReport: () => report.reply(),
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'spend').click()
    expect(skeletonsOf(document)).toEqual(['caps', 'report'])

    caps.release({ ok: true, data: { document: {} } })
    await flush()
    // The caps landed: their section is the shell's own, drawn from the answer,
    // and its skeleton is gone with the wait it stood for.
    expect(sectionOf(document, 'spend-caps').getAttribute('data-available')).toBe('true')
    expect(skeletonOf(document, 'caps')).toBeNull()
    // The report is still waiting, and that is the whole difference: one
    // section's answer is not the other section's, and a panel that replaced
    // both when the first landed would be the all-or-nothing redraw the owner's
    // rule of 2026-10-31 refuses.
    expect(skeletonsOf(document), 'the report’s skeleton went with the caps’ answer').toEqual(['report'])
    expect(blocksOf(document, 'report')).toEqual([...SKELETON_SHAPES.spend[1]!.blocks])
    expect(sectionOf(document, 'spend-report').getAttribute(BUSY_ATTRIBUTE)).toBe('true')

    report.release({ ok: true, data: { document: reportDoc('2026-10-01') } })
    await flush()
    expect(skeletonsOf(document)).toEqual([])
    expect(theSpendView(document)).toEqual(['2026-10-01'])
  })

  it('draws a refused section’s own sentence instead of a skeleton', async () => {
    // A refusal is the section's answer: the read was made and it failed, so
    // there is nothing coming and nothing to hold the shape of.
    const report = holding()
    const { bridge } = graphBridge({
      planStatus: () => holding().reply(),
      showBudget: () => ({ ok: false, reason: BUDGET_REFUSAL }),
      runReport: () => report.reply(),
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'spend').click()
    expect(skeletonsOf(document)).toEqual(['caps', 'report'])

    await flush()
    expect(holdsText(sectionOf(document, 'spend-caps'), BUDGET_REFUSAL)).toBe(true)
    expect(skeletonOf(document, 'caps'), 'a section that refused is not a section that is waiting').toBeNull()
    expect(skeletonsOf(document)).toEqual(['report'])
  })

  it('takes the shape down with the placeholder when the tab’s whole read lands', async () => {
    const plan = holding()
    const { bridge } = graphBridge({
      planStatus: () => plan.reply(),
      showBudget: () => holding().reply(),
      runReport: () => holding().reply(),
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'graph').click()
    expect(skeletonsOf(document)).toEqual(SKELETON_SHAPES.graph.map((shape) => shape.section))

    plan.release(planStatusReply())
    await flush()
    expect(readingOf(document), 'the Graph landed and the placeholder is still up').toBe(null)
    expect(skeletonsOf(document), 'the skeleton outlived the placeholder it was drawn for').toEqual([])
    expect(classed(panelOf(document), 'graph-canvas').length).toBeGreaterThan(0)
  })
})

// ---------------------------------------------------------------------------
// Spend's two sections, the mark a control change puts up, and the session
// cache (console 0.116.1).
//
// The owner's rule of 2026-10-31 -- "we need to render parts of screen as soon
// as we get that data, not wait for it all before we show anything" -- is a rule
// about *when* each read's answer reaches the screen, and this is the entry's
// half of it: the mark that goes up in the click's own frame, the token that
// keeps the latest change's view, the two reads in flight together, and the
// cache that draws a view this session already read without asking again. The
// helper's own claims -- what the mark draws, what the cache evicts -- are
// `section.test.ts`'s.
// ---------------------------------------------------------------------------

/** reportDoc is a `report --json` document whose `since` is the day the case
 * chose: the header draws the document's own range, so a case can tell one
 * window's view from another's on screen without reading a figure. */
function reportDoc(since: string, by = 'day'): Readonly<Record<string, unknown>> {
  return {
    project: 'acme',
    source: 'firestore',
    degraded: false,
    by,
    since,
    until: '2026-10-31',
    weeks: 'iso-utc-monday',
    rows: [],
  }
}

/**
 * windowReads is the fake CLI's report read with one held promise per window.
 *
 * The order the answers come back in is the whole of what these cases are about,
 * and one shared promise cannot be in two orders -- so each window gets its own,
 * released by name. A window a case has already released answers every later
 * read at once, which is what lets one holder serve a window chosen twice.
 */
function windowReads(): {
  readonly reply: (args: unknown) => unknown
  readonly release: (since: string, document: Readonly<Record<string, unknown>>) => void
} {
  const held = new Map<string, ReturnType<typeof holding>>()
  const one = (since: string): ReturnType<typeof holding> => {
    const found = held.get(since)
    if (found !== undefined) return found
    const made = holding()
    held.set(since, made)
    return made
  }
  return {
    reply: (args) => one(String((args as { readonly since?: unknown }).since ?? '')).reply(),
    release: (since, document) => {
      one(since).release({ ok: true, data: { document } })
    },
  }
}

/**
 * readQueue is one bridge read with a held promise per call, in the order the
 * calls are made.
 *
 * `windowReads` keys its promises by window, which cannot tell one read of a
 * window from the next read of that same window -- and that is exactly what a
 * case about two visits is looking at: the console asks for the same view twice,
 * and the answer that arrives may be the first call's.
 */
function readQueue(): {
  readonly reply: () => unknown
  readonly release: (index: number, value: unknown) => void
} {
  const held: ReturnType<typeof holding>[] = []
  return {
    reply: () => {
      const made = holding()
      held.push(made)
      return made.reply()
    },
    release: (index, value) => {
      const found = held[index]
      if (found === undefined) throw new Error(`the read was made ${held.length} times, not ${index + 1}`)
      found.release(value)
    },
  }
}

/** spendBridge is the fake bridge with the two reads Spend makes: the report is
 * the case's, and `budget show` answers at once unless the case says otherwise. */
function spendBridge(replies: Readonly<Record<string, AReply>>): FakeBridge {
  const frame = syntheticDocuments()[0] as Readonly<Record<string, unknown>>
  return fakeBridge({
    replies: {
      subscribeLive: () => snapshotReply(frame),
      showBudget: () => ({ ok: true, data: { document: {} } }),
      ...replies,
    },
  })
}

/** openSpend boots a console, opens Spend and answers once the frame the first
 * of the two reads mounts is up. */
async function openSpend(bridge: Record<string, unknown>): Promise<FakeDocument> {
  const document = await boot(bridge, new FakeDocument())
  buttonFor(document, 'spend').click()
  await flush()
  return document
}

/** sectionOf is one of Spend's two sections: the element its own read marks busy
 * and dims. */
function sectionOf(document: FakeDocument, className: string): FakeNode {
  const found = classed(panelOf(document), className)[0]
  if (found === undefined) throw new Error(`the Spend screen drew no ${className} section`)
  return found
}

/** windowButton is one window of the range picker. */
function windowButton(document: FakeDocument, word: string): FakeNode {
  const found = classed(panelOf(document), 'spend-window').find((node) => node.textContent === word)
  if (found === undefined) throw new Error(`the range picker offers no ${word} window`)
  return found
}

/** busyLabels is what the marks on screen say they are reading, in document
 * order: the window and the breakdown a report read is running for. */
function busyLabels(document: FakeDocument): readonly string[] {
  return classed(panelOf(document), BUSY_LABEL_CLASS).map((node) => node.textContent ?? '')
}

/** stamped is the panel's "as of HH:MM" line, or `null` when what is on screen
 * came out of a read rather than out of the session cache. It is read from the
 * words' own element (`span.console-stamp-at`) and not from the line around
 * them: the line also holds the refresh control, and a DOM node's `textContent`
 * is its whole subtree's, so the line answers `as of 09:05Refresh`. */
function stamped(document: FakeDocument): string | null {
  return classed(panelOf(document), STAMP_AT_CLASS)[0]?.textContent ?? null
}

/** theSpendView is the range the report section says it is drawn for: the
 * document's own `since`, which is what tells one window's view from another's. */
function theSpendView(document: FakeDocument): readonly (string | null)[] {
  return classed(panelOf(document), 'spend-since').map((node) => node.textContent)
}

describe('the sections Spend fills as their own reads land', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('opens on the recent week by day, and offers the wider windows beside it', async () => {
    // The owner's choice for 0.118.0: Spend opens on 7d rather than on the
    // month the CLI's own `--since` defaults to. The picker keeps every window
    // it offered, so the wider ranges are one click away.
    const windows = windowReads()
    const { bridge, calls } = spendBridge({ runReport: windows.reply })
    const document = await openSpend(bridge)
    expect(
      calls.filter((call) => call.name === 'runReport').map((call) => call.args),
      'the screen did not open on the recent week by day',
    ).toEqual([{ by: 'day', since: '7d' }])
    windows.release('7d', reportDoc('2026-10-24'))
    await flush()
    expect(theSpendView(document)).toEqual(['2026-10-24'])
    expect(classed(panelOf(document), 'spend-window').map((node) => node.textContent)).toEqual([
      '7d',
      '30d',
      '90d',
      '12w',
    ])

    // And 30d is still read as 30d: the default moved, the picker's windows did
    // not.
    windowButton(document, '30d').click()
    await flush()
    expect(
      calls.filter((call) => call.name === 'runReport').map((call) => call.args),
    ).toContainEqual({ by: 'day', since: '30d' })
  })

  it('marks the report section in the click’s own frame, over the figures it already drew', async () => {
    const windows = windowReads()
    const { bridge } = spendBridge({ runReport: windows.reply })
    const document = await openSpend(bridge)
    windows.release('7d', reportDoc('2026-10-01'))
    await flush()
    expect(theSpendView(document)).toEqual(['2026-10-01'])
    expect(classed(panelOf(document), BUSY_CLASS)).toEqual([])

    windowButton(document, '90d').click()
    // Nothing is awaited: this is the frame the click happened in, which is the
    // whole of the owner's complaint -- "it feels like nothing happens".
    const report = sectionOf(document, 'spend-report')
    expect(report.getAttribute(BUSY_ATTRIBUTE)).toBe('true')
    expect(report.getAttribute(BUSY_BUSY_ATTRIBUTE)).toBe('true')
    expect(classed(report, BUSY_SPINNER_CLASS)).toHaveLength(1)
    expect(busyLabels(document)).toEqual(['Reading spend for 90d, by day…'])
    // The figures already drawn stay under the mark: a section that blanked
    // would lose its answer to say it is reading another.
    expect(theSpendView(document)).toEqual(['2026-10-01'])
    // The caps are not the report's read: a control change never makes the
    // section that has already answered look busy.
    expect(sectionOf(document, 'spend-caps').getAttribute(BUSY_ATTRIBUTE)).toBeNull()

    windows.release('90d', reportDoc('2026-08-02'))
    await flush()
    expect(report.getAttribute(BUSY_ATTRIBUTE)).toBeNull()
    expect(classed(panelOf(document), BUSY_CLASS)).toEqual([])
    expect(theSpendView(document)).toEqual(['2026-08-02'])
  })

  it('keeps the second view when the read asked for first answers last', async () => {
    const windows = windowReads()
    const { bridge } = spendBridge({ runReport: windows.reply })
    const document = await openSpend(bridge)
    windows.release('7d', reportDoc('2026-10-01'))
    await flush()

    windowButton(document, '30d').click()
    windowButton(document, '90d').click()
    expect(busyLabels(document)).toEqual(['Reading spend for 90d, by day…'])

    // The earlier read answers last. It is not this section's any more, and
    // drawing it would put the window the operator has already replaced under a
    // picker that marks the later one.
    windows.release('30d', reportDoc('2026-10-01'))
    await flush()
    expect(busyLabels(document), 'the superseded read took the latest change’s mark down').toEqual([
      'Reading spend for 90d, by day…',
    ])
    expect(sectionOf(document, 'spend-report').getAttribute(BUSY_ATTRIBUTE)).toBe('true')
    expect(theSpendView(document)).toEqual(['2026-10-01'])

    windows.release('90d', reportDoc('2026-08-02'))
    await flush()
    expect(theSpendView(document)).toEqual(['2026-08-02'])
    expect(classed(panelOf(document), BUSY_CLASS)).toEqual([])
  })

  it('says a section is still reading when its read outlives fifteen seconds', async () => {
    const windows = windowReads()
    const { bridge } = spendBridge({ runReport: windows.reply })
    const document = await openSpend(bridge)
    windows.release('7d', reportDoc('2026-10-01'))
    await flush()

    windowButton(document, '90d').click()
    await vi.advanceTimersByTimeAsync(READING_STILL_MS - 1)
    expect(classed(panelOf(document), BUSY_STILL_CLASS)).toEqual([])
    await vi.advanceTimersByTimeAsync(1)
    expect(classed(panelOf(document), BUSY_STILL_CLASS).map((node) => node.textContent)).toEqual([
      READING_STILL,
    ])
    // It is the report section's line and not the caps': the caps answered a
    // long time ago and are not waiting for anything.
    expect(classed(sectionOf(document, 'spend-caps'), BUSY_STILL_CLASS)).toEqual([])

    windows.release('90d', reportDoc('2026-08-02'))
    await flush()
    expect(classed(panelOf(document), BUSY_STILL_CLASS)).toEqual([])
  })

  it('paints the caps section while the report read never answers', async () => {
    const never = holding()
    const { bridge } = spendBridge({ runReport: () => never.reply() })
    const document = await openSpend(bridge)

    const caps = sectionOf(document, 'spend-caps')
    expect(caps.getAttribute('data-available')).toBe('true')
    expect(caps.getAttribute(BUSY_ATTRIBUTE)).toBeNull()
    expect(classed(caps, BUSY_CLASS)).toEqual([])
    // The report's section is the one that is busy, and it names the read that
    // is running rather than only that one is.
    expect(sectionOf(document, 'spend-report').getAttribute(BUSY_ATTRIBUTE)).toBe('true')
    expect(busyLabels(document)).toEqual(['Reading spend for 7d, by day…'])
    expect(theSpendView(document)).toEqual([])
  })

  it('draws the caps answer that follows a refusal, and not the refusal it replaced', async () => {
    // A refusal is about **one answer** and not about the installation: the read
    // is made again on every visit, and the sentence the section draws from used
    // to outlive the read that set it -- `forgetLive` was the only thing that
    // cleared it, so a `budget show` the CLI refused once left the caps refusing
    // for the rest of the session, whatever the next answer said.
    const answers: ReturnType<typeof holding>[] = []
    const fake = spendBridge({
      showBudget: () => {
        const answer = holding()
        answers.push(answer)
        return answer.reply()
      },
      runReport: () => ({ ok: true, data: { document: reportDoc('2026-10-01') } }),
    })
    const document = await openSpend(fake.bridge)
    answers[0]?.release({ ok: false, reason: BUDGET_REFUSAL })
    await flush()
    expect(
      holdsText(sectionOf(document, 'spend-caps'), BUDGET_REFUSAL),
      'the refused read drew no refusal: the case proves nothing',
    ).toBe(true)
    // And the refusal names its own read: the caps' is `budget show`, which is
    // what tells the two sections' refusals apart on the panel.
    expect(operationOf(document)).toBe('budget.show')

    buttonFor(document, 'live').click()
    await flush()
    buttonFor(document, 'spend').click()
    answers[1]?.release({ ok: true, data: { document: {} } })
    await flush()
    const caps = sectionOf(document, 'spend-caps')
    expect(holdsText(caps, BUDGET_REFUSAL), 'the refusal outlived the answer that replaced it').toBe(false)
    // `data-available` is `fillSpendCaps`'s own attribute, so it says the section
    // was drawn from an answer rather than from the sentence beside it.
    expect(caps.getAttribute('data-available')).toBe('true')
    expect(caps.getAttribute(BUSY_ATTRIBUTE)).toBeNull()
  })

  it('starts `budget show` and the report together, and neither waits for the other', async () => {
    const caps = holding()
    const report = holding()
    const fake = spendBridge({ showBudget: () => caps.reply(), runReport: () => report.reply() })
    const document = await openSpend(fake.bridge)

    // Both reads were made in the frame the tab was pressed in, and neither has
    // answered: a screen that asked for the report and then for the caps would
    // have one call here and not two.
    expect(
      fake.calls
        .filter((call) => call.name === 'showBudget' || call.name === 'runReport')
        .map((call) => call.name),
    ).toEqual(['showBudget', 'runReport'])

    caps.release({ ok: true, data: { document: {} } })
    await flush()
    expect(sectionOf(document, 'spend-caps').getAttribute('data-available')).toBe('true')
    expect(sectionOf(document, 'spend-report').getAttribute(BUSY_ATTRIBUTE)).toBe('true')

    report.release({ ok: true, data: { document: reportDoc('2026-10-01') } })
    await flush()
    expect(sectionOf(document, 'spend-report').getAttribute(BUSY_ATTRIBUTE)).toBeNull()
    expect(theSpendView(document)).toEqual(['2026-10-01'])
  })

  it('keeps the report’s refusal in its own section when the caps land after it', async () => {
    // A refusal is one section's, not the panel's. Drawn over the whole panel it
    // would be replaced by the caps the moment they landed -- which are their own
    // read and are still on their way -- and the report section would then be
    // marked as reading with no read in flight for it at all.
    const caps = holding()
    const { bridge } = spendBridge({
      showBudget: () => caps.reply(),
      runReport: () => ({ ok: false, reason: REPORT_REFUSAL }),
    })
    const document = await openSpend(bridge)

    expect(operationOf(document), 'the report refused without naming its own read').toBe('spend.report')
    expect(holdsText(sectionOf(document, 'spend-report'), REPORT_REFUSAL)).toBe(true)
    expect(sectionOf(document, 'spend-caps').getAttribute(BUSY_ATTRIBUTE)).toBe('true')

    caps.release({ ok: true, data: { document: {} } })
    await flush()
    expect(operationOf(document), 'the caps took the report’s refusal down with them').toBe('spend.report')
    expect(holdsText(sectionOf(document, 'spend-report'), REPORT_REFUSAL)).toBe(true)
    expect(sectionOf(document, 'spend-caps').getAttribute('data-available')).toBe('true')
    // And nothing on the screen says a report is being read: there is no read
    // for one, and the section that refused is not the section that is busy.
    expect(sectionOf(document, 'spend-report').getAttribute(BUSY_ATTRIBUTE)).toBeNull()
    expect(busyLabels(document)).toEqual([])
  })

  it('never takes the current visit’s busy state down with a read from an earlier one', async () => {
    // Two visits, two report reads, and the one the operator left behind answers
    // first. The busy state belongs to the section helper rather than to the read
    // that set it, so ending it on behalf of the older read would leave the
    // section the operator is looking at saying it is not reading -- with a read
    // really out for it -- and would cancel its fifteen-second line.
    const reads = readQueue()
    const { bridge } = spendBridge({ runReport: reads.reply })
    const document = await openSpend(bridge)

    buttonFor(document, 'live').click()
    await flush()
    buttonFor(document, 'spend').click()
    await flush()
    const report = sectionOf(document, 'spend-report')
    expect(report.getAttribute(BUSY_ATTRIBUTE), 'the second visit is not reading: the case proves nothing').toBe(
      'true',
    )
    expect(busyLabels(document)).toEqual(['Reading spend for 7d, by day…'])

    reads.release(0, { ok: true, data: { document: reportDoc('2026-10-01') } })
    await flush()
    expect(report.getAttribute(BUSY_ATTRIBUTE), 'a read from an earlier visit ended the current one’s').toBe('true')
    expect(report.getAttribute(BUSY_BUSY_ATTRIBUTE)).toBe('true')
    expect(theSpendView(document), 'a view for a visit that is over was drawn').toEqual([])
    await vi.advanceTimersByTimeAsync(READING_STILL_MS)
    expect(classed(report, BUSY_STILL_CLASS), 'the fifteen-second line was cancelled with it').toHaveLength(1)

    reads.release(1, { ok: true, data: { document: reportDoc('2026-10-01') } })
    await flush()
    expect(theSpendView(document)).toEqual(['2026-10-01'])
    expect(classed(panelOf(document), BUSY_CLASS)).toEqual([])
  })

  it('never lets a caps read from an earlier visit answer for the one that is out', async () => {
    // The caps are read on every visit, so two of those reads can be out at once.
    // The older one belongs to a visit that is over: it may fill the rail and the
    // gauge, which are facts about the installation, but it must not settle the
    // section's token -- which would take down the mark of the read that is
    // really in flight and draw a section that has not answered.
    const reads = readQueue()
    const { bridge } = spendBridge({
      showBudget: reads.reply,
      runReport: () => ({ ok: true, data: { document: reportDoc('2026-10-01') } }),
    })
    const document = await openSpend(bridge)

    buttonFor(document, 'live').click()
    await flush()
    buttonFor(document, 'spend').click()
    await flush()
    const caps = sectionOf(document, 'spend-caps')
    expect(caps.getAttribute(BUSY_ATTRIBUTE), 'the second visit is not reading the caps: the case proves nothing').toBe(
      'true',
    )

    reads.release(0, { ok: true, data: { document: {} } })
    await flush()
    expect(caps.getAttribute(BUSY_ATTRIBUTE), 'a caps read from an earlier visit answered the current one').toBe('true')

    reads.release(1, { ok: true, data: { document: {} } })
    await flush()
    expect(caps.getAttribute(BUSY_ATTRIBUTE)).toBeNull()
    expect(caps.getAttribute('data-available')).toBe('true')
  })

  it('reads the by-person runs only after the report it belongs to has landed, and with `since`', async () => {
    const people = holding()
    const fake = spendBridge({
      runReport: (args) =>
        (args as { readonly by?: unknown }).by === 'person'
          ? people.reply()
          : { ok: true, data: { document: reportDoc('2026-10-01') } },
      statsRuns: () => ({ ok: true, data: { document: { rows: [], totals: [], notes: [] } } }),
    })
    const document = await openSpend(fake.bridge)
    expect(
      fake.calls.filter((call) => call.name === 'statsRuns'),
      'the knob is off: no runs read is made',
    ).toEqual([])

    const knob = classed(panelOf(document), 'spend-persons')[0]
    expect(knob, 'the spend screen drew no person knob: the case proves nothing').toBeDefined()
    knob?.click()
    await flush()
    // The person report is still out, and the aggregate that belongs to it has
    // not been asked for: it is read from the report's answer, not beside it.
    expect(fake.calls.filter((call) => call.name === 'statsRuns')).toEqual([])

    people.release({ ok: true, data: { document: reportDoc('2026-10-01', 'person') } })
    await flush()
    const stats = fake.calls.filter((call) => call.name === 'statsRuns')
    expect(stats).toHaveLength(1)
    expect((stats[0]?.args as { readonly since?: unknown }).since).toBe('7d')
  })
})

describe('the view a session already read', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  /** readSpendOnce boots onto Spend and answers the first report, and answers
   * what the case needs to make its claims about what comes next. */
  async function readSpendOnce() {
    const windows = windowReads()
    const fake = spendBridge({ runReport: windows.reply })
    const document = await openSpend(fake.bridge)
    windows.release('7d', reportDoc('2026-10-01'))
    await flush()
    expect(theSpendView(document)).toEqual(['2026-10-01'])
    return { fake, document, windows }
  }

  it('draws it instantly, stamped, and reads no report for it', async () => {
    const { fake, document } = await readSpendOnce()
    const reads = fake.calls.filter((call) => call.name === 'runReport').length

    buttonFor(document, 'live').click()
    await flush()
    buttonFor(document, 'spend').click()
    // Nothing is awaited: a cached view is drawn in the click's own frame, which
    // is the whole of what the cache is for.
    expect(theSpendView(document)).toEqual(['2026-10-01'])
    expect(stamped(document)).toMatch(/^as of \d\d:\d\d$/)
    // The report section is not the busy one: the view on screen is one this
    // session has already read. The caps section is, because the budget is read
    // again on every visit -- a fact about the installation, not about the view.
    expect(sectionOf(document, 'spend-report').getAttribute(BUSY_ATTRIBUTE)).toBeNull()
    expect(sectionOf(document, 'spend-caps').getAttribute(BUSY_ATTRIBUTE)).toBe('true')
    await flush()
    expect(
      fake.calls.filter((call) => call.name === 'runReport').length,
      'a view that came out of the cache was read again',
    ).toBe(reads)

    // The stamp's own control is the door out of the cache, and it reads the
    // view again for the controls it was drawn for.
    const refresh = classed(panelOf(document), STAMP_REFRESH_CLASS)[0]
    expect(refresh, 'the stamp drew no refresh control').toBeDefined()
    refresh?.click()
    await flush()
    expect(
      fake.calls
        .filter((call) => call.name === 'runReport')
        .map((call) => (call.args as { readonly since?: unknown }).since),
    ).toEqual(['7d', '7d'])
    expect(stamped(document), 'the re-read view is still stamped as a cached one').toBeNull()
  })

  it('says when the view was read in an element of its own, not in the line around the control', async () => {
    const { document } = await readSpendOnce()
    buttonFor(document, 'live').click()
    await flush()
    buttonFor(document, 'spend').click()

    const line = classed(panelOf(document), STAMP_CLASS)[0]
    expect(line, 'the cached view drew no stamp').toBeDefined()
    const words = classed(line as FakeNode, STAMP_AT_CLASS)
    expect(words, 'the stamp has no element of its own saying when').toHaveLength(1)
    expect(words[0]?.textContent).toMatch(/^as of \d\d:\d\d$/)
    // And the control is in the line beside the words: a DOM node's `textContent`
    // is its whole subtree's, so a hook on the line itself would answer
    // "as of 09:05Refresh" to whatever read it -- a test, or a screen reader.
    expect(classed(line as FakeNode, STAMP_REFRESH_CLASS)).toHaveLength(1)
  })

  it('starts one read per click, however many times the cached view has been drawn', async () => {
    const { fake, document } = await readSpendOnce()
    const offered = classed(panelOf(document), 'spend-window').length

    // Leave Spend and come back twice: the cached view is drawn again on each
    // visit, which is the path where a control could be wired a second time and
    // one click would start two reads racing on the same token.
    for (let visit = 0; visit < 2; visit += 1) {
      buttonFor(document, 'live').click()
      await flush()
      buttonFor(document, 'spend').click()
      await flush()
    }
    expect(
      classed(panelOf(document), 'spend-window'),
      'a visit left the picker it drew behind, beside the one it drew next',
    ).toHaveLength(offered)

    const before = fake.calls.filter((call) => call.name === 'runReport').length
    windowButton(document, '90d').click()
    await flush()
    const reads = fake.calls.filter((call) => call.name === 'runReport')
    expect(reads.length - before, 'one click started more than one read').toBe(1)
    expect((reads[reads.length - 1]?.args as { readonly since?: unknown }).since).toBe('90d')
  })

  it('draws a view older than five minutes and reads it again at once', async () => {
    const { fake, document } = await readSpendOnce()
    const reads = fake.calls.filter((call) => call.name === 'runReport').length

    vi.setSystemTime(Date.now() + CACHE_FRESH_MS)
    buttonFor(document, 'live').click()
    await flush()
    buttonFor(document, 'spend').click()
    // The stale view is drawn -- instantly, which is the point -- and the read
    // that refreshes it starts in the same frame, so a figure the operator is
    // looking at is never silently older than five minutes.
    expect(theSpendView(document)).toEqual(['2026-10-01'])
    expect(stamped(document)).not.toBeNull()
    expect(sectionOf(document, 'spend-report').getAttribute(BUSY_ATTRIBUTE)).toBe('true')

    await flush()
    expect(fake.calls.filter((call) => call.name === 'runReport').length).toBe(reads + 1)
    expect(stamped(document)).toBeNull()
  })

  it('answers a control change from the cache, and reads again only what is older than five minutes', async () => {
    // 7d → 30d → 7d is a round trip the three controls make easy and an
    // operator makes without meaning to ask the CLI anything. The view is in
    // this session's cache, so the second 7d is drawn stamped in the click's own
    // frame and no report is read for it.
    const { fake, document, windows } = await readSpendOnce()
    const reads = (): number => fake.calls.filter((call) => call.name === 'runReport').length
    const before = reads()

    // 30d is asked for and does not answer, and the operator comes straight back.
    windowButton(document, '30d').click()
    windowButton(document, '7d').click()
    expect(theSpendView(document), 'the change was not answered from the cache').toEqual(['2026-10-01'])
    expect(stamped(document)).toMatch(/^as of \d\d:\d\d$/)
    expect(busyLabels(document), 'the cached view was left under the mark of the read it replaced').toEqual([])
    await flush()
    expect(reads(), 'a change to a view this session had already read made a read').toBe(before + 1)

    // The read the operator left behind is not drawn over the view they chose:
    // the cached view is the newest answer for the controls on screen.
    windows.release('30d', reportDoc('2026-09-01'))
    await flush()
    expect(theSpendView(document), 'a read for a range the operator left was drawn').toEqual(['2026-10-01'])

    // A cached view older than five minutes is drawn **and** read again, exactly
    // as it is on entering the tab: a change is not a door around that rule.
    vi.setSystemTime(Date.now() + CACHE_FRESH_MS)
    windowButton(document, '7d').click()
    expect(stamped(document), 'the stale cached view was served without its stamp').not.toBeNull()
    await flush()
    expect(reads()).toBe(before + 2)
  })

  it('reads the budget again on every visit, with the caps it already has under the mark', async () => {
    // The caps are the installation's own budget and not the view, so a control
    // change never re-reads them -- but they move while the console is open, and
    // a `budget show` the CLI refused would otherwise stay refused for the rest
    // of the session. So the visit reads them again, and the section carries
    // what this session already has under the mark while that read runs.
    const answers: ReturnType<typeof holding>[] = []
    const fake = spendBridge({
      showBudget: () => {
        const answer = holding()
        answers.push(answer)
        return answer.reply()
      },
      runReport: () => ({ ok: true, data: { document: reportDoc('2026-10-01') } }),
    })
    const document = await openSpend(fake.bridge)
    answers[0]?.release({ ok: true, data: { document: {} } })
    await flush()
    expect(sectionOf(document, 'spend-caps').getAttribute(BUSY_ATTRIBUTE)).toBeNull()

    buttonFor(document, 'live').click()
    await flush()
    buttonFor(document, 'spend').click()
    const caps = sectionOf(document, 'spend-caps')
    expect(
      fake.calls.filter((call) => call.name === 'showBudget'),
      'the budget was not read again on the visit',
    ).toHaveLength(2)
    expect(busyLabels(document)).toEqual(['Reading the budget caps…'])
    // The answer from the visit before is on screen under that mark: a blank
    // section for the second the read takes is what the owner's rule is against.
    // `data-available` is `fillSpendCaps`'s own, so it is set by the drawing and
    // by nothing else.
    expect(caps.getAttribute('data-available')).toBe('true')

    answers[1]?.release({ ok: true, data: { document: {} } })
    await flush()
    expect(caps.getAttribute(BUSY_ATTRIBUTE)).toBeNull()
    expect(classed(panelOf(document), BUSY_CLASS)).toEqual([])
  })

  it('reads the caps again from the stamp’s own control', async () => {
    // The stamp is the operator saying the figures on screen are old, and the
    // caps are as old as the view the stamp is about: a Refresh that read only
    // the report would leave the section beside it stale and say so nowhere.
    const { fake, document } = await readSpendOnce()
    buttonFor(document, 'live').click()
    await flush()
    buttonFor(document, 'spend').click()
    const before = fake.calls.filter((call) => call.name === 'showBudget').length

    const refresh = classed(panelOf(document), STAMP_REFRESH_CLASS)[0]
    expect(refresh, 'the stamp drew no refresh control: the case proves nothing').toBeDefined()
    refresh?.click()
    expect(
      fake.calls.filter((call) => call.name === 'showBudget').length,
      'the stamp’s Refresh read the report and left the caps as they were',
    ).toBe(before + 1)
    expect(sectionOf(document, 'spend-caps').getAttribute(BUSY_ATTRIBUTE)).toBe('true')
    await flush()
  })
})

// ---------------------------------------------------------------------------
// The guard each tab's own read owns (console 0.116.1, review follow-up a).
//
// Every screen reads through the same bridge and the operator may leave while a
// read is out. Turning off the check that drops a late answer -- `if
// (stale(mine)) return` -- used to fail nothing for the Coordinator and the
// Graph: the drawing is what the operator sees, and only a case that leaves the
// tab *before* the answer lands can tell a guarded read from an unguarded one.
// ---------------------------------------------------------------------------

/** liveFrame is the frame the Live screen is drawn from, so a case that moves
 * to Live is looking at the Live screen and not at a refusal it would have to
 * tell apart from the answer it is waiting for. */
function liveFrame(): Readonly<Record<string, unknown>> {
  return syntheticDocuments()[0] as Readonly<Record<string, unknown>>
}

/** COORDINATOR_STATUS is the fixture the coordinator screen next door is pinned
 * against, so a case here drives the same document. */
const COORDINATOR_STATUS: unknown = JSON.parse(
  readFileSync(join(CONSOLE_ROOT, 'test', 'fixtures', 'coordinator-status.json'), 'utf8'),
)

describe('a read that lands after the operator left its tab', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  /** leftBehind opens one tab whose read is held, moves to Live, and answers the
   * screen the operator is looking at -- the element a late answer must not
   * replace. */
  async function leftBehind(
    bridge: Record<string, unknown>,
    view: string,
  ): Promise<{ readonly document: FakeDocument; readonly chosen: FakeNode }> {
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, view).click()
    await flush()
    // The non-vacuity guard: the tab the case opens has drawn something of its
    // own before the late read lands -- the panel's placeholder for a tab that
    // waits that way, or the screen's own frame with a section marked busy for
    // one that draws its frame in the click's frame (Projects, the Coordinator).
    const drew =
      readingOf(document) !== null ||
      everyNode(panelOf(document)).some((node) => node.attributes.get(BUSY_ATTRIBUTE) === 'true') ||
      operationOf(document) !== null
    expect(drew, `the ${view} tab drew nothing of its own: the case proves nothing`).toBe(true)
    buttonFor(document, 'live').click()
    await flush()
    const chosen = panelOf(document).children[0] as FakeNode
    expect(chosen.attributes.get('class'), 'the operator never left Live: the case proves nothing').toBe(
      'live',
    )
    return { document, chosen }
  }

  it('never draws the Coordinator’s status over the tab the operator moved to', async () => {
    const status = holding()
    const { bridge } = graphBridge({
      subscribeLive: () => snapshotReply(liveFrame()),
      coordinatorStatus: () => status.reply(),
    })
    const { document, chosen } = await leftBehind(bridge, 'coordinator')
    status.release({ ok: true, data: COORDINATOR_STATUS })
    await flush()
    expect(panelOf(document).children[0], 'a late status replaced the view on screen').toBe(chosen)
  })

  it('never draws the Projects screen over the tab the operator moved to', async () => {
    // The read is `budget show` and it is the tab's **only** one: the run list
    // this case used to hold is not a read of this screen any more (the owner's
    // report of 2026-10-31), so what can land after the operator has moved on is
    // the budget's own answer.
    const budget = holding()
    const { bridge } = graphBridge({
      subscribeLive: () => snapshotReply(liveFrame()),
      showBudget: () => budget.reply(),
    })
    const { document, chosen } = await leftBehind(bridge, 'projects')
    budget.release({ ok: true, data: { document: {} } })
    await flush()
    expect(panelOf(document).children[0], 'a late budget read replaced the view on screen').toBe(chosen)
  })

  it('never draws the plan over the tab the operator moved to', async () => {
    const plan = holding()
    const { bridge } = graphBridge({
      subscribeLive: () => snapshotReply(liveFrame()),
      planStatus: () => plan.reply(),
    })
    const { document, chosen } = await leftBehind(bridge, 'graph')
    plan.release(planStatusReply())
    await flush()
    expect(panelOf(document).children[0], 'a late plan replaced the view on screen').toBe(chosen)
  })

  it('never draws the plan’s refusal over the tab the operator moved to', async () => {
    // A refused `plan.status` is the half of the read that draws a whole screen
    // of its own, and the guard above it is checked **before** the refusal is
    // drawn: a refusal for a generation that has passed would put the operation
    // cell `plan.status` under a tab that never asked for it -- the defect the
    // placeholder fix is about, with the read's own answer in place of the
    // screen it left behind.
    const plan = holding()
    const { bridge } = graphBridge({
      subscribeLive: () => snapshotReply(liveFrame()),
      planStatus: () => plan.reply(),
    })
    const { document, chosen } = await leftBehind(bridge, 'graph')
    plan.release({ ok: false, reason: GRAPH_REFUSAL })
    await flush()
    expect(panelOf(document).children[0], 'a late refusal replaced the view on screen').toBe(chosen)
    expect(operationOf(document), 'a read nobody is waiting for named itself on the panel').toBe(null)
    expect(holdsText(panelOf(document), GRAPH_REFUSAL)).toBe(false)
  })

  it('never draws the picture a layout answered for a tab the operator left', async () => {
    // The layout is the **last** await of a plan read: the plan and the run list
    // are in, and the worker has not placed anything yet. The operator moves on
    // inside that window, and the drawing the layout answers with is the plan the
    // console read for a tab nobody is on.
    const { bridge } = graphBridge({ subscribeLive: () => snapshotReply(liveFrame()) }, HeldWorker)
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'graph').click()
    await flush()
    expect(
      readingOf(document),
      'the graph is not waiting on a layout: the case proves nothing',
    ).toContain('Reading')
    buttonFor(document, 'live').click()
    await flush()
    const chosen = panelOf(document).children[0] as FakeNode
    expect(chosen.attributes.get('class'), 'the operator never left the Graph: the case proves nothing').toBe(
      'live',
    )

    ;(HeldWorker.latest as HeldWorker).release()
    await flush()
    expect(panelOf(document).children[0], 'a late layout drew the plan over the view on screen').toBe(chosen)
    expect(classed(panelOf(document), 'graph-canvas')).toEqual([])
  })

  it('draws the console’s own sentence for a graph that could not be built, and none for a tab the operator left', async () => {
    // The catch is the one guard a bridge read cannot reach: `askMember` answers
    // `null` rather than rejecting, so the only way into it is a throw this file
    // did not expect -- and the layout worker is one, because the screen builds
    // its worker in its constructor (`GraphLayoutClient`). This suite would
    // otherwise never get there: every case that draws a graph stubs the worker
    // in. Both halves are pinned here, the sentence and the generation.
    //
    // The throw is in the same turn as the plan it follows -- the canvas is drawn
    // from the plan alone, so there is no second read between the two -- and what
    // can land after the operator has left is therefore the plan itself: the
    // first half pins the guard in front of the catch, which is the reason the
    // sentence is not drawn for a generation that has passed.
    const plan = holding()
    const { bridge } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(liveFrame()),
        planStatus: () => plan.reply(),
      },
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'graph').click()
    await flush()
    buttonFor(document, 'live').click()
    await flush()
    const chosen = panelOf(document).children[0] as FakeNode
    expect(chosen.attributes.get('class'), 'the operator never left the Graph: the case proves nothing').toBe(
      'live',
    )

    plan.release(planStatusReply())
    await flush()
    expect(panelOf(document).children[0], 'a build nobody is waiting for replaced the view on screen').toBe(
      chosen,
    )
    expect(holdsText(panelOf(document), READ_UNREADABLE)).toBe(false)
    expect(operationOf(document)).toBe(null)

    // The same throw under the operator's own tab: the console's sentence, and
    // the read it came from.
    buttonFor(document, 'graph').click()
    await flush()
    expect(operationOf(document), 'a graph that could not be built drew no refusal at all').toBe(
      'plan.status',
    )
    expect(holdsText(panelOf(document), READ_UNREADABLE)).toBe(true)
  })

  it('draws the plan it already read the moment the operator comes back', async () => {
    // The session cache, and the other half of answering in the click's frame: a
    // plan this console has already read is drawn again the moment the tab comes
    // back, **before** anything is awaited but the layout -- stamped `as of
    // HH:MM` with the door to read it again beside it. The read the return makes
    // is the layout engine's cache, not a second `plan status`.
    const plans = readQueue()
    let reads = 0
    const { bridge } = graphBridge({
      subscribeLive: () => snapshotReply(liveFrame()),
      planStatus: () => {
        reads += 1
        return plans.reply()
      },
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'graph').click()
    await flush()
    plans.release(0, planStatusReply())
    await flush()
    expect(
      classed(panelOf(document), 'graph-canvas').length,
      'the first read drew no canvas: the case proves nothing',
    ).toBeGreaterThan(0)

    buttonFor(document, 'live').click()
    await flush()
    buttonFor(document, 'graph').click()
    await flush()
    expect(
      classed(panelOf(document), 'graph-canvas').length,
      'the return drew nothing from the cache',
    ).toBeGreaterThan(0)
    const stamp = classed(panelOf(document), 'console-stamp')[0]
    expect(stamp, 'the cached view was drawn with no stamp').toBeDefined()
    expect(textOf(stamp as FakeNode)).toContain('as of')
    expect(reads, 'the return read the plan again instead of drawing what it held').toBe(1)
  })

  it('never mounts the Spend frame from a caps read that landed after the operator left', async () => {
    // The caps read is Spend's *other* read, and it mounts the frame the report
    // fills: without its own guard a caps answer that arrived late would put the
    // whole Spend screen over the tab the operator is looking at.
    const caps = holding()
    const report = holding()
    const { bridge } = spendBridge({ showBudget: () => caps.reply(), runReport: () => report.reply() })
    const { document, chosen } = await leftBehind(bridge, 'spend')
    caps.release({ ok: true, data: { document: {} } })
    await flush()
    expect(panelOf(document).children[0], 'a late caps read mounted the Spend frame').toBe(chosen)
  })

  it('never draws a run’s screen over the tab the operator moved to', async () => {
    // Run detail is a view without a placeholder: it is opened from a row, and
    // the panel keeps the screen the row was drawn on until the run reads land.
    // Those reads are three, and the first of the two guards stands between the
    // second and the third -- a diagnosis that arrives after the operator has
    // moved on must not take the tab they are on down to draw a run.
    const documents = syntheticDocuments()
    const frame = documents[documents.length - 1] as Readonly<Record<string, unknown>>
    const diagnosis = holding()
    const runs = holding()
    const { bridge } = graphBridge({
      subscribeLive: () => snapshotReply(frame),
      diagnoseRun: () => diagnosis.reply(),
      listRuns: () => runs.reply(),
    })
    const document = await boot(bridge, new FakeDocument())
    ;(classed(panelOf(document), 'live-run')[0] as FakeNode).click()
    await flush()

    buttonFor(document, 'projects').click()
    await flush()
    const chosen = panelOf(document).children[0] as FakeNode
    expect(
      chosen.attributes.get('class'),
      'the Projects tab drew nothing of its own: the case proves nothing',
    ).toBe('projects')

    diagnosis.release({ ok: true, data: { document: {} } })
    await flush()
    expect(panelOf(document).children[0], 'a late diagnosis drew the run over the view on screen').toBe(
      chosen,
    )
  })

  it('never draws a run’s screen from a log link that landed after the operator moved', async () => {
    // The third read is the link, and it is the one the screen holds last: it is
    // not a reason to refuse the run (`buildRunModel` draws its own sentence for
    // a link that did not arrive), so it carries a guard of its own -- the two
    // document reads are in, and the link is the await the operator leaves in.
    const documents = syntheticDocuments()
    const frame = documents[documents.length - 1] as Readonly<Record<string, unknown>>
    const link = holding()
    const runs = holding()
    const { bridge } = graphBridge({
      subscribeLive: () => snapshotReply(frame),
      diagnoseRun: () => ({ ok: true, data: { document: {} } }),
      readRunLogs: () => ({ ok: true, data: { document: { entries: [], total: 0, refused: 0 } } }),
      runLogLink: () => link.reply(),
      listRuns: () => runs.reply(),
    })
    const document = await boot(bridge, new FakeDocument())
    ;(classed(panelOf(document), 'live-run')[0] as FakeNode).click()
    await flush()

    buttonFor(document, 'projects').click()
    await flush()
    const chosen = panelOf(document).children[0] as FakeNode
    expect(
      chosen.attributes.get('class'),
      'the Projects tab drew nothing of its own: the case proves nothing',
    ).toBe('projects')

    link.release({ ok: true, data: 'https://logs.example/one-run' })
    await flush()
    expect(panelOf(document).children[0], 'a late log link drew the run over the view on screen').toBe(
      chosen,
    )
  })
})

// ---------------------------------------------------------------------------
// Each tab answers in the frame it was opened in, and the reads it does not
// wait for (the owner's rule of 2026-10-31, `CLAUDE.md`'s console rules 1-5).
//
// Every read here is measured on the owner's installation: `coordinator status
// --from-store --all` about thirteen seconds and 724 KB, `budget show` about
// one, `runs ls --json` about six and 248 KB for a day. A tab that waited for
// all of them answered a click with nothing for twenty seconds, which is the
// report these cases are the fix for.
// ---------------------------------------------------------------------------

/** BUDGET_DOC is `budget show --json` as the Projects screen reads it: one
 * repository with a kill switch that is on. Invented, like the fixture next
 * door. */
const BUDGET_DOC = Object.freeze({
  project: 'example-project',
  mode: 'enforce',
  day: '2026-10-15',
  global: { daily_cap_usd: 40, per_run_cap_usd: 8, counted_usd: 12.5, kill: null },
  defaults: { repo_daily_cap_usd: 10, repo_per_run_cap_usd: 2 },
  repos: [
    {
      slug: 'example-owner/example-repo',
      repo: 'example-owner/example-repo',
      daily_cap_usd: 20,
      daily_cap_source: 'repo',
      per_run_cap_usd: 4,
      per_run_cap_source: 'global',
      counted_usd: 12.5,
      kill: { by: 'operator@example.test', at: '2026-10-15T11:30:00Z', reason: 'spend rate' },
      running: [],
    },
  ],
  running: [],
  warnings: [],
})

/** MODELS_DOC is `models ls --json` (contract section 6.18), invented: one
 * route and one model with a price. It is the Projects tab's second read. */
const MODELS_DOC = Object.freeze({
  version: 1,
  project: 'example-project',
  catalog: { source: 'built-in', checked_at: '2026-10-01', age_days: 33, stale: false },
  routes: [
    {
      name: 'anthropic',
      kind: 'direct',
      base_url: 'https://api.anthropic.com',
      host: 'api.anthropic.com',
      models: ['claude-*'],
      route_fee_pct: 0,
      allow_data_to: ['*'],
      open_to_every_repo: true,
      note: '',
    },
  ],
  models: [
    {
      id: 'deepseek-v4.1-flash',
      route: 'anthropic',
      source: 'catalog',
      input_per_m: 0.14,
      output_per_m: 0.28,
      catalog: null,
      allowed_to: ['*'],
      refused: '',
      note: '',
    },
  ],
  repositories: [],
})

/** MANAGED_DOC is `managed status --json` (contract section 6.21), invented: a
 * managed project whose newest audit record is one update that retired one
 * setting. **One read, two sections** (CM8 and CM9): the managed state's marks
 * and trail, and the last update's retirements, are both drawn from this
 * document -- which is why the case beside it asserts both hosts. */
const MANAGED_DOC = Object.freeze({
  managed: true,
  reason: '',
  version: 3,
  generation: '7',
  audit_day: '2026-11-07',
  audit_count: 1,
  audit_state: '',
  audit: {
    at: '2026-11-07T09:15:00Z',
    command: 'update',
    verdict: 'ok',
    identity: 'operator@example.test',
    plan_digest: 'sha256:0000',
  },
  retirements: {
    version: 1,
    at: '2026-11-07T09:15:00Z',
    runs_in_flight: 0,
    retire: [
      {
        address: 'config/agent.models.coder',
        kind: 'setting',
        cause: 'the registry dropped it',
        kept: '',
        logical: 'agent.models.coder',
        secret_id: '',
        protected: false,
      },
    ],
  },
})

/** POLICY_DOC is `project policy show --json`: the `{project, policy}` object
 * CM4's row answers with, and the document the tab's policy panel is drawn from
 * (CM10). One allow-list entry, one account and one route, which is what the
 * panel's body draws. */
const POLICY_DOC = Object.freeze({
  project: 'example-project',
  policy: {
    version: 3,
    base_branch: 'trunk',
    allow_repos: ['github/example-owner/example-repo'],
    accounts: { 'claude-ops': { allow_repos: ['github/example-owner/example-repo'] } },
  },
})

/** DOCTOR_DOC is `doctor --json`: the checks array, and it is **two reads'**
 * answer at once (CM9 and CM7): the credentials service's section draws its own
 * `credentials-service-deployed` line, and the policy panel's body draws the six
 * `policy-*` checks. `managed-audit` is here as the check neither draws. */
const DOCTOR_DOC = Object.freeze({
  ok: false,
  checks: [
    {
      id: 'credentials-service-deployed',
      ok: false,
      severity: 'warning',
      problem: 'the credentials service is not deployed',
      fix: 'fugaro update deploys it',
    },
    {
      id: 'policy-published',
      ok: false,
      severity: 'info',
      problem: 'a policy is published',
      fix: '',
    },
    { id: 'managed-audit', ok: true, severity: 'info', problem: 'the audit trail is readable', fix: '' },
  ],
})

/** SETTINGS_DOC is `settings --json` (the settings-edit plan's C1), invented:
 * two rows -- a writable one with a target layer, and one the generic path does
 * not write. It is the Projects tab's third read. */
const SETTINGS_DOC = Object.freeze({
  version: 1,
  project: 'example-project',
  generated_at: '2026-11-03T06:00:00Z',
  sources: [{ layer: 'default', read_at: '2026-11-03T06:00:00Z', duration_ms: 0, status: 'ok' }],
  settings: [
    {
      key: 'budget.per_run_usd',
      scope: 'project',
      value: '5',
      wins: 'project',
      default: { value: '3', source: 'registry' },
      state: 'set',
      writable: true,
      owner: '',
      target_layer: 'project',
    },
    {
      key: 'agent.models.coder',
      scope: 'repo',
      value: 'claude-*',
      wins: 'repo',
      default: { unset: 'the recipe’s own coder model' },
      state: 'set',
      writable: false,
      owner: 'fugaro models',
      target_layer: '',
    },
  ],
})

/** QUEUE_OFFER is the one repository identity the open installation offers: the
 * `<provider>/<owner>/<name>` string the launch form's `select` holds as an
 * option's value, and the value the form sends. */
const QUEUE_OFFER = 'github/owner/repo-a'

/** QUEUE_REPO is how the queue's **own documents** spell that repository, and it
 * is deliberately not `QUEUE_OFFER`: `runs ls --json` and `plan status` both
 * carry a repository as `owner/name`, with no provider, while the form offers
 * the installation's identity. The two are different strings for the same
 * repository, and joining them is the whole of what the prefill does. */
const QUEUE_REPO = 'owner/repo-a'

/** QUEUE_RUN_ID and QUEUE_RUN_PLAN_TASK are the two strings the run the queue
 * offers to retry carries: the id the console draws and opens, and the plan task
 * its own launch recorded, which the console keeps as stored. */
const QUEUE_RUN_ID = '20261031-101502-9f00'
const QUEUE_RUN_PLAN_TASK = 'plans/2026-10-01-a/task-one'

/** QUEUE_RUN is one `runs ls --json` row the action queue takes: a run that
 * ended without succeeding, with the plan task its launch recorded. */
const QUEUE_RUN: Readonly<Record<string, unknown>> = {
  run: QUEUE_RUN_ID,
  repo: QUEUE_REPO,
  status: 'failed',
  terminal: true,
  title: 'the job that did not finish',
  plan_task: QUEUE_RUN_PLAN_TASK,
}

/**
 * k2Status is the committed stored view with the two things the K2 cases need
 * and the fixture predates: the `provider` C1 added to every node (D814), and a
 * run behind the open pull request the queue waits for -- the fixture's `#11` is
 * open and is not a draft, but the queue takes only a pull request a run stands
 * behind, and its own `runs` is empty.
 *
 * `url` is written on that pull request and is the value the console may never
 * use: whatever the document says, the destination is built by main from the
 * triple, so a hostile `url` here is a case that can only pass by the console
 * sending the triple and nothing else.
 */
function k2Status(provider: string, url: string): unknown {
  type Node = {
    provider?: string
    view: { records: { pr: { number: number; url: string } | null; runs: Record<string, unknown>[] }[] }
  }
  const status = JSON.parse(JSON.stringify(COORDINATOR_STATUS)) as { repos: Node[] }
  for (const repo of status.repos) {
    if (provider === '') delete repo.provider
    else repo.provider = provider
  }
  const first = status.repos[0]
  const held = first?.view.records[0]
  const waits = first?.view.records[1]
  const run = held?.runs[0]
  if (waits === undefined || waits.pr === null || run === undefined) {
    throw new Error('the fixture no longer holds the two records and the run this case is written against')
  }
  waits.pr.url = url
  waits.runs = [{ ...run, id: '20261031-101502-9f11', pr: waits.pr.number }]
  return status
}

/** QUEUE_PLAN_TASK is the plan task of the one item `QUEUE_PLAN` holds: the
 * plan's own id and the task's own id, joined the way the flag spells it. */
const QUEUE_PLAN_TASK = 'plans/2026-10-01-example-graph/task-three'

/** QUEUE_PLAN is the plan read's own document -- the committed plan-status
 * fixture, with its repository the one `QUEUE_REPO` names, spelled the way the
 * read spells it. Its `task-three` is the CLI's own `state == "ready"`, so it is
 * one item of the queue and one press of Launch. */
const QUEUE_PLAN: unknown = {
  ...(PLAN_STATUS as Record<string, unknown>),
  repo: QUEUE_REPO,
}

/** aWindow is one `runs summary --json` document (console 0.119.0): the window
 * the fleet strip reads, its runs counted by state and what they spent. The
 * defaults are a window every source answered for whose compute the CLI priced
 * in full, so a case that is about something else draws no unknown-day line and
 * no lower bound. */
function aWindow(
  options: {
    readonly runs?: number
    readonly succeeded?: number
    readonly failed?: number
    readonly computeMicros?: number | null
    readonly computeRuns?: number
    readonly daysUnknown?: readonly string[]
  } = {},
): Readonly<Record<string, unknown>> {
  const runs = options.runs ?? 4
  return {
    v: 1,
    project: 'acme',
    since: '2026-10-27',
    until: '2026-11-02',
    repositories: [],
    totals: {
      runs,
      states: {
        succeeded: options.succeeded ?? 0,
        failed: options.failed ?? 0,
        halted: 0,
        cancelled: 0,
        infra_error: 0,
        other: 0,
      },
      spent_micros: 0,
      notional_micros: 0,
      compute_micros: options.computeMicros === undefined ? 0 : options.computeMicros,
      compute_runs: options.computeRuns ?? runs,
    },
    days_unknown: options.daysUnknown ?? [],
    notes: [],
  }
}

/** AN_EMPTY_WINDOW is the window `queueBridge` answers with when a case says
 * nothing about it: a read that succeeded and counted nothing. It is a document
 * and not a refusal for the pane's own reason -- a case about the queue or the
 * job list must not find a second refusal on the page it asserts about -- and it
 * is empty rather than populated so the strip's own six figures stay the only
 * ones a case has to know. */
const AN_EMPTY_WINDOW = aWindow({ runs: 0 })

/** jobsBridge is `queueBridge` with one thing a case about the job list's floor
 * needs and it cannot give: **the CLI version line the handshake answered with**.
 * The paged read is gated on `RUNS_LIST_PAGE_CLI_FLOOR`, so both sides of that
 * gate are cases about the handshake's own answer. */
function jobsBridge(
  answers: Readonly<Record<string, AReply>>,
  cliVersion = '1.2.3',
): FakeBridge {
  const base = queueBridge(answers)
  base.bridge['getCompatibility'] = (): unknown => ({
    ok: true,
    data: { ...anOkCompatibility(), cliVersion },
  })
  return base
}

/** LS_ROWS is the id suffix the job list's fixture rows carry, so the fixture's
 * rows are distinguishable in a claim about which page drew which row. */
const LS_ROW_A = '20261101-090000-aa01'
const LS_LAST_ID = '20261101-090000-zz50'

/** HOSTILE_CLI_VERSION is a handshake version line that carries markup: the
 * console draws it as the text it is, and this is the string a case reads back
 * out of the drawing rather than the element it names. */
const HOSTILE_CLI_VERSION = '0.15.3<img src=x onerror=alert(1)>'

/** lsRow is one `runs ls --json` row of the job list: a run the CLI printed. */
function lsRow(run: string): Readonly<Record<string, unknown>> {
  return {
    run,
    repo: QUEUE_REPO,
    status: 'succeeded',
    stage: 'implement',
    outcome: 'implemented',
    title: 'a job somebody ran',
    created: '2026-11-01T09:00:00Z',
    terminal: true,
    cost: { total_usd: 1.25, model_usd: 1, compute_usd: 0.25, compute_estimated: true },
  }
}

/** lsPage is one page of the job list: the rows the CLI printed and the
 * **window's** own `totals`. `window` is deliberately far above the row count in
 * every case that uses it -- the CLI cuts what it prints and sums what it read. */
function lsPage(window: number, spent: number, rows: readonly string[]): Readonly<Record<string, unknown>> {
  return {
    project: 'acme',
    runs: rows.map(lsRow),
    totals: { runs: window, model_usd: spent, model_notional_usd: 0, compute_usd: 0, total_usd: spent },
    warnings: [],
  }
}

/** aFullPage is `COORDINATOR_JOBS_PAGE` rows, the last of them `LS_LAST_ID`: the
 * only page length on which the console knows the window is longer than what it
 * drew, and therefore the only one that offers the Older control. */
function aFullPage(): readonly string[] {
  const rows: string[] = []
  for (let index = 0; index < COORDINATOR_JOBS_PAGE - 1; index += 1) {
    rows.push(`20261101-090000-${String(index).padStart(4, '0')}`)
  }
  rows.push(LS_LAST_ID)
  return rows
}

/** aPullRequest is one `coordinator show --json` document: one pull request as
 * the host has it, whole. `body` is the field the read exists for. */
function aPullRequest(body: string): Readonly<Record<string, unknown>> {
  return {
    number: 7,
    url: 'https://example.invalid/owner/repo-a/pull/7',
    title: 'a title someone wrote',
    body,
    state: 'open',
    draft: true,
    branch: 'feat/a-thing',
    base: 'main',
    author: 'person-1',
  }
}

/** descriptionBlock is the block one record's row drew, which is the element the
 * description is painted into. */
function descriptionBlock(document: FakeDocument): FakeNode {
  const block = classed(panelOf(document), COORDINATOR_DESCRIPTION_CLASS)[0]
  if (block === undefined) throw new Error('the row drew no description block')
  return block
}

/** coordinatorBridge is the Coordinator screen's own reads, with the description
 * read answered by the case: the committed fixture's one pull request is
 * `owner/repo-a`'s #7. */
function coordinatorBridge(answers: Readonly<Record<string, AReply>>): FakeBridge {
  return queueBridge({
    coordinatorStatus: () => ({ ok: true, data: { document: COORDINATOR_STATUS } }),
    planStatus: () => planStatusReply(QUEUE_PLAN),
    showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
    ...answers,
  })
}

describe('the description the entry reads on demand (K3)', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  /** land is one read and the chunk the screen draws its records on: the
   * microtasks a landing settles, and then the clock the queued detail runs on
   * (a `setTimeout(…, 0)` per repository, which is the freeze the chunks are
   * for). */
  async function land(): Promise<void> {
    await flush()
    await vi.advanceTimersByTimeAsync(10)
    await flush()
  }

  it('reads one pull request on the first expand, and draws what it read on the second', async () => {
    const asked: Record<string, unknown>[] = []
    const { bridge } = coordinatorBridge({
      coordinatorShow: (args) => {
        asked.push(args as Record<string, unknown>)
        return { ok: true, data: { document: aPullRequest('the first line\nthe second line') } }
      },
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'coordinator').click()
    await land()

    // Nothing is read until the operator asks: the row is drawn with its
    // disclosure and the block that disclosure opens, hidden.
    expect(asked, 'the description was read without being asked for').toEqual([])
    const block = descriptionBlock(document)
    expect(block.attributes.get('hidden')).toBe('')
    expect(classed(block, COORDINATOR_DESCRIPTION_LINE_CLASS)).toEqual([])

    // The press is the read, and the pair is the row's own repository and number
    // -- nothing the console read back out of the DOM.
    const head = buttonLabelled(panelOf(document), COORDINATOR_DESCRIPTION_SHOW_WORD)
    head.click()
    expect(asked).toEqual([{ repo: 'owner/repo-a', pr: 7 }])
    // A press is answered in the frame it happened in: the block is revealed and
    // the section's own mark is up in it.
    expect(block.attributes.get('hidden')).toBeUndefined()
    expect(head.textContent).toBe(COORDINATOR_DESCRIPTION_HIDE_WORD)
    expect(holdsText(block, COORDINATOR_DESCRIPTION_BUSY)).toBe(true)

    await land()
    // The description, one line at a time, in the block the row already drew --
    // with the stamp that says when it was read.
    expect(classed(block, COORDINATOR_DESCRIPTION_LINE_CLASS).map((line) => line.textContent)).toEqual([
      'the first line',
      'the second line',
    ])
    expect(classed(block, STAMP_CLASS)).toHaveLength(1)
    expect(holdsText(block, COORDINATOR_DESCRIPTION_BUSY)).toBe(false)

    // An expand/collapse cycle is not a second read: the second expand draws the
    // document the first one read.
    head.click()
    expect(block.attributes.get('hidden')).toBe('')
    expect(head.textContent).toBe(COORDINATOR_DESCRIPTION_SHOW_WORD)
    head.click()
    expect(asked, 'the second expand read the description again').toHaveLength(1)
    expect(block.attributes.get('hidden')).toBeUndefined()
    expect(classed(block, COORDINATOR_DESCRIPTION_LINE_CLASS)).toHaveLength(2)
  })

  it('draws a refused description read as its own sentence, naming the operation it refused', async () => {
    // The credentials-service refusal is the CLI's own sentence
    // (`ee/coordinator/cli/coordinator.go`), and this block draws it as it came:
    // the console adds no words of its own to a refusal, and what names the read
    // is the operation id every `refusedScreen` draws -- `coordinator.show`, not
    // the command line and not the console's word for either.
    const credentials = 'the credentials service holds the GitHub App key and this command holds none'
    const { bridge } = coordinatorBridge({ coordinatorShow: () => ({ ok: false, reason: credentials }) })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'coordinator').click()
    await land()
    buttonLabelled(panelOf(document), COORDINATOR_DESCRIPTION_SHOW_WORD).click()
    await land()

    const block = descriptionBlock(document)
    expect(classed(block, 'unavailable-operation')[0]?.textContent).toBe('coordinator.show')
    expect(holdsText(block, credentials)).toBe(true)
    // A refusal is not a block with an empty description in it.
    expect(classed(block, COORDINATOR_DESCRIPTION_LINE_CLASS)).toEqual([])
  })
})

describe('the frame a tab answers in, and the read it does not wait for', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('reads the run list never on Projects, and paints the kills out of `budget show` alone', async () => {
    // The owner's measured read (`runs ls --json`, about six seconds for a day)
    // used to start before this screen could draw anything, which is the twenty
    // seconds the tab took with no feedback. It is not this screen's read at all
    // any more: `budget show` is.
    const { bridge, calls } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(liveFrame()),
        showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
      },
    })
    const document = await boot(bridge, new FakeDocument())
    const before = calls.length
    buttonFor(document, 'projects').click()
    await flush()
    expect(
      calls.slice(before).map((call) => call.name),
      'the Projects tab made a read that is not its own',
    ).toEqual([
      'showBudget',
      'listModels',
      'readSettings',
      'managedStatus',
      'policyShow',
      'environmentReport',
    ])
    expect(
      calls.slice(before).some((call) => call.name === 'listRuns'),
      'the Projects tab read the run list, which is the read the owner measured at six seconds',
    ).toBe(false)
    // What is on the screen is the budget document's own: the kill switch, its
    // operator and its reason, and no figure any run list could add.
    expect(classed(panelOf(document), 'projects-kill-row').length).toBeGreaterThan(0)
    expect(holdsText(panelOf(document), 'spend rate')).toBe(true)
    // The operator is a person, and the console draws one through the
    // sanitizer's own class: the address loses its local part (`sanitize.ts`).
    expect(classed(panelOf(document), 'projects-kill-by')[0]?.textContent).toBe(ADDRESS_PLACEHOLDER)
  })

  it('starts the models and settings reads with the tab’s others and lets none of them wait', async () => {
    // The three reads of this tab are independent documents: `models ls` is
    // offline and cheap and does not wait on `budget show`, and neither waits on
    // `settings --json` (the settings-edit plan's C1). All three go out in the
    // click's own turn -- nothing is awaited before the second and third calls --
    // which is what the assertion on the order says.
    const { bridge, calls } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(liveFrame()),
        showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
        listModels: () => ({ ok: true, data: { document: MODELS_DOC } }),
        readSettings: () => ({ ok: true, data: { document: SETTINGS_DOC } }),
      },
    })
    const document = await boot(bridge, new FakeDocument())
    const before = calls.length
    buttonFor(document, 'projects').click()
    // **Nothing is awaited**: the click's own turn is where all three calls are,
    // so a tab that serialised them would show fewer than three here.
    expect(
      calls
        .slice(before)
        .map((call) => call.name)
        .filter(
          (name) => name === 'showBudget' || name === 'listModels' || name === 'readSettings',
        ),
    ).toEqual(['showBudget', 'listModels', 'readSettings'])
    // While all seven are out, the tab says which read each of its marks is --
    // one mark per section, on the section's own host (CM10's four siblings are
    // the last four, and the policy panel's read wears no mark of its own: it is
    // a member of the body's model, drawn with it).
    expect(busyLabels(document)).toEqual([
      PROJECTS_BUSY,
      PROJECTS_MODELS_BUSY,
      PROJECTS_SETTINGS_BUSY,
      MANAGED_BUSY,
      ACCOUNT_KEYS_BUSY,
      RETIREMENTS_BUSY,
      CREDENTIALS_SERVICE_BUSY,
    ])
    await flush()
    // Each section paints its own answer, and the models section paints the
    // document's own routes and models and not the budget's.
    const models = classed(panelOf(document), 'projects-models')[0]
    expect(models, 'the tab drew no models section').toBeDefined()
    expect(holdsText(models as FakeNode, 'anthropic')).toBe(true)
    expect(holdsText(models as FakeNode, 'deepseek-v4.1-flash')).toBe(true)
    // And the settings section paints the settings document's own rows.
    const settings = classed(panelOf(document), 'projects-settings')[0]
    expect(settings, 'the tab drew no settings section').toBeDefined()
    expect(holdsText(settings as FakeNode, 'budget.per_run_usd')).toBe(true)
    expect(holdsText(settings as FakeNode, 'fugaro models')).toBe(true)
    expect(busyLabels(document)).toEqual([])
  })

  it('draws the four later sections from their own reads, and the policy panel from the body’s', async () => {
    // CM10: the tab's four sibling sections (the managed state, the review
    // account keys, the retirements and the credentials service) and the policy
    // panel's own read. **One read, two sections** twice over -- `managed status`
    // draws the first and the third, `settings --json` the second, `doctor
    // --json` the fourth -- and the policy panel is no host of its own: it stays a
    // member of `PROJECT_PANEL_KEYS`, drawn with the body (the plan's "a panel
    // another read builds is a sibling, not a member").
    const { bridge } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(liveFrame()),
        showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
        listModels: () => ({ ok: true, data: { document: MODELS_DOC } }),
        readSettings: () => ({ ok: true, data: { document: SETTINGS_DOC } }),
        managedStatus: () => ({ ok: true, data: { document: MANAGED_DOC } }),
        policyShow: () => ({ ok: true, data: { document: POLICY_DOC } }),
        environmentReport: () => ({ ok: true, data: { document: DOCTOR_DOC } }),
      },
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'projects').click()
    await flush()
    const root = panelOf(document)
    const section = (className: string): FakeNode => {
      const drawn = classed(root, className)[0]
      expect(drawn, `the tab drew no ${className} section`).toBeDefined()
      return drawn as FakeNode
    }
    // The managed state and the retirements, out of the one document that carries
    // both -- the marks block (whose generation is this document's) and the set
    // the last update retired.
    expect(holdsText(section(PROJECTS_MANAGED_CLASS), MANAGED_TITLE)).toBe(true)
    expect(holdsText(section(PROJECTS_MANAGED_CLASS), '7')).toBe(true)
    expect(holdsText(section(PROJECTS_RETIREMENTS_CLASS), RETIREMENTS_SET_TITLE)).toBe(true)
    // The account keys, out of the settings document the panel beside it draws.
    expect(holdsText(section(PROJECTS_ACCOUNT_KEYS_CLASS), ACCOUNT_KEYS_TITLE)).toBe(true)
    // The credentials service, out of doctor's own check.
    expect(holdsText(section(PROJECTS_CREDENTIALS_SERVICE_CLASS), CREDENTIALS_SERVICE_TITLE)).toBe(true)
    // And the policy panel, a member of the body's own seven: its body is CM7's
    // section, drawn from the node.
    expect(holdsText(policyPanelOf(document), POLICY_NODE_TITLE)).toBe(true)
    expect(busyLabels(document)).toEqual([])
  })

  it('paints the policy panel when its own read lands, out of the body’s own repaint', async () => {
    // The policy panel's read is a later one than the budget's, and the panel is
    // drawn **inside** the body: while its read is out the panel says so, with the
    // sentence naming the command, and the document's arrival is what turns it
    // from that state into a read one. Nothing else on the body moves with it.
    const policy = holding()
    const { bridge } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(liveFrame()),
        showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
        listModels: () => ({ ok: true, data: { document: MODELS_DOC } }),
        readSettings: () => ({ ok: true, data: { document: SETTINGS_DOC } }),
        managedStatus: () => ({ ok: true, data: { document: MANAGED_DOC } }),
        policyShow: () => policy.reply(),
        environmentReport: () => ({ ok: true, data: { document: DOCTOR_DOC } }),
      },
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'projects').click()
    await flush()
    expect(policyPanelOf(document).getAttribute('data-state')).toBe('unavailable')
    expect(
      holdsText(policyPanelOf(document), 'no document arrived for `fugaro project policy show --json`'),
    ).toBe(true)
    // The caps the budget drew are on screen, and the policy read is still out:
    // the panel is the only thing waiting for it.
    expect(classed(panelOf(document), 'projects-kill-row').length).toBeGreaterThan(0)
    policy.release({ ok: true, data: { document: POLICY_DOC } })
    await flush()
    expect(policyPanelOf(document).getAttribute('data-state')).toBe('read')
    expect(holdsText(policyPanelOf(document), POLICY_NODE_TITLE)).toBe(true)
    // The node's own string is on the screen, and the state word is the design's
    // `read` rather than the `locked` a not-built panel wears.
    expect(holdsText(policyPanelOf(document), 'trunk')).toBe(true)
  })

  it('holds the body’s own mark when a later read lands while `budget show` is out', async () => {
    // **The repaint guard** (CM10): the body is repainted by the two later reads
    // of a panel inside it -- `policy.show` and doctor's -- and a repaint while
    // the budget's own read is out would clear the section and take down the mark
    // of a read that is still on its way. That is this console taking a drawing
    // away to say a drawing is coming, which the guard is there to refuse.
    //
    // The visit that shows it is the one served from a view older than
    // `CACHE_FRESH_MS`: the figures are drawn at once, marked, and read again in
    // the same frame, so the body holds a model **and** a mark at the same time.
    const policy = holding()
    const secondBudget = holding()
    let budgetReads = 0
    const { bridge } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(liveFrame()),
        showBudget: () => {
          budgetReads += 1
          return budgetReads === 1
            ? { ok: true, data: { document: BUDGET_DOC } }
            : secondBudget.reply()
        },
        listModels: () => ({ ok: true, data: { document: MODELS_DOC } }),
        readSettings: () => ({ ok: true, data: { document: SETTINGS_DOC } }),
        managedStatus: () => ({ ok: true, data: { document: MANAGED_DOC } }),
        policyShow: () => policy.reply(),
        environmentReport: () => ({ ok: true, data: { document: DOCTOR_DOC } }),
      },
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'projects').click()
    await flush()
    expect(classed(panelOf(document), 'projects-kill-row').length).toBeGreaterThan(0)

    vi.setSystemTime(Date.now() + CACHE_FRESH_MS)
    buttonFor(document, 'live').click()
    await flush()
    buttonFor(document, 'projects').click()
    await flush()
    expect(
      busyLabels(document),
      'a later read’s own landing repainted the body under the budget read’s mark',
    ).toEqual([PROJECTS_BUSY])
    // The figures the cached view carried are kept under the mark rather than
    // blanked, which is what makes the mark's disappearance observable.
    expect(classed(panelOf(document), 'projects-kill-row').length).toBeGreaterThan(0)

    // The policy read lands while the budget's own is out: it draws nothing, and
    // the mark above the figures stays where it is.
    policy.release({ ok: true, data: { document: POLICY_DOC } })
    await flush()
    expect(
      busyLabels(document),
      'a later read repainted the body while the budget’s own read was out',
    ).toEqual([PROJECTS_BUSY])
    expect(policyPanelOf(document).getAttribute('data-state')).toBe('unavailable')

    // Nothing is lost by waiting: the budget's own answer draws the body, and the
    // policy document that landed first is drawn with it.
    secondBudget.release({ ok: true, data: { document: BUDGET_DOC } })
    await flush()
    expect(busyLabels(document)).toEqual([])
    expect(holdsText(policyPanelOf(document), POLICY_NODE_TITLE)).toBe(true)
  })

  it('does not read a dropped answer as the current visit’s budget read ending', async () => {
    // A superseded answer says nothing about the read that superseded it (the
    // owner's rule of 2026-10-31, point 4), and that includes the body's
    // in-flight flag: an answer the sequence token has already dropped must not
    // tell the two later reads that `budget show` is over. The second press here
    // is the stamp's own Refresh, over a cached view whose refresh read is still
    // out -- one press, one token, and the answer to the first press arriving
    // afterwards.
    const policy = holding()
    const secondBudget = holding()
    const thirdBudget = holding()
    let budgetReads = 0
    const { bridge } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(liveFrame()),
        showBudget: () => {
          budgetReads += 1
          if (budgetReads === 1) return { ok: true, data: { document: BUDGET_DOC } }
          return budgetReads === 2 ? secondBudget.reply() : thirdBudget.reply()
        },
        listModels: () => ({ ok: true, data: { document: MODELS_DOC } }),
        readSettings: () => ({ ok: true, data: { document: SETTINGS_DOC } }),
        managedStatus: () => ({ ok: true, data: { document: MANAGED_DOC } }),
        policyShow: () => policy.reply(),
        environmentReport: () => ({ ok: true, data: { document: DOCTOR_DOC } }),
      },
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'projects').click()
    await flush()

    // The view is stale, so the cached drawing is marked and read again at once;
    // the stamp's Refresh is the second press, and it supersedes that read.
    vi.setSystemTime(Date.now() + CACHE_FRESH_MS)
    buttonFor(document, 'live').click()
    await flush()
    buttonFor(document, 'projects').click()
    await flush()
    const refresh = classed(panelOf(document), STAMP_REFRESH_CLASS)[0]
    expect(refresh, 'the cached view drew no refresh control: the case proves nothing').toBeDefined()
    refresh?.click()
    await flush()
    expect(budgetReads).toBe(3)
    expect(busyLabels(document)).toEqual([PROJECTS_BUSY])

    // The first press's answer lands. It is not this visit's, so it draws nothing
    // -- and it does not open the guard either: the read the second press made is
    // the one that is still out.
    secondBudget.release({ ok: true, data: { document: BUDGET_DOC } })
    await flush()
    expect(busyLabels(document)).toEqual([PROJECTS_BUSY])
    policy.release({ ok: true, data: { document: POLICY_DOC } })
    await flush()
    expect(
      busyLabels(document),
      'an answer the token had dropped opened the body’s repaint',
    ).toEqual([PROJECTS_BUSY])

    thirdBudget.release({ ok: true, data: { document: BUDGET_DOC } })
    await flush()
    expect(busyLabels(document)).toEqual([])
    expect(holdsText(policyPanelOf(document), POLICY_NODE_TITLE)).toBe(true)
  })

  it('keeps a later section’s content when a sibling’s read is refused', async () => {
    // The one document two sections are drawn from is refused: both draw the
    // CLI's own sentence, because both are the same read's (D1488) -- and the
    // sections whose own reads answered lose nothing to it.
    const { bridge } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(liveFrame()),
        showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
        listModels: () => ({ ok: true, data: { document: MODELS_DOC } }),
        readSettings: () => ({ ok: true, data: { document: SETTINGS_DOC } }),
        managedStatus: () => ({ ok: false, reason: MANAGED_REFUSAL }),
        policyShow: () => ({ ok: true, data: { document: POLICY_DOC } }),
        environmentReport: () => ({ ok: true, data: { document: DOCTOR_DOC } }),
      },
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'projects').click()
    await flush()
    const root = panelOf(document)
    const section = (className: string): FakeNode => classed(root, className)[0] as FakeNode
    expect(holdsText(root, MANAGED_REFUSAL)).toBe(true)
    // Both sections of the refused read say so, in the CLI's own words: they are
    // one document and two drawings, and a refusal is the document's.
    expect(holdsText(section(PROJECTS_MANAGED_CLASS), MANAGED_REFUSAL)).toBe(true)
    expect(holdsText(section(PROJECTS_RETIREMENTS_CLASS), MANAGED_REFUSAL)).toBe(true)
    expect(holdsText(section(PROJECTS_CREDENTIALS_SERVICE_CLASS), CREDENTIALS_SERVICE_TITLE)).toBe(true)
    expect(holdsText(section(PROJECTS_ACCOUNT_KEYS_CLASS), ACCOUNT_KEYS_TITLE)).toBe(true)
    expect(classed(root, 'projects-kill-row').length).toBeGreaterThan(0)
    expect(holdsText(policyPanelOf(document), POLICY_NODE_TITLE)).toBe(true)
  })

  it('paints the settings section on its own token, and a document of another version is its own sentence', async () => {
    // **The section's own lifecycle**, which is the whole of the progressive
    // rendering rule (the owner's rule of 2026-10-31, `.claude/rules/console.md`):
    // the settings read is the third of three, and it is held by its own token
    // rather than by the tab's. The case shows both halves -- a document of
    // version 2 draws the console's own sentence and no table, and the read that
    // answers afterwards is the one that ends the mark.
    const settings = holding()
    const { bridge, calls } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(liveFrame()),
        showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
        listModels: () => ({ ok: true, data: { document: MODELS_DOC } }),
        readSettings: () => settings.reply(),
      },
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'projects').click()
    await flush()
    // The budget and the models have painted; the settings read is still out, and
    // it is a mark on **two** sections: the settings panel's and the account
    // keys' own, which are one read and two sections (D1488).
    expect(busyLabels(document)).toEqual([PROJECTS_SETTINGS_BUSY, ACCOUNT_KEYS_BUSY])
    settings.release({ ok: true, data: { document: { ...SETTINGS_DOC, version: 2 } } })
    await flush()
    expect(busyLabels(document)).toEqual([])
    const section = classed(panelOf(document), 'projects-settings')[0]
    expect(section, 'the tab drew no settings section').toBeDefined()
    expect(
      holdsText(section as FakeNode, 'settings --json is version 2 and this console reads version 1'),
    ).toBe(true)
    // A document this build does not read draws no table: the rows of the
    // document that did arrive are not on screen.
    expect(classed(section as FakeNode, 'projects-settings-row')).toEqual([])
    expect(calls.some((call) => call.name === 'readSettings')).toBe(true)
  })

  it('opens the row’s own editor on a pencil press, and sends only what the operator’s control asks', async () => {
    // The press draws the editor and sends nothing: an edit is the operator's
    // value, and the pencil is a control that opens a box (C3). What leaves the
    // renderer is the three members `settings.edit`'s schema closes -- the
    // subcommand, the key and the value -- and the layer, the dry run, the
    // window and the `--yes` are the main process's.
    const sent: unknown[] = []
    const { bridge } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(liveFrame()),
        showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
        listModels: () => ({ ok: true, data: { document: MODELS_DOC } }),
        readSettings: () => ({ ok: true, data: { document: SETTINGS_DOC } }),
      },
    })
    bridge['editSetting'] = (args: unknown): unknown => {
      sent.push(args)
      return {
        ok: true,
        data: { ok: true, sentence: 'wrote layers/project.yaml (backup at layers/project.yaml.bak)' },
      }
    }
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'projects').click()
    await flush()

    const pencil = classed(panelOf(document), SETTINGS_PENCIL_CLASS)[0] as FakeNode
    expect(pencil, 'the settings table drew no pencil').toBeDefined()
    const row = pencil.closest('li')
    expect(row, 'the pencil is not inside a row').not.toBeNull()

    pencil.click()
    await flush()
    expect(sent, 'a pencil press sent an edit').toEqual([])
    // D1116: the press puts the focus on the pencil as the editor is drawn, so
    // it does not drop the keyboard to `<body>`. That is the half of the rule
    // the entry owns; the other half -- the keyboard back on the pencil when the
    // confirmation window closes -- is not done, and `bindSettingsPencils` names
    // that limit where the code is.
    expect(document.activeElement, 'the press dropped the keyboard').toBe(pencil)

    const editor = classed(panelOf(document), SETTINGS_EDITOR_CLASS)[0] as FakeNode
    expect(editor, 'the pencil drew no editor').toBeDefined()
    // The editor is drawn **inside the row it edits**, so the key, the layer
    // that decides it and the value a run gets stay on the screen around the
    // box the operator is typing in.
    expect(editor.closest('li')).toBe(row)
    expect(holdsText(editor, 'New value for budget.per_run_usd')).toBe(true)
    expect(holdsText(editor, 'Leave the box empty to remove the key.')).toBe(true)
    // The sentence the flow ends with is an announced region, like the launch
    // form's: a refusal can arrive while the operator is looking elsewhere.
    expect(classed(editor, SETTINGS_EDIT_OUTCOME_CLASS)[0]?.getAttribute('aria-live')).toBe('polite')

    typeValue(theBox(editor), '25')
    controlUnder(editor, 'Review and write').click()
    await flush()
    expect(sent).toEqual([{ op: 'set', key: 'budget.per_run_usd', value: '25' }])
    expect(holdsText(editor, 'wrote layers/project.yaml (backup at layers/project.yaml.bak)')).toBe(true)

    // The empty box is the **unset** subcommand and never a value of `""`.
    controlUnder(editor, 'Cancel').click()
    await flush()
    expect(classed(panelOf(document), SETTINGS_EDITOR_CLASS)).toEqual([])

    pencil.click()
    await flush()
    const reopened = classed(panelOf(document), SETTINGS_EDITOR_CLASS)[0] as FakeNode
    controlUnder(reopened, 'Review and write').click()
    await flush()
    expect(sent).toEqual([
      { op: 'set', key: 'budget.per_run_usd', value: '25' },
      { op: 'unset', key: 'budget.per_run_usd' },
    ])
  })

  it('disables the control that starts an edit while one is in flight', async () => {
    // A double press is not two flows over one row: the control that sends is
    // disabled and the editor says it is busy for as long as the edit takes --
    // the dry run, the window and, on a yes, the write.
    const held = holding()
    const { bridge } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(liveFrame()),
        showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
        listModels: () => ({ ok: true, data: { document: MODELS_DOC } }),
        readSettings: () => ({ ok: true, data: { document: SETTINGS_DOC } }),
      },
    })
    bridge['editSetting'] = () => held.reply()
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'projects').click()
    await flush()

    ;(classed(panelOf(document), SETTINGS_PENCIL_CLASS)[0] as FakeNode).click()
    await flush()
    const editor = classed(panelOf(document), SETTINGS_EDITOR_CLASS)[0] as FakeNode
    typeValue(theBox(editor), '25')
    const submit = controlUnder(editor, 'Review and write')
    submit.click()
    await flush()

    expect((submit as unknown as { readonly disabled: boolean }).disabled).toBe(true)
    expect(editor.getAttribute('aria-busy')).toBe('true')

    held.release({ ok: true, data: { ok: true, sentence: 'wrote layers/project.yaml (backup at layers/project.yaml.bak)' } })
    await flush()
    expect(editor.getAttribute('aria-busy')).toBe('false')
    expect((submit as unknown as { readonly disabled: boolean }).disabled).toBe(true)
  })

  it('draws the refusal’s own sentence and offers a read, and never sends the edit again', async () => {
    // D1112: the CLI's compare-and-swap refuses an edit built on bytes that
    // moved, and the console shows that sentence and offers the section's own
    // `settings --json` again. A silent second write would be this console
    // deciding an edit it was told not to make.
    const sent: unknown[] = []
    const stale =
      'layers/project.yaml changed since it was read: nothing was written; read it again and repeat the edit'
    const { bridge, calls } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(liveFrame()),
        showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
        listModels: () => ({ ok: true, data: { document: MODELS_DOC } }),
        readSettings: () => ({ ok: true, data: { document: SETTINGS_DOC } }),
      },
    })
    bridge['editSetting'] = (args: unknown): unknown => {
      sent.push(args)
      return { ok: true, data: { ok: false, sentence: stale } }
    }
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'projects').click()
    await flush()

    ;(classed(panelOf(document), SETTINGS_PENCIL_CLASS)[0] as FakeNode).click()
    await flush()
    const editor = classed(panelOf(document), SETTINGS_EDITOR_CLASS)[0] as FakeNode
    typeValue(theBox(editor), '25')
    controlUnder(editor, 'Review and write').click()
    await flush()

    expect(holdsText(editor, stale), 'the refusal’s sentence is not drawn').toBe(true)
    const reads = calls.filter((call) => call.name === 'readSettings').length

    controlUnder(editor, 'Read again').click()
    await flush()
    // The control is a **read**, and the edit went out once.
    expect(calls.filter((call) => call.name === 'readSettings')).toHaveLength(reads + 1)
    expect(sent).toHaveLength(1)
    // The editor goes with the read: the row it was drawn into is one of the
    // rows that read's repaint replaces.
    expect(classed(panelOf(document), SETTINGS_EDITOR_CLASS)).toEqual([])
  })

  it('asks for no models read at all below the command’s floor, and says which version it needs', async () => {
    // `models ls` landed after v0.15.0: a 0.15.x CLI answers `unknown command
    // "ls"`, which is a failure the operator can do nothing about. The floor is
    // asked **before** the read goes out, and below it the panel draws the
    // console's own sentence instead.
    const { bridge, calls } = fakeBridge({
      compatibility: { ...anOkCompatibility(), cliVersion: '0.15.3' },
      replies: {
        subscribeLive: () => snapshotReply(liveFrame()),
        showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
        listModels: () => ({ ok: true, data: { document: MODELS_DOC } }),
      },
    })
    const document = await boot(bridge, new FakeDocument())
    const before = calls.length
    buttonFor(document, 'projects').click()
    await flush()
    expect(
      calls.slice(before).some((call) => call.name === 'listModels'),
      'the tab asked a CLI below the floor for `models ls`',
    ).toBe(false)
    const models = classed(panelOf(document), 'projects-models')[0]
    expect(models, 'the tab drew no models section').toBeDefined()
    expect(holdsText(models as FakeNode, 'the models read needs fugaro 0.16.0 and this CLI is 0.15.3')).toBe(true)
    expect(classed(models as FakeNode, 'projects-models-route-list')).toEqual([])
  })

  it('draws the Coordinator’s own header in the click’s frame, and its summary the moment the view lands', async () => {
    // The longest read of any tab: the header is the console's own words and
    // needs no document, so it is drawn before the read is awaited -- and the
    // summary follows the document in the same turn, while each repository's
    // records are laid out in a chunk of their own (a 724 KB document read in
    // one turn is the freeze the chunks are for).
    const status = holding()
    const { bridge } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(liveFrame()),
        coordinatorStatus: () => status.reply(),
      },
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'coordinator').click()
    // Nothing is awaited: this is the frame the click happened in.
    const source = classed(panelOf(document), 'coordinator-source')[0]
    expect(source?.textContent, 'the Coordinator drew no source line before its read').toBe(
      COORDINATOR_SOURCE_LINE,
    )
    expect(classed(panelOf(document), 'coordinator-age')[0]?.textContent).toBe(COORDINATOR_NO_AGE_WORD)
    // One mark per section the frame holds, in the order the frame draws them --
    // the queue, the fleet's totals, the panel of repositories -- because each
    // is a read of its own and none of them waits for another (the owner's rule
    // of 2026-10-31, drawn).
    expect(busyLabels(document)).toEqual([
      COORDINATOR_QUEUE_BUSY,
      COORDINATOR_WINDOW_BUSY,
      COORDINATOR_TOTALS_BUSY,
      COORDINATOR_BUSY,
      COORDINATOR_TRANSCRIPT_BUSY,
    ])
    expect(skeletonsOf(document)).toEqual(SKELETON_SHAPES.coordinator.map((shape) => shape.section))
    expect(classed(panelOf(document), 'coordinator-detail')).toEqual([])

    status.release({ ok: true, data: { document: COORDINATOR_STATUS } })
    await flush()
    // The summary is up and the mark is gone; the details are queued, one
    // repository per chunk.
    expect(busyLabels(document)).toEqual([])
    expect(skeletonsOf(document)).toEqual([])
    const details = classed(panelOf(document), 'coordinator-detail')
    expect(details.length, 'the summary drew no repository detail host').toBeGreaterThan(0)
    expect(classed(panelOf(document), 'coordinator-record'), 'the detail was drawn in the summary’s own turn').toEqual([])

    await vi.advanceTimersByTimeAsync(10)
    expect(
      classed(panelOf(document), 'coordinator-record').length,
      'the queued chunks never drew the records',
    ).toBeGreaterThan(0)
  })

  it('draws the Coordinator’s cached view at once, stamped, and reads it again only past five minutes', async () => {
    const statuses: ReturnType<typeof holding>[] = []
    const { bridge, calls } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(liveFrame()),
        coordinatorStatus: () => {
          const answer = holding()
          statuses.push(answer)
          return answer.reply()
        },
      },
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'coordinator').click()
    await flush()
    statuses[0]?.release({ ok: true, data: { document: COORDINATOR_STATUS } })
    await flush()
    await vi.advanceTimersByTimeAsync(10)

    buttonFor(document, 'live').click()
    await flush()
    buttonFor(document, 'coordinator').click()
    // Nothing is awaited: the view this session already read is drawn in the
    // click's own frame -- summary and all -- and it says when it was read.
    expect(
      classed(panelOf(document), 'coordinator-detail').length,
      'the cached view drew nothing in the click’s own frame',
    ).toBeGreaterThan(0)
    expect(stamped(document)).toMatch(/^as of \d\d:\d\d$/)
    await vi.advanceTimersByTimeAsync(10)
    expect(classed(panelOf(document), 'coordinator-record').length).toBeGreaterThan(0)
    await flush()
    expect(
      calls.filter((call) => call.name === 'coordinatorStatus').length,
      'a view that came out of the cache was read again',
    ).toBe(1)

    // Five minutes old, and it is drawn **and** read again: a view is never
    // silently older than the rule allows.
    vi.setSystemTime(Date.now() + CACHE_FRESH_MS)
    buttonFor(document, 'live').click()
    await flush()
    buttonFor(document, 'coordinator').click()
    expect(stamped(document), 'the stale view was served without its stamp').not.toBeNull()
    expect(busyLabels(document)).toEqual([
      COORDINATOR_QUEUE_BUSY,
      COORDINATOR_WINDOW_BUSY,
      COORDINATOR_TOTALS_BUSY,
      COORDINATOR_BUSY,
      COORDINATOR_TRANSCRIPT_BUSY,
    ])
    await flush()
    expect(calls.filter((call) => call.name === 'coordinatorStatus').length).toBe(2)
  })

  it('starts the Coordinator’s own reads in the click’s own turn, and reads the jobs only when the queue is asked for them', async () => {
    // Two rules meet on the screen with the most reads of any tab. The owner's
    // rule of 2026-10-31: the stored view, the plan tasks and the budget are
    // three independent documents, so all three are asked for in the turn the
    // tab was opened in and each part of the queue is painted the moment its own
    // answer lands -- never one spinner held until the last of them. And rule 5
    // of §6.2's opening: jobs are never part of a first paint, so `runs ls --json`
    // is the one read the tab does not make on entry -- the part the jobs fill
    // carries the control that asks for them, and the read goes out on the press.
    const status = holding()
    const runs = holding()
    const plans = holding()
    const caps = holding()
    const { bridge, calls } = queueBridge({
      coordinatorStatus: () => status.reply(),
      listRuns: () => runs.reply(),
      planStatus: () => plans.reply(),
      showBudget: () => caps.reply(),
    })
    const document = await boot(bridge, new FakeDocument())
    const before = calls.length
    buttonFor(document, 'coordinator').click()
    // **Nothing is awaited, and that is the claim**: the five calls went out in
    // the click's own turn, so none of them is a consequence of another. A
    // screen that asked for the plan tasks after the stored view had answered
    // would show one call here, and the queue would be behind the slowest read
    // of the page. `listRuns` is not among them: the jobs are the read this tab
    // does not make until the operator asks. The pane's own read is the fourth
    // (K5): the declarations it draws first are a document of their own, and a
    // pane that waited for the stored view would be the all-or-nothing redraw
    // the same rule forbids. The fifth is the fleet strip's window (0.119.0):
    // `runs summary` is a command of its own -- its document is the runs counted
    // by day, not the run objects `runs ls` lists -- so it is the strip's second
    // read and never a consequence of the sweep's.
    expect(calls.slice(before).map((call) => call.name)).toEqual([
      'coordinatorStatus',
      'planStatus',
      'showBudget',
      'summaryRuns',
      'coordinatorIntents',
    ])
    // While they are all out, each section says what it is reading.
    expect(busyLabels(document)).toEqual([
      COORDINATOR_QUEUE_BUSY,
      COORDINATOR_WINDOW_BUSY,
      COORDINATOR_TOTALS_BUSY,
      COORDINATOR_BUSY,
      COORDINATOR_TRANSCRIPT_BUSY,
    ])

    // The budget is the smallest of the three and answers first: its refusal is
    // on the screen while the pull requests and the plan tasks -- the two reads
    // this page is mostly about -- are still coming.
    caps.release({ ok: false, reason: BUDGET_REFUSAL })
    await flush()
    const parts = classed(panelOf(document), COORDINATOR_QUEUE_PART_CLASS)
    expect(
      parts.map((part) => classed(part, 'unavailable-operation')[0]?.textContent ?? null),
      'an answer painted a part that is not its own',
    ).toEqual([null, null, null, 'budget.show'])
    expect(busyLabels(document), 'the queue stopped saying it is reading one answer in').toContain(
      COORDINATOR_QUEUE_BUSY,
    )

    // The other two, and every read this visit makes has answered: the item the
    // plan read carried is drawn, the section's mark comes down -- a queue
    // waiting on a press is not a queue being read -- and the jobs have not been
    // asked for at all.
    plans.release(planStatusReply(QUEUE_PLAN))
    status.release({ ok: true, data: { document: COORDINATOR_STATUS } })
    await flush()
    expect(holdsText(panelOf(document), 'task-three')).toBe(true)
    expect(busyLabels(document)).toEqual([])
    // The queue has read nothing of the run list: the Live screen's own join
    // asked for it (`maybeRefreshRigor`), and this tab has not.
    const readForTheQueue = calls.filter((call) => call.name === 'listRuns').length

    // The press is the read, and it is one read: the control says it was pressed
    // and the section says it is reading again.
    buttonLabelled(panelOf(document), COORDINATOR_QUEUE_JOBS_WORD).click()
    expect(calls.filter((call) => call.name === 'listRuns').length - readForTheQueue).toBe(1)
    expect(
      buttonLabelled(panelOf(document), COORDINATOR_QUEUE_JOBS_WORD).attributes.get('aria-expanded'),
    ).toBe('true')
    expect(busyLabels(document)).toContain(COORDINATOR_QUEUE_BUSY)

    runs.release({ ok: true, data: { document: { runs: [QUEUE_RUN] } } })
    await flush()
    // The item is named by the run's **short id**, as every list view of this
    // console names one (SR10): the whole id is the value the item's act
    // carries, and never the text this row draws.
    expect(holdsText(panelOf(document), shortRun(QUEUE_RUN_ID))).toBe(true)
    // One button per item: the job's Retry and the ready task's Launch.
    expect(
      classed(panelOf(document), 'coordinator-queue-button').length,
      'the queue drew no button for the two items it found',
    ).toBe(2)
    // The control is gone, replaced by the items it asked for: a second press
    // has nothing to press.
    expect(classed(panelOf(document), COORDINATOR_QUEUE_JOBS_CLASS)).toEqual([])
    // Every read has answered, so no section is waiting any more -- and the
    // budget's refusal is still where it belongs, drawn in its own part: the
    // reads that answered are not taken down for it, and it is not taken down
    // for them.
    expect(busyLabels(document)).toEqual([])
    expect(
      classed(panelOf(document), COORDINATOR_QUEUE_PART_CLASS).map(
        (part) => classed(part, 'unavailable-operation')[0]?.textContent ?? null,
      ),
    ).toEqual([null, null, null, 'budget.show'])
  })

  it('draws a queue it cannot decode as `runs ls`’s own refusal, with no item and no button', async () => {
    // The queue's reader refuses a page holding a row it cannot read -- here a
    // `terminal` that is a word where the struct writes a boolean -- and it
    // refuses it **whole**: what is drawn is the section's own refusal, the
    // state a read that failed already draws, naming `runs ls`, rather than the
    // rows around it. A queue that dropped the row it could not read would draw
    // a shorter list than the document holds, and the operator would have no way
    // to tell that from a shorter day.
    const { bridge } = queueBridge({
      coordinatorStatus: () => ({ ok: true, data: { document: COORDINATOR_STATUS } }),
      planStatus: () => planStatusReply(QUEUE_PLAN),
      showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
      listRuns: () => ({
        ok: true,
        data: { document: { runs: [{ ...QUEUE_RUN, terminal: 'yes' }] } },
      }),
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'coordinator').click()
    await flush()
    await askForJobs(document)
    const job = classed(panelOf(document), COORDINATOR_QUEUE_PART_CLASS)[1]
    if (job === undefined) throw new Error('the queue drew no runs part')
    expect(classed(job, 'unavailable-operation')[0]?.textContent).toBe('runs.list')
    expect(classed(job, 'unavailable-sentence')[0]?.textContent).toBe(
      'the runs list document is not one the console reads: runs[0].terminal is not-a-boolean',
    )
    // The row the decoder refused reached neither the drawing nor an act: the
    // runs part holds no item and no button, and the run's own id is nowhere on
    // the screen. The plan read's ready task is still the one item drawn -- it
    // was never the document that was refused.
    expect(classed(job, 'coordinator-queue-item')).toEqual([])
    expect(classed(job, 'coordinator-queue-button')).toEqual([])
    expect(holdsText(panelOf(document), shortRun(QUEUE_RUN_ID))).toBe(false)
    expect(holdsText(panelOf(document), 'task-three')).toBe(true)
  })


  it('reads the fleet’s window with the tab’s other first-paint reads, and never with `runs ls`', async () => {
    // Console 0.119.0. The strip's window is a read of its own -- `runs summary
    // --json` counts runs by day and lists no run object -- so it goes out in the
    // click's own turn beside the three K1 documents, and **never** waits on or
    // triggers `runs ls`: the jobs are rule 5's read and this section does not
    // draw a figure from them (D819).
    const window = holding()
    const { bridge, calls } = queueBridge({
      coordinatorStatus: () => ({ ok: true, data: { document: COORDINATOR_STATUS } }),
      planStatus: () => planStatusReply(QUEUE_PLAN),
      showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
      summaryRuns: () => window.reply(),
    })
    const document = await boot(bridge, new FakeDocument())
    const before = calls.length
    buttonFor(document, 'coordinator').click()
    const made = calls.slice(before)
    expect(made.map((call) => call.name)).toContain('summaryRuns')
    expect(
      made.filter((call) => call.name === 'listRuns'),
      'the Coordinator made the job list’s read as part of its first paint',
    ).toEqual([])
    // The window is asked for the console's own window and nothing else: the
    // surface admits two words, and this page opens on the week.
    expect(made.find((call) => call.name === 'summaryRuns')?.args).toEqual({ since: COORDINATOR_SUMMARY_SINCE })
    // The strip says both of its reads out: the window's mark is its own, beside
    // the one the K1 documents wear.
    expect(busyLabels(document)).toEqual([
      COORDINATOR_QUEUE_BUSY,
      COORDINATOR_WINDOW_BUSY,
      COORDINATOR_TOTALS_BUSY,
      COORDINATOR_BUSY,
      COORDINATOR_TRANSCRIPT_BUSY,
    ])
    // A read that has not answered draws no window, and no zero in its place --
    // and the host its mark goes on is in the strip, because this visit reads it.
    expect(classed(panelOf(document), 'coordinator-window-runs')).toEqual([])
    expect(classed(panelOf(document), COORDINATOR_WINDOW_SLOT_CLASS).length).toBe(1)

    window.release({ ok: true, data: { document: aWindow({ runs: 9, succeeded: 6, failed: 2 }) } })
    await flush()
    expect(busyLabels(document)).toEqual([])
    expect(figureOfIn(panelOf(document), 'coordinator-window-runs')).toEqual([COORDINATOR_WINDOW_RUNS_WORD, '9'])
    expect(figureOfIn(panelOf(document), 'coordinator-window-succeeded')).toEqual([
      COORDINATOR_WINDOW_SUCCEEDED_WORD,
      '6',
    ])
    // The strip's own figures are still there: the window is a second region of
    // the panel, drawn beside the sweep's counts and never instead of them.
    expect(classed(panelOf(document), 'coordinator-total-repos').length).toBe(1)
  })

  it('asks no CLI below the window’s floor for it, and draws the sentence naming the version it needs', async () => {
    // `runs summary` landed after v0.15.0, so a CLI below `RUNS_SUMMARY_CLI_FLOOR`
    // has no such verb and answers `unknown command "summary"`. Asking anyway
    // would draw that failure as the fleet's week, which is a broken project
    // rather than a fact, so the floor is asked before anything goes out.
    const { bridge, calls } = jobsBridge(
      {
        coordinatorStatus: () => ({ ok: true, data: { document: COORDINATOR_STATUS } }),
        planStatus: () => planStatusReply(QUEUE_PLAN),
        showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
      },
      '0.15.3',
    )
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'coordinator').click()
    await flush()
    expect(calls.filter((call) => call.name === 'summaryRuns')).toEqual([])
    expect(textIn(panelOf(document), 'coordinator-window-floor-word')).toBe(COORDINATOR_WINDOW_FLOOR_WORD)
    expect(textIn(panelOf(document), 'coordinator-window-floor-version')).toBe('0.15.3')
    // The sentence is the whole of what the window says: no figures behind a
    // read that was never made, and no mark left up for one either.
    expect(classed(panelOf(document), 'coordinator-window-runs')).toEqual([])
    expect(busyLabels(document)).not.toContain(COORDINATOR_WINDOW_BUSY)
    // The host the window's mark would have gone on is not kept either: a slot
    // is a flex item of the strip, and one no read on this visit can fill is a
    // line held open for nothing.
    expect(classed(panelOf(document), COORDINATOR_WINDOW_SLOT_CLASS)).toEqual([])
    // ...and the strip's own six figures are untouched by it: two reads of one
    // panel, and only one of them is below its floor.
    expect(classed(panelOf(document), 'coordinator-total-repos').length).toBe(1)
  })

  it('names `runs.summary` when the window’s own read is refused, and keeps the strip’s figures', async () => {
    const window = holding()
    const { bridge } = queueBridge({
      coordinatorStatus: () => ({ ok: true, data: { document: COORDINATOR_STATUS } }),
      planStatus: () => planStatusReply(QUEUE_PLAN),
      showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
      summaryRuns: () => window.reply(),
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'coordinator').click()
    await flush()
    window.release({ ok: false, reason: WINDOW_REFUSAL })
    await flush()
    // A refusal names its own command and nothing else -- `runs.summary`, the
    // operation's id on this surface, beside the `runs.list` and `budget.show`
    // the same screen draws.
    expect(classed(panelOf(document), 'unavailable-operation').map((node) => node.textContent)).toContain(
      'runs.summary',
    )
    expect(holdsText(panelOf(document), WINDOW_REFUSAL)).toBe(true)
    expect(busyLabels(document)).toEqual([])
    // The window is gone rather than stale, and the strip's own figures are not
    // taken down for a read that is not theirs.
    expect(classed(panelOf(document), 'coordinator-window-runs')).toEqual([])
    expect(classed(panelOf(document), 'coordinator-total-repos').length).toBe(1)
  })

  it('treats a window read that fails to answer at all as a refusal, and takes its mark down', async () => {
    // `readBridgeFrom` answers `null` for a read that failed to answer -- a
    // member that threw, or a promise that rejected, which is what `invoke` does
    // on a channel with nothing behind it -- so a rejection reaching the entry is
    // a bridge built by something other than that function. Left uncaught it
    // takes the visit's `summaryOut` with it: the strip would wear the window's
    // mark for the rest of the visit with no read left to answer it, which is a
    // section that never settles rather than one that refused.
    const { bridge } = queueBridge({
      coordinatorStatus: () => ({ ok: true, data: { document: COORDINATOR_STATUS } }),
      planStatus: () => planStatusReply(QUEUE_PLAN),
      showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
      summaryRuns: () => Promise.reject(new Error('the channel is gone')),
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'coordinator').click()
    await flush()
    // The read is drawn as the refusal a reply nobody could read gets: its own
    // command named, and the mark down.
    expect(classed(panelOf(document), 'unavailable-operation').map((node) => node.textContent)).toContain(
      'runs.summary',
    )
    expect(busyLabels(document)).not.toContain(COORDINATOR_WINDOW_BUSY)
    // ...and the strip's own figures are not taken down for a read that is not
    // theirs.
    expect(classed(panelOf(document), 'coordinator-total-repos').length).toBe(1)
  })

  it('draws the pane’s declared intents when the pane’s own read lands, and says when it read them', async () => {
    // K5: the pane has a read of its own -- `coordinator intents --all --json`
    // -- so it draws nothing until that read answers, and what it draws then
    // carries the console's own clock. The stored view is answered here and the
    // declarations are held, which is what tells the two halves apart: the
    // questions under them are the view's and are already up, and the lines
    // above them are the document that has not landed.
    const intents = holding()
    const { bridge, calls } = queueBridge({
      coordinatorStatus: () => ({ ok: true, data: { document: COORDINATOR_STATUS } }),
      planStatus: () => planStatusReply(QUEUE_PLAN),
      showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
      coordinatorIntents: () => intents.reply(),
    })
    const document = await boot(bridge, new FakeDocument())
    const before = calls.length
    buttonFor(document, 'coordinator').click()
    expect(
      calls.slice(before).map((call) => call.name),
      'the pane’s own read did not go out with the tab’s other reads',
    ).toContain('coordinatorIntents')
    expect(busyLabels(document)).toContain(COORDINATOR_TRANSCRIPT_BUSY)
    await flush()
    // The stored view answered and the declarations have not: the three
    // questions are up, answered off the view, and the lines above them are not
    // -- a read that has not answered draws no declaration at all, and the pane
    // never draws the empty store's own sentence for a document it does not
    // have.
    expect(classed(panelOf(document), 'coordinator-transcript-question')).toHaveLength(3)
    expect(classed(panelOf(document), 'coordinator-transcript-intent')).toEqual([])
    expect(classed(panelOf(document), 'coordinator-transcript-repo')).toEqual([])
    expect(holdsText(panelOf(document), COORDINATOR_TRANSCRIPT_WORD)).toBe(true)
    // And the pane still says it is reading: every other section's landing
    // repaints the pane, and a repaint that took the mark down with the drawing
    // would leave a read in flight looking like a pane that had answered with
    // nothing.
    expect(
      busyLabels(document),
      'another section’s landing took the pane’s mark down',
    ).toContain(COORDINATOR_TRANSCRIPT_BUSY)

    intents.release({ ok: true, data: { document: AN_INTENTS_DOCUMENT } })
    await flush()
    expect(classed(panelOf(document), 'coordinator-transcript-repo')[0]?.textContent).toBe('owner/repo-a')
    expect(classed(panelOf(document), 'coordinator-transcript-intent')).toHaveLength(1)
    expect(classed(panelOf(document), 'coordinator-transcript-declaration')[0]?.textContent).toBe('person-1')
    expect(stamped(document), 'the pane drew no clock of its own').toMatch(/^as of \d\d:\d\d$/)
    expect(busyLabels(document)).toEqual([])
  })

  it('names `coordinator intents` when the pane’s own read is refused', async () => {
    // The pane is a read of its own, so a refusal is its own section's and
    // names its own command: the stored view answered, and its panels are not
    // taken down for a read that is not theirs.
    const { bridge } = queueBridge({
      coordinatorStatus: () => ({ ok: true, data: { document: COORDINATOR_STATUS } }),
      planStatus: () => planStatusReply(QUEUE_PLAN),
      showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
      coordinatorIntents: () => ({ ok: false, reason: INTENTS_REFUSAL }),
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'coordinator').click()
    await flush()
    const pane = classed(panelOf(document), COORDINATOR_TRANSCRIPT_CLASS)[0]
    if (pane === undefined) throw new Error('the Coordinator drew no transcript pane')
    expect(classed(pane, 'unavailable-operation')[0]?.textContent).toBe('coordinator.intents')
    expect(holdsText(pane, INTENTS_REFUSAL)).toBe(true)
    expect(classed(panelOf(document), 'coordinator-detail').length).toBeGreaterThan(0)
    expect(busyLabels(document)).toEqual([])
  })

  it('reads the declarations again from the pane’s own Refresh, and keeps them under the mark', async () => {
    // `reReadIntents` is the pane's own press, and it is not the tab's Refresh:
    // the questions under the declarations are the stored view's and are read by
    // that view's own control, so this one reads `coordinator intents` and
    // nothing else -- one read, of the same kind the tab made on entry.
    const first = holding()
    const again = holding()
    const asked: unknown[] = []
    const { bridge } = queueBridge({
      coordinatorStatus: () => ({ ok: true, data: { document: COORDINATOR_STATUS } }),
      planStatus: () => planStatusReply(QUEUE_PLAN),
      showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
      coordinatorIntents: (args) => {
        asked.push(args)
        return asked.length === 1 ? first.reply() : again.reply()
      },
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'coordinator').click()
    await flush()
    first.release({ ok: true, data: { document: AN_INTENTS_DOCUMENT } })
    await flush()
    const pane = classed(panelOf(document), COORDINATOR_TRANSCRIPT_CLASS)[0]
    if (pane === undefined) throw new Error('the Coordinator drew no transcript pane')
    expect(classed(pane, 'coordinator-transcript-intent')).toHaveLength(1)

    const refresh = classed(pane, STAMP_REFRESH_CLASS)[0]
    if (refresh === undefined) throw new Error('the pane drew no refresh control')
    refresh.click()
    expect(asked, 'the pane’s own Refresh read something other than the declarations').toHaveLength(2)
    // The press is answered where it happened: the mark goes up over the pane
    // whose read it is, and the declarations the last answer drew stay under it
    // rather than blanking.
    expect(busyLabels(document)).toContain(COORDINATOR_TRANSCRIPT_BUSY)
    expect(classed(pane, 'coordinator-transcript-intent')).toHaveLength(1)

    again.release({ ok: true, data: { document: AN_INTENTS_DOCUMENT } })
    await flush()
    expect(classed(pane, 'coordinator-transcript-intent')).toHaveLength(1)
    expect(textIn(pane, STAMP_AT_CLASS)).toMatch(/^as of \d\d:\d\d$/)
    expect(busyLabels(document)).toEqual([])
  })

  it('drops the pane’s clock when its own read is refused, and keeps the one press', async () => {
    // K5's follow-up, on the #1581 review. The declarations are dropped the
    // moment a read is refused -- a refusal is the whole of what the pane has to
    // say -- and the clock the stamp carried was the **previous** success's, so a
    // pane that drew it beside the sentence would be dating the CLI's refusal
    // with a read that has nothing to do with it. The time goes, and the pane's
    // one press stays: without it the operator's doubt has nowhere to go.
    const first = holding()
    const refused = holding()
    const asked: unknown[] = []
    const { bridge } = queueBridge({
      coordinatorStatus: () => ({ ok: true, data: { document: COORDINATOR_STATUS } }),
      planStatus: () => planStatusReply(QUEUE_PLAN),
      showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
      coordinatorIntents: (args) => {
        asked.push(args)
        return asked.length === 1 ? first.reply() : refused.reply()
      },
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'coordinator').click()
    await flush()
    first.release({ ok: true, data: { document: AN_INTENTS_DOCUMENT } })
    await flush()
    const pane = classed(panelOf(document), COORDINATOR_TRANSCRIPT_CLASS)[0]
    if (pane === undefined) throw new Error('the Coordinator drew no transcript pane')
    expect(textIn(pane, STAMP_AT_CLASS)).toMatch(/^as of \d\d:\d\d$/)

    const refresh = classed(pane, STAMP_REFRESH_CLASS)[0]
    if (refresh === undefined) throw new Error('the pane drew no refresh control')
    refresh.click()
    // While the read is out the declarations on screen are still the ones that
    // read answered, and the clock beside them is still theirs.
    expect(textIn(pane, STAMP_AT_CLASS)).toMatch(/^as of \d\d:\d\d$/)
    refused.release({ ok: false, reason: INTENTS_REFUSAL })
    await flush()

    expect(classed(pane, 'unavailable-operation')[0]?.textContent).toBe('coordinator.intents')
    expect(holdsText(pane, INTENTS_REFUSAL)).toBe(true)
    expect(
      classed(pane, STAMP_AT_CLASS),
      'a refused read dated itself with the previous success’s clock',
    ).toEqual([])
    const left = classed(pane, STAMP_REFRESH_CLASS)[0]
    if (left === undefined) throw new Error('the refused pane drew no press at all')
    expect(left.textContent).toBe(STAMP_REFRESH_LABEL)
    expect(busyLabels(document)).toEqual([])
  })

  it('spawns no runs ls for the job list until Load jobs is pressed, and pages from the last row it drew', async () => {
    // The jobs rule, twice over (rule 5 of §6.2's opening). **No `runs ls` of
    // this tab's own goes out on entry**: the job list is a section of its own
    // with a read the operator asks for, so a screen that warmed it would be the
    // one change that breaks this. And the page after the first is asked with
    // **the last row the console drew** -- D818's cursor -- so the console never
    // holds a list of ids to page from.
    const first = holding()
    const second = holding()
    const asked: unknown[] = []
    const { bridge, calls } = jobsBridge({
      coordinatorStatus: () => ({ ok: true, data: { document: COORDINATOR_STATUS } }),
      planStatus: () => planStatusReply(QUEUE_PLAN),
      showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
      listRuns: (args) => {
        // The Live screen makes a `runs ls` read of its own; the job list's is
        // the only one that passes a page, which is what this case counts.
        if ((args as Record<string, unknown>)['limit'] === undefined) {
          return { ok: true, data: { document: { runs: [] } } }
        }
        asked.push(args)
        return asked.length === 1 ? first.reply() : second.reply()
      },
    })
    const document = await boot(bridge, new FakeDocument())
    const before = calls.length
    buttonFor(document, 'coordinator').click()
    await flush()

    expect(
      calls.slice(before).filter((call) => call.name === 'listRuns'),
      'the job list read the run list without being asked',
    ).toEqual([])
    expect(busyLabels(document)).toEqual([])
    // The section is on screen with exactly one control: the one that asks.
    expect(classed(panelOf(document), COORDINATOR_JOBS_LIST_CLASS)).toHaveLength(1)
    expect(classed(panelOf(document), 'coordinator-jobs-older')).toEqual([])

    // The press is the read, and it is one read, carrying the page the console
    // asks for and nothing else: no cursor yet, because there is no row drawn.
    buttonLabelled(panelOf(document), COORDINATOR_JOBS_LOAD_WORD).click()
    expect(asked).toEqual([{ since: RUNS_SINCE, limit: COORDINATOR_JOBS_PAGE }])
    // A press is answered in the frame it happened in: the section's own mark is
    // up over the control that asked.
    expect(busyLabels(document)).toContain(COORDINATOR_JOBS_BUSY)

    first.release({ ok: true, data: { document: lsPage(41, 12.5, [LS_ROW_A]) } })
    await flush()
    // The head is the **window's** own figure and the list is the page's: the two
    // numbers are different in this fixture, which is what tells a section that
    // drew `totals` from one that summed its rows.
    expect(textIn(panelOf(document), 'coordinator-jobs-window-word')).toBe(COORDINATOR_JOBS_WINDOW_WORD)
    expect(figureOfIn(panelOf(document), 'coordinator-jobs-window-runs')).toEqual([
      COORDINATOR_JOBS_RUNS_WORD,
      '41',
    ])
    expect(classed(panelOf(document), 'coordinator-job')).toHaveLength(1)
    expect(busyLabels(document)).toEqual([])

    // The row expands in place, and the press is a toggle rather than a read:
    // nothing is spawned by opening a row.
    const row = classed(panelOf(document), 'coordinator-job')[0]
    if (row === undefined) throw new Error('the list drew no row')
    const head = classed(row, 'coordinator-job-head')[0]
    if (head === undefined) throw new Error('the row drew no head')
    const detail = classed(row, COORDINATOR_JOB_DETAIL_CLASS)[0]
    expect(detail?.attributes.get('hidden')).toBe('')
    head.click()
    expect(detail?.attributes.get('hidden')).toBeUndefined()
    expect(asked).toHaveLength(1)

    // A page that came back short is the state in which the console knows the
    // window ends where the drawing does, so the control that asked is gone and
    // the one that asks for more is not offered: `Older 50` is a button for a
    // page there may be, and this page says there is not.
    expect(classed(panelOf(document), 'coordinator-jobs-load')).toEqual([])
    expect(classed(panelOf(document), 'coordinator-jobs-older')).toEqual([])
  })

  it('asks for the page after the one on screen with --after the last row it drew, and stops when a page comes back short', async () => {
    const first = holding()
    const second = holding()
    const asked: Record<string, unknown>[] = []
    const { bridge } = jobsBridge({
      coordinatorStatus: () => ({ ok: true, data: { document: COORDINATOR_STATUS } }),
      planStatus: () => planStatusReply(QUEUE_PLAN),
      showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
      listRuns: (args) => {
        const page = args as Record<string, unknown>
        if (page['limit'] === undefined) return { ok: true, data: { document: { runs: [] } } }
        asked.push(page)
        return asked.length === 1 ? first.reply() : second.reply()
      },
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'coordinator').click()
    await flush()
    buttonLabelled(panelOf(document), COORDINATOR_JOBS_LOAD_WORD).click()
    // A full page: `COORDINATOR_JOBS_PAGE` rows, so the Older control is offered.
    first.release({ ok: true, data: { document: lsPage(41, 12.5, aFullPage()) } })
    await flush()

    buttonLabelled(panelOf(document), COORDINATOR_JOBS_OLDER_WORD).click()
    // The cursor is **the last row's own id** (`LS_LAST_ID`) and nothing the
    // console computed: the two words are the page's and the cursor's.
    expect(asked).toHaveLength(2)
    expect(asked[1]).toEqual({ since: RUNS_SINCE, limit: COORDINATOR_JOBS_PAGE, after: LS_LAST_ID })
    expect(busyLabels(document)).toContain(COORDINATOR_JOBS_BUSY)

    // A short page: the window ends where the drawing does, so the control that
    // asked for more is not offered again -- and the rows already read stay.
    second.release({ ok: true, data: { document: lsPage(41, 12.5, [LS_ROW_A]) } })
    await flush()
    expect(classed(panelOf(document), 'coordinator-job')).toHaveLength(COORDINATOR_JOBS_PAGE + 1)
    expect(classed(panelOf(document), 'coordinator-jobs-older')).toEqual([])
    // The head is still the window's, and it was not rewritten from the second
    // page's own `totals`.
    expect(figureOfIn(panelOf(document), 'coordinator-jobs-window-runs')).toEqual([
      COORDINATOR_JOBS_RUNS_WORD,
      '41',
    ])
  })

  it('keeps the rows already drawn when the page after them is refused', async () => {
    // The rows are **appended, never redrawn**, and a refusal is no exception: the
    // page the operator asked for failed, and the page they were reading did not.
    // Taking the list down to say so would take their own reading of it -- the
    // rows they had expanded -- down with the sentence.
    const first = holding()
    const second = holding()
    const asked: Record<string, unknown>[] = []
    const { bridge } = jobsBridge({
      coordinatorStatus: () => ({ ok: true, data: { document: COORDINATOR_STATUS } }),
      planStatus: () => planStatusReply(QUEUE_PLAN),
      showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
      listRuns: (args) => {
        const page = args as Record<string, unknown>
        if (page['limit'] === undefined) return { ok: true, data: { document: { runs: [] } } }
        asked.push(page)
        return asked.length === 1 ? first.reply() : second.reply()
      },
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'coordinator').click()
    await flush()
    buttonLabelled(panelOf(document), COORDINATOR_JOBS_LOAD_WORD).click()
    first.release({ ok: true, data: { document: lsPage(41, 12.5, aFullPage()) } })
    await flush()
    expect(classed(panelOf(document), 'coordinator-job')).toHaveLength(COORDINATOR_JOBS_PAGE)

    buttonLabelled(panelOf(document), COORDINATOR_JOBS_OLDER_WORD).click()
    second.release({ ok: false, reason: RUNS_REFUSAL })
    await flush()

    // The rows the first page drew are still there, one refusal and no more is
    // drawn beside them, and the sentence is the CLI's own words for the read
    // that failed -- the section's refusal names the operation it made.
    expect(classed(panelOf(document), 'coordinator-job')).toHaveLength(COORDINATOR_JOBS_PAGE)
    const refusals = classed(panelOf(document), 'unavailable-operation')
    expect(refusals).toHaveLength(1)
    expect(refusals.map((node) => node.textContent)).toContain('runs.list')
    expect(holdsText(panelOf(document), RUNS_REFUSAL)).toBe(true)
    expect(busyLabels(document)).toEqual([])
    // No control offers the same failure again; the head's own refresh is the one
    // press left, and it reads the window from the top.
    expect(classed(panelOf(document), 'coordinator-jobs-load')).toEqual([])
    expect(classed(panelOf(document), 'coordinator-jobs-older')).toEqual([])
  })

  it('stamps the page it drew, and reads the window again from the top when the stamp’s Refresh is pressed', async () => {
    const first = holding()
    const again = holding()
    const asked: Record<string, unknown>[] = []
    const { bridge } = jobsBridge({
      coordinatorStatus: () => ({ ok: true, data: { document: COORDINATOR_STATUS } }),
      planStatus: () => planStatusReply(QUEUE_PLAN),
      showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
      listRuns: (args) => {
        const page = args as Record<string, unknown>
        if (page['limit'] === undefined) return { ok: true, data: { document: { runs: [] } } }
        asked.push(page)
        return asked.length === 1 ? first.reply() : again.reply()
      },
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'coordinator').click()
    await flush()

    // Nothing has been read of the window, so the head has nothing to stamp: the
    // line says when a page was read, and a section nothing answered draws
    // neither it nor a figure. The stamp is read inside the head and not across
    // the panel, because the transcript pane below it holds a clock of its own
    // (K5), which is that read's and not this one's.
    const jobsHead = classed(panelOf(document), COORDINATOR_JOBS_HEAD_CLASS)[0]
    if (jobsHead === undefined) throw new Error('the job list drew no head')
    expect(classed(jobsHead, STAMP_AT_CLASS)).toEqual([])

    buttonLabelled(panelOf(document), COORDINATOR_JOBS_LOAD_WORD).click()
    first.release({ ok: true, data: { document: lsPage(41, 12.5, aFullPage()) } })
    await flush()

    // The head says when the list on screen was read -- a page of a moving
    // `--since 24h` window is a snapshot, and the words are the console's.
    const head = classed(panelOf(document), COORDINATOR_JOBS_HEAD_CLASS)[0]
    if (head === undefined) throw new Error('the section drew no head')
    expect(textIn(head, STAMP_AT_CLASS)).toMatch(/^as of \d\d:\d\d$/)

    // Its own control reads the window **again from the top**: the pages drawn
    // and the cursor go with it, because a cursor belongs to the window those
    // rows came from, and the read that replaces it is the first page's.
    const refresh = classed(head, STAMP_REFRESH_CLASS)[0]
    if (refresh === undefined) throw new Error('the stamp drew no refresh control')
    refresh.click()
    expect(asked).toHaveLength(2)
    expect(asked[1]).toEqual({ since: RUNS_SINCE, limit: COORDINATOR_JOBS_PAGE })
    expect(classed(panelOf(document), 'coordinator-job')).toEqual([])

    again.release({ ok: true, data: { document: lsPage(41, 12.5, [LS_ROW_A]) } })
    await flush()
    expect(classed(panelOf(document), 'coordinator-job')).toHaveLength(1)
  })

  it('draws a refused job list as the CLI’s own sentence for runs ls, with no control beside it', async () => {
    const { bridge } = jobsBridge({
      coordinatorStatus: () => ({ ok: true, data: { document: COORDINATOR_STATUS } }),
      planStatus: () => planStatusReply(QUEUE_PLAN),
      showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
      listRuns: () => ({ ok: false, reason: RUNS_REFUSAL }),
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'coordinator').click()
    await flush()
    buttonLabelled(panelOf(document), COORDINATOR_JOBS_LOAD_WORD).click()
    await flush()
    // The refusal names its own command -- `runs ls`, which is the operation this
    // read is -- in the section that read owns, and the reads that answered are
    // not taken down for it.
    // The refusal names the operation this read is, which is the console's own
    // name for `runs ls` -- the same name the queue's runs part draws.
    const refusals = classed(panelOf(document), 'unavailable-operation')
    expect(refusals.map((node) => node.textContent)).toContain('runs.list')
    expect(holdsText(panelOf(document), RUNS_REFUSAL)).toBe(true)
    // Neither paging control is offered again -- `Load jobs` asked for a first
    // page and `Older 50` for a page under a window nothing landed -- and the
    // press the section holds instead is the stamp's own line in the head, which
    // is drawn with no `as of` beside it because no page was ever read (the case
    // below).
    expect(classed(panelOf(document), 'coordinator-jobs-load')).toEqual([])
    expect(classed(panelOf(document), 'coordinator-jobs-older')).toEqual([])
    expect(busyLabels(document)).toEqual([])
    // The queue's own read is a different one and its parts are still drawn.
    expect(classed(panelOf(document), COORDINATOR_QUEUE_PART_CLASS).length).toBeGreaterThan(0)
  })

  it('reads the window again from the head’s own line when the first page was refused', async () => {
    // K4's follow-up. A refused **first** page draws the sentence where the rows
    // would be, and the head's stamp is drawn with the figures of a page that
    // landed -- so with no page ever landed there is no head, and a section that
    // stopped there would hold the CLI's sentence and **no press at all**: the
    // operator's own doubt would have nowhere to go. The line drawn instead is
    // the stamp's, with its one control and no time, because the console has read
    // no page to put a clock on.
    const asked: Record<string, unknown>[] = []
    const retry = holding()
    const { bridge } = jobsBridge({
      coordinatorStatus: () => ({ ok: true, data: { document: COORDINATOR_STATUS } }),
      planStatus: () => planStatusReply(QUEUE_PLAN),
      showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
      listRuns: (args) => {
        const page = args as Record<string, unknown>
        if (page['limit'] === undefined) return { ok: true, data: { document: { runs: [] } } }
        asked.push(page)
        return asked.length === 1 ? { ok: false, reason: RUNS_REFUSAL } : retry.reply()
      },
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'coordinator').click()
    await flush()
    buttonLabelled(panelOf(document), COORDINATOR_JOBS_LOAD_WORD).click()
    await flush()

    const jobsHead = classed(panelOf(document), COORDINATOR_JOBS_HEAD_CLASS)[0]
    if (jobsHead === undefined) throw new Error('the job list drew no head')
    expect(classed(jobsHead, STAMP_AT_CLASS), 'a refused first page claimed a time').toEqual([])
    const refresh = classed(jobsHead, STAMP_REFRESH_CLASS)[0]
    if (refresh === undefined) throw new Error('the refused list drew no press at all')
    expect(refresh.textContent).toBe(STAMP_REFRESH_LABEL)

    // The press is the first page's own read and not a second kind of it: no
    // cursor, because no row was drawn to page from.
    refresh.click()
    expect(asked).toHaveLength(2)
    expect(asked[1]).toEqual({ since: RUNS_SINCE, limit: COORDINATOR_JOBS_PAGE })
    expect(busyLabels(document)).toContain(COORDINATOR_JOBS_BUSY)

    retry.release({ ok: true, data: { document: lsPage(41, 12.5, [LS_ROW_A]) } })
    await flush()
    // The refusal went with the list the press dropped, the page's rows are
    // drawn, and the head now carries the clock of the read that answered.
    expect(classed(panelOf(document), 'unavailable-operation')).toEqual([])
    expect(classed(panelOf(document), 'coordinator-job')).toHaveLength(1)
    expect(textIn(jobsHead, STAMP_AT_CLASS)).toMatch(/^as of \d\d:\d\d$/)
  })

  it('draws the console’s own sentence, naming the version, for a CLI older than the paged read', async () => {
    // `runs ls --limit` cut after the sort and `--after` are both #1545's, in
    // 0.16.0. Below the floor the console spawns nothing at all: a `--after` the
    // CLI refuses is a failure the operator cannot act on, and a `--limit` whose
    // `totals` are the page's would draw the page's count under the window's
    // word -- the one wrong number this section could draw.
    const { bridge, calls } = jobsBridge(
      {
        coordinatorStatus: () => ({ ok: true, data: { document: COORDINATOR_STATUS } }),
        planStatus: () => planStatusReply(QUEUE_PLAN),
        showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
      },
      '0.15.3',
    )
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'coordinator').click()
    await flush()
    const before = calls.length
    expect(textIn(panelOf(document), 'coordinator-jobs-floor-word')).toBe(COORDINATOR_JOBS_FLOOR_WORD)
    expect(textIn(panelOf(document), 'coordinator-jobs-floor-version')).toBe('0.15.3')
    // No control to press, and no read behind one: the sentence is the whole of
    // what this section can say.
    expect(classed(panelOf(document), 'coordinator-jobs-load')).toEqual([])
    expect(classed(panelOf(document), 'coordinator-job')).toEqual([])
    expect(calls.slice(before).filter((call) => call.name === 'listRuns')).toEqual([])

    // The version line is **main's own answer** and it is text wherever it came
    // from. `coordinator.test.ts` makes that claim about the note itself; this is
    // the handshake's string through the whole round trip -- the bridge's answer,
    // the entry's decode and the drawing -- so a version line carrying markup is
    // the characters on the screen and no element of the tag it names, and the
    // floor is read the same way for it as for a well-formed one.
    const hostile = jobsBridge(
      {
        coordinatorStatus: () => ({ ok: true, data: { document: COORDINATOR_STATUS } }),
        planStatus: () => planStatusReply(QUEUE_PLAN),
        showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
      },
      HOSTILE_CLI_VERSION,
    )
    const planted = await boot(hostile.bridge, new FakeDocument())
    buttonFor(planted, 'coordinator').click()
    await flush()
    const section = classed(panelOf(planted), COORDINATOR_JOBS_CLASS)[0]
    if (section === undefined) throw new Error('the job list drew no section')
    expect(textIn(section, 'coordinator-jobs-floor-version')).toBe(HOSTILE_CLI_VERSION)
    expect(
      everyNode(section).map((node) => node.tagName).filter((tag) => tag === 'IMG'),
      'the version line was drawn as an element',
    ).toEqual([])
    expect(classed(section, 'coordinator-jobs-load')).toEqual([])

    // At the floor the section is the one it is built to be: a control, a read on
    // the press, and the window's figures when it answers.
    const at = jobsBridge(
      {
        coordinatorStatus: () => ({ ok: true, data: { document: COORDINATOR_STATUS } }),
        planStatus: () => planStatusReply(QUEUE_PLAN),
        showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
        listRuns: () => ({ ok: true, data: { document: lsPage(41, 12.5, [LS_ROW_A]) } }),
      },
      '0.16.0',
    )
    const floored = await boot(at.bridge, new FakeDocument())
    buttonFor(floored, 'coordinator').click()
    await flush()
    expect(classed(panelOf(floored), 'coordinator-jobs-floor-word')).toEqual([])
    buttonLabelled(panelOf(floored), COORDINATOR_JOBS_LOAD_WORD).click()
    await flush()
    expect(figureOfIn(panelOf(floored), 'coordinator-jobs-window-runs')).toEqual([
      COORDINATOR_JOBS_RUNS_WORD,
      '41',
    ])
  })

  it('draws the console’s own sentence for a window a read answered with no run', async () => {
    const { bridge } = jobsBridge({
      coordinatorStatus: () => ({ ok: true, data: { document: COORDINATOR_STATUS } }),
      planStatus: () => planStatusReply(QUEUE_PLAN),
      showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
      listRuns: (args) =>
        (args as Record<string, unknown>)['limit'] === undefined
          ? { ok: true, data: { document: { runs: [] } } }
          : { ok: true, data: { document: { runs: [], totals: { runs: 0, total_usd: 0 } } } },
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'coordinator').click()
    await flush()
    buttonLabelled(panelOf(document), COORDINATOR_JOBS_LOAD_WORD).click()
    await flush()
    expect(holdsText(panelOf(document), COORDINATOR_JOBS_EMPTY_WORD)).toBe(true)
    expect(classed(panelOf(document), 'coordinator-jobs-older')).toEqual([])
  })

  it('opens the launch confirmation on a queue item’s own repository and plan task, and spawns nothing until the operator submits', async () => {
    // Retry and Launch lead to the console's **one** write (V2.5.1, rule 11):
    // the item's repository and plan task are filled into the form the operator
    // already confirms in the main process's own window, and this file sends
    // nothing until they press that form's submit. No run id is written here and
    // none is passed: main mints it when the launch is confirmed, and this file
    // has no argv to put one in -- the prefill is a value on a control, not a
    // call.
    const { bridge, calls } = queueBridge({
      coordinatorStatus: () => ({ ok: true, data: { document: COORDINATOR_STATUS } }),
      listRuns: () => ({ ok: true, data: { document: { runs: [QUEUE_RUN] } } }),
      planStatus: () => planStatusReply(QUEUE_PLAN),
      showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'coordinator').click()
    await flush()
    await askForJobs(document)

    // The job item's Retry, which is the queue's button for a run that ended
    // without succeeding.
    buttonLabelled(panelOf(document), COORDINATOR_RETRY_WORD).click()
    // Nothing is awaited: the Live screen the form lives on is drawn in the
    // click's own frame, and the item's two values are already in it.
    expect(
      valueOf(launchField(document, LAUNCH_REPOSITORY_CLASS)),
      'the form was not given the item’s repository',
    ).toBe(QUEUE_OFFER)
    expect(valueOf(launchField(document, LAUNCH_PLAN_TASK_CLASS))).toBe(QUEUE_RUN_PLAN_TASK)
    // The form is revealed, and the control that hides it says so: a form filled
    // out of sight would be a press that appears to do nothing.
    expect(launchField(document, LAUNCH_FORM_CLASS).attributes.get('hidden')).toBeUndefined()
    expect(launchField(document, LAUNCH_OPEN_CLASS).attributes.get('aria-expanded')).toBe('true')
    // Nothing is spawned by opening the form: this file's one write is the
    // form's own submit, and the operator has not pressed it.
    expect(calls.filter((call) => call.name === 'launchRun')).toEqual([])

    typeInto(document, LAUNCH_TASK_CLASS, 'try the nightly job again')
    launchField(document, LAUNCH_SUBMIT_CLASS).click()
    await flush()
    const launched = calls.filter((call) => call.name === 'launchRun').map((call) => call.args)
    expect(launched, 'the confirmation was asked for more than once').toHaveLength(1)
    // Rule 11's five fields and nothing else: no run id, no flag, no path. The
    // item's repository and plan task are *values* of those fields, and the key
    // set is the schema's.
    expect(launched[0]).toEqual({
      installation: 'acme',
      repository: QUEUE_OFFER,
      task: 'try the nightly job again',
      planTask: QUEUE_RUN_PLAN_TASK,
    })
  })

  it('opens the pull request a number names, with the triple the console formed and nothing a document wrote', async () => {
    // K2: every pull request number the Coordinator draws is a button, and a
    // press hands `link.openPullRequest` the three values the main process
    // builds the destination from -- the provider, the repository's `owner/name`
    // and the number. **The pull request's own `url` is not one of them**: the
    // document below carries a `javascript:` one, and the presses are the whole
    // of what this console may send.
    const { bridge, calls } = queueBridge({
      coordinatorStatus: () => ({ ok: true, data: { document: k2Status('github', 'javascript:alert(1)') } }),
      listRuns: () => ({ ok: true, data: { document: {} } }),
      planStatus: () => planStatusReply(QUEUE_PLAN),
      showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'coordinator').click()
    await flush()

    // The queue's own item: the pull request waiting for a person, whose one
    // button opens the run -- and whose number is a second button, this one
    // opening the pull request itself.
    const inQueue = pullButtons(panelOf(document), 'coordinator-queue-subject')
    expect(inQueue.map((node) => node.textContent), 'the queue’s pull request is not a button').toEqual(['#11'])
    expect(inQueue[0]?.attributes.get('aria-label')).toBe('open pull request #11 in owner/repo-a')
    inQueue[0]?.click()
    await flush()

    // And the record rows, drawn by the other half of the screen once its chunks
    // have run: `#7` is the draft no queue item stands for and `#11` the open
    // one, and both are numbers a person can open.
    await vi.advanceTimersByTimeAsync(10)
    const rows = pullButtons(panelOf(document), 'coordinator-record-label')
    expect(rows.map((node) => node.textContent), 'a record’s number is not a button').toEqual(['#7', '#11'])
    rows[1]?.click()
    await flush()

    const opened = calls.filter((call) => call.name === 'openPullRequest').map((call) => call.args)
    expect(opened, 'the two presses did not send the two triples').toEqual([
      { provider: 'github', repo: 'owner/repo-a', number: 11 },
      { provider: 'github', repo: 'owner/repo-a', number: 11 },
    ])
    // Nothing the document wrote is in what was sent: the case's own url is a
    // `javascript:` one, and no part of it reached the operation.
    expect(JSON.stringify(opened)).not.toContain('javascript')
  })

  it('clears the repository when two of the installation’s identities name the item’s repository', async () => {
    // The same repository can be recorded on two providers -- a provider is part
    // of the identity and not of the `owner/name` the documents carry -- and
    // there is then no one identity the item's repository is. The choice the
    // prefill may not make is the operator's earlier one: a Retry that kept the
    // GitHub identity standing would launch on a host nobody asked for, with
    // this item's plan task.
    const { bridge } = queueBridge(
      {
        coordinatorStatus: () => ({ ok: true, data: { document: COORDINATOR_STATUS } }),
        listRuns: () => ({ ok: true, data: { document: { runs: [QUEUE_RUN] } } }),
        planStatus: () => planStatusReply(QUEUE_PLAN),
        showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
      },
      ['github/owner/repo-a', 'bitbucket/owner/repo-a'],
    )
    const document = await boot(bridge, new FakeDocument())
    typeInto(document, LAUNCH_REPOSITORY_CLASS, 'github/owner/repo-a')
    buttonFor(document, 'coordinator').click()
    await flush()
    await askForJobs(document)

    buttonLabelled(panelOf(document), COORDINATOR_RETRY_WORD).click()
    expect(
      valueOf(launchField(document, LAUNCH_REPOSITORY_CLASS)),
      'the form kept a repository the item’s own document does not single out',
    ).toBe('')
    // The rest of the item is still handed over: it is the repository that has
    // no single answer, and the operator is left to choose one.
    expect(valueOf(launchField(document, LAUNCH_PLAN_TASK_CLASS))).toBe(QUEUE_RUN_PLAN_TASK)
  })

  it('clears the repository when the item names one the installation does not record', async () => {
    // A queue item's repository is not re-validated against the installation's
    // own list: the plan read is the one the session opened on, and the run list
    // is the installation's runs, but neither is a promise that this form has an
    // option for what the item carries. Nothing is written for a repository the
    // form does not offer -- and what the select held is cleared rather than
    // kept, for the reason above.
    const elsewhere: unknown = { ...(PLAN_STATUS as Record<string, unknown>), repo: 'someone-else/repo-z' }
    const { bridge } = queueBridge({
      coordinatorStatus: () => ({ ok: true, data: { document: COORDINATOR_STATUS } }),
      listRuns: () => ({ ok: true, data: { document: {} } }),
      planStatus: () => planStatusReply(elsewhere),
      showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
    })
    const document = await boot(bridge, new FakeDocument())
    typeInto(document, LAUNCH_REPOSITORY_CLASS, QUEUE_OFFER)
    buttonFor(document, 'coordinator').click()
    await flush()

    buttonLabelled(panelOf(document), COORDINATOR_LAUNCH_WORD).click()
    expect(
      valueOf(launchField(document, LAUNCH_REPOSITORY_CLASS)),
      'the form kept the repository of an item it has no option for',
    ).toBe('')
    expect(valueOf(launchField(document, LAUNCH_PLAN_TASK_CLASS))).toBe(QUEUE_PLAN_TASK)
  })

  it('reads an item’s repository as the installation’s own when the two differ only in ASCII case', async () => {
    // The prefill joins two spellings of one repository: the documents carry
    // `owner/name` and the form offers the installation's `provider/owner/name`,
    // and the fold they are compared under is the CLI's own -- `task.ASCIILower`,
    // behind `CanonicalRepo`, which folds A-Z and nothing else. A run that
    // recorded `Owner/Repo-A` therefore names the identity the installation
    // holds, and the form is filled rather than cleared.
    const { bridge } = queueBridge({
      coordinatorStatus: () => ({ ok: true, data: { document: COORDINATOR_STATUS } }),
      listRuns: () => ({ ok: true, data: { document: { runs: [{ ...QUEUE_RUN, repo: 'Owner/Repo-A' }] } } }),
      planStatus: () => planStatusReply(QUEUE_PLAN),
      showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'coordinator').click()
    await flush()
    await askForJobs(document)

    buttonLabelled(panelOf(document), COORDINATOR_RETRY_WORD).click()
    expect(
      valueOf(launchField(document, LAUNCH_REPOSITORY_CLASS)),
      'the same repository in another case was read as a repository the installation does not record',
    ).toBe(QUEUE_OFFER)
    expect(valueOf(launchField(document, LAUNCH_PLAN_TASK_CLASS))).toBe(QUEUE_RUN_PLAN_TASK)
  })

  it('does not fold a Kelvin sign to a `k`, which would launch on a repository nobody named', async () => {
    // U+212A KELVIN SIGN is `k` to `toLowerCase` and is **not** `k` to the CLI,
    // so an item naming `owner/Key` with a Kelvin sign names a repository the
    // installation does not record: the form is cleared, and the GitHub identity
    // a Unicode fold would have matched it to is left alone. The case is the
    // half of the fold above that a `toLowerCase` would pass while breaking.
    const { bridge } = queueBridge(
      {
        coordinatorStatus: () => ({ ok: true, data: { document: COORDINATOR_STATUS } }),
        // Spelled as an escape rather than as the character: U+212A is a `k` to
        // the eye as well as to `toLowerCase`, and a case about a fold that must
        // not happen is one whose input a reader has to be able to check.
        listRuns: () => ({ ok: true, data: { document: { runs: [{ ...QUEUE_RUN, repo: 'owner/\u212Aey' }] } } }),
        planStatus: () => planStatusReply(QUEUE_PLAN),
        showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
      },
      ['github/owner/key'],
    )
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'coordinator').click()
    await flush()
    await askForJobs(document)

    buttonLabelled(panelOf(document), COORDINATOR_RETRY_WORD).click()
    expect(
      valueOf(launchField(document, LAUNCH_REPOSITORY_CLASS)),
      'a Kelvin sign was folded to a `k`, and the form was given an identity the item does not name',
    ).toBe('')
  })

  it('answers a budget alert by opening Spend, and names the ground the alert stands on', async () => {
    // A BUDGET item's one button is a navigation (D812): the queue never calls
    // `budget kill` or `budget resume` -- those are the CLI's, behind a typed
    // confirmation -- so what the alert does is take the operator to the screen
    // that draws the fleet's money and its caps.
    const { bridge } = queueBridge({
      coordinatorStatus: () => ({ ok: true, data: { document: COORDINATOR_STATUS } }),
      listRuns: () => ({ ok: true, data: { document: {} } }),
      planStatus: () => planStatusReply(QUEUE_PLAN),
      // The committed budget document's one repository is killed, which is a
      // ground the operator can see and act on -- and one this screen does not
      // act on itself.
      showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'coordinator').click()
    await flush()
    expect(holdsText(panelOf(document), COORDINATOR_QUEUE_REPO_KILL_WORD)).toBe(true)

    buttonLabelled(panelOf(document), COORDINATOR_OPEN_SPEND_WORD).click()
    // The navigation is answered in the frame it happened in: Spend's button is
    // the current page before any read of that screen has answered.
    expect(
      buttonFor(document, 'spend').attributes.get('aria-current'),
      'the alert did not move the console to Spend',
    ).toBe('page')
    await flush()
    // And the screen behind it is Spend's own: this session has read the budget
    // it draws its caps from, and its report read is the refusal the fake bridge
    // answers with -- named by the screen that asked for it.
    expect(operationOf(document), 'the alert left the Coordinator’s screen on the panel').toBe(
      'spend.report',
    )
  })

  it('draws the Graph’s canvas while the run list never answers, and asks for that list only afterwards', async () => {
    // The canvas is the plan. `runs ls --json` is what a node's door is resolved
    // against -- four characters of a run id -- so it decorates a picture that is
    // already true without it, and the Graph is drawn, and readable, before the
    // list is even asked for.
    const runs = holding()
    const { bridge, calls } = graphBridge({
      planStatus: () => planStatusReply(),
      listRuns: () => runs.reply(),
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'graph').click()
    await flush()
    expect(
      classed(panelOf(document), 'graph-canvas').length,
      'the canvas waited for the run list',
    ).toBeGreaterThan(0)
    expect(
      classed(panelOf(document), 'graph-node').length,
      'the canvas was drawn from the plan alone',
    ).toBeGreaterThan(0)
    const asked = calls.filter((call) => call.name === 'listRuns')
    expect(asked.length, 'the Graph never asked for the run list at all').toBeGreaterThan(0)

    // And the list never answering changes nothing about what is on screen: no
    // mark, no refusal, no empty panel.
    expect(busyLabels(document)).toEqual([])
    expect(operationOf(document)).toBe(null)
  })

  it('reads the run list for a day, on every read it makes of it', async () => {
    // The window is the rule, not a detail: a console that read the whole run
    // log would pay for the reads of every installation the owner has ever run.
    const { bridge, calls } = fakeBridge({
      replies: { subscribeLive: () => snapshotReply(liveFrame()), listRuns: () => runsReply() },
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'graph').click()
    await flush()
    await flush()
    const asked = calls.filter((call) => call.name === 'listRuns')
    expect(asked.length, 'no run list was read: the case proves nothing').toBeGreaterThan(0)
    for (const call of asked) {
      expect((call.args as { readonly since?: unknown }).since, 'a run list was read for all time').toBe(
        RUNS_SINCE,
      )
    }
  })

  it('fills the Live rows’ mode column when the join lands, without the frame ever waiting for it', async () => {
    // The mode on a Live row is not in the live document: it is joined from
    // `runs ls --json` by run id (`shared/rigor.ts`). The rows are drawn without
    // it and the column fills in the moment it lands -- a join that never
    // answered would leave the frame exactly as it is, which is a screen about
    // what is running now rather than a screen holding a read.
    const join = holding()
    const { bridge } = fakeBridge({
      replies: { subscribeLive: () => snapshotReply(liveFrame()), listRuns: () => join.reply() },
    })
    const document = await boot(bridge, new FakeDocument())
    expect(
      classed(panelOf(document), 'live-run').length,
      'the frame drew no rows: the case proves nothing',
    ).toBeGreaterThan(0)
    expect(classed(panelOf(document), 'live-run-mode')).toEqual([])

    join.release({
      ok: true,
      data: {
        document: {
          project: 'example-project',
          runs: [{ run: '20261015-120000-aa01', rigor: { mode: 'pro', from: 'repo' } }],
          warnings: [],
        },
      },
    })
    await flush()
    expect(
      classed(panelOf(document), 'live-run-mode').map((node) => node.textContent),
      'the join landed and the mode column stayed empty',
    ).toEqual(['Pro'])
  })

  it('keeps the stream filter the operator pressed last when the first press draws after it', async () => {
    // Two presses in one turn -- a click and a mis-click, or the arrow key after
    // the mouse -- and each of them is a layout the engine answers on its own
    // time. The drawing that answers the press the operator has already replaced
    // is `null` and must not be drawn: the tab that stays is the one they chose
    // last, whatever order the two answers arrive in.
    const { bridge } = graphBridge()
    const document = await openGraph(bridge)
    const tabs = classed(panelOf(document), 'graph-tab')
    expect(tabs.map((tab) => tab.attributes.get('data-tab'))).toEqual(['stream:example', 'stream:', 'all'])
    ;(tabs[0] as FakeNode).click()
    ;(tabs[1] as FakeNode).click()
    await flush()
    expect(
      classed(panelOf(document), 'graph-tab').map((tab) => tab.attributes.get('aria-pressed')),
      'the stream the operator pressed first is the one that is showing',
    ).toEqual(['false', 'true', 'false'])
  })

  it('never draws the run list a read the operator left over the tab they moved to', async () => {
    // The decoration is the read that arrives after the one the operator is
    // waiting for, and it has no mark of its own: it is the **sequence check** on
    // the Graph that keeps it off a tab nobody is on. **The mutation this case is
    // proven against is that check -- the two `stale(mine)` guards
    // `decorateRuns` stands on, the one after the read and the one after the
    // drawing -- and dropping them fails this case**: the older picture is drawn
    // over the Live tab the operator moved to. Each guard alone survives the
    // mutation, which is why the case names the pair.
    const lists: ReturnType<typeof holding>[] = []
    const { bridge } = graphBridge({
      subscribeLive: () => snapshotReply(liveFrame()),
      listRuns: () => {
        const answer = holding()
        lists.push(answer)
        return answer.reply()
      },
    })
    const document = await boot(bridge, new FakeDocument())
    expect(lists, 'the Live rigor join is not in flight: the case proves nothing').toHaveLength(1)
    buttonFor(document, 'graph').click()
    await flush()
    expect(classed(panelOf(document), 'graph-canvas').length).toBeGreaterThan(0)

    buttonFor(document, 'live').click()
    await flush()
    const chosen = panelOf(document).children[0] as FakeNode
    expect(chosen.attributes.get('class'), 'the operator never left the Graph: the case proves nothing').toBe(
      'live',
    )

    lists[1]?.release(runsReply())
    await flush()
    expect(panelOf(document).children[0], 'a run list nobody waited for replaced the view on screen').toBe(
      chosen,
    )
    expect(classed(panelOf(document), 'graph-canvas')).toEqual([])
  })

  it('never lets a read the operator has replaced draw over the picture they kept', async () => {
    // Two quick presses of the Graph's own Refresh, and the read the first one
    // started answers last: it is not the newest read's and must not be drawn.
    // This is the Graph's half of "the latest change's view is the one that
    // stays, whatever order the answers arrive in", and it is the second press's
    // mark that is on screen while the earlier answer lands under it -- a
    // refusal drawn there would take the canvas down for a read nobody asked
    // for. Two guards stand between the two (the section's token and the read's
    // generation), and either one alone is enough: the case is the behaviour,
    // and `decorateRuns`' case below is the one that is proven against a
    // mutation.
    const plans = readQueue()
    const { bridge } = graphBridge({
      planStatus: plans.reply,
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'graph').click()
    await flush()
    plans.release(0, planStatusReply())
    await flush()
    expect(classed(panelOf(document), 'graph-canvas').length).toBeGreaterThan(0)

    const refresh = classed(panelOf(document), 'graph-refresh')[0]
    expect(refresh, 'the Graph drew no Refresh control: the case proves nothing').toBeDefined()
    refresh?.click()
    refresh?.click()
    // The first press's read answers with a refusal, and it is nobody's read any
    // more: the picture stays, the mark stays up for the read the operator asked
    // for last, and no refusal screen is drawn.
    plans.release(1, { ok: false, reason: GRAPH_REFUSAL })
    await flush()
    expect(operationOf(document), 'a read the operator had replaced drew its refusal').toBe(null)
    expect(holdsText(panelOf(document), GRAPH_REFUSAL)).toBe(false)
    expect(
      classed(panelOf(document), 'graph-canvas').length,
      'a read the operator had replaced took the canvas down',
    ).toBeGreaterThan(0)
    expect(busyLabels(document)).toEqual([GRAPH_BUSY])

    // The read the operator asked for last is drawn when it lands.
    plans.release(2, planStatusReply())
    await flush()
    expect(busyLabels(document)).toEqual([])
    expect(classed(panelOf(document), 'graph-canvas').length).toBeGreaterThan(0)
  })
})

describe('the Graph’s no-checkout state', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  /** noCheckoutBridge is `graphBridge` with the plan read refused for want of a
   * checkout -- the one sentence the card is drawn for -- and the rail's own
   * `installation.open` recorded, so a case can hold the card's button to the
   * door the rail's `+` opens. */
  function noCheckoutBridge(): FakeBridge & {
    readonly rail: readonly { readonly name: string; readonly args: unknown }[]
  } {
    const base = graphBridge({ planStatus: () => ({ ok: false, reason: NO_PLAN_CHECKOUT }) })
    const rail: { name: string; args: unknown }[] = []
    base.bridge['openInstallations'] = (args: unknown): unknown => {
      rail.push({ name: 'openInstallations', args })
      return { ok: true, data: null }
    }
    return { ...base, rail }
  }

  it('draws the calm card, keeps the CLI’s sentence behind a disclosure, and opens the picker', async () => {
    const { bridge, rail } = noCheckoutBridge()
    const document = await openGraph(bridge)
    const card = panelOf(document).children[0] as FakeNode
    expect(card.attributes.get('class')).toBe('console-unavailable')
    expect(card.attributes.get('data-kind')).toBe(NO_CHECKOUT_KIND)
    // The tone is the kind's: `refused` is the kind the theme paints with the
    // warn rule, and a state with one action is not a failure of the read.
    expect(NO_CHECKOUT_KIND).not.toBe('refused')
    expect(operationOf(document), 'the card is not a refusal screen').toBe(null)
    expect(textOf(classed(card, 'unavailable-headline')[0] as FakeNode)).toBe(NO_CHECKOUT_HEADLINE)

    const details = classed(card, 'installation-issue-details')[0] as FakeNode
    expect(details.tagName).toBe('DETAILS')
    expect(classed(details, 'installation-issue-summary')[0]?.textContent).toBe('details')
    expect(holdsText(details, NO_PLAN_CHECKOUT), 'the CLI’s own sentence is not in the disclosure').toBe(true)
    expect(holdsText(classed(card, 'unavailable-body')[0] as FakeNode, NO_PLAN_CHECKOUT)).toBe(true)

    buttonLabelled(card, NO_CHECKOUT_CHOICE_LABEL).click()
    expect(rail.at(-1), 'the card’s button does not open the picker').toEqual({
      name: 'openInstallations',
      args: {},
    })
  })

  /** checkoutBridge is a console whose rail read answered with one row and
   * whose plan read refused for want of a checkout until a checkout is
   * recorded: the two facts the card's action depends on. `choices` is the menu
   * the main process built for that row, which is what the card's control is
   * drawn from and what the write is matched against. */
  function checkoutBridge(choices: readonly Record<string, unknown>[]): FakeBridge & {
    readonly rail: readonly { readonly name: string; readonly args: unknown }[]
    readonly release: (value: unknown) => void
  } {
    vi.stubGlobal('Worker', FakeWorker)
    const write = holding()
    let recorded = false
    const base = railBridge([listRow('acme', { open: true, checkoutChoices: choices })], {
      setInstallationCheckout: () => {
        recorded = true
        return write.reply()
      },
    })
    // The write is **held**, so a case can look at the card the console drew
    // before the answer lands and at the plan read that follows it: the two are
    // the whole of what this card does.
    base.bridge['planStatus'] = () => (recorded ? planStatusReply() : { ok: false, reason: NO_PLAN_CHECKOUT })
    return { ...base, release: (value) => write.release(value) }
  }

  it('says what to do instead of drawing an action for an installation that lists no checkout', async () => {
    const { bridge, rail } = checkoutBridge([])
    const document = await openGraph(bridge)
    const card = panelOf(document).children[0] as FakeNode
    expect(card.attributes.get('data-kind')).toBe(NO_CHECKOUT_KIND)
    // The action is a menu drawn from the CLI's own list, so for a row with
    // nothing in that list the card offers no action and says what records one.
    expect(holdsText(card, NO_CHECKOUT_EMPTY_SENTENCE)).toBe(true)
    expect(
      everyNode(card).some(
        (node) =>
          (node.tagName === 'BUTTON' && node.textContent === NO_CHECKOUT_USE_LABEL) ||
          node.attributes.get('class') === CHECKOUT_SELECT_CLASS,
      ),
      'the card drew an action that leads nowhere',
    ).toBe(false)
    expect(rail.map((call) => call.name), 'the card wrote a checkout for a row it has no action for').toEqual([
      'listInstallations',
    ])
  })

  it('sends the directory the menu has selected, and draws the plan it then reads', async () => {
    const { bridge, rail, release } = checkoutBridge([
      { repository: 'github/owner/acme', path: '/home/operator/src/acme', more: 0 },
      { repository: 'github/owner/acme', path: '/home/operator/src/scratch', more: 0 },
    ])
    const document = await openGraph(bridge)
    const card = panelOf(document).children[0] as FakeNode
    expect(holdsText(card, NO_CHECKOUT_EMPTY_SENTENCE)).toBe(false)
    const menu = classed(card, CHECKOUT_SELECT_CLASS)[0] as FakeNode
    expect(menu.children.map((option) => option.attributes.get('value'))).toEqual([
      '/home/operator/src/acme',
      '/home/operator/src/scratch',
    ])
    ;(menu as unknown as { value: string }).value = '/home/operator/src/scratch'
    buttonLabelled(card, NO_CHECKOUT_USE_LABEL).click()
    await flush()
    expect(rail.at(-1), 'the menu sent a directory it was not offering').toEqual({
      name: 'setInstallationCheckout',
      args: { project: 'acme', checkout: '/home/operator/src/scratch' },
    })
    // The write is not the answer: the plan read after it is, and it is what
    // replaces the card with the picture -- in the frame the operator is in,
    // never by walking off to the issue view the rail's own actions leave for.
    expect(panelOf(document).children[0]?.attributes.get('data-kind')).toBe(NO_CHECKOUT_KIND)
    release({ ok: true, data: null })
    await flush()
    expect(classed(panelOf(document), 'graph-canvas').length, 'the card did not give way to the plan').toBeGreaterThan(0)
  })

  it('chooses the installation’s one checkout by itself, says so once, and draws the plan', async () => {
    const { bridge, rail, release } = checkoutBridge([
      { repository: 'github/owner/acme', path: '/home/operator/src/acme', more: 0 },
    ])
    const document = await openGraph(bridge)
    const card = panelOf(document).children[0] as FakeNode
    // A menu of one entry is a control with nothing to choose, so the console
    // presses it: the card says which directory the Graph is reading, in the
    // entry's own words for it, and draws no control at all.
    expect(holdsText(card, `${NO_CHECKOUT_AUTO_SENTENCE} github/owner/acme — …/src/acme`)).toBe(true)
    expect(classed(card, CHECKOUT_SELECT_CLASS)).toEqual([])
    expect(everyNode(card).some((node) => node.tagName === 'BUTTON' && node.textContent === NO_CHECKOUT_USE_LABEL)).toBe(
      false,
    )
    expect(rail.at(-1), 'the card did not choose the one checkout it was offered').toEqual({
      name: 'setInstallationCheckout',
      args: { project: 'acme', checkout: '/home/operator/src/acme' },
    })
    release({ ok: true, data: null })
    await flush()
    expect(classed(panelOf(document), 'graph-canvas').length).toBeGreaterThan(0)
    // Once: the same directory is never sent twice for one installation, so a
    // card drawn again for it -- a cadence read, another visit -- offers the
    // menu rather than claiming a read it is not making.
    expect(rail.filter((call) => call.name === 'setInstallationCheckout')).toHaveLength(1)
  })

  it('draws the refusal screen for every other sentence the plan read refuses with', async () => {
    const { bridge } = graphBridge({ planStatus: () => ({ ok: false, reason: GRAPH_REFUSAL }) })
    const document = await openGraph(bridge)
    const drawn = panelOf(document).children[0] as FakeNode
    expect(drawn.attributes.get('data-kind')).toBe('refused')
    expect(operationOf(document)).toBe('plan.status')
    expect(holdsText(drawn, GRAPH_REFUSAL)).toBe(true)
    expect(classed(drawn, 'installation-issue-details')).toEqual([])
  })
})

// ---------------------------------------------------------------------------
// The Graph's scope (the graph plan's CG10)
//
// **The Graph is one tab with two scopes, and this is where the two reads behind
// it are held.** The plan scope is `plan.status` (the checkout's own document),
// the coordinator scope is `graph.read` (`fugaro graph --json`, contract §6.22),
// and the scope that needs no checkout is the second (H9): an installation that
// records none is read there rather than shown a card, and the card means only
// that the **plan** scope needs one.
//
// Four of the five cases here are the owner's rule of 2026-10-31 for a control
// that changes the drawing: the change is answered **in the frame it happened
// in** (a mark up, the figures kept under it), the answer it gets is the answer
// to the *latest* change (the section's sequence token), and a refusal names its
// own read and leaves the operator a way out.
// ---------------------------------------------------------------------------

/** GRAPH_DOCUMENT is one `graph --json` document as the wire has it (§6.22):
 * two sources -- the coordinator, up, and an orchestrator that is down with its
 * reason -- one pull request, **no edges**, and the read's own warning. The
 * console decodes it with the contract's own decoder, so a case here drives the
 * document a real read answers with. Its one id is the shape the coordinator
 * writes (`<repo>#<number>`), because the row it draws is the source's own id. */
function graphDocument(): Record<string, unknown> {
  return {
    version: GRAPH_DOCUMENT_VERSION,
    stream_id: '',
    seq: 0,
    generated_at: '2026-11-07T10:30:00Z',
    sources: [
      { source: 'coordinator', state: 'up', as_of: null, reason: '' },
      { source: 'orchestrator:o1', state: 'down', as_of: '2026-11-07T09:00:00Z', reason: 'the stream was withdrawn' },
    ],
    nodes: [
      {
        key: 'coordinator:acme/widgets#42',
        source: 'coordinator',
        kind: 'pull_request',
        id: 'acme/widgets#42',
        title: 'Tidy the widget loader',
        repo: 'acme/widgets',
        state: 'open',
        release: 'v1.4.0',
        updated: '2026-11-06T18:00:00Z',
      },
    ],
    edges: [],
    warnings: ['the coordinator view holds no edges'],
  }
}

/** graphReply is one `graph.read` answer: the envelope `CliBackedHandlers`
 * builds, with the document inside it. */
function graphReply(document: Record<string, unknown> = graphDocument()): Record<string, unknown> {
  return {
    ok: true,
    data: {
      document,
      receivedAt: '2026-11-07T10:30:00Z',
      sequence: 0,
      day: '2026-11-07',
      rolled: false,
      notices: [],
    },
  }
}

/** heldScopeAnswers is a `graph.read` whose every answer the case releases when
 * it likes, by the order it was asked in: the case that looks at a scope change
 * needs the moment **before** the answer, and the one that looks at the sequence
 * token needs two answers to land out of order. */
function heldScopeAnswers(): {
  readonly reply: () => Promise<unknown>
  readonly asked: () => number
  readonly release: (at: number) => void
} {
  const waiters: ((value: unknown) => void)[] = []
  return {
    reply: () =>
      new Promise((resolve) => {
        waiters.push(resolve)
      }),
    asked: () => waiters.length,
    release: (at: number) => {
      const waiter = waiters[at]
      if (waiter === undefined) throw new Error(`no graph read number ${String(at)} is outstanding`)
      waiter(graphReply())
    },
  }
}

/** scopeControl is the drawn scope control. */
function scopeControl(document: FakeDocument): FakeNode {
  const control = classed(panelOf(document), 'graph-scope-select')[0]
  if (control === undefined) throw new Error('the Graph drew no scope control')
  return control
}

/** scopeChosen is the scope the control is showing. */
function scopeChosen(document: FakeDocument): string | null {
  return scopeControl(document).attributes.get('data-scope') ?? null
}

/** chooseScope is the operator's own choice of a scope: the value the browser
 * puts on the control, and the change it reports for it. */
async function chooseScope(document: FakeDocument, scope: string): Promise<void> {
  const control = scopeControl(document)
  control.value = scope
  control.change()
  await flush()
}

/** GRAPH_SCOPE_REFUSAL is the sentence a `graph.read` is refused with by a CLI
 * built without the graph command (T3, the core stub). It is written here rather
 * than taken from a module: what the console does with a refusal is the same
 * whatever the CLI's words are, and this file drives the sentence it is given. */
const GRAPH_SCOPE_REFUSAL = 'this installation’s CLI has no graph command'

describe('the Graph’s scope', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('no checkout opens the coordinator scope', async () => {
    const { bridge, calls } = graphBridge({
      planStatus: () => ({ ok: false, reason: NO_PLAN_CHECKOUT }),
      graphRead: () => graphReply(),
    })
    const document = await openGraph(bridge)
    // The plan scope is the read the console opens with, and its refusal for an
    // installation that records no checkout is no longer a card: the Graph opens
    // on the scope that needs none, which is the read H9 exists for.
    expect(calls.filter((call) => call.name === 'graphRead')).toEqual([
      { name: 'graphRead', args: { source: GRAPH_SCOPE_ALL } },
    ])
    expect(scopeChosen(document)).toBe(GRAPH_SCOPE_ALL)
    expect(classed(panelOf(document), 'graph-scope-node').map((each) => each.attributes.get('data-key'))).toEqual([
      'coordinator:acme/widgets#42',
    ])
    // Neither the card nor the plan is drawn under it: nothing was refused, and
    // the scope that answers is the scope that is shown.
    expect(classed(panelOf(document), 'console-unavailable')).toEqual([])
    expect(classed(panelOf(document), 'graph-node')).toEqual([])
  })

  /** ONE_CHECKOUT is the one checkout an installation with a single repository
   * lists: the card's `auto` state, and the directory that state sends. */
  const ONE_CHECKOUT = { repository: 'github/owner/acme', path: '/home/operator/src/acme', more: 0 }

  it('still chooses an installation’s one checkout by itself, over the fallback', async () => {
    // **H9 does not take the plan scope's own way out away.** The fallback
    // answers a card the operator could only be sent somewhere else from; an
    // installation the CLI listed exactly one checkout for is one the console
    // records and then reads the plan from, so **a coordinator read that works
    // does not replace that card** -- which is what this case has and the four
    // card cases above do not: a console that fell back here would open on the
    // coordinator scope with the checkout still unchosen and the plan still
    // unreadable.
    const write = holding()
    let recorded = false
    let scopes = 0
    vi.stubGlobal('Worker', FakeWorker)
    const { bridge, rail } = railBridge([listRow('acme', { open: true, checkoutChoices: [ONE_CHECKOUT] })], {
      setInstallationCheckout: () => {
        recorded = true
        return write.reply()
      },
    })
    bridge['planStatus'] = () => (recorded ? planStatusReply() : { ok: false, reason: NO_PLAN_CHECKOUT })
    bridge['graphRead'] = () => {
      scopes += 1
      return graphReply()
    }
    const document = await openGraph(bridge)
    expect(scopes, 'the fallback opened the coordinator scope over the card’s own choice').toBe(0)
    const card = panelOf(document).children[0] as FakeNode
    expect(card.attributes.get('data-kind')).toBe(NO_CHECKOUT_KIND)
    expect(holdsText(card, `${NO_CHECKOUT_AUTO_SENTENCE} ${checkoutLabel(ONE_CHECKOUT)}`)).toBe(true)
    expect(rail.at(-1), 'the card did not choose the one checkout it was offered').toEqual({
      name: 'setInstallationCheckout',
      args: { project: 'acme', checkout: ONE_CHECKOUT.path },
    })
    // And the write is still not the answer: the plan read after it is, and it
    // draws the picture the card was standing in for.
    write.release({ ok: true, data: null })
    await flush()
    expect(
      classed(panelOf(document), 'graph-canvas').length,
      'the chosen checkout did not give way to the plan',
    ).toBeGreaterThan(0)
    expect(scopes, 'the coordinator scope was read after the plan answered').toBe(0)
  })

  it('the plan scope with no checkout still shows its card, and the way back to the coordinator', async () => {
    const { bridge } = graphBridge({
      planStatus: () => ({ ok: false, reason: NO_PLAN_CHECKOUT }),
      graphRead: () => graphReply(),
    })
    const document = await openGraph(bridge)
    // The operator asks for the plan scope of an installation that records no
    // checkout: the card is what that scope is, and it is drawn for a choice
    // somebody made rather than for the scope the Graph opens on.
    await chooseScope(document, GRAPH_SCOPE_PLAN)
    const card = classed(panelOf(document), 'console-unavailable')[0]
    expect(card?.attributes.get('data-kind')).toBe(NO_CHECKOUT_KIND)
    expect(holdsText(card as FakeNode, NO_CHECKOUT_HEADLINE)).toBe(true)
    // **And the way back.** The card is the plan scope's state and holds no scope
    // control, so without this door an operator whose installation has no
    // checkout would be on a screen they cannot leave.
    const back = classed(card as FakeNode, NO_CHECKOUT_SCOPE_CLASS)[0]
    expect(back?.textContent).toBe(NO_CHECKOUT_SCOPE_LABEL)
    back?.click()
    await flush()
    expect(scopeChosen(document)).toBe(GRAPH_SCOPE_ALL)
    expect(classed(panelOf(document), 'graph-scope-node')).toHaveLength(1)
    expect(classed(panelOf(document), 'console-unavailable')).toEqual([])
  })

  it('a scope change is answered in its own frame (busy mark, figures kept)', async () => {
    const answers = heldScopeAnswers()
    const { bridge } = graphBridge({ graphRead: () => answers.reply() })
    const document = await openGraph(bridge)
    const before = classed(panelOf(document), 'graph-node').length
    expect(before).toBeGreaterThan(0)

    await chooseScope(document, GRAPH_SCOPE_ALL)
    // **The frame the control was used in**: the section is busy and says what it
    // is reading, and the figures the last read drew are still on the panel under
    // the mark -- a switch that blanked the Graph would take the picture away to
    // say that another one is coming.
    const section = classed(panelOf(document), 'graph')[0]
    expect(section?.attributes.get(BUSY_BUSY_ATTRIBUTE)).toBe('true')
    expect(classed(panelOf(document), BUSY_LABEL_CLASS).map((each) => each.textContent)).toEqual([GRAPH_SCOPE_BUSY])
    expect(classed(panelOf(document), 'graph-node')).toHaveLength(before)

    // And the answer replaces the figures with the scope's own, and takes the
    // mark down with it.
    answers.release(0)
    await flush()
    expect(classed(panelOf(document), 'graph-scope-node')).toHaveLength(1)
    expect(classed(panelOf(document), 'graph-node')).toEqual([])
    expect(classed(panelOf(document), BUSY_LABEL_CLASS)).toEqual([])
    expect(scopeChosen(document)).toBe(GRAPH_SCOPE_ALL)
  })

  it('a late answer for a changed scope is dropped (sequence token)', async () => {
    const answers = heldScopeAnswers()
    const { bridge } = graphBridge({ graphRead: () => answers.reply() })
    const document = await openGraph(bridge)
    // The coordinator scope is asked for, and its answer is held.
    await chooseScope(document, GRAPH_SCOPE_ALL)
    expect(answers.asked()).toBe(1)
    // The operator changes their mind while that read is out: the plan scope is
    // asked for, and the read is held as one read to make when the outstanding
    // one settles.
    await chooseScope(document, GRAPH_SCOPE_PLAN)
    // The answer for the scope nobody is looking at any more arrives ...
    answers.release(0)
    await flush()
    // ... and draws nothing: a slower read must never replace a later one, and the
    // panel shows the scope the operator asked for last.
    expect(classed(panelOf(document), 'graph-scope-node')).toEqual([])
    expect(classed(panelOf(document), 'graph-node').length).toBeGreaterThan(0)
    expect(scopeChosen(document)).toBe(GRAPH_SCOPE_PLAN)
  })

  it('a CLI without graph shows the plan scope and a refusal sentence (T3)', async () => {
    const { bridge } = graphBridge({
      planStatus: () => planStatusReply(),
      graphRead: () => ({ ok: false, reason: GRAPH_SCOPE_REFUSAL }),
    })
    const document = await openGraph(bridge)
    await chooseScope(document, GRAPH_SCOPE_ALL)
    // **T3: the core stub keeps the plan scope.** The read the CLI has no command
    // for is refused, and the console goes back to the scope it can read and says
    // which read refused -- in the drawing, under the control, rather than on a
    // refusal screen that would take the way back away with it.
    expect(scopeChosen(document)).toBe(GRAPH_SCOPE_PLAN)
    expect(classed(panelOf(document), 'graph-scope-notice').map((each) => each.textContent)).toEqual([
      GRAPH_SCOPE_REFUSAL,
    ])
    expect(operationOf(document), 'the refused scope is not a refusal screen').toBe(null)
    expect(classed(panelOf(document), 'graph-node').length).toBeGreaterThan(0)
    // Not vacuous: the plan scope is the scope the console read, and the sentence
    // is about the read it could not make.
    expect(holdsText(panelOf(document), GRAPH_SCOPE_REFUSAL)).toBe(true)
  })

  it('the card’s own door is a read, and a refusal of it is the refusal screen', async () => {
    // An installation that records no checkout **and** a CLI with no `graph`
    // command: the console's own fallback finds nothing to draw either, so the
    // card is what is left -- and its door is the one control that reads the
    // scope the installation can have.
    const { bridge } = graphBridge({
      planStatus: () => ({ ok: false, reason: NO_PLAN_CHECKOUT }),
      graphRead: () => ({ ok: false, reason: GRAPH_SCOPE_REFUSAL }),
    })
    const document = await openGraph(bridge)
    const card = classed(panelOf(document), 'console-unavailable')[0]
    expect(card?.attributes.get('data-kind')).toBe(NO_CHECKOUT_KIND)
    classed(card as FakeNode, NO_CHECKOUT_SCOPE_CLASS)[0]?.click()
    await flush()
    // **The door is the operator's own choice, and the answer to it is that
    // read's.** A refusal here names the command and is a screen of its own: the
    // card's sentence is the *plan* read's, and drawing the card back over the
    // door's own refusal would be a control that answers with the screen it is
    // on -- which is what the console's own fallback flag did when it outlived
    // the fallback (`chooseScope` clears it).
    expect(operationOf(document), 'the door’s refusal is a screen').toBe('graph.read')
    expect(holdsText(panelOf(document), GRAPH_SCOPE_REFUSAL)).toBe(true)
    // The card's own kind is gone with it: this frame is the refusal's, not the
    // console's fallback drawn a second time.
    expect(classed(panelOf(document), 'console-unavailable')[0]?.attributes.get('data-kind')).not.toBe(
      NO_CHECKOUT_KIND,
    )
  })

  it('a control value this build does not know is the plan scope, not an invented one', async () => {
    const { bridge, calls } = graphBridge({ graphRead: () => graphReply() })
    const document = await openGraph(bridge)
    // The control's value is read back through `valueOf`, and a value this build
    // has no scope for -- a menu item a newer CLI draws, a rewritten DOM -- is the
    // plan scope: the two keys this console writes are the only scopes it reads,
    // and a key it does not know is not a read it will make up.
    await chooseScope(document, 'orchestrator:o1')
    expect(calls.filter((call) => call.name === 'graphRead')).toEqual([])
    expect(scopeChosen(document)).toBe(GRAPH_SCOPE_PLAN)
    expect(classed(panelOf(document), 'graph-node').length).toBeGreaterThan(0)
  })
})
