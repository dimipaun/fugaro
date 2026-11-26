// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The new operations' contract (desktop-console improvements plan, C-OPS;
// `docs/console-improvements/contract-ops.md`).
//
// This is the test the contract is judged by, and it is written **before** the
// types it reads exist: the plan's red step is that "a stub answer with a
// missing field fails the decode before the type exists", so every assertion
// below fails while `src/shared/new-ops.ts` holds nothing, and the missing-field
// case is the first of them to be tried rather than the last.
//
// What it pins, and why each half is here:
//
//   1. **the nine names and their bridge methods** -- a name the allow-list task
//      (AL) spells differently, or a method the renderer cannot reach, is a
//      consumer written against a contract nobody implements;
//   2. **the closed payloads** -- `settings.editor.open` takes `{key}` and
//      nothing else, `setup.apply` takes nothing at all, and `setup.plan` takes
//      an answers object and never a path: these are the three the plan names,
//      and the ones a renderer could otherwise use to name a file or a project
//      the owner never saw;
//   3. **a fixture per operation that decodes against its type** -- the answers
//      the consumers build against (`test/support/newOpAnswers.ts`, which is
//      where C-OPS's stub fixtures went when I-OPS deleted the stub bridge),
//      held to the contract in the one place both are readable;
//   4. **the three things each side may rely on**, each as its own case: an
//      unknown role reads as `none`, a stream without an `end` is interrupted,
//      and an answers object never names a file;
//   5. **the typed read over the structural bridge** -- `newOpsFrom` answers a
//      value, a refusal, or `not available`, and never throws for a bridge that
//      is missing, silent or wrong;
//   6. **the text classes the contract names** are the console's own closed set,
//      so SRC's readers sanitize every answer through a class that exists.

import { readFileSync } from 'node:fs'

import { describe, expect, it } from 'vitest'

import { RIGOR_MODE_WORDS } from '../../src/shared/rigor'
import { TEXT_CLASSES } from '../../src/shared/sanitize'
import type { Skew } from '../../src/shared/schema'
import {
  NEW_OPERATION_METHODS,
  NEW_OPERATION_NAMES,
  NEW_OPERATION_PAYLOAD_KEYS,
  NEW_OPERATION_TEXT_CLASSES,
  SETUP_ANSWER_KEYS,
  SETUP_ANSWERS_FILE_KEYS,
  SETUP_BUDGET_MAX,
  SETUP_BUDGET_MIN,
  SETUP_RIGOR_MODES,
  decodeNewOperationAnswer,
} from '../../src/shared/new-ops'
import type { NewOperationName } from '../../src/shared/new-ops'
import { newOpsFrom } from '../../src/renderer/new-bridge'
import { NEW_OP_ANSWERS, newOpAnswersBridge } from '../support/newOpAnswers'

/** THE_NINE is the plan's own list, in its own order: the names this contract
 * exists for, spelled as the CLI's operations are. */
const THE_NINE: readonly NewOperationName[] = Object.freeze([
  'runs.stream',
  'spend.stream',
  'budget.stream',
  'whoami',
  'image.catalog.ls',
  'recipes.ls',
  'setup.plan',
  'setup.apply',
  'settings.editor.open',
])

/** clone is one fixture with one field taken out of it, or put back: every case
 * below that mutates an answer does it on a copy, so the fixture the next case
 * reads is the fixture the bridge answers with. */
function clone<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T
}

/** refusalOf is a decode's refusal, as `field: name`, or a sentence saying the
 * decode did not refuse at all -- which is what a case that expects a refusal
 * must read to be about the field it means. */
function refusalOf(name: NewOperationName, answer: unknown): string {
  const decoded = decodeNewOperationAnswer(name, answer)
  if (decoded.ok) return `accepted (${JSON.stringify(decoded.value)})`
  const skew: Skew = decoded.skew
  return `${skew.refusal.field}: ${skew.refusal.name}`
}

describe('the nine names and the bridge methods they are reached by', () => {
  it('is exactly the plan’s nine, spelled as the CLI spells them', () => {
    expect(NEW_OPERATION_NAMES).toEqual(THE_NINE)
  })

  it('gives every name one method, and no method to two names', () => {
    expect(Object.keys(NEW_OPERATION_METHODS).sort()).toEqual([...THE_NINE].sort())
    const methods = THE_NINE.map((name) => NEW_OPERATION_METHODS[name])
    expect(new Set(methods).size).toBe(THE_NINE.length)
    for (const method of methods) expect(method).toMatch(/^[a-z][A-Za-z]*$/)
  })
})

describe('the payloads the renderer may fill in', () => {
  it('is a closed list per operation, and the three the plan names are exact', () => {
    expect(Object.keys(NEW_OPERATION_PAYLOAD_KEYS).sort()).toEqual([...THE_NINE].sort())
    // The write that opens the settings editor names one key and nothing else:
    // the layer and the phase stay main-process decisions (AL).
    expect(NEW_OPERATION_PAYLOAD_KEYS['settings.editor.open']).toEqual(['key'])
    // Apply carries nothing: it reuses the answers main held at plan time, so a
    // renderer cannot slip new ones in at apply.
    expect(NEW_OPERATION_PAYLOAD_KEYS['setup.apply']).toEqual([])
    // Plan carries an answers object, and the object is the whole payload.
    expect(NEW_OPERATION_PAYLOAD_KEYS['setup.plan']).toEqual(['answers'])
  })
})

describe('the fixtures answer every operation with a value that decodes', () => {
  it('has a fixture for every one of the nine, and no fixture for anything else', () => {
    expect(Object.keys(NEW_OP_ANSWERS).sort()).toEqual([...THE_NINE].sort())
  })

  it('decodes each of the nine against its own type', () => {
    for (const name of THE_NINE) {
      const decoded = decodeNewOperationAnswer(name, NEW_OP_ANSWERS[name])
      expect(decoded.ok, `${name}: ${refusalOf(name, NEW_OP_ANSWERS[name])}`).toBe(true)
    }
  })

  it('refuses a fixture answer with a missing field, by the field’s own name', () => {
    // The plan's red step, run first: the decode is what tells a fixture that
    // has stopped carrying what a consumer reads from one that has not.
    const whoami = clone(NEW_OP_ANSWERS['whoami']) as Record<string, unknown>
    const document = whoami['document'] as Record<string, unknown>
    delete document['role']
    expect(refusalOf('whoami', whoami)).toBe('role: missing')
  })

  it('refuses a fixture answer whose document is not there at all', () => {
    expect(refusalOf('recipes.ls', {})).toBe('document: missing')
    expect(refusalOf('budget.stream', { document: [] })).toBe('the document: not-an-object')
  })

  it('reads a role the console does not know as none', () => {
    const whoami = clone(NEW_OP_ANSWERS['whoami']) as Record<string, unknown>
    ;(whoami['document'] as Record<string, unknown>)['role'] = 'auditor'
    const decoded = decodeNewOperationAnswer('whoami', whoami)
    expect(decoded.ok).toBe(true)
    if (decoded.ok) expect((decoded.value as { role: string }).role).toBe('none')
  })

  it('reads a stream with no end line as interrupted, and never as complete', () => {
    const runs = clone(NEW_OP_ANSWERS['runs.stream']) as Record<string, unknown>
    const lines = (runs['document'] as { lines: readonly unknown[] }).lines
    ;(runs['document'] as { lines: readonly unknown[] }).lines = lines.filter(
      (line) => (line as { type?: string }).type !== 'end',
    )
    const decoded = decodeNewOperationAnswer('runs.stream', runs)
    expect(decoded.ok).toBe(true)
    if (decoded.ok) {
      const feed = decoded.value as { end: unknown; failure: unknown; rows: readonly unknown[] }
      expect(feed.end).toBeNull()
      expect(feed.failure).toBeNull()
      expect(feed.rows.length).toBeGreaterThan(0)
    }
  })

  it('reads a stream’s error line as the failure, with the CLI’s own sentence', () => {
    const budget = clone(NEW_OP_ANSWERS['budget.stream']) as Record<string, unknown>
    const document = budget['document'] as { lines: readonly unknown[] }
    document.lines = [
      ...document.lines,
      { type: 'error', message: 'the budget database could not be read' },
    ]
    const decoded = decodeNewOperationAnswer('budget.stream', budget)
    expect(decoded.ok).toBe(true)
    if (decoded.ok) {
      const feed = decoded.value as { end: unknown; failure: string | null }
      expect(feed.end).toBeNull()
      expect(feed.failure).toBe('the budget database could not be read')
    }
  })

  it('ends the setup feed on its done line, and never on a line it does not know', () => {
    const plan = clone(NEW_OP_ANSWERS['setup.plan']) as Record<string, unknown>
    const document = plan['document'] as { lines: readonly unknown[] }
    const decoded = decodeNewOperationAnswer('setup.plan', plan)
    expect(decoded.ok).toBe(true)
    if (decoded.ok) {
      const feed = decoded.value as { done: boolean; failure: unknown; events: readonly unknown[] }
      expect(feed.done).toBe(true)
      expect(feed.failure).toBeNull()
      expect(feed.events.length).toBe(document.lines.length)
    }
    document.lines = [...document.lines, { type: 'progress' }]
    const last = document.lines.length - 1
    expect(refusalOf('setup.plan', plan)).toBe(`lines[${String(last)}].type: unknown-value`)
  })
})

describe('an answers object never names a file', () => {
  it('carries the six questions, and the six are the wizard’s own', () => {
    // The order is the CLI's own (`setupAnswerQuestions`): `budget` sits between
    // the rigor mode and the bake switch, which is where D2210 asks it.
    expect(SETUP_ANSWER_KEYS).toEqual([
      'gcp_project',
      'region',
      'name',
      'rigor',
      'budget',
      'bake_repos',
    ])
  })

  it('answers only members the answers file itself declares, with the alert’s own bounds', () => {
    // The file the CLI decodes is `schemas/setup-answers.schema.json`, and it is
    // the authority for both halves of this: a question this console asks must be
    // a member **of that document** (`additionalProperties: false` refuses any
    // other by name), and the budget's two bounds must be the alert's own. Read
    // as bytes off the repository rather than restated, because a schema that
    // moved its range and a console that kept the old one is exactly the drift
    // this catches.
    const schema = JSON.parse(
      readFileSync(new URL('../../../schemas/setup-answers.schema.json', import.meta.url), 'utf8'),
    ) as {
      readonly properties: Readonly<Record<string, { readonly minimum?: number; readonly maximum?: number }>>
      readonly additionalProperties: boolean
    }
    const members = Object.keys(schema.properties)
    // The console asks a **subset** of the file: `location` and `budget_currency`
    // are members the CLI answers for itself (the place's region, and USD,
    // D2211), so a console key outside the file is the failure and not a missing
    // one.
    for (const key of SETUP_ANSWER_KEYS) {
      expect(members, `${key} is not a member of the answers file`).toContain(key)
    }
    expect(schema.additionalProperties).toBe(false)
    expect(schema.properties['budget']?.minimum).toBe(SETUP_BUDGET_MIN)
    expect(schema.properties['budget']?.maximum).toBe(SETUP_BUDGET_MAX)
  })

  it('leaves the file’s version to main, which writes the file', () => {
    // The renderer sends the six answers; the file main hands the CLI on stdin
    // carries the schema's `version` beside them (`schemas/setup-answers.json`,
    // ST1), and `version` is nobody's answer.
    expect(SETUP_ANSWERS_FILE_KEYS).toEqual(['version', ...SETUP_ANSWER_KEYS])
    expect(SETUP_ANSWER_KEYS).not.toContain('version')
  })

  it('has no key of the six that could name a path', () => {
    for (const key of SETUP_ANSWERS_FILE_KEYS) {
      expect(key, `${key} is not an answer`).not.toMatch(/file|path|stdin/i)
    }
  })

  it('names the five rigor modes the console already draws', () => {
    expect([...SETUP_RIGOR_MODES].sort()).toEqual(Object.keys(RIGOR_MODE_WORDS).sort())
    expect(SETUP_RIGOR_MODES).toContain('poc')
  })
})

describe('the typed read over the structural bridge', () => {
  it('answers a value for each of the nine, from the fixture bridge', async () => {
    const ops = newOpsFrom(newOpAnswersBridge())
    const runs = await ops.streamRuns({ since: '7d' })
    expect(runs.ok).toBe(true)
    if (runs.ok) expect(runs.value.end).not.toBeNull()
    const spend = await ops.streamSpend({ by: 'day', since: '30d' })
    expect(spend.ok).toBe(true)
    if (spend.ok) expect(spend.value.rows.length).toBeGreaterThan(0)
    const budget = await ops.streamBudget({})
    expect(budget.ok).toBe(true)
    if (budget.ok) expect(budget.value.failure).toBeNull()
    const whoami = await ops.whoAmI({})
    expect(whoami.ok).toBe(true)
    // The recorded answer, not an invented one: WH1's golden is an operator
    // reaching every repository of one host (`ops-contract.test.ts` holds the
    // fixture to those bytes).
    if (whoami.ok) expect(whoami.value.role).toBe('operator')
    const images = await ops.listImageCatalog({})
    expect(images.ok).toBe(true)
    if (images.ok) expect(images.value.entries.length).toBeGreaterThan(0)
    const recipes = await ops.listRecipes({})
    expect(recipes.ok).toBe(true)
    if (recipes.ok) expect(recipes.value.recipes.length).toBeGreaterThan(0)
    const plan = await ops.planSetup({ answers: SETUP_ANSWERS })
    expect(plan.ok).toBe(true)
    if (plan.ok) expect(plan.value.done).toBe(true)
    const apply = await ops.applySetup({})
    expect(apply.ok).toBe(true)
    if (apply.ok) expect(apply.value.done).toBe(true)
    const editor = await ops.openSettingsEditor({ key: 'agent.models.coder' })
    expect(editor.ok).toBe(true)
    if (editor.ok) expect(editor.value.ok).toBe(true)
  })

  it('reads every one of the nine as not available over a bridge with no methods', async () => {
    const ops = newOpsFrom({})
    const methodNames = THE_NINE.map((name) => NEW_OPERATION_METHODS[name]) as readonly string[]
    expect(methodNames).toContain('streamRuns')
    for (const method of methodNames) {
      const reply = await (ops as unknown as Record<string, (args: unknown) => Promise<unknown>>)[
        method
      ]?.({})
      expect(reply, `${method} is not a method of the typed read`).toBeDefined()
      const answer = reply as { ok: boolean; gap?: { gap: string } }
      expect(answer.ok).toBe(false)
      expect(answer.gap?.gap).toBe('not-available')
    }
  })

  it('answers a refusal for a bridge whose channel said no, and not-available for one that threw', async () => {
    const refused = newOpsFrom({
      whoAmI: async () => ({ ok: false, reason: 'the CLI is older than this operation' }),
    })
    const answer = await refused.whoAmI({})
    expect(answer.ok).toBe(false)
    if (!answer.ok) {
      expect(answer.gap.gap).toBe('refused')
      expect(answer.gap.sentence).toBe('the CLI is older than this operation')
    }

    const thrown = newOpsFrom({
      whoAmI: async () => {
        throw new Error('no handler behind this channel')
      },
    })
    const failed = await thrown.whoAmI({})
    expect(failed.ok).toBe(false)
    if (!failed.ok) expect(failed.gap.gap).toBe('not-available')
  })

  it('answers unreadable for a reply nobody can read, never an empty document', async () => {
    const unreadable = newOpsFrom({ whoAmI: async () => ({ ok: true, data: 7 }) })
    const answer = await unreadable.whoAmI({})
    expect(answer.ok).toBe(false)
    if (!answer.ok) expect(answer.gap.gap).toBe('unreadable')

    const skewed = newOpsFrom({
      whoAmI: async () => ({ ok: true, data: { document: { role: 'operator' } } }),
    })
    const decoded = await skewed.whoAmI({})
    expect(decoded.ok).toBe(false)
    if (!decoded.ok) expect(decoded.gap.sentence).toContain('missing')
  })
})

describe('the text classes the contract names', () => {
  it('are the console’s own closed set', () => {
    const classes = Object.values(NEW_OPERATION_TEXT_CLASSES)
    expect(classes.length).toBeGreaterThan(3)
    for (const [key, textClass] of Object.entries(NEW_OPERATION_TEXT_CLASSES)) {
      expect(TEXT_CLASSES, `${key} carries a class the console cannot draw`).toContain(textClass)
    }
  })
})

/** SETUP_ANSWERS is the answers object the fixture bridge's `setup.plan` answer
 * stands for: the six questions, filled in. */
const SETUP_ANSWERS = Object.freeze({
  gcp_project: 'fugaro-dev',
  region: 'europe-west1',
  name: 'fugaro',
  rigor: 'startup' as const,
  budget: 250,
  bake_repos: false,
})
