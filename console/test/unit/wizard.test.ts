// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The first-run setup wizard's screen (the console-improvements plan, task
// SW3b: D2904, D2905, O6, F15, F27; `console/src/renderer/wizard/wizard.ts`,
// and the onboarding view's own door to it in `entry.ts`).
//
// **The bridge here is this file's own**, built from the contract's types
// (`src/shared/new-ops.ts`) rather than from the fixture bridge: I-OPS deletes
// `test/support/bridge-stub.ts`, and a screen's test that leaned on it would be
// a test of a stub that no longer exists. The two members it answers are the
// only two the wizard touches -- `setup.plan` and `setup.apply` -- and both
// answer the shape main's own handlers answer: the console's `{ok, data}` reply
// wrapping `{document: {lines: [...]}}`, which `decodeNewOperationAnswer` reads
// into a `SetupFeed`.
//
// Each case names the mutation it catches:
//
//   - a **rigor select with a default**, or a form that sent an empty one,
//     fails the red step: a press with no mode draws the console's own refusal
//     naming the question and asks main for nothing at all;
//   - a screen wrapped in a **`<form>`** (Enter submits it) fails the same
//     case's second half, which asserts no `form` was made and that the submit
//     is a `type="button"`;
//   - a **wrong default** (a region that is not the CLI's, baking off) fails the
//     defaults case, which reads the region out of the input's own `value` and
//     the bake out of which option is `selected`;
//   - an answers object assembled from anything but the five rows fails the
//     send case, which compares the payload with the object it typed into the
//     form, member for member;
//   - a feed drawn **out of order**, or one whose kinds are lost, fails the
//     event-order case;
//   - a `left_for_you` line whose **Copy is drawn for a word MG0's builder
//     refuses** (or for a line that opens with neither of the two commands)
//     fails the hand-off case, which reads the clipboard after each press;
//   - an **`error` line drawn without its stage** fails the failure case;
//   - a screen that **guesses about the apply** fails the follow case: with no
//     plan confirmed, the press draws main's own refusal and no event;
//   - a read that **ended without a last line** drawn as a finished run fails
//     the interruption case, which asserts the console's own sentence and a
//     Retry that asks again.

import { describe, expect, it } from 'vitest'

import { everyNode, FakeDocument, textOf } from '../support/fakeDom'
import type { FakeNode } from '../support/fakeDom'
import { BUSY_BUSY_ATTRIBUTE, RETRY_CLASS, RETRY_LABEL } from '../../src/renderer/section'
import { STREAM_INTERRUPTED_WORD } from '../../src/renderer/stream'
import {
  domSurface,
  drawOnboarding,
  clipboardCopy,
} from '../../src/renderer/entry'
import type { DomSurface } from '../../src/renderer/entry'
import type { SkewElement } from '../../src/shared/version'
import { ONBOARDING_CLASS, ONBOARDING_COMMAND_CLASS, ONBOARDING_TITLE_CLASS } from '../../src/renderer/entry'
import { newOpsFrom } from '../../src/renderer/new-bridge'
import {
  WIZARD_ANSWER_CLASS,
  WIZARD_APPLY_BUSY,
  WIZARD_ATTEMPT_CLASS,
  WIZARD_BUSY_CLASS,
  WIZARD_CLASS,
  WIZARD_COMMANDS_LEAD,
  WIZARD_COPY_CLASS,
  WIZARD_COPY_REFUSAL_CLASS,
  WIZARD_CONTROL_ID_PREFIX,
  WIZARD_COPY_WORDS,
  WIZARD_DONE,
  WIZARD_EVENT_ATTRIBUTE,
  WIZARD_EVENT_CLASS,
  WIZARD_FOLLOW_CLASS,
  WIZARD_HAND_OFF,
  WIZARD_INPUT_CLASS,
  WIZARD_KIND_WORDS,
  WIZARD_LABEL_CLASS,
  WIZARD_LEAD_CLASS,
  WIZARD_NO,
  WIZARD_OPTION_CLASS,
  WIZARD_PLAN_BUSY,
  WIZARD_QUESTION_ATTRIBUTE,
  WIZARD_QUESTION_CLASS,
  WIZARD_QUESTIONS,
  WIZARD_RIGOR_MODES,
  WIZARD_SUBMIT_CLASS,
  WIZARD_SUBMIT_LABEL,
  WIZARD_YES,
  drawWizard,
  wizardNotAChoice,
  wizardUnanswered,
} from '../../src/renderer/wizard/wizard'
import { NEW_OPS_NOT_AVAILABLE, SETUP_ANSWER_KEYS, SETUP_RIGOR_MODES } from '../../src/shared/new-ops'
import type { NewOps, SetupAnswers } from '../../src/shared/new-ops'

const FIRST = Object.freeze({ kind: 'first-installation' as const, command: 'fugaro setup', reason: '' })

/** Line is one line of a `setup --events` feed (ST2): the five kinds, and no
 * member a reader of this contract cannot place. */
type Line = Readonly<Record<string, unknown>>

/** SetupCall is one operation the wizard asked, in the order it asked it. */
interface SetupCall {
  readonly name: 'planSetup' | 'applySetup'
  readonly payload: unknown
}

/** SetupHarness is this file's own fake bridge, the calls it recorded, the
 * clipboard the Copy buttons wrote to, and the screen drawn over them. */
interface SetupHarness {
  readonly surface: DomSurface
  readonly root: FakeNode
  readonly ops: NewOps
  readonly calls: SetupCall[]
  readonly clipboard: string[]
}

/** feedOf is one operation's answer: the console's `{ok, data}` reply around
 * the document the child printed, the shape `decodeNewOperationAnswer` reads. */
function feedOf(lines: readonly Line[]): Record<string, unknown> {
  return { ok: true, data: { document: { lines } } }
}

/**
 * setupHarness builds the fake bridge and draws the wizard over it: `plan` and
 * `apply` are the two operations' answers, called in whatever order the screen
 * asks for them. The clock is the frame the press happened in (`paint`), so
 * "the events are drawn in order" is a claim about the drawing rather than
 * about the host's timer.
 */
function setupHarness(
  plan: (payload: unknown) => Record<string, unknown> = () => feedOf(DONE),
  apply: (payload: unknown) => Record<string, unknown> = () => feedOf(DONE),
  bridge: Record<string, unknown> = {},
): SetupHarness {
  const calls: SetupCall[] = []
  const clipboard: string[] = []
  const fugaro = {
    ...bridge,
    planSetup: async (payload: unknown): Promise<unknown> => {
      calls.push({ name: 'planSetup', payload })
      return plan(payload)
    },
    applySetup: async (payload: unknown): Promise<unknown> => {
      calls.push({ name: 'applySetup', payload })
      return apply(payload)
    },
  }
  const document = new FakeDocument()
  const surface = domSurface(document as unknown as Document)
  const ops = newOpsFrom(fugaro)
  const wizard = drawWizard({
    factory: surface.factory,
    plan: (request) => ops.planSetup(request),
    apply: (request) => ops.applySetup(request),
    copy: (text) => clipboard.push(text),
    paint: (step) => step(),
  })
  // `nodeOf` is the real node behind the wrapper the factory made, and this
  // file's own corroboration reads the tree through it: the surface is the one
  // `entry.ts` builds, so the screen is drawn into the same DOM the console
  // draws into.
  const root = surface.nodeOf(wizard as unknown as SkewElement) as unknown as FakeNode
  document.body.appendChild(root)
  return { surface, root, ops, calls, clipboard }
}

/** classed finds the drawn elements carrying one class. */
function classed(root: FakeNode, className: string): readonly FakeNode[] {
  return everyNode(root).filter((node) => node.getAttribute('class') === className)
}

/** rowsOf is the form's five question rows, in the order they were drawn. */
function rowsOf(root: FakeNode): readonly FakeNode[] {
  return classed(root, WIZARD_QUESTION_CLASS)
}

/** rowFor is one question's row, by the answer key it fills. */
function rowFor(root: FakeNode, key: string): FakeNode {
  const row = rowsOf(root).find((each) => each.getAttribute(WIZARD_QUESTION_ATTRIBUTE) === key)
  if (row === undefined) throw new Error(`the wizard drew no row for ${key}`)
  return row
}

/** controlOf is the control one row is answered with. */
function controlOf(root: FakeNode, key: string): FakeNode {
  const control = classed(rowFor(root, key), WIZARD_INPUT_CLASS)[0]
  if (control === undefined) throw new Error(`the row for ${key} drew no control`)
  return control
}

/** optionsOf is a select's options, in the order they were drawn. */
function optionsOf(root: FakeNode, key: string): readonly FakeNode[] {
  return classed(rowFor(root, key), WIZARD_OPTION_CLASS)
}

/** valueOfOption is the wire word an option carries: its own `value` attribute,
 * which is what the answers object reads back. */
function valueOfOption(option: FakeNode): string {
  return option.getAttribute('value') ?? ''
}

/** answersOf is every sentence drawn in an answer line, the empty form's own
 * line left out: the console's refusals and each press's own outcome. */
function answersOf(root: FakeNode): readonly string[] {
  return classed(root, WIZARD_ANSWER_CLASS)
    .map((line) => line.textContent ?? '')
    .filter((text) => text !== '')
}

/** kindsOf is the kind of the events drawn, in the order they were drawn. */
function kindsOf(root: FakeNode): readonly (string | null)[] {
  return classed(root, WIZARD_EVENT_CLASS).map((line) => line.getAttribute(WIZARD_EVENT_ATTRIBUTE))
}

/** eventLines is one drawn event line per text run, in order: the line's own
 * words, with the empty run the container itself contributes left out. */
function eventLines(root: FakeNode): readonly string[] {
  return classed(root, WIZARD_EVENT_CLASS).map((line) =>
    textOf(line)
      .split('\n')
      .filter((part) => part !== '')
      .join(' '),
  )
}

/** submitOf is the one press that sends the answers. */
function submitOf(root: FakeNode): FakeNode {
  const submit = classed(root, WIZARD_SUBMIT_CLASS)[0]
  if (submit === undefined) throw new Error('the wizard drew no submit')
  return submit
}

/** followOf is the press that asks for the apply's own feed. */
function followOf(root: FakeNode): FakeNode {
  const follow = classed(root, WIZARD_FOLLOW_CLASS)[0]
  if (follow === undefined) throw new Error('the wizard drew no follow control')
  return follow
}

/** busyOf is the screen's one line naming the press in flight. */
function busyOf(root: FakeNode): FakeNode {
  const busy = classed(root, WIZARD_BUSY_CLASS)[0]
  if (busy === undefined) throw new Error('the wizard drew no busy line')
  return busy
}

/** attemptOf is one press's whole block, in the order the presses were made. */
function attemptOf(root: FakeNode, index = 0): FakeNode {
  const block = classed(root, WIZARD_ATTEMPT_CLASS)[index]
  if (block === undefined) throw new Error(`the wizard drew no attempt block at ${index}`)
  return block
}

/** flush lets the awaited reads settle: every step of a press is a promise. */
async function flush(): Promise<void> {
  for (let i = 0; i < 12; i += 1) await Promise.resolve()
}

/** fill answers the whole form the way an operator would: the text fields by
 * their own `value`, the two selects by the option's word. */
function fill(
  root: FakeNode,
  answers: {
    readonly gcp_project: string
    readonly region: string
    readonly name: string
    readonly rigor: string
    readonly bake_repos: string
  },
): void {
  controlOf(root, 'gcp_project').value = answers.gcp_project
  controlOf(root, 'region').value = answers.region
  controlOf(root, 'name').value = answers.name
  controlOf(root, 'rigor').value = answers.rigor
  controlOf(root, 'bake_repos').value = answers.bake_repos
}

/** DONE is a run that finished and left nothing for the operator behind it, the
 * feed a case that is not about the lines themselves answers with. */
const DONE: readonly Line[] = Object.freeze([{ type: 'done', exit_code: 0 }])

const ANSWERED = Object.freeze({
  gcp_project: 'acme-one',
  region: 'us-east5',
  name: '',
  rigor: 'pro',
  bake_repos: WIZARD_YES,
})

describe('the setup wizard’s questions', () => {
  it('asks the contract’s five questions, in its own order, with the CLI’s defaults', () => {
    const { root } = setupHarness(() => feedOf(DONE))
    expect(rowsOf(root).map((row) => row.getAttribute(WIZARD_QUESTION_ATTRIBUTE))).toEqual([
      ...SETUP_ANSWER_KEYS,
    ])
    expect(WIZARD_QUESTIONS.map((question) => question.key)).toEqual([...SETUP_ANSWER_KEYS])
    // The defaults are the CLI's own: the US place's region, and baking on.
    expect(controlOf(root, 'region').getAttribute('value')).toBe('us-east5')
    expect(controlOf(root, 'gcp_project').getAttribute('value')).toBe('')
    expect(
      optionsOf(root, 'bake_repos')
        .filter((option) => option.getAttribute('selected') === '')
        .map(valueOfOption),
    ).toEqual([WIZARD_YES])
    // The rigor mode has no default at all (D900), which is why its first option
    // is the empty "choose a mode" rather than one of the four: a select with a
    // default would offer an answer the CLI never takes by itself.
    expect(controlOf(root, 'rigor').getAttribute('value')).toBeNull()
    expect(optionsOf(root, 'rigor').map(valueOfOption)[0]).toBe('')
    // What the select shows is the empty "choose a mode": the answer a press
    // with no mode sends is `''`, which the CLI refuses (D900).
    expect(optionsOf(root, 'rigor').filter((option) => option.getAttribute('selected') === '').map(valueOfOption)).toEqual([''])
    // The form offers the four layer modes and never `hotfix`, which main
    // refuses for these answers.
    expect(WIZARD_RIGOR_MODES).not.toContain('hotfix')
    expect(optionsOf(root, 'rigor').map(valueOfOption)).toEqual(['', ...WIZARD_RIGOR_MODES])
    expect([...WIZARD_RIGOR_MODES].sort()).toEqual(
      SETUP_RIGOR_MODES.filter((mode) => mode !== 'hotfix').sort(),
    )
    // No secret is asked for here, and there is no field beyond the five.
    expect(rowsOf(root)).toHaveLength(SETUP_ANSWER_KEYS.length)
    expect(classed(root, WIZARD_LEAD_CLASS)[0]?.textContent).toContain('no secret')
  })

  it('names every control with the label beside it, and gives each one its own id', () => {
    const { root } = setupHarness(() => feedOf(DONE))
    // One label and one control per row, in the contract's order, and the label
    // points at the control beside it: a control with no accessible name is read
    // as "edit text", which is what this form drew before.
    const pairs = rowsOf(root).map((row) => ({
      id: classed(row, WIZARD_INPUT_CLASS)[0]?.getAttribute('id') ?? '',
      label: classed(row, WIZARD_LABEL_CLASS)[0]?.getAttribute('for') ?? '',
    }))
    expect(pairs.map((pair) => pair.id)).toEqual(
      [...SETUP_ANSWER_KEYS].map((key) => WIZARD_CONTROL_ID_PREFIX + key),
    )
    expect(pairs.filter((pair) => pair.label !== pair.id)).toEqual([])
    // And no two questions share an id, which a duplicate would quietly do while
    // still pairing with its own label above.
    expect(new Set(pairs.map((pair) => pair.id)).size).toBe(SETUP_ANSWER_KEYS.length)
  })

  it('sends nothing, and refuses the rigor question by name, when no mode is answered', async () => {
    const { root, calls } = setupHarness(() => feedOf(DONE))
    fill(root, { ...ANSWERED, rigor: '' })
    submitOf(root).click()
    await flush()
    expect(calls).toEqual([])
    expect(answersOf(root)).toEqual([wizardUnanswered('Rigor mode')])
    // Enter is not a second way to press this: the screen draws no `form` at
    // all, so a keystroke in a text field submits nothing.
    expect(everyNode(root).some((node) => node.tagName.toLowerCase() === 'form')).toBe(false)
    expect(submitOf(root).getAttribute('type')).toBe('button')
    expect(submitOf(root).textContent).toBe(WIZARD_SUBMIT_LABEL)
  })

  it('refuses an empty project ID the same way, and sends the object once the form is answered', async () => {
    const { root, calls } = setupHarness(() => feedOf(DONE))
    fill(root, { ...ANSWERED, gcp_project: '   ' })
    submitOf(root).click()
    await flush()
    expect(calls).toEqual([])
    expect(answersOf(root)).toEqual([wizardUnanswered('GCP project ID')])

    fill(root, ANSWERED)
    submitOf(root).click()
    await flush()
    expect(calls).toHaveLength(1)
    expect(calls[0]?.name).toBe('planSetup')
    const payload = calls[0]?.payload as { readonly answers: SetupAnswers }
    expect(payload.answers).toEqual({
      gcp_project: 'acme-one',
      region: 'us-east5',
      name: '',
      rigor: 'pro',
      bake_repos: true,
    })
    // The form's own refusal is cleared by a press that got past it: what is
    // left is the run's own sentence and nothing about the form.
    expect(answersOf(root)).toEqual([WIZARD_DONE])
  })

  it('reads baking off the switch, and sends a written no as `false`', async () => {
    const { root, calls } = setupHarness(() => feedOf(DONE))
    fill(root, { ...ANSWERED, bake_repos: WIZARD_NO })
    submitOf(root).click()
    await flush()
    const payload = calls[0]?.payload as { readonly answers: SetupAnswers }
    expect(payload.answers.bake_repos).toBe(false)
  })

  it('refuses a select answered outside its own choices, and sends nothing for it', async () => {
    const { root, calls } = setupHarness(() => feedOf(DONE))
    fill(root, { ...ANSWERED, rigor: 'a-mode-this-build-does-not-offer' })
    submitOf(root).click()
    await flush()
    expect(calls).toEqual([])
    expect(answersOf(root)).toEqual([wizardNotAChoice('Rigor mode')])
  })
})

describe('the setup wizard’s feed', () => {
  const LINES: readonly Line[] = Object.freeze([
    { type: 'step', stage: 'project', name: 'project', detail: '' },
    {
      type: 'stage',
      name: 'project',
      state: 'done',
      detail: 'the project is there',
      after: '',
      undone: false,
    },
    { type: 'step', stage: 'billing', name: 'billing', detail: '' },
    { type: 'stage', name: 'billing', state: 'left', detail: '', after: 'project', undone: false },
    { type: 'done', exit_code: 1 },
  ])

  it('draws every line in the order it arrived, with the CLI’s own fields', async () => {
    const { root } = setupHarness(() => feedOf(LINES))
    fill(root, ANSWERED)
    submitOf(root).click()
    await flush()
    expect(kindsOf(root)).toEqual(['step', 'stage', 'step', 'stage', 'done'])
    expect(eventLines(root)).toEqual([
      `${WIZARD_KIND_WORDS.step} project`,
      `${WIZARD_KIND_WORDS.stage} project done the project is there`,
      `${WIZARD_KIND_WORDS.step} billing`,
      `${WIZARD_KIND_WORDS.stage} billing left project`,
      WIZARD_KIND_WORDS.done,
    ])
    // A run that ended without leaving anything for the operator says so, and
    // draws no hand-off sentence.
    expect(answersOf(root)).toEqual([WIZARD_DONE])
  })

  it('lists every `left_for_you` line, with a Copy only for the two commands it may hand a clipboard', async () => {
    const { root, clipboard } = setupHarness(() =>
      feedOf([
        {
          type: 'left_for_you',
          stage: 'secrets',
          kind: 'secret',
          text: 'set the GitHub App key',
          commands: ['fugaro secrets set github-app-key'],
        },
        {
          type: 'left_for_you',
          stage: 'billing',
          kind: 'billing',
          text: 'link the billing account',
          commands: ['gcloud billing projects link acme-one --billing-account=<billing-account-id>'],
        },
        {
          type: 'left_for_you',
          stage: 'repos',
          kind: 'repo',
          text: 'wire the repository',
          commands: ['terraform apply in your checkout'],
        },
        { type: 'done', exit_code: 1 },
      ]),
    )
    fill(root, ANSWERED)
    submitOf(root).click()
    await flush()
    expect(kindsOf(root)).toEqual(['left_for_you', 'left_for_you', 'left_for_you', 'done'])
    const lines = eventLines(root)
    expect(lines[0]).toContain('set the GitHub App key')
    expect(lines[0]).toContain('fugaro secrets set github-app-key')
    expect(lines[0]).toContain(WIZARD_COMMANDS_LEAD)
    expect(lines[1]).toContain(
      'gcloud billing projects link acme-one --billing-account=<billing-account-id>',
    )
    expect(lines[2]).toContain('terraform apply in your checkout')
    // The one Copy is the first line's, and it puts the builder's own joined
    // argv on the clipboard: the line opens with `fugaro`, one of the two words
    // this console may copy.
    const copies = classed(root, WIZARD_COPY_CLASS)
    expect(copies).toHaveLength(1)
    expect(copies[0]?.textContent).toBe('fugaro secrets set github-app-key')
    copies[0]?.click()
    expect(clipboard).toEqual(['fugaro secrets set github-app-key'])
    // The `<billing-account-id>` placeholder is a word MG0's builder refuses, so
    // that line draws no Copy and the builder's own reason beside it -- the
    // console never repairs a command it will not copy.
    const refusals = classed(root, WIZARD_COPY_REFUSAL_CLASS)
    expect(refusals).toHaveLength(1)
    expect(refusals[0]?.textContent).not.toBe('')
    // A line that opens with neither of the two commands is displayed only.
    expect(WIZARD_COPY_WORDS).toEqual(['fugaro', 'gcloud'])
    // And the hand-off is drawn: the steps above are the operator's.
    expect(answersOf(root)).toEqual([WIZARD_DONE, WIZARD_HAND_OFF])
  })

  it('shows an `error` line’s sentence with the stage the run stopped at', async () => {
    const { root } = setupHarness(() =>
      feedOf([
        { type: 'step', stage: 'terraform', name: 'terraform', detail: '' },
        {
          type: 'error',
          stage: 'terraform',
          error: 'the Terraform apply refused the plan',
          fix: 'fugaro setup --rigor pro',
          exit_code: 1,
        },
      ]),
    )
    fill(root, ANSWERED)
    submitOf(root).click()
    await flush()
    const line = classed(root, WIZARD_EVENT_CLASS).find(
      (each) => each.getAttribute(WIZARD_EVENT_ATTRIBUTE) === 'error',
    )
    expect(line).toBeDefined()
    expect(textOf(line as FakeNode)).toContain('terraform')
    expect(textOf(line as FakeNode)).toContain('the Terraform apply refused the plan')
    expect(textOf(line as FakeNode)).toContain('fugaro setup --rigor pro')
    // The feed's own failure is drawn once, and the console adds no sentence of
    // its own about a run that reported one.
    expect(answersOf(root)).toEqual([])
  })

  it('draws an interrupted read as one, with a Retry that asks again', async () => {
    let reads = 0
    const { root, calls } = setupHarness(() => {
      reads += 1
      // The first read stops after a step: no last line, which is the read a
      // consumer must handle -- a killed child, a truncated pipe, a cap.
      return reads === 1
        ? feedOf([{ type: 'step', stage: 'project', name: 'project', detail: '' }])
        : feedOf(DONE)
    })
    fill(root, ANSWERED)
    submitOf(root).click()
    await flush()
    expect(kindsOf(root)).toEqual(['step'])
    expect(answersOf(root)).toEqual([STREAM_INTERRUPTED_WORD])
    const retry = classed(root, RETRY_CLASS)[0]
    expect(retry, 'the interrupted read drew no Retry').toBeDefined()
    expect(retry?.textContent).toBe(RETRY_LABEL)
    retry?.click()
    await flush()
    expect(calls).toHaveLength(2)
    expect(kindsOf(root)).toEqual(['step', 'done'])
  })

  it('draws a gap as the console’s own sentence, and nothing as a run', async () => {
    // A bridge with no `setup.plan` at all: the operation is not available in
    // this build, which is a gap and never an empty feed.
    const document = new FakeDocument()
    const surface = domSurface(document as unknown as Document)
    const ops = newOpsFrom({})
    const root = surface.nodeOf(
      drawWizard({
        factory: surface.factory,
        plan: (request) => ops.planSetup(request),
        apply: (request) => ops.applySetup(request),
        copy: () => {},
        paint: (step) => step(),
      }),
    ) as unknown as FakeNode
    document.body.appendChild(root)
    fill(root, ANSWERED)
    submitOf(root).click()
    await flush()
    expect(classed(root, WIZARD_EVENT_CLASS)).toHaveLength(0)
    expect(answersOf(root).join(' ')).toContain(NEW_OPS_NOT_AVAILABLE)
  })

  it('never guesses about the apply: the follow press draws main’s own refusal until a plan is confirmed', async () => {
    const { root, calls } = setupHarness(
      () => feedOf(DONE),
      () => ({ ok: false, reason: 'no setup plan is waiting: nothing is applied' }),
    )
    fill(root, ANSWERED)
    submitOf(root).click()
    await flush()
    followOf(root).click()
    await flush()
    expect(calls.map((call) => call.name)).toEqual(['planSetup', 'applySetup'])
    expect(calls[1]?.payload).toEqual({})
    expect(answersOf(root).join(' ')).toContain('no setup plan is waiting: nothing is applied')
    expect(kindsOf(root)).toEqual(['done'])
  })
})

describe('the wizard’s in-flight state', () => {
  /** Deferred is one read the case answers by hand: the promise the bridge
   * returns, and the two ways to settle it. */
  interface Deferred {
    readonly answer: Promise<Record<string, unknown>>
    release(value: Record<string, unknown>): void
    fail(reason: Error): void
  }

  /** deferred builds one read the case settles when it chooses. */
  function deferred(): Deferred {
    let release: (value: Record<string, unknown>) => void = () => {}
    let fail: (reason: Error) => void = () => {}
    const answer = new Promise<Record<string, unknown>>((resolve, reject) => {
      release = resolve
      fail = reject
    })
    return { answer, release, fail }
  }

  /** held draws the wizard over a bridge whose two reads answer only when the
   * case lets them: the frame between the press and its answer is the state
   * under test, and an answer that lands at once would never draw it. */
  function held(): {
    readonly root: FakeNode
    readonly plan: Deferred
    readonly apply: Deferred
  } {
    const plan = deferred()
    const apply = deferred()
    const document = new FakeDocument()
    const surface = domSurface(document as unknown as Document)
    const ops = newOpsFrom({
      planSetup: () => plan.answer,
      applySetup: () => apply.answer,
    })
    const root = surface.nodeOf(
      drawWizard({
        factory: surface.factory,
        plan: (request) => ops.planSetup(request),
        apply: (request) => ops.applySetup(request),
        copy: () => {},
        paint: (step) => step(),
      }),
    ) as unknown as FakeNode
    document.body.appendChild(root)
    return { root, plan, apply }
  }

  it('names the press in flight, marks the screen busy and holds the two controls that start one', async () => {
    const { root, plan } = held()
    fill(root, ANSWERED)
    // The line is drawn from the start and empty: the state has one writer and
    // one place in the tree.
    expect(busyOf(root).textContent ?? '').toBe('')
    expect(root.getAttribute(BUSY_BUSY_ATTRIBUTE)).toBeNull()
    submitOf(root).click()
    await flush()
    // Nothing has answered yet, so the press is named, the screen is marked busy
    // and the two controls that start a read are held: a second press here would
    // be a second child for one answer, and main's runs for up to an hour.
    expect(busyOf(root).textContent).toBe(WIZARD_PLAN_BUSY)
    expect(root.getAttribute(BUSY_BUSY_ATTRIBUTE)).toBe('true')
    expect(attemptOf(root).getAttribute(BUSY_BUSY_ATTRIBUTE)).toBe('true')
    expect(submitOf(root).getAttribute('disabled')).toBe('')
    expect(followOf(root).getAttribute('disabled')).toBe('')
    // The attempt is drawn empty in the same frame, which is why the busy line
    // is the screen's: a blank block with no line above it is the endless
    // spinner the console's rules refuse.
    expect(attemptOf(root).textContent ?? '').toBe('')
    plan.release(feedOf(DONE))
    await flush()
    // And the answer clears every bit of it: the line, the two marks and the two
    // held controls.
    expect(busyOf(root).textContent ?? '').toBe('')
    expect(root.getAttribute(BUSY_BUSY_ATTRIBUTE)).toBeNull()
    expect(attemptOf(root).getAttribute(BUSY_BUSY_ATTRIBUTE)).toBeNull()
    expect(submitOf(root).getAttribute('disabled')).toBeNull()
    expect(followOf(root).getAttribute('disabled')).toBeNull()
    expect(answersOf(root)).toEqual([WIZARD_DONE])
  })

  it('names the follow press in its own words, and clears the state when the read throws', async () => {
    const { root, apply } = held()
    fill(root, ANSWERED)
    followOf(root).click()
    await flush()
    expect(busyOf(root).textContent).toBe(WIZARD_APPLY_BUSY)
    expect(submitOf(root).getAttribute('disabled')).toBe('')
    expect(followOf(root).getAttribute('disabled')).toBe('')
    // A bridge that throws is not an answer, and the screen may not be left
    // marked busy for it: the mark is cleared on every path out of the send.
    apply.fail(new Error('the bridge is gone'))
    await flush()
    expect(busyOf(root).textContent ?? '').toBe('')
    expect(root.getAttribute(BUSY_BUSY_ATTRIBUTE)).toBeNull()
    expect(submitOf(root).getAttribute('disabled')).toBeNull()
    expect(followOf(root).getAttribute('disabled')).toBeNull()
  })
})

describe('the wizard in the onboarding view', () => {
  it('takes the place of the command line when the caller can drive the operations', () => {
    const document = new FakeDocument()
    const surface = domSurface(document as unknown as Document)
    const ops = newOpsFrom({
      planSetup: async () => feedOf(DONE),
      applySetup: async () => feedOf(DONE),
    })
    const section = surface.nodeOf(
      drawOnboarding(surface, FIRST, { ops, copy: () => {} }),
    ) as unknown as FakeNode
    document.body.appendChild(section)
    expect(classed(section, ONBOARDING_CLASS)).toHaveLength(1)
    expect(classed(section, ONBOARDING_TITLE_CLASS)[0]?.textContent).toBe(
      'Set up your first installation',
    )
    expect(classed(section, ONBOARDING_COMMAND_CLASS)).toHaveLength(0)
    expect(classed(section, WIZARD_CLASS)).toHaveLength(1)
    expect(rowsOf(section)).toHaveLength(SETUP_ANSWER_KEYS.length)
  })

  it('keeps the command line for a caller with no door to the operations', () => {
    const document = new FakeDocument()
    const surface = domSurface(document as unknown as Document)
    const section = surface.nodeOf(drawOnboarding(surface, FIRST)) as unknown as FakeNode
    document.body.appendChild(section)
    expect(classed(section, ONBOARDING_COMMAND_CLASS)[0]?.textContent).toBe('fugaro setup')
    expect(classed(section, WIZARD_CLASS)).toHaveLength(0)
  })

  it('is one screen of its own, which the rail’s Add project opens with no onboarding state', () => {
    // SW3c opens the same screen behind "Add project": what it takes is the
    // operations and a clipboard, never a `RailOnboarding`.
    const { root } = setupHarness(() => feedOf(DONE))
    expect(root.getAttribute('class')).toBe(WIZARD_CLASS)
    expect(rowsOf(root)).toHaveLength(SETUP_ANSWER_KEYS.length)
    expect(classed(root, WIZARD_SUBMIT_CLASS)).toHaveLength(1)
    expect(classed(root, WIZARD_FOLLOW_CLASS)).toHaveLength(1)
    expect(classed(root, ONBOARDING_COMMAND_CLASS)).toHaveLength(0)
  })
})

describe('the wizard’s clipboard', () => {
  it('copies nothing, and throws nothing, where the window has no clipboard', () => {
    expect(() => clipboardCopy(undefined)('fugaro update')).not.toThrow()
    expect(() => clipboardCopy({})('fugaro update')).not.toThrow()
    expect(() => clipboardCopy({ clipboard: {} })('fugaro update')).not.toThrow()
  })

  it('copies through `writeText` on the navigator it was given, and swallows a refused write', async () => {
    const written: string[] = []
    const copy = clipboardCopy({
      clipboard: { writeText: (text: string) => Promise.resolve(written.push(text)) },
    })
    copy('fugaro update')
    await flush()
    expect(written).toEqual(['fugaro update'])
    const refusing = clipboardCopy({
      clipboard: { writeText: (text: string) => Promise.reject(new Error(text)) },
    })
    expect(() => refusing('fugaro update')).not.toThrow()
    await flush()
  })
})
