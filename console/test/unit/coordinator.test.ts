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

import { describe, expect, it } from 'vitest'

import { CONSOLE_OPERATIONS } from '../../src/main/ipc'
import { SPAWN_COMMANDS, argvFor, assertSpawnSurface } from '../../src/main/spawn'
import type { SpawnCommand } from '../../src/main/spawn'
import { putText } from '../../src/renderer/app'
import { capsOf } from '../../src/renderer/screens/spend'
import {
  COORDINATOR_BLIND_STALE_WORD,
  COORDINATOR_BLIND_UNREAD_WORD,
  COORDINATOR_BLIND_WORD,
  COORDINATOR_CARRIED_WORD,
  COORDINATOR_COLLISION_WORD,
  COORDINATOR_COLLISIONS_WORD,
  COORDINATOR_DRAFT_WORD,
  COORDINATOR_IN_FLIGHT_COUNT_WORD,
  COORDINATOR_IN_FLIGHT_WORD,
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
  COORDINATOR_UNREAD_WORD,
  QUEUE_SOURCES,
  buildCoordinatorModel,
  queueRuns,
  renderCoordinator,
} from '../../src/renderer/screens/coordinator'
import type {
  CoordinatorInputs,
  CoordinatorQueueItem,
  ScreenElement,
  ScreenFactory,
} from '../../src/renderer/screens/coordinator'
import { UNAVAILABLE_MARK } from '../../src/shared/availability'
import { PATH_PLACEHOLDER } from '../../src/shared/sanitize'
import { decodeCoordinatorStatus, decodePlanStatus } from '../../src/shared/schema'
import type { CoordinatorStatusView, PlanStatusView } from '../../src/shared/schema'
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

/** decodes is a document that must decode, or the failure is the decoder's own
 * sentence rather than an assertion three lines later. */
function decodes(raw: unknown): CoordinatorStatusView {
  const result = decodeCoordinatorStatus(raw)
  if (!result.ok) throw new Error(result.skew.because)
  return result.value
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
function aStore(records: readonly unknown[]): unknown {
  return {
    repos: [
      { repo: 'owner/repo-a', stored: true, view: { repo: 'owner/repo-a', records, collisions: [] } },
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
    expect(head.nodes[0], 'the number does not open the row').toBe(one(head, 'coordinator-record-label'))
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
// What this file pins is the drawing and the model; that the four reads are
// started together, and what the buttons do when they are clicked, is
// `entry-wiring.test.ts`'s.
// ---------------------------------------------------------------------------

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
    // is absent, and a count over the items drawn is drawn only when all four
    // reads were made and answered.
    const waiting = drawsQueue({ runs: null })
    expect(textOf(waiting, 'coordinator-queue-count')).toBe('')
    expect(all(waiting, 'coordinator-queue-item')).toEqual([])

    const noCheckout = drawsQueue({ checkout: false, plans: null })
    expect(textOf(noCheckout, 'coordinator-queue-count')).toBe('')
    expect(textOf(noCheckout, 'coordinator-queue-note')).toBe(COORDINATOR_QUEUE_NO_PLANS_WORD)

    const reduced = drawsQueue({ capsHidden: true, caps: null })
    expect(textOf(reduced, 'coordinator-queue-note')).toBe(COORDINATOR_QUEUE_CAPS_HIDDEN_WORD)
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
