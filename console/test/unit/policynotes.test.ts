// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The policy notes: the section the Project tab's policy panel draws once CM10
// wires this module in (managed-project screens plan CM7; reconciliation D1481,
// D1482, and §6 rule 3).
//
// Three claims of the plan's are what this file is about, and each of them is a
// rule rather than a cell:
//
//   - **the node's members drawn, and only the ones the plan names.** The ask's
//     "policy notes" is the published node (D1480), the document is
//     `fugaro project policy show --json` (D1481), and the members the screen
//     draws are the accounts, `allow_repos`, `base_branch`, the routing and the
//     image catalog. A node carries more than those -- `budget`'s dollar
//     figures, `default_claude_account` and `claude_account_stages` -- and the
//     last two are `settings --json`'s and the settings panel's (D1478, CM6), so
//     the fixture below carries all of them and the test asserts that none is
//     drawn.
//   - **the six `policy-*` checks drawn and no other check.** `doctor --json`
//     returns around twenty checks; the six the owner's ruling names are the
//     policy family, and a section that drew the others would be a section whose
//     subject is doctor rather than the policy (D1482). The fixture's document
//     carries a `managed-audit` line and two credential lines besides the six,
//     which is what the plan's mutation ("draw every check `doctor` returns")
//     makes fail.
//   - **no line claims what the last update changed.** The change is printed by
//     `fugaro update` and recorded nowhere (F19), so the section draws the node
//     as it is and says where the change is printed instead (D1482). The test
//     reads every cell and holds that the only one about a change is the
//     console's own sentence saying it draws none.
//
// A fourth rule of the owner's is tested here too, because the document tempts a
// screen into breaking it: reconciliation §6 rule 3 is **"no figure the Spend
// screen owns. These screens show no dollars and no caps"**, so the node's
// `budget` block -- which travels in this very document -- reaches no cell.
//
// The fake screen below is `projects.test.ts`'s, in this file's own words: the
// tab's panels are siblings, each declares its own `ScreenElement`, and a test
// that a factory drawing one draws another is `projects.test.ts`'s job.

import { describe, expect, it } from 'vitest'

import { UNAVAILABLE_MARK } from '../../src/shared/availability'
import { PATH_PLACEHOLDER } from '../../src/shared/sanitize'
import {
  POLICY_CHECK_ABSENT_BECAUSE,
  POLICY_CHECK_IDS,
  POLICY_CHECKS_SOURCE,
  POLICY_CHECKS_TITLE,
  POLICY_CHECK_FAILING_WORD,
  POLICY_CHECK_NOTED_WORD,
  POLICY_CHECK_OK_WORD,
  POLICY_CHECK_UNDRAWN_WORD,
  POLICY_DEFAULT_WORD,
  POLICY_DIRECT_WORD,
  POLICY_NO_BASE_BRANCH,
  POLICY_NO_DIFF_NOTE,
  POLICY_NO_POLICY,
  POLICY_NODE_ABSENT,
  POLICY_NODE_SHAPE,
  POLICY_NODE_SOURCE,
  POLICY_NODE_TITLE,
  POLICY_NONE_WORD,
  POLICY_OTHERS_LEAD,
  buildPolicyNotes,
  policyNotesBody,
} from '../../src/renderer/screens/policynotes'
import type {
  PolicyNotesInputs,
  PolicyNotesModel,
  ScreenElement,
  ScreenFactory,
} from '../../src/renderer/screens/policynotes'

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

/** factory makes fresh fakes, in the shape the renderer hands a screen: one
 * `create`, because this section draws no SVG. */
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

/** texts is the text of every node carrying a class. */
function texts(node: ScreenElement, className: string): readonly (string | null)[] {
  return all(node, className).map((leaf) => leaf.textContent)
}

/** block is the one block a read is drawn in, found by the operation it came
 * from. The section has two, and which one a line came from is one of the
 * plan's own claims. */
function block(node: ScreenElement, source: string): FakeScreen {
  const found = all(node, 'policy-notes-block').filter((one) => one.getAttribute('data-source') === source)
  expect(found, `expected exactly one block for ${source}`).toHaveLength(1)
  const first = found[0]
  if (first === undefined) throw new Error(`no block for ${source}`)
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

/** model is the section's two documents through the builder, which is where
 * every rule about what a cell may hold is decided. */
function model(overrides: Partial<PolicyNotesInputs> = {}): PolicyNotesModel {
  return buildPolicyNotes({ document: POLICY_DOC, doctor: DOCTOR, ...overrides })
}

/** draw is the whole section, drawn. */
function draw(overrides: Partial<PolicyNotesInputs> = {}): ScreenElement {
  return policyNotesBody(factory, model(overrides))
}

// ---------------------------------------------------------------------------
// The documents.
// ---------------------------------------------------------------------------

/** POLICY_DOC is `fugaro project policy show --json`: `{project, policy}`
 * (F18, D1481), with the five members the plan names **and** the members it does
 * not -- a `budget` block with a dollar figure, the two review-account keys of
 * D1478, a price override -- because a screen that drew one of those would pass
 * a test whose fixture carried only what it draws.
 *
 * The allow-list entries are what the CLI really writes: a canonical
 * `provider/owner/name` (`projectpolicy.Canonical`), a wildcard pattern of the
 * same three segments (`projectpolicy.MatchRepo`, `config.MatchSegment`), and
 * one entry with a wildcard **inside** a segment, which the grammar allows. The
 * two-segment `owner/name` this fixture carried before is not a value the CLI
 * ever writes into `allow_repos`, and drawing the list through the `repository`
 * class -- which is `owner/name` -- refused every real entry (review of
 * 2026-11-07). */
const POLICY_DOC = {
  project: 'example-project',
  policy: {
    version: 3,
    base_branch: 'trunk',
    allow_repos: ['github/example-owner/example-repo', '*/example-owner/example-*'],
    accounts: {
      'anthropic-api': { allow_repos: ['github/example-owner/example-repo', 'github/example-owner/other-repo'] },
      'claude-ops': { allow_repos: ['*/example-owner/example-repo'] },
    },
    routing: { claude: { via: 'openrouter', models: ['claude-sonnet-5-5'] } },
    image_catalog: {
      entries: [
        {
          name: 'node-22',
          tags: ['node'],
          source: { kind: 'dockerfile', repo: 'example-owner/example-repo', path: 'images/node.Dockerfile' },
          default: true,
        },
        // A container image reference with a registry host and a path, which is
        // what `catalogImageRE` (`internal/config/catalog.go`) accepts and what
        // the `label` class mangled into `<host><path>:1.2` (review of
        // 2026-11-07).
        { name: 'agent', tags: ['agent'], source: { kind: 'image', image: 'ghcr.io/example-owner/agent:1.2' } },
        { name: 'tools', tags: [], source: { kind: 'inline', inline: 'FROM node:22\n' } },
      ],
    },
    default_claude_account: 'review-pool@example.test',
    claude_account_stages: { review: 'review-pool@example.test' },
    budget: { mode: 'enforce', per_run_usd: 8, allowed_models: ['claude-sonnet-5-5'], max_run_tokens: 900000 },
    model_prices: { 'claude-sonnet-5-5': { input_per_m: 3 } },
  },
}

/** OTHER_CHECK_IDS are the checks this document carries that the section must
 * **not** draw: `managed-audit` and two credential lines, which are not the
 * policy's at all, and doctor's two allow-list warnings, which are about the
 * same published node and are still outside the six the ruling names. The two
 * allow-list ones are here on purpose: a heading or a filter that reached them
 * would be claiming coverage the ruling did not give (D1482), and the only thing
 * the section says about any of the five is the figure beside the lead
 * sentence. */
const OTHER_CHECK_IDS = [
  'allow-list-empty',
  'allow-list-uncovered',
  'managed-audit',
  'creds-rules',
  'creds-broken',
]

/** DOCTOR is `fugaro doctor --json`: `{ok, checks}`, carrying the checks a real
 * installation's `doctor` writes, **with doctor's own `ok` and severity for
 * each** -- which the fixture got wrong before: it gave `policy-published`
 * `ok: true`, a state no branch of `doctorPolicyChecks` writes
 * (`internal/cli/doctor.go`: every emission of that id is an `info` line with
 * `ok` unset, and `checksFail`/`doctorClassOf` in `internal/cli/doctor_render.go`
 * are the rule that reads it). Three of the six are carried:
 *
 *   - `policy-published`, the one check nearly every installation carries, a
 *     measurement (`doctorMeasureIDs`) rather than something to do;
 *   - `policy-unknown-keys` and `policy-vs-jobs`, both real findings.
 *
 * `policy-newer-than-binary`, `policy-older-than-binary` and
 * `policy-rules-skew` are **not** carried: the first two are the two directions
 * of one version skew and cannot both be in one document, and the third is the
 * generation the rules marker records, which a database doctor cannot read (the
 * plan says so). So the document is one installation's, with one exception: the
 * five checks of `OTHER_CHECK_IDS` are a union rather than one installation's --
 * doctor raises the two allow-list warnings only about a node whose allow-list is
 * empty -- and they are here for the filter, which is the only claim this file
 * makes about them. */
const DOCTOR = {
  ok: false,
  checks: [
    {
      id: 'policy-published',
      ok: false,
      severity: 'info',
      problem: 'a policy is published at config/policy/shared (version 3, 2 allow-list entries)',
      fix: 'fugaro project policy show prints it',
    },
    {
      id: 'policy-unknown-keys',
      ok: false,
      severity: 'warning',
      problem: 'no run is bounded by the policy node: the node carries a key this Fugaro does not know: image_catalog',
      fix: 'remove the unknown key from the node by hand (fugaro project policy show prints it), or upgrade Fugaro',
    },
    {
      id: 'policy-vs-jobs',
      ok: false,
      severity: 'warning',
      problem: 'the jobs are deployed from the runner image ghcr.io/example-owner/runner:1.1, which predates this Fugaro: a policy published at config/policy/shared is one those runs do not read, so they keep the ceiling from their own environment',
      fix: 'fugaro image refresh moves the jobs to this release’s runner image',
    },
    {
      id: 'allow-list-empty',
      ok: false,
      severity: 'warning',
      problem: 'the policy published at config/policy/shared admits no repository: every run’s repository is refused before it mints a git token',
      fix: 'fugaro project repos add owner/name enrolls one',
    },
    {
      id: 'allow-list-uncovered',
      ok: false,
      severity: 'warning',
      problem: 'the policy published at config/policy/shared does not admit example-owner/other-repo, which this machine’s config names',
      fix: 'fugaro project repos add example-owner/other-repo enrolls it',
    },
    { id: 'managed-audit', ok: true, severity: 'info', problem: 'the audit trail is readable', fix: '' },
    { id: 'creds-rules', ok: true, severity: 'info', problem: 'the rules match', fix: '' },
    { id: 'creds-broken', ok: false, severity: 'warning', problem: 'a credential cannot be read', fix: '' },
  ],
}

// ---------------------------------------------------------------------------
// The node.
// ---------------------------------------------------------------------------

describe('the policy notes', () => {
  it('draws every member of the node the plan names', () => {
    const root = draw()
    const node = block(root, 'policy.show')

    expect(texts(node, 'policy-notes-branch')).toEqual(['trunk'])
    // The node's own `allow_repos`, then each account's list in name order:
    // `anthropic-api`'s two, then `claude-ops`'s one. Every value is drawn as
    // the document wrote it -- a canonical three-segment repository and both
    // spellings of a `*` pattern -- and none of them is the `n/a` mark, which is
    // what the `repository` class drew for all five (review of 2026-11-07).
    expect(texts(node, 'policy-notes-repo')).toEqual([
      'github/example-owner/example-repo',
      '*/example-owner/example-*',
      'github/example-owner/example-repo',
      'github/example-owner/other-repo',
      '*/example-owner/example-repo',
    ])
    expect(texts(node, 'policy-notes-account-name')).toEqual(['anthropic-api', 'claude-ops'])
    expect(texts(node, 'policy-notes-value')).toContain('openrouter')
    expect(texts(node, 'policy-notes-model')).toEqual(['claude-sonnet-5-5'])
    expect(texts(node, 'policy-notes-entry-name')).toEqual(['node-22', 'agent', 'tools'])
    expect(texts(node, 'policy-notes-entry-kind')).toEqual(['dockerfile', 'image', 'inline'])
    expect(texts(node, 'policy-notes-entry-repo')).toEqual(['example-owner/example-repo'])
    // A registry reference, drawn whole: the `label` class drew this one as
    // `<host><path>:1.2`, a redaction the document does not carry (review of
    // 2026-11-07).
    expect(texts(node, 'policy-notes-entry-image')).toEqual(['ghcr.io/example-owner/agent:1.2'])
    // The catalog entry's own path goes through the `path` class, which this
    // console replaces with its placeholder: a path is where the operator's own
    // machine shows through, and the entry's repository and branch are drawn.
    expect(texts(node, 'policy-notes-entry-path')).toEqual([PATH_PLACEHOLDER])
    expect(texts(node, 'policy-notes-default')).toEqual([POLICY_DEFAULT_WORD])
    expect(texts(node, 'policy-notes-count')).toEqual(['2', '2', '3'])
  })

  it('draws the members the plan does not name nowhere on the screen', () => {
    const root = draw()
    const text = drawn(root)

    // The review account keys are `settings --json`'s and the settings panel's
    // (D1478), and the node's `budget` block is the Spend screen's (rule 3):
    // this section shows no dollars and no caps.
    expect(text).not.toContain('review-pool@example.test')
    expect(text).not.toContain('default_claude_account')
    expect(text).not.toContain('claude_account_stages')
    expect(text).not.toContain('$')
    expect(texts(root, 'policy-notes-number')).not.toContain('8')
  })

  it('names the read each block came from, on the block itself', () => {
    const root = draw()

    expect(block(root, 'policy.show').getAttribute('data-state')).toBe('read')
    expect(block(root, 'environment.report').getAttribute('data-state')).toBe('read')
    expect(texts(root, 'policy-notes-heading')).toEqual([POLICY_NODE_TITLE, POLICY_CHECKS_TITLE])
    expect(texts(root, 'policy-notes-source')).toEqual([POLICY_NODE_SOURCE, POLICY_CHECKS_SOURCE])
  })

  it('reads a node with no base branch as the default, never as an empty cell', () => {
    const node = block(
      policyNotesBody(factory, buildPolicyNotes({ document: { project: 'p', policy: { version: 1 } }, doctor: DOCTOR })),
      'policy.show',
    )
    // The mark is what a reader sees, and the reason is in the cell's title --
    // which is where `putReading` puts an absent value's sentence, so a cell
    // that reads `n/a` always says why.
    const branch = all(node, 'policy-notes-branch')[0]
    expect(branch?.textContent).toBe(UNAVAILABLE_MARK)
    expect(branch?.getAttribute('title')).toBe(POLICY_NO_BASE_BRANCH)
    expect(texts(node, 'policy-notes-empty')).toEqual([POLICY_NONE_WORD, POLICY_NONE_WORD, POLICY_NONE_WORD])
  })

  it('reads a node whose switch is absent or empty as direct, and draws no model list for it', () => {
    const node = block(
      policyNotesBody(
        factory,
        buildPolicyNotes({
          document: { project: 'p', policy: { version: 1, base_branch: 'main', routing: { claude: { via: '' } } } },
          doctor: DOCTOR,
        }),
      ),
      'policy.show',
    )
    expect(texts(node, 'policy-notes-value')).toContain(POLICY_DIRECT_WORD)
    expect(texts(node, 'policy-notes-model')).toEqual([])
  })

  it('draws an unpublished policy as its own sentence, and a node it cannot read with its own command', () => {
    const none = block(policyNotesBody(factory, model({ document: { project: 'p', policy: null } })), 'policy.show')
    expect(none.getAttribute('data-state')).toBe('no-policy')
    expect(drawn(none)).toContain(POLICY_NO_POLICY)
    expect(texts(none, 'policy-notes-entry-name')).toEqual([])

    const refused = block(policyNotesBody(factory, model({ document: null })), 'policy.show')
    expect(refused.getAttribute('data-state')).toBe('unavailable')
    expect(drawn(refused)).toContain(POLICY_NODE_ABSENT)

    const wrong = block(policyNotesBody(factory, model({ document: { project: 'p' } })), 'policy.show')
    expect(drawn(wrong)).toContain(POLICY_NODE_SHAPE)
    expect(drawn(wrong)).toContain(POLICY_NODE_SOURCE)
  })

})

// ---------------------------------------------------------------------------
// The checks.
// ---------------------------------------------------------------------------

describe('the policy checks', () => {
  it('draws the six policy checks and no other check', () => {
    const root = draw()
    const checks = block(root, 'environment.report')

    const others = DOCTOR.checks.filter((check) => !POLICY_CHECK_IDS.includes(check.id))
    expect(POLICY_CHECK_IDS).toHaveLength(6)
    expect(others.map((check) => check.id)).toEqual([...OTHER_CHECK_IDS])
    expect(texts(checks, 'policy-notes-check-id')).toEqual(POLICY_CHECK_IDS)
    // Not one of the withheld ids -- nor any sentence of theirs -- reaches a
    // cell: the figure beside the lead sentence is the whole of what the section
    // says about them, and it says how many.
    const other = walk(checks)
      .map((leaf) => leaf.textContent ?? '')
      .filter((text) => OTHER_CHECK_IDS.includes(text))
    expect(other).toEqual([])
    expect(texts(checks, 'policy-notes-others')).toEqual([String(others.length)])
    expect(texts(checks, 'policy-notes-others-lead')).toEqual([POLICY_OTHERS_LEAD])
  })

  it('draws each check in the state its own document gives it, and a missing check as missing', () => {
    const checks = block(draw(), 'environment.report')

    // In `POLICY_CHECK_IDS` order. `policy-published` is the one line nearly
    // every installation carries, and it is `ok: false` with `severity: "info"`:
    // `noted`, never `failing`, because `doctor` fails on it in no mode
    // (`checksFail`). The two warning lines are findings. The three the document
    // does not carry say so.
    expect(texts(checks, 'policy-notes-check-state')).toEqual([
      POLICY_CHECK_NOTED_WORD,
      POLICY_CHECK_UNDRAWN_WORD,
      POLICY_CHECK_UNDRAWN_WORD,
      POLICY_CHECK_FAILING_WORD,
      POLICY_CHECK_FAILING_WORD,
      POLICY_CHECK_UNDRAWN_WORD,
    ])
    expect(all(checks, 'policy-notes-check').map((row) => row.getAttribute('data-ok'))).toEqual([
      'false',
      'unstated',
      'unstated',
      'false',
      'false',
      'unstated',
    ])
  })

  it('draws a check the document says passed as passed', () => {
    // Not doctor's own output: no branch of `doctorPolicyChecks` writes
    // `"ok": true` for any of the six (they are emitted only as a finding or as
    // an `info` measurement). It is the wire's contract -- `checks[].ok` is a
    // boolean -- and a document that says a check passed may not be drawn as a
    // failure, so the third state stays reachable and is held here.
    const document = {
      ok: true,
      checks: [{ id: 'policy-published', ok: true, severity: 'info', problem: 'nothing to report', fix: '' }],
    }
    const checks = block(draw({ doctor: document }), 'environment.report')

    expect(texts(checks, 'policy-notes-check-state')[0]).toBe(POLICY_CHECK_OK_WORD)
    expect(texts(checks, 'policy-notes-check-state')[1]).toBe(POLICY_CHECK_UNDRAWN_WORD)
  })

  it('says a check the document did not carry rather than drawing five and passing', () => {
    const checks = block(draw(), 'environment.report')

    // The absent check's severity, problem and fix are members the document does
    // not have: each is the reading mark with the console's own sentence in its
    // title, and none is a title about the wire (review of 2026-11-07).
    // The three absent checks are `policy-newer-than-binary`,
    // `policy-older-than-binary` and `policy-rules-skew`.
    const absent = [1, 2, 5].map((index) => all(checks, 'policy-notes-check')[index])
    for (const className of [
      'policy-notes-check-severity',
      'policy-notes-check-problem',
      'policy-notes-check-fix',
    ]) {
      const cells = all(checks, className)
      expect(cells, className).toHaveLength(6)
      for (const index of [1, 2, 5]) {
        const cell = cells[index]
        expect(cell?.textContent, className).toBe(UNAVAILABLE_MARK)
        expect(cell?.getAttribute('title'), className).toBe(POLICY_CHECK_ABSENT_BECAUSE)
      }
    }
    // The present rows' cells are the document's own strings, not marks.
    expect(texts(checks, 'policy-notes-check-severity')).toContain('info')
    expect(texts(checks, 'policy-notes-check-severity')).toContain('warning')
    for (const row of absent) expect(row?.getAttribute('data-ok')).toBe('unstated')
  })

  it('refuses with doctor\'s own command when the checks document did not arrive', () => {
    const root = draw({ doctor: null })
    const checks = block(root, 'environment.report')

    expect(checks.getAttribute('data-state')).toBe('unavailable')
    expect(drawn(checks)).toContain(POLICY_CHECKS_SOURCE)
    expect(texts(checks, 'policy-notes-check-id')).toEqual([])
  })
})

// ---------------------------------------------------------------------------
// The claim the section must not make.
// ---------------------------------------------------------------------------

describe('the diff', () => {
  it('claims no diff, and says where the change is printed instead of drawing one', () => {
    const root = draw()
    const aboutChange = walk(root)
      .map((leaf) => leaf.textContent ?? '')
      .filter((text) => /changed|\bdiff\b|last update/i.test(text))

    expect(aboutChange).toEqual([POLICY_NO_DIFF_NOTE])
  })

  it('holds no control that writes, and no contenteditable subtree', () => {
    const root = draw()
    const controls = walk(root).filter((one) => ['button', 'input', 'select', 'a', 'form'].includes(one.tag))

    expect(controls).toEqual([])
    expect(walk(root).filter((one) => one.getAttribute('contenteditable') !== null)).toEqual([])
  })
})
