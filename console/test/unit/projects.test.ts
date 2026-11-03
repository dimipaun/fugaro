// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
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
// **Where the Run detail screen's model tests live.** The plan's file list for
// this task names `projects.test.ts` and `replay.test.ts` and no `run.test.ts`,
// so `buildRunModel`'s tests are the last two describes of this file, under a
// banner that says so. They are also where the task's other named case is held:
// **the pull request is text and no link is built**, per source -- a
// `runview.Row` (whose number is `pr`), a `JSONFinished`-shaped record (whose
// number is `pr_number`), and a live entry that carries neither -- with a
// Bitbucket-shaped `pr_url` on the allowed host, and an open-external spy that
// must never be called.
//
// The fake screen below is `spend.test.ts`'s, in this file's own words: the
// three screens are siblings, each declares its own `ScreenElement`, and a test
// that a factory drawing one draws another is `projects.test.ts`'s job here
// (`renderProjects` is drawn through the *Run* screen's factory in the last
// describe, and `renderRun` through this one).

import { describe, expect, it } from 'vitest'

import { CONSOLE_OPERATIONS } from '../../src/main/ipc'
import { UNAVAILABLE_MARK } from '../../src/shared/availability'
import { putText } from '../../src/renderer/app'
import {
  PROJECT_PANEL_KEYS,
  PROJECTS_THEMES,
  SETTINGS_PENCIL_KEY,
  buildProjectsModel,
  buildProjectsModels,
  buildProjectsSettings,
  fillProjectsModels,
  fillProjectsSettings,
  renderProjects,
} from '../../src/renderer/screens/projects'
import type {
  ProjectsInputs,
  ProjectsModelsInputs,
  ProjectsSettingsInputs,
  ScreenElement,
  ScreenFactory,
} from '../../src/renderer/screens/projects'
import {
  PR_NO_LINK_REASON,
  RUN_LOG_CAP,
  RUN_SPEND_STAGES_MAX,
  RUN_TOLERATED_MAX,
  buildRunModel,
  linkFor,
  parseLogWindow,
  pullRequestUrl,
  renderRun,
} from '../../src/renderer/screens/run'
import type { RunFactory, RunInputs } from '../../src/renderer/screens/run'
import { ReplayBuffer } from '../../src/shared/replay'
import { decodeModelsList } from '../../src/shared/schema'
import type { ModelsListView } from '../../src/shared/schema'
import { ADDRESS_PLACEHOLDER, sanitizeText, TOKEN_PLACEHOLDER } from '../../src/shared/sanitize'
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

  constructor(
    readonly tag: string,
    readonly namespace: 'html' | 'svg' = 'html',
  ) {}

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

/** factory makes fresh fakes, in the shape the renderer is handed: Run's stage
 * timeline draws its icons and bars in the SVG namespace, which is the one
 * screen that needs the second `create`. */
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

/** PR_LINE_CLASSES is the pull-request line's cells in the order they are
 * drawn: the three facts and the separator between them. */
const PR_LINE_CLASSES = ['run-pr-repo', 'run-pr-number', 'run-pr-sep', 'run-pr-url'] as const

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
 * and the two binding scopes the design's screen 2 names. */
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
  return { budget: BUDGET, accounts: ACCOUNTS, secrets: SECRETS, sentences: {}, ...overrides }
}

// ---------------------------------------------------------------------------

describe('buildProjectsModel', () => {
  it('gives every panel a source, a state and a reason when it has no source', () => {
    const model = buildProjectsModel(inputs())
    expect(model.panels.map((entry) => entry.key)).toEqual([
      'caps',
      'kill',
      'repositories',
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

  it('shows the repository set the cap document carries, and nothing the run list would add', () => {
    const model = buildProjectsModel(inputs())
    // One list: the cap document's own `repos`. The run list is not read by this
    // screen any more (the owner's report of 2026-10-31), so there is no second
    // list to difference it against and no claim about which of these have run.
    expect(model.repositories.enrolled.map((entry) => entry.slug)).toEqual([SLUG, OTHER_SLUG])
    expect(model.repositories.enrolledCount.value).toBe(2)
    const panel_ = model.panels.find((entry) => entry.key === 'repositories')
    expect(panel_?.state).toBe('read')
    expect(panel_?.because).toBeNull()
  })

  it('draws no repository claim when its one document did not arrive', () => {
    // The list comes from the cap document, so the document missing leaves the
    // panel unable to say anything about the project's repository set -- and it
    // says the read is missing rather than drawing an empty list, which would
    // read as "this project has no repository".
    const withoutCaps = buildProjectsModel(inputs({ budget: null }))
    const noCaps = withoutCaps.panels.find((entry) => entry.key === 'repositories')
    expect(noCaps?.state).toBe('unavailable')
    expect(noCaps?.count).toBeNull()
    expect(noCaps?.because).toContain('the enrolled repositories are unknown')
    expect(withoutCaps.repositories.enrolled).toEqual([])
    // And the count is not a zero: it is the reading the missing document owes.
    expect(withoutCaps.repositories.enrolledCount.value).toBeNull()
    expect(withoutCaps.repositories.enrolledCount.because).toContain('enrolled')

    // A sentence the CLI printed for a refused read is carried as it was worded.
    const refused = buildProjectsModel({
      budget: null,
      accounts: null,
      secrets: null,
      sentences: { 'budget.show': 'permission denied' },
    })
    const entry = refused.panels.find((candidate) => candidate.key === 'repositories')
    expect(entry?.because).toContain('permission denied')
  })

  it('names the one document the repositories panel reads', () => {
    const model = buildProjectsModel(inputs())
    const entry = model.panels.find((candidate) => candidate.key === 'repositories')
    expect(entry?.source).toContain('fugaro budget show --json')
    // The run list is gone from this screen, so no panel names it and no panel
    // has more than one source.
    expect(entry?.source).not.toContain('runs ls')
    expect(model.panels.filter((candidate) => (candidate.source ?? '').includes('runs ls'))).toHaveLength(0)
    expect(model.panels.filter((candidate) => (candidate.source ?? '').includes('+'))).toHaveLength(0)
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

  it('reads nothing out of a document it was not given, and says so per panel', () => {
    const model = buildProjectsModel({ budget: null, accounts: null, secrets: null, sentences: {} })
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
    expect(model.repositories.enrolled).toEqual([])
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

  it('puts a secret label\'s key through the sanitizer, like its value', () => {
    // The key is `Object.entries(labels)` of the document: a string the
    // document chose, not the console's vocabulary. A token-shaped key is
    // redacted in place, and a key carrying a control or an invisible character
    // is not drawn as itself.
    const token = githubToken()
    const hidden = ['managed', '\u202e', 'by', '\u0007'].join('')
    const secrets = [{ ...SECRETS[0], labels: { [token]: 'fugaro', [hidden]: 'fugaro' } }]
    const tree = renderProjects(factory, buildProjectsModel(inputs({ secrets })), 'light')
    expect(drawn(tree)).not.toContain(token)
    const keys = all(panel(tree, 'secrets'), 'projects-secret-label-key').map((node) => node.textContent ?? '')
    expect(keys).toHaveLength(2)
    expect(keys[0]).toBe(TOKEN_PLACEHOLDER)
    expect(keys[1]).not.toBe(hidden)
    expect(keys[1]).not.toMatch(/[\u0000-\u001f\u202a-\u202e]/)
  })

  it('writes no document string into an attribute without the sanitizer', () => {
    // A cap row's slug is the document's string, and an attribute is as much
    // the document's DOM as a text cell: a token-shaped slug, or one carrying a
    // control or an invisible character, may not reach any attribute raw.
    const token = githubToken()
    const hidden = ['r-1a2b3c4d/r-', '‮', '5e6f7a8b', '\u0007'].join('')
    for (const slug of [token, hidden]) {
      const budget = { ...BUDGET, repos: [{ ...BUDGET.repos[0], slug }, BUDGET.repos[1]] }
      const tree = renderProjects(factory, buildProjectsModel(inputs({ budget })), 'light')
      const values = walk(tree).flatMap((node) => [...node.attributes.values()])
      for (const value of values) {
        expect(value).not.toContain(token)
        expect(value).not.toMatch(/[\u0000-\u001f‪-‮]/)
      }
    }
  })

  it('draws every person field in the person class, so no address shape leaks its local part', () => {
    // An operator who killed a repository and a Claude account are people. The
    // `person` class refuses what is not a bare address or a login; a `label`
    // would draw a quoted local part, or a display name beside the marker, as it
    // arrived. Built at run time, on a reserved host.
    const local = ['ana', 'lopez'].join('.')
    const shapes = [`"${local}"@example.invalid`, `Ana Lopez <${local}@example.invalid>`]
    for (const who of shapes) {
      const kill = { ...BUDGET.repos[0]?.kill, by: who }
      const budget = { ...BUDGET, repos: [{ ...BUDGET.repos[0], kill }, BUDGET.repos[1]] }
      const accounts = { ...ACCOUNTS, accounts: [who] }
      const tree = renderProjects(factory, buildProjectsModel(inputs({ budget, accounts })), 'light')
      for (const className of ['projects-kill-by', 'projects-account']) {
        const cells = all(tree, className)
        expect(cells.length, className).toBeGreaterThan(0)
        for (const leaf of cells) expect(leaf.textContent?.toLowerCase(), `${className} for ${who}`).not.toContain('lopez')
      }
      expect(drawn(tree).toLowerCase()).not.toContain('lopez')
    }
  })

  it('puts the CLI\'s refusal sentence through the refusal door, not through `putText`', () => {
    // A refused read's reason is the CLI's stderr sentence: text the console did
    // not write, so a credential echoed into it is redacted, not drawn. Review
    // round 5 of PR 478: it goes through `sanitizeRefusal` rather than the
    // `sentence` class, because the class would also redact the remedy -- this
    // panel and the refused-read screen drew the same channel two ways.
    const token = githubToken()
    const sentence = `permission denied for ${token}`
    const tree = renderProjects(
      factory,
      buildProjectsModel(inputs({ budget: null, sentences: { 'budget.show': sentence } })),
      'light',
    )
    expect(drawn(tree)).not.toContain(token)
    const reasons = all(panel(tree, 'caps'), 'projects-because').map((node) => node.textContent ?? '')
    expect(reasons).toEqual([`permission denied for ${TOKEN_PLACEHOLDER}`])
  })

  it('draws the CLI\'s remedy in a refusal verbatim, as the refused-read screen does', () => {
    // Design section 6.3: "The CLI's own message, verbatim, plus the one-line
    // remedy the CLI prints ... the console shows that text unedited." Through
    // `putText` this panel drew `[redacted]` where the role name belongs.
    const sentence =
      "you need the Cloud Datastore Viewer role (roles/datastore.viewer) on project sandbox-db"
    const tree = renderProjects(
      factory,
      buildProjectsModel(inputs({ budget: null, sentences: { 'budget.show': sentence } })),
      'light',
    )
    const reasons = all(panel(tree, 'caps'), 'projects-because').map((node) => node.textContent ?? '')
    expect(reasons).toEqual([sentence])
    // The `sentence` class is what would have eaten it, asserted rather than
    // argued: `roles/datastore.viewer` is two long segments around a slash.
    const through = sanitizeText(sentence, 'sentence', 'the CLI sentence')
    expect(through.ok && through.text).not.toBe(sentence)
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

  it('draws an unavailable repositories panel as its reason, with no list and no zero', () => {
    const tree = renderProjects(factory, buildProjectsModel(inputs({ budget: null })), 'light')
    const drawn_ = panel(tree, 'repositories')
    expect(drawn_.getAttribute('data-state')).toBe('unavailable')
    // No count, no card at all, and no claim about a project nobody read.
    expect(all(drawn_, 'projects-count')).toEqual([])
    expect(all(drawn_, 'projects-repo-card')).toEqual([])
    expect(all(drawn_, 'projects-repo-enrolled')).toEqual([])
    const text = drawnText(drawn_)
    expect(text).toContain('enrolled repositories are unknown')
    expect(text).toContain('fugaro budget show --json')
    expect(text).not.toContain('none')
  })

  it('draws a kill with its operator, its time and its reason', () => {
    const tree = renderProjects(factory, buildProjectsModel(inputs()), 'light')
    const kill = one(tree, 'projects-kill')
    expect(drawn(kill)).toContain('KILLED by')
    expect(drawn(kill)).toContain('spend rate')
    // The project's own switch reads as the measured fact it is -- the word
    // `live` -- and never as a missing value, which `n/a` would say.
    expect(texts(panel(tree, 'kill'), 'projects-kill-state')).toEqual(['live', 'KILLED', 'live'])
  })

  it('draws each switch\'s state as a word on its row, and the killed row\'s word twice', () => {
    // **The mutation this case is proven against is the defect itself**: draw a
    // killed row without its `KILLED` word and it fails. A row's state is never
    // the frame's tone alone (design section 3.5): every row carries a word, the
    // word and the `data-killed` hook agree, and the killed row says `KILLED` in
    // its state cell and in its banner, which also names who killed it.
    const tree = renderProjects(factory, buildProjectsModel(inputs()), 'light')
    const rows = all(tree, 'projects-kill-row')
    expect(rows).toHaveLength(3)
    expect(rows.map((row) => one(row, 'projects-kill-state').textContent)).toEqual(['live', 'KILLED', 'live'])
    rows.forEach((row, at) => {
      const killed = at === 1
      expect(row.getAttribute('data-killed')).toBe(killed ? 'true' : 'false')
      expect(one(row, 'projects-kill-state').textContent).toBe(killed ? 'KILLED' : 'live')
      const banner = all(row, 'projects-kill-word')
      if (!killed) {
        expect(banner).toEqual([])
        return
      }
      // The word, and then the three facts when the document has them.
      expect(banner[0]?.textContent).toBe('KILLED by')
      expect(all(row, 'projects-kill-by')).toHaveLength(1)
      expect(all(row, 'projects-kill-at')).toHaveLength(1)
      expect(all(row, 'projects-kill-reason')).toHaveLength(1)
    })
    // Every row's write is drawn and refused: Kill where the row is live,
    // Revive where it is killed.
    const actions = all(tree, 'projects-kill-action')
    expect(actions.map((action) => drawn(action))).toEqual(['Kill', 'Revive', 'Kill'])
    for (const action of actions) {
      expect(action.getAttribute('aria-disabled')).toBe('true')
      expect(action.getAttribute('title')).toBe('Not available in this release')
    }
  })

  it('draws the two caps of every scope in labelled cells, with the word each binds by', () => {
    // The table is a real one -- a column heading for each cap, a row per scope
    // -- so no figure is a bare number in a column: each cell carries the cap
    // and, when the CLI named one, the word `daily_cap_source` /
    // `per_run_cap_source` gave for where it binds.
    const tree = renderProjects(factory, buildProjectsModel(inputs()), 'light')
    expect(texts(tree, 'projects-cap-head')).toEqual(['scope', 'daily cap', 'per-run cap'])
    const rows = all(tree, 'projects-cap')
    expect(rows.map((row) => row.getAttribute('data-scope'))).toEqual(['global', 'defaults', 'repo', 'repo'])
    expect(texts(rows[2] as FakeScreen, 'projects-cap-scope')).toEqual(['example-owner/example-repo'])
    expect(texts(rows[2] as FakeScreen, 'projects-cap-value')).toEqual(['$20.00', '$4.00'])
    expect(texts(rows[2] as FakeScreen, 'projects-cap-source')).toEqual(['repo', 'global'])
    // A scope the document gives no cap for draws the console's mark and the
    // CLI's own word for a cap that binds nowhere (`none`), and never a zero.
    expect(texts(rows[3] as FakeScreen, 'projects-cap-value')).toEqual(['n/a', 'n/a'])
    expect(texts(rows[3] as FakeScreen, 'projects-cap-source')).toEqual(['none', 'none'])
    // The largest single lease sits above the table, labelled.
    expect(drawn(one(tree, 'projects-reserve'))).toContain('largest single lease $5.00')
  })

  it('draws the repository set as one card, with the console\'s own `none` when it is empty', () => {
    const tree = renderProjects(factory, buildProjectsModel(inputs()), 'light')
    const cards = all(panel(tree, 'repositories'), 'projects-repo-card')
    // One card: the cap document's own set. The three cards were the
    // enrolled-and-seen split, and every one of them needed the run list this
    // screen no longer reads.
    expect(cards.map((card) => card.getAttribute('data-kind'))).toEqual(['enrolled'])
    expect(texts(cards[0] as FakeScreen, 'projects-repo-enrolled')).toEqual([
      'example-owner/example-repo',
      'example-owner/other-repo',
    ])
    expect(all(cards[0] as FakeScreen, 'projects-repo-add')).toEqual([])

    // A project whose cap document names no repository draws the console's own
    // word rather than an empty list.
    const empty = buildProjectsModel({ ...inputs(), budget: { ...BUDGET, repos: [] } })
    const none = all(panel(renderProjects(factory, empty, 'light'), 'repositories'), 'projects-repo-card-none')
    expect(texts(none[0] as FakeScreen, 'projects-repo-card-none')).toEqual(['none'])
  })

  it('draws the accounts panel\'s own list, its `none` and the document\'s sentence', () => {
    const tree = renderProjects(factory, buildProjectsModel(inputs()), 'light')
    const accounts = panel(tree, 'accounts')
    // A Claude account is a person: an address is drawn as the marker and never
    // as itself.
    expect(texts(accounts, 'projects-account')).toEqual([ADDRESS_PLACEHOLDER])
    expect(all(accounts, 'projects-account-none')).toEqual([])
    // A complete document with no account draws the console's own `none` -- and
    // a sentence the document itself carries, when it carries one.
    const empty = renderProjects(
      factory,
      buildProjectsModel(inputs({ accounts: { repo: null, accounts: [], complete: true, note: 'no account is stored on this project' } })),
      'light',
    )
    expect(texts(empty, 'projects-account-none')).toEqual(['none'])
    expect(texts(empty, 'projects-accounts-note')).toEqual(['no account is stored on this project'])
    // `complete: false` is the project config that could not be selected: the
    // document's own sentence is the **panel's reason** rather than a body line
    // (the panel is `unavailable`), and the empty list is that sentence's fact
    // and not a project with no accounts -- so no `none` is drawn.
    const absent = renderProjects(
      factory,
      buildProjectsModel(inputs({ accounts: { repo: null, accounts: [], complete: false, note: 'the project could not be selected' } })),
      'light',
    )
    expect(panel(absent, 'accounts').getAttribute('data-state')).toBe('unavailable')
    expect(drawn(panel(absent, 'accounts'))).toContain('the project could not be selected')
    expect(all(absent, 'projects-account-none')).toEqual([])
  })

  it('draws "all managed by fugaro" only where every entry of a document says so', () => {
    // The sentence is a claim about every entry, so it takes the fact about
    // every entry -- and the panel has no document at all in this build
    // (`entry.ts` hands it `null`), which draws no body and so no sentence.
    const managed = renderProjects(factory, buildProjectsModel(inputs()), 'light')
    expect(texts(managed, 'projects-secrets-managed')).toEqual(['All managed by fugaro.'])
    expect(texts(managed, 'projects-secret-bound-word')).toEqual(['bound to'])
    const foreign = [{ ...SECRETS[0], labels: { managed_by: 'another-tool' } }]
    const mixed = renderProjects(factory, buildProjectsModel(inputs({ secrets: foreign })), 'light')
    expect(all(mixed, 'projects-secrets-managed')).toEqual([])
    const none = renderProjects(factory, buildProjectsModel(inputs({ secrets: null })), 'light')
    expect(panel(none, 'secrets').getAttribute('data-state')).toBe('unavailable')
    expect(all(panel(none, 'secrets'), 'projects-secrets')).toEqual([])
  })

})

/** drawnText is the text of one subtree, in document order. */
function drawnText(node: ScreenElement): string {
  return walk(node)
    .map((leaf) => leaf.textContent ?? '')
    .filter((text) => text !== '')
    .join(' ')
}

// ---------------------------------------------------------------------------
// buildRunModel -- the Run detail screen's model, which the plan's file list
// puts here (there is no `run.test.ts` in this task's Files).
// ---------------------------------------------------------------------------

/** DIAGNOSIS is `diagnose RUN --json`, invented: a finished run whose row
 * carries the Bitbucket-shaped `pr_url` and the `pr` number, and whose
 * diagnosis carries the halt, the verify records and the failed tests. */
const DIAGNOSIS = {
  project: 'example-project',
  row: {
    run: '20261015-110030-4a5b',
    run_id: '4a5b',
    repo: 'example-owner/example-repo',
    workflow: 'default',
    oauth_account: 'claude-ops@example.test',
    recipe: 'default',
    status: 'finished',
    stage: 'done',
    reason: 'the run finished',
    branch: 'fugaro/20261015-110030-4a5b',
    outcome: 'success',
    requested_by: 'operator@example.test',
    pr: 42,
    pr_url: 'https://bitbucket.org/example-owner/example-repo/pull-requests/42',
    log_url: 'https://console.cloud.google.com/batch/jobs/example-job',
    created: '2026-10-15T11:00:00Z',
    started_at: '2026-10-15T11:00:30Z',
    image_age_s: 3600,
    terminal: true,
    settled: true,
    follow_up: false,
    stale_draft: false,
    cost: { model_usd: 1.25, compute_usd: 0.5, compute_estimated: true, total_usd: 1.75, model_basis: 'api-list' },
  },
  halt: { reason: 'the daily cap was reached', at: '2026-10-15T11:30:00Z' },
  // One `verify.Record`, with the keys the struct actually carries
  // (`internal/verify/verify.go:110`): `kind` is the stage's name, `passed` its
  // outcome, and no stage carries a spend or a deadline.
  verify: [
    {
      n: 1,
      kind: 'test',
      head_sha: '0f1e2d3c4b5a69788796a5b4c3d2e1f0a1b2c3d4',
      clean_tree: true,
      exit_code: 0,
      passed: true,
      tests: 1182,
      failures: 0,
      skipped: 2,
      started_at: '2026-10-15T11:10:00Z',
      duration_s: 42,
    },
  ],
  failed: ['TestSpend'],
  flaky: [],
  findings: [],
  log_tail: ['verify: TestSpend failed'],
  report_path: 'runs/example/20261015-110030-4a5b/report.json',
}

/** LOG_LINES is `logs RUN --json`: one compact `logLine` object per line, as
 * the contract describes it (section 6.2), parsed here as a list. */
function logLines(count: number): readonly Record<string, unknown>[] {
  return Array.from({ length: count }, (_, index) => ({
    time: `2026-10-15T11:0${String(index % 10)}:00Z`,
    severity: 'info',
    stage: 'verify',
    message: `line ${String(index)}`,
  }))
}

/** runInputs is a complete set of run inputs. */
function runInputs(overrides: Partial<RunInputs> = {}): RunInputs {
  return {
    diagnosis: DIAGNOSIS,
    logs: logLines(3),
    logLink: 'https://console.cloud.google.com/batch/jobs/example-job',
    sentences: {},
    ...overrides,
  }
}

// ---------------------------------------------------------------------------
// The read-only models panel (console 0.119.0).
// ---------------------------------------------------------------------------

/** MODELS is `models ls --json` (contract section 6.18), invented: two routes
 * -- the direct one open to every repository, a gateway one with an allow-list
 * and a fee -- and two models, one of them with no price at all. */
const MODELS = {
  version: 1,
  project: 'example-project',
  catalog: { source: 'https://catalog.example.test/models.json', checked_at: '2026-10-01', age_days: 33, stale: true },
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
    {
      name: 'gateway',
      kind: 'openai',
      base_url: 'https://gateway.example.test/v1',
      host: 'gateway.example.test',
      models: ['deepseek-v4.1-flash'],
      route_fee_pct: 12.5,
      allow_data_to: ['example-owner/example-repo'],
      open_to_every_repo: false,
      note: 'the provider margin is added on this route',
    },
  ],
  models: [
    {
      id: 'deepseek-v4.1-flash',
      route: 'gateway',
      source: 'catalog',
      input_per_m: 0.14,
      output_per_m: 0.28,
      catalog: null,
      allowed_to: ['*'],
      refused: '',
      note: '',
    },
    { id: 'claude-sonnet-5', route: '', source: 'unpriced', input_per_m: null, output_per_m: null, catalog: null, allowed_to: [], refused: 'no route serves it', note: '' },
  ],
  repositories: [],
}

/** modelsDocument is the fixture **through the console's own decoder**, so the
 * panel is drawn from the shape the console will really hold and not from the
 * fixture's spelling. */
function modelsDocument(raw: Record<string, unknown> = MODELS): ModelsListView {
  const decoded = decodeModelsList(raw)
  if (!decoded.ok) throw new Error(`the models fixture is not a document the console reads: ${decoded.skew.because}`)
  return decoded.value
}

/** modelsInputs is a complete set of models inputs: the document above and the
 * current CLI. */
function modelsInputs(overrides: Partial<ProjectsModelsInputs> = {}): ProjectsModelsInputs {
  return { document: modelsDocument(), because: null, tooOld: false, version: '1.2.3', ...overrides }
}

/** modelsTree draws the models panel into a host of its own, the way the entry
 * does. */
function modelsTree(inputs: ProjectsModelsInputs): FakeScreen {
  const host = new FakeScreen('div')
  fillProjectsModels(factory, buildProjectsModels(inputs), host)
  return host
}

describe('the read-only models panel', () => {
  it('says a route open to every repository in words, and draws an allow-list as its own list', () => {
    const tree = modelsTree(modelsInputs())
    expect(texts(tree, 'projects-models-to-every')).toEqual(['open to every repository'])
    // The allow-listed route draws the repository itself and the console's word
    // for "every one of them" nowhere: the flag is the document's, and a route
    // that lists one repository is not open to the others.
    expect(texts(tree, 'projects-models-to-every')).toHaveLength(1)
    expect(texts(tree, 'projects-models-allow')).toEqual(['example-owner/example-repo'])
    expect(texts(tree, 'projects-models-to-word')).toEqual(['may send data to', 'may send data to'])
    // **The host is the console's marker, and not the machine's own name.**
    // `routes[].host` is `host` in the decoder's table (`WIRE_TEXT_CLASSES`), and
    // that class returns `HOST_PLACEHOLDER` for every machine name it is given,
    // valid or not (`sanitize.ts`) -- which is the console's rule for a machine
    // and the recorder's too. No other class would draw the name either: `label`
    // and `sentence` run the free-text rules, and their hostname rule replaces
    // one before any class branch reads the value. The route is recognised
    // without the host: its `name` is the document's own word for it, and the
    // full hostname is drawn inside the `base_url` beside it.
    expect(texts(tree, 'projects-models-route-host')).toEqual(['<host>', '<host>'])
    // A route that claims no model at all: the panel's own word, not a blank.
    const empty = modelsTree(
      modelsInputs({
        document: modelsDocument({
          ...MODELS,
          routes: [{ ...MODELS.routes[1], models: [], allow_data_to: [], open_to_every_repo: false }],
        }),
      }),
    )
    expect(texts(empty, 'projects-models-to-none')).toEqual(['no repository'])
    expect(drawn(empty)).toContain('claims')
  })

  it('draws an absent price as `n/a` and never as a zero', () => {
    const tree = modelsTree(modelsInputs())
    const inputs = texts(tree, 'projects-models-input')
    expect(inputs).toHaveLength(2)
    expect(inputs[1]).toBe(UNAVAILABLE_MARK)
    // The price the document carried is drawn as money, and the row it left out
    // is not a `$0` anywhere: an unknown model is not a free one.
    expect(inputs[0]).toBe('$0.14')
    expect(texts(tree, 'projects-models-output')[1]).toBe(UNAVAILABLE_MARK)
    expect(drawn(tree)).not.toContain('$0.00')
    // A model no route serves says so in the console's own words.
    expect(texts(tree, 'projects-models-route-of')).toEqual(['gateway', 'no route'])
  })

  it('draws a hostile `base_url` and `note` as inert text and builds no link', () => {
    const hostile = {
      ...MODELS,
      routes: [
        {
          ...MODELS.routes[1],
          base_url: 'https://gateway.example.test/‮reversed',
          note: '<b>bold</b>\nnext <a href="https://evil.example.test">go</a>',
        },
        { ...MODELS.routes[0], base_url: '<img src=x onerror=alert(1)>', note: 'javascript:alert(1)' },
      ],
    }
    const tree = modelsTree(modelsInputs({ document: modelsDocument(hostile) }))
    // The bidi override and the newline are neutralised, and the markup is
    // **characters**: it is drawn through `textContent`, so no element the
    // document wrote exists and nothing is clickable.
    expect(texts(tree, 'projects-models-route-base')).toEqual([
      'https://gateway.example.test/reversed',
      UNAVAILABLE_MARK,
    ])
    const note = texts(tree, 'projects-models-route-note')[0] ?? ''
    expect(note).toContain('<b>bold</b> next')
    // The bidi override and the newline are gone: what is drawn is one
    // flattened run of inert characters, whatever the document wrote.
    expect([...note].some((one) => one.charCodeAt(0) < 0x20 || one === '‮')).toBe(false)
    expect(
      walk(tree).filter((node) => node.tag === 'a' || node.tag === 'b' || node.tag === 'img'),
      'a document string became an element',
    ).toEqual([])
    expect(
      walk(tree).filter((node) => node.getAttribute('href') !== null || node.getAttribute('src') !== null),
    ).toEqual([])
    // A `base_url` the url class will not draw is the mark, and the sentence
    // about why is a title and not the hostile string.
    expect(texts(tree, 'projects-models-route-base')).not.toContain('<img src=x onerror=alert(1)>')
  })

  it('holds no control that writes, and no contenteditable subtree', () => {
    const tree = modelsTree(modelsInputs())
    const WRITERS = new Set(['input', 'textarea', 'select', 'button', 'form'])
    expect(
      walk(tree).filter((node) => WRITERS.has(node.tag)),
      'the models panel is read-only: nothing here adds, removes or uses a model',
    ).toEqual([])
    expect(walk(tree).filter((node) => node.getAttribute('contenteditable') !== null)).toEqual([])
    // And the panel draws the console's own read words: a source line naming
    // `models ls`, and the count of what it drew.
    expect(drawn(tree)).toContain('fugaro models ls --json')
  })

  it('draws the console’s own sentence below the floor, and no list at all', () => {
    const tree = modelsTree(modelsInputs({ document: null, because: null, tooOld: true, version: '0.15.1' }))
    const panel = all(tree, 'projects-panel')[0]
    expect(panel?.getAttribute('data-state')).toBe('unavailable')
    expect(drawn(tree)).toContain('the models read needs fugaro 0.16.0 and this CLI is 0.15.1')
    expect(all(tree, 'projects-models-list')).toEqual([])
    expect(all(tree, 'projects-models-route-list')).toEqual([])
  })

  it('draws the catalog’s age and its staleness in words', () => {
    const tree = modelsTree(modelsInputs())
    expect(texts(tree, 'projects-models-catalog-checked')).toEqual(['2026-10-01'])
    expect(texts(tree, 'projects-models-catalog-age')).toEqual(['33'])
    expect(texts(tree, 'projects-models-catalog-stale')).toEqual(['stale'])
    const fresh = modelsTree(
      modelsInputs({
        document: modelsDocument({ ...MODELS, catalog: { ...MODELS.catalog, stale: false, age_days: 3 } }),
      }),
    )
    expect(texts(fresh, 'projects-models-catalog-stale')).toEqual(['current'])
  })
})

// ---------------------------------------------------------------------------
// The settings section, and the pencils' row set (the settings-edit plan's C1).
// ---------------------------------------------------------------------------

/** SETTINGS is `settings --json` (contract section 6.19), invented: four rows,
 * one per case the screen has to tell apart. The first is writable with a
 * target layer and gets a pencil; the second is owned by a family command and
 * names it instead; the third is sensitive and is **not drawn at all**; the
 * fourth is writable with **no** target layer, which is a row the generic path
 * would not write, so it gets no pencil either. */
const SETTINGS = {
  version: 1,
  project: 'example-project',
  generated_at: '2026-11-03T06:00:00Z',
  sources: [{ layer: 'default', read_at: '2026-11-03T06:00:00Z', duration_ms: 0, status: 'ok' }],
  settings: [
    {
      key: 'budget.per_run_usd',
      scope: 'project',
      value: '8',
      wins: 'project',
      default: { value: '10', source: 'settings.DefaultPerRunUSD' },
      state: 'ok',
      writable: true,
      owner: '',
      target_layer: 'project',
    },
    {
      key: 'agent.models.coder',
      scope: 'project',
      value: 'example-model',
      wins: 'default',
      default: { value: 'example-model', source: 'settings.DefaultCoder' },
      state: 'ok',
      writable: false,
      owner: 'fugaro models',
      target_layer: '',
    },
    {
      key: 'credentials.example_key_name',
      scope: 'project',
      value: 'a-value-the-table-must-not-draw',
      wins: 'local',
      default: { unset: 'no value is stored' },
      state: 'ok',
      sensitive: true,
      writable: false,
      owner: '',
      target_layer: '',
    },
    {
      key: 'budget.mode',
      scope: 'project',
      value: 'enforce',
      wins: 'project',
      default: { value: 'observe', source: 'settings.DefaultMode' },
      state: 'ok',
      writable: true,
      owner: '',
      target_layer: '',
    },
  ],
}

/** settingsInputs is a complete set of settings inputs: the document above. */
function settingsInputs(overrides: Partial<ProjectsSettingsInputs> = {}): ProjectsSettingsInputs {
  return { raw: SETTINGS, because: null, ...overrides }
}

/** settingsTree draws the settings panel into a host of its own, the way the
 * entry does. */
function settingsTree(inputs: ProjectsSettingsInputs): FakeScreen {
  const host = new FakeScreen('div')
  fillProjectsSettings(factory, buildProjectsSettings(inputs), host)
  return host
}

/** settingsRow is the row drawn for one key, asserted to be single. The key is
 * the row's own first cell and never an attribute: a key is a dotted string a
 * document chose, and the screen sanitizes every string it draws rather than
 * putting one in an attribute. */
function settingsRow(node: ScreenElement, key: string): FakeScreen {
  const found = all(node, 'projects-settings-row').filter((row) =>
    all(row, 'projects-settings-key').some((cell) => cell.textContent === key),
  )
  expect(found, `expected exactly one settings row for ${key}`).toHaveLength(1)
  const first = found[0]
  if (first === undefined) throw new Error(`no settings row ${key}`)
  return first
}

/** settingsKeys is every row's key, in the order the table draws them. */
function settingsKeys(node: ScreenElement): readonly (string | null)[] {
  return texts(node, 'projects-settings-key')
}

/** DOC_SHAPE_REASON is the console's own sentence for a `settings` member that
 * is not a list of rows, and ROW_SHAPE_REASON is its sentence for a row that
 * carries a member of a type the contract does not give it. Both are the
 * screen's words and not the CLI's -- no CLI prints either -- so they are
 * pinned here as text, the way the version sentence is below. */
const DOC_SHAPE_REASON =
  'settings --json is not a document this console reads: its settings member is not a list of rows'
const ROW_SHAPE_REASON =
  'settings --json carried a row this console does not read: a member the table draws is not the type the contract gives it, so no row of it is drawn'

/** settingsWithRow is the document above with **one** row: the first row, with
 * one member replaced. It is how the two shape rules are tested one member at a
 * time without writing out a document per case. */
function settingsWithRow(member: string, value: unknown): unknown {
  const first = SETTINGS.settings[0] as unknown as Record<string, unknown>
  return { ...SETTINGS, settings: [{ ...first, [member]: value }] }
}

describe('the settings section', () => {
  it('draws one row per setting the document carries, in its own order', () => {
    const tree = settingsTree(settingsInputs())
    expect(settingsKeys(tree)).toEqual([
      'budget.per_run_usd',
      'agent.models.coder',
      'budget.mode',
    ])
    expect(texts(tree, 'projects-settings-key')).toEqual([
      'budget.per_run_usd',
      'agent.models.coder',
      'budget.mode',
    ])
    expect(texts(tree, 'projects-settings-value')).toEqual(['8', 'example-model', 'enforce'])
  })

  it('gives a writable row with a target layer a pencil that names its key, and no other row one', () => {
    const tree = settingsTree(settingsInputs())
    const row = settingsRow(tree, 'budget.per_run_usd')
    expect(all(row, 'projects-settings-pencil')).toHaveLength(1)
    // D1116: the pencil is a real control, in the tab order, and its accessible
    // name names the row it edits -- a column of six identical "Edit"s names
    // none of them. It is **not** `aria-disabled`: the release that draws this
    // pencil is the one that binds it (the entry's own seam, below).
    const pencil = one(row, 'projects-settings-pencil')
    expect(pencil.tag).toBe('button')
    expect(pencil.getAttribute('type')).toBe('button')
    expect(pencil.getAttribute('aria-label')).toBe('Edit budget.per_run_usd')
    expect(pencil.getAttribute('aria-disabled')).toBeNull()
    expect(pencil.getAttribute('disabled')).toBeNull()
    expect(pencil.getAttribute('tabindex')).toBeNull()
    // The key travels on the button as well, because the entry binds the click
    // on the element the drawing answered with and has no row of its own.
    expect(pencil.getAttribute('data-setting-key')).toBe('budget.per_run_usd')
    // The state is never colour alone (styling design section 3.5): the control
    // says what it does in a word, and the word is the button's own text.
    expect(pencil.textContent).toBe('Edit')
    // The owned row, and the writable row with no target layer, each draw none.
    expect(all(settingsRow(tree, 'agent.models.coder'), 'projects-settings-pencil')).toEqual([])
    expect(all(settingsRow(tree, 'budget.mode'), 'projects-settings-pencil')).toEqual([])
  })

  it('names the pencil with the key as the sanitizer drew it, never with the raw text', () => {
    // The key reaches an **attribute** on the pencil and the sanitizer is the
    // door it goes through, the same door the key's own cell uses: a key this
    // build will not draw as a key is the console's own mark in the name and in
    // the attribute alike, so no document string rides into either.
    const tree = settingsTree(
      settingsInputs({
        raw: {
          ...SETTINGS,
          settings: [
            {
              ...(SETTINGS.settings[0] as unknown as Record<string, unknown>),
              key: 'budget per run',
            },
          ],
        },
      }),
    )
    const pencil = one(tree, 'projects-settings-pencil')
    expect(pencil.getAttribute(SETTINGS_PENCIL_KEY)).toBe(UNAVAILABLE_MARK)
    expect(pencil.getAttribute('aria-label')).toBe(`Edit ${UNAVAILABLE_MARK}`)
  })

  it('draws the row with no pencil when the read carries no `writable` member', () => {
    // The owner's ruling of 2026-11-03: the console keeps no floor of its own
    // for this screen. A CLI whose read carries none of the three writability
    // members (`writable`, `owner`, `target_layer`, the D1102 half of the row)
    // draws the table it can -- key, value, wins, state -- and **no pencil, and
    // no sentence about one**: an absent member is a CLI that does not offer the
    // edit, which is not the same fact as a row that refuses it.
    const row = {
      key: 'budget.per_run_usd',
      scope: 'project',
      value: '8',
      wins: 'project',
      state: 'ok',
    }
    const tree = settingsTree(settingsInputs({ raw: { ...SETTINGS, settings: [row] } }))
    expect(settingsKeys(tree)).toEqual(['budget.per_run_usd'])
    expect(texts(tree, 'projects-settings-value')).toEqual(['8'])
    expect(all(tree, 'projects-settings-pencil')).toEqual([])
    expect(texts(tree, 'projects-settings-owner')).toEqual([])
    expect(drawn(tree)).not.toContain(ROW_SHAPE_REASON)
    // Each of the three on its own: none of them is required to draw the row,
    // and the pencil goes when the member that decides it goes. `owner` is the
    // exception -- it says which family writes a row the generic path may not,
    // so a writable row with a target layer keeps its pencil without it.
    for (const member of ['writable', 'owner', 'target_layer']) {
      const absent = settingsTree(
        settingsInputs({
          raw: {
            ...SETTINGS,
            settings: [
              Object.fromEntries(
                Object.entries(SETTINGS.settings[0] as unknown as Record<string, unknown>).filter(
                  ([name]) => name !== member,
                ),
              ),
            ],
          },
        }),
      )
      expect(settingsKeys(absent), `${member} absent dropped the row`).toEqual(['budget.per_run_usd'])
      expect(all(absent, 'projects-settings-pencil'), `${member} absent drew a pencil`).toHaveLength(
        member === 'owner' ? 1 : 0,
      )
    }
  })

  it('names the family that writes an owned row, in the command’s own words', () => {
    const tree = settingsTree(settingsInputs())
    expect(texts(tree, 'projects-settings-owner')).toEqual(['fugaro models'])
    expect(texts(tree, 'projects-settings-owner-word')).toEqual(['written by '])
    expect(texts(tree, 'projects-settings-owner-generic')).toEqual([', not by the generic path'])
  })

  it('a Sensitive row is absent from the rendered table', () => {
    const tree = settingsTree(settingsInputs())
    // The owner's ruling of 2026-11-01 (D1111): a sensitive row is never
    // editable and **never shown**, so the key, the value and the pencil are
    // all absent -- not redacted, not blanked: absent.
    expect(settingsKeys(tree)).not.toContain('credentials.example_key_name')
    expect(drawn(tree)).not.toContain('credentials.example_key_name')
    expect(drawn(tree)).not.toContain('a-value-the-table-must-not-draw')
  })

  it('drops a row whose `sensitive` is anything but a false one, whatever its type', () => {
    // The check **fails closed**: only an absent member or exactly `false` may
    // draw a row, so a document that spells the flag `"true"`, `1`, `"false"`,
    // `0` or `null` loses the row rather than drawing it. A coercion here would
    // draw exactly the row the ruling of 2026-11-01 (D1111) says is never shown.
    for (const sensitive of ['true', 1, 'false', 0, null, {}]) {
      const tree = settingsTree(
        settingsInputs({
          raw: {
            ...SETTINGS,
            settings: [
              {
                ...(SETTINGS.settings[0] as unknown as Record<string, unknown>),
                sensitive,
              },
            ],
          },
        }),
      )
      expect(settingsKeys(tree), `sensitive: ${JSON.stringify(sensitive)} drew a row`).toEqual([])
      expect(drawn(tree)).not.toContain('a-value-the-table-must-not-draw')
      expect(all(tree, 'projects-settings-pencil')).toEqual([])
    }
  })

  it('draws no pencil for a sensitive row that is otherwise writable, so nothing of it reaches the window', () => {
    // The one mutation proof on D1111's guard, and it is aimed at the **pencil**
    // rather than at the row: this row carries `writable: true` and a target
    // layer, so the `sensitive` check is the only thing keeping the pencil off
    // it -- remove that one check and the pencil below appears, and with it the
    // door C3 opens a dry run, a window and an argv behind. `settingsview.ts`
    // carries no member a sensitive value could arrive through; this is the half
    // that keeps such a row from ever being drawn for it. The same row without
    // the flag is the control: it draws one.
    const drawable = SETTINGS.settings[0] as unknown as Record<string, unknown>
    const sensitive = settingsTree(
      settingsInputs({ raw: { ...SETTINGS, settings: [{ ...drawable, sensitive: true }] } }),
    )
    expect(settingsKeys(sensitive)).toEqual([])
    expect(all(sensitive, 'projects-settings-pencil')).toEqual([])
    const control = settingsTree(settingsInputs({ raw: { ...SETTINGS, settings: [drawable] } }))
    expect(all(settingsRow(control, 'budget.per_run_usd'), 'projects-settings-pencil')).toHaveLength(1)
  })

  it('refuses the table rather than coercing a member of a type the contract does not give it', () => {
    // The owner's rule of 2026-10-30: unknown keys are ignored and **types are
    // strict**. Every member the table draws is checked, and a wrong one makes
    // the document an unavailable panel rather than a `''`, a `null` or a
    // `false` the CLI never printed -- a coerced owner would draw a row as
    // writable, or as owned by nobody, without a word about it.
    const wrong: readonly (readonly [string, unknown])[] = [
      ['scope', 3],
      ['value', 8],
      ['wins', false],
      ['state', null],
      ['writable', 'true'],
      ['owner', 5],
      ['target_layer', {}],
    ]
    for (const [member, value] of wrong) {
      const tree = settingsTree(settingsInputs({ raw: settingsWithRow(member, value) }))
      expect(all(tree, 'projects-settings-row'), `${member}: ${JSON.stringify(value)} drew a row`).toEqual([])
      expect(all(tree, 'projects-settings-pencil'), `${member} drew a pencil`).toEqual([])
      expect(drawn(tree), `${member} drew no reason`).toContain(ROW_SHAPE_REASON)
    }
  })

  it('refuses the document when `settings` is not a list of rows', () => {
    for (const settings of [undefined, null, {}, 'none', 0]) {
      const tree = settingsTree(settingsInputs({ raw: { ...SETTINGS, settings } }))
      expect(all(tree, 'projects-settings-row')).toEqual([])
      expect(drawn(tree), `settings: ${JSON.stringify(settings)} drew no reason`).toContain(
        DOC_SHAPE_REASON,
      )
    }
  })

  it('draws no row that has no key, or a key that is not text', () => {
    // The code matches the comment: a row is keyed by its key, so an entry with
    // no key at all -- or one whose key is a number -- is not a row of this
    // table. The other rows are drawn: a table is not refused for a row that
    // cannot be named.
    for (const key of [undefined, null, 7, {}, '']) {
      const tree = settingsTree(settingsInputs({ raw: settingsWithRow('key', key) }))
      expect(settingsKeys(tree), `key: ${JSON.stringify(key)} drew a row`).toEqual([])
      expect(drawn(tree)).not.toContain(ROW_SHAPE_REASON)
    }
    const mixed = settingsTree(
      settingsInputs({
        raw: {
          ...SETTINGS,
          settings: [
            { ...(SETTINGS.settings[0] as unknown as Record<string, unknown>), key: 7 },
            SETTINGS.settings[1],
          ],
        },
      }),
    )
    expect(settingsKeys(mixed)).toEqual(['agent.models.coder'])
  })

  it('draws a refusal rather than an empty table when no document arrived', () => {
    const tree = settingsTree(settingsInputs({ raw: null, because: 'the CLI refused the read' }))
    expect(all(tree, 'projects-settings-row')).toEqual([])
    expect(drawn(tree)).toContain('the CLI refused the read')
  })

  it('refuses a document whose version this build does not read', () => {
    const tree = settingsTree(settingsInputs({ raw: { ...SETTINGS, version: 2 } }))
    expect(all(tree, 'projects-settings-row')).toEqual([])
    expect(drawn(tree)).toContain('settings --json is version 2 and this console reads version 1')
  })

  it('draws the settings section beside the budget panels, under one frame', () => {
    const tree = renderProjects(
      factory,
      buildProjectsModel({ budget: BUDGET, accounts: null, secrets: null, sentences: {} }),
      'light',
      undefined,
      buildProjectsSettings(settingsInputs()),
    )
    expect(all(tree, 'projects-settings')).toHaveLength(1)
    expect(all(settingsRow(tree, 'budget.per_run_usd'), 'projects-settings-pencil')).toHaveLength(1)
  })
})

describe('buildRunModel', () => {
  it('reads the ledger, the stage timeline and the outcome out of the row', () => {
    const model = buildRunModel(runInputs())
    expect(model.row.run).toBe('20261015-110030-4a5b')
    expect(model.row.repo).toBe('example-owner/example-repo')
    expect(model.row.outcome).toBe('success')
    expect(model.row.status).toBe('finished')
    // The ledger is the row's own figures: the model dollars, the compute the
    // document marks as an estimate, and the two accounts apart.
    expect(model.ledger.model.value).toBe(1.25)
    expect(model.ledger.compute.value).toBe(0.5)
    expect(model.ledger.compute.state).toBe('estimated')
    expect(model.ledger.total.value).toBe(1.75)
    // Per-stage spend is a reading a verify record does not carry: the run's
    // spend by stage is the document's top-level `stages` and its own panel, so
    // the cell is n/a with the sentence that says which of the two it looked
    // for -- here the missing key, because this document has no `stages`.
    expect(model.stages).toHaveLength(1)
    expect(model.stages[0]?.stage).toBe('test')
    expect(model.stages[0]?.passed).toBe(true)
    expect(model.stages[0]?.tests.value).toBe(1182)
    expect(model.stages[0]?.spend.state).toBe('unavailable')
    expect(model.stages[0]?.spend.because).toContain('the document carries no stages')
    // And the deadline: `runview.Row` carries none, so the timeline says which
    // field it looked for rather than inventing a deadline from `started_at`.
    expect(model.stages[0]?.deadline).toBeNull()
    expect(model.stages[0]?.deadlineBecause).toBe('the document has no row.deadline')
    expect(model.halt?.reason).toBe('the daily cap was reached')
  })

  it('splits the ledger\'s model figure on `model_notional_usd`, never on the basis', () => {
    // The same rule as the Projects screen's job rows, on this screen's own
    // document (C1.16, P12/D140): `model_usd` is the whole model figure,
    // `model_notional_usd` the subscription's part of it, and the billed dollars
    // are the difference -- while a mixed cascade run's `model_basis` is
    // `api-list` for the whole run. Read through the basis, the plan's dollars
    // were the figure this ledger drew as money.
    const costRow = (cost: Record<string, unknown>) => ({ ...DIAGNOSIS, row: { ...DIAGNOSIS.row, cost } })
    const ledger = (cost: Record<string, unknown>) => buildRunModel(runInputs({ diagnosis: costRow(cost) })).ledger.model
    // Mixed: money and the plan's list price in one figure.
    const mixed = { model_usd: 3.2, model_notional_usd: 2, compute_usd: 0.5, compute_estimated: true, total_usd: 1.7, model_basis: 'api-list' }
    expect(ledger(mixed).value).toBeCloseTo(1.2)
    expect(ledger(mixed).state).toBe('measured')
    const tree = renderRun(factory, buildRunModel(runInputs({ diagnosis: costRow(mixed) })), 'light')
    expect(one(tree, 'run-ledger-model-value').textContent).toBe('$1.20')
    // All notional: the plan's figure, marked notional -- with the key (C1.16)
    // and without it (the fallback for a row written before the split).
    expect(ledger({ model_usd: 3.2, model_notional_usd: 3.2, compute_usd: 0, compute_estimated: true, total_usd: 0, model_basis: 'api-list' }).state).toBe('notional')
    expect(ledger({ model_usd: 3.2, compute_usd: 0, compute_estimated: true, total_usd: 0, model_basis: 'subscription' }).state).toBe('notional')
    // All billed: no notional part, so the whole figure is the money.
    const billed = ledger({ model_usd: 1.25, compute_usd: 0.5, compute_estimated: true, total_usd: 1.75, model_basis: 'api-list' })
    expect(billed.value).toBeCloseTo(1.25)
    expect(billed.state).toBe('measured')
  })

  it('reads a compute the document did not estimate as unavailable, not as a free zero', () => {
    // `runstore.Cost`'s own comment is the rule: `ComputeEstimated` "is true
    // only when prices and resources were known; when false, ComputeUSD is 0 and
    // means 'not estimated', not free" (`internal/runstore/cost.go:15`). A
    // measured `$0.00` in the ledger would read as a run that cost nothing to
    // compute, which is the one thing design section 10.1 forbids.
    const notEstimated = {
      ...DIAGNOSIS,
      row: { ...DIAGNOSIS.row, cost: { ...DIAGNOSIS.row.cost, compute_usd: 0, compute_estimated: false } },
    }
    const model = buildRunModel(runInputs({ diagnosis: notEstimated }))
    expect(model.ledger.compute.value).toBeNull()
    expect(model.ledger.compute.state).toBe('unavailable')
    expect(model.ledger.compute.because).toContain('not estimated')
    // The two other accounts are untouched by the flag.
    expect(model.ledger.model.value).toBe(1.25)
    expect(model.ledger.total.value).toBe(1.75)
  })

  it('draws a deadline the row does carry, and only then', () => {
    // A live `watch --json` entry carries `deadline: "near"` (contract section
    // 2); a `runview.Row` does not. The timeline reads the row it was given.
    const live = { ...DIAGNOSIS, row: { ...DIAGNOSIS.row, deadline: 'near' } }
    const model = buildRunModel(runInputs({ diagnosis: live }))
    expect(model.stages[0]?.deadline).toBe('near')
    expect(model.stages[0]?.deadlineBecause).toBeNull()
    // A word this build does not know is no deadline, not a word drawn as one.
    const unknown = { ...DIAGNOSIS, row: { ...DIAGNOSIS.row, deadline: 'tomorrow' } }
    expect(buildRunModel(runInputs({ diagnosis: unknown })).stages[0]?.deadline).toBeNull()
  })

  it('draws the reported/charged pair and the two splits in the ledger, not in the diagnosis', () => {
    // Step 3b moved them: the pair is a reconciliation against the billed total
    // and the two splits are two scopes of the same spend, so all four are read
    // beside the total they sit under rather than under the halt and the failed
    // tests. The warning that is about the pair stays verbatim with it.
    const document = {
      ...DIAGNOSIS,
      reported_usd: 1.2,
      charge_usd: 1.25,
      route_by: { 'anthropic-api': 0.9 },
      model_by: { 'claude-sonnet-5': 1.25 },
      reported_warning: 'the providers reported less than the table charged',
    }
    const tree = renderRun(factory, buildRunModel(runInputs({ diagnosis: document })), 'light')
    const ledger = one(tree, 'run-ledger')
    expect(one(ledger, 'run-reported-usd').textContent).toBe('$1.20')
    expect(one(ledger, 'run-charge-usd').textContent).toBe('$1.25')
    expect(one(ledger, 'run-recon-diff-value').textContent).toBe('$0.05')
    expect(texts(ledger, 'run-route-name')).toEqual(['anthropic-api'])
    expect(texts(ledger, 'run-model-name')).toEqual(['claude-sonnet-5'])
    expect(one(ledger, 'run-reported-warning').textContent).toBe('the providers reported less than the table charged')
    const diagnosis = one(tree, 'run-diagnosis')
    expect(all(diagnosis, 'run-route')).toHaveLength(0)
    expect(all(diagnosis, 'run-reported-usd')).toHaveLength(0)
  })

  it('builds a pull request URL per provider, for the day gap 7 lands', () => {
    // Design section 5.1 rule 2's construction, both providers, unit-tested now
    // so that the screen only has to start offering the link later. The two
    // paths differ, and that is the whole point: a Bitbucket repository built as
    // a GitHub URL passes the design's own host allow-list while naming a
    // repository the operator does not own.
    expect(pullRequestUrl('github', 'example-owner/example-repo', 42)).toBe(
      'https://github.com/example-owner/example-repo/pull/42',
    )
    expect(pullRequestUrl('bitbucket', 'example-owner/example-repo', 42)).toBe(
      'https://bitbucket.org/example-owner/example-repo/pull-requests/42',
    )
    // Nothing to build from: a repository that is not `owner/name`, a number
    // that is not a positive integer, and a number that is not there at all.
    expect(pullRequestUrl('github', 'example-repo', 42)).toBeNull()
    expect(pullRequestUrl('github', 'example-owner/example-repo', 0)).toBeNull()
    expect(pullRequestUrl('bitbucket', 'example-owner/example-repo', 1.5)).toBeNull()
  })

  it('refuses to build a link from a `pr_url` alone', () => {
    // The inference that must not creep back in: a URL the host gave is not
    // evidence of which host gave it. `linkFor` takes a provider and nothing
    // else -- it has no overload that takes the URL -- so a caller without one
    // gets null, which is what the screen draws today.
    expect(linkFor(null, 'example-owner/example-repo', 42)).toBeNull()
    // And even with the provider the console does not have, the *model* offers
    // no link: the Bitbucket-shaped `pr_url` above is text on the screen.
    const model = buildRunModel(runInputs())
    expect(model.pullRequest?.url).toBe('https://bitbucket.org/example-owner/example-repo/pull-requests/42')
    expect(model.pullRequest?.link).toBeNull()
    expect(linkFor('bitbucket', model.pullRequest?.repository ?? null, model.pullRequest?.number ?? null)).toBe(
      model.pullRequest?.url,
    )
  })

  it('shows the pull request as text, and builds no link from it', () => {
    const model = buildRunModel(runInputs())
    expect(model.pullRequest).not.toBeNull()
    expect(model.pullRequest?.repository).toBe('example-owner/example-repo')
    expect(model.pullRequest?.number).toBe(42)
    expect(model.pullRequest?.url).toBe('https://bitbucket.org/example-owner/example-repo/pull-requests/42')
    // Design section 4.3's gap 7: no document names the provider, so the console
    // offers no link. `link` is null and the reason is a sentence.
    expect(model.pullRequest?.link).toBeNull()
    expect(model.pullRequest?.because).toContain('provider')
  })

  it('renders a finished run\'s `pr_number` as text too, and a live entry with no number at all', () => {
    // The two spellings: `pr` on a `runview.Row`, `pr_number` on `JSONFinished`.
    const finished = { ...DIAGNOSIS, row: { ...DIAGNOSIS.row, pr: undefined, pr_number: 43 } }
    const model = buildRunModel(runInputs({ diagnosis: finished }))
    expect(model.pullRequest?.number).toBe(43)
    expect(model.pullRequest?.link).toBeNull()
    // A live `watch --json` entry carries neither, so no number is invented.
    const live = { ...DIAGNOSIS, row: { ...DIAGNOSIS.row, pr: undefined, pr_number: undefined, pr_url: undefined } }
    const without = buildRunModel(runInputs({ diagnosis: live }))
    expect(without.pullRequest?.number).toBeNull()
    expect(without.pullRequest?.url).toBeNull()
    expect(without.pullRequest?.link).toBeNull()
  })

  it('bounds the log panel at the cap and never follows the log', () => {
    const model = buildRunModel(runInputs({ logs: logLines(RUN_LOG_CAP + 25) }))
    expect(model.logs.lines).toHaveLength(RUN_LOG_CAP)
    expect(model.logs.dropped.value).toBe(25)
    expect(model.logs.total.value).toBe(RUN_LOG_CAP + 25)
    // The window is the **newest** lines, oldest first: a cap that kept the
    // first two hundred would show the start of a run as if it were its end.
    expect(model.logs.lines[0]?.message).toBe('line 25')
    expect(model.logs.lines.at(-1)?.message).toBe(`line ${String(RUN_LOG_CAP + 24)}`)
    // The link to the full transcript is the CLI's own `logs RUN --url` line.
    expect(model.logs.link).toBe('https://console.cloud.google.com/batch/jobs/example-job')
  })

  it('reads a ring the caller keeps, so arrivals need not be held to be redrawn', () => {
    // A ring built per render would need every arrival since the screen opened:
    // the caller keeps the bounded buffer and hands it over instead.
    const buffer = new ReplayBuffer({ maxRecords: 2 })
    for (const frame of REPLAY_FRAMES) buffer.observe(frame)
    const model = buildRunModel(runInputs({ replay: buffer, scrubAt: '2026-10-15T12:05:00Z' }))
    expect(model.replay.records.value).toBe(2)
    expect(model.replay.dropped.value).toBe(1)
    expect(model.replay.scrubbed?.at).toBe('2026-10-15T12:04:00Z')
    // Drawing it does not feed it.
    expect(buffer.bounds().records).toBe(2)
  })

  it('draws the replay buffer\'s bound, its span and the moment scrubbed to', () => {
    const model = buildRunModel(runInputs({ replay: REPLAY_FRAMES, scrubAt: '2026-10-15T12:05:00Z' }))
    expect(model.replay.records.value).toBe(REPLAY_FRAMES.length)
    expect(model.replay.from).toBe(REPLAY_FRAMES[0]?.receivedAt)
    expect(model.replay.to).toBe(REPLAY_FRAMES.at(-1)?.receivedAt)
    // The moment scrubbed to is 12:05, and the frame *in effect* then is the
    // 12:04 one -- not the nearest, and not the newest.
    expect(model.replay.scrubbed?.at).toBe('2026-10-15T12:04:00Z')
    expect(model.replay.scrubbedBecause).toBeNull()
    expect(model.replay.windowSeconds.value).toBe(900)
    // A moment before the ring starts is a moment with nothing to show, which
    // the panel says rather than drawing the first frame as if it were that one.
    const before = buildRunModel(runInputs({ replay: REPLAY_FRAMES, scrubAt: '2026-10-15T11:00:00Z' }))
    expect(before.replay.scrubbed).toBeNull()
    expect(before.replay.scrubbedBecause).toContain('2026-10-15T11:00:00Z')
    // And with no ring at all the panel draws no span and no moment.
    const none = buildRunModel(runInputs())
    expect(none.replay.records.value).toBe(0)
    expect(none.replay.from).toBeNull()
    expect(none.replay.to).toBeNull()
  })

  it('reads a run\'s rigor block out of the row, and answers null without one', () => {
    // Owner request 2026-10-25. The block is `row.rigor`, the same key the
    // Projects screen's job rows read, through the same reader
    // (`shared/rigor.ts`): the mode, the layer that decided it and the areas
    // that matched. The record's two stamps -- the moment the block was written
    // and the revision the configuration was read from -- are not carried,
    // because this row draws what the run resolved.
    const rigor = { mode: 'critical', from: 'area', areas: [{ name: 'payments', mode: 'critical' }], at: '2026-10-15T11:00:00Z', revision: 'origin/main' }
    const withBlock = { ...DIAGNOSIS, row: { ...DIAGNOSIS.row, rigor } }
    expect(buildRunModel(runInputs({ diagnosis: withBlock })).row.rigor).toEqual({
      mode: 'critical',
      from: 'area',
      areas: [{ name: 'payments', mode: 'critical' }],
    })
    // The fixture's row is a run from before the field: no block, and the model
    // says so with `null` rather than with an empty one, which is what the
    // header reads to decide whether to draw the row at all.
    expect(buildRunModel(runInputs()).row.rigor).toBeNull()
    // A block the reader will not show is the same `null`: a mode with no layer
    // is not a decision this console can describe.
    const half = { ...DIAGNOSIS, row: { ...DIAGNOSIS.row, rigor: { mode: 'pro' } } }
    expect(buildRunModel(runInputs({ diagnosis: half })).row.rigor).toBeNull()
  })

  it('spend by stage reads calls and agents absent as not counted and zero as zero', () => {
    // The document's top-level `stages` is the run's own per-stage record
    // (`runstore.StageTiming`), and `calls`/`agents` are pointers on the wire:
    // a stage nobody counted has the key absent, which is not the same fact as
    // a stage counted at zero. A screen that read the absent key as a zero
    // would say the stage made no calls.
    const stages = [
      {
        name: 'implement',
        started_at: '2026-10-15T11:01:00Z',
        duration_s: 420,
        calls: 42,
        agents: 3,
        model_usd: 1.1,
        model_notional_usd: 0,
      },
      // No `calls`, no `agents`: nobody counted this stage.
      {
        name: 'review',
        started_at: '2026-10-15T11:08:00Z',
        duration_s: 95,
        model_usd: 0.15,
        model_notional_usd: 0,
      },
      // Counted, and the count is zero.
      {
        name: 'verify',
        started_at: '2026-10-15T11:10:00Z',
        duration_s: 42,
        calls: 0,
        agents: 0,
        model_usd: 0,
        model_notional_usd: 0,
      },
    ]
    const model = buildRunModel(runInputs({ diagnosis: { ...DIAGNOSIS, stages } }))
    expect(model.spendStages.map((stage) => stage.name)).toEqual(['implement', 'review', 'verify'])
    expect(model.spendStages[0]?.calls.value).toBe(42)
    expect(model.spendStages[0]?.agents.value).toBe(3)
    expect(model.spendStages[0]?.duration.value).toBe(420)
    expect(model.spendStages[0]?.usd.value).toBe(1.1)
    expect(model.spendStages[0]?.notional.value).toBe(0)
    // The absent pair: no value, and the sentence that says which fact it is.
    expect(model.spendStages[1]?.calls.value).toBeNull()
    expect(model.spendStages[1]?.calls.because).toBe('not counted for this stage')
    expect(model.spendStages[1]?.agents.state).toBe('unavailable')
    // The counted zero: a measured zero, which is a figure the document wrote.
    expect(model.spendStages[2]?.calls.value).toBe(0)
    expect(model.spendStages[2]?.calls.state).toBe('measured')
    expect(model.spendStages[2]?.agents.value).toBe(0)
    // An entry that is not an object is skipped, never thrown on: the record is
    // an object a hand can edit. A skipped entry is an entry the panel did not
    // draw, so it is counted with the bound's overflow rather than left out
    // altogether -- otherwise the rows and the count would not be the array.
    const malformed = buildRunModel(runInputs({ diagnosis: { ...DIAGNOSIS, stages: [null, 'review', stages[0]] } }))
    expect(malformed.spendStages.map((stage) => stage.name)).toEqual(['implement'])
    expect(malformed.spendStagesDropped.value).toBe(2)
    // A document with no `stages` at all draws no panel, and the timeline says
    // which key is missing rather than naming a panel that is not there.
    const none = buildRunModel(runInputs())
    expect(none.spendStages).toEqual([])
    expect(none.spendStagesDropped.value).toBe(0)
    expect(none.stages[0]?.spend.because).toContain('the document carries no stages')
    // With stages, the timeline's spend cell names the panel instead.
    expect(model.stages[0]?.spend.because).toContain('"spend by stage" panel')
  })

  it('spend by stage splits each figure into the billed part and the plan’s part', () => {
    // `model_usd` is the stage's whole figure -- billed money plus the part a
    // plan paid -- and `model_notional_usd` is that part. The two cells are the
    // split and never a sum of it, and the split is the record's own subtraction
    // (`modelReading`'s rule, one panel above) rather than one this panel
    // invents: the fallback for a figure with no part at all belongs to the
    // billed cell alone, and applying it beside a carried part draws the same
    // figure in both cells.
    //
    // Both keys are `omitempty` floats (`internal/runstore/runstore.go`), the
    // opposite case to `calls`/`agents`: `encoding/json` writes no key for a
    // zero, so an absent part is the value zero and never an unknown. Reading it
    // as unknown would draw `n/a` for the notional part of every stage on no
    // plan.
    const stages = [
      // Both keys dropped by `omitempty`: nothing billed, nothing notional.
      { name: 'implement', duration_s: 420 },
      // A cascade stage: part of its figure is the plan's, and that part is a
      // notional figure -- `~$X`, never money (C1.16, P12). The billed
      // cell holds what is left of the figure.
      { name: 'review', duration_s: 95, model_usd: 2.1, model_notional_usd: 0.6 },
    ]
    const model = buildRunModel(runInputs({ diagnosis: { ...DIAGNOSIS, stages } }))
    expect(model.spendStages[0]?.usd.state).toBe('measured')
    expect(model.spendStages[0]?.usd.value).toBe(0)
    expect(model.spendStages[0]?.notional.state).toBe('measured')
    expect(model.spendStages[0]?.notional.value).toBe(0)
    expect(model.spendStages[1]?.usd.value).toBe(1.5)
    expect(model.spendStages[1]?.usd.state).toBe('measured')
    expect(model.spendStages[1]?.notional.value).toBe(0.6)
    expect(model.spendStages[1]?.notional.state).toBe('notional')
    // A record written before the key existed carries no part, and then the run's
    // basis is the fallback the ledger applies to the row's own figure one panel
    // above: a subscription run's whole stage figure is the plan's, so it is the
    // **usd** cell that carries it -- marked notional, because nothing was billed
    // -- and the notional cell is the zero the record carries.
    const before = buildRunModel(
      runInputs({
        diagnosis: {
          ...DIAGNOSIS,
          row: {
            ...DIAGNOSIS.row,
            cost: { ...DIAGNOSIS.row.cost, model_usd: 2.1, model_basis: 'subscription' },
          },
          stages: [{ name: 'review', duration_s: 95, model_usd: 2.1 }],
        },
      }),
    )
    expect(before.spendStages[0]?.usd.value).toBe(2.1)
    expect(before.spendStages[0]?.usd.state).toBe('notional')
    expect(before.spendStages[0]?.notional.value).toBe(0)
    expect(before.spendStages[0]?.notional.state).toBe('measured')
    // The same absent key on an api-list run is a billed figure: nothing says the
    // plan paid any of it.
    const listed = buildRunModel(
      runInputs({ diagnosis: { ...DIAGNOSIS, stages: [{ name: 'review', duration_s: 95, model_usd: 2.1 }] } }),
    )
    expect(listed.spendStages[0]?.usd.value).toBe(2.1)
    expect(listed.spendStages[0]?.usd.state).toBe('measured')
    expect(listed.spendStages[0]?.notional.value).toBe(0)
    expect(listed.spendStages[0]?.notional.state).toBe('measured')
    // A hand-edited record can say the plan paid more than the stage cost; the
    // part is read as the part of the figure it can be, which is what the
    // record's own writer clamps it to.
    const over = buildRunModel(
      runInputs({
        diagnosis: { ...DIAGNOSIS, stages: [{ name: 'review', duration_s: 95, model_usd: 0.8, model_notional_usd: 5 }] },
      }),
    )
    expect(over.spendStages[0]?.notional.value).toBe(0.8)
    expect(over.spendStages[0]?.usd.value).toBe(0)
    expect(over.spendStages[0]?.usd.state).toBe('measured')
    // A subscription run's own split of a stage it settled without a gateway is
    // the part equal to the whole figure (`internal/runner/cost.go`). That is a
    // carried part, not the absent key, so the fallback does not apply: the
    // billed cell is the figure less the part -- a measured zero, which the
    // notional cell beside it explains -- and the two cells together are the
    // stage's one figure. Applying the fallback here draws `~$2.10` in
    // both cells and reads as twice what the stage cost.
    const self = buildRunModel(
      runInputs({
        diagnosis: {
          ...DIAGNOSIS,
          row: {
            ...DIAGNOSIS.row,
            cost: { ...DIAGNOSIS.row.cost, model_usd: 2.1, model_basis: 'subscription' },
          },
          stages: [{ name: 'review', duration_s: 95, model_usd: 2.1, model_notional_usd: 2.1 }],
        },
      }),
    )
    expect(self.spendStages[0]?.usd.value).toBe(0)
    expect(self.spendStages[0]?.usd.state).toBe('measured')
    expect(self.spendStages[0]?.notional.value).toBe(2.1)
    expect(self.spendStages[0]?.notional.state).toBe('notional')
    expect((self.spendStages[0]?.usd.value ?? 0) + (self.spendStages[0]?.notional.value ?? 0)).toBe(2.1)
  })

  it('spend by stage bounds a hand-edited list at 128', () => {
    // `diagnoseStages` bounds the list when it is written; the document is an
    // object a hand can edit, so the screen bounds it again and counts what it
    // left out rather than drawing a list nobody bounded.
    const stages = Array.from({ length: RUN_SPEND_STAGES_MAX + 2 }, (_, index) => ({
      name: `stage-${String(index)}`,
      duration_s: index,
      calls: index,
      agents: 0,
      model_usd: 0.1,
      model_notional_usd: 0,
    }))
    const model = buildRunModel(runInputs({ diagnosis: { ...DIAGNOSIS, stages } }))
    expect(model.spendStages).toHaveLength(RUN_SPEND_STAGES_MAX)
    expect(model.spendStages.at(-1)?.name).toBe(`stage-${String(RUN_SPEND_STAGES_MAX - 1)}`)
    expect(model.spendStagesDropped.value).toBe(2)
  })

  it('tolerated is null when the block is absent and keeps total above the listed entries', () => {
    // A run that tolerated nothing has no block at all -- never an empty one --
    // and the model says so with `null` rather than with an empty panel.
    expect(buildRunModel(runInputs()).tolerated).toBeNull()
    const findings = [
      {
        title: 'the backoff ceiling is a constant here',
        label: 'low',
        mode: 'pro',
        likelihood: 2,
        impact: 2,
        fix_cost: 1,
        class: 'maintainability',
      },
    ]
    const model = buildRunModel(runInputs({ diagnosis: { ...DIAGNOSIS, tolerated: { findings, total: 4 } } }))
    // `total` is how many findings the mode really tolerated and is never below
    // the entries listed: the console draws it as the document wrote it rather
    // than as the length of the list beside it.
    expect(model.tolerated?.total.value).toBe(4)
    expect(model.tolerated?.findings).toHaveLength(1)
    expect(model.tolerated?.findings[0]?.title).toBe('the backoff ceiling is a constant here')
    expect(model.tolerated?.findings[0]?.mode).toBe('pro')
    expect(model.tolerated?.findings[0]?.likelihood.value).toBe(2)
    expect(model.tolerated?.findings[0]?.scoreClass).toBe('maintainability')
    expect(model.tolerated?.dropped.value).toBe(0)
    // An entry that is not an object is skipped, never thrown on, and it is an
    // entry the panel did not draw: counted with the bound's overflow rather
    // than left out of the count, so the rows plus `dropped` are the array.
    const malformed = buildRunModel(
      runInputs({ diagnosis: { ...DIAGNOSIS, tolerated: { findings: [{ ...findings[0] }, null, 'nit'], total: 3 } } }),
    )
    expect(malformed.tolerated?.findings).toHaveLength(1)
    expect(malformed.tolerated?.dropped.value).toBe(2)
    // The list is bounded here too, and what it left out is a reading.
    const many = Array.from({ length: RUN_TOLERATED_MAX + 3 }, () => findings[0])
    const bounded = buildRunModel(runInputs({ diagnosis: { ...DIAGNOSIS, tolerated: { findings: many, total: many.length } } }))
    expect(bounded.tolerated?.findings).toHaveLength(RUN_TOLERATED_MAX)
    expect(bounded.tolerated?.dropped.value).toBe(3)
  })
})

describe('renderRun', () => {
  it('draws the run through the Projects screen\'s factory, and offers no link', () => {
    const tree = renderRun(factory, buildRunModel(runInputs()), 'light')
    expect(tree.getAttribute('data-theme')).toBe('light')
    // The pull request is text: repository, number and URL, and nothing that
    // navigates. `FakeScreen` would throw on a navigating attribute, and the
    // model's `link` is null -- both halves of the rule in one test.
    const pr = one(tree, 'run-pr')
    expect(drawnText(pr)).toContain('example-owner/example-repo')
    expect(drawnText(pr)).toContain('42')
    expect(drawnText(pr)).toContain('https://bitbucket.org/example-owner/example-repo/pull-requests/42')
    expect(one(pr, 'run-pr-reason').textContent).toContain('provider')
    // Nothing in the pull request panel carries a destination at all: there is
    // no inert link, and none waiting for a provider.
    expect(walk(pr).map((node) => node.getAttribute('data-href'))).toEqual(walk(pr).map(() => null))
  })

  it('puts an element between the repository, the number and the URL', () => {
    // The owner's report of 2026-10-21, from the screenshot run: the three
    // facts stood side by side with nothing between them, and the panel read
    // `acme/platform-core#412https://github.com/acme/platform-core/pull/412`
    // -- three values run into one string, with the URL no longer a URL. The
    // separator is a cell of its own rather than part of a value, so each value
    // is still exactly the document's string (the case above draws them).
    const tree = renderRun(factory, buildRunModel(runInputs()), 'light')
    const line = walk(one(tree, 'run-pr'))
      .filter((node) => PR_LINE_CLASSES.some((name) => node.classes.includes(name)))
      .map((node) => node.textContent)
    expect(line).toEqual([
      'example-owner/example-repo',
      ' · ',
      '#42',
      ' · ',
      'https://bitbucket.org/example-owner/example-repo/pull-requests/42',
    ])
  })

  it('separates what it draws, so a panel missing a field has no stray separator', () => {
    // The separator belongs *between* the facts, not before the URL: a run whose
    // `pr_url` the document does not carry draws two cells and one separator.
    const withoutUrl = { ...DIAGNOSIS, row: { ...DIAGNOSIS.row, pr_url: undefined } }
    const tree = renderRun(factory, buildRunModel(runInputs({ diagnosis: withoutUrl })), 'light')
    const pr = one(tree, 'run-pr')
    const line = walk(pr)
      .filter((node) => PR_LINE_CLASSES.some((name) => node.classes.includes(name)))
      .map((node) => node.textContent)
    expect(line).toEqual(['example-owner/example-repo', ' · ', '#42'])
    expect(texts(pr, 'run-pr-url')).toEqual([])
  })

  it('gives the reason no link is offered in one line, in the design’s words', () => {
    // Design section 5.1 rule 2's own sentence, and design gap 7 is why a link
    // is not built from a repository and a number: it would name a repository
    // this run does not use. The panel drew a two-clause explanation of that
    // instead -- the owner's "long explanatory sentence under it" -- and the
    // design's own line says the same thing in one.
    expect(PR_NO_LINK_REASON).toBe(
      'this CLI does not say which provider this run’s repository is on, so the console will not build a link from it',
    )
    const tree = renderRun(factory, buildRunModel(runInputs()), 'light')
    expect(one(one(tree, 'run-pr'), 'run-pr-reason').textContent).toBe(PR_NO_LINK_REASON)
  })

  it('offers the logs link as a destination attribute and never as a navigation', () => {
    const tree = renderRun(factory, buildRunModel(runInputs()), 'light')
    const link = one(tree, 'run-log-link')
    // `putLink` writes `data-href`, which does nothing when clicked: the main
    // process owns the allow-list (design section 5.1 rule 2).
    expect(link.getAttribute('data-href')).toBe('https://console.cloud.google.com/batch/jobs/example-job')
    expect(link.getAttribute(['h', 'ref'].join(''))).toBeNull()
  })

  it('puts the scrubbed-to moment through the sanitizer when the ring holds nothing then', () => {
    // The moment is an input the console did not write; when the ring has
    // nothing at or before it, the sentence that names it is drawn through
    // `putText`, so a hostile moment is not drawn as itself.
    const token = githubToken()
    const tree = renderRun(factory, buildRunModel(runInputs({ replay: REPLAY_FRAMES, scrubAt: token })), 'light')
    expect(drawnText(tree)).not.toContain(token)
    expect(drawnText(tree)).toContain(TOKEN_PLACEHOLDER)
  })

  it('counts the lines the log read refused, and draws none of them', () => {
    const logs = { entries: logLines(3), total: 3, refused: 2 }
    const tree = renderRun(factory, buildRunModel(runInputs({ logs })), 'light')
    expect(all(tree, 'run-log-line')).toHaveLength(3)
    expect(one(tree, 'run-log-refused-count').textContent).toBe('2')
    // None refused is no line at all, not a zero drawn for its own sake.
    const clean = renderRun(factory, buildRunModel(runInputs({ logs: { ...logs, refused: 0 } })), 'light')
    expect(all(clean, 'run-log-refused')).toEqual([])
  })

  it('reads no refused count where the input carried none, in either shape', () => {
    // A bare list of log lines and a window that omits `refused` both carry no
    // count: the reading is unavailable, never a measured zero.
    expect(parseLogWindow(logLines(3)).refused).toBeNull()
    expect(parseLogWindow({ entries: logLines(3), total: 3 }).refused).toBeNull()
    expect(parseLogWindow({ entries: logLines(3), total: 3, refused: 0 }).refused).toBe(0)
    expect(buildRunModel(runInputs({ logs: logLines(3) })).logs.refused.value).toBeNull()
  })

  it('draws exactly the capped window of log lines', () => {
    const tree = renderRun(factory, buildRunModel(runInputs({ logs: logLines(RUN_LOG_CAP + 25) })), 'light')
    expect(all(tree, 'run-log-line')).toHaveLength(RUN_LOG_CAP)
    expect(drawnText(one(tree, 'run-log-note'))).toContain('25')
  })

  it('puts a document\'s log line through the sanitizer like every other string', () => {
    // A line a job wrote is free text, and a line that carries a credential is
    // refused rather than drawn: the panel is a renderer that cannot read the
    // transcript, which is why it is capped in the first place.
    const token = githubToken()
    const logs = [{ time: '2026-10-15T11:00:00Z', severity: 'info', message: `using ${token}` }]
    const tree = renderRun(factory, buildRunModel(runInputs({ logs })), 'light')
    expect(drawnText(tree)).not.toContain(token)
    expect(one(one(tree, 'run-log-line'), 'run-log-message').textContent).toBe(`using ${TOKEN_PLACEHOLDER}`)
  })

  it('draws the ledger\'s not-estimated compute as n/a, never as a free $0.00', () => {
    const notEstimated = {
      ...DIAGNOSIS,
      row: { ...DIAGNOSIS.row, cost: { ...DIAGNOSIS.row.cost, compute_usd: 0, compute_estimated: false } },
    }
    const tree = renderRun(factory, buildRunModel(runInputs({ diagnosis: notEstimated })), 'light')
    expect(one(tree, 'run-ledger-compute-value').textContent).toBe('n/a')
    // The sentence is in the cell's title, the way every unavailable reading
    // carries its reason, and `$0.00` is nowhere on the screen.
    expect(one(tree, 'run-ledger-compute-value').getAttribute('title')).toContain('not estimated')
    expect(drawnText(tree)).not.toContain('$0.00')
  })

  it('puts a provider\'s log severity through the sanitizer, like the message beside it', () => {
    // `severity` is the cloud logging API's own word, not one of this console's
    // constants, so it is a document string like any other: a token-shaped one
    // is redacted in place rather than written into the document raw.
    const token = githubToken()
    const logs = [{ time: '2026-10-15T11:00:00Z', severity: token, stage: 'verify', message: 'ok' }]
    const tree = renderRun(factory, buildRunModel(runInputs({ logs })), 'light')
    expect(one(tree, 'run-log-severity').textContent).toBe(TOKEN_PLACEHOLDER)
    expect(drawnText(tree)).not.toContain(token)
  })

  it('draws the run\'s requester in the person class, so no address shape leaks its local part', () => {
    const local = ['ana', 'lopez'].join('.')
    for (const who of [`"${local}"@example.invalid`, `Ana Lopez <${local}@example.invalid>`]) {
      const diagnosis = { ...DIAGNOSIS, row: { ...DIAGNOSIS.row, requested_by: who } }
      const tree = renderRun(factory, buildRunModel(runInputs({ diagnosis })), 'light')
      expect(one(tree, 'run-requested-by').textContent?.toLowerCase()).not.toContain('lopez')
      expect(drawnText(tree).toLowerCase()).not.toContain('lopez')
    }
  })

  it('draws the whole recorded launcher in the run view, in the person class', () => {
    // The run view is where the owner's spec puts the **full identity** ("the
    // full identity only in the expanded or run view and in JSON"): the Live and
    // Projects columns draw the short form. It goes through the `person` class,
    // the class the wire key carries, so an address is the console's marker here
    // as it is in the requester cell beside it, and a login is itself.
    const named = { ...DIAGNOSIS, row: { ...DIAGNOSIS.row, launched_by: 'dana@example.test' } }
    const tree = renderRun(factory, buildRunModel(runInputs({ diagnosis: named })), 'light')
    expect(one(tree, 'run-started-by').textContent).toBe(ADDRESS_PLACEHOLDER)
    expect(drawnText(tree)).not.toContain('dana@example.test')

    const login = { ...DIAGNOSIS, row: { ...DIAGNOSIS.row, launched_by: 'ci-runner' } }
    const other = renderRun(factory, buildRunModel(runInputs({ diagnosis: login })), 'light')
    expect(one(other, 'run-started-by').textContent).toBe('ci-runner')

    // A record whose `launch.json` recorded no launcher draws no cell at all.
    expect(all(renderRun(factory, buildRunModel(runInputs()), 'light'), 'run-started-by')).toHaveLength(0)
  })

  it('puts the run record\'s status through the sanitizer too', () => {
    const token = githubToken()
    const status = { ...DIAGNOSIS, row: { ...DIAGNOSIS.row, status: token } }
    const tree = renderRun(factory, buildRunModel(runInputs({ diagnosis: status })), 'light')
    expect(one(tree, 'run-status').textContent).toBe(TOKEN_PLACEHOLDER)
    expect(drawnText(tree)).not.toContain(token)
  })

  it('writes only the console\'s own text into the reading cells', () => {
    // The `putText` door, exercised directly: a screen that wrote a document's
    // string into a cell without it would leave the cell holding the value.
    const element = new FakeScreen('span')
    putText(element, githubToken(), 'label', 'name')
    expect(element.textContent).toBe(TOKEN_PLACEHOLDER)
  })

  it('draws the run\'s rigor: the mode, the layer that decided it and the areas that matched', () => {
    // Owner request 2026-10-25: the Rigor row, and the one screen with room for
    // the whole answer -- `runs ls`'s MODE column and the Projects cell are one
    // word in a table, and the Live chip is one word on a row. The CLI's
    // `fugaro diagnose` prints the same three facts on its own `Rigor:` line
    // (`internal/cli/diagnose.go`'s `rigorLine`).
    const rigor = {
      mode: 'critical',
      from: 'area',
      areas: [
        { name: 'payments', mode: 'critical' },
        { name: 'billing', mode: 'pro' },
      ],
      // The record's two stamps. This row is about what the run resolved, so
      // neither is drawn -- and both are distinctive here, so a screen that
      // started drawing one fails this test rather than passing quietly.
      at: '2026-10-15T13:59:59Z',
      revision: 'origin/main',
    }
    const diagnosis = { ...DIAGNOSIS, row: { ...DIAGNOSIS.row, rigor } }
    const tree = renderRun(factory, buildRunModel(runInputs({ diagnosis })), 'light')
    const row = one(tree, 'run-rigor')
    expect(one(row, 'run-rigor-word').textContent).toBe('RIGOR')
    expect(one(row, 'run-rigor-mode').textContent).toBe('Critical')
    expect(one(row, 'run-rigor-from-word').textContent).toBe('from')
    // The layer in the console's words, not the record's enum: `area` is the
    // CLI's spelling of a layer and `an area rule` is what a person reads.
    expect(one(row, 'run-rigor-from').textContent).toBe('an area rule')
    expect(texts(row, 'run-rigor-area-name')).toEqual(['payments', 'billing'])
    expect(texts(row, 'run-rigor-area-mode')).toEqual(['Critical', 'Pro'])
    // The two stamps are in the record and in no cell of this screen.
    expect(drawnText(tree)).not.toContain('origin/main')
    expect(drawnText(tree)).not.toContain('2026-10-15T13:59:59Z')
  })

  it('draws no Rigor row at all for a run whose record carries no block', () => {
    // Absence is "no record of this run's rigor", and the answer is **no row**:
    // the fixture's run is older than the field, and a row reading `-` here
    // would be answering a question the record was never asked.
    const tree = renderRun(factory, buildRunModel(runInputs()), 'light')
    expect(all(tree, 'run-rigor')).toHaveLength(0)
  })

  it('draws a rigor value this build does not know as the bounded text it is', () => {
    // The direction the reader fails in: a newer CLI may resolve a mode this
    // console has never heard of, and a row that hid it would be hiding the
    // run's rigor entirely. The five modes and the five layers have the
    // console's words; everything else takes the text path.
    const rigor = {
      mode: 'standard',
      from: 'reached',
      areas: [{ name: 'payments\u0000ledger', mode: 'standard' }],
    }
    const diagnosis = { ...DIAGNOSIS, row: { ...DIAGNOSIS.row, rigor } }
    const tree = renderRun(factory, buildRunModel(runInputs({ diagnosis })), 'light')
    const row = one(tree, 'run-rigor')
    expect(one(row, 'run-rigor-mode').textContent).toBe('standard')
    expect(one(row, 'run-rigor-from').textContent).toBe('reached')
    // An area's name is a repository's own word, so it is text on every path:
    // the control character is stripped rather than drawn.
    expect(one(row, 'run-rigor-area-name').textContent).toBe('paymentsledger')
    expect(one(row, 'run-rigor-area-mode').textContent).toBe('standard')
  })

  it('masks a rigor string shaped like a credential, and draws none of it', () => {
    // Every string in the block is job-written text (SECURITY.md D4), so a
    // token-shaped one is masked in place by the free-text rules -- in the mode
    // cell, the layer cell and an area's name alike.
    const token = githubToken()
    const rigor = { mode: token, from: token, areas: [{ name: token, mode: token }] }
    const diagnosis = { ...DIAGNOSIS, row: { ...DIAGNOSIS.row, rigor } }
    const tree = renderRun(factory, buildRunModel(runInputs({ diagnosis })), 'light')
    const row = one(tree, 'run-rigor')
    expect(one(row, 'run-rigor-mode').textContent).toBe(TOKEN_PLACEHOLDER)
    expect(one(row, 'run-rigor-from').textContent).toBe(TOKEN_PLACEHOLDER)
    expect(one(row, 'run-rigor-area-name').textContent).toBe(TOKEN_PLACEHOLDER)
    expect(one(row, 'run-rigor-area-mode').textContent).toBe(TOKEN_PLACEHOLDER)
    expect(drawnText(tree)).not.toContain(token)
  })

  it('draws a block with no matched area as the mode and the layer alone', () => {
    // A run that resolved its mode from the repository's own `rigor.default`
    // matched no rule: the row is the two facts it has, and no empty chip.
    const rigor = { mode: 'pro', from: 'repo', areas: [] }
    const diagnosis = { ...DIAGNOSIS, row: { ...DIAGNOSIS.row, rigor } }
    const tree = renderRun(factory, buildRunModel(runInputs({ diagnosis })), 'light')
    const row = one(tree, 'run-rigor')
    expect(one(row, 'run-rigor-mode').textContent).toBe('Pro')
    expect(one(row, 'run-rigor-from').textContent).toBe('the repository')
    expect(all(row, 'run-rigor-area')).toHaveLength(0)
  })

  it('draws the spend by stage panel and the findings the mode tolerated', () => {
    const stages = [
      { name: 'implement', duration_s: 420, calls: 42, agents: 3, model_usd: 1.1, model_notional_usd: 0 },
      { name: 'review', duration_s: 95, model_usd: 0.15, model_notional_usd: 0.05 },
    ]
    const tolerated = {
      findings: [
        {
          title: 'the retry table is stale',
          label: 'low',
          mode: 'pro',
          likelihood: 2,
          impact: 2,
          fix_cost: 1,
          class: 'maintainability',
        },
      ],
      total: 4,
    }
    const tree = renderRun(factory, buildRunModel(runInputs({ diagnosis: { ...DIAGNOSIS, stages, tolerated } })), 'light')
    const spend = one(tree, 'run-spend-stages')
    expect(texts(spend, 'run-spend-stage-name')).toEqual(['implement', 'review'])
    expect(texts(spend, 'run-spend-stage-calls')).toEqual(['42', 'n/a'])
    expect(texts(spend, 'run-spend-stage-agents')).toEqual(['3', 'n/a'])
    expect(texts(spend, 'run-spend-stage-usd')).toEqual(['$1.10', '$0.10'])
    // The usd cell is the **billed** part of the stage's figure -- the ledger's
    // own split, one panel above -- and the notional cell beside it is the part
    // the record wrote, carrying the console's notional mark: the plan's list
    // price and not money the run was billed (C1.16, P12). Together the two are
    // the stage's one figure, never a sum of it. A part of zero is a measured
    // `$0.00` rather than a marked zero.
    expect(texts(spend, 'run-spend-stage-notional')).toEqual(['$0.00', '~$0.05'])
    // The absent counts say which fact they are, in the cell's title.
    expect(all(spend, 'run-spend-stage-calls')[1]?.getAttribute('title')).toBe('not counted for this stage')
    const mode = one(tree, 'run-tolerated')
    // The console's own word for the mode, and the document's own for the rest.
    expect(texts(mode, 'run-tolerated-mode')).toEqual(['Pro'])
    expect(texts(mode, 'run-tolerated-label')).toEqual(['low'])
    expect(one(mode, 'run-tolerated-count').textContent).toBe('4')
  })

  it('draws the billed part of a stage’s figure, and the plan’s part beside it', () => {
    // A stage's `model_usd` is billed money **plus** the plan's notional part
    // (`runstore.Cost`, C1.16), so the usd cell is the ledger's own split of it
    // and never the whole figure: drawn whole, the two cells would add billed
    // and notional money on one row.
    const listed = { name: 'review', duration_s: 95, model_usd: 0.8, model_notional_usd: 0.3 }
    const drawn = renderRun(
      factory,
      buildRunModel(runInputs({ diagnosis: { ...DIAGNOSIS, stages: [listed] } })),
      'light',
    )
    expect(texts(drawn, 'run-spend-stage-usd')).toEqual(['$0.50'])
    expect(texts(drawn, 'run-spend-stage-notional')).toEqual(['~$0.30'])
    // A record written before the key existed carries no part, and then a
    // subscription run's whole stage figure is the plan's: the usd cell is that
    // figure marked notional and never plain dollars, because `$0.00` there
    // would read as free.
    const before = buildRunModel(
      runInputs({
        diagnosis: {
          ...DIAGNOSIS,
          row: {
            ...DIAGNOSIS.row,
            cost: { ...DIAGNOSIS.row.cost, model_usd: 2.1, model_basis: 'subscription' },
          },
          stages: [{ name: 'review', duration_s: 95, model_usd: 2.1 }],
        },
      }),
    )
    const old = renderRun(factory, before, 'light')
    expect(texts(old, 'run-spend-stage-usd')).toEqual(['~$2.10'])
    expect(texts(old, 'run-spend-stage-usd')).not.toContain('$2.10')
    expect(texts(old, 'run-spend-stage-notional')).toEqual(['$0.00'])
    // A subscription run that settled a stage without a gateway writes a part
    // equal to the stage's own whole figure (`internal/runner/cost.go`), and that
    // is the record's split rather than the fallback: the plan paid all of it, so
    // the billed cell is a measured `$0.00` and the figure stands once, under the
    // notional mark, in the cell beside it. Reading this shape as the fallback
    // would draw `~$2.10` twice and read as twice the stage's spend.
    const self = buildRunModel(
      runInputs({
        diagnosis: {
          ...DIAGNOSIS,
          row: {
            ...DIAGNOSIS.row,
            cost: { ...DIAGNOSIS.row.cost, model_usd: 2.1, model_basis: 'subscription' },
          },
          stages: [{ name: 'review', duration_s: 95, model_usd: 2.1, model_notional_usd: 2.1 }],
        },
      }),
    )
    const split = renderRun(factory, self, 'light')
    expect(texts(split, 'run-spend-stage-usd')).toEqual(['$0.00'])
    expect(texts(split, 'run-spend-stage-notional')).toEqual(['~$2.10'])
  })

  it('a tolerated title is drawn as text, never markup', () => {
    // A title is a reviewer's own sentence, stored in a record a hand can edit:
    // it reaches the screen through `putText`, so a title holding markup is the
    // literal characters and never an element.
    const title = '<img src=x onerror=1>'
    const tolerated = {
      findings: [
        {
          title,
          label: 'low',
          mode: 'pro',
          likelihood: 2,
          impact: 2,
          fix_cost: 1,
          class: 'maintainability',
        },
      ],
      total: 1,
    }
    const tree = renderRun(factory, buildRunModel(runInputs({ diagnosis: { ...DIAGNOSIS, tolerated } })), 'light')
    expect(one(tree, 'run-tolerated-title').textContent).toBe(title)
    expect(walk(tree).map((node) => node.tag)).not.toContain('img')
  })

  it('draws no panel for a document with no tolerated block, and names the absent key in one line', () => {
    // The block is absent for a run that tolerated nothing, but also for a
    // record written before the field existed and for a run whose record could
    // not be read: the absence is one line naming the key that is missing, not
    // the claim that nothing was tolerated.
    const tree = renderRun(factory, buildRunModel(runInputs()), 'light')
    expect(all(tree, 'run-tolerated')).toHaveLength(0)
    const line = one(tree, 'run-tolerated-absent').textContent ?? ''
    expect(line).toContain('the document carries no tolerated block')
    expect(line).not.toContain('nothing tolerated')
    // The same for the run's spend by stage: no `stages`, no panel.
    expect(all(tree, 'run-spend-stages')).toHaveLength(0)
  })
})

/** REPLAY_FRAMES is a small replay buffer, built here rather than imported: the
 * buffer's own module is `replay.test.ts`'s subject. */
const REPLAY_FRAMES = [
  { at: '2026-10-15T12:00:00Z', document: { project: 'example-project', day: '2026-10-15' } },
  { at: '2026-10-15T12:04:00Z', document: { project: 'example-project', day: '2026-10-15' } },
  { at: '2026-10-15T12:08:00Z', document: { project: 'example-project', day: '2026-10-15' } },
].map((record) => ({ receivedAt: record.at, document: record.document, notices: [] }))

describe('the shapes the CLI writes where a repository is expected (review round 2 of PR 494)', () => {
  it('draws the two "every repository" values `secrets ls` writes, and still refuses anything else', () => {
    const secrets = [{ ...SECRETS[0], allowed: ['any repository', '*', 'not a repository'] }]
    const tree = renderProjects(factory, buildProjectsModel(inputs({ secrets })), 'light')
    expect(texts(tree, 'projects-secret-allowed')).toEqual(['any repository', '*', 'n/a'])
  })

  it('draws a repository the document names only by its slash-free slug with the slug class', () => {
    // `repo` is omitempty: a host whose local config does not list a
    // repository with spend has only the bucket slug for it.
    const slug = 'teammate-app-dc4d4e59e884fc44'
    const block = { ...BUDGET.repos[1], slug }
    delete (block as { repo?: string }).repo
    const budget = { ...BUDGET, repos: [BUDGET.repos[0], block] }
    const tree = renderProjects(factory, buildProjectsModel(inputs({ budget })), 'light')
    expect(texts(tree, 'projects-kill-repo')).toContain(slug)
    expect(texts(tree, 'projects-repo-enrolled')).toEqual(['example-owner/example-repo', slug])
    // A slug-shaped value that is not a slug is still refused.
    const bad = { ...block, slug: 'Not A Slug' }
    const refused = renderProjects(factory, buildProjectsModel(inputs({ budget: { ...BUDGET, repos: [bad] } })), 'light')
    expect(texts(refused, 'projects-repo-enrolled')).toEqual(['n/a'])
  })
})
