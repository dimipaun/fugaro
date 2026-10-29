// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See console/LICENSE.md for the terms.
// The Live view's launch control (console v2 plan V2.5.1b; design section 5.1
// rule 11).
//
// V2.5.1a put the *operation* on the surface: `run.launch` / `launchRun`, its
// closed five-field schema, the conversion from a repository identity to a clone
// URL, and the confirmation window the main process owns. This suite is the
// renderer half, and it is written against the two rules that make that half
// safe to have at all:
//
//   - **the renderer cannot name a path, a flag or a ref.** The one write this
//     surface carries takes five fields, and the call this form makes is held to
//     exactly those five keys: a repository that is one of the installation's own
//     recorded identities, a task that is text, and nothing else. The `--pr=1`,
//     `/etc/passwd` and `../x` values below are the point -- they are all
//     *values* of those five fields and never keys -- and the assertion that the
//     argument object's key set is exactly the schema's is what makes them text
//     rather than a flag or a path. Its mutation proof is in the case itself: the
//     same predicate over an object with a sixth key must reject it.
//   - **the renderer adds no confirmation.** Main re-validates every field and
//     its own window is the only confirmation, so this form has no "are you
//     sure", no preview of the argv and no run id of its own: it says in one
//     sentence that a window will open, and then asks for it.
//
// What is drawn is this build's own decoding: the reply envelope and the outcome
// are read closed, and a reply this build does not understand is one sentence
// rather than a partial drawing. Every refusal is main's own sentence as a text
// node -- the hostile reason below carries markup, and the assertion is that the
// markup is *text* and that no element was built from it.
//
// The entry is driven through `bootDashboard` against the fake document, the way
// `entry-wiring.test.ts` drives it: these cases are about the *wiring* (which
// view offers the control, what a press calls, what is disabled while a call is
// out), and the drawing itself is `screens/launch.ts`.

import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import { DECLINED_CANCELLED } from '../../src/main/launch'
import {
  MAX_LAUNCH_PLAN_TASK_BYTES,
  MAX_LAUNCH_TASK_BYTES,
  MAX_LAUNCH_TITLE_RUNES,
} from '../../src/main/launchrules'
import { bootDashboard } from '../../src/renderer/entry'
import {
  LAUNCH_ALREADY_WORD,
  LAUNCH_COUNT_CLASS,
  LAUNCH_DECLINED_REASON,
  LAUNCH_DECLINED_WORD,
  LAUNCH_EMPTY_CLASS,
  LAUNCH_FLOOR_CLASS,
  LAUNCH_FORM_CLASS,
  LAUNCH_LAUNCHED_WORD,
  LAUNCH_LABEL_CLASS,
  LAUNCH_NEEDS_CLI,
  LAUNCH_OPEN_CLASS,
  LAUNCH_OUTCOME_CLASS,
  LAUNCH_OUTCOME_UNKNOWN_WORD,
  LAUNCH_PLAN_TASK_CLASS,
  LAUNCH_PLAN_TASK_MAX_BYTES,
  LAUNCH_REPLAYED_WORD,
  LAUNCH_REPOSITORY_CLASS,
  LAUNCH_SUBMIT_CLASS,
  LAUNCH_TASK_CLASS,
  LAUNCH_TASK_MAX_BYTES,
  LAUNCH_TITLE_CLASS,
  LAUNCH_TITLE_MAX_RUNES,
  LAUNCH_UNREADABLE,
} from '../../src/renderer/screens/launch'
import { loadFixture } from '../../src/source/fixture'
import { FakeDocument, FakeNode, anOkCompatibility, everyNode, keyEvent } from '../support/fakeDom'

const CONSOLE_ROOT = join(__dirname, '..', '..')

/** syntheticFrame is the committed recording's first document, verified by the
 * console's own loader -- the same frames the stub CLI prints for the e2e. The
 * Live view is drawn from it, so the launch control is exercised on a screen
 * that really was drawn rather than on an empty panel. */
function syntheticFrame(): Readonly<Record<string, unknown>> {
  const text = readFileSync(join(CONSOLE_ROOT, 'test', 'fixtures', 'synthetic.ndjson'), 'utf8')
  const record = loadFixture(text, 'test/fixtures/synthetic.ndjson').records.find(
    (each) => 'document' in each,
  )
  if (record === undefined || !('document' in record)) {
    throw new Error('the fixture carries no document')
  }
  return record.document as Readonly<Record<string, unknown>>
}

/** nowStamp is the console's own clock shape: a frame stamped now is a feed that
 * has not gone quiet. */
function nowStamp(): string {
  return new Date().toISOString().replace(/\.\d{3}Z$/, 'Z')
}

/** AReply is one bridge method's answer. */
type AReply = (args: unknown) => unknown

/** ACall is one recorded bridge call. */
interface ACall {
  readonly name: string
  readonly args: unknown
}

/** AHarness is the document a case asserts on and the calls the fake bridge
 * recorded. */
interface AHarness {
  readonly document: FakeDocument
  readonly calls: readonly ACall[]
}

/** OPEN_REPOSITORIES is what the installation the console is open on records.
 * The list is the main process's own -- already deduplicated, in the CLI's
 * order -- and the select is drawn from it as it stands. */
const OPEN_REPOSITORIES: readonly string[] = Object.freeze([
  'github/owner/repo-a',
  'github/owner/repo-b',
])

/** openRow is one `installation.list` row as the main process answers it: the
 * fields the rail reads, and the identities a launch may name. */
function openRow(repositories: readonly string[] = OPEN_REPOSITORIES): Record<string, unknown> {
  return {
    project: 'orbit',
    open: true,
    active: true,
    saved: true,
    checkouts: [],
    repositories,
    state: { issue: 'none', readable: true, sentence: '', command: '', details: [] },
  }
}

/**
 * fakeBridge is the preload seam, recorded: the handshake, the installation, the
 * ten reads, the rail's list and -- this slice's subject -- `launchRun`, which
 * every case either answers with a reply or leaves to fail.
 */
function fakeBridge(
  options: {
    readonly compatibility?: Record<string, unknown>
    readonly rows?: readonly Record<string, unknown>[]
    readonly launch?: AReply
  } = {},
): { readonly bridge: Record<string, unknown>; readonly calls: ACall[] } {
  const calls: ACall[] = []
  const frame = syntheticFrame()
  const bridge: Record<string, unknown> = {
    getCompatibility: (): unknown => ({
      ok: true,
      data: options.compatibility ?? anOkCompatibility(),
    }),
    getInstallation: (): unknown => ({
      ok: true,
      data: {
        chosen: true,
        id: '0123456789abcdef',
        project: 'orbit',
        binary: '/opt/fugaro/bin/fugaro',
        cliVersion: '1.2.3',
        mode: 'ok',
      },
    }),
  }
  for (const name of [
    'subscribeLive',
    'showBudget',
    'runReport',
    'listRuns',
    'statsRuns',
    'coordinatorStatus',
    'diagnoseRun',
    'readRunLogs',
    'runLogLink',
    'planStatus',
    'listInstallations',
  ]) {
    bridge[name] = (args: unknown): unknown => {
      calls.push({ name, args })
      if (name === 'subscribeLive') {
        return {
          ok: true,
          data: {
            document: frame,
            receivedAt: nowStamp(),
            sequence: 1,
            day: '2026-10-15',
            rolled: false,
            notices: [],
          },
        }
      }
      if (name === 'listInstallations') {
        return { ok: true, data: { rows: options.rows ?? [openRow()], warnings: [] } }
      }
      return { ok: false, reason: `the console did not read ${name} in this case` }
    }
  }
  bridge['launchRun'] = (args: unknown): unknown => {
    calls.push({ name: 'launchRun', args })
    return options.launch === undefined
      ? { ok: false, reason: 'the console did not launch in this case' }
      : options.launch(args)
  }
  return { bridge, calls }
}

/** boot runs the entry and answers the harness once the first Live read has been
 * drawn -- which is the moment the launch control exists. */
async function boot(options: Parameters<typeof fakeBridge>[0] = {}): Promise<AHarness> {
  const { bridge, calls } = fakeBridge(options)
  const document = new FakeDocument()
  await bootDashboard({ fugaro: bridge, document: document as unknown as Document })
  return { document, calls }
}

/** flush lets the entry's own microtasks run: every read is answered through a
 * promise, and a drawing lands a turn after the call that asked for it. */
async function flush(): Promise<void> {
  for (let i = 0; i < 20; i += 1) await Promise.resolve()
}

/** all is every node carrying a class, in document order. */
function all(root: FakeNode, className: string): readonly FakeNode[] {
  return everyNode(root).filter((node) =>
    (node.attributes.get('class') ?? '').split(/\s+/).includes(className),
  )
}

/** one is the single node carrying a class, asserted to be single. */
function one(root: FakeNode, className: string): FakeNode {
  const found = all(root, className)
  expect(found, `expected exactly one .${className}`).toHaveLength(1)
  const first = found[0]
  if (first === undefined) throw new Error(`no .${className}`)
  return first
}

/** textOf is a node's own text, with its children's, in document order. */
function textOf(node: FakeNode): string {
  return everyNode(node)
    .map((each) => each.textContent ?? '')
    .filter((text) => text !== '')
    .join(' ')
}

/** valueOf is the value a control carries, which the fake holds only where the
 * entry (or a case) put one. */
function valueOf(node: FakeNode): string {
  const value = (node as unknown as { readonly value?: unknown }).value
  return typeof value === 'string' ? value : ''
}

/** set writes one control's value the way an operator's typing would, and fires
 * the event the console counts bytes on: a value written without it would be a
 * case testing a counter that never ran. */
function set(root: FakeNode, className: string, value: string): void {
  const node = one(root, className)
  ;(node as unknown as { value: string }).value = value
  for (const listener of node.listeners.get('input') ?? []) listener(keyEvent(''))
}

/** disabledOf is whether a button is disabled right now. */
function disabledOf(node: FakeNode): boolean {
  return (node as unknown as { readonly disabled?: unknown }).disabled === true
}

/** openForm presses the control that opens the form inline, and gives the select
 * the value a real one reports on its own: the first option it offers. A `select`
 * is the one control whose value a browser derives rather than a case typing it,
 * and the fake node derives nothing. */
async function openForm(root: FakeNode): Promise<void> {
  one(root, LAUNCH_OPEN_CLASS).click()
  await flush()
  const select = all(root, LAUNCH_REPOSITORY_CLASS)[0]
  if (select === undefined) return
  ;(select as unknown as { value: string }).value = OPEN_REPOSITORIES[0] ?? ''
}

/** submit presses the form's one submit. */
async function submit(root: FakeNode): Promise<void> {
  one(root, LAUNCH_SUBMIT_CLASS).click()
  await flush()
}

/** show moves the navigation to one view. */
async function show(root: FakeNode, view: string): Promise<void> {
  const button = everyNode(root).find((node) => node.attributes.get('data-view') === view)
  if (button === undefined) throw new Error(`the navigation offers no ${view} view`)
  button.click()
  await flush()
}

/** launchCalls is every `launchRun` the bridge recorded, by its argument. */
function launchCalls(calls: readonly ACall[]): readonly unknown[] {
  return calls.filter((call) => call.name === 'launchRun').map((call) => call.args)
}

/** EXACTLY_THE_FIVE is the schema's own set of five keys, and the whole of what
 * a renderer may say about a launch. */
const EXACTLY_THE_FIVE: readonly string[] = Object.freeze([
  'installation',
  'repository',
  'task',
  'title',
  'planTask',
])

/** isExactlyTheFive is the headline's predicate: the five keys, no sixth, and no
 * key shaped like a flag (a leading `-`), a path (a `/`) or an assignment (a
 * `=`). It is a function rather than an inline `expect` so that the mutation
 * below can run the same check over a deliberately broken object. */
function isExactlyTheFive(args: unknown): boolean {
  if (typeof args !== 'object' || args === null || Array.isArray(args)) return false
  const keys = Object.keys(args as Record<string, unknown>)
  if (keys.length !== EXACTLY_THE_FIVE.length) return false
  for (const key of keys) {
    if (!EXACTLY_THE_FIVE.includes(key)) return false
    if (key.startsWith('-') || key.includes('/') || key.includes('=')) return false
  }
  return true
}

/** aResult is one `launchResult` as the main process decodes it: camelCase,
 * because that decoder has already read the CLI's snake_case document. */
function aResult(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    project: 'orbit',
    run: '20261029-120000-abcd',
    repo: 'github/owner/repo-a',
    runId: '20261029-120000-abcd',
    branch: 'main',
    ref: 'main',
    execution: 'cloud-run',
    logUrl: 'https://logs.example.invalid/run/20261029-120000-abcd',
    status: 'launched',
    pr: null,
    previousRun: '',
    recipe: null,
    projectLayer: null,
    ...overrides,
  }
}

/** aRefusal is one refused `LaunchOutcome`, inside the reply envelope. */
function aRefusal(reason: string, runId: string | null = null): Record<string, unknown> {
  return {
    ok: true,
    data: { ok: false, runId, result: null, reason, outcomeUnknown: false, replayed: false },
  }
}

/** launched is one accepted `LaunchOutcome`, inside the reply envelope. */
function launched(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    ok: true,
    data: {
      ok: true,
      runId: '20261029-120000-abcd',
      result: aResult(),
      reason: '',
      outcomeUnknown: false,
      replayed: false,
      ...overrides,
    },
  }
}

describe('the launch control', () => {
  it('exists on the Live view and on no other view', async () => {
    const { document } = await boot()

    expect(all(document.body, LAUNCH_OPEN_CLASS)).toHaveLength(1)
    // The form is drawn hidden: "Launch a run" is a button, and a form that was
    // already open would be a button that did nothing.
    expect(one(document.body, LAUNCH_FORM_CLASS).getAttribute('hidden')).not.toBeNull()
    await openForm(document.body)
    expect(one(document.body, LAUNCH_FORM_CLASS).getAttribute('hidden')).toBeNull()

    for (const view of ['graph', 'spend', 'projects', 'coordinator']) {
      await show(document.body, view)
      expect(all(document.body, LAUNCH_OPEN_CLASS), `${view} offers a launch control`).toEqual([])
      expect(all(document.body, LAUNCH_SUBMIT_CLASS), `${view} offers a submit`).toEqual([])
    }
    await show(document.body, 'live')
    expect(all(document.body, LAUNCH_OPEN_CLASS)).toHaveLength(1)
  })

  it('asks for exactly the schema’s five keys, and carries no path and no flag', async () => {
    const { document, calls } = await boot()
    await openForm(document.body)

    // Every one of these is a value an operator could type: a flag, a path and
    // a relative escape. None of them is a *key*, and the assertion below is
    // what makes the difference.
    set(document.body, LAUNCH_REPOSITORY_CLASS, 'github/owner/repo-b')
    set(document.body, LAUNCH_TASK_CLASS, '/etc/passwd')
    set(document.body, LAUNCH_TITLE_CLASS, '--pr=1')
    set(document.body, LAUNCH_PLAN_TASK_CLASS, '../x')
    await submit(document.body)

    const sent = launchCalls(calls)
    expect(sent, 'the form made no launch call').toHaveLength(1)
    const args = sent[0] as Record<string, unknown>
    expect(Object.keys(args).sort()).toEqual([...EXACTLY_THE_FIVE].sort())
    expect(isExactlyTheFive(args)).toBe(true)
    // The mutation proof: the same predicate over an object with a sixth key --
    // a `pr` a later edit might add, or a flag spelled as one -- must reject it,
    // so the assertion above is not one that would pass on any object at all.
    expect(isExactlyTheFive({ ...args, pr: 1 })).toBe(false)
    expect(isExactlyTheFive({ ...args, '--pr': 1 })).toBe(false)
    expect(args['installation']).toBe('orbit')
    expect(args['repository']).toBe('github/owner/repo-b')
    expect(args['task']).toBe('/etc/passwd')
    expect(args['title']).toBe('--pr=1')
    expect(args['planTask']).toBe('../x')
  })

  it('offers no repository, and no submit, when the installation records none', async () => {
    const { document, calls } = await boot({ rows: [openRow([])] })
    await openForm(document.body)

    expect(textOf(one(document.body, LAUNCH_EMPTY_CLASS))).toContain('records no repository')
    expect(all(document.body, LAUNCH_SUBMIT_CLASS)).toEqual([])
    expect(launchCalls(calls)).toEqual([])
  })

  it('offers no control, and reaches no launch, below the CLI’s floor', async () => {
    const { document, calls } = await boot({
      compatibility: { ...anOkCompatibility(), cliVersion: '0.11.1' },
    })

    expect(all(document.body, LAUNCH_OPEN_CLASS)).toEqual([])
    expect(all(document.body, LAUNCH_SUBMIT_CLASS)).toEqual([])
    expect(textOf(one(document.body, LAUNCH_FLOOR_CLASS))).toBe(LAUNCH_NEEDS_CLI)
    expect(launchCalls(calls)).toEqual([])
  })

  it('disables the submit while a call is out, and re-enables it on either settlement', async () => {
    let settle: (reply: unknown) => void = () => undefined
    const pending = new Promise<unknown>((resolve) => {
      settle = resolve
    })
    const { document } = await boot({ launch: () => pending })
    await openForm(document.body)
    set(document.body, LAUNCH_TASK_CLASS, 'a task')

    one(document.body, LAUNCH_SUBMIT_CLASS).click()
    await flush()
    expect(disabledOf(one(document.body, LAUNCH_SUBMIT_CLASS))).toBe(true)
    expect(one(document.body, LAUNCH_FORM_CLASS).getAttribute('aria-busy')).toBe('true')

    settle(launched())
    await flush()
    expect(disabledOf(one(document.body, LAUNCH_SUBMIT_CLASS))).toBe(false)
    expect(one(document.body, LAUNCH_FORM_CLASS).getAttribute('aria-busy')).toBe('false')

    // A rejected call is a settlement too: a flag left set would block every
    // later attempt.
    const rejecting = await boot({ launch: () => Promise.reject(new Error('the call failed')) })
    await openForm(rejecting.document.body)
    set(rejecting.document.body, LAUNCH_TASK_CLASS, 'a task')
    await submit(rejecting.document.body)
    expect(disabledOf(one(rejecting.document.body, LAUNCH_SUBMIT_CLASS))).toBe(false)
  })

  it('draws a refusal as text, and builds no element from it', async () => {
    // Markup a CLI could print and an operator would then be reading: it must
    // arrive as characters, never as an element.
    const hostile = 'the CLI refused: <a href=/etc/passwd>open</a> and <img src=x onerror=y>'
    const { document } = await boot({ launch: () => ({ ok: false, reason: hostile }) })
    await openForm(document.body)
    set(document.body, LAUNCH_TASK_CLASS, 'a task')
    await submit(document.body)

    const drawn = textOf(one(document.body, LAUNCH_OUTCOME_CLASS))
    expect(drawn).toContain('<a href=')
    expect(drawn).toContain('<img src=x')
    expect(drawn).toContain('</a>')
    expect(
      everyNode(document.body).filter((node) => node.tagName === 'A' || node.tagName === 'IMG'),
    ).toEqual([])
  })

  it('draws each outcome state as its own sentence, and offers no retry', async () => {
    const unknownReason =
      'the child was killed, so whether run 20261029-120000-abcd started is unknown. Read `fugaro runs ls --json`, or `fugaro diagnose 20261029-120000-abcd --json`, to settle it.'
    const states: readonly {
      readonly name: string
      readonly reply: unknown
      readonly want: readonly string[]
    }[] = [
      {
        name: 'launched',
        reply: launched(),
        want: [
          LAUNCH_LAUNCHED_WORD,
          '20261029-120000-abcd',
          'github/owner/repo-a',
          'main',
          'cloud-run',
          'https://logs.example.invalid/run/20261029-120000-abcd',
        ],
      },
      {
        name: 'already-launched',
        reply: launched({ result: aResult({ status: 'already-launched' }) }),
        want: [LAUNCH_ALREADY_WORD, '20261029-120000-abcd'],
      },
      {
        name: 'replayed',
        reply: launched({ replayed: true }),
        want: [LAUNCH_LAUNCHED_WORD, LAUNCH_REPLAYED_WORD],
      },
      {
        name: 'declined',
        reply: aRefusal(DECLINED_CANCELLED),
        want: [LAUNCH_DECLINED_WORD],
      },
      {
        name: 'outcome unknown',
        reply: {
          ok: true,
          data: {
            ok: false,
            runId: '20261029-120000-abcd',
            result: null,
            reason: unknownReason,
            outcomeUnknown: true,
            replayed: false,
          },
        },
        want: [
          LAUNCH_OUTCOME_UNKNOWN_WORD,
          'fugaro runs ls --json',
          'fugaro diagnose 20261029-120000-abcd --json',
        ],
      },
      {
        name: 'refused',
        reply: aRefusal('the budget pre-check refused this launch'),
        want: ['the budget pre-check refused this launch'],
      },
      {
        name: 'a reply this build does not understand',
        reply: launched({ extra: 1 }),
        want: [LAUNCH_UNREADABLE],
      },
      {
        name: 'a refusal from the operation itself',
        reply: { ok: false, reason: 'the repository is not one this installation records' },
        want: ['the repository is not one this installation records'],
      },
    ]
    for (const state of states) {
      const { document } = await boot({ launch: () => state.reply })
      await openForm(document.body)
      set(document.body, LAUNCH_TASK_CLASS, 'a task')
      await submit(document.body)
      const region = one(document.body, LAUNCH_OUTCOME_CLASS)
      const drawn = textOf(region)
      for (const want of state.want) {
        expect(drawn, `${state.name}: ${want}`).toContain(want)
      }
      // Open point 6 stays open: there is no retry control anywhere in the
      // outcome, whatever the state.
      expect(
        everyNode(region).filter((node) => node.tagName === 'BUTTON'),
        state.name,
      ).toEqual([])
      expect(all(region, LAUNCH_SUBMIT_CLASS), state.name).toEqual([])
    }
  })

  it('clears the task a launch accepted, and keeps the fields a refusal leaves', async () => {
    const accepted = await boot({ launch: () => launched() })
    await openForm(accepted.document.body)
    set(accepted.document.body, LAUNCH_TASK_CLASS, 'a task')
    await submit(accepted.document.body)
    expect(valueOf(one(accepted.document.body, LAUNCH_TASK_CLASS))).toBe('')
    expect(textOf(one(accepted.document.body, LAUNCH_COUNT_CLASS))).toBe('0 of 16 KiB')

    const refused = await boot({ launch: () => aRefusal('a refusal') })
    await openForm(refused.document.body)
    set(refused.document.body, LAUNCH_TASK_CLASS, 'a task')
    await submit(refused.document.body)
    expect(valueOf(one(refused.document.body, LAUNCH_TASK_CLASS))).toBe('a task')
  })

  it('gives every control an accessible name and the lines that describe it', async () => {
    const { document } = await boot()
    await openForm(document.body)

    const labels = all(document.body, LAUNCH_LABEL_CLASS).map((node) => node.getAttribute('for'))
    expect(labels.length).toBe(4)
    for (const className of [
      LAUNCH_REPOSITORY_CLASS,
      LAUNCH_TASK_CLASS,
      LAUNCH_TITLE_CLASS,
      LAUNCH_PLAN_TASK_CLASS,
    ]) {
      const control = one(document.body, className)
      const id = control.getAttribute('id')
      expect(id, `${className} has no id`).toBeTruthy()
      expect(labels, `${className} has no label`).toContain(id)
      const described = (control.getAttribute('aria-describedby') ?? '').split(/\s+/)
      expect(described.length, `${className} describes nothing`).toBeGreaterThanOrEqual(2)
      for (const each of described) {
        expect(
          everyNode(document.body).filter((node) => node.getAttribute('id') === each),
          `${className} names no ${each}`,
        ).toHaveLength(1)
      }
    }
    expect(textOf(one(document.body, LAUNCH_SUBMIT_CLASS))).not.toBe('')
    expect(one(document.body, LAUNCH_OUTCOME_CLASS).getAttribute('aria-live')).toBe('polite')
  })

  it('counts the task’s bytes as they are typed, against the cap', async () => {
    const { document } = await boot()
    await openForm(document.body)

    const counter = (): string => textOf(one(document.body, LAUNCH_COUNT_CLASS))
    expect(counter()).toBe('0 of 16 KiB')
    set(document.body, LAUNCH_TASK_CLASS, '  abc  ')
    expect(counter()).toBe('3 of 16 KiB')
    set(document.body, LAUNCH_TASK_CLASS, 'é')
    expect(counter()).toBe('2 of 16 KiB')
  })
})

describe('the mirrors of the main process', () => {
  it('carries the caps launchrules enforces, to the number', () => {
    // A screen may not read a main-process value, so the three are mirrored --
    // and this is where the mirror is held to the original: a cap that drifted
    // would be a form that refuses a value main would accept, or offers one it
    // will refuse.
    expect(LAUNCH_TASK_MAX_BYTES).toBe(MAX_LAUNCH_TASK_BYTES)
    expect(LAUNCH_TITLE_MAX_RUNES).toBe(MAX_LAUNCH_TITLE_RUNES)
    expect(LAUNCH_PLAN_TASK_MAX_BYTES).toBe(MAX_LAUNCH_PLAN_TASK_BYTES)
  })

  it('carries the cancel sentence a decline is told apart by', () => {
    // The renderer separates "nothing was launched, because you said so" from
    // every other refusal by the sentence alone, so the two strings are one
    // fact: a reworded cancel sentence must fail here rather than be drawn as a
    // refusal with no explanation.
    expect(LAUNCH_DECLINED_REASON).toBe(DECLINED_CANCELLED)
  })
})
