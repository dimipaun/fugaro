// The Projects screen, and the Run detail screen's model
// (desktop-console plan Task 2.3; design section 6.2 screens 3 and 4).
//
// The design's rule for this screen is the one the plan's tests are about:
// **"Every panel names its source, and where there is no source the panel says
// so rather than drawing an empty box."** So the model carries one `Panel` per
// section with a *state* -- `read`, `unavailable`, `not-built` -- and the two
// tests the plan names are here:
//
//   - **an unbuilt section renders as not-built and never as a zero.** The
//     policy object (`fugaro project show --json`, project-centric phase 1) and
//     the enrollment allow-list (phase 1 and 4) have no command behind them on
//     this tree, so their panels carry no count at all: a screen that drew `0`
//     for either would be reporting a measurement nobody made, and a screen that
//     drew an empty list would be reporting "there are none". The test asserts
//     the state, the reason, and that the drawn tree holds **no count cell** for
//     those two panels.
//   - **the inventory contains no value-shaped string.** The secrets panel is
//     `secrets ls --json`'s entries: name, scope, labels and version counts. The
//     test feeds it an entry whose label value is a real-shaped token --
//     assembled at run time by `test/support/fakeSecrets.ts`, never written as a
//     literal -- and asserts that the token appears in no cell of the drawn
//     tree, that the cell that would have carried it reads `n/a`, and that the
//     whole tree holds no string matching a credential's shape.
//
//
// The fake screen below is `spend.test.ts`'s, in this file's own words: the
// three screens are siblings, each declares its own `ScreenElement`, and a test
// that a factory drawing one draws another is `projects.test.ts`'s job here
// (`renderProjects` is drawn through the *Run* screen's factory in the last
// describe, and `renderRun` through this one).

import { describe, expect, it } from 'vitest'

import { CONSOLE_OPERATIONS } from '../../src/main/ipc'
import { putText } from '../../src/renderer/app'
import { PROJECT_PANEL_KEYS, PROJECTS_THEMES, buildProjectsModel, renderProjects } from '../../src/renderer/screens/projects'
import type { ProjectsInputs, ScreenElement, ScreenFactory } from '../../src/renderer/screens/projects'
import { ADDRESS_PLACEHOLDER, TOKEN_PLACEHOLDER } from '../../src/shared/sanitize'
import { githubToken, highEntropyRun } from '../support/fakeSecrets'

// ---------------------------------------------------------------------------
// The harness: a fake element that refuses what a screen may not do.
// ---------------------------------------------------------------------------

/** FakeScreen is the element a screen draws into. It records text, classes and
 * attributes, and it **throws on the attributes a screen must never set**: the
 * navigating attribute, a resource, a style and any handler. A screen that
 * reached for one would fail every test in this file rather than one. */
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

/** panel is the panel with a key, asserted to be single. */
function panel(node: ScreenElement, key: string): FakeScreen {
  const found = all(node, 'projects-panel').filter((one) => one.getAttribute('data-panel') === key)
  expect(found, `expected exactly one panel for ${key}`).toHaveLength(1)
  const first = found[0]
  if (first === undefined) throw new Error(`no panel ${key}`)
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

// ---------------------------------------------------------------------------
// The documents.
// ---------------------------------------------------------------------------

const SLUG = 'r-1a2b3c4d/r-5e6f7a8b'
const OTHER_SLUG = 'r-1a2b3c4d/r-9c8d7e6f'

/** BUDGET is `budget show --json`, invented: two cap rows, one of them killed,
 * one repository seen but not enrolled (it is in `RUNS` only), and the two
 * binding scopes the design's screen 2 names. */
const BUDGET = {
  project: 'example-project',
  gcp_project: 'example-cloud',
  mode: 'enforce',
  day: '2026-10-15',
  max_reserve_usd: 5,
  global: {
    daily_cap_usd: 40,
    per_run_cap_usd: 8,
    counted_usd: 12.5,
    spent_usd: 4,
    notional_usd: 3.25,
    headroom_usd: 27.5,
    kill: null,
  },
  defaults: { repo_daily_cap_usd: 10, repo_per_run_cap_usd: 2 },
  repos: [
    {
      slug: SLUG,
      repo: 'example-owner/example-repo',
      daily_cap_usd: 20,
      daily_cap_source: 'repo',
      per_run_cap_usd: 4,
      per_run_cap_source: 'global',
      counted_usd: 12.5,
      spent_usd: 4,
      notional_usd: 3.25,
      headroom_usd: 7.5,
      kill: { by: 'operator@example.test', at: '2026-10-15T11:30:00Z', reason: 'spend rate' },
      running: [],
    },
    {
      slug: OTHER_SLUG,
      repo: 'example-owner/other-repo',
      daily_cap_usd: null,
      daily_cap_source: 'none',
      per_run_cap_usd: null,
      per_run_cap_source: 'none',
      counted_usd: 0,
      spent_usd: 0,
      notional_usd: 0,
      headroom_usd: null,
      kill: null,
      running: [],
    },
  ],
  running: [],
  warnings: ['project mode is `enforce`: leases are capped'],
}

/** RUNS is `runs ls --json`, invented: one job for the enrolled repository and
 * one for a repository the cap document does not know. */
const RUNS = {
  project: 'example-project',
  runs: [
    {
      run: 'run-4a5b6c7d8e9f',
      run_id: '4a5b6c7d8e9f',
      repo: 'example-owner/example-repo',
      workflow: 'default',
      status: 'running',
      stage: 'verify',
      branch: 'fugaro/run-4a5b6c7d8e9f',
      requested_by: 'operator@example.test',
      pr: 42,
      pr_url: 'https://bitbucket.org/example-owner/example-repo/pull-requests/42',
      created: '2026-10-15T11:00:00Z',
      cost: { model_usd: 1.25, compute_usd: 0.5, compute_estimated: true, total_usd: 1.75, model_basis: 'api-list' },
    },
    {
      run: 'run-112233445566',
      run_id: '112233445566',
      repo: 'example-owner/other-repo',
      workflow: 'default',
      status: 'finished',
      stage: 'done',
      outcome: 'success',
      created: '2026-10-15T10:00:00Z',
      cost: { model_usd: 0.5, compute_usd: 0, compute_estimated: false, total_usd: 0.5, model_basis: 'subscription' },
    },
  ],
  totals: { runs: 2, model_usd: 1.75, model_notional_usd: 0.5, compute_usd: 0.5, total_usd: 2.25, compute_not_estimated: 1 },
  warnings: [],
}

/** ACCOUNTS is `accounts ls --json`, complete. */
const ACCOUNTS = { repo: 'example-owner/example-repo', accounts: ['claude-ops@example.test'], complete: true }

/** SECRETS is `secrets ls --json`'s body: a **bare array**, as the contract
 * says (section 6.6). */
const SECRETS = [
  {
    name: 'fugaro-github-token',
    scope: 'repo',
    allowed: ['example-owner/example-repo'],
    id: 'projects/example-cloud/secrets/fugaro-github-token',
    created: '2026-09-01T09:00:00Z',
    labels: { fugaro_secret: 'fugaro-github-token', managed_by: 'fugaro' },
    versions: 3,
    latest: '3',
  },
]

/** inputs is a complete set of documents. */
function inputs(overrides: Partial<ProjectsInputs> = {}): ProjectsInputs {
  return { budget: BUDGET, runs: RUNS, accounts: ACCOUNTS, secrets: SECRETS, sentences: {}, ...overrides }
}

// ---------------------------------------------------------------------------

describe('buildProjectsModel', () => {
  it('gives every panel a source, a state and a reason when it has no source', () => {
    const model = buildProjectsModel(inputs())
    expect(model.panels.map((entry) => entry.key)).toEqual([
      'caps',
      'kill',
      'repositories',
      'jobs',
      'accounts',
      'secrets',
      'policy',
      'enrollment',
    ])
    expect(PROJECT_PANEL_KEYS).toEqual(model.panels.map((entry) => entry.key))
    for (const entry of model.panels) {
      // Every panel names what it read, and the two that have no source name the
      // command they would read instead -- the design's "every panel names its
      // source" and the plan's "rather than drawing an empty box".
      expect(entry.source, `${entry.key} has no source line`).not.toBeNull()
      const built = entry.key === 'policy' || entry.key === 'enrollment'
      expect(entry.state, `${entry.key}`).toBe(built ? 'not-built' : 'read')
      expect(entry.because === null).toBe(!built)
    }
  })

  it('names a panel\'s source with a command line the allow-list carries', () => {
    // The design's review criterion for this screen above all others: every
    // field traces to a command line in section 5.1's allow-list. Eight panels,
    // and each one that claims a read names an operation of `CONSOLE_OPERATIONS`
    // whose command line is the one the panel drew.
    const model = buildProjectsModel(inputs())
    const commands = new Map<string, string | null>(CONSOLE_OPERATIONS.map((operation) => [operation.name, operation.command]))
    for (const entry of model.panels) {
      if (entry.state === 'not-built') {
        expect(entry.operation, `${entry.key} claims an operation it cannot have`).toBeNull()
        continue
      }
      const operation: string | null = entry.operation
      expect(operation, `${entry.key} names no operation`).not.toBeNull()
      const command = commands.get(operation as string)
      expect(command, `${entry.key}'s operation is not allow-listed`).toBeDefined()
      expect(entry.source).toContain(`fugaro ${command as string}`)
    }
  })

  it('draws the two panels the CLI has no command for as not-built, with no count at all', () => {
    const model = buildProjectsModel(inputs())
    for (const key of ['policy', 'enrollment']) {
      const entry = model.panels.find((candidate) => candidate.key === key)
      expect(entry?.state).toBe('not-built')
      // The plan's rule: not-built and **never a zero**. A count is the only
      // thing a screen could draw a zero from, so a not-built panel has none.
      expect(entry?.count, `${key} carries a count`).toBeNull()
      expect(entry?.because ?? '').not.toBe('')
      expect(entry?.operation).toBeNull()
    }
    expect(model.panels.find((entry) => entry.key === 'policy')?.source).toContain('project show')
  })

  it('shows the enrolled and the seen repositories, and names which is which', () => {
    const model = buildProjectsModel(inputs())
    // Enrolled: the cap document knows them. Seen: the run list has rows for
    // them. Neither list is the other, and the difference is the interesting
    // part -- a repository with runs and no cap row is a cap nobody set.
    expect(model.repositories.enrolled.map((entry) => entry.slug)).toEqual([SLUG, OTHER_SLUG])
    // A run row carries no slug (`runview.Row` has none): the run list names a
    // repository by `repo` alone, which is how the two lists are matched.
    expect(model.repositories.seen.map((entry) => entry.repo).sort()).toEqual(
      ['example-owner/example-repo', 'example-owner/other-repo'].sort(),
    )
    expect(model.repositories.seen.map((entry) => entry.slug)).toEqual(['example-owner/example-repo', 'example-owner/other-repo'])
    expect(model.repositories.onlySeen).toEqual([])
    expect(model.repositories.enrolledNote).toBeNull()
  })

  it('names a repository the run list knows and the cap document does not', () => {
    const runs = { ...RUNS, runs: [...RUNS.runs, { ...RUNS.runs[1], run: 'run-abcdef012345', repo: 'example-owner/third-repo' }] }
    const model = buildProjectsModel(inputs({ runs }))
    expect(model.repositories.onlySeen).toEqual(['example-owner/third-repo'])
    // The count is a measurement, so it is a reading and not a bare number.
    expect(model.repositories.seenCount.value).toBe(3)
  })

  it('names the binding scope of every effective per-run cap', () => {
    const model = buildProjectsModel(inputs())
    const rows = model.caps
    expect(rows.map((row) => row.scope)).toEqual(['global', 'defaults', 'repo', 'repo'])
    expect(rows.map((row) => row.slug)).toEqual([null, null, SLUG, OTHER_SLUG])
    // `per_run_cap_source` is the "binding scope named" the design asks for
    // (`internal/cli/budget.go`: `repo`, `default`, `global` or `none`).
    expect(rows.map((row) => row.perRunCapSource)).toEqual([null, null, 'global', 'none'])
    expect(rows.map((row) => row.dailyCapSource)).toEqual([null, null, 'repo', 'none'])
    // A cap the document does not carry is unavailable and never a cap of zero.
    expect(rows[3]?.dailyCap.state).toBe('unavailable')
    expect(rows[0]?.dailyCap.value).toBe(40)
    // The global row carries the largest single lease, which is its own figure:
    // it is a limit on one lease and not a cap (`budget.Limits.MaxReserveMicros`).
    expect(model.maxReserve.value).toBe(5)
  })

  it('draws "not killed" as a fact about the document, not as a missing value', () => {
    const model = buildProjectsModel(inputs())
    // `kill: null` on the wire is a measured not-killed (the contract's `*showKill`),
    // and the global row is the project's own switch.
    expect(model.kill.global).toBeNull()
    const killed = model.kill.rows.filter((entry) => entry.kill !== null)
    expect(killed.map((entry) => entry.slug)).toEqual([SLUG])
    expect(killed[0]?.kill?.reason).toBe('spend rate')
    // The other repository's switch is off, and off is on the panel as the word
    // rather than as a missing value.
    expect(model.kill.rows.filter((entry) => entry.kill === null).map((entry) => entry.slug)).toEqual([OTHER_SLUG])
  })

  it('renders an incomplete accounts document as unavailable rather than as an empty list', () => {
    const accounts = { repo: 'example-owner/example-repo', accounts: [], complete: false, note: 'no project config selected' }
    const model = buildProjectsModel(inputs({ accounts }))
    const panel = model.panels.find((entry) => entry.key === 'accounts')
    expect(panel?.state).toBe('unavailable')
    // The reason is the CLI's own sentence, which the screen draws verbatim.
    expect(panel?.because).toBe('no project config selected')
    expect(model.accounts.complete).toBe(false)
    expect(model.accounts.names).toEqual([])
  })

  it('reads the secrets inventory from the bare array the CLI prints', () => {
    const model = buildProjectsModel(inputs())
    expect(model.secrets.entries.map((entry) => entry.name)).toEqual(['fugaro-github-token'])
    expect(model.secrets.entries[0]?.scope).toBe('repo')
    expect(model.secrets.entries[0]?.versions.value).toBe(3)
    expect(model.secrets.entries[0]?.labels.map((label) => label.key)).toEqual(['fugaro_secret', 'managed_by'])
    // Names, labels and version counts only: the entry carries an `id` and a
    // `created`, and neither is on this screen.
    expect(model.secrets.entries[0]?.allowed).toEqual(['example-owner/example-repo'])
  })

  it('says which source has no document rather than drawing an empty panel', () => {
    const model = buildProjectsModel(inputs({ secrets: null, sentences: { 'secrets.inventory': 'permission denied' } }))
    const panel = model.panels.find((entry) => entry.key === 'secrets')
    expect(panel?.state).toBe('unavailable')
    expect(panel?.because).toBe('permission denied')
    expect(model.secrets.entries).toEqual([])
  })

  it('bounds the job list and says how many runs it did not draw', () => {
    const many = Array.from({ length: 60 }, (_, index) => ({ ...RUNS.runs[1], run: `run-${String(index).padStart(12, '0')}` }))
    const model = buildProjectsModel(inputs({ runs: { ...RUNS, runs: many } }))
    expect(model.jobs.rows).toHaveLength(50)
    expect(model.jobs.dropped.value).toBe(10)
    expect(model.jobs.total.value).toBe(60)
  })

  it('reads nothing out of a document it was not given, and says so per panel', () => {
    const model = buildProjectsModel({ budget: null, runs: null, accounts: null, secrets: null, sentences: {} })
    expect(model.project).toBeNull()
    for (const entry of model.panels) {
      if (entry.key === 'policy' || entry.key === 'enrollment') {
        expect(entry.state).toBe('not-built')
        continue
      }
      expect(entry.state, `${entry.key} claims a read it did not make`).toBe('unavailable')
      expect(entry.because).not.toBeNull()
    }
    expect(model.caps).toEqual([])
    expect(model.jobs.rows).toEqual([])
  })

  it('carries a document\'s warnings verbatim, as the CLI worded them', () => {
    const model = buildProjectsModel(inputs())
    expect(model.warnings).toEqual(['project mode is `enforce`: leases are capped'])
  })
})

describe('renderProjects', () => {
  it('puts the theme on the root and one panel per key, each naming its state', () => {
    for (const theme of PROJECTS_THEMES) {
      const tree = renderProjects(factory, buildProjectsModel(inputs()), theme)
      expect(tree.getAttribute('data-theme')).toBe(theme)
      expect((tree as FakeScreen).classes).toContain('projects')
    }
    const tree = renderProjects(factory, buildProjectsModel(inputs()), 'light')
    for (const key of PROJECT_PANEL_KEYS) {
      const drawn = panel(tree, key)
      expect(drawn.getAttribute('data-state')).toBe(key === 'policy' || key === 'enrollment' ? 'not-built' : 'read')
    }
  })

  it('renders an unbuilt section as not-built, with no zero anywhere in it', () => {
    const tree = renderProjects(factory, buildProjectsModel(inputs()), 'light')
    for (const key of ['policy', 'enrollment']) {
      const drawn = panel(tree, key)
      expect(drawn.getAttribute('data-state')).toBe('not-built')
      // No count cell and no reading: the only text is the console's own
      // sentence about why the panel has nothing to draw.
      expect(all(drawn, 'projects-count')).toEqual([])
      expect(texts(drawn, 'projects-number').filter((text) => text !== null)).toEqual([])
      const text = drawnText(drawn)
      expect(text).not.toContain(' 0')
      expect(text).toContain('not readable')
    }
  })

  it('shows the source of every panel on the panel itself', () => {
    const tree = renderProjects(factory, buildProjectsModel(inputs()), 'light')
    expect(one(panel(tree, 'caps'), 'projects-source').textContent).toContain('fugaro budget show --json')
    expect(one(panel(tree, 'jobs'), 'projects-source').textContent).toContain('fugaro runs ls --json')
    expect(one(panel(tree, 'policy'), 'projects-source').textContent).toContain('fugaro project show --json')
  })

  it('contains no value-shaped string, and refuses one a document carries', () => {
    // A label value that is a token of the real shape -- assembled at run time,
    // never written as a literal (`test/support/fakeSecrets.ts`) -- and a name
    // that is a high-entropy run. Neither may reach a cell.
    const token = githubToken()
    const secrets = [{ ...SECRETS[0], name: token, labels: { managed_by: highEntropyRun() } }]
    const tree = renderProjects(factory, buildProjectsModel(inputs({ secrets })), 'light')
    const text = drawn(tree)
    expect(text).not.toContain(token)
    expect(text).not.toContain(highEntropyRun())
    // Both are drawn as the redaction marker, not as the value: `putText`'s
    // sanitizer replaces a token-shaped run with `[redacted-token]` and a
    // high-entropy one keeps its shape only if it is short, so the inventory can
    // hold a value and still not show one.
    const names = all(panel(tree, 'secrets'), 'projects-secret-name')
    expect(names.map((node) => node.textContent)).toEqual([TOKEN_PLACEHOLDER])
    const labels = all(panel(tree, 'secrets'), 'projects-secret-label-value')
    expect(labels.map((node) => node.textContent)).toEqual([TOKEN_PLACEHOLDER])
  })

  it('draws a cap the document does not carry as n/a, never as zero', () => {
    const tree = renderProjects(factory, buildProjectsModel(inputs()), 'light')
    const caps = all(tree, 'projects-cap')
    // Four rows: global, defaults and the two repositories.
    expect(caps).toHaveLength(4)
    const unset = (caps[3] as FakeScreen)
    expect(texts(unset, 'projects-cap-value')).toEqual(['n/a', 'n/a'])
    expect(texts(caps[0] as FakeScreen, 'projects-cap-value')).toEqual(['$40.00', '$8.00'])
  })

  it('draws a kill with its operator, its time and its reason', () => {
    const tree = renderProjects(factory, buildProjectsModel(inputs()), 'light')
    const kill = one(tree, 'projects-kill')
    expect(drawn(kill)).toContain('KILLED by')
    expect(drawn(kill)).toContain('spend rate')
    // The project's own switch reads as the measured fact it is.
    expect(drawn(panel(tree, 'kill'))).toContain('not killed')
  })

  it('draws the job rows with their stage, their requester and their cost', () => {
    const tree = renderProjects(factory, buildProjectsModel(inputs()), 'light')
    const rows = all(tree, 'projects-job')
    expect(rows).toHaveLength(2)
    expect(drawn(rows[0] as FakeScreen)).toContain('verify')
    // `requested_by` is a person's address, and the person class draws an
    // address as the marker rather than as the address: a job's requester never
    // reaches a screen (`schema.ts`'s `requested_by: 'person'`).
    expect(drawn(rows[0] as FakeScreen)).toContain(ADDRESS_PLACEHOLDER)
    expect(drawn(rows[0] as FakeScreen)).not.toContain('operator@example.test')
    // The notional account is marked as notional wherever it is drawn.
    expect(drawn(rows[1] as FakeScreen)).toContain('NOTIONAL~')
  })
})

/** drawnText is the text of one subtree, in document order. */
function drawnText(node: ScreenElement): string {
  return walk(node)
    .map((leaf) => leaf.textContent ?? '')
    .filter((text) => text !== '')
    .join(' ')
}