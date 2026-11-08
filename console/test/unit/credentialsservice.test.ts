// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The credentials service's state, as a section: the
// `credentials-service-deployed` line of `doctor --json` (the managed-screens
// plan's CM9; reconciliation F22, decision D1486).
//
// Four claims of the plan's are what this file is about, and each is a rule
// rather than a cell:
//
//   - **one section, one read, and the read is doctor's.** The section draws
//     `environment.report` -- the operation CM4 put on the allow-list for
//     `doctor --json` -- and it names that operation rather than an operation of
//     its own. Nothing here spawns anything, and nothing here reads a bundle.
//   - **the CLI's words, re-derived nowhere** (D1486). The severity, the problem
//     and the fix are the document's own strings; the section decides nothing
//     about whether the service is deployed, converged or serving. The two
//     states a run cannot work around and the two that are states rather than
//     faults are told apart by the CLI's own `severity`, which is drawn verbatim,
//     so a screen that "helpfully" classified a line itself would fail the case
//     below rather than pass.
//   - **an installation whose CLI predates DON7 carries no such check, and the
//     section says exactly that** rather than drawing the service off. The check
//     arrived with DON7 (`internal/cli/doctor_brokering.go`, pull request
//     #1816); a console newer than the CLI beside it reads a document with no
//     such line, and "there is no such check" is a different fact from "the
//     check says the service is off". That is the red step's second half: with
//     the missing-check case merged into the read case (an empty list of rows),
//     the absent-check case below fails.
//   - **the remedy survives the refusal door.** The problem and the fix are
//     sentences a person acts on, and one of the CLI's fixes names
//     `roles/run.invoker` -- which the free-text `sentence` class draws as
//     `[redacted]`. So they go through `sanitizeRefusal`, the door the design
//     keeps for a CLI sentence (`docs/design/desktop-console.md` §6.3, the door
//     `degraded.ts`, `managed.ts`, `projects.ts` and `accountkeys.ts` all use),
//     and the case below asserts a role name and a `fugaro creds set NAME` are
//     drawn character for character. The sibling policy section (`policynotes.ts`,
//     CM7) draws the very same document's `checks[].problem` through `sentence`;
//     that divergence is reported rather than changed here, because CM9's file
//     group is this module and this test.
//
// **Every check the document carries under the one ID is drawn, in the order it
// carries them.** DON7's line is one line for the service; the plan's own
// "per-repository held-off rows" are the same check's output -- one line per
// repository, under the same ID (DON7's design, `docs/credentials-service/
// default-on.md` §3, whose per-repository half is DON8's). The section therefore
// reads the ID and not a position: it draws the first line and the tenth alike,
// which is what makes it correct before DON8 lands and after it.
//
// The fake screen below is `projects.test.ts`'s, in this file's own words: the
// tab's sections are siblings, each declares its own `ScreenElement`, and a test
// that a factory drawing one draws another is `projects.test.ts`'s job.

import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import { CONSOLE_OPERATIONS } from '../../src/main/ipc'
import { UNAVAILABLE_MARK } from '../../src/shared/availability'
import { MAX_TEXT_INPUT_LENGTH, TOKEN_PLACEHOLDER, sanitizeRefusal } from '../../src/shared/sanitize'
import { REFUSAL_UNREADABLE } from '../../src/renderer/screens/degraded'
import {
  CREDENTIALS_SERVICE_ABSENT,
  CREDENTIALS_SERVICE_BUSY,
  CREDENTIALS_SERVICE_CHECK_ID,
  CREDENTIALS_SERVICE_CLASS,
  CREDENTIALS_SERVICE_FIX_TERM,
  CREDENTIALS_SERVICE_MEMBER_ABSENT_BECAUSE,
  CREDENTIALS_SERVICE_NO_CHECK,
  CREDENTIALS_SERVICE_OK_WORD,
  CREDENTIALS_SERVICE_OPERATION,
  CREDENTIALS_SERVICE_PANEL_KEY,
  CREDENTIALS_SERVICE_PROBLEM_TERM,
  CREDENTIALS_SERVICE_REPORTED_WORD,
  CREDENTIALS_SERVICE_SEVERITY_TERM,
  CREDENTIALS_SERVICE_SHAPE,
  CREDENTIALS_SERVICE_SOURCE,
  CREDENTIALS_SERVICE_TITLE,
  CREDENTIALS_SERVICE_UNSTATED_WORD,
  buildCredentialsService,
  fillCredentialsService,
  renderCredentialsService,
} from '../../src/renderer/screens/credentialsservice'
import type {
  CredentialsServiceInputs,
  ScreenElement,
  ScreenFactory,
} from '../../src/renderer/screens/credentialsservice'
import { githubToken } from '../support/fakeSecrets'

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

/** factory makes fresh fakes, in the one-member shape every screen declares. */
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

/** texts is every node's text carrying a class, in document order. */
function texts(node: ScreenElement, className: string): readonly (string | null)[] {
  return all(node, className).map((cell) => cell.textContent)
}

/** drawn is the whole tree's text, in document order: what a person reading the
 * screen would read if they read every cell. */
function drawn(node: ScreenElement): string {
  return walk(node)
    .map((leaf) => leaf.textContent ?? '')
    .filter((text) => text !== '')
    .join(' ')
}

/** section is the section node the fill drew, asserted to be single. */
function section(host: ScreenElement): FakeScreen {
  const found = all(host, CREDENTIALS_SERVICE_CLASS)
  expect(found, `expected the section in .${CREDENTIALS_SERVICE_CLASS}`).toHaveLength(1)
  const root = found[0]
  if (root === undefined) throw new Error('no section')
  return root
}

/** draw builds the model from the read's own document and draws it into a host. */
function draw(inputs: CredentialsServiceInputs): FakeScreen {
  const host = factory.create('div')
  renderCredentialsService(factory, inputs, host)
  return section(host)
}

/** fact is the value drawn beside one term of a row's definition list. */
function fact(row: ScreenElement, term: string): string | null {
  const cells = (row as FakeScreen).childNodes()
  const list = cells.find((cell) => cell.tag === 'dl')
  if (list === undefined) throw new Error('the row carries no dl')
  const pairs = (list as FakeScreen).childNodes()
  for (let index = 0; index + 1 < pairs.length; index += 2) {
    if (pairs[index]?.textContent === term) return pairs[index + 1]?.textContent ?? null
  }
  return null
}

// ---------------------------------------------------------------------------
// The documents.
// ---------------------------------------------------------------------------

/** check is one line of the check, as `doctor --json` writes it. */
function check(fields: {
  ok?: boolean
  severity?: string
  problem?: string
  fix?: string
}): Record<string, unknown> {
  return { id: CREDENTIALS_SERVICE_CHECK_ID, ...fields }
}

/** report is the document `doctor --json` answers with: the checks, and the
 * envelope a real document carries beside them. */
function report(checks: readonly unknown[]): Record<string, unknown> {
  return { ok: false, checks, plugin: { version: '0.120.0', pin: '0.120.0' } }
}

/**
 * SERVICE_STATES are the lines the CLI writes for this one check, in the
 * design's own order (`docs/credentials-service/default-on.md` §3 DON7): the
 * installation that never took the opt-in, the block written and no service
 * behind it, the deployed service whose bundle holds no version, the deployed
 * service whose newest version Secret Manager will not serve, the deployed
 * service whose verify route was emptied, a job account the service does not
 * admit, and the healthy line.
 *
 * The strings are the CLI's own shapes, abbreviated where the CLI's sentence is
 * long: what this file holds is that **the section draws the document's words**,
 * and the length of a word is the CLI's business.
 */
const SERVICE_STATES: readonly { readonly name: string; readonly line: Record<string, unknown> }[] = [
  {
    name: 'the installation never took the opt-in',
    line: check({
      ok: true,
      severity: 'info',
      problem:
        'the installation has no credentials service (fugaro-credentials): an ordinary fugaro update deploys one on every installation that can hold it, and this installation has either not run an update of this release or cannot carry one',
      fix: 'nothing to run for this: the deploy alone switches nothing on, and a run reaches the service only where the bundle holds a model credential and that repository’s own root has been re-applied with the switch',
    }),
  },
  {
    name: 'the block is written and Cloud Run has no service',
    line: check({
      ok: false,
      severity: 'warning',
      problem:
        'the local config opted this installation into the credentials service (fugaro-credentials), and Cloud Run has no service fugaro-credentials in project example-cloud, region europe-west1: every run of it would fail to fetch a model credential',
      fix: 'fugaro update (re-applies the installation root, which creates the service)',
    }),
  },
  {
    name: 'the deployed service’s bundle holds no version',
    line: check({
      ok: false,
      severity: 'warning',
      problem:
        'the credentials service fugaro-credentials is deployed and serving, and it admits nobody: the bundle it serves from (fugaro-credentials) holds no version, so the loader fails closed and every model call of a run against it is refused',
      fix: 'fugaro creds set NAME (the only writer of the bundle: no update writes a credential)',
    }),
  },
  {
    name: 'the newest version is not ENABLED',
    line: check({
      ok: false,
      severity: 'warning',
      problem:
        'the credentials service fugaro-credentials is deployed and serving, and it admits nobody: the newest version of the bundle it serves from (fugaro-credentials, version 3) is DISABLED, so the loader’s read of it is refused',
      fix: 'gcloud secrets versions enable 3 --secret=fugaro-credentials, then fugaro creds set NAME writes a new one',
    }),
  },
  {
    name: 'the verify route was emptied',
    line: check({
      ok: true,
      severity: 'info',
      problem:
        'the credentials service fugaro-credentials is deployed and serving, and it admits nobody on the verify route: the verifier list it is deployed with is empty (this checkout’s config writes verifier_accounts: []), so nothing can ask whether a verdict’s signature is this service’s',
      fix: 'name the installation’s own verifier account in this checkout’s config and re-apply it, or take the key out of the config to keep the module’s own account admitted',
    }),
  },
  {
    name: 'a job account the service does not admit',
    line: check({
      ok: false,
      severity: 'warning',
      problem:
        'the credentials service fugaro-credentials is deployed and grants no roles/run.invoker to example-repo (example-workflow): a run of example-repo would fail to fetch a model credential',
      fix: 'fugaro update --repo <each repository named above> (grants each of its job accounts roles/run.invoker on the service)',
    }),
  },
  { name: 'the service is deployed, serving and admitting its callers', line: check({ ok: true }) },
]

/**
 * HELD_OFF are the per-repository lines the same check reports once DON8 lands
 * (DON7's design, §3: one line per repository, under the same ID, naming why
 * that repository's runs do not go through the service). The section draws them
 * because it reads the ID and not a position; the fixture is here so a console
 * that assumed exactly one line fails.
 */
const HELD_OFF: readonly Record<string, unknown>[] = [
  check({
    ok: true,
    severity: 'info',
    problem: 'example-one/alpha goes through the credentials service: the bundle holds its credential and its agent.auth is cascade',
    fix: '',
  }),
  check({
    ok: true,
    severity: 'info',
    problem: 'example-one/beta is held off: the service serves a cascade workflow today, and this repository’s agent.auth is oauth',
    fix: 'set agent.auth: cascade on this repository and re-apply its root',
  }),
  check({
    ok: true,
    severity: 'info',
    problem: 'example-one/gamma is held off: the bundle holds nothing to replace the job’s credential',
    fix: 'fugaro creds set NAME (the only writer of the bundle: no update writes a credential)',
  }),
  check({
    ok: true,
    severity: 'info',
    problem: 'example-one/delta is held off: this repository has not been converged since the switch landed',
    fix: 'fugaro update turns it on once the bundle is ready',
  }),
]

/** OTHER_CHECKS is a document that carries doctor's other lines and not this one:
 * the shape a CLI older than DON7 answers with. */
const OTHER_CHECKS: readonly Record<string, unknown>[] = [
  { id: 'secret-rotation-age', ok: true, severity: 'info', problem: 'nothing to report', fix: '' },
  {
    id: 'policy-published',
    ok: false,
    severity: 'info',
    problem: 'a policy is published at config/policy/shared (version 3, 2 allow-list entries)',
    fix: 'fugaro project policy show prints it',
  },
]

// ---------------------------------------------------------------------------
// The cases.
// ---------------------------------------------------------------------------

describe('the credentials service state, drawn from doctor’s own line', () => {
  it('adds no operation: it draws the read CM4 put on the console’s surface', () => {
    const operations = CONSOLE_OPERATIONS.map((operation) => operation.name)
    expect(operations).toContain(CREDENTIALS_SERVICE_OPERATION)
    expect(CREDENTIALS_SERVICE_OPERATION).toBe('environment.report')
    const root = draw({ raw: report([SERVICE_STATES[6]?.line]) })
    expect(root.getAttribute('data-operation')).toBe(CREDENTIALS_SERVICE_OPERATION)
    expect(root.getAttribute('data-panel')).toBe(CREDENTIALS_SERVICE_PANEL_KEY)
    expect(one(root, 'projects-credentials-service-source').textContent).toBe(CREDENTIALS_SERVICE_SOURCE)
    expect(one(root, 'projects-credentials-service-title').textContent).toBe(CREDENTIALS_SERVICE_TITLE)
    expect(root.getAttribute('data-state')).toBe('read')
    expect(one(root, 'projects-credentials-service-state').textContent).toBe('read')
  })

  it('draws the service states as the CLI words them, and classifies none of them itself', () => {
    for (const state of SERVICE_STATES) {
      const root = draw({ raw: report([state.line]) })
      const rows = all(root, 'projects-credentials-service-row')
      expect(rows, state.name).toHaveLength(1)
      const row = rows[0]
      if (row === undefined) throw new Error(state.name)
      expect(row.getAttribute('data-ok'), state.name).toBe(
        state.line['ok'] === true ? 'true' : 'false',
      )
      expect(texts(row, 'projects-credentials-service-check-id'), state.name).toEqual([
        CREDENTIALS_SERVICE_CHECK_ID,
      ])
      // The severity is the CLI's own word and the section's own state word is
      // the CLI's own boolean: neither is a classification the console made.
      const severity = state.line['severity']
      if (severity === undefined) {
        expect(fact(row, CREDENTIALS_SERVICE_SEVERITY_TERM), state.name).toBe(UNAVAILABLE_MARK)
      } else {
        expect(fact(row, CREDENTIALS_SERVICE_SEVERITY_TERM), state.name).toBe(severity)
      }
      // The problem and the fix are the document's own sentences, drawn as the
      // refusal door leaves them.
      for (const [term, field] of [
        [CREDENTIALS_SERVICE_PROBLEM_TERM, 'problem'],
        [CREDENTIALS_SERVICE_FIX_TERM, 'fix'],
      ] as const) {
        const raw = state.line[field]
        if (raw === undefined || raw === '') {
          expect(fact(row, term), `${state.name} / ${term}`).toBe(UNAVAILABLE_MARK)
          continue
        }
        const door = sanitizeRefusal(raw, 'the line')
        if (!door.ok) throw new Error(`${state.name} / ${term}: the door refused`)
        expect(fact(row, term), `${state.name} / ${term}`).toBe(door.text)
      }
    }
  })

  it('keeps the remedy: a fix naming a role is drawn character for character', () => {
    // The free-text `sentence` class draws `roles/run.invoker` as `[redacted]`,
    // which takes the whole value of the fix away; the refusal door keeps it.
    const root = draw({ raw: report([SERVICE_STATES[5]?.line]) })
    const row = all(root, 'projects-credentials-service-row')[0]
    if (row === undefined) throw new Error('no row')
    expect(fact(row, CREDENTIALS_SERVICE_PROBLEM_TERM)).toContain('roles/run.invoker')
    expect(fact(row, CREDENTIALS_SERVICE_FIX_TERM)).toContain('roles/run.invoker')
  })

  it('still redacts a credential a child echoed into its line', () => {
    const echoed = `the credentials service could not be read: gcloud printed token=${githubToken()}`
    const root = draw({
      raw: report([check({ ok: false, severity: 'warning', problem: echoed, fix: 'fugaro update' })]),
    })
    const row = all(root, 'projects-credentials-service-row')[0]
    if (row === undefined) throw new Error('no row')
    const problem = fact(row, CREDENTIALS_SERVICE_PROBLEM_TERM) ?? ''
    expect(problem).toContain(TOKEN_PLACEHOLDER)
    expect(problem).not.toContain(githubToken())
  })

  it('draws every line the document carries under the one ID, in its own order', () => {
    const root = draw({ raw: report([SERVICE_STATES[6]?.line, ...HELD_OFF]) })
    expect(root.getAttribute('data-state')).toBe('read')
    const rows = all(root, 'projects-credentials-service-row')
    expect(rows).toHaveLength(HELD_OFF.length + 1)
    // The held-off rows are drawn as the CLI wrote them, each with its own
    // reason -- the two an operator acts on name a credential and an auth mode.
    const problems = texts(root, 'projects-credentials-service-check-id')
    expect(problems).toHaveLength(HELD_OFF.length + 1)
    // The service's own line is first (the healthy one carries no problem), and
    // the four repository lines follow it in the document's order.
    const drawnProblems = rows.map((row) => fact(row, CREDENTIALS_SERVICE_PROBLEM_TERM))
    expect(drawnProblems[0]).toBe(UNAVAILABLE_MARK)
    expect(drawnProblems[2]).toContain('agent.auth is oauth')
    expect(drawnProblems[3]).toContain('the bundle holds nothing')
    expect(drawnProblems[4]).toContain('not been converged')
  })

  it('says the check is not in the document rather than drawing the service off', () => {
    const root = draw({ raw: report(OTHER_CHECKS) })
    // The document answered and carried no such line: that is the state, and it
    // is drawn as itself -- a CLI older than DON7 reports no service state at
    // all, and the absence of the check is not the service being off.
    expect(root.getAttribute('data-state')).toBe('no-check')
    expect(one(root, 'projects-credentials-service-no-check').textContent).toBe(
      CREDENTIALS_SERVICE_NO_CHECK,
    )
    expect(all(root, 'projects-credentials-service-row')).toHaveLength(0)
    expect(drawn(root)).not.toMatch(/\boff\b/)
    // And it is not the read case with nothing to show: a document that carried
    // the line is drawn as rows even when the line is the healthy one.
    const healthy = draw({ raw: report([SERVICE_STATES[6]?.line]) })
    expect(healthy.getAttribute('data-state')).toBe('read')
    expect(all(healthy, 'projects-credentials-service-row')).toHaveLength(1)
  })

  it('says no document arrived, naming the read a person can run', () => {
    const root = draw({ raw: null })
    expect(root.getAttribute('data-state')).toBe('absent')
    expect(one(root, 'projects-credentials-service-because').textContent).toBe(
      CREDENTIALS_SERVICE_ABSENT,
    )
    expect(CREDENTIALS_SERVICE_ABSENT).toContain(CREDENTIALS_SERVICE_SOURCE)
    expect(one(root, 'projects-credentials-service-state').textContent).toBe('not read')
  })

  it('draws the CLI’s own refusal sentence, and refuses one it cannot draw', () => {
    const because = 'doctor could not run: no credentials for project example-cloud'
    expect(one(draw({ raw: null, because }), 'projects-credentials-service-because').textContent).toBe(
      because,
    )
    // A refusal the door refuses is not drawn as an empty cell, and one that is
    // not a sentence at all is the console's own sentence naming the read.
    expect(
      one(
        draw({ raw: null, because: 'x'.repeat(MAX_TEXT_INPUT_LENGTH + 1) }),
        'projects-credentials-service-because',
      ).textContent,
    ).toBe(REFUSAL_UNREADABLE)
    expect(
      one(draw({ raw: null, because: 7 as unknown as string }), 'projects-credentials-service-because')
        .textContent,
    ).toBe(CREDENTIALS_SERVICE_ABSENT)
  })

  it('refuses a document that carries no checks array', () => {
    const root = draw({ raw: { ok: false, plugin: {} } })
    expect(root.getAttribute('data-state')).toBe('shape')
    expect(one(root, 'projects-credentials-service-shape').textContent).toBe(
      CREDENTIALS_SERVICE_SHAPE,
    )
    expect(all(root, 'projects-credentials-service-row')).toHaveLength(0)
  })

  it('reads no credential: the module names one operation and no bundle command', () => {
    // The mutation this holds is the plan's: read the bundle's own configuration
    // instead of the check -- name `secrets.inventory`, or any operation of the
    // console's surface but doctor's -- and this case fails. The scan is over
    // the module's **code**, so a header comment that names the read this
    // section refuses does not read as a breach of it.
    const code = codeOnly(readFileSync(MODULE, 'utf8'))
    const named = CONSOLE_OPERATIONS.map((operation) => operation.name).filter((name) =>
      code.includes(name),
    )
    expect(named).toEqual(['environment.report'])
    expect(CREDENTIALS_SERVICE_OPERATION).toBe('environment.report')
    expect(code).not.toContain('creds')
    expect(code).not.toContain('secrets')
  })

  it('draws no control, and offers no way to change the state it shows', () => {
    const root = draw({ raw: report([SERVICE_STATES[5]?.line, ...HELD_OFF]) })
    const CONTROLS = ['button', 'a', 'input', 'select', 'textarea', 'form', 'label']
    for (const node of walk(root)) {
      expect(CONTROLS, `a section that shows a fact offers no control: ${node.tag}`).not.toContain(
        node.tag,
      )
    }
  })

  it('holds the model apart from the tree, so its rules are testable in plain Node', () => {
    const model = buildCredentialsService({ raw: report([SERVICE_STATES[6]?.line]) })
    expect(model.state).toBe('read')
    expect(model.because).toBeNull()
    expect(model.checks).toHaveLength(1)
    expect(model.checks[0]?.ok).toBe(true)
    expect(model.checks[0]?.severity.reading?.because).toBe(
      CREDENTIALS_SERVICE_MEMBER_ABSENT_BECAUSE,
    )

    const noCheck = buildCredentialsService({ raw: report(OTHER_CHECKS) })
    expect(noCheck.state).toBe('no-check')
    expect(noCheck.checks).toEqual([])

    // The two words the section writes for the CLI's own boolean.
    expect(CREDENTIALS_SERVICE_OK_WORD).toBe('ok')
    expect(CREDENTIALS_SERVICE_REPORTED_WORD).toBe('reported')
    expect(CREDENTIALS_SERVICE_UNSTATED_WORD).toBe('not stated')
    expect(CREDENTIALS_SERVICE_BUSY).toContain('credentials service')
    const word = all(draw({ raw: report([SERVICE_STATES[1]?.line]) }), 'projects-credentials-service-check-state')[0]
    expect(word?.textContent).toBe(CREDENTIALS_SERVICE_REPORTED_WORD)
    // A row the document carries without the boolean says so rather than
    // claiming a pass.
    const bare = { id: CREDENTIALS_SERVICE_CHECK_ID }
    expect(
      all(draw({ raw: report([bare]) }), 'projects-credentials-service-check-state')[0]?.textContent,
    ).toBe(CREDENTIALS_SERVICE_UNSTATED_WORD)
  })

  it('draws the same section through the fill and the one-call renderer', () => {
    const host = factory.create('div')
    fillCredentialsService(factory, buildCredentialsService({ raw: report(OTHER_CHECKS) }), host)
    expect(drawn(section(host))).toBe(drawn(draw({ raw: report(OTHER_CHECKS) })))
  })
})

/** MODULE is the screen this file tests, read as source by the no-read case. */
const MODULE = join(__dirname, '..', '..', 'src', 'renderer', 'screens', 'credentialsservice.ts')

/** codeOnly blanks comments, so a header that explains a rule is not read as a
 * breach of it. It is a plain scanner and not a parser: the module's own strings
 * carry no `//` and no `/*`, which is what makes it safe here. */
function codeOnly(source: string): string {
  return source.replace(/\/\*[\s\S]*?\*\//g, ' ').replace(/(^|[^:])\/\/[^\n]*/g, '$1')
}
