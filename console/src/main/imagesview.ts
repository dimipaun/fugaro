// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The image write's window content (the console-improvements plan's MW2,
// D2903), and the models write's sibling one command over (`modelsview.ts`): the
// view the one confirmation window draws, kept in a module of its own so that
// `launch.ts` can carry the view's type without importing the flow that builds
// it -- the same seam the wizard's view and the models write's are on.
//
// **The view carries the CLI's own document and nothing the console invented.**
// The entry -- its name, its tags, its description, whether it is the default and
// the one-line form of its definition -- and the steps the add owes come out of
// the dry run's document (`schemas/image-add-plan.schema.json`, MD2), read member
// by member with a version check; the digest and the exact argv are main's, taken
// from the bytes and the plan the window was opened over. A document this module
// cannot vouch for is a refused window rather than a coerced one.
//
// The four small readers below are `modelsview.ts`'s, written out again rather
// than imported: that module does not export them, and this task's files are the
// ones it was given. The extraction is a follow-up of its own -- both modules
// would import it -- and the two copies are held to the same answers by their
// tests.

/** IMAGE_ADD_PLAN_VERSION is the only plan document this build reads. */
export const IMAGE_ADD_PLAN_VERSION = 1

/** IMAGE_ADD_COMMAND is the word the document carries for itself (`command`,
 * MD2's `imageAddDoor`), and the value this module requires: the schema writes
 * the member so that a window drawing more than one add reads which command it
 * is holding from the document rather than from the call it made, and the
 * console draws the models and the images one. */
export const IMAGE_ADD_COMMAND = 'image add'

/** IMAGE_ADD_DOCUMENT_VERSION_REFUSAL is the sentence a window is refused with
 * when the child printed a document of another version -- the screens' header
 * rule: a version this build does not know is a refusal and not a best-effort
 * read. */
export const IMAGE_ADD_DOCUMENT_VERSION_REFUSAL =
  'the image plan is a document version this console does not know'

/** IMAGE_ADD_DOCUMENT_SHAPE_REFUSAL is the sentence for a plan the child printed
 * that is not this module's document at all. */
export const IMAGE_ADD_DOCUMENT_SHAPE_REFUSAL = 'the image plan is not a plan document'

/** IMAGE_ADD_DOCUMENT_COMMAND_REFUSAL is the sentence for a document of this
 * version that is the *other* add's: the two documents are one version apart in
 * nothing but their members, and the member the CLI writes for exactly this --
 * `command` -- is what tells them apart. */
export const IMAGE_ADD_DOCUMENT_COMMAND_REFUSAL = 'the plan is for another command'

/** IMAGE_ADD_DOCUMENT_PROJECT_REFUSAL is the sentence for a plan whose own
 * `project` is not the installation this console is acting on. The schema states
 * the member for this: a consumer compares it with the project it believes it is
 * acting on rather than assuming the two agree -- and an entry written into
 * another installation than the one the screen named is the whole class of
 * mistake the console's "main names the installation, never a renderer" rule
 * exists to prevent. */
export const IMAGE_ADD_DOCUMENT_PROJECT_REFUSAL =
  'the plan is for another installation than the one this console is acting on'

/**
 * ImageAddView is the whole of what the window is shown for one write. Every
 * member is either a value the CLI printed or the console's own shape for the
 * answer (`defaultButton`, `confirmNeedsTick`), plus the two facts that are
 * main's: `digest`, the plan document's own, and `argv`, the exact command line
 * the confirmed write spawns -- `argvFor`'s answer, never a second spelling built
 * here.
 *
 * **It carries no tick, and that is a decision rather than an omission.** The
 * write is not a widening: it raises no limit (nothing in the document is a
 * limit, and MD2's document has no `widening` member for a window to classify it
 * by), so its yes is one deliberate click, as the wizard's is -- `confirm.ts`
 * holds the tick to the subjects that really do raise one. `confirmNeedsTick` is
 * spelled `false` rather than left out so that the window reads the same member
 * on every view it draws, and `defaultButton` follows from it.
 */
export interface ImageAddView {
  readonly kind: 'image-add'
  /** the window's name for the screen: the app's, never the domain's. */
  readonly command: string
  /** the installation the entry lands in, as the document spells it. */
  readonly project: string
  /** the entry's name, the positional `image add` reads. */
  readonly name: string
  /** the tags the entry provides, as the document lists them -- always present
   * and possibly empty, which is an entry that covers no repository's needs. */
  readonly tags: readonly string[]
  /** what the entry is for, as a listing prints it -- empty when the caller gave
   * none, which is the same answer the listing gives. */
  readonly description: string
  /** whether the entry is the one a job whose repositories name no needs runs.
   * False for every write this console can make: `--default` is a definition flag
   * no row of the spawn table may emit, so the window can say so. */
  readonly entryDefault: boolean
  /** the entry's definition in the one-line form `image ls` prints it, which for
   * this surface is always `dockerfile owner/name:path` with `@branch` when the
   * entry names one. A consumer shows the line as it stands rather than parsing
   * it. */
  readonly source: string
  /** the steps the add owes, each with the document's own state: `saved` is what
   * `--no-apply` makes them, which is the whole of what the console's write does
   * with them. */
  readonly steps: readonly ImageAddStep[]
  /** the plan document's own digest, as the window was opened over it. */
  readonly digest: string
  /** the exact command line the confirmed write spawns. */
  readonly argv: readonly string[]
  readonly defaultButton: 'cancel' | 'confirm'
  readonly confirmNeedsTick: boolean
}

/** ImageAddStep is one follow-up step of the document, by the CLI's own name for
 * it and the state it would be left in. */
export interface ImageAddStep {
  readonly name: string
  readonly saved: boolean
}

/** ImageAddDocument is the plan document's members, read strictly: a document
 * whose version is not this build's, whose `command` is another one's, or whose
 * shape is not this object, is a refused window. */
export interface ImageAddDocument {
  readonly project: string
  readonly name: string
  readonly tags: readonly string[]
  readonly description: string
  readonly entryDefault: boolean
  readonly source: string
  readonly steps: readonly ImageAddStep[]
}

/** ImageAddDocumentRead is the document as this module read it, or the reason
 * there is no window. */
export type ImageAddDocumentRead =
  | { readonly ok: true; readonly document: ImageAddDocument }
  | { readonly ok: false; readonly because: string }

/** recordAt is a member that is a JSON object, or null. */
function recordAt(raw: unknown): Record<string, unknown> | null {
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) return null
  return raw as Record<string, unknown>
}

/** memberAt is a member of a JSON object, or undefined. */
function memberAt(raw: unknown, key: string): unknown {
  const record = recordAt(raw)
  return record === null ? undefined : record[key]
}

/** textAt is a member that is a JSON string, or null. */
function textAt(raw: unknown): string | null {
  return typeof raw === 'string' ? raw : null
}

/** boolAt is a member that is a JSON boolean, or null. */
function boolAt(raw: unknown): boolean | null {
  return typeof raw === 'boolean' ? raw : null
}

/** textListAt is a member that is a JSON array of strings, or null. An empty
 * array is a value rather than a missing one: an entry with no tags lists none,
 * and the document always carries the member. */
function textListAt(raw: unknown): string[] | null {
  if (!Array.isArray(raw)) return null
  const out: string[] = []
  for (const entry of raw) {
    if (typeof entry !== 'string') return null
    out.push(entry)
  }
  return out
}

/**
 * readImageAddDocument reads the CLI's plan document (`--dry-run --json`),
 * member by member, and refuses anything it cannot vouch for: another version,
 * another command's document, a missing member, or a member of the wrong type.
 * **It is deliberately strict**, because the window is the operator's only sight
 * of what the write will do -- a coerced document would draw a change nobody
 * described.
 *
 * **`project` is checked against the installation the console is acting on**, and
 * that is the one member this reader judges rather than reads: the schema asks a
 * consumer to compare it, and a plan for another installation is one whose
 * confirmed write would land somewhere the screen never named.
 */
export function readImageAddDocument(raw: unknown, project: string): ImageAddDocumentRead {
  const version = memberAt(raw, 'version')
  if (version === null || version === undefined) {
    return { ok: false, because: IMAGE_ADD_DOCUMENT_SHAPE_REFUSAL }
  }
  if (version !== IMAGE_ADD_PLAN_VERSION) {
    return { ok: false, because: IMAGE_ADD_DOCUMENT_VERSION_REFUSAL }
  }
  const command = textAt(memberAt(raw, 'command'))
  if (command === null) return { ok: false, because: IMAGE_ADD_DOCUMENT_SHAPE_REFUSAL }
  if (command !== IMAGE_ADD_COMMAND) {
    return { ok: false, because: IMAGE_ADD_DOCUMENT_COMMAND_REFUSAL }
  }
  const documentProject = textAt(memberAt(raw, 'project'))
  if (documentProject === null) return { ok: false, because: IMAGE_ADD_DOCUMENT_SHAPE_REFUSAL }
  if (documentProject !== project) {
    return { ok: false, because: IMAGE_ADD_DOCUMENT_PROJECT_REFUSAL }
  }
  const entry = memberAt(raw, 'entry')
  const name = textAt(memberAt(entry, 'name'))
  const tags = textListAt(memberAt(entry, 'tags'))
  const description = textAt(memberAt(entry, 'description'))
  const entryDefault = boolAt(memberAt(entry, 'default'))
  const source = textAt(memberAt(entry, 'source'))
  const stepsRaw = memberAt(raw, 'steps')
  if (
    name === null ||
    name === '' ||
    tags === null ||
    description === null ||
    entryDefault === null ||
    source === null ||
    source === '' ||
    !Array.isArray(stepsRaw) ||
    stepsRaw.length === 0
  ) {
    return { ok: false, because: IMAGE_ADD_DOCUMENT_SHAPE_REFUSAL }
  }
  const steps: ImageAddStep[] = []
  for (const step of stepsRaw) {
    const stepName = textAt(memberAt(step, 'name'))
    const saved = boolAt(memberAt(step, 'saved'))
    if (stepName === null || stepName === '' || saved === null) {
      return { ok: false, because: IMAGE_ADD_DOCUMENT_SHAPE_REFUSAL }
    }
    steps.push({ name: stepName, saved })
  }
  return {
    ok: true,
    document: {
      project: documentProject,
      name,
      tags,
      description,
      entryDefault,
      source,
      steps,
    },
  }
}

/** ImageAddViewInputs is everything the window is built from. */
export interface ImageAddViewInputs {
  /** the document **as it arrived**, not a decoded view. */
  readonly dryRun: unknown
  /** the installation the write lands in. */
  readonly project: string
  /** the plan document's own digest, as the window was opened over it. */
  readonly digest: string
  /** the exact command line the confirmed write spawns. */
  readonly argv: readonly string[]
}

/** ImageAddViewRead is the built view, or the reason there is no window. */
export type ImageAddViewRead =
  | { readonly ok: true; readonly view: ImageAddView }
  | { readonly ok: false; readonly because: string }

/**
 * imageAddView builds the window's view from the plan document. **Its default
 * button is `confirm` and it carries no tick**: the write raises no limit, so it
 * is a narrowing's window rather than a widening's (D1109) -- one deliberate
 * click, with Escape, Enter and the close box cancelling as they do for every
 * other subject.
 */
export function imageAddView(inputs: ImageAddViewInputs): ImageAddViewRead {
  const read = readImageAddDocument(inputs.dryRun, inputs.project)
  if (!read.ok) return read
  const document = read.document
  return {
    ok: true,
    view: {
      kind: 'image-add',
      command: IMAGE_ADD_COMMAND,
      project: document.project,
      name: document.name,
      tags: document.tags,
      description: document.description,
      entryDefault: document.entryDefault,
      source: document.source,
      steps: document.steps,
      digest: inputs.digest,
      argv: inputs.argv,
      defaultButton: 'confirm',
      confirmNeedsTick: false,
    },
  }
}

/** IMAGE_ADD_CONFIRM_PREFIX is the window's own identity for an image write: one
 * window per installation, the way a models window is one per installation
 * (`modelsAddConfirmID`) and a setup window is one per project. */
export const IMAGE_ADD_CONFIRM_PREFIX = 'image-add:'

/** imageAddConfirmID is the id `confirm.ts` files an image window under. */
export function imageAddConfirmID(project: string): string {
  return IMAGE_ADD_CONFIRM_PREFIX + project
}
