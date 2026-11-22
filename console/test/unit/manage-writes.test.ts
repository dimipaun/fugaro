// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Manage section's two write actions and the two windows behind them (the
// console-improvements plan's task MWR; D2903; O4). It is the meeting point of
// four earlier tasks -- MW1's and MW2's operations, SU1's one window, RB4's
// gating and the kinds MO-K and MO-C drew -- so what it holds is that the three
// of them are wired to each other and not to a fourth thing:
//
//   - **the floor is the kind's and the role is the shell's.** `models add` and
//     `image add` are offered as writes only above their own CLI floor, and
//     below it the kind hands the shell a Copy-command the operator runs
//     themselves; the write control is drawn through `disabledWrite` with RB4's
//     sentence when the caller's role may not use it, and a control a role may
//     not use binds nothing at all. The plan's own red step is the second case
//     below: it fails the moment the action is enabled for every role, because
//     the shell asked no gate;
//   - **the form collects and submits, and decides nothing.** Its values go to
//     the kind's *plan* operation -- one of MW1's and MW2's own -- and what comes
//     back is main's: a window, or the CLI's own refusal sentence drawn in the
//     box. A required box that is empty leaves the submit disabled, which is a
//     pre-check for a button and never the decision (`types.ts`'s `WriteField`);
//   - **the window draws what main sent, and nothing else.** The two subjects
//     below are built by main's own builders (`modelsAddView`, `imageAddView`,
//     over plan documents read by their own strict readers), and each case
//     asserts the window's **whole** set of rows against the view's own members,
//     so a row the drawing invented -- or a member main sent and the window
//     dropped -- fails here. Every document string goes through `TEXT_CLASSES`,
//     and a value the class refuses is the console's own mark and never the
//     value.
//
// The harness is `manage-shell.test.ts`'s fake element and `confirm-view.ts`'s
// fake document, in this file's own words: the first because the shell owns its
// interaction (the control, the form, the submit) and a case has to press a
// control, the second because the window's document has no markup slot at all.

import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import {
  ADD_ENTRY_LABEL,
  ADD_MODEL_LABEL,
  ALLOWED_ABSENT_WORD,
  ALLOWED_ADDED_WORD,
  ALLOWED_NAMES_WORD,
  CONFIRM_UNDRAWABLE,
  NONE,
  NO_WORD,
  SAVED_WORD,
  TICK_ID,
  TITLE_IMAGE_ADD,
  TITLE_MODELS_ADD,
  YES_WORD,
  mountConfirm,
} from '../../src/renderer/confirm'
import type {
  ConfirmDocument,
  ConfirmElement,
  ConfirmEvent,
} from '../../src/renderer/confirm'
import type { ConfirmAnswer, ConfirmBridge, ConfirmReply } from '../../src/preload/confirm'
import { GATE_ACTIONS, gateFrom } from '../../src/renderer/gating'
import type { GateAction, GateDecision } from '../../src/renderer/gating'
import {
  MANAGE_ACTIONS_CLASS,
  MANAGE_COPY_BOX_CLASS,
  MANAGE_COPY_CLASS,
  MANAGE_COPY_CONSEQUENCE_CLASS,
  MANAGE_REFUSAL_CLASS,
  MANAGE_WRITE_CANCEL_CLASS,
  MANAGE_WRITE_CLASS,
  MANAGE_WRITE_FIELD_CLASS,
  MANAGE_WRITE_FORM_CLASS,
  MANAGE_WRITE_INPUT_CLASS,
  mountManage,
} from '../../src/renderer/manage/shell'
import type { ManageDeps, ManageElement, ManageFactory } from '../../src/renderer/manage/shell'
import { MODELS_ADD_LABEL, modelsKindFor } from '../../src/renderer/manage/kinds/models'
import { IMAGE_ADD_LABEL, imagesKindFor } from '../../src/renderer/manage/kinds/images'
import type { ManageKind, WriteAction } from '../../src/renderer/manage/types'
import { copyCommandText } from '../../src/renderer/manage/command'
import { MODELS_ADD_TICK_LABEL, modelsAddView } from '../../src/main/modelsview'
import type { ModelsAddView } from '../../src/main/modelsview'
import { IMAGE_ADD_COMMAND, imageAddView } from '../../src/main/imagesview'
import type { ImageAddView } from '../../src/main/imagesview'

// ---------------------------------------------------------------------------
// A CLI version above both floors, and the two floors themselves
// ---------------------------------------------------------------------------

/**
 * ABOVE and FLOOR are a CLI and a floor that put it above one: both floors are
 * `null` today (`MODELS_ADD_CLI_FLOOR`, `IMAGE_ADD_CLI_FLOOR`), so the branch a
 * release will take has to be driven with an explicit floor, which is why both
 * `modelsKindFor` and `imagesKindFor` take one. `BELOW` is a version under it.
 */
const ABOVE = '0.121.0'
const BELOW = '0.119.0'
const FLOOR = '0.120.0'

/** CASES is the two kinds, above their floor, with the four facts a case about a
 * write action reads: the kind's own id and group, the label on its control, the
 * plan operation its form submits to, and the command the kind offers below the
 * floor instead. */
const CASES = [
  {
    name: 'models',
    id: 'models',
    group: 'Models & keys',
    label: MODELS_ADD_LABEL,
    plan: 'models.add.plan',
    below: 'fugaro models add',
    kind: modelsKindFor(ABOVE, FLOOR),
    under: modelsKindFor(BELOW, FLOOR),
  },
  {
    name: 'images',
    id: 'images',
    group: 'Code',
    label: IMAGE_ADD_LABEL,
    plan: 'image.add.plan',
    below: 'fugaro image add',
    kind: imagesKindFor(ABOVE, FLOOR),
    under: imagesKindFor(BELOW, FLOOR),
  },
] as const

// ---------------------------------------------------------------------------
// The section's own fake: the shell's harness, in this file's words
// ---------------------------------------------------------------------------

/** FakeManage is the element the shell draws into. It records text, classes,
 * attributes and listeners, and it **throws on the attributes a section must
 * never set** -- the navigating attribute, a resource, a style and any handler.
 * `value` and `disabled` are the form's own: they are what a control holds and
 * whether a button may be pressed, and they are read back off the element rather
 * than kept beside it. */
class FakeManage implements ManageElement {
  readonly attributes = new Map<string, string>()
  readonly nodes: FakeManage[] = []
  readonly classes: string[] = []
  readonly listeners = new Map<string, (() => void)[]>()
  textContent: string | null = null
  value = ''
  disabled = false

  static readonly FORBIDDEN_ATTRIBUTES = new Set(['src', 'srcdoc', 'style', 'formaction', 'action'])

  constructor(readonly tag: string) {}

  setAttribute(name: string, value: string): void {
    const lowered = name.toLowerCase()
    if (FakeManage.FORBIDDEN_ATTRIBUTES.has(lowered) || lowered.startsWith('on')) {
      throw new Error(`a console section may not set the ${name} attribute`)
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

  append(child: ManageElement): void {
    this.nodes.push(child as unknown as FakeManage)
  }

  replaceChildren(...children: ManageElement[]): void {
    this.nodes.length = 0
    for (const child of children) this.nodes.push(child as unknown as FakeManage)
  }

  addEventListener(type: string, listener: () => void): void {
    const held = this.listeners.get(type) ?? []
    held.push(listener)
    this.listeners.set(type, held)
  }

  /** fire presses the element: every listener the shell wired on one type, in
   * the order it wired them, which is what the document does. */
  fire(type: string): void {
    for (const listener of this.listeners.get(type) ?? []) listener()
  }

  /** presses is whether this element has a listener at all: the whole of what a
   * refused write must not have. */
  get presses(): boolean {
    return this.listeners.size > 0
  }
}

const factory: ManageFactory = { create: (tag) => new FakeManage(tag) }

/** walk visits every node of a tree, the root first. */
function walk(node: ManageElement): readonly FakeManage[] {
  const fake = node as unknown as FakeManage
  const out: FakeManage[] = [fake]
  for (const child of fake.nodes) out.push(...walk(child))
  return out
}

/** all is every node carrying a class, in document order. */
function all(node: ManageElement, className: string): readonly FakeManage[] {
  return walk(node).filter((one_) => one_.classes.includes(className))
}

/** one is the single node carrying a class, asserted to be single: a case that
 * read the first of two would pass over the defect it is written to catch. */
function one(node: ManageElement, className: string): FakeManage {
  const found = all(node, className)
  expect(found, `expected exactly one .${className}`).toHaveLength(1)
  const first = found[0]
  if (first === undefined) throw new Error(`no .${className}`)
  return first
}

/** AT is the console clock the shell draws its stamp against. */
const AT = Date.UTC(2026, 10, 20, 14, 5)

/** Section is the caller's side of the shell: the reads it was asked for, in the
 * order it asked, with the answers a case decides when to give. Every read is
 * answered by the case and none by a default, because which answer a read got is
 * what these cases are about. */
class Section {
  readonly calls: { readonly operation: string; readonly args: Readonly<Record<string, unknown>> }[] = []
  readonly waits: Wait[] = []
  readonly deps: ManageDeps

  constructor(can?: (action: GateAction) => GateDecision) {
    this.deps = {
      factory,
      theme: 'dark',
      read: (operation, args) => {
        this.calls.push({ operation, args })
        const held = wait()
        this.waits.push(held)
        return held.promise
      },
      copy: () => undefined,
      openView: () => undefined,
      // A case that hands no gate reads no role, which is `gating.ts`'s
      // no-answer value: it allows every action, and the member is required so
      // that the section always has one to ask (`ManageDeps.can`).
      can: can ?? gateFrom(null).can,
      now: () => AT,
      timers: {
        setTimeout: () => 1 as unknown as ReturnType<typeof setTimeout>,
        clearTimeout: () => undefined,
      },
    }
  }

  answer(index: number, doc: unknown): void {
    this.waits[index]?.answer(doc)
  }

  refuse(index: number, reason: unknown): void {
    this.waits[index]?.refuse(reason)
  }

  /** operations is every read this section was asked for, by name. */
  get operations(): readonly string[] {
    return this.calls.map((call) => call.operation)
  }
}

interface Wait {
  readonly promise: Promise<unknown>
  answer(doc: unknown): void
  refuse(reason: unknown): void
}

function wait(): Wait {
  let answer!: (doc: unknown) => void
  let refuse!: (reason: unknown) => void
  const promise = new Promise<unknown>((onAnswer, onRefuse) => {
    answer = onAnswer
    refuse = onRefuse
  })
  return { promise, answer: (doc) => answer(doc), refuse: (reason) => refuse(reason) }
}

/** settled lets every promise a press set going land: the shell draws through
 * `.then`, so a case that asserted without this would read the tree mid-flight. */
async function settled(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 0))
}

/** drawSection opens one kind's group, answers its read with a document, and
 * answers the drawn section and its root: the whole of what a case about an
 * action needs before it presses anything. */
async function drawSection(
  kind: ManageKind,
  groupName: string,
  can?: (action: GateAction) => GateDecision,
): Promise<{ readonly section: Section; readonly root: ManageElement }> {
  const section = new Section(can)
  const root = mountManage([kind]).draw(section.deps).root
  const disclosure = walk(root).find(
    (node) => node.tag === 'details' && node.getAttribute('data-group') === groupName,
  )
  if (disclosure === undefined) throw new Error(`no group ${groupName}`)
  disclosure.setAttribute('open', '')
  disclosure.fire('toggle')
  // No rows are needed for any case here: the actions are drawn whenever the
  // read landed, an empty list included (`shell.ts`'s `actionsOf`).
  section.answer(0, {})
  await settled()
  return { section, root }
}

/** writeAction is the kind's one write action, refusing when the kind offered
 * something else: a case that expected a write and read `argv` off a union says
 * so. */
function writeAction(kind: ManageKind): WriteAction {
  const [action] = kind.actions
  if (action === undefined || action.kind !== 'write') {
    throw new Error(`the kind offers no write: ${JSON.stringify(kind.actions)}`)
  }
  return action
}

// ---------------------------------------------------------------------------
// The window's own fake document, from the real one
// ---------------------------------------------------------------------------

/** FakeWindowElement is one node of the confirmation document. It has **no
 * markup slot**: a view that reached for one is recorded here and throws, which
 * is what makes "a value is a text node" a fact this file can assert. */
class FakeWindowElement implements ConfirmElement {
  textContent: string | null = null
  tabIndex = 0
  checked = false
  disabled = false
  value = ''
  readonly attributes = new Map<string, string>()
  readonly children: FakeWindowElement[] = []
  focused = false
  readonly failures: string[] = []
  private readonly listeners = new Map<string, ((event: ConfirmEvent) => void | Promise<void>)[]>()

  constructor(readonly tag: string) {}

  set innerHTML(_markup: string) {
    this.failures.push('innerHTML')
    throw new Error('a value of the confirmation window is a text node: this element has no markup slot')
  }

  addEventListener(type: string, listener: (event: ConfirmEvent) => void | Promise<void>): void {
    const held = this.listeners.get(type) ?? []
    held.push(listener)
    this.listeners.set(type, held)
  }

  setAttribute(name: string, value: string): void {
    this.attributes.set(name, value)
  }

  getAttribute(name: string): string | null {
    return this.attributes.get(name) ?? null
  }

  appendChild(child: ConfirmElement): unknown {
    this.children.push(child as FakeWindowElement)
    return child
  }

  replaceChildren(...nodes: ConfirmElement[]): void {
    this.children.length = 0
    for (const node of nodes) this.children.push(node as FakeWindowElement)
  }

  focus(): void {
    this.focused = true
  }

  async fire(type: string, detail = 0): Promise<ConfirmEvent> {
    const event: ConfirmEvent = { detail }
    for (const listener of this.listeners.get(type) ?? []) await listener(event)
    return event
  }

  /** button finds a child button by its label. */
  button(label: string): FakeWindowElement {
    const found = this.children.find((child) => child.tag === 'button' && child.textContent === label)
    if (found === undefined) {
      throw new Error(
        `no button labelled ${JSON.stringify(label)} under <${this.tag}>; has ${JSON.stringify(this.children.map((child) => child.textContent))}`,
      )
    }
    return found
  }
}

class FakeWindowDocument implements ConfirmDocument {
  readonly byId = new Map<string, FakeWindowElement>()
  readonly created: string[] = []

  createElement(tag: string): ConfirmElement {
    this.created.push(tag)
    return new FakeWindowElement(tag)
  }

  getElementById(id: string): ConfirmElement | null {
    return this.byId.get(id) ?? null
  }
}

const html = readFileSync(
  fileURLToPath(new URL('../../src/renderer/confirm.html', import.meta.url)),
  'utf8',
)

/** fakeWindowDocument builds the document from the ids the real file declares,
 * so the seam between the file and the view is held. */
function fakeWindowDocument(): FakeWindowDocument {
  const document = new FakeWindowDocument()
  for (const match of html.matchAll(/id="([^"]+)"/g)) {
    document.byId.set(match[1] ?? '', new FakeWindowElement('div'))
  }
  return document
}

/** window is the confirmation window drawn over one subject: the document it was
 * drawn into, the answers the view gave, and the elements a case reads. */
async function drawWindow(subject: ModelsAddView | ImageAddView) {
  const document = fakeWindowDocument()
  const answers: ConfirmAnswer[] = []
  const bridge: ConfirmBridge = {
    fields: async (): Promise<ConfirmReply> => ({ ok: true, data: subject }),
    answer: async (answer: ConfirmAnswer): Promise<ConfirmReply> => {
      answers.push(answer)
      return { ok: true, data: { answered: true } }
    },
  }
  await mountConfirm(document, bridge)
  const at = (id: string): FakeWindowElement => document.byId.get(id) as FakeWindowElement
  return { document, answers, at }
}

/** Row is one row of the window: the label and **every** value drawn under it,
 * which is what makes a case able to assert the command's words one by one and
 * the steps' lines one by one. */
interface Row {
  readonly label: string
  readonly values: readonly string[]
}

/** rowsOf is the whole of what the window drew into the fields container, in
 * order: one entry per label, with the values that followed it. A value drawn
 * as a `<code>` answers with its own child's text, so a case reads what the
 * operator reads. */
function rowsOf(document: FakeWindowDocument): readonly Row[] {
  const fields = document.byId.get('confirm-fields') as FakeWindowElement
  const out: { label: string; values: string[] }[] = []
  for (const child of fields.children) {
    if (child.tag === 'dt') {
      out.push({ label: child.textContent ?? '', values: [] })
      continue
    }
    if (child.tag !== 'dd') continue
    const last = out[out.length - 1]
    if (last === undefined) continue
    last.values.push(
      child.textContent ?? child.children.map((nested) => nested.textContent ?? '').join(''),
    )
  }
  return out
}

// ---------------------------------------------------------------------------
// The documents: the CLI's own dry-run plans, as main reads them
// ---------------------------------------------------------------------------

/**
 * MODELS_PLAN is `fugaro models add --dry-run --json` (contract section 6.33),
 * with both halves of the claim's either/or settled the way an add of a model no
 * pattern covers leaves them: an `entry` the route's list would gain, and no
 * `claimed_by`.
 */
const MODELS_PLAN: unknown = {
  version: 1,
  refusal: '',
  model: 'deepseek-v4.1-flash',
  route: 'gateway',
  path: 'fugaro.yaml',
  // `local` and never another: the command edits the selected installation's own
  // file (contract section 6.33, D780), and `settingsLayerLocal` is what
  // `internal/cli/models_add.go` writes.
  layer: 'local',
  changed: true,
  claim: { entry: 'deepseek-v4.1-flash', claimed_by: '' },
  allowed_models: { entry: 'deepseek-v4.1-flash', present: true },
  prices: {
    key: 'model_prices.deepseek-v4.1-flash',
    input_per_m: 0.14,
    output_per_m: 0.28,
    source: 'catalog',
  },
  // The apply chain's own step name, which section 6.33 pins as `policy` today,
  // and the state `--no-apply` leaves it in.
  steps: [{ name: 'policy', state: 'saved' }],
}

/** IMAGE_PLAN is `fugaro image add --dry-run --json` (contract section 6.34). */
const IMAGE_PLAN: unknown = {
  version: 1,
  command: IMAGE_ADD_COMMAND,
  project: 'sandbox',
  entry: {
    name: 'ci-runner',
    tags: ['node', 'redis'],
    description: 'the repository’s own image',
    default: false,
    source: 'dockerfile owner/repo:images/ci.Dockerfile',
  },
  steps: [{ name: 'catalog-build', saved: true }],
}

const PROJECT = 'sandbox'
const DIGEST = 'a3f1c0d9e5b74f2a8c6d1e0b9a7f4c2d8e6b0a3f5c1d9e7b4a2f0c8d6e4b2a0f'
/** MODELS_ARGV and IMAGE_ARGV are the argvs main re-runs, word for word as
 * `argvFor` builds them (`console/src/main/spawn.ts`): the row's own words, then
 * one `--flag=value` word per argument the renderer sent, then the installation's
 * flags, then the row's `--` and the positional behind it. They are the **write's**
 * argv and not the dry run's -- the window shows the command the yes spawns
 * (`src/main/modelsadd.ts`) -- so neither carries `--dry-run --json`, and the
 * rows the design pins (§5.1: `--route=<route>`, `--project <name>`, `-- <ID>`)
 * are the shape a case here has to exercise. */
const MODELS_ARGV: readonly string[] = Object.freeze([
  '/usr/local/bin/fugaro',
  'models',
  'add',
  '--no-apply',
  '--route=gateway',
  '--price-in=0.14',
  '--price-out=0.28',
  '--project',
  PROJECT,
  '--',
  'deepseek-v4.1-flash',
])
const IMAGE_ARGV: readonly string[] = Object.freeze([
  '/usr/local/bin/fugaro',
  'image',
  'add',
  '--no-apply',
  '--dockerfile=owner/repo:images/ci.Dockerfile',
  '--tags=node,redis',
  '--description=the repository’s own image',
  '--project',
  PROJECT,
  '--',
  'ci-runner',
])

/** modelsWindow builds the models window's subject the way main does, refusing
 * (rather than defaulting) when the reader did not vouch for the document: a
 * case that got no view is a fixture this file has to fix. */
function modelsWindow(dryRun: unknown = MODELS_PLAN): ModelsAddView {
  const read = modelsAddView({ dryRun, project: PROJECT, digest: DIGEST, argv: MODELS_ARGV })
  if (!read.ok) throw new Error(`the models plan was refused: ${read.because}`)
  return read.view
}

function imageWindow(dryRun: unknown = IMAGE_PLAN): ImageAddView {
  const read = imageAddView({ dryRun, project: PROJECT, digest: DIGEST, argv: IMAGE_ARGV })
  if (!read.ok) throw new Error(`the image plan was refused: ${read.because}`)
  return read.view
}

// ---------------------------------------------------------------------------
// The floor: the write above it, the Copy-command below it
// ---------------------------------------------------------------------------

describe('the floor a Manage write waits on', () => {
  it('offers the write above the floor and the Copy-command below it, for both kinds', () => {
    for (const one_ of CASES) {
      const above = writeAction(one_.kind)
      expect(above.plan, one_.name).toBe(one_.plan)
      expect(above.label, one_.name).toBe(one_.label)
      // The role is never spelled at the control (F19): the action names the
      // thing it does, and the shell looks the role up.
      expect(above.gate, one_.name).toBe('manage-project')
      expect(GATE_ACTIONS['manage-project'], one_.name).toEqual({ min: 'admin', project: true })
      expect(above.fields.length, one_.name).toBeGreaterThan(0)
      expect(above.fields.filter((field) => field.required).length, one_.name).toBeGreaterThan(0)

      // Below the floor: one Copy-command and no write at all, and it names the
      // command with no values -- a placeholder is not a word the builder admits
      // (`command.ts`).
      const under = one_.under.actions
      expect(under.map((action) => action.kind), one_.name).toEqual(['copy-command'])
      const [copy] = under
      if (copy === undefined || copy.kind !== 'copy-command') throw new Error('not a copy-command')
      expect(copyCommandText(copy), one_.name).toBe(one_.below)
      // The sentence says why there is no button, and that the follow-up steps
      // stay saved for `fugaro apply` rather than running here. **It names no
      // flag**: a CLI below this floor has no `--no-apply` either (the kind's
      // comment above its floor), so a sentence telling the operator to add one
      // would hand out a word their CLI refuses -- the hazard
      // `NO_APPLY_CLI_FLOOR` keeps the console from handing a child.
      expect(copy.consequence, one_.name).toContain('fugaro apply')
      expect(copy.consequence, one_.name).not.toContain('--no-apply')
    }
  })

  it('draws that sentence beside the command, which is the below-floor state’s whole meaning', async () => {
    // Below the floor the sentence is the only thing that tells the operator why
    // there is no Add button and what their CLI still needs, so it is drawn with
    // the command rather than left in the descriptor (`shell.ts`'s copy arm).
    for (const one_ of CASES) {
      const { root } = await drawSection(one_.under, one_.group)
      const action = one_.under.actions[0]
      if (action === undefined || action.kind !== 'copy-command') throw new Error('not a copy-command')
      const box = one(root, MANAGE_COPY_BOX_CLASS)
      const copy = one(root, MANAGE_COPY_CLASS)
      expect(copy.textContent, one_.name).toBe(one_.below)
      expect(one(root, MANAGE_COPY_CONSEQUENCE_CLASS).textContent, one_.name).toBe(action.consequence)
      // The command and its sentence are one control: the button is inside the
      // box, so the reason travels with what it is about.
      expect(box.nodes.map((child) => child.textContent), one_.name).toEqual([
        one_.below,
        action.consequence,
      ])
      // And the write itself is not offered at all -- no control, no form.
      expect(all(root, MANAGE_WRITE_CLASS), one_.name).toHaveLength(0)
    }
  })
})

// ---------------------------------------------------------------------------
// The role: RB4's sentence on the control, and no form behind it
// ---------------------------------------------------------------------------

describe('the role a Manage write asks for', () => {
  it('draws the action disabled with the reason for a role that may not use it', async () => {
    // The plan's own red step: while the action is enabled for every role, this
    // case fails -- the shell asked no gate, and the control is a button that
    // opens a form.
    const viewer = gateFrom({ version: 1, role: 'viewer', scope: 'project', repos: [] })
    for (const one_ of CASES) {
      const { section, root } = await drawSection(one_.kind, one_.group, viewer.can)
      const control = one(root, MANAGE_WRITE_CLASS)
      expect(control.getAttribute('aria-disabled'), one_.name).toBe('true')
      // The tooltip is the control's own label plus RB4's clause: the role the
      // action needs, and the role the caller holds. The release's sentence is
      // **not** what a role refusal says, because this build does perform the
      // write for a role that may use it.
      expect(control.getAttribute('title'), one_.name).toBe(
        `${one_.label} needs the admin role at project scope and your role is viewer at project scope`,
      )
      // Nothing behind it: no form is drawn, the press opens nothing, and the
      // only read the section made is the kind's own.
      expect(all(root, MANAGE_WRITE_FORM_CLASS), one_.name).toHaveLength(0)
      control.fire('click')
      await settled()
      expect(all(root, MANAGE_WRITE_FORM_CLASS), one_.name).toHaveLength(0)
      expect(section.operations, one_.name).toEqual([one_.kind.source])
    }
  })

  it('draws the live control for a project admin, and a refusal names the scope it is held at', async () => {
    const admin = gateFrom({ version: 1, role: 'admin', scope: 'project', repos: [] })
    const { root } = await drawSection(CASES[0].kind, CASES[0].group, admin.can)
    const control = one(root, MANAGE_WRITE_CLASS)
    expect(control.getAttribute('aria-disabled')).toBeNull()
    expect(control.textContent).toBe(MODELS_ADD_LABEL)
    control.fire('click')
    expect(all(root, MANAGE_WRITE_FORM_CLASS)).toHaveLength(1)

    // An admin at **every repository** holds no project-level action
    // (`EffectiveAtProject`, F19), and the sentence says where their role is:
    // a control an admin expects to be able to use has to explain itself.
    const elsewhere = gateFrom({ version: 1, role: 'admin', scope: 'all', repos: [] })
    const second = await drawSection(CASES[0].kind, CASES[0].group, elsewhere.can)
    expect(one(second.root, MANAGE_WRITE_CLASS).getAttribute('title')).toBe(
      `${MODELS_ADD_LABEL} needs the admin role at project scope and your role is admin at every repository`,
    )
  })

  it('draws every write as one a role that may use it would see with no role read at all', async () => {
    // `gating.ts`'s "no answer is not a refusal": a caller that read no role
    // hands `gateFrom(null).can` -- the value this case's helper defaults to,
    // and the only way a caller can say it read nothing now that the member is
    // required -- so the control is drawn the way it always was, and the CLI's
    // own refusal is what the operator meets after the click.
    const { root } = await drawSection(CASES[0].kind, CASES[0].group)
    const control = one(root, MANAGE_WRITE_CLASS)
    expect(control.getAttribute('aria-disabled')).toBeNull()
    expect(control.presses).toBe(true)
  })
})

// ---------------------------------------------------------------------------
// The form: the values, the plan, and the CLI's sentence when it refuses
// ---------------------------------------------------------------------------

/** formOf opens the write's form and answers its boxes and its two buttons, in
 * the order they were drawn. */
function formOf(root: ManageElement): {
  readonly labels: readonly string[]
  readonly boxes: readonly FakeManage[]
  readonly submit: FakeManage
  readonly back: FakeManage
  readonly status: FakeManage
} {
  const form = one(root, MANAGE_WRITE_FORM_CLASS)
  const boxes = all(form, MANAGE_WRITE_INPUT_CLASS)
  const labels = all(form, MANAGE_WRITE_FIELD_CLASS).map((line) => line.textContent ?? '')
  const buttons = all(form, MANAGE_WRITE_CLASS)
  const backs = all(form, MANAGE_WRITE_CANCEL_CLASS)
  const submit = buttons[0]
  const back = backs[0]
  if (submit === undefined || back === undefined) throw new Error('the form drew no buttons')
  return { labels, boxes, submit, back, status: one(form, MANAGE_REFUSAL_CLASS) }
}

describe('the form a write action opens', () => {
  it('labels one box per field the action declared', async () => {
    for (const one_ of CASES) {
      const { root } = await drawSection(one_.kind, one_.group)
      one(root, MANAGE_WRITE_CLASS).fire('click')
      const form = formOf(root)
      const fields = writeAction(one_.kind).fields
      expect(form.labels, one_.name).toEqual(fields.map((field) => field.label))
      // One value box per field, and every one of them a text box: the console
      // sends words, and main holds each to its own grammar (`ipc.ts`).
      expect(form.boxes, one_.name).toHaveLength(fields.length)
      for (const box of form.boxes) expect(box.getAttribute('type'), one_.name).toBe('text')
      // The form is inside the actions line, under the control's own box, and
      // the way out is the same word the window's Cancel carries.
      expect(all(root, MANAGE_ACTIONS_CLASS), one_.name).toHaveLength(1)
      expect(form.back.textContent, one_.name).toBe('Cancel')
    }
  })

  it('submits the typed values to the kind’s plan operation, and no empty optional one', async () => {
    const { section, root } = await drawSection(CASES[0].kind, CASES[0].group)
    one(root, MANAGE_WRITE_CLASS).fire('click')
    const form = formOf(root)
    // Both required boxes empty: the submit is disabled, and a press of it asks
    // the console for nothing at all.
    expect(form.submit.disabled).toBe(true)
    form.submit.fire('click')
    await settled()
    expect(section.operations).toEqual(['models.list'])
    // A required box holding nothing but spaces is still empty for the button:
    // `check` trims, because a value a grammar would refuse is not a value this
    // form should send (`ipc.ts` holds the same words to their own grammars).
    form.boxes[0]!.value = '   '
    form.boxes[0]!.fire('input')
    expect(form.submit.disabled).toBe(true)
    form.boxes[0]!.value = 'deepseek-v4.1-flash'
    form.boxes[0]!.fire('input')
    form.boxes[1]!.value = 'gateway'
    form.boxes[1]!.fire('input')
    expect(form.submit.disabled).toBe(false)
    form.boxes[3]!.value = '0.28'
    form.boxes[3]!.fire('input')
    form.submit.fire('click')
    await settled()
    expect(section.calls[1]).toEqual({
      operation: 'models.add.plan',
      // Only what was typed: the empty optional box sends **no member**, which
      // is a different message from an empty one (`types.ts`'s `WriteField`).
      args: { id: 'deepseek-v4.1-flash', route: 'gateway', priceOut: '0.28' },
    })
  })

  it('draws the CLI’s own sentence when the plan refuses, and leaves the values in place', async () => {
    const { section, root } = await drawSection(CASES[1].kind, CASES[1].group)
    one(root, MANAGE_WRITE_CLASS).fire('click')
    const form = formOf(root)
    form.boxes[0]!.value = 'ci-runner'
    form.boxes[0]!.fire('input')
    form.boxes[1]!.value = 'owner/repo:images/ci.Dockerfile'
    form.boxes[1]!.fire('input')
    form.submit.fire('click')
    await settled()
    expect(section.operations).toEqual(['image.catalog.ls', 'image.add.plan'])
    section.refuse(1, new Error('a window is already waiting for an answer'))
    await settled()
    expect(form.status.textContent).toBe('a window is already waiting for an answer')
    // The values stay typed and the buttons are usable again: the operator's
    // next step after a refusal is another press, not retyping.
    expect(form.boxes[0]!.value).toBe('ci-runner')
    expect(form.submit.disabled).toBe(false)
    // And Cancel puts the control back exactly as it was.
    form.back.fire('click')
    expect(all(root, MANAGE_WRITE_FORM_CLASS)).toHaveLength(0)
    expect(one(root, MANAGE_WRITE_CLASS).textContent).toBe(IMAGE_ADD_LABEL)
  })

  it('clears the sentence an earlier press drew when a later plan lands', async () => {
    // The sequence the review found: one press is refused (`a window is already
    // waiting for an answer`, main's sentence for the press behind a window), the
    // operator presses again, and **this** press opens the window. The refusal
    // that no longer holds must not stand beside it -- main has a window waiting
    // for an answer while the box would say the plan was refused.
    const { section, root } = await drawSection(CASES[0].kind, CASES[0].group)
    one(root, MANAGE_WRITE_CLASS).fire('click')
    const form = formOf(root)
    form.boxes[0]!.value = 'deepseek-v4.1-flash'
    form.boxes[0]!.fire('input')
    form.boxes[1]!.value = 'gateway'
    form.boxes[1]!.fire('input')
    form.submit.fire('click')
    await settled()
    section.refuse(1, new Error('a window is already waiting for an answer'))
    await settled()
    expect(form.status.textContent).toBe('a window is already waiting for an answer')
    form.submit.fire('click')
    await settled()
    section.answer(2, modelsWindow())
    await settled()
    expect(form.status.textContent).toBe('')
    expect(section.calls[2]).toEqual({
      operation: 'models.add.plan',
      args: { id: 'deepseek-v4.1-flash', route: 'gateway' },
    })
  })

  it('draws the console’s own sentence when the refusal carried none', async () => {
    const { section, root } = await drawSection(CASES[0].kind, CASES[0].group)
    one(root, MANAGE_WRITE_CLASS).fire('click')
    const form = formOf(root)
    form.boxes[0]!.value = 'x'
    form.boxes[0]!.fire('input')
    form.boxes[1]!.value = 'gateway'
    form.boxes[1]!.fire('input')
    form.submit.fire('click')
    await settled()
    section.refuse(1, new Error(''))
    await settled()
    expect(form.status.textContent).toBe('no plan arrived for the write `models.add.plan`')
  })
})

// ---------------------------------------------------------------------------
// The two windows: exactly the rows main's own builder sent
// ---------------------------------------------------------------------------

describe('the models write’s window', () => {
  it('draws the plan document main parsed, row by row and in order', async () => {
    const { at, document, answers } = await drawWindow(modelsWindow())
    expect(at('confirm-title').textContent).toBe(TITLE_MODELS_ADD)
    // Every row, in the window's own order, against the view's own members: the
    // whole set is asserted, so a row the drawing invented and a member it
    // dropped both fail here.
    expect(rowsOf(document)).toEqual([
      { label: 'Project', values: [PROJECT] },
      { label: 'Model', values: ['deepseek-v4.1-flash'] },
      { label: 'Route', values: ['gateway'] },
      { label: 'Config file', values: ['fugaro.yaml'] },
      // The document's own layer word, drawn as it arrived: `local`, the one
      // layer this write ever lands in (contract section 6.33).
      { label: 'Written at', values: ['local'] },
      // The console's own words for a boolean: a document sent `true` and a
      // window that drew it would be one an operator reads a value out of.
      { label: 'Would change the file', values: [YES_WORD] },
      { label: 'Route entry', values: ['deepseek-v4.1-flash'] },
      // The list is set and does not name the model, so the write appends it:
      // the row is the addition, not the entry on its own (contract section
      // 6.33's `allowed_models.entry`).
      { label: 'Allowed models', values: [`${ALLOWED_ADDED_WORD} deepseek-v4.1-flash`] },
      // No `Price entry` row: the catalog prices this model, so the write lands
      // no `model_prices` entry and the source row below says where the rates
      // came from instead.
      { label: 'Price $/M (in / out)', values: ['0.14 USD per million tokens / 0.28 USD per million tokens'] },
      { label: 'Price source', values: ['catalog'] },
      { label: 'Steps this saves', values: ['policy: saved'] },
      { label: 'SHA-256 of the answers', values: [DIGEST] },
      { label: 'Command', values: [...MODELS_ARGV] },
    ])
    // The write's own button, out of the tab order: this window writes, and a
    // keystroke is not a deliberate answer (rule 11).
    const actions = at('confirm-actions')
    const confirm = actions.button(ADD_MODEL_LABEL)
    expect(confirm.tabIndex).toBe(-1)
    expect(confirm.textContent).toBe(ADD_MODEL_LABEL)
    // **A widening's window**: the tick is drawn beside it, Confirm starts
    // disabled, and the tick's words are main's own constant.
    const tick = actions.children.find((child) => child.getAttribute('id') === TICK_ID)
    if (tick === undefined) throw new Error('the models window drew no tick')
    expect(tick.getAttribute('type')).toBe('checkbox')
    expect(confirm.disabled).toBe(true)
    expect(actions.children.map((child) => child.textContent)).toContain(MODELS_ADD_TICK_LABEL)
    tick.checked = true
    await tick.fire('change')
    expect(confirm.disabled).toBe(false)
    // And the yes carries the tick, because this window drew one.
    await confirm.fire('click', 1)
    expect(answers).toEqual([{ answer: 'yes', ticked: true }])
  })

  it('draws the console’s own word for an allowed-models key the project has not set', async () => {
    // D786: an absent key stays absent, so the window says that rather than
    // drawing an empty list: every model a route opens is allowed in this
    // project, which is the opposite of what an empty list would mean.
    const plan = {
      ...(MODELS_PLAN as Record<string, unknown>),
      allowed_models: { entry: '', present: false },
    }
    const { document } = await drawWindow(modelsWindow(plan))
    const allowed = rowsOf(document).find((row) => row.label === 'Allowed models')
    expect(allowed?.values).toEqual([ALLOWED_ABSENT_WORD])
  })

  it('says the key is set and already names the model, and claims no addition', async () => {
    // The third state of the same two members: `present` with no `entry` is a
    // list that already holds the model, which is a fact about the policy the
    // window must not draw as an append (`internal/cli/models_add.go`'s
    // `modelsAddPlanAllowText`).
    const plan = {
      ...(MODELS_PLAN as Record<string, unknown>),
      allowed_models: { entry: '', present: true },
    }
    const { document } = await drawWindow(modelsWindow(plan))
    const allowed = rowsOf(document).find((row) => row.label === 'Allowed models')
    expect(allowed?.values).toEqual([ALLOWED_NAMES_WORD])
  })

  it('draws the price entry the write lands when the rates are the operator’s own', async () => {
    // `given` is the one source whose `model_prices` entry the write lands: a
    // rate the catalog or the file already carries needs none (contract section
    // 6.33).
    const plan = {
      ...(MODELS_PLAN as Record<string, unknown>),
      prices: {
        key: 'model_prices.deepseek-v4.1-flash',
        input_per_m: 0.14,
        output_per_m: 0.28,
        source: 'given',
      },
    }
    const { document } = await drawWindow(modelsWindow(plan))
    const rows = rowsOf(document)
    expect(rows.find((row) => row.label === 'Price entry')?.values).toEqual([
      'model_prices.deepseek-v4.1-flash',
    ])
    expect(rows.find((row) => row.label === 'Price source')?.values).toEqual(['given'])
  })

  it('draws the pattern that already covers the model, and never both halves of the claim', async () => {
    const plan = {
      ...(MODELS_PLAN as Record<string, unknown>),
      claim: { entry: '', claimed_by: 'deepseek-*' },
    }
    const { document } = await drawWindow(modelsWindow(plan))
    const labels = rowsOf(document).map((row) => row.label)
    expect(labels).toContain('Already covered by')
    expect(labels).not.toContain('Route entry')
  })
})

describe('the image write’s window', () => {
  it('draws the plan document main parsed, row by row, and no tick', async () => {
    const { at, document, answers } = await drawWindow(imageWindow())
    expect(at('confirm-title').textContent).toBe(TITLE_IMAGE_ADD)
    expect(rowsOf(document)).toEqual([
      { label: 'Project', values: [PROJECT] },
      { label: 'Entry', values: ['ci-runner'] },
      { label: 'Built from', values: ['dockerfile owner/repo:images/ci.Dockerfile'] },
      // Each tag is drawn on its own through its class before the console joins
      // them: the join is this window's punctuation.
      { label: 'Tags', values: ['node, redis'] },
      { label: 'Description', values: ['the repository’s own image'] },
      { label: 'Default entry', values: [NO_WORD] },
      { label: 'Steps this saves', values: ['catalog-build: saved'] },
      { label: 'SHA-256 of the answers', values: [DIGEST] },
      { label: 'Command', values: [...IMAGE_ARGV] },
    ])
    // A narrowing's window: two buttons and no checkbox at all, so this yes
    // carries no tick member (`confirm.ts`'s argument for the optional member).
    const actions = at('confirm-actions')
    expect(actions.children.map((child) => child.tag)).toEqual(['button', 'button'])
    expect(document.created).not.toContain('input')
    const confirm = actions.button(ADD_ENTRY_LABEL)
    expect(confirm.disabled).toBe(false)
    expect(confirm.tabIndex).toBe(-1)
    // D1109: the default button is the view's own member, and this view's is
    // Confirm -- so the focus the window takes is this button. (Cancel was
    // focused before the subject arrived, which is the window's own rule for a
    // `fields()` that never answers; the fake records the focus calls and not
    // the blur that a real focus() would do.)
    expect(confirm.focused).toBe(true)
    await confirm.fire('click', 1)
    expect(answers).toEqual([{ answer: 'yes' }])
  })

  it('draws the console’s own word for an entry with no tags and no description', async () => {
    const plan = {
      ...(IMAGE_PLAN as Record<string, unknown>),
      entry: {
        name: 'ci-runner',
        tags: [],
        description: '',
        default: false,
        source: 'dockerfile owner/repo:images/ci.Dockerfile',
      },
    }
    const { document } = await drawWindow(imageWindow(plan))
    const rows = rowsOf(document)
    expect(rows.find((row) => row.label === 'Tags')?.values).toEqual([NONE])
    expect(rows.find((row) => row.label === 'Description')?.values).toEqual([NONE])
  })

  it('draws an unsaved step as the console’s own words', async () => {
    const plan = {
      ...(IMAGE_PLAN as Record<string, unknown>),
      steps: [
        { name: 'catalog-build', saved: true },
        { name: 'repository', saved: false },
      ],
    }
    const { document } = await drawWindow(imageWindow(plan))
    expect(rowsOf(document).find((row) => row.label === 'Steps this saves')?.values).toEqual([
      `catalog-build: ${SAVED_WORD}`,
      'repository: not saved',
    ])
  })
})

// ---------------------------------------------------------------------------
// The sanitizer's door, on the two windows' own values
// ---------------------------------------------------------------------------

describe('the values the two windows draw', () => {
  it('draws a document string as a text node, and never as markup', async () => {
    const hostile = '<img src=x onerror=alert(1)>'
    const plan = { ...(MODELS_PLAN as Record<string, unknown>), model: hostile }
    const { document } = await drawWindow(modelsWindow(plan))
    // The value is drawn as itself -- a `label` admits it -- and the document
    // built no element of a markup kind: the fake has no markup slot at all, so
    // a view that reached for one would have thrown.
    expect(rowsOf(document).find((row) => row.label === 'Model')?.values).toEqual([hostile])
    expect(document.created.filter((tag) => tag === 'img' || tag === 'a' || tag === 'b')).toEqual([])
    // The whole of what this window built: the rows, the words of the command
    // and the digest as code, the tick's box and its words, and the two buttons.
    expect([...new Set(document.created)].sort()).toEqual([
      'button',
      'code',
      'dd',
      'dt',
      'input',
      'label',
    ])
  })

  it('draws the console’s own mark, and never the value, for a string a class refuses', async () => {
    // A `label` is one line and 200 characters (`sanitize.ts`): a document
    // string with a break in it is refused, and the refusal reaches the screen
    // as the console's own mark rather than as the value.
    const plan = { ...(MODELS_PLAN as Record<string, unknown>), model: 'deepseek-v4.1\nflash' }
    const { document } = await drawWindow(modelsWindow(plan))
    const rows = rowsOf(document)
    expect(rows.find((row) => row.label === 'Model')?.values).toEqual([CONFIRM_UNDRAWABLE])
    // The refused value is nowhere on the window, and the rest of the document
    // is drawn as it was: one bad member is one mark, not a refused window.
    const drawn = rows.flatMap((row) => row.values).join('\n')
    expect(drawn).not.toContain('deepseek-v4.1\nflash')
    expect(rows.find((row) => row.label === 'Route')?.values).toEqual(['gateway'])
  })

  it('carries the class of the column it is a row of, not a class of its own', async () => {
    // The window draws the same values the Manage table draws, and under the
    // same classes: a price entry's key is a `branch` and a config file is a
    // `path`. A value a class rewrites is rewritten here too -- the console
    // re-applies the display layer's sanitization rather than trusting the
    // transport (rule 7).
    const plan = {
      ...(MODELS_PLAN as Record<string, unknown>),
      path: 'fugaro.yaml',
      // The `given` source, because it is the one whose key the window draws as
      // an entry: the price row is here at all only for that source.
      prices: {
        key: 'model_prices.deepseek-v4.1-flash',
        input_per_m: 0.14,
        output_per_m: 0.28,
        source: 'given',
      },
    }
    const { document } = await drawWindow(modelsWindow(plan))
    const rows = rowsOf(document)
    // Both are drawn as themselves: a path with no traversal and a branch-shaped
    // key are what the classes admit. What the case pins is that they are drawn
    // through a class at all -- a window that printed a raw document string
    // would put a value the class would have rewritten on the screen.
    expect(rows.find((row) => row.label === 'Config file')?.values).toEqual(['fugaro.yaml'])
    expect(rows.find((row) => row.label === 'Price entry')?.values).toEqual([
      'model_prices.deepseek-v4.1-flash',
    ])
  })
})
