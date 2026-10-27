// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
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
// (`internal/coordinator/sweep.go`), so the screen prints the console's own
// sentence about that and the stale marker's `since` as the stamp it is --
// never a derived "5 minutes ago", which would compare two things that are not
// on the same scale.

import { describe, expect, it } from 'vitest'

import { CONSOLE_OPERATIONS } from '../../src/main/ipc'
import { SPAWN_COMMANDS, argvFor, assertSpawnSurface } from '../../src/main/spawn'
import type { SpawnCommand } from '../../src/main/spawn'
import { putText } from '../../src/renderer/app'
import {
  COORDINATOR_IN_FLIGHT_WORD,
  COORDINATOR_NO_AGE_WORD,
  COORDINATOR_RUNS_WORD,
  COORDINATOR_STALE_WORD,
  COORDINATOR_STORED_WORD,
  buildCoordinatorModel,
  renderCoordinator,
} from '../../src/renderer/screens/coordinator'
import type { ScreenElement, ScreenFactory } from '../../src/renderer/screens/coordinator'
import { UNAVAILABLE_MARK } from '../../src/shared/availability'
import { decodeCoordinatorStatus } from '../../src/shared/schema'
import type { CoordinatorStatusView } from '../../src/shared/schema'

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

/** renders draws a document, as the entry point does. */
function renders(raw: unknown): ScreenElement {
  return renderCoordinator(factory, buildCoordinatorModel({ view: decodes(raw) }), 'dark')
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

  it('refuses an unknown field, so a key this console does not read is a failing read', () => {
    // The strict decoding every document of this console is held to: a key no
    // struct of the read writes is refused by name rather than ignored, because
    // a document that gained a field is a CLI whose shape this build does not
    // know.
    const rogue = { repos: [{ ...STORED, colour: 'red' }] }
    const result = decodeCoordinatorStatus(rogue)
    expect(result.ok).toBe(false)
    if (!result.ok) {
      expect(result.skew.refusal.name).toBe('unknown-key')
      expect(result.skew.refusal.field).toBe('repos[0].colour')
    }
    // The same at the document's own level, and a document that is not an object
    // at all is refused before anything is read from it.
    expect(decodeCoordinatorStatus({ repos: [STORED], download: 'https://example.invalid/x' }).ok).toBe(false)
    expect(decodeCoordinatorStatus([]).ok).toBe(false)
  })

  it('refuses this document at the feed’s door, and a feed at this one', () => {
    // The fourth door: the shapes share no marker, so neither admits the
    // other's. A screen that read one as the other would draw an empty project
    // view over a live feed.
    const other = decodeCoordinatorStatus({ conn: { state: 'ok' }, runs: [] })
    expect(other.ok).toBe(false)
    if (!other.ok) expect(other.skew.refusal.name).toBe('unknown-key')
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

  it('builds no link from wire text: the document’s URL is drawn as text and no element carries an href', () => {
    // Design section 5.1 rule 2: the console builds no link from a document's
    // string. The pull request's `url` is not drawn at all -- a link is
    // `link.openExternal`'s, validated in the main process -- and no element of
    // this tree carries a navigating attribute, an attribute value that is a
    // URL, or a tag that could navigate.
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
    const node = renders({ repos: [STORED] })
    expect(drawn(node)).toContain(COORDINATOR_IN_FLIGHT_WORD)
    for (const element of all(node, 'coordinator-count-value')) expect(element.textContent).toBe('1')
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
