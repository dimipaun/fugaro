// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The models write's window content (the console-improvements plan's MW1,
// D2903), and the sibling of `setupview.ts`: the view the one confirmation
// window draws and the one rule the tick decides, kept in a module of its own so
// that `launch.ts` can carry the view's type without importing the flow that
// builds it -- the same seam the wizard's view is on.
//
// **The view carries the CLI's own document and nothing the console invented.**
// The model, the route, the two rates and the steps come out of the dry run's
// document (`schemas/models-add-plan.schema.json`, MD1), read member by member
// with a version check; the digest and the exact argv are main's, taken from the
// bytes and the plan the window was opened over. A document this module cannot
// vouch for is a refused window rather than a coerced one.

/** MODELS_ADD_TICK_LABEL is the widening's tick, and it is the same sentence
 * the settings write's own widening carries (D1101, D1109): what the write
 * changes is what every run of the project may name and spend on, which is the
 * class of change a tick exists for. */
export const MODELS_ADD_TICK_LABEL = 'I understand this changes what every run may name and spend on'

/** MODELS_ADD_PLAN_VERSION is the only plan document this build reads. */
export const MODELS_ADD_PLAN_VERSION = 1

/** MODELS_ADD_DOCUMENT_VERSION_REFUSAL is the sentence a window is refused
 * with when the child printed a document of another version -- the screens'
 * header rule: a version this build does not know is a refusal and not a
 * best-effort read. */
export const MODELS_ADD_DOCUMENT_VERSION_REFUSAL =
  'the models plan is a document version this console does not know'

/** MODELS_ADD_DOCUMENT_SHAPE_REFUSAL is the sentence for a plan the child
 * printed that is not this module's document at all. */
export const MODELS_ADD_DOCUMENT_SHAPE_REFUSAL = 'the models plan is not a plan document'

/**
 * ModelsAddView is the whole of what the window is shown for one write. Every
 * member is either a string or a number the CLI printed, or the console's own
 * shape for the answer (`defaultButton`, `tick`, `confirmNeedsTick`), plus the
 * two facts that are main's: `digest`, the plan document's own, and `argv`, the
 * exact command line the confirmed write spawns -- `argvFor`'s answer, never a
 * second spelling built here.
 *
 * **`widening` is always true and is a member anyway.** The models write
 * appends the model to a route's list, writes a `model_prices` entry and appends
 * to `budget.allowed_models` when that key is set, so it changes what every run
 * of the project may name and spend on -- there is no narrowing form of it, and
 * the member is here because the window draws the widening's own frame from the
 * document rather than from a constant of the drawing.
 */
export interface ModelsAddView {
  readonly kind: 'models-add'
  /** the model the write claims, as the document spells it. */
  readonly model: string
  /** the route it is claimed on, as the document spells it. */
  readonly route: string
  /** the config file the write lands in (`path`), and the layer it decides. */
  readonly path: string
  readonly layer: string
  /** whether the file would change at all: false is the CLI's own
   * classification of an add the file already holds, not an error. */
  readonly changed: boolean
  /** the `providers.<route>.models` entry the change appends, or the pattern
   * that already covers the model -- the two are never both set. */
  readonly claimEntry: string
  readonly claimedBy: string
  /** the `budget.allowed_models` entry, and whether that key is set at all: an
   * absent list stays absent (D786), so an empty `allowEntry` with
   * `allowPresent` false is a project where every model a route opens is
   * allowed. */
  readonly allowEntry: string
  readonly allowPresent: boolean
  /** the price entry's key and its two rates, in US dollars per million
   * tokens, with the document's own word for where they came from. */
  readonly priceKey: string
  readonly priceIn: number
  readonly priceOut: number
  readonly priceSource: string
  /** the follow-up steps the change owes, each with the document's own state:
   * `saved` is what `--no-apply` makes them, which is the whole of what the
   * console's write does with them. */
  readonly steps: readonly ModelsAddStep[]
  /** the installation the write lands in, named so the window can say so. */
  readonly project: string
  /** the plan document's own digest, as the window was opened over it. */
  readonly digest: string
  /** the exact command line the confirmed write spawns. */
  readonly argv: readonly string[]
  readonly widening: true
  readonly defaultButton: 'cancel' | 'confirm'
  readonly tick: string
  readonly confirmNeedsTick: boolean
}

/** ModelsAddStep is one follow-up step of the document, by the CLI's own name
 * for it and the state it would be left in. */
export interface ModelsAddStep {
  readonly name: string
  readonly state: string
}

/** ModelsAddDocument is the plan document's members, read strictly: a document
 * whose version is not this build's, or whose shape is not this object, is a
 * refused window. */
export interface ModelsAddDocument {
  readonly model: string
  readonly route: string
  readonly path: string
  readonly layer: string
  readonly changed: boolean
  readonly claimEntry: string
  readonly claimedBy: string
  readonly allowEntry: string
  readonly allowPresent: boolean
  readonly priceKey: string
  readonly priceIn: number
  readonly priceOut: number
  readonly priceSource: string
  readonly steps: readonly ModelsAddStep[]
}

/** ModelsAddDocumentRead is the document as this module read it, or the reason
 * there is no window. */
export type ModelsAddDocumentRead =
  | { readonly ok: true; readonly document: ModelsAddDocument }
  | { readonly ok: false; readonly because: string }

/** recordAt is a member that is a JSON object, or null. */
function recordAt(raw: unknown): Record<string, unknown> | null {
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) return null
  return raw as Record<string, unknown>
}

/** textAt is a member that is a JSON string, or null. */
function textAt(raw: unknown, key: string): string | null {
  const record = recordAt(raw)
  if (record === null) return null
  const value = record[key]
  return typeof value === 'string' ? value : null
}

/** numberAt is a member that is a JSON number, or null. */
function numberAt(raw: unknown, key: string): number | null {
  const record = recordAt(raw)
  if (record === null) return null
  const value = record[key]
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

/** boolAt is a member that is a JSON boolean, or null. */
function boolAt(raw: unknown, key: string): boolean | null {
  const record = recordAt(raw)
  if (record === null) return null
  const value = record[key]
  return typeof value === 'boolean' ? value : null
}

/**
 * readModelsAddDocument reads the CLI's plan document (`--dry-run --json`),
 * member by member, and refuses anything it cannot vouch for: another version,
 * a missing member, or a member of the wrong type. **It is deliberately
 * strict**, because the window is the operator's only sight of what the write
 * will do -- a coerced document would draw a change nobody described.
 *
 * `refusal` is read for the one thing it can say: the CLI writes it empty in
 * every document it prints (a refusal is a sentence with a non-zero exit and no
 * document at all), so a document that carries one is a plan this console will
 * not show.
 */
export function readModelsAddDocument(raw: unknown): ModelsAddDocumentRead {
  const version = numberAt(raw, 'version')
  if (version === null) return { ok: false, because: MODELS_ADD_DOCUMENT_SHAPE_REFUSAL }
  if (version !== MODELS_ADD_PLAN_VERSION) {
    return { ok: false, because: MODELS_ADD_DOCUMENT_VERSION_REFUSAL }
  }
  const refusal = textAt(raw, 'refusal')
  if (refusal === null) return { ok: false, because: MODELS_ADD_DOCUMENT_SHAPE_REFUSAL }
  if (refusal !== '') return { ok: false, because: refusal }
  const model = textAt(raw, 'model')
  const route = textAt(raw, 'route')
  const path = textAt(raw, 'path')
  const layer = textAt(raw, 'layer')
  const changed = boolAt(raw, 'changed')
  // The three records the document nests (`claim`, `allowed_models`, `prices`),
  // read as records first and then member by member: the `entry` and `key` of a
  // record are **inside** it, so a reader that looked for `claim` as a string
  // would refuse every document the CLI prints.
  const claim = recordAt(recordAt(raw) === null ? null : (recordAt(raw) as Record<string, unknown>)['claim'])
  const allow = recordAt(
    recordAt(raw) === null ? null : (recordAt(raw) as Record<string, unknown>)['allowed_models'],
  )
  const prices = recordAt(recordAt(raw) === null ? null : (recordAt(raw) as Record<string, unknown>)['prices'])
  const stepsRaw = recordAt(raw) === null ? null : (recordAt(raw) as Record<string, unknown>)['steps']
  if (
    model === null ||
    route === null ||
    path === null ||
    layer === null ||
    changed === null ||
    claim === null ||
    allow === null ||
    prices === null ||
    !Array.isArray(stepsRaw)
  ) {
    return { ok: false, because: MODELS_ADD_DOCUMENT_SHAPE_REFUSAL }
  }
  const claimEntry = textAt(claim, 'entry')
  const claimedBy = textAt(claim, 'claimed_by')
  const allowEntry = textAt(allow, 'entry')
  const allowPresent = boolAt(allow, 'present')
  const priceKey = textAt(prices, 'key')
  const priceIn = numberAt(prices, 'input_per_m')
  const priceOut = numberAt(prices, 'output_per_m')
  const priceSource = textAt(prices, 'source')
  if (
    claimEntry === null ||
    claimedBy === null ||
    allowEntry === null ||
    allowPresent === null ||
    priceKey === null ||
    priceIn === null ||
    priceOut === null ||
    priceSource === null
  ) {
    return { ok: false, because: MODELS_ADD_DOCUMENT_SHAPE_REFUSAL }
  }
  const steps: ModelsAddStep[] = []
  for (const step of stepsRaw) {
    const name = textAt(step, 'name')
    const state = textAt(step, 'state')
    if (name === null || state === null) {
      return { ok: false, because: MODELS_ADD_DOCUMENT_SHAPE_REFUSAL }
    }
    steps.push({ name, state })
  }
  return {
    ok: true,
    document: {
      model,
      route,
      path,
      layer,
      changed,
      claimEntry,
      claimedBy,
      allowEntry,
      allowPresent,
      priceKey,
      priceIn,
      priceOut,
      priceSource,
      steps,
    },
  }
}

/** ModelsAddViewInputs is everything the window is built from. */
export interface ModelsAddViewInputs {
  /** the document **as it arrived**, not a decoded view. */
  readonly dryRun: unknown
  /** the installation the write lands in. */
  readonly project: string
  /** the plan document's own digest, as the window was opened over it. */
  readonly digest: string
  /** the exact command line the confirmed write spawns. */
  readonly argv: readonly string[]
}

/** ModelsAddViewRead is the built view, or the reason there is no window. */
export type ModelsAddViewRead =
  | { readonly ok: true; readonly view: ModelsAddView }
  | { readonly ok: false; readonly because: string }

/**
 * modelsAddView builds the window's view from the plan document. **A widening's
 * default button is `cancel`** (D1109): the write changes what every run may
 * name and spend on, so a stray double-click must not be what confirms it, and
 * the tick gates Confirm besides.
 */
export function modelsAddView(inputs: ModelsAddViewInputs): ModelsAddViewRead {
  const read = readModelsAddDocument(inputs.dryRun)
  if (!read.ok) return read
  const document = read.document
  return {
    ok: true,
    view: {
      kind: 'models-add',
      model: document.model,
      route: document.route,
      path: document.path,
      layer: document.layer,
      changed: document.changed,
      claimEntry: document.claimEntry,
      claimedBy: document.claimedBy,
      allowEntry: document.allowEntry,
      allowPresent: document.allowPresent,
      priceKey: document.priceKey,
      priceIn: document.priceIn,
      priceOut: document.priceOut,
      priceSource: document.priceSource,
      steps: document.steps,
      project: inputs.project,
      digest: inputs.digest,
      argv: inputs.argv,
      widening: true,
      defaultButton: 'cancel',
      tick: MODELS_ADD_TICK_LABEL,
      confirmNeedsTick: true,
    },
  }
}

/**
 * modelsAddConfirmAnswerable is the one rule the tick decides (D1109), and it is
 * here rather than in the drawing because it is a rule about the *answer*: the
 * models write is always a widening, so its Confirm is answerable only while the
 * tick is set -- `confirm.ts` refuses a yes without it, on the window's own
 * frame, which is the gate and not a disabled button.
 */
export function modelsAddConfirmAnswerable(view: ModelsAddView, ticked: boolean): boolean {
  return !view.confirmNeedsTick || ticked
}

/** MODELS_ADD_CONFIRM_PREFIX is the window's own identity for a models write:
 * one window per installation, the way a setup window is one per project
 * (`setupConfirmID`) and a settings window is one per installation. */
export const MODELS_ADD_CONFIRM_PREFIX = 'models-add:'

/** modelsAddConfirmID is the id `confirm.ts` files a models window under. */
export function modelsAddConfirmID(project: string): string {
  return MODELS_ADD_CONFIRM_PREFIX + project
}
