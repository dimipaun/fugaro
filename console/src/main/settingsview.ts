// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The settings edit window's content (the settings-edit plan's C2; design
// section 5.1 rule 11's pattern, D1108, D1109, D1113).
//
// **What this module is.** The window the operator confirms an edit in is
// `confirm.ts`'s, one implementation for both questions (D1115, C3): this is
// what that window is **shown** for a settings edit, built from three things
// and nothing else -- the dry run's document (`settings set|unset --dry-run
// --json`, P3), the read's own row (`settings --json`, P2) and the argv the
// confirmed edit spawns. It is a pure function with no Electron in it, the way
// `launch.ts`'s `confirmView` is, so the whole of the window's content is
// testable under plain Node.
//
// **What it carries is decided elsewhere, and it is six fields.** D1108: the
// key, the old value, the new value, the layer written, the layer that wins,
// and one sentence present **only** when the edit will not change what runs
// (D1113). Plus, for a widening, the CLI's own `why`; and, for every edit, the
// exact argv field by field, so the window shows what is spawned rather than a
// summary of it. Nothing here computes a value the CLI printed: the two values
// are the dry run's, the two layers are the read's and the dry run's, and the
// argv is the caller's (`argvFor`'s answer, C3).
//
// **D1113's sentence is a comparison of two fields the CLI printed, and it has
// one known limit.** "The layer written is not the layer that wins" is drawn as
// "no run reads differently", which is exactly right when the layer written
// loses to the winner (a local edit under the project layer that wins, a
// project-layer edit under the repository layer the CLI reads that wins) and
// wrong when it outranks the winner -- a key no layer sets has `wins: default`,
// and a project-layer write to it does change what a run reads. Telling those
// two apart needs the layers' order, which is the CLI's and which the console
// keeps no copy of (D1102); P3's document carries no `shadowed` field, and the
// plan says the console uses one if it ever grows there instead of comparing.

/**
 * SETTINGS_VIEW_DOC_VERSION is the dry run's document version this window reads
 * (`settingsWriteDocVersion`, `internal/cli/settings_set.go`). A document of
 * another version is refused rather than read as the one this build knows, for
 * `SETTINGS_DOC_VERSION`'s reason one screen over: the CLI's own rule is that
 * members are only ever added within a version.
 */
export const SETTINGS_VIEW_DOC_VERSION = 1

/** SETTINGS_VIEW_VERSION_WORD is the console's own sentence for a dry run this
 * build does not read, with the version the CLI printed interpolated. */
export const SETTINGS_VIEW_VERSION_WORD = 'settings set --dry-run --json is version '

/** SETTINGS_VIEW_SHAPE_BECAUSE is the console's own sentence -- no command
 * prints it -- for a dry run whose member the window draws is not the type the
 * contract gives it. The window is refused whole rather than opened on a
 * coerced member (`settingsView`). */
export const SETTINGS_VIEW_SHAPE_BECAUSE =
  'settings set --dry-run --json carried a member this console does not read: it is not the type the contract gives it, so no window is drawn for it'

/** SETTINGS_VIEW_ABSENT_BECAUSE is the console's own sentence for a dry run that
 * answered no document at all -- and it is the fallback only: a read that was
 * refused shows the CLI's sentence verbatim (`SettingsViewInputs.because`). */
export const SETTINGS_VIEW_ABSENT_BECAUSE = 'the dry run answered no document for this edit'

/**
 * SETTINGS_SHADOWED_SENTENCE is the one sentence D1113 asks for, and it is the
 * **console's** words with nothing interpolated: the two layers it is about are
 * fields of the view and are drawn as fields, so the sentence stays a sentence
 * whether or not a layer name is a string the sanitizer would pass.
 *
 * It is **not a refusal**: the edit proceeds, and the window draws the argv
 * beside it.
 */
export const SETTINGS_SHADOWED_SENTENCE =
  'this edit will not change what a run reads: another layer decides this key'

/** SETTINGS_TICK_LABEL is the widening's tick, in the owner's own words
 * (D1101, D1109). A narrowing has no tick, and its `tick` member is empty. */
export const SETTINGS_TICK_LABEL = 'I understand this raises a limit'

/** SettingsViewButton is which of the window's two buttons is the default: the
 * one Escape, Enter and the close box agree with. A narrowing's is `confirm`;
 * a widening's is `cancel`, so a stray double-click cannot raise a limit
 * (D1109). */
export type SettingsViewButton = 'cancel' | 'confirm'

/**
 * SettingsViewRow is the read's own row as the window needs it, and it is a
 * structural subset of the screen's `ProjectsSettingsRow`: the four members the
 * window draws. The pencil is offered only for a row the document says is
 * writable with a non-empty target layer (D1111), so a row a sensitive setting
 * produced is not one of these -- the drawing dropped it whole, and this type
 * has no member a sensitive value could arrive through.
 */
export interface SettingsViewRow {
  readonly key: string
  /** the value a run reads today, or null when the read carried none. */
  readonly value: string | null
  /** the layer that decides the key today (`wins`). */
  readonly wins: string
  /** the layer this edit is written to (`target_layer`). */
  readonly targetLayer: string
}

/** SettingsViewInputs is everything the window is built from. `dryRun` is the
 * document **as it arrived**, not a decoded view: this module reads its own
 * document and checks its own version and members (the screens' header rule),
 * so a document it cannot vouch for is a refused window rather than a coerced
 * one. `because` is the CLI's own sentence for a dry run that was refused, and
 * `argv` is the exact command line the confirmed edit spawns -- the caller's
 * (`argvFor`'s answer), never a second spelling built here. */
export interface SettingsViewInputs {
  readonly dryRun: unknown
  readonly row: SettingsViewRow
  readonly argv: readonly string[]
  readonly because: string | null
}

/**
 * SettingsView is the whole of what the window is shown for one edit. Every
 * member is either a string the CLI printed, a boolean the CLI printed, or the
 * console's own shape for the answer (which button is the default, whether the
 * tick gates Confirm).
 *
 * `sentence` is `SETTINGS_SHADOWED_SENTENCE` exactly when the layer written is
 * not the layer that wins, and `''` otherwise -- "present only when they
 * differ", as D1113 words it, so the window draws one line or none rather than
 * deciding for itself whether to.
 */
export interface SettingsView {
  readonly key: string
  readonly oldValue: string
  readonly newValue: string
  readonly layer: string
  readonly wins: string
  readonly sentence: string
  readonly widening: boolean
  readonly why: string
  readonly argv: readonly string[]
  readonly defaultButton: SettingsViewButton
  readonly tick: string
  readonly confirmNeedsTick: boolean
}

/** SettingsViewRead is the built view, or the reason there is no window. */
export type SettingsViewRead =
  | { readonly ok: true; readonly view: SettingsView }
  | { readonly ok: false; readonly because: string }

/** SettingsDryRun is P3's dry-run document (`settingsWriteDoc`), read
 * strictly: the ten members the contract gives it, and no others. */
export interface SettingsDryRun {
  readonly key: string
  readonly layer: string
  readonly path: string
  readonly changed: boolean
  readonly widening: boolean
  readonly why: string
  readonly old: string
  readonly new: string
  readonly refusal: string
}

/** textAt is a member that is a JSON string, or null. */
function textAt(raw: unknown): string | null {
  return typeof raw === 'string' ? raw : null
}

/**
 * dryRunAt reads the dry run's document.
 *
 * **Unknown keys are ignored and types are strict** -- the owner's ruling of
 * 2026-10-30, which `settingsRowAt` applies to the read one screen over,
 * applied here for the same reason. The two members that decide the window's
 * *shape* are `widening` and the values it draws: a `widening` spelled `"true"`
 * would shape a write that raises a limit as a one-click edit, an `old` that is
 * a number would draw a value the CLI never printed, and a `changed` that is
 * `"false"` would be read as true. Each of those is a claim the CLI never made,
 * so the whole document is refused and the window is not drawn
 * (`SETTINGS_VIEW_SHAPE_BECAUSE`).
 */
function dryRunAt(document: Record<string, unknown>): SettingsDryRunRefusal | SettingsDryRun {
  if (document['version'] !== SETTINGS_VIEW_DOC_VERSION) {
    return {
      ok: false,
      because: `${SETTINGS_VIEW_VERSION_WORD}${String(document['version'])} and this console reads version ${SETTINGS_VIEW_DOC_VERSION}`,
    }
  }
  const key = textAt(document['key'])
  const layer = textAt(document['layer'])
  const path = textAt(document['path'])
  const why = textAt(document['why'])
  const old = textAt(document['old'])
  const newValue = textAt(document['new'])
  const refusal = textAt(document['refusal'])
  const changed = document['changed']
  const widening = document['widening']
  if (
    key === null ||
    layer === null ||
    path === null ||
    why === null ||
    old === null ||
    newValue === null ||
    refusal === null ||
    typeof changed !== 'boolean' ||
    typeof widening !== 'boolean'
  ) {
    return { ok: false, because: SETTINGS_VIEW_SHAPE_BECAUSE }
  }
  return { key, layer, path, changed, widening, why, old, new: newValue, refusal }
}

/** isRefusal is whether `dryRunAt` answered with a reason rather than a
 * document. The two shapes are told apart by the one member a document always
 * carries and a reason never does. */
function isRefusal(read: SettingsDryRunRefusal | SettingsDryRun): read is SettingsDryRunRefusal {
  return 'ok' in read
}

/** SettingsDryRunRefusal is the reason there is no dry run to read. */
interface SettingsDryRunRefusal {
  readonly ok: false
  readonly because: string
}

/** SettingsDryRunRead is the parsed dry run, or the reason there is none. */
export type SettingsDryRunRead = { readonly ok: true; readonly dryRun: SettingsDryRun } | SettingsDryRunRefusal

/**
 * settingsDryRun is the dry run's document read once, for the two callers that
 * must read the **same** document: `settingsView`, which draws the window from
 * it, and the flow that spawns the write (`settingsedit.ts`), which refuses a
 * document that answers for another key or another layer, or that carries a
 * refusal. A reader of its own on that side could answer differently from this
 * one, and the two would then build a window and a write over two readings of
 * one document.
 *
 * A document that is not there at all answers `SETTINGS_VIEW_ABSENT_BECAUSE`:
 * the caller with the CLI's own sentence for a refused dry run substitutes it
 * (`SettingsViewInputs.because`), which is what makes a refusal the CLI's
 * words rather than this console's.
 */
export function settingsDryRun(document: unknown): SettingsDryRunRead {
  if (document === null || typeof document !== 'object' || Array.isArray(document)) {
    return { ok: false, because: SETTINGS_VIEW_ABSENT_BECAUSE }
  }
  const read = dryRunAt(document as Record<string, unknown>)
  return isRefusal(read) ? { ok: false, because: read.because } : { ok: true, dryRun: read }
}

/**
 * settingsView builds the window's content for one edit.
 *
 * `inputs.because` is the CLI's own sentence for a dry run that was refused,
 * and it is shown verbatim: a refusal is the CLI's sentence about a read the
 * operator can act on, and the console authors no refusal of its own -- which
 * is why the documented absence of a document (`because` with no document) is
 * the CLI's sentence and `absent()`'s is only the fallback when there is none.
 */
export function settingsView(inputs: SettingsViewInputs): SettingsViewRead {
  const read = settingsDryRun(inputs.dryRun)
  if (!read.ok) {
    // A dry run that answered **no document** is the CLI's own sentence when
    // there is one to show; every other refusal is this console's, and a shape
    // it cannot decode is never explained away by a sentence about a read.
    const absent = read.because === SETTINGS_VIEW_ABSENT_BECAUSE
    return { ok: false, because: absent ? (inputs.because ?? SETTINGS_VIEW_ABSENT_BECAUSE) : read.because }
  }
  const dry = read.dryRun
  const { row } = inputs
  // D1108: the old value is the dry run's `old`, and the read's `value` when
  // the key is unset in the layer being written -- the dry run prints no old
  // value there, and an empty cell would read as "the old value is the empty
  // string", which is a different fact from "nothing is set here".
  const oldValue = dry.old !== '' ? dry.old : (row.value ?? '')
  const shadowed = row.targetLayer !== row.wins
  return {
    ok: true,
    view: Object.freeze({
      key: dry.key,
      oldValue,
      newValue: dry.new,
      layer: row.targetLayer,
      wins: row.wins,
      sentence: shadowed ? SETTINGS_SHADOWED_SENTENCE : '',
      widening: dry.widening,
      why: dry.why,
      argv: Object.freeze([...inputs.argv]),
      defaultButton: dry.widening ? 'cancel' : 'confirm',
      tick: dry.widening ? SETTINGS_TICK_LABEL : '',
      confirmNeedsTick: dry.widening,
    }),
  }
}

/**
 * settingsConfirmAnswerable is the one rule the tick decides (D1109): a
 * widening's Confirm is answerable only while the tick is set, and a narrowing
 * is one click -- Confirm is the default button and a single deliberate click
 * confirms.
 *
 * It is here rather than in the drawing because it is a rule about the answer
 * and not about the pixels: the window that draws Confirm reads it, and the
 * test that pins "Confirm is disabled until the tick" reads it without a
 * window.
 */
export function settingsConfirmAnswerable(view: SettingsView, ticked: boolean): boolean {
  return !view.confirmNeedsTick || ticked
}
