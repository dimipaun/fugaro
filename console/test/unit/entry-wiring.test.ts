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
import { NEW_OPS_NOT_AVAILABLE, NEW_OPS_UNREADABLE } from '../../src/shared/new-ops'
import type { SettingsEditorOpenRequest, SettingsEditorOutcome } from '../../src/shared/new-ops'
import { GRAPH_DOCUMENT_VERSION, decodeSnapshot } from '../../src/shared/schema'
import { UNAVAILABLE_MARK } from '../../src/shared/availability'
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
import { LIVE_CAPS_HIDDEN_WORD } from '../../src/renderer/screens/live'
import {
  GRAPH_ESTIMATE_COST_HIDDEN_WORD,
  GRAPH_ESTIMATE_MINUTES,
  GRAPH_ESTIMATE_READY,
  GRAPH_SCOPE_ALL,
  GRAPH_SCOPE_PLAN,
} from '../../src/renderer/screens/graph'
import {
  BUSY_ATTRIBUTE,
  BUSY_BUSY_ATTRIBUTE,
  BUSY_CLASS,
  BUSY_LABEL_CLASS,
  BUSY_SPINNER_CLASS,
  BUSY_STILL_CLASS,
  CACHE_FRESH_MS,
  RETRY_CLASS,
  RETRY_LABEL,
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
  COORDINATOR_ATTRIBUTE,
  COORDINATOR_BUSY,
  COORDINATOR_FLOOR_AND_WORD,
  COORDINATOR_FLOOR_WORD,
  COORDINATOR_HEALTH_CLASS,
  COORDINATOR_NODE_CLASS,
  COORDINATOR_SOURCE_CLASS,
  COORDINATOR_SOURCE_LINE,
  COORDINATOR_TASKS_BUSY,
  COORDINATOR_TASKS_CLASS,
  COORDINATOR_TASKS_EMPTY_CLASS,
  COORDINATOR_TASKS_EMPTY_WORD,
  COORDINATOR_TASKS_NOT_UP_WORD,
  COORDINATOR_TITLE,
  COORDINATOR_TITLE_CLASS,
} from '../../src/renderer/screens/coordinator'
import { shortRun } from '../../src/shared/shortid'
import {
  PROJECTS_ACCOUNT_KEYS_CLASS,
  PROJECTS_BUSY,
  PROJECTS_CREDENTIALS_SERVICE_CLASS,
  PROJECTS_LICENCE_CLASS,
  PROJECTS_MANAGED_CLASS,
  PROJECTS_MODELS_BUSY,
  PROJECTS_RETIREMENTS_CLASS,
  PROJECTS_SEATS_CLASS,
  PROJECTS_SETTINGS_BUSY,
  SETTINGS_PENCIL_CLASS,
} from '../../src/renderer/screens/projects'
import { ACCOUNT_KEYS_BUSY, ACCOUNT_KEYS_TITLE } from '../../src/renderer/screens/accountkeys'
import {
  CREDENTIALS_SERVICE_BUSY,
  CREDENTIALS_SERVICE_TITLE,
} from '../../src/renderer/screens/credentialsservice'
import {
  LICENCE_BOX_CLASS,
  LICENCE_BUSY,
  LICENCE_HINT,
  LICENCE_SAVED_WORD,
  LICENCE_SUBMIT_CLASS,
  LICENCE_TITLE,
} from '../../src/renderer/screens/licence'
import { MANAGED_BUSY, MANAGED_TITLE } from '../../src/renderer/screens/managed'
import {
  SEATS_ADVISORY,
  SEATS_BUSY,
  SEATS_CLI_FLOOR_WORD,
  SEATS_OVER_WARNING,
  SEATS_PEOPLE_CLASS,
  SEATS_TITLE,
  SEATS_VALUE_CLASS,
} from '../../src/renderer/screens/seats'
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
  MOVE_TARGET_UNCHOSEN,
  MOVE_TARGET_UNCHOSEN_LABEL,
  MOVE_UNASSIGNED_WORD,
} from '../../src/renderer/graph/move'
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

/**
 * floor is the version floor the Coordinator's map cases set, and the mock below
 * is the one thing they cannot reach without it.
 * `ORCHESTRATOR_MAP_CLI_FLOOR` is `null` on this tree -- `orchestrator map
 * --json` is registered from `ee/` and no release carries it -- so
 * `orchestratorMapSupported` answers false for every version and the Coordinator
 * tab draws its own sentence and spawns nothing (the case below that clicks the
 * tab on the real constant pins exactly that state). The branch **above** the
 * floor is therefore unreachable through the constant, which is what
 * `orchestratorMapSupported`'s own `floor` argument exists for: this mock
 * changes its default and nothing else, so every other case in this file reads
 * the real constant, which is `null`, and nothing here behaves differently. The
 * cases that set it are `the Coordinator's map read, above its version floor`.
 */
const floor = vi.hoisted(() => ({ value: null as string | null, merges: null as string | null }))

vi.mock('../../src/shared/version', async (importOriginal) => {
  const real = await importOriginal<typeof import('../../src/shared/version')>()
  return {
    ...real,
    orchestratorMapSupported: (version: string, at: string | null = floor.value) =>
      real.orchestratorMapSupported(version, at),
    // IR4's own floor, mocked the same way and for the same reason:
    // `PLAN_STATUS_NO_MERGES_CLI_FLOOR` is `null` on this tree -- nothing carries
    // `plan status --no-merges` yet -- so `planStatusNoMergesSupported` answers
    // false for every installation and the Graph tab draws its canvas and its
    // merge state from the single read. The branch a released floor takes -- two
    // reads, the canvas first -- could not be driven at all without this, and the
    // cases that set it are `the Graph canvas read before its merge state`.
    planStatusNoMergesSupported: (version: string, at: string | null = floor.merges) =>
      real.planStatusNoMergesSupported(version, at),
  }
})

/** MAP_FLOOR is the release that first carries `orchestrator map --json` and
 * MAP_VERSION the installation's own line: one patch above it, so the map read
 * is admitted. Both are spelled rather than imported -- the claim is about the
 * comparison, not about a constant's value. */
const MAP_FLOOR = '1.4.0'
const MAP_VERSION = '1.4.2'

/** MERGES_FLOOR is the release that first carries `plan status --no-merges`
 * (IR4's floor) and MERGES_VERSION the installation's own line: one patch above
 * it, so the tab's two reads are admitted. Spelled rather than imported, on the
 * two above's terms. */
const MERGES_FLOOR = '0.33.0'
const MERGES_VERSION = '0.33.1'

/** COORDINATOR_MAP is the document the map read answers with in the cases that
 * set the floor: two active orchestrators of one project -- the project's own
 * and one named -- with a dependency edge, an overlap edge and a sweep that is
 * on. It goes through the console's own decoder on its way to the screen, so a
 * case whose document the decoder refuses fails rather than passing on a
 * drawing nobody made. */
const COORDINATOR_MAP = {
  version: 1,
  project: 'repo-a',
  generated_at: '2026-11-22T10:00:00Z',
  nodes: [
    { id: 'repo-a', name: '', driver: 'person-1', active_tasks: 2, running_jobs: 1, lease_age_seconds: 90 },
    { id: 'repo-a~auth', name: 'auth', driver: 'person-2', active_tasks: 0, running_jobs: 3, lease_age_seconds: null },
  ],
  edges: [
    { from: 'repo-a~auth', to: 'repo-a', kind: 'dependency', reason: '' },
    { from: 'repo-a', to: 'repo-a~auth', kind: 'overlap', reason: 'both touch src/auth' },
  ],
  health: {
    sweep: true,
    started_at: '2026-11-22T09:59:00Z',
    ok_at: '2026-11-22T09:59:30Z',
    at: '2026-11-22T10:00:00Z',
    note: '',
  },
}

afterEach(() => {
  floor.value = null
  floor.merges = null
})

/** CONSOLE_ROOT is the package root, which is where the sources, the fixtures
 * and the built tree are read from. */
const CONSOLE_ROOT = join(__dirname, '..', '..')

/**
 * READ_METHODS is the twenty-two reads `entry.ts` may reach for (`ReadBridge`'s own
 * list less `summaryRuns`, which `queueBridge` answers with its own recorder),
 * by the name the preload exposes. Every one of them is on the fake bridge
 * below, and every one records its call -- so "this read was never made" is a
 * claim this suite can make about any of them.
 *
 * `graphRead` is the graph plan's CG9a read, and it is the fifteenth: the
 * Graph's second scope is read through it, and a case that drove the Graph with
 * a bridge that could not answer it would be driving a console whose only
 * remaining scope is the plan's. `managedStatus`, `policyShow` and
 * `environmentReport` are the managed-project screens plan's CM4 rows: the
 * Project tab's later sections read them (CM10). `readLicence` is the
 * subscriptions plan's S2.7 row: the Project tab's licence key, which is the one
 * read here whose answer is not a `Snapshot` around a CLI document -- that
 * operation spawns no child at all. `readSeats` is the subscriptions plan's
 * S4.2 row: the same tab's advisory seat estimate, which reads the
 * installation's own bucket and is therefore the one read of that tab whose
 * answer is a minute rather than a second out. `graphWatch` is the graph plan's CG9b row
 * and the last: the Graph tab's own live feed, which is why a case that drives
 * the Graph and never leaves it is a case that keeps a `fugaro graph --watch`
 * child alive -- so this bridge answers it, and the call it records is how the
 * tab's own lifetime is asserted below.
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
  'readLicence',
  'readSeats',
  'graphWatch',
  'orchestratorMap',
] as const

/** A_REPLY is a bridge method's answer. */
type AReply = (args: unknown) => unknown

/** FakeBridge is the preload seam, recorded: the handshake, the installation,
 * and the reads (`READ_METHODS`), each answering what the case told it to and
 * appending its own call to `calls`. */
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
    // a view this mode withholds (GV2.1's decision, and `VIEWS`' own `null`): the
    // read is repository state and none of its operations is a `spend` read, so
    // the plan, its tasks and its stages are drawn -- and the one money figure
    // the screen draws since S4, the plan's estimate cost, is withheld **inside
    // the drawing** rather than with the tab (`graphScreen`'s third argument;
    // `the graph’s estimate in a reduced console` below is the case that holds
    // it). `coordinator` is offered because the project view is repository state
    // rather than money, so the mode withholds no figure of it (Bitbucket
    // credentials T1.2).
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

  it('draws the spend-rate gauge and the runs cap bar, and withholds them in the reduced mode', async () => {
    // The `caps` view is the Live screen's spend rate, its red zone and the two
    // figures that are a limit rather than money spent (`REDUCED_VIEWS`,
    // `version.ts`), and the gate's verdict reaches the screen as `renderLive`'s
    // fourth argument -- the withholding the screen does itself is `live.test.ts`'s
    // (`capsWithheld`), and what this case holds is that the entry *hands the
    // verdict over at all*. The same capped frame, in the two modes.
    const last = syntheticDocuments()[syntheticDocuments().length - 1] as Readonly<Record<string, unknown>>
    const capped = {
      ...last,
      total: { ...(last['total'] as Readonly<Record<string, unknown>>), max_parallel: 4 },
    }
    const panelIn = async (compatibility: Record<string, unknown>): Promise<FakeNode> => {
      const { bridge } = fakeBridge({ compatibility, replies: { subscribeLive: () => snapshotReply(capped) } })
      const document = await boot(bridge, new FakeDocument())
      const panel = panelOf(document)
      expect(classed(panel, 'live-repo-name'), 'the Live screen was not drawn').toHaveLength(1)
      return panel
    }
    // The recording's day is at its cap, so its red zone is zero and the needle
    // stands at the right-hand end: the figure this holds is the needle's
    // presence and its place, not a rate the fixture happens to carry.
    const ok = await panelIn(anOkCompatibility())
    expect(
      classed(ok, 'live-gauge-needle').map((needle) => needle.attributes.get('x')),
      'the ok mode drew no needle',
    ).toEqual(['99'])
    expect(classed(ok, 'live-gauge-zone'), 'the ok mode drew no red zone').toHaveLength(1)
    expect(
      classed(ok, 'live-fleet-runs-cap').map((cap) => cap.attributes.get('data-state')),
      'the ok mode did not read max_parallel',
    ).toEqual(['measured'])

    const reduced = await panelIn(aReducedCompatibility())
    expect(classed(reduced, 'live-gauge-needle'), 'the reduced mode drew the needle').toEqual([])
    expect(classed(reduced, 'live-gauge-zone'), 'the reduced mode drew the red zone').toEqual([])
    const hidden = classed(reduced, 'live-fleet-runs-cap')
    expect(hidden.map((cap) => cap.attributes.get('data-state')), 'the reduced mode still read a cap').toEqual([
      'unavailable',
    ])
    expect(hidden.map((cap) => cap.attributes.get('title')), 'the runs bar withheld without its reason').toEqual([
      LIVE_CAPS_HIDDEN_WORD,
    ])
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

  it('offers a refused log section its own read again, and never the diagnosis (R1)', async () => {
    // F21's own case: a run's refused section had the CLI's sentence and the
    // command it names and **no way to ask again**. The two reads are
    // independent, so the control in the log section reads `run logs` and
    // nothing else.
    const frame = syntheticDocuments()[syntheticDocuments().length - 1] as Readonly<Record<string, unknown>>
    const answers: ReturnType<typeof holding>[] = []
    const { bridge, calls } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(frame),
        diagnoseRun: () => ({ ok: true, data: { document: {} } }),
        readRunLogs: () => {
          const answer = holding()
          answers.push(answer)
          return answer.reply()
        },
      },
    })
    const document = await boot(bridge, new FakeDocument())
    const panel = panelOf(document)
    ;(classed(panel, 'live-run')[0] as FakeNode).click()
    answers[0]?.release({ ok: false, reason: 'no such run 20261015-120000-aa01' })
    await flush()

    const retry = classed(classed(panel, 'run-logs')[0] as FakeNode, RETRY_CLASS)[0]
    expect(retry, 'the refused log section offered no Retry').toBeDefined()
    expect(retry?.textContent).toBe(RETRY_LABEL)
    const diagnoses = calls.filter((call) => call.name === 'diagnoseRun').length

    retry?.click()
    await flush()
    expect(
      calls.filter((call) => call.name === 'readRunLogs'),
      'the press did not read the section it stands in',
    ).toHaveLength(2)
    expect(
      calls.filter((call) => call.name === 'diagnoseRun'),
      'the log Retry read the run’s record as well',
    ).toHaveLength(diagnoses)
    expect((classed(panel, 'run-logs')[0] as FakeNode).attributes.get(BUSY_ATTRIBUTE)).toBe('true')

    answers[1]?.release({
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
    expect(classed(panel, 'run-log-line')).toHaveLength(1)
    expect(classed(panel, 'console-unavailable')).toEqual([])
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
      // The watches are counted rather than the whole log compared: the prewarm
      // queue reads the other three tabs the moment Live is painted (PW1), so
      // `planStatus`, one more `listRuns` and the rest are in the log of every
      // boot here. What this case is about is the `watch`.
      expect(watchesOf(calls)).toBe(1)

      // One tick: a second watch is made, and it does not answer. The rigor join
      // is not read again -- its TTL is `LIVE_RIGOR_TTL_MS` and this tick is one
      // second in -- so a poll is one `watch` and the join is not on its clock.
      await vi.advanceTimersByTimeAsync(LIVE_POLL_MS)
      expect(watchesOf(calls)).toBe(2)
      expect(held).toHaveLength(1)

      // Three more ticks with that read outstanding: **no third call**. This is
      // the claim the flag exists for -- a slow read cannot be overtaken by the
      // next tick, so the console never has two watches of one installation in
      // flight at once.
      await vi.advanceTimersByTimeAsync(LIVE_POLL_MS * 3)
      expect(watchesOf(calls)).toBe(2)

      // Once it answers, the next tick is a new read.
      held[0]?.()
      await vi.advanceTimersByTimeAsync(LIVE_POLL_MS)
      expect(watchesOf(calls)).toBe(3)
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
      expect(watchesOf(calls)).toBe(1)

      // Spend is offered in an `ok` console, and leaving Live for it stops the
      // interval; the read behind the view is made once, on entry. The click
      // also stops the prewarm queue (PW1) -- and the queue reads nothing of
      // Spend or the Project tab until it gets there, which is why the caps'
      // read below is the first `budget show` in the log.
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
      // The boot's own join, plus the run list the prewarm queue read for the
      // Graph it cached (PW1). Which of the two is which is not asserted: what
      // follows is a delta, and the claim is the same either way.
      const atBoot = joinsOf(calls)
      expect(atBoot).toBeGreaterThan(0)

      // Five polls, five seconds: the watch is read five more times and the join
      // is not read again.
      await vi.advanceTimersByTimeAsync(LIVE_POLL_MS * 5)
      expect(watchesOf(calls)).toBe(6)
      expect(joinsOf(calls)).toBe(atBoot)

      // Past the TTL the next tick refreshes it -- once, however many ticks the
      // advance covers, because the read's own start is what the TTL is measured
      // from.
      await vi.advanceTimersByTimeAsync(LIVE_RIGOR_TTL_MS * 3)
      expect(joinsOf(calls)).toBeGreaterThan(atBoot)
      expect(joinsOf(calls)).toBeLessThanOrEqual(atBoot + 4)
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

      // The prewarm queue's own report read is in flight for the Spend tab it
      // has not reached yet (PW1). It is held here like the visit's, and it is
      // the boundary the guard below counts from: the click that follows stops
      // the queue, so the one read this case answers is the visit's.
      const queued = held.length

      // Spend is entered and its report does not answer; the operator goes back
      // to Live, which puts the last poll's own drawing back at once.
      buttonFor(document, 'spend').click()
      await flush()
      buttonFor(document, 'live').click()
      expect(panel.children[0]?.attributes.get('class')).toBe('live')

      // The spend report answers last. The panel is Live -- the view on screen
      // -- and not the Spend screen the report was read for.
      expect(
        held.length - queued,
        'the spend report answered before the operator moved: the case proves nothing',
      ).toBe(1)
      held[queued]?.()
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
      // The prewarm queue's report read -- the Spend tab's own default view,
      // read before the operator got there (PW1) -- is answered first, so the
      // read this case answers below is the visit's.
      const queued = held.length
      buttonFor(document, 'spend').click()
      await flush()
      for (const each of held.splice(0, queued)) each.answer()
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

    // And **every** flow `main` builds is over that one window's confirmer,
    // never over the declining one: D1115's "one window, two questions" is one
    // `createConfirmWindow` call in this whole file and three runners built over
    // it -- the launcher, the older settings edit and the one modal SU2b wires
    // the Project tab's pencil to -- because a second window would be a second
    // implementation of the tick, the delay and the sender gate.
    const asked = /const confirmer = createConfirmWindow\(\{[\s\S]*?\n  \}\)\n/.exec(body)
    if (asked === null) throw new Error('the entry builds no confirmer to read')
    expect(withoutComments(index).match(/createConfirmWindow\(/g) ?? []).toHaveLength(1)
    expect(body).toContain('launchRunner = createLaunchRunner(')
    expect(body).toContain('settingsEditRunner = createSettingsEdit(')
    expect(body).toContain('settingsEditorRunner = createSettingsEditor(')
    expect(body).not.toContain('DECLINING_CONFIRMER')

    // The third runner's fail-closed default is its own declining twin, so a
    // pencil pressed before `main` has built the window is answered
    // `SETTINGS_EDIT_UNBUILT` rather than reaching a half-built flow -- and the
    // surface's `settings.editor.open` reads it at message time.
    expect(index).toContain('let settingsEditorRunner: SettingsEditorRunner = DECLINING_SETTINGS_EDITOR_FLOW')
    expect(withoutComments(index)).toContain("'settings.editor.open': (args) => settingsEditorRunner.open(args)")

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
    // **And the settings window with it** (D1115): an installation that moved
    // closes the edit's window too, and one already read for that installation
    // must not write. All three flows are named, in both places, because the mark
    // an edit leaves is only reachable while its window is not.
    expect(chosen[0]).toContain('settingsEditRunner.cancelFor(chosen.project)')
    expect(chosen[0]).toContain('settingsEditorRunner.cancelFor(chosen.project)')

    const removed = /onRecordRemoved: \(project\) => \{[\s\S]*?\n    \},\n/.exec(body)
    if (removed === null) throw new Error('the entry has no onRecordRemoved to read')
    expect(removed[0]).toContain('launchRunner.cancelFor(project)')
    expect(removed[0]).toContain('settingsEditRunner.cancelFor(project)')
    expect(removed[0]).toContain('settingsEditorRunner.cancelFor(project)')
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
    // The two reads the mount's own screens make: the Live frame, and the run
    // list the Live screen's rigor join is read from. The rail reads neither.
    // What follows them is the prewarm queue's, for the tabs nobody is on (PW1),
    // and none of those is the rail's either -- the rail's spend is `n/a` above,
    // and no read of this boot fills it.
    expect(watchesOf(calls)).toBe(1)
    expect(joinsOf(calls)).toBeGreaterThan(0)
    expect(
      calls.every((call) =>
        ['subscribeLive', 'listRuns', 'planStatus', 'runReport', 'showBudget'].includes(call.name),
      ),
      'a read the mount does not make, and the prewarm queue does not either',
    ).toBe(true)
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

    // A click shows the view and makes its read. The counts are deltas: the
    // prewarm queue's own reads are in `calls` before any of these clicks (PW1),
    // and what the case is about is what the click adds.
    const planBefore = calls.filter((call) => call.name === 'planStatus').length
    graph.click()
    await flush()
    expect(graph.attributes.get('aria-current')).toBe('page')
    expect(live.attributes.has('aria-current')).toBe(false)
    expect(calls.filter((call) => call.name === 'planStatus')).toHaveLength(planBefore + 1)
    const budgetBefore = calls.filter((call) => call.name === 'showBudget').length
    spend.click()
    await flush()
    expect(spend.attributes.get('aria-current')).toBe('page')
    expect(calls.filter((call) => call.name === 'showBudget')).toHaveLength(budgetBefore + 1)

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
    // The prewarm queue's reads for the other tabs (PW1) have all answered by
    // the time the buttons are pressed: what grows the log below would be a read
    // a *click* made, which is the claim.
    await flush()
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
  reads: Readonly<Record<string, AReply>> = {},
): FakeBridge & { readonly rail: readonly { readonly name: string; readonly args: unknown }[] } {
  const frame = syntheticDocuments()[0] as Readonly<Record<string, unknown>>
  const base = fakeBridge({ replies: { subscribeLive: () => snapshotReply(frame, nowStamp()), ...reads } })
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
 * launchBridge is the fake bridge for a case that launches a run from the Live
 * screen: one open installation that records a checkout and offers the
 * repository the launch form is pre-filled with, with the console's one write
 * recorded in the same list as every read -- so "the console launched exactly
 * once, and re-read Live after it" is a claim about the order of the calls.
 */
function launchBridge(answers: Readonly<Record<string, AReply>>): FakeBridge {
  const base = railBridge([
    {
      ...listRow('acme', { open: true }),
      checkout: '/home/operator/src/acme',
      repositories: [OFFERED_REPO],
    },
  ])
  // The base's list is the same array its own reads append to; it is typed
  // `readonly` because a case reads it, and this helper is the one writer.
  const calls = base.calls as { name: string; args: unknown }[]
  base.bridge['launchRun'] = (args: unknown): unknown => {
    calls.push({ name: 'launchRun', args })
    const answer = answers['launchRun']
    return answer === undefined ? { ok: false, reason: 'the console launched nothing in this case' } : answer(args)
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

/** launchedReply is one accepted `run.launch`, in the envelope the main process
 * builds: the six keys `launchOutcomeOf` reads closed, with a `launchResult`'s
 * thirteen inside. It is here rather than in `launch-form.test.ts` because this
 * file is where the *write* is driven -- the form's submit, and what the console
 * does with the tab the write was made in (R2). */
function launchedReply(): Record<string, unknown> {
  return {
    ok: true,
    data: {
      ok: true,
      runId: '20261119-010000-abcd',
      result: {
        project: 'acme',
        run: '20261119-010000-abcd',
        repo: 'owner/repo-a',
        runId: '20261119-010000-abcd',
        branch: 'main',
        ref: 'main',
        execution: 'cloud-run',
        logUrl: '',
        status: 'launched',
        pr: null,
        previousRun: '',
        recipe: null,
        projectLayer: null,
      },
      reason: '',
      outcomeUnknown: false,
      replayed: false,
    },
  }
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

  it('reads Live again the moment a launch is accepted, and not at the next tick (R2)', async () => {
    // R2 and F17: `run.launch` names the tab it was made in. Live's frame is the
    // one that does not hold the run the console has just started, and the
    // poll's own tick is a second away -- so the scope is cleared and read at
    // once, which is the frame the operator pressed Launch to see.
    const { bridge, calls } = launchBridge({ launchRun: () => launchedReply() })
    const document = await boot(bridge, new FakeDocument())
    const watches = calls.filter((call) => call.name === 'subscribeLive').length
    const joins = calls.filter((call) => call.name === 'listRuns').length

    ;(launchField(document, LAUNCH_OPEN_CLASS) as FakeNode).click()
    await flush()
    typeInto(document, LAUNCH_REPOSITORY_CLASS, OFFERED_REPO)
    typeInto(document, LAUNCH_TASK_CLASS, 'try the nightly job again')
    launchField(document, LAUNCH_SUBMIT_CLASS).click()
    await flush()

    expect(calls.filter((call) => call.name === 'launchRun')).toHaveLength(1)
    expect(
      calls.filter((call) => call.name === 'subscribeLive'),
      'the accepted launch left the pre-launch frame on screen',
    ).toHaveLength(watches + 1)
    // The join's own clock goes with the frame: `live.rigorAt` is what holds the
    // run list for `LIVE_RIGOR_TTL_MS`, and the run this write started is one
    // only a fresh join can put a mode chip on.
    expect(
      calls.filter((call) => call.name === 'listRuns'),
      'the launch kept the join read from before it',
    ).toHaveLength(joins + 1)
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

  /** waiting is how many **layouts** are held right now: the observable a case
   * needs to stand in the moment between the plan and the picture. The
   * registration message the client sends from its constructor is not one of
   * them -- `FakeWorker` ignores it, and so does this count. */
  static waiting = 0

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
    if ((message as { readonly cmd?: unknown }).cmd === 'layout') HeldWorker.waiting += 1
    this.held.push(() => {
      super.postMessage(message)
    })
  }

  release(): void {
    this.open = true
    HeldWorker.waiting = 0
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
 * `HeldWorker` -- and the default is the one that answers at once;
 * `compatibility` is the handshake a case wants the gate to have answered, and
 * the default is the mode that withholds nothing. */
function graphBridge(
  replies: Readonly<Record<string, AReply>> = {},
  worker: typeof FakeWorker = FakeWorker,
  compatibility?: Record<string, unknown>,
): FakeBridge {
  vi.stubGlobal('Worker', worker)
  return fakeBridge({
    compatibility,
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

  it('holds the tab’s own feed while the tab is on screen, and gives it up when it is left', async () => {
    // H6, at the wiring: the graph feed is a subscription the CLI holds open on
    // the project's scope subtree, so the console asks for it when the Graph
    // becomes the view and gives it up when the operator walks away. **The stop
    // names the scope the subscription was made with**, because that argv is the
    // feed's key in the main process (`src/source/cli.ts`): a stop with a
    // different `source` would name a feed that is not running and leave this
    // one's child alive until quit.
    const { bridge, calls } = graphBridge({
      // The plan scope needs a checkout this installation does not record, so
      // the tab opens on the coordinator scope -- which is the scope this feed
      // draws (`all`) and therefore the one whose document reaches the panel.
      planStatus: () => ({ ok: false, reason: NO_PLAN_CHECKOUT }),
      graphRead: () => graphReply(),
      graphWatch: () => graphReply(),
    })
    const document = await openGraph(bridge)
    expect(calls.filter((call) => call.name === 'graphWatch')).toEqual([
      { name: 'graphWatch', args: { source: GRAPH_SCOPE_ALL } },
    ])
    // The subscription's own answer is a drawing: the call that starts the child
    // is also `current()`, so the picture is up before the first tick -- and it
    // is the coordinator scope's, which is the drawing the feed's document lays
    // out (`all`), so the plan the tab opened on is not what is on screen.
    expect(classed(panelOf(document), 'graph-scope-node').map((each) => each.attributes.get('data-key'))).toEqual([
      'coordinator:acme/widgets#42',
    ])

    buttonFor(document, 'live').click()
    await flush()
    expect(calls.filter((call) => call.name === 'graphWatch')).toEqual([
      { name: 'graphWatch', args: { source: GRAPH_SCOPE_ALL } },
      { name: 'graphWatch', args: { source: GRAPH_SCOPE_ALL, visible: false } },
    ])

    // And a second visit subscribes again rather than trusting a child the
    // window gave up: nothing is held while nobody is looking at the graph.
    buttonFor(document, 'graph').click()
    await flush()
    const feeds = calls.filter((call) => call.name === 'graphWatch')
    expect(feeds).toHaveLength(3)
    expect(feeds[2]).toEqual({ name: 'graphWatch', args: { source: GRAPH_SCOPE_ALL } })
  })
  it('holds no feed for a scope the feed does not draw', async () => {
    // **The feed's document is the coordinator scope's.** `graph --watch --json
    // --source all` prints the union scope's document; the plan scope -- the one
    // the Graph opens on whenever the installation records a checkout -- is a
    // `plan.status`, and there is no streaming form of it. A child held there
    // prints a document every one of whose answers is thrown away, which is H6's
    // own cost (the CLI's subscription on the project's scope subtree) on the
    // console's common path, so it is never started.
    const { bridge, calls } = graphBridge({ graphRead: () => graphReply(), graphWatch: () => graphReply() })
    const document = await openGraph(bridge)
    expect(scopeChosen(document), 'the case would prove nothing: the tab did not open on the plan scope').toBe(
      GRAPH_SCOPE_PLAN,
    )
    expect(classed(panelOf(document), 'graph-canvas').length).toBeGreaterThan(0)
    // Neither the entry nor the tick: the tab is on screen and the plan it read
    // is the picture it holds. The tick is the case's real half -- it is what
    // runs while the tab is up, and a poll that subscribed for a scope it cannot
    // draw would hold the child from the second after the entry.
    await vi.advanceTimersByTimeAsync(LIVE_POLL_MS * 3)
    await flush()
    expect(calls.filter((call) => call.name === 'graphWatch')).toEqual([])

    // The operator asks for the scope the feed is about, and the child is
    // subscribed in that frame: the document the scope opens with is the feed's
    // own, and not one a tick brought a second later.
    await chooseScope(document, GRAPH_SCOPE_ALL)
    expect(calls.filter((call) => call.name === 'graphWatch')).toEqual([
      { name: 'graphWatch', args: { source: GRAPH_SCOPE_ALL } },
    ])

    // And back on the plan scope the child is given up: nothing is held for a
    // scope whose documents are not this screen's.
    await chooseScope(document, GRAPH_SCOPE_PLAN)
    expect(calls.filter((call) => call.name === 'graphWatch')).toEqual([
      { name: 'graphWatch', args: { source: GRAPH_SCOPE_ALL } },
      { name: 'graphWatch', args: { source: GRAPH_SCOPE_ALL, visible: false } },
    ])
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
// The plan's estimate in a reduced console (the estimates plan's S4)
// ---------------------------------------------------------------------------

/**
 * withEstimate is the fixture plan with one plan's estimate block on it: the
 * block `plan status --json` writes since S3, which the fixture itself does not
 * carry (it is a document from before that release, which is what
 * `graph-estimate.test.ts`'s absent-block case is about).
 */
function withEstimate(): unknown {
  const document = JSON.parse(JSON.stringify(PLAN_STATUS)) as { plans: { estimate?: unknown }[] }
  const plan = document.plans[0]
  if (plan === undefined) throw new Error('the fixture has no plan to estimate')
  plan.estimate = { ready_in_minutes: { low: 40, high: 70 }, cost_usd: { low: 3, high: 5 }, cost_unknown_tasks: 0 }
  return document
}

describe('the graph’s estimate in a reduced console (S4)', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  /** estimateLine opens the Graph on a plan that carries an estimate, switches
   * to the drawing that has a heading per plan (the list -- the canvas gives a
   * plan no text of its own), and answers the estimate line it drew. */
  async function estimateLine(compatibility: Record<string, unknown>): Promise<FakeNode | undefined> {
    const { bridge } = graphBridge({ planStatus: () => planStatusReply(withEstimate()) }, FakeWorker, compatibility)
    const document = await openGraph(bridge)
    const view = classed(panelOf(document), 'graph-view')[0]
    if (view === undefined) throw new Error('the graph drew no view switch')
    view.click()
    await flush()
    expect(classed(panelOf(document), 'graph-list'), 'the list was not drawn').not.toEqual([])
    return classed(panelOf(document), 'graph-list-estimate')[0]
  }

  it('hands the screen the gate’s verdict for the spend view, and draws the money only where the mode allows it', async () => {
    // **The call site is the thing this case is for.** The screen's own default
    // is to draw what the document carried (`GraphScreenDeps.costWithheld` is
    // optional and absent means "no gate in front of this drawing"), so a call
    // site that dropped the argument would draw `$3.00–$5.00` on a console whose
    // handshake says it cannot vouch for the CLI's money fields -- silently, and
    // with every other case in this suite still green. What holds it is the two
    // modes below, drawn through the entry: the same plan, the same read, and
    // the gate's answer the only difference.
    const ok = await estimateLine(anOkCompatibility())
    expect(ok?.textContent, 'the ok mode drew no cost').toContain('$3.00–$5.00')
    expect(ok?.attributes.get('title'), 'the ok mode gave a drawn figure a reason').toBeUndefined()

    const reduced = await estimateLine(aReducedCompatibility())
    expect(reduced?.textContent, 'the reduced mode drew a dollar figure').not.toContain('$')
    // The ready-by range stays -- minutes are not money -- and the withheld cost
    // reads the console's unavailable mark with its reason in the `title`.
    expect(reduced?.textContent).toBe(
      `${GRAPH_ESTIMATE_READY} 40–70 ${GRAPH_ESTIMATE_MINUTES}, ${UNAVAILABLE_MARK}–${UNAVAILABLE_MARK}`,
    )
    expect(reduced?.attributes.get('title'), 'the withheld cost lost its reason').toBe(
      GRAPH_ESTIMATE_COST_HIDDEN_WORD,
    )
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

/** typeValue writes what an operator would type into one control. */
function typeValue(box: FakeNode, value: string): void {
  ;(box as unknown as { value: string }).value = value
}

/** valueUnderPencil is the value cell of the row whose pencil the section drew,
 * as it draws it now: the figure a settings write's own re-read replaces. The
 * row is found through the pencil and not by its place in the table -- the
 * section groups its rows, so a place is not a fact about which row this is --
 * and the case asks again rather than holding the node it pressed, because the
 * read the write asked for repaints the row.
 */
function valueUnderPencil(document: FakeDocument): string {
  const pencil = classed(panelOf(document), SETTINGS_PENCIL_CLASS)[0] as FakeNode
  const row = pencil.closest('li') as FakeNode
  return textIn(row, 'projects-settings-value')
}

/** textIn is the text of the single node carrying one class under a root: the
 * drawn value of one cell of a screen this suite is reading. */
function textIn(root: FakeNode, className: string): string {
  const found = classed(root, className)
  expect(found, `expected exactly one .${className}`).toHaveLength(1)
  return found[0]?.textContent ?? ''
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
/** MANAGED_REFUSAL is a refused `managed status --json`, in the CLI's own words:
 * the sentence **both** of the sections that read it draw, because they are one
 * read (the managed state and the retirements). */
const MANAGED_REFUSAL = 'the managed read refused, in the CLI’s own words'
/** WINDOW_REFUSAL is a refused `runs summary --json`, in the CLI's own words: the
 * sentence the strip draws beside the figures that are not its read's. */
const WINDOW_REFUSAL = 'the window’s read refused, in the CLI’s own words'

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
    const { bridge } = graphBridge({
      planStatus: () => ({ ok: false, reason: GRAPH_REFUSAL }),
      showBudget: () => budget.reply(),
      runReport: () => report.reply(),
      // The run list is **held and never released**: no tab of this case may
      // read it, so a screen that did would answer its own refusal below rather
      // than the sentence this case is about.
      listRuns: () => runs.reply(),
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
    // The Coordinator draws a frame of its own in the click's frame (CG7b,
    // D3210): the title and the source line are up at once, and under them the
    // console's own sentence naming the release the map's read needs. **That
    // sentence is the whole of what this case can assert**: the floor is `null`
    // until a CLI release carries `orchestrator map`, so `orchestratorMapSupported`
    // answers false for every version this console knows, the tab spawns nothing
    // at all, and no read of the map is ever made -- which is also why the run
    // list this case holds is never asked for here.
    buttonFor(document, 'coordinator').click()
    await flush()
    expect(operationOf(document), 'the Graph’s refusal outlived its tab').toBe(null)
    expect(holdsText(panelOf(document), GRAPH_REFUSAL)).toBe(false)
    expect(textIn(panelOf(document), COORDINATOR_TITLE_CLASS)).toBe(COORDINATOR_TITLE)
    expect(textIn(panelOf(document), COORDINATOR_SOURCE_CLASS)).toBe(COORDINATOR_SOURCE_LINE)
    expect(holdsText(panelOf(document), COORDINATOR_FLOOR_WORD)).toBe(true)
    expect(holdsText(panelOf(document), COORDINATOR_FLOOR_AND_WORD)).toBe(true)
    // A tab that draws its own frame is not waiting on anything, so it draws no
    // mark and no placeholder: the floor is an answer, not a read in flight.
    expect(busyLabels(document)).toEqual([])
    expect(readingOf(document)).toBe(null)
    // ...and it stays that way: a line armed for a read nobody made cannot
    // arrive over the screen.
    await vi.advanceTimersByTimeAsync(READING_STILL_MS)
    expect(holdsText(panelOf(document), READING_STILL)).toBe(false)
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
   * **The Coordinator draws no skeleton at all** (CG7b, D3210): its one read is
   * the map, and that read sits behind a version floor no released CLI has
   * passed yet (`orchestratorMapSupported`), so the tab draws its own frame with
   * the sentence naming the release the read needs and spawns nothing. There is
   * no read in flight for a shape to stand in for, and the empty list below is
   * the assertion that none was drawn. */
  const ownShape = (tab: string): readonly SkeletonShape[] | null => {
    if (tab === 'projects') return [PROJECTS_SHAPE, PROJECTS_MODELS_SHAPE]
    if (tab === 'coordinator') return []
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
      } else if (own.length === 0) {
        // The Coordinator: a frame of its own, drawn in the click's frame, with
        // nothing behind it to wait for -- so no mark and no shape.
        expect(
          busyLabels(document),
          `the ${tab} tab drew a mark over a read it never made`,
        ).toEqual([])
        expect(classed(panelOf(document), tab)[0], `the ${tab} tab drew no screen of its own`).toBeDefined()
        expect(holdsText(panelOf(document), COORDINATOR_FLOOR_WORD)).toBe(true)
      } else {
        expect(busyLabels(document), `the ${tab} tab drew no mark of its own`).toContain(PROJECTS_BUSY)
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
  readonly count: () => number
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
    // count is how many reads are held so far, which is how a case marks where
    // its own begin: the prewarm queue reads this console's other tabs at boot
    // (PW1), and those reads are the operator's visit's, not the case's.
    count: () => held.length,
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

/** watchesOf is the number of `live.watch` reads in the log: the Live poll's own
 * reads and nothing else. The prewarm queue reads for the tabs nobody is on the
 * moment the first paint is done (PW1), so every boot here logs calls that are
 * not the poll's, and a case about the poll's cadence counts these. */
function watchesOf(calls: readonly { readonly name: string }[]): number {
  return calls.filter((call) => call.name === 'subscribeLive').length
}

/** joinsOf is the number of `runs ls` reads in the log. Two of them are made at
 * a boot: the Live screen's rigor join, and the run list the prewarm queue
 * decorates the Graph it caches with (PW1). The join's own cadence is asserted
 * as a delta from what the boot left behind, which is the claim either way --
 * the join is not on the poll's clock. */
function joinsOf(calls: readonly { readonly name: string }[]): number {
  return calls.filter((call) => call.name === 'listRuns').length
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
    // The prewarm queue read this same view before the operator got there
    // (PW1): the read this case is about is the visit's own, which is the last
    // one in the log.
    const reads = calls.filter((call) => call.name === 'runReport')
    expect(
      reads[reads.length - 1]?.args,
      'the screen did not open on the recent week by day',
    ).toEqual({ by: 'day', since: '7d' })
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
    // The caps read is RS5's streamed one, and this bridge has no
    // `budget.stream` behind it: the read asks the operation first and reaches
    // `budget show` when that answers nothing, one gap later than the click. A
    // visit still makes the read -- nothing here waits for anything -- and the
    // held refusal is on screen until this answer replaces it.
    await flush()
    expect(answers, 'the visit made no caps read').toHaveLength(2)
    answers[1]?.release({ ok: true, data: { document: {} } })
    await flush()
    const caps = sectionOf(document, 'spend-caps')
    expect(holdsText(caps, BUDGET_REFUSAL), 'the refusal outlived the answer that replaced it').toBe(false)
    // `data-available` is `fillSpendCaps`'s own attribute, so it says the section
    // was drawn from an answer rather than from the sentence beside it.
    expect(caps.getAttribute('data-available')).toBe('true')
    expect(caps.getAttribute(BUSY_ATTRIBUTE)).toBeNull()
  })

  it('offers a refused caps section its own read again, and no other (R1)', async () => {
    // R1: a refused or timed-out section draws the CLI's sentence, the command
    // it names **and** a control that re-spawns that section's own read. The
    // caps and the report are two independent reads, so the control beside the
    // caps' sentence must read `budget show` and never the report -- a Retry
    // that re-read a view the operator was not refused would answer one refusal
    // with another read's figures.
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

    const retry = classed(sectionOf(document, 'spend-caps'), RETRY_CLASS)[0]
    expect(retry, 'the refused caps section offered no Retry').toBeDefined()
    expect(retry?.textContent).toBe(RETRY_LABEL)
    // The CLI's own sentence and the command it names are still the section's
    // whole explanation: the control is added beside them, not instead of them.
    expect(holdsText(sectionOf(document, 'spend-caps'), BUDGET_REFUSAL)).toBe(true)
    expect(operationOf(document)).toBe('budget.show')
    const reports = fake.calls.filter((call) => call.name === 'runReport').length

    retry?.click()
    await flush()
    expect(
      fake.calls.filter((call) => call.name === 'showBudget'),
      'the press did not read the section it stands in',
    ).toHaveLength(2)
    expect(
      fake.calls.filter((call) => call.name === 'runReport'),
      'the caps’ Retry read the report as well',
    ).toHaveLength(reports)
    // The read is out and the section says so, rather than sitting on the
    // sentence as though nothing were happening.
    expect(sectionOf(document, 'spend-caps').getAttribute(BUSY_ATTRIBUTE)).toBe('true')

    answers[1]?.release({ ok: true, data: { document: {} } })
    await flush()
    expect(sectionOf(document, 'spend-caps').getAttribute('data-available')).toBe('true')
    expect(holdsText(sectionOf(document, 'spend-caps'), BUDGET_REFUSAL)).toBe(false)
    expect(classed(sectionOf(document, 'spend-caps'), RETRY_CLASS)).toEqual([])
  })

  it('starts `budget show` and the report together, and neither waits for the other', async () => {
    const caps = holding()
    const report = holding()
    const fake = spendBridge({ showBudget: () => caps.reply(), runReport: () => report.reply() })
    const document = await openSpend(fake.bridge)

    // Both reads were made in the frame the tab was pressed in, and neither has
    // answered: a screen that asked for the report and then for the caps would
    // have one call here and not two. They are the **last** two in the log: the
    // prewarm queue's own report read is ahead of them (PW1), and the visit's
    // caps read is the only `budget show` this case makes.
    const started = fake.calls
      .filter((call) => call.name === 'showBudget' || call.name === 'runReport')
      .map((call) => call.name)
    expect(started.filter((name) => name === 'showBudget')).toHaveLength(1)
    expect(started.slice(-2)).toEqual(['showBudget', 'runReport'])

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

    // The prewarm queue read this view at boot too (PW1), so the two reads this
    // case is about -- the visits' own -- are the last two the bridge holds.
    const older = reads.count() - 2
    reads.release(older, { ok: true, data: { document: reportDoc('2026-10-01') } })
    await flush()
    expect(report.getAttribute(BUSY_ATTRIBUTE), 'a read from an earlier visit ended the current one’s').toBe('true')
    expect(report.getAttribute(BUSY_BUSY_ATTRIBUTE)).toBe('true')
    expect(theSpendView(document), 'a view for a visit that is over was drawn').toEqual([])
    await vi.advanceTimersByTimeAsync(READING_STILL_MS)
    expect(classed(report, BUSY_STILL_CLASS), 'the fifteen-second line was cancelled with it').toHaveLength(1)

    reads.release(older + 1, { ok: true, data: { document: reportDoc('2026-10-01') } })
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

// ---------------------------------------------------------------------------
// The rail's spend beside the caps feed, and the caps a feed never finished
// (RS5, review follow-up b).
//
// RS5 moved two reads onto `budget.stream`, and that feed carries the
// repositories' rows and the project's kill switch and **neither** figure the
// rail and the fleet gauge draw -- so the call site makes the console's own
// `budget show` read for those two beside the feed (design section 4.1: the
// rail's spend is the console's **own** budget read, and no field is added to
// the feed for it). And a feed is a read **arriving**: the rows it has written
// so far are that read's, never a view this session holds, so a tab left
// mid-feed has nothing to draw as the caps on the next visit (R1: never a
// partial list drawn as the list).
// ---------------------------------------------------------------------------

/** capsFeedLine is one line of the caps feed: RS1 wraps a document's own row in
 * the line (`{"type":"row","row":{...}}`), as the Coordinator's jobs feed does
 * with its runs. */
function capsFeedLine(row: Readonly<Record<string, unknown>>): Readonly<Record<string, unknown>> {
  return { type: 'row', row }
}

/** CAPS_FEED_ROW is one repository's row of `budget show --stream`: one element
 * of the document's `repos`, unchanged (`internal/cli/budget_stream.go`). */
const CAPS_FEED_ROW: Readonly<Record<string, unknown>> = {
  slug: 'github/acme/app',
  repo: 'acme/app',
  daily_cap_usd: 25,
  per_run_cap_usd: 4,
  counted_usd: 3.5,
  headroom_usd: 21.5,
  kill: null,
}

/** CAPS_FEED is that feed: one repository's row, and the line that ends it. */
const CAPS_FEED: Readonly<Record<string, unknown>> = {
  lines: [capsFeedLine(CAPS_FEED_ROW), { type: 'end', version: 1, count: 1 }],
}

/** streamed lets one chunk of a feed land. A feed crosses the bridge as one
 * promise, so what makes its rows arrive one at a time is `readStream`'s own
 * tick (`STREAM_ROWS_PER_PAINT`), which is the host's `setTimeout(step, 0)` and
 * this suite's fake clock. */
async function streamed(): Promise<void> {
  await vi.advanceTimersByTimeAsync(10)
  await flush()
}

describe('the rail’s spend beside the caps feed', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('fills the rail’s spend and the gauge from the console’s own read where the Spend caps feed answered', async () => {
    // The table is the feed's, and the two figures are not in it: the rail and
    // the gauge draw the project's counted dollars and its counters, which
    // `budget.stream` does not carry -- so the call site makes the console's own
    // `budget show` read for them beside the feed.
    const budget = { global: { daily_cap_usd: 40, counted_usd: 10 } }
    const fake = spendBridge({
      showBudget: () => ({ ok: true, data: { document: budget } }),
      runReport: () => ({ ok: true, data: { document: reportDoc('2026-10-01') } }),
    })
    const streams: unknown[] = []
    fake.bridge['streamBudget'] = (args: unknown): unknown => {
      streams.push(args)
      return { ok: true, data: { document: CAPS_FEED } }
    }
    const document = await openSpend(fake.bridge)
    await streamed()
    // The non-vacuity guard: the caps section drew the **feed's** row, so this
    // is not the whole-document fallback filling the two figures on its way.
    expect(textIn(sectionOf(document, 'spend-caps'), 'spend-cap-repo-name')).toBe('acme/app')
    expect(streams).toHaveLength(1)
    // And both figures are filled, from the document the console read for them.
    expect(spanOf(document, RAIL_SPEND_CLASS).textContent).toBe('$10.00')
    const gauge = classed(document.body, GAUGE_CLASS)[0] as FakeNode
    expect(classed(gauge, GAUGE_CAP_CLASS)[0]?.textContent).toBe('$40.00')
    expect(
      fake.calls.filter((call) => call.name === 'showBudget'),
      'the console made more than the one read the two figures need',
    ).toHaveLength(1)
  })

  it('fills them the same way where the Project tab’s caps feed answered', async () => {
    // The Project tab reads the same feed for the same three panels, and the
    // two figures are the same document's: the one read at the call site serves
    // both screens.
    const budget = { global: { daily_cap_usd: 40, counted_usd: 10 } }
    const fake = spendBridge({ showBudget: () => ({ ok: true, data: { document: budget } }) })
    const streams: unknown[] = []
    fake.bridge['streamBudget'] = (args: unknown): unknown => {
      streams.push(args)
      return { ok: true, data: { document: CAPS_FEED } }
    }
    const document = await boot(fake.bridge, new FakeDocument())
    buttonFor(document, 'projects').click()
    await flush()
    await streamed()
    expect(streams, 'the tab never asked its own caps feed').toHaveLength(1)
    expect(spanOf(document, RAIL_SPEND_CLASS).textContent).toBe('$10.00')
    const gauge = classed(document.body, GAUGE_CLASS)[0] as FakeNode
    expect(classed(gauge, GAUGE_CAP_CLASS)[0]?.textContent).toBe('$40.00')
    expect(fake.calls.filter((call) => call.name === 'showBudget')).toHaveLength(1)
  })

  it('holds no caps a feed was still writing when the operator left the tab', async () => {
    // A tab left mid-feed answers `dropped` at the next chunk (`readStream`),
    // and the rows it had already drawn are that read's and not the session's:
    // what the next visit draws under its own mark must not be the partial list
    // as the held caps (R1 -- a partial list is never drawn as the list).
    const fake = spendBridge({
      runReport: () => ({ ok: true, data: { document: reportDoc('2026-10-01') } }),
    })
    const partial: Readonly<Record<string, unknown>> = {
      slug: 'github/acme/partial',
      repo: 'acme/partial',
      daily_cap_usd: 25,
      counted_usd: 3.5,
      kill: null,
    }
    const streams: ReturnType<typeof holding>[] = []
    fake.bridge['streamBudget'] = (): unknown => {
      const answer = holding()
      streams.push(answer)
      return answer.reply()
    }
    const document = await openSpend(fake.bridge)
    streams[0]?.release({
      ok: true,
      data: {
        document: {
          lines: [
            capsFeedLine(partial),
            capsFeedLine(CAPS_FEED_ROW),
            { type: 'end', version: 1, count: 2 },
          ],
        },
      },
    })
    await flush()
    // The first row is drawn while the feed is still arriving, one tick before
    // the second -- which is the state this case leaves the tab in.
    expect(
      holdsText(sectionOf(document, 'spend-caps'), 'acme/partial'),
      'the feed drew no first row: the case proves nothing',
    ).toBe(true)

    buttonFor(document, 'live').click()
    await streamed()
    buttonFor(document, 'spend').click()
    await flush()
    // This visit's own read is out, and what is on screen is its mark over what
    // the session holds -- which is nothing: the abandoned feed's rows were
    // never the session's caps.
    expect(streams).toHaveLength(2)
    expect(busyLabels(document)).toEqual(['Reading the budget caps…'])
    const caps = sectionOf(document, 'spend-caps')
    expect(holdsText(caps, 'acme/partial')).toBe(false)
    expect(classed(caps, 'spend-cap-repo-name')).toEqual([])
    streams[1]?.release({ ok: false, reason: 'this case is not about the second answer' })
    await flush()
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
    // The last two are the read the visit made and the one the refresh made: the
    // first of the three is the prewarm queue's own (PW1), made at boot.
    expect(
      fake.calls
        .filter((call) => call.name === 'runReport')
        .map((call) => (call.args as { readonly since?: unknown }).since)
        .slice(-2),
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
    // The mark is up in the click's own frame, over the caps this session
    // already has: the read itself is RS5's streamed one, whose whole-document
    // fallback (`budget show`, this bridge's only caps read) is one gap behind
    // the operation it asks first.
    expect(busyLabels(document)).toEqual(['Reading the budget caps…'])
    await flush()
    expect(
      fake.calls.filter((call) => call.name === 'showBudget'),
      'the budget was not read again on the visit',
    ).toHaveLength(2)
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
    await flush()
    const before = fake.calls.filter((call) => call.name === 'showBudget').length

    const refresh = classed(panelOf(document), STAMP_REFRESH_CLASS)[0]
    expect(refresh, 'the stamp drew no refresh control: the case proves nothing').toBeDefined()
    refresh?.click()
    // The mark goes up in the click's own frame, over the caps the session has.
    expect(sectionOf(document, 'spend-caps').getAttribute(BUSY_ATTRIBUTE)).toBe('true')
    // The caps read is RS5's streamed one: it asks `budget.stream` first and
    // reaches `budget show` (this bridge's only caps read, one gap later) when
    // the operation answers nothing.
    await flush()
    expect(
      fake.calls.filter((call) => call.name === 'showBudget').length,
      'the stamp’s Refresh read the report and left the caps as they were',
    ).toBe(before + 1)
    await flush()
  })
})

// ---------------------------------------------------------------------------
// The guard each tab's own read owns (console 0.116.1, review follow-up a).
//
// Every screen reads through the same bridge and the operator may leave while a
// read is out. Turning off the check that drops a late answer -- `if
// (stale(mine)) return` -- used to fail nothing for the Graph: the drawing is
// what the operator sees, and only a case that leaves the tab *before* the
// answer lands can tell a guarded read from an unguarded one.
// ---------------------------------------------------------------------------

/** liveFrame is the frame the Live screen is drawn from, so a case that moves
 * to Live is looking at the Live screen and not at a refusal it would have to
 * tell apart from the answer it is waiting for. */
function liveFrame(): Readonly<Record<string, unknown>> {
  return syntheticDocuments()[0] as Readonly<Record<string, unknown>>
}

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
    // one that draws its frame in the click's frame (Projects).
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
    // The plan is on this console's cache already (the prewarm queue read it,
    // PW1), and a cached plan is drawn **after** the layout answers -- so the
    // window this case is about is the one where the worker is holding a layout
    // and the panel holds no canvas yet.
    expect(HeldWorker.waiting, 'the Graph asked for no layout: the case proves nothing').toBeGreaterThan(0)
    expect(
      classed(panelOf(document), 'graph-canvas'),
      'the layout answered before the operator left: the case proves nothing',
    ).toEqual([])
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

  it('never draws the map over the tab the operator moved to', async () => {
    // The Coordinator's own late answer (CG7b). The tab is one section over one
    // read, and the read is the slow one -- a database round trip -- so the
    // operator is most likely to be inside it when they move on. The floor is
    // set so the read is made at all (see the mock at the top of this file):
    // with the real constant the tab spawns nothing and this case would prove
    // nothing.
    //
    // **What fails here is the pair of guards**, `coordinatorRead.settle(token)`
    // and `stale(mine)` in `loadCoordinatorMap`, with the drawing's own
    // `stale(mine)` behind them: any of the three alone drops this answer, and
    // the assertion below is about the answer never reaching the panel the
    // operator is looking at.
    floor.value = MAP_FLOOR
    const map = holding()
    const { bridge } = graphBridge(
      { subscribeLive: () => snapshotReply(liveFrame()), orchestratorMap: () => map.reply() },
      FakeWorker,
      { ...anOkCompatibility(), cliVersion: MAP_VERSION },
    )
    const { document, chosen } = await leftBehind(bridge, 'coordinator')
    map.release({ ok: true, data: { document: COORDINATOR_MAP } })
    await flush()
    expect(panelOf(document).children[0], 'a late map replaced the view on screen').toBe(chosen)
    expect(classed(panelOf(document), COORDINATOR_NODE_CLASS), 'a late map drew its boxes on another tab').toEqual(
      [],
    )
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
    // The prewarm queue read the plan at boot too (PW1), and its read is the one
    // before this visit's: the case releases the visit's, and it is the read the
    // return draws from.
    const mine = plans.count()
    buttonFor(document, 'graph').click()
    await flush()
    plans.release(mine, planStatusReply())
    await flush()
    expect(
      classed(panelOf(document), 'graph-canvas').length,
      'the first read drew no canvas: the case proves nothing',
    ).toBeGreaterThan(0)
    const atFirst = reads

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
    expect(reads, 'the return read the plan again instead of drawing what it held').toBe(atFirst)
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

/** A_LICENCE_REPLY is one `licence.read` answer (S2.7; `LicenceReply` in
 * `src/main/licence-store.ts`): the state the verifier answered for the key this
 * installation holds, and the claim set a screen may draw. It is **not** wrapped
 * in a `Snapshot`, and that is the difference this suite has to drive: the two
 * `licence.*` operations spawn no child, so the surface answers the reply itself
 * and the entry hands it on through `replyData`. */
const A_LICENCE_REPLY: Readonly<Record<string, unknown>> = Object.freeze({
  state: 'grace',
  claims: {
    tier: 'team',
    features: ['entitlements', 'fleet'],
    seats: 4,
    organisation: 'acme.example',
    issued: 1_767_225_600,
    expires: 1_798_761_600,
  },
})

/** LICENCE_REFUSAL is the verifier's own sentence for a key that does not
 * verify, which `licence.save` answers a refused paste with. */
const LICENCE_REFUSAL = 'the licence key’s signature does not verify'

/** PASTED_KEY is what the operator types into the box. It is deliberately a
 * sentence and not a key: the section never draws what was pasted and no answer
 * carries it, so no case here needs a key-shaped string to say so. */
const PASTED_KEY = 'the key the operator pasted'

/** A_SEATS_REPLY is one `seats.read` answer (S4.2; `fugaro seats --json`): five
 * people over the licence's own four seats, which is what makes the section
 * warn. The licence below is `A_LICENCE_REPLY`, whose claim set carries four,
 * so the two documents are read against each other -- and neither is written
 * anywhere: the estimate is the panel's own arithmetic over a document the CLI
 * printed for it. */
const A_SEATS_REPLY: Readonly<Record<string, unknown>> = Object.freeze({
  document: {
    project: 'acme',
    days: 90,
    since: '2026-08-13',
    until: '2026-11-11',
    people: [
      { person: 'ana', sources: ['launcher', 'requester'] },
      { person: 'bo', sources: ['launcher'] },
      { person: 'cyd', sources: ['operator'] },
      { person: 'dee', sources: ['operator'] },
      { person: 'eli', sources: ['requester'] },
    ],
    not_counted: [],
    unreadable: 0,
    notes: [],
  },
})

/** OFFERED_REPO is the one repository identity the open installation offers: the
 * `<provider>/<owner>/<name>` string the launch form's `select` holds as an
 * option's value, and the value the form sends. */
const OFFERED_REPO = 'github/owner/repo-a'

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
      'listModels',
      'readSettings',
      'managedStatus',
      'policyShow',
      'environmentReport',
      'readLicence',
      'readSeats',
      // The caps read is RS5's streamed one (`budget.stream`), and this bridge
      // has no such operation: the whole-document read it falls back to is one
      // gap behind the operation the click asks first. Every one of these is in
      // the click's own turn; none of them waited for another's answer.
      'showBudget',
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
    // sanitizer's own class: the address is not a credential, so it is drawn as
    // the budget document wrote it (D3213).
    expect(classed(panelOf(document), 'projects-kill-by')[0]?.textContent).toBe('operator@example.test')
  })

  it('starts the models and settings reads with the tab’s others and lets none of them wait', async () => {
    // The reads of this tab are independent documents: `models ls` is offline
    // and cheap and does not wait on `budget show`, and neither waits on
    // `settings --json` (the settings-edit plan's C1) nor on the licence key's
    // own read (S2.7). All of them go out in the click's own turn -- nothing is
    // awaited before the later calls -- which is what the assertion on the order
    // says.
    const { bridge, calls } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(liveFrame()),
        showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
        listModels: () => ({ ok: true, data: { document: MODELS_DOC } }),
        readSettings: () => ({ ok: true, data: { document: SETTINGS_DOC } }),
      },
    })
    const document = await boot(bridge, new FakeDocument())
    // A bridge that **can** be asked the caps feed's operation (C-OPS's
    // `budget.stream`, which RS5 reads through): the read asks this first and
    // only reaches `budget show` -- the whole document -- when it answers
    // nothing, which is what the refusal here makes it do.
    const streams: unknown[] = []
    bridge['streamBudget'] = (args: unknown): unknown => {
      streams.push(args)
      return { ok: false, reason: 'the console did not read budget.stream in this case' }
    }
    const before = calls.length
    buttonFor(document, 'projects').click()
    // **Nothing is awaited**: the click's own turn is where the caps read's own
    // operation is asked, beside the four whole-document calls -- so a tab that
    // serialised its reads would show fewer than these five here. The licence
    // key's read is the fourth of those (S2.7) even though it is the one read
    // here that spawns no child: it is a read of the console's own store, and it
    // starts with the others rather than behind them. The caps read's fallback
    // is the fifth call and it is one gap later, which is `readStream`'s own
    // shape for every screen that reads a feed: the operation first, the whole
    // document where the operation cannot be asked.
    expect(streams, 'the caps read did not ask its own operation in the click’s turn').toHaveLength(1)
    expect(
      calls
        .slice(before)
        .map((call) => call.name)
        .filter(
          (name) =>
            name === 'showBudget' ||
            name === 'listModels' ||
            name === 'readSettings' ||
            name === 'readLicence' ||
            name === 'readSeats',
        ),
    ).toEqual(['listModels', 'readSettings', 'readLicence', 'readSeats'])
    // While all nine are out, the tab says which read each of its marks is --
    // one mark per section, on the section's own host (CM10's four siblings are
    // in the middle, the licence key's the eighth, S4.2's seat estimate the
    // ninth, and the policy panel's read wears no mark of its own: it is a
    // member of the body's model, drawn with it).
    expect(busyLabels(document)).toEqual([
      PROJECTS_BUSY,
      PROJECTS_MODELS_BUSY,
      PROJECTS_SETTINGS_BUSY,
      MANAGED_BUSY,
      ACCOUNT_KEYS_BUSY,
      RETIREMENTS_BUSY,
      CREDENTIALS_SERVICE_BUSY,
      LICENCE_BUSY,
      SEATS_BUSY,
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

  it('hands the row’s own key to the operation on a pencil press, and draws no control of its own in the row', async () => {
    // SU2b's red step, and the whole of the press: the pencil is a control that
    // **opens main's window** (`settings.editor.open`, SU1, D2902), and the
    // entry draws no box, no submit and no cancel into the row any more. What
    // leaves the renderer is the row's own key and nothing else -- no value, no
    // subcommand, no layer and no argv (`SettingsEditorOpenRequest`) -- and what
    // comes back is the CLI's own sentence, appended to the `li` the pencil sits
    // in so the key, its scope and the value a run gets stay on the screen
    // around it.
    const asked: SettingsEditorOpenRequest[] = []
    const sent: unknown[] = []
    const stale =
      'layers/project.yaml changed since it was read: nothing was written; read it again and repeat the edit'
    const { bridge } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(liveFrame()),
        showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
        listModels: () => ({ ok: true, data: { document: MODELS_DOC } }),
        readSettings: () => ({ ok: true, data: { document: SETTINGS_DOC } }),
      },
    })
    bridge['openSettingsEditor'] = (args: SettingsEditorOpenRequest): unknown => {
      asked.push(args)
      const answer: SettingsEditorOutcome = { ok: false, sentence: stale }
      return { ok: true, data: answer }
    }
    // The old door is still registered in main (`settings.edit` stays until a
    // follow-up removes it) and the renderer must not reach it: this member
    // fails the case if it is ever called.
    bridge['editSetting'] = (args: unknown): unknown => {
      sent.push(args)
      return { ok: true, data: { ok: true, sentence: 'wrote' } }
    }
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'projects').click()
    await flush()

    const pencil = classed(panelOf(document), SETTINGS_PENCIL_CLASS)[0] as FakeNode
    expect(pencil, 'the settings table drew no pencil').toBeDefined()
    const row = pencil.closest('li') as FakeNode
    expect(row, 'the pencil is not inside a row').not.toBeNull()

    pencil.click()
    await flush()

    // The key, and only the key: a value here would be a box this console drew,
    // and a subcommand or a layer would be this console choosing where a write
    // lands.
    expect(asked, 'the press asked for something other than the row’s key').toEqual([
      { key: 'budget.per_run_usd' },
    ])
    expect(sent, 'the press still called settings.edit').toEqual([])

    // **The red step.** The row holds no control of this file's own: no box to
    // type into, and no button beside the pencil that opened the window. The
    // window is main's, and the renderer draws nothing for it.
    const WRITERS = new Set(['INPUT', 'TEXTAREA', 'SELECT', 'FORM'])
    expect(
      everyNode(row).filter((node) => WRITERS.has(node.tagName)),
      'the press drew a control of its own in the row',
    ).toEqual([])
    const controls = everyNode(row).filter((node) => node.tagName === 'BUTTON')
    expect(controls, 'the row drew a control beside its pencil').toHaveLength(1)
    expect(controls[0]).toBe(pencil)

    // The answer is drawn **in the row**, as an announced region, and it is the
    // CLI's own sentence: a refusal is drawn the way the launch form's outcome
    // region draws one, with the row it is about still around it.
    const outcome = classed(row, SETTINGS_EDIT_OUTCOME_CLASS)[0] as FakeNode
    expect(outcome, 'the press drew no outcome line').toBeDefined()
    expect(outcome.getAttribute('aria-live')).toBe('polite')
    expect(outcome.textContent).toBe(stale)
    expect(holdsText(row, 'budget.per_run_usd'), 'the row lost its own key').toBe(true)

    // D1116: the flow ends with the keyboard back on the control that opened the
    // window rather than on the window that closed.
    expect(document.activeElement, 'the answer dropped the keyboard').toBe(pencil)
  })

  it('draws a write’s own sentence and reads the Project tab again at once (R2)', async () => {
    // F17, the defect R2 answers: the three writes of this console left every
    // tab showing the figures it had read **before** them. The row the operator
    // wrote is one of those figures, so the scope the visit registered is
    // dropped and read again -- the body's read and the settings section's own
    // -- and the CLI's account of the write stays on the screen until that read
    // lands.
    const second = holding()
    const written = {
      ...SETTINGS_DOC,
      settings: [
        { ...(SETTINGS_DOC.settings[0] as unknown as Record<string, unknown>), value: '25' },
        SETTINGS_DOC.settings[1],
      ],
    }
    let reads = 0
    const { bridge, calls } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(liveFrame()),
        showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
        listModels: () => ({ ok: true, data: { document: MODELS_DOC } }),
        readSettings: () => {
          reads += 1
          return reads === 1 ? { ok: true, data: { document: SETTINGS_DOC } } : second.reply()
        },
      },
    })
    bridge['openSettingsEditor'] = (): unknown => {
      const answer: SettingsEditorOutcome = {
        ok: true,
        sentence: 'wrote layers/project.yaml (backup at layers/project.yaml.bak)',
      }
      return { ok: true, data: answer }
    }
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'projects').click()
    await flush()
    expect(calls.filter((call) => call.name === 'readSettings')).toHaveLength(1)

    /** countOf is how many times this case has read one bridge member. */
    const countOf = (name: string): number => calls.filter((call) => call.name === name).length
    // The sections a settings write **cannot** move: the reread is the body's and
    // the settings section's own, and never `start()`.
    const UNMOVED = [
      'listModels',
      'managedStatus',
      'policyShow',
      'environmentReport',
      'readLicence',
      'readSeats',
    ]
    const unmoved = UNMOVED.map(countOf)

    const pencil = classed(panelOf(document), SETTINGS_PENCIL_CLASS)[0] as FakeNode
    const row = pencil.closest('li') as FakeNode
    pencil.click()
    await flush()

    // Both reads are **at once** and before any second press: the body's, whose
    // cache entry is the one the write moved, and the settings section's own,
    // whose every row is a figure of the document the write changed.
    expect(calls.filter((call) => call.name === 'showBudget')).toHaveLength(2)
    expect(calls.filter((call) => call.name === 'readSettings'), 'the write left the settings section as it was').toHaveLength(2)
    expect(UNMOVED.map(countOf), 'a section a settings write cannot move was read again').toEqual(unmoved)

    // The figures on the screen are still the ones from before the write -- that
    // read is out -- and the sentence under the row is the CLI's account of it.
    expect(valueUnderPencil(document), 'the pre-write figure is not on the screen').toBe('5')
    const outcome = classed(row, SETTINGS_EDIT_OUTCOME_CLASS)[0] as FakeNode
    expect(outcome.textContent).toBe('wrote layers/project.yaml (backup at layers/project.yaml.bak)')

    second.release({ ok: true, data: { document: written } })
    await flush()

    // The read the write asked for is what the operator is left reading: the row
    // carries the value the CLI now reports, and the sentence went with the row
    // it was appended to.
    expect(valueUnderPencil(document), 'the write’s own read did not reach the row').toBe('25')
    expect(classed(panelOf(document), SETTINGS_EDIT_OUTCOME_CLASS)).toEqual([])
  })

  it('draws the operation’s own gap sentence for a bridge that cannot answer, and no claim about a write', async () => {
    // `newOpsFrom` turns a bridge with no member for the operation into the
    // contract's own `not-available` gap (`new-ops.ts`): a console whose
    // operation is unregistered -- AL's stub, until I-OPS swaps it -- draws a
    // read this build cannot make, and the row never says a write happened.
    const { bridge, calls } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(liveFrame()),
        showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
        listModels: () => ({ ok: true, data: { document: MODELS_DOC } }),
        readSettings: () => ({ ok: true, data: { document: SETTINGS_DOC } }),
      },
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'projects').click()
    await flush()

    const pencil = classed(panelOf(document), SETTINGS_PENCIL_CLASS)[0] as FakeNode
    const row = pencil.closest('li') as FakeNode
    const reads = calls.filter((call) => call.name === 'readSettings').length

    pencil.click()
    await flush()

    expect(classed(row, SETTINGS_EDIT_OUTCOME_CLASS)[0]?.textContent).toBe(NEW_OPS_NOT_AVAILABLE)
    // A press that answered nothing is not a write: nothing is read again for
    // it, and no second press is made on the operator's behalf.
    expect(calls.filter((call) => call.name === 'readSettings')).toHaveLength(reads)
  })

  it('draws an answer this build cannot read as the contract’s own sentence, not as a write', async () => {
    // The third gap: something answered, and it is not the shape the contract
    // gives this operation (`settingsEditorAt` refuses an `ok` that is not a
    // boolean), so the row says the console could not read the answer rather
    // than drawing a sentence about a write nobody can vouch for.
    const { bridge } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(liveFrame()),
        showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
        listModels: () => ({ ok: true, data: { document: MODELS_DOC } }),
        readSettings: () => ({ ok: true, data: { document: SETTINGS_DOC } }),
      },
    })
    bridge['openSettingsEditor'] = (): unknown => ({ ok: true, data: { ok: 'yes', sentence: 'wrote' } })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'projects').click()
    await flush()

    const pencil = classed(panelOf(document), SETTINGS_PENCIL_CLASS)[0] as FakeNode
    const row = pencil.closest('li') as FakeNode
    pencil.click()
    await flush()

    expect(classed(row, SETTINGS_EDIT_OUTCOME_CLASS)[0]?.textContent).toContain(NEW_OPS_UNREADABLE)
  })

  it('draws a refusal’s own words, keeps one outcome line per row, and never retries the edit', async () => {
    // D1112: the CLI's compare-and-swap refuses an edit built on bytes that
    // moved, and the row draws that sentence. It is **not** retried -- a silent
    // second write would be this console deciding an edit it was told not to
    // make -- and the way to ask again is the pencil itself, which is the
    // operator's press and not this file's decision.
    const asked: SettingsEditorOpenRequest[] = []
    const { bridge, calls } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(liveFrame()),
        showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
        listModels: () => ({ ok: true, data: { document: MODELS_DOC } }),
        readSettings: () => ({ ok: true, data: { document: SETTINGS_DOC } }),
      },
    })
    bridge['openSettingsEditor'] = (args: SettingsEditorOpenRequest): unknown => {
      asked.push(args)
      return { ok: false, reason: 'permission denied: this account may not write the project layer' }
    }
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'projects').click()
    await flush()

    const pencil = classed(panelOf(document), SETTINGS_PENCIL_CLASS)[0] as FakeNode
    const row = pencil.closest('li') as FakeNode
    const reads = calls.filter((call) => call.name === 'readSettings').length

    pencil.click()
    await flush()
    expect(classed(row, SETTINGS_EDIT_OUTCOME_CLASS)[0]?.textContent).toBe(
      'permission denied: this account may not write the project layer',
    )
    expect(asked, 'the refusal was followed by a second edit').toHaveLength(1)

    // A second press is the operator asking again, and it leaves **one**
    // sentence under the row: two answers to one question, the older one first,
    // is a row nobody can read.
    pencil.click()
    await flush()
    expect(asked).toHaveLength(2)
    const outcomes = classed(row, SETTINGS_EDIT_OUTCOME_CLASS)
    expect(outcomes, 'the second press left two answers under one row').toHaveLength(1)
    expect(outcomes[0]?.textContent).toBe(
      'permission denied: this account may not write the project layer',
    )
    // Neither press wrote anything, so neither read the tab again.
    expect(calls.filter((call) => call.name === 'readSettings')).toHaveLength(reads)
  })

  it('draws the verifier’s own state for the key this installation holds, and re-reads the section on a paste', async () => {
    // S2.7, the whole of the console's half. The read's answer is the verifier's
    // -- a state word and a claim set -- and it arrives as the reply itself
    // rather than as a `Snapshot` around a CLI document, because `licence.read`
    // spawns no child: the console keeps the key in its own store.
    //
    // What leaves the renderer on a paste is **one member, the text the box
    // held**. The operation takes no path, no installation and no flag
    // (`licenceKey`, `src/main/ipc.ts`), and the key is never in an argv: the
    // store keeps it and the child's environment is the one door out
    // (`licenceKeyFor`, `src/main/index.ts`) -- the other half of that claim is
    // `licence-store.test.ts`'s argv case, one layer down.
    //
    // And a save that worked **re-reads the section**: what an operator is left
    // looking at is the verifier's answer about the key that was just kept, not
    // the form's own claim about it. The case holds the second read open so both
    // states are asserted rather than raced.
    const sent: unknown[] = []
    const second = holding()
    let reads = 0
    const { bridge, calls } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(liveFrame()),
        showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
        listModels: () => ({ ok: true, data: { document: MODELS_DOC } }),
        readSettings: () => ({ ok: true, data: { document: SETTINGS_DOC } }),
        readLicence: () => {
          reads += 1
          return reads === 1 ? { ok: true, data: A_LICENCE_REPLY } : second.reply()
        },
      },
    })
    bridge['saveLicence'] = (args: unknown): unknown => {
      sent.push(args)
      return { ok: true, data: A_LICENCE_REPLY }
    }
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'projects').click()
    await flush()

    const panel = classed(panelOf(document), PROJECTS_LICENCE_CLASS)[0] as FakeNode
    expect(panel, 'the tab drew no licence section').toBeDefined()
    expect(holdsText(panel, LICENCE_TITLE)).toBe(true)
    // The state in the verifier's own terms, and the claims a reader of the key
    // is shown: the organisation, the tier, the seats and the two days.
    expect(holdsText(panel, 'in grace')).toBe(true)
    expect(holdsText(panel, 'acme.example')).toBe(true)
    expect(holdsText(panel, 'team')).toBe(true)
    expect(holdsText(panel, LICENCE_HINT)).toBe(true)
    expect(busyLabels(document)).toEqual([])

    typeValue(classed(panel, LICENCE_BOX_CLASS)[0] as FakeNode, PASTED_KEY)
    ;(classed(panel, LICENCE_SUBMIT_CLASS)[0] as FakeNode).click()
    await flush()

    expect(sent).toEqual([{ key: PASTED_KEY }])
    // The write answered and the section is reading again: the sentence about
    // the write is drawn while the verifier's answer to it is still out, and the
    // mark is the section's own.
    expect(reads).toBe(2)
    expect(busyLabels(document)).toEqual([LICENCE_BUSY])
    expect(holdsText(panel, LICENCE_SAVED_WORD)).toBe(true)
    // The box is emptied: what it held is kept now, and a credential left in a
    // text field is a credential on screen for as long as the tab is open.
    expect((classed(panel, LICENCE_BOX_CLASS)[0] as unknown as { value: string }).value).toBe('')
    expect(holdsText(panelOf(document), PASTED_KEY), 'the pasted key is on screen').toBe(false)

    second.release({ ok: true, data: { ...A_LICENCE_REPLY, state: 'valid' } })
    await flush()
    const repainted = classed(panelOf(document), PROJECTS_LICENCE_CLASS)[0] as FakeNode
    expect(holdsText(repainted, 'valid')).toBe(true)
    expect(busyLabels(document)).toEqual([])
    expect(classed(repainted, LICENCE_SUBMIT_CLASS), 'the box did not come back').toHaveLength(1)
    expect(calls.filter((call) => call.name === 'readLicence')).toHaveLength(2)
  })

  it('draws the verifier’s own sentence for a paste it refuses, and keeps the text the operator typed', async () => {
    // A key that does not verify is refused and written nowhere (S2.7's store,
    // and its own case), and the sentence the operator reads is the verifier's
    // own: a refusal is the remedy, and a paraphrase would take it away.
    // Nothing is re-read -- nothing was kept -- and the box keeps what was
    // typed, so a mistyped paste is corrected rather than typed again.
    const sent: unknown[] = []
    const { bridge, calls } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(liveFrame()),
        showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
        listModels: () => ({ ok: true, data: { document: MODELS_DOC } }),
        readSettings: () => ({ ok: true, data: { document: SETTINGS_DOC } }),
        readLicence: () => ({ ok: true, data: { state: 'none' } }),
      },
    })
    bridge['saveLicence'] = (args: unknown): unknown => {
      sent.push(args)
      return { ok: false, reason: LICENCE_REFUSAL }
    }
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'projects').click()
    await flush()

    const panel = classed(panelOf(document), PROJECTS_LICENCE_CLASS)[0] as FakeNode
    // An installation with no key is a state and not an error, and the box is
    // the way out of it -- the section is never gated, which is what makes a
    // lapse something an operator can walk out of.
    expect(holdsText(panel, 'no key')).toBe(true)
    const box = classed(panel, LICENCE_BOX_CLASS)[0] as FakeNode
    typeValue(box, PASTED_KEY)
    ;(classed(panel, LICENCE_SUBMIT_CLASS)[0] as FakeNode).click()
    await flush()

    expect(sent).toEqual([{ key: PASTED_KEY }])
    expect(holdsText(panel, LICENCE_REFUSAL)).toBe(true)
    expect((box as unknown as { value: string }).value).toBe(PASTED_KEY)
    // The mark is down and the control is offered again: a refusal is something
    // to act on, and one read was made because nothing was kept.
    expect(busyLabels(document)).toEqual([])
    expect(calls.filter((call) => call.name === 'readLicence')).toHaveLength(1)
  })

  it('draws the advisory seat estimate beside the key’s own seats, and warns over without blocking', async () => {
    // S4.2, the whole of the console's half. The estimate and the key's own
    // seats are two documents read by two sections of one tab, and the panel is
    // where they meet: it finds the seats claim `buildLicence` drew rather than
    // reading the key itself, so the tab is never at risk of one section
    // disagreeing with another about what the licence carries.
    //
    // What the estimate is drawn *against* is the point of the section: a
    // installation over its seats says so and stops there. **Nothing is
    // blocked** -- there is no control under this panel at all, and the section
    // takes no view of what the operator may launch (D439: seats are compared,
    // never enforced).
    const { bridge, calls } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(liveFrame()),
        showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
        listModels: () => ({ ok: true, data: { document: MODELS_DOC } }),
        readSettings: () => ({ ok: true, data: { document: SETTINGS_DOC } }),
        readLicence: () => ({ ok: true, data: A_LICENCE_REPLY }),
        readSeats: () => ({ ok: true, data: A_SEATS_REPLY }),
      },
    })
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'projects').click()
    await flush()

    const panel = classed(panelOf(document), PROJECTS_SEATS_CLASS)[0] as FakeNode
    expect(panel, 'the tab drew no seats section').toBeDefined()
    expect(holdsText(panel, SEATS_TITLE)).toBe(true)
    // Five people, four seats: the estimate is marked as compute is (`est.`) and
    // the key's own count is the licence's, drawn as the measured number it is.
    expect(holdsText(panel, 'est. 5')).toBe(true)
    expect(holdsText(panel, '4')).toBe(true)
    expect(holdsText(panel, SEATS_OVER_WARNING)).toBe(true)
    expect(holdsText(panel, SEATS_ADVISORY)).toBe(true)
    // The read happened, and the panel is the only place it is drawn: no mark is
    // left up, because the section's own read landed.
    expect(calls.filter((call) => call.name === 'readSeats')).toHaveLength(1)
    expect(busyLabels(document)).toEqual([])
    // The one case that would make this section a gate, and it is not:
    // no button, no input, no link.
    expect(
      everyNode(panel).filter((node) =>
        ['button', 'input', 'select', 'textarea', 'form', 'a'].includes(node.tagName.toLowerCase()),
      ),
    ).toEqual([])
  })

  it('asks for no seats read at all below the command’s floor, and says which version it needs', async () => {
    // `fugaro seats` landed in v0.21.0 (`SEATS_CLI_FLOOR`), and every CLI above
    // this console's supported set is read as one it may ask -- so a 0.20.x
    // installation is a CLI this tab draws its full dashboard for and one that
    // answers `unknown command "seats"` for this read, which the panel would draw
    // as a broken estimate. The floor is asked **before** the read goes out.
    const { bridge, calls } = fakeBridge({
      compatibility: { ...anOkCompatibility(), cliVersion: '0.20.9' },
      replies: {
        subscribeLive: () => snapshotReply(liveFrame()),
        showBudget: () => ({ ok: true, data: { document: BUDGET_DOC } }),
        listModels: () => ({ ok: true, data: { document: MODELS_DOC } }),
        readSettings: () => ({ ok: true, data: { document: SETTINGS_DOC } }),
        readLicence: () => ({ ok: true, data: A_LICENCE_REPLY }),
        readSeats: () => ({ ok: true, data: A_SEATS_REPLY }),
      },
    })
    const document = await boot(bridge, new FakeDocument())
    const before = calls.length
    buttonFor(document, 'projects').click()
    await flush()
    expect(
      calls.slice(before).some((call) => call.name === 'readSeats'),
      'the tab asked a CLI below the floor for `seats`',
    ).toBe(false)
    const panel = classed(panelOf(document), PROJECTS_SEATS_CLASS)[0] as FakeNode
    expect(panel, 'the tab drew no seats section').toBeDefined()
    expect(holdsText(panel, `${SEATS_CLI_FLOOR_WORD}0.20.9`)).toBe(true)
    // No figure behind a read that was never made, no mark left up for one, and
    // no table of people either.
    expect(classed(panel, SEATS_PEOPLE_CLASS)).toEqual([])
    expect(classed(panel, SEATS_VALUE_CLASS)).toEqual([])
    expect(busyLabels(document)).toEqual([])
    // The rest of the tab is untouched by it: one section below its floor takes
    // no other panel down with it.
    expect(classed(panelOf(document), PROJECTS_LICENCE_CLASS).length).toBe(1)
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
    expect(lists.length, 'the console made no run-list read at all: the case proves nothing').toBeGreaterThan(0)
    buttonFor(document, 'graph').click()
    await flush()
    expect(classed(panelOf(document), 'graph-canvas').length).toBeGreaterThan(0)
    // The Graph's own join is the last read of this kind the console made -- the
    // prewarm queue read the run list at boot as well (PW1), and Live's own join
    // is in flight beside it -- and it is the one the operator leaves behind.
    const join = lists.length - 1

    buttonFor(document, 'live').click()
    await flush()
    const chosen = panelOf(document).children[0] as FakeNode
    expect(chosen.attributes.get('class'), 'the operator never left the Graph: the case proves nothing').toBe(
      'live',
    )

    lists[join]?.release(runsReply())
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
    // The prewarm queue read the plan at boot too (PW1): the visit's read is the
    // one after it, and the two presses' follow that.
    const mine = plans.count()
    buttonFor(document, 'graph').click()
    await flush()
    plans.release(mine, planStatusReply())
    await flush()
    expect(classed(panelOf(document), 'graph-canvas').length).toBeGreaterThan(0)

    const refresh = classed(panelOf(document), 'graph-refresh')[0]
    expect(refresh, 'the Graph drew no Refresh control: the case proves nothing').toBeDefined()
    refresh?.click()
    refresh?.click()
    // The first press's read answers with a refusal, and it is nobody's read any
    // more: the picture stays, the mark stays up for the read the operator asked
    // for last, and no refusal screen is drawn.
    plans.release(mine + 1, { ok: false, reason: GRAPH_REFUSAL })
    await flush()
    expect(operationOf(document), 'a read the operator had replaced drew its refusal').toBe(null)
    expect(holdsText(panelOf(document), GRAPH_REFUSAL)).toBe(false)
    expect(
      classed(panelOf(document), 'graph-canvas').length,
      'a read the operator had replaced took the canvas down',
    ).toBeGreaterThan(0)
    expect(busyLabels(document)).toEqual([GRAPH_BUSY])

    // The read the operator asked for last is drawn when it lands.
    plans.release(mine + 2, planStatusReply())
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

// ---------------------------------------------------------------------------
// The move's renderer half (GV3.2, design section 5.1 rule 14).
//
// `graph-move.test.ts` holds the flow and the sheet, `ipc.test.ts` and
// `spawn.test.ts` hold the fence's main-process half. **What is driven here is
// the wiring between them**, which nothing else can reach: the drag on a record
// task's row and the drop on a release box, the row's own "Move…" button, the
// select of boxes, the three buttons, Escape, the re-read a move the record
// applied is followed by, and the flow dropped when the operator switches
// installation. A regression here is a version of the screen that draws a
// confirmation and sends something else -- or nothing at all, which is what a
// `<select>` drawn on its first box sent before the placeholder was added.
// ---------------------------------------------------------------------------

const MOVE_NODE = 'coordinator:gv/GV3.2'
const MOVE_TASK = 'GV/GV3.2'
const MOVE_LOOSE_NODE = 'coordinator:gv/GV4.9'
const MOVE_LOOSE_TASK = 'GV/GV4.9'
const MOVE_BOX = 'a'.repeat(64)
const MOVE_OTHER = 'b'.repeat(64)

/** moveGraphDocument is one `graph --json` document with the record's own members
 * (GV4.1): two boxes of one stream, one task placed in the first and one in no
 * box at all -- which is what draws the lane's unassigned group, the second drop
 * target -- decoded by the console's own decoder, since a case here drives the
 * document a real read answers with. */
function moveGraphDocument(): Record<string, unknown> {
  return {
    version: GRAPH_DOCUMENT_VERSION,
    stream_id: '',
    seq: 0,
    generated_at: '2026-11-12T10:30:00Z',
    sources: [{ source: 'coordinator', state: 'up', as_of: null, reason: '' }],
    releases: [
      { key: MOVE_BOX, stream: 'gv', id: 'rel-1', name: '0.12.0', state: 'open', order: 1 },
      { key: MOVE_OTHER, stream: 'gv', id: 'rel-2', name: '0.13.0', state: 'planned', order: 2 },
    ],
    nodes: [
      {
        key: MOVE_NODE,
        source: 'coordinator',
        kind: 'task',
        id: MOVE_TASK,
        title: 'drag, preview, confirm, undo',
        repo: '',
        state: 'ready',
        release: MOVE_BOX,
        updated: null,
        plan: 'GV/GV.md',
        stream: 'gv',
      },
      {
        key: MOVE_LOOSE_NODE,
        source: 'coordinator',
        kind: 'task',
        id: MOVE_LOOSE_TASK,
        title: 'the open edges',
        repo: '',
        state: 'ready',
        release: '',
        updated: null,
        plan: 'GV/GV.md',
        stream: 'gv',
      },
    ],
    edges: [],
    warnings: [],
  }
}

/** moveAnswer is one `roadmap.move` answer as the source writes one: the argv
 * main built out of the document, the task and the two sides. The words are the
 * main process's own, which is why this case may spell them here. */
function moveAnswer(
  options: { readonly to?: string; readonly status?: string | null } = {},
): Record<string, unknown> {
  const to = options.to ?? MOVE_OTHER
  return {
    argv: [
      '/opt/fugaro/bin/fugaro',
      'coordinator',
      'roadmap',
      'move',
      '--json',
      MOVE_TASK,
      '--to',
      to,
      '--expect',
      MOVE_BOX,
      '--project',
      'acme',
    ],
    task: { key: MOVE_NODE, id: MOVE_TASK, title: 'drag, preview, confirm, undo' },
    from: { key: MOVE_BOX, name: '0.12.0' },
    to: to === '' ? null : { key: to, name: '0.13.0' },
    refusal: null,
    outcome:
      options.status === null || options.status === undefined
        ? null
        : { status: options.status, request: 'req-1', reason: null, current: null },
    sentence: null,
  }
}

/** moveBridge is the fake bridge the move's cases are driven with: the Graph
 * opened on the coordinator scope -- the plan read refuses for want of a
 * checkout, which is H9's own fallback and the same door the scope cases above
 * use -- with the record's document answered for that scope, and
 * `moveRoadmapTask` recorded in the order it was called. `replies` is what the
 * write answers, one per call; a call past the list is a refusal naming the
 * case, so a case that sent one call too many fails on the call it did not
 * expect rather than on an assertion about `sent`. */
function moveBridge(
  replies: readonly Record<string, unknown>[],
  rows?: readonly Record<string, unknown>[],
): FakeBridge & { readonly sent: readonly Readonly<Record<string, unknown>>[] } {
  vi.stubGlobal('Worker', FakeWorker)
  // The three reads go in through the bridge's own `replies`, so each of them
  // stays the recorded call `fakeBridge` wrapped it in: `calls` is what says a
  // move that applied was followed by a re-read, and a case that installed its
  // own functions over the top would be reading a list nothing writes to.
  const reads: Readonly<Record<string, AReply>> = {
    planStatus: () => ({ ok: false, reason: NO_PLAN_CHECKOUT }),
    graphRead: () => graphReply(moveGraphDocument()),
    graphWatch: () => graphReply(moveGraphDocument()),
  }
  const base =
    rows === undefined
      ? graphBridge(reads)
      : railBridge(
          [...rows],
          {
            chooseInstallation: () => ({
              ok: true,
              data: { rows: [...rows].reverse().map((row) => ({ ...row, open: row['open'] !== true })) },
            }),
          },
          undefined,
          reads,
        )
  const sent: Record<string, unknown>[] = []
  base.bridge['moveRoadmapTask'] = (args: unknown): unknown => {
    sent.push(args as Record<string, unknown>)
    return replies[sent.length - 1] ?? { ok: false, reason: 'this case gave the move no answer' }
  }
  return { ...base, sent }
}

/** heldMoveBridge is the same bridge with the answers in the case's own hands:
 * every `moveRoadmapTask` is recorded as it is made and left unanswered until
 * `answerWith` hands it one. **That is how a preview that lands after the next
 * gesture is written at all** -- a child's answer arrives when the record says,
 * and the operators of this screen are perfectly capable of dragging a second
 * task, choosing another box, or pressing Escape while it is out. */
function heldMoveBridge(): {
  readonly bridge: Record<string, unknown>
  readonly sent: readonly Record<string, unknown>[]
  readonly answerWith: (index: number, reply: unknown) => void
} {
  const { bridge } = moveBridge([])
  const sent: Record<string, unknown>[] = []
  const waiting: ((reply: unknown) => void)[] = []
  bridge['moveRoadmapTask'] = (args: unknown): unknown => {
    sent.push(args as Record<string, unknown>)
    return new Promise((resolve) => waiting.push(resolve))
  }
  return {
    bridge,
    sent,
    answerWith: (index, reply) => {
      const resolve = waiting[index]
      if (resolve === undefined) throw new Error(`no move has been called ${index + 1} times`)
      resolve(reply)
    },
  }
}

/** scopeRow is one row of the coordinator drawing by the key it carries. */
function scopeRow(document: FakeDocument, key: string): FakeNode {
  const row = classed(panelOf(document), 'graph-scope-node').find((each) => each.attributes.get('data-key') === key)
  if (row === undefined) throw new Error(`the scope drawing holds no row for ${key}`)
  return row
}

/** moveButton is one row's own "Move…" control, by the key it is about. */
function moveButton(document: FakeDocument, key: string): FakeNode {
  const button = classed(panelOf(document), 'graph-move-open').find((each) => each.attributes.get('data-key') === key)
  if (button === undefined) throw new Error(`the drawing holds no move control for ${key}`)
  return button
}

/** movedSheet is the sheet on the drawing, and its phase. */
function movedSheet(document: FakeDocument): FakeNode | undefined {
  return classed(panelOf(document), 'graph-move')[0]
}

/** sheetWords is the command line the sheet shows, word by word -- the whole of
 * what a confirmation of it would spawn, as the drawing spells it. */
function sheetWords(document: FakeDocument): readonly string[] {
  return classed(panelOf(document), 'graph-move-word').map((each) => each.textContent ?? '')
}

/** theSelect is the sheet's own select of boxes. */
function theSelect(document: FakeDocument): FakeNode {
  const select = classed(panelOf(document), 'graph-move-target')[0]
  if (select === undefined) throw new Error('the choosing sheet drew no select')
  return select
}

/** optionValues is a select's options' values, in the drawing's order. */
function optionValues(select: FakeNode): readonly (string | null)[] {
  return everyNode(select)
    .filter((each) => each.tagName === 'OPTION')
    .map((each) => each.getAttribute('value'))
}

/** chooseBox is a person's own choice on the sheet's select: the value a browser
 * puts on the control, and the change it reports for it. */
async function chooseBox(document: FakeDocument, value: string): Promise<void> {
  const select = theSelect(document)
  select.value = value
  select.change()
  await flush()
}

/** FakeDrag is the event a browser hands a drag handler: the transfer a
 * `dragstart` writes its payload into, and the `preventDefault` a target's own
 * answer is. */
interface FakeDrag {
  readonly dataTransfer: { setData(type: string, value: string): void }
  readonly written: (readonly [string, string])[]
  readonly defaultPrevented: { value: boolean }
  preventDefault(): void
}

/** fireDrag dispatches one drag event on a drawn node and answers the event, so
 * a case can read what a transfer was handed and whether the console took the
 * event rather than leaving it to the window -- **the payload is the console's
 * own to write**, which is why the event here is handed over empty. The fake
 * document has no drag of its own (`fakeDom.ts` offers a click, a change and a
 * key), and a drag is the one gesture of this screen: the listener is on the
 * node, so the case fires it with the event a real browser would. */
function fireDrag(node: FakeNode, type: string): FakeDrag {
  const event: FakeDrag = {
    dataTransfer: { setData: (kind, value) => event.written.push([kind, value]) },
    written: [],
    defaultPrevented: { value: false },
    preventDefault: () => {
      event.defaultPrevented.value = true
    },
  }
  for (const listener of node.listeners.get(type) ?? []) {
    ;(listener as unknown as (fired: FakeDrag) => void)(event)
  }
  return event
}

describe('the move the Graph screen wires', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  /** openMoveGraph boots a console on the coordinator scope and answers its
   * document, which is the picture the move's gestures are drawn on. */
  async function openMoveGraph(bridge: Record<string, unknown>): Promise<FakeDocument> {
    const document = await openGraph(bridge)
    const row = scopeRow(document, MOVE_NODE)
    // The non-vacuity guard: the drawing is the record's, so the two gestures
    // exist on it at all.
    expect(row.attributes.get('draggable'), 'the record’s row is not draggable').toBe('true')
    expect(classed(panelOf(document), 'graph-scope-box').map((each) => each.attributes.get('data-release'))).toEqual([
      MOVE_BOX,
      MOVE_OTHER,
    ])
    return document
  }

  it('opens the sheet on the row’s own button, and the select sends the box a person chose', async () => {
    const { bridge, sent } = moveBridge([{ ok: true, data: moveAnswer() }])
    const document = await openMoveGraph(bridge)

    moveButton(document, MOVE_NODE).click()
    await flush()
    // **The keyboard's way in sends nothing by itself**: the sheet is the list of
    // boxes and nothing has been asked of the record yet.
    expect(sent).toEqual([])
    const choosing = movedSheet(document)
    expect(choosing?.attributes.get('data-phase')).toBe('choosing')
    // The control opens on its own not-yet-chosen option, and the boxes after it
    // are the record's keys with the unassign last: an option's value is an id
    // and never a word of a command line.
    const select = theSelect(document)
    expect(optionValues(select)).toEqual([MOVE_TARGET_UNCHOSEN, MOVE_BOX, MOVE_OTHER, ''])
    expect(
      everyNode(select)
        .filter((each) => each.tagName === 'OPTION')
        .map((each) => each.textContent),
    ).toEqual([MOVE_TARGET_UNCHOSEN_LABEL, '0.12.0', '0.13.0', MOVE_UNASSIGNED_WORD])

    await chooseBox(document, MOVE_BOX)
    // What left the renderer is the row's key and the box's, and the call is the
    // **preview**: `confirmed` is false, so no child was spawned for it.
    expect(sent).toEqual([{ node: MOVE_NODE, toRelease: MOVE_BOX, confirmed: false }])
    expect(movedSheet(document)?.attributes.get('data-phase')).toBe('preview')
    // The confirmation is up, drawn from the answer the main process composed --
    // and the confirming button is the only way the other half can be asked for.
    expect(classed(panelOf(document), 'graph-move-confirm')).toHaveLength(1)
    expect(classed(panelOf(document), 'graph-move-undo')).toHaveLength(0)
  })

  it('sends the confirming half only from the confirm button, and re-reads the graph after a move that applied', async () => {
    const { bridge, sent, calls } = moveBridge([
      { ok: true, data: moveAnswer() },
      { ok: true, data: moveAnswer({ status: 'applied' }) },
    ])
    const document = await openMoveGraph(bridge)
    const before = calls.filter((call) => call.name === 'graphRead').length

    moveButton(document, MOVE_NODE).click()
    await flush()
    await chooseBox(document, MOVE_BOX)
    // The preview and the write are the same two ids, and the boolean is the
    // whole of the difference: a confirmed call for a pair nobody previewed is
    // refused by the source, so this is the one shape a confirm may send.
    classed(panelOf(document), 'graph-move-confirm')[0]?.click()
    await flush()
    expect(sent).toEqual([
      { node: MOVE_NODE, toRelease: MOVE_BOX, confirmed: false },
      { node: MOVE_NODE, toRelease: MOVE_BOX, confirmed: true },
    ])
    expect(movedSheet(document)?.attributes.get('data-phase')).toBe('answered')
    // **The move applied, so the document in hand is the one from before it**:
    // the console reads the graph again, and the way back is offered over the
    // fresh read rather than over the stale one (`--expect` is what the record
    // holds now).
    expect(calls.filter((call) => call.name === 'graphRead').length).toBe(before + 1)
    expect(classed(panelOf(document), 'graph-move-undo')).toHaveLength(1)
  })

  it('sends nothing when the sheet is declined, by Cancel or by Escape', async () => {
    // Two previews, one per opening: a sheet is opened twice here, and the
    // second one is the one Escape closes.
    const { bridge, sent } = moveBridge([
      { ok: true, data: moveAnswer() },
      { ok: true, data: moveAnswer() },
    ])
    const document = await openMoveGraph(bridge)

    moveButton(document, MOVE_NODE).click()
    await flush()
    await chooseBox(document, MOVE_BOX)
    expect(sent).toHaveLength(1)
    classed(panelOf(document), 'graph-move-cancel')[0]?.click()
    await flush()
    // The request is dropped with the sheet: a second press on a button that is
    // no longer drawn, or a confirm from a key the flow no longer holds, has
    // nothing to send.
    expect(sent).toHaveLength(1)
    expect(movedSheet(document)).toBeUndefined()

    // The same for the Escape that stands for Cancel, from inside the sheet.
    moveButton(document, MOVE_NODE).click()
    await flush()
    await chooseBox(document, MOVE_BOX)
    const previews = sent.length
    movedSheet(document)?.press('Escape')
    await flush()
    expect(sent, 'the Escape sent something').toHaveLength(previews)
    expect(movedSheet(document)).toBeUndefined()
    // And the keyboard went back to the row the move was about rather than
    // nowhere: a reader who cancels with the keyboard is left on the control
    // that opened the sheet.
    expect(document.activeElement).toBe(scopeRow(document, MOVE_NODE))
  })

  it('leaves the sheet up and asks the record nothing when the placeholder is chosen', async () => {
    // **The defect the placeholder exists for.** A browser pre-selects the first
    // option of a `<select>` nobody has touched, and reports a `change` only when
    // the value differs from the one it holds -- so a control drawn on the first
    // box would swallow that box's own choice. It is drawn on a value no release
    // key can be, and choosing it is not a request.
    const { bridge, sent } = moveBridge([{ ok: true, data: moveAnswer() }])
    const document = await openMoveGraph(bridge)
    moveButton(document, MOVE_NODE).click()
    await flush()
    await chooseBox(document, MOVE_TARGET_UNCHOSEN)
    expect(sent).toEqual([])
    expect(movedSheet(document)?.attributes.get('data-phase')).toBe('choosing')
    // The box the browser would otherwise have reported as the value is chose-
    // able after it, which is the whole point: the first box of the drawing is
    // reachable from this control.
    await chooseBox(document, MOVE_BOX)
    expect(sent).toEqual([{ node: MOVE_NODE, toRelease: MOVE_BOX, confirmed: false }])
  })

  it('previews a drag and a drop, the unassigned group included', async () => {
    const { bridge, sent } = moveBridge([
      { ok: true, data: moveAnswer() },
      { ok: true, data: moveAnswer({ to: '' }) },
    ])
    const document = await openMoveGraph(bridge)

    // A drag on a task in a box, dropped on the other box: the two ids are the
    // row's key and the box's own `data-release`.
    const row = scopeRow(document, MOVE_NODE)
    const started = fireDrag(row, 'dragstart')
    expect(started.written, 'the drag wrote no payload into the transfer').toEqual([['text/plain', MOVE_NODE]])
    const box = classed(panelOf(document), 'graph-scope-box').find(
      (each) => each.attributes.get('data-release') === MOVE_OTHER,
    )
    if (box === undefined) throw new Error('the drawing holds no second box')
    const over = fireDrag(box, 'dragover')
    expect(over.defaultPrevented.value, 'the box did not say the drag was welcome').toBe(true)
    const dropped = fireDrag(box, 'drop')
    expect(dropped.defaultPrevented.value).toBe(true)
    await flush()
    expect(sent).toEqual([{ node: MOVE_NODE, toRelease: MOVE_OTHER, confirmed: false }])
    classed(panelOf(document), 'graph-move-cancel')[0]?.click()
    await flush()

    // And the unassigned group is a target like any other, whose key is the empty
    // string the operation declares for it (D2114) -- never a word of the argv.
    const loose = classed(panelOf(document), 'graph-scope-unassigned')[0]
    if (loose === undefined) throw new Error('the drawing holds no unassigned group')
    fireDrag(scopeRow(document, MOVE_NODE), 'dragstart')
    fireDrag(loose, 'drop')
    await flush()
    expect(sent.at(-1)).toEqual({ node: MOVE_NODE, toRelease: '', confirmed: false })
  })

  it('offers the way back as the inverse move, previewed like any other', async () => {
    const { bridge, sent } = moveBridge([
      { ok: true, data: moveAnswer() },
      { ok: true, data: moveAnswer({ status: 'applied' }) },
      { ok: true, data: moveAnswer({ to: MOVE_BOX }) },
    ])
    const document = await openMoveGraph(bridge)
    moveButton(document, MOVE_NODE).click()
    await flush()
    await chooseBox(document, MOVE_OTHER)
    classed(panelOf(document), 'graph-move-confirm')[0]?.click()
    await flush()

    classed(panelOf(document), 'graph-move-undo')[0]?.click()
    await flush()
    // Undo is the same operation with the two boxes exchanged, and it is a
    // **preview**: the way back is drawn and confirmed like any other move, so
    // nothing ran here.
    expect(sent.at(-1)).toEqual({ node: MOVE_NODE, toRelease: MOVE_BOX, confirmed: false })
    expect(movedSheet(document)?.attributes.get('data-phase')).toBe('preview')
  })

  it('drops a preview made against one installation when the operator switches', async () => {
    // **`forgetLive`'s `moveFlow = null`.** The request a confirm would send and
    // the answer an undo inverts are both about the record the console just left,
    // so the flow goes with the screen. The listener of the button a case held on
    // to is still attached to the node -- exactly as a keyboard press on a
    // detached control is -- and it finds a flow that holds nothing.
    const { bridge, sent } = moveBridge([{ ok: true, data: moveAnswer() }], [
      listRow('acme', { open: true }),
      listRow('orbit'),
    ])
    const document = await openMoveGraph(bridge)
    moveButton(document, MOVE_NODE).click()
    await flush()
    await chooseBox(document, MOVE_BOX)
    const confirm = classed(panelOf(document), 'graph-move-confirm')[0]
    expect(sent).toHaveLength(1)

    rowNamed(document, 'orbit').click()
    await flush()
    confirm?.click()
    await flush()
    expect(sent, 'a preview made against the old installation was confirmed after the switch').toHaveLength(1)
  })

  it('never draws a preview that landed after the next gesture, and confirms the move the sheet shows', async () => {
    // **The defect a late answer is.** A preview is a child, so its answer lands
    // when the record answers rather than when the operator stopped; two gestures
    // can therefore be in flight at once, and the earlier one's answer drawn over
    // the later one's sheet is a confirmation that runs a move nobody saw. The
    // two here are the two the sheet is reachable by: a drag onto no release, and
    // the row's own button with the other box chosen from the list.
    const { bridge, sent, answerWith } = heldMoveBridge()
    const document = await openMoveGraph(bridge)
    const loose = classed(panelOf(document), 'graph-scope-unassigned')[0]
    if (loose === undefined) throw new Error('the drawing holds no unassigned group')
    fireDrag(scopeRow(document, MOVE_NODE), 'dragstart')
    fireDrag(loose, 'drop')
    await flush()
    moveButton(document, MOVE_NODE).click()
    await flush()
    await chooseBox(document, MOVE_OTHER)
    expect(sent).toEqual([
      { node: MOVE_NODE, toRelease: '', confirmed: false },
      { node: MOVE_NODE, toRelease: MOVE_OTHER, confirmed: false },
    ])

    // The gesture in hand answers first: the sheet is the move onto the other
    // box, drawn from the main process's own answer.
    const shown = moveAnswer()
    answerWith(1, { ok: true, data: shown })
    await flush()
    expect(movedSheet(document)?.attributes.get('data-phase')).toBe('preview')
    expect(sheetWords(document)).toEqual(shown['argv'])

    // The drag's own answer lands after it -- the unassign, a move onto no
    // release at all. Drawn, the sheet would show `--to ''` while the flow held
    // the request the second preview drew, which is exactly the reported case:
    // the sheet showed one box and the click sent the other.
    answerWith(0, { ok: true, data: moveAnswer({ to: '' }) })
    await flush()
    expect(sheetWords(document), 'the sheet was redrawn from the answer to the gesture before it').toEqual(
      shown['argv'],
    )

    // And the confirmation is what the sheet draws, on the same two ids.
    classed(panelOf(document), 'graph-move-confirm')[0]?.click()
    await flush()
    expect(sent.at(-1), 'the confirmation sent a move the sheet did not draw').toEqual({
      node: MOVE_NODE,
      toRelease: MOVE_OTHER,
      confirmed: true,
    })
  })

  it('never puts the sheet back up after an Escape that was answered while the record was being asked', async () => {
    // The same drop from the other side, and the second half of the report: the
    // Escape drops the request, so the sheet that a late answer would put back up
    // is one offering a confirming button for a request the flow no longer holds
    // -- an inert "Move it" over a row the keyboard has already gone back to.
    const { bridge, sent, answerWith } = heldMoveBridge()
    const document = await openMoveGraph(bridge)
    moveButton(document, MOVE_NODE).click()
    await flush()
    await chooseBox(document, MOVE_BOX)
    expect(movedSheet(document)?.attributes.get('data-phase')).toBe('reading')

    movedSheet(document)?.press('Escape')
    await flush()
    expect(movedSheet(document)).toBeUndefined()

    answerWith(0, { ok: true, data: moveAnswer() })
    await flush()
    expect(movedSheet(document), 'the sheet came back after the Escape that closed it').toBeUndefined()
    expect(sent).toEqual([{ node: MOVE_NODE, toRelease: MOVE_BOX, confirmed: false }])
  })

  it('re-reads the drawing after a move the record refused, as well as after one it applied', async () => {
    // **A refusal is not a reason to keep the picture.** The refusal this screen
    // shows is the record saying the release it holds is not the one the document
    // in hand names, so the drawing behind the sheet, the sheet's own `in` field
    // and the `--expect` of the next move are all about a record that has moved.
    // The source drops its floor for that answer, and this is the read that makes
    // the drop count.
    const { bridge, calls } = moveBridge([
      { ok: true, data: moveAnswer() },
      { ok: true, data: moveAnswer({ status: 'refused' }) },
    ])
    const document = await openMoveGraph(bridge)
    const before = calls.filter((call) => call.name === 'graphRead').length
    moveButton(document, MOVE_NODE).click()
    await flush()
    await chooseBox(document, MOVE_BOX)
    classed(panelOf(document), 'graph-move-confirm')[0]?.click()
    await flush()

    expect(movedSheet(document)?.attributes.get('data-phase')).toBe('answered')
    expect(calls.filter((call) => call.name === 'graphRead').length, 'the refused move drew no fresh graph').toBe(
      before + 1,
    )
    // A refused move is no move to undo: the way back is offered for what the
    // record applied and nothing else.
    expect(classed(panelOf(document), 'graph-move-undo')).toHaveLength(0)
  })
})

// ---------------------------------------------------------------------------
// The Graph canvas read before its merge state (IR4, D3217 as amended).
//
// The tab's first paint is the plan **without** merge state -- `plan status
// --json --all --no-merges`, the read that spawns no `gh` and therefore no
// pull-request lookup at all -- and the plain `plan status --json --all` follows
// as the tab's own merge section, decorating the merged nodes in the drawing
// already on screen (a refusal there is a sentence in the canvas, never a
// refusal screen: the canvas the first read answered is true). That flag is
// newer than the oldest CLI this console supports, so it sits behind
// `PLAN_STATUS_NO_MERGES_CLI_FLOOR`, which is `null` on this tree: below it the
// tab makes the one read it has always made and its document carries its own
// merge state. These cases set the floor (see the mock at the top of this file)
// and drive both branches.
// ---------------------------------------------------------------------------

describe('the Graph canvas read before its merge state', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  /** mergesGraphBridge is `graphBridge` for an installation whose CLI is above
   * the flag's floor, clicked through to a drawn canvas. `replies` are the
   * plan's own two answers: the canvas read (`--all --no-merges`) first, the
   * merge read (`--all`) after it. */
  function mergesGraphBridge(replies: Readonly<Record<string, AReply>>): FakeBridge {
    floor.merges = MERGES_FLOOR
    return graphBridge(replies, FakeWorker, { ...anOkCompatibility(), cliVersion: MERGES_VERSION })
  }

  /** withMergesRead is the fixture with its first task merged: the same graph
   * the plain read answers with, where `task-one` -- done, with a ready pull
   * request its run recorded -- is closed and carries no mark. It is the canvas
   * document's own difference and nothing else, which is what makes it a
   * decoration rather than a second graph. */
  function withMergesRead(): unknown {
    const document = JSON.parse(JSON.stringify(PLAN_STATUS)) as {
      plans: { nodes: { id: string; mark?: string; closed: boolean }[] }[]
    }
    const node = document.plans[0]?.nodes.find((each) => each.id === 'task-one')
    if (node === undefined) throw new Error('the fixture no longer carries task-one')
    delete node.mark
    node.closed = true
    return document
  }

  /** mergeMarkOf is the merge mark drawn on one task's card or row: the word
   * the canvas carries while its merge state is unknown, and nothing once the
   * plain read has said it. */
  function mergeMarkOf(document: FakeDocument, task: string): string {
    const node = nodeForTask(document, task)
    if (node === undefined) throw new Error(`the graph drew no node for ${task}`)
    const mark = [...classed(node, 'graph-node-mark'), ...classed(node, 'graph-row-mark')][0]
    return mark?.textContent ?? ''
  }

  it('paints the canvas from the read that spawns no gh, and decorates it when the merge read lands', async () => {
    // **The first paint is the read with no forge lookups behind it** (D3217 as
    // amended): the canvas is drawn from `--all --no-merges` while the plain read
    // is still on its way, so a task whose merge state is unknown is on screen --
    // `done` with the `merge unknown` mark, a picture that is true -- rather than
    // a screen that waited for up to thirty-two lookups. The plain read then
    // decorates that drawing: the same task loses its mark in the picture the
    // operator is already reading.
    //
    // The canvas read here is the prewarm's -- the queue that warms this tab at
    // boot (`startPrewarm`) reads it with the same argv, so the visit itself is
    // served from the session cache -- which is why the case asserts the shape of
    // every read rather than the exact sequence of the two.
    const plain = holding()
    const { bridge, calls } = mergesGraphBridge({
      planStatus: (args) =>
        (args as { noMerges?: boolean }).noMerges === true ? planStatusReply() : plain.reply(),
    })
    const document = await openGraph(bridge)

    // Every read of the canvas names the flag -- the tab's own read and the
    // prewarm's are both this tab's first paint -- and the one read without it
    // is the merge section, made after a canvas was read. Nothing reads the
    // canvas with its merges in for a first paint.
    const reads = calls.filter((call) => call.name === 'planStatus').map((call) => call.args)
    expect(reads.filter((args) => (args as { noMerges?: boolean }).noMerges === true).length).toBeGreaterThan(0)
    expect(reads.at(-1)).toEqual({ all: true })
    expect(reads.slice(0, -1).every((args) => (args as { noMerges?: boolean }).noMerges === true)).toBe(true)
    // The canvas is up while the merge read is out, with the mark the canvas's
    // own document carries.
    expect(classed(panelOf(document), 'graph-canvas').length).toBeGreaterThan(0)
    expect(mergeMarkOf(document, 'task-one')).toBe('merge unknown')

    plain.release(planStatusReply(withMergesRead()))
    await flush()
    // The decoration landed in the drawing, not as a second one: the canvas is
    // still the canvas, and the node set is the canvas's own.
    expect(classed(panelOf(document), 'graph-canvas').length).toBeGreaterThan(0)
    expect(classed(panelOf(document), 'graph-scope-notice')).toEqual([])
    expect(mergeMarkOf(document, 'task-one'), 'the merged state did not reach the drawing').toBe('')
    expect(nodeForTask(document, 'task-one'), 'the decoration replaced the graph').toBeDefined()
  })

  it('makes the single read below the floor, whose document carries its own merge state', async () => {
    // An installation below the flag's floor gets **one** read, the one it has
    // always had: `plan status --json --all` looks the merges up itself, so the
    // canvas is painted with them and there is no merge section to wait for --
    // the tab spawns no second child and the argv never names the flag the
    // binary would refuse.
    const { bridge, calls } = graphBridge({ planStatus: () => planStatusReply(withMergesRead()) })
    const document = await openGraph(bridge)

    expect(calls.filter((call) => call.name === 'planStatus').map((call) => call.args)).toEqual([{ all: true }])
    expect(classed(panelOf(document), 'graph-canvas').length).toBeGreaterThan(0)
    expect(mergeMarkOf(document, 'task-one')).toBe('')
  })

  it('keeps one merge read for a canvas painted while one was out, and never two at once', async () => {
    // **The section has one read out at a time, and a claim is not dropped on
    // the floor.** Refresh while the merge lookups are still running paints the
    // canvas again -- a canvas with no merge state and nothing coming for it --
    // so the read the refresh asked for is kept and made when the one in flight
    // settles. Two at once would be two sets of up to thirty-two forge lookups
    // against the same graph.
    const plain = holding()
    let canvasReads = 0
    const { bridge, calls } = mergesGraphBridge({
      planStatus: (args) => {
        if ((args as { noMerges?: boolean }).noMerges === true) {
          canvasReads += 1
          return planStatusReply()
        }
        return plain.reply()
      },
    })
    const document = await openGraph(bridge)
    const painted = canvasReads

    ;(classed(panelOf(document), 'graph-refresh')[0] as FakeNode).click()
    await flush()
    // The refresh painted the canvas again -- and the section had a read out, so
    // it asked for no second one.
    expect(canvasReads).toBe(painted + 1)
    expect(calls.filter((call) => call.name === 'planStatus').map((call) => call.args)).toEqual([
      { all: true, noMerges: true },
      { all: true },
      { all: true, noMerges: true },
    ])

    plain.release(planStatusReply(withMergesRead()))
    await flush()
    // The kept read was made the moment the one in flight settled, and its
    // answer is in the drawing the refresh left.
    const mergeReads = calls.filter(
      (call) => call.name === 'planStatus' && (call.args as { noMerges?: boolean }).noMerges !== true,
    )
    expect(mergeReads).toHaveLength(2)
    expect(mergeMarkOf(document, 'task-one')).toBe('')
  })

  it('keeps the canvas and draws the sentence when the merge read is refused', async () => {
    // **A refusal of the merge state is a sentence in the drawing and never a
    // refusal screen**: the canvas on screen is the plan, read without merge
    // state, and taking it down for a fact about a colour would throw away a
    // graph the operator can read. The node keeps the mark its own document
    // carries, which is the honest answer while nothing has read the merge.
    const { bridge } = mergesGraphBridge({
      planStatus: (args) =>
        (args as { noMerges?: boolean }).noMerges === true
          ? planStatusReply()
          : ({ ok: false, reason: GRAPH_REFUSAL } as const),
    })
    const document = await openGraph(bridge)

    expect(classed(panelOf(document), 'graph-scope-notice').map((each) => each.textContent)).toEqual([GRAPH_REFUSAL])
    expect(classed(panelOf(document), 'graph-canvas').length).toBeGreaterThan(0)
    expect(mergeMarkOf(document, 'task-one')).toBe('merge unknown')
    expect(operationOf(document), 'a refused merge read is not a refusal screen').toBe(null)
  })
})

// ---------------------------------------------------------------------------
// The Coordinator's map read, above its version floor (CG7b, D3210).
//
// The tab is one section over one read -- `orchestrator map --json` -- and that
// read sits behind a version floor: the command is registered from `ee/` and no
// release carries it, so `ORCHESTRATOR_MAP_CLI_FLOOR` is `null` on this tree and
// the tab draws the sentence naming the CLI it needs and spawns nothing (the
// case above that clicks the tab on the real constant pins that state). The
// branch **above** the floor -- the mark and the skeleton in the click's own
// frame, and the refusal with its Retry -- is therefore unreachable through the
// real constant, so these cases set it (see the mock at the top of this file)
// and drive the wiring.
// ---------------------------------------------------------------------------

describe('the Coordinator’s map read, above its version floor', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  /** openCoordinator boots a console whose CLI is above the map's floor and
   * clicks the Coordinator tab, with the map read the case supplies. Nothing is
   * awaited after the click, so what the tab drew in the click's own frame is
   * what the caller sees first. `calls` comes back with the document, because
   * the drill-down's own read is what a press is asserted to have made. */
  async function openCoordinator(
    replies: Readonly<Record<string, AReply>>,
  ): Promise<{ readonly document: FakeDocument; readonly calls: FakeBridge['calls'] }> {
    floor.value = MAP_FLOOR
    const { bridge, calls } = graphBridge(
      { subscribeLive: () => snapshotReply(liveFrame()), ...replies },
      FakeWorker,
      { ...anOkCompatibility(), cliVersion: MAP_VERSION },
    )
    const document = await boot(bridge, new FakeDocument())
    buttonFor(document, 'coordinator').click()
    return { document, calls }
  }

  /** boxFor is one orchestrator's drawn box, by the id it carries: the door the
   * drill-down's own press goes through. */
  function boxFor(document: FakeDocument, id: string): FakeNode {
    const box = classed(panelOf(document), COORDINATOR_NODE_CLASS).find(
      (node) => node.getAttribute(COORDINATOR_ATTRIBUTE) === id,
    )
    if (box === undefined) throw new Error(`the map drew no box for ${id}`)
    return box
  }

  /** tasksDocument is one orchestrator's own document, as the drill-down's
   * `graph.read --source orchestrator:<id>` answers it: the graph document's own
   * shape with the one task the panel draws. `title` is what tells one
   * orchestrator's answer from another's, which is what a case about an answer
   * that was dropped reads. */
  function tasksDocument(title: string): Record<string, unknown> {
    const [first] = graphDocument()['nodes'] as readonly Record<string, unknown>[]
    return {
      ...graphDocument(),
      nodes: [{ ...first, key: `plan-1/${title}`, id: `plan-1/${title}`, title }],
    }
  }

  it('marks the section in the click’s own frame and draws the map when it lands', async () => {
    // The owner's rule (2026-10-31): a control change is answered in the frame
    // it happened in -- the mark and the shape of what is coming, with the
    // header the tab already knows above them -- and the figure replaces the
    // shape the moment the answer lands.
    const map = holding()
    const { document } = await openCoordinator({ orchestratorMap: () => map.reply() })

    expect(textIn(panelOf(document), COORDINATOR_TITLE_CLASS)).toBe(COORDINATOR_TITLE)
    expect(busyLabels(document)).toContain(COORDINATOR_BUSY)
    expect(skeletonsOf(document), 'the tab drew no shape for the figure on its way').toEqual([
      COORDINATOR_SHAPE.section,
    ])
    expect(classed(panelOf(document), COORDINATOR_NODE_CLASS), 'a box was drawn before it was read').toEqual([])

    map.release({ ok: true, data: { document: COORDINATOR_MAP } })
    await flush()
    expect(classed(panelOf(document), COORDINATOR_NODE_CLASS)).toHaveLength(2)
    expect(classed(panelOf(document), COORDINATOR_HEALTH_CLASS)).toHaveLength(1)
    expect(busyLabels(document), 'the mark outlived the answer it was for').toEqual([])
    expect(skeletonsOf(document)).toEqual([])
  })

  it('draws the CLI’s own sentence for a refused map, with a Retry that reads it again', async () => {
    // A refusal is the section's own and not the tab's: the header and the
    // source line stay up, the sentence goes under them, and the one control
    // beside it reads the map again. Both halves are pinned here -- the sentence
    // is the CLI's and not the console's, and the Retry is a real second read
    // rather than a redraw of the refusal.
    const sentence = 'the map could not be read: the store is unreachable'
    const first = holding()
    const second = holding()
    let reads = 0
    const { document } = await openCoordinator({
      orchestratorMap: () => {
        reads += 1
        return (reads === 1 ? first : second).reply()
      },
    })

    first.release({ ok: false, reason: sentence })
    await flush()
    expect(holdsText(panelOf(document), sentence)).toBe(true)
    expect(textIn(panelOf(document), COORDINATOR_TITLE_CLASS), 'the refusal took the header down with it').toBe(
      COORDINATOR_TITLE,
    )
    const retry = classed(panelOf(document), RETRY_CLASS)[0]
    expect(retry, 'a refused map drew no Retry: the case proves nothing').toBeDefined()
    expect(retry?.textContent).toBe(RETRY_LABEL)

    retry?.click()
    await flush()
    expect(reads, 'the Retry drew the refusal again instead of reading the map').toBe(2)
    second.release({ ok: true, data: { document: COORDINATOR_MAP } })
    await flush()
    expect(holdsText(panelOf(document), sentence), 'the refusal outlived the answer that replaced it').toBe(false)
    expect(classed(panelOf(document), COORDINATOR_NODE_CLASS)).toHaveLength(2)
  })

  it('opens the pressed box’s live tasks, and drops an answer for a box the operator has left', async () => {
    // The press is the entry's own listener on the drawn box, and the read it
    // makes is the scope that box names -- `graph.read --source
    // orchestrator:<id>`, spelled the way the CLI's own grammar spells it. The
    // selection is checked again after the await, because an operator can open a
    // second box while the first read is out: that answer belongs to a panel
    // which is no longer open, and it is dropped rather than drawn over the one
    // that is.
    const map = holding()
    const first = holding()
    const second = holding()
    const asked: string[] = []
    const { document, calls } = await openCoordinator({
      orchestratorMap: () => map.reply(),
      graphRead: (args: unknown) => {
        const source = (args as { readonly source: string }).source
        asked.push(source)
        return (source === 'orchestrator:repo-a' ? first : second).reply()
      },
    })
    map.release({ ok: true, data: { document: COORDINATOR_MAP } })
    await flush()

    boxFor(document, 'repo-a').click()
    await flush()
    expect(classed(panelOf(document), COORDINATOR_TASKS_CLASS), 'the press opened no panel').toHaveLength(1)
    expect(textIn(panelOf(document), COORDINATOR_TASKS_EMPTY_CLASS), 'the panel did not say its read is out').toBe(
      COORDINATOR_TASKS_BUSY,
    )
    expect(calls.filter((call) => call.name === 'graphRead')).toEqual([
      { name: 'graphRead', args: { source: 'orchestrator:repo-a' } },
    ])

    boxFor(document, 'repo-a~auth').click()
    await flush()
    expect(asked, 'the second press read no second scope').toEqual([
      'orchestrator:repo-a',
      'orchestrator:repo-a~auth',
    ])

    first.release({ ok: true, data: { document: tasksDocument('the answer for repo-a') } })
    await flush()
    expect(holdsText(panelOf(document), 'the answer for repo-a'), 'an answer landed on the box that was left').toBe(false)
    expect(textIn(panelOf(document), COORDINATOR_TASKS_EMPTY_CLASS), 'the dropped answer closed the open panel').toBe(
      COORDINATOR_TASKS_BUSY,
    )
    expect(classed(panelOf(document), COORDINATOR_TASKS_CLASS)[0]?.getAttribute(COORDINATOR_ATTRIBUTE)).toBe(
      'repo-a~auth',
    )

    second.release({ ok: true, data: { document: tasksDocument('the answer for repo-a~auth') } })
    await flush()
    expect(holdsText(panelOf(document), 'the answer for repo-a~auth'), 'the open panel drew no tasks').toBe(true)
  })

  it('says an orchestrator whose own source is not up, and never that it holds nothing', async () => {
    // The read answered and the document's own `sources[]` row for the scope is
    // not `up`: an empty `nodes[]` there is a stream nobody could read, and the
    // empty sentence would report it as an orchestrator doing nothing (H3).
    const map = holding()
    const tasks = holding()
    const { document } = await openCoordinator({
      orchestratorMap: () => map.reply(),
      graphRead: () => tasks.reply(),
    })
    map.release({ ok: true, data: { document: COORDINATOR_MAP } })
    await flush()

    boxFor(document, 'repo-a').click()
    await flush()
    tasks.release({
      ok: true,
      data: {
        document: {
          ...graphDocument(),
          nodes: [],
          sources: [
            { source: 'orchestrator:repo-a', state: 'down', as_of: null, reason: 'the stream was withdrawn' },
          ],
        },
      },
    })
    await flush()
    expect(textIn(panelOf(document), COORDINATOR_TASKS_EMPTY_CLASS)).toBe(
      `${COORDINATOR_TASKS_NOT_UP_WORD}: down — the stream was withdrawn`,
    )
    expect(holdsText(panelOf(document), COORDINATOR_TASKS_EMPTY_WORD)).toBe(false)
  })
})

describe('the tabs the console reads for the one it is showing', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('reads the other tabs after the first paint, one at a time, and stops at the switch', async () => {
    // PW1, and the wiring half of it: the queue is started by the paint and not
    // by a timer, it reads **one** tab's primary read at a time and in the
    // navigation's own order, and a tab switch is the whole of its
    // cancellation -- the read already out is the last one (`prewarm.ts` is the
    // queue itself, and its own file pins the order and the stops).
    const plan = holding()
    const { bridge, calls } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(liveFrame()),
        planStatus: () => plan.reply(),
        // The run list answers, so the Graph's read of its two documents really
        // completes after the switch: a case whose list refused would return
        // before the check it is here for and would pass with that check gone.
        listRuns: () => runsReply(),
      },
    })
    const document = await boot(bridge, new FakeDocument())
    await flush()
    const reads = (name: string): number => calls.filter((call) => call.name === name).length
    expect(reads('planStatus'), 'the first paint started no queue').toBe(1)
    expect(reads('runReport'), 'the queue is reading a second tab beside the first').toBe(0)
    expect(reads('showBudget'), 'the queue is reading a third tab beside the first').toBe(0)

    buttonFor(document, 'spend').click()
    await flush()
    plan.release(planStatusReply())
    await flush()
    // The answer is the Graph's, and the Graph is not the tab the operator is
    // on: nothing of it is cached, and the queue reads nothing further -- the
    // report and the budget in `calls` are the two the Spend visit made for
    // itself.
    expect(reads('planStatus'), 'the dropped answer was read again').toBe(1)
    expect(reads('runReport'), 'the queue read the report after the switch').toBe(1)
    expect(reads('showBudget'), 'the queue read the budget after the switch').toBe(1)

    buttonFor(document, 'graph').click()
    await flush()
    // The switch cached nothing: the Graph is cold when the operator gets there,
    // and what it draws is its own read's, with no stamp on it. This is the pair
    // that fails while `prewarmGraph`'s `still` check is gone.
    expect(reads('planStatus'), 'the switch cached the answer it dropped').toBe(2)
    expect(stamped(document), 'a dropped answer was drawn as a cached view').toBeNull()
  })

  it('drops the report the queue read for Spend, and caches nothing of it', async () => {
    // The other half of the check, one tab over: the report is the spend read
    // that answers after the switch, and the answer is not cached -- so the
    // visit reads the report itself and draws no stamp. This fails while
    // `prewarmSpend`'s `still` check is gone.
    const report = holding()
    const { bridge, calls } = fakeBridge({
      replies: {
        subscribeLive: () => snapshotReply(liveFrame()),
        planStatus: () => planStatusReply(),
        listRuns: () => runsReply(),
        runReport: () => report.reply(),
        showBudget: () => ({ ok: true, data: { document: {} } }),
      },
    })
    const document = await boot(bridge, new FakeDocument())
    await flush()
    const reads = (name: string): number => calls.filter((call) => call.name === name).length
    expect(reads('runReport'), 'the queue is not on the report: the case proves nothing').toBe(1)

    buttonFor(document, 'live').click()
    await flush()
    report.release({ ok: true, data: { document: reportDoc('2026-10-01') } })
    await flush()

    const before = reads('runReport')
    buttonFor(document, 'spend').click()
    await flush()
    expect(reads('runReport'), 'the visit drew the answer the switch dropped').toBe(before + 1)
    expect(stamped(document), 'a dropped answer was drawn as a cached view').toBeNull()
  })

  it('draws the view the queue read when the operator gets there, stamped and without a second read', async () => {
    // What the queue is for: the first switch to a tab it read draws what this
    // session already has -- the canvas, the stamp, and no `plan.status` read of
    // the visit's own.
    const { bridge, calls } = graphBridge({ subscribeLive: () => snapshotReply(liveFrame()) })
    const document = await boot(bridge, new FakeDocument())
    const plans = calls.filter((call) => call.name === 'planStatus').length
    expect(plans, 'the queue read no plan: the case proves nothing').toBeGreaterThan(0)

    buttonFor(document, 'graph').click()
    await flush()
    expect(classed(panelOf(document), 'graph-canvas').length).toBeGreaterThan(0)
    expect(
      calls.filter((call) => call.name === 'planStatus').length,
      'the visit read the plan the queue had read for it',
    ).toBe(plans)
    expect(stamped(document), 'a prewarmed view was drawn with no stamp').toMatch(/^as of \d\d:\d\d$/)
  })

  it('reads the other tabs for a console whose own frame refused, and caches nothing a read refused', async () => {
    // The first paint is the trigger whether it drew a frame or the refusal for a
    // frame it could not read: the tabs this console is not showing are
    // independent sections, and the operator whose Live feed is refused is the
    // one most likely to go looking in them. Every read of the queue's here
    // refuses, and the queue goes on to the next tab and caches nothing.
    const { bridge, calls } = fakeBridge({})
    const document = await boot(bridge, new FakeDocument())
    await flush()
    expect(holdsText(panelOf(document), 'the console did not read subscribeLive in this case')).toBe(true)
    expect(calls.filter((call) => call.name === 'planStatus'), 'a refused feed prewarmed nothing').toHaveLength(1)
    expect(calls.filter((call) => call.name === 'runReport')).toHaveLength(1)
    expect(calls.filter((call) => call.name === 'showBudget')).toHaveLength(1)

    buttonFor(document, 'graph').click()
    await flush()
    expect(calls.filter((call) => call.name === 'planStatus'), 'a refusal filled the Graph’s cache').toHaveLength(2)
    expect(stamped(document), 'a view no read answered was stamped as a cached one').toBeNull()
  })
})
