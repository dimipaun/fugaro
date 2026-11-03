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
//   2. **the dry run** -- `settings set|unset KEY --layer <L> --dry-run --json`,
//      which writes nothing, backs nothing up and classifies the edit (D1106,
//      P3). Its document is what the window draws, and it is refused unless it
//      answers about **this** edit: another key, another layer, a document this
//      console cannot read or one carrying a refusal stops the flow here, with
//      no window and no write (`SETTINGS_EDIT_DISAGREES`,
//      `SETTINGS_EDIT_REFUSED`).
//   3. **the window** -- `confirm.ts`'s, one implementation for both questions
//      (D1115), showing C2's `settingsView` of that dry run.
//   4. **the write** -- `settings set|unset KEY --layer <L> --yes -- <VALUE>`,
//      reached only from a yes on that window's own top frame. `--yes` is on the
//      write whatever the dry run classified the edit (D1107, C3): the CLI asks
//      the flag, and the console answers it with the word the CLI named rather
//      than with a hook of its own -- so `CHILD_ENV_KEYS` gains no key for this
//      task and the `--yes` is a word of the argv the window shows (rule 5's
//      count and rule 6's rows, same commit). The classification decides the
//      console's own gate -- the tick a widening needs (D1109) -- and not this
//      word: a narrowing spawns with no terminal either, and the CLI's ordinary
//      y/N has nobody to ask.
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
// sentence, shown verbatim with a **Read again** control and nothing else: the
// write's own compare-and-swap refuses an edit built on bytes that moved, and
// that refusal exists precisely because another writer won -- retrying it
// silently would be this console deciding an edit it was told not to make
// (D1112). The one thing this module adds to the CLI's words is the shape
// refusal of the read document (`SETTINGS_READ_SHAPE_BECAUSE`), for C2's
// reason: a member that is not the type the contract gives it is a claim the
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

import type { ValidatedArguments } from './ipc'
import type { LaunchConfirmer, SettingsConfirmView } from './launch'
import type { SettingsView, SettingsViewRow } from './settingsview'
import { settingsDryRun, settingsView } from './settingsview'
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

/** SettingsReadRow is one row of the read, with the two members this flow
 * decides on that C2's window does not draw: `writable` (the CLI's own answer to
 * "may this console write this key", D1102) and, through `targetLayer`, whether
 * the CLI named a layer at all. Both are checked here, and only a row that
 * passes both reaches the window. */
interface SettingsReadRow extends SettingsViewRow {
  readonly writable: boolean
}

/** SettingsRead is the read document decoded down to the two facts this flow
 * uses: the project the layers belong to, and the row one key is. */
interface SettingsRead {
  readonly project: string
  readonly rows: readonly SettingsReadRow[]
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
  const rows: SettingsReadRow[] = []
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
    if (
      typeof key !== 'string' ||
      typeof wins !== 'string' ||
      typeof targetLayer !== 'string' ||
      typeof writable !== 'boolean' ||
      (sensitive !== undefined && typeof sensitive !== 'boolean') ||
      (value !== undefined && typeof value !== 'string')
    ) {
      return { ok: false, because: SETTINGS_READ_SHAPE_BECAUSE }
    }
    if (sensitive === true) continue
    rows.push({
      key,
      value: typeof value === 'string' ? value : null,
      wins,
      targetLayer,
      writable,
    })
  }
  return { ok: true, read: { project, rows } }
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
   * rather than waiting for an answer nothing will give. */
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
      // not the argv's: both write phases carry `--yes` (D1107) -- is the dry
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
    } finally {
      editing.delete(project)
      cancelled.delete(project)
    }
  }

  return Object.freeze({
    edit,
    cancelFor(project: string): void {
      if (editing.has(project)) cancelled.add(project)
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
