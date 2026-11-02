// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The console's project view (Bitbucket credentials T1.2; coordinator plan P6,
// docs/coordinator/implementation-plan.md, as amended by D364).
//
// The read is `fugaro coordinator status --from-store --all --json`, the one
// argv this task added to the console's surface. What this file pins is the
// three things the plan names and the one thing the decoder exists for:
//
//   - **the document** -- `decodeCoordinatorStatus` -- against documents built
//     here rather than read from a fixture, because the two states this screen
//     turns on (`stored: false`, a stale marker) are not in any stored view the
//     console's own fixtures carry;
//   - **the drawing** -- `renderCoordinator` -- through a fake factory that
//     **throws on the attributes a screen may not set** (`href`, `src`,
//     `style`, `on*`), so a screen that reached for one would fail every case
//     below rather than one, and so "a hostile title is text" is a fact about
//     the element rather than about a string comparison;
//   - **the surface** -- the one argv, exactly, with the `--project` word spawn
//     rule 3 adds to every row, and the mutation control for the pin: a spawn
//     row that drops `--from-store` is refused.
//
// **No age is claimed anywhere.** The stored view carries no time by design
// (`ee/coordinator/sweep.go`), so the screen prints the console's own
// sentence about that and the stale marker's `since` as the stamp it is --
// never a derived "5 minutes ago", which would compare two things that are not
// on the same scale.

import { randomBytes } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import { CONSOLE_OPERATIONS, validateArguments } from '../../src/main/ipc'
import { SPAWN_COMMANDS, argvFor, assertSpawnSurface } from '../../src/main/spawn'
import type { SpawnCommand } from '../../src/main/spawn'
import { PULL_REQUEST_PROVIDERS, pullRequestUrl } from '../../src/main/window'
import { putText } from '../../src/renderer/app'
import { asciiLower } from '../../src/renderer/screens/coordinator'
import { capsOf } from '../../src/renderer/screens/spend'
import {
  COORDINATOR_BLIND_STALE_WORD,
  COORDINATOR_BLIND_UNREAD_WORD,
  COORDINATOR_BLIND_WORD,
  COORDINATOR_CARRIED_WORD,
  COORDINATOR_COLLISION_WORD,
  COORDINATOR_DESCRIPTION_ALL_CLASS,
  COORDINATOR_DESCRIPTION_ALL_WORD,
  COORDINATOR_DESCRIPTION_CLASS,
  COORDINATOR_DESCRIPTION_EMPTY_CLASS,
  COORDINATOR_DESCRIPTION_EMPTY_WORD,
  COORDINATOR_DESCRIPTION_HEAD_CLASS,
  COORDINATOR_DESCRIPTION_LINES,
  COORDINATOR_DESCRIPTION_LINE_CLASS,
  COORDINATOR_DESCRIPTION_SHOW_WORD,
  COORDINATOR_COLLISIONS_WORD,
  COORDINATOR_DECLARED_WORD,
  COORDINATOR_DRAFT_WORD,
  COORDINATOR_IN_FLIGHT_COUNT_WORD,
  COORDINATOR_IN_FLIGHT_WORD,
  COORDINATOR_JOBS_FLOOR_WORD,
  COORDINATOR_JOBS_LOAD_CLASS,
  COORDINATOR_JOBS_LOAD_WORD,
  COORDINATOR_JOBS_OLDER_CLASS,
  COORDINATOR_JOBS_OLDER_WORD,
  COORDINATOR_JOBS_RUNS_WORD,
  COORDINATOR_JOBS_SPENT_WORD,
  COORDINATOR_LAUNCH_WORD,
  COORDINATOR_LIST_CAP,
  COORDINATOR_NO_AGE_WORD,
  COORDINATOR_OMITTED_WORD,
  COORDINATOR_OPEN_RUN_WORD,
  COORDINATOR_OPEN_SPEND_WORD,
  COORDINATOR_QUEUE_BUDGET_WORD,
  COORDINATOR_QUEUE_CAP_WORD,
  COORDINATOR_QUEUE_CAPS_HIDDEN_WORD,
  COORDINATOR_QUEUE_EMPTY_WORD,
  COORDINATOR_QUEUE_JOBS_WORD,
  COORDINATOR_QUEUE_JOB_WHY_WORD,
  COORDINATOR_QUEUE_JOB_WORD,
  COORDINATOR_QUEUE_KILL_WORD,
  COORDINATOR_QUEUE_NO_PLANS_WORD,
  COORDINATOR_QUEUE_PLAN_WHY_WORD,
  COORDINATOR_QUEUE_PLAN_WORD,
  COORDINATOR_QUEUE_PR_WHY_WORD,
  COORDINATOR_QUEUE_PR_WORD,
  COORDINATOR_QUEUE_REPO_KILL_WORD,
  COORDINATOR_QUEUE_WORD,
  COORDINATOR_RECORDS_WORD,
  COORDINATOR_RETRY_WORD,
  COORDINATOR_RUNS_WORD,
  COORDINATOR_STALE_WORD,
  COORDINATOR_STORED_WORD,
  COORDINATOR_TOTAL_CAP_WORD,
  COORDINATOR_TOTAL_COLLISIONS_WORD,
  COORDINATOR_TOTAL_COUNTED_WORD,
  COORDINATOR_TOTAL_IN_FLIGHT_WORD,
  COORDINATOR_TOTAL_PLANS_WORD,
  COORDINATOR_TOTAL_REPOS_WORD,
  COORDINATOR_TRANSCRIPT_MISSING_WORD,
  COORDINATOR_TRANSCRIPT_WORD,
  COORDINATOR_UNREAD_WORD,
  PULL_PROVIDERS,
  QUEUE_SOURCES,
  buildCoordinatorModel,
  coordinatorJobsControl,
  coordinatorJobsFloorNote,
  coordinatorLoadJobsControl,
  coordinatorOlderJobsControl,
  fillCoordinatorDescription,
  fillCoordinatorJobs,
  fillCoordinatorJobsHead,
  jobsDocument,
  queueRuns,
  renderCoordinator,
  renderCoordinatorFrame,
  repoDetail,
} from '../../src/renderer/screens/coordinator'
import type {
  CoordinatorInputs,
  CoordinatorQueueItem,
  CoordinatorRepoPanel,
  ScreenElement,
  ScreenFactory,
} from '../../src/renderer/screens/coordinator'
import { BUSY_SLOT_CLASS } from '../../src/renderer/section'
import { UNAVAILABLE_MARK } from '../../src/shared/availability'
import { PATH_PLACEHOLDER, TOKEN_PLACEHOLDER, sanitizeText, type TextClass } from '../../src/shared/sanitize'
import { decodeCoordinatorIntents, decodeCoordinatorStatus, decodePlanStatus } from '../../src/shared/schema'
import type { CoordinatorIntentsView, CoordinatorStatusView, PlanStatusView } from '../../src/shared/schema'
import type { JSONDoc } from '../../src/source/source'

// ---------------------------------------------------------------------------
// The harness: a fake element that refuses what a screen may not do.
// ---------------------------------------------------------------------------

/** FakeScreen is the element a screen draws into. It records text, classes and
 * attributes, and it **throws on the attributes a screen must never set**: the
 * navigating attribute, a resource, a style and any handler. A harness that
 * merely recorded an `href` would let the screen build one and leave the
 * assertion three lines below to remember to look; this one fails at the call.
 * It is `projects.test.ts`'s harness in this file's own words, because the
 * screens are siblings and a screen drawn through another's factory is a test
 * about the interface, not about the sibling. */
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
      throw new Error(`a console screen may not set the ${name} attribute`)
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

/** factory makes fresh fakes, in the shape the renderer is handed. */
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

/** textOf is a node's own text, asserted to be there. */
function textOf(node: ScreenElement, className: string): string {
  return one(node, className).textContent ?? ''
}

/** drawn is the whole tree's text, in document order. */
function drawn(node: ScreenElement): string {
  return walk(node)
    .map((leaf) => leaf.textContent ?? '')
    .filter((text) => text !== '')
    .join(' ')
}

/** said is the whole tree's text the way a browser concatenates it: every node's
 * own text, in document order, **with nothing put between them**. `drawn` joins
 * with a space, so it answers a sentence whose two spans run together as one that
 * reads -- which is the one thing a case about a sentence's spacing cannot use. */
function said(node: ScreenElement): string {
  return walk(node)
    .map((leaf) => leaf.textContent ?? '')
    .join('')
}

/** decodes is a document that must decode, or the failure is the decoder's own
 * sentence rather than an assertion three lines later. */
function decodes(raw: unknown): CoordinatorStatusView {
  const result = decodeCoordinatorStatus(raw)
  if (!result.ok) throw new Error(result.skew.because)
  return result.value
}

/** bodyOf is a row's expanded body, which is the one `coordinator-job-detail`
 * every row carries. */
function bodyOf(row: ScreenElement): ScreenElement {
  return one(row, 'coordinator-job-detail')
}

/** renders draws a document, as the entry point does: the document's own read
 * has landed and the other three have answered with nothing waiting, so a case
 * about the records and collisions is not also a case about the queue. */
function renders(raw: unknown): ScreenElement {
  return renderCoordinator(factory, buildCoordinatorModel(queueInputs({ view: decodes(raw) })), 'dark')
}

// ---------------------------------------------------------------------------
// The documents.
// ---------------------------------------------------------------------------

/** STORED is one repository the sweep reconciled: a pull request with a run
 * behind it, a collision, an unread pull request, and the sweep's stale
 * marker. */
const STORED = {
  repo: 'owner/repo-a',
  stored: true,
  view: {
    repo: 'owner/repo-a',
    records: [
      {
        pr: {
          number: 7,
          url: 'https://example.invalid/owner/repo-a/pull/7',
          title: 'a title someone wrote',
          branch: 'feat/a-thing',
          draft: true,
          state: 'open',
        },
        runs: [
          {
            id: '20261027-030000-aa01',
            pr: 7,
            status: 'running',
            outcome: '',
            terminal: false,
            branch: 'feat/a-thing',
            base_branch: 'main',
            declared_by: 'person-1',
            files: ['src/a.go'],
            files_truncated: false,
          },
        ],
        branch: 'feat/a-thing',
        release: '1.4',
        in_flight: true,
        files: ['src/a.go'],
        services: ['service-a'],
      },
    ],
    collisions: [{ a: '#7', b: '20261027-030000-aa01', files: ['src/a.go'], services: [] }],
    unread: [
      {
        number: 9,
        reason: 'the host refused the read',
        carried: true,
        failures: 2,
        retry_after: '2026-10-27T04:00:00Z',
      },
    ],
    omitted: 1,
    omitted_collisions: 2,
  },
  stale: { since: '2026-10-27T02:00:00Z', reason: 'the repository’s runs could not be read' },
}

/** UNSTORED is a repository whose database node holds no view: the runs half
 * alone, with `stored: false`. `omitted`, `omitted_collisions` and `unread` are
 * absent here on purpose -- they are the sweep's own `omitempty` keys, so
 * absent means "nothing was dropped" rather than "the console does not know". */
const UNSTORED = {
  repo: 'owner/repo-b',
  stored: false,
  view: {
    repo: 'owner/repo-b',
    records: [
      {
        runs: [
          {
            id: '20261027-031500-bb02',
            status: 'succeeded',
            outcome: 'implemented',
            terminal: true,
            files: [],
          },
        ],
        in_flight: false,
        files: [],
        services: [],
      },
    ],
    collisions: [],
  },
}

// ---------------------------------------------------------------------------
// The four documents the action queue and the fleet totals are read from (K1).
//
// They are built here rather than read from a fixture: the queue turns on
// states no stored view carries (a draft, a record with no run, a run whose
// halt block is set), and the two documents beside it are read through the
// console's own decoders -- `decodePlanStatus` and `spend.ts`'s `capsOf` --
// so a case hands the screen exactly what the entry would.
// ---------------------------------------------------------------------------

/** aRecord is one record of the stored view: a pull request and the runs behind
 * it, in the shape `ee/coordinator/view.go` writes. `runs: 0` is a pull request
 * the store holds no run for, which is a fact the queue has a rule about. */
function aRecord(options: {
  number?: number
  title?: string
  draft?: boolean
  state?: string
  inFlight?: boolean
  runs?: number
}): unknown {
  const number = options.number ?? 1458
  const runs = options.runs ?? 1
  return {
    pr: {
      number,
      url: `https://example.invalid/owner/repo-a/pull/${String(number)}`,
      title: options.title ?? 'a title someone wrote',
      branch: 'feat/a-thing',
      draft: options.draft ?? false,
      state: options.state ?? 'open',
    },
    runs: Array.from({ length: runs }, (_unused, index) => ({
      id: `20261031-101502-9f0${String(index)}`,
      pr: number,
      status: 'succeeded',
      outcome: 'implemented',
      terminal: true,
      branch: 'feat/a-thing',
      base_branch: 'main',
      declared_by: 'person-1',
      files: [],
      files_truncated: false,
    })),
    branch: 'feat/a-thing',
    release: '',
    in_flight: options.inFlight ?? false,
    files: [],
    services: [],
  }
}

/** aStore is a coordinator document: one stored repository holding the records
 * a case hands it. */
function aStore(records: readonly unknown[], options: { provider?: string; repo?: string } = {}): unknown {
  const repo = options.repo ?? 'owner/repo-a'
  return {
    repos: [
      {
        repo,
        // `provider` is C1's field on the node (D814). A store without it is
        // what a CLI older than C1 answers, which is the node K2 draws its
        // numbers as text on -- so a case that wants a button writes one, and
        // every case that does not stays a case about the older CLI.
        ...(options.provider === undefined ? {} : { provider: options.provider }),
        stored: true,
        view: { repo, records, collisions: [] },
      },
    ],
  }
}

/** aRunRow is one row of a `runs ls --json` document. `halted` writes the block
 * a person's halt leaves on the row, which is the first of the two grounds the
 * queue draws a job for. */
function aRunRow(options: {
  run?: string
  repo?: string
  status?: string
  terminal?: boolean
  halted?: boolean
  title?: string
  planTask?: string
}): unknown {
  const run = options.run ?? '20261031-101502-9f3c'
  const row: Record<string, unknown> = {
    run,
    repo: options.repo ?? 'owner/repo-a',
    run_id: `${run}-full`,
    status: options.status ?? 'failed',
    terminal: options.terminal ?? true,
    reason: options.halted === true ? 'waiting for a person' : '',
    title: options.title ?? '',
    plan_task: options.planTask ?? '',
    created: '2026-10-31T10:15:02Z',
  }
  if (options.halted === true) {
    row['halt'] = { by: 'person-1', at: '2026-10-31T10:41:00Z', reason: 'waiting for a person' }
  }
  return row
}

/** runsDocument is a `runs ls --json` document, in the CLI's own order: newest
 * first. */
function runsDocument(rows: readonly unknown[]): unknown {
  return { runs: rows }
}

/** aTask is one plan task in the state a case gives it. */
function aTask(id: string, state: string, title = 'a task somebody wrote'): unknown {
  return { id, title, state, closed: state === 'done', level: 1, critical: false, needs: [] }
}

/** aPlanStatus is a `plan status --json` document: one plan holding the nodes a
 * case hands it, on the checkout's own repository. */
function aPlanStatus(nodes: readonly unknown[]): unknown {
  return {
    repo: 'owner/repo-a',
    plans: [
      {
        id: 'p1',
        artifact: 'console-coordinator-v2',
        plan: 'console-coordinator-v2',
        stream: 'v1',
        nodes,
        releases: [],
        tasks: nodes.length,
        closed: 0,
        open: nodes.length,
      },
    ],
    totals: { plans: 1, tasks: nodes.length, closed: 0, open: nodes.length },
    streams: [],
    warnings: [],
  }
}

/** plans is the plan status the screen is handed, through the console's own
 * decoder. */
function plans(nodes: readonly unknown[]): PlanStatusView {
  const result = decodePlanStatus(aPlanStatus(nodes))
  if (!result.ok) throw new Error(result.skew.because)
  return result.value
}

/** aBudget is a `budget show --json` document: the global block and one
 * repository row, which are where the queue's alerts and the totals' money come
 * from. The defaults are a fleet under its caps and a repository under its own,
 * so a case that is not about an alert draws none. */
function aBudget(options: {
  counted?: number
  cap?: number
  fleetKilled?: boolean
  repoCounted?: number
  repoCap?: number
  repoKilled?: boolean
}): JSONDoc {
  const cap = options.cap ?? 600
  const counted = options.counted ?? 412.6
  const repoCap = options.repoCap ?? 120
  const repoCounted = options.repoCounted ?? 1.25
  const global: Record<string, unknown> = {
    daily_cap_usd: cap,
    per_run_cap_usd: 5,
    counted_usd: counted,
    spent_usd: 400,
    notional_usd: 12.6,
    headroom_usd: cap - counted,
  }
  if (options.fleetKilled === true) {
    global['kill'] = { by: 'person-1', at: '2026-10-31T10:00:00Z', reason: 'the fleet is stopped' }
  }
  const repo: Record<string, unknown> = {
    slug: 'owner-repo-a',
    repo: 'owner/repo-a',
    counted_usd: repoCounted,
    spent_usd: repoCounted,
    notional_usd: 0,
    headroom_usd: repoCap - repoCounted,
    daily_cap_usd: repoCap,
    daily_cap_source: 'repo',
    per_run_cap_usd: 0,
    per_run_cap_source: '',
  }
  if (options.repoKilled === true) {
    repo['kill'] = { by: 'person-1', at: '2026-10-31T10:00:00Z', reason: 'this repository is stopped' }
  }
  return {
    project: '<project>',
    day: '2026-10-31',
    mode: 'live',
    max_reserve_usd: 0,
    global,
    defaults: {},
    repos: [repo],
    warnings: [],
  }
}

/** aBudgetFor is `aBudget`'s own document with its one repository row renamed,
 * which is where a budget alert's one document cell comes from: an alert names
 * the repository it is about, and `repoKilled` is what makes the queue draw one
 * for it. */
function aBudgetFor(repo: string): JSONDoc {
  const document = aBudget({ repoKilled: true })
  const rows = document['repos']
  const row = Array.isArray(rows) ? (rows[0] as Record<string, unknown> | undefined) : undefined
  return { ...document, repos: [{ ...row, repo }] }
}

/** queueInputs is what the entry hands the model, with the four reads answered
 * and nothing waiting: a case names the one read it is about. A read that has
 * not answered is `null`, `checkout` is whether the installation has one at all
 * (the condition that turns the plan read off), and `capsHidden` is the reduced
 * mode's own withholding. */
function queueInputs(inputs: Partial<CoordinatorInputs>): CoordinatorInputs {
  return {
    view: decodes(aStore([])),
    runs: [],
    plans: plans([]),
    caps: capsOf(aBudget({})),
    checkout: true,
    capsHidden: false,
    // `null` is the pane's own read not having answered (K5), which is the
    // state every case that is not about the pane is in: a document here would
    // draw intent lines under assertions that are about the queue or a record.
    intents: null,
    ...inputs,
  }
}

/** drawsQueue draws the screen from the queue's own inputs, as the entry does
 * when a read lands. */
function drawsQueue(inputs: Partial<CoordinatorInputs>): ScreenElement {
  return renderCoordinator(factory, buildCoordinatorModel(queueInputs(inputs)), 'dark')
}

/** itemTexts is one class of every queue item's cells, in the order they are
 * drawn. */
function itemTexts(node: ScreenElement, className: string): readonly string[] {
  return all(node, 'coordinator-queue-item').map((item) => textOf(item, className))
}

/** figureOf is one count cell's word and its drawn figure, in the order they are
 * drawn: the two halves are drawn by `numberCell`, so a case reads them the way
 * a reader does -- together. */
function figureOf(node: ScreenElement, className: string): readonly [string, string] {
  const cell = one(node, className)
  return [textOf(cell, 'coordinator-count-word'), textOf(cell, 'coordinator-count-value')]
}

// ---------------------------------------------------------------------------
// The decoder.
// ---------------------------------------------------------------------------

describe('the stored project view’s decoder (T1.2)', () => {
  it('decodes the stored view, and keeps `stored` apart from "no records"', () => {
    // The distinction this decoder exists for: an absent node and a repository
    // the sweep reconciled to nothing both read as an empty `records`, and a
    // screen that drew the second when it held the first would claim the
    // coordinator had read a repository it never did.
    const stored = decodes({ repos: [STORED] }).repos[0]
    expect(stored?.repo).toBe('owner/repo-a')
    expect(stored?.stored).toBe(true)
    expect(stored?.view.records).toHaveLength(1)
    expect(stored?.view.records[0]?.pr?.number).toBe(7)
    expect(stored?.view.records[0]?.pr?.title).toBe('a title someone wrote')
    expect(stored?.view.records[0]?.runs[0]?.declaredBy).toBe('person-1')
    expect(stored?.view.records[0]?.runs[0]?.baseBranch).toBe('main')
    expect(stored?.view.records[0]?.inFlight).toBe(true)

    // An unstored node with an empty view is the other fact, and it is the
    // flag and not the array's length that says which.
    const empty = decodes({
      repos: [{ repo: 'owner/repo-c', stored: false, view: { repo: 'owner/repo-c', records: [], collisions: [] } }],
    }).repos[0]
    expect(empty?.stored).toBe(false)
    expect(empty?.view.records).toEqual([])

    // The sweep's three `omitempty` keys: absent is zero and an empty list, not
    // an absence the console would have to draw as unknown.
    expect(decodes({ repos: [UNSTORED] }).repos[0]?.view.omitted).toBe(0)
    expect(decodes({ repos: [UNSTORED] }).repos[0]?.view.omittedCollisions).toBe(0)
    expect(decodes({ repos: [UNSTORED] }).repos[0]?.view.unread).toEqual([])
    // ...and a key that is there is read as the number it is.
    expect(decodes({ repos: [STORED] }).repos[0]?.view.omitted).toBe(1)
    expect(decodes({ repos: [STORED] }).repos[0]?.view.omittedCollisions).toBe(2)
  })

  it('reads a stale marker exactly as stored, and asks for no age of its own', () => {
    const stale = decodes({ repos: [STORED] }).repos[0]?.stale
    // The stamp and the reason, as the sweep wrote them. `since` is a stamp and
    // not a duration: nothing here subtracts it from a clock.
    expect(stale?.since).toBe('2026-10-27T02:00:00Z')
    expect(stale?.reason).toBe('the repository’s runs could not be read')
    // Absent and null are both "this repository has no marker", so a screen has
    // one branch and not two.
    expect(decodes({ repos: [UNSTORED] }).repos[0]?.stale).toBeNull()
    const explicit = { ...UNSTORED, stale: null }
    expect(decodes({ repos: [explicit] }).repos[0]?.stale).toBeNull()
  })

  it('ignores an unknown field, so a newer CLI’s addition is not a failing read', () => {
    // The owner's ruling of 2026-10-30: a key no struct of the read writes yet
    // is a field a newer CLI added, and it is ignored -- the document decodes to
    // what it would without it -- while a document that is not an object at
    // all is still refused before anything is read from it.
    const plain = decodeCoordinatorStatus({ repos: [STORED] })
    const rogue = decodeCoordinatorStatus({ repos: [{ ...STORED, colour: 'red' }] })
    expect(plain.ok).toBe(true)
    expect(rogue).toEqual(plain)
    expect(decodeCoordinatorStatus({ repos: [STORED], download: 'https://example.invalid/x' })).toEqual(plain)
    expect(decodeCoordinatorStatus([]).ok).toBe(false)
  })

  it('refuses this document at the feed’s door, and a feed at this one', () => {
    // The fourth door: the shapes share no marker, so neither admits the
    // other's. A screen that read one as the other would draw an empty project
    // view over a live feed.
    const other = decodeCoordinatorStatus({ conn: { state: 'ok' }, runs: [] })
    expect(other.ok).toBe(false)
    if (!other.ok) expect(other.skew.refusal.name).toBe('missing')
  })
})

// ---------------------------------------------------------------------------
// The drawing.
// ---------------------------------------------------------------------------

describe('the project view screen', () => {
  it('labels a stored node as the last stored view, and an unstored one as the runs alone', () => {
    // The stored node is the sweep's view, and the console says so in its own
    // words: a node left in the database after the coordinator was turned off
    // *is* the last stored view, which is what the label claims and nothing
    // more.
    const stored = renders({ repos: [STORED] })
    expect(textOf(stored, 'coordinator-repo-state')).toBe(COORDINATOR_STORED_WORD)
    expect(drawn(stored)).toContain('the last stored view')

    const unstored = renders({ repos: [UNSTORED] })
    expect(textOf(unstored, 'coordinator-repo-state')).toBe(COORDINATOR_RUNS_WORD)
    expect(drawn(unstored)).toContain('no stored view')
    // The runs half is drawn as the runs half: the run behind it is on screen,
    // and no pull request is invented for it.
    expect(drawn(unstored)).toContain('20261027-031500-bb02')
  })

  it('claims no age for the view, and prints the stale marker’s stamp as the stamp it is', () => {
    const node = renders({ repos: [STORED] })
    // The console's own sentence, once, whatever the document held.
    expect(textOf(node, 'coordinator-age')).toBe(COORDINATOR_NO_AGE_WORD)
    // The marker's two fields, as stored. A derived age would be a second
    // rendering of `since` that the design refuses: the view beside it carries
    // no time, so the two are not on a common scale.
    expect(textOf(node, 'coordinator-stale-word')).toBe(COORDINATOR_STALE_WORD)
    expect(textOf(node, 'coordinator-stale-since')).toBe('2026-10-27T02:00:00Z')
    expect(textOf(node, 'coordinator-stale-reason')).toBe('the repository’s runs could not be read')
    // A repository with no marker draws no marker at all.
    expect(all(renders({ repos: [UNSTORED] }), 'coordinator-stale')).toEqual([])
  })

  it('draws a repository name and a pull request title as text, and creates no element from either', () => {
    // The two untrusted strings this screen draws: the repository name the CLI
    // read out of the local config, and the pull request's own words, written
    // by whoever opened it. Both are text through `putText` and nothing else --
    // no element is created from either, and no attribute of the tree carries
    // one.
    const name = `owner/${'img'}${' '}${'src'}=${'x'}`
    const title = `<${'script'}>${'alert'}(1)<${'/'}${'script'}>`
    const node = renders({
      repos: [
        {
          ...STORED,
          repo: name,
          view: {
            ...STORED.view,
            records: [
              {
                ...STORED.view.records[0],
                pr: { ...STORED.view.records[0]?.pr, title },
              },
            ],
          },
        },
      ],
    })

    // The title is drawn whole, as the text it is: the console's `sentence`
    // class keeps free text and the markup is inert because it is text.
    expect(textOf(node, 'coordinator-record-title')).toBe(title)
    // The repository name is a *validating* class: a name carrying markup is not
    // a repository, so the cell reads the unavailable mark rather than a name
    // this console cannot vouch for. Either way nothing was built from it.
    expect(textOf(node, 'coordinator-repo-name')).toBe(UNAVAILABLE_MARK)

    // And the tree: no tag and no attribute value is either string. `setAttribute`
    // would have thrown for a navigating attribute, a resource or a handler, so
    // this is the other half of the same claim.
    for (const element of walk(node)) {
      expect(element.tag).not.toContain('<')
      for (const [attribute, value] of element.attributes) {
        expect(value, `${element.tag}'s ${attribute}`).not.toContain(title)
        expect(value, `${element.tag}'s ${attribute}`).not.toContain(name)
      }
    }
  })

  it('builds no link from wire text: the document’s URL is not drawn at all, and no element carries an href', () => {
    // Design section 5.1 rule 2: the console builds no link from a document's
    // string. The pull request's `url` is not drawn at all -- a link is
    // `link.openExternal`'s, validated in the main process -- so no element of
    // this tree carries it as text, and none carries a navigating attribute, an
    // attribute value that is a URL, or a tag that could navigate.
    const node = renders({ repos: [STORED] })
    for (const element of walk(node)) {
      expect(FakeScreen.FORBIDDEN_ATTRIBUTES.has(element.tag)).toBe(false)
      for (const [attribute, value] of element.attributes) {
        expect(attribute.toLowerCase(), `${element.tag}`).not.toContain('href')
        expect(value, `${element.tag}'s ${attribute}`).not.toMatch(/^(?:https?|javascript|data):/)
      }
    }
    expect(drawn(node)).not.toContain('https://example.invalid/owner/repo-a/pull/7')
  })

  it('says so when the document selects no repository', () => {
    // **No argv this console passes prints an empty `repos`.** `--all` on a
    // config that lists no repository is refused by the CLI
    // (`coordinatorRepos`' `userErr`), and without `--all` the document is the
    // single `RepoView` and not this envelope — so what is drawn here is the
    // console's own guard rather than a state it reads: a screen that drew
    // nothing for it would be indistinguishable from one whose read failed,
    // and the guard costs a branch. It is asserted so the branch stays real
    // rather than becoming dead code nobody checked.
    const node = renders({ repos: [] })
    expect(drawn(node)).toContain('no repository')
    expect(drawn(node)).toContain('fugaro coordinator status --from-store --all --json')
    expect(all(node, 'coordinator-repo')).toEqual([])
  })

  it('draws the counts it computed, never a figure the document carried', () => {
    // The one reading on this screen is a count the console counted off the
    // arrays it was handed, so the three counts are the arrays' lengths and the
    // run's own in-flight word is drawn from the record's flag.
    //
    // The body and not the whole tree: the fleet's totals strip (K1) is drawn
    // from the same screen and counts other things -- `collisions: 0` among
    // them -- so a case about this repository's counts reads this repository's
    // panel. `fleet totals` below is where the strip's own figures are pinned.
    const node = renders({ repos: [STORED] })
    expect(drawn(node)).toContain(COORDINATOR_IN_FLIGHT_WORD)
    const body = one(node, 'coordinator-body')
    for (const element of all(body, 'coordinator-count-value')) expect(element.textContent).toBe('1')
  })
})

// ---------------------------------------------------------------------------
// The Flight Deck drawing (console redesign, step 7).
// ---------------------------------------------------------------------------
//
// The restyling, and the facts it must not lose while it is restyled: one panel
// per repository with its name and its source line at the head and its counter
// strip under them, the collisions cell marked by a word beside its tone; the
// amber stale banner and the values a bound left out; the collision callout and
// the state-unread chip; and the pull request rows with the runs nested under
// them, cut at the console's own cap with the count it did not draw.
//
// Every case below reads a hook the styling design's section 2.12 names, so the
// rules in `flight-deck.css` and this drawing are the same set of elements.

/** recordWithRuns is one record whose run list is longer than the console
 * draws: the collision lists are not the only list this screen cuts. */
function recordWithRuns(count: number): unknown {
  return {
    runs: Array.from({ length: count }, (_, at) => ({
      id: `20261027-0300${String(at % 100).padStart(2, '0')}-aa${String(at).padStart(2, '0')}`,
      status: 'running',
      outcome: '',
      terminal: false,
      files: [],
    })),
    in_flight: true,
    files: [],
    services: [],
  }
}

/** collisionWithFiles is one collision whose path list is longer than the
 * console draws. */
function collisionWithFiles(count: number): unknown {
  return {
    a: '#7',
    b: '20261027-030000-aa01',
    files: Array.from({ length: count }, (_, at) => `src/file-${String(at)}.go`),
    services: [],
  }
}

describe('the Coordinator screen in the Flight Deck look (console redesign, step 7)', () => {
  it('draws one panel per repository, with its name and its source line at the head', () => {
    const node = renders({ repos: [STORED, UNSTORED] })
    const panels = all(node, 'coordinator-repo')
    expect(panels, 'one panel per repository the document lists').toHaveLength(2)

    const heads = all(node, 'coordinator-repo-head')
    expect(heads, 'every panel opens with its own head row').toHaveLength(2)
    for (const [at, word] of [
      [0, COORDINATOR_STORED_WORD],
      [1, COORDINATOR_RUNS_WORD],
    ] as const) {
      const panel = panels[at]
      const head = heads[at]
      if (panel === undefined || head === undefined) throw new Error('a panel has no head')
      // The head is the panel's own first child: the name and which view the
      // node is, above the counts and above everything the repository lists.
      expect(panel.nodes[0], 'the head is not the panel’s first element').toBe(head)
      expect(head.nodes[0], 'the name is not the head’s first element').toBe(one(head, 'coordinator-repo-name'))
      expect(textOf(head, 'coordinator-repo-state')).toBe(word)
    }
  })

  it('draws the counter strip: three labelled cells, in the console’s own words', () => {
    const node = renders({ repos: [STORED] })
    const strip = one(node, 'coordinator-counts')
    const cells = all(strip, 'coordinator-count')
    expect(cells, 'records, in flight and collisions').toHaveLength(3)
    expect(cells.map((cell) => textOf(cell, 'coordinator-count-word'))).toEqual([
      COORDINATOR_RECORDS_WORD,
      COORDINATOR_IN_FLIGHT_COUNT_WORD,
      COORDINATOR_COLLISIONS_WORD,
    ])
    // The figures are the console's own counts off the document's arrays, one
    // reading per cell, and the word is drawn beside each of them.
    expect(cells.map((cell) => textOf(cell, 'coordinator-count-value'))).toEqual(['1', '1', '1'])
    for (const cell of cells) expect(all(cell, 'coordinator-count-value')).toHaveLength(1)
  })

  it('marks the collisions cell when there are any, by word and not by tone alone', () => {
    // The strip's one state: a repository whose records collide is a repository
    // a reader has to look at. The mark is a class on the cell and the cell's
    // own word is beside it, which is what makes the tone redundant (design
    // section 3.5) -- a greyscale reader loses the colour and not the fact.
    const collided = all(renders({ repos: [STORED] }), 'coordinator-count-alert')
    expect(collided, 'a repository with a collision marks one cell').toHaveLength(1)
    const cell = collided[0]
    if (cell === undefined) throw new Error('no alerted cell')
    expect(cell.classes).toContain('coordinator-count-collisions')
    expect(textOf(cell, 'coordinator-count-word')).toBe(COORDINATOR_COLLISIONS_WORD)
    expect(textOf(cell, 'coordinator-count-value')).toBe('1')

    // And a repository with none marks nothing: the tone follows the count the
    // document gave, and a zero is not a state.
    expect(all(renders({ repos: [UNSTORED] }), 'coordinator-count-alert')).toEqual([])
  })

  it('draws the stale marker as a banner, drawn only when the document carries one', () => {
    const node = renders({ repos: [STORED] })
    const banner = one(node, 'coordinator-repo-stale')
    expect(banner.tag, 'the banner is a block and not a line of prose').toBe('div')
    expect(textOf(banner, 'coordinator-stale-word')).toBe(COORDINATOR_STALE_WORD)
    expect(textOf(banner, 'coordinator-stale-since')).toBe('2026-10-27T02:00:00Z')
    expect(textOf(banner, 'coordinator-stale-reason')).toBe('the repository’s runs could not be read')
    // It is the repository's own banner, inside the panel it is about.
    expect(walk(one(node, 'coordinator-repo'))).toContain(banner)

    // An unstored node carries no marker, and a repository with none draws no
    // banner at all.
    expect(all(renders({ repos: [UNSTORED] }), 'coordinator-repo-stale')).toEqual([])
  })

  it('keeps the values a bound left out, beside the banner that says why', () => {
    // The document's own bound is the third thing this screen says and does not
    // draw: the sweep left records and collisions out of the stored node. It
    // stands next to the stale banner, because both are the panel saying what
    // it is not showing.
    const node = renders({ repos: [STORED] })
    const omitted = one(node, 'coordinator-omitted')
    expect(textOf(omitted, 'coordinator-omitted-word')).toBe(COORDINATOR_OMITTED_WORD)
    expect(textOf(omitted, 'coordinator-omitted-records')).toBe('1')
    expect(textOf(omitted, 'coordinator-omitted-collisions')).toBe('2')
    expect(all(renders({ repos: [UNSTORED] }), 'coordinator-omitted')).toEqual([])
  })

  it('draws a collision as a callout: the pair, the word between them and the paths', () => {
    const node = renders({ repos: [STORED] })
    const callout = one(node, 'coordinator-collision')
    expect(textOf(callout, 'coordinator-collision-a')).toBe('#7')
    expect(textOf(callout, 'coordinator-collision-word')).toBe(COORDINATOR_COLLISION_WORD)
    expect(textOf(callout, 'coordinator-collision-b')).toBe('20261027-030000-aa01')
    // A path is drawn as the placeholder and never as the path itself: this
    // console has no reason to draw one (design section 5.1 rule 3).
    expect(all(callout, 'coordinator-file').map((path) => path.textContent)).toEqual([PATH_PLACEHOLDER])
  })

  it('cuts a shared path list at the console’s own cap and draws the count it left off', () => {
    const node = renders({
      repos: [
        {
          ...STORED,
          view: { ...STORED.view, collisions: [collisionWithFiles(COORDINATOR_LIST_CAP + 3)] },
        },
      ],
    })
    const callout = one(node, 'coordinator-collision')
    expect(all(callout, 'coordinator-file')).toHaveLength(COORDINATOR_LIST_CAP)
    // The count that was not drawn is a reading like every other figure here,
    // and the paths past the cap are not in the tree at all.
    expect(textOf(callout, 'coordinator-more')).toBe('3')
  })

  it('cuts a record’s runs at the console’s own cap and draws the count it left off', () => {
    // A pull request with two hundred runs is not a row of two hundred lines:
    // the runs drawn under it are the first `COORDINATOR_LIST_CAP`, and the rest
    // is a count rather than a silent prefix.
    const node = renders({
      repos: [
        {
          ...UNSTORED,
          view: { ...UNSTORED.view, records: [recordWithRuns(COORDINATOR_LIST_CAP + 3)] },
        },
      ],
    })
    const row = one(node, 'coordinator-record')
    const runs = all(row, 'coordinator-run')
    expect(runs).toHaveLength(COORDINATOR_LIST_CAP)
    expect(textOf(row, 'coordinator-more')).toBe('3')
    // The first run the cap cut is not in the tree at all: the count above is
    // what stands for it, and there is no silent twenty-first row.
    const cut = `20261027-0300${String(COORDINATOR_LIST_CAP % 100).padStart(2, '0')}-aa${String(COORDINATOR_LIST_CAP).padStart(2, '0')}`
    expect(runs.map((run) => textOf(run, 'coordinator-run-id'))).not.toContain(cut)
  })

  it('draws a pull request as a row: its number, chip, title, branch, version and state', () => {
    const node = renders({ repos: [STORED] })
    const row = one(node, 'coordinator-record')
    const head = one(row, 'coordinator-record-head')
    expect(head.nodes[0], 'the number leads the row').toBe(one(head, 'coordinator-record-label'))
    expect(textOf(head, 'coordinator-record-label')).toBe('#7')
    expect(textOf(head, 'coordinator-record-title')).toBe('a title someone wrote')
    expect(textOf(head, 'coordinator-record-branch')).toBe('feat/a-thing')
    expect(textOf(head, 'coordinator-record-release')).toBe('1.4')
    expect(textOf(head, 'coordinator-record-flight')).toBe(COORDINATOR_IN_FLIGHT_WORD)
    // The host's own word for the pull request and the console's mark when it is
    // a draft are one chip, and both are drawn when the document says both.
    const chip = one(head, 'coordinator-record-chip')
    expect(textOf(chip, 'coordinator-record-state')).toBe('open')
    expect(textOf(chip, 'coordinator-record-draft')).toBe(COORDINATOR_DRAFT_WORD)
  })

  it('nests the runs behind a pull request under its row, and says when there is none', () => {
    const node = renders({
      repos: [
        {
          ...STORED,
          view: {
            ...STORED.view,
            records: [
              ...STORED.view.records,
              {
                pr: { number: 11, url: '', title: 'another title', branch: 'feat/b-thing', draft: false, state: 'open' },
                runs: [],
                branch: 'feat/b-thing',
                release: '',
                in_flight: false,
                files: [],
                services: [],
              },
            ],
          },
        },
      ],
    })
    const rows = all(node, 'coordinator-record')
    expect(rows).toHaveLength(2)
    const [first, second] = rows
    if (first === undefined || second === undefined) throw new Error('the two rows were not drawn')

    // The runs are the row's own nested list, not a list beside it.
    const runs = one(first, 'coordinator-runs')
    expect(walk(first)).toContain(runs)
    expect(textOf(runs, 'coordinator-run-id')).toBe('20261027-030000-aa01')

    // The second record has no run behind it, and no draft chip either: the
    // console's own sentence is drawn where the runs would be.
    expect(textOf(second, 'coordinator-record-noruns')).toBe('no run behind it')
    expect(all(second, 'coordinator-record-draft')).toEqual([])
    expect(all(second, 'coordinator-runs')).toEqual([])
  })

  it('draws a pull request whose state no sweep could read as an amber chip, with the reason', () => {
    const node = renders({ repos: [STORED] })
    const chip = one(node, 'coordinator-unread')
    expect(textOf(chip, 'coordinator-unread-word')).toBe(COORDINATOR_UNREAD_WORD)
    expect(textOf(chip, 'coordinator-unread-number')).toBe('#9')
    expect(textOf(chip, 'coordinator-unread-reason')).toBe('the host refused the read')
    expect(textOf(chip, 'coordinator-unread-carried')).toBe(COORDINATOR_CARRIED_WORD)
    // The stamp the sweep left for the retry, drawn as stored: the view carries
    // no time of its own, so this is the only clock-shaped field on the chip.
    expect(textOf(chip, 'coordinator-unread-retry')).toBe('2026-10-27T04:00:00Z')
  })

  it('keeps the header line about the age the view does not carry, above the panels', () => {
    // Header-first (0.117.0), kept: the console's sentence about the time the
    // stored view does not have is drawn once, before any repository panel.
    const node = renders({ repos: [STORED] })
    const order = walk(node)
    const age = one(node, 'coordinator-age')
    expect(age.textContent).toBe(COORDINATOR_NO_AGE_WORD)
    const panel = one(node, 'coordinator-repo')
    expect(order.indexOf(age)).toBeLessThan(order.indexOf(panel))
  })
})

// ---------------------------------------------------------------------------
// The action queue and the fleet totals (Coordinator v2, K1; D810, D812, D813,
// D819).
//
// The page opens with what is waiting for a person: a pull request open and not
// a draft with its runs finished, a run that halted or ended without
// succeeding, a plan task ready to launch, and a budget alert. One item per
// thing, one button per item, and every figure either read off one of four
// documents or absent -- never invented, and never a zero standing in for
// "unknown".
//
// What this file pins is the drawing and the model; that three of the four
// reads are started together and the jobs are read when they are asked for, and
// what the buttons do when they are clicked, is `entry-wiring.test.ts`'s.
// ---------------------------------------------------------------------------

/** itemOf is one kind's item, found by the word it draws for its kind rather
 * than by its place in the list: a case that named a kind is about that kind. */
function itemOf(node: ScreenElement, word: string): FakeScreen {
  const found = all(node, 'coordinator-queue-item').filter(
    (item) => textOf(item, 'coordinator-queue-kind') === word,
  )
  expect(found, `expected exactly one ${word} item`).toHaveLength(1)
  const first = found[0]
  if (first === undefined) throw new Error(`no ${word} item`)
  return first
}

/** holdsToPutText asserts one drawn cell holds exactly what `putText` makes of
 * the same value, in the class its wire key declares: the sanitized text, or
 * the not-available mark with the refusal's reason in the title. It is the one
 * assertion a cell drawn through `literal` cannot pass -- `literal` draws the
 * string the document wrote and writes no reason -- so it is how a case tells
 * the screen's two text paths apart. */
function holdsToPutText(cell: ScreenElement, raw: string, textClass: TextClass, field: string): void {
  const expected = new FakeScreen('span')
  putText(expected, raw, textClass, field)
  expect(
    [cell.textContent, cell.getAttribute('title')],
    `${field} was not drawn the way putText draws it`,
  ).toEqual([expected.textContent, expected.getAttribute('title')])
}

describe('the action queue (K1)', () => {
  it('draws one item per thing that is waiting, and exactly one button on each', () => {
    // The plan's red step: a document holding one open non-draft pull request,
    // one halted run row and one ready plan task draws **three** queue items,
    // each with exactly one button -- the pull request, the job, the plan task,
    // in that order.
    const node = drawsQueue({
      view: decodes(aStore([aRecord({})])),
      runs: queueRuns(runsDocument([aRunRow({ halted: true })])),
      plans: plans([aTask('C2', 'ready')]),
    })
    const items = all(node, 'coordinator-queue-item')
    expect(items).toHaveLength(3)
    for (const item of items) {
      expect(all(item, 'coordinator-queue-button'), 'an item drew more than one button').toHaveLength(1)
    }
    expect(itemTexts(node, 'coordinator-queue-kind')).toEqual([
      COORDINATOR_QUEUE_PR_WORD,
      COORDINATOR_QUEUE_JOB_WORD,
      COORDINATOR_QUEUE_PLAN_WORD,
    ])
    // The count is the console's own over the items it drew, in its own words:
    // a figure no document carries, and one that is absent rather than zero
    // while a read is still out (the case below).
    expect(textOf(node, 'coordinator-queue-count')).toBe('3')
    expect(textOf(node, 'coordinator-queue-word')).toBe(COORDINATOR_QUEUE_WORD)
    expect(textOf(node, 'coordinator-queue-empty')).toBe('')
  })

  it('draws no item for an open draft, and says nothing is waiting', () => {
    // The second half of the red step, and the difference between a queue and a
    // list of everything open: a draft is not waiting for a review.
    const node = drawsQueue({ view: decodes(aStore([aRecord({ draft: true })])) })
    expect(all(node, 'coordinator-queue-item')).toEqual([])
    expect(textOf(node, 'coordinator-queue-empty')).toBe(COORDINATOR_QUEUE_EMPTY_WORD)
    expect(textOf(node, 'coordinator-queue-count')).toBe('')
  })

  it('draws no item for a pull request nobody closed, one with no run, one still running, or a task that is not ready', () => {
    // Four records that all read "waiting for you" to a screen that only looked
    // at `pr.state`: a merged pull request, a pull request with no run behind
    // it (somebody opened it by hand), one whose run is still going, and a task
    // that is blocked or already done.
    const node = drawsQueue({
      view: decodes(
        aStore([
          aRecord({ number: 1458, state: 'merged' }),
          aRecord({ number: 1459, runs: 0 }),
          aRecord({ number: 1460, inFlight: true }),
        ]),
      ),
      runs: queueRuns(
        runsDocument([
          // A run that is still going, a terminal run that succeeded, and the
          // body's own record of a halt: none of them is waiting for a person.
          aRunRow({ run: '20261031-101502-9f3c', status: 'running', terminal: false }),
          aRunRow({ run: '20261031-114805-c1a2', status: 'succeeded' }),
          aRunRow({ run: '20261030-220114-77bd', status: 'succeeded', halted: true }),
        ]),
      ),
      plans: plans([aTask('C2', 'running'), aTask('C3', 'blocked'), aTask('C4', 'done')]),
    })
    // The halted run is the one item: a halt is a person's own act on a run
    // that then ended however it ended, so the status is not the whole ground.
    expect(itemTexts(node, 'coordinator-queue-subject')).toEqual(['20261030-220114-77bd'])
    expect(itemTexts(node, 'coordinator-queue-kind')).toEqual([COORDINATOR_QUEUE_JOB_WORD])
  })

  it('gives every item the one act its own button is, and the values a launch is prefilled with', () => {
    // Rule 11's one write is the launch, and the two buttons that reach it hand
    // it a repository and a plan task -- nothing else, and no argv. The values
    // are the model's, and `entry-wiring.test.ts` is where the click is.
    const model = buildCoordinatorModel(
      queueInputs({
        view: decodes(aStore([aRecord({ number: 1458 })])),
        runs: queueRuns(
          runsDocument([aRunRow({ run: '20261030-220114-77bd', planTask: 'C9', title: 'a run title' })]),
        ),
        plans: plans([aTask('C2', 'ready')]),
        caps: capsOf(aBudget({ repoCounted: 120, repoCap: 120 })),
      }),
    )
    // Every part's items, in the order the queue draws them: the parts are the
    // four reads, and `QUEUE_SOURCES` is that order.
    const actions = QUEUE_SOURCES.flatMap(
      (source) => model.queue[source]?.items.map((item) => item.action) ?? [],
    )
    expect(actions).toEqual([
      { kind: 'openRun', repo: 'owner/repo-a', run: '20261031-101502-9f00' },
      { kind: 'retry', repo: 'owner/repo-a', planTask: 'C9' },
      { kind: 'launch', repo: 'owner/repo-a', planTask: 'p1/C2' },
      { kind: 'spend' },
    ])
    // `Retry` and `Launch` carry exactly what rule 11's argv is given: the
    // repository and the plan task. The run id is **main's** -- it is minted in
    // the confirmation window, on Submit, and a renderer that carried one would
    // be naming a run that does not exist yet -- and the title is the operator's,
    // typed in that window. `Open run` is the one act that names a run, because
    // opening one is what it does.
    //
    // The plan task is `<plan>/<task>` and never the node alone: the CLI refuses
    // a plan task that is not shaped that way (`task.PlanTaskProblem`), and
    // `plan status`'s `plans[].id` is the id the join uses.
    const plan = model.queue.plans?.items[0]
    expect(plan?.action).toEqual({ kind: 'launch', repo: 'owner/repo-a', planTask: 'p1/C2' })
    const node = renderCoordinator(factory, model, 'dark')
    expect(all(node, 'coordinator-queue-button').map((button) => button.textContent)).toEqual([
      COORDINATOR_OPEN_RUN_WORD,
      COORDINATOR_RETRY_WORD,
      COORDINATOR_LAUNCH_WORD,
      COORDINATOR_OPEN_SPEND_WORD,
    ])
    // A pull request's item names the pull request and never offers the link:
    // the number is text, and no element anywhere carries a destination
    // (`href` is refused by the harness, and K2 is where the link arrives).
    expect(itemTexts(node, 'coordinator-queue-subject')).toEqual(['#1458', '20261030-220114-77bd', 'C2', 'owner/repo-a'])
    expect(itemTexts(node, 'coordinator-queue-why')).toEqual([
      COORDINATOR_QUEUE_PR_WHY_WORD,
      COORDINATOR_QUEUE_JOB_WHY_WORD,
      COORDINATOR_QUEUE_PLAN_WHY_WORD,
      COORDINATOR_QUEUE_CAP_WORD,
    ])
  })

  it('reads the jobs off the runs document in the CLI’s own order, and cuts the queue at the console’s cap', () => {
    // `runs ls` answers newest first and the console does not re-sort it; the
    // cut is the console's own (this tree has no `runs ls --limit`), and what it
    // did not draw is said rather than dropped silently.
    const rows = Array.from({ length: COORDINATOR_LIST_CAP + 3 }, (_unused, index) =>
      aRunRow({ run: `20261031-15150${String(index % 10)}-a${String(index).padStart(2, '0')}` }),
    )
    const model = buildCoordinatorModel(queueInputs({ runs: queueRuns(runsDocument(rows)) }))
    expect(model.queue.runs?.items).toHaveLength(COORDINATOR_LIST_CAP)
    expect(model.queue.runs?.items[0]?.subject).toBe('20261031-151500-a00')
    expect(model.queue.runs?.items[COORDINATOR_LIST_CAP - 1]?.subject).toBe('20261031-151509-a19')
    expect(model.queue.runs?.dropped).toBe(3)
    const node = renderCoordinator(factory, model, 'dark')
    expect(textOf(node, 'coordinator-more')).toBe('3')
  })

  it('draws the alerts a budget document carries: a killed fleet, a killed repository and a repository at its cap', () => {
    // D812: the alert is a navigation to Spend and nothing else, and it says
    // which of the two grounds it is -- a killed fleet refuses everything, a
    // reached repository cap refuses that repository. The kill is the CLI's kill
    // switch (`budget kill`), which is not the same fact as a cap being reached.
    const fleet = drawsQueue({ caps: capsOf(aBudget({ fleetKilled: true })) })
    expect(itemTexts(fleet, 'coordinator-queue-kind')).toEqual([COORDINATOR_QUEUE_BUDGET_WORD])
    expect(itemTexts(fleet, 'coordinator-queue-subject')).toEqual([''])
    expect(itemTexts(fleet, 'coordinator-queue-why')).toEqual([COORDINATOR_QUEUE_KILL_WORD])

    const repo = drawsQueue({ caps: capsOf(aBudget({ repoKilled: true })) })
    expect(itemTexts(repo, 'coordinator-queue-subject')).toEqual(['owner/repo-a'])
    expect(itemTexts(repo, 'coordinator-queue-why')).toEqual([COORDINATOR_QUEUE_REPO_KILL_WORD])

    const cap = drawsQueue({ caps: capsOf(aBudget({ repoCounted: 120, repoCap: 120 })) })
    expect(itemTexts(cap, 'coordinator-queue-why')).toEqual([COORDINATOR_QUEUE_CAP_WORD])
    // A repository under its cap is not an alert: the headroom the document
    // reports is above zero and the counted dollars are below the cap.
    expect(all(drawsQueue({}), 'coordinator-queue-item')).toEqual([])
  })

  it('says a figure it has not read rather than a zero, and says the plan tasks are not known without a checkout', () => {
    // The rule the whole task is reviewed against: a number that is not in hand
    // is absent. The count is the console's own over the parts it has read, and
    // the jobs are the one read it makes only when the operator asks for them
    // (rule 5 of §6.2's opening), so a queue whose jobs have not been asked for
    // draws the items it has with the count of those -- and the jobs control in
    // the part they would fill is what names the read the figure leaves out.
    const waiting = drawsQueue({ runs: null })
    expect(textOf(waiting, 'coordinator-queue-count')).toBe('')
    expect(all(waiting, 'coordinator-queue-item')).toEqual([])

    // The same queue holding what the three reads it does make can hold: the
    // figure is drawn over those two items, and the sentence for a queue that
    // holds nothing is **not** drawn beside it.
    const floor = drawsQueue({
      runs: null,
      plans: plans([aTask('C2', 'ready')]),
      caps: capsOf(aBudget({ repoKilled: true })),
    })
    expect(textOf(floor, 'coordinator-queue-count')).toBe('2')
    expect(textOf(floor, 'coordinator-queue-empty')).toBe('')

    const noCheckout = drawsQueue({ checkout: false, plans: null })
    expect(textOf(noCheckout, 'coordinator-queue-count')).toBe('')
    expect(textOf(noCheckout, 'coordinator-queue-note')).toBe(COORDINATOR_QUEUE_NO_PLANS_WORD)

    const reduced = drawsQueue({ capsHidden: true, caps: null })
    expect(textOf(reduced, 'coordinator-queue-note')).toBe(COORDINATOR_QUEUE_CAPS_HIDDEN_WORD)
  })

  it('says nothing is waiting only over a whole queue, and never over one it could not read', () => {
    // The head's sentence is about a queue that holds nothing, and a queue whose
    // refusals stand in for parts of it is not that: a read the console could not
    // make is a gap in what it knows, so the head says nothing at all and the
    // parts say why. A queue whose jobs have not been asked for is the same gap
    // by another route -- the read is one the console chose not to make (rule 5)
    // -- so the sentence waits for the jobs the way it waits for a refusal. The
    // queue here holds nothing at all, which is the only way the sentence would
    // be drawn if the condition were the count alone.
    const refusedRuns = drawsQueue({ runs: null })
    expect(textOf(refusedRuns, 'coordinator-queue-empty')).toBe('')
    const noCheckout = drawsQueue({ checkout: false, plans: null })
    expect(textOf(noCheckout, 'coordinator-queue-empty')).toBe('')
    const reduced = drawsQueue({ capsHidden: true, caps: null })
    expect(textOf(reduced, 'coordinator-queue-empty')).toBe('')

    // And over a whole queue that holds nothing it is drawn, with no count
    // beside it: the sentence is what a reader gets instead of a zero.
    const quiet = drawsQueue({})
    expect(textOf(quiet, 'coordinator-queue-empty')).toBe(COORDINATOR_QUEUE_EMPTY_WORD)
    expect(textOf(quiet, 'coordinator-queue-count')).toBe('')
  })

  it('draws the control that asks for the jobs, and says with it whether the ask has been made', () => {
    // The jobs are the queue's fourth read and the one the entry does not make on
    // entry (rule 5), so the part they fill carries a button whose press is the
    // read. It is drawn in both of the states that have no answer yet -- before
    // the press and while the read the press made is out -- and `aria-expanded`
    // is what tells them apart, so the press is answered where it happened.
    const asked = coordinatorJobsControl(factory, true)
    expect(asked.tag).toBe('button')
    expect(asked.getAttribute('type')).toBe('button')
    expect(asked.getAttribute('aria-expanded')).toBe('true')
    expect(asked.textContent).toBe(COORDINATOR_QUEUE_JOBS_WORD)

    const waiting = coordinatorJobsControl(factory, false)
    expect(waiting.getAttribute('aria-expanded')).toBe('false')
    expect(waiting.textContent).toBe(COORDINATOR_QUEUE_JOBS_WORD)
  })

  it('draws every kind’s title and subject as text, whatever the document wrote', () => {
    // A document's strings are drawn through `putText` in the class their wire
    // key declares, and the console's own words through `literal`. A case that
    // read only the text would pass with either -- `literal` draws the string
    // too -- so each cell here is held to what `putText` makes of the same
    // value, and the three values below are the shapes that tell the two paths
    // apart: a bidi override, which no class keeps, a newline, which no
    // validating class accepts, and a marker-shaped string, which is the
    // console's own placeholder drawn as though a document had written it.
    //
    // Two cells of the four kinds are not a document's, and neither is a gap in
    // this case: a pull request's subject is the console's own label over its
    // number (`recordLabel`), and a budget alert draws no title at all -- the
    // item is about a repository, and there is no title somebody wrote. The
    // budget alert's subject is that repository, which is the hostile value.
    const hostile = ['a\u202Eb', 'first\nsecond', '<path>']

    for (const value of hostile) {
      const node = drawsQueue({
        view: decodes(aStore([aRecord({ title: value })])),
        runs: queueRuns(runsDocument([aRunRow({ halted: true, run: value, title: value })])),
        plans: plans([aTask(value, 'ready', value)]),
        caps: capsOf(aBudgetFor(value)),
      })
      const pull = itemOf(node, COORDINATOR_QUEUE_PR_WORD)
      holdsToPutText(one(pull, 'coordinator-queue-title'), value, 'sentence', 'records[].pr.title')
      // The subject is the console's own label, and it is the number whatever
      // the title says.
      expect(textOf(pull, 'coordinator-queue-subject')).toBe('#1458')

      const job = itemOf(node, COORDINATOR_QUEUE_JOB_WORD)
      holdsToPutText(one(job, 'coordinator-queue-subject'), value, 'runId', 'runs[].run')
      holdsToPutText(one(job, 'coordinator-queue-title'), value, 'sentence', 'runs[].title')

      const task = itemOf(node, COORDINATOR_QUEUE_PLAN_WORD)
      holdsToPutText(one(task, 'coordinator-queue-subject'), value, 'label', 'plans[].nodes[].id')
      holdsToPutText(one(task, 'coordinator-queue-title'), value, 'sentence', 'plans[].nodes[].title')

      const alert = itemOf(node, COORDINATOR_QUEUE_BUDGET_WORD)
      holdsToPutText(one(alert, 'coordinator-queue-subject'), value, 'repository', 'repos[].repo')
      expect(all(alert, 'coordinator-queue-title')).toEqual([])

      // And the bidi override is nowhere on the screen at all: the cells above
      // are the ones a document's strings reach, and this is the whole tree
      // saying so rather than the four kinds saying it one at a time.
      expect(drawn(node), `${value} reached the screen as itself`).not.toContain('\u202E')
    }
  })
})

/** COORDINATOR_SOURCE and WINDOW_SOURCE are the two modules whose copies of one
 * rule the K2 cases below hold together. */
const COORDINATOR_SOURCE = join(__dirname, '..', '..', 'src', 'renderer', 'screens', 'coordinator.ts')
const WINDOW_SOURCE = join(__dirname, '..', '..', 'src', 'main', 'window.ts')

/** patternLiteralOf is the regex literal a module declares one name with, read
 * out of the source rather than taken off the module: main does not export
 * `PULL_REQUEST_REPO_RE`, and what the two comments claim is about the
 * **characters** of the pattern -- two spellings of one rule are the same value,
 * so comparing the values could not see the drift in the text. */
function patternLiteralOf(source: string, name: string): string {
  const found = new RegExp(`^const ${name} = (.+)$`, 'm').exec(source)?.[1]?.trim()
  if (found === undefined) throw new Error(`no \`const ${name} = ...\` in the source it is declared in`)
  return found
}

describe('the numbers that open a pull request (K2)', () => {
  /** A_GOOD_NODE is the one node K2 draws a button for: a provider the console
   * has a route for, and the `owner/name` the store's own key is. Everything
   * below is that node with one thing taken away. */
  const A_GOOD_NODE = { provider: 'github' }

  it('draws the number as a button when the node can form a triple, and as text when it cannot', () => {
    // The tag is the whole of the rule: a number the console can form a
    // `(provider, owner/name, number)` for is a button the entry binds to the
    // operation that opens it, and a number it cannot is the text K1 drew --
    // never a button that would do nothing, which is a control a reader is owed
    // an explanation for and cannot get from a press that never happens.
    const buttoned = drawsQueue({ view: decodes(aStore([aRecord({})], A_GOOD_NODE)) })
    const subject = one(itemOf(buttoned, COORDINATOR_QUEUE_PR_WORD), 'coordinator-queue-subject')
    expect(subject.tag).toBe('button')
    expect(subject.getAttribute('type')).toBe('button')
    expect(subject.textContent).toBe('#1458')
    const label = one(buttoned, 'coordinator-record-label')
    expect(label.tag).toBe('button')
    expect(label.getAttribute('type')).toBe('button')
    expect(label.textContent).toBe('#1458')

    // The four shapes a triple cannot be formed from: no provider at all (a CLI
    // older than C1), the empty provider the decoder answers for the absent key,
    // a provider this console has no route for, and a repository that is not
    // `owner/name` -- which is where a value that would become part of a URL has
    // to be stopped, because a document writes it.
    const textOnly: readonly (readonly [string, { provider?: string; repo?: string }])[] = [
      ['no provider at all', {}],
      ['the empty provider', { provider: '' }],
      ['a provider with no route', { provider: 'gitlab' }],
      ['a repository with no owner', { provider: 'github', repo: 'owner' }],
      ['a repository with a traversal', { provider: 'github', repo: 'owner/../evil' }],
      ['a repository with a space', { provider: 'github', repo: 'owner/repo a' }],
      ['a repository with a query', { provider: 'github', repo: 'owner/repo?x=1' }],
    ]
    for (const [because, options] of textOnly) {
      const plain = drawsQueue({ view: decodes(aStore([aRecord({})], options)) })
      const drawnSubject = one(itemOf(plain, COORDINATOR_QUEUE_PR_WORD), 'coordinator-queue-subject')
      expect([drawnSubject.tag, drawnSubject.textContent], because).toEqual(['span', '#1458'])
      expect(one(plain, 'coordinator-record-label').tag, because).toBe('span')
    }
  })

  it('names the button with the pull request and the repository, and puts no destination on it', () => {
    // A number that is the same in two repositories is two different things to
    // open, so the name carries both halves -- and it is an `aria-label` rather
    // than more text, because the row is already dense.
    const node = drawsQueue({ view: decodes(aStore([aRecord({})], A_GOOD_NODE)) })
    const name = 'open pull request #1458 in owner/repo-a'
    expect(one(itemOf(node, COORDINATOR_QUEUE_PR_WORD), 'coordinator-queue-subject').getAttribute('aria-label')).toBe(
      name,
    )
    expect(one(node, 'coordinator-record-label').getAttribute('aria-label')).toBe(name)

    // The pull request's own `url` is in the document `aRecord` builds, and it
    // is nowhere on the screen: not as text, and -- because `FakeScreen` refuses
    // `href`, `src` and the rest outright -- not as a destination either. The
    // entry hands the operation the triple and never a URL.
    expect(drawn(node)).not.toContain('example.invalid')
  })

  it('draws the number as text when the repository is one the row itself refuses to draw', () => {
    // A button acts on a string the reader can see: a number whose cell draws
    // `n/a` because the `repository` class refused the value must not be a
    // control that opens something built from it. So the class is asked, and
    // these two are what it refuses while the host's own alphabet would allow
    // them -- a walk inside a segment, and a repository past the class's bound.
    const long = `${'a'.repeat(150)}/${'b'.repeat(150)}`
    const refused: readonly (readonly [string, string])[] = [
      ['a walk inside a segment', 'owner/a..b'],
      ['a repository past the class\u2019s bound', long],
    ]
    for (const [because, repo] of refused) {
      const node = drawsQueue({ view: decodes(aStore([aRecord({})], { provider: 'github', repo })) })
      const label = one(node, 'coordinator-record-label')
      expect([label.tag, label.textContent], because).toEqual(['span', '#1458'])
      expect(label.getAttribute('aria-label'), because).toBeNull()
      expect(drawn(node), because).not.toContain(repo)
    }
  })

  it('lists the providers the main process has a route for, in the main process’s own order', () => {
    // The screen cannot ask main before it draws, so its own list is a second
    // copy of the builder's, and a copy is only safe while it is still a copy.
    // Drift is silent in both directions: a provider main gained and the screen
    // did not is a number drawn as text that nobody can press, and one the screen
    // gained and main did not is a button that opens nothing. Main's list is read
    // off its own module rather than out of its source, because it is
    // `PULL_REQUEST_ROUTES`' keys and has no literal of its own.
    expect(PULL_PROVIDERS).toEqual(PULL_REQUEST_PROVIDERS)
  })

  it('holds the main process’s own repository pattern, character for character', () => {
    // The doc comment beside `PULL_REPO` claims the pattern is main's "character
    // for character", and this is where that claim is a fact about two strings
    // rather than a reader's eye. The pattern is the half that decides whether a
    // repository a document wrote becomes part of a URL, so a copy that drifted
    // in one alternative would be a rule the two halves of the console disagree
    // about -- and neither would say so.
    const mine = patternLiteralOf(readFileSync(COORDINATOR_SOURCE, 'utf8'), 'PULL_REPO')
    const theirs = patternLiteralOf(readFileSync(WINDOW_SOURCE, 'utf8'), 'PULL_REQUEST_REPO_RE')
    expect(mine.startsWith('/') && mine.endsWith('/'), 'the literal was not read as a pattern').toBe(true)
    expect(mine, 'the screen’s repository pattern is no longer main’s').toBe(theirs)
  })

  it('draws a button for exactly the repositories the main process would open and the row can draw', () => {
    // The two cases above hold the copies equal; this one holds the **decision**
    // equal, which is what a reader of the screen is affected by. Making a number
    // a button is the conjunction of two authorities that are not this file's:
    // main's own builder (which answers a destination only for a provider with a
    // route, an `owner/name` in the host's alphabet and a whole positive number)
    // and the `repository` text class (which is what the cell beside the number
    // draws). A repository this screen refuses and main would open is a number
    // that could have been pressable -- a gap, and the safe direction. A button
    // main would refuse is the other direction, and it is the one this case
    // exists for: a press that opens nothing is a control no reader can tell from
    // a broken console.
    const long = `${'a'.repeat(150)}/${'b'.repeat(150)}`
    const table: readonly (readonly [string, string])[] = [
      ['the store’s own repository', 'owner/repo-a'],
      ['a repository with a dot and a dash', 'owner/repo.b-c'],
      ['a repository with an underscore and a digit', 'owner_1/repo-2'],
      ['a walk inside a segment', 'owner/a..b'],
      ['a repository past the class’s bound', long],
      ['a traversal', 'owner/../evil'],
      ['a repository with a space', 'owner/repo a'],
      ['a repository with a query', 'owner/repo?x=1'],
      ['a repository with no owner', 'owner'],
      ['a mixed-case repository (main takes lowercase only)', 'Owner/Repo-A'],
    ]
    for (const [because, repo] of table) {
      const node = drawsQueue({ view: decodes(aStore([aRecord({})], { provider: 'github', repo })) })
      const drawnAsButton = one(node, 'coordinator-record-label').tag === 'button'
      const mainWouldOpen =
        validateArguments('link.openPullRequest', { provider: 'github', repo: asciiLower(repo), number: 1458 }).ok &&
        pullRequestUrl('github', asciiLower(repo), 1458) !== null
      const rowCanDraw = sanitizeText(repo, 'repository', 'repos[].repo').ok
      expect(drawnAsButton, `${because}: ${String(mainWouldOpen)} and ${String(rowCanDraw)}`).toBe(
        mainWouldOpen && rowCanDraw,
      )
    }
  })
})

// ---------------------------------------------------------------------------
// The job list (K4).
// ---------------------------------------------------------------------------

/** LS_DOC is one page of `runs ls --json --since 24h --limit=50`, with the two
 * figures this section turns on **deliberately different**: the window's own
 * `totals` say 41 runs, and the page below prints two. A section that summed the
 * rows it drew would show `2` under a word that claims the window. */
const LS_DOC = {
  project: 'a-project',
  runs: [
    {
      run: '20261101-090000-aa01',
      repo: 'owner/repo-a',
      status: 'succeeded',
      stage: 'implement',
      outcome: 'implemented',
      title: 'the job that finished',
      branch: 'feat/a-thing',
      reason: '',
      created: '2026-11-01T09:00:00Z',
      plan_task: 'plans/2026-10-01-a/task-one',
      launched_by: 'person-1',
      pr: 1459,
      cost: { total_usd: 1.25, model_usd: 1.0, compute_usd: 0.25, compute_estimated: true },
      terminal: true,
      changed_files: { paths: ['src/a.go'], total: 3 },
    },
    {
      run: '20261101-080000-bb02',
      repo: 'owner/repo-a',
      status: 'failed',
      stage: 'review',
      outcome: '',
      title: 'the job that did not finish',
      branch: '',
      reason: 'the reviewer refused the change',
      created: '2026-11-01T08:00:00Z',
      plan_task: '',
      launched_by: '',
      cost: { total_usd: 0.0, model_usd: 0, compute_usd: 0, compute_estimated: false },
      terminal: true,
    },
  ],
  totals: { runs: 41, model_usd: 9.0, model_notional_usd: 0, compute_usd: 3.5, total_usd: 12.5 },
  warnings: [],
}

describe('the job list (K4)', () => {
  it('draws the window’s own figures in the head and only the page’s rows under them', () => {
    // D818: the head is the window and the list is the page. The CLI cuts what it
    // *prints* and sums what it *read*, so the two figures are different numbers
    // in any real document -- and a section that recomputed the head from the
    // rows on screen would draw the page's count under the window's word.
    const page = jobsDocument(LS_DOC)
    const frame = renderCoordinatorFrame(factory, 'dark')
    fillCoordinatorJobsHead(factory, page.window, frame.jobs.head)
    fillCoordinatorJobs(factory, page.rows, frame.jobs.list)

    expect(figureOf(frame.jobs.head, 'coordinator-jobs-window-runs')).toEqual([
      COORDINATOR_JOBS_RUNS_WORD,
      '41',
    ])
    expect(figureOf(frame.jobs.head, 'coordinator-jobs-window-spent')).toEqual([
      COORDINATOR_JOBS_SPENT_WORD,
      '$12.50',
    ])
    expect(all(frame.jobs.list, 'coordinator-job')).toHaveLength(2)
    expect(
      drawn(frame.jobs.list),
      'the list drew the window’s own count, which is the figure a section that summed its rows would show',
    ).not.toContain('41')
    // The cursor is the last row's own id: what `--after` takes, read back out of
    // the row the console drew rather than computed.
    expect(page.cursor).toBe('20261101-080000-bb02')
  })

  it('draws a row off the document’s own fields, and keeps the body it expands hidden', () => {
    const page = jobsDocument(LS_DOC)
    const frame = renderCoordinatorFrame(factory, 'dark')
    fillCoordinatorJobs(factory, page.rows, frame.jobs.list)
    const rows = all(frame.jobs.list, 'coordinator-job')
    const first = rows[0]
    const second = rows[1]
    if (first === undefined || second === undefined) throw new Error('the list drew no row')

    expect(textOf(first, 'coordinator-job-id')).toBe('20261101-090000-aa01')
    expect(textOf(first, 'coordinator-job-repo')).toBe('owner/repo-a')
    expect(textOf(first, 'coordinator-job-status')).toBe('succeeded')
    expect(textOf(first, 'coordinator-job-title')).toBe('the job that finished')
    // A field the document did not carry draws nothing at all: the second row
    // wrote no outcome, and an empty cell is not a cell saying `unknown`.
    expect(all(second, 'coordinator-job-outcome')).toEqual([])

    // The body is drawn with the row and hidden, so the press is a toggle rather
    // than a rendering: every string in it is a document's.
    const body = one(first, 'coordinator-job-detail')
    expect(body.getAttribute('hidden'), 'the expanded body is open before the press').toBe('')
    expect(textOf(body, 'coordinator-job-stage')).toBe('implement')
    expect(textOf(body, 'coordinator-job-branch')).toBe('feat/a-thing')
    // The plan task is a `label` on the wire, and a label shaped like a path is
    // masked by that class: the console draws the masked value, which is what
    // keeps a document from using this cell to spell a real path.
    expect(textOf(body, 'coordinator-job-plan')).toBe('plans<path>')
    expect(textOf(body, 'coordinator-job-by')).toBe('person-1')
    expect(textOf(body, 'coordinator-job-created')).toBe('2026-11-01T09:00:00Z')
    expect(textOf(body, 'coordinator-job-pr')).toBe('1459')
    expect(figureOf(body, 'coordinator-job-cost')).toEqual([COORDINATOR_JOBS_SPENT_WORD, '$1.25'])
    // The path class masks a repository-relative path in a document the recorder
    // did not redact, which is the class's whole job: what is drawn is the
    // placeholder and never a directory of the operator's machine.
    expect(textOf(body, 'coordinator-job-file')).toBe(PATH_PLACEHOLDER)
    // The set's own `total` is the document's: three paths, one drawn, two left
    // off -- a figure this screen subtracted but did not invent.
    expect(one(body, 'coordinator-job-more').textContent).toBe('2')
    // The second row's own body: the reason a run stopped, and no cost cell for a
    // document that carried `0` -- the reading is drawn either way, and the row
    // that wrote none draws nothing.
    const secondBody = one(second, 'coordinator-job-detail')
    expect(textOf(secondBody, 'coordinator-job-reason')).toBe('the reviewer refused the change')
    expect(all(secondBody, 'coordinator-job-file')).toEqual([])
  })

  it('draws a document it cannot read as a page of nothing rather than inventing rows', () => {
    // The reader is the console's own, and a document that is not one draws no
    // row: the CLI's refusal is the entry's to draw, and a page this screen
    // cannot read is not a page of zeroes with a head claiming a window.
    const page = jobsDocument({ runs: 'not a list', totals: { runs: 3, total_usd: 1 } })
    expect(page.rows).toEqual([])
    expect(page.cursor).toBe('')
    expect(page.window?.runs.value).toBe(3)
    expect(page.window?.spent.value).toBe(1)
  })

  it('draws the section’s two controls in the console’s own words, and the floor sentence with the version', () => {
    const load = coordinatorLoadJobsControl(factory)
    expect(load.getAttribute('class')).toBe(COORDINATOR_JOBS_LOAD_CLASS)
    expect(load.getAttribute('type')).toBe('button')
    // Both carry the same state: what the press asks for is not on screen while
    // the control is, which is what the attribute says.
    expect(load.getAttribute('aria-expanded')).toBe('false')
    expect(load.textContent).toBe(COORDINATOR_JOBS_LOAD_WORD)
    const older = coordinatorOlderJobsControl(factory)
    expect(older.getAttribute('class')).toBe(COORDINATOR_JOBS_OLDER_CLASS)
    expect(older.getAttribute('aria-expanded')).toBe('false')
    expect(older.textContent).toBe(COORDINATOR_JOBS_OLDER_WORD)

    // The floor: the sentence names the release the paged read needs and the
    // version line the handshake read, and the version is *text* wherever it came
    // from -- a hostile one is a value drawn, not an element or an attribute.
    const note = coordinatorJobsFloorNote(factory, '0.15.3+<script>')
    expect(textOf(note, 'coordinator-jobs-floor-word')).toBe(COORDINATOR_JOBS_FLOOR_WORD)
    expect(one(note, 'coordinator-jobs-floor-version').textContent).toBe('0.15.3+<script>')
    // The sentence reads as one: the two spans are inline and nothing separates
    // them but what the drawing puts there, so the version must not run into
    // "this CLI is" on the screen. Spelled out rather than built from the
    // constant, because the space between the two words is the claim.
    expect(said(note)).toBe('the job list needs fugaro 0.16.0 and this CLI is 0.15.3+<script>')
  })

  it('renders every string a row draws through the class its wire key declares', () => {
    // A row is a document's text end to end -- a title somebody wrote, a reason a
    // stage wrote, a branch, a path -- so every one of them goes through
    // `putText`: nothing here is `literal`, and no value reaches an attribute.
    const page = jobsDocument({
      runs: [
        {
          run: '20261101-093000-cc03',
          repo: 'owner/repo-a',
          status: 'failed',
          title: '<img src=x onerror=alert(1)>',
          branch: 'feat/<script>',
          reason: 'javascript:alert(1)',
          changed_files: { paths: ['../etc/passwd'], total: 1 },
        },
      ],
      totals: { runs: 1, total_usd: 0 },
    })
    const frame = renderCoordinatorFrame(factory, 'dark')
    fillCoordinatorJobs(factory, page.rows, frame.jobs.list)
    const row = all(frame.jobs.list, 'coordinator-job')[0]
    if (row === undefined) throw new Error('the list drew no row')
    // The title's own class refuses markup, so what is drawn is the sanitizer's
    // refusal and never the tag it carried.
    // The title is drawn as text: the harness throws on the attributes a screen
    // may not set, so a screen that turned this string into an element would have
    // failed at the call rather than at this assertion -- and no element of the
    // tag it names exists anywhere under the row.
    expect(one(row, 'coordinator-job-title').textContent).not.toBe('')
    expect(walk(row).every((node) => node.tag !== 'img')).toBe(true)
    expect(textOf(bodyOf(row), 'coordinator-job-reason')).toBe('javascript:alert(1)')
    expect(one(row, 'coordinator-job-detail').getAttribute('hidden')).toBe('')
  })
})

describe('the pull request description (K3)', () => {
  /** blockOf draws one description into a host of its own and answers the host:
   * the entry paints the block the record's cell drew, and what a case is about
   * is what that paint wrote. */
  function blockOf(body: string, showAll = false): ScreenElement {
    const host = factory.create('div')
    fillCoordinatorDescription(factory, body, host, showAll)
    return host
  }

  /** empties is what the entry does before it paints a host: the block is drawn
   * once and painted again, and a paint is the whole of what it holds. */
  function empties(element: ScreenElement): void {
    ;(element as FakeScreen).nodes.length = 0
  }

  /** panelsOf is the model's own repository panels, which is what the entry's
   * chunk hands `repoDetail`. */
  function panelsOf(raw: unknown): readonly CoordinatorRepoPanel[] {
    return buildCoordinatorModel(queueInputs({ view: decodes(raw) })).repos ?? []
  }

  it('draws the block with the row, hidden, behind the record’s own disclosure', () => {
    const node = renders(aStore([aRecord({ number: 1458 })]))
    const block = one(node, COORDINATOR_DESCRIPTION_CLASS)
    // Drawn with the row and hidden, so the press is a toggle rather than a
    // rendering -- the same shape the job list's own rows take (K4).
    expect(block.getAttribute('hidden')).toBe('')
    const head = one(node, COORDINATOR_DESCRIPTION_HEAD_CLASS)
    expect(head.tag).toBe('button')
    expect(head.getAttribute('type')).toBe('button')
    expect(head.getAttribute('aria-expanded')).toBe('false')
    expect(head.textContent).toBe(COORDINATOR_DESCRIPTION_SHOW_WORD)
    // The mark the read wears goes in a slot of the block's own, so the press has
    // somewhere to put it before anything has been read.
    expect(all(block, BUSY_SLOT_CLASS)).toHaveLength(1)
  })

  it('answers the entry the disclosure it binds, with the pair the read is made of', () => {
    const parent = factory.create('div')
    const panel = panelsOf(aStore([aRecord({ number: 1458 })]))[0]
    if (panel === undefined) throw new Error('the store named no repository')
    const drawn = repoDetail(parent, factory, panel)
    expect(drawn.descriptions).toHaveLength(1)
    const target = drawn.descriptions[0]
    // The pair is the repository's own name and the pull request's number, and
    // the two are answered beside the elements so the entry sends what the row
    // was drawn for rather than reading it back out of the DOM.
    expect(target?.request).toEqual({ repo: 'owner/repo-a', number: 1458 })
    expect(target?.element).toBe(one(parent, COORDINATOR_DESCRIPTION_HEAD_CLASS))
    expect(target?.block).toBe(one(parent, COORDINATOR_DESCRIPTION_CLASS))
  })

  it('draws a hostile description as one text node, with nothing in it built into an element', () => {
    // The plan's own red step (K3): the host's untrusted author writes this, and
    // the whole of what the console may do with it is draw it.
    const hostile = '<script>alert(1)</script> and [click](https://evil.test)'
    const host = blockOf(hostile)
    const written = walk(host).filter((node) => (node.textContent ?? '') !== '')
    // One line in, one text node out: the description is a value, not a document
    // to parse.
    expect(written, 'the block wrote more than one text node').toHaveLength(1)
    const sanitized = sanitizeText(hostile, 'sentence', 'body')
    if (!sanitized.ok) throw new Error(sanitized.reason)
    expect(written[0]?.textContent).toBe(sanitized.text)
    // The markup is characters on the screen -- `<script>` is text -- and no
    // element of the tag it names exists under the block.
    expect(written[0]?.textContent).toContain('<script>alert(1)</script>')
    expect(walk(host).every((node) => node.tag !== 'script')).toBe(true)
    // The address is the class's own marker and never the URL the author wrote,
    // and neither string reaches an attribute: the harness throws on the
    // attributes a screen may not set, so a screen that built a link out of this
    // would have failed at the call rather than at this assertion.
    expect(written[0]?.textContent).not.toContain('evil.test')
    expect(
      walk(host).every((node) =>
        [...node.attributes.values()].every(
          (value) => !value.includes('evil.test') && !value.includes('<script>'),
        ),
      ),
    ).toBe(true)
    // One line is under the block's own bound, so there is nothing to show.
    expect(all(host, COORDINATOR_DESCRIPTION_ALL_CLASS)).toEqual([])
  })

  it('masks a credential block that spans lines before the body is split into them', () => {
    // The block is split into lines before the class is run over each one, and
    // the one redaction that needs a whole value -- a PEM block, whose body may
    // not run past its own BEGIN and END lines -- would never see one: every
    // line it drew would be a line of key material with no marker in sight. So
    // the masking runs on the body, and the split runs on what it left.
    //
    // The block is **built here** rather than written out: the file holds no
    // key-shaped literal, and the material is fresh on every run.
    const begin = ['-----BEGIN', 'PRIVATE KEY-----'].join(' ')
    const end = ['-----END', 'PRIVATE KEY-----'].join(' ')
    // Wrapped the way a real block is, and 57 bytes so that the wrap leaves a
    // short line: a run under the entropy net's own floor is exactly the line
    // the class keeps whole, which is what a split-then-mask drawing of this
    // value puts on the screen.
    const material = randomBytes(57).toString('base64')
    const wrapped = [material.slice(0, 64), material.slice(64)]
    const host = blockOf(['before the block', [begin, ...wrapped, end].join('\n'), 'after the block'].join('\n'))
    const drawn = all(host, COORDINATOR_DESCRIPTION_LINE_CLASS).map((line) => line.textContent ?? '')

    // Every window of the material's own characters over every drawn line: the
    // key is gone whole, and not one line of it at a time.
    for (let at = 0; at + 8 <= material.length; at += 1) {
      const window = material.slice(at, at + 8)
      expect(
        drawn.some((line) => line.includes(window)),
        `a drawn line carries eight characters of the key material`,
      ).toBe(false)
    }
    expect(drawn.join('\n')).toContain(TOKEN_PLACEHOLDER)
    // The markers the block was written with go with it: what is left of that
    // value is the placeholder and nothing else.
    expect(drawn.join('\n')).not.toContain('PRIVATE KEY')
    // The lines around it are untouched by the masking -- the split still gets
    // the author's own lines.
    expect(drawn[0]).toBe('before the block')
    expect(drawn[drawn.length - 1]).toBe('after the block')
  })

  it('clips a long description to the console’s twenty lines, and draws every line once Show all is pressed', () => {
    expect(COORDINATOR_DESCRIPTION_LINES, 'the block’s own bound').toBe(20)
    const body = Array.from({ length: 60 }, (_unused, index) => `marker-${String(index + 1).padStart(2, '0')}`).join('\n')
    const host = blockOf(body)
    const drawnLines = all(host, COORDINATOR_DESCRIPTION_LINE_CLASS)
    expect(drawnLines, 'the block drew every line of the body').toHaveLength(COORDINATOR_DESCRIPTION_LINES)
    expect(drawnLines[0]?.textContent).toBe('marker-01')
    expect(drawnLines[COORDINATOR_DESCRIPTION_LINES - 1]?.textContent).toBe('marker-20')
    // The bound is this block's and not the text class's: `sentence` caps one
    // text node at `MAX_TEXT_LENGTH`, so a description drawn as one node could
    // never be shown whole -- which is why the lines are drawn one at a time.
    const control = one(host, COORDINATOR_DESCRIPTION_ALL_CLASS)
    expect(control.textContent).toBe(COORDINATOR_DESCRIPTION_ALL_WORD)
    expect(control.getAttribute('type')).toBe('button')

    // Show all draws the rest, and the control goes with it: there is nothing
    // left to show.
    empties(host)
    fillCoordinatorDescription(factory, body, host, true)
    const every = all(host, COORDINATOR_DESCRIPTION_LINE_CLASS)
    expect(every).toHaveLength(60)
    expect(every[59]?.textContent).toBe('marker-60')
    expect(all(host, COORDINATOR_DESCRIPTION_ALL_CLASS)).toEqual([])
  })

  it('draws the console’s own sentence for a pull request whose description is empty', () => {
    // A body the host answered with nothing is a fact about the pull request,
    // and it is drawn in the console's own words rather than as a blank block.
    const host = blockOf('')
    expect(textOf(host, COORDINATOR_DESCRIPTION_EMPTY_CLASS)).toBe(COORDINATOR_DESCRIPTION_EMPTY_WORD)
    expect(all(host, COORDINATOR_DESCRIPTION_LINE_CLASS)).toEqual([])
    expect(all(host, COORDINATOR_DESCRIPTION_ALL_CLASS)).toEqual([])
  })

  it('draws no disclosure for a record that names no pull request to read a description of', () => {
    // A record with no pull request is the in-flight runs alone: there is no
    // number, so there is nothing a read could be made of and no control that
    // would ask for one.
    const bare = { ...(aRecord({}) as Record<string, unknown>), pr: null }
    const node = renders(aStore([bare]))
    expect(all(node, COORDINATOR_DESCRIPTION_CLASS)).toEqual([])
    expect(all(node, COORDINATOR_DESCRIPTION_HEAD_CLASS)).toEqual([])
  })
})

describe('the fleet totals and the blind spots (K1)', () => {
  it('draws every figure off the document it comes from, and no money figure the caps read has not answered', () => {
    const node = drawsQueue({
      view: decodes(aStore([aRecord({}), aRecord({ number: 1459, inFlight: true })])),
      plans: plans([aTask('C2', 'ready')]),
      caps: capsOf(aBudget({ counted: 412.6, cap: 600 })),
    })
    const totals = one(node, 'coordinator-totals')
    expect(figureOf(totals, 'coordinator-total-repos')).toEqual([COORDINATOR_TOTAL_REPOS_WORD, '1'])
    expect(figureOf(totals, 'coordinator-total-inflight')).toEqual([COORDINATOR_TOTAL_IN_FLIGHT_WORD, '1'])
    expect(figureOf(totals, 'coordinator-total-collisions')).toEqual([COORDINATOR_TOTAL_COLLISIONS_WORD, '0'])
    expect(figureOf(totals, 'coordinator-total-plans')).toEqual([COORDINATOR_TOTAL_PLANS_WORD, '1'])
    expect(figureOf(totals, 'coordinator-total-counted')).toEqual([COORDINATOR_TOTAL_COUNTED_WORD, '$412.60'])
    expect(figureOf(totals, 'coordinator-total-cap')).toEqual([COORDINATOR_TOTAL_CAP_WORD, '$600.00'])

    // The document has not answered: no counts at all, rather than a strip of
    // zeroes standing in for a document nobody read.
    const empty = one(drawsQueue({ view: null }), 'coordinator-totals')
    expect(all(empty, 'coordinator-total-repos')).toEqual([])
    expect(all(empty, 'coordinator-total-counted')).toEqual([])
  })

  it('draws the blind spots under the queue, with no button and no figure that is not in the document', () => {
    // D813: neither can be fixed by a button on this page, so neither is a
    // queue item -- the sweep is a scheduled job, and the console may not start
    // it. The line says what the console cannot see.
    const node = drawsQueue({
      view: decodes({
        repos: [
          STORED,
          { repo: 'owner/repo-b', stored: true, view: { repo: 'owner/repo-b', records: [], collisions: [] } },
        ],
      }),
    })
    const blind = one(node, 'coordinator-blind')
    expect(textOf(node, 'coordinator-blind-word')).toBe(COORDINATOR_BLIND_WORD)
    expect(figureOf(blind, 'coordinator-blind-stale')).toEqual([COORDINATOR_BLIND_STALE_WORD, '1'])
    expect(figureOf(blind, 'coordinator-blind-unread')).toEqual([COORDINATOR_BLIND_UNREAD_WORD, '1'])
    // It is a line under the queue and it is not a queue item.
    expect(all(blind, 'coordinator-queue-item')).toEqual([])
    expect(all(node, 'coordinator-queue-item')).toEqual([])
  })
})

// ---------------------------------------------------------------------------
// The read-only transcript pane (K5).
// ---------------------------------------------------------------------------

/** anIntent is one stored declaration, in the shape `coordinator intents
 * --all --json` prints it. */
function anIntent(options: {
  readonly id?: string
  readonly declaredBy?: string
  readonly at?: string
  readonly branch?: string
  readonly release?: string
  readonly files?: readonly string[]
  readonly services?: readonly string[]
}): unknown {
  return {
    id: options.id ?? 'a'.repeat(32),
    declared_by: options.declaredBy ?? 'person-1',
    at: options.at ?? '2026-11-02T08:00:00Z',
    branch: options.branch ?? 'feat/a-thing',
    release: options.release ?? '0.119.0',
    files: options.files ?? ['console/src/renderer/screens/coordinator.ts'],
    services: options.services ?? [],
  }
}

/** anIntentsDocument is the whole `--all` document: one entry per repository,
 * in the order the CLI printed them. */
function anIntentsDocument(
  repos: readonly { readonly repo: string; readonly intents: readonly unknown[] }[],
): unknown {
  return {
    repos: repos.map((repo) => ({ repo: repo.repo, provider: 'github', intents: repo.intents })),
  }
}

/** intentsOf decodes a document that must decode, or the failure is the
 * decoder's own sentence rather than an assertion three lines later. */
function intentsOf(raw: unknown): CoordinatorIntentsView {
  const result = decodeCoordinatorIntents(raw)
  if (!result.ok) throw new Error(result.skew.because)
  return result.value
}

/** drawsPane draws the screen with the intents document in hand, as the entry
 * does when the pane's own read lands. */
function drawsPane(inputs: Partial<CoordinatorInputs>): ScreenElement {
  return renderCoordinator(factory, buildCoordinatorModel(queueInputs(inputs)), 'dark')
}

describe('the read-only transcript pane (K5)', () => {
  const DECLARED = anIntentsDocument([
    { repo: 'owner/repo-a', intents: [anIntent({ declaredBy: 'person-1', files: ['a/b.ts', 'c/d.ts'] })] },
    { repo: 'owner/repo-b', intents: [anIntent({ declaredBy: 'person-2', services: ['api'], branch: '', release: '0.118.0' })] },
  ])

  it('draws the declared intents, one line per record, under the repository they were declared for', () => {
    const node = drawsPane({ intents: intentsOf(DECLARED) })
    const pane = one(node, 'coordinator-transcript')
    const [first, second] = all(pane, 'coordinator-transcript-intent')
    if (first === undefined || second === undefined) throw new Error('the pane drew fewer than two intent lines')
    expect(all(pane, 'coordinator-transcript-repo').map((each) => each.textContent)).toEqual([
      'owner/repo-a',
      'owner/repo-b',
    ])
    expect(textOf(first, 'coordinator-transcript-declaration')).toBe('person-1')
    expect(said(first)).toContain('2026-11-02T08:00:00Z')
    expect(said(first)).toContain('feat/a-thing')
    // A path is a path: the console draws none at all, and the pane is not an
    // exception to that rule (sanitize.ts's `path` class replaces it with the
    // placeholder wherever it is drawn).
    expect(said(first)).toContain(PATH_PLACEHOLDER)
    expect(textOf(second, 'coordinator-transcript-declaration')).toBe('person-2')
    expect(said(second)).toContain('api')
    expect(said(second)).toContain('0.118.0')
  })

  it('draws declared_by as a declaration and never as an identity', () => {
    // The one field of this document that is not a fact (finding #11): the word
    // that introduces it is the console's own, it is the same word the runs
    // behind a record are drawn with, and nothing on the pane presents the
    // value as anything the console verified.
    const node = drawsPane({ intents: intentsOf(DECLARED) })
    const line = all(node, 'coordinator-transcript-intent')[0]
    if (line === undefined) throw new Error('the pane drew no intent')
    const texts = walk(line)
      .map((each) => each.textContent)
      .filter((text): text is string => text !== null && text !== '')
    expect(texts[0]).toBe(COORDINATOR_DECLARED_WORD)
    expect(textOf(line, 'coordinator-transcript-declaration')).toBe('person-1')
  })

  it('answers its own three questions off the stored view, in the console’s own words', () => {
    const node = drawsPane({ view: decodes(aStore([aRecord({ inFlight: true })])) })
    const questions = all(node, 'coordinator-transcript-question')
    expect(questions.map((each) => textOf(each, 'coordinator-transcript-question-word'))).toEqual([
      'what is in flight?',
      'what collides?',
      'which repositories has the sweep not reconciled?',
    ])
    // The in-flight answer is the repository and the reading off the stored
    // document, through the screen's own count cell -- never a figure the pane
    // counted for itself and never a sentence it composed around one.
    const answers = all(node, 'coordinator-transcript-answer')
    expect(answers.map((each) => textOf(each, 'coordinator-transcript-answer-repo'))).toEqual(['owner/repo-a'])
    const answer = answers[0]
    if (answer === undefined) throw new Error('the pane answered the in-flight question with nothing')
    expect(figureOf(answer, 'coordinator-count')).toEqual([COORDINATOR_IN_FLIGHT_COUNT_WORD, '1'])
  })

  it('answers with the console’s own sentence where a question has no answer at all', () => {
    const node = drawsPane({ view: decodes(aStore([])) })
    const questions = all(node, 'coordinator-transcript-question')
    expect(questions).toHaveLength(3)
    for (const question of questions) {
      expect(all(question, 'coordinator-transcript-answer')).toEqual([])
    }
    expect(all(node, 'coordinator-transcript-none')).toHaveLength(3)
  })

  it('holds no input, no textarea and nothing contenteditable, and no control that sends text', () => {
    // The security claim of the read-only state (D827): the pane is a drawing
    // and not a door. The tag scan is the whole of it -- an `input` the screen
    // built would be a field a person could type a command line into, and no
    // assertion about its value would make it less of one.
    const node = drawsPane({ intents: intentsOf(DECLARED) })
    const tags = walk(one(node, 'coordinator-transcript')).map((each) => each.tag.toLowerCase())
    expect(tags).not.toContain('input')
    expect(tags).not.toContain('textarea')
    expect(tags).not.toContain('select')
    for (const element of walk(one(node, 'coordinator-transcript'))) {
      expect(element.getAttribute('contenteditable'), `${element.tag} is editable`).toBeNull()
      expect(element.getAttribute('role')).toBeNull()
    }
  })

  it('draws every intent line through putText, whatever the store holds', () => {
    // The store carries text anybody who can reach the report path wrote, so
    // each cell is held to `putText`'s own refusal the way the screen's other
    // wire cells are: a value the class refuses is drawn as the console's
    // placeholder rather than as itself, and an instruction found in one is
    // never an element, an attribute or a link.
    const hostile = '<script>alert(1)</script>'
    const node = drawsPane({
      intents: intentsOf(
        anIntentsDocument([
          {
            repo: 'owner/repo-a',
            intents: [anIntent({ declaredBy: hostile, branch: hostile, files: [hostile], services: [hostile] })],
          },
        ]),
      ),
    })
    expect(walk(one(node, 'coordinator-transcript')).map((each) => each.tag.toLowerCase())).not.toContain('script')
    expect(one(node, 'coordinator-transcript-declaration').getAttribute('href')).toBeNull()
  })

  it('draws a store’s hostile declarations as inert text, with no key material in them', () => {
    // The pane draws what the fleet's writers wrote, so it is the screen where a
    // control character, a bidi override or a hyperlink escape would reach the
    // drawing as itself if a document's string were written with `textContent`
    // (`literal`) or parsed (`innerHTML`) instead of classified. Every field goes
    // through `putText`'s own class, and this is the assertion that tells the two
    // drawings apart: `holdsToPutText` compares each cell with what the class
    // itself draws, which a cell drawn through `literal` cannot pass.
    //
    // The key block is **built here** rather than written out: the file holds no
    // key-shaped literal, and the material is fresh on every run. It is one line,
    // which is the shape a label can hold -- a block wrapped over its newlines
    // is a block no rule sees whole, and that case is the description block's
    // (`maskCredentialBlocks`), not this pane's: every cell here is one field of
    // one record, and the class reads the whole value.
    const begin = ['-----BEGIN', 'PRIVATE KEY-----'].join(' ')
    const end = ['-----END', 'PRIVATE KEY-----'].join(' ')
    const material = randomBytes(57).toString('base64')
    const block = [begin, material.slice(0, 64), material.slice(64), end].join(' ')
    const bidi = 'a‮b‬​c'
    const csi = '\u001b[31mred\u001b[0m'
    const hyperlink = '\u001b]8;;https://evil.test\u0007link\u001b]8;;\u0007'
    const view = intentsOf(
      anIntentsDocument([
        {
          repo: 'owner/repo-a',
          intents: [
            anIntent({
              declaredBy: `${bidi} ${csi} ${hyperlink}`,
              branch: `feat/${bidi}`,
              release: `${csi}${block}`,
              files: [`${bidi}/home/operator/notes.md`],
              // A line break is the one thing a label may not carry: the class
              // refuses rather than flattening it, because two rows wearing one
              // cell is a field the CLI's own renderer does not write, and this
              // cell is where that rule is read off the drawing.
              services: [`${hyperlink}\n${bidi}`],
            }),
          ],
        },
      ]),
    )
    const record = view.repos[0]?.intents[0]
    if (record === undefined) throw new Error('the fixture decoded to no declaration')
    const node = drawsPane({ intents: view })
    const pane = one(node, 'coordinator-transcript')
    const line = one(pane, 'coordinator-transcript-intent')

    // Every cell, drawn the way its class draws it -- the assertion a `literal`
    // drawing fails, because `literal` writes the string the store holds.
    holdsToPutText(one(pane, 'coordinator-transcript-repo'), 'owner/repo-a', 'repository', 'repos[].repo')
    holdsToPutText(one(line, 'coordinator-transcript-declaration'), record.declaredBy, 'label', 'intents[].declared_by')
    holdsToPutText(one(line, 'coordinator-transcript-at'), record.at, 'sentence', 'intents[].at')
    holdsToPutText(one(line, 'coordinator-transcript-branch'), record.branch, 'branch', 'intents[].branch')
    holdsToPutText(one(line, 'coordinator-transcript-release'), record.release, 'label', 'intents[].release')
    holdsToPutText(one(line, 'coordinator-transcript-file'), record.files[0] ?? '', 'path', 'intents[].files')
    holdsToPutText(one(line, 'coordinator-transcript-service'), record.services[0] ?? '', 'label', 'intents[].services')

    // What is on screen is inert: no escape sequence, no bidi control, no
    // zero-width character and no line break survived the classes, and neither
    // the hyperlink's own address nor the block's own markers came through as
    // text. The key material is gone whole, in every window of eight characters
    // of it, in a text node and in an attribute alike.
    // The cells are joined with a **space**: the scan below says the drawing
    // holds no control character, a line break included, and a reader that put
    // one between two cells would be finding its own.
    const drawnText = walk(pane)
      .map((each) => each.textContent ?? '')
      .join(' ')
    const drawnAttributes = walk(pane)
      .flatMap((each) => [...each.attributes.values()])
      .join(' ')
    const CONTROLS = /[\u0000-\u001f\u007f-\u009f​-‏‪-‮⁦-⁩﻿]/
    expect(CONTROLS.test(drawnText), 'a control character reached the drawing').toBe(false)
    expect(drawnText).not.toContain('evil.test')
    expect(drawnText).not.toContain('PRIVATE KEY')
    expect(drawnText).toContain(TOKEN_PLACEHOLDER)
    for (let from = 0; from + 8 <= material.length; from += 1) {
      const window = material.slice(from, from + 8)
      expect(drawnText.includes(window), 'the pane drew eight characters of the key material').toBe(false)
      expect(
        drawnAttributes.includes(window),
        'an attribute the pane set carries eight characters of the key material',
      ).toBe(false)
    }

    // And no element was built out of any of it: the pane's own tags are the
    // screen's, and nothing carries a destination.
    for (const element of walk(pane)) {
      const tag = element.tag.toLowerCase()
      expect(['a', 'script', 'img', 'iframe', 'style', 'link', 'object', 'embed'], `${tag} came from the store`).not.toContain(tag)
      expect(element.getAttribute('href'), `${tag} carries a destination`).toBeNull()
    }
  })

  it('builds no link and no destination out of anything the store wrote', () => {
    const node = drawsPane({ intents: intentsOf(DECLARED) })
    const pane = one(node, 'coordinator-transcript')
    for (const element of walk(pane)) {
      expect(element.getAttribute('href'), `${element.tag} carries a destination`).toBeNull()
      expect(element.tag.toLowerCase()).not.toBe('a')
    }
  })

  it('says what is missing under it, and that the decision is the terminal’s', () => {
    const node = drawsPane({ intents: intentsOf(DECLARED) })
    const pane = one(node, 'coordinator-transcript')
    expect(textOf(pane, 'coordinator-transcript-missing')).toBe(COORDINATOR_TRANSCRIPT_MISSING_WORD)
    // The sentence names the decision that has not been taken, and it is drawn
    // under the pane rather than over it: what the pane holds is what the store
    // holds, and the sentence is about the thing that is not built.
    expect(said(pane).indexOf(COORDINATOR_TRANSCRIPT_MISSING_WORD)).toBeGreaterThan(
      said(pane).indexOf(COORDINATOR_TRANSCRIPT_WORD),
    )
  })

  it('is the screen’s last region, under the panels the sketch draws it under', () => {
    const node = drawsPane({ intents: intentsOf(DECLARED) })
    const regions = (node as FakeScreen).childNodes().map((each) => (each as FakeScreen).classes[0])
    expect(regions[regions.length - 1]).toBe('coordinator-transcript')
  })

  it('draws no intent line at all while the read has not answered', () => {
    // `null` is the read still out: an installation with nothing declared and
    // one nobody has read yet are two states, and a pane that drew the second
    // as the first would claim a quiet fleet it never looked at.
    const node = drawsPane({ intents: null })
    const pane = one(node, 'coordinator-transcript')
    expect(all(pane, 'coordinator-transcript-intent')).toEqual([])
    expect(all(pane, 'coordinator-transcript-declaration')).toEqual([])
  })
})

// ---------------------------------------------------------------------------
// The surface: the one argv, and the mutation control for its pin.
// ---------------------------------------------------------------------------

describe('the surface the project view is read through (T1.2)', () => {
  const operation = CONSOLE_OPERATIONS.find((candidate) => candidate.name === 'coordinator.status')

  it('fixes exactly one argv, with the `--project` word rule 3 adds to every row', () => {
    // The exact argv, written out rather than read from the row: a test that
    // asked the table what it contains could not catch the table changing.
    // `--from-store` and `--all` are words of the row and not arguments --
    // no screen control turns either off -- and `--repo` and `--since` are
    // deliberately absent, which is the whole of why this read can be drawn
    // without a control for them.
    expect(
      argvFor('coordinator.status', {}, { target: { project: 'chosen' }, directory: undefined }),
    ).toEqual(['coordinator', 'status', '--from-store', '--all', '--json', '--project', 'chosen'])
    expect(operation?.flags).toEqual(['--from-store', '--all', '--json'])
  })

  it('declares no argument, so a renderer cannot hand it one', () => {
    // The operation's schema is empty on purpose: every value it needs is a
    // constant of the row, and a payload that carried a key would be refused by
    // `argvFor` before a child existed.
    expect(operation?.args).toEqual([])
    const row = SPAWN_COMMANDS.find((candidate) => candidate.operation === 'coordinator.status')
    expect(row?.args).toEqual([])
    expect(row?.takesProject).toBe(true)
    expect(row?.dirFlag).toBe(false)
    expect(() => argvFor('coordinator.status', { repo: 'owner/repo-a' }, { target: { project: 'chosen' }, directory: undefined })).toThrow(
      /does not declare/,
    )
  })

  it('refuses a spawn row that drops `--from-store`, which is the pin’s own control', () => {
    // The plan's red step, as a case: allow the variant without `--from-store`
    // in a scratch tree and this fails. `assertSpawnSurface` holds the row to
    // the operation's flags, so the exact-argv list above and this check are
    // two independent doors onto the same fact.
    const without = SPAWN_COMMANDS.map((row): SpawnCommand =>
      row.operation === 'coordinator.status'
        ? { ...row, words: ['coordinator', 'status', '--all', '--json'] }
        : row,
    )
    expect(() => assertSpawnSurface(without)).toThrow(/--from-store/)
    // ...and the real table passes it, so the check above is not one that
    // refuses every table.
    expect(() => assertSpawnSurface(SPAWN_COMMANDS)).not.toThrow()
  })

  it('renders every text this screen can draw through putText', () => {
    // A guard on the harness rather than on the screen: if `putText` ever
    // stopped writing `textContent`, every case above would still pass while
    // measuring nothing. It is asserted here so the file owns its own premise.
    const element = new FakeScreen('span')
    putText(element, 'a value', 'sentence', 'a field')
    expect(element.textContent).toBe('a value')
  })
})
