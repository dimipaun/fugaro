// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The settings edit: the flow behind `settings.edit` (the settings-edit plan's
// C3, design section 5.1 rule 12, D1103, D1104, D1105, D1112, D1114, D1115).
//
// **What this module is.** One edit of one key, in four steps and this order:
//
//   1. **the read** -- the console's own `settings --json` (`settings.read`,
//      C1), run again here rather than remembered. The row's `target_layer` is
//      the layer the edit is written to (D1105) and the row itself is what the
//      window is drawn from, so both come from a document read for **this** edit
//      rather than from a table the tab may have fetched minutes ago. Nothing
//      else in this process holds a settings document, and nothing needs to: a
//      pencil press is one read, one dry run and one write.
//   2. **the dry run** -- `settings set|unset KEY --layer <L>
//      [--repo <directory>] --dry-run --json`,
//      which writes nothing, backs nothing up and classifies the edit (D1106,
//      P3). Its document is what the window draws, and it is refused unless it
//      answers about **this** edit: another key, another layer, a document this
//      console cannot read or one carrying a refusal stops the flow here, with
//      no window and no write (`SETTINGS_EDIT_DISAGREES`,
//      `SETTINGS_EDIT_REFUSED`).
//   3. **the window** -- `confirm.ts`'s, one implementation for both questions
//      (D1115), showing C2's `settingsView` of that dry run.
//   4. **the write** -- `settings set|unset KEY --layer <L>
//      [--repo <directory>] [--no-apply]
//      -- <VALUE>`, reached only from a yes on that window's own top frame. The
//      write's one word is `--no-apply`: *save this and run none of its
//      follow-up steps* (docs/composite-commands, D1565, D1572), so what the
//      change owes is run by `fugaro apply` -- the pending-steps read the
//      console draws -- and never from behind this window. **It is passed only
//      to a CLI that registers it** (`NO_APPLY_CLI_FLOOR`,
//      `src/shared/version.ts`): no release is yet, so the write going out today
//      carries no phase word and the CLI runs what the change owes itself,
//      exactly as every installed CLI does. It answers no
//      question the CLI asks (D1107, C3): `applySettingsEdit` refuses only
//      inside a coding agent's session, never a plain piped one
//      (`internal/cli/settings_write.go`), so there is no word to answer with
//      and `CHILD_ENV_KEYS` gains no key for this task. The classification
//      decides the console's own gate -- the tick a widening needs (D1109) --
//      and not a word of this argv, which is the same for both write phases.
//
// **Why the read is run here and D1114's layer route is (i).** The layer is the
// one argument of this operation a renderer must not be able to name: a
// renderer that can name a layer can name a layer the CLI did not offer, and
// the whole of D1105 is that the layer written is the layer the read named. So
// the request carries `op`, `key` and `value` and nothing else
// (`src/main/ipc.ts`), the layer is read out of a document **this process**
// fetched and validated, and it reaches the argv through `argvFor`'s own
// `settingsEdit` context rather than through `args`.
//
// **What is not retried, and what is not guessed.** A refusal is the CLI's own
// sentence, shown verbatim and nothing else: the write's own compare-and-swap
// refuses an edit built on bytes that moved, and that refusal exists precisely
// because another writer won -- the way to ask again is the row's own pencil,
// which runs the read before it, and retrying it silently would be this console
// deciding an edit it was told not to make (D1112). The one thing this module
// adds to the CLI's words is the shape refusal of the read document
// (`SETTINGS_READ_SHAPE_BECAUSE`), for C2's reason: a member that is not the type the contract gives it is a claim the
// CLI never made, and the window is refused whole rather than drawn from a
// coerced value.
//
// **The window shows what is spawned.** The write's argv is built **once**,
// before the window opens, and is both what the window draws and what the child
// is started from: it is handed back to the source with the phase, and the
// source refuses to spawn a plan whose argv differs from the one shown
// (`SETTINGS_EDIT_MOVED`). That is what makes "an installation re-chosen while
// the window is open cancels the edit" true even for the sliver of time before
// the window exists, and it is why the shown argv travels with the request
// rather than being rebuilt at the write.

import { validateArguments } from './ipc'
import type { ValidatedArguments } from './ipc'
import type { LaunchConfirmer, SettingsConfirmView } from './launch'
import type { SettingsEntryDefault, SettingsEntryMeta, SettingsEntryRow, SettingsEntryView, SettingsView } from './settingsview'
import { settingsDryRun, settingsEntryView, settingsView } from './settingsview'
import type { SettingsEditPhase } from './spawn'
import type { SettingsEditSpawn } from '../source/source'

/**
 * SETTINGS_READ_DOC_VERSION is the settings read's document version this module
 * reads (`settingsDocVersion`, `internal/cli/settings_render.go`, and
 * `schemas/settings.schema.json`). It is the renderer's `SETTINGS_DOC_VERSION`
 * seen from the main process, and it is a copy for the reason the confirm
 * window's channel names are: the two halves of the app cannot import each
 * other's modules, so the number is held in both places and a test reads the
 * contract for it.
 */
export const SETTINGS_READ_DOC_VERSION = 1

/** SETTINGS_READ_VERSION_WORD is this module's sentence for a read this build
 * does not know, with the version the CLI printed interpolated. */
export const SETTINGS_READ_VERSION_WORD = 'settings --json is version '

/** SETTINGS_READ_SHAPE_BECAUSE is the console's own sentence -- no command
 * prints it -- for a read whose member this module draws is not the type the
 * contract gives it. The window is refused whole rather than opened on a
 * coerced member, which is the owner's ruling of 2026-10-30 that C1's row set
 * and C2's view already apply. */
export const SETTINGS_READ_SHAPE_BECAUSE =
  'settings --json carried a member this console does not read: it is not the type the contract gives it, so no window is drawn for it'

/** SETTINGS_EDIT_CANCELLED is the console's own sentence for an edit the
 * operator did not confirm: the window was answered no, or closed, or the
 * installation moved while it was open. It mirrors `DECLINED_CANCELLED` a screen
 * over, and it never says the CLI refused anything. */
export const SETTINGS_EDIT_CANCELLED = 'the edit was not confirmed: nothing was written'

/** SETTINGS_EDIT_BUSY is the refusal for a second edit of the same project while
 * one is in flight. The two would be two windows for one installation, and the
 * second window's yes would be a second write of a document the first one is
 * already rewriting. */
export const SETTINGS_EDIT_BUSY =
  'an edit of this project’s settings is already open: answer that window before starting a second'

/** SETTINGS_EDIT_NO_ROW is the refusal for a key the project's settings do not
 * carry any more. The pencil was drawn from a table, and the table was read
 * before this edit's own read: a key that moved between the two is a re-read
 * rather than an invented row. */
export const SETTINGS_EDIT_NO_ROW = 'this project’s settings do not carry this key: read the settings again'

/** SETTINGS_EDIT_NOT_WRITABLE is the refusal for a row the read says the console
 * may not write -- an owned key (D1102), or one whose `target_layer` is empty.
 * It is a re-read's refusal for `SETTINGS_EDIT_NO_ROW`'s reason. */
export const SETTINGS_EDIT_NOT_WRITABLE = 'this console does not write this key: read the settings again'

/** SETTINGS_EDIT_DISAGREES is the refusal for a dry run that answered about
 * **another edit**: its `key` is not the one the operator's pencil named, or its
 * `layer` is not the layer the read's own row gave. The window's key is the
 * document's and the write's `--layer` is the row's, so a document that
 * disagrees on either would be shown as this edit and written as another one;
 * it is refused before the window exists, and nothing is spawned but the dry run
 * that answered it. It is a re-read's refusal for `SETTINGS_EDIT_NO_ROW`'s
 * reason: the operator's way back is to read the settings again. */
export const SETTINGS_EDIT_DISAGREES =
  'the dry run answered for another edit: nothing was written; read the settings again'

/** SETTINGS_EDIT_REFUSED is the refusal for a dry run whose **document** carries
 * a refusal. The CLI prints that member empty in every document it prints
 * (`settingsWriteDoc`, `internal/cli/settings_set.go`): a refusal is its own
 * sentence, with a non-zero exit and no document at all, which is the state
 * above (`dry.refusal`). A zero-exit document that carries one is therefore a
 * state this CLI does not produce, and text this console cannot act on -- it is
 * refused rather than read as a classification to write over. */
export const SETTINGS_EDIT_REFUSED = 'the dry run reported a refusal: nothing was written'

/** SETTINGS_EDIT_SHAPE is the refusal for a request that is not a subcommand, a
 * key and an optional value. The IPC schema (`settings.edit`) already closes
 * those three, so this is the total form of a state the surface cannot produce:
 * it is here so that a request this module cannot read is refused rather than
 * defaulted. */
export const SETTINGS_EDIT_SHAPE = 'an edit is a subcommand, a key and a value, and nothing else'

/** SETTINGS_EDIT_UNBUILT is what a settings edit meets before `main` has built
 * the confirmation window and this flow over it. It is `DECLINED_UNBUILT`'s
 * sentence for the other question, and it is a state of this process rather
 * than an answer an operator is expected to read: the surface is registered only
 * from `onDecided`, which only `lifecycle.start()` and `choose` reach, both from
 * inside `main` and after this flow exists. The default writes nothing, which is
 * the whole of what a fail-closed default has to do. */
export const SETTINGS_EDIT_UNBUILT = 'the settings edit is not built yet'

/**
 * SETTINGS_CONFIRM_PREFIX and settingsConfirmID are the settings window's own
 * identity, and they live here rather than in `confirm.ts` so that this module
 * -- which the window's module imports -- stays free of Electron: nothing in
 * the flow above reads a window, and `cancelFor`'s project is the whole of what
 * the two modules share.
 *
 * A project name cannot collide with a launch's identity, which is a run id
 * (`\d{8}-\d{6}-[0-9a-f]{4}`), so one map of open windows holds both questions
 * and a close reaches the right one.
 */
export const SETTINGS_CONFIRM_PREFIX = 'settings:'

/** settingsConfirmID is the identity of the window one project's edit opens. */
export function settingsConfirmID(project: string): string {
  return SETTINGS_CONFIRM_PREFIX + project
}

/** SettingsEditOutcome is what `settings.edit` answers with, and it is a
 * sentence rather than a document: the CLI's own `wrote %s (backup at %s)` on a
 * write that happened, and a refusal's sentence -- the CLI's verbatim, or one of
 * this module's own -- on one that did not. The screen draws it and offers the
 * re-read; `ok` is what says which of the two it is drawing. */
export interface SettingsEditOutcome {
  readonly ok: boolean
  readonly sentence: string
}

/**
 * SettingsEditDeps is everything the flow is built from. Every one of them is a
 * seam the unit test drives without a child, a window or a filesystem.
 */
export interface SettingsEditDeps {
  /** readSettings runs the console's own `settings --json` and answers the
   * document it printed. It is the source's own `settings.read` operation, so
   * this module reads the same argv the Project tab's section does. A refusal
   * arrives as a thrown `SourceRefusal` whose sentence is the CLI's
   * (`src/source/source.ts`), and it is shown as it is. */
  readonly readSettings: () => Promise<{ readonly document: unknown }>
  /** argvForEdit answers the exact argv one phase spawns, which is what the
   * window draws (the write's) and what the spawn is held to. */
  readonly argvForEdit: (
    args: ValidatedArguments,
    phase: SettingsEditPhase,
    layer: string,
  ) => Promise<readonly string[]>
  /** spawnEdit runs one phase and answers what came of it. `shown` is the argv
   * the operator was shown for that phase: the source refuses to spawn a plan
   * that differs from it, so this is the second half of "what the window shows
   * is what is spawned". */
  readonly spawnEdit: (
    args: ValidatedArguments,
    phase: SettingsEditPhase,
    layer: string,
    shown: readonly string[],
  ) => Promise<SettingsEditSpawn>
  /** confirmer is the one confirmation window, shared with the launch (D1115).
   * A confirmer that throws is a window that did not open: the edit declines
   * rather than writing on an answer nobody gave. */
  readonly confirmer: LaunchConfirmer
}

/** SettingsEditRunner is what the entry point wires the operation to. */
export interface SettingsEditRunner {
  /** edit runs the four steps for one request, in order, and answers the
   * sentence the screen draws. */
  edit(args: ValidatedArguments): Promise<SettingsEditOutcome>
  /** cancelFor is D1115's "an installation change cancels the window", reached
   * from the two places the entry point learns that an installation moved: a
   * choose and a removal from the rail. It takes a project because that is what
   * those callers know -- `settingsConfirmID` is where a project becomes the
   * window's identity.
   *
   * It closes the window **and** marks the edit, because the two cover different
   * moments: the close is what the operator sees when the window is already
   * open, and the mark is what a flow still reading its layers sees when its
   * window does not exist yet. A project with no edit in flight is a no-op, so a
   * cancel that arrives between two edits cannot decline the next one. */
  cancelFor(project: string): void
}

/** SettingsRequest is the three members `settings.edit`'s schema closes, read
 * out of the validated arguments: the operation, the key and, for a `set`, the
 * operator's value. The layer is deliberately not here -- it is the read's own
 * `target_layer` (D1105). */
interface SettingsRequest {
  readonly op: 'set' | 'unset'
  readonly key: string
  readonly value: string | null
}

/** request reads the validated arguments. It answers null for a shape the
 * surface cannot produce (`SETTINGS_EDIT_SHAPE`), which is the total form of a
 * state `ipc.ts` has already refused. */
function request(args: ValidatedArguments): SettingsRequest | null {
  const op = args['op']
  const key = args['key']
  if (op !== 'set' && op !== 'unset') return null
  if (typeof key !== 'string' || key === '') return null
  const value = args['value']
  if (value !== undefined && typeof value !== 'string') return null
  if (op === 'set' && value === undefined) return null
  if (op === 'unset' && value !== undefined) return null
  return { op, key, value: value ?? null }
}

/** SettingsRead is the read document decoded down to the two facts this flow
 * uses: the project the layers belong to, and the rows a key may be found in. A
 * row is `SettingsEntryRow` -- C2's four members plus the write's own answer
 * (D1102), the command the read names for a key this path does not write
 * (D1178), and the metadata and built-in default the entry form draws (SU1,
 * D2902, R6) -- so the one row the flow finds is the whole of what both halves
 * of the window are built from. */
interface SettingsRead {
  readonly project: string
  readonly rows: readonly SettingsEntryRow[]
}

/** SettingsReadResult is the decoded read, or the sentence saying why there is
 * none. */
type SettingsReadResult = { readonly ok: true; readonly read: SettingsRead } | { readonly ok: false; readonly because: string }

/**
 * decodeRead reads the `settings --json` document strictly: unknown keys are
 * ignored and every member this flow draws must be the type the contract gives
 * it, or the whole document is refused -- the owner's ruling of 2026-10-30,
 * applied here because a `writable` spelled `"true"` would offer a pencil the
 * CLI did not offer and a `target_layer` that is not a string would become a
 * layer word this console passed to a write.
 *
 * A `sensitive: true` row is **dropped rather than refused**: D1111 says such a
 * row is never shown and never editable, the CLI prints no `value` for one at
 * all, and a row that is not in this list is a key `SETTINGS_EDIT_NO_ROW`
 * answers for. A row whose members are the wrong type is a different thing --
 * a claim the contract does not license -- and that refuses the document. A row
 * the document says is not writable is kept, so that a key that exists and may
 * not be written is refused with `SETTINGS_EDIT_NOT_WRITABLE` rather than as a
 * key that is not there.
 */
export function decodeRead(document: unknown): SettingsReadResult {
  if (document === null || typeof document !== 'object' || Array.isArray(document)) {
    return { ok: false, because: SETTINGS_READ_SHAPE_BECAUSE }
  }
  const raw = document as Record<string, unknown>
  if (raw['version'] !== SETTINGS_READ_DOC_VERSION) {
    return {
      ok: false,
      because: `${SETTINGS_READ_VERSION_WORD}${String(raw['version'])} and this console reads version ${SETTINGS_READ_DOC_VERSION}`,
    }
  }
  const project = raw['project']
  const settings = raw['settings']
  if (typeof project !== 'string' || project === '' || !Array.isArray(settings)) {
    return { ok: false, because: SETTINGS_READ_SHAPE_BECAUSE }
  }
  const rows: SettingsEntryRow[] = []
  for (const entry of settings) {
    if (entry === null || typeof entry !== 'object' || Array.isArray(entry)) {
      return { ok: false, because: SETTINGS_READ_SHAPE_BECAUSE }
    }
    const row = entry as Record<string, unknown>
    const key = row['key']
    const wins = row['wins']
    const targetLayer = row['target_layer']
    const writable = row['writable']
    const sensitive = row['sensitive']
    const value = row['value']
    const owner = row['owner']
    if (
      typeof key !== 'string' ||
      typeof wins !== 'string' ||
      typeof targetLayer !== 'string' ||
      typeof writable !== 'boolean' ||
      (sensitive !== undefined && typeof sensitive !== 'boolean') ||
      (value !== undefined && typeof value !== 'string') ||
      (owner !== undefined && typeof owner !== 'string')
    ) {
      return { ok: false, because: SETTINGS_READ_SHAPE_BECAUSE }
    }
    const meta = metaAt(row)
    const fallback = defaultAt(row)
    if (meta === null || fallback === null) return { ok: false, because: SETTINGS_READ_SHAPE_BECAUSE }
    if (sensitive === true) continue
    rows.push({
      key,
      value: typeof value === 'string' ? value : null,
      wins,
      targetLayer,
      writable,
      owner: typeof owner === 'string' ? owner : '',
      meta,
      default: fallback,
    })
  }
  return { ok: true, read: { project, rows } }
}

/** metaAt reads the eight metadata members (SM-E) off one row, and answers null
 * for a member present with a type the contract does not give it. An **absent**
 * member is the fallback rather than a refusal -- `label` empty means the raw
 * key and `type` empty means a text control (D2908) -- which is why every member
 * is read as its empty spelling and only a wrongly-typed one refuses the whole
 * document, exactly as `key` and `writable` do above. */
function metaAt(row: Record<string, unknown>): SettingsEntryMeta | null {
  const text = (name: string): string | null => {
    const raw = row[name]
    if (raw === undefined) return ''
    return typeof raw === 'string' ? raw : null
  }
  const label = text('label')
  const description = text('description')
  const group = text('group')
  const type = text('type')
  const min = text('min')
  const max = text('max')
  const format = text('format')
  if (
    label === null ||
    description === null ||
    group === null ||
    type === null ||
    min === null ||
    max === null ||
    format === null
  ) {
    return null
  }
  const values = row['enum']
  let options: readonly string[] = []
  if (values !== undefined) {
    if (!Array.isArray(values) || values.some((value) => typeof value !== 'string')) return null
    options = values as readonly string[]
  }
  return { label, description, group, type, enum: options, min, max, format }
}

/** defaultAt reads the built-in default R6 draws: the value and the constant
 * holding it, or the sentence saying what unset means (`default.value` and
 * `default.unset`, both optional and both `omitempty` on the wire). */
function defaultAt(row: Record<string, unknown>): SettingsEntryDefault | null {
  const raw = row['default']
  if (raw === undefined) return { value: '', unset: '' }
  if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) return null
  const members = raw as Record<string, unknown>
  const value = members['value']
  const unset = members['unset']
  if ((value !== undefined && typeof value !== 'string') || (unset !== undefined && typeof unset !== 'string')) {
    return null
  }
  return {
    value: typeof value === 'string' ? value : '',
    unset: typeof unset === 'string' ? unset : '',
  }
}

/** sentenceOf is the CLI's own sentence for a refused read, and this module's
 * own only when there is none to show: a `SourceRefusal` carries the CLI's
 * words (`src/source/source.ts`), and anything else is this console failing to
 * read rather than the CLI refusing. */
function sentenceOf(error: unknown): string {
  if (error !== null && typeof error === 'object' && 'sentence' in error) {
    const sentence = (error as { readonly sentence?: unknown }).sentence
    if (typeof sentence === 'string' && sentence !== '') return sentence
  }
  return error instanceof Error && error.message !== ''
    ? error.message
    : 'the settings edit failed: nothing was written'
}

/** refused is the one spelling of an outcome that wrote nothing. */
function refused(sentence: string): SettingsEditOutcome {
  return Object.freeze({ ok: false, sentence })
}

/** One open window is the function its answer resolves. It lives until the
 * window is gone, and its absence is what says an answer has already arrived --
 * a second one is dropped rather than delivered to a window that came after it. */
type Pending = (yes: boolean) => void

/**
 * createSettingsEdit builds the flow over its dependencies.
 *
 * The three maps are the whole of its state, and each answers one question:
 * `pending` is the windows that are open (keyed by the window's own identity, so
 * a cancel reaches the right one), `editing` is the projects whose edit is in
 * flight (which is what makes `SETTINGS_EDIT_BUSY` true of a second pencil
 * press and a cancel meaningful), and `cancelled` is the projects whose
 * installation moved while their edit was being built -- the case a window
 * cannot see, because there is no window yet.
 */
export function createSettingsEdit(deps: SettingsEditDeps): SettingsEditRunner {
  const pending = new Map<string, Pending>()
  const editing = new Set<string>()
  const cancelled = new Set<string>()

  /** openWindow asks the operator, once. The promise it answers is resolved by
   * the window's own main-side path (`confirm.ts`), and by the confirmer
   * throwing -- which is a window that did not open, so the edit declines
   * rather than waiting for an answer nothing will give.
   *
   * **There is no deadline here, and one would be a decision this module cannot
   * make.** Every way a window can go away settles this promise -- an answer, a
   * close, the `close(id)` of an installation that moved -- so what a wait means
   * is that the operator still has the window open and is reading it, which is
   * exactly what the flow is for: a timer would cancel a window the person had
   * not finished with, and how long a person may read a settings change for is
   * the owner's answer rather than this file's. */
  function openWindow(id: string, subject: SettingsConfirmView): Promise<boolean> {
    return new Promise<boolean>((resolve) => {
      pending.set(id, resolve)
      try {
        deps.confirmer.open(subject, (yes) => {
          if (!pending.has(id)) return
          pending.delete(id)
          resolve(yes)
        })
      } catch {
        pending.delete(id)
        resolve(false)
      }
    })
  }

  async function edit(args: ValidatedArguments): Promise<SettingsEditOutcome> {
    const asked = request(args)
    if (asked === null) return refused(SETTINGS_EDIT_SHAPE)

    let read: SettingsReadResult
    try {
      read = decodeRead((await deps.readSettings()).document)
    } catch (error) {
      return refused(sentenceOf(error))
    }
    if (!read.ok) return refused(read.because)

    const project = read.read.project
    if (editing.has(project)) return refused(SETTINGS_EDIT_BUSY)
    editing.add(project)
    try {
      const row = read.read.rows.find((candidate) => candidate.key === asked.key)
      if (row === undefined) return refused(SETTINGS_EDIT_NO_ROW)
      if (!row.writable || row.targetLayer === '') return refused(SETTINGS_EDIT_NOT_WRITABLE)
      const layer = row.targetLayer

      const dryArgv = await deps.argvForEdit(args, 'dry-run', layer)
      const dry = await deps.spawnEdit(args, 'dry-run', layer, dryArgv)
      if (dry.refusal !== null) return refused(dry.refusal)

      // The dry run is the document the window and the write are built over, so
      // it has to be a document **about this edit** before either exists. Three
      // ways it can fail to be, and each is a refusal with no window and no
      // write:
      //   - it is not a document this console reads at all, refused with the
      //     same sentence `settingsView` would refuse it with;
      //   - its `key` is not the one the pencil named, or its `layer` is not
      //     the layer the read's own row gave. Both members reach a window: the
      //     key is drawn from the document and the layer written is the row's,
      //     so a disagreement is a window showing one edit and a write of
      //     another (`SETTINGS_EDIT_DISAGREES`);
      //   - it carries a `refusal` (`SETTINGS_EDIT_REFUSED`), which is a state
      //     this CLI does not print.
      const dryRead = settingsDryRun(dry.document)
      if (!dryRead.ok) return refused(dryRead.because)
      if (dryRead.dryRun.key !== asked.key || dryRead.dryRun.layer !== layer) {
        return refused(SETTINGS_EDIT_DISAGREES)
      }
      if (dryRead.dryRun.refusal !== '') return refused(SETTINGS_EDIT_REFUSED)

      // Which of the two write phases this edit is -- the tick's question, and
      // not the argv's: the two write phases spell one argv (D1107) -- is the dry
      // run's classification, and the classification is a member of the window
      // C2 builds. So the document is read again here, for `widening` alone:
      // `settingsView` is a pure function of it, so this call cannot answer
      // differently from the checks above, and `classified` is thrown away as
      // soon as the argv it decides exists -- the window below is the one the
      // operator sees, carrying the argv the child is started from. The window's
      // argv is built here, before the window exists, because it is both what is
      // drawn and what is spawned: the terminator and the value are part of what
      // the operator reads before answering (D1104), and the source refuses a
      // plan that differs from it.
      const classified = settingsView({ dryRun: dry.document, row, argv: [], because: null })
      if (!classified.ok) return refused(classified.because)
      const writeArgv = await deps.argvForEdit(args, phaseOf(classified.view), layer)
      const view = settingsView({ dryRun: dry.document, row, argv: writeArgv, because: null })
      if (!view.ok) return refused(view.because)

      if (cancelled.has(project)) return refused(SETTINGS_EDIT_CANCELLED)
      const id = settingsConfirmID(project)
      const subject: SettingsConfirmView = Object.freeze({
        ...view.view,
        kind: 'settings' as const,
        project,
      })
      const yes = await openWindow(id, subject)
      // The mark is read again **after** the answer, and that is the point of
      // it: an installation that moved while the window was open must not write
      // even though a window existed and the operator said yes. The window's own
      // close is the operator's half of the same rule.
      if (!yes || cancelled.has(project)) return refused(SETTINGS_EDIT_CANCELLED)

      const write = await deps.spawnEdit(args, phaseOf(view.view), layer, writeArgv)
      if (write.refusal !== null) return refused(write.refusal)
      return Object.freeze({ ok: true, sentence: write.text })
    } catch (error) {
      // The steps above await a **source** rather than a pure function:
      // `argvForEdit` builds a plan over an installation that can have moved,
      // and `spawnEdit` runs a child that can fail to start, time out or be
      // killed with the console (`runToCompletion` rejects for all three). The
      // read has its own refusal above, and these two are the same one: `edit`
      // answers an outcome -- that is the whole of its contract with the
      // operation, which draws a sentence -- so a throw is the refusal a
      // returned one is, in the source's own words when it has any
      // (`sentenceOf`), and never a rejected promise for a caller that has
      // nothing to do with one.
      return refused(sentenceOf(error))
    } finally {
      editing.delete(project)
      cancelled.delete(project)
    }
  }

  return Object.freeze({
    edit,
    cancelFor(project: string): void {
      // A project with no edit in flight has no window of this flow's to close
      // -- the window exists only while `editing` holds the project -- so this
      // returns before the confirmer rather than leaving the no-op to it, as
      // `launch.ts`'s own `cancelFor` does for a project with nothing in flight.
      if (!editing.has(project)) return
      cancelled.add(project)
      deps.confirmer.close(settingsConfirmID(project))
    },
  })
}

/** phaseOf is which of the two write phases one view's edit is: the dry run
 * classified it (D1106), and the classification decides which phase the child is
 * spawned as -- the tick's question, since both spell the same argv. It is a
 * function of C2's own `widening` and never of this module's reading of a
 * limit. */
export function phaseOf(view: SettingsView): SettingsEditPhase {
  return view.widening ? 'widening' : 'narrowing'
}

/**
 * DECLINING_SETTINGS_EDITOR is the fail-closed default: the runner this process
 * starts with, before `main` has built anything, over no source and no window.
 * Every edit is declined with `SETTINGS_EDIT_UNBUILT` and every cancel is
 * nothing, so there is no state for an operation that arrives too early to
 * touch. `index.ts` replaces it once, in `main`, exactly as it replaces the
 * launcher over `DECLINING_CONFIRMER`.
 */
export const DECLINING_SETTINGS_EDITOR: SettingsEditRunner = Object.freeze({
  edit: (): Promise<SettingsEditOutcome> => Promise.resolve(refused(SETTINGS_EDIT_UNBUILT)),
  cancelFor: (): void => undefined,
})

/**
 * SettingsEditorAnswer is what the one modal's window answers `settings.editor.open`
 * with, and it is the flow's own type rather than the window's envelope
 * (`src/preload/confirm.ts`'s `ConfirmAnswer`): that one is the renderer's
 * message off the channel, and this one is what the main-process window delivers
 * once it has checked it. There are exactly three, and each is a **phase**'s
 * answer -- the entry form's value or reset, the diff's confirmation, and the
 * operator's way out of either (D2902, rule 12).
 *
 * `submitted` carries the value the operator typed, or `null` for "Reset to
 * default": the reset writes no value at all (`settings unset`, A4), so the two
 * are told apart here rather than by a spelling of the empty string. `confirmed`
 * carries no tick: a widening's tick is the **window's** gate, checked in the
 * main process before a yes is delivered (`CONFIRM_TICK_NEEDED`), so a flow can
 * never see a confirmation that skipped it.
 */
export type SettingsEditorAnswer =
  | { readonly kind: 'declined' }
  | { readonly kind: 'submitted'; readonly value: string | null }
  | { readonly kind: 'confirmed' }

/**
 * SettingsEditorConfirmer is the seam `createConfirmWindow` fills for this flow,
 * and it is the **two-phase** half of D1115's one window: the launch's half is
 * `LaunchConfirmer.open`, whose one phase is the whole question, and this one
 * asks, then shows the diff, in the same window.
 *
 *   - `ask` opens the window on the entry form and delivers the form's answer.
 *     It is `open`'s shape with the entry's own subject, and it is called
 *     **once** per edit.
 *   - `settle` puts the dry run's diff into the window the entry form is still
 *     open in, and answers whether it took it: false means the window is gone
 *     (the operator declined, closed it, or an installation moved under it) and
 *     the flow cancels the edit rather than writing. It is called once, after
 *     the dry run, and only for the window `ask` opened.
 *   - `close` is the seam's second half, unchanged: an installation removed or
 *     re-chosen while the window is open closes it as a cancel.
 *
 * The flow holds no window of its own and reads no clock: what a window is, when
 * a yes is answerable and what a document may answer are the window's own
 * (rules 11 and 12), and this interface is what keeps that true.
 */
export interface SettingsEditorConfirmer {
  ask(subject: SettingsEntryView, decide: (answer: SettingsEditorAnswer) => void): void
  settle(id: string, subject: SettingsConfirmView): boolean
  close(id: string): void
}

/** SettingsEditorDeps is `SettingsEditDeps` with the two-phase confirmer: the
 * read, the argv builder and the two spawns are the same seams, because both
 * flows drive the same CLI over the same source. */
export type SettingsEditorDeps = Omit<SettingsEditDeps, 'confirmer'> & {
  readonly confirmer: SettingsEditorConfirmer
}

/** SettingsEditorRunner is what the entry point wires `settings.editor.open`
 * to. `open` takes the operation's validated arguments -- `{key}` and nothing
 * else (C-OPS) -- and answers the same outcome `settings.edit` does, so the
 * screen that asked for the window has one shape to draw. */
export interface SettingsEditorRunner {
  open(args: ValidatedArguments): Promise<SettingsEditOutcome>
  cancelFor(project: string): void
}

/** Answers is one edit's queue of window answers, and it exists because a
 * window answers **twice**: the entry form's submit and the diff's confirmation
 * are delivered by the same `decide`, and the two can arrive in either order
 * relative to the flow's own awaits -- a `close(id)` from an installation that
 * moved delivers a decline while the flow is still running the dry run. The
 * queue holds one until the flow asks for it, so no answer is dropped and none
 * is delivered twice. */
interface Answers {
  next(): Promise<SettingsEditorAnswer>
  deliver(answer: SettingsEditorAnswer): void
}

/** answerQueue builds one edit's queue. */
function answerQueue(): Answers {
  const queued: SettingsEditorAnswer[] = []
  const waiting: ((answer: SettingsEditorAnswer) => void)[] = []
  return {
    next(): Promise<SettingsEditorAnswer> {
      return new Promise<SettingsEditorAnswer>((resolve) => {
        const held = queued.shift()
        if (held === undefined) waiting.push(resolve)
        else resolve(held)
      })
    },
    deliver(answer: SettingsEditorAnswer): void {
      const waiter = waiting.shift()
      if (waiter === undefined) queued.push(answer)
      else waiter(answer)
    },
  }
}

/**
 * createSettingsEditor builds the flow behind `settings.editor.open` (SU1,
 * D2902): **one modal does the entry, the dry run's diff and the confirmation**,
 * where `createSettingsEdit` above does the dry run first and shows the
 * operator the result.
 *
 * The order is the whole of the difference between the two, and it is what R5
 * asks for: the operator types a value into the window (validated on the client
 * from the metadata, the entry form's own rule), OK runs the dry run, and the
 * **same** window then shows what that edit would do -- the old and new values,
 * the layer, the classification and the exact argv a second OK spawns. The dry
 * run is still the authority at OK: a value the client let through and the CLI
 * refuses is the CLI's own sentence, and no window and no write follow it.
 *
 * `settings.edit` and its flow are left standing beside this one: the two are
 * the same four steps in a different order, an installed renderer still calls
 * the older operation, and the write floor both are held to is one floor.
 */
export function createSettingsEditor(deps: SettingsEditorDeps): SettingsEditorRunner {
  const editing = new Set<string>()
  const cancelled = new Set<string>()

  /** give up is the one path out of a window this flow holds: it closes the
   * window (which the window itself reads as the operator's decline, resolving
   * any answer still pending on it) and answers the sentence. */
  function giveUp(id: string, sentence: string): SettingsEditOutcome {
    deps.confirmer.close(id)
    return refused(sentence)
  }

  async function open(args: ValidatedArguments): Promise<SettingsEditOutcome> {
    const asked = args['key']
    if (typeof asked !== 'string' || asked === '') return refused(SETTINGS_EDIT_SHAPE)

    let read: SettingsReadResult
    try {
      read = decodeRead((await deps.readSettings()).document)
    } catch (error) {
      return refused(sentenceOf(error))
    }
    if (!read.ok) return refused(read.because)

    const project = read.read.project
    if (editing.has(project)) return refused(SETTINGS_EDIT_BUSY)
    editing.add(project)
    let id: string | null = null
    try {
      const row = read.read.rows.find((candidate) => candidate.key === asked)
      if (row === undefined) return refused(SETTINGS_EDIT_NO_ROW)
      const layer = row.targetLayer
      id = settingsConfirmID(project)
      const window = id
      const answers = answerQueue()
      try {
        deps.confirmer.ask(settingsEntryView({ project, row }), (answer) => {
          answers.deliver(answer)
        })
      } catch {
        // A confirmer that throws is a window that did not open: the edit
        // declines rather than waiting for an answer nothing will give.
        return refused(SETTINGS_EDIT_CANCELLED)
      }

      const first = await answers.next()
      if (first.kind !== 'submitted') return refused(SETTINGS_EDIT_CANCELLED)
      // The window draws no control for a key this path does not write, and its
      // only answer is Cancel, so a value for one is a document that did not
      // follow the view it was shown. It is refused here rather than at the
      // dry run: a key with no layer to write has no argv at all.
      if (!row.writable || layer === '') return giveUp(window, SETTINGS_EDIT_NOT_WRITABLE)

      // The arguments object the argv builder and both spawns are given. The
      // operation's payload carries `{key}` and nothing else (C-OPS), because the
      // value is the operator's and it is typed into the window rather than sent
      // by the renderer that asked for it: the two words a `settings set` takes
      // are composed here, from the answer the window's own top frame delivered.
      const fields: Record<string, string> = { op: first.value === null ? 'unset' : 'set', key: asked }
      if (first.value !== null) fields['value'] = first.value
      // **The composed request goes through the operation's own gate, exactly as
      // a renderer's payload did when `settings.edit` was the only door.** The
      // value is checked rather than assumed, because nothing else on this path
      // checks it: the renderer's own payload is `{key}` and nothing else (C-OPS)
      // so `validateArguments` had no value to look at when the request came in,
      // the entry form's rule (`settingsEntryError`) is a **pre-check for the
      // button** and knows a key's type and bounds rather than what is hidden in
      // a string, and what `spawn.ts` holds a value to is only that it is
      // non-empty, carries no NUL and is not absurdly long. So a value carrying a
      // bidi override, a zero-width space or a control character would otherwise
      // be spawned, shown in the diff as one string and **written on the second
      // click** as another. It is refused here, before the first child: nothing is
      // spawned, the form closes, and the sentence is the gate's own -- the one
      // the older flow answered with, word for word.
      const decision = validateArguments('settings.edit', fields)
      if (!decision.ok) return giveUp(window, decision.reason)
      const request: ValidatedArguments = decision.args
      const dryArgv = await deps.argvForEdit(request, 'dry-run', layer)
      const dry = await deps.spawnEdit(request, 'dry-run', layer, dryArgv)
      if (dry.refusal !== null) return giveUp(window, dry.refusal)

      // The dry run is the document the diff and the write are built over, so it
      // has to be a document **about this edit** before either is shown: the
      // three ways it can fail to be are the three `settings.edit` refuses for
      // the same reasons (`SETTINGS_EDIT_DISAGREES`, `SETTINGS_EDIT_REFUSED`),
      // and each is answered with the window closed and nothing written.
      const dryRead = settingsDryRun(dry.document)
      if (!dryRead.ok) return giveUp(window, dryRead.because)
      if (dryRead.dryRun.key !== asked || dryRead.dryRun.layer !== layer) {
        return giveUp(window, SETTINGS_EDIT_DISAGREES)
      }
      if (dryRead.dryRun.refusal !== '') return giveUp(window, SETTINGS_EDIT_REFUSED)

      const classified = settingsView({ dryRun: dry.document, row, argv: [], because: null })
      if (!classified.ok) return giveUp(window, classified.because)
      const writeArgv = await deps.argvForEdit(request, phaseOf(classified.view), layer)
      const diff = settingsView({ dryRun: dry.document, row, argv: writeArgv, because: null })
      if (!diff.ok) return giveUp(window, diff.because)

      if (cancelled.has(project)) return refused(SETTINGS_EDIT_CANCELLED)
      const subject: SettingsConfirmView = Object.freeze({
        ...diff.view,
        kind: 'settings' as const,
        project,
      })
      if (!deps.confirmer.settle(window, subject)) return refused(SETTINGS_EDIT_CANCELLED)

      const second = await answers.next()
      // The mark is read again after the answer, as `settings.edit` reads it:
      // an installation that moved while the window was open must not write even
      // though the operator said yes.
      if (second.kind !== 'confirmed' || cancelled.has(project)) return refused(SETTINGS_EDIT_CANCELLED)

      const wrote = await deps.spawnEdit(request, phaseOf(diff.view), layer, writeArgv)
      if (wrote.refusal !== null) return refused(wrote.refusal)
      return Object.freeze({ ok: true, sentence: wrote.text })
    } catch (error) {
      return refused(sentenceOf(error))
    } finally {
      // Every way out of the steps above closes this flow's window, whatever it
      // is still showing: a window left open would hold a pending answer for the
      // life of the process, and the operator would be looking at a form whose
      // edit was already over.
      if (id !== null) deps.confirmer.close(id)
      editing.delete(project)
      cancelled.delete(project)
    }
  }

  return Object.freeze({
    open,
    cancelFor(project: string): void {
      if (!editing.has(project)) return
      cancelled.add(project)
      deps.confirmer.close(settingsConfirmID(project))
    },
  })
}

/**
 * DECLINING_SETTINGS_EDITOR_FLOW is the fail-closed default for
 * `settings.editor.open`, `DECLINING_SETTINGS_EDITOR`'s twin: the runner this
 * process starts with, before `main` has built a window. Every open is declined
 * with `SETTINGS_EDIT_UNBUILT` and every cancel is nothing.
 */
export const DECLINING_SETTINGS_EDITOR_FLOW: SettingsEditorRunner = Object.freeze({
  open: (): Promise<SettingsEditOutcome> => Promise.resolve(refused(SETTINGS_EDIT_UNBUILT)),
  cancelFor: (): void => undefined,
})
